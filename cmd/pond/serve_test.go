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

// TestServeHandlerCreateDecisionThroughSPA covers the fatia-4 demo criterion Vini
// asked for: from `pond serve` a person can create a decision, not just browse the
// seeded ones. Two things must hold together. First, the served shell must host a
// creation affordance wired to POST /decisions — a shell that only lists decisions
// leaves the demo with nothing to create. Second, and the anti-sprint#28 guard: the
// exact JSON body the form builds, posted through the serve mux (not the server
// package in isolation), must be accepted and round-trip — created, then listed,
// then rankable. A form that targets a route or a shape the backend does not accept
// would pass a shell-only assertion yet fail the demo; posting the real shape here
// proves the route the front calls actually exists and takes what the front sends.
func TestServeHandlerCreateDecisionThroughSPA(t *testing.T) {
	store := pondera.NewFileStore(t.TempDir())
	h := serveHandler(store, "local")

	// The shell must carry a create affordance pointed at the create route, or the
	// demo can only read pre-seeded decisions.
	body := httptestGet(h, "/").Body.String()
	if !strings.Contains(body, `@click="startCreate`) {
		t.Fatalf("SPA shell has no create affordance (startCreate):\n%s", body)
	}
	if !strings.Contains(body, "method: 'POST'") || !strings.Contains(body, "fetch('decisions'") {
		t.Fatalf("SPA shell does not POST a new decision to decisions:\n%s", body)
	}

	// The body below is the exact JSON shape the form assembles: field names match
	// the Go struct (Direction serializes as its keyword), Scores keyed by criterion
	// name. If this shape drifts from what the form sends, the demo breaks silently.
	newDecision := `{"Title":"pick-laptop",` +
		`"Criteria":[{"Name":"battery","Weight":3,"Direction":"benefit"},` +
		`{"Name":"price","Weight":2,"Direction":"cost"}],` +
		`"Options":[{"Name":"light","Scores":{"battery":90,"price":50}},` +
		`{"Name":"cheap","Scores":{"battery":60,"price":10}}]}`

	rec := httptestPost(h, "/decisions", newDecision)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /decisions = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}

	// Created through the mux → it now shows up in the owner's list.
	rec = httptestGet(h, "/decisions")
	var titles []string
	if err := json.Unmarshal(rec.Body.Bytes(), &titles); err != nil {
		t.Fatalf("GET /decisions body not a JSON array: %v (%s)", err, rec.Body.String())
	}
	if len(titles) != 1 || titles[0] != "pick-laptop" {
		t.Fatalf("after create, GET /decisions = %v, want [pick-laptop]", titles)
	}

	// ...and it is rankable through the same engine, proving the posted shape
	// reached Rank() intact rather than being stored as opaque bytes.
	rec = httptestGet(h, "/decisions/pick-laptop/rank")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /decisions/pick-laptop/rank = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var ranked []pondera.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &ranked); err != nil {
		t.Fatalf("rank body not a JSON array of results: %v (%s)", err, rec.Body.String())
	}
	if len(ranked) != 2 {
		t.Fatalf("rank returned %d results, want 2", len(ranked))
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

func httptestPost(h http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
