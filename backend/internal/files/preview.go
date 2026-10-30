package files

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"image"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	// Registered for their DecodeConfig, which reads a header rather than the
	// whole image — the dimensions of a 40 MB photograph cost a few hundred
	// bytes of read.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// Preview is what one click on a row gets you.
//
// Opening a file used to mean loading it into Monaco, which is the right
// answer for a config file and the wrong one for everything else: a 200 MB
// log, a JPEG, a tarball and a binary all arrived at the same sheet, three of
// them to be told the editor would not have them. Selecting a row now asks
// this instead, and it answers the question the click was actually asking —
// what *is* this — for every kind of thing a server holds.
//
// It never loads the whole file. Text comes back as a head, an archive as its
// first entries, an image as its dimensions and a URL the browser fetches
// itself.
type Preview struct {
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	Kind     string    `json:"kind"`
	Mime     string    `json:"mime,omitempty"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Mode     string    `json:"modeOctal"`
	Owner    string    `json:"owner,omitempty"`
	Group    string    `json:"group,omitempty"`

	// Text head, for kind=text.
	Language  string `json:"language,omitempty"`
	Text      string `json:"text,omitempty"`
	Lines     int    `json:"lines,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	// Editable says the whole file would open in the editor. A preview of the
	// first hundred lines of a two-gigabyte log must not offer a Save button
	// that would write those hundred lines over it.
	Editable bool `json:"editable"`

	// Image, when the dimensions could be read.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`

	// Archive peek.
	Entries      []ArchiveEntry `json:"entries,omitempty"`
	EntryCount   int            `json:"entryCount,omitempty"`
	MoreEntries  bool           `json:"moreEntries,omitempty"`
	ArchiveError string         `json:"archiveError,omitempty"`

	// Directory.
	ChildCount int `json:"childCount,omitempty"`
	DirCount   int `json:"dirCount,omitempty"`
	FileCount  int `json:"fileCount,omitempty"`

	IsSymlink     bool   `json:"isSymlink,omitempty"`
	SymlinkTarget string `json:"symlinkTarget,omitempty"`
	LinkBroken    bool   `json:"linkBroken,omitempty"`
}

