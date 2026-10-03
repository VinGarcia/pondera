package main

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vingarcia/pondera"
	"github.com/vingarcia/pondera/webui"
)

// explainRenderDriver is injected into the SPA shell (at the PONDERA_CONFIG seam,
// the same trick TestServeHandlerRendersDemoModeInBrowser uses) so a real headless
// browser DRIVES the expandable-breakdown feature end to end and reports asserted
// results, not served source strings. It wraps window.fetch BEFORE Vue loads to
// count and (on demand) delay /explain calls, then after mount it:
//   - expands a ranking row and confirms /explain is fetched only on expand (lazy),
//   - measures the stacked-bar segment widths and the track width via the live
//     layout (getBoundingClientRect), so their sum can be asserted,
//   - starts a DELAYED /explain, edits a draft weight before it resolves, and
//     confirms the stale breakdown never paints (the breakdownEpoch invalidation),
//   - resolves the 5 segment-palette colors against the card background and
//     computes their WCAG contrast in whatever color scheme the browser emulates.
//
// It writes the findings as JSON into <pre id="render-report"> for the Go test to
// parse. With ?shot=1 it only expands the first row (for a clean screenshot) and
// skips the destructive edit step.
const explainRenderDriver = `<script>
(function(){
  var explainCalls = 0, explainDelayMs = 0;
  var origFetch = window.fetch.bind(window);
  window.fetch = function(input, init){
    var url = typeof input === 'string' ? input : (input && input.url) || '';
    if (url.indexOf('explain') !== -1) {
      explainCalls++;
      if (explainDelayMs > 0) {
        return new Promise(function(resolve){
          setTimeout(function(){ resolve(origFetch(input, init)); }, explainDelayMs);
        });
      }
    }
    return origFetch(input, init);
  };
  function sleep(ms){ return new Promise(function(r){ setTimeout(r, ms); }); }
  async function until(pred){
    for (var i=0;i<400;i++){ if (pred()) return true; await sleep(25); }
    return false;
  }
  function parseRGB(s){ var m=s.match(/[\d.]+/g); return m?[+m[0],+m[1],+m[2]]:null; }
  function lum(c){
    var a=c.map(function(v){ v/=255; return v<=0.03928?v/12.92:Math.pow((v+0.055)/1.055,2.4); });
    return 0.2126*a[0]+0.7152*a[1]+0.0722*a[2];
  }
  function contrast(fg,bg){ var L1=lum(fg),L2=lum(bg),hi=Math.max(L1,L2),lo=Math.min(L1,L2); return (hi+0.05)/(lo+0.05); }
  function resolve(cssVal){
    var el=document.createElement('span'); el.style.background=cssVal; el.style.display='none';
    document.body.appendChild(el); var c=getComputedStyle(el).backgroundColor; el.remove();
    return parseRGB(c);
  }
  var shot = location.search.indexOf('shot') !== -1;
  async function drive(){
    var rep={ok:false,steps:{}};
    try{
      rep.steps.rankingRendered = await until(function(){ return document.querySelector('tr.expandable'); });
      var rows = document.querySelectorAll('tr.expandable');
      // 2b: /explain is fetched lazily, only once the row is expanded.
      rep.steps.explainBeforeExpand = explainCalls;
      explainDelayMs = 0;
      rows[0].click();
      rep.steps.breakdownRendered = await until(function(){ return document.querySelector('.bar.stacked > span'); });
      rep.steps.explainAfterExpand = explainCalls;
      // 2a: a mounted template leaves no raw mustaches anywhere.
      rep.steps.mustachesAfterMount = document.body.innerText.indexOf('{' + '{') !== -1;
      // 2c: stacked-bar segment widths vs the track width (live layout).
      var stacked = document.querySelector('.bar.stacked');
      rep.steps.trackWidth = stacked.getBoundingClientRect().width;
      var segs = Array.prototype.map.call(stacked.children, function(s){ return s.getBoundingClientRect().width; });
      rep.steps.segWidths = segs;
      rep.steps.segCount = segs.length;
      rep.steps.segSum = segs.reduce(function(a,b){ return a+b; }, 0);
      // 2e: 5-color segment palette contrast against the card surface, this scheme.
      rep.steps.scheme = matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
      var bg = resolve(getComputedStyle(document.documentElement).getPropertyValue('--card'));
      rep.steps.bg = bg;
      rep.steps.contrast = {};
      ['--accent','--good','--seg-amber','--seg-violet','--danger'].forEach(function(n){
        var rgb = resolve('var(' + n + ')');
        rep.steps.contrast[n] = rgb ? Math.round(contrast(rgb, bg) * 1000) / 1000 : null;
      });
      if (shot){ return; } // screenshot mode: leave the expanded bar on screen, no report
      // 2d: a draft edit must invalidate an IN-FLIGHT /explain (breakdownEpoch guard).
      // /explain returns every option at once, so the first fast expand already
      // cached all rows; an edit first clears that cache so the next expand truly
      // issues a fresh fetch we can leave in flight.
      function editWeight(){
        var w = document.querySelector('input.num[type=number]');
        rep.steps.weightInputFound = !!w;
        if (!w) return;
        w.value = String((parseFloat(w.value) || 1) + 1);
        w.dispatchEvent(new Event('input',  {bubbles:true}));
        w.dispatchEvent(new Event('change', {bubbles:true}));
      }
      editWeight();                    // invalidate the cache (clears breakdowns)
      await sleep(1300);               // let refreshRank settle; cache now empty
      rep.steps.stackedAfterInvalidate = document.querySelectorAll('.bar.stacked').length;
      explainDelayMs = 1500;           // next /explain stays in flight
      var before = explainCalls;
      document.querySelectorAll('tr.expandable')[0].click(); // expand -> delayed fetch
      await sleep(150);
      rep.steps.explainInflightStarted = explainCalls > before;
      editWeight();                    // bump breakdownEpoch while the fetch is in flight
      await sleep(1900);               // the stale /explain lands and must be dropped
      rep.steps.stackedAfterEdit = document.querySelectorAll('.bar.stacked').length;
      rep.ok = true;
    }catch(e){ rep.error = String((e && e.stack) || e); }
    emit(rep);
  }
  function emit(rep){
    var pre=document.createElement('pre'); pre.id='render-report';
    pre.textContent=JSON.stringify(rep); document.body.appendChild(pre);
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', drive);
  else drive();
})();
</script>`

