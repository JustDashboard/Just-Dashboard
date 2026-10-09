package netx

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"
)

const maxNativeProfileBytes = 256 << 10

// NativeIntent contains only the supported connection properties. Native
// profile bytes, secrets, paths and checkpoint identities never reach a client.
type NativeIntent struct {
	IPv4 NativeFamilyIntent `json:"ipv4"`
	IPv6 NativeFamilyIntent `json:"ipv6"`
}

type NativeFamilyIntent struct {
	Method           string        `json:"method"`
	Addresses        []string      `json:"addresses"`
	DNS              []string      `json:"dns"`
	Domains          []string      `json:"domains"`
	IgnoreAutoDNS    bool          `json:"ignoreAutoDns"`
	IgnoreAutoRoutes bool          `json:"ignoreAutoRoutes"`
	Routes           []NativeRoute `json:"routes"`
}

type NativeRoute struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway,omitempty"`
	Metric      int    `json:"metric"`
	Table       int    `json:"table"`
}

type NativeEditRequest struct {
	Generation string                  `json:"generation"`
	Intent     NativeIntent            `json:"intent"`
	Structure  *NativeStructureRequest `json:"structure,omitempty"`
}

type NativeEvidence struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type NativeManagerCapability struct {
	Manager string   `json:"manager"`
	Version string   `json:"version,omitempty"`
	Status  string   `json:"status"`
	Reason  string   `json:"reason,omitempty"`
	Edits   []string `json:"edits"`
}

type NativeDeviceSummary struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	MAC  string `json:"mac"`
}

type NativeManagerView struct {
	CheckedAt    time.Time                 `json:"checkedAt"`
	Capabilities []NativeManagerCapability `json:"capabilities"`
	Devices      []NativeDeviceSummary     `json:"devices"`
	Warnings     []string                  `json:"warnings"`
}

type NativeProfileView struct {
	CheckedAt  time.Time      `json:"checkedAt"`
	Device     string         `json:"device"`
	Kind       string         `json:"kind"`
	Owner      string         `json:"owner"`
	Renderer   string         `json:"renderer,omitempty"`
	Version    string         `json:"version,omitempty"`
	Profile    string         `json:"profile,omitempty"`
	Generation string         `json:"generation,omitempty"`
	Editable   bool           `json:"editable"`
	Refusal    string         `json:"refusal,omitempty"`
	Coverage   []string       `json:"coverage"`
	Contract   NativeContract `json:"contract"`
	Intent     *NativeIntent  `json:"intent,omitempty"`
	Configured NativeEvidence `json:"configured"`
	Runtime    NativeEvidence `json:"runtime"`
	Boot       NativeEvidence `json:"boot"`
}

// Existing bond/VRF L3 edits retain these observed native relationships. They
// do not imply structural provisioning or permission to take over members.
type NativeContract struct {
	Members  []string `json:"members"`
	BondMode string   `json:"bondMode,omitempty"`
	VRFTable int      `json:"vrfTable,omitempty"`
	Master   string   `json:"master,omitempty"`
}

func normalizeNativeIntent(in NativeIntent) (NativeIntent, error) {
	for index, family := range []*NativeFamilyIntent{&in.IPv4, &in.IPv6} {
		v6 := index == 1
		methods := []string{"auto", "manual", "disabled"}
		if v6 {
			methods = append(methods, "dhcp", "slaac")
		}
		if !slices.Contains(methods, family.Method) {
			return in, errors.New("native address method must be auto, manual, disabled, or IPv6 dhcp/slaac")
		}
		if len(family.Addresses) > 16 || len(family.DNS) > 8 || len(family.Domains) > 16 || len(family.Routes) > 32 {
			return in, errors.New("native profiles support at most 16 addresses, 8 DNS servers, 16 domains and 32 routes per family")
		}
		if family.Method == "manual" && len(family.Addresses) == 0 {
			return in, errors.New("manual addressing requires at least one address")
		}
		if family.Method == "disabled" && (len(family.Addresses)+len(family.DNS)+len(family.Domains)+len(family.Routes) != 0) {
			return in, errors.New("a disabled family cannot contain addresses, DNS, domains or routes")
		}
		seen := map[string]bool{}
		for i, raw := range family.Addresses {
			p, err := netip.ParsePrefix(raw)
			if err != nil || p.Addr().Is6() != v6 || !p.Addr().IsGlobalUnicast() || p.Addr().Is4In6() || p.Bits() == 0 {
				return in, fmt.Errorf("invalid native family address %q", raw)
			}
			family.Addresses[i] = p.String()
			if seen[p.String()] {
				return in, errors.New("duplicate native address")
			}
			seen[p.String()] = true
		}
		seen = map[string]bool{}
		for i, raw := range family.DNS {
			a, err := netip.ParseAddr(raw)
			if err != nil || a.Is6() != v6 || a.Zone() != "" || a.IsUnspecified() || a.IsMulticast() || a.Is4In6() {
				return in, errors.New("native DNS servers must be unscoped IP literals of their selected family")
			}
			family.DNS[i] = a.String()
			if seen[a.String()] {
				return in, errors.New("duplicate native DNS server")
			}
			seen[a.String()] = true
		}
		seen = map[string]bool{}
		for _, domain := range family.Domains {
			name := strings.TrimPrefix(domain, "~")
			if domain != "~." && !nativeDomain(name) || seen[domain] {
				return in, errors.New("native domains must be unique DNS suffixes, optionally prefixed with ~")
			}
			seen[domain] = true
		}
		seen = map[string]bool{}
		for i, route := range family.Routes {
			p, err := netip.ParsePrefix(route.Destination)
			if err != nil || p.Addr().Is6() != v6 || p.Addr().Is4In6() || p != p.Masked() || p.Addr().IsMulticast() {
				return in, errors.New("native route destinations must be aligned prefixes of their selected family")
			}
			if route.Gateway != "" {
				a, err := netip.ParseAddr(route.Gateway)
				if err != nil || a.Is6() != v6 || a.Zone() != "" || a.IsUnspecified() || a.IsMulticast() || a.Is4In6() {
					return in, errors.New("native route gateways must be unscoped literals of their selected family")
				}
				route.Gateway = a.String()
			}
			if route.Metric < -1 || route.Metric > 1000000 || route.Table < 1 || route.Table > 2147483647 || route.Table == 253 || route.Table == 255 || route.Table == tableTailscale {
				return in, errors.New("native route metric/table is outside the supported range or belongs to a protected owner")
			}
			route.Destination = p.String()
			key := fmt.Sprintf("%s/%s/%d/%d", route.Destination, route.Gateway, route.Metric, route.Table)
			if seen[key] {
				return in, errors.New("duplicate native route")
			}
			seen[key], family.Routes[i] = true, route
		}
		if family.Addresses == nil {
			family.Addresses = []string{}
		}
		if family.DNS == nil {
			family.DNS = []string{}
		}
		if family.Domains == nil {
			family.Domains = []string{}
		}
		if family.Routes == nil {
			family.Routes = []NativeRoute{}
		}
	}
	return in, nil
}

func nativeDomain(s string) bool {
	if s == "" || len(s) > 253 || strings.HasSuffix(s, ".") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}
