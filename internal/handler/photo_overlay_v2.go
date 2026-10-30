package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	xdraw "golang.org/x/image/draw"
	"image"
	"image/color"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Adaptive rendering is deliberately deterministic for a frame and rectangle.
const adaptiveStyle = "adaptive-v2-r2"

type adaptiveClock struct {
	overlayClock
	Tone     string `json:"tone"`
	Backdrop bool   `json:"backdrop"`
}
type adaptiveContext struct {
	Version    int            `json:"version"`
	FrameID    string         `json:"frame_id"`
	Revision   string         `json:"revision"`
	ServerTime int64          `json:"server_time"`
	Clock      adaptiveClock  `json:"clock"`
	Blocks     []overlayBlock `json:"blocks"`
}

func validFrameID(id string) bool { return len(id) == 64 && strings.Trim(id, "0123456789abcdef") == "" }

func (h *Handler) PhotoContextV2(w http.ResponseWriter, r *http.Request) {
	if !h.overlayAuth(w, r) {
		return
	}
	if h.overlay == nil || h.photoFrame == nil {
		http.NotFound(w, r)
		return
	}
	id := r.URL.Query().Get("frame_id")
	if !validFrameID(id) {
		http.NotFound(w, r)
		return
	}
	frame, release, ok := h.photoFrame.Borrow(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	defer release()
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
	h.refreshOverlay(r, s)
	if r.Context().Err() != nil {
		return
	}
	_, verifyRelease, stillValid := h.photoFrame.Borrow(id)
	if !stillValid {
		http.NotFound(w, r)
		return
	}
	verifyRelease()
	now := time.Now()
	clockTone, clockGlass := analyzeTextRegions(frame, "clock", 24, 460)
	result := adaptiveContext{Version: 2, FrameID: id, ServerTime: now.Unix(), Clock: adaptiveClock{overlayClock: overlayClock{enabled(s.photos.ShowClock), s.screen.Locale, s.screen.Timezone, s.screen.ClockFormat}, Tone: clockTone, Backdrop: enabled(s.photos.ShowClock) && clockGlass}, Blocks: []overlayBlock{}}
	add := func(kind string, x, y, width, height int, expiry time.Time, drawArt func(*image.RGBA, bool)) bool {
		tone, glass := analyzeTextRegions(frame, kind, x, y)
		if kind == "clock" && !glass {
			return true
		}
		data := adaptivePatchContext(r.Context(), frame, x, y, width, height, tone, glass, drawArt)
		if r.Context().Err() != nil {
			return false
		}
		b := s.saveAdaptive(device, id, kind, data, width, height, x, y, expiry)
		if b == nil {
			return false
		}
		result.Blocks = append(result.Blocks, *b)
		return true
	}
	if s.weatherEnabled && enabled(s.photos.ShowWeather) && s.weatherData != nil && s.weatherExpiry.After(now) {
		if !add("weather", 752, 36, 236, 200, s.weatherExpiry, func(img *image.RGBA, dark bool) { drawWeather(img, *s.weatherData, s.screen, dark, true) }) {
			if r.Context().Err() == nil {
				http.Error(w, "busy", 503)
			}
			return
		}
	}
	if s.calendarEnabled && enabled(s.photos.ShowEvent) && s.eventData != nil && s.eventExpiry.After(now) {
		if !add("event", 530, 450, 456, 112, s.eventExpiry, func(img *image.RGBA, dark bool) { drawEvent(img, *s.eventData, s.screen, now, dark, true) }) {
			if r.Context().Err() == nil {
				http.Error(w, "busy", 503)
			}
			return
		}
	}
	if result.Clock.Backdrop {
		if !add("clock", 24, 460, 480, 128, now.Add(30*time.Minute), nil) {
			if r.Context().Err() == nil {
				http.Error(w, "busy", 503)
			}
			return
		}
	}
	if r.Context().Err() != nil {
		return
	}
	_, finalRelease, valid := h.photoFrame.Borrow(id)
	if !valid {
		http.NotFound(w, r)
		return
	}
	finalRelease()
	rev, _ := json.Marshal(struct {
		FrameID string
		Clock   adaptiveClock
		Blocks  []overlayBlock
	}{id, result.Clock, result.Blocks})
	sum := sha256.Sum256(rev)
	result.Revision = hex.EncodeToString(sum[:])
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *overlayState) saveAdaptive(device, frame, kind string, data []byte, width, height, x, y int, expiry time.Time) *overlayBlock {
	sum := sha256.Sum256(data)
	id := sha256.Sum256(append([]byte(adaptiveStyle+"\x00"+device+"\x00"+frame+"\x00"+kind+"\x00"), data...))
	key := hex.EncodeToString(id[:])
	now := time.Now()
	c := s.images
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.adaptive == nil {
		c.adaptive = map[string]overlayImage{}
	}
	for k, v := range c.adaptive {
		if now.Sub(v.added) > 35*time.Minute {
			delete(c.adaptive, k)
		}
	}
	if _, ok := c.adaptive[key]; !ok && len(c.adaptive) >= 48 {
		oldest := ""
		var at time.Time
		for k, v := range c.adaptive {
			if now.Sub(v.added) < 2*time.Minute {
				continue
			}
			if oldest == "" || v.added.Before(at) {
				oldest = k
				at = v.added
			}
		}
		if oldest == "" {
			return nil
		}
		delete(c.adaptive, oldest)
	}
	c.adaptive[key] = overlayImage{data: data, added: now, device: device, frame: frame, version: 2}
	return &overlayBlock{kind, key, hex.EncodeToString(sum[:]), x, y, width, height, expiry.Unix()}
}
func (h *Handler) PhotoOverlayV2(w http.ResponseWriter, r *http.Request) {
	if !h.overlayAuth(w, r) {
		return
	}
	if h.overlay == nil || h.photoFrame == nil {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/photo-frame/v2/overlays/")
	if !validFrameID(id) {
		http.NotFound(w, r)
		return
	}
	c := h.overlay.images
	c.mu.Lock()
	entry, ok := c.adaptive[id]
	if ok && time.Since(entry.added) > 35*time.Minute {
		delete(c.adaptive, id)
		ok = false
	}
	c.mu.Unlock()
	if !ok || entry.device != r.Header.Get("X-Device-ID") || entry.version != 2 {
		http.NotFound(w, r)
		return
	}
	_, release, ok := h.photoFrame.Borrow(entry.frame)
	if !ok {
		http.NotFound(w, r)
		return
	}
	defer release()
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second))
	defer controller.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(entry.data)))
	_, _ = w.Write(entry.data)
}
func rgb(frame []byte, x, y int) (int, int, int) {
	i := (y*1024 + x) * 2
	v := uint16(frame[i]) | uint16(frame[i+1])<<8
	r := int(v >> 11)
	g := int(v >> 5 & 63)
	b := int(v & 31)
	return r * 255 / 31, g * 255 / 63, b * 255 / 31
}

