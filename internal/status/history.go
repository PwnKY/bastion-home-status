package status

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const historyStep = int64(1800000)
const historyLimit = 75000
const historyGridSQL = `WITH RECURSIVE grid(n,at) AS (
 SELECT 0,? UNION ALL SELECT n+1,at+1800000 FROM grid WHERE n+1<?
) `

type historyGrid struct {
	start, end int64
	count      int
}

func makeHistoryGrid(now time.Time, hours int) (historyGrid, error) {
	if hours < 1 || hours > 168 {
		return historyGrid{}, errors.New("invalid history range")
	}
	count := hours * 2
	return historyGrid{now.UnixMilli() - int64(count-1)*historyStep, now.UnixMilli(), count}, nil
}
func (s *Store) historyOwner(id string) (string, int64) {
	for _, d := range s.Config.Services {
		if d.ID == id {
			for _, a := range s.Config.Agents {
				if a.ID == d.CollectorID {
					return a.ID, int64(a.StaleAfterSeconds) * 1000
				}
			}
		}
	}
	return "", 0
}

// Seek only the latest heartbeat at each window endpoint. The index excludes
// batch receipts; a shared source grid is reused across all of its services.
func (s *Store) heartbeatGrid(ctx context.Context, owner string, ttl int64, g historyGrid) ([]sql.NullInt64, error) {
	var cutoff any
	var last int64
	err := s.db.QueryRowContext(ctx, `SELECT received FROM receipts INDEXED BY receipt_history WHERE agent=? AND kind='heartbeat' AND received>=? AND received<=? ORDER BY received LIMIT 1 OFFSET 74999`, owner, g.start-ttl, g.end).Scan(&last)
	if err == nil {
		cutoff = last
	} else if err != sql.ErrNoRows {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, historyGridSQL+`SELECT (
 SELECT received FROM receipts INDEXED BY receipt_history
 WHERE agent=? AND kind='heartbeat' AND received>=? AND received<=grid.at
 AND received<=coalesce(?,9223372036854775807)
 ORDER BY received DESC LIMIT 1
) FROM grid ORDER BY n`, g.start, g.count, owner, g.start-ttl, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]sql.NullInt64, 0, g.count)
	for rows.Next() {
		var v sql.NullInt64
		if err = rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Preserve the old bounded, endpoint-sampled semantics, including the first
// 75000 input rows, the two-day carry-in, expiry, replay and source heartbeat.
// Only selected payloads are decoded. Window seeks and payload work are bounded
// by chart points; the ordinal-cap lookup is index-only and scans at most 75000
// entries, rather than decoding the entire raw observation range.
func (s *Store) sampleGrid(ctx context.Context, id string, g historyGrid, ttl int64, hearts []sql.NullInt64) ([]Point, error) {
	if len(hearts) != g.count {
		return nil, errors.New("invalid heartbeat grid")
	}
	var cutoffAt, cutoffReceived, cutoffID any
	var at, received, rowID int64
	err := s.db.QueryRowContext(ctx, `SELECT observed,received,rowid FROM samples INDEXED BY sample_history_ordered WHERE service=? AND observed>=? AND observed<=? ORDER BY observed,received,rowid LIMIT 1 OFFSET 74999`, id, g.start-172800000, g.end).Scan(&at, &received, &rowID)
	if err == nil {
		cutoffAt, cutoffReceived, cutoffID = at, received, rowID
	} else if err != sql.ErrNoRows {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, historyGridSQL+`SELECT grid.at,s.rowid,s.expires,s.status,s.data,s.replay
 FROM grid LEFT JOIN samples AS s ON s.rowid=(
  SELECT rowid FROM samples INDEXED BY sample_history_ordered
  WHERE service=? AND observed>=? AND (observed,received,rowid)<=(
   min(grid.at,coalesce(?,grid.at)),
   CASE WHEN ?<=grid.at THEN ? ELSE 9223372036854775807 END,
   CASE WHEN ?<=grid.at THEN ? ELSE 9223372036854775807 END
  )
  ORDER BY observed DESC,received DESC,rowid DESC LIMIT 1
 ) ORDER BY grid.n`, g.start, g.count, id, g.start-172800000, cutoffAt, cutoffAt, cutoffReceived, cutoffAt, cutoffID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Point, 0, g.count)
	var decodedID int64
	var observation Observation
	for rows.Next() {
		var endpoint int64
		var id, expires, replay sql.NullInt64
		var state, raw sql.NullString
		if err = rows.Scan(&endpoint, &id, &expires, &state, &raw, &replay); err != nil {
			return nil, err
		}
		point := Point{At: iso(endpoint), Status: "unknown"}
		if id.Valid {
			if id.Int64 != decodedID {
				observation = Observation{}
				if err = json.Unmarshal([]byte(raw.String), &observation); err != nil {
					return nil, err
				}
				decodedID = id.Int64
			}
			heart := hearts[len(out)]
			if expires.Valid && expires.Int64 > endpoint && (replay.Int64 != 0 || heart.Valid && heart.Int64 > 0 && heart.Int64+ttl > endpoint) {
				point.Status = state.String
				if state.String == "healthy" || state.String == "degraded" {
					point.Value = observation.LatencyMS
				}
			}
		}
		out = append(out, point)
	}
	return out, rows.Err()
}

func (s *Store) History(id string, now time.Time, hours int) ([]Point, error) {
	return s.HistoryContext(context.Background(), id, now, hours)
}
func (s *Store) HistoryContext(ctx context.Context, id string, now time.Time, hours int) ([]Point, error) {
	g, err := makeHistoryGrid(now, hours)
	if err != nil {
		return nil, err
	}
	owner, ttl := s.historyOwner(id)
	hearts, err := s.heartbeatGrid(ctx, owner, ttl, g)
	if err != nil {
		return nil, err
	}
	return s.sampleGrid(ctx, id, g, ttl, hearts)
}
