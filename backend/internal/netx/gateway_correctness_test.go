package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGatewayCapabilityRetainsExplicitDropsAndUnknownPolicyLayers(t *testing.T) {
	cases := []struct {
		name, policy, expr, status string
		writable                   bool
	}{
		{"explicit drop in accept chain", "accept", `[{"drop":null}]`, "blocked", false},
		{"explicit reject in accept chain", "accept", `[{"reject":{"type":"icmpx","expr":"port-unreachable"}}]`, "blocked", false},
		{"conditional source drop", "accept", `[{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"saddr"}},"right":"198.51.100.7"}},{"drop":null}]`, "unknown", false},
		{"foreign jump", "accept", `[{"jump":{"target":"other"}}]`, "unknown", false},
		{"unconditional accept", "drop", `[{"accept":null}]`, "admitted", true},
		{"base return applies drop policy", "drop", `[{"return":null}]`, "blocked", false},
		{"mark exemption", "drop", `[{"match":{"op":"==","left":{"&":[{"ct":{"key":"mark"}},4278190080]},"right":1241513984}},{"counter":{"packets":0,"bytes":0}},{"accept":null}]`, "admitted", true},
		{"incorrect mark exemption", "drop", `[{"match":{"op":"==","left":{"&":[{"ct":{"key":"mark"}},16711680]},"right":1241513984}},{"accept":null}]`, "unknown", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newGwHost(t)
			listing := fmt.Sprintf(`{"nftables":[{"chain":{"family":"inet","table":"foreign","name":"forward","hook":"forward","policy":%q}},{"rule":{"family":"inet","table":"foreign","chain":"forward","expr":%s}}]}`, tc.policy, tc.expr)
			h.first("nft -t -j list ruleset", listing, nil)
			c := h.GatewayCapability(context.Background())
			if c.Writable != tc.writable || c.Reachability != "unknown" || len(c.Layers) != 1 || c.Layers[0].Status != tc.status || len(c.UnknownLayers) < 2 {
				t.Fatalf("capability = %+v", c)
			}
		})
	}
}

func TestGatewayCapabilityChecksIndependentInputAndSecurityTables(t *testing.T) {
	for _, fixture := range []string{
		`{"nftables":[{"chain":{"family":"inet","table":"foreign","name":"input","hook":"input","policy":"drop"}}]}`,
		`{"nftables":[{"chain":{"family":"ip","table":"security","name":"FORWARD","hook":"forward","policy":"drop"}}]}`,
	} {
		h := newGwHost(t)
		h.first("nft -t -j list ruleset", fixture, nil)
		if c := h.GatewayCapability(context.Background()); c.Writable || c.Blocker == nil {
			t.Fatalf("independent filtering ignored: %+v", c)
		}
	}
}

func dualStackAdmissionSpec() *Spec {
	sp := emptySpec()
	sp.Forwards = []ForwardSpec{{ID: 1, Target: "10.0.0.5", Enabled: true}, {ID: 2, Target: "2001:db8::5", Enabled: true}}
	return sp
}

func TestAdmissionHealthDetectsEveryRequiredRuleIndependently(t *testing.T) {
	for _, tool := range []string{"iptables", "ip6tables"} {
		for _, chain := range admissionChains {
			t.Run(tool+"/"+chain, func(t *testing.T) {
				h := newGwHost(t)
				h.first(tool+" -C "+chain, "Bad rule (does a matching rule exist in that chain?).", errors.New("missing rule"))
				a := h.admissionState(context.Background(), dualStackAdmissionSpec())
				if !a.Needed || a.Present || len(a.Chains) != 6 || a.CheckedAt.IsZero() {
					t.Fatalf("health = %+v", a)
				}
				missing := 0
				for _, ch := range a.Chains {
					if ch.Status == "absent" {
						missing++
						if ch.Tool != tool || ch.Chain != chain {
							t.Fatalf("wrong missing rule: %+v", ch)
						}
					}
				}
				if missing != 1 {
					t.Fatalf("missing %d rules: %+v", missing, a)
				}
			})
		}
	}
}

func TestAdmissionHealthSeparatesUnsupportedAndUnreadableAndMisorderedRules(t *testing.T) {
	cases := []struct {
		name, listing string
		err           error
		status        string
		present       bool
	}{
		{"unsupported chain", "No chain/target/match by that name.", errors.New("missing chain"), "unsupported", true},
		{"unreadable chain", "Permission denied", errors.New("Permission denied"), "unreadable", false},
		{"misordered rule", "-A DOCKER-USER -j DROP\n-A DOCKER-USER " + strings.Join(admissionRule(), " ") + "\n", nil, "absent", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newGwHost(t)
			h.first("ip6tables -S DOCKER-USER", tc.listing, tc.err)
			a := h.admissionState(context.Background(), dualStackAdmissionSpec())
			if a.Present != tc.present || a.Chains[5].Status != tc.status {
				t.Fatalf("health = %+v", a)
			}
		})
	}
}

