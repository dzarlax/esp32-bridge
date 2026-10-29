package photoframe

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestFixtureService(t *testing.T) {
	s := New(true, FixtureSource{}, 300, 60, 900, 32)
	s.refresh(context.Background())
	m := s.Manifest()
	if m.State != "ready" || len(m.Frames) != 3 {
		t.Fatalf("manifest: %+v", m)
	}
	for _, f := range m.Frames {
		data, release, ok := s.Borrow(f.ID)
		if !ok || len(data) != FrameBytes {
			t.Fatal("missing frame")
		}
		sum := sha256.Sum256(data)
		release()
		if hex.EncodeToString(sum[:]) != f.SHA256 {
			t.Fatal("hash mismatch")
		}
	}
	first := m.Frames[0].ID
	s.refresh(context.Background())
	if s.Manifest().Frames[0].ID == first {
		t.Fatal("rotation did not advance")
	}
}
func TestUnavailableAndDisabled(t *testing.T) {
	for _, v := range []struct {
		enabled bool
		state   string
	}{{false, "disabled"}, {true, "unavailable"}} {
		s := New(v.enabled, nil, 300, 60, 900, 32)
		m := s.Manifest()
		if m.State != v.state || len(m.Frames) != 0 {
			t.Fatal(m)
		}
	}
}
func TestTransferLimit(t *testing.T) {
	s := New(false, nil, 300, 60, 900, 32)
	if !s.Acquire() || !s.Acquire() || s.Acquire() {
		t.Fatal("bad limit")
	}
	s.Release()
	if !s.Acquire() {
		t.Fatal("slot not released")
	}
	s.Release()
	s.Release()
}

type manySource struct{ FixtureSource }

func (manySource) List(context.Context) ([]Asset, error) {
	out := make([]Asset, 12)
	for i := range out {
		out[i] = Asset{fmt.Sprintf("%d", i), "1"}
	}
	return out, nil
}
func (manySource) Open(ctx context.Context, a Asset) (io.ReadCloser, error) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	n, _ := strconv.Atoi(a.ID)
	img.Set(0, 0, color.RGBA{uint8(n * 17), uint8(n * 13), uint8(n * 7), 255})
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return io.NopCloser(bytes.NewReader(b.Bytes())), ctx.Err()
}
func TestCacheRetainsPublishedAndBoundsRotation(t *testing.T) {
	s := New(true, manySource{}, 300, 60, 900, 9)
	s.refresh(context.Background())
	first := s.Manifest()
	if len(first.Frames) != 8 {
		t.Fatal(len(first.Frames))
	}
	s.refresh(context.Background())
	if len(s.cache) > 9 {
		t.Fatal("cache limit exceeded")
	}
	s.refresh(context.Background())
	if len(s.cache) > 9 || len(s.Manifest().Frames) != 8 {
		t.Fatal("full cache must delay rotation")
	}
	for _, f := range first.Frames {
		_, release, ok := s.Borrow(f.ID)
		if !ok {
			t.Fatal("published frame was evicted")
		}
		release()
	}
}

func TestSmallCycleShufflesWithoutRepeatingBoundary(t *testing.T) {
	s := New(true, FixtureSource{}, 300, 60, 900, 32)
	calls := 0
	s.shuffle = func(n int, swap func(int, int)) {
		calls++
		for i := 0; i < n/2; i++ {
			swap(i, n-1-i)
		}
	}
	s.refresh(context.Background())
	if calls != 1 {
		t.Fatal(calls)
	}
	for i := 0; i < 3; i++ {
		before := s.Manifest().Frames[0].ID
		s.refresh(context.Background())
		after := s.Manifest().Frames[0].ID
		if before == after {
			t.Fatal("adjacent repeated frame")
		}
	}
	if calls != 2 {
		t.Fatalf("cycle did not reshuffle: %d", calls)
	}
}
func TestFullCacheRecoversAfterRetention(t *testing.T) {
	s := New(true, manySource{}, 300, 60, 900, 9)
	s.refresh(context.Background())
	s.refresh(context.Background())
	before := s.Manifest()
	s.refresh(context.Background())
	blocked := s.Manifest()
	if before.Frames[0].ID != blocked.Frames[0].ID {
		t.Fatal("expected blocked rotation")
	}
	for id, c := range s.cache {
		if !frameContains(blocked.Frames, id) {
			c.published = time.Now().Add(-121 * time.Second)
		}
	}
	s.refresh(context.Background())
	after := s.Manifest()
	if after.Frames[0].ID == blocked.Frames[0].ID {
		t.Fatal("rotation did not recover")
	}
	if len(s.cache) > 9 {
		t.Fatal("cache limit exceeded")
	}
}

type changingSource struct {
	FixtureSource
	assets           []Asset
	listErr, openErr error
}

