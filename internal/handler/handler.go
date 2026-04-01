package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"esp32-bridge/internal/fetcher"
	"esp32-bridge/internal/model"
)

type Handler struct {
	orch             *fetcher.Orchestrator
	apiKey           string
	haBaseURL        string
	haToken          string
	haClient         *http.Client
	startAt          time.Time
	otaGitHubRepo    string // "owner/repo"
	otaGitHubToken   string
	migrateBridgeURL string
	// cached latest release info
	otaLatestVersion string
	otaLatestURL     string
	otaCheckedAt     time.Time
	fwCache          []byte
	fwCacheVersion   string
}

func New(orch *fetcher.Orchestrator, apiKey, haBaseURL, haToken string, haClient *http.Client) *Handler {
	return &Handler{orch: orch, apiKey: apiKey, haBaseURL: haBaseURL, haToken: haToken, haClient: haClient, startAt: time.Now()}
}

func (h *Handler) SetOTA(ghRepo, ghToken, migrateBridgeURL string) {
	h.otaGitHubRepo = ghRepo
	h.otaGitHubToken = ghToken
	h.migrateBridgeURL = migrateBridgeURL
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

var sectionKeys = []string{"health", "tasks", "news", "sensors", "lights", "weather", "transport"}

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
	if h.migrateBridgeURL != "" {
		fmt.Fprintf(w, `,"config":{"bridge_url":"%s"}`, h.migrateBridgeURL)
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
		// Fetch fresh lights state after toggle
		time.Sleep(200 * time.Millisecond)
		lights := h.orch.FetchOne("lights")
		if lights != nil {
			fmt.Fprintf(w, `{"ok":true,"lights":%s}`, lights)
		} else {
			fmt.Fprintf(w, `{"ok":true}`)
		}
	} else {
		respBody, _ := io.ReadAll(haResp.Body)
		w.WriteHeader(haResp.StatusCode)
		w.Write(respBody)
	}
}

// Calendar fetches events from Home Assistant for a given date.
// GET /api/calendar?date=YYYY-MM-DD (defaults to today)
func (h *Handler) Calendar(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuth(w, r) {
		return
	}
	if h.haBaseURL == "" || h.haToken == "" {
		http.Error(w, `{"error":"HA not configured"}`, http.StatusServiceUnavailable)
		return
	}

	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	// Calculate next day for the range end
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		http.Error(w, `{"error":"invalid date format, use YYYY-MM-DD"}`, http.StatusBadRequest)
		return
	}
	nextDay := t.AddDate(0, 0, 1).Format("2006-01-02")

	// Phase 1: get calendar list
	calendars, err := h.fetchCalendarList(r)
	if err != nil {
		log.Printf("[calendar] list error: %v", err)
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadGateway)
		return
	}

	// Phase 2: fetch events from each calendar in parallel
	var allEvents []model.CalendarEvent
	var mu sync.Mutex
	var wg sync.WaitGroup

	for calIdx, calID := range calendars {
		wg.Add(1)
		go func(idx int, entityID string) {
			defer wg.Done()
			events, err := h.fetchCalendarEvents(r, entityID, date, nextDay, idx)
			if err != nil {
				log.Printf("[calendar] events error for %s: %v", entityID, err)
				return
			}
			mu.Lock()
			allEvents = append(allEvents, events...)
			mu.Unlock()
		}(calIdx, calID)
	}
	wg.Wait()

	// Sort: all-day first, then by start time
	sort.Slice(allEvents, func(i, j int) bool {
		ki := allEvents[i].StartHour*100 + allEvents[i].StartMin
		kj := allEvents[j].StartHour*100 + allEvents[j].StartMin
		if allEvents[i].AllDay {
			ki = -1
		}
		if allEvents[j].AllDay {
			kj = -1
		}
		return ki < kj
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(allEvents)
}

func (h *Handler) fetchCalendarList(r *http.Request) ([]string, error) {
	req, err := http.NewRequestWithContext(r.Context(), "GET", h.haBaseURL+"/api/calendars", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+h.haToken)

	resp, err := h.haClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var cals []struct {
		EntityID string `json:"entity_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cals); err != nil {
		return nil, err
	}

	var out []string
	for _, c := range cals {
		if strings.Contains(c.EntityID, "workday_sensor") {
			continue
		}
		out = append(out, c.EntityID)
	}
	return out, nil
}

func (h *Handler) fetchCalendarEvents(r *http.Request, entityID, date, nextDay string, calIdx int) ([]model.CalendarEvent, error) {
	u := fmt.Sprintf("%s/api/calendars/%s?start=%sT00:00:00&end=%sT00:00:00",
		h.haBaseURL, entityID, date, nextDay)
	req, err := http.NewRequestWithContext(r.Context(), "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+h.haToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.haClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var rawEvents []struct {
		Summary string `json:"summary"`
		Start   struct {
			Date     string `json:"date"`
			DateTime string `json:"dateTime"`
		} `json:"start"`
		End struct {
			DateTime string `json:"dateTime"`
		} `json:"end"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rawEvents); err != nil {
		return nil, err
	}

	var events []model.CalendarEvent
	for _, e := range rawEvents {
		ev := model.CalendarEvent{
			Summary: model.SanitizeForDisplay(e.Summary),
			CalIdx:  calIdx,
		}
		if e.Start.Date != "" {
			ev.AllDay = true
		} else if e.Start.DateTime != "" {
			ev.StartHour, ev.StartMin = parseTime(e.Start.DateTime)
			ev.EndHour, ev.EndMin = parseTime(e.End.DateTime)
		}
		events = append(events, ev)
	}
	return events, nil
}

// parseTime extracts HH:MM from "2026-04-01T09:30:00+02:00" or "2026-04-01T09:30:00"
func parseTime(dt string) (int, int) {
	// Find the T separator
	idx := strings.IndexByte(dt, 'T')
	if idx < 0 || idx+6 > len(dt) {
		return 0, 0
	}
	timePart := dt[idx+1:]
	var h, m int
	fmt.Sscanf(timePart, "%d:%d", &h, &m)
	return h, m
}

// refreshLatestRelease fetches the latest release from GitHub API.
// Caches result for 5 minutes to avoid rate limiting.
func (h *Handler) refreshLatestRelease() {
	if h.otaGitHubRepo == "" {
		return
	}
	if time.Since(h.otaCheckedAt) < 5*time.Minute {
		return // use cached
	}

	u := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", h.otaGitHubRepo)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if h.otaGitHubToken != "" {
		req.Header.Set("Authorization", "token "+h.otaGitHubToken)
	}

	resp, err := h.haClient.Do(req)
	if err != nil {
		log.Printf("[ota] GitHub API error: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("[ota] GitHub API HTTP %d", resp.StatusCode)
		return
	}

	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		log.Printf("[ota] GitHub API parse error: %v", err)
		return
	}

	// Strip "v" prefix from tag: "v1.0.1" → "1.0.1"
	version := strings.TrimPrefix(release.TagName, "v")

	// Find firmware.bin asset
	var fwURL string
	for _, a := range release.Assets {
		if a.Name == "firmware.bin" {
			fwURL = a.BrowserDownloadURL
			break
		}
	}

	if version != "" && fwURL != "" {
		h.otaLatestVersion = version
		h.otaLatestURL = fwURL
		h.otaCheckedAt = time.Now()
		log.Printf("[ota] latest release: %s (%s)", version, fwURL)
	}
}

