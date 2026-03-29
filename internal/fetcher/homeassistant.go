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

type HAFetcher struct {
	baseURL string
	token   string
	sensors []string
	client  *http.Client
	ttl     time.Duration
}

func NewHA(baseURL, token string, sensors []string, client *http.Client, ttl time.Duration) *HAFetcher {
	return &HAFetcher{baseURL: baseURL, token: token, sensors: sensors, client: client, ttl: ttl}
}

func (f *HAFetcher) Name() string        { return "sensors" }
func (f *HAFetcher) TTL() time.Duration   { return f.ttl }

func (f *HAFetcher) Fetch(ctx context.Context) (json.RawMessage, error) {
	type result struct {
		idx  int
		item model.SensorItem
		err  error
	}

	ch := make(chan result, len(f.sensors))
	var wg sync.WaitGroup

	for i, entityID := range f.sensors {
		wg.Add(1)
		go func(idx int, entity string) {
			defer wg.Done()
			item, err := f.fetchSensor(ctx, entity)
			ch <- result{idx: idx, item: item, err: err}
		}(i, entityID)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	items := make([]model.SensorItem, len(f.sensors))
	valid := make([]bool, len(f.sensors))
	for r := range ch {
		if r.err != nil {
			log.Printf("[sensors] %s: %v", f.sensors[r.idx], r.err)
			continue
		}
		items[r.idx] = r.item
		valid[r.idx] = true
	}

	out := make([]model.SensorItem, 0, len(f.sensors))
	for i, v := range valid {
		if v {
			out = append(out, items[i])
		}
	}

	return json.Marshal(out)
}

func (f *HAFetcher) fetchSensor(ctx context.Context, entityID string) (model.SensorItem, error) {
	url := fmt.Sprintf("%s/api/states/%s", f.baseURL, entityID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return model.SensorItem{}, err
	}
	req.Header.Set("Authorization", "Bearer "+f.token)

	resp, err := f.client.Do(req)
	if err != nil {
		return model.SensorItem{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return model.SensorItem{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var state struct {
		State      string `json:"state"`
		Attributes struct {
			FriendlyName      string `json:"friendly_name"`
			UnitOfMeasurement string `json:"unit_of_measurement"`
		} `json:"attributes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return model.SensorItem{}, err
	}

	return model.SensorItem{
		Name:  state.Attributes.FriendlyName,
		Value: state.State,
		Unit:  state.Attributes.UnitOfMeasurement,
	}, nil
}
