package proxysvc

import (
	"context"
	"errors"
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
	// TargetServedElsewhere says the file a stale link points at is read
	// through something besides that link too — another name in
	// sites-enabled, or conf.d. Enabling the site points the link at its own
	// file, and a target with no other way in stops being served.
	TargetServedElsewhere bool `json:"targetServedElsewhere,omitempty"`
	// LinkedAs are the other names in sites-enabled that link to this
	// file — 00-default -> ../sites-available/default — each of which
	// serves it as surely as a link under its own name, and each of which
	// the site's Disable takes out.
	LinkedAs []string `json:"linkedAs,omitempty"`
	// ResolvesTo is where the file really is when that is outside the
	// proxy's directories — a sites-available link into an application's
	// repository. The editor does not open such a file; the switch still
	// works, since it moves only the link in sites-enabled.
	ResolvesTo string `json:"resolvesTo,omitempty"`
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
	CertPath  string   `json:"certPath,omitempty"`
	CertPaths []string `json:"certPaths,omitempty"`
	// AccessLog and ErrorLog are the files the site's requests and errors
	// are written to: its own access_log and error_log, or the ones it
	// inherits from nginx.conf. Empty where it logs nowhere this page can
	// open — access_log off, syslog, a path built from variables.
	AccessLog string `json:"accessLog,omitempty"`
	ErrorLog  string `json:"errorLog,omitempty"`
	// Pools are the upstream blocks the file declares, so a proxy_pass to
	// one reads as the servers behind it rather than as its name.
	Pools []Pool `json:"pools,omitempty"`
	// Features are what the site's server blocks do besides naming and
	// listening, in siteFeatureOrder.
	Features []string `json:"features,omitempty"`
	// Package is the distribution package that installed this file, when
	// the file is still byte for byte what it installed: the stock default
	// site, which is not something the operator made or has to act on.
	Package string `json:"package,omitempty"`
	// Owner is the deployment that writes this site, filled in by the API
	// from the deploy store; the proxy knows only the file.
	Owner    *VHostOwner `json:"owner,omitempty"`
	Modified time.Time   `json:"modified"`
	Size     int64       `json:"size"`
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
	// Names in sites-enabled that link to a sites-available file of another
	// name. They were listed as sites of their own while the file they
	// serve read "disabled", and its Enable loaded it a second time.
	aliased := map[string]bool{}
	logs := s.inheritedLogs()
	if debian {
		links := enabledLinks(enabled)
		for _, name := range siteFiles(available) {
			full := filepath.Join(available, name)
			// The dashboard's own plumbing is listed by the feature that
			// writes it, and so is its link in sites-enabled — while that
			// link is its own. A link under its name to nothing, or to
			// another file, is not: it is listed from sites-enabled below,
			// since nginx refuses every reload over the first and serves
			// the second.
			if dashboardOwned(name, full) {
				if state, _ := readEnabledLink(filepath.Join(enabled, name), full); state == linkAbsent || state == linkServes {
					own[name] = true
				}
				continue
			}
			v := s.fileVHost(name, full, "sites-available", logs)
			v.Package = stockPackage(full)
			v.EnabledPath = filepath.Join(enabled, name)
			v.FormEditable = s.readable(full)
			v.LinkedAs = otherNames(links[resolvedFile(full)], name)
			for _, alias := range v.LinkedAs {
				aliased[alias] = true
			}
			switch state, target := readEnabledLink(v.EnabledPath, full); state {
			case linkServes:
				v.Enabled = true
			case linkDangling:
				v.Broken, v.LinkTarget = "dangling", target
			case linkElsewhere:
				v.Broken, v.LinkTarget = "stale", target
				v.TargetServedElsewhere = target != "" && servedElsewhere(links, confd, v.EnabledPath)
			}
			if len(v.LinkedAs) > 0 {
				v.Enabled = true
			}
			own[name] = true
			out = append(out, v)
		}
	}
	for _, name := range siteFiles(confd) {
		full := filepath.Join(confd, name)
		if !declaresServer(full) || dashboardOwned(name, full) {
			continue
		}
		v := s.fileVHost(name, full, "conf.d", logs)
		v.Package = stockPackage(full)
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
		// judged with it, and so was a link to a site under another name.
		// Backups are not skipped here: nginx includes sites-enabled/*, so
		// an app.bak there is read like any other file.
		if own[name] || aliased[name] || strings.HasPrefix(name, ".") {
			continue
		}
		link := filepath.Join(enabled, name)
		if info, err := os.Stat(link); err == nil && info.IsDir() {
			continue
		}
		// Under any name, a link to the dashboard's own file is its own.
		if real := resolvedFile(link); dashboardOwned(filepath.Base(real), real) {
			continue
		}
		v := s.fileVHost(name, link, "sites-enabled", logs)
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

// enabledLinks are the symlinks in sites-enabled that resolve, keyed by the
// file each one resolves to. nginx includes sites-enabled/*, which skips
// dotfiles.
func enabledLinks(dir string) map[string][]string {
	out := map[string][]string{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || e.Type()&os.ModeSymlink == 0 {
			continue
		}
		if real, err := filepath.EvalSymlinks(filepath.Join(dir, e.Name())); err == nil {
			out[real] = append(out[real], e.Name())
		}
	}
	return out
}

// servedElsewhere is whether nginx reads the file link resolves to through
// something besides link: another name in sites-enabled, or conf.d, which it
// includes as *.conf.
func servedElsewhere(links map[string][]string, confd, link string) bool {
	real := resolvedFile(link)
	if len(otherNames(links[real], filepath.Base(link))) > 0 {
		return true
	}
	entries, _ := os.ReadDir(confd)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".conf") && resolvedFile(filepath.Join(confd, name)) == real {
			return true
		}
	}
	return false
}

