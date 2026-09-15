package proxysvc

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// ParseSiteSpec reads a site file back into the form.
//
// Not an nginx parser — a line reader that tracks brace depth and which
// location block it is inside, which is enough for the files this form
// produces and for the great majority of hand-written ones. What it cannot
// promise is that saving the form reproduces the file, so it reports whether
// the file carries our marker: a managed file round-trips, and for anything
// else the UI says plainly that saving will replace what is there.
func ParseSiteSpec(name, content string) (*SiteSpec, bool) {
	spec := &SiteSpec{
		Name: name, Kind: "proxy",
		Domains: []string{}, AllowFrom: []string{}, DenyFrom: []string{}, Locations: []SiteLocation{},
	}
	managed := strings.Contains(content, managedMarker)

	seenDomains := map[string]bool{}
	depth := 0
	location := ""
	var current *SiteLocation
	rootLocationUpstream := ""
	rootLocationWS := false
	sawTLSListen := false
	sawPlainRedirect := false
	var custom []string
	inCustom := false

	sc := bufio.NewScanner(strings.NewReader(content))
	sc.Buffer(make([]byte, 0, 8192), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		raw := strings.TrimSpace(line)
		// Everything after the custom marker belongs to the operator, so it is
		// collected verbatim rather than parsed. Without this the "extra
		// configuration" box was written to the file and silently dropped the
		// next time anybody opened the form and saved — the form's own escape
		// hatch was the one field an edit destroyed.
		if inCustom {
			custom = append(custom, strings.TrimPrefix(line, "    "))
			continue
		}
		if raw == customMarker {
			inCustom = true
			continue
		}
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		if m := locationOpenRe.FindStringSubmatch(raw); m != nil {
			depth++
			location = m[1]
			// The exploit blocks are this renderer's, not the operator's, and
			// the switch that produced them is a field of its own. Reading
			// them back as locations would drop them; reading them back as
			// the flag is what makes the switch survive an edit.
			if location == exploitDotLocation {
				spec.BlockExploits = true
			}
			// The ACME challenge location belongs to the redirect block this
			// renderer writes for a forced-HTTPS site. Reading it back as one
			// of the operator's own locations makes a round trip emit it
			// twice — once in the redirect server and once inside the TLS
			// one, where it does nothing.
			if location != "/" && location != acmeChallengePath &&
				!strings.HasPrefix(location, "~") && locationPathRe.MatchString(location) {
				spec.Locations = append(spec.Locations, SiteLocation{Path: location})
				current = &spec.Locations[len(spec.Locations)-1]
			} else {
				current = nil
			}
			continue
		}
		if strings.HasSuffix(raw, "{") {
			depth++
			continue
		}
		if raw == "}" {
			depth--
			location, current = "", nil
			continue
		}

		directive, value := cutDirective(raw)
		switch directive {
		case "server_name":
			for _, d := range strings.Fields(value) {
				if d == "_" || seenDomains[d] {
					continue
				}
				seenDomains[d] = true
				spec.Domains = append(spec.Domains, d)
			}
		case "listen":
			if strings.Contains(value, "ssl") || strings.HasPrefix(value, "443") {
				spec.TLS = true
				sawTLSListen = true
			}
		case "http2":
			spec.HTTP2 = value == "on"
		case "ssl_certificate":
			spec.CertPath = value
		case "ssl_certificate_key":
			spec.KeyPath = value
		case "client_max_body_size":
			spec.ClientMaxBody = value
		case "gzip":
			spec.Gzip = value == "on"
		case "access_log":
			spec.AccessLog = value != "off"
		case "auth_basic":
			spec.BasicAuthRealm = strings.Trim(value, `"`)
		case "auth_basic_user_file":
			spec.BasicAuthFile = value
		case "allow":
			// Server level only, like deny: an allow inside a location
			// restricts that one path, and hoisting it into the form's
			// site-wide list would apply it to the whole site on the next
			// save — a widening or a narrowing nobody asked for.
			if location == "" {
				spec.AllowFrom = append(spec.AllowFrom, value)
			}
		case "deny":
			if location == "" {
				spec.DenyFrom = append(spec.DenyFrom, value)
			}
		case "add_header":
			if strings.HasPrefix(value, "Strict-Transport-Security") {
				spec.HSTS = true
			}
			if strings.HasPrefix(value, "X-Content-Type-Options") {
				spec.SecurityHeaders = true
			}
		case "root":
			if location == acmeChallengePath && value == deploymentACMEWebroot {
				spec.ManagedACME = true
			} else if location == "" || location == "/" {
				spec.Root = value
			} else if current != nil {
				current.Root = value
			}
		case "proxy_pass":
			if current != nil {
				current.Upstream = value
			} else if location == "/" || location == "" {
				rootLocationUpstream = value
			}
		case "proxy_set_header":
			if strings.HasPrefix(value, "Upgrade") {
				if current != nil {
					current.WebSockets = true
				} else {
					rootLocationWS = true
				}
			}
		case "proxy_read_timeout":
			spec.ProxyTimeout = parseSeconds(value)
		case "return":
			fields := strings.Fields(value)
			if len(fields) >= 2 {
				target := fields[1]
				if strings.Contains(target, "$host$request_uri") {
					sawPlainRedirect = true
				} else if strings.HasPrefix(target, "http") {
					spec.Kind = "redirect"
					spec.RedirectTo = strings.TrimSuffix(target, "$request_uri")
					spec.Permanent = fields[0] == "301"
				}
			}
		}
	}

	spec.Upstream = rootLocationUpstream
	spec.WebSockets = rootLocationWS
	if spec.Kind != "redirect" {
		if spec.Upstream == "" && spec.Root != "" {
			spec.Kind = "static"
		}
	}
	spec.ForceHTTPS = sawTLSListen && sawPlainRedirect
	// A file with a plain-HTTP redirect block and nothing else is a redirect
	// site; one that also serves something is a TLS site forcing HTTPS.
	if spec.Kind == "proxy" && spec.Upstream == "" && spec.Root == "" && sawPlainRedirect {
		spec.Kind = "redirect"
		spec.RedirectTo = "https://" + firstOr(spec.Domains, "")
		spec.Permanent = true
	}
	// Extra locations that ended up with neither an upstream nor a root are
	// something this form cannot express; dropping them is better than
	// offering to save a location that proxies nowhere.
	kept := spec.Locations[:0]
	for _, loc := range spec.Locations {
		if loc.Upstream != "" || loc.Root != "" {
			kept = append(kept, loc)
		}
	}
	spec.Locations = kept
	// The custom block is everything between the marker and the server's
	// closing brace, which the renderer always writes last.
	for len(custom) > 0 && strings.TrimSpace(custom[len(custom)-1]) == "" {
		custom = custom[:len(custom)-1]
	}
	if len(custom) > 0 && strings.TrimSpace(custom[len(custom)-1]) == "}" {
		custom = custom[:len(custom)-1]
	}
	spec.Custom = strings.TrimRight(strings.Join(custom, "\n"), "\n")
	return spec, managed
}

