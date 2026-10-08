package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var driftGatewayComment = regexp.MustCompile(`comment "([^"]+)"`)

type driftNFTListing struct {
	Nftables []struct {
		Table *struct{ Family, Name string } `json:"table"`
		Chain *struct {
			Family, Table, Name, Type, Hook, Policy string
			Priority                                json.RawMessage `json:"prio"`
		} `json:"chain"`
		Rule *struct {
			Family, Table, Chain, Comment string
			Expr                          json.RawMessage `json:"expr"`
		} `json:"rule"`
		Set *struct{ Family, Table, Name string } `json:"set"`
	} `json:"nftables"`
}

func (s *Service) driftGatewayRender(sp *Spec) (string, error) {
	candidate := map[int][]netip.Prefix{}
	for _, bl := range sp.Blocklists {
		if !bl.Enabled {
			continue
		}
		nets, health := blocklistData(filepath.Join(s.paths.Dir, "lists"), bl)
		if health.Status != "ready" {
			return "", fmt.Errorf("blocklist %d cannot be rendered safely: %s", bl.ID, health.Error)
		}
		candidate[bl.ID] = nets
	}
	return renderGatewayWith(sp, s.trustedFor(sp), func(bl BlocklistSpec) []netip.Prefix { return candidate[bl.ID] })
}

// Structural coverage proves missing owned objects and base-chain changes.
// It deliberately cannot certify arbitrary rule expression equality. In
// particular, retained comments do not prove that DNAT targets or limit
// thresholds still match. Exact blocklist contents have separate evidence.
func (s *Service) driftGateway(ctx context.Context, sp *Spec, rendered string, renderErr error) []DriftObservation {
	o := observation("gateway", gatewayTable)
	o.Coverage = "structure"
	if renderErr != nil {
		o.Reason = renderErr.Error()
		return []DriftObservation{o}
	}
	listing, err := run(ctx, "nft", "-t", "-j", "list", "ruleset")
	if err != nil {
		o.Status, o.Reason = "unreadable", err.Error()
		return []DriftObservation{o}
	}
	var live driftNFTListing
	if json.Unmarshal([]byte(listing), &live) != nil || live.Nftables == nil {
		o.Status, o.Reason = "unreadable", "nft did not return a valid ruleset inventory."
		return []DriftObservation{o}
	}
	present := false
	chains := map[string]struct{ hook, typ, policy, priority string }{}
	comments, ruleCounts, sets := map[string]int{}, map[string]int{}, map[string]bool{}
	for _, object := range live.Nftables {
		if object.Table != nil && object.Table.Family == "inet" && object.Table.Name == gatewayTable {
			present = true
		}
		if c := object.Chain; c != nil && c.Family == "inet" && c.Table == gatewayTable {
			chains[c.Name] = struct{ hook, typ, policy, priority string }{c.Hook, c.Type, c.Policy, string(c.Priority)}
		}
		if r := object.Rule; r != nil && r.Family == "inet" && r.Table == gatewayTable {
			comments[r.Chain+"/"+r.Comment]++
			ruleCounts[r.Chain]++
		}
		if set := object.Set; set != nil && set.Family == "inet" && set.Table == gatewayTable {
			sets[set.Name] = true
		}
	}
	if !present {
		o.Status, o.Repairable, o.Reason = "missing", true, "The managed gateway table is absent."
		return []DriftObservation{o}
	}
	expectedComments, expectedCounts := map[string]int{}, map[string]int{}
	expectedSets := map[string]bool{}
	chain := ""
	for _, line := range strings.Split(rendered, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "set ") {
			name := strings.Fields(line)[1]
			expectedSets[name] = true
		}
		if strings.HasPrefix(line, "chain ") {
			chain = strings.Fields(line)[1]
			expectedCounts[chain] = 0
			continue
		}
		if chain != "" && line == "}" {
			chain = ""
			continue
		}
		if chain == "" || strings.HasPrefix(line, "type ") {
			continue
		}
		if line != "" {
			expectedCounts[chain]++
			comment := ""
			if match := driftGatewayComment.FindStringSubmatch(line); len(match) > 1 {
				comment = match[1]
			}
			expectedComments[chain+"/"+comment]++
		}
	}
	mismatch := []string{}
	for name, count := range expectedCounts {
		c, exists := chains[name]
		if !exists {
			mismatch = append(mismatch, "missing chain "+name)
			continue
		}
		hook, typ, priority := name, "filter", "-10"
		switch name {
		case "pre":
			hook, priority = "prerouting", "-150"
		case "nat_pre":
			hook, typ, priority = "prerouting", "nat", "-110"
		case "nat_post":
			hook, typ, priority = "postrouting", "nat", "90"
		}
		if c.hook != hook || c.typ != typ || c.policy != "accept" || c.priority != priority {
			mismatch = append(mismatch, "changed base chain "+name)
		}
		if ruleCounts[name] != count {
			mismatch = append(mismatch, "changed rule count in "+name)
		}
	}
	for name := range chains {
		if _, expected := expectedCounts[name]; !expected {
			mismatch = append(mismatch, "unexpected chain "+name)
		}
	}
	for comment, count := range expectedComments {
		if comments[comment] != count {
			mismatch = append(mismatch, "changed owned rule identity "+comment)
		}
	}
	for name := range expectedSets {
		if !sets[name] {
			mismatch = append(mismatch, "missing set "+name)
		}
	}
	for name := range sets {
		if !expectedSets[name] {
			mismatch = append(mismatch, "unexpected set "+name)
		}
	}
	if len(mismatch) > 0 {
		sort.Strings(mismatch)
		o.Status, o.Repairable, o.Reason = "drift", true, strings.Join(mismatch, "; ")
	} else {
		o.Reason = "Owned gateway structure is present; rule expression equality has not been established."
	}
	return []DriftObservation{o}
}
