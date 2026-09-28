package proxysvc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// ErrBadSetting is a settings change the service refuses before touching a
// file: an unknown directive, a value it does not take, a file it cannot edit.
var ErrBadSetting = errors.New("setting refused")

// Setting is one of nginx's server-wide directives as nginx loads it: its
// value, where that value is written or that it is nginx's default, and what
// to make of it.
type Setting struct {
	Name string `json:"name"`
	// Context is the block the directive is read from: "http" or "events".
	Context string `json:"context"`
	Value   string `json:"value"`
	// Set is false when no file writes the directive and Value is nginx's
	// default.
	Set  bool   `json:"set"`
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	// Overrides counts the server and location blocks that set the directive
	// again, where this value does not apply.
	Overrides int    `json:"overrides"`
	Default   string `json:"default"`
	// Level is "ok", "notice" or "warning".
	Level  string `json:"level"`
	Advice string `json:"advice"`
	// Recommended is the value Apply would write, empty when there is no one
	// value to recommend.
	Recommended string `json:"recommended,omitempty"`
	// Choices are the values the directive takes, for a closed choice.
	Choices []string `json:"choices,omitempty"`
}

// SettingsReport is every Setting, and the limit worker_connections is read
// against.
type SettingsReport struct {
	Settings []Setting `json:"settings"`
	// OpenFiles is how many descriptors each worker may hold: the
	// worker_rlimit_nofile directive, or the soft limit the running master
	// has, which its workers inherit. 0 when neither can be read.
	OpenFiles int `json:"openFiles"`
	// OpenFilesFrom says which of the two OpenFiles is.
	OpenFilesFrom string `json:"openFilesFrom,omitempty"`
	// Target is the file a directive no file sets is added to.
	Target string `json:"target"`
}

type settingDef struct {
	name    string
	context string
	def     string
	valid   func(string) bool
	choices []string
	judge   func(value string, rep *SettingsReport) (level, advice, recommended string)
}

var (
	sizeValue    = regexp.MustCompile(`^[0-9]+[kKmMgG]?$`)
	timeValue    = `[0-9]+(ms|s|m|h|d)?`
	keepValue    = regexp.MustCompile(`^` + timeValue + `( ` + timeValue + `)?$`)
	numberValue  = regexp.MustCompile(`^[0-9]{1,7}$`)
	tlsProtocols = []string{"SSLv2", "SSLv3", "TLSv1", "TLSv1.1", "TLSv1.2", "TLSv1.3"}
)

func oneOf(values ...string) func(string) bool {
	return func(v string) bool { return slices.Contains(values, v) }
}

