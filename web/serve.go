package web

import (
	"bytes"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

func Handler() http.Handler { return handlerFor(assetsFS) }

func handlerFor(source func() (fs.FS, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", 405)
			return
		}
		assets, e := source()
		if e != nil {
			http.Error(w, "Web assets not built. Run npm ci && npm run build in web/.", 503)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/settings/")
		if name == "" {
			name = "index.html"
		}
		if !fs.ValidPath(name) || path.Clean(name) != name {
			http.NotFound(w, r)
			return
		}
		if _, e = fs.Stat(assets, name); e != nil {
			if strings.Contains(path.Base(name), ".") {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if name == "index.html" {
			w.Header().Set("Cache-Control", "no-store")
			body, err := fs.ReadFile(assets, name)
			if err != nil {
				http.Error(w, "Web assets unavailable", 503)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
			return
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		copy := r.Clone(r.Context())
		copy.URL.Path = "/" + name
		http.FileServer(http.FS(assets)).ServeHTTP(w, copy)
	})
}
