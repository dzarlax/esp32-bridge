package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func safeCalendarTimestamp(value *string) *string {
	if value == nil {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*value))
	if err != nil {
		return nil
	}
	formatted := parsed.UTC().Format(time.RFC3339)
	return &formatted
}

func safeCalendarErrorCode(code string) string {
	switch code {
	case "transient", "rate_limited", "auth", "permission", "unsupported", "protocol":
		return code
	default:
		return ""
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

// CalendarDetails describes only requested sources, never event content.
type CalendarDetails struct {
	Date     string           `json:"date"`
	Complete bool             `json:"complete"`
	Reason   string           `json:"reason"`
	Sources  []CalendarSource `json:"sources"`
}
type CalendarSource struct {
	ID               string   `json:"id"`
	State            string   `json:"state"`
	Stale            *bool    `json:"stale,omitempty"`
	LastSuccessAt    *string  `json:"last_success_at,omitempty"`
	NextSyncAt       *string  `json:"next_sync_at,omitempty"`
	CoverageComplete *bool    `json:"coverage_complete,omitempty"`
	ErrorCode        string   `json:"error_code,omitempty"`
	Reason           string   `json:"reason"`
	Reasons          []string `json:"reasons,omitempty"`
}

func (h *Handler) CalendarStatus(ctx context.Context) (CalendarDetails, error) {
	d := CalendarDetails{Sources: []CalendarSource{}}
	if !h.calendarPlatformEnabled() {
		d.Reason = "disabled"
		return d, nil
	}
	location := h.calendarLocation
	if location == nil {
		location = time.UTC
	}
	now := time.Now().In(location)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	d.Date = start.Format("2006-01-02")
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://bridge.invalid/", nil)
	if err != nil {
		return d, err
	}
	_, err = h.fetchCalendarPlatformRawDetailed(r, start, start.AddDate(0, 0, 1), false, &d)
	if err != nil {
		return d, fmt.Errorf("calendar status unavailable")
	}
	return d, nil
}
