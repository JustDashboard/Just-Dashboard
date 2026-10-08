package netipam

import (
	"context"
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

func testService(t *testing.T, snapshot Snapshot) *Service {
	t.Helper()
	st, e := storetest.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, func(context.Context) (Snapshot, error) { return snapshot, nil })
}
func observedSnapshot() Snapshot {
	now := time.Now().UTC()
	return Snapshot{CheckedAt: now, FinishedAt: now, Observations: []Observation{}, Coverage: []Coverage{{Source: "controlled_fixture", State: "observed", CheckedAt: now}}}
}
func pool(t *testing.T, s *Service, prefix string, bits int) Pool {
	t.Helper()
	p, e := s.CreatePool(context.Background(), PoolRequest{Name: "shared", Prefix: prefix, AllocationBits: bits})
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func reserve(t *testing.T, s *Service, p Pool, name string) Reservation {
	t.Helper()
	r, e := s.Reserve(context.Background(), ReserveRequest{PoolID: p.ID, Owner: "docker_network", Resource: name, AcknowledgeUnknown: true}, "operator")
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestIPAMAtomicConcurrentAllocationBothFamilies(t *testing.T) {
	for _, value := range []struct {
		prefix string
		bits   int
	}{{"10.244.0.0/16", 24}, {"fd48:abcd::/48", 64}} {
		t.Run(value.prefix, func(t *testing.T) {
			s := testService(t, observedSnapshot())
			p := pool(t, s, value.prefix, value.bits)
			var wg sync.WaitGroup
			var mu sync.Mutex
			prefixes := map[string]bool{}
			for n := range 12 {
				wg.Go(func() {
					r, e := s.Reserve(context.Background(), ReserveRequest{PoolID: p.ID, Owner: "docker_network", Resource: fmt.Sprintf("network-%d", n)}, "operator")
					mu.Lock()
					defer mu.Unlock()
					if e != nil {
						t.Error(e)
						return
					}
					if prefixes[r.Prefix] {
						t.Errorf("allocated duplicate %s", r.Prefix)
					}
					prefixes[r.Prefix] = true
				})
			}
			wg.Wait()
			if len(prefixes) != 12 {
				t.Fatal("allocations lost", len(prefixes))
			}
		})
	}
}
func TestIPAMFirstFitSkipsCoveredIPv6SpaceWithoutEnumeration(t *testing.T) {
	p := netip.MustParsePrefix("::/0")
	block := prefixInterval(netip.MustParsePrefix("::/1"))
	got, e := FirstFit(p, 128, []interval{block})
	if e != nil || got.String() != "8000::/128" {
		t.Fatal(got, e)
	}
	s := testService(t, Snapshot{Observations: []Observation{{Prefix: "fd00::/9", Owner: "foreign", Resource: "known", Basis: "observed_native"}}, Coverage: observedSnapshot().Coverage})
	p2 := pool(t, s, "fd00::/8", 128)
	r := reserve(t, s, p2, "one")
	if r.Prefix != "fd80::/128" {
		t.Fatal("covering native block was not skipped", r.Prefix)
	}
}
func TestIPAMUtilizationUsesExactUnionAndLargeCounts(t *testing.T) {
	s := testService(t, observedSnapshot())
	p := pool(t, s, "fd48:abcd::/48", 64)
	r := reserve(t, s, p, "one")
	snap := observedSnapshot()
	snap.Observations = []Observation{{Prefix: r.Prefix, Owner: "docker", Resource: "one", Basis: "observed_native"}}
	u := UtilizationFor(p, []Reservation{r}, snap)
	if u.TotalAddresses != "1208925819614629174706176" || u.TotalBlocks != "65536" || u.ReservedBlocks != "1" || u.ObservedBlocks != "1" || u.UnavailableBlocks != "1" || u.CandidateBlocks != "65535" {
		t.Fatalf("lossy/double-counted utilization %#v", u)
	}
}
func TestIPAMKnownOverlapAndUnknownCoverageRemainDistinct(t *testing.T) {
	snap := observedSnapshot()
	snap.Coverage = append(snap.Coverage, Coverage{Source: "provider", State: "unknown", Detail: "Not authoritative"})
	snap.Observations = []Observation{{Prefix: "10.244.0.0/24", Owner: "wireguard", Resource: "wg0", Basis: "configured_native"}}
	s := testService(t, snap)
	p := pool(t, s, "10.244.0.0/16", 24)
	if _, e := s.Reserve(context.Background(), ReserveRequest{PoolID: p.ID, Owner: "docker_network", Resource: "one"}, "operator"); e == nil {
		t.Fatal("unknown coverage silently accepted")
	}
	r := reserve(t, s, p, "one")
	if r.Prefix != "10.244.1.0/24" || !r.AcknowledgedUnknown {
		t.Fatal("known overlap not excluded", r)
	}
	known, e := s.Preview(context.Background(), "10.244.0.0/25")
	if e != nil || known.Status != "known_overlap" || known.Conflicts[0].Owner != "wireguard" {
		t.Fatal(known, e)
	}
	unknown, e := s.Preview(context.Background(), "10.244.9.0/24")
	if e != nil || unknown.Status != "unknown_coverage" {
		t.Fatal("absence claimed", unknown, e)
	}
}
func TestIPAMHandoffRechecksTupleAndChangedCoverage(t *testing.T) {
	snap := observedSnapshot()
	s := testService(t, snap)
	p := pool(t, s, "10.244.0.0/16", 24)
	r := reserve(t, s, p, "one")
	for _, req := range []struct {
		ids                 []string
		owner, name, prefix string
	}{{[]string{"absent"}, "docker_network", "one", r.Prefix}, {[]string{r.ID}, "wireguard_server", "one", r.Prefix}, {[]string{r.ID}, "docker_network", "other", r.Prefix}, {[]string{r.ID}, "docker_network", "one", "10.244.99.0/24"}} {
		if _, e := s.BeginHandoff(context.Background(), req.ids, req.owner, req.name, []string{req.prefix}); e == nil {
			t.Fatal("mismatched tuple accepted", req)
		}
	}
	changed := snap
	changed.Coverage = append(changed.Coverage, Coverage{Source: "docker", State: "unreadable"})
	s.inventory = func(context.Context) (Snapshot, error) { return changed, nil }
	if _, e := s.BeginHandoff(context.Background(), []string{r.ID}, r.Owner, r.Resource, []string{r.Prefix}); e == nil {
		t.Fatal("new read failure borrowed an unrelated acknowledgement")
	}
	changed = snap
	changed.Observations = []Observation{{Prefix: r.Prefix, Owner: "foreign", Resource: "new", Basis: "observed_native"}}
	if _, e := s.BeginHandoff(context.Background(), []string{r.ID}, r.Owner, r.Resource, []string{r.Prefix}); e == nil {
		t.Fatal("fresh foreign overlap accepted")
	}
}
func TestIPAMFailedAndInterruptedHandoffsKeepAllocationHeld(t *testing.T) {
	for _, outcome := range []string{"failed", "restarted", "observed"} {
		t.Run(outcome, func(t *testing.T) {
			s := testService(t, observedSnapshot())
			p := pool(t, s, "10.244.0.0/16", 24)
			r := reserve(t, s, p, "one")
			h, e := s.BeginHandoff(context.Background(), []string{r.ID}, r.Owner, r.Resource, []string{r.Prefix})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.BeginHandoff(context.Background(), []string{r.ID}, r.Owner, r.Resource, []string{r.Prefix}); e == nil {
				t.Fatal("reservation handed off twice")
			}
			if e = s.Release(context.Background(), r.ID); e == nil {
				t.Fatal("released an active native handoff")
			}
			switch outcome {
			case "failed":
				e = s.FinishHandoff(context.Background(), h, "", fmt.Errorf("lost response"))
			case "observed":
				e = s.FinishHandoff(context.Background(), h, "native-id", nil)
			case "restarted":
				s = New(&store.Store{DB: s.db}, s.inventory)
				e = s.Ready()
			}
			if e != nil {
				t.Fatal(e)
			}
			view, e := s.View(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			state := view.Reservations[0].State
			if outcome == "observed" && state != "observed" || outcome != "observed" && state != "review_required" {
				t.Fatal("native outcome was invented", state)
			}
			next := reserve(t, s, p, "two")
			if next.Prefix == r.Prefix {
				t.Fatal("possible native allocation freed")
			}
			if e = s.Release(context.Background(), r.ID); e != nil {
				t.Fatal(e)
			}
			if e = s.FinishHandoff(context.Background(), h, "late-native-id", nil); e == nil {
				t.Fatal("stale completion overwrote released planning state")
			}
		})
	}
}
func TestIPAMValidationAndPoolRetirementProtectPlanningOwnership(t *testing.T) {
	s := testService(t, observedSnapshot())
	p := pool(t, s, "10.244.0.0/16", 24)
	for _, input := range []PoolRequest{{Name: "mapped", Prefix: "::ffff:10.0.0.0/112", AllocationBits: 120}, {Name: "hostbits", Prefix: "10.244.1.1/24", AllocationBits: 24}, {Name: "wide", Prefix: "10.244.0.0/16", AllocationBits: 8}} {
		if _, e := s.CreatePool(context.Background(), input); e == nil {
			t.Fatal("bad pool accepted", input)
		}
	}
	if _, e := s.CreatePool(context.Background(), PoolRequest{Name: "overlap", Prefix: "10.244.0.0/17", AllocationBits: 24}); e == nil {
		t.Fatal("overlapping shared managers created")
	}
	r := reserve(t, s, p, "one")
	if e := s.RetirePool(context.Background(), p.ID); e == nil {
		t.Fatal("pool retired with active reservation")
	}
	if e := s.Release(context.Background(), r.ID); e != nil {
		t.Fatal(e)
	}
	if e := s.RetirePool(context.Background(), p.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Reserve(context.Background(), ReserveRequest{PoolID: p.ID, Owner: "docker_network", Resource: "two"}, "operator"); e == nil {
		t.Fatal("retired pool allocated")
	}
}

func TestIPAMUnselectedNativePrefixCannotBorrowHeldPlan(t *testing.T) {
	s := testService(t, observedSnapshot())
	p := pool(t, s, "10.244.0.0/16", 24)
	r := reserve(t, s, p, "owner")
	if e := s.CheckUnselectedReservations(context.Background(), []string{r.Prefix}); e == nil {
		t.Fatal("omitting identity borrowed a held plan")
	}
	if e := s.CheckUnselectedReservations(context.Background(), []string{"10.245.0.0/24"}); e != nil {
		t.Fatal(e)
	}
	if e := s.CheckUnselectedReservations(context.Background(), nil); e != nil {
		t.Fatal("legacy automatic native allocation changed", e)
	}
	if e := s.Release(context.Background(), r.ID); e != nil {
		t.Fatal(e)
	}
	if e := s.CheckUnselectedReservations(context.Background(), []string{r.Prefix}); e != nil {
		t.Fatal("released plan remained active", e)
	}
}
