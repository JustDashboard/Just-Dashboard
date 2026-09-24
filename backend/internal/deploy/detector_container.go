package deploy

import (
	"bytes"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// addSkippedBuildDockerfiles gives the walk's skipped build/ directories
// their Dockerfiles back, as markers of their own directories.
func addSkippedBuildDockerfiles(tree detectionTree, markers map[string]*detectedMarkers, directories []string, limits DetectionLimits, result *DetectionResult) {
	for _, directory := range directories {
		for _, relative := range dockerfilesUnderSkippedBuild(tree, directory) {
			if dockerfileOutsideApplication(relative) {
				continue
			}
			content, ok := tree.read(relative, limits.MaxFileBytes)
			if !ok || result.ScannedBytes+int64(len(content)) > limits.MaxReadBytes {
				continue
			}
			result.ScannedBytes += int64(len(content))
			parent := path.Dir(relative)
			key := filepath.FromSlash(parent)
			marker := markers[key]
			if marker == nil {
				marker = &detectedMarkers{root: key, pythonFiles: map[string][]byte{}, csprojs: map[string][]byte{}}
				markers[key] = marker
			}
			appendDetectedDockerfile(marker, detectedDockerfile{path: relative, content: content})
		}
	}
}

// attachBuiltDockerfiles hands the per-root passes the Dockerfile each of
// this root's container candidates builds. A Dockerfile's context can sit
// above its own directory, so the file is looked up in the directory it was
// found in, and it is read only through the stage the candidate targets:
// the stages after it are never built, and every reader of "the final
// stage" then reads the one the image comes from.
func (m *detectedMarkers) attachBuiltDockerfiles(markers map[string]*detectedMarkers, candidates []DetectedCandidate) {
	m.builtDockerfiles = map[string]detectedDockerfile{}
	for _, candidate := range candidates {
		if candidate.BuildMethod != BuildDockerfile || candidate.Dockerfile == "" {
			continue
		}
		file := joinRoot(candidate.Root, candidate.Dockerfile)
		directory := path.Dir(file)
		if directory == "." {
			directory = ""
		}
		owner := markers[filepath.FromSlash(directory)]
		if owner == nil {
			continue
		}
		for _, dockerfile := range owner.dockerfiles {
			if dockerfile.path == file {
				m.builtDockerfiles[candidate.ID] = detectedDockerfile{
					path: file, content: dockerfileThroughTarget(dockerfile.content, candidate.DockerfileTarget),
				}
			}
		}
	}
}

// dockerfileFor is the Dockerfile a container candidate at this root builds;
// one this detection did not read is empty.
func (m *detectedMarkers) dockerfileFor(candidate *DetectedCandidate) detectedDockerfile {
	return m.builtDockerfiles[candidate.ID]
}

// dockerfileThroughTarget is a Dockerfile up to the end of the stage a build
// targets.
func dockerfileThroughTarget(content []byte, target string) []byte {
	if target == "" {
		return content
	}
	model := modelDockerfile(content)
	stage := model.finalStage(target)
	if stage < 0 || stage+1 >= len(model.stages) {
		return content
	}
	lines := bytes.SplitAfter(content, []byte("\n"))
	next := model.stages[stage+1].Line - 1
	if next <= 0 || next > len(lines) {
		return content
	}
	return bytes.Join(lines[:next], nil)
}

// containerCandidates are the candidates the repository's own container
// definitions make: one per Dockerfile, built from the context its COPY
// lines or a Compose service name, and one per directory of Compose files
// that is a deployment rather than a development stack.
func containerCandidates(tree detectionTree, markers map[string]*detectedMarkers, roots []string) (candidates, backingOnly []DetectedCandidate) {
	references := map[string]*composeBuildReference{}
	for _, root := range roots {
		marker := markers[root]
		sort.Strings(marker.compose)
		marker.composeFiles = nil
		for _, composePath := range marker.compose {
			detection, ok := readComposeForDetection(tree, composePath)
			if !ok {
				continue
			}
			marker.composeFiles = append(marker.composeFiles, detection)
			for _, service := range detection.services {
				if !service.Builds || service.Dockerfile == "" {
					continue
				}
				dockerfile := path.Clean(path.Join(service.Context, service.Dockerfile))
				if references[dockerfile] == nil && safeRelativePath(dockerfile) {
					references[dockerfile] = &composeBuildReference{
						Service: service.Name, ComposePath: composePath, Context: service.Context,
						Dockerfile: dockerfile, Target: service.Target,
					}
				}
			}
		}
	}
	result := []DetectedCandidate{}
	for _, root := range roots {
		marker := markers[root]
		for _, dockerfile := range marker.dockerfiles {
			if candidate, ok := dockerfileCandidate(tree, dockerfile, references[dockerfile.path]); ok {
				result = append(result, candidate)
			}
		}
		if len(marker.packageSwift) > 0 && len(marker.dockerfiles) == 0 {
			if candidate, ok := swiftCandidate(filepath.ToSlash(marker.root), marker.packageSwift); ok {
				result = append(result, candidate)
			}
		}
		if len(marker.compose) > 0 {
			if candidate, backing := composeCandidate(tree, marker, marker.composeFiles); backing {
				backingOnly = append(backingOnly, candidate)
			} else {
				result = append(result, candidate)
			}
		}
	}
	// Two Dockerfiles can resolve to the same context and name only through a
	// Compose reference naming both; the first one stands.
	seen := map[string]bool{}
	unique := result[:0]
	for _, candidate := range result {
		if !seen[candidate.ID] {
			seen[candidate.ID] = true
			unique = append(unique, candidate)
		}
	}
	return unique, backingOnly
}

// annotateImageFacts adds what detection can prove about each candidate's
// image from the tree: the backing databases a development Compose file
// runs, the release command the repository declares, the start script a
// recipe executes, and the .dockerignore rules a recipe build has to set
// aside.
func annotateImageFacts(tree detectionTree, markers map[string]*detectedMarkers, candidates []DetectedCandidate) {
	for index := range candidates {
		candidate := &candidates[index]
		marker := markers[filepath.FromSlash(candidate.Root)]
		if marker != nil && candidate.BuildMethod != BuildCompose && len(marker.composeFiles) > 0 {
			if kind, reason := classifyCompose(tree, marker.composeFiles); kind != composeKindApplication {
				for _, database := range composeBackingDatabases(marker.composeFiles) {
					if len(candidate.Databases) >= 8 || databaseSuggested(candidate.Databases, database.Engine) {
						continue
					}
					candidate.Databases = append(candidate.Databases, database)
				}
				if kind == composeKindInfrastructure {
					candidate.Evidence = append(candidate.Evidence, DetectionEvidence{
						Path: marker.composeFiles[0].path, Reason: "Compose file " + reason + "; offered as databases, not deployed",
					})
				}
			}
		}
		if candidate.ReleaseCommand == "" && marker != nil {
			if command, evidence, ok := declaredReleaseCommand(tree, marker); ok {
				candidate.ReleaseCommand = command
				candidate.Evidence = append(candidate.Evidence, evidence)
			}
		}
		switch candidate.BuildMethod {
		case BuildRecipe, BuildStatic:
			if candidate.BuildMethod == BuildRecipe && candidate.StartCommand != "" {
				candidate.ImageBuildIssues = append(candidate.ImageBuildIssues,
					startCommandScriptIssues(tree, candidate.Root, candidate.StartCommand)...)
			}
			kind := candidate.Recipe
			if candidate.BuildMethod == BuildStatic {
				kind = "static"
			}
			ignore, _ := tree.read(joinRoot(candidate.Root, ".dockerignore"), 256<<10)
			_, dropped := recipeDockerignore(ignore, tree.entries(candidate.Root), kind)
			for _, drop := range dropped {
				candidate.ImageBuildIssues = append(candidate.ImageBuildIssues, newImageBuildIssue("dockerignore_drops_recipe_input",
					PreflightWarning, 0, drop.Input, ".dockerignore rule "+drop.Rule+" would leave out "+drop.Input+"; the automatic build sets that rule aside"))
			}
			if candidate.Recipe == "php" {
				candidate.ImageBuildIssues = append(candidate.ImageBuildIssues, htaccessIssues(tree, candidate)...)
			}
		}
		if len(candidate.ImageBuildIssues) > 32 {
			candidate.ImageBuildIssues = candidate.ImageBuildIssues[:32]
		}
	}
}

func databaseSuggested(databases []DetectedDatabase, engine string) bool {
	for _, database := range databases {
		if database.Engine == engine {
			return true
		}
	}
	return false
}

var htaccessRuleRE = regexp.MustCompile(`(?im)^\s*(?:deny|require|rewriterule|order)\b`)

// htaccessIssues: FrankenPHP is not Apache, so the .htaccess rules a PHP
// application relies on to keep logs, dotfiles and includes private are not
// read at all.
func htaccessIssues(tree detectionTree, candidate *DetectedCandidate) []ImageBuildIssue {
	documentRoot := phpDocumentRoot(candidate.StartCommand)
	if documentRoot == "-" {
		return nil
	}
	served := joinRoot(candidate.Root, documentRoot)
	directories := []string{served}
	for index, name := range tree.entries(served) {
		if index >= 128 {
			break
		}
		if info, ok := tree.lstat(path.Join(served, name)); ok && info.IsDir() && !strings.HasPrefix(name, ".") {
			directories = append(directories, path.Join(served, name))
		}
	}
	for _, directory := range directories {
		relative := path.Join(directory, ".htaccess")
		if content, ok := tree.read(relative, 64<<10); ok && htaccessRuleRE.Match(content) {
			return []ImageBuildIssue{newImageBuildIssue("php_htaccess_ignored", PreflightWarning, 0, relative,
				relative+" has access or rewrite rules; FrankenPHP does not read .htaccess, so they do not apply")}
		}
	}
	return nil
}

var phpDocumentRootRE = regexp.MustCompile(`--root\s+/app(/[^\s]*)?(?:\s|$)`)

// phpDocumentRoot is the directory under the application a FrankenPHP start
// command serves, "" for the application root itself, "-" when unknown.
func phpDocumentRoot(command string) string {
	match := phpDocumentRootRE.FindStringSubmatch(command)
	if match == nil {
		return "-"
	}
	return strings.Trim(match[1], "/")
}

// declaredReleaseCommand is the command a repository says runs once before
// each release: Heroku's Procfile `release:`, fly.toml's
// `[deploy] release_command`, render.yaml's `preDeployCommand`.
func declaredReleaseCommand(tree detectionTree, marker *detectedMarkers) (string, DetectionEvidence, bool) {
	root := filepath.ToSlash(marker.root)
	accept := func(command, source, label string) (string, DetectionEvidence, bool) {
		command = strings.TrimSpace(command)
		if command == "" || len(command) > 1024 || strings.ContainsAny(command, "\x00\r\n") ||
			rejectPlanSecretLiteral("release command", command) != nil || secretCommandFlagRE.MatchString(command) {
			return "", DetectionEvidence{}, false
		}
		return command, DetectionEvidence{Path: source, Reason: label + ": " + boundedEvidence(command)}, true
	}
	if command := procfileProcess(marker.procfile, "release"); command != "" {
		if result, evidence, ok := accept(command, joinRoot(root, "Procfile"), "release process"); ok {
			return result, evidence, true
		}
	}
	if content, ok := tree.read(joinRoot(root, "fly.toml"), 64<<10); ok {
		if command := flyReleaseCommand(content); command != "" {
			if result, evidence, ok := accept(command, joinRoot(root, "fly.toml"), "release_command"); ok {
				return result, evidence, true
			}
		}
	}
	for _, name := range []string{"render.yaml", "render.yml"} {
		if content, ok := tree.read(joinRoot(root, name), 256<<10); ok {
			if command := renderPreDeployCommand(content); command != "" {
				if result, evidence, ok := accept(command, joinRoot(root, name), "preDeployCommand"); ok {
					return result, evidence, true
				}
			}
		}
	}
	return "", DetectionEvidence{}, false
}

var flyReleaseCommandRE = regexp.MustCompile(`^\s*release_command\s*=\s*(?:"([^"]*)"|'([^']*)')\s*(?:#.*)?$`)

func flyReleaseCommand(content []byte) string {
	section := ""
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[] ")
			continue
		}
		if section != "deploy" {
			continue
		}
		if match := flyReleaseCommandRE.FindStringSubmatch(line); match != nil {
			return match[1] + match[2]
		}
	}
	return ""
}

