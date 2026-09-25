package deploy

import (
	"fmt"
	"slices"
	"strings"
)

// rustBuildFindings are what the Rust recipe will do with the planned crate
// and what it would refuse, from detection's facts and the plan.
func rustBuildFindings(candidate *DetectedCandidate, configuration PlanConfiguration) []PreflightFinding {
	facts := candidate.Rust
	if facts == nil {
		return nil
	}
	build := configuration.Build
	findings := []PreflightFinding{}
	add := func(code string, severity PreflightSeverity, title, measured, means, action, field string) {
		findings = append(findings, finding(code, severity, title, boundedFindingText(measured), means, action, "deploy", field))
	}
	binary := facts.Binary
	if build.CargoBin != "" {
		binary = build.CargoBin
	}
	switch {
	case candidate.NotDeployable != "" || facts.Fullstack == "trunk":
	case build.CargoBin != "" && len(facts.Binaries) > 0 && !slices.Contains(facts.Binaries, build.CargoBin):
		add("rust_binary_missing", PreflightBlocked, "The selected Rust binary is not a target of this crate",
			build.CargoBin+"; binaries: "+strings.Join(facts.Binaries, ", "),
			"The recipe builds one binary target with cargo build --bin; the crate has none by that name.",
			"Choose one of the crate's binaries in Build settings.", "configuration.build.cargoBin")
	case binary == "" && len(facts.Binaries) > 1:
		add("rust_binary_ambiguous", PreflightDecision, "Choose the binary to serve", strings.Join(facts.Binaries, ", "),
			"The crate builds several binaries and none is default-run, the only one that starts a server, or named after the package, so the recipe cannot tell which is the service.",
			"Choose the binary in Build settings, or set package.default-run in Cargo.toml.", "configuration.build.cargoBin")
	case binary != "" && len(facts.Binaries) > 1:
		reason := facts.BinaryReason
		if build.CargoBin != "" {
			reason = "selected in Build settings"
		}
		add("rust_binary", PreflightPass, "The service's binary", binary+" ("+reason+")",
			"The crate builds "+strings.Join(facts.Binaries, ", ")+"; the recipe builds and runs "+binary+".", "", "configuration.build.cargoBin")
	}
	if facts.Workspace != "" {
		add("cargo_workspace_member", PreflightPass, "Builds within its Cargo workspace", facts.Workspace+": -p "+facts.Package,
			"The crate inherits from the workspace at "+facts.Workspace+" and shares its Cargo.lock, so the build starts there and selects the package.", "", "configuration.build.rootDirectory")
	}
	if len(facts.NativeCrates) > 0 {
		means := "These crates compile or link C code; the build stage, which the runtime image leaves behind, installs what they need and links the binary statically."
		switch {
		case facts.Fullstack != "":
			means = "These crates compile or link C code; the Debian build stage installs what they need, and the runtime image the shared libraries the binary links."
		case len(facts.NativeRuntime) > 0:
			means = "These crates compile or link C code; the build stage installs what they need. bindgen loads libclang while the crate builds, which a statically linked build cannot, so the binary links musl dynamically and the runtime image installs " +
				strings.Join(facts.NativeRuntime, ", ") + "."
		}
		add("rust_native_dependency", PreflightPass, "Native build dependencies are installed", strings.Join(facts.NativeCrates, "; "), means, "", "configuration.build")
	}
	if len(facts.NativeUnmapped) > 0 {
		add("rust_native_dependency_unmapped", PreflightWarning, "A crate needs a system library the recipe does not install", strings.Join(facts.NativeUnmapped, ", "),
			"Cargo.lock resolves a binding to a desktop or hardware library; if the service's binary links it, the build fails where it links.",
			"Build from a Dockerfile that installs the library, or drop the dependency from the server's features.", "configuration.build")
	}
	if facts.SQLxMacros {
		databaseURL := slices.ContainsFunc(configuration.Variables, func(variable PlannedVariable) bool {
			return variable.Name == "DATABASE_URL" && slices.Contains(variable.Scopes, "build")
		})
		switch {
		case facts.SQLxOffline:
			data := facts.SQLxOfflineData
			if data == "" {
				data = ".sqlx"
			}
			add("sqlx_offline", PreflightPass, "sqlx checks its queries against committed data", data,
				"The build compiles with SQLX_OFFLINE=true, so its query macros read the committed offline data and connect to nothing.", "", "configuration.build")
		case databaseURL:
			add("sqlx_offline_data_missing", PreflightWarning, "sqlx will check its queries against a database while compiling", "DATABASE_URL is a build variable; no .sqlx or sqlx-data.json is committed",
				"The query macros connect to DATABASE_URL at compile time; a database linked by the dashboard is on the project's network, which the build is not.",
				"Run cargo sqlx prepare and commit .sqlx, or keep DATABASE_URL pointing at a database the build can reach.", "variables.DATABASE_URL")
		default:
			add("sqlx_offline_data_missing", PreflightBlocked, "sqlx cannot compile its queries", "query!/query_as! macros and no .sqlx directory or sqlx-data.json",
				"sqlx checks each query macro against a database while compiling, and the build has neither a database nor committed offline data, so it fails with \"set DATABASE_URL to use query macros online\".",
				"Run cargo sqlx prepare (cargo sqlx prepare --workspace in a workspace) and commit the .sqlx directory, or sqlx-data.json with sqlx 0.6 and older.", "configuration.build")
		}
	}
	if facts.SQLxMigrate {
		add("sqlx_migrations", PreflightPass, "Migrations are applied by the application", "sqlx::migrate!()",
			"The migrations are compiled into the binary and run when it starts, so the release applies them before it serves.", "", "configuration.build")
	}
	if len(facts.LockStale) > 0 {
		add("lockfile_out_of_sync", PreflightWarning, "Cargo.lock is out of sync with Cargo.toml", "Cargo.lock does not resolve "+strings.Join(boundedNames(facts.LockStale), ", "),
			"cargo build --locked would refuse (\"the lock file needs to be updated but --locked was passed\"), so the build runs without --locked and resolves them to the newest versions the requirements allow.",
			"Run cargo update -w (or cargo build) and commit Cargo.lock.", "configuration.build")
	}
	if facts.Toolchain != "" && rustLockOutgrowsToolchain(facts.LockVersion, facts.Toolchain) {
		add("rust_lock_newer_than_toolchain", PreflightWarning, "Cargo.lock is newer than the pinned Rust reads",
			fmt.Sprintf("Cargo.lock version %d; rust-toolchain pins %s", facts.LockVersion, facts.Toolchain),
			"Lockfile version 4 needs Cargo 1.78 or newer, so the build uses the current stable Rust instead of the pin.",
			"Update the rust-toolchain pin to 1.78 or newer.", "configuration.build")
	}
	switch facts.Fullstack {
	case "leptos":
		add("rust_leptos", PreflightPass, "cargo-leptos builds the server and its site", "cargo-leptos "+cargoLeptosVersion,
			"The build compiles the server with its ssr features and the WebAssembly site, and the server serves target/site from its working directory on PORT.", "", "configuration.build")
	case "trunk":
		add("rust_trunk", PreflightPass, "Trunk builds the WebAssembly site", "trunk "+trunkVersion,
			"The build compiles the crate to WebAssembly with trunk build --release and serves dist/ as a static site.", "", "configuration.build")
	}
	return findings
}
