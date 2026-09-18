package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// Config is resolved once at boot from the environment. Every configurable
// field that weakens the security posture (such as the allowlist or TLS)
// fails closed: the zero value is the restrictive one.
type Config struct {
	Addr           string
	DataDir        string
	MasterKeyHex   string
	TrustedProxies []*net.IPNet
	AllowedCIDRs   []*net.IPNet
	AllowedOrigins []string
	SessionTTL     time.Duration
	IdleTTL        time.Duration
	DockerHost     string
	// Site and TLSMode describe how the proxy in front of this process is
	// reached. This process never binds them — it is loopback-only in every
	// configuration — but it is the half of the stack that can run `tailscale
	// cert` on the host, and it is what the settings page reports the address
	// from.
	Site           string
	TLSMode        string
	TerminalEnable bool
	AgentMode      bool
	// Require2FA makes an authenticator mandatory for every account: a
	// password-only session then reaches nothing but the enrolment routes.
	//
	// It used to be unconditional, and the reasoning was sound for the install
	// this product was first written for. It is wrong for the one it is
	// actually installed into: a dashboard reachable only over a tailnet or an
	// ssh tunnel already sits behind an authenticated network, and forcing an
	// authenticator app onto a single operator setting up their own server was
	// turning the first ninety seconds of the product into a chore that could
	// not be skipped. Enrolment is still offered, still encouraged, and can be
	// made compulsory again by setting this — but the default no longer
	// decides on the operator's behalf.
	Require2FA       bool
	TerminalShell    string
	TerminalUser     string
	FileRoots        []string
	LogRoots         []string
	NginxDir         string
	CaddyFile        string
	ComposeRoots     []string
	GitRoots         []string
	DeployRoots      []string
	DeployHeavySlots int
	DeployLightSlots int
	DeployLeaseTTL   time.Duration
	BackupLocalDir   string
	Dev              bool

	// MetricsInterval is how often the backend samples the host into its
	// own history, independently of any browser. MetricsRetention of 0
	// turns that recording off; the live charts still work, they just have
	// nothing to show for the time nobody was watching.
	MetricsInterval  time.Duration
	MetricsRetention time.Duration

	// UpdateCheck is whether the dashboard may ask the repository whether a
	// newer version exists. Turning it off leaves the compiled-in changelog
	// readable. Deployment branch monitoring uses separate Git connections
	// and is independent of update discovery.
	UpdateCheck bool
	// UpdateRepo is the GitHub repository releases are read from, as
	// owner/name. A fork sets it and starts describing its own releases.
	UpdateRepo string
	// UpdateBranch is the ref the changelog is read from and the checkout is
	// fast-forwarded to.
	UpdateBranch string
	// UpdateDir is the checkout this dashboard was installed from. Empty is
	// the normal case: it is discovered by asking Docker where this stack was
	// deployed from, which is a fact nobody has to keep in step with reality.
	UpdateDir string
}

