package photoframe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

const testAlbumID = "123e4567-e89b-42d3-a456-426614174000"
const testAssetID = "123e4567-e89b-42d3-a456-426614174001"
const testAssetID2 = "123e4567-e89b-42d3-a456-426614174002"
const testAPIKey = "immich-test-key"

type testImmichAsset struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Visibility string `json:"visibility"`
	Archived   bool   `json:"isArchived"`
	Trashed    bool   `json:"isTrashed"`
	Offline    bool   `json:"isOffline"`
	Checksum   string `json:"checksum"`
	UpdatedAt  string `json:"updatedAt"`
	IsEdited   bool   `json:"isEdited"`
}

func eligibleAsset(id string) testImmichAsset {
	return testImmichAsset{ID: id, Type: "IMAGE", Visibility: "timeline", Checksum: "abc123", UpdatedAt: "2026-10-29T12:30:00Z"}
}

func newTestSource(t *testing.T, handler http.HandlerFunc) (*ImmichSource, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	source, err := NewImmich(server.URL, testAlbumID, testAPIKey, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return source, server
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func albumResponse(w http.ResponseWriter) { writeJSON(w, map[string]string{"id": testAlbumID}) }

func searchResponse(w http.ResponseWriter, items []testImmichAsset, cursor any) {
	writeJSON(w, map[string]any{"assets": map[string]any{"items": items, "nextCursor": cursor}})
}

func TestValidateImmichEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint, album string
		valid                 bool
	}{
		{"valid", "https://photos.example", testAlbumID, true},
		{"http", "http://photos.example", testAlbumID, false},
		{"credentials", "https://user:pass@photos.example", testAlbumID, false},
		{"query", "https://photos.example?x=1", testAlbumID, false},
		{"fragment", "https://photos.example/#x", testAlbumID, false},
		{"missing host", "https:///path", testAlbumID, false},
		{"not v4", "https://photos.example", "123e4567-e89b-12d3-a456-426614174000", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateImmichEndpoint(tc.endpoint, tc.album)
			if (err == nil) != tc.valid {
				t.Fatalf("ValidateImmichEndpoint() error = %v, want valid=%v", err, tc.valid)
			}
		})
	}
	if _, err := NewImmich("https://photos.example", testAlbumID, "bad\nkey", nil); err == nil {
		t.Fatal("NewImmich accepted a multiline API key")
	}
	if _, err := NewImmich("https://photos.example", testAlbumID, "bad key", nil); err == nil {
		t.Fatal("NewImmich accepted whitespace in API key")
	}
}

