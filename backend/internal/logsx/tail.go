// Package logsx unifies the log sources on a host — syslog, nginx, PM2,
// application files and the journal — behind one tailing and search API.
package logsx

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nxadm/tail"
)

// Service reads log files. Reads are confined to the configured roots: the
// file manager is the deliberate way to read arbitrary paths, and a log
// viewer that could open /etc/shadow would be a privilege escalation dressed
// up as a feature.
type Service struct {
	roots []string
}

func New(roots []string) *Service {
	cleaned := make([]string, 0, len(roots))
	for _, r := range roots {
		if abs, err := filepath.Abs(r); err == nil {
			cleaned = append(cleaned, filepath.Clean(abs))
		}
	}
	return &Service{roots: cleaned}
}

// Roots reports the configured log roots, which the UI names when it has to
// explain why a path was refused.
func (s *Service) Roots() []string { return append([]string(nil), s.roots...) }

// Allow reports whether a path is inside a configured root, after resolving
// symlinks — otherwise a symlink planted in /var/log would defeat the check.
func (s *Service) Allow(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// A path that does not exist yet is judged on its literal form; a
		// rotated-away log file is a normal case.
		resolved = filepath.Clean(abs)
	}
	for _, root := range s.roots {
		if resolved == root || strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
			return nil
		}
	}
	return fmt.Errorf("path %q is outside the configured log roots", path)
}

// Tail follows a file, emitting the last n lines first and then new ones as
// they arrive. It survives rotation: tail reopens the path when the inode
// changes, which is what logrotate does nightly.
func (s *Service) Tail(ctx context.Context, path string, lines int) (<-chan string, error) {
	if err := s.Allow(path); err != nil {
		return nil, err
	}
	if lines <= 0 || lines > 20000 {
		lines = 300
	}
	offset, err := seekBackLines(path, lines)
	if err != nil {
		return nil, err
	}
	return s.follow(ctx, path, offset)
}

func (s *Service) follow(ctx context.Context, path string, offset int64) (<-chan string, error) {
	t, err := tail.TailFile(path, tail.Config{
		Follow:    true,
		ReOpen:    true,
		MustExist: true,
		Location:  &tail.SeekInfo{Offset: offset, Whence: io.SeekStart},
		Logger:    tail.DiscardingLogger,
	})
	if err != nil {
		return nil, err
	}
	out := make(chan string, 256)
	go func() {
		defer close(out)
		defer t.Cleanup()
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case line, ok := <-t.Lines:
				if !ok {
					return
				}
				if line.Err != nil {
					continue
				}
				select {
				case <-ctx.Done():
					return
				case out <- line.Text:
				}
			}
		}
	}()
	return out, nil
}

// TailInto is the callback form used where the caller already owns a channel,
// such as the PM2 viewer merging stdout and stderr into one stream.
func (s *Service) TailInto(ctx context.Context, path string, lines int, fn func(string)) {
	ch, err := s.Tail(ctx, path, lines)
	if err != nil {
		return
	}
	for line := range ch {
		fn(line)
	}
}

// prefillCap bounds the backwards scan that fills a filtered tail. A filtered
// tail cannot start n lines from the end the way an unfiltered one does — on a
// log where one line in a thousand is an error, "the last 400 lines" is two
// errors and an empty-looking page. So the prefill scans backwards until it
// has n *matching* lines or has read this much, whichever comes first, and the
// UI says which of the two happened.
const prefillCap = 32 << 20

// TailLines is the tail the log viewer uses: it parses each line, applies the
// filter server-side, and — the part a plain tail cannot do — makes the
// initial window n lines that *match* rather than n lines of which a handful
// might. Returning parsed lines rather than text also means the level and the
// timestamp are decided in exactly one place for every source kind.
func (s *Service) TailLines(ctx context.Context, path string, n int, f *Filter) (<-chan Line, *Prefill, error) {
	if err := s.Allow(path); err != nil {
		return nil, nil, err
	}
	if n <= 0 || n > 20000 {
		n = 400
	}
	name := filepath.Base(path)

	pre := &Prefill{}
	var (
		seed   []Line
		offset int64
	)
	if f.Empty() {
		off, err := seekBackLines(path, n)
		if err != nil {
			return nil, nil, err
		}
		offset = off
	} else {
		var err error
		seed, offset, pre.Complete, err = scanBack(path, n, name, f)
		if err != nil {
			return nil, nil, err
		}
		pre.Lines = len(seed)
	}

	raw, err := s.follow(ctx, path, offset)
	if err != nil {
		return nil, nil, err
	}
	out := make(chan Line, 256)
	go func() {
		defer close(out)
		for _, l := range seed {
			select {
			case <-ctx.Done():
				return
			case out <- l:
			}
		}
		for text := range raw {
			if !f.MatchText(text) {
				continue
			}
			line := ParseLine(text, name)
			if !f.MatchLevel(line.Level) {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case out <- line:
			}
		}
	}()
	return out, pre, nil
}

