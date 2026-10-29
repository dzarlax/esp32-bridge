package photoframe

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"strconv"
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
