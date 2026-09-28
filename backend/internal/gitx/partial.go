package gitx

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type PartialDiff struct {
	File    string `json:"file"`
	Staged  bool   `json:"staged"`
	Body    string `json:"body"`
	Version string `json:"version"`
	Lines   []int  `json:"lines"`
	Reason  string `json:"reason,omitempty"`
	index   string
	mode    string
	object  string
}

type partialHunk struct {
	oldStart, oldCount, newStart, newCount int
	lines                                  []partialLine
}

type partialLine struct {
	id   int
	kind byte
	text string
}

var patchHunk = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

func parseHunks(body string) []partialHunk {
	hunks := []partialHunk{}
	for id, line := range strings.SplitAfter(body, "\n") {
		if m := patchHunk.FindStringSubmatch(line); m != nil {
			count := func(value string) int {
				if value == "" {
					return 1
				}
				n, _ := strconv.Atoi(value)
				return n
			}
			old, _ := strconv.Atoi(m[1])
			next, _ := strconv.Atoi(m[3])
			hunks = append(hunks, partialHunk{oldStart: old, oldCount: count(m[2]), newStart: next, newCount: count(m[4])})
			continue
		}
		if len(hunks) == 0 || line == "" {
			continue
		}
		h := &hunks[len(hunks)-1]
		if strings.HasPrefix(line, "\\ No newline") {
			if len(h.lines) > 0 {
				h.lines[len(h.lines)-1].text = strings.TrimSuffix(h.lines[len(h.lines)-1].text, "\n")
			}
		} else if line[0] == '+' || line[0] == '-' || line[0] == ' ' {
			h.lines = append(h.lines, partialLine{id: id, kind: line[0], text: line[1:]})
		}
	}
	return hunks
}

func (s *Service) PartialDiff(ctx context.Context, path, file string, staged bool) (*PartialDiff, error) {
	if _, err := repositoryEntry(path, file); err != nil {
		return nil, err
	}
	d := &PartialDiff{File: file, Staged: staged, Lines: []int{}}
	index, err := s.run(ctx, path, "ls-files", "--stage", "-z", "--", file)
	if err != nil {
		return nil, err
	}
	for _, entry := range strings.Split(index, "\x00") {
		info, name, ok := strings.Cut(entry, "\t")
		if !ok || name != file {
			continue
		}
		fields := strings.Fields(info)
		if len(fields) != 3 || fields[2] != "0" {
			return nil, fmt.Errorf("%w: resolve this conflict first", ErrInvalidRef)
		}
		d.mode, d.object = fields[0], fields[1]
	}
	args := []string{"--no-pager", "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", "--unified=3"}
	if staged {
		args = append(args, "--cached")
	}
	d.Body, err = s.run(ctx, path, append(args, "--", file)...)
	if err != nil {
		return nil, err
	}
	if d.Body == "" && !staged && d.object == "" {
		d.Body = s.diffUntracked(ctx, path, file)
	}
	d.Version = digest(index + "\x00" + d.Body)
	if strings.Count(d.Body, "diff --git ") > 1 {
		d.Reason = "Open one file to select lines, or stage this folder as a whole."
		return d, nil
	}
	if len(d.Body) > maxDiff || strings.Contains(d.Body, "… diff truncated") {
		d.Body = capDiff(d.Body)
		d.Reason = "This diff is too large for partial staging. Stage or unstage the whole file."
		return d, nil
	}
	if (d.mode != "" && d.mode != "100644" && d.mode != "100755") || strings.Contains(d.Body, " mode 120000") || strings.Contains(d.Body, " mode 160000") || !utf8.ValidString(d.Body) || strings.ContainsRune(d.Body, 0) {
		d.Reason = "Stage or unstage this binary file, symlink or submodule as a whole."
		return d, nil
	}
	if d.object != "" {
		size, err := s.run(ctx, path, "cat-file", "-s", d.object)
		if err != nil {
			return nil, err
		}
		n, _ := strconv.Atoi(strings.TrimSpace(size))
		if n > conflictLimit {
			d.Reason = "The file is too large for partial staging."
			return d, nil
		}
		d.index, err = s.run(ctx, path, "cat-file", "blob", d.object)
		if err != nil {
			return nil, err
		}
	}
	for _, h := range parseHunks(d.Body) {
		for _, line := range h.lines {
			if line.kind == '+' || line.kind == '-' {
				d.Lines = append(d.Lines, line.id)
			}
		}
	}
	if len(d.Lines) == 0 {
		d.Reason = "There are no textual lines to select. Stage or unstage the whole file."
	}
	return d, nil
}

type PartialStageRequest struct {
	File    string `json:"file"`
	Staged  bool   `json:"staged"`
	Version string `json:"version"`
	Lines   []int  `json:"lines"`
}

