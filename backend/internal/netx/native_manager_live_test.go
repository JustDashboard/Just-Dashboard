package netx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func nativeFixtureExecute(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, tool, args...)
	var out nativeBoundedOutput
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.Stdin = bytes.NewReader(input)
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("%s %s: %w: %s", tool, strings.Join(args, " "), err, out.String())
	}
	if out.overflow {
		return "", errors.New("native fixture output exceeded its bound")
	}
	return out.String(), nil
}

func nativeFixtureCommands(t *testing.T) {
	t.Helper()
	previousExecute, previousRun, previousHas := nativeExecute, run, has
	nativeExecute = nativeFixtureExecute
	has = func(tool string) bool { _, err := exec.LookPath(tool); return err == nil }
	run = func(ctx context.Context, tool string, args ...string) (string, error) {
		// This fixture measures native-owner data and independent executable
		// recovery. Real host timer dispatch has its separate systemd fixture.
		if tool == "systemd-run" || tool == "systemctl" && (args[0] == "daemon-reload" || args[0] == "enable") {
			return "fixture timer admission", nil
		}
		return nativeFixtureExecute(ctx, nil, tool, args...)
	}
	t.Cleanup(func() { nativeExecute, run, has = previousExecute, previousRun, previousHas })
}

func nativeFixtureWrite(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
}

func nativeFixtureOwnerUnit(t *testing.T, name string) {
	t.Helper()
	path := "/etc/systemd/system/" + name
	nativeFixtureWrite(t, path, "[Unit]\nDescription=Disposable native-owner acceptance fixture\n[Service]\nExecStart=/bin/true\n[Install]\nWantedBy=multi-user.target\n", 0o644)
	if err := os.MkdirAll("/etc/systemd/system/multi-user.target.wants", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, "/etc/systemd/system/multi-user.target.wants/"+name); err != nil && !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
}

func nativeFixtureDaemon(t *testing.T, tool string, args ...string) func() {
	t.Helper()
	cmd := exec.Command(tool, args...)
	if tool == "/usr/lib/systemd/systemd-networkd" {
		cmd.Env = append(os.Environ(), "SYSTEMD_LOG_LEVEL=debug", "SYSTEMD_LOG_TARGET=console")
	}
	var output nativeBoundedOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			log := output.String()
			if len(log) > 6000 {
				log = log[len(log)-6000:]
			}
			t.Logf("%s daemon evidence: %s", tool, log)
		}
	}
	t.Cleanup(stop)
	return stop
}

func nativeFixtureIntent(address4, address6 string) NativeIntent {
	in := NativeIntent{IPv4: nativeEmptyFamily("manual"), IPv6: nativeEmptyFamily("manual")}
	in.IPv4.Addresses, in.IPv6.Addresses = []string{address4 + "/24"}, []string{address6 + "/64"}
	in.IPv4.DNS, in.IPv6.DNS = []string{"127.0.0.1"}, []string{"::1"}
	in.IPv4.Domains, in.IPv6.Domains = []string{"corp.test"}, []string{"corp.test"}
	return in
}

func nativeFixtureService() *Service {
	return New(Options{Paths: Paths{Dir: "/run/jd-native-state", Unit: "/etc/systemd/system/just-dashboard-network.service"}, IndependentRecovery: true})
}

