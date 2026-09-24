package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func validNodePackageManager(manager string) bool {
	switch manager {
	case "bun", "npm", "pnpm", "yarn":
		return true
	}
	return false
}

// nodeInstallSource is a package's install inputs, read from the checkout:
// the facts the plan decides from, and the directory the install runs in.
// context is the package's own directory dir, or the workspace root whose
// lockfile installs it; both are slash paths inside the checkout ("" is its
// top).
type nodeInstallSource struct {
	facts        nodeInstallFacts
	context, dir string
}

// member is the package's path under its install context, "" when the
// package installs on its own.
func (s nodeInstallSource) member() string { return s.facts.member }

// readNodeInstallSource reads the package at dir inside the checkout at
// boundary. Reads never leave the checkout: they go through an os.Root, and
// a symlink is never followed to a file.
func readNodeInstallSource(boundary, dir, arch string, budget *nodeReadBudget) (nodeInstallSource, error) {
	root, err := os.OpenRoot(boundary)
	if err != nil {
		return nodeInstallSource{}, err
	}
	defer root.Close()
	return readNodeInstallSourceIn(root, dir, arch, budget)
}

func readNodeInstallSourceIn(root *os.Root, dir, arch string, budget *nodeReadBudget) (nodeInstallSource, error) {
	files := nodeFiles{root: root, dir: dir, budget: budget}
	manifest, err := files.read("package.json", nodeManifestMax)
	if err != nil {
		return nodeInstallSource{}, fmt.Errorf("%w: Node recipe requires package.json", ErrUnsupportedBuilder)
	}
	source := nodeInstallSource{context: dir, dir: dir}
	member := ""
	if ancestor, ok := nodeWorkspaceRoot(nodeFiles{root: root, budget: budget}, dir); ok {
		source.context = ancestor
		member = strings.TrimPrefix(strings.TrimPrefix(dir, ancestor), "/")
	}
	source.facts = readNodeInstallFacts(nodeFiles{root: root, dir: source.context, budget: budget}, member, manifest, arch)
	return source, nil
}

func nodeHasLockfile(files nodeFiles) bool {
	for _, lock := range nodeLockfileNames {
		if files.exists(lock.path) {
			return true
		}
	}
	return false
}

// nodeWorkspaceRoot finds the workspace a package without a lockfile of its
// own belongs to: the nearest ancestor that holds a lockfile and whose
// package.json workspaces, or pnpm-workspace.yaml packages, include it. A
// pnpm-workspace.yaml of settings alone is not a workspace — pnpm 10 writes
// one in single-package repositories for its build policy.
func nodeWorkspaceRoot(checkout nodeFiles, dir string) (string, bool) {
	if dir == "" || nodeHasLockfile(checkout.sub(dir)) {
		return "", false
	}
	for parent := path.Dir(dir); ; parent = path.Dir(parent) {
		if parent == "." {
			parent = ""
		}
		candidate := checkout.sub(parent)
		member := strings.TrimPrefix(strings.TrimPrefix(dir, parent), "/")
		if nodeHasLockfile(candidate) && nodeWorkspaceIncludes(nodeWorkspacePatterns(candidate), member) {
			return parent, true
		}
		if parent == "" {
			return "", false
		}
	}
}

func nodeWorkspacePatterns(files nodeFiles) []string {
	patterns := []string{}
	if content, err := files.read("package.json", nodeManifestMax); err == nil {
		var manifest struct {
			Workspaces json.RawMessage `json:"workspaces"`
		}
		if json.Unmarshal(content, &manifest) == nil && len(manifest.Workspaces) > 0 {
			var list []string
			var nested struct {
				Packages []string `json:"packages"`
			}
			if json.Unmarshal(manifest.Workspaces, &list) == nil {
				patterns = append(patterns, list...)
			} else if json.Unmarshal(manifest.Workspaces, &nested) == nil {
				patterns = append(patterns, nested.Packages...)
			}
		}
	}
	if content, err := files.read("pnpm-workspace.yaml", nodeConfigMaxBytes); err == nil {
		var workspace struct {
			Packages []string `yaml:"packages"`
		}
		if yaml.Unmarshal(content, &workspace) == nil {
			patterns = append(patterns, workspace.Packages...)
		}
	}
	return patterns
}

// nodeWorkspaceIncludes matches a member path against workspace globs the
// way npm, Yarn, pnpm and Bun do for the common forms: `*` within a
// segment, `**` across segments, and `!` exclusions.
func nodeWorkspaceIncludes(patterns []string, member string) bool {
	included := false
	for _, pattern := range patterns {
		exclude := strings.HasPrefix(pattern, "!")
		pattern = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(pattern, "!"), "./"), "/")
		if nodeGlobMatch(strings.Split(pattern, "/"), strings.Split(member, "/")) {
			included = !exclude
		}
	}
	return included
}

