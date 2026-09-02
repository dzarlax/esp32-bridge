package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"esp32-bridge/internal/fetcher"
	"esp32-bridge/internal/model"
)

type Handler struct {
	orch                    *fetcher.Orchestrator
	apiKey                  string
	haBaseURL               string
	haToken                 string
	haLights                map[string]struct{}
	haClimates              map[string]struct{}
	haClient                *http.Client
	calendarPlatformBaseURL string
	calendarPlatformToken   string
	calendarPlatformIDs     []string
	calendarLocation        *time.Location
	startAt                 time.Time
	otaGitHubRepo           string // "owner/repo"
	otaGitHubToken          string
	migrateBridgeURL        string
	// cached latest release info
	otaLatestVersion string
	otaLatestURL     string
	otaCheckedAt     time.Time
	fwCache          []byte
	fwCacheVersion   string
	otaMu            sync.Mutex
}

func New(orch *fetcher.Orchestrator, apiKey, haBaseURL, haToken string, haLights, haClimates []string, haClient *http.Client) *Handler {
	allowedLights := make(map[string]struct{}, len(haLights))
	for _, id := range haLights {
		allowedLights[id] = struct{}{}
	}
	allowedClimates := make(map[string]struct{}, len(haClimates))
	for _, id := range haClimates {
		allowedClimates[id] = struct{}{}
	}
	return &Handler{orch: orch, apiKey: apiKey, haBaseURL: haBaseURL, haToken: haToken, haLights: allowedLights, haClimates: allowedClimates, haClient: haClient, startAt: time.Now()}
}

func (h *Handler) SetOTA(ghRepo, ghToken, migrateBridgeURL string) {
	h.otaGitHubRepo = ghRepo
	h.otaGitHubToken = ghToken
	h.migrateBridgeURL = migrateBridgeURL
}

// SetCalendarPlatform enables Calendar Platform as the on-demand calendar
// source only when URL, token, and an explicit allowlist are all present.
func (h *Handler) SetCalendarPlatform(baseURL, token string, calendarIDs []string, timeZone string) {
	h.calendarPlatformBaseURL = strings.TrimSuffix(baseURL, "/")
	h.calendarPlatformToken = token
	h.calendarPlatformIDs = append([]string(nil), calendarIDs...)
	location, err := time.LoadLocation(timeZone)
	if err != nil {
		location = time.Local
	}
	h.calendarLocation = location
}

func (h *Handler) calendarPlatformEnabled() bool {
	return h.calendarPlatformBaseURL != "" && h.calendarPlatformToken != "" && len(h.calendarPlatformIDs) > 0
}