func TestAdmissionRepairReinsertsOwnedRulesAndVerifiesEveryChain(t *testing.T) {
	h := newGwHost(t)
	sp := dualStackAdmissionSpec()
	h.seed(t, sp)
	if err := h.RepairGatewayAdmission(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"iptables", "ip6tables"} {
		for _, chain := range admissionChains {
			if !h.rec.ran(tool+" -I "+chain+" 1 "+strings.Join(admissionRule(), " ")) || !h.rec.ran(tool+" -C "+chain) {
				t.Fatalf("%s/%s was not repaired and checked", tool, chain)
			}
		}
	}
	if h.rec.ran("nft -f") || h.rec.ran("iptables -P") {
		t.Fatal("admission repair changed table or foreign policy")
	}
}

func TestActiveIPv6AdmissionFailureRollsBackInsteadOfClaimingSuccess(t *testing.T) {
	h := newGwHost(t)
	req := gwWebForward()
	req.Target = "2001:db8::5"
	h.fail("ip6tables -I INPUT")
	if _, err := h.AddForward(context.Background(), req, gwClient, "ops", gwProtected); err == nil {
		t.Fatal("IPv6 apply failure was ignored")
	}
	if h.saved() {
		t.Fatal("failed IPv6 admission was persisted")
	}
}

func TestAdmissionRepairRefusesUnresolvedJournalBeforeHostCommands(t *testing.T) {
	for _, phase := range []string{"prepared", "runtime_applied", "persisted", "awaiting_confirmation", "recovering", "degraded"} {
		t.Run(phase, func(t *testing.T) {
			h := newGwHost(t)
			h.seed(t, dualStackAdmissionSpec())
			j := &changeJournal{Paths: h.paths, ChangeStatus: ChangeStatus{ID: "pending", Phase: phase, Generation: strings.Repeat("a", 64)}}
			if err := j.save(); err != nil {
				t.Fatal(err)
			}
			if err := h.RepairGatewayAdmission(context.Background()); !errors.Is(err, ErrReadOnly) {
				t.Fatalf("repair during %s = %v", phase, err)
			}
			if commands := h.rec.commands(); len(commands) != 0 {
				t.Fatalf("unresolved journal allowed kernel work: %v", commands)
			}
		})
	}
}

func TestAdmissionRepairWaitsForIndependentRecoveryLock(t *testing.T) {
	r := record(t)
	s := testService(t)
	lock, err := lockChange(s.paths.Dir)
	if err != nil {
		t.Fatal(err)
	}
	unlocked := false
	defer func() {
		if !unlocked {
			unlockChange(lock)
		}
	}()
	done := make(chan error, 1)
	go func() { done <- s.RepairGatewayAdmission(context.Background()) }()
	select {
	case err := <-done:
		t.Fatalf("repair passed an independent process lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if commands := r.commands(); len(commands) != 0 {
		t.Fatalf("kernel commands ran while recovery owned the lock: %v", commands)
	}
	unlockChange(lock)
	unlocked = true
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("repair did not resume after releasing the independent lock")
	}
}

func TestMissingAdmissionToolCannotHideAnExistingDropPolicy(t *testing.T) {
	h := newGwHost(t)
	h.first("ip6tables -S", "", &UnavailableError{Tool: "ip6tables"})
	a := h.admissionState(context.Background(), dualStackAdmissionSpec())
	if a.Present || a.Chains[3].Status != "unsupported" || !a.Chains[3].Needed {
		t.Fatalf("existing IPv6 filter was claimed admitted without its tool: %+v", a)
	}
	req := gwWebForward()
	req.Target = "2001:db8::5"
	if _, err := h.AddForward(context.Background(), req, gwClient, "ops", gwProtected); err == nil {
		t.Fatal("an active IPv6 drop policy was ignored when its admission tool was unavailable")
	}
}