func nodeGlobMatch(pattern, segments []string) bool {
	if len(pattern) == 0 {
		return len(segments) == 0
	}
	if pattern[0] == "**" {
		for index := 0; index <= len(segments); index++ {
			if nodeGlobMatch(pattern[1:], segments[index:]) {
				return true
			}
		}
		return false
	}
	if len(segments) == 0 {
		return false
	}
	if matched, err := path.Match(pattern[0], segments[0]); err != nil || !matched {
		return false
	}
	return nodeGlobMatch(pattern[1:], segments[1:])
}

// nodeTargetArch names the architecture a platform's native npm packages
// are published for: x64 or arm64, from the build's target platform or,
// without one, this host.
func nodeTargetArch(platform string) string {
	arch := runtime.GOARCH
	if _, platformArch, found := strings.Cut(strings.ToLower(platform), "/"); found {
		arch, _, _ = strings.Cut(platformArch, "/")
	}
	switch arch {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	}
	return arch
}

// Package manifests are inert input. Detection and preparation never evaluate
// a repository's JavaScript configuration on the dashboard host.

// nodeRecipeFramework is what the recipe knows about a matched framework at
// build time: its catalogue name and the defaults its production build has,
// re-read from the manifest so the generated Dockerfile can check the build
// produced the server entry and give that server the environment it needs.
type nodeRecipeFramework struct {
	name       string
	label      string
	resolution nodeFrameworkResolution
}

func validateNodeRecipeContent(content []byte, files nodeRootFiles, config BuildPlanConfig) (nodeRecipeFramework, error) {
	var manifest nodeManifest
	if json.Unmarshal(content, &manifest) != nil {
		return nodeRecipeFramework{}, fmt.Errorf("%w: package.json is malformed", ErrUnsupportedBuilder)
	}
	framework := matchNodeFramework(manifest)
	if framework == nil {
		return nodeRecipeFramework{}, nil
	}
	runner := config.PackageManager
	if runner == "" {
		runner = "npm"
	}
	result := nodeRecipeFramework{name: framework.Name, label: framework.Label, resolution: framework.resolve(manifest, files, runner)}
	if framework.Name != "sveltekit" {
		return result, nil
	}
	if manifest.has("@sveltejs/adapter-node") == manifest.has("@sveltejs/adapter-static") {
		return nodeRecipeFramework{}, fmt.Errorf("%w: SvelteKit requires one of adapter-node or adapter-static; configure one supported adapter or use a Dockerfile", ErrUnsupportedBuilder)
	}
	if strings.TrimSpace(config.BuildCommand) == "" {
		return nodeRecipeFramework{}, fmt.Errorf("%w: SvelteKit needs a build command", ErrUnsupportedBuilder)
	}
	if manifest.has("@sveltejs/adapter-static") {
		if strings.TrimSpace(config.OutputDirectory) == "" {
			return nodeRecipeFramework{}, fmt.Errorf("%w: SvelteKit adapter-static needs its generated output directory (normally build)", ErrUnsupportedBuilder)
		}
		return result, nil
	}
	if config.OutputDirectory != "" {
		return nodeRecipeFramework{}, fmt.Errorf("%w: SvelteKit adapter-node produces a server; clear static output and set a server start command", ErrUnsupportedBuilder)
	}
	return result, nil
}

