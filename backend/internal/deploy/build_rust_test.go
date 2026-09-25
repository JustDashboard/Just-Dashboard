package deploy

import (
	"reflect"
	"strings"
	"testing"
)

// The Rust recipe beyond a single crate's default binary: workspaces, the
// binary a crate serves, the native crates its lockfile resolves, sqlx's
// compile-time queries, the frameworks that build a browser side, and the
// lockfile and memory a build needs.

func prepareRust(t *testing.T, boundary, root string, config BuildPlanConfig) PreparedBuild {
	t.Helper()
	config.Method, config.Recipe = BuildRecipe, "rust"
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).PrepareWithin(t.Context(), boundary, root, config, false, "fixture:rust")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	return prepared
}

func detectRust(t *testing.T, root string) DetectionResult {
	t.Helper()
	detection, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("b", 40)})
	if err != nil {
		t.Fatal(err)
	}
	return detection
}

func rustCandidateAt(t *testing.T, detection DetectionResult, root string) *DetectedCandidate {
	t.Helper()
	for index := range detection.Candidates {
		if candidate := &detection.Candidates[index]; candidate.Recipe == "rust" && candidate.Root == root {
			return candidate
		}
	}
	t.Fatalf("no Rust candidate at %q: %+v", root, detection.Candidates)
	return nil
}

func TestCargoWorkspaceMembersBuildWithinTheirWorkspace(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "Cargo.toml", "[workspace]\nresolver = \"2\"\nmembers = [\"crates/*\"]\n\n[workspace.package]\nversion = \"0.1.0\"\nedition = \"2021\"\n\n[workspace.dependencies]\naxum = \"0.8\"\ntokio = { version = \"1\", features = [\"full\"] }\n")
	writeBuildFixture(t, root, "Cargo.lock", "version = 4\n\n[[package]]\nname = \"axum\"\nversion = \"0.8.4\"\n\n[[package]]\nname = \"tokio\"\nversion = \"1.47.1\"\n\n[[package]]\nname = \"core\"\nversion = \"0.1.0\"\n\n[[package]]\nname = \"server\"\nversion = \"0.1.0\"\n")
	writeBuildFixture(t, root, "crates/server/Cargo.toml", "[package]\nname = \"server\"\nversion.workspace = true\nedition.workspace = true\n\n[dependencies]\naxum.workspace = true\ntokio = { workspace = true }\ncore = { path = \"../core\" }\n")
	writeBuildFixture(t, root, "crates/server/src/main.rs", "#[tokio::main]\nasync fn main() { let listener = tokio::net::TcpListener::bind(\"0.0.0.0:3000\").await.unwrap(); axum::serve(listener, axum::Router::new()).await.unwrap(); }\n")
	writeBuildFixture(t, root, "crates/server/templates/index.html", "<h1>hi</h1>")
	writeBuildFixture(t, root, "crates/core/Cargo.toml", "[package]\nname = \"core\"\nversion.workspace = true\n\n[lib]\n")
	writeBuildFixture(t, root, "crates/core/src/lib.rs", "pub fn f() {}\n")
	detection := detectRust(t, root)
	for _, candidate := range detection.Candidates {
		if candidate.Root == "" {
			t.Fatalf("the workspace root without a package is still a candidate: %+v", candidate)
		}
	}
	server := rustCandidateAt(t, detection, "crates/server")
	if selected := selectedDetectionCandidate(&detection); selected == nil || selected.ID != server.ID {
		t.Fatalf("selected %+v", selected)
	}
	if server.Framework != "axum" || server.UnpinnedDependencies || server.Rust == nil || server.Rust.Workspace != "." || server.Rust.Package != "server" || server.RecipeIssue != "" {
		t.Fatalf("server = %+v / %+v", server, server.Rust)
	}
	prepared := prepareRust(t, root, root+"/crates/server", BuildPlanConfig{RootDirectory: "crates/server"})
	if prepared.ContextDirectory != "." {
		t.Fatalf("context = %q", prepared.ContextDirectory)
	}
	assertGoDockerfile(t, prepared.DockerfilePreview, "RUN cargo fetch --locked\n", "cargo build --release --locked -p server --bin server\n",
		"cp /src/target/release/server /out/app", "COPY --from=build --chown=app:app /src/crates/server/templates /home/app/templates")
	if item := findingByCode(rustBuildFindings(server, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "rust"}}), "cargo_workspace_member"); item == nil || item.Severity != PreflightPass {
		t.Fatalf("cargo_workspace_member = %+v", item)
	}

	// A crate an ancestor workspace does not list builds on its own.
	outside := t.TempDir()
	writeBuildFixture(t, outside, "Cargo.toml", "[workspace]\nmembers = [\"crates/*\"]\nexclude = [\"tools\"]\n")
	writeBuildFixture(t, outside, "tools/gen/Cargo.toml", "[package]\nname = \"gen\"\n")
	writeBuildFixture(t, outside, "tools/gen/src/main.rs", "fn main() {}\n")
	prepared = prepareRust(t, outside, outside+"/tools/gen", BuildPlanConfig{RootDirectory: "tools/gen"})
	if prepared.ContextDirectory != "" || !strings.Contains(prepared.DockerfilePreview, "cargo build --release --bin gen\n") {
		t.Fatalf("excluded crate built in the workspace: %q\n%s", prepared.ContextDirectory, prepared.DockerfilePreview)
	}
}

