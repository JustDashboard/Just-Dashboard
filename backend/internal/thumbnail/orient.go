package thumbnail

import (
	"encoding/binary"
	"image"
)

// jpegOrientation reads the EXIF orientation tag (0x0112) from a JPEG's
// first bytes, or 1 (upright) when there is none or it cannot be read.
//
// A phone writes a photograph in the sensor's orientation and records how to
// turn it in this tag; a browser drawing the original honours it, so a copy
// that ignored it would show a portrait photograph lying on its side next to
// the same picture upright in the viewer. Only the tag is read, by hand,
// because the standard library has no EXIF reader: every offset is checked
// against the bytes there are, and anything unexpected is "upright".
func jpegOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return 1
		}
		marker := b[i+1]
		if marker == 0xD8 || (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01 {
			i += 2
			continue
		}
		if marker == 0xDA || marker == 0xD9 {
			// Image data or the end: no EXIF came before it.
			return 1
		}
		length := int(binary.BigEndian.Uint16(b[i+2:]))
		if length < 2 || i+2+length > len(b) {
			return 1
		}
		segment := b[i+4 : i+2+length]
		if marker == 0xE1 && len(segment) >= 6 && string(segment[:6]) == "Exif\x00\x00" {
			return tiffOrientation(segment[6:])
		}
		i += 2 + length
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(t[2:]) != 42 {
		return 1
	}
	ifd := int(order.Uint32(t[4:]))
	if ifd < 8 || ifd+2 > len(t) {
		return 1
	}
	count := int(order.Uint16(t[ifd:]))
	for n := 0; n < count; n++ {
		at := ifd + 2 + n*12
		if at+12 > len(t) {
			return 1
		}
		if order.Uint16(t[at:]) != 0x0112 {
			continue
		}
		// SHORT, one value, stored in the first two bytes of the value field.
		if order.Uint16(t[at+2:]) != 3 {
			return 1
		}
		v := int(order.Uint16(t[at+8:]))
		if v < 1 || v > 8 {
			return 1
		}
		return v
	}
	return 1
}

// Orient turns img upright by an EXIF orientation value: 2 mirrors, 3 turns
// half round, 4 flips, 5 to 8 are the quarter turns with and without a mirror.
func Orient(img *image.NRGBA, orientation int) *image.NRGBA {
	if orientation < 2 || orientation > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	ow, oh := w, h
	if orientation >= 5 {
		ow, oh = h, w
	}
	out := image.NewNRGBA(image.Rect(0, 0, ow, oh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch orientation {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			s := img.PixOffset(b.Min.X+x, b.Min.Y+y)
			d := out.PixOffset(dx, dy)
			copy(out.Pix[d:d+4], img.Pix[s:s+4])
		}
	}
	return out
}
