package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// CalendarDetails describes only requested sources, never event content.
type CalendarDetails struct {
	Date     string           `json:"date"`
	Complete bool             `json:"complete"`
	Reason   string           `json:"reason"`
	Sources  []CalendarSource `json:"sources"`
}
type CalendarSource struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Stale  *bool  `json:"stale,omitempty"`
	Reason string `json:"reason"`
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
