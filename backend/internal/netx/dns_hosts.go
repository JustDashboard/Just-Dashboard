package netx

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The host's own records are lines in /etc/hosts. The dashboard owns exactly
// one block of that file, between two marker comments, and every other byte
// of it belongs to somebody else: the distribution's localhost lines, the
// provider's cloud-init entries, whatever the operator typed. Rewriting the
// file from a parsed model would normalise their whitespace, reorder their
// lines and lose their comments, so the file is edited as bytes: the block is
// found, replaced, and everything before and after it is carried across
// untouched.

const (
	hostsBegin = "# BEGIN Just Dashboard"
	hostsEnd   = "# END Just Dashboard"

	maxHostRecords = 500
	maxHostNames   = 16
)

// HostRecord is one line of the dashboard's block: an address and the names
// that resolve to it.
type HostRecord struct {
	Address string   `json:"address"`
	Names   []string `json:"names"`
}

// HostLine is a line of the file outside the block, shown and never edited.
type HostLine struct {
	Address string   `json:"address"`
	Names   []string `json:"names"`
	// Line is its 1-based line number in the file.
	Line int `json:"line"`
}

// HostRecords is the hosts file split at the dashboard's block.
type HostRecords struct {
	Path    string       `json:"path"`
	Managed []HostRecord `json:"managed"`
	Other   []HostLine   `json:"other"`
	// Problem is why the file cannot be edited from here, such as two blocks.
	// Reading still works and Other is then every record in the file.
	Problem string `json:"problem,omitempty"`
}

// hostsSpan locates the block in the file's lines.
type hostsSpan struct {
	begin, end int // line indexes, inclusive; -1 when there is no block
}

// splitHostsLines splits keeping each line's terminator, so joining the pieces
// gives back exactly the bytes that were read.
func splitHostsLines(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	return bytes.SplitAfter(data, []byte("\n"))
}

func lineText(l []byte) string {
	return strings.TrimSpace(string(l))
}

// findHostsBlock finds the markers, refusing a file where they cannot be
// trusted: two blocks, a block with no end, an end before its beginning. In
// each the dashboard cannot know which text is its own, and guessing means
// deleting someone's lines or leaving a stale record in force.
func findHostsBlock(path string, lines [][]byte) (hostsSpan, error) {
	span := hostsSpan{begin: -1, end: -1}
	var begins, ends int
	for i, l := range lines {
		switch lineText(l) {
		case hostsBegin:
			begins++
			if span.begin < 0 {
				span.begin = i
			}
		case hostsEnd:
			ends++
			if span.end < 0 {
				span.end = i
			}
		}
	}
	switch {
	case begins > 1:
		return span, fmt.Errorf("%s has %d %q lines. Remove the extra blocks by hand; the dashboard will not guess which one is real", path, begins, hostsBegin)
	case ends > 1:
		return span, fmt.Errorf("%s has %d %q lines. Remove the extra ones by hand; the dashboard will not guess where the block stops", path, ends, hostsEnd)
	case begins == 1 && ends == 0:
		return span, fmt.Errorf("%s has a %q line but no %q after it. Add or remove the marker by hand", path, hostsBegin, hostsEnd)
	case begins == 0 && ends == 1:
		return span, fmt.Errorf("%s has a %q line but no %q before it. Add or remove the marker by hand", path, hostsEnd, hostsBegin)
	case begins == 1 && span.end < span.begin:
		return span, fmt.Errorf("%s has %q before %q. Put them in order by hand", path, hostsEnd, hostsBegin)
	}
	return span, nil
}

// parseHostsLine reads one record. Comments and blank lines are not records.
func parseHostsLine(l []byte) (string, []string, bool) {
	text := string(l)
	if i := strings.Index(text, "#"); i >= 0 {
		text = text[:i]
	}
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return "", nil, false
	}
	return fields[0], fields[1:], true
}

