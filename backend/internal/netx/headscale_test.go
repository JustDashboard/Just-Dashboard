package netx

import (
	"context"
	"strings"
	"testing"
)

func TestHeadscaleBinaryNodesAndUsers(t *testing.T) {
	s := vpnService(t)
	rec := record(t)
	rec.on("headscale nodes list -o json", fixture(t, "headscale-nodes.json")).
		on("headscale users list -o json", fixture(t, "headscale-users.json"))
	v := s.Headscale(context.Background())
	if !v.Installed || v.Container != "" || v.Error != "" {
		t.Fatalf("view = %+v", v)
	}
	if len(v.Nodes) != 3 {
		t.Fatalf("nodes = %+v", v.Nodes)
	}
	// Online first, then by name.
	if v.Nodes[0].Name != "laptop" || v.Nodes[1].Name != "phone" || v.Nodes[2].Name != "server" {
		t.Errorf("order = %s %s %s", v.Nodes[0].Name, v.Nodes[1].Name, v.Nodes[2].Name)
	}
	laptop := v.Nodes[0]
	if laptop.ID != "1" || laptop.GivenName != "alice-laptop" || !laptop.Online || laptop.LastSeen != 1790000000 || laptop.User != "alice" ||
		strings.Join(laptop.IPAddresses, ",") != "100.64.0.1,fd7a:115c:a1e0::1" || laptop.ForcedTags == nil {
		t.Errorf("laptop = %+v", laptop)
	}
	// An id that is a number, and a time that is a string, both read.
	if phone := v.Nodes[1]; phone.ID != "3" || phone.LastSeen == 0 || phone.ForcedTags == nil {
		t.Errorf("phone = %+v", phone)
	}
	if server := v.Nodes[2]; server.Online || strings.Join(server.ForcedTags, ",") != "tag:server" || server.User != "bob" {
		t.Errorf("server = %+v", server)
	}
	counts := map[string]int{}
	for _, u := range v.Users {
		counts[u.Name] = u.Nodes
	}
	if len(v.Users) != 3 || counts["alice"] != 2 || counts["bob"] != 1 || counts["carol"] != 0 {
		t.Errorf("users = %+v", v.Users)
	}
	raw := wgMustString(t, v)
	for _, secret := range []string{"mkey:", "nodekey:", "machineKey"} {
		if strings.Contains(raw, secret) {
			t.Errorf("a node's key leaked: %s", secret)
		}
	}
	// Reading never changes anything.
	for _, c := range rec.commands() {
		if !strings.HasSuffix(c, "list -o json") {
			t.Errorf("ran %q", c)
		}
	}
}

func TestHeadscaleAbsentIsInformation(t *testing.T) {
	s := vpnService(t)
	rec := record(t, "headscale")
	v := s.Headscale(context.Background())
	if v.Installed || v.Nodes == nil || v.Users == nil || len(rec.commands()) != 0 {
		t.Fatalf("view = %+v ran %v", v, rec.commands())
	}
}

func TestHeadscaleOneFailingReadStillShowsTheOther(t *testing.T) {
	s := vpnService(t)
	rec := record(t)
	rec.fail("headscale nodes list", "Cannot get nodes: permission denied").
		on("headscale users list -o json", fixture(t, "headscale-users.json"))
	v := s.Headscale(context.Background())
	if len(v.Nodes) != 0 || len(v.Users) != 3 || !strings.Contains(v.Error, "nodes:") {
		t.Fatalf("view = %+v", v)
	}
	rec = record(t)
	rec.on("headscale nodes list", "not json").on("headscale users list", "{}")
	if v := s.Headscale(context.Background()); !strings.Contains(v.Error, "nodes") || !strings.Contains(v.Error, "users") {
		t.Fatalf("error = %q", v.Error)
	}
}

func TestHeadscaleContainer(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	s := vpnService(t)
	rec := record(t)
	rec.on("docker exec "+id+" headscale nodes list -o json", fixture(t, "headscale-nodes.json")).
		on("docker exec "+id+" headscale users list -o json", fixture(t, "headscale-users.json"))
	v := s.HeadscaleContainer(context.Background(), id, "headscale")
	if v.Installed || v.Container != "headscale" || len(v.Nodes) != 3 || len(v.Users) != 3 || v.Error != "" {
		t.Fatalf("view = %+v", v)
	}
	cmds := rec.commands()
	if len(cmds) != 2 || cmds[0] != "docker exec "+id+" headscale nodes list -o json" {
		t.Errorf("ran %v", cmds)
	}

	t.Run("an id that is not a container id is never an argument", func(t *testing.T) {
		rec := record(t)
		for _, bad := range []string{"--help", "-it", "abc", "../x", id + "a", "0123456789ABCDEF", "x; rm"} {
			v := s.HeadscaleContainer(context.Background(), bad, "h")
			if v.Error == "" || len(v.Nodes) != 0 {
				t.Errorf("%q was accepted: %+v", bad, v)
			}
		}
		if len(rec.commands()) != 0 {
			t.Errorf("ran %v", rec.commands())
		}
	})
	t.Run("without the docker command the container is named and nodes are unavailable", func(t *testing.T) {
		rec := record(t, "docker")
		v := s.HeadscaleContainer(context.Background(), id, "headscale")
		if v.Container != "headscale" || v.Error == "" || len(v.Nodes) != 0 || len(rec.commands()) != 0 {
			t.Fatalf("view = %+v", v)
		}
	})
}
