package netsec

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"
)

const ssListeners = `Netid State  Recv-Q Send-Q Local Address:Port  Peer Address:Port Process
udp   UNCONN 0      0      127.0.0.53%lo:53        0.0.0.0:*    users:(("systemd-resolve",pid=812,fd=13))
tcp   LISTEN 0      4096   0.0.0.0:22              0.0.0.0:*    users:(("sshd",pid=1001,fd=3),("systemd",pid=1,fd=50))
tcp   LISTEN 0      511    [::]:443                [::]:*       users:(("caddy",pid=2020,fd=7))
tcp   LISTEN 0      128    127.0.0.1:5432          0.0.0.0:*`

func TestListenersStructureRowsAndLinkToPorts(t *testing.T) {
	rows := parseListeners(ssListeners)
	want := []listenRow{
		{"udp", "127.0.0.53", "53", "systemd-resolve (812)"},
		{"tcp", "0.0.0.0", "22", "sshd (1001), systemd (1)"},
		{"tcp", "::", "443", "caddy (2020)"},
		{"tcp", "127.0.0.1", "5432", ""},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %+v", rows)
	}
	netstat := parseListeners("Proto Recv-Q Send-Q Local Address Foreign Address State PID/Program name\ntcp 0 0 0.0.0.0:80 0.0.0.0:* LISTEN 77/nginx\ntcp6 0 0 :::8080 :::* LISTEN 88/node\nudp 0 0 0.0.0.0:68 0.0.0.0:* 99/dhclient")
	if len(netstat) != 3 || netstat[0].Process != "nginx (77)" || netstat[1].Address != "::" || netstat[2].Port != "68" {
		t.Fatalf("netstat = %+v", netstat)
	}
	res := &ProbeResult{}
	describeListeners(res, ssListeners, "ss")
	table := tableByID(res, "listeners")
	if table == nil || len(table.RowLinks) != len(table.Rows) || table.Rows[0][2] != "22" || table.Rows[0][4] != "all addresses" {
		t.Fatalf("table = %+v", table)
	}
	if table.RowLinks[0] != "/proxy/ports?q=%3A22&socket=tcp-ipv4-0.0.0.0-22" || table.RowLinks[1] != "/proxy/ports?q=%3A53&socket=udp-ipv4-127.0.0.53-53" {
		t.Fatalf("links = %v", table.RowLinks)
	}
	if v, _ := metricValue(res, "beyond_loopback"); v != 2 || !hasLink(res, "/proxy/ports") {
		t.Fatalf("exposure = %+v", res)
	}
}

func TestEgressClassifiesSourcesPerFamilyWithoutClaimingThePublicAddress(t *testing.T) {
	stubLANDiagnostics(t)
	diagnosticRun = func(_ context.Context, _ time.Duration, cmd string, args ...string) (string, string, error) {
		switch strings.Join(args, " ") {
		case "-4 route get 1.1.1.1":
			return "1.1.1.1 via 100.64.0.1 dev eth0 src 100.64.12.7 uid 0\n    cache", "1ms", nil
		case "-4 route show default":
			return "default via 100.64.0.1 dev eth0 proto dhcp metric 100", "1ms", nil
		case "-6 route get 2606:4700:4700::1111":
			return "2606:4700:4700::1111 from :: via fe80::1 dev eth0 proto ra src 2001:db8::7 metric 1024 pref medium", "1ms", nil
		case "-6 route show default":
			return "default via fe80::1 dev eth0 proto ra metric 1024 pref medium\ndefault via fe80::2 dev eth1 proto ra metric 2048 pref medium", "1ms", nil
		}
		t.Fatalf("unexpected %s %v", cmd, args)
		return "", "", nil
	}
	res, err := New().Egress(t.Context())
	if err != nil || !res.OK || res.Verdict != ProbeFindings || !hasFinding(res, "ipv4-nat") {
		t.Fatalf("%+v %v", res, err)
	}
	table := tableByID(res, "egress")
	if table.Rows[0][5] != "shared address space (carrier-grade NAT)" || table.Rows[1][5] != "global" || table.Rows[1][6] != "2" {
		t.Fatalf("egress = %+v", table.Rows)
	}
	if !hasLink(res, "/network/external") || !strings.Contains(strings.Join(res.Limitations, " "), "not proof") {
		t.Fatalf("public-address boundary = %+v", res)
	}
	for addr, scope := range map[string]string{"192.168.1.2": "private (RFC 1918)", "fd00::1": "unique local (ULA)", "fe80::1": "link-local", "8.8.8.8": "global", "127.0.0.1": "loopback"} {
		if got := addressScope(netip.MustParseAddr(addr)); got != scope {
			t.Errorf("%s => %s", addr, got)
		}
	}
}

