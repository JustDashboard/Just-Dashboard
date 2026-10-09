package netsec

import (
	"strings"
	"testing"
	"time"
)

var enforcementNow = time.Date(2026, 10, 7, 9, 30, 0, 0, time.UTC)

func pulled(name, kind string, age time.Duration) BouncerEvidence {
	e := BouncerEvidence{Name: name, Kind: kind, Valid: true, PullAge: int64(age / time.Second), Fresh: age <= bouncerFreshness}
	if kind == "firewall" {
		e.Unit = "crowdsec-firewall-bouncer"
	}
	return e
}

func droppingKernel(entries int) *CrowdSecKernel {
	return &CrowdSecKernel{Backend: "nftables", Entries: entries, Sets: []CrowdSecSet{
		{Family: "ip", Table: "crowdsec", Name: "crowdsec-blacklists-crowdsec", Entries: entries, Dropped: true, Hooks: []string{"input"}},
	}}
}

// Protection is claimed only from a bouncer that pulled recently and, for a
// firewall bouncer, from a kernel set with a hooked rule dropping on it.
func TestEnforcementIsClaimedOnlyFromAFreshPullAndAKernelDrop(t *testing.T) {
	inactive := false
	stoppedUnit := pulled("cs-firewall-bouncer", "firewall", 5*time.Second)
	stoppedUnit.UnitActive = &inactive
	never := BouncerEvidence{Name: "cs-firewall-bouncer", Kind: "firewall", Valid: true, PullAge: -1}
	revoked := pulled("cs-firewall-bouncer", "firewall", 5*time.Second)
	revoked.Valid, revoked.Fresh = false, false
	for _, tc := range []struct {
		name      string
		active    bool
		bouncers  []BouncerEvidence
		decisions int
		kernel    *CrowdSecKernel
		want      string
		says      string
	}{
		{"engine stopped", false, []BouncerEvidence{pulled("fw", "firewall", time.Second)}, 3, droppingKernel(3), EnforcementStopped, "not running"},
		{"no bouncer", true, nil, 3, nil, EnforcementUnenforced, "No bouncer"},
		{"last pull an hour ago", true, []BouncerEvidence{pulled("fw", "firewall", time.Hour)}, 3, droppingKernel(3), EnforcementStale, "No bouncer has pulled"},
		{"never pulled", true, []BouncerEvidence{never}, 3, droppingKernel(3), EnforcementStale, ""},
		{"revoked key", true, []BouncerEvidence{revoked}, 3, droppingKernel(3), EnforcementStale, ""},
		{"bouncer unit stopped", true, []BouncerEvidence{stoppedUnit}, 3, droppingKernel(3), EnforcementStale, ""},
		{"no set in the kernel", true, []BouncerEvidence{pulled("fw", "firewall", time.Second)}, 3, &CrowdSecKernel{Sets: []CrowdSecSet{}}, EnforcementDegraded, "no CrowdSec set"},
		{"set nobody drops on", true, []BouncerEvidence{pulled("fw", "firewall", time.Second)}, 3, &CrowdSecKernel{Backend: "nftables", Entries: 3, Sets: []CrowdSecSet{{Name: "crowdsec-blacklists", Entries: 3}}}, EnforcementDegraded, "no rule attached"},
		{"empty set beside decisions", true, []BouncerEvidence{pulled("fw", "firewall", time.Second)}, 3, droppingKernel(0), EnforcementDegraded, "empty"},
		{"kernel unreadable", true, []BouncerEvidence{pulled("fw", "firewall", time.Second)}, 3, &CrowdSecKernel{Error: "nft: permission denied"}, EnforcementUnverified, "could not be read"},
		{"proxy only", true, []BouncerEvidence{pulled("caddy-bouncer", "proxy", 10*time.Second)}, 3, nil, EnforcementPartial, "SSH and every other port"},
		{"unknown kind", true, []BouncerEvidence{pulled("custom", "other", 10*time.Second)}, 3, nil, EnforcementUnverified, "cannot be read"},
		{"firewall enforcing", true, []BouncerEvidence{pulled("fw", "firewall", 8*time.Second), pulled("caddy-bouncer", "proxy", time.Hour)}, 3, droppingKernel(3), EnforcementEnforcing, "pulled 8 s ago"},
		{"nothing to drop yet", true, []BouncerEvidence{pulled("fw", "firewall", time.Second)}, 0, droppingKernel(0), EnforcementEnforcing, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := AssessEnforcement(tc.active, tc.bouncers, tc.decisions, tc.kernel, enforcementNow)
			if got.State != tc.want || !strings.Contains(got.Summary, tc.says) {
				t.Fatalf("state=%s summary=%q missing=%+v", got.State, got.Summary, got.Missing)
			}
			if got.State != EnforcementEnforcing && got.State != EnforcementPartial && len(got.Enforced) != 0 {
				t.Fatalf("%s names enforcers %v", got.State, got.Enforced)
			}
			if (got.State == EnforcementStale || got.State == EnforcementDegraded) && len(got.Missing) == 0 {
				t.Fatal("a refusal to claim protection names no cause")
			}
		})
	}
}

