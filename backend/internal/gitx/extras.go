package gitx

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
)

type Submodule struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	URL         string `json:"url"`
	Head        string `json:"head,omitempty"`
	Expected    string `json:"expected,omitempty"`
	Initialized bool   `json:"initialized"`
	Dirty       bool   `json:"dirty"`
}

func (s *Service) Submodules(ctx context.Context, path string) ([]Submodule, error) {
	result := []Submodule{}
	config, err := files.New([]string{path}).Resolve(filepath.Join(path, ".gitmodules"))
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(config); os.IsNotExist(err) {
		return result, nil
	}
	out, err := s.runAllowing(ctx, path, 1, "config", "--file", config, "--null", "--get-regexp", `^submodule\..*\.path$`)
	if err != nil {
		return nil, err
	}
	for _, entry := range strings.Split(out, "\x00") {
		key, child, ok := strings.Cut(entry, "\n")
		if !ok {
			continue
		}
		full, err := repositoryEntry(path, child)
		if err != nil {
			return nil, err
		}
		resolved, err := files.New([]string{path}).Resolve(full)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "submodule."), ".path")
		url, _ := s.run(ctx, path, "config", "--file", config, "--get", "submodule."+name+".url")
		row := Submodule{Name: name, Path: child, URL: scrubRemote(strings.TrimSpace(url))}
		index, err := s.run(ctx, path, "ls-files", "--stage", "-z", "--", child)
		if err != nil {
			return nil, err
		}
		for _, e := range strings.Split(index, "\x00") {
			meta, p, ok := strings.Cut(e, "\t")
			fields := strings.Fields(meta)
			if ok && p == child && len(fields) == 3 && fields[0] == "160000" {
				row.Expected = fields[1]
				break
			}
		}
		if _, err := os.Lstat(filepath.Join(resolved, ".git")); err == nil {
			head, err := s.commitID(ctx, resolved, "HEAD")
			if err == nil {
				row.Initialized = true
				row.Head = head
				status, err := s.run(ctx, resolved, "status", "--porcelain=v1", "--untracked-files=all")
				if err != nil {
					return nil, err
				}
				row.Dirty = strings.TrimSpace(status) != ""
			}
		}
		result = append(result, row)
	}
	return result, nil
}

type SubmoduleRequest struct {
	Action string `json:"action"`
	Path   string `json:"path"`
	URL    string `json:"url"`
}

func (s *Service) ManageSubmodule(ctx context.Context, path string, req SubmoduleRequest) (*Result, error) {
	if filepath.Clean(req.Path) == "." || filepath.Clean(req.Path) != req.Path {
		return nil, fmt.Errorf("%w: name one submodule directory", ErrInvalidRef)
	}
	full, err := repositoryEntry(path, req.Path)
	if err != nil {
		return nil, err
	}
	if _, err := files.New([]string{path}).Resolve(full); err != nil {
		return nil, err
	}
	// Never run update commands from an account's submodule config. Network
	// transports keep the same restrictions as adding a top-level remote.
	args := []string{"-c", "protocol.file.allow=never", "-c", "protocol.ext.allow=never", "submodule"}
	if req.Action == "add" {
		if err := ValidateRemoteURL(req.URL); err != nil {
			return nil, err
		}
		if _, err := os.Lstat(full); !os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: choose a new submodule directory", ErrInvalidRef)
		}
		args = append(args, "add", "--", req.URL, req.Path)
	} else {
		rows, err := s.Submodules(ctx, path)
		if err != nil {
			return nil, err
		}
		var selected *Submodule
		for i := range rows {
			if rows[i].Path == req.Path {
				selected = &rows[i]
				break
			}
		}
		if selected == nil {
			return nil, fmt.Errorf("%w: unknown submodule", ErrInvalidRef)
		}
		if selected.Dirty && req.Action != "sync" {
			return nil, fmt.Errorf("%w: commit or stash changes in the submodule first", ErrInvalidRef)
		}
		switch req.Action {
		case "update":
			args = append(args, "update", "--init", "--checkout", "--", ":(literal)"+req.Path)
		case "sync":
			args = append(args, "sync", "--", ":(literal)"+req.Path)
		case "deinit":
			args = append(args, "deinit", "--", ":(literal)"+req.Path)
		case "remove":
			return s.op(ctx, path, time.Minute, "rm", "--", req.Path)
		default:
			return nil, fmt.Errorf("%w: unknown submodule operation", ErrInvalidRef)
		}
	}
	return s.op(ctx, path, 3*time.Minute, args...)
}

type LFSStatus struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Status    string `json:"status,omitempty"`
	Files     string `json:"files,omitempty"`
	Patterns  string `json:"patterns,omitempty"`
}

