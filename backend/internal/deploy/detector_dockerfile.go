package deploy

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
)

// ImageBuildIssue is something detection proved, from the files alone, about
// how a candidate's image would build: a line the builder refuses, a COPY of
// a path the context lacks, a script that cannot execute. Preflight turns
// each into a finding, so the operator reads it before Deploy instead of in
// a failed build's log. The text names lines, paths and variables — never a
// value.
type ImageBuildIssue struct {
	Code     string            `json:"code"`
	Severity PreflightSeverity `json:"severity"`
	Line     int               `json:"line,omitempty"`
	Subject  string            `json:"subject,omitempty"`
	Detail   string            `json:"detail"`
}

func newImageBuildIssue(code string, severity PreflightSeverity, line int, subject, detail string) ImageBuildIssue {
	clean := func(value string, limit int) string {
		value = strings.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == 0 {
				return ' '
			}
			return r
		}, value)
		if len(value) > limit {
			value = value[:limit-3] + "..."
		}
		if rejectPlanSecretLiteral("build issue", value) != nil {
			return ""
		}
		return value
	}
	text := clean(detail, 400)
	if text == "" {
		text = "details withheld because they resemble credential material"
	}
	return ImageBuildIssue{Code: code, Severity: severity, Line: line, Subject: clean(subject, 256), Detail: text}
}

var (
	imageBuildIssueCodeRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	dockerfileStageNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)
)

func validateCandidateImageFacts(candidate DetectedCandidate) error {
	malformed := fmt.Errorf("%w: detected image build facts are malformed", ErrInvalidPlan)
	if (candidate.DockerfileRole != "" && candidate.DockerfileRole != DockerfileRoleProduction && candidate.DockerfileRole != DockerfileRoleDevelopment) ||
		(candidate.DockerfileTarget != "" && !dockerfileStageNameRE.MatchString(candidate.DockerfileTarget)) ||
		len(candidate.DockerfileArgs) > 64 || len(candidate.DockerfilePlatforms) > 16 || len(candidate.ImageBuildIssues) > 32 ||
		len(candidate.ReleaseCommand) > 1024 || strings.ContainsAny(candidate.ReleaseCommand, "\x00\r\n") ||
		rejectPlanSecretLiteral("release command", candidate.ReleaseCommand) != nil ||
		secretCommandFlagRE.MatchString(candidate.ReleaseCommand) {
		return malformed
	}
	for _, arg := range candidate.DockerfileArgs {
		if len(arg.Name) > 128 || !shellAssignmentRE.MatchString(arg.Name) {
			return malformed
		}
	}
	for _, platform := range candidate.DockerfilePlatforms {
		if !validPlatform(platform) {
			return malformed
		}
	}
	for _, issue := range candidate.ImageBuildIssues {
		if !imageBuildIssueCodeRE.MatchString(issue.Code) ||
			(issue.Severity != PreflightBlocked && issue.Severity != PreflightWarning) ||
			issue.Line < 0 || issue.Detail == "" || len(issue.Detail) > 400 || len(issue.Subject) > 256 ||
			strings.ContainsAny(issue.Detail+issue.Subject, "\x00\r\n") ||
			rejectPlanSecretLiteral("build issue", issue.Detail) != nil ||
			rejectPlanSecretLiteral("build issue", issue.Subject) != nil {
			return malformed
		}
	}
	return nil
}

func (c DetectedCandidate) blockingImageIssue() (ImageBuildIssue, bool) {
	for _, issue := range c.ImageBuildIssues {
		if issue.Severity == PreflightBlocked {
			return issue, true
		}
	}
	return ImageBuildIssue{}, false
}

const (
	DockerfileRoleProduction  = "production"
	DockerfileRoleDevelopment = "development"
)

