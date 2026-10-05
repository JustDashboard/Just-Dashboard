package deploy

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
)

func TestReplicaCaptureRejectsDifferentStorageAndNetworkIdentity(t *testing.T) {
	first, second := adoptionCaptureFixture(t, "web", true), adoptionCaptureFixture(t, "web", true)
	if replicaStorageDigest(first) != replicaStorageDigest(second) || replicaNetworkDigest(first) != replicaNetworkDigest(second) {
		t.Fatal("identical settings are not stable")
	}
	second.Inspection.Mounts[0].Name = "different-private-data"
	if replicaStorageDigest(first) == replicaStorageDigest(second) {
		t.Fatal("replica storage isolation difference was lost")
	}
	second.Inspection.NetworkSettings.Networks["original_default"] = &network.EndpointSettings{MacAddress: "02:42:aa:bb:cc:dd"}
	if replicaNetworkDigest(first) == replicaNetworkDigest(second) {
		t.Fatal("replica endpoint difference was lost")
	}
	second.Inspection.Config.Hostname = "explicit-per-replica-name"
	if containerSettingsDigest(first) == containerSettingsDigest(second) {
		t.Fatal("custom per-replica hostname difference was lost")
	}
}

func TestComposeReleaseIdentityRequiresCapturedServiceDigest(t *testing.T) {
	compose := &ResolvedComposeSnapshot{Services: []ResolvedComposeService{{Digest: fakeContentDigest("first")}, {Digest: fakeContentDigest("second")}}}
	snapshot := runtimeReleaseSnapshot{Version: 1, Plan: RuntimePlanConfig{Strategy: StrategyStopFirst}, Compose: compose}
	raw := mustJSON(snapshot)
	release := &ReleaseWithArtifacts{Release: Release{Strategy: StrategyStopFirst, ConfigDigest: digestBytes(raw), ImageDigest: compose.Services[0].Digest}, Artifacts: []ReleaseArtifact{{Kind: ArtifactRuntimeConfig, State: "available", Digest: digestBytes(raw), Metadata: mustJSON(map[string]any{"snapshot": json.RawMessage(raw)})}}}
	if _, err := decodeReleaseRuntimeSnapshot(release); err != nil {
		t.Fatal("valid Compose release rejected", err)
	}
	release.Release.ImageDigest = fakeContentDigest("not-in-snapshot")
	if _, err := decodeReleaseRuntimeSnapshot(release); !errors.Is(err, ErrInvalidPlan) {
		t.Fatal("foreign image digest accepted", err)
	}
	release.Release.ImageDigest = compose.Services[0].Digest
	release.Release.ConfigDigest = fakeContentDigest("altered-config")
	if _, err := decodeReleaseRuntimeSnapshot(release); !errors.Is(err, ErrArtifactMissing) {
		t.Fatal("altered configuration digest accepted", err)
	}
}

func TestAdoptedContainerRequiresExactBaselineOrReleaseOwnership(t *testing.T) {
	runtime := ReleaseRuntime{EnvironmentID: 12, ReleaseID: 34}
	entry := AdoptedContainer{ID: "original"}
	for _, test := range []struct {
		name      string
		candidate dockerx.Container
		allowed   bool
	}{
		{"original", dockerx.Container{ID: "original"}, true},
		{"empty-identity", dockerx.Container{}, false},
		{"short-identity", dockerx.Container{ID: "orig"}, false},
		{"managed-other-environment", dockerx.Container{ID: "new", Labels: map[string]string{"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": "99", "io.just-dashboard.release-id": "34"}}, false},
		{"managed-other-release", dockerx.Container{ID: "new", Labels: map[string]string{"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": "12", "io.just-dashboard.release-id": "99"}}, false},
		{"exact-owned-replacement", dockerx.Container{ID: "new", Labels: map[string]string{"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": "12", "io.just-dashboard.release-id": "34"}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if adoptedContainerAuthorized(test.candidate, entry, runtime) != test.allowed {
				t.Fatal("ownership authorization mismatch")
			}
		})
	}
}

func TestComposeBaselineScopeRejectsWholeActionBeforeForeignOrAmbiguousReplica(t *testing.T) {
	runtime := ReleaseRuntime{EnvironmentID: 12, ReleaseID: 34}
	baseline := []AdoptedContainer{{ID: "original-web", Service: "web", Number: 1, Running: true}, {ID: "original-worker", Service: "worker", Number: 1}}
	web := dockerx.Container{ID: "original-web", Labels: map[string]string{"com.docker.compose.service": "web", "com.docker.compose.container-number": "1"}}
	worker := dockerx.Container{ID: "foreign-worker", Labels: map[string]string{"com.docker.compose.service": "worker", "com.docker.compose.container-number": "1", "io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": "99", "io.just-dashboard.release-id": "34"}}
	if scope, err := composeBaselineScope([]dockerx.Container{web, worker}, baseline, runtime, true); !errors.Is(err, ErrInvalidPlan) || len(scope) != 0 {
		t.Fatal("authorized first replica escaped a later ownership rejection")
	}
	worker.ID = "original-worker"
	if scope, err := composeBaselineScope([]dockerx.Container{web, worker}, baseline, runtime, false); err != nil || len(scope) != 2 {
		t.Fatal("complete original baseline rejected", err)
	}
	duplicate := worker
	duplicate.ID = "foreign-worker"
	if scope, err := composeBaselineScope([]dockerx.Container{web, worker, duplicate}, baseline, runtime, true); !errors.Is(err, ErrInvalidPlan) || len(scope) != 0 {
		t.Fatal("ambiguous replica identity accepted")
	}
	if _, err := composeBaselineScope([]dockerx.Container{web}, baseline, runtime, false); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatal("missing restored replica accepted")
	}
	if scope, err := composeBaselineScope([]dockerx.Container{web}, baseline, runtime, true); err != nil || len(scope) != 1 {
		t.Fatal("already missing replica prevents stopping remaining original", err)
	}
}

