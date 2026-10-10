package netx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func driftRepairHost(t *testing.T, sp *Spec) *gwHost {
	t.Helper()
	h := pendingHost(t)
	driftSave(t, h.Service, sp)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(filepath.Join(h.paths.Dir, recoveryBinary), data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(filepath.Join(filepath.Dir(h.paths.Unit), "just-dashboard-network-recovery.service"), []byte(renderRecoveryUnit(h.paths)), 0o644); err != nil {
		t.Fatal(err)
	}
	h.rec.on("systemctl show", "LoadState=loaded\nUnitFileState=enabled\nActiveState=active\nFragmentPath="+h.paths.Unit+"\nDropInPaths=\nNeedDaemonReload=no\nExecMainStartTimestampMonotonic=0\n")
	h.first("systemctl show just-dashboard-network-recovery.service", "LoadState=loaded\nUnitFileState=enabled\nFragmentPath="+filepath.Join(filepath.Dir(h.paths.Unit), "just-dashboard-network-recovery.service")+"\nDropInPaths=\nNeedDaemonReload=no\n", nil)
	return h
}

func driftRepairItem(t *testing.T, r DriftReport, resource string) OwnedRepair {
	t.Helper()
	for _, item := range r.RepairPlan.Items {
		if item.Resource == resource {
			return item
		}
	}
	t.Fatalf("missing repair for %s: %+v", resource, r.RepairPlan)
	return OwnedRepair{}
}

func driftRepairRequest(r DriftReport, items ...OwnedRepair) DriftRepairRequest {
	req := DriftRepairRequest{Generation: r.SavedGeneration}
	for _, item := range items {
		req.Selections = append(req.Selections, DriftRepairSelection{ID: item.ID, ReviewToken: item.ReviewToken})
	}
	return req
}

func driftDamage(t *testing.T, path string, mode os.FileMode) []byte {
	t.Helper()
	data := []byte(generatedHeader + "# another owned render\n")
	if err := writeFileAtomic(path, data, mode); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDriftRepairRequiresPendingBeforeReadingOrCreatingMetadata(t *testing.T) {
	for _, owner := range []int64{0, -1} {
		t.Run(fmt.Sprint(owner), func(t *testing.T) {
			h := newGwHost(t)
			ctx := context.Background()
			if owner != 0 {
				ctx = WithPendingConfirmation(ctx, owner)
			}
			_, err := h.RepairDrift(ctx, DriftRepairRequest{Generation: strings.Repeat("a", 64), Selections: []DriftRepairSelection{{ID: "reviewed", ReviewToken: "token"}}}, "127.0.0.1")
			var confirmation *ConfirmationError
			if !errors.As(err, &confirmation) || len(h.rec.calls) != 0 {
				t.Fatalf("immediate repair reached native inspection: %v, %v", err, h.rec.calls)
			}
			for _, name := range []string{".change.lock", recoveryFile} {
				if _, err := os.Lstat(filepath.Join(h.paths.Dir, name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("immediate repair created %s: %v", name, err)
				}
			}
		})
	}
}

func TestDriftRepairWritesOnlySelectedOwnedRenderAndPreservesGenerationAndMode(t *testing.T) {
	h := driftRepairHost(t, emptySpec())
	path := filepath.Join(h.paths.Dir, linksFile)
	driftDamage(t, path, 0o640)
	unselected := filepath.Join(h.paths.Dir, rules6File)
	foreign := []byte("# Native manager owns this file\n")
	if err := os.WriteFile(unselected, foreign, 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(h.specPath())
	r := h.Drift(context.Background())
	item := driftRepairItem(t, r, path)
	if !item.Executable || !r.RepairPlan.Executable || item.Before == item.After || item.ReviewToken == "" {
		t.Fatalf("unreviewable item: %+v", item)
	}
	j, err := h.RepairDrift(WithPendingConfirmation(context.Background(), 7), driftRepairRequest(r, item), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if j.Phase != "awaiting_confirmation" || j.Persistence != "written" || j.Runtime != "not_applied" || j.Boot != "not_verified" || j.Generation != r.SavedGeneration {
		t.Fatalf("wrong phase evidence: %+v", j)
	}
	after, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(after) != item.After || info.Mode().Perm() != 0o640 {
		t.Fatal("render bytes or mode not preserved")
	}
	afterSpec, _ := os.ReadFile(h.specPath())
	afterForeign, _ := os.ReadFile(unselected)
	if string(afterSpec) != string(before) || string(afterForeign) != string(foreign) {
		t.Fatal("repair changed an unselected file or saved generation")
	}
	journal, _ := readChange(h.paths.Dir)
	if len(journal.Files) != 1 || journal.Files[0].Path != path || len(journal.Commands) != 0 {
		t.Fatalf("overbroad undo: %+v", journal)
	}
}

func TestDriftRepairRepairsTheBootOwnerLastAndReloadsOnlyItsSelection(t *testing.T) {
	h := driftRepairHost(t, emptySpec())
	links, rules := filepath.Join(h.paths.Dir, linksFile), filepath.Join(h.paths.Dir, rules6File)
	for _, path := range []string{links, rules, h.paths.Unit} {
		driftDamage(t, path, 0o640)
	}
	r := h.Drift(context.Background())
	selected := []OwnedRepair{driftRepairItem(t, r, h.paths.Unit), driftRepairItem(t, r, links), driftRepairItem(t, r, rules)}
	priorReloads := 0
	for _, command := range h.rec.commands() {
		if command == "systemctl daemon-reload" {
			priorReloads++
		}
	}
	status, err := h.RepairDrift(WithPendingConfirmation(context.Background(), 7), driftRepairRequest(r, selected...), "127.0.0.1")
	if err != nil || status.Phase != "awaiting_confirmation" || status.Boot != "not_verified" {
		t.Fatalf("selected file repair failed or invented boot execution: %+v %v", status, err)
	}
	j, err := readChange(h.paths.Dir)
	if err != nil || len(j.Files) != 3 || j.Files[2].Path != h.paths.Unit {
		t.Fatalf("boot owner was not the final selected file: %+v %v", j, err)
	}
	for _, item := range selected {
		data, err := os.ReadFile(item.Resource)
		info, statErr := os.Stat(item.Resource)
		if err != nil || statErr != nil || string(data) != item.After || info.Mode().Perm() != 0o640 {
			t.Fatalf("wrong selected content or mode for %s", item.Resource)
		}
	}
	reloads := 0
	for _, command := range h.rec.commands() {
		if command == "systemctl daemon-reload" {
			reloads++
		}
	}
	if reloads != priorReloads+1 {
		t.Fatalf("selected boot owner did not cause exactly one verified reload: %v", h.rec.commands())
	}
}

func TestDriftRepairRefusesStaleGenerationAndUnresolvedJournalBeforeHostCommands(t *testing.T) {
	for _, phase := range []string{"stale", "awaiting_confirmation", "degraded"} {
		t.Run(phase, func(t *testing.T) {
			h := driftRepairHost(t, emptySpec())
			path := filepath.Join(h.paths.Dir, linksFile)
			before := driftDamage(t, path, 0o640)
			r := h.Drift(context.Background())
			req := driftRepairRequest(r, driftRepairItem(t, r, path))
			if phase == "stale" {
				req.Generation = strings.Repeat("a", 64)
			} else {
				j := &changeJournal{Paths: h.paths, ChangeStatus: ChangeStatus{ID: "pending", Generation: r.SavedGeneration, Phase: phase}}
				if err := j.save(); err != nil {
					t.Fatal(err)
				}
			}
			count := len(h.rec.commands())
			if _, err := h.RepairDrift(WithPendingConfirmation(context.Background(), 7), req, "127.0.0.1"); err == nil {
				t.Fatal("unsafe precondition accepted")
			}
			if len(h.rec.commands()) != count {
				t.Fatal("host command ran after an early refusal")
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(before) {
				t.Fatal("refusal wrote a render")
			}
		})
	}
}

func TestDriftRepairRefusesForeignSymlinkAndChangedModeAfterReview(t *testing.T) {
	for _, replacement := range []string{"foreign", "symlink", "mode", "inode"} {
		t.Run(replacement, func(t *testing.T) {
			h := driftRepairHost(t, emptySpec())
			path := h.paths.Unit
			driftDamage(t, path, 0o640)
			r := h.Drift(context.Background())
			req := driftRepairRequest(r, driftRepairItem(t, r, path))
			switch replacement {
			case "foreign":
				if err := os.WriteFile(path, []byte("[Unit]\nDescription=Foreign\n"), 0o640); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(h.paths.Dir, linksFile), path); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(path, 0o600); err != nil {
					t.Fatal(err)
				}
			case "inode":
				before, _ := os.ReadFile(path)
				if err := writeFileAtomic(path, before, 0o640); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := h.RepairDrift(WithPendingConfirmation(context.Background(), 7), req, "127.0.0.1"); err == nil {
				t.Fatal("replacement was overwritten")
			}
			if h.rec.ran("systemd-run") {
				t.Fatal("refusal armed an apply timer")
			}
		})
	}
}

func TestDriftRepairRestoresAttemptedRenameWhenDirectorySyncFails(t *testing.T) {
	h := driftRepairHost(t, emptySpec())
	path := filepath.Join(h.paths.Dir, linksFile)
	before := driftDamage(t, path, 0o640)
	r := h.Drift(context.Background())
	prior := writeSelectedDriftFile
	writeSelectedDriftFile = func(p string, data []byte, mode os.FileMode, beforeRename func(string) error) error {
		if err := prior(p, data, mode, beforeRename); err != nil {
			return err
		}
		if p == path {
			return errors.New("directory sync failed after rename")
		}
		return nil
	}
	t.Cleanup(func() { writeSelectedDriftFile = prior })
	status, err := h.RepairDrift(WithPendingConfirmation(context.Background(), 7), driftRepairRequest(r, driftRepairItem(t, r, path)), "127.0.0.1")
	if err == nil || status.Phase != "recovered" {
		t.Fatalf("failed write reported success: %+v %v", status, err)
	}
	after, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(after) != string(before) || info.Mode().Perm() != 0o640 {
		t.Fatal("attempted file or mode was not restored")
	}
}

func TestDriftRepairPreservesAnUnattemptedForeignReplacementAfterWatchdogPreflight(t *testing.T) {
	h := driftRepairHost(t, emptySpec())
	path := filepath.Join(h.paths.Dir, linksFile)
	driftDamage(t, path, 0o640)
	r := h.Drift(context.Background())
	previousRun := run
	foreign := []byte("# New foreign owner\n")
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		out, err := previousRun(ctx, name, args...)
		if name == "systemd-run" {
			if writeErr := os.WriteFile(path, foreign, 0o640); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		return out, err
	}
	t.Cleanup(func() { run = previousRun })
	status, err := h.RepairDrift(WithPendingConfirmation(context.Background(), 7), driftRepairRequest(r, driftRepairItem(t, r, path)), "127.0.0.1")
	if err == nil || status.Phase != "recovered" {
		t.Fatalf("changed precondition accepted: %+v %v", status, err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(foreign) {
		t.Fatal("rollback overwrote an unattempted replacement")
	}
}

func TestDriftRepairRefusesChangedBootOwnerIdentityImmediatelyBeforeItsEffect(t *testing.T) {
	h := driftRepairHost(t, emptySpec())
	path := filepath.Join(h.paths.Dir, linksFile)
	before := driftDamage(t, path, 0o640)
	r := h.Drift(context.Background())
	prior := writeSelectedDriftFile
	writeSelectedDriftFile = func(p string, data []byte, mode os.FileMode, beforeRename func(string) error) error {
		unit, err := os.ReadFile(h.paths.Unit)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(h.paths.Unit, unit, 0o644); err != nil {
			return err
		}
		return prior(p, data, mode, beforeRename)
	}
	t.Cleanup(func() { writeSelectedDriftFile = prior })
	status, err := h.RepairDrift(WithPendingConfirmation(context.Background(), 7), driftRepairRequest(r, driftRepairItem(t, r, path)), "127.0.0.1")
	if err == nil || status.Phase != "recovered" || !strings.Contains(err.Error(), "boot owner") {
		t.Fatalf("new boot owner identity was accepted: %+v %v", status, err)
	}
	after, _ := os.ReadFile(path)
	j, err := readChange(h.paths.Dir)
	if string(after) != string(before) || err != nil || len(j.Files) != 0 {
		t.Fatalf("unattempted file changed or entered undo coverage: %+v %v", j, err)
	}
}

func TestDriftRepairAdmissionOnlyUsesRuntimePhasesAndSelectedChainUndo(t *testing.T) {
	sp := emptySpec()
	sp.Forwards = []ForwardSpec{{ID: 1, Name: "web", Protocol: "tcp", Ports: "8080", Target: "10.0.0.5", TargetPort: "80", SourceNAT: "never", Enabled: true}}
	h := driftRepairHost(t, sp)
	previousRun := run
	inserted := false
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "iptables" && len(args) > 1 && args[1] == "FORWARD" {
			switch args[0] {
			case "-S":
				if inserted {
					return "-P FORWARD DROP\n-A FORWARD " + strings.Join(admissionRule(), " ") + "\n-A FORWARD -j foreign\n", nil
				}
				return "-P FORWARD DROP\n-A FORWARD -j foreign\n", nil
			case "-C":
				if !inserted {
					return "Bad rule", errors.New("missing rule")
				}
			case "-I":
				inserted = true
			}
		}
		return previousRun(ctx, name, args...)
	}
	t.Cleanup(func() { run = previousRun })
	r := h.Drift(context.Background())
	item := driftRepairItem(t, r, "inet/FORWARD")
	status, err := h.RepairDrift(WithPendingConfirmation(context.Background(), 7), driftRepairRequest(r, item), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !inserted || status.Phase != "awaiting_confirmation" || status.Runtime != "applied" || status.Persistence != "not_applicable" || status.Boot != "not_applicable" {
		t.Fatalf("wrong admission phase evidence: %+v", status)
	}
	j, _ := readChange(h.paths.Dir)
	if len(j.Files) != 0 || len(j.Commands) != admissionDeleteCap {
		t.Fatalf("unexpected snapshots: %+v", j)
	}
	for _, c := range j.Commands {
		if c.Tool != "iptables" || len(c.Args) < 2 || c.Args[1] != "FORWARD" {
			t.Fatalf("unselected chain in undo: %+v", c)
		}
	}
	for _, cmd := range h.rec.commands() {
		if strings.Contains(cmd, " -I INPUT ") || strings.Contains(cmd, " -D INPUT ") || strings.Contains(cmd, " -I DOCKER-USER ") || strings.Contains(cmd, " -D DOCKER-USER ") || strings.HasPrefix(cmd, "ip6tables -I") {
			t.Fatalf("unselected chain mutation: %s", cmd)
		}
	}
}

func TestDriftRepairAdmissionAtTheRecoveryBoundRemainsReviewOnly(t *testing.T) {
	sp := emptySpec()
	sp.Forwards = []ForwardSpec{{ID: 1, Name: "web", Protocol: "tcp", Ports: "8080", Target: "10.0.0.5", TargetPort: "80", SourceNAT: "never", Enabled: true}}
	h := driftRepairHost(t, sp)
	previousRun := run
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "iptables" && len(args) > 1 && args[1] == "FORWARD" {
			switch args[0] {
			case "-S":
				return "-P FORWARD DROP\n" + strings.Repeat("-A FORWARD "+strings.Join(admissionRule(), " ")+"\n", admissionDeleteCap), nil
			case "-C":
				return "", nil
			}
		}
		return previousRun(ctx, name, args...)
	}
	t.Cleanup(func() { run = previousRun })
	r := h.Drift(context.Background())
	item := driftRepairItem(t, r, "inet/FORWARD")
	if item.Executable || !strings.Contains(item.Blocker, "bounded") {
		t.Fatalf("unrecoverable duplicate count was executable: %+v", item)
	}
	if _, err := h.RepairDrift(WithPendingConfirmation(context.Background(), 7), driftRepairRequest(r, item), "127.0.0.1"); err == nil || h.rec.ran("systemd-run") {
		t.Fatal("non-executable duplicate count armed a mutation")
	}
}

func TestDriftRepairGatewayCacheCandidateInvalidatesReviewedRender(t *testing.T) {
	sp := emptySpec()
	sp.Blocklists = []BlocklistSpec{{ID: 1, Name: "feed", Kind: "feed", Enabled: true, Count: 1}}
	h := pendingHost(t)
	cache := filepath.Join(h.paths.Dir, "lists", "1.txt")
	if err := writeFileAtomic(cache, []byte("203.0.113.0/24\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	// Reuse the same service paths rather than the global cache directory.
	driftSave(t, h.Service, emptySpec())
	rtSaveSpec(t, h.Service, sp)
	executable, _ := os.Executable()
	data, _ := os.ReadFile(executable)
	if err := writeFileAtomic(filepath.Join(h.paths.Dir, recoveryBinary), data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(filepath.Join(filepath.Dir(h.paths.Unit), "just-dashboard-network-recovery.service"), []byte(renderRecoveryUnit(h.paths)), 0o644); err != nil {
		t.Fatal(err)
	}
	h.rec.on("systemctl show", "LoadState=not-found\nActiveState=inactive\n")
	h.first("systemctl show just-dashboard-network-recovery.service", "LoadState=loaded\nUnitFileState=enabled\nFragmentPath="+filepath.Join(filepath.Dir(h.paths.Unit), "just-dashboard-network-recovery.service")+"\nDropInPaths=\nNeedDaemonReload=no\n", nil)
	path := filepath.Join(h.paths.Dir, gatewayFile)
	before := driftDamage(t, path, 0o640)
	r := h.Drift(context.Background())
	req := driftRepairRequest(r, driftRepairItem(t, r, path))
	if err := writeFileAtomic(cache, []byte("198.51.100.0/24\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := h.RepairDrift(WithPendingConfirmation(context.Background(), 7), req, "127.0.0.1"); err == nil {
		t.Fatal("new fetched candidate activated under an old review")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("stale gateway render was written")
	}
}

func TestDriftRepairBlocksExecutingOrForeignBootOwnerAndIncompleteRecoveryEvidence(t *testing.T) {
	for _, condition := range []string{"executing", "foreign", "incomplete_recovery"} {
		t.Run(condition, func(t *testing.T) {
			h := driftRepairHost(t, emptySpec())
			path := filepath.Join(h.paths.Dir, linksFile)
			before := driftDamage(t, path, 0o640)
			r := h.Drift(context.Background())
			req := driftRepairRequest(r, driftRepairItem(t, r, path))
			switch condition {
			case "executing":
				h.first("systemctl show "+UnitName, "LoadState=loaded\nUnitFileState=enabled\nActiveState=activating\nFragmentPath="+h.paths.Unit+"\nDropInPaths=\nNeedDaemonReload=no\n", nil)
			case "foreign":
				if err := os.WriteFile(h.paths.Unit, []byte("[Unit]\nDescription=Foreign\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "incomplete_recovery":
				h.first("systemctl show just-dashboard-network-recovery.service", "LoadState=loaded\nUnitFileState=enabled\nFragmentPath="+filepath.Join(filepath.Dir(h.paths.Unit), "just-dashboard-network-recovery.service")+"\nNeedDaemonReload=no\n", nil)
			}
			if _, err := h.RepairDrift(WithPendingConfirmation(context.Background(), 7), req, "127.0.0.1"); err == nil {
				t.Fatal("unsafe owner or execution accepted")
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(before) || h.rec.ran("systemd-run") {
				t.Fatal("refusal changed a boot input or armed effects")
			}
		})
	}
}

func TestDriftRepairRefusesSymlinkedLockBeforeOpeningForeignMetadata(t *testing.T) {
	h := driftRepairHost(t, emptySpec())
	path := filepath.Join(h.paths.Dir, linksFile)
	driftDamage(t, path, 0o640)
	r := h.Drift(context.Background())
	req := driftRepairRequest(r, driftRepairItem(t, r, path))
	foreign := filepath.Join(t.TempDir(), "foreign-lock")
	if err := os.WriteFile(foreign, []byte("foreign"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, filepath.Join(h.paths.Dir, ".change.lock")); err != nil {
		t.Fatal(err)
	}
	count := len(h.rec.commands())
	if _, err := h.RepairDrift(WithPendingConfirmation(context.Background(), 7), req, "127.0.0.1"); err == nil {
		t.Fatal("symlinked serialization owner accepted")
	}
	if len(h.rec.commands()) != count {
		t.Fatal("metadata refusal reached host commands")
	}
}

func TestDriftAdmissionIncompleteAndDisagreeingReadingsCannotAuthorizeRepair(t *testing.T) {
	for _, listing := range []string{"", "garbage", "-P FORWARD ACCEPT\n"} {
		t.Run(listing, func(t *testing.T) {
			sp := emptySpec()
			sp.Forwards = []ForwardSpec{{ID: 1, Name: "web", Protocol: "tcp", Ports: "8080", Target: "10.0.0.5", TargetPort: "80", SourceNAT: "never", Enabled: true}}
			h := driftRepairHost(t, sp)
			h.first("iptables -S FORWARD", listing, nil)
			r := h.Drift(context.Background())
			o := driftFind(t, r.Runtime, "admission", "inet/FORWARD")
			if o.Repairable || o.Status == "matching" || o.Status == "missing" || o.Status == "drift" {
				t.Fatalf("incomplete evidence authorized reconciliation: %+v", o)
			}
		})
	}
}

func TestDriftRepairReadinessHashesAnExecutableSizedHelper(t *testing.T) {
	h := driftRepairHost(t, emptySpec())
	if reason := h.driftRecoveryReady(context.Background()); reason != "" {
		t.Fatalf("installed helper refused: %s", reason)
	}
	// Past the render limit the helper must still be read and compared with
	// the running backend, not reported as missing.
	helper := filepath.Join(h.paths.Dir, recoveryBinary)
	if err := os.Truncate(helper, maxRecoveryJournalBytes+1<<20); err != nil {
		t.Fatal(err)
	}
	if reason := h.driftRecoveryReady(context.Background()); reason != "The independent recovery helper's current backend ownership cannot be established." {
		t.Fatalf("executable-sized helper misread: %s", reason)
	}
}
