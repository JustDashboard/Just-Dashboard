package proxysvc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// Connecting the stream directory.
//
// A stream file forwards nothing until a top-level stream block includes the
// directory it sits in, and that block belongs in nginx.conf — the one file
// every site on the host depends on. Printing the block for the operator to
// paste over SSH was the Streams page's dead end; writing it is safe only on
// these terms: the change is shown before it is made and is as small as it
// can be, nginx's own test passes on the whole configuration before it stays
// (every byte put back, mode and line endings with them, when it does not),
// and the dashboard takes out exactly what it added and nothing else.
//
// It prefers not to touch nginx.conf at all. Debian and Ubuntu's nginx.conf
// includes modules-enabled/*.conf at its top level, so a file there is main
// context: a stream block in it sits beside http, and nginx.conf stays the
// conffile its package shipped, with no prompt on the next upgrade. The file
// is named to sort after the NN-mod-*.conf files packages put there, because a
// stream block must come after a dynamic stream module's load_module, and
// nginx refuses any load_module that comes after a block. Where nginx.conf
// includes no such directory (nginx.org's own), the block is appended to
// nginx.conf, after every load_module, with a copy of the file kept beside it.
// Where a top-level stream block exists already a second one is "duplicate"
// to nginx, so the include goes into that block as one marked line.

// How the stream directory is connected.
const (
	// IncludeDropIn is a file of its own in a directory nginx.conf includes
	// at its top level.
	IncludeDropIn = "dropin"
	// IncludeMainFile is a stream block appended to nginx.conf.
	IncludeMainFile = "nginx.conf"
	// IncludeStreamBlock is one include line in the stream block that is
	// already there.
	IncludeStreamBlock = "stream-block"
)

// Why a connect edits nginx.conf rather than adding a drop-in: the plan says
// which, so the page can say why nginx.conf is the file changed.
const (
	// AppendNoDirectory is nginx.conf including no directory at its top level
	// by a glob the drop-in's name matches.
	AppendNoDirectory = "no-directory"
	// AppendModuleLoadsAfter is such a directory with a load_module read after
	// it, which nginx refuses once a stream block has come before it.
	AppendModuleLoadsAfter = "load-module-after"
	// AppendDirectoryElsewhere is such a directory leading outside the nginx
	// directory, where the dashboard does not write.
	AppendDirectoryElsewhere = "directory-elsewhere"
	// AppendNameTaken is such a directory holding a file of the drop-in's name
	// that is not the dashboard's.
	AppendNameTaken = "name-taken"
)

// streamDropIn is the drop-in's file name: after every NN-mod-*.conf.
const streamDropIn = "zz-just-dashboard-stream.conf"

// includeLineMark closes the line added to a stream block the operator wrote,
// so that line — and only it — can be found and taken out again.
const includeLineMark = "# added by Just Dashboard"

// StreamConnection is the include the dashboard added, which it can take out.
type StreamConnection struct {
	Mode string `json:"mode"`
	Path string `json:"path"`
}

// StreamIncludePlan is the change that connects the stream directory, worked
// out and shown before anything is written.
type StreamIncludePlan struct {
	Mode string `json:"mode"`
	// Path is the file the change is made in.
	Path string `json:"path"`
	// Exists is false for a drop-in, a file the change creates.
	Exists bool `json:"exists"`
	// Reason is why the change is an edit to nginx.conf and not a drop-in:
	// no-directory, load-module-after, directory-elsewhere or name-taken.
	Reason string `json:"reason,omitempty"`
	// DropIn is where the drop-in would have gone, for every reason but
	// no-directory.
	DropIn string `json:"dropIn,omitempty"`
	// KeepsCopy is whether a copy of the file as it was is kept beside it:
	// not where an include would read that copy as configuration.
	KeepsCopy bool   `json:"keepsCopy"`
	Before    string `json:"before"`
	After     string `json:"after"`
	// Added is what the change adds, as it reads in the file.
	Added string `json:"added"`
	// Line is where Added starts in After, counted from 1.
	Line int `json:"line"`
	// Streams are the files in the directory that nginx starts reading.
	Streams []string `json:"streams"`
	// Conflicts are those streams' sockets that something already holds.
	// nginx -t does not bind, so it passes them; the reload then fails inside
	// the master while the command that sent it exits 0, and every later
	// reload fails the same way. The change is refused until they are gone.
	Conflicts []string `json:"conflicts"`
	Warnings  []string `json:"warnings"`
	// backup is the copy the change keeps, named when the plan is made so
	// the plan's word on it and the change cannot differ.
	backup string
}

