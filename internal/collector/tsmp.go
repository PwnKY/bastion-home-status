package collector

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/netip"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PwnKY/bastion-home-status/internal/status"
)

// The TSMP adapter performs exactly one fixed, read-only Tailscale data-plane
// probe to ONE configured peer. It never logs in, changes routes/ACLs, or runs a
// shell. TSMP encrypts the Tailscale data plane end to end, but it deliberately
// bypasses application, OS and subnet paths: a matched pong proves only that the
// gateway sent one datagram to the selected peer and got a matching TSMP reply.
// It does NOT prove DNS, playback, download, subnet routing or business success.
//
// The direction is always this gateway -> the selected peer. The underlying
// path (direct, DERP relay, ...) is deliberately NOT classified from status or
// CLI text; PathMode stays unknown. A raw observation is a single sample, not a
// duration-weighted SLA or availability promise.

var errTSMPTarget = errors.New("invalid Tailscale TSMP target")

// Tailscale assigns IPv4 peers from the CGNAT range and IPv6 peers from its ULA.
var tsmpIPv4Prefix = netip.PrefixFrom(netip.AddrFrom4([4]byte{100, 64, 0, 0}), 10)
var tsmpIPv6Prefix = netip.MustParsePrefix("fd7a:115c:a1e0::/48")

const tsmpPingBudget = 4 * time.Second
const tsmpPingTimeout = "3s"
const tsmpMaxRTTMillis = 3000.0

// tsmpTarget accepts only an explicit, unzoned Tailscale literal in the IPv4
// CGNAT range or the exact Tailscale ULA prefix. Hostnames, ports, zones,
// mapped IPv4, RFC1918 addresses and public addresses are rejected.
func tsmpTarget(target string) (netip.Addr, error) {
	ip, err := netip.ParseAddr(target)
	if err != nil || ip.Zone() != "" || ip.Is4In6() {
		return netip.Addr{}, errTSMPTarget
	}
	if ip.Is4() {
		if !tsmpIPv4Prefix.Contains(ip) {
			return netip.Addr{}, errTSMPTarget
		}
		return ip, nil
	}
	if ip.Is6() && tsmpIPv6Prefix.Contains(ip) {
		return ip, nil
	}
	return netip.Addr{}, errTSMPTarget
}

// tsmpIncompatibleFields rejects every Check field other than the identity,
// kind and literal target so a TSMP check cannot silently borrow another
// adapter's transport, evidence or payload fields.
func tsmpIncompatibleFields(c Check) bool {
	return c.URL != "" || c.ProxyURL != "" || c.Network != "" || c.ConnectIP != "" || c.Socket != "" ||
		c.MetricURL != "" || c.ExpectedStatus != 0 || c.ExpectedText != "" || c.DNSServer != "" || c.Query != "" ||
		c.RecordType != "" || c.TCP || c.ExpectedRCode != 0 || c.Path != "" || c.RequireSeparateFS ||
		c.WarningPercent != 0 || c.CriticalPercent != 0 || c.Unit != "" || c.ResourceID != 0 ||
		c.ResourceType != "" || c.MaxAgeSeconds != 0 || c.CAFile != "" || c.JournalHistory
}

type tsmpStatus struct {
	BackendState string `json:"BackendState"`
	Self         struct {
		TailscaleIPs []string `json:"TailscaleIPs"`
	} `json:"Self"`
	Peer map[string]struct {
		TailscaleIPs []string `json:"TailscaleIPs"`
		Online       bool     `json:"Online"`
	} `json:"Peer"`
}

// tsmpPeerState verifies the local daemon is Running and the configured literal
// is exactly one known peer with Online set. Self, absent and offline targets
// return a fixed code and MUST NOT be probed. Raw status is never published.
func tsmpPeerState(raw []byte, target netip.Addr) (string, bool) {
	var state tsmpStatus
	if json.Unmarshal(raw, &state) != nil || state.BackendState != "Running" {
		return "tsmp_unavailable", false
	}
	for _, value := range state.Self.TailscaleIPs {
		if ip, err := netip.ParseAddr(value); err == nil && ip == target {
			return "tsmp_self", false
		}
	}
	matched, online := 0, false
	for _, peer := range state.Peer {
		for _, value := range peer.TailscaleIPs {
			if ip, err := netip.ParseAddr(value); err == nil && ip == target {
				matched++
				if peer.Online {
					online = true
				}
			}
		}
	}
	if matched == 0 {
		return "tsmp_absent", false
	}
	if matched != 1 {
		return "tsmp_unavailable", false
	}
	if !online {
		return "tsmp_offline", false
	}
	return "", true
}

