package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Tailscale is how the operator very likely reaches this dashboard, so the
// whole of what is done to it is one command, `tailscale set`, with two
// flags, and neither of them can take this machine off the tailnet: what is
// changed is what the server *offers* the other devices — an exit node and
// subnet routes. `down`, `logout`, `up`, `serve` and `funnel` are never run
// from here, and nothing here sets `--exit-node` (using one would send this
// server's own replies, including to the browser, through somebody else).

// tsTailnetV4 and tsTailnetV6 are the ranges Tailscale hands out addresses from.
var (
	tsTailnetV4 = netip.MustParsePrefix("100.64.0.0/10")
	tsTailnetV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// ErrForwardingOff is a Tailscale offer that cannot work because the kernel
// is not forwarding packets. The handler turns it into forwarding_off.
var ErrForwardingOff = errors.New("forwarding is off")

// TailscaleView is the Tailscale half of the VPN page.
type TailscaleView struct {
	Installed    bool   `json:"installed"`
	Running      bool   `json:"running"`
	BackendState string `json:"backendState"`
	Version      string `json:"version"`
	// AuthURL is the address the device is waiting to be logged in at, when
	// it is waiting.
	AuthURL        string   `json:"authUrl"`
	Self           *TSSelf  `json:"self"`
	MagicDNSSuffix string   `json:"magicDnsSuffix"`
	Tailnet        string   `json:"tailnet"`
	Health         []string `json:"health"`
	Peers          []TSPeer `json:"peers"`
	Prefs          TSPrefs  `json:"prefs"`
	ControlServer  string   `json:"controlServer"`
	ControlURL     string   `json:"controlUrl"`
	// ClientOnTailnet is the dashboard's reader arriving through the tailnet,
	// which is what makes everything here something that must not be broken.
	ClientOnTailnet bool         `json:"clientOnTailnet"`
	Forwarding      TSForwarding `json:"forwarding"`
	Warnings        []string     `json:"warnings"`
	Error           string       `json:"error,omitempty"`
}

// TSForwarding is the kernel's forwarding, which an exit node and subnet
// routes both need.
type TSForwarding struct {
	IPv4 bool `json:"ipv4"`
	IPv6 bool `json:"ipv6"`
}

// TSSelf is this server on the tailnet.
type TSSelf struct {
	HostName     string   `json:"hostName"`
	DNSName      string   `json:"dnsName"`
	TailscaleIPs []string `json:"tailscaleIps"`
	OS           string   `json:"os"`
	Online       bool     `json:"online"`
	// ExitNodeOption is the tailnet having approved this server as an exit
	// node. Advertising it (Prefs) is asking; this is being allowed.
	ExitNodeOption bool `json:"exitNodeOption"`
	// PrimaryRoutes are the subnets the tailnet routes to this server.
	PrimaryRoutes []string `json:"primaryRoutes"`
	Relay         string   `json:"relay"`
}

// TSPeer is another device on the tailnet.
type TSPeer struct {
	ID           string   `json:"id"`
	HostName     string   `json:"hostName"`
	DNSName      string   `json:"dnsName"`
	OS           string   `json:"os"`
	TailscaleIPs []string `json:"tailscaleIps"`
	Online       bool     `json:"online"`
	Active       bool     `json:"active"`
	// ExitNode is this server using the peer as its exit node.
	ExitNode       bool `json:"exitNode"`
	ExitNodeOption bool `json:"exitNodeOption"`
	// Relay is the DERP region a relayed connection goes through.
	Relay string `json:"relay"`
	// Direct is a peer-to-peer path, not a relayed one.
	Direct        bool     `json:"direct"`
	CurAddr       string   `json:"curAddr"`
	RxBytes       uint64   `json:"rxBytes"`
	TxBytes       uint64   `json:"txBytes"`
	LastSeen      int64    `json:"lastSeen"`
	LastHandshake int64    `json:"lastHandshake"`
	PrimaryRoutes []string `json:"primaryRoutes"`
	Tags          []string `json:"tags"`
	UserLoginName string   `json:"userLoginName"`
	Expired       bool     `json:"expired"`
}

// TSPrefs are the settings that decide what this server does for the tailnet.
type TSPrefs struct {
	// AdvertiseRoutes are the subnets offered, without the default-route pair
	// that means "exit node".
	AdvertiseRoutes     []string `json:"advertiseRoutes"`
	AdvertisingExitNode bool     `json:"advertisingExitNode"`
	// UsingExitNode is this server sending its own traffic through another
	// device. Worth a warning: it moves the replies to the internet.
	UsingExitNode bool   `json:"usingExitNode"`
	ExitNodeID    string `json:"exitNodeId"`
	AcceptRoutes  bool   `json:"routeAll"`
	AcceptDNS     bool   `json:"corpDns"`
	ShieldsUp     bool   `json:"shieldsUp"`
}

type tsNodeJSON struct {
	ID             string    `json:"ID"`
	HostName       string    `json:"HostName"`
	DNSName        string    `json:"DNSName"`
	OS             string    `json:"OS"`
	UserID         int64     `json:"UserID"`
	TailscaleIPs   []string  `json:"TailscaleIPs"`
	CurAddr        string    `json:"CurAddr"`
	Relay          string    `json:"Relay"`
	RxBytes        uint64    `json:"RxBytes"`
	TxBytes        uint64    `json:"TxBytes"`
	LastSeen       time.Time `json:"LastSeen"`
	LastHandshake  time.Time `json:"LastHandshake"`
	Online         bool      `json:"Online"`
	Active         bool      `json:"Active"`
	ExitNode       bool      `json:"ExitNode"`
	ExitNodeOption bool      `json:"ExitNodeOption"`
	Expired        bool      `json:"Expired"`
	PrimaryRoutes  []string  `json:"PrimaryRoutes"`
	Tags           []string  `json:"Tags"`
}

type tsStatusJSON struct {
	Version        string                 `json:"Version"`
	BackendState   string                 `json:"BackendState"`
	AuthURL        string                 `json:"AuthURL"`
	Self           *tsNodeJSON            `json:"Self"`
	Health         []string               `json:"Health"`
	MagicDNSSuffix string                 `json:"MagicDNSSuffix"`
	CurrentTailnet *struct{ Name string } `json:"CurrentTailnet"`
	Peer           map[string]*tsNodeJSON `json:"Peer"`
	User           map[string]struct {
		LoginName string `json:"LoginName"`
	} `json:"User"`
}

// tsPrefsJSON names only what is read. `tailscale debug prefs` also prints
// the node's private key and the network-lock key; decoding into a struct
// that has no field for them is what keeps them out of every response.
type tsPrefsJSON struct {
	ControlURL      string   `json:"ControlURL"`
	RouteAll        bool     `json:"RouteAll"`
	ExitNodeID      string   `json:"ExitNodeID"`
	ExitNodeIP      string   `json:"ExitNodeIP"`
	CorpDNS         bool     `json:"CorpDNS"`
	ShieldsUp       bool     `json:"ShieldsUp"`
	AdvertiseRoutes []string `json:"AdvertiseRoutes"`
}

// Tailscale reads the tailnet as this server sees it. client is the address
// of the person asking, to say whether they arrive through it.
func (s *Service) Tailscale(ctx context.Context, client string) (*TailscaleView, error) {
	v := &TailscaleView{
		Health: []string{}, Peers: []TSPeer{}, Warnings: []string{},
		Prefs: TSPrefs{AdvertiseRoutes: []string{}},
	}
	if !has("tailscale") {
		return v, nil
	}
	v.Installed = true
	out, err := run(ctx, "tailscale", "status", "--json")
	if err != nil {
		return v, err
	}
	v, err = parseTailscaleStatus(out, v)
	if err != nil {
		return v, err
	}
	if pout, err := run(ctx, "tailscale", "debug", "prefs"); err != nil {
		v.Warnings = append(v.Warnings, "The preferences could not be read: "+err.Error())
	} else if err := tsApplyPrefs(pout, v); err != nil {
		v.Warnings = append(v.Warnings, "The preferences could not be read: "+err.Error())
	}
	v.Forwarding = TSForwarding{IPv4: wgIPForwarding("ipv4"), IPv6: wgIPForwarding("ipv6")}
	v.ClientOnTailnet = tsClientOnTailnet(client, v)
	if v.Prefs.UsingExitNode {
		v.Warnings = append(v.Warnings, "This server sends its own internet traffic through an exit node, so replies to visitors may leave by another route than they arrived on.")
	}
	if v.Prefs.ShieldsUp {
		v.Warnings = append(v.Warnings, "Shields are up: this server refuses every connection arriving over the tailnet.")
	}
	return v, nil
}

func parseTailscaleStatus(out string, v *TailscaleView) (*TailscaleView, error) {
	var st tsStatusJSON
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		return v, fmt.Errorf("tailscale status printed something unreadable")
	}
	v.BackendState = st.BackendState
	v.Running = st.BackendState == "Running"
	v.Version = st.Version
	v.AuthURL = st.AuthURL
	v.MagicDNSSuffix = st.MagicDNSSuffix
	if st.CurrentTailnet != nil {
		v.Tailnet = st.CurrentTailnet.Name
	}
	v.Health = append(v.Health, st.Health...)
	if st.Self != nil {
		v.Self = &TSSelf{
			HostName: st.Self.HostName, DNSName: st.Self.DNSName, OS: st.Self.OS,
			TailscaleIPs: vpnNonNil(st.Self.TailscaleIPs), Online: st.Self.Online,
			ExitNodeOption: st.Self.ExitNodeOption, PrimaryRoutes: vpnNonNil(st.Self.PrimaryRoutes),
			Relay: st.Self.Relay,
		}
	}
	for _, n := range st.Peer {
		if n == nil {
			continue
		}
		p := TSPeer{
			ID: n.ID, HostName: n.HostName, DNSName: n.DNSName, OS: n.OS,
			TailscaleIPs: vpnNonNil(n.TailscaleIPs), Online: n.Online, Active: n.Active,
			ExitNode: n.ExitNode, ExitNodeOption: n.ExitNodeOption, Relay: n.Relay,
			Direct: n.CurAddr != "", CurAddr: n.CurAddr, RxBytes: n.RxBytes, TxBytes: n.TxBytes,
			LastSeen: vpnUnixOrZero(n.LastSeen), LastHandshake: vpnUnixOrZero(n.LastHandshake),
			PrimaryRoutes: vpnNonNil(n.PrimaryRoutes), Tags: vpnNonNil(n.Tags), Expired: n.Expired,
		}
		if u, ok := st.User[fmt.Sprint(n.UserID)]; ok {
			p.UserLoginName = u.LoginName
		}
		v.Peers = append(v.Peers, p)
	}
	sort.Slice(v.Peers, func(i, j int) bool {
		a, b := v.Peers[i], v.Peers[j]
		if a.Online != b.Online {
			return a.Online
		}
		if a.HostName != b.HostName {
			return a.HostName < b.HostName
		}
		return a.ID < b.ID
	})
	return v, nil
}

func vpnNonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func vpnUnixOrZero(t time.Time) int64 {
	if t.IsZero() || t.Unix() <= 0 {
		return 0
	}
	return t.Unix()
}

func tsReadPrefs(ctx context.Context) (tsPrefsJSON, error) {
	var p tsPrefsJSON
	out, err := run(ctx, "tailscale", "debug", "prefs")
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		return p, fmt.Errorf("tailscale debug prefs printed something unreadable")
	}
	return p, nil
}

func tsApplyPrefs(out string, v *TailscaleView) error {
	var p tsPrefsJSON
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		return errors.New("the preferences printed something unreadable")
	}
	v.Prefs.AdvertiseRoutes, v.Prefs.AdvertisingExitNode = tsSplitAdvertised(p.AdvertiseRoutes)
	v.Prefs.ExitNodeID = p.ExitNodeID
	v.Prefs.UsingExitNode = p.ExitNodeID != "" || p.ExitNodeIP != ""
	v.Prefs.AcceptRoutes, v.Prefs.AcceptDNS, v.Prefs.ShieldsUp = p.RouteAll, p.CorpDNS, p.ShieldsUp
	v.ControlURL = p.ControlURL
	v.ControlServer = tsControlServerOf(p.ControlURL)
	return nil
}

// tsSplitAdvertised separates the exit-node pair (0.0.0.0/0 and ::/0, how
// Tailscale stores "offer to be an exit node") from the subnets.
func tsSplitAdvertised(routes []string) (subnets []string, exit bool) {
	subnets = []string{}
	for _, r := range routes {
		p, err := netip.ParsePrefix(r)
		if err == nil && p.Bits() == 0 {
			exit = true
			continue
		}
		subnets = append(subnets, r)
	}
	return subnets, exit
}

