package collector

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func journalFixture(kind, boot, unit string, at time.Time, mono string) map[string]string {
	entry := map[string]string{"MESSAGE_ID": jobFinishedMessage, "UNIT": unit, "JOB_TYPE": "start", "JOB_RESULT": "done", "_BOOT_ID": boot, "_PID": "1", "_UID": "0", "__REALTIME_TIMESTAMP": "", "__MONOTONIC_TIMESTAMP": mono, "MESSAGE": "private log text must never leave collector"}
	entry["__REALTIME_TIMESTAMP"] = jsonNumber(at.UnixMicro())
	if kind == "job_failure" {
		entry["MESSAGE_ID"], entry["RESULT"], entry["JOB_RESULT"] = jobFailedMessage, "exit-code", "failed"
	}
	return entry
}
func jsonNumber(n int64) string { raw, _ := json.Marshal(n); return string(raw) }
func journalBytes(entries ...map[string]string) []byte {
	var raw []byte
	for _, entry := range entries {
		data, _ := json.Marshal(entry)
		raw = append(raw, data...)
		raw = append(raw, '\n')
	}
	return raw
}
func TestJournalPreviousBootIsReferenceNotFreshSuccess(t *testing.T) {
	c := Check{Kind: "job", Unit: "fixture.service", JournalHistory: true, MaxAgeSeconds: 172800}
	at := time.Date(2026, 1, 1, 4, 35, 0, 0, time.UTC)
	oldBoot, newBoot := strings.Repeat("a", 32), strings.Repeat("b", 32)
	for _, kind := range []string{"job_success", "job_failure"} {
		raw := journalBytes(journalFixture(kind, oldBoot, c.Unit, at, "10000000"))
		first := jobJournalResult(raw, c, newBoot, 100, unknown("job_no_record"))
		again := jobJournalResult(raw, c, newBoot, 200, unknown("job_no_record"))
		if first.Status != "unknown" || first.Code != "job_previous_boot" || first.Evidence == nil || !first.Evidence.Historical || first.Evidence.Kind != kind || first.Evidence.At != again.Evidence.At || first.AgeSeconds != nil {
			t.Fatal(first, again)
		}
		data, _ := json.Marshal(first)
		if strings.Contains(string(data), "private log") || strings.Contains(string(data), oldBoot) || strings.Contains(string(data), c.Unit) {
			t.Fatal("raw metadata leaked", string(data))
		}
	}
}
func TestJournalSameBootMonotonicCompletionAndOverdue(t *testing.T) {
	c := Check{Kind: "job", Unit: "fixture.service", JournalHistory: true, MaxAgeSeconds: 60}
	boot := strings.Repeat("a", 32)
	at := time.Date(2026, 1, 1, 4, 35, 0, 0, time.UTC)
	raw := journalBytes(journalFixture("job_success", boot, c.Unit, at, "90000000"))
	if o := jobJournalResult(raw, c, boot, 100, unknown("job_no_record")); o.Status != "healthy" || o.Evidence.Historical {
		t.Fatal(o)
	}
	if o := jobJournalResult(raw, c, boot, 150, unknown("job_no_record")); o.Status != "degraded" || o.Code != "job_overdue" || !o.Evidence.Historical {
		t.Fatal(o)
	}
	raw = append(raw, journalBytes(journalFixture("job_failure", boot, c.Unit, at.Add(-time.Hour), "95000000"))...)
	if o := jobJournalResult(raw, c, boot, 100, unknown("job_no_record")); o.Status != "down" || o.Evidence.Kind != "job_failure" {
		t.Fatal("latest failure hidden", o)
	}
	// Source wall time, including jumps into the future, is reference only.
	for _, mono := range []string{"0", "200000000", "NaN", "Inf", "-1", "bad"} {
		raw := journalBytes(journalFixture("job_success", boot, c.Unit, at.AddDate(5, 0, 0), mono))
		if o := jobJournalResult(raw, c, boot, 100, unknown("job_no_record")); o.Status != "unknown" || o.Evidence == nil {
			t.Fatal("invalid monotonic evidence green", mono, o)
		}
	}
	if o := jobJournalResult(raw, c, boot, math.NaN(), unknown("job_no_record")); o.Status != "unknown" {
		t.Fatal(o)
	}
}
func TestJournalRejectsSpoofedTruncatedUnboundedAndUnconfiguredEvidence(t *testing.T) {
	c := Check{Kind: "job", Unit: "fixture.service", JournalHistory: true}
	boot := strings.Repeat("a", 32)
	at := time.Date(2026, 1, 1, 4, 35, 0, 0, time.UTC)
	for key, bad := range map[string]string{"_PID": "20", "_UID": "1000", "UNIT": "other.service", "MESSAGE_ID": "not-completion", "JOB_TYPE": "stop", "JOB_RESULT": "failed", "_BOOT_ID": "invalid", "__REALTIME_TIMESTAMP": "NaN"} {
		entry := journalFixture("job_success", boot, c.Unit, at, "90000000")
		entry[key] = bad
		if o := jobJournalResult(journalBytes(entry), c, boot, 100, unknown("job_no_record")); o.Evidence != nil || o.Status != "unknown" {
			t.Fatal(key, o)
		}
	}
	good := journalBytes(journalFixture("job_success", boot, c.Unit, at, "90000000"))
	for _, raw := range [][]byte{append(good, '{'), []byte(strings.Repeat("x", 1024*1024+1)), []byte(strings.Repeat(string(good), 25))} {
		if o := jobJournalResult(raw, c, boot, 100, unknown("job_no_record")); o.Evidence != nil || o.Status != "unknown" {
			t.Fatal(o)
		}
	}
	started := journalFixture("job_success", boot, c.Unit, at.Add(time.Minute), "95000000")
	started["MESSAGE_ID"], started["JOB_RESULT"] = jobStartingMessage, ""
	if o := jobJournalResult(append(good, journalBytes(started)...), c, boot, 100, unknown("job_no_record")); o.Status != "unknown" || o.Evidence != nil {
		t.Fatal("incomplete later attempt hidden by old success", o)
	}
	c.JournalHistory = false
	if o := jobJournalResult(good, c, boot, 100, unknown("job_no_record")); o.Evidence != nil {
		t.Fatal("unconfigured fallback used", o)
	}
}