func TestRustServedBinaryFollowsCargoTargets(t *testing.T) {
	// A maintenance [[bin]] beside the src/main.rs server: the server.
	root := t.TempDir()
	writeBuildFixture(t, root, "Cargo.toml", "[package]\nname = \"shop\"\nversion = \"0.1.0\"\n\n[[bin]]\nname = \"migrate\"\n\n[dependencies]\nactix-web = \"4\"\n")
	writeBuildFixture(t, root, "src/main.rs", "#[actix_web::main]\nasync fn main() { actix_web::HttpServer::new(|| actix_web::App::new()).bind((\"0.0.0.0\", 8080)).unwrap().run().await.unwrap(); }\n")
	writeBuildFixture(t, root, "src/bin/migrate.rs", "fn main() {}\n")
	prepared := prepareRust(t, root, root, BuildPlanConfig{})
	assertGoDockerfile(t, prepared.DockerfilePreview, "cargo build --release --bin shop", "cp /src/target/release/shop /out/app")
	candidate := rustCandidateAt(t, detectRust(t, root), "")
	if candidate.Rust == nil || !reflect.DeepEqual(candidate.Rust.Binaries, []string{"migrate", "shop"}) || candidate.Rust.Binary != "shop" ||
		candidate.Rust.BinaryReason != "the only binary that starts a server" {
		t.Fatalf("binaries = %+v", candidate.Rust)
	}

	// Only src/bin/server.rs: that binary, not the package name.
	bins := t.TempDir()
	writeBuildFixture(t, bins, "Cargo.toml", "[package]\nname = \"api\"\n\n[dependencies]\naxum = \"0.8\"\n")
	writeBuildFixture(t, bins, "src/bin/server.rs", "fn main() {}\n")
	prepared = prepareRust(t, bins, bins, BuildPlanConfig{})
	assertGoDockerfile(t, prepared.DockerfilePreview, "cp /src/target/release/server /out/app")

	// Two tools and no server: the operator chooses.
	tie := t.TempDir()
	writeBuildFixture(t, tie, "Cargo.toml", "[package]\nname = \"tools\"\n")
	writeBuildFixture(t, tie, "src/bin/import.rs", "fn main() {}\n")
	writeBuildFixture(t, tie, "src/bin/export.rs", "fn main() {}\n")
	if _, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(t.Context(), tie, BuildPlanConfig{Method: BuildRecipe, Recipe: "rust"}, false, "t:1"); err == nil ||
		!strings.Contains(err.Error(), "export, import") {
		t.Fatalf("a tie was guessed: %v", err)
	}
	candidate = rustCandidateAt(t, detectRust(t, tie), "")
	if !strings.Contains(strings.Join(candidate.NeedsDecision, " "), "choose the binary to serve: export, import") || candidate.RecipeIssue != "" {
		t.Fatalf("decision = %v, issue = %q", candidate.NeedsDecision, candidate.RecipeIssue)
	}
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "rust"}
	if item := findingByCode(rustBuildFindings(candidate, PlanConfiguration{Build: build}), "rust_binary_ambiguous"); item == nil || item.Severity != PreflightDecision {
		t.Fatalf("rust_binary_ambiguous = %+v", item)
	}
	build.CargoBin = "export"
	if item := findingByCode(rustBuildFindings(candidate, PlanConfiguration{Build: build}), "rust_binary_ambiguous"); item != nil {
		t.Fatal("a chosen binary was still asked for")
	}
	prepared = prepareRust(t, tie, tie, BuildPlanConfig{CargoBin: "export"})
	assertGoDockerfile(t, prepared.DockerfilePreview, "cargo build --release --bin export")
	build.CargoBin = "nothing"
	if item := findingByCode(rustBuildFindings(candidate, PlanConfiguration{Build: build}), "rust_binary_missing"); item == nil || item.Severity != PreflightBlocked {
		t.Fatalf("rust_binary_missing = %+v", item)
	}
	if err := (PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "go", CargoBin: "export"}, Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst}}).Validate(); err == nil {
		t.Fatal("a Rust binary was accepted for a Go recipe")
	}

	// .cargo/config.toml decides where the binary is written.
	config := t.TempDir()
	writeBuildFixture(t, config, "Cargo.toml", "[package]\nname = \"svc\"\n")
	writeBuildFixture(t, config, "src/main.rs", "fn main() {}\n")
	writeBuildFixture(t, config, ".cargo/config.toml", "[build]\ntarget = \"x86_64-unknown-linux-musl\"\ntarget-dir = \"out\"\n")
	prepared = prepareRust(t, config, config, BuildPlanConfig{})
	assertGoDockerfile(t, prepared.DockerfilePreview, "cp /src/out/x86_64-unknown-linux-musl/release/svc /out/app")
}

