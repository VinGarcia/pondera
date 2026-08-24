package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sylgarcia00/pondera"
)

// TestServeHandlerServesSPAAndAPI is the fatia-4 foundation: `pond serve` puts
// one http.Handler in front of the browser that serves the embedded SPA shell at
// the root AND mounts the decision API on the same origin, so the single-file
// Vue app can fetch its own endpoints without CORS or a separate process. The
// API is scoped to the owner the command injects (a fixed local identity for the
// desktop demo), and the ranking comes back through the same engine the CLI uses
// — the SPA never re-implements the weighted sum in JS.
func TestServeHandlerServesSPAAndAPI(t *testing.T) {
	store := pondera.NewFileStore(t.TempDir())
	seedRankable(t, store, "local", "buy-car")

	h := serveHandler(store, "local")

	// The root serves the SPA shell: a Vue mount point and the CDN runtime, so
	// opening the page in a browser boots the app with no build toolchain.
	rec := httptestGet(h, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("GET / content-type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="app"`) {
		t.Fatalf("SPA shell missing Vue mount point id=\"app\":\n%s", body)
	}
	if !strings.Contains(body, "vue") && !strings.Contains(body, "Vue") {
		t.Fatalf("SPA shell missing the Vue runtime script:\n%s", body)
	}

	// The list endpoint is reachable on the same origin and scoped to the
	// injected owner — the app fetches its own decisions with no header to set.
	rec = httptestGet(h, "/decisions")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /decisions = %d, want 200", rec.Code)
	}
	var titles []string
	if err := json.Unmarshal(rec.Body.Bytes(), &titles); err != nil {
		t.Fatalf("GET /decisions body not a JSON array: %v (%s)", err, rec.Body.String())
	}
	if len(titles) != 1 || titles[0] != "buy-car" {
		t.Fatalf("GET /decisions = %v, want [buy-car]", titles)
	}

	// The ranking endpoint the every SPA ranking screen consumes is wired to the
	// real engine: the safer, cheaper option outranks the riskier, dearer one,
	// so a passthrough that never ran Rank() would fail this ordering.
	rec = httptestGet(h, "/decisions/buy-car/rank")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /decisions/buy-car/rank = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var ranked []pondera.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &ranked); err != nil {
		t.Fatalf("rank body not a JSON array of results: %v (%s)", err, rec.Body.String())
	}
	if len(ranked) != 2 {
		t.Fatalf("rank returned %d results, want 2", len(ranked))
	}
	if ranked[0].Option != "safe-cheap" {
		t.Fatalf("rank[0] = %q, want safe-cheap (engine did not run)", ranked[0].Option)
	}
}

// seedRankable writes a two-option decision whose ranking has an unambiguous
// winner, so the rank assertion proves the engine ran rather than echoing input.
func seedRankable(t *testing.T, store pondera.Store, owner, title string) {
	t.Helper()
	d := pondera.Decision{
		Title: title,
		Owner: owner,
		Criteria: []pondera.Criterion{
			{Name: "safety", Weight: 3},
			{Name: "price", Weight: 2, Direction: pondera.Cost, Range: pondera.NewRange(pondera.MinAnchor(), pondera.MaxAnchor())},
		},
		Options: []pondera.Option{
			{Name: "safe-cheap", Scores: map[string]float64{"safety": 90, "price": 20000}},
			{Name: "risky-dear", Scores: map[string]float64{"safety": 70, "price": 40000}},
		},
	}
	if err := store.Save(d); err != nil {
		t.Fatalf("seeding %s/%s: %v", owner, title, err)
	}
}

func httptestGet(h http.Handler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