// dockerfileFileName recognises a Dockerfile by name: Dockerfile and
// Containerfile, and the variants repositories keep beside them —
// Dockerfile.prod, Dockerfile-dev, api.Dockerfile, Containerfile.worker.
// Templates and ignore files that merely start the same way are not ones.
func dockerfileFileName(name string) bool {
	name = strings.ToLower(name)
	for _, suffix := range []string{
		".dockerignore", ".md", ".txt", ".j2", ".jinja", ".tmpl", ".tpl", ".template", ".erb", ".tt",
		".sample", ".example", ".orig", ".bak", ".swp", ".in", ".sh", ".yml", ".yaml", ".json",
	} {
		if strings.HasSuffix(name, suffix) {
			return false
		}
	}
	switch {
	case name == "dockerfile" || name == "containerfile":
		return true
	case strings.HasPrefix(name, "dockerfile.") || strings.HasPrefix(name, "dockerfile-") ||
		strings.HasPrefix(name, "containerfile.") || strings.HasPrefix(name, "containerfile-"):
		return true
	default:
		return strings.HasSuffix(name, ".dockerfile") || strings.HasSuffix(name, ".containerfile")
	}
}

// dockerfileOutsideApplication is a Dockerfile a repository keeps for its
// tooling rather than its application: a dev container, or a GitHub Action.
func dockerfileOutsideApplication(relative string) bool {
	for _, segment := range strings.Split(relative, "/") {
		if segment == ".devcontainer" || segment == ".github" {
			return true
		}
	}
	return false
}

var dockerfileNameTokenRE = regexp.MustCompile(`[._-]+`)

// dockerfileNameRole reads what a Dockerfile's name says it was written for.
func dockerfileNameRole(relative string) (role string, exact bool) {
	base := strings.ToLower(path.Base(relative))
	exact = base == "dockerfile" || base == "containerfile"
	for _, token := range dockerfileNameTokenRE.Split(base, -1) {
		switch token {
		case "dev", "develop", "development", "local", "test", "tests", "testing", "debug", "ci", "e2e":
			return DockerfileRoleDevelopment, exact
		case "prod", "production", "release", "deploy":
			role = DockerfileRoleProduction
		}
	}
	return role, exact
}

// detectionTree reads the checkout being detected, through os.Root so that a
// symlink in the repository cannot point a read outside it. Every read is a
// bounded read of data.
type detectionTree struct{ root *os.Root }

func openDetectionTree(root string) detectionTree {
	opened, err := os.OpenRoot(root)
	if err != nil {
		return detectionTree{}
	}
	return detectionTree{root: opened}
}

func (t detectionTree) close() {
	if t.root != nil {
		_ = t.root.Close()
	}
}

func (t detectionTree) lstat(relative string) (fs.FileInfo, bool) {
	if t.root == nil {
		return nil, false
	}
	relative = strings.TrimPrefix(path.Clean("/"+relative), "/")
	if relative == "" {
		relative = "."
	}
	info, err := t.root.Lstat(relative)
	return info, err == nil
}

func (t detectionTree) exists(relative string) bool {
	_, ok := t.lstat(relative)
	return ok
}

func (t detectionTree) regular(relative string) (fs.FileInfo, bool) {
	info, ok := t.lstat(relative)
	if !ok || !info.Mode().IsRegular() {
		return nil, false
	}
	return info, true
}

func (t detectionTree) read(relative string, limit int64) ([]byte, bool) {
	if _, ok := t.regular(relative); !ok {
		return nil, false
	}
	file, err := t.root.Open(strings.TrimPrefix(path.Clean("/"+relative), "/"))
	if err != nil {
		return nil, false
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(content)) > limit {
		return nil, false
	}
	return content, true
}