// tsControlServerOf says whose coordination server the node talks to: Tailscale's
// own, or one somebody runs (Headscale).
func tsControlServerOf(raw string) string {
	if raw == "" {
		return "tailscale"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "self-hosted"
	}
	host := strings.ToLower(u.Hostname())
	if host == "tailscale.com" || strings.HasSuffix(host, ".tailscale.com") {
		return "tailscale"
	}
	return "self-hosted"
}

// tsClientOnTailnet is the requester's address being on the tailnet: one of
// the devices it knows, or in the ranges Tailscale draws addresses from.
func tsClientOnTailnet(client string, v *TailscaleView) bool {
	addr, err := ParseAddr(client)
	if err != nil {
		return false
	}
	if tsTailnetV4.Contains(addr) || tsTailnetV6.Contains(addr) {
		return true
	}
	known := func(ips []string) bool {
		for _, ip := range ips {
			if a, err := netip.ParseAddr(ip); err == nil && a == addr {
				return true
			}
		}
		return false
	}
	if v.Self != nil && known(v.Self.TailscaleIPs) {
		return true
	}
	for _, p := range v.Peers {
		if known(p.TailscaleIPs) {
			return true
		}
	}
	return false
}

// TailscaleNeedsForwarding says what this server offers the tailnet that only
// works while the kernel forwards, for the Routing page to refuse turning
// forwarding off under it.
func (s *Service) TailscaleNeedsForwarding(ctx context.Context) (exitNode, subnetRoutes bool) {
	if !has("tailscale") {
		return false, false
	}
	p, err := tsReadPrefs(ctx)
	if err != nil {
		return false, false
	}
	routes, exit := tsSplitAdvertised(p.AdvertiseRoutes)
	return exit, len(routes) > 0
}

