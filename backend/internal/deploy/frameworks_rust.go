package deploy

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// cargoManifest is the summary of Cargo.toml the repository-shape and
// network passes read: the package and binary names a build produces,
// whether the file is a workspace, and the crates it depends on. It is
// readCargoFile's reading, so both agree on dotted keys, renamed packages
// and target-specific dependencies.
type cargoManifest struct {
	name       string
	defaultRun string
	bins       []string
	workspace  bool
	hasPkg     bool
	deps       map[string]bool
}

var (
	rustToolchainRE  = regexp.MustCompile(`\b(1\.[0-9]{2,3}(?:\.[0-9]+)?)\b`)
	rustPreReleaseRE = regexp.MustCompile(`\b(nightly|beta)\b`)
)

func parseCargoManifest(content []byte) cargoManifest {
	file := readCargoFile(content)
	manifest := cargoManifest{name: file.name, defaultRun: file.defaultRun, workspace: file.workspace, hasPkg: file.hasPkg, deps: map[string]bool{}}
	for _, bin := range file.bins {
		manifest.bins = append(manifest.bins, bin.name)
	}
	for name := range file.crates(nil, false) {
		manifest.deps[name] = true
	}
	return manifest
}

func (manifest cargoManifest) binary() string {
	if manifest.defaultRun != "" {
		return manifest.defaultRun
	}
	if len(manifest.bins) > 0 {
		return manifest.bins[0]
	}
	return manifest.name
}

func normalizeCrate(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "_", "-")
}

// rustWebFrameworks name the crate a Rust HTTP service is built on and the
// port its documentation starts on.
var rustWebFrameworks = []struct {
	crate, name string
	port        int
}{
	// Loco is built on axum, so it is recognised first.
	{"loco-rs", "loco", 5150}, {"axum", "axum", 3000}, {"actix-web", "actix-web", 8080}, {"rocket", "rocket", 8000},
	{"warp", "warp", 3030}, {"poem", "poem", 3000}, {"salvo", "salvo", 5800},
}

// chooseRustToolchain reads a `rust-toolchain` or `rust-toolchain.toml`
// pin. A stable version becomes the image tag; nightly and beta need a
// Dockerfile, since the recipe's images are stable releases only.
func chooseRustToolchain(content string) (string, error) {
	if rustPreReleaseRE.MatchString(content) {
		return "", fmt.Errorf("%w: the Rust recipe builds with stable toolchains; a nightly or beta channel needs a Dockerfile", ErrUnsupportedBuilder)
	}
	if match := rustToolchainRE.FindStringSubmatch(content); match != nil {
		return match[1], nil
	}
	return "1", nil
}

// rustLockOutgrowsToolchain says Cargo.lock's format is newer than the
// pinned toolchain reads: version 4 needs Cargo 1.78.
func rustLockOutgrowsToolchain(lockVersion int, toolchain string) bool {
	if lockVersion < 4 || toolchain == "1" {
		return false
	}
	parts := strings.Split(toolchain, ".")
	if len(parts) < 2 {
		return false
	}
	minor, err := strconv.Atoi(parts[1])
	return err == nil && minor < 78
}

// rustMarker is what the Cargo pass read about one root: the crate, the
// facts a candidate records, and how many workspace members name this
// root as their workspace.
type rustMarker struct {
	crate   *cargoCrate
	facts   *DetectedRustBuild
	members int
}

// readCargoCrates reads every crate the walk found, under one source budget
// apart from the walk's own, before candidates are built: a member's
// framework is declared through its workspace, and a Trunk crate's
// index.html is its source, not a site of its own.
func readCargoCrates(checkout string, markers map[string]*detectedMarkers) {
	roots := []string{}
	for root, marker := range markers {
		if len(marker.cargoToml) > 0 {
			roots = append(roots, root)
		}
	}
	sort.Strings(roots)
	budget := &rustSourceBudget{remaining: 16 << 20, locks: map[string]*cargoLock{}}
	for index, root := range roots {
		marker := markers[root]
		marker.rust = &rustMarker{}
		if index >= 64 {
			continue
		}
		crate, err := readCargoCrate(checkout, filepath.Join(checkout, root), budget)
		if err != nil {
			continue
		}
		marker.rust.crate = &crate
		marker.rust.facts = rustFacts(checkout, crate, planRustNative(crate.lock))
	}
	for _, root := range roots {
		crate := markers[root].rust.crate
		if crate == nil || crate.workspace == nil || crate.workspace.member == "" || !crate.file.hasPkg {
			continue
		}
		if owner := markers[filepath.FromSlash(checkoutPath(checkout, crate.workspace.dir))]; owner != nil && owner.rust != nil {
			owner.rust.members++
		}
	}
}

