package netx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func nativeYAML(data []byte) (*yaml.Node, error) {
	var node yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&node); err != nil || len(node.Content) != 1 {
		return nil, errors.New("unreadable bounded netplan source")
	}
	if decoder.Decode(new(yaml.Node)) != io.EOF {
		return nil, errors.New("netplan source must contain one document")
	}
	var walk func(*yaml.Node) error
	walk = func(n *yaml.Node) error {
		if n.Kind == yaml.AliasNode || n.Anchor != "" {
			return errors.New("netplan aliases require review through their native owner")
		}
		if n.Kind == yaml.MappingNode {
			seen := map[string]bool{}
			for i := 0; i < len(n.Content); i += 2 {
				if seen[n.Content[i].Value] {
					return errors.New("duplicate netplan mapping key")
				}
				seen[n.Content[i].Value] = true
			}
		}
		for _, child := range n.Content {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(&node); err != nil {
		return nil, err
	}
	return &node, nil
}
func nativeYAMLGet(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
func nativeYAMLScalar(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	if n.Kind == yaml.ScalarNode {
		return n.Value
	}
	return ""
}
func nativeYAMLSet(n *yaml.Node, key string, value any) error {
	var encoded yaml.Node
	if err := encoded.Encode(value); err != nil {
		return err
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			encoded.HeadComment = n.Content[i+1].HeadComment
			encoded.LineComment = n.Content[i+1].LineComment
			n.Content[i+1] = &encoded
			return nil
		}
	}
	n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &encoded)
	return nil
}

func nativeNetplanFiles() ([]nativeProfileFile, error) {
	var files []nativeProfileFile
	total := 0
	for _, root := range []string{"/lib/netplan", "/etc/netplan", "/run/netplan"} {
		entries, err := os.ReadDir(nativeHostPath(root))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, errors.New("netplan source inventory could not be read")
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".yaml") && !strings.HasSuffix(entry.Name(), ".yml") {
				continue
			}
			if len(files) >= 32 {
				return nil, errors.New("netplan source count exceeds its bound")
			}
			path := filepath.Join(root, entry.Name())
			file, err := nativeReadProfile(path)
			if err != nil {
				return nil, errors.New("netplan source is unreadable or changed during inspection")
			}
			file.Path = path
			total += len(file.Data)
			if total > 1<<20 {
				return nil, errors.New("netplan source inventory exceeds its byte bound")
			}
			files = append(files, *file)
		}
	}
	return files, nil
}

func nativeNetplanOrigin(p *nativeProfile, selected string) error {
	id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(selected), "10-netplan-"), ".network")
	if p.View.Renderer == "NetworkManager" {
		id = strings.TrimSuffix(strings.TrimPrefix(filepath.Base(selected), "netplan-"), ".nmconnection")
	}
	if id == "" || len(id) > 100 {
		return errors.New("generated netplan profile identity is unsupported")
	}
	files, err := nativeNetplanFiles()
	if err != nil {
		return err
	}
	p.Sources = files
	matches := 0
	for _, file := range files {
		node, err := nativeYAML(file.Data)
		if err != nil {
			return err
		}
		network := nativeYAMLGet(node.Content[0], "network")
		if nativeYAMLScalar(nativeYAMLGet(network, "version")) != "2" {
			return errors.New("netplan source version is outside the supported contract")
		}
		for _, section := range []string{"ethernets", "bonds", "vrfs", "bridges", "vlans"} {
			profile := nativeYAMLGet(nativeYAMLGet(network, section), id)
			if profile == nil {
				continue
			}
			matches++
			if file.Path != "/etc/netplan/"+filepath.Base(file.Path) {
				return errors.New("this netplan profile originates outside persistent /etc ownership")
			}
			name := id
			match := nativeYAMLGet(profile, "match")
			if match != nil {
				if match.Kind != yaml.MappingNode {
					return errors.New("unsupported netplan match selectors")
				}
				for i := 0; i < len(match.Content); i += 2 {
					key, value := match.Content[i].Value, nativeYAMLScalar(match.Content[i+1])
					switch key {
					case "name":
						name = value
					case "macaddress":
						if value != p.Device.Address {
							return errors.New("netplan source MAC does not match the active native device")
						}
					default:
						return errors.New("advanced netplan match selectors require native review")
					}
				}
				if nativeYAMLGet(match, "name") == nil && nativeYAMLGet(match, "macaddress") != nil {
					name = p.View.Device
				}
			}
			if setName := nativeYAMLScalar(nativeYAMLGet(profile, "set-name")); setName != "" {
				name = setName
			}
			if name != p.View.Device {
				return errors.New("netplan source does not bind this exact native device")
			}
			renderer := nativeYAMLScalar(nativeYAMLGet(profile, "renderer"))
			if renderer == "" {
				renderer = nativeYAMLScalar(nativeYAMLGet(network, "renderer"))
			}
			if renderer == "" {
				renderer = "networkd"
			}
			if renderer != p.View.Renderer {
				return errors.New("netplan renderer disagrees with the actual active native owner")
			}
			p.File, p.NetplanID, p.NetplanSection = file, id, section
		}
	}
	if matches != 1 {
		return errors.New("netplan has missing or multiple source definitions; resolve origin through its native owner")
	}
	return nil
}

