package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
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
	// Local is a client on this machine itself (an SSH tunnel to loopback),
	// whose path no network change can take away.
	Local bool `json:"local,omitempty"`
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
		return Path{}, nil
	}
	p := Path{Address: addr.String()}
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
	p.Device, p.Gateway, p.Source = r.Dev, r.Gateway, r.PrefSrc
	return p, nil
}

// samePath reports whether a change left the way back where it was. The
// source may change (a second address on the same device) without the reply
// going anywhere else, so only the device and the gateway are compared.
func samePath(before, after Path) bool {
	return before.Local == after.Local && before.Device == after.Device && before.Gateway == after.Gateway
}

// verifyPath is the verify half of a route or rule change: resolve the client
// again and refuse what moved it.
func verifyPath(before Path) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		if before.Address == "" || before.Local {
			return nil
		}
		after, err := clientPath(ctx, before.Address)
		if err != nil {
			return err
		}
		if !samePath(before, after) {
			return guarded("this would send the reply to your browser (%s) %s instead of %s, so it was put back",
				before.Address, describePath(after), describePath(before))
		}
		return nil
	}
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
// operator's allowlist ranges and the addresses the spec keeps.
func (s *Service) trustedFor(sp *Spec) []netip.Prefix {
	out := append([]netip.Prefix{}, loopbackRanges...)
	out = append(out, s.trustedRanges...)
	for _, raw := range sp.Trusted {
		if p, err := ParsePrefix(raw); err == nil {
			out = append(out, p.Masked())
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
	addr, err := ParseAddr(client)
	if err != nil || addr.IsLoopback() || s.isTrusted(sp, addr) {
		return false
	}
	sp.Trusted = append(sp.Trusted, netip.PrefixFrom(addr, addr.BitLen()).String())
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
