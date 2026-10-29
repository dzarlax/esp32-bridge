package photoframe

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestRuntimeRenderModesAndThreshold(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 150, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 150; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y * 2), 80, 255})
		}
	}
	var source bytes.Buffer
	if e := png.Encode(&source, img); e != nil {
		t.Fatal(e)
	}
	render := func(mode string, percent int) []byte {
		t.Helper()
		out, e := RenderWithOptions(context.Background(), bytes.NewReader(source.Bytes()), RenderOptions{Layout: mode, CropLimitPercent: percent})
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	adaptive := render("adaptive", 10)
	cover := render("cover", 10)
	contain := render("contain", 10)
	if !bytes.Equal(adaptive, contain) || bytes.Equal(adaptive, cover) {
		t.Fatal("threshold did not preserve complete source")
	}
	if !bytes.Equal(render("adaptive", 20), cover) {
		t.Fatal("higher limit did not allow cover")
	}
	if out, e := RenderWithOptions(context.Background(), bytes.NewReader(source.Bytes()), RenderOptions{Layout: "unknown", CropLimitPercent: 10}); e == nil || out != nil {
		t.Fatal("invalid render mode accepted")
	}
}