func TestRustNativeCratesInstallWhatTheyNeed(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "Cargo.toml", "[package]\nname = \"svc\"\n\n[dependencies]\ndiesel = { version = \"2\", features = [\"postgres\"] }\ntonic = \"0.12\"\n")
	writeBuildFixture(t, root, "src/main.rs", "fn main() {}\n")
	lock := "version = 4\n"
	for _, name := range []string{"svc", "diesel", "tonic", "pq-sys", "openssl-src", "prost-build", "cmake", "clang-sys"} {
		lock += "\n[[package]]\nname = \"" + name + "\"\nversion = \"1.0.0\"\n"
	}
	writeBuildFixture(t, root, "Cargo.lock", lock)
	prepared := prepareRust(t, root, root, BuildPlanConfig{})
	assertGoDockerfile(t, prepared.DockerfilePreview,
		"RUN apk add --no-cache musl-dev pkgconfig openssl-dev openssl-libs-static perl make cmake g++ linux-headers clang-dev protoc protobuf-dev libpq-dev\n",
		`ENV OPENSSL_STATIC=1 PQ_LIB_STATIC=1 RUSTFLAGS="-C link-arg=-Wl,-Bstatic -C link-arg=-lpgcommon_shlib`,
		`export CARGO_BUILD_JOBS="$(`, "FROM alpine:3.22@sha256:")
	candidate := rustCandidateAt(t, detectRust(t, root), "")
	item := findingByCode(rustBuildFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}), "rust_native_dependency")
	if item == nil || !strings.Contains(item.Measured, "pq-sys (libpq-dev)") || !strings.Contains(item.Measured, "prost-build (protoc, protobuf-dev)") {
		t.Fatalf("rust_native_dependency = %+v", item)
	}
	// A vendored protoc needs no system one.
	writeBuildFixture(t, root, "Cargo.lock", lock+"\n[[package]]\nname = \"protoc-bin-vendored\"\nversion = \"3.1.0\"\n")
	prepared = prepareRust(t, root, root, BuildPlanConfig{})
	if strings.Contains(prepared.DockerfilePreview, "protobuf-dev") {
		t.Fatalf("a vendored protoc still installed one:\n%s", prepared.DockerfilePreview)
	}
}

