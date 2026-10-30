package photoframe

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	mrand "math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"
)

type Asset struct{ ID, Revision string }
type Source interface {
	List(context.Context) ([]Asset, error)
	Open(context.Context, Asset) (io.ReadCloser, error)
}
type Frame struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	OverlayContextVersion int     `json:"overlay_context_version,omitempty"`
	SceneContextVersion   int     `json:"scene_context_version,omitempty"`
	Version               int     `json:"version"`
	State                 string  `json:"state"`
	Generation            string  `json:"generation"`
	Idle                  int     `json:"idle_after_s"`
	Interval              int     `json:"slide_interval_s"`
	Stale                 int     `json:"max_stale_s"`
	Width                 int     `json:"width"`
	Height                int     `json:"height"`
	Format                string  `json:"pixel_format"`
	Bytes                 int     `json:"frame_bytes"`
	Frames                []Frame `json:"frames"`
}
type cached struct {
	frame     Frame
	data      []byte
	published time.Time
	pins      int
}
type Service struct {
	renderOptions     RenderOptions
	sequential        bool
	mu                sync.RWMutex
	manifest          Manifest
	cache             map[string]*cached
	limit             int
	source            Source
	enabled           bool
	transfers         chan struct{}
	byAsset           map[string]string
	lastSourceSuccess time.Time
	cycle             []Asset
	pos               int
	signature         string
	window            []string
	smallSteps        int
	shuffle           func(int, func(int, int))
}

func New(enabled bool, source Source, idle, interval, stale, limit int) *Service {
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	state := "disabled"
	if enabled {
		state = "warming"
		if source == nil {
			state = "unavailable"
		}
	}
	return &Service{renderOptions: RenderOptions{Layout: "adaptive", CropLimitPercent: 10}, manifest: Manifest{Version: 1, State: state, Generation: hex.EncodeToString(nonce[:]), Idle: idle, Interval: interval, Stale: stale, Width: Width, Height: Height, Format: "rgb565le", Bytes: FrameBytes, Frames: []Frame{}}, cache: map[string]*cached{}, limit: limit, source: source, enabled: enabled, transfers: make(chan struct{}, 2), byAsset: map[string]string{}, shuffle: mrand.Shuffle}
}

// Configure is called only before Run; each runtime snapshot owns its service.
func (s *Service) Configure(options RenderOptions, order string) {
	s.renderOptions = options
	if order == "sequential" {
		s.sequential = true
		s.shuffle = func(int, func(int, int)) {}
	}
}
func (s *Service) Manifest() Manifest {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.manifest
	m.Frames = append([]Frame{}, m.Frames...)
	now := time.Now()
	for _, f := range m.Frames {
		if c := s.cache[f.ID]; c != nil {
			c.published = now
		}
	}
	return m
}
func (s *Service) Enabled() bool { return s.enabled }
func (s *Service) Acquire() bool {
	select {
	case s.transfers <- struct{}{}:
		return true
	default:
		return false
	}
}
func (s *Service) Release() { <-s.transfers }

