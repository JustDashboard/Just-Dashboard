package deploy

import (
	"strings"
	"testing"
)

// The language recipes' dependency order and caches: what each copies and
// installs before the source, what it mounts, and that a forced build
// renders no cache at all.

// prepareCachedAndForced prepares one recipe as an environment's build and
// again as a forced build of it, which must render no cache mount.
func prepareCachedAndForced(t *testing.T, boundary, root string, config BuildPlanConfig, variables ...string) (cached, forced string) {
	t.Helper()
	config.Method = BuildRecipe
	builder := NewArtifactBuilder(&artifactBackendFake{})
	prepared, err := builder.PrepareWithin(t.Context(), boundary, root, config, false, "just-dashboard/deployment-7:run-3", variables...)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	config.NoCache = true
	again, err := builder.PrepareWithin(t.Context(), boundary, root, config, false, "just-dashboard/deployment-7:run-4", variables...)
	if err != nil {
		t.Fatalf("prepare a forced build: %v", err)
	}
	if strings.Contains(again.DockerfilePreview, "type=cache") {
		t.Fatalf("a forced build mounts a cache:\n%s", again.DockerfilePreview)
	}
	return prepared.DockerfilePreview, again.DockerfilePreview
}

// assertBefore fails unless first occurs in dockerfile, and before second.
func assertBefore(t *testing.T, dockerfile, first, second string) {
	t.Helper()
	at, then := strings.Index(dockerfile, first), strings.Index(dockerfile, second)
	if at < 0 || then < 0 || at > then {
		t.Fatalf("Dockerfile does not have %q before %q:\n%s", first, second, dockerfile)
	}
}

// dockerfileLine is the first line of dockerfile that contains part.
func dockerfileLine(t *testing.T, dockerfile, part string) string {
	t.Helper()
	for _, line := range strings.Split(dockerfile, "\n") {
		if strings.Contains(line, part) {
			return line
		}
	}
	t.Fatalf("Dockerfile has no line with %q:\n%s", part, dockerfile)
	return ""
}

func cacheMountFor(name, target string) string {
	return "--mount=type=cache,id=jd-deployment-7-" + name + ",target=" + target + ",sharing=locked "
}

func TestRustCachesCratesAndTargetAndCopiesTheBinaryInTheBuildStep(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "Cargo.toml", "[package]\nname = \"api\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n[dependencies]\naxum = \"0.8\"\n")
	writeBuildFixture(t, root, "Cargo.lock", "version = 4\n\n[[package]]\nname = \"api\"\nversion = \"0.1.0\"\n\n[[package]]\nname = \"axum\"\nversion = \"0.8.4\"\n")
	writeBuildFixture(t, root, "src/main.rs", "fn main() {}\n")
	config := BuildPlanConfig{Recipe: "rust"}
	cached, forced := prepareCachedAndForced(t, root, root, config)
	registry := cacheMountFor("cargo-registry", "/usr/local/cargo/registry") + cacheMountFor("cargo-git", "/usr/local/cargo/git")
	assertGoDockerfile(t, cached, "RUN "+registry+"cargo fetch --locked\n")
	build := dockerfileLine(t, cached, "cargo build --release")
	for _, want := range []string{"RUN " + registry + cacheMountFor("cargo-target", "/src/target"),
		"rm -f /src/target/release/api && cargo build --release --locked --bin api && (mkdir -p /out && test -f /src/target/release/api && cp /src/target/release/api /out/app ||"} {
		if !strings.Contains(build, want) {
			t.Fatalf("build step %q lacks %q", build, want)
		}
	}
	// A forced build compiles from nothing, and still copies the binary out
	// of the step that built it.
	assertGoDockerfile(t, forced, "RUN cargo fetch --locked\n", "&& cp /src/target/release/api /out/app ||")
	if strings.Count(forced, "\nRUN ") != strings.Count(cached, "\nRUN ") {
		t.Fatalf("the forced build has other steps:\n%s\n---\n%s", forced, cached)
	}

	// A private registry's token is the fetch's alone, so the crates it
	// fetches stay in the image for the build that has no token.
	secret := config
	secret.Secrets = []BuildSecretConfig{{Variable: "CARGO_REGISTRIES_ACME_TOKEN", Step: "install"}}
	cached, _ = prepareCachedAndForced(t, root, root, secret, "CARGO_REGISTRIES_ACME_TOKEN")
	if strings.Contains(cached, "cargo-registry") || !strings.Contains(cached, cacheMountFor("cargo-target", "/src/target")) {
		t.Fatalf("a private registry's crates are cached:\n%s", cached)
	}

	// An absolute target-dir may name a directory of the image itself.
	writeBuildFixture(t, root, ".cargo/config.toml", "[build]\ntarget-dir = \"/opt/out\"\n")
	cached, _ = prepareCachedAndForced(t, root, root, config)
	if strings.Contains(cached, "cargo-target") || !strings.Contains(cached, "cp /opt/out/release/api /out/app") {
		t.Fatalf("an absolute target-dir is cached:\n%s", cached)
	}
	writeBuildFixture(t, root, ".cargo/config.toml", "[build]\ntarget-dir = \"build/cargo\"\n")
	cached, _ = prepareCachedAndForced(t, root, root, config)
	assertGoDockerfile(t, cached, cacheMountFor("cargo-target", "/src/build/cargo"), "cp /src/build/cargo/release/api /out/app")
}

