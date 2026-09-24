package deploy

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// A repository that already ran somewhere else says how it runs there:
// render.yaml's startCommand and healthCheckPath, fly.toml's internal_port,
// app.json's generated secrets, netlify.toml's publish directory, Kamal's
// config/deploy.yml with its job role and volumes. Detection used to read the
// Procfile alone and guess the rest. These files are now read as bounded data
// — never evaluated, their commands screened like any other — and what they
// declare ranks below the operator's input and the Procfile and above a
// framework's defaults. Values are never imported from them: a secret is a
// name to fill in or generate, and a host-shaped value belongs to the old host.

// DetectedPlatformManifest is what one platform file declares for a
// candidate, and which of those facts the candidate took.
type DetectedPlatformManifest struct {
	File               string   `json:"file"`
	Platform           string   `json:"platform"`
	StartCommand       string   `json:"startCommand,omitempty"`
	BuildCommand       string   `json:"buildCommand,omitempty"`
	OutputDirectory    string   `json:"outputDirectory,omitempty"`
	Port               int      `json:"port,omitempty"`
	HealthPath         string   `json:"healthPath,omitempty"`
	SPAFallback        bool     `json:"spaFallback,omitempty"`
	Dockerfile         string   `json:"dockerfile,omitempty"`
	ReleaseCommand     string   `json:"releaseCommand,omitempty"`
	GeneratedVariables []string `json:"generatedVariables,omitempty"`
	RequiredVariables  []string `json:"requiredVariables,omitempty"`
	Volumes            []string `json:"volumes,omitempty"`
	SystemPackages     []string `json:"systemPackages,omitempty"`
	Redirects          int      `json:"redirects,omitempty"`
	Toolchains         []string `json:"toolchains,omitempty"`
	// Applied names the facts the candidate took from this file.
	Applied []string `json:"applied,omitempty"`
}

var platformNames = map[string]string{
	"fly": "fly.toml", "render": "render.yaml", "railway": "Railway", "heroku": "Heroku", "nixpacks": "nixpacks.toml",
	"netlify": "netlify.toml", "vercel": "vercel.json", "digitalocean": "DigitalOcean app spec", "kamal": "Kamal",
	"readthedocs": "Read the Docs", "huggingface": "Hugging Face Spaces", "firebase": "firebase.json", "aptfile": "Aptfile",
}

// kamalClearAllowlist are the only Kamal env.clear values brought over, as
// examples: framework toggles, never a host, an IP or a URL.
var kamalClearAllowlist = map[string]bool{
	"SOLID_QUEUE_IN_PUMA": true, "WEB_CONCURRENCY": true, "JOB_CONCURRENCY": true, "RAILS_LOG_LEVEL": true,
	"RAILS_MAX_THREADS": true, "RAILS_SERVE_STATIC_FILES": true, "RAILS_LOG_TO_STDOUT": true,
}

