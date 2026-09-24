package deploy

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// The Node install plan is computed once, here, from files read as data, and
// used identically by detection (which records it per package manager on the
// candidate), preflight (which judges the operator's choice from that
// record) and the recipe (which renders it). The three therefore cannot
// disagree about which lockfile is installed, whether the install is frozen,
// which manager release runs it, or why.

// nodeInstallManifest is the part of package.json the install reads.
type nodeInstallManifest struct {
	Name                 string                     `json:"name"`
	Scripts              map[string]string          `json:"scripts"`
	Dependencies         map[string]string          `json:"dependencies"`
	DevDependencies      map[string]string          `json:"devDependencies"`
	OptionalDependencies map[string]string          `json:"optionalDependencies"`
	PackageManager       string                     `json:"packageManager"`
	DevEngines           json.RawMessage            `json:"devEngines"`
	TrustedDependencies  []string                   `json:"trustedDependencies"`
	Pnpm                 map[string]json.RawMessage `json:"pnpm"`
}

func (m nodeInstallManifest) version(name string) string {
	for _, kind := range []map[string]string{m.Dependencies, m.DevDependencies, m.OptionalDependencies} {
		if version := kind[name]; version != "" {
			return version
		}
	}
	return ""
}

func (m nodeInstallManifest) has(name string) bool { return m.version(name) != "" }

// nodeDeclaredManager is the manager a repository names itself, through
// Corepack's packageManager field or devEngines.packageManager. spec is set
// only for an exact release, validated, which is what a Dockerfile may name.
type nodeDeclaredManager struct {
	name, version, spec, source string
}

var nodePackageManagerSpecRE = regexp.MustCompile(`^(bun|npm|pnpm|yarn)@([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.]+)?)(\+sha(?:1|224|256|384|512)\.[0-9a-f]{16,128})?$`)
var nodeExactVersionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.]+)?$`)

func declaredNodeManager(manifest nodeInstallManifest) nodeDeclaredManager {
	field := strings.TrimSpace(manifest.PackageManager)
	if match := nodePackageManagerSpecRE.FindStringSubmatch(field); match != nil {
		return nodeDeclaredManager{name: match[1], version: match[2], spec: field, source: "packageManager"}
	}
	if name, _, _ := strings.Cut(field, "@"); validNodePackageManager(name) {
		return nodeDeclaredManager{name: name, source: "packageManager"}
	}
	var engines struct {
		PackageManager json.RawMessage `json:"packageManager"`
	}
	if len(manifest.DevEngines) == 0 || json.Unmarshal(manifest.DevEngines, &engines) != nil || len(engines.PackageManager) == 0 {
		return nodeDeclaredManager{}
	}
	type engine struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	var entries []engine
	var single engine
	if json.Unmarshal(engines.PackageManager, &single) == nil && single.Name != "" {
		entries = []engine{single}
	} else if json.Unmarshal(engines.PackageManager, &entries) != nil {
		return nodeDeclaredManager{}
	}
	for _, entry := range entries {
		if !validNodePackageManager(entry.Name) {
			continue
		}
		declared := nodeDeclaredManager{name: entry.Name, source: "devEngines"}
		if version := strings.TrimSpace(entry.Version); nodeExactVersionRE.MatchString(version) {
			declared.version, declared.spec = version, entry.Name+"@"+version
		}
		return declared
	}
	return nodeDeclaredManager{}
}

func (d nodeDeclaredManager) major() int {
	major, err := strconv.Atoi(strings.SplitN(d.version, ".", 2)[0])
	if err != nil {
		return 0
	}
	return major
}

type nodeYarnConfig struct {
	present   bool
	linker    string
	yarnPath  string
	variables []DetectedVariable
}

type nodePNPMPolicy struct {
	declared bool
	// onlyLegacy is a policy that names builds only through the pnpm 10
	// fields, which pnpm 11 and later no longer read.
	onlyLegacy bool
	allowed    []string
}

// nodeInstallFacts is everything the plan reads, gathered once per package.
type nodeInstallFacts struct {
	// manifest is the package's own package.json; settings is the one whose
	// install settings apply — the workspace root's for a member, since
	// packageManager, trustedDependencies and the pnpm field are read there.
	manifest, settings nodeInstallManifest
	// member is the package's directory under the install root, "" for a
	// package that installs on its own.
	member       string
	readings     []nodeLockfileReading
	declared     nodeDeclaredManager
	signals      map[string][]string
	yarn         nodeYarnConfig
	pnpm         nodePNPMPolicy
	legacyPeers  bool
	registry     []DetectedVariable
	literal      []string
	superseded   []string
	inputs       []string
	dockerignore []byte
	// workspaceTurbo says the workspace root builds with Turborepo.
	workspaceTurbo bool
}

// detectedLockfiles are the readings as the candidate records them.
func (f nodeInstallFacts) detectedLockfiles() []DetectedLockfile {
	lockfiles := make([]DetectedLockfile, 0, len(f.readings))
	for _, reading := range f.readings {
		lockfiles = append(lockfiles, reading.DetectedLockfile)
	}
	return lockfiles
}

// detectedInstalls plans the install under every manager an operator can
// choose, with the commands detection proposes for that manager's runner;
// resolved is the manager chosen automatically, planned as the automatic
// choice so its record carries why it was chosen.
func (f nodeInstallFacts) detectedInstalls(resolved string, assets bool, commands func(runner string) (string, string)) []DetectedNodeInstall {
	installs := make([]DetectedNodeInstall, 0, len(nodeManagerOrder))
	for _, manager := range nodeManagerOrder {
		build, start := commands(manager)
		selected := manager
		if manager == resolved {
			selected = ""
		}
		plan := planNodeInstall(f, nodeInstallChoice{selected: selected, build: build, start: start, assets: assets})
		installs = append(installs, plan.detected(manager, build, start))
	}
	return installs
}

// readNodeInstallFacts reads a package's install inputs. files is rooted at
// the install root (the package itself, or the workspace root that holds its
// lockfile); member is the package's path under it.
func readNodeInstallFacts(files nodeFiles, member string, manifest []byte, arch string) nodeInstallFacts {
	facts := nodeInstallFacts{member: member, signals: map[string][]string{}}
	_ = json.Unmarshal(manifest, &facts.manifest)
	facts.settings = facts.manifest
	if member != "" {
		if content, err := files.read("package.json", nodeManifestMax); err == nil {
			var workspace nodeInstallManifest
			if json.Unmarshal(content, &workspace) == nil {
				facts.settings = workspace
				facts.workspaceTurbo = workspace.has("turbo")
			}
		}
	}
	facts.declared = declaredNodeManager(facts.settings)
	if facts.declared.name == "" {
		facts.declared = declaredNodeManager(facts.manifest)
	}
	present := map[string]bool{}
	for _, lock := range nodeLockfileNames {
		if files.exists(lock.path) {
			present[lock.path] = true
		}
	}
	for _, lock := range nodeLockfileNames {
		if !present[lock.path] {
			continue
		}
		switch {
		case lock.path == "bun.lockb" && present["bun.lock"]:
			facts.superseded = append(facts.superseded, "bun.lockb is superseded by bun.lock; delete it")
			continue
		case lock.path == "package-lock.json" && present["npm-shrinkwrap.json"]:
			facts.superseded = append(facts.superseded, "npm-shrinkwrap.json takes precedence over package-lock.json")
			continue
		}
		facts.readings = append(facts.readings, readNodeLockfile(files, lock.path, member, arch))
	}
	// Directories carry a trailing slash, so an ignore-file exception can
	// reach what is under them.
	for _, input := range []string{".npmrc", ".yarnrc.yml", ".yarnrc", "bunfig.toml", "pnpm-workspace.yaml", ".pnpmfile.cjs"} {
		if files.exists(input) {
			facts.inputs = append(facts.inputs, input)
		}
	}
	for _, input := range []string{"patches", ".yarn/releases", ".yarn/patches", ".yarn/plugins"} {
		if files.dirExists(input) {
			facts.inputs = append(facts.inputs, input+"/")
		}
	}
	facts.dockerignore, _ = files.read(".dockerignore", nodeConfigMaxBytes)

	scriptTools := nodeCommandTools(facts.manifest.Scripts, nil)
	signal := func(manager, reason string) { facts.signals[manager] = append(facts.signals[manager], reason) }
	if facts.settings.TrustedDependencies != nil {
		signal("bun", "trustedDependencies in package.json")
	}
	if files.exists("bunfig.toml") {
		signal("bun", "bunfig.toml")
	}
	if facts.manifest.has("@types/bun") || facts.manifest.has("bun-types") {
		signal("bun", "Bun's type definitions")
	}
	if scriptTools["bun"] || scriptTools["bunx"] {
		signal("bun", "package scripts run bun")
	}
	if workspace, err := files.read("pnpm-workspace.yaml", nodeConfigMaxBytes); err == nil {
		signal("pnpm", "pnpm-workspace.yaml")
		facts.pnpm = readPNPMPolicy(workspace, facts.settings.Pnpm)
	} else {
		facts.pnpm = readPNPMPolicy(nil, facts.settings.Pnpm)
	}
	if facts.settings.Pnpm != nil {
		signal("pnpm", "a pnpm field in package.json")
	}
	if files.exists(".pnpmfile.cjs") {
		signal("pnpm", ".pnpmfile.cjs")
	}
	if scriptTools["pnpm"] || scriptTools["pnpx"] {
		signal("pnpm", "package scripts run pnpm")
	}
	if content, err := files.read(".yarnrc.yml", nodeConfigMaxBytes); err == nil {
		signal("yarn", ".yarnrc.yml")
		facts.yarn = readYarnConfig(content)
		facts.registry = append(facts.registry, facts.yarn.variables...)
	}
	if files.dirExists(".yarn/releases") {
		signal("yarn", ".yarn/releases")
	}
	if scriptTools["yarn"] {
		signal("yarn", "package scripts run yarn")
	}
	scopes := nodeManifestScopes(facts.manifest)
	npmrcs := []nodeFiles{files}
	if member != "" {
		npmrcs = append(npmrcs, files.sub(member))
	}
	for index, directory := range npmrcs {
		content, err := directory.read(".npmrc", nodeConfigMaxBytes)
		if err != nil {
			continue
		}
		source := ".npmrc"
		if index > 0 {
			source = path.Join(member, ".npmrc")
		}
		config := readNPMRC(content, source, scopes)
		facts.legacyPeers = facts.legacyPeers || config.legacyPeers
		facts.registry = append(facts.registry, config.variables...)
		if config.literal {
			facts.literal = append(facts.literal, source)
		}
	}
	if content, err := files.read("bunfig.toml", nodeConfigMaxBytes); err == nil {
		facts.registry = append(facts.registry, readBunfigRegistry(content, scopes)...)
	}
	facts.registry = mergeDetectedVariables(facts.registry)
	return facts
}

// nodeManifestScopes are the npm scopes a manifest installs from.
func nodeManifestScopes(manifest nodeInstallManifest) map[string]bool {
	scopes := map[string]bool{}
	for _, kind := range []map[string]string{manifest.Dependencies, manifest.DevDependencies, manifest.OptionalDependencies} {
		for name := range kind {
			if scope, _, found := strings.Cut(name, "/"); found && strings.HasPrefix(scope, "@") {
				scopes[scope] = true
			}
		}
	}
	return scopes
}

func readPNPMPolicy(workspace []byte, field map[string]json.RawMessage) nodePNPMPolicy {
	policy := nodePNPMPolicy{}
	legacy, modern := false, false
	allow := func(raw any) {
		switch value := raw.(type) {
		case []any:
			for _, item := range value {
				if name, ok := item.(string); ok {
					policy.allowed = append(policy.allowed, name)
				}
			}
		case map[string]any:
			for name, allowed := range value {
				if allowed == true {
					policy.allowed = append(policy.allowed, name)
				}
			}
		}
	}
	read := func(values map[string]any) {
		for key, value := range values {
			switch key {
			case "onlyBuiltDependencies":
				legacy, policy.declared = true, true
				allow(value)
			case "neverBuiltDependencies", "ignoredBuiltDependencies", "onlyBuiltDependenciesFile":
				legacy, policy.declared = true, true
			case "allowBuilds", "dangerouslyAllowAllBuilds":
				modern, policy.declared = true, true
				allow(value)
			}
		}
	}
	if len(workspace) > 0 {
		var values map[string]any
		if yaml.Unmarshal(workspace, &values) == nil {
			read(values)
		}
	}
	if field != nil {
		values := map[string]any{}
		for key, raw := range field {
			var value any
			if json.Unmarshal(raw, &value) == nil {
				values[key] = value
			}
		}
		read(values)
	}
	policy.onlyLegacy = legacy && !modern
	return policy
}

// nodeRegistryVariableRE finds ${NAME} (npm, Yarn) and $NAME (Bun) in a
// package-manager configuration value.
var (
	nodeBracedVariableRE = regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]{1,127})(\?)?([:]?-[^}]*)?\}`)
	nodeBareVariableRE   = regexp.MustCompile(`\$([A-Z][A-Z0-9_]{1,127})`)
)

