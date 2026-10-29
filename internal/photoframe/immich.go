package photoframe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	immichTimeout    = 30 * time.Second
	immichPageSize   = 200
	immichMaxAssets  = 5000
	immichMaxJSON    = 4 << 20
	immichMaxPreview = 20 << 20
)

var (
	ErrAccessDenied      = errors.New("Immich access denied")
	errImmichResponse    = errors.New("invalid Immich response")
	errImmichRequest     = errors.New("Immich request failed")
	errImmichTooLarge    = errors.New("Immich response exceeds size limit")
	errImmichRedirect    = errors.New("Immich redirects are not allowed")
	errImmichAssetAccess = errors.New("Immich asset is not available")
)

// ValidateImmichEndpoint validates the URL and album identifier without
// requiring an API key. It is suitable for configuration validation.
func ValidateImmichEndpoint(baseURL, albumID string) error {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(baseURL, "#") || u.Opaque != "" {
		return errors.New("invalid Immich endpoint")
	}
	if !validUUIDv4(albumID) {
		return errors.New("invalid Immich album ID")
	}
	return nil
}

// ImmichSource reads eligible images from one Immich album.
type ImmichSource struct {
	baseURL string
	albumID string
	apiKey  string
	client  *http.Client

	mu        sync.RWMutex
	allowlist map[string]string
}

// NewImmich constructs a source for a single Immich album. The supplied HTTP
// client is cloned, so its redirect policy and timeout are not modified.
func NewImmich(baseURL, albumID, apiKey string, client *http.Client) (*ImmichSource, error) {
	if err := ValidateImmichEndpoint(baseURL, albumID); err != nil {
		return nil, err
	}
	if apiKey == "" {
		return nil, errors.New("invalid Immich API key")
	}
	for _, r := range apiKey {
		if r < 0x21 || r > 0x7e {
			return nil, errors.New("invalid Immich API key")
		}
	}
	if client == nil {
		client = http.DefaultClient
	}
	cloned := *client
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	cloned.Timeout = immichTimeout
	return &ImmichSource{
		baseURL: strings.TrimRight(baseURL, "/"), albumID: albumID, apiKey: apiKey,
		client: &cloned, allowlist: make(map[string]string),
	}, nil
}

func (s *ImmichSource) clearAllowlist() {
	s.mu.Lock()
	s.allowlist = make(map[string]string)
	s.mu.Unlock()
}

func (s *ImmichSource) endpoint(path string) string { return s.baseURL + path }

func (s *ImmichSource) request(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.endpoint(path), r)
	if err != nil {
		return nil, errImmichRequest
	}
	req.Header.Set("X-API-Key", s.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if strings.HasPrefix(path, "/api/assets/") {
		req.Header.Set("Accept", "image/jpeg, image/png, image/webp")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, errImmichRequest
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		s.clearAllowlist()
		return nil, ErrAccessDenied
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		resp.Body.Close()
		return nil, errImmichRedirect
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, errImmichRequest
	}
	return resp, nil
}

type immichAlbum struct {
	ID string `json:"id"`
}

type immichSearchResponse struct {
	Assets struct {
		Items      *[]immichAsset  `json:"items"`
		NextCursor json.RawMessage `json:"nextCursor"`
	} `json:"assets"`
}

type immichAsset struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Visibility string `json:"visibility"`
	Archived   *bool  `json:"isArchived"`
	Trashed    *bool  `json:"isTrashed"`
	Offline    *bool  `json:"isOffline"`
	Checksum   string `json:"checksum"`
	UpdatedAt  string `json:"updatedAt"`
	IsEdited   *bool  `json:"isEdited"`
}

func readJSON(resp *http.Response, dst any) error {
	defer resp.Body.Close()
	if resp.ContentLength > immichMaxJSON {
		return errImmichTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, immichMaxJSON+1))
	if err != nil || len(data) > immichMaxJSON {
		return errImmichTooLarge
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(dst); err != nil {
		return errImmichResponse
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errImmichResponse
	}
	return nil
}

// List verifies the configured album and walks every page. The published
// allowlist changes only after a complete, validated listing.
func (s *ImmichSource) List(ctx context.Context) ([]Asset, error) {
	assets, err := s.list(ctx)
	if err != nil {
		s.clearAllowlist()
		return nil, err
	}
	allowed := make(map[string]string, len(assets))
	for _, a := range assets {
		allowed[a.ID] = a.Revision
	}
	s.mu.Lock()
	s.allowlist = allowed
	s.mu.Unlock()
	return assets, nil
}

