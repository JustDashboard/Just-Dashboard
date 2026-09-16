package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeImagesAndComposeOverridesUseImmutableIdentities(t *testing.T) {
	configDigest := fakeContentDigest("local-image-config")
	manifestDigest := fakeContentDigest("registry-manifest")
	if got := immutableRuntimeImage(ResolvedImage{
		Reference: "registry.example/app:latest", Digest: manifestDigest, ConfigDigest: configDigest,
	}); got != configDigest {
		t.Fatalf("local immutable image = %q, want config digest", got)
	}
	if got := immutableRuntimeImage(ResolvedImage{
		Reference: "registry.example/app:latest", Digest: manifestDigest,
	}); got != "registry.example/app:latest@"+manifestDigest {
		t.Fatalf("remote immutable image = %q", got)
	}

	request := CandidateRuntimeRequest{
		Run: EngineRun{ID: 41}, Release: Release{ID: 71, EnvironmentID: 9, Number: 3},
		Snapshot: runtimeReleaseSnapshot{Compose: &ResolvedComposeSnapshot{Services: []ResolvedComposeService{
			{Plan: ComposeServicePlan{Name: "api"}, Reference: "example/api:current", Digest: manifestDigest, ConfigDigest: configDigest},
			{Plan: ComposeServicePlan{Name: "worker"}, Reference: "example/worker:current", Digest: fakeContentDigest("worker")},
		}}},
	}
	override, err := renderComposeReleaseOverride(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"image: \"" + configDigest + "\"", "example/worker:current@" + fakeContentDigest("worker"),
		"pull_policy: never", "io.just-dashboard.release-id: \"71\"",
	} {
		if !strings.Contains(override, required) {
			t.Fatalf("override omitted %q:\n%s", required, override)
		}
	}
	if strings.Contains(override, "latest") || strings.Contains(override, "TOKEN") {
		t.Fatalf("override retained a mutable tag or variable: %s", override)
	}
}

func TestImmutableRuntimeFileRejectsDifferentRetryBytes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".just-dashboard", "release.yml")
	if err := writeImmutableRuntimeFile(root, ".just-dashboard/release.yml", "services: {}\n"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("runtime file mode = %v, error=%v", info.Mode().Perm(), err)
	}
	if err := writeImmutableRuntimeFile(root, ".just-dashboard/release.yml", "services: {}\n"); err != nil {
		t.Fatalf("idempotent write: %v", err)
	}
	if err := writeImmutableRuntimeFile(root, ".just-dashboard/release.yml", "services:\n  changed: {}\n"); err == nil {
		t.Fatal("runtime override accepted different retry bytes")
	}
}

func TestImmutableRuntimeFileRejectsCheckoutSymlinks(t *testing.T) {
	for _, target := range []string{"directory", "file"} {
		t.Run(target, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			sentinel := filepath.Join(outside, "release.yml")
			if err := os.WriteFile(sentinel, []byte("operator-owned"), 0o600); err != nil {
				t.Fatal(err)
			}
			link, destination := filepath.Join(root, ".just-dashboard"), outside
			if target == "file" {
				if err := os.Mkdir(link, 0o700); err != nil {
					t.Fatal(err)
				}
				link, destination = filepath.Join(link, "release.yml"), sentinel
			}
			if err := os.Symlink(destination, link); err != nil {
				t.Fatal(err)
			}
			if err := writeImmutableRuntimeFile(root, ".just-dashboard/release.yml", "services: {}\n"); err == nil {
				t.Fatal("accepted a runtime override outside the checkout")
			}
			if content, err := os.ReadFile(sentinel); err != nil || string(content) != "operator-owned" {
				t.Fatalf("outside sentinel changed: %q, %v", content, err)
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 1 {
				t.Fatalf("wrote generated files outside the checkout: %v, %v", entries, err)
			}
		})
	}
}

func TestRuntimePortDefaultPreservesExplicitVariables(t *testing.T) {
	variables := map[string]string{"USER_SETTING": "kept"}
	env, names := containerRuntimeEnvironment(RuntimePlanConfig{InternalPort: 3123}, variables)
	if len(env) != 2 || len(names) != 1 || len(variables) != 1 || env[1].Name != "PORT" || env[1].Value != "3123" {
		t.Fatalf("runtime environment=%+v names=%v", env, names)
	}
	env, _ = containerRuntimeEnvironment(RuntimePlanConfig{InternalPort: 3123}, map[string]string{"PORT": "9090"})
	if len(env) != 1 || env[0].Value != "9090" {
		t.Fatal("overrode explicit PORT")
	}
	env, _ = containerRuntimeEnvironment(RuntimePlanConfig{InternalPort: 3123, HostNetwork: true}, nil)
	if len(env) != 0 {
		t.Fatal("changed host-network application environment")
	}
}
