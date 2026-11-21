package thumbnail

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// halves is a w×h picture, red on its left half and blue on its right.
func halves(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBA{R: 255, A: 255}
			if x >= w/2 {
				c = color.NRGBA{B: 255, A: 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withOrientation puts an EXIF segment carrying an orientation tag straight
// after a JPEG's start marker, the way a phone writes one.
func withOrientation(jpg []byte, orientation uint16, order binary.ByteOrder) []byte {
	tiff := &bytes.Buffer{}
	if order == binary.LittleEndian {
		tiff.WriteString("II")
	} else {
		tiff.WriteString("MM")
	}
	_ = binary.Write(tiff, order, uint16(42))
	_ = binary.Write(tiff, order, uint32(8))
	_ = binary.Write(tiff, order, uint16(1))
	_ = binary.Write(tiff, order, uint16(0x0112))
	_ = binary.Write(tiff, order, uint16(3))
	_ = binary.Write(tiff, order, uint32(1))
	_ = binary.Write(tiff, order, orientation)
	_ = binary.Write(tiff, order, uint16(0))
	_ = binary.Write(tiff, order, uint32(0))
	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	segment := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(segment[2:], uint16(len(payload)+2))
	out := append([]byte{}, jpg[:2]...)
	out = append(out, segment...)
	out = append(out, payload...)
	return append(out, jpg[2:]...)
}

func decode(t *testing.T, thumb *Thumbnail) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(thumb.Data))
	if err != nil {
		t.Fatalf("the copy does not decode: %v", err)
	}
	return img
}

func near(c color.Color, r, g, b uint8) bool {
	cr, cg, cb, _ := c.RGBA()
	d := func(a uint32, b uint8) bool { return int(a>>8)-int(b) < 40 && int(b)-int(a>>8) < 40 }
	return d(cr, r) && d(cg, g) && d(cb, b)
}

func TestEachFormatMakesASmallCopy(t *testing.T) {
	ctx := context.Background()
	var gifData bytes.Buffer
	palette := color.Palette{color.NRGBA{R: 255, A: 255}, color.NRGBA{B: 255, A: 255}}
	frame := image.NewPaletted(image.Rect(0, 0, 600, 300), palette)
	for y := 0; y < 300; y++ {
		for x := 300; x < 600; x++ {
			frame.SetColorIndex(x, y, 1)
		}
	}
	if err := gif.EncodeAll(&gifData, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	transparent := image.NewNRGBA(image.Rect(0, 0, 600, 300))
	transparent.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 128})

	for _, tc := range []struct {
		name, want string
		data       []byte
	}{
		{"png", "image/jpeg", encodePNG(t, halves(600, 300))},
		{"jpeg", "image/jpeg", encodeJPEG(t, halves(600, 300))},
		{"gif", "image/jpeg", gifData.Bytes()},
		{"png with transparency", "image/png", encodePNG(t, transparent)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			thumb, err := Make(ctx, bytes.NewReader(tc.data), Edge)
			if err != nil {
				t.Fatal(err)
			}
			if thumb.ContentType != tc.want {
				t.Fatalf("content type %q, want %q", thumb.ContentType, tc.want)
			}
			img := decode(t, thumb)
			if got := img.Bounds().Size(); got != image.Pt(256, 128) {
				t.Fatalf("copy is %v, want 256×128 (the longest side at Edge, the shape kept)", got)
			}
			if tc.name != "png with transparency" {
				if !near(img.At(10, 64), 255, 0, 0) || !near(img.At(245, 64), 0, 0, 255) {
					t.Fatalf("the copy is not the picture: %v %v", img.At(10, 64), img.At(245, 64))
				}
			}
		})
	}
}

func TestASmallPictureKeepsItsSize(t *testing.T) {
	thumb, err := Make(context.Background(), bytes.NewReader(encodePNG(t, halves(32, 16))), Edge)
	if err != nil {
		t.Fatal(err)
	}
	if got := decode(t, thumb).Bounds().Size(); got != image.Pt(32, 16) {
		t.Fatalf("a 32×16 icon came back %v", got)
	}
}

// pngHeader is a PNG that declares a canvas and holds no pixels: the first
// thing a decompression bomb does.
func pngHeader(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6
	chunk := append([]byte("IHDR"), ihdr...)
	_ = binary.Write(&b, binary.BigEndian, uint32(13))
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

func TestAnEnormousCanvasIsNeverDecoded(t *testing.T) {
	_, err := Make(context.Background(), bytes.NewReader(pngHeader(100_000, 100_000)), Edge)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a 10-gigapixel canvas: %v, want ErrTooLarge", err)
	}
	// Just over the limit is over it.
	_, err = Make(context.Background(), bytes.NewReader(pngHeader(MaxPixels/1000+1, 1000)), Edge)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("just over MaxPixels: %v, want ErrTooLarge", err)
	}
}

