package status

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func rawObservation(status, code string, latency any) string {
	payload := map[string]any{"status": status, "code": code, "ageSeconds": 0, "pathMode": "direct"}
	if latency != nil {
		payload["latencyMs"] = latency
	}
	raw, _ := json.Marshal(payload)
	return string(raw)
}

func insertRaw(t *testing.T, s *Store, id string, observed, received int64, observedNull bool, replay int, raw string) {
	t.Helper()
	var observedValue, expiresValue any
	if !observedNull {
		observedValue = observed
		expiresValue = observed + 90000
	}
	if _, err := s.db.Exec(`INSERT INTO samples VALUES(?,?,?,?,?,?,?,?,?)`, "home", id, "ipv4", received, observedValue, expiresValue, "healthy", raw, replay); err != nil {
		t.Fatal(err)
	}
}

func decodeObservations(t *testing.T, w *httptest.ResponseRecorder) ObservationResponse {
	t.Helper()
	var out ObservationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err, w.Body.String())
	}
	return out
}

func TestTailscaleTSMPCodesAccepted(t *testing.T) {
	s, _, _, _ := fixture(t)
	for _, code := range []string{"tsmp_error", "tsmp_unavailable", "tsmp_self", "tsmp_absent", "tsmp_offline"} {
		b := batch("id-"+code, "sample-"+code, "unknown", 0)
		b.Samples[0].Code = code
		if err := validateBatch(b, s.Config, "home"); err != nil {
			t.Fatalf("fixed code %q rejected: %v", code, err)
		}
	}
}

