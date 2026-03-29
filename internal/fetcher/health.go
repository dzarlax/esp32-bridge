package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"esp32-bridge/internal/model"
)

type HealthFetcher struct {
	baseURL string
	apiKey  string
	client  *http.Client
	ttl     time.Duration
}

func NewHealth(baseURL, apiKey string, client *http.Client, ttl time.Duration) *HealthFetcher {
	return &HealthFetcher{baseURL: baseURL, apiKey: apiKey, client: client, ttl: ttl}
}

func (f *HealthFetcher) Name() string        { return "health" }
func (f *HealthFetcher) TTL() time.Duration   { return f.ttl }

func (f *HealthFetcher) Fetch(ctx context.Context) (json.RawMessage, error) {
	type dashCard struct {
		Metric string  `json:"metric"`
		Value  float64 `json:"value"`
		Prev   float64 `json:"prev"`
	}
	type dashResp struct {
		Cards []dashCard `json:"cards"`
	}
	type readinessEntry struct {
		Score int `json:"score"`
	}
	type readinessResp struct {
		Points []readinessEntry `json:"points"`
	}

	var (
		dash      dashResp
		readiness readinessResp
		dashErr   error
		readErr   error
	)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		dashErr = f.getJSON(ctx, "/api/dashboard", &dash)
	}()
	go func() {
		defer wg.Done()
		readErr = f.getJSON(ctx, "/api/readiness-history?days=1", &readiness)
	}()
	wg.Wait()

	if dashErr != nil {
		return nil, fmt.Errorf("dashboard: %w", dashErr)
	}

	result := model.HealthData{}
	for _, c := range dash.Cards {
		switch c.Metric {
		case "step_count":
			result.Steps = int(c.Value)
			result.StepsPrev = int(c.Prev)
		case "active_energy":
			result.Cal = int(c.Value)
			result.CalPrev = int(c.Prev)
		case "sleep_total":
			result.Sleep = math.Round(c.Value*10) / 10
			result.SleepPrev = math.Round(c.Prev*10) / 10
		case "heart_rate":
			result.HR = int(c.Value)
		case "resting_heart_rate":
			result.RHR = int(c.Value)
		case "heart_rate_variability":
			result.HRV = int(c.Value)
		case "blood_oxygen_saturation":
			result.SpO2 = int(c.Value)
		}
	}

	if readErr == nil && len(readiness.Points) > 0 {
		result.Readiness = readiness.Points[0].Score
	}

	return json.Marshal(result)
}

func (f *HealthFetcher) getJSON(ctx context.Context, path string, target interface{}) error {
	req, err := http.NewRequestWithContext(ctx, "GET", f.baseURL+path, nil)
	if err != nil {
		return err
	}
	if f.apiKey != "" {
		req.Header.Set("X-API-Key", f.apiKey)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}