func TestTruncatedAndHostileFilesAreRefusedNotPanicked(t *testing.T) {
	full := encodeJPEG(t, halves(600, 300))
	for name, data := range map[string][]byte{
		"empty":            {},
		"not a picture":    []byte("#!/bin/sh\nrm -rf /\n"),
		"truncated jpeg":   full[:len(full)/3],
		"header only":      pngHeader(64, 64),
		"html called .png": []byte("<html><script>alert(1)</script></html>"),
		"bad exif offsets": withBrokenExif(full),
	} {
		t.Run(name, func(t *testing.T) {
			thumb, err := Make(context.Background(), bytes.NewReader(data), Edge)
			if name == "bad exif offsets" {
				// The picture is fine; the tag is unreadable, so it is upright.
				if err != nil {
					t.Fatalf("a broken EXIF block refused the picture: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("made a %d-byte copy of a broken file", len(thumb.Data))
			}
		})
	}
}

func withBrokenExif(jpg []byte) []byte {
	payload := append([]byte("Exif\x00\x00MM\x00\x2a"), 0xFF, 0xFF, 0xFF, 0xF0)
	segment := []byte{0xFF, 0xE1, 0, byte(len(payload) + 2)}
	out := append([]byte{}, jpg[:2]...)
	out = append(out, segment...)
	out = append(out, payload...)
	return append(out, jpg[2:]...)
}

func TestACancelledRequestStopsTheDecode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Make(ctx, bytes.NewReader(encodeJPEG(t, halves(600, 300))), Edge); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled decode: %v", err)
	}
}

func TestOrientationIsHonoured(t *testing.T) {
	src := encodeJPEG(t, halves(400, 200))
	for _, order := range []binary.ByteOrder{binary.BigEndian, binary.LittleEndian} {
		// 6: the camera was turned a quarter clockwise, so the picture's left
		// edge is the top of what was seen.
		thumb, err := Make(context.Background(), bytes.NewReader(withOrientation(src, 6, order)), Edge)
		if err != nil {
			t.Fatal(err)
		}
		img := decode(t, thumb)
		if got := img.Bounds().Size(); got != image.Pt(128, 256) {
			t.Fatalf("turned copy is %v, want 128×256", got)
		}
		if !near(img.At(64, 10), 255, 0, 0) || !near(img.At(64, 245), 0, 0, 255) {
			t.Fatalf("orientation 6 drew %v at the top and %v at the foot", img.At(64, 10), img.At(64, 245))
		}
	}
	// 3: half round, so blue comes first.
	thumb, err := Make(context.Background(), bytes.NewReader(withOrientation(src, 3, binary.BigEndian)), Edge)
	if err != nil {
		t.Fatal(err)
	}
	if img := decode(t, thumb); !near(img.At(10, 64), 0, 0, 255) {
		t.Fatalf("orientation 3 drew %v on the left", img.At(10, 64))
	}
	// Every value turns a picture into the shape it should have.
	for v := 1; v <= 8; v++ {
		got := Orient(halves(4, 2), v).Bounds().Size()
		want := image.Pt(4, 2)
		if v >= 5 {
			want = image.Pt(2, 4)
		}
		if got != want {
			t.Fatalf("orientation %d: %v, want %v", v, got, want)
		}
	}
}

func writeFile(t *testing.T, path string, data []byte) (*os.File, os.FileInfo) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return f, info
}

