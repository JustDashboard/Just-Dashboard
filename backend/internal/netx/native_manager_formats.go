package netx

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

func nativeEmptyFamily(method string) NativeFamilyIntent {
	return NativeFamilyIntent{Method: method, Addresses: []string{}, DNS: []string{}, Domains: []string{}, Routes: []NativeRoute{}}
}
func nativeRouteTable(p *nativeProfile) int {
	if p.View.Contract.VRFTable > 0 {
		return p.View.Contract.VRFTable
	}
	return 254
}
func nativeFamily(in *NativeIntent, value string) *NativeFamilyIntent {
	if strings.Contains(value, ":") {
		return &in.IPv6
	}
	return &in.IPv4
}
func nativeBoolean(s string, fallback bool) (bool, error) {
	if s == "" {
		return fallback, nil
	}
	switch s {
	case "true", "yes", "1":
		return true, nil
	case "false", "no", "0":
		return false, nil
	}
	return false, errors.New("unsupported native boolean")
}

func parseNativeNetworkd(data []byte, p *nativeProfile) (NativeIntent, error) {
	in := NativeIntent{IPv4: nativeEmptyFamily("disabled"), IPv6: nativeEmptyFamily("disabled")}
	blocks, err := parseNativeINI(data)
	if err != nil {
		return in, err
	}
	name, err := blocks.one("Match", "Name")
	mac, macErr := blocks.one("Match", "MACAddress")
	if err != nil || macErr != nil || (name != "" && name != p.View.Device) || (mac != "" && mac != p.Device.Address) || (name == "" && mac == "") {
		return in, errors.New("networkd Match must identify this device by an exact name or MAC")
	}
	for _, block := range blocks {
		if block.Section == "Match" {
			for _, line := range block.Lines {
				key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
				if !ok {
					continue
				}
				if key == "Name" {
					continue
				}
				if key != "MACAddress" || value != p.Device.Address {
					return in, errors.New("networkd additional match selectors require native review")
				}
			}
		}
	}
	dhcp, err := blocks.one("Network", "DHCP")
	if err != nil {
		return in, err
	}
	switch dhcp {
	case "yes", "true":
		in.IPv4.Method, in.IPv6.Method = "auto", "dhcp"
	case "ipv4":
		in.IPv4.Method = "auto"
	case "ipv6":
		in.IPv6.Method = "dhcp"
	case "", "no", "false":
	default:
		return in, errors.New("unsupported networkd DHCP method")
	}
	ra, err := blocks.one("Network", "IPv6AcceptRA")
	if err != nil {
		return in, err
	}
	acceptRA, err := nativeBoolean(ra, true)
	if err != nil {
		return in, err
	}
	if acceptRA {
		if in.IPv6.Method == "dhcp" {
			in.IPv6.Method = "auto"
		} else {
			in.IPv6.Method = "slaac"
		}
	}
	for _, raw := range blocks.values("Network", "Address") {
		nativeFamily(&in, raw).Addresses = append(nativeFamily(&in, raw).Addresses, raw)
	}
	for _, raw := range blocks.values("Network", "DNS") {
		for _, value := range strings.Fields(raw) {
			nativeFamily(&in, value).DNS = append(nativeFamily(&in, value).DNS, value)
		}
	}
	var domains []string
	for _, raw := range blocks.values("Network", "Domains") {
		domains = append(domains, strings.Fields(raw)...)
	}
	for _, block := range blocks {
		if block.Section != "Address" && block.Section != "Route" {
			continue
		}
		props := map[string]string{}
		for _, line := range block.Lines {
			key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok {
				continue
			}
			if _, found := props[key]; found {
				return in, errors.New("duplicate networkd address/route key")
			}
			props[key] = value
		}
		if block.Section == "Address" {
			if len(props) != 1 || props["Address"] == "" {
				return in, errors.New("advanced networkd Address sections are outside this adapter")
			}
			f := nativeFamily(&in, props["Address"])
			f.Addresses = append(f.Addresses, props["Address"])
			continue
		}
		for key := range props {
			if !slices.Contains([]string{"Destination", "Gateway", "Metric", "Table"}, key) {
				return in, errors.New("advanced networkd Route sections are outside this adapter")
			}
		}
		dest := props["Destination"]
		if dest == "" {
			if strings.Contains(props["Gateway"], ":") {
				dest = "::/0"
			} else {
				dest = "0.0.0.0/0"
			}
		}
		metric, table := 0, nativeRouteTable(p)
		if props["Metric"] != "" {
			metric, err = strconv.Atoi(props["Metric"])
			if err != nil {
				return in, errors.New("unsupported native route metric")
			}
		}
		if props["Table"] != "" {
			if props["Table"] == "main" {
				table = 254
			} else {
				table, err = strconv.Atoi(props["Table"])
				if err != nil {
					return in, errors.New("unsupported named native route table")
				}
			}
		}
		f := nativeFamily(&in, dest)
		f.Routes = append(f.Routes, NativeRoute{Destination: dest, Gateway: props["Gateway"], Metric: metric, Table: table})
	}
	for _, gateway := range blocks.values("Network", "Gateway") {
		f := nativeFamily(&in, gateway)
		dest := "0.0.0.0/0"
		if f == &in.IPv6 {
			dest = "::/0"
		}
		f.Routes = append(f.Routes, NativeRoute{Destination: dest, Gateway: gateway, Table: nativeRouteTable(p)})
	}
	for i, f := range []*NativeFamilyIntent{&in.IPv4, &in.IPv6} {
		if f.Method == "disabled" && len(f.Addresses) > 0 {
			f.Method = "manual"
		}
		if f.Method == "disabled" && (len(f.DNS)+len(f.Routes) > 0) {
			return in, errors.New("native family without addressing has DNS/routes that require native review")
		}
		if f.Method != "disabled" {
			f.Domains = append([]string{}, domains...)
		}
		section := "DHCPv4"
		if i == 1 {
			section = "DHCPv6"
		}
		autoDNS, e := blocks.one(section, "UseDNS")
		if e != nil {
			return in, e
		}
		useDNS, e := nativeBoolean(autoDNS, true)
		if e != nil {
			return in, e
		}
		f.IgnoreAutoDNS = !useDNS
		routeSection, routeKeys := section, []string{"UseRoutes"}
		if i == 1 {
			if len(blocks.values(section, "UseRoutes")) != 0 {
				return in, errors.New("DHCPv6 UseRoutes is unsupported by networkd")
			}
			raDNS, e := blocks.one("IPv6AcceptRA", "UseDNS")
			if e != nil {
				return in, e
			}
			raUseDNS, e := nativeBoolean(raDNS, true)
			if e != nil || raUseDNS != useDNS {
				return in, errors.New("separate DHCPv6 and RA DNS overrides require native review")
			}
			routeSection, routeKeys = "IPv6AcceptRA", []string{"UseGateway", "UseRoutePrefix", "UseOnLinkPrefix"}
		}
		useRoutes := true
		for index, key := range routeKeys {
			autoRoutes, e := blocks.one(routeSection, key)
			if e != nil {
				return in, e
			}
			value, e := nativeBoolean(autoRoutes, true)
			if e != nil {
				return in, e
			}
			if index != 0 && value != useRoutes {
				return in, errors.New("separate IPv6 automatic route overrides require native review")
			}
			useRoutes = value
		}
		f.IgnoreAutoRoutes = !useRoutes
	}
	if len(blocks.values("DHCP", "UseDNS"))+len(blocks.values("DHCP", "UseRoutes")) > 0 {
		return in, errors.New("shared legacy DHCP overrides require native review")
	}
	return normalizeNativeIntent(in)
}