// otherNames is names without name.
func otherNames(names []string, name string) []string {
	var out []string
	for _, n := range names {
		if n != name {
			out = append(out, n)
		}
	}
	return out
}

// declaresServer is whether a conf.d file holds a server block of its own.
// conf.d is included inside http, and a file there that only sets a
// log_format, a map or a zone is configuration other sites depend on rather
// than a site: listed as one it read "Default host", and its Delete took out
// what the next reload needed. A file nginx could not parse is listed, so
// the operator sees it.
func declaresServer(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	directives, err := ParseNginxFile(path, string(b), []string{"http"})
	if err != nil {
		return true
	}
	for _, d := range directives {
		if d.Name == "server" && d.Block != nil {
			return true
		}
	}
	return false
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
// itself — and, for its logs, nginx.conf — says.
func (s *Service) fileVHost(name, path, layout string, logs logDefaults) VHost {
	v := VHost{
		Name: name, Kind: KindNginx, Path: path, Layout: layout,
		ServerNames: []string{}, Listen: []string{}, Upstreams: []string{},
	}
	if info, err := os.Stat(path); err == nil {
		v.Modified, v.Size = info.ModTime().UTC(), info.Size()
	} else if info, err := os.Lstat(path); err == nil {
		v.Modified = info.ModTime().UTC()
	}
	if _, err := s.allowedPath(path); errors.Is(err, ErrUnsafePath) {
		v.ResolvesTo = resolvedFile(path)
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
	s.siteDetails(&v, path, text, logs)
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
//
// Two shapes Caddy documents read as plain when they are not. A file with
// one site may leave out its braces: the first line is the address and the
// rest are its directives. And the usual way to force HTTPS by hand is an
// http:// block that only redirects, beside the site itself; that block
// serves nothing in plain text, as an nginx port-80 block that only
// redirects does not make its site plain either.
//
// An address list may go on over several lines, each but the last ending in
// a comma. Read line by line, its first line looked like the one-site form,
// and every site after it was taken for one of its directives.
func parseCaddyfile(content string) (names, upstreams []string, tls bool) {
	names, upstreams = []string{}, []string{}
	depth := 0
	global, autoOff, sawSite := false, false, false
	var site *caddySite
	addresses := ""
	served, secure := 0, 0
	finish := func() {
		if site != nil && (site.directives == 0 || site.redirects < site.directives) {
			for _, address := range site.addresses {
				served++
				if caddyAddressTLS(address, site.explicit, autoOff) {
					secure++
				}
			}
		}
		site = nil
	}
	start := func(addresses string, bare bool) {
		site = &caddySite{bare: bare}
		sawSite = true
		for _, field := range strings.Split(addresses, ",") {
			for _, address := range strings.Fields(field) {
				site.addresses = append(site.addresses, address)
				names = appendNew(names, caddyHost(address))
			}
		}
	}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if depth == 0 && (site == nil || !site.bare) {
			if addresses != "" {
				trimmed, addresses = addresses+" "+trimmed, ""
			}
			if strings.HasSuffix(trimmed, ",") {
				addresses = trimmed
				continue
			}
		}
		fields := strings.Fields(trimmed)
		if after, ok := strings.CutPrefix(trimmed, "reverse_proxy "); ok {
			target := strings.TrimSpace(strings.TrimSuffix(after, "{"))
			if target != "" {
				upstreams = append(upstreams, target)
			}
		}
		// A block opened and closed on one line, placeholders and all:
		// `http://example.com { redir https://{host}{uri} }`. Caddy wants
		// its braces as tokens of their own, which is what tells them from
		// a placeholder's.
		open := slices.Index(fields, "{")
		oneLine := open >= 0 && len(fields) > open+1 && fields[len(fields)-1] == "}"
		opens := strings.HasSuffix(trimmed, "{")
		if depth == 0 && (site == nil || !site.bare) {
			switch {
			case oneLine:
				name := strings.Join(fields[:open], " ")
				inner := fields[open+1 : len(fields)-1]
				if name == "" && strings.Join(inner, " ") == "auto_https off" {
					autoOff = true
				}
				if name != "" && !strings.ContainsAny(name, "()") {
					start(name, false)
					if len(inner) > 0 {
						site.directive(inner)
					}
					finish()
				}
			case opens:
				name := strings.TrimSpace(strings.TrimSuffix(trimmed, "{"))
				global = name == ""
				if !global && !strings.ContainsAny(name, "()") {
					start(name, false)
				}
				depth++
			case !sawSite && fields[0] != "import" && trimmed != "}":
				start(trimmed, true)
			}
			continue
		}
		closes := trimmed == "}"
		if site != nil && depth == 0 {
			site.directive(fields)
		}
		if depth == 1 && !closes {
			if global && strings.Join(fields, " ") == "auto_https off" {
				autoOff = true
			}
			if site != nil && !site.bare {
				site.directive(fields)
			}
		}
		if opens {
			depth++
		}
		if closes && depth > 0 {
			depth--
			if depth == 0 {
				global = false
				if site != nil && !site.bare {
					finish()
				}
			}
		}
	}
	finish()
	return names, upstreams, served > 0 && secure == served
}

// caddySite is one site block as parseCaddyfile reads it: its addresses,
// whether it names a certificate of its own, and how many of its directives
// only redirect to HTTPS.
type caddySite struct {
	addresses             []string
	bare                  bool
	explicit              bool
	directives, redirects int
}

func (c *caddySite) directive(fields []string) {
	c.directives++
	switch fields[0] {
	case "tls":
		c.explicit = true
	case "redir":
		// `redir [matcher] <to> [code]`: the first argument that is not a
		// matcher is where it sends the visitor.
		for _, arg := range fields[1:] {
			if arg == "*" || strings.HasPrefix(arg, "@") || strings.HasPrefix(arg, "/") {
				continue
			}
			if strings.HasPrefix(strings.ToLower(arg), "https://") {
				c.redirects++
			}
			break
		}
	}
}

// caddyHost is a site address as a name: without the scheme, which the
// listing's TLS flag already says, so an http:// redirect block and the
// site it redirects to name the domain once and "Open site" does not open
// https://http://example.com.
func caddyHost(address string) string {
	lower := strings.ToLower(address)
	for _, scheme := range []string{"https://", "http://"} {
		if strings.HasPrefix(lower, scheme) {
			return address[len(scheme):]
		}
	}
	return address
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
	// Lead is what the refusal means for this change, where nginx's first
	// error alone would mislead: taking a site out can break another site's
	// file, an enable into a configuration nginx already refuses is turned
	// away by an error in some other file, and a site that clashes with
	// another — a second default server, an upstream both define — is
	// refused in whichever of the two nginx reads second. Each time the
	// error names a file that is not the one being switched.
	Lead string
}

// Reason is the refusal in one line: the lead, when there is one, then
// nginx's first error with its file and line.
func (e *RefusedError) Reason() string {
	if e.Lead == "" {
		return FailureHeadline(e.Validation)
	}
	return e.Lead + ": " + FailureHeadline(e.Validation)
}

func (e *RefusedError) Error() string {
	if e.Lead != "" {
		return e.Reason()
	}
	return "nginx refused the change: " + e.Reason()
}

func (e *RefusedError) Unwrap() error { return ErrInvalidConf }

// FailureHeadline is the line of a failed test that says why: nginx's first
// error, with the file and line it names. "configuration failed validation"
// was all a refused reload used to report, which sent the operator to a
// terminal to find out which of forty files it meant.
func FailureHeadline(res *ValidationResult) string {
	if d := firstFailure(res); d != nil {
		if d.File != "" && d.Line > 0 {
			return fmt.Sprintf("%s in %s:%d", d.Message, d.File, d.Line)
		}
		return d.Message
	}
	if line := firstLine(res.Output); line != "" {
		return line
	}
	return "the configuration test failed"
}

// firstFailure is nginx's first error in a test, nil when it printed none
// this can read.
func firstFailure(res *ValidationResult) *Diagnostic {
	for i, d := range res.Diagnostics {
		switch d.Level {
		case "emerg", "alert", "crit", "error", "fatal", "panic":
			return &res.Diagnostics[i]
		}
	}
	return nil
}

// linkName refuses a name that is not one entry in sites-enabled.
func linkName(name string) error {
	if strings.ContainsAny(name, "/\\\x00") || name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid vhost name %q", name)
	}
	return nil
}

// LinkReload is how the reload a link change asked for went: Result is
// nginx's test and reload, and Err why nginx is not running the change —
// ErrInvalidConf when the test refused it.
type LinkReload struct {
	Result *ReloadResult
	Err    error
}

// SetVHostEnabled links a site into sites-enabled or takes its links out, and
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
	_, err := s.ToggleVHost(ctx, name, enabled, false)
	return err
}

