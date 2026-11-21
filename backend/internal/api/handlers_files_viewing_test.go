package api

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writePicture(t *testing.T, path string, w, h int, fill color.Color) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, fill)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A thumbnail goes out with the raw route's posture: inline, a closed type,
// the sandbox policy and a short private cache. And it is small.
func TestThumbnailIsSmallAndServedLikeTheRawRoute(t *testing.T) {
	c, s := newClient(t)
	root := fileFixture(t, s)
	photo := filepath.Join(root, "photo.png")
	writePicture(t, photo, 1200, 800, color.NRGBA{R: 200, G: 40, B: 40, A: 255})

	w := c.do(http.MethodGet, query("/api/v1/files/thumbnail", map[string]string{"path": photo, "v": "1"}), "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("thumbnail: %d %s", w.Code, w.Body.String())
	}
	for header, want := range map[string]string{
		"Content-Type":            "image/jpeg",
		"Cache-Control":           "private, max-age=30",
		"Content-Security-Policy": "default-src 'none'; sandbox; frame-ancestors 'none'",
	} {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if got := w.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline") {
		t.Errorf("Content-Disposition = %q, want inline", got)
	}
	if w.Header().Get("Last-Modified") == "" {
		t.Error("no Last-Modified, so the browser cannot revalidate a kept copy")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
	if err != nil || cfg.Width != 256 || cfg.Height != 170 {
		t.Fatalf("copy is %dx%d (%v), want 256x170", cfg.Width, cfg.Height, err)
	}

	// Kept root-only under the data directory.
	dir := filepath.Join(s.Cfg.DataDir, "thumbnails")
	st, err := os.Stat(dir)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("thumbnail directory: %v %v, want 0700", st, err)
	}
	kept, _ := os.ReadDir(dir)
	if len(kept) != 1 {
		t.Fatalf("%d copies kept, want 1", len(kept))
	}

	// A changed picture is a new copy of the new picture.
	writePicture(t, photo, 400, 800, color.NRGBA{G: 200, A: 255})
	later := time.Now().Add(time.Minute)
	_ = os.Chtimes(photo, later, later)
	w = c.do(http.MethodGet, query("/api/v1/files/thumbnail", map[string]string{"path": photo, "v": "2"}), "", nil)
	cfg, _, err = image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
	if w.Code != http.StatusOK || err != nil || cfg.Width != 128 || cfg.Height != 256 {
		t.Fatalf("after a change: %d, %dx%d (%v), want the new 128x256", w.Code, cfg.Width, cfg.Height, err)
	}
}

// Only pictures it can decode, and never a path outside the roots, whatever
// the cache holds.
func TestThumbnailRefusals(t *testing.T) {
	c, s := newClient(t)
	root := fileFixture(t, s)

	for name, tc := range map[string]struct {
		path string
		code int
	}{
		"html":          {filepath.Join(root, "index.html"), http.StatusUnsupportedMediaType},
		"config":        {filepath.Join(root, "etc/nginx/nginx.conf"), http.StatusUnsupportedMediaType},
		"svg":           {filepath.Join(root, "logo.svg"), http.StatusUnsupportedMediaType},
		"missing":       {filepath.Join(root, "nothing.png"), http.StatusNotFound},
		"no path":       {"", http.StatusBadRequest},
		"a directory":   {filepath.Join(root, "etc.png"), http.StatusBadRequest},
		"not a picture": {filepath.Join(root, "fake.jpg"), http.StatusUnsupportedMediaType},
		"a huge canvas": {filepath.Join(root, "bomb.png"), http.StatusRequestEntityTooLarge},
		"traversal":     {filepath.Join(root, "../../etc/passwd.png"), http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			switch name {
			case "a directory":
				if err := os.Mkdir(tc.path, 0o755); err != nil {
					t.Fatal(err)
				}
			case "not a picture":
				if err := os.WriteFile(tc.path, []byte("#!/bin/sh\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "a huge canvas":
				// A PNG signature and a header claiming 100000×100000, and nothing else.
				chunk := []byte("IHDR\x00\x01\x86\xa0\x00\x01\x86\xa0\x08\x06\x00\x00\x00")
				header := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0d"), chunk...)
				header = binary.BigEndian.AppendUint32(header, crc32.ChecksumIEEE(chunk))
				if err := os.WriteFile(tc.path, header, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			w := c.do(http.MethodGet, query("/api/v1/files/thumbnail", map[string]string{"path": tc.path}), "", nil)
			if w.Code != tc.code {
				t.Fatalf("%s answered %d %s, want %d", tc.path, w.Code, w.Body.String(), tc.code)
			}
		})
	}

	// A picture outside the roots is refused, directly or through a link in
	// a root, even once a copy of the same bytes was made from inside: the
	// cache never answers for a path.
	outside := t.TempDir()
	writePicture(t, filepath.Join(outside, "same.png"), 300, 300, color.NRGBA{B: 255, A: 255})
	inside := filepath.Join(root, "same.png")
	writePicture(t, inside, 300, 300, color.NRGBA{B: 255, A: 255})
	if w := c.do(http.MethodGet, query("/api/v1/files/thumbnail", map[string]string{"path": inside}), "", nil); w.Code != http.StatusOK {
		t.Fatalf("inside: %d", w.Code)
	}
	link := filepath.Join(root, "escape.png")
	if err := os.Symlink(filepath.Join(outside, "same.png"), link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(outside, "same.png"), link} {
		w := c.do(http.MethodGet, query("/api/v1/files/thumbnail", map[string]string{"path": path}), "", nil)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s answered %d, want 403 outside_root", path, w.Code)
		}
	}
}
