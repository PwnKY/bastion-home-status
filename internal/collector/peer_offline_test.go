package collector

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestOfflinePeerEventsAreReferenceOnlyEvenWhenRecent(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := Check{Kind: "peer-pinger", ServiceID: "path", MaxAgeSeconds: 90}
	for _, pingAge := range []time.Duration{10 * time.Second, 2 * time.Hour} {
		for _, ok := range []bool{true, false} {
			raw := []byte(strings.Replace(string(peerFixture(now, pingAge, ok)), `"online":true`, `"online":false`, 1))
			first, again := peerResult(raw, c, now), peerResult(raw, c, now.Add(time.Second))
			kind := "peer_failure"
			if ok {
				kind = "peer_success"
			}
			if first.Status != "unknown" || first.PathMode != "unknown" || first.LatencyMS != nil || first.ID != "" || first.AgeSeconds != nil || first.Evidence == nil || first.Evidence.Kind != kind || !first.Evidence.Historical || first.Evidence.At != again.Evidence.At {
				t.Fatal("offline record supplied current evidence", first, again)
			}
			data, _ := json.Marshal(first)
			if strings.Contains(string(data), "private-peer") || strings.Contains(string(data), "secret-bearing") {
				t.Fatal("private record leaked")
			}
			absent := []byte(strings.Replace(string(raw), `"present":true`, `"present":false`, 1))
			if o := peerResult(absent, c, now); o.Evidence != nil || o.Status != "unknown" {
				t.Fatal("absent reference exported", o)
			}
			future := []byte(strings.Replace(string(raw), now.Add(-pingAge).Format(time.RFC3339Nano), now.Add(time.Second).Format(time.RFC3339Nano), 1))
			if o := peerResult(future, c, now); o.Evidence != nil || o.Status != "unknown" {
				t.Fatal("future offline event exported", o)
			}
		}
	}
}

func TestOfflineFailureDoesNotEnterActiveFingerprintOrFailureCount(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := Check{Kind: "peer-pinger", ServiceID: "path", MaxAgeSeconds: 90}
	original := peerFixture(now, 20*time.Second, true)
	first := peerResult(original, c, now)
	var raw map[string]any
	if err := json.Unmarshal(original, &raw); err != nil {
		t.Fatal(err)
	}
	raw["peers"].(map[string]any)["private-offline"] = map[string]any{"present": true, "online": false, "lastPingAt": now.Add(-time.Second).Format(time.RFC3339Nano), "lastPingOK": false, "lastPingPath": "none"}
	added, _ := json.Marshal(raw)
	again := peerResult(added, c, now)
	if again.Status != "healthy" || again.PathMode != "direct" || again.ID != first.ID || again.CapturedAt != first.CapturedAt || *again.AgeSeconds != *first.AgeSeconds || again.Evidence == nil || !again.Evidence.Historical || again.Evidence.Kind != "peer_failure" {
		t.Fatal("inactive reference altered current subset", first, again)
	}
}