const acmeChallengePath = "/.well-known/acme-challenge/"

// customMarker introduces the operator's own directives, and exploitDotLocation
// is the first location renderExploitBlocks writes. Both are read back by the
// parser, so they are constants shared with the renderer rather than strings
// repeated in two files that can drift apart.
const (
	customMarker       = "# Added by hand from the site form."
	exploitDotLocation = `/\.(?!well-known)`
)

var locationOpenRe = regexp.MustCompile(`^location\s+(?:[~^=*]+\s+)?(\S+)\s*\{`)

func cutDirective(line string) (string, string) {
	line = strings.TrimSuffix(strings.TrimSpace(line), ";")
	name, value, ok := strings.Cut(line, " ")
	if !ok {
		return name, ""
	}
	return name, strings.TrimSpace(value)
}

func parseSeconds(value string) int {
	value = strings.TrimSuffix(strings.TrimSpace(value), "s")
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return n
}

func firstOr(list []string, fallback string) string {
	if len(list) == 0 {
		return fallback
	}
	return list[0]
}

// SiteResult reports what happened to a site, in order.
type SiteResult struct {
	Name       string            `json:"name"`
	Path       string            `json:"path"`
	Content    string            `json:"content"`
	Warnings   []string          `json:"warnings"`
	Validation *ValidationResult `json:"validation,omitempty"`
	Enabled    bool              `json:"enabled"`
	Reloaded   bool              `json:"reloaded"`
	Output     string            `json:"output,omitempty"`
}

// ApplySite writes a site, enables it, tests the whole configuration and puts
// everything back if the test fails.
//
// The order matters and is different from the plain config editor's. A brand
// new file in sites-available is not in nginx's include tree, so `nginx -t`
// has nothing to say about it — the existing editor documents that gap. Here
// the symlink goes in *before* the test, which is what makes the test mean
// something, and both the file and the link are undone together if it fails.
func (s *Service) ApplySite(ctx context.Context, spec *SiteSpec, enable, reload, overwrite bool) (*SiteResult, error) {
	content, err := RenderNginx(spec)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applySiteLocked(ctx, spec, content, enable, reload, overwrite)
}

// applySiteLocked performs ApplySite after rendering. The caller holds s.mu;
// deployment cutovers use this form so snapshot, apply, and recovery are one
// serialized proxy transaction.
func (s *Service) applySiteLocked(ctx context.Context, spec *SiteSpec, content string, enable, reload, overwrite bool) (*SiteResult, error) {
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
	if existed && !overwrite {
		return nil, fmt.Errorf("a site called %s already exists", spec.Name)
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
		undo, err := linkEnabled(filepath.Join(s.nginxDir, "sites-enabled", spec.Name), full)
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
	if reload {
		raw, err := hostexec.Command(ctx, "nginx", "-s", "reload").CombinedOutput()
		out := strings.TrimSpace(string(raw))
		res.Output = out
		if err != nil {
			// The config tested clean, so a reload failure is about the
			// running process rather than the file. Undoing the write would
			// lose the operator's work for a problem it did not cause.
			return res, fmt.Errorf("reload failed: %s", out)
		}
		res.Reloaded = true
	}
	return res, nil
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
		if b, err := os.ReadFile(full); err == nil {
			os.WriteFile(full+".bak", b, 0o644)
		}
		return os.Remove(full)
	}
	if removedLink {
		// A link with nothing behind it is exactly what takes every site on
		// the box down at the next reload, so removing it is the whole job
		// and reporting failure afterwards would be wrong.
		return nil
	}
	return fmt.Errorf("no such site: %s", name)
}
