package main

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/sylgarcia00/pondera"
)

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
