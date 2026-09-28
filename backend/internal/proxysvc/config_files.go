package proxysvc

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ConfigEntry is one file under the nginx directory, as the Configuration
// page lists it: what it is, whether nginx reads it, and whether the
// dashboard wrote it.
type ConfigEntry struct {
	Path string `json:"path"`
	// Kind is "main" for nginx.conf, "link" for a symlink (a sites-enabled
	// entry, a module), "password" for an htpasswd file and "file" for the
	// rest.
	Kind     string    `json:"kind"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	// Included says nginx reads the file: an include reaches it from
	// nginx.conf, or, for a file a link points at, reaches the link.
	Included   bool         `json:"included"`
	IncludedBy *IncludeSite `json:"includedBy,omitempty"`
	// Managed is a file the dashboard wrote: a site or stream carrying its
	// marker, or a password file it keeps.
	Managed bool `json:"managed"`
	// Protected is a password file. It is listed so the tree is whole, but
	// never read: its hashes are offline-crackable and this list is read by
	// every signed-in account.
	Protected bool `json:"protected"`
	// Backup is an editor's or a package manager's copy that nginx
	// nevertheless reads — a sites-enabled/*.bak is a second copy of a site.
	// Backups nginx does not read are left out.
	Backup bool `json:"backup,omitempty"`
	// Target is where a link points, with every link resolved.
	Target string `json:"target,omitempty"`
	// Outside is a link to a file outside the proxy's directories — a
	// module's load_module file under /usr/share — which the config editor
	// does not open.
	Outside bool `json:"outside,omitempty"`
	// Missing is a link to nothing. nginx refuses to start over an include
	// that reaches one.
	Missing bool `json:"missing,omitempty"`
}

// IncludeSite is the include directive that first reaches a file: its file
// and line. Via is the path nginx opens when that is a link to this file.
type IncludeSite struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Via  string `json:"via,omitempty"`
}

// ConfigProblem is why the includes could not all be followed: a file nginx
// reads that does not parse, or an include of a file that is not there.
// nginx refuses the whole configuration over either.
type ConfigProblem struct {
	Message string `json:"message"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
}

// ConfigFiles is the nginx directory as a tree of files.
type ConfigFiles struct {
	Root  string        `json:"root"`
	Main  string        `json:"main"`
	Files []ConfigEntry `json:"files"`
	// IncludesKnown is false when a file nginx reads could not be parsed:
	// what that file includes is unknown, so a file that is not Included
	// may still be read.
	IncludesKnown bool           `json:"includesKnown"`
	Problem       *ConfigProblem `json:"problem,omitempty"`
	// Truncated is a directory with more files than are listed.
	Truncated bool `json:"truncated"`
}

const (
	// configDepth is how deep under the nginx directory the tree goes:
	// nginx.conf, conf.d/app.conf, and one folder further.
	configDepth = 3
	// maxConfigEntries bounds the listing of a directory somebody has
	// filled with something other than configuration.
	maxConfigEntries = 5000
	// maxConfigRead is the most read of one file to find the dashboard's
	// marker or its includes; a configuration file is kilobytes.
	maxConfigRead = 4 << 20
	// maxIncludeDepth stops a chain of includes nginx itself would give up
	// on long before.
	maxIncludeDepth = 32
)

// dashboardMarker opens every file the dashboard writes, sites and streams
// (managedMarker) and the Docker ingress's routes alike.
const dashboardMarker = "# Managed by Just Dashboard"