// settingDefs are the directives the settings page reads and writes, in the
// order it shows them. Each value check is strict enough that a value can
// never carry a second directive into the file.
var settingDefs = []settingDef{
	{
		name: "server_tokens", context: "http", def: "on",
		valid: oneOf("on", "off", "build"), choices: []string{"on", "off", "build"},
		judge: func(v string, _ *SettingsReport) (string, string, string) {
			if v == "off" {
				return "ok", "Error pages and the Server header say nginx without its version.", ""
			}
			return "notice", "Every error page and Server header names nginx's version, which tells a scanner which advisories apply. off leaves only the word nginx.", "off"
		},
	},
	{
		name: "client_max_body_size", context: "http", def: "1m",
		valid: sizeValue.MatchString,
		judge: func(string, *SettingsReport) (string, string, string) {
			return "ok", "A request body larger than this is refused with 413. A site that takes uploads is better raised on its own than every site here.", ""
		},
	},
	{
		name: "keepalive_timeout", context: "http", def: "75s",
		valid: keepValue.MatchString,
		judge: func(string, *SettingsReport) (string, string, string) {
			return "ok", "How long an idle client connection stays open. Each one holds a worker connection while it waits.", ""
		},
	},
	{
		name: "gzip", context: "http", def: "off",
		valid: oneOf("on", "off"), choices: []string{"on", "off"},
		judge: func(string, *SettingsReport) (string, string, string) {
			return "ok", "Compressing a response over TLS that mixes a secret with text a visitor chose is what the BREACH attack reads; gzip_types decides which responses are compressed.", ""
		},
	},
	{
		name: "ssl_protocols", context: "http", def: "TLSv1.2 TLSv1.3",
		valid: func(v string) bool {
			seen := map[string]bool{}
			for _, p := range strings.Fields(v) {
				if !slices.Contains(tlsProtocols, p) || seen[p] {
					return false
				}
				seen[p] = true
			}
			return len(seen) > 0 && strings.Join(strings.Fields(v), " ") == v
		},
		judge: func(v string, _ *SettingsReport) (string, string, string) {
			for _, p := range strings.Fields(v) {
				if p != "TLSv1.2" && p != "TLSv1.3" {
					return "warning", p + " is broken or deprecated (RFC 8996) and no current browser needs it. Servers that set ssl_protocols themselves are not changed by this.", "TLSv1.2 TLSv1.3"
				}
			}
			return "ok", "Only TLS 1.2 and 1.3. A server block that sets ssl_protocols itself keeps its own.", ""
		},
	},
	{
		name: "server_names_hash_bucket_size", context: "http", def: "32, 64 or 128 (the CPU's cache line)",
		valid: numberValue.MatchString,
		judge: func(string, *SettingsReport) (string, string, string) {
			return "ok", "nginx refuses to start when a server name does not fit and says so in its test; raise it then, to the next power of two.", ""
		},
	},
	{
		name: "worker_connections", context: "events", def: "512",
		valid: numberValue.MatchString,
		judge: func(v string, rep *SettingsReport) (string, string, string) {
			n, _ := strconv.Atoi(v)
			if rep.OpenFiles == 0 {
				return "ok", "Connections each worker may hold, clients and upstreams together. The open-file limit it is measured against could not be read.", ""
			}
			limit := strconv.Itoa(rep.OpenFiles)
			switch {
			case n > rep.OpenFiles:
				return "warning", "More than the " + limit + " files a worker may open, so a busy worker runs out of descriptors before it reaches this. Raise worker_rlimit_nofile or lower this.", limit
			case 2*n > rep.OpenFiles:
				return "notice", "A proxied request holds two descriptors, client and upstream, so a worker proxying runs out near " + strconv.Itoa(rep.OpenFiles/2) + " connections of its " + limit + " open files.", ""
			}
			return "ok", "Connections each worker may hold, clients and upstreams together, within its " + limit + " open files.", ""
		},
	},
}

func settingDefFor(name string) (settingDef, bool) {
	for _, d := range settingDefs {
		if d.name == name {
			return d, true
		}
	}
	return settingDef{}, false
}

// settingsFile is the file the dashboard adds a server-wide directive to when
// no file sets it: its own, in conf.d, which stock nginx reads inside http.
func (s *Service) settingsFile() string {
	return filepath.Join(s.nginxDir, "conf.d", "jd-http.conf")
}

// Settings reads the server-wide directives from the configuration nginx
// loads. It runs `nginx -T` through EffectiveConfig, so it must not be called
// with s.mu held.
func (s *Service) Settings(ctx context.Context) (*SettingsReport, error) {
	files, err := s.EffectiveConfig(ctx)
	if err != nil {
		return nil, err
	}
	tree, err := NginxTree(files)
	if err != nil {
		return nil, err
	}
	rep := &SettingsReport{Settings: []Setting{}, Target: s.settingsFile()}
	rep.OpenFiles, rep.OpenFilesFrom = workerOpenFiles(tree)
	for _, def := range settingDefs {
		if def.name == "ssl_protocols" {
			def.def = defaultProtocols(ctx)
		}
		set := Setting{Name: def.name, Context: def.context, Default: def.def, Value: def.def, Choices: def.choices}
		if d, ok := directiveIn(tree, def.context, def.name); ok {
			set.Set, set.Value, set.File, set.Line = true, strings.Join(d.Args, " "), d.File, d.Line
		}
		if def.context == "http" {
			set.Overrides = countNested(tree, def.name)
		}
		value := set.Value
		if !set.Set && def.name == "server_names_hash_bucket_size" {
			value = ""
		}
		set.Level, set.Advice, set.Recommended = def.judge(value, rep)
		if set.Recommended == set.Value {
			set.Recommended = ""
		}
		rep.Settings = append(rep.Settings, set)
	}
	return rep, nil
}

// defaultProtocols is ssl_protocols when nothing sets it: nginx 1.23.4
// narrowed it from TLSv1 to TLSv1.2 to TLS 1.2 and 1.3. A version that cannot
// be read is taken as older, so an unset directive is never called safe on a
// guess.
func defaultProtocols(ctx context.Context) string {
	const older, newer = "TLSv1 TLSv1.1 TLSv1.2", "TLSv1.2 TLSv1.3"
	out, _ := hostexec.Command(ctx, "nginx", "-v").CombinedOutput()
	version := strings.TrimPrefix(parseNginxVersion(string(out)), "nginx/")
	var parts [3]int
	for i, field := range strings.SplitN(version, ".", 3) {
		n, err := strconv.Atoi(field)
		if err != nil {
			return older
		}
		parts[i] = n
	}
	if parts[0] > 1 || parts[1] > 23 || (parts[1] == 23 && parts[2] >= 4) {
		return newer
	}
	return older
}

