package selfcfg

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/portalloc"
)

// The settings an operator can change from inside the dashboard.
//
// Deliberately not "every JD_ variable". The ones here are the ones somebody
// has a reason to change from a browser — where it listens, who may reach it,
// how it is trusted, how long a session lasts — and every one of them is
// checked before it is written, because the process validating the change is
// the process the change is about to restart.
//
// Everything else (the roots the file manager may open, the backup directory,
// the deploy worker slots) stays in .env and ssh. Those are decisions made
// once at install time, and putting them on a settings page would be inviting
// an operator to break their dashboard for no benefit.

// TLS modes.
const (
	// TLSTailscale serves the certificate `tailscale cert` issues for this
	// machine's MagicDNS name. It is a real, publicly trusted certificate —
	// Tailscale's CA is Let's Encrypt — so the browser shows a padlock and no
	// warning, which is the only configuration here that manages that over a
	// network. It requires HTTPS to be enabled for the tailnet and the name in
	// Site to be the machine's `*.ts.net` name.
	TLSTailscale = "tailscale"
	// TLSInternal is Caddy's own CA. Encrypted, unverifiable, and the reason
	// browsers put a line through the padlock: nothing outside this machine
	// has ever heard of the issuer.
	TLSInternal = "internal"
	// TLSOff serves plain HTTP, and is allowed only on loopback. That is not
	// the compromise it sounds like: an SSH tunnel is already an encrypted,
	// authenticated channel, and browsers treat http://localhost as a secure
	// context — so this is the one configuration that produces no warning at
	// all, with no certificate to trust and nothing to click through.
	TLSOff = "off"
)

// Settings is the editable configuration, as the API speaks it.
type Settings struct {
	// Site is the address the dashboard answers on and the name on its
	// certificate. Caddy binds it, so it is also what decides which interface
	// is listening.
	Site string `json:"site"`
	// Bind is the interface to listen on, when that is not the same string as
	// Site. Blank is the ordinary case and means "the address above". It is
	// separate for one configuration: a Tailscale install answers for a
	// MagicDNS name whose certificate is issued to that name, while the socket
	// has to be opened on the tailnet IP behind it.
	Bind string `json:"bind"`
	TLS  string `json:"tls"`
	// Port is the one an operator types. The other two are internal: nothing
	// off this machine can reach them, and they are here only because a port
	// already taken by something else is a real and ordinary collision.
	Port         int `json:"port"`
	FrontendPort int `json:"frontendPort"`
	BackendPort  int `json:"backendPort"`

	AllowedCIDRs    string `json:"allowedCidrs"`
	TerminalEnabled bool   `json:"terminalEnabled"`
	Require2FA      bool   `json:"require2fa"`
	SessionTTL      string `json:"sessionTtl"`
	IdleTTL         string `json:"idleTtl"`
	UpdateCheck     bool   `json:"updateCheck"`
}

// Env keys, in one place, because the compose file, the Caddyfile and this
// package all have to mean the same variable by the same name.
const (
	keySite         = "JD_SITE"
	keyBind         = "JD_BIND"
	keyTLS          = "JD_TLS"
	keyPort         = "JD_PORT"
	keyFrontendPort = "JD_FRONTEND_PORT"
	keyBackendPort  = "JD_BACKEND_PORT"
	keyAllowedCIDRs = "JD_ALLOWED_CIDRS"
	keyTerminal     = "JD_TERMINAL_ENABLED"
	keyRequire2FA   = "JD_REQUIRE_2FA"
	keySessionTTL   = "JD_SESSION_TTL"
	keyIdleTTL      = "JD_SESSION_IDLE_TTL"
	keyUpdateCheck  = "JD_UPDATE_CHECK"
)