// TailscaleSetRequest changes what this server offers. A nil field is left
// as it is; an empty AdvertiseRoutes withdraws every subnet.
type TailscaleSetRequest struct {
	AdvertiseExitNode *bool     `json:"advertiseExitNode"`
	AdvertiseRoutes   *[]string `json:"advertiseRoutes"`
}

// TailscaleSetResult is the node after the change, and what the operator has
// to do next.
type TailscaleSetResult struct {
	Tailscale *TailscaleView `json:"tailscale"`
	Note      string         `json:"note"`
}

// ForwardingOffError is ErrForwardingOff with the family that is off.
type ForwardingOffError struct{ Family string }

func (e *ForwardingOffError) Error() string {
	return fmt.Sprintf("%s forwarding is off on this host, so what you are offering to the tailnet would reach nothing", e.Family)
}
func (e *ForwardingOffError) Unwrap() error { return ErrForwardingOff }

// tsParseAdvertiseRoutes validates the subnets to offer.
func tsParseAdvertiseRoutes(in []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	seen := map[netip.Prefix]bool{}
	for _, raw := range in {
		p, err := ParsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("advertiseRoutes: %w", err)
		}
		p = p.Masked()
		if p.Bits() == 0 {
			return nil, fmt.Errorf("advertiseRoutes: %s is every address, which is what the exit node switch offers; use that instead", p)
		}
		if tsTailnetV4.Overlaps(p) || tsTailnetV6.Overlaps(p) {
			return nil, fmt.Errorf("advertiseRoutes: %s overlaps the tailnet's own addresses", p)
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Addr().Is4() != out[j].Addr().Is4() {
			return out[i].Addr().Is4()
		}
		if c := out[i].Addr().Compare(out[j].Addr()); c != 0 {
			return c < 0
		}
		return out[i].Bits() < out[j].Bits()
	})
	return out, nil
}

