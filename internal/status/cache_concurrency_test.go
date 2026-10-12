package status

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSnapshotFlightDoesNotBlockWritesOrRepopulateInvalidatedCache(t *testing.T) {
	s, h, token, _ := fixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	var builds atomic.Int32
	h.buildSnapshot = func(ctx context.Context, at time.Time) (Snapshot, error) {
		v, err := s.SnapshotContext(ctx, at)
		if builds.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return Snapshot{}, ctx.Err()
			}
		}
		return v, err
	}
	result := make(chan error, 1)
	go func() { _, err := h.snapshot(context.Background()); result <- err }()
	<-started
	// Another disconnected browser must not wait for the shared computation.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := h.snapshot(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("waiter cancellation", err)
	}
	write := make(chan int, 1)
	go func() { write <- request(h, "POST", "/api/v1/heartbeat", token, Heartbeat{ID: "during-flight"}).Code }()
	select {
	case code := <-write:
		if code != 200 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("ACK waited for snapshot mutex")
	}
	if w := request(h, "POST", "/api/v1/ingest", token, batch("during-b", "during-s", "healthy", 0)); w.Code != 200 {
		t.Fatal(w.Code)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	h.cacheMu.Lock()
	cached := h.cached
	h.cacheMu.Unlock()
	if cached != nil {
		t.Fatal("old flight overwrote write invalidation")
	}
	v, err := h.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.Services[0].Status != "healthy" || builds.Load() != 2 {
		t.Fatal("new write not visible", builds.Load())
	}
	_, err = h.snapshot(context.Background())
	if err != nil || builds.Load() != 2 {
		t.Fatal("warm result not shared", err)
	}
}

func TestSnapshotCacheDatesFromCompletionButExpiresMeasurementsAtResponse(t *testing.T) {
	s, h, _, now := fixture(t)
	past := now.Add(-89 * time.Second)
	if err := s.Heartbeat("home", "h", past); err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest("home", batch("b", "s", "healthy", 0), past); err != nil {
		t.Fatal(err)
	}
	var clock atomic.Int64
	clock.Store(now.UnixMilli())
	h.Now = func() time.Time { return time.UnixMilli(clock.Load()) }
	started, release := make(chan struct{}), make(chan struct{})
	h.buildSnapshot = func(ctx context.Context, at time.Time) (Snapshot, error) {
		v, err := s.SnapshotContext(ctx, at)
		close(started)
		<-release
		return v, err
	}
	out := make(chan Snapshot, 1)
	go func() { v, _ := h.snapshot(context.Background()); out <- v }()
	<-started
	later := now.Add(2 * time.Second)
	clock.Store(later.UnixMilli())
	close(release)
	v := <-out
	if v.Services[0].Status != "unknown" || v.Services[0].LatencyMS != nil || !v.Collectors[0].Stale || v.ServerTime != iso(later.UnixMilli()) {
		t.Fatal("slow computation renewed stale proof", v.Services[0])
	}
	if !h.cachedAt.Equal(later) || h.cached.Services[0].Status != "healthy" {
		t.Fatal("cache origin/copy mutation incorrect")
	}
	warm, err := h.snapshot(context.Background())
	if err != nil || warm.Services[0].Status != "unknown" {
		t.Fatal(err)
	}
}

func TestDenseColdOpenAndConcurrentReadsStayWithinBrowserBudget(t *testing.T) {
	s, now := denseStore(t)
	h := NewHandler(s)
	h.Now = func() time.Time { return now }
	var builds atomic.Int32
	h.buildSnapshot = func(ctx context.Context, at time.Time) (Snapshot, error) {
		builds.Add(1)
		return s.SnapshotContext(ctx, at)
	}
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := request(h, "GET", "/api/v1/status", "", nil)
			if w.Code != 200 {
				t.Errorf("cold status HTTP %d", w.Code)
				return
			}
			var v Snapshot
			if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
				t.Error(err)
				return
			}
			if len(v.Services) != 60 || len(v.Collectors) != 8 || v.Services[0].Status != "healthy" || len(v.Services[0].History) != 48 {
				t.Error("dense snapshot lost services or history")
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	if elapsed >= 8*time.Second {
		t.Fatalf("dense cold opens took %s", elapsed)
	}
	if builds.Load() != 1 {
		t.Fatal("concurrent cold opens amplified history work", builds.Load())
	}
	t.Logf("24 dense cold opens: %s; shared builds: %d", elapsed, builds.Load())
}