func TestRustSQLxCompilesOfflineOrSaysWhyNot(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "Cargo.toml", "[package]\nname = \"svc\"\n\n[dependencies]\nsqlx = { version = \"0.8\", features = [\"postgres\", \"runtime-tokio\", \"macros\"] }\naxum = \"0.8\"\n")
	writeBuildFixture(t, root, "Cargo.lock", "version = 4\n\n[[package]]\nname = \"svc\"\nversion = \"0.1.0\"\n\n[[package]]\nname = \"sqlx\"\nversion = \"0.8.6\"\n\n[[package]]\nname = \"sqlx-macros\"\nversion = \"0.8.6\"\n\n[[package]]\nname = \"axum\"\nversion = \"0.8.4\"\n")
	writeBuildFixture(t, root, "src/main.rs", "mod db;\nfn main() { sqlx::migrate!().run(&pool); }\n")
	writeBuildFixture(t, root, "src/db.rs", "pub async fn users() { sqlx::query_as!(User, \"select id from users\").fetch_all(&pool).await; }\n")
	candidate := rustCandidateAt(t, detectRust(t, root), "")
	if candidate.Rust == nil || !candidate.Rust.SQLxMacros || candidate.Rust.SQLxOffline || !candidate.Rust.SQLxMigrate {
		t.Fatalf("sqlx = %+v", candidate.Rust)
	}
	plan := PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}
	if item := findingByCode(rustBuildFindings(candidate, plan), "sqlx_offline_data_missing"); item == nil || item.Severity != PreflightBlocked || !strings.Contains(item.Action, "cargo sqlx prepare") {
		t.Fatalf("sqlx_offline_data_missing = %+v", item)
	}
	plan.Variables = []PlannedVariable{{Name: "DATABASE_URL", Scopes: []string{"build", "runtime"}}}
	if item := findingByCode(rustBuildFindings(candidate, plan), "sqlx_offline_data_missing"); item == nil || item.Severity != PreflightWarning {
		t.Fatalf("a build-time DATABASE_URL = %+v", item)
	}
	if item := findingByCode(rustBuildFindings(candidate, plan), "sqlx_migrations"); item == nil {
		t.Fatal("migrate!() was not reported")
	}
	writeBuildFixture(t, root, ".sqlx/query-1.json", "{}")
	prepared := prepareRust(t, root, root, BuildPlanConfig{})
	assertGoDockerfile(t, prepared.DockerfilePreview, "ENV OPENSSL_STATIC=1 SQLX_OFFLINE=true\n")
	candidate = rustCandidateAt(t, detectRust(t, root), "")
	if item := findingByCode(rustBuildFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}), "sqlx_offline"); item == nil || item.Severity != PreflightPass {
		t.Fatalf("sqlx_offline = %+v", item)
	}
}

