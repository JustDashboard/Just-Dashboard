package netsec

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A decision is a row in CrowdSec's database. It becomes protection only when
// a bouncer pulls it and installs something that drops the traffic, and the
// engine's own status says nothing about whether that happened: a valid
// bouncer that last pulled a week ago, or a firewall bouncer whose kernel set
// was flushed by a ruleset reload, leaves every decision in place and the
// host open. So the page claims protection only from evidence: a bouncer that
// pulled recently and, for a firewall bouncer, a kernel set that holds the
// addresses with a hooked rule that drops on it.

// bouncerFreshness is how recently a bouncer must have pulled to count as
// running. The firewall bouncer's default update_frequency is ten seconds and
// proxy bouncers poll on a similar interval; three minutes absorbs a slow
// LAPI without treating a bouncer that died this morning as alive.
const bouncerFreshness = 3 * time.Minute

// Enforcement states, worst to best.
const (
	EnforcementStopped    = "stopped"
	EnforcementUnenforced = "unenforced"
	EnforcementStale      = "stale"
	EnforcementDegraded   = "degraded"
	EnforcementUnverified = "unverified"
	EnforcementPartial    = "partial"
	EnforcementEnforcing  = "enforcing"
)

// CrowdSecEnforcement is the verdict on whether decisions are being enforced,
// with the evidence it was reached from.
type CrowdSecEnforcement struct {
	State    string             `json:"state"`
	Summary  string             `json:"summary"`
	Bouncers []BouncerEvidence  `json:"bouncers"`
	Kernel   *CrowdSecKernel    `json:"kernel,omitempty"`
	Checked  time.Time          `json:"checkedAt"`
	Fresh    string             `json:"freshness"`
	Enforced []string           `json:"enforcedBy"`
	Missing  []EnforcementCause `json:"missing,omitempty"`
}

// EnforcementCause is one reason the verdict is not "enforcing".
type EnforcementCause struct {
	Bouncer string `json:"bouncer,omitempty"`
	Reason  string `json:"reason"`
}

// BouncerEvidence is what was read about one bouncer.
type BouncerEvidence struct {
	Name string `json:"name"`
	// Kind is firewall, proxy or other: only a firewall bouncer's effect can be
	// read from this host's kernel.
	Kind     string `json:"kind"`
	Valid    bool   `json:"valid"`
	LastPull string `json:"lastPull,omitempty"`
	// PullAge is seconds since the last pull, -1 for never.
	PullAge int64 `json:"pullAgeSeconds"`
	Fresh   bool  `json:"fresh"`
	// Unit is the bouncer's systemd unit where the kind has a known one, and
	// UnitActive whether it is running; nil where it was not read.
	Unit       string `json:"unit,omitempty"`
	UnitActive *bool  `json:"unitActive,omitempty"`
}

// CrowdSecKernel is the firewall bouncer's footprint in the kernel.
type CrowdSecKernel struct {
	// Backend is nftables or ipset, empty when neither showed a CrowdSec set.
	Backend string        `json:"backend,omitempty"`
	Sets    []CrowdSecSet `json:"sets"`
	Entries int           `json:"entries"`
	Error   string        `json:"error,omitempty"`
}

// CrowdSecSet is one set of banned addresses and whether a rule drops on it.
type CrowdSecSet struct {
	Family  string `json:"family,omitempty"`
	Table   string `json:"table,omitempty"`
	Name    string `json:"name"`
	Entries int    `json:"entries"`
	// Dropped is a drop or reject rule matching the set in a chain attached
	// to a netfilter hook (nftables), or any iptables rule doing so (ipset).
	Dropped bool     `json:"dropped"`
	Hooks   []string `json:"hooks,omitempty"`
}

