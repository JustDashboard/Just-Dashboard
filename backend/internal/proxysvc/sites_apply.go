package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// SiteResult reports what happened to a site, in order.
type SiteResult struct {
	Name       string            `json:"name"`
	Path       string            `json:"path"`
	Content    string            `json:"content"`
	Warnings   []string          `json:"warnings"`
	Validation *ValidationResult `json:"validation,omitempty"`
	// Conflicts are this site's names that nginx found another server block
	// already answering on the same address. A save is refused over them
	// unless it allows them, since nginx serves each name from one block
	// only and "ignores" the other with nothing but a warning.
	Conflicts []ServerNameConflict `json:"conflicts,omitempty"`
	// TestWarnings are the test's warnings placed in this site's own file.
	TestWarnings []Diagnostic `json:"testWarnings,omitempty"`
	Enabled      bool         `json:"enabled"`
	Reloaded     bool         `json:"reloaded"`
	// ReloadError is why nginx did not reload a configuration that tested
	// clean. The site is written and in place, and nginx goes on serving
	// what it served before until something reloads it.
	ReloadError string `json:"reloadError,omitempty"`
	Output      string `json:"output,omitempty"`
}

// ServerNameConflict is one of a site's names that another server block
// already answers on the same address.
type ServerNameConflict struct {
	Domain string `json:"domain"`
	// Listen is the address as nginx names it: 0.0.0.0:80, [::]:443.
	Listen string `json:"listen"`
	// Site is the other enabled site serving the name there, when one of the
	// listed sites does; it may also be a block in nginx.conf or elsewhere.
	Site string `json:"site,omitempty"`
}

// ErrServerNameConflict refuses a save nginx would half ignore.
var ErrServerNameConflict = errors.New("another server block already answers to one of this site's names")

// errSiteReloadFailed marks a save that tested clean and is in place, and
// that nginx did not pick up.
var errSiteReloadFailed = errors.New("reload failed")

// SiteSave is how a site is saved.
type SiteSave struct {
	// Enable links the site into sites-enabled. Without it the link is left
	// as it was, so saving a disabled site keeps it disabled — the form used
	// to enable every site it saved.
	Enable bool
	Reload bool
	// Overwrite replaces a site of the same name; without it one is refused.
	Overwrite bool
	// AllowConflict saves a site even when one of its names is already
	// answered on the same address by another server block.
	AllowConflict bool
}

// ApplySite is SaveSite without allowing a server-name conflict.
func (s *Service) ApplySite(ctx context.Context, spec *SiteSpec, enable, reload, overwrite bool) (*SiteResult, error) {
	return s.SaveSite(ctx, spec, SiteSave{Enable: enable, Reload: reload, Overwrite: overwrite})
}

// SaveSite writes a site, enables it when asked, tests the whole
// configuration and puts everything back if the test fails.
//
// The order matters and is different from the plain config editor's. A brand
// new file in sites-available is not in nginx's include tree, so `nginx -t`
// has nothing to say about it — the existing editor documents that gap. Here
// the symlink goes in *before* the test, which is what makes the test mean
// something, and both the file and the link are undone together if it fails.
//
// A reload that fails after a clean test is not an error here: the site was
// saved, and the result says what nginx did not do.
func (s *Service) SaveSite(ctx context.Context, spec *SiteSpec, opts SiteSave) (*SiteResult, error) {
	content, err := RenderNginx(spec)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.saveSiteLocked(ctx, spec, content, opts)
	if errors.Is(err, errSiteReloadFailed) {
		return res, nil
	}
	return res, err
}

// applySiteLocked performs ApplySite after rendering. The caller holds s.mu;
// deployment cutovers use this form so snapshot, apply, and recovery are one
// serialized proxy transaction. They are not refused over a server-name
// conflict, and a failed reload is their error: their recovery is built on
// nginx's own test and reload.
func (s *Service) applySiteLocked(ctx context.Context, spec *SiteSpec, content string, enable, reload, overwrite bool) (*SiteResult, error) {
	return s.saveSiteLocked(ctx, spec, content, SiteSave{
		Enable: enable, Reload: reload, Overwrite: overwrite, AllowConflict: true,
	})
}

