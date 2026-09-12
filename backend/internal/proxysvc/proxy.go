// Package proxysvc manages the reverse proxy in front of the host — nginx or
// Caddy — plus the TLS certificates and listening ports that go with it.
//
// The rule this package exists to enforce: a configuration is never activated
// without passing the server's own validator first. Reloading a broken nginx
// config takes every site on the box offline, and doing that from a web UI
// would be an unforced outage.
package proxysvc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

var (
	ErrNoProxy     = errors.New("neither nginx nor Caddy was found on this host")
	ErrInvalidConf = errors.New("configuration failed validation")
	ErrUnsafePath  = errors.New("path is outside the proxy configuration directory")
)

type Kind string

const (
	KindNginx Kind = "nginx"
	KindCaddy Kind = "caddy"
)

type Service struct {
	nginxDir  string
	caddyFile string

	// nginx has no way to test a config fragment in isolation, so validation
	// has to put the candidate where nginx expects it and take it away again.
	// Serialising every validate and write keeps two operators from having
	// their candidates interleaved on the same file.
	mu sync.Mutex
}

func New(nginxDir, caddyFile string) *Service {
	return &Service{nginxDir: filepath.Clean(nginxDir), caddyFile: filepath.Clean(caddyFile)}
}

type Availability struct {
	Nginx     bool   `json:"nginx"`
	Caddy     bool   `json:"caddy"`
	NginxVer  string `json:"nginxVersion,omitempty"`
	CaddyVer  string `json:"caddyVersion,omitempty"`
	NginxDir  string `json:"nginxDir"`
	CaddyFile string `json:"caddyFile"`
	Certbot   bool   `json:"certbot"`
}

func (s *Service) Availability(ctx context.Context) Availability {
	a := Availability{NginxDir: s.nginxDir, CaddyFile: s.caddyFile}
	// nginx is deliberately not installed in this image — a second copy with
	// different modules would validate against a config the running server
	// would reject. It is detected and driven on the host instead.
	if hostexec.Available("nginx") {
		a.Nginx = true
		if out, err := hostexec.Command(ctx, "nginx", "-v").CombinedOutput(); err == nil || len(out) > 0 {
			a.NginxVer = parseNginxVersion(string(out))
		}
	}
	if hostexec.Available("caddy") {
		a.Caddy = true
		if out, err := hostexec.Command(ctx, "caddy", "version").Output(); err == nil {
			a.CaddyVer = parseCaddyVersion(string(out))
		}
	}
	if hostexec.Available("certbot") {
		a.Certbot = true
	}
	return a
}

// nginxVersionLine matches what `nginx -v` writes to stderr:
// "nginx version: nginx/1.26.3 (Ubuntu)". The build name is kept because it is
// not always "nginx" — openresty and tengine identify themselves here, and an
// operator debugging a module that only one of them ships needs to see which.
var nginxVersionLine = regexp.MustCompile(`(?i)version:\s*(\S+)`)

// parseNginxVersion reduces that line to the part a caller can put beside a
// label. The whole line is what the binary emits, and printing it under a
// "Reverse proxy" heading reads "nginx nginx version: nginx/1.26.3" — the
// label, the build and the word "version" three times over.
//
// Anything unrecognised is returned trimmed rather than dropped: a version
// this does not know the shape of is still more useful on screen than nothing.
func parseNginxVersion(out string) string {
	out = strings.TrimSpace(out)
	if m := nginxVersionLine.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return firstLine(out)
}

// parseCaddyVersion takes the version from `caddy version`, whose first line is
// "v2.7.6 h1:w1dLC…" — a version followed by a module hash that means nothing
// to a reader and pushes the version itself off a narrow tile.
func parseCaddyVersion(out string) string {
	line := firstLine(out)
	if field, _, found := strings.Cut(line, " "); found {
		return field
	}
	return line
}

func firstLine(s string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0])
}

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
			if _, err := os.Lstat(link); err == nil {
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
		for _, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if strings.HasSuffix(trimmed, "{") && !strings.HasPrefix(trimmed, "reverse_proxy") {
				name := strings.TrimSpace(strings.TrimSuffix(trimmed, "{"))
				if name != "" && !strings.ContainsAny(name, "()") {
					v.ServerNames = append(v.ServerNames, strings.Fields(name)...)
				}
			}
			if after, ok := strings.CutPrefix(trimmed, "reverse_proxy "); ok {
				v.Upstreams = append(v.Upstreams, strings.TrimSpace(strings.TrimSuffix(after, "{")))
			}
		}
	}
	return []VHost{v}, nil
}

// allowedPath keeps the config editor pointed at the proxy's own directories.
// Without it this endpoint would be an arbitrary-file-write primitive dressed
// up as a config editor.
func (s *Service) allowedPath(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	resolved := abs
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		resolved = r
	}
	roots := []string{s.nginxDir, filepath.Dir(s.caddyFile)}
	for _, root := range roots {
		if resolved == root || strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrUnsafePath, path)
}

// confdPath is where a site lives on a host with no sites-available. nginx
// includes conf.d/*.conf, so the suffix is the difference between a file it
// reads and one it ignores — and a name that already carries it, which is
// exactly what the listing reports on such a host, must not gain a second one
// and become app.conf.conf.
func (s *Service) confdPath(name string) string {
	if strings.HasSuffix(name, ".conf") {
		return filepath.Join(s.nginxDir, "conf.d", name)
	}
	return filepath.Join(s.nginxDir, "conf.d", name+".conf")
}

// authDir and streamDir hang off the configured nginx directory rather than
// off /etc/nginx, because JD_NGINX_DIR exists precisely for the hosts whose
// nginx is somewhere else — and a password file written where that nginx never
// looks is a site that refuses every visitor.
func (s *Service) authDir() string   { return filepath.Join(s.nginxDir, "jd-auth") }
func (s *Service) streamDir() string { return filepath.Join(s.nginxDir, "stream.d") }

