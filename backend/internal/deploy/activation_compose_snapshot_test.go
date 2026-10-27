package deploy

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// A Compose release keeps no single image of its own; the release row records
// the first of its services' image artifacts. The snapshot has to be accepted
// when that digest is one of its services — every Compose release used to be
// refused here, at its first activation step — and still refused when the
// digest names an image the stack does not run.
func TestComposeReleaseSnapshotMatchesTheImageItsReleaseRecords(t *testing.T) {
	db := "sha256:" + strings.Repeat("d", 64)
	app := "sha256:" + strings.Repeat("a", 64)
	snapshot := runtimeReleaseSnapshot{Version: 1, Plan: RuntimePlanConfig{Strategy: StrategyStopFirst},
		Compose: &ResolvedComposeSnapshot{PrimaryService: "wordpress", Services: []ResolvedComposeService{
			{Plan: ComposeServicePlan{Name: "db"}, Reference: "docker.io/library/mariadb", Digest: db},
			{Plan: ComposeServicePlan{Name: "wordpress"}, Reference: "docker.io/library/wordpress", Digest: app},
		}}}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	release := func(imageDigest string) *ReleaseWithArtifacts {
		digest := digestBytes(raw)
		return &ReleaseWithArtifacts{
			Release: Release{Strategy: StrategyStopFirst, ImageDigest: imageDigest, ConfigDigest: digest},
			Artifacts: []ReleaseArtifact{{Kind: ArtifactRuntimeConfig, State: "available", Digest: digest,
				Metadata: mustJSON(map[string]json.RawMessage{"snapshot": raw})}},
		}
	}
	for _, digest := range []string{db, app, ""} {
		if _, err := decodeReleaseRuntimeSnapshot(release(digest)); err != nil {
			t.Fatalf("a Compose release recording %q is refused: %v", digest, err)
		}
	}
	if _, err := decodeReleaseRuntimeSnapshot(release("sha256:" + strings.Repeat("e", 64))); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("a Compose release recording an image it does not run: %v", err)
	}
	single := snapshot
	single.Compose, single.Image = nil, ResolvedImage{Digest: app}
	if !snapshotNamesReleaseImage(single, app) || snapshotNamesReleaseImage(single, db) {
		t.Fatal("a container release's identity check changed")
	}
}