// trunkSource says the root's index.html is a Trunk crate's source page.
func (m *detectedMarkers) trunkSource() bool {
	return m.rust != nil && m.rust.crate != nil && m.rust.crate.trunk != ""
}

// rustCandidate builds the candidate for a root with a Cargo.toml. A
// workspace root with no package of its own is no candidate when the walk
// found its members: each member is one, built within the workspace.
func rustCandidate(marker *detectedMarkers, rootLabel string) (DetectedCandidate, bool) {
	manifest := parseCargoManifest(marker.cargoToml)
	candidate := DetectedCandidate{
		Name: "Rust service in " + rootLabel, Profile: ProfileWorker, Confidence: ConfidenceMedium,
		Framework: "rust", Recipe: "rust",
		Evidence:      []DetectionEvidence{{Path: joinRoot(marker.root, "Cargo.toml"), Reason: "Cargo package manifest"}},
		NeedsDecision: []string{},
	}
	var crate *cargoCrate
	if marker.rust != nil {
		crate = marker.rust.crate
	}
	if manifest.workspace && !manifest.hasPkg {
		if marker.rust != nil && marker.rust.members > 0 {
			return DetectedCandidate{}, false
		}
		candidate.Confidence = ConfidenceLow
		candidate.RecipeIssue = "Cargo workspace without a root package; set the root directory to the member crate that builds the service, or use a Dockerfile"
		candidate.UnpinnedDependencies = !marker.cargoLock
		return candidate, true
	}
	toolchain := string(marker.rustToolchain)
	candidate.UnpinnedDependencies = !marker.cargoLock
	deps := manifest.deps
	if crate != nil {
		toolchain = crate.toolchain
		candidate.UnpinnedDependencies = crate.lock == nil
		deps = map[string]bool{}
		for name := range crate.deps {
			deps[name] = true
		}
		candidate.Rust = marker.rust.facts
	}
	if _, err := chooseRustToolchain(toolchain); err != nil {
		candidate.RecipeIssue = err.Error()
	}
	if manifest.name == "" {
		candidate.Confidence = ConfidenceLow
		candidate.NeedsDecision = append(candidate.NeedsDecision, "Cargo.toml names no package; confirm the binary the build produces")
	}
	binary := manifest.binary()
	if facts := candidate.Rust; facts != nil {
		binary = facts.Binary
		if workspace := facts.Workspace; workspace != "" {
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(strings.TrimPrefix(workspace, "."), "Cargo.toml"),
				Reason: detectionLine("member of the Cargo workspace at " + workspace + "; the build runs there with -p " + facts.Package)})
		}
		if binary == "" && len(facts.Binaries) > 1 {
			candidate.NeedsDecision = append(candidate.NeedsDecision,
				detectionLine("choose the binary to serve: "+strings.Join(facts.Binaries, ", ")+" (or set package.default-run in Cargo.toml)"))
		}
	} else if len(manifest.bins) > 1 && manifest.defaultRun == "" {
		candidate.NeedsDecision = append(candidate.NeedsDecision, "several binaries are declared; the first, "+binary+", is served — set package.default-run in Cargo.toml to choose another")
	}
	// A library crate's package name is no binary it builds, which the
	// repository-shape evidence already says.
	if facts := candidate.Rust; binary != "" && (facts == nil || len(facts.Binaries) > 0 || facts.BinaryReason == "package.default-run") {
		reason := "binary target " + binary
		if facts := candidate.Rust; facts != nil && facts.BinaryReason != "" && facts.BinaryReason != "package.default-run" {
			reason += ": " + facts.BinaryReason
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "Cargo.toml"), Reason: reason})
	}
	for _, framework := range rustWebFrameworks {
		if deps[framework.crate] {
			candidate.Framework = framework.name
			candidate.Profile, candidate.Port = ProfileWeb, framework.port
			candidate.Confidence = ConfidenceHigh
			candidate.Name = framework.name + " service in " + rootLabel
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "Cargo.toml"), Reason: framework.crate + " dependency; listens on " + fmt.Sprint(framework.port) + " by convention"})
			candidate.NeedsDecision = append(candidate.NeedsDecision, "confirm the port the service binds; the recipe passes PORT")
			break
		}
	}
	if crate != nil {
		rustFullstackCandidate(&candidate, marker, crate, rootLabel)
	}
	if candidate.Framework == "rust" {
		candidate.NeedsDecision = append(candidate.NeedsDecision, "confirm whether this binary serves HTTP (web application) or runs as a worker, and its port")
	}
	if procfileWeb := procfileProcess(marker.procfile, "web"); procfileWeb != "" && rejectPlanSecretLiteral("Procfile web process", procfileWeb) == nil {
		candidate.StartCommand = procfileWeb
		candidate.Profile = ProfileWeb
		if candidate.Port == 0 {
			candidate.Port = 8080
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "Procfile"), Reason: "web process: " + boundedEvidence(procfileWeb)})
	}
	if facts := candidate.Rust; facts != nil {
		rustBuildEvidence(&candidate, marker.root, facts)
	}
	if candidate.RecipeIssue != "" && candidate.Confidence == ConfidenceHigh {
		candidate.Confidence = ConfidenceMedium
	}
	return candidate, true
}

