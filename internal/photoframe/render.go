package photoframe

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
)

const Width = 1024
const Height = 600
const FrameBytes = Width * Height * 2
const maxSource = 20 << 20
const maxPixels = 24_000_000

// Render decodes a bounded JPEG, PNG or WebP and emits RGB565 little endian.
func Render(ctx context.Context, r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxSource+1))
	if err != nil || len(data) > maxSource {
		return nil, errors.New("source read failed or exceeded limit")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png" && format != "webp") || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, errors.New("unsupported image or dimensions")
	}
	// Only the two pinned libvips sRGB/Display P3 JPEG profiles are supported.
	convertP3 := false
	if format == "jpeg" {
		profile, supported := jpegProfile(data)
		if !supported {
			return nil, errors.New("unsupported color profile")
		}
		convertP3 = profile == libvipsP3Hash
	}
	if (format == "png" && bytes.Contains(data, []byte("iCCP"))) || (format == "webp" && webPHasProfile(data)) {
		return nil, errors.New("unsupported color profile")
	}
	orient := 1
	if format == "jpeg" {
		orient, err = orientation(data)
		if err != nil {
			return nil, err
		}
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("decode failed")
	}
	sw, sh := cfg.Width, cfg.Height
	ow, oh := sw, sh
	if orient >= 5 {
		ow, oh = sh, sw
	}
	dw, dh := Width, Height
	if int64(Width)*int64(oh) <= int64(Height)*int64(ow) {
		dh = int(int64(Width) * int64(oh) / int64(ow))
	} else {
		dw = int(int64(Height) * int64(ow) / int64(oh))
	}
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	out := make([]byte, FrameBytes)
	left, top := (Width-dw)/2, (Height-dh)/2
	for y := 0; y < dh; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := 0; x < dw; x++ {
			ox, oy := x*ow/dw, y*oh/dh
			sx, sy := sourcePoint(orient, ox, oy, sw, sh)
			rr, gg, bb, _ := img.At(img.Bounds().Min.X+sx, img.Bounds().Min.Y+sy).RGBA() // premultiplied alpha composites on black
			if convertP3 {
				rr, gg, bb = p3ToSRGB(rr, gg, bb)
			}
			v := uint16(rr>>11)<<11 | uint16(gg>>10)<<5 | uint16(bb>>11)
			p := ((top+y)*Width + left + x) * 2
			binary.LittleEndian.PutUint16(out[p:p+2], v)
		}
	}
	return out, nil
}

func webPHasProfile(data []byte) bool {
	for p := 12; p+8 <= len(data); {
		n := uint64(binary.LittleEndian.Uint32(data[p+4 : p+8]))
		if string(data[p:p+4]) == "ICCP" {
			return true
		}
		end := uint64(p) + 8 + n + n%2
		if end > uint64(len(data)) {
			return false // The decoder rejects the malformed image.
		}
		p = int(end)
	}
	return false
}
func sourcePoint(o, x, y, w, h int) (int, int) {
	switch o {
	case 2:
		return w - 1 - x, y
	case 3:
		return w - 1 - x, h - 1 - y
	case 4:
		return x, h - 1 - y
	case 5:
		return y, x
	case 6:
		return y, h - 1 - x
	case 7:
		return w - 1 - y, h - 1 - x
	case 8:
		return w - 1 - y, x
	default:
		return x, y
	}
}

// orientation scans only bounded JPEG segments and validates the TIFF offsets.
func orientation(b []byte) (int, error) {
	if len(b) < 2 || b[0] != 0xff || b[1] != 0xd8 {
		return 1, errors.New("invalid JPEG")
	}
	for p := 2; p+4 <= len(b); {
		if b[p] != 0xff {
			return 1, errors.New("invalid JPEG segment")
		}
		for p < len(b) && b[p] == 0xff {
			p++
		}
		if p >= len(b) {
			break
		}
		marker := b[p]
		p++
		if marker == 0xda || marker == 0xd9 {
			break
		}
		if marker == 0x01 || marker >= 0xd0 && marker <= 0xd7 {
			continue
		}
		if p+2 > len(b) {
			return 1, errors.New("invalid JPEG segment")
		}
		n := int(binary.BigEndian.Uint16(b[p : p+2]))
		if n < 2 || p+n > len(b) {
			return 1, errors.New("invalid JPEG segment")
		}
		seg := b[p+2 : p+n]
		p += n
		if marker != 0xe1 || len(seg) < 6 || !bytes.Equal(seg[:6], []byte("Exif\x00\x00")) {
			continue
		}
		t := seg[6:]
		if len(t) < 8 {
			return 1, errors.New("invalid EXIF")
		}
		var order binary.ByteOrder
		switch string(t[:2]) {
		case "II":
			order = binary.LittleEndian
		case "MM":
			order = binary.BigEndian
		default:
			return 1, errors.New("invalid EXIF")
		}
		if order.Uint16(t[2:4]) != 42 {
			return 1, errors.New("invalid EXIF")
		}
		off := int64(order.Uint32(t[4:8]))
		if off+2 > int64(len(t)) {
			return 1, errors.New("invalid EXIF")
		}
		count := int64(order.Uint16(t[off : off+2]))
		start := off + 2
		if start+count*12 > int64(len(t)) {
			return 1, errors.New("invalid EXIF")
		}
		for i := int64(0); i < count; i++ {
			e := t[start+i*12 : start+(i+1)*12]
			if order.Uint16(e[:2]) == 0x112 {
				if order.Uint16(e[2:4]) != 3 || order.Uint32(e[4:8]) != 1 {
					return 1, errors.New("invalid EXIF orientation")
				}
				v := int(order.Uint16(e[8:10]))
				if v < 1 || v > 8 {
					return 1, errors.New("invalid EXIF orientation")
				}
				return v, nil
			}
		}
		return 1, nil
	}
	return 1, nil
}
