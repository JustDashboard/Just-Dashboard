package proxysvc

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// How nginx reads a list beside other rules.
//
// nginx joins every allow and deny in one block — a server, a location —
// into one run, the rules of an included file standing where its include
// does, and stops at the first rule an address matches. A list with an allow
// list ends it with deny all, so in a site that includes an office list and
// then a VPN list the VPN's addresses are never reached, while each list on
// its own reads as letting them in. A block with rules of its own does not
// inherit its parent's, so each block is a run of its own.
//
// Each use of a list carries the runs it is part of, so the page can say
// which rules nginx never reaches and whether a site, with everything it
// reads beside the list, lets the reader in.

// AccessRule is one allow or deny as written: an address, a range or all.
type AccessRule struct {
	Deny    bool   `json:"deny"`
	Address string `json:"address"`
}

// AccessSource is where some of a run's rules come from: a list, or the
// site's own allow, deny, satisfy and auth_basic lines — in its file or a
// snippet it includes — between two lists.
type AccessSource struct {
	// List is the list's name, and empty for the site's own lines.
	List  string       `json:"list,omitempty"`
	File  string       `json:"file"`
	Rules []AccessRule `json:"rules"`
	// Password is an auth_basic other than off: a login is asked for.
	Password bool   `json:"password,omitempty"`
	Satisfy  string `json:"satisfy,omitempty"`
}

// AccessRun is one block of a site that takes a list in: every source of
// rules in it, in the order nginx reads them.
type AccessRun struct {
	// Block is "server", "location /admin" and the like, or "http" for the
	// top of the site's file.
	Block   string         `json:"block"`
	Sources []AccessSource `json:"sources"`
}

// accessSources is every list in jd-access as a source, under its path and
// under its path with links resolved. A list the listing does not read is
// left out, and is then read as no list at all.
func (s *Service) accessSources() map[string]AccessSource {
	out := map[string]AccessSource{}
	entries, err := os.ReadDir(s.AccessListDir())
	if err != nil {
		return out
	}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".conf")
		if !ok || e.IsDir() || !accessListNameRe.MatchString(name) {
			continue
		}
		path := filepath.Join(s.AccessListDir(), e.Name())
		content, _, why := s.readListFile(path)
		if why != "" {
			continue
		}
		directives, err := ParseNginxFile(path, content, []string{"http", "server"})
		if err != nil {
			continue
		}
		source := AccessSource{List: name, File: path, Rules: []AccessRule{}}
		for _, d := range directives {
			if isAccessLine(d) {
				addAccessLine(&source, d)
			}
		}
		out[path] = source
		out[resolvedFile(path)] = source
	}
	return out
}

func isAccessLine(d Directive) bool {
	if d.Block != nil || len(d.Args) != 1 {
		return false
	}
	switch d.Name {
	case "allow", "deny", "auth_basic", "satisfy":
		return true
	}
	return false
}

func addAccessLine(source *AccessSource, d Directive) {
	switch d.Name {
	case "allow", "deny":
		source.Rules = append(source.Rules, AccessRule{Deny: d.Name == "deny", Address: d.Args[0]})
	case "auth_basic":
		source.Password = d.Args[0] != "off"
	case "satisfy":
		source.Satisfy = d.Args[0]
	}
}

// runsWith is the runs a list called name is part of.
func runsWith(runs []AccessRun, name string) []AccessRun {
	var out []AccessRun
	for _, run := range runs {
		if slices.ContainsFunc(run.Sources, func(source AccessSource) bool { return source.List == name }) {
			out = append(out, run)
		}
	}
	return out
}

// accessRuns reads the site file at path into the runs that take in a
// list. Snippets it includes are read where they are included, one level
// deep, as far as the listing follows them to find a list's sites.
func (s *Service) accessRuns(path string, sources map[string]AccessSource) []AccessRun {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	directives, err := ParseNginxFile(path, string(b), []string{"http"})
	if err != nil {
		return nil
	}
	r := &runReader{s: s, sources: sources, self: resolvedFile(path)}
	r.block("http", directives, 0)
	return r.runs
}

type runReader struct {
	s       *Service
	sources map[string]AccessSource
	self    string
	runs    []AccessRun
}

// block reads one block into a run, and each block inside it into a run of
// its own.
func (r *runReader) block(label string, directives []Directive, depth int) {
	run := AccessRun{Block: label, Sources: []AccessSource{}}
	r.read(&run, directives, depth)
	if slices.ContainsFunc(run.Sources, func(source AccessSource) bool { return source.List != "" }) {
		r.runs = append(r.runs, run)
	}
}

func (r *runReader) read(run *AccessRun, directives []Directive, depth int) {
	for _, d := range directives {
		switch {
		case d.Block != nil:
			r.block(strings.Join(append([]string{d.Name}, d.Args...), " "), d.Block, depth)
		case d.Name == "include" && len(d.Args) == 1:
			for _, file := range r.s.includedFiles(d.Args[0]) {
				if list, ok := r.list(file); ok {
					// A second include of the same list reads the same
					// rules again, after the first has decided.
					if !slices.ContainsFunc(run.Sources, func(source AccessSource) bool { return source.List == list.List }) {
						run.Sources = append(run.Sources, list)
					}
					continue
				}
				if depth == 0 {
					r.read(run, r.snippet(file, d.Context), depth+1)
				}
			}
		case isAccessLine(d):
			last := len(run.Sources) - 1
			if last < 0 || run.Sources[last].List != "" || run.Sources[last].File != d.File {
				run.Sources = append(run.Sources, AccessSource{File: d.File, Rules: []AccessRule{}})
				last++
			}
			addAccessLine(&run.Sources[last], d)
		}
	}
}

func (r *runReader) list(file string) (AccessSource, bool) {
	if source, ok := r.sources[file]; ok {
		return source, true
	}
	source, ok := r.sources[resolvedFile(file)]
	return source, ok
}

// snippet is the directives of a file a site includes, where the config
// editor would read it: not the site itself, a list the listing left out,
// or a password file.
func (r *runReader) snippet(file string, context []string) []Directive {
	full, err := r.s.allowedPath(file)
	if err != nil || full == r.self || filepath.Dir(full) == r.s.AccessListDir() || r.s.isPasswordFile(full) {
		return nil
	}
	if info, err := os.Stat(full); err != nil || !info.Mode().IsRegular() {
		return nil
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return nil
	}
	directives, err := ParseNginxFile(full, string(b), context)
	if err != nil {
		return nil
	}
	return directives
}

// includedFiles is what an include takes in, in the order nginx reads it: a
// relative path from nginx.conf's directory, a glob's matches sorted, and a
// dotfile only where the pattern names one.
func (s *Service) includedFiles(pattern string) []string {
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(s.nginxDir, pattern)
	}
	pattern = filepath.Clean(pattern)
	if !strings.ContainsAny(pattern, "*?[") {
		return []string{pattern}
	}
	matches, _ := filepath.Glob(globToMatch(pattern))
	out := matches[:0]
	for _, match := range matches {
		if strings.HasPrefix(filepath.Base(match), ".") && !strings.HasPrefix(filepath.Base(pattern), ".") {
			continue
		}
		out = append(out, match)
	}
	return out
}