func TestBlocklistCacheHealthNeverTreatsLostOrMalformedDataAsPopulated(t *testing.T) {
	dir := t.TempDir()
	path := blocklistFile(dir, 1)
	bl := BlocklistSpec{ID: 1, Kind: "feed", Enabled: true, Count: 4}
	check := func(status string, count int) {
		t.Helper()
		v := blocklistView(dir, bl, netip.Addr{}, nil)
		if v.Cache.Status != status || v.Count != count || v.SavedCount != 4 || (status != "ready" && (v.Error == "" || v.Enforcement != "degraded")) {
			t.Fatalf("view = %+v", v)
		}
	}
	check("missing", 0)
	if err := os.WriteFile(path, []byte("198.51.100.0/24\n203.0.113.0/24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	check("ready", 2)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	check("unreadable", 0)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("198.51.100.0/24\nmalformed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	check("invalid", 0)
	if err := os.WriteFile(path, []byte("10.0.0.0/8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	check("invalid", 0)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	check("invalid", 0)
}

func TestBlocklistCacheMemoDetectsAtomicReplacementWithPreservedModificationTime(t *testing.T) {
	dir := t.TempDir()
	path := blocklistFile(dir, 1)
	if err := os.WriteFile(path, []byte("198.51.100.0/24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	_, oldHealth := checkedBlocklist(dir, 1)
	if err := writeFileAtomic(path, []byte("203.0.113.0/24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	_, newHealth := checkedBlocklist(dir, 1)
	if oldHealth.Generation == newHealth.Generation || newHealth.Status != "ready" {
		t.Fatalf("replaced file retained old cache data: %+v -> %+v", oldHealth, newHealth)
	}
}

func TestCacheLossRefusesUnrelatedGatewayReloadAndStillAllowsRemoval(t *testing.T) {
	srv := newFeedServer(t)
	srv.set("/feed", "203.0.113.0/24\n")
	h := newGwHost(t)
	v, err := h.AddBlocklist(context.Background(), BlocklistRequest{Name: "last good", Kind: "feed", URL: srv.URL + "/feed"}, gwClient, "ops")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(h.paths.Dir, gatewayFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(blocklistFile(filepath.Join(h.paths.Dir, "lists"), v.ID)); err != nil {
		t.Fatal(err)
	}
	loads := len(h.loaded)
	if _, err := h.AddLimit(context.Background(), LimitRequest{Name: "x", Protocol: "tcp", Ports: "8080", Rate: 5, Per: "second"}, gwClient, "ops"); err == nil {
		t.Fatal("unhealthy cache was replaced by an empty set")
	}
	after, _ := os.ReadFile(filepath.Join(h.paths.Dir, gatewayFile))
	if len(h.loaded) != loads || string(before) != string(after) || len(h.spec(t).Limits) != 0 {
		t.Fatal("cache loss changed existing policy")
	}
	if err := h.DeleteBlocklist(context.Background(), v.ID, gwClient); err != nil {
		t.Fatalf("cache loss prevented removing the list: %v", err)
	}
}

func TestCacheLossCanBeRepairedByRefreshUsingTheLastCommittedRender(t *testing.T) {
	srv := newFeedServer(t)
	srv.set("/feed", "203.0.113.0/24\n")
	h := newGwHost(t)
	v, err := h.AddBlocklist(context.Background(), BlocklistRequest{Name: "last good", Kind: "feed", URL: srv.URL + "/feed"}, gwClient, "ops")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(blocklistFile(filepath.Join(h.paths.Dir, "lists"), v.ID)); err != nil {
		t.Fatal(err)
	}
	srv.set("/feed", "198.51.100.0/24\n")
	if err := h.RefreshBlocklist(context.Background(), v.ID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.loaded[len(h.loaded)-1], "198.51.100.0/24") {
		t.Fatal("refresh did not restore enforcement data")
	}
}

func TestBlocklistHealthComparesCacheRenderAndObservedAddressUnion(t *testing.T) {
	h := newGwHost(t)
	sp := emptySpec()
	sp.Blocklists = []BlocklistSpec{{ID: 1, Name: "union", Kind: "manual", Count: 3, Entries: []string{"198.51.100.0/25", "198.51.100.128/25", "2001:db8::/64"}, Enabled: true}}
	h.seed(t, sp)
	rendered, err := renderGateway(sp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(filepath.Join(h.paths.Dir, gatewayFile), []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	h.first("nft -j list set inet jd_gateway bl_1_4", `{"nftables":[{"set":{"name":"bl_1_4","elem":[{"range":["198.51.100.0","198.51.100.255"]}]}}]}`, nil)
	h.first("nft -j list set inet jd_gateway bl_1_6", `{"nftables":[{"set":{"name":"bl_1_6","elem":[{"prefix":{"addr":"2001:db8::","len":64}}]}}]}`, nil)
	v, err := h.Blocklist(context.Background(), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Enforcement != "verified" || v.Runtime.Count == nil || *v.Runtime.Count != 2 || v.Cache.Count != 3 {
		t.Fatalf("auto-merged sets should verify: %+v", v)
	}
	h.first("nft -j list set inet jd_gateway bl_1_6", `{"nftables":[{"set":{"name":"bl_1_6","elem":[]}}]}`, nil)
	v, _ = h.Blocklist(context.Background(), 1, "")
	if v.Enforcement != "degraded" {
		t.Fatalf("partial runtime set loss was hidden: %+v", v)
	}
	h.first("nft -j list set inet jd_gateway bl_1_4", "permission denied", errors.New("permission denied"))
	v, _ = h.Blocklist(context.Background(), 1, "")
	if v.Enforcement != "unknown" || v.Runtime.Status != "unreadable" || v.Runtime.Count != nil {
		t.Fatalf("unreadable runtime invented a set count: %+v", v)
	}
	encoded, _ := json.Marshal(v)
	if !strings.Contains(string(encoded), `"count":null`) {
		t.Fatalf("unknown count should be null: %s", encoded)
	}
}
