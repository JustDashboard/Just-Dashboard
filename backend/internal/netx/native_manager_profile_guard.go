package netx

import (
	"errors"
	"slices"
	"strconv"
	"strings"
)

func nativeIndexedKey(key, prefix string) bool {
	number, err := strconv.Atoi(strings.TrimPrefix(key, prefix))
	return strings.HasPrefix(key, prefix) && err == nil && number > 0 && number <= 32
}

// Reactivating a native profile applies the whole file. An unrepresented
// property can therefore change topology, firewall or identity even when its
// L3 subset matches. Keep admission closed until that property's contract is
// validated against the active owner and kernel.
func nativeProfileFieldsGuard(p *nativeProfile, data []byte, renderer string) error {
	blocks, err := parseNativeINI(data)
	if err != nil {
		return err
	}
	if renderer == "networkd" {
		if _, err := nativeNetworkdProfileDomainPolicy(data, p); err != nil {
			return err
		}
	}
	networkd := map[string][]string{
		"Match":        {"Name", "MACAddress"},
		"Network":      {"Address", "Gateway", "DNS", "Domains", "DHCP", "IPv6AcceptRA", "LinkLocalAddressing"},
		"DHCPv4":       {"UseDNS", "UseRoutes", "UseDomains"},
		"DHCPv6":       {"UseDNS", "UseDomains"},
		"IPv6AcceptRA": {"UseDNS", "UseGateway", "UseRoutePrefix", "UseOnLinkPrefix", "UseDomains"},
		"Address":      {"Address"},
		"Route":        {"Destination", "Gateway", "Metric", "Table"},
		"Link":         {"RequiredForOnline"},
	}
	nm := map[string][]string{
		"connection":     {"id", "uuid", "type", "interface-name", "autoconnect", "timestamp"},
		"ipv4":           {"method", "dns", "dns-search", "gateway", "route-metric", "route-table", "ignore-auto-dns", "ignore-auto-routes"},
		"ipv6":           {"method", "dns", "dns-search", "gateway", "route-metric", "route-table", "ignore-auto-dns", "ignore-auto-routes", "addr-gen-mode", "ip6-privacy"},
		"ethernet":       {},
		"802-3-ethernet": {},
		"dummy":          {},
		"proxy":          {},
	}
	allowed := networkd
	if renderer == "networkd" && p != nil && p.View.Owner == "netplan" && p.View.Renderer == "networkd" {
		allowed["DHCP"] = []string{"RouteMetric", "UseMTU", "UseDNS", "UseRoutes", "UseDomains"}
	}
	if renderer == "NetworkManager" {
		allowed = nm
	}
	for _, block := range blocks {
		keys, known := allowed[block.Section]
		if !known && block.Section != "" {
			return errors.New("this saved native profile has properties whose active effects are not yet verified")
		}
		for _, line := range block.Lines {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, ";") {
				continue
			}
			key, _, property := strings.Cut(trim, "=")
			key = strings.TrimSpace(key)
			if !property {
				continue
			}
			if slices.Contains(keys, key) {
				continue
			}
			if renderer == "NetworkManager" && (block.Section == "ipv4" || block.Section == "ipv6") {
				if nativeIndexedKey(key, "address") || nativeIndexedKey(key, "route") {
					continue
				}
				if base, options := strings.CutSuffix(key, "_options"); options && nativeIndexedKey(base, "route") && len(blocks.values(block.Section, base)) == 1 {
					continue
				}
			}
			return errors.New("this saved native profile has properties whose active effects are not yet verified")
		}
	}
	return nil
}

func nativeNetplanFieldsGuard(p *nativeProfile) error {
	_, profile, err := nativeNetplanNode(p.File.Data, p)
	if err != nil {
		return err
	}
	if p.NetplanSection != "ethernets" && !(p.NetplanSection == "dummy-devices" && p.View.Kind == "dummy" && p.View.Renderer == "NetworkManager") {
		return errors.New("this Netplan structural profile requires verified native topology before activation")
	}
	for i := 0; i < len(profile.Content); i += 2 {
		key := profile.Content[i].Value
		if key == "ra-overrides" && p.View.Renderer == "networkd" {
			continue
		}
		if !slices.Contains([]string{"renderer", "match", "set-name", "optional", "link-local", "dhcp4", "dhcp6", "accept-ra", "addresses", "nameservers", "routes", "dhcp4-overrides", "dhcp6-overrides", "networkmanager"}, key) {
			return errors.New("this saved Netplan profile has properties whose active effects are not yet verified")
		}
	}
	if p.View.Renderer == "NetworkManager" {
		if err := nativeNetplanNMIdentity(p); err != nil {
			return err
		}
	} else if nativeYAMLGet(profile, "networkmanager") != nil {
		return errors.New("NetworkManager metadata disagrees with the Netplan renderer")
	}
	if err := nativeProfileFieldsGuard(p, p.Generated.Data, p.View.Renderer); err != nil {
		return err
	}
	var rendered NativeIntent
	if p.View.Renderer == "networkd" {
		rendered, err = parseNativeNetworkd(p.Generated.Data, p)
	} else {
		rendered, err = parseNativeNM(p.Generated.Data, p)
	}
	if err != nil || p.View.Intent == nil || !nativeRenderedIntentEqual(rendered, *p.View.Intent) {
		return errors.New("the selected Netplan renderer artifact does not agree with its authored supported intent")
	}
	return nil
}

func nativeRenderedIntentEqual(a, b NativeIntent) bool {
	for _, intent := range []*NativeIntent{&a, &b} {
		for _, family := range []*NativeFamilyIntent{&intent.IPv4, &intent.IPv6} {
			if family.Method == "manual" || family.Method == "disabled" {
				family.IgnoreAutoDNS, family.IgnoreAutoRoutes = false, false
			}
		}
	}
	return nativeIntentEqual(a, b)
}