// The only accepted CLI shape is a single, direct TSMP pong for the configured
// target. A DERP/relay/direct/ICMP line, a foreign target, extra lines or a
// non-finite/oversized RTT never becomes healthy.
var tsmpPongLine = regexp.MustCompile(`^pong from [^ ]+ \(([^()]*)\) via TSMP in ([0-9]+(?:\.[0-9]+)?)ms$`)

func tsmpPongRTT(raw []byte, target netip.Addr) (float64, bool) {
	text := strings.TrimSpace(string(raw))
	if text == "" || strings.ContainsAny(text, "\r\n") {
		return 0, false
	}
	match := tsmpPongLine.FindStringSubmatch(text)
	if match == nil {
		return 0, false
	}
	address := strings.TrimSpace(match[1])
	if comma := strings.IndexByte(address, ','); comma >= 0 {
		address = strings.TrimSpace(address[:comma])
	}
	got, err := netip.ParseAddr(address)
	if err != nil || got != target {
		return 0, false
	}
	rtt, err := strconv.ParseFloat(match[2], 64)
	if err != nil || math.IsNaN(rtt) || math.IsInf(rtt, 0) || rtt < 0 || rtt > tsmpMaxRTTMillis {
		return 0, false
	}
	return rtt, true
}

func tsmpProbe(ctx context.Context, c Check) status.Observation {
	return tsmpProbeWith(ctx, c, readCommand, readPingCommand)
}

// readPingCommand is the dedicated bounded reader for `tailscale ping`. Unlike
// readCommand it still returns whatever the CLI wrote to stdout when the
// process exits non-zero, so the caller can tell the vendor's exact single-line
// no-reply timeout apart from a daemon, socket, permission or other failure.
// Stderr is discarded; neither raw stdout nor stderr is ever exported.
func readPingCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	b := &boundedOutput{limit: 64 * 1024}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = b
	cmd.Stderr = io.Discard
	err := cmd.Run()
	return b.Bytes(), err
}

// tsmpNoReply reports whether the CLI stdout is exactly the vendor's no-reply
// timeout for the configured literal. The message changed across releases
// ("timeout waiting for ping reply", then `ping "<ip>" timed out`). Only these
// whole single-line messages count; raw output is never exported and no other
// text (including unrelated stderr-like text on stdout) is matched.
func tsmpNoReply(raw []byte, target netip.Addr) bool {
	text := strings.TrimSpace(string(raw))
	return text == "timeout waiting for ping reply" || text == `ping "`+target.String()+`" timed out`
}

// tsmpProbeWith uses injectable readers so generated tests can exercise the
// status gate, the exact ping arguments, the bounded budget and error mapping.
// readStatus mirrors readCommand (no output on failure); readPing mirrors
// readPingCommand (stdout is still returned on non-zero exit).
func tsmpProbeWith(ctx context.Context, c Check, readStatus, readPing func(context.Context, string, ...string) ([]byte, error)) status.Observation {
	target, err := tsmpTarget(c.Target)
	if err != nil || ctx.Err() != nil {
		return unknown("tsmp_unavailable")
	}
	raw, err := readStatus(ctx, "tailscale", "status", "--json")
	if err != nil {
		return unknown("tsmp_unavailable")
	}
	if code, ok := tsmpPeerState(raw, target); !ok {
		return unknown(code)
	}
	pingCtx, cancel := context.WithTimeout(ctx, tsmpPingBudget)
	defer cancel()
	raw, err = readPing(pingCtx, "tailscale", "ping", "--tsmp", "--c=1", "--timeout="+tsmpPingTimeout, "--until-direct=false", target.String())
	// Caller cancellation is never evidence about the peer, and a local
	// inability to run or read the measurement stays unknown even though the
	// peer was verified Online: the daemon/socket can fail independently.
	if ctx.Err() != nil || errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrPermission) || errors.Is(err, errOutputBudget) {
		return unknown("tsmp_unavailable")
	}
	// Only the vendor's exact single-line no-reply timeout, after an Online peer
	// was verified, is treated as down. Every other non-zero exit (including a
	// daemon/socket failure) and an expired local budget that killed the process
	// are not proof of a peer timeout and stay unknown.
	if tsmpNoReply(raw, target) {
		return status.Observation{Status: "down", Code: "tsmp_error", PathMode: "unknown"}
	}
	if err != nil {
		return unknown("tsmp_unavailable")
	}
	rtt, ok := tsmpPongRTT(raw, target)
	if !ok {
		return unknown("tsmp_unavailable")
	}
	result := healthy()
	result.Code = "tsmp_ok"
	result.LatencyMS = &rtt
	return result
}
