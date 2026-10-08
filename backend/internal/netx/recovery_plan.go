package netx

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// recoveryPlan limits undo to the managed objects changed by this commit.
// Replaying the entire spec would replace unaffected resources and could
// overwrite a concurrent native-manager change.
func (s *Service) recoveryPlan(ctx context.Context, old, next *Spec) ([]recoveryCommand, error) {
	var commands []recoveryCommand
	add := func(tool string, args []string, gone, exists bool) {
		commands = append(commands, recoveryCommand{Tool: tool, Args: args, AllowGone: gone, AllowExists: exists})
	}
	routeKey := func(r RouteSpec) string { return strconv.Itoa(r.ID) }
	for _, r := range differing(next.Routes, old.Routes, routeKey) {
		args, err := routeCommand("del", r)
		if err != nil {
			return nil, err
		}
		add("ip", args, true, false)
	}
	ruleKey := func(r RuleSpec) string { return strconv.Itoa(r.ID) }
	for _, r := range differing(next.Rules, old.Rules, ruleKey) {
		args, err := ruleArgs(r)
		if err != nil {
			return nil, err
		}
		add("ip", append(append(familyArgs(r.Family), "rule", "del"), args...), true, false)
	}
	addressKey := func(a AddressSpec) string { return strconv.Itoa(a.ID) }
	for _, a := range differing(next.Addresses, old.Addresses, addressKey) {
		add("ip", addressRecoveryArgs(next, a, "del"), true, false)
	}
	oldLinks, nextLinks := map[string]LinkSpec{}, map[string]LinkSpec{}
	for _, l := range old.Links {
		oldLinks[l.Name] = l
	}
	for _, l := range next.Links {
		nextLinks[l.Name] = l
	}
	// Children disappear before parents. A deleted veth also removes its peer.
	for i := len(next.Links) - 1; i >= 0; i-- {
		l := next.Links[i]
		if _, existed := oldLinks[l.Name]; !existed {
			add("ip", []string{"link", "del", l.Name}, true, false)
		}
	}
	for _, ns := range differing(next.Namespaces, old.Namespaces, func(n NamespaceSpec) string { return n.Name }) {
		add("ip", []string{"netns", "del", ns.Name}, true, false)
	}
	for _, ns := range differing(old.Namespaces, next.Namespaces, func(n NamespaceSpec) string { return n.Name }) {
		add("ip", []string{"netns", "add", ns.Name}, false, true)
	}
	var restoreLinks []LinkSpec
	var properties []recoveryCommand
	for _, l := range old.Links {
		after, exists := nextLinks[l.Name]
		if !exists {
			restoreLinks = append(restoreLinks, l)
			continue
		}
		if l.Up != after.Up {
			state := "down"
			if l.Up {
				state = "up"
			}
			properties = append(properties, recoveryCommand{Tool: "ip", Args: []string{"link", "set", l.Name, state}})
		}
		if l.MTU != after.MTU && l.MTU > 0 {
			properties = append(properties, recoveryCommand{Tool: "ip", Args: []string{"link", "set", l.Name, "mtu", strconv.Itoa(l.MTU)}})
		}
		if l.Master != after.Master {
			args := []string{"link", "set", l.Name, "nomaster"}
			if l.Master != "" {
				args = []string{"link", "set", l.Name, "master", l.Master}
			}
			properties = append(properties, recoveryCommand{Tool: "ip", Args: args})
		}
		beforeAddresses, afterAddresses := map[string]bool{}, map[string]bool{}
		for _, addr := range l.Addresses {
			beforeAddresses[addr] = true
		}
		for _, addr := range after.Addresses {
			afterAddresses[addr] = true
		}
		for _, addr := range after.Addresses {
			if !beforeAddresses[addr] {
				add("ip", []string{"addr", "del", addr, "dev", l.Name}, true, false)
			}
		}
		for _, addr := range l.Addresses {
			if !afterAddresses[addr] {
				properties = append(properties, recoveryCommand{Tool: "ip", Args: []string{"addr", "add", addr, "dev", l.Name}, AllowExists: true})
			}
		}
	}
	for _, line := range batchLines(&Spec{Links: restoreLinks}) {
		add("ip", strings.Fields(line), false, true)
	}
	commands = append(commands, properties...)
	for _, a := range differing(old.Addresses, next.Addresses, addressKey) {
		add("ip", addressRecoveryArgs(old, a, "add"), false, true)
	}
	for _, r := range differing(old.Routes, next.Routes, routeKey) {
		args, err := routeCommand("add", r)
		if err != nil {
			return nil, err
		}
		add("ip", args, false, true)
	}
	for _, r := range differing(old.Rules, next.Rules, ruleKey) {
		args, err := ruleArgs(r)
		if err != nil {
			return nil, err
		}
		add("ip", append(append(familyArgs(r.Family), "rule", "add"), args...), false, true)
	}
	// Snapshot actual kernel values, including keys not owned before this
	// change. Turning forwarding on also resets a family of kernel settings.
	var keys []string
	for key, value := range next.Sysctls {
		if old.Sysctls[key] != value {
			keys = append(keys, key)
		}
	}
	for key := range old.Sysctls {
		if _, exists := next.Sysctls[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		value, err := readSysctl(key)
		if err != nil {
			return nil, fmt.Errorf("reading %s for recovery: %w", key, err)
		}
		add("sysctl", []string{"-w", key + "=" + value}, false, false)
	}
	shapeKey := func(sh ShapeSpec) string { return sh.Device }
	for _, sh := range differing(next.Shaping, old.Shaping, shapeKey) {
		if sh.hasRoot() {
			add("tc", []string{"qdisc", "del", "dev", sh.Device, "root"}, true, false)
		}
		if sh.IngressKbit > 0 {
			add("tc", []string{"qdisc", "del", "dev", sh.Device, "ingress"}, true, false)
		}
	}
	for _, sh := range differing(old.Shaping, next.Shaping, shapeKey) {
		for _, line := range shapeLines(sh) {
			args := strings.Fields(line)
			add("tc", args, len(args) > 1 && args[1] == "del", false)
		}
	}
	if !reflect.DeepEqual(old.Forwards, next.Forwards) || !reflect.DeepEqual(old.NAT, next.NAT) || !reflect.DeepEqual(old.Limits, next.Limits) || !reflect.DeepEqual(old.Blocklists, next.Blocklists) || !reflect.DeepEqual(old.Trusted, next.Trusted) {
		if gatewayEmpty(old) {
			add("nft", []string{"delete", "table", "inet", gatewayTable}, true, false)
		} else {
			// The saved render is restored first. It contains the known-good
			// fetched list even if its external cache has subsequently vanished.
			add("nft", []string{"-f", filepath.Join(s.paths.Dir, gatewayFile)}, false, false)
		}
		if needsAdmission(old) || needsAdmission(next) {
			for _, cmd := range admissionCommands(needsAdmission(old)) {
				add(cmd[0], cmd[1:], cmd[1] == "-D", false)
			}
		}
	}
	return commands, nil
}

func addressRecoveryArgs(sp *Spec, a AddressSpec, verb string) []string {
	args := []string{"addr", verb, a.CIDR, "dev", a.Link}
	if peer, ok := peerOf(sp.Links, a.Link); ok {
		return peerCommand(peer, args...)
	}
	return args
}

func differing[T any](left, right []T, key func(T) string) []T {
	byKey := map[string]T{}
	for _, item := range right {
		byKey[key(item)] = item
	}
	var out []T
	for _, item := range left {
		prior, exists := byKey[key(item)]
		if !exists || !reflect.DeepEqual(prior, item) {
			out = append(out, item)
		}
	}
	return out
}
