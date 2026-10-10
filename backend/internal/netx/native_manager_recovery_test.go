package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func nativeRootTest(t *testing.T) bool {
	t.Helper()
	if os.Geteuid() == 0 {
		return true
	}
	if exec.Command("sudo", "-n", "true").Run() != nil {
		t.Skip("native profile provenance tests need root or passwordless sudo")
	}
	cmd := exec.Command("sudo", "-n", os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1", "-test.v")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("owned root-file fixture: %v\n%s", err, out)
	}
	return false
}

type nativeRecoveryFixture struct {
	root string
	s    *Service
	j    *changeJournal
	u    *nativeUndo
}

func newNativeRecoveryFixture(t *testing.T, owner string) *nativeRecoveryFixture {
	t.Helper()
	root, err := os.MkdirTemp("/run", "jd-native-recovery-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	priorPath, priorExecute, priorWriter, priorExchange, priorClaim := nativeHostPath, nativeExecute, writeChangeJournal, nativeExchangeFiles, nativeClaimStage
	nativeHostPath = func(path string) string { return filepath.Join(root, path) }
	t.Cleanup(func() {
		nativeHostPath, nativeExecute, writeChangeJournal, nativeExchangeFiles, nativeClaimStage = priorPath, priorExecute, priorWriter, priorExchange, priorClaim
	})
	bootID := "cd8a65d9-42f1-4fe9-966c-c9a3268f89a2"
	if err := os.MkdirAll(nativeHostPath("/proc/sys/kernel/random"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nativeHostPath("/proc/sys/kernel/random/boot_id"), []byte(bootID), 0o600); err != nil {
		t.Fatal(err)
	}
	profilePath := "/etc/systemd/network/20-d0.network"
	version, renderer := "systemd 257 (257)", "networkd"
	if owner == "NetworkManager" {
		profilePath, version, renderer = "/etc/NetworkManager/system-connections/d0.nmconnection", "1.52.0", owner
	}
	if err := os.MkdirAll(filepath.Dir(nativeHostPath(profilePath)), 0o700); err != nil {
		t.Fatal(err)
	}
	before := []byte("[Match]\nName=d0\n[Network]\nDHCP=no\nIPv6AcceptRA=no\nAddress=192.0.2.14/24\n")
	candidate := []byte(strings.ReplaceAll(string(before), "192.0.2.14", "192.0.2.15"))
	if err := os.WriteFile(nativeHostPath(profilePath), before, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := nativeReadProfile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	p := &nativeProfile{View: NativeProfileView{Owner: owner, Renderer: renderer, Version: version, Device: "d0", Kind: "dummy", Contract: NativeContract{Members: []string{}}}, Device: ipLink{IfName: "d0", IfIndex: 2, Address: "02:00:00:00:00:14"}, File: *file}
	p.OwnerBus = ":1.17"
	p.BootID, p.BusID, p.TransportGUID = bootID, "5b286446d6274299b84164c935f01076", "7b286446d6274299b84164c935f01076"
	p.View.Intent = &NativeIntent{IPv4: nativeEmptyFamily("manual"), IPv6: nativeEmptyFamily("disabled")}
	p.View.Intent.IPv4.Addresses = []string{"192.0.2.14/24"}
	intent := *p.View.Intent
	intent.IPv4.Addresses = []string{"192.0.2.15/24"}
	if owner == "NetworkManager" {
		p.UUID, p.DeviceObject, p.ConnectionObject = "55afc950-b609-4466-955a-2fa4788c0140", nmObject+"/Devices/1", nmObject+"/Settings/1"
	}
	u, err := prepareNativeFiles(p, intent)
	if err != nil {
		t.Fatal(err)
	}
	if err := nativeStageUndoFiles(u, []nativeProfileFile{*file}, [][]byte{candidate}); err != nil {
		t.Fatal(err)
	}
	if owner == "NetworkManager" {
		u.Checkpoint, u.CheckpointState = nmObject+"/Checkpoint/1", "armed"
	}
	c, err := nativeCommand(u)
	if err != nil {
		t.Fatal(err)
	}
	paths := Paths{Dir: filepath.Join(root, "journal")}
	s := New(Options{Paths: paths})
	j := &changeJournal{Paths: paths, Commands: []recoveryCommand{c}, ChangeStatus: ChangeStatus{ID: u.Transaction, Generation: digestBytes(c.Input), Phase: "awaiting_confirmation", Watchdog: "armed", Cleanup: "pending", Runtime: "applied", Persistence: "written", OwnerUserID: 7, AppliedAt: time.Now().Add(-time.Second), ExpiresAt: time.Now().Add(time.Minute)}}
	staged := u.Files[0].Candidate
	staged.Path = u.Files[0].CandidatePath
	if err := nativeReplaceProfile(staged, u.Files[0].Before); err != nil {
		t.Fatal(err)
	}
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	f := &nativeRecoveryFixture{root: root, s: s, j: j, u: u}
	nativeExecute = f.ownerTranscript(t)
	return f
}

func (f *nativeRecoveryFixture) ownerTranscript(t *testing.T) func(context.Context, []byte, string, ...string) (string, error) {
	t.Helper()
	return func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		bus := func(kind string, value any) (string, error) {
			data := value
			if !strings.Contains(joined, " get-property ") {
				data = []any{value}
			}
			b, err := json.Marshal(map[string]any{"type": kind, "data": data})
			return string(b), err
		}
		switch {
		case tool == "busctl" && strings.HasSuffix(joined, " status"):
			return "BusID=" + f.u.TransportGUID + "\n", nil
		case tool == "busctl" && strings.HasSuffix(joined, " GetId"):
			return bus("s", f.u.BusID)
		case tool == "busctl" && strings.Contains(joined, "GetNameOwner s "):
			return bus("s", f.u.OwnerBus)
		case tool == "busctl" && strings.Contains(joined, "NameHasOwner s "+nmService):
			return bus("b", false)
		case tool == "busctl" && strings.Contains(joined, "NameHasOwner s "+networkdService):
			return bus("b", true)
		case tool == "networkctl" && joined == "--version":
			return "systemd 257 (257)", nil
		case tool == "networkctl" && strings.Contains(joined, "status d0"):
			return `{"Name":"d0","AdministrativeState":"configured","NetworkFile":"/etc/systemd/network/20-d0.network","DNS":[],"SearchDomains":[],"RouteDomains":[]}`, nil
		case tool == "networkctl" && (joined == "reload" || joined == "reconfigure d0"):
			return "", nil
		case tool == "busctl" && (strings.HasSuffix(joined, " Reload") || strings.HasSuffix(joined, " ReconfigureLink i 2")):
			return "", nil
		case tool == "systemctl" && joined == "--root=/ is-enabled systemd-networkd.service":
			return "enabled", nil
		case tool == "ip" && joined == "-j -d link show":
			return `[{"ifname":"d0","ifindex":2,"address":"02:00:00:00:00:14","linkinfo":{"info_kind":"dummy"}}]`, nil
		case tool == "ip" && joined == "-j addr show dev d0":
			file, err := os.ReadFile(nativeHostPath(f.u.Files[0].Before.Path))
			if err != nil {
				return "", err
			}
			address := "192.0.2.14"
			if strings.Contains(string(file), "192.0.2.15") {
				address = "192.0.2.15"
			}
			return fmt.Sprintf(`[{"ifname":"d0","addr_info":[{"family":"inet","local":%q,"prefixlen":24,"scope":"global"}]}]`, address), nil
		case tool == "ip" && strings.Contains(joined, "route show table all dev d0"):
			return "[]", nil
		}
		return "", fmt.Errorf("unexpected native transcript operation: %s %s", tool, joined)
	}
}

func TestNativeConfirmationKeepsRollbackUntilDurableDecision(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	f := newNativeRecoveryFixture(t, "networkd")
	proof, err := f.s.VerifyReconnection(context.Background(), f.j.ID, 7, "session", "192.0.2.17")
	if err != nil {
		t.Fatal(err)
	}
	writer := writeChangeJournal
	writeChangeJournal = func(path string, data []byte, mode os.FileMode) error {
		if strings.Contains(string(data), `"phase": "confirmed"`) {
			return errors.New("fixture confirmation storage failure")
		}
		return writer(path, data, mode)
	}
	if _, err := f.s.ConfirmChange(context.Background(), f.j.ID, 7, "session", proof.Challenge, "192.0.2.17"); err == nil {
		t.Fatal("failed durable confirmation was accepted")
	}
	j, err := readChange(f.s.paths.Dir)
	if err != nil || j.Phase != "awaiting_confirmation" {
		t.Fatalf("previous pending decision was lost: %+v %v", j, err)
	}
	rollback := f.u.Files[0].Before
	rollback.Path, rollback.Identity = f.u.Files[0].RollbackPath, f.u.Files[0].RollbackID
	if err := nativeCurrentFile(rollback, rollback.Identity); err != nil {
		t.Fatalf("confirmation storage failure removed rollback evidence: %v", err)
	}
	writeChangeJournal = writer
	if err := RecoverNetwork(context.Background(), f.s.paths.Dir, f.j.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(nativeHostPath(f.u.Files[0].Before.Path))
	if string(got) != string(f.u.Files[0].Before.Data) {
		t.Fatal("fresh independent recovery did not restore prior native profile")
	}
	restored, err := nativeReadProfile(f.u.Files[0].Before.Path)
	if err != nil || restored.Identity != f.u.Files[0].Before.Identity {
		t.Fatalf("recovery failed to return its retained authored inode: %+v %v", restored, err)
	}
}

func TestNativeConfirmedCleanupRetriesWithoutRollback(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	f := newNativeRecoveryFixture(t, "NetworkManager")
	f.u.CheckpointState = "held"
	if err := saveNativeUndo(f.j, f.u); err != nil {
		t.Fatal(err)
	}
	f.j.Phase, f.j.Watchdog = "confirmed", "completed"
	if err := f.j.save(); err != nil {
		t.Fatal(err)
	}
	checkpointPresent, destroys := true, 0
	nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		bus := func(kind string, value any) (string, error) {
			data := value
			if !strings.Contains(joined, " get-property ") {
				data = []any{value}
			}
			b, err := json.Marshal(map[string]any{"type": kind, "data": data})
			return string(b), err
		}
		switch {
		case tool == "busctl" && strings.HasSuffix(joined, " status"):
			return "BusID=" + f.u.TransportGUID + "\n", nil
		case tool == "busctl" && strings.HasSuffix(joined, " GetId"):
			return bus("s", f.u.BusID)
		case tool == "busctl" && strings.HasSuffix(joined, " GetNameOwner s "+nmService):
			return bus("s", f.u.OwnerBus)
		case tool == "busctl" && strings.HasSuffix(joined, " Checkpoints"):
			list := []string{}
			if checkpointPresent {
				list = append(list, f.u.Checkpoint)
			}
			return bus("ao", list)
		case tool == "busctl" && strings.HasSuffix(joined, " Devices"):
			return bus("ao", []string{f.u.DeviceObject})
		case tool == "busctl" && strings.Contains(joined, "CheckpointDestroy o "):
			durable, err := readChange(f.s.paths.Dir)
			if err != nil || durable.Phase != "confirmed" {
				t.Fatal("checkpoint was destroyed before durable confirmation")
			}
			if _, err := os.Stat(nativeHostPath(f.u.Files[0].RollbackPath)); err != nil {
				t.Fatal("rollback stage vanished before checkpoint release")
			}
			checkpointPresent, destroys = false, destroys+1
			return "", nil
		default:
			t.Fatalf("terminal cleanup attempted native activation/rollback: %s %s", tool, joined)
			return "", nil
		}
	}
	writer := writeChangeJournal
	// Fail every write after release, emulating process death before progress
	// can be saved; the next invocation only has the durable releasing state.
	writeChangeJournal = func(path string, data []byte, mode os.FileMode) error {
		if !checkpointPresent {
			return errors.New("fixture process stopped after owned checkpoint release")
		}
		return writer(path, data, mode)
	}
	if err := RecoverNetwork(context.Background(), f.s.paths.Dir, f.j.ID); err == nil {
		t.Fatal("unsaved checkpoint release did not expose cleanup failure")
	}
	writeChangeJournal = writer
	if err := RecoverNetwork(context.Background(), f.s.paths.Dir, f.j.ID); err != nil {
		t.Fatal(err)
	}
	j, err := readChange(f.s.paths.Dir)
	if err != nil || j.Phase != "confirmed" || j.Cleanup != "complete" || destroys != 1 {
		t.Fatalf("terminal cleanup retry=%+v destroys=%d err=%v", j, destroys, err)
	}
	got, _ := os.ReadFile(nativeHostPath(f.u.Files[0].Before.Path))
	if string(got) != string(f.u.Files[0].Candidate.Data) {
		t.Fatal("confirmed cleanup retry rolled the accepted profile back")
	}
}

func TestNativeForeignStagePreservedAndNextChangesBlocked(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	f := newNativeRecoveryFixture(t, "networkd")
	f.j.Phase, f.j.Watchdog = "confirmed", "completed"
	if err := f.j.save(); err != nil {
		t.Fatal(err)
	}
	stage := nativeHostPath(f.u.Files[0].RollbackPath)
	foreign := []byte("native owner edited the stage in place")
	if err := os.WriteFile(stage, foreign, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverNetwork(context.Background(), f.s.paths.Dir, f.j.ID); err == nil {
		t.Fatal("foreign staged bytes were silently removed")
	}
	j, err := readChange(f.s.paths.Dir)
	if err != nil || j.Phase != "confirmed" || j.Cleanup != "failed" || len(j.RecoveryErrors) == 0 {
		t.Fatalf("foreign cleanup evidence not durable: %+v %v", j, err)
	}
	got, _ := os.ReadFile(stage)
	if string(got) != string(foreign) {
		t.Fatal("foreign stage was changed")
	}
	if _, err := f.s.prepareChange(context.Background(), emptySpec(), nil, nil, []byte("candidate"), nil, nil); err == nil {
		t.Fatal("ordinary change replaced a failed native cleanup journal")
	}
	if _, err := f.s.prepareNativeChange(WithPendingConfirmation(context.Background(), 7), f.u); err == nil {
		t.Fatal("native change replaced a failed cleanup journal")
	}
	still, err := readChange(f.s.paths.Dir)
	if err != nil || still.ID != f.j.ID || still.Cleanup != "failed" {
		t.Fatal("next-change refusal discarded cleanup evidence")
	}
	if err := os.WriteFile(stage, f.u.Files[0].Before.Data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := finishPriorChange(context.Background(), f.s.paths.Dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned cleanup retry did not remove the verified stage: %v", err)
	}
}

func TestNativeUnrecordedTerminalDecisionCannotClean(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	f := newNativeRecoveryFixture(t, "NetworkManager")
	f.j.Phase = "confirmed"
	nativeExecute = func(context.Context, []byte, string, ...string) (string, error) {
		t.Fatal("unrecorded terminal decision reached the native owner")
		return "", nil
	}
	if err := finalizeNativeChange(context.Background(), f.j); err == nil {
		t.Fatal("in-memory confirmation could delete durable pending recovery")
	}
	if _, err := os.Stat(nativeHostPath(f.u.Files[0].RollbackPath)); err != nil {
		t.Fatal("pending rollback stage disappeared")
	}
}

func TestNativeBootRecoveryRebindsExactOwner(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	f := newNativeRecoveryFixture(t, "networkd")
	newBoot := "83847f2b-c249-4bc4-9ae9-57438d273438"
	if err := os.WriteFile(nativeHostPath("/proc/sys/kernel/random/boot_id"), []byte(newBoot), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverNetwork(context.Background(), f.s.paths.Dir, f.j.ID); err != nil {
		t.Fatal(err)
	}
	j, err := readChange(f.s.paths.Dir)
	if err != nil || j.Phase != "recovered" || j.Cleanup != "complete" {
		t.Fatalf("native cold-owner recovery: %+v %v", j, err)
	}
	u, err := nativeJournalUndo(j)
	if err != nil || u.BootID != newBoot {
		t.Fatalf("native boot epoch was not saved before restoration: %+v %v", u, err)
	}
	file, err := nativeReadProfile(u.Files[0].Before.Path)
	if err != nil || string(file.Data) != string(u.Files[0].Before.Data) {
		t.Fatalf("native cold-owner profile was not restored: %+v %v", file, err)
	}
}

func TestNativeCheckpointBusEpochRefusesReusedObjects(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	f := newNativeRecoveryFixture(t, "NetworkManager")
	f.u.CheckpointState = "held"
	if err := saveNativeUndo(f.j, f.u); err != nil {
		t.Fatal(err)
	}
	f.j.Phase, f.j.Watchdog = "confirmed", "completed"
	if err := f.j.save(); err != nil {
		t.Fatal(err)
	}
	nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
		if tool == "busctl" && strings.HasSuffix(strings.Join(args, " "), " GetId") {
			return `{"type":"s","data":["11111111111111111111111111111111"]}`, nil
		}
		t.Fatalf("reused bus object was queried or changed: %s %v", tool, args)
		return "", nil
	}
	if err := finalizeNativeChange(context.Background(), f.j); err == nil {
		t.Fatal("old-bus checkpoint cleanup was accepted")
	}
	j, err := readChange(f.s.paths.Dir)
	if err != nil || j.Phase != "confirmed" || j.Cleanup != "failed" {
		t.Fatalf("old-bus cleanup failure did not preserve its decision: %+v %v", j, err)
	}
	if _, err := nativeReadProfile(f.u.Files[0].RollbackPath); err != nil {
		t.Fatal("old-bus cleanup destroyed rollback evidence")
	}
}

func TestNativeConfirmedBootCleanupNeverUsesOldCheckpoint(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	f := newNativeRecoveryFixture(t, "NetworkManager")
	f.u.CheckpointState = "held"
	if err := saveNativeUndo(f.j, f.u); err != nil {
		t.Fatal(err)
	}
	f.j.Phase, f.j.Watchdog = "confirmed", "completed"
	if err := f.j.save(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nativeHostPath("/proc/sys/kernel/random/boot_id"), []byte("83847f2b-c249-4bc4-9ae9-57438d273438"), 0o600); err != nil {
		t.Fatal(err)
	}
	nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
		t.Fatalf("confirmed old-boot cleanup attempted native execution: %s %v", tool, args)
		return "", nil
	}
	if err := RecoverNetwork(context.Background(), f.s.paths.Dir, f.j.ID); err != nil {
		t.Fatal(err)
	}
	j, err := readChange(f.s.paths.Dir)
	if err != nil || j.Phase != "confirmed" || j.Cleanup != "complete" {
		t.Fatalf("confirmed old-boot cleanup: %+v %v", j, err)
	}
	file, err := nativeReadProfile(f.u.Files[0].Candidate.Path)
	if err != nil || string(file.Data) != string(f.u.Files[0].Candidate.Data) {
		t.Fatal("confirmed old-boot candidate was rolled back")
	}
}

func TestNativeProfileExchangePreservesConcurrentForeignWrite(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	f := newNativeRecoveryFixture(t, "networkd")
	file := f.u.Files[0]
	staged := file.Before
	staged.Path = file.CandidatePath
	if err := nativeExchangeFiles(file.CandidatePath, file.Candidate.Path); err != nil {
		t.Fatal(err)
	}
	staged = file.Candidate
	staged.Path = file.CandidatePath
	exchange := nativeExchangeFiles
	first := true
	foreign := append(append([]byte{}, file.Before.Data...), []byte("# concurrent native-owner edit\n")...)
	nativeExchangeFiles = func(a, b string) error {
		if first {
			first = false
			if err := os.WriteFile(nativeHostPath(b), foreign, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return exchange(a, b)
	}
	if err := nativeReplaceProfile(staged, file.Before); err == nil {
		t.Fatal("concurrent native profile write was accepted")
	}
	current, err := nativeReadProfile(file.Before.Path)
	if err != nil || string(current.Data) != string(foreign) {
		t.Fatal("concurrent foreign profile bytes were overwritten")
	}
	if err := nativeCurrentFile(staged, staged.Identity); err != nil {
		t.Fatal("refused candidate was not retained in its exact stage")
	}
}

func TestNativeStageClaimPreservesConcurrentForeignWrite(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	f := newNativeRecoveryFixture(t, "networkd")
	f.j.Phase, f.j.Watchdog = "confirmed", "completed"
	if err := f.j.save(); err != nil {
		t.Fatal(err)
	}
	file := f.u.Files[0]
	foreign := []byte("foreign concurrent rollback bytes\n")
	claim := nativeClaimStage
	first := true
	nativeClaimStage = func(a, b string) error {
		if a == file.RollbackPath && first {
			first = false
			if err := os.WriteFile(nativeHostPath(a), foreign, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return claim(a, b)
	}
	if err := finalizeNativeChange(context.Background(), f.j); err == nil {
		t.Fatal("concurrent foreign cleanup claim was accepted")
	}
	current, err := nativeReadProfile(file.RollbackPath)
	if err != nil || string(current.Data) != string(foreign) {
		t.Fatal("foreign rollback bytes were deleted or not restored to their name")
	}
	j, err := readChange(f.s.paths.Dir)
	if err != nil || j.Phase != "confirmed" || j.Cleanup != "failed" {
		t.Fatalf("concurrent cleanup failure did not preserve terminal decision: %+v %v", j, err)
	}
}