// HostRecords reads the file.
func (s *Service) HostRecords(ctx context.Context) (*HostRecords, error) {
	path := hostFilePath(s.paths.Hosts)
	out := &HostRecords{Path: s.paths.Hosts, Managed: []HostRecord{}, Other: []HostLine{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	lines := splitHostsLines(data)
	span, err := findHostsBlock(s.paths.Hosts, lines)
	if err != nil {
		out.Problem = err.Error()
		span = hostsSpan{begin: -1, end: -1}
	}
	for i, l := range lines {
		addr, names, ok := parseHostsLine(l)
		if !ok {
			continue
		}
		if span.begin >= 0 && i > span.begin && i < span.end {
			out.Managed = append(out.Managed, HostRecord{Address: addr, Names: names})
			continue
		}
		out.Other = append(out.Other, HostLine{Address: addr, Names: names, Line: i + 1})
	}
	return out, nil
}

// SetHostRecords replaces the dashboard's block with records. An empty list
// removes the block; a file with no block gets one at its end.
func (s *Service) SetHostRecords(ctx context.Context, records []HostRecord) (*HostRecords, error) {
	clean, err := cleanHostRecords(records)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	path := hostFilePath(s.paths.Hosts)
	// A symbolic link is replaced by the rename that makes the write atomic,
	// which would turn the link into a file. Write through it instead.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	mode := fs.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	next, err := replaceHostsBlock(s.paths.Hosts, data, clean)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(next, data) {
		if err := writeFileAtomic(path, next, mode); err != nil {
			return nil, fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return s.HostRecords(ctx)
}

// replaceHostsBlock is the byte-level edit: the lines outside the block come
// back exactly as they were.
func replaceHostsBlock(path string, data []byte, records []HostRecord) ([]byte, error) {
	lines := splitHostsLines(data)
	span, err := findHostsBlock(path, lines)
	if err != nil {
		return nil, err
	}
	// The block is written with the line ending the file already uses.
	nl := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		nl = "\r\n"
	}
	var block bytes.Buffer
	if len(records) > 0 {
		block.WriteString(hostsBegin + nl)
		for _, r := range records {
			block.WriteString(r.Address + " " + strings.Join(r.Names, " ") + nl)
		}
		block.WriteString(hostsEnd + nl)
	}

	var out bytes.Buffer
	if span.begin < 0 {
		out.Write(data)
		if block.Len() == 0 {
			return out.Bytes(), nil
		}
		if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
			out.WriteString(nl)
		}
		out.Write(block.Bytes())
		return out.Bytes(), nil
	}
	for _, l := range lines[:span.begin] {
		out.Write(l)
	}
	last := lines[span.end]
	if block.Len() > 0 {
		// The end marker's own terminator is kept, so a block that ended the
		// file without a newline still does.
		b := block.Bytes()
		if !bytes.HasSuffix(last, []byte("\n")) {
			b = bytes.TrimSuffix(bytes.TrimSuffix(b, []byte("\n")), []byte("\r"))
		}
		out.Write(b)
	}
	for _, l := range lines[span.end+1:] {
		out.Write(l)
	}
	return out.Bytes(), nil
}

// cleanHostRecords validates the records as they will be written. Every value
// ends up in a line of a file every program on the host reads, so one that
// carried a newline would be a line of the request's choosing.
func cleanHostRecords(in []HostRecord) ([]HostRecord, error) {
	if len(in) > maxHostRecords {
		return nil, fmt.Errorf("at most %d records", maxHostRecords)
	}
	out := make([]HostRecord, 0, len(in))
	for i, r := range in {
		a, err := ParseAddr(r.Address)
		if err != nil {
			return nil, fmt.Errorf("record %d: %w", i+1, err)
		}
		if len(r.Names) == 0 {
			return nil, fmt.Errorf("record %d (%s): give it at least one name", i+1, a)
		}
		if len(r.Names) > maxHostNames {
			return nil, fmt.Errorf("record %d (%s): at most %d names on a line", i+1, a, maxHostNames)
		}
		names := make([]string, 0, len(r.Names))
		for _, n := range r.Names {
			n = strings.TrimSpace(n)
			if err := validDNSName(n, false); err != nil {
				return nil, fmt.Errorf("record %d (%s): %w", i+1, a, err)
			}
			if strings.HasSuffix(n, ".") {
				return nil, fmt.Errorf("record %d (%s): write %q without the trailing dot", i+1, a, n)
			}
			// localhost is the one name every program assumes means this
			// machine; pointing it elsewhere breaks things that are not
			// obviously about names.
			if isLoopbackName(n) && !a.IsLoopback() {
				return nil, fmt.Errorf("record %d: %q has to stay on a loopback address", i+1, n)
			}
			names = append(names, strings.ToLower(n))
		}
		out = append(out, HostRecord{Address: a.String(), Names: names})
	}
	return out, nil
}