// SetTailscale changes what this server offers the tailnet, with one
// `tailscale set`. Both flags are always sent: a client older than the one
// that taught `set` to keep the exit-node pair when only the routes change
// would drop the exit node while "just" changing the subnets, so the flag
// that was not asked about is sent at the value it already has.
func (s *Service) SetTailscale(ctx context.Context, req TailscaleSetRequest, client string) (*TailscaleSetResult, error) {
	if !has("tailscale") {
		return nil, &UnavailableError{Tool: "tailscale"}
	}
	if req.AdvertiseExitNode == nil && req.AdvertiseRoutes == nil {
		return nil, fmt.Errorf("nothing to change: give advertiseExitNode, advertiseRoutes or both")
	}
	var routes []netip.Prefix
	if req.AdvertiseRoutes != nil {
		var err error
		if routes, err = tsParseAdvertiseRoutes(*req.AdvertiseRoutes); err != nil {
			return nil, err
		}
	}
	current, err := tsReadPrefs(ctx)
	if err != nil {
		return nil, fmt.Errorf("the current Tailscale preferences could not be read, so nothing was changed: %w", err)
	}
	curRoutes, curExit := tsSplitAdvertised(current.AdvertiseRoutes)

	exit := curExit
	if req.AdvertiseExitNode != nil {
		exit = *req.AdvertiseExitNode
	}
	routeStrs := curRoutes
	if req.AdvertiseRoutes != nil {
		routeStrs = wgPrefixStrings(routes)
	}

	// Only an offer being made needs the kernel to forward; withdrawing one
	// must always be possible.
	if req.AdvertiseExitNode != nil && *req.AdvertiseExitNode && !wgIPForwarding("ipv4") {
		return nil, &ForwardingOffError{Family: "IPv4"}
	}
	for _, r := range routes {
		family, label := "ipv4", "IPv4"
		if r.Addr().Is6() {
			family, label = "ipv6", "IPv6"
		}
		if !wgIPForwarding(family) {
			return nil, &ForwardingOffError{Family: label}
		}
	}

	if _, err := run(ctx, "tailscale", "set",
		fmt.Sprintf("--advertise-exit-node=%t", exit),
		"--advertise-routes="+strings.Join(routeStrs, ",")); err != nil {
		return nil, fmt.Errorf("tailscale set: %w", err)
	}

	view, err := s.Tailscale(ctx, client)
	if err != nil {
		return nil, err
	}
	note := "Advertising is asking: a tailnet administrator has to approve the exit node and each subnet in the admin console before other devices can use them, unless auto-approval is configured."
	if view.ControlServer == "self-hosted" {
		note = "Advertising is asking: approve the routes on the control server (for Headscale, `headscale nodes approve-routes`) before other devices can use them."
	}
	return &TailscaleSetResult{Tailscale: view, Note: note}, nil
}
