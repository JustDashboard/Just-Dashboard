package netx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestHostSupportUsesHostToolsAndProbesServiceManager(t *testing.T) {
	rec := record(t)
	rec.on("systemctl is-system-running", "offline").on("systemctl is-active systemd-resolved", "inactive")
	old := hostTool
	defer func() { hostTool = old }()
	hostTool = func(name string) bool { return name == "systemctl" || name == "ip" }
	view := testService(t).HostSupport(t.Context())
	if view.Persistence != "systemd manager unreachable" || len(view.Tools) < 15 || len(view.Notes) == 0 {
		t.Fatalf("view = %+v", view)
	}
	for _, tool := range view.Tools {
		if tool.Available != (tool.Tool == "systemctl" || tool.Tool == "ip") {
			t.Fatalf("wrong availability: %+v", tool)
		}
	}
	if !rec.ran("systemctl is-system-running") {
		t.Fatal("binary existence mistaken for running service manager")
	}
}

func TestHostSupportWithoutSystemdHasNoPersistencePromise(t *testing.T) {
	record(t)
	old := hostTool
	defer func() { hostTool = old }()
	hostTool = func(string) bool { return false }
	view := testService(t).HostSupport(t.Context())
	if view.Persistence != "unavailable" {
		t.Fatalf("%+v", view)
	}
}

// Probes ask the kernel and the process, not the package list: a present
// binary whose operation is refused reads as restricted, a module that is
// only available reads as supported-but-unloaded, and nothing writes.
func TestHostSupportProbesCapabilitiesBeyondBinaries(t *testing.T) {
	rec := record(t)
	rec.on("systemctl is-system-running", "running").
		on("systemctl is-active systemd-resolved", "active").
		fail("nft list tables", "Error: Operation not permitted (you must be root)").
		on("iptables -V", "iptables v1.8.10 (nf_tables)").
		on("modinfo -F filename sch_cake", "/lib/modules/6.14/kernel/net/sched/sch_cake.ko.zst").
		fail("modinfo -F filename wireguard", "modinfo: ERROR: Module wireguard not found.").
		on("findmnt -n -t bpf -o TARGET", "/sys/fs/bpf")
	oldTool, oldSocket, oldModules, oldProc := hostTool, packetSocket, sysModuleRoot, procSysRoot
	t.Cleanup(func() { hostTool, packetSocket, sysModuleRoot, procSysRoot = oldTool, oldSocket, oldModules, oldProc })
	hostTool = func(string) bool { return true }
	packetSocket = func() error { return unix.EPERM }
	sysModuleRoot = t.TempDir()
	procSysRoot = t.TempDir()
	for path, value := range map[string]string{
		"net/ipv6/conf/all/disable_ipv6": "0", "net/ipv6/conf/eth0/disable_ipv6": "0", "net/ipv6/conf/wg0/disable_ipv6": "1",
		"net/ipv4/ip_forward": "1", "net/ipv6/conf/all/forwarding": "0",
		"net/netfilter/nf_conntrack_count": "12", "net/netfilter/nf_conntrack_max": "262144",
	} {
		full := filepath.Join(procSysRoot, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	view := testService(t).HostSupport(t.Context())
	got := map[string]CapabilityProbe{}
	for _, p := range view.Probes {
		got[p.ID] = p
	}
	for id, want := range map[string]string{
		"packet_socket": ProbeRestricted, "nftables": ProbeRestricted, "iptables_backend": ProbeSupported,
		"ipv6": ProbeSupported, "forwarding": ProbeSupported, "wireguard": ProbeUnsupported, "sch_cake": ProbeSupported,
		"conntrack": ProbeSupported, "bpffs": ProbeSupported, "resolved": ProbeSupported,
	} {
		if got[id].Status != want {
			t.Errorf("%s = %+v, want %s", id, got[id], want)
		}
	}
	if !strings.Contains(got["ipv6"].Evidence, "disabled on wg0") || !strings.Contains(got["packet_socket"].Evidence, "CAP_NET_RAW") ||
		!strings.Contains(got["sch_cake"].Evidence, "not loaded") || !strings.Contains(got["conntrack"].Evidence, "12 tracked of 262144") {
		t.Fatalf("probe evidence = %+v", view.Probes)
	}
	for _, c := range rec.commands() {
		for _, word := range []string{" add ", " del", " set ", "modprobe", "insmod", "-w"} {
			if strings.Contains(" "+c+" ", word) {
				t.Fatalf("capability probe ran a mutating command: %s", c)
			}
		}
	}
	// A loaded module is read from sysfs without asking modinfo.
	if err := os.MkdirAll(filepath.Join(sysModuleRoot, "wireguard"), 0o755); err != nil {
		t.Fatal(err)
	}
	view = testService(t).HostSupport(t.Context())
	for _, p := range view.Probes {
		if p.ID == "wireguard" && (p.Status != ProbeSupported || p.Evidence != "loaded") {
			t.Fatalf("loaded module = %+v", p)
		}
	}
}

func TestHostSupportProbesWithoutBinariesAreUnknown(t *testing.T) {
	record(t)
	oldTool, oldSocket := hostTool, packetSocket
	t.Cleanup(func() { hostTool, packetSocket = oldTool, oldSocket })
	hostTool = func(string) bool { return false }
	packetSocket = func() error { return nil }
	view := testService(t).HostSupport(t.Context())
	for _, p := range view.Probes {
		switch p.ID {
		case "nftables":
			if p.Status != ProbeUnknown {
				t.Fatalf("a missing nft was reported as %+v", p)
			}
		case "packet_socket":
			if p.Status != ProbeSupported {
				t.Fatalf("packet socket = %+v", p)
			}
		case "iptables_backend", "bpffs", "resolved":
			t.Fatalf("probe ran without its binary: %+v", p)
		}
	}
}
