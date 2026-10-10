package status

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"regexp"
	"strings"
)

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,96}$`)
var publicIPv4 = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)
var publicDomain = regexp.MustCompile(`(?i)(?:https?://|\b(?:[a-z0-9-]+\.)+[a-z]{2,}\b|\b[a-z0-9_+/=-]{40,}\b)`)

func safePublicText(text string) bool {
	if publicDomain.MatchString(text) || strings.Contains(text, "PRIVATE KEY") {
		return false
	}
	for _, word := range publicIPv4.FindAllString(text, -1) {
		if net.ParseIP(word) != nil {
			return false
		}
	}
	for _, word := range strings.FieldsFunc(text, func(r rune) bool {
		return !(r == ':' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F')
	}) {
		if strings.Contains(word, ":") && net.ParseIP(word) != nil {
			return false
		}
	}
	return true
}

var statuses = map[string]bool{"healthy": true, "degraded": true, "down": true, "unknown": true, "untested": true, "maintenance": true}
var summaries = map[string]string{
	"ok": "检测符合预期；结论仅限所列范围。", "http_error": "请求未得到预期响应。", "dns_error": "DNS 查询未得到预期应答。",
	"health_error": "本机健康接口报告异常。", "space_warning": "存储空间达到预警阈值。", "space_critical": "存储空间达到高风险阈值。",
	"probe_error": "检测未完成；不据此推断全部网络故障。", "unconfigured": "尚未配置此项检测。", "pending": "连续失败确认中。", "recovering": "响应已恢复，等待连续成功确认。",
	"untested": "尚未执行此范围的业务验证。", "stale": "没有新鲜观测；未知不代表服务故障。",
	"process_failed": "指定进程或服务未处于运行态。", "task_failed": "最近一次指定任务执行失败。",
	"job_overdue": "指定任务缺少有效期内的成功记录。", "resource_stopped": "指定虚拟资源未处于运行态。",
	"icmp_error": "指定 IPv6 ICMP Echo 未得到匹配应答；不代表 DNS 或业务故障。", "icmp_unavailable": "本机未能提供无需额外权限的 IPv6 ICMP 检测。",
	"tcp_error": "指定 TCP 端口或协议握手未符合预期。", "path_failed": "既有节点探测中存在新鲜失败记录；不代表全部链路故障。",
}

type Config struct {
	Listen        string       `json:"listen"`
	Database      string       `json:"database"`
	RetentionDays int          `json:"retentionDays"`
	Agents        []Agent      `json:"agents"`
	Services      []Definition `json:"services"`
}
type Agent struct {
	ID                string   `json:"id"`
	Label             string   `json:"label"`
	Kind              string   `json:"kind"`
	SecretFile        string   `json:"secretFile,omitempty"`
	SecretHashFile    string   `json:"secretHashFile,omitempty"`
	StaleAfterSeconds int      `json:"staleAfterSeconds"`
	IntervalSeconds   int      `json:"intervalSeconds"`
	Digest            [32]byte `json:"-"`
}
type Definition struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Subtitle          string `json:"subtitle"`
	Group             string `json:"group"`
	CollectorID       string `json:"collectorId"`
	Scope             string `json:"scope"`
	BusinessProbe     bool   `json:"businessProbe,omitempty"`
	StaleAfterSeconds int    `json:"staleAfterSeconds"`
}
type Observation struct {
	ID               string   `json:"id"`
	ServiceID        string   `json:"serviceId"`
	Status           string   `json:"status"`
	Code             string   `json:"code"`
	AgeSeconds       *float64 `json:"ageSeconds"`
	CapturedAt       string   `json:"capturedAt,omitempty"`
	LatencyMS        *float64 `json:"latencyMs"`
	PathMode         string   `json:"pathMode,omitempty"`
	ReadyConnections *int     `json:"readyConnections,omitempty"`
	UsagePercent     *float64 `json:"usagePercent,omitempty"`
}
type Batch struct {
	ID      string        `json:"id"`
	Replay  bool          `json:"replay"`
	Samples []Observation `json:"samples"`
}
type Heartbeat struct {
	ID string `json:"id"`
}
type Point struct {
	At     string   `json:"at"`
	Status string   `json:"status"`
	Value  *float64 `json:"value,omitempty"`
}
type Detail struct {
	Label string `json:"label"`
	Value string `json:"value"`
}
type Service struct {
	Definition
	Status             string   `json:"status"`
	ObservedAt         *string  `json:"observedAt"`
	ObservedAgeSeconds *float64 `json:"observedAgeSeconds"`
	LatencyMS          *float64 `json:"latencyMs"`
	Availability24h    *float64 `json:"availability24h"`
	Availability7d     *float64 `json:"availability7d"`
	Coverage           *float64 `json:"coverage"`
	Summary            string   `json:"summary"`
	ProbeLabel         string   `json:"probeLabel"`
	History            []Point  `json:"history"`
	Detail             []Detail `json:"detail"`
	PathMode           string   `json:"pathMode"`
	ReadyConnections   *int     `json:"readyConnections"`
	UsagePercent       *float64 `json:"usagePercent"`
}
type Collector struct {
	ID                  string   `json:"id"`
	Label               string   `json:"label"`
	Kind                string   `json:"kind"`
	LastHeartbeatAt     *string  `json:"lastHeartbeatAt"`
	HeartbeatAgeSeconds *float64 `json:"heartbeatAgeSeconds"`
	IntervalSeconds     int      `json:"intervalSeconds"`
	StaleAfterSeconds   int      `json:"staleAfterSeconds"`
	Stale               bool     `json:"stale"`
}
type Incident struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Status             string   `json:"status"`
	StartedAt          string   `json:"startedAt"`
	ResolvedAt         *string  `json:"resolvedAt"`
	AffectedServiceIDs []string `json:"affectedServiceIds"`
}
type Metrics struct {
	LatencyTrend  []Point `json:"latencyTrend"`
	CoverageTrend []Point `json:"coverageTrend"`
}
type Snapshot struct {
	Version    int         `json:"version"`
	Source     string      `json:"source"`
	Synthetic  bool        `json:"synthetic"`
	SampledAt  string      `json:"sampledAt"`
	ServerTime string      `json:"serverTime"`
	Disclaimer string      `json:"disclaimer"`
	Collector  Collector   `json:"collector"`
	Collectors []Collector `json:"collectors"`
	Services   []Service   `json:"services"`
	Metrics    Metrics     `json:"metrics"`
	Incidents  []Incident  `json:"incidents"`
}

