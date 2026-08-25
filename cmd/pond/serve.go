package main

import (
	_ "embed"
	"flag"
	"fmt"
	"net/http"
	"strings"

	"github.com/sylgarcia00/pondera"
	"github.com/sylgarcia00/pondera/server"
)

// spaIndex is the single-file Vue SPA and vueRuntime is the Vue library it loads,
// both embedded so `pond serve` ships as one binary with no build step, no asset
// directory to lose, and no CDN to be unreachable at demo time — the whole app
// boots with no network on a desktop family machine.
//
//go:embed web/index.html
var spaIndex []byte

//go:embed web/vue.global.prod.js
var vueRuntime []byte

// serveHandler builds the http.Handler `pond serve` puts in front of the
// browser: the embedded SPA at the root and the decision API on the same origin,
// every API request scoped to owner. Factoring it out of cmdServe lets tests
// drive it with httptest instead of binding a port.
//
// pondera itself does not authenticate; here the desktop host injects a single
// fixed owner because the demo is one person on one machine. A multi-user
// deployment would swap this constant for server.OwnerFromHeader behind an
// authenticating proxy (the server package documents that boundary).
func serveHandler(store pondera.Store, owner string) http.Handler {
	api := server.New(store, func(*http.Request) string { return owner })
	mux := http.NewServeMux()
	// The API's own mux method-routes these paths; we forward the full request
	// to it, so registering the same handler on the prefixes is enough.
	mux.Handle("/decisions", api)
	mux.Handle("/decisions/", api)
	// The Vue runtime the shell loads, served from the same origin so the page has
	// no off-site dependency. Explicit route (not a FileServer) keeps the served
	// surface to exactly the two embedded files.
	mux.HandleFunc("GET /vue.global.prod.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(vueRuntime)
	})
	// Everything else is the SPA shell. {$} matches only the exact root so the
	// API prefixes above take precedence; the shell drives all navigation
	// client-side from there.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(spaIndex)
	})
	return mux
}

// cmdServe runs the HTTP server: the SPA plus the API over a FileStore, scoped
// to a single local owner. It is the disciplined engine reached from a browser
// instead of the shell — the SPA renders rankings from the same Rank() the CLI
// prints, never re-implementing the weighted sum in JavaScript.
func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "address to listen on")
	dir := fs.String("dir", ".", "directory of decision files (the store root)")
	owner := fs.String("owner", "local", "owner every decision is scoped to (single-user demo)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *owner == "" {
		return fmt.Errorf("pondera: --owner must not be empty")
	}
	h := serveHandler(pondera.NewFileStore(*dir), *owner)
	// A ":8080"-style addr has no host; localhost is the friendly URL to open.
	// An addr that already names a host is printed as given, not doubled up.
	url := *addr
	if strings.HasPrefix(url, ":") {
		url = "localhost" + url
	}
	fmt.Printf("pondera serving %s on http://%s (owner %q)\n", *dir, url, *owner)
	return http.ListenAndServe(*addr, h)
}
