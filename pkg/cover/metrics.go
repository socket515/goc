package cover

import (
	"github.com/wangjia184/sortedset"
	"golang.org/x/tools/cover"
	"path"
	"strconv"
	"sync"
	"time"
)

type MetricsItem struct {
	Covered   int      `json:"covered"`
	Total     int      `json:"total"`
	CoverRate float64  `json:"cover_rate"`
}

type MetricsData struct {
	Name      string                  `json:"name"`
	Covered   int                     `json:"covered"`
	Total     int                     `json:"total"`
	CoverRate float64                 `json:"cover_rate"`
	PkgData   map[string]*MetricsItem `json:"pkg_data"`
	Ts        int64 				  `json:"ts"`
}

func getMetricsData(profiles []*cover.Profile) *MetricsData {
	pkgData := make(map[string]*MetricsItem)
	var total, covered int
	for _, profile := range profiles {
		var covered_, total_ int
		for _, b := range profile.Blocks {
			total_ += b.NumStmt
			if b.Count > 0 {
				covered_ += b.NumStmt
			}
		}
		pkgName := path.Dir(profile.FileName)
		v, ok := pkgData[pkgName]
		if !ok || v == nil {
			v = new(MetricsItem)
			pkgData[pkgName] = v
		}
		covered += covered_
		total += total_
		v.Covered += covered_
		v.Total += total_
	}

	for _, v := range pkgData {
		v.CoverRate = percent(v.Covered, v.Total)
	}

	return &MetricsData{
		Covered: covered,
		Total: total,
		CoverRate: percent(covered, total),
		PkgData: pkgData,
	}
}

type MetricsStore struct {
	mutex        sync.RWMutex
	serviceData *sortedset.SortedSet
}

func NewMetricsStore() *MetricsStore {
	return &MetricsStore{
		serviceData: sortedset.New(),
	}
}

func (m *MetricsStore) SaveMetricsData(md *MetricsData) {
	if md == nil {
		return
	}
	if md.Ts == 0 {
		md.Ts = time.Now().Unix()
	}
	key := strconv.FormatInt(md.Ts, 10)
	m.mutex.Lock()
	defer m.mutex.Unlock()
	var sdata *sortedset.SortedSet
	if snode := m.serviceData.GetByKey(md.Name); snode != nil {
		sdata = snode.Value.(*sortedset.SortedSet)
	} else {
		sdata = sortedset.New()
	}
	sdata.AddOrUpdate(key, sortedset.SCORE(md.Ts), md)
	m.serviceData.AddOrUpdate(md.Name, sortedset.SCORE(time.Now().Unix()), sdata)
}

func (m *MetricsStore) GetMetricsData(name string, beg, end int64) []*MetricsData {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	snode := m.serviceData.GetByKey(name)
	if snode == nil {
		return nil
	}
	sdata := snode.Value.(*sortedset.SortedSet)
	nodes := sdata.GetByScoreRange(sortedset.SCORE(beg), sortedset.SCORE(end), nil)
	data := make([]*MetricsData, 0, len(nodes))
	for _, node := range nodes {
		md := node.Value.(*MetricsData)
		data = append(data, md)
	}
	return data
}

func (m *MetricsStore) ClearOld() {
	// 删除旧的数据
	m.mutex.Lock()
	defer m.mutex.Unlock()
	minTs := time.Now().Add(-24 * time.Hour).Unix()
	nodes := m.serviceData.GetByScoreRange(0, sortedset.SCORE(minTs), nil)
	for _, node := range nodes {
		m.serviceData.Remove(node.Key())
	}
}

func percent(covered, total int) float64 {
	if total == 0 {
		total = 0 // Avoid zero denominator.
	}
	return 100.0 * float64(covered) / float64(total)
}