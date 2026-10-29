package proxysvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// A Cloudflare Tunnel's connector (cftunnel) runs as a container of its own
// and reaches this host's proxy over a private network, never through a
// published port. These are the proxy's two halves of that: where the
// connector is to send requests, and the Cloudflare credentials saved for
// the DNS-01 plugin, which also create the tunnel and its records.

// TunnelOrigin is where a connector on network reaches the proxy that
// serves deployment routes.
type TunnelOrigin struct {
	// Service is the URL the connector asks.
	Service string
	// HostGateway says the proxy is this host's nginx, which the connector
	// reaches as host.docker.internal through Docker's gateway.
	HostGateway bool
	// Proxy names it for a sentence.
	Proxy string
}

// TunnelOrigin joins the Docker Caddy ingress to the connector's network and
// answers its name there, or — on a host whose routes are nginx's — this
// host's own nginx through Docker's gateway. Port 80 either way: Cloudflare
// terminates TLS, and a domain routed through the tunnel is served over
// plain HTTP on the private network.
func (s *Service) TunnelOrigin(ctx context.Context, network string) (TunnelOrigin, error) {
	edge, err := s.dockerCaddy(ctx)
	if err != nil {
		return TunnelOrigin{}, err
	}
	if edge != nil {
		return caddyTunnelOrigin(ctx, edge, network)
	}
	if s.canProvisionIngress(ctx) && !hostexec.Available("nginx") {
		return TunnelOrigin{}, errors.New("the Caddy ingress is started by the first deployment with a domain; the tunnel is pointed at it then")
	}
	if !hostexec.Available("nginx") {
		return TunnelOrigin{}, errors.New("this host runs neither the Caddy ingress nor nginx, so the tunnel has no proxy to send requests to")
	}
	return TunnelOrigin{Service: "http://host.docker.internal:80", HostGateway: true, Proxy: "this host's nginx"}, nil
}

// caddyTunnelOrigin is the Docker Caddy ingress as a connector's origin:
// joined to the connector's network and asked by its container name there.
func caddyTunnelOrigin(ctx context.Context, edge *dockerCaddy, network string) (TunnelOrigin, error) {
	if err := joinNetwork(ctx, edge.ID, network); err != nil {
		return TunnelOrigin{}, fmt.Errorf("the Caddy ingress could not join the tunnel's network: %w", err)
	}
	return TunnelOrigin{Service: "http://" + edge.Name + ":80", Proxy: "the Caddy ingress " + edge.Name}, nil
}

// joinNetwork connects a container to a network it is not on yet.
func joinNetwork(ctx context.Context, container, network string) error {
	raw, err := hostexec.Command(ctx, "docker", "inspect", "--type", "container", container).Output()
	if err != nil {
		return err
	}
	var containers []ingressContainer
	if json.Unmarshal(raw, &containers) != nil || len(containers) != 1 {
		return errors.New("the ingress could not be read")
	}
	if _, joined := containers[0].NetworkSettings.Networks[network]; joined {
		return nil
	}
	out, err := hostexec.Command(ctx, "docker", "network", "connect", network, container).CombinedOutput()
	if err != nil {
		return errors.New(strings.TrimSpace(string(out)))
	}
	return nil
}

// CloudflareCredentials are what is saved for the Cloudflare DNS plugin: an
// API token, or the account e-mail with its global key.
type CloudflareCredentials struct {
	Token, Email, Key string
}

// SavedCloudflareCredentials reads the credentials saved for the Cloudflare
// DNS provider under Certificates. They stay in certbot's file; this reads
// them for one call and keeps nothing.
func SavedCloudflareCredentials() (CloudflareCredentials, error) {
	raw, err := os.ReadFile(credentialsPath("cloudflare"))
	if errors.Is(err, os.ErrNotExist) {
		return CloudflareCredentials{}, errors.New("no Cloudflare credentials are saved: save an API token for the Cloudflare DNS provider under Certificates, with Zone › DNS › Edit and Account › Cloudflare Tunnel › Edit")
	}
	if err != nil {
		return CloudflareCredentials{}, err
	}
	var out CloudflareCredentials
	for _, line := range strings.Split(string(raw), "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(name) {
		case "dns_cloudflare_api_token":
			out.Token = strings.TrimSpace(value)
		case "dns_cloudflare_email":
			out.Email = strings.TrimSpace(value)
		case "dns_cloudflare_api_key":
			out.Key = strings.TrimSpace(value)
		}
	}
	if out.Token == "" && (out.Email == "" || out.Key == "") {
		return CloudflareCredentials{}, errors.New("the saved Cloudflare credentials hold neither an API token nor an e-mail and global key")
	}
	return out, nil
}
