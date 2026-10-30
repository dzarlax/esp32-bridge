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
var renderSlots = make(chan struct{}, 1)

type RenderOptions struct {
	Layout           string
	CropLimitPercent int
	ClockShade       bool
}

func Render(ctx context.Context, r io.Reader) ([]byte, error) {
	return RenderWithOptions(ctx, r, RenderOptions{Layout: "adaptive", CropLimitPercent: 10})
}

func RenderWithOptions(ctx context.Context, r io.Reader, options RenderOptions) ([]byte, error) {
	if (options.Layout != "adaptive" && options.Layout != "contain" && options.Layout != "cover") || options.CropLimitPercent < 0 || options.CropLimitPercent > 40 {
		return nil, errors.New("invalid render options")
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case renderSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-renderSlots }()
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
	out, err := renderComposed(ctx, orientedPhoto{img: img, width: sw, height: sh, orientation: orient, p3: convertP3}, ow, oh, options)
	if err != nil || !options.ClockShade {
		return out, err
	}
	for y := 400; y < Height; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		shade := 100 - (y-400)*35/200
		for x := 0; x < Width; x++ {
			i := (y*Width + x) * 2
			v := binary.LittleEndian.Uint16(out[i : i+2])
			r, g, b := int(v>>11)*shade/100, int((v>>5)&63)*shade/100, int(v&31)*shade/100
			binary.LittleEndian.PutUint16(out[i:i+2], uint16(r<<11|g<<5|b))
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
