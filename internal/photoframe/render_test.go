package photoframe

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"testing"
)

func TestRenderRGB565AndBlackAlpha(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, Width, Height))
	colors := []color.NRGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 255, 0}}
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			img.SetNRGBA(x, y, colors[x/256])
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	data, err := Render(context.Background(), &b)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != FrameBytes {
		t.Fatal(len(data))
	}
	for x, want := range []uint16{0xf800, 0x07e0, 0x001f, 0} {
		got := binary.LittleEndian.Uint16(data[((Height/2)*Width+x*256+128)*2:])
		if got != want {
			t.Fatalf("pixel %d got %04x want %04x", x, got, want)
		}
	}
	if binary.LittleEndian.Uint16(data[:2]) != 0xf800 {
		t.Fatal("matching-aspect photo edge was not preserved")
	}
}
func TestOrientationCoordinates(t *testing.T) {
	for o := 1; o <= 8; o++ {
		w, h := 3, 2
		ow, oh := w, h
		if o >= 5 {
			ow, oh = h, w
		}
		seen := map[[2]int]bool{}
		for y := 0; y < oh; y++ {
			for x := 0; x < ow; x++ {
				sx, sy := sourcePoint(o, x, y, w, h)
				if sx < 0 || sx >= w || sy < 0 || sy >= h || seen[[2]int{sx, sy}] {
					t.Fatalf("orientation %d invalid mapping", o)
				}
				seen[[2]int{sx, sy}] = true
			}
		}
	}
}
func TestMalformedExif(t *testing.T) {
	_, err := orientation([]byte{0xff, 0xd8, 0xff, 0xe1, 0, 10, 'E', 'x', 'i', 'f', 0, 0, 0, 0})
	if err == nil {
		t.Fatal("expected malformed EXIF error")
	}
}

func TestJPEGExifOrientations(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 80, 40))
	cols := []color.RGBA{{240, 20, 20, 255}, {20, 240, 20, 255}, {20, 20, 240, 255}, {240, 240, 20, 255}}
	for y := 0; y < 40; y++ {
		for x := 0; x < 80; x++ {
			i := 0
			if x >= 40 {
				i++
			}
			if y >= 20 {
				i += 2
			}
			img.SetRGBA(x, y, cols[i])
		}
	}
	var original bytes.Buffer
	if err := jpeg.Encode(&original, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	corners := [][4]int{{}, {0, 1, 2, 3}, {1, 0, 3, 2}, {3, 2, 1, 0}, {2, 3, 0, 1}, {0, 2, 1, 3}, {2, 0, 3, 1}, {3, 1, 2, 0}, {1, 3, 0, 2}}
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for o := 1; o <= 8; o++ {
			t.Run(fmt.Sprintf("%T/%d", order, o), func(t *testing.T) {
				tiff := make([]byte, 26)
				if order == binary.LittleEndian {
					copy(tiff, "II")
				} else {
					copy(tiff, "MM")
				}
				order.PutUint16(tiff[2:], 42)
				order.PutUint32(tiff[4:], 8)
				order.PutUint16(tiff[8:], 1)
				order.PutUint16(tiff[10:], 0x112)
				order.PutUint16(tiff[12:], 3)
				order.PutUint32(tiff[14:], 1)
				order.PutUint16(tiff[18:], uint16(o))
				segment := append([]byte("Exif\x00\x00"), tiff...)
				n := len(segment) + 2
				encoded := append([]byte{0xff, 0xd8, 0xff, 0xe1, byte(n >> 8), byte(n)}, segment...)
				encoded = append(encoded, original.Bytes()[2:]...)
				out, err := Render(context.Background(), bytes.NewReader(encoded))
				if err != nil {
					t.Fatal(err)
				}
				dw, dh := 1024, 512
				if o >= 5 {
					dw, dh = 300, 600
				}
				left, top := (Width-dw)/2, (Height-dh)/2
				points := [][2]int{{left + dw/4, top + dh/4}, {left + 3*dw/4, top + dh/4}, {left + dw/4, top + 3*dh/4}, {left + 3*dw/4, top + 3*dh/4}}
				for i, p := range points {
					v := binary.LittleEndian.Uint16(out[(p[1]*Width+p[0])*2:])
					r, g, b := int(v>>11)*255/31, int((v>>5)&63)*255/63, int(v&31)*255/31
					c := cols[corners[o][i]]
					if abs(r-int(c.R)) > 20 || abs(g-int(c.G)) > 20 || abs(b-int(c.B)) > 20 {
						t.Fatalf("corner %d got %d,%d,%d expected %+v", i, r, g, b, c)
					}
				}
			})
		}
	}
}
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func TestRenderWebPAndRejectProfile(t *testing.T) {
	data, err := os.ReadFile("testdata/solid-red.webp")
	if err != nil {
		t.Fatal(err)
	}
	pixels, err := Render(context.Background(), bytes.NewReader(data))
	if err != nil || len(pixels) != FrameBytes {
		t.Fatalf("WebP render: %v", err)
	}
	if got := binary.LittleEndian.Uint16(pixels[((Height/2)*Width+Width/2)*2:]); got != 0xf800 {
		t.Fatalf("red WebP pixel = %04x", got)
	}
	// An ancillary ICCP chunk must not bypass color-profile rejection.
	profiled := append(append([]byte{}, data...), []byte{'I', 'C', 'C', 'P', 2, 0, 0, 0, 0, 0}...)
	binary.LittleEndian.PutUint32(profiled[4:8], uint32(len(profiled)-8))
	if _, err := Render(context.Background(), bytes.NewReader(profiled)); err == nil {
		t.Fatal("accepted WebP ICC profile")
	}
}

