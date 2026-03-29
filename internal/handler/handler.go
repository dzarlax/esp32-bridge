package handler

import (
	"fmt"
	"net/http"
	"time"

	"esp32-bridge/internal/fetcher"
)

type Handler struct {
	orch    *fetcher.Orchestrator
	apiKey  string
	startAt time.Time
}

func New(orch *fetcher.Orchestrator, apiKey string) *Handler {
	return &Handler{orch: orch, apiKey: apiKey, startAt: time.Now()}
}

func (h *Handler) requireAuth(w http.ResponseWriter, r *http.Request) bool {
	if h.apiKey == "" {
		return true
	}
	key := r.Header.Get("X-API-Key")
	if key == "" {
		key = r.URL.Query().Get("key")
	}
	if key != h.apiKey {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return false
	}
	return true
}

var sectionKeys = []string{"health", "tasks", "news", "sensors"}

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuth(w, r) {
		return
	}

	results := h.orch.FetchAll()

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ts":%d`, time.Now().Unix())
	for _, key := range sectionKeys {
		data, ok := results[key]
		if !ok || data == nil {
			fmt.Fprintf(w, `,"%s":null`, key)
		} else {
			fmt.Fprintf(w, `,"%s":%s`, key, data)
		}
	}
	w.Write([]byte("}"))
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	uptime := int(time.Since(h.startAt).Seconds())
	fmt.Fprintf(w, `{"status":"ok","uptime":%d}`, uptime)
}
