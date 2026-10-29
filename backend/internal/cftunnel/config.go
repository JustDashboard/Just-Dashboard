package cftunnel

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Configuration is a tunnel's remotely managed configuration: where each
// hostname's requests go, in order, the last rule answering everything else.
type Configuration struct {
	Ingress []IngressRule `json:"ingress"`
}

// IngressRule is one hostname and the service its requests are sent to.
type IngressRule struct {
	Hostname      string         `json:"hostname,omitempty"`
	Service       string         `json:"service"`
	OriginRequest *OriginRequest `json:"originRequest,omitempty"`
}

// OriginRequest is how the connector speaks to the origin.
type OriginRequest struct {
	// NoTLSVerify accepts the proxy's own certificate, for an origin that
	// only speaks HTTPS with Caddy's internal authority.
	NoTLSVerify bool `json:"noTLSVerify,omitempty"`
	// OriginServerName is the name the connector asks the origin's TLS for.
	OriginServerName string `json:"originServerName,omitempty"`
}

// notFound answers a hostname the tunnel has no rule for, as Cloudflare's
// own examples end every ingress list.
const notFound = "http_status:404"

var hostnameRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

// ValidHostname says whether a name can be a tunnel's public hostname: a
// lowercase DNS name with no wildcard — a wildcard needs a wildcard record
// and certificate of its own at Cloudflare, which this does not make.
func ValidHostname(name string) bool {
	return len(name) <= 253 && hostnameRE.MatchString(name)
}

// Render is the configuration for these hostnames, each sent to origin:
// sorted and once each, so the same set renders the same bytes and a pass
// that changes nothing sends nothing.
func Render(hostnames []string, origin Origin) (Configuration, error) {
	if origin.Service == "" {
		return Configuration{}, errors.New("the tunnel has no origin to send requests to")
	}
	names := []string{}
	for _, name := range hostnames {
		name = strings.ToLower(strings.TrimSpace(name))
		if !ValidHostname(name) {
			return Configuration{}, fmt.Errorf("%s cannot be routed through a tunnel", name)
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	configuration := Configuration{Ingress: make([]IngressRule, 0, len(names)+1)}
	for _, name := range names {
		rule := IngressRule{Hostname: name, Service: origin.Service}
		if strings.HasPrefix(origin.Service, "https://") {
			rule.OriginRequest = &OriginRequest{NoTLSVerify: true, OriginServerName: name}
		}
		configuration.Ingress = append(configuration.Ingress, rule)
	}
	configuration.Ingress = append(configuration.Ingress, IngressRule{Service: notFound})
	return configuration, nil
}

// Digest names a configuration's bytes.
func (c Configuration) Digest() string {
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Hostnames are the names the configuration routes.
func (c Configuration) Hostnames() []string {
	out := []string{}
	for _, rule := range c.Ingress {
		if rule.Hostname != "" {
			out = append(out, rule.Hostname)
		}
	}
	return out
}

// Origin is where the connector sends a tunnel's requests: this host's
// proxy, over the private network the connector shares with it.
type Origin struct {
	// Service is the URL the connector asks, http://<proxy>:80.
	Service string `json:"service"`
	// Network is the Docker network the connector joins to reach it.
	Network string `json:"network"`
	// HostGateway says the proxy is this host's own nginx, which the
	// connector reaches as host.docker.internal through Docker's gateway.
	HostGateway bool `json:"hostGateway,omitempty"`
	// Proxy names it in words: "the Caddy ingress just-dashboard-ingress".
	Proxy string `json:"proxy"`
}

// TokenFields are what a tunnel token says about itself: the account and
// the tunnel. The secret it carries is never kept outside the sealed token.
type TokenFields struct {
	AccountID string
	TunnelID  string
}

var tunnelIDRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ParseToken reads a tunnel token as cloudflared does: base64 of a JSON
// object with the account tag, the tunnel's id and its secret. A token that
// is not one is refused here, with the reason, rather than by the connector
// a minute later.
func ParseToken(token string) (TokenFields, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return TokenFields{}, errors.New("paste the tunnel's token")
	}
	if len(token) > 4096 {
		return TokenFields{}, errors.New("that is too long to be a tunnel token")
	}
	var raw []byte
	var err error
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if raw, err = encoding.DecodeString(token); err == nil {
			break
		}
	}
	if err != nil {
		return TokenFields{}, errors.New("a tunnel token is base64, as Cloudflare shows it after cloudflared tunnel run --token; this is not")
	}
	var fields struct {
		Account string `json:"a"`
		Tunnel  string `json:"t"`
		Secret  string `json:"s"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil || fields.Account == "" || fields.Secret == "" {
		return TokenFields{}, errors.New("that does not read as a tunnel token: it names no account, tunnel and secret")
	}
	if !tunnelIDRE.MatchString(strings.ToLower(fields.Tunnel)) {
		return TokenFields{}, errors.New("that token names no tunnel id")
	}
	return TokenFields{AccountID: fields.Account, TunnelID: strings.ToLower(fields.Tunnel)}, nil
}

// TokenDigest names a token without carrying it: the connector container is
// labelled with it, so a token changed since is a container made again.
func TokenDigest(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:8])
}
