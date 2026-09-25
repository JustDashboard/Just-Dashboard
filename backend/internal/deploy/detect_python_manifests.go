package deploy

import (
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Python declares its dependencies in more shapes than any other ecosystem:
// requirement files that include one another (a cookiecutter's
// requirements/production.txt, a root requirements.txt that is only `-r`),
// PEP 621 and Poetry pyproject tables, Pipfile and Pipfile.lock, the uv,
// Poetry and PDM locks, setup.cfg, a literal setup.py install_requires and a
// conda environment.yml. Every one of them is read here as bounded data;
// none is executed, and setup.py is only ever searched for a literal list.

// pythonRequirement is one requirement as a manifest states it.
type pythonRequirement struct {
	// name is PEP 503-normalized; extras are lower-case.
	name      string
	extras    []string
	specifier string
	marker    string
	// url is a direct reference: `name @ https://…`, a VCS URL, a local path.
	url      string
	editable bool
	// file is the manifest the requirement came from, and text its line.
	file string
	text string
}

// pinned says the requirement names one release: `==`, `===`, or a direct
// reference, which is a single artifact.
func (r pythonRequirement) pinned() bool {
	return r.url != "" || (strings.HasPrefix(r.specifier, "==") && !strings.Contains(r.specifier, ",") && !strings.HasSuffix(r.specifier, "*"))
}

// pinnedVersion is the release an exact pin names.
func (r pythonRequirement) pinnedVersion() string {
	if !r.pinned() || r.url != "" {
		return ""
	}
	return strings.TrimSpace(strings.TrimLeft(r.specifier, "="))
}

func (r pythonRequirement) hasExtra(extra string) bool {
	for _, candidate := range r.extras {
		if candidate == extra {
			return true
		}
	}
	return false
}

var (
	pythonPEP508NameRE = regexp.MustCompile(`^([A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?)\s*(\[[^\]]*\])?\s*`)
	pythonEggRE        = regexp.MustCompile(`[#&]egg=([A-Za-z0-9][A-Za-z0-9._-]*)(\[[^\]]*\])?`)
	pythonWheelNameRE  = regexp.MustCompile(`(?:^|/)([A-Za-z0-9][A-Za-z0-9._]*)-[0-9][^/-]*(?:-[0-9][^/-]*)?-[^/-]+-[^/-]+-[^/-]+\.whl$`)
)

// parsePythonRequirement reads one PEP 508 requirement string: a name, its
// extras, a version specifier (with or without parentheses), a direct
// reference after `@`, and an environment marker after `;`.
func parsePythonRequirement(raw string) (pythonRequirement, bool) {
	text := strings.TrimSpace(raw)
	requirement := pythonRequirement{text: text}
	body, marker, _ := strings.Cut(text, ";")
	requirement.marker = strings.TrimSpace(marker)
	match := pythonPEP508NameRE.FindStringSubmatch(body)
	if match == nil {
		return pythonRequirement{}, false
	}
	requirement.name = normalizePythonName(match[1])
	requirement.extras = pythonExtras(match[2])
	rest := strings.TrimSpace(body[len(match[0]):])
	if strings.HasPrefix(rest, "@") {
		requirement.url = strings.TrimSpace(strings.TrimPrefix(rest, "@"))
		// A marker after a URL is separated by " ;", which the cut above
		// already removed; a URL keeps its own fragment.
		return requirement, requirement.url != ""
	}
	rest = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(rest, "("), ")"))
	if rest != "" && !strings.ContainsAny(rest[:1], "<>=!~") {
		return pythonRequirement{}, false
	}
	requirement.specifier = strings.ReplaceAll(rest, " ", "")
	return requirement, true
}

func pythonExtras(bracketed string) []string {
	bracketed = strings.Trim(strings.TrimSpace(bracketed), "[]")
	if bracketed == "" {
		return nil
	}
	var extras []string
	for _, extra := range strings.Split(bracketed, ",") {
		if extra = strings.ToLower(strings.TrimSpace(extra)); extra != "" {
			extras = append(extras, extra)
		}
	}
	return extras
}

// pythonIndexOption is a package index a requirement file or a pyproject
// table names, by the option or table that named it.
type pythonIndexOption struct {
	option string // --index-url, --extra-index-url, --find-links, poetry, uv, pdm, pipenv
	name   string // the source's name in a pyproject or Pipfile table
	url    string
	file   string
}

// pythonRequirementsFile is one requirement file as pip reads it.
type pythonRequirementsFile struct {
	path         string
	requirements []pythonRequirement
	// includes and constraints are the -r and -c targets, relative to the
	// root the file belongs to; an include that leaves it is kept as written
	// in escapes, since the build cannot read it.
	includes    []string
	constraints []string
	escapes     []string
	indexes     []pythonIndexOption
	// variables are the ${NAME} references pip expands in the file.
	variables []string
}

var pythonRequirementVariableRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// parsePythonRequirementsFile reads a requirement file at file (relative to
// its root). Continuation lines are joined and comments dropped as pip does;
// options are kept apart from requirements.
func parsePythonRequirementsFile(file string, content []byte) pythonRequirementsFile {
	result := pythonRequirementsFile{path: file}
	text := strings.ReplaceAll(string(manifestText(content)), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	directory := path.Dir(file)
	for index := 0; index < len(lines) && index < 4096; index++ {
		line := lines[index]
		for strings.HasSuffix(line, `\`) && index+1 < len(lines) {
			index++
			line = strings.TrimSuffix(line, `\`) + " " + lines[index]
		}
		line = stripRequirementComment(line)
		if line == "" {
			continue
		}
		for _, match := range pythonRequirementVariableRE.FindAllStringSubmatch(line, -1) {
			result.variables = appendUnique(result.variables, match[1])
		}
		if strings.HasPrefix(line, "-") {
			option, value := requirementOption(line)
			switch option {
			case "-r", "--requirement", "-c", "--constraint":
				if value == "" || strings.Contains(value, "://") {
					continue
				}
				target := path.Clean(path.Join(directory, value))
				switch {
				case target == ".." || strings.HasPrefix(target, "../") || path.IsAbs(value):
					result.escapes = append(result.escapes, value)
				case option == "-c" || option == "--constraint":
					result.constraints = append(result.constraints, target)
				default:
					result.includes = append(result.includes, target)
				}
			case "-i", "--index-url", "--extra-index-url", "-f", "--find-links":
				if option == "-i" {
					option = "--index-url"
				} else if option == "-f" {
					option = "--find-links"
				}
				result.indexes = append(result.indexes, pythonIndexOption{option: option, url: value, file: file})
			case "-e", "--editable":
				if requirement, ok := requirementFromReference(value); ok {
					requirement.editable, requirement.file, requirement.text = true, file, line
					result.requirements = append(result.requirements, requirement)
				} else {
					result.requirements = append(result.requirements, pythonRequirement{url: value, editable: true, file: file, text: line})
				}
			}
			continue
		}
		// Per-requirement options (--hash, --config-settings) follow the
		// requirement and a space.
		if cut := strings.Index(line, " --"); cut >= 0 {
			line = strings.TrimSpace(line[:cut])
		}
		requirement, ok := parsePythonRequirement(line)
		if !ok {
			requirement, ok = requirementFromReference(line)
		}
		if !ok {
			continue
		}
		requirement.file, requirement.text = file, line
		result.requirements = append(result.requirements, requirement)
	}
	return result
}

// stripRequirementComment drops a comment the way pip does: at the start of
// a line, or after whitespace, so a URL's #egg= fragment survives.
func stripRequirementComment(line string) string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "#") {
		return ""
	}
	for index := 1; index < len(line); index++ {
		if line[index] == '#' && (line[index-1] == ' ' || line[index-1] == '\t') {
			return strings.TrimSpace(line[:index])
		}
	}
	return line
}

// requirementOption splits `-r x`, `-rx`, `--requirement=x` and
// `--requirement x` into the option and its value.
func requirementOption(line string) (string, string) {
	fields := strings.Fields(line)
	first := fields[0]
	if name, value, found := strings.Cut(first, "="); found && strings.HasPrefix(first, "--") {
		return name, strings.Trim(value, `'"`)
	}
	if !strings.HasPrefix(first, "--") && len(first) > 2 {
		return first[:2], strings.Trim(first[2:], `'"`)
	}
	if len(fields) > 1 {
		return first, strings.Trim(fields[1], `'"`)
	}
	return first, ""
}

// requirementFromReference names a requirement given as a URL or a path:
// a VCS URL's #egg=, or a wheel's file name. Anything else keeps only its
// reference, so a local path still counts as a declared install.
func requirementFromReference(value string) (pythonRequirement, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return pythonRequirement{}, false
	}
	requirement := pythonRequirement{url: value}
	if match := pythonEggRE.FindStringSubmatch(value); match != nil {
		requirement.name, requirement.extras = normalizePythonName(match[1]), pythonExtras(match[2])
		return requirement, true
	}
	if match := pythonWheelNameRE.FindStringSubmatch(strings.SplitN(value, "#", 2)[0]); match != nil {
		requirement.name = normalizePythonName(match[1])
		return requirement, true
	}
	if strings.Contains(value, "://") || strings.HasPrefix(value, ".") || strings.HasPrefix(value, "/") || strings.Contains(value, `\`) {
		return requirement, true
	}
	return pythonRequirement{}, false
}

// pythonRequirementsNameRE is a requirement file at a root other than
// requirements.txt itself: requirements-prod.txt, requirements_dev.txt.
var pythonRequirementsNameRE = regexp.MustCompile(`^requirements[-_.][\w.-]+\.txt$`)

// pythonDevelopmentRequirements are the stems of requirement files a
// deployment never installs from.
var pythonDevelopmentRequirements = map[string]bool{
	"dev": true, "development": true, "local": true, "test": true, "tests": true, "testing": true,
	"docs": true, "doc": true, "lint": true, "linting": true, "ci": true, "typing": true, "mypy": true,
	"style": true, "tox": true, "build": true, "debug": true, "optional": true, "extras": true, "e2e": true,
}

// pythonManifestTarget says which root a walked file is a Python manifest of,
// and the key it is kept under there: the file name at its own directory,
// or requirements/<name> for a requirements/ folder beside the project.
// requirements.txt, pyproject.toml and the uv and Poetry locks are the walk's
// own markers and are not returned here.
func pythonManifestTarget(rel string) (string, string, bool) {
	rel = path.Clean(rel)
	directory, base := path.Dir(rel), path.Base(rel)
	if directory == "." {
		directory = ""
	}
	lower := strings.ToLower(base)
	switch {
	case lower == "pipfile", lower == "pipfile.lock", lower == "pdm.lock", lower == "setup.py", lower == "setup.cfg",
		lower == "environment.yml", lower == "environment.yaml", pythonRequirementsNameRE.MatchString(lower):
		return directory, base, true
	case strings.HasSuffix(lower, ".txt") && strings.EqualFold(path.Base(directory), "requirements") && directory != "":
		owner := path.Dir(directory)
		if owner == "." {
			owner = ""
		}
		return owner, path.Base(directory) + "/" + base, true
	}
	return "", "", false
}

// pythonManifestName names the files pythonManifestTarget keeps, so the walk
// reads them in its first, breadth-first pass beside the other manifests.
func pythonManifestName(lower string) bool {
	switch lower {
	case "pipfile", "pipfile.lock", "pdm.lock", "setup.py", "setup.cfg", "environment.yml", "environment.yaml":
		return true
	}
	return pythonRequirementsNameRE.MatchString(lower)
}

// pythonRequirementsFiles are the requirement files a root holds.
func pythonRequirementsFiles(files map[string][]byte) []string {
	var names []string
	for name := range files {
		if pythonRequirementsFileKey(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func pythonRequirementsFileKey(name string) bool {
	lower := strings.ToLower(name)
	return lower == "requirements.txt" || pythonRequirementsNameRE.MatchString(lower) ||
		(strings.HasPrefix(lower, "requirements/") && strings.HasSuffix(lower, ".txt") && !strings.Contains(lower[len("requirements/"):], "/"))
}

// pythonRequirementsStem is what a requirement file is for: "prod" for
// requirements-prod.txt and requirements/prod.txt, "" for requirements.txt.
func pythonRequirementsStem(name string) string {
	lower := strings.ToLower(name)
	switch {
	case lower == "requirements.txt":
		return ""
	case strings.HasPrefix(lower, "requirements/"):
		return strings.TrimSuffix(strings.TrimPrefix(lower, "requirements/"), ".txt")
	}
	return strings.TrimSuffix(lower[len("requirements")+1:], ".txt")
}

// pythonInstallRequirements is the requirement file a deployment installs
// from: requirements.txt, then the production file of a split layout, then
// its shared base, then the only file that is not for development.
func pythonInstallRequirements(files map[string][]byte) string {
	names := pythonRequirementsFiles(files)
	if environment, ok := readCondaEnvironment(files); ok && len(environment.unmapped()) == 0 {
		// A file the environment's pip list includes is part of the
		// environment, not an install of its own — unless the environment
		// needs conda packages the recipe cannot install, when the file is
		// still the best install there is.
		included := environment.pipList.paths()
		names = slices.DeleteFunc(names, func(name string) bool { return slices.Contains(included, name) })
	}
	has := map[string]string{}
	for _, name := range names {
		has[strings.ToLower(name)] = name
	}
	for _, preferred := range []string{
		"requirements.txt", "requirements/production.txt", "requirements/prod.txt", "requirements-prod.txt",
		"requirements-production.txt", "requirements_prod.txt", "requirements_production.txt",
		"requirements/base.txt", "requirements/common.txt", "requirements/main.txt", "requirements/requirements.txt",
	} {
		if name, ok := has[preferred]; ok {
			return name
		}
	}
	remaining := []string{}
	for _, name := range names {
		if !pythonDevelopmentRequirements[pythonRequirementsStem(name)] {
			remaining = append(remaining, name)
		}
	}
	if len(remaining) == 1 {
		return remaining[0]
	}
	return ""
}

// pythonMissingIncludes are the files an install reads that files does not
// hold yet: what the install requirement file includes, and what a conda
// environment's pip list does.
func pythonMissingIncludes(files map[string][]byte) []string {
	missing := readPythonRequirementClosure(files, pythonInstallRequirements(files)).missing
	if environment, ok := readCondaEnvironment(files); ok {
		missing = append(missing, environment.pipList.missing...)
	}
	return missing
}

// pythonRequirementClosure is the install file and every file it includes,
// in the order pip reads them, at most 16 files and three includes deep.
// Missing names an include the files do not hold.
type pythonRequirementClosure struct {
	files   []pythonRequirementsFile
	missing []string
	escapes []string
}

func readPythonRequirementClosure(files map[string][]byte, start string) pythonRequirementClosure {
	var closure pythonRequirementClosure
	if start == "" {
		return closure
	}
	seen := map[string]bool{}
	var visit func(name string, depth int)
	visit = func(name string, depth int) {
		if seen[name] || len(closure.files) >= 16 {
			return
		}
		seen[name] = true
		content, ok := files[name]
		if !ok {
			closure.missing = append(closure.missing, name)
			return
		}
		parsed := parsePythonRequirementsFile(name, content)
		closure.files = append(closure.files, parsed)
		closure.escapes = append(closure.escapes, parsed.escapes...)
		if depth >= 3 {
			return
		}
		for _, include := range parsed.includes {
			visit(include, depth+1)
		}
	}
	visit(start, 0)
	return closure
}

// requirements lists what the closure installs, first mention first.
func (c pythonRequirementClosure) requirements() []pythonRequirement {
	var result []pythonRequirement
	for _, file := range c.files {
		result = append(result, file.requirements...)
	}
	return result
}

func (c pythonRequirementClosure) indexes() []pythonIndexOption {
	var result []pythonIndexOption
	for _, file := range c.files {
		result = append(result, file.indexes...)
	}
	return result
}

func (c pythonRequirementClosure) paths() []string {
	var result []string
	for _, file := range c.files {
		result = append(result, file.path)
	}
	return result
}

// pyprojectFacts is what a pyproject.toml declares, read as data.
type pyprojectFacts struct {
	present bool
	name    string
	// project says a [project] table exists; dependencies are its
	// dependencies array, and declaresDependencies that the key is there.
	project              bool
	dependencies         []pythonRequirement
	declaresDependencies bool
	optional             map[string][]pythonRequirement
	groups               map[string][]pythonRequirement
	requiresPython       string
	// Poetry: the main dependency table (without python), its python
	// constraint, the named groups, and whether packages come from src/.
	poetry        bool
	poetryMain    []pythonRequirement
	poetryPython  string
	poetryGroups  map[string][]string
	poetryFromSrc bool
	// uv: how [tool.uv.sources] resolves a name (workspace, path, git,
	// index, url), the workspace members this file declares, and whether the
	// project is a package uv installs.
	uvSources          map[string]string
	uvWorkspaceMembers []string
	uvWorkspaceExclude []string
	uvDevDependencies  []string
	pdm                bool
	indexes            []pythonIndexOption
	buildSystem        bool
	// scripts are [project.scripts] and [tool.poetry.scripts] console
	// entries; tasks the start commands task runners declare.
	scripts       map[string]string
	tasks         []pythonTask
	setuptoolsSrc bool
	hatchPackages []string
	aerich        bool
}

// pythonTask is a command a pyproject task runner declares under a name that
// says it serves: start, serve, server, web, prod. `run` is left out: it is
// as often a script or a development server as the production process.
type pythonTask struct {
	runner  string // pdm, poe, taskipy, hatch
	name    string
	command string
	// composite is a task that runs other tasks or several commands, which
	// is a decision rather than a start command.
	composite bool
}

var pythonTaskNames = []string{"start", "serve", "server", "web", "prod", "production"}

var (
	poetryFromSrcRE = regexp.MustCompile(`from\s*=\s*["']src["']`)
	// A table header, plain or an array of tables, which ends the section
	// before it either way.
	pyprojectTableRE = regexp.MustCompile(`(?m)^\s*(\[\[?)([^\[\]]+)\]\]?\s*(?:#.*)?$`)
)

func readPyproject(content []byte) pyprojectFacts {
	facts := pyprojectFacts{optional: map[string][]pythonRequirement{}, groups: map[string][]pythonRequirement{},
		poetryGroups: map[string][]string{}, uvSources: map[string]string{}, scripts: map[string]string{}}
	if len(content) == 0 || string(content) == "locked" {
		return facts
	}
	facts.present = true
	text := string(manifestText(content))
	entries := readTOML([]byte(text))
	requirements := func(file string, values []string) []pythonRequirement {
		var result []pythonRequirement
		for _, value := range values {
			if requirement, ok := parsePythonRequirement(value); ok {
				requirement.file = file
				result = append(result, requirement)
			}
		}
		return result
	}
	tasks := map[string]pythonTask{}
	addTask := func(runner, table, key string, value tomlValue) {
		name, field, _ := strings.Cut(key, ".")
		if !slices.Contains(pythonTaskNames, name) {
			return
		}
		existing, seen := tasks[runner+":"+name]
		task := pythonTask{runner: runner, name: name}
		if seen {
			task = existing
		}
		switch {
		case field == "" && value.isList:
			task.composite = true
		case field == "" || field == "cmd" || field == "shell" || field == "call":
			if task.command == "" {
				task.command = strings.TrimSpace(value.text)
			}
			if field == "call" {
				task.composite = true
			}
		case field == "composite" || field == "sequence" || field == "script" || field == "ref":
			task.composite = true
		default:
			return
		}
		tasks[runner+":"+name] = task
	}
	for _, entry := range entries {
		switch {
		case entry.table == "project":
			facts.project = true
			switch entry.key {
			case "name":
				facts.name = entry.value.text
			case "dependencies":
				facts.declaresDependencies = true
				facts.dependencies = requirements("pyproject.toml", entry.value.list)
			case "requires-python":
				facts.requiresPython = entry.value.text
			}
		case entry.table == "project.optional-dependencies":
			facts.optional[entry.key] = requirements("pyproject.toml", entry.value.list)
		case entry.table == "dependency-groups":
			facts.groups[entry.key] = requirements("pyproject.toml", entry.value.list)
		case entry.table == "project.scripts", entry.table == "tool.poetry.scripts":
			facts.scripts[entry.key] = entry.value.text
		case entry.table == "tool.poetry" || strings.HasPrefix(entry.table, "tool.poetry."):
			facts.poetry = true
			if entry.table == "tool.poetry" && entry.key == "name" && facts.name == "" {
				facts.name = entry.value.text
			}
		case entry.table == "tool.uv.sources":
			name, kind, _ := strings.Cut(entry.key, ".")
			if kind != "" && facts.uvSources[normalizePythonName(name)] == "" {
				facts.uvSources[normalizePythonName(name)] = kind
			}
		case entry.table == "tool.uv.workspace":
			if entry.key == "members" {
				facts.uvWorkspaceMembers = entry.value.list
			} else if entry.key == "exclude" {
				facts.uvWorkspaceExclude = entry.value.list
			}
		case entry.table == "tool.uv" && entry.key == "dev-dependencies":
			facts.uvDevDependencies = entry.value.list
		case entry.table == "tool.uv.index":
			if entry.index >= 0 {
				for len(facts.indexes) <= entry.index {
					facts.indexes = append(facts.indexes, pythonIndexOption{option: "uv", file: "pyproject.toml"})
				}
			}
		case entry.table == "tool.pdm" || strings.HasPrefix(entry.table, "tool.pdm."):
			facts.pdm = true
			if entry.table == "tool.pdm.scripts" {
				addTask("pdm", entry.table, entry.key, entry.value)
			} else if strings.HasPrefix(entry.table, "tool.pdm.scripts.") {
				addTask("pdm", entry.table, strings.TrimPrefix(entry.table, "tool.pdm.scripts.")+"."+entry.key, entry.value)
			}
		case entry.table == "tool.poe.tasks":
			addTask("poe", entry.table, entry.key, entry.value)
		case strings.HasPrefix(entry.table, "tool.poe.tasks."):
			addTask("poe", entry.table, strings.TrimPrefix(entry.table, "tool.poe.tasks.")+"."+entry.key, entry.value)
		case entry.table == "tool.taskipy.tasks":
			addTask("taskipy", entry.table, entry.key, entry.value)
		case entry.table == "tool.hatch.envs.default.scripts":
			addTask("hatch", entry.table, entry.key, entry.value)
		case entry.table == "build-system":
			facts.buildSystem = true
		case entry.table == "tool.setuptools.packages.find" && entry.key == "where":
			facts.setuptoolsSrc = slices.Contains(entry.value.list, "src")
		case entry.table == "tool.setuptools" && entry.key == "package-dir.":
			facts.setuptoolsSrc = entry.value.text == "src"
		case strings.HasPrefix(entry.table, "tool.hatch.build") && entry.key == "packages":
			facts.hatchPackages = entry.value.list
		case entry.table == "tool.aerich":
			facts.aerich = true
		}
	}
	// Index tables are arrays of tables whose URL and name the generic
	// reader keeps per ordinal.
	for _, table := range []struct{ name, option string }{{"tool.uv.index", "uv"}, {"tool.poetry.source", "poetry"}, {"tool.pdm.source", "pdm"}} {
		for _, source := range tomlArrayTables(entries, table.name) {
			if source["url"].text == "" {
				continue
			}
			facts.indexes = append(facts.indexes, pythonIndexOption{option: table.option, name: source["name"].text, url: source["url"].text, file: "pyproject.toml"})
		}
	}
	kept := facts.indexes[:0]
	for _, index := range facts.indexes {
		if index.url != "" {
			kept = append(kept, index)
		}
	}
	facts.indexes = kept
	if tomlText(entries, "tool.setuptools", "package-dir.") == "src" {
		facts.setuptoolsSrc = true
	}
	// Poetry's dependency tables hold one requirement per key, whose value
	// may be a string or an inline table; the key is all detection needs,
	// and a key can hold dots, so the section's lines are read directly.
	for _, section := range pyprojectSections(text) {
		switch {
		case section.table == "tool.poetry":
			if poetryFromSrcRE.MatchString(section.body) {
				facts.poetryFromSrc = true
			}
		case section.table == "tool.poetry.dependencies":
			for _, key := range tableKeys(section.body) {
				if strings.EqualFold(key.name, "python") {
					facts.poetryPython = tomlScalar(key.value)
					continue
				}
				requirement := pythonRequirement{name: normalizePythonName(key.name), file: "pyproject.toml", text: key.name + " = " + key.value}
				if extras := poetryExtrasRE.FindStringSubmatch(key.value); extras != nil {
					requirement.extras = pythonExtras(strings.ReplaceAll(strings.ReplaceAll(extras[1], `"`, ""), "'", ""))
				}
				if strings.Contains(key.value, "git") && strings.Contains(key.value, "=") && poetryGitRE.MatchString(key.value) {
					requirement.url = "git"
				}
				facts.poetryMain = append(facts.poetryMain, requirement)
			}
		case section.table == "tool.poetry.dev-dependencies":
			for _, key := range tableKeys(section.body) {
				facts.poetryGroups["dev"] = append(facts.poetryGroups["dev"], normalizePythonName(key.name))
			}
		case strings.HasPrefix(section.table, "tool.poetry.group.") && strings.HasSuffix(section.table, ".dependencies"):
			group := strings.TrimSuffix(strings.TrimPrefix(section.table, "tool.poetry.group."), ".dependencies")
			for _, key := range tableKeys(section.body) {
				facts.poetryGroups[group] = append(facts.poetryGroups[group], normalizePythonName(key.name))
			}
		}
	}
	for _, task := range tasks {
		facts.tasks = append(facts.tasks, task)
	}
	sort.Slice(facts.tasks, func(i, j int) bool {
		return pythonTaskRank(facts.tasks[i]) < pythonTaskRank(facts.tasks[j])
	})
	return facts
}

var (
	poetryExtrasRE = regexp.MustCompile(`extras\s*=\s*\[([^\]]*)\]`)
	poetryGitRE    = regexp.MustCompile(`\bgit\s*=`)
)

func pythonTaskRank(task pythonTask) int {
	for index, name := range pythonTaskNames {
		if task.name == name {
			return index*8 + map[string]int{"pdm": 0, "poe": 1, "hatch": 2, "taskipy": 3}[task.runner]
		}
	}
	return 1 << 20
}

type pyprojectSection struct {
	table string
	body  string
}

// pyprojectSections splits a TOML document into its plain tables' bodies.
func pyprojectSections(text string) []pyprojectSection {
	var sections []pyprojectSection
	locations := pyprojectTableRE.FindAllStringSubmatchIndex(text, -1)
	for index, location := range locations {
		end := len(text)
		if index+1 < len(locations) {
			end = locations[index+1][0]
		}
		table := normalizeTOMLKey(strings.TrimSpace(text[location[4]:location[5]]))
		if text[location[2]:location[3]] == "[[" {
			table = "[[" + table + "]]"
		}
		sections = append(sections, pyprojectSection{table: table, body: text[location[1]:end]})
	}
	return sections
}

type tableKey struct{ name, value string }

// tableKeys reads a table body's `key = value` lines; a value's own
// continuation lines are not keys.
func tableKeys(body string) []tableKey {
	var keys []tableKey
	depth := 0
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(stripTOMLComment(raw))
		if line == "" {
			continue
		}
		if depth == 0 {
			if name, value, found := strings.Cut(line, "="); found {
				name = strings.Trim(strings.TrimSpace(name), `"'`)
				if name != "" && !strings.ContainsAny(name, " {}[]") {
					keys = append(keys, tableKey{name: name, value: strings.TrimSpace(value)})
				}
			}
		}
		depth += strings.Count(line, "[") + strings.Count(line, "{") - strings.Count(line, "]") - strings.Count(line, "}")
		if depth < 0 {
			depth = 0
		}
	}
	return keys
}

// pipfileFacts is what a Pipfile declares.
type pipfileFacts struct {
	present  bool
	packages []pythonRequirement
	dev      []string
	python   string
	indexes  []pythonIndexOption
}

func readPipfile(content []byte) pipfileFacts {
	facts := pipfileFacts{}
	if len(content) == 0 {
		return facts
	}
	facts.present = true
	text := string(manifestText(content))
	for _, section := range pyprojectSections(text) {
		switch section.table {
		case "packages":
			for _, key := range tableKeys(section.body) {
				requirement := pythonRequirement{name: normalizePythonName(key.name), file: "Pipfile", text: key.name + " = " + key.value}
				value := tomlScalar(key.value)
				if strings.HasPrefix(key.value, "{") {
					if extras := poetryExtrasRE.FindStringSubmatch(key.value); extras != nil {
						requirement.extras = pythonExtras(strings.ReplaceAll(strings.ReplaceAll(extras[1], `"`, ""), "'", ""))
					}
					if poetryGitRE.MatchString(key.value) {
						requirement.url = "git"
					}
					if version := pipfileVersionRE.FindStringSubmatch(key.value); version != nil {
						value = version[1]
					} else {
						value = ""
					}
				}
				if value != "*" {
					requirement.specifier = strings.ReplaceAll(value, " ", "")
				}
				facts.packages = append(facts.packages, requirement)
			}
		case "dev-packages":
			for _, key := range tableKeys(section.body) {
				facts.dev = append(facts.dev, normalizePythonName(key.name))
			}
		case "requires":
			for _, key := range tableKeys(section.body) {
				if key.name == "python_version" || (key.name == "python_full_version" && facts.python == "") {
					facts.python = tomlScalar(key.value)
				}
			}
		}
	}
	for _, source := range tomlArrayTables(readTOML([]byte(text)), "source") {
		if url := source["url"].text; url != "" && !strings.Contains(url, "pypi.org/simple") {
			facts.indexes = append(facts.indexes, pythonIndexOption{option: "pipenv", name: source["name"].text, url: url, file: "Pipfile"})
		}
	}
	return facts
}

var pipfileVersionRE = regexp.MustCompile(`version\s*=\s*["']([^"']+)["']`)

// pipfileLock is Pipfile.lock's main section: each package and its pin.
type pipfileLock struct {
	present  bool
	readable bool
	packages map[string]string
	git      []string
	python   string
}

func readPipfileLock(content []byte) pipfileLock {
	lock := pipfileLock{packages: map[string]string{}}
	if len(content) == 0 {
		return lock
	}
	lock.present = true
	var document struct {
		Meta struct {
			Requires struct {
				PythonVersion string `json:"python_version"`
			} `json:"requires"`
		} `json:"_meta"`
		Default map[string]struct {
			Version string `json:"version"`
			Git     string `json:"git"`
		} `json:"default"`
	}
	if json.Unmarshal(manifestText(content), &document) != nil {
		return lock
	}
	lock.readable = true
	lock.python = document.Meta.Requires.PythonVersion
	for name, entry := range document.Default {
		lock.packages[normalizePythonName(name)] = entry.Version
		if entry.Git != "" {
			lock.git = append(lock.git, normalizePythonName(name))
		}
	}
	return lock
}

// setupRequirements reads install_requires from setup.cfg's [options] (INI
// data) or a literal list in setup.py. setup.py is never executed: a list
// built in code is not read, and the project is then not a manifest.
func setupRequirements(files map[string][]byte) ([]pythonRequirement, string, bool) {
	if content, ok := files["setup.cfg"]; ok {
		if requirements, python, found := setupCfgRequirements(string(manifestText(content))); found {
			return requirements, python, true
		}
	}
	if content, ok := files["setup.py"]; ok {
		return setupPyRequirements(string(manifestText(content)))
	}
	return nil, "", false
}

var setupPythonRequiresRE = regexp.MustCompile(`python_requires\s*=\s*["']([^"']+)["']`)

func setupCfgRequirements(text string) ([]pythonRequirement, string, bool) {
	section, key, found := "", "", false
	python := ""
	var requirements []pythonRequirement
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section, key = strings.ToLower(strings.Trim(trimmed, "[] ")), ""
			continue
		}
		continuation := raw[0] == ' ' || raw[0] == '\t'
		if !continuation {
			name, value, _ := strings.Cut(trimmed, "=")
			key = strings.ToLower(strings.TrimSpace(name))
			trimmed = strings.TrimSpace(value)
			if section == "options" && key == "python_requires" {
				python = trimmed
			}
		}
		if section != "options" || key != "install_requires" {
			continue
		}
		found = true
		if trimmed == "" {
			continue
		}
		if requirement, ok := parsePythonRequirement(trimmed); ok {
			requirement.file = "setup.cfg"
			requirements = append(requirements, requirement)
		}
	}
	return requirements, python, found
}

