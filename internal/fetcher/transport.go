package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"esp32-bridge/internal/model"
)

type TransportFetcher struct {
	baseURL string
	stops   []string
	client  *http.Client
	ttl     time.Duration
}

func NewTransport(baseURL string, stops []string, client *http.Client, ttl time.Duration) *TransportFetcher {
	return &TransportFetcher{baseURL: baseURL, stops: stops, client: client, ttl: ttl}
}

func (f *TransportFetcher) Name() string      { return "transport" }
func (f *TransportFetcher) TTL() time.Duration { return f.ttl }

func (f *TransportFetcher) Fetch(ctx context.Context) (json.RawMessage, error) {
	results := make([]model.TransportStop, len(f.stops))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i, stopID := range f.stops {
		wg.Add(1)
		go func(idx int, id string) {
			defer wg.Done()
			stop, err := f.fetchStop(ctx, id)
			if err != nil {
				return
			}
			mu.Lock()
			results[idx] = stop
			mu.Unlock()
		}(i, stopID)
	}
	wg.Wait()

	return json.Marshal(results)
}

func (f *TransportFetcher) fetchStop(ctx context.Context, stopID string) (model.TransportStop, error) {
	u := fmt.Sprintf("%s/api/stations/bg/search?id=%s", f.baseURL, stopID)
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return model.TransportStop{}, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return model.TransportStop{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return model.TransportStop{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var apiResp struct {
		Vehicles []struct {
			LineNumber      string `json:"lineNumber"`
			SecondsLeft     int    `json:"secondsLeft"`
			StationsBetween int    `json:"stationsBetween"`
		} `json:"vehicles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return model.TransportStop{}, err
	}

	vehicles := make([]model.TransportVehicle, 0, len(apiResp.Vehicles))
	for _, v := range apiResp.Vehicles {
		vehicles = append(vehicles, model.TransportVehicle{
			LineNumber:      v.LineNumber,
			SecondsLeft:     v.SecondsLeft,
			StationsBetween: v.StationsBetween,
		})
	}
	sort.Slice(vehicles, func(i, j int) bool {
		return vehicles[i].SecondsLeft < vehicles[j].SecondsLeft
	})
	if len(vehicles) > 5 {
		vehicles = vehicles[:5]
	}

	return model.TransportStop{Vehicles: vehicles}, nil
}