// Defaults match docker-compose.yml and the Caddyfile. A setting absent from
// .env is not unset — it is whatever those two files fall back to — and
// reporting it as blank would be a lie the operator then "fixes".
func defaults() Settings {
	return Settings{
		Site:            "localhost",
		TLS:             TLSInternal,
		Port:            8443,
		FrontendPort:    3000,
		BackendPort:     8080,
		AllowedCIDRs:    "127.0.0.1/32,::1/128",
		TerminalEnabled: true,
		Require2FA:      false,
		SessionTTL:      "12h",
		IdleTTL:         "60m",
		UpdateCheck:     true,
	}
}

// ReadSettings is what the stack is configured to do, read from the file that
// configures it.
func ReadSettings(f *EnvFile) Settings {
	s := defaults()
	get := func(key string) (string, bool) {
		v := strings.TrimSpace(f.Get(key))
		return v, v != ""
	}
	if v, ok := get(keySite); ok {
		s.Site = v
	}
	if v, ok := get(keyBind); ok {
		s.Bind = v
	}
	if v, ok := get(keyTLS); ok {
		s.TLS = strings.ToLower(v)
	}
	if v, ok := get(keyPort); ok {
		s.Port = atoi(v, s.Port)
	}
	if v, ok := get(keyFrontendPort); ok {
		s.FrontendPort = atoi(v, s.FrontendPort)
	}
	if v, ok := get(keyBackendPort); ok {
		s.BackendPort = atoi(v, s.BackendPort)
	}
	if v, ok := get(keyAllowedCIDRs); ok {
		s.AllowedCIDRs = v
	}
	if v, ok := get(keyTerminal); ok {
		s.TerminalEnabled = truthy(v, s.TerminalEnabled)
	}
	if v, ok := get(keyRequire2FA); ok {
		s.Require2FA = truthy(v, s.Require2FA)
	}
	if v, ok := get(keySessionTTL); ok {
		s.SessionTTL = v
	}
	if v, ok := get(keyIdleTTL); ok {
		s.IdleTTL = v
	}
	if v, ok := get(keyUpdateCheck); ok {
		s.UpdateCheck = truthy(v, s.UpdateCheck)
	}
	return s
}

// EnvValues is the settings as .env assignments.
func (s Settings) EnvValues() map[string]string {
	return map[string]string{
		keySite:         s.Site,
		keyBind:         s.Bind,
		keyTLS:          s.TLS,
		keyPort:         strconv.Itoa(s.Port),
		keyFrontendPort: strconv.Itoa(s.FrontendPort),
		keyBackendPort:  strconv.Itoa(s.BackendPort),
		keyAllowedCIDRs: s.AllowedCIDRs,
		keyTerminal:     strconv.FormatBool(s.TerminalEnabled),
		keyRequire2FA:   strconv.FormatBool(s.Require2FA),
		keySessionTTL:   s.SessionTTL,
		keyIdleTTL:      s.IdleTTL,
		keyUpdateCheck:  strconv.FormatBool(s.UpdateCheck),
	}
}

// Endpoint is the URL to open once this configuration is live. It is the one
// piece of the answer an operator genuinely cannot work out for themselves
// after changing a port, so the API states it rather than leaving them to
// reconstruct it from three fields.
func (s Settings) Endpoint() string {
	scheme := "https"
	if s.TLS == TLSOff {
		scheme = "http"
	}
	host := s.Site
	if host == "" {
		host = "localhost"
	}
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, s.Port)
}

