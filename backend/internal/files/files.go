// Package files backs the file manager. Every path that arrives from a client
// is resolved through Resolve before it is touched: that single choke point is
// what prevents ../ traversal and symlink escapes from reaching outside the
// configured roots.
package files

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	ErrOutsideRoot = errors.New("path is outside the permitted roots")
	ErrTooLarge    = errors.New("file is too large for the in-browser editor")
	ErrIsDir       = errors.New("path is a directory")
	ErrNotDir      = errors.New("path is not a directory")
)

// maxEditBytes bounds what the editor will load. Monaco is not usable beyond
// this and the browser tab would be, in practice, unusable well before it.
const maxEditBytes = 8 << 20

type Service struct {
	roots     []string
	extractMu sync.Mutex
}

func New(roots []string) *Service {
	cleaned := make([]string, 0, len(roots))
	for _, r := range roots {
		if abs, err := filepath.Abs(r); err == nil {
			cleaned = append(cleaned, filepath.Clean(abs))
		}
	}
	if len(cleaned) == 0 {
		cleaned = []string{"/"}
	}
	return &Service{roots: cleaned}
}

func (s *Service) Roots() []string { return s.roots }

// Resolve validates a client-supplied path. It checks the literal cleaned path
// and the nearest existing ancestor after resolving symlinks — a symlink inside
// a root pointing outside it must not become a way out, even when several path
// components below that link do not exist yet.
func (s *Service) Resolve(path string) (string, error) {
	if path == "" {
		path = s.roots[0]
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	if !s.within(abs) {
		return "", fmt.Errorf("%w: %s", ErrOutsideRoot, path)
	}
	resolved, err := resolveFromExistingAncestor(abs)
	if err != nil {
		return "", err
	}
	if !s.within(resolved) {
		return "", fmt.Errorf("%w: %s resolves outside the permitted roots", ErrOutsideRoot, path)
	}
	return resolved, nil
}

// ResolveEntry validates a client-supplied path the same way Resolve does, but
// returns the entry itself rather than what it points at.
//
// Resolve dereferences, which is right for reading and writing *through* a
// symlink and wrong for every operation that acts *on* one. With Resolve alone,
// deleting the symlink "current -> releases/v1" removed the release directory
// and left the link dangling, and Stat on it always reported isSymlink:false —
// the listing showed a link and the operation hit the data. The containment
// guarantee is unchanged: the parent chain is still resolved and still has to
// land inside the roots.
func (s *Service) ResolveEntry(path string) (string, error) {
	if path == "" {
		path = s.roots[0]
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	if !s.within(abs) {
		return "", fmt.Errorf("%w: %s", ErrOutsideRoot, path)
	}
	// A configured root has no parent inside the roots to check against, so it
	// is judged on the literal form alone — which has already passed.
	for _, root := range s.roots {
		if abs == root {
			return abs, nil
		}
	}
	parent, err := resolveFromExistingAncestor(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	if !s.within(parent) {
		return "", fmt.Errorf("%w: %s resolves outside the permitted roots", ErrOutsideRoot, path)
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

// resolveFromExistingAncestor preserves the missing suffix but resolves every
// ancestor that already exists. Checking only the immediate parent leaves an
// outward symlink hidden whenever two new directories follow it.
func resolveFromExistingAncestor(path string) (string, error) {
	current := path
	missing := []string{}
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		// A broken symlink exists even though EvalSymlinks reports ENOENT. Do
		// not treat it as an ordinary missing component: its target could be
		// created outside the roots after this check.
		if _, lstatErr := os.Lstat(current); lstatErr == nil {
			return "", err
		} else if !os.IsNotExist(lstatErr) {
			return "", lstatErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func (s *Service) within(abs string) bool {
	for _, root := range s.roots {
		if root == "/" || abs == root || strings.HasPrefix(abs, root+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

type Entry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	Mode       string    `json:"mode"`
	ModeOctal  string    `json:"modeOctal"`
	IsDir      bool      `json:"isDir"`
	IsSymlink  bool      `json:"isSymlink"`
	LinkTarget string    `json:"linkTarget,omitempty"`
	LinkBroken bool      `json:"linkBroken,omitempty"`
	Modified   time.Time `json:"modified"`
	Owner      string    `json:"owner"`
	Group      string    `json:"group"`
	UID        uint32    `json:"uid"`
	GID        uint32    `json:"gid"`
	MimeHint   string    `json:"mimeHint,omitempty"`
	ChildCount *int      `json:"childCount,omitempty"`
}

type Listing struct {
	Path    string   `json:"path"`
	Parent  string   `json:"parent"`
	Entries []Entry  `json:"entries"`
	Roots   []string `json:"roots"`
}

func (s *Service) List(path string, showHidden bool) (*Listing, error) {
	dir, err := s.Resolve(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, ErrNotDir
	}
	names, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := &Listing{Path: dir, Parent: filepath.Dir(dir), Entries: []Entry{}, Roots: s.roots}
	for _, d := range names {
		if !showHidden && strings.HasPrefix(d.Name(), ".") {
			continue
		}
		full := filepath.Join(dir, d.Name())
		e, err := s.entry(full, d.Name())
		if err != nil {
			continue
		}
		out.Entries = append(out.Entries, *e)
	}
	// Directories first, then case-insensitive by name — the ordering every
	// file manager uses, and the one that makes navigation predictable.
	sort.Slice(out.Entries, func(i, j int) bool {
		if out.Entries[i].IsDir != out.Entries[j].IsDir {
			return out.Entries[i].IsDir
		}
		return strings.ToLower(out.Entries[i].Name) < strings.ToLower(out.Entries[j].Name)
	})
	return out, nil
}

func (s *Service) entry(full, name string) (*Entry, error) {
	// Lstat, not Stat: the listing must show the link itself, including one
	// whose target is missing.
	li, err := os.Lstat(full)
	if err != nil {
		return nil, err
	}
	e := &Entry{
		Name: name, Path: full, Size: li.Size(),
		Mode: li.Mode().String(), ModeOctal: fmt.Sprintf("%04o", li.Mode().Perm()),
		Modified: li.ModTime().UTC(),
	}
	if li.Mode()&os.ModeSymlink != 0 {
		e.IsSymlink = true
		if target, err := os.Readlink(full); err == nil {
			e.LinkTarget = target
		}
		target, err := os.Stat(full)
		if err != nil {
			e.LinkBroken = true
		} else {
			e.IsDir = target.IsDir()
			e.Size = target.Size()
		}
	} else {
		e.IsDir = li.IsDir()
	}
	if st, ok := li.Sys().(*syscall.Stat_t); ok {
		e.UID, e.GID = st.Uid, st.Gid
		e.Owner = lookupUser(st.Uid)
		e.Group = lookupGroup(st.Gid)
	}
	if !e.IsDir {
		e.MimeHint = mimeHint(name)
	}
	return e, nil
}

// User and group lookups hit NSS, which can be slow; a directory listing does
// hundreds of them, so results are memoised for the process lifetime.
//
// The lock is not optional. These are filled from entry(), which runs on the
// request goroutine for /files/list, /files/stat and /files/search, so two
// operators browsing at once wrote the same map concurrently. A concurrent map
// write is a runtime throw, not a panic: httpx.Recoverer cannot catch it and
// the process dies, taking every open PTY, log tail and metrics socket with it.
var (
	nssMu      sync.RWMutex
	userCache  = map[uint32]string{}
	groupCache = map[uint32]string{}
)

func lookupUser(uid uint32) string {
	nssMu.RLock()
	v, ok := userCache[uid]
	nssMu.RUnlock()
	if ok {
		return v
	}
	name := strconv.FormatUint(uint64(uid), 10)
	if u, err := user.LookupId(name); err == nil {
		name = u.Username
	}
	nssMu.Lock()
	userCache[uid] = name
	nssMu.Unlock()
	return name
}

func lookupGroup(gid uint32) string {
	nssMu.RLock()
	v, ok := groupCache[gid]
	nssMu.RUnlock()
	if ok {
		return v
	}
	name := strconv.FormatUint(uint64(gid), 10)
	if g, err := user.LookupGroupId(name); err == nil {
		name = g.Name
	}
	nssMu.Lock()
	groupCache[gid] = name
	nssMu.Unlock()
	return name
}

// editorLanguages is the extension the editor and the preview both key off.
//
// It is deliberately wider than the languages Monaco highlights: an extension
// missing from here is not merely unhighlighted, it is a file the preview has
// to fall back to sniffing bytes for, and a `.tf` or a `.vue` reported as
// "plaintext" reads to the operator as "the dashboard does not know what this
// is". Monaco ignores an id it has no grammar for, so an entry costs nothing.
var editorLanguages = map[string]string{
	".go": "go", ".js": "javascript", ".mjs": "javascript", ".cjs": "javascript",
	".ts": "typescript", ".tsx": "typescript", ".jsx": "javascript", ".mts": "typescript",
	".json": "json", ".jsonc": "json", ".json5": "json",
	".yml": "yaml", ".yaml": "yaml", ".toml": "toml",
	".md": "markdown", ".mdx": "markdown", ".rst": "plaintext",
	".sh": "shell", ".bash": "shell", ".zsh": "shell", ".fish": "shell",
	".py": "python", ".rb": "ruby", ".rs": "rust", ".php": "php",
	".sql": "sql", ".html": "html", ".htm": "html", ".css": "css",
	".scss": "scss", ".less": "less", ".vue": "html", ".svelte": "html",
	".xml": "xml", ".svg": "xml", ".ini": "ini", ".conf": "ini", ".cfg": "ini",
	".env": "shell", ".properties": "ini",
	".dockerfile": "dockerfile", ".service": "ini", ".socket": "ini",
	".timer": "ini", ".mount": "ini", ".nginx": "nginx",
	".c": "c", ".h": "c", ".cpp": "cpp", ".cc": "cpp", ".hpp": "cpp",
	".cs": "csharp", ".java": "java", ".kt": "kotlin", ".kts": "kotlin",
	".swift": "swift", ".m": "objective-c", ".scala": "scala", ".clj": "clojure",
	".ex": "elixir", ".exs": "elixir", ".erl": "erlang", ".hs": "haskell",
	".lua": "lua", ".pl": "perl", ".r": "r", ".dart": "dart", ".zig": "plaintext",
	".ps1": "powershell", ".bat": "bat", ".cmd": "bat", ".vim": "plaintext",
	".tf": "hcl", ".tfvars": "hcl", ".hcl": "hcl", ".proto": "proto",
	".graphql": "graphql", ".gql": "graphql", ".prisma": "plaintext",
	".diff": "diff", ".patch": "diff", ".csv": "plaintext", ".tsv": "plaintext",
	".txt": "plaintext", ".log": "plaintext", ".list": "plaintext",
}

// namedFiles are the files a server keeps that have no extension at all, and
// which are therefore invisible to any mapping that only looks at one.
var namedFiles = map[string]string{
	"dockerfile": "dockerfile", "containerfile": "dockerfile",
	"makefile": "makefile", "gnumakefile": "makefile",
	"caddyfile": "caddyfile", "vagrantfile": "ruby", "gemfile": "ruby",
	"rakefile": "ruby", "brewfile": "ruby", "procfile": "yaml",
	"license": "plaintext", "readme": "markdown", "changelog": "markdown",
	"authorized_keys": "plaintext", "known_hosts": "plaintext",
	"hosts": "plaintext", "fstab": "plaintext", "crontab": "plaintext",
	".gitignore": "plaintext", ".dockerignore": "plaintext",
	".env": "shell", ".bashrc": "shell", ".zshrc": "shell", ".profile": "shell",
	".bash_profile": "shell", ".editorconfig": "ini", ".gitconfig": "ini",
	".npmrc": "ini", ".eslintrc": "json", ".prettierrc": "json",
}

func mimeHint(name string) string {
	lower := strings.ToLower(name)
	if lang, ok := namedFiles[lower]; ok {
		return lang
	}
	if lang, ok := editorLanguages[filepath.Ext(lower)]; ok {
		return lang
	}
	// `nginx.conf.bak`, `app.service.old`: the interesting extension is the
	// one before the suffix that says this is a copy.
	if backupSuffixes[filepath.Ext(lower)] {
		if lang, ok := editorLanguages[filepath.Ext(strings.TrimSuffix(lower, filepath.Ext(lower)))]; ok {
			return lang
		}
	}
	return "plaintext"
}

var backupSuffixes = map[string]bool{
	".bak": true, ".old": true, ".orig": true, ".save": true, ".dpkg-old": true,
	".rpmsave": true, ".disabled": true,
}

type FileContent struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Size     int64  `json:"size"`
	Language string `json:"language"`
	Binary   bool   `json:"binary"`
	Mode     string `json:"modeOctal"`
}

func (s *Service) Read(path string) (*FileContent, error) {
	full, err := s.Resolve(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, ErrIsDir
	}
	if st.Size() > maxEditBytes {
		return nil, fmt.Errorf("%w: %d bytes exceeds the %d byte limit", ErrTooLarge, st.Size(), maxEditBytes)
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	fc := &FileContent{
		Path: full, Size: st.Size(),
		Language: mimeHint(filepath.Base(full)),
		Mode:     fmt.Sprintf("%04o", st.Mode().Perm()),
		Binary:   looksBinary(b),
	}
	if !fc.Binary {
		fc.Content = string(b)
	}
	return fc, nil
}

// looksBinary uses the same heuristic as most editors: a NUL byte in the first
// kilobyte means this is not text worth loading into a code editor.
func looksBinary(b []byte) bool {
	n := len(b)
	if n > 1024 {
		n = 1024
	}
	for i := 0; i < n; i++ {
		if b[i] == 0 {
			return true
		}
	}
	return false
}

// Write replaces a file's contents atomically: the new content lands in a
// temporary file in the same directory and is renamed over the target, so an
// interrupted save can never leave a half-written config behind.
func (s *Service) Write(path, content string) error {
	full, err := s.Resolve(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(full)
	perm := os.FileMode(0o644)
	var uid, gid = -1, -1
	if st, err := os.Stat(full); err == nil {
		if st.IsDir() {
			return ErrIsDir
		}
		perm = st.Mode().Perm()
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(sys.Uid), int(sys.Gid)
		}
	}
	tmp, err := os.CreateTemp(dir, ".vpsd-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if uid >= 0 {
		os.Chown(tmpName, uid, gid)
	}
	return os.Rename(tmpName, full)
}

// Mkdir is the "New folder" verb. Missing parents are created on the way,
// but the folder itself has to be new: MkdirAll alone answered "Created x"
// for a folder that was already there, which is a success message about
// nothing having happened, and the same name typed twice is the common way
// to arrive here.
func (s *Service) Mkdir(path string, mode os.FileMode) error {
	full, err := s.Resolve(path)
	if err != nil {
		return err
	}
	if mode == 0 {
		mode = 0o755
	}
	if _, err := os.Lstat(full); err == nil {
		return fmt.Errorf("%w: %s", fs.ErrExist, path)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(full, mode)
}

// Touch is the "New file" verb, not the shell command of the same name: an
// existing path is a conflict, because the page opens whatever this creates
// in the editor, and bumping the timestamp of a file that was already there
// handed the operator somebody else's contents under the name they had just
// typed as new.
func (s *Service) Touch(path string) error {
	full, err := s.Resolve(path)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(full, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

func (s *Service) Delete(path string, recursive bool) error {
	full, err := s.ResolveEntry(path)
	if err != nil {
		return err
	}
	for _, root := range s.roots {
		if full == root {
			return fmt.Errorf("refusing to delete the configured root %s", root)
		}
	}
	if recursive {
		return os.RemoveAll(full)
	}
	return os.Remove(full)
}

// Move renames an entry, or moves it into a directory when the destination
// names one. The destination has to be free unless overwrite says otherwise:
// rename(2) replaces a file silently, which is how "move a.txt here" used to
// eat the a.txt that was already there without a word.
func (s *Service) Move(from, to string, overwrite bool) error {
	src, err := s.ResolveEntry(from)
	if err != nil {
		return err
	}
	dst, err := s.ResolveEntry(to)
	if err != nil {
		return err
	}
	if st, err := os.Stat(dst); err == nil && st.IsDir() && dst != src {
		dst, err = s.Resolve(dst)
		if err != nil {
			return err
		}
		dst = filepath.Join(dst, filepath.Base(src))
	}
	if dst == src {
		return nil
	}
	// rename(2) answers this with EINVAL, which reaches the operator as
	// "invalid argument" for the most ordinary mis-drop there is.
	if strings.HasPrefix(dst, src+string(filepath.Separator)) {
		return fmt.Errorf("cannot move %s into itself", filepath.Base(src))
	}
	if !overwrite {
		if _, err := os.Lstat(dst); err == nil {
			return fmt.Errorf("%w: %s", fs.ErrExist, dst)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	root, source, err := s.openRootEntry(src)
	if err != nil {
		return err
	}
	defer root.Close()
	target, err := filepath.Rel(root.Name(), dst)
	if err != nil {
		return err
	}
	if target == ".." || strings.HasPrefix(target, ".."+string(filepath.Separator)) {
		if err := s.copyPath(src, dst); err != nil {
			return err
		}
		return root.RemoveAll(source)
	}
	if err := root.Rename(source, target); err != nil {
		if linkErr, ok := err.(*os.LinkError); ok && linkErr.Err == syscall.EXDEV {
			if err := s.copyPath(src, dst); err != nil {
				return err
			}
			return root.RemoveAll(source)
		}
		return err
	}
	return nil
}

// Copy duplicates an entry, into a directory when the destination names one.
// Like Move it refuses an occupied destination unless told to replace it —
// the replacement itself, when asked for, is the atomic one copyRootEntry
// does.
func (s *Service) Copy(from, to string, overwrite bool) error {
	src, err := s.ResolveEntry(from)
	if err != nil {
		return err
	}
	dst, err := s.Resolve(to)
	if err != nil {
		return err
	}
	if st, err := os.Stat(dst); err == nil && st.IsDir() {
		dst = filepath.Join(dst, filepath.Base(src))
	}
	if !overwrite {
		if _, err := os.Lstat(dst); err == nil {
			return fmt.Errorf("%w: %s", fs.ErrExist, dst)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return s.copyPath(src, dst)
}

func (s *Service) Chmod(path string, mode string, recursive bool) error {
	full, err := s.ResolveEntry(path)
	if err != nil {
		return err
	}
	parsed, err := strconv.ParseUint(mode, 8, 32)
	if err != nil {
		return fmt.Errorf("mode must be octal, for example 0644: %w", err)
	}
	perm := os.FileMode(parsed).Perm()
	// Linux has no lchmod, so a symlink cannot be chmod-ed at all — os.Chmod
	// would change the target instead, and the target may be anywhere. Chown
	// below already uses Lchown for exactly this reason; refusing and skipping
	// is the closest chmod can get to the same rule.
	if !recursive {
		st, err := os.Lstat(full)
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("cannot change the mode of a symlink: %s", path)
		}
		return os.Chmod(full, perm)
	}
	return filepath.WalkDir(full, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		return os.Chmod(p, perm)
	})
}

func (s *Service) Chown(path, owner, group string, recursive bool) error {
	full, err := s.ResolveEntry(path)
	if err != nil {
		return err
	}
	uid, gid := -1, -1
	if owner != "" {
		u, err := user.Lookup(owner)
		if err != nil {
			if n, convErr := strconv.Atoi(owner); convErr == nil {
				uid = n
			} else {
				return fmt.Errorf("unknown user %q", owner)
			}
		} else {
			uid, _ = strconv.Atoi(u.Uid)
		}
	}
	if group != "" {
		g, err := user.LookupGroup(group)
		if err != nil {
			if n, convErr := strconv.Atoi(group); convErr == nil {
				gid = n
			} else {
				return fmt.Errorf("unknown group %q", group)
			}
		} else {
			gid, _ = strconv.Atoi(g.Gid)
		}
	}
	if !recursive {
		return os.Lchown(full, uid, gid)
	}
	return filepath.WalkDir(full, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, gid)
	})
}

func (s *Service) Symlink(target, link string) error {
	full, err := s.ResolveEntry(link)
	if err != nil {
		return err
	}
	// The target was never checked, which made this endpoint an escape hatch
	// out of JD_FILE_ROOTS for anything holding file.write: plant the link,
	// then let any later write, upload or extraction follow it.
	absTarget := target
	if !filepath.IsAbs(absTarget) {
		absTarget = filepath.Join(filepath.Dir(full), target)
	}
	if _, err := s.Resolve(absTarget); err != nil {
		return fmt.Errorf("%w: symlink target %s", ErrOutsideRoot, target)
	}
	return os.Symlink(target, full)
}

// Stat is the single-entry form used by the details panel.
func (s *Service) Stat(path string) (*Entry, error) {
	full, err := s.ResolveEntry(path)
	if err != nil {
		return nil, err
	}
	return s.entry(full, filepath.Base(full))
}

// Open returns a handle for download. The caller closes it.
func (s *Service) Open(path string) (*os.File, os.FileInfo, error) {
	full, err := s.Resolve(path)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, st, nil
}

// Upload writes r to path: a new file, or a replacement for an existing
// regular file when overwrite is set.
//
// The bytes land in a temporary sibling and are renamed into place, the same
// way Write works, and for the same reason with the stakes higher: an upload
// is the one write that arrives over a network at the browser's pace, so a
// transfer that is cancelled, times out or trips the request size limit
// halfway through used to leave a truncated file where a whole one had been.
// An existing file keeps its owner and mode across the replacement, which is
// what lets the image editor save over a picture the web server owns.
func (s *Service) Upload(path string, r io.Reader, overwrite bool) (int64, error) {
	full, err := s.Resolve(path)
	if err != nil {
		return 0, err
	}
	perm := os.FileMode(0o644)
	uid, gid := -1, -1
	if st, err := os.Lstat(full); err == nil {
		if st.IsDir() {
			return 0, ErrIsDir
		}
		if !overwrite {
			return 0, fmt.Errorf("%w: %s", fs.ErrExist, path)
		}
		if !st.Mode().IsRegular() {
			return 0, fmt.Errorf("refusing to replace %s: it is not a regular file", path)
		}
		perm = st.Mode().Perm()
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(sys.Uid), int(sys.Gid)
		}
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".jd-upload-*")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	n, err := io.Copy(tmp, r)
	if err != nil {
		tmp.Close()
		return n, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return n, err
	}
	if err := tmp.Close(); err != nil {
		return n, err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return n, err
	}
	if uid >= 0 {
		os.Chown(tmpName, uid, gid)
	}
	return n, os.Rename(tmpName, full)
}
