package netx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A file is rendered back exactly as it was read, whoever wrote it: nothing
// is normalised, so the dashboard's edit of a managed file changes only the
// lines it meant to, and a hand-written file read for display is not
// disturbed in any way.
func TestWGConfRoundTripsByteForByte(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"wg-managed.conf", "wg-handwritten.conf"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			text := fixture(t, name)
			if got := parseWGConf(text).render(); got != text {
				t.Fatalf("round trip changed the file:\n--- got\n%s\n--- want\n%s", got, text)
			}
		})
	}
}

func TestWGConfManagedMarkerDecidesWhoMayEdit(t *testing.T) {
	t.Parallel()
	if !parseWGConf(fixture(t, "wg-managed.conf")).managed {
		t.Error("a file that starts with the marker is managed")
	}
	if parseWGConf(fixture(t, "wg-handwritten.conf")).managed {
		t.Error("a hand-written file is read-only")
	}
	if parseWGConf("[Interface]\n# Managed by Just Dashboard\n").managed {
		t.Error("the marker counts only as the first line")
	}
}

func TestWGConfReadsSettingsAndNotes(t *testing.T) {
	t.Parallel()
	c := parseWGConf(fixture(t, "wg-managed.conf"))
	iface := c.iface()
	if iface == nil {
		t.Fatal("no [Interface]")
	}
	if got := iface.get("address"); got != "10.8.0.1/24" {
		t.Errorf("address = %q", got)
	}
	meta := iface.bodyMeta()
	if meta["endpoint"] != "203.0.113.20:51820" || meta["dns"] != "1.1.1.1,1.0.0.1" {
		t.Errorf("interface notes = %v", meta)
	}
	peers := c.peers()
	if len(peers) != 2 {
		t.Fatalf("%d peers, want 2", len(peers))
	}
	office := peerConf(peers[1])
	if office.id != 2 || office.name != "Office" || office.kind != "site" || office.keepalive != 25 {
		t.Errorf("office = %+v", office)
	}
	if len(office.allowedIPs) != 2 || office.allowedIPs[1] != "192.168.77.0/24" {
		t.Errorf("allowed ips = %v", office.allowedIPs)
	}
	if want := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC); !office.created.Equal(want) {
		t.Errorf("created = %v", office.created)
	}
	// The hand-written note above the second peer stays with that peer, not
	// with the first one's body.
	if !strings.Contains(c.render(), "# a note somebody wrote by hand\n# jd:id=2") {
		t.Error("the note moved")
	}
}

func TestWGConfHandWrittenValuesHaveTheirCommentsStripped(t *testing.T) {
	t.Parallel()
	c := parseWGConf(fixture(t, "wg-handwritten.conf"))
	if got := c.iface().get("address"); got != "10.9.9.1/24" {
		t.Errorf("address = %q, want the inline comment removed", got)
	}
	p := peerConf(c.peers()[0])
	if p.keepalive != 30 {
		t.Errorf("keepalive from a lower-case key = %d", p.keepalive)
	}
	if p.id != 0 || p.name != "" {
		t.Errorf("a peer somebody else wrote has no notes: %+v", p)
	}
}

func TestWGConfEditKeepsEverythingElse(t *testing.T) {
	t.Parallel()
	text := fixture(t, "wg-managed.conf")
	c := parseWGConf(text)
	c.iface().set("MTU", "1380")
	c.iface().set("DNS", "9.9.9.9")
	out := c.render()
	if !strings.Contains(out, "MTU = 1380\n") || strings.Contains(out, "MTU = 1420") {
		t.Error("the MTU was not replaced in place")
	}
	for _, keep := range []string{"Table = off\n", "SaveConfig = false\n", "# a note somebody wrote by hand\n", "# jd:endpoint=203.0.113.20:51820\n"} {
		if !strings.Contains(out, keep) {
			t.Errorf("lost %q", keep)
		}
	}
	// A new setting goes after the last setting, not after the blank line
	// and comments that lead the next block.
	if !strings.Contains(out, "Table = off\nDNS = 9.9.9.9\n\n# jd:id=1") {
		t.Errorf("DNS was added in the wrong place:\n%s", out)
	}
}

func TestWGConfAppendAndRemovePeerIsReversible(t *testing.T) {
	t.Parallel()
	text := fixture(t, "wg-managed.conf")
	c := parseWGConf(text)
	added := newPeerSection(9, "Tablet", "device", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		[][2]string{{"PublicKey", "KEY"}, {"AllowedIPs", "10.8.0.4/32"}})
	c.appendPeer(added)
	out := c.render()
	want := "\n# jd:id=9\n# jd:name=Tablet\n# jd:kind=device\n# jd:created=2026-10-01T00:00:00Z\n[Peer]\nPublicKey = KEY\nAllowedIPs = 10.8.0.4/32\n"
	if !strings.HasSuffix(out, want) {
		t.Fatalf("the peer block is not as written:\n%s", out)
	}
	// And it parses back as the peer it was.
	back := parseWGConf(out)
	if p := peerConf(back.peers()[2]); p.id != 9 || p.name != "Tablet" || p.publicKey != "KEY" {
		t.Errorf("parsed back as %+v", p)
	}
	c.removePeer(added)
	if got := c.render(); got != text {
		t.Fatalf("removing the peer did not restore the file:\n%s", got)
	}
}

func TestWGConfRemovingAPeerTakesItsNotesWithIt(t *testing.T) {
	t.Parallel()
	c := parseWGConf(fixture(t, "wg-managed.conf"))
	c.removePeer(c.peers()[0])
	out := c.render()
	if strings.Contains(out, "Phone") || strings.Contains(out, "jd:id=1") {
		t.Error("the removed peer's notes remain")
	}
	if !strings.Contains(out, "# jd:name=Office") || !strings.Contains(out, "Table = off") {
		t.Error("something else was removed")
	}
}

func TestWriteWGConfIs0600AndAtomic(t *testing.T) {
	s := testService(t)
	c := parseWGConf(fixture(t, "wg-managed.conf"))
	if err := s.writeWGConf("wg0", c); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.paths.WireGuard, "wg0.conf")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", st.Mode().Perm())
	}
	if dir, _ := os.Stat(s.paths.WireGuard); dir.Mode().Perm() != 0o700 {
		t.Errorf("directory mode = %v, want 0700", dir.Mode().Perm())
	}
	if entries, _ := os.ReadDir(s.paths.WireGuard); len(entries) != 1 {
		t.Errorf("a temporary file was left behind: %v", entries)
	}
	if _, err := s.wgConfPath("../etc/passwd"); err == nil {
		t.Error("a path was accepted as an interface name")
	}
}

func TestValidWGName(t *testing.T) {
	t.Parallel()
	for name, ok := range map[string]bool{
		"wg0": true, "office-vpn": true, "a.b_c": true,
		"": false, "wg0@x": false, "../wg0": false, "a b": false, "wg0;rm": false, "averyveryverylongname": false, "wg=0": false,
	} {
		if err := validWGName(name); (err == nil) != ok {
			t.Errorf("validWGName(%q) = %v, want ok=%v", name, err, ok)
		}
	}
}