type explainReport struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	Steps struct {
		RankingRendered        bool               `json:"rankingRendered"`
		MustachesAfterMount    bool               `json:"mustachesAfterMount"`
		ExplainBeforeExpand    int                `json:"explainBeforeExpand"`
		BreakdownRendered      bool               `json:"breakdownRendered"`
		ExplainAfterExpand     int                `json:"explainAfterExpand"`
		TrackWidth             float64            `json:"trackWidth"`
		SegWidths              []float64          `json:"segWidths"`
		SegCount               int                `json:"segCount"`
		SegSum                 float64            `json:"segSum"`
		Scheme                 string             `json:"scheme"`
		Bg                     []float64          `json:"bg"`
		Contrast               map[string]float64 `json:"contrast"`
		StackedAfterInvalidate int                `json:"stackedAfterInvalidate"`
		ExplainInflightStarted bool               `json:"explainInflightStarted"`
		WeightInputFound       bool               `json:"weightInputFound"`
		StackedAfterEdit       int                `json:"stackedAfterEdit"`
	} `json:"steps"`
}

// explainRenderServer builds a server that serves the real decision API but a
// driver-injected SPA shell, over the same origin — the live-render equivalent of
// serveHandler for the breakdown feature.
func explainRenderServer(t *testing.T, store pondera.Store, owner string) *httptest.Server {
	t.Helper()
	shell := strings.Replace(string(webui.Index), "<!--PONDERA_CONFIG-->", explainRenderDriver, 1)
	if !strings.Contains(shell, "render-report") {
		t.Fatal("driver was not injected into the SPA shell (PONDERA_CONFIG seam missing)")
	}
	api := serveHandler(store, owner) // real /decisions + /explain + webui
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(shell))
	})
	mux.Handle("/", api) // Vue runtime + the decision API
	return httptest.NewServer(mux)
}

// seedFiveCriteria writes a decision with all five palette colors in play (five
// benefit criteria) so the stacked bar renders five segments and the contrast
// check exercises the whole segment palette.
func seedFiveCriteria(t *testing.T, store pondera.Store, owner, title string) {
	t.Helper()
	d := pondera.Decision{
		Title: title,
		Owner: owner,
		Criteria: []pondera.Criterion{
			{Name: "safety", Weight: 5},
			{Name: "price", Weight: 4},
			{Name: "range", Weight: 3},
			{Name: "comfort", Weight: 2},
			{Name: "style", Weight: 1},
		},
		Options: []pondera.Option{
			{Name: "alpha", Scores: map[string]float64{"safety": 90, "price": 70, "range": 60, "comfort": 80, "style": 50}},
			{Name: "bravo", Scores: map[string]float64{"safety": 60, "price": 85, "range": 75, "comfort": 55, "style": 70}},
		},
	}
	if err := store.Save(context.Background(), d); err != nil {
		t.Fatalf("seeding %s/%s: %v", owner, title, err)
	}
}

