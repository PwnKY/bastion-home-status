package collector

import (
	"strings"
	"testing"
	"time"
)

func TestJournalFailedStartWithoutServiceResult(t *testing.T) {
	c := Check{Kind: "job", Unit: "fixture.service", JournalHistory: true}
	boot := strings.Repeat("a", 32)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	failed := journalFixture("job_failure", boot, c.Unit, at, "90000000")
	delete(failed, "RESULT") // Real systemd failed-start shape: JOB_RESULT only.
	raw := journalBytes(failed)
	for _, currentBoot := range []bool{false, true} {
		current := strings.Repeat("b", 32)
		if currentBoot {
			current = boot
		}
		o := jobJournalResult(raw, c, current, 100, unknown("job_no_record"))
		want := "unknown"
		if currentBoot {
			want = "down"
		}
		if o.Status != want || o.Evidence == nil || o.Evidence.Kind != "job_failure" || !o.Evidence.Historical {
			t.Fatal("failed start shape lost", o)
		}
	}
	// An older failed attempt is not the latest task outcome after completion.
	completed := journalFixture("job_success", boot, c.Unit, at.Add(time.Minute), "95000000")
	raw = append(raw, journalBytes(completed)...)
	first := jobJournalResult(raw, c, strings.Repeat("b", 32), 100, unknown("job_no_record"))
	again := jobJournalResult(raw, c, strings.Repeat("b", 32), 150, unknown("job_no_record"))
	if first.Status != "unknown" || first.Code != "job_previous_boot" || first.Evidence == nil || first.Evidence.Kind != "job_success" || first.Evidence.At != at.Add(time.Minute).Format(time.RFC3339Nano) || first.Evidence.At != again.Evidence.At || !first.Evidence.Historical {
		t.Fatal("older failure masked later completion", first, again)
	}
}

func TestJournalUnknownOrConflictingLatestFailureBlocksOlderSuccess(t *testing.T) {
	c := Check{Kind: "job", Unit: "fixture.service", JournalHistory: true}
	boot := strings.Repeat("a", 32)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	good := journalBytes(journalFixture("job_success", boot, c.Unit, at, "90000000"))
	for key, value := range map[string]string{"RESULT": "unknown-result", "JOB_TYPE": "stop", "JOB_RESULT": "done"} {
		failed := journalFixture("job_failure", boot, c.Unit, at.Add(time.Minute), "95000000")
		failed[key] = value
		o := jobJournalResult(append(append([]byte{}, good...), journalBytes(failed)...), c, boot, 100, unknown("job_no_record"))
		if o.Status != "unknown" || o.Evidence != nil {
			t.Fatal("conflicting failure promoted old success", key, o)
		}
		// Unknown historical outcomes do not mask a subsequent verified success.
		o = jobJournalResult(append(journalBytes(failed), good...), c, boot, 100, unknown("job_no_record"))
		if o.Status != "healthy" || o.Evidence == nil || o.Evidence.Kind != "job_success" {
			t.Fatal("new completion not selected in journal order", key, o)
		}
	}
	failed := journalFixture("job_failure", boot, c.Unit, at, "95000000")
	delete(failed, "RESULT")
	delete(failed, "JOB_TYPE")
	if o := jobJournalResult(append(good, journalBytes(failed)...), c, boot, 100, unknown("job_no_record")); o.Status != "unknown" || o.Evidence != nil {
		t.Fatal("missing job type accepted", o)
	}
}
