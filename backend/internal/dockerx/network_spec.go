package dockerx

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"unicode/utf8"
)

// NetworkIPAM keeps each address family's allocation policy together. The
// legacy top-level subnet remains accepted for existing API callers.
type NetworkIPAM struct {
	Subnet  string `json:"subnet"`
	Gateway string `json:"gateway,omitempty"`
	IPRange string `json:"ipRange,omitempty"`
}

func NormalizeNetworkSpec(spec NetworkSpec) (NetworkSpec, error) {
	spec.Name = strings.TrimSpace(spec.Name)
	if !validResourceName(spec.Name) {
		return spec, errors.New("a network name may contain letters, digits, and _ . - after the first character")
	}
	spec.Driver = strings.TrimSpace(spec.Driver)
	if spec.Driver == "" {
		spec.Driver = "bridge"
	}
	if spec.Driver == "host" || spec.Driver == "none" || spec.Driver == "null" || len(spec.Driver) > 256 || strings.ContainsAny(spec.Driver, " \t\r\n\x00") || !utf8.ValidString(spec.Driver) {
		return spec, errors.New("choose a network driver; host and none are existing Docker system networks")
	}
	if err := validateNetworkMetadata("labels", spec.Labels); err != nil {
		return spec, err
	}
	if err := validateNetworkMetadata("driver options", spec.Options); err != nil {
		return spec, err
	}
	legacy := NetworkIPAM{strings.TrimSpace(spec.Subnet), strings.TrimSpace(spec.Gateway), strings.TrimSpace(spec.IPRange)}
	if legacy.Subnet != "" || legacy.Gateway != "" || legacy.IPRange != "" {
		if len(spec.IPAM) != 0 {
			return spec, errors.New("use either the legacy subnet fields or ipam pools, not both")
		}
		spec.IPAM = []NetworkIPAM{legacy}
	} else {
		spec.IPAM = append([]NetworkIPAM(nil), spec.IPAM...)
	}
	if len(spec.IPAM) > 16 {
		return spec, errors.New("a network can specify at most sixteen address pools")
	}
	var prefixes []netip.Prefix
	for i, pool := range spec.IPAM {
		pool.Subnet, pool.Gateway, pool.IPRange = strings.TrimSpace(pool.Subnet), strings.TrimSpace(pool.Gateway), strings.TrimSpace(pool.IPRange)
		prefix, err := netip.ParsePrefix(pool.Subnet)
		if err != nil || prefix.Addr().Is4In6() || prefix != prefix.Masked() {
			return spec, fmt.Errorf("address pool %d needs a canonical IPv4 or IPv6 subnet", i+1)
		}
		if prefix.Addr().Is6() && !spec.IPv6 {
			return spec, errors.New("enable IPv6 before adding an IPv6 address pool")
		}
		for _, previous := range prefixes {
			if prefix.Overlaps(previous) {
				return spec, errors.New("address pools in one network cannot overlap")
			}
		}
		if pool.Gateway != "" {
			gateway, err := netip.ParseAddr(pool.Gateway)
			if err != nil || gateway.Zone() != "" || gateway.Is4In6() || !prefix.Contains(gateway) {
				return spec, fmt.Errorf("address pool %d gateway must be a literal address inside its subnet", i+1)
			}
		}
		if pool.IPRange != "" {
			rangePrefix, err := netip.ParsePrefix(pool.IPRange)
			if err != nil || rangePrefix != rangePrefix.Masked() || rangePrefix.Addr().Is4In6() || rangePrefix.Bits() < prefix.Bits() || !prefix.Contains(rangePrefix.Addr()) {
				return spec, fmt.Errorf("address pool %d allocation range must be a canonical subnet inside its pool", i+1)
			}
		}
		prefixes = append(prefixes, prefix)
		spec.IPAM[i] = pool
	}
	spec.Subnet, spec.Gateway, spec.IPRange = "", "", ""
	return spec, nil
}

func validateNetworkMetadata(name string, values map[string]string) error {
	if len(values) > 32 {
		return fmt.Errorf("network %s can contain at most thirty-two entries", name)
	}
	total := 0
	for key, value := range values {
		if key == "" || key != strings.TrimSpace(key) || len(key) > 256 || len(value) > 4096 || strings.ContainsRune(key+value, '\x00') || !utf8.ValidString(key+value) {
			return fmt.Errorf("network %s need nonempty keys up to 256 bytes and values up to 4096 bytes", name)
		}
		total += len(key) + len(value)
	}
	if total > 16<<10 {
		return fmt.Errorf("network %s exceed the sixteen KiB limit", name)
	}
	return nil
}
