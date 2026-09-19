package logsx

import (
	"bufio"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// SearchOptions is a history question rather than a live one: it names a
// window, a filter and how much context to keep around each hit.
type SearchOptions struct {
	Filter   Filter
	Since    time.Time
	Until    time.Time
	Limit    int
	Before   int
	After    int
	Archives bool
	// Head keeps the first matches rather than the last. The tail is the right
	// default — a log is searched to find out what just happened — but an
	// operator reconstructing the start of an incident wants the other end,
	// and without this the only way to reach it is to narrow the window until
	// the result stops being truncated.
	Head bool
}

// Bucket is one column of the volume histogram. Counting by level rather than
// only in total is what turns "something happened at 03:12" into "the errors
// started at 03:12 while the traffic stayed flat".
type Bucket struct {
	Start  time.Time      `json:"start"`
	Total  int            `json:"total"`
	Counts map[string]int `json:"counts"`
}

// SearchedFile reports what each file in a rotated set contributed, because a
// search that spans an archive and finds everything in yesterday's file is
// telling you something a merged total hides.
type SearchedFile struct {
	Path     string     `json:"path"`
	Name     string     `json:"name"`
	Archive  bool       `json:"archive"`
	Scanned  int        `json:"scanned"`
	Matched  int        `json:"matched"`
	Modified *time.Time `json:"modified,omitempty"`
	Error    string     `json:"error,omitempty"`
}

type SearchResult struct {
	Lines     []Line `json:"lines"`
	Scanned   int    `json:"scanned"`
	Matched   int    `json:"matched"`
	Truncated bool   `json:"truncated"`
	// Complete is false when the scan ran out of time rather than out of file.
	Complete      bool           `json:"complete"`
	Files         []SearchedFile `json:"files"`
	Histogram     []Bucket       `json:"histogram"`
	BucketSeconds int            `json:"bucketSeconds,omitempty"`
	First         *time.Time     `json:"first,omitempty"`
	Last          *time.Time     `json:"last,omitempty"`
	TookMillis    int64          `json:"tookMillis"`
}

// histogramCap bounds what the histogram remembers. Every match contributes a
// timestamp and a level — nine bytes — and a wide-open search of a year of
// syslog would otherwise be counted line by line into memory to draw sixty
// columns.
const histogramCap = 500_000

const histogramBuckets = 60

// Search greps a file server-side. Doing it here rather than shipping the
// whole file to the browser is the difference between a usable viewer and a
// 2 GB download. With Archives set it walks the rotated set oldest-first, so
// "when did this start" can be answered past last night's logrotate run —
// which is the question a viewer that only reads the live file can never
// answer, and the reason people fall back to ssh and zgrep.
func (s *Service) Search(ctx context.Context, path string, opts SearchOptions) (*SearchResult, error) {
	return s.SearchTargets(ctx, []SearchTarget{{Path: path}}, opts)
}

// SearchTarget is one file to read, and the stream tag to hang on every line
// that comes out of it. PM2 is the reason it exists: a process's output is two
// files, and a search that read only stdout answered "not found" for a crash
// that is sitting in the error log.
type SearchTarget struct {
	Path   string
	Stream string
}

// SearchTargets is the general form. Each target's rotated archives are read
// before it when Archives is set, so a single answer can span both a process's
// two streams and last week's compressed generations.
func (s *Service) SearchTargets(ctx context.Context, targets []SearchTarget, opts SearchOptions) (*SearchResult, error) {
	c, err := NewCollector(opts)
	if err != nil {
		return nil, err
	}
	for _, target := range targets {
		if target.Path == "" || target.Path == "/dev/null" {
			continue
		}
		if err := s.Allow(target.Path); err != nil {
			return nil, err
		}
		paths := []string{target.Path}
		if opts.Archives {
			paths = append(Archives(target.Path), target.Path)
		}
		for _, p := range paths {
			if ctx.Err() != nil {
				c.Incomplete()
				return c.Result(), nil
			}
			var mod *time.Time
			if st, err := os.Stat(p); err == nil {
				m := st.ModTime().UTC()
				mod = &m
			}
			c.NextFile(p, p != target.Path, mod)
			if err := c.scanFile(ctx, p, target.Stream); err != nil {
				c.FileError(err)
				if ctx.Err() != nil {
					c.Incomplete()
					return c.Result(), nil
				}
			}
		}
	}
	return c.Result(), nil
}

func (c *Collector) scanFile(ctx context.Context, path, stream string) error {
	rc, err := openLog(path)
	if err != nil {
		return err
	}
	defer rc.Close()
	name := filepath.Base(path)
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)
	lineNo := 0
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		lineNo++
		text := sc.Text()
		// Parsing the level and the timestamp is the expensive part per line,
		// and a line the text filter has already rejected needs neither —
		// unless it is a candidate for the context window, which is the one
		// case where a non-matching line still gets rendered.
		if !c.filter.MatchText(text) && c.pending == 0 && c.before == 0 {
			c.scanned++
			continue
		}
		line := ParseLine(text, name)
		line.No, line.File, line.Stream = lineNo, name, stream

		c.Feed(line)
	}
	return sc.Err()
}

