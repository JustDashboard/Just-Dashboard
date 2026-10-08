package netx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeOnlyChangeRetainsObservedUndoWithoutWritingBootConfiguration(t *testing.T) {
	r := record(t, "systemctl").on("ip link set eth0 mtu", "")
	s := testService(t)
	st := step{
		apply: func(ctx context.Context) error {
			_, err := run(ctx, "ip", "link", "set", "eth0", "mtu", "1400")
			return err
		},
		undo:     func(ctx context.Context) { s.best(ctx, "ip", "link", "set", "eth0", "mtu", "1500") },
		recovery: []recoveryCommand{{Tool: "ip", Args: []string{"link", "set", "eth0", "mtu", "1500"}}},
	}
	if err := s.runtimeOnly(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	j, err := readChange(s.paths.Dir)
	if err != nil || j.Phase != "saved" || j.Persistence != "not_applicable" || j.Boot != "not_applicable" || len(j.Files) != 0 {
		t.Fatalf("runtime-only journal = %+v, %v", j, err)
	}
	for _, path := range []string{s.specPath(), s.paths.Unit, s.paths.Sysctl, filepath.Join(s.paths.Dir, linksFile)} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("runtime edit created boot configuration %s: %v", path, err)
		}
	}
	// Emulate a process death after the journaled edit reached the kernel.
	j.Phase = "runtime_applied"
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	if err := RecoverNetwork(context.Background(), s.paths.Dir, j.ID); err != nil {
		t.Fatal(err)
	}
	j, err = readChange(s.paths.Dir)
	if err != nil || j.Phase != "recovered" || j.Persistence != "not_applicable" || !r.ran("ip link set eth0 mtu 1500") {
		t.Fatalf("runtime recovery = %+v, %v; calls %v", j, err, r.commands())
	}
}

func TestRuntimeOnlyRecoveryFailuresRemainVisibleAndBlockAnotherEdit(t *testing.T) {
	record(t, "systemctl").fail("ip link set eth0 mtu 1500", "permission denied")
	s := testService(t)
	err := s.runtimeOnly(context.Background(), step{
		apply:    func(context.Context) error { return nil },
		verify:   func(context.Context) error { return errors.New("path changed") },
		undo:     func(ctx context.Context) { s.best(ctx, "ip", "link", "set", "eth0", "mtu", "1500") },
		recovery: []recoveryCommand{{Tool: "ip", Args: []string{"link", "set", "eth0", "mtu", "1500"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "path changed") {
		t.Fatalf("verify result = %v", err)
	}
	j, err := readChange(s.paths.Dir)
	if err != nil || j.Phase != "degraded" || len(j.RecoveryErrors) != 1 || j.Persistence != "not_applicable" {
		t.Fatalf("recovery evidence = %+v, %v", j, err)
	}
	if err := s.runtimeOnly(context.Background(), step{apply: func(context.Context) error { t.Fatal("unresolved recovery allowed mutation"); return nil }}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("unresolved journal = %v", err)
	}
}

func TestPendingRuntimeOnlyChangeNeedsReconnectionWithoutClaimingBootPersistence(t *testing.T) {
	h := pendingHost(t)
	if err := h.runtimeOnly(WithPendingConfirmation(context.Background(), 7), step{
		apply:    func(context.Context) error { return nil },
		recovery: []recoveryCommand{{Tool: "ip", Args: []string{"link", "set", "eth0", "mtu", "1500"}}},
	}); err != nil {
		t.Fatal(err)
	}
	j, err := readChange(h.paths.Dir)
	if err != nil || j.Phase != "awaiting_confirmation" || j.Persistence != "not_applicable" || j.AppliedAt.IsZero() || j.OwnerUserID != 7 {
		t.Fatalf("runtime-only pending = %+v, %v", j, err)
	}
	proof, err := h.VerifyReconnection(context.Background(), j.ID, 7, "session", "192.0.2.17")
	if err != nil {
		t.Fatal(err)
	}
	status, err := h.ConfirmChange(context.Background(), j.ID, 7, "session", proof.Challenge, "192.0.2.17")
	if err != nil || status.Phase != "confirmed" || status.Persistence != "not_applicable" || status.Boot != "not_applicable" {
		t.Fatalf("runtime-only confirmation = %+v, %v", status, err)
	}
}

func TestRecoveryRestoresForwardingBeforeUnchangedManagedProtections(t *testing.T) {
	rtProc(t, nil, "0", "0")
	before := map[string]string{"net.ipv4.tcp_syncookies": "1", "net.ipv4.conf.all.rp_filter": "2"}
	for key, value := range before {
		path := procPath(key)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := testService(t)
	old, next := emptySpec(), emptySpec()
	old.Sysctls[sysctlForwardV4], next.Sysctls[sysctlForwardV4] = "0", "1"
	for key, value := range before {
		old.Sysctls[key], next.Sysctls[key] = value, value
	}
	commands, err := s.recoveryPlan(context.Background(), old, next)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 3 || commands[0].Args[1] != sysctlForwardV4+"=0" {
		t.Fatalf("recovery lost reset protections or forwarding-first order: %+v", commands)
	}
	for _, cmd := range commands[1:] {
		key, value, _ := strings.Cut(cmd.Args[1], "=")
		if before[key] != value {
			t.Fatalf("recovery did not preserve observed value: %+v", cmd)
		}
	}
}

func TestRecoveryUnitIsRecheckedWhenPreviouslyInstalled(t *testing.T) {
	s := testService(t)
	s.recoveryInstalled = true
	r := record(t).on(filepath.Join(s.paths.Dir, recoveryBinary)+" --network-recovery-check", "").on("systemctl daemon-reload", "").on("systemctl enable", "")
	unitPath := filepath.Join(filepath.Dir(s.paths.Unit), "just-dashboard-network-recovery.service")
	if err := s.installRecoveryBinary(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !r.ran("systemctl enable just-dashboard-network-recovery.service") {
		t.Fatal("cached installation skipped enabling reboot recovery")
	}
	if err := os.WriteFile(unitPath, []byte("[Unit]\nDescription=owned elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.installRecoveryBinary(context.Background()); err == nil {
		t.Fatal("cached installation accepted a replacement unit owned elsewhere")
	}
}
