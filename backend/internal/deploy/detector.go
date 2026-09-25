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
	"slices"
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
	root        string
	dockerfiles []detectedDockerfile
	// builtDockerfiles are the Dockerfiles this root's container candidates
	// build, by candidate ID (attachBuiltDockerfiles).
	builtDockerfiles  map[string]detectedDockerfile
	packageSwift      []byte
	compose           []string
	composeFiles      []composeDetection
	lockfiles         []string
	packageJSON       []byte
	packagePath       string
	goMod             string
	goModContent      []byte
	goVersionFile     []byte
	pythonFiles       map[string][]byte
	staticFile        string
	angularJSON       []byte
	procfile          []byte
	managePy          bool
	pythonVersionFile []byte
	runtimeTxt        []byte
	cargoToml         []byte
	cargoLock         bool
	rustToolchain     []byte
	pomXML            []byte
	gradleBuild       []byte
	gradleBuildPath   string
	gradlew           bool
	javaVersionFile   []byte
	csprojs           map[string][]byte
	denoJSON          []byte
	denoJSONPath      string
	denoLock          bool
	denoEntries       map[string]bool
	composerJSON      []byte
	composerLock      bool
	phpIndex          bool
	phpPublicIndex    bool
	// node is the package's install inputs, read after the walk under
	// their own budget.
	node *nodeInstallSource
	// release is the command the repository declares runs once before each
	// release (declaredReleaseCommand), read before any candidate is made:
	// a schema step it runs is not chained into the start command too.
	release         string
	releaseEvidence DetectionEvidence
}

// phpOwnsAssets says the PHP recipe builds this root's package.json itself:
// a Laravel or Symfony application's Vite or Encore bundle is a stage of the
// PHP image, not a site of its own.
func (m *detectedMarkers) phpOwnsAssets() bool {
	manifest, ok := parseComposerManifest(m.composerJSON)
	if !ok {
		return false
	}
	for _, framework := range phpFrameworks {
		if manifest.has(framework.pkg) {
			return true
		}
	}
	return false
}

// denoEntryNames are the files a Deno service is conventionally run from
// when deno.json declares no start task.
var denoEntryNames = map[string]bool{
	"main.ts": true, "server.ts": true, "mod.ts": true, "main.js": true, "server.js": true,
	"index.ts": true, "app.ts": true, "main.tsx": true,
}

// pythonEntryDirsSkipped are the directories an application object is never
// looked for in, so a test's fixture app cannot become the served one.
var pythonEntryDirsSkipped = map[string]bool{"tests": true, "test": true, "migrations": true, "examples": true, "example": true}

