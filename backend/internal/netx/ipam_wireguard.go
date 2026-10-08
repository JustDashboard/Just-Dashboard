package netx

import (
	"fmt"
	"net/netip"
	"strings"
)

// ValidateWireGuardReservation checks the native name and explicit family
// bounds before claiming shared planning rows. Native creation still checks
// current kernel/config ownership, peer drift and forwarding independently.
func ValidateWireGuardReservation(req WGServerRequest) error {
	if err := validWGName(req.Name); err != nil {
		return err
	}
	for _, raw := range []string{req.Subnet, func() string {
		if req.IPv6 != nil {
			return req.IPv6.Subnet
		}
		return ""
	}()} {
		if raw == "" {
			continue
		}
		p, err := netip.ParsePrefix(raw)
		if err != nil || p.Addr().Is4In6() || p != p.Masked() {
			return fmt.Errorf("selected reservations require their exact canonical prefix")
		}
	}
	if req.Subnet != "" {
		if _, err := (&Service{}).wgSubnetFor(req.Subnet, wgHostState{}, nil); err != nil {
			return err
		}
	}
	if req.IPv6 != nil && req.IPv6.Subnet != "" {
		if _, err := wgIPv6Subnet(req.IPv6.Subnet, wgHostState{}, nil); err != nil {
			return err
		}
	}
	if req.IPv6 != nil && req.IPv6.ExitNode && !req.ExitNode {
		return fmt.Errorf("IPv6 exit egress accompanies the IPv4 exit; enable exitNode too")
	}
	if strings.TrimSpace(req.Subnet) == "" && (req.IPv6 == nil || strings.TrimSpace(req.IPv6.Subnet) == "") {
		return fmt.Errorf("selected reservations require an explicit native subnet")
	}
	return nil
}
