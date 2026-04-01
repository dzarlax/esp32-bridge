package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"esp32-bridge/internal/model"
)

type NewsFetcher struct {
	baseURL    string
	limit      int
	sinceHours int
	client     *http.Client
	ttl        time.Duration
}

func NewNews(baseURL string, limit, sinceHours int, client *http.Client, ttl time.Duration) *NewsFetcher {
	return &NewsFetcher{baseURL: baseURL, limit: limit, sinceHours: sinceHours, client: client, ttl: ttl}
}

func (f *NewsFetcher) Name() string        { return "news" }
func (f *NewsFetcher) TTL() time.Duration   { return f.ttl }

func (f *NewsFetcher) Fetch(ctx context.Context) (json.RawMessage, error) {
	url := fmt.Sprintf("%s/api/v1/feed?limit=%d&since_hours=%d", f.baseURL, f.limit, f.sinceHours)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var apiResp struct {
		Items []struct {
			Title       string `json:"title"`
			Category    string `json:"category"`
			PublishedAt string `json:"published_at"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, err
	}

	now := time.Now()
	items := make([]model.NewsItem, 0, len(apiResp.Items))
	for _, a := range apiResp.Items {
		hoursAgo := 0
		if t, err := time.Parse(time.RFC3339, a.PublishedAt); err == nil {
			hoursAgo = int(math.Round(now.Sub(t).Hours()))
		}
		items = append(items, model.NewsItem{
			Title:    model.SanitizeForDisplay(a.Title),
			Category: model.SanitizeForDisplay(a.Category),
			HoursAgo: hoursAgo,
		})
	}

	return json.Marshal(items)
}