func TestRenderRecognizesOnlyCompleteKnownSRGBProfile(t *testing.T) {
	profile, err := os.ReadFile("testdata/libvips-srgb.icm")
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.SetRGBA(0, 0, color.RGBA{255, 0, 0, 255})
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, nil); err != nil {
		t.Fatal(err)
	}
	original := encoded.Bytes()
	withICC := func(parts ...[]byte) []byte {
		out := append([]byte{}, original[:2]...)
		for i, part := range parts {
			segment := append([]byte("ICC_PROFILE\x00"), byte(i+1), byte(len(parts)))
			segment = append(segment, part...)
			out = append(out, 0xff, 0xe2, byte((len(segment)+2)>>8), byte(len(segment)+2))
			out = append(out, segment...)
		}
		return append(out, original[2:]...)
	}
	baseline, err := Render(context.Background(), bytes.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{withICC(profile), withICC(profile[:240], profile[240:])} {
		pixels, err := Render(context.Background(), bytes.NewReader(data))
		if err != nil || !bytes.Equal(pixels, baseline) {
			t.Fatalf("known sRGB rejected or pixels changed: %v", err)
		}
	}
	altered := append([]byte{}, profile...)
	altered[100] ^= 1
	if _, err := Render(context.Background(), bytes.NewReader(withICC(altered))); err == nil {
		t.Fatal("unknown profile trusted by description")
	}
	incomplete := withICC(profile)
	incomplete[2+4+13] = 2
	if _, err := Render(context.Background(), bytes.NewReader(incomplete)); err == nil {
		t.Fatal("missing ICC segment accepted")
	}
	duplicate := withICC(profile[:240], profile[240:])
	second := 2 + 4 + 14 + 240
	duplicate[second+4+12] = 1
	if _, err := Render(context.Background(), bytes.NewReader(duplicate)); err == nil {
		t.Fatal("duplicate ICC segment accepted")
	}
}

func TestPinnedDisplayP3ProfileAndConversion(t *testing.T) {
	profile, err := os.ReadFile("testdata/libvips-p3.icm")
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetRGBA(x, y, color.RGBA{204, 102, 51, 255})
		}
	}
	var encoded bytes.Buffer
	jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 100})
	segment := append([]byte("ICC_PROFILE\x00"), 1, 1)
	segment = append(segment, profile...)
	data := append([]byte{}, encoded.Bytes()[:2]...)
	data = append(data, 0xff, 0xe2, byte((len(segment)+2)>>8), byte(len(segment)+2))
	data = append(data, segment...)
	data = append(data, encoded.Bytes()[2:]...)
	if hash, ok := jpegProfile(data); !ok || hash != libvipsP3Hash {
		t.Fatal("standard Display P3 not recognized")
	}
	if _, err := Render(context.Background(), bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	for _, v := range []uint32{0, 32768, 65535} {
		r, g, b := p3ToSRGB(v, v, v)
		if r != v || g != v || b != v {
			t.Fatal("neutral color changed", v, r, g, b)
		}
	}
	r, g, b := p3ToSRGB(52428, 26214, 13107)
	// Golden vector calculated independently from the pinned ICC XYZ matrices.
	for i, got := range []uint32{r, g, b} {
		want := []int{56320, 24282, 8378}[i]
		if diff := int(got) - want; diff < -3 || diff > 3 {
			t.Fatalf("P3 channel %d = %d expected %d", i, got, want)
		}
	}
}

func TestRenderCoverCropsCenterAndFillsEveryPixel(t *testing.T) {
	for _, size := range []image.Point{{40, 10}, {10, 40}, {20, 20}, {1, 10}, {10, 1}} {
		t.Run(fmt.Sprintf("%dx%d", size.X, size.Y), func(t *testing.T) {
			img := image.NewNRGBA(image.Rect(0, 0, size.X, size.Y))
			for y := 0; y < size.Y; y++ {
				for x := 0; x < size.X; x++ {
					c := color.NRGBA{0, 255, 0, 255}
					if size.X == 40 && (x < 10 || x >= 30) || size.Y == 40 && (y < 10 || y >= 30) || size.X == 20 && (y < 3 || y >= 17) {
						c = color.NRGBA{255, 0, 255, 255}
					}
					img.SetNRGBA(x, y, c)
				}
			}
			data, err := renderCover(context.Background(), orientedPhoto{img: img, width: size.X, height: size.Y, orientation: 1}, size.X, size.Y)
			if err != nil {
				t.Fatal(err)
			}
			if len(data) != FrameBytes {
				t.Fatal("wrong frame size")
			}
			for i := 0; i < len(data); i += 2 {
				if v := binary.LittleEndian.Uint16(data[i : i+2]); v != 0x07e0 {
					t.Fatalf("cropped photo left a border or lost center at pixel %d: %04x", i/2, v)
				}
			}
		})
	}
}
