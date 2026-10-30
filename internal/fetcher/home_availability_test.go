package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"esp32-bridge/internal/model"
)

func TestHomeProjectionOmitsUnavailableEntities(t *testing.T) {
	states := map[string]string{"light.on": "on", "light.missing": "unavailable", "light.unknown": "unknown", "light.off": "off", "light.invalid": "bogus", "climate.heat": "heat", "climate.missing": "unavailable", "climate.unknown": "unknown"}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[len("/api/states/"):]
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing auth")
		}
		_, _ = fmt.Fprintf(w, `{"entity_id":%q,"state":%q,"attributes":{"friendly_name":%q}}`, id, states[id], id)
	}))
	defer upstream.Close()
	lights := NewLights(upstream.URL, "test-token", []string{"light.on", "light.missing", "light.unknown", "light.off", "light.invalid"}, upstream.Client(), time.Second)
	data, err := lights.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var gotLights []model.LightItem
	if err = json.Unmarshal(data, &gotLights); err != nil {
		t.Fatal(err)
	}
	if len(gotLights) != 2 || gotLights[0].ID != "light.on" || !gotLights[0].On || gotLights[1].ID != "light.off" || gotLights[1].On {
		t.Fatalf("lights: %#v", gotLights)
	}
	climate := NewClimate(upstream.URL, "test-token", []string{"climate.missing", "climate.heat", "climate.unknown"}, upstream.Client(), time.Second)
	data, err = climate.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var gotClimate []model.ClimateItem
	if err = json.Unmarshal(data, &gotClimate); err != nil {
		t.Fatal(err)
	}
	if len(gotClimate) != 1 || gotClimate[0].ID != "climate.heat" {
		t.Fatalf("climate: %#v", gotClimate)
	}
	// Missing entities are absent from shared room data and favorite lookup data.
	ids := make([]string, 0, len(gotLights))
	for _, item := range gotLights {
		ids = append(ids, item.ID)
	}
	if !reflect.DeepEqual(ids, []string{"light.on", "light.off"}) {
		t.Fatalf("unexpected IDs: %v", ids)
	}
}
