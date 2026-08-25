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

	// The root serves the SPA shell: a Vue mount point and the embedded runtime, so
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
	if !strings.Contains(body, "'POST'") || !strings.Contains(body, "fetch(url") {
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

// TestServeHandlerEditDecisionThroughSPA closes the demo loop Vini asked for: from
// `pond serve` a person opens an existing decision, changes a weight, and sees the
// ranking re-order — a decision tool is only useful if you can tune it. Two things
// must hold together. First, the ranking view must host an edit affordance wired to
// PUT /decisions/{title}; a page that can only read the frozen ranking cannot be
// tuned. Second, the anti-sprint#28 guard: the exact JSON body the edit form builds,
// PUT through the serve mux (not the server package in isolation), must replace the
// stored decision and re-rank — proving the route the front calls exists and takes
// what the front sends. The edit flips the dominant weight, so the winner changes:
// a PUT that never reached Save(), or a rank that echoed the old file, would fail.
func TestServeHandlerEditDecisionThroughSPA(t *testing.T) {
	store := pondera.NewFileStore(t.TempDir())
	// A decision whose winner is decided purely by which criterion carries the
	// weight: quality leads for premium, price for budget. With quality heavier,
	// premium wins.
	seed := pondera.Decision{
		Title: "pick-plan",
		Owner: "local",
		Criteria: []pondera.Criterion{
			{Name: "quality", Weight: 3},
			{Name: "price", Weight: 1},
		},
		Options: []pondera.Option{
			{Name: "premium", Scores: map[string]float64{"quality": 100, "price": 0}},
			{Name: "budget", Scores: map[string]float64{"quality": 0, "price": 100}},
		},
	}
	if err := store.Save(seed); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	h := serveHandler(store, "local")

	// The ranking view must carry an edit affordance pointed at the update route, or
	// a demo decision can be read but never tuned.
	body := httptestGet(h, "/").Body.String()
	if !strings.Contains(body, `@click="startEdit`) {
		t.Fatalf("SPA shell has no edit affordance (startEdit):\n%s", body)
	}
	if !strings.Contains(body, "'PUT'") {
		t.Fatalf("SPA shell does not PUT an edited decision:\n%s", body)
	}

	// Before the edit, quality's weight dominates: premium ranks first.
	rec := httptestGet(h, "/decisions/pick-plan/rank")
	var ranked []pondera.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &ranked); err != nil {
		t.Fatalf("pre-edit rank not JSON: %v (%s)", err, rec.Body.String())
	}
	if ranked[0].Option != "premium" {
		t.Fatalf("pre-edit rank[0] = %q, want premium", ranked[0].Option)
	}

	// The body below is the exact shape the edit form builds (same build() as create):
	// field names match the Go struct, Direction is its keyword, Scores keyed by name.
	// The only change is the weights swapped — price now dominates. Identity comes
	// from the path, so the body Title/Owner are ignored by the handler.
	edited := `{"Title":"pick-plan",` +
		`"Criteria":[{"Name":"quality","Weight":1,"Direction":"benefit"},` +
		`{"Name":"price","Weight":3,"Direction":"benefit"}],` +
		`"Options":[{"Name":"premium","Scores":{"quality":100,"price":0}},` +
		`{"Name":"budget","Scores":{"quality":0,"price":100}}]}`

	rec = httptestPut(h, "/decisions/pick-plan", edited)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /decisions/pick-plan = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	// After the edit, price's weight dominates: the ranking re-orders, budget wins.
	// A stale rank or a PUT that never persisted would still show premium.
	rec = httptestGet(h, "/decisions/pick-plan/rank")
	if err := json.Unmarshal(rec.Body.Bytes(), &ranked); err != nil {
		t.Fatalf("post-edit rank not JSON: %v (%s)", err, rec.Body.String())
	}
	if ranked[0].Option != "budget" {
		t.Fatalf("post-edit rank[0] = %q, want budget (edit did not re-rank)", ranked[0].Option)
	}
}

