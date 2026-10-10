package netx

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/boombuler/barcode/qr"
)

func wgB64Hex(t *testing.T, h string) string {
	t.Helper()
	raw, err := hex.DecodeString(h)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// The public key is derived with X25519 from the curve's base point. The two
// vectors are RFC 7748 §6.1's Alice and Bob: a private scalar and the public
// value the RFC computes from it, not anything this code produced.
func TestWGPublicKeyFromRFC7748Vectors(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, private, public string }{
		{"alice", "77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a", "8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a"},
		{"bob", "5dab087e624a8a4b79e17f8b83800ee66f3bb1292618b6fd1c2f8b27ff88e0eb", "de9edb7d7b7dc1b4d35b61c2ece435373f8343c85b78674dadfc7e146f882b4f"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := wgPublicKey(wgB64Hex(t, tc.private))
			if err != nil {
				t.Fatal(err)
			}
			if want := wgB64Hex(t, tc.public); got != want {
				t.Fatalf("public key = %s, want %s", got, want)
			}
		})
	}
}

func TestNewWGKeyPairIsClampedAndDerivable(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		priv, pub, err := newWGKeyPair()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := decodeWGKey(priv)
		if err != nil {
			t.Fatal(err)
		}
		if raw[0]&7 != 0 || raw[31]&0x80 != 0 || raw[31]&0x40 == 0 {
			t.Fatalf("private key %x is not clamped", raw)
		}
		again, err := wgPublicKey(priv)
		if err != nil || again != pub {
			t.Fatalf("public key does not derive from the private key: %v", err)
		}
		if seen[priv] {
			t.Fatal("two keys were the same")
		}
		seen[priv] = true
		if len(priv) != 44 || len(pub) != 44 {
			t.Fatalf("keys should be 44 characters of base64, got %d and %d", len(priv), len(pub))
		}
	}
	psk, err := newWGPresharedKey()
	if err != nil || !validWGKey(psk) {
		t.Fatalf("preshared key %q: %v", psk, err)
	}
}

func TestDecodeWGKeyRefusesWrongShapes(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"", "not base64!", base64.StdEncoding.EncodeToString(make([]byte, 31)), base64.StdEncoding.EncodeToString(make([]byte, 33))} {
		if validWGKey(bad) {
			t.Errorf("%q passed as a key", bad)
		}
		if _, err := wgPublicKey(bad); err == nil {
			t.Errorf("a public key was derived from %q", bad)
		}
	}
}

func TestQRDataURLIsAPNGOfTheRightShape(t *testing.T) {
	t.Parallel()
	config := "[Interface]\nPrivateKey = " + strings.Repeat("A", 43) + "=\nAddress = 10.8.0.2/32\n\n[Peer]\nPublicKey = x\nAllowedIPs = 0.0.0.0/0, ::/0\n"
	url, err := qrDataURL(config)
	if err != nil {
		t.Fatal(err)
	}
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(url, prefix) {
		t.Fatalf("not a PNG data URL: %.40s", url)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, prefix))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("PNG magic is missing: %x", raw[:8])
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if b.Dx() != b.Dy() || b.Dx() < 200 || b.Dx() > 400 {
		t.Fatalf("QR is %dx%d, want a square near 384", b.Dx(), b.Dy())
	}
	// A phone needs the quiet margin: the corner is white, and somewhere
	// inside is black.
	if r, g, bl, _ := img.At(0, 0).RGBA(); r != 0xffff || g != 0xffff || bl != 0xffff {
		t.Fatal("the margin is not white")
	}
	dark := false
	for y := 0; y < b.Dy() && !dark; y++ {
		for x := 0; x < b.Dx(); x++ {
			if r, _, _, _ := img.At(x, y).RGBA(); r == 0 {
				dark = true
				break
			}
		}
	}
	if !dark {
		t.Fatal("the code has no dark modules")
	}
}

func TestQRDataURLRefusesWhatCannotFit(t *testing.T) {
	t.Parallel()
	if _, err := qrDataURL(strings.Repeat("x", 5000)); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want a refusal that says it is too large", err)
	}
}

// The picture is the encoder's module grid, drawn a whole number of pixels
// to the module with a four-module margin: every module's centre is the
// colour the encoder gave it, which is what a scanner reads.
func TestQRImageIsTheEncodersGridWithAQuietZone(t *testing.T) {
	t.Parallel()
	config := "[Interface]\nPrivateKey = abc\nAddress = 10.8.0.2/32\n"
	url, err := qrDataURL(config)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:image/png;base64,"))
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	code, err := qr.Encode(config, qr.M, qr.Auto)
	if err != nil {
		t.Fatal(err)
	}
	modules := code.Bounds().Dx()
	total := modules + 2*qrQuietModules
	if img.Bounds().Dx()%total != 0 {
		t.Fatalf("image is %d pixels for %d modules: not a whole number of pixels to the module", img.Bounds().Dx(), total)
	}
	scale := img.Bounds().Dx() / total
	for my := 0; my < total; my++ {
		for mx := 0; mx < total; mx++ {
			want := color.Color(color.White)
			if x, y := mx-qrQuietModules, my-qrQuietModules; x >= 0 && y >= 0 && x < modules && y < modules {
				want = code.At(x, y)
			}
			wr, _, _, _ := want.RGBA()
			gr, _, _, _ := img.At(mx*scale+scale/2, my*scale+scale/2).RGBA()
			if wr != gr {
				t.Fatalf("module (%d,%d) is %v in the image, %v from the encoder", mx, my, gr, wr)
			}
		}
	}
}