func TestNativeManagerOwnerLive(t *testing.T) {
	if mode := os.Getenv("JD_NATIVE_MANAGER_WORKER"); mode != "" {
		nativeFixtureCommands(t)
		s := nativeFixtureService()
		s.recoveryInstalled = true
		profile, err := s.NativeProfile(context.Background(), "d0")
		if err != nil || !profile.Editable {
			t.Fatalf("fresh worker native profile: %+v %v", profile, err)
		}
		if mode == "checkpoint-create-lost" {
			delegate := nativeExecute
			lost := false
			nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
				out, err := delegate(ctx, input, tool, args...)
				if tool == "busctl" && strings.Contains(strings.Join(args, " "), " CheckpointCreate aouu ") && err == nil {
					lost = true
					return "", errors.New("fixture lost the actual checkpoint-create reply")
				}
				return out, err
			}
			_, err = s.EditNativeProfile(WithPendingConfirmation(context.Background(), 7), "d0", NativeEditRequest{Generation: profile.Generation, Intent: nativeFixtureIntent("198.18.8.4", "2001:db8:18::4")}, "127.0.0.1")
			if lost && err != nil {
				os.Exit(95)
			}
			t.Fatalf("actual native create reply was not lost before activation: %t %v", lost, err)
		}
		writer := writeChangeJournal
		if mode != "apply" {
			j, err := readChange(s.paths.Dir)
			if err != nil || j.Phase != "awaiting_confirmation" {
				t.Fatalf("confirmation-death worker lacks a pending change: %+v %v", j, err)
			}
			proof, err := s.VerifyReconnection(context.Background(), j.ID, 7, "native-death-session", "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			writeChangeJournal = func(path string, data []byte, permissions os.FileMode) error {
				if !strings.Contains(string(data), `"phase": "confirmed"`) {
					return writer(path, data, permissions)
				}
				if mode == "confirm-save-failure" {
					return errors.New("native fixture durable confirmation failed")
				}
				if mode == "confirm-death" {
					if err := writer(path, data, permissions); err != nil {
						return err
					}
					os.Exit(92)
				}
				if mode == "cleanup-death" {
					var current changeJournal
					if err := json.Unmarshal(data, &current); err != nil {
						return err
					}
					u, err := nativeJournalUndo(&current)
					if err != nil {
						return err
					}
					if u.CheckpointState == "released" || slices.ContainsFunc(u.Files, func(f nativeUndoFile) bool { return f.Cleanup == "complete" }) {
						os.Exit(94)
					}
				}
				return writer(path, data, permissions)
			}
			_, err = s.ConfirmChange(context.Background(), j.ID, 7, "native-death-session", proof.Challenge, "127.0.0.1")
			if mode == "confirm-save-failure" && err != nil {
				os.Exit(93)
			}
			t.Fatalf("native confirmation worker did not reach requested death window %s: %v", mode, err)
		}
		writeChangeJournal = func(path string, data []byte, mode os.FileMode) error {
			if err := writer(path, data, mode); err != nil {
				return err
			}
			if strings.Contains(string(data), `"phase": "awaiting_confirmation"`) {
				os.Exit(91)
			}
			return nil
		}
		_, err = s.EditNativeProfile(WithPendingConfirmation(context.Background(), 7), "d0", NativeEditRequest{Generation: profile.Generation, Intent: nativeFixtureIntent("198.18.8.4", "2001:db8:18::4")}, "127.0.0.1")
		t.Fatalf("backend-death worker did not reach pending apply: %v", err)
	}
	liveRequired(t)
	if os.Getenv("JD_NATIVE_MANAGER_NS") != "1" {
		artifact := t.TempDir()
		helper := filepath.Join(artifact, recoveryBinary)
		buildCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		build := exec.CommandContext(buildCtx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-p", "2", "-trimpath", "-o", helper, "./cmd/server")
		build.Dir = filepath.Join("..", "..")
		build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOMAXPROCS=2")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("native standalone helper build: %v %s", err, out)
		}
		nmRoot := os.Getenv("JD_NATIVE_MANAGER_NM_ROOT")
		if nmRoot == "" {
			t.Fatal("set JD_NATIVE_MANAGER_NM_ROOT to the directory containing the verified NetworkManager/nmcli userland")
		}
		ctx, stop := context.WithTimeout(context.Background(), 4*time.Minute)
		defer stop()
		unbounded := liveSudo("env", "JD_NETNS_LIVE=1", "JD_NATIVE_MANAGER_NS=1", "JD_NATIVE_MANAGER_CASE="+os.Getenv("JD_NATIVE_MANAGER_CASE"), "JD_NATIVE_MANAGER_NM_ORIGIN="+os.Getenv("JD_NATIVE_MANAGER_NM_ORIGIN"), "JD_NATIVE_MANAGER_CHECKPOINT_CREATE_LOST="+os.Getenv("JD_NATIVE_MANAGER_CHECKPOINT_CREATE_LOST"), "JD_NATIVE_MANAGER_HELPER="+helper, "JD_NATIVE_MANAGER_NM_ROOT="+nmRoot, "unshare", "--net", "--mount", "--pid", "--fork", "--kill-child=SIGKILL", "--mount-proc", "--propagation", "private", "--", os.Args[0], "-test.run=^TestNativeManagerOwnerLive$", "-test.count=1", "-test.v")
		cmd := exec.CommandContext(ctx, unbounded.Path, unbounded.Args[1:]...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated native manager acceptance: %v\n%s", err, out)
		}
		t.Logf("native owner acceptance:\n%s", out)
		return
	}
	passwd, _ := os.ReadFile("/etc/passwd")
	group, _ := os.ReadFile("/etc/group")
	packageStatus, err := os.ReadFile("/var/lib/dpkg/status")
	if err != nil || len(packageStatus) > 8<<20 {
		t.Fatal("native fixture package version inventory is unreadable or exceeds its bound")
	}
	for _, target := range []string{"/etc", "/run", "/var/lib"} {
		if err := syscall.Mount("tmpfs", target, "tmpfs", 0, "mode=0755,size=64m"); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mount("sysfs", "/sys", "sysfs", 0, ""); err != nil {
		t.Fatal(err)
	}
	nativeFixtureWrite(t, "/etc/passwd", string(passwd), 0o644)
	nativeFixtureWrite(t, "/etc/group", string(group), 0o644)
	nativeFixtureWrite(t, "/var/lib/dpkg/status", string(packageStatus), 0o644)
	nativeFixtureWrite(t, "/etc/machine-id", "c9b03149ab5e4dada9ee28642e42c561\n", 0o444)
	if err := os.MkdirAll("/run/systemd/netif", 0o755); err != nil {
		t.Fatal(err)
	}
	networkUser, err := user.Lookup("systemd-network")
	if err != nil {
		t.Fatal(err)
	}
	networkUID, uidErr := strconv.Atoi(networkUser.Uid)
	networkGID, gidErr := strconv.Atoi(networkUser.Gid)
	if uidErr != nil || gidErr != nil {
		t.Fatal("native fixture networkd account identity is unreadable")
	}
	if err := os.Chown("/run/systemd/netif", networkUID, networkGID); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("/run/systemd/system", 0o755); err != nil {
		t.Fatal(err)
	}
	nativeFixtureWrite(t, "/run/dbus/fixture.conf", `<busconfig><type>system</type><listen>unix:path=/run/dbus/system_bus_socket</listen><auth>EXTERNAL</auth><policy context="default"><allow user="*"/><allow own="*"/><allow send_destination="*"/><allow receive_sender="*"/></policy></busconfig>`, 0o600)
	stopBus := nativeFixtureDaemon(t, "dbus-daemon", "--nofork", "--config-file=/run/dbus/fixture.conf")
	defer func() { stopBus() }()
	nmRoot := os.Getenv("JD_NATIVE_MANAGER_NM_ROOT")
	if err := os.Setenv("PATH", filepath.Join(nmRoot, "usr/bin")+":"+os.Getenv("PATH")); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("LD_LIBRARY_PATH", filepath.Join(nmRoot, "usr/lib/x86_64-linux-gnu")); err != nil {
		t.Fatal(err)
	}
	nativeFixtureCommands(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	fixtureTest := t
	t.Run("transport-epoch", func(t *testing.T) {
		readID := func() string {
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if id, err := nativeTransportIdentity(ctx); err == nil {
					return id
				}
				time.Sleep(50 * time.Millisecond)
			}
			t.Fatal("private bus did not expose its authentication epoch")
			return ""
		}
		before := readID()
		if id, err := nativeTransportIdentity(nativePinnedBus(ctx, before)); err != nil || id != before {
			t.Fatalf("Transport GUID did not authenticate before native methods: %q %v", id, err)
		}
		name := "org.justdashboard.NativeEpochFixture"
		mismatched := strings.Repeat("1", 32)
		if before == mismatched {
			mismatched = strings.Repeat("2", 32)
		}
		if _, err := nativeBus(nativePinnedBus(ctx, mismatched), "org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus", "call", "RequestName", "su", name, "0"); err == nil {
			t.Fatal("mismatched GUID reached a private bus mutation")
		}
		if owned, err := nativeServiceOwned(ctx, name); err != nil || owned {
			t.Fatalf("refused GUID mutation changed bus ownership: %t %v", owned, err)
		}
		stopBus()
		if err := os.Remove("/run/dbus/system_bus_socket"); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		stopBus = nativeFixtureDaemon(fixtureTest, "dbus-daemon", "--nofork", "--config-file=/run/dbus/fixture.conf")
		after := readID()
		if after == before {
			t.Fatal("actual private bus restart reused its authentication epoch")
		}
		if _, err := nativeBus(nativePinnedBus(ctx, before), "org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus", "call", "RequestName", "su", name, "0"); err == nil {
			t.Fatal("old GUID reached a mutation after actual private bus restart")
		}
		if owned, err := nativeServiceOwned(ctx, name); err != nil || owned {
			t.Fatalf("old-bus mutation changed restarted ownership: %t %v", owned, err)
		}
		if id, err := nativeTransportIdentity(nativePinnedBus(ctx, after)); err != nil || id != after {
			t.Fatalf("new private bus GUID cannot authenticate: %q %v", id, err)
		}
		t.Log("Authenticated transport GUID is pinned; mismatched and actual prior-bus epochs refuse before RequestName effects")
	})
	if _, err := nativeFixtureExecute(ctx, nil, "ip", "link", "set", "lo", "up"); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"networkd", "NetworkManager", "netplan", "netplan-NetworkManager"} {
		if selected := os.Getenv("JD_NATIVE_MANAGER_CASE"); selected != "" && selected != owner {
			continue
		}
		t.Run(owner, func(t *testing.T) {
			useNM := owner == "NetworkManager" || owner == "netplan-NetworkManager"
			expectedOwner := owner
			if owner == "netplan-NetworkManager" {
				expectedOwner = "netplan"
			}
			must := func(tool string, args ...string) string {
				t.Helper()
				out, err := nativeFixtureExecute(ctx, nil, tool, args...)
				if err != nil {
					t.Fatal(err)
				}
				return out
			}
			defer func() {
				for _, path := range []string{"/etc/systemd/network", "/etc/NetworkManager", "/etc/netplan", "/run/systemd/network", "/run/NetworkManager", "/run/jd-native-state"} {
					_ = os.RemoveAll(path)
				}
			}()
			must("ip", "link", "add", "d0", "address", "02:00:00:00:08:02", "type", "dummy")
			defer func() { must("ip", "link", "del", "d0") }()
			must("ip", "link", "set", "d0", "up")
			indexBytes, err := os.ReadFile("/sys/class/net/d0/ifindex")
			if err != nil {
				t.Fatal(err)
			}
			manager := "io.systemd.Network"
			if useNM {
				manager = nmService
			}
			nativeFixtureWrite(t, "/run/udev/data/n"+strings.TrimSpace(string(indexBytes)), "I:1\nE:INTERFACE=d0\nE:ID_NET_MANAGED_BY="+manager+"\n", 0o644)
			before := nativeFixtureIntent("198.18.8.2", "2001:db8:18::2")
			unit := "systemd-networkd.service"
			var stopOwner func()
			if useNM {
				unit = "NetworkManager.service"
				nativeFixtureWrite(t, "/etc/NetworkManager/NetworkManager.conf", "[main]\nplugins=keyfile\ndns=none\nrc-manager=unmanaged\n[device-fixture]\nmatch-device=interface-name:d0\nmanaged=true\n[connectivity]\nenabled=false\n", 0o600)
				profilePath, activation := "/etc/NetworkManager/system-connections/d0.nmconnection", "d0"
				if owner == "netplan-NetworkManager" {
					activation = "27c153d6-ade1-4dde-988b-7acb135a8495"
					nativeFixtureWrite(t, "/etc/netplan/20-fixture.yaml", "network:\n  version: 2\n  renderer: NetworkManager\n  dummy-devices:\n    d0:\n      dhcp4: false\n      dhcp6: false\n      accept-ra: false\n      addresses: [198.18.8.2/24, '2001:db8:18::2/64']\n      nameservers:\n        addresses: [127.0.0.1, '::1']\n        search: [corp.test]\n      networkmanager:\n        uuid: '"+activation+"'\n        name: d0\n", 0o600)
					must("netplan", "generate")
					profilePath = "/run/NetworkManager/system-connections/netplan-d0.nmconnection"
				} else {
					profile := must("nmcli", "--offline", "connection", "add", "type", "dummy", "ifname", "d0", "con-name", "d0", "ipv4.method", "manual", "ipv4.addresses", before.IPv4.Addresses[0], "ipv4.dns", "127.0.0.1", "ipv4.dns-search", "corp.test", "ipv6.method", "manual", "ipv6.addresses", before.IPv6.Addresses[0], "ipv6.dns", "::1", "ipv6.dns-search", "corp.test")
					nativeFixtureWrite(t, profilePath, profile, 0o600)
				}
				stopOwner = nativeFixtureDaemon(t, filepath.Join(nmRoot, "usr/sbin/NetworkManager"), "--debug", "--log-level=INFO", "--plugins=keyfile", "--config=/etc/NetworkManager/NetworkManager.conf", "--system-config-dir=/run/jd-native-nm/conf.d", "--intern-config=/run/jd-native-nm/intern.conf", "--state-file=/run/jd-native-nm/state", "--pid-file=/run/jd-native-nm/pid")
				nmDeadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(nmDeadline) {
					if _, err := nativeFixtureExecute(ctx, nil, "nmcli", "connection", "load", profilePath); err == nil {
						must("nmcli", "connection", "up", activation)
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
			} else {
				if owner == "netplan" {
					nativeFixtureWrite(t, "/etc/netplan/20-fixture.yaml", "network:\n  version: 2\n  renderer: networkd\n  ethernets:\n    d0:\n      dhcp4: false\n      dhcp6: false\n      accept-ra: false\n      addresses: [198.18.8.2/24, '2001:db8:18::2/64']\n      nameservers:\n        addresses: [127.0.0.1, '::1']\n        search: [corp.test]\n", 0o600)
					must("netplan", "generate")
				} else {
					profile, err := renderNativeNetworkd([]byte("[Match]\nName=d0\n[Network]\n"), before)
					if err != nil {
						t.Fatal(err)
					}
					nativeFixtureWrite(t, "/etc/systemd/network/20-d0.network", string(profile), 0o644)
				}
				stopOwner = nativeFixtureDaemon(t, "/usr/lib/systemd/systemd-networkd")
			}
			defer stopOwner()
			nativeFixtureOwnerUnit(t, unit)
			deadline := time.Now().Add(20 * time.Second)
			s := nativeFixtureService()
			var p *NativeProfileView
			for time.Now().Before(deadline) {
				p, _ = s.NativeProfile(ctx, "d0")
				if p != nil && p.Editable && p.Owner == expectedOwner {
					break
				}
				if owner == "NetworkManager" && os.Getenv("JD_NATIVE_MANAGER_NM_ORIGIN") == "refuse-migration" && p != nil && strings.Contains(p.Refusal, "migrates saved profiles through Netplan") {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			if owner == "NetworkManager" && os.Getenv("JD_NATIVE_MANAGER_NM_ORIGIN") == "refuse-migration" {
				if p == nil || p.Editable || !strings.Contains(p.Refusal, "migrates saved profiles through Netplan") {
					t.Fatalf("actual migrating native writer was not refused: %+v", p)
				}
				path := "/etc/NetworkManager/system-connections/d0.nmconnection"
				beforeFile, err := nativeReadProfile(path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.EditNativeProfile(WithPendingConfirmation(ctx, 7), "d0", NativeEditRequest{Generation: p.Generation, Intent: nativeFixtureIntent("198.18.8.3", "2001:db8:18::3")}, "127.0.0.1"); err == nil {
					t.Fatal("migrating writer admitted a native profile change")
				}
				if err := nativeCurrentFile(*beforeFile, beforeFile.Identity); err != nil {
					t.Fatalf("writer refusal changed the selected origin: %v", err)
				}
				if j, err := readChange(s.paths.Dir); !errors.Is(err, os.ErrNotExist) || j != nil {
					t.Fatalf("writer refusal created a pending transaction: %+v %v", j, err)
				}
				unique, err := nativeOwnerIdentity(ctx, nmService)
				if err != nil {
					t.Fatal(err)
				}
				checkpoints, err := nativeBusProperty[[]string](ctx, unique, nmObject, nmService, "Checkpoints", "ao")
				if err != nil || len(checkpoints) != 0 {
					t.Fatalf("writer refusal left a native checkpoint: %v %v", checkpoints, err)
				}
				if sources, err := os.ReadDir("/etc/netplan"); !errors.Is(err, os.ErrNotExist) && (err != nil || len(sources) != 0) {
					t.Fatalf("writer refusal migrated an authored origin: %v %v", sources, err)
				}
				t.Log("Actual Ubuntu Netplan-writer image refused before profile mutation, journal creation, checkpoint creation or origin migration; this is refusal acceptance, not supported editing")
				return
			}
			if p == nil || !p.Editable || p.Owner != expectedOwner {
				t.Logf("native owner capabilities: %+v %+v %+v", nativeCapability(ctx, "NetworkManager"), nativeCapability(ctx, "networkd"), nativeCapability(ctx, "netplan"))
				for _, command := range [][]string{{"busctl", "--system", "list"}, {"networkctl", "--no-pager", "--json=short", "status", "d0"}, {"ip", "-j", "addr", "show", "dev", "d0"}} {
					out, err := nativeFixtureExecute(ctx, nil, command[0], command[1:]...)
					t.Logf("native refusal evidence %s: %s %v", command[0], out, err)
				}
				t.Fatalf("actual native profile is not editable: %+v", p)
			}
			if err := os.MkdirAll(s.paths.Dir, 0o700); err != nil {
				t.Fatal(err)
			}
			helper := os.Getenv("JD_NATIVE_MANAGER_HELPER")
			info, err := os.Lstat(helper)
			if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<20 {
				t.Fatal(err)
			}
			if err := os.Chown(helper, 0, 0); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(helper, 0o700); err != nil {
				t.Fatal(err)
			}
			helperPath := filepath.Join(s.paths.Dir, recoveryBinary)
			if err := os.WriteFile(helperPath, nil, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mount(helper, helperPath, "", syscall.MS_BIND, ""); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = syscall.Unmount(helperPath, 0) }()
			if err := syscall.Mount("", helperPath, "", syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY, ""); err != nil {
				t.Fatal(err)
			}
			s.recoveryInstalled = true
			if os.Getenv("JD_NATIVE_MANAGER_CHECKPOINT_CREATE_LOST") == "1" {
				if owner != "NetworkManager" {
					t.Fatal("lost checkpoint-create acceptance requires the isolated nonmigrating keyfile case")
				}
				nativeFixtureLostCheckpointCreate(t, ctx, s, p, before)
				return
			}
			candidate := nativeFixtureIntent("198.18.8.3", "2001:db8:18::3")
			candidate.IPv4.DNS = []string{"127.0.0.2"}
			candidate.IPv4.Routes = []NativeRoute{{Destination: "198.18.20.0/24", Gateway: "198.18.8.1", Metric: 111, Table: 100}}
			candidate.IPv6.Routes = []NativeRoute{{Destination: "2001:db8:20::/64", Gateway: "2001:db8:18::1", Metric: 111, Table: 100}}
			if _, err := s.EditNativeProfile(WithPendingConfirmation(ctx, 7), "d0", NativeEditRequest{Generation: p.Generation, Intent: candidate}, "127.0.0.1"); err != nil {
				t.Fatal(err)
			}
			j, err := readChange(s.paths.Dir)
			if err != nil || j.Phase != "awaiting_confirmation" {
				t.Fatalf("native pending journal: %+v %v", j, err)
			}
			undo, err := nativeJournalUndo(j)
			if err != nil {
				t.Fatal(err)
			}
			if owner == "netplan-NetworkManager" && (undo.RecoveryStrategy != nativeExactOriginStrategy || undo.NMWriter != "netplan" || undo.Checkpoint != "" || undo.CheckpointState != "none" || undo.UUID != "27c153d6-ade1-4dde-988b-7acb135a8495" || len(undo.Files) != 2 || undo.Files[0].Before.Path != "/etc/netplan/20-fixture.yaml" || undo.Files[1].Before.Path != "/run/NetworkManager/system-connections/netplan-d0.nmconnection") {
				t.Fatalf("actual Ubuntu generated-origin transaction lost its closed origin/UUID strategy: %+v", undo)
			}
			must(filepath.Join(s.paths.Dir, recoveryBinary), "--network-recover", s.paths.Dir, j.ID)
			if owner == "netplan-NetworkManager" {
				for _, f := range undo.Files {
					if err := nativeCurrentFile(f.Before, f.Before.Identity); err != nil {
						t.Fatalf("fresh helper did not restore exact authored/generated bytes and original inode: %v", err)
					}
				}
				entries, err := os.ReadDir("/etc/netplan")
				if err != nil || len(entries) != 1 || entries[0].Name() != "20-fixture.yaml" {
					t.Fatalf("exact-origin recovery migrated authored source inventory: %v %v", entries, err)
				}
			}
			p, err = s.NativeProfile(ctx, "d0")
			if err != nil || p.Intent == nil || !nativeIntentEqual(*p.Intent, before) {
				t.Fatalf("fresh executable did not restore real owner intent: %+v %v", p, err)
			}
			if _, err := s.EditNativeProfile(WithPendingConfirmation(ctx, 7), "d0", NativeEditRequest{Generation: p.Generation, Intent: candidate}, "127.0.0.1"); err != nil {
				t.Fatal(err)
			}
			j, _ = readChange(s.paths.Dir)
			proof, err := s.VerifyReconnection(ctx, j.ID, 7, "native-session", "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			status, err := s.ConfirmChange(ctx, j.ID, 7, "native-session", proof.Challenge, "127.0.0.1")
			if err != nil || status.Cleanup != "complete" {
				t.Fatalf("actual native confirmed cleanup: %+v %v", status, err)
			}
			p = nativeFixtureEqualProfileLive(t, ctx, s, owner, candidate)
			worker := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeManagerOwnerLive$", "-test.count=1")
			worker.Env = append(os.Environ(), "JD_NATIVE_MANAGER_WORKER=apply")
			out, err := worker.CombinedOutput()
			var exited *exec.ExitError
			if !errors.As(err, &exited) || exited.ExitCode() != 91 {
				t.Fatalf("native applying backend did not die at pending state: %v %s", err, out)
			}
			j, _ = readChange(s.paths.Dir)
			must(filepath.Join(s.paths.Dir, recoveryBinary), "--network-recover", s.paths.Dir, j.ID)
			p, err = s.NativeProfile(ctx, "d0")
			if err != nil || p.Intent == nil || !nativeIntentEqual(*p.Intent, candidate) {
				t.Fatalf("backend-death native recovery did not restore confirmed intent: %+v %v", p, err)
			}
			if useNM {
				for index, mode := range []string{"confirm-save-failure", "confirm-death", "cleanup-death"} {
					prior := *p.Intent
					next := nativeFixtureIntent(fmt.Sprintf("198.18.8.%d", 5+index), fmt.Sprintf("2001:db8:18::%d", 5+index))
					if _, err := s.EditNativeProfile(WithPendingConfirmation(ctx, 7), "d0", NativeEditRequest{Generation: p.Generation, Intent: next}, "127.0.0.1"); err != nil {
						t.Fatal(err)
					}
					worker := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeManagerOwnerLive$", "-test.count=1")
					worker.Env = append(os.Environ(), "JD_NATIVE_MANAGER_WORKER="+mode)
					out, err := worker.CombinedOutput()
					expectedExit := map[string]int{"confirm-save-failure": 93, "confirm-death": 92, "cleanup-death": 94}[mode]
					if !errors.As(err, &exited) || exited.ExitCode() != expectedExit {
						t.Fatalf("actual native %s did not reach its death window: %v %s", mode, err, out)
					}
					j, err := readChange(s.paths.Dir)
					if err != nil || j.Cleanup == "complete" {
						t.Fatalf("native death window lost pending cleanup: %+v %v", j, err)
					}
					want, phase := next, "confirmed"
					if mode == "confirm-save-failure" {
						want, phase = prior, "awaiting_confirmation"
					}
					if j.Phase != phase {
						t.Fatalf("native %s left the wrong durable decision: %+v", mode, j.ChangeStatus)
					}
					must(filepath.Join(s.paths.Dir, recoveryBinary), "--network-recover", s.paths.Dir, j.ID)
					p, err = s.NativeProfile(ctx, "d0")
					if err != nil || !p.Editable || p.Intent == nil || !nativeIntentEqual(*p.Intent, want) {
						t.Fatalf("native %s fresh helper did not preserve its durable decision: %+v %v", mode, p, err)
					}
					finished, err := readChange(s.paths.Dir)
					if err != nil || finished.Cleanup != "complete" {
						t.Fatalf("native %s cleanup did not retry completely: %+v %v", mode, finished, err)
					}
				}
				t.Log("Native confirmation save failure, process death after durable confirmation, and lost terminal cleanup outcome recovered through fresh helpers")
			}
			// The DNS page's split-DNS editor writes this shape: the profile's own
			// intent with servers split by family and the same routing and search
			// domains on every enabled family.
			split := *p.Intent
			split.IPv4.DNS, split.IPv6.DNS = []string{"127.0.0.3"}, []string{"::1"}
			split.IPv4.Domains = []string{"~corp.test", "lab.test"}
			split.IPv6.Domains = []string{"~corp.test", "lab.test"}
			if _, err := s.EditNativeProfile(WithPendingConfirmation(ctx, 7), "d0", NativeEditRequest{Generation: p.Generation, Intent: split}, "127.0.0.1"); err != nil {
				t.Fatalf("%s split-DNS temporary apply: %v", owner, err)
			}
			j, _ = readChange(s.paths.Dir)
			proof, err = s.VerifyReconnection(ctx, j.ID, 7, "native-session", "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			if status, err := s.ConfirmChange(ctx, j.ID, 7, "native-session", proof.Challenge, "127.0.0.1"); err != nil || status.Cleanup != "complete" {
				t.Fatalf("%s split-DNS confirmation: %+v %v", owner, status, err)
			}
			splitDeadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(splitDeadline) {
				p, err = s.NativeProfile(ctx, "d0")
				if err == nil && p.Intent != nil && nativeIntentEqual(*p.Intent, split) && p.Runtime.Status == "matching" {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			if err != nil || p.Intent == nil || !nativeIntentEqual(*p.Intent, split) || p.Runtime.Status != "matching" {
				t.Fatalf("%s split-DNS runtime agreement: %+v %v", owner, p, err)
			}
			if !useNM {
				state, _ := nativeFixtureExecute(ctx, nil, "networkctl", "--no-pager", "--json=short", "status", "d0")
				if !strings.Contains(state, `"corp.test"`) || !strings.Contains(state, `"lab.test"`) {
					t.Fatalf("%s split-DNS runtime domains: %s", owner, state)
				}
			}
			t.Logf("%s split-DNS routing/search domains and per-family servers applied temporarily, confirmed and verified against native runtime", owner)
			t.Logf("%s actual IPv4/IPv6 addresses, DNS, metric/table routes, rollback, confirmation and backend-death helper recovery passed", owner)
			stopOwner()
		})
	}
}

func nativeFixtureLostCheckpointCreate(t *testing.T, ctx context.Context, s *Service, beforeView *NativeProfileView, before NativeIntent) {
	t.Helper()
	selected, err := s.readNativeProfile(ctx, "d0")
	if err != nil || selected == nil || selected.RecoveryStrategy != nativeCheckpointStrategy {
		t.Fatal("lost-create fixture lacks a verified native checkpoint owner:", err)
	}
	worker := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeManagerOwnerLive$", "-test.count=1")
	worker.Env = append(os.Environ(), "JD_NATIVE_MANAGER_WORKER=checkpoint-create-lost")
	out, err := worker.CombinedOutput()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 95 {
		t.Fatalf("actual checkpoint-create reply loss did not leave its journal: %v %s", err, out)
	}
	j, err := readChange(s.paths.Dir)
	if err != nil || j.Phase != "degraded" || j.Cleanup == "complete" {
		t.Fatalf("lost-create evidence was discarded: %+v %v", j, err)
	}
	u, err := nativeJournalUndo(j)
	if err != nil || u.Checkpoint != "" || u.CheckpointState != "creating" {
		t.Fatalf("lost-create object identity was guessed: %+v %v", u, err)
	}
	checkpoints := func() []string {
		t.Helper()
		objects, err := nativeBusProperty[[]string](nativePinnedBus(ctx, selected.TransportGUID), selected.OwnerBus, nmObject, nmService, "Checkpoints", "ao")
		if err != nil {
			t.Fatal(err)
		}
		return objects
	}
	if len(checkpoints()) != 1 {
		t.Fatal("actual lost create did not leave its bounded native object")
	}
	if _, err := nativeFixtureExecute(ctx, nil, filepath.Join(s.paths.Dir, recoveryBinary), "--network-recover", s.paths.Dir, j.ID); err == nil {
		t.Fatal("fresh helper guessed an unsaved checkpoint from opaque inventory")
	}
	if _, err := s.EditNativeProfile(WithPendingConfirmation(ctx, 7), "d0", NativeEditRequest{Generation: beforeView.Generation, Intent: nativeFixtureIntent("198.18.8.5", "2001:db8:18::5")}, "127.0.0.1"); err == nil {
		t.Fatal("the next native change replaced unresolved create evidence")
	}
	retained, err := readChange(s.paths.Dir)
	if err != nil || retained.ID != j.ID {
		t.Fatal("the unresolved native admission journal was replaced:", err)
	}
	if err := nativeCurrentFile(selected.File, selected.File.Identity); err != nil {
		t.Fatal("lost-create admission changed its selected profile before any activation:", err)
	}
	p, err := s.NativeProfile(ctx, "d0")
	if err != nil || p == nil || p.Intent == nil || !nativeIntentEqual(*p.Intent, before) || p.Runtime.Status != "matching" {
		t.Fatalf("lost-create admission changed real owner/runtime intent: %+v %v", p, err)
	}
	deadline := time.Now().Add(130 * time.Second)
	for len(checkpoints()) != 0 && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			t.Fatal("bounded native timeout cleanup did not finish:", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	if len(checkpoints()) != 0 {
		t.Fatal("lost native object survived its recorded 120-second native timeout")
	}
	if out, err := nativeFixtureExecute(ctx, nil, filepath.Join(s.paths.Dir, recoveryBinary), "--network-recover", s.paths.Dir, j.ID); err != nil {
		t.Fatalf("fresh helper could not retry after actual native timeout: %v %s", err, out)
	}
	finished, err := readChange(s.paths.Dir)
	if err != nil || finished.Phase != "recovered" || finished.Cleanup != "complete" {
		t.Fatalf("lost-create cleanup did not finish durably: %+v %v", finished, err)
	}
	p, err = s.NativeProfile(ctx, "d0")
	if err != nil || p == nil || p.Intent == nil || !nativeIntentEqual(*p.Intent, before) || p.Runtime.Status != "matching" {
		t.Fatalf("bounded native timeout/fresh helper did not retain prior real intent: %+v %v", p, err)
	}
	t.Log("Actual native120-second checkpoint-create reply loss preserved identity-less evidence, refused early helper/next change, then completed only after native timeout removed the opaque object")
}
