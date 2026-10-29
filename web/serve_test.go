package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAppIndexDoesNotRedirectAndAssetsRemainBounded(t *testing.T) {
	assets := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>HomeDash</title>")}, "assets/app.js": {Data: []byte("console.log('isolated')")}}
	handler := handlerFor(func() (fs.FS, error) { return assets, nil })
	for _, path := range []string{"/settings/", "/settings/index.html", "/settings/network"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "HomeDash") || w.Header().Get("Location") != "" {
			t.Fatalf("app redirect at %s: %d %s", path, w.Code, w.Header().Get("Location"))
		}
		if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatal("missing private app headers")
		}
	}
	for _, path := range []string{"/settings/assets/missing.js", "/settings/../secret"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal("unexpected asset fallback", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/settings/", nil))
	if w.Code != 405 {
		t.Fatal("unsafe method accepted")
	}
}

func TestEmbeddedBuildContainsAllApplicationAssets(t *testing.T) {
	assets, e := assetsFS()
	if e != nil {
		t.Skip("frontend build required for asset integration")
	}
	index, e := fs.ReadFile(assets, "index.html")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(index), "/settings/assets/") {
		t.Fatal("wrong production asset base")
	}
	entries, e := fs.ReadDir(assets, "assets")
	if e != nil || len(entries) < 2 {
		t.Fatal("missing application assets", e)
	}
	for _, entry := range entries {
		w := httptest.NewRecorder()
		handlerFor(func() (fs.FS, error) { return assets, nil }).ServeHTTP(w, httptest.NewRequest("GET", "/settings/assets/"+entry.Name(), nil))
		if w.Code != 200 || w.Body.Len() == 0 || w.Header().Get("Location") != "" {
			t.Fatal("asset not served", entry.Name(), w.Code)
		}
	}
}
