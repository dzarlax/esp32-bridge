package handler

import (
	"fmt"
	"net/http"
	"time"

	"esp32-bridge/internal/fetcher"
)

type Handler struct {
	orch    *fetcher.Orchestrator
	startAt time.Time
}

func New(orch *fetcher.Orchestrator) *Handler {
	return &Handler{orch: orch, startAt: time.Now()}
}

var sectionKeys = []string{"health", "tasks", "news", "sensors"}

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
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
