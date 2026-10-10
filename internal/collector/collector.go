package collector

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PwnKY/bastion-home-status/internal/status"
)

type Config struct {
	ServerURL          string  `json:"serverUrl"`
	SecretFile         string  `json:"secretFile"`
	CAFile             string  `json:"caFile,omitempty"`
	WireGuardTransport bool    `json:"wireguardTransport,omitempty"`
	StateDirectory     string  `json:"stateDirectory"`
	IntervalSeconds    int     `json:"intervalSeconds"`
	MaxQueuedBatches   int     `json:"maxQueuedBatches"`
	MaxQueueBytes      int64   `json:"maxQueueBytes"`
	Checks             []Check `json:"checks"`
}
type Check struct {
	ServiceID         string  `json:"serviceId"`
	Kind              string  `json:"kind"`
	URL               string  `json:"url,omitempty"`
	ProxyURL          string  `json:"proxyUrl,omitempty"`
	Network           string  `json:"network,omitempty"`
	ConnectIP         string  `json:"connectIp,omitempty"`
	Socket            string  `json:"socket,omitempty"`
	MetricURL         string  `json:"metricUrl,omitempty"`
	ExpectedStatus    int     `json:"expectedStatus,omitempty"`
	ExpectedText      string  `json:"expectedText,omitempty"`
	DNSServer         string  `json:"dnsServer,omitempty"`
	Query             string  `json:"query,omitempty"`
	RecordType        string  `json:"recordType,omitempty"`
	TCP               bool    `json:"tcp,omitempty"`
	ExpectedRCode     int     `json:"expectedRCode,omitempty"`
	Path              string  `json:"path,omitempty"`
	RequireSeparateFS bool    `json:"requireSeparateFs,omitempty"`
	WarningPercent    float64 `json:"warningPercent,omitempty"`
	CriticalPercent   float64 `json:"criticalPercent,omitempty"`
	IntervalSeconds   int     `json:"intervalSeconds,omitempty"`
}
type Agent struct {
	Config Config
	client *http.Client
	token  string
	next   map[string]time.Time
}
type Queued struct {
	Batch  status.Batch `json:"batch"`
	BootID string       `json:"bootId"`
	Uptime float64      `json:"uptime"`
}

