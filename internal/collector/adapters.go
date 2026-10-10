package collector

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PwnKY/bastion-home-status/internal/status"
)

var unitName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.@:-]{0,150}\.(service|timer|mount)$`)

func validateAdapter(c Check) error {
	switch c.Kind {
	case "http", "tailscale", "tunnel", "dns", "disk", "untested":
	case "unit", "job":
		if !unitName.MatchString(c.Unit) {
			return errors.New("invalid read-only unit")
		}
	case "tcp":
		host, port, err := net.SplitHostPort(c.Target)
		n, e := strconv.Atoi(port)
		if err != nil || e != nil || host == "" || n < 1 || n > 65535 {
			return errors.New("invalid TCP target")
		}
	case "pve":
		if c.ResourceType != "node" && c.ResourceType != "qemu" && c.ResourceType != "lxc" {
			return errors.New("invalid PVE resource type")
		}
		if c.ResourceType != "node" && (c.ResourceID < 100 || c.ResourceID > 999999999) {
			return errors.New("invalid PVE resource id")
		}
	case "peer-pinger", "peer-poll":
		if c.Path == "" {
			return errors.New("missing peer evidence file")
		}
	default:
		return errors.New("unsupported check kind")
	}
	if c.MaxAgeSeconds < 0 || c.MaxAgeSeconds > 604800 {
		return errors.New("invalid evidence age limit")
	}
	return nil
}

func unknown(code string) status.Observation {
	return status.Observation{Status: "unknown", Code: code, PathMode: "unknown"}
}
func healthy() status.Observation {
	return status.Observation{Status: "healthy", Code: "ok", PathMode: "unknown"}
}

// These commands are fixed read-only APIs, never a configurable shell command.
// Output and runtime are bounded; neither command output nor addresses are published.
type boundedOutput struct {
	bytes.Buffer
	limit int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("read-only output budget exceeded")
	}
	return b.Buffer.Write(p)
}
func readCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	b := &boundedOutput{limit: 1024 * 1024}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = b
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
func unitProbe(ctx context.Context, c Check) status.Observation {
	if !unitName.MatchString(c.Unit) {
		return unknown("probe_error")
	}
	raw, err := readCommand(ctx, "systemctl", "show", "--no-pager", c.Unit, "--property=LoadState,ActiveState,SubState,Result,ExecMainStatus,ExecMainExitTimestampMonotonic")
	if err != nil {
		return unknown("probe_error")
	}
	_, uptime := clock()
	return unitResult(raw, c, uptime)
}
func unitResult(raw []byte, c Check, uptime float64) status.Observation {
	p := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			p[k] = v
		}
	}
	if p["LoadState"] == "not-found" {
		return status.Observation{Status: "untested", Code: "unconfigured"}
	}
	if p["LoadState"] != "loaded" {
		return unknown("probe_error")
	}
	if c.Kind == "unit" {
		if p["ActiveState"] == "active" {
			return healthy()
		}
		if p["ActiveState"] == "activating" || p["ActiveState"] == "deactivating" {
			return unknown("pending")
		}
		return status.Observation{Status: "down", Code: "process_failed"}
	}
	if p["ActiveState"] == "activating" {
		return unknown("pending")
	}
	if p["Result"] != "success" || p["ExecMainStatus"] != "0" {
		return status.Observation{Status: "down", Code: "task_failed"}
	}
	stamp, err := strconv.ParseFloat(p["ExecMainExitTimestampMonotonic"], 64)
	if err != nil || stamp <= 0 || stamp/1e6 > uptime {
		return unknown("probe_error")
	}
	maxAge := c.MaxAgeSeconds
	if maxAge == 0 {
		maxAge = 86400
	}
	if uptime-stamp/1e6 >= float64(maxAge) {
		return status.Observation{Status: "degraded", Code: "job_overdue"}
	}
	// A completed oneshot being inactive is expected, not a stopped service.
	return healthy()
}
func pveProbe(ctx context.Context, c Check) status.Observation {
	raw, err := readCommand(ctx, "pvesh", "get", "/cluster/resources", "--output-format", "json")
	if err != nil {
		return unknown("probe_error")
	}
	return pveResult(raw, c)
}
func pveResult(raw []byte, c Check) status.Observation {
	var entries []struct {
		Type   string `json:"type"`
		VMID   int    `json:"vmid"`
		Status string `json:"status"`
	}
	if json.Unmarshal(raw, &entries) != nil {
		return unknown("probe_error")
	}
	matches := 0
	state := ""
	for _, e := range entries {
		if e.Type == c.ResourceType && (c.ResourceType == "node" || e.VMID == c.ResourceID) {
			matches++
			state = e.Status
		}
	}
	if matches != 1 {
		return unknown("probe_error")
	}
	if state == "running" || c.ResourceType == "node" && state == "online" {
		return healthy()
	}
	if state == "stopped" || state == "offline" {
		return status.Observation{Status: "down", Code: "resource_stopped"}
	}
	return unknown("probe_error")
}
func tcpProbe(ctx context.Context, c Check) status.Observation {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	began := time.Now()
	conn, err := (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, "tcp", c.Target)
	if err != nil {
		return status.Observation{Status: "down", Code: "tcp_error"}
	}
	defer conn.Close()
	if c.ExpectedText != "" {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		b := make([]byte, 512)
		n, err := conn.Read(b)
		if err != nil || !strings.HasPrefix(string(b[:n]), c.ExpectedText) {
			return status.Observation{Status: "down", Code: "tcp_error"}
		}
	}
	o := healthy()
	ms := float64(time.Since(began).Microseconds()) / 1000
	o.LatencyMS = &ms
	return o
}

// Consumes existing event-triggered ping evidence. It NEVER invokes tailscale ping.
func peerProbe(c Check) status.Observation {
	file, err := os.Open(c.Path)
	if err != nil {
		return unknown("probe_error")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	if err != nil || len(raw) > 1024*1024 {
		return unknown("probe_error")
	}
	return peerResult(raw, c, time.Now())
}
func peerResult(raw []byte, c Check, now time.Time) status.Observation {
	var s struct {
		Version     int    `json:"version"`
		Initialized bool   `json:"initialized"`
		Poll        string `json:"lastSuccessfulPollAt"`
		Peers       map[string]struct {
			Present bool   `json:"present"`
			Online  bool   `json:"online"`
			At      string `json:"lastPingAt"`
			OK      *bool  `json:"lastPingOK"`
			Path    string `json:"lastPingPath"`
		} `json:"peers"`
	}
	if json.Unmarshal(raw, &s) != nil || s.Version != 1 || !s.Initialized {
		return unknown("probe_error")
	}
	poll, err := time.Parse(time.RFC3339Nano, s.Poll)
	if err != nil || poll.After(now.Add(5*time.Second)) || now.Sub(poll) >= 30*time.Second {
		return unknown("stale")
	}
	if c.Kind == "peer-poll" {
		// Confirms only successful local status polling, not server control sync.
		return healthy()
	}
	maxAge := c.MaxAgeSeconds
	if maxAge == 0 {
		maxAge = 180
	}
	count, failed := 0, 0
	direct, relay := false, false
	at := time.Time{}
	fingerprints := []string{}
	for key, p := range s.Peers {
		if !p.Present || !p.Online || p.OK == nil {
			continue
		}
		stamp, err := time.Parse(time.RFC3339Nano, p.At)
		if err != nil || stamp.After(now) || now.Sub(stamp) >= time.Duration(maxAge)*time.Second {
			continue
		}
		count++
		if !*p.OK {
			failed++
		}
		if p.Path == "direct" && *p.OK {
			direct = true
		}
		if (p.Path == "derp" || p.Path == "peer-relay") && *p.OK {
			relay = true
		}
		if at.IsZero() || stamp.Before(at) {
			at = stamp
		}
		fingerprints = append(fingerprints, key+"|"+p.At+"|"+strconv.FormatBool(*p.OK)+"|"+p.Path)
	}
	if count == 0 {
		return unknown("stale")
	}
	o := healthy()
	if failed > 0 {
		o.Status = "degraded"
		o.Code = "path_failed"
	} // subset evidence, not all peers/business down
	if direct && relay {
		o.PathMode = "mixed"
	} else if direct {
		o.PathMode = "direct"
	} else if relay {
		o.PathMode = "relay"
	}
	// Preserve source age, capture time and ID; reading old evidence again cannot
	// refresh current state or count one failed event as multiple independent failures.
	sort.Strings(fingerprints)
	digest := sha256.Sum256([]byte(c.ServiceID + "|" + strings.Join(fingerprints, ";")))
	o.ID = hex.EncodeToString(digest[:16])
	o.CapturedAt = at.UTC().Format(time.RFC3339Nano)
	age := now.Sub(at).Seconds()
	o.AgeSeconds = &age
	return o
}
