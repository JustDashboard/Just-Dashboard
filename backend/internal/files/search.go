package files

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

var ErrInvalidSearch = errors.New("invalid search")

type SearchResult struct {
	Hits       []SearchHit `json:"hits"`
	Truncated  bool        `json:"truncated"`
	Visited    int         `json:"visited"`
	Unreadable int         `json:"unreadable"`
	ElapsedMs  int64       `json:"elapsedMs"`
}

type SearchHit struct {
	Path    string   `json:"path"`
	Name    string   `json:"name"`
	IsDir   bool     `json:"isDir"`
	Line    int      `json:"line,omitempty"`
	Snippet string   `json:"snippet,omitempty"`
	Size    int64    `json:"size"`
	Ranges  [][2]int `json:"ranges,omitempty"`
}

type SearchOptions struct {
	Root       string
	Query      string
	Content    bool
	Regex      bool
	IgnoreCase bool
	MaxDepth   int
	Limit      int
	SkipHidden bool
	AllLines   bool
	MaxVisits  int
	// MaxFileSize caps which files are opened for a content search. Grepping
	// a database dump or a video is never what the operator meant and would
	// stall the request for minutes.
	MaxFileSize int64
}

var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, ".svn": true, "vendor": true,
	"__pycache__": true, ".cache": true, ".next": true,
	"proc": true, "sys": true, "dev": true, "run": true,
}

func (s *Service) Search(ctx context.Context, opts SearchOptions) ([]SearchHit, error) {
	result, err := s.SearchDetailed(ctx, opts)
	if err != nil {
		return nil, err
	}
	return result.Hits, nil
}

func (s *Service) SearchDetailed(ctx context.Context, opts SearchOptions) (*SearchResult, error) {
	started := time.Now()
	root, err := s.Resolve(opts.Root)
	if err != nil {
		return nil, err
	}
	if opts.Limit <= 0 || opts.Limit > 2000 {
		opts.Limit = 500
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 12
	}
	if opts.MaxFileSize <= 0 {
		opts.MaxFileSize = 4 << 20
	}
	if opts.MaxVisits <= 0 {
		opts.MaxVisits = 100_000
	}

	expr := opts.Query
	if !opts.Regex {
		expr = regexp.QuoteMeta(expr)
	}
	if opts.IgnoreCase {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSearch, err)
	}
	match := re.MatchString

	allowed, relative, err := s.openRootEntry(root)
	if err != nil {
		return nil, err
	}
	defer allowed.Close()
	pinned, err := allowed.OpenRoot(relative)
	if err != nil {
		return nil, err
	}
	defer pinned.Close()
	result := &SearchResult{Hits: []SearchHit{}}
	err = fs.WalkDir(pinned.FS(), ".", func(relative string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil || result.Visited >= opts.MaxVisits || len(result.Hits) >= opts.Limit {
			result.Truncated = true
			return fs.SkipAll
		}
		result.Visited++
		if err != nil {
			result.Unreadable++
			return nil
		}
		path := filepath.Join(root, filepath.FromSlash(relative))
		if relative != "." && opts.SkipHidden && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if relative != "." && (skipDirs[d.Name()] ||
				strings.Count(relative, "/")+1 > opts.MaxDepth) {
				return fs.SkipDir
			}
			if path != root && match(d.Name()) && !opts.Content {
				result.Hits = append(result.Hits, SearchHit{Path: path, Name: d.Name(), IsDir: true})
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			result.Unreadable++
			return nil
		}
		// A symlink is followed by neither the name nor content search; the
		// target is visited on its own if it lives under the root.
		if !info.Mode().IsRegular() {
			return nil
		}
		if !opts.Content {
			if match(d.Name()) {
				result.Hits = append(result.Hits, SearchHit{Path: path, Name: d.Name(), Size: info.Size()})
			}
			return nil
		}
		if info.Size() > opts.MaxFileSize || info.Size() == 0 {
			return nil
		}
		// A replaced FIFO cannot block Open between the directory stat and
		// the opened-file stat. Root keeps a replaced symlink contained too.
		f, err := pinned.OpenFile(relative, os.O_RDONLY|unix.O_NONBLOCK, 0)
		if err != nil {
			result.Unreadable++
			return nil
		}
		defer f.Close()
		// Check the opened entry too: a special file must never stall a search.
		opened, err := f.Stat()
		if err != nil || !opened.Mode().IsRegular() || opened.Size() > opts.MaxFileSize {
			return nil
		}
		remaining := opts.Limit - len(result.Hits)
		if !opts.AllLines {
			remaining = 1
		}
		for _, hit := range grepLines(ctx, f, path, opened.Size(), re, remaining) {
			hit.Name = d.Name()
			result.Hits = append(result.Hits, hit)
		}
		return nil
	})
	result.ElapsedMs = time.Since(started).Milliseconds()
	if ctx.Err() != nil {
		result.Truncated = true
	}
	if len(result.Hits) >= opts.Limit {
		result.Truncated = true
	}
	if err != nil && err != fs.SkipAll && ctx.Err() == nil {
		return result, err
	}
	return result, nil
}

func grepLines(ctx context.Context, f *os.File, path string, size int64, re *regexp.Regexp, limit int) []SearchHit {
	hits := []SearchHit{}

	head := make([]byte, 512)
	n, _ := f.Read(head)
	if looksBinary(head[:n]) {
		return hits
	}
	f.Seek(0, 0)

	sc := bufio.NewScanner(io.LimitReader(f, size))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	lineNo := 0
	for sc.Scan() {
		if ctx.Err() != nil || len(hits) >= limit {
			break
		}
		lineNo++
		line := sc.Text()
		if location := re.FindStringIndex(line); location != nil {
			snippet := searchSnippet(line, location[0])
			ranges := [][2]int{}
			for _, span := range re.FindAllStringIndex(snippet, -1) {
				ranges = append(ranges, [2]int{len(utf16.Encode([]rune(snippet[:span[0]]))), len(utf16.Encode([]rune(snippet[:span[1]])))})
			}
			hits = append(hits, SearchHit{Path: path, Line: lineNo, Snippet: snippet, Size: size, Ranges: ranges})
		}
	}
	return hits
}

func searchSnippet(line string, match int) string {
	start := max(0, match-80)
	for start < len(line) && !utf8.RuneStart(line[start]) {
		start++
	}
	end := min(len(line), max(start+240, match+80))
	for end < len(line) && end > start && !utf8.RuneStart(line[end]) {
		end--
	}
	return line[start:end]
}