// ConfigFiles lists the nginx directory and follows nginx.conf's includes
// through it.
//
// Whether nginx reads a file is worked out from the files on disk rather
// than from `nginx -T`, which prints nothing at all for a configuration that
// fails its test — the moment the question matters most — and is the admin's
// to run. The includes are followed as nginx follows them: relative to the
// main file's directory, a glob matched as glob(3) matches it (sorted, no
// dotfiles unless the pattern asks, "[!…]" negated), inside any block, each
// file parsed with nginx's own tokenising. A file that does not parse stops
// the walk there, and IncludesKnown says so rather than calling everything
// past it unread.
func (s *Service) ConfigFiles() (*ConfigFiles, error) {
	root := s.nginxDir
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	out := &ConfigFiles{Root: root, Main: filepath.Join(root, "nginx.conf"), Files: []ConfigEntry{}, IncludesKnown: true}

	scan := includeScan{service: s, prefix: root, reached: map[string]IncludeSite{}, open: map[string]bool{}}
	if _, err := os.Stat(out.Main); err != nil {
		out.IncludesKnown = false
		out.Problem = &ConfigProblem{Message: fmt.Sprintf("there is no nginx.conf in %s, so what nginx reads is not known", root)}
	} else {
		scan.reached[out.Main] = IncludeSite{}
		scan.file(out.Main, 0)
		out.IncludesKnown = !scan.unknown
		out.Problem = scan.problem
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable folder is left out rather than failing the list.
			if d != nil && d.IsDir() && path != root {
				return fs.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		depth := strings.Count(strings.TrimPrefix(path, root), string(os.PathSeparator))
		if d.IsDir() {
			if depth >= configDepth {
				return fs.SkipDir
			}
			return nil
		}
		if len(out.Files) >= maxConfigEntries {
			out.Truncated = true
			return fs.SkipAll
		}
		if entry, ok := s.configEntry(path, d, &scan); ok {
			out.Files = append(out.Files, entry)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	return out, nil
}

// configEntry describes one file of the walk, or reports it left out: a
// backup or a dotfile nginx does not read, or a link to a directory.
func (s *Service) configEntry(path string, d fs.DirEntry, scan *includeScan) (ConfigEntry, bool) {
	entry := ConfigEntry{Path: path, Kind: "file"}
	site, reached := scan.reached[path]
	entry.Included = reached
	if reached && site.File != "" {
		entry.IncludedBy = &site
	}
	resolved := path
	if d.Type()&fs.ModeSymlink != 0 {
		entry.Kind = "link"
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			entry.Missing = true
			if raw, err := os.Readlink(path); err == nil {
				if !filepath.IsAbs(raw) {
					raw = filepath.Join(filepath.Dir(path), raw)
				}
				entry.Target = filepath.Clean(raw)
			}
		} else {
			if st, err := os.Stat(target); err == nil && st.IsDir() {
				return entry, false
			}
			entry.Target = target
			resolved = target
			if _, err := s.allowedPath(path); err != nil {
				entry.Outside = true
			}
		}
	}

	name := filepath.Base(path)
	entry.Protected = s.isPasswordFile(path) || s.isPasswordFile(resolved)
	if (strings.HasPrefix(name, ".") || isBackupFile(name)) && !entry.Included && !entry.Protected {
		return entry, false
	}
	entry.Backup = isBackupFile(name) && !entry.Protected
	switch {
	case entry.Protected:
		entry.Kind = "password"
		// The dashboard keeps its own password files in jd-auth; one
		// elsewhere was written by hand.
		entry.Managed = strings.HasPrefix(resolved, s.authDir()+string(os.PathSeparator))
	case path == filepath.Join(s.nginxDir, "nginx.conf"):
		entry.Kind = "main"
	}

	if st, err := os.Stat(path); err == nil {
		entry.Size, entry.Modified = st.Size(), st.ModTime().UTC()
	} else if st, err := os.Lstat(path); err == nil {
		entry.Size, entry.Modified = st.Size(), st.ModTime().UTC()
	}
	if !entry.Protected && !entry.Outside && !entry.Missing {
		if content, err := readConfigHead(resolved); err == nil {
			entry.Managed = strings.Contains(content, dashboardMarker)
		}
	}
	return entry, true
}

// ResolveConfigPath is path with its links resolved, held to the proxy's
// directories as the config editor's routes hold it.
func (s *Service) ResolveConfigPath(path string) (string, error) {
	return s.allowedPath(path)
}

// readConfigHead reads a configuration file, or its first maxConfigRead
// bytes when somebody has put something much larger in the directory.
func readConfigHead(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxConfigRead))
	return string(b), err
}

// includeScan follows includes from the main file across the disk.
type includeScan struct {
	service *Service
	prefix  string
	// reached holds every path an include reaches, as nginx opens it and,
	// for a link, as the file it resolves to, each with the first include
	// that reached it.
	reached map[string]IncludeSite
	// open is the chain being followed, so a file including itself is read
	// once rather than for ever.
	open    map[string]bool
	unknown bool
	problem *ConfigProblem
}