// bouncerKind reads what a bouncer enforces from its type, falling back to its
// name: older LAPIs leave type empty for bouncers registered by hand.
func bouncerKind(b CrowdSecBouncer) string {
	words := strings.ToLower(b.Type + " " + b.Name)
	switch {
	case strings.Contains(words, "firewall"):
		return "firewall"
	case containsAny(words, "nginx", "openresty", "traefik", "caddy", "haproxy", "apache", "envoy", "cloudflare", "lua", "php", "wordpress", "ingress"):
		return "proxy"
	}
	return "other"
}

func containsAny(s string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// bouncerEvidence classifies every bouncer and dates its last pull.
func bouncerEvidence(bouncers []CrowdSecBouncer, now time.Time) []BouncerEvidence {
	out := make([]BouncerEvidence, 0, len(bouncers))
	for _, b := range bouncers {
		e := BouncerEvidence{Name: b.Name, Kind: bouncerKind(b), Valid: b.Valid, LastPull: b.LastPull, PullAge: -1}
		if t, err := time.Parse(time.RFC3339Nano, b.LastPull); err == nil {
			age := now.Sub(t)
			if age < 0 {
				// A LAPI clock slightly ahead of this one.
				age = 0
			}
			e.PullAge = int64(age / time.Second)
			e.Fresh = b.Valid && age <= bouncerFreshness
		}
		if e.Kind == "firewall" {
			e.Unit = "crowdsec-firewall-bouncer"
		}
		out = append(out, e)
	}
	return out
}

// AssessEnforcement decides whether the decisions in force are enforced. It is
// a pure function of the evidence so every state can be pinned by a test.
func AssessEnforcement(active bool, bouncers []BouncerEvidence, banDecisions int, kernel *CrowdSecKernel, now time.Time) CrowdSecEnforcement {
	e := CrowdSecEnforcement{Bouncers: bouncers, Kernel: kernel, Checked: now.UTC(), Fresh: bouncerFreshness.String(), Enforced: []string{}}
	if e.Bouncers == nil {
		e.Bouncers = []BouncerEvidence{}
	}
	if !active {
		e.State = EnforcementStopped
		e.Summary = "CrowdSec is not running, so nothing new is decided and no bouncer can pull. Addresses a bouncer already installed may still be dropped until they expire."
		return e
	}
	if len(bouncers) == 0 {
		e.State = EnforcementUnenforced
		e.Summary = "No bouncer is registered. Every decision is recorded and nothing drops the traffic it names."
		return e
	}
	var fresh []BouncerEvidence
	for _, b := range bouncers {
		switch {
		case !b.Valid:
			e.Missing = append(e.Missing, EnforcementCause{Bouncer: b.Name, Reason: "its API key is not valid"})
		case b.PullAge < 0:
			e.Missing = append(e.Missing, EnforcementCause{Bouncer: b.Name, Reason: "it has never pulled a decision"})
		case !b.Fresh:
			e.Missing = append(e.Missing, EnforcementCause{Bouncer: b.Name, Reason: "its last pull was " + ageWords(b.PullAge) + " ago"})
		case b.UnitActive != nil && !*b.UnitActive:
			e.Missing = append(e.Missing, EnforcementCause{Bouncer: b.Name, Reason: b.Unit + " is not running"})
		default:
			fresh = append(fresh, b)
		}
	}
	if len(fresh) == 0 {
		e.State = EnforcementStale
		e.Summary = "No bouncer has pulled within " + bouncerFreshness.String() + ". Decisions keep accumulating with nothing enforcing them."
		return e
	}
	var firewall, proxy, other []string
	for _, b := range fresh {
		switch b.Kind {
		case "firewall":
			firewall = append(firewall, b.Name)
		case "proxy":
			proxy = append(proxy, b.Name)
		default:
			other = append(other, b.Name)
		}
	}
	if len(firewall) > 0 {
		switch {
		case kernel == nil || kernel.Error != "":
			e.State = EnforcementUnverified
			reason := "the kernel's sets could not be read"
			if kernel != nil && kernel.Error != "" {
				reason += ": " + kernel.Error
			}
			e.Missing = append(e.Missing, EnforcementCause{Bouncer: firewall[0], Reason: reason})
			e.Summary = firewall[0] + " is pulling decisions, and whether the kernel drops them could not be read."
			return e
		case len(kernel.Sets) == 0:
			e.State = EnforcementDegraded
			e.Missing = append(e.Missing, EnforcementCause{Bouncer: firewall[0], Reason: "no CrowdSec set exists in nftables or ipset"})
			e.Summary = firewall[0] + " is pulling decisions, but no CrowdSec set exists in the kernel, so nothing it pulled is dropped."
			return e
		case !anyDropped(kernel.Sets):
			e.State = EnforcementDegraded
			e.Missing = append(e.Missing, EnforcementCause{Bouncer: firewall[0], Reason: "no hooked rule drops on its sets"})
			e.Summary = firewall[0] + " fills its kernel sets, but no rule attached to a netfilter hook drops on them."
			return e
		case banDecisions > 0 && kernel.Entries == 0:
			e.State = EnforcementDegraded
			e.Missing = append(e.Missing, EnforcementCause{Bouncer: firewall[0], Reason: fmt.Sprintf("%d decisions are in force and its sets are empty", banDecisions)})
			e.Summary = fmt.Sprintf("%s is pulling decisions, but its kernel sets are empty while %d decisions are in force.", firewall[0], banDecisions)
			return e
		}
		e.State = EnforcementEnforcing
		e.Enforced = append(e.Enforced, firewall...)
		e.Enforced = append(e.Enforced, proxy...)
		e.Summary = fmt.Sprintf("%s pulled %s ago; the kernel holds %d addresses in %s and a hooked rule drops them.",
			firewall[0], ageWords(freshestAge(fresh, "firewall")), kernel.Entries, setNames(kernel.Sets))
		return e
	}
	if len(proxy) > 0 {
		e.State = EnforcementPartial
		e.Enforced = append(e.Enforced, proxy...)
		e.Missing = append(e.Missing, EnforcementCause{Reason: "no firewall bouncer is pulling, so traffic that does not pass through these proxies is not covered"})
		e.Summary = strings.Join(proxy, ", ") + " enforce decisions for the HTTP traffic passing through them. SSH and every other port are not covered, and the proxy's own drop cannot be read from here."
		return e
	}
	e.State = EnforcementUnverified
	e.Missing = append(e.Missing, EnforcementCause{Bouncer: other[0], Reason: "what this bouncer enforces cannot be read from this host"})
	e.Summary = strings.Join(other, ", ") + " is pulling decisions; what it does with them cannot be read from this host."
	return e
}

func anyDropped(sets []CrowdSecSet) bool {
	for _, s := range sets {
		if s.Dropped {
			return true
		}
	}
	return false
}

func setNames(sets []CrowdSecSet) string {
	names := []string{}
	for _, s := range sets {
		if s.Dropped {
			names = append(names, s.Name)
		}
	}
	return strings.Join(names, ", ")
}

func freshestAge(bouncers []BouncerEvidence, kind string) int64 {
	best := int64(-1)
	for _, b := range bouncers {
		if b.Kind == kind && b.PullAge >= 0 && (best < 0 || b.PullAge < best) {
			best = b.PullAge
		}
	}
	return best
}

func ageWords(seconds int64) string {
	d := time.Duration(seconds) * time.Second
	switch {
	case seconds < 0:
		return "never"
	case d < time.Minute:
		return fmt.Sprintf("%d s", seconds)
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d/time.Hour))
	}
	return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
}