func TestRustTargetIsNeverMountedOverTheSource(t *testing.T) {
	for _, recipe := range []rustRecipe{{targetDir: "."}, {targetDir: "crates", member: "crates/server"}, {targetDir: "/target"}} {
		if got := rustTargetPath(recipe); got != "" {
			t.Fatalf("target %+v mounts at %q", recipe, got)
		}
	}
	if got := rustTargetPath(rustRecipe{member: "crates/server"}); got != "/src/target" {
		t.Fatalf("a workspace target = %q", got)
	}
}

func TestLeptosAndTrunkCacheTheirBuildsApartFromTheMuslTarget(t *testing.T) {
	leptos := t.TempDir()
	writeBuildFixture(t, leptos, "Cargo.toml", "[package]\nname = \"site\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n[lib]\ncrate-type = [\"cdylib\", \"rlib\"]\n\n[dependencies]\nleptos = { version = \"0.8\" }\nleptos_axum = { version = \"0.8\", optional = true }\naxum = { version = \"0.8\", optional = true }\n\n[features]\nhydrate = [\"leptos/hydrate\"]\nssr = [\"dep:axum\", \"dep:leptos_axum\", \"leptos/ssr\"]\n\n[package.metadata.leptos]\nsite-root = \"target/site\"\nhash-files = true\nbin-features = [\"ssr\"]\nlib-features = [\"hydrate\"]\n")
	writeBuildFixture(t, leptos, "src/main.rs", "fn main() {}\n")
	writeBuildFixture(t, leptos, "src/lib.rs", "\n")
	cached, forced := prepareCachedAndForced(t, leptos, leptos, BuildPlanConfig{Recipe: "rust"})
	build := dockerfileLine(t, cached, "cargo leptos build")
	for _, want := range []string{cacheMountFor("cargo-target-debian", "/src/target"), cacheMountFor("cargo-leptos-tools", "/root/.cache/cargo-leptos"),
		// The last build's site is removed, so the image holds this
		// build's hashed bundle alone.
		"rm -rf target/release/site target/release/hash.txt target/site && cargo leptos build --release && (mkdir -p /out"} {
		if !strings.Contains(build, want) {
			t.Fatalf("build step %q lacks %q", build, want)
		}
	}
	assertGoDockerfile(t, forced, "rm -rf target/release/site target/release/hash.txt target/site && cargo leptos build --release && (")

	trunk := t.TempDir()
	writeBuildFixture(t, trunk, "Cargo.toml", "[package]\nname = \"counter\"\n\n[dependencies]\nyew = { version = \"0.21\", features = [\"csr\"] }\n")
	writeBuildFixture(t, trunk, "src/main.rs", "fn main() { yew::Renderer::<App>::new().render(); }\n")
	writeBuildFixture(t, trunk, "index.html", "<!doctype html><html><head><link data-trunk rel=\"rust\" /></head><body></body></html>")
	cached, _ = prepareCachedAndForced(t, trunk, trunk, BuildPlanConfig{Recipe: "rust", OutputDirectory: "dist"})
	build = dockerfileLine(t, cached, "trunk build")
	for _, want := range []string{cacheMountFor("cargo-target-debian", "/src/target"), cacheMountFor("trunk-tools", "/root/.cache/trunk")} {
		if !strings.Contains(build, want) {
			t.Fatalf("build step %q lacks %q", build, want)
		}
	}
}
