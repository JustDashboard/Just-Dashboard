// Package proxysvc manages the reverse proxy in front of the host — nginx or
// Caddy — plus the TLS certificates and listening ports that go with it.
//
// The rule this package exists to enforce: a configuration is never activated
// without passing the server's own validator first. Reloading a broken nginx
// config takes every site on the box offline, and doing that from a web UI
// would be an unforced outage.
package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

var (
	ErrNoProxy     = errors.New("neither nginx nor Caddy was found on this host")
	ErrInvalidConf = errors.New("configuration failed validation")
	ErrUnsafePath  = errors.New("path is outside the proxy configuration directory")
	// ErrProtectedFile is a file inside the proxy directory that the config
	// editor must not show: a password file. Its hashes are offline-crackable,
	// and the editor's read route is open to every signed-in account.
	ErrProtectedFile = errors.New("this file holds password hashes and is not shown by the config editor")
)

type Kind string

const (
	KindNginx Kind = "nginx"
	KindCaddy Kind = "caddy"
	// KindCaddyIngress is the Docker Caddy that deployments share. It is
	// tested and reloaded inside its container, against the Caddyfile the
	// container serves; the host may have no caddy binary at all.
	KindCaddyIngress Kind = "caddy-ingress"
)

// Known reports whether k is an engine the config test and reload can drive.
func (k Kind) Known() bool {
	return k == KindNginx || k == KindCaddy || k == KindCaddyIngress
}

type Service struct {
	dockerIngress bool
	nginxDir      string
	caddyFile     string

	// nginx has no way to test a config fragment in isolation, so validation
	// has to put the candidate where nginx expects it and take it away again.
	// Serialising every validate and write keeps two operators from having
	// their candidates interleaved on the same file.
	mu sync.Mutex

	recorder  ChangeRecorder
	effective effectiveCache
	pending   pendingTracker
	tested    testMemory
}

func New(nginxDir, caddyFile string) *Service {
	return &Service{nginxDir: filepath.Clean(nginxDir), caddyFile: filepath.Clean(caddyFile)}
}

// NewWithDockerIngress enables discovery of the host's existing public Caddy.
// Isolated services and tests use New without inspecting unrelated containers.
func NewWithDockerIngress(nginxDir, caddyFile string) *Service {
	s := New(nginxDir, caddyFile)
	s.dockerIngress = true
	return s
}

func (s *Service) dockerCaddy(ctx context.Context) (*dockerCaddy, error) {
	if !s.dockerIngress {
		return nil, nil
	}
	return discoverDockerCaddy(ctx)
}

// IngressState says whether the shared Docker Caddy exists yet.
const (
	// IngressRunning is a Caddy container found publishing 80 and 443.
	IngressRunning = "running"
	// IngressProvisionable is none running, with Docker here and 80 and 443
	// free: the first deployment that routes a domain starts one.
	IngressProvisionable = "provisionable"
)

type Availability struct {
	// IngressContainer is the running Docker Caddy's name; never set for one
	// that would only be started later.
	IngressContainer string `json:"ingressContainer,omitempty"`
	// IngressID is that container's ID, which the page's Restart container
	// and Container logs act on through the Docker routes and their own
	// gates; a name can be reused by another container between two reads.
	IngressID string `json:"ingressId,omitempty"`
	// IngressStartedAt is when that container last started (RFC 3339).
	IngressStartedAt string `json:"ingressStartedAt,omitempty"`
	IngressState     string `json:"ingressState,omitempty"`
	Nginx            bool   `json:"nginx"`
	Caddy            bool   `json:"caddy"`
	NginxVer         string `json:"nginxVersion,omitempty"`
	CaddyVer         string `json:"caddyVersion,omitempty"`
	NginxDir         string `json:"nginxDir"`
	CaddyFile        string `json:"caddyFile"`
	Certbot          bool   `json:"certbot"`
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
	edge, err := s.dockerCaddy(ctx)
	a.setIngress(edge, err == nil && edge == nil && s.canProvisionIngress(ctx))
	return a
}

