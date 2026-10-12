package collector

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Generated fixture addresses are derived from the Tailscale ranges at runtime;
// they are not deployment targets and no real peer address appears in source.
func tsmpFixture4(offset uint32) netip.Addr {
	base := tsmpIPv4Prefix.Addr().As4()
	value := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
	value += offset
	return netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)})
}

func tsmpFixture6(offset int) netip.Addr {
	ip := tsmpIPv6Prefix.Addr()
	for i := 0; i < offset; i++ {
		ip = ip.Next()
	}
	return ip
}

func tsmpStatusJSON(t *testing.T, self, peer netip.Addr, online bool, backend string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"BackendState": backend,
		"Self":         map[string]any{"TailscaleIPs": []string{self.String()}},
		"Peer": map[string]any{
			"private-node-key": map[string]any{"TailscaleIPs": []string{peer.String()}, "Online": online},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestTSMPTargetRejectsNonTailscaleLiterals(t *testing.T) {
	for _, ip := range []netip.Addr{tsmpFixture4(1), tsmpFixture4(700), tsmpFixture6(1), tsmpFixture6(9)} {
		if ip.Is4() {
			if !tsmpIPv4Prefix.Contains(ip) {
				t.Fatalf("fixture outside IPv4 prefix: %s", ip)
			}
		} else if !tsmpIPv6Prefix.Contains(ip) {
			t.Fatalf("fixture outside IPv6 prefix: %s", ip)
		}
		if _, err := tsmpTarget(ip.String()); err != nil {
			t.Fatalf("valid Tailscale literal rejected: %s", ip)
		}
	}
	bad := []string{
		"", "example.invalid", tsmpFixture4(1).String() + ":41641", tsmpFixture4(1).String() + "%eth0", "::ffff:" + tsmpFixture4(1).String(),
		netip.AddrFrom4([4]byte{10, 0, 0, 1}).String(),
		netip.AddrFrom4([4]byte{100, 63, 255, 255}).String(),
		netip.AddrFrom4([4]byte{100, 128, 0, 1}).String(),
		netip.MustParseAddr("2001:db8::1").String(),
		netip.MustParseAddr("fd7a:115c:a1e1::1").String(),
		netip.MustParseAddr("::1").String(),
	}
	for _, target := range bad {
		if _, err := tsmpTarget(target); err == nil {
			t.Fatalf("unsafe TSMP target accepted: %q", target)
		}
	}
}

func TestTSMPValidationRejectsIncompatibleFields(t *testing.T) {
	target := tsmpFixture4(3).String()
	if err := validateAdapter(Check{Kind: "tailscale-tsmp", Target: target}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Check{
		{Kind: "tailscale-tsmp"},
		{Kind: "tailscale-tsmp", Target: "example.invalid"},
		{Kind: "tailscale-tsmp", Target: target, URL: "https://example.invalid"},
		{Kind: "tailscale-tsmp", Target: target, ProxyURL: "http://localhost"},
		{Kind: "tailscale-tsmp", Target: target, Network: "tcp4"},
		{Kind: "tailscale-tsmp", Target: target, ConnectIP: target},
		{Kind: "tailscale-tsmp", Target: target, Socket: "socket"},
		{Kind: "tailscale-tsmp", Target: target, MetricURL: "http://localhost"},
		{Kind: "tailscale-tsmp", Target: target, ExpectedStatus: 200},
		{Kind: "tailscale-tsmp", Target: target, ExpectedText: "x"},
		{Kind: "tailscale-tsmp", Target: target, DNSServer: "127.0.0.1:53"},
		{Kind: "tailscale-tsmp", Target: target, Query: "example.invalid"},
		{Kind: "tailscale-tsmp", Target: target, RecordType: "A"},
		{Kind: "tailscale-tsmp", Target: target, TCP: true},
		{Kind: "tailscale-tsmp", Target: target, ExpectedRCode: 1},
		{Kind: "tailscale-tsmp", Target: target, Path: "evidence.json"},
		{Kind: "tailscale-tsmp", Target: target, RequireSeparateFS: true},
		{Kind: "tailscale-tsmp", Target: target, WarningPercent: 90},
		{Kind: "tailscale-tsmp", Target: target, CriticalPercent: 95},
		{Kind: "tailscale-tsmp", Target: target, Unit: "x.service"},
		{Kind: "tailscale-tsmp", Target: target, ResourceID: 100},
		{Kind: "tailscale-tsmp", Target: target, ResourceType: "node"},
		{Kind: "tailscale-tsmp", Target: target, MaxAgeSeconds: 60},
		{Kind: "tailscale-tsmp", Target: target, CAFile: "ca.pem"},
		{Kind: "tailscale-tsmp", Target: target, JournalHistory: true},
	} {
		if validateAdapter(bad) == nil {
			t.Fatalf("incompatible TSMP check accepted: %+v", bad)
		}
	}
}

func TestTSMPStatusGateSelfAbsentOffline(t *testing.T) {
	self, peer := tsmpFixture4(1), tsmpFixture4(2)
	cases := []struct {
		name   string
		raw    []byte
		target netip.Addr
		code   string
	}{
		{"self", tsmpStatusJSON(t, self, peer, true, "Running"), self, "tsmp_self"},
		{"absent", tsmpStatusJSON(t, self, peer, true, "Running"), tsmpFixture4(9), "tsmp_absent"},
		{"offline", tsmpStatusJSON(t, self, peer, false, "Running"), peer, "tsmp_offline"},
		{"stopped", tsmpStatusJSON(t, self, peer, true, "Stopped"), peer, "tsmp_unavailable"},
		{"garbage", []byte("not-json"), peer, "tsmp_unavailable"},
	}
	for _, tc := range cases {
		code, ok := tsmpPeerState(tc.raw, tc.target)
		if ok || code != tc.code {
			t.Fatalf("%s: got (%q,%v) want %q", tc.name, code, ok, tc.code)
		}
	}
	if code, ok := tsmpPeerState(tsmpStatusJSON(t, self, peer, true, "Running"), peer); !ok || code != "" {
		t.Fatalf("online peer rejected: %q %v", code, ok)
	}
}

func TestTSMPPongParsingRequiresMatchedTSMP(t *testing.T) {
	target, other := tsmpFixture4(2), tsmpFixture4(7)
	if rtt, ok := tsmpPongRTT([]byte("pong from private-node ("+target.String()+", 41641) via TSMP in 16ms\n"), target); !ok || rtt != 16 {
		t.Fatal("valid TSMP pong rejected", rtt, ok)
	}
	if rtt, ok := tsmpPongRTT([]byte("pong from private-node ("+target.String()+") via TSMP in 3.5ms"), target); !ok || rtt != 3.5 {
		t.Fatal("portless decimal pong rejected", rtt, ok)
	}
	bad := []string{
		"pong from private-node (" + target.String() + ", 41641) via DERP(nyc) in 16ms",
		"pong from private-node (" + target.String() + ", 41641) via " + other.String() + ":41641 in 16ms",
		"pong from private-node (" + other.String() + ", 41641) via TSMP in 16ms",
		"pong from private-node (" + target.String() + ", 41641) via TSMP in 16ms\npong from private-node (" + target.String() + ", 41641) via TSMP in 20ms",
		"pong from private-node (" + target.String() + ") via TSMP in NaNms",
		"pong from private-node (" + target.String() + ") via TSMP in 999999ms",
		"pong from private-node (" + target.String() + ") via TSMP in -4ms",
		"timeout: no matching TSMP response",
		"",
	}
	for _, raw := range bad {
		if _, ok := tsmpPongRTT([]byte(raw), target); ok {
			t.Fatalf("non-TSMP/foreign/oversized pong accepted: %q", raw)
		}
	}
}

func TestTSMPNoReplyRequiresExactVendorLine(t *testing.T) {
	target := tsmpFixture4(2)
	for name, raw := range map[string]string{
		"legacy":         "timeout waiting for ping reply\n",
		"current":        `ping "` + target.String() + `" timed out` + "\n",
		"legacy-trimmed": "  timeout waiting for ping reply  ",
	} {
		if !tsmpNoReply([]byte(raw), target) {
			t.Fatalf("%s: exact vendor timeout not recognized: %q", name, raw)
		}
	}
	for name, raw := range map[string]string{
		"embedded":             "note: timeout waiting for ping reply was seen",
		"extra-line":           "timeout waiting for ping reply\nsecond line",
		"current-other-target": `ping "` + tsmpFixture4(8).String() + `" timed out`,
		"current-no-quotes":    "ping " + target.String() + " timed out",
		"empty":                "",
	} {
		if tsmpNoReply([]byte(raw), target) {
			t.Fatalf("%s: non-exact timeout text accepted: %q", name, raw)
		}
	}
}

func TestTSMPProbeFixedCommandBudgetAndVerdicts(t *testing.T) {
	self, target := tsmpFixture4(1), tsmpFixture4(2)
	status := tsmpStatusJSON(t, self, target, true, "Running")
	check := Check{ServiceID: "tailscale-path", Kind: "tailscale-tsmp", Target: target.String()}
	wantPing := "tailscale ping --tsmp --c=1 --timeout=3s --until-direct=false " + target.String()

	t.Run("healthy", func(t *testing.T) {
		var calls []string
		var budget time.Duration
		readStatus := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			calls = append(calls, "tailscale status --json")
			return status, nil
		}
		readPing := func(ctx context.Context, name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			if deadline, ok := ctx.Deadline(); ok {
				budget = time.Until(deadline)
			}
			return []byte("pong from private-node (" + target.String() + ", 41641) via TSMP in 16ms\n"), nil
		}
		o := tsmpProbeWith(context.Background(), check, readStatus, readPing)
		if o.Status != "healthy" || o.LatencyMS == nil || *o.LatencyMS != 16 || o.PathMode != "unknown" {
			t.Fatal(o)
		}
		if len(calls) != 2 || calls[0] != "tailscale status --json" || calls[1] != wantPing {
			t.Fatal(calls)
		}
		if budget <= 0 || budget > tsmpPingBudget+100*time.Millisecond {
			t.Fatal("ping budget", budget)
		}
		// No generated address may appear in the sanitized observation.
		marshaled, _ := json.Marshal(o)
		if strings.Contains(string(marshaled), target.String()) || strings.Contains(string(marshaled), "private-node") {
			t.Fatal("target identity leaked", string(marshaled))
		}
	})

	t.Run("exact_vendor_timeout_is_down", func(t *testing.T) {
		for name, out := range map[string]string{
			"legacy":  "timeout waiting for ping reply\n",
			"current": `ping "` + target.String() + `" timed out` + "\n",
		} {
			readStatus := func(_ context.Context, _ string, _ ...string) ([]byte, error) { return status, nil }
			readPing := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
				return []byte(out), &exec.ExitError{}
			}
			o := tsmpProbeWith(context.Background(), check, readStatus, readPing)
			if o.Status != "down" || o.Code != "tsmp_error" || o.LatencyMS != nil || o.PathMode != "unknown" {
				t.Fatalf("%s: %+v", name, o)
			}
		}
	})

	t.Run("nonzero_or_local_failure_is_unknown", func(t *testing.T) {
		cases := map[string]struct {
			out []byte
			err error
		}{
			// A real ExitError with no output is a daemon/socket/other failure,
			// not a peer timeout.
			"daemon_exit":         {nil, &exec.ExitError{}},
			"daemon_text":         {[]byte("failed to connect to local tailscaled; it doesn't appear to be running\n"), &exec.ExitError{}},
			"unrelated_output":    {[]byte("no reply\n"), &exec.ExitError{}},
			"killed_local_budget": {nil, context.DeadlineExceeded},
			"timeout_plus_text":   {[]byte("timeout waiting for ping reply\nextra\n"), &exec.ExitError{}},
			"embedded_timeout":    {[]byte("note: timeout waiting for ping reply was seen\n"), &exec.ExitError{}},
		}
		for name, tc := range cases {
			readStatus := func(_ context.Context, _ string, _ ...string) ([]byte, error) { return status, nil }
			readPing := func(_ context.Context, _ string, _ ...string) ([]byte, error) { return tc.out, tc.err }
			o := tsmpProbeWith(context.Background(), check, readStatus, readPing)
			if o.Status != "unknown" || o.Code != "tsmp_unavailable" || o.LatencyMS != nil {
				t.Fatalf("%s: %+v", name, o)
			}
		}
	})

	t.Run("inability_is_unknown", func(t *testing.T) {
		for _, failure := range []error{exec.ErrNotFound, os.ErrPermission, errOutputBudget, errors.New("bounded read failure")} {
			readStatus := func(_ context.Context, _ string, _ ...string) ([]byte, error) { return status, nil }
			readPing := func(_ context.Context, _ string, _ ...string) ([]byte, error) { return nil, failure }
			o := tsmpProbeWith(context.Background(), check, readStatus, readPing)
			if o.Status != "unknown" || o.Code != "tsmp_unavailable" || o.LatencyMS != nil {
				t.Fatalf("%v: %+v", failure, o)
			}
		}
	})

	t.Run("cancel_is_unknown", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		readStatus := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			t.Fatal("status read issued after cancellation")
			return nil, nil
		}
		readPing := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			t.Fatal("ping issued after cancellation")
			return nil, nil
		}
		if o := tsmpProbeWith(ctx, check, readStatus, readPing); o.Status != "unknown" || o.Code != "tsmp_unavailable" {
			t.Fatal(o)
		}
	})

	t.Run("gate_rejects_before_ping", func(t *testing.T) {
		for name, raw := range map[string][]byte{
			"self":    tsmpStatusJSON(t, target, tsmpFixture4(5), true, "Running"),
			"offline": tsmpStatusJSON(t, self, target, false, "Running"),
			"absent":  tsmpStatusJSON(t, self, tsmpFixture4(5), true, "Running"),
		} {
			readStatus := func(_ context.Context, _ string, _ ...string) ([]byte, error) { return raw, nil }
			readPing := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
				t.Fatalf("%s: ping sent before the status gate passed", name)
				return nil, nil
			}
			o := tsmpProbeWith(context.Background(), check, readStatus, readPing)
			if o.Status != "unknown" {
				t.Fatalf("%s: %+v", name, o)
			}
		}
	})

	t.Run("invalid_target_no_command", func(t *testing.T) {
		readStatus := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			t.Fatal("command issued for invalid target")
			return nil, nil
		}
		readPing := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			t.Fatal("ping issued for invalid target")
			return nil, nil
		}
		if o := tsmpProbeWith(context.Background(), Check{Kind: "tailscale-tsmp", Target: "example.invalid"}, readStatus, readPing); o.Status != "unknown" || o.Code != "tsmp_unavailable" {
			t.Fatal(o)
		}
	})
}
