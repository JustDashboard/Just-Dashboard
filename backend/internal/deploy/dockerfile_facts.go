package deploy

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// dockerfileModel is a parsed Dockerfile grouped into its build stages. Every
// fact detection states about a repository's own Dockerfile is read from it,
// as data; nothing here resolves an image or runs a build.
type dockerfileModel struct {
	escape     byte
	globalArgs []dockerfileArgDeclaration
	stages     []dockerfileStage
}

type dockerfileStage struct {
	Name         string
	Base         string
	Platform     string
	Line         int
	Parent       int
	Instructions []dockerfileInstruction
}

type dockerfileArgDeclaration struct {
	dockerfileKeyValue
	Line int
}

// dockerfilePredefinedArgs are the arguments BuildKit supplies to every
// build, so an ARG naming one without a default still has a value.
var dockerfilePredefinedArgs = map[string]bool{
	"TARGETPLATFORM": true, "TARGETOS": true, "TARGETARCH": true, "TARGETVARIANT": true,
	"BUILDPLATFORM": true, "BUILDOS": true, "BUILDARCH": true, "BUILDVARIANT": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "FTP_PROXY": true, "NO_PROXY": true, "ALL_PROXY": true,
	"http_proxy": true, "https_proxy": true, "ftp_proxy": true, "no_proxy": true, "all_proxy": true,
}

func modelDockerfile(content []byte) dockerfileModel {
	parsed := parseDockerfile(content)
	model := dockerfileModel{escape: parsed.Escape}
	names := map[string]int{}
	for _, instruction := range parsed.Instructions {
		if instruction.Keyword == "FROM" {
			words := shellWords(instruction.Args, parsed.Escape)
			stage := dockerfileStage{Line: instruction.Line, Parent: -1}
			if len(words) > 0 {
				stage.Base = words[0]
			}
			if len(words) >= 3 && strings.EqualFold(words[1], "as") {
				stage.Name = strings.ToLower(words[2])
			}
			stage.Platform, _ = instruction.flag("platform")
			if parent, ok := names[strings.ToLower(stage.Base)]; ok {
				stage.Parent = parent
			}
			if stage.Name != "" {
				names[stage.Name] = len(model.stages)
			}
			model.stages = append(model.stages, stage)
			continue
		}
		if len(model.stages) == 0 {
			if instruction.Keyword == "ARG" {
				for _, pair := range dockerfileKeyValues(instruction, parsed.Escape) {
					model.globalArgs = append(model.globalArgs, dockerfileArgDeclaration{pair, instruction.Line})
				}
			}
			continue
		}
		last := &model.stages[len(model.stages)-1]
		last.Instructions = append(last.Instructions, instruction)
	}
	return model
}

// finalStage is the stage the image is built from: the named target when
// one is chosen, otherwise the last one.
func (m dockerfileModel) finalStage(target string) int {
	if target != "" {
		for index, stage := range m.stages {
			if stage.Name == strings.ToLower(target) {
				return index
			}
		}
	}
	return len(m.stages) - 1
}

// lineage is a stage and the stages it was built FROM, nearest first, so a
// setting a parent stage made (WORKDIR, ENV, EXPOSE, CMD) reads as inherited.
func (m dockerfileModel) lineage(stage int) []int {
	result := []int{}
	seen := map[int]bool{}
	for stage >= 0 && !seen[stage] {
		seen[stage] = true
		result = append(result, stage)
		stage = m.stages[stage].Parent
	}
	return result
}

// preferredTarget chooses the stage to build when the file's last stage is
// a development one: a multi-stage file that ends in `dev` is written for
// `docker build --target production`, and building it whole deploys a dev
// server.
func (m dockerfileModel) preferredTarget() string {
	if len(m.stages) < 2 {
		return ""
	}
	switch m.stages[len(m.stages)-1].Name {
	case "dev", "development", "develop", "test", "debug":
	default:
		return ""
	}
	for _, preferred := range []string{"production", "prod", "release", "runner", "runtime"} {
		for _, stage := range m.stages[:len(m.stages)-1] {
			if stage.Name == preferred {
				return preferred
			}
		}
	}
	return ""
}