// runExplainDriver loads the driver page in headless Chrome (dark when asked) and
// returns the parsed report. It SKIPS, like the sibling render tests, when no
// browser is installed.
func runExplainDriver(t *testing.T, url string, dark bool) explainReport {
	t.Helper()
	extra := []string{"--virtual-time-budget=20000"}
	if dark {
		extra = append(extra, "--blink-settings=preferredColorScheme=0")
	}
	dom := runChromeDOM(t, url, extra...)
	const open = `<pre id="render-report">`
	i := strings.Index(dom, open)
	if i < 0 {
		t.Fatalf("driver never emitted a render report (page did not run):\n%s", dom)
	}
	j := strings.Index(dom[i:], "</pre>")
	if j < 0 {
		t.Fatalf("render report not terminated:\n%s", dom)
	}
	// The DOM dump HTML-escapes the <pre> text content, so the JSON is entity-encoded.
	raw := html.UnescapeString(dom[i+len(open) : i+j])
	var rep explainReport
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatalf("parsing render report: %v\nraw: %s", err, raw)
	}
	return rep
}

// chromeBaseArgs are the headless flags every browser check in this package shares
// (--no-sandbox is required when the test runs as root in a container).
func chromeBaseArgs() []string {
	return []string{"--headless", "--no-sandbox", "--disable-gpu"}
}

