package netsec

import (
	"context"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// fail2ban, CrowdSec and the firewall each keep their own list of refused
// addresses, and a host running two of them holds many addresses twice: the
// brute-forcer fail2ban banned an hour ago is also a CrowdSec decision, and
// sits inside a /24 somebody denied in ufw last month. Each page shows its own
// list and none of them says so. MergeBlocks folds the three into one list by
// address, naming every engine holding each one and the broader blocks that
// already cover it, so a second ban can be seen to add nothing.

// BlockSource is one engine's hold on an address.
type BlockSource struct {
	// Engine is fail2ban, crowdsec or firewall.
	Engine string `json:"engine"`
	// Ref is the jail, the decision id or the rule number.
	Ref    string `json:"ref"`
	Detail string `json:"detail,omitempty"`
	// Origin is CrowdSec's: crowdsec, cscli, CAPI or lists.
	Origin    string `json:"origin,omitempty"`
	Until     string `json:"until,omitempty"`
	Community bool   `json:"community,omitempty"`
}

// BlockedEntry is one address or range and everything holding it.
type BlockedEntry struct {
	Value   string        `json:"value"`
	Range   bool          `json:"range"`
	Sources []BlockSource `json:"sources"`
	Engines []string      `json:"engines"`
	// CoveredBy are broader blocks, from any engine, that already contain
	// this one.
	CoveredBy []string `json:"coveredBy,omitempty"`
}

// EngineRead says whether an engine's list was read, and how many entries it
// gave, so an engine that could not be read is not mistaken for one holding
// nothing.
type EngineRead struct {
	Engine string `json:"engine"`
	Read   bool   `json:"read"`
	Count  int    `json:"count"`
	Note   string `json:"note,omitempty"`
}

// BlocksView is the merged list.
type BlocksView struct {
	Entries []BlockedEntry `json:"entries"`
	// Distinct is how many addresses and ranges are held across every
	// engine; Duplicated how many of the listed are held more than once; and
	// Covered how many sit inside a broader block.
	Distinct   int `json:"distinct"`
	Duplicated int `json:"duplicated"`
	Covered    int `json:"covered"`
	// CommunityOnly counts CrowdSec community decisions no other engine or
	// local decision touches; they are counted rather than listed.
	CommunityOnly int          `json:"communityOnly"`
	Truncated     bool         `json:"truncated"`
	Engines       []EngineRead `json:"engines"`
}

// maxBlockEntries bounds the listed entries.
const maxBlockEntries = 500

// MergeBlocks folds the three engines' lists into one. Any argument may be
// nil, which is an engine that was not read.
func MergeBlocks(f2b *Fail2banStatus, decisions []CrowdSecDecision, crowdsecRead bool, fw *FirewallStatus) BlocksView {
	type held struct {
		prefix  netip.Prefix
		sources []BlockSource
	}
	entries := map[netip.Prefix]*held{}
	add := func(value string, src BlockSource) bool {
		p, ok := refusedPrefix(value)
		if !ok {
			return false
		}
		h := entries[p]
		if h == nil {
			h = &held{prefix: p}
			entries[p] = h
		}
		h.sources = append(h.sources, src)
		return true
	}
	v := BlocksView{Entries: []BlockedEntry{}, Engines: []EngineRead{}}

	f2bRead := EngineRead{Engine: "fail2ban"}
	if f2b != nil && f2b.Running {
		f2bRead.Read = true
		for _, j := range f2b.Jails {
			for _, ip := range j.BannedIPs {
				if add(ip, BlockSource{Engine: "fail2ban", Ref: j.Name}) {
					f2bRead.Count++
				}
			}
		}
	} else if f2b != nil && f2b.Available {
		f2bRead.Note = "not running"
	} else {
		f2bRead.Note = "not installed"
	}
	v.Engines = append(v.Engines, f2bRead)

	csRead := EngineRead{Engine: "crowdsec", Read: crowdsecRead}
	if !crowdsecRead {
		csRead.Note = "not read"
	}
	for _, d := range decisions {
		scope := strings.ToLower(d.Scope)
		if !strings.EqualFold(d.Type, "ban") || (scope != "ip" && scope != "range") {
			continue
		}
		community := d.Origin == "CAPI" || d.Origin == "lists"
		if add(d.Value, BlockSource{Engine: "crowdsec", Ref: strconv.FormatInt(d.ID, 10), Detail: d.Scenario,
			Origin: d.Origin, Until: d.Until, Community: community}) {
			csRead.Count++
		}
	}
	v.Engines = append(v.Engines, csRead)

	fwRead := EngineRead{Engine: "firewall"}
	if fw != nil && fw.Available {
		fwRead.Read = true
		for _, r := range fw.Rules {
			action := strings.ToUpper(r.Action)
			if action != "DENY" && action != "REJECT" && action != "DROP" {
				continue
			}
			if d := strings.ToUpper(r.Direction); d != "" && d != "IN" {
				continue
			}
			detail := strings.ToLower(action)
			if r.Port != "" {
				detail += " to port " + r.Port
			}
			if add(strings.TrimSuffix(strings.TrimSpace(r.From), " (v6)"), BlockSource{Engine: "firewall", Ref: strconv.Itoa(r.Number), Detail: detail}) {
				fwRead.Count++
			}
		}
	} else {
		fwRead.Note = "no firewall"
	}
	v.Engines = append(v.Engines, fwRead)

	var ranges []netip.Prefix
	for p := range entries {
		if p.Bits() < p.Addr().BitLen() {
			ranges = append(ranges, p)
		}
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Bits() < ranges[j].Bits() })

	v.Distinct = len(entries)
	listed := []BlockedEntry{}
	for p, h := range entries {
		e := BlockedEntry{Value: blockValue(p), Range: p.Bits() < p.Addr().BitLen(), Sources: h.sources}
		local := false
		engines := map[string]bool{}
		for _, s := range h.sources {
			engines[s.Engine] = true
			if !s.Community {
				local = true
			}
		}
		for name := range engines {
			e.Engines = append(e.Engines, name)
		}
		sort.Strings(e.Engines)
		for _, r := range ranges {
			if r.Bits() < p.Bits() && r.Contains(p.Addr()) {
				e.CoveredBy = append(e.CoveredBy, blockValue(r))
			}
		}
		if !local {
			v.CommunityOnly++
			continue
		}
		if len(h.sources) > 1 {
			v.Duplicated++
		}
		if len(e.CoveredBy) > 0 {
			v.Covered++
		}
		listed = append(listed, e)
	}
	sort.Slice(listed, func(i, j int) bool {
		ri, rj := redundancy(listed[i]), redundancy(listed[j])
		if ri != rj {
			return ri > rj
		}
		return listed[i].Value < listed[j].Value
	})
	if len(listed) > maxBlockEntries {
		listed, v.Truncated = listed[:maxBlockEntries], true
	}
	v.Entries = listed
	return v
}