func TestAdoptionOwnershipOverrideIsPrivateImmutableAndContained(t *testing.T) {
	root := t.TempDir()
	runtime := ReleaseRuntime{EnvironmentID: 12, ReleaseID: 34}
	metadata := dockerReleaseRuntimeMetadata{ProjectDirectory: root, BaselineContainers: []AdoptedContainer{{Service: "web", Number: 1}}}
	file, err := finalizedAdoptionOverride(runtime, metadata)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(content), `io.just-dashboard.environment-id: "12"`) || !strings.Contains(string(content), `io.just-dashboard.release-id: "34"`) {
		t.Fatalf("bad ownership override: %s %v", content, err)
	}
	info, _ := os.Stat(file)
	if info.Mode().Perm() != 0o600 {
		t.Fatal("override contains nonprivate permissions")
	}
	if _, err := finalizedAdoptionOverride(runtime, metadata); err != nil {
		t.Fatal("same release cannot reuse immutable ownership", err)
	}
	if err := os.WriteFile(file, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := finalizedAdoptionOverride(runtime, metadata); err == nil {
		t.Fatal("damaged ownership override was replaced")
	}
	outside, inside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(inside, ".just-dashboard")); err != nil {
		t.Fatal(err)
	}
	metadata.ProjectDirectory = inside
	if _, err := finalizedAdoptionOverride(runtime, metadata); err == nil {
		t.Fatal("override escaped its private recovery directory")
	}
}

func TestWritableLayerPermitsOnlyVerifiedAddedRegenerableFiles(t *testing.T) {
	for _, name := range []string{"added-bytecode", "missing-stat", "modified-bytecode", "modified-source", "unknown-ancestor", "unrelated-data", "docker-init", "fake-docker-init", "mount-directory", "mount-file", "mount-child"} {
		t.Run(name, func(t *testing.T) {
			capture := adoptionCaptureFixture(t, "web", true)
			capture.Changes = []container.FilesystemChange{{Path: "/app", Kind: container.ChangeModify}, {Path: "/app/__pycache__", Kind: container.ChangeAdd}, {Path: "/app/__pycache__/server.cpython-311.pyc", Kind: container.ChangeAdd}}
			capture.ChangeModes = map[string]os.FileMode{"/app": os.ModeDir, "/app/__pycache__": os.ModeDir, "/app/__pycache__/server.cpython-311.pyc": 0o644}
			want, cache := name == "added-bytecode", name == "added-bytecode"
			switch name {
			case "missing-stat":
				delete(capture.ChangeModes, "/app/__pycache__/server.cpython-311.pyc")
			case "modified-bytecode":
				capture.Changes[2].Kind = container.ChangeModify
			case "modified-source":
				capture.Changes = append(capture.Changes, container.FilesystemChange{Path: "/app/server.py", Kind: container.ChangeModify})
			case "unknown-ancestor":
				delete(capture.ChangeModes, "/app")
			case "unrelated-data":
				capture.Changes = append(capture.Changes, container.FilesystemChange{Path: "/tmp/db.sqlite", Kind: container.ChangeAdd})
			case "docker-init", "fake-docker-init":
				init := name == "docker-init"
				capture.Inspection.HostConfig.Init = &init
				capture.Changes = []container.FilesystemChange{{Path: "/usr", Kind: container.ChangeModify}, {Path: "/usr/sbin", Kind: container.ChangeModify}, {Path: "/usr/sbin/docker-init", Kind: container.ChangeAdd}}
				capture.ChangeModes = map[string]os.FileMode{"/usr": os.ModeDir, "/usr/sbin": os.ModeDir, "/usr/sbin/docker-init": 0o755}
				want, cache = init, false
			case "mount-directory", "mount-file", "mount-child":
				capture.Changes = []container.FilesystemChange{{Path: "/data", Kind: container.ChangeAdd}}
				capture.ChangeModes = map[string]os.FileMode{"/data": os.ModeDir}
				if name == "mount-file" {
					capture.ChangeModes["/data"] = 0o644
				}
				if name == "mount-child" {
					capture.Changes = append(capture.Changes, container.FilesystemChange{Path: "/data/unowned.db", Kind: container.ChangeAdd})
				}
				want, cache = name == "mount-directory", false
			}
			if actual, regenerated := recoverableWritableLayer(capture); actual != want || regenerated != cache {
				t.Fatalf("safe=%v cache=%v want=%v,%v", actual, regenerated, want, cache)
			}
		})
	}
}