func TestBouncerEvidenceDatesPullsAndClassifiesKinds(t *testing.T) {
	got := bouncerEvidence([]CrowdSecBouncer{
		{Name: "cs-firewall-bouncer", Valid: true, LastPull: "2026-10-07T09:29:50Z", Type: "crowdsec-firewall-bouncer"},
		{Name: "caddy-bouncer", Valid: true, LastPull: "2026-10-07T08:00:00Z"},
		{Name: "custom", Valid: true},
		{Name: "ahead", Valid: true, LastPull: "2026-10-07T09:31:00Z", Type: "crowdsec-nginx-bouncer"},
	}, enforcementNow)
	if got[0].Kind != "firewall" || got[0].PullAge != 10 || !got[0].Fresh || got[0].Unit != "crowdsec-firewall-bouncer" {
		t.Fatalf("firewall=%+v", got[0])
	}
	if got[1].Kind != "proxy" || got[1].Fresh || got[1].PullAge != 5400 {
		t.Fatalf("proxy=%+v", got[1])
	}
	if got[2].Kind != "other" || got[2].PullAge != -1 || got[2].Fresh {
		t.Fatalf("never pulled=%+v", got[2])
	}
	if !got[3].Fresh || got[3].PullAge != 0 || got[3].Kind != "proxy" {
		t.Fatalf("a clock slightly ahead=%+v", got[3])
	}
}