type ArchiveEntry struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"isDir"`
}

const (
	// previewTextBytes is the head of a text file the preview carries. Enough
	// to recognise a config, a script or a log format at a glance; small
	// enough that clicking down a directory of them costs nothing.
	previewTextBytes = 64 << 10
	previewMaxLines  = 500
	previewArchiveN  = 200
)

// Kinds, as the UI switches on them.
const (
	KindDir     = "dir"
	KindText    = "text"
	KindImage   = "image"
	KindVideo   = "video"
	KindAudio   = "audio"
	KindPDF     = "pdf"
	KindArchive = "archive"
	KindBinary  = "binary"
)

func (s *Service) Preview(path string) (*Preview, error) {
	if s.run != nil {
		return hosted2(s, func(s *Service) (*Preview, error) { return s.Preview(path) })
	}
	full, err := s.ResolveEntry(path)
	if err != nil {
		return nil, err
	}
	entry, err := s.entry(full, filepath.Base(full))
	if err != nil {
		return nil, err
	}
	p := &Preview{
		Path: full, Name: entry.Name, Size: entry.Size, Modified: entry.Modified,
		Mode: entry.ModeOctal, Owner: entry.Owner, Group: entry.Group,
		IsSymlink: entry.IsSymlink, SymlinkTarget: entry.LinkTarget, LinkBroken: entry.LinkBroken,
	}
	if entry.LinkBroken {
		p.Kind = KindBinary
		return p, nil
	}
	if entry.IsDir {
		p.Kind = KindDir
		if names, err := os.ReadDir(full); err == nil {
			p.ChildCount = len(names)
			for _, d := range names {
				if d.IsDir() {
					p.DirCount++
				} else {
					p.FileCount++
				}
			}
		}
		return p, nil
	}

	p.Mime = MediaType(entry.Name)
	p.Kind = kindOf(entry.Name, p.Mime)

	switch p.Kind {
	case KindImage:
		p.Width, p.Height = imageSize(full)
	case KindArchive:
		entries, more, err := peekArchive(full)
		if err != nil {
			p.ArchiveError = err.Error()
		}
		p.Entries, p.MoreEntries = entries, more
		p.EntryCount = len(entries)
	}

	// Everything that is not obviously media gets the same test the editor
	// uses, because extensions lie in both directions: a `.log` may be a
	// rotated binary and a file with no extension at all is usually a script.
	if p.Kind == KindBinary || p.Kind == KindText {
		head, err := readHead(full, previewTextBytes)
		if err != nil {
			return nil, err
		}
		if looksBinary(head) {
			p.Kind = KindBinary
		} else {
			p.Kind = KindText
			p.Language = mimeHint(entry.Name)
			p.Text, p.Lines, p.Truncated = clampText(head, previewMaxLines, entry.Size > int64(len(head)))
			p.Editable = entry.Size <= maxEditBytes
		}
	}
	return p, nil
}

func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	read, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return buf[:read], nil
}

// clampText trims the head to whole lines and to a line count, and repairs a
// multi-byte rune cut in half by the byte limit — which is otherwise a
// replacement glyph at the bottom of every preview of a file with an accent
// near the boundary.
func clampText(b []byte, maxLines int, truncatedBySize bool) (string, int, bool) {
	truncated := truncatedBySize
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
		truncated = true
	}
	text := string(b)
	lines := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		lines++
	}
	if lines > maxLines {
		idx := 0
		for i := 0; i < maxLines; i++ {
			next := strings.IndexByte(text[idx:], '\n')
			if next < 0 {
				break
			}
			idx += next + 1
		}
		text = text[:idx]
		lines = maxLines
		truncated = true
	}
	return text, lines, truncated
}

// mediaTypes is the closed set this dashboard is willing to serve inline.
//
// It is an allowlist rather than a lookup because the raw route hands a file's
// own bytes back to the browser on the same origin as a session that drives
// the Docker socket. An HTML file served as text/html there would run as this
// dashboard; a JavaScript file served as such would be worse. Everything not
// named here is a download, which is inert.
var mediaTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".avif": "image/avif",
	".bmp": "image/bmp", ".ico": "image/x-icon", ".svg": "image/svg+xml",
	".mp4": "video/mp4", ".webm": "video/webm", ".ogv": "video/ogg",
	".mov": "video/quicktime", ".mkv": "video/x-matroska",
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".ogg": "audio/ogg",
	".flac": "audio/flac", ".m4a": "audio/mp4", ".aac": "audio/aac",
	".pdf": "application/pdf",
}

// MediaType is the content type this file may be served as, or "" for
// everything that may not be served inline at all.
func MediaType(name string) string {
	return mediaTypes[strings.ToLower(filepath.Ext(name))]
}

var archiveExts = map[string]bool{
	".zip": true, ".tar": true, ".gz": true, ".tgz": true, ".bz2": true,
	".xz": true, ".zst": true, ".7z": true, ".rar": true, ".jar": true,
}

func kindOf(name, mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return KindImage
	case strings.HasPrefix(mime, "video/"):
		return KindVideo
	case strings.HasPrefix(mime, "audio/"):
		return KindAudio
	case mime == "application/pdf":
		return KindPDF
	}
	lower := strings.ToLower(name)
	if archiveExts[filepath.Ext(lower)] {
		return KindArchive
	}
	if mimeHint(name) != "plaintext" {
		return KindText
	}
	// Anything left is decided by its bytes rather than by its name.
	return KindBinary
}

// peekArchive lists what is inside without unpacking anything. "What is in
// this tarball" is otherwise a question that can only be answered by
// extracting it somewhere and looking, which is how a home directory fills up
// with half-unpacked releases.
func peekArchive(path string) ([]ArchiveEntry, bool, error) {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".zip"), strings.HasSuffix(lower, ".jar"):
		zr, err := zip.OpenReader(path)
		if err != nil {
			return nil, false, err
		}
		defer zr.Close()
		out := []ArchiveEntry{}
		for _, f := range zr.File {
			if len(out) >= previewArchiveN {
				return out, true, nil
			}
			out = append(out, ArchiveEntry{
				Name:  f.Name,
				Size:  int64(f.UncompressedSize64),
				IsDir: f.FileInfo().IsDir(),
			})
		}
		return out, false, nil
	case strings.HasSuffix(lower, ".tar"), strings.HasSuffix(lower, ".tar.gz"),
		strings.HasSuffix(lower, ".tgz"), strings.HasSuffix(lower, ".tar.bz2"):
		f, err := os.Open(path)
		if err != nil {
			return nil, false, err
		}
		defer f.Close()
		var r io.Reader = bufio.NewReader(f)
		switch {
		case strings.HasSuffix(lower, ".gz"), strings.HasSuffix(lower, ".tgz"):
			gz, err := gzip.NewReader(r)
			if err != nil {
				return nil, false, err
			}
			defer gz.Close()
			r = gz
		case strings.HasSuffix(lower, ".bz2"):
			r = bzip2.NewReader(r)
		}
		tr := tar.NewReader(r)
		out := []ArchiveEntry{}
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				return out, false, nil
			}
			if err != nil {
				return out, false, err
			}
			if len(out) >= previewArchiveN {
				return out, true, nil
			}
			out = append(out, ArchiveEntry{
				Name:  hdr.Name,
				Size:  hdr.Size,
				IsDir: hdr.FileInfo().IsDir(),
			})
		}
	}
	// xz, zst, 7z and rar have no decoder in the standard library and none
	// worth a dependency here. They are still archives — the row says so and
	// offers a download rather than a listing.
	return nil, false, nil
}

var svgDimRe = regexp.MustCompile(`(?is)<svg[^>]*?\b(width|height|viewBox)\s*=\s*["']([^"']+)["']`)

// imageSize reads the dimensions from the header. The standard library covers
// png, jpeg and gif; webp and svg are parsed here because they are half of
// what a web server actually holds and neither has a decoder in the standard
// library. A format that cannot be read reports nothing rather than guessing.
func imageSize(path string) (int, int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	head := make([]byte, 4096)
	n, _ := io.ReadFull(f, head)
	head = head[:n]

	if w, h, ok := webpSize(head); ok {
		return w, h
	}
	if w, h, ok := svgSize(head); ok {
		return w, h
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(head)); err == nil {
		return cfg.Width, cfg.Height
	}
	// A progressive JPEG can carry more than 4 KB of headers before the frame
	// that holds the size, so a failed header read is retried against the file.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, 0
	}
	if cfg, _, err := image.DecodeConfig(bufio.NewReader(f)); err == nil {
		return cfg.Width, cfg.Height
	}
	return 0, 0
}

// webpSize reads a RIFF/WEBP header. The three chunk types store the size
// three different ways and a viewer that only knows the simple one reports
// nothing for most of the files a modern site serves.
func webpSize(b []byte) (int, int, bool) {
	if len(b) < 30 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return 0, 0, false
	}
	switch string(b[12:16]) {
	case "VP8 ":
		// Lossy: a 3-byte start code at 23, then 14-bit width and height.
		if len(b) < 30 || b[23] != 0x9d || b[24] != 0x01 || b[25] != 0x2a {
			return 0, 0, false
		}
		w := int(binary.LittleEndian.Uint16(b[26:28]) & 0x3fff)
		h := int(binary.LittleEndian.Uint16(b[28:30]) & 0x3fff)
		return w, h, w > 0 && h > 0
	case "VP8L":
		// Lossless: a signature byte, then 14 bits of width-1 and 14 of
		// height-1 packed little-endian across four bytes.
		if len(b) < 25 || b[20] != 0x2f {
			return 0, 0, false
		}
		bits := binary.LittleEndian.Uint32(b[21:25])
		w := int(bits&0x3fff) + 1
		h := int((bits>>14)&0x3fff) + 1
		return w, h, true
	case "VP8X":
		// Extended: 24-bit canvas width-1 and height-1.
		if len(b) < 30 {
			return 0, 0, false
		}
		w := int(uint32(b[24]) | uint32(b[25])<<8 | uint32(b[26])<<16)
		h := int(uint32(b[27]) | uint32(b[28])<<8 | uint32(b[29])<<16)
		return w + 1, h + 1, true
	}
	return 0, 0, false
}

func svgSize(b []byte) (int, int, bool) {
	if !bytes.Contains(bytes.ToLower(b), []byte("<svg")) {
		return 0, 0, false
	}
	var w, h int
	var vbW, vbH int
	for _, m := range svgDimRe.FindAllStringSubmatch(string(b), 8) {
		switch strings.ToLower(m[1]) {
		case "width":
			w = parseSVGLength(m[2])
		case "height":
			h = parseSVGLength(m[2])
		case "viewbox":
			parts := strings.FieldsFunc(m[2], func(r rune) bool { return r == ' ' || r == ',' })
			if len(parts) == 4 {
				vbW, vbH = parseSVGLength(parts[2]), parseSVGLength(parts[3])
			}
		}
	}
	if w == 0 || h == 0 {
		// A viewBox is the size for the very common SVG that sets no width at
		// all — an icon written to scale with its container.
		w, h = vbW, vbH
	}
	return w, h, w > 0 && h > 0
}

func parseSVGLength(s string) int {
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) && (s[end] == '.' || s[end] == '-' || (s[end] >= '0' && s[end] <= '9')) {
		end++
	}
	v, err := strconv.ParseFloat(s[:end], 64)
	if err != nil {
		return 0
	}
	return int(v)
}

// Usage is a recursive size, which is the one number a directory listing
// cannot show and the one everybody wants: a folder's row says "—" where every
// other row says how big it is, and "which of these is eating the disk" then
// needs an ssh session and du.
type Usage struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	Files int    `json:"files"`
	Dirs  int    `json:"dirs"`
	// Truncated means the walk hit its budget. The figure is then a floor,
	// and the UI says so rather than quoting a partial total as a total.
	Truncated bool `json:"truncated"`
	ElapsedMS int  `json:"elapsedMs"`
	// Largest is the heaviest handful of entries directly inside the path,
	// which is what turns a number into an answer.
	Largest []UsageEntry `json:"largest,omitempty"`
}

type UsageEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	IsDir bool   `json:"isDir"`
}

const usageMaxEntries = 400_000

func (s *Service) Usage(ctx context.Context, path string, budget time.Duration) (*Usage, error) {
	if s.run != nil {
		return hosted2(s, func(s *Service) (*Usage, error) { return s.Usage(ctx, path, budget) })
	}
	full, err := s.Resolve(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	if budget <= 0 || budget > 30*time.Second {
		budget = 10 * time.Second
	}
	deadline := started.Add(budget)
	out := &Usage{Path: full}
	if !st.IsDir() {
		out.Bytes, out.Files = st.Size(), 1
		out.ElapsedMS = int(time.Since(started).Milliseconds())
		return out, nil
	}

	// The per-child totals are accumulated in the same walk rather than in one
	// walk each: a directory of forty children would otherwise be forty-one
	// passes over the same tree.
	perChild := map[string]*UsageEntry{}
	visited := 0
	_ = filepath.WalkDir(full, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		visited++
		if visited%2048 == 0 && (ctx.Err() != nil || time.Now().After(deadline)) {
			out.Truncated = true
			return filepath.SkipAll
		}
		if visited > usageMaxEntries {
			out.Truncated = true
			return filepath.SkipAll
		}
		if p == full {
			return nil
		}
		var size int64
		if d.IsDir() {
			out.Dirs++
		} else {
			out.Files++
			// Lstat semantics: a symlink counts as the link, not as what it
			// points at, or a tree with links into /usr reports the size of
			// the operating system.
			if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
				size = info.Size()
				out.Bytes += size
			}
		}
		if rel, err := filepath.Rel(full, p); err == nil {
			top := rel
			if i := strings.IndexByte(rel, os.PathSeparator); i > 0 {
				top = rel[:i]
			}
			child, ok := perChild[top]
			if !ok {
				child = &UsageEntry{Name: top, Path: filepath.Join(full, top), IsDir: top != rel || d.IsDir()}
				perChild[top] = child
			}
			child.Bytes += size
		}
		return nil
	})

	for _, c := range perChild {
		out.Largest = append(out.Largest, *c)
	}
	sortUsage(out.Largest)
	if len(out.Largest) > 8 {
		out.Largest = out.Largest[:8]
	}
	out.ElapsedMS = int(time.Since(started).Milliseconds())
	return out, nil
}

func sortUsage(in []UsageEntry) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j].Bytes > in[j-1].Bytes; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}

// Checksum answers "is this the file I uploaded". It is the other command
// people keep a terminal open for, it costs one read, and getting it wrong is
// impossible in a way that most of this page is not.
type Checksum struct {
	Path string `json:"path"`
	Algo string `json:"algo"`
	Sum  string `json:"sum"`
	Size int64  `json:"size"`
}

func (s *Service) Checksum(ctx context.Context, path, algo string) (*Checksum, error) {
	if s.run != nil {
		return hosted2(s, func(s *Service) (*Checksum, error) { return s.Checksum(ctx, path, algo) })
	}
	full, err := s.Resolve(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, ErrIsDir
	}
	var h hash.Hash
	switch strings.ToLower(algo) {
	case "", "sha256":
		algo, h = "sha256", sha256.New()
	case "sha1":
		h = sha1.New()
	case "md5":
		h = md5.New()
	default:
		return nil, fmt.Errorf("unsupported checksum %q: use sha256, sha1 or md5", algo)
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := io.Copy(h, &ctxReader{ctx: ctx, r: f}); err != nil {
		return nil, err
	}
	return &Checksum{Path: full, Algo: strings.ToLower(algo), Sum: hex.EncodeToString(h.Sum(nil)), Size: st.Size()}, nil
}

// ctxReader is what makes hashing a 40 GB file abandonable: io.Copy alone runs
// to the end however long the caller has been gone.
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
