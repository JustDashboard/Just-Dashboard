package deploy

import (
	"encoding/json"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// A Node install used to run after `COPY . .`, so every commit — a one-line
// change to a page — reinstalled every dependency and wrote a new
// node_modules layer the runtime stage then copied, compressed and unpacked
// again. The recipe now copies only what the install reads (installInputs),
// installs, and copies the source after: a commit that changes no input
// reuses the install's layer, and the runtime stage copies node_modules as a
// layer of its own, so it is reused too.
//
// That order is only safe when the install reads nothing else, and a
// package manager runs code during an install. nodeInstallReach reads,
// before any build, everything that could reach past the inputs, and keeps
// the source-first order (with the reason in the run's notes) whenever one
// does:
//   - a lifecycle script (preinstall, install, postinstall, prepare and their
//     pre/post forms) in the root's, the member's or any workspace
//     member's package.json, except a few known to read only inputs, and a
//     binding.gyp beside one (npm runs `node-gyp rebuild` for it);
//   - a file:, link:, portal: or exec: dependency, resolution or override,
//     and a Yarn patch: outside .yarn/patches;
//   - Prisma, whose client generates from the schema in its postinstall;
//   - a pnpmfile, which is code, a configuration key naming another file
//     (a CA file, another npmrc), and pnpm's injected workspace packages,
//     which it copies rather than links;
//   - an npm lock linking a package to a directory whose package.json is
//     not an input;
//   - Yarn's committed cache (zero-installs), which lives in the source, or
//     a committed .pnp.cjs, which the install rewrites;
//   - a lockfile out of sync with package.json, which the install rewrites.
// A patched dependency's patch (pnpm's and Bun's patchedDependencies), a
// member's own .npmrc, Yarn's .env.yarn and a workspace member's committed
// bin file are added to the inputs instead.

// nodeInstallReach is what reading the install's manifests and settings
// found: why the install might read a file outside its inputs, and the
// files added to the inputs so it does not.
type nodeInstallReach struct {
	sourceFirst string
	extra       []string
	// yarnCache says .yarn/cache is committed, and pnp that .pnp.cjs is: a
	// Yarn Berry install reads the one from the source and rewrites the
	// other, which a later copy of the source would undo.
	yarnCache, pnp bool
}

// nodeInstallScripts are the scripts an install runs for the root and
// each workspace member.
var nodeInstallScripts = []string{"preinstall", "install", "postinstall", "preprepare", "prepare", "postprepare"}

// nodeInputOnlyScripts are lifecycle scripts known to read nothing beyond
// the install's inputs: Git hook installers (which find no .git in the
// build context, in either order) and patch-package over patches/.
var nodeInputOnlyScripts = regexp.MustCompile(`^(?:(?:is-ci \|\| )?husky(?: install)?(?: \|\| (?:true|exit 0))?|(?:npx )?patch-package|simple-git-hooks)$`)

// nodeLocalSpecRE matches a dependency specifier that reads a path of the
// checkout at install time.
var nodeLocalSpecRE = regexp.MustCompile(`^(?:file:|link:|portal:|exec:|git\+file:|\.{1,2}/|/|~/)`)

type nodeReachManifest struct {
	Scripts              map[string]string          `json:"scripts"`
	Dependencies         map[string]string          `json:"dependencies"`
	DevDependencies      map[string]string          `json:"devDependencies"`
	OptionalDependencies map[string]string          `json:"optionalDependencies"`
	PeerDependencies     map[string]string          `json:"peerDependencies"`
	Overrides            json.RawMessage            `json:"overrides"`
	Resolutions          map[string]string          `json:"resolutions"`
	Pnpm                 map[string]json.RawMessage `json:"pnpm"`
	PatchedDependencies  map[string]string          `json:"patchedDependencies"`
	DependenciesMeta     map[string]struct {
		Injected bool `json:"injected"`
	} `json:"dependenciesMeta"`
	Bin         json.RawMessage   `json:"bin"`
	Directories map[string]string `json:"directories"`
}

// readNodeInstallReach reads the install root at files: member is the
// package's directory under it, workspaceManifests every other member's
// package.json there.
func readNodeInstallReach(files nodeFiles, member string, workspaceManifests []string) nodeInstallReach {
	reach := nodeInstallReach{yarnCache: files.dirExists(".yarn/cache"), pnp: files.exists(".pnp.cjs")}
	because := func(reason string) {
		if reach.sourceFirst == "" {
			reach.sourceFirst = reason
		}
	}
	addInput := func(rel string) bool {
		rel = strings.TrimPrefix(path.Clean(rel), "./")
		if !safeRelativePath(rel) || !nodeLiteralPathRE.MatchString(rel) || !files.exists(rel) {
			return false
		}
		if !slices.Contains(reach.extra, rel) {
			reach.extra = append(reach.extra, rel)
		}
		return true
	}
	manifests := []string{"package.json"}
	if member != "" {
		manifests = append(manifests, path.Join(member, "package.json"))
		if files.exists(path.Join(member, ".npmrc")) {
			addInput(path.Join(member, ".npmrc"))
		}
	}
	manifests = append(manifests, workspaceManifests...)
	for index, manifestPath := range manifests {
		dir := path.Dir(manifestPath)
		if dir == "." {
			dir = ""
		}
		content, err := files.read(manifestPath, nodeManifestMax)
		if err != nil {
			because(manifestPath + " could not be read to check what its install runs")
			continue
		}
		var manifest nodeReachManifest
		if json.Unmarshal(content, &manifest) != nil {
			because(manifestPath + " could not be parsed to check what its install runs")
			continue
		}
		for _, script := range nodeInstallScripts {
			if command := strings.TrimSpace(manifest.Scripts[script]); command != "" && !nodeInputOnlyScripts.MatchString(command) {
				because(manifestPath + " has a " + script + " script, which the install runs and which may read any file")
			}
		}
		if files.exists(path.Join(dir, "binding.gyp")) && manifest.Scripts["install"] == "" {
			because(path.Join(dir, "binding.gyp") + " makes the install compile the package's own sources")
		}
		specs := []map[string]string{manifest.Dependencies, manifest.DevDependencies, manifest.OptionalDependencies, manifest.PeerDependencies, manifest.Resolutions}
		for _, raw := range []json.RawMessage{manifest.Overrides, manifest.Pnpm["overrides"]} {
			specs = append(specs, flatNodeOverrides(raw))
		}
		for _, set := range specs {
			for _, name := range slices.Sorted(maps.Keys(set)) {
				spec := set[name]
				spec = strings.TrimSpace(spec)
				if _, alias, ok := strings.Cut(spec, "@"); ok && strings.HasPrefix(spec, "npm:") {
					spec = alias
				}
				switch {
				case nodeLocalSpecRE.MatchString(spec):
					because(manifestPath + " installs " + name + " from a path of the repository (" + boundedFindingText(spec) + ")")
				case strings.Contains(spec, "patch:") && !strings.Contains(spec, ".yarn/patches/"):
					because(manifestPath + " patches " + name + " with a file outside .yarn/patches")
				}
			}
		}
		for _, name := range slices.Sorted(maps.Keys(manifest.DependenciesMeta)) {
			if manifest.DependenciesMeta[name].Injected {
				because(manifestPath + " injects " + name + ", which the install copies from the source")
			}
		}
		for _, name := range []string{"prisma", "@prisma/client"} {
			for _, set := range specs[:4] {
				if set[name] != "" {
					because(manifestPath + " depends on " + name + ", whose install generates from the schema in the source")
				}
			}
		}
		patches := map[string]string{}
		for name, file := range manifest.PatchedDependencies {
			patches[name] = file
		}
		if raw := manifest.Pnpm["patchedDependencies"]; len(raw) > 0 {
			var pnpm map[string]string
			if json.Unmarshal(raw, &pnpm) != nil {
				because(manifestPath + " has pnpm.patchedDependencies that could not be read")
			}
			for name, file := range pnpm {
				patches[name] = file
			}
		}
		for _, name := range slices.Sorted(maps.Keys(patches)) {
			file := patches[name]
			if !addInput(file) {
				because(manifestPath + " patches " + name + " with " + boundedFindingText(file) + ", which is not a plain file of the repository")
			}
		}
		if index == 0 {
			continue
		}
		// A workspace member's bin file is linked into node_modules/.bin by
		// the install; one the repository commits is there to be linked.
		if manifest.Directories["bin"] != "" {
			because(manifestPath + " declares directories.bin, which the install links from the source")
		}
		for _, target := range nodeBinTargets(manifest.Bin) {
			addInput(path.Join(dir, target))
		}
	}
	if content, err := files.read("pnpm-workspace.yaml", nodeConfigMaxBytes); err == nil {
		var workspace struct {
			PatchedDependencies map[string]string `yaml:"patchedDependencies"`
			Overrides           map[string]string `yaml:"overrides"`
			Pnpmfile            string            `yaml:"pnpmfile"`
			Inject              bool              `yaml:"injectWorkspacePackages"`
		}
		if yaml.Unmarshal(content, &workspace) != nil {
			because("pnpm-workspace.yaml could not be parsed to check what the install reads")
		}
		for _, name := range slices.Sorted(maps.Keys(workspace.PatchedDependencies)) {
			file := workspace.PatchedDependencies[name]
			if !addInput(file) {
				because("pnpm-workspace.yaml patches " + name + " with " + boundedFindingText(file) + ", which is not a plain file of the repository")
			}
		}
		for _, name := range slices.Sorted(maps.Keys(workspace.Overrides)) {
			spec := workspace.Overrides[name]
			if nodeLocalSpecRE.MatchString(strings.TrimSpace(spec)) {
				because("pnpm-workspace.yaml overrides " + name + " with a path of the repository")
			}
		}
		if workspace.Inject {
			because("pnpm-workspace.yaml injects workspace packages, which the install copies from the source")
		}
		if workspace.Pnpmfile != "" {
			because("pnpm-workspace.yaml names a pnpmfile, which is code the install runs")
		}
	}
	if files.exists(".pnpmfile.cjs") || files.exists(".pnpmfile.mjs") {
		because("a pnpmfile is code the install runs, which may read any file")
	}
	for _, config := range []struct {
		file string
		keys *regexp.Regexp
	}{
		{".npmrc", nodeNPMRCFileKeyRE}, {path.Join(member, ".npmrc"), nodeNPMRCFileKeyRE},
		{".yarnrc.yml", nodeYarnrcFileKeyRE}, {"bunfig.toml", nodeBunfigFileKeyRE},
	} {
		if content, err := files.read(config.file, nodeConfigMaxBytes); err == nil {
			if key := config.keys.FindStringSubmatch(string(content)); key != nil {
				because(config.file + " sets " + key[1] + ", which names another file the install reads")
			}
		}
	}
	if content, err := files.read(".yarnrc.yml", nodeConfigMaxBytes); err == nil {
		var yarnrc struct {
			YarnPath string `yaml:"yarnPath"`
			Plugins  []any  `yaml:"plugins"`
		}
		_ = yaml.Unmarshal(content, &yarnrc)
		if yarnrc.YarnPath != "" && !strings.HasPrefix(strings.TrimPrefix(yarnrc.YarnPath, "./"), ".yarn/releases/") {
			because(".yarnrc.yml runs Yarn from " + boundedFindingText(yarnrc.YarnPath) + ", outside .yarn/releases")
		}
		for _, plugin := range yarnrc.Plugins {
			pluginPath, _ := plugin.(string)
			if entry, ok := plugin.(map[string]any); ok {
				pluginPath, _ = entry["path"].(string)
			}
			if pluginPath != "" && !strings.HasPrefix(strings.TrimPrefix(pluginPath, "./"), ".yarn/plugins/") {
				because(".yarnrc.yml loads a plugin from " + boundedFindingText(pluginPath) + ", outside .yarn/plugins")
			}
		}
	}
	// Yarn 4 reads .env.yarn beside .yarnrc.yml.
	if files.exists(".env.yarn") {
		addInput(".env.yarn")
	}
	return reach
}

var (
	nodeNPMRCFileKeyRE  = regexp.MustCompile(`(?m)^\s*((?:cafile|certfile|keyfile|userconfig|globalconfig|pnpmfile|global-pnpmfile|init-module|prefix|inject-workspace-packages))\s*=`)
	nodeYarnrcFileKeyRE = regexp.MustCompile(`(?m)^\s*((?:httpsCaFilePath|caFilePath|httpsCertFilePath|httpsKeyFilePath|injectEnvironmentFiles|globalFolder|cacheFolder|pnpUnpluggedFolder|virtualFolder|installStatePath|patchFolder))\s*:`)
	nodeBunfigFileKeyRE = regexp.MustCompile(`(?m)^\s*((?:cafile|globalDir|globalBinDir))\s*=`)
	// nodeLiteralPathRE is what an input may be named to be copied by name:
	// no glob character, no quote, no whitespace.
	nodeLiteralPathRE = regexp.MustCompile(`^[A-Za-z0-9._@+/=,-]+/?$`)
)

// flatNodeOverrides lists an overrides object's specifiers by key, nested
// npm overrides ("." and per-parent objects) included.
func flatNodeOverrides(raw json.RawMessage) map[string]string {
	result := map[string]string{}
	var walk func(prefix string, value any, depth int)
	walk = func(prefix string, value any, depth int) {
		switch typed := value.(type) {
		case string:
			result[prefix] = typed
		case map[string]any:
			if depth > 8 {
				return
			}
			for key, child := range typed {
				walk(strings.TrimPrefix(prefix+">"+key, ">"), child, depth+1)
			}
		}
	}
	var value any
	if len(raw) > 0 && json.Unmarshal(raw, &value) == nil {
		walk("", value, 0)
	}
	return result
}

// nodeBinTargets are a manifest's bin files: a string, or a map of names.
func nodeBinTargets(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return []string{single}
	}
	var named map[string]string
	if json.Unmarshal(raw, &named) != nil {
		return nil
	}
	targets := make([]string, 0, len(named))
	for _, target := range named {
		targets = append(targets, target)
	}
	slices.Sort(targets)
	return targets
}

