package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"esp32-bridge/internal/model"
)

const maxTasks = 8

type TodoistFetcher struct {
	token  string
	client *http.Client
	ttl    time.Duration
}

func NewTodoist(token string, client *http.Client, ttl time.Duration) *TodoistFetcher {
	return &TodoistFetcher{token: token, client: client, ttl: ttl}
}

func (f *TodoistFetcher) Name() string        { return "tasks" }
func (f *TodoistFetcher) TTL() time.Duration   { return f.ttl }

func (f *TodoistFetcher) Fetch(ctx context.Context) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://api.todoist.com/api/v1/tasks/filter?query=today%20%7C%20overdue", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+f.token)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var apiResp struct {
		Results []struct {
			Content  string `json:"content"`
			Priority int    `json:"priority"`
			Due      *struct {
				Date string `json:"date"`
			} `json:"due"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, err
	}

	sort.Slice(apiResp.Results, func(i, j int) bool {
		return apiResp.Results[i].Priority > apiResp.Results[j].Priority
	})

	limit := len(apiResp.Results)
	if limit > maxTasks {
		limit = maxTasks
	}

	items := make([]model.TaskItem, limit)
	for i := 0; i < limit; i++ {
		t := apiResp.Results[i]
		items[i] = model.TaskItem{
			Title:    model.SanitizeForDisplay(t.Content),
			Priority: t.Priority,
		}
		if t.Due != nil {
			items[i].Due = t.Due.Date
		}
	}

	return json.Marshal(items)
}
