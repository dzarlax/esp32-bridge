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

func (f *HealthFetcher) Name() string       { return "health" }
func (f *HealthFetcher) TTL() time.Duration { return f.ttl }

func (f *HealthFetcher) Fetch(ctx context.Context) (json.RawMessage, error) {
	type dashCard struct {
		Metric string   `json:"metric"`
		Value  *float64 `json:"value"`
		Prev   *float64 `json:"prev"`
	}
	type dashResp struct {
		Cards []dashCard `json:"cards"`
	}
	type readinessEntry struct {
		Score *int `json:"score"`
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

	result := model.HealthData{Present: map[string]bool{}}
	for _, c := range dash.Cards {
		if c.Value == nil || math.IsNaN(*c.Value) || math.IsInf(*c.Value, 0) || *c.Value < 0 || *c.Value > math.MaxInt32 {
			continue
		}
		value := *c.Value
		previous := 0.0
		hasPrev := c.Prev != nil && !math.IsNaN(*c.Prev) && !math.IsInf(*c.Prev, 0) && *c.Prev >= 0 && *c.Prev <= math.MaxInt32
		if hasPrev {
			previous = *c.Prev
		}
		switch c.Metric {
		case "step_count":
			result.Present["steps"] = true
			result.Present["steps_prev"] = hasPrev
			result.Steps = int(value)
			result.StepsPrev = int(previous)
		case "active_energy":
			result.Present["cal"] = true
			result.Present["cal_prev"] = hasPrev
			result.Cal = int(value)
			result.CalPrev = int(previous)
		case "sleep_total":
			result.Present["sleep"] = true
			result.Present["sleep_prev"] = hasPrev
			result.Sleep = math.Round(value*10) / 10
			result.SleepPrev = math.Round(previous*10) / 10
		case "heart_rate":
			result.Present["hr"] = value > 0
			result.Present["hr_prev"] = hasPrev
			result.HR = int(value)
		case "resting_heart_rate":
			result.Present["rhr"] = value > 0
			result.Present["rhr_prev"] = hasPrev
			result.RHR = int(value)
		case "heart_rate_variability":
			result.Present["hrv"] = value > 0
			result.Present["hrv_prev"] = hasPrev
			result.HRV = int(value)
		case "blood_oxygen_saturation":
			result.Present["spo2"] = value > 0
			result.Present["spo2_prev"] = hasPrev
			result.SpO2 = int(value)
		}
	}

	if readErr == nil && len(readiness.Points) > 0 && readiness.Points[0].Score != nil && *readiness.Points[0].Score >= 0 && *readiness.Points[0].Score <= 100 {
		result.Present["readiness"] = true
		result.Readiness = *readiness.Points[0].Score
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