func Load() (*Config, error) {
	l := &loader{}
	c := &Config{
		Addr:           env("JD_ADDR", "127.0.0.1:8080"),
		DataDir:        env("JD_DATA_DIR", "/var/lib/just-dashboard"),
		MasterKeyHex:   Env("JD_MASTER_KEY"),
		SessionTTL:     l.duration("JD_SESSION_TTL", 12*time.Hour),
		IdleTTL:        l.duration("JD_SESSION_IDLE_TTL", 60*time.Minute),
		DockerHost:     env("JD_DOCKER_HOST", "unix:///var/run/docker.sock"),
		Site:           env("JD_SITE", "localhost"),
		TLSMode:        strings.ToLower(env("JD_TLS", "internal")),
		TerminalEnable: l.boolean("JD_TERMINAL_ENABLED", true),
		AgentMode:      l.boolean("JD_AGENT_MODE", false),
		Require2FA:     l.boolean("JD_REQUIRE_2FA", false),
		// Empty means "the account's own login shell", read from the host's
		// passwd file. Naming a shell here would override what the operator
		// chose with chsh, which is the opposite of behaving like ssh.
		TerminalShell: env("JD_TERMINAL_SHELL", ""),
		// Empty means "the lowest regular account on the host" — the login a
		// VPS provider creates, and the one an operator would reach for.
		TerminalUser:     env("JD_TERMINAL_USER", ""),
		FileRoots:        envList("JD_FILE_ROOTS", "/"),
		LogRoots:         envList("JD_LOG_ROOTS", "/var/log"),
		NginxDir:         env("JD_NGINX_DIR", "/etc/nginx"),
		CaddyFile:        env("JD_CADDYFILE", "/etc/caddy/Caddyfile"),
		ComposeRoots:     envList("JD_COMPOSE_ROOTS", "/opt,/srv,/home"),
		GitRoots:         envList("JD_GIT_ROOTS", "/opt,/srv,/home,/root"),
		DeployRoots:      deployRoots(l),
		DeployHeavySlots: l.integer("JD_DEPLOY_HEAVY_SLOTS", 1, 1, 8),
		DeployLightSlots: l.integer("JD_DEPLOY_LIGHT_SLOTS", 2, 1, 8),
		DeployLeaseTTL:   l.boundedDuration("JD_DEPLOY_LEASE_TTL", 30*time.Second, 5*time.Second, 5*time.Minute),
		BackupLocalDir:   env("JD_BACKUP_DIR", "/var/backups/just-dashboard"),
		Dev:              l.boolean("JD_DEV", false),
		MetricsInterval:  l.window("JD_METRICS_INTERVAL", metrics.DefaultInterval),
		MetricsRetention: l.optionalWindow("JD_METRICS_RETENTION", metrics.DefaultRetention),
		UpdateCheck:      l.boolean("JD_UPDATE_CHECK", true),
		UpdateRepo:       env("JD_UPDATE_REPO", selfupdate.DefaultRepo),
		UpdateBranch:     env("JD_UPDATE_BRANCH", selfupdate.DefaultRef),
		UpdateDir:        env("JD_UPDATE_DIR", ""),
	}

	// The dashboard is root-equivalent. It must never be reachable from the
	// open internet, so an explicit network allowlist is mandatory. Binding
	// to loopback only is the one configuration that implies its own
	// perimeter (reverse proxy / WireGuard terminates in front of us).
	raw := Env("JD_ALLOWED_CIDRS")
	if raw == "" {
		if !isLoopbackBind(c.Addr) {
			return nil, fmt.Errorf("JD_ALLOWED_CIDRS is required when JD_ADDR (%s) is not bound to loopback: "+
				"expose this dashboard only through a WireGuard/VPN interface or a reverse proxy with an IP allowlist", c.Addr)
		}
		raw = "127.0.0.1/32,::1/128"
	}
	nets, err := parseCIDRs(raw)
	if err != nil {
		return nil, fmt.Errorf("JD_ALLOWED_CIDRS: %w", err)
	}
	c.AllowedCIDRs = nets

	if tp := Env("JD_TRUSTED_PROXIES"); tp != "" {
		nets, err := parseCIDRs(tp)
		if err != nil {
			return nil, fmt.Errorf("JD_TRUSTED_PROXIES: %w", err)
		}
		c.TrustedProxies = nets
	}

	// An install that predates the "Just Dashboard" rename keeps its database
	// where it left it. Adopting that path is the difference between an
	// upgrade and a dashboard that boots with an empty account table and
	// bootstraps a new admin over the top of a real one.
	c.DataDir = adoptLegacyData(c.DataDir, "/var/lib/vps-dashboard")

	c.AllowedOrigins = envList("JD_ALLOWED_ORIGINS", "")
	if len(c.MasterKeyHex) != 64 {
		return nil, fmt.Errorf("JD_MASTER_KEY must be 64 hex characters (32 bytes); generate one with: openssl rand -hex 32")
	}
	if err := l.err(); err != nil {
		return nil, err
	}
	return c, nil
}

// loader collects malformed settings so Load can refuse to start.
//
// These used to fall back to the default on a parse error, silently. That is
// wrong for JD_SESSION_TTL=12 — no unit, so the operator who meant twelve
// minutes gets twelve hours and nothing in the log disagrees with them. A
// package whose doc comment promises to fail closed has to fail closed on the
// settings it cannot read, too.
type loader struct{ errs []string }

func (l *loader) boolean(k string, def bool) bool {
	v := Env(k)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Sprintf("%s=%q is not a boolean (true/false)", k, v))
		return def
	}
	return b
}

