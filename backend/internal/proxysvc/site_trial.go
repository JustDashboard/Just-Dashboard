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

// trialConfig is the configuration nginx would load with one more site
// enabled, built beside the live one rather than in it: a copy of nginx.conf
// whose include of the site directory reads a copy of that directory, which
// links every entry of the real one plus the site under the name enabling it
// gives it. nginx sorts the copy's entries as it sorts the real ones, so the
// site falls where enabling it would put it.
//
// A site saved disabled used to be tested with its link in the live
// sites-enabled for the length of the test. Only this service's own changes
// wait for its lock, and a reload or test from anywhere else — the reload
// and test endpoints, a site toggle or delete, certbot's hook, systemctl —
// read the site while the link was there: a disabled site went live on a
// reload that landed in the window, and one failing its test failed the
// reload. The live tree is never touched now.
type trialConfig struct {
	// main is the copy of nginx.conf, beside it so that a relative include
	// resolves where nginx resolves the original's; real is nginx.conf.
	main, real string
	// dir is the copy of from, the site directory nginx.conf includes.
	dir, from string
}

// trialUntested ends every reason a site could not be tested as enabled.
const trialUntested = ", so nginx could not test this site as enabled."

// stageTrial builds the trial configuration in which the file is read as
// entry of the directory from. It returns why it could not instead, as the
// sentence a result's note carries.
func (s *Service) stageTrial(from, entry, file string) (*trialConfig, string) {
	real := filepath.Join(s.nginxDir, "nginx.conf")
	raw, err := os.ReadFile(real)
	if err != nil {
		return nil, fmt.Sprintf("%s could not be read (%v)%s", real, err, trialUntested)
	}
	shown := strings.TrimPrefix(from, s.nginxDir+string(os.PathSeparator))
	directives, err := ParseNginxFile(real, string(raw), nil)
	if err != nil {
		return nil, fmt.Sprintf("nginx.conf could not be read for the test (%v)%s", err, trialUntested)
	}
	includes := trialIncludes(directives, s.nginxDir, from)
	if len(includes) == 0 {
		return nil, fmt.Sprintf("nginx.conf does not include %s itself%s", shown, trialUntested)
	}
	if !includedAs(includes, entry) {
		return nil, fmt.Sprintf("nginx.conf includes %s as %s, which does not match %s%s",
			shown, includes[0].Args[0], entry, trialUntested)
	}
	if _, err := os.Lstat(filepath.Join(from, entry)); err == nil {
		return nil, fmt.Sprintf("%s/%s is already there%s", shown, entry, trialUntested)
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		return nil, fmt.Sprintf("%s could not be read (%v)%s", shown, err, trialUntested)
	}

	copied, err := os.CreateTemp(s.nginxDir, ".jd-trial-*.conf")
	if err != nil {
		return nil, fmt.Sprintf("the copy of nginx.conf could not be written (%v)%s", err, trialUntested)
	}
	copied.Close()
	t := &trialConfig{main: copied.Name(), real: real, dir: strings.TrimSuffix(copied.Name(), ".conf") + ".d", from: from}
	failed := func(err error) (*trialConfig, string) {
		t.remove()
		return nil, fmt.Sprintf("the copy of the configuration could not be made (%v)%s", err, trialUntested)
	}
	// The copy is spliced into nginx.conf's own words, so the path goes in
	// unquoted or inside the original's quotes and must read the same in
	// both.
	if strings.ContainsAny(t.dir, " \t\r\n;{}#\"'\\$") {
		return failed(fmt.Errorf("%s holds a character nginx reads as syntax", t.dir))
	}
	if err := os.Mkdir(t.dir, 0o755); err != nil {
		return failed(err)
	}
	for _, e := range entries {
		if err := os.Symlink(filepath.Join(from, e.Name()), filepath.Join(t.dir, e.Name())); err != nil {
			return failed(err)
		}
	}
	if err := os.Symlink(file, filepath.Join(t.dir, entry)); err != nil {
		return failed(err)
	}
	content, ok := spliceIncludes(string(raw), includes, t.dir)
	if !ok {
		t.remove()
		return nil, "nginx.conf spells its include of " + shown + " in a way the test cannot copy" + trialUntested
	}
	if err := os.WriteFile(t.main, []byte(content), 0o600); err != nil {
		return failed(err)
	}
	return t, ""
}

