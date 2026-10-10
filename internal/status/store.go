package status

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db     *sql.DB
	Config Config
}

func Open(c Config) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(c.Database), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(c.Database, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	file.Close()
	db, err := sql.Open("sqlite", c.Database)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	schema := `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA synchronous=NORMAL;
 CREATE TABLE IF NOT EXISTS collectors(id TEXT PRIMARY KEY, heartbeat INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS receipts(agent TEXT, id TEXT, kind TEXT, received INTEGER, PRIMARY KEY(agent,id,kind));
 CREATE TABLE IF NOT EXISTS samples(agent TEXT, id TEXT, service TEXT, received INTEGER, observed INTEGER, expires INTEGER, status TEXT, data TEXT, replay INTEGER NOT NULL, PRIMARY KEY(agent,id));
 CREATE INDEX IF NOT EXISTS sample_history ON samples(service,observed);
 CREATE TABLE IF NOT EXISTS current(service TEXT PRIMARY KEY, observed INTEGER, expires INTEGER, status TEXT, failures INTEGER, successes INTEGER, data TEXT);
 CREATE TABLE IF NOT EXISTS incidents(id INTEGER PRIMARY KEY AUTOINCREMENT, target TEXT, title TEXT, description TEXT, started INTEGER, resolved INTEGER);
 CREATE INDEX IF NOT EXISTS incident_active ON incidents(target,resolved);
 CREATE TABLE IF NOT EXISTS hourly(service TEXT, hour INTEGER, healthy INTEGER, degraded INTEGER, down INTEGER, unknown INTEGER, total INTEGER, PRIMARY KEY(service,hour));`
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	for _, a := range c.Agents {
		if _, err = db.Exec(`INSERT OR IGNORE INTO collectors(id) VALUES(?)`, a.ID); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db, c}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Heartbeat(agent, id string, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.Exec(`INSERT OR IGNORE INTO receipts VALUES(?,?,?,?)`, agent, id, "heartbeat", now.UnixMilli())
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n > 0 {
		if _, err = tx.Exec(`UPDATE collectors SET heartbeat=? WHERE id=?`, now.UnixMilli(), agent); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE incidents SET resolved=? WHERE target=? AND resolved IS NULL`, now.UnixMilli(), "collector:"+agent); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Ingest(agent string, b Batch, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.Exec(`INSERT OR IGNORE INTO receipts VALUES(?,?,?,?)`, agent, b.ID, "batch", now.UnixMilli())
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return tx.Commit()
	}
	for _, o := range b.Samples {
		d, _ := s.Config.definition(o.ServiceID, agent)
		var observed, expires any
		at := int64(0)
		if o.AgeSeconds != nil {
			at = now.Add(-time.Duration(*o.AgeSeconds * float64(time.Second))).UnixMilli()
			observed = at
			expires = at + int64(d.StaleAfterSeconds)*1000
		}
		raw, _ := json.Marshal(o)
		r, err = tx.Exec(`INSERT OR IGNORE INTO samples VALUES(?,?,?,?,?,?,?,?,?)`, agent, o.ID, o.ServiceID, now.UnixMilli(), observed, expires, o.Status, string(raw), boolInt(b.Replay))
		if err != nil {
			return err
		}
		n, _ = r.RowsAffected()
		if n == 0 {
			continue
		}
		if observed != nil {
			_, err = tx.Exec(`INSERT INTO hourly VALUES(?,?,?,?,?,?,?) ON CONFLICT(service,hour) DO UPDATE SET healthy=healthy+excluded.healthy,degraded=degraded+excluded.degraded,down=down+excluded.down,unknown=unknown+excluded.unknown,total=total+1`, o.ServiceID, (at/3600000)*3600000, boolInt(o.Status == "healthy"), boolInt(o.Status == "degraded"), boolInt(o.Status == "down"), boolInt(o.Status == "unknown"), 1)
			if err != nil {
				return err
			}
		}
		// Replay enters evidence/history only, even if its reported timestamp is newer.
		if b.Replay || observed == nil || *o.AgeSeconds >= float64(d.StaleAfterSeconds) {
			continue
		}
		var lastAt int64
		var previous string
		var fails, successes int
		err = tx.QueryRow(`SELECT observed,status,failures,successes FROM current WHERE service=?`, o.ServiceID).Scan(&lastAt, &previous, &fails, &successes)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if at <= lastAt {
			continue
		}
		judged := o
		switch o.Status {
		case "down":
			fails++
			successes = 0
			if fails < 3 && previous != "down" {
				judged.Status = "unknown"
				judged.Code = "pending"
			}
		case "healthy":
			successes++
			fails = 0
			if previous == "down" && successes < 2 {
				judged.Status = "down"
				judged.Code = "recovering"
			}
		default:
			fails = 0
			successes = 0
		}
		// The raw JSON preserves probe evidence; history uses the same judged state as current.
		if _, err = tx.Exec(`UPDATE samples SET status=? WHERE agent=? AND id=?`, judged.Status, agent, o.ID); err != nil {
			return err
		}
		judgedJSON, _ := json.Marshal(judged)
		_, err = tx.Exec(`INSERT INTO current VALUES(?,?,?,?,?,?,?) ON CONFLICT(service) DO UPDATE SET observed=excluded.observed,expires=excluded.expires,status=excluded.status,failures=excluded.failures,successes=excluded.successes,data=excluded.data`, o.ServiceID, at, expires, judged.Status, fails, successes, string(judgedJSON))
		if err != nil {
			return err
		}
		if judged.Status == "down" || judged.Status == "degraded" {
			_, err = tx.Exec(`INSERT INTO incidents(target,title,description,started) SELECT ?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM incidents WHERE target=? AND resolved IS NULL)`, o.ServiceID, d.Name+"观测异常", summaries[judged.Code], now.UnixMilli(), o.ServiceID)
		} else if judged.Status == "healthy" {
			_, err = tx.Exec(`UPDATE incidents SET resolved=? WHERE target=? AND resolved IS NULL`, now.UnixMilli(), o.ServiceID)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func iso(ms int64) string       { return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano) }
func number(v float64) *float64 { return &v }
func (s *Store) Collectors(now time.Time) ([]Collector, error) {
	list := make([]Collector, 0, len(s.Config.Agents))
	for _, a := range s.Config.Agents {
		var at int64
		if err := s.db.QueryRow(`SELECT heartbeat FROM collectors WHERE id=?`, a.ID).Scan(&at); err != nil {
			return nil, err
		}
		c := Collector{ID: a.ID, Label: a.Label, Kind: a.Kind, IntervalSeconds: a.IntervalSeconds, StaleAfterSeconds: a.StaleAfterSeconds, Stale: true}
		if at > 0 {
			stamp := iso(at)
			c.LastHeartbeatAt = &stamp
			age := max(0, float64(now.UnixMilli()-at)/1000)
			c.HeartbeatAgeSeconds = &age
			c.Stale = age >= float64(a.StaleAfterSeconds)
		}
		list = append(list, c)
	}
	return list, nil
}
func (s *Store) History(id string, now time.Time, hours int) ([]Point, error) {
	step := int64(1800000)
	count := hours * 2
	start := now.UnixMilli() - int64(count-1)*step
	rows, err := s.db.Query(`SELECT observed,expires,status,data,replay FROM samples WHERE service=? AND observed>=? AND observed<=? ORDER BY observed,received LIMIT 75000`, id, start-172800000, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type sample struct {
		at, expires int64
		status      string
		latency     *float64
		replay      bool
	}
	samples := []sample{}
	for rows.Next() {
		var e sample
		var raw string
		if err = rows.Scan(&e.at, &e.expires, &e.status, &raw, &e.replay); err != nil {
			return nil, err
		}
		var o Observation
		if err = json.Unmarshal([]byte(raw), &o); err != nil {
			return nil, err
		}
		e.latency = o.LatencyMS
		samples = append(samples, e)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	// Long-lived probes must not fill history across a collector's missing heartbeat.
	var owner string
	var sourceTTL int64
	for _, d := range s.Config.Services {
		if d.ID == id {
			owner = d.CollectorID
		}
	}
	for _, a := range s.Config.Agents {
		if a.ID == owner {
			sourceTTL = int64(a.StaleAfterSeconds) * 1000
		}
	}
	hearts, err := s.db.Query(`SELECT received FROM receipts WHERE agent=? AND kind='heartbeat' AND received>=? AND received<=? ORDER BY received LIMIT 75000`, owner, start-sourceTTL, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	heartbeats := []int64{}
	for hearts.Next() {
		var at int64
		if err = hearts.Scan(&at); err != nil {
			hearts.Close()
			return nil, err
		}
		heartbeats = append(heartbeats, at)
	}
	if err = hearts.Err(); err != nil {
		hearts.Close()
		return nil, err
	}
	hearts.Close()
	points := make([]Point, 0, count)
	j, h := 0, 0
	var lastHeartbeat int64
	var latest *sample
	for i := 0; i < count; i++ {
		at := start + int64(i)*step
		for j < len(samples) && samples[j].at <= at {
			latest = &samples[j]
			j++
		}
		for h < len(heartbeats) && heartbeats[h] <= at {
			lastHeartbeat = heartbeats[h]
			h++
		}
		p := Point{At: iso(at), Status: "unknown"}
		if latest != nil && latest.expires > at && (latest.replay || lastHeartbeat > 0 && lastHeartbeat+sourceTTL > at) {
			p.Status = latest.status
			if latest.status == "healthy" || latest.status == "degraded" {
				p.Value = latest.latency
			}
		}
		points = append(points, p)
	}
	return points, nil
}
func availability(points []Point) (*float64, *float64) {
	tested, good, expected := 0, 0, 0
	for _, p := range points {
		if p.Status != "maintenance" {
			expected++
		}
		if p.Status == "healthy" || p.Status == "degraded" || p.Status == "down" {
			tested++
		}
		if p.Status == "healthy" || p.Status == "degraded" {
			good++
		}
	}
	var up, coverage *float64
	if tested > 0 {
		up = number(float64(good) * 100 / float64(tested))
	}
	if expected > 0 {
		coverage = number(float64(tested) / float64(expected))
	}
	return up, coverage
}
func (s *Store) Snapshot(now time.Time) (Snapshot, error) {
	collectors, err := s.Collectors(now)
	if err != nil {
		return Snapshot{}, err
	}
	byAgent := map[string]Collector{}
	family := Collector{Stale: true, StaleAfterSeconds: 90, IntervalSeconds: 30}
	for _, a := range collectors {
		byAgent[a.ID] = a
		if a.Kind == "family" && family.ID == "" {
			family = a
		}
	}
	snapshot := Snapshot{Version: 1, Source: "live", SampledAt: iso(now.UnixMilli()), ServerTime: iso(now.UnixMilli()), Disclaimer: "真实检测摘要；本机健康和家庭侧观测不代表外部用户业务已验证。", Collectors: collectors, Collector: family, Services: []Service{}, Metrics: Metrics{LatencyTrend: []Point{}, CoverageTrend: []Point{}}}
	for _, d := range s.Config.Services {
		c := byAgent[d.CollectorID]
		entry := Service{Definition: d, Status: "unknown", Summary: summaries["stale"], ProbeLabel: c.Label, PathMode: "unknown", Detail: []Detail{{"结论限制", d.Scope}, {"补充限制", "检测结果只覆盖此探针；不能推断所有用户或业务均正常。"}}}
		var at, expires int64
		var raw string
		err = s.db.QueryRow(`SELECT observed,expires,status,data FROM current WHERE service=?`, d.ID).Scan(&at, &expires, &entry.Status, &raw)
		if err != nil && err != sql.ErrNoRows {
			return Snapshot{}, err
		}
		if err == nil {
			var o Observation
			if err = json.Unmarshal([]byte(raw), &o); err != nil {
				return Snapshot{}, err
			}
			stamp := iso(at)
			entry.ObservedAt = &stamp
			entry.ObservedAgeSeconds = number(max(0, float64(now.UnixMilli()-at)/1000))
			entry.Summary = summaries[o.Code]
			if now.UnixMilli() >= expires || c.Stale {
				if entry.Status != "untested" {
					entry.Status = "unknown"
				}
				entry.Summary = summaries["stale"]
			} else {
				entry.LatencyMS = o.LatencyMS
				entry.PathMode = o.PathMode
				entry.ReadyConnections = o.ReadyConnections
				entry.UsagePercent = o.UsagePercent
				if entry.PathMode == "" {
					entry.PathMode = "unknown"
				}
			}
		}
		if d.ID == "collectors" {
			entry.ObservedAt = c.LastHeartbeatAt
			entry.ObservedAgeSeconds = c.HeartbeatAgeSeconds
			entry.Summary = "采集心跳的服务端接收记录；不代表业务验证。"
			if c.Stale {
				entry.Status = "unknown"
			} else {
				entry.Status = "healthy"
			}
		}
		entry.History, err = s.History(d.ID, now, 24)
		if err != nil {
			return Snapshot{}, err
		}
		entry.Availability24h, entry.Coverage = availability(entry.History)
		seven, err := s.History(d.ID, now, 168)
		if err != nil {
			return Snapshot{}, err
		}
		entry.Availability7d, _ = availability(seven)
		snapshot.Services = append(snapshot.Services, entry)
		if d.ID == "ipv4" {
			for _, p := range entry.History {
				if p.Value != nil {
					snapshot.Metrics.LatencyTrend = append(snapshot.Metrics.LatencyTrend, p)
				}
			}
		}
	}
	snapshot.Incidents, err = s.Incidents()
	return snapshot, err
}
func (s *Store) Incidents() ([]Incident, error) {
	rows, err := s.db.Query(`SELECT id,target,title,description,started,resolved FROM incidents ORDER BY started DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Incident{}
	for rows.Next() {
		var id int64
		var target, title, desc string
		var start int64
		var end sql.NullInt64
		if err = rows.Scan(&id, &target, &title, &desc, &start, &end); err != nil {
			return nil, err
		}
		ids := []string{}
		if len(target) > 10 && target[:10] == "collector:" {
			for _, d := range s.Config.Services {
				if d.CollectorID == target[10:] {
					ids = append(ids, d.ID)
				}
			}
		} else {
			ids = append(ids, target)
		}
		e := Incident{ID: fmt.Sprintf("event-%d", id), Title: title, Description: desc, Status: "investigating", StartedAt: iso(start), AffectedServiceIDs: ids}
		if end.Valid {
			stamp := iso(end.Int64)
			e.ResolvedAt = &stamp
			e.Status = "resolved"
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) Reconcile(now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, a := range s.Config.Agents {
		var at int64
		if err = tx.QueryRow(`SELECT heartbeat FROM collectors WHERE id=?`, a.ID).Scan(&at); err != nil {
			return err
		}
		if at > 0 && now.UnixMilli()-at >= int64(a.StaleAfterSeconds)*1000 {
			target := "collector:" + a.ID
			_, err = tx.Exec(`INSERT INTO incidents(target,title,description,started) SELECT ?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM incidents WHERE target=? AND resolved IS NULL)`, target, a.Label+"心跳缺失", "采集观测不可用；不据此判断业务或家庭出口中断。", now.UnixMilli(), target)
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
func (s *Store) Cleanup(now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cut := now.Add(-time.Duration(s.Config.RetentionDays) * 24 * time.Hour).UnixMilli()
	for _, query := range []string{`DELETE FROM samples WHERE received<?`, `DELETE FROM receipts WHERE received<?`, `DELETE FROM incidents WHERE resolved IS NOT NULL AND resolved<?`} {
		if _, err = tx.Exec(query, cut); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`DELETE FROM hourly WHERE hour<?`, now.Add(-365*24*time.Hour).UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}