func ID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		panic("secure randomness unavailable")
	}
	return hex.EncodeToString(raw)
}
func New(c Config) (*Agent, error) {
	parsed, err := url.Parse(c.ServerURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
		return nil, errors.New("invalid reporting origin")
	}
	if parsed.Scheme != "https" {
		ip := net.ParseIP(parsed.Hostname())
		if parsed.Scheme != "http" || ip == nil || (!ip.IsLoopback() && !(c.WireGuardTransport && ip.IsPrivate())) {
			return nil, errors.New("HTTPS required outside explicitly configured private transport")
		}
		if c.WireGuardTransport && !ip.IsLoopback() {
			if err = verifyWireGuardRoute(ip.String()); err != nil {
				return nil, err
			}
		}
	}
	if c.IntervalSeconds < 10 || c.IntervalSeconds > 3600 || len(c.Checks) > 64 || len(c.Checks) == 0 {
		return nil, errors.New("invalid collection budget")
	}
	if c.MaxQueuedBatches == 0 {
		c.MaxQueuedBatches = 2048
	}
	if c.MaxQueueBytes == 0 {
		c.MaxQueueBytes = 16 * 1024 * 1024
	}
	if c.MaxQueuedBatches < 1 || c.MaxQueuedBatches > 10000 || c.MaxQueueBytes < 1024 || c.MaxQueueBytes > 128*1024*1024 {
		return nil, errors.New("invalid queue limits")
	}
	seen := map[string]bool{}
	for _, check := range c.Checks {
		if check.ServiceID == "" || seen[check.ServiceID] || len(check.ServiceID) > 64 || check.IntervalSeconds < 0 || check.IntervalSeconds > 86400 || check.IntervalSeconds > 0 && check.IntervalSeconds < 10 || check.Network != "" && check.Network != "tcp4" && check.Network != "tcp6" || check.ConnectIP != "" && (net.ParseIP(check.ConnectIP) == nil || check.ProxyURL != "" || check.Socket != "") {
			return nil, errors.New("invalid check configuration")
		}
		if check.Kind != "http" && check.Kind != "tailscale" && check.Kind != "tunnel" && check.Kind != "dns" && check.Kind != "disk" && check.Kind != "untested" {
			return nil, errors.New("unsupported check kind")
		}
		seen[check.ServiceID] = true
	}
	raw, err := os.ReadFile(c.SecretFile)
	if err != nil {
		return nil, errors.New("cannot read reporting credential")
	}
	token := strings.TrimSpace(string(raw))
	if len(token) < 32 || len(token) > 96 {
		return nil, errors.New("invalid reporting credential")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxConnsPerHost = 4
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if c.CAFile != "" {
		cert, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, errors.New("cannot read reporting trust file")
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(cert) {
			return nil, errors.New("invalid trust file")
		}
		transport.TLSClientConfig.RootCAs = pool
	}
	if err = os.MkdirAll(c.StateDirectory, 0700); err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	return &Agent{Config: c, client: client, token: token, next: map[string]time.Time{}}, nil
}
func (a *Agent) post(ctx context.Context, path string, payload any) error {
	if a.Config.WireGuardTransport {
		parsed, _ := url.Parse(a.Config.ServerURL)
		if parsed != nil {
			ip := net.ParseIP(parsed.Hostname())
			if ip != nil && !ip.IsLoopback() {
				if err := verifyWireGuardRoute(ip.String()); err != nil {
					return err
				}
			}
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(a.Config.ServerURL, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+a.token)
	response, err := a.client.Do(request)
	if err != nil {
		return errors.New("report transport failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("report rejected (%d)", response.StatusCode)
	}
	var ack struct {
		Accepted bool `json:"accepted"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&ack) != nil || !ack.Accepted {
		return errors.New("invalid report acknowledgement")
	}
	return nil
}
func (a *Agent) Heartbeat(ctx context.Context) error {
	return a.post(ctx, "/api/v1/heartbeat", status.Heartbeat{ID: ID()})
}
func (a *Agent) Collect(ctx context.Context) Queued {
	now := time.Now()
	checks := []Check{}
	for _, c := range a.Config.Checks {
		if !now.Before(a.next[c.ServiceID]) {
			checks = append(checks, c)
			interval := c.IntervalSeconds
			if interval == 0 {
				interval = a.Config.IntervalSeconds
			}
			a.next[c.ServiceID] = now.Add(time.Duration(interval) * time.Second)
		}
	}
	samples := make([]status.Observation, len(checks))
	times := make([]time.Time, len(checks))
	var group sync.WaitGroup
	budget := make(chan struct{}, 4)
	for i, c := range checks {
		group.Add(1)
		go func(i int, c Check) {
			defer group.Done()
			select {
			case budget <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-budget }()
			samples[i] = Probe(ctx, c)
			times[i] = time.Now()
			samples[i].ID = ID()
			samples[i].ServiceID = c.ServiceID
			samples[i].CapturedAt = times[i].UTC().Format(time.RFC3339Nano)
		}(i, c)
	}
	group.Wait()
	finished := time.Now()
	valid := []status.Observation{}
	for i, o := range samples {
		if o.ID == "" {
			continue
		}
		age := finished.Sub(times[i]).Seconds()
		o.AgeSeconds = &age
		valid = append(valid, o)
	}
	boot, uptime := clock()
	return Queued{Batch: status.Batch{ID: ID(), Samples: valid}, BootID: boot, Uptime: uptime}
}
func (q Queued) ForSend(replay bool) status.Batch {
	batch := q.Batch
	batch.Replay = replay
	batch.Samples = append([]status.Observation(nil), q.Batch.Samples...)
	boot, uptime := clock()
	for i := range batch.Samples {
		if boot != "" && boot == q.BootID && uptime >= q.Uptime && q.Batch.Samples[i].AgeSeconds != nil {
			age := *q.Batch.Samples[i].AgeSeconds + uptime - q.Uptime
			batch.Samples[i].AgeSeconds = &age
		} else {
			batch.Samples[i].AgeSeconds = nil
		}
	}
	return batch
}
func (a *Agent) Save(q Queued) error {
	raw, err := json.Marshal(q)
	if err != nil {
		return err
	}
	path := filepath.Join(a.Config.StateDirectory, q.Batch.ID+".json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return a.Prune()
}
func (a *Agent) queue() ([]os.FileInfo, error) {
	entries, err := os.ReadDir(a.Config.StateDirectory)
	if err != nil {
		return nil, err
	}
	files := []os.FileInfo{}
	for _, e := range entries {
		if e.Type().IsRegular() && len(e.Name()) == 37 && strings.HasSuffix(e.Name(), ".json") {
			name := strings.TrimSuffix(e.Name(), ".json")
			decoded, err := hex.DecodeString(name)
			if err != nil || len(decoded) != 16 {
				continue
			}
			info, err := e.Info()
			if err != nil {
				return nil, err
			}
			files = append(files, info)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime().Before(files[j].ModTime()) })
	return files, nil
}
func (a *Agent) Prune() error {
	files, err := a.queue()
	if err != nil {
		return err
	}
	var size int64
	for _, f := range files {
		size += f.Size()
	}
	for len(files) > 0 && (len(files) > a.Config.MaxQueuedBatches || size > a.Config.MaxQueueBytes) {
		f := files[0]
		if err = os.Remove(filepath.Join(a.Config.StateDirectory, f.Name())); err != nil {
			return err
		}
		size -= f.Size()
		files = files[1:]
		log.Print("oldest cached batch dropped: queue budget reached")
	}
	return nil
}
func (a *Agent) Flush(ctx context.Context) error {
	files, err := a.queue()
	if err != nil {
		return err
	}
	for i, f := range files {
		if i >= 8 {
			break
		}
		path := filepath.Join(a.Config.StateDirectory, f.Name())
		if f.Size() > 256*1024 {
			_ = os.Remove(path)
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var q Queued
		if json.Unmarshal(raw, &q) != nil {
			_ = os.Remove(path)
			continue
		}
		if err = a.post(ctx, "/api/v1/ingest", q.ForSend(true)); err != nil {
			return err
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}
func (a *Agent) Round(ctx context.Context) {
	q := a.Collect(ctx)
	if len(q.Batch.Samples) > 0 {
		if err := a.post(ctx, "/api/v1/ingest", q.ForSend(false)); err != nil {
			if err = a.Save(q); err != nil {
				log.Print("could not persist failed batch")
			} else {
				log.Print("report unavailable; observations cached")
			}
		}
	}
	if err := a.Flush(ctx); err != nil {
		log.Print("historical queue remains pending")
	}
}
func (a *Agent) Run(ctx context.Context) {
	var heart sync.WaitGroup
	heart.Add(1)
	go func() {
		defer heart.Done()
		ticker := time.NewTicker(time.Duration(a.Config.IntervalSeconds) * time.Second)
		defer ticker.Stop()
		for {
			if a.Heartbeat(ctx) != nil && ctx.Err() == nil {
				log.Print("heartbeat unavailable")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	defer heart.Wait()
	ticker := time.NewTicker(time.Duration(a.Config.IntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		a.Round(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