func pythonEntryCandidate(rel, name string, depth int) bool {
	if !pythonEntryNames[name] || name == "manage.py" || depth > 4 || (name == "__init__.py" && depth > 3) {
		return false
	}
	for _, segment := range strings.Split(filepath.ToSlash(filepath.Dir(rel)), "/") {
		if pythonEntryDirsSkipped[segment] {
			return false
		}
	}
	return true
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
	shape := newRepoShapeScan(root, limits)
	goSources := newGoSourceScan()
	schemaPaths := []string{}
	schemaPathCounts := map[string]int{}
	pythonEntries := []pythonEntry{}
	denoEntryPaths := []string{}
	skippedBuild := []string{}
	scanner := newEnvScanner()
	readiness := newReadinessScanner()
	state := newStateScanner()
	prismaProviders := map[string]string{}
	network := newNetworkDetection(detectCtx, root)
	skip := map[string]bool{
		".just-dashboard": true,
		".git":            true, "node_modules": true, "vendor": true, ".next": true,
		"dist": true, "build": true, "target": true, ".cache": true,
		".venv": true, "venv": true, "__pycache__": true,
	}
	stop := errors.New("bounded detector stopped")
	// The file bound counts files detection opens, not files a repository
	// holds: ten thousand images are no reason to stop looking for the
	// manifest beside them.
	readMarkerFile := func(path string, max int64) ([]byte, int64, error) {
		if result.ScannedFiles >= limits.MaxFiles {
			result.Truncated, result.TruncatedReason = true, "file limit reached"
			return nil, 0, stop
		}
		result.ScannedFiles++
		return readDetectionFile(path, max)
	}
	manifestFirst := func(name string) bool { return detectionInterestingName(name) || repoShapeFileName(name) }
	walkErr := walkDetectionTree(root, manifestFirst, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := detectCtx.Err(); err != nil {
			result.Truncated = true
			result.TruncatedReason = "time limit reached"
			return stop
		}
		if result.Truncated {
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
			switch {
			case rel == ".":
				return nil
			case skip[entry.Name()]:
				// Go's project layout keeps its image definition under build/,
				// which is otherwise output (addSkippedBuildDockerfiles).
				if entry.Name() == "build" && depth <= limits.MaxDepth {
					skippedBuild = append(skippedBuild, filepath.ToSlash(rel))
				}
				return filepath.SkipDir
			case depth > limits.MaxDepth:
				shape.depthPruned(path, rel)
				return filepath.SkipDir
			case shape.setAsideDirectory(path, rel, entry.Name()):
				return filepath.SkipDir
			}
			return nil
		}
		shape.observeFile(rel, entry)
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		name := strings.ToLower(entry.Name())
		readiness.visit(path, filepath.ToSlash(rel))
		state.observe(filepath.ToSlash(rel), name, path, entry)
		// Presence is all a schema marker proves, so a repository with a
		// thousand migrations records a handful of them. The bound is per
		// name: migrations sort before the schema they belong to, and an
		// overall cap would have let them crowd it out.
		if schemaMarkerFile(name) {
			if schemaPathCounts[name] < 16 {
				schemaPathCounts[name]++
				schemaPaths = append(schemaPaths, filepath.ToSlash(rel))
			}
			if strings.HasSuffix(name, ".prisma") && scanner.budget(64<<10) {
				if content, _, err := readDetectionFile(path, 64<<10); err == nil {
					if provider := prismaProvider(content); provider != "" {
						prismaProviders[filepath.ToSlash(rel)] = provider
					}
					scanner.observeSchema(filepath.ToSlash(rel), content)
				}
			}
			// The first few migrations are where a schema enables the
			// extensions the database it runs on has to provide.
			if name == "migration.sql" && schemaPathCounts[name] <= 4 && scanner.budget(64<<10) {
				if content, _, err := readDetectionFile(path, 64<<10); err == nil {
					scanner.observeSchema(filepath.ToSlash(rel), content)
				}
			}
			return nil
		}
		if envTemplateFile(name) {
			if scanner.budget(64 << 10) {
				if content, _, err := readDetectionFile(path, 64<<10); err == nil {
					scanner.scanTemplate(filepath.ToSlash(rel), content, envRealFile(name))
				}
			}
			return nil
		}
		if pythonEntryCandidate(filepath.ToSlash(rel), name, depth) {
			// Bounded like the schema markers: a few dozen conventional files
			// are enough to find an application object, and a repository of
			// packages must not turn detection into a source scan.
			if len(pythonEntries) < 64 {
				content, n, err := readMarkerFile(path, 64<<10)
				result.ScannedBytes += n
				if result.ScannedBytes > limits.MaxReadBytes {
					result.Truncated, result.TruncatedReason = true, "read-byte limit reached"
					return stop
				}
				if err == nil {
					pythonEntries = append(pythonEntries, pythonEntry{path: filepath.ToSlash(rel), content: content})
					if scanner.scannable(filepath.ToSlash(rel), name) && scanner.budget(n) {
						scanner.scanSource(filepath.ToSlash(rel), content)
					}
				}
			}
			return nil
		}
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			// Go source is read head-first under a budget of its own, so
			// generated code cannot spend what the manifests need.
			content, ok := goSources.read(path, rel, name)
			if ok {
				source := content
				if len(content) == goScanHead && goPackageMainRE.Match(content) {
					// A main package's listener is as often at the end of the
					// file as at the top, so network reads it whole.
					if whole, _, err := readDetectionFile(path, limits.MaxFileBytes); err == nil {
						source = whole
					}
				}
				network.observeGo(filepath.ToSlash(rel), source)
			}
			if ok && scanner.scannable(filepath.ToSlash(rel), name) {
				if len(content) == goScanHead {
					if info, err := entry.Info(); err == nil && scanner.budget(info.Size()) {
						if full, _, err := readDetectionFile(path, envScanMaxFile); err == nil {
							scanner.scanSource(filepath.ToSlash(rel), full)
						}
					}
				} else if scanner.budget(int64(len(content))) {
					scanner.scanSource(filepath.ToSlash(rel), content)
				}
			}
			return nil
		}
		if shapeRoot, shapeKey, limit, ok := shapeFileTarget(rel, name, depth); ok {
			shape.readFile(path, shapeRoot, shapeKey, limit)
			// A vite.config or a tasks.py is also application source whose
			// environment reads the form lists, and a Gemfile.lock or
			// config/database.yml a fact file its classification reads.
			if !envSourceExtensions[filepath.Ext(name)] && !scanner.factFile(filepath.ToSlash(rel), name) {
				return nil
			}
		}
		interesting := detectionInterestingName(name)
		if denoEntryNames[name] && len(denoEntryPaths) < 64 {
			denoEntryPaths = append(denoEntryPaths, filepath.ToSlash(rel))
		}
		if !interesting {
			network.observeFile(path, filepath.ToSlash(rel), entry.Name())
			if scanner.factFile(filepath.ToSlash(rel), name) || scanner.scannable(filepath.ToSlash(rel), name) {
				// Application code is read under the scanner's own budget, apart
				// from detection's limits: the names an application reads are a
				// convenience for the form, never a reason to call a scan truncated.
				if info, err := entry.Info(); err == nil && scanner.admit(filepath.ToSlash(rel), name, info.Size()) {
					if content, _, err := readDetectionFile(path, envScanMaxFile); err == nil {
						scanner.scanSource(filepath.ToSlash(rel), content)
						shape.sources.scan(filepath.ToSlash(rel), name, content)
					}
				}
			}
			return nil
		}
		if name == ".gitmodules" {
			content, n, err := readMarkerFile(path, 64<<10)
			result.ScannedBytes += n
			if err == nil {
				shape.gitModulesPath, shape.gitModules = filepath.ToSlash(rel), content
			}
			return nil
		}
		if name == ".gitattributes" {
			content, n, err := readMarkerFile(path, limits.MaxFileBytes)
			result.ScannedBytes += n
			if result.ScannedBytes > limits.MaxReadBytes {
				result.Truncated = true
				result.TruncatedReason = "read-byte limit reached"
				return stop
			}
			if err == nil && strings.Contains(strings.ToLower(string(content)), "filter=lfs") {
				shape.lfsDeclared = true
				shape.readGitAttributes(filepath.ToSlash(rel), content)
			}
			return nil
		}
		if name == "index.html" && shape.skipStaticIndex(path, rel) {
			return nil
		}
		parent := filepath.Dir(rel)
		if parent == "." {
			parent = ""
		}
		if (dockerfileFileName(name) || composeFileName(name)) && dockerfileOutsideApplication(filepath.ToSlash(rel)) {
			return nil
		}
		if name == "index.php" {
			// A root's own index.php, or the one under its public/ directory,
			// names a PHP application; one deeper (a theme, a plugin) does not.
			switch {
			case parent == "":
			case filepath.Base(parent) == "public":
				parent = filepath.Dir(parent)
				if parent == "." {
					parent = ""
				}
			default:
				return nil
			}
		}
		marker := markers[parent]
		if marker == nil {
			marker = &detectedMarkers{root: parent, pythonFiles: map[string][]byte{}, csprojs: map[string][]byte{}}
			markers[parent] = marker
		}
		readMarker := func(limit int64) ([]byte, bool) {
			content, n, err := readMarkerFile(path, limit)
			result.ScannedBytes += n
			if result.ScannedBytes > limits.MaxReadBytes {
				result.Truncated, result.TruncatedReason = true, "read-byte limit reached"
				return nil, false
			}
			return shape.manifest(filepath.ToSlash(rel), content), err == nil
		}
		if dockerfileFileName(name) {
			if len(marker.dockerfiles) >= 8 {
				return nil
			}
			content, ok := readMarker(limits.MaxFileBytes)
			if result.Truncated {
				return stop
			}
			if ok {
				appendDetectedDockerfile(marker, detectedDockerfile{path: filepath.ToSlash(rel), content: content})
			}
			return nil
		}
		switch name {
		case "package.swift":
			if content, ok := readMarker(256 << 10); ok {
				marker.packageSwift = content
			} else if result.Truncated {
				return stop
			}
		case "compose.yml", "compose.yaml", "docker-compose.yml", "docker-compose.yaml":
			marker.compose = append(marker.compose, rel)
		case "go.mod":
			marker.goMod = rel
			content, n, err := readMarkerFile(path, limits.MaxFileBytes)
			result.ScannedBytes += n
			if err == nil {
				marker.goModContent = content
			}
		case ".go-version":
			content, n, err := readMarkerFile(path, limits.MaxFileBytes)
			result.ScannedBytes += n
			if err == nil {
				marker.goVersionFile = content
			}
		case "bun.lock", "bun.lockb", "package-lock.json", "pnpm-lock.yaml", "yarn.lock":
			marker.lockfiles = append(marker.lockfiles, rel)
		case "manage.py":
			marker.managePy = true
		case "cargo.lock":
			marker.cargoLock = true
		case "gradlew":
			marker.gradlew = true
		case "deno.lock":
			marker.denoLock = true
		case "composer.lock":
			marker.composerLock = true
		case "index.php":
			if filepath.Base(filepath.Dir(rel)) == "public" {
				marker.phpPublicIndex = true
			} else {
				marker.phpIndex = true
			}
		case "composer.json":
			content, ok := readMarker(limits.MaxFileBytes)
			if result.Truncated {
				return stop
			}
			if ok {
				marker.composerJSON = content
			}
		case "cargo.toml", "pom.xml", "build.gradle", "build.gradle.kts", "deno.json", "deno.jsonc":
			content, ok := readMarker(limits.MaxFileBytes)
			if result.Truncated {
				return stop
			}
			if !ok {
				break
			}
			switch name {
			case "cargo.toml":
				marker.cargoToml = content
			case "pom.xml":
				marker.pomXML = content
			case "deno.json", "deno.jsonc":
				marker.denoJSON, marker.denoJSONPath = content, entry.Name()
			default:
				// The Kotlin script wins when both are present, as Gradle's own resolution does.
				if marker.gradleBuild == nil || name == "build.gradle.kts" {
					marker.gradleBuild, marker.gradleBuildPath = content, entry.Name()
				}
			}
		case "rust-toolchain", "rust-toolchain.toml", ".java-version":
			content, ok := readMarker(4096)
			if result.Truncated {
				return stop
			}
			if ok && name == ".java-version" {
				marker.javaVersionFile = content
			} else if ok {
				marker.rustToolchain = content
			}
		case "uv.lock", "poetry.lock":
			// The lock's package names say which framework is installed; a
			// lock too large to read still proves the install is frozen.
			marker.pythonFiles[name] = []byte("locked")
			content, n, err := readMarkerFile(path, limits.MaxFileBytes)
			result.ScannedBytes += n
			if err == nil && result.ScannedBytes <= limits.MaxReadBytes {
				marker.pythonFiles[name] = content
			} else if result.ScannedBytes > limits.MaxReadBytes {
				result.Truncated, result.TruncatedReason = true, "read-byte limit reached"
				return stop
			}
		case "runtime.txt", ".python-version":
			content, n, err := readMarkerFile(path, 4096)
			result.ScannedBytes += n
			if err == nil {
				if name == "runtime.txt" {
					marker.runtimeTxt = content
				} else {
					marker.pythonVersionFile = content
				}
			}
		case "requirements.txt", "pyproject.toml":
			content, n, err := readMarkerFile(path, limits.MaxFileBytes)
			result.ScannedBytes += n
			if err == nil && result.ScannedBytes <= limits.MaxReadBytes {
				marker.pythonFiles[name] = shape.manifest(filepath.ToSlash(rel), content)
			} else if result.ScannedBytes > limits.MaxReadBytes {
				result.Truncated = true
				result.TruncatedReason = "read-byte limit reached"
				return stop
			}
		case "index.html":
			marker.staticFile = rel
		case "angular.json", "procfile":
			content, n, err := readMarkerFile(path, limits.MaxFileBytes)
			result.ScannedBytes += n
			if err == nil && result.ScannedBytes <= limits.MaxReadBytes {
				if name == "procfile" {
					marker.procfile = shape.manifest(filepath.ToSlash(rel), content)
				} else {
					marker.angularJSON = shape.manifest(filepath.ToSlash(rel), content)
				}
			} else if result.ScannedBytes > limits.MaxReadBytes {
				result.Truncated, result.TruncatedReason = true, "read-byte limit reached"
				return stop
			}
		case "package.json":
			content, n, err := readMarkerFile(path, limits.MaxFileBytes)
			result.ScannedBytes += n
			if err == nil && result.ScannedBytes <= limits.MaxReadBytes {
				marker.packageJSON, marker.packagePath = shape.manifest(filepath.ToSlash(rel), content), rel
			} else if result.ScannedBytes > limits.MaxReadBytes {
				result.Truncated = true
				result.TruncatedReason = "read-byte limit reached"
				return stop
			}
		}
		if strings.HasSuffix(name, ".csproj") && len(marker.csprojs) < 8 {
			content, ok := readMarker(limits.MaxFileBytes)
			if result.Truncated {
				return stop
			}
			if ok {
				marker.csprojs[entry.Name()] = content
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
	readiness.walkStopped = result.Truncated
	tree := openDetectionTree(root)
	defer tree.close()
	addSkippedBuildDockerfiles(tree, markers, skippedBuild, limits, &result)

	roots := make([]string, 0, len(markers))
	packageRoots := []string{}
	pythonRoots := []string{}
	goRoots, jvmRoots := []string{}, []string{}
	for candidateRoot, marker := range markers {
		roots = append(roots, candidateRoot)
		if len(marker.packageJSON) > 0 {
			packageRoots = append(packageRoots, filepath.ToSlash(candidateRoot))
		}
		if marker.goMod != "" {
			goRoots = append(goRoots, filepath.ToSlash(candidateRoot))
		}
		if len(marker.pomXML) > 0 || len(marker.gradleBuild) > 0 {
			jvmRoots = append(jvmRoots, filepath.ToSlash(candidateRoot))
		}
		if marker.hasPythonManifest() {
			pythonRoots = append(pythonRoots, filepath.ToSlash(candidateRoot))
		}
	}
	sort.Strings(roots)
	allRoots := make([]string, 0, len(roots))
	for _, candidateRoot := range roots {
		allRoots = append(allRoots, filepath.ToSlash(candidateRoot))
	}
	readNodeInstalls(root, markers)
	for _, candidateRoot := range roots {
		marker := markers[candidateRoot]
		marker.release, marker.releaseEvidence, _ = declaredReleaseCommand(tree, marker)
	}
	for _, candidateRoot := range roots {
		marker := markers[candidateRoot]
		root := filepath.ToSlash(marker.root)
		marker.denoEntries = map[string]bool{}
		for _, entry := range pathsUnderRoot(denoEntryPaths, root, allRoots) {
			marker.denoEntries[entry] = true
		}
		candidates := candidatesForMarkers(marker,
			pathsUnderRoot(schemaPaths, root, packageRoots), pythonEntriesUnderRoot(pythonEntries, root, pythonRoots))
		result.Candidates = append(result.Candidates, candidates...)
	}
	containers, backingOnly := containerCandidates(tree, markers, roots)
	result.Candidates = append(result.Candidates, containers...)
	// A Compose file of backing services is offered as databases beside an
	// application; alone, it is what the operator may mean to deploy.
	if len(result.Candidates) == 0 {
		result.Candidates = append(result.Candidates, backingOnly...)
	}
	// A candidate's variables and databases are its build root's, and a
	// Dockerfile's context can sit above the directory the file is in.
	variablesByRoot := map[string][]DetectedVariable{}
	databasesByRoot := map[string][]DetectedDatabase{}
	for index := range result.Candidates {
		candidateRoot := result.Candidates[index].Root
		if _, done := variablesByRoot[candidateRoot]; !done {
			marker := markers[filepath.FromSlash(candidateRoot)]
			if marker == nil {
				marker = &detectedMarkers{root: candidateRoot, pythonFiles: map[string][]byte{}, csprojs: map[string][]byte{}}
			}
			variablesByRoot[candidateRoot] = scanner.variables(candidateRoot, allRoots)
			databasesByRoot[candidateRoot] = detectDatabases(marker, variablesByRoot[candidateRoot], prismaProviders)
		}
		// The registry credentials a Node install's configuration reads
		// (.npmrc, .yarnrc.yml, bunfig.toml) are the root's variables too,
		// whichever of the root's candidates read them, since the root's
		// environment is described once for all of them.
		for _, variable := range result.Candidates[index].Variables {
			if variable.Step == "install" {
				variablesByRoot[candidateRoot] = withInstallVariables(variablesByRoot[candidateRoot], []DetectedVariable{variable})
			}
		}
	}
	for index := range result.Candidates {
		candidateRoot := result.Candidates[index].Root
		result.Candidates[index].Variables = variablesByRoot[candidateRoot]
		result.Candidates[index].Databases = databasesByRoot[candidateRoot]
	}
	// What only the repository as a whole shows — which roots are examples,
	// tooling or asset pipelines, what other platforms' files declare, what
	// else the source runs — reshapes the candidates before any root's
	// passes read them.
	shapeRun := shapeContext{
		ctx: detectCtx, markers: markers, identity: identity, goSources: goSources, pythonEntries: pythonEntries,
		variables: func(root string) []DetectedVariable { return scanner.variables(root, allRoots) },
		databases: func(root string, variables []DetectedVariable) []DetectedDatabase {
			return detectDatabases(&detectedMarkers{root: root}, variables, prismaProviders)
		},
	}
	shape.shapeCandidates(&result, shapeRun)
	// A root's candidates, recipe and container alike, are refined together
	// against the root's files. State settles first: the schema step it
	// chains into a start command is what readiness budgets a slow start for,
	// and the start command it gives PocketBase is the one network reads a
	// listener from. The environment is described last, against the ports
	// and frameworks the others settled, and it replaces the root's
	// databases, so what the shape pass and state suggested for a candidate
	// is added back after it, and the image facts after that.
	for _, group := range groupCandidatesByRoot(result.Candidates) {
		root := group.root
		marker := markers[filepath.FromSlash(root)]
		if marker == nil {
			marker = &detectedMarkers{root: filepath.FromSlash(root), pythonFiles: map[string][]byte{}, csprojs: map[string][]byte{}}
		}
		marker.attachBuiltDockerfiles(markers, group.candidates)
		suggested := make([][]DetectedDatabase, len(group.candidates))
		for index, candidate := range group.candidates {
			suggested[index] = databasesBeyond(candidate.Databases, databasesByRoot[root])
		}
		rootPythonEntries := pythonEntriesUnderRoot(pythonEntries, root, pythonRoots)
		applyStateDetection(marker, group.candidates, state.forRoot(root, allRoots), variablesByRoot[root])
		refineServing(group.candidates, marker, readiness, root, allRoots)
		network.apply(marker, group.candidates, rootPythonEntries, goRoots, jvmRoots)
		describeRootEnvironment(marker, scanner, prismaProviders, group.candidates)
		for index := range group.candidates {
			candidate := &group.candidates[index]
			if len(suggested[index]) > 0 {
				candidate.Databases = appendDatabases(append([]DetectedDatabase(nil), candidate.Databases...), suggested[index]...)
			}
			pythonDatabaseSuggestions(candidate)
		}
		group.store(result.Candidates)
	}
	annotateImageFacts(tree, markers, result.Candidates)
	for index := range result.Candidates {
		candidate := &result.Candidates[index]
		if candidate.Recipe == "go" {
			// Main packages are read by the recipe's own scan of the module —
			// headers only, under its own bound — not from the files this walk
			// read before its budget ran out, so what detection says about
			// them is what the recipe will decide.
			packages, err := scanGoModule(filepath.Join(root, filepath.FromSlash(candidate.Root)))
			if err != nil {
				candidate.RecipeIssue = recipeRefusalText(err, root)
			} else {
				applyGoModulePackages(candidate, packages, markers[filepath.FromSlash(candidate.Root)])
			}
			if goSources.truncated {
				candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(candidate.Root, "go.mod"),
					Reason: fmt.Sprintf("Go source scan stopped after %d files; the environment and listeners of the rest were not read", goSources.files)})
			}
		}
	}
	// A candidate the recipe would refuse is marked before the ranking
	// chooses among them (recipe_preflight.go).
	applyDetectedRecipeIssues(detectCtx, root, result.Candidates)
	// The frontend and API a split repository pairs, by the ports and
	// profiles the passes settled, and the ranking that selects the
	// application.
	shape.rankDetection(&result, shapeRun)
	refreshNodeInstalls(result.Candidates, markers)
	return result, nil
}