// directiveIn finds the directive name written directly in the top-level
// block called context — http or events — wherever the file it sits in was
// included from.
func directiveIn(tree []Directive, context, name string) (Directive, bool) {
	for _, top := range tree {
		if top.Name != context || top.Block == nil {
			continue
		}
		for _, d := range top.Block {
			if d.Name == name {
				return d, true
			}
		}
	}
	return Directive{}, false
}

// countNested counts the blocks inside http that set name themselves.
func countNested(tree []Directive, name string) int {
	n := 0
	var walk func([]Directive, int)
	walk = func(list []Directive, depth int) {
		for _, d := range list {
			if d.Name == name && depth > 1 {
				n++
			}
			if d.Block != nil {
				walk(d.Block, depth+1)
			}
		}
	}
	for _, top := range tree {
		if top.Name == "http" && top.Block != nil {
			walk(top.Block, 1)
		}
	}
	return n
}

// workerOpenFiles is how many files each worker may open: the
// worker_rlimit_nofile directive, or else the running master's soft limit,
// which a worker inherits. The master is found by nginx's pid file.
func workerOpenFiles(tree []Directive) (int, string) {
	pidFile := "/run/nginx.pid"
	for _, d := range tree {
		switch {
		case d.Name == "worker_rlimit_nofile" && len(d.Args) == 1:
			if n, err := strconv.Atoi(d.Args[0]); err == nil {
				return n, "worker_rlimit_nofile"
			}
		case d.Name == "pid" && len(d.Args) == 1 && filepath.IsAbs(d.Args[0]):
			pidFile = d.Args[0]
		}
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, ""
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0, ""
	}
	f, err := os.Open(filepath.Join("/proc", strconv.Itoa(pid), "limits"))
	if err != nil {
		return 0, ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		rest, ok := strings.CutPrefix(sc.Text(), "Max open files")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) > 0 {
			if n, err := strconv.Atoi(fields[0]); err == nil {
				return n, "the running master's open-file limit"
			}
		}
	}
	return 0, ""
}

// SettingChange is one directive and the value to give it.
type SettingChange struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SettingEdit is what a change does to one file.
type SettingEdit struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	File  string `json:"file"`
	DirectiveEdit
	// Created marks a file the change writes for the first time.
	Created bool `json:"created"`
	// Conffile marks a file the nginx package owns as a conffile: when an
	// upgrade ships a new version of it, dpkg asks whether to keep this edit
	// or take the package's.
	Conffile bool `json:"conffile"`
}

type settingsPlan struct {
	edits []SettingEdit
	// files are the new contents by path, in the order they are first
	// touched.
	order []string
	files map[string]string
}

// PreviewSettings is what ApplySettings would write, without writing it.
func (s *Service) PreviewSettings(ctx context.Context, changes []SettingChange) ([]SettingEdit, error) {
	plan, err := s.planSettings(ctx, changes)
	if err != nil {
		return nil, err
	}
	return plan.edits, nil
}

// ApplySettings writes each change into the file that sets the directive
// nginx reads, or adds it to the dashboard's own conf.d file when none does.
// Each file goes through WriteConfig, so nginx tests it in place and a
// refusal leaves the file as it was; a change that spans files writes them
// one by one, and the edits returned beside an error are the ones written.
func (s *Service) ApplySettings(ctx context.Context, changes []SettingChange) ([]SettingEdit, *ValidationResult, error) {
	plan, err := s.planSettings(ctx, changes)
	if err != nil {
		return nil, nil, err
	}
	var written []SettingEdit
	var last *ValidationResult
	for _, path := range plan.order {
		res, err := s.WriteConfig(ctx, KindNginx, path, plan.files[path])
		if res != nil {
			last = res
		}
		if err != nil {
			return written, last, err
		}
		for _, e := range plan.edits {
			if e.File == path {
				written = append(written, e)
			}
		}
	}
	return written, last, nil
}

