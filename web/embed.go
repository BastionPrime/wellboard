// Package web embeds the built SPA bundle (web/dist) so the Go binary
// serves the UI from itself — no external static hosting, no CDN
// (initial TZ NFR; DECISIONS D13).
//
// dist/ is COMMITTED: the bundle is small (~150 KB) and committing it
// keeps `--dev` and the packaged binary working from a plain checkout
// or release artifact without a Node toolchain on the build host.
// It is rebuilt with `cd web && npm run build`.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var distFS embed.FS

// Dist returns the embedded SPA filesystem rooted at dist/.
func Dist() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// all:dist embeds the directory; Sub cannot fail. Panic is a
		// programming-error signal, not a runtime path.
		panic("web: embedded dist missing: " + err.Error())
	}
	return sub
}

// SPAHandler serves the embedded SPA: static assets at their paths and
// index.html for any other GET (client-side hash routing still works
// without the fallback, but the catch-all makes direct /-paths safe).
func SPAHandler() http.Handler {
	fileServer := http.FileServerFS(Dist())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Serve index.html at / and unknown non-asset paths.
		p := r.URL.Path
		if p == "/" || p == "/index.html" {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, distFS, "dist/index.html")
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
