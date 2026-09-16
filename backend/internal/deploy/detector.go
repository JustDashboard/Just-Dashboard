package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type DetectionLimits struct {
	MaxFiles     int
	MaxReadBytes int64
	MaxFileBytes int64
	MaxDepth     int
	MaxDuration  time.Duration
}

func (l DetectionLimits) normalized() DetectionLimits {
	if l.MaxFiles <= 0 || l.MaxFiles > 100_000 {
		l.MaxFiles = 10_000
	}
	if l.MaxReadBytes <= 0 || l.MaxReadBytes > 64<<20 {
		l.MaxReadBytes = 4 << 20
	}
	if l.MaxFileBytes <= 0 || l.MaxFileBytes > 4<<20 {
		l.MaxFileBytes = 512 << 10
	}
	if l.MaxDepth <= 0 || l.MaxDepth > 32 {
		l.MaxDepth = 10
	}
	if l.MaxDuration <= 0 || l.MaxDuration > 30*time.Second {
		l.MaxDuration = 5 * time.Second
	}
	return l
}

type Detector struct{ Limits DetectionLimits }

type detectedMarkers struct {
	root              string
	dockerfile        string
	dockerfileContent []byte
	compose           []string
	lockfiles         []string
	packageJSON       []byte
	packagePath       string
	goMod             string
	goModContent      []byte
	goVersionFile     []byte
	pythonFiles       map[string][]byte
	staticFile        string
}

