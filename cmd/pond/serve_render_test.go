package main

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/vingarcia/pondera"
	"github.com/vingarcia/pondera/webui"
)

// canonicalUUID is the RFC-4122 36-char lowercase form the public backend
// enforces and crypto.randomUUID() emits; the render test asserts the header the
// SPA sent matches it exactly, so a non-canonical id would fail.
var canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// chromeDumpDOM renders url in headless Chrome and returns the post-JavaScript
// DOM (what Vue actually mounted, not the served source). It SKIPS the test when
// no browser is installed, so `go test ./...` stays green on a machine without
// one — the browser render check is an extra guarantee where Chrome exists, never
// a hard build dependency of the suite (pondera still ships as one binary, and no
// browser is imported into it).
func chromeDumpDOM(t *testing.T, url string) string {
	t.Helper()
	var bin string
	for _, c := range []string{"google-chrome-stable", "google-chrome", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(c); err == nil {
			bin = p
			break
		}
	}
	if bin == "" {
		t.Skip("no Chrome/Chromium in PATH; skipping browser render check")
	}
	// --virtual-time-budget lets the SPA's onMounted fetch resolve and Vue
	// re-render before the DOM is captured; --dump-dom prints the live DOM.
	// --no-sandbox is required when the test runs as root in a container.
	cmd := exec.Command(bin, "--headless", "--no-sandbox", "--disable-gpu",
		"--virtual-time-budget=5000", "--dump-dom", url)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("chrome --dump-dom %s: %v", url, err)
	}
	return string(out)
}

// TestServeHandlerRendersInBrowser closes the render debt runs #150–154 left
// open: earlier tests proved the shell and the vendored Vue runtime are SERVED,
// never that a real browser BOOTS them into a rendered page (the gap behind
// Vini's "the JS didn't load"). Here headless Chrome loads the page over the same
// same-origin server `pond serve` exposes, and we assert the live DOM is the
// Vue-rendered result — the seeded decision shows up as data-driven text and the
// template's {{ }} mustaches are gone — so a page that serves but fails to mount
// would fail this test.
func TestServeHandlerRendersInBrowser(t *testing.T) {
	store := pondera.NewFileStore(t.TempDir())
	seedRankable(t, store, "local", "buy-car")
	srv := httptest.NewServer(serveHandler(store, "local"))
	defer srv.Close()

	dom := chromeDumpDOM(t, srv.URL+"/")

	// The seeded title is in the DOM only if Vue mounted, fetched /decisions, and
	// rendered the reactive list — a served-but-unmounted shell would not have it.
	if !strings.Contains(dom, "buy-car") {
		t.Fatalf("rendered DOM missing the seeded decision (Vue did not render the list):\n%s", dom)
	}
	// A mounted Vue app resolves every {{ }}; raw mustaches surviving in the DOM
	// mean the template was served but never compiled.
	if strings.Contains(dom, "{{") {
		t.Fatalf("rendered DOM still has unresolved {{ }} mustaches (Vue did not mount):\n%s", dom)
	}
}

// TestServeHandlerRendersRankingViewInBrowser closes the render debt runs #150–155
// left open on the RANKING view: run #155 proved the list view boots, but the
// score-bars view (the feature Vini cares about) only appears after a decision is
// opened, which --dump-dom cannot click. A deep-link — the page loaded at
// `/#<title>` opens that decision on mount — drives the same open()/rank code path
// a click does, so headless Chrome can prove the ranking table, the eval360-style
// bars, and the rendered fill widths are real DOM, not served template.
func TestServeHandlerRendersRankingViewInBrowser(t *testing.T) {
	store := pondera.NewFileStore(t.TempDir())
	seedRankable(t, store, "local", "buy-car")
	srv := httptest.NewServer(serveHandler(store, "local"))
	defer srv.Close()

	dom := chromeDumpDOM(t, srv.URL+"/#buy-car")

	// The ranked options are in the DOM only if Vue mounted, followed the hash into
	// open('buy-car'), fetched the rank, and rendered the reactive table.
	for _, want := range []string{"safe-cheap", "risky-dear"} {
		if !strings.Contains(dom, want) {
			t.Fatalf("ranking DOM missing option %q (deep-link did not render the ranking):\n%s", want, dom)
		}
	}
	// The eval360-style score bar: the track span and a fill whose width is the
	// bound score. Both only exist once the ranking table rendered.
	if !strings.Contains(dom, `class="bar"`) {
		t.Fatalf("ranking DOM missing the score bar track (bars did not render):\n%s", dom)
	}
	if !strings.Contains(dom, "width:") || !strings.Contains(dom, "%") {
		t.Fatalf("ranking DOM missing a bound fill width (:style did not evaluate):\n%s", dom)
	}
	if strings.Contains(dom, "{{") {
		t.Fatalf("ranking DOM still has unresolved {{ }} mustaches (Vue did not mount):\n%s", dom)
	}
}

