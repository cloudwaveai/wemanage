// Package webui serves the embedded React single-page app.
//
// The SPA is built by `cd web && pnpm build` (Vite outDir -> internal/webui/dist).
// A committed placeholder dist/index.html keeps the Go build working before the
// frontend is built; the Docker build overwrites dist with the real bundle.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler serves static assets from the embedded dist directory, falling back to
// index.html for client-side routes (SPA history fallback).
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("webui: dist not embedded: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(sub))
	index, _ := fs.ReadFile(sub, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(sub, p); err == nil {
			// Vite fingerprints everything under assets/, so it can be cached
			// forever; index.html (and other root files) must be revalidated
			// so a deploy reaches browsers on the next load.
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback: unknown non-asset paths return index.html.
		if strings.Contains(p, ".") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}
