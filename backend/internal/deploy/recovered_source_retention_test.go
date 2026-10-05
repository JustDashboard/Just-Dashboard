package deploy

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func retainedSourceSnapshot(t *testing.T, root, marker string) (string, string) {
	t.Helper()
	digest, tree, err := stageRecoveredSource(t.Context(), root, func(tree string) error {
		return os.WriteFile(filepath.Join(tree, "marker.txt"), []byte(marker), 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return digest, filepath.Dir(tree)
}

func expireSourceSnapshot(t *testing.T, directory string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, "last-used"), []byte(strconv.FormatInt(time.Now().Add(-31*24*time.Hour).Unix(), 10)), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveredSnapshotRetentionKeepsDraftsAndEverySourceRevision(t *testing.T) {
	fixture := newPlanningStoreFixture(t)
	fixture.now = time.Now().UTC()
	root := t.TempDir()
	expiredDigest, expired := retainedSourceSnapshot(t, root, "expired draft")
	_, fresh := retainedSourceSnapshot(t, root, "fresh orphan")
	draftDigest, draftRoot := retainedSourceSnapshot(t, root, "unexpired draft")
	releaseDigest, releaseRoot := retainedSourceSnapshot(t, root, "stored source revision")
	createDraft := func(name, digest string) *Draft {
		t.Helper()
		recovered := recoveredStoreFixture(t)
		recovered.Adoption.BaselineDetection = recovered.Detection
		recovered.Source.Mode, recovered.Source.ResourceID = SourceModeRecoveredSnapshot, digest
		draft, err := fixture.plans.CreateRecoveredDraft(t.Context(), 41, "operator", DraftIntentConfig{Name: name, Profile: ProfileCompose}, recovered)
		if err != nil {
			t.Fatal(err)
		}
		return draft
	}
	expiredDraft := createDraft("expired-snapshot", expiredDigest)
	if _, err := fixture.store.DB.Exec(`UPDATE deploy_drafts SET expires_at=? WHERE id=?`, time.Now().Add(-time.Hour).Unix(), expiredDraft.ID); err != nil {
		t.Fatal(err)
	}
	createDraft("retained-draft", draftDigest)
	draft := checkRecoveredDraft(t, fixture, createDraft("retained-release", releaseDigest))
	ack := []string{}
	for _, finding := range draft.Findings {
		if finding.Severity == PreflightWarning {
			ack = append(ack, finding.Code)
		}
	}
	if _, err := fixture.plans.Commit(t.Context(), draft.ID, 41, false, DraftCommitRequest{Revision: draft.Revision, AcknowledgedWarnings: ack}); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{expired, draftRoot, releaseRoot} {
		expireSourceSnapshot(t, directory)
	}
	outside := t.TempDir()
	writeBuildFixture(t, outside, "marker", "must remain")
	symlink := filepath.Join(root, "sources", strings.Repeat("e", 64))
	if err := os.Symlink(outside, symlink); err != nil {
		t.Fatal(err)
	}
	if err := fixture.plans.PruneRecoveredSnapshots(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Fatal("expired unreferenced source was retained")
	}
	for _, directory := range []string{fresh, draftRoot, releaseRoot, outside} {
		if _, err := os.Stat(directory); err != nil {
			t.Fatal("a referenced, fresh or foreign source was removed")
		}
	}
}

func TestRecoveredSnapshotReuseRefreshesRetentionAndDetectsTampering(t *testing.T) {
	fixture := newPlanningStoreFixture(t)
	fixture.now = time.Now().UTC()
	root := t.TempDir()
	digest, directory := retainedSourceSnapshot(t, root, "same source")
	expireSourceSnapshot(t, directory)
	again, same := retainedSourceSnapshot(t, root, "same source")
	if again != digest || same != directory {
		t.Fatal("same content did not reuse its immutable snapshot")
	}
	if err := fixture.plans.PruneRecoveredSnapshots(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), nil, nil).WithRecoveryRoot(root)
	if _, err := analyzer.recoveredSnapshotRoot(context.Background(), digest); err != nil {
		t.Fatal("reused snapshot was removed")
	}
	if err := os.WriteFile(filepath.Join(directory, "tree", "marker.txt"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := analyzer.recoveredSnapshotRoot(t.Context(), digest); err == nil {
		t.Fatal("tampered immutable source was accepted")
	}
}