func (h *Handler) requireAuth(w http.ResponseWriter, r *http.Request) bool {
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

var sectionKeys = []string{"health", "tasks", "news", "sensors", "lights", "climate", "weather", "transport"}

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

// HAAction proxies explicitly allowed light and climate commands to Home Assistant.
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
		EntityID    string   `json:"entity_id"`
		Action      string   `json:"action"`
		Brightness  *int     `json:"brightness,omitempty"`
		Temperature *float64 `json:"temperature,omitempty"`
		HVACMode    string   `json:"hvac_mode,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	domain, service, cacheKey, err := h.haServiceForAction(req.EntityID, req.Action)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}

	body := map[string]interface{}{
		"entity_id": req.EntityID,
	}
	if domain == "light" && req.Brightness != nil && service == "turn_on" {
		body["brightness"] = *req.Brightness
	}
	if domain == "climate" && service == "set_temperature" {
		if req.Temperature == nil || *req.Temperature < 16 || *req.Temperature > 30 {
			http.Error(w, `{"error":"temperature must be between 16 and 30"}`, http.StatusBadRequest)
			return
		}
		body["temperature"] = *req.Temperature
	}
	if domain == "climate" && service == "set_hvac_mode" {
		if !allowedHVACMode(req.HVACMode) {
			http.Error(w, `{"error":"unsupported climate mode"}`, http.StatusBadRequest)
			return
		}
		body["hvac_mode"] = req.HVACMode
	}

	bodyJSON, _ := json.Marshal(body)
	haURL := fmt.Sprintf("%s/api/services/%s/%s", h.haBaseURL, domain, service)
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

	if h.orch != nil {
		h.orch.Invalidate(cacheKey)
	}

	w.Header().Set("Content-Type", "application/json")
	if haResp.StatusCode == 200 {
		// Fetch fresh state after a successful action.
		time.Sleep(200 * time.Millisecond)
		var data json.RawMessage
		if h.orch != nil {
			data = h.orch.FetchOne(cacheKey)
		}
		if data != nil {
			fmt.Fprintf(w, `{"ok":true,"%s":%s}`, cacheKey, data)
		} else {
			fmt.Fprintf(w, `{"ok":true}`)
		}
	} else {
		respBody, _ := io.ReadAll(haResp.Body)
		w.WriteHeader(haResp.StatusCode)
		w.Write(respBody)
	}
}

func (h *Handler) haServiceForAction(entityID, action string) (domain, service, cacheKey string, err error) {
	if strings.HasPrefix(entityID, "light.") {
		if _, ok := h.haLights[entityID]; !ok {
			return "", "", "", fmt.Errorf("light not configured")
		}
		switch action {
		case "turn_on", "turn_off", "toggle":
			return "light", action, "lights", nil
		default:
			return "", "", "", fmt.Errorf("unknown light action")
		}
	}
	if strings.HasPrefix(entityID, "climate.") {
		if _, ok := h.haClimates[entityID]; !ok {
			return "", "", "", fmt.Errorf("climate not configured")
		}
		switch action {
		case "turn_on", "turn_off", "set_temperature", "set_hvac_mode":
			return "climate", action, "climate", nil
		default:
			return "", "", "", fmt.Errorf("unknown climate action")
		}
	}
	return "", "", "", fmt.Errorf("unsupported entity")
}

func allowedHVACMode(mode string) bool {
	switch mode {
	case "cool", "heat", "dry", "fan_only":
		return true
	default:
		return false
	}
}

// Calendar fetches events for a given date. Calendar Platform takes precedence
// when fully configured; otherwise Home Assistant remains the compatibility path.
// GET /api/calendar?date=YYYY-MM-DD (defaults to today)
func (h *Handler) Calendar(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuth(w, r) {
		return
	}

	date := r.URL.Query().Get("date")
	if date == "" {
		location := h.calendarLocation
		if location == nil {
			location = time.Local
		}
		date = time.Now().In(location).Format("2006-01-02")
	}
	location := h.calendarLocation
	if location == nil {
		location = time.Local
	}
	dayStart, err := time.ParseInLocation("2006-01-02", date, location)
	if err != nil {
		http.Error(w, `{"error":"invalid date format, use YYYY-MM-DD"}`, http.StatusBadRequest)
		return
	}
	dayEnd := dayStart.AddDate(0, 0, 1)
	if h.calendarPlatformEnabled() {
		events, err := h.fetchCalendarPlatformEvents(r, dayStart, dayEnd)
		if err != nil {
			log.Printf("[calendar] Calendar Platform error: %v", err)
			http.Error(w, `{"error":"calendar source unavailable"}`, http.StatusBadGateway)
			return
		}
		writeCalendarEvents(w, events)
		return
	}
	if h.haBaseURL == "" || h.haToken == "" {
		http.Error(w, `{"error":"calendar not configured"}`, http.StatusServiceUnavailable)
		return
	}
	nextDay := dayEnd.Format("2006-01-02")

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

	writeCalendarEvents(w, allEvents)
}

func writeCalendarEvents(w http.ResponseWriter, events []model.CalendarEvent) {
	sort.SliceStable(events, func(i, j int) bool {
		left, right := events[i], events[j]
		if left.AllDay != right.AllDay {
			return left.AllDay
		}
		leftTime, rightTime := left.StartHour*60+left.StartMin, right.StartHour*60+right.StartMin
		if leftTime != rightTime {
			return leftTime < rightTime
		}
		if left.CalIdx != right.CalIdx {
			return left.CalIdx < right.CalIdx
		}
		return left.Summary < right.Summary
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(events)
}

type calendarPlatformEventTime struct {
	Date     string `json:"date"`
	DateTime string `json:"date_time"`
}

type calendarPlatformEvent struct {
	CalendarID string                    `json:"calendar_id"`
	Title      string                    `json:"title"`
	Status     string                    `json:"status"`
	Start      calendarPlatformEventTime `json:"start"`
	End        calendarPlatformEventTime `json:"end"`
}

func (h *Handler) fetchCalendarPlatformEvents(r *http.Request, dayStart, dayEnd time.Time) ([]model.CalendarEvent, error) {
	u, err := url.Parse(h.calendarPlatformBaseURL + "/api/native/v1/cached-events")
	if err != nil {
		return nil, err
	}
	query := u.Query()
	query.Set("start", dayStart.Format(time.RFC3339))
	query.Set("end", dayEnd.Format(time.RFC3339))
	for _, id := range h.calendarPlatformIDs {
		query.Add("calendar_id", id)
	}
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+h.calendarPlatformToken)
	resp, err := h.haClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Calendar Platform HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Items    []calendarPlatformEvent `json:"items"`
		Complete bool                    `json:"complete"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	if !payload.Complete {
		log.Printf("[calendar] Calendar Platform returned a partial result")
	}
	indices := make(map[string]int, len(h.calendarPlatformIDs))
	for index, id := range h.calendarPlatformIDs {
		indices[id] = index
	}
	result := make([]model.CalendarEvent, 0, len(payload.Items))
	for _, item := range payload.Items {
		index, allowed := indices[item.CalendarID]
		if !allowed || strings.EqualFold(item.Status, "cancelled") {
			continue
		}
		event, ok := mapCalendarPlatformEvent(item, index, dayStart, dayEnd)
		if ok {
			result = append(result, event)
		}
	}
	return result, nil
}