// trialIncludes are the includes, anywhere in directives, whose pattern
// names files directly in dir.
func trialIncludes(directives []Directive, prefix, dir string) []Directive {
	var out []Directive
	for _, d := range directives {
		if d.Name == "include" && d.Block == nil && len(d.Args) == 1 {
			pattern := d.Args[0]
			if !filepath.IsAbs(pattern) {
				pattern = filepath.Join(prefix, pattern)
			}
			if parent := filepath.Dir(filepath.Clean(pattern)); parent == dir || resolvePath(parent) == resolvePath(dir) {
				out = append(out, d)
			}
		}
		out = append(out, trialIncludes(d.Block, prefix, dir)...)
	}
	return out
}

// includedAs says whether one of the includes reads a file named entry, the
// way glob(3) matches for nginx: a leading dot only where the pattern has one.
func includedAs(includes []Directive, entry string) bool {
	for _, d := range includes {
		base := filepath.Base(d.Args[0])
		if strings.HasPrefix(entry, ".") && !strings.HasPrefix(base, ".") {
			continue
		}
		if ok, _ := filepath.Match(globToMatch(base), entry); ok {
			return true
		}
	}
	return false
}

// spliceIncludes points each include at dir in place of its own directory,
// keeping every other byte of content — and so every line number nginx
// names in it. An include is found by its argument as written from its own
// line on; one written with escapes is not found, and the copy is refused.
func spliceIncludes(content string, includes []Directive, dir string) (string, bool) {
	lineStarts := []int{0}
	for i, c := range content {
		if c == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	var b strings.Builder
	done := 0
	for _, d := range includes {
		if d.Line < 1 || d.Line > len(lineStarts) {
			return "", false
		}
		from := max(lineStarts[d.Line-1], done)
		at := strings.Index(content[from:], d.Args[0])
		if at < 0 {
			return "", false
		}
		at += from
		b.WriteString(content[done:at])
		b.WriteString(filepath.Join(dir, filepath.Base(d.Args[0])))
		done = at + len(d.Args[0])
	}
	b.WriteString(content[done:])
	return b.String(), true
}

// validate is `nginx -t` on the trial configuration, speaking of the files
// the trial stands in for: nginx.conf rather than its copy, and the site
// directory rather than the copy of it.
func (t *trialConfig) validate(ctx context.Context) *ValidationResult {
	res := runValidator(ctx, "nginx", "-t", "-c", t.main)
	main, real := resolvedFile(t.main), resolvedFile(t.real)
	res.Output = t.rename(res.Output)
	res.Command = t.rename(res.Command)
	for i, d := range res.Diagnostics {
		// A copied entry was resolved to its own file while the copy
		// stood; one that resolves nowhere still names the copy.
		if d.File == main {
			res.Diagnostics[i].File = real
		} else {
			res.Diagnostics[i].File = t.rename(d.File)
		}
		res.Diagnostics[i].Message = t.rename(d.Message)
	}
	return res
}

func (t *trialConfig) rename(text string) string {
	text = strings.ReplaceAll(text, t.main, t.real)
	return strings.ReplaceAll(text, t.dir+string(os.PathSeparator), t.from+string(os.PathSeparator))
}

// dump is `nginx -T` on the trial configuration, for the order nginx would
// read its server blocks in. Its paths are the trial's: they resolve to the
// real files only while the trial stands.
func (t *trialConfig) dump(ctx context.Context) ([]ConfigFile, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := hostexec.Command(ctx, "nginx", "-T", "-c", t.main).Output()
	if err != nil {
		return nil, fmt.Errorf("nginx -T: %w", err)
	}
	return ParseEffective(string(out)), nil
}

// remove takes the trial away. The copy of the directory holds only links,
// which RemoveAll removes without following.
func (t *trialConfig) remove() {
	_ = os.RemoveAll(t.dir)
	_ = os.Remove(t.main)
}
