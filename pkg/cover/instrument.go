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
	"fmt"
	"os"
	"path"
	"path/filepath"
	"text/template"
)

// InjectCountersHandlers generate a file _cover_http_apis.go besides the main.go file
func InjectCountersHandlers(tc TestCover, dest string) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	if err := coverMainTmpl.Execute(f, tc); err != nil {
		return err
	}
	return nil
}

var coverMainTmpl = template.Must(template.New("coverMain").Parse(coverMain))

const coverMain = `
// Code generated by goc system. DO NOT EDIT.

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	_ "embed"

	_cover {{.GlobalCoverVarImportPath | printf "%q"}}

)

//go:embed src.json
var codeSrcBytes []byte

//go:embed template.html
var templateHtml string

// file bytes cache
var fileSrcMap map[string][]byte

var htmlTemplate *template.Template

func init() {
	fileSrcMap = make(map[string][]byte)
	json.Unmarshal(codeSrcBytes, &fileSrcMap)
	htmlTemplate = template.Must(template.New("html").Funcs(template.FuncMap{
		"colors": colors,
	}).Parse(templateHtml))
	go registerHandlers()
}

func loadValues() (map[string][]uint32, map[string][]testing.CoverBlock) {
	var (
		coverCounters = make(map[string][]uint32)
		coverBlocks   = make(map[string][]testing.CoverBlock)
	)

	{{range $i, $pkgCover := .DepsCover}}
	{{range $file, $cover := $pkgCover.Vars}}
	loadFileCover(coverCounters, coverBlocks, {{printf "%q" $cover.File}}, _cover.{{$cover.Var}}.Count[:], _cover.{{$cover.Var}}.Pos[:], _cover.{{$cover.Var}}.NumStmt[:])
	{{end}}
	{{end}}

	{{range $file, $cover := .MainPkgCover.Vars}}
	loadFileCover(coverCounters, coverBlocks, {{printf "%q" $cover.File}}, _cover.{{$cover.Var}}.Count[:], _cover.{{$cover.Var}}.Pos[:], _cover.{{$cover.Var}}.NumStmt[:])
	{{end}}

	return coverCounters, coverBlocks
}

func loadFileCover(coverCounters map[string][]uint32, coverBlocks map[string][]testing.CoverBlock, fileName string, counter []uint32, pos []uint32, numStmts []uint16) {
	if 3*len(counter) != len(pos) || len(counter) != len(numStmts) {
		panic("coverage: mismatched sizes")
	}
	if coverCounters[fileName] != nil {
		// Already registered.
		return
	}
	coverCounters[fileName] = counter
	block := make([]testing.CoverBlock, len(counter))
	for i := range counter {
		block[i] = testing.CoverBlock{
			Line0: pos[3*i+0],
			Col0:  uint16(pos[3*i+2]),
			Line1: pos[3*i+1],
			Col1:  uint16(pos[3*i+2] >> 16),
			Stmts: numStmts[i],
		}
	}
	coverBlocks[fileName] = block
}

func clearValues() {

	{{range $i, $pkgCover := .DepsCover}}
	{{range $file, $cover := $pkgCover.Vars}}
	clearFileCover(_cover.{{$cover.Var}}.Count[:])
	{{end}}
	{{end}}

	{{range $file, $cover := .MainPkgCover.Vars}}
	clearFileCover(_cover.{{$cover.Var}}.Count[:])
	{{end}}

}

func clearFileCover(counter []uint32) {
	for i := range counter {
		counter[i] = 0
	}
}

func registerHandlers() {
	{{if .Singleton}}
	ln, _, err := listen()
	{{else}}
	ln, host, err := listen()
	{{end}}
	if err != nil {
		log.Fatalf("listen failed, err:%v", err)
	}
	{{if not .Singleton}}
	profileAddr := "http://" + host
	if resp, err := registerSelf(profileAddr); err != nil {
		log.Fatalf("register address %v failed, err: %v, response: %v", profileAddr, err, string(resp))
	}

	stopChan := make(chan struct{})
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				keepaliveSelf(profileAddr)
			case <-stopChan:
				return
			}
		}
	}()
	fn := func() {
		close(stopChan)
		var (
			err          error
			profileAddrs []string
			addresses    []string
		)
		if addresses, err = getAllHosts(ln); err != nil {
				log.Fatalf("get all host failed, err: %v", err)
				return
		}
		for _, addr := range addresses {
				profileAddrs = append(profileAddrs, "http://"+addr)
		}
		deregisterSelf(profileAddrs)
	}
	go watchSignal(fn)
	{{end}}

	mux := http.NewServeMux()
	// Coverage reports the current code coverage as a fraction in the range [0, 1].
	// If coverage is not enabled, Coverage returns 0.
	mux.HandleFunc("/v1/cover/coverage", func(w http.ResponseWriter, r *http.Request) {
		counters, _ := loadValues()
		var n, d int64
		for _, counter := range counters {
			for i := range counter {
				if atomic.LoadUint32(&counter[i]) > 0 {
					n++
				}
				d++
			}
		}
		if d == 0 {
			fmt.Fprint(w, 0)
			return
		}
		fmt.Fprintf(w, "%f", float64(n)/float64(d))
	})

	// coverprofile reports a coverage profile with the coverage percentage
	mux.HandleFunc("/v1/cover/profile", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "mode: {{.Mode}}\n")
		counters, blocks := loadValues()
		var active, total int64
		var count uint32
		for name, counts := range counters {
			block := blocks[name]
			for i := range counts {
				stmts := int64(block[i].Stmts)
				total += stmts
				count = atomic.LoadUint32(&counts[i]) // For -mode=atomic.
				if count > 0 {
					active += stmts
				}
				_, err := fmt.Fprintf(w, "%s:%d.%d,%d.%d %d %d\n", name,
					block[i].Line0, block[i].Col0,
					block[i].Line1, block[i].Col1,
					stmts,
					count)
				if err != nil {
					fmt.Fprintf(w, "invalid block format, err: %v", err)
					return
				}
			}
		}
	})

	mux.HandleFunc("/v1/cover/clear", func(w http.ResponseWriter, r *http.Request) {
		clearValues()
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "clear call successfully")
	})

	mux.HandleFunc("/v1/cover/report", func(w http.ResponseWriter, r *http.Request) {
		selfName := filepath.Base(os.Args[0])
		center := {{.Center | printf "%q"}}
    	serviceName := {{.Service | printf "%q"}}
    	if serviceName != "" {
        	selfName = serviceName
    	}
		requestQuery := map[string]interface{}{"service": []string{selfName}}
		if r.URL != nil {
			qq := r.URL.Query()
			requestQuery["force"] = qq.Get("force") == "1"
            requestQuery["coverfile"] = qq["coverfile"]
           	requestQuery["skipfile"] = qq["skipfile"]
		}
		requestBody, _ := json.Marshal(&requestQuery)
		httpResp, err := http.Post(center+"/v1/cover/profile", "application/json", bytes.NewReader(requestBody))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "failed to get cover file, err: %v", err)
			return
		}
		defer httpResp.Body.Close()
		responseBody, err := io.ReadAll(httpResp.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "failed to get cover file, err: %v", err)
			return
		}
		bs, err := htmlOutput(responseBody)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "failed to gen cover report, err: %v", err)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(bs)
	})

	log.Fatal(http.Serve(ln, mux))
}

func registerSelf(address string) ([]byte, error) {
	selfName := filepath.Base(os.Args[0])
    serviceName := {{.Service | printf "%q"}}
    if serviceName != "" {
        selfName = serviceName
    }
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/v1/cover/register?name=%s&address=%s", {{.Center | printf "%q"}}, selfName, address), nil)
	if err != nil {
		log.Fatalf("http.NewRequest failed: %v", err)
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil && isNetworkError(err) {
		log.Printf("[goc][WARN]error occurred:%v, try again", err)
		resp, err = http.DefaultClient.Do(req)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to register into coverage center, err:%v", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body, err:%v", err)
	}

	if resp.StatusCode != 200 {
		err = fmt.Errorf("failed to register into coverage center, response code %d", resp.StatusCode)
	}

	return body, err
}

func keepaliveSelf(address string) ([]byte, error) {
	selfName := filepath.Base(os.Args[0])
    serviceName := {{.Service | printf "%q"}}
    if serviceName != "" {
        selfName = serviceName
    }
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/v1/cover/keepalive?name=%s&address=%s", {{.Center | printf "%q"}}, selfName, address), nil)
	if err != nil {
		log.Fatalf("http.NewRequest failed: %v", err)
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil && isNetworkError(err) {
		log.Printf("[goc][WARN]error occurred:%v, try again", err)
		resp, err = http.DefaultClient.Do(req)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to register into coverage center, err:%v", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body, err:%v", err)
	}

	if resp.StatusCode != 200 {
		err = fmt.Errorf("failed to register into coverage center, response code %d", resp.StatusCode)
	}

	return body, err
}

func deregisterSelf(address []string) ([]byte, error) {
        param := map[string]interface{}{
                "address": address,
        }
        jsonBody, err := json.Marshal(param)
        if err != nil {
                return nil, err
        }
        req, err := http.NewRequest("POST", fmt.Sprintf("%s/v1/cover/remove", {{.Center | printf "%q"}}), bytes.NewReader(jsonBody))
        if err != nil {
                log.Fatalf("http.NewRequest failed: %v", err)
                return nil, err
        }
        req.Header.Set("Content-Type", "application/json")

        resp, err := http.DefaultClient.Do(req)
        if err != nil && isNetworkError(err) {
                log.Printf("[goc][WARN]error occurred:%v, try again", err)
                resp, err = http.DefaultClient.Do(req)
        }
        if err != nil {
                return nil, fmt.Errorf("failed to deregister into coverage center, err:%v", err)
        }
        defer resp.Body.Close()

        body, err := ioutil.ReadAll(resp.Body)
        if err != nil {
                return nil, fmt.Errorf("failed to read response body, err:%v", err)
        }

        if resp.StatusCode != 200 {
                err = fmt.Errorf("failed to deregister into coverage center, response code %d", resp.StatusCode)
        }

        return body, err
}

type CallbackFunc func()

func watchSignal(fn CallbackFunc) {
        // init signal
        c := make(chan os.Signal, 1)
        signal.Notify(c, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
        for {
                si := <-c
                log.Printf("get a signal %s", si.String())
                switch si {
                case syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT:
                        fn()
                        os.Exit(0) // Exit successfully.
                case syscall.SIGHUP:
                default:
                        return
                }
        }
}

func isNetworkError(err error) bool {
	if err == io.EOF {
		return true
	}
	_, ok := err.(net.Error)
	return ok
}

func listen() (ln net.Listener, host string, err error) {
	agentPort := "{{.AgentPort }}"
	if agentPort != "" {
		if ln, err = net.Listen("tcp4", agentPort); err != nil {
			return
		}
		if host, err = getRealHost(ln); err != nil {
			return
		}
	} else {
		// 获取上次使用的监听地址
		if previousAddr := getPreviousAddr(); previousAddr != "" {
			ss := strings.Split(previousAddr, ":")
			// listen on all network interface
			ln, err = net.Listen("tcp4", ":"+ss[len(ss)-1])
			if err == nil {
				host = previousAddr
				return
			}
		}
		if ln, err = net.Listen("tcp4", ":0"); err != nil {
			return
		}
		if host, err = getRealHost(ln); err != nil {
			return 
		}
	}
	go genProfileAddr(host)
	return
}

func getRealHost(ln net.Listener) (host string, err error) {
	adds, err := net.InterfaceAddrs()
	if err != nil {
		return
	}

	var localIPV4 string
	var nonLocalIPV4 string
	for _, addr := range adds {
		if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.To4() != nil {
			if ipNet.IP.IsLoopback() {
				localIPV4 = ipNet.IP.String()
			} else {
				nonLocalIPV4 = ipNet.IP.String()
			}
		}
	}
	if nonLocalIPV4 != "" {
		host = fmt.Sprintf("%s:%d", nonLocalIPV4, ln.Addr().(*net.TCPAddr).Port)
	} else {
		host = fmt.Sprintf("%s:%d", localIPV4, ln.Addr().(*net.TCPAddr).Port)
	}

	return
}

func getAllHosts(ln net.Listener) (hosts []string, err error) {
	adds, err := net.InterfaceAddrs()
	if err != nil {
		return
	}

	var host string
	for _, addr := range adds {
		if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.To4() != nil {
			host = fmt.Sprintf("%s:%d", ipNet.IP.String(), ln.Addr().(*net.TCPAddr).Port)
			hosts = append(hosts, host)
		}
	}
	return
}

func getPreviousAddr() string {
	file, err := os.Open(os.Args[0] + "_profile_listen_addr")
	if err != nil {
		return ""
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	addr, _, _ := reader.ReadLine()
	return string(addr)
}

func genProfileAddr(profileAddr string) {
	fn := os.Args[0] + "_profile_listen_addr"
	f, err := os.OpenFile(fn, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		log.Println(err)
		return
	}
	defer f.Close()

	fmt.Fprintf(f, strings.TrimPrefix(profileAddr, "http://"))
}

func htmlOutput(src []byte) ([]byte, error) {
	profiles, err := ParseProfiles(src)
	if err != nil {
		return nil, err
	}

	var d templateData

	for _, profile := range profiles {
		fn := profile.FileName
		if profile.Mode == "set" {
			d.Set = true
		}
		src, ok := fileSrcMap[profile.FileName]
		if !ok {
			continue
		}

		var buf strings.Builder
		err = htmlGen(&buf, src, profile.Boundaries(src))
		if err != nil {
			return nil, err
		}
		d.Files = append(d.Files, &templateFile{
			Name:     fn,
			Body:     template.HTML(buf.String()),
			Coverage: percentCovered(profile),
		})
	}

	buf := new(bytes.Buffer)
	err = htmlTemplate.Execute(buf, d)
	if err != nil {
		return nil, err
	}


	return buf.Bytes(), nil
}

func percentCovered(p *Profile) float64 {
	var total, covered int64
	for _, b := range p.Blocks {
		total += int64(b.NumStmt)
		if b.Count > 0 {
			covered += int64(b.NumStmt)
		}
	}
	if total == 0 {
		return 0
	}
	return float64(covered) / float64(total) * 100
}

type Profile struct {
	FileName string
	Mode     string
	Blocks   []ProfileBlock
}

type ProfileBlock struct {
	StartLine, StartCol int
	EndLine, EndCol     int
	NumStmt, Count      int
}

type byFileName []*Profile

func (p byFileName) Len() int           { return len(p) }
func (p byFileName) Less(i, j int) bool { return p[i].FileName < p[j].FileName }
func (p byFileName) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

func ParseProfiles(bs []byte) ([]*Profile, error) {
	bytesReader := bytes.NewReader(bs)
	files := make(map[string]*Profile)
	buf := bufio.NewReader(bytesReader)
	s := bufio.NewScanner(buf)
	mode := ""
	for s.Scan() {
		line := s.Text()
		if mode == "" {
			const p = "mode: "
			if !strings.HasPrefix(line, p) || line == p {
				return nil, fmt.Errorf("bad mode line: %v", line)
			}
			mode = line[len(p):]
			continue
		}
		m := lineRe.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("line %q doesn't match expected format: %v", m, lineRe)
		}
		fn := m[1]
		p := files[fn]
		if p == nil {
			p = &Profile{
				FileName: fn,
				Mode:     mode,
			}
			files[fn] = p
		}
		p.Blocks = append(p.Blocks, ProfileBlock{
			StartLine: toInt(m[2]),
			StartCol:  toInt(m[3]),
			EndLine:   toInt(m[4]),
			EndCol:    toInt(m[5]),
			NumStmt:   toInt(m[6]),
			Count:     toInt(m[7]),
		})
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	for _, p := range files {
		sort.Sort(blocksByStart(p.Blocks))
		// Merge samples from the same location.
		j := 1
		for i := 1; i < len(p.Blocks); i++ {
			b := p.Blocks[i]
			last := p.Blocks[j-1]
			if b.StartLine == last.StartLine &&
				b.StartCol == last.StartCol &&
				b.EndLine == last.EndLine &&
				b.EndCol == last.EndCol {
				if b.NumStmt != last.NumStmt {
					return nil, fmt.Errorf("inconsistent NumStmt: changed from %d to %d", last.NumStmt, b.NumStmt)
				}
				if mode == "set" {
					p.Blocks[j-1].Count |= b.Count
				} else {
					p.Blocks[j-1].Count += b.Count
				}
				continue
			}
			p.Blocks[j] = b
			j++
		}
		p.Blocks = p.Blocks[:j]
	}
	// Generate a sorted slice.
	profiles := make([]*Profile, 0, len(files))
	for _, profile := range files {
		profiles = append(profiles, profile)
	}
	sort.Sort(byFileName(profiles))
	return profiles, nil
}

type blocksByStart []ProfileBlock

func (b blocksByStart) Len() int      { return len(b) }
func (b blocksByStart) Swap(i, j int) { b[i], b[j] = b[j], b[i] }
func (b blocksByStart) Less(i, j int) bool {
	bi, bj := b[i], b[j]
	return bi.StartLine < bj.StartLine || bi.StartLine == bj.StartLine && bi.StartCol < bj.StartCol
}

var lineRe = regexp.MustCompile("^(.+):([0-9]+).([0-9]+),([0-9]+).([0-9]+) ([0-9]+) ([0-9]+)$")

func toInt(s string) int {
	i, err := strconv.Atoi(s)
	if err != nil {
		panic(err)
	}
	return i
}

type Boundary struct {
	Offset int     // Location as a byte offset in the source file.
	Start  bool    // Is this the start of a block?
	Count  int     // Event count from the cover profile.
	Norm   float64 // Count normalized to [0..1].
	Index  int     // Order in input file.
}

func (p *Profile) Boundaries(src []byte) (boundaries []Boundary) {
	// Find maximum count.
	max := 0
	for _, b := range p.Blocks {
		if b.Count > max {
			max = b.Count
		}
	}
	// Divisor for normalization.
	divisor := math.Log(float64(max))

	// boundary returns a Boundary, populating the Norm field with a normalized Count.
	index := 0
	boundary := func(offset int, start bool, count int) Boundary {
		b := Boundary{Offset: offset, Start: start, Count: count, Index: index}
		index++
		if !start || count == 0 {
			return b
		}
		if max <= 1 {
			b.Norm = 0.8 // Profile is in "set" mode; we want a heat map. Use cov8 in the CSS.
		} else if count > 0 {
			b.Norm = math.Log(float64(count)) / divisor
		}
		return b
	}

	line, col := 1, 2 // TODO: Why is this 2?
	for si, bi := 0, 0; si < len(src) && bi < len(p.Blocks); {
		b := p.Blocks[bi]
		if b.StartLine == line && b.StartCol == col {
			boundaries = append(boundaries, boundary(si, true, b.Count))
		}
		if b.EndLine == line && b.EndCol == col || line > b.EndLine {
			boundaries = append(boundaries, boundary(si, false, 0))
			bi++
			continue // Don't advance through src; maybe the next block starts here.
		}
		if src[si] == '\n' {
			line++
			col = 0
		}
		col++
		si++
	}
	sort.Sort(boundariesByPos(boundaries))
	return
}

type boundariesByPos []Boundary

func (b boundariesByPos) Len() int      { return len(b) }
func (b boundariesByPos) Swap(i, j int) { b[i], b[j] = b[j], b[i] }
func (b boundariesByPos) Less(i, j int) bool {
	if b[i].Offset == b[j].Offset {
		// Boundaries at the same offset should be ordered according to
		// their original position.
		return b[i].Index < b[j].Index
	}
	return b[i].Offset < b[j].Offset
}
// gen
func htmlGen(w io.Writer, src []byte, boundaries []Boundary) error {
	dst := bufio.NewWriter(w)
	for i := range src {
		for len(boundaries) > 0 && boundaries[0].Offset == i {
			b := boundaries[0]
			if b.Start {
				n := 0
				if b.Count > 0 {
					n = int(math.Floor(b.Norm*9)) + 1
				}
				fmt.Fprintf(dst, "<span class=\"cov%v\" title=\"%v\">", n, b.Count)
			} else {
				dst.WriteString("</span>")
			}
			boundaries = boundaries[1:]
		}
		switch b := src[i]; b {
		case '>':
			dst.WriteString("&gt;")
		case '<':
			dst.WriteString("&lt;")
		case '&':
			dst.WriteString("&amp;")
		case '\t':
			dst.WriteString("        ")
		default:
			dst.WriteByte(b)
		}
	}
	return dst.Flush()
}

// rgb returns an rgb value for the specified coverage value
// between 0 (no coverage) and 10 (max coverage).
func rgb(n int) string {
	if n == 0 {
		return "rgb(192, 0, 0)" // Red
	}
	// Gradient from gray to green.
	r := 128 - 12*(n-1)
	g := 128 + 12*(n-1)
	b := 128 + 3*(n-1)
	return fmt.Sprintf("rgb(%v, %v, %v)", r, g, b)
}

// colors generates the CSS rules for coverage colors.
func colors() template.CSS {
	var buf bytes.Buffer
	for i := 0; i < 11; i++ {
		fmt.Fprintf(&buf, ".cov%v { color: %v }\n", i, rgb(i))
	}
	return template.CSS(buf.String())
}

type templateData struct {
	Files []*templateFile
	Set   bool
}

func (td templateData) PackageName() string {
	if len(td.Files) == 0 {
		return ""
	}
	fileName := td.Files[0].Name
	elems := strings.Split(fileName, "/") // Package path is always slash-separated.
	// Return the penultimate non-empty element.
	for i := len(elems) - 2; i >= 0; i-- {
		if elems[i] != "" {
			return elems[i]
		}
	}
	return ""
}

type templateFile struct {
	Name     string
	Body     template.HTML
	Coverage float64
}

`