func redundancy(e BlockedEntry) int {
	n := len(e.Sources) - 1
	if len(e.CoveredBy) > 0 {
		n++
	}
	return n
}

// refusedPrefix reads an address or a network as the prefix it blocks.
func refusedPrefix(value string) (netip.Prefix, bool) {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "/") {
		p, err := netip.ParsePrefix(value)
		if err != nil {
			return netip.Prefix{}, false
		}
		addr := p.Addr().Unmap()
		bits := p.Bits()
		if p.Addr().Is4In6() {
			bits -= 96
		}
		if bits < 0 {
			return netip.Prefix{}, false
		}
		return netip.PrefixFrom(addr, bits).Masked(), true
	}
	a, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Prefix{}, false
	}
	a = a.Unmap().WithZone("")
	return netip.PrefixFrom(a, a.BitLen()), true
}

// blockValue writes an address without its full-length prefix.
func blockValue(p netip.Prefix) string {
	if p.Bits() == p.Addr().BitLen() {
		return p.Addr().String()
	}
	return p.String()
}

// CrowdSecDecisions reads only the decisions, for a caller that needs the
// list and not the alerts, the bouncers or the enforcement evidence.
func (s *Service) CrowdSecDecisions(ctx context.Context) ([]CrowdSecDecision, bool, error) {
	if !hasTool("cscli") {
		return nil, false, nil
	}
	out, err := run(ctx, "cscli", "decisions", "list", "-o", "json", "--limit", "500")
	if err != nil {
		return nil, true, err
	}
	decisions, err := parseCrowdSecDecisions(out, crowdsecNow())
	return decisions, true, err
}
