package risk

import (
	"fmt"
	"strings"
	"sync"

	"github.com/lionsoul2014/ip2region/binding/golang/xdb"
)

// GeoInfo is the location and carrier of an IP.
type GeoInfo struct {
	Country    string
	Province   string
	City       string
	ISP        string
	Datacenter bool
}

// GeoLookup resolves an IP to GeoInfo. ok is false when unknown.
type GeoLookup interface {
	Lookup(ip string) (GeoInfo, bool)
}

// IP2Region is a GeoLookup backed by an in-memory ip2region xdb file.
type IP2Region struct {
	mu sync.Mutex // Searcher keeps per-search state (io count)
	s  *xdb.Searcher
}

// NewIP2Region loads an ip2region .xdb file fully into memory.
func NewIP2Region(path string) (*IP2Region, error) {
	buf, err := xdb.LoadContentFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("geoip: load %s: %w", path, err)
	}
	hdr, err := xdb.LoadHeaderFromBuff(buf)
	if err != nil {
		return nil, fmt.Errorf("geoip: header: %w", err)
	}
	ver, err := xdb.VersionFromHeader(hdr)
	if err != nil {
		return nil, fmt.Errorf("geoip: version: %w", err)
	}
	s, err := xdb.NewWithBuffer(ver, buf)
	if err != nil {
		return nil, fmt.Errorf("geoip: searcher: %w", err)
	}
	return &IP2Region{s: s}, nil
}

// Lookup implements GeoLookup.
func (g *IP2Region) Lookup(ip string) (GeoInfo, bool) {
	g.mu.Lock()
	region, err := g.s.Search(ip)
	g.mu.Unlock()
	if err != nil || region == "" {
		return GeoInfo{}, false
	}
	info := ParseRegion(region)
	return info, true
}

// ParseRegion parses an ip2region result. Older files use
// "国家|区域|省份|城市|ISP", newer ones "国家|省份|城市|ISP"; "0" means empty.
func ParseRegion(region string) GeoInfo {
	f := strings.Split(region, "|")
	clean := func(i int) string {
		if i < 0 || i >= len(f) || f[i] == "0" {
			return ""
		}
		return strings.TrimSpace(f[i])
	}
	var g GeoInfo
	switch len(f) {
	case 5:
		g = GeoInfo{Country: clean(0), Province: clean(2), City: clean(3), ISP: clean(4)}
	case 4:
		g = GeoInfo{Country: clean(0), Province: clean(1), City: clean(2), ISP: clean(3)}
	default:
		g = GeoInfo{Country: clean(0), ISP: clean(len(f) - 1)}
	}
	g.Datacenter = IsDatacenterISP(g.ISP)
	return g
}

// datacenterISPs are carrier names that indicate cloud/hosting networks rather
// than home or office broadband.
var datacenterISPs = []string{
	"阿里云", "阿里巴巴", "腾讯云", "腾讯", "华为云", "百度云", "京东云", "金山云", "ucloud", "优刻得", "青云",
	"amazon", "aws", "google", "microsoft", "azure", "digitalocean", "linode", "akamai", "vultr",
	"ovh", "hetzner", "oracle", "cloudflare", "choopa", "contabo", "leaseweb", "m247", "datacamp",
	"bandwagon", "racknerd", "it7", "gcore", "zenlayer", "cogent", "fastly", "hostinger", "scaleway",
	"数据中心", "idc", "hosting", "cloud",
}

// IsDatacenterISP reports whether the ISP name looks like a hosting provider.
func IsDatacenterISP(isp string) bool {
	s := strings.ToLower(isp)
	if s == "" {
		return false
	}
	for _, k := range datacenterISPs {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}
