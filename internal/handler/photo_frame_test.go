package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"esp32-bridge/internal/photoframe"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPhotoEndpointsAuthMethodAndDisabled(t *testing.T) {
	h := New(nil, "secret", "", "", nil, nil, nil)
	h.SetPhotoFrame(photoframe.New(false, nil, 300, 60, 900, 32))
	for _, tc := range []struct {
		method, path, key string
		want              int
	}{{"GET", "/api/photo-frame/v1/manifest", "", 401}, {"GET", "/api/photo-frame/v1/manifest?key=secret", "", 401}, {"POST", "/api/photo-frame/v1/manifest", "secret", 405}, {"GET", "/api/photo-frame/v1/frames/a", "secret", 503}} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r.Header.Set("X-API-Key", tc.key)
		w := httptest.NewRecorder()
		if tc.path == "/api/photo-frame/v1/frames/a" {
			h.PhotoFrame(w, r)
		} else {
			h.PhotoManifest(w, r)
		}
		if w.Code != tc.want {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/api/photo-frame/v1/manifest", nil)
	r.Header.Set("X-API-Key", "secret")
	w := httptest.NewRecorder()
	h.PhotoManifest(w, r)
	var m photoframe.Manifest
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || m.State != "disabled" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response %s", w.Body.String())
	}
}

func TestPhotoFrameBodyAndHeaders(t *testing.T) {
	s := photoframe.New(true, photoframe.FixtureSource{}, 300, 60, 900, 32)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	var m photoframe.Manifest
	for time.Now().Before(deadline) {
		m = s.Manifest()
		if m.State == "ready" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if m.State != "ready" {
		t.Fatal("fixture not prepared")
	}
	h := New(nil, "secret", "", "", nil, nil, nil)
	h.SetPhotoFrame(s)
	r := httptest.NewRequest("GET", "/api/photo-frame/v1/frames/"+m.Frames[0].ID, nil)
	r.Header.Set("X-API-Key", "secret")
	r.Header.Set("Range", "bytes=0-99")
	w := httptest.NewRecorder()
	h.PhotoFrame(w, r)
	if w.Code != 200 || w.Body.Len() != photoframe.FrameBytes || w.Header().Get("Content-Length") != "1228800" || w.Header().Get("Content-Type") != "application/octet-stream" || w.Header().Get("Content-Range") != "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("bad response %d %v %d", w.Code, w.Header(), w.Body.Len())
	}
	sum := sha256.Sum256(w.Body.Bytes())
	if hex.EncodeToString(sum[:]) != m.Frames[0].SHA256 {
		t.Fatal("wrong hash")
	}
	r = httptest.NewRequest("GET", "/api/photo-frame/v1/frames/"+strings.Repeat("0", 64), nil)
	r.Header.Set("X-API-Key", "secret")
	w = httptest.NewRecorder()
	h.PhotoFrame(w, r)
	if w.Code != 404 {
		t.Fatalf("missing frame status %d", w.Code)
	}
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
	reset    bool
}

func (w *deadlineRecorder) SetWriteDeadline(d time.Time) error {
	if d.IsZero() {
		w.reset = true
	} else {
		w.deadline = d
	}
	return nil
}
func TestPhotoFrameSetsWriteDeadline(t *testing.T) {
	s := photoframe.New(true, photoframe.FixtureSource{}, 300, 60, 900, 32)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	var m photoframe.Manifest
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
		m = s.Manifest()
		if m.State == "ready" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if m.State != "ready" {
		t.Fatal("fixture not ready")
	}
	h := New(nil, "secret", "", "", nil, nil, nil)
	h.SetPhotoFrame(s)
	r := httptest.NewRequest("GET", "/api/photo-frame/v1/frames/"+m.Frames[0].ID, nil)
	r.Header.Set("X-API-Key", "secret")
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	before := time.Now()
	h.PhotoFrame(w, r)
	if !w.reset || w.deadline.Before(before.Add(14*time.Second)) || w.deadline.After(time.Now().Add(16*time.Second)) {
		t.Fatalf("unexpected deadline %v", w.deadline)
	}
}
