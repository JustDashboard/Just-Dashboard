// Package dnsservice manages declared connections to mature DNS engines. It
// never becomes a resolver or redirects the host's DNS policy.
package dnsservice

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var (
	ErrNotFound    = errors.New("DNS service connection not found")
	ErrReadOnly    = errors.New("DNS service connection is read-only")
	ErrConflict    = errors.New("DNS service changed; read and review a new plan")
	ErrUnavailable = errors.New("DNS service is unavailable")
)

type Engine string

const (
	AdGuard    Engine = "adguard"
	PiHole     Engine = "pihole"
	Technitium Engine = "technitium"
)

type Credential struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
}

type ConnectionRequest struct {
	Name       string     `json:"name"`
	Engine     Engine     `json:"engine"`
	Endpoint   string     `json:"endpoint"`
	ServerName string     `json:"serverName,omitempty"`
	CA         string     `json:"ca,omitempty"`
	Management bool       `json:"management"`
	Credential Credential `json:"credential"`
}

type Connection struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Engine        Engine    `json:"engine"`
	Endpoint      string    `json:"endpoint"`
	ServerName    string    `json:"serverName,omitempty"`
	CustomCA      bool      `json:"customCA"`
	Management    bool      `json:"management"`
	HasCredential bool      `json:"hasCredential"`
	Generation    int64     `json:"generation"`
	Ownership     string    `json:"ownership"`
	ContainerID   string    `json:"containerId,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type Reading struct {
	State   string `json:"state"`
	Basis   string `json:"basis"`
	Summary string `json:"summary"`
}

type Listener struct {
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Scope    string `json:"scope"`
}

type ClientPolicy struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
	Groups    []int    `json:"groups,omitempty"`
	Filtering *bool    `json:"filtering,omitempty"`
	Inherited bool     `json:"inherited"`
}

type Zone struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Disabled bool   `json:"disabled"`
	DNSSEC   string `json:"dnssec"`
}

type LocalOverride struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

type NativeGroup struct {
	ID             *int              `json:"id,omitempty"`
	Name           string            `json:"name"`
	Enabled        *bool             `json:"enabled,omitempty"`
	ClientScopes   []string          `json:"clientScopes"`
	ListenerScopes []string          `json:"listenerScopes"`
	Domains        []string          `json:"domains"`
	Translations   map[string]string `json:"translations,omitempty"`
}

type Query struct {
	At       string `json:"at"`
	Client   string `json:"client"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	Protocol string `json:"protocol,omitempty"`
}

type NativeProcess struct {
	PID           int       `json:"pid"`
	StartedAt     time.Time `json:"startedAt"`
	StartedBefore time.Time `json:"startedBefore"`
}

type Snapshot struct {
	PolicyFingerprint   string              `json:"policyFingerprint"`
	ObservedAt          time.Time           `json:"observedAt"`
	Engine              Engine              `json:"engine"`
	Version             string              `json:"version"`
	Roles               []string            `json:"roles"`
	Transport           Reading             `json:"transport"`
	Runtime             Reading             `json:"runtime"`
	Process             *NativeProcess      `json:"process,omitempty"`
	Listeners           []Listener          `json:"listeners"`
	Access              Reading             `json:"access"`
	AllowedClients      []string            `json:"allowedClients"`
	DeniedClients       []string            `json:"deniedClients"`
	Protection          bool                `json:"protection"`
	Upstreams           []string            `json:"upstreams"`
	UpstreamProtocol    string              `json:"upstreamProtocol"`
	ProtectionTemporary bool                `json:"protectionTemporary"`
	Clients             []ClientPolicy      `json:"clients"`
	ClientEvidence      Reading             `json:"clientEvidence"`
	Zones               []Zone              `json:"zones"`
	ZoneEvidence        Reading             `json:"zoneEvidence"`
	Views               Reading             `json:"views"`
	ViewGroups          []NativeGroup       `json:"viewGroups"`
	NamedNetworks       map[string][]string `json:"namedNetworks"`
	TranslationEnabled  *bool               `json:"translationEnabled,omitempty"`
	FilterGroups        []NativeGroup       `json:"filterGroups"`
	AppProtection       *bool               `json:"appProtection,omitempty"`
	AppClientEvidence   Reading             `json:"appClientEvidence"`
	LocalOverrides      []LocalOverride     `json:"localOverrides"`
	OverrideEvidence    Reading             `json:"overrideEvidence"`
	Queries             []Query             `json:"queries"`
	QueryEvidence       Reading             `json:"queryEvidence"`
	Limitations         []string            `json:"limitations"`
}

type View struct {
	Connection Connection `json:"connection"`
	Snapshot   *Snapshot  `json:"snapshot,omitempty"`
	State      string     `json:"state"`
	Error      string     `json:"error,omitempty"`
}

type ChangeRequest struct {
	Action         string   `json:"action"`
	Protection     *bool    `json:"protection,omitempty"`
	Upstreams      []string `json:"upstreams,omitempty"`
	AllowedClients []string `json:"allowedClients,omitempty"`
	DeniedClients  []string `json:"deniedClients,omitempty"`
	Zone           string   `json:"zone,omitempty"`
}

