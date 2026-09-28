package gitx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

const conflictLimit = 2 << 20

type ConflictSide struct {
	Present bool   `json:"present"`
	Content string `json:"content"`
	Binary  bool   `json:"binary"`
	Mode    string `json:"mode,omitempty"`
	Object  string `json:"object,omitempty"`
}

type Conflict struct {
	File      string       `json:"file"`
	Version   string       `json:"version"`
	Base      ConflictSide `json:"base"`
	Ours      ConflictSide `json:"ours"`
	Theirs    ConflictSide `json:"theirs"`
	Result    string       `json:"result"`
	Editable  bool         `json:"editable"`
	Operation string       `json:"operation"`
}

func repositoryEntry(path, file string) (string, error) {
	if err := validatePath(file); err != nil {
		return "", err
	}
	for _, part := range strings.Split(file, "/") {
		if strings.EqualFold(part, ".git") {
			return "", fmt.Errorf("%w: Git metadata is not a working file", ErrInvalidRef)
		}
	}
	full, err := files.New([]string{path}).ResolveEntry(filepath.Join(path, file))
	if err != nil {
		return "", fmt.Errorf("%w: file must stay inside the checkout", ErrInvalidRef)
	}
	return full, nil
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *Service) Conflict(ctx context.Context, path, file string) (*Conflict, error) {
	full, err := repositoryEntry(path, file)
	if err != nil {
		return nil, err
	}
	entries, err := s.run(ctx, path, "ls-files", "--unmerged", "-z", "--", file)
	if err != nil {
		return nil, err
	}
	result := &Conflict{File: file, Editable: true, Operation: s.operationInProgress(ctx, path)}
	sides := []*ConflictSide{nil, &result.Base, &result.Ours, &result.Theirs}
	count := 0
	for _, entry := range strings.Split(entries, "\x00") {
		info, name, ok := strings.Cut(entry, "\t")
		if !ok || name != file {
			continue
		}
		fields := strings.Fields(info)
		if len(fields) != 3 {
			continue
		}
		stage, _ := strconv.Atoi(fields[2])
		if stage < 1 || stage > 3 {
			continue
		}
		side := sides[stage]
		side.Present, side.Mode, side.Object = true, fields[0], fields[1]
		count++
		if side.Mode == "160000" {
			side.Content = "Submodule commit " + side.Object
			side.Binary, result.Editable = true, false
			continue
		}
		size, err := s.run(ctx, path, "cat-file", "-s", side.Object)
		if err != nil {
			return nil, err
		}
		n, _ := strconv.Atoi(strings.TrimSpace(size))
		if n > conflictLimit || side.Mode != "100644" && side.Mode != "100755" {
			side.Binary, result.Editable = true, false
			continue
		}
		side.Content, err = s.run(ctx, path, "cat-file", "blob", side.Object)
		if err != nil {
			return nil, err
		}
		side.Binary = strings.ContainsRune(side.Content, 0) || !utf8.ValidString(side.Content)
		if side.Binary {
			side.Content = ""
			result.Editable = false
		}
	}
	if count == 0 {
		return nil, fmt.Errorf("%w: this file has no unresolved conflict", ErrInvalidRef)
	}
	work := "missing"
	if st, err := os.Lstat(full); err == nil {
		if st.Mode().IsRegular() && st.Size() <= conflictLimit {
			resolved, err := files.New([]string{path}).Resolve(full)
			if err != nil {
				return nil, err
			}
			f, err := os.Open(resolved)
			if err != nil {
				return nil, err
			}
			body, readErr := io.ReadAll(io.LimitReader(f, conflictLimit+1))
			f.Close()
			if readErr != nil {
				return nil, readErr
			}
			work = string(body)
			if len(body) > conflictLimit || !utf8.Valid(body) || strings.ContainsRune(work, 0) {
				result.Editable = false
			} else {
				result.Result = work
			}
		} else {
			result.Editable = false
			work = fmt.Sprintf("%s:%d:%d", st.Mode(), st.Size(), st.ModTime().UnixNano())
			if st.Mode()&os.ModeSymlink != 0 {
				work, _ = os.Readlink(full)
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	result.Version = digest(entries + "\x00" + work)
	return result, nil
}

type ResolveConflictRequest struct {
	File    string `json:"file"`
	Version string `json:"version"`
	Choice  string `json:"choice"`
	Content string `json:"content"`
}

func (s *Service) ResolveConflict(ctx context.Context, path string, req ResolveConflictRequest) (*Result, error) {
	conflict, err := s.Conflict(ctx, path, req.File)
	if err != nil {
		return nil, err
	}
	if req.Version == "" || req.Version != conflict.Version {
		return nil, fmt.Errorf("%w: the conflict changed; reload it before resolving", ErrInvalidRef)
	}
	full, err := repositoryEntry(path, req.File)
	if err != nil {
		return nil, err
	}
	switch req.Choice {
	case "result":
		if !conflict.Editable || len(req.Content) > conflictLimit || !utf8.ValidString(req.Content) || strings.ContainsRune(req.Content, 0) {
			return nil, fmt.Errorf("%w: only text conflicts up to 2 MB can be edited", ErrInvalidRef)
		}
		_, before := os.Lstat(full)
		if err := files.New([]string{path}).Write(full, req.Content); err != nil {
			return nil, err
		}
		if os.IsNotExist(before) {
			if owner, ok := hostexec.OwnerOf(path); ok {
				if err := os.Chown(full, int(owner.UID), int(owner.GID)); err != nil {
					return nil, err
				}
			}
		}
	case "ours", "theirs":
		side := conflict.Ours
		if req.Choice == "theirs" {
			side = conflict.Theirs
		}
		if !side.Present {
			return nil, fmt.Errorf("%w: this side deleted the file; choose deletion explicitly", ErrInvalidRef)
		}
		if side.Mode == "160000" {
			return s.op(ctx, path, time.Minute, "update-index", "--add", "--cacheinfo", "160000", side.Object, req.File)
		}
		if res, err := s.op(ctx, path, time.Minute, "checkout", "--"+req.Choice, "--", req.File); err != nil {
			return res, err
		}
	case "delete":
		return s.op(ctx, path, time.Minute, "rm", "-f", "--", req.File)
	default:
		return nil, fmt.Errorf("%w: choose a result, current version, incoming version or deletion", ErrInvalidRef)
	}
	return s.Stage(ctx, path, []string{req.File})
}

// Interactive operations start only from a clean checkout. Unlike the legacy
// operations, they retain a conflict so the workspace can resolve and continue it.
func (s *Service) StartOperation(ctx context.Context, path, operation, ref string) (*Result, error) {
	if operation != "merge" && operation != "cherry-pick" && operation != "revert" {
		return nil, fmt.Errorf("%w: unknown operation", ErrInvalidRef)
	}
	sha, err := s.commitID(ctx, path, ref)
	if err != nil {
		return nil, err
	}
	status, err := s.Status(ctx, path)
	if err != nil {
		return nil, err
	}
	if !status.Clean || status.Operation != "" {
		return nil, fmt.Errorf("%w: commit or stash changes and finish the current operation first", ErrInvalidRef)
	}
	args := []string{operation}
	if operation == "merge" {
		args = append(args, "--no-edit")
	}
	args = append(args, sha)
	return s.op(ctx, path, 2*time.Minute, args...)
}

func (s *Service) ContinueOperation(ctx context.Context, path string) (*Result, error) {
	operation := s.operationInProgress(ctx, path)
	if operation != "merge" && operation != "cherry-pick" && operation != "revert" && operation != "rebase" {
		return nil, fmt.Errorf("%w: there is no operation to continue", ErrInvalidRef)
	}
	unmerged, err := s.run(ctx, path, "ls-files", "--unmerged", "-z")
	if err != nil {
		return nil, err
	}
	if unmerged != "" {
		return nil, fmt.Errorf("%w: resolve every conflicted file first", ErrInvalidRef)
	}
	if operation == "rebase" {
		plan, err := s.rebasePlanPath(ctx, path)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(plan); err == nil {
			return s.rebaseCommand(ctx, path, plan, "--continue")
		}
	}
	return s.op(ctx, path, 2*time.Minute, operation, "--continue")
}

func (s *Service) AbortOperation(ctx context.Context, path string) (*Result, error) {
	operation := s.operationInProgress(ctx, path)
	if operation != "merge" && operation != "cherry-pick" && operation != "revert" && operation != "rebase" {
		return nil, fmt.Errorf("%w: there is no operation to abort", ErrInvalidRef)
	}
	res, err := s.op(ctx, path, 2*time.Minute, operation, "--abort")
	if err == nil && operation == "rebase" {
		if plan, e := s.rebasePlanPath(ctx, path); e == nil {
			_ = os.Remove(plan)
		}
	}
	return res, err
}
