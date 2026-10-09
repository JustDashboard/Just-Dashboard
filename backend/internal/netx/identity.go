package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
)

// EgressIdentity is how this server reaches the internet in one family, as
// the kernel answers it, and what that does and does not establish about the
// address the internet sees.
//
// The kernel knows the device, the gateway and the address it sends from — the
// NIC source. Whether a provider or a router in front of that NIC rewrites it
// is outside this machine: a private source is certainly translated, to an
// address this host never sees, and a public one may still sit behind a 1:1
// NAT or a provider firewall. The two are reported apart so a page never
// presents the NIC's address as a measured public identity.
type EgressIdentity struct {
	Family string `json:"family"`
	// Forwarding is the family's forwarding switch; ForwardingError says why
	// it could not be read, in which case Forwarding is meaningless.
	Forwarding      bool   `json:"forwarding"`
	ForwardingError string `json:"forwardingError,omitempty"`
	Device          string `json:"device,omitempty"`
	Gateway         string `json:"gateway,omitempty"`
	// Source is the address the kernel selects to send from.
	Source string `json:"source,omitempty"`
	// SourceScope classifies Source: public, private, shared (carrier-grade
	// NAT space), unique-local, link-local or none.
	SourceScope string `json:"sourceScope"`
	// Public is what is known about the provider-facing identity: nic (the
	// NIC's own public source; provider layers in front of it unobserved),
	// translated (a private source that something upstream rewrites),
	// unobserved (no source to judge), no_route (the family has no way out)
	// or unknown (the kernel could not be asked).
	Public string `json:"public"`
	Detail string `json:"detail"`
	Error  string `json:"error,omitempty"`
}

// The targets each family's way out is asked about; `ip route get` only asks
// the kernel and sends nothing.
var identityTargets = []struct {
	family string
	args   []string
	sysctl string
}{
	{"inet", []string{"-j", "route", "get", "1.1.1.1"}, sysctlForwardV4},
	{"inet6", []string{"-j", "-6", "route", "get", "2606:4700:4700::1111"}, sysctlForwardV6},
}

// EgressIdentities reads both families. A family whose route cannot be read
// carries its own error rather than failing the other.
func (s *Service) EgressIdentities(ctx context.Context) []EgressIdentity {
	out := make([]EgressIdentity, 0, len(identityTargets))
	for _, target := range identityTargets {
		id := EgressIdentity{Family: target.family}
		if v, err := readSysctl(target.sysctl); err != nil {
			id.ForwardingError = err.Error()
		} else {
			id.Forwarding = v == "1"
		}
		raw, err := run(ctx, "ip", target.args...)
		id = judgeIdentity(id, raw, err)
		out = append(out, id)
	}
	return out
}

// judgeIdentity turns one route answer into the family's identity. Pure.
func judgeIdentity(id EgressIdentity, raw string, err error) EgressIdentity {
	id.SourceScope = "none"
	if err != nil {
		message := strings.ToLower(err.Error() + " " + raw)
		if strings.Contains(message, "network is unreachable") || strings.Contains(message, "no route to host") {
			id.Public = "no_route"
			id.Detail = fmt.Sprintf("The kernel has no %s route to the internet, so nothing leaves in this family.", familyLabel(id.Family))
			return id
		}
		id.Public, id.Error = "unknown", err.Error()
		id.Detail = fmt.Sprintf("The %s way out could not be read; this is not evidence that the family has none.", familyLabel(id.Family))
		return id
	}
	var got []routeGet
	if json.Unmarshal([]byte(raw), &got) != nil || len(got) == 0 {
		id.Public, id.Error = "unknown", "ip route get printed something unreadable"
		id.Detail = fmt.Sprintf("The %s way out could not be read; this is not evidence that the family has none.", familyLabel(id.Family))
		return id
	}
	r := got[0]
	if r.Type == "unreachable" || r.Type == "blackhole" || r.Type == "prohibit" {
		id.Public = "no_route"
		id.Detail = fmt.Sprintf("The kernel discards %s traffic to the internet (%s route).", familyLabel(id.Family), r.Type)
		return id
	}
	id.Device, id.Gateway, id.Source = r.Dev, r.Gateway, r.PrefSrc
	addr, err := netip.ParseAddr(r.PrefSrc)
	if err != nil {
		id.Public = "unobserved"
		id.Detail = fmt.Sprintf("The kernel names %s as the way out but no source address, so the address the internet sees is not known here.", firstNonEmpty(r.Dev, "a device"))
		return id
	}
	id.SourceScope = addressScope(addr)
	switch id.SourceScope {
	case "public":
		id.Public = "nic"
		id.Detail = fmt.Sprintf("Traffic leaves from %s, the NIC's own public address. A provider firewall or 1:1 NAT in front of the NIC is not visible from this host.", addr)
	case "link-local":
		id.Public = "unobserved"
		id.Detail = fmt.Sprintf("%s is link-local and cannot reach the internet on its own; the address the internet sees is not known here.", addr)
	default:
		id.Public = "translated"
		id.Detail = fmt.Sprintf("Traffic leaves from %s, which is not routable on the internet, so a router or the provider translates it. The public address it becomes was not observed by this host.", addr)
	}
	return id
}

// addressScope classifies an address the way the identity line reads it.
func addressScope(a netip.Addr) string {
	a = a.Unmap()
	switch {
	case a.IsLoopback():
		return "loopback"
	case a.IsLinkLocalUnicast():
		return "link-local"
	case cgnat.Contains(a):
		return "shared"
	case a.Is6() && a.IsPrivate():
		return "unique-local"
	case a.IsPrivate():
		return "private"
	case isPublic(a):
		return "public"
	}
	return "private"
}

func familyLabel(family string) string {
	if family == "inet6" {
		return "IPv6"
	}
	return "IPv4"
}