// banDecisionCount is the decisions a firewall bouncer would install: bans on
// an address or a range. A captcha, a country or an AS is a proxy's business.
func banDecisionCount(decisions []CrowdSecDecision) int {
	n := 0
	for _, d := range decisions {
		scope := strings.ToLower(d.Scope)
		if strings.EqualFold(d.Type, "ban") && (scope == "ip" || scope == "range") {
			n++
		}
	}
	return n
}

// crowdsecEnforcement reads the evidence the verdict needs: each firewall
// bouncer's unit and, where one exists, the kernel's sets. Nothing here is a
// write; every command is a listing.
func (s *Service) crowdsecEnforcement(ctx context.Context, v *CrowdSecView, now time.Time) CrowdSecEnforcement {
	evidence := bouncerEvidence(v.Bouncers, now)
	var kernel *CrowdSecKernel
	for i := range evidence {
		if evidence[i].Kind != "firewall" {
			continue
		}
		if out, err := run(ctx, "systemctl", "is-active", evidence[i].Unit); err == nil || strings.TrimSpace(out) != "" {
			active := strings.TrimSpace(out) == "active"
			evidence[i].UnitActive = &active
		}
		if kernel == nil {
			kernel = readCrowdSecKernel(ctx)
		}
	}
	return AssessEnforcement(v.Active, evidence, banDecisionCount(v.Decisions), kernel, now)
}

