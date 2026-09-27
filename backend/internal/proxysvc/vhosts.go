package proxysvc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// VHost is one virtual host. For nginx the enabled state is the presence of a
// symlink in sites-enabled, which is the convention Debian-family packages use
// and the one operators expect the toggle to drive.
type VHost struct {
	Name        string `json:"name"`
	Kind        Kind   `json:"kind"`
	Path        string `json:"path"`
	EnabledPath string `json:"enabledPath,omitempty"`
	Enabled     bool   `json:"enabled"`
	// Layout is the directory an nginx site was found in: sites-available,
	// conf.d, or sites-enabled for a file or link that is only there.
	Layout string `json:"layout,omitempty"`
	// Broken says the site's name in sites-enabled does not serve its file:
	// "dangling" is a link to nothing, which makes nginx refuse every reload,
	// and "stale" is a link to — or a copy of — some other file.
	Broken string `json:"broken,omitempty"`
	// LinkTarget is where the link in sites-enabled points, absolute, for a
	// broken site and for one that is only a link there.
	LinkTarget string `json:"linkTarget,omitempty"`
	// FormEditable says the site form reads this file and saves it back to
	// the same place. The form writes to sites-available where that exists
	// and to conf.d/<name> where it does not, so a conf.d file on a Debian
	// host, a file only in sites-enabled, or one outside the proxy's
	// directories would be saved somewhere else — beside the original, with
	// the same server names.
	FormEditable bool     `json:"formEditable"`
	ServerNames  []string `json:"serverNames"`
	Listen       []string `json:"listen"`
	Upstreams    []string `json:"upstreams"`
	TLS          bool     `json:"tls"`
	// CertPath is the first certificate the site names and CertPaths every
	// one, including those in a snippet it includes: an RSA and an ECDSA
	// pair is two, and a certificate only reached through its snippet is
	// still the one the site serves.
	CertPath  string    `json:"certPath,omitempty"`
	CertPaths []string  `json:"certPaths,omitempty"`
	Modified  time.Time `json:"modified"`
	Size      int64     `json:"size"`
}

var (
	serverNameRe = regexp.MustCompile(`(?m)^\s*server_name\s+([^;]+);`)
	listenRe     = regexp.MustCompile(`(?m)^\s*listen\s+([^;]+);`)
	proxyPassRe  = regexp.MustCompile(`(?m)^\s*proxy_pass\s+([^;]+);`)
	certRe       = regexp.MustCompile(`(?m)^\s*ssl_certificate\s+([^;]+);`)
	includeRe    = regexp.MustCompile(`(?m)^\s*include\s+([^;]+);`)
)

func (s *Service) ListVHosts(ctx context.Context) ([]VHost, error) {
	out := []VHost{}
	out = append(out, s.nginxVHosts()...)
	if caddy, err := s.caddySites(); err == nil {
		out = append(out, caddy...)
	}
	if edge, err := s.dockerCaddy(ctx); err != nil {
		return nil, err
	} else if edge != nil {
		sites, err := edge.vhosts(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, sites...)
	}
	// A name can be in more than one place — conf.d/app.conf beside
	// sites-available/app.conf — and a poll that swapped the two would move
	// the cards under the operator's pointer.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Layout < out[j].Layout
	})
	return out, nil
}

// backupSuffixes are the files that live beside a configuration and are not
// one. nginx reads none of them — sites-enabled is a directory of symlinks and
// conf.d is included as *.conf — but the listing used to show every one as a
// site in its own right. That made deleting a site produce a second site
// called <name>.bak, deleting *that* produce <name>.bak.bak, and a host where
// the package manager had ever written an .dpkg-old show a duplicate of every
// site it had touched.
var backupSuffixes = []string{
	".bak", ".old", ".orig", ".save", ".swp", ".tmp",
	".rpmsave", ".rpmnew", ".dpkg-old", ".dpkg-new", ".dpkg-dist",
	".ucf-old", ".ucf-new", ".ucf-dist",
}