// TestRankingRenderCheckIsLoadBearing is the ranking-specific non-vacuity bite:
// the same server WITHOUT the hash lands on the list view, and the score-bar
// assertions must NOT pass there — proving they distinguish a rendered ranking
// from a rendered list, not merely "some DOM came back".
func TestRankingRenderCheckIsLoadBearing(t *testing.T) {
	store := pondera.NewFileStore(t.TempDir())
	seedRankable(t, store, "local", "buy-car")
	srv := httptest.NewServer(serveHandler(store, "local"))
	defer srv.Close()

	dom := chromeDumpDOM(t, srv.URL+"/") // no hash → list view, not ranking

	if strings.Contains(dom, `class="bar"`) {
		t.Fatalf("list view rendered a score bar without opening a decision — the ranking check is not load-bearing:\n%s", dom)
	}
	if strings.Contains(dom, "safe-cheap") {
		t.Fatalf("list view rendered a ranked option without opening a decision — the ranking check is not load-bearing:\n%s", dom)
	}
}

// TestServeHandlerRendersEditViewInBrowser covers the tune view a click can't
// reach under --dump-dom: the "edit:<title>" deep-link boots straight into the
// edit form, so headless Chrome can prove the drag-to-set bars and the
// per-criterion range editor are real rendered DOM. The seeded decision carries
// both a default-range criterion (numeric min/max inputs) and a dynamic ["min",
// "max"] one (keyword chips), so a render exercises both range branches.
func TestServeHandlerRendersEditViewInBrowser(t *testing.T) {
	store := pondera.NewFileStore(t.TempDir())
	seedRankable(t, store, "local", "buy-car")
	srv := httptest.NewServer(serveHandler(store, "local"))
	defer srv.Close()

	dom := chromeDumpDOM(t, srv.URL+"/#edit:buy-car")

	// The criterion names appear only once startEdit fetched the decision and the
	// edit form rendered its reactive rows.
	for _, want := range []string{"safety", "price"} {
		if !strings.Contains(dom, want) {
			t.Fatalf("edit DOM missing criterion %q (edit deep-link did not render the form):\n%s", want, dom)
		}
	}
	// The drag-to-set bars are the headline feature; their track class exists only
	// in the edit template, so its presence proves the interactive bars rendered.
	if !strings.Contains(dom, `class="dbar"`) {
		t.Fatalf("edit DOM missing the drag-to-set bar track (input bars did not render):\n%s", dom)
	}
	// The range editor: the dynamic ["min", "max"] criterion renders its anchors as
	// keyword chips, so "max" surfacing in the edit form proves the range is shown.
	if !strings.Contains(dom, "max") {
		t.Fatalf("edit DOM missing the criterion range keyword (range editor did not render):\n%s", dom)
	}
	// Numeric fields size to their content: the score/range inputs carry a bound
	// inline `width: <n>ch` style so a wide value (the seeded price is 20000/40000)
	// shows in full. The `ch;"` tail is an inline style attribute the static CSS
	// never emits, so its presence proves numWidth's :style binding evaluated.
	if !strings.Contains(dom, `ch;"`) {
		t.Fatalf("edit DOM missing a content-sized numeric field (numWidth :style did not evaluate):\n%s", dom)
	}
	if strings.Contains(dom, "{{") {
		t.Fatalf("edit DOM still has unresolved {{ }} mustaches (Vue did not mount):\n%s", dom)
	}
}

