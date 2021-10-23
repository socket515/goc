/*
 Copyright 2020 Qiniu Cloud (qiniu.com)

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package cover

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/wangjia184/sortedset"
	"golang.org/x/tools/cover"
	"html/template"
	"io"
	"io/ioutil"
	"k8s.io/test-infra/gopherage/pkg/cov"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"

	"embed"
)

//go:embed js
var jsDir embed.FS

// LogFile a file to save log.
const LogFile = "goc.log"

type server struct {
	PersistenceFile string
	Store           Store
	Mutex           sync.Mutex
	List            *sortedset.SortedSet
	MetricsStore    *MetricsStore
}

// NewFileBasedServer new a file based server with persistenceFile
func NewFileBasedServer(persistenceFile string) (*server, error) {
	store, err := NewFileStore(persistenceFile)
	if err != nil {
		return nil, err
	}
	return &server{
		PersistenceFile: persistenceFile,
		Store:           store,
		List: sortedset.New(),
		MetricsStore: NewMetricsStore(),
	}, nil
}

// NewMemoryBasedServer new a memory based server without persistenceFile
func NewMemoryBasedServer() *server {
	return &server{
		Store: NewMemoryStore(),
		List: sortedset.New(),
		MetricsStore: NewMetricsStore(),
	}
}

// Run starts coverage host center
func (s *server) Run(logfile, port string) {
	var w io.Writer = os.Stdout
	if logfile != "" {
		f, err := os.Create(LogFile)
		if err != nil {
			log.Fatalf("failed to create log file %s, err: %v", LogFile, err)
		}
		w = io.MultiWriter(f, w)
	}

	// read addr from store
	score := sortedset.SCORE(time.Now().Unix())
	allInfo := s.Store.GetAll()
	for _, addrs := range allInfo {
		for _, addr := range addrs {
			s.List.AddOrUpdate(addr, score, struct {}{})
		}
	}
	// both log to stdout and file by default
	go s.KeepAliveWorker()
	go s.MetricsWorker()
	r := s.Route(w)
	log.Fatal(r.Run(port))
}

func (s *server) KeepAliveWorker() {
	ticker := time.NewTicker(5 * time.Minute)
	for {
		select {
		case <-ticker.C:
			min := time.Now().Add(-5 * time.Minute).Unix()
			s.Mutex.Lock()
			nodes := s.List.GetByScoreRange(0, sortedset.SCORE(min), nil)
			for _, node := range nodes {
				s.List.Remove(node.Key())
			}
			s.Mutex.Unlock()
			for _, node := range nodes {
				addr := node.Key()
				log.Infof("address:%s lost connect", addr)
				// clean lost connect
				s.Store.Remove(addr)
			}
		}
	}
}

func (s *server) MetricsWorker() {
	ticker := time.NewTicker(time.Minute)
	for {
		select {
		case now := <-ticker.C:
			ts := now.Unix()
			allInfos := s.Store.GetAll()
			for name, addrList := range allInfos {
				profiles, err := getProfile(addrList, true, nil, nil)
				if err != nil {
					log.Warnf("failed to get profile, err=%v, name=%v", err, name)
					continue
				}
				md := getMetricsData(profiles)
				md.Name = name
				md.Ts = ts
				s.MetricsStore.SaveMetricsData(md)
			}
			s.MetricsStore.ClearOld()
		}
	}
}

// Router init goc server engine
func (s *server) Route(w io.Writer) *gin.Engine {
	if w != nil {
		gin.DefaultWriter = w
	}
	r := gin.Default()
	// api to show the registered services
	r.StaticFile("static", "./"+s.PersistenceFile)
	r.StaticFS("static", http.FS(jsDir))

	v1 := r.Group("/v1")
	{
		v1.POST("/cover/keepalive", s.keepalive)
		v1.POST("/cover/register", s.registerService)
		v1.GET("/cover/profile", s.profile)
		v1.POST("/cover/profile", s.profile)
		v1.POST("/cover/clear", s.clear)
		v1.POST("/cover/init", s.initSystem)
		v1.GET("/cover/list", s.listServices)
		v1.POST("/cover/remove", s.removeServices)
		v1.GET("/cover/report", s.genCoverReport)
		v1.GET("/cover/metrics", s.getMetrics)
	}
	report := r.Group("goc-coverage-report")
	report.GET("/", s.getReport)
	return r
}

// ServiceUnderTest is a entry under being tested
type ServiceUnderTest struct {
	Name    string `form:"name" json:"name" binding:"required"`
	Address string `form:"address" json:"address" binding:"required"`
}

// ProfileParam is param of profile API
type ProfileParam struct {
	Force             bool     `form:"force" json:"force"`
	Service           []string `form:"service" json:"service"`
	Address           []string `form:"address" json:"address"`
	CoverFilePatterns []string `form:"coverfile" json:"coverfile"`
	SkipFilePatterns  []string `form:"skipfile" json:"skipfile"`
}

//listServices list all the registered services
func (s *server) listServices(c *gin.Context) {
	services := s.Store.GetAll()
	c.JSON(http.StatusOK, services)
}

func (s *server) keepalive(c *gin.Context) {
	s.registerService(c)
}

func (s *server) registerService(c *gin.Context) {
	var service ServiceUnderTest
	if err := c.ShouldBind(&service); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	u, err := url.Parse(service.Address)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	realIP := c.ClientIP()
	// only for IPV4
	// refer: https://github.com/qiniu/goc/issues/177
	if net.ParseIP(realIP).To4() != nil && host != realIP {
		log.Printf("the registered host %s of service %s is different with the real one %s, here we choose the real one", service.Name, host, realIP)
		service.Address = fmt.Sprintf("http://%s:%s", realIP, port)
	}

	address := s.Store.Get(service.Name)
	if !contains(address, service.Address) {
		if err := s.Store.Add(service); err != nil && err != ErrServiceAlreadyRegistered {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	s.Mutex.Lock()
	defer s.Mutex.Unlock()
	// 记录注册时间
	s.List.AddOrUpdate(service.Address, sortedset.SCORE(time.Now().Unix()), struct {}{})

	c.JSON(http.StatusOK, gin.H{"result": "success"})
	return
}

// profile API examples:
// POST /v1/cover/profile
// { "force": "true", "service":["a","b"], "address":["c","d"],"coverfile":["e","f"] }
func (s *server) profile(c *gin.Context) {
	var body ProfileParam
	if err := c.ShouldBind(&body); err != nil {
		c.JSON(http.StatusExpectationFailed, gin.H{"error": err.Error()})
		return
	}

	allInfos := s.Store.GetAll()
	filterAddrList, err := filterAddrs(body.Service, body.Address, body.Force, allInfos)
	if err != nil {
		c.JSON(http.StatusExpectationFailed, gin.H{"error": err.Error()})
		return
	}

	merged, err := getProfile(filterAddrList, body.Force, body.CoverFilePatterns, body.SkipFilePatterns)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if err := cov.DumpProfile(merged, c.Writer); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
}

// getProfile get profile
func getProfile(addrList []string, force bool, coverfile, skipfile []string) ([]*cover.Profile, error) {
	var mergedProfiles = make([][]*cover.Profile, 0)
	for _, addr := range addrList {
		pp, err := NewWorker(addr).Profile(ProfileParam{})
		if err != nil {
			if force {
				log.Warnf("get profile from [%s] failed, error: %s", addr, err.Error())
				continue
			}
			return nil, fmt.Errorf("failed to get profile from %s, error %s", addr, err.Error())
		}

		profile, err := convertProfile(pp)
		if err != nil {
			return nil, err
		}
		mergedProfiles = append(mergedProfiles, profile)
	}

	if len(mergedProfiles) == 0 {
		return nil, errors.New("no profiles")
	}

	merged, err := cov.MergeMultipleProfiles(mergedProfiles)
	if err != nil {
		return nil, err
	}

	if len(coverfile) > 0 {
		merged, err = filterProfile(coverfile, merged)
		if err != nil {
			return nil, fmt.Errorf("failed to filter profile based on the patterns: %v, error: %v", coverfile, err)
		}
	}

	if len(skipfile) > 0 {
		merged, err = skipProfile(skipfile, merged)
		if err != nil {
			return nil, fmt.Errorf("failed to skip profile based on the patterns: %v, error: %v", skipfile, err)
		}
	}
	return merged, nil
}

// filterProfile filters profiles of the packages matching the coverFile pattern
func filterProfile(coverFile []string, profiles []*cover.Profile) ([]*cover.Profile, error) {
	var out = make([]*cover.Profile, 0)
	for _, profile := range profiles {
		for _, pattern := range coverFile {
			matched, err := regexp.MatchString(pattern, profile.FileName)
			if err != nil {
				return nil, fmt.Errorf("filterProfile failed with pattern %s for profile %s, err: %v", pattern, profile.FileName, err)
			}
			if matched {
				out = append(out, profile)
				break // no need to check again for the file
			}
		}
	}

	return out, nil
}

// skipProfile skips profiles of the packages matching the skipFile pattern
func skipProfile(skipFile []string, profiles []*cover.Profile) ([]*cover.Profile, error) {
	var out = make([]*cover.Profile, 0)
	for _, profile := range profiles {
		var shouldSkip bool
		for _, pattern := range skipFile {
			matched, err := regexp.MatchString(pattern, profile.FileName)
			if err != nil {
				return nil, fmt.Errorf("filterProfile failed with pattern %s for profile %s, err: %v", pattern, profile.FileName, err)
			}

			if matched {
				shouldSkip = true
				break // no need to check again for the file
			}
		}

		if !shouldSkip {
			out = append(out, profile)
		}
	}

	return out, nil
}

func (s *server) clear(c *gin.Context) {
	var body ProfileParam
	if err := c.ShouldBind(&body); err != nil {
		c.JSON(http.StatusExpectationFailed, gin.H{"error": err.Error()})
		return
	}
	svrsUnderTest := s.Store.GetAll()
	filterAddrList, err := filterAddrs(body.Service, body.Address, true, svrsUnderTest)
	if err != nil {
		c.JSON(http.StatusExpectationFailed, gin.H{"error": err.Error()})
		return
	}
	for _, addr := range filterAddrList {
		pp, err := NewWorker(addr).Clear(ProfileParam{})
		if err != nil {
			c.JSON(http.StatusExpectationFailed, gin.H{"error": err.Error()})
			return
		}
		fmt.Fprintf(c.Writer, "Register service %s coverage counter %s", addr, string(pp))
	}

}

func (s *server) initSystem(c *gin.Context) {
	if err := s.Store.Init(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, "")
}

func (s *server) removeServices(c *gin.Context) {
	var body ProfileParam
	if err := c.ShouldBind(&body); err != nil {
		c.JSON(http.StatusExpectationFailed, gin.H{"error": err.Error()})
		return
	}
	svrsUnderTest := s.Store.GetAll()
	filterAddrList, err := filterAddrs(body.Service, body.Address, true, svrsUnderTest)
	if err != nil {
		c.JSON(http.StatusExpectationFailed, gin.H{"error": err.Error()})
		return
	}
	for _, addr := range filterAddrList {
		err := s.Store.Remove(addr)
		if err != nil {
			c.JSON(http.StatusExpectationFailed, gin.H{"error": err.Error()})
			return
		}
		fmt.Fprintf(c.Writer, "Register service %s removed from the center.", addr)
	}
}

func (s *server) genCoverReport(c *gin.Context) {
	module := c.Query("module")
	if module == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing module"})
		return
	}
	addrs := s.Store.Get(module)
	if len(addrs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "can not find module"})
		return
	}
	force := c.Query("force") == "1"
	coverfile := c.QueryArray("coverfile")
	skipfile := c.QueryArray("skipfile")
	fm := c.Query("format")
	switch fm {
	case "html":
		s.genHtmlCoverReport(c, addrs, force, coverfile, skipfile)
	case "pkg":
		s.genPackageCoverReport(c, addrs, force, coverfile, skipfile)
	default:
		s.genHtmlCoverReport(c, addrs, force, coverfile, skipfile)
	}
}

func (s *server) getReport(c *gin.Context) {
	modules := c.QueryArray("module")
	if len(modules) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing module"})
		return
	}
	var beg, end int64
	var err error
	begStr := c.Query("beg")
	if begStr != "" {
		beg, err = strconv.ParseInt(begStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "beg field must be int"})
			return
		}
	}
	endStr := c.Query("end")
	if endStr != "" {
		end, err = strconv.ParseInt(endStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "end field must be int"})
			return
		}
	}
	if beg > end {
		c.JSON(http.StatusBadRequest, gin.H{"error": "end > beg"})
		return
	}
	if beg == 0 && end == 0 {
		now := time.Now()
		beg = now.Add(-1* time.Hour).Unix()
		end = now.Unix()
	}
	// 时间戳取整点
	beg = beg - (beg % 60)
	end = end - (end % 60)
	var tsList []int64
	for i := beg; i <= end; i = i + 60 {
		tsList = append(tsList, i)
	}
	data := make(map[string][]float64)
	for _, module := range modules {
		ms := s.MetricsStore.GetMetricsData(module, beg, end+20)
		ln := len(ms)
		coverage := make([]float64, len(tsList))
		for i, j:= 0, 0; i < len(tsList); i++ {
			if j >= ln || math.Abs(float64(ms[j].Ts - tsList[i])) > 30 {
				continue
			}
			coverage[i] = ms[j].CoverRate
			j++
		}
		data[module] = coverage
	}

	var coverReportHtmlTmpl = template.Must(template.New("coverReportHtml").Parse(coverReportHtml))
	coverReportHtmlTmpl.Execute(c.Writer, map[string]interface{}{
		"tsList": tsList,
		"data": data,
	})
	return
}

func (s *server) genHtmlCoverReport(c *gin.Context, addrs []string, force bool, coverfile, skipfile []string) {
	params := url.Values{}
	if force {
		params.Add("force", "1")
	}
	for _, p := range coverfile {
		params.Add("coverfile", p)
	}
	for _, p := range skipfile {
		params.Add("skipfile", p)
	}
	requestUrl := addrs[0] + "/v1/cover/report"
	if len(params) > 0 {
		requestUrl += "?" + params.Encode()
	}
	resp, err := http.Get(requestUrl)
	if err != nil {
		c.JSON(http.StatusExpectationFailed, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	bs, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusExpectationFailed, gin.H{"error": err.Error()})
		return
	}

	c.Writer.WriteHeader(resp.StatusCode)
	_, err = c.Writer.Write(bs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func (s *server) genPackageCoverReport(c *gin.Context, addrs []string, force bool, coverfile, skipfile []string) {
	// gen profile
	merged, err := getProfile(addrs, force, coverfile, skipfile)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// calc
	md := getMetricsData(merged)
	data := make(map[string]string, len(md.PkgData) + 1)
	if md.Total == 0 {
		c.Writer.WriteHeader(http.StatusOK)
		_, err = c.Writer.Write([]byte(`{"total": "0.0%"}`))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
	}

	for pkgName, coverData := range md.PkgData {
		data[pkgName] = fmt.Sprintf("%.2f%%", coverData.CoverRate)
	}
	data["total"] = fmt.Sprintf("%.2f%%", md.CoverRate)
	bs, _ := json.MarshalIndent(data, "", "    ")
	c.Writer.WriteHeader(http.StatusOK)
	_, err = c.Writer.Write(bs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func (s *server) getMetrics(c *gin.Context) {
	module := c.Query("module")
	if module == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing module"})
		return
	}
	var beg, end int64
	var err error
	begStr := c.Query("beg")
	if begStr != "" {
		beg, err = strconv.ParseInt(begStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "beg field must be int"})
			return
		}
	}
	endStr := c.Query("end")
	if endStr != "" {
		end, err = strconv.ParseInt(endStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "end field must be int"})
			return
		}
	}
	if beg > end {
		c.JSON(http.StatusBadRequest, gin.H{"error": "end > beg"})
		return
	}
	if beg == 0 && end == 0 {
		now := time.Now()
		beg = now.Add(-15 * time.Minute).Unix()
		end = now.Unix()
	}
	mds := s.MetricsStore.GetMetricsData(module, beg, end)
	data := make(map[string]interface{})
	data["data"] = mds
	bs, _ := json.Marshal(&data)
	c.Writer.WriteHeader(http.StatusOK)
	_, err = c.Writer.Write(bs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func convertProfile(p []byte) ([]*cover.Profile, error) {
	// Annoyingly, ParseProfiles only accepts a filename, so we have to write the bytes to disk
	// so it can read them back.
	// We could probably also just give it /dev/stdin, but that'll break on Windows.
	tf, err := ioutil.TempFile("", "")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file, err: %v", err)
	}
	defer tf.Close()
	defer os.Remove(tf.Name())
	if _, err := io.Copy(tf, bytes.NewReader(p)); err != nil {
		return nil, fmt.Errorf("failed to copy data to temp file, err: %v", err)
	}

	return cover.ParseProfiles(tf.Name())
}

func contains(arr []string, str string) bool {
	for _, element := range arr {
		if str == element {
			return true
		}
	}
	return false
}

// filterAddrs filter address list by given service and address list
func filterAddrs(serviceList, addressList []string, force bool, allInfos map[string][]string) (filterAddrList []string, err error) {
	addressAll := []string{}
	for _, addr := range allInfos {
		addressAll = append(addressAll, addr...)
	}

	if len(serviceList) != 0 && len(addressList) != 0 {
		return nil, fmt.Errorf("use 'service' flag and 'address' flag at the same time may cause ambiguity, please use them separately")
	}

	// Add matched services to map
	for _, name := range serviceList {
		if addr, ok := allInfos[name]; ok {
			filterAddrList = append(filterAddrList, addr...)
			continue // jump to match the next service
		}
		if !force {
			return nil, fmt.Errorf("service [%s] not found", name)
		}
		log.Warnf("service [%s] not found", name)
	}

	// Add matched addresses to map
	for _, addr := range addressList {
		if contains(addressAll, addr) {
			filterAddrList = append(filterAddrList, addr)
			continue
		}
		if !force {
			return nil, fmt.Errorf("address [%s] not found", addr)
		}
		log.Warnf("address [%s] not found", addr)
	}

	if len(addressList) == 0 && len(serviceList) == 0 {
		filterAddrList = addressAll
	}

	// Return all services when all param is nil
	return filterAddrList, nil
}

const coverReportHtml = `
<!DOCTYPE html>
<html lang="en">

<head>
    <meta charset="UTF-8" />
    <meta http-equiv="X-UA-Compatible" content="IE=edge" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <script src="../static/js/highcharts.js"></script>
    <script src="../static/js/dateFormat.js"></script>
    <title>goc-coverage-report</title>
</head>

<body style="max-width: 100vw; max-height: calc(100vh - 20px)">
<div id="container" style="max-width: 100vw; height: calc(100vh - 20px)"></div>
<script>
    var ts = [{{range $i, $el := .tsList}}{{if $i}},{{end}}{{$el}}{{end}}];
    var coverage = {
		{{range $key, $value := .data}}
        {{$key}}: [{{range $i, $el := $value}}{{if $i}},{{end}}{{$el}}{{end}}],
        {{end}}
    };
</script>
<script>
    var chart = Highcharts.chart('container', {
        title: {
            text: '各模块单元测试覆盖率',
        },
        subtitle: {
            text: '覆盖率变化',
        },
        yAxis: {
            title: {
                text: '覆盖率',
            },
        },
        plotOptions: {
            series: {
                label: {
                    connectorAllowed: false,
                },
            },
        },
        xAxis: {
            categories: ts.map((x) => dateFormat({ t: x * 1000, format: 'YYYY-MM-DD hh:mm:ss' })),
        },
        series: Object.keys(coverage).map((key) => ({
            name: key,
            data: coverage[key].map((x) => parseFloat(x.toFixed(2))),
        })),
        tooltip: {
            shared: true,
        },
        responsive: {
            rules: [
                {
                    condition: {
                        maxWidth: 500,
                    },
                    chartOptions: {
                        legend: {
                            layout: 'horizontal',
                            align: 'center',
                            verticalAlign: 'bottom',
                        },
                    },
                },
            ],
        },
        credits: {
            enabled: false,
        },
    });
</script>
</body>
</html>
`
