package netx

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type nativeNetplanProtocolPolicy struct {
	DNS, Routes bool
	Domains     string
	MTU         bool
}

type nativeNetplanAutomaticPolicy struct {
	DHCP4, DHCP6, RA nativeNetplanProtocolPolicy
	DHCP4Active      bool
	DHCP6Active      bool
	RAActive         bool
}

func nativeNetplanPolicyBoolean(node *yaml.Node, fallback bool) (bool, error) {
	if node == nil {
		return fallback, nil
	}
	if node.Kind != yaml.ScalarNode || node.Tag != "!!bool" {
		return false, errors.New("the selected Netplan automatic policy requires literal booleans")
	}
	return nativeBoolean(node.Value, fallback)
}

func nativeDomainPolicyValue(value string) (string, error) {
	if value == "" || value == "route" {
		return value, nil
	}
	use, err := nativeBoolean(value, false)
	if err != nil {
		return "", errors.New("unsupported native automatic domain policy")
	}
	if use {
		return "yes", nil
	}
	return "no", nil
}

func nativeNetplanProtocolOverrides(node *yaml.Node, ra bool) (nativeNetplanProtocolPolicy, error) {
	policy := nativeNetplanProtocolPolicy{DNS: true, Routes: true, MTU: true}
	if node == nil {
		return policy, nil
	}
	if node.Kind != yaml.MappingNode {
		return policy, errors.New("the selected Netplan automatic overrides must be a closed mapping")
	}
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i].Value, node.Content[i+1]
		var err error
		switch key {
		case "use-dns":
			policy.DNS, err = nativeNetplanPolicyBoolean(value, true)
		case "use-domains":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!bool" && !(value.Tag == "!!str" && value.Value == "route") {
				return policy, errors.New("the selected Netplan domain policy requires a boolean or literal route")
			}
			policy.Domains, err = nativeDomainPolicyValue(value.Value)
		case "use-routes":
			if ra {
				return policy, errors.New("this Netplan version cannot author the selected RA route suppression")
			}
			policy.Routes, err = nativeNetplanPolicyBoolean(value, true)
		case "use-mtu":
			if ra {
				return policy, errors.New("this Netplan version cannot author the selected RA MTU policy")
			}
			policy.MTU, err = nativeNetplanPolicyBoolean(value, true)
		default:
			return policy, errors.New("the selected Netplan automatic policy contains unverified native effects")
		}
		if err != nil {
			return policy, err
		}
	}
	return policy, nil
}

// Shared generated DHCP settings are eligible only with their exact authored
// standalone origin. The same fields in a direct networkd file stay refused.
func nativeReadNetplanAutomaticPolicy(p *nativeProfile, data []byte) (nativeNetplanAutomaticPolicy, error) {
	var policy nativeNetplanAutomaticPolicy
	if p == nil || p.View.Owner != "netplan" || p.View.Renderer != "networkd" || p.NetplanSection != "ethernets" || p.View.Contract.Master != "" || len(p.View.Contract.Members) != 0 || p.View.Contract.BondMode != "" || p.View.Contract.VRFTable != 0 {
		return policy, errors.New("shared native automatic policy requires the exact standalone Netplan/networkd origin")
	}
	_, profile, err := nativeNetplanNode(data, p)
	if err != nil {
		return policy, err
	}
	policy.DHCP4Active, err = nativeNetplanPolicyBoolean(nativeYAMLGet(profile, "dhcp4"), false)
	if err != nil {
		return policy, err
	}
	policy.DHCP6Active, err = nativeNetplanPolicyBoolean(nativeYAMLGet(profile, "dhcp6"), false)
	if err != nil {
		return policy, err
	}
	policy.RAActive, err = nativeNetplanPolicyBoolean(nativeYAMLGet(profile, "accept-ra"), true)
	if err != nil {
		return policy, err
	}
	for _, override := range []struct {
		Key    string
		Target *nativeNetplanProtocolPolicy
		RA     bool
	}{{"dhcp4-overrides", &policy.DHCP4, false}, {"dhcp6-overrides", &policy.DHCP6, false}, {"ra-overrides", &policy.RA, true}} {
		*override.Target, err = nativeNetplanProtocolOverrides(nativeYAMLGet(profile, override.Key), override.RA)
		if err != nil {
			return policy, err
		}
	}
	if policy.DHCP4Active && policy.DHCP4.MTU || policy.DHCP6Active && policy.DHCP6.MTU {
		return policy, errors.New("DHCP-provided MTU requires a separately verified native link contract")
	}
	if policy.DHCP4Active && policy.DHCP6Active && policy.DHCP4 != policy.DHCP6 {
		return policy, errors.New("the selected Netplan networkd owner requires matching DHCP4/DHCP6 overrides")
	}
	if (policy.DHCP6Active || policy.RAActive) && !policy.DHCP6.Routes {
		return policy, errors.New("the selected Netplan DHCP6 override does not represent RA route suppression")
	}
	if policy.DHCP6Active && policy.RAActive && policy.DHCP6.DNS != policy.RA.DNS {
		return policy, errors.New("separate DHCP6 and RA DNS preferences require their native owner")
	}
	return policy, nil
}