// ToggleVHost is SetVHostEnabled followed, when reload is set, by nginx's
// reload — inside the same hold of the service lock. The reload used to run
// after the lock was let go, so its test could see a link another request
// was testing at that moment, and report a switch that had worked as "not
// reloaded" because of a site nobody had enabled.
func (s *Service) ToggleVHost(ctx context.Context, name string, enabled, reload bool) (*LinkReload, error) {
	if err := linkName(name); err != nil {
		return nil, err
	}
	available := filepath.Join(s.nginxDir, "sites-available", name)
	if _, err := os.Stat(filepath.Dir(available)); err != nil {
		// Saying which of the two layouts this host uses, rather than "no
		// such vhost": on a conf.d host the site is there and it is the
		// toggle that does not exist.
		return nil, fmt.Errorf("this host keeps its nginx sites in conf.d, where every file is active — there is no enable or disable to set. Delete the site, or rename its file so it no longer ends in .conf")
	}
	if _, err := os.Stat(available); err != nil {
		return nil, fmt.Errorf("no such vhost: %s", name)
	}
	// Held like every other change to the tree, so a toggle cannot land in
	// the middle of another operator's validation and is recorded in order.
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.setVHostEnabledLocked(ctx, name, available, enabled); err != nil {
		return nil, err
	}
	return s.reloadLocked(ctx, reload), nil
}

