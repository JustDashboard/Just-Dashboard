package deploy

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The Rust recipe builds one binary of one crate: inside its Cargo workspace
// when it is a member, with the system packages its lockfile's native crates
// need, and with Cargo's own output layout. cargo-leptos applications and
// Trunk sites are built by their own tools on a Debian image, whose glibc the
// tools' prebuilt helpers (wasm-bindgen, dart-sass, wasm-opt) need.

// Pinned tool releases the Leptos and Trunk builds install from crates.io.
const (
	cargoLeptosVersion = "0.3.9"
	trunkVersion       = "0.21.14"
)

// rustRecipe is what the builder needs beyond the plan.
type rustRecipe struct {
	binary  string
	version string
	locked  bool
	// framework decides whether the image needs Rocket's address and its
	// default start Rocket's port bridge.
	framework string
	// assets are the root-level files the service reads at runtime.
	assets []string
	// context is the workspace root the build starts in, relative to the
	// checkout, and member the crate's directory inside it; pkg is the
	// crate's package, which -p selects there.
	context, member, pkg string
	// packageName is the crate's package name, which cargo-leptos names
	// the site's bundle after unless output-name says otherwise.
	packageName string
	native      rustNativePlan
	// output is where Cargo writes release binaries, relative to the
	// context: target/release, target/<triple>/release, or the configured
	// target-dir's release directory.
	output      string
	sqlxOffline bool
	leptos      *cargoLeptos
	trunk       string
	notes       []string
}