func TestRustFullStackFrameworksBuildTheirOwnWay(t *testing.T) {
	leptos := t.TempDir()
	writeBuildFixture(t, leptos, "Cargo.toml", "[package]\nname = \"start-axum\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n[lib]\ncrate-type = [\"cdylib\", \"rlib\"]\n\n[dependencies]\nleptos = { version = \"0.8\" }\nleptos_axum = { version = \"0.8\", optional = true }\naxum = { version = \"0.8\", optional = true }\n\n[features]\nhydrate = [\"leptos/hydrate\"]\nssr = [\"dep:axum\", \"dep:leptos_axum\", \"leptos/ssr\"]\n\n[package.metadata.leptos]\noutput-name = \"start-axum\"\nsite-root = \"target/site\"\nsite-addr = \"127.0.0.1:3000\"\nbin-features = [\"ssr\"]\nlib-features = [\"hydrate\"]\n")
	writeBuildFixture(t, leptos, "Cargo.lock", "version = 4\n\n[[package]]\nname = \"start-axum\"\nversion = \"0.1.0\"\n")
	writeBuildFixture(t, leptos, "src/main.rs", "#[cfg(feature = \"ssr\")]\n#[tokio::main]\nasync fn main() { leptos_axum::generate_route_list(App); }\n")
	writeBuildFixture(t, leptos, "src/lib.rs", "pub mod app;\n")
	detection := detectRust(t, leptos)
	candidate := rustCandidateAt(t, detection, "")
	if candidate.Framework != "leptos" || candidate.Port != 3000 || candidate.Profile != ProfileWeb || candidate.Confidence != ConfidenceHigh || candidate.RecipeIssue != "" ||
		strings.Contains(strings.Join(candidate.NeedsDecision, " "), "confirm the port") {
		t.Fatalf("leptos = %+v", candidate)
	}
	prepared := prepareRust(t, leptos, leptos, BuildPlanConfig{})
	assertGoDockerfile(t, prepared.DockerfilePreview, "FROM rust:1-bookworm@sha256:", "RUN rustup target add wasm32-unknown-unknown\n",
		"RUN cargo install cargo-leptos --locked --version "+cargoLeptosVersion, "cargo leptos build --release\n",
		"cp target/release/start-axum /out/app && cp -r target/site /out/site", "FROM debian:bookworm-slim@sha256:",
		"COPY --from=build --chown=app:app /out/site /home/app/site", "ENV LEPTOS_SITE_ROOT=site LEPTOS_ENV=PROD LEPTOS_OUTPUT_NAME=start-axum",
		`CMD ["/bin/sh","-c","exec env LEPTOS_SITE_ADDR=0.0.0.0:${PORT:-3000} /app"]`)
	if findings := buildMemoryFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "rust"}}, HostObservation{AvailableMemory: 2 << 30}); len(findings) != 1 ||
		!strings.Contains(findings[0].Measured, "Leptos") {
		t.Fatalf("build memory = %+v", findings)
	}

	trunk := t.TempDir()
	writeBuildFixture(t, trunk, "Cargo.toml", "[package]\nname = \"counter\"\n\n[dependencies]\nyew = { version = \"0.21\", features = [\"csr\"] }\n")
	writeBuildFixture(t, trunk, "src/main.rs", "fn main() { yew::Renderer::<App>::new().render(); }\n")
	writeBuildFixture(t, trunk, "index.html", "<!doctype html><html><head><link data-trunk rel=\"rust\" /></head><body></body></html>")
	detection = detectRust(t, trunk)
	if len(detection.Candidates) != 1 {
		t.Fatalf("the Trunk source page became a site of its own: %+v", detection.Candidates)
	}
	candidate = rustCandidateAt(t, detection, "")
	if candidate.Framework != "trunk" || candidate.Profile != ProfileStatic || candidate.OutputDirectory != "dist" || !candidate.SPAFallback {
		t.Fatalf("trunk = %+v", candidate)
	}
	prepared = prepareRust(t, trunk, trunk, BuildPlanConfig{OutputDirectory: "dist", SPAFallback: true})
	assertGoDockerfile(t, prepared.DockerfilePreview, "RUN cargo install trunk --locked --version "+trunkVersion, "trunk build --release --dist /out/dist",
		"FROM nginx:1.29-alpine@sha256:", "try_files $uri $uri/ /index.html;", "COPY --from=build /out/dist/ /usr/share/nginx/html/")

	for name, fixture := range map[string]struct{ manifest, want string }{
		"dioxus":  {"[package]\nname = \"app\"\n\n[dependencies]\ndioxus = { version = \"0.6\", features = [\"fullstack\"] }\n", "dx bundle"},
		"shuttle": {"[package]\nname = \"app\"\n\n[dependencies]\nshuttle-runtime = \"0.56\"\nshuttle-axum = \"0.56\"\naxum = \"0.8\"\n", "Shuttle"},
	} {
		root := t.TempDir()
		writeBuildFixture(t, root, "Cargo.toml", fixture.manifest)
		writeBuildFixture(t, root, "src/main.rs", "fn main() {}\n")
		candidate := rustCandidateAt(t, detectRust(t, root), "")
		if candidate.Framework != name || !strings.Contains(candidate.RecipeIssue, fixture.want) {
			t.Fatalf("%s = %+v", name, candidate)
		}
		if _, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(t.Context(), root, BuildPlanConfig{Method: BuildRecipe, Recipe: "rust"}, false, "t:1"); err == nil ||
			!strings.Contains(err.Error(), fixture.want) {
			t.Fatalf("%s prepared: %v", name, err)
		}
	}
}