func (s *Service) saveSiteLocked(ctx context.Context, spec *SiteSpec, content string, opts SiteSave) (*SiteResult, error) {
	enable := opts.Enable
	available := filepath.Join(s.nginxDir, "sites-available", spec.Name)
	if _, err := os.Stat(filepath.Dir(available)); err != nil {
		// A host keeping everything in conf.d has no sites-available, and
		// there is no enable/disable there either. The suffix is added by
		// confdPath rather than here, so editing a site the listing calls
		// app.conf writes back over it instead of creating app.conf.conf.
		available = s.confdPath(spec.Name)
		enable = true
		if _, err := os.Stat(filepath.Dir(available)); err != nil {
			// Neither layout is present, which on a host that really runs
			// nginx means JD_NGINX_DIR points at the wrong place. Saying
			// which directory was looked for beats the "no such file or
			// directory" the write would otherwise fail with.
			return nil, fmt.Errorf("%s has neither a sites-available nor a conf.d directory — set JD_NGINX_DIR to where this host keeps its nginx configuration", s.nginxDir)
		}
	}
	full, err := s.allowedPath(available)
	if err != nil {
		return nil, err
	}
	original, existed := readIfPresent(full)
	if existed && !opts.Overwrite {
		return nil, fmt.Errorf("a site called %s already exists", spec.Name)
	}
	link := filepath.Join(s.nginxDir, "sites-enabled", spec.Name)
	if !enable {
		// Leaving the link as it was: a site that has one is enabled, and
		// relinking it makes sure the link names the file being saved.
		if _, err := os.Lstat(link); err == nil {
			enable = true
		}
	}

	res := &SiteResult{
		Name: spec.Name, Path: full, Content: content, Warnings: SpecWarnings(spec),
	}
	if err := writeAtomic(full, content); err != nil {
		return nil, err
	}
	undoLink := func() {}
	if !strings.Contains(full, "sites-available") {
		// In the conf.d layout every present file is active, so there is no
		// symlink to make and nothing to report as pending.
		res.Enabled = true
	} else if enable {
		undo, err := linkEnabled(link, full)
		if err != nil {
			// The write is undone rather than left standing: a file in
			// sites-available that nginx does not include is invisible
			// everywhere except the next person to wonder why the site is
			// not serving.
			restoreConfig(full, original, existed)
			return nil, err
		}
		undoLink = undo
		res.Enabled = true
	}

	res.Validation = runValidator(ctx, "nginx", "-t")
	if !res.Validation.Valid {
		undoLink()
		restoreConfig(full, original, existed)
		res.Enabled = false
		return res, ErrInvalidConf
	}
	// nginx passes a second server block claiming a name on an address the
	// first already answers it on, and serves only one of them. Which one is
	// include order, so saving could as easily take a domain from a working
	// site as leave the new one unreachable.
	res.Conflicts = s.serverNameConflicts(res.Validation, spec.Domains, content, full)
	if len(res.Conflicts) > 0 && !opts.AllowConflict {
		undoLink()
		restoreConfig(full, original, existed)
		res.Enabled = false
		return res, ErrServerNameConflict
	}
	res.TestWarnings = warningsIn(res.Validation, full)
	s.recordChange(ctx, Change{Path: full, Action: ChangeWrite,
		Before: []byte(original), BeforeExisted: existed, After: []byte(content)})
	if opts.Reload {
		raw, err := hostexec.Command(ctx, "nginx", "-s", "reload").CombinedOutput()
		out := strings.TrimSpace(string(raw))
		res.Output = out
		if err != nil {
			// The config tested clean, so a reload failure is about the
			// running process rather than the file. Undoing the write would
			// lose the operator's work for a problem it did not cause.
			res.ReloadError = out
			if res.ReloadError == "" {
				res.ReloadError = err.Error()
			}
			return res, fmt.Errorf("%w: %s", errSiteReloadFailed, res.ReloadError)
		}
		res.Reloaded = true
	}
	return res, nil
}

// conflictingNameRe is nginx's warning for a server name a second block
// claims on an address the first already answers it on.
var conflictingNameRe = regexp.MustCompile(`^conflicting server name "([^"]*)" on (\S+), ignored$`)

var siteListenRe = regexp.MustCompile(`(?m)^\s*listen\s+([^;]+);`)