type Change struct {
	ID           string        `json:"id"`
	ConnectionID string        `json:"connectionId"`
	Generation   int64         `json:"generation"`
	Request      ChangeRequest `json:"request"`
	Before       *Snapshot     `json:"before,omitempty"`
	After        *Snapshot     `json:"after,omitempty"`
	State        string        `json:"state"`
	CreatedAt    time.Time     `json:"createdAt"`
	ExpiresAt    time.Time     `json:"expiresAt"`
	EndedAt      *time.Time    `json:"endedAt,omitempty"`
	Error        string        `json:"error,omitempty"`
}

func ValidateConnection(req ConnectionRequest) (ConnectionRequest, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 80 || strings.IndexFunc(req.Name, unicode.IsControl) >= 0 {
		return req, errors.New("give the DNS connection a name of at most 80 characters without controls")
	}
	if req.Engine != AdGuard && req.Engine != PiHole && req.Engine != Technitium {
		return req, errors.New("select AdGuard Home, Pi-hole or Technitium")
	}
	u, err := url.Parse(req.Endpoint)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return req, errors.New("endpoint is an HTTP(S) origin with a literal IP and explicit port")
	}
	ip, err := netip.ParseAddr(u.Hostname())
	port, portErr := strconv.Atoi(u.Port())
	if err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() || portErr != nil || port < 1 || port > 65535 {
		return req, errors.New("endpoint needs a unicast literal IP and explicit port")
	}
	if u.Scheme == "http" && !ip.IsLoopback() {
		return req, errors.New("cleartext management credentials are allowed only over loopback; use verified HTTPS for other addresses")
	}
	if len(req.ServerName) > 253 || strings.IndexFunc(req.ServerName, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-')
	}) >= 0 {
		return req, errors.New("TLS server name is a DNS name")
	}
	if req.ServerName != "" && !validLabels(strings.ToLower(strings.TrimSuffix(req.ServerName, "."))) {
		return req, errors.New("TLS server name requires complete nonempty DNS labels")
	}
	if len(req.CA) > 32768 || strings.ContainsRune(req.CA, 0) {
		return req, errors.New("custom CA exceeds the certificate limit")
	}
	if u.Scheme != "https" && (req.CA != "" || req.ServerName != "") {
		return req, errors.New("CA and server identity require HTTPS")
	}
	if err := validateCredential(req.Engine, req.Credential); err != nil {
		return req, err
	}
	req.Endpoint = strings.TrimSuffix(u.String(), "/")
	return req, nil
}

func validateCredential(engine Engine, c Credential) error {
	for _, value := range []string{c.Username, c.Password, c.Token} {
		if len(value) > 4096 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return errors.New("credential exceeds bounds or contains control characters")
		}
	}
	switch engine {
	case AdGuard:
		if c.Username == "" || c.Password == "" || c.Token != "" {
			return errors.New("AdGuard requires username and password")
		}
	case PiHole:
		if c.Password == "" || c.Username != "" || c.Token != "" {
			return errors.New("Pi-hole requires a native API password")
		}
	case Technitium:
		if c.Token == "" || c.Username != "" || c.Password != "" || strings.ContainsAny(c.Token, " \t") {
			return errors.New("Technitium requires a scoped API token")
		}
	default:
		return errors.New("unsupported DNS engine")
	}
	return nil
}

func validateChange(req ChangeRequest, engine Engine) error {
	switch req.Action {
	case "protection":
		if req.Protection == nil || req.Upstreams != nil || req.AllowedClients != nil || req.DeniedClients != nil || req.Zone != "" {
			return errors.New("protection change takes only an explicit enabled value")
		}
	case "upstreams":
		if req.Protection != nil || len(req.Upstreams) == 0 || len(req.Upstreams) > 16 || req.AllowedClients != nil || req.DeniedClients != nil || req.Zone != "" {
			return errors.New("upstream change takes 1–16 exact classic DNS endpoints")
		}
		for _, endpoint := range req.Upstreams {
			ap, err := netip.ParseAddrPort(endpoint)
			if err != nil || ap.Port() == 0 || ap.Addr().Zone() != "" || ap.Addr().IsUnspecified() || ap.Addr().IsMulticast() {
				return fmt.Errorf("upstream %q requires a literal address and port", endpoint)
			}
		}
	case "access":
		if engine != AdGuard || req.Protection != nil || req.Upstreams != nil || len(req.AllowedClients) == 0 || len(req.AllowedClients) > 128 || len(req.DeniedClients) > 128 || req.Zone != "" {
			return errors.New("AdGuard access change requires an explicit nonempty allowed-client scope")
		}
		seen := map[string]bool{}
		for _, value := range append(append([]string{}, req.AllowedClients...), req.DeniedClients...) {
			if prefix, err := netip.ParsePrefix(value); err != nil || prefix.Masked() != prefix {
				return errors.New("client scopes are canonical IP prefixes")
			}
			if seen[value] {
				return errors.New("client scopes must be unique across allowed and denied lists")
			}
			seen[value] = true
		}
	case "zone_create":
		if engine != Technitium || req.Protection != nil || req.Upstreams != nil || req.AllowedClients != nil || req.DeniedClients != nil || !validZone(req.Zone) {
			return errors.New("authoritative primary-zone creation requires a Technitium DNS name")
		}
	default:
		return errors.New("unsupported native DNS change")
	}
	return nil
}

func validZone(name string) bool {
	return strings.Contains(name, ".") && validLabels(name)
}

func validLabels(name string) bool {
	if len(name) == 0 || len(name) > 253 || name != strings.ToLower(name) {
		return false
	}
	for _, part := range strings.Split(name, ".") {
		if part == "" || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return false
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}