// TestServeHandlerRangeRoundTripsThroughSPA is the anti-sprint#28 guard for the
// per-criterion range feature: the range the edit form sends must survive PUT →
// GET as the same anchor array AND change what Rank() computes — a range that is
// accepted but ignored (the old behaviour, when Range serialized as {}) would
// silently make the range editor a no-op. One cost criterion, one option scoring
// 40: under the default [0, 100] its contribution is 60; retuning the range to
// [0, "max"] pins the sole value at the top of a cost axis, dropping the score to
// 0. The GET in between proves the array is exposed to the UI, not just stored.
func TestServeHandlerRangeRoundTripsThroughSPA(t *testing.T) {
	store := pondera.NewFileStore(t.TempDir())
	h := serveHandler(store, "local")

	// Exactly the shape build() emits, now including the Range anchor array.
	create := `{"Title":"one-cost",` +
		`"Criteria":[{"Name":"price","Weight":1,"Direction":"cost","Range":[0,100]}],` +
		`"Options":[{"Name":"only","Scores":{"price":40}}]}`
	if rec := httptestPost(h, "/decisions", create); rec.Code != http.StatusCreated {
		t.Fatalf("POST /decisions = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}

	// GET exposes the range to the UI as the anchor array, not an opaque object.
	rec := httptestGet(h, "/decisions/one-cost")
	var got pondera.Decision
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET decision not decodable: %v (%s)", err, rec.Body.String())
	}
	if got.Criteria[0].Range.String() != "[0, 100]" {
		t.Fatalf("GET range = %s, want [0, 100] (range not exposed as an array)", got.Criteria[0].Range.String())
	}

	// Under the default range, the cost contribution of 40 is 60.
	if score := topScore(t, h, "one-cost"); score != 60 {
		t.Fatalf("default-range score = %g, want 60", score)
	}

	// Retune only the range to [0, "max"]; the score must change, proving the range
	// reached Rank() rather than being accepted and dropped.
	retuned := `{"Title":"one-cost",` +
		`"Criteria":[{"Name":"price","Weight":1,"Direction":"cost","Range":[0,"max"]}],` +
		`"Options":[{"Name":"only","Scores":{"price":40}}]}`
	if rec := httptestPut(h, "/decisions/one-cost", retuned); rec.Code != http.StatusOK {
		t.Fatalf("PUT /decisions/one-cost = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if score := topScore(t, h, "one-cost"); score != 0 {
		t.Fatalf("retuned [0,\"max\"] score = %g, want 0 (range edit did not re-rank)", score)
	}
}

// topScore fetches a decision's ranking through the mux and returns the top
// option's score, failing the test on any transport or decode error.
func topScore(t *testing.T, h http.Handler, title string) float64 {
	t.Helper()
	rec := httptestGet(h, "/decisions/"+title+"/rank")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET rank %s = %d, want 200 (body %s)", title, rec.Code, rec.Body.String())
	}
	var ranked []pondera.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &ranked); err != nil {
		t.Fatalf("rank body not JSON: %v (%s)", err, rec.Body.String())
	}
	if len(ranked) == 0 {
		t.Fatalf("rank %s returned no results", title)
	}
	return ranked[0].Score
}

// TestServeHandlerRanksWithScoreBars covers the fatia-4 demo polish Vini asked for
// ("barras estilo eval360, bonito pra mostrar a leigos"): the ranking view must not
// be a bare column of numbers — each option's score is drawn as a proportional bar,
// the eval360 idiom, so a layperson reads the gap at a glance. The score index is
// already normalized 0-100, so the bar fill is bound directly to the score as a
// percentage width. A shell that only prints Score.toFixed(2) would fail the width
// binding assertion; the numeric value stays too, so the exact figure is still there.
func TestServeHandlerRanksWithScoreBars(t *testing.T) {
	store := pondera.NewFileStore(t.TempDir())
	seedRankable(t, store, "local", "buy-car")
	h := serveHandler(store, "local")

	body := httptestGet(h, "/").Body.String()
	// The bar fill must be driven by the score, not a fixed width — the visual gap
	// between options is the whole point, so the width has to track r.Score.
	if !strings.Contains(body, "r.Score + '%'") {
		t.Fatalf("ranking view has no score-driven bar (width bound to r.Score):\n%s", body)
	}
	// The numeric score stays alongside the bar: the picture is for scanning, the
	// number for the exact value.
	if !strings.Contains(body, "r.Score.toFixed(2)") {
		t.Fatalf("ranking view dropped the numeric score:\n%s", body)
	}
}

// TestServeHandlerServesVueLocallyNotFromCDN guards the whole point of the
// embedded single-file demo: `pond serve` must boot the SPA with no network. Vini
// reviewed the page and the JS did not load — a CDN <script> is a live dependency
// on unpkg being reachable at demo time, which contradicts the "one binary, all a
// family-demo machine needs" promise in serve.go. Two things must hold. First, the
// served shell must not pull its runtime from an off-site CDN (no external http
// URL), or the demo dies whenever the network does. Second, the Vue runtime must
// be served by the handler itself, from the same origin — a real 200 with a
// JavaScript body carrying Vue's createApp, proving the runtime ships in the
// binary rather than being fetched from the internet.
func TestServeHandlerServesVueLocallyNotFromCDN(t *testing.T) {
	h := serveHandler(pondera.NewFileStore(t.TempDir()), "local")

	body := httptestGet(h, "/").Body.String()
	// No off-site script source: an http(s):// URL in the shell means the demo
	// depends on a third party being up. The runtime must be same-origin.
	for _, cdn := range []string{"unpkg.com", "cdn.jsdelivr", "https://", "http://"} {
		if strings.Contains(body, cdn) {
			t.Fatalf("SPA shell still references an off-site source %q — the demo must be self-contained:\n%s", cdn, body)
		}
	}

	// The Vue runtime is served by the handler on the same origin, in the binary.
	rec := httptestGet(h, "/vue.global.prod.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /vue.global.prod.js = %d, want 200 (runtime not embedded/served)", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("vue runtime content-type = %q, want a javascript type", ct)
	}
	if js := rec.Body.String(); !strings.Contains(js, "createApp") {
		t.Fatalf("served vue runtime does not look like Vue (no createApp), got %d bytes", len(js))
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

func httptestPut(h http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
