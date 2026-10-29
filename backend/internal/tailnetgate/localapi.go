// Package tailnetgate decides who may open a deployment that exists only on
// the tailnet.
//
// Such a deployment is published the way a pull request preview is: tailscaled
// listens on the host's tailnet address and forwards one port to loopback. When
// the environment names who may open it, what tailscaled forwards to is the
// proxy rather than the container, and the proxy asks this package — through
// the dashboard's GET /api/v1/tailnet/whois — about every request before it is
// answered. The answer comes from tailscaled itself: its local API says which
// login, or which tagged machine, holds the tailnet address the request came
// from, and that is compared with the environment's list.
//
// The package is a leaf on purpose. The proxy's live tests run the real
// handler and the real local API client against a real nginx and a real Caddy,
// and proxysvc cannot import anything that imports it back.
package tailnetgate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"
)

// Peer is who tailscaled says holds a tailnet address.
type Peer struct {
	// Address is the address that was asked about.
	Address netip.Addr
	// Login is the user's login name, alice@example.com. A tagged machine
	// has no user of its own — tailscaled answers with its "tagged-devices"
	// placeholder — so its Login is empty and only its tags can admit it.
	Login string
	// Name is the user's display name, or the machine's for a tagged one.
	Name string
	// Node is the machine's MagicDNS name without the trailing dot.
	Node string
	// Tags are the machine's ACL tags, tag:ci.
	Tags []string
}

// Tagged says the address belongs to a tagged machine rather than a person.
func (p Peer) Tagged() bool { return len(p.Tags) > 0 }

var (
	// ErrNotAPeer is tailscaled answering that no device on the tailnet holds
	// the address: a request that did not come over the tailnet at all.
	ErrNotAPeer = errors.New("no device on this tailnet holds that address")
	// ErrUnavailable is tailscaled not answering, or answering something
	// that cannot be read. It is never a refusal: the gate answers 503, so a
	// daemon restarting reads as an outage rather than as "you may not".
	ErrUnavailable = errors.New("tailscaled could not be asked who that is")
)

// WhoIs is the one question the gate asks of tailscaled.
type WhoIs interface {
	WhoIs(ctx context.Context, address netip.Addr) (Peer, error)
}

// localAPIHost is the Host tailscaled's local API answers to. Its own client
// sends it, and the daemon refuses a request naming any other host, which is
// its defence against a browser reaching the socket through a rebinding name.
const localAPIHost = "local-tailscaled.sock"

// Sockets are where tailscaled listens on Linux: /var/run is a link to /run
// on every current distribution, and the older path is still where some
// packages put it.
var Sockets = []string{"/run/tailscale/tailscaled.sock", "/var/run/tailscale/tailscaled.sock"}

// LocalAPI asks tailscaled's local API over its unix socket. The socket is
// found again on every new connection rather than once, so a tailscaled
// installed or restarted after the dashboard started is reached without a
// restart of the dashboard.
type LocalAPI struct {
	client *http.Client
	base   string
}

// NewLocalAPI talks to the first of sockets that exists. hostPath maps a
// host path to one this process can open — the backend reaches the host's
// /run through PID 1's root (hostexec.HostPath); nil uses the path as it is.
func NewLocalAPI(hostPath func(string) string, sockets ...string) *LocalAPI {
	if len(sockets) == 0 {
		sockets = Sockets
	}
	if hostPath == nil {
		hostPath = func(path string) string { return path }
	}
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var last error = os.ErrNotExist
			for _, socket := range sockets {
				path := hostPath(socket)
				if _, err := os.Stat(path); err != nil {
					last = err
					continue
				}
				return dialer.DialContext(ctx, "unix", path)
			}
			return nil, last
		},
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: 3 * time.Second,
	}
	return newLocalAPI(&http.Client{Transport: transport, Timeout: 5 * time.Second}, "http://"+localAPIHost)
}

// newLocalAPI is the client over any transport, which is how the tests point
// it at an httptest server speaking the local API's shape.
func newLocalAPI(client *http.Client, base string) *LocalAPI {
	return &LocalAPI{client: client, base: strings.TrimSuffix(base, "/")}
}

// whoIsResponse is the part of tailscaled's apitype.WhoIsResponse this reads:
// GET /localapi/v0/whois?addr=<ip> answers the node and the user profile
// that holds the address, and 404 when no node does.
type whoIsResponse struct {
	Node *struct {
		Name      string   `json:"Name"`
		Tags      []string `json:"Tags"`
		Addresses []string `json:"Addresses"`
		Hostinfo  *struct {
			Hostname string `json:"Hostname"`
		} `json:"Hostinfo"`
		ComputedName string `json:"ComputedName"`
	} `json:"Node"`
	UserProfile *struct {
		LoginName   string `json:"LoginName"`
		DisplayName string `json:"DisplayName"`
	} `json:"UserProfile"`
}

// WhoIs asks tailscaled who holds address.
func (c *LocalAPI) WhoIs(ctx context.Context, address netip.Addr) (Peer, error) {
	address = address.Unmap()
	if !address.IsValid() {
		return Peer{}, ErrNotAPeer
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.base+"/localapi/v0/whois?addr="+url.QueryEscape(address.String()), nil)
	if err != nil {
		return Peer{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	request.Host = localAPIHost
	response, err := c.client.Do(request)
	if err != nil {
		return Peer{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Peer{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return Peer{}, ErrNotAPeer
	default:
		return Peer{}, fmt.Errorf("%w: the local API answered %d: %s", ErrUnavailable, response.StatusCode,
			strings.TrimSpace(string(body)))
	}
	var who whoIsResponse
	if err := json.Unmarshal(body, &who); err != nil || who.Node == nil || who.UserProfile == nil {
		return Peer{}, fmt.Errorf("%w: the local API's answer could not be read", ErrUnavailable)
	}
	peer := Peer{Address: address, Node: strings.TrimSuffix(who.Node.Name, ".")}
	for _, tag := range who.Node.Tags {
		if tag = strings.TrimSpace(tag); tag != "" {
			peer.Tags = append(peer.Tags, tag)
		}
	}
	if peer.Tagged() {
		// The "tagged-devices" profile tailscaled answers for a tagged
		// machine is a placeholder shared by every one of them; matching a
		// login against it would admit them all.
		peer.Name = firstNonEmpty(who.Node.ComputedName, hostname(who), peer.Node)
	} else {
		peer.Login = strings.TrimSpace(who.UserProfile.LoginName)
		peer.Name = firstNonEmpty(strings.TrimSpace(who.UserProfile.DisplayName), peer.Login)
	}
	if !peer.Tagged() && peer.Login == "" {
		return Peer{}, fmt.Errorf("%w: the local API named neither a login nor a tag", ErrUnavailable)
	}
	return peer, nil
}

func hostname(who whoIsResponse) string {
	if who.Node.Hostinfo != nil {
		return who.Node.Hostinfo.Hostname
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