var (
	hostShapedRE       = regexp.MustCompile(`(?i)://|^[a-z0-9-]+(\.[a-z0-9-]+)+(:[0-9]+)?$|^[0-9]{1,3}(\.[0-9]{1,3}){3}`)
	aptPackageRE       = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{0,127}$`)
	erbTagRE           = regexp.MustCompile(`<%.*?%>`)
	commandSeparatorRE = regexp.MustCompile(`\s*(?:&&|;)\s*`)
	installSegments    = regexp.MustCompile(`^(?:npm (?:ci|install|i)(?:\s|$)|yarn(?:\s+install)?(?:\s+--[a-z-]+)*$|pnpm (?:i|install)(?:\s|$)|bun (?:i|install)(?:\s|$)|pip3? install\s|python3? -m pip install\s|poetry install(?:\s|$)|uv sync(?:\s|$)|pipenv install(?:\s|$)|bundle(?:\s+install)?(?:\s+--[a-z=-]+)*$|composer install(?:\s|$)|go mod download(?:\s|$)|corepack enable(?:\s|$))`)
)

type platformTarget struct {
	root      string
	manifest  DetectedPlatformManifest
	processes []DetectedProcess
	databases []DetectedDatabase
	variables []DetectedVariable
}

func validatePlatformManifest(manifest DetectedPlatformManifest, text func(string, int) bool) error {
	malformed := fmt.Errorf("%w: platform manifest evidence is malformed", ErrInvalidPlan)
	if platformNames[manifest.Platform] == "" || manifest.File == "" || !text(manifest.File, 4096) ||
		!text(manifest.StartCommand, 4096) || !text(manifest.BuildCommand, 4096) || !text(manifest.ReleaseCommand, 4096) ||
		!text(manifest.HealthPath, 1024) || !text(manifest.Dockerfile, 4096) || manifest.Port < 0 || manifest.Port > 65535 ||
		manifest.Redirects < 0 || (manifest.OutputDirectory != "" && manifest.OutputDirectory != "." && !safeRelativePath(manifest.OutputDirectory)) ||
		len(manifest.GeneratedVariables) > 64 || len(manifest.RequiredVariables) > 64 || len(manifest.Volumes) > 32 ||
		len(manifest.SystemPackages) > 64 || len(manifest.Toolchains) > 16 || len(manifest.Applied) > 16 {
		return malformed
	}
	for _, name := range append(append([]string(nil), manifest.GeneratedVariables...), manifest.RequiredVariables...) {
		if ValidateEnvKey(name) != nil {
			return malformed
		}
	}
	for _, values := range [][]string{manifest.Volumes, manifest.SystemPackages, manifest.Toolchains, manifest.Applied} {
		for _, value := range values {
			if value == "" || !text(value, 256) {
				return malformed
			}
		}
	}
	return nil
}

// cleanPlatformCommand drops the dependency installs a platform's build
// command starts with — the recipe installs from the lockfile already — and
// refuses anything credential-shaped.
func cleanPlatformCommand(command string, build bool) string {
	command = strings.TrimSpace(strings.Join(strings.Fields(command), " "))
	if command == "" || len(command) > 1024 || rejectPlanSecretLiteral("platform command", command) != nil {
		return ""
	}
	if !build {
		return command
	}
	var kept []string
	for _, segment := range commandSeparatorRE.Split(command, -1) {
		segment = strings.TrimSpace(segment)
		if segment == "" || installSegments.MatchString(segment) {
			continue
		}
		kept = append(kept, segment)
	}
	return strings.Join(kept, " && ")
}

func platformPath(base, relative string) (string, bool) {
	relative = strings.TrimPrefix(strings.TrimSpace(relative), "./")
	relative = strings.TrimSuffix(relative, "/")
	if relative == "" || relative == "." {
		return base, true
	}
	if !safeRelativePath(relative) {
		return "", false
	}
	return path.Clean(joinRoot(base, relative)), true
}

func portOf(value string) int {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port < 1 || port > 65535 {
		return 0
	}
	return port
}

func variableFrom(name, file, example string) (DetectedVariable, bool) {
	if ValidateEnvKey(name) != nil || !envNameRE.MatchString(name) || envProvidedNames[name] {
		return DetectedVariable{}, false
	}
	variable := DetectedVariable{Name: name, Sources: []string{file}}
	if example != "" && !hostShapedRE.MatchString(example) && len(example) <= 256 &&
		!strings.ContainsAny(example, "\x00\r\n") && rejectPlanSecretLiteral("example", example) == nil {
		variable.Example = example
	}
	return variable, true
}

func engineForAddon(name string) string {
	name = strings.ToLower(name)
	switch {
	case strings.Contains(name, "postgres"), strings.HasPrefix(name, "pg"):
		return "postgres"
	case strings.Contains(name, "mysql"), strings.Contains(name, "jawsdb"), strings.Contains(name, "cleardb"):
		return "mysql"
	case strings.Contains(name, "mariadb"):
		return "mariadb"
	case strings.Contains(name, "redis"), strings.Contains(name, "valkey"), strings.Contains(name, "keyvalue"):
		return "redis"
	case strings.Contains(name, "mongo"):
		return "mongodb"
	}
	return ""
}

// platformTargets reads every platform file recorded at one root.
func (s *repoShapeScan) platformTargets(root string) []platformTarget {
	var targets []platformTarget
	add := func(target platformTarget) {
		if target.manifest.File != "" {
			targets = append(targets, target)
		}
	}
	if content, ok := s.file(root, "fly.toml"); ok {
		add(flyTarget(root, content))
	}
	if content, ok := s.file(root, "render.yaml"); ok {
		for _, target := range renderTargets(root, content) {
			add(target)
		}
	}
	for _, name := range []string{"railway.json", "railway.toml"} {
		if content, ok := s.file(root, name); ok {
			add(railwayTarget(root, name, content))
		}
	}
	if content, ok := s.file(root, "app.json"); ok {
		add(herokuAppTarget(root, content))
	}
	if content, ok := s.file(root, "heroku.yml"); ok {
		add(herokuYAMLTarget(root, content))
	}
	if content, ok := s.file(root, "nixpacks.toml"); ok {
		add(nixpacksTarget(root, content))
	}
	if content, ok := s.file(root, "netlify.toml"); ok {
		add(netlifyTarget(root, content))
	}
	if content, ok := s.file(root, "vercel.json"); ok {
		add(vercelTarget(root, content))
	}
	if content, ok := s.file(root, ".do/app.yaml"); ok {
		for _, target := range digitalOceanTargets(root, content) {
			add(target)
		}
	}
	if content, ok := s.file(root, "config/deploy.yml"); ok {
		add(kamalTarget(root, content))
	}
	if content, ok := s.file(root, "aptfile"); ok {
		add(aptfileTarget(root, content))
	}
	for _, name := range []string{".readthedocs.yaml", ".readthedocs.yml"} {
		if content, ok := s.file(root, name); ok {
			add(readTheDocsTarget(root, name, content))
		}
	}
	if content, ok := s.file(root, "readme.md"); ok {
		add(huggingFaceTarget(root, content))
	}
	if content, ok := s.file(root, "firebase.json"); ok {
		add(firebaseTarget(root, content))
	}
	for _, directory := range []string{root, joinRoot(root, "public"), joinRoot(root, "static")} {
		if content, ok := s.file(directory, "_redirects"); ok {
			target := redirectsTarget(root, joinRoot(directory, "_redirects"), content)
			add(target)
			break
		}
	}
	return targets
}

func flyTarget(root string, content []byte) platformTarget {
	entries := readTOML(content)
	file := joinRoot(root, "fly.toml")
	target := platformTarget{root: root, manifest: DetectedPlatformManifest{File: file, Platform: "fly"}}
	manifest := &target.manifest
	manifest.Dockerfile = tomlText(entries, "build", "dockerfile")
	manifest.Port = portOf(tomlText(entries, "http_service", "internal_port"))
	for _, check := range tomlArrayTables(entries, "http_service.checks") {
		if manifest.HealthPath == "" {
			manifest.HealthPath = check["path"].text
		}
	}
	for index, service := range tomlArrayTables(entries, "services") {
		if manifest.Port == 0 && index == 0 {
			manifest.Port = portOf(service["internal_port"].text)
		}
	}
	for _, check := range tomlArrayTables(entries, "services.http_checks") {
		if manifest.HealthPath == "" {
			manifest.HealthPath = check["path"].text
		}
	}
	for _, entry := range tomlTable(entries, "processes") {
		command := cleanPlatformCommand(entry.value.text, false)
		if command == "" {
			continue
		}
		if entry.key == "app" || entry.key == "web" {
			manifest.StartCommand = command
			continue
		}
		if !validProcessName(entry.key) {
			continue
		}
		target.processes = append(target.processes, DetectedProcess{Name: entry.key, Kind: processKind(entry.key, command), Command: command,
			Source: file, Reason: "fly.toml declares the " + entry.key + " process"})
	}
	if release := cleanPlatformCommand(tomlText(entries, "deploy", "release_command"), false); release != "" {
		manifest.ReleaseCommand = release
		target.processes = append(target.processes, DetectedProcess{Name: "release", Kind: "release", Command: release,
			Source: file, Reason: "fly.toml runs this release_command before each deploy"})
	}
	for _, mount := range tomlArrayTables(entries, "mounts") {
		if destination := mount["destination"].text; strings.HasPrefix(destination, "/") && len(manifest.Volumes) < 32 {
			manifest.Volumes = append(manifest.Volumes, destination)
		}
	}
	for _, entry := range tomlTable(entries, "env") {
		if variable, ok := variableFrom(entry.key, file, entry.value.text); ok {
			target.variables = append(target.variables, variable)
		}
	}
	return target
}

type renderService struct {
	Type              string `yaml:"type"`
	Name              string `yaml:"name"`
	Runtime           string `yaml:"runtime"`
	Env               string `yaml:"env"`
	RootDir           string `yaml:"rootDir"`
	BuildCommand      string `yaml:"buildCommand"`
	StartCommand      string `yaml:"startCommand"`
	HealthCheckPath   string `yaml:"healthCheckPath"`
	DockerfilePath    string `yaml:"dockerfilePath"`
	PreDeployCommand  string `yaml:"preDeployCommand"`
	StaticPublishPath string `yaml:"staticPublishPath"`
	Schedule          string `yaml:"schedule"`
	Routes            []struct {
		Type        string `yaml:"type"`
		Source      string `yaml:"source"`
		Destination string `yaml:"destination"`
	} `yaml:"routes"`
	EnvVars []struct {
		Key           string         `yaml:"key"`
		Value         any            `yaml:"value"`
		GenerateValue bool           `yaml:"generateValue"`
		Sync          *bool          `yaml:"sync"`
		FromDatabase  map[string]any `yaml:"fromDatabase"`
		FromService   map[string]any `yaml:"fromService"`
	} `yaml:"envVars"`
}

func renderTargets(root string, content []byte) []platformTarget {
	var document struct {
		Services  []renderService `yaml:"services"`
		Databases []struct {
			Name string `yaml:"name"`
		} `yaml:"databases"`
	}
	if yaml.Unmarshal(manifestText(content), &document) != nil {
		return nil
	}
	file := joinRoot(root, "render.yaml")
	byRoot := map[string]*platformTarget{}
	order := []string{}
	for _, service := range document.Services {
		serviceRoot, ok := platformPath(root, service.RootDir)
		if !ok {
			continue
		}
		target := byRoot[serviceRoot]
		if target == nil {
			target = &platformTarget{root: serviceRoot, manifest: DetectedPlatformManifest{File: file, Platform: "render"}}
			byRoot[serviceRoot] = target
			order = append(order, serviceRoot)
		}
		manifest := &target.manifest
		switch service.Type {
		case "web", "pserv":
			if manifest.StartCommand != "" || manifest.OutputDirectory != "" {
				break
			}
			runtime := service.Runtime
			if runtime == "" {
				runtime = service.Env
			}
			if runtime == "static" {
				if output, ok := platformPath("", service.StaticPublishPath); ok && output != "" {
					manifest.OutputDirectory = output
				}
				for _, route := range service.Routes {
					if route.Type == "rewrite" && route.Destination == "/index.html" {
						manifest.SPAFallback = true
					}
				}
			} else {
				manifest.StartCommand = cleanPlatformCommand(service.StartCommand, false)
			}
			manifest.BuildCommand = cleanPlatformCommand(service.BuildCommand, true)
			manifest.HealthPath = service.HealthCheckPath
			manifest.Dockerfile = strings.TrimPrefix(service.DockerfilePath, "./")
			if release := cleanPlatformCommand(service.PreDeployCommand, false); release != "" {
				manifest.ReleaseCommand = release
				target.processes = append(target.processes, DetectedProcess{Name: "release", Kind: "release", Command: release,
					Source: file, Reason: "render.yaml runs this preDeployCommand before each deploy"})
			}
		case "worker", "cron":
			command := cleanPlatformCommand(service.StartCommand, false)
			name := service.Name
			if !validProcessName(name) {
				name = service.Type
			}
			kind := "worker"
			reason := "render.yaml declares the " + name + " background worker"
			if service.Type == "cron" {
				kind = "scheduler"
				reason = "render.yaml runs " + name + " on the schedule " + boundedText(service.Schedule, 64)
			}
			target.processes = append(target.processes, DetectedProcess{Name: name, Kind: kind, Command: command, Source: file, Reason: reason})
		case "redis", "keyvalue":
			target.databases = append(target.databases, DetectedDatabase{Engine: "redis", Variable: "REDIS_URL", Evidence: "Key Value service in " + file})
		}
		for _, variable := range service.EnvVars {
			if ValidateEnvKey(variable.Key) != nil {
				continue
			}
			switch {
			case variable.GenerateValue:
				manifest.GeneratedVariables = appendUnique(manifest.GeneratedVariables, variable.Key)
			case variable.Sync != nil && !*variable.Sync:
				manifest.RequiredVariables = appendUnique(manifest.RequiredVariables, variable.Key)
			case variable.FromDatabase != nil:
				target.databases = append(target.databases, DetectedDatabase{Engine: "postgres", Variable: variable.Key, Evidence: "fromDatabase in " + file})
			case variable.FromService != nil:
				if engine := engineForAddon(fmt.Sprint(variable.FromService["type"])); engine != "" {
					target.databases = append(target.databases, DetectedDatabase{Engine: engine, Variable: variable.Key, Evidence: "fromService in " + file})
				}
			}
			example := ""
			if value, ok := variable.Value.(string); ok {
				example = value
			}
			if detected, ok := variableFrom(variable.Key, file, example); ok {
				target.variables = append(target.variables, detected)
			}
		}
	}
	if len(document.Databases) > 0 && len(order) > 0 {
		first := byRoot[order[0]]
		first.databases = append(first.databases, DetectedDatabase{Engine: "postgres", Variable: "DATABASE_URL", Evidence: "databases in " + file})
	}
	result := make([]platformTarget, 0, len(order))
	for _, root := range order {
		result = append(result, *byRoot[root])
	}
	return result
}

func railwayTarget(root, name string, content []byte) platformTarget {
	file := joinRoot(root, name)
	target := platformTarget{root: root, manifest: DetectedPlatformManifest{File: file, Platform: "railway"}}
	var build, start, health, dockerfile, release string
	if name == "railway.json" {
		var document struct {
			Build struct {
				BuildCommand   string `json:"buildCommand"`
				DockerfilePath string `json:"dockerfilePath"`
			} `json:"build"`
			Deploy struct {
				StartCommand     string          `json:"startCommand"`
				HealthcheckPath  string          `json:"healthcheckPath"`
				PreDeployCommand json.RawMessage `json:"preDeployCommand"`
			} `json:"deploy"`
		}
		if json.Unmarshal(manifestText(content), &document) != nil {
			return platformTarget{}
		}
		build, start, health, dockerfile = document.Build.BuildCommand, document.Deploy.StartCommand, document.Deploy.HealthcheckPath, document.Build.DockerfilePath
		var single string
		var many []string
		if json.Unmarshal(document.Deploy.PreDeployCommand, &single) == nil {
			release = single
		} else if json.Unmarshal(document.Deploy.PreDeployCommand, &many) == nil {
			release = strings.Join(many, " && ")
		}
	} else {
		entries := readTOML(content)
		build, start, health = tomlText(entries, "build", "buildCommand"), tomlText(entries, "deploy", "startCommand"), tomlText(entries, "deploy", "healthcheckPath")
		dockerfile = tomlText(entries, "build", "dockerfilePath")
		if value, ok := tomlLookup(entries, "deploy", "preDeployCommand"); ok {
			release = value.text
			if value.isList {
				release = strings.Join(value.list, " && ")
			}
		}
	}
	manifest := &target.manifest
	manifest.BuildCommand = cleanPlatformCommand(build, true)
	manifest.StartCommand = cleanPlatformCommand(start, false)
	manifest.HealthPath = health
	manifest.Dockerfile = strings.TrimPrefix(dockerfile, "./")
	if release = cleanPlatformCommand(release, false); release != "" {
		manifest.ReleaseCommand = release
		target.processes = append(target.processes, DetectedProcess{Name: "release", Kind: "release", Command: release,
			Source: file, Reason: "Railway runs this preDeployCommand before each deploy"})
	}
	return target
}

func herokuAppTarget(root string, content []byte) platformTarget {
	var document map[string]json.RawMessage
	if json.Unmarshal(manifestText(content), &document) != nil || document["expo"] != nil {
		return platformTarget{}
	}
	if document["env"] == nil && document["addons"] == nil && document["formation"] == nil && document["buildpacks"] == nil {
		return platformTarget{}
	}
	file := joinRoot(root, "app.json")
	target := platformTarget{root: root, manifest: DetectedPlatformManifest{File: file, Platform: "heroku"}}
	var environment map[string]json.RawMessage
	_ = json.Unmarshal(document["env"], &environment)
	names := make([]string, 0, len(environment))
	for name := range environment {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		var detail struct {
			Value     string `json:"value"`
			Generator string `json:"generator"`
			Required  *bool  `json:"required"`
		}
		var plain string
		if json.Unmarshal(environment[name], &plain) == nil {
			detail.Value = plain
		} else {
			_ = json.Unmarshal(environment[name], &detail)
		}
		if ValidateEnvKey(name) != nil {
			continue
		}
		switch {
		case detail.Generator == "secret":
			target.manifest.GeneratedVariables = appendUnique(target.manifest.GeneratedVariables, name)
		case detail.Required == nil || *detail.Required:
			if detail.Value == "" {
				target.manifest.RequiredVariables = appendUnique(target.manifest.RequiredVariables, name)
			}
		}
		if variable, ok := variableFrom(name, file, detail.Value); ok {
			target.variables = append(target.variables, variable)
		}
	}
	var addons []json.RawMessage
	_ = json.Unmarshal(document["addons"], &addons)
	for _, raw := range addons {
		var name string
		var detail struct {
			Plan string `json:"plan"`
		}
		if json.Unmarshal(raw, &name) != nil && json.Unmarshal(raw, &detail) == nil {
			name = detail.Plan
		}
		if engine := engineForAddon(name); engine != "" {
			variable := databaseVariableNames[engine]
			target.databases = append(target.databases, DetectedDatabase{Engine: engine, Variable: variable, Evidence: boundedText(name, 64) + " add-on in " + file})
		}
	}
	return target
}

func herokuYAMLTarget(root string, content []byte) platformTarget {
	var document struct {
		Run     map[string]any `yaml:"run"`
		Release struct {
			Command []string `yaml:"command"`
		} `yaml:"release"`
	}
	if yaml.Unmarshal(manifestText(content), &document) != nil {
		return platformTarget{}
	}
	file := joinRoot(root, "heroku.yml")
	target := platformTarget{root: root, manifest: DetectedPlatformManifest{File: file, Platform: "heroku"}}
	names := make([]string, 0, len(document.Run))
	for name := range document.Run {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		command := ""
		switch value := document.Run[name].(type) {
		case string:
			command = value
		case map[string]any:
			if list, ok := value["command"].([]any); ok {
				parts := []string{}
				for _, part := range list {
					parts = append(parts, fmt.Sprint(part))
				}
				command = strings.Join(parts, " ")
			}
		}
		command = cleanPlatformCommand(command, false)
		if name == "web" {
			target.manifest.StartCommand = command
		} else if validProcessName(name) {
			target.processes = append(target.processes, DetectedProcess{Name: name, Kind: processKind(name, command), Command: command,
				Source: file, Reason: "heroku.yml declares the " + name + " process"})
		}
	}
	if release := cleanPlatformCommand(strings.Join(document.Release.Command, " "), false); release != "" {
		target.manifest.ReleaseCommand = release
		target.processes = append(target.processes, DetectedProcess{Name: "release", Kind: "release", Command: release,
			Source: file, Reason: "heroku.yml runs this release command before each deploy"})
	}
	return target
}

func nixpacksTarget(root string, content []byte) platformTarget {
	entries := readTOML(content)
	file := joinRoot(root, "nixpacks.toml")
	target := platformTarget{root: root, manifest: DetectedPlatformManifest{File: file, Platform: "nixpacks"}}
	target.manifest.StartCommand = cleanPlatformCommand(tomlText(entries, "start", "cmd"), false)
	if value, ok := tomlLookup(entries, "phases.build", "cmds"); ok && value.isList {
		target.manifest.BuildCommand = cleanPlatformCommand(strings.Join(value.list, " && "), true)
	}
	if value, ok := tomlLookup(entries, "phases.setup", "aptPkgs"); ok {
		for _, name := range value.list {
			if aptPackageRE.MatchString(name) && len(target.manifest.SystemPackages) < 64 {
				target.manifest.SystemPackages = append(target.manifest.SystemPackages, name)
			}
		}
	}
	for _, entry := range tomlTable(entries, "variables") {
		if variable, ok := variableFrom(entry.key, file, entry.value.text); ok {
			target.variables = append(target.variables, variable)
		}
	}
	return target
}

func netlifyTarget(root string, content []byte) platformTarget {
	entries := readTOML(content)
	file := joinRoot(root, "netlify.toml")
	base, ok := platformPath(root, tomlText(entries, "build", "base"))
	if !ok {
		return platformTarget{}
	}
	target := platformTarget{root: base, manifest: DetectedPlatformManifest{File: file, Platform: "netlify"}}
	target.manifest.BuildCommand = cleanPlatformCommand(tomlText(entries, "build", "command"), true)
	if publish := strings.TrimSpace(tomlText(entries, "build", "publish")); publish != "" {
		publish = strings.TrimPrefix(publish, "./")
		if relative, ok := platformPath("", publish); ok && relative != "" {
			target.manifest.OutputDirectory = relative
		}
	}
	for _, entry := range tomlTable(entries, "build.environment") {
		switch entry.key {
		case "NODE_VERSION", "HUGO_VERSION", "PYTHON_VERSION", "RUBY_VERSION", "GO_VERSION":
			if value := strings.TrimSpace(entry.value.text); value != "" && len(value) <= 32 {
				target.manifest.Toolchains = append(target.manifest.Toolchains, strings.ToLower(strings.TrimSuffix(entry.key, "_VERSION"))+" "+value)
			}
		}
	}
	for _, redirect := range tomlArrayTables(entries, "redirects") {
		from, to, status := redirect["from"].text, redirect["to"].text, redirect["status"].text
		if from == "/*" && to == "/index.html" && (status == "200" || status == "") {
			target.manifest.SPAFallback = true
			continue
		}
		target.manifest.Redirects++
	}
	return target
}

func vercelTarget(root string, content []byte) platformTarget {
	var document struct {
		BuildCommand    string `json:"buildCommand"`
		OutputDirectory string `json:"outputDirectory"`
		Rewrites        []struct {
			Source      string `json:"source"`
			Destination string `json:"destination"`
		} `json:"rewrites"`
		Redirects []json.RawMessage          `json:"redirects"`
		Functions map[string]json.RawMessage `json:"functions"`
	}
	if json.Unmarshal(manifestText(content), &document) != nil {
		return platformTarget{}
	}
	target := platformTarget{root: root, manifest: DetectedPlatformManifest{File: joinRoot(root, "vercel.json"), Platform: "vercel"}}
	target.manifest.BuildCommand = cleanPlatformCommand(document.BuildCommand, true)
	if output, ok := platformPath("", document.OutputDirectory); ok && output != "" {
		target.manifest.OutputDirectory = output
	}
	for _, rewrite := range document.Rewrites {
		if rewrite.Destination == "/index.html" || rewrite.Destination == "/" {
			target.manifest.SPAFallback = true
		}
	}
	target.manifest.Redirects = len(document.Redirects)
	return target
}

func digitalOceanTargets(root string, content []byte) []platformTarget {
	type component struct {
		Name           string `yaml:"name"`
		SourceDir      string `yaml:"source_dir"`
		BuildCommand   string `yaml:"build_command"`
		RunCommand     string `yaml:"run_command"`
		HTTPPort       int    `yaml:"http_port"`
		OutputDir      string `yaml:"output_dir"`
		CatchallDoc    string `yaml:"catchall_document"`
		Kind           string `yaml:"kind"`
		DockerfilePath string `yaml:"dockerfile_path"`
		HealthCheck    struct {
			HTTPPath string `yaml:"http_path"`
		} `yaml:"health_check"`
		Envs []struct {
			Key   string `yaml:"key"`
			Value string `yaml:"value"`
			Type  string `yaml:"type"`
		} `yaml:"envs"`
	}
	var document struct {
		Services    []component `yaml:"services"`
		Workers     []component `yaml:"workers"`
		Jobs        []component `yaml:"jobs"`
		StaticSites []component `yaml:"static_sites"`
		Databases   []struct {
			Engine string `yaml:"engine"`
		} `yaml:"databases"`
	}
	if yaml.Unmarshal(manifestText(content), &document) != nil {
		return nil
	}
	file := joinRoot(root, ".do/app.yaml")
	byRoot := map[string]*platformTarget{}
	order := []string{}
	targetFor := func(sourceDir string) *platformTarget {
		componentRoot, ok := platformPath(root, sourceDir)
		if !ok {
			return nil
		}
		if byRoot[componentRoot] == nil {
			byRoot[componentRoot] = &platformTarget{root: componentRoot, manifest: DetectedPlatformManifest{File: file, Platform: "digitalocean"}}
			order = append(order, componentRoot)
		}
		return byRoot[componentRoot]
	}
	for _, service := range document.Services {
		target := targetFor(service.SourceDir)
		if target == nil || target.manifest.StartCommand != "" {
			continue
		}
		target.manifest.StartCommand = cleanPlatformCommand(service.RunCommand, false)
		target.manifest.BuildCommand = cleanPlatformCommand(service.BuildCommand, true)
		target.manifest.Port = service.HTTPPort
		target.manifest.HealthPath = service.HealthCheck.HTTPPath
		target.manifest.Dockerfile = strings.TrimPrefix(service.DockerfilePath, "./")
		for _, variable := range service.Envs {
			if strings.EqualFold(variable.Type, "SECRET") && ValidateEnvKey(variable.Key) == nil {
				target.manifest.RequiredVariables = appendUnique(target.manifest.RequiredVariables, variable.Key)
			}
			if detected, ok := variableFrom(variable.Key, file, variable.Value); ok {
				target.variables = append(target.variables, detected)
			}
		}
	}
	for _, site := range document.StaticSites {
		target := targetFor(site.SourceDir)
		if target == nil {
			continue
		}
		if output, ok := platformPath("", site.OutputDir); ok && output != "" {
			target.manifest.OutputDirectory = output
		}
		target.manifest.BuildCommand = cleanPlatformCommand(site.BuildCommand, true)
		target.manifest.SPAFallback = site.CatchallDoc == "index.html"
	}
	for _, worker := range document.Workers {
		target := targetFor(worker.SourceDir)
		name := worker.Name
		if target == nil || !validProcessName(name) {
			continue
		}
		command := cleanPlatformCommand(worker.RunCommand, false)
		target.processes = append(target.processes, DetectedProcess{Name: name, Kind: processKind(name, command), Command: command,
			Source: file, Reason: "the app spec declares the " + name + " worker"})
	}
	for _, job := range document.Jobs {
		target := targetFor(job.SourceDir)
		if target == nil || job.Kind != "PRE_DEPLOY" {
			continue
		}
		if release := cleanPlatformCommand(job.RunCommand, false); release != "" {
			target.manifest.ReleaseCommand = release
			target.processes = append(target.processes, DetectedProcess{Name: "release", Kind: "release", Command: release,
				Source: file, Reason: "the app spec runs this PRE_DEPLOY job before each deploy"})
		}
	}
	for _, database := range document.Databases {
		engine := map[string]string{"PG": "postgres", "MYSQL": "mysql", "REDIS": "redis", "VALKEY": "redis", "MONGODB": "mongodb"}[strings.ToUpper(database.Engine)]
		if engine != "" && len(order) > 0 {
			byRoot[order[0]].databases = append(byRoot[order[0]].databases, DetectedDatabase{Engine: engine, Variable: databaseVariableNames[engine], Evidence: "database in " + file})
		}
	}
	result := make([]platformTarget, 0, len(order))
	for _, componentRoot := range order {
		result = append(result, *byRoot[componentRoot])
	}
	return result
}

func kamalTarget(root string, content []byte) platformTarget {
	text := erbTagRE.ReplaceAllString(string(manifestText(content)), "")
	var document struct {
		Servers map[string]any `yaml:"servers"`
		Proxy   struct {
			AppPort     int `yaml:"app_port"`
			Healthcheck struct {
				Path string `yaml:"path"`
			} `yaml:"healthcheck"`
		} `yaml:"proxy"`
		Env struct {
			Secret []string       `yaml:"secret"`
			Clear  map[string]any `yaml:"clear"`
		} `yaml:"env"`
		Volumes     []string                  `yaml:"volumes"`
		Accessories map[string]map[string]any `yaml:"accessories"`
	}
	if yaml.Unmarshal([]byte(text), &document) != nil {
		return platformTarget{}
	}
	file := joinRoot(root, "config/deploy.yml")
	target := platformTarget{root: root, manifest: DetectedPlatformManifest{File: file, Platform: "kamal"}}
	manifest := &target.manifest
	manifest.Port = document.Proxy.AppPort
	manifest.HealthPath = document.Proxy.Healthcheck.Path
	for _, name := range document.Env.Secret {
		if ValidateEnvKey(name) == nil {
			manifest.RequiredVariables = appendUnique(manifest.RequiredVariables, name)
			if variable, ok := variableFrom(name, file, ""); ok {
				target.variables = append(target.variables, variable)
			}
		}
	}
	names := make([]string, 0, len(document.Env.Clear))
	for name := range document.Env.Clear {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		example := ""
		if kamalClearAllowlist[name] {
			example = fmt.Sprint(document.Env.Clear[name])
		}
		if variable, ok := variableFrom(name, file, example); ok {
			target.variables = append(target.variables, variable)
		}
	}
	for _, volume := range document.Volumes {
		if _, destination, ok := strings.Cut(volume, ":"); ok && strings.HasPrefix(destination, "/") && len(manifest.Volumes) < 32 {
			destination, _, _ = strings.Cut(destination, ":")
			manifest.Volumes = append(manifest.Volumes, destination)
		}
	}
	roles := make([]string, 0, len(document.Servers))
	for role := range document.Servers {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		settings, ok := document.Servers[role].(map[string]any)
		if role == "web" || !ok || !validProcessName(role) {
			continue
		}
		command, _ := settings["cmd"].(string)
		command = cleanPlatformCommand(command, false)
		target.processes = append(target.processes, DetectedProcess{Name: role, Kind: processKind(role, command), Command: command,
			Source: file, Reason: "Kamal runs the " + role + " role beside the web servers"})
	}
	accessories := make([]string, 0, len(document.Accessories))
	for name := range document.Accessories {
		accessories = append(accessories, name)
	}
	sort.Strings(accessories)
	for _, name := range accessories {
		image, _ := document.Accessories[name]["image"].(string)
		if engine := engineForAddon(image); engine != "" {
			target.databases = append(target.databases, DetectedDatabase{Engine: engine, Variable: databaseVariableNames[engine], Evidence: "Kamal accessory " + boundedText(image, 64) + " in " + file})
		}
	}
	return target
}

func aptfileTarget(root string, content []byte) platformTarget {
	target := platformTarget{root: root, manifest: DetectedPlatformManifest{File: joinRoot(root, "Aptfile"), Platform: "aptfile"}}
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if aptPackageRE.MatchString(line) && len(target.manifest.SystemPackages) < 64 {
			target.manifest.SystemPackages = append(target.manifest.SystemPackages, line)
		}
	}
	if len(target.manifest.SystemPackages) == 0 {
		return platformTarget{}
	}
	return target
}

func readTheDocsTarget(root, name string, content []byte) platformTarget {
	var document struct {
		Sphinx struct {
			Configuration string `yaml:"configuration"`
		} `yaml:"sphinx"`
		MkDocs struct {
			Configuration string `yaml:"configuration"`
		} `yaml:"mkdocs"`
	}
	if yaml.Unmarshal(manifestText(content), &document) != nil {
		return platformTarget{}
	}
	target := platformTarget{root: root, manifest: DetectedPlatformManifest{File: joinRoot(root, name), Platform: "readthedocs"}}
	for _, configuration := range []string{document.Sphinx.Configuration, document.MkDocs.Configuration} {
		if configuration != "" && len(configuration) <= 256 {
			target.manifest.Applied = append(target.manifest.Applied, "documentation built from "+configuration)
		}
	}
	return target
}

// huggingFaceTarget reads a Space's README front matter: which SDK serves it,
// the app file and the port a Docker Space listens on.
func huggingFaceTarget(root string, content []byte) platformTarget {
	text := string(content)
	if !strings.HasPrefix(text, "---") {
		return platformTarget{}
	}
	body, _, found := strings.Cut(strings.TrimPrefix(text, "---"), "\n---")
	if !found {
		return platformTarget{}
	}
	var front struct {
		SDK     string `yaml:"sdk"`
		AppFile string `yaml:"app_file"`
		AppPort int    `yaml:"app_port"`
	}
	if yaml.Unmarshal([]byte(body), &front) != nil || front.SDK == "" {
		return platformTarget{}
	}
	target := platformTarget{root: root, manifest: DetectedPlatformManifest{File: joinRoot(root, "README.md"), Platform: "huggingface"}}
	appFile := strings.TrimPrefix(front.AppFile, "./")
	if appFile != "" && !safeRelativePath(appFile) {
		appFile = ""
	}
	switch front.SDK {
	case "gradio":
		if appFile != "" {
			target.manifest.StartCommand = "python " + appFile
		}
		target.manifest.Port = 7860
	case "streamlit":
		if appFile != "" {
			target.manifest.StartCommand = "streamlit run " + appFile + " --server.port 8501 --server.address 0.0.0.0 --server.headless true"
		}
		target.manifest.Port = 8501
	case "docker":
		target.manifest.Port = front.AppPort
		if target.manifest.Port == 0 {
			target.manifest.Port = 7860
		}
	case "static":
		if appFile != "" && path.Dir(appFile) != "." {
			target.manifest.OutputDirectory = path.Dir(appFile)
		}
	}
	return target
}

func firebaseTarget(root string, content []byte) platformTarget {
	var document struct {
		Hosting json.RawMessage `json:"hosting"`
	}
	if json.Unmarshal(manifestText(content), &document) != nil || len(document.Hosting) == 0 {
		return platformTarget{}
	}
	var hosting struct {
		Public   string `json:"public"`
		Rewrites []struct {
			Destination string `json:"destination"`
		} `json:"rewrites"`
	}
	var list []json.RawMessage
	if json.Unmarshal(document.Hosting, &list) == nil && len(list) > 0 {
		_ = json.Unmarshal(list[0], &hosting)
	} else {
		_ = json.Unmarshal(document.Hosting, &hosting)
	}
	target := platformTarget{root: root, manifest: DetectedPlatformManifest{File: joinRoot(root, "firebase.json"), Platform: "firebase"}}
	if output, ok := platformPath("", hosting.Public); ok && output != "" {
		target.manifest.OutputDirectory = output
	}
	for _, rewrite := range hosting.Rewrites {
		if rewrite.Destination == "/index.html" {
			target.manifest.SPAFallback = true
		}
	}
	return target
}

func redirectsTarget(root, file string, content []byte) platformTarget {
	target := platformTarget{root: root, manifest: DetectedPlatformManifest{File: file, Platform: "netlify"}}
	for _, raw := range strings.Split(string(content), "\n") {
		fields := strings.Fields(raw)
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if fields[0] == "/*" && fields[1] == "/index.html" && (len(fields) == 2 || strings.HasPrefix(fields[2], "200")) {
			target.manifest.SPAFallback = true
			continue
		}
		target.manifest.Redirects++
	}
	return target
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	if len(values) >= 64 {
		return values
	}
	return append(values, value)
}

// startDecisionPhrases are the NeedsDecision texts a declared start command
// answers.
var startDecisionPhrases = []string{"start command", "for gunicorn", "for uvicorn", "script to run", "ASGI/WSGI", "serves HTTP", "entry file"}

// applyPlatformManifests hands each candidate what the platform files at, or
// pointing at, its root declare.
func (s *repoShapeScan) applyPlatformManifests(result *DetectionResult, context shapeContext) {
	roots := make([]string, 0, len(s.roots))
	for root := range s.roots {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	for _, root := range roots {
		// Past detection's deadline the files are left unread, and the
		// result says it stopped short.
		if context.ctx.Err() != nil {
			if !result.Truncated {
				result.Truncated, result.TruncatedReason = true, "time limit reached"
			}
			return
		}
		for _, target := range s.platformTargets(root) {
			for index := range result.Candidates {
				candidate := &result.Candidates[index]
				if candidate.Root != target.root || len(candidate.PlatformManifests) >= 12 {
					continue
				}
				applyPlatformTarget(candidate, target, context.markers[candidate.Root])
			}
		}
	}
}

func applyPlatformTarget(candidate *DetectedCandidate, target platformTarget, marker *detectedMarkers) {
	manifest := target.manifest
	platform := platformNames[manifest.Platform]
	evidence := func(reason string) {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: manifest.File, Reason: reason})
	}
	static := candidate.OutputDirectory != "" || candidate.BuildMethod == BuildStatic || candidate.Profile == ProfileStatic
	fromProcfile := marker != nil && procfileProcess(marker.procfile, "web") != "" && candidate.StartCommand == procfileProcess(marker.procfile, "web")
	startTaken := false
	switch {
	case static && candidate.BuildMethod == BuildRecipe && manifest.OutputDirectory != "" && manifest.OutputDirectory != candidate.OutputDirectory:
		candidate.OutputDirectory = manifest.OutputDirectory
		manifest.Applied = append(manifest.Applied, "output directory")
		evidence(platform + " publishes " + manifest.OutputDirectory)
	case !static && manifest.StartCommand != "" && !fromProcfile && candidate.NotDeployable == "" && candidate.BuildMethod == BuildRecipe:
		if reason := runnerConflict(manifest.StartCommand, candidate); reason != "" {
			evidence(platform + " start command not used: " + reason)
			break
		}
		candidate.StartCommand = withSchemaStep(candidate, manifest.StartCommand)
		startTaken = true
		manifest.Applied = append(manifest.Applied, "start command")
		evidence(platform + " start command: " + boundedEvidence(manifest.StartCommand))
		if candidate.Profile == ProfileWorker || candidate.Profile == ProfileService {
			if manifest.Port > 0 || manifest.HealthPath != "" {
				candidate.Profile = ProfileWeb
			}
		}
		kept := candidate.NeedsDecision[:0]
		for _, decision := range candidate.NeedsDecision {
			answered := false
			for _, phrase := range startDecisionPhrases {
				answered = answered || strings.Contains(decision, phrase)
			}
			if !answered {
				kept = append(kept, decision)
			}
		}
		candidate.NeedsDecision = kept
		if candidate.Confidence == ConfidenceLow && candidate.RecipeIssue == "" {
			candidate.Confidence = ConfidenceMedium
		}
	}
	if static && manifest.SPAFallback && !candidate.SPAFallback && candidate.BuildMethod != BuildDockerfile {
		candidate.SPAFallback = true
		manifest.Applied = append(manifest.Applied, "single-page fallback")
		evidence(platform + " rewrites every path to /index.html")
	}
	if manifest.BuildCommand != "" && candidate.BuildCommand == "" && candidate.BuildMethod == BuildRecipe && candidate.NotDeployable == "" {
		if reason := runnerConflict(manifest.BuildCommand, candidate); reason == "" {
			candidate.BuildCommand = manifest.BuildCommand
			manifest.Applied = append(manifest.Applied, "build command")
			evidence(platform + " build command: " + boundedEvidence(manifest.BuildCommand))
		}
	}
	if manifest.Port > 0 && manifest.Port != candidate.Port && !static &&
		(startTaken || candidate.Port == 0 || (candidate.BuildMethod == BuildDockerfile && candidate.Port == 0)) {
		candidate.Port = manifest.Port
		manifest.Applied = append(manifest.Applied, "port")
		evidence(fmt.Sprintf("%s listens on %d", platform, manifest.Port))
	}
	if manifest.HealthPath != "" {
		if strings.HasPrefix(manifest.HealthPath, "/") && len(manifest.HealthPath) <= 1024 && !strings.ContainsAny(manifest.HealthPath, " \t\r\n") {
			evidence(platform + " health check path " + boundedEvidence(manifest.HealthPath))
		} else {
			manifest.HealthPath = ""
		}
	}
	for _, name := range manifest.GeneratedVariables {
		evidence(name + " is generated (" + platform + ")")
	}
	for _, name := range manifest.RequiredVariables {
		evidence(name + " must be set (" + platform + ")")
	}
	for _, volume := range manifest.Volumes {
		evidence(platform + " keeps data in " + volume)
	}
	for _, variable := range target.variables {
		candidate.Variables = mergeDetectedVariable(candidate.Variables, variable)
	}
	for _, name := range append(append([]string(nil), manifest.GeneratedVariables...), manifest.RequiredVariables...) {
		if variable, ok := variableFrom(name, manifest.File, ""); ok {
			candidate.Variables = mergeDetectedVariable(candidate.Variables, variable)
		}
	}
	candidate.Databases = appendDatabases(candidate.Databases, target.databases...)
	candidate.Processes = appendProcesses(candidate.Processes, target.processes...)
	if len(manifest.Applied) > 16 {
		manifest.Applied = manifest.Applied[:16]
	}
	candidate.PlatformManifests = append(candidate.PlatformManifests, manifest)
}

// withSchemaStep keeps the detected schema step in front of a start command
// another platform's file declares, the way the package's own start gets it:
// the database this server creates is empty until the step runs, and that
// platform ran it somewhere this file does not say.
func withSchemaStep(candidate *DetectedCandidate, command string) string {
	tool := schemaToolByName(candidate.SchemaTool)
	if candidate.SchemaCommand == "" || candidate.SchemaInStart || tool == nil || tool.applied(command) {
		return command
	}
	runner := candidate.PackageManager
	if runner == "" {
		runner = "npm"
	}
	return nodeExecRunner(runner) + " " + candidate.SchemaCommand + " && " + command
}

// runnerConflict refuses a declared command that runs a JavaScript package
// manager other than the one the lockfile builds with: the recipe's image
// carries only that one.
func runnerConflict(command string, candidate *DetectedCandidate) string {
	if candidate.Recipe != "node" || candidate.PackageManager == "" {
		return ""
	}
	first, _, _ := strings.Cut(strings.TrimSpace(command), " ")
	implied := map[string]string{"npm": "npm", "npx": "npm", "yarn": "yarn", "pnpm": "pnpm", "pnpx": "pnpm", "bun": "bun", "bunx": "bun"}[first]
	if implied != "" && implied != candidate.PackageManager {
		return "it runs " + first + ", and the lockfile builds with " + candidate.PackageManager
	}
	return ""
}

func mergeDetectedVariable(variables []DetectedVariable, variable DetectedVariable) []DetectedVariable {
	for index := range variables {
		if variables[index].Name == variable.Name {
			if variables[index].Example == "" {
				variables[index].Example = variable.Example
			}
			if len(variables[index].Sources) < 8 {
				variables[index].Sources = appendUnique(variables[index].Sources, variable.Sources[0])
			}
			return variables
		}
	}
	if len(variables) >= 64 {
		return variables
	}
	return append(variables, variable)
}
