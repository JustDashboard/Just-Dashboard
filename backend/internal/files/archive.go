package files

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Wayy01/Just-Dashboard/backend/internal/safepath"
)

type ArchiveFormat string

const (
	FormatTarGz ArchiveFormat = "tar.gz"
	FormatZip   ArchiveFormat = "zip"

	maxExtractBytes     int64 = 8 << 30
	maxExtractEntries         = 100_000
	minExtractFreeBytes int64 = 1 << 30
)

var (
	ErrArchiveTooLarge       = errors.New("archive expands beyond the safe extraction byte limit")
	ErrArchiveTooManyEntries = errors.New("archive contains more than 100000 entries")
	ErrArchiveNoSpace        = errors.New("archive extraction would leave less than 1 GiB free")
)

// ResolveArchive checks what an archive download is asking for before a byte
// of it is streamed: the base directory the entry names are made relative to,
// and the members, each of which has to live under it.
//
// It is separate from Compress because the handler has to know the request is
// sound *before* it commits to a 200 and an attachment header — an error found
// mid-stream can only be logged.
//
// Members go through ResolveEntry rather than Resolve. A selected symlink is
// archived as the link it is; resolving it to its target put the target's
// bytes into the archive under a name computed from wherever the target
// actually lived, which for a link out of the base directory was a name
// beginning "../". An archive this server writes must never carry the path
// traversal its own extractor exists to refuse.
func (s *Service) ResolveArchive(base string, paths []string) (string, []string, error) {
	baseDir, err := s.Resolve(base)
	if err != nil {
		return "", nil, err
	}
	members := make([]string, 0, len(paths))
	for _, p := range paths {
		full, err := s.ResolveEntry(p)
		if err != nil {
			return "", nil, err
		}
		rel, err := filepath.Rel(baseDir, full)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", nil, fmt.Errorf("%s is not inside %s", p, base)
		}
		members = append(members, full)
	}
	return baseDir, members, nil
}

// Compress writes an archive of the resolved members to w. Streaming rather
// than building the archive on disk first means a multi-gigabyte directory
// download costs no temporary space. Callers resolve through ResolveArchive
// first; nothing here checks a path.
func (s *Service) Compress(w io.Writer, baseDir string, members []string, format ArchiveFormat) error {
	switch format {
	case FormatZip:
		return writeZip(w, baseDir, members)
	default:
		return writeTarGz(w, baseDir, members)
	}
}

func writeTarGz(w io.Writer, baseDir string, paths []string) error {
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	for _, root := range paths {
		if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			rel, err := filepath.Rel(baseDir, path)
			if err != nil {
				return err
			}
			link := ""
			if info.Mode()&os.ModeSymlink != 0 {
				link, _ = os.Readlink(path)
			}
			hdr, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return err
			}
			hdr.Name = filepath.ToSlash(rel)
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			f, err := os.Open(path)
			if err != nil {
				return nil
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func writeZip(w io.Writer, baseDir string, paths []string) error {
	zw := zip.NewWriter(w)
	defer zw.Close()

	for _, root := range paths {
		if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || !info.Mode().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(baseDir, path)
			if err != nil {
				return err
			}
			hdr, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			hdr.Name = filepath.ToSlash(rel)
			hdr.Method = zip.Deflate
			out, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			f, err := os.Open(path)
			if err != nil {
				return nil
			}
			defer f.Close()
			_, err = io.Copy(out, f)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

// Extract unpacks an archive into dest. Entry names are checked against the
// destination before anything is written: a crafted archive containing
// "../../etc/cron.d/evil" is the classic path-traversal write primitive, and
// refusing it is not optional.
func (s *Service) Extract(ctx context.Context, archivePath, dest string) ([]string, error) {
	// Serialising extraction keeps two requests from each reserving the same
	// free space and then consuming it together.
	s.extractMu.Lock()
	defer s.extractMu.Unlock()

	src, err := s.Resolve(archivePath)
	if err != nil {
		return nil, err
	}
	target, err := s.Resolve(dest)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return nil, err
	}
	byteLimit, err := extractByteLimit(target)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(src)
	budget := &extractBudget{ctx: ctx, maxBytes: byteLimit, maxEntries: maxExtractEntries}
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(src, target, budget)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTar(src, target, true, budget)
	case strings.HasSuffix(lower, ".tar"):
		return extractTar(src, target, false, budget)
	default:
		return nil, fmt.Errorf("unsupported archive format: %s", filepath.Base(src))
	}
}

func extractByteLimit(dest string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dest, &st); err != nil {
		return 0, err
	}
	available := int64(st.Bavail) * int64(st.Bsize)
	if available <= minExtractFreeBytes {
		return 0, ErrArchiveNoSpace
	}
	limit := available - minExtractFreeBytes
	if limit > maxExtractBytes {
		limit = maxExtractBytes
	}
	return limit, nil
}

type extractBudget struct {
	ctx        context.Context
	maxBytes   int64
	written    int64
	maxEntries int
	entries    int
}

func (b *extractBudget) nextEntry() error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	if b.entries >= b.maxEntries {
		return ErrArchiveTooManyEntries
	}
	b.entries++
	return nil
}