func (l *loader) duration(k string, def time.Duration) time.Duration {
	v := Env(k)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Sprintf("%s=%q is not a duration; use a unit, for example 12h or 60m", k, v))
		return def
	}
	if d <= 0 {
		l.errs = append(l.errs, fmt.Sprintf("%s=%q must be positive", k, v))
		return def
	}
	return d
}

func (l *loader) boundedDuration(k string, def, min, max time.Duration) time.Duration {
	d := l.duration(k, def)
	if Env(k) == "" {
		return d
	}
	if d < min || d > max {
		l.errs = append(l.errs, fmt.Sprintf("%s must be between %s and %s", k, min, max))
		return def
	}
	return d
}

func (l *loader) integer(k string, def, min, max int) int {
	v := Env(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Sprintf("%s=%q is not an integer", k, v))
		return def
	}
	if n < min || n > max {
		l.errs = append(l.errs, fmt.Sprintf("%s must be between %d and %d", k, min, max))
		return def
	}
	return n
}

// window is duration, extended with the day and week suffixes time.ParseDuration
// refuses. Retention on a monitoring page is naturally expressed in days, and
// making an operator write "168h" for a week is the kind of paper cut that
// ends with the setting left at its default.
func (l *loader) window(k string, def time.Duration) time.Duration {
	v := Env(k)
	if v == "" {
		return def
	}
	d, err := metrics.ParseWindow(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Sprintf("%s=%q is not a duration; use a unit, for example 30s, 12h or 7d", k, v))
		return def
	}
	if d <= 0 {
		l.errs = append(l.errs, fmt.Sprintf("%s=%q must be positive", k, v))
		return def
	}
	return d
}

// optionalWindow is window, plus the one value that means "do not do this at
// all". Retention is the setting an operator on a tiny disk needs to be able
// to switch off, and making them do it by setting an absurdly small number
// would leave the sampler running for nothing.
func (l *loader) optionalWindow(k string, def time.Duration) time.Duration {
	v := Env(k)
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "off", "false", "none", "never":
		return 0
	}
	return l.window(k, def)
}

func (l *loader) err() error {
	if len(l.errs) == 0 {
		return nil
	}
	return fmt.Errorf("invalid configuration: %s", strings.Join(l.errs, "; "))
}

func parseCIDRs(raw string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			ip := net.ParseIP(part)
			if ip == nil {
				return nil, fmt.Errorf("invalid address %q", part)
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			part = fmt.Sprintf("%s/%d", part, bits)
		}
		_, n, err := net.ParseCIDR(part)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no usable entries")
	}
	return out, nil
}

func isLoopbackBind(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// adoptLegacyData returns the pre-rename data directory when it holds the
// database and the configured one does not.
//
// It looks for the database file rather than for the directory, because the
// compose stack bind-mounts the configured path and Docker creates it empty if
// it is missing — so "the new directory exists" says nothing about whether
// anything has ever been written to it.
func adoptLegacyData(current, legacy string) string {
	if current == legacy || hasDatabase(current) || !hasDatabase(legacy) {
		return current
	}
	return legacy
}

func hasDatabase(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, store.DatabaseFile))
	return err == nil && !info.IsDir()
}

// Env reads a JD_* setting, falling back to the VPSD_* name the dashboard
// used when it was called "VPS Dashboard". An install that predates the rename
// keeps its .env working; a new one never sees the old prefix.
func Env(k string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	if legacy, ok := strings.CutPrefix(k, "JD_"); ok {
		return strings.TrimSpace(os.Getenv("VPSD_" + legacy))
	}
	return ""
}

func env(k, def string) string {
	if v := Env(k); v != "" {
		return v
	}
	return def
}

func envList(k, def string) []string {
	v := Env(k)
	if v == "" {
		v = def
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func deployRoots(l *loader) []string {
	// Unlike the general list settings, an explicitly empty deployment root
	// must not acquire a permissive filesystem default. Unset keeps the shipped
	// compatibility roots; set-but-empty is a configuration error.
	for _, key := range []string{"JD_DEPLOY_ROOTS", "VPSD_DEPLOY_ROOTS"} {
		if raw, ok := os.LookupEnv(key); ok {
			if strings.TrimSpace(raw) == "" {
				l.errs = append(l.errs, key+" must contain at least one absolute path")
				return nil
			}
			break
		}
	}
	return envList("JD_DEPLOY_ROOTS", "/opt,/srv,/home,/root")
}