// readCrowdSecKernel finds the firewall bouncer's sets in nftables, or in
// ipset where the bouncer runs in iptables mode.
func readCrowdSecKernel(ctx context.Context) *CrowdSecKernel {
	k := &CrowdSecKernel{Sets: []CrowdSecSet{}}
	var problems []string
	if hasTool("nft") {
		out, err := run(ctx, "nft", "-j", "list", "tables")
		if err != nil {
			problems = append(problems, "nft: "+firstLine(out+" "+err.Error()))
		} else {
			for _, t := range crowdsecTables(out) {
				body, err := run(ctx, "nft", "-j", "list", "table", t.family, t.name)
				if err != nil {
					problems = append(problems, "nft list table "+t.family+" "+t.name+": "+firstLine(body+" "+err.Error()))
					continue
				}
				sets, err := parseCrowdSecTable(body)
				if err != nil {
					problems = append(problems, err.Error())
					continue
				}
				k.Sets = append(k.Sets, sets...)
			}
			if len(k.Sets) > 0 {
				k.Backend = "nftables"
			}
		}
	}
	if len(k.Sets) == 0 && hasTool("ipset") {
		out, err := run(ctx, "ipset", "list", "-t")
		if err != nil {
			problems = append(problems, "ipset: "+firstLine(out+" "+err.Error()))
		} else if sets := parseIPSetTerse(out); len(sets) > 0 {
			rules := ""
			for _, tool := range []string{"iptables", "ip6tables"} {
				if r, err := run(ctx, tool, "-S"); err == nil {
					rules += r + "\n"
				}
			}
			markIPSetDrops(sets, rules)
			k.Sets, k.Backend = sets, "ipset"
		}
	}
	for _, set := range k.Sets {
		k.Entries += set.Entries
	}
	if len(k.Sets) == 0 && len(problems) > 0 {
		k.Error = strings.Join(problems, "; ")
	}
	if !hasTool("nft") && !hasTool("ipset") {
		k.Error = "neither nft nor ipset is installed, so the kernel's sets cannot be read"
	}
	return k
}

type nftTableRef struct{ family, name string }

var crowdsecNameRe = regexp.MustCompile(`^crowdsec[A-Za-z0-9_-]*$`)

func crowdsecTables(out string) []nftTableRef {
	var doc struct {
		Nftables []map[string]json.RawMessage `json:"nftables"`
	}
	if json.Unmarshal([]byte(out), &doc) != nil {
		return nil
	}
	var tables []nftTableRef
	for _, item := range doc.Nftables {
		raw, ok := item["table"]
		if !ok {
			continue
		}
		var t struct{ Family, Name string }
		if json.Unmarshal(raw, &t) == nil && crowdsecNameRe.MatchString(t.Name) && validNFTFamily(t.Family) {
			tables = append(tables, nftTableRef{t.Family, t.Name})
		}
	}
	return tables
}

func validNFTFamily(f string) bool {
	switch f {
	case "ip", "ip6", "inet", "bridge", "arp", "netdev":
		return true
	}
	return false
}