// rustFullstackCandidate reads the frameworks that build a browser side as
// well: cargo-leptos's server and site, a Trunk application's static
// WebAssembly site, and the two the recipe cannot build — Dioxus, which
// bundles with its own CLI, and Shuttle, whose runtime starts main.
func rustFullstackCandidate(candidate *DetectedCandidate, marker *detectedMarkers, crate *cargoCrate, rootLabel string) {
	cargo := joinRoot(marker.root, "Cargo.toml")
	confirm := "confirm the port the service binds; the recipe passes PORT"
	switch {
	case crate.leptos != nil:
		port := 3000
		if _, value, ok := strings.Cut(crate.leptos.siteAddr, ":"); ok {
			if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 && parsed < 65536 {
				port = parsed
			}
		}
		candidate.Framework, candidate.Profile, candidate.Port, candidate.Confidence = "leptos", ProfileWeb, port, ConfidenceHigh
		candidate.Name = "Leptos application in " + rootLabel
		candidate.NeedsDecision = withoutDecision(candidate.NeedsDecision, confirm)
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: cargo,
			Reason: fmt.Sprintf("cargo-leptos metadata: the build compiles the server and its WebAssembly site; site-addr port %d", port)})
	case crate.trunk != "":
		candidate.Framework, candidate.Profile, candidate.Port, candidate.Confidence = "trunk", ProfileStatic, 80, ConfidenceHigh
		candidate.OutputDirectory, candidate.SPAFallback = "dist", true
		candidate.Name = "Trunk site in " + rootLabel
		candidate.NeedsDecision = withoutDecision(candidate.NeedsDecision, confirm)
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "index.html"),
			Reason: "Trunk builds " + crate.trunk + " to WebAssembly and serves the result as a static site"})
	case crate.dioxus:
		candidate.Framework = "dioxus"
		candidate.RecipeIssue = "Dioxus builds with its own CLI (dx bundle), which the Rust recipe does not run; build from a Dockerfile that runs dx bundle"
	case crate.shuttle:
		candidate.Framework = "shuttle"
		candidate.RecipeIssue = "shuttle-runtime: main is started by Shuttle's runtime (#[shuttle_runtime::main]) and does not listen on its own; deploy it on Shuttle, or give main a tokio runtime that serves and remove the Shuttle attribute"
	}
}

// rustBuildEvidence says on the candidate what the recipe adds to the build.
func rustBuildEvidence(candidate *DetectedCandidate, root string, facts *DetectedRustBuild) {
	lock := joinRoot(strings.TrimPrefix(facts.Workspace, "."), "Cargo.lock")
	if facts.Workspace == "" {
		lock = joinRoot(root, "Cargo.lock")
	}
	if len(facts.NativeCrates) > 0 {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: lock,
			Reason: detectionLine("native build dependencies installed in the build stage: " + strings.Join(facts.NativeCrates, "; "))})
	}
	if facts.SQLxMigrate {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, "src"),
			Reason: "sqlx::migrate!() embeds the migrations; the application applies them when it starts"})
	}
	if facts.SQLxOffline {
		data := facts.SQLxOfflineData
		if data == "" {
			data = joinRoot(root, ".sqlx")
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: data,
			Reason: "sqlx offline query data is committed; the build compiles its queries with SQLX_OFFLINE=true"})
	}
}

func joinRoot(root, name string) string {
	if root == "" {
		return name
	}
	return strings.TrimSuffix(root, "/") + "/" + name
}