func (s *Service) setVHostEnabledLocked(ctx context.Context, name, available string, enabled bool) error {
	enabledDir := filepath.Join(s.nginxDir, "sites-enabled")
	link := filepath.Join(enabledDir, name)
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
	// A link under another name serves the file as surely as its own: the
	// disable has to take it out too, and an enable would load the site a
	// second time.
	aliases := otherNames(enabledLinks(enabledDir)[resolvedFile(available)], name)
	state, _ := readEnabledLink(link, available)
	if !enabled {
		change.Action = ChangeDisable
		links := []string{}
		switch state {
		case linkServes, linkDangling:
			links = append(links, link)
		case linkElsewhere:
			// A link to another file is that file's, and stays. A file
			// copied in under this name is a configuration of its own:
			// removing it to "disable" the site deleted it.
			if info, err := os.Lstat(link); err == nil && info.Mode()&os.ModeSymlink == 0 && len(aliases) == 0 {
				return fmt.Errorf("%s is a file, not a link, and disabling it would delete that configuration — move it to sites-available and enable it from there", link)
			}
		}
		for _, alias := range aliases {
			links = append(links, filepath.Join(enabledDir, alias))
		}
		return s.unlinkLocked(ctx, links, name, change)
	}
	if state == linkServes || len(aliases) > 0 {
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
		refused := &RefusedError{Validation: res}
		// nginx stops at its first error and names the file it was reading,
		// which need not be this site's. Tested again as it was, the same
		// error means nginx refused the configuration before the site came
		// in. Any other error, or none, means the site brought it, and
		// where nginx found it somewhere else — the site clashes with
		// another, and nginx reads that one second — the refusal says the
		// site is what nginx will not take.
		before := runValidator(ctx, "nginx", "-t")
		switch {
		case !before.Valid && FailureHeadline(before) == FailureHeadline(res):
			refused.Lead = "nginx already refuses the configuration without " + name
		case !failsIn(res, resolvedFile(available)):
			refused.Lead = "nginx refuses the configuration with " + name
		}
		return refused
	}
	change.Action = ChangeEnable
	s.recordChange(ctx, change)
	return nil
}

