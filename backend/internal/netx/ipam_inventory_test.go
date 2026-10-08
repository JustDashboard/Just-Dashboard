package netx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIPAMInventoryKeepsFamilyAndPerNamespaceFailures(t *testing.T) {
	rec := record(t, "tailscale", "wg").
		on("ip -j -d link show", fixture(t, "ip-link.json")).
		on("ip -j addr show", fixture(t, "ip-addr.json")).
		on("ip -j route show table all", `[{"dst":"10.250.0.0/16","dev":"ens3","table":254,"protocol":"dhcp"},{"dst":"default","dev":"ens3"}]`).
		fail("ip -j -6 route show table all", "IPv6 family unreadable").
		on("ip -j netns list", `[{"name":"tenant-a"},{"name":"unreadable"}]`).
		on("ip -n tenant-a -j addr show", fixture(t, "links-ns-addr.json")).
		fail("ip -n unreadable -j addr show", "namespace changed during read")
	s := testService(t)
	got := s.IPAMInventory(context.Background(), Inventory{})
	family, namespace, provider := false, false, false
	for _, source := range got.Sources {
		family = family || source.Source == "host_routes/inet6" && source.State == "unreadable"
		namespace = namespace || source.Source == "namespace/unreadable" && source.State == "unreadable"
		provider = provider || source.Source == "foreign_provider_scope" && source.State == "unknown"
	}
	if !family || !namespace || !provider {
		t.Fatalf("failure became empty native inventory %#v", got.Sources)
	}
	found := false
	for _, p := range got.Prefixes {
		found = found || p.Prefix == "10.250.0.0/16"
	}
	if !found {
		t.Fatal("known provider-learned route disappeared")
	}
	for _, call := range rec.calls {
		if strings.Contains(call, " -batch ") || strings.Contains(call, " link add ") || strings.Contains(call, "wg show all dump") {
			t.Fatal("inventory mutated or queried secret material", call)
		}
	}
}
func TestIPAMWireGuardUnreadableConfigurationIsCoverageEvidence(t *testing.T) {
	record(t, "tailscale").on("ip -j -d link show", "[]").on("ip -j addr show", "[]").on("ip -j route show table all", "[]").on("ip -j -6 route show table all", "[]").on("ip -j netns list", "[]").on("wg show all allowed-ips", "wg0\tPUBLIC\t10.50.0.0/24 0.0.0.0/0\n")
	s := testService(t)
	os.MkdirAll(s.paths.WireGuard, 0o700)
	if e := os.WriteFile(filepath.Join(s.paths.WireGuard, "good.conf"), []byte("[Interface]\nPrivateKey = SECRET\nAddress = 10.51.0.1/24\n[Peer]\nPresharedKey = SECRET2\nAllowedIPs = 10.52.0.0/24, 0.0.0.0/0\n"), 0o600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(filepath.Join(s.paths.WireGuard, "missing"), filepath.Join(s.paths.WireGuard, "unreadable.conf")); e != nil {
		t.Fatal(e)
	}
	got := s.IPAMInventory(context.Background(), Inventory{})
	unknown := false
	prefixes := map[string]bool{}
	for _, source := range got.Sources {
		unknown = unknown || source.Source == "wireguard_file/unreadable" && source.State == "unreadable"
		if strings.Contains(source.Detail, "SECRET") {
			t.Fatal("native secret escaped into evidence")
		}
	}
	for _, p := range got.Prefixes {
		prefixes[p.Prefix] = true
	}
	if !unknown || !prefixes["10.50.0.0/24"] || !prefixes["10.51.0.0/24"] || !prefixes["10.52.0.0/24"] || prefixes["0.0.0.0/0"] {
		t.Fatalf("file/native/forwarding distinction lost %#v", got)
	}
}
