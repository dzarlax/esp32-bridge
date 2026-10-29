package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"esp32-bridge/internal/photoframe"
)

func (h *Handler) SetPhotoFrame(s *photoframe.Service) { h.photoFrame = s }
func (h *Handler) PhotoManifest(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-API-Key") == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !h.requireAuth(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", 405)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if h.photoFrame == nil {
		http.Error(w, "unavailable", 500)
		return
	}
	_ = json.NewEncoder(w).Encode(h.photoFrame.Manifest())
}
func (h *Handler) PhotoFrame(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-API-Key") == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !h.requireAuth(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", 405)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if h.photoFrame == nil {
		http.Error(w, "unavailable", 500)
		return
	}
	if !h.photoFrame.Enabled() {
		http.Error(w, "disabled", 503)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/photo-frame/v1/frames/")
	if len(id) != 64 || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	if !h.photoFrame.Acquire() {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "busy", 503)
		return
	}
	defer h.photoFrame.Release()
	data, release, ok := h.photoFrame.Borrow(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	defer release()
	// Bound each transfer without affecting dashboard requests.
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second))
	defer controller.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}
