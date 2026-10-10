package netx

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"

	"github.com/boombuler/barcode/qr"
)

// A QR code a phone reads needs the code and an empty margin of four modules
// around it. barcode's encoder draws the modules and no margin, and Scale
// stretches by a fraction of a module, which blurs the edges a camera has to
// find; so the code is drawn here, a whole number of pixels to the module,
// from the module grid the encoder produced.
const (
	qrTargetPixels = 384
	qrQuietModules = 4
)

// qrDataURL renders text as a PNG QR code and returns it as a data: URL the
// page can put in an <img>. Error correction M survives a scuffed screen.
func qrDataURL(text string) (string, error) {
	code, err := qr.Encode(text, qr.M, qr.Auto)
	if err != nil {
		return "", fmt.Errorf("this configuration is too large for a QR code: %w", err)
	}
	modules := code.Bounds().Dx()
	total := modules + 2*qrQuietModules
	scale := qrTargetPixels / total
	if scale < 1 {
		scale = 1
	}
	side := total * scale
	img := image.NewPaletted(image.Rect(0, 0, side, side), color.Palette{color.White, color.Black})
	for my := 0; my < modules; my++ {
		for mx := 0; mx < modules; mx++ {
			if code.At(mx, my) != color.Black {
				continue
			}
			x0 := (mx + qrQuietModules) * scale
			y0 := (my + qrQuietModules) * scale
			for y := y0; y < y0+scale; y++ {
				for x := x0; x < x0+scale; x++ {
					img.SetColorIndex(x, y, 1)
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", fmt.Errorf("encoding the QR code: %w", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