// The fixtures are nft's own JSON, recorded in a throwaway network namespace
// from a table shaped like the firewall bouncer's.
func TestParseCrowdSecTableCountsSetsAndHookedDrops(t *testing.T) {
	tables := crowdsecTables(testdata(t, "crowdsec-nft-tables.json"))
	if len(tables) != 2 || tables[0] != (nftTableRef{"ip", "crowdsec"}) || tables[1] != (nftTableRef{"ip6", "crowdsec6"}) {
		t.Fatalf("tables=%+v", tables)
	}
	sets, err := parseCrowdSecTable(testdata(t, "crowdsec-nft-table-ip.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) != 2 || sets[0].Name != "crowdsec-blacklists-crowdsec" || sets[0].Entries != 2 || !sets[0].Dropped ||
		sets[0].Hooks[0] != "input" || sets[1].Entries != 1 || !sets[1].Dropped {
		t.Fatalf("sets=%+v", sets)
	}
	v6, err := parseCrowdSecTable(testdata(t, "crowdsec-nft-table-ip6.json"))
	if err != nil || len(v6) != 1 || v6[0].Entries != 0 || !v6[0].Dropped {
		t.Fatalf("ip6 sets=%+v,%v", v6, err)
	}
	unhooked, err := parseCrowdSecTable(testdata(t, "crowdsec-nft-table-unhooked.json"))
	if err != nil || len(unhooked) != 1 || unhooked[0].Dropped || unhooked[0].Entries != 1 {
		t.Fatalf("a drop in a chain no hook reaches counted: %+v,%v", unhooked, err)
	}
	if _, err := parseCrowdSecTable("not json"); err == nil {
		t.Fatal("unreadable output accepted")
	}
}

func TestIPSetModeReadsEntriesAndTheDropRule(t *testing.T) {
	sets := parseIPSetTerse(testdata(t, "crowdsec-ipset-terse.txt"))
	if len(sets) != 2 || sets[0].Name != "crowdsec-blacklists" || sets[0].Entries != 41 || sets[1].Entries != 2 {
		t.Fatalf("sets=%+v", sets)
	}
	markIPSetDrops(sets, testdata(t, "crowdsec-iptables-S.txt"))
	if !sets[0].Dropped || sets[1].Dropped {
		t.Fatalf("drops=%+v", sets)
	}
}

func TestCrowdSecViewReportsEnforcementFromTheKernel(t *testing.T) {
	h := newCscliHost(t, true)
	h.replies["systemctl is-active crowdsec-firewall-bouncer"] = "active\n"
	h.replies["systemctl is-active crowdsec"] = "active\n"
	h.replies["cscli decisions list -o json"] = testdata(t, "crowdsec-decisions.json")
	h.replies["cscli alerts list -o json --limit 50"] = testdata(t, "crowdsec-alerts.json")
	h.replies["cscli bouncers list -o json"] = testdata(t, "crowdsec-bouncers.json")
	h.replies["nft -j list tables"] = testdata(t, "crowdsec-nft-tables.json")
	h.replies["nft -j list table ip crowdsec"] = testdata(t, "crowdsec-nft-table-ip.json")
	h.replies["nft -j list table ip6 crowdsec6"] = testdata(t, "crowdsec-nft-table-ip6.json")
	prev := crowdsecNow
	crowdsecNow = func() time.Time { return time.Date(2026, 10, 7, 9, 21, 0, 0, time.UTC) }
	t.Cleanup(func() { crowdsecNow = prev })

	v, err := New().CrowdSec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	e := v.Enforcement
	if e == nil || e.State != EnforcementEnforcing || e.Kernel.Backend != "nftables" || e.Kernel.Entries != 3 || len(e.Kernel.Sets) != 3 {
		t.Fatalf("enforcement=%+v", e)
	}
	if e.Bouncers[1].UnitActive == nil || !*e.Bouncers[1].UnitActive || e.Bouncers[0].Fresh || e.Bouncers[0].Kind != "proxy" {
		t.Fatalf("bouncers=%+v", e.Bouncers)
	}
	for _, call := range h.calls {
		if !strings.HasPrefix(call, "cscli ") && !strings.HasPrefix(call, "systemctl is-active") && !strings.HasPrefix(call, "nft -j list") {
			t.Fatalf("enforcement ran a command that is not a listing: %s", call)
		}
	}
}

func TestCrowdSecViewDoesNotClaimProtectionWhenTheBouncerListFails(t *testing.T) {
	h := newCscliHost(t, true)
	h.replies["systemctl is-active crowdsec"] = "active\n"
	h.replies["cscli decisions list -o json"] = "null"
	h.replies["cscli alerts list -o json --limit 50"] = "null"
	h.replies["cscli bouncers list -o json"] = "level=fatal msg=\"unable to connect to the database\""
	h.errs["cscli bouncers list -o json"] = errFixture("exit status 1")

	v, err := New().CrowdSec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if v.Enforcement == nil || v.Enforcement.State != EnforcementUnverified {
		t.Fatalf("an unreadable bouncer list became %+v", v.Enforcement)
	}
}

type errFixture string

func (e errFixture) Error() string { return string(e) }
