package deploy

import (
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The Elixir recipe builds a mix release — Phoenix, a plain Plug or Bandit
// service, or any OTP application — on the official elixir:<release>-otp-
// <otp>-slim image and runs it on that same image. A release carries the
// ERTS it was built with, linked against the builder's libcrypto and
// ncurses, so the runtime stage starts from the builder's own digest rather
// than a Debian base whose release might differ.

// elixirRecipeReleases are the Elixir families the catalogue builds on and
// the OTP majors the official image publishes for each, the reviewed one
// first.
var elixirRecipeReleases = []struct {
	family string
	otps   []string
}{
	{"1.17", []string{"27", "26", "25"}},
	{"1.18", []string{"28", "27", "26", "25"}},
	{"1.19", []string{"28", "27", "26"}},
	{"1.20", []string{"28", "29", "27"}},
}

const elixirDefaultFamily = "1.19"

var (
	mixElixirRE        = regexp.MustCompile(`\belixir:\s*"([^"]+)"`)
	mixUmbrellaRE      = regexp.MustCompile(`\bapps_path:\s*"([^"]+)"`)
	mixUmbrellaChildRE = regexp.MustCompile(`\bbuild_path:\s*"\.\./`)
	mixDefaultRelease  = regexp.MustCompile(`\bdefault_release:\s*:([a-z_][a-z0-9_]*)`)
	mixAliasRE         = regexp.MustCompile(`["']?(assets\.(?:deploy|setup))["']?\s*:`)
	mixOrganizationRE  = regexp.MustCompile(`\borganization:\s*"`)
	elixirOTPSuffixRE  = regexp.MustCompile(`-otp-(\d+)`)
	elixirMigrateRE    = regexp.MustCompile(`(?m)^\s*defmodule\s+([A-Z][\w.]*)\s+do[\s\S]*?\bdef\s+migrate\b`)
	elixirPortLiteral  = regexp.MustCompile(`\bport:\s*(\d{2,5})\b`)
	elixirReadsPortRE  = regexp.MustCompile(`System\.(?:get_env|fetch_env!?)\(\s*"PORT"`)
	buildpackVersionRE = regexp.MustCompile(`(?m)^\s*(elixir|erlang)_version\s*=\s*"?([0-9][0-9.]*)`)
)

// elixirVersionChoice is the image a project builds on.
type elixirVersionChoice struct {
	family, exact, otp, source string
}

func (c elixirVersionChoice) image() string {
	return "elixir:" + firstNonEmpty(c.exact, c.family) + "-otp-" + c.otp + "-slim"
}

func (c elixirVersionChoice) release() string {
	return firstNonEmpty(c.exact, c.family) + "-otp-" + c.otp
}

// chooseElixirRecipeVersion reads .tool-versions (`elixir 1.17.3-otp-27`,
// `erlang 27.1.2`), then .elixir-version, then the Elixir buildpack's
// config, with mix.exs's `elixir:` requirement as the floor the choice must
// meet. Without a declaration it builds on the reviewed family, or the
// newest one the requirement allows.
func chooseElixirRecipeVersion(toolVersions, versionFile, buildpack, mix []byte) (elixirVersionChoice, error) {
	requirement := ""
	if match := mixElixirRE.FindSubmatch(mix); match != nil {
		requirement = string(match[1])
	}
	constraint, ok := parseVersionConstraint(requirement)
	if !ok {
		constraint = nil
	}
	declared, otp, source := "", "", ""
	if entry := toolVersionsEntry(toolVersions, "elixir"); entry != "" {
		declared, source = entry, ".tool-versions"
	} else if line := firstMeaningfulLine(string(versionFile)); line != "" {
		declared, source = line, ".elixir-version"
	}
	if erlang := toolVersionsEntry(toolVersions, "erlang"); erlang != "" {
		otp = strings.SplitN(erlang, ".", 2)[0]
	}
	for _, match := range buildpackVersionRE.FindAllSubmatch(buildpack, -1) {
		switch string(match[1]) {
		case "elixir":
			if declared == "" {
				declared, source = string(match[2]), "elixir_buildpack.config"
			}
		case "erlang":
			if otp == "" {
				otp = strings.SplitN(string(match[2]), ".", 2)[0]
			}
		}
	}
	if match := elixirOTPSuffixRE.FindStringSubmatch(declared); match != nil {
		otp = match[1]
	}
	families := make([]string, 0, len(elixirRecipeReleases))
	for _, release := range elixirRecipeReleases {
		families = append(families, release.family)
	}
	otpsOf := func(family string) []string {
		for _, release := range elixirRecipeReleases {
			if release.family == family {
				return release.otps
			}
		}
		return nil
	}
	choice := elixirVersionChoice{source: source}
	if declared != "" {
		version, parts, ok := parseLanguageVersion(declared)
		family := fmt.Sprintf("%d.%d", version[0], version[1])
		if !ok || !slices.Contains(families, family) {
			return elixirVersionChoice{}, fmt.Errorf("%w: the Elixir recipe builds on Elixir %s; %s names %s — use a Dockerfile for other releases",
				ErrUnsupportedBuilder, strings.Join(families, ", "), source, boundedText(declared, 32))
		}
		choice.family = family
		if parts == 3 {
			choice.exact = version.String()
		}
		if len(constraint) > 0 && ((parts == 3 && !constraint.allows(version)) || (parts < 3 && !constraint.allowsFamily(family))) {
			return elixirVersionChoice{}, fmt.Errorf("%w: %s names Elixir %s, but mix.exs requires elixir %s; make them agree",
				ErrUnsupportedBuilder, source, boundedText(declared, 32), boundedText(requirement, 64))
		}
	} else {
		candidates := []string{elixirDefaultFamily}
		for index := len(families) - 1; index >= 0; index-- {
			candidates = append(candidates, families[index])
		}
		for _, family := range candidates {
			if len(constraint) == 0 || constraint.allowsFamily(family) {
				choice.family = family
				break
			}
		}
		if choice.family == "" {
			return elixirVersionChoice{}, fmt.Errorf("%w: mix.exs requires elixir %s, and the Elixir recipe builds on %s — use a Dockerfile",
				ErrUnsupportedBuilder, boundedText(requirement, 64), strings.Join(families, ", "))
		}
		if requirement != "" {
			choice.source = "mix.exs"
		}
	}
	otps := otpsOf(choice.family)
	switch {
	case otp == "":
		choice.otp = otps[0]
	case slices.Contains(otps, otp):
		choice.otp = otp
	default:
		return elixirVersionChoice{}, fmt.Errorf("%w: Elixir %s is published for Erlang/OTP %s; this project names OTP %s — use a Dockerfile",
			ErrUnsupportedBuilder, choice.family, strings.Join(otps, ", "), boundedText(otp, 8))
	}
	return choice, nil
}

// elixirProject is what the recipe and detection read from a mix root.
type elixirProject struct {
	mix        []byte
	app        string
	deps       map[string]string
	locked     bool
	version    elixirVersionChoice
	versionErr error
	// release is the release mix builds and starts; releaseErr says why
	// there is none to name.
	release    string
	releaseErr error
	// releaseNamed says mix.exs configures releases, so the build names
	// the one it assembles.
	releaseNamed bool
	phoenix      bool
	web          bool
	aliases      map[string]bool
	// assetApps are an umbrella's applications whose own mix.exs defines
	// asset aliases, which run from their directory.
	assetApps []mixAssetApp
	// assets is the npm package under assets/ the build installs, when it
	// has one; serverOverlay and migrateOverlay are phx.gen.release's
	// scripts.
	assets                        bool
	serverOverlay, migrateOverlay bool
	migrations                    bool
	migrateModule                 string
	// migrateApps are the umbrella's applications with migrations, whose
	// repositories the start migrates.
	migrateApps []string
	port        int
	readsPort   bool
	privateHex  bool
}

func readElixirProject(root string) elixirProject {
	project := elixirProject{mix: readRecipeFile(root, "mix.exs", 256<<10), deps: map[string]string{}, aliases: map[string]bool{}}
	project.locked = regularExists(root, "mix.lock")
	for _, match := range mixDependencyRE.FindAllSubmatch(project.mix, -1) {
		project.deps[string(match[1])] = ""
	}
	if match := mixAppRE.FindSubmatch(project.mix); match != nil {
		project.app = string(match[1])
	}
	project.version, project.versionErr = chooseElixirRecipeVersion(readRecipeFile(root, ".tool-versions", 16<<10),
		readRecipeFile(root, ".elixir-version", 4096), readRecipeFile(root, "elixir_buildpack.config", 16<<10), project.mix)
	project.release, project.releaseErr = mixReleaseName(project.mix, project.app)
	project.releaseNamed = project.releaseErr == nil && (mixDefaultRelease.Match(project.mix) || len(mixReleases(project.mix)) > 0)
	for _, match := range mixAliasRE.FindAllSubmatch(project.mix, -1) {
		project.aliases[string(match[1])] = true
	}
	// The directories whose priv/ and lib/ hold migrations and a Release
	// module: the root, or an umbrella's applications, by their names.
	sourceApps := map[string]string{".": project.app}
	if match := mixUmbrellaRE.FindSubmatch(project.mix); match != nil && safeRelativePath(string(match[1])) {
		// An umbrella's root lists no dependencies of its own: what it
		// serves is what its applications declare.
		sourceApps = map[string]string{}
		children, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(string(match[1])), "*", "mix.exs"))
		for index, child := range children {
			if index >= 16 {
				break
			}
			relative, err := filepath.Rel(root, child)
			if err != nil {
				continue
			}
			content := readRecipeFile(root, filepath.ToSlash(relative), 256<<10)
			for _, dependency := range mixDependencyRE.FindAllSubmatch(content, -1) {
				project.deps[string(dependency[1])] = ""
			}
			if name := mixAppRE.FindSubmatch(content); name != nil {
				sourceApps[filepath.ToSlash(filepath.Dir(relative))] = string(name[1])
			}
			app := mixAssetApp{dir: filepath.ToSlash(filepath.Dir(relative)), aliases: map[string]bool{}}
			for _, alias := range mixAliasRE.FindAllSubmatch(content, -1) {
				app.aliases[string(alias[1])] = true
			}
			if len(app.aliases) > 0 && safeRelativePath(app.dir) && !strings.ContainsAny(app.dir, " '\"$;&|") {
				project.assetApps = append(project.assetApps, app)
			}
		}
	}
	project.phoenix = has(project.deps, "phoenix")
	project.web = project.phoenix || has(project.deps, "plug_cowboy", "bandit", "cowboy")
	project.assets = regularExists(root, "assets/package.json")
	project.serverOverlay = regularExists(root, "rel/overlays/bin/server")
	project.migrateOverlay = regularExists(root, "rel/overlays/bin/migrate")
	project.privateHex = mixOrganizationRE.Match(project.mix)
	if has(project.deps, "ecto_sql") {
		for _, dir := range slices.Sorted(maps.Keys(sourceApps)) {
			entries, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(dir), "priv", "*", "migrations", "*.exs"))
			if len(entries) > 0 {
				project.migrations = true
				if dir != "." && sourceApps[dir] != "" {
					project.migrateApps = append(project.migrateApps, sourceApps[dir])
				}
			}
			sources, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(dir), "lib", "*", "release.ex"))
			for _, source := range sources {
				relative, err := filepath.Rel(root, source)
				if err != nil || project.migrateModule != "" {
					continue
				}
				if match := elixirMigrateRE.FindSubmatch(readRecipeFile(root, filepath.ToSlash(relative), 64<<10)); match != nil {
					project.migrateModule = string(match[1])
				}
			}
		}
	}
	project.port = 4000
	if !project.phoenix {
		sources, _ := filepath.Glob(filepath.Join(root, "lib", "*", "application.ex"))
		sources = append(sources, filepath.Join(root, "lib", "application.ex"))
		for _, source := range sources {
			relative, err := filepath.Rel(root, source)
			if err != nil {
				continue
			}
			content := readRecipeFile(root, relative, 64<<10)
			if elixirReadsPortRE.Match(content) {
				project.readsPort = true
			}
			if match := elixirPortLiteral.FindSubmatch(content); match != nil {
				if port, err := strconv.Atoi(string(match[1])); err == nil && port > 0 && port < 65536 {
					project.port = port
				}
			}
		}
	}
	return project
}