// Regions cover the maximum glyph extents, independent of locale and current text.
func textRegions(kind string) []image.Rectangle {
	switch kind {
	case "clock":
		return []image.Rectangle{image.Rect(14, 15, 396, 96), image.Rect(16, 98, 456, 124)}
	case "weather":
		return []image.Rectangle{image.Rect(18, 78, 220, 183)}
	case "event":
		return []image.Rectangle{image.Rect(20, 10, 440, 101)}
	default:
		return nil
	}
}
func analyzeTextRegions(frame []byte, kind string, x, y int) (string, bool) {
	var lightBad, darkBad, n int
	var sum, squares int64
	for _, rect := range textRegions(kind) {
		for yy := rect.Min.Y; yy < rect.Max.Y; yy++ {
			for xx := rect.Min.X; xx < rect.Max.X; xx++ {
				r, g, b := rgb(frame, x+xx, y+yy)
				l := (r*2126 + g*7152 + b*722) / 10000
				n++
				sum += int64(l)
				squares += int64(l * l)
				if l > 75 {
					lightBad++
				}
				if l < 180 {
					darkBad++
				}
			}
		}
	}
	variance := squares/int64(n) - (sum/int64(n))*(sum/int64(n))
	glass := (lightBad*10 > n && darkBad*10 > n && variance > 100) || variance > 1000
	if lightBad <= darkBad {
		return "light", glass
	}
	return "dark", glass
}