func (b *extractBudget) copy(dst io.Writer, src io.Reader) error {
	_, err := io.Copy(&extractWriter{budget: b, dst: dst}, src)
	return err
}

type extractWriter struct {
	budget *extractBudget
	dst    io.Writer
}

func (w *extractWriter) Write(p []byte) (int, error) {
	if err := w.budget.ctx.Err(); err != nil {
		return 0, err
	}
	remaining := w.budget.maxBytes - w.budget.written
	if remaining <= 0 {
		return 0, ErrArchiveTooLarge
	}
	tooLarge := int64(len(p)) > remaining
	if tooLarge {
		p = p[:int(remaining)]
	}
	n, err := w.dst.Write(p)
	w.budget.written += int64(n)
	if err != nil {
		return n, err
	}
	if tooLarge {
		return n, ErrArchiveTooLarge
	}
	return n, nil
}

func extractTar(src, dest string, compressed bool, budget *extractBudget) ([]string, error) {
	f, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var reader io.Reader = f
	if compressed {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		reader = gz
	}
	written := []string{}
	tr := tar.NewReader(reader)
	for {
		if err := budget.ctx.Err(); err != nil {
			return written, err
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			return written, nil
		}
		if err != nil {
			return written, err
		}
		if err := budget.nextEntry(); err != nil {
			return written, err
		}
		path, err := safepath.Join(dest, hdr.Name)
		if err != nil {
			return written, err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := safepath.Mkdir(path, os.FileMode(hdr.Mode).Perm()); err != nil {
				return written, err
			}
		case tar.TypeSymlink:
			// A symlink whose target escapes the destination turns into an
			// arbitrary write the next time anything follows it.
			if err := safepath.CheckLinkTarget(dest, hdr.Name, hdr.Linkname); err != nil {
				return written, err
			}
			if err := safepath.Symlink(hdr.Linkname, path); err != nil {
				return written, err
			}
		case tar.TypeReg:
			if err := safepath.MkdirParents(path); err != nil {
				return written, err
			}
			out, err := safepath.Create(path, os.FileMode(hdr.Mode).Perm())
			if err != nil {
				return written, err
			}
			if err := budget.copy(out, tr); err != nil {
				out.Close()
				os.Remove(path)
				return written, err
			}
			if err := out.Close(); err != nil {
				os.Remove(path)
				return written, err
			}
		default:
			continue
		}
		written = append(written, path)
	}
}

func extractZip(src, dest string, budget *extractBudget) ([]string, error) {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	written := []string{}
	for _, entry := range zr.File {
		if err := budget.nextEntry(); err != nil {
			return written, err
		}
		path, err := safepath.Join(dest, entry.Name)
		if err != nil {
			return written, err
		}
		if entry.FileInfo().IsDir() {
			if err := safepath.Mkdir(path, entry.Mode().Perm()); err != nil {
				return written, err
			}
			written = append(written, path)
			continue
		}
		if err := safepath.MkdirParents(path); err != nil {
			return written, err
		}
		rc, err := entry.Open()
		if err != nil {
			return written, err
		}
		out, err := safepath.Create(path, entry.Mode().Perm())
		if err != nil {
			rc.Close()
			return written, err
		}
		copyErr := budget.copy(out, rc)
		closeOutErr := out.Close()
		closeReadErr := rc.Close()
		if copyErr != nil {
			os.Remove(path)
			return written, copyErr
		}
		if closeOutErr != nil {
			os.Remove(path)
			return written, closeOutErr
		}
		if closeReadErr != nil {
			os.Remove(path)
			return written, closeReadErr
		}
		written = append(written, path)
	}
	return written, nil
}