// Prefill describes the initial window a filtered tail could produce, so the
// viewer can say "the last 120 matches in the final 32 MB" rather than leaving
// a short list looking like the whole story.
type Prefill struct {
	Lines int `json:"lines"`
	// Complete is true when the scan reached the start of the file, i.e. the
	// prefill really is every match there has ever been.
	Complete bool `json:"complete"`
}

// scanBack reads the tail end of a file forwards — seeking backwards line by
// line would be one syscall per line — collecting matches into a ring of n,
// and returns the offset it stopped at so the follow starts exactly there and
// no line is shown twice or missed.
func scanBack(path string, n int, name string, f *Filter) ([]Line, int64, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil {
		return nil, 0, false, err
	}
	size := st.Size()
	start := int64(0)
	complete := true
	if size > prefillCap {
		start = size - prefillCap
		complete = false
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, 0, false, err
	}
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)
	if start > 0 {
		// The first line is almost certainly a fragment of a record that began
		// before the window; drop it rather than parse half a line.
		sc.Scan()
	}
	ring := make([]Line, 0, n)
	for sc.Scan() {
		text := sc.Text()
		if !f.MatchText(text) {
			continue
		}
		line := ParseLine(text, name)
		if !f.MatchLevel(line.Level) {
			continue
		}
		if len(ring) == n {
			ring = ring[1:]
		}
		ring = append(ring, line)
	}
	// Whatever the scanner consumed is where the follow picks up. Using the
	// size read before the scan instead would replay anything appended while
	// it ran.
	end, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, 0, false, err
	}
	return ring, end, complete, sc.Err()
}

// seekBackLines finds the byte offset n lines from the end, so tailing a
// multi-gigabyte log does not read the whole file into memory.
func seekBackLines(path string, n int) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	size := st.Size()
	const chunk = 64 * 1024
	var (
		offset = size
		count  int
		buf    = make([]byte, chunk)
	)
	for offset > 0 && count <= n {
		read := int64(chunk)
		if offset < read {
			read = offset
		}
		offset -= read
		if _, err := f.ReadAt(buf[:read], offset); err != nil && err != io.EOF {
			return 0, err
		}
		for i := int(read) - 1; i >= 0; i-- {
			if buf[i] == '\n' {
				count++
				if count > n {
					return offset + int64(i) + 1, nil
				}
			}
		}
	}
	return 0, nil
}

// Line is a parsed log record. Level is best-effort: log formats on a typical
// server are not uniform, so the parser recognises the common shapes and
// leaves the rest unclassified rather than guessing.
type Line struct {
	Text      string     `json:"text"`
	Level     string     `json:"level,omitempty"`
	Timestamp *time.Time `json:"timestamp,omitempty"`
	Source    string     `json:"source,omitempty"`
	// Stream is "stdout" or "stderr" where the producer distinguishes them —
	// a container and a PM2 process do, a file does not.
	Stream string `json:"stream,omitempty"`
	// No is the 1-based line number within the file, set by the history
	// search. "Line 48211 of syslog" is the answer to "where did you find
	// this", and it is what makes the surrounding context navigable.
	No int `json:"no,omitempty"`
	// Context marks a line included because it sits next to a match rather
	// than because it matched, so the viewer can render it dimmed instead of
	// implying it was a hit.
	Context bool `json:"context,omitempty"`
	// File names the archive a search result came from, since a search over a
	// rotated set spans several files and "which one" changes what you do next.
	File string `json:"file,omitempty"`
	// Match holds the byte ranges of the search term, computed here because
	// the browser cannot faithfully re-run a Go regular expression.
	Match [][2]int `json:"match,omitempty"`
}

// maxLine bounds one record. A binary blob written into a log file with no
// newline in it must not be able to exhaust this process's memory.
const maxLine = 1 << 20

// levelWords is the vocabulary detectLevel recognises, already normalised.
// It replaced a regular expression with an alternation of thirteen words, run
// over every line of every file. That was the single most expensive thing in a
// history search: a five-file scan of auth.log spent most of nineteen seconds
// inside it. A word scan with a map lookup is the same answer for a fraction
// of the work, and it is the reason searching a rotated set is usable at all.
var levelWords = map[string]string{
	"emerg": "critical", "alert": "critical", "crit": "critical",
	"critical": "critical", "fatal": "critical",
	"error": "error", "err": "error",
	"warn": "warn", "warning": "warn",
	"notice": "info", "info": "info",
	"debug": "debug", "trace": "debug",
}

// wordByte matches what a regular expression's \b treats as a word character,
// plus every non-ASCII byte — so a UTF-8 letter cannot split a word and turn
// "Fehler" into a match for "err".
func wordByte(c byte) bool {
	return c >= 0x80 || c == '_' ||
		(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 32
	}
	return c
}