var coverParentFileTmpl = template.Must(template.New("coverParentFileTmpl").Parse(coverParentFile))

const coverParentFile = `
// Code generated by goc system. DO NOT EDIT.

package {{.}}

`

var coverParentVarsTmpl = template.Must(template.New("coverParentVarsTmpl").Parse(coverParentVars))

const coverParentVars = `

import (

	{{range $i, $pkgCover := .}}
	_cover{{$i}} {{$pkgCover.Package.ImportPath | printf "%q"}}
	{{end}} 

)

{{range $i, $pkgCover := .}}
{{range $v, $cover := $pkgCover.Vars}}
var {{$v}} = &_cover{{$i}}.{{$cover.Var}}
{{end}}
{{end}}
	
`

func InjectCacheCounters(covers map[string][]*PackageCover, cache map[string]*PackageCover) []error {
	var errs []error
	for k, v := range covers {
		if pkg, ok := cache[k]; ok {
			err := checkCacheDir(pkg.Package.Dir)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			_, pkgName := path.Split(k)
			err = injectCache(v, pkgName, fmt.Sprintf("%s/%s", pkg.Package.Dir, pkg.Package.GoFiles[0]))
			if err != nil {
				errs = append(errs, err)
				continue
			}
		}
	}
	return errs
}

// InjectCacheCounters generate a file _cover_http_apis.go besides the main.go file
func injectCache(covers []*PackageCover, pkg, dest string) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}

	if err := coverParentFileTmpl.Execute(f, pkg); err != nil {
		return err
	}

	if err := coverParentVarsTmpl.Execute(f, covers); err != nil {
		return err
	}
	return nil
}

func checkCacheDir(p string) error {
	_, err := os.Stat(p)
	if os.IsNotExist(err) {
		err := os.Mkdir(p, 0755)
		if err != nil {
			return err
		}
	}
	return nil
}

func injectGlobalCoverVarFile(ci *CoverInfo, content string) error {
	coverFile, err := os.Create(filepath.Join(ci.Target, ci.GlobalCoverVarImportPath, "cover.go"))
	if err != nil {
		return err
	}
	defer coverFile.Close()

	packageName := "package " + filepath.Base(ci.GlobalCoverVarImportPath) + "\n\n"

	_, err = coverFile.WriteString(packageName)
	if err != nil {
		return err
	}
	_, err = coverFile.WriteString(content)

	return err
}