type nodeNPMRC struct {
	legacyPeers bool
	literal     bool
	variables   []DetectedVariable
}

// readNPMRC reads .npmrc as key=value data: the scoped registries, the
// variables its credentials name, whether a credential is committed as a
// literal (never echoed), and the peer-dependency settings npm ci honours.
func readNPMRC(content []byte, source string, scopes map[string]bool) nodeNPMRC {
	result := nodeNPMRC{}
	scopeHosts := map[string]string{}
	defaultHost := "registry.npmjs.org"
	credentials := map[string][]string{}
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key, value = strings.TrimSpace(key), strings.Trim(strings.TrimSpace(value), `"'`)
		switch {
		case (key == "legacy-peer-deps" || key == "force") && value == "true":
			result.legacyPeers = true
		case key == "registry":
			defaultHost = npmrcHost(value)
		case strings.HasSuffix(key, ":registry") && strings.HasPrefix(key, "@"):
			scopeHosts[strings.TrimSuffix(key, ":registry")] = npmrcHost(value)
		case strings.HasSuffix(key, "_authToken") || strings.HasSuffix(key, "_auth") || strings.HasSuffix(key, "_password"):
			host := defaultHost
			if strings.HasPrefix(key, "//") {
				host = npmrcHost(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(key, "_authToken"), "_auth"), "_password"))
			}
			matches := nodeBracedVariableRE.FindAllStringSubmatch(value, -1)
			if len(matches) == 0 && value != "" {
				result.literal = true
			}
			for _, match := range matches {
				credentials[host] = append(credentials[host], match[1])
			}
		}
	}
	for host, names := range credentials {
		required := host == defaultHost && defaultHost != "registry.npmjs.org"
		for scope, scopeHost := range scopeHosts {
			if scopeHost == host && scopes[scope] {
				required = true
			}
		}
		for _, name := range names {
			result.variables = append(result.variables, DetectedVariable{
				Name: name, Sources: []string{source}, Step: "install", InstallRequired: required,
			})
		}
	}
	return result
}