// checkoutPath is root's slash path inside the checkout at boundary.
func checkoutPath(boundary, root string) string {
	rel, err := filepath.Rel(boundary, root)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// settleBunImage keeps a declared Bun release only when its image exists:
// oven/bun publishes an alpine and a slim image for almost every 1.x
// release, and one that is missing falls back to the newest 1.x image with a
// logged note instead of failing the build on a registry lookup.
func (b *ArtifactBuilder) settleBunImage(ctx context.Context, plan *nodeInstallPlan) {
	fallback := bunImage("", plan.family)
	if plan.bun == "" || plan.bun == fallback {
		return
	}
	if _, err := b.backend.ResolveImage(ctx, plan.bun, ""); err == nil {
		return
	}
	plan.notes = append(plan.notes, "the declared Bun release has no "+plan.bun+" image; installing with "+fallback)
	plan.bun = fallback
	if plan.manager == "bun" {
		plan.toolchain = "bun 1 (the newest 1.x image; the declared release has no image)"
	}
}

// installInputs are the files the install reads from the build context, by
// their path in it.
func (s nodeInstallSource) installInputs(plan nodeInstallPlan) []string {
	inputs := []string{"package.json"}
	if member := s.member(); member != "" {
		inputs = append(inputs, path.Join(member, "package.json"))
	}
	if plan.lockfile != "" {
		inputs = append(inputs, plan.lockfile)
	}
	return append(inputs, s.facts.inputs...)
}

// dockerignore is the ignore file for the generated Dockerfile. BuildKit
// prefers <Dockerfile>.dockerignore over the repository's own, so it is the
// repository's file verbatim followed by exceptions for the install inputs
// the recipe reads: later lines win, the operator's exclusions stand, and a
// lockfile or registry configuration excluded by habit still reaches the
// install. A repository without a .dockerignore gets none.
func (r selectedRecipe) dockerignore() (string, []string) {
	if len(r.dockerignoreSource) == 0 || len(r.nodeInputs) == 0 {
		return "", nil
	}
	patterns := dockerignorePatterns(r.dockerignoreSource)
	var content strings.Builder
	content.Write(r.dockerignoreSource)
	if !strings.HasSuffix(content.String(), "\n") {
		content.WriteByte('\n')
	}
	content.WriteString("# Just Dashboard: the install inputs the recipe reads\n")
	notes := []string{}
	for _, input := range r.nodeInputs {
		directory := strings.HasSuffix(input, "/")
		input = strings.TrimSuffix(input, "/")
		if dockerignoreExcludes(patterns, input) {
			notes = append(notes, ".dockerignore excludes "+input+"; the build context keeps it because the install reads it")
		}
		content.WriteString("!" + input + "\n")
		if directory {
			content.WriteString("!" + input + "/**\n")
		}
	}
	return content.String(), notes
}

func dockerignorePatterns(content []byte) []string {
	patterns := []string{}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// dockerignoreExcludes applies .dockerignore's rules to one path: the last
// matching pattern decides, `!` re-includes, and a pattern that matches a
// directory excludes what is under it.
func dockerignoreExcludes(patterns []string, input string) bool {
	excluded := false
	segments := strings.Split(input, "/")
	for _, pattern := range patterns {
		negated := strings.HasPrefix(pattern, "!")
		pattern = strings.Trim(strings.TrimPrefix(pattern, "!"), "/")
		parts := strings.Split(path.Clean(pattern), "/")
		for length := 1; length <= len(segments); length++ {
			if nodeGlobMatch(parts, segments[:length]) {
				excluded = !negated
				break
			}
		}
	}
	return excluded
}

// renderNodeDockerfile renders the Node recipe: an optional toolchain stage
// with the plan's pinned manager releases (and Bun beside Node), a build
// stage that installs as planned and runs the build command in the
// package's directory, then nginx for static output or the toolchain again
// for a server, so a start command that runs pnpm, yarn or bun finds the
// same release offline.
func renderNodeDockerfile(recipe selectedRecipe, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	plan := recipe.nodeInstall
	lines, base, err := nodeInstallStage(plan, bases[0], bases, "toolchain", "build", installSecrets)
	if err != nil {
		return nil, err
	}
	workdir, binPath := "/app", "/app/node_modules/.bin"
	if recipe.member != "" {
		workdir = "/app/" + recipe.member
		binPath = workdir + "/node_modules/.bin:" + binPath
	}
	if recipe.member != "" {
		lines = append(lines, "WORKDIR "+workdir)
	}
	lines = append(lines, "ENV PATH="+binPath+":$PATH")
	for _, run := range plan.image.buildRuns {
		lines = append(lines, "RUN "+run)
	}
	if plan.prisma.generate != "" {
		lines = append(lines, nodeRunWith(buildSecrets, plan.prisma.defaults, plan.prisma.generate))
	}
	if command := strings.TrimSpace(config.BuildCommand); command != "" {
		lines = append(lines, nodeRunWith(buildSecrets, plan.buildDefaults(), command))
	}
	defaults := recipe.node.resolution.nodeFrameworkDefaults
	if strings.TrimSpace(config.OutputDirectory) == "" && defaults.Entry != "" && frameworkDefaultStart(config.StartCommand, defaults.Start) {
		// The framework's own entrypoint is what the start command runs, so
		// a build that did not write it is a wrong output path, and that
		// is a build failure with a name rather than a readiness timeout.
		lines = append(lines, "RUN test -f "+workdir+"/"+defaults.Entry+" || (echo '"+recipe.node.label+" must produce "+defaults.Entry+"; configure its output and start command together' >&2; exit 1)")
	}
	if output := strings.TrimSpace(config.OutputDirectory); output != "" {
		static, err := resolveCatalogueImage(bases, recipeBaseCatalogue["static"][0])
		if err != nil {
			return nil, err
		}
		lines = append(lines, "FROM "+immutableImageReference(static))
		lines = append(lines, staticServerLines(config.SPAFallback)...)
		return append(lines, "COPY --from=build "+workdir+"/"+filepath.ToSlash(output)+"/ /usr/share/nginx/html/"), nil
	}
	if strings.TrimSpace(config.StartCommand) == "" {
		return nil, fmt.Errorf("%w: Node service recipe requires a start command", ErrUnsupportedBuilder)
	}
	lines = append(lines, "FROM "+base)
	if packages := plan.image.runtimePackages; len(packages) > 0 {
		lines = append(lines, nodePackagesLine(plan.family, packages))
	}
	lines = append(lines, "WORKDIR "+workdir, "ENV NODE_ENV=production")
	if len(plan.corepack) > 0 {
		// The releases are installed; a start must never download one.
		lines = append(lines, "ENV COREPACK_ENABLE_NETWORK=0")
	}
	lines = append(lines, "ENV PATH="+binPath+":$PATH")
	for _, env := range append(append([]string(nil), plan.image.runtimeEnv...), defaults.Env...) {
		lines = append(lines, "ENV "+env)
	}
	lines = append(lines, "COPY --from=build /app /app")
	for _, run := range plan.image.runtimeRuns {
		lines = append(lines, "RUN "+run)
	}
	return append(lines, shellCMD(config.StartCommand)), nil
}

// nodeInstallStage renders what the Node recipe and the PHP recipe's asset
// stage share, so the two cannot install differently: the toolchain stage
// when the plan needs one, then a stage that copies the source and runs the
// planned install. base is what a later stage starts from to have the same
// tools.
func nodeInstallStage(plan nodeInstallPlan, node ResolvedImage, bases []ResolvedImage, toolchainStage, stage, installSecrets string) ([]string, string, error) {
	bun := ""
	if plan.bun != "" {
		image, err := resolveCatalogueImage(bases, plan.bun)
		if err != nil {
			return nil, "", err
		}
		bun = immutableImageReference(image)
	}
	lines := []string{}
	base := immutableImageReference(node)
	if toolchain := plan.toolchainLines(bun); len(toolchain) > 0 {
		lines = append(lines, "FROM "+base+" AS "+toolchainStage)
		lines = append(lines, toolchain...)
		base = toolchainStage
	}
	lines = append(lines, "FROM "+base+" AS "+stage)
	if packages := plan.image.buildPackages; len(packages) > 0 {
		lines = append(lines, nodePackagesLine(plan.family, packages))
	}
	for _, env := range plan.image.buildEnv {
		lines = append(lines, "ENV "+env)
	}
	lines = append(lines, "WORKDIR /app", "COPY . .")
	if plan.berry {
		lines = append(lines, "ENV YARN_ENABLE_GLOBAL_CACHE=false")
	}
	return append(lines, nodeRunWith(installSecrets, plan.installDefaults(), plan.installLine())), base, nil
}

// readNodeInstalls reads the install inputs of every package detection
// found, after the walk and under a budget of its own: comparing lockfiles
// is the install's evidence, never a reason to call a scan truncated. A
// Laravel or Symfony application's package.json is read in its own
// directory, since the PHP recipe's asset stage builds there.
func readNodeInstalls(checkout string, markers map[string]*detectedMarkers) {
	root, err := os.OpenRoot(checkout)
	if err != nil {
		return
	}
	defer root.Close()
	budget := newNodeReadBudget()
	arch := nodeTargetArch("")
	dirs := make([]string, 0, len(markers))
	for dir, marker := range markers {
		if len(marker.packageJSON) > 0 {
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		marker := markers[dir]
		slash := filepath.ToSlash(marker.root)
		var source nodeInstallSource
		if marker.phpOwnsAssets() {
			files := nodeFiles{root: root, dir: slash, budget: budget}
			source = nodeInstallSource{context: slash, dir: slash,
				facts: readNodeInstallFacts(files, "", marker.packageJSON, arch)}
		} else if source, err = readNodeInstallSourceIn(root, slash, arch, budget); err != nil {
			continue
		}
		marker.node = &source
	}
}

// withInstallVariables adds the registry credentials a package manager's
// configuration names to the variables the source reads, marking a name
// both list as needed by the install.
func withInstallVariables(variables, install []DetectedVariable) []DetectedVariable {
	result := append([]DetectedVariable(nil), variables...)
	for _, variable := range install {
		index := slices.IndexFunc(result, func(existing DetectedVariable) bool { return existing.Name == variable.Name })
		if index < 0 {
			if len(result) < 64 {
				result = append(result, variable)
			}
			continue
		}
		result[index].Step, result[index].InstallRequired = variable.Step, variable.InstallRequired
		for _, source := range variable.Sources {
			if !slices.Contains(result[index].Sources, source) && len(result[index].Sources) < 8 {
				result[index].Sources = append(result[index].Sources, source)
			}
		}
	}
	return result
}
