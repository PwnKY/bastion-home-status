package status

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"
	"time"
)

// Independent old scan/sweep algorithm is kept in tests as a semantic oracle.
func scanHistoryOracle(t *testing.T, s *Store, id string, now time.Time, hours int) []Point {
	t.Helper()
	g, _ := makeHistoryGrid(now, hours)
	type sample struct {
		at, expires int64
		state, raw  string
		replay      bool
	}
	rows, err := s.db.Query(`SELECT observed,expires,status,data,replay FROM samples WHERE service=? AND observed>=? AND observed<=? ORDER BY observed,received,rowid LIMIT 75000`, id, g.start-172800000, g.end)
	if err != nil {
		t.Fatal(err)
	}
	var samples []sample
	for rows.Next() {
		var v sample
		if err = rows.Scan(&v.at, &v.expires, &v.state, &v.raw, &v.replay); err != nil {
			t.Fatal(err)
		}
		samples = append(samples, v)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	owner, ttl := s.historyOwner(id)
	rows, err = s.db.Query(`SELECT received FROM receipts WHERE agent=? AND kind='heartbeat' AND received>=? AND received<=? ORDER BY received LIMIT 75000`, owner, g.start-ttl, g.end)
	if err != nil {
		t.Fatal(err)
	}
	var hearts []int64
	for rows.Next() {
		var at int64
		if err = rows.Scan(&at); err != nil {
			t.Fatal(err)
		}
		hearts = append(hearts, at)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	out := make([]Point, 0, g.count)
	j, h := 0, 0
	var latest *sample
	var heart int64
	for i := 0; i < g.count; i++ {
		at := g.start + int64(i)*historyStep
		for j < len(samples) && samples[j].at <= at {
			latest = &samples[j]
			j++
		}
		for h < len(hearts) && hearts[h] <= at {
			heart = hearts[h]
			h++
		}
		point := Point{At: iso(at), Status: "unknown"}
		if latest != nil && latest.expires > at && (latest.replay || heart > 0 && heart+ttl > at) {
			point.Status = latest.state
			if point.Status == "healthy" || point.Status == "degraded" {
				var o Observation
				if err = json.Unmarshal([]byte(latest.raw), &o); err != nil {
					t.Fatal(err)
				}
				point.Value = o.LatencyMS
			}
		}
		out = append(out, point)
	}
	return out
}

func TestIndexedHistoryMatchesScanForGapsReplayExpiryTiesAndMissingLatency(t *testing.T) {
	s, _, _, now := fixture(t)
	r := rand.New(rand.NewPCG(41, 93))
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO samples VALUES(?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	states := []string{"healthy", "degraded", "down", "unknown", "untested", "maintenance"}
	for i := 0; i < 5000; i++ {
		at := now.Add(-time.Duration(r.IntN(9*24*3600)) * time.Second).UnixMilli()
		state := states[r.IntN(len(states))]
		o := Observation{Status: state, Code: "ok", AgeSeconds: number(0), PathMode: "unknown"}
		if i%3 != 0 {
			o.LatencyMS = number(float64(i % 120))
		}
		raw, _ := json.Marshal(o)
		if _, err = stmt.Exec("home", fmt.Sprintf("p-%d", i), "ipv4", at+int64(i%3), at, at+int64(r.IntN(172800))*1000, state, string(raw), i%7 == 0); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3000; i++ {
		at := now.Add(-time.Duration(r.IntN(9*24*3600)) * time.Second).UnixMilli()
		kind := "heartbeat"
		if i%3 == 0 {
			kind = "batch"
		}
		if _, err = tx.Exec(`INSERT INTO receipts VALUES(?,?,?,?)`, "home", fmt.Sprintf("r-%d", i), kind, at); err != nil {
			t.Fatal(err)
		}
	}
	// Equal observed/received uses insertion order, not success preference.
	for i, state := range []string{"healthy", "down", "healthy"} {
		raw := `{"status":"` + state + `","code":"ok","ageSeconds":0}`
		if _, err = stmt.Exec("home", fmt.Sprintf("tie-%d", i), "ipv4", now.UnixMilli(), now.UnixMilli(), now.UnixMilli()+90000, state, raw, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, hours := range []int{24, 168} {
		want := scanHistoryOracle(t, s, "ipv4", now, hours)
		got, err := s.History("ipv4", now, hours)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(want, got) {
			for i := range got {
				if !reflect.DeepEqual(want[i], got[i]) {
					t.Fatalf("range %d point %d: want %#v got %#v", hours, i, want[i], got[i])
				}
			}
		}
		if got[len(got)-1].Value != nil {
			t.Fatal("missing latency inherited from older payload")
		}
	}
}
func TestIndexedHistoryPreservesSampleAndHeartbeatLimits(t *testing.T) {
	s, _, _, now := fixture(t)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	samples, err := tx.Prepare(`INSERT INTO samples VALUES(?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer samples.Close()
	receipts, err := tx.Prepare(`INSERT INTO receipts VALUES(?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer receipts.Close()
	for i := 0; i < historyLimit+2; i++ {
		at := now.UnixMilli() - int64(historyLimit+2-i)
		if _, err = samples.Exec("home", fmt.Sprintf("s-%d", i), "ipv4", at, at, at+90000, "healthy", `{"ageSeconds":0,"latencyMs":12}`, 0); err != nil {
			t.Fatal(err)
		}
		heart := now.UnixMilli() - 95000 + int64(i%1000)
		if i >= historyLimit {
			heart = now.UnixMilli()
		}
		if _, err = receipts.Exec("home", fmt.Sprintf("h-%d", i), "heartbeat", heart); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	want := scanHistoryOracle(t, s, "ipv4", now, 24)
	got, err := s.History("ipv4", now, 24)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) || got[47].Status != "unknown" {
		t.Fatal("history limits changed or later excluded heartbeat became current")
	}
	// Replay ignores the source gate, but cannot expose rows beyond the sample cap.
	if _, err = s.db.Exec(`UPDATE samples SET replay=1,status='down' WHERE rowid<=?`, historyLimit); err != nil {
		t.Fatal(err)
	}
	want = scanHistoryOracle(t, s, "ipv4", now, 24)
	got, err = s.History("ipv4", now, 24)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) || got[47].Status != "down" {
		t.Fatal("sample cap/replay gate changed")
	}
	// A large equal-time burst must select the cap tuple, not scan beyond it
	// or allow excluded later insertion IDs to override the judged failure.
	if _, err = s.db.Exec(`UPDATE samples SET observed=?,received=?`, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	want = scanHistoryOracle(t, s, "ipv4", now, 24)
	got, err = s.History("ipv4", now, 24)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) || got[47].Status != "down" {
		t.Fatal("equal-time ordinal cap changed")
	}
}
func TestHistoryContextsReleaseConnectionAndLegacyDatabaseAddsIndexes(t *testing.T) {
	s, _, _, now := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.HistoryContext(ctx, "ipv4", now, 24); err == nil {
		t.Fatal("cancelled history succeeded")
	}
	if _, err := s.SnapshotContext(ctx, now); err == nil {
		t.Fatal("cancelled snapshot succeeded")
	}
	if err := s.Heartbeat("home", "still-writable", now); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"receipt_history", "sample_history_ordered"} {
		if _, err := s.db.Exec("DROP INDEX " + name); err != nil {
			t.Fatal(err)
		}
	}
	c := s.Config
	s.Close()
	upgraded, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if _, err = upgraded.History("ipv4", now, 168); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = upgraded.db.QueryRow(`SELECT count(*) FROM receipts WHERE id='still-writable'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("upgrade lost data", err)
	}
	// Busy single-connection waits must respect cancellation too.
	tx, err := upgraded.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = upgraded.HistoryContext(ctx, "ipv4", now, 24)
	tx.Rollback()
	if err == nil {
		t.Fatal("blocked query did not honor deadline")
	}
	if err = upgraded.db.Ping(); err != nil {
		t.Fatal(err)
	}
}