func npmrcHost(value string) string {
	value = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(value, "https:"), "http:"), "//")
	host, _, _ := strings.Cut(value, "/")
	host, _, _ = strings.Cut(host, ":")
	return strings.ToLower(host)
}

// readYarnConfig reads .yarnrc.yml as YAML data: the linker and bundled
// release, and the credentials Berry expands. Berry aborts any install when
// a referenced variable is unset and has no default, even for public
// packages, so every such reference is required.
func readYarnConfig(content []byte) nodeYarnConfig {
	config := nodeYarnConfig{present: true}
	var values struct {
		NodeLinker    string                       `yaml:"nodeLinker"`
		YarnPath      string                       `yaml:"yarnPath"`
		NPMAuthToken  string                       `yaml:"npmAuthToken"`
		NPMAuthIdent  string                       `yaml:"npmAuthIdent"`
		NPMScopes     map[string]map[string]string `yaml:"npmScopes"`
		NPMRegistries map[string]map[string]string `yaml:"npmRegistries"`
	}
	if yaml.Unmarshal(content, &values) != nil {
		return config
	}
	config.linker = strings.TrimSpace(values.NodeLinker)
	if yarnPath := strings.TrimSpace(values.YarnPath); safeRelativePath(yarnPath) {
		config.yarnPath = yarnPath
	}
	secrets := []string{values.NPMAuthToken, values.NPMAuthIdent}
	for _, scope := range values.NPMScopes {
		secrets = append(secrets, scope["npmAuthToken"], scope["npmAuthIdent"])
	}
	for _, registry := range values.NPMRegistries {
		secrets = append(secrets, registry["npmAuthToken"], registry["npmAuthIdent"])
	}
	for _, secret := range secrets {
		for _, match := range nodeBracedVariableRE.FindAllStringSubmatch(secret, -1) {
			config.variables = append(config.variables, DetectedVariable{
				Name: match[1], Sources: []string{".yarnrc.yml"}, Step: "install", InstallRequired: match[3] == "",
			})
		}
	}
	return config
}

// readBunfigRegistry reads the registry credentials bunfig.toml names, line
// by line: [install] registry = { token = "$NPM_TOKEN" } and the
// [install.scopes] table.
func readBunfigRegistry(content []byte, scopes map[string]bool) []DetectedVariable {
	variables := []DetectedVariable{}
	section := ""
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[] ")
			continue
		}
		if section != "install" && section != "install.scopes" {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.Trim(strings.TrimSpace(key), `"`)
		required := section == "install" && strings.TrimSpace(key) == "registry"
		if section == "install.scopes" {
			scope := key
			if !strings.HasPrefix(scope, "@") {
				scope = "@" + scope
			}
			required = scopes[scope]
		}
		names := map[string]bool{}
		for _, match := range nodeBracedVariableRE.FindAllStringSubmatch(value, -1) {
			names[match[1]] = true
		}
		for _, match := range nodeBareVariableRE.FindAllStringSubmatch(value, -1) {
			names[match[1]] = true
		}
		for name := range names {
			variables = append(variables, DetectedVariable{Name: name, Sources: []string{"bunfig.toml"}, Step: "install", InstallRequired: required})
		}
	}
	return variables
}

// mergeDetectedVariables keeps one entry per name, required if any source
// requires it, in name order.
func mergeDetectedVariables(variables []DetectedVariable) []DetectedVariable {
	byName := map[string]*DetectedVariable{}
	names := []string{}
	for _, variable := range variables {
		if !envNameRE.MatchString(variable.Name) {
			continue
		}
		existing := byName[variable.Name]
		if existing == nil {
			copy := variable
			copy.Sources = append([]string(nil), variable.Sources...)
			byName[variable.Name] = &copy
			names = append(names, variable.Name)
			continue
		}
		existing.InstallRequired = existing.InstallRequired || variable.InstallRequired
		for _, source := range variable.Sources {
			if !slices.Contains(existing.Sources, source) {
				existing.Sources = append(existing.Sources, source)
			}
		}
	}
	sort.Strings(names)
	result := make([]DetectedVariable, 0, len(names))
	for _, name := range names {
		result = append(result, *byName[name])
	}
	return result
}

// nodeManagerOrder is the order competing managers are listed in.
var nodeManagerOrder = []string{"bun", "npm", "pnpm", "yarn"}

func (f nodeInstallFacts) reading(manager string) *nodeLockfileReading {
	for index := range f.readings {
		if f.readings[index].Manager == manager {
			return &f.readings[index]
		}
	}
	return nil
}

// lockfileManagers lists each manager with a committed lockfile, once.
func (f nodeInstallFacts) lockfileManagers() []string {
	managers := []string{}
	for _, manager := range nodeManagerOrder {
		if f.reading(manager) != nil {
			managers = append(managers, manager)
		}
	}
	return managers
}

// nodeResolution is which manager installs and why.
type nodeResolution struct {
	manager string
	reading *nodeLockfileReading
	reason  string
	// competed says other lockfiles were set aside, so the reason is worth
	// a finding of its own.
	competed bool
}

// resolveNodeManager decides which manager installs: the operator's choice,
// then the manifest's declaration, then the one lockfile a frozen install
// would accept, then the one in-sync lockfile, then the manager the
// repository's own files are exclusive to. Only a genuine tie is refused.
// Without any lockfile the same order applies, ending at npm.
func resolveNodeManager(facts nodeInstallFacts, selected string) (nodeResolution, error) {
	managers := facts.lockfileManagers()
	if selected != "" {
		if reading := facts.reading(selected); reading != nil {
			return nodeResolution{manager: selected, reading: reading, reason: "chosen in the build settings"}, nil
		}
		if len(managers) == 0 {
			return nodeResolution{manager: selected, reason: "chosen in the build settings"}, nil
		}
		hint := "choose a package manager whose lockfile is committed"
		if automatic, err := resolveNodeManager(facts, ""); err == nil {
			hint = automatic.reading.Path + " is committed — choose " + nodeManagerLabel(automatic.manager) + " or the lockfile in the build settings"
		}
		return nodeResolution{}, fmt.Errorf("%w: the build uses %s, but the source has no %s lockfile; %s", ErrUnsupportedBuilder, selected, selected, hint)
	}
	declared := facts.declared.name
	if declared != "" {
		if reading := facts.reading(declared); reading != nil {
			return nodeResolution{manager: declared, reading: reading, reason: facts.declared.source + " in package.json selects " + nodeManagerLabel(declared), competed: len(managers) > 1}, nil
		}
		if len(managers) == 0 {
			return nodeResolution{manager: declared, reason: facts.declared.source + " in package.json selects " + nodeManagerLabel(declared)}, nil
		}
	}
	switch len(managers) {
	case 0:
		if signaled := facts.signaled(nodeManagerOrder); len(signaled) == 1 {
			return nodeResolution{manager: signaled[0], reason: strings.Join(facts.signals[signaled[0]], ", ") + " (no lockfile)"}, nil
		}
		return nodeResolution{manager: "npm", reason: "no lockfile is committed; npm installs by default"}, nil
	case 1:
		reading := facts.reading(managers[0])
		return nodeResolution{manager: managers[0], reading: reading, reason: "the only lockfile is " + reading.Path}, nil
	}
	viable, inSync := []string{}, []string{}
	for _, manager := range managers {
		reading := facts.reading(manager)
		if reading.State != LockfileStale {
			viable = append(viable, manager)
		}
		if reading.State == LockfileInSync {
			inSync = append(inSync, manager)
		}
	}
	choose := func(manager, why string) (nodeResolution, error) {
		reading := facts.reading(manager)
		reasons := []string{why}
		for _, other := range managers {
			if other != manager {
				reasons = append(reasons, facts.reading(other).Note)
			}
		}
		return nodeResolution{manager: manager, reading: reading, reason: strings.Join(reasons, "; "), competed: true}, nil
	}
	switch {
	case len(viable) == 1:
		return choose(viable[0], facts.reading(viable[0]).Note)
	case len(inSync) == 1:
		return choose(inSync[0], facts.reading(inSync[0]).Note)
	}
	pool := viable
	if len(pool) == 0 {
		pool = managers
	}
	if signaled := facts.signaled(pool); len(signaled) == 1 {
		return choose(signaled[0], strings.Join(facts.signals[signaled[0]], ", ")+" point to "+nodeManagerLabel(signaled[0]))
	}
	paths := []string{}
	for _, manager := range managers {
		paths = append(paths, facts.reading(manager).Path)
	}
	return nodeResolution{}, fmt.Errorf("%w: competing lockfiles %s; choose the package manager in the build settings, declare packageManager in package.json, or delete the lockfile this project no longer uses", ErrUnsupportedBuilder, strings.Join(paths, " and "))
}

