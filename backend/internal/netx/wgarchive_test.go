package netx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wgArchivedText is the managed fixture on a network the host fixture leaves
// free (it routes 10.8.0.0/24 itself).
func wgArchivedText(t *testing.T) string {
	return strings.ReplaceAll(fixture(t, "wg-managed.conf"), "10.8.0.", "10.40.0.")
}

// wgArchiveHost is a host whose wg0 was removed: its file sits in the archive,
// nothing holds its port, and the kernel has no WireGuard device.
func wgArchiveHost(t *testing.T) (*Service, *recorder, string) {
	t.Helper()
	s := vpnService(t)
	wgWriteFile(t, filepath.Join(wgProcNet, "udp"), wgUDPTable(68))
	file := "wg0.conf.1790000000"
	wgWriteFile(t, filepath.Join(s.paths.WireGuard, wgRemovedDir, file), wgArchivedText(t))
	rec := record(t)
	wgHostReplies(t, rec)
	rec.on("wg show all dump", "").
		on("ip -j route get 198.51.100.7", fixture(t, "wg-ip-route-get-client.json")).
		on("systemctl enable --now wg-quick@wg0", "").
		on("systemctl is-enabled wg-quick@wg0", "enabled\n").
		on("systemctl is-active wg-quick@wg0", "active\n")
	return s, rec, file
}

func TestWireGuardArchiveListsWithoutKeys(t *testing.T) {
	s, _, file := wgArchiveHost(t)
	wgWriteFile(t, filepath.Join(s.paths.WireGuard, wgRemovedDir, "wg1.conf.1790000100"), fixture(t, "wg-handwritten.conf"))
	wgWriteFile(t, filepath.Join(s.paths.WireGuard, wgRemovedDir, "notes.txt"), "not an archive")
	list, err := s.WireGuardArchive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "wg1" || list[1].File != file {
		t.Fatalf("archive = %+v", list)
	}
	if list[0].Restorable || !strings.Contains(list[0].Refusal, "not written by the dashboard") {
		t.Errorf("hand-written = %+v", list[0])
	}
	a := list[1]
	if !a.Restorable || a.ListenPort != 51820 || a.Peers != 2 || a.Endpoint != "203.0.113.20:51820" || a.ArchivedAt != 1790000000 ||
		strings.Join(a.Addresses, ",") != "10.40.0.1/24" {
		t.Errorf("archived = %+v", a)
	}
	raw := wgMustString(t, list)
	for _, secret := range fixSecrets {
		if strings.Contains(raw, secret) {
			t.Fatalf("a key is in the archive listing: %s", secret)
		}
	}
}

func TestWireGuardArchiveRefusesWhatNoLongerFits(t *testing.T) {
	ctx := context.Background()
	t.Run("the port is taken", func(t *testing.T) {
		s, _, _ := wgArchiveHost(t)
		wgWriteFile(t, filepath.Join(wgProcNet, "udp"), wgUDPTable(51820))
		list, _ := s.WireGuardArchive(ctx)
		if list[0].Restorable || !strings.Contains(list[0].Refusal, "UDP port 51820") {
			t.Fatalf("archive = %+v", list)
		}
	})
	t.Run("the name is taken", func(t *testing.T) {
		s, rec, file := wgArchiveHost(t)
		wgInstallConf(t, s, "wg0", "wg-handwritten.conf")
		if _, err := s.RestoreWireGuard(ctx, file, "", "alice"); err == nil || !strings.Contains(err.Error(), "exists again") {
			t.Fatalf("err = %v", err)
		}
		if rec.ran("systemctl enable") {
			t.Fatal("a refused restore started something")
		}
	})
	t.Run("a peer's network holds the operator", func(t *testing.T) {
		s, _, file := wgArchiveHost(t)
		_, err := s.RestoreWireGuard(ctx, file, "192.168.77.20", "alice")
		if !errors.Is(err, ErrGuarded) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a name that is not an archive", func(t *testing.T) {
		s, _, _ := wgArchiveHost(t)
		for _, bad := range []string{"../wg0.conf", "wg0.conf", "wg0.conf.1/..", "x"} {
			if _, err := s.RestoreWireGuard(ctx, bad, "", "a"); !errors.Is(err, ErrNotFound) {
				t.Errorf("%q: %v", bad, err)
			}
		}
	})
}