// failsIn says whether nginx's first error in res is in file, a resolved
// path. An error that names no file is in none.
func failsIn(res *ValidationResult, file string) bool {
	d := firstFailure(res)
	return d != nil && d.File == file
}

// RemoveVHostLink takes out a link in sites-enabled that is not a site's own
// switch — one pointing at nothing, which makes nginx refuse every reload, or
// one to a file kept outside sites-available — and reloads nginx inside the
// same hold of the service lock when reload is set. A link to a site in
// sites-available, under its name or another, is that site's Disable, and a
// real file in sites-enabled is never deleted from here: it may be the only
// copy of that configuration.
func (s *Service) RemoveVHostLink(ctx context.Context, name string, reload bool) (*LinkReload, error) {
	if err := linkName(name); err != nil {
		return nil, err
	}
	link := filepath.Join(s.nginxDir, "sites-enabled", name)
	s.mu.Lock()
	defer s.mu.Unlock()
	info, err := os.Lstat(link)
	if err != nil {
		return nil, fmt.Errorf("sites-enabled has nothing called %s", name)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return nil, fmt.Errorf("sites-enabled/%s is a file, not a link, and removing it would delete that configuration — move it to sites-available and enable it from there", name)
	}
	if site := s.availableSiteOf(link); site != "" {
		return nil, fmt.Errorf("sites-enabled/%s is how the site %s is enabled — disable the site instead", name, site)
	}
	// Recorded like a disable: the file behind the link, when there is one
	// the editor would show, is what stopped being served.
	change := Change{Path: link, Action: ChangeDisable, BeforeExisted: true}
	if s.readable(link) {
		full, _ := s.allowedPath(link)
		content, _ := os.ReadFile(full)
		change.Path, change.Before, change.After = full, content, content
	}
	if err := s.unlinkLocked(ctx, []string{link}, "sites-enabled/"+name, change); err != nil {
		return nil, err
	}
	return s.reloadLocked(ctx, reload), nil
}

// availableSiteOf is the site in sites-available that link serves, if any.
func (s *Service) availableSiteOf(link string) string {
	real, err := filepath.EvalSymlinks(link)
	if err != nil {
		return ""
	}
	available := filepath.Join(s.nginxDir, "sites-available")
	for _, name := range siteFiles(available) {
		if resolvedFile(filepath.Join(available, name)) == real {
			return name
		}
	}
	return ""
}