func TestTheCacheKeepsCopiesRootOnlyAndRemakesAChangedFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "thumbnails")
	cache := New(dir, 0)
	pics := t.TempDir()
	path := filepath.Join(pics, "photo.jpg")
	f, info := writeFile(t, path, encodeJPEG(t, halves(600, 300)))

	first, err := cache.Get(context.Background(), f, info)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("cache directory mode %v (%v), want 0700", st.Mode().Perm(), err)
	}
	kept, _ := os.ReadDir(dir)
	if len(kept) != 1 {
		t.Fatalf("%d files kept, want 1", len(kept))
	}
	keptInfo, _ := kept[0].Info()
	if keptInfo.Mode().Perm()&0o077 != 0 {
		t.Fatalf("a kept copy is %v, readable by others", keptInfo.Mode().Perm())
	}
	if kept[0].Name() != Key(info, Edge) {
		t.Fatalf("kept under %q, want the file's identity key", kept[0].Name())
	}

	// A second ask is served from the kept copy, even with the source gone.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	again, err := cache.Get(context.Background(), f, info)
	if err != nil || !bytes.Equal(again.Data, first.Data) {
		t.Fatalf("second ask: %v", err)
	}

	// The file changed: new bytes, a later time. A new copy, of the new picture.
	f2, info2 := writeFile(t, path, encodeJPEG(t, halves(300, 600)))
	_ = os.Chtimes(path, time.Now().Add(time.Minute), time.Now().Add(time.Minute))
	info2, _ = os.Stat(path)
	if Key(info2, Edge) == Key(info, Edge) {
		t.Fatal("a changed file kept its key")
	}
	changed, err := cache.Get(context.Background(), f2, info2)
	if err != nil {
		t.Fatal(err)
	}
	if got := decode(t, changed).Bounds().Size(); got != image.Pt(128, 256) {
		t.Fatalf("the changed file's copy is %v, want the new 128×256", got)
	}

	// A new cache over the same directory finds what is kept.
	reopened := New(dir, 0)
	if reopened.Size() != 0 {
		t.Fatal("size before first use should be unread")
	}
	if _, ok := reopened.lookup(Key(info2, Edge)); !ok {
		t.Fatal("a restarted cache did not find its kept copies")
	}
}

func TestTheCacheIsBoundedAndForgetsTheLeastRecentlyUsedFirst(t *testing.T) {
	dir := t.TempDir()
	clock := time.Unix(1_700_000_000, 0)
	pics := t.TempDir()
	var files []*os.File
	var infos []os.FileInfo
	for i := 0; i < 6; i++ {
		img := halves(300+i*10, 300)
		f, info := writeFile(t, filepath.Join(pics, string(rune('a'+i))+".png"), encodePNG(t, img))
		files, infos = append(files, f), append(infos, info)
	}
	one, err := Make(context.Background(), files[0], Edge)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = files[0].Seek(0, 0)
	// Room for about three copies.
	cache := New(dir, int64(len(one.Data))*7/2)
	cache.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	for i := 0; i < 3; i++ {
		if _, err := cache.Get(context.Background(), files[i], infos[i]); err != nil {
			t.Fatal(err)
		}
	}
	// a is used again, so b is now the oldest.
	if _, ok := cache.lookup(Key(infos[0], Edge)); !ok {
		t.Fatal("a was not kept")
	}
	for i := 3; i < 6; i++ {
		if _, err := cache.Get(context.Background(), files[i], infos[i]); err != nil {
			t.Fatal(err)
		}
		if cache.Size() > cache.maxBytes {
			t.Fatalf("the cache holds %d bytes, over its bound of %d", cache.Size(), cache.maxBytes)
		}
	}
	if _, ok := cache.lookup(Key(infos[1], Edge)); ok {
		t.Fatal("b, the least recently used, survived")
	}
	if _, ok := cache.lookup(Key(infos[5], Edge)); !ok {
		t.Fatal("f, the newest, was evicted")
	}
	kept, _ := os.ReadDir(dir)
	var total int64
	for _, k := range kept {
		info, _ := k.Info()
		total += info.Size()
	}
	if total != cache.Size() || total > cache.maxBytes {
		t.Fatalf("on disk %d, counted %d, bound %d", total, cache.Size(), cache.maxBytes)
	}
}

func TestOnlyDecodableRegularFilesAreTried(t *testing.T) {
	cache := New(t.TempDir(), 0)
	pics := t.TempDir()
	f, info := writeFile(t, filepath.Join(pics, "logo.webp"), []byte("RIFF....WEBP"))
	if _, err := cache.Get(context.Background(), f, info); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("webp: %v, want ErrUnsupported (the page draws it itself)", err)
	}
	dirFile, err := os.Open(pics)
	if err != nil {
		t.Fatal(err)
	}
	defer dirFile.Close()
	dirInfo, _ := dirFile.Stat()
	if _, err := cache.Get(context.Background(), dirFile, dirInfo); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("a directory: %v", err)
	}
}

func TestManyAsksForOneNewPictureDecodeItOnce(t *testing.T) {
	cache := New(t.TempDir(), 0)
	path := filepath.Join(t.TempDir(), "p.png")
	if err := os.WriteFile(path, encodePNG(t, halves(600, 300)), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			f, err := os.Open(path)
			if err != nil {
				errs <- err
				return
			}
			defer f.Close()
			info, _ := f.Stat()
			_, err = cache.Get(context.Background(), f, info)
			errs <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	kept, _ := os.ReadDir(cache.dir)
	if len(kept) != 1 {
		t.Fatalf("%d files after eight asks for one picture, want 1", len(kept))
	}
}