// report keeps the first thing that stopped the walk.
func (sc *includeScan) report(p ConfigProblem) {
	if sc.problem == nil {
		sc.problem = &p
	}
}

// file parses path and follows its includes. A file outside the proxy's
// directories — certbot's options, a module under /usr/share — is reached but
// not read: what the editor may not show, the tree does not open either.
func (sc *includeScan) file(path string, depth int) {
	full, err := sc.service.allowedPath(path)
	if err != nil || sc.service.isPasswordFile(full) || depth > maxIncludeDepth || sc.open[full] {
		return
	}
	content, err := readConfigHead(full)
	if errors.Is(err, fs.ErrNotExist) {
		// A link to nothing, which match has already reported.
		return
	}
	if err != nil {
		sc.unknown = true
		sc.report(ConfigProblem{Message: fmt.Sprintf("%s could not be read: %v", full, err), File: full})
		return
	}
	directives, err := ParseNginxFile(full, content, nil)
	if err != nil {
		sc.unknown = true
		message, file, line := nginxPosition(err.Error())
		if file == "" {
			// An unclosed block is named at the end of the file, with no line.
			message, file = strings.TrimSuffix(message, " in "+full), full
		}
		sc.report(ConfigProblem{Message: message, File: file, Line: line})
		return
	}
	sc.open[full] = true
	defer delete(sc.open, full)
	sc.directives(full, directives, depth)
}

func (sc *includeScan) directives(file string, directives []Directive, depth int) {
	for _, d := range directives {
		if d.Block != nil {
			sc.directives(file, d.Block, depth)
			continue
		}
		if d.Name != "include" || len(d.Args) != 1 {
			continue
		}
		site := IncludeSite{File: file, Line: d.Line}
		for _, match := range sc.match(site, d.Args[0]) {
			sc.reach(match, site)
			sc.file(match, depth+1)
		}
	}
}

// reach records that site's include reaches path, and the file a link there
// resolves to.
func (sc *includeScan) reach(path string, site IncludeSite) {
	if _, seen := sc.reached[path]; !seen {
		sc.reached[path] = site
	}
	if resolved := resolvedFile(path); resolved != path {
		if _, seen := sc.reached[resolved]; !seen {
			via := site
			via.Via = path
			sc.reached[resolved] = via
		}
	}
}

// match is what nginx opens for an include of pattern: the file itself, or a
// glob's matches in glob(3)'s order. nginx refuses a configuration whose
// include names a file that is not there, and one whose glob matches a
// directory, so each is reported.
func (sc *includeScan) match(site IncludeSite, pattern string) []string {
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(sc.prefix, pattern)
	}
	pattern = filepath.Clean(pattern)
	if !strings.ContainsAny(pattern, "*?[") {
		st, err := os.Stat(pattern)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			sc.report(ConfigProblem{Message: fmt.Sprintf("includes %s, which is not there", pattern), File: site.File, Line: site.Line})
			return nil
		case err == nil && st.IsDir():
			sc.report(ConfigProblem{Message: fmt.Sprintf("includes %s, which is a directory", pattern), File: site.File, Line: site.Line})
			return nil
		}
		return []string{pattern}
	}
	matches, err := filepath.Glob(globToMatch(pattern))
	if err != nil {
		sc.report(ConfigProblem{Message: fmt.Sprintf("includes %s, which is not a pattern nginx can read", pattern), File: site.File, Line: site.Line})
		return nil
	}
	hidden := strings.HasPrefix(filepath.Base(pattern), ".")
	out := matches[:0]
	for _, m := range matches {
		if !hidden && strings.HasPrefix(filepath.Base(m), ".") {
			continue
		}
		st, err := os.Stat(m)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			sc.report(ConfigProblem{Message: fmt.Sprintf("includes %s, a link to nothing", m), File: site.File, Line: site.Line})
		case err == nil && st.IsDir():
			sc.report(ConfigProblem{Message: fmt.Sprintf("includes %s, which matches the directory %s", pattern, m), File: site.File, Line: site.Line})
			continue
		}
		out = append(out, m)
	}
	return out
}
