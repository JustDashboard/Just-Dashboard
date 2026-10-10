package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// TrafficExecutor can be the host runner or a verified pinned source namespace.
type TrafficExecutor func(context.Context, string, ...string) (string, error)

type TrafficRule struct {
	Priority         int      `json:"priority"`
	From             string   `json:"from"`
	To               string   `json:"to"`
	Table            string   `json:"table,omitempty"`
	Action           string   `json:"action,omitempty"`
	Mark             string   `json:"mark,omitempty"`
	UnknownSelectors []string `json:"unknownSelectors"`
}

// LookupTrafficRoute keeps the chosen DNS address, source, mark, protocol and
// destination port in one kernel request. This is a decision for this query's
// UID/source port, never proof that a packet or an application traversed it.
func (s *Service) LookupTrafficRoute(ctx context.Context, target, source, mark, protocol string, port int, execute TrafficExecutor) (RouteLookup, error) {
	addr, err := ParseAddr(target)
	if err != nil || (protocol != "tcp" && protocol != "udp") || port < 1 || port > 65535 {
		return RouteLookup{}, fmt.Errorf("a literal target, TCP/UDP and port from 1 to 65535 are required")
	}
	args := []string{"-j"}
	if addr.Is6() {
		args = append(args, "-6")
	}
	args = append(args, "route", "get", addr.String())
	if source != "" {
		src, err := ParseAddr(source)
		if err != nil || src.Is4() != addr.Is4() {
			return RouteLookup{}, fmt.Errorf("the source and destination must have the same family")
		}
		args = append(args, "from", src.String())
	}
	if mark != "" {
		mark, err = canonicalFWMark(mark)
		if err != nil {
			return RouteLookup{}, err
		}
		for _, c := range mark {
			if c == '/' {
				return RouteLookup{}, fmt.Errorf("the route-query mark cannot have a mask")
			}
		}
		args = append(args, "mark", mark)
	}
	args = append(args, "ipproto", protocol, "dport", strconv.Itoa(port))
	if execute == nil {
		execute = run
	}
	out, err := execute(ctx, "ip", args...)
	if err != nil {
		return RouteLookup{}, err
	}
	p, err := parseRouteGet(out, Path{Address: addr.String()})
	if err != nil {
		return RouteLookup{}, err
	}
	if source != "" {
		p.Source = source
	}
	var rows []struct {
		Table ipTable `json:"table"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 {
		return RouteLookup{}, fmt.Errorf("the kernel returned an ambiguous route decision")
	}
	table, _ := tableOf(rows[0].Table, map[int]string{254: "main", 255: "local"}, map[string]int{"main": 254, "local": 255, "default": 253})
	if p.Local {
		table = tableLocal
	}
	return RouteLookup{Path: p, Family: familyOf(addr), Table: table, Mark: mark}, nil
}

func (s *Service) TrafficRules(ctx context.Context, family string, execute TrafficExecutor) ([]TrafficRule, error) {
	if family != "inet" && family != "inet6" {
		return nil, fmt.Errorf("select IPv4 or IPv6")
	}
	if execute == nil {
		execute = run
	}
	flag := "-4"
	if family == "inet6" {
		flag = "-6"
	}
	out, err := execute(ctx, "ip", "-j", flag, "rule", "show")
	if err != nil {
		return nil, err
	}
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil || len(raw) > 1024 {
		return nil, fmt.Errorf("the policy rule listing is unreadable or exceeds 1024 rules")
	}
	known := map[string]bool{"priority": true, "src": true, "dst": true, "table": true, "action": true, "fwmark": true, "fwmask": true, "protocol": true}
	rules := make([]TrafficRule, 0, len(raw))
	for _, row := range raw {
		r := TrafficRule{UnknownSelectors: []string{}}
		_ = json.Unmarshal(row["priority"], &r.Priority)
		_ = json.Unmarshal(row["src"], &r.From)
		_ = json.Unmarshal(row["dst"], &r.To)
		_ = json.Unmarshal(row["action"], &r.Action)
		if mark := row["fwmark"]; len(mark) > 0 {
			var value any
			if json.Unmarshal(mark, &value) == nil {
				r.Mark = fmt.Sprint(value)
			}
		}
		if mask := row["fwmask"]; len(mask) > 0 {
			var value any
			if json.Unmarshal(mask, &value) == nil {
				r.Mark += "/" + fmt.Sprint(value)
			}
		}
		if table := row["table"]; len(table) > 0 {
			var value any
			if err := json.Unmarshal(table, &value); err == nil {
				r.Table = fmt.Sprint(value)
			}
		}
		for key := range row {
			if !known[key] {
				r.UnknownSelectors = append(r.UnknownSelectors, key)
			}
		}
		sort.Strings(r.UnknownSelectors)
		rules = append(rules, r)
	}
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].Priority < rules[j].Priority })
	return rules, nil
}