func renderNativeNetworkd(data []byte, in NativeIntent) ([]byte, error) {
	blocks, err := parseNativeINI(data)
	if err != nil {
		return nil, err
	}
	v4, v6 := in.IPv4.Method == "auto", in.IPv6.Method == "auto" || in.IPv6.Method == "dhcp"
	dhcp := "no"
	if v4 && v6 {
		dhcp = "yes"
	} else if v4 {
		dhcp = "ipv4"
	} else if v6 {
		dhcp = "ipv6"
	}
	lines := []string{"DHCP=" + dhcp, "IPv6AcceptRA=" + strconv.FormatBool(in.IPv6.Method == "auto" || in.IPv6.Method == "slaac")}
	if in.IPv6.Method == "disabled" {
		lines = append(lines, "LinkLocalAddressing=no")
	} else {
		lines = append(lines, "LinkLocalAddressing=ipv6")
	}
	for _, f := range []NativeFamilyIntent{in.IPv4, in.IPv6} {
		for _, a := range f.Addresses {
			lines = append(lines, "Address="+a)
		}
		for _, dns := range f.DNS {
			lines = append(lines, "DNS="+dns)
		}
	}
	domains := in.IPv4.Domains
	if in.IPv4.Method == "disabled" {
		domains = in.IPv6.Domains
	} else if in.IPv6.Method != "disabled" && !slices.Equal(domains, in.IPv6.Domains) {
		return nil, errors.New("networkd has one per-link domain list; use the same domains for both enabled families")
	}
	if len(domains) > 0 {
		lines = append(lines, "Domains="+strings.Join(domains, " "))
	}
	blocks = blocks.update("Network", func(k string) bool {
		return slices.Contains([]string{"Address", "Gateway", "DNS", "Domains", "DHCP", "IPv6AcceptRA", "LinkLocalAddressing"}, k)
	}, lines)
	var result nativeINI
	for _, block := range blocks {
		if block.Section != "Address" && block.Section != "Route" {
			result = append(result, block)
		}
	}
	for i, f := range []NativeFamilyIntent{in.IPv4, in.IPv6} {
		section := "DHCPv4"
		if i == 1 {
			section = "DHCPv6"
		}
		values := []string{"UseDNS=" + strconv.FormatBool(!f.IgnoreAutoDNS)}
		if i == 0 {
			values = append(values, "UseRoutes="+strconv.FormatBool(!f.IgnoreAutoRoutes))
		} else {
			raValues := []string{"UseDNS=" + strconv.FormatBool(!f.IgnoreAutoDNS)}
			for _, key := range []string{"UseGateway", "UseRoutePrefix", "UseOnLinkPrefix"} {
				raValues = append(raValues, key+"="+strconv.FormatBool(!f.IgnoreAutoRoutes))
			}
			result = result.update("IPv6AcceptRA", func(k string) bool {
				return slices.Contains([]string{"UseDNS", "UseGateway", "UseRoutePrefix", "UseOnLinkPrefix"}, k)
			}, raValues)
		}
		result = result.update(section, func(k string) bool { return k == "UseDNS" || k == "UseRoutes" }, values)
		for _, r := range f.Routes {
			values := []string{"Destination=" + r.Destination, "Metric=" + strconv.Itoa(r.Metric), "Table=" + strconv.Itoa(r.Table)}
			if r.Gateway != "" {
				values = append(values, "Gateway="+r.Gateway)
			}
			result = append(result, nativeINIBlock{Section: "Route", Lines: values})
		}
	}
	return result.render(), nil
}