func (d Detector) DetectPath(ctx context.Context, root string, identity SourceIdentity) (DetectionResult, error) {
	limits := d.Limits.normalized()
	result := DetectionResult{Source: identity, Candidates: []DetectedCandidate{}}
	info, err := os.Stat(root)
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrSourceUnavailable, err)
	}
	if !info.IsDir() {
		return result, fmt.Errorf("%w: detection root is not a directory", ErrInvalidSource)
	}
	detectCtx, cancel := context.WithTimeout(ctx, limits.MaxDuration)
	defer cancel()
	markers := map[string]*detectedMarkers{}
	gitModulesPath := ""
	lfsAttributesPath := ""
	cgoPaths := []string{}
	skip := map[string]bool{
		".just-dashboard": true,
		".git":            true, "node_modules": true, "vendor": true, ".next": true,
		"dist": true, "build": true, "target": true, ".cache": true,
		".venv": true, "venv": true, "__pycache__": true,
	}
	stop := errors.New("bounded detector stopped")
	walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := detectCtx.Err(); err != nil {
			result.Truncated = true
			result.TruncatedReason = "time limit reached"
			return stop
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		depth := 0
		if rel != "." {
			depth = strings.Count(rel, string(filepath.Separator)) + 1
		}
		if entry.IsDir() {
			if rel != "." && (skip[entry.Name()] || depth > limits.MaxDepth) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		result.ScannedFiles++
		if result.ScannedFiles > limits.MaxFiles {
			result.Truncated = true
			result.TruncatedReason = "file limit reached"
			return stop
		}
		name := strings.ToLower(entry.Name())
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			content, n, err := readDetectionFile(path, limits.MaxFileBytes)
			result.ScannedBytes += n
			if result.ScannedBytes > limits.MaxReadBytes {
				result.Truncated, result.TruncatedReason = true, "read-byte limit reached"
				return stop
			}
			if err == nil && sourceUsesCGO(content) {
				cgoPaths = append(cgoPaths, rel)
			}
			return nil
		}
		interesting := name == "package.json" || name == "go.mod" || name == "dockerfile" ||
			name == ".go-version" ||
			name == "containerfile" || name == "compose.yml" || name == "compose.yaml" ||
			name == "docker-compose.yml" || name == "docker-compose.yaml" ||
			name == "index.html" || name == ".gitmodules" || name == ".gitattributes" ||
			name == "bun.lock" || name == "bun.lockb" || name == "package-lock.json" ||
			name == "pnpm-lock.yaml" || name == "yarn.lock" || name == "requirements.txt" ||
			name == "uv.lock" || name == "poetry.lock" || name == "pyproject.toml"
		if !interesting {
			return nil
		}
		if name == ".gitmodules" {
			gitModulesPath = rel
			return nil
		}
		if name == ".gitattributes" {
			content, n, err := readDetectionFile(path, limits.MaxFileBytes)
			result.ScannedBytes += n
			if result.ScannedBytes > limits.MaxReadBytes {
				result.Truncated = true
				result.TruncatedReason = "read-byte limit reached"
				return stop
			}
			if err == nil && strings.Contains(strings.ToLower(string(content)), "filter=lfs") {
				lfsAttributesPath = rel
			}
			return nil
		}
		parent := filepath.Dir(rel)
		if parent == "." {
			parent = ""
		}
		marker := markers[parent]
		if marker == nil {
			marker = &detectedMarkers{root: parent, pythonFiles: map[string][]byte{}}
			markers[parent] = marker
		}
		switch name {
		case "dockerfile", "containerfile":
			if marker.dockerfile == "" {
				marker.dockerfile = rel
				content, n, err := readDetectionFile(path, limits.MaxFileBytes)
				result.ScannedBytes += n
				if err == nil && result.ScannedBytes <= limits.MaxReadBytes {
					marker.dockerfileContent = content
				} else if result.ScannedBytes > limits.MaxReadBytes {
					result.Truncated, result.TruncatedReason = true, "read-byte limit reached"
					return stop
				}
			}
		case "compose.yml", "compose.yaml", "docker-compose.yml", "docker-compose.yaml":
			marker.compose = append(marker.compose, rel)
		case "go.mod":
			marker.goMod = rel
			content, n, err := readDetectionFile(path, limits.MaxFileBytes)
			result.ScannedBytes += n
			if err == nil {
				marker.goModContent = content
			}
		case ".go-version":
			content, n, err := readDetectionFile(path, limits.MaxFileBytes)
			result.ScannedBytes += n
			if err == nil {
				marker.goVersionFile = content
			}
		case "bun.lock", "bun.lockb", "package-lock.json", "pnpm-lock.yaml", "yarn.lock":
			marker.lockfiles = append(marker.lockfiles, rel)
		case "uv.lock", "poetry.lock":
			marker.pythonFiles[name] = []byte("locked")
		case "requirements.txt", "pyproject.toml":
			content, n, err := readDetectionFile(path, limits.MaxFileBytes)
			result.ScannedBytes += n
			if err == nil && result.ScannedBytes <= limits.MaxReadBytes {
				marker.pythonFiles[name] = content
			} else if result.ScannedBytes > limits.MaxReadBytes {
				result.Truncated = true
				result.TruncatedReason = "read-byte limit reached"
				return stop
			}
		case "index.html":
			marker.staticFile = rel
		case "package.json":
			content, n, err := readDetectionFile(path, limits.MaxFileBytes)
			result.ScannedBytes += n
			if err == nil && result.ScannedBytes <= limits.MaxReadBytes {
				marker.packageJSON, marker.packagePath = content, rel
			} else if result.ScannedBytes > limits.MaxReadBytes {
				result.Truncated = true
				result.TruncatedReason = "read-byte limit reached"
				return stop
			}
		}
		if result.ScannedBytes > limits.MaxReadBytes {
			result.Truncated, result.TruncatedReason = true, "read-byte limit reached"
			return stop
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, stop) && !errors.Is(walkErr, context.Canceled) && !errors.Is(walkErr, context.DeadlineExceeded) {
		return result, walkErr
	}

	roots := make([]string, 0, len(markers))
	for candidateRoot := range markers {
		roots = append(roots, candidateRoot)
	}
	sort.Strings(roots)
	for _, candidateRoot := range roots {
		marker := markers[candidateRoot]
		result.Candidates = append(result.Candidates, candidatesForMarkers(marker)...)
	}
	sort.Slice(result.Candidates, func(i, j int) bool {
		if result.Candidates[i].Root != result.Candidates[j].Root {
			return result.Candidates[i].Root < result.Candidates[j].Root
		}
		if result.Candidates[i].BuildMethod != result.Candidates[j].BuildMethod {
			return result.Candidates[i].BuildMethod < result.Candidates[j].BuildMethod
		}
		return result.Candidates[i].ID < result.Candidates[j].ID
	})
	for index := range result.Candidates {
		candidate := &result.Candidates[index]
		if candidate.Recipe == "go" {
			for _, path := range cgoPaths {
				if candidate.Root == "" || strings.HasPrefix(path, candidate.Root+string(filepath.Separator)) {
					candidate.RecipeIssue = "CGO source requires a Dockerfile with the required C toolchain"
					break
				}
			}
		}
		if gitModulesPath != "" {
			result.Candidates[index].Evidence = append(result.Candidates[index].Evidence,
				DetectionEvidence{Path: gitModulesPath, Reason: "Git submodules are declared but not fetched during bounded detection"})
			result.Candidates[index].NeedsDecision = append(result.Candidates[index].NeedsDecision,
				"confirm required submodules and credential access")
		}
		if lfsAttributesPath != "" {
			result.Candidates[index].Evidence = append(result.Candidates[index].Evidence,
				DetectionEvidence{Path: lfsAttributesPath, Reason: "Git LFS objects are skipped during bounded detection"})
			result.Candidates[index].NeedsDecision = append(result.Candidates[index].NeedsDecision,
				"confirm required Git LFS objects and credential access")
		}
	}
	result.GitRequirements = GitRequirements{
		Submodules: gitModulesPath != "",
		LFS:        lfsAttributesPath != "",
	}
	result.SelectedID = selectedCandidate(result.Candidates)
	return result, nil
}

