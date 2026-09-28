package proxysvc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// VHost is one virtual host. For nginx the enabled state is the presence of a
// symlink in sites-enabled, which is the convention Debian-family packages use
// and the one operators expect the toggle to drive.
type VHost struct {
	Name        string    `json:"name"`
	Kind        Kind      `json:"kind"`
	Path        string    `json:"path"`
	EnabledPath string    `json:"enabledPath,omitempty"`
	Enabled     bool      `json:"enabled"`
	ServerNames []string  `json:"serverNames"`
	Listen      []string  `json:"listen"`
	Upstreams   []string  `json:"upstreams"`
	TLS         bool      `json:"tls"`
	CertPath    string    `json:"certPath,omitempty"`
	Modified    time.Time `json:"modified"`
	Size        int64     `json:"size"`
}

var (
	serverNameRe = regexp.MustCompile(`(?m)^\s*server_name\s+([^;]+);`)
	listenRe     = regexp.MustCompile(`(?m)^\s*listen\s+([^;]+);`)
	proxyPassRe  = regexp.MustCompile(`(?m)^\s*proxy_pass\s+([^;]+);`)
	certRe       = regexp.MustCompile(`(?m)^\s*ssl_certificate\s+([^;]+);`)
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
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
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

func (s *Service) nginxVHosts() []VHost {
	available := filepath.Join(s.nginxDir, "sites-available")
	enabled := filepath.Join(s.nginxDir, "sites-enabled")
	entries, err := os.ReadDir(available)
	if err != nil {
		// Hosts without the Debian layout keep everything in conf.d. That is
		// every RPM distribution, Alpine and Arch — most of the servers this
		// runs on.
		available = filepath.Join(s.nginxDir, "conf.d")
		entries, err = os.ReadDir(available)
		if err != nil {
			return nil
		}
		enabled = ""
	}
	out := []VHost{}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || isBackupFile(e.Name()) {
			continue
		}
		full := filepath.Join(available, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		v := VHost{
			Name: e.Name(), Kind: KindNginx, Path: full,
			Modified: info.ModTime().UTC(), Size: info.Size(),
			ServerNames: []string{}, Listen: []string{}, Upstreams: []string{},
		}
		if enabled == "" {
			// conf.d is included as *.conf, so the suffix is the whole
			// difference between a file nginx reads and one it ignores.
			// There is no symlink to toggle either way, which EnabledPath
			// staying empty is what tells the UI.
			v.Enabled = strings.HasSuffix(e.Name(), ".conf")
		} else {
			link := filepath.Join(enabled, e.Name())
			if _, err := os.Lstat(link); err == nil && enabledElsewhere(link, full) == "" {
				v.Enabled = true
				v.EnabledPath = link
			} else {
				v.EnabledPath = link
			}
		}
		if b, err := os.ReadFile(full); err == nil {
			text := string(b)
			for _, m := range serverNameRe.FindAllStringSubmatch(text, -1) {
				v.ServerNames = append(v.ServerNames, strings.Fields(m[1])...)
			}
			for _, m := range listenRe.FindAllStringSubmatch(text, -1) {
				v.Listen = append(v.Listen, strings.TrimSpace(m[1]))
			}
			for _, m := range proxyPassRe.FindAllStringSubmatch(text, -1) {
				v.Upstreams = append(v.Upstreams, strings.TrimSpace(m[1]))
			}
			if m := certRe.FindStringSubmatch(text); m != nil {
				v.TLS = true
				v.CertPath = strings.TrimSpace(m[1])
			}
		}
		out = append(out, v)
	}
	return out
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
		v.ServerNames, v.Upstreams = parseCaddyfile(string(b))
	}
	return []VHost{v}, nil
}

// parseCaddyfile pulls the site addresses and the reverse_proxy targets out
// of a Caddyfile without being a Caddyfile parser.
//
// Only a block opened at the top level is a site address. Every directive
// that takes a block — handle, route, tls, header, encode, log — opens one
// too, and reading those as names put "header" and "handle" in the server
// list of every Caddyfile that used them. Brace depth is tracked line by
// line, which is enough for the files Caddy's own formatter produces; a
// global options block, which opens with a bare `{`, is skipped by the same
// rule since it has no name.
func parseCaddyfile(content string) (names, upstreams []string) {
	names, upstreams = []string{}, []string{}
	depth := 0
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
		opens := strings.HasSuffix(trimmed, "{")
		if opens && depth == 0 {
			name := strings.TrimSpace(strings.TrimSuffix(trimmed, "{"))
			if name != "" && !strings.ContainsAny(name, "()") {
				for _, field := range strings.Split(name, ",") {
					names = append(names, strings.Fields(field)...)
				}
			}
		}
		if opens {
			depth++
		}
		if trimmed == "}" && depth > 0 {
			depth--
		}
	}
	return names, upstreams
}

// SetVHostEnabled toggles the sites-enabled symlink. Only nginx has this
// notion; Caddy has no per-site enable, and saying so is better than pretending.
func (s *Service) SetVHostEnabled(ctx context.Context, name string, enabled bool) error {
	if strings.ContainsAny(name, "/\\") || name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid vhost name %q", name)
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
	if enabled {
		if target, err := os.Readlink(link); err == nil && target == available {
			// Already on: nothing changes on disk, so nothing is recorded,
			// the same as disabling a site that is already off.
			return nil
		}
		// linkEnabled rather than a bare Symlink: a link already present but
		// pointing somewhere else — the previous file of a renamed site, a
		// dangling target — used to be reported as "enabled" and left as it
		// was, so the switch said on while nginx read nothing.
		if _, err := linkEnabled(link, available); err != nil {
			return err
		}
		change.Action = ChangeEnable
		s.recordChange(ctx, change)
		return nil
	}
	if err := os.Remove(link); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	change.Action = ChangeDisable
	s.recordChange(ctx, change)
	return nil
}