// detectionInterestingName names the files whose content says what a
// directory is. The walk's breadth-first pass reads them before any source.
func detectionInterestingName(name string) bool {
	return name == "package.json" || name == "go.mod" || dockerfileFileName(name) ||
		name == ".go-version" || name == "package.swift" ||
		name == "compose.yml" || name == "compose.yaml" ||
		name == "docker-compose.yml" || name == "docker-compose.yaml" ||
		name == "index.html" || name == ".gitmodules" || name == ".gitattributes" ||
		name == "bun.lock" || name == "bun.lockb" || name == "package-lock.json" ||
		name == "pnpm-lock.yaml" || name == "yarn.lock" || name == "requirements.txt" ||
		name == "uv.lock" || name == "poetry.lock" || name == "pyproject.toml" ||
		name == "angular.json" || name == "procfile" || name == "manage.py" ||
		name == "runtime.txt" || name == ".python-version" ||
		name == "cargo.toml" || name == "cargo.lock" || name == "rust-toolchain" || name == "rust-toolchain.toml" ||
		name == "pom.xml" || name == "build.gradle" || name == "build.gradle.kts" || name == "gradlew" ||
		name == ".java-version" || strings.HasSuffix(name, ".csproj") ||
		name == "deno.json" || name == "deno.jsonc" || name == "deno.lock" ||
		name == "composer.json" || name == "composer.lock" || name == "index.php"
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

func (m *detectedMarkers) hasPythonManifest() bool {
	for _, name := range []string{"requirements.txt", "pyproject.toml", "uv.lock", "poetry.lock"} {
		if _, ok := m.pythonFiles[name]; ok {
			return true
		}
	}
	return false
}

// pythonEntriesUnderRoot keeps the entries that belong to one Python root,
// re-based on it, leaving out those a nested Python root owns.
func pythonEntriesUnderRoot(entries []pythonEntry, root string, pythonRoots []string) []pythonEntry {
	paths := make([]string, 0, len(entries))
	byPath := map[string][]byte{}
	for _, entry := range entries {
		paths = append(paths, entry.path)
		byPath[entry.path] = entry.content
	}
	prefix := rootPrefix(root)
	result := []pythonEntry{}
	for _, relative := range pathsUnderRoot(paths, root, pythonRoots) {
		result = append(result, pythonEntry{path: relative, content: byPath[prefix+relative]})
	}
	return result
}

func candidatesForMarkers(marker *detectedMarkers, schemaPaths []string, pythonEntries []pythonEntry) []DetectedCandidate {
	var result []DetectedCandidate
	rootLabel := marker.root
	if rootLabel == "" {
		rootLabel = "."
	}
	if len(marker.packageJSON) > 0 && !marker.phpOwnsAssets() {
		result = append(result, packageCandidate(marker, schemaPaths)...)
	}
	if marker.goMod != "" {
		candidate := DetectedCandidate{
			Name: "Go service in " + rootLabel, Profile: ProfileService, Confidence: ConfidenceHigh,
			Framework: "go", Recipe: "go",
			Evidence: []DetectionEvidence{{Path: marker.goMod, Reason: "Go module definition"}},
			// The recipe finds the executable itself — it refuses a module that
			// does not have exactly one main package — and starts the binary it
			// writes; preflight asks a service for no readiness gate. The port is
			// all that was ever owed here, and an unset port asks for it on the
			// screen that has the field.
			NeedsDecision: []string{},
		}
		// Which toolchain builds the module is a setting, so a version the
		// automatic choice cannot satisfy is left to preflight, which judges
		// it against the plan's own Go version rather than this default.
		candidate.GoMinimumVersion = goModuleMinimum(marker.goModContent)
		candidate.GoToolchain = goModuleToolchain(marker.goModContent)
		candidate.GoVersionFile = goVersionFileValue(marker.goVersionFile)
		if version, err := chooseGoRecipeVersion("", string(marker.goVersionFile), marker.goModContent); err == nil {
			candidate.GoVersion = version
		}
		result = append(result, newDetectedCandidate(marker.root, BuildRecipe, candidate))
	}
	if marker.hasPythonManifest() {
		result = append(result, newDetectedCandidate(marker.root, BuildRecipe, pythonCandidate(marker, pythonEntries, rootLabel)))
	}
	if len(marker.cargoToml) > 0 {
		result = append(result, newDetectedCandidate(marker.root, BuildRecipe, rustCandidate(marker, rootLabel)))
	}
	if len(marker.pomXML) > 0 || len(marker.gradleBuild) > 0 {
		result = append(result, newDetectedCandidate(marker.root, BuildRecipe, javaCandidate(marker, rootLabel)))
	}
	if len(marker.csprojs) > 0 {
		result = append(result, newDetectedCandidate(marker.root, BuildRecipe, dotnetCandidate(marker, rootLabel)))
	}
	if len(marker.denoJSON) > 0 {
		result = append(result, newDetectedCandidate(marker.root, BuildRecipe, denoCandidate(marker, rootLabel)))
	}
	if len(marker.composerJSON) > 0 || marker.phpIndex || marker.phpPublicIndex {
		result = append(result, newDetectedCandidate(marker.root, BuildRecipe, phpCandidate(marker, rootLabel)))
	}
	if marker.staticFile != "" && len(marker.packageJSON) == 0 {
		// There is no public directory left to confirm. A marker's root is the
		// directory its files were found in, so this candidate's root is
		// already the one holding the index.html — at the top of the checkout,
		// under public/, under docs/, each is its own candidate — and
		// renderStaticDockerfile serves exactly that root when the output
		// directory is empty, which is how this candidate is planned.
		result = append(result, newDetectedCandidate(marker.root, BuildStatic, DetectedCandidate{
			Name: "Static site in " + rootLabel, Profile: ProfileStatic, Confidence: ConfidenceMedium,
			Port:          80,
			Evidence:      []DetectionEvidence{{Path: marker.staticFile, Reason: "static HTML entry point"}},
			NeedsDecision: []string{},
		}))
	}
	return result
}

func packageCandidate(marker *detectedMarkers, schemaPaths []string) []DetectedCandidate {
	var manifest nodeManifest
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
	install := marker.node
	if install == nil {
		install = &nodeInstallSource{facts: nodeInstallFacts{signals: map[string][]string{}}}
	}
	facts := install.facts
	candidate.PackageManagers = facts.lockfileManagers()
	candidate.Lockfiles = facts.detectedLockfiles()
	candidate.NodeVersion = nodeReleaseFor(facts).label()
	// Whether the manager is settled caps the confidence the framework
	// reading may claim: a Next.js match does not make competing lockfiles
	// any less of a question.
	settled := true
	chosen, resolveErr := resolveNodeManager(facts, "")
	if resolveErr == nil {
		candidate.PackageManager = chosen.manager
		evidencePath := marker.packagePath
		if chosen.reading != nil {
			evidencePath = joinRoot(install.context, chosen.reading.Path)
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: evidencePath, Reason: boundedEvidenceSentence(chosen.reason)})
	} else {
		settled = false
		candidate.Confidence = ConfidenceLow
		paths := []string{}
		for _, reading := range facts.readings {
			paths = append(paths, reading.Path)
		}
		candidate.NeedsDecision = append(candidate.NeedsDecision, "choose the package manager: competing lockfiles "+strings.Join(paths, ", "))
	}
	if install.context != install.dir {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{
			Path: joinRoot(install.context, "package.json"), Reason: "installed from the workspace lockfile at " + rootLabelOf(install.context),
		})
	}
	for _, superseded := range facts.superseded {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(install.context, "package.json"), Reason: superseded})
	}
	// The runner has to be the one the lockfile names. The build recipe picks
	// its toolchain from that lockfile, and a runner the image lacks was a
	// build that died on `<runner>: not found` after a successful install,
	// with nothing in the configuration screen saying which field was wrong.
	// With competing lockfiles nothing resolves yet, and npm is the guess
	// that fails most legibly: the build refuses before any command runs.
	runner := candidate.PackageManager
	if runner == "" {
		runner = "npm"
	}
	framework := matchNodeFramework(manifest)
	files := nodeRootFiles{angularJSON: marker.angularJSON, procfile: marker.procfile}
	var resolution nodeFrameworkResolution
	if framework != nil {
		resolution = framework.resolve(manifest, files, runner)
	}
	// A Procfile is the one place a repository declares how it is served
	// rather than leaving it to be inferred, so it outranks a start script
	// and a framework default alike. It is only ever a server command: a
	// site framework's build is still served by nginx.
	procfileWeb := procfileProcess(marker.procfile, "web")
	if procfileWeb != "" && rejectPlanSecretLiteral("Procfile web process", procfileWeb) != nil {
		procfileWeb = ""
	}
	inputs := nodeCommandInputs{
		manifest: manifest, files: files, framework: framework, procfileWeb: procfileWeb,
		schema: detectSchemaTool(dependencies, schemaPaths, facts.prisma), release: marker.release,
	}
	// The name becomes part of a command, so it has to be a package name.
	if install.context != install.dir && facts.workspaceTurbo && nodePackageNameRE.MatchString(manifest.Name) && nodeHasWorkspaceDependency(manifest, facts.workspacePackages) {
		inputs.turboFilter = manifest.Name
	}
	candidate.BuildCommand, candidate.StartCommand = inputs.commands(runner)
	bareStart := inputs.start(runner)
	buildScript := "build"
	if resolution.BuildScript != "" {
		buildScript = resolution.BuildScript
	}
	if command := manifest.Scripts[buildScript]; command != "" {
		candidate.Evidence = append(candidate.Evidence,
			DetectionEvidence{Path: marker.packagePath, Reason: buildScript + " script: " + boundedEvidence(command)})
	}
	if framework == nil {
		candidate.Framework = matchNodeServerLibrary(manifest)
		entry := nodeMainEntry(manifest)
		switch {
		case procfileWeb != "":
			candidate.Profile, candidate.Port = ProfileWeb, 3000
			candidate.Evidence = append(candidate.Evidence,
				DetectionEvidence{Path: filepath.ToSlash(filepath.Join(marker.root, "Procfile")), Reason: "web process: " + boundedEvidence(procfileWeb)})
		case manifest.Scripts["start"] != "":
			candidate.Profile, candidate.Port = ProfileWeb, 3000
			candidate.Evidence = append(candidate.Evidence,
				DetectionEvidence{Path: marker.packagePath, Reason: "start script: " + boundedEvidence(manifest.Scripts["start"])})
		case entry != "":
			candidate.Evidence = append(candidate.Evidence,
				DetectionEvidence{Path: marker.packagePath, Reason: "main entry: " + entry})
			// A main file says how the package runs, not whether anything
			// listens; an HTTP library in its manifest does.
			if candidate.Framework != "" {
				candidate.Profile, candidate.Port = ProfileWeb, 3000
			} else {
				candidate.Confidence = ConfidenceLow
				candidate.NeedsDecision = append(candidate.NeedsDecision, "confirm whether this package serves HTTP (web application) or runs as a worker")
			}
		default:
			candidate.NeedsDecision = append(candidate.NeedsDecision, "choose a start command or static output")
		}
	} else {
		candidate.Framework = framework.Name
		candidate.Confidence = ConfidenceHigh
		if resolution.Confidence != "" {
			candidate.Confidence = resolution.Confidence
		}
		for _, dependency := range framework.Dependencies {
			if manifest.has(dependency) {
				candidate.Evidence = append(candidate.Evidence, DetectionEvidence{
					Path: marker.packagePath, Reason: framework.Label + " dependency " + boundedEvidence(manifest.version(dependency)),
				})
				break
			}
		}
		candidate.NeedsDecision = append(candidate.NeedsDecision, resolution.Decisions...)
		if resolution.Output != "" {
			candidate.Profile, candidate.OutputDirectory, candidate.Port = ProfileStatic, resolution.Output, 80
			candidate.SPAFallback = resolution.SPA
		} else {
			candidate.Profile, candidate.Port = ProfileWeb, resolution.Port
			switch {
			case procfileWeb != "":
				candidate.Evidence = append(candidate.Evidence,
					DetectionEvidence{Path: filepath.ToSlash(filepath.Join(marker.root, "Procfile")), Reason: "web process: " + boundedEvidence(procfileWeb)})
			default:
				for _, script := range resolution.StartScripts {
					if command := manifest.Scripts[script]; command != "" {
						candidate.Evidence = append(candidate.Evidence,
							DetectionEvidence{Path: marker.packagePath, Reason: script + " script: " + boundedEvidence(command)})
						break
					}
				}
			}
		}
		if candidate.BuildCommand == "" && (resolution.Output != "" || resolution.Entry != "") {
			candidate.Confidence = ConfidenceLow
			candidate.NeedsDecision = append(candidate.NeedsDecision, "add a "+buildScript+" script that runs the "+framework.Label+" build")
		}
	}
	if !settled {
		candidate.readingConfidence = candidate.Confidence
		candidate.Confidence = ConfidenceLow
	}
	if tool := inputs.schema; tool != nil {
		candidate.SchemaTool = tool.Tool.Name
		candidate.Evidence = append(candidate.Evidence, tool.Evidence)
		switch {
		case inputs.schemaInStart(bareStart):
			candidate.SchemaInStart = true
			candidate.Evidence[len(candidate.Evidence)-1].Reason = tool.Tool.Label + " schema applied by the package's own start script"
		case tool.Command != "" && inputs.releaseAppliesSchema():
			candidate.SchemaCommand, candidate.SchemaInRelease = tool.Command, true
			reason, _, _ := strings.Cut(tool.Evidence.Reason, "; the start command")
			candidate.Evidence[len(candidate.Evidence)-1].Reason = reason + "; the release command applies it before each release, so the start command does not"
		case tool.Command == "":
			candidate.NeedsDecision = append(candidate.NeedsDecision, "choose how "+tool.Tool.Label+" migrations run before the database is used")
		default:
			candidate.SchemaCommand = tool.Command
		}
	}
	candidate.NodeInstalls = facts.detectedInstalls(candidate.PackageManager, false, inputs.commands)
	candidate.NodeBuild = detectedNodeBuild(facts, candidate.Framework, candidate.BuildCommand)
	candidate.Variables = facts.registry
	if _, err := validateNodeRecipeContent(marker.packageJSON, files,
		BuildPlanConfig{Method: BuildRecipe, Recipe: "node", PackageManager: runner, BuildCommand: candidate.BuildCommand, StartCommand: candidate.StartCommand, OutputDirectory: candidate.OutputDirectory}); err != nil {
		candidate.RecipeIssue = err.Error()
	}
	return []DetectedCandidate{newDetectedCandidate(marker.root, BuildRecipe, candidate)}
}

