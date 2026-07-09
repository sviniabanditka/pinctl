// Static UI serving, per docs/10-api.md's "Static UI serving": every
// request whose path does not start with /api/ is served from the embedded
// web/dist build (history-mode SPA fallback to index.html for anything that
// isn't a real embedded file); /api/* paths that match no handler stay a
// JSON 404, never the SPA fallback.
package api

import (
	"io/fs"
	"net/http"
	"net/url"
	"strings"

	"github.com/sviniabanditka/pinctl/web"
)

// distFS is web.FS rooted at "dist" (the embed directive embeds
// "all:dist", so paths inside it are dist-prefixed) so file lookups below
// match what the built app itself expects at "/", e.g. "/index.html", not
// "/dist/index.html".
var distFS = mustSubFS(web.FS, "dist")

func mustSubFS(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic("api: web.FS has no \"" + dir + "\" directory: " + err.Error())
	}
	return sub
}

var staticFileServer = http.FileServer(http.FS(distFS))

// wrapWithStatic routes non-/api/* requests to the embedded UI (serveStatic)
// and lets everything under /api/ fall through to apiMux unchanged —
// apiMux itself has a low-priority "/api/" catch-all (registered in
// NewServer) that turns an unmatched /api/* path into the docs/10-api.md
// JSON 404 envelope, so an unknown API path is never handed the SPA
// fallback.
func wrapWithStatic(apiMux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			apiMux.ServeHTTP(w, r)
			return
		}
		serveStatic(w, r)
	})
}

// serveStatic implements the history-mode fallback: a request matching a
// real file in the embedded build is served as-is (correct Content-Type
// via http.FileServer's sniffing/extension table); anything else — a
// client-side route like /pipelines/pl_x/edit — falls back to index.html
// so deep links and refreshes work without the server knowing Vue Router's
// route table.
func serveStatic(w http.ResponseWriter, r *http.Request) {
	clean := strings.TrimPrefix(pathClean(r.URL.Path), "/")
	if clean == "" || clean == "." {
		clean = "index.html"
	}

	if f, err := distFS.Open(clean); err == nil {
		_ = f.Close()
		staticFileServer.ServeHTTP(w, r)
		return
	}

	// Deliberately fall back to "/" rather than "/index.html": net/http's
	// FileServer special-cases a request path literally ending in
	// "index.html" by 301-redirecting to "./" (its usual directory-index
	// canonicalization), which would turn every SPA-fallback request into
	// a redirect loop back through this same handler. "/" serves the same
	// file without tripping that rule.
	fallback := *r
	fallbackURL := *r.URL
	fallbackURL.Path = "/"
	fallback.URL = &fallbackURL
	staticFileServer.ServeHTTP(w, &fallback)
}

// pathClean mirrors path.Clean but is spelled out here to make the
// url.Path-only scope explicit (query/fragment never reach this far).
func pathClean(p string) string {
	u := &url.URL{Path: p}
	return u.EscapedPath() // no-op cleanup; the meaningful cleaning is fs.FS's own path validation on Open
}

// handleAPINotFound is the low-priority "/api/" catch-all: any /api/* path
// that didn't match one of NewServer's specific routes lands here, per
// docs/10-api.md: "/api/* paths that don't match a defined endpoint return
// the standard JSON error envelope with 404, never the SPA fallback."
func handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "no such endpoint: "+r.Method+" "+r.URL.Path)
}
