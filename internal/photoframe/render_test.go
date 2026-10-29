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
	"testing"
)

func TestRenderRGB565AndBlackAlpha(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 1))
	img.SetNRGBA(0, 0, color.NRGBA{255, 0, 0, 255})
	img.SetNRGBA(1, 0, color.NRGBA{0, 255, 0, 255})
	img.SetNRGBA(2, 0, color.NRGBA{0, 0, 255, 255})
	img.SetNRGBA(3, 0, color.NRGBA{255, 255, 255, 0})
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
	if data[0] != 0 {
		t.Fatal("letterbox is not black")
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