// isBackupFile reports a file nginx will never read and the operator never
// asked for. The trailing tilde is every editor's own backup.
func isBackupFile(name string) bool {
	if strings.HasSuffix(name, "~") {
		return true
	}
	lower := strings.ToLower(name)
	for _, suffix := range backupSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// nginxVHosts lists every place nginx takes a site from.
//
// It used to read one directory: sites-available, or conf.d where there was
// none. On a Debian host that left out conf.d — which nginx.conf includes
// there too — and everything that is only in sites-enabled: a file copied in
// rather than linked, a link to a file kept elsewhere, and a link whose file
// is gone. That last one is the worst thing a proxy directory can hold,
// because nginx refuses every reload while it is there, and it was either
// invisible or, through Lstat, reported as a site that was serving.
func (s *Service) nginxVHosts() []VHost {
	available := filepath.Join(s.nginxDir, "sites-available")
	enabled := filepath.Join(s.nginxDir, "sites-enabled")
	confd := filepath.Join(s.nginxDir, "conf.d")
	info, err := os.Stat(available)
	debian := err == nil && info.IsDir()
	out := []VHost{}
	own := map[string]bool{}
	if debian {
		for _, name := range siteFiles(available) {
			full := filepath.Join(available, name)
			v := s.fileVHost(name, full, "sites-available")
			v.EnabledPath = filepath.Join(enabled, name)
			v.FormEditable = s.readable(full)
			switch state, target := readEnabledLink(v.EnabledPath, full); state {
			case linkServes:
				v.Enabled = true
			case linkDangling:
				v.Broken, v.LinkTarget = "dangling", target
			case linkElsewhere:
				v.Broken, v.LinkTarget = "stale", target
			}
			own[name] = true
			out = append(out, v)
		}
	}
	for _, name := range siteFiles(confd) {
		full := filepath.Join(confd, name)
		v := s.fileVHost(name, full, "conf.d")
		// conf.d is included as *.conf, so the suffix is the whole
		// difference between a file nginx reads and one it ignores. There
		// is no symlink to toggle either way, which EnabledPath staying
		// empty is what tells the UI.
		v.Enabled = strings.HasSuffix(name, ".conf")
		v.FormEditable = !debian && v.Enabled && s.readable(full)
		out = append(out, v)
	}
	if !debian {
		return out
	}
	entries, _ := os.ReadDir(enabled)
	for _, e := range entries {
		name := e.Name()
		// A name sites-available also has is that site's link and was
		// judged with it. Backups are not skipped here: nginx includes
		// sites-enabled/*, so an app.bak there is read like any other file.
		if own[name] || strings.HasPrefix(name, ".") {
			continue
		}
		link := filepath.Join(enabled, name)
		if info, err := os.Stat(link); err == nil && info.IsDir() {
			continue
		}
		v := s.fileVHost(name, link, "sites-enabled")
		v.Enabled = true
		if state, target := readEnabledLink(link, link); target != "" {
			v.EnabledPath, v.LinkTarget = link, target
			if state == linkDangling {
				v.Enabled, v.Broken, v.Path = false, "dangling", ""
			}
		}
		out = append(out, v)
	}
	return out
}

// siteFiles are the names in dir the listing considers: not directories,
// dotfiles or backups.
func siteFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || isBackupFile(e.Name()) {
			continue
		}
		out = append(out, e.Name())
	}
	return out
}

// readable is whether the config editor and the site form will open path:
// both read through allowedPath.
func (s *Service) readable(path string) bool {
	full, err := s.allowedPath(path)
	if err != nil || s.isPasswordFile(full) {
		return false
	}
	info, err := os.Stat(full)
	return err == nil && info.Mode().IsRegular()
}

