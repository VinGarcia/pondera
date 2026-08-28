// Package webui ships pondera's single-file Vue SPA and the Vue runtime it
// loads, both embedded so any host — the standalone `pond serve`, or a service
// in a cluster — mounts the exact same UI with no build step, no asset directory
// to lose, and no CDN to be unreachable. Keeping the assets here, at the library
// root, is what lets the cluster service reuse the UI instead of copying it.
package webui

import (
	_ "embed"
	"net/http"
)

// Index is the SPA shell and Runtime is the Vue library it loads. They are
// exported so a host that serves over a framework other than net/http (e.g.
// fiber) can write the bytes on its own routes, same-origin with its API.
//
//go:embed web/index.html
var Index []byte

//go:embed web/vue.global.prod.js
var Runtime []byte

// Handler serves the SPA shell at the root and the Vue runtime, and nothing
// else — the API is the host's to mount on the same origin. Explicit routes
// (not a FileServer) keep the served surface to exactly the two files.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /vue.global.prod.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(Runtime)
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(Index)
	})
	return mux
}