// mixAssetApp is an umbrella application with asset aliases of its own.
type mixAssetApp struct {
	dir     string
	aliases map[string]bool
}

// mixReleaseName is the release the build assembles: the one
// default_release names, else the first one `releases:` lists, else the
// application's own, which `mix release` builds only when it is given no
// name. An umbrella has no application of its own to release.
func mixReleaseName(mix []byte, app string) (string, error) {
	if match := mixDefaultRelease.FindSubmatch(mix); match != nil {
		return string(match[1]), nil
	}
	if names := mixReleases(mix); len(names) > 0 {
		return names[0], nil
	}
	if mixUmbrellaRE.Match(mix) {
		return "", fmt.Errorf("%w: an umbrella project releases only what mix.exs lists under releases:; add a release naming the applications to run", ErrUnsupportedBuilder)
	}
	if mixUmbrellaChildRE.Match(mix) {
		return "", fmt.Errorf("%w: this application is part of the umbrella project above it, which builds it; deploy the umbrella's root", ErrUnsupportedBuilder)
	}
	if app == "" {
		return "", fmt.Errorf("%w: mix.exs names no app:, so the release has no name", ErrUnsupportedBuilder)
	}
	return app, nil
}

// mixReleases lists the names of a literal `releases:` keyword list, or of
// the one `defp releases` returns. Keys are read at the list's own depth,
// so a release's options are not taken for releases.
func mixReleases(mix []byte) []string {
	text := string(mix)
	start := strings.Index(text, "releases:")
	body := ""
	if start >= 0 {
		body = strings.TrimSpace(text[start+len("releases:"):])
	}
	if !strings.HasPrefix(body, "[") {
		index := regexp.MustCompile(`defp?\s+releases\s*(?:\(\s*\))?\s*do`).FindStringIndex(text)
		if index == nil {
			return nil
		}
		body = strings.TrimSpace(text[index[1]:])
		if !strings.HasPrefix(body, "[") {
			return nil
		}
	}
	names := []string{}
	depth := 0
	keyRE := regexp.MustCompile(`^([a-z_][a-z0-9_]*):\s`)
	for index := 0; index < len(body) && len(names) < 8; index++ {
		switch body[index] {
		case '[', '{', '(':
			depth++
			continue
		case ']', '}', ')':
			depth--
			if depth == 0 {
				return names
			}
			continue
		case '"':
			if end := strings.IndexByte(body[index+1:], '"'); end >= 0 {
				index += end + 1
			}
			continue
		}
		if depth == 1 && (index == 0 || strings.ContainsRune("[, \n\t", rune(body[index-1]))) {
			if match := keyRE.FindStringSubmatch(body[index:]); match != nil {
				names = append(names, match[1])
				index += len(match[1])
			}
		}
	}
	return names
}