func readDetectionFile(path string, max int64) ([]byte, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return nil, int64(len(content)), err
	}
	if int64(len(content)) > max {
		return nil, int64(len(content)), fmt.Errorf("detection marker exceeds %d bytes", max)
	}
	return content, int64(len(content)), nil
}

func candidatesForMarkers(marker *detectedMarkers) []DetectedCandidate {
	var result []DetectedCandidate
	rootLabel := marker.root
	if rootLabel == "" {
		rootLabel = "."
	}
	if marker.dockerfile != "" {
		result = append(result, newDetectedCandidate(marker.root, BuildDockerfile, DetectedCandidate{
			Name: "Dockerfile in " + rootLabel, Profile: ProfileWeb, Confidence: ConfidenceHigh,
			Dockerfile:    filepath.Base(marker.dockerfile),
			Port:          detectedDockerfilePort(marker.dockerfileContent),
			Evidence:      []DetectionEvidence{{Path: marker.dockerfile, Reason: "container build definition"}},
			NeedsDecision: []string{"confirm container port and readiness check"},
		}))
	}
	if len(marker.compose) > 0 {
		sort.Strings(marker.compose)
		evidence := make([]DetectionEvidence, 0, len(marker.compose))
		for _, path := range marker.compose {
			evidence = append(evidence, DetectionEvidence{Path: path, Reason: "Compose configuration"})
		}
		result = append(result, newDetectedCandidate(marker.root, BuildCompose, DetectedCandidate{
			Name: "Compose stack in " + rootLabel, Profile: ProfileCompose,
			Confidence: ConfidenceHigh, Evidence: evidence,
			NeedsDecision: []string{"review services, storage, ports, and unsupported fields"},
		}))
	}
	if len(marker.packageJSON) > 0 {
		result = append(result, packageCandidate(marker)...)
	}
	if marker.goMod != "" {
		candidate := DetectedCandidate{
			Name: "Go service in " + rootLabel, Profile: ProfileService, Confidence: ConfidenceHigh,
			Framework: "go", Recipe: "go",
			Evidence:      []DetectionEvidence{{Path: marker.goMod, Reason: "Go module definition"}},
			NeedsDecision: []string{"confirm executable, start command, port, and readiness check"},
		}
		version, err := chooseGoRecipeVersion("", string(marker.goVersionFile), marker.goModContent)
		candidate.GoMinimumVersion = goModuleMinimum(marker.goModContent)
		if err != nil {
			candidate.RecipeIssue = err.Error()
		} else {
			candidate.GoVersion = version
		}
		result = append(result, newDetectedCandidate(marker.root, BuildRecipe, candidate))
	}
	if pythonLock, ok := detectedPythonLock(marker.pythonFiles); ok {
		result = append(result, newDetectedCandidate(marker.root, BuildRecipe, DetectedCandidate{
			Name: "Python service in " + rootLabel, Profile: ProfileService, Confidence: ConfidenceMedium,
			Framework: "python", Recipe: "python",
			Evidence:      []DetectionEvidence{{Path: filepath.ToSlash(filepath.Join(marker.root, pythonLock)), Reason: "locked Python dependency input"}},
			NeedsDecision: []string{"confirm ASGI/WSGI start command, port, and readiness check"},
		}))
	}
	if marker.staticFile != "" && len(marker.packageJSON) == 0 {
		result = append(result, newDetectedCandidate(marker.root, BuildStatic, DetectedCandidate{
			Name: "Static site in " + rootLabel, Profile: ProfileStatic, Confidence: ConfidenceMedium,
			Port:          80,
			Evidence:      []DetectionEvidence{{Path: marker.staticFile, Reason: "static HTML entry point"}},
			NeedsDecision: []string{"confirm the public directory"},
		}))
	}
	return result
}

