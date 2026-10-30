package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"net/http"
	"strconv"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
)

const sceneStyle = "persistent-v3-r1"
const scenePixelFormat = "argb8888-le-straight"

type sceneBlock struct {
	overlayBlock
	PixelFormat string `json:"pixel_format"`
	ByteCount   int    `json:"byte_count"`
}
type sceneContext struct {
	Version    int           `json:"version"`
	Revision   string        `json:"revision"`
	ServerTime int64         `json:"server_time"`
	Clock      adaptiveClock `json:"clock"`
	Blocks     []sceneBlock  `json:"blocks"`
}

// contentArt measures the same raster that is composited into the final patch.
// A transparent layer keeps the measured geometry independent of the photo.
func contentArt(width, height int, paint func(*image.RGBA)) (int, int, func(*image.RGBA, bool)) {
	layer := image.NewRGBA(image.Rect(0, 0, width*2, height*2))
	paint(layer)
	minX, minY, maxX, maxY := width*2, height*2, 0, 0
	for y := 0; y < height*2; y++ {
		for x := 0; x < width*2; x++ {
			if layer.RGBAAt(x, y).A != 0 {
				if x < minX {
					minX = x
				}
				if y < minY {
					minY = y
				}
				if x+1 > maxX {
					maxX = x + 1
				}
				if y+1 > maxY {
					maxY = y + 1
				}
			}
		}
	}
	// Keep 16 physical pixels around visible art; rounded corners need that clearance.
	left, top := minX/2-16, minY/2-16
	if left < 0 {
		left = 0
	}
	if top < 0 {
		top = 0
	}
	right, bottom := (maxX+1)/2+16, (maxY+1)/2+16
	if right > width {
		right = width
	}
	if bottom > height {
		bottom = height
	}
	w, h := right-left, bottom-top
	return w, h, func(dst *image.RGBA, _ bool) { draw.Draw(dst, dst.Bounds(), layer, image.Pt(left*2, top*2), draw.Over) }
}

// scenePixels converts Go's premultiplied RGBA into straight-alpha BGRA bytes.
func scenePixels(width, height int, art func(*image.RGBA, bool)) []byte {
	high := image.NewRGBA(image.Rect(0, 0, width*2, height*2))
	art(high, false)
	small := image.NewRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(small, small.Bounds(), high, high.Bounds(), xdraw.Src, nil)
	out := make([]byte, width*height*4)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			c := small.RGBAAt(x, y)
			i := (y*width + x) * 4
			if c.A != 0 {
				un := func(v uint8) byte {
					n := (int(v)*255 + int(c.A)/2) / int(c.A)
					if n > 255 {
						n = 255
					}
					return byte(n)
				}
				out[i], out[i+1], out[i+2], out[i+3] = un(c.B), un(c.G), un(c.R), c.A
			}
		}
	}
	return out
}

func (h *Handler) PhotoContextV3(w http.ResponseWriter, r *http.Request) {
	if !h.overlayAuth(w, r) {
		return
	}
	if h.overlay == nil {
		http.NotFound(w, r)
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
	h.refreshOverlay(r, s)
	if r.Context().Err() != nil {
		return
	}
	now := time.Now()
	clock := adaptiveClock{overlayClock: overlayClock{enabled(s.photos.ShowClock), s.screen.Locale, s.screen.Timezone, s.screen.ClockFormat}, Tone: "light", Backdrop: enabled(s.photos.ShowClock)}
	result := sceneContext{Version: 3, ServerTime: now.Unix(), Clock: clock, Blocks: []sceneBlock{}}
	add := func(kind string, x, y, width, height int, expiry time.Time, art func(*image.RGBA, bool)) bool {
		data := scenePixels(width, height, art)
		if r.Context().Err() != nil {
			return false
		}
		block := s.saveScene(device, kind, data, width, height, x, y, expiry)
		if block == nil {
			return false
		}
		result.Blocks = append(result.Blocks, *block)
		return true
	}
	if s.weatherEnabled && enabled(s.photos.ShowWeather) && s.weatherData != nil && s.weatherExpiry.After(now) {
		width, height, art := contentArt(236, 200, func(img *image.RGBA) { drawWeather(img, *s.weatherData, s.screen, false, true) })
		if !add("weather", 752, 36, width, height, s.weatherExpiry, art) {
			if r.Context().Err() == nil {
				http.Error(w, "busy", 503)
			}
			return
		}
	}
	if s.calendarEnabled && enabled(s.photos.ShowEvent) && s.eventData != nil && s.eventExpiry.After(now) {
		width, height, art := contentArt(456, 112, func(img *image.RGBA) { drawEvent(img, *s.eventData, s.screen, now, false, true) })
		if !add("event", 986-width, 576-height, width, height, s.eventExpiry, art) {
			if r.Context().Err() == nil {
				http.Error(w, "busy", 503)
			}
			return
		}
	}
	if r.Context().Err() != nil {
		return
	}
	rev, _ := json.Marshal(struct {
		Clock  adaptiveClock
		Blocks []sceneBlock
	}{result.Clock, result.Blocks})
	sum := sha256.Sum256(rev)
	result.Revision = hex.EncodeToString(sum[:])
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *overlayState) saveScene(device, kind string, data []byte, width, height, x, y int, expiry time.Time) *sceneBlock {
	sum := sha256.Sum256(data)
	identity := fmt.Sprintf("%s\x00%s\x00%s\x00%d,%d,%d,%d\x00", sceneStyle, device, kind, x, y, width, height)
	id := sha256.Sum256(append([]byte(identity), data...))
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
	c.adaptive[key] = overlayImage{data: data, added: now, device: device, version: 3}
	return &sceneBlock{overlayBlock{kind, key, hex.EncodeToString(sum[:]), x, y, width, height, expiry.Unix()}, scenePixelFormat, len(data)}
}

func (h *Handler) PhotoOverlayV3(w http.ResponseWriter, r *http.Request) {
	if !h.overlayAuth(w, r) {
		return
	}
	if h.overlay == nil {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/photo-frame/v3/overlays/")
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
	if !ok || entry.device != r.Header.Get("X-Device-ID") || entry.version != 3 {
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
