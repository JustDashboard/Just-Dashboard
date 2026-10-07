package netx

import (
	"context"
	"os"
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