// Borrow pins a payload until the HTTP transfer has finished.
func (s *Service) Borrow(id string) ([]byte, func(), bool) {
	s.mu.Lock()
	v, ok := s.cache[id]
	if !ok || s.lastSourceSuccess.IsZero() || time.Since(s.lastSourceSuccess) >= time.Duration(s.manifest.Stale)*time.Second {
		s.mu.Unlock()
		return nil, nil, false
	}
	v.pins++
	s.mu.Unlock()
	return v.data, func() { s.mu.Lock(); v.pins--; s.mu.Unlock() }, true
}
func (s *Service) Run(ctx context.Context) {
	if !s.enabled || s.source == nil {
		return
	}
	s.refresh(ctx)
	ticker := time.NewTicker(time.Duration(s.manifest.Interval) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refresh(ctx)
		}
	}
}
func assetKey(a Asset) string {
	h := sha256.Sum256([]byte(a.ID + "\x00" + a.Revision))
	return hex.EncodeToString(h[:])
}
func (s *Service) refresh(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	assets, err := s.source.List(ctx)
	if err != nil {
		if errors.Is(err, ErrAccessDenied) {
			s.revokeSource()
			return
		}
		s.setState("unavailable")
		return
	}
	s.pruneSource(assets)
	if len(assets) == 0 {
		s.signature = ""
		s.window = nil
		s.cycle = nil
		s.setState("empty")
		return
	}
	// Hash the source listing without publishing source identifiers.
	keys := make([]string, 0, len(assets))
	seen := map[string]bool{}
	unique := make([]Asset, 0, len(assets))
	for _, a := range assets {
		k := assetKey(a)
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
			unique = append(unique, a)
		}
	}
	sort.Strings(keys)
	sig := strings.Join(keys, "")
	if sig != s.signature {
		s.signature = sig
		s.cycle = unique
		s.pos = 0
		s.window = nil
		s.smallSteps = 0
		s.shuffle(len(s.cycle), func(i, j int) { s.cycle[i], s.cycle[j] = s.cycle[j], s.cycle[i] })
		var nonce [16]byte
		_, _ = rand.Read(nonce[:])
		s.mu.Lock()
		s.manifest.Generation = hex.EncodeToString(nonce[:])
		s.manifest.State = "warming"
		s.manifest.Frames = []Frame{}
		s.mu.Unlock()
	}
	previousWindow := append([]string{}, s.window...)
	if len(s.window) > 0 {
		if len(s.cycle) <= 8 {
			previous := s.window[0]
			s.window = append(s.window[1:], s.window[0])
			s.smallSteps++
			if !s.sequential && s.smallSteps >= len(s.window) && len(s.window) > 1 {
				s.shuffle(len(s.window), func(i, j int) { s.window[i], s.window[j] = s.window[j], s.window[i] })
				if s.window[0] == previous {
					s.window[0], s.window[1] = s.window[1], s.window[0]
				}
				s.smallSteps = 0
			}
		} else {
			s.window = s.window[1:]
		}
	}
	target := min(8, len(s.cycle))
	attempts := 0
	for len(s.window) < target && attempts < len(s.cycle) && ctx.Err() == nil {
		if s.pos >= len(s.cycle) {
			last := assetKey(s.cycle[len(s.cycle)-1])
			s.shuffle(len(s.cycle), func(i, j int) { s.cycle[i], s.cycle[j] = s.cycle[j], s.cycle[i] })
			if !s.sequential && len(s.cycle) > 1 && assetKey(s.cycle[0]) == last {
				s.cycle[0], s.cycle[1] = s.cycle[1], s.cycle[0]
			}
			s.pos = 0
		}
		a := s.cycle[s.pos]
		k := assetKey(a)
		attempts++
		if contains(s.window, k) {
			s.pos++
			continue
		}
		s.mu.RLock()
		id := s.byAsset[k]
		c := s.cache[id]
		s.mu.RUnlock()
		if c == nil {
			s.mu.Lock()
			now := time.Now()
			for old, v := range s.cache {
				if len(s.cache) < s.limit {
					break
				}
				if v.pins == 0 && now.Sub(v.published) >= 120*time.Second && !frameContains(s.manifest.Frames, old) {
					delete(s.cache, old)
					for ak, aid := range s.byAsset {
						if aid == old {
							delete(s.byAsset, ak)
						}
					}
				}
			}
			full := len(s.cache) >= s.limit
			s.mu.Unlock()
			if full {
				s.window = previousWindow
				break
			}
			r, e := s.source.Open(ctx, a)
			if e != nil {
				if errors.Is(e, ErrAccessDenied) {
					s.revokeSource()
					return
				}
				s.pos++
				continue
			}
			data, e := RenderWithOptions(ctx, r, s.renderOptions)
			_ = r.Close()
			if e != nil {
				s.pos++
				continue
			}
			sum := sha256.Sum256(data)
			idHash := sha256.New()
			_, _ = idHash.Write([]byte("rgb565le-1024x600-v1\x00"))
			_, _ = idHash.Write(data)
			id = hex.EncodeToString(idHash.Sum(nil))
			c = &cached{frame: Frame{ID: id, SHA256: hex.EncodeToString(sum[:])}, data: data}
			s.mu.Lock()
			if prior := s.cache[id]; prior != nil {
				c = prior
			} else {
				s.cache[id] = c
			}
			s.byAsset[k] = id
			s.mu.Unlock()
		}
		s.window = append(s.window, k)
		s.pos++
	}
	if ctx.Err() != nil {
		s.setState("unavailable")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	frames := make([]Frame, 0, len(s.window))
	for _, k := range s.window {
		if c := s.cache[s.byAsset[k]]; c != nil {
			frames = append(frames, c.frame)
			c.published = time.Now()
		}
	}
	if len(frames) == 0 {
		s.manifest.State = "unavailable"
		s.manifest.Frames = []Frame{}
		return
	}
	s.manifest.Frames = frames
	s.manifest.State = "ready"
}

// Existing transfers own their borrowed byte slice. Removing the cache entry
// revokes new requests without invalidating those slices or their release hook.
func (s *Service) pruneSource(assets []Asset) {
	allowed := make(map[string]bool, len(assets))
	for _, a := range assets {
		allowed[assetKey(a)] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSourceSuccess = time.Now()
	retained := make(map[string]bool)
	for key, id := range s.byAsset {
		if !allowed[key] {
			delete(s.byAsset, key)
		} else {
			retained[id] = true
		}
	}
	for id := range s.cache {
		if !retained[id] {
			delete(s.cache, id)
		}
	}
	frames := s.manifest.Frames[:0]
	for _, f := range s.manifest.Frames {
		if retained[f.ID] {
			frames = append(frames, f)
		}
	}
	s.manifest.Frames = frames
}

func (s *Service) revokeSource() {
	s.mu.Lock()
	s.cache = map[string]*cached{}
	s.byAsset = map[string]string{}
	s.lastSourceSuccess = time.Time{}
	s.manifest.State = "unavailable"
	s.manifest.Frames = []Frame{}
	s.mu.Unlock()
	s.signature = ""
	s.window = nil
}
func frameContains(fs []Frame, id string) bool {
	for _, f := range fs {
		if f.ID == id {
			return true
		}
	}
	return false
}
func contains(fs []string, id string) bool {
	for _, f := range fs {
		if f == id {
			return true
		}
	}
	return false
}
func (s *Service) setState(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.manifest.State = state
	s.manifest.Frames = []Frame{}
}

// FixtureSource creates synthetic pictures in memory and accepts no filesystem paths.
type FixtureSource struct{}

func (FixtureSource) List(context.Context) ([]Asset, error) {
	return []Asset{{"red", "1"}, {"green", "1"}, {"blue", "1"}}, nil
}
func (FixtureSource) Open(ctx context.Context, a Asset) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var c color.RGBA
	switch a.ID {
	case "red":
		c = color.RGBA{255, 0, 0, 255}
	case "green":
		c = color.RGBA{0, 255, 0, 255}
	case "blue":
		c = color.RGBA{0, 0, 255, 255}
	default:
		return nil, fmt.Errorf("unknown fixture")
	}
	img := image.NewRGBA(image.Rect(0, 0, 32, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 32; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(b.Bytes())), nil
}