func TestObservationsWindowInclusiveAndNewestFirst(t *testing.T) {
	s, h, token, now := fixture(t)
	base := now.UnixMilli()
	// Exactly at the window edges and inside; one just outside each edge.
	insertRaw(t, s, "outside-before", base-3600001, base-3600001, false, 0, rawObservation("healthy", "ok", 1.0))
	insertRaw(t, s, "at-from", base-3600000, base-3600000, false, 0, rawObservation("healthy", "ok", 2.0))
	insertRaw(t, s, "middle", base-1800000, base-1800000, false, 0, rawObservation("down", "icmp_error", 3.0))
	insertRaw(t, s, "at-to", base, base, false, 0, rawObservation("healthy", "ok", 4.0))
	insertRaw(t, s, "outside-after", base+1, base+1, false, 0, rawObservation("healthy", "ok", 5.0))

	from := now.Add(-time.Hour)
	page, err := s.ObservationsContext(context.Background(), "ipv4", from, now, 1000, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 3 {
		t.Fatalf("window size: %+v", page.Records)
	}
	if page.Records[0].Status != "healthy" || page.Records[0].LatencyMS == nil || *page.Records[0].LatencyMS != 4 {
		t.Fatalf("newest record wrong: %+v", page.Records[0])
	}
	// A raw down keeps its raw status/code, and latency is withheld for it.
	if page.Records[1].Status != "down" || page.Records[1].Code != "icmp_error" || page.Records[1].LatencyMS != nil {
		t.Fatal("raw failure or latency suppression wrong", page.Records[1])
	}
	if page.Records[2].Status != "healthy" || page.Records[2].LatencyMS == nil || *page.Records[2].LatencyMS != 2 {
		t.Fatalf("oldest record wrong: %+v", page.Records[2])
	}

	// Handler default window is the last hour.
	w := request(h, "GET", "/api/v1/observations?service=ipv4", token, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	out := decodeObservations(t, w)
	if out.ServiceID != "ipv4" || out.From != from.UTC().Format(time.RFC3339Nano) || out.To != now.UTC().Format(time.RFC3339Nano) {
		t.Fatal(out)
	}
	if len(out.Observations) != 3 || out.Truncated || out.NextCursor != "" {
		t.Fatal(out)
	}
}

func TestObservationsPaginationTiesStable(t *testing.T) {
	s, _, _, now := fixture(t)
	at := now.UnixMilli()
	// Identical observed and received: newest first must fall back to rowid.
	for i := 1; i <= 5; i++ {
		insertRaw(t, s, "tie-"+string(rune('0'+i)), at, at, false, 0, rawObservation("healthy", "ok", float64(i)))
	}
	var seen []float64
	cursor := ""
	pages := 0
	for {
		page, err := s.ObservationsContext(context.Background(), "ipv4", now.Add(-time.Hour), now, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if pages > 5 {
			t.Fatal("pagination did not terminate")
		}
		for _, record := range page.Records {
			seen = append(seen, *record.LatencyMS)
		}
		if !page.Truncated {
			if page.NextCursor != "" {
				t.Fatal("untruncated page carried a cursor")
			}
			break
		}
		if page.NextCursor == "" {
			t.Fatal("truncated page without cursor")
		}
		cursor = page.NextCursor
	}
	want := []float64{5, 4, 3, 2, 1}
	if len(seen) != len(want) {
		t.Fatalf("seen %v", seen)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("tie order %v want %v", seen, want)
		}
	}
}

func TestObservationsReplayAndUnlocatable(t *testing.T) {
	s, _, _, now := fixture(t)
	base := now.UnixMilli()
	insertRaw(t, s, "live", base-1000, base-1000, false, 0, rawObservation("healthy", "ok", 1.0))
	insertRaw(t, s, "replay-located", base-2000, base-500, false, 1, rawObservation("down", "icmp_error", nil))
	insertRaw(t, s, "replay-unlocated", 0, base-100, true, 1, rawObservation("unknown", "stale", nil))
	// Unlocatable replay received outside the window must be omitted.
	insertRaw(t, s, "replay-old", 0, base-int64(2*time.Hour/time.Millisecond), true, 1, rawObservation("unknown", "stale", nil))

	page, err := s.ObservationsContext(context.Background(), "ipv4", now.Add(-time.Hour), now, 1000, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 3 {
		t.Fatalf("replay selection: %+v", page.Records)
	}
	if page.Records[1].Replay != true || page.Records[1].ObservedAt == nil {
		t.Fatal("located replay not clearly historical", page.Records[1])
	}
	last := page.Records[2]
	if last.ObservedAt != nil || !last.Replay || last.Status != "unknown" {
		t.Fatal("unlocatable replay must be reported with null observedAt", last)
	}
	for _, record := range page.Records {
		if record.Status == "down" && record.LatencyMS != nil {
			t.Fatal("replay failure gained latency")
		}
	}
}

func TestObservationsRawFailureSurvivesConfirmation(t *testing.T) {
	s, h, token, now := fixture(t)
	_ = s.Heartbeat("home", "h", now)
	down := batch("b1", "s1", "down", 0)
	down.Samples[0].Code = "icmp_error"
	if err := s.Ingest("home", down, now); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot(now)
	if err != nil || snap.Services[0].Status != "unknown" {
		t.Fatal("hysteresis not applied", err, snap.Services[0])
	}
	out := decodeObservations(t, request(h, "GET", "/api/v1/observations?service=ipv4", token, nil))
	if len(out.Observations) != 1 || out.Observations[0].Status != "down" || out.Observations[0].Code != "icmp_error" {
		t.Fatal("raw single failure hidden by confirmation", out)
	}
	for i := 0; i < 2; i++ {
		id := string(rune('c' + i))
		next := batch("batch-"+id, "sample-"+id, "down", 0)
		next.Samples[0].Code = "icmp_error"
		if err = s.Ingest("home", next, now.Add(time.Duration(i+1)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	snap, _ = s.Snapshot(now.Add(3 * time.Second))
	if snap.Services[0].Status != "down" {
		t.Fatal("confirmation threshold", snap.Services[0])
	}
	h.Now = func() time.Time { return now.Add(3 * time.Second) }
	out = decodeObservations(t, request(h, "GET", "/api/v1/observations?service=ipv4", token, nil))
	if len(out.Observations) != 3 {
		t.Fatal("confirmed failures lost", out)
	}
}

func TestObservationsInvalidParamsAndCursors(t *testing.T) {
	_, h, token, now := fixture(t)
	validFrom := now.Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	validTo := now.UTC().Format(time.RFC3339Nano)
	if w := request(h, "GET", "/api/v1/observations?service=missing", token, nil); w.Code != 404 {
		t.Fatalf("unknown service: %d", w.Code)
	}
	for name, path := range map[string]string{
		"bad-from":        "/api/v1/observations?service=ipv4&from=not-a-time",
		"bad-to":          "/api/v1/observations?service=ipv4&to=not-a-time",
		"future-to":       "/api/v1/observations?service=ipv4&to=" + now.Add(time.Minute).UTC().Format(time.RFC3339Nano),
		"reversed":        "/api/v1/observations?service=ipv4&from=" + validTo + "&to=" + validFrom,
		"over-seven-days": "/api/v1/observations?service=ipv4&from=" + now.Add(-8*24*time.Hour).UTC().Format(time.RFC3339Nano) + "&to=" + validTo,
		"bad-cursor":      "/api/v1/observations?service=ipv4&cursor=!!!!",
		"unknown-cursor":  "/api/v1/observations?service=ipv4&cursor=" + encodeObservationCursor(observationCursor{Present: true, Observed: 1, Received: 1, RowID: 1}),
		"zero-row-cursor": "/api/v1/observations?service=ipv4&cursor=" + encodeObservationCursor(observationCursor{Present: true, Observed: now.UnixMilli(), Received: now.UnixMilli()}),
	} {
		if name == "unknown-cursor" {
			// A structurally valid cursor for a row that does not exist is allowed.
			continue
		}
		if w := request(h, "GET", path, token, nil); w.Code != 400 {
			t.Fatalf("%s: got %d %s", name, w.Code, w.Body.String())
		}
	}
	// The observations endpoint matches the other read endpoints: same-origin GET
	// without a collector credential, and an unrelated Authorization header is
	// ignored rather than rejected.
	if w := request(h, "GET", "/api/v1/observations?service=ipv4", "", nil); w.Code != 200 {
		t.Fatal("read endpoint required a collector credential", w.Code)
	}
	if w := request(h, "GET", "/api/v1/observations?service=ipv4", "unrelated-token", nil); w.Code != 200 {
		t.Fatal("read endpoint rejected an unrelated Authorization header", w.Code)
	}
	if w := request(h, "POST", "/api/v1/observations?service=ipv4", token, nil); w.Code != 405 {
		t.Fatal("method not allowed", w.Code)
	}
	// A structurally valid but unknown cursor simply exhausts the page.
	w := request(h, "GET", "/api/v1/observations?service=ipv4&cursor="+encodeObservationCursor(observationCursor{Present: true, Observed: now.UnixMilli(), Received: now.UnixMilli(), RowID: 99}), token, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if out := decodeObservations(t, w); len(out.Observations) != 0 {
		t.Fatal(out)
	}
	// Exactly seven days is accepted.
	w = request(h, "GET", "/api/v1/observations?service=ipv4&from="+now.Add(-7*24*time.Hour).UTC().Format(time.RFC3339Nano)+"&to="+validTo, token, nil)
	if w.Code != 200 {
		t.Fatal("seven day window rejected", w.Code, w.Body.String())
	}
}

func TestObservationsCursorsAreBounded(t *testing.T) {
	if _, err := decodeObservationCursor(strings.Repeat("A", observationCursorMaxBytes+1)); err == nil {
		t.Fatal("oversized cursor accepted")
	}
	if _, err := decodeObservationCursor("bm90LWpzb24"); err == nil {
		t.Fatal("non-JSON cursor accepted")
	}
	if _, err := decodeObservationCursor(encodeObservationCursor(observationCursor{Present: true, Observed: 1, Received: 1, RowID: 0})); err == nil {
		t.Fatal("missing rowid accepted")
	}
}

func TestObservationsNoPrivateIdentifiers(t *testing.T) {
	s, h, token, now := fixture(t)
	_ = s.Heartbeat("home", "h", now)
	b := batch("batch-secret-id", "sample-secret-identity", "healthy", 0)
	b.Samples[0].Code = "job_previous_boot"
	b.Samples[0].Evidence = &Evidence{Kind: "job_success", At: now.Add(-time.Minute).Format(time.RFC3339Nano), Historical: true}
	if err := s.Ingest("home", b, now); err != nil {
		t.Fatal(err)
	}
	w := request(h, "GET", "/api/v1/observations?service=ipv4", token, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, forbidden := range []string{"sample-secret-identity", "batch-secret-id", token, s.Config.Database, s.Config.Agents[0].SecretFile, "secretFile", "Digest", "ageSeconds", "capturedAt", "checkedAgeSeconds"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("private field %q leaked: %s", forbidden, body)
		}
	}
	out := decodeObservations(t, w)
	if len(out.Observations) != 1 || out.Observations[0].Evidence == nil || out.Observations[0].Evidence.Kind != "job_success" {
		t.Fatal(out)
	}
}

func TestObservationsRejectCorruptStoredPayload(t *testing.T) {
	cases := map[string]string{
		"not-json":     `{"status":"healthy","code":"ok"`,
		"bad-status":   rawObservation("plausible", "ok", 1.0),
		"bad-code":     rawObservation("healthy", "not_a_code", 1.0),
		"bad-path":     `{"status":"healthy","code":"ok","pathMode":"fabricated"}`,
		"latency-oob":  rawObservation("healthy", "ok", 120001.0),
		"bad-evidence": `{"status":"healthy","code":"ok","evidence":{"kind":"raw_log","at":"2024-01-01T00:00:00Z"}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			s, h, token, now := fixture(t)
			insertRaw(t, s, "corrupt", now.UnixMilli(), now.UnixMilli(), false, 0, raw)
			if _, err := s.ObservationsContext(context.Background(), "ipv4", now.Add(-time.Hour), now, 10, ""); err == nil {
				t.Fatalf("corrupt payload %q accepted", name)
			}
			w := request(h, "GET", "/api/v1/observations?service=ipv4", token, nil)
			if w.Code != 503 {
				t.Fatalf("corrupt payload: got %d %s", w.Code, w.Body.String())
			}
			if body := w.Body.String(); strings.Contains(body, raw) || strings.Contains(body, "not_a_code") || strings.Contains(body, "fabricated") {
				t.Fatalf("stored payload leaked: %s", body)
			}
		})
	}
}

func TestObservationsCancellation(t *testing.T) {
	s, h, token, now := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ObservationsContext(ctx, "ipv4", now.Add(-time.Hour), now, 10, ""); err == nil {
		t.Fatal("cancelled store read succeeded")
	}
	_ = s.Heartbeat("home", "h", now)
	insertRaw(t, s, "live", now.UnixMilli(), now.UnixMilli(), false, 0, rawObservation("healthy", "ok", 1.0))
	r := httptest.NewRequest("GET", "/api/v1/observations?service=ipv4", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal("cancelled request not reported unavailable", w.Code, w.Body.String())
	}
}

func TestObservationsUseIndexedWindow(t *testing.T) {
	s, _, _, now := fixture(t)
	base := now.UnixMilli()
	located := explainPlan(t, s, `SELECT rowid,observed,received,expires,replay,data FROM samples INDEXED BY sample_history_ordered WHERE service=? AND observed>=? AND observed<=? AND (observed,received,rowid)<(?,?,?) ORDER BY observed DESC,received DESC,rowid DESC LIMIT ?`, "ipv4", base-3600000, base, base, base, 1, 1001)
	assertIndexedSeek(t, "located cursor", located)
	locatedFirst := explainPlan(t, s, `SELECT rowid,observed,received,expires,replay,data FROM samples INDEXED BY sample_history_ordered WHERE service=? AND observed>=? AND observed<=? ORDER BY observed DESC,received DESC,rowid DESC LIMIT ?`, "ipv4", base-3600000, base, 1001)
	assertIndexedSeek(t, "located first page", locatedFirst)
	unlocated := explainPlan(t, s, `SELECT rowid,observed,received,expires,replay,data FROM samples INDEXED BY sample_history_ordered WHERE service=? AND observed IS NULL AND replay=1 AND received>=? AND received<=? AND (received,rowid)<(?,?) ORDER BY received DESC,rowid DESC LIMIT ?`, "ipv4", base-3600000, base, base, 1, 1001)
	assertIndexedSeek(t, "unlocated cursor", unlocated)
}

// assertIndexedSeek rejects any plan that scans the table or materialises a
// temporary sort: the seek must descend the ordered index directly.
func assertIndexedSeek(t *testing.T, name, plan string) {
	t.Helper()
	if !strings.Contains(plan, "sample_history_ordered") || strings.Contains(plan, "SCAN samples") {
		t.Fatalf("%s not indexed: %s", name, plan)
	}
	if strings.Contains(plan, "USE TEMP B-TREE") {
		t.Fatalf("%s resorted in a temp tree: %s", name, plan)
	}
}

func explainPlan(t *testing.T, s *Store, query string, args ...any) string {
	t.Helper()
	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	details := []string{}
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(details, "\n")
}