var setupInstallRequiresRE = regexp.MustCompile(`install_requires\s*=\s*\[`)

func setupPyRequirements(text string) ([]pythonRequirement, string, bool) {
	location := setupInstallRequiresRE.FindStringIndex(text)
	python := ""
	if match := setupPythonRequiresRE.FindStringSubmatch(text); match != nil {
		python = match[1]
	}
	if location == nil {
		return nil, python, false
	}
	var scan tomlArrayScan
	body := text[location[1]-1:]
	end := -1
	for index := range body {
		if scan.feed(body[index:index+1]) && index > 0 {
			end = index
			break
		}
	}
	if end < 0 {
		return nil, python, false
	}
	var requirements []pythonRequirement
	for _, value := range tomlStrings(body[:end+1]) {
		if requirement, ok := parsePythonRequirement(value); ok {
			requirement.file = "setup.py"
			requirements = append(requirements, requirement)
		}
	}
	return requirements, python, true
}

// condaEnvironment is a conda environment.yml: its conda packages, the pip
// sub-list, and the Python it pins. conda hands the pip list to pip as a
// requirement file written beside environment.yml, so pipList reads it the
// way pip does — markers, direct references, index options, and the -r
// includes it follows — and pip is what that installs.
type condaEnvironment struct {
	file     string
	python   string
	conda    []condaPackage
	pip      []pythonRequirement
	pipLines []string
	pipList  pythonRequirementClosure
}