func TestNeighboursExplainCacheStatesAndInterfaces(t *testing.T) {
	stubLANDiagnostics(t)
	old := lanInterfaceAddrs
	t.Cleanup(func() { lanInterfaceAddrs = old })
	lanInterfaceAddrs = func(name string) []string {
		return map[string][]string{"eth0": {"192.168.1.10/24"}, "docker0": {"172.17.0.1/16"}}[name]
	}
	diagnosticRun = func(_ context.Context, _ time.Duration, cmd string, args ...string) (string, string, error) {
		if cmd != "ip" || !reflect.DeepEqual(args, []string{"neigh", "show"}) {
			t.Fatalf("argv = %s %v", cmd, args)
		}
		return "192.168.1.1 dev eth0 lladdr AA:BB:CC:DD:EE:01 REACHABLE\n192.168.1.20 dev eth0 lladdr aa:bb:cc:dd:ee:02 STALE\n192.168.1.99 dev eth0 FAILED\nfe80::1 dev eth0 lladdr aa:bb:cc:dd:ee:01 router STALE\n172.17.0.2 dev docker0 lladdr 02:42:ac:11:00:02 DELAY", "1ms", nil
	}
	res, err := New().Neighbours(t.Context())
	if err != nil || !res.OK || res.Verdict != ProbeOK {
		t.Fatalf("%+v %v", res, err)
	}
	cache := tableByID(res, "neighbours")
	if cache.Rows[0][3] != "aa:bb:cc:dd:ee:01" || cache.Rows[2][3] != "none" || !strings.Contains(cache.Rows[3][5], "router") || cache.Rows[2][5] != neighbourStates["FAILED"] {
		t.Fatalf("cache = %+v", cache.Rows)
	}
	ifaces := tableByID(res, "interfaces")
	if ifaces.Rows[0][0] != "docker0" || ifaces.Rows[0][1] != "Docker bridge" || ifaces.Rows[1][2] != "192.168.1.10/24" || ifaces.Rows[1][3] != "4" || ifaces.Rows[1][6] != "1" {
		t.Fatalf("interfaces = %+v", ifaces.Rows)
	}
	if !strings.Contains(factValue(res, "Not shown"), "not a scan") || !strings.Contains(factValue(res, "Method"), "no packets") {
		t.Fatalf("passive boundary = %+v", res.Facts)
	}
}