func (s *Service) ReadConfig(path string) (string, error) {
	full, err := s.allowedPath(path)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

type ValidationResult struct {
	Valid   bool   `json:"valid"`
	Output  string `json:"output"`
	Command string `json:"command"`
}

// Validate runs the server's own config test and leaves the host exactly as it
// found it. Caddy can be pointed at a temporary file; nginx cannot, so the
// candidate is written where nginx expects it, tested, and the original put
// back **whatever the outcome**. An earlier version restored only on failure,
// which turned a "dry run" into a permanent write for every file nginx does not
// currently include — `nginx -t` says nothing about a config it never reads, so
// the result was Valid and the rollback never ran.
func (s *Service) Validate(ctx context.Context, kind Kind, path, content string) (*ValidationResult, error) {
	if kind == KindCaddy {
		return s.validateCaddy(ctx, content)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.validateNginx(ctx, path, content)
}

// validateNginx must be called with s.mu held.
func (s *Service) validateNginx(ctx context.Context, path, content string) (*ValidationResult, error) {
	full, err := s.allowedPath(path)
	if err != nil {
		return nil, err
	}
	restore, err := s.stageNginx(full, content)
	if err != nil {
		return nil, err
	}
	res := runValidator(ctx, "nginx", "-t")
	// Unconditional: the caller asked whether this content would be accepted,
	// not for it to be installed.
	restore()
	return res, nil
}

// stageNginx puts content at full and returns the undo. The transient window is
// unavoidable for nginx and is why validation requires system.admin — the same
// capability as writing the file outright.
func (s *Service) stageNginx(full, content string) (func(), error) {
	original, readErr := os.ReadFile(full)
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, readErr
	}
	var mode os.FileMode = 0o644
	if st, err := os.Stat(full); err == nil {
		mode = st.Mode().Perm()
	}
	if err := writeAtomic(full, content); err != nil {
		return nil, err
	}
	return func() {
		// Leaving a broken or absent file behind means the next unrelated
		// reload takes the sites down, so the undo ignores nothing.
		if readErr == nil {
			os.WriteFile(full, original, mode)
		} else {
			os.Remove(full)
		}
	}, nil
}

func (s *Service) validateCaddy(ctx context.Context, content string) (*ValidationResult, error) {
	tmp, err := os.CreateTemp("", "vpsd-caddy-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return nil, err
	}
	tmp.Close()
	return runValidator(ctx, "caddy", "validate", "--config", tmp.Name(), "--adapter", "caddyfile"), nil
}

func runValidator(ctx context.Context, name string, args ...string) *ValidationResult {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := hostexec.Command(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return &ValidationResult{
		Valid:   err == nil,
		Output:  strings.TrimSpace(buf.String()),
		Command: name + " " + strings.Join(args, " "),
	}
}

// WriteConfig saves a configuration only after it validates. The order here is
// the whole point of the endpoint: validate, then write, then reload.
func (s *Service) WriteConfig(ctx context.Context, kind Kind, path, content string) (*ValidationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	full, err := s.allowedPath(path)
	if err != nil {
		return nil, err
	}
	if kind == KindCaddy {
		res, err := s.validateCaddy(ctx, content)
		if err != nil {
			return nil, err
		}
		if !res.Valid {
			return res, ErrInvalidConf
		}
		if err := writeAtomic(full, content); err != nil {
			return nil, err
		}
		return res, nil
	}
	// Validation now restores the original unconditionally, so the write has
	// to be made explicitly afterwards. Testing again with the content in
	// place is not redundant: the first test only proves nginx tolerates the
	// candidate at the moment it ran, and the file may not be in nginx's
	// include tree at all.
	res, err := s.validateNginx(ctx, path, content)
	if err != nil {
		return nil, err
	}
	if !res.Valid {
		return res, ErrInvalidConf
	}
	restore, err := s.stageNginx(full, content)
	if err != nil {
		return nil, err
	}
	if after := runValidator(ctx, "nginx", "-t"); !after.Valid {
		restore()
		return after, ErrInvalidConf
	}
	return res, nil
}

func writeAtomic(path, content string) error {
	dir := filepath.Dir(path)
	var mode os.FileMode = 0o644
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".vpsd-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

type ReloadResult struct {
	Validation *ValidationResult `json:"validation"`
	Reloaded   bool              `json:"reloaded"`
	Output     string            `json:"output"`
}

// Reload tests first and refuses to reload a config that does not pass. This
// is the guard rail that makes a config editor safe to expose at all.
func (s *Service) Reload(ctx context.Context, kind Kind) (*ReloadResult, error) {
	var validation *ValidationResult
	var reload *exec.Cmd
	switch kind {
	case KindCaddy:
		validation = runValidator(ctx, "caddy", "validate", "--config", s.caddyFile, "--adapter", "caddyfile")
		reload = hostexec.Command(ctx, "caddy", "reload", "--config", s.caddyFile)
	default:
		validation = runValidator(ctx, "nginx", "-t")
		reload = hostexec.Command(ctx, "nginx", "-s", "reload")
	}
	res := &ReloadResult{Validation: validation}
	if !validation.Valid {
		return res, ErrInvalidConf
	}
	out, err := reload.CombinedOutput()
	res.Output = strings.TrimSpace(string(out))
	res.Reloaded = err == nil
	if err != nil {
		return res, fmt.Errorf("reload failed: %s", res.Output)
	}
	return res, nil
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
	if enabled {
		if _, err := os.Lstat(link); err == nil {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return err
		}
		return os.Symlink(available, link)
	}
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