// Collector turns a stream of parsed lines into a SearchResult. It is the one
// implementation of everything a search answer is made of — the filter, the
// time window, the surrounding context, the result cap and the volume
// histogram — so that searching a file, a container and the journal cannot
// quietly answer the same question three different ways. The three differ only
// in where the lines come from.
type Collector struct {
	filter *Filter
	opts   SearchOptions
	before int
	after  int
	limit  int

	res     *SearchResult
	stamps  []stamp
	started time.Time

	trailing []Line
	pending  int
	lastKept int
	scanned  int
	matched  int
	file     int // index into res.Files, -1 before the first NextFile
}

func NewCollector(opts SearchOptions) (*Collector, error) {
	f, err := NewFilter(opts.Filter)
	if err != nil {
		return nil, err
	}
	limit := opts.Limit
	if limit <= 0 || limit > 20000 {
		limit = 2000
	}
	before, after := clampContext(opts.Before), clampContext(opts.After)
	return &Collector{
		filter:   f,
		opts:     opts,
		before:   before,
		after:    after,
		limit:    limit,
		res:      &SearchResult{Lines: []Line{}, Files: []SearchedFile{}, Complete: true},
		stamps:   make([]stamp, 0, 1024),
		started:  time.Now(),
		trailing: make([]Line, 0, before),
		file:     -1,
	}, nil
}

// Filter exposes the compiled filter so a caller reading from a source that is
// not a file can skip work the collector would discard anyway.
func (c *Collector) Filter() *Filter { return c.filter }

// NextFile starts a new file's tally. Line numbers and the context window both
// reset: context must never bridge two files, since the last line of yesterday
// is not the line before the first line of today.
func (c *Collector) NextFile(path string, archive bool, mod *time.Time) {
	c.closeFile()
	c.res.Files = append(c.res.Files, SearchedFile{
		Path: path, Name: filepath.Base(path), Archive: archive, Modified: mod,
	})
	c.file = len(c.res.Files) - 1
	c.trailing = c.trailing[:0]
	c.pending, c.lastKept = 0, 0
}

func (c *Collector) closeFile() {
	if c.file >= 0 {
		c.res.Files[c.file].Scanned = c.scanned
		c.res.Files[c.file].Matched = c.matched
	}
	c.res.Scanned += c.scanned
	c.res.Matched += c.matched
	c.scanned, c.matched = 0, 0
}

// FileError records that a file could not be read in full, without failing the
// whole search: an archive with the wrong permissions must not cost the
// operator the results from the six files that opened.
func (c *Collector) FileError(err error) {
	if c.file >= 0 && err != nil {
		c.res.Files[c.file].Error = err.Error()
	}
}

// Incomplete marks the answer as bounded by time rather than by data, which is
// the difference between "no more matches" and "we stopped looking".
func (c *Collector) Incomplete() { c.res.Complete = false }

