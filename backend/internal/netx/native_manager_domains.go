package netx

import (
	"errors"
	"net/netip"
	"slices"
)

type nativeNetworkdDomain struct {
	Domain   string `json:"Domain"`
	Source   string `json:"ConfigSource"`
	Provider []byte `json:"ConfigProvider"`
}

// Automatic search/routing domains are independent of UseDNS. The API edits
// configured domains while retaining this existing native policy verbatim.
func nativeNetworkdDomainPolicy(data []byte) (map[string]string, error) {
	blocks, err := parseNativeINI(data)
	if err != nil {
		return nil, err
	}
	if len(blocks.values("Network", "UseDomains"))+len(blocks.values("DHCP", "UseDomains")) != 0 {
		return nil, errors.New("shared or inherited native domain policy requires review through its owner")
	}
	policy := map[string]string{}
	for _, section := range []string{"DHCPv4", "DHCPv6", "IPv6AcceptRA"} {
		value, err := blocks.one(section, "UseDomains")
		if err != nil {
			return nil, err
		}
		switch value {
		case "", "route":
			policy[section] = value
		default:
			use, err := nativeBoolean(value, false)
			if err != nil {
				return nil, errors.New("unsupported automatic native domain policy")
			}
			policy[section] = "no"
			if use {
				policy[section] = "yes"
			}
		}
	}
	// Unset protocol policies can inherit daemon defaults. They are not assumed
	// to be 'no'; an acquired domain needs an explicit captured policy below.
	return policy, nil
}

func nativeNetworkdDomainPolicyEqual(before, candidate []byte) error {
	a, err := nativeNetworkdDomainPolicy(before)
	if err != nil {
		return err
	}
	b, err := nativeNetworkdDomainPolicy(candidate)
	if err != nil {
		return err
	}
	for section, value := range a {
		if b[section] != value {
			return errors.New("native staging changed the retained automatic domain policy")
		}
	}
	return nil
}

func nativeNetworkdProfileDomainPolicy(data []byte, p *nativeProfile) (map[string]string, error) {
	if p != nil && p.View.Owner == "netplan" && p.View.Renderer == "networkd" {
		return nativeNetplanGeneratedAutomaticPolicy(p, p.File.Data, data)
	}
	return nativeNetworkdDomainPolicy(data)
}

func nativeNetworkdVerifyDomains(data []byte, intent NativeIntent, search, routes []nativeNetworkdDomain) ([]string, error) {
	policy, err := nativeNetworkdDomainPolicy(data)
	if err != nil {
		return nil, err
	}
	return nativeNetworkdVerifyDomainPolicy(policy, intent, search, routes)
}

func nativeNetworkdVerifyDomainPolicy(policy map[string]string, intent NativeIntent, search, routes []nativeNetworkdDomain) ([]string, error) {
	if len(search)+len(routes) > 128 {
		return nil, errors.New("native active domain evidence exceeds its bound")
	}
	var result []string
	for _, set := range []struct {
		Domains []nativeNetworkdDomain
		Route   bool
	}{{search, false}, {routes, true}} {
		for _, domain := range set.Domains {
			value := domain.Domain
			if !nativeDomain(value) && !(set.Route && value == "." && domain.Source == "static") {
				return nil, errors.New("unreadable native domain evidence")
			}
			if set.Route {
				value = "~" + value
			}
			if domain.Source == "static" {
				if len(domain.Provider) != 0 || !slices.Contains(intent.IPv4.Domains, value) && !slices.Contains(intent.IPv6.Domains, value) {
					return nil, errors.New("native active configured domain differs from its captured intent")
				}
				result = append(result, value)
				continue
			}
			section, size, enabled := "", 0, false
			switch domain.Source {
			case "DHCPv4":
				section, size, enabled = "DHCPv4", 4, intent.IPv4.Method == "auto"
			case "DHCPv6":
				section, size, enabled = "DHCPv6", 16, intent.IPv6.Method == "auto" || intent.IPv6.Method == "dhcp"
			case "NDisc":
				section, size, enabled = "IPv6AcceptRA", 16, intent.IPv6.Method == "auto" || intent.IPv6.Method == "slaac"
			default:
				return nil, errors.New("native domain has foreign or unreadable active provenance")
			}
			provider, valid := netip.AddrFromSlice(domain.Provider)
			wanted := "yes"
			if set.Route {
				wanted = "route"
			}
			if !enabled || len(domain.Provider) != size || !valid || provider.IsUnspecified() || provider.IsMulticast() || provider.Is4In6() || policy[section] != wanted {
				return nil, errors.New("native acquired domain disagrees with its captured protocol/provider/search-or-route policy")
			}
			result = append(result, value)
		}
	}
	return result, nil
}
