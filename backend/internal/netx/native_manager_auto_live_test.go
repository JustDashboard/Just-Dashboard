package netx

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// This fixture exercises actual DHCP/RA acquisition and fresh executable
// recovery. Timer admission is stubbed by nativeFixtureCommands; neither that
// admission nor native boot enablement is a timer-dispatch or reboot proof.
func TestNativeManagerAutomaticOwnerLive(t *testing.T) {
	liveRequired(t)
	if os.Getenv("JD_NATIVE_AUTO_WORKER") == "apply" {
		nativeAutomaticApplyDeath(t)
		return
	}
	if os.Getenv("JD_NATIVE_AUTO_NS") != "1" {
		nativeAutomaticOuter(t)
		return
	}
	if os.Geteuid() != 0 || os.Getpid() != 1 {
		t.Fatal("automatic owner fixture requires its private root PID namespace")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	nativeAutomaticRoot(t)
	nativeFixtureCommands(t)
	nmRoot := os.Getenv("JD_NATIVE_MANAGER_NM_ROOT")
	t.Setenv("PATH", filepath.Join(nmRoot, "usr/bin")+":"+os.Getenv("PATH"))
	t.Setenv("LD_LIBRARY_PATH", filepath.Join(nmRoot, "usr/lib/x86_64-linux-gnu"))
	owner := os.Getenv("JD_NATIVE_AUTO_CASE")
	if !slices.Contains([]string{"networkd", "NetworkManager", "netplan", "netplan-NetworkManager"}, owner) {
		t.Fatal("select a closed automatic owner case")
	}
	stopBus := nativeFixtureDaemon(t, "dbus-daemon", "--nofork", "--config-file=/run/dbus/fixture.conf")
	defer stopBus()
	must := func(tool string, args ...string) string {
		t.Helper()
		out, err := nativeFixtureExecute(ctx, nil, tool, args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	must("ip", "link", "set", "lo", "up")
	must("ip", "link", "add", "d0", "address", "02:00:00:00:08:02", "type", "veth", "peer", "name", "d1", "address", "02:00:00:00:08:01")
	defer func() { must("ip", "link", "del", "d0") }()
	for _, device := range []string{"d0", "d1"} {
		for _, knob := range []string{"accept_dad", "accept_ra", "autoconf"} {
			nativeFixtureWrite(t, "/proc/sys/net/ipv6/conf/"+device+"/"+knob, "0\n", 0o644)
		}
		must("ip", "link", "set", device, "up")
	}
	must("ip", "addr", "add", "198.18.8.1/24", "dev", "d1")
	must("ip", "-6", "addr", "replace", "fe80::1/64", "dev", "d1")
	nativeFixtureWrite(t, "/run/jd-auto/udhcpd.conf", "start 198.18.8.10\nend 198.18.8.10\ninterface d1\nmax_leases 1\nauto_time 1\nlease_file /run/jd-auto/leases\npidfile /run/jd-auto/dhcp.pid\noption subnet 255.255.255.0\noption router 198.18.8.1\noption dns 198.18.8.53\noption domain auto.test\noption lease 600\n", 0o600)
	stopDHCP := nativeFixtureDaemon(t, "busybox", "udhcpd", "-f", "-a", "0", "/run/jd-auto/udhcpd.conf")
	defer stopDHCP()
	stopRA := nativeAutomaticRA(t, ctx)
	defer stopRA()
	stopOwner := nativeAutomaticOwner(t, ctx, owner, nmRoot)
	defer stopOwner()
	s := nativeFixtureService()
	nativeAutomaticHelper(t, s)
	initial := nativeAutomaticIntent(owner, false)
	p := nativeAutomaticWait(t, ctx, s, owner, initial)
	if p.Renderer == "NetworkManager" {
		nativeAutomaticNMTypes(t, ctx, s)
		nativeAutomaticNMDomains(t, ctx, s)
	}
	nativeAutomaticAcquired(t, ctx, s, false)
	nativeAutomaticUnmanaged(t, ctx, p.Renderer)
	if owner == "netplan" && os.Getenv("JD_NATIVE_NETPLAN_AUTO_POLICY") == "explicit" {
		nativeAutomaticNetplanRARefusal(t, ctx, s, initial)
	}
	t.Logf("%s saved/loaded/applied automatic intent, actual DHCPv4 lease, SLAAC prefix, DHCP/RA default routes and active-owner DNS/domain acquisition verified", owner)

	manual := nativeFixtureIntent("198.18.8.20", "2001:db8:18::20")
	manual.IPv4.Domains, manual.IPv6.Domains = []string{"manual.test"}, []string{"manual.test"}
	nativeAutomaticStaticDefaults(&manual)
	apply := func(in NativeIntent) *changeJournal {
		t.Helper()
		p = nativeAutomaticWait(t, ctx, s, owner, *p.Intent)
		if _, err := s.EditNativeProfile(WithPendingConfirmation(ctx, 7), "d0", NativeEditRequest{Generation: p.Generation, Intent: in}, "127.0.0.1"); err != nil {
			t.Fatalf("%s automatic native apply: %v", owner, err)
		}
		j, err := readChange(s.paths.Dir)
		if err != nil || j.Phase != "awaiting_confirmation" {
			t.Fatalf("automatic pending journal: %+v %v", j, err)
		}
		p = nativeAutomaticWait(t, ctx, s, owner, in)
		return j
	}
	recover := func(j *changeJournal, want NativeIntent) {
		t.Helper()
		must(filepath.Join(s.paths.Dir, recoveryBinary), "--network-recover", s.paths.Dir, j.ID)
		p = nativeAutomaticWait(t, ctx, s, owner, want)
		nativeAutomaticCleanup(t, ctx, s, j.ID)
	}
	j := apply(manual)
	nativeAutomaticManual(t, ctx)
	recover(j, initial)
	nativeAutomaticAcquired(t, ctx, s, false)

	// Suppressing the client's acquired defaults is valid only while another
	// preferred route remains unchanged. This private peer route preserves the
	// production guard without substituting any client acquisition evidence.
	for _, family := range []string{"-4", "-6"} {
		must("ip", family, "route", "add", "default", "dev", "d1", "metric", "1")
	}
	suppressed := nativeAutomaticIntent(owner, true)
	j = apply(suppressed)
	nativeAutomaticAcquired(t, ctx, s, true)
	recover(j, initial)
	nativeAutomaticAcquired(t, ctx, s, false)
	for _, family := range []string{"-4", "-6"} {
		must("ip", family, "route", "del", "default", "dev", "d1", "metric", "1")
	}

	// The existing read-only worker exits after the durable confirmation, but
	// before saving a terminal cleanup outcome. Recovery must retain manual
	// intent and retry cleanup instead of reverting a confirmed decision.
	j = apply(manual)
	nativeAutomaticWorker(t, ctx, "cleanup-death", 94)
	durable, err := readChange(s.paths.Dir)
	if err != nil || durable.Phase != "confirmed" || durable.Cleanup == "complete" {
		t.Fatalf("automatic confirmed cleanup death lost its durable decision: %+v %v", durable, err)
	}
	recover(j, manual)
	nativeAutomaticManual(t, ctx)

	j = apply(initial)
	proof, err := s.VerifyReconnection(ctx, j.ID, 7, "automatic-session", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.ConfirmChange(ctx, j.ID, 7, "automatic-session", proof.Challenge, "127.0.0.1")
	if err != nil || status.Phase != "confirmed" || status.Cleanup != "complete" {
		t.Fatalf("automatic durable confirmation: %+v %v", status, err)
	}
	p = nativeAutomaticWait(t, ctx, s, owner, initial)
	nativeAutomaticAcquired(t, ctx, s, false)
	nativeAutomaticCleanup(t, ctx, s, j.ID)

	// This separate backend process reaches actual manual activation before
	// exiting. A new production helper must restore dynamic acquisition.
	nativeAutomaticWorker(t, ctx, "apply", 91)
	j, err = readChange(s.paths.Dir)
	if err != nil || j.Phase != "awaiting_confirmation" {
		t.Fatalf("automatic applying-process death: %+v %v", j, err)
	}
	recover(j, initial)
	nativeAutomaticAcquired(t, ctx, s, false)
	if owner == "NetworkManager" {
		j = apply(manual)
		nativeAutomaticForeignDeadline(t, ctx, s, j)
		recover(j, initial)
		nativeAutomaticAcquired(t, ctx, s, false)
	}
	t.Logf("%s actual auto/manual activation, automatic DNS/routes suppression, independent rollback, applying-backend death, durable confirmation and cleanup retry verified; timer/reboot unmeasured", owner)

	stopRA()
	stopDHCP()
	stopOwner()
	stopBus()
	for _, path := range []string{"/run/jd-auto/leases", "/run/jd-auto/dhcp.pid", "/run/jd-native-nm/pid"} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("owned DHCP lease/pid evidence remains: %s %v", path, err)
		}
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if pid, err := strconv.Atoi(entry.Name()); err == nil && pid != os.Getpid() {
			t.Fatalf("owned namespace retains a child process: %d", pid)
		}
	}
	t.Log("Owned RA raw socket closed and sender drained; DHCP/manager/bus children reaped; lease/pid files withdrawn; private PID namespace has no remaining child")
}

func nativeAutomaticNetplanRARefusal(t *testing.T, ctx context.Context, s *Service, initial NativeIntent) {
	t.Helper()
	p, err := s.readNativeProfile(ctx, "d0")
	if err != nil || p == nil || !p.View.Editable {
		t.Fatalf("Netplan RA refusal lacks editable authored ownership: %+v %v", p, err)
	}
	files := []nativeProfileFile{p.File, p.Generated}
	stages := make([][]string, len(files))
	for i, file := range files {
		stages[i], err = filepath.Glob(filepath.Join(filepath.Dir(file.Path), ".jd-native-*"))
		if err != nil {
			t.Fatal(err)
		}
	}
	unsupported := initial
	unsupported.IPv6.IgnoreAutoRoutes = true
	_, err = s.EditNativeProfile(WithPendingConfirmation(ctx, 7), "d0", NativeEditRequest{Generation: p.View.Generation, Intent: unsupported}, "127.0.0.1")
	var refusal *ReadOnlyError
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "cannot persist") {
		t.Fatalf("unrepresentable authored RA route policy was not refused: %v", err)
	}
	if j, err := readChange(s.paths.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused Netplan RA policy created recovery state: %+v %v", j, err)
	}
	for i, file := range files {
		current, err := nativeReadProfile(file.Path)
		if err != nil || current.Identity != file.Identity || !slices.Equal(current.Data, file.Data) {
			t.Fatalf("refused Netplan RA policy changed selected bytes/inode: %s %v", file.Path, err)
		}
		currentStages, err := filepath.Glob(filepath.Join(filepath.Dir(file.Path), ".jd-native-*"))
		if err != nil || !slices.Equal(currentStages, stages[i]) {
			t.Fatalf("refused Netplan RA policy changed private staging: %s %v", file.Path, err)
		}
	}
	nativeAutomaticWait(t, ctx, s, "netplan", initial)
	nativeAutomaticAcquired(t, ctx, s, false)
	t.Log("Unrepresentable authored RA route suppression refused before selected-file/staging/journal effects; actual automatic acquisition remains unchanged")
}

func nativeAutomaticOuter(t *testing.T) {
	t.Helper()
	policy := os.Getenv("JD_NATIVE_NETPLAN_AUTO_POLICY")
	if policy != "" && (policy != "explicit" || os.Getenv("JD_NATIVE_AUTO_CASE") != "netplan") {
		t.Fatal("explicit authored automatic policy requires the selected netplan/networkd case")
	}
	nmRoot := os.Getenv("JD_NATIVE_MANAGER_NM_ROOT")
	if nmRoot == "" {
		t.Fatal("set JD_NATIVE_MANAGER_NM_ROOT to the independently verified Debian NetworkManager/nmcli userland")
	}
	for _, tool := range []string{"busybox", "unshare", "ip", "dbus-daemon", "networkctl", "netplan"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("selected automatic acceptance lacks %s: %v", tool, err)
		}
	}
	artifact := t.TempDir()
	helper := filepath.Join(artifact, recoveryBinary)
	buildCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	build := exec.CommandContext(buildCtx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-p", "2", "-trimpath", "-o", helper, "./cmd/server")
	build.Dir = filepath.Join("..", "..")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOMAXPROCS=2")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("automatic standalone recovery helper build: %v %s", err, out)
	}
	helperData, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	check := exec.CommandContext(buildCtx, helper, "--network-native-check")
	capability, err := check.CombinedOutput()
	if err != nil || strings.TrimSpace(string(capability)) != nativeRecoveryToken {
		t.Fatalf("automatic standalone helper capability: %s %v", capability, err)
	}
	t.Logf("Actual standalone helper SHA256=%x capability=%s", sha256.Sum256(helperData), strings.TrimSpace(string(capability)))
	cases := []string{"networkd", "NetworkManager", "netplan", "netplan-NetworkManager"}
	if selected := os.Getenv("JD_NATIVE_AUTO_CASE"); selected != "" {
		if !slices.Contains(cases, selected) {
			t.Fatal("unsupported selected automatic owner")
		}
		cases = []string{selected}
	}
	for _, owner := range cases {
		t.Run(owner, func(t *testing.T) {
			ctx, stop := context.WithTimeout(context.Background(), 4*time.Minute)
			defer stop()
			unbounded := liveSudo("env", "GOMAXPROCS=2", "JD_NETNS_LIVE=1", "JD_NATIVE_AUTO_NS=1", "JD_NATIVE_AUTO_CASE="+owner, "JD_NATIVE_NETPLAN_AUTO_POLICY="+policy, "JD_NATIVE_MANAGER_HELPER="+helper, "JD_NATIVE_MANAGER_NM_ROOT="+nmRoot, "unshare", "--net", "--mount", "--pid", "--fork", "--kill-child=SIGKILL", "--mount-proc", "--propagation", "private", "--", os.Args[0], "-test.run=^TestNativeManagerAutomaticOwnerLive$", "-test.count=1", "-test.timeout=3m", "-test.v")
			cmd := exec.CommandContext(ctx, unbounded.Path, unbounded.Args[1:]...)
			var out nativeBoundedOutput
			cmd.Stdout, cmd.Stderr = &out, &out
			if err := cmd.Run(); err != nil {
				t.Fatalf("isolated automatic %s acceptance: %v\n%s", owner, err, out.String())
			}
			if out.overflow {
				t.Fatal("automatic fixture output exceeded its bound")
			}
			t.Logf("automatic owner acceptance:\n%s", out.String())
		})
	}
}