func TestRestoreWireGuardPutsTheTunnelBack(t *testing.T) {
	s, rec, file := wgArchiveHost(t)
	ctx := context.Background()
	s.wg.note(ctx, WGEvent{Iface: "wg0", Kind: "removed", Detail: "archived as " + file + "; its IPv4 exit was withdrawn"})
	// An orphaned row of a tunnel removed by hand under the same name.
	_, _ = s.db.Exec(`INSERT INTO network_vpn_clients (iface, public_key, name, config_sealed, created_at) VALUES ('wg0', ?, 'old', 'sealed:x', 1)`, fixPeerC)

	res, err := s.RestoreWireGuard(ctx, file, "198.51.100.7", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if res.Interface.Name != "wg0" || !res.Interface.Managed || len(res.Interface.Peers) != 2 {
		t.Fatalf("restored = %+v", res.Interface)
	}
	if !strings.Contains(strings.Join(res.Warnings, " "), "IPv4 exit was withdrawn") {
		t.Errorf("warnings = %v", res.Warnings)
	}
	got, err := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
	if err != nil || string(got) != wgArchivedText(t) {
		t.Fatalf("the file is not back byte for byte: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.paths.WireGuard, wgRemovedDir, file)); !os.IsNotExist(err) {
		t.Error("the archive copy must be the restored file, not a duplicate of its key")
	}
	if list, _ := s.vpn.List(ctx, "wg0"); len(list) != 0 {
		t.Errorf("an orphaned private key survived the restore: %+v", list)
	}
	// The operator's path is read before the tunnel starts and compared after.
	var reads []int
	cmds := rec.commands()
	for i, c := range cmds {
		if strings.HasPrefix(c, "ip -j route get 198.51.100.7") {
			reads = append(reads, i)
		}
	}
	start := wgIndexOf(cmds, "systemctl enable --now wg-quick@wg0")
	if len(reads) != 2 || reads[0] > start || reads[1] < start {
		t.Fatalf("client path reads %v around start %d:\n%s", reads, start, strings.Join(cmds, "\n"))
	}
	events, _ := s.wg.events(ctx, "wg0", "", 1)
	if len(events) != 1 || events[0].Kind != "restored" || events[0].Actor != "alice" {
		t.Fatalf("events = %+v", events)
	}
}

func TestRestoreWireGuardThatWillNotStartReturnsToTheArchive(t *testing.T) {
	s, rec, file := wgArchiveHost(t)
	rec.mu.Lock()
	rec.replies = append([]reply{
		{prefix: "systemctl enable --now wg-quick@wg0", err: fmt.Errorf("Job for wg-quick@wg0.service failed")},
		{prefix: "systemctl disable --now wg-quick@wg0", out: ""},
		{prefix: "systemctl is-active wg-quick@wg0", out: "inactive\n"},
	}, rec.replies...)
	rec.mu.Unlock()
	if _, err := s.RestoreWireGuard(context.Background(), file, "", "alice"); err == nil {
		t.Fatal("a tunnel that does not start is not restored")
	}
	if _, err := os.Stat(filepath.Join(s.paths.WireGuard, wgRemovedDir, file)); err != nil {
		t.Fatalf("the archive must keep the file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.paths.WireGuard, "wg0.conf")); !os.IsNotExist(err) {
		t.Fatal("a failed restore left the file in place")
	}
	events, _ := s.wg.events(context.Background(), "wg0", "", 1)
	if len(events) != 1 || events[0].Kind != "restore_failed" || events[0].Outcome != wgOutcomeFailed {
		t.Fatalf("events = %+v", events)
	}
}

func TestRemoveWireGuardRecordsTheArchiveAndWithdrawnExit(t *testing.T) {
	s, rec := wgAddPeerHost(t)
	rec.on("systemctl disable --now wg-quick@wg0", "")
	ctx := context.Background()
	if err := s.RemoveWireGuard(ctx, "wg0", "", "alice"); err != nil {
		t.Fatal(err)
	}
	events, _ := s.wg.events(ctx, "wg0", "", 1)
	if len(events) != 1 || events[0].Kind != "removed" || !strings.Contains(events[0].Detail, "archived as wg0.conf.") {
		t.Fatalf("events = %+v", events)
	}
}
