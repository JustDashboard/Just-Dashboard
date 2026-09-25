package deploy

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
)

// The site recipe builds a site generator that is its own program — Hugo,
// Zola, mdBook — or Jekyll on Ruby, and serves what it writes with nginx.
// MkDocs, Sphinx, Pelican and Zensical build on the Python recipe and Lume on
// the Deno recipe: each language recipe with an output directory builds, then
// serves that directory, the way the JavaScript recipe always has. Which
// generator builds is read from the commit being prepared, never from the
// plan, so the image follows the configuration files the source holds.

// siteRecipe is a site's build as the recipe prepares it.
type siteRecipe struct {
	generator siteGenerator
	// image is the generator's own image or the stage base it runs on;
	// runtime is the base a copied generator binary runs on (Zola).
	image, runtime string
	// binary is where the generator's image keeps its executable.
	binary string
	// node is the install Hugo Pipes' dependencies need, when package.json
	// declares any.
	node       *nodeInstallPlan
	nodeInputs []string
	notes      []string
	// Python sites: the install lines and the environment they need.
	pythonInstalls []string
	pythonEnv      []string
}

func (r siteRecipe) toolchain() string {
	switch r.generator.name {
	case "hugo":
		return "hugo " + r.generator.version + " (extended)"
	case "jekyll":
		return "jekyll on ruby " + r.generator.ruby
	}
	return r.generator.name + " " + r.generator.version
}

func selectSiteRecipe(boundary, root string, config BuildPlanConfig) (siteRecipe, error) {
	tree := containedSiteTree(boundary, root)
	generator, ok := readSiteGenerator(tree)
	if !ok || generator.recipe != "site" {
		return siteRecipe{}, fmt.Errorf("%w: the site recipe builds Hugo, Zola, mdBook and Jekyll sites, and the root holds none of their configuration (hugo.toml, config.toml with base_url, book.toml, a Jekyll _config.yml)", ErrUnsupportedBuilder)
	}
	if generator.issue != "" {
		return siteRecipe{}, fmt.Errorf("%w: %s", ErrUnsupportedBuilder, generator.issue)
	}
	if strings.TrimSpace(config.OutputDirectory) == "" {
		return siteRecipe{}, fmt.Errorf("%w: the site recipe serves the directory %s writes; set the output directory (%s)", ErrUnsupportedBuilder, generator.label(), generator.output)
	}
	recipe := siteRecipe{generator: generator}
	switch generator.name {
	case "hugo":
		recipe.image = "ghcr.io/gohugoio/hugo:v" + generator.version
		if generator.nodeDependencies {
			source, err := readNodeInstallSource(root, "", nodeTargetArch(config.TargetPlatform), newNodeReadBudget())
			if err != nil {
				return siteRecipe{}, err
			}
			plan := planNodeInstall(source.facts, nodeInstallChoice{selected: config.PackageManager, assets: true})
			if plan.blocked != nil {
				return siteRecipe{}, fmt.Errorf("%w: the Hugo recipe installs package.json for Hugo Pipes: %s", ErrUnsupportedBuilder, plan.blocked.Measured)
			}
			recipe.node, recipe.nodeInputs = &plan, source.installInputs(plan)
		}
	case "zola":
		recipe.image = "ghcr.io/getzola/zola:v" + generator.version
		recipe.runtime, recipe.binary = zolaRuntime(generator.version)
	case "mdbook":
		recipe.image = recipeBaseCatalogue["site:mdbook"][0]
	case "jekyll":
		recipe.image = "ruby:" + generator.ruby + "-slim"
	}
	return recipe, nil
}

// zolaRuntime is where a Zola release's binary runs: from 0.23 the official
// image carries a musl build at /zola, which Alpine runs; before, a glibc
// build at /bin/zola on distroless Debian, which Debian slim runs.
func zolaRuntime(version string) (string, string) {
	if compareSiteVersions(version, "0.23.0") >= 0 {
		return recipeBaseCatalogue["site:zola"][1], "/zola"
	}
	return recipeBaseCatalogue["site:zola"][2], "/bin/zola"
}

// bases are the images the rendered Dockerfile names, in the order it names
// them, the serving nginx last.
func (r siteRecipe) bases() []string {
	bases := []string{r.image}
	if r.runtime != "" {
		bases = append(bases, r.runtime)
	}
	if r.node != nil {
		bases = append(bases, r.node.baseImages(false)...)
	}
	return append(bases, recipeBaseCatalogue["static"]...)
}