// entries lists a directory's names, bounded, for a glob in a COPY source.
func (t detectionTree) entries(relative string) []string {
	if t.root == nil {
		return nil
	}
	relative = strings.TrimPrefix(path.Clean("/"+relative), "/")
	if relative == "" {
		relative = "."
	}
	directory, err := t.root.Open(relative)
	if err != nil {
		return nil
	}
	defer directory.Close()
	entries, _ := directory.ReadDir(4096)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func (t detectionTree) copySourceExists(context string, source dockerfileCopySource) bool {
	if source.Path == "." {
		return true
	}
	if source.Path == ".." || strings.HasPrefix(source.Path, "../") {
		return false
	}
	full := path.Join(context, source.Path)
	if !source.Glob {
		return t.exists(full)
	}
	directory, pattern := path.Split(full)
	if strings.ContainsAny(directory, "*?[") {
		// A glob across directories is not something to be sure about.
		return true
	}
	for _, name := range t.entries(directory) {
		if matched, err := path.Match(pattern, name); err == nil && matched {
			return true
		}
	}
	return false
}

// detectedDockerfile is one Dockerfile the walk found, with its bytes.
type detectedDockerfile struct {
	path    string
	content []byte
}

// composeBuildReference is a Compose service that builds a Dockerfile, which
// is the repository saying exactly which context that file expects.
type composeBuildReference struct {
	Service     string
	ComposePath string
	Context     string
	Dockerfile  string
	Target      string
}

// chooseDockerfileContext picks the directory a Dockerfile is built from:
// the nearest one, from the file's own directory up to the repository root,
// that holds the most of the paths it copies. `docker/Dockerfile` written for
// `docker build -f docker/Dockerfile .` copies root files; building it from
// docker/ fails on its first COPY.
func chooseDockerfileContext(tree detectionTree, directory string, sources []dockerfileCopySource) string {
	best, bestPresent := directory, -1
	for current := directory; ; current = parentDirectory(current) {
		present := 0
		for _, source := range sources {
			if source.Path != "." && !source.Glob && tree.copySourceExists(current, source) {
				present++
			}
		}
		if present > bestPresent {
			best, bestPresent = current, present
		}
		if current == "" {
			break
		}
	}
	if bestPresent <= 0 {
		return directory
	}
	return best
}

func parentDirectory(directory string) string {
	parent := path.Dir(directory)
	if parent == "." || parent == "/" {
		return ""
	}
	return parent
}

func rootLabel(root string) string {
	if root == "" {
		return "."
	}
	return root
}

var nextStandaloneRE = regexp.MustCompile("['\"`]standalone['\"`]")

// nextStandaloneConfigured reads whether a Next.js app at the root asks for
// standalone output; ok is false when there is no Next.js app to ask.
func nextStandaloneConfigured(tree detectionTree, root string) (configured, ok bool) {
	for _, name := range []string{"next.config.js", "next.config.mjs", "next.config.ts", "next.config.cjs", "next.config.mts"} {
		if content, found := tree.read(path.Join(root, name), 64<<10); found {
			return nextStandaloneRE.Match(content), true
		}
	}
	if manifest, found := tree.read(path.Join(root, "package.json"), 512<<10); found && strings.Contains(string(manifest), `"next"`) {
		return false, true
	}
	return false, false
}

// dockerfileCandidate reads one of the repository's Dockerfiles into a
// candidate: its build context and stage, its port, and every problem the
// build would certainly hit that the files already show.
func dockerfileCandidate(tree detectionTree, dockerfile detectedDockerfile, reference *composeBuildReference) (DetectedCandidate, bool) {
	model := modelDockerfile(dockerfile.content)
	role, exact := dockerfileNameRole(dockerfile.path)
	directory := path.Dir(dockerfile.path)
	if directory == "." {
		directory = ""
	}
	evidence := []DetectionEvidence{{Path: dockerfile.path, Reason: "container build definition"}}

	target := model.preferredTarget()
	if reference != nil && reference.Target != "" {
		for _, stage := range model.stages {
			if stage.Name == strings.ToLower(reference.Target) {
				target = stage.Name
			}
		}
	}
	if target != "" {
		evidence = append(evidence, DetectionEvidence{Path: dockerfile.path, Reason: "builds stage " + target + "; the last stage is for development"})
		if reference != nil && reference.Target != "" {
			evidence[len(evidence)-1].Reason = "builds stage " + target + " as " + reference.ComposePath + " does"
		}
	}
	context := chooseDockerfileContext(tree, directory, model.copySources())
	if reference != nil {
		context = reference.Context
		evidence = append(evidence, DetectionEvidence{Path: reference.ComposePath,
			Reason: "service " + reference.Service + " builds it with context " + rootLabel(context)})
	} else if context != directory {
		evidence = append(evidence, DetectionEvidence{Path: dockerfile.path,
			Reason: "build context " + rootLabel(context) + ": the paths it copies are there"})
	}
	relative := strings.TrimPrefix(dockerfile.path, rootPrefix(context))
	if !safeRelativePath(relative) || (context != "" && !safeRelativePath(context)) {
		return DetectedCandidate{}, false
	}
	name := "Dockerfile in " + rootLabel(context)
	if !exact || context != directory {
		name = dockerfile.path + " in " + rootLabel(context)
	}
	if len(name) > 256 || rejectPlanSecretLiteral("detected label", name) != nil {
		return DetectedCandidate{}, false
	}
	candidate := DetectedCandidate{
		Name: name, Profile: ProfileWeb, Confidence: ConfidenceHigh,
		Dockerfile: relative, DockerfileRole: role, DockerfileTarget: target,
		DockerfileArgs: model.args(), DockerfilePlatforms: model.platforms(),
		NeedsDecision: []string{},
	}
	switch {
	case role == DockerfileRoleDevelopment:
		candidate.Confidence = ConfidenceLow
		evidence = append(evidence, DetectionEvidence{Path: dockerfile.path, Reason: "named for development; never chosen on its own"})
	case !exact && role != DockerfileRoleProduction:
		candidate.Confidence = ConfidenceMedium
	case role == DockerfileRoleProduction:
		evidence = append(evidence, DetectionEvidence{Path: dockerfile.path, Reason: "named for production"})
	}
	if role != DockerfileRoleDevelopment && strings.Contains(strings.ToLower(string(dockerfile.content)), "designed for production") {
		candidate.DockerfileRole = DockerfileRoleProduction
		evidence = append(evidence, DetectionEvidence{Path: dockerfile.path, Reason: "the file says it is designed for production"})
	}
	if port, reason := model.exposedPort(target); port > 0 {
		candidate.Port = port
		evidence = append(evidence, DetectionEvidence{Path: dockerfile.path, Reason: reason})
	}
	if len(candidate.DockerfileArgs) > 64 {
		candidate.DockerfileArgs = candidate.DockerfileArgs[:64]
	}
	candidate.Framework, candidate.ReleaseCommand = dockerfileFramework(tree, context, &evidence)
	candidate.ImageBuildIssues = dockerfileIssues(tree, model, dockerfile.content, dockerfile.path, context, target)
	if label, line := model.devServer(target); label != "" {
		candidate.Confidence = ConfidenceLow
		candidate.ImageBuildIssues = append(candidate.ImageBuildIssues, newImageBuildIssue("dockerfile_dev_server", PreflightWarning, line, dockerfile.path,
			fmt.Sprintf("line %d of %s starts %s", line, dockerfile.path, label)))
	}
	if len(candidate.ImageBuildIssues) > 32 {
		candidate.ImageBuildIssues = candidate.ImageBuildIssues[:32]
	}
	candidate.Evidence = evidence
	return newDetectedCandidate(context, BuildDockerfile, candidate), true
}

// dockerfileIssues is every certain build failure the Dockerfile and its
// context show, checked the way BuildKit would meet them.
func dockerfileIssues(tree detectionTree, model dockerfileModel, content []byte, dockerfilePath, context, target string) []ImageBuildIssue {
	issues := []ImageBuildIssue{}
	if credential, found := dockerfileCredentialIssue(content); found {
		issues = append(issues, newImageBuildIssue("dockerfile_refused", PreflightBlocked, credential.Line, credential.Name,
			dockerfilePath+" "+credential.String()))
	}
	ignoreFile := dockerfilePath + ".dockerignore"
	ignore, found := tree.read(ignoreFile, 256<<10)
	if !found {
		ignoreFile = path.Join(context, ".dockerignore")
		ignore, _ = tree.read(ignoreFile, 256<<10)
	}
	rules := parseDockerignore(ignore)
	reported := 0
	for _, source := range model.copySources() {
		if reported >= 4 {
			break
		}
		// BuildKit accepts a wildcard that matches nothing (the official
		// Next.js Dockerfile copies `yarn.lock*` beside `package-lock.json*`),
		// so only a literal path can be missing.
		if source.Glob {
			continue
		}
		if !tree.copySourceExists(context, source) {
			issues = append(issues, newImageBuildIssue("dockerfile_copy_source_missing", PreflightBlocked, source.Line, source.Path,
				fmt.Sprintf("line %d %s %s: not in the build context %s", source.Line, source.Keyword, source.Path, rootLabel(context))))
			reported++
			continue
		}
		if source.Path == "." {
			continue
		}
		if excluded, rule := dockerignoreExcludes(rules, source.Path); excluded {
			if info, ok := tree.lstat(path.Join(context, source.Path)); ok && info.IsDir() && dockerignoreReincludesUnder(rules, source.Path) {
				continue
			}
			issues = append(issues, newImageBuildIssue("dockerfile_copy_ignored", PreflightBlocked, source.Line, source.Path,
				fmt.Sprintf("line %d %s %s: excluded by %s rule %s", source.Line, source.Keyword, source.Path, ignoreFile, rule)))
			reported++
		}
	}
	for _, arg := range model.args() {
		if arg.UsedInFrom && !arg.HasDefault {
			issues = append(issues, newImageBuildIssue("dockerfile_arg_required", PreflightBlocked, 0, arg.Name,
				"FROM uses build argument "+arg.Name+", which has no default"))
		}
	}
	if line := model.sshMountLine(); line > 0 {
		issues = append(issues, newImageBuildIssue("dockerfile_ssh_mount", PreflightBlocked, line, "",
			fmt.Sprintf("line %d mounts an SSH agent (--mount=type=ssh)", line)))
	}
	if line := model.copiesNextStandalone(); line > 0 {
		if configured, known := nextStandaloneConfigured(tree, context); known && !configured {
			issues = append(issues, newImageBuildIssue("dockerfile_standalone_missing", PreflightBlocked, line, ".next/standalone",
				fmt.Sprintf("line %d copies .next/standalone, but next.config does not set output: 'standalone'", line)))
		}
	}
	issues = append(issues, dockerfileScriptIssues(tree, model, context, target)...)
	return issues
}

// dockerfileScriptIssues checks the files the image executes directly: a
// first line ending in CR runs `sh\r` (no such file or directory), and a
// file committed without its executable bit cannot be exec'd at all unless
// the Dockerfile chmods it.
func dockerfileScriptIssues(tree detectionTree, model dockerfileModel, context, target string) []ImageBuildIssue {
	issues := []ImageBuildIssue{}
	for _, script := range model.scripts(target) {
		for _, candidate := range script.Candidates {
			relative := path.Join(context, candidate)
			info, ok := tree.regular(relative)
			if !ok {
				continue
			}
			severity := PreflightWarning
			if script.Starts {
				severity = PreflightBlocked
			}
			if head, found := tree.read(relative, 64<<10); found && scriptHasCRLF(head) && !model.normalizesLineEndings(path.Base(candidate)) {
				issues = append(issues, newImageBuildIssue("script_crlf", severity, script.Line, relative,
					fmt.Sprintf("%s (run by %s on line %d) has Windows line endings", relative, script.Keyword, script.Line)))
			}
			if !script.Interpreted && !script.Chmodded && info.Mode()&0o111 == 0 {
				issues = append(issues, newImageBuildIssue("script_not_executable", PreflightBlocked, script.Line, relative,
					fmt.Sprintf("%s (run by %s on line %d) is committed without its executable bit", relative, script.Keyword, script.Line)))
			}
			break
		}
	}
	return issues
}

var nodePackageRunners = map[string]bool{"npm": true, "pnpm": true, "yarn": true, "bun": true}

func scriptHasCRLF(content []byte) bool {
	line, _, _ := strings.Cut(string(content), "\n")
	return strings.HasSuffix(line, "\r")
}

// startCommandScriptIssues checks a recipe's start command the same way: the
// recipe runs it through /bin/sh, so `./bin/start` must be executable and
// have Unix line endings.
func startCommandScriptIssues(tree detectionTree, root, command string) []ImageBuildIssue {
	issues := []ImageBuildIssue{}
	segments := strings.Split(command, "&&")
	scripts := map[string]string{}
	if manifest, ok := tree.read(joinRoot(root, "package.json"), 512<<10); ok {
		var parsed nodeManifest
		if parseNodeManifest(manifest, &parsed) {
			scripts = parsed.Scripts
		}
	}
	for index := 0; index < len(segments) && index < 16; index++ {
		words := shellWords(strings.TrimSpace(segments[index]), '\\')
		for len(words) > 0 && strings.Contains(words[0], "=") {
			words = words[1:]
		}
		if len(words) == 0 {
			continue
		}
		// `npm run start` runs the package's script through a shell, so the
		// file that script names is what has to be executable.
		if len(words) >= 2 && nodePackageRunners[words[0]] {
			name := words[len(words)-1]
			if script := scripts[name]; script != "" && (words[1] == "run" || words[1] == "start") {
				segments = append(segments, strings.Split(script, "&&")...)
				delete(scripts, name)
			}
			continue
		}
		executable, interpreted := words[0], false
		if dockerfileShells[executable] && len(words) > 1 {
			executable, interpreted = words[1], true
		}
		if !strings.HasPrefix(executable, "./") && !strings.HasPrefix(executable, "bin/") {
			continue
		}
		relative := path.Join(root, path.Clean(executable))
		info, ok := tree.regular(relative)
		if !ok {
			continue
		}
		if head, found := tree.read(relative, 64<<10); found && scriptHasCRLF(head) {
			issues = append(issues, newImageBuildIssue("script_crlf", PreflightBlocked, 0, relative,
				relative+" (the start command) has Windows line endings"))
		}
		if !interpreted && info.Mode()&0o111 == 0 {
			issues = append(issues, newImageBuildIssue("script_not_executable", PreflightBlocked, 0, relative,
				relative+" (the start command) is committed without its executable bit"))
		}
	}
	return issues
}

// dockerfileFramework names the framework whose generator wrote a
// Dockerfile, from the manifests beside it, and the release command such an
// application needs before each start.
func dockerfileFramework(tree detectionTree, context string, evidence *[]DetectionEvidence) (string, string) {
	if lock, ok := tree.read(path.Join(context, "Gemfile.lock"), 512<<10); ok && strings.Contains(string(lock), "\n    railties (") {
		*evidence = append(*evidence, DetectionEvidence{Path: joinRoot(context, "Gemfile.lock"), Reason: "Rails application"})
		return "rails", ""
	}
	if mix, ok := tree.read(path.Join(context, "mix.exs"), 256<<10); ok && strings.Contains(string(mix), ":phoenix") {
		*evidence = append(*evidence, DetectionEvidence{Path: joinRoot(context, "mix.exs"), Reason: "Phoenix application"})
		if _, found := tree.regular(path.Join(context, "rel/overlays/bin/migrate")); found {
			*evidence = append(*evidence, DetectionEvidence{Path: joinRoot(context, "rel/overlays/bin/migrate"),
				Reason: "the release migrates with bin/migrate before each start"})
			return "phoenix", "bin/migrate"
		}
		return "phoenix", ""
	}
	if manifest, ok := tree.read(path.Join(context, "Package.swift"), 256<<10); ok {
		if framework := swiftServerFramework(manifest); framework != "" {
			*evidence = append(*evidence, DetectionEvidence{Path: joinRoot(context, "Package.swift"), Reason: framework + " server package"})
			return framework, ""
		}
	}
	return "", ""
}

func swiftServerFramework(manifest []byte) string {
	text := strings.ToLower(string(manifest))
	switch {
	case strings.Contains(text, "vapor/vapor"):
		return "vapor"
	case strings.Contains(text, "hummingbird-project/hummingbird") || strings.Contains(text, "/hummingbird"):
		return "hummingbird"
	}
	return ""
}

// swiftCandidate is the named cause for a Swift server package with no
// Dockerfile: there is no automatic Swift recipe, and every Vapor and
// Hummingbird template ships the Dockerfile that builds one.
func swiftCandidate(root string, manifest []byte) (DetectedCandidate, bool) {
	framework := swiftServerFramework(manifest)
	if framework == "" {
		return DetectedCandidate{}, false
	}
	label := map[string]string{"vapor": "Vapor", "hummingbird": "Hummingbird"}[framework]
	candidate := DetectedCandidate{
		Name: label + " server in " + rootLabel(root), Profile: ProfileWeb, Confidence: ConfidenceLow,
		Framework: framework, Dockerfile: "Dockerfile", Port: 8080,
		Evidence:      []DetectionEvidence{{Path: joinRoot(root, "Package.swift"), Reason: label + " server package"}},
		NeedsDecision: []string{},
		ImageBuildIssues: []ImageBuildIssue{newImageBuildIssue("dockerfile_missing", PreflightBlocked, 0, joinRoot(root, "Dockerfile"),
			"Swift has no automatic recipe; commit the Dockerfile the "+label+" template generates (it serves on 8080 with --hostname 0.0.0.0)")},
	}
	return newDetectedCandidate(root, BuildDockerfile, candidate), true
}

// dockerfileMarkers is how the walk hands its Dockerfiles over: bounded per
// directory, the exact names first so a repository's plain Dockerfile is
// never crowded out by variants.
func appendDetectedDockerfile(marker *detectedMarkers, dockerfile detectedDockerfile) {
	if len(marker.dockerfiles) >= 8 {
		return
	}
	marker.dockerfiles = append(marker.dockerfiles, dockerfile)
	sort.SliceStable(marker.dockerfiles, func(i, j int) bool {
		_, exactI := dockerfileNameRole(marker.dockerfiles[i].path)
		_, exactJ := dockerfileNameRole(marker.dockerfiles[j].path)
		if exactI != exactJ {
			return exactI
		}
		return marker.dockerfiles[i].path < marker.dockerfiles[j].path
	})
}

// dockerfilesUnderSkippedBuild finds the Dockerfiles of a `build/` directory
// the walk otherwise skips as output: Go's project layout keeps its image
// definition in build/package/Dockerfile. Only names are listed here, two
// levels deep and bounded.
func dockerfilesUnderSkippedBuild(tree detectionTree, directory string) []string {
	found := []string{}
	var visit func(string, int)
	visit = func(current string, depth int) {
		for index, name := range tree.entries(current) {
			if index >= 256 || len(found) >= 8 {
				return
			}
			relative := path.Join(current, name)
			info, ok := tree.lstat(relative)
			if !ok {
				continue
			}
			switch {
			case info.Mode().IsRegular() && dockerfileFileName(name):
				found = append(found, relative)
			case info.IsDir() && depth < 2 && !strings.HasPrefix(name, "."):
				visit(relative, depth+1)
			}
		}
	}
	visit(directory, 1)
	sort.Strings(found)
	return found
}