// variables are the values an instruction of a stage can expand: its own
// ARG defaults (a global one only once redeclared), and the ENV values of
// the stage and those it inherits.
func (m dockerfileModel) variables(stage int) map[string]string {
	global := map[string]string{}
	for _, arg := range m.globalArgs {
		if arg.HasValue {
			global[arg.Name] = arg.Value
		}
	}
	values := map[string]string{}
	lineage := m.lineage(stage)
	for index := len(lineage) - 1; index >= 0; index-- {
		for _, instruction := range m.stages[lineage[index]].Instructions {
			switch instruction.Keyword {
			case "ARG":
				for _, pair := range dockerfileKeyValues(instruction, m.escape) {
					if pair.HasValue {
						values[pair.Name] = expandDockerfileWord(pair.Value, values)
					} else if value, ok := global[pair.Name]; ok {
						values[pair.Name] = value
					}
				}
			case "ENV":
				for _, pair := range dockerfileKeyValues(instruction, m.escape) {
					values[pair.Name] = expandDockerfileWord(pair.Value, values)
				}
			}
		}
	}
	return values
}

var dockerfileExpansionRE = regexp.MustCompile(`\$(?:\{([A-Za-z_][A-Za-z0-9_]*)(?:(:?[-+])([^}]*))?\}|([A-Za-z_][A-Za-z0-9_]*))`)

// expandDockerfileWord substitutes $X, ${X}, ${X:-d} and ${X:+a} the way
// BuildKit does for the values a Dockerfile itself declares. A name with no
// known value is left as written, so a caller can tell it was unresolved.
func expandDockerfileWord(word string, values map[string]string) string {
	return dockerfileExpansionRE.ReplaceAllStringFunc(word, func(match string) string {
		groups := dockerfileExpansionRE.FindStringSubmatch(match)
		name := groups[1]
		if name == "" {
			name = groups[4]
		}
		value, ok := values[name]
		switch groups[2] {
		case ":-":
			if !ok || value == "" {
				return groups[3]
			}
		case "-":
			if !ok {
				return groups[3]
			}
		case ":+":
			if ok && value != "" {
				return groups[3]
			}
			return ""
		case "+":
			if ok {
				return groups[3]
			}
			return ""
		}
		if !ok {
			return match
		}
		return value
	})
}

// dockerfileAuxiliaryPorts are debugger and metrics listeners a Dockerfile
// often exposes next to the one it serves on.
var dockerfileAuxiliaryPorts = map[int]bool{9229: true, 5005: true, 9464: true, 9090: true}

// exposedPort is the one TCP port the built stage exposes, resolving
// `EXPOSE $PORT` through the stage's own ARG and ENV defaults and setting
// aside debugger and metrics ports. Anything else leaves the port unset.
func (m dockerfileModel) exposedPort(target string) (int, string) {
	if len(m.stages) == 0 {
		return 0, ""
	}
	stage := m.finalStage(target)
	values := m.variables(stage)
	ports := map[int]string{}
	lineage := m.lineage(stage)
	for index := len(lineage) - 1; index >= 0; index-- {
		for _, instruction := range m.stages[lineage[index]].Instructions {
			if instruction.Keyword != "EXPOSE" {
				continue
			}
			for _, raw := range shellWords(instruction.Args, m.escape) {
				if strings.HasPrefix(raw, "#") {
					break
				}
				expanded := expandDockerfileWord(raw, values)
				portText, protocol, _ := strings.Cut(expanded, "/")
				port, err := strconv.Atoi(portText)
				if err != nil || port < 1 || port > 65535 {
					return 0, ""
				}
				if protocol != "" && !strings.EqualFold(protocol, "tcp") {
					continue
				}
				reason := fmt.Sprintf("EXPOSE %d/tcp", port)
				if expanded != raw {
					name := strings.Trim(strings.TrimPrefix(raw, "$"), "{}")
					name, _, _ = strings.Cut(name, ":")
					reason = fmt.Sprintf("EXPOSE %s = %d from the Dockerfile's default for %s", raw, port, name)
				}
				ports[port] = reason
			}
		}
	}
	if len(ports) > 1 {
		for port := range ports {
			if dockerfileAuxiliaryPorts[port] {
				delete(ports, port)
			}
		}
	}
	if len(ports) != 1 {
		return 0, ""
	}
	for port, reason := range ports {
		return port, reason
	}
	return 0, ""
}

// DockerfileArg is a build argument a Dockerfile declares, by name only: its
// default is repository content that may be anything, and a value never
// leaves the file.
type DockerfileArg struct {
	Name       string `json:"name"`
	HasDefault bool   `json:"hasDefault,omitempty"`
	// UsedInFrom says a FROM line names it, so without a value the build
	// cannot even resolve its base image.
	UsedInFrom bool `json:"usedInFrom,omitempty"`
	// Consumed says an instruction after the declaration reads it, so an
	// unset value reaches a command, an ENV or a copied path empty.
	Consumed bool `json:"consumed,omitempty"`
}