func (f nodeInstallFacts) signaled(pool []string) []string {
	signaled := []string{}
	for _, manager := range pool {
		if len(f.signals[manager]) > 0 {
			signaled = append(signaled, manager)
		}
	}
	return signaled
}

func nodeManagerLabel(manager string) string {
	switch manager {
	case "bun":
		return "Bun"
	case "yarn":
		return "Yarn"
	}
	return manager
}

// nodeManagerReleases are the exact pnpm and Yarn releases the recipe
// installs when a repository does not name one, keyed by the lockfile
// format they read and write. They are reviewed like the base images:
// Corepack's own default is the newest release, whose major moves under a
// digest-pinned image and rejects older lockfiles (pnpm 11 and later refuse
// lockfileVersion 6.0, and fail on any dependency build script without an
// allowBuilds policy).
var nodeManagerReleases = map[string]map[string]string{
	"pnpm": {"5.3": "7.33.7", "5.4": "7.33.7", "6.0": "8.15.9", "6.1": "8.15.9", "9.0": "10.34.5"},
	// Berry's __metadata version, measured by installing with each release.
	"yarn": {"4": "2.4.3", "5": "3.1.1", "6": "3.8.7", "8": "4.9.2", "10": "4.18.0"},
}

const (
	nodeDefaultPNPM  = "10.34.5"
	nodeDefaultBerry = "4.18.0"
	nodeBunImage     = "oven/bun:1-alpine"
)

// nodeRuntimeNeedsScripts are packages whose own install script produces
// something the application loads at run time. An install policy that skips
// it builds cleanly and crashes on first require.
var nodeRuntimeNeedsScripts = []string{
	"better-sqlite3", "sqlite3", "bcrypt", "argon2", "canvas", "puppeteer", "node-sass",
	"isolated-vm", "re2", "onnxruntime-node", "@tensorflow/tfjs-node",
}

// nodeScriptPackagesPresent lists the packages of that kind the install
// puts in place, in order: those the lock names when it lists its packages,
// otherwise the manifest's own. Prisma 6 and earlier generate their client
// and fetch engines from install scripts; sharp before 0.33 compiled its
// binding.
func nodeScriptPackagesPresent(manifest nodeInstallManifest, locked map[string]string) []string {
	installed := func(name string) bool {
		if locked != nil {
			_, ok := locked[name]
			return ok
		}
		return manifest.has(name)
	}
	present := []string{}
	for _, name := range nodeRuntimeNeedsScripts {
		if installed(name) {
			present = append(present, name)
		}
	}
	for _, name := range []string{"prisma", "@prisma/client"} {
		if major, err := strconv.Atoi(semverMajor(manifest.version(name))); err == nil && major <= 6 {
			for _, prisma := range []string{"@prisma/client", "@prisma/engines"} {
				if installed(prisma) {
					present = append(present, prisma)
				}
			}
			break
		}
	}
	if version := strings.TrimLeft(manifest.version("sharp"), "^~>=v "); strings.HasPrefix(version, "0.") && installed("sharp") {
		if minor, err := strconv.Atoi(strings.SplitN(strings.TrimPrefix(version, "0."), ".", 2)[0]); err == nil && minor < 33 {
			present = append(present, "sharp")
		}
	}
	return present
}

func (p nodeInstallPlan) lockedNames() map[string]string {
	if p.reading == nil {
		return nil
	}
	return p.reading.names
}

// nodeInstallPlan is the rendered decision.
type nodeInstallPlan struct {
	manager  string
	lockfile string
	reading  *nodeLockfileReading
	frozen   bool
	// command is the install line; env is set on that RUN alone.
	command, env string
	// corepack lists exact releases installed into the shared toolchain
	// stage (manager@version[+hash]); strict false lets a manager run in a
	// repository whose packageManager names another.
	corepack []string
	strict   bool
	// bun is the image Bun is copied from, when the image needs it.
	bun string
	// berry keeps Yarn's cache inside the build so a Plug'n'Play install
	// is copied with the application.
	berry     bool
	toolchain string
	reason    string
	// build and start are the choice's commands on this manager's runner.
	build, start string
	findings     []PreflightFinding
	notes        []string
	blocked      *PreflightFinding
}

func (p nodeInstallPlan) blockedError() error {
	return fmt.Errorf("%w: %s", ErrUnsupportedBuilder, p.blocked.Measured)
}

// baseImages are the reviewed images the plan builds from, in the order the
// Dockerfile names them: Node, then Bun when it is copied in, then nginx for
// static output.
func (p nodeInstallPlan) baseImages(static bool) []string {
	images := []string{recipeBaseCatalogue["node:npm"][0]}
	if p.bun != "" {
		images = append(images, p.bun)
	}
	if static {
		images = append(images, recipeBaseCatalogue["static"]...)
	}
	return images
}

// nodeInstallChoice is what the plan is asked for.
type nodeInstallChoice struct {
	selected            string
	build, start        string
	assets              bool
	fieldPackageManager string
}

func nodeFinding(code string, severity PreflightSeverity, title, measured, means, action, field string) PreflightFinding {
	return finding(code, severity, title, boundedFindingText(measured), means, action, "deploy", field)
}

// boundedFindingText keeps a finding's measured text inside the detection
// bounds a saved draft is validated against.
func boundedFindingText(text string) string {
	if len(text) > 480 {
		return text[:477] + "..."
	}
	return text
}