func (s *Service) planSettings(ctx context.Context, changes []SettingChange) (*settingsPlan, error) {
	if len(changes) == 0 {
		return nil, fmt.Errorf("%w: no changes", ErrBadSetting)
	}
	seen := map[string]bool{}
	for _, c := range changes {
		def, ok := settingDefFor(c.Name)
		if !ok {
			return nil, fmt.Errorf("%w: %q is not a setting this page edits", ErrBadSetting, c.Name)
		}
		if !def.valid(c.Value) {
			return nil, fmt.Errorf("%w: %q is not a value %s takes", ErrBadSetting, c.Value, c.Name)
		}
		if seen[c.Name] {
			return nil, fmt.Errorf("%w: %s is changed twice", ErrBadSetting, c.Name)
		}
		seen[c.Name] = true
	}
	files, err := s.EffectiveConfig(ctx)
	if err != nil {
		return nil, err
	}
	tree, err := NginxTree(files)
	if err != nil {
		return nil, err
	}
	main := files[0]
	plan := &settingsPlan{files: map[string]string{}}
	created := map[string]bool{}
	load := func(path string) (string, error) {
		if content, ok := plan.files[path]; ok {
			return content, nil
		}
		content, err := s.ReadConfig(path)
		if errors.Is(err, os.ErrNotExist) && path == s.settingsFile() {
			created[path] = true
			content, err = dashboardMarker+": server-wide settings from Configuration > Settings.\n", nil
		}
		if err != nil {
			return "", err
		}
		plan.order = append(plan.order, path)
		plan.files[path] = content
		return content, nil
	}
	for _, c := range changes {
		def, _ := settingDefFor(c.Name)
		var path string
		var context []string
		if d, ok := directiveIn(tree, def.context, def.name); ok {
			path = d.File
			content, err := load(path)
			if err != nil {
				return nil, err
			}
			context, err = fileContext(path, content, d)
			if err != nil {
				return nil, err
			}
		} else if def.context == "http" && includedInHTTP(main, s.settingsFile()) {
			path = s.settingsFile()
		} else {
			path, context = main.Path, []string{def.context}
		}
		content, err := load(path)
		if err != nil {
			return nil, err
		}
		next, edit, err := SetDirective(content, context, def.name, c.Value)
		if err != nil {
			return nil, fmt.Errorf("%w: %s in %s: %v", ErrBadSetting, def.name, path, err)
		}
		plan.files[path] = next
		plan.edits = append(plan.edits, SettingEdit{
			Name: def.name, Value: c.Value, File: path, DirectiveEdit: edit,
			Created: created[path], Conffile: isConffile(path),
		})
	}
	return plan, nil
}

// fileContext is the blocks d sits in within its own file, which is what
// SetDirective is told. The tree names them from nginx.conf down; the file
// read from disk now is parsed on its own and d found in it by line, so a
// file edited since nginx printed it is refused rather than edited blind.
func fileContext(path, content string, d Directive) ([]string, error) {
	directives, err := ParseNginxFile(path, content, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadSetting, err)
	}
	var found []string
	var walk func([]Directive) bool
	walk = func(list []Directive) bool {
		for _, x := range list {
			if x.Name == d.Name && x.Line == d.Line && x.Block == nil {
				found = x.Context
				return true
			}
			if x.Block != nil && walk(x.Block) {
				return true
			}
		}
		return false
	}
	if !walk(directives) {
		return nil, fmt.Errorf("%w: %s changed since nginx read it; read the settings again", ErrBadSetting, path)
	}
	return found, nil
}

// includedInHTTP reports whether the main file's http block includes path,
// directly or by a pattern, the way nginx resolves a relative include.
func includedInHTTP(main ConfigFile, path string) bool {
	directives, err := ParseNginxFile(main.Path, main.Content, nil)
	if err != nil {
		return false
	}
	for _, top := range directives {
		if top.Name != "http" {
			continue
		}
		for _, d := range top.Block {
			if d.Name != "include" || len(d.Args) != 1 {
				continue
			}
			pattern := d.Args[0]
			if !filepath.IsAbs(pattern) {
				pattern = filepath.Join(filepath.Dir(main.Path), pattern)
			}
			if ok, _ := filepath.Match(filepath.Clean(pattern), path); ok {
				return true
			}
		}
	}
	return false
}

// isConffile reports whether a Debian nginx package lists path among the
// files dpkg asks about on upgrade.
func isConffile(path string) bool {
	lists, _ := filepath.Glob("/var/lib/dpkg/info/nginx*.conffiles")
	resolved := path
	if r, err := filepath.EvalSymlinks(path); err == nil {
		resolved = r
	}
	for _, list := range lists {
		raw, err := os.ReadFile(list)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && (line == path || line == resolved) {
				return true
			}
		}
	}
	return false
}