// installFirst reports whether the plan installs before the source is
// copied, and why not when it does not.
func (p nodeInstallPlan) installFirst() (bool, string) {
	if p.sourceFirst != "" {
		return false, p.sourceFirst
	}
	for _, input := range p.inputs {
		if !nodeLiteralPathRE.MatchString(input) || !safeRelativePath(strings.TrimSuffix(input, "/")) {
			return false, boundedFindingText(input) + " cannot be copied by name"
		}
	}
	return len(p.inputs) > 0, ""
}

// installOrderNote is what the run log says about the install's order.
func (p nodeInstallPlan) installOrderNote() string {
	if first, reason := p.installFirst(); !first {
		return "Dependencies install after the source is copied, so every commit reinstalls them: " + reason
	}
	return "Dependencies install before the source is copied, from " + strings.Join(boundedNames(p.inputs), ", ") +
		"; their layer is reused until one of these changes"
}

// nodeInstallLines are the install of a stage whose WORKDIR is /app: the
// inputs, the install and then the source, or the source first when the
// plan says so. dir is the install's directory under /app ("" for /app
// itself), where the plan's inputs sit.
func nodeInstallLines(plan nodeInstallPlan, dir, install string) []string {
	workdir := []string{}
	if dir != "" && dir != "." {
		workdir = append(workdir, "WORKDIR /app/"+dir)
	} else {
		dir = ""
	}
	settings := plan.yarnSettings()
	if first, _ := plan.installFirst(); !first {
		return slices.Concat([]string{"COPY . ."}, workdir, settings, []string{install})
	}
	sources := make([]string, 0, len(plan.inputs)+1)
	for _, input := range plan.inputs {
		sources = append(sources, path.Join(dir, input)+map[bool]string{true: "/"}[strings.HasSuffix(input, "/")])
	}
	encoded, _ := json.Marshal(append(sources, "./"))
	return slices.Concat([]string{"COPY --parents " + string(encoded)}, workdir, settings, []string{install, "COPY . /app/"})
}

// nodeModulesCopy is the runtime stage's copy of the build stage's /app:
// node_modules first, the root's and the member's, as layers of their own a
// source-only commit leaves unchanged, then the rest without them. A
// node_modules the install did not create is not created either.
func nodeModulesCopy(member string) []string {
	lines := []string{"COPY --from=build /app/node_module[s] /app/node_modules/"}
	exclude := "--exclude=node_modules"
	if member != "" {
		lines = append(lines, "COPY --from=build /app/"+member+"/node_module[s] /app/"+member+"/node_modules/")
		exclude += " --exclude=" + member + "/node_modules"
	}
	return append(lines, "COPY --from=build "+exclude+" /app /app")
}
