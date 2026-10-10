package netx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootRecoveryCapturesOnlyManagedDependencies(t *testing.T) {
	old := emptySpec()
	old.Namespaces = []NamespaceSpec{{Name: "oldns"}}
	old.Links = []LinkSpec{
		{Name: "oldbr", Kind: "bridge", Up: true, Addresses: []string{"10.70.0.1/24"}},
		{Name: "oldveth", Kind: "veth", Peer: "eth0", PeerNamespace: "oldns", Master: "oldbr", MTU: 1400, Up: true},
	}
	old.Addresses = []AddressSpec{{ID: 1, Link: "eth0", CIDR: "10.70.0.2/24"}}
	old.Routes = []RouteSpec{{ID: 2, Destination: "192.0.2.0/24", Device: "oldbr", Table: 254}}
	old.Rules = []RuleSpec{{ID: 3, Family: "inet", Priority: 10000, Table: 100, Action: "lookup"}}
	old.Shaping = []ShapeSpec{{Device: "oldbr", Qdisc: "fq_codel"}}
	commands, err := bootRecoveryDependencies(old)
	if err != nil {
		t.Fatal(err)
	}
	var text []string
	for _, c := range commands {
		if c.Tool != "ip" || !validBootRecoveryCommand(c.Args) {
			t.Fatalf("unexpected dependency: %+v", c)
		}
		if c.AllowExists != recoveryCreationArgs(c.Args) {
			t.Fatalf("property failure could be swallowed: %+v", c)
		}
		text = append(text, strings.Join(c.Args, " "))
	}
	joined := strings.Join(text, "\n")
	for _, want := range []string{"netns add oldns", "link add oldbr type bridge", "link set oldveth master oldbr", "netns exec oldns ip addr add 10.70.0.2/24 dev eth0"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s in %s", want, joined)
		}
	}
	if strings.Contains(joined, "route ") || strings.Contains(joined, "rule ") || strings.Contains(joined, "qdisc ") {
		t.Fatalf("boot dependencies replayed unrelated runtime policy: %s", joined)
	}
	old.Links[0].Addresses = []string{"bad prefix"}
	if _, err := bootRecoveryDependencies(old); err == nil {
		t.Fatal("invalid saved dependency was silently omitted")
	}
}

func TestBootRecoveryRestoresFilesThenDependenciesBeforeTargetedUndo(t *testing.T) {
	for _, boot := range []bool{false, true} {
		t.Run(map[bool]string{false: "timer", true: "boot"}[boot], func(t *testing.T) {
			r := record(t, "systemctl").on("ip link add oldbr", "").on("ip addr add", "").on("ip route add", "")
			s := testService(t)
			old := emptySpec()
			old.Links = []LinkSpec{{Name: "oldbr", Kind: "bridge"}}
			dependencies, err := bootRecoveryDependencies(old)
			if err != nil {
				t.Fatal(err)
			}
			j := &changeJournal{Paths: s.paths, ChangeStatus: ChangeStatus{ID: "boot", Phase: "runtime_applied", Generation: strings.Repeat("a", 64)},
				Files: []recoverySnapshot{{Path: s.specPath(), Data: []byte("previous"), Mode: 0o600, Exists: true}}, BootDependencies: dependencies,
				Commands: []recoveryCommand{{Tool: "ip", Args: []string{"route", "add", "192.0.2.0/24", "dev", "oldbr"}}},
			}
			if err := writeFileAtomic(s.specPath(), []byte("candidate"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := j.save(); err != nil {
				t.Fatal(err)
			}
			prevStdin := runStdin
			runStdin = func(ctx context.Context, data []byte, tool string, args ...string) (string, error) {
				if bytes, err := os.ReadFile(s.specPath()); err != nil || string(bytes) != "previous" {
					t.Fatal("runtime recovery began before restoring the snapshot")
				}
				return prevStdin(ctx, data, tool, args...)
			}
			defer func() { runStdin = prevStdin }()
			if err := recoverNetwork(context.Background(), s.paths.Dir, "pending", boot); err != nil {
				t.Fatal(err)
			}
			commands := r.commands()
			if boot {
				if len(commands) != 2 || !strings.HasPrefix(commands[0], "ip link add") || !strings.HasPrefix(commands[1], "ip route add") {
					t.Fatalf("boot order = %v", commands)
				}
			} else if len(commands) != 1 || !strings.HasPrefix(commands[0], "ip route add") {
				t.Fatalf("timer replayed unchanged dependencies: %v", commands)
			}
		})
	}
}

func TestBootRecoveryReportsDependencyFailuresAndAcceptsOnlyCreationDuplicates(t *testing.T) {
	r := record(t, "systemctl").fail("ip link add oldbr", "RTNETLINK answers: File exists").fail("ip link set oldbr", "permission denied").on("ip route add", "")
	s := testService(t)
	j := &changeJournal{Paths: s.paths, ChangeStatus: ChangeStatus{ID: "boot", Phase: "prepared", Generation: strings.Repeat("a", 64)},
		BootDependencies: []recoveryCommand{
			{Tool: "ip", Args: []string{"link", "add", "oldbr", "type", "bridge"}, AllowExists: true},
			{Tool: "ip", Args: []string{"link", "set", "oldbr", "up"}},
		}, Commands: []recoveryCommand{{Tool: "ip", Args: []string{"route", "add", "192.0.2.0/24", "dev", "oldbr"}}},
	}
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	if err := RecoverNetworkBoot(context.Background(), s.paths.Dir, "pending"); err == nil {
		t.Fatal("dependency failure reported healthy recovery")
	}
	got, err := readChange(s.paths.Dir)
	if err != nil || got.Phase != "degraded" || len(got.RecoveryErrors) != 1 || !strings.Contains(got.RecoveryErrors[0], "permission denied") || len(r.commands()) != 3 {
		t.Fatalf("failed dependency evidence = %+v, %v; commands=%v", got, err, r.commands())
	}
	if recoveryExpectedExistence("ip", []string{"link", "set", "oldbr", "up"}, "File exists", errors.New("exit 2")) {
		t.Fatal("property error treated as an existing object")
	}
	if !recoveryExpectedExistence("ip", []string{"addr", "add", "10.81.0.1/24", "dev", "oldbr"}, "Error: ipv4: Address already assigned.", errors.New("exit 2")) {
		t.Fatal("kernel address duplicate was not recognized")
	}
}

func TestBootRecoveryUnitWaitsForNativeOwnersAndPrecedesManagedReplay(t *testing.T) {
	paths := DefaultPaths()
	unit := renderRecoveryUnit(paths)
	for _, want := range []string{"Wants=network-online.target", "After=local-fs.target " + networkBootOwners, "Before=" + UnitName, " --network-recover-boot " + paths.Dir + " pending"} {
		if !strings.Contains(unit, want) {
			t.Fatalf("recovery unit missing %s: %s", want, unit)
		}
	}
	want := "ExecStartPre=" + filepath.Join(paths.Dir, recoveryBinary) + " --network-recover-boot " + paths.Dir + " pending"
	if !strings.Contains(renderUnit(paths, false, true), want) {
		t.Fatal("managed unit does not share the boot dependency recovery path")
	}
}
