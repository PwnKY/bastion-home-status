package status

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"
)

// The observations endpoint reads one configured service's RAW persisted
// single-probe samples. It never returns the judged/current state, sample or
// agent IDs, payloads, addresses, credentials or raw logs. A single raw failure
// therefore survives the server's 3-failure confirmation, which only affects
// current/history.
const observationsMaxLimit = 1000
const observationCursorMaxBytes = 512

var errInvalidObservationCursor = errors.New("invalid observation cursor")

// ObservationRecord is one raw single-probe sample. ObservedAt is null only for
// an unlocatable replay that has no trustworthy source time; such a row is
// windowed by the server receipt time and clearly flagged Replay.
type ObservationRecord struct {
	ObservedAt *string   `json:"observedAt"`
	ReceivedAt string    `json:"receivedAt"`
	ExpiresAt  *string   `json:"expiresAt"`
	Status     string    `json:"status"`
	Code       string    `json:"code"`
	Summary    string    `json:"summary"`
	LatencyMS  *float64  `json:"latencyMs"`
	PathMode   string    `json:"pathMode,omitempty"`
	Replay     bool      `json:"replay"`
	Evidence   *Evidence `json:"evidence,omitempty"`
}

// ObservationResponse is a bounded page. It is not globally newest-first: all
// located samples (ordered by observed,received,rowid) precede unlocatable
// replay samples (ordered by received,rowid), because the two are read from
// different ordered index ranges.
type ObservationResponse struct {
	ServiceID    string              `json:"serviceId"`
	From         string              `json:"from"`
	To           string              `json:"to"`
	Observations []ObservationRecord `json:"observations"`
	NextCursor   string              `json:"nextCursor"`
	Truncated    bool                `json:"truncated"`
}

type ObservationPage struct {
	Records    []ObservationRecord
	NextCursor string
	Truncated  bool
}

// The cursor is opaque but strictly bounded and validated. It carries the exact
// (observed, received, rowid) tie-break tuple so newest-first pages never skip
// or repeat a row with duplicate timestamps.
type observationCursor struct {
	Present  bool  `json:"p"`
	Observed int64 `json:"o"`
	Received int64 `json:"r"`
	RowID    int64 `json:"i"`
}