// TestEditRenderCheckIsLoadBearing is the edit-specific non-vacuity bite: the
// same server on the list view (no hash) must NOT render the drag bars, proving
// the check distinguishes a rendered edit form from the list, not merely "some
// DOM came back".
func TestEditRenderCheckIsLoadBearing(t *testing.T) {
	store := pondera.NewFileStore(t.TempDir())
	seedRankable(t, store, "local", "buy-car")
	srv := httptest.NewServer(serveHandler(store, "local"))
	defer srv.Close()

	dom := chromeDumpDOM(t, srv.URL+"/") // no hash → list view, not the edit form

	if strings.Contains(dom, `class="dbar"`) {
		t.Fatalf("list view rendered a drag bar without opening the edit form — the edit check is not load-bearing:\n%s", dom)
	}
}

// TestBrowserRenderCheckIsLoadBearing is the non-vacuity bite: it points the same
// two assertions at a deliberately broken page — the SPA shell served but the
// Vue runtime route removed — and confirms the browser does NOT render it (the
// seeded title never appears and the mustaches survive). Without this,
// TestServeHandlerRendersInBrowser could pass vacuously on any served HTML.
func TestBrowserRenderCheckIsLoadBearing(t *testing.T) {
	store := pondera.NewFileStore(t.TempDir())
	seedRankable(t, store, "local", "buy-car")
	// Serve the shell and the API as usual, but 404 the Vue runtime, so the page
	// loads with Vue undefined and nothing mounts.
	full := serveHandler(store, "local")
	broken := http.NewServeMux()
	broken.Handle("/vue.global.prod.js", http.NotFoundHandler())
	broken.Handle("/", full)
	srv := httptest.NewServer(broken)
	defer srv.Close()

	dom := chromeDumpDOM(t, srv.URL+"/")

	if strings.Contains(dom, "buy-car") {
		t.Fatalf("broken page rendered the decision without its Vue runtime — the render check is not load-bearing:\n%s", dom)
	}
	if !strings.Contains(dom, "{{") {
		t.Fatalf("broken page shows no unresolved mustaches — the check cannot tell rendered from unrendered:\n%s", dom)
	}
}

// TestServeHandlerRendersDemoModeInBrowser proves demo mode boots end-to-end in a
// real browser. Standalone `pond serve` does NOT enable demo, so the test serves
// its own shell with window.PONDERA_DEMO="public" injected into the config
// placeholder, mounts a fake /public/decisions backend that records the
// X-Pondera-Public-Id header, and asserts the DOM shows the seeded decision (the
// SPA fetched the public prefix), the demo banner text is present, and the header
// carried a canonical UUID. This is the only proof the demo seam wires the
// prefix, the header, and the banner together in a live page.
func TestServeHandlerRendersDemoModeInBrowser(t *testing.T) {
	// Shell with demo turned on: the same trick a public host uses — replace the
	// config placeholder with the PONDERA_DEMO injection. serves the vendored Vue
	// runtime unchanged so the SPA actually mounts.
	shell := strings.Replace(string(webui.Index), "<!--PONDERA_CONFIG-->",
		`<script>window.PONDERA_DEMO = "public";</script>`, 1)

	var mu sync.Mutex
	var gotPublicID string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(shell))
	})
	mux.HandleFunc("GET /vue.global.prod.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(webui.Runtime)
	})
	// Fake public backend: the SPA in demo mode lists decisions from
	// /public/decisions and must send the per-browser public id on it.
	mux.HandleFunc("GET /public/decisions", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPublicID = r.Header.Get("X-Pondera-Public-Id")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`["demo-decision"]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dom := chromeDumpDOM(t, srv.URL+"/")

	// The seeded title is in the DOM only if the SPA resolved demo mode, prefixed
	// the API base, and fetched /public/decisions.
	if !strings.Contains(dom, "demo-decision") {
		t.Fatalf("demo DOM missing the seeded decision (SPA did not fetch the public prefix):\n%s", dom)
	}
	// The demo banner is rendered only in demo mode; its exact copy must survive.
	if !strings.Contains(dom, "You're trying Pondera in demo mode") {
		t.Fatalf("demo DOM missing the demo banner (banner did not render):\n%s", dom)
	}
	if strings.Contains(dom, "{{") {
		t.Fatalf("demo DOM still has unresolved {{ }} mustaches (Vue did not mount):\n%s", dom)
	}
	// The recorded header proves the SPA generated and sent a canonical UUID — the
	// public identity the server requires — not an empty or malformed value.
	mu.Lock()
	got := gotPublicID
	mu.Unlock()
	if !canonicalUUID.MatchString(got) {
		t.Fatalf("public backend received X-Pondera-Public-Id %q, want a canonical RFC-4122 UUID", got)
	}
}
