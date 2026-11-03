package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

// What the assistant reads of a deployment's source: the files a person opens to
// learn what a project is (its README, its manifest, a source file) and the
// folders they sit in. It reads the private, temporary checkout detection reads
// (InspectRevision), at the commit a deployment would build now, so it runs
// nothing and keeps nothing. A read answers a model, so it is held to what fits
// in one of the assistant's answers, which the API cuts at 24,000 bytes.

const (
	// SourceReadPathsMax is how many paths one read takes.
	SourceReadPathsMax   = 8
	sourceReadFileMax    = 16 << 10
	sourceReadTotalMax   = 18 << 10
	sourceReadEntriesMax = 120
	// sourceReadSniff is how much of a file is looked at to tell text from
	// binary, as Git does.
	sourceReadSniff = 8000
)

// SourceRead is what one read of a deployment's source found.
type SourceRead struct {
	Repository   string         `json:"repository,omitempty"`
	Ref          string         `json:"ref,omitempty"`
	Commit       string         `json:"commit,omitempty"`
	Subdirectory string         `json:"subdirectory,omitempty"`
	Folders      []SourceFolder `json:"folders,omitempty"`
	Files        []SourceFile   `json:"files,omitempty"`
	// Unread are the paths that were asked for and not read, each with why.
	Unread []string `json:"unread,omitempty"`
}

// SourceFolder is one folder's listing, folders first.
type SourceFolder struct {
	Path    string        `json:"path"`
	Entries []SourceEntry `json:"entries"`
	// More is how many entries past the bound were left out.
	More int `json:"more,omitempty"`
}

// SourceEntry is one name in a folder.
type SourceEntry struct {
	Name string `json:"name"`
	// Kind is file, folder or link.
	Kind  string `json:"kind"`
	Bytes int64  `json:"bytes,omitempty"`
}

// SourceFile is one file's text.
type SourceFile struct {
	Path    string `json:"path"`
	Bytes   int64  `json:"bytes"`
	Content string `json:"content,omitempty"`
	// Cut says the content stops short of the file's end.
	Cut bool `json:"cut,omitempty"`
	// Binary is a file that is not text, whose content is not sent.
	Binary bool `json:"binary,omitempty"`
}

// readmeNames are what a read with no paths looks for in the top folder, in
// this order, whatever their case.
var readmeNames = []string{"readme.md", "readme", "readme.markdown", "readme.rst", "readme.txt", "readme.adoc"}

