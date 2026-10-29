package photoframe

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestAdaptivePortraitPreservesTopAndBottom(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 100, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 100; x++ {
			c := color.NRGBA{0, 255, 0, 255}
			if y < 50 {
				c = color.NRGBA{255, 0, 0, 255}
			} else if y >= 150 {
				c = color.NRGBA{0, 0, 255, 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	out, err := Render(context.Background(), &b)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		x, y int
		want uint16
	}{{Width / 2, 10, 0xf800}, {Width / 2, Height - 11, 0x001f}, {Width / 2, Height / 2, 0x07e0}, {Width / 2, 149, 0xf800}, {Width / 2, 150, 0x07e0}, {Width / 2, 449, 0x07e0}, {Width / 2, 450, 0x001f}} {
		got := binary.LittleEndian.Uint16(out[(p.y*Width+p.x)*2:])
		if got != p.want {
			t.Fatalf("portrait content at %d,%d = %04x, want %04x", p.x, p.y, got, p.want)
		}
	}
	for _, x := range []int{0, Width - 1} {
		got := binary.LittleEndian.Uint16(out[((Height/2)*Width+x)*2:])
		if got == 0 || got == 0x07e0 || got&0xf81f != 0 {
			t.Fatalf("background is not dimmed green: %04x", got)
		}
	}
}

func TestAdaptiveTenPercentBoundary(t *testing.T) {
	for _, tc := range []struct {
		w, h  int
		cover bool
	}{
		{1024, 600, true}, {16, 9, true}, {3, 2, false}, {1, 1, false}, {3, 4, false}, {4, 1, false},
		{1537, 1000, true}, {1536, 1000, true}, {1535, 1000, false},
		{2559, 1350, true}, {2560, 1350, true}, {2561, 1350, false},
	} {
		if got := cropWithinLimit(tc.w, tc.h); got != tc.cover {
			t.Fatalf("%dx%d cover=%v expected %v", tc.w, tc.h, got, tc.cover)
		}
	}
}

func TestAdaptivePanoramaPreservesLeftAndRight(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 200, 50))
	for y := 0; y < 50; y++ {
		for x := 0; x < 200; x++ {
			c := color.NRGBA{0, 255, 0, 255}
			if x < 50 {
				c = color.NRGBA{255, 0, 0, 255}
			} else if x >= 150 {
				c = color.NRGBA{0, 0, 255, 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	out, err := Render(context.Background(), &b)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		x, y int
		want uint16
	}{{10, Height / 2, 0xf800}, {Width - 11, Height / 2, 0x001f}, {Width / 2, Height / 2, 0x07e0}} {
		got := binary.LittleEndian.Uint16(out[(p.y*Width+p.x)*2:])
		if got != p.want {
			t.Fatalf("panorama content missing: %04x want %04x", got, p.want)
		}
	}
	for _, y := range []int{0, Height - 1} {
		got := binary.LittleEndian.Uint16(out[(y*Width+Width/2)*2:])
		if got == 0 || got == 0x07e0 || got&0xf81f != 0 {
			t.Fatalf("background is not dimmed green: %04x", got)
		}
	}
}

func TestBlurSoftensOnlyTheBackground(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, backgroundWidth, backgroundHeight))
	for y := 0; y < backgroundHeight; y++ {
		for x := 0; x < backgroundWidth; x++ {
			c := color.RGBA{0, 0, 255, 255}
			if x >= backgroundWidth/2 {
				c = color.RGBA{255, 0, 0, 255}
			}
			src.SetRGBA(x, y, c)
		}
	}
	dst := image.NewRGBA(src.Bounds())
	for range blurPasses {
		if err := blurAxis(context.Background(), src, dst, true); err != nil {
			t.Fatal(err)
		}
		if err := blurAxis(context.Background(), dst, src, false); err != nil {
			t.Fatal(err)
		}
	}
	center := src.RGBAAt(backgroundWidth/2, backgroundHeight/2)
	if center.R == 0 || center.R == 255 || center.B == 0 || center.B == 255 || center.A != 255 {
		t.Fatal("background edge was not softened", center)
	}
	if src.RGBAAt(0, 0) != (color.RGBA{0, 0, 255, 255}) {
		t.Fatal("blur introduced black edge padding")
	}
}

func TestAdaptiveCancellationAndTinySources(t *testing.T) {
	for _, size := range []image.Point{{16, 9}, {1, 1}, {1, 10}, {10, 1}} {
		img := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
		for y := 0; y < size.Y; y++ {
			for x := 0; x < size.X; x++ {
				img.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
			}
		}
		var encoded bytes.Buffer
		png.Encode(&encoded, img)
		out, err := Render(context.Background(), bytes.NewReader(encoded.Bytes()))
		if err != nil || len(out) != FrameBytes {
			t.Fatalf("tiny source %v: %v", size, err)
		}
		for i := 0; i < len(out); i += 2 {
			if binary.LittleEndian.Uint16(out[i:i+2]) == 0 {
				t.Fatal("solid photo gained black padding")
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		out, err = Render(ctx, bytes.NewReader(encoded.Bytes()))
		if !errors.Is(err, context.Canceled) || out != nil {
			t.Fatal("render ignored cancellation")
		}
	}
}