func nativeNetplanRetainsAutomaticPolicy(p *nativeProfile, before, candidate []byte) error {
	a, err := nativeReadNetplanAutomaticPolicy(p, before)
	if err != nil {
		return err
	}
	b, err := nativeReadNetplanAutomaticPolicy(p, candidate)
	if err != nil {
		return err
	}
	for _, pair := range [][2]nativeNetplanProtocolPolicy{{a.DHCP4, b.DHCP4}, {a.DHCP6, b.DHCP6}, {a.RA, b.RA}} {
		if pair[0].Domains != pair[1].Domains || pair[0].MTU != pair[1].MTU {
			return errors.New("native staging changed the retained authored automatic domain or MTU policy")
		}
	}
	return nativeNetplanRetainsSelectedOrigin(p, before, candidate)
}

type nativeYAMLSemanticValue struct {
	Kind     yaml.Kind                          `json:"kind"`
	Tag      string                             `json:"tag"`
	Value    string                             `json:"value,omitempty"`
	Mapping  map[string]nativeYAMLSemanticValue `json:"mapping,omitempty"`
	Sequence []nativeYAMLSemanticValue          `json:"sequence,omitempty"`
}

func nativeYAMLSemantic(node *yaml.Node) nativeYAMLSemanticValue {
	value := nativeYAMLSemanticValue{Kind: node.Kind, Tag: node.Tag, Value: node.Value}
	if node.Kind == yaml.MappingNode {
		value.Mapping = map[string]nativeYAMLSemanticValue{}
		for i := 0; i < len(node.Content); i += 2 {
			key, _ := json.Marshal(nativeYAMLSemantic(node.Content[i]))
			value.Mapping[string(key)] = nativeYAMLSemantic(node.Content[i+1])
		}
	} else {
		for _, child := range node.Content {
			value.Sequence = append(value.Sequence, nativeYAMLSemantic(child))
		}
	}
	return value
}

func nativeYAMLRemoveFields(node *yaml.Node, keys ...string) {
	var content []*yaml.Node
	for i := 0; i < len(node.Content); i += 2 {
		if !slices.Contains(keys, node.Content[i].Value) {
			content = append(content, node.Content[i], node.Content[i+1])
		}
	}
	node.Content = content
}

// YAML formatting can change during a selected edit. Compare every remaining
// semantic value so another definition or an unrepresented selected property
// cannot hitch a ride in the full authored file written by recovery.
func nativeNetplanRetainsSelectedOrigin(p *nativeProfile, before, candidate []byte) error {
	var evidence [2]string
	for i, data := range [][]byte{before, candidate} {
		node, profile, err := nativeNetplanNode(data, p)
		if err != nil {
			return err
		}
		nativeYAMLRemoveFields(profile, "dhcp4", "dhcp6", "accept-ra", "addresses", "routes")
		for _, field := range []struct {
			Key  string
			Keys []string
		}{{"nameservers", []string{"addresses", "search"}}, {"dhcp4-overrides", []string{"use-dns", "use-routes"}}, {"dhcp6-overrides", []string{"use-dns", "use-routes"}}, {"ra-overrides", []string{"use-dns"}}} {
			child := nativeYAMLGet(profile, field.Key)
			if child == nil {
				continue
			}
			if child.Kind != yaml.MappingNode {
				return errors.New("unreadable authored Netplan property scope")
			}
			nativeYAMLRemoveFields(child, field.Keys...)
			if len(child.Content) == 0 {
				nativeYAMLRemoveFields(profile, field.Key)
			}
		}
		encoded, err := json.Marshal(nativeYAMLSemantic(node))
		if err != nil {
			return errors.New("the retained authored Netplan scope could not be compared")
		}
		evidence[i] = string(encoded)
	}
	if evidence[0] != evidence[1] {
		return errors.New("native staging changed an unselected Netplan definition or retained selected property")
	}
	return nil
}

func nativeNetplanGeneratedPolicyValue(blocks nativeINI, section, key string, shared bool) (string, error) {
	value, err := blocks.one(section, key)
	if err != nil || !shared {
		return value, err
	}
	alias, err := blocks.one("DHCP", key)
	if err != nil {
		return "", err
	}
	if key == "UseDomains" {
		value, err = nativeDomainPolicyValue(value)
		if err == nil {
			alias, err = nativeDomainPolicyValue(alias)
		}
	} else {
		for _, target := range []*string{&value, &alias} {
			if *target == "" {
				continue
			}
			parsed, parseErr := nativeBoolean(*target, false)
			if parseErr != nil {
				return "", parseErr
			}
			*target = "no"
			if parsed {
				*target = "yes"
			}
		}
	}
	if err != nil {
		return "", err
	}
	if value != "" && alias != "" && value != alias {
		return "", errors.New("shared and per-protocol generated native policy disagree")
	}
	if value == "" {
		value = alias
	}
	return value, nil
}