// Feed accepts one parsed line, in file order.
func (c *Collector) Feed(line Line) {
	c.scanned++
	if !c.filter.Match(line) || !inWindow(line, c.opts) {
		switch {
		case c.pending > 0:
			line.Context = true
			c.keep(line)
			c.pending--
		case c.before > 0:
			line.Context = true
			if len(c.trailing) == c.before {
				c.trailing = c.trailing[1:]
			}
			c.trailing = append(c.trailing, line)
		}
		return
	}

	c.matched++
	if line.Timestamp != nil && len(c.stamps) < histogramCap {
		level := line.Level
		if level == "" {
			level = LevelUnknown
		}
		c.stamps = append(c.stamps, stamp{unix: line.Timestamp.Unix(), level: level})
	}
	// In head mode the cap is final: the rest of the file is still counted, so
	// the histogram and the totals describe the whole search, but nothing more
	// is rendered.
	if c.opts.Head && c.res.Truncated {
		c.trailing = c.trailing[:0]
		c.pending = 0
		return
	}
	for _, before := range c.trailing {
		if before.No != 0 && before.No <= c.lastKept {
			continue
		}
		c.keep(before)
	}
	c.trailing = c.trailing[:0]
	line.Match = c.filter.Highlights(line.Text)
	c.keep(line)
	c.pending = c.after
}

func (c *Collector) keep(l Line) {
	if l.No > c.lastKept {
		c.lastKept = l.No
	}
	c.res.Lines = append(c.res.Lines, l)
	if len(c.res.Lines) <= c.limit {
		return
	}
	c.res.Truncated = true
	if c.opts.Head {
		c.res.Lines = c.res.Lines[:c.limit]
		return
	}
	// Keep the most recent window rather than the first N: when a log is being
	// searched, the tail is what matters.
	c.res.Lines = c.res.Lines[1:]
}

func (c *Collector) Result() *SearchResult {
	c.closeFile()
	c.res.Histogram, c.res.BucketSeconds = histogram(c.stamps)
	if len(c.stamps) > 0 {
		min, max := c.stamps[0].unix, c.stamps[0].unix
		for _, s := range c.stamps {
			if s.unix < min {
				min = s.unix
			}
			if s.unix > max {
				max = s.unix
			}
		}
		first, last := time.Unix(min, 0).UTC(), time.Unix(max, 0).UTC()
		c.res.First, c.res.Last = &first, &last
	}
	c.res.TookMillis = time.Since(c.started).Milliseconds()
	return c.res
}

type stamp struct {
	unix  int64
	level string
}

func inWindow(l Line, opts SearchOptions) bool {
	if opts.Since.IsZero() && opts.Until.IsZero() {
		return true
	}
	if l.Timestamp == nil {
		// A line with no parseable timestamp continues the record above it, so
		// dropping it would cut a stack trace in half. Time bounds are a
		// coarse instrument on a log whose format the parser does not know,
		// and refusing to show anything is the worse failure.
		return true
	}
	if !opts.Since.IsZero() && l.Timestamp.Before(opts.Since) {
		return false
	}
	if !opts.Until.IsZero() && l.Timestamp.After(opts.Until) {
		return false
	}
	return true
}

func clampContext(n int) int {
	if n < 0 {
		return 0
	}
	if n > 20 {
		return 20
	}
	return n
}

// histogram buckets the matched timestamps into a fixed number of columns
// across the span they cover, snapping the width to a round unit so the
// x-axis reads as minutes or hours rather than as 37-second intervals.
func histogram(stamps []stamp) ([]Bucket, int) {
	if len(stamps) == 0 {
		return []Bucket{}, 0
	}
	min, max := stamps[0].unix, stamps[0].unix
	for _, s := range stamps {
		if s.unix < min {
			min = s.unix
		}
		if s.unix > max {
			max = s.unix
		}
	}
	span := max - min
	if span <= 0 {
		span = 1
	}
	width := int64(1)
	for _, candidate := range []int64{1, 5, 15, 30, 60, 300, 900, 1800, 3600, 7200, 21600, 43200, 86400, 604800} {
		width = candidate
		if span/candidate <= histogramBuckets {
			break
		}
	}
	start := (min / width) * width
	count := int((max-start)/width) + 1
	buckets := make([]Bucket, count)
	for i := range buckets {
		buckets[i] = Bucket{Start: time.Unix(start+int64(i)*width, 0).UTC(), Counts: map[string]int{}}
	}
	for _, s := range stamps {
		i := int((s.unix - start) / width)
		if i < 0 || i >= count {
			continue
		}
		buckets[i].Total++
		buckets[i].Counts[s.level]++
	}
	return buckets, int(width)
}

// openLog opens a log file, transparently decompressing the rotated archives
// logrotate leaves behind. Without this the history search stops at last
// night's rotation — the exact boundary an incident investigation needs to
// cross — and the operator is sent to ssh and zgrep, which is the thing this
// page exists to replace.
func openLog(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".gz":
		zr, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		return &nestedCloser{r: zr, closers: []io.Closer{zr, f}}, nil
	case ".bz2":
		return &nestedCloser{r: bzip2.NewReader(f), closers: []io.Closer{f}}, nil
	}
	return f, nil
}