func renderPreDeployCommand(content []byte) string {
	var root yaml.Node
	if yaml.Unmarshal(content, &root) != nil || countYAMLNodes(&root, 0) > 100_000 {
		return ""
	}
	services := mappingValue(documentMapping(&root), "services")
	if services == nil || services.Kind != yaml.SequenceNode {
		return ""
	}
	found := ""
	for _, service := range services.Content {
		command := scalarMappingValue(service, "preDeployCommand")
		switch {
		case command == "":
		case found == "":
			found = command
		case found != command:
			// Several services, several commands: which one this deployment
			// is cannot be read from the file.
			return ""
		}
	}
	return found
}

// sanitizeDetectionEvidence keeps evidence from repository names inside what
// validateDetectionResult accepts: a file or image named like an assignment,
// or a path with a control character, must cost that one line of evidence
// rather than make the whole detection unsavable.
func sanitizeDetectionEvidence(candidates []DetectedCandidate) {
	for index := range candidates {
		kept := candidates[index].Evidence[:0]
		for _, evidence := range candidates[index].Evidence {
			if len(evidence.Path) > 4096 || strings.ContainsAny(evidence.Path, "\x00\r\n") {
				continue
			}
			if len(evidence.Reason) > 512 || strings.ContainsAny(evidence.Reason, "\x00\r\n") ||
				rejectPlanSecretLiteral("detection evidence", evidence.Reason) != nil {
				evidence.Reason = "details withheld because they resemble credential material"
			}
			kept = append(kept, evidence)
		}
		if len(kept) > 128 {
			kept = kept[:128]
		}
		candidates[index].Evidence = kept
		databases := candidates[index].Databases[:0]
		for _, database := range candidates[index].Databases {
			if len(database.Evidence) <= 512 && !strings.ContainsAny(database.Evidence, "\x00\r\n") &&
				rejectPlanSecretLiteral("detected database evidence", database.Evidence) == nil {
				databases = append(databases, database)
			}
		}
		candidates[index].Databases = databases
	}
}