func (s *Service) StagePartial(ctx context.Context, path string, req PartialStageRequest) (*Result, error) {
	d, err := s.PartialDiff(ctx, path, req.File, req.Staged)
	if err != nil {
		return nil, err
	}
	if d.Reason != "" {
		return nil, fmt.Errorf("%w: %s", ErrInvalidRef, d.Reason)
	}
	if req.Version == "" || d.Version != req.Version {
		return nil, fmt.Errorf("%w: this diff changed; reload it before staging", ErrInvalidRef)
	}
	allowed := map[int]bool{}
	for _, line := range d.Lines {
		allowed[line] = true
	}
	selected := map[int]bool{}
	for _, line := range req.Lines {
		if !allowed[line] {
			return nil, fmt.Errorf("%w: select only changed lines in this diff", ErrInvalidRef)
		}
		selected[line] = true
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("%w: select at least one changed line", ErrInvalidRef)
	}
	content, err := partialContent(d.index, parseHunks(d.Body), selected, req.Staged)
	if err != nil {
		return nil, err
	}
	remove := content == "" && ((!req.Staged && strings.Contains(d.Body, "\ndeleted file mode ")) || (req.Staged && strings.Contains(d.Body, "\nnew file mode ")))
	mode := d.mode
	if mode == "" {
		mode = "100644"
		if strings.Contains(d.Body, "\nnew file mode 100755") || (req.Staged && strings.Contains(d.Body, "\ndeleted file mode 100755")) {
			mode = "100755"
		}
	}
	patch := contentPatch(req.File, d.index, content, d.object != "", !remove, mode)
	out, err := s.executeInput(ctx, path, time.Minute, patch, "apply", "--cached", "--whitespace=nowarn", "-")
	res := &Result{Command: "git apply --cached", Output: strings.TrimSpace(string(out)), OK: err == nil}
	if err == nil {
		res.Output = "Selected changes " + map[bool]string{true: "unstaged.", false: "staged."}[req.Staged]
	}
	return res, err
}

func contentLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.SplitAfter(content, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func partialContent(index string, hunks []partialHunk, selected map[int]bool, reverse bool) (string, error) {
	old := contentLines(index)
	var result strings.Builder
	position := 0
	for _, h := range hunks {
		start, count := h.oldStart, h.oldCount
		if reverse {
			start, count = h.newStart, h.newCount
		}
		if count > 0 {
			start--
		}
		if start < position || start > len(old) {
			return "", fmt.Errorf("%w: diff no longer matches the index", ErrInvalidRef)
		}
		for position < start {
			result.WriteString(old[position])
			position++
		}
		for _, line := range h.lines {
			kind := line.kind
			if reverse {
				if kind == '+' {
					kind = '-'
				} else if kind == '-' {
					kind = '+'
				}
			}
			switch kind {
			case '+':
				if selected[line.id] {
					result.WriteString(line.text)
				}
			case '-', ' ':
				if position >= len(old) || old[position] != line.text {
					return "", fmt.Errorf("%w: diff no longer matches the index", ErrInvalidRef)
				}
				if kind == ' ' || !selected[line.id] {
					result.WriteString(old[position])
				}
				position++
			}
		}
	}
	for position < len(old) {
		result.WriteString(old[position])
		position++
	}
	return result.String(), nil
}

func gitQuote(path string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, b := range []byte(path) {
		if b == '"' || b == '\\' {
			out.WriteByte('\\')
			out.WriteByte(b)
		} else if b < 32 || b >= 127 {
			fmt.Fprintf(&out, "\\%03o", b)
		} else {
			out.WriteByte(b)
		}
	}
	out.WriteByte('"')
	return out.String()
}

// A whole-file patch lets Git verify every old line against the index. The
// working file is never rewritten, and Git takes its own index lock to apply it.
func contentPatch(file, old, next string, oldExists, nextExists bool, mode string) string {
	a, b := gitQuote("a/"+file), gitQuote("b/"+file)
	var patch strings.Builder
	fmt.Fprintf(&patch, "diff --git %s %s\n", a, b)
	if !oldExists {
		fmt.Fprintf(&patch, "new file mode %s\n", mode)
		a = "/dev/null"
	}
	if !nextExists {
		fmt.Fprintf(&patch, "deleted file mode %s\n", mode)
		b = "/dev/null"
	}
	fmt.Fprintf(&patch, "--- %s\n+++ %s\n", a, b)
	before, after := contentLines(old), contentLines(next)
	start := func(lines []string) int {
		if len(lines) == 0 {
			return 0
		}
		return 1
	}
	fmt.Fprintf(&patch, "@@ -%d,%d +%d,%d @@\n", start(before), len(before), start(after), len(after))
	for i, lines := range [][]string{before, after} {
		for _, line := range lines {
			patch.WriteByte("-+"[i])
			patch.WriteString(line)
			if !strings.HasSuffix(line, "\n") {
				patch.WriteString("\n\\ No newline at end of file\n")
			}
		}
	}
	return patch.String()
}