// findChrome returns the first installed Chrome/Chromium, or SKIPS the test when
// none is in PATH — the single source of browser discovery for the sibling render
// tests too, so `go test ./...` stays green on a machine without a browser.
func findChrome(t *testing.T) string {
	t.Helper()
	for _, c := range []string{"google-chrome-stable", "google-chrome", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	t.Skip("no Chrome/Chromium in PATH; skipping browser render check")
	return ""
}

// runChromeDOM renders url in headless Chrome with the shared base flags plus any
// extras (e.g. a virtual-time budget or a color-scheme override) and returns the
// post-JavaScript DOM. It is the single source for browser discovery and the base
// flag set, used by both chromeDumpDOM and the explain driver.
func runChromeDOM(t *testing.T, url string, extra ...string) string {
	t.Helper()
	bin := findChrome(t)
	args := append(chromeBaseArgs(), extra...)
	args = append(args, "--dump-dom", url)
	out, err := exec.Command(bin, args...).Output()
	if err != nil {
		t.Fatalf("chrome --dump-dom %s: %v", url, err)
	}
	return string(out)
}

// assertRenderGate applies the quality-gate render assertions (2a-2e) to one
// scheme's report. The in-flight invalidation (2d) runs only in the non-shot
// driver, so it is checked only when the step was recorded.
func assertRenderGate(t *testing.T, rep explainReport, wantScheme string) {
	t.Helper()
	s := rep.Steps
	if !rep.OK {
		t.Fatalf("%s: driver aborted: %s (steps: %+v)", wantScheme, rep.Error, s)
	}
	// 2a: template compiled and rendered with no runtime error.
	if !s.RankingRendered || !s.BreakdownRendered {
		t.Fatalf("%s: feature did not render (ranking=%v breakdown=%v)", wantScheme, s.RankingRendered, s.BreakdownRendered)
	}
	if s.MustachesAfterMount {
		t.Fatalf("%s: raw {{ }} survived — a binding failed to compile", wantScheme)
	}
	// 2b: /explain fetched lazily, only on expand.
	if s.ExplainBeforeExpand != 0 {
		t.Fatalf("%s: /explain fetched before expand (%d) — not lazy", wantScheme, s.ExplainBeforeExpand)
	}
	if s.ExplainAfterExpand < 1 {
		t.Fatalf("%s: expand did not trigger /explain", wantScheme)
	}
	// 2c: segment widths sum to the track width.
	if s.SegCount != 5 {
		t.Fatalf("%s: want 5 rendered segments, got %d", wantScheme, s.SegCount)
	}
	if s.TrackWidth <= 0 {
		t.Fatalf("%s: stacked track has no width (%v)", wantScheme, s.TrackWidth)
	}
	// Tolerance scales with the track (2%, floor 1.5px) to absorb per-segment
	// sub-pixel rounding across Chrome versions and font metrics, while still
	// catching a genuinely missing or overflowing segment (off by tens of px).
	tol := s.TrackWidth * 0.02
	if tol < 1.5 {
		tol = 1.5
	}
	if d := s.SegSum - s.TrackWidth; d > tol || d < -tol {
		t.Fatalf("%s: segment widths %.3f do not sum to track %.3f (diff %.3f, tol %.3f)", wantScheme, s.SegSum, s.TrackWidth, d, tol)
	}
	// 2e: palette contrast against the card surface in this scheme.
	if s.Scheme != wantScheme {
		t.Fatalf("want scheme %q, browser reported %q", wantScheme, s.Scheme)
	}
	if len(s.Contrast) != 5 {
		t.Fatalf("%s: want 5 palette contrasts, got %d", wantScheme, len(s.Contrast))
	}
	for name, ratio := range s.Contrast {
		if ratio < 1.5 {
			t.Fatalf("%s: segment color %s has contrast %.3f against the card (< 1.5, effectively invisible)", wantScheme, name, ratio)
		}
	}
	t.Logf("%s scheme contrasts: %+v (bg=%v)", wantScheme, s.Contrast, s.Bg)
}

// TestExplainBreakdownRendersAndInvalidates is the quality-gate render check for
// the expandable per-criterion breakdown: it drives the feature in a live headless
// browser and asserts 2a-2e from the actual layout and fetch behavior, in both the
// light and dark schemes.
func TestExplainBreakdownRendersAndInvalidates(t *testing.T) {
	_ = findChrome(t) // skip early when no browser
	store := pondera.NewFileStore(t.TempDir())
	seedFiveCriteria(t, store, "local", "buy-car")
	srv := explainRenderServer(t, store, "local")
	defer srv.Close()
	url := srv.URL + "/#buy-car"

	light := runExplainDriver(t, url, false)
	assertRenderGate(t, light, "light")
	// 2d: the in-flight invalidation, verified in the light (non-shot) run.
	if !light.Steps.WeightInputFound {
		t.Fatal("could not find a draft weight input to edit for the invalidation check")
	}
	if light.Steps.StackedAfterInvalidate != 0 {
		t.Fatalf("an edit did not collapse/clear the open breakdown: %d stacked bars remained", light.Steps.StackedAfterInvalidate)
	}
	if !light.Steps.ExplainInflightStarted {
		t.Fatal("delayed /explain never started — the in-flight invalidation check is vacuous")
	}
	if light.Steps.StackedAfterEdit != 0 {
		t.Fatalf("a stale breakdown survived a draft edit: %d stacked bars still shown (breakdownEpoch guard failed)", light.Steps.StackedAfterEdit)
	}

	dark := runExplainDriver(t, url, true)
	assertRenderGate(t, dark, "dark")
}

// TestExplainBreakdownScreenshots captures light and dark screenshots of the
// expanded breakdown as render evidence and writes their paths to the test log.
func TestExplainBreakdownScreenshots(t *testing.T) {
	bin := findChrome(t)
	store := pondera.NewFileStore(t.TempDir())
	seedFiveCriteria(t, store, "local", "buy-car")
	srv := explainRenderServer(t, store, "local")
	defer srv.Close()
	url := srv.URL + "/?shot=1#buy-car"

	dir := filepath.Join(os.TempDir(), "pondera-render-evidence")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir evidence dir: %v", err)
	}
	for _, tc := range []struct {
		name string
		dark bool
	}{{"light", false}, {"dark", true}} {
		out := filepath.Join(dir, "explain-breakdown-"+tc.name+".png")
		args := append(chromeBaseArgs(),
			"--hide-scrollbars", "--window-size=1200,1500", "--virtual-time-budget=9000")
		if tc.dark {
			args = append(args, "--blink-settings=preferredColorScheme=0")
		}
		args = append(args, "--screenshot="+out, url)
		if b, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
			t.Fatalf("chrome --screenshot (%s): %v\n%s", tc.name, err, b)
		}
		info, err := os.Stat(out)
		if err != nil || info.Size() == 0 {
			t.Fatalf("screenshot %s not written: err=%v", out, err)
		}
		t.Logf("screenshot (%s): %s (%d bytes)", tc.name, out, info.Size())
	}
}
