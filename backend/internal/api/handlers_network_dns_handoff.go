package api

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dnsservice"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
)

// The DNS page finds AdGuard Home, Pi-hole and Technitium running on the host,
// and the native DNS module connects to them; the two used to meet only in the
// operator's head. A handoff joins them: each detected server with the
// connections that already reach it — by the owned container's identity or by
// the management origin's port — and, where none does, the loopback origin a
// new connection would use. Connections and their queries, filters and clients
// stay the native module's; this only points at them. It is administrator-only
// because the connection inventory is.

// dnsServiceHandoff is one detected server and its connections.
type dnsServiceHandoff struct {
	Detected netx.Adblock `json:"detected"`
	// Engine is the native engine the detected server is: adguard, pihole or
	// technitium.
	Engine string `json:"engine"`
	// Endpoint is the management origin a new connection would use, empty
	// when there is none the dashboard may send a credential to; then
	// EndpointProblem says why.
	Endpoint        string                   `json:"endpoint,omitempty"`
	EndpointProblem string                   `json:"endpointProblem,omitempty"`
	Connections     []dnsServiceHandoffMatch `json:"connections"`
}

// dnsServiceHandoffMatch is a connection that reaches the detected server.
type dnsServiceHandoffMatch struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Management bool   `json:"management"`
	Ownership  string `json:"ownership"`
	// Match is container (the connection owns this exact container) or
	// endpoint (its management origin is the server's published web port).
	Match string `json:"match"`
}

var handoffEngines = map[string]dnsservice.Engine{"adguardhome": dnsservice.AdGuard, "pihole": dnsservice.PiHole, "technitium": dnsservice.Technitium}

func loopbackOrAny(address string) bool {
	if address == "" {
		return true
	}
	a, err := netip.ParseAddr(address)
	return err == nil && (a.IsLoopback() || a.IsUnspecified())
}

// handoffEndpoint is the origin a new connection would use. Credentials go
// over loopback HTTP or verified HTTPS only, so a web port published on some
// other address has no origin here.
func handoffEndpoint(a netx.Adblock) (string, string) {
	if a.WebPort == 0 {
		return "", "No web port is published or listening, so the dashboard has no management origin to connect to. Publish the admin port on 127.0.0.1."
	}
	if !loopbackOrAny(a.WebAddress) {
		return "", "The web port listens only on " + a.WebAddress + ". The dashboard sends credentials over loopback HTTP or verified HTTPS, so publish it on 127.0.0.1 or connect through HTTPS."
	}
	host := "127.0.0.1"
	if a.WebAddress == "::1" {
		host = "[::1]"
	}
	return "http://" + host + ":" + strconv.Itoa(a.WebPort), ""
}

// dnsServiceHandoffs joins detection with the connection inventory.
func dnsServiceHandoffs(detected []netx.Adblock, connections []dnsservice.Connection) []dnsServiceHandoff {
	out := make([]dnsServiceHandoff, 0, len(detected))
	for _, a := range detected {
		engine := handoffEngines[a.Kind]
		h := dnsServiceHandoff{Detected: a, Engine: string(engine), Connections: []dnsServiceHandoffMatch{}}
		h.Endpoint, h.EndpointProblem = handoffEndpoint(a)
		for _, c := range connections {
			if c.Engine != engine {
				continue
			}
			match := ""
			if c.ContainerID != "" && a.ContainerID != "" && (c.ContainerID == a.ContainerID || strings.HasPrefix(a.ContainerID, c.ContainerID) || strings.HasPrefix(c.ContainerID, a.ContainerID)) {
				match = "container"
			} else if u, err := url.Parse(c.Endpoint); err == nil && a.WebPort != 0 {
				host, port, splitErr := net.SplitHostPort(u.Host)
				if splitErr == nil && port == strconv.Itoa(a.WebPort) {
					if ip, err := netip.ParseAddr(host); err == nil && (ip.IsLoopback() && loopbackOrAny(a.WebAddress) || ip.String() == a.WebAddress || a.WebAddress != "" && net.ParseIP(a.WebAddress).IsUnspecified()) {
						match = "endpoint"
					}
				}
			}
			if match != "" {
				h.Connections = append(h.Connections, dnsServiceHandoffMatch{ID: c.ID, Name: c.Name, Management: c.Management, Ownership: c.Ownership, Match: match})
			}
		}
		out = append(out, h)
	}
	return out
}

func (s *Server) handleDNSServiceHandoffs(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	connections, err := s.modules.dnsServices.List(ctx)
	if err != nil {
		return mapDNSServiceError(err)
	}
	detected := s.modules.network.DetectDNSServices(ctx, s.dnsContainers(ctx))
	httpx.JSON(w, http.StatusOK, dnsServiceHandoffs(detected, connections))
	return nil
}