func (m dockerfileModel) args() []DockerfileArg {
	byName := map[string]*DockerfileArg{}
	order := []string{}
	declare := func(name string, hasDefault bool) *DockerfileArg {
		arg := byName[name]
		if arg == nil {
			arg = &DockerfileArg{Name: name}
			byName[name] = arg
			order = append(order, name)
		}
		arg.HasDefault = arg.HasDefault || hasDefault
		return arg
	}
	for _, global := range m.globalArgs {
		if !shellAssignmentRE.MatchString(global.Name) || dockerfilePredefinedArgs[global.Name] {
			continue
		}
		arg := declare(global.Name, global.HasValue)
		for _, stage := range m.stages {
			if dockerfileReferences(stage.Base+" "+stage.Platform, global.Name) {
				arg.UsedInFrom = true
			}
		}
	}
	for _, stage := range m.stages {
		for index, instruction := range stage.Instructions {
			if instruction.Keyword != "ARG" {
				continue
			}
			for _, pair := range dockerfileKeyValues(instruction, m.escape) {
				if !shellAssignmentRE.MatchString(pair.Name) || dockerfilePredefinedArgs[pair.Name] {
					continue
				}
				inherited := false
				for _, global := range m.globalArgs {
					inherited = inherited || (global.Name == pair.Name && global.HasValue)
				}
				arg := declare(pair.Name, pair.HasValue || inherited)
				for _, later := range stage.Instructions[index+1:] {
					if dockerfileReferences(later.Args, pair.Name) {
						arg.Consumed = true
					}
				}
			}
		}
	}
	result := make([]DockerfileArg, 0, len(order))
	for _, name := range order {
		result = append(result, *byName[name])
	}
	return result
}

func dockerfileReferences(text, name string) bool {
	for _, match := range dockerfileExpansionRE.FindAllStringSubmatch(text, -1) {
		if match[1] == name || match[4] == name {
			return true
		}
	}
	return false
}

// platforms are the literal `FROM --platform=` values: a Dockerfile written
// on one architecture for another pins its stages this way.
func (m dockerfileModel) platforms() []string {
	result := []string{}
	for _, stage := range m.stages {
		platform := strings.ToLower(strings.TrimSpace(stage.Platform))
		if platform == "" || strings.Contains(platform, "$") || !validPlatform(platform) {
			continue
		}
		result = append(result, platform)
	}
	return uniqueSorted(result)
}

type dockerfileCopySource struct {
	Line    int
	Keyword string
	Path    string
	Glob    bool
}

// copySources are the build-context paths COPY and ADD read: not a --from
// stage or image, not a heredoc, not a URL, and not a path only a build
// argument decides.
func (m dockerfileModel) copySources() []dockerfileCopySource {
	result := []dockerfileCopySource{}
	for _, stage := range m.stages {
		for _, instruction := range stage.Instructions {
			if instruction.Keyword != "COPY" && instruction.Keyword != "ADD" {
				continue
			}
			if _, fromStage := instruction.flag("from"); fromStage || len(instruction.Heredocs) > 0 {
				continue
			}
			words := instruction.commandWords(m.escape)
			if len(words) < 2 {
				continue
			}
			for _, source := range words[:len(words)-1] {
				lower := strings.ToLower(source)
				if strings.Contains(source, "$") || strings.Contains(lower, "://") || strings.HasPrefix(lower, "git@") {
					continue
				}
				cleaned := path.Clean("/" + source)[1:]
				if strings.HasPrefix(source, "../") || source == ".." {
					cleaned = source
				}
				if cleaned == "" {
					cleaned = "."
				}
				result = append(result, dockerfileCopySource{
					Line: instruction.Line, Keyword: instruction.Keyword, Path: cleaned,
					Glob: strings.ContainsAny(cleaned, "*?["),
				})
			}
		}
	}
	return result
}

// sshMountLine is the first RUN that mounts an SSH agent, which a
// deployment build never forwards.
func (m dockerfileModel) sshMountLine() int {
	for _, stage := range m.stages {
		for _, instruction := range stage.Instructions {
			if instruction.Keyword != "RUN" {
				continue
			}
			for _, flag := range instruction.Flags {
				if strings.HasPrefix(flag, "--mount=") && strings.Contains(strings.ToLower(flag), "type=ssh") {
					return instruction.Line
				}
			}
		}
	}
	return 0
}

// copiesNextStandalone is the official Next.js Dockerfile's dependency on
// `output: 'standalone'`: without it the directory it copies never exists.
func (m dockerfileModel) copiesNextStandalone() int {
	for _, stage := range m.stages {
		for _, instruction := range stage.Instructions {
			if (instruction.Keyword == "COPY" || instruction.Keyword == "ADD") && strings.Contains(instruction.Args, ".next/standalone") {
				return instruction.Line
			}
		}
	}
	return 0
}