// nodeCommandInputs is what the detected build and start commands are made
// from, apart from the runner. Detection proposes commands for the manager
// that resolved, and records them for every other manager as well, from
// this one function, so a manager chosen later swaps whole commands.
type nodeCommandInputs struct {
	manifest    nodeManifest
	files       nodeRootFiles
	framework   *nodeFramework
	procfileWeb string
	schema      *detectedSchemaTool
	// turboFilter is the workspace member Turborepo builds, with the
	// workspace packages it depends on.
	turboFilter string
	// release is the repository's declared release command.
	release string
}

func (in nodeCommandInputs) schemaInStart(start string) bool {
	return in.schema != nil && (in.schema.Tool.applied(in.manifest.Scripts["start"]) || in.schema.Tool.applied(start))
}

func (in nodeCommandInputs) releaseAppliesSchema() bool {
	return in.schema != nil && releaseAppliesSchema(&in.schema.Tool, in.release, in.manifest.Scripts)
}

func (in nodeCommandInputs) commands(runner string) (string, string) {
	var resolution nodeFrameworkResolution
	if in.framework != nil {
		resolution = in.framework.resolve(in.manifest, in.files, runner)
	}
	buildScript := "build"
	if resolution.BuildScript != "" {
		buildScript = resolution.BuildScript
	}
	build := ""
	if in.manifest.Scripts[buildScript] != "" {
		build = runner + " run " + buildScript
		if in.turboFilter != "" {
			build = nodeExecRunner(runner) + " turbo run " + buildScript + " --filter=" + in.turboFilter + "..."
		}
	}
	start := in.start(runner)
	if in.schema != nil && in.schema.Command != "" && start != "" && !in.schemaInStart(start) && !in.releaseAppliesSchema() {
		start = nodeExecRunner(runner) + " " + in.schema.Command + " && " + start
	}
	return build, start
}