// Compressed reports whether a path is one of the archive formats this package
// can read, which is what lets the source list offer an archive rather than
// listing it and then failing to open it.
func Compressed(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".gz", ".bz2":
		return true
	}
	return false
}

type nestedCloser struct {
	r       io.Reader
	closers []io.Closer
}

func (n *nestedCloser) Read(p []byte) (int, error) { return n.r.Read(p) }
func (n *nestedCloser) Close() error {
	var err error
	for _, c := range n.closers {
		if e := c.Close(); e != nil && err == nil {
			err = e
		}
	}
	return err
}

// archiveSuffix recognises what logrotate leaves next to a live log: a numeric
// generation (syslog.1), a dated one (syslog-20240612), either optionally
// compressed. Matching the shape rather than listing the schemes is what makes
// this work on a host configured with `dateext` as well as one without.
var archiveSuffix = regexp.MustCompile(`^[-.](\d+|\d{8}|\d{8}\.\d+)(\.gz|\.bz2|\.xz|\.zst)?$`)

// Archives lists the rotated generations of a log, oldest first. Order is by
// modification time rather than by the number in the name, because the two
// schemes count in opposite directions — syslog.1 is *newer* than syslog.2,
// while syslog-20240612 is older than syslog-20240613 — and a search that
// concatenated them in the wrong order would report an incident running
// backwards.
func Archives(path string) []string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type candidate struct {
		path string
		mod  time.Time
	}
	found := []candidate{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == base || !strings.HasPrefix(name, base) {
			continue
		}
		if !archiveSuffix.MatchString(name[len(base):]) {
			continue
		}
		// .xz and .zst have no decompressor in the standard library, and
		// offering an archive that cannot be opened is worse than not
		// offering it.
		if ext := strings.ToLower(filepath.Ext(name)); ext == ".xz" || ext == ".zst" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		found = append(found, candidate{path: filepath.Join(dir, name), mod: info.ModTime()})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].mod.Before(found[j].mod) })
	out := make([]string, len(found))
	for i, c := range found {
		out[i] = c.path
	}
	return out
}

// Range writes the lines that pass the filter and fall inside the window,
// which is what the export action serves. It takes the same Filter the live
// view is using, so exporting what is on screen produces what is on screen —
// the previous version ignored the filter entirely and handed back the whole
// file.
func (s *Service) Range(ctx context.Context, path string, opts SearchOptions, w io.Writer) (int, error) {
	return s.RangeTargets(ctx, []SearchTarget{{Path: path}}, opts, w)
}

// RangeTargets writes every line that passes the filter, across the same set of
// files a search would read. Export and search therefore answer the same
// question — the previous export ignored the filter and handed back the whole
// file, so narrowing the view and pressing Export produced two different logs.
func (s *Service) RangeTargets(ctx context.Context, targets []SearchTarget, opts SearchOptions, w io.Writer) (int, error) {
	f, err := NewFilter(opts.Filter)
	if err != nil {
		return 0, err
	}
	written := 0
	for _, target := range targets {
		if target.Path == "" || target.Path == "/dev/null" {
			continue
		}
		if err := s.Allow(target.Path); err != nil {
			return written, err
		}
		paths := []string{target.Path}
		if opts.Archives {
			paths = append(Archives(target.Path), target.Path)
		}
		for _, p := range paths {
			n, err := s.rangeOne(ctx, p, f, opts, w)
			written += n
			if err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func (s *Service) rangeOne(ctx context.Context, path string, f *Filter, opts SearchOptions, w io.Writer) (int, error) {
	rc, err := openLog(path)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	name := filepath.Base(path)
	written := 0
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)
	for sc.Scan() {
		if ctx.Err() != nil {
			return written, ctx.Err()
		}
		text := sc.Text()
		if !f.MatchText(text) {
			continue
		}
		line := ParseLine(text, name)
		if !f.MatchLevel(line.Level) || !inWindow(line, opts) {
			continue
		}
		if _, err := io.WriteString(w, text+"\n"); err != nil {
			return written, err
		}
		written++
	}
	return written, sc.Err()
}
