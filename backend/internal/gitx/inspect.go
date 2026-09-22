package gitx

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type ReflogEntry struct {
	SHA      string    `json:"sha"`
	Selector string    `json:"selector"`
	Message  string    `json:"message"`
	Author   string    `json:"author"`
	At       time.Time `json:"at"`
}

// Reflog only reads HEAD's journal. Recovery creates a new branch through the
// existing branch operation; reading an old position never moves the current one.
func (s *Service) Reflog(ctx context.Context, path string, skip int) ([]ReflogEntry, error) {
	if skip < 0 {
		return nil, fmt.Errorf("%w: negative offset", ErrInvalidRef)
	}
	out, err := s.run(ctx, path, "reflog", "show", "--max-count=100", "--skip="+strconv.Itoa(skip),
		"--date=iso-strict", "--format=%H%x1f%gd%x1f%gs%x1f%gn", "HEAD", "--")
	if err != nil {
		return nil, err
	}
	entries := []ReflogEntry{}
	for _, line := range nonEmptyLines(out) {
		p := strings.SplitN(line, "\x1f", 4)
		if len(p) != 4 {
			continue
		}
		e := ReflogEntry{SHA: p[0], Selector: p[1], Message: p[2], Author: p[3]}
		if start := strings.Index(p[1], "@{"); start >= 0 {
			e.At, _ = time.Parse(time.RFC3339, strings.TrimSuffix(p[1][start+2:], "}"))
		}
		entries = append(entries, e)
	}
	return entries, nil
}

type BlameLine struct {
	SHA          string    `json:"sha"`
	Line         int       `json:"line"`
	OriginalLine int       `json:"originalLine"`
	Author       string    `json:"author"`
	At           time.Time `json:"at"`
	Subject      string    `json:"subject"`
	Content      string    `json:"content"`
}

type Blame struct {
	Ref     string      `json:"ref"`
	File    string      `json:"file"`
	Lines   []BlameLine `json:"lines"`
	HasMore bool        `json:"hasMore"`
}

func (s *Service) commitID(ctx context.Context, path, ref string) (string, error) {
	if err := ValidateRef(ref); err != nil {
		return "", err
	}
	out, err := s.run(ctx, path, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%w: cannot read commit %s", ErrInvalidRef, ref)
	}
	return strings.TrimSpace(out), nil
}

func (s *Service) Blame(ctx context.Context, path, ref, file string, start int) (*Blame, error) {
	if ref == "" {
		ref = "HEAD"
	}
	sha, err := s.commitID(ctx, path, ref)
	if err != nil {
		return nil, err
	}
	if err := validatePath(file); err != nil {
		return nil, err
	}
	if start < 1 {
		start = 1
	}
	if start > 10000000 {
		return nil, fmt.Errorf("%w: line is too large", ErrInvalidRef)
	}
	out, err := s.run(ctx, path, "blame", "--line-porcelain", "-L", fmt.Sprintf("%d,%d", start, start+200), sha, "--", file)
	if err != nil {
		return nil, err
	}
	result := &Blame{Ref: sha, File: file, Lines: []BlameLine{}}
	var line BlameLine
	for _, text := range strings.Split(out, "\n") {
		if strings.HasPrefix(text, "\t") {
			line.Content = text[1:]
			result.Lines = append(result.Lines, line)
			continue
		}
		key, value, _ := strings.Cut(text, " ")
		switch key {
		case "author":
			line.Author = value
		case "author-time":
			secs, _ := strconv.ParseInt(value, 10, 64)
			line.At = time.Unix(secs, 0).UTC()
		case "summary":
			line.Subject = value
		default:
			fields := strings.Fields(text)
			if len(fields) >= 3 && (len(fields[0]) == 40 || len(fields[0]) == 64) {
				line = BlameLine{SHA: fields[0]}
				line.OriginalLine, _ = strconv.Atoi(fields[1])
				line.Line, _ = strconv.Atoi(fields[2])
			}
		}
	}
	if len(result.Lines) > 200 {
		result.Lines = result.Lines[:200]
		result.HasMore = true
	}
	return result, nil
}

type Signature struct {
	Status      string `json:"status"`
	Signer      string `json:"signer,omitempty"`
	Key         string `json:"key,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

func (s *Service) Signature(ctx context.Context, path, ref string) (*Signature, error) {
	sha, err := s.commitID(ctx, path, ref)
	if err != nil {
		return nil, err
	}
	out, err := s.run(ctx, path, "show", "--no-patch", "--format=JD_SIGNATURE%x1f%G?%x1f%GS%x1f%GK%x1f%GF", sha, "--")
	if err != nil {
		return nil, err
	}
	// A verifier can write diagnostics before Git's formatted record (notably
	// SSH without an allowed-signers file). Keep that diagnostic from becoming
	// a status code or accidentally presenting an unchecked signature as good.
	start := strings.LastIndex(out, "JD_SIGNATURE\x1f")
	if start < 0 {
		return nil, fmt.Errorf("could not read commit signature")
	}
	p := strings.Split(strings.TrimSpace(out[start+len("JD_SIGNATURE\x1f"):]), "\x1f")
	if len(p) != 4 {
		return nil, fmt.Errorf("could not read commit signature")
	}
	if p[0] == "N" {
		// Some Git versions report N for an SSH signature when no trust file
		// exists. The object header distinguishes unsigned from uncheckable.
		object, err := s.run(ctx, path, "cat-file", "commit", sha)
		if err != nil {
			return nil, err
		}
		header, _, _ := strings.Cut(object, "\n\n")
		if strings.Contains("\n"+header, "\ngpgsig ") || strings.Contains("\n"+header, "\ngpgsig-sha256 ") {
			p[0] = "E"
		}
	}
	return &Signature{Status: p[0], Signer: p[1], Key: p[2], Fingerprint: p[3]}, nil
}

// CompareDiff freezes both sides before producing the merge-base diff. Passing
// the returned object ids on later reads keeps a review stable if a branch moves.
func (s *Service) CompareDiff(ctx context.Context, path, base, head, file string) (string, error) {
	baseID, err := s.commitID(ctx, path, base)
	if err != nil {
		return "", err
	}
	headID, err := s.commitID(ctx, path, head)
	if err != nil {
		return "", err
	}
	args := []string{"--no-pager", "diff", "--no-ext-diff", "--no-textconv", "--no-color", "-M", baseID + "..." + headID, "--"}
	if file != "" {
		if err := validatePath(file); err != nil {
			return "", err
		}
		args = append(args, file)
	}
	out, err := s.run(ctx, path, args...)
	return capDiff(out), err
}