func (s *Service) LFS(ctx context.Context, path string) (*LFSStatus, error) {
	result := &LFSStatus{}
	if _, err := exec.LookPath("git-lfs"); err != nil {
		return result, nil
	}
	result.Available = true
	var err error
	result.Version, err = s.run(ctx, path, "lfs", "version")
	if err != nil {
		return nil, err
	}
	result.Status, err = s.run(ctx, path, "lfs", "status")
	if err != nil {
		return nil, err
	}
	result.Files, err = s.run(ctx, path, "lfs", "ls-files", "--long", "--size")
	if err != nil {
		return nil, err
	}
	result.Patterns, err = s.run(ctx, path, "lfs", "track")
	if err != nil {
		return nil, err
	}
	return result, nil
}
func (s *Service) ManageLFS(ctx context.Context, path, action, pattern string) (*Result, error) {
	if _, err := exec.LookPath("git-lfs"); err != nil {
		return nil, fmt.Errorf("%w: Git LFS is not installed on this host", ErrInvalidRef)
	}
	args := []string{"lfs"}
	switch action {
	case "install":
		args = append(args, "install", "--local")
	case "fetch":
		args = append(args, "fetch")
	case "pull":
		args = append(args, "pull")
	case "track", "untrack":
		if strings.TrimSpace(pattern) == "" || len(pattern) > 1024 || strings.ContainsAny(pattern, "\x00\r\n") || strings.HasPrefix(pattern, "-") {
			return nil, fmt.Errorf("%w: enter a file pattern", ErrInvalidRef)
		}
		if _, err := files.New([]string{path}).Resolve(filepath.Join(path, ".gitattributes")); err != nil {
			return nil, err
		}
		args = append(args, action, "--", pattern)
	default:
		return nil, fmt.Errorf("%w: unknown LFS action", ErrInvalidRef)
	}
	return s.op(ctx, path, 3*time.Minute, args...)
}

const exchangePatchLimit = 2 << 20

type PatchRequest struct {
	Body    string `json:"body"`
	Staged  bool   `json:"staged"`
	Version string `json:"version"`
}
type PatchCheck struct {
	Version string   `json:"version"`
	Summary string   `json:"summary"`
	Files   []string `json:"files"`
}

func (s *Service) ExportPatch(ctx context.Context, path, mode, ref string) (string, error) {
	args := []string{"diff", "--binary", "--full-index", "--no-ext-diff", "--no-textconv", "--no-color"}
	switch mode {
	case "working":
	case "staged":
		args = append(args, "--cached")
	case "commit":
		sha, err := s.commitID(ctx, path, ref)
		if err != nil {
			return "", err
		}
		args = []string{"show", "--format=", "--first-parent", "--binary", "--full-index", "--no-ext-diff", "--no-textconv", "--no-color", sha}
	default:
		return "", fmt.Errorf("%w: choose working, staged or commit", ErrInvalidRef)
	}
	out, err := s.run(ctx, path, append(args, "--")...)
	if err != nil {
		return "", err
	}
	if len(out) > exchangePatchLimit {
		return "", fmt.Errorf("%w: patch exceeds the 2 MiB exchange limit", ErrInvalidRef)
	}
	return out, nil
}

func (s *Service) CheckPatch(ctx context.Context, path string, req PatchRequest) (*PatchCheck, error) {
	if req.Body == "" || len(req.Body) > exchangePatchLimit {
		return nil, fmt.Errorf("%w: provide a patch of at most 2 MiB", ErrInvalidRef)
	}
	status, err := s.Status(ctx, path)
	if err != nil {
		return nil, err
	}
	if !status.Clean || status.Operation != "" {
		return nil, fmt.Errorf("%w: commit or stash changes before importing a patch", ErrInvalidRef)
	}
	head, err := s.commitID(ctx, path, "HEAD")
	if err != nil {
		return nil, err
	}
	out, err := s.executeInput(ctx, path, time.Minute, req.Body, "apply", "--numstat", "-z", "-")
	if err != nil {
		return nil, fmt.Errorf("%w: invalid patch: %s", ErrInvalidRef, strings.TrimSpace(string(out)))
	}
	result := &PatchCheck{Files: []string{}, Version: digest(head + "\x00" + req.Body + fmt.Sprint(req.Staged))}
	for _, entry := range strings.Split(string(out), "\x00") {
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "\t", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("%w: unsupported patch paths", ErrInvalidRef)
		}
		full, err := repositoryEntry(path, parts[2])
		if err != nil {
			return nil, err
		}
		if _, err := files.New([]string{path}).Resolve(full); err != nil {
			return nil, fmt.Errorf("%w: patch path leaves checkout", ErrInvalidRef)
		}
		result.Files = append(result.Files, parts[2])
	}
	if len(result.Files) == 0 {
		return nil, fmt.Errorf("%w: patch changes no files", ErrInvalidRef)
	}
	args := []string{"apply", "--check"}
	if req.Staged {
		args = append(args, "--index")
	}
	args = append(args, "-")
	out, err = s.executeInput(ctx, path, time.Minute, req.Body, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: patch does not apply: %s", ErrInvalidRef, strings.TrimSpace(string(out)))
	}
	summary, err := s.executeInput(ctx, path, time.Minute, req.Body, "apply", "--stat", "-")
	if err != nil {
		return nil, err
	}
	result.Summary = string(summary)
	return result, nil
}
func (s *Service) ImportPatch(ctx context.Context, path string, req PatchRequest) (*Result, error) {
	check, err := s.CheckPatch(ctx, path, req)
	if err != nil {
		return nil, err
	}
	if check.Version != req.Version {
		return nil, fmt.Errorf("%w: the patch or checkout changed; check it again", ErrInvalidRef)
	}
	args := []string{"apply", "--whitespace=nowarn"}
	if req.Staged {
		args = append(args, "--index")
	}
	args = append(args, "-")
	out, err := s.executeInput(ctx, path, time.Minute, req.Body, args...)
	res := &Result{Command: "git apply", Output: strings.TrimSpace(string(out)), OK: err == nil}
	if err != nil {
		return res, fmt.Errorf("git apply: %s", res.Output)
	}
	return res, nil
}
