package netx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDriftRepairFreshRecoveryPreservesAnAttemptedReplacement(t *testing.T) {
	for _, identical := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreign_contents", true: "identical_new_inode"}[identical], func(t *testing.T) {
			h := driftRepairHost(t, emptySpec())
			path := filepath.Join(h.paths.Dir, linksFile)
			driftDamage(t, path, 0o640)
			driftDamage(t, filepath.Join(h.paths.Dir, rules6File), 0o640)
			invoke := driftRepairFixture(h.paths)
			out, err := invoke("apply").CombinedOutput()
			if !driftRepairFixtureDied(err) {
				t.Fatalf("fixture did not die after its selected write: %v %s", err, out)
			}
			foreign := []byte("# A native owner replaced the attempted file\n")
			if identical {
				foreign, err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := writeFileAtomic(path, foreign, 0o640); err != nil {
				t.Fatal(err)
			}
			if out, err := invoke("recover").CombinedOutput(); err == nil || !strings.Contains(string(out), "native contents were preserved") {
				t.Fatalf("replacement recovery did not refuse: %v %s", err, out)
			}
			after, _ := os.ReadFile(path)
			j, err := readChange(h.paths.Dir)
			if string(after) != string(foreign) || err != nil || j.Phase != "degraded" || len(j.RecoveryErrors) == 0 {
				t.Fatalf("foreign replacement or degraded evidence was lost: %+v %v", j, err)
			}
		})
	}
}

func TestDriftRepairRestorationRetryRecognizesItsStagedInode(t *testing.T) {
	rec := record(t)
	dir := t.TempDir()
	path := filepath.Join(dir, linksFile)
	before := []byte(generatedHeader + "# Prior owned render\n")
	if err := writeFileAtomic(path, before, 0o640); err != nil {
		t.Fatal(err)
	}
	old, err := driftSnapshotFile(path)
	if err != nil {
		t.Fatal(err)
	}
	j := &changeJournal{Paths: Paths{Dir: dir}, SelectedDriftRepair: true, ChangeStatus: ChangeStatus{ID: "selected", Generation: strings.Repeat("a", 64), Phase: "prepared", Persistence: "not_written", Runtime: "not_applied", Boot: "not_verified"}}
	candidate := []byte(generatedHeader + "# Candidate owned render\n")
	if err := stageSelectedDriftFile(path, candidate, old.perm, func(identity string) error {
		j.Files = []recoverySnapshot{{Path: path, Data: before, Mode: old.perm, Exists: true, BeforeIdentity: old.identity, CandidateIdentity: identity, CandidateSHA256: digestBytes(candidate), CandidateMode: old.perm}}
		return j.save()
	}); err != nil {
		t.Fatal(err)
	}
	prior := syncNetworkDirectory
	syncNetworkDirectory = func(string) error { return errors.New("injected restore directory sync failure") }
	t.Cleanup(func() { syncNetworkDirectory = prior })
	if err := recoverChange(context.Background(), j); err == nil || j.Phase != "degraded" || j.Files[0].RestoredIdentity == "" {
		t.Fatalf("restore sync failure lost durable identity or became terminal: %+v %v", j, err)
	}
	syncNetworkDirectory = prior
	if err := RecoverNetwork(context.Background(), dir, "selected"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	j, err = readChange(dir)
	if err != nil || j.Phase != "recovered" || string(after) != string(before) || len(rec.commands()) != 0 {
		t.Fatalf("fresh journal retry failed or reached an untouched unit: %+v %v %v", j, err, rec.commands())
	}
}

func TestDriftRepairRecoveryDoesNotReloadAReplacedUnit(t *testing.T) {
	rec := record(t)
	dir := t.TempDir()
	path := filepath.Join(dir, UnitName)
	before := []byte(generatedHeader + "# Original owned unit\n")
	if err := writeFileAtomic(path, before, 0o640); err != nil {
		t.Fatal(err)
	}
	old, err := driftSnapshotFile(path)
	if err != nil {
		t.Fatal(err)
	}
	j := &changeJournal{Paths: Paths{Dir: dir, Unit: path}, SelectedDriftRepair: true, ChangeStatus: ChangeStatus{ID: "selected", Generation: strings.Repeat("a", 64), Phase: "prepared"}}
	candidate := []byte(generatedHeader + "# Candidate owned unit\n")
	if err := stageSelectedDriftFile(path, candidate, old.perm, func(identity string) error {
		j.Files = []recoverySnapshot{{Path: path, Data: before, Mode: old.perm, Exists: true, BeforeIdentity: old.identity, CandidateIdentity: identity, CandidateSHA256: digestBytes(candidate), CandidateMode: old.perm}}
		return j.save()
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, candidate, old.perm); err != nil {
		t.Fatal(err)
	}
	if err := RecoverNetwork(context.Background(), dir, "selected"); err == nil {
		t.Fatal("foreign unit replacement was restored")
	}
	j, err = readChange(dir)
	if err != nil || j.Phase != "degraded" || len(rec.commands()) != 0 {
		t.Fatalf("replacement caused a reload or lost degraded evidence: %+v %v %v", j, err, rec.commands())
	}
}

func TestDriftRepairRecoveryRefusesWiderJournalScope(t *testing.T) {
	for _, invalid := range []string{"boot_dependency", "command", "spec", "cache", "identity"} {
		t.Run(invalid, func(t *testing.T) {
			rec := record(t)
			dir := t.TempDir()
			path := filepath.Join(dir, linksFile)
			before := []byte("Native bytes must remain\n")
			if err := os.WriteFile(path, before, 0o640); err != nil {
				t.Fatal(err)
			}
			j := &changeJournal{Paths: Paths{Dir: dir}, SelectedDriftRepair: true, ChangeStatus: ChangeStatus{Phase: "prepared"}}
			switch invalid {
			case "boot_dependency":
				j.BootDependencies = []recoveryCommand{{Tool: "ip", Args: []string{"link", "add", "unselected0", "type", "dummy"}}}
			case "command":
				j.Commands = []recoveryCommand{{Tool: "iptables", Args: []string{"-F", "FORWARD"}}}
			case "spec":
				j.Files = []recoverySnapshot{{Path: filepath.Join(dir, "spec.json")}}
			case "cache":
				j.Files = []recoverySnapshot{{Path: filepath.Join(dir, "blocklist-1.txt")}}
			case "identity":
				j.Files = []recoverySnapshot{{Path: path}}
			}
			if err := recoverChange(context.Background(), j); err == nil || j.Phase != "prepared" {
				t.Fatalf("invalid selected scope was accepted: %+v %v", j, err)
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(before) || len(rec.commands()) != 0 {
				t.Fatal("invalid scope changed a file or ran a host command")
			}
		})
	}
}