// fileVHost is the listing's entry for the file at path, as far as the file
// itself says.
func (s *Service) fileVHost(name, path, layout string) VHost {
	v := VHost{
		Name: name, Kind: KindNginx, Path: path, Layout: layout,
		ServerNames: []string{}, Listen: []string{}, Upstreams: []string{},
	}
	if info, err := os.Stat(path); err == nil {
		v.Modified, v.Size = info.ModTime().UTC(), info.Size()
	} else if info, err := os.Lstat(path); err == nil {
		v.Modified = info.ModTime().UTC()
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return v
	}
	text := string(b)
	// A site that forces HTTPS is two server blocks with the same names, and
	// each name was listed once per block.
	for _, m := range serverNameRe.FindAllStringSubmatch(text, -1) {
		for _, name := range strings.Fields(m[1]) {
			v.ServerNames = appendNew(v.ServerNames, unquote(name))
		}
	}
	for _, m := range listenRe.FindAllStringSubmatch(text, -1) {
		fields := strings.Fields(m[1])
		if len(fields) == 0 {
			continue
		}
		v.Listen = appendNew(v.Listen, strings.Join(fields, " "))
		// A TLS listener is TLS whether or not its certificate is named in
		// this file; the http block may carry it for every site.
		for _, param := range fields[1:] {
			if param == "ssl" || param == "quic" {
				v.TLS = true
			}
		}
	}
	for _, m := range proxyPassRe.FindAllStringSubmatch(text, -1) {
		v.Upstreams = appendNew(v.Upstreams, unquote(strings.TrimSpace(m[1])))
	}
	certs := certificatePaths(text)
	for _, m := range includeRe.FindAllStringSubmatch(text, -1) {
		certs = append(certs, s.includedCertificates(unquote(strings.TrimSpace(m[1])))...)
	}
	for _, cert := range certs {
		v.CertPaths = appendNew(v.CertPaths, cert)
	}
	if len(v.CertPaths) > 0 {
		v.TLS = true
		v.CertPath = v.CertPaths[0]
	}
	return v
}

// certificatePaths are the ssl_certificate files a piece of configuration
// names, without the quotes nginx allows around them: a quoted path used to
// reach the certificate list quotes and all, and read as a file that did not
// exist.
func certificatePaths(text string) []string {
	out := []string{}
	for _, m := range certRe.FindAllStringSubmatch(text, -1) {
		out = append(out, unquote(strings.TrimSpace(m[1])))
	}
	return out
}

// includedCertificates reads the certificates out of the files an include
// names — Debian's snakeoil snippet is the stock example. One level deep,
// relative to the nginx directory as nginx resolves it, and only files the
// editor would show: certbot's options file sits outside the proxy's
// directories and names no certificate anyway.
func (s *Service) includedCertificates(pattern string) []string {
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(s.nginxDir, pattern)
	}
	matches, _ := filepath.Glob(pattern)
	out := []string{}
	for _, match := range matches {
		full, err := s.allowedPath(match)
		if err != nil || s.isPasswordFile(full) {
			continue
		}
		if b, err := os.ReadFile(full); err == nil {
			out = append(out, certificatePaths(string(b))...)
		}
	}
	return out
}

func appendNew(list []string, item string) []string {
	if item == "" || slices.Contains(list, item) {
		return list
	}
	return append(list, item)
}

func unquote(value string) string {
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		return value[1 : len(value)-1]
	}
	return value
}

// linkState is what the entry of a name in sites-enabled does for a file.
type linkState int

const (
	linkAbsent    linkState = iota // nothing under that name
	linkServes                     // resolves to the file
	linkDangling                   // a link to nothing: nginx cannot load the configuration
	linkElsewhere                  // a link to another file, or a file of its own
)

