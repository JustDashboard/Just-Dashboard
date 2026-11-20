// Package thumbnail makes the small copy of a picture the Files listing draws,
// once, and keeps it.
//
// The listing used to draw a picture by fetching the whole file and letting the
// browser shrink it, and gave up above 4 MB: a folder of forty 12 MB
// photographs was either 480 MB over a VPN or forty generic icons. A copy a few
// hundred pixels across is tens of kilobytes, made with the standard library's
// decoders (PNG, JPEG, GIF), so the format a server cannot decode keeps the
// browser-drawn behaviour the page had.
//
// Everything about making one is bounded, because a picture is input from
// whoever put it on the server: a file declaring more than MaxPixels is not
// decoded at all (a 40-byte PNG can claim a canvas of 4 GiB), a decode reads
// through the request's context and stops when it is gone, and only a few are
// made at once. The copies live in one root-only directory under the data
// directory, named by a digest of the file's identity (device, inode, size and
// modification time) and the size asked for, so a changed file is a new name
// and the old copy is simply never asked for again. The directory is bounded
// in total size and the least recently used copies go first.
//
// The cache never decides whether a caller may see a file: it is handed a file
// the caller already opened through the Files resolver, and keys on what that
// open file is, never on a path.
package thumbnail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	// Registered for image.Decode and image.DecodeConfig.
	_ "image/gif"
)

var (
	// ErrUnsupported is a picture in a format this package does not decode,
	// or a file that is not a picture at all.
	ErrUnsupported = errors.New("no thumbnail can be made of this file")
	// ErrTooLarge is a picture whose canvas is over MaxPixels or whose file is
	// over MaxSourceBytes: it is not decoded.
	ErrTooLarge = errors.New("this picture is too large to make a thumbnail of")
)

const (
	// MaxPixels is the largest canvas decoded: 50 megapixels covers every
	// camera a phone ships with at its default setting, and keeps one decode
	// under 200 MiB however the format stores its pixels.
	MaxPixels = 50_000_000
	// MaxSourceBytes is the largest file read. A JPEG of MaxPixels is rarely
	// more than 40 MB; past this the file is something other than a picture.
	MaxSourceBytes = 128 << 20
	// DefaultMaxBytes is what the copies may take on disk in total. At tens of
	// kilobytes each, that is several thousand pictures.
	DefaultMaxBytes = 256 << 20
	// Edge is the longest side of a copy, in pixels: a tile at twice the
	// screen's density, and the viewer's strip.
	Edge = 256
	// workers is how many decodes run at once, across every request.
	workers = 2
)

// Decodable reports whether a name is one of the formats a copy is made of.
// The page asks the same question by extension (`thumbnailUrl` in media.ts).
func Decodable(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif":
		return true
	}
	return false
}

// Thumbnail is one made copy.
type Thumbnail struct {
	Data        []byte
	ContentType string
}

// Cache makes copies and keeps them in a directory.
type Cache struct {
	dir      string
	maxBytes int64
	sem      chan struct{}

	mu      sync.Mutex
	loaded  bool
	entries map[string]*entry
	total   int64
	// making holds a key while it is being made, so forty requests for one
	// new picture decode it once.
	making map[string]chan struct{}
	now    func() time.Time
}

type entry struct {
	size int64
	used time.Time
}

// New returns a cache over dir, which is made root-only on first use. A
// maxBytes of zero or less is DefaultMaxBytes.
func New(dir string, maxBytes int64) *Cache {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Cache{
		dir:      dir,
		maxBytes: maxBytes,
		sem:      make(chan struct{}, workers),
		entries:  map[string]*entry{},
		making:   map[string]chan struct{}{},
		now:      time.Now,
	}
}

// Key names a copy: what the open file is, when it last changed, and the
// size asked for. A path is deliberately not part of it.
func Key(info os.FileInfo, edge int) string {
	var dev, ino uint64
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		dev, ino = uint64(st.Dev), st.Ino
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%d:%d:%d:%d:%d", dev, ino, info.Size(),
		info.ModTime().UnixNano(), edge))
	return hex.EncodeToString(sum[:20])
}

// Get returns the copy of the opened file f, making it if it is not kept.
// The caller opened f through the Files resolver and closes it.
func (c *Cache) Get(ctx context.Context, f *os.File, info os.FileInfo) (*Thumbnail, error) {
	if !info.Mode().IsRegular() || !Decodable(info.Name()) {
		return nil, ErrUnsupported
	}
	if info.Size() > MaxSourceBytes {
		return nil, ErrTooLarge
	}
	key := Key(info, Edge)
	for {
		if thumb, ok := c.lookup(key); ok {
			return thumb, nil
		}
		c.mu.Lock()
		wait, busy := c.making[key]
		if !busy {
			c.making[key] = make(chan struct{})
		}
		c.mu.Unlock()
		if !busy {
			break
		}
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	defer func() {
		c.mu.Lock()
		close(c.making[key])
		delete(c.making, key)
		c.mu.Unlock()
	}()

	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	thumb, err := Make(ctx, f, Edge)
	<-c.sem
	if err != nil {
		return nil, err
	}
	// A copy that cannot be kept is still served: the disk being full is no
	// reason to show an icon instead of a picture already made.
	_ = c.store(key, thumb)
	return thumb, nil
}

func (c *Cache) path(key string) string {
	return filepath.Join(c.dir, key)
}

// ensure makes the directory root-only and reads what is already in it, once.
func (c *Cache) ensure() error {
	if c.loaded {
		return nil
	}
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return err
	}
	// MkdirAll leaves an existing directory's mode alone.
	if err := os.Chmod(c.dir, 0o700); err != nil {
		return err
	}
	items, err := os.ReadDir(c.dir)
	if err != nil {
		return err
	}
	for _, item := range items {
		name := item.Name()
		if strings.HasPrefix(name, ".") {
			// A temporary file a crash left behind.
			_ = os.Remove(filepath.Join(c.dir, name))
			continue
		}
		info, err := item.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		c.entries[name] = &entry{size: info.Size(), used: info.ModTime()}
		c.total += info.Size()
	}
	c.loaded = true
	c.evictLocked()
	return nil
}