// planNodeInstall decides the install for one choice.
func planNodeInstall(facts nodeInstallFacts, choice nodeInstallChoice) nodeInstallPlan {
	field := choice.fieldPackageManager
	if field == "" {
		field = "configuration.build.packageManager"
	}
	choice.fieldPackageManager = field
	plan := nodeInstallPlan{strict: true}
	resolution, err := resolveNodeManager(facts, choice.selected)
	if err != nil {
		code, title := "package_manager_ambiguous", "Competing lockfiles need a package manager"
		if choice.selected != "" {
			code, title = "package_manager_lockfile_missing", "Selected package manager has no lockfile"
		}
		blocked := nodeFinding(code, PreflightBlocked, title, strings.TrimPrefix(err.Error(), ErrUnsupportedBuilder.Error()+": "),
			"A frozen install needs the chosen manager's own lockfile, and installing from one the project abandoned builds untested versions.",
			"Choose the package manager in the build settings, declare packageManager in package.json, or delete the stale lockfile.", field)
		plan.blocked = &blocked
		return plan
	}
	plan.manager, plan.reading, plan.reason = resolution.manager, resolution.reading, resolution.reason
	if plan.reading != nil {
		plan.lockfile = plan.reading.Path
	}
	// A saved command still naming another manager's runner — detected when
	// a different lockfile resolved, or left behind by a later commit that
	// switched managers — would run a program the image may not have.
	plan.build, plan.start = nodeRunnerFor(choice.build, plan.manager), nodeRunnerFor(choice.start, plan.manager)
	for _, command := range []struct{ label, saved, runs string }{
		{"Build command", choice.build, plan.build}, {"Start command", choice.start, plan.start},
	} {
		// The asset stage's command is the recipe's own, never a saved one.
		if command.saved != command.runs && !choice.assets {
			plan.notes = append(plan.notes, command.label+" runs with "+plan.manager+": `"+command.runs+"` (saved: `"+command.saved+"`)")
		}
	}
	plan.frozen = plan.reading != nil && plan.reading.State != LockfileStale
	if resolution.competed && choice.selected == "" {
		stale := []string{}
		for _, reading := range facts.readings {
			if reading.Manager != plan.manager && reading.State == LockfileStale {
				stale = append(stale, reading.Path)
			}
		}
		action := ""
		if len(stale) > 0 {
			action = "Delete " + strings.Join(stale, " and ") + " so the repository names one manager."
		}
		plan.findings = append(plan.findings, nodeFinding("package_manager_resolved", PreflightPass,
			"Package manager chosen from the lockfiles", plan.reason,
			"Detection compared every committed lockfile with package.json and installs from the one a frozen install accepts.",
			action, field))
	}
	for _, superseded := range facts.superseded {
		plan.notes = append(plan.notes, superseded)
	}
	declaredOther := facts.declared.name != "" && facts.declared.name != plan.manager
	switch plan.manager {
	case "npm":
		planNPMInstall(facts, &plan)
	case "pnpm":
		planPNPMInstall(facts, &plan, field)
	case "yarn":
		planYarnInstall(facts, &plan, choice)
	case "bun":
		planBunInstall(facts, &plan, choice.assets)
	}
	if plan.blocked != nil {
		return plan
	}
	tools := nodeCommandTools(facts.manifest.Scripts, []string{plan.build, plan.start})
	if (tools["bun"] || tools["bunx"]) && plan.bun == "" {
		plan.bun = nodeBunImage
		plan.findings = append(plan.findings, nodeFinding("script_runtime_added", PreflightPass,
			"Bun is added for the scripts that call it", "bun or bunx in the commands and package scripts the build runs",
			"The image installs with "+nodeManagerLabel(plan.manager)+"; Bun is copied beside Node so those scripts find it.", "", "configuration.build.buildCommand"))
	}
	if (tools["pnpm"] || tools["pnpx"]) && plan.manager != "pnpm" {
		version := nodeDefaultPNPM
		if facts.declared.name == "pnpm" && facts.declared.version != "" {
			version = facts.declared.version
		}
		plan.corepack = append(plan.corepack, "pnpm@"+version)
		plan.findings = append(plan.findings, nodeFinding("script_runtime_added", PreflightPass,
			"pnpm is added for the scripts that call it", "pnpm "+version,
			"The image installs with "+nodeManagerLabel(plan.manager)+"; pnpm is installed through Corepack so those scripts find it.", "", "configuration.build.buildCommand"))
	}
	if tools["deno"] {
		blocked := nodeFinding("command_runner_missing", PreflightBlocked,
			"A command needs a runtime the Node image does not have", "deno",
			"The build and start commands, or the package scripts they run, call deno, which the Node recipe does not install.",
			"Use a Dockerfile, or change the command to one the Node image runs.", "configuration.build.buildCommand")
		plan.blocked = &blocked
		return plan
	}
	for _, segment := range nodeInstallSegments(plan.build) {
		plan.findings = append(plan.findings, nodeFinding("install_in_build_command", PreflightWarning,
			"The build command installs dependencies again", segment,
			"The recipe already ran "+plan.command+"; installing again in the build resolves versions the lockfile did not pin.",
			"Remove the install from the build command.", "configuration.build.buildCommand"))
	}
	if declaredOther && (plan.manager == "pnpm" || plan.manager == "yarn") {
		plan.strict = false
		plan.findings = append(plan.findings, nodeFinding("package_manager_declaration_conflict", PreflightWarning,
			"package.json declares a different package manager",
			facts.declared.source+" names "+facts.declared.name+"; the build installs with "+plan.manager+" from "+nodeLockOrNone(plan.lockfile),
			"Corepack refuses to run a manager the manifest does not name, so the build lets it run anyway.",
			"Fix packageManager, or commit the declared manager's lockfile.", field))
	}
	for _, spec := range plan.corepack {
		// Corepack refuses to run a manager the manifest does not name.
		if name, _, _ := strings.Cut(spec, "@"); facts.declared.name != "" && name != facts.declared.name {
			plan.strict = false
		}
	}
	for _, source := range facts.literal {
		plan.findings = append(plan.findings, nodeFinding("registry_token_committed", PreflightWarning,
			"A registry credential is committed to the repository", source,
			"The value is in Git history and in every build context; anyone who can read the repository can use it.",
			"Revoke it, reference an environment variable instead (${NPM_TOKEN}), and add that variable mapped to the install step.", "variables"))
	}
	if plan.reading != nil && plan.reading.State == LockfileStale {
		alternative := ""
		for _, reading := range facts.readings {
			if reading.Manager != plan.manager && reading.State == LockfileInSync {
				alternative = "Choose " + nodeManagerLabel(reading.Manager) + ", whose " + reading.Path + " matches package.json, or commit an updated " + plan.lockfile + "."
				break
			}
		}
		if alternative == "" {
			alternative = "Run " + nodeRegenerateCommand(plan.manager) + " and commit " + plan.lockfile + "."
		}
		plan.findings = append(plan.findings, nodeFinding("lockfile_out_of_sync", PreflightWarning,
			plan.lockfile+" is out of sync with package.json", plan.reading.Note,
			"A frozen install would refuse it, so the build runs "+plan.command+", which resolves these to the newest versions the ranges allow.",
			alternative, field))
		plan.notes = append(plan.notes, "Installing with "+plan.command+": "+plan.reading.Note)
	}
	if plan.reading == nil {
		code := "dependencies_unpinned"
		if choice.assets {
			code = "assets_dependencies_unpinned"
		}
		plan.findings = append(plan.findings, nodeFinding(code, PreflightWarning,
			"Dependencies are not pinned to exact versions", "no JavaScript lockfile is committed",
			"The build runs "+plan.command+", which installs the newest versions package.json allows, so a rebuild of this same commit can run different code.",
			"Run "+nodeRegenerateCommand(plan.manager)+" and commit its lockfile when rebuilds must be identical; deploying as is works today.", field))
	}
	plan.findings = append(plan.findings, nodeFinding("package_manager_version", PreflightPass,
		"Package manager release", plan.toolchain,
		"The release that installs, and what chose it: a declaration, the lockfile's format, or the reviewed default.", "", field))
	return plan
}