// detectLevel finds the first whole word that names a level. The word must be
// delimited on both sides, which is what stops "errors" and "criticality"
// being read as a level.
func detectLevel(text string) string {
	const maxWord = 8
	var buf [maxWord]byte
	for i := 0; i < len(text); {
		if !wordByte(text[i]) {
			i++
			continue
		}
		start := i
		for i < len(text) && wordByte(text[i]) {
			i++
		}
		n := i - start
		if n < 3 || n > maxWord {
			continue
		}
		for k := range n {
			buf[k] = lowerASCII(text[start+k])
		}
		// The compiler elides the allocation for a map index written this way.
		if level, ok := levelWords[string(buf[:n])]; ok {
			return level
		}
	}
	return ""
}

// Normalise ranks a level onto a fixed scale so one filter works across
// syslog priorities, nginx's error levels and application JSON logs.
func Normalise(level string) string {
	switch strings.ToLower(level) {
	case "emerg", "alert", "crit", "critical", "fatal":
		return "critical"
	case "error", "err":
		return "error"
	case "warn", "warning":
		return "warn"
	case "notice", "info":
		return "info"
	case "debug", "trace":
		return "debug"
	}
	return ""
}

// LevelFromPriority maps a syslog/journal priority onto the same scale, so the
// journal's numbers and a text log's words end up as one vocabulary and one
// set of filter chips.
func LevelFromPriority(p int) string {
	switch {
	case p <= 2:
		return "critical"
	case p == 3:
		return "error"
	case p == 4:
		return "warn"
	case p <= 6:
		return "info"
	default:
		return "debug"
	}
}

func ParseLine(text, source string) Line {
	l := Line{Text: text, Source: source, Level: detectLevel(text)}
	if ts, ok := parseTimestamp(text); ok {
		l.Timestamp = &ts
	}
	return l
}

// Timestamp formats seen in the wild: syslog's "Jan  2 15:04:05", nginx's
// bracketed "02/Jan/2006:15:04:05 -0700", and ISO-8601 from anything modern.
//
// The ISO family is read as a *token* rather than by a fixed width, which is
// the fix for a wrong answer rather than a slow one: "2026-08-28T23:03:24.
// 804642+00:00" is 32 characters, so slicing 25 of them chopped the zone off
// and the remainder parsed as naive UTC. Every line on a host that does not
// log in UTC was therefore filed under the wrong hour — invisible on a UTC
// server, and an hour of confusion on any other.
var syslogLayouts = []struct {
	layout string
	length int
}{
	{"Jan  2 15:04:05", 15},
	{"Jan 02 15:04:05", 15},
}

var isoLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02T15:04:05.999999999",
}

func parseTimestamp(text string) (time.Time, bool) {
	if len(text) == 0 {
		return time.Time{}, false
	}
	// The first byte decides which family can possibly parse, so a line is
	// never fed to every layout to be refused by all of them. time.Parse is
	// expensive and a log is mostly lines that carry no timestamp at all.
	switch first := text[0]; {
	case first >= '0' && first <= '9':
		end := strings.IndexByte(text, ' ')
		if end < 0 {
			end = len(text)
		}
		if end >= 19 && end <= 40 {
			for _, layout := range isoLayouts {
				if t, err := time.Parse(layout, text[:end]); err == nil {
					return t.UTC(), true
				}
			}
		}
		// "2006-01-02 15:04:05" has a space inside it, so it is the one ISO
		// spelling the token above cannot reach.
		if len(text) >= 19 {
			if t, err := time.Parse("2006-01-02 15:04:05", text[:19]); err == nil {
				return t.UTC(), true
			}
		}
		// nginx's access log starts with an address and carries its time in
		// brackets. The length test is what keeps this off every syslog line
		// that happens to contain a pid in brackets — a failed time.Parse
		// costs more than the whole rest of this function.
		if t, ok := parseBracketed(text); ok {
			return t, true
		}
	case first >= 'A' && first <= 'Z':
		for _, l := range syslogLayouts {
			if len(text) < l.length {
				continue
			}
			t, err := time.Parse(l.layout, strings.TrimSpace(text[:l.length]))
			if err != nil {
				continue
			}
			// Syslog omits the year; assume the current one, correcting
			// backwards when that would place the entry in the future.
			now := time.Now()
			t = t.AddDate(now.Year(), 0, 0)
			if t.After(now.Add(24 * time.Hour)) {
				t = t.AddDate(-1, 0, 0)
			}
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

const bracketLayout = "02/Jan/2006:15:04:05 -0700"

func parseBracketed(text string) (time.Time, bool) {
	start := strings.IndexByte(text, '[')
	if start < 0 {
		return time.Time{}, false
	}
	end := strings.IndexByte(text[start:], ']')
	if end != len(bracketLayout)+1 {
		return time.Time{}, false
	}
	t, err := time.Parse(bracketLayout, text[start+1:start+end])
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}