func (c *Cache) lookup(key string) (*Thumbnail, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ensure() != nil {
		return nil, false
	}
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	data, err := os.ReadFile(c.path(key))
	if err != nil {
		c.total -= e.size
		delete(c.entries, key)
		return nil, false
	}
	e.used = c.now()
	// The use is kept on disk too, so the order survives a restart.
	_ = os.Chtimes(c.path(key), e.used, e.used)
	return &Thumbnail{Data: data, ContentType: sniff(data)}, true
}

func (c *Cache) store(key string, thumb *Thumbnail) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensure(); err != nil {
		return err
	}
	if int64(len(thumb.Data)) > c.maxBytes {
		return nil
	}
	tmp, err := os.CreateTemp(c.dir, ".new-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(thumb.Data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return errors.Join(werr, cerr)
	}
	if err := os.Rename(tmp.Name(), c.path(key)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if old, ok := c.entries[key]; ok {
		c.total -= old.size
	}
	c.entries[key] = &entry{size: int64(len(thumb.Data)), used: c.now()}
	c.total += int64(len(thumb.Data))
	c.evictLocked()
	return nil
}

// evictLocked removes the least recently used copies until the directory is
// back under nine tenths of its bound, so it is not trimmed on every write.
func (c *Cache) evictLocked() {
	if c.total <= c.maxBytes {
		return
	}
	keys := make([]string, 0, len(c.entries))
	for key := range c.entries {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return c.entries[keys[i]].used.Before(c.entries[keys[j]].used) })
	goal := c.maxBytes * 9 / 10
	for _, key := range keys {
		if c.total <= goal {
			break
		}
		if err := os.Remove(c.path(key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		c.total -= c.entries[key].size
		delete(c.entries, key)
	}
}

// Size is what the kept copies take on disk, for tests and the docs' claim.
func (c *Cache) Size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

func sniff(data []byte) string {
	if bytes.HasPrefix(data, []byte("\x89PNG")) {
		return "image/png"
	}
	return "image/jpeg"
}

// Make decodes the picture r holds and returns a copy no wider or taller
// than edge, turned upright by its orientation tag. It reads at most
// MaxSourceBytes, refuses a canvas over MaxPixels before decoding it, and
// stops when ctx does.
func Make(ctx context.Context, r io.ReadSeeker, edge int) (*Thumbnail, error) {
	head := make([]byte, 64<<10)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	head = head[:n]
	cfg, format, err := image.DecodeConfig(io.MultiReader(bytes.NewReader(head), &ctxReader{ctx, r}))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, ErrUnsupported
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return nil, ErrTooLarge
	}
	orientation := 1
	if format == "jpeg" {
		orientation = jpegOrientation(head)
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	src, _, err := image.Decode(&ctxReader{ctx, io.LimitReader(r, MaxSourceBytes)})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	small := Scale(src, edge)
	small = Orient(small, orientation)

	var out bytes.Buffer
	if opaque(src) {
		err = jpeg.Encode(&out, small, &jpeg.Options{Quality: 82})
	} else {
		err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&out, small)
	}
	if err != nil {
		return nil, err
	}
	thumb := &Thumbnail{Data: out.Bytes(), ContentType: "image/jpeg"}
	if !opaque(src) {
		thumb.ContentType = "image/png"
	}
	return thumb, nil
}

func opaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	return false
}

// Scale returns img shrunk so its longest side is at most edge, each pixel the
// average of a grid of samples over the area it covers. A picture already that
// small is copied as it is.
func Scale(img image.Image, edge int) *image.NRGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	tw, th := w, h
	if w > edge || h > edge {
		if w >= h {
			tw, th = edge, max(1, h*edge/w)
		} else {
			tw, th = max(1, w*edge/h), edge
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, tw, th))
	if tw == w && th == h {
		draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
		return dst
	}
	// Up to four samples a side per output pixel: past that the average no
	// longer changes visibly, and the cost would grow with the source.
	sx := min(4, max(1, w/tw))
	sy := min(4, max(1, h/th))
	for y := 0; y < th; y++ {
		for x := 0; x < tw; x++ {
			var r, g, bl, a uint64
			for j := 0; j < sy; j++ {
				py := b.Min.Y + ((y*sy+j)*h)/(th*sy)
				for i := 0; i < sx; i++ {
					px := b.Min.X + ((x*sx+i)*w)/(tw*sx)
					cr, cg, cb, ca := img.At(px, py).RGBA()
					r += uint64(cr)
					g += uint64(cg)
					bl += uint64(cb)
					a += uint64(ca)
				}
			}
			n := uint64(sx * sy)
			r, g, bl, a = r/n, g/n, bl/n, a/n
			o := dst.PixOffset(x, y)
			// The samples are premultiplied; NRGBA is not.
			if a > 0 {
				dst.Pix[o+0] = uint8(min(255, r*255/a))
				dst.Pix[o+1] = uint8(min(255, g*255/a))
				dst.Pix[o+2] = uint8(min(255, bl*255/a))
			}
			dst.Pix[o+3] = uint8(a >> 8)
		}
	}
	return dst
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