// settleSiteImage keeps a declared generator release only when its official
// image exists; one that is missing (Hugo skips publishing the odd release)
// builds with the reviewed default, and the run log says so.
func (b *ArtifactBuilder) settleSiteImage(ctx context.Context, recipe *siteRecipe) {
	var fallback string
	switch recipe.generator.name {
	case "hugo":
		fallback = recipeBaseCatalogue["site:hugo"][0]
	case "zola":
		fallback = recipeBaseCatalogue["site:zola"][0]
	default:
		return
	}
	if recipe.image == fallback {
		return
	}
	if _, err := b.backend.ResolveImage(ctx, recipe.image, ""); err == nil {
		return
	}
	declared := recipe.generator.version
	recipe.image = fallback
	recipe.generator.version = strings.TrimPrefix(fallback[strings.LastIndex(fallback, ":")+1:], "v")
	if recipe.generator.name == "zola" {
		recipe.runtime, recipe.binary = zolaRuntime(recipe.generator.version)
	}
	recipe.notes = append(recipe.notes, recipe.generator.label()+" "+declared+" has no official image; building with "+recipe.generator.version)
}

func renderSiteDockerfile(recipe siteRecipe, config BuildPlanConfig, serving staticServing, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	image, err := resolveCatalogueImage(bases, recipe.image)
	if err != nil {
		return nil, err
	}
	command := strings.TrimSpace(config.BuildCommand)
	if command == "" {
		command = recipe.generator.build
	}
	var lines []string
	workdir := "/app"
	switch recipe.generator.name {
	case "hugo":
		if recipe.node != nil {
			node, err := resolveCatalogueImage(bases, recipe.node.nodeImage())
			if err != nil {
				return nil, err
			}
			stage, _, err := nodeInstallStage(*recipe.node, node, bases, "deps-toolchain", "deps", installSecrets)
			if err != nil {
				return nil, err
			}
			lines = append(lines, stage...)
		}
		// The image runs as its own hugo user, who has to own the site to
		// write public/ and the build lock.
		workdir = "/project"
		lines = append(lines, "FROM "+immutableImageReference(image)+" AS build", "WORKDIR "+workdir, "COPY --chown=hugo:hugo . .")
		if recipe.node != nil {
			lines = append(lines, "COPY --from=deps --chown=hugo:hugo /app/node_modules ./node_modules")
		}
	case "zola":
		runtime, err := resolveCatalogueImage(bases, recipe.runtime)
		if err != nil {
			return nil, err
		}
		lines = append(lines, "FROM "+immutableImageReference(image)+" AS zola",
			"FROM "+immutableImageReference(runtime)+" AS build",
			"COPY --from=zola "+recipe.binary+" /usr/local/bin/zola", "WORKDIR "+workdir, "COPY . .")
	case "mdbook":
		release := mdbookReleases[recipe.generator.mdbookLine]
		archive := "https://github.com/rust-lang/mdBook/releases/download/v" + release.version + "/mdbook-v" + release.version + "-${arch}-unknown-linux-musl.tar.gz"
		lines = append(lines, "FROM "+immutableImageReference(image)+" AS build", "ARG TARGETARCH",
			// The release archive is checked against its reviewed digest and
			// read no further than 64 MiB, so a changed or endless download
			// fails the step instead of reaching the image.
			`RUN case "$TARGETARCH" in amd64) arch=x86_64 sum=`+release.amd64Checksum+` ;; arm64) arch=aarch64 sum=`+release.arm64Checksum+
				` ;; *) echo "mdBook publishes no release for $TARGETARCH" >&2; exit 1 ;; esac`+
				` && wget -q -O - "`+archive+`" | head -c 67108864 > /tmp/mdbook.tar.gz`+
				` && echo "$sum  /tmp/mdbook.tar.gz" | sha256sum -c -`+
				` && tar -xzf /tmp/mdbook.tar.gz -C /usr/local/bin mdbook && rm /tmp/mdbook.tar.gz`,
			"WORKDIR "+workdir, "COPY . .",
			// Built for the root it is served from: site-url only sets the
			// 404 page's links, which a GitHub Pages sub-path would break.
			"ENV MDBOOK_OUTPUT__HTML__SITE_URL=/")
	case "jekyll":
		// The GitHub Pages gem asks GitHub's API about the repository while
		// it builds; unauthenticated and rate-limited, that answer is not
		// the build's to depend on.
		lines = append(lines, "FROM "+immutableImageReference(image)+" AS build",
			"RUN apt-get update && apt-get install -y --no-install-recommends build-essential git && rm -rf /var/lib/apt/lists/*",
			"WORKDIR "+workdir, "ENV LANG=C.UTF-8 JEKYLL_ENV=production PAGES_DISABLE_NETWORK=1",
			"RUN printf '%s\\n' 'url: \"\"' > "+jekyllURLOverride, "COPY . .")
		if !recipe.generator.gemfile {
			lines = append(lines, `RUN printf '%s\n' 'source "https://rubygems.org"' 'gem "github-pages", group: :jekyll_plugins' > Gemfile`)
		}
		install := "bundle install --jobs 4 --retry 3"
		switch {
		case recipe.generator.addLinuxPlatform:
			install = "bundle lock --add-platform x86_64-linux aarch64-linux && " + install
		case recipe.generator.gemLock:
			lines = append(lines, "ENV BUNDLE_FROZEN=true")
		}
		lines = append(lines, "RUN "+installSecrets+install)
	default:
		return nil, ErrUnsupportedBuilder
	}
	lines = append(lines, "RUN "+buildSecrets+command)
	source, err := staticOutputSource(workdir, config.OutputDirectory)
	if err != nil {
		return nil, err
	}
	stage, err := staticServingStage(bases, serving, "build", source)
	if err != nil {
		return nil, err
	}
	return append(lines, stage...), nil
}

