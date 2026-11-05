package hublink

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// tailnetRange is 100.64.0.0/10, where tailnet addresses are. The dashboard
// never dials a hub anywhere else, so link traffic stays on the tailnet.
var tailnetRange = netip.MustParsePrefix("100.64.0.0/10")

// hostTailnet reads `tailscale status --json` on the host, the call
// selfcfg.DetectTailscale makes: tailscaled's socket is the host's and this
// image carries no Tailscale client.
type hostTailnet struct{}

func (hostTailnet) Status(ctx context.Context) (TailnetStatus, error) {
	if !hostexec.Available("tailscale") {
		return TailnetStatus{}, errors.New("Tailscale is not installed on this host")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := hostexec.CommandOnHost(ctx, "tailscale", "status", "--json").Output()
	if err != nil {
		return TailnetStatus{}, fmt.Errorf("Tailscale did not answer: %w", err)
	}
	return parseTailnetStatus(out)
}

type wireTailnetStatus struct {
	BackendState   string `json:"BackendState"`
	MagicDNSSuffix string `json:"MagicDNSSuffix"`
	CurrentTailnet *struct {
		MagicDNSSuffix string `json:"MagicDNSSuffix"`
	} `json:"CurrentTailnet"`
	Self *struct {
		DNSName string `json:"DNSName"`
	} `json:"Self"`
	Peer map[string]struct {
		DNSName      string   `json:"DNSName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
	} `json:"Peer"`
}

// parseTailnetStatus is the status without the subprocess, so reading fields
// out of somebody else's JSON is tested against a fixture.
func parseTailnetStatus(raw []byte) (TailnetStatus, error) {
	var w wireTailnetStatus
	if err := json.Unmarshal(raw, &w); err != nil {
		return TailnetStatus{}, fmt.Errorf("Tailscale's status could not be read: %w", err)
	}
	st := TailnetStatus{Running: w.BackendState == "Running", Peers: map[string]netip.Addr{}}
	if w.Self != nil {
		st.Self = trimName(w.Self.DNSName)
	}
	switch {
	case w.CurrentTailnet != nil && trimName(w.CurrentTailnet.MagicDNSSuffix) != "":
		st.Suffix = trimName(w.CurrentTailnet.MagicDNSSuffix)
	case trimName(w.MagicDNSSuffix) != "":
		st.Suffix = trimName(w.MagicDNSSuffix)
	default:
		if _, rest, ok := strings.Cut(st.Self, "."); ok {
			st.Suffix = rest
		}
	}
	for _, p := range w.Peer {
		name := trimName(p.DNSName)
		if name == "" {
			continue
		}
		for _, a := range p.TailscaleIPs {
			if ip, err := netip.ParseAddr(a); err == nil && ip.Is4() && tailnetRange.Contains(ip) {
				st.Peers[name] = ip
				break
			}
		}
	}
	return st, nil
}

func trimName(s string) string { return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".") }

// dialer finds the address to dial for a hub (protocol 2.2, "Reaching the
// hub"): what the system resolver says, if that is a tailnet address, or else
// what `tailscale status` lists for the peer of that name. Whether the host
// resolves MagicDNS names depends on how its resolver is set up, which is why
// there is a second way. Either way TLS verifies the certificate for the name,
// and the request's Host is the name.
type dialer struct {
	tailnet    Tailnet
	testOrigin string
	lookup     func(ctx context.Context, host string) ([]netip.Addr, error)
	net        net.Dialer

	mu     sync.Mutex
	cached map[string]cachedPeer
}

type cachedPeer struct {
	addr netip.Addr
	at   time.Time
}

// peerCacheFor is how long a peer address from `tailscale status` is reused.
const peerCacheFor = time.Minute

func newDialer(tn Tailnet, testOrigin string) *dialer {
	return &dialer{
		tailnet: tn, testOrigin: testOrigin,
		lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
		},
		net:    net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second},
		cached: map[string]cachedPeer{},
	}
}

// resolve is the tailnet address of host, or an error when it has none.
func (d *dialer) resolve(ctx context.Context, host string) (netip.Addr, error) {
	host = trimName(host)
	if addrs, err := d.lookup(ctx, host); err == nil {
		for _, a := range addrs {
			if a = a.Unmap(); tailnetRange.Contains(a) {
				return a, nil
			}
		}
	}
	d.mu.Lock()
	c, ok := d.cached[host]
	d.mu.Unlock()
	if ok && time.Since(c.at) < peerCacheFor {
		return c.addr, nil
	}
	st, err := d.tailnet.Status(ctx)
	if err == nil {
		if a, ok := st.Peers[host]; ok && tailnetRange.Contains(a) {
			d.mu.Lock()
			d.cached[host] = cachedPeer{a, time.Now()}
			d.mu.Unlock()
			return a, nil
		}
	}
	return netip.Addr{}, fmt.Errorf("%s has no address on this server's tailnet", host)
}

func (d *dialer) forget(host string) {
	d.mu.Lock()
	delete(d.cached, trimName(host))
	d.mu.Unlock()
}

func (d *dialer) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	// A hub at the test origin is dialled as written: it is a loopback
	// address, and nothing else is let through here.
	if d.testOrigin != "" && strings.EqualFold(address, hostPortOf(d.testOrigin)) {
		return d.net.DialContext(ctx, network, address)
	}
	addr, err := d.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	if !tailnetRange.Contains(addr) {
		return nil, fmt.Errorf("%s resolves to %s, which is not a tailnet address", host, addr)
	}
	conn, err := d.net.DialContext(ctx, "tcp4", net.JoinHostPort(addr.String(), port))
	if err != nil {
		// A peer that moved is looked up again on the next attempt.
		d.forget(host)
	}
	return conn, err
}

// hostPortOf is the host:port a transport dials for an origin.
func hostPortOf(origin string) string {
	scheme, rest, _ := strings.Cut(origin, "://")
	if _, _, err := net.SplitHostPort(rest); err == nil {
		return rest
	}
	if scheme == "http" {
		return rest + ":80"
	}
	return rest + ":443"
}

// newHTTPClient is the transport every exchange with a hub uses: TLS 1.2 at
// least against the system roots, no proxy (the hub is on the tailnet, and a
// proxy would take the traffic off it), the tailnet dialer, and a refusal of
// any redirect.
func newHTTPClient(d *dialer) *http.Client {
	t := &http.Transport{
		Proxy:                 nil,
		DialContext:           d.dial,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Transport: t,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("the hub redirected a link request, which is never followed")
		},
	}
}
