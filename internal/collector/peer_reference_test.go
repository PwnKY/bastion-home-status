package collector

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPeerReferencesDoNotRenewAndRejectUnboundedSourceTime(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := Check{Kind: "peer-pinger", MaxAgeSeconds: 90}
	raw := peerFixture(now, 2*time.Hour, true)
	first := peerResult(raw, c, now)
	again := peerResult(raw, c, now.Add(time.Second))
	if first.Status != "unknown" || first.PathMode != "unknown" || first.LatencyMS != nil || first.Evidence == nil || !first.Evidence.Historical || first.Evidence.At != again.Evidence.At {
		t.Fatal(first, again)
	}
	raw = []byte(strings.ReplaceAll(string(raw), now.Add(-2*time.Hour).Format(time.RFC3339Nano), "1900-01-01T00:00:00Z"))
	if o := peerResult(raw, c, now); o.Evidence != nil || o.Status != "unknown" {
		t.Fatal("out-of-contract reference exported", o)
	}
	peers := map[string]any{}
	for key, ok := range map[string]bool{"private-a": true, "private-b": false} {
		peers[key] = map[string]any{"present": true, "online": true, "lastPingAt": now.Add(-time.Hour).Format(time.RFC3339Nano), "lastPingOK": ok, "lastPingPath": "direct"}
	}
	raw, _ = json.Marshal(map[string]any{"version": 1, "initialized": true, "lastSuccessfulPollAt": now.Format(time.RFC3339Nano), "peers": peers})
	for n := 0; n < 20; n++ {
		if o := peerResult(raw, c, now); o.Evidence == nil || o.Evidence.Kind != "peer_failure" {
			t.Fatal("ambiguous tied records varied with map iteration", o)
		}
	}
}

func TestJobNonFiniteMonotonicStampCannotProveSuccess(t *testing.T) {
	for _, stamp := range []string{"NaN", "Inf", "-Inf"} {
		raw := "LoadState=loaded\nActiveState=inactive\nResult=success\nExecMainStatus=0\nExecMainExitTimestampMonotonic=" + stamp + "\n"
		if o := unitResult([]byte(raw), Check{Kind: "job"}, 100); o.Status != "unknown" || o.Code != "job_no_record" {
			t.Fatal(o)
		}
	}
}