// setIngress records the Docker Caddy: the one found running, or that the
// first deployment would start one. The second is neither Caddy nor a
// container — nothing serves yet — and naming the container it would be put
// Test and Reload buttons on the overview for a Caddy that did not exist.
func (a *Availability) setIngress(edge *dockerCaddy, provisionable bool) {
	switch {
	case edge != nil:
		a.Caddy = true
		a.IngressContainer = edge.Name
		a.IngressID = edge.ID
		a.IngressStartedAt = edge.StartedAt
		a.IngressState = IngressRunning
	case provisionable:
		a.IngressState = IngressProvisionable
	}
}

// ErrNoEngineUnit is a proxy the dashboard cannot start or stop as a service:
// the Docker Caddy ingress, whose lifecycle is its container's, or none.
var ErrNoEngineUnit = errors.New("this host's proxy is not run by a systemd unit")

// EngineUnit is the service behind the engine the overview shows: its unit,
// the kind its config test takes, and its name for a sentence.
type EngineUnit struct {
	Unit string
	Kind Kind
	Name string
}

// Engine resolves the unit on the server — nginx where it is installed, else a
// host Caddy — so a request can only ever start or stop the proxy, never a
// unit named by the caller.
func (s *Service) Engine() (EngineUnit, error) {
	if hostexec.Available("nginx") {
		return EngineUnit{Unit: "nginx.service", Kind: KindNginx, Name: "nginx"}, nil
	}
	if hostexec.Available("caddy") {
		return EngineUnit{Unit: "caddy.service", Kind: KindCaddy, Name: "Caddy"}, nil
	}
	return EngineUnit{}, ErrNoEngineUnit
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

// isPasswordFile recognises an htpasswd file by where it is or what it is
// called. The dashboard's own live under authDir; the ones written by hand
// are `.htpasswd` by near-universal convention. The config editor's read
// route is held by every signed-in account, and a list of bcrypt hashes is
// not configuration.
func (s *Service) isPasswordFile(full string) bool {
	if strings.HasPrefix(full, s.authDir()+string(os.PathSeparator)) {
		return true
	}
	base := strings.ToLower(filepath.Base(full))
	return strings.HasPrefix(base, ".ht") || strings.Contains(base, "htpasswd")
}

// nginxIncluded reports whether nginx reads this file at all, and whether
// that can be known. `nginx -t` says nothing about a file outside the include
// tree, so a candidate for a disabled site tests "valid" whatever it says; the
// answer here is what turns that silence into a sentence.
func (s *Service) nginxIncluded(full string) (included, known bool) {
	dir, name := filepath.Split(full)
	switch filepath.Clean(dir) {
	case filepath.Join(s.nginxDir, "sites-available"):
		_, err := os.Lstat(filepath.Join(s.nginxDir, "sites-enabled", name))
		return err == nil, true
	case filepath.Join(s.nginxDir, "sites-enabled"):
		return true, true
	case filepath.Join(s.nginxDir, "conf.d"):
		return strings.HasSuffix(name, ".conf"), true
	case s.streamDir():
		return strings.HasSuffix(name, ".conf") && streamDirRead(s.nginxDir, s.streamDir()), true
	}
	return false, false
}

// includeNote is the sentence a validation result carries when the test
// could not have seen the file.
func (s *Service) includeNote(full string) string {
	included, known := s.nginxIncluded(full)
	if !known || included {
		return ""
	}
	return "nginx does not include this file at the moment, so the test could not see it: enable the site (or, for a stream, include the stream directory in nginx.conf) before trusting this result."
}

func (s *Service) ReadConfig(path string) (string, error) {
	full, err := s.allowedPath(path)
	if err != nil {
		return "", err
	}
	if s.isPasswordFile(full) {
		return "", fmt.Errorf("%w: %s", ErrProtectedFile, path)
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
	// Note qualifies the verdict: an nginx file outside the include tree
	// passes `nginx -t` without being read, and saying so is the difference
	// between a dry run and a false reassurance.
	Note string `json:"note,omitempty"`
	// Diagnostics are the leveled lines of Output, placed where they name a
	// file and line. Warnings counts the warn-level ones, because nginx passes
	// a config it is quietly ignoring part of.
	Diagnostics []Diagnostic `json:"diagnostics"`
	Warnings    int          `json:"warnings"`
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
		// The path only names the file in the result, but it is still held
		// to the proxy's directories: a name the editor could never save to
		// is not one to resolve on a caller's say-so.
		target := s.caddyFile
		if path != "" {
			full, err := s.allowedPath(path)
			if err != nil {
				return nil, err
			}
			target = full
		}
		return s.validateCaddy(ctx, target, content)
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
	res, err := runTest(ctx, "nginx", "-t")
	// Unconditional: the caller asked whether this content would be accepted,
	// not for it to be installed.
	restore()
	if err != nil {
		return nil, err
	}
	res.Note = s.includeNote(full)
	return res, nil
}

// stageNginx puts content at full and returns the undo. The transient window is
// unavoidable for nginx and is why validation requires system.admin — the same
// capability as writing the file outright.
func (s *Service) stageNginx(full, content string) (func(), error) {
	s.keepLoaded(full)
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

// caddyScratchRoot is where a Caddyfile candidate is copied for `caddy
// validate`. caddy is not in the dashboard's image and runs on the host
// through nsenter, where the container's /tmp is a different directory: a
// copy there was a file the host's caddy could not open, so every check of an
// edit failed. docker-compose.yml mounts this one directory at the same path
// on both sides. A variable so tests can use their own.
var caddyScratchRoot = "/tmp/just-dashboard"

// caddyScratch makes a private directory for one candidate and returns it
// with its removal. The root is shared with every account on the host, so it
// must be a real directory the dashboard owns and nobody else can write to,
// the check the terminal's clipboard makes of the same root: a directory
// another account planted could read the candidate — a Caddyfile can hold
// credentials — or swap it between the write and the test.
func caddyScratch() (string, func(), error) {
	if err := os.Mkdir(caddyScratchRoot, 0o711); err != nil && !errors.Is(err, os.ErrExist) {
		return "", nil, err
	}
	info, err := os.Lstat(caddyScratchRoot)
	if err != nil {
		return "", nil, err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || owner.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0o022 != 0 {
		return "", nil, fmt.Errorf("%s is not a directory only the dashboard can write to, so a Caddyfile cannot be checked there", caddyScratchRoot)
	}
	dir, err := os.MkdirTemp(caddyScratchRoot, "caddy-validate-")
	if err != nil {
		return "", nil, err
	}
	return dir, func() { os.RemoveAll(dir) }, nil
}

// validateCaddy tests content as the file at target. Caddy can be pointed at
// a copy, so nothing is staged, but it names the copy in every message it
// writes: a file the caller never saw, and one that is gone by the time the
// result is read. The copy's name is replaced by target's throughout.
func (s *Service) validateCaddy(ctx context.Context, target, content string) (*ValidationResult, error) {
	dir, remove, err := caddyScratch()
	if err != nil {
		return nil, err
	}
	defer remove()
	copied := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(copied, []byte(content), 0o600); err != nil {
		return nil, err
	}
	res, err := runTest(ctx, "caddy", "validate", "--config", copied, "--adapter", "caddyfile")
	if err != nil {
		return nil, err
	}
	staged, target := resolvedFile(copied), resolvedFile(target)
	res.Output = strings.ReplaceAll(res.Output, copied, target)
	for i, d := range res.Diagnostics {
		if d.File == staged {
			res.Diagnostics[i].File = target
		}
		res.Diagnostics[i].Message = strings.ReplaceAll(d.Message, copied, target)
	}
	return res, nil
}

// runValidator is runTest for a caller that only acts on a pass: a test that
// did not finish reads as a failure, which refuses whatever it guards.
func runValidator(ctx context.Context, name string, args ...string) *ValidationResult {
	res, _ := runTest(ctx, name, args...)
	return res
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
		res, err := s.validateCaddy(ctx, full, content)
		if err != nil {
			return nil, err
		}
		if !res.Valid {
			return res, ErrInvalidConf
		}
		original, existed := readIfPresent(full)
		if err := writeAtomic(full, content); err != nil {
			return nil, err
		}
		s.recordChange(ctx, Change{Path: full, Action: ChangeWrite,
			Before: []byte(original), BeforeExisted: existed, After: []byte(content)})
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
	original, existed := readIfPresent(full)
	restore, err := s.stageNginx(full, content)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	after, err := runTest(ctx, "nginx", "-t")
	if err != nil {
		restore()
		return nil, err
	}
	if !after.Valid {
		restore()
		return after, ErrInvalidConf
	}
	// The file stays, so this is a test of the configuration as it now is.
	s.remember(KindNginx, started, after)
	s.recordChange(ctx, Change{Path: full, Action: ChangeWrite,
		Before: []byte(original), BeforeExisted: existed, After: []byte(content)})
	return res, nil
}

func writeAtomic(path, content string) error {
	var mode os.FileMode = 0o644
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	return writeAtomicMode(path, content, mode)
}

func writeAtomicMode(path, content string, mode os.FileMode) error {
	dir := filepath.Dir(path)
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

// Test runs the server's own config test against what is on disk right now.
// It stages nothing and reloads nothing: it is the answer to "would a reload
// succeed", asked before pressing the button that finds out the hard way. The
// ingress is tested inside its container, and there is nothing to test until
// one runs.
//
// Every test that gives a verdict is kept as the engine's last (see
// TestRecord); one that does not is ErrTestUnfinished and kept nowhere.
func (s *Service) Test(ctx context.Context, kind Kind) (*ValidationResult, error) {
	started := time.Now()
	// Once begun, a test runs to its verdict: a tab closed or refreshed while
	// it runs would otherwise stop it, and a stopped test says nothing.
	run := context.WithoutCancel(ctx)
	var res *ValidationResult
	var err error
	switch kind {
	case KindCaddyIngress:
		var edge *dockerCaddy
		if edge, err = s.ingress(ctx); err != nil {
			return nil, err
		}
		res, err = edge.validate(run)
	case KindCaddy:
		res, err = runTest(run, "caddy", "validate", "--config", s.caddyFile, "--adapter", "caddyfile")
	default:
		kind = KindNginx
		res, err = runTest(run, "nginx", "-t")
	}
	if err != nil {
		return nil, err
	}
	s.remember(kind, started, res)
	return res, nil
}

// WithTestedConfig runs start only when the engine's config test passes, and
// holds the service lock across both, so no candidate can be staged between
// the test and what it guards. It exists for starting and restarting the
// engine's service: this host's nginx.service runs `nginx -t` before it
// starts, so a restart over a broken file stops nginx and then cannot bring it
// back — every site down until someone logs in. A failing test returns the
// result with ErrInvalidConf and start is never called; start must not call
// back into the Service.
func (s *Service) WithTestedConfig(ctx context.Context, kind Kind, start func() error) (*ValidationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.Test(ctx, kind)
	if err != nil {
		return nil, err
	}
	if !res.Valid {
		return res, ErrInvalidConf
	}
	return res, start()
}

type ReloadResult struct {
	Validation *ValidationResult `json:"validation"`
	Reloaded   bool              `json:"reloaded"`
	Output     string            `json:"output"`
}

// Reload tests first and refuses to reload a config that does not pass. This
// is the guard rail that makes a config editor safe to expose at all. Its test
// is kept as the engine's last, passed or not; one that gives no verdict is
// ErrTestUnfinished, and nothing is reloaded or kept.
func (s *Service) Reload(ctx context.Context, kind Kind) (*ReloadResult, error) {
	s.forgetEffective()
	if kind == KindCaddyIngress {
		return s.reloadIngress(ctx)
	}
	var validation *ValidationResult
	var err error
	var reload *exec.Cmd
	started := time.Now()
	// As Test's: a closed tab does not stop the test once begun.
	run := context.WithoutCancel(ctx)
	switch kind {
	case KindCaddy:
		validation, err = runTest(run, "caddy", "validate", "--config", s.caddyFile, "--adapter", "caddyfile")
		reload = hostexec.Command(ctx, "caddy", "reload", "--config", s.caddyFile)
	default:
		kind = KindNginx
		validation, err = runTest(run, "nginx", "-t")
		reload = hostexec.Command(ctx, "nginx", "-s", "reload")
	}
	if err != nil {
		return nil, err
	}
	s.remember(kind, started, validation)
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