func TestImmichListPaginationFilterDedupAndEligibility(t *testing.T) {
	var calls int
	source, _ := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != testAPIKey {
			t.Errorf("API key header = %q", r.Header.Get("X-API-Key"))
		}
		switch r.URL.Path {
		case "/api/albums/" + testAlbumID:
			if r.Method != http.MethodGet {
				t.Errorf("album method = %s", r.Method)
			}
			albumResponse(w)
		case "/api/search/metadata":
			calls++
			if r.Method != http.MethodPost {
				t.Errorf("search method = %s", r.Method)
			}
			var payload struct {
				Size   int             `json:"size"`
				Cursor string          `json:"cursor"`
				Filter json.RawMessage `json:"filter"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode search body: %v", err)
			}
			var filter map[string]any
			if err := json.Unmarshal(payload.Filter, &filter); err != nil {
				t.Errorf("decode filter: %v", err)
			}
			expectedFilter := map[string]any{
				"albumIds":   map[string]any{"any": []any{testAlbumID}},
				"type":       map[string]any{"eq": "IMAGE"},
				"visibility": map[string]any{"eq": "timeline"},
				"isOffline":  map[string]any{"eq": false},
			}
			if payload.Size != immichPageSize || !reflect.DeepEqual(filter, expectedFilter) {
				t.Errorf("search omitted size or required filter: %+v / %s", payload, payload.Filter)
			}
			if calls == 1 {
				if payload.Cursor != "" {
					t.Errorf("first page cursor = %q", payload.Cursor)
				}
				ineligible := eligibleAsset(testAssetID2)
				ineligible.Type = "VIDEO"
				archived := eligibleAsset("123e4567-e89b-42d3-a456-426614174003")
				archived.Archived = true
				searchResponse(w, []testImmichAsset{eligibleAsset(testAssetID), eligibleAsset(testAssetID), ineligible, archived}, "next")
			} else {
				if payload.Cursor != "next" {
					t.Errorf("second page cursor = %q", payload.Cursor)
				}
				searchResponse(w, []testImmichAsset{eligibleAsset(testAssetID2)}, nil)
			}
		case "/api/assets/" + testAssetID + "/thumbnail":
			if r.URL.Query().Get("size") != "preview" {
				t.Errorf("thumbnail size = %q", r.URL.Query().Get("size"))
			}
			io.WriteString(w, "preview")
		default:
			http.NotFound(w, r)
		}
	})
	assets, err := source.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(assets) != 2 {
		t.Fatalf("List() calls=%d assets=%v", calls, assets)
	}
	opened, err := source.Open(context.Background(), assets[0])
	if err != nil {
		t.Fatal(err)
	}
	opened.Close()
}

func TestImmichListRejectsMalformedAndRevokesAllowlist(t *testing.T) {
	mode := "ok"
	source, _ := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/albums/" + testAlbumID:
			if mode == "wrong-album" {
				writeJSON(w, map[string]string{"id": testAssetID})
			} else {
				albumResponse(w)
			}
		case "/api/search/metadata":
			switch mode {
			case "ok":
				searchResponse(w, []testImmichAsset{eligibleAsset(testAssetID)}, nil)
			case "bad-json":
				io.WriteString(w, `{"assets":`)
			case "auth":
				http.Error(w, "private response", http.StatusForbidden)
			case "wrong-album":
				searchResponse(w, []testImmichAsset{}, nil)
			}
		}
	})
	assets, err := source.List(context.Background())
	if err != nil || len(assets) != 1 {
		t.Fatalf("initial List() = %v, %v", assets, err)
	}
	mode = "bad-json"
	if _, err := source.List(context.Background()); err == nil {
		t.Fatal("List accepted malformed JSON")
	}
	if _, err := source.Open(context.Background(), assets[0]); err == nil {
		t.Fatal("Open retained allowlist after failed List")
	}
	mode = "ok"
	assets, err = source.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mode = "auth"
	if _, err := source.List(context.Background()); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("List auth error = %v", err)
	}
	if _, err := source.Open(context.Background(), assets[0]); err == nil {
		t.Fatal("Open retained allowlist after auth failure")
	}
}

func TestImmichOpenBindsRevisionAndUsesFixedPath(t *testing.T) {
	var requested string
	source, _ := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/albums/"+testAlbumID {
			albumResponse(w)
			return
		}
		if r.URL.Path == "/api/search/metadata" {
			searchResponse(w, []testImmichAsset{eligibleAsset(testAssetID)}, nil)
			return
		}
		requested = r.URL.RequestURI()
		if r.Header.Get("X-API-Key") != testAPIKey {
			t.Errorf("preview missing API key header")
		}
		io.WriteString(w, "preview")
	})
	assets, err := source.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Open(context.Background(), Asset{ID: testAssetID, Revision: "other"}); err == nil {
		t.Fatal("Open accepted stale revision")
	}
	r, err := source.Open(context.Background(), assets[0])
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if requested != "/api/assets/"+testAssetID+"/thumbnail?size=preview&edited=true" {
		t.Fatalf("preview request URI = %q", requested)
	}
}

func TestImmichOpenAuthFailureRevokesAllowlist(t *testing.T) {
	previewStatus := http.StatusOK
	source, _ := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/albums/"+testAlbumID {
			albumResponse(w)
			return
		}
		if r.URL.Path == "/api/search/metadata" {
			searchResponse(w, []testImmichAsset{eligibleAsset(testAssetID)}, nil)
			return
		}
		if previewStatus != http.StatusOK {
			http.Error(w, "private response", previewStatus)
			return
		}
		io.WriteString(w, "preview")
	})
	assets, err := source.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	previewStatus = http.StatusUnauthorized
	if _, err := source.Open(context.Background(), assets[0]); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("Open auth error = %v", err)
	}
	if _, err := source.Open(context.Background(), assets[0]); err == nil {
		t.Fatal("Open retained allowlist after auth failure")
	}
}

func TestImmichRedirectDoesNotForwardAPIKey(t *testing.T) {
	var keySeen bool
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { keySeen = r.Header.Get("X-API-Key") != "" }))
	defer destination.Close()
	source, _ := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/albums/"+testAlbumID {
			albumResponse(w)
			return
		}
		if r.URL.Path == "/api/search/metadata" {
			searchResponse(w, []testImmichAsset{eligibleAsset(testAssetID)}, nil)
			return
		}
		http.Redirect(w, r, destination.URL+"/steal", http.StatusFound)
	})
	assets, err := source.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Open(context.Background(), assets[0]); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("Open redirect error = %v", err)
	}
	if keySeen {
		t.Fatal("API key was forwarded to redirect destination")
	}
}

func TestImmichCapsCursorCyclesAndSanitizesErrors(t *testing.T) {
	t.Run("cursor cycle", func(t *testing.T) {
		source, _ := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/albums/"+testAlbumID {
				albumResponse(w)
				return
			}
			searchResponse(w, []testImmichAsset{}, "again")
		})
		if _, err := source.List(context.Background()); err == nil || !strings.Contains(err.Error(), "cycle") {
			t.Fatalf("List cursor-cycle error = %v", err)
		}
	})
	t.Run("page cap for empty pages", func(t *testing.T) {
		var pages int
		source, _ := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/albums/"+testAlbumID {
				albumResponse(w)
				return
			}
			pages++
			searchResponse(w, []testImmichAsset{}, fmt.Sprintf("cursor-%d", pages))
		})
		if _, err := source.List(context.Background()); err == nil || !strings.Contains(err.Error(), "page limit") {
			t.Fatalf("List empty-page cap error = %v", err)
		}
		if pages != 26 {
			t.Fatalf("pages requested = %d, want 26", pages)
		}
	})
	t.Run("asset cap", func(t *testing.T) {
		source, _ := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/albums/"+testAlbumID {
				albumResponse(w)
				return
			}
			items := make([]testImmichAsset, immichMaxAssets+1)
			for i := range items {
				items[i] = eligibleAsset(fmt.Sprintf("123e4567-e89b-42d3-a456-%012x", i))
			}
			searchResponse(w, items, nil)
		})
		if _, err := source.List(context.Background()); err == nil || !strings.Contains(err.Error(), "asset limit") {
			t.Fatalf("List cap error = %v", err)
		}
	})
	t.Run("json cap", func(t *testing.T) {
		source, _ := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/albums/"+testAlbumID {
				albumResponse(w)
				return
			}
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, strings.Repeat("x", immichMaxJSON+1))
		})
		if _, err := source.List(context.Background()); err == nil || !strings.Contains(err.Error(), "size limit") {
			t.Fatalf("List JSON cap error = %v", err)
		}
	})
	t.Run("sanitized status", func(t *testing.T) {
		source, _ := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "secret body https://internal.example/private", http.StatusBadGateway)
		})
		_, err := source.List(context.Background())
		if err == nil || strings.Contains(err.Error(), "internal.example") || strings.Contains(err.Error(), "secret body") {
			t.Fatalf("unsanitized error: %v", err)
		}
	})
}

func TestImmichOpenCapsBodyAndHonorsCancellation(t *testing.T) {
	source, _ := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/albums/"+testAlbumID {
			albumResponse(w)
			return
		}
		if r.URL.Path == "/api/search/metadata" {
			searchResponse(w, []testImmichAsset{eligibleAsset(testAssetID)}, nil)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(immichMaxPreview+1))
		w.WriteHeader(http.StatusOK)
	})
	assets, err := source.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Open(context.Background(), assets[0]); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("Open oversized body error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.Open(ctx, assets[0]); err == nil {
		t.Fatal("Open ignored canceled context")
	}
}

func TestImmichClientCloneDoesNotMutateCaller(t *testing.T) {
	client := &http.Client{Timeout: 5 * time.Second}
	source, err := NewImmich("https://photos.example", testAlbumID, testAPIKey, client)
	if err != nil {
		t.Fatal(err)
	}
	if client.Timeout != 5*time.Second || client.CheckRedirect != nil {
		t.Fatal("NewImmich mutated the caller's HTTP client")
	}
	if source.client.Timeout != immichTimeout {
		t.Fatalf("cloned timeout = %s", source.client.Timeout)
	}
}

func TestImmichEligibilityAndResponseValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		valid  bool
	}{
		{"trashed", func(m map[string]any) { m["isTrashed"] = true }, true},
		{"offline", func(m map[string]any) { m["isOffline"] = true }, true},
		{"hidden", func(m map[string]any) { m["visibility"] = "hidden" }, true},
		{"locked", func(m map[string]any) { m["visibility"] = "locked" }, true},
		{"missing trash flag", func(m map[string]any) { delete(m, "isTrashed") }, false},
		{"invalid ID", func(m map[string]any) { m["id"] = "../other-album" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, _ := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/albums/"+testAlbumID {
					albumResponse(w)
					return
				}
				encoded, _ := json.Marshal(eligibleAsset(testAssetID))
				var item map[string]any
				json.Unmarshal(encoded, &item)
				tc.mutate(item)
				writeJSON(w, map[string]any{"assets": map[string]any{"items": []any{item}, "nextCursor": nil}})
			})
			got, err := source.List(context.Background())
			if (err == nil) != tc.valid || len(got) != 0 {
				t.Fatalf("eligibility result: %d assets / %v", len(got), err)
			}
		})
	}
	t.Run("trailing JSON", func(t *testing.T) {
		source, _ := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
			albumResponse(w)
			io.WriteString(w, `{"private":"extra"}`)
		})
		if _, err := source.List(context.Background()); err == nil {
			t.Fatal("accepted trailing JSON")
		}
	})
}

func TestImmichUnknownLengthPreviewLimit(t *testing.T) {
	body := &boundedBody{ReadCloser: io.NopCloser(strings.NewReader(strings.Repeat("x", immichMaxPreview+1))), remaining: immichMaxPreview}
	if _, err := io.Copy(io.Discard, body); !errors.Is(err, errImmichTooLarge) {
		t.Fatalf("unbounded preview: %v", err)
	}
}
