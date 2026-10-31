package management

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeviceEndpointsRejectQueryCredentialsWithoutChangingState(t *testing.T) {
	// No store is installed: rejecting before state access is part of the contract.
	a := &API{DeviceKey: "fixture-native-key"}
	for _, endpoint := range []struct {
		method, path string
		serve        http.HandlerFunc
	}{
		{"GET", "/api/device/v1/probe", a.probe}, {"POST", "/api/device/v1/sync", a.sync},
	} {
		for _, tc := range []struct {
			key, query string
			status     int
		}{
			{"", "?key=fixture-native-key", 401},
			{"wrong", "?key=fixture-native-key", 401},
			{"fixture-native-key", "?key=fixture-native-key", 400},
			{"fixture-native-key", "?key=", 400},
			{"fixture-native-key", "?%6bey=fixture-native-key", 400},
		} {
			t.Run(endpoint.path+"/"+tc.key+tc.query, func(t *testing.T) {
				r := httptest.NewRequest(endpoint.method, endpoint.path+tc.query, strings.NewReader(`{"device_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
				if tc.key != "" {
					r.Header.Set("X-API-Key", tc.key)
				}
				r.Header.Set("X-Device-ID", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
				w := httptest.NewRecorder()
				endpoint.serve(w, r)
				if w.Code != tc.status {
					t.Fatalf("HTTP=%d want=%d", w.Code, tc.status)
				}
				if strings.Contains(w.Body.String(), "fixture-native-key") {
					t.Fatal("credential reflected")
				}
				if tc.status == 400 && !strings.Contains(w.Body.String(), "query_key_not_supported") {
					t.Fatal("request passed the query credential boundary")
				}
			})
		}
	}
}