func (s *changingSource) List(context.Context) ([]Asset, error) { return s.assets, s.listErr }
func (s *changingSource) Open(ctx context.Context, a Asset) (io.ReadCloser, error) {
	if s.openErr != nil {
		return nil, s.openErr
	}
	return s.FixtureSource.Open(ctx, a)
}
func TestSourceRemovalRevokesBorrowAndRecoversFromEmpty(t *testing.T) {
	src := &changingSource{assets: []Asset{{"red", "1"}}}
	s := New(true, src, 300, 60, 900, 32)
	s.refresh(context.Background())
	old := s.Manifest().Frames[0].ID
	data, release, ok := s.Borrow(old)
	if !ok {
		t.Fatal("initial borrow")
	}
	src.assets = nil
	s.refresh(context.Background())
	if _, _, ok := s.Borrow(old); ok {
		t.Fatal("removed frame remains accessible")
	}
	if len(data) != FrameBytes {
		t.Fatal("in-flight payload corrupted")
	}
	release()
	if len(s.cache) != 0 || len(s.byAsset) != 0 || s.Manifest().State != "empty" {
		t.Fatal("empty album not purged")
	}
	src.assets = []Asset{{"red", "1"}}
	s.refresh(context.Background())
	if s.Manifest().State != "ready" {
		t.Fatal("empty album never recovers")
	}
}
func TestSourceAuthorizationRevocation(t *testing.T) {
	for _, duringOpen := range []bool{false, true} {
		t.Run(fmt.Sprint(duringOpen), func(t *testing.T) {
			src := &changingSource{assets: []Asset{{"red", "1"}}}
			s := New(true, src, 300, 60, 900, 32)
			s.refresh(context.Background())
			old := s.Manifest().Frames[0].ID
			_, release, ok := s.Borrow(old)
			if !ok {
				t.Fatal("initial borrow")
			}
			if duringOpen {
				src.assets = append(src.assets, Asset{"blue", "1"})
				src.openErr = ErrAccessDenied
			} else {
				src.listErr = ErrAccessDenied
			}
			s.refresh(context.Background())
			if _, _, ok := s.Borrow(old); ok {
				t.Fatal("revoked credential can fetch old frame")
			}
			release()
			if len(s.cache) != 0 || len(s.Manifest().Frames) != 0 {
				t.Fatal("revocation retains cache")
			}
			src.listErr = nil
			src.openErr = nil
			s.refresh(context.Background())
			if s.Manifest().State != "ready" {
				t.Fatal("auth recovery failed")
			}
		})
	}
}
func TestTransientFailureCannotExtendCachedFrameLifetime(t *testing.T) {
	src := &changingSource{assets: []Asset{{"red", "1"}}}
	s := New(true, src, 300, 60, 60, 32)
	s.refresh(context.Background())
	old := s.Manifest().Frames[0].ID
	src.listErr = fmt.Errorf("offline")
	s.refresh(context.Background())
	_, release, ok := s.Borrow(old)
	if !ok {
		t.Fatal("transient failure lost bounded stale frame")
	}
	release()
	s.mu.Lock()
	s.lastSourceSuccess = time.Now().Add(-61 * time.Second)
	s.mu.Unlock()
	s.Manifest()
	if _, _, ok := s.Borrow(old); ok {
		t.Fatal("expired stale frame is accessible")
	}
}

func TestImmichSourceThroughRendererAndCache(t *testing.T) {
	red, err := os.ReadFile("testdata/solid-red.webp")
	if err != nil {
		t.Fatal(err)
	}
	greenReader, err := (FixtureSource{}).Open(context.Background(), Asset{ID: "green"})
	if err != nil {
		t.Fatal(err)
	}
	green, err := io.ReadAll(greenReader)
	greenReader.Close()
	if err != nil {
		t.Fatal(err)
	}
	src, _ := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != testAPIKey {
			t.Error("missing source authentication")
		}
		switch r.URL.Path {
		case "/api/albums/" + testAlbumID:
			albumResponse(w)
		case "/api/search/metadata":
			searchResponse(w, []testImmichAsset{eligibleAsset(testAssetID), eligibleAsset(testAssetID2)}, nil)
		case "/api/assets/" + testAssetID + "/thumbnail":
			w.Write(red)
		case "/api/assets/" + testAssetID2 + "/thumbnail":
			w.Write(green)
		default:
			http.NotFound(w, r)
		}
	})
	s := New(true, src, 300, 60, 900, 32)
	s.refresh(context.Background())
	m := s.Manifest()
	if m.State != "ready" || len(m.Frames) != 2 {
		t.Fatalf("pipeline manifest: %+v", m)
	}
	colors := map[uint16]bool{}
	for _, f := range m.Frames {
		data, release, ok := s.Borrow(f.ID)
		if !ok {
			t.Fatal("missing prepared frame")
		}
		if len(data) != FrameBytes {
			t.Fatal("bad rendered frame length")
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != f.SHA256 {
			t.Fatal("bad frame SHA256")
		}
		colors[binary.LittleEndian.Uint16(data[((Height/2)*Width+Width/2)*2:])] = true
		release()
	}
	if !colors[0xf800] || !colors[0x07e0] {
		t.Fatal("real codec pipeline lost colors")
	}
}

func TestBorrowDuringRevocation(t *testing.T) {
	src := &changingSource{assets: []Asset{{"red", "1"}}}
	s := New(true, src, 300, 60, 900, 32)
	s.refresh(context.Background())
	old := s.Manifest().Frames[0].ID
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				data, release, ok := s.Borrow(old)
				if ok {
					if len(data) != FrameBytes {
						t.Error("corrupt borrowed payload")
					}
					release()
				}
				s.Manifest()
			}
		}()
	}
	src.listErr = ErrAccessDenied
	s.refresh(context.Background())
	wg.Wait()
	if _, _, ok := s.Borrow(old); ok {
		t.Fatal("revoked payload remains available")
	}
}
