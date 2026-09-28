package proxysvc

import (
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// OwnedMarker is what the first line of a file the dashboard keeps for its
// own plumbing carries — the catch-all default site, a shared log format —
// under a jd- name in sites-available or conf.d. Such a file is not a site
// the operator made: the feature that writes it shows it, and the Sites list
// leaves it out.
const OwnedMarker = "Just Dashboard owned"

// dashboardOwned is whether the file at path, called name, is one of the
// dashboard's own: a jd- name and the marker on its first line. Both are
// needed, so an operator's jd-app.conf is still listed, and so is a file that
// only mentions the phrase further down.
func dashboardOwned(name, path string) bool {
	if !strings.HasPrefix(name, "jd-") {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	line, _ := bufio.NewReader(io.LimitReader(f, 4096)).ReadString('\n')
	return strings.Contains(line, OwnedMarker)
}

// Pool is an upstream block a site file declares and the servers in it.
type Pool struct {
	Name    string   `json:"name"`
	Servers []string `json:"servers"`
}

// VHostOwner is the deployment environment that writes a site. The API fills
// it in from the deploy store; the proxy only knows the file.
type VHostOwner struct {
	ProjectID     int64  `json:"projectId"`
	EnvironmentID int64  `json:"environmentId"`
	Project       string `json:"project"`
	Environment   string `json:"environment"`
	// Archived is a route whose environment or project was archived and
	// left behind: nothing deploys it any more, so nothing overwrites an
	// edit and nothing breaks when it goes.
	Archived bool `json:"archived,omitempty"`
}

// siteFeatureOrder is every feature the listing reports, in the order it
// reports them.
//
//   - auth: auth_basic, anywhere in the site.
//   - sso: auth_request, which asks another server before each request.
//   - allow: an allow list anywhere, or a deny at the server's own level. A
//     location that only denies — the site form's fence around dotfiles and
//     backups — restricts nobody, so it does not count.
//   - ratelimit: limit_req or limit_conn.
//   - cache: proxy_cache.
//   - ws: an Upgrade header passed to the upstream.
//   - h2 and h3: http2 or http3 on, or a listener with http2 or quic.
//   - maintenance: an `if ($jd_<site>_maint)` in a server block, which the
//     site form writes only while the site is in maintenance.
var siteFeatureOrder = []string{"auth", "sso", "allow", "ratelimit", "cache", "ws", "h2", "h3", "maintenance"}

var maintenanceTestRe = regexp.MustCompile(`^\(\$jd_[A-Za-z0-9_]+_maint\)$`)

// logDefaults are the logs a server block writes to when it names none of
// its own: the http block's access_log, and the http block's error_log or
// else the main context's. "" is none this page can open — unset, off, or
// not a file.
type logDefaults struct {
	access, errorLog string
}

// inheritedLogs reads the log files nginx.conf sets for every site. Only the
// file itself: a log set in something it includes is not followed, and a
// site that relies on one reads as having no log rather than the wrong one.
// Unset is left empty too, although nginx then writes to the path it was
// built with, because this page does not ask the binary.
func (s *Service) inheritedLogs() logDefaults {
	path := filepath.Join(s.nginxDir, "nginx.conf")
	b, err := os.ReadFile(path)
	if err != nil {
		return logDefaults{}
	}
	directives, err := ParseNginxFile(path, string(b), nil)
	if err != nil {
		return logDefaults{}
	}
	out := logDefaults{}
	for _, d := range directives {
		if d.Name == "error_log" && len(d.Args) > 0 && out.errorLog == "" {
			out.errorLog = logFile(d.Args[0])
		}
		if d.Name != "http" {
			continue
		}
		accessSet, errorSet := false, false
		for _, h := range d.Block {
			switch {
			case h.Name == "access_log" && len(h.Args) > 0 && !accessSet:
				accessSet, out.access = true, logFile(h.Args[0])
			case h.Name == "error_log" && len(h.Args) > 0 && !errorSet:
				errorSet, out.errorLog = true, logFile(h.Args[0])
			}
		}
	}
	return out
}

// logFile is a log directive's target as a file the log viewer can open, or
// "" for off, syslog:, stderr, memory:, a device, a path relative to nginx's
// prefix, and a path built from variables.
func logFile(target string) string {
	if !filepath.IsAbs(target) || strings.Contains(target, "$") || strings.HasPrefix(target, "/dev/") {
		return ""
	}
	return filepath.Clean(target)
}

// siteWalk gathers what a site file's server blocks do. path is the site's
// own file: an include written there is followed, one in a file it includes
// is not. self is path with its links resolved, the name allowedPath gives
// the file when an include reaches it.
// redirectURLRe is the one-argument return nginx treats as a redirect: a
// URL with a scheme, or one built from $scheme.
var redirectURLRe = regexp.MustCompile(`^["']?(https?://|\$scheme)`)

type siteWalk struct {
	s          *Service
	path, self string
	features   map[string]bool
	roots      []string
	redirects  []string
}

// siteDetails fills in the site's logs, pools and features from its file,
// read the way nginx reads it. A file nginx could not parse gets none: the
// listing still shows it, and the test says what is wrong with it.
func (s *Service) siteDetails(v *VHost, path, text string, inherited logDefaults) {
	directives, err := ParseNginxFile(path, text, []string{"http"})
	if err != nil {
		return
	}
	walk := siteWalk{s: s, path: path, self: resolvedFile(path), features: map[string]bool{}}
	// A site that forces HTTPS is a redirect block that names no log of its
	// own beside the block that serves, which does: the log the site writes
	// is the serving block's, not nginx.conf's. So a log a block names comes
	// first, and the inherited one counts only where a block names none.
	access, errorLog := siteLog{}, siteLog{}
	for _, d := range directives {
		switch {
		case d.Name == "upstream" && d.Block != nil && len(d.Args) > 0:
			pool := Pool{Name: d.Args[0], Servers: []string{}}
			for _, member := range d.Block {
				if member.Name == "server" && len(member.Args) > 0 {
					pool.Servers = append(pool.Servers, member.Args[0])
				}
			}
			v.Pools = append(v.Pools, pool)
		case d.Name == "server" && d.Block != nil:
			access.add(walk.server(d.Block, "access_log"))
			errorLog.add(walk.server(d.Block, "error_log"))
			walk.block(walk.expand(d.Block), true)
		}
	}
	v.AccessLog = access.file(inherited.access)
	v.ErrorLog = errorLog.file(inherited.errorLog)
	v.Roots, v.Redirects = walk.roots, walk.redirects
	for _, feature := range siteFeatureOrder {
		if walk.features[feature] {
			v.Features = append(v.Features, feature)
		}
	}
}

// siteLog is one kind of log across a site's server blocks: the first file a
// block names, and whether any block names none and so inherits.
type siteLog struct {
	own      string
	inherits bool
}

func (l *siteLog) add(target string, named bool) {
	switch {
	case !named:
		l.inherits = true
	case l.own == "":
		l.own = logFile(target)
	}
}

// file is the log the site writes: one a block names, or else the inherited
// one if some block inherits it.
func (l siteLog) file(inherited string) string {
	if l.own != "" || !l.inherits {
		return l.own
	}
	return inherited
}

// server is the first argument of the first directive called name at the
// server block's own level, an include there counted as part of it.
func (w *siteWalk) server(block []Directive, name string) (string, bool) {
	for _, d := range w.expand(block) {
		if d.Name == name && len(d.Args) > 0 {
			return d.Args[0], true
		}
	}
	return "", false
}

// expand replaces each include the site's own file writes in block with what
// it includes: one level deep, and only files the editor would open, as the
// certificate reading does.
//
// The site's own file is never included into itself. Its directives would
// carry its own path, so they would be expanded again inside the block they
// were expanded into, without end: a site that includes itself — directly,
// or through sites-available/* or sites-enabled/* inside a server block —
// overflowed the stack and took the whole dashboard down on every listing,
// although nginx never reads such a file while it is disabled.
func (w *siteWalk) expand(block []Directive) []Directive {
	out := make([]Directive, 0, len(block))
	for _, d := range block {
		if d.Name != "include" || len(d.Args) == 0 || d.File != w.path {
			out = append(out, d)
			continue
		}
		pattern := d.Args[0]
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(w.s.nginxDir, pattern)
		}
		matches, _ := filepath.Glob(pattern)
		for _, match := range matches {
			full, err := w.s.allowedPath(match)
			if err != nil || full == w.path || full == w.self || w.s.isPasswordFile(full) {
				continue
			}
			b, err := os.ReadFile(full)
			if err != nil {
				continue
			}
			if included, err := ParseNginxFile(full, string(b), d.Context); err == nil {
				out = append(out, included...)
			}
		}
	}
	return out
}

// block records the features directives turn on. serverLevel is the server
// block's own level, where a deny restricts the whole site.
func (w *siteWalk) block(block []Directive, serverLevel bool) {
	on := func(feature string) { w.features[feature] = true }
	for _, d := range block {
		first := ""
		if len(d.Args) > 0 {
			first = d.Args[0]
		}
		switch d.Name {
		case "auth_basic":
			if first != "off" {
				on("auth")
			}
		case "auth_request":
			if first != "off" {
				on("sso")
			}
		case "allow":
			on("allow")
		case "deny":
			if serverLevel {
				on("allow")
			}
		case "limit_req", "limit_conn":
			on("ratelimit")
		case "proxy_cache":
			if first != "off" {
				on("cache")
			}
		case "proxy_set_header":
			if strings.EqualFold(first, "Upgrade") {
				on("ws")
			}
		case "http2":
			if first == "on" {
				on("h2")
			}
		case "http3":
			if first == "on" {
				on("h3")
			}
		case "listen":
			if len(d.Args) > 1 && slices.Contains(d.Args[1:], "http2") {
				on("h2")
			}
			if len(d.Args) > 1 && slices.Contains(d.Args[1:], "quic") {
				on("h3")
			}
		case "root":
			w.roots = appendNew(w.roots, unquote(first))
		case "return":
			// "return 301 URL", or "return URL", which nginx sends as a 302.
			if len(d.Args) == 2 && strings.HasPrefix(first, "30") {
				w.redirects = appendNew(w.redirects, unquote(d.Args[1]))
			} else if len(d.Args) == 1 && redirectURLRe.MatchString(first) {
				w.redirects = appendNew(w.redirects, unquote(first))
			}
		case "if":
			if maintenanceTestRe.MatchString(strings.Join(d.Args, "")) {
				on("maintenance")
			}
		}
		if d.Block != nil {
			w.block(w.expand(d.Block), false)
		}
	}
}

// dpkgStatusFiles are where dpkg records what each package installed: this
// host's, and the managed host's as the dashboard's container sees it.
var dpkgStatusFiles = []string{"/var/lib/dpkg/status", "/host/var/lib/dpkg/status"}

// conffile is a configuration file a package installed and the digest dpkg
// recorded for it.
type conffile struct {
	pkg, md5 string
}

// packageConffiles is every conffile dpkg knows, read again only when a
// status file changes: it runs to megabytes and the listing is polled.
var packageConffiles = struct {
	sync.Mutex
	stamp  string
	byPath map[string]conffile
}{}

func conffiles() map[string]conffile {
	stamp := ""
	for _, path := range dpkgStatusFiles {
		if info, err := os.Stat(path); err == nil {
			stamp += fmt.Sprintf("%s:%d:%d;", path, info.Size(), info.ModTime().UnixNano())
		}
	}
	packageConffiles.Lock()
	defer packageConffiles.Unlock()
	if packageConffiles.byPath != nil && packageConffiles.stamp == stamp {
		return packageConffiles.byPath
	}
	byPath := map[string]conffile{}
	for _, path := range dpkgStatusFiles {
		if f, err := os.Open(path); err == nil {
			readConffiles(f, byPath)
			f.Close()
		}
	}
	packageConffiles.stamp, packageConffiles.byPath = stamp, byPath
	return byPath
}

// readConffiles adds the Conffiles of each package stanza in a dpkg status
// file to into. A path already there keeps its first package.
func readConffiles(r io.Reader, into map[string]conffile) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	pkg, listing := "", false
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			pkg, listing = "", false
		case strings.HasPrefix(line, " "):
			if !listing || pkg == "" {
				continue
			}
			// " /etc/nginx/sites-available/default <md5> [obsolete]": an
			// obsolete one is no longer the package's, and "newconffile"
			// stands where dpkg has no digest yet.
			fields := strings.Fields(line)
			if len(fields) < 2 || len(fields) > 2 && fields[2] == "obsolete" || len(fields[1]) != 32 {
				continue
			}
			if _, seen := into[fields[0]]; !seen {
				into[fields[0]] = conffile{pkg: pkg, md5: fields[1]}
			}
		default:
			listing = line == "Conffiles:"
			if name, ok := strings.CutPrefix(line, "Package: "); ok {
				pkg = strings.TrimSpace(name)
			}
		}
	}
}

// stockPackage is the package that installed path, when the file is still
// byte for byte what the package installed; "" otherwise, and on a host
// without dpkg.
func stockPackage(path string) string {
	c, ok := conffiles()[path]
	if !ok {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := md5.Sum(b)
	if hex.EncodeToString(sum[:]) != c.md5 {
		return ""
	}
	return c.pkg
}