// start is the served command before any schema step is chained in front.
func (in nodeCommandInputs) start(runner string) string {
	var resolution nodeFrameworkResolution
	if in.framework != nil {
		resolution = in.framework.resolve(in.manifest, in.files, runner)
	}
	start := ""
	switch {
	case in.framework == nil && in.procfileWeb != "":
		start = in.procfileWeb
	case in.framework == nil && in.manifest.Scripts["start"] != "":
		start = runner + " run start"
	case in.framework == nil:
		if entry := nodeMainEntry(in.manifest); entry != "" {
			start = nodeEntryCommand(runner, entry)
		}
	case resolution.Output != "":
	case in.procfileWeb != "":
		start = in.procfileWeb
	default:
		start = resolution.Start
		for _, script := range resolution.StartScripts {
			if in.manifest.Scripts[script] != "" {
				start = runner + " run " + script
				break
			}
		}
	}
	return start
}

// nodeHasWorkspaceDependency says whether a member depends on another
// package of its workspace, which may need its own build first: through
// pnpm, Bun and Berry's workspace: protocol, or by a sibling's name, which
// is how npm and Yarn 1 workspaces refer to one.
func nodeHasWorkspaceDependency(manifest nodeManifest, siblings []string) bool {
	for _, kind := range []map[string]string{manifest.Dependencies, manifest.DevDependencies} {
		for name, spec := range kind {
			if strings.HasPrefix(spec, "workspace:") || slices.Contains(siblings, name) {
				return true
			}
		}
	}
	return false
}