func (c *Config) Validate() error {
	if c.RetentionDays == 0 {
		c.RetentionDays = 30
	}
	if c.RetentionDays < 7 || c.RetentionDays > 90 || len(c.Agents) < 1 || len(c.Agents) > 20 || len(c.Services) < 1 || len(c.Services) > 64 {
		return errors.New("invalid capacity limits")
	}
	agents := map[string]bool{}
	digests := map[[32]byte]bool{}
	for i := range c.Agents {
		a := &c.Agents[i]
		if !identifier.MatchString(a.ID) || agents[a.ID] || (a.Kind != "family" && a.Kind != "public") || len(a.Label) > 60 || !safePublicText(a.Label) {
			return errors.New("invalid collector definition")
		}
		if a.IntervalSeconds < 10 || a.IntervalSeconds > 3600 || a.StaleAfterSeconds < a.IntervalSeconds*2 || a.StaleAfterSeconds > 86400 {
			return errors.New("invalid collector expiry")
		}
		if (a.SecretFile == "") == (a.SecretHashFile == "") {
			return errors.New("exactly one credential source required")
		}
		path := a.SecretFile
		if a.SecretHashFile != "" {
			path = a.SecretHashFile
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("cannot read collector credential: %s", a.ID)
		}
		value := strings.TrimSpace(string(raw))
		if a.SecretHashFile != "" {
			decoded, err := hex.DecodeString(value)
			if err != nil || len(decoded) != 32 {
				return errors.New("invalid credential digest")
			}
			copy(a.Digest[:], decoded)
		} else {
			if len(value) < 32 || len(value) > 96 || !identifier.MatchString(value) {
				return errors.New("invalid collector credential format")
			}
			a.Digest = sha256.Sum256([]byte(value))
		}
		if digests[a.Digest] {
			return errors.New("collector credentials must be distinct")
		}
		digests[a.Digest] = true
		agents[a.ID] = true
	}
	ids := map[string]bool{}
	for _, d := range c.Services {
		if !identifier.MatchString(d.ID) || ids[d.ID] || !agents[d.CollectorID] || len(d.Name) < 1 || len(d.Name) > 80 || len(d.Subtitle) > 160 || len(d.Scope) > 160 || !safePublicText(d.Name+" "+d.Subtitle+" "+d.Scope) || d.StaleAfterSeconds < 20 || d.StaleAfterSeconds > 172800 {
			return errors.New("invalid service definition")
		}
		if d.Group != "network" && d.Group != "access" && d.Group != "application" && d.Group != "monitoring" {
			return errors.New("invalid group")
		}
		ids[d.ID] = true
	}
	return nil
}
func (c Config) definition(id, agent string) (Definition, bool) {
	for _, d := range c.Services {
		if d.ID == id && d.CollectorID == agent {
			return d, true
		}
	}
	return Definition{}, false
}
func validateBatch(b Batch, c Config, agent string) error {
	if !identifier.MatchString(b.ID) || len(b.Samples) < 1 || len(b.Samples) > 128 {
		return errors.New("invalid batch")
	}
	ids := map[string]bool{}
	services := map[string]bool{}
	for _, o := range b.Samples {
		if !identifier.MatchString(o.ID) || ids[o.ID] || !statuses[o.Status] || summaries[o.Code] == "" || len(o.CapturedAt) > 40 {
			return errors.New("invalid observation")
		}
		if _, ok := c.definition(o.ServiceID, agent); !ok {
			return errors.New("unauthorized service")
		}
		if !b.Replay && services[o.ServiceID] {
			return errors.New("duplicate live service")
		}
		services[o.ServiceID] = true
		ids[o.ID] = true
		if o.AgeSeconds == nil {
			if !b.Replay {
				return errors.New("live sample age required")
			}
		} else if !finiteRange(*o.AgeSeconds, 0, 7776000) {
			return errors.New("invalid sample age")
		}
		if o.LatencyMS != nil && !finiteRange(*o.LatencyMS, 0, 120000) {
			return errors.New("invalid latency")
		}
		if o.UsagePercent != nil && !finiteRange(*o.UsagePercent, 0, 100) {
			return errors.New("invalid usage")
		}
		if o.ReadyConnections != nil && (*o.ReadyConnections < 0 || *o.ReadyConnections > 1000) {
			return errors.New("invalid connections")
		}
		if o.PathMode != "" && o.PathMode != "unknown" && o.PathMode != "direct" && o.PathMode != "relay" && o.PathMode != "mixed" {
			return errors.New("invalid path")
		}
	}
	return nil
}
func finiteRange(v, min, max float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= min && v <= max
}
