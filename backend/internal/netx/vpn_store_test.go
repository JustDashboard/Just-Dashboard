package netx

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestVPNStoreLifecycle(t *testing.T) {
	s := vpnService(t)
	ctx := context.Background()
	st := s.vpn

	id1, err := st.Save(ctx, VPNClient{Iface: "wg0", PublicKey: "pk1", Name: "Phone", Kind: "device", Address: "10.8.0.2", CreatedBy: "alice", CreatedAt: time.Unix(1700000000, 0)}, "[Interface]\nPrivateKey = secret\n")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := st.Save(ctx, VPNClient{Iface: "wg0", PublicKey: "pk2", Name: "Office", Kind: "site"}, "cfg2")
	if err != nil {
		t.Fatal(err)
	}
	idOther, err := st.Save(ctx, VPNClient{Iface: "wg1", PublicKey: "pk1", Name: "Other"}, "cfg3")
	if err != nil {
		t.Fatal(err)
	}
	if id1 == id2 || id2 == idOther {
		t.Fatalf("ids = %d %d %d", id1, id2, idOther)
	}

	// The configuration is stored sealed, and read back opened.
	var raw string
	if err := s.db.QueryRow(`SELECT config_sealed FROM network_vpn_clients WHERE id = ?`, id1).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "sealed:[Interface]\nPrivateKey = secret\n" || !strings.HasPrefix(raw, "sealed:") {
		t.Errorf("stored = %q", raw)
	}
	got, err := st.Get(ctx, "wg0", id1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config != "[Interface]\nPrivateKey = secret\n" || got.Name != "Phone" || got.Address != "10.8.0.2" || got.CreatedBy != "alice" ||
		got.CreatedAt.Unix() != 1700000000 || !got.HasConfig {
		t.Errorf("got = %+v", got)
	}
	// A row is read under its own tunnel only.
	if _, err := st.Get(ctx, "wg1", id1); !errors.Is(err, ErrNotFound) {
		t.Errorf("another tunnel's id: %v", err)
	}

	list, err := st.List(ctx, "wg0")
	if err != nil || len(list) != 2 || list[0].Name != "Phone" || list[1].Name != "Office" || !list[0].HasConfig || list[0].Config != "" {
		t.Fatalf("list = %+v err = %v: the listing carries no configuration", list, err)
	}

	// Forget keeps the row and loses the key.
	if err := st.Forget(ctx, "wg0", id1); err != nil {
		t.Fatal(err)
	}
	got, err = st.Get(ctx, "wg0", id1)
	if !errors.Is(err, ErrForgotten) || got.Name != "Phone" || got.Config != "" || got.HasConfig {
		t.Fatalf("forgotten: %+v %v", got, err)
	}
	if list, _ := st.List(ctx, "wg0"); list[0].HasConfig || list[1].Name != "Office" {
		t.Errorf("list = %+v", list)
	}
	if err := st.Forget(ctx, "wg0", 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("forgetting nothing: %v", err)
	}
	if err := st.Forget(ctx, "wg1", id1); !errors.Is(err, ErrNotFound) {
		t.Errorf("forgetting under another tunnel: %v", err)
	}

	if err := st.Delete(ctx, "wg0", id2); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(ctx, "wg0", id2); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting twice: %v", err)
	}
	if _, err := st.Get(ctx, "wg0", id2); !errors.Is(err, ErrNotFound) {
		t.Errorf("a deleted row is not found: %v", err)
	}

	if err := st.DeleteInterface(ctx, "wg0"); err != nil {
		t.Fatal(err)
	}
	if l, _ := st.List(ctx, "wg0"); len(l) != 0 {
		t.Errorf("wg0 rows = %v", l)
	}
	if l, _ := st.List(ctx, "wg1"); len(l) != 1 {
		t.Errorf("wg1 rows = %v: removing a tunnel removes its own clients only", l)
	}
	if err := st.DeleteInterface(ctx, "wg9"); err != nil {
		t.Errorf("removing the clients of a tunnel that had none: %v", err)
	}
}

func TestVPNStoreRefusesTheSameKeyTwiceOnATunnel(t *testing.T) {
	s := vpnService(t)
	ctx := context.Background()
	if _, err := s.vpn.Save(ctx, VPNClient{Iface: "wg0", PublicKey: "pk", Name: "a"}, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.vpn.Save(ctx, VPNClient{Iface: "wg0", PublicKey: "pk", Name: "b"}, "x"); err == nil {
		t.Fatal("a public key is one peer of a tunnel")
	}
}

func TestVPNStoreSealFailureStoresNothing(t *testing.T) {
	s := vpnService(t)
	s.vpn.seal = func(string) (string, error) { return "", errors.New("no master key") }
	if _, err := s.vpn.Save(context.Background(), VPNClient{Iface: "wg0", PublicKey: "pk", Name: "a"}, "secret"); err == nil {
		t.Fatal("want an error")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM network_vpn_clients`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows = %d %v: a configuration that could not be sealed must not be stored in the clear", n, err)
	}
}
