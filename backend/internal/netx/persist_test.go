package netx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitWritesFilesAndEnablesTheUnitOnlyAfterTheApplyWorked(t *testing.T) {
	rec := record(t).on("nft -c -f", "").on("systemctl daemon-reload", "").
		on("systemctl is-enabled", "disabled").on("systemctl enable", "")
	s := testService(t)
	sp := emptySpec()
	sp.Sysctls["net.ipv4.tcp_syncookies"] = "1"
	applied := false
	if err := s.commit(context.Background(), sp, step{apply: func(context.Context) error { applied = true; return nil }}); err != nil {
		t.Fatal(err)
	}
	if !applied || !rec.ran("systemctl enable "+UnitName) {
		t.Fatalf("apply ran %v, commands %v", applied, rec.commands())
	}
	b, err := os.ReadFile(s.paths.Sysctl)
	if err != nil || !strings.Contains(string(b), "net.ipv4.tcp_syncookies = 1") {
		t.Fatalf("sysctl drop-in = %q, %v", b, err)
	}
	loaded, err := s.loadSpec()
	if err != nil || loaded.Sysctls["net.ipv4.tcp_syncookies"] != "1" {
		t.Fatalf("spec not saved: %+v %v", loaded, err)
	}
}

func TestCommitPutsTheRuntimeChangeBackWhenVerifyFails(t *testing.T) {
	record(t).on("nft -c -f", "")
	s := testService(t)
	undone := false
	err := s.commit(context.Background(), emptySpec(), step{
		apply:  func(context.Context) error { return nil },
		verify: func(context.Context) error { return guarded("moved") },
		undo:   func(context.Context) { undone = true },
	})
	if err == nil || !undone {
		t.Fatalf("err %v, undone %v", err, undone)
	}
	if _, err := os.Stat(s.specPath()); err == nil {
		t.Fatal("a refused change must not write the spec")
	}
}

func TestCommitRefusesARulesetNftRejects(t *testing.T) {
	rec := record(t).fail("nft -c -f", "Error: syntax error")
	s := testService(t)
	sp := emptySpec()
	sp.Limits = []LimitSpec{{ID: 1, Name: "ssh", Protocol: "tcp", Ports: "22", Rate: 10, Per: "minute", Action: "drop"}}
	err := s.commit(context.Background(), sp, step{apply: func(context.Context) error {
		t.Fatal("nothing may be applied after the check failed")
		return nil
	}})
	if err == nil || !strings.Contains(err.Error(), "refused by nft") {
		t.Fatalf("err = %v (%v)", err, rec.commands())
	}
}

func TestUnitRestoresEverythingAndAdmitsOnlyWhenNeeded(t *testing.T) {
	paths := DefaultPaths()
	without := renderUnit(paths, false)
	with := renderUnit(paths, true)
	for _, want := range []string{
		"ExecStart=-ip -force -batch /etc/just-dashboard/network/links.batch",
		"ExecStart=-ip -6 -force -batch /etc/just-dashboard/network/rules6.batch",
		"ExecStart=-tc -force -batch /etc/just-dashboard/network/shaping.batch",
		"ExecStart=-nft -f /etc/just-dashboard/network/gateway.nft",
		"ExecStop=-nft delete table inet jd_gateway",
		"After=network-online.target",
	} {
		if !strings.Contains(without, want) {
			t.Errorf("unit lacks %q", want)
		}
	}
	if strings.Contains(without, "-I FORWARD") {
		t.Error("nothing to admit, yet the unit inserts an admission rule")
	}
	if !strings.Contains(with, "ExecStart=-iptables -I DOCKER-USER 1 -m connmark --mark 0x4a000000/0xff000000") {
		t.Error("admission into DOCKER-USER missing")
	}
	// Delete comes before insert in each chain, so a second run leaves one rule.
	if strings.Index(with, "-iptables -D FORWARD") > strings.Index(with, "-iptables -I FORWARD") {
		t.Error("delete must precede insert")
	}
	// A fresh boot has no rule to delete. The deletes are preparation, not
	// restoration, so their expected failure is not a measured boot result.
	for _, unit := range []string{without, with} {
		if strings.Contains(unit, "ExecStart=-iptables -D") || strings.Contains(unit, "ExecStart=-ip6tables -D") || !strings.Contains(unit, "ExecStartPre=-ip6tables -D DOCKER-USER") {
			t.Errorf("admission deletes must be ExecStartPre lines:\n%s", unit)
		}
	}
}

func TestUnitClearsShapingBeforeItsBatchAndOutsideTheRestoration(t *testing.T) {
	s := testService(t)
	sp := gwShapeSpec()
	unit := s.unitFor(sp)
	batch := renderShaping(sp)
	if strings.Contains(batch, " del ") {
		t.Fatalf("a delete stayed in the batch whose exit is the boot result:\n%s", batch)
	}
	for _, want := range []string{"ExecStartPre=-tc qdisc del dev eth0 root", "ExecStartPre=-tc filter del dev eth0 parent ffff: prio 1", "ExecStartPre=-tc filter del dev wg0 parent ffff: prio 1"} {
		if !strings.Contains(unit, want+"\n") || strings.Index(unit, want) > strings.Index(unit, "ExecStart=-tc -force -batch") {
			t.Errorf("unit lacks %q before the batch:\n%s", want, unit)
		}
	}
}