func encodeObservationCursor(cursor observationCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeObservationCursor(value string) (*observationCursor, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > observationCursorMaxBytes {
		return nil, errInvalidObservationCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) == 0 || len(raw) > observationCursorMaxBytes {
		return nil, errInvalidObservationCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var cursor observationCursor
	if decoder.Decode(&cursor) != nil {
		return nil, errInvalidObservationCursor
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errInvalidObservationCursor
	}
	if cursor.Received < 0 || cursor.Observed < 0 || cursor.RowID < 1 || (!cursor.Present && cursor.Observed != 0) {
		return nil, errInvalidObservationCursor
	}
	return &cursor, nil
}

// ObservationsContext returns at most limit raw samples for one service inside
// the inclusive [from,to] window. Located samples are ordered by
// (observed,received,rowid) descending and returned first; unlocatable replay
// samples are then ordered by (received,rowid) descending, so the page is not
// globally newest-first. Indexed window seeks keep the scan and the decoded
// payload work bounded; unselected payloads are not read.
func (s *Store) ObservationsContext(ctx context.Context, id string, from, to time.Time, limit int, cursor string) (ObservationPage, error) {
	if limit < 1 || limit > observationsMaxLimit {
		return ObservationPage{}, errors.New("invalid observation limit")
	}
	if to.Before(from) {
		return ObservationPage{}, errors.New("invalid observation window")
	}
	parsed, err := decodeObservationCursor(cursor)
	if err != nil {
		return ObservationPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return ObservationPage{}, err
	}
	fromMS, toMS := from.UnixMilli(), to.UnixMilli()
	want := limit + 1
	records := make([]ObservationRecord, 0, want)
	tuples := make([]observationCursor, 0, want)
	if parsed == nil || parsed.Present {
		records, tuples, err = s.locatedObservations(ctx, id, fromMS, toMS, parsed, want, records, tuples)
		if err != nil {
			return ObservationPage{}, err
		}
	}
	if len(records) < want {
		var tail *observationCursor
		if parsed != nil && !parsed.Present {
			tail = parsed
		}
		records, tuples, err = s.unlocatedObservations(ctx, id, fromMS, toMS, tail, want-len(records), records, tuples)
		if err != nil {
			return ObservationPage{}, err
		}
	}
	page := ObservationPage{Records: records}
	if len(records) > limit {
		page.Truncated = true
		page.Records = records[:limit]
		page.NextCursor = encodeObservationCursor(tuples[limit-1])
	}
	return page, nil
}

func (s *Store) locatedObservations(ctx context.Context, id string, from, to int64, cursor *observationCursor, limit int, records []ObservationRecord, tuples []observationCursor) ([]ObservationRecord, []observationCursor, error) {
	query := `SELECT rowid,observed,received,expires,replay,data FROM samples INDEXED BY sample_history_ordered WHERE service=? AND observed>=? AND observed<=?`
	args := []any{id, from, to}
	if cursor != nil && cursor.Present {
		// A row-value comparison is a single index seek that skips every page
		// at or before the cursor, instead of an OR tree that rescans them.
		query += ` AND (observed,received,rowid)<(?,?,?)`
		args = append(args, cursor.Observed, cursor.Received, cursor.RowID)
	}
	query += ` ORDER BY observed DESC,received DESC,rowid DESC LIMIT ?`
	args = append(args, limit)
	return s.scanObservations(ctx, query, args, records, tuples)
}

// Unlocatable replay (observed IS NULL, replay only) is included conservatively
// rather than dropped: the index groups NULL observed rows and windows them by
// server receipt time, and each record reports observedAt:null.
func (s *Store) unlocatedObservations(ctx context.Context, id string, from, to int64, cursor *observationCursor, limit int, records []ObservationRecord, tuples []observationCursor) ([]ObservationRecord, []observationCursor, error) {
	query := `SELECT rowid,observed,received,expires,replay,data FROM samples INDEXED BY sample_history_ordered WHERE service=? AND observed IS NULL AND replay=1 AND received>=? AND received<=?`
	args := []any{id, from, to}
	if cursor != nil {
		query += ` AND (received,rowid)<(?,?)`
		args = append(args, cursor.Received, cursor.RowID)
	}
	query += ` ORDER BY received DESC,rowid DESC LIMIT ?`
	args = append(args, limit)
	return s.scanObservations(ctx, query, args, records, tuples)
}

func validPathMode(pathMode string) bool {
	switch pathMode {
	case "", "unknown", "direct", "relay", "mixed":
		return true
	default:
		return false
	}
}

// validStoredObservation re-checks a persisted sample against the same contract
// as ingest. Anything else is corruption, not data to be sanitised and exposed.
func validStoredObservation(observation Observation) bool {
	if !statuses[observation.Status] || summaries[observation.Code] == "" || !validPathMode(observation.PathMode) {
		return false
	}
	if observation.LatencyMS != nil && !finiteRange(*observation.LatencyMS, 0, 120000) {
		return false
	}
	return validEvidence(observation.Evidence)
}

func (s *Store) scanObservations(ctx context.Context, query string, args []any, records []ObservationRecord, tuples []observationCursor) ([]ObservationRecord, []observationCursor, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return records, tuples, err
	}
	defer rows.Close()
	for rows.Next() {
		var rowID, received int64
		var observed, expires sql.NullInt64
		var replay int
		var raw string
		if err = rows.Scan(&rowID, &observed, &received, &expires, &replay, &raw); err != nil {
			return records, tuples, err
		}
		var observation Observation
		if json.Unmarshal([]byte(raw), &observation) != nil || !validStoredObservation(observation) {
			// A corrupt or out-of-contract row is an internal error: fail closed
			// with a fixed message and never echo the stored payload.
			return records, tuples, errors.New("invalid stored observation")
		}
		record := ObservationRecord{
			Status:     observation.Status,
			Code:       observation.Code,
			Summary:    summaries[observation.Code],
			ReceivedAt: iso(received),
			PathMode:   observation.PathMode,
			Replay:     replay != 0,
		}
		if record.PathMode == "" {
			record.PathMode = "unknown"
		}
		if observed.Valid {
			stamp := iso(observed.Int64)
			record.ObservedAt = &stamp
		}
		if expires.Valid {
			stamp := iso(expires.Int64)
			record.ExpiresAt = &stamp
		}
		// Latency is only meaningful for a raw healthy/degraded sample.
		if observation.Status == "healthy" || observation.Status == "degraded" {
			record.LatencyMS = observation.LatencyMS
		}
		record.Evidence = observation.Evidence
		records = append(records, record)
		tuples = append(tuples, observationCursor{Present: observed.Valid, Observed: observed.Int64, Received: received, RowID: rowID})
	}
	return records, tuples, rows.Err()
}