func TestWakeOnLANMeasuresVerificationWithinItsWindow(t *testing.T) {
	stubLANDiagnostics(t)
	stubNeighbourCache(t, "192.168.1.50 dev eno1 lladdr 02:11:22:33:44:55 STALE")
	oldWindow, oldInterval, oldCheck := wakeWindow, wakeInterval, wakeCheck
	t.Cleanup(func() { wakeWindow, wakeInterval, wakeCheck = oldWindow, oldInterval, oldCheck })
	wakeWindow, wakeInterval = 400*time.Millisecond, 20*time.Millisecond
	sent := false
	wakeSend = func(int, []byte) error { sent = true; return nil }
	checks := 0
	wakeCheck = func(_ context.Context, address string, port int) (bool, string, error) {
		if address != "192.168.1.50" || port != 22 {
			t.Fatalf("check %s %d", address, port)
		}
		checks++
		return sent && checks >= 3, "TCP 22 connected", nil
	}
	res, err := New().WakeOnLAN(t.Context(), "02:11:22:33:44:55", "eno1", "192.168.1.50", 22)
	if err != nil || !res.OK || res.Verdict != ProbeOK || stageStatus(res, "precheck") != StagePassed || stageStatus(res, "verify") != StagePassed {
		t.Fatalf("woken = %+v %v", res, err)
	}
	if _, ok := metricValue(res, "wake_seconds"); !ok || factValue(res, "Last cached address for this MAC") != "192.168.1.50 on eno1 (STALE)" {
		t.Fatalf("measurement = %+v", res)
	}

	sent = false
	wakeCheck = func(context.Context, string, int) (bool, string, error) { return false, "", nil }
	res, _ = New().WakeOnLAN(t.Context(), "02:11:22:33:44:55", "eno1", "192.168.1.50", 0)
	if !res.OK || res.Verdict != ProbeUnknown || stageStatus(res, "verify") != StageUnknown || !strings.Contains(res.Summary, "may still have woken") {
		t.Fatalf("silent = %+v", res)
	}

	wakeCheck = func(context.Context, string, int) (bool, string, error) { return true, "ICMP echo answered", nil }
	res, _ = New().WakeOnLAN(t.Context(), "02:11:22:33:44:55", "eno1", "192.168.1.50", 0)
	if stageStatus(res, "precheck") != StageWarning || stageStatus(res, "verify") != StageWarning || res.Verdict != ProbeUnknown || !strings.Contains(res.Summary, "already answering") {
		t.Fatalf("already awake = %+v", res)
	}
	if _, ok := metricValue(res, "wake_seconds"); ok {
		t.Fatal("an already-awake device recorded a wake time")
	}

	wakeCheck = func(context.Context, string, int) (bool, string, error) {
		return false, "", errors.New("ping could not run: operation not permitted")
	}
	res, _ = New().WakeOnLAN(t.Context(), "02:11:22:33:44:55", "eno1", "192.168.1.50", 0)
	if !res.OK || res.Verdict != ProbeUnknown || stageStatus(res, "verify") != StageUnknown || !strings.Contains(res.Summary, "could not run") {
		t.Fatalf("broken verification = %+v", res)
	}
	diagnosticHas = func(name string) bool { return name != "ping" }
	if _, err := New().WakeOnLAN(t.Context(), "02:11:22:33:44:55", "eno1", "192.168.1.50", 0); err == nil {
		t.Fatal("ICMP verification was accepted without ping")
	}
	diagnosticHas = func(string) bool { return true }

	stubNeighbourCache(t, "192.168.1.50 dev eno1 lladdr 02:11:22:33:44:55 STALE")
	res, _ = New().WakeOnLAN(t.Context(), "02:11:22:33:44:55", "eno1", "", 0)
	if !res.OK || res.Verdict != "" || !hasFinding(res, "verify-hint") || len(res.Stages) != 1 {
		t.Fatalf("unverified = %+v", res)
	}
	for _, bad := range []string{"0.0.0.0", "127.0.0.1", "ff02::1", "fe80::1%eno1", "host.example"} {
		if _, err := ValidWakeVerification(bad, 0); err == nil {
			t.Errorf("accepted verification address %q", bad)
		}
	}
}

func TestPacketSnapshotHandsOffToRetainedCaptures(t *testing.T) {
	stubLANDiagnostics(t)
	diagnosticRun = func(context.Context, time.Duration, string, ...string) (string, string, error) {
		return "tcpdump: verbose output suppressed\nlistening on eno1, link-type EN10MB\nIP 192.0.2.1.53 > 192.0.2.2.4321: UDP, length 40\nIP 192.0.2.2.4321 > 192.0.2.1.53: UDP, length 30\n2 packets captured\n2 packets received by filter\n0 packets dropped by kernel", "1s", nil
	}
	res, err := New().PacketSnapshot(t.Context(), "eno1", "udp")
	if err != nil || !res.OK || res.Verdict != ProbeOK || !hasLink(res, "/network/captures?interface=eno1&protocol=udp") {
		t.Fatalf("%+v %v", res, err)
	}
	if v, _ := metricValue(res, "packets"); v != 2 {
		t.Fatalf("packets = %v", v)
	}
}
