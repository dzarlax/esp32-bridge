package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"esp32-bridge/internal/model"
)

// ClimateFetcher reads only the configured Home Assistant climate entities.
// Keeping the list explicit is important because dashboard clients can invoke
// actions only for entities that Bridge itself exposes.
type ClimateFetcher struct {
	baseURL  string
	token    string
	climates []string
	client   *http.Client
	ttl      time.Duration
}

func NewClimate(baseURL, token string, climates []string, client *http.Client, ttl time.Duration) *ClimateFetcher {
	return &ClimateFetcher{baseURL: baseURL, token: token, climates: climates, client: client, ttl: ttl}
}

func (f *ClimateFetcher) Name() string       { return "climate" }
func (f *ClimateFetcher) TTL() time.Duration { return f.ttl }

func (f *ClimateFetcher) Fetch(ctx context.Context) (json.RawMessage, error) {
	type result struct {
		idx  int
		item model.ClimateItem
		err  error
	}

	ch := make(chan result, len(f.climates))
	var wg sync.WaitGroup
	for i, entityID := range f.climates {
		wg.Add(1)
		go func(idx int, entity string) {
			defer wg.Done()
			item, err := f.fetchClimate(ctx, entity)
			ch <- result{idx: idx, item: item, err: err}
		}(i, entityID)
	}
	go func() {
		wg.Wait()
		close(ch)
	}()

	items := make([]model.ClimateItem, len(f.climates))
	valid := make([]bool, len(f.climates))
	for r := range ch {
		if r.err != nil {
			log.Printf("[climate] %s: %v", f.climates[r.idx], r.err)
			continue
		}
		items[r.idx] = r.item
		valid[r.idx] = true
	}

	out := make([]model.ClimateItem, 0, len(f.climates))
	for i, ok := range valid {
		if ok {
			out = append(out, items[i])
		}
	}
	return json.Marshal(out)
}

func (f *ClimateFetcher) fetchClimate(ctx context.Context, entityID string) (model.ClimateItem, error) {
	url := fmt.Sprintf("%s/api/states/%s", f.baseURL, entityID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return model.ClimateItem{}, err
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
	resp, err := f.client.Do(req)
	if err != nil {
		return model.ClimateItem{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return model.ClimateItem{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var state struct {
		State      string `json:"state"`
		Attributes struct {
			FriendlyName       string   `json:"friendly_name"`
			CurrentTemperature *float64 `json:"current_temperature"`
			Temperature        *float64 `json:"temperature"`
			HVACModes          []string `json:"hvac_modes"`
		} `json:"attributes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return model.ClimateItem{}, err
	}
	if state.State == "unavailable" || state.State == "unknown" {
		return model.ClimateItem{}, fmt.Errorf("state %s", state.State)
	}

	return model.ClimateItem{
		ID:          entityID,
		Name:        model.SanitizeForDisplay(state.Attributes.FriendlyName),
		Mode:        state.State,
		CurrentTemp: state.Attributes.CurrentTemperature,
		TargetTemp:  state.Attributes.Temperature,
		Modes:       state.Attributes.HVACModes,
	}, nil
}