// readEnabledLink says what link does for file, and where link points when it
// is a symlink, made absolute. The answer is by identity, not by the text of
// the link: `ln -s ../sites-available/app` serves the file as surely as the
// absolute path does, and a link through another symlink to the same file is
// the same file.
func readEnabledLink(link, file string) (linkState, string) {
	info, err := os.Lstat(link)
	if err != nil {
		return linkAbsent, ""
	}
	target := ""
	if info.Mode()&os.ModeSymlink != 0 {
		target, _ = os.Readlink(link)
		if target != "" && !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(link), target)
		}
	}
	served, err := os.Stat(link)
	if err != nil {
		return linkDangling, target
	}
	if own, err := os.Stat(file); err == nil && os.SameFile(own, served) {
		return linkServes, target
	}
	return linkElsewhere, target
}

// caddySites treats the Caddyfile as a single vhost entry. Caddy's config is
// one file with site blocks rather than a directory of them, so "enable/disable
// a site" has no filesystem equivalent — the editor is the interface.
func (s *Service) caddySites() ([]VHost, error) {
	info, err := os.Stat(s.caddyFile)
	if err != nil {
		return nil, err
	}
	v := VHost{
		Name: filepath.Base(s.caddyFile), Kind: KindCaddy, Path: s.caddyFile,
		Enabled: true, Modified: info.ModTime().UTC(), Size: info.Size(),
		ServerNames: []string{}, Listen: []string{}, Upstreams: []string{},
	}
	if b, err := os.ReadFile(s.caddyFile); err == nil {
		v.ServerNames, v.Upstreams, v.TLS = parseCaddyfile(string(b))
	}
	return []VHost{v}, nil
}

// parseCaddyfile pulls the site addresses and the reverse_proxy targets out
// of a Caddyfile without being a Caddyfile parser, and says whether every
// site in it is served over HTTPS. The file is one entry in the listing, so
// a single plain-HTTP site in it makes the entry plain: the page must never
// call something TLS that is partly not.
//
// Only a block opened at the top level is a site address. Every directive
// that takes a block — handle, route, tls, header, encode, log — opens one
// too, and reading those as names put "header" and "handle" in the server
// list of every Caddyfile that used them. Brace depth is tracked line by
// line, which is enough for the files Caddy's own formatter produces; a
// global options block, which opens with a bare `{`, is skipped by the same
// rule since it has no name, and read only for `auto_https off`.
func parseCaddyfile(content string) (names, upstreams []string, tls bool) {
	names, upstreams = []string{}, []string{}
	depth := 0
	global, autoOff, explicit := false, false, false
	var block []string
	served, secure := 0, 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if after, ok := strings.CutPrefix(trimmed, "reverse_proxy "); ok {
			target := strings.TrimSpace(strings.TrimSuffix(after, "{"))
			if target != "" {
				upstreams = append(upstreams, target)
			}
		}
		if depth == 1 {
			if global && strings.Join(strings.Fields(trimmed), " ") == "auto_https off" {
				autoOff = true
			}
			if directive := strings.Fields(trimmed)[0]; directive == "tls" {
				explicit = true
			}
		}
		opens := strings.HasSuffix(trimmed, "{")
		if opens && depth == 0 {
			name := strings.TrimSpace(strings.TrimSuffix(trimmed, "{"))
			global, explicit, block = name == "", false, nil
			if name != "" && !strings.ContainsAny(name, "()") {
				for _, field := range strings.Split(name, ",") {
					block = append(block, strings.Fields(field)...)
				}
				names = append(names, block...)
			}
		}
		if opens {
			depth++
		}
		if trimmed == "}" && depth > 0 {
			depth--
			if depth == 0 {
				for _, address := range block {
					served++
					if caddyAddressTLS(address, explicit, autoOff) {
						secure++
					}
				}
				block = nil
			}
		}
	}
	return names, upstreams, served > 0 && secure == served
}

