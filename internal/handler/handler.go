package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"esp32-bridge/internal/fetcher"
)

type Handler struct {
	orch      *fetcher.Orchestrator
	apiKey    string
	haBaseURL string
	haToken   string
	haClient  *http.Client
	startAt   time.Time
}

func New(orch *fetcher.Orchestrator, apiKey, haBaseURL, haToken string, haClient *http.Client) *Handler {
	return &Handler{orch: orch, apiKey: apiKey, haBaseURL: haBaseURL, haToken: haToken, haClient: haClient, startAt: time.Now()}
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

var sectionKeys = []string{"health", "tasks", "news", "sensors", "lights"}

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

// HAAction proxies light control commands to Home Assistant.
// POST /api/ha/action with JSON body:
//
//	{"entity_id": "light.office_light", "action": "toggle"}
//	{"entity_id": "light.office_light", "action": "turn_on", "brightness": 128}
func (h *Handler) HAAction(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if h.haBaseURL == "" || h.haToken == "" {
		http.Error(w, `{"error":"HA not configured"}`, http.StatusServiceUnavailable)
		return
	}

	var req struct {
		EntityID   string `json:"entity_id"`
		Action     string `json:"action"` // toggle, turn_on, turn_off
		Brightness *int   `json:"brightness,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	// Validate entity_id starts with light.
	if !strings.HasPrefix(req.EntityID, "light.") {
		http.Error(w, `{"error":"only light entities supported"}`, http.StatusBadRequest)
		return
	}

	// Map action to HA service
	service := "toggle"
	switch req.Action {
	case "turn_on":
		service = "turn_on"
	case "turn_off":
		service = "turn_off"
	case "toggle":
		service = "toggle"
	default:
		http.Error(w, `{"error":"unknown action"}`, http.StatusBadRequest)
		return
	}

	// Build HA service call body
	body := map[string]interface{}{
		"entity_id": req.EntityID,
	}
	if req.Brightness != nil && service == "turn_on" {
		body["brightness"] = *req.Brightness
	}

	bodyJSON, _ := json.Marshal(body)
	haURL := fmt.Sprintf("%s/api/services/light/%s", h.haBaseURL, service)
	haReq, err := http.NewRequestWithContext(r.Context(), "POST", haURL, strings.NewReader(string(bodyJSON)))
	if err != nil {
		http.Error(w, `{"error":"failed to create request"}`, http.StatusInternalServerError)
		return
	}
	haReq.Header.Set("Authorization", "Bearer "+h.haToken)
	haReq.Header.Set("Content-Type", "application/json")

	haResp, err := h.haClient.Do(haReq)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadGateway)
		return
	}
	defer haResp.Body.Close()

	// Invalidate lights cache so next dashboard fetch gets fresh state
	h.orch.Invalidate("lights")

	w.Header().Set("Content-Type", "application/json")
	if haResp.StatusCode == 200 {
		fmt.Fprintf(w, `{"ok":true}`)
	} else {
		respBody, _ := io.ReadAll(haResp.Body)
		w.WriteHeader(haResp.StatusCode)
		w.Write(respBody)
	}
}