// StreamIncludeResult is what connecting or disconnecting did.
type StreamIncludeResult struct {
	Mode string `json:"mode"`
	Path string `json:"path"`
	// Backup is the copy kept of a file the change edited.
	Backup     string            `json:"backup,omitempty"`
	Validation *ValidationResult `json:"validation"`
	// Streams is how many stream files nginx starts or stops reading.
	Streams  int  `json:"streams"`
	Reloaded bool `json:"reloaded"`
	// ReloadError is why nginx did not reload after the change passed its
	// test. The change stays: it takes effect at the next reload.
	ReloadError string   `json:"reloadError,omitempty"`
	Output      string   `json:"output,omitempty"`
	Warnings    []string `json:"warnings"`
}

// StreamIncludeError is a connect or a disconnect refused before anything was
// written, with a code the page can act on.
type StreamIncludeError struct {
	// Code is already_included, include_misplaced, module_missing,
	// stream_block_elsewhere, plan_changed, port_in_use, not_connected or
	// config_unreadable.
	Code   string
	Reason string
}

func (e *StreamIncludeError) Error() string { return e.Reason }

// moduleMissing is a stream module known to be missing — not one nginx could
// not be asked about, which the test then decides.
func moduleMissing(m StreamModule) bool {
	return !m.Usable && m.State != ModuleUnknown
}

