// Package spa embeds the prebuilt SvelteKit single-page bundle and serves it on
// the same origin as the API, so the Windows desktop build ships one Go binary
// with no Node runtime.
//
// The bundle is produced by `VARYAONE_ADAPTER=static npm run build` in web/ and
// copied into dist/ by the desktop build. When only the committed placeholder is
// present, Enabled() reports false and the API mounts no SPA handler (unchanged
// behaviour for the Docker/adapter-node deployment).
package spa

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

// FS returns the embedded bundle rooted at dist/.
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err) // dist/ is guaranteed to exist by the //go:embed directive.
	}
	return sub
}

// Enabled reports whether a real bundle (not just the placeholder) is embedded.
func Enabled() bool {
	f, err := FS().Open("_app")
	if err != nil {
		// _app/ is emitted by every SvelteKit static build; the placeholder has none.
		return false
	}
	_ = f.Close()
	return true
}

// Handler serves the SPA: real files when they exist, otherwise index.html so
// client-side routing (deep links, reloads) works. It never shadows /api or
// /health — callers mount it as the catch-all.
func Handler() http.Handler {
	bundle := FS()
	fileServer := http.FileServer(http.FS(bundle))
	index, _ := fs.ReadFile(bundle, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// An installation holds its owner's business data; it has no place in a
		// search index, whether or not the crawler honours robots.txt.
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")

		upstream := strings.TrimPrefix(r.URL.Path, "/")
		if upstream == "" {
			serveIndex(w, index)
			return
		}
		if f, err := bundle.Open(upstream); err == nil {
			_ = f.Close()
			// Immutable, content-hashed assets can cache aggressively.
			if strings.HasPrefix(upstream, "_app/immutable/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		serveIndex(w, index)
	})
}

func serveIndex(w http.ResponseWriter, index []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	_, _ = w.Write(index)
}
