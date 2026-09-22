package deploy

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// cargoManifest is the inert view of Cargo.toml detection reads: the package
// and binary names a build produces, whether the file is a workspace, and
// the crates it depends on.
type cargoManifest struct {
	name       string
	defaultRun string
	bins       []string
	workspace  bool
	hasPkg     bool
	deps       map[string]bool
}

var (
	cargoTableRE     = regexp.MustCompile(`^\[\[?([A-Za-z0-9_.\-"']+)\]\]?$`)
	cargoAssignRE    = regexp.MustCompile(`^([A-Za-z0-9_\-"']+)\s*=\s*(.*)$`)
	rustToolchainRE  = regexp.MustCompile(`\b(1\.[0-9]{2,3}(?:\.[0-9]+)?)\b`)
	rustPreReleaseRE = regexp.MustCompile(`\b(nightly|beta)\b`)
)

// parseCargoManifest is a line reader over the tables that matter, not a
// TOML parser: [package] name, [[bin]] name, [workspace], and the keys of
// [dependencies] (including `[dependencies.foo]` tables). An unusual layout
// yields fewer facts, never wrong ones.
func parseCargoManifest(content []byte) cargoManifest {
	manifest := cargoManifest{deps: map[string]bool{}}
	table := ""
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if comment := strings.Index(line, " #"); comment >= 0 {
			line = strings.TrimSpace(line[:comment])
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if match := cargoTableRE.FindStringSubmatch(line); match != nil {
			table = strings.Trim(match[1], `"'`)
			switch {
			case table == "package":
				manifest.hasPkg = true
			case table == "workspace":
				manifest.workspace = true
			case strings.HasPrefix(table, "dependencies."):
				manifest.deps[normalizeCrate(strings.TrimPrefix(table, "dependencies."))] = true
			case strings.HasPrefix(table, "bin"):
				manifest.bins = append(manifest.bins, "")
			}
			continue
		}
		match := cargoAssignRE.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		key, value := strings.Trim(match[1], `"'`), strings.TrimSpace(match[2])
		switch {
		case table == "package" && key == "name":
			manifest.name = strings.Trim(value, `"'`)
		case table == "package" && key == "default-run":
			manifest.defaultRun = strings.Trim(value, `"'`)
		case table == "bin" && key == "name" && len(manifest.bins) > 0 && manifest.bins[len(manifest.bins)-1] == "":
			manifest.bins[len(manifest.bins)-1] = strings.Trim(value, `"'`)
		case table == "dependencies":
			manifest.deps[normalizeCrate(key)] = true
		}
	}
	bins := manifest.bins[:0]
	for _, bin := range manifest.bins {
		if bin != "" {
			bins = append(bins, bin)
		}
	}
	manifest.bins = bins
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
	{"axum", "axum", 3000}, {"actix-web", "actix-web", 8080}, {"rocket", "rocket", 8000},
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

// rustCandidate builds the candidate for a root with a Cargo.toml.
func rustCandidate(marker *detectedMarkers, rootLabel string) DetectedCandidate {
	manifest := parseCargoManifest(marker.cargoToml)
	candidate := DetectedCandidate{
		Name: "Rust service in " + rootLabel, Profile: ProfileWorker, Confidence: ConfidenceMedium,
		Framework: "rust", Recipe: "rust",
		Evidence:      []DetectionEvidence{{Path: joinRoot(marker.root, "Cargo.toml"), Reason: "Cargo package manifest"}},
		NeedsDecision: []string{},
	}
	candidate.UnpinnedDependencies = !marker.cargoLock
	if _, err := chooseRustToolchain(string(marker.rustToolchain)); err != nil {
		candidate.RecipeIssue = err.Error()
	}
	switch {
	case manifest.workspace && !manifest.hasPkg:
		candidate.Confidence = ConfidenceLow
		candidate.RecipeIssue = "Cargo workspace without a root package; set the root directory to the member crate that builds the service, or use a Dockerfile"
	case manifest.name == "":
		candidate.Confidence = ConfidenceLow
		candidate.NeedsDecision = append(candidate.NeedsDecision, "Cargo.toml names no package; confirm the binary the build produces")
	}
	binary := manifest.binary()
	if len(manifest.bins) > 1 && manifest.defaultRun == "" {
		candidate.NeedsDecision = append(candidate.NeedsDecision, "several binaries are declared; the first, "+binary+", is served — set package.default-run in Cargo.toml to choose another")
	}
	if binary != "" {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "Cargo.toml"), Reason: "binary target " + binary})
	}
	names := make([]string, 0, len(manifest.deps))
	for name := range manifest.deps {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, framework := range rustWebFrameworks {
		if manifest.deps[framework.crate] {
			candidate.Framework = framework.name
			candidate.Profile, candidate.Port = ProfileWeb, framework.port
			candidate.Confidence = ConfidenceHigh
			candidate.Name = framework.name + " service in " + rootLabel
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "Cargo.toml"), Reason: framework.crate + " dependency; listens on " + fmt.Sprint(framework.port) + " by convention"})
			candidate.NeedsDecision = append(candidate.NeedsDecision, "confirm the port the service binds; the recipe passes PORT")
			break
		}
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
	if candidate.RecipeIssue != "" && candidate.Confidence == ConfidenceHigh {
		candidate.Confidence = ConfidenceMedium
	}
	return candidate
}