func nodeLockOrNone(lockfile string) string {
	if lockfile == "" {
		return "no lockfile"
	}
	return lockfile
}

func nodeRegenerateCommand(manager string) string {
	return map[string]string{"npm": "npm install", "pnpm": "pnpm install", "yarn": "yarn install", "bun": "bun install"}[manager]
}

var nodePackageNameRE = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._~-]*/)?[a-z0-9][a-z0-9._~-]*$`)

func planNPMInstall(facts nodeInstallFacts, plan *nodeInstallPlan) {
	plan.command = "npm install --no-audit --no-fund"
	if plan.frozen {
		plan.command = "npm ci"
	}
	plan.toolchain = "npm (bundled with Node 22)"
	reading := plan.reading
	if reading == nil {
		return
	}
	if len(reading.peers) > 0 && !facts.legacyPeers {
		conflict := reading.peers[0]
		plan.command += " --legacy-peer-deps"
		measured := fmt.Sprintf("%s requires %s %s; the lockfile has %s", conflict.Package, conflict.Peer, conflict.Range, conflict.Version)
		if len(reading.peers) > 1 {
			measured += fmt.Sprintf(" (and %d more)", len(reading.peers)-1)
		}
		plan.findings = append(plan.findings, nodeFinding("peer_dependencies_legacy", PreflightPass,
			reading.Path+" was written with --legacy-peer-deps", measured,
			"npm ci re-checks peers and would refuse with ERESOLVE; the install passes --legacy-peer-deps, which installs exactly the locked tree.",
			"Commit .npmrc with legacy-peer-deps=true, or upgrade the package whose peer range is out of date.", "configuration.build.packageManager"))
	}
	missing := []string{}
	for _, binary := range reading.optional {
		if nodePackageNameRE.MatchString(binary.Name) && nodeExactVersionRE.MatchString(binary.Version) {
			missing = append(missing, binary.Name+"@"+binary.Version)
		}
	}
	if len(missing) > 0 {
		plan.command += " && npm install --no-save --no-audit --no-fund " + strings.Join(missing, " ")
		plan.findings = append(plan.findings, nodeFinding("optional_binary_missing", PreflightPass,
			reading.Path+" lacks this platform's native binaries", strings.Join(missing, ", "),
			"npm records optional platform packages only for the machine that wrote the lock (npm/cli#4828); the build adds these exact versions after installing the rest from the lock.",
			"Regenerate "+reading.Path+" from a clean install so it lists every platform.", "configuration.build.packageManager"))
	}
	for _, host := range reading.hosts {
		plan.findings = append(plan.findings, nodeFinding("registry_host_private", PreflightWarning,
			reading.Path+" downloads from a private registry", host,
			"The lock resolves packages from a host this server may not reach, so the install can fail at that download.",
			"Make the registry reachable from this server, or regenerate the lockfile against a public registry.", "configuration.build.packageManager"))
		break
	}
}

func planPNPMInstall(facts nodeInstallFacts, plan *nodeInstallPlan, field string) {
	version, source := nodeDefaultPNPM, "the reviewed default"
	lockVersion := ""
	if plan.reading != nil {
		lockVersion = plan.reading.version
	}
	spec := ""
	switch {
	case facts.declared.name == "pnpm" && facts.declared.version != "":
		version, source, spec = facts.declared.version, facts.declared.source, facts.declared.spec
	case nodeManagerReleases["pnpm"][lockVersion] != "":
		version, source = nodeManagerReleases["pnpm"][lockVersion], "lockfileVersion "+lockVersion
	case lockVersion != "":
		source = "the reviewed default; lockfileVersion " + lockVersion + " is not in the release table"
	}
	if spec == "" {
		spec = "pnpm@" + version
	}
	major, _ := strconv.Atoi(strings.SplitN(version, ".", 2)[0])
	if lockVersion != "" && !pnpmReadsLockfile(major, lockVersion) {
		blocked := nodeFinding("package_manager_lockfile_incompatible", PreflightBlocked,
			"The declared pnpm cannot read this lockfile", fmt.Sprintf("pnpm %s; pnpm-lock.yaml lockfileVersion %s", version, lockVersion),
			"pnpm refuses a lockfile format its major does not read, so the frozen install would fail.",
			"Declare the pnpm release that wrote the lockfile in packageManager, or regenerate pnpm-lock.yaml with the declared one.", field)
		plan.blocked = &blocked
		return
	}
	plan.corepack = append(plan.corepack, spec)
	plan.toolchain = "pnpm " + version + " (" + source + ")"
	plan.command = "pnpm install --no-frozen-lockfile"
	if plan.frozen {
		plan.command = "pnpm install --frozen-lockfile"
	}
	needed := nodeScriptPackagesPresent(facts.manifest, plan.lockedNames())
	switch {
	case !facts.pnpm.declared && major >= 10:
		// Without a policy pnpm 10 skips every dependency build script and
		// later majors fail on them; npm, Yarn and pnpm 9 run them, and so
		// does this build, inside its own container.
		plan.command += " --config.dangerously-allow-all-builds=true"
		plan.findings = append(plan.findings, nodeFinding("dependency_scripts_allowed", PreflightPass,
			"Dependency build scripts run", "no onlyBuiltDependencies or allowBuilds policy is declared",
			"pnpm "+strconv.Itoa(major)+" skips dependency install scripts without a policy; the build runs them, as npm does, inside the build container.",
			"Declare allowBuilds in pnpm-workspace.yaml to choose which dependencies may build.", field))
	case facts.pnpm.declared && facts.pnpm.onlyLegacy && major >= 11:
		plan.findings = append(plan.findings, nodeFinding("pnpm_build_policy_ignored", PreflightWarning,
			"pnpm "+strconv.Itoa(major)+" ignores this build policy", "onlyBuiltDependencies",
			"pnpm 11 and later read allowBuilds only, so every dependency build script is unapproved and the install fails.",
			"Move the list to allowBuilds in pnpm-workspace.yaml.", field))
	case facts.pnpm.declared && major >= 10:
		blocked := []string{}
		for _, name := range needed {
			if !slices.Contains(facts.pnpm.allowed, name) {
				blocked = append(blocked, name)
			}
		}
		if len(blocked) > 0 {
			plan.findings = append(plan.findings, nodeFinding("install_scripts_blocked", PreflightWarning,
				"The build policy skips install scripts the application needs", strings.Join(blocked, ", "),
				"These packages build a native binding or generate code in their install script; skipped, the application fails when it loads them.",
				"Add them to the build policy (allowBuilds in pnpm-workspace.yaml).", field))
		}
	}
}

// pnpmReadsLockfile is which lockfile formats each pnpm major reads.
func pnpmReadsLockfile(major int, lockVersion string) bool {
	family, _, _ := strings.Cut(lockVersion, ".")
	switch {
	case major <= 7:
		return family == "5"
	case major == 8:
		return family == "5" || family == "6"
	case major == 9:
		return family == "6" || family == "9"
	default:
		return family == "9"
	}
}

func planYarnInstall(facts nodeInstallFacts, plan *nodeInstallPlan, choice nodeInstallChoice) {
	berry := facts.declared.name == "yarn" && facts.declared.major() >= 2
	if plan.reading != nil {
		berry = berry || plan.reading.format == "yarn-berry"
	} else {
		berry = berry || facts.yarn.present
	}
	if !berry {
		plan.toolchain = "yarn 1.22 (classic, bundled with the Node image)"
		if facts.declared.name == "yarn" && facts.declared.spec != "" {
			plan.corepack = append(plan.corepack, facts.declared.spec)
			plan.toolchain = "yarn " + facts.declared.version + " (classic, " + facts.declared.source + ")"
		} else if facts.declared.name != "" && facts.declared.name != "yarn" {
			// Yarn 1.22 refuses a manifest naming another manager unless it
			// runs through Corepack's lenient mode.
			plan.corepack = append(plan.corepack, "yarn@1.22.22")
		}
		plan.command = "yarn install"
		if plan.frozen {
			plan.command = "yarn install --frozen-lockfile"
		}
		return
	}
	plan.berry = true
	metadata := ""
	if plan.reading != nil {
		metadata = plan.reading.version
	}
	immutable := plan.frozen
	switch {
	case facts.declared.name == "yarn" && facts.declared.spec != "":
		plan.corepack = append(plan.corepack, facts.declared.spec)
		plan.toolchain = "yarn " + facts.declared.version + " (" + facts.declared.source + ")"
	case facts.yarn.yarnPath != "":
		// The committed release is what Yarn 1.22 dispatches to, in the
		// build and at run time alike, so no other release is installed.
		plan.toolchain = "yarn from .yarnrc.yml yarnPath (" + facts.yarn.yarnPath + ")"
	case nodeManagerReleases["yarn"][metadata] != "":
		version := nodeManagerReleases["yarn"][metadata]
		plan.corepack = append(plan.corepack, "yarn@"+version)
		plan.toolchain = "yarn " + version + " (lockfile metadata version " + metadata + ")"
	default:
		plan.corepack = append(plan.corepack, "yarn@"+nodeDefaultBerry)
		plan.toolchain = "yarn " + nodeDefaultBerry + " (the reviewed default)"
		if plan.reading != nil {
			immutable = false
			plan.findings = append(plan.findings, nodeFinding("yarn_version_inferred", PreflightWarning,
				"Yarn's release could not be inferred", "yarn.lock metadata version "+nodeLockOrNone(metadata),
				"No packageManager or yarnPath names the Yarn that wrote this lock, so the build installs with Yarn "+nodeDefaultBerry+" and lets it update the lock.",
				"Declare packageManager in package.json (corepack use yarn@<version>) to pin Yarn.", choice.fieldPackageManager))
		}
	}
	plan.command = "yarn install --no-immutable"
	if immutable {
		plan.command = "yarn install --immutable"
	}
	pnp := facts.yarn.linker == "" || facts.yarn.linker == "pnp"
	if pnp && !nodeCommandsRunThrough([]string{plan.build, plan.start}, "yarn") {
		plan.env = "YARN_NODE_LINKER=node-modules"
		plan.findings = append(plan.findings, nodeFinding("yarn_linker_adjusted", PreflightPass,
			"Yarn installs node_modules for this image", "Plug'n'Play is Yarn's linker here, and a command runs outside yarn",
			"A command started without yarn cannot load Plug'n'Play dependencies, so the install links node_modules; the lockfile does not depend on the linker.",
			"", choice.fieldPackageManager))
	}
}

func planBunInstall(facts nodeInstallFacts, plan *nodeInstallPlan, assets bool) {
	plan.bun = nodeBunImage
	plan.toolchain = "bun 1 (the newest 1.x image)"
	if facts.declared.name == "bun" && facts.declared.version != "" && !strings.Contains(facts.declared.version, "-") {
		plan.bun = "oven/bun:" + facts.declared.version + "-alpine"
		plan.toolchain = "bun " + facts.declared.version + " (" + facts.declared.source + ")"
	}
	plan.command = "bun install"
	if plan.frozen {
		plan.command = "bun install --frozen-lockfile"
	}
	if facts.settings.TrustedDependencies != nil {
		untrusted := []string{}
		for _, name := range nodeScriptPackagesPresent(facts.manifest, plan.lockedNames()) {
			if !slices.Contains(facts.settings.TrustedDependencies, name) && !slices.Contains(untrusted, name) {
				untrusted = append(untrusted, name)
			}
		}
		if len(untrusted) > 0 {
			plan.command += " && bun pm trust " + strings.Join(untrusted, " ")
			plan.findings = append(plan.findings, nodeFinding("install_scripts_trusted", PreflightPass,
				"Install scripts the application needs are trusted", strings.Join(untrusted, ", "),
				"Declaring trustedDependencies replaces Bun's default allowlist, so these packages' install scripts were blocked; the build trusts them after installing.",
				"Add them to trustedDependencies in package.json.", "configuration.build.packageManager"))
		}
	}
	if assets {
		return
	}
	plan.findings = append(plan.findings, nodeFinding("runtime_selected", PreflightPass,
		"Bun installs; Node runs", "Node 22 with Bun "+strings.TrimSuffix(strings.TrimPrefix(plan.bun, "oven/bun:"), "-alpine"),
		"Bun installs the dependencies and runs bun and bunx commands; tools started through node run on Node, as on a machine with both installed.",
		"", "configuration.build.packageManager"))
}

var (
	nodeSegmentSeparatorRE = regexp.MustCompile(`&&|\|\||;|\|`)
	nodeAndSeparatorRE     = regexp.MustCompile(`\s+&&\s+`)
)

// nodeCommandSegments splits a shell command into its simple commands.
func nodeCommandSegments(command string) []string {
	segments := []string{}
	for _, part := range nodeSegmentSeparatorRE.Split(command, -1) {
		if part = strings.TrimSpace(part); part != "" {
			segments = append(segments, part)
		}
	}
	return segments
}

var nodeAssignmentRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// nodeSegmentWords drops what runs before the program: VAR=value
// assignments, env, cross-env and dotenv's `-e file --`.
func nodeSegmentWords(segment string) []string {
	words := strings.Fields(segment)
	for len(words) > 0 {
		switch {
		case nodeAssignmentRE.MatchString(words[0]):
			words = words[1:]
		case words[0] == "env" || words[0] == "cross-env" || words[0] == "cross-env-shell":
			words = words[1:]
		case words[0] == "dotenv" || words[0] == "dotenvx":
			index := slices.Index(words, "--")
			if index < 0 {
				return nil
			}
			words = words[index+1:]
		default:
			return words
		}
	}
	return words
}

// nodeYarnCommands are Yarn's own subcommands; any other word after yarn
// names a script or a binary.
var nodeYarnCommands = map[string]bool{
	"add": true, "install": true, "remove": true, "upgrade": true, "up": true, "dlx": true, "exec": true,
	"node": true, "run": true, "workspace": true, "workspaces": true, "why": true, "info": true, "cache": true,
	"config": true, "init": true, "link": true, "unlink": true, "pack": true, "publish": true, "set": true,
	"plugin": true, "constraints": true, "version": true, "npm": true, "rebuild": true, "dedupe": true, "global": true,
}

// nodeCommandTools lists the programs the given commands run, following
// `<manager> run <script>` (and the script's pre and post hooks) into the
// package's scripts, as well as the lifecycle scripts every install runs.
// With no commands it reads the scripts alone, which is how a repository's
// own tooling signals its manager.
func nodeCommandTools(scripts map[string]string, commands []string) map[string]bool {
	tools := map[string]bool{}
	visited := map[string]bool{}
	var visit func(command string, depth int)
	runScript := func(name string, depth int) {
		for _, hook := range []string{"pre" + name, name, "post" + name} {
			if body, ok := scripts[hook]; ok && !visited[hook] {
				visited[hook] = true
				visit(body, depth+1)
			}
		}
	}
	visit = func(command string, depth int) {
		if depth > 4 {
			return
		}
		for _, segment := range nodeCommandSegments(command) {
			words := nodeSegmentWords(segment)
			if len(words) == 0 {
				continue
			}
			program := path.Base(words[0])
			tools[program] = true
			if len(words) < 2 {
				continue
			}
			switch program {
			case "npm", "pnpm", "bun", "yarn":
				switch {
				case words[1] == "run" || words[1] == "run-script":
					if len(words) > 2 {
						runScript(words[2], depth)
					}
				case (program == "npm" && (words[1] == "start" || words[1] == "test")) ||
					(program != "npm" && !nodeYarnCommands[words[1]] && scripts[words[1]] != ""):
					runScript(words[1], depth)
				}
			}
		}
	}
	if commands == nil {
		for _, body := range scripts {
			for _, segment := range nodeCommandSegments(body) {
				if words := nodeSegmentWords(segment); len(words) > 0 {
					tools[path.Base(words[0])] = true
				}
			}
		}
		return tools
	}
	for _, lifecycle := range []string{"preinstall", "install", "postinstall", "prepare"} {
		if body, ok := scripts[lifecycle]; ok && !visited[lifecycle] {
			visited[lifecycle] = true
			visit(body, 1)
		}
	}
	for _, command := range commands {
		visit(command, 0)
	}
	return tools
}

// nodeCommandsRunThrough reports whether every simple command runs through
// the given manager, which is what keeps Yarn's Plug'n'Play loader in place.
func nodeCommandsRunThrough(commands []string, manager string) bool {
	for _, command := range commands {
		for _, segment := range nodeCommandSegments(command) {
			if words := nodeSegmentWords(segment); len(words) > 0 && words[0] != manager {
				return false
			}
		}
	}
	return true
}

// nodeInstallSegments are the parts of a build command that resolve
// dependencies again after the recipe's install.
func nodeInstallSegments(command string) []string {
	found := []string{}
	for _, segment := range nodeCommandSegments(command) {
		words := nodeSegmentWords(segment)
		if len(words) < 1 {
			continue
		}
		install := false
		switch words[0] {
		case "npm":
			install = len(words) > 1 && (words[1] == "install" || words[1] == "i" || words[1] == "add")
		case "pnpm", "bun":
			install = len(words) > 1 && (words[1] == "install" || words[1] == "i" || words[1] == "add") &&
				!slices.Contains(words, "--frozen-lockfile")
		case "yarn":
			install = (len(words) == 1 || words[1] == "install" || words[1] == "add") &&
				!slices.Contains(words, "--frozen-lockfile") && !slices.Contains(words, "--immutable")
		}
		if install {
			found = append(found, segment)
		}
	}
	return found
}

var (
	nodeAssignmentsRE = regexp.MustCompile(`^((?:[A-Za-z_][A-Za-z0-9_]*=\S*\s+)*)(.*)$`)
	nodeScriptRunRE   = regexp.MustCompile(`^(?:bun|npm|pnpm|yarn) run (\S+)$`)
	nodeShorthandRE   = regexp.MustCompile(`^(npm|pnpm|yarn) (start|test)$`)
	nodeBinaryRunRE   = regexp.MustCompile(`^(?:npx|bunx|pnpm exec) (.+)$`)
	nodeYarnBinRE     = regexp.MustCompile(`^yarn (\S.*)$`)
)

// nodeRunnerFor moves a command to another manager's runner the way the
// configure form does (deployment-defaults.ts withPackageManagerRunner):
// each `&&` segment that is a plain `<manager> run <script>`, npm's, pnpm's
// or Yarn's start and test shorthands, or a binary run through a manager,
// follows the manager, after any NAME=value assignments in front of it;
// anything else is the operator's own command and is left alone. A bare
// `yarn <thing>` is a binary run only when chained, since alone it is as
// likely a script.
func nodeRunnerFor(command, manager string) string {
	if manager == "" || strings.TrimSpace(command) == "" {
		return command
	}
	segments := nodeAndSeparatorRE.Split(strings.TrimSpace(command), -1)
	for index, segment := range segments {
		parts := nodeAssignmentsRE.FindStringSubmatch(segment)
		assignments, body := parts[1], parts[2]
		switch {
		case nodeScriptRunRE.MatchString(body):
			segments[index] = assignments + manager + " run " + nodeScriptRunRE.FindStringSubmatch(body)[1]
		case nodeShorthandRE.MatchString(body) && nodeShorthandRE.FindStringSubmatch(body)[1] != manager:
			segments[index] = assignments + manager + " run " + nodeShorthandRE.FindStringSubmatch(body)[2]
		case nodeBinaryRunRE.MatchString(body):
			segments[index] = assignments + nodeExecRunner(manager) + " " + nodeBinaryRunRE.FindStringSubmatch(body)[1]
		case len(segments) > 1 && nodeYarnBinRE.MatchString(body) && !strings.HasPrefix(body, "yarn run "):
			segments[index] = assignments + nodeExecRunner(manager) + " " + nodeYarnBinRE.FindStringSubmatch(body)[1]
		}
	}
	return strings.Join(segments, " && ")
}

// detectedInstall records a plan on the candidate.
func (p nodeInstallPlan) detected(manager, build, start string) DetectedNodeInstall {
	install := DetectedNodeInstall{Manager: manager, BuildCommand: build, StartCommand: start, Findings: []PreflightFinding{}}
	if p.blocked != nil {
		install.Findings = append(install.Findings, *p.blocked)
		return install
	}
	install.Lockfile, install.Install, install.Toolchain = p.lockfile, p.installLine(), p.toolchain
	install.Findings = append(install.Findings, p.findings...)
	return install
}

// installLine is the install as the Dockerfile runs it, without mounts.
func (p nodeInstallPlan) installLine() string {
	if p.env != "" {
		return p.env + " " + p.command
	}
	return p.command
}

// toolchainLines render the stage the build and the runtime both start from:
// the Node image, Corepack's exact manager releases installed offline-ready,
// and Bun copied beside Node. It is empty when the Node image alone serves.
func (p nodeInstallPlan) toolchainLines(bunImage string) []string {
	lines := []string{}
	if len(p.corepack) > 0 {
		env := "ENV COREPACK_HOME=/opt/corepack COREPACK_ENABLE_DOWNLOAD_PROMPT=0 COREPACK_ENABLE_AUTO_PIN=0"
		if !p.strict {
			env += " COREPACK_ENABLE_STRICT=0"
		}
		lines = append(lines, env)
		for _, spec := range p.corepack {
			name, _, _ := strings.Cut(spec, "@")
			lines = append(lines, "RUN corepack enable "+name+" && corepack install -g "+spec)
		}
	}
	if bunImage != "" {
		lines = append(lines, "COPY --from="+bunImage+" /usr/local/bin/bun /usr/local/bin/bun", "RUN ln -s bun /usr/local/bin/bunx")
	}
	return lines
}