// OTACheck reports whether a firmware update is available.
// GET /api/ota/check?v=1.0.0
func (h *Handler) OTACheck(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuth(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")

	if h.otaGitHubRepo == "" {
		fmt.Fprintf(w, `{"update":false}`)
		return
	}

	h.refreshLatestRelease()

	if h.otaLatestVersion == "" {
		fmt.Fprintf(w, `{"update":false}`)
		return
	}

	clientVersion := r.URL.Query().Get("v")
	if clientVersion == h.otaLatestVersion {
		fmt.Fprintf(w, `{"update":false}`)
		return
	}

	fmt.Fprintf(w, `{"update":true,"version":"%s"}`, h.otaLatestVersion)
}

// OTAFirmware streams the firmware binary to the ESP32.
// GET /api/ota/firmware
func (h *Handler) OTAFirmware(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuth(w, r) {
		return
	}

	h.refreshLatestRelease()

	if h.otaLatestURL == "" {
		http.Error(w, `{"error":"no release found"}`, http.StatusServiceUnavailable)
		return
	}

	// Serve from cache if version matches
	if h.fwCache != nil && h.fwCacheVersion == h.otaLatestVersion {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(h.fwCache)))
		w.Write(h.fwCache)
		return
	}

	// Download from GitHub Release
	req, err := http.NewRequestWithContext(r.Context(), "GET", h.otaLatestURL, nil)
	if err != nil {
		http.Error(w, `{"error":"failed to create request"}`, http.StatusInternalServerError)
		return
	}
	if h.otaGitHubToken != "" {
		req.Header.Set("Authorization", "token "+h.otaGitHubToken)
	}

	resp, err := h.haClient.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		http.Error(w, fmt.Sprintf(`{"error":"GitHub HTTP %d"}`, resp.StatusCode), resp.StatusCode)
		return
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, `{"error":"failed to read firmware"}`, http.StatusBadGateway)
		return
	}

	// Cache for subsequent requests
	h.fwCache = data
	h.fwCacheVersion = h.otaLatestVersion

	log.Printf("[ota] cached firmware v%s (%d bytes)", h.otaLatestVersion, len(data))

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Write(data)
}