// Change is one field moving, for the confirmation dialog and the audit trail.
type Change struct {
	Key string `json:"key"`
	// Label is what the field is called on screen, so the record of what
	// happened reads the same way as the form that caused it.
	Label string `json:"label"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// Diff lists what would actually change. An apply with nothing in it is
// refused rather than restarting the stack for no reason.
func Diff(old, next Settings) []Change {
	fields := []struct {
		key, label string
		from, to   string
	}{
		{keySite, "Address", old.Site, next.Site},
		{keyBind, "Listening interface", old.Bind, next.Bind},
		{keyTLS, "Certificate", old.TLS, next.TLS},
		{keyPort, "Dashboard port", itoa(old.Port), itoa(next.Port)},
		{keyFrontendPort, "Frontend port", itoa(old.FrontendPort), itoa(next.FrontendPort)},
		{keyBackendPort, "Backend port", itoa(old.BackendPort), itoa(next.BackendPort)},
		{keyAllowedCIDRs, "Network allowlist", old.AllowedCIDRs, next.AllowedCIDRs},
		{keyTerminal, "Web terminal", btoa(old.TerminalEnabled), btoa(next.TerminalEnabled)},
		{keyRequire2FA, "Two-factor required", btoa(old.Require2FA), btoa(next.Require2FA)},
		{keySessionTTL, "Session lifetime", old.SessionTTL, next.SessionTTL},
		{keyIdleTTL, "Idle timeout", old.IdleTTL, next.IdleTTL},
		{keyUpdateCheck, "Version checks", btoa(old.UpdateCheck), btoa(next.UpdateCheck)},
	}
	out := []Change{}
	for _, f := range fields {
		if f.from != f.to {
			out = append(out, Change{Key: f.key, Label: f.label, From: f.from, To: f.to})
		}
	}
	return out
}

// ErrNoChange is an apply that would write the file it just read.
var ErrNoChange = errors.New("nothing about this configuration is different")

// Validate checks a proposed configuration hard enough that the restart which
// follows is boring.
//
// clientIP is the address of whoever is asking, and the allowlist check
// against it is the single most valuable rule in this file: the allowlist is
// enforced before authentication, so an operator who removes their own network
// from it does not get an error page, they get a dashboard that has silently
// stopped existing for them. free is how a port is tested; it is a parameter
// so this is testable without binding anything.
func (s *Settings) Validate(old Settings, clientIP string, free func(port int) error) error {
	s.Site = strings.TrimSpace(s.Site)
	s.Bind = strings.TrimSpace(s.Bind)
	s.TLS = strings.ToLower(strings.TrimSpace(s.TLS))
	s.AllowedCIDRs = strings.TrimSpace(s.AllowedCIDRs)
	s.SessionTTL = strings.TrimSpace(s.SessionTTL)
	s.IdleTTL = strings.TrimSpace(s.IdleTTL)

	if s.Site == "" {
		return errors.New("the address is required: it is what the dashboard answers on and the name on its certificate")
	}
	if strings.ContainsAny(s.Site, " \t/\\") || strings.Contains(s.Site, "://") {
		return fmt.Errorf("%q is not an address — give a hostname or an IP, with no scheme and no path", s.Site)
	}
	if s.Site == "0.0.0.0" || s.Site == "::" {
		return errors.New("a wildcard address would put this dashboard on every interface, including the public one; " +
			"name the interface you actually reach it on")
	}

	if s.Bind != "" && !isLoopbackHost(s.Bind) && net.ParseIP(s.Bind) == nil {
		return fmt.Errorf("the listening interface has to be an IP address on this machine (or blank to use %s); "+
			"%q is a name, and the proxy resolves names in its own container rather than on the host", s.Site, s.Bind)
	}
	if s.Bind == "0.0.0.0" || s.Bind == "::" {
		return errors.New("binding a wildcard address would put this dashboard on every interface, " +
			"including any public one; name the interface you actually reach it on")
	}

	switch s.TLS {
	case TLSTailscale, TLSInternal:
	case TLSOff:
		if !isLoopbackHost(s.Site) {
			return errors.New("plain HTTP is allowed only on localhost, where an SSH tunnel already provides the encryption; " +
				"on any other address the traffic would cross the network in the clear")
		}
	default:
		return fmt.Errorf("unknown certificate mode %q: use %s, %s or %s", s.TLS, TLSTailscale, TLSInternal, TLSOff)
	}
	if s.TLS == TLSTailscale && !strings.HasSuffix(strings.ToLower(s.Site), ".ts.net") {
		return errors.New("a Tailscale certificate is issued for this machine's MagicDNS name, so the address has to be " +
			"that name (something.tailnet.ts.net) rather than an IP")
	}

	ports := []struct {
		name   string
		value  int
		was    int
		target *int
	}{
		{"dashboard port", s.Port, old.Port, &s.Port},
		{"frontend port", s.FrontendPort, old.FrontendPort, &s.FrontendPort},
		{"backend port", s.BackendPort, old.BackendPort, &s.BackendPort},
	}
	seen := map[int]bool{}
	for _, p := range ports {
		if p.value < 1 || p.value > 65535 {
			return fmt.Errorf("the %s must be between 1 and 65535", p.name)
		}
		if p.value < 1024 {
			return fmt.Errorf("the %s is %d; ports below 1024 are reserved for the system's own services "+
				"and collide with whatever already runs there", p.name, p.value)
		}
		selected, err := portalloc.Select(p.value, 1024, seen, func(candidate int) error {
			if candidate == p.was {
				return nil
			}
			if candidate == old.Port || candidate == old.FrontendPort || candidate == old.BackendPort {
				return portalloc.ErrReserved
			}
			if free != nil {
				return free(candidate)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("select %s: %w", p.name, err)
		}
		*p.target, seen[selected] = selected, true
	}

	nets, err := parseCIDRList(s.AllowedCIDRs)
	if err != nil {
		return fmt.Errorf("the network allowlist is not usable: %w", err)
	}
	if !containsLoopback(nets) {
		return errors.New("the allowlist has to keep 127.0.0.1/32 — that is what makes an SSH tunnel work, " +
			"and it is the way back in when everything else fails")
	}
	if ip := net.ParseIP(clientIP); ip != nil && !ip.IsLoopback() && !containsIP(nets, ip) {
		return fmt.Errorf("this allowlist does not include %s, which is the address you are connected from; "+
			"applying it would lock you out before the login page, because the allowlist is checked before authentication", clientIP)
	}

	if err := checkDuration("session lifetime", s.SessionTTL, time.Minute, 30*24*time.Hour); err != nil {
		return err
	}
	if err := checkDuration("idle timeout", s.IdleTTL, time.Minute, 30*24*time.Hour); err != nil {
		return err
	}
	return nil
}

func checkDuration(name, raw string, min, max time.Duration) error {
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("the %s (%q) needs a unit, for example 12h or 60m", name, raw)
	}
	if d < min || d > max {
		return fmt.Errorf("the %s must be between %s and %s", name, min, max)
	}
	return nil
}

func parseCIDRList(raw string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			ip := net.ParseIP(part)
			if ip == nil {
				return nil, fmt.Errorf("%q is neither an address nor a range", part)
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			part = fmt.Sprintf("%s/%d", part, bits)
		}
		_, n, err := net.ParseCIDR(part)
		if err != nil {
			return nil, fmt.Errorf("%q is not a valid range", part)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, errors.New("it is empty, and the backend refuses to start without one")
	}
	return out, nil
}

// normalizeCIDRs renders a list the way net.IPNet.String does, so two spellings
// of the same allowlist compare equal. An unparseable list is returned as it
// was: this is only used for comparison, and refusing to render it would turn
// a malformed setting into a silent one.
func normalizeCIDRs(raw string) string {
	nets, err := parseCIDRList(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	parts := make([]string, 0, len(nets))
	for _, n := range nets {
		parts = append(parts, n.String())
	}
	return strings.Join(parts, ",")
}

func containsLoopback(nets []*net.IPNet) bool {
	return containsIP(nets, net.ParseIP("127.0.0.1"))
}

func containsIP(nets []*net.IPNet, ip net.IP) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func atoi(v string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}

func itoa(n int) string { return strconv.Itoa(n) }

func btoa(b bool) string { return strconv.FormatBool(b) }

func truthy(v string, def bool) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return b
}