func parseNativeNM(data []byte, p *nativeProfile) (NativeIntent, error) {
	in := NativeIntent{IPv4: nativeEmptyFamily("disabled"), IPv6: nativeEmptyFamily("disabled")}
	blocks, err := parseNativeINI(data)
	if err != nil {
		return in, err
	}
	uuid, e := blocks.one("connection", "uuid")
	if e != nil || uuid != p.UUID {
		return in, errors.New("NetworkManager profile UUID differs from the active owner")
	}
	name, e := blocks.one("connection", "interface-name")
	if e != nil || name != p.View.Device {
		return in, errors.New("NetworkManager profile must bind this exact interface")
	}
	typeName, e := blocks.one("connection", "type")
	if e != nil || !slices.Contains([]string{"ethernet", "802-3-ethernet", "dummy", "vlan", "bridge", "bond", "vrf"}, typeName) {
		return in, errors.New("this NetworkManager connection type is outside the secret-free adapter")
	}
	if typeName != p.View.Kind && !((p.View.Kind == "physical" || p.View.Kind == "veth" && p.Peer != nil) && slices.Contains([]string{"ethernet", "802-3-ethernet"}, typeName)) {
		return in, errors.New("the saved NetworkManager connection type differs from the observed device kind")
	}
	auto, e := blocks.one("connection", "autoconnect")
	if e != nil {
		return in, e
	}
	connect, e := nativeBoolean(auto, true)
	if e != nil || !connect {
		return in, errors.New("the existing native profile does not autoconnect at boot")
	}
	for i, f := range []*NativeFamilyIntent{&in.IPv4, &in.IPv6} {
		section := "ipv4"
		if i == 1 {
			section = "ipv6"
		}
		method, e := blocks.one(section, "method")
		if e != nil {
			return in, e
		}
		if method == "" {
			return in, errors.New("native address method is not explicit")
		}
		f.Method = method
		defaultMetric := -1
		if raw, e := blocks.one(section, "route-metric"); e != nil {
			return in, e
		} else if raw != "" {
			defaultMetric, e = strconv.Atoi(raw)
			if e != nil {
				return in, errors.New("unsupported native default route metric")
			}
		}
		defaultTable := nativeRouteTable(p)
		if raw, e := blocks.one(section, "route-table"); e != nil {
			return in, e
		} else if raw != "" && raw != "0" {
			defaultTable, e = strconv.Atoi(raw)
			if e != nil {
				return in, errors.New("unsupported native default route table")
			}
		}
		for _, block := range blocks {
			if block.Section != section {
				continue
			}
			for _, line := range block.Lines {
				key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
				if !ok {
					continue
				}
				switch {
				case strings.HasPrefix(key, "address") && key != "addresses":
					if _, e := strconv.Atoi(strings.TrimPrefix(key, "address")); e != nil {
						return in, errors.New("unknown native address encoding")
					}
					parts := strings.Split(value, ",")
					if len(parts) > 2 {
						return in, errors.New("unsupported native address options")
					}
					f.Addresses = append(f.Addresses, parts[0])
					if len(parts) == 2 && parts[1] != "" {
						dest := "0.0.0.0/0"
						if i == 1 {
							dest = "::/0"
						}
						f.Routes = append(f.Routes, NativeRoute{Destination: dest, Gateway: parts[1], Metric: defaultMetric, Table: defaultTable})
					}
				case strings.HasPrefix(key, "route") && key != "route-metric" && key != "route-table":
					if strings.HasSuffix(key, "_options") {
						continue
					}
					if _, e := strconv.Atoi(strings.TrimPrefix(key, "route")); e != nil {
						return in, errors.New("unknown native route encoding")
					}
					parts := strings.Split(value, ",")
					if len(parts) < 1 || len(parts) > 3 {
						return in, errors.New("unsupported native route encoding")
					}
					r := NativeRoute{Destination: parts[0], Metric: defaultMetric, Table: defaultTable}
					if len(parts) > 1 {
						r.Gateway = parts[1]
					}
					if len(parts) > 2 && parts[2] != "" {
						r.Metric, e = strconv.Atoi(parts[2])
						if e != nil {
							return in, errors.New("unsupported native route metric")
						}
						if r.Metric == 0 {
							r.Metric = defaultMetric
						}
					}
					options, e := blocks.one(section, key+"_options")
					if e != nil {
						return in, e
					}
					if options != "" {
						attribute, table, found := strings.Cut(options, "=")
						if !found || attribute != "table" {
							return in, errors.New("advanced native route attributes require review")
						}
						r.Table, e = strconv.Atoi(table)
						if e != nil {
							return in, errors.New("unsupported native route table")
						}
					}
					f.Routes = append(f.Routes, r)
				case key == "dns":
					for _, dns := range strings.Split(value, ";") {
						if dns != "" {
							f.DNS = append(f.DNS, dns)
						}
					}
				case key == "dns-search":
					for _, domain := range strings.Split(value, ";") {
						if domain != "" {
							f.Domains = append(f.Domains, domain)
						}
					}
				case key == "ignore-auto-dns":
					f.IgnoreAutoDNS, e = nativeBoolean(value, false)
					if e != nil {
						return in, e
					}
				case key == "ignore-auto-routes":
					f.IgnoreAutoRoutes, e = nativeBoolean(value, false)
					if e != nil {
						return in, e
					}
				case key == "gateway" && value != "":
					dest := "0.0.0.0/0"
					if i == 1 {
						dest = "::/0"
					}
					f.Routes = append(f.Routes, NativeRoute{Destination: dest, Gateway: value, Metric: defaultMetric, Table: defaultTable})
				}
			}
		}
	}
	return normalizeNativeIntent(in)
}

