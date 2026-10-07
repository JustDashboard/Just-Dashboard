package netx

import (
	"context"
	"strings"
	"testing"
)

func TestBGPParsesNewerFRRWithBothFamilies(t *testing.T) {
	fams, err := parseBGPSummary(fixture(t, "routing-bgp-summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fams) != 2 || fams[0].Name != "ipv4Unicast" || fams[1].Name != "ipv6Unicast" {
		t.Fatalf("families = %+v", fams)
	}
	v4 := fams[0]
	if v4.RouterID != "192.0.2.1" || v4.LocalAS != 65000 || len(v4.Peers) != 3 {
		t.Fatalf("ipv4 = %+v", v4)
	}
	up := v4.Peers[0]
	if up.Address != "192.0.2.2" || up.Hostname != "edge-a" || up.RemoteAS != 65001 || up.State != "Established" ||
		up.UptimeSeconds != 93780 || up.Uptime != "1d02h03m" || up.PrefixesReceived != 4 || up.PrefixesSent != 2 ||
		up.MessagesReceived != 1042 || up.MessagesSent != 1040 {
		t.Fatalf("established peer = %+v", up)
	}
	down := v4.Peers[1]
	if down.State != "Active" || down.UptimeSeconds != 0 || down.PrefixesReceived != 0 || down.RemoteAS != 65002 {
		t.Fatalf("active peer = %+v", down)
	}
	// A 4-byte AS number.
	if big := v4.Peers[2]; big.RemoteAS != 4200000001 || big.ConnectionsDropped != 2 || big.PrefixesReceived != 0 || big.PrefixesSent != 1 {
		t.Fatalf("four-byte AS peer = %+v", big)
	}
	if p := fams[1].Peers[0]; p.Address != "2001:db8:ffff::2" || p.PrefixesReceived != 1 || p.UptimeSeconds != 7201 {
		t.Fatalf("ipv6 peer = %+v", p)
	}
}

func TestBGPParsesOlderFRRWithOneFamilyAtTheTopLevel(t *testing.T) {
	fams, err := parseBGPSummary(fixture(t, "routing-bgp-summary-old.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fams) != 1 || fams[0].LocalAS != 65000 || len(fams[0].Peers) != 1 {
		t.Fatalf("families = %+v", fams)
	}
	p := fams[0].Peers[0]
	// remoteAs as a string, prefixReceivedCount for pfxRcd, and an uptime with
	// no millisecond field: 2 weeks, 3 days, 4 hours.
	if p.RemoteAS != 65001 || p.PrefixesReceived != 3 || p.UptimeSeconds != 2*7*86400+3*86400+4*3600 || p.State != "Established" {
		t.Fatalf("peer = %+v", p)
	}
}

func TestBGPParsesWhatHasNoNeighbours(t *testing.T) {
	for name, in := range map[string]string{
		"a warning":  fixture(t, "routing-bgp-none.json"),
		"empty":      "",
		"empty json": "{}",
	} {
		fams, err := parseBGPSummary(in)
		if err != nil || fams == nil || len(fams) != 0 {
			t.Errorf("%s: %+v, %v", name, fams, err)
		}
	}
	if _, err := parseBGPSummary("% BGP instance not found"); err == nil {
		t.Error("plain text accepted as JSON")
	}
}

func TestBGPUptimeFormats(t *testing.T) {
	t.Parallel()
	cases := map[string]int64{"00:01:02": 62, "1d02h03m": 93780, "2w3d04h": 2*7*86400 + 3*86400 + 4*3600, "05m10s": 310, "never": 0, "": 0}
	for in, want := range cases {
		if got := parseFRRUptime(in); got != want {
			t.Errorf("parseFRRUptime(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestBGPNotInstalledIsInformationNotAnError(t *testing.T) {
	rec := record(t, "vtysh")
	s := testService(t)
	v, err := s.BGP(context.Background())
	if err != nil || v.Installed || v.Running || v.Families == nil || len(v.Families) != 0 {
		t.Fatalf("view = %+v, %v", v, err)
	}
	if len(rec.commands()) != 0 {
		t.Fatalf("ran %v without vtysh", rec.commands())
	}
}

func TestBGPReadsTheSummaryAndWhetherFRRRuns(t *testing.T) {
	rec := record(t).
		on("systemctl is-active frr", "active\n").
		on(`vtysh -c show bgp summary json`, fixture(t, "routing-bgp-summary.json"))
	s := testService(t)
	v, err := s.BGP(context.Background())
	if err != nil || !v.Installed || !v.Running || len(v.Families) != 2 || v.Error != "" {
		t.Fatalf("view = %+v, %v", v, err)
	}
	if got := rtMutations(rec); len(got) != 0 && !(len(got) == 1 && strings.HasPrefix(got[0], "vtysh")) {
		t.Fatalf("a read changed something: %v", got)
	}
}

func TestBGPReportsADaemonThatIsNotAnswering(t *testing.T) {
	record(t).
		fail("systemctl is-active frr", "inactive").
		fail("vtysh", "vtysh: bgpd is not running\nsecond line")
	s := testService(t)
	v, err := s.BGP(context.Background())
	if err != nil {
		t.Fatalf("an unanswering daemon is a reading, not a failure: %v", err)
	}
	if !v.Installed || v.Running || len(v.Families) != 0 || !strings.Contains(v.Error, "bgpd is not running") {
		t.Fatalf("view = %+v", v)
	}
}

func TestBGPUnreadableOutputIsAReading(t *testing.T) {
	record(t).on("systemctl is-active frr", "active").on("vtysh", "% No BGP process is configured")
	s := testService(t)
	v, err := s.BGP(context.Background())
	if err != nil || v.Error == "" || len(v.Families) != 0 {
		t.Fatalf("view = %+v, %v", v, err)
	}
}