func TestCargoLockIsComparedWithTheManifest(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "Cargo.toml", "[package]\nname = \"svc\"\n\n[dependencies]\naxum = \"0.8\"\nserde = { version = \"1\", features = [\"derive\"] }\n\n[dev-dependencies]\ninsta = \"1\"\n")
	writeBuildFixture(t, root, "src/main.rs", "fn main() {}\n")
	writeBuildFixture(t, root, "Cargo.lock", "version = 4\n\n[[package]]\nname = \"svc\"\nversion = \"0.1.0\"\n\n[[package]]\nname = \"axum\"\nversion = \"0.7.9\"\n\n[[package]]\nname = \"serde\"\nversion = \"1.0.219\"\n")
	candidate := rustCandidateAt(t, detectRust(t, root), "")
	if candidate.Rust == nil || !reflect.DeepEqual(candidate.Rust.LockStale, []string{"axum 0.8", "insta"}) {
		t.Fatalf("stale = %+v", candidate.Rust)
	}
	if item := findingByCode(rustBuildFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}), "lockfile_out_of_sync"); item == nil || item.Severity != PreflightWarning {
		t.Fatalf("lockfile_out_of_sync = %+v", item)
	}
	prepared := prepareRust(t, root, root, BuildPlanConfig{})
	if strings.Contains(prepared.DockerfilePreview, "--locked") || !strings.Contains(strings.Join(prepared.Notes, " "), "Building without --locked") {
		t.Fatalf("a stale lock was built --locked:\n%s\n%v", prepared.DockerfilePreview, prepared.Notes)
	}

	// A version 4 lock with a toolchain pinned before Cargo 1.78.
	writeBuildFixture(t, root, "Cargo.lock", "version = 4\n\n[[package]]\nname = \"svc\"\nversion = \"0.1.0\"\n\n[[package]]\nname = \"axum\"\nversion = \"0.8.4\"\n\n[[package]]\nname = \"serde\"\nversion = \"1.0.219\"\n\n[[package]]\nname = \"insta\"\nversion = \"1.43.1\"\n")
	writeBuildFixture(t, root, "rust-toolchain.toml", "[toolchain]\nchannel = \"1.75.0\"\n")
	prepared = prepareRust(t, root, root, BuildPlanConfig{})
	assertGoDockerfile(t, prepared.DockerfilePreview, "FROM rust:1-alpine@sha256:", "cargo build --release --locked --bin svc")
	candidate = rustCandidateAt(t, detectRust(t, root), "")
	if item := findingByCode(rustBuildFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}), "rust_lock_newer_than_toolchain"); item == nil {
		t.Fatalf("lock/toolchain = %+v", candidate.Rust)
	}
	if action := unpinnedDependenciesAction("rust"); !strings.Contains(action, "cargo generate-lockfile") {
		t.Fatalf("unpinned action = %q", action)
	}
}

func TestCargoRequirementsFollowCargoSemver(t *testing.T) {
	for _, fixture := range []struct {
		requirement, version string
		accepts              bool
	}{
		{"0.8", "0.8.4", true}, {"0.8", "0.7.9", false}, {"0.8", "0.9.0", false},
		{"1", "1.47.1", true}, {"1.2.3", "1.9.0", true}, {"1.2.3", "1.2.2", false}, {"1.2.3", "2.0.0", false},
		{"0.0.3", "0.0.4", false}, {"~1.2", "1.2.9", true}, {"~1.2", "1.3.0", false},
		{"=1.0.5", "1.0.5", true}, {"=1.0.5", "1.0.6", false}, {">=1.0, <2.0", "1.5.0", true}, {">=1.0, <2.0", "2.1.0", false},
		{"*", "3.0.0", true}, {"1.*", "1.8.0", true}, {"^0.2", "0.2.7", true},
	} {
		if got := cargoRequirementAccepts(fixture.requirement, fixture.version); got != fixture.accepts {
			t.Fatalf("%s accepts %s = %v", fixture.requirement, fixture.version, got)
		}
	}
	entries := readTOML([]byte("[dependencies]\ntokio = { version = \"1\", features = [\"macros\", \"rt\"], optional = true }\n"))
	if value, ok := tomlLookup(entries, "dependencies", "tokio.features"); !ok || !reflect.DeepEqual(value.list, []string{"macros", "rt"}) {
		t.Fatalf("inline array = %+v", value)
	}
	if tomlText(entries, "dependencies", "tokio.optional") != "true" {
		t.Fatal("a key after an inline array was lost")
	}
}