// command is the process the built image starts: the nearest ENTRYPOINT and
// CMD along the stage's lineage, as words.
func (m dockerfileModel) command(target string) (entrypoint, cmd []string, line int) {
	if len(m.stages) == 0 {
		return nil, nil, 0
	}
	stage := m.finalStage(target)
	lineage := m.lineage(stage)
	haveEntry, haveCmd := false, false
	for _, index := range lineage {
		instructions := m.stages[index].Instructions
		for position := len(instructions) - 1; position >= 0; position-- {
			instruction := instructions[position]
			switch {
			case instruction.Keyword == "ENTRYPOINT" && !haveEntry:
				entrypoint, haveEntry = instruction.commandWords(m.escape), true
				if line == 0 {
					line = instruction.Line
				}
			case instruction.Keyword == "CMD" && !haveCmd:
				cmd, haveCmd = instruction.commandWords(m.escape), true
				if line == 0 {
					line = instruction.Line
				}
			}
		}
	}
	return entrypoint, cmd, line
}

var dockerfileDevServerPatterns = []struct {
	re    *regexp.Regexp
	label string
}{
	{regexp.MustCompile(`\b(?:npm|pnpm|yarn|bun)\s+(?:run\s+)?(?:dev|start:dev|develop|serve:dev)\b`), "a package's dev script"},
	{regexp.MustCompile(`\b(?:next|nuxt|nuxi|astro|remix|vite)\s+dev\b`), "a framework dev server"},
	{regexp.MustCompile(`(?:^|\s)(?:npx\s+)?vite(?:\s+--|\s*$)`), "the Vite dev server"},
	{regexp.MustCompile(`\b(?:nodemon|ts-node-dev|tsx\s+watch|air)\b`), "a file watcher"},
	{regexp.MustCompile(`\bng\s+serve\b`), "the Angular dev server"},
	{regexp.MustCompile(`\bmanage\.py\s+runserver\b`), "Django's runserver"},
	{regexp.MustCompile(`\bflask\s+run\b`), "flask run"},
	{regexp.MustCompile(`\bfastapi\s+dev\b`), "fastapi dev"},
	{regexp.MustCompile(`--reload\b`), "auto-reload"},
	{regexp.MustCompile(`\bartisan\s+serve\b`), "php artisan serve"},
}

// devServer names the development server the built image would start, if
// its command is one: a dev server deployed is slow, unoptimised and often
// bound to localhost.
func (m dockerfileModel) devServer(target string) (string, int) {
	entrypoint, cmd, line := m.command(target)
	text := strings.Join(append(append([]string{}, entrypoint...), cmd...), " ")
	for _, pattern := range dockerfileDevServerPatterns {
		if pattern.re.MatchString(text) {
			return pattern.label, line
		}
	}
	return "", 0
}

// dockerfileScript is a file in the image a build step or the container's
// start executes directly, with where it would come from in the context.
type dockerfileScript struct {
	Line      int
	Keyword   string
	Container string
	// Starts says the container's own ENTRYPOINT or CMD runs it, so a broken
	// one stops every start rather than one build step.
	Starts bool
	// Interpreted says a shell is handed the file (`sh ./start.sh`), so it
	// needs no executable bit — but a CR at each line end still breaks it.
	Interpreted bool
	Chmodded    bool
	Candidates  []string
}