func nativeNetplanNode(data []byte, p *nativeProfile) (*yaml.Node, *yaml.Node, error) {
	node, err := nativeYAML(data)
	if err != nil {
		return nil, nil, err
	}
	profile := nativeYAMLGet(nativeYAMLGet(nativeYAMLGet(node.Content[0], "network"), p.NetplanSection), p.NetplanID)
	if profile == nil || profile.Kind != yaml.MappingNode {
		return nil, nil, errors.New("selected netplan source definition is absent")
	}
	return node, profile, nil
}

func parseNativeNetplan(data []byte, p *nativeProfile) (NativeIntent, error) {
	in := NativeIntent{IPv4: nativeEmptyFamily("disabled"), IPv6: nativeEmptyFamily("disabled")}
	_, profile, err := nativeNetplanNode(data, p)
	if err != nil {
		return in, err
	}
	getBool := func(key string, fallback bool) (bool, error) {
		node := nativeYAMLGet(profile, key)
		if node == nil {
			return fallback, nil
		}
		if node.Kind != yaml.ScalarNode || node.Tag != "!!bool" {
			return false, errors.New("unsupported netplan boolean")
		}
		return nativeBoolean(node.Value, fallback)
	}
	dhcp4, err := getBool("dhcp4", false)
	if err != nil {
		return in, err
	}
	dhcp6, err := getBool("dhcp6", false)
	if err != nil {
		return in, err
	}
	ra, err := getBool("accept-ra", true)
	if err != nil {
		return in, err
	}
	if dhcp4 {
		in.IPv4.Method = "auto"
	}
	if dhcp6 {
		in.IPv6.Method = "dhcp"
	}
	if ra {
		if dhcp6 {
			in.IPv6.Method = "auto"
		} else {
			in.IPv6.Method = "slaac"
		}
	}
	if addresses := nativeYAMLGet(profile, "addresses"); addresses != nil {
		if addresses.Kind != yaml.SequenceNode {
			return in, errors.New("unsupported netplan addresses")
		}
		for _, node := range addresses.Content {
			if node.Kind != yaml.ScalarNode {
				return in, errors.New("advanced netplan address options require native review")
			}
			f := nativeFamily(&in, node.Value)
			f.Addresses = append(f.Addresses, node.Value)
		}
	}
	names := nativeYAMLGet(profile, "nameservers")
	if names != nil && names.Kind != yaml.MappingNode {
		return in, errors.New("unsupported netplan nameservers")
	}
	var domains []string
	if list := nativeYAMLGet(names, "addresses"); list != nil {
		if list.Kind != yaml.SequenceNode {
			return in, errors.New("unsupported netplan DNS list")
		}
		for _, node := range list.Content {
			if node.Kind != yaml.ScalarNode {
				return in, errors.New("unsupported netplan DNS entry")
			}
			f := nativeFamily(&in, node.Value)
			f.DNS = append(f.DNS, node.Value)
		}
	}
	if list := nativeYAMLGet(names, "search"); list != nil {
		if list.Kind != yaml.SequenceNode {
			return in, errors.New("unsupported netplan domain list")
		}
		for _, node := range list.Content {
			if node.Kind != yaml.ScalarNode {
				return in, errors.New("unsupported netplan domain entry")
			}
			domains = append(domains, node.Value)
		}
	}
	if routes := nativeYAMLGet(profile, "routes"); routes != nil {
		if routes.Kind != yaml.SequenceNode {
			return in, errors.New("unsupported netplan routes")
		}
		for _, node := range routes.Content {
			if node.Kind != yaml.MappingNode {
				return in, errors.New("unsupported netplan route")
			}
			for i := 0; i < len(node.Content); i += 2 {
				if !slices.Contains([]string{"to", "via", "metric", "table"}, node.Content[i].Value) {
					return in, errors.New("advanced netplan route attributes require native review")
				}
			}
			dest := nativeYAMLScalar(nativeYAMLGet(node, "to"))
			via := nativeYAMLScalar(nativeYAMLGet(node, "via"))
			if dest == "default" {
				dest = "0.0.0.0/0"
				if strings.Contains(via, ":") {
					dest = "::/0"
				}
			}
			metric, table := 0, nativeRouteTable(p)
			if raw := nativeYAMLScalar(nativeYAMLGet(node, "metric")); raw != "" {
				metric, err = strconv.Atoi(raw)
				if err != nil {
					return in, errors.New("unsupported netplan route metric")
				}
			}
			if raw := nativeYAMLScalar(nativeYAMLGet(node, "table")); raw != "" {
				table, err = strconv.Atoi(raw)
				if err != nil {
					return in, errors.New("unsupported netplan route table")
				}
			}
			f := nativeFamily(&in, dest)
			f.Routes = append(f.Routes, NativeRoute{Destination: dest, Gateway: via, Metric: metric, Table: table})
		}
	}
	for i, f := range []*NativeFamilyIntent{&in.IPv4, &in.IPv6} {
		if f.Method == "disabled" && len(f.Addresses) > 0 {
			f.Method = "manual"
		}
		if f.Method != "disabled" {
			f.Domains = append([]string{}, domains...)
		}
		key := "dhcp4-overrides"
		if i == 1 {
			key = "dhcp6-overrides"
		}
		overrides := nativeYAMLGet(profile, key)
		if raw := nativeYAMLGet(overrides, "use-dns"); raw != nil {
			value, err := nativeBoolean(raw.Value, true)
			if err != nil {
				return in, err
			}
			f.IgnoreAutoDNS = !value
		}
		if raw := nativeYAMLGet(overrides, "use-routes"); raw != nil {
			value, err := nativeBoolean(raw.Value, true)
			if err != nil {
				return in, err
			}
			f.IgnoreAutoRoutes = !value
		}
	}
	if nativeYAMLGet(profile, "gateway4") != nil || nativeYAMLGet(profile, "gateway6") != nil {
		return in, errors.New("legacy netplan gateways require native review before route conversion")
	}
	return normalizeNativeIntent(in)
}