func nativeAutomaticRoot(t *testing.T) {
	t.Helper()
	passwd, err := os.ReadFile("/etc/passwd")
	if err != nil {
		t.Fatal(err)
	}
	group, err := os.ReadFile("/etc/group")
	if err != nil {
		t.Fatal(err)
	}
	packages, err := os.ReadFile("/var/lib/dpkg/status")
	if err != nil || len(packages) > 8<<20 {
		t.Fatal("automatic fixture package inventory is unreadable")
	}
	for _, path := range []string{"/etc", "/run", "/var/lib"} {
		if err := syscall.Mount("tmpfs", path, "tmpfs", 0, "mode=0755,size=64m"); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mount("sysfs", "/sys", "sysfs", 0, ""); err != nil {
		t.Fatal(err)
	}
	nativeFixtureWrite(t, "/etc/passwd", string(passwd), 0o644)
	nativeFixtureWrite(t, "/etc/group", string(group), 0o644)
	nativeFixtureWrite(t, "/var/lib/dpkg/status", string(packages), 0o644)
	nativeFixtureWrite(t, "/etc/machine-id", "4c491fa513b8440ca2c11cc2d3bfaca1\n", 0o444)
	account, err := user.Lookup("systemd-network")
	if err != nil {
		t.Fatal(err)
	}
	uid, uidErr := strconv.Atoi(account.Uid)
	gid, gidErr := strconv.Atoi(account.Gid)
	if uidErr != nil || gidErr != nil {
		t.Fatal("automatic networkd identity is unreadable")
	}
	if err := os.MkdirAll("/run/systemd/netif", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown("/run/systemd/netif", uid, gid); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("/run/systemd/system", 0o755); err != nil {
		t.Fatal(err)
	}
	nativeFixtureWrite(t, "/run/dbus/fixture.conf", `<busconfig><type>system</type><listen>unix:path=/run/dbus/system_bus_socket</listen><auth>EXTERNAL</auth><policy context="default"><allow user="*"/><allow own="*"/><allow send_destination="*"/><allow receive_sender="*"/></policy></busconfig>`, 0o600)
}

func nativeAutomaticIntent(owner string, suppressed bool) NativeIntent {
	in := NativeIntent{IPv4: nativeEmptyFamily("auto"), IPv6: nativeEmptyFamily("slaac")}
	if owner == "NetworkManager" || owner == "netplan-NetworkManager" {
		in.IPv6.Method = "auto"
	}
	if suppressed {
		in.IPv4.IgnoreAutoDNS, in.IPv6.IgnoreAutoDNS = true, true
		in.IPv4.IgnoreAutoRoutes, in.IPv6.IgnoreAutoRoutes = true, true
		in.IPv4.DNS, in.IPv6.DNS = []string{"127.0.0.2"}, []string{"::2"}
		in.IPv4.Domains, in.IPv6.Domains = []string{"manual.test"}, []string{"manual.test"}
		if owner == "netplan" && os.Getenv("JD_NATIVE_NETPLAN_AUTO_POLICY") == "explicit" {
			in.IPv6.IgnoreAutoRoutes = false
		}
	}
	return in
}

func nativeAutomaticStaticDefaults(in *NativeIntent) {
	// Dynamic acquisition has actual defaults; disabling it must keep usable
	// replacements so the production connectivity guard remains in force.
	in.IPv4.Routes = []NativeRoute{{Destination: "0.0.0.0/0", Gateway: "198.18.8.1", Metric: 1024, Table: 254}}
	in.IPv6.Routes = []NativeRoute{{Destination: "::/0", Gateway: "fe80::1", Metric: 1024, Table: 254}}
}

func nativeAutomaticOwner(t *testing.T, ctx context.Context, owner, nmRoot string) func() {
	t.Helper()
	must := func(tool string, args ...string) string {
		t.Helper()
		out, err := nativeFixtureExecute(ctx, nil, tool, args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	useNM := owner == "NetworkManager" || owner == "netplan-NetworkManager"
	manager := "io.systemd.Network"
	if useNM {
		manager = nmService
	}
	if !useNM {
		nativeFixtureWrite(t, "/etc/systemd/network/10-peer.network", "[Match]\nName=d1\n[Link]\nUnmanaged=yes\n", 0o644)
	}
	index, err := os.ReadFile("/sys/class/net/d0/ifindex")
	if err != nil {
		t.Fatal(err)
	}
	nativeFixtureWrite(t, "/run/udev/data/n"+strings.TrimSpace(string(index)), "I:1\nE:INTERFACE=d0\nE:ID_NET_MANAGED_BY="+manager+"\n", 0o644)
	peerIndex, err := os.ReadFile("/sys/class/net/d1/ifindex")
	if err != nil {
		t.Fatal(err)
	}
	nativeFixtureWrite(t, "/run/udev/data/n"+strings.TrimSpace(string(peerIndex)), "I:1\nE:INTERFACE=d1\n", 0o644)
	if useNM {
		nativeFixtureWrite(t, "/etc/NetworkManager/NetworkManager.conf", "[main]\nplugins=keyfile\ndns=none\nrc-manager=unmanaged\ndhcp=internal\n[device-fixture]\nmatch-device=interface-name:d0\nmanaged=true\n[device-peer]\nmatch-device=interface-name:d1\nmanaged=false\n[connectivity]\nenabled=false\n", 0o600)
	}
	activation := "d0"
	profilePath := "/etc/NetworkManager/system-connections/d0.nmconnection"
	if strings.HasPrefix(owner, "netplan") {
		renderer, dhcp6 := "networkd", "false"
		metadata := ""
		if useNM {
			renderer, dhcp6 = "NetworkManager", "true"
			activation = "57bc1142-0de1-4ab4-a156-4d2cba203561"
			metadata = "      networkmanager:\n        uuid: '" + activation + "'\n        name: d0\n"
			profilePath = "/run/NetworkManager/system-connections/netplan-d0.nmconnection"
		}
		if !useNM && os.Getenv("JD_NATIVE_NETPLAN_AUTO_POLICY") == "explicit" {
			metadata = "      dhcp4-overrides:\n        use-domains: true\n        use-mtu: false\n      ra-overrides:\n        use-domains: true\n"
		}
		nativeFixtureWrite(t, "/etc/netplan/20-auto.yaml", "network:\n  version: 2\n  renderer: "+renderer+"\n  ethernets:\n    d0:\n      dhcp4: true\n      dhcp6: "+dhcp6+"\n      accept-ra: true\n"+metadata, 0o600)
		must("netplan", "generate")
	} else if useNM {
		profile := must("nmcli", "--offline", "connection", "add", "type", "ethernet", "ifname", "d0", "con-name", "d0", "ipv4.method", "auto", "ipv6.method", "auto")
		nativeFixtureWrite(t, profilePath, profile, 0o600)
	} else {
		profile, err := renderNativeNetworkd([]byte("[Match]\nName=d0\n[Network]\n[DHCPv4]\nUseDomains=yes\n[IPv6AcceptRA]\nUseDomains=yes\n"), nativeAutomaticIntent(owner, false))
		if err != nil {
			t.Fatal(err)
		}
		nativeFixtureWrite(t, "/etc/systemd/network/20-d0.network", string(profile), 0o644)
	}
	if useNM {
		nativeFixtureOwnerUnit(t, "NetworkManager.service")
		stop := nativeFixtureDaemon(t, filepath.Join(nmRoot, "usr/sbin/NetworkManager"), "--debug", "--log-level=INFO", "--plugins=keyfile", "--config=/etc/NetworkManager/NetworkManager.conf", "--system-config-dir=/run/jd-native-nm/conf.d", "--intern-config=/run/jd-native-nm/intern.conf", "--state-file=/run/jd-native-nm/state", "--pid-file=/run/jd-native-nm/pid")
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := nativeFixtureExecute(ctx, nil, "nmcli", "connection", "load", profilePath); err == nil {
				must("nmcli", "connection", "up", activation)
				return stop
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("private automatic NetworkManager did not load its exact profile")
	} else {
		nativeFixtureOwnerUnit(t, "systemd-networkd.service")
		return nativeFixtureDaemon(t, "/usr/lib/systemd/systemd-networkd")
	}
	return nil
}

func nativeAutomaticHelper(t *testing.T, s *Service) {
	t.Helper()
	if err := os.MkdirAll(s.paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	helper := os.Getenv("JD_NATIVE_MANAGER_HELPER")
	info, err := os.Lstat(helper)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<20 {
		t.Fatal("automatic fixture standalone helper is unreadable")
	}
	if err := os.Chown(helper, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(helper, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.paths.Dir, recoveryBinary)
	if err := os.WriteFile(path, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mount(helper, path, "", syscall.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Unmount(path, 0) })
	if err := syscall.Mount("", path, "", syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY, ""); err != nil {
		t.Fatal(err)
	}
	s.recoveryInstalled = true
}

func nativeAutomaticWait(t *testing.T, ctx context.Context, s *Service, owner string, want NativeIntent) *NativeProfileView {
	t.Helper()
	expectedOwner := owner
	if strings.HasPrefix(owner, "netplan") {
		expectedOwner = "netplan"
	}
	deadline := time.Now().Add(25 * time.Second)
	var p *NativeProfileView
	var err error
	for time.Now().Before(deadline) && ctx.Err() == nil {
		p, err = s.NativeProfile(ctx, "d0")
		if err == nil && p != nil && p.Editable && p.Owner == expectedOwner && p.Intent != nil && nativeIntentEqual(*p.Intent, want) && p.Configured.Status == "matching" && p.Runtime.Status == "matching" {
			return p
		}
		time.Sleep(200 * time.Millisecond)
	}
	for _, command := range [][]string{{"ip", "-j", "-d", "link", "show"}, {"ip", "-j", "addr", "show", "dev", "d0"}, {"ip", "-j", "-4", "route", "show", "table", "all", "dev", "d0"}, {"ip", "-j", "-6", "route", "show", "table", "all", "dev", "d0"}, {"networkctl", "--no-pager", "--json=short", "status", "d0"}} {
		out, evidenceErr := nativeFixtureExecute(ctx, nil, command[0], command[1:]...)
		t.Logf("automatic owner refusal evidence %v: %s %v", command, out, evidenceErr)
	}
	if p != nil && p.Renderer == "NetworkManager" {
		nativeAutomaticNMTypes(t, ctx, s)
	}
	t.Fatalf("automatic %s saved/loaded/applied/runtime agreement was not editable: %+v intent=%+v wanted=%+v err=%v", owner, p, func() any {
		if p != nil {
			return p.Intent
		}
		return nil
	}(), want, err)
	return nil
}

func nativeAutomaticNMTypes(t *testing.T, ctx context.Context, s *Service) {
	t.Helper()
	p, err := s.readNativeProfile(ctx, "d0")
	if err != nil || p == nil || !nativeBusOwner.MatchString(p.OwnerBus) || p.DeviceObject == "" || p.ConnectionObject == "" {
		t.Fatal("automatic loaded settings type inventory has no pinned selected owner")
	}
	ctx = nativePinnedBus(ctx, p.TransportGUID)
	unsaved, unsavedErr := nativeBusProperty[bool](ctx, p.OwnerBus, p.ConnectionObject, nmService+".Settings.Connection", "Unsaved", "b")
	t.Logf("Native selected connection Unsaved=%t read_error=%v", unsaved, unsavedErr)
	for _, method := range []string{"GetSettings", "GetAppliedConnection"} {
		object, iface := p.ConnectionObject, nmService+".Settings.Connection"
		args := []string{method}
		if method == "GetAppliedConnection" {
			object, iface = p.DeviceObject, nmService+".Device"
			args = append(args, "u", "0")
		}
		result, err := nativeBus(ctx, p.OwnerBus, object, iface, "call", args...)
		var settings map[string]map[string]nativeVariant
		if err != nil || len(result.Data) == 0 || json.Unmarshal(result.Data[0], &settings) != nil {
			t.Fatalf("automatic %s settings type inventory: %v", method, err)
		}
		fields := []string{}
		for section, values := range settings {
			for property, value := range values {
				fields = append(fields, section+"."+property+":"+value.Type)
			}
		}
		slices.Sort(fields)
		t.Logf("Native %s D-Bus types (names only, no values): %s", method, strings.Join(fields, ", "))
		intent, projectionErr := nativeNMSettingsIntent(settings, p)
		t.Logf("Native %s controlled supported-intent projection: %+v read_error=%v", method, intent, projectionErr)
	}
}

func nativeAutomaticUnmanaged(t *testing.T, ctx context.Context, renderer string) {
	t.Helper()
	if renderer == "NetworkManager" {
		r, err := nativeBus(ctx, nmService, nmObject, nmService, "call", "GetDeviceByIpIface", "s", "d1")
		if err != nil {
			t.Fatal(err)
		}
		object, err := nativeBusValue[string](r, "o")
		if err != nil {
			t.Fatal(err)
		}
		managed, err := nativeBusProperty[bool](ctx, nmService, object, nmService+".Device", "Managed", "b")
		if err != nil || managed {
			t.Fatalf("automatic server peer is not natively unmanaged: %t %v", managed, err)
		}
		return
	}
	out, err := nativeFixtureExecute(ctx, nil, "networkctl", "--no-pager", "--json=short", "status", "d1")
	var peer struct {
		Name  string `json:"Name"`
		State string `json:"AdministrativeState"`
	}
	if err != nil || json.Unmarshal([]byte(out), &peer) != nil || peer.Name != "d1" || peer.State != "unmanaged" {
		t.Fatalf("automatic server peer is not natively unmanaged: %s %v", out, err)
	}
}

func nativeAutomaticNMDomains(t *testing.T, ctx context.Context, s *Service) {
	t.Helper()
	p, err := s.readNativeProfile(ctx, "d0")
	if err != nil || p == nil || !p.View.Editable {
		t.Fatal("automatic domain provenance has no verified native owner")
	}
	for _, config := range []string{"Ip4Config", "Ip6Config", "Dhcp4Config", "Dhcp6Config"} {
		object, err := nativeBusProperty[string](ctx, nmService, p.DeviceObject, nmService+".Device", config, "o")
		if err != nil {
			t.Fatalf("automatic native %s object: %v", config, err)
		}
		if object == "/" {
			t.Logf("Native %s has no active object", config)
			continue
		}
		iface := nmService + "." + config
		if strings.HasPrefix(config, "Ip") {
			iface = nmService + ".IP" + strings.TrimPrefix(config, "Ip")
		}
		if strings.HasPrefix(config, "Dhcp") {
			iface = nmService + ".DHCP" + strings.TrimPrefix(config, "Dhcp")
		}
		if strings.HasPrefix(config, "Ip") {
			for _, property := range []string{"Domains", "Searches"} {
				values, err := nativeBusProperty[[]string](ctx, nmService, object, iface, property, "as")
				if err != nil {
					t.Fatalf("automatic native %s.%s: %v", config, property, err)
				}
				t.Logf("Native %s.%s: %v", config, property, values)
			}
			continue
		}
		// Only the controlled fixture's domain/provider option values are
		// retained; other native DHCP options are represented by name/type.
		options, err := nativeBusProperty[map[string]nativeVariant](ctx, nmService, object, iface, "Options", "a{sv}")
		if err != nil {
			t.Fatalf("automatic native %s.Options: %v", config, err)
		}
		fields := []string{}
		for key, value := range options {
			field := key + ":" + value.Type
			if strings.Contains(key, "domain") || strings.Contains(key, "search") || key == "dhcp_server_identifier" {
				var text string
				if value.Type == "s" && json.Unmarshal(value.Data, &text) == nil && len(text) <= 256 {
					field += "=" + text
				}
			}
			fields = append(fields, field)
		}
		slices.Sort(fields)
		t.Logf("Native %s.Options controlled domain/provider provenance: %s", config, strings.Join(fields, ", "))
	}
}

func nativeAutomaticAcquired(t *testing.T, ctx context.Context, s *Service, suppressed bool) {
	t.Helper()
	p, err := s.readNativeProfile(ctx, "d0")
	if err != nil || !p.View.Editable {
		t.Fatalf("automatic native private profile: %+v %v", p, err)
	}
	out, err := nativeFixtureExecute(ctx, nil, "ip", "-j", "addr", "show", "dev", "d0")
	var addresses []ipAddr
	if err != nil || json.Unmarshal([]byte(out), &addresses) != nil || len(addresses) != 1 {
		t.Fatalf("automatic kernel addresses: %s %v", out, err)
	}
	v4, v6 := false, false
	for _, a := range addresses[0].AddrInfo {
		if a.Scope != "global" || !a.Dynamic {
			continue
		}
		v4 = v4 || a.Family == "inet" && a.Local == "198.18.8.10" && a.PrefixLen == 24
		addr, parseErr := netip.ParseAddr(a.Local)
		v6 = v6 || parseErr == nil && addr.Is6() && a.PrefixLen == 64 && netip.MustParsePrefix("2001:db8:18::/64").Contains(addr)
	}
	if !v4 || !v6 {
		t.Fatalf("real DHCP/RA global addresses missing: %s", out)
	}
	for index, flag := range []string{"-4", "-6"} {
		out, err := nativeFixtureExecute(ctx, nil, "ip", "-j", flag, "route", "show", "table", "all", "dev", "d0")
		var routes []ipRoute
		if err != nil || json.Unmarshal([]byte(out), &routes) != nil {
			t.Fatalf("automatic routes: %s %v", out, err)
		}
		defaultRoute := false
		routeSuppressed := suppressed && (index == 0 || p.View.Intent.IPv6.IgnoreAutoRoutes)
		for _, r := range routes {
			if routeSuppressed && (r.Protocol == "dhcp" || r.Protocol == "ra") {
				t.Fatalf("ignore automatic routes retained DHCP/RA route: %+v", r)
			}
			gateway, protocol := "198.18.8.1", "dhcp"
			if index == 1 {
				gateway, protocol = "fe80::1", "ra"
			}
			defaultRoute = defaultRoute || r.Dst == "default" && r.Gateway == gateway && r.Protocol == protocol
		}
		if !routeSuppressed && !defaultRoute {
			t.Fatalf("actual automatic %s default route missing: %s", flag, out)
		}
		var dns, domains []string
		if p.View.Renderer == "NetworkManager" {
			dns, domains, err = nativeNMDNS(ctx, p, index)
		} else {
			dns, domains, err = nativeNetworkdDNS(ctx, p, *p.View.Intent)
		}
		if err != nil {
			t.Fatal(err)
		}
		expected := "198.18.8.53"
		if index == 1 {
			expected = "2001:db8:18::53"
		}
		if suppressed {
			desired := "127.0.0.2"
			if index == 1 {
				desired = "::2"
			}
			familyDNS := []string{}
			for _, value := range dns {
				addr, err := netip.ParseAddr(value)
				if err != nil {
					t.Fatal("automatic suppression has invalid native DNS evidence")
				}
				if addr.Is6() == (index == 1) {
					familyDNS = append(familyDNS, value)
				}
			}
			if !slices.Equal(familyDNS, []string{desired}) || !slices.Contains(domains, "manual.test") {
				t.Fatalf("automatic DNS suppression does not retain only configured DNS/domain: %v %v", familyDNS, domains)
			}
			if p.View.Renderer == "networkd" && !slices.Contains(domains, "auto.test") {
				t.Fatalf("automatic DNS suppression lost the independent retained yes-domain policy: %v", domains)
			}
		} else if !slices.Contains(dns, expected) || !slices.Contains(domains, "auto.test") {
			t.Fatalf("actual DHCP/RDNSS native-owner DNS/domain missing: %v %v", dns, domains)
		}
	}
	if info, err := os.Stat("/run/jd-auto/leases"); err != nil || info.Size() == 0 {
		t.Fatalf("actual DHCP lease file is absent or empty: %v", err)
	}
}

func nativeAutomaticManual(t *testing.T, ctx context.Context) {
	t.Helper()
	for _, flag := range []string{"-4", "-6"} {
		out, err := nativeFixtureExecute(ctx, nil, "ip", "-j", flag, "route", "show", "table", "all", "dev", "d0")
		var routes []ipRoute
		if err != nil || json.Unmarshal([]byte(out), &routes) != nil {
			t.Fatal("manual kernel route evidence is unreadable")
		}
		if slices.ContainsFunc(routes, func(r ipRoute) bool { return r.Protocol == "dhcp" || r.Protocol == "ra" }) {
			t.Fatalf("manual activation retained automatic routes: %s", out)
		}
	}
}

func nativeAutomaticCleanup(t *testing.T, ctx context.Context, s *Service, id string) {
	t.Helper()
	j, err := readChange(s.paths.Dir)
	if err != nil || j.ID != id || j.Cleanup != "complete" {
		t.Fatalf("automatic terminal cleanup is incomplete: %+v %v", j, err)
	}
	u, err := nativeJournalUndo(j)
	if err != nil {
		t.Fatal(err)
	}
	if u.Renderer == "NetworkManager" {
		checkpoints, err := nativeBusProperty[[]string](ctx, nmService, nmObject, nmService, "Checkpoints", "ao")
		if err != nil || len(checkpoints) != 0 {
			t.Fatalf("automatic owned native checkpoint remains after terminal cleanup: %v %v", checkpoints, err)
		}
	}
	for _, f := range u.Files {
		if f.Cleanup != "complete" {
			t.Fatalf("automatic stage cleanup was not retained: %+v", f)
		}
		for _, path := range []string{f.CandidatePath, f.RollbackPath, f.CandidatePath + "-cleanup", f.RollbackPath + "-cleanup"} {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("automatic owned terminal stage remains: %s %v", path, err)
			}
		}
	}
}

func nativeAutomaticWorker(t *testing.T, ctx context.Context, mode string, code int) {
	t.Helper()
	test, variable := "TestNativeManagerOwnerLive", "JD_NATIVE_MANAGER_WORKER="+mode
	if mode == "apply" {
		test, variable = "TestNativeManagerAutomaticOwnerLive", "JD_NATIVE_AUTO_WORKER=apply"
	}
	worker := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+test+"$", "-test.count=1", "-test.timeout=1m")
	worker.Env = append(os.Environ(), variable)
	out, err := worker.CombinedOutput()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != code {
		t.Fatalf("automatic %s did not reach its real death window: %v %s", mode, err, out)
	}
}

func nativeAutomaticApplyDeath(t *testing.T) {
	t.Helper()
	nativeFixtureCommands(t)
	s := nativeFixtureService()
	s.recoveryInstalled = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p, err := s.NativeProfile(ctx, "d0")
	if err != nil || p == nil || !p.Editable {
		t.Fatalf("automatic applying worker lacks a verified owner: %+v %v", p, err)
	}
	write := writeChangeJournal
	writeChangeJournal = func(path string, data []byte, mode os.FileMode) error {
		if err := write(path, data, mode); err != nil {
			return err
		}
		if strings.Contains(string(data), `"phase": "awaiting_confirmation"`) {
			os.Exit(91)
		}
		return nil
	}
	manual := nativeFixtureIntent("198.18.8.4", "2001:db8:18::4")
	nativeAutomaticStaticDefaults(&manual)
	_, err = s.EditNativeProfile(WithPendingConfirmation(ctx, 7), "d0", NativeEditRequest{Generation: p.Generation, Intent: manual}, "127.0.0.1")
	t.Fatalf("automatic applying worker did not reach actual activation/death: %v", err)
}

func nativeAutomaticForeignDeadline(t *testing.T, ctx context.Context, s *Service, j *changeJournal) {
	t.Helper()
	u, err := nativeJournalUndo(j)
	if err != nil || !nativeUsesCheckpoint(u) || len(u.Files) != 1 || u.CheckpointState != "armed" {
		t.Fatalf("automatic foreign-file deadline lacks an exact owned checkpoint: %+v %v", u, err)
	}
	snapshot := func(path string) nativeProfileFile {
		t.Helper()
		file, err := nativeReadProfile(path)
		if err != nil {
			t.Fatalf("automatic foreign-file snapshot %s: %v", path, err)
		}
		return *file
	}
	unchanged := func(file nativeProfileFile) {
		t.Helper()
		current := snapshot(file.Path)
		if current.Identity != file.Identity || !slices.Equal(current.Data, file.Data) {
			t.Fatalf("owned deadline changed exact bytes/inode: %s", file.Path)
		}
	}
	selected := snapshot(u.Files[0].Before.Path)
	if selected.Identity != u.Files[0].Candidate.Identity || !slices.Equal(selected.Data, u.Files[0].Candidate.Data) {
		t.Fatal("automatic foreign-file candidate differs before fixture interference")
	}
	stages := []nativeProfileFile{snapshot(u.Files[0].CandidatePath), snapshot(u.Files[0].RollbackPath)}
	hold := selected.Path + ".jd-auto-owned-candidate"
	if _, err := os.Lstat(hold); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("automatic candidate holding path already exists")
	}
	pinned := nativePinnedBus(ctx, u.TransportGUID)
	if _, err := nativeBus(pinned, u.OwnerBus, nmObject, nmService, "call", "CheckpointAdjustRollbackTimeout", "ou", u.Checkpoint, "3"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	if err := os.Rename(selected.Path, hold); err != nil {
		t.Fatal(err)
	}
	foreignData := append(slices.Clone(selected.Data), []byte("\n# jd-auto exact foreign writer bytes\n")...)
	nativeFixtureWrite(t, selected.Path, string(foreignData), 0o600)
	foreign := snapshot(selected.Path)
	if foreign.Identity == selected.Identity {
		t.Fatal("automatic foreign writer did not get an independent inode")
	}
	// Holding the exact inode lets fixture cleanup restore it without
	// recreating a profile or letting production recovery overwrite a writer.
	defer func() {
		held, heldErr := nativeReadProfile(hold)
		current, currentErr := nativeReadProfile(selected.Path)
		if heldErr != nil || currentErr != nil || held.Identity != selected.Identity || !slices.Equal(held.Data, selected.Data) || current.Identity != foreign.Identity || !slices.Equal(current.Data, foreign.Data) {
			t.Error("fixture cleanup preserves unexpectedly changed selected or held candidate file")
			return
		}
		if err := os.Rename(hold, selected.Path); err != nil {
			t.Error(err)
		}
	}()
	output, helperErr := nativeFixtureExecute(ctx, nil, filepath.Join(s.paths.Dir, recoveryBinary), "--network-recover", s.paths.Dir, j.ID)
	if helperErr == nil {
		t.Fatal("fresh helper accepted foreign selected profile ownership")
	}
	retained, err := readChange(s.paths.Dir)
	if err != nil || retained.Phase != "degraded" || retained.Cleanup == "complete" {
		t.Fatalf("foreign-file refusal lost its manual-review journal: %+v %v", retained, err)
	}
	undo, err := nativeJournalUndo(retained)
	if err != nil || undo.CheckpointState != "released" {
		t.Fatalf("foreign-file refusal retains an automatic native rollback deadline: %+v %v", undo, err)
	}
	checkpoints, err := nativeBusProperty[[]string](pinned, u.OwnerBus, nmObject, nmService, "Checkpoints", "ao")
	if err != nil || len(checkpoints) != 0 {
		t.Fatalf("owned checkpoint remains after foreign-file refusal: %v %v", checkpoints, err)
	}
	if remaining := time.Until(deadline); remaining > 0 {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(remaining):
		}
	}
	unchanged(foreign)
	for _, stage := range stages {
		unchanged(stage)
	}
	currentJournal, err := readChange(s.paths.Dir)
	if err != nil || currentJournal.ID != j.ID || currentJournal.Phase != "degraded" || currentJournal.Cleanup == "complete" {
		t.Fatalf("automatic foreign-file manual-review journal changed past deadline: %+v %v", currentJournal, err)
	}
	t.Logf("Fresh helper refused foreign selected bytes/inode, released the exact owned checkpoint, retained phase=%s cleanup=%s errors=%v; foreign file and both stages unchanged past actual 3-second rollback deadline. Fixture cleanup alone restores captured candidate inode. Helper evidence: %s", retained.Phase, retained.Cleanup, retained.RecoveryErrors, output)
}

func nativeAutomaticRA(t *testing.T, ctx context.Context) func() {
	t.Helper()
	device, err := net.InterfaceByName("d1")
	if err != nil {
		t.Fatal(err)
	}
	fd, err := syscall.Socket(syscall.AF_INET6, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.IPPROTO_ICMPV6)
	if err != nil {
		t.Fatal(err)
	}
	stopCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	stop := func() {
		cancel()
		if done == nil {
			return
		}
		if err := <-done; err != nil {
			t.Error(err)
		}
		done = nil
		if err := syscall.Close(fd); err != nil {
			t.Error(err)
		}
		var info syscall.Stat_t
		if err := syscall.Fstat(fd, &info); !errors.Is(err, syscall.EBADF) {
			t.Error("owned RA file descriptor remains open")
		}
	}
	for _, option := range []int{syscall.IPV6_UNICAST_HOPS, syscall.IPV6_MULTICAST_HOPS} {
		if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_IPV6, option, 255); err != nil {
			_ = syscall.Close(fd)
			t.Fatal(err)
		}
	}
	if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_IPV6, syscall.IPV6_MULTICAST_IF, device.Index); err != nil {
		_ = syscall.Close(fd)
		t.Fatal(err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrInet6{Addr: netip.MustParseAddr("fe80::1").As16(), ZoneId: uint32(device.Index)}); err != nil {
		_ = syscall.Close(fd)
		t.Fatal(err)
	}
	// RFC 4861 RA/prefix and RFC 8106 RDNSS/DNSSL, on this private link only.
	// Linux supplies the ICMPv6 checksum for its raw ICMPv6 protocol socket.
	packet := make([]byte, 16+8+32+24+24)
	packet[0], packet[4] = 134, 64
	binary.BigEndian.PutUint16(packet[6:8], 180)
	packet[16], packet[17] = 1, 1
	copy(packet[18:24], device.HardwareAddr)
	packet[24], packet[25], packet[26], packet[27] = 3, 4, 64, 0xc0
	binary.BigEndian.PutUint32(packet[28:32], 180)
	binary.BigEndian.PutUint32(packet[32:36], 120)
	prefix := netip.MustParseAddr("2001:db8:18::").As16()
	copy(packet[40:56], prefix[:])
	packet[56], packet[57] = 25, 3
	binary.BigEndian.PutUint32(packet[60:64], 180)
	server := netip.MustParseAddr("2001:db8:18::53").As16()
	copy(packet[64:80], server[:])
	packet[80], packet[81] = 31, 3
	binary.BigEndian.PutUint32(packet[84:88], 180)
	copy(packet[88:], []byte{4, 'a', 'u', 't', 'o', 4, 't', 'e', 's', 't', 0})
	destination := &syscall.SockaddrInet6{Addr: netip.MustParseAddr("ff02::1").As16(), ZoneId: uint32(device.Index)}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			if err := syscall.Sendto(fd, packet, 0, destination); err != nil {
				done <- fmt.Errorf("owned RA send: %w", err)
				return
			}
			select {
			case <-stopCtx.Done():
				done <- nil
				return
			case <-ticker.C:
			}
		}
	}()
	t.Cleanup(stop)
	return stop
}
