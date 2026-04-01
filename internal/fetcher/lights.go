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

type LightsFetcher struct {
	baseURL string
	token   string
	lights  []string
	client  *http.Client
	ttl     time.Duration
}

func NewLights(baseURL, token string, lights []string, client *http.Client, ttl time.Duration) *LightsFetcher {
	return &LightsFetcher{baseURL: baseURL, token: token, lights: lights, client: client, ttl: ttl}
}

func (f *LightsFetcher) Name() string      { return "lights" }
func (f *LightsFetcher) TTL() time.Duration { return f.ttl }

func (f *LightsFetcher) Fetch(ctx context.Context) (json.RawMessage, error) {
	type result struct {
		idx  int
		item model.LightItem
		err  error
	}

	ch := make(chan result, len(f.lights))
	var wg sync.WaitGroup

	for i, entityID := range f.lights {
		wg.Add(1)
		go func(idx int, entity string) {
			defer wg.Done()
			item, err := f.fetchLight(ctx, entity)
			ch <- result{idx: idx, item: item, err: err}
		}(i, entityID)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	items := make([]model.LightItem, len(f.lights))
	valid := make([]bool, len(f.lights))
	for r := range ch {
		if r.err != nil {
			log.Printf("[lights] %s: %v", f.lights[r.idx], r.err)
			continue
		}
		items[r.idx] = r.item
		valid[r.idx] = true
	}

	out := make([]model.LightItem, 0, len(f.lights))
	for i, v := range valid {
		if v {
			out = append(out, items[i])
		}
	}

	return json.Marshal(out)
}

func (f *LightsFetcher) fetchLight(ctx context.Context, entityID string) (model.LightItem, error) {
	url := fmt.Sprintf("%s/api/states/%s", f.baseURL, entityID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return model.LightItem{}, err
	}
	req.Header.Set("Authorization", "Bearer "+f.token)

	resp, err := f.client.Do(req)
	if err != nil {
		return model.LightItem{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return model.LightItem{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var state struct {
		EntityID   string `json:"entity_id"`
		State      string `json:"state"`
		Attributes struct {
			FriendlyName string   `json:"friendly_name"`
			Brightness   *float64 `json:"brightness"`
		} `json:"attributes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return model.LightItem{}, err
	}

	br := 0
	if state.Attributes.Brightness != nil {
		br = int(*state.Attributes.Brightness)
	}

	return model.LightItem{
		ID:         entityID,
		Name:       model.SanitizeForDisplay(state.Attributes.FriendlyName),
		On:         state.State == "on",
		Brightness: br,
	}, nil
}