func (s *ImmichSource) list(ctx context.Context) ([]Asset, error) {
	resp, err := s.request(ctx, http.MethodGet, "/api/albums/"+s.albumID, nil)
	if err != nil {
		return nil, err
	}
	var album immichAlbum
	if err := readJSON(resp, &album); err != nil || album.ID != s.albumID {
		return nil, errImmichResponse
	}

	filter, err := json.Marshal(map[string]any{
		"albumIds":   map[string]any{"any": []string{s.albumID}},
		"type":       map[string]string{"eq": "IMAGE"},
		"visibility": map[string]string{"eq": "timeline"},
		"isOffline":  map[string]bool{"eq": false},
	})
	if err != nil {
		return nil, errImmichRequest
	}
	var assets []Asset
	seenIDs := make(map[string]struct{})
	seenCursors := make(map[string]struct{})
	var cursor *string
	considered := 0
	pages := 0
	for {
		pages++
		if pages > 26 {
			return nil, errors.New("Immich pagination page limit exceeded")
		}
		query := map[string]any{
			"size":   immichPageSize,
			"filter": json.RawMessage(filter),
		}
		if cursor != nil {
			query["cursor"] = *cursor
		}
		body, err := json.Marshal(query)
		if err != nil {
			return nil, errImmichRequest
		}
		resp, err := s.request(ctx, http.MethodPost, "/api/search/metadata", body)
		if err != nil {
			return nil, err
		}
		var page immichSearchResponse
		if err := readJSON(resp, &page); err != nil {
			return nil, err
		}
		if page.Assets.Items == nil || len(page.Assets.NextCursor) == 0 {
			return nil, errImmichResponse
		}
		considered += len(*page.Assets.Items)
		if considered > immichMaxAssets {
			return nil, errors.New("Immich album exceeds asset limit")
		}
		for _, item := range *page.Assets.Items {
			if !validUUID(item.ID) || item.Type == "" || item.Visibility == "" || item.Archived == nil || item.Trashed == nil || item.Offline == nil || item.Checksum == "" || item.UpdatedAt == "" || item.IsEdited == nil {
				return nil, errImmichResponse
			}
			if _, err := time.Parse(time.RFC3339Nano, item.UpdatedAt); err != nil {
				return nil, errImmichResponse
			}
			if item.Type != "IMAGE" || item.Visibility != "timeline" || *item.Archived || *item.Trashed || *item.Offline {
				continue
			}
			if _, ok := seenIDs[item.ID]; ok {
				continue
			}
			seenIDs[item.ID] = struct{}{}
			assets = append(assets, Asset{ID: item.ID, Revision: item.Checksum + ":" + item.UpdatedAt + ":" + fmt.Sprint(*item.IsEdited)})
		}
		if bytes.Equal(bytes.TrimSpace(page.Assets.NextCursor), []byte("null")) {
			break
		}
		var nextCursor string
		if err := json.Unmarshal(page.Assets.NextCursor, &nextCursor); err != nil || nextCursor == "" {
			return nil, errImmichResponse
		}
		if _, ok := seenCursors[nextCursor]; ok {
			return nil, errors.New("Immich pagination cursor cycle")
		}
		if pages == 26 {
			return nil, errors.New("Immich pagination page limit exceeded")
		}
		seenCursors[nextCursor] = struct{}{}
		cursor = &nextCursor
	}
	return assets, nil
}

// Open fetches a preview only for an asset from the latest successful List.
func (s *ImmichSource) Open(ctx context.Context, asset Asset) (io.ReadCloser, error) {
	s.mu.RLock()
	revision, ok := s.allowlist[asset.ID]
	s.mu.RUnlock()
	if !ok || !validUUID(asset.ID) || asset.Revision == "" || asset.Revision != revision {
		return nil, errImmichAssetAccess
	}
	path := "/api/assets/" + asset.ID + "/thumbnail?size=preview&edited=true"
	resp, err := s.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if resp.ContentLength > immichMaxPreview {
		resp.Body.Close()
		return nil, errImmichTooLarge
	}
	return &boundedBody{ReadCloser: resp.Body, remaining: immichMaxPreview}, nil
}

type boundedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if b.remaining < 0 {
		return 0, errImmichTooLarge
	}
	if int64(len(p)) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	if b.remaining < 0 {
		return n, errImmichTooLarge
	}
	return n, err
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func validUUIDv4(value string) bool {
	if !validUUID(value) || value[14] != '4' {
		return false
	}
	variant := value[19]
	return variant == '8' || variant == '9' || variant == 'a' || variant == 'A' || variant == 'b' || variant == 'B'
}
