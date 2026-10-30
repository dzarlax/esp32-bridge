package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"esp32-bridge/internal/model"
	"esp32-bridge/internal/settings"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

type overlayBlock struct {
	Kind      string `json:"kind"`
	RenderID  string `json:"render_id"`
	SHA256    string `json:"sha256"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	ExpiresAt int64  `json:"expires_at"`
}
type overlayClock struct {
	Enabled     bool   `json:"enabled"`
	Locale      string `json:"locale"`
	Timezone    string `json:"timezone"`
	ClockFormat string `json:"clock_format"`
}
type overlayContext struct {
	Version    int            `json:"version"`
	Revision   string         `json:"revision"`
	ServerTime int64          `json:"server_time"`
	Clock      overlayClock   `json:"clock"`
	Blocks     []overlayBlock `json:"blocks"`
}
type overlayImage struct {
	data  []byte
	added time.Time
}
type overlayImages struct {
	mu     sync.Mutex
	images map[string]overlayImage
}
type overlayState struct {
	gate                            chan struct{}
	screen                          settings.Screen
	photos                          settings.Photos
	weatherEnabled, calendarEnabled bool
	images                          *overlayImages
	lastWeather, lastCalendar       time.Time
	boundDevice                     string
	weather, event                  *overlayBlock
}

func enabled(p *bool) bool { return p == nil || *p }
func (h *Handler) SetPhotoOverlaySettings(s settings.Screen, p settings.Photos, weather, calendar bool) {
	h.overlay = &overlayState{gate: make(chan struct{}, 1), screen: s, photos: p, weatherEnabled: weather, calendarEnabled: calendar, images: &overlayImages{images: map[string]overlayImage{}}}
}
func (h *Handler) SharePhotoOverlayCache(old *Handler) {
	if old != nil && old.overlay != nil && h.overlay != nil {
		h.overlay.images = old.overlay.images
	}
}
func (h *Handler) overlayAuth(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-API-Key") == "" || !h.requireAuth(w, r) {
		if r.Header.Get("X-API-Key") == "" {
			http.Error(w, "unauthorized", 401)
		}
		return false
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", 405)
		return false
	}
	device := r.Header.Get("X-Device-ID")
	if len(device) != 32 || strings.Trim(device, "0123456789abcdef") != "" {
		http.Error(w, "device required", 400)
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	return true
}
func (h *Handler) PhotoContext(w http.ResponseWriter, r *http.Request) {
	if !h.overlayAuth(w, r) {
		return
	}
	if h.overlay == nil {
		http.Error(w, "unavailable", 503)
		return
	}
	s := h.overlay
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-r.Context().Done():
		return
	}
	device := r.Header.Get("X-Device-ID")
	if s.boundDevice != "" && s.boundDevice != device {
		http.NotFound(w, r)
		return
	}
	s.boundDevice = device
	now := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	req := r.Clone(ctx)
	if s.weatherEnabled && enabled(s.photos.ShowWeather) && now.Sub(s.lastWeather) >= 5*time.Minute {
		s.lastWeather = now
		data, sourceAt := h.orch.FetchRecentContext(ctx, "weather", 5*time.Minute)
		var weather model.WeatherData
		if len(data) > 0 && json.Unmarshal(data, &weather) == nil && ctx.Err() == nil {
			b := renderWeather(weather, s.screen)
			s.weather = s.save(r.Header.Get("X-Device-ID"), "weather", b, 236, 200, 752, 36, sourceAt.Add(30*time.Minute))
		}
	}
	if s.calendarEnabled && enabled(s.photos.ShowEvent) && now.Sub(s.lastCalendar) >= time.Minute {
		s.lastCalendar = now
		event, err := h.nextPhotoEvent(req, now)
		if err == nil && ctx.Err() == nil {
			s.event = nil
			if event != nil {
				b := renderEvent(*event, s.screen, now)
				expiry := now.Add(5 * time.Minute)
				if event.start.After(now) && event.start.Before(expiry) {
					expiry = event.start
				}
				if event.end.Before(expiry) {
					expiry = event.end
				}
				s.event = s.save(r.Header.Get("X-Device-ID"), "event", b, 480, 112, 506, 450, expiry)
			}
		}
	}
	if r.Context().Err() != nil {
		return
	}
	now = time.Now()
	result := overlayContext{Version: 1, ServerTime: now.Unix(), Clock: overlayClock{enabled(s.photos.ShowClock), s.screen.Locale, s.screen.Timezone, s.screen.ClockFormat}, Blocks: []overlayBlock{}}
	if s.weatherEnabled && enabled(s.photos.ShowWeather) && s.weather != nil && s.weather.ExpiresAt > now.Unix() {
		result.Blocks = append(result.Blocks, *s.weather)
	}
	if s.calendarEnabled && enabled(s.photos.ShowEvent) && s.event != nil && s.event.ExpiresAt > now.Unix() {
		result.Blocks = append(result.Blocks, *s.event)
	}
	rev, _ := json.Marshal(struct {
		Clock  overlayClock
		Blocks []overlayBlock
	}{result.Clock, result.Blocks})
	sum := sha256.Sum256(rev)
	result.Revision = hex.EncodeToString(sum[:])
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
func (s *overlayState) save(device, kind string, data []byte, width, height, x, y int, expiry time.Time) *overlayBlock {
	sum := sha256.Sum256(data)
	id := sha256.Sum256(append([]byte("photoframe-overlay-v1\x00"+device+"\x00"+kind+"\x00"), data...))
	key := hex.EncodeToString(id[:])
	now := time.Now()
	s.images.mu.Lock()
	defer s.images.mu.Unlock()
	for k, v := range s.images.images {
		if now.Sub(v.added) > 35*time.Minute {
			delete(s.images.images, k)
		}
	}
	for len(s.images.images) >= 16 {
		var oldest string
		var at time.Time
		for k, v := range s.images.images {
			if (s.weather != nil && k == s.weather.RenderID) || (s.event != nil && k == s.event.RenderID) {
				continue
			}
			if oldest == "" || v.added.Before(at) {
				oldest = k
				at = v.added
			}
		}
		delete(s.images.images, oldest)
	}
	s.images.images[key] = overlayImage{data, now}
	return &overlayBlock{kind, key, hex.EncodeToString(sum[:]), x, y, width, height, expiry.Unix()}
}
func (h *Handler) PhotoOverlay(w http.ResponseWriter, r *http.Request) {
	if !h.overlayAuth(w, r) {
		return
	}
	if h.overlay == nil {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/photo-frame/v1/overlays/")
	if len(id) != 64 {
		http.NotFound(w, r)
		return
	}
	c := h.overlay.images
	c.mu.Lock()
	entry, ok := c.images[id]
	if ok && time.Since(entry.added) > 35*time.Minute {
		delete(c.images, id)
		ok = false
	}
	c.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	device := r.Header.Get("X-Device-ID")
	valid := false
	for _, kind := range []string{"weather", "event"} {
		v := sha256.Sum256(append([]byte("photoframe-overlay-v1\x00"+device+"\x00"+kind+"\x00"), entry.data...))
		if hex.EncodeToString(v[:]) == id {
			valid = true
			break
		}
	}
	if !valid {
		http.NotFound(w, r)
		return
	}
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second))
	defer controller.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(entry.data)))
	_, _ = w.Write(entry.data)
}

type photoEvent struct {
	title, calendar, id string
	start, end          time.Time
}

func (h *Handler) nextPhotoEvent(r *http.Request, now time.Time) (*photoEvent, error) {
	end := now.Add(24 * time.Hour)
	events := []photoEvent{}
	if h.calendarPlatformEnabled() {
		items, err := h.fetchCalendarPlatformRaw(r, now, end, true)
		if err != nil {
			return nil, err
		}
		allowed := map[string]bool{}
		for _, id := range h.calendarPlatformIDs {
			allowed[id] = true
		}
		for _, v := range items {
			if !allowed[v.CalendarID] || strings.EqualFold(v.Status, "cancelled") || v.Start.Date != "" || v.End.Date != "" {
				continue
			}
			a, e1 := time.Parse(time.RFC3339, v.Start.DateTime)
			b, e2 := time.Parse(time.RFC3339, v.End.DateTime)
			if e1 == nil && e2 == nil {
				events = append(events, photoEvent{model.SanitizeForDisplay(strings.TrimSpace(v.Title)), v.CalendarID, v.ID, a, b})
			}
		}
	} else {
		if h.haBaseURL == "" || h.haToken == "" {
			return nil, fmt.Errorf("calendar unavailable")
		}
		calendars, err := h.fetchCalendarList(r)
		if err != nil {
			return nil, err
		}
		loc := h.calendarLocation
		if loc == nil {
			loc = time.Local
		}
		for _, cal := range calendars {
			raw, err := h.fetchCalendarHARaw(r, cal, now.In(loc).Format(time.RFC3339), end.In(loc).Format(time.RFC3339))
			if err != nil {
				return nil, err
			}
			for _, v := range raw {
				if v.Start.Date != "" {
					continue
				}
				a, e1 := parsePhotoTime(v.Start.DateTime, loc)
				b, e2 := parsePhotoTime(v.End.DateTime, loc)
				if e1 == nil && e2 == nil {
					events = append(events, photoEvent{model.SanitizeForDisplay(strings.TrimSpace(v.Summary)), cal, v.ID, a, b})
				}
			}
		}
	}
	filtered := events[:0]
	for _, e := range events {
		if e.title != "" && e.end.After(e.start) && e.end.After(now) && e.start.Before(end) {
			filtered = append(filtered, e)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		a, b := filtered[i], filtered[j]
		ao, bo := !a.start.After(now), !b.start.After(now)
		if ao != bo {
			return ao
		}
		if !a.start.Equal(b.start) {
			return a.start.Before(b.start)
		}
		if a.calendar != b.calendar {
			return a.calendar < b.calendar
		}
		if a.id != b.id {
			return a.id < b.id
		}
		return a.title < b.title
	})
	if len(filtered) == 0 {
		return nil, nil
	}
	return &filtered[0], nil
}
func parsePhotoTime(v string, loc *time.Location) (time.Time, error) {
	t, e := time.Parse(time.RFC3339, v)
	if e == nil {
		return t, nil
	}
	return time.ParseInLocation("2006-01-02T15:04:05", v, loc)
}

// Go Regular is bundled with golang.org/x/image under the Go project BSD license.
var overlayFont, _ = opentype.Parse(goregular.TTF)

func face(size float64) font.Face {
	f, _ := opentype.NewFace(overlayFont, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	return f
}
func textLine(img *image.RGBA, x, y int, text string, size float64, c color.Color, max int) {
	x *= 2
	y *= 2
	max *= 2
	f := face(size * 2)
	defer f.Close()
	d := font.Drawer{Dst: img, Src: image.NewUniform(c), Face: f, Dot: fixed.P(x, y)}
	if max > 0 {
		r := []rune(text)
		for len(r) > 0 && d.MeasureString(string(r)).Ceil() > max {
			r = r[:len(r)-1]
		}
		if len(r) < len([]rune(text)) {
			for len(r) > 0 && d.MeasureString(string(r)+"…").Ceil() > max {
				r = r[:len(r)-1]
			}
			text = string(r) + "…"
		}
	}
	d.DrawString(text)
}
func circle(img *image.RGBA, cx, cy, r int, c color.Color) {
	cx *= 2
	cy *= 2
	r *= 2
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r {
				img.Set(x, y, c)
			}
		}
	}
}
func pixels(img *image.RGBA) []byte {
	small := image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx()/2, img.Bounds().Dy()/2))
	xdraw.CatmullRom.Scale(small, small.Bounds(), img, img.Bounds(), xdraw.Src, nil)
	img = small
	b := make([]byte, img.Bounds().Dx()*img.Bounds().Dy()*2)
	i := 0
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			v := uint16(r>>11)<<11 | uint16(g>>10)<<5 | uint16(bl>>11)
			b[i] = byte(v)
			b[i+1] = byte(v >> 8)
			i += 2
		}
	}
	return b
}
func card(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w*2, h*2))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{20, 29, 42, 255}), image.Point{}, draw.Src)
	return img
}
func weatherDescription(code int, day bool, loc string) string {
	idx := 0
	switch {
	case code == 0:
		idx = 0
	case code <= 3:
		idx = 1
	case code <= 48:
		idx = 2
	case code <= 67 || code >= 80 && code <= 82:
		idx = 3
	case code <= 77 || code == 85 || code == 86:
		idx = 4
	default:
		idx = 5
	}
	descriptions := map[string][]string{"ru": {"Ясно", "Облачно", "Туман", "Дождь", "Снег", "Гроза"}, "en": {"Clear", "Cloudy", "Fog", "Rain", "Snow", "Thunderstorm"}, "sr-Latn": {"Vedro", "Oblačno", "Magla", "Kiša", "Sneg", "Oluja"}}
	a, ok := descriptions[loc]
	if !ok {
		a = descriptions["en"]
	}
	if idx == 1 && !day {
		switch loc {
		case "ru":
			return "Облачная ночь"
		case "sr-Latn":
			return "Oblačna noć"
		default:
			return "Cloudy night"
		}
	}
	if idx == 0 && !day {
		switch loc {
		case "ru":
			return "Ясная ночь"
		case "sr-Latn":
			return "Vedra noć"
		default:
			return "Clear night"
		}
	}
	return a[idx]
}
func renderWeather(w model.WeatherData, s settings.Screen) []byte {
	img := card(236, 200)
	white := color.RGBA{242, 246, 252, 255}
	muted := color.RGBA{190, 205, 220, 255}
	if w.IsDay {
		circle(img, 166, 43, 23, color.RGBA{255, 211, 113, 255})
	} else {
		circle(img, 166, 43, 22, color.RGBA{217, 230, 247, 255})
		circle(img, 176, 34, 20, color.RGBA{20, 29, 42, 255})
	}
	if w.WeatherCode != 0 {
		cloud := color.RGBA{226, 235, 248, 255}
		circle(img, 143, 65, 17, cloud)
		circle(img, 165, 57, 23, cloud)
		circle(img, 188, 66, 16, cloud)
		draw.Draw(img, image.Rect(264, 130, 404, 160), image.NewUniform(cloud), image.Point{}, draw.Src)
		code := w.WeatherCode
		switch {
		case code >= 95:
			for i := 0; i < 14; i++ {
				x := 170 - i/2
				if i > 7 {
					x = 163 + (i-7)/2
				}
				draw.Draw(img, image.Rect(x*2, (79+i)*2, (x+4)*2, (80+i)*2), image.NewUniform(color.RGBA{255, 211, 113, 255}), image.Point{}, draw.Src)
			}
		case code >= 71 && code <= 77 || code == 85 || code == 86:
			for x := 145; x <= 185; x += 20 {
				circle(img, x, 89, 3, color.RGBA{235, 245, 255, 255})
				circle(img, x-4, 89, 1, color.RGBA{235, 245, 255, 255})
				circle(img, x+4, 89, 1, color.RGBA{235, 245, 255, 255})
				circle(img, x, 85, 1, color.RGBA{235, 245, 255, 255})
				circle(img, x, 93, 1, color.RGBA{235, 245, 255, 255})
			}
		case code >= 51 && code <= 67 || code >= 80 && code <= 82:
			for x := 145; x <= 185; x += 20 {
				circle(img, x, 89, 3, color.RGBA{125, 191, 244, 255})
			}
		case code == 45 || code == 48:
			for y := 85; y <= 95; y += 5 {
				draw.Draw(img, image.Rect(276, y*2, 394, (y+2)*2), image.NewUniform(color.RGBA{169, 190, 209, 255}), image.Point{}, draw.Src)
			}
		}
	}
	textLine(img, 18, 118, fmt.Sprintf("%.0f°", math.Round(w.Temp)), 40, white, 200)
	textLine(img, 19, 149, s.City, 18, white, 198)
	textLine(img, 19, 177, weatherDescription(w.WeatherCode, w.IsDay, s.Locale), 16, muted, 198)
	return pixels(img)
}
func renderEvent(e photoEvent, s settings.Screen, now time.Time) []byte {
	img := card(480, 112)
	white := color.RGBA{242, 246, 252, 255}
	muted := color.RGBA{185, 205, 225, 255}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		loc = time.UTC
	}
	label := "NEXT"
	if !e.start.After(now) {
		label = "NOW"
	}
	if s.Locale == "ru" {
		label = "ДАЛЕЕ"
		if !e.start.After(now) {
			label = "СЕЙЧАС"
		}
	} else if s.Locale == "sr-Latn" {
		label = "SLEDEĆE"
		if !e.start.After(now) {
			label = "SADA"
		}
	}
	format := "15:04"
	if s.ClockFormat == "12h" {
		format = "3:04 PM"
	}
	if e.start.After(now) {
		day := "TODAY"
		if e.start.In(loc).Format("2006-01-02") != now.In(loc).Format("2006-01-02") {
			day = "TOMORROW"
		}
		if s.Locale == "ru" {
			if day == "TODAY" {
				day = "СЕГОДНЯ"
			} else {
				day = "ЗАВТРА"
			}
		} else if s.Locale == "sr-Latn" {
			if day == "TODAY" {
				day = "DANAS"
			} else {
				day = "SUTRA"
			}
		}
		label += " · " + day + " " + e.start.In(loc).Format(format)
	}
	textLine(img, 20, 29, label, 16, muted, 440)
	textLine(img, 20, 70, e.title, 27, white, 440)
	return pixels(img)
}
