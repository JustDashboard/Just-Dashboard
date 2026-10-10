package netx

import (
	"context"
	"encoding/json"
	"fmt"
)

// RouteLookup is a kernel decision, not a connectivity measurement. In
// particular, it says nothing about a firewall, remote listener or DNS.
type RouteLookup struct {
	Path
	Family string `json:"family"`
	Table  int    `json:"table,omitempty"`
	Mark   string `json:"mark,omitempty"`
}

// LookupRoute asks the kernel without sending a packet. Literal addresses
// keep this read from accidentally resolving or disclosing a private name.
func (s *Service) LookupRoute(ctx context.Context, target, source, mark string) (RouteLookup, error) {
	addr, err := ParseAddr(target)
	if err != nil {
		return RouteLookup{}, err
	}
	args := []string{"-j"}
	if addr.Is6() {
		args = append(args, "-6")
	}
	args = append(args, "route", "get", addr.String())
	if source != "" {
		src, err := ParseAddr(source)
		if err != nil {
			return RouteLookup{}, err
		}
		if src.Is4() != addr.Is4() {
			return RouteLookup{}, fmt.Errorf("the source and target must use the same address family")
		}
		args = append(args, "from", src.String())
	}
	if mark != "" {
		mark, err = canonicalFWMark(mark)
		if err != nil {
			return RouteLookup{}, err
		}
		// route get takes a value, whereas policy rules also accept a mask.
		for _, c := range mark {
			if c == '/' {
				return RouteLookup{}, fmt.Errorf("a route lookup mark is a value without a mask")
			}
		}
		args = append(args, "mark", mark)
	}
	out, err := run(ctx, "ip", args...)
	if err != nil {
		return RouteLookup{}, fmt.Errorf("the kernel could not select a route to %s: %w", addr, err)
	}
	p, err := parseRouteGet(out, Path{Address: addr.String()})
	if err != nil {
		return RouteLookup{}, err
	}
	var rows []struct {
		Table ipTable `json:"table"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 {
		return RouteLookup{}, fmt.Errorf("ip route get printed an ambiguous decision")
	}
	table, _ := tableOf(rows[0].Table, map[int]string{254: "main", 255: "local"}, map[string]int{"main": 254, "local": 255, "default": 253})
	if p.Local {
		table = tableLocal
	}
	return RouteLookup{Path: p, Family: familyOf(addr), Table: table, Mark: mark}, nil
}