func packageCandidate(marker *detectedMarkers) []DetectedCandidate {
	var manifest struct {
		Name            string            `json:"name"`
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(marker.packageJSON, &manifest) != nil {
		return []DetectedCandidate{newDetectedCandidate(marker.root, BuildRecipe, DetectedCandidate{
			Name: "JavaScript project", Profile: ProfileWorker, Confidence: ConfidenceLow,
			Evidence:      []DetectionEvidence{{Path: marker.packagePath, Reason: "package.json could not be parsed"}},
			NeedsDecision: []string{"choose build and start commands"},
		})}
	}
	dependencies := map[string]string{}
	for key, value := range manifest.Dependencies {
		dependencies[key] = value
	}
	for key, value := range manifest.DevDependencies {
		dependencies[key] = value
	}
	name := manifest.Name
	if name == "" || len(name) > 256 || rejectPlanSecretLiteral("package name", name) != nil {
		name = "JavaScript project"
	}
	candidate := DetectedCandidate{
		Name: name, Profile: ProfileWorker, BuildMethod: BuildRecipe,
		Recipe:        "node",
		Confidence:    ConfidenceMedium,
		Evidence:      []DetectionEvidence{{Path: marker.packagePath, Reason: "JavaScript package manifest"}},
		NeedsDecision: []string{},
	}
	present := make([]string, 0, len(marker.lockfiles))
	for _, lockfile := range marker.lockfiles {
		present = append(present, filepath.Base(lockfile))
	}
	candidate.PackageManagers = nodePackageManagers(present)
	manager, lockfile, _ := resolveNodePackageManager(present, declaredNodePackageManager(marker.packageJSON), "")
	candidate.PackageManager = manager
	switch {
	case len(marker.lockfiles) == 1:
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{
			Path: marker.lockfiles[0], Reason: "single recognized JavaScript lockfile",
		})
	case len(marker.lockfiles) == 0:
		candidate.Confidence = ConfidenceLow
		candidate.NeedsDecision = append(candidate.NeedsDecision, "add one supported JavaScript lockfile")
	case manager != "":
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{
			Path: marker.packagePath, Reason: "packageManager selects " + manager + " and " + lockfile + " among competing lockfiles",
		})
	default:
		candidate.Confidence = ConfidenceLow
		candidate.NeedsDecision = append(candidate.NeedsDecision, "choose the package manager: competing lockfiles "+strings.Join(present, ", "))
	}
	// The runner has to be the one the lockfile names. The build recipe picks
	// its base image from that lockfile — a bun.lock project builds on
	// oven/bun, which has no npm on it at all — so "npm run build" was not a
	// harmless stylistic default: it was a build that died on `npm: not found`
	// after a successful install, with nothing in the configuration screen
	// saying which field was wrong. With no lockfile, or with competing ones
	// nothing resolves, npm is the guess that fails most legibly: the recipe
	// refuses a build it cannot pin before any command is run.
	runner := manager
	if runner == "" {
		runner = "npm"
	}
	if command := manifest.Scripts["build"]; command != "" {
		candidate.BuildCommand = runner + " run build"
		candidate.Evidence = append(candidate.Evidence,
			DetectionEvidence{Path: marker.packagePath, Reason: "build script: " + boundedEvidence(command)})
	}
	if command := manifest.Scripts["start"]; command != "" {
		candidate.StartCommand = runner + " run start"
		candidate.Profile = ProfileWeb
		candidate.Port = 3000
		candidate.Evidence = append(candidate.Evidence,
			DetectionEvidence{Path: marker.packagePath, Reason: "start script: " + boundedEvidence(command)})
	} else {
		candidate.NeedsDecision = append(candidate.NeedsDecision, "choose a start command or static output")
	}
	switch {
	case dependencies["next"] != "":
		candidate.Framework, candidate.Profile = "nextjs", ProfileWeb
		candidate.Confidence, candidate.Port = ConfidenceHigh, 3000
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{
			Path: marker.packagePath, Reason: "next dependency " + boundedEvidence(dependencies["next"]),
		})
	case dependencies["@sveltejs/kit"] != "":
		candidate.Framework, candidate.Profile = "sveltekit", ProfileWeb
		candidate.Confidence, candidate.Port = ConfidenceHigh, 3000
		switch {
		case dependencies["@sveltejs/adapter-node"] != "" && dependencies["@sveltejs/adapter-static"] == "":
			candidate.StartCommand = "node build"
			if runner == "bun" {
				candidate.StartCommand = "bun ./build/index.js"
			}
		case dependencies["@sveltejs/adapter-static"] != "" && dependencies["@sveltejs/adapter-node"] == "":
			candidate.Profile, candidate.OutputDirectory, candidate.Port, candidate.StartCommand = ProfileStatic, "build", 80, ""
		default:
			candidate.Confidence = ConfidenceLow
			candidate.NeedsDecision = append(candidate.NeedsDecision, "select adapter-node or adapter-static for this server; adapter-auto and provider adapters require a Dockerfile")
		}
	case dependencies["vite"] != "":
		candidate.Framework, candidate.Profile = "vite", ProfileStatic
		candidate.Confidence, candidate.OutputDirectory, candidate.Port, candidate.StartCommand = ConfidenceHigh, "dist", 80, ""
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{
			Path: marker.packagePath, Reason: "vite dependency " + boundedEvidence(dependencies["vite"]),
		})
	}
	if _, err := validateNodeRecipeContent(marker.packageJSON, BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: candidate.BuildCommand, StartCommand: candidate.StartCommand, OutputDirectory: candidate.OutputDirectory}); err != nil {
		candidate.RecipeIssue = err.Error()
	}
	return []DetectedCandidate{newDetectedCandidate(marker.root, BuildRecipe, candidate)}
}