// start is the command that runs the release: phx.gen.release's server
// script, else the release's own start, behind Ecto's migrations when the
// project has them and no release overlay runs them as the release command.
func (p elixirProject) start() string {
	if p.release == "" {
		return ""
	}
	start := "/app/bin/" + p.release + " start"
	if p.serverOverlay {
		start = "/app/bin/server"
	}
	if !p.migrations || p.migrateOverlay {
		return start
	}
	migrate := ""
	switch {
	case p.migrateModule != "":
		migrate = "/app/bin/" + p.release + ` eval "` + p.migrateModule + `.migrate"`
	case len(p.migrateApps) > 0:
		// An umbrella's release may leave an application out, which then
		// neither loads nor has repositories to migrate.
		apps := ":" + strings.Join(p.migrateApps, ", :")
		migrate = "/app/bin/" + p.release + " eval 'for app <- [" + apps + "], do: Application.load(app); Application.ensure_all_started(:ssl); " +
			"for app <- [" + apps + "], repo <- Application.get_env(app, :ecto_repos, []), do: {:ok, _, _} = Ecto.Migrator.with_repo(repo, &Ecto.Migrator.run(&1, :up, all: true))'"
	case p.app != "":
		// What phx.gen.release's Release.migrate does, for a project
		// without it: load the application and run every repository's
		// migrations up.
		migrate = "/app/bin/" + p.release + " eval 'Application.load(:" + p.app + "); Application.ensure_all_started(:ssl); " +
			"for repo <- Application.fetch_env!(:" + p.app + ", :ecto_repos), do: {:ok, _, _} = Ecto.Migrator.with_repo(repo, &Ecto.Migrator.run(&1, :up, all: true))'"
	default:
		return start
	}
	return migrate + " && exec " + start
}