func stageNativeNM(ctx context.Context, p *nativeProfile, in NativeIntent) ([]byte, error) {
	args := []string{"--offline", "connection", "modify"}
	for i, f := range []NativeFamilyIntent{in.IPv4, in.IPv6} {
		section := "ipv4"
		if i == 1 {
			section = "ipv6"
		}
		if f.Method == "slaac" {
			return nil, errors.New("the supported NetworkManager method auto governs SLAAC/DHCP through router advertisements; a SLAAC-only override is not supported")
		}
		var routes []string
		for _, r := range f.Routes {
			gateway := r.Gateway
			if gateway == "" {
				if i == 1 {
					gateway = "::"
				} else {
					gateway = "0.0.0.0"
				}
			}
			if r.Metric == -1 {
				routes = append(routes, fmt.Sprintf("%s %s table=%d", r.Destination, gateway, r.Table))
			} else {
				routes = append(routes, fmt.Sprintf("%s %s %d table=%d", r.Destination, gateway, r.Metric, r.Table))
			}
		}
		args = append(args, section+".method", f.Method, section+".addresses", strings.Join(f.Addresses, ","), section+".dns", strings.Join(f.DNS, ","), section+".dns-search", strings.Join(f.Domains, ","), section+".gateway", "", section+".routes", strings.Join(routes, ","), section+".ignore-auto-dns", strconv.FormatBool(f.IgnoreAutoDNS), section+".ignore-auto-routes", strconv.FormatBool(f.IgnoreAutoRoutes))
	}
	out, err := nativeExecute(ctx, p.File.Data, "nmcli", args...)
	if err != nil {
		return nil, errors.New("NetworkManager refused the staged supported profile; no native profile was changed")
	}
	if len(out) > maxNativeProfileBytes {
		return nil, errors.New("staged native profile exceeds its bound")
	}
	return []byte(out), nil
}