// selectPythonSite prepares the Python recipe's static output: the site's
// own requirements when it has any, its documentation build's when Read the
// Docs names them, and otherwise the pinned releases its configuration
// needs. A root with no generator is a Python project that writes files: it
// installs from its manifests like any other and serves what its build
// command writes.
func selectPythonSite(boundary, root, version string, config BuildPlanConfig) (siteRecipe, error) {
	tree := containedSiteTree(boundary, root)
	generator, known := readSiteGenerator(tree)
	if known && generator.recipe != "python" {
		known = false
	}
	recipe := siteRecipe{generator: generator}
	if strings.TrimSpace(config.BuildCommand) == "" && !known {
		return siteRecipe{}, fmt.Errorf("%w: the Python recipe serves the output directory its build command writes; set a build command", ErrUnsupportedBuilder)
	}
	pip := "pip install --no-cache-dir "
	if rtd, ok := readTheDocsConfig(tree); ok && known && (len(rtd.requirements) > 0 || len(rtd.packages) > 0) {
		for _, requirements := range rtd.requirements {
			recipe.pythonInstalls = append(recipe.pythonInstalls, pip+"--requirement "+requirements)
		}
		for _, target := range rtd.packages {
			recipe.pythonInstalls = append(recipe.pythonInstalls, pip+"'"+target+"'")
		}
		recipe.generator.needsGit = generator.needsGit
		return recipe, nil
	}
	if known {
		places := []string{}
		if generator.sphinxSource != "" && generator.sphinxSource != "." {
			places = append(places, path.Join(generator.sphinxSource, "requirements.txt"))
		}
		for _, requirements := range append(places, pythonDocsRequirements...) {
			if tree.file(requirements) {
				recipe.pythonInstalls = append(recipe.pythonInstalls, pip+"--requirement "+requirements)
				return recipe, nil
			}
		}
	}
	declared := regularExists(root, "uv.lock") || regularExists(root, "poetry.lock") || regularExists(root, "requirements.txt")
	if !declared && regularExists(root, "pyproject.toml") {
		content, _ := tree.read("pyproject.toml")
		declared = !known || strings.Contains(strings.ToLower(string(content)), generator.name)
	}
	if declared || !known {
		install, err := selectPythonInstall(root, version)
		if err != nil {
			return siteRecipe{}, err
		}
		recipe.pythonInstalls, recipe.pythonEnv = []string{install.command}, install.env
		switch {
		case !known || len(generator.pythonPackages) == 0 || pythonMainDependency(tree, generator):
		case install.kind == "uv.lock" && pythonManifestNames(tree, generator.name, "uv.lock"),
			install.kind == "poetry.lock" && pythonManifestNames(tree, generator.name, "poetry.lock", "pyproject.toml"):
			// A library declares its documentation tool in a dependency group
			// (`[dependency-groups] docs`, `[tool.poetry.group.docs]`), which
			// an application's install leaves out; the site's build takes
			// every group the lock resolved.
			recipe.pythonInstalls = []string{pythonInstallAllGroups[install.kind]}
		default:
			// The project's own manifest does not install its documentation
			// tool; the pinned release does, into the same environment.
			recipe.pythonInstalls = append(recipe.pythonInstalls, strings.TrimSuffix(install.serverInstall, " ")+" "+
				strings.Join(pythonSitePackageList(generator.pythonPackages, version), " "))
		}
		return recipe, nil
	}
	if generator.issue != "" {
		return siteRecipe{}, fmt.Errorf("%w: %s", ErrUnsupportedBuilder, generator.issue)
	}
	recipe.pythonInstalls = []string{pip + strings.Join(pythonSitePackageList(generator.pythonPackages, version), " ")}
	return recipe, nil
}

