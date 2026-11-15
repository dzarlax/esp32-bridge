package management

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"esp32-bridge/internal/settings"
)

func TestVoiceClearSyncIsScopedAndBounded(t *testing.T) {
	store, err := settings.New(filepath.Join(t.TempDir(), "state"), managementDefaults(), nil, "https://panel.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := strings.Repeat("a", 32)
	b := strings.Repeat("b", 32)
	api := &API{Store: store, DeviceKey: "secret", VoiceClear: func(id string) any {
		if id == a {
			return map[string]string{"device_id": a}
		}
		return nil
	}}
	sync := func(id string) *httptest.ResponseRecorder {
		data, _ := json.Marshal(settings.Device{DeviceID: id, BridgeID: store.BridgeID(), Firmware: "1", BootID: "boot"})
		r := httptest.NewRequest(http.MethodPost, "/api/device/v1/sync", bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-API-Key", "secret")
		r.Header.Set("X-Device-ID", id)
		w := httptest.NewRecorder()
		api.sync(w, r)
		return w
	}
	wa := sync(a)
	wb := sync(b)
	if wa.Code != 200 || wb.Code != 200 {
		t.Fatalf("sync status %d %d", wa.Code, wb.Code)
	}
	if !bytes.Contains(wa.Body.Bytes(), []byte(`"voice_clear"`)) || bytes.Contains(wb.Body.Bytes(), []byte(`"voice_clear"`)) {
		t.Fatal("clear crossed device boundary")
	}
	api.VoiceClear = func(string) any { return strings.Repeat("x", 8192) }
	w := sync(a)
	if w.Code != 200 || w.Body.Len() > 8192 || bytes.Contains(w.Body.Bytes(), []byte(`"voice_clear"`)) {
		t.Fatal("oversized clear was not omitted")
	}
	api.VoiceClear = nil
	if w := sync(a); bytes.Contains(w.Body.Bytes(), []byte(`"voice_clear"`)) {
		t.Fatal("disabled mode changed sync")
	}
}
