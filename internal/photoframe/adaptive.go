package photoframe

import (
	"context"
	"encoding/binary"
	"image"

	xdraw "golang.org/x/image/draw"
)

const maxCropLossPercent = 10
const backgroundWidth, backgroundHeight = Width / 8, Height / 8
const backgroundBrightness = 70
const blurRadius, blurPasses = 4, 3

type orientedPhoto struct {
	img                        image.Image
	width, height, orientation int
	p3                         bool
}

func (p orientedPhoto) sample(x, y int) (uint32, uint32, uint32) {
	sx, sy := sourcePoint(p.orientation, x, y, p.width, p.height)
	r, g, b, _ := p.img.At(p.img.Bounds().Min.X+sx, p.img.Bounds().Min.Y+sy).RGBA()
	if p.p3 {
		return p3ToSRGB(r, g, b)
	}
	return r, g, b
}

// Compare retained area exactly, after orientation. Equality uses cover.
func cropWithinLimit(width, height int) bool {
	source := int64(width) * Height
	target := int64(height) * Width
	if source >= target {
		return 100*target >= (100-maxCropLossPercent)*source
	}
	return 100*source >= (100-maxCropLossPercent)*target
}

// Rational pixel centers keep the same scale on both axes, including tiny sources.
func coverSampling(ow, oh, width, height int) (den, step, originX, originY int64) {
	den, step = int64(2*width), int64(2*ow)
	originX = int64(ow)
	originY = int64(oh)*int64(width) - int64(ow)*int64(height) + int64(ow)
	if int64(ow)*int64(height) >= int64(oh)*int64(width) {
		den, step = int64(2*height), int64(2*oh)
		originX = int64(ow)*int64(height) - int64(oh)*int64(width) + int64(oh)
		originY = int64(oh)
	}
	return
}

func writeRGB565(out []byte, x, y int, r, g, b uint32) {
	v := uint16(r>>11)<<11 | uint16(g>>10)<<5 | uint16(b>>11)
	offset := (y*Width + x) * 2
	binary.LittleEndian.PutUint16(out[offset:offset+2], v)
}

func renderCover(ctx context.Context, p orientedPhoto, ow, oh int) ([]byte, error) {
	out := make([]byte, FrameBytes)
	den, step, originX, originY := coverSampling(ow, oh, Width, Height)
	for y := 0; y < Height; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		oy := int((originY + int64(y)*step) / den)
		for x := 0; x < Width; x++ {
			ox := int((originX + int64(x)*step) / den)
			r, g, b := p.sample(ox, oy)
			writeRGB565(out, x, y, r, g, b)
		}
	}
	return out, nil
}

func containRect(ow, oh int) image.Rectangle {
	width, height := Width, Height
	if int64(ow)*Height >= int64(oh)*Width {
		height = max(1, int(int64(Width)*int64(oh)/int64(ow)))
	} else {
		width = max(1, int(int64(Height)*int64(ow)/int64(oh)))
	}
	left, top := (Width-width)/2, (Height-height)/2
	return image.Rect(left, top, left+width, top+height)
}

func renderAdaptive(ctx context.Context, p orientedPhoto, ow, oh int) ([]byte, error) {
	if cropWithinLimit(ow, oh) {
		return renderCover(ctx, p, ow, oh)
	}
	small := image.NewRGBA(image.Rect(0, 0, backgroundWidth, backgroundHeight))
	den, step, originX, originY := coverSampling(ow, oh, backgroundWidth, backgroundHeight)
	for y := 0; y < backgroundHeight; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := 0; x < backgroundWidth; x++ {
			r, g, b := p.sample(int((originX+int64(x)*step)/den), int((originY+int64(y)*step)/den))
			i := small.PixOffset(x, y)
			small.Pix[i], small.Pix[i+1], small.Pix[i+2], small.Pix[i+3] = uint8(r>>8), uint8(g>>8), uint8(b>>8), 255
		}
	}
	scratch := image.NewRGBA(small.Bounds())
	for range blurPasses {
		if err := blurAxis(ctx, small, scratch, true); err != nil {
			return nil, err
		}
		if err := blurAxis(ctx, scratch, small, false); err != nil {
			return nil, err
		}
	}
	background := image.NewRGBA(image.Rect(0, 0, Width, Height))
	xdraw.BiLinear.Scale(background, background.Bounds(), small, small.Bounds(), xdraw.Src, nil)
	out := make([]byte, FrameBytes)
	for y := 0; y < Height; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := 0; x < Width; x++ {
			i := background.PixOffset(x, y)
			writeRGB565(out, x, y, uint32(background.Pix[i])*257*backgroundBrightness/100, uint32(background.Pix[i+1])*257*backgroundBrightness/100, uint32(background.Pix[i+2])*257*backgroundBrightness/100)
		}
	}
	// Paint the sharp full photo after the background, keeping its colors intact.
	rect := containRect(ow, oh)
	for y := 0; y < rect.Dy(); y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		oy := int((2*int64(y) + 1) * int64(oh) / (2 * int64(rect.Dy())))
		for x := 0; x < rect.Dx(); x++ {
			ox := int((2*int64(x) + 1) * int64(ow) / (2 * int64(rect.Dx())))
			r, g, b := p.sample(ox, oy)
			writeRGB565(out, rect.Min.X+x, rect.Min.Y+y, r, g, b)
		}
	}
	return out, nil
}

// Three separable box passes approximate a strong Gaussian blur on the small
// background. Clamp at image edges so blur never introduces black padding.
func blurAxis(ctx context.Context, src, dst *image.RGBA, horizontal bool) error {
	bounds := src.Bounds()
	window := 2*blurRadius + 1
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b := 0, 0, 0
			for k := -blurRadius; k <= blurRadius; k++ {
				sx, sy := x, y
				if horizontal {
					sx = max(bounds.Min.X, min(bounds.Max.X-1, x+k))
				} else {
					sy = max(bounds.Min.Y, min(bounds.Max.Y-1, y+k))
				}
				i := src.PixOffset(sx, sy)
				r += int(src.Pix[i])
				g += int(src.Pix[i+1])
				b += int(src.Pix[i+2])
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = uint8((r+window/2)/window), uint8((g+window/2)/window), uint8((b+window/2)/window), 255
		}
	}
	return nil
}
