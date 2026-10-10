package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/PwnKY/bastion-home-status/internal/status"
)

const jobStartingMessage = "7d4958e842da4a758f6c1cdc7b36dcc5"
const jobFinishedMessage = "39f53479d3a045ac8e11786248231fbf"
const jobFailedMessage = "be02cf6855d2428ba40df7e9d022f03d"

func jobJournalProbe(ctx context.Context, c Check, boot string, fallback status.Observation) status.Observation {
	raw, err := readCommand(ctx, "journalctl", "--unit", c.Unit, "--no-pager", "--output=json", "--lines=24", "--output-fields=MESSAGE_ID,UNIT,JOB_TYPE,JOB_RESULT,RESULT,_BOOT_ID,_PID,_UID,__REALTIME_TIMESTAMP,__MONOTONIC_TIMESTAMP", "_PID=1", "MESSAGE_ID="+jobStartingMessage, "MESSAGE_ID="+jobFinishedMessage, "MESSAGE_ID="+jobFailedMessage)
	if err != nil {
		return fallback
	}
	_, uptime := clock()
	return jobJournalResult(raw, c, boot, uptime, fallback)
}

func jobJournalResult(raw []byte, c Check, boot string, uptime float64, fallback status.Observation) status.Observation {
	if len(raw) > 1024*1024 || !unitName.MatchString(c.Unit) || !c.JournalHistory {
		return fallback
	}
	type record struct {
		Message string `json:"MESSAGE_ID"`
		Unit    string `json:"UNIT"`
		Job     string `json:"JOB_TYPE"`
		Result  string `json:"JOB_RESULT"`
		Failure string `json:"RESULT"`
		Boot    string `json:"_BOOT_ID"`
		PID     string `json:"_PID"`
		UID     string `json:"_UID"`
		At      string `json:"__REALTIME_TIMESTAMP"`
		Mono    string `json:"__MONOTONIC_TIMESTAMP"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var chosen record
	var chosenAt int64
	kind := ""
	for n := 0; ; n++ {
		var entry record
		err := decoder.Decode(&entry)
		if err == io.EOF {
			break
		}
		if err != nil || n >= 24 {
			return fallback
		}
		if entry.PID != "1" || entry.UID != "0" || entry.Unit != c.Unit || len(entry.Boot) != 32 || strings.Trim(entry.Boot, "0123456789abcdef") != "" {
			continue
		}
		candidate := ""
		if entry.Message == jobStartingMessage && entry.Job == "start" {
			candidate = "job_started"
		} else if entry.Message == jobFinishedMessage && entry.Job == "start" && entry.Result == "done" {
			candidate = "job_success"
		} else if entry.Message == jobFailedMessage {
			switch entry.Failure {
			case "exit-code", "signal", "core-dump", "timeout", "resources", "start-limit-hit", "watchdog", "oom-kill", "protocol", "exec-condition":
				candidate = "job_failure"
			default:
				// An unrecognized trusted failure must not expose an older success.
				return fallback
			}
		}
		at, err := strconv.ParseInt(entry.At, 10, 64)
		if candidate == "" || err != nil || at <= 0 || time.UnixMicro(at).Year() < 2000 || time.UnixMicro(at).Year() > 2100 {
			continue
		}
		// journalctl provides ordered records; do not sort wall timestamps again.
		// A wall-clock correction must not hide a later failed attempt.
		chosen, chosenAt, kind = entry, at, candidate
	}
	if kind == "" || kind == "job_started" {
		return fallback
	}
	evidence := &status.Evidence{Kind: kind, At: time.UnixMicro(chosenAt).UTC().Format(time.RFC3339Nano), Historical: true}
	o := unknown("job_previous_boot")
	o.Evidence = evidence
	// Across a boot boundary the source wall clock cannot prove elapsed age.
	// Preserve the record for reference without manufacturing fresh task success.
	if boot == "" || strings.ReplaceAll(boot, "-", "") != chosen.Boot {
		return o
	}
	mono, err := strconv.ParseFloat(chosen.Mono, 64)
	if err != nil || math.IsNaN(mono) || math.IsInf(mono, 0) || mono <= 0 || mono/1e6 > uptime || uptime < 0 || math.IsNaN(uptime) || math.IsInf(uptime, 0) {
		return o
	}
	maxAge := c.MaxAgeSeconds
	if maxAge == 0 {
		maxAge = 86400
	}
	if kind == "job_failure" {
		o.Status, o.Code = "down", "task_failed"
	} else if uptime-mono/1e6 >= float64(maxAge) {
		o.Status, o.Code = "degraded", "job_overdue"
	} else {
		o.Status, o.Code = "healthy", "ok"
		evidence.Historical = false
	}
	return o
}