// elixirCandidate is the recipe candidate for a mix root.
func elixirCandidate(buildRoot, root string, match *ecosystemMatch) DetectedCandidate {
	project := readElixirProject(buildRoot)
	candidate := DetectedCandidate{
		Name: match.label + " in " + rootLabelOf(root), Profile: ProfileService, Confidence: ConfidenceHigh,
		Framework: match.framework, Recipe: "elixir",
		Evidence:      []DetectionEvidence{},
		NeedsDecision: []string{},
		Toolchain:     &DetectedToolchain{Language: "elixir", Release: project.version.release(), From: project.version.source},
		StartCommand:  project.start(),
	}
	if project.phoenix && candidate.Framework != "phoenix" {
		candidate.Framework, candidate.Name = "phoenix", "Phoenix application in "+rootLabelOf(root)
	}
	if project.web {
		candidate.Profile, candidate.Port = ProfileWeb, project.port
		reason := "Phoenix listens on PORT, 4000 by default (config/runtime.exs)"
		if !project.phoenix {
			reason = "the Plug server listens on " + strconv.Itoa(project.port)
			if project.readsPort {
				reason += " or the PORT it reads"
			}
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, "mix.exs"), Reason: reason})
	} else {
		candidate.Confidence = ConfidenceMedium
		candidate.NeedsDecision = append(candidate.NeedsDecision, "confirm whether this release serves HTTP (web application) or runs as a worker, and its port")
	}
	switch {
	case project.versionErr != nil:
		candidate.RecipeIssue = recipeRefusalText(project.versionErr, "")
	case project.releaseErr != nil:
		candidate.RecipeIssue = recipeRefusalText(project.releaseErr, "")
	default:
		reason := "mix release " + project.release + " on Elixir " + project.version.release()
		if project.version.source != "" {
			reason += " (" + project.version.source + ")"
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, "mix.exs"), Reason: reason})
	}
	candidate.UnpinnedDependencies = !project.locked
	if project.aliases["assets.deploy"] {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, "mix.exs"), Reason: "the build runs mix assets.deploy"})
	}
	if project.migrateOverlay {
		candidate.ReleaseCommand = "bin/migrate"
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, "rel/overlays/bin/migrate"), Reason: "release command: bin/migrate, run in the release image before each release"})
	} else if project.migrations && strings.Contains(candidate.StartCommand, " eval ") {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, "mix.exs"), Reason: "Ecto migrations run before the release starts"})
	}
	if project.assets {
		if assets, err := planLanguageNodeAssets(buildRoot, "assets", nodeTargetArch(""), nodeInstallChoice{}); err == nil {
			facts := assets.source.facts
			candidate.PackageManagers, candidate.Lockfiles = facts.lockfileManagers(), facts.detectedLockfiles()
			candidate.NodeVersion = assets.plan.node.label()
			candidate.PackageManager = assets.plan.manager
			candidate.NodeInstalls = facts.detectedInstalls(assets.plan.manager, true, nil, func(runner string) (string, string) { return runner + " run build", "" })
			candidate.Variables = facts.registry
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, "assets/package.json"),
				Reason: "assets/ npm dependencies are installed in the Elixir build before mix assets.deploy"})
		}
	}
	if project.privateHex {
		candidate.Variables = withInstallVariables(candidate.Variables, []DetectedVariable{{Name: "HEX_API_KEY", Sources: []string{"mix.exs"}, Step: "install", InstallRequired: true}})
	}
	if candidate.RecipeIssue != "" {
		candidate.Confidence = ConfidenceLow
	}
	return candidate
}

