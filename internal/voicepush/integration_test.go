package voicepush

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRealWebSocketSyntheticPair(t *testing.T) {
	h := New("publisher-synthetic", "subscriber-synthetic", "target-a")
	defer h.Close()
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	header := http.Header{"Authorization": []string{"Bearer subscriber-synthetic"}}
	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // isolated synthetic test certificate
	ws, _, err := dialer.Dial("wss"+strings.TrimPrefix(srv.URL, "https")+"/api/voice/v1/events", header)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/api/voice/v1/lease", nil)
	req.Header.Set("Authorization", "Bearer publisher-synthetic")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var lease struct {
		LeaseID string `json:"lease_id"`
	}
	if err = json.NewDecoder(res.Body).Decode(&lease); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	s := Snapshot{V: 1, Type: "voice.snapshot", LeaseID: lease.LeaseID, Revision: 1, SessionID: "s", TurnID: "s:1", Phase: "answer", Playback: "unknown", Text: "synthetic", Active: true, TTLMS: 3000}
	b, _ := json.Marshal(s)
	req, _ = http.NewRequest("POST", srv.URL+"/api/voice/v1/publish", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer publisher-synthetic")
	res, err = srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatalf("publish %d", res.StatusCode)
	}
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	var got struct {
		Snapshot
		Replay bool `json:"replay"`
	}
	if err = ws.ReadJSON(&got); err != nil {
		t.Fatal(err)
	}
	if got.Replay || got.Text != "synthetic" || got.Revision != 1 || got.TTLMS <= 0 || got.TTLMS > 3000 {
		t.Fatalf("snapshot %#v", got)
	}
	// A newly connected subscriber receives the retained snapshot marked replay.
	second, _, err := dialer.Dial("wss"+strings.TrimPrefix(srv.URL, "https")+"/api/voice/v1/events", header)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_ = second.SetReadDeadline(time.Now().Add(time.Second))
	var replay struct {
		Snapshot
		Replay bool `json:"replay"`
	}
	if err = second.ReadJSON(&replay); err != nil {
		t.Fatal(err)
	}
	if !replay.Replay || replay.Revision != got.Revision || replay.TTLMS > got.TTLMS {
		t.Fatalf("bad replay: %#v", replay)
	}
	_ = ws.SetReadDeadline(time.Now().Add(4 * time.Second))
	var clear Clear
	if err = ws.ReadJSON(&clear); err != nil {
		t.Fatal(err)
	}
	if clear.Type != "voice.clear" || clear.Reason != "expired" || clear.Revision != 1 {
		t.Fatalf("clear %#v", clear)
	}
}