// parseCrowdSecTable reads one table's sets, their element counts and the
// rules that drop on them in chains attached to a hook.
func parseCrowdSecTable(out string) ([]CrowdSecSet, error) {
	var doc struct {
		Nftables []map[string]json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return nil, fmt.Errorf("unreadable nft output: %w", err)
	}
	hooks := map[string]string{}
	sets := map[string]*CrowdSecSet{}
	var order []string
	type rule struct {
		chain string
		expr  []map[string]json.RawMessage
	}
	var rules []rule
	for _, item := range doc.Nftables {
		switch {
		case item["chain"] != nil:
			var c struct{ Name, Hook string }
			if json.Unmarshal(item["chain"], &c) == nil && c.Hook != "" {
				hooks[c.Name] = c.Hook
			}
		case item["set"] != nil:
			var st struct {
				Family, Table, Name string
				Elem                []json.RawMessage `json:"elem"`
			}
			if json.Unmarshal(item["set"], &st) == nil {
				sets[st.Name] = &CrowdSecSet{Family: st.Family, Table: st.Table, Name: st.Name, Entries: len(st.Elem)}
				order = append(order, st.Name)
			}
		case item["rule"] != nil:
			var r struct {
				Chain string                       `json:"chain"`
				Expr  []map[string]json.RawMessage `json:"expr"`
			}
			if json.Unmarshal(item["rule"], &r) == nil {
				rules = append(rules, rule{r.Chain, r.Expr})
			}
		}
	}
	for _, r := range rules {
		hook, hooked := hooks[r.chain]
		if !hooked {
			continue
		}
		var matched []string
		drops := false
		for _, e := range r.expr {
			if raw, ok := e["match"]; ok {
				var m struct {
					Right json.RawMessage `json:"right"`
				}
				var name string
				if json.Unmarshal(raw, &m) == nil && json.Unmarshal(m.Right, &name) == nil && strings.HasPrefix(name, "@") {
					matched = append(matched, strings.TrimPrefix(name, "@"))
				}
			}
			if _, ok := e["drop"]; ok {
				drops = true
			}
			if _, ok := e["reject"]; ok {
				drops = true
			}
		}
		if !drops {
			continue
		}
		for _, name := range matched {
			if set := sets[name]; set != nil {
				set.Dropped = true
				if !containsString(set.Hooks, hook) {
					set.Hooks = append(set.Hooks, hook)
				}
			}
		}
	}
	out2 := make([]CrowdSecSet, 0, len(order))
	for _, name := range order {
		out2 = append(out2, *sets[name])
	}
	return out2, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// parseIPSetTerse reads `ipset list -t`, keeping the CrowdSec sets.
func parseIPSetTerse(out string) []CrowdSecSet {
	var sets []CrowdSecSet
	var current *CrowdSecSet
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "Name":
			current = nil
			if crowdsecNameRe.MatchString(value) {
				sets = append(sets, CrowdSecSet{Name: value})
				current = &sets[len(sets)-1]
			}
		case "Number of entries":
			if current != nil {
				current.Entries, _ = strconv.Atoi(value)
			}
		}
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i].Name < sets[j].Name })
	return sets
}

// markIPSetDrops marks a set dropped where an iptables rule matches it and
// jumps to DROP or REJECT.
func markIPSetDrops(sets []CrowdSecSet, rules string) {
	for _, line := range strings.Split(rules, "\n") {
		fields := strings.Fields(line)
		target := ""
		var matched []string
		for i := 0; i < len(fields)-1; i++ {
			switch fields[i] {
			case "--match-set":
				matched = append(matched, fields[i+1])
			case "-j":
				target = fields[i+1]
			}
		}
		if target != "DROP" && target != "REJECT" {
			continue
		}
		for _, name := range matched {
			for i := range sets {
				if sets[i].Name == name {
					sets[i].Dropped = true
				}
			}
		}
	}
}
