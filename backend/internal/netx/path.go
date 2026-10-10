package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sort"
	"strings"
)

// Path is how the kernel answers one address: the device the reply leaves
// through, the gateway it is handed to, and the address it is sent from.
//
// For the address a request came from, this is the dashboard's own lifeline.
// Every guard compares a change against it, and a route or rule is checked
// against it again after it is applied, because which route wins is the
// kernel's decision and only asking the kernel is certain.
type Path struct {
	Address string `json:"address"`
	Device  string `json:"device,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	Source  string `json:"source,omitempty"`
	// Table is the table the kernel says answered, when it names one;
	// ip omits it for main.
	Table string `json:"table,omitempty"`
	// Local is a client on this machine itself (a local process, or an SSH
	// tunnel whose session could not be found), whose path no network change
	// can take away.
	Local bool `json:"local,omitempty"`
	// anchors are how this server itself reaches the internet, read with
	// the client's path: every guard that compares the one compares the
	// other, because the operator's way in rides on them too — tailscaled's
	// own packets, a WireGuard tunnel's endpoint, the SSH session behind a
	// tunnel to loopback.
	anchors []anchorPath
	// replyUIDs are the owners of the sockets answering the client, read
	// only when a UID-selecting discard rule needs them.
	replyUIDs []uint32
}

// anchorPath is the kernel's answer for one fixed address, as plainly and as
// tailscaled's marked packets would ask it, or for one WireGuard peer's
// endpoint as that interface's own socket asks it.
type anchorPath struct {
	label string
	args  []string
	path  Path
	// tunnels are the WireGuard devices of a transport anchor: its route may
	// move between native devices, but never into one of them.
	tunnels map[string]bool
}

// routeGet is one entry of `ip -j route get`.
type routeGet struct {
	Dst      string   `json:"dst"`
	Gateway  string   `json:"gateway"`
	Dev      string   `json:"dev"`
	PrefSrc  string   `json:"prefsrc"`
	Type     string   `json:"type"`
	Flags    []string `json:"flags"`
	Uid      int      `json:"uid"`
	Cache    []string `json:"cache"`
	Protocol string   `json:"protocol"`
	Table    ipTable  `json:"table"`
}

// ClientPath resolves the way back to a client address. An address that does
// not parse — no client, or a proxy that sent a name — yields an empty path,
// which the guards treat as "nothing known to protect" only for changes that
// cannot reach the uplink; the uplink itself is always protected.
func (s *Service) ClientPath(ctx context.Context, client string) (Path, error) {
	return clientPath(ctx, client)
}

func clientPath(ctx context.Context, client string) (Path, error) {
	addr, err := ParseAddr(client)
	if err != nil {
		return Path{anchors: anchorPaths(ctx)}, nil
	}
	p := Path{Address: addr.String(), anchors: anchorPaths(ctx)}
	if addr.IsLoopback() {
		p.Local, p.Device = true, "lo"
		return p, nil
	}
	args := []string{"-j"}
	if addr.Is6() {
		args = append(args, "-6")
	}
	args = append(args, "route", "get", addr.String())
	out, err := run(ctx, "ip", args...)
	if err != nil {
		return p, fmt.Errorf("the route back to %s could not be read: %w", addr, err)
	}
	return parseRouteGet(out, p)
}

func parseRouteGet(out string, p Path) (Path, error) {
	var got []routeGet
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) == 0 {
		return p, fmt.Errorf("ip route get printed something unreadable")
	}
	r := got[0]
	if r.Type == "local" || r.Dev == "lo" {
		p.Local, p.Device = true, "lo"
		return p, nil
	}
	p.Device, p.Gateway, p.Source, p.Table = r.Dev, r.Gateway, r.PrefSrc, string(r.Table)
	return p, nil
}

// samePath reports whether a change left the way back where it was. The
// source may change (a second address on the same device) without the reply
// going anywhere else, so only the device and the gateway are compared.
func samePath(before, after Path) bool {
	return before.Local == after.Local && before.Device == after.Device && before.Gateway == after.Gateway
}

// verifyPath is the verify half of a route or rule change: resolve the client
// and the anchors again and refuse what moved either.
func verifyPath(before Path) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		if err := verifyAnchors(ctx, before.anchors); err != nil {
			return err
		}
		if before.Address == "" || before.Local {
			return nil
		}
		after, err := clientPath(ctx, before.Address)
		if err != nil {
			return err
		}
		if !samePath(before, after) {
			return guarded("this would send the reply to your connection (%s) %s instead of %s, so it was put back",
				before.Address, describePath(after), describePath(before))
		}
		return nil
	}
}

// The addresses this server's own way out is read against: one public
// resolver per family, never contacted — `ip route get` only asks the kernel.
// Tailscale marks its own packets 0x80000 and looks them up in the main table
// past its rules, so a route that moves them takes the tailnet down while the
// browser's tailnet address still resolves to tailscale0.
var anchorTargets = []anchorTarget{
	{label: "the internet", args: []string{"-j", "route", "get", "1.1.1.1"}},
	{label: "the internet for Tailscale's own packets", args: []string{"-j", "route", "get", "1.1.1.1", "mark", "0x80000"}},
	{label: "the internet over IPv6", args: []string{"-j", "-6", "route", "get", "2606:4700:4700::1111"}},
}

// anchorTarget is one kernel question an anchor is read with.
type anchorTarget struct {
	label string
	args  []string
}

// maxTunnelAnchors bounds the tunnel endpoints read beside every change.
const maxTunnelAnchors = 16

// tunnelAnchorTargets are the Tailscale peers' direct addresses, asked with
// tailscaled's mark: a route that moves one breaks that path while every fixed
// anchor still reads the same. WireGuard transports are anchored by
// wgTransportAnchors, which lets them move between native devices but never
// into a tunnel. Endpoints are read when the change starts; a roaming peer is
// checked at the address it had then. A variable so tests about something
// else can leave the host's tunnels out.
var tunnelAnchorTargets = readTunnelAnchorTargets

func readTunnelAnchorTargets(ctx context.Context) []anchorTarget {
	var out []anchorTarget
	seen := map[string]bool{}
	add := func(label, ip, mark string) {
		addr, err := netip.ParseAddr(strings.Trim(ip, "[]"))
		if err != nil || addr.IsLoopback() || addr.IsUnspecified() || seen[addr.String()+"|"+mark] {
			return
		}
		addr = addr.WithZone("")
		seen[addr.String()+"|"+mark] = true
		args := []string{"-j"}
		if addr.Is6() && !addr.Is4In6() {
			args = append(args, "-6")
		}
		args = append(args, "route", "get", addr.Unmap().String())
		if mark != "" {
			args = append(args, "mark", mark)
		}
		out = append(out, anchorTarget{label: label, args: args})
	}
	if has("tailscale") {
		if raw, err := run(ctx, "tailscale", "status", "--json"); err == nil {
			var st tsStatusJSON
			if json.Unmarshal([]byte(raw), &st) == nil {
				keys := make([]string, 0, len(st.Peer))
				for key := range st.Peer {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					peer := st.Peer[key]
					if peer == nil || peer.CurAddr == "" {
						continue
					}
					if host, _, err := net.SplitHostPort(peer.CurAddr); err == nil {
						add(fmt.Sprintf("Tailscale's direct path to %s (%s)", firstNonEmpty(peer.HostName, "a peer"), host), host, "0x80000")
					}
				}
			}
		}
	}
	if len(out) > maxTunnelAnchors {
		out = out[:maxTunnelAnchors]
	}
	return out
}

// anchorPaths reads every anchor that has a route now; a family with no
// default route has none and is not compared. A variable so the recorder can
// leave it out of tests about something else.
var anchorPaths = func(ctx context.Context) []anchorPath {
	var out []anchorPath
	for _, a := range append(append([]anchorTarget(nil), anchorTargets...), tunnelAnchorTargets(ctx)...) {
		if p, ok := readAnchor(ctx, a); ok {
			out = append(out, anchorPath{label: a.label, args: a.args, path: p})
		}
	}
	return append(out, wgTransportAnchors(ctx)...)
}

// wgTransportAnchors are the endpoints WireGuard peers are dialled at or were
// last seen from, read from the kernel rather than the files: a host name is
// already resolved there and a roaming phone is where it is now. A change
// that would route one of them into a tunnel cuts that peer off, the operator
// included when they arrive over WireGuard, so every guarded change compares
// them as it compares the internet anchors. A transport already captured is
// not anchored: moving it back out is the repair.
func wgTransportAnchors(ctx context.Context) []anchorPath {
	if !has("wg") {
		return nil
	}
	out, err := run(ctx, "wg", "show", "all", "dump")
	if err != nil {
		return nil
	}
	live, err := wgCheckedDump(out)
	if err != nil {
		return nil
	}
	tunnels := map[string]bool{}
	for name := range live {
		tunnels[name] = true
	}
	var anchors []anchorPath
	for _, t := range wgTransportTargets(live) {
		args := wgTransportArgs(t)
		raw, err := run(ctx, "ip", args...)
		if err != nil {
			continue
		}
		p, err := parseRouteGet(raw, Path{Address: t.addr.String()})
		if err != nil || tunnels[p.Device] {
			continue
		}
		anchors = append(anchors, anchorPath{
			label: fmt.Sprintf("the WireGuard transport of %s to %s", t.ref.iface, t.addr), args: args, path: p, tunnels: tunnels,
		})
	}
	return anchors
}

func readAnchor(ctx context.Context, a anchorTarget) (Path, bool) {
	raw, err := run(ctx, "ip", a.args...)
	if err != nil {
		return Path{}, false
	}
	target := a.args[slices.Index(a.args, "get")+1]
	p, err := parseRouteGet(raw, Path{Address: target})
	return p, err == nil
}

// verifyAnchors refuses a change that moved how this server reaches the
// internet or a Tailscale path, or left it with no way at all, and one that
// routed a WireGuard transport into a tunnel or nowhere. It asks the kernel
// exactly the questions the anchors were read with, so an endpoint that roams
// during the change cannot turn into a missing anchor.
func verifyAnchors(ctx context.Context, before []anchorPath) error {
	for _, a := range before {
		after, ok := readAnchor(ctx, anchorTarget{label: a.label, args: a.args})
		if !ok {
			return guarded("this would leave this server with no route to %s, which your connection and every outbound one ride on, so it was put back", a.label)
		}
		if a.tunnels != nil {
			if a.tunnels[after.Device] {
				return guarded("this would route %s into %s, so the tunnel would carry its own transport and its peer would be cut off; it was put back", a.label, after.Device)
			}
			continue
		}
		if !samePath(a.path, after) {
			return guarded("this would move how this server reaches %s (%s instead of %s), which your connection and every outbound one ride on, so it was put back",
				a.label, describePath(after), describePath(a.path))
		}
	}
	return nil
}

func describePath(p Path) string {
	switch {
	case p.Device == "":
		return "nowhere"
	case p.Gateway != "":
		return fmt.Sprintf("through %s via %s", p.Device, p.Gateway)
	default:
		return "through " + p.Device
	}
}

// operatorRanges keeps the allowlist ranges narrow enough to mean "the
// operator's networks". An install open to everything has 0.0.0.0/0 in its
// allowlist, and trusting that would make every blocklist match nothing; a
// tailnet (/10) or an office (/24) is what the guard is for.
func operatorRanges(allowlist []string) []netip.Prefix {
	var out []netip.Prefix
	for _, raw := range allowlist {
		p, err := ParsePrefix(raw)
		if err != nil {
			continue
		}
		if (p.Addr().Is4() && p.Bits() < 8) || (p.Addr().Is6() && p.Bits() < 16) {
			continue
		}
		out = append(out, p.Masked())
	}
	return out
}

// loopbackRanges are never dropped, whatever the lists say.
var loopbackRanges = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("::1/128"),
}

// trustedFor is the set the gateway table returns early for: loopback, the
// operator's allowlist ranges and the addresses the spec keeps without an
// expiry. An expiring kept address is a rule of its own (allowRules), and is
// not trusted for the guards: it stops protecting anyone when it expires.
func (s *Service) trustedFor(sp *Spec) []netip.Prefix {
	out := append([]netip.Prefix{}, loopbackRanges...)
	out = append(out, s.trustedRanges...)
	expiring := expiringTrusted(sp)
	for _, raw := range sp.Trusted {
		if p, err := ParsePrefix(raw); err == nil {
			if _, ends := expiring[p.Masked().String()]; !ends {
				out = append(out, p.Masked())
			}
		}
	}
	return mergePrefixes(out)
}

// isTrusted reports whether an address is in the trusted set.
func (s *Service) isTrusted(sp *Spec, addr netip.Addr) bool {
	for _, p := range s.trustedFor(sp) {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// trustClient adds the requesting address to the spec's trusted list when it
// is not already covered, so the first protection entry an operator makes
// cannot be the one that refuses them. Returns whether it added one.
func (s *Service) trustClient(sp *Spec, client string) bool {
	return s.trustClientBy(sp, client, "", "")
}

// trustClientBy is trustClient recording who the address was kept for and
// why. A kept address that was set to expire becomes permanent again rather
// than being added twice.
func (s *Service) trustClientBy(sp *Spec, client, actor, reason string) bool {
	addr, err := ParseAddr(client)
	if err != nil || addr.IsLoopback() || s.isTrusted(sp, addr) {
		return false
	}
	key := netip.PrefixFrom(addr, addr.BitLen()).String()
	if reason == "" {
		reason = "Kept for the operator's address when a protection entry was saved from it."
	}
	if _, ends := expiringTrusted(sp)[key]; ends {
		recordTrustedNote(sp, key, actor, reason)
		return true
	}
	sp.Trusted = append(sp.Trusted, key)
	recordTrustedNote(sp, key, actor, reason)
	return true
}

// mergePrefixes sorts, de-duplicates and drops networks another one holds,
// so a set never has to be told the same thing twice (nft refuses
// overlapping intervals without auto-merge, and reads better without them).
func mergePrefixes(in []netip.Prefix) []netip.Prefix {
	ps := make([]netip.Prefix, 0, len(in))
	for _, p := range in {
		if p.IsValid() {
			ps = append(ps, p.Masked())
		}
	}
	sort.Slice(ps, func(i, j int) bool {
		a, b := ps[i], ps[j]
		if a.Addr().Is4() != b.Addr().Is4() {
			return a.Addr().Is4()
		}
		if c := a.Addr().Compare(b.Addr()); c != 0 {
			return c < 0
		}
		return a.Bits() < b.Bits()
	})
	var out []netip.Prefix
	for _, p := range ps {
		if n := len(out); n > 0 {
			last := out[n-1]
			if last.Bits() <= p.Bits() && last.Contains(p.Addr()) {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// splitFamilies separates v4 networks from v6 for nft's per-family sets.
func splitFamilies(ps []netip.Prefix) (v4, v6 []netip.Prefix) {
	for _, p := range ps {
		if p.Addr().Is4() {
			v4 = append(v4, p)
		} else {
			v6 = append(v6, p)
		}
	}
	return v4, v6
}

// prefixList renders networks as nft set elements.
func prefixList(ps []netip.Prefix) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		if p.Bits() == p.Addr().BitLen() {
			parts[i] = p.Addr().String()
		} else {
			parts[i] = p.String()
		}
	}
	return strings.Join(parts, ", ")
}