func detectedPythonLock(files map[string][]byte) (string, bool) {
	if _, ok := files["uv.lock"]; ok {
		return "uv.lock", true
	}
	if _, ok := files["poetry.lock"]; ok {
		return "poetry.lock", true
	}
	requirements, ok := files["requirements.txt"]
	if !ok {
		return "", false
	}
	seen := false
	for _, raw := range strings.Split(string(requirements), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "--") {
			continue
		}
		seen = true
		if !strings.Contains(line, "==") && !strings.Contains(line, "@") {
			return "", false
		}
	}
	return "requirements.txt", seen
}

func newDetectedCandidate(root string, method BuildMethod, candidate DetectedCandidate) DetectedCandidate {
	candidate.Root = root
	candidate.BuildMethod = method
	if candidate.Evidence == nil {
		candidate.Evidence = []DetectionEvidence{}
	}
	if candidate.NeedsDecision == nil {
		candidate.NeedsDecision = []string{}
	}
	hash := sha256.Sum256([]byte(root + "\x00" + string(method) + "\x00" + candidate.Name))
	candidate.ID = "candidate-" + hex.EncodeToString(hash[:6])
	return candidate
}

func selectedCandidate(candidates []DetectedCandidate) string {
	best := ""
	bestRank := 0
	tied := false
	for _, candidate := range candidates {
		rank := confidenceRank(candidate.Confidence)
		if rank > bestRank {
			best, bestRank, tied = candidate.ID, rank, false
		} else if rank == bestRank && rank != 0 {
			tied = true
		}
	}
	if tied {
		return ""
	}
	return best
}

func confidenceRank(confidence DetectionConfidence) int {
	switch confidence {
	case ConfidenceHigh:
		return 3
	case ConfidenceMedium:
		return 2
	case ConfidenceLow:
		return 1
	default:
		return 0
	}
}

func boundedEvidence(value string) string {
	value = strings.TrimSpace(value)
	if rejectPlanSecretLiteral("detection evidence", value) != nil {
		return "script present (details withheld because it resembles credential material)"
	}
	if len(value) > 160 {
		return value[:157] + "..."
	}
	return value
}