func TestCommitNeedsNoNftForAChangeOutsideTheGateway(t *testing.T) {
	// No reply for nft at all: running it would fail the test.
	record(t, "nft").on("systemctl daemon-reload", "").
		on("systemctl is-enabled", "disabled").on("systemctl enable", "")
	s := testService(t)
	sp := emptySpec()
	sp.Links = []LinkSpec{{Name: "lan0", Kind: "bridge", Up: true}}
	if err := s.commit(context.Background(), sp, step{}); err != nil {
		t.Fatalf("a bridge on a host without nftables: %v", err)
	}
}

func TestCommitRestoresEveryBootFileAndRuntimeWhenPersistenceFails(t *testing.T) {
	for _, target := range []string{"sysctl", "spec"} {
		t.Run(target, func(t *testing.T) {
			record(t).on("systemctl daemon-reload", "").on("systemctl is-enabled", "enabled")
			s := testService(t)
			before := emptySpec()
			before.Sysctls["net.ipv4.tcp_syncookies"] = "1"
			if err := s.commit(context.Background(), before, step{}); err != nil {
				t.Fatal(err)
			}
			files, err := s.renderAll(before)
			if err != nil {
				t.Fatal(err)
			}
			files[s.specPath()], err = os.ReadFile(s.specPath())
			if err != nil {
				t.Fatal(err)
			}
			failedPath := s.paths.Sysctl
			if target == "spec" {
				failedPath = s.specPath()
			}
			prevWriter := writeNetworkFile
			t.Cleanup(func() { writeNetworkFile = prevWriter })
			writeNetworkFile = func(path string, data []byte, mode os.FileMode) error {
				if path == failedPath {
					return errors.New("disk refused the candidate")
				}
				return writeFileAtomic(path, data, mode)
			}
			next := before.clone()
			next.Sysctls["net.ipv4.tcp_syncookies"] = "0"
			next.Links = []LinkSpec{{Name: "test0", Kind: "dummy", Up: true}}
			applied, undone := false, false
			err = s.commit(context.Background(), next, step{
				apply: func(context.Context) error { applied = true; return nil },
				undo:  func(context.Context) { undone = true },
			})
			if err == nil || !applied || !undone {
				t.Fatalf("err %v, applied %v, undone %v", err, applied, undone)
			}
			for path, want := range files {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != string(want) {
					t.Errorf("%s kept failed candidate: %q, %v", path, got, err)
				}
			}
			info, _ := os.Stat(s.specPath())
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("spec mode = %o", info.Mode().Perm())
			}
		})
	}
}

func TestCommitRefusesUnreadableBootTargetsBeforeChangingRuntime(t *testing.T) {
	record(t)
	s := testService(t)
	if err := os.MkdirAll(s.paths.Sysctl, 0o755); err != nil {
		t.Fatal(err)
	}
	err := s.commit(context.Background(), emptySpec(), step{apply: func(context.Context) error {
		t.Fatal("runtime changed before persistence targets were checked")
		return nil
	}})
	if err == nil || !strings.Contains(err.Error(), "before changing the network") {
		t.Fatalf("err = %v", err)
	}
}

func TestCommitRemovesNewBootFilesWhenTheFirstSpecCannotBeSaved(t *testing.T) {
	record(t)
	s := testService(t)
	prevWriter := writeNetworkFile
	t.Cleanup(func() { writeNetworkFile = prevWriter })
	writeNetworkFile = func(path string, data []byte, mode os.FileMode) error {
		if path == s.specPath() {
			return errors.New("spec write failed")
		}
		return writeFileAtomic(path, data, mode)
	}
	if err := s.commit(context.Background(), emptySpec(), step{}); err == nil {
		t.Fatal("a failed spec write was reported successful")
	}
	for _, path := range []string{s.paths.Unit, s.paths.Sysctl, filepath.Join(s.paths.Dir, linksFile), filepath.Join(s.paths.Dir, rules6File)} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("new boot file survived failed first commit: %s, %v", path, err)
		}
	}
}

func TestCanceledRequestStillRunsRollbackWithABoundedContext(t *testing.T) {
	record(t)
	s := testService(t)
	ctx, cancel := context.WithCancel(context.Background())
	undone := false
	err := s.commit(ctx, emptySpec(), step{
		apply:  func(context.Context) error { cancel(); return nil },
		verify: func(ctx context.Context) error { return ctx.Err() },
		undo: func(ctx context.Context) {
			undone = true
			if ctx.Err() != nil {
				t.Errorf("rollback inherited canceled context: %v", ctx.Err())
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Error("rollback has no deadline")
			}
		},
	})
	if !errors.Is(err, context.Canceled) || !undone {
		t.Fatalf("err %v, undone %v", err, undone)
	}
	applying(func(context.Context) error { return context.Canceled }, func(ctx context.Context) {
		if ctx.Err() != nil {
			t.Errorf("partial apply rollback inherited cancellation: %v", ctx.Err())
		}
	})(ctx)
}