// caddyAddressTLS is whether Caddy serves a site address over HTTPS. Any
// address with a host is, unless its scheme is http:// or its port is 80 —
// localhost and IP addresses included, which Caddy covers from its own local
// authority (caddy:2 answers https://127.0.0.1:8443 and https://localhost).
// An address with no host (":8080") is not, having no name to hold a
// certificate for. With auto_https off a site still listens with TLS but has
// no certificate unless its block names one, so only a block with its own
// tls directive counts then.
func caddyAddressTLS(address string, explicit, autoOff bool) bool {
	if explicit {
		return true
	}
	if autoOff {
		return false
	}
	lower := strings.ToLower(address)
	if strings.HasPrefix(lower, "https://") {
		return true
	}
	if strings.HasPrefix(lower, "http://") {
		return false
	}
	hostport, _, _ := strings.Cut(address, "/")
	host, port := hostport, ""
	if i := strings.LastIndex(hostport, ":"); i >= 0 && !strings.HasSuffix(hostport, "]") {
		host, port = hostport[:i], hostport[i+1:]
	}
	return host != "" && port != "80"
}

// RefusedError is a change nginx's own test turned away. The change has been
// undone by the time it is returned, and Validation is the test that refused
// it, so the caller can say why rather than only that it did.
type RefusedError struct {
	Validation *ValidationResult
}

func (e *RefusedError) Error() string {
	return "nginx refused the change: " + FailureHeadline(e.Validation)
}

func (e *RefusedError) Unwrap() error { return ErrInvalidConf }

// FailureHeadline is the line of a failed test that says why: nginx's first
// error, with the file and line it names. "configuration failed validation"
// was all a refused reload used to report, which sent the operator to a
// terminal to find out which of forty files it meant.
func FailureHeadline(res *ValidationResult) string {
	for _, d := range res.Diagnostics {
		switch d.Level {
		case "emerg", "alert", "crit", "error", "fatal", "panic":
			if d.File != "" && d.Line > 0 {
				return fmt.Sprintf("%s in %s:%d", d.Message, d.File, d.Line)
			}
			return d.Message
		}
	}
	if line := firstLine(res.Output); line != "" {
		return line
	}
	return "the configuration test failed"
}

// linkName refuses a name that is not one entry in sites-enabled.
func linkName(name string) error {
	if strings.ContainsAny(name, "/\\\x00") || name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid vhost name %q", name)
	}
	return nil
}

// SetVHostEnabled links a site into sites-enabled or takes its link out, and
// keeps the change only if nginx still accepts the whole configuration. Only
// nginx has this notion; Caddy has no per-site enable, and saying so is better
// than pretending.
//
// Enabling used to make the link and return. A site nginx could not load was
// then in the include tree: the reload that followed was refused, the link
// stayed, and every later reload was refused with it — a deployment's cutover
// included — until somebody found the link and removed it by hand. Now the
// link goes in, `nginx -t` runs, and a refusal takes the link back out (or
// puts back the one it replaced) and returns a *RefusedError naming nginx's
// reason.
func (s *Service) SetVHostEnabled(ctx context.Context, name string, enabled bool) error {
	if err := linkName(name); err != nil {
		return err
	}
	available := filepath.Join(s.nginxDir, "sites-available", name)
	link := filepath.Join(s.nginxDir, "sites-enabled", name)
	if _, err := os.Stat(filepath.Dir(available)); err != nil {
		// Saying which of the two layouts this host uses, rather than "no
		// such vhost": on a conf.d host the site is there and it is the
		// toggle that does not exist.
		return fmt.Errorf("this host keeps its nginx sites in conf.d, where every file is active — there is no enable or disable to set. Delete the site, or rename its file so it no longer ends in .conf")
	}
	if _, err := os.Stat(available); err != nil {
		return fmt.Errorf("no such vhost: %s", name)
	}
	// Held like every other change to the tree, so a toggle cannot land in
	// the middle of another operator's validation and is recorded in order.
	s.mu.Lock()
	defer s.mu.Unlock()
	// The site file may itself be a symlink, so it is resolved the way the
	// write records resolve theirs. Reading through the link unchecked put a
	// password file's hashes, or a file outside the proxy's directories, in
	// front of whoever reads the history; now the first is skipped by
	// recordChange under its real path, and the second is recorded without
	// content, as ReadConfig refuses it.
	change := Change{Path: available, BeforeExisted: true}
	if full, err := s.allowedPath(available); err == nil {
		content, _ := os.ReadFile(full)
		change.Path, change.Before, change.After = full, content, content
	}
	if !enabled {
		change.Action = ChangeDisable
		return s.unlinkLocked(ctx, link, change)
	}
	if state, _ := readEnabledLink(link, available); state == linkServes {
		// Already on: nothing changes on disk, so nothing is recorded,
		// the same as disabling a site that is already off.
		return nil
	}
	// linkEnabled rather than a bare Symlink: a link already present but
	// pointing somewhere else — the previous file of a renamed site, a
	// dangling target — used to be reported as "enabled" and left as it
	// was, so the switch said on while nginx read nothing.
	undo, err := linkEnabled(link, available)
	if err != nil {
		return err
	}
	if res := runValidator(ctx, "nginx", "-t"); !res.Valid {
		undo()
		return &RefusedError{Validation: res}
	}
	change.Action = ChangeEnable
	s.recordChange(ctx, change)
	return nil
}