// boundedEvidenceSentence keeps a detection sentence within the evidence
// bounds without the credential filter boundedEvidence applies to script
// text: this text is assembled from lockfile and package names.
func boundedEvidenceSentence(value string) string {
	if len(value) > 480 {
		return value[:477] + "..."
	}
	return value
}

// nodeMainEntry is the file package.json says the package runs from, when it
// is a source file inside the package.
func nodeMainEntry(manifest nodeManifest) string {
	for _, entry := range []string{manifest.Main, manifest.Module} {
		entry = strings.TrimPrefix(strings.TrimSpace(entry), "./")
		if entry == "" || !safeRelativePath(entry) {
			continue
		}
		switch strings.ToLower(filepath.Ext(entry)) {
		case ".js", ".mjs", ".cjs", ".ts", ".mts":
			return filepath.ToSlash(entry)
		}
	}
	return ""
}

// nodeEntryCommand runs a main file through the runtime the lockfile locks
// to: Bun executes TypeScript itself, and Node 22 strips erasable types.
func nodeEntryCommand(runner, entry string) string {
	if runner == "bun" {
		return "bun " + entry
	}
	return "node " + entry
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

// candidateGroup is one root's candidates, copied out of the detection so
// the per-root passes can refine them together, and where each came from.
type candidateGroup struct {
	root       string
	indexes    []int
	candidates []DetectedCandidate
}

// groupCandidatesByRoot splits candidates by root, in root order, keeping
// each group in the detection's own order.
func groupCandidatesByRoot(candidates []DetectedCandidate) []candidateGroup {
	byRoot := map[string]*candidateGroup{}
	roots := []string{}
	for index, candidate := range candidates {
		group := byRoot[candidate.Root]
		if group == nil {
			group = &candidateGroup{root: candidate.Root}
			byRoot[candidate.Root] = group
			roots = append(roots, candidate.Root)
		}
		group.indexes = append(group.indexes, index)
		group.candidates = append(group.candidates, candidate)
	}
	sort.Strings(roots)
	groups := make([]candidateGroup, 0, len(roots))
	for _, root := range roots {
		groups = append(groups, *byRoot[root])
	}
	return groups
}

// databasesBeyond are the databases a candidate carries that its root's own
// detection did not suggest: another platform's file's, an ecosystem's.
func databasesBeyond(carried, root []DetectedDatabase) []DetectedDatabase {
	var beyond []DetectedDatabase
	for _, database := range carried {
		known := false
		for _, suggested := range root {
			known = known || (suggested.Engine == database.Engine && suggested.Variable == database.Variable)
		}
		if !known {
			beyond = append(beyond, database)
		}
	}
	return beyond
}

// store writes the group's refined candidates back where they came from.
func (g candidateGroup) store(candidates []DetectedCandidate) {
	for position, index := range g.indexes {
		candidates[index] = g.candidates[position]
	}
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
