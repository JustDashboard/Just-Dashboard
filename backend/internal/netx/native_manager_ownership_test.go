package netx

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeUnverifiedStructureAndPreservedFieldsRefuse(t *testing.T) {
	for _, contract := range []NativeContract{{Members: []string{"port0"}}, {Master: "bond0"}, {BondMode: "active-backup"}, {VRFTable: 100}} {
		kind := "physical"
		if contract.BondMode != "" {
			kind = "bond"
		}
		if contract.VRFTable != 0 {
			kind = "vrf"
		}
		p := &nativeProfile{View: NativeProfileView{Kind: kind, Contract: contract}}
		if nativeStructureGuard(p) == nil {
			t.Fatalf("unverified native topology admitted: %+v", p.View)
		}
	}
	for _, fixture := range []struct{ owner, data string }{
		{"networkd", "[Match]\nName=d0\n[Network]\nBridge=foreign0\n"},
		{"networkd", "[Link]\nMTUBytes=9000\n"},
		{"networkd", "[Network]\nIPMasquerade=yes\n"},
		{"NetworkManager", "[connection]\ncontroller=foreign0\n"},
		{"NetworkManager", "[connection]\nzone=trusted\n"},
		{"NetworkManager", "[ethernet]\ncloned-mac-address=random\n"},
		{"NetworkManager", "[ipv4]\nroute1_options=table=100\n"},
	} {
		if nativeProfileFieldsGuard(nil, []byte(fixture.data), fixture.owner) == nil {
			t.Errorf("whole-profile activation admitted an unrepresented effect: %s %q", fixture.owner, fixture.data)
		}
	}
	if err := nativeProfileFieldsGuard(nil, []byte("# retained comment = value\n[Match]\nName = d0\n[Network]\nDHCP=no\n"), "networkd"); err != nil {
		t.Fatal("safe native comments/whitespace refused:", err)
	}
}

func TestNativeSavedNMTypeMustMatchKernel(t *testing.T) {
	p := &nativeProfile{UUID: "55afc950-b609-4466-955a-2fa4788c0140", View: NativeProfileView{Device: "d0", Kind: "dummy"}}
	data := "[connection]\nuuid=" + p.UUID + "\ninterface-name=d0\ntype=bond\n[ipv4]\nmethod=disabled\n[ipv6]\nmethod=disabled\n"
	if _, err := parseNativeNM([]byte(data), p); err == nil {
		t.Fatal("saved bond was accepted for an active dummy device")
	}
}

func TestNativePinnedNMImageRefusesMigratingWriter(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	f := newNativeRecoveryFixture(t, "networkd")
	image, err := os.ReadFile("/usr/bin/dbus-daemon")
	if err != nil || len(image) > 2<<20 || !bytes.Contains(image, []byte("libsystemd.so.0")) {
		t.Fatal("bounded ELF fixture with the systemd dependency is unavailable")
	}
	path := nativeHostPath("/proc/17/exe")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, image, 0o600); err != nil {
		t.Fatal(err)
	}
	nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
		if tool != "busctl" || args[0] != "--address=unix:path=/run/dbus/system_bus_socket,guid="+f.u.TransportGUID || !strings.HasSuffix(strings.Join(args, " "), "GetConnectionUnixProcessID s "+f.u.OwnerBus) {
			t.Fatalf("running-image probe lost its pinned native identity: %s %v", tool, args)
		}
		return `{"type":"u","data":[17]}`, nil
	}
	ctx := nativePinnedBus(context.Background(), f.u.TransportGUID)
	if err := nativeNMOriginGuard(ctx, f.u.OwnerBus); err != nil {
		t.Fatal("nonmigrating running image refused:", err)
	}
	image = bytes.Replace(image, []byte("libsystemd.so.0"), []byte("libnetplan.so.1"), 1)
	if err := os.WriteFile(path, image, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := nativeNMOriginGuard(ctx, f.u.OwnerBus); err == nil || !strings.Contains(err.Error(), "migrates") {
		t.Fatal("compiled Netplan writer was admitted:", err)
	}
	if err := os.WriteFile(path, []byte("unreadable image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := nativeNMOriginGuard(ctx, f.u.OwnerBus); err == nil {
		t.Fatal("unreadable running image was admitted")
	}
}