// elixirRecipe is what the builder needs beyond the plan.
type elixirRecipe struct {
	version      elixirVersionChoice
	release      string
	releaseNamed bool
	assetApps    []mixAssetApp
	locked       bool
	phoenix      bool
	aliases      map[string]bool
	node         *languageNodeAssets
}

func selectElixirRecipe(root string, config BuildPlanConfig) (elixirRecipe, error) {
	if !regularExists(root, "mix.exs") {
		return elixirRecipe{}, fmt.Errorf("%w: Elixir recipe requires mix.exs", ErrUnsupportedBuilder)
	}
	project := readElixirProject(root)
	if project.versionErr != nil {
		return elixirRecipe{}, project.versionErr
	}
	if project.releaseErr != nil {
		return elixirRecipe{}, project.releaseErr
	}
	recipe := elixirRecipe{version: project.version, release: project.release, releaseNamed: project.releaseNamed, locked: project.locked,
		phoenix: project.phoenix, aliases: project.aliases, assetApps: project.assetApps}
	if project.assets {
		assets, err := planLanguageNodeAssets(root, "assets", nodeTargetArch(config.TargetPlatform),
			nodeInstallChoice{selected: config.PackageManager, nodeVersion: config.NodeVersion})
		if err != nil {
			return elixirRecipe{}, fmt.Errorf("%w: the Elixir recipe installs assets/package.json with Node: %s", ErrUnsupportedBuilder, strings.TrimPrefix(err.Error(), ErrUnsupportedBuilder.Error()+": "))
		}
		recipe.node = assets
	}
	return recipe, nil
}

// elixirRecipeBases lists the images in the order the Dockerfile names them.
func elixirRecipeBases(recipe elixirRecipe) []string {
	bases := []string{recipe.version.image()}
	if recipe.node != nil {
		bases = append(bases, recipe.node.bases()...)
	}
	return bases
}
