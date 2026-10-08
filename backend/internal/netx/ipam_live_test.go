package netx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netipam"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

func TestLiveIPAMObservesDualFamilyOwnersWithoutChangingThem(t *testing.T) {
	liveRequired(t)
	ns := newLiveNS(t)
	useLive(t, ns)
	ns.must(t, "ip", "link", "add", "native0", "type", "dummy")
	ns.must(t, "ip", "link", "set", "native0", "up")
	ns.must(t, "ip", "addr", "add", "10.244.0.1/24", "dev", "native0")
	ns.must(t, "ip", "addr", "add", "fd48:abcd::1/64", "dev", "native0")
	ns.must(t, "ip", "route", "add", "10.244.1.0/24", "dev", "native0", "table", "100")
	ns.must(t, "ip", "-6", "route", "add", "fd48:abcd:0:1::/64", "dev", "native0", "table", "100")
	ns.must(t, "ip", "netns", "add", "tenant")
	ns.must(t, "ip", "-n", "tenant", "link", "add", "inside0", "type", "dummy")
	ns.must(t, "ip", "-n", "tenant", "addr", "add", "10.244.2.1/24", "dev", "inside0")
	ns.must(t, "ip", "-n", "tenant", "addr", "add", "fd48:abcd:0:2::1/64", "dev", "inside0")
	before := ns.snapshot(t)
	nativeRun := run
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name != "ip" {
			return "", fmt.Errorf("fixture owner %s intentionally unavailable", name)
		}
		return nativeRun(ctx, name, args...)
	}
	s := testService(t)
	snapshot := s.IPAMInventory(context.Background(), Inventory{})
	seen := map[string]bool{}
	for _, p := range snapshot.Prefixes {
		seen[p.Prefix] = true
	}
	for _, prefix := range []string{"10.244.0.0/24", "10.244.1.0/24", "10.244.2.0/24", "fd48:abcd::/64", "fd48:abcd:0:1::/64", "fd48:abcd:0:2::/64"} {
		if !seen[prefix] {
			t.Fatal("native owner prefix absent", prefix, snapshot)
		}
	}
	st, e := storetest.Open(filepath.Join(t.TempDir(), "planner"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	planner := netipam.New(st, func(context.Context) (netipam.Snapshot, error) {
		v := netipam.Snapshot{}
		for _, p := range snapshot.Prefixes {
			v.Observations = append(v.Observations, netipam.Observation{Prefix: p.Prefix, Owner: p.Owner, Resource: p.Resource, Domain: p.Domain, Basis: p.Basis})
		}
		for _, c := range snapshot.Sources {
			v.Coverage = append(v.Coverage, netipam.Coverage{Source: c.Source, State: c.State, Detail: c.Detail, CheckedAt: c.CheckedAt})
		}
		return v, nil
	})
	for _, p := range []netipam.PoolRequest{{Name: "v4", Prefix: "10.244.0.0/16", AllocationBits: 24}, {Name: "v6", Prefix: "fd48:abcd::/48", AllocationBits: 64}} {
		created, e := planner.CreatePool(context.Background(), p)
		if e != nil {
			t.Fatal(e)
		}
		r, e := planner.Reserve(context.Background(), netipam.ReserveRequest{PoolID: created.ID, Owner: "docker_network", Resource: "native-evidence", AcknowledgeUnknown: true}, "fixture")
		if e != nil {
			t.Fatal(e)
		}
		if r.Prefix != map[string]string{"v4": "10.244.3.0/24", "v6": "fd48:abcd:0:3::/64"}[p.Name] {
			t.Fatal("shared allocation ignored readable route/namespace evidence", r.Prefix)
		}
	}
	after := ns.snapshot(t)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("inspection or planning changed native state")
	}
	if _, e := os.Stat(s.paths.Dir); !os.IsNotExist(e) {
		t.Fatal("planner wrote native managed configuration", e)
	}
}
