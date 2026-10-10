package collector

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/PwnKY/bastion-home-status/internal/status"
	"golang.org/x/net/dns/dnsmessage"
)

var errUnsupported = errors.New("unsupported platform capability")
var errMissingMount = errors.New("required separate filesystem missing")
var processBoot = ID()
var started = time.Now()

func clock() (string, float64) {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	up, err2 := os.ReadFile("/proc/uptime")
	fields := strings.Fields(string(up))
	if err == nil && err2 == nil && len(fields) > 0 {
		value, err := strconv.ParseFloat(fields[0], 64)
		if err == nil {
			return strings.TrimSpace(string(boot)), value
		}
	}
	return processBoot, time.Since(started).Seconds()
}
func verifyWireGuardRoute(host string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, "ip", "-j", "route", "get", host).Output()
	if err != nil {
		return errors.New("cannot verify private reporting route")
	}
	var routes []struct {
		Dev string `json:"dev"`
	}
	if json.Unmarshal(raw, &routes) != nil || len(routes) != 1 || routes[0].Dev != "wg-bastion" {
		return errors.New("reporting route is not WireGuard")
	}
	return nil
}
func Probe(ctx context.Context, c Check) status.Observation {
	result := status.Observation{Status: "down", Code: "probe_error", PathMode: "unknown"}
	began := time.Now()
	switch c.Kind {
	case "unit", "job":
		return unitProbe(ctx, c)
	case "pve":
		return pveProbe(ctx, c)
	case "tcp":
		return tcpProbe(ctx, c)
	case "peer-pinger", "peer-poll":
		return peerProbe(c)
	case "untested":
		result.Status = "untested"
		result.Code = "untested"
		return result
	case "http", "tailscale", "tunnel":
		code, body, err := fetch(ctx, c, c.URL)
		expected := c.ExpectedStatus
		if expected == 0 {
			expected = 200
		}
		if c.Kind == "tunnel" && c.MetricURL != "" {
			statusCode, metrics, metricErr := fetch(ctx, c, c.MetricURL)
			if metricErr == nil && statusCode == 200 {
				if value, ok := metric(string(metrics), "cloudflared_tunnel_ha_connections"); ok && value >= 0 && value <= 1000 {
					n := int(value)
					result.ReadyConnections = &n
				}
			}
		}
		if err != nil || code != expected || (c.ExpectedText != "" && strings.TrimSpace(string(body)) != c.ExpectedText) {
			result.Code = "http_error"
			return result
		}
		if c.Kind == "tailscale" {
			var state struct {
				BackendState string
				Health       []string
			}
			if json.Unmarshal(body, &state) != nil || state.BackendState != "Running" {
				result.Code = "health_error"
				return result
			}
			if len(state.Health) > 0 {
				result.Status = "degraded"
				result.Code = "health_error"
				return result
			}
		}
		result.Status = "healthy"
		result.Code = "ok"

	case "dns":
		if err := dns(ctx, c); err != nil {
			result.Code = "dns_error"
			return result
		}
		result.Status = "healthy"
		result.Code = "ok"
	case "disk":
		usage, err := diskUsage(c.Path, c.RequireSeparateFS)
		if err != nil {
			result.Status = "unknown"
			result.Code = "probe_error"
			if errors.Is(err, errUnsupported) {
				result.Status = "untested"
				result.Code = "unconfigured"
			}
			if os.IsNotExist(err) || errors.Is(err, errMissingMount) {
				result.Status = "down"
				result.Code = "health_error"
			}
			return result
		}
		result.UsagePercent = &usage
		warning, critical := c.WarningPercent, c.CriticalPercent
		if warning == 0 {
			warning = 90
		}
		if critical == 0 {
			critical = 95
		}
		result.Status = "healthy"
		result.Code = "ok"
		if usage >= warning {
			result.Status = "degraded"
			result.Code = "space_warning"
		}
		if usage >= critical {
			// Capacity risk is not proof that playback/download is already unavailable.
			result.Status = "degraded"
			result.Code = "space_critical"
		}
		return result
	default:
		result.Status = "untested"
		result.Code = "unconfigured"
		return result
	}
	ms := float64(time.Since(began).Microseconds()) / 1000
	result.LatencyMS = &ms
	return result
}
func fetch(ctx context.Context, c Check, target string) (int, []byte, error) {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return 0, nil, errors.New("invalid check URL")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if c.CAFile != "" {
		cert, err := os.ReadFile(c.CAFile)
		if err != nil || len(cert) > 1024*1024 {
			return 0, nil, errors.New("invalid probe trust file")
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(cert) {
			return 0, nil, errors.New("invalid probe trust file")
		}
		transport.TLSClientConfig.RootCAs = pool
	}
	if c.Network != "" || c.ConnectIP != "" {
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			if c.Network != "" {
				network = c.Network
			}
			if c.ConnectIP != "" {
				if c.ProxyURL != "" || c.Socket != "" {
					return nil, errors.New("ambiguous transport")
				}
				_, port, err := net.SplitHostPort(address)
				if err != nil {
					return nil, err
				}
				address = net.JoinHostPort(c.ConnectIP, port)
			}
			return (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, network, address)
		}
	}
	if c.ProxyURL != "" {
		proxy, err := url.Parse(c.ProxyURL)
		if err != nil || proxy.Host == "" || proxy.Scheme != "http" {
			return 0, nil, errors.New("invalid proxy")
		}
		transport.Proxy = http.ProxyURL(proxy)
	}
	if c.Socket != "" {
		if c.ProxyURL != "" {
			return 0, nil, errors.New("ambiguous transport")
		}
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, "unix", c.Socket)
		}
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 8 * time.Second, Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return 0, nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 256*1024+1))
	if err != nil || len(body) > 256*1024 {
		return 0, nil, errors.New("invalid check response")
	}
	return response.StatusCode, body, nil
}
func metric(text, name string) (float64, bool) {
	sum := 0.0
	found := false
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.SplitN(fields[0], "{", 2)[0]
		if key != name {
			continue
		}
		v, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || v < 0 || v > 1000 {
			return 0, false
		}
		sum += v
		found = true
	}
	return sum, found
}
func dns(ctx context.Context, c Check) error {
	if c.Query == "" || len(c.Query) > 253 {
		return errors.New("invalid DNS name")
	}
	query := make([]byte, 12)
	if _, err := rand.Read(query[:2]); err != nil {
		return err
	}
	binary.BigEndian.PutUint16(query[2:4], 0x0100)
	binary.BigEndian.PutUint16(query[4:6], 1)
	for _, label := range strings.Split(strings.TrimSuffix(c.Query, "."), ".") {
		if len(label) < 1 || len(label) > 63 {
			return errors.New("invalid DNS label")
		}
		query = append(query, byte(len(label)))
		query = append(query, []byte(label)...)
	}
	query = append(query, 0)
	record := uint16(1)
	if c.RecordType == "AAAA" {
		record = 28
	} else if c.RecordType != "" && c.RecordType != "A" {
		return errors.New("invalid DNS record")
	}
	query = append(query, byte(record>>8), byte(record), 0, 1)
	network := "udp"
	if c.TCP {
		network = "tcp"
	}
	conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, c.DNSServer)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	var response []byte
	if c.TCP {
		framed := append([]byte{byte(len(query) >> 8), byte(len(query))}, query...)
		if _, err = conn.Write(framed); err != nil {
			return err
		}
		prefix := make([]byte, 2)
		if _, err = io.ReadFull(conn, prefix); err != nil {
			return err
		}
		length := int(binary.BigEndian.Uint16(prefix))
		if length < 12 {
			return errors.New("short DNS response")
		}
		response = make([]byte, length)
		if _, err = io.ReadFull(conn, response); err != nil {
			return err
		}
	}
	if !c.TCP {
		if _, err = conn.Write(query); err != nil {
			return err
		}
		buffer := make([]byte, 4096)
		n, err := conn.Read(buffer)
		if err != nil {
			return err
		}
		response = buffer[:n]
	}
	if len(response) < 12 || binary.BigEndian.Uint16(response[:2]) != binary.BigEndian.Uint16(query[:2]) {
		return errors.New("invalid DNS transaction")
	}
	flags := binary.BigEndian.Uint16(response[2:4])
	if flags&0x8000 == 0 || flags&0x0200 != 0 || int(flags&15) != c.ExpectedRCode {
		return errors.New("unexpected DNS result")
	}
	var message dnsmessage.Message
	if message.Unpack(response) != nil || len(message.Questions) != 1 {
		return errors.New("malformed DNS response")
	}
	question := message.Questions[0]
	if !strings.EqualFold(strings.TrimSuffix(question.Name.String(), "."), strings.TrimSuffix(c.Query, ".")) || uint16(question.Type) != record || question.Class != dnsmessage.ClassINET {
		return errors.New("mismatched DNS question")
	}
	if c.ExpectedRCode == 0 {
		found := false
		for _, answer := range message.Answers {
			if uint16(answer.Header.Type) == record && answer.Header.Class == dnsmessage.ClassINET {
				found = true
			}
		}
		if !found {
			return errors.New("missing requested DNS record")
		}
	}
	return nil
}