// serverNameConflicts reads nginx's own conflict warnings for this site's
// names on the addresses its file listens on. nginx's verdict rather than a
// comparison of listings: it already knows that 127.0.0.1:80 and a wildcard
// :80 are separate, and that a name is compared lowercased.
func (s *Service) serverNameConflicts(v *ValidationResult, domains []string, content, full string) []ServerNameConflict {
	names := map[string]bool{}
	for _, domain := range domains {
		names[strings.ToLower(domain)] = true
	}
	addresses := map[string]bool{}
	for _, m := range siteListenRe.FindAllStringSubmatch(content, -1) {
		addresses[listenAddress(m[1])] = true
	}
	out := []ServerNameConflict{}
	seen := map[string]bool{}
	var others []VHost
	listed := false
	for _, d := range v.Diagnostics {
		m := conflictingNameRe.FindStringSubmatch(d.Message)
		if d.Level != "warn" || m == nil || !names[m[1]] || !addresses[m[2]] {
			continue
		}
		// nginx warns once per address, so a site on 0.0.0.0:80 and [::]:80
		// is told once per port.
		key := m[1] + " " + listenPort(m[2])
		if seen[key] {
			continue
		}
		seen[key] = true
		if !listed {
			others, listed = s.nginxVHosts(), true
		}
		out = append(out, ServerNameConflict{
			Domain: m[1], Listen: m[2], Site: servingSite(others, full, m[1], m[2]),
		})
	}
	return out
}

// servingSite is the enabled site, other than the one at full, that lists
// name on address.
func servingSite(sites []VHost, full, name, address string) string {
	for _, v := range sites {
		if !v.Enabled || resolvedFile(v.Path) == full {
			continue
		}
		named := false
		for _, n := range v.ServerNames {
			named = named || strings.EqualFold(n, name)
		}
		if !named {
			continue
		}
		for _, listen := range v.Listen {
			if listenAddress(listen) == address {
				return v.Name
			}
		}
	}
	return ""
}

// listenAddress is a listen value's address the way nginx names it in a
// warning: `listen 80` binds 0.0.0.0:80, and an address with no port is on
// 80.
func listenAddress(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	address := fields[0]
	if _, err := strconv.Atoi(address); err == nil {
		return "0.0.0.0:" + address
	}
	host, port := address, "80"
	if i := strings.LastIndex(address, ":"); i >= 0 && !strings.HasSuffix(address, "]") {
		host, port = address[:i], address[i+1:]
	}
	if host == "*" {
		host = "0.0.0.0"
	}
	return host + ":" + port
}

// listenPort is the port of an address as listenAddress names it.
func listenPort(address string) string {
	return address[strings.LastIndex(address, ":")+1:]
}

// ConflictSummary says who already answers each conflicting name, in one
// sentence the form can show as it is.
func ConflictSummary(conflicts []ServerNameConflict) string {
	type owner struct{ domain, site string }
	var order []owner
	listens := map[owner][]string{}
	for _, c := range conflicts {
		key := owner{c.Domain, c.Site}
		if _, ok := listens[key]; !ok {
			order = append(order, key)
		}
		listens[key] = append(listens[key], c.Listen)
	}
	parts := make([]string, 0, len(order))
	for _, key := range order {
		site := key.site
		if site == "" {
			site = "another server block"
		}
		parts = append(parts, fmt.Sprintf("%s is already served by %s on %s",
			key.domain, site, strings.Join(listens[key], " and ")))
	}
	return strings.Join(parts, "; ") + ". nginx answers a name from one server block and ignores the other."
}

// warningsIn are the test's warnings placed in file.
func warningsIn(v *ValidationResult, file string) []Diagnostic {
	out := []Diagnostic{}
	for _, d := range v.Diagnostics {
		if d.Level == "warn" && d.File == file {
			out = append(out, d)
		}
	}
	return out
}