func renderNativeNetplan(data []byte, p *nativeProfile, in NativeIntent) ([]byte, error) {
	node, profile, err := nativeNetplanNode(data, p)
	if err != nil {
		return nil, err
	}
	domains := in.IPv4.Domains
	if in.IPv4.Method == "disabled" {
		domains = in.IPv6.Domains
	} else if in.IPv6.Method != "disabled" && !slices.Equal(domains, in.IPv6.Domains) {
		return nil, errors.New("netplan has one per-link domain list; use the same domains for both enabled families")
	}
	dns := append(append([]string{}, in.IPv4.DNS...), in.IPv6.DNS...)
	addresses := append(append([]string{}, in.IPv4.Addresses...), in.IPv6.Addresses...)
	routes := []map[string]any{}
	for _, f := range []NativeFamilyIntent{in.IPv4, in.IPv6} {
		for _, r := range f.Routes {
			entry := map[string]any{"to": r.Destination, "metric": r.Metric, "table": r.Table}
			if r.Gateway != "" {
				entry["via"] = r.Gateway
			}
			routes = append(routes, entry)
		}
	}
	for key, value := range map[string]any{"dhcp4": in.IPv4.Method == "auto", "dhcp6": in.IPv6.Method == "auto" || in.IPv6.Method == "dhcp", "accept-ra": in.IPv6.Method == "auto" || in.IPv6.Method == "slaac", "addresses": addresses, "routes": routes} {
		if err := nativeYAMLSet(profile, key, value); err != nil {
			return nil, err
		}
	}
	names := nativeYAMLGet(profile, "nameservers")
	if names == nil {
		if err := nativeYAMLSet(profile, "nameservers", map[string]any{}); err != nil {
			return nil, err
		}
		names = nativeYAMLGet(profile, "nameservers")
	}
	if err := nativeYAMLSet(names, "addresses", dns); err != nil {
		return nil, err
	}
	if err := nativeYAMLSet(names, "search", domains); err != nil {
		return nil, err
	}
	for i, f := range []NativeFamilyIntent{in.IPv4, in.IPv6} {
		key := "dhcp4-overrides"
		if i == 1 {
			key = "dhcp6-overrides"
		}
		overrides := nativeYAMLGet(profile, key)
		if overrides == nil {
			if err := nativeYAMLSet(profile, key, map[string]any{}); err != nil {
				return nil, err
			}
			overrides = nativeYAMLGet(profile, key)
		}
		if err := nativeYAMLSet(overrides, "use-dns", !f.IgnoreAutoDNS); err != nil {
			return nil, err
		}
		if err := nativeYAMLSet(overrides, "use-routes", !f.IgnoreAutoRoutes); err != nil {
			return nil, err
		}
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(node); err != nil {
		return nil, err
	}
	if out.Len() > maxNativeProfileBytes {
		return nil, errors.New("staged netplan profile exceeds its bound")
	}
	return out.Bytes(), nil
}

func stageNativeNetplan(ctx context.Context, p *nativeProfile, candidate []byte) ([]byte, error) {
	files, err := nativeNetplanFiles()
	if err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp(nativeHostPath("/run"), "jd-native-stage-")
	if err != nil {
		return nil, errors.New("private native staging directory could not be created")
	}
	defer os.RemoveAll(root)
	for _, file := range files {
		target := filepath.Join(root, file.Path)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, err
		}
		data := file.Data
		if file.Path == p.File.Path {
			data = candidate
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return nil, err
		}
	}
	// The private root keeps generation away from the active manager files.
	hostRoot := strings.TrimPrefix(root, strings.TrimSuffix(nativeHostPath("/"), "/"))
	if !strings.HasPrefix(hostRoot, "/run/jd-native-stage-") {
		return nil, errors.New("private native staging root identity is invalid")
	}
	_, err = nativeExecute(ctx, nil, "netplan", "generate", "--root-dir", hostRoot)
	if err != nil {
		return nil, errors.New("netplan refused the staged supported profile; no native profile was changed")
	}
	file, err := nativeReadProfile(filepath.Join(hostRoot, p.Generated.Path))
	if err != nil {
		return nil, errors.New("netplan did not produce the exact selected native artifact with safe ownership")
	}
	want, err := parseNativeNetplan(candidate, p)
	if err != nil || nativeProfileFieldsGuard(p, file.Data, p.View.Renderer) != nil {
		return nil, errors.New("netplan produced renderer properties outside the verified native contract; nothing was applied")
	}
	var rendered NativeIntent
	if p.View.Renderer == "networkd" {
		rendered, err = parseNativeNetworkd(file.Data, p)
	} else {
		rendered, err = parseNativeNM(file.Data, p)
	}
	if err != nil || !nativeRenderedIntentEqual(rendered, want) {
		return nil, errors.New("netplan's selected renderer artifact does not preserve the requested supported intent; nothing was applied")
	}
	return file.Data, nil
}