func nativeNetplanGeneratedAutomaticPolicy(p *nativeProfile, authored, generated []byte) (map[string]string, error) {
	policy, err := nativeReadNetplanAutomaticPolicy(p, authored)
	if err != nil {
		return nil, err
	}
	blocks, err := parseNativeINI(generated)
	if err != nil || len(blocks.values("Network", "UseDomains")) != 0 {
		return nil, errors.New("unreadable or inherited generated native domain policy")
	}
	for _, section := range []string{"DHCPv4", "DHCPv6"} {
		for _, key := range []string{"UseMTU", "UseRoutes", "RouteMetric"} {
			if len(blocks.values(section, key)) != 0 {
				return nil, errors.New("per-protocol generated MTU/route/metric policy is outside this captured Netplan contract")
			}
		}
	}
	for _, block := range blocks {
		if block.Section != "DHCP" {
			continue
		}
		if !policy.DHCP4Active && !policy.DHCP6Active {
			return nil, errors.New("inactive generated DHCP policy has no matching authored effect")
		}
		for _, line := range block.Lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
				continue
			}
			key, _, property := strings.Cut(trimmed, "=")
			if property && !slices.Contains([]string{"RouteMetric", "UseMTU", "UseDomains", "UseDNS", "UseRoutes"}, strings.TrimSpace(key)) {
				return nil, errors.New("the generated shared DHCP block contains unrepresented native effects")
			}
		}
	}
	if policy.DHCP4Active || policy.DHCP6Active {
		metric, err := blocks.one("DHCP", "RouteMetric")
		mtu, mtuErr := nativeNetplanGeneratedPolicyValue(blocks, "DHCPv4", "UseMTU", true)
		if err != nil || mtuErr != nil || metric != "100" || mtu != "no" {
			return nil, errors.New("the generated native DHCP metric/MTU differs from its closed Netplan contract")
		}
	}
	domains := map[string]string{}
	for _, protocol := range []struct {
		Section string
		Policy  nativeNetplanProtocolPolicy
		Active  bool
		Shared  bool
	}{{"DHCPv4", policy.DHCP4, policy.DHCP4Active, true}, {"DHCPv6", policy.DHCP6, policy.DHCP6Active, true}, {"IPv6AcceptRA", policy.RA, policy.RAActive, false}} {
		value, err := nativeNetplanGeneratedPolicyValue(blocks, protocol.Section, "UseDomains", protocol.Shared)
		if err == nil {
			value, err = nativeDomainPolicyValue(value)
		}
		if err != nil || protocol.Active && value != protocol.Policy.Domains {
			return nil, errors.New("the generated native domain policy differs from its exact authored protocol")
		}
		if !protocol.Active {
			if !protocol.Shared && value != "" && value != protocol.Policy.Domains {
				return nil, errors.New("inactive generated RA domain policy differs from its retained authored value")
			}
			continue
		}
		domains[protocol.Section] = value
		dns, err := nativeNetplanGeneratedPolicyValue(blocks, protocol.Section, "UseDNS", protocol.Shared)
		useDNS, boolErr := nativeBoolean(dns, true)
		if err != nil || boolErr != nil || useDNS != protocol.Policy.DNS {
			return nil, errors.New("the generated native DNS preference differs from its exact authored protocol")
		}
		if protocol.Section == "DHCPv4" {
			routes, err := nativeNetplanGeneratedPolicyValue(blocks, protocol.Section, "UseRoutes", true)
			useRoutes, boolErr := nativeBoolean(routes, true)
			if err != nil || boolErr != nil || useRoutes != protocol.Policy.Routes {
				return nil, errors.New("the generated native route preference differs from its exact authored protocol")
			}
		} else if protocol.Section == "IPv6AcceptRA" {
			for _, key := range []string{"UseGateway", "UseRoutePrefix", "UseOnLinkPrefix"} {
				value, err := blocks.one(protocol.Section, key)
				use, boolErr := nativeBoolean(value, true)
				if err != nil || boolErr != nil || !use {
					return nil, errors.New("the generated RA route preference has no matching authored Netplan contract")
				}
			}
		}
	}
	for _, section := range []string{"DHCPv4", "DHCPv6"} {
		for _, key := range []string{"UseDomains", "UseDNS"} {
			if _, err := nativeNetplanGeneratedPolicyValue(blocks, section, key, true); err != nil {
				return nil, err
			}
		}
	}
	return domains, nil
}