// rustRecipe is what the builder needs beyond the plan: the binary the build
// writes and the image tag for the pinned toolchain.
type rustRecipe struct {
	binary  string
	version string
	locked  bool
}

func selectRustRecipe(root string, config BuildPlanConfig) (rustRecipe, error) {
	content, err := readContainedRegular(root, "Cargo.toml", 512<<10)
	if err != nil {
		return rustRecipe{}, fmt.Errorf("%w: Rust recipe requires Cargo.toml", ErrUnsupportedBuilder)
	}
	manifest := parseCargoManifest(content)
	if manifest.workspace && !manifest.hasPkg {
		return rustRecipe{}, fmt.Errorf("%w: Cargo workspace without a root package; set the root directory to the member crate or use a Dockerfile", ErrUnsupportedBuilder)
	}
	binary := manifest.binary()
	if binary == "" {
		return rustRecipe{}, fmt.Errorf("%w: Cargo.toml names no package or binary to build", ErrUnsupportedBuilder)
	}
	if !safeRelativePath(binary) || strings.ContainsAny(binary, "/\\ ") {
		return rustRecipe{}, fmt.Errorf("%w: Cargo binary name %q is not a plain file name", ErrUnsupportedBuilder, binary)
	}
	toolchain := ""
	for _, name := range []string{"rust-toolchain.toml", "rust-toolchain"} {
		if regularExists(root, name) {
			file, err := readContainedRegular(root, name, 4096)
			if err == nil {
				toolchain = string(file)
			}
			break
		}
	}
	version, err := chooseRustToolchain(toolchain)
	if err != nil {
		return rustRecipe{}, err
	}
	return rustRecipe{binary: binary, version: version, locked: regularExists(root, "Cargo.lock")}, nil
}

func renderRustDockerfile(recipe rustRecipe, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	if len(bases) != 2 {
		return nil, ErrBuilderUnavailable
	}
	locked := ""
	if recipe.locked {
		locked = " --locked"
	}
	build := strings.TrimSpace(config.BuildCommand)
	if build == "" {
		build = "cargo build --release" + locked
	}
	lines := []string{
		"FROM " + immutableImageReference(bases[0]) + " AS build",
		"WORKDIR /src",
		// musl-dev links the static binary the alpine image targets; the
		// OpenSSL pieces let openssl-sys crates link statically too.
		"RUN apk add --no-cache musl-dev pkgconfig openssl-dev openssl-libs-static",
		"ENV OPENSSL_STATIC=1",
		"COPY . .",
		"RUN " + installSecrets + "cargo fetch" + locked,
		"RUN " + buildSecrets + build,
		"RUN mkdir -p /out && test -f /src/target/release/" + recipe.binary + " && cp /src/target/release/" + recipe.binary + " /out/app || (echo 'Rust build must produce target/release/" + recipe.binary + "; configure the build command and binary together' >&2; exit 1)",
		"FROM " + immutableImageReference(bases[1]),
		"RUN adduser -D -u 10001 app",
		"USER app",
		"COPY --from=build /out/app /app",
	}
	if strings.TrimSpace(config.StartCommand) == "" {
		lines = append(lines, `ENTRYPOINT ["/app"]`)
	} else {
		lines = append(lines, shellCMD(config.StartCommand))
	}
	return lines, nil
}

func joinRoot(root, name string) string {
	if root == "" {
		return name
	}
	return strings.TrimSuffix(root, "/") + "/" + name
}
