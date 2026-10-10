package netx

import (
	"context"
	"fmt"
	"slices"
)

// A full tunnel sends both default routes into WireGuard while its interface
// is up, but a more specific local route still leaves natively, and an
// interface that disappears without wg-quick's teardown leaves its policy
// rules pointing at an empty table, so traffic falls back to the native
// default. A kill switch closes both: an nftables output filter that admits
// only the tunnel, loopback, WireGuard's own marked transport packets and the
// link's own address upkeep (DHCP and IPv6 neighbour discovery).
//
// It is a Linux wg-quick feature. The phone apps refuse a configuration with
// PostUp lines, so the variant is a separate download and never the QR code;
// the Windows client has its own kill switch for this configuration's shape,
// and phones have the operating system's "block connections without VPN".
//
// PreDown removes the filter, so a deliberate `wg-quick down` restores the
// native path; an interface lost any other way leaves the filter in place
// and the device offline until `nft delete table inet jd_killswitch`.

const wgKillSwitchTable = "jd_killswitch"

// wgKillSwitchHooks are the lines added to [Interface]. %i is wg-quick's own
// substitution for the interface name; nothing here comes from a request.
var wgKillSwitchHooks = [][2]string{
	{"PostUp", "nft add table inet " + wgKillSwitchTable},
	{"PostUp", "nft 'add chain inet " + wgKillSwitchTable + " output { type filter hook output priority 0; policy accept; }'"},
	{"PostUp", "nft 'add rule inet " + wgKillSwitchTable + " output oifname \"lo\" accept'"},
	{"PostUp", "nft 'add rule inet " + wgKillSwitchTable + " output oifname \"%i\" accept'"},
	{"PostUp", "nft add rule inet " + wgKillSwitchTable + " output meta mark \"$(wg show %i fwmark)\" accept"},
	{"PostUp", "nft 'add rule inet " + wgKillSwitchTable + " output icmpv6 type { nd-router-solicit, nd-neighbor-solicit, nd-neighbor-advert } accept'"},
	{"PostUp", "nft 'add rule inet " + wgKillSwitchTable + " output udp sport 68 udp dport 67 accept'"},
	{"PostUp", "nft 'add rule inet " + wgKillSwitchTable + " output udp sport 546 udp dport 547 accept'"},
	{"PostUp", "nft 'add rule inet " + wgKillSwitchTable + " output reject'"},
	{"PreDown", "nft delete table inet " + wgKillSwitchTable},
}

// wgKillSwitchVariant is a full-tunnel client configuration with the kill
// switch added. Any other configuration is refused: a split tunnel's native
// traffic is meant to leave natively.
func wgKillSwitchVariant(config string) (string, error) {
	c := parseWGConf(config)
	sec, peers := c.iface(), c.peers()
	if sec == nil || len(peers) != 1 {
		return "", fmt.Errorf("the stored configuration is not one this dashboard generated")
	}
	allowed := peers[0].list("allowedips")
	if !slices.Contains(allowed, "0.0.0.0/0") || !slices.Contains(allowed, "::/0") {
		return "", fmt.Errorf("a kill switch belongs to a full tunnel; this configuration sends only some networks through the tunnel")
	}
	for _, hook := range wgKillSwitchHooks {
		sec.add(hook[0], hook[1])
	}
	sec.lead = append(sec.lead,
		wgLine{raw: "# Linux wg-quick only, with nftables: every packet that does not leave through"},
		wgLine{raw: "# this tunnel is refused, IPv4 and IPv6, local routes included. A deliberate"},
		wgLine{raw: "# `wg-quick down` lifts it; if the interface is lost any other way the device"},
		wgLine{raw: "# stays offline until: nft delete table inet " + wgKillSwitchTable},
	)
	return c.render(), nil
}

// WireGuardPeerKillSwitch is a full-tunnel device's stored configuration as
// the Linux kill-switch variant. It is shown as text only: no QR code,
// because the apps that scan one refuse its PostUp lines.
func (s *Service) WireGuardPeerKillSwitch(ctx context.Context, iface string, id int) (*WGPeerConfig, error) {
	if err := validWGName(iface); err != nil {
		return nil, err
	}
	c, err := s.peerClient(ctx, iface, id)
	if err != nil {
		return nil, err
	}
	if c.Kind != wgKindDevice {
		return nil, fmt.Errorf("a kill switch belongs to a device's full tunnel")
	}
	text, err := wgKillSwitchVariant(c.Config)
	if err != nil {
		return nil, err
	}
	return &WGPeerConfig{Name: c.Name, Config: text}, nil
}