type condaPackage struct {
	name, version string
}

// unmapped are the conda packages with no PyPI equivalent the recipe knows.
func (e condaEnvironment) unmapped() []string {
	var names []string
	for _, pkg := range e.conda {
		if condaPyPIName(pkg.name) == "" && !condaKnown(pkg.name) {
			names = append(names, pkg.name)
		}
	}
	return names
}

func readCondaEnvironment(files map[string][]byte) (condaEnvironment, bool) {
	for _, name := range []string{"environment.yml", "environment.yaml"} {
		content, ok := files[name]
		if !ok {
			continue
		}
		var document struct {
			Dependencies []yaml.Node `yaml:"dependencies"`
		}
		if yaml.Unmarshal(manifestText(content), &document) != nil {
			return condaEnvironment{}, false
		}
		environment := condaEnvironment{file: name}
		for _, node := range document.Dependencies {
			switch node.Kind {
			case yaml.ScalarNode:
				spec := strings.TrimSpace(node.Value)
				name, version := condaSpec(spec)
				if name == "" {
					continue
				}
				if name == "python" {
					environment.python = version
					continue
				}
				environment.conda = append(environment.conda, condaPackage{name: name, version: version})
			case yaml.MappingNode:
				for index := 0; index+1 < len(node.Content); index += 2 {
					if node.Content[index].Value != "pip" || node.Content[index+1].Kind != yaml.SequenceNode {
						continue
					}
					for _, item := range node.Content[index+1].Content {
						environment.pipLines = append(environment.pipLines, strings.TrimSpace(item.Value))
					}
				}
			}
		}
		list := parsePythonRequirementsFile(name, []byte(strings.Join(environment.pipLines, "\n")))
		environment.pipList = pythonRequirementClosure{files: []pythonRequirementsFile{list}, escapes: list.escapes}
		for _, include := range list.includes {
			included := readPythonRequirementClosure(files, include)
			environment.pipList.files = append(environment.pipList.files, included.files...)
			environment.pipList.missing = append(environment.pipList.missing, included.missing...)
			environment.pipList.escapes = append(environment.pipList.escapes, included.escapes...)
		}
		environment.pip = environment.pipList.requirements()
		return environment, true
	}
	return condaEnvironment{}, false
}

var condaSpecRE = regexp.MustCompile(`^(?:[\w.-]+::)?([A-Za-z0-9_][A-Za-z0-9._-]*)\s*(?:(==|>=|<=|>|<|=|!=)\s*([^=\s]+))?`)

// condaSpec reads `numpy=1.26`, `numpy==1.26.4`, `numpy>=1.2`,
// `conda-forge::numpy=1.26.4=py311h…` into a name and the version part the
// build string does not belong to.
func condaSpec(spec string) (string, string) {
	match := condaSpecRE.FindStringSubmatch(spec)
	if match == nil {
		return "", ""
	}
	name := strings.ToLower(match[1])
	if match[2] == "" {
		return name, ""
	}
	return name, match[2] + match[3]
}
