package handler

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type observation struct {
	EntityID          string   `json:"entity_id"`
	Kind              string   `json:"kind"`
	Available         bool     `json:"available"`
	State             string   `json:"state"`
	TargetTemperature *float64 `json:"target_temperature,omitempty"`
}

func (h *Handler) entityKind(id string) string {
	if _, ok := h.haLights[id]; ok {
		return "light"
	}
	if _, ok := h.haClimates[id]; ok {
		return "climate"
	}
	return ""
}

func (h *Handler) readObservation(parent context.Context, id, kind string) (*observation, string) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(h.haBaseURL, "/")+"/api/states/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, "upstream_unavailable"
	}
	req.Header.Set("Authorization", "Bearer "+h.haToken)
	resp, err := h.haClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "timeout"
		}
		return nil, "upstream_unavailable"
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, "unauthorized"
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "upstream_unavailable"
	}
	var raw struct {
		EntityID   string `json:"entity_id"`
		State      string `json:"state"`
		Attributes struct {
			Temperature *float64 `json:"temperature"`
		} `json:"attributes"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); err != nil {
		if ctx.Err() != nil {
			return nil, "timeout"
		}
		return nil, "invalid_response"
	}
	if raw.EntityID != id {
		return nil, "invalid_response"
	}
	o := &observation{EntityID: id, Kind: kind, State: raw.State}
	if kind == "light" {
		o.Available = raw.State == "on" || raw.State == "off"
	} else {
		o.Available = raw.State == "off" || allowedHVACMode(raw.State) || raw.State == "auto" || raw.State == "heat_cool"
	}
	if !o.Available {
		o.State = "unknown"
		return o, ""
	}
	if kind == "climate" && raw.Attributes.Temperature != nil && !math.IsNaN(*raw.Attributes.Temperature) && !math.IsInf(*raw.Attributes.Temperature, 0) {
		o.TargetTemperature = raw.Attributes.Temperature
	}
	return o, ""
}

func (h *Handler) HAState(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuth(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"unsupported"}`, http.StatusMethodNotAllowed)
		return
	}
	ids, ok := r.URL.Query()["entity_id"]
	if !ok || len(ids) != 1 || h.entityKind(ids[0]) == "" {
		http.Error(w, `{"error":"unsupported"}`, http.StatusBadRequest)
		return
	}
	if h.haBaseURL == "" || h.haToken == "" {
		http.Error(w, `{"error":"upstream_unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	o, code := h.readObservation(r.Context(), ids[0], h.entityKind(ids[0]))
	if code != "" {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusBadGateway
		if code == "timeout" {
			status = http.StatusGatewayTimeout
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"observation": o})
}