// RemoveVHostLink takes out a link in sites-enabled that is not a site's own
// switch: one pointing at nothing, which makes nginx refuse every reload, or
// one to a file kept outside sites-available. A site's own link is its
// Disable, and a real file in sites-enabled is never deleted from here — it
// may be the only copy of that configuration.
func (s *Service) RemoveVHostLink(ctx context.Context, name string) error {
	if err := linkName(name); err != nil {
		return err
	}
	link := filepath.Join(s.nginxDir, "sites-enabled", name)
	s.mu.Lock()
	defer s.mu.Unlock()
	info, err := os.Lstat(link)
	if err != nil {
		return fmt.Errorf("sites-enabled has nothing called %s", name)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("sites-enabled/%s is a file, not a link, and removing it would delete that configuration — move it to sites-available and enable it from there", name)
	}
	if state, _ := readEnabledLink(link, filepath.Join(s.nginxDir, "sites-available", name)); state == linkServes {
		return fmt.Errorf("sites-enabled/%s is how the site %s is enabled — disable the site instead", name, name)
	}
	// Recorded like a disable: the file behind the link, when there is one
	// the editor would show, is what stopped being served.
	change := Change{Path: link, Action: ChangeDisable, BeforeExisted: true}
	if s.readable(link) {
		full, _ := s.allowedPath(link)
		content, _ := os.ReadFile(full)
		change.Path, change.Before, change.After = full, content, content
	}
	return s.unlinkLocked(ctx, link, change)
}

// unlinkLocked takes link out of sites-enabled and keeps it out if nginx
// accepts what is left, putting it back exactly as it was — relative target
// and all — and returning a *RefusedError if it does not: another site may
// use an upstream or a zone this one defines. A configuration nginx was
// already refusing with the link in place is the exception, and the link
// stays out: switching sites off is how a broken configuration gets fixed,
// and refusing every disable until it is fixed some other way would leave
// the page no way to do it. Must be called with s.mu held.
func (s *Service) unlinkLocked(ctx context.Context, link string, change Change) error {
	info, err := os.Lstat(link)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("%s is a file, not a link, and disabling it would delete that configuration — move it to sites-available and enable it from there", link)
	}
	target, err := os.Readlink(link)
	if err != nil {
		return err
	}
	if err := os.Remove(link); err != nil {
		return err
	}
	if res := runValidator(ctx, "nginx", "-t"); !res.Valid {
		if err := os.Symlink(target, link); err != nil {
			return fmt.Errorf("nginx refused the configuration without %s, and putting the link back failed: %w", link, err)
		}
		if before := runValidator(ctx, "nginx", "-t"); before.Valid {
			return &RefusedError{Validation: res}
		}
		if err := os.Remove(link); err != nil {
			return err
		}
	}
	s.recordChange(ctx, change)
	return nil
}
