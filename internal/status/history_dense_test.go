package status

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Generated workload only: 60 services, 8 independent sources, 156000 samples
// and 64800 receipts. No real deployment config, database or evidence is used.
func denseStore(t testing.TB) (*Store, time.Time) {
	return denseStoreRows(t, 2600, 5500)
}
func denseStoreRows(t testing.TB, perService, batchReceipts int) (*Store, time.Time) {
	t.Helper()
	dir := t.TempDir()
	c := Config{Database: filepath.Join(dir, "dense.sqlite")}
	for i := 0; i < 8; i++ {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			t.Fatal(err)
		}
		secret := filepath.Join(dir, fmt.Sprintf("credential-%d", i))
		if err := os.WriteFile(secret, []byte(hex.EncodeToString(raw)), 0600); err != nil {
			t.Fatal(err)
		}
		c.Agents = append(c.Agents, Agent{ID: fmt.Sprintf("source-%d", i), Label: fmt.Sprintf("生成来源 %d", i), Kind: "family", SecretFile: secret, IntervalSeconds: 30, StaleAfterSeconds: 90})
	}
	for i := 0; i < 60; i++ {
		c.Services = append(c.Services, Definition{ID: fmt.Sprintf("check-%d", i), Name: fmt.Sprintf("生成观测 %d", i), Group: "network", CollectorID: fmt.Sprintf("source-%d", i%8), Scope: "仅生成的密集查询负载", StaleAfterSeconds: 90})
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
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
	raw, _ := json.Marshal(Observation{Status: "healthy", Code: "ok", AgeSeconds: number(0), LatencyMS: number(8), PathMode: "unknown"})
	for i, d := range c.Services {
		for j := 0; j < perService; j++ {
			at := now.Add(-time.Duration(perService-1-j) * 30 * time.Second).UnixMilli()
			if _, err = samples.Exec(d.CollectorID, fmt.Sprintf("s-%d-%d", i, j), d.ID, at, at, at+90000, "healthy", string(raw), 0); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = tx.Exec(`INSERT INTO current VALUES(?,?,?,?,?,?,?)`, d.ID, now.UnixMilli(), now.Add(90*time.Second).UnixMilli(), "healthy", 0, 1, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for i, a := range c.Agents {
		for j := 0; j < perService; j++ {
			at := now.Add(-time.Duration(perService-1-j) * 30 * time.Second).UnixMilli()
			if _, err = receipts.Exec(a.ID, fmt.Sprintf("h-%d-%d", i, j), "heartbeat", at); err != nil {
				t.Fatal(err)
			}
		}
		for j := 0; j < batchReceipts; j++ {
			if _, err = receipts.Exec(a.ID, fmt.Sprintf("b-%d-%d", i, j), "batch", now.UnixMilli()-int64(j)*13000); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = tx.Exec(`UPDATE collectors SET heartbeat=? WHERE id=?`, now.UnixMilli(), a.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return s, now
}

// 1.56 million samples / 680000 receipts, spanning nine days at 30 seconds.
func BenchmarkGrowingSnapshot(b *testing.B) {
	s, now := denseStoreRows(b, 26000, 59000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Snapshot(now); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDenseSnapshot(b *testing.B) {
	s, now := denseStore(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Snapshot(now); err != nil {
			b.Fatal(err)
		}
	}
}