var (
	rustBinaryNameRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]{0,127}$`)
	rustOutputPathRE = regexp.MustCompile(`^/?[A-Za-z0-9_][A-Za-z0-9_./-]{0,255}$`)
)

func selectRustRecipe(boundary, root string, config BuildPlanConfig) (rustRecipe, error) {
	if !regularExists(root, "Cargo.toml") {
		return rustRecipe{}, fmt.Errorf("%w: Rust recipe requires Cargo.toml", ErrUnsupportedBuilder)
	}
	crate, err := readCargoCrate(boundary, root, nil)
	if err != nil {
		return rustRecipe{}, fmt.Errorf("%w: Rust recipe requires Cargo.toml: %v", ErrUnsupportedBuilder, err)
	}
	file := crate.file
	switch {
	case file.workspace && !file.hasPkg:
		return rustRecipe{}, fmt.Errorf("%w: Cargo workspace without a root package; set the root directory to the member crate that builds the service", ErrUnsupportedBuilder)
	case crate.dioxus:
		return rustRecipe{}, fmt.Errorf("%w: Dioxus builds with its own CLI (dx bundle), which the Rust recipe does not run; build from a Dockerfile", ErrUnsupportedBuilder)
	case crate.shuttle:
		return rustRecipe{}, fmt.Errorf("%w: shuttle-runtime starts main on Shuttle's runtime and it does not listen on its own; deploy it on Shuttle, or make main serve without the Shuttle attribute", ErrUnsupportedBuilder)
	}
	binary := crate.binary
	if config.CargoBin != "" {
		names := []string{}
		for _, target := range crate.binaries {
			names = append(names, target.name)
		}
		if !slices.Contains(names, config.CargoBin) && config.CargoBin != file.defaultRun {
			return rustRecipe{}, fmt.Errorf("%w: the configured Rust binary %s is not a binary target of %s (binaries: %s)",
				ErrUnsupportedBuilder, config.CargoBin, file.name, strings.Join(names, ", "))
		}
		binary = config.CargoBin
	}
	if binary == "" && len(crate.tied) > 0 {
		return rustRecipe{}, fmt.Errorf("%w: the crate has several binaries (%s); choose the one to serve in Build settings",
			ErrUnsupportedBuilder, strings.Join(crate.tied, ", "))
	}
	if binary == "" {
		return rustRecipe{}, fmt.Errorf("%w: Cargo.toml names no package or binary to build", ErrUnsupportedBuilder)
	}
	if !rustBinaryNameRE.MatchString(binary) {
		return rustRecipe{}, fmt.Errorf("%w: Cargo binary name %q is not a plain file name", ErrUnsupportedBuilder, binary)
	}
	version, err := chooseRustToolchain(crate.toolchain)
	if err != nil {
		return rustRecipe{}, err
	}
	recipe := rustRecipe{binary: binary, version: version, locked: crate.lock != nil, native: planRustNative(crate.lock),
		sqlxOffline: crate.sqlxData, leptos: crate.leptos, trunk: crate.trunk, packageName: file.name}
	if crate.lock != nil && rustLockOutgrowsToolchain(crate.lock.version, version) {
		recipe.notes = append(recipe.notes, fmt.Sprintf("Cargo.lock is version %d, which Rust %s cannot read; building with the current stable Rust", crate.lock.version, version))
		recipe.version = "1"
	}
	if stale := rustLockStale(crate); len(stale) > 0 {
		recipe.locked = false
		recipe.notes = append(recipe.notes, "Building without --locked: Cargo.lock does not resolve "+strings.Join(boundedNames(stale), ", "))
	}
	if crate.workspace != nil && crate.workspace.member != "" {
		recipe.context = rootLabelOf(checkoutPath(boundary, crate.workspace.dir))
		recipe.member, recipe.pkg = crate.workspace.member, file.name
		recipe.notes = append(recipe.notes, "Building "+file.name+" inside its Cargo workspace at "+recipe.context)
	}
	targetDir := "target"
	if crate.targetDir != "" {
		targetDir = strings.TrimSuffix(path.Clean(crate.targetDir), "/")
	}
	recipe.output = targetDir
	if crate.target != "" {
		recipe.output += "/" + crate.target
	}
	recipe.output += "/release"
	if !rustOutputPathRE.MatchString(recipe.output) || strings.Contains(recipe.output, "..") {
		return rustRecipe{}, fmt.Errorf("%w: .cargo/config sets a build target or target-dir the recipe cannot copy from (%s)", ErrUnsupportedBuilder, recipe.output)
	}
	if recipe.native.rustflags != "" && crate.rustflags {
		recipe.notes = append(recipe.notes, "RUSTFLAGS links libpq statically and replaces .cargo/config's build.rustflags")
	}
	for _, framework := range rustWebFrameworks {
		if crate.deps[framework.crate].crate != "" {
			recipe.framework = framework.name
			break
		}
	}
	if recipe.leptos != nil {
		recipe.framework = "leptos"
		if recipe.leptos.binTarget != "" && config.CargoBin == "" && rustBinaryNameRE.MatchString(recipe.leptos.binTarget) {
			recipe.binary = recipe.leptos.binTarget
		}
	}
	return recipe, nil
}

// rustDebianRelease is the Debian release the Leptos and Trunk builds run
// on, which every rust:<version> image since 1.66 is published for.
const rustDebianRelease = "bookworm"

// rustRecipeBases are the images the recipe resolves, in the order the
// Dockerfile refers to them.
func rustRecipeBases(recipe rustRecipe) []string {
	switch {
	case recipe.leptos != nil:
		return []string{"rust:" + recipe.version + "-" + rustDebianRelease, "debian:" + rustDebianRelease + "-slim"}
	case recipe.trunk != "":
		return []string{"rust:" + recipe.version + "-" + rustDebianRelease, recipeBaseCatalogue["static"][0]}
	}
	return []string{"rust:" + recipe.version + "-alpine", recipeBaseCatalogue["rust"][1]}
}

// rustBuildJobs caps Cargo's parallel jobs at the gigabytes the builder
// has free, read when the step runs: rustc peaks around a gigabyte per job
// in a release build, and a small host otherwise kills the build (exit 137).
const rustBuildJobs = `export CARGO_BUILD_JOBS="$(m=$(awk '/MemAvailable/ {print int($2/1048576)}' /proc/meminfo); c=$(nproc); j=$((m < c ? m : c)); echo $((j < 1 ? 1 : j)))" && `

func (r rustRecipe) cargoSelection() string {
	selection := " --bin " + r.binary
	if r.pkg != "" {
		selection = " -p " + r.pkg + selection
	}
	return selection
}

func renderRustDockerfile(recipe rustRecipe, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	if len(bases) != 2 {
		return nil, ErrBuilderUnavailable
	}
	switch {
	case recipe.leptos != nil:
		return renderLeptosDockerfile(recipe, config, bases, installSecrets, buildSecrets), nil
	case recipe.trunk != "":
		return renderTrunkDockerfile(recipe, config, bases, installSecrets, buildSecrets), nil
	}
	locked := ""
	if recipe.locked {
		locked = " --locked"
	}
	build := strings.TrimSpace(config.BuildCommand)
	if build == "" {
		build = "cargo build --release" + locked + recipe.cargoSelection()
	}
	env := append([]string(nil), recipe.native.env...)
	if recipe.native.rustflags != "" {
		env = append(env, `RUSTFLAGS="`+recipe.native.rustflags+`"`)
	}
	if recipe.sqlxOffline {
		// The committed query data is what the macros check against; a
		// DATABASE_URL in a committed .env or the build's variables would
		// otherwise send them to a database the build cannot reach.
		env = append(env, "SQLX_OFFLINE=true")
	}
	binary := "/src/" + recipe.output + "/" + recipe.binary
	if strings.HasPrefix(recipe.output, "/") {
		binary = recipe.output + "/" + recipe.binary
	}
	lines := []string{
		"FROM " + immutableImageReference(bases[0]) + " AS build",
		"WORKDIR /src",
		"RUN apk add --no-cache " + strings.Join(recipe.native.packages, " "),
		"ENV " + strings.Join(env, " "),
		"COPY . .",
		"RUN " + installSecrets + "cargo fetch" + locked,
		"RUN " + buildSecrets + rustBuildJobs + build,
		"RUN mkdir -p /out && test -f " + binary + " && cp " + binary + " /out/app || (echo 'Rust build must produce " +
			strings.TrimPrefix(binary, "/src/") + "; configure the build command and binary together' >&2; exit 1)",
	}
	start := config.StartCommand
	var runtimeEnv []string
	if recipe.framework == "rocket" {
		runtimeEnv = append(runtimeEnv, rocketAddressEnv)
		if strings.TrimSpace(start) == "" {
			start = rocketStart
		}
	}
	return append(lines, compiledRuntimeLines(bases[1], compiledRuntime{
		assets: recipe.assets, source: rustSourceDir(recipe), start: start, env: runtimeEnv,
	})...), nil
}

// rustSourceDir is where the crate sits in the build stage.
func rustSourceDir(recipe rustRecipe) string {
	if recipe.member != "" {
		return "/src/" + recipe.member
	}
	return "/src"
}

// rustDebianPackages are what a Debian build adds for the lockfile's native
// crates beyond the rust image's own toolchain, and the runtime libraries
// the glibc binary links dynamically.
func rustDebianPackages(native rustNativePlan) (build, runtime []string) {
	for _, crate := range native.crates {
		name, _, _ := strings.Cut(crate, " ")
		switch name {
		case "cmake":
			build = append(build, "cmake")
		case "bindgen", "clang-sys":
			build = append(build, "libclang-dev")
		case "prost-build", "tonic-build", "tonic-prost-build", "protobuf-codegen", "protoc-rust":
			build = append(build, "protobuf-compiler")
		case "pq-sys":
			runtime = append(runtime, "libpq5")
		case "mysqlclient-sys":
			runtime = append(runtime, "libmariadb3")
		case "libsqlite3-sys":
			runtime = append(runtime, "libsqlite3-0")
		}
	}
	return uniqueOrdered(build), uniqueOrdered(runtime)
}

func debianInstall(packages []string) string {
	return "apt-get update && apt-get install -y --no-install-recommends " + strings.Join(packages, " ") + " && rm -rf /var/lib/apt/lists/*"
}

// renderLeptosDockerfile builds with cargo-leptos, which compiles the
// server with its ssr features and the site's WebAssembly with hydrate, and
// runs the server beside the site it serves.
func renderLeptosDockerfile(recipe rustRecipe, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) []string {
	locked := ""
	if recipe.locked {
		locked = " --locked"
	}
	buildPackages, runtimePackages := rustDebianPackages(recipe.native)
	lines := []string{
		"FROM " + immutableImageReference(bases[0]) + " AS build",
		"WORKDIR /src",
		"RUN rustup target add wasm32-unknown-unknown",
	}
	if len(buildPackages) > 0 {
		lines = append(lines, "RUN "+debianInstall(buildPackages))
	}
	lines = append(lines, "RUN cargo install cargo-leptos --locked --version "+cargoLeptosVersion)
	if recipe.sqlxOffline {
		lines = append(lines, "ENV SQLX_OFFLINE=true")
	}
	build := strings.TrimSpace(config.BuildCommand)
	if build == "" {
		build = "cargo leptos build --release"
		if recipe.member != "" {
			project := recipe.pkg
			if recipe.leptos.name != "" {
				project = recipe.leptos.name
			}
			build += " --project " + project
		}
	}
	siteRoot := "target/site"
	if recipe.leptos.siteRoot != "" {
		siteRoot = strings.TrimSuffix(path.Clean(recipe.leptos.siteRoot), "/")
	}
	// cargo-leptos names the bundle after the project: output-name, else
	// the workspace project's name, else the package's.
	outputName := recipe.packageName
	if recipe.leptos.name != "" {
		outputName = recipe.leptos.name
	}
	if recipe.leptos.outputName != "" {
		outputName = recipe.leptos.outputName
	}
	binary := recipe.output + "/" + recipe.binary
	lines = append(lines,
		"COPY . .",
		"RUN "+installSecrets+"cargo fetch"+locked,
		"RUN "+buildSecrets+rustBuildJobs+build,
		"RUN mkdir -p /out && test -f "+binary+" && cp "+binary+" /out/app && cp -r "+siteRoot+" /out/site || (echo 'cargo leptos build must produce "+binary+" and "+siteRoot+"' >&2; exit 1)",
		"FROM "+immutableImageReference(bases[1]),
		"RUN "+debianInstall(append([]string{"ca-certificates", "tzdata"}, runtimePackages...))+" && useradd -u 10001 -m app",
		"USER app",
		"WORKDIR "+compiledRuntimeHome,
		"COPY --from=build /out/app /app",
		"COPY --from=build --chown=app:app /out/site "+compiledRuntimeHome+"/site",
	)
	for _, asset := range recipe.assets {
		if asset != "site" {
			lines = append(lines, "COPY --from=build --chown=app:app "+rustSourceDir(recipe)+"/"+asset+" "+compiledRuntimeHome+"/"+asset)
		}
	}
	env := []string{"LEPTOS_SITE_ROOT=site", "LEPTOS_ENV=PROD"}
	if outputName != "" && rustBinaryNameRE.MatchString(outputName) {
		env = append(env, "LEPTOS_OUTPUT_NAME="+outputName)
	}
	lines = append(lines, "RUN mkdir -p "+compiledRuntimeHome+"/data", "ENV "+strings.Join(env, " "))
	start := strings.TrimSpace(config.StartCommand)
	if start == "" {
		// site-addr is the development address, usually 127.0.0.1; the
		// environment outranks it and binds every interface on PORT.
		start = "exec env LEPTOS_SITE_ADDR=0.0.0.0:${PORT:-" + strconv.Itoa(leptosPort(recipe.leptos)) + "} /app"
	}
	return append(lines, shellCMD(start))
}

func leptosPort(leptos *cargoLeptos) int {
	if _, value, ok := strings.Cut(leptos.siteAddr, ":"); ok {
		if port, err := strconv.Atoi(value); err == nil && port > 0 && port < 65536 {
			return port
		}
	}
	return 3000
}

// renderTrunkDockerfile builds a Trunk application's WebAssembly site and
// serves it with the static site server.
func renderTrunkDockerfile(recipe rustRecipe, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) []string {
	locked := ""
	if recipe.locked {
		locked = " --locked"
	}
	buildPackages, _ := rustDebianPackages(recipe.native)
	lines := []string{
		"FROM " + immutableImageReference(bases[0]) + " AS build",
		"WORKDIR /src",
		"RUN rustup target add wasm32-unknown-unknown",
	}
	if len(buildPackages) > 0 {
		lines = append(lines, "RUN "+debianInstall(buildPackages))
	}
	build := strings.TrimSpace(config.BuildCommand)
	if build == "" {
		build = "trunk build --release --dist /out/dist"
	}
	lines = append(lines, "RUN cargo install trunk --locked --version "+trunkVersion, "COPY . .")
	if recipe.member != "" {
		lines = append(lines, "WORKDIR /src/"+recipe.member)
	}
	lines = append(lines,
		"RUN "+installSecrets+"cargo fetch"+locked,
		"RUN "+buildSecrets+rustBuildJobs+build,
		"RUN test -f /out/dist/index.html || (echo 'Trunk build must write /out/dist/index.html' >&2; exit 1)",
		"FROM "+immutableImageReference(bases[1]),
	)
	lines = append(lines, staticServerLines(true)...)
	return append(lines, "COPY --from=build /out/dist/ /usr/share/nginx/html/")
}