// Kept for generic fixture inspection.
func analyzeRegion(frame []byte, x, y, w, h int) (string, bool) {
	var lightBad, darkBad int
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			r, g, b := rgb(frame, xx, yy)
			l := (r*2126 + g*7152 + b*722) / 10000
			if l > 75 {
				lightBad++
			}
			if l < 180 {
				darkBad++
			}
		}
	}
	if lightBad <= darkBad {
		return "light", lightBad > 0
	}
	return "dark", darkBad > 0
}
func insideRound(x, y, w, h int) bool {
	const radius = 18
	cx, cy := x, y
	if x < radius {
		cx = radius
	} else if x >= w-radius {
		cx = w - radius - 1
	}
	if y < radius {
		cy = radius
	} else if y >= h-radius {
		cy = h - radius - 1
	}
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= radius*radius
}
func adaptivePatch(frame []byte, x, y, w, h int, tone string, glass bool, art func(*image.RGBA, bool)) []byte {
	return adaptivePatchContext(context.Background(), frame, x, y, w, h, tone, glass, art)
}
func adaptivePatchContext(ctx context.Context, frame []byte, x, y, w, h int, tone string, glass bool, art func(*image.RGBA, bool)) []byte {
	if !glass {
		out := make([]byte, w*h*2)
		for yy := 0; yy < h; yy++ {
			if ctx.Err() != nil {
				return nil
			}
			copy(out[yy*w*2:(yy+1)*w*2], frame[((y+yy)*1024+x)*2:((y+yy)*1024+x+w)*2])
		}
		if art == nil {
			return out
		}
		layer := image.NewRGBA(image.Rect(0, 0, w*2, h*2))
		art(layer, tone == "dark")
		scaled := image.NewRGBA(image.Rect(0, 0, w, h))
		xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), layer, layer.Bounds(), xdraw.Src, nil)
		for yy := 0; yy < h; yy++ {
			if ctx.Err() != nil {
				return nil
			}
			for xx := 0; xx < w; xx++ {
				ar, ag, ab, alpha := scaled.At(xx, yy).RGBA()
				if alpha == 0 {
					continue
				}
				r, g, b := rgb(frame, x+xx, y+yy)
				rr := int(ar>>8) + r*int(65535-alpha)/65535
				gg := int(ag>>8) + g*int(65535-alpha)/65535
				bb := int(ab>>8) + b*int(65535-alpha)/65535
				v := uint16(rr>>3)<<11 | uint16(gg>>2)<<5 | uint16(bb>>3)
				i := (yy*w + xx) * 2
				out[i] = byte(v)
				out[i+1] = byte(v >> 8)
			}
		}
		return out
	}
	img := image.NewRGBA(image.Rect(0, 0, w*2, h*2))
	for yy := 0; yy < h; yy++ {
		if ctx.Err() != nil {
			return nil
		}
		for xx := 0; xx < w; xx++ {
			r, g, b := rgb(frame, x+xx, y+yy)
			if glass && insideRound(xx, yy, w, h) {
				var sr, sg, sb, n int
				for dy := -4; dy <= 4; dy += 4 {
					for dx := -4; dx <= 4; dx += 4 {
						sx, sy := xx+dx, yy+dy
						if sx < 0 {
							sx = 0
						}
						if sx >= w {
							sx = w - 1
						}
						if sy < 0 {
							sy = 0
						}
						if sy >= h {
							sy = h - 1
						}
						a, c, d := rgb(frame, x+sx, y+sy)
						sr += a
						sg += c
						sb += d
						n++
					}
				}
				tint := 20
				if tone == "dark" {
					tint = 235
				}
				r = (sr/n*7 + tint*3) / 10
				g = (sg/n*7 + tint*3) / 10
				b = (sb/n*7 + tint*3) / 10
				if !insideRound(xx-1, yy, w, h) || !insideRound(xx+1, yy, w, h) || !insideRound(xx, yy-1, w, h) || !insideRound(xx, yy+1, w, h) {
					edge := 255
					if tone == "dark" {
						edge = 0
					}
					r = (r*8 + edge*2) / 10
					g = (g*8 + edge*2) / 10
					b = (b*8 + edge*2) / 10
				}
			}
			c := color.RGBA{uint8(r), uint8(g), uint8(b), 255}
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					img.SetRGBA(xx*2+dx, yy*2+dy, c)
				}
			}
		}
	}
	if art != nil {
		art(img, tone == "dark")
	}
	out := pixels(img)
	// Scaling can change unchanged edge pixels; restore those outside the rounded treatment when no art covers them.
	if glass {
		for yy := 0; yy < h; yy++ {
			if ctx.Err() != nil {
				return nil
			}
			for xx := 0; xx < w; xx++ {
				if !insideRound(xx, yy, w, h) {
					a := (yy*w + xx) * 2
					b := ((y+yy)*1024 + x + xx) * 2
					copy(out[a:a+2], frame[b:b+2])
				}
			}
		}
	}
	return out
}