// nginxWord writes a value as one nginx argument, quoted only when it has to
// be: a directory with a space in it is otherwise two arguments.
func nginxWord(value string) string {
	if !strings.ContainsAny(value, " \t\r\n;{}#'\"$\\") {
		return value
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

func streamIncludeDirective(dir string) string {
	return "include " + nginxWord(dir+"/*.conf") + ";"
}

// renderDropIn is the drop-in's whole content.
func renderDropIn(dir string) string {
	l := &lines{}
	l.add(managedMarker)
	l.add("# Reads the streams on the Streams page, which live in %s.", dir)
	l.add("# nginx.conf includes this directory at its top level, after the modules it")
	l.add("# loads, so the stream block sits here and nginx.conf stays as its package")
	l.add("# shipped it. Disconnect it on the Streams page, or delete this file.")
	l.add("stream {")
	l.add("    %s", streamIncludeDirective(dir))
	l.add("}")
	return l.String()
}

// renderAppended is the block appended to nginx.conf, in its line endings.
func renderAppended(dir, nl string) string {
	block := strings.Join([]string{
		"# Just Dashboard: streams begin. Reads the streams in " + dir + ";",
		`# disconnect them on the Streams page, or delete from here to "streams end".`,
		"stream {",
		"    " + streamIncludeDirective(dir),
		"}",
		"# Just Dashboard: streams end.",
	}, nl)
	return block + nl
}

// includeLine is the line added to a stream block that was already there.
func includeLine(dir string) string {
	return streamIncludeDirective(dir) + " " + includeLineMark
}

// lineEnding is the file's own: CRLF when it has any, which is what nginx.conf
// edited on Windows and copied up has throughout.
func lineEnding(content string) string {
	if strings.Contains(content, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

// PlanStreamInclude works out the change that connects the stream directory,
// writing nothing. It refuses where no change can work: the module missing,
// the directory included already or included in the wrong block, or the stream
// block in a file the dashboard does not write.
func (s *Service) PlanStreamInclude(ctx context.Context) (*StreamIncludePlan, error) {
	if module := s.StreamModule(ctx); moduleMissing(module) {
		return nil, moduleRefusal(module)
	}
	listeners, listenErr := readListeners(ctx)
	return s.planStreamInclude(listeners, listenErr)
}

func moduleRefusal(m StreamModule) error {
	return &StreamIncludeError{Code: "module_missing", Reason: "nginx on this host has no stream module (" + m.State +
		"), so a stream block would fail its configuration test and stop every reload; the module comes first"}
}

func (s *Service) planStreamInclude(listeners []Listener, listenErr error) (*StreamIncludePlan, error) {
	dir := s.streamDir()
	files, err := readConfigFiles(s.nginxDir)
	if err != nil {
		return nil, &StreamIncludeError{Code: "config_unreadable", Reason: "nginx.conf could not be read: " + err.Error()}
	}
	context, found, err := streamIncludeContext(files, dir)
	if err != nil {
		return nil, &StreamIncludeError{Code: "config_unreadable", Reason: "the configuration could not be read: " + err.Error()}
	}
	switch {
	case found && len(context) == 1 && context[0] == "stream":
		return nil, &StreamIncludeError{Code: "already_included", Reason: "nginx already reads " + dir + " in a stream block"}
	case found:
		return nil, &StreamIncludeError{Code: "include_misplaced", Reason: "nginx.conf includes " + dir +
			" in the wrong block already, where its test refuses a stream; that include has to come out first"}
	}

	var plan *StreamIncludePlan
	if block := streamBlockFile(files); block != "" {
		if plan, err = s.planIntoStreamBlock(files, block, dir); err != nil {
			return nil, err
		}
	} else {
		var reason, dropIn string
		if plan, reason, dropIn = s.planDropIn(files, dir); plan == nil {
			plan = planAppend(files[0], dir)
			plan.Reason, plan.DropIn = reason, dropIn
		}
	}
	// The plan is held to what it is for: read as nginx reads it, the
	// changed configuration has to include the directory in a stream block.
	changed := withFile(files, plan.Path, plan.After)
	if context, found, err := streamIncludeContext(changed, dir); err != nil ||
		!found || len(context) != 1 || context[0] != "stream" {
		return nil, fmt.Errorf("the change planned in %s would not have nginx read %s in a stream block", plan.Path, dir)
	}
	target, err := s.includeTarget(plan.Path)
	if err != nil {
		return nil, err
	}
	plan.Streams, plan.Conflicts = stagedStreams(dir, listeners)
	plan.Warnings = []string{}
	if plan.Exists {
		plan.backup = backupFor(changed, s.nginxDir, plan.Path, target, time.Now())
		plan.KeepsCopy = plan.backup != ""
		if !plan.KeepsCopy {
			plan.Warnings = append(plan.Warnings, "No copy of "+plan.Path+
				" is kept beside it: an include in the configuration would read the copy as configuration.")
		}
	}
	if listenErr != nil {
		plan.Warnings = append(plan.Warnings, "The host's listening sockets could not be read ("+listenErr.Error()+
			"), so the streams' ports were not checked for a program already holding them.")
	}
	return plan, nil
}

// withFile is the configuration with one file's content replaced, or added.
func withFile(files []ConfigFile, path, content string) []ConfigFile {
	out := make([]ConfigFile, 0, len(files)+1)
	replaced := false
	for _, f := range files {
		if f.Path == path {
			f.Content, replaced = content, true
		}
		out = append(out, f)
	}
	if !replaced {
		out = append(out, ConfigFile{Path: path, Content: content})
	}
	return out
}

// streamBlockFile is the file holding a top-level stream block, if there is
// one: the block a new stream directory has to go into.
func streamBlockFile(files []ConfigFile) string {
	tree, err := NginxTree(files)
	if err != nil {
		return ""
	}
	for _, d := range tree {
		if d.Name == "stream" && d.Block != nil && len(d.Context) == 0 {
			return d.File
		}
	}
	return ""
}

// includeTarget is the file a change to path writes: its directory and any
// link resolved, and held inside the nginx directory. A modules directory
// that is a link elsewhere is not somewhere this writes.
func (s *Service) includeTarget(path string) (string, error) {
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	full, err := s.allowedPath(filepath.Join(dir, filepath.Base(path)))
	if err != nil {
		return "", err
	}
	if full != s.nginxDir && !strings.HasPrefix(full, s.nginxDir+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: %s", ErrUnsafePath, path)
	}
	return full, nil
}

// planDropIn is a drop-in in the first directory nginx.conf includes at its
// top level by a glob the drop-in's name matches — unless a load_module comes
// after it, which nginx would refuse as too late. Where there is none, it says
// why not, and where the drop-in would have gone: the first such directory's
// reason, since that is the one nginx.conf's reader looks for.
func (s *Service) planDropIn(files []ConfigFile, dir string) (plan *StreamIncludePlan, reason, dropIn string) {
	main, err := ParseNginxFile(files[0].Path, files[0].Content, nil)
	if err != nil {
		return nil, AppendNoDirectory, ""
	}
	content := renderDropIn(dir)
	for _, d := range main {
		if d.Name != "include" || d.Block != nil || len(d.Args) != 1 {
			continue
		}
		pattern := d.Args[0]
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(filepath.Dir(files[0].Path), pattern)
		}
		pattern = filepath.Clean(pattern)
		parent, base := filepath.Dir(pattern), filepath.Base(pattern)
		if !strings.ContainsAny(base, "*?[") || strings.ContainsAny(parent, "*?[") {
			continue
		}
		if ok, _ := filepath.Match(globToMatch(base), streamDropIn); !ok {
			continue
		}
		if st, err := os.Stat(parent); err != nil || !st.IsDir() {
			continue
		}
		path := filepath.Join(parent, streamDropIn)
		var why string
		if _, err := s.includeTarget(path); err != nil {
			why = AppendDirectoryElsewhere
		} else if _, err := os.Lstat(path); err == nil {
			// A file of that name that nginx is not reading as ours.
			why = AppendNameTaken
		} else if loadModuleAfter(withFile(files, path, content), path) {
			why = AppendModuleLoadsAfter
		} else {
			return &StreamIncludePlan{Mode: IncludeDropIn, Path: path, After: content, Added: content, Line: 1}, "", ""
		}
		if reason == "" {
			reason, dropIn = why, path
		}
	}
	if reason == "" {
		reason = AppendNoDirectory
	}
	return nil, reason, dropIn
}

// loadModuleAfter reports a load_module that nginx reads after the stream
// block in file, which it would refuse: "load_module is specified too late".
func loadModuleAfter(files []ConfigFile, file string) bool {
	tree, err := NginxTree(files)
	if err != nil {
		return true
	}
	seen := false
	for _, d := range tree {
		if d.Name == "stream" && d.File == file {
			seen = true
		}
		if seen && d.Name == "load_module" {
			return true
		}
	}
	return false
}

// planAppend is the stream block appended to nginx.conf, a blank line after
// what is there.
func planAppend(main ConfigFile, dir string) *StreamIncludePlan {
	nl := lineEnding(main.Content)
	sep := ""
	if main.Content != "" {
		if !strings.HasSuffix(main.Content, "\n") {
			sep = nl
		}
		sep += nl
	}
	added := renderAppended(dir, nl)
	return &StreamIncludePlan{
		Mode: IncludeMainFile, Path: main.Path, Exists: true,
		Before: main.Content, After: main.Content + sep + added,
		Added: strings.TrimRight(added, "\r\n"), Line: strings.Count(main.Content+sep, "\n") + 1,
	}
}

// planIntoStreamBlock is one include line before the closing brace of the
// stream block in file, indented as the file indents.
func (s *Service) planIntoStreamBlock(files []ConfigFile, file, dir string) (*StreamIncludePlan, error) {
	if _, err := s.includeTarget(file); err != nil {
		return nil, &StreamIncludeError{Code: "stream_block_elsewhere", Reason: fmt.Sprintf(
			"nginx's stream block is in %s, which the dashboard does not write; add `%s` inside it by hand",
			file, streamIncludeDirective(dir))}
	}
	var content string
	for _, f := range files {
		if f.Path == file {
			content = f.Content
		}
	}
	_, closing, ok := topLevelBlock(content, "stream")
	if !ok {
		return nil, fmt.Errorf("the stream block in %s could not be found", file)
	}
	nl := lineEnding(content)
	unit := "    "
	if strings.Contains(content, "\n\t") {
		unit = "\t"
	}
	lineStart := strings.LastIndex(content[:closing], "\n") + 1
	before := content[lineStart:closing]
	indent := before[:len(before)-len(strings.TrimLeft(before, " \t"))]
	line := indent + unit + includeLine(dir)
	at, insert := lineStart, line+nl
	if strings.TrimSpace(before) != "" {
		// The brace shares its line: the include gets a line of its own
		// before it, so its comment cannot swallow the brace.
		at, insert = closing, nl+line+nl
	}
	lead := len(insert) - len(strings.TrimLeft(insert, "\r\n"))
	return &StreamIncludePlan{
		Mode: IncludeStreamBlock, Path: file, Exists: true,
		Before: content, After: content[:at] + insert + content[at:],
		Added: line, Line: strings.Count(content[:at]+insert[:lead], "\n") + 1,
	}, nil
}

// topLevelBlock finds the braces of a block at the top of a file by their
// offsets, reading the file as nginx does — comments, quotes and ${variables}
// — so a brace in a comment or a string is not taken for one.
func topLevelBlock(content, name string) (openAt, closeAt int, ok bool) {
	depth, open := 0, -1
	var words []string
	for i := 0; i < len(content); {
		c := content[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '#':
			for i < len(content) && content[i] != '\n' {
				i++
			}
		case c == ';':
			words = nil
			i++
		case c == '{':
			if depth == 0 && open < 0 && len(words) == 1 && words[0] == name {
				open = i
			}
			depth++
			words = nil
			i++
		case c == '}':
			depth--
			if depth == 0 && open >= 0 {
				return open, i, true
			}
			words = nil
			i++
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(content) && content[j] != c {
				if content[j] == '\\' {
					j++
				}
				j++
			}
			words = append(words, content[i+1:min(j, len(content))])
			i = j + 1
		default:
			j, variable := i, false
			for j < len(content) {
				ch := content[j]
				if ch == '{' && variable {
					j++
					continue
				}
				variable = false
				if ch == '\\' && j+1 < len(content) {
					j += 2
					continue
				}
				if ch == '$' {
					variable = true
				} else if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' || ch == ';' || ch == '{' {
					break
				}
				j++
			}
			words = append(words, content[i:j])
			i = j
		}
	}
	return 0, 0, false
}

// stagedStreams are the files in the stream directory, and the sockets among
// them that something already holds or that two of them ask for. None of them
// is being served while the directory is not read, so any socket on their
// ports — nginx's own for a site included — is one the reload cannot bind.
func stagedStreams(dir string, listeners []Listener) (names, conflicts []string) {
	names, conflicts = []string{}, []string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return names, conflicts
	}
	type staged struct {
		name  string
		binds []bind
	}
	var streams []staged
	for _, e := range entries {
		if !streamFileName(e) {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".conf")
		names = append(names, name)
		if b, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
			streams = append(streams, staged{name, parseStreamFile(e.Name(), string(b)).binds})
		}
	}
	seen := map[string]bool{}
	add := func(conflict string) {
		if !seen[conflict] {
			seen[conflict] = true
			conflicts = append(conflicts, conflict)
		}
	}
	for i, stream := range streams {
		for _, b := range stream.binds {
			for _, other := range streams[i+1:] {
				for _, o := range other.binds {
					if bindsClash(b, o) {
						add(fmt.Sprintf("%s and %s both listen on port %d/%s", stream.name, other.name, b.port, b.proto()))
					}
				}
			}
			for _, l := range listeners {
				if !listenerHolds(l, b) {
					continue
				}
				owner := l.Process
				if owner == "" {
					owner = "another program"
				}
				if l.PID > 0 {
					owner += fmt.Sprintf(" (pid %d)", l.PID)
				}
				add(fmt.Sprintf("%s: port %d/%s is already in use by %s", stream.name, b.port, b.proto(), owner))
			}
		}
	}
	return names, conflicts
}

// ownInclude is the include the dashboard added, found again to take it out.
type ownInclude struct {
	mode, path    string
	before, after string
	// remove is a drop-in: the whole file goes.
	remove bool
}

// findOwnInclude looks for exactly what a connect writes: the drop-in with its
// content unchanged, the appended block between its markers, or the marked
// line. Anything edited by hand since is the operator's, and stays.
func findOwnInclude(files []ConfigFile, dir string) *ownInclude {
	dropIn := renderDropIn(dir)
	for _, f := range files {
		if filepath.Base(f.Path) == streamDropIn && f.Content == dropIn {
			return &ownInclude{mode: IncludeDropIn, path: f.Path, before: f.Content, remove: true}
		}
	}
	if len(files) > 0 {
		main := files[0]
		for _, nl := range []string{"\n", "\r\n"} {
			block := renderAppended(dir, nl)
			i := strings.Index(main.Content, block)
			if i < 0 {
				continue
			}
			start := i
			if strings.HasSuffix(main.Content[:i], nl+nl) {
				start -= len(nl)
			}
			return &ownInclude{mode: IncludeMainFile, path: main.Path, before: main.Content,
				after: main.Content[:start] + main.Content[i+len(block):]}
		}
	}
	marked := includeLine(dir)
	for _, f := range files {
		for start := 0; start < len(f.Content); {
			end := strings.IndexByte(f.Content[start:], '\n')
			if end < 0 {
				end = len(f.Content)
			} else {
				end += start + 1
			}
			if strings.TrimSpace(f.Content[start:end]) == marked {
				return &ownInclude{mode: IncludeStreamBlock, path: f.Path, before: f.Content,
					after: f.Content[:start] + f.Content[end:]}
			}
			start = end
		}
	}
	return nil
}

// ownConnection is the include the dashboard added, for the listing.
func ownConnection(files []ConfigFile, dir string) *StreamConnection {
	if own := findOwnInclude(files, dir); own != nil {
		return &StreamConnection{Mode: own.mode, Path: own.path}
	}
	return nil
}

// backupFor is the copy a change to target keeps of the file as it was, or ""
// where an include would read that copy as configuration — by the name the
// configuration gives the file or by the one it resolves to, since the copy
// sits beside both.
func backupFor(files []ConfigFile, nginxDir, named, target string, at time.Time) string {
	suffix := fmt.Sprintf(".jd-stream-%d.bak", at.Unix())
	for _, path := range []string{named + suffix, target + suffix} {
		if readByNginx(files, nginxDir, path) {
			return ""
		}
	}
	return target + suffix
}

// readByNginx reports whether an include anywhere in the configuration would
// take path, which a backup must never be.
func readByNginx(files []ConfigFile, nginxDir, path string) bool {
	for _, f := range files {
		directives, err := ParseNginxFile(f.Path, f.Content, nil)
		if err != nil {
			continue
		}
		for _, pattern := range streamIncludePatterns(directives) {
			if !filepath.IsAbs(pattern) {
				pattern = filepath.Join(nginxDir, pattern)
			}
			if ok, _ := filepath.Match(globToMatch(filepath.Clean(pattern)), path); ok {
				return true
			}
		}
	}
	return false
}

// reloadNginxChecked signals the reload after a change passed its test, and says so
// in the result. The change stays when it fails: it is valid, and it takes
// effect at the next reload that succeeds.
func reloadNginxChecked(ctx context.Context) (reloaded bool, output, failure string) {
	out, err := hostexec.Command(ctx, "nginx", "-s", "reload").CombinedOutput()
	output = strings.TrimSpace(string(out))
	if err != nil {
		failure = output
		if failure == "" {
			failure = err.Error()
		}
		return false, output, failure
	}
	return true, output, ""
}

// ApplyStreamInclude makes the change PlanStreamInclude showed. mode and path
// are the plan the operator saw: the plan is worked out again under the lock,
// and a different one is refused rather than made in its place.
func (s *Service) ApplyStreamInclude(ctx context.Context, mode, path string, reload bool) (*StreamIncludeResult, error) {
	if module := s.StreamModule(ctx); moduleMissing(module) {
		return nil, moduleRefusal(module)
	}
	listeners, listenErr := readListeners(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	plan, err := s.planStreamInclude(listeners, listenErr)
	if err != nil {
		return nil, err
	}
	if plan.Mode != mode || plan.Path != path {
		return nil, &StreamIncludeError{Code: "plan_changed", Reason: fmt.Sprintf(
			"the configuration changed since the change was shown: it would now be made in %s — look at it again", plan.Path)}
	}
	if len(plan.Conflicts) > 0 {
		return nil, &StreamIncludeError{Code: "port_in_use", Reason: "nginx could not bind every stream it would start reading: " +
			strings.Join(plan.Conflicts, "; ") + " — change or delete those streams first"}
	}
	target, err := s.includeTarget(plan.Path)
	if err != nil {
		return nil, err
	}
	// The plan's warnings already say when no copy is kept.
	res := &StreamIncludeResult{Mode: plan.Mode, Path: plan.Path, Streams: len(plan.Streams), Warnings: plan.Warnings}
	if plan.backup != "" {
		st, err := os.Stat(target)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(plan.backup, []byte(plan.Before), st.Mode().Perm()); err != nil {
			return nil, err
		}
		res.Backup = plan.backup
	}
	undo := func() {
		if plan.Exists {
			writeAtomic(target, plan.Before)
		} else {
			os.Remove(target)
		}
		if res.Backup != "" {
			os.Remove(res.Backup)
			res.Backup = ""
		}
	}
	if err := writeAtomic(target, plan.After); err != nil {
		undo()
		return nil, err
	}
	res.Validation = runValidator(ctx, "nginx", "-t")
	if !res.Validation.Valid {
		undo()
		return res, ErrInvalidConf
	}
	s.recordChange(ctx, Change{Path: target, Action: ChangeWrite,
		Before: []byte(plan.Before), BeforeExisted: plan.Exists, After: []byte(plan.After)})
	if reload {
		res.Reloaded, res.Output, res.ReloadError = reloadNginxChecked(ctx)
	}
	return res, nil
}

// RemoveStreamInclude takes out the include the dashboard added — the drop-in,
// the appended block or the marked line — tests, and reloads when asked. An
// include written by hand is left for the hand that wrote it.
func (s *Service) RemoveStreamInclude(ctx context.Context, reload bool) (*StreamIncludeResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.streamDir()
	files, err := readConfigFiles(s.nginxDir)
	if err != nil {
		return nil, &StreamIncludeError{Code: "config_unreadable", Reason: "nginx.conf could not be read: " + err.Error()}
	}
	own := findOwnInclude(files, dir)
	if own == nil {
		return nil, &StreamIncludeError{Code: "not_connected", Reason: "nothing the dashboard added includes " + dir +
			"; an include written by hand comes out by hand"}
	}
	target, err := s.includeTarget(own.path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	names, _ := stagedStreams(dir, nil)
	res := &StreamIncludeResult{Mode: own.mode, Path: own.path, Streams: len(names), Warnings: []string{}}
	if own.remove {
		err = os.Remove(target)
	} else {
		err = writeAtomic(target, own.after)
	}
	if err != nil {
		return nil, err
	}
	res.Validation = runValidator(ctx, "nginx", "-t")
	if !res.Validation.Valid {
		if own.remove {
			os.WriteFile(target, []byte(own.before), st.Mode().Perm())
		} else {
			writeAtomic(target, own.before)
		}
		return res, ErrInvalidConf
	}
	change := Change{Path: target, Action: ChangeWrite, Before: []byte(own.before), BeforeExisted: true, After: []byte(own.after)}
	if own.remove {
		change.Action, change.After = ChangeDelete, nil
	}
	s.recordChange(ctx, change)
	if reload {
		res.Reloaded, res.Output, res.ReloadError = reloadNginxChecked(ctx)
	}
	return res, nil
}