// ReadSiteSpec reads a site the listing names back into the form: its spec,
// whether this dashboard wrote the file, and the file itself.
//
// Through ReadConfig's allowlist rather than a joined path, since a name that
// turns out to be a path is exactly what that check exists for. On a conf.d
// host the listing names a site app.conf; its spec is called app, the name
// saving it writes back to, where app.conf rendered its logs as
// app.conf.access.log and an edit moved them.
func (s *Service) ReadSiteSpec(name string) (*SiteSpec, bool, string, error) {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return nil, false, "", fmt.Errorf("invalid site name")
	}
	candidates := []struct {
		path, spec string
	}{
		{filepath.Join(s.nginxDir, "sites-available", name), name},
		// Both spellings of the conf.d layout: the listing on such a host
		// reports a name that already ends in .conf, and a host that was set
		// up by hand may have a file without it.
		{filepath.Join(s.nginxDir, "conf.d", name), strings.TrimSuffix(name, ".conf")},
		{filepath.Join(s.nginxDir, "conf.d", name+".conf"), name},
	}
	var err error
	for _, c := range candidates {
		var content string
		content, err = s.ReadConfig(c.path)
		if err == nil {
			spec, managed := ParseSiteSpec(c.spec, content)
			return spec, managed, content, nil
		}
	}
	return nil, false, "", err
}

// linkEnabled points sites-enabled at this file, and returns the undo.
//
// Three cases rather than one, because two of them used to be reported as
// success and were not. A link that already points somewhere else is replaced:
// leaving it meant the new file was never in nginx's include tree while the
// page said the site was enabled. And a symlink that could not be created at
// all is an error now — the earlier version swallowed it and set Enabled true
// regardless, so a read-only or missing sites-enabled produced a site that had
// been "enabled" and was serving nothing.
func linkEnabled(link, target string) (func(), error) {
	previous := ""
	restore := func() {
		_ = os.Remove(link)
		if previous != "" {
			_ = os.Symlink(previous, link)
		}
	}
	if existing, err := os.Readlink(link); err == nil {
		if existing == target {
			return func() {}, nil
		}
		previous = existing
		if err := os.Remove(link); err != nil {
			return nil, err
		}
	} else if _, err := os.Lstat(link); err == nil {
		// Not a symlink: a real file sitting where the link belongs. Removing
		// somebody's configuration is not this function's decision.
		return nil, fmt.Errorf("%s already exists and is not a symlink — move it aside first", link)
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		restore()
		return nil, err
	}
	if err := os.Symlink(target, link); err != nil {
		restore()
		return nil, err
	}
	return restore, nil
}

func readIfPresent(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func restoreConfig(path, original string, existed bool) {
	if existed {
		writeAtomic(path, original)
		return
	}
	os.Remove(path)
}

// DeleteSite removes a site's file and its symlink.
//
// Both, and in that order: leaving the link behind points nginx at a file that
// no longer exists, which takes every site on the box down at the next reload.
func (s *Service) DeleteSite(ctx context.Context, name string) error {
	if !siteNameRe.MatchString(name) {
		return fmt.Errorf("invalid site name")
	}
	if isBackupFile(name) {
		// Unreachable from the listing, which hides these — but a second
		// delete of the same name must never be able to produce
		// <name>.bak.bak, which is the shape the old bug took.
		return fmt.Errorf("%s is a backup of a deleted site, not a site — remove it from the file manager if you no longer want it", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	removedLink := false
	link := filepath.Join(s.nginxDir, "sites-enabled", name)
	if _, err := os.Lstat(link); err == nil {
		if err := os.Remove(link); err != nil {
			return err
		}
		removedLink = true
		s.forgetEffective()
	}
	for _, candidate := range []string{
		filepath.Join(s.nginxDir, "sites-available", name),
		s.confdPath(name),
	} {
		full, err := s.allowedPath(candidate)
		if err != nil {
			continue
		}
		if _, err := os.Stat(full); err != nil {
			continue
		}
		// Kept as .bak for the same reason a compose file is: validation
		// catches a broken config, not a correct one that says the wrong
		// thing, and the only cure for the second is the previous version.
		// The listing skips these, so the copy is a file on disk rather than
		// a site that comes back the moment the one it replaced is deleted.
		b, readErr := os.ReadFile(full)
		if readErr == nil {
			os.WriteFile(full+".bak", b, 0o644)
		}
		if err := os.Remove(full); err != nil {
			return err
		}
		s.recordChange(ctx, Change{Path: full, Action: ChangeDelete, Before: b, BeforeExisted: true})
		return nil
	}
	if removedLink {
		// A link with nothing behind it is exactly what takes every site on
		// the box down at the next reload, so removing it is the whole job
		// and reporting failure afterwards would be wrong.
		return nil
	}
	return fmt.Errorf("no such site: %s", name)
}