// ReadSource reads paths of an environment's source at the commit a deployment of
// it would build now: a folder is listed, a file's text is read. With no paths it
// lists the top folder and reads the README, which is what a person opens first.
func (c *DeploymentChecker) ReadSource(ctx context.Context, projectID, environmentID int64, paths []string) (*SourceRead, error) {
	plan, _, err := c.desiredPlan(ctx, projectID, environmentID)
	if err != nil {
		return nil, err
	}
	if plan == nil || !sourceHasTree(plan.Source) {
		return nil, ErrSourceHasNoTree
	}
	if c.sources == nil {
		return nil, fmt.Errorf("%w: %w: source inspection is unavailable", ErrSourceUnavailable, ErrGitUnavailable)
	}
	if err := c.resolveRevision(ctx, plan, DeploymentCheckRequest{}); err != nil {
		return nil, err
	}
	out := &SourceRead{Ref: plan.Identity.Ref, Subdirectory: plan.Source.Subdirectory}
	err = c.sources.InspectRevision(ctx, plan.Source, plan.Identity, func(root string, inspected SourceIdentity) error {
		out.Commit = inspected.Revision
		if inspected.Ref != "" {
			out.Ref = inspected.Ref
		}
		return readSourceTree(root, paths, out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SourceReadFailure words why a deployment's source could not be read, for the
// model that asked: without Git's own output, which never leaves the planning
// boundary.
func SourceReadFailure(err error) string {
	switch {
	case errors.Is(err, ErrSourceHasNoTree):
		return "this deployment is not built from a repository (it runs an image or a Compose file), so it has no source to read"
	case errors.Is(err, ErrNotFound):
		return "the deployment has no saved configuration to read its source from"
	}
	return "the source could not be read: " + sourceInspectionUnavailable(err)
}

// readSourceTree reads paths inside dir. os.Root keeps every open inside it, a
// symbolic link that leads out included.
func readSourceTree(dir string, paths []string, out *SourceRead) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	budget := sourceReadTotalMax
	if len(paths) == 0 {
		folder := listSourceFolder(root, ".", out)
		if name := readmeOf(folder); name != "" {
			if info, err := root.Stat(name); err == nil && info.Mode().IsRegular() {
				readSourceFile(root, name, info, &budget, out)
			}
		}
		return nil
	}
	seen := map[string]bool{}
	for _, raw := range paths {
		name, ok := sourcePath(raw)
		if !ok {
			out.Unread = append(out.Unread, fmt.Sprintf("%s: the repository's Git metadata is not read", strings.TrimSpace(raw)))
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		info, err := root.Stat(name)
		switch {
		case err != nil:
			if link, lerr := root.Lstat(name); lerr == nil && link.Mode()&fs.ModeSymlink != 0 {
				out.Unread = append(out.Unread, name+": a link that leads out of the repository, which is not read")
				break
			}
			out.Unread = append(out.Unread, name+": not in this commit")
		case info.IsDir():
			listSourceFolder(root, name, out)
		case info.Mode().IsRegular():
			readSourceFile(root, name, info, &budget, out)
		default:
			out.Unread = append(out.Unread, name+": not a file or a folder")
		}
	}
	return nil
}

// sourcePath is a path the model wrote as one inside the checkout: slashes
// forward, no way above the top, "" for the top itself. The .git folder is not
// the project's and is refused.
func sourcePath(raw string) (string, bool) {
	p := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	if p == "" {
		p = "."
	}
	if p == ".git" || strings.HasPrefix(p, ".git/") || strings.ContainsRune(p, 0) {
		return "", false
	}
	return p, true
}

// listSourceFolder lists one folder, folders first and then files, each by name.
func listSourceFolder(root *os.Root, name string, out *SourceRead) *SourceFolder {
	dir, err := root.Open(name)
	if err != nil {
		out.Unread = append(out.Unread, name+": could not be opened")
		return nil
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		out.Unread = append(out.Unread, name+": could not be listed")
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})
	folder := SourceFolder{Path: name, Entries: []SourceEntry{}}
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		if len(folder.Entries) == sourceReadEntriesMax {
			folder.More++
			continue
		}
		entry := SourceEntry{Name: e.Name(), Kind: "file"}
		switch {
		case e.IsDir():
			entry.Kind = "folder"
		case e.Type()&fs.ModeSymlink != 0:
			entry.Kind = "link"
		default:
			if info, err := e.Info(); err == nil {
				entry.Bytes = info.Size()
			}
		}
		folder.Entries = append(folder.Entries, entry)
	}
	out.Folders = append(out.Folders, folder)
	return &out.Folders[len(out.Folders)-1]
}

// readmeOf is the folder's README, by the first of readmeNames it has.
func readmeOf(folder *SourceFolder) string {
	if folder == nil {
		return ""
	}
	for _, want := range readmeNames {
		for _, entry := range folder.Entries {
			if entry.Kind == "file" && strings.EqualFold(entry.Name, want) {
				return entry.Name
			}
		}
	}
	return ""
}

// readSourceFile reads one file's text, as much of it as the read has left.
func readSourceFile(root *os.Root, name string, info fs.FileInfo, budget *int, out *SourceRead) {
	limit := min(sourceReadFileMax, *budget)
	if limit <= 0 {
		out.Unread = append(out.Unread, name+": not read, because this read's size is spent; read it on its own")
		return
	}
	f, err := root.Open(name)
	if err != nil {
		out.Unread = append(out.Unread, name+": could not be opened")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		out.Unread = append(out.Unread, name+": could not be read")
		return
	}
	file := SourceFile{Path: name, Bytes: info.Size()}
	if len(data) > limit {
		data, file.Cut = data[:limit], true
	}
	if bytes.IndexByte(data[:min(len(data), sourceReadSniff)], 0) >= 0 {
		file.Binary = true
		out.Files = append(out.Files, file)
		return
	}
	*budget -= len(data)
	file.Content = strings.ToValidUTF8(string(data), "�")
	out.Files = append(out.Files, file)
}