func renderPythonSiteDockerfile(recipe siteRecipe, version string, config BuildPlanConfig, serving staticServing, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	command := strings.TrimSpace(config.BuildCommand)
	if command == "" {
		command = recipe.generator.build
	}
	lines := []string{"FROM " + immutableImageReference(bases[0]) + " AS build", "WORKDIR /app",
		"ENV PYTHONUNBUFFERED=1 PYTHONDONTWRITEBYTECODE=1 PIP_DISABLE_PIP_VERSION_CHECK=1 PIP_ROOT_USER_ACTION=ignore"}
	for _, env := range recipe.pythonEnv {
		lines = append(lines, "ENV "+env)
	}
	if recipe.generator.needsGit {
		// A plugin that dates pages from their commits runs git.
		lines = append(lines, "RUN apt-get update && apt-get install -y --no-install-recommends git && rm -rf /var/lib/apt/lists/*")
	}
	lines = append(lines, "COPY . .")
	for _, install := range recipe.pythonInstalls {
		lines = append(lines, "RUN "+installSecrets+install)
	}
	lines = append(lines, "RUN "+buildSecrets+command)
	source, err := staticOutputSource("/app", config.OutputDirectory)
	if err != nil {
		return nil, err
	}
	stage, err := staticServingStage(bases, serving, "build", source)
	if err != nil {
		return nil, err
	}
	return append(lines, stage...), nil
}

// denoSiteStage ends the Deno recipe's build stage in nginx serving its
// output, for a site generator such as Lume.
func denoSiteStage(config BuildPlanConfig, serving staticServing, bases []ResolvedImage) ([]string, error) {
	source, err := staticOutputSource("/app", config.OutputDirectory)
	if err != nil {
		return nil, err
	}
	return staticServingStage(bases, serving, "build", source)
}

// pythonInstallAllGroups are the lock installs of selectPythonInstall with
// every dependency group, for a site whose generator is in one.
var pythonInstallAllGroups = map[string]string{
	"uv.lock":     "pip install --no-cache-dir uv && uv sync --frozen --all-groups",
	"poetry.lock": "pip install --no-cache-dir poetry && poetry install --all-groups --no-root --no-interaction",
}

// pythonMainDependency says the project's own dependencies — requirements.txt,
// or the main list of pyproject.toml, never a group — install the generator
// or a theme or plugin that brings it.
func pythonMainDependency(tree siteTree, generator siteGenerator) bool {
	names := []string{}
	if content, ok := tree.read("requirements.txt"); ok {
		names = append(names, strings.Split(string(content), "\n")...)
	}
	if content, ok := tree.read("pyproject.toml"); ok {
		names = append(names, pyprojectDependencies(string(content))...)
	}
	for _, requirement := range names {
		match := pythonRequirementRE.FindStringSubmatch(strings.TrimSpace(requirement))
		if match == nil {
			continue
		}
		name := normalizePythonName(match[1])
		if strings.Contains(name, generator.name) || slices.Contains(generator.pythonPackages, name) {
			return true
		}
	}
	return false
}

// pythonManifestNames says whether the first of the manifests the root has
// names a package, by name as it appears in it: a lock when there is one,
// since a frozen install reads nothing else.
func pythonManifestNames(tree siteTree, name string, manifests ...string) bool {
	for _, manifest := range manifests {
		if content, ok := tree.read(manifest); ok {
			return strings.Contains(strings.ToLower(string(content)), name)
		}
	}
	return false
}