// scripts are the files the image executes by path — an exec-form
// ENTRYPOINT/CMD, a `RUN ./x` — mapped back to the context paths that would
// supply them: through the stage's WORKDIR when `COPY . .` puts the tree
// there, or through a COPY whose source has the same file name.
func (m dockerfileModel) scripts(target string) []dockerfileScript {
	if len(m.stages) == 0 {
		return nil
	}
	chmodded := map[string]bool{}
	copies := map[string][]string{}
	workdirs := map[int]string{}
	for index, stage := range m.stages {
		workdir := "/"
		if stage.Parent >= 0 {
			workdir = workdirs[stage.Parent]
		}
		for _, instruction := range stage.Instructions {
			switch instruction.Keyword {
			case "WORKDIR":
				target := expandDockerfileWord(strings.TrimSpace(instruction.Args), m.variables(index))
				if strings.HasPrefix(target, "/") {
					workdir = path.Clean(target)
				} else {
					workdir = path.Join(workdir, target)
				}
			case "RUN":
				words := instruction.commandWords(m.escape)
				for position, word := range words {
					if word == "chmod" {
						for _, operand := range words[position+1:] {
							if operand == "&&" || operand == ";" {
								break
							}
							chmodded[path.Base(operand)] = true
						}
					}
				}
			case "COPY", "ADD":
				if value, ok := instruction.flag("chmod"); ok && value != "" {
					for _, word := range instruction.commandWords(m.escape) {
						chmodded[path.Base(word)] = true
					}
				}
				if _, fromStage := instruction.flag("from"); fromStage {
					continue
				}
				words := instruction.commandWords(m.escape)
				for _, source := range words[:max(len(words)-1, 0)] {
					copies[path.Base(source)] = append(copies[path.Base(source)], path.Clean("/" + source)[1:])
				}
			}
		}
		workdirs[index] = workdir
	}
	stage := m.finalStage(target)
	resolve := func(executable, workdir string) []string {
		switch {
		case strings.HasPrefix(executable, "./") || (!strings.HasPrefix(executable, "/") && strings.Contains(executable, "/")):
			return []string{path.Clean(executable)}
		case strings.HasPrefix(executable, "/"):
			result := copies[path.Base(executable)]
			if workdir != "/" && strings.HasPrefix(executable, workdir+"/") {
				result = append([]string{strings.TrimPrefix(executable, workdir+"/")}, result...)
			}
			return result
		default:
			return copies[executable]
		}
	}
	scriptable := func(executable string) bool {
		if executable == "" || strings.ContainsAny(executable, "$`=") {
			return false
		}
		base := path.Base(executable)
		return strings.HasPrefix(executable, "./") || strings.Contains(executable, "/") ||
			strings.HasSuffix(base, ".sh") || strings.Contains(base, "entrypoint")
	}
	result := []dockerfileScript{}
	entrypoint, cmd, startLine := m.command(target)
	start, keyword := entrypoint, "ENTRYPOINT"
	if len(start) == 0 {
		start, keyword = cmd, "CMD"
	}
	if script, ok := dockerfileExecutedScript(start, scriptable); ok {
		script.Line, script.Keyword, script.Starts = startLine, keyword, true
		script.Chmodded = chmodded[path.Base(script.Container)]
		script.Candidates = resolve(script.Container, workdirs[stage])
		result = append(result, script)
	}
	for index, stage := range m.stages {
		for _, instruction := range stage.Instructions {
			if instruction.Keyword != "RUN" {
				continue
			}
			words := instruction.commandWords(m.escape)
			for len(words) > 0 && strings.Contains(words[0], "=") && !strings.HasPrefix(words[0], "-") {
				words = words[1:]
			}
			if len(words) == 0 || !strings.HasPrefix(words[0], "./") && !(dockerfileShells[words[0]] && len(words) > 1 && strings.HasPrefix(words[1], "./")) {
				continue
			}
			script, ok := dockerfileExecutedScript(words, scriptable)
			if !ok {
				continue
			}
			script.Line, script.Keyword = instruction.Line, "RUN"
			script.Chmodded = chmodded[path.Base(script.Container)]
			script.Candidates = resolve(script.Container, workdirs[index])
			result = append(result, script)
		}
	}
	return result
}

var dockerfileShells = map[string]bool{
	"sh": true, "bash": true, "ash": true, "dash": true, "zsh": true,
	"/bin/sh": true, "/bin/bash": true, "/bin/ash": true, "/bin/dash": true, "/usr/bin/env": false,
}

// dockerfileExecutedScript reads which file a command executes: its first
// word, or the file a shell is handed as its first operand.
func dockerfileExecutedScript(words []string, scriptable func(string) bool) (dockerfileScript, bool) {
	if len(words) == 0 {
		return dockerfileScript{}, false
	}
	if dockerfileShells[words[0]] {
		if len(words) < 2 || strings.HasPrefix(words[1], "-") || !scriptable(words[1]) {
			return dockerfileScript{}, false
		}
		return dockerfileScript{Container: words[1], Interpreted: true}, true
	}
	if !scriptable(words[0]) {
		return dockerfileScript{}, false
	}
	return dockerfileScript{Container: words[0]}, true
}

// normalizesLineEndings says a RUN step strips carriage returns from a file
// itself (dos2unix, or sed on \r), which makes a CRLF checkout harmless.
func (m dockerfileModel) normalizesLineEndings(base string) bool {
	for _, stage := range m.stages {
		for _, instruction := range stage.Instructions {
			if instruction.Keyword == "RUN" && strings.Contains(instruction.Args, base) &&
				(strings.Contains(instruction.Args, "dos2unix") || strings.Contains(instruction.Args, `\r`)) {
				return true
			}
		}
	}
	return false
}