// reloadLocked reloads nginx for a link change, before the caller lets go of
// s.mu. Must be called with s.mu held; Reload itself does not take it.
func (s *Service) reloadLocked(ctx context.Context, reload bool) *LinkReload {
	if !reload {
		return nil
	}
	res, err := s.Reload(ctx, KindNginx)
	return &LinkReload{Result: res, Err: err}
}

// unlinkLocked takes links out of sites-enabled and keeps them out if nginx
// accepts what is left, putting them back exactly as they were — relative
// targets and all — and returning a *RefusedError if it does not: another
// site may use an upstream or a zone this one defines. A configuration nginx
// was already refusing with the links in place is the exception, and they
// stay out: switching sites off is how a broken configuration gets fixed,
// and refusing every disable until it is fixed some other way would leave
// the page no way to do it. Nothing to take out changes nothing and records
// nothing. what names the links in a refusal, whose first error is in the
// file that needed them rather than in them. Must be called with s.mu held.
func (s *Service) unlinkLocked(ctx context.Context, links []string, what string, change Change) error {
	type removed struct{ link, target string }
	var present []removed
	for _, link := range links {
		info, err := os.Lstat(link)
		if os.IsNotExist(err) {
			continue
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
		present = append(present, removed{link, target})
	}
	if len(present) == 0 {
		return nil
	}
	restore := func() error {
		for _, r := range present {
			if _, err := os.Lstat(r.link); err == nil {
				continue
			}
			if err := os.Symlink(r.target, r.link); err != nil {
				return fmt.Errorf("nginx refused the configuration without %s, and putting the link back failed: %w", r.link, err)
			}
		}
		return nil
	}
	for _, r := range present {
		if err := os.Remove(r.link); err != nil {
			if undo := restore(); undo != nil {
				return undo
			}
			return err
		}
	}
	if res := runValidator(ctx, "nginx", "-t"); !res.Valid {
		if err := restore(); err != nil {
			return err
		}
		if before := runValidator(ctx, "nginx", "-t"); before.Valid {
			return &RefusedError{Validation: res, Lead: "nginx refuses the configuration without " + what}
		}
		for _, r := range present {
			if err := os.Remove(r.link); err != nil {
				return err
			}
		}
	}
	s.recordChange(ctx, change)
	return nil
}

// checkSiteDelete refuses a DeleteSite that would take out configuration
// other than the site's own. The delete removes sites-enabled/<name>, file
// or link, before its backup of the site: a copy there — the file nginx
// was really serving — went with no copy kept, and a stale link took out
// the other site it served. A link under another name to the file would be
// left pointing at nothing, which stops every reload. Must be called with
// s.mu held.
func (s *Service) checkSiteDelete(name string) error {
	enabledDir := filepath.Join(s.nginxDir, "sites-enabled")
	link := filepath.Join(enabledDir, name)
	file := ""
	for _, candidate := range []string{filepath.Join(s.nginxDir, "sites-available", name), s.confdPath(name)} {
		if full, err := s.allowedPath(candidate); err == nil {
			if _, err := os.Stat(full); err == nil {
				file = candidate
				break
			}
		}
	}
	if info, err := os.Lstat(link); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("sites-enabled/%s is a file of its own, and it is what nginx serves under that name — deleting the site would remove it with no copy kept. Move it out of sites-enabled first", name)
		}
		if state, target := readEnabledLink(link, file); state == linkElsewhere {
			return fmt.Errorf("sites-enabled/%s points at %s, not at this site's file — deleting the site would take that file out of nginx. Remove or re-point the link first", name, target)
		}
	}
	if file == "" {
		return nil
	}
	if aliases := otherNames(enabledLinks(enabledDir)[resolvedFile(file)], name); len(aliases) > 0 {
		return fmt.Errorf("%s is also enabled as sites-enabled/%s, which the delete would leave pointing at nothing — disable the site first, which takes that link out too", name, strings.Join(aliases, " and sites-enabled/"))
	}
	return nil
}
