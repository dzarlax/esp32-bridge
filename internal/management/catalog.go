package management

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const catalogLimit = 2 << 20

type catalogItem struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Domain     string `json:"domain,omitempty"`
	Available  *bool  `json:"available,omitempty"`
	AssetCount *int   `json:"asset_count,omitempty"`
	Provider   string `json:"provider,omitempty"`
	TimeZone   string `json:"time_zone,omitempty"`
	Color      string `json:"color,omitempty"`
	ReadOnly   bool   `json:"read_only,omitempty"`
}
type catalogEntry struct {
	kind    string
	items   []catalogItem
	expires time.Time
}
type catalogCache struct {
	mu      sync.Mutex
	entries map[[32]byte]catalogEntry
}

func calendarProvider(id string) string {
	for _, provider := range []string{"google", "microsoft", "apple"} {
		if strings.HasPrefix(id, provider+":") {
			return provider
		}
	}
	return ""
}

func validCatalogText(v string, limit int) bool {
	if v == "" || len(v) > limit || !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validEntityID(id string) bool {
	if len(id) > 255 || !validCatalogText(id, 255) {
		return false
	}
	parts := strings.SplitN(id, ".", 2)
	if len(parts) != 2 || (parts[0] != "light" && parts[0] != "climate" && parts[0] != "sensor" && !validEntitySlug(parts[0])) {
		return false
	}
	return validEntitySlug(parts[1])
}

func validEntitySlug(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' {
			return false
		}
	}
	return true
}
func (a *API) catalogCurrent(kind, base, secret string) bool {
	values, secrets := a.Store.Snapshot()
	if kind == "entities" {
		return values[""].Services["home"].BaseURL == base && secrets["ha_token"] == secret
	}
	if kind == "calendars" {
		return values[""].Services["calendar"].BaseURL == base && secrets["calendar_platform_token"] == secret
	}
	return values[""].Photos.BaseURL == base && secrets["immich_api_key"] == secret
}
func (a *API) catalog(w http.ResponseWriter, r *http.Request, id, kind string) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if !a.Store.HasDevice(id) {
		failure(w, http.StatusNotFound, "device_not_found")
		return
	}
	values, secrets := a.Store.Snapshot()
	base, secret, path, header := values[""].Photos.BaseURL, secrets["immich_api_key"], "/api/albums", "X-API-Key"
	if kind == "calendars" {
		base, secret, path, header = values[""].Services["calendar"].BaseURL, secrets["calendar_platform_token"], "/api/native/v1/calendars", "Authorization"
	} else if kind == "entities" {
		base, secret, path, header = values[""].Services["home"].BaseURL, secrets["ha_token"], "/api/states", "Authorization"
	}
	if base == "" || secret == "" {
		failure(w, http.StatusServiceUnavailable, "catalog_not_configured")
		return
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (kind == "albums" && u.Scheme != "https") || (kind != "albums" && u.Scheme != "https" && u.Scheme != "http") {
		failure(w, http.StatusServiceUnavailable, "catalog_not_configured")
		return
	}
	key := sha256.Sum256([]byte(kind + "\x00" + base + "\x00" + secret))
	a.catalogs.mu.Lock()
	entry, ok := a.catalogs.entries[key]
	if ok && time.Now().Before(entry.expires) {
		a.catalogs.mu.Unlock()
		if !a.catalogCurrent(kind, base, secret) {
			failure(w, http.StatusConflict, "catalog_connection_changed")
			return
		}
		output(w, http.StatusOK, map[string]any{"items": entry.items, "complete": true})
		return
	}
	a.catalogs.mu.Unlock()
	client := &http.Client{}
	if a.catalogClient != nil {
		*client = *a.catalogClient
	}
	if client.Timeout <= 0 || client.Timeout > 15*time.Second {
		client.Timeout = 15 * time.Second
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		failure(w, http.StatusServiceUnavailable, "catalog_not_configured")
		return
	}
	auth := secret
	if header == "Authorization" {
		auth = "Bearer " + secret
	}
	req.Header.Set(header, auth)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			failure(w, http.StatusGatewayTimeout, "catalog_timeout")
		} else {
			failure(w, http.StatusBadGateway, "catalog_upstream_error")
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		failure(w, http.StatusBadGateway, "catalog_forbidden")
		return
	}
	if resp.StatusCode == 404 {
		failure(w, http.StatusBadGateway, "catalog_api_unavailable")
		return
	}
	if resp.StatusCode == http.StatusServiceUnavailable {
		payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if readErr != nil {
			var ne net.Error
			if errors.As(readErr, &ne) && ne.Timeout() {
				failure(w, http.StatusGatewayTimeout, "catalog_timeout")
			} else {
				failure(w, http.StatusBadGateway, "catalog_upstream_error")
			}
			return
		}
		var detail struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(payload, &detail) == nil && detail.Error == "catalog_too_large" {
			failure(w, http.StatusServiceUnavailable, "catalog_too_large")
			return
		}
		failure(w, http.StatusBadGateway, "catalog_upstream_error")
		return
	}
	if resp.StatusCode != 200 {
		failure(w, http.StatusBadGateway, "catalog_upstream_error")
		return
	}
	maxBody := int64(catalogLimit)
	if kind == "entities" {
		maxBody = 8 << 20
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			failure(w, http.StatusGatewayTimeout, "catalog_timeout")
		} else {
			failure(w, http.StatusBadGateway, "catalog_upstream_error")
		}
		return
	}
	if int64(len(body)) > maxBody {
		failure(w, http.StatusBadGateway, "catalog_too_large")
		return
	}
	items := []catalogItem{}
	seen := map[string]bool{}
	if kind == "albums" {
		var rows []struct {
			ID    string `json:"id"`
			Name  string `json:"albumName"`
			Count *int   `json:"assetCount"`
		}
		if json.Unmarshal(body, &rows) != nil || rows == nil {
			failure(w, http.StatusBadGateway, "catalog_invalid_response")
			return
		}
		if len(rows) > 2000 {
			failure(w, http.StatusBadGateway, "catalog_too_large")
			return
		}
		for _, row := range rows {
			if !validCatalogText(row.ID, 64) || !validCatalogText(row.Name, 512) || seen[row.ID] || (row.Count != nil && *row.Count < 0) {
				failure(w, http.StatusBadGateway, "catalog_invalid_response")
				return
			}
			seen[row.ID] = true
			items = append(items, catalogItem{ID: row.ID, Name: row.Name, AssetCount: row.Count})
		}
	} else if kind == "calendars" {
		var data struct {
			Calendars []struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				TimeZone string `json:"time_zone"`
				Color    string `json:"color"`
				CanRead  bool   `json:"can_read"`
				ReadOnly bool   `json:"read_only"`
			} `json:"calendars"`
		}
		if json.Unmarshal(body, &data) != nil || data.Calendars == nil {
			failure(w, http.StatusBadGateway, "catalog_invalid_response")
			return
		}
		if len(data.Calendars) > 2000 {
			failure(w, http.StatusBadGateway, "catalog_too_large")
			return
		}
		for _, row := range data.Calendars {
			if !validCatalogText(row.ID, 512) || !validCatalogText(row.Name, 512) || seen[row.ID] || (row.TimeZone != "" && !validCatalogText(row.TimeZone, 64)) || (row.Color != "" && !validCatalogText(row.Color, 64)) || !row.CanRead || !row.ReadOnly {
				failure(w, http.StatusBadGateway, "catalog_invalid_response")
				return
			}
			seen[row.ID] = true
			items = append(items, catalogItem{ID: row.ID, Name: row.Name, Provider: calendarProvider(row.ID), TimeZone: row.TimeZone, Color: row.Color, ReadOnly: true})
		}
	} else {
		var rows []struct {
			ID    string          `json:"entity_id"`
			State json.RawMessage `json:"state"`
			Attrs json.RawMessage `json:"attributes"`
		}
		if json.Unmarshal(body, &rows) != nil || rows == nil {
			failure(w, http.StatusBadGateway, "catalog_invalid_response")
			return
		}
		if len(rows) > 5000 {
			failure(w, http.StatusBadGateway, "catalog_too_large")
			return
		}
		for _, row := range rows {
			if !validEntityID(row.ID) || seen[row.ID] {
				failure(w, http.StatusBadGateway, "catalog_invalid_response")
				return
			}
			seen[row.ID] = true
			domain := strings.SplitN(row.ID, ".", 2)[0]
			if domain != "light" && domain != "climate" && domain != "sensor" {
				continue
			}
			var state string
			var attrs struct {
				Name string `json:"friendly_name"`
			}
			if json.Unmarshal(row.State, &state) != nil || json.Unmarshal(row.Attrs, &attrs) != nil || (attrs.Name != "" && !validCatalogText(attrs.Name, 512)) {
				failure(w, http.StatusBadGateway, "catalog_invalid_response")
				return
			}
			name := attrs.Name
			if name == "" {
				name = row.ID
			}
			available := state != "unavailable" && state != "unknown"
			items = append(items, catalogItem{ID: row.ID, Name: name, Domain: domain, Available: &available})
		}
	}
	if !a.catalogCurrent(kind, base, secret) {
		failure(w, http.StatusConflict, "catalog_connection_changed")
		return
	}
	a.catalogs.mu.Lock()
	if a.catalogs.entries == nil {
		a.catalogs.entries = make(map[[32]byte]catalogEntry)
	}
	for k, v := range a.catalogs.entries {
		if time.Now().After(v.expires) || (v.kind == kind && k != key) {
			delete(a.catalogs.entries, k)
		}
	}
	a.catalogs.entries[key] = catalogEntry{kind: kind, items: items, expires: time.Now().Add(time.Minute)}
	a.catalogs.mu.Unlock()
	output(w, http.StatusOK, map[string]any{"items": items, "complete": true})
}