func mapCalendarPlatformEvent(item calendarPlatformEvent, calendarIndex int, dayStart, dayEnd time.Time) (model.CalendarEvent, bool) {
	event := model.CalendarEvent{Summary: model.SanitizeForDisplay(strings.TrimSpace(item.Title)), CalIdx: calendarIndex}
	if event.Summary == "" {
		event.Summary = "Событие"
	}
	if item.Start.Date != "" || item.End.Date != "" {
		start, startErr := time.Parse("2006-01-02", item.Start.Date)
		end, endErr := time.Parse("2006-01-02", item.End.Date)
		if startErr != nil || endErr != nil || !end.After(start) {
			return model.CalendarEvent{}, false
		}
		selected := dayStart.Format("2006-01-02")
		if selected < item.Start.Date || selected >= item.End.Date {
			return model.CalendarEvent{}, false
		}
		event.AllDay = true
		return event, true
	}
	start, startErr := time.Parse(time.RFC3339, item.Start.DateTime)
	end, endErr := time.Parse(time.RFC3339, item.End.DateTime)
	if startErr != nil || endErr != nil || !end.After(start) || !start.Before(dayEnd) || !end.After(dayStart) {
		return model.CalendarEvent{}, false
	}
	if start.Before(dayStart) {
		start = dayStart
	}
	if end.After(dayEnd) {
		end = dayEnd
	}
	start = start.In(dayStart.Location())
	end = end.In(dayStart.Location())
	event.StartHour, event.StartMin = start.Hour(), start.Minute()
	event.EndHour, event.EndMin = end.Hour(), end.Minute()
	return event, true
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
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cals); err != nil {
		return nil, err
	}

	var out []string
	for _, c := range cals {
		if isIgnoredCalendar(c.EntityID, c.Name) {
			continue
		}
		out = append(out, c.EntityID)
	}
	return out, nil
}

func isIgnoredCalendar(entityID, name string) bool {
	if entityID == "calendar.rabochii" {
		return true
	}

	calendarText := strings.ToLower(entityID + " " + name)
	return strings.Contains(calendarText, "workday_sensor") ||
		strings.Contains(calendarText, "microsoft") ||
		strings.Contains(calendarText, "outlook")
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
	h.otaMu.Lock()
	defer h.otaMu.Unlock()
	h.refreshLatestReleaseLocked()
}

func (h *Handler) refreshLatestReleaseLocked() {
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
	if _, ok := parseVersion(version); !ok {
		log.Printf("[ota] invalid release version: %q", release.TagName)
		return
	}

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

	h.otaMu.Lock()
	defer h.otaMu.Unlock()
	h.refreshLatestReleaseLocked()

	if h.otaLatestVersion == "" {
		fmt.Fprintf(w, `{"update":false}`)
		return
	}

	clientVersion := r.URL.Query().Get("v")
	if compareVersions(h.otaLatestVersion, clientVersion) <= 0 {
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

	h.otaMu.Lock()
	defer h.otaMu.Unlock()
	h.refreshLatestReleaseLocked()

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

func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, part := range parts {
		if part == "" {
			return v, false
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func compareVersions(remote, client string) int {
	r, ok := parseVersion(strings.TrimPrefix(remote, "v"))
	if !ok {
		return -1
	}
	c, ok := parseVersion(strings.TrimPrefix(client, "v"))
	if !ok {
		return -1
	}
	for i := range r {
		if r[i] > c[i] {
			return 1
		}
		if r[i] < c[i] {
			return -1
		}
	}
	return 0
}
