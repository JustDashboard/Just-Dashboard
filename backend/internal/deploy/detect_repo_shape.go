package deploy

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Repository shape is what detection learns about a checkout as a whole
// rather than about one directory: which roots are the application and which
// are its examples, documentation, tooling or asset pipeline; what other
// platforms' deployment files already declare; which processes besides the
// web server the source runs; and whether the repository is a service at all.
// Every fact is read from files as bounded data — nothing a repository holds
// is executed on the dashboard host.

// DetectionSetAside is a directory or file detection recognised and
// deliberately did not offer as a candidate, and why.
type DetectionSetAside struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	// Kind is one of the setAsideKinds; the not-a-service kinds (library,
	// mobile-app, desktop-app, …) let an empty result say what the
	// repository is instead of "nothing was detected".
	Kind string `json:"kind"`
}

// DetectionAlternative is a better way to run the same application than
// building this checkout: a reviewed template, or the image its own project
// publishes.
type DetectionAlternative struct {
	Kind     string `json:"kind"` // template or image
	Ref      string `json:"ref"`
	Label    string `json:"label"`
	Evidence string `json:"evidence"`
}

// DetectedProcess is a process the source runs besides its main one: a
// queue worker, a scheduler, a release command, a second server.
type DetectedProcess struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"` // worker, scheduler, release or web
	Command string `json:"command,omitempty"`
	Source  string `json:"source"`
	Reason  string `json:"reason"`
}

// DetectedServerlessCode is server code written for a hosting platform's
// function or edge runtime, which a container build does not run.
type DetectedServerlessCode struct {
	Platform string   `json:"platform"`
	Paths    []string `json:"paths"`
	Entry    string   `json:"entry,omitempty"`
	// Blocking says the candidate cannot work without it: a Worker entry
	// that serves the application's API, not a few optional functions.
	Blocking bool `json:"blocking,omitempty"`
}

// ImportCaseMismatch is an import that resolves only on a case-insensitive
// disk: it builds on the author's macOS or Windows machine and fails on the
// Linux build.
type ImportCaseMismatch struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Specifier string `json:"specifier"`
	Actual    string `json:"actual"`
	Language  string `json:"language"` // javascript or php
}

// GitSubmodule is one entry of .gitmodules as detection judged it.
type GitSubmodule struct {
	Path string `json:"path"`
	// SameSource says the URL is relative to the repository or on its own
	// host, so the source's own access fetches it and nobody needs asking.
	SameSource bool `json:"sameSource"`
}

var setAsideKinds = map[string]bool{
	"tooling": true, "virtualenv": true, "dependencies": true, "template": true, "static-files": true,
	"pages": true, "hosted-functions": true, "asset-pipeline": true, "decoy": true, "depth": true,
	"library": true, "cli": true, "mobile-app": true, "desktop-app": true, "notebook": true,
	"editor-extension": true, "browser-extension": true, "github-action": true, "windows-only": true,
}

// notDeployableKinds is the closed set of DetectedCandidate.NotDeployable.
var notDeployableKinds = map[string]string{
	"library":           "a library",
	"cli":               "a command-line tool",
	"editor-extension":  "an editor extension",
	"browser-extension": "a browser extension",
	"github-action":     "a GitHub Action",
	"desktop-app":       "a desktop application",
	"mobile-app":        "a mobile application",
	"notebook":          "a set of notebooks",
	"windows-only":      "a Windows-only program",
}

// repoShapeScan accumulates what the walk sees that no single root's markers
// hold. Its maps are bounded; past a bound a fact is simply not recorded,
// which only ever costs a warning, never a wrong plan.
type repoShapeScan struct {
	root     string
	maxDepth int
	setAside []DetectionSetAside
	seen     map[string]bool
	// roots holds the repository-shape files each root carries, keyed by a
	// stable name ("gemfile", "fly.toml", ".do/app.yaml").
	roots map[string]*shapeRoot
	// files records every file path under the detection root (lower-cased
	// to the paths spelled that way), so imports can be resolved without
	// touching the disk again.
	files          map[string]bool
	filesLower     map[string][]string
	filesTruncated bool
	gitModules     []byte
	gitModulesPath string
	lfsPatterns    []lfsPattern
	lfsFiles       []string
	lfsCount       int
	notebooks      map[string]int
	rustBinaries   map[string]bool
	functionFiles  []string
	pythonProcess  []pythonEntry
	workflows      map[string][]byte
	depthManifests []string
	manifestsBOM   []string
	templatedIndex []string
	lfsDeclared    bool
	bytes          int64
	sources        *sourceShapeScan
}

type shapeRoot struct {
	files map[string][]byte
}

const (
	shapeMaxFilePaths  = 60_000
	shapeMaxSetAside   = 48
	shapeMaxCandidates = 64
	// shapeMaxBytes bounds what repository-shape files may read, apart from
	// detection's own byte limit: these files refine a plan and are never a
	// reason to call a scan truncated.
	shapeMaxBytes = 4 << 20
)

func newRepoShapeScan(root string, limits DetectionLimits) *repoShapeScan {
	return &repoShapeScan{
		root: root, maxDepth: limits.MaxDepth, seen: map[string]bool{}, roots: map[string]*shapeRoot{},
		files: map[string]bool{}, filesLower: map[string][]string{}, notebooks: map[string]int{},
		rustBinaries: map[string]bool{}, workflows: map[string][]byte{}, sources: newSourceShapeScan(),
	}
}

func (s *repoShapeScan) addSetAside(item DetectionSetAside) {
	key := item.Kind + "\x00" + item.Path
	if s.seen[key] || len(s.setAside) >= shapeMaxSetAside {
		return
	}
	s.seen[key] = true
	s.setAside = append(s.setAside, item)
}

func (s *repoShapeScan) rootFiles(root string) *shapeRoot {
	entry := s.roots[root]
	if entry == nil {
		entry = &shapeRoot{files: map[string][]byte{}}
		s.roots[root] = entry
	}
	return entry
}

// file returns a repository-shape file recorded at root.
func (s *repoShapeScan) file(root, key string) ([]byte, bool) {
	entry := s.roots[root]
	if entry == nil {
		return nil, false
	}
	content, ok := entry.files[key]
	return content, ok
}

// setAsideDirectory prunes directories that are never the application, and
// says why when the reason is worth reading.
func (s *repoShapeScan) setAsideDirectory(path, rel, name string) bool {
	rel = filepath.ToSlash(rel)
	lower := strings.ToLower(name)
	if reason, ok := detectionToolingDirectories[lower]; ok {
		switch lower {
		case ".devcontainer":
			for _, file := range []string{"Dockerfile", "Containerfile"} {
				if regularExists(path, file) {
					s.addSetAside(DetectionSetAside{Path: rel + "/" + file, Reason: reason, Kind: "tooling"})
					break
				}
			}
		case ".github":
			if rel == ".github" {
				s.readWorkflows(path)
			}
		}
		return true
	}
	parent := filepath.Dir(path)
	if lower == "deps" && regularExists(parent, "mix.exs") {
		return true
	}
	if lower == "obj" && len(listContainedFiles(parent, ".csproj")) > 0 {
		return true
	}
	if virtualenvDirectory(path) {
		s.addSetAside(DetectionSetAside{
			Path: rel, Kind: "virtualenv",
			Reason: "committed virtualenv " + rel + "/ ignored; it is copied into the build context",
		})
		return true
	}
	return false
}

// depthPruned records a manifest the depth bound kept detection from reading,
// so the scan can say it stopped short instead of quietly offering less.
// Examples and fixtures nest deeply by nature and are not worth the warning.
func (s *repoShapeScan) depthPruned(path, rel string) {
	rel = filepath.ToSlash(rel)
	if decoySegment(rel) != "" || len(s.depthManifests) >= 8 {
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := strings.ToLower(entry.Name())
		if entry.Type().IsRegular() && (detectionInterestingName(name) && name != "index.html" && name != ".gitattributes") {
			s.depthManifests = append(s.depthManifests, rel+"/"+entry.Name())
			return
		}
	}
}

// observeFile records what a file's presence says, without reading it.
func (s *repoShapeScan) observeFile(rel string, entry fs.DirEntry) {
	rel = filepath.ToSlash(rel)
	if len(s.files) < shapeMaxFilePaths {
		s.files[rel] = true
		lower := strings.ToLower(rel)
		s.filesLower[lower] = append(s.filesLower[lower], rel)
	} else {
		s.filesTruncated = true
	}
	if !entry.Type().IsRegular() {
		return
	}
	lowerName := strings.ToLower(entry.Name())
	directory := path.Dir(rel)
	if directory == "." {
		directory = ""
	}
	if s.lfsTracked(rel) {
		s.lfsCount++
		if len(s.lfsFiles) < 256 {
			s.lfsFiles = append(s.lfsFiles, rel)
		}
	}
	switch {
	case strings.HasSuffix(lowerName, ".ipynb"):
		s.notebooks[directory]++
	case strings.HasSuffix(lowerName, ".rs") && (strings.HasSuffix(rel, "src/main.rs") || strings.Contains("/"+rel, "/src/bin/")):
		crate := rel[:strings.LastIndex(rel, "src/")]
		s.rustBinaries[strings.TrimSuffix(crate, "/")] = true
	}
	if len(s.functionFiles) < 256 && functionFile(rel) {
		s.functionFiles = append(s.functionFiles, rel)
	}
}

// functionFile is a file under a directory a hosting platform deploys as
// functions: api/ (Vercel), netlify/functions/, supabase/functions/,
// functions/ (Firebase, Cloudflare Pages).
func functionFile(rel string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".js", ".ts", ".mjs", ".cjs", ".mts", ".jsx", ".tsx", ".go", ".py":
	default:
		return false
	}
	segments := strings.Split(rel, "/")
	for index, segment := range segments[:len(segments)-1] {
		switch segment {
		case "api", "functions":
			return true
		case "netlify", "supabase":
			if index+1 < len(segments)-1 && segments[index+1] == "functions" {
				return true
			}
		}
	}
	return false
}

// repoShapeFileName says whether the breadth-first pass should visit a name
// for this file's sake.
func repoShapeFileName(name string) bool {
	if _, ok := repoShapeFiles[name]; ok {
		return true
	}
	for _, suffix := range []string{".cabal", ".nimble", ".fsproj"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// repoShapeFiles are read as data at the root that holds them, with the
// byte bound for each; zero records presence only.
var repoShapeFiles = map[string]int64{
	// Ecosystems without an automatic recipe, and the asset pipelines of
	// the ones that own a package.json.
	"gemfile": 64 << 10, "gemfile.lock": 256 << 10, "mix.exs": 64 << 10, "config.ru": 16 << 10,
	"stack.yaml": 16 << 10, "package.yaml": 16 << 10, "shard.yml": 16 << 10, "build.zig": 16 << 10,
	"build.zig.zon": 16 << 10, "package.swift": 64 << 10, "pubspec.yaml": 64 << 10, "build.sbt": 64 << 10,
	"project.clj": 64 << 10, "deps.edn": 64 << 10, "gleam.toml": 16 << 10, "description": 16 << 10,
	"renv.lock": 0, "app.r": 16 << 10, "ui.r": 0, "server.r": 0, "plumber.r": 0, "dune-project": 16 << 10,
	"cpanfile": 16 << 10, "cmakelists.txt": 16 << 10, "meson.build": 16 << 10, "rebar.config": 16 << 10,
	"elm.json": 16 << 10, "hugo.toml": 16 << 10, "hugo.yaml": 16 << 10, "hugo.json": 16 << 10,
	"_config.yml": 16 << 10, "mkdocs.yml": 16 << 10, "artisan": 0,
	// Other platforms' deployment manifests.
	"fly.toml": 64 << 10, "render.yaml": 64 << 10, "railway.json": 64 << 10, "railway.toml": 64 << 10,
	"app.json": 64 << 10, "nixpacks.toml": 64 << 10, "netlify.toml": 64 << 10, "vercel.json": 64 << 10,
	"heroku.yml": 64 << 10, "aptfile": 16 << 10, ".readthedocs.yaml": 16 << 10, ".readthedocs.yml": 16 << 10,
	"_redirects": 64 << 10, "app.yaml": 64 << 10, "deploy.yml": 64 << 10, "readme.md": 8 << 10,
	// Serverless, edge and non-service shapes.
	"wrangler.toml": 64 << 10, "wrangler.json": 64 << 10, "wrangler.jsonc": 64 << 10, "firebase.json": 64 << 10,
	"action.yml": 16 << 10, "action.yaml": 16 << 10, "manifest.json": 64 << 10, "wails.json": 16 << 10,
	"tauri.conf.json": 64 << 10,
	"vite.config.js":  64 << 10, "vite.config.ts": 64 << 10, "vite.config.mjs": 64 << 10, "vite.config.mts": 64 << 10,
	"vite.config.cjs": 64 << 10,
	// The files a Python project conventionally defines its Celery app or
	// workers in, beyond the application entry points.
	"celery.py": 64 << 10, "celery_app.py": 64 << 10, "tasks.py": 64 << 10, "worker.py": 64 << 10,
}

// shapeFileTarget decides the root a repository-shape file belongs to and the
// key it is kept under, or false when this copy of the name means nothing.
func shapeFileTarget(rel, lowerName string, depth int) (root, key string, limit int64, ok bool) {
	rel = filepath.ToSlash(rel)
	directory := path.Dir(rel)
	if directory == "." {
		directory = ""
	}
	parentName := path.Base(directory)
	grandparent := path.Dir(directory)
	if grandparent == "." {
		grandparent = ""
	}
	limit, known := repoShapeFiles[lowerName]
	if !known {
		for _, suffix := range []string{".cabal", ".nimble", ".fsproj"} {
			if strings.HasSuffix(lowerName, suffix) {
				return directory, suffix, 16 << 10, true
			}
		}
		return "", "", 0, false
	}
	switch lowerName {
	case "app.yaml":
		// DigitalOcean's app spec; any other app.yaml is somebody else's.
		if parentName != ".do" {
			return "", "", 0, false
		}
		return grandparent, ".do/app.yaml", limit, true
	case "deploy.yml":
		// Kamal's config/deploy.yml, the one Rails 8 generates.
		if parentName != "config" {
			return "", "", 0, false
		}
		return grandparent, "config/deploy.yml", limit, true
	case "tauri.conf.json":
		if parentName != "src-tauri" {
			return "", "", 0, false
		}
		return grandparent, "src-tauri/tauri.conf.json", limit, true
	case "readme.md":
		if depth != 1 {
			return "", "", 0, false
		}
	case "celery.py", "celery_app.py", "tasks.py", "worker.py":
		if depth > 4 || decoySegment(directory) != "" {
			return "", "", 0, false
		}
		return directory, rel, limit, true
	}
	return directory, lowerName, limit, true
}

// readFile reads a repository-shape file under this scan's own budget.
func (s *repoShapeScan) readFile(absolute, root, key string, limit int64) {
	if limit == 0 {
		s.recordFile(root, key, nil)
		return
	}
	if s.bytes >= shapeMaxBytes {
		return
	}
	content, n, err := readDetectionFile(absolute, limit)
	s.bytes += n
	if err == nil {
		s.recordFile(root, key, content)
	}
}

// manifest returns a detection manifest's text without a byte-order mark,
// remembering which ones carried one.
func (s *repoShapeScan) manifest(rel string, content []byte) []byte {
	text, bom := decodeManifest(content)
	if bom {
		s.markBOM(rel)
	}
	return text
}

// recordFile keeps a repository-shape file's content (or its presence).
func (s *repoShapeScan) recordFile(root, key string, content []byte) {
	text, bom := decodeManifest(content)
	if bom {
		s.markBOM(joinRoot(root, key))
	}
	if strings.HasSuffix(key, ".py") {
		if len(s.pythonProcess) < 16 {
			s.pythonProcess = append(s.pythonProcess, pythonEntry{path: key, content: text})
		}
		return
	}
	s.rootFiles(root).files[key] = text
}

func (s *repoShapeScan) markBOM(rel string) {
	if len(s.manifestsBOM) < 16 {
		s.manifestsBOM = append(s.manifestsBOM, rel)
	}
}

// readWorkflows keeps the repository's GitHub workflows, which say whether
// the project publishes its own container image.
func (s *repoShapeScan) readWorkflows(githubDir string) {
	directory := filepath.Join(githubDir, "workflows")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := strings.ToLower(entry.Name())
		if !entry.Type().IsRegular() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}
		if len(s.workflows) >= 16 {
			return
		}
		if content, _, err := readDetectionFile(filepath.Join(directory, entry.Name()), 64<<10); err == nil {
			s.workflows[".github/workflows/"+entry.Name()] = content
		}
	}
}

// Path segments that hold a repository's examples, tests and demos rather
// than its application: a candidate under one of them is offered, never
// chosen over the application itself.
var decoySegments = map[string]string{
	"example": "an example", "examples": "an example", "_examples": "an example",
	"demo": "a demo", "demos": "a demo", "sample": "a sample", "samples": "a sample",
	"fixture": "a test fixture", "fixtures": "a test fixture", "__fixtures__": "a test fixture",
	"testdata": "test data", "test": "a test", "tests": "a test", "__tests__": "a test", "spec": "a test",
	"e2e": "an end-to-end test", "cypress": "an end-to-end test", "playwright": "an end-to-end test",
	"playground": "a playground", "playgrounds": "a playground", "sandbox": "a sandbox", "sandboxes": "a sandbox",
	"benchmark": "a benchmark", "benchmarks": "a benchmark", "bench": "a benchmark",
	"templates": "a project template", "starters": "a starter template", "boilerplates": "a starter template",
}

// decoySegment names what the first decoy segment of a relative path makes
// it, or "" when the path is none of those.
func decoySegment(rel string) string {
	for _, segment := range strings.Split(filepath.ToSlash(rel), "/") {
		lower := strings.ToLower(segment)
		if label, ok := decoySegments[lower]; ok {
			return label
		}
		if strings.HasPrefix(lower, "storybook") || strings.HasPrefix(lower, ".storybook") {
			return "a Storybook"
		}
	}
	return ""
}

// shapeContext is what the post-walk shaping needs from DetectPath.
type shapeContext struct {
	markers       map[string]*detectedMarkers
	identity      SourceIdentity
	goSources     *goSourceScan
	pythonEntries []pythonEntry
	variables     func(root string) []DetectedVariable
	databases     func(root string, variables []DetectedVariable) []DetectedDatabase
}

// shapeDetection runs once every root's candidates exist: it adds what only
// the repository as a whole shows, removes what is not the application, and
// ranks what is left so the right candidate is selected on its own.
func (s *repoShapeScan) shapeDetection(result *DetectionResult, context shapeContext) {
	s.applyGitRequirements(result)
	if len(s.manifestsBOM) > 0 {
		for index := range result.Candidates {
			for _, file := range s.manifestsBOM {
				if underRoot(file, result.Candidates[index].Root) {
					result.Candidates[index].Evidence = append(result.Candidates[index].Evidence, DetectionEvidence{
						Path: file, Reason: path.Base(file) + " starts with a byte-order mark (accepted)",
					})
				}
			}
		}
	}
	if len(s.depthManifests) > 0 && !result.Truncated {
		result.Truncated = true
		result.TruncatedReason = boundedText("directory depth limit reached before "+s.depthManifests[0], 256)
	}
	s.applyEcosystems(result, context)
	s.applyNotDeployable(result, context)
	s.applyStaticShape(result, context)
	s.applyPlatformManifests(result, context)
	s.applyServerless(result, context)
	s.applyProcesses(result, context)
	s.applySplitRepository(result, context)
	s.applyDecoys(result)
	s.applyImportCase(result)
	result.Alternatives = s.upstreamAlternatives(context.identity)
	if result.Truncated {
		for index := range result.Candidates {
			result.Candidates[index].Evidence = append(result.Candidates[index].Evidence, DetectionEvidence{
				Path: rootLabelOf(result.Candidates[index].Root), Reason: "source scan truncated (" + result.TruncatedReason + "); the candidate was found before the bound",
			})
		}
	}
	for index := range result.Candidates {
		candidate := &result.Candidates[index]
		if len(candidate.Evidence) > 128 {
			candidate.Evidence = candidate.Evidence[:128]
		}
	}
	sort.SliceStable(result.Candidates, func(i, j int) bool {
		return compareCandidateRank(result.Candidates[i], result.Candidates[j]) > 0
	})
	// A committed build output or a repository of fixtures can hold hundreds
	// of roots; past the first few dozen, ranked, none is the application and
	// a chooser of them is unreadable.
	if dropped := len(result.Candidates) - shapeMaxCandidates; dropped > 0 {
		result.Candidates = result.Candidates[:shapeMaxCandidates]
		s.addSetAside(DetectionSetAside{Path: ".", Kind: "decoy",
			Reason: fmt.Sprintf("%d lower-ranked candidates are not listed", dropped)})
	}
	result.SetAside = append([]DetectionSetAside(nil), s.setAside...)
	result.SelectedID = rankedSelection(result.Candidates)
}

func rootLabelOf(root string) string {
	if root == "" {
		return "."
	}
	return root
}

// underRoot says whether a relative path lies inside a candidate root.
func underRoot(rel, root string) bool {
	return root == "" || rel == root || strings.HasPrefix(rel, rootPrefix(root))
}

func boundedText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit-3] + "..."
}

// removeCandidates drops the candidates keep refuses, in place.
func removeCandidates(result *DetectionResult, drop func(DetectedCandidate) bool) {
	kept := result.Candidates[:0]
	for _, candidate := range result.Candidates {
		if !drop(candidate) {
			kept = append(kept, candidate)
		}
	}
	result.Candidates = kept
}

// Ranking. A candidate's standing is, in order: whether it is a service at
// all, whether something marks it as not the application (an example, a
// docs site beside an app, static files inside a code root, the frontend of
// an API), how confident its evidence is, whether it lives where monorepos
// keep applications rather than packages, and how near the top it sits.

func candidateTier(candidate DetectedCandidate) int {
	switch {
	case candidate.NotDeployable != "":
		return 0
	case candidate.Demotion != "":
		return 1
	}
	return 2
}

// candidateArea is +1 for the directories monorepos keep applications in and
// -1 for those they keep shared packages and tooling in.
func candidateArea(root string) int {
	first, _, _ := strings.Cut(root, "/")
	switch strings.ToLower(first) {
	case "apps", "app", "services", "service", "servers", "server", "backend", "api", "web", "site", "website":
		return 1
	case "packages", "libs", "lib", "tools", "tooling", "scripts", "configs", "config", "internal":
		return -1
	}
	return 0
}

func rootDepth(root string) int {
	if root == "" {
		return 0
	}
	return strings.Count(root, "/") + 1
}

// compareCandidateRank is positive when a outranks b, zero on a tie.
func compareCandidateRank(a, b DetectedCandidate) int {
	if difference := candidateTier(a) - candidateTier(b); difference != 0 {
		return difference
	}
	if difference := confidenceRank(a.Confidence) - confidenceRank(b.Confidence); difference != 0 {
		return difference
	}
	// Where a monorepo keeps a root says something only between two nested
	// roots; the repository root itself is neither an app nor a package.
	if a.Root != "" && b.Root != "" {
		if difference := candidateArea(a.Root) - candidateArea(b.Root); difference != 0 {
			return difference
		}
	}
	// The shallower root wins a tie, because a root build usually builds the
	// whole repository — except when the shallower one is plain static
	// files, which say least of all about what the repository is for.
	depthA, depthB := rootDepth(a.Root), rootDepth(b.Root)
	switch {
	case depthA < depthB && a.BuildMethod != BuildStatic:
		return 1
	case depthB < depthA && b.BuildMethod != BuildStatic:
		return -1
	}
	return 0
}

// rankedSelection names the one candidate that outranks every other, or
// nothing when the best are tied. A candidate that is not a service is never
// chosen on its own, and one ranked down (an example, a docs site) is chosen
// only when it is the only deployable thing and nothing else in the
// repository is a service it could be the example of.
func rankedSelection(candidates []DetectedCandidate) string {
	best := -1
	tied := false
	for index := range candidates {
		if candidateTier(candidates[index]) == 0 {
			continue
		}
		if best < 0 {
			best = index
			continue
		}
		switch comparison := compareCandidateRank(candidates[index], candidates[best]); {
		case comparison > 0:
			best, tied = index, false
		case comparison == 0:
			tied = true
		}
	}
	if best < 0 || tied {
		return ""
	}
	// A library's docs site or example app is what the operator may want,
	// but it is not what the repository is; that is theirs to say.
	if candidateTier(candidates[best]) == 1 || documentationCandidate(candidates[best]) {
		demoted := 0
		for _, candidate := range candidates {
			if candidate.NotDeployable != "" {
				return ""
			}
			if candidateTier(candidate) == 1 {
				demoted++
			}
		}
		if demoted > 1 {
			return ""
		}
	}
	return candidates[best].ID
}

// Static roots. Any directory with an index.html used to be a site of its own,
// so a multi-page site was three tied candidates, a Flask app's templates
// folder a site serving raw Jinja, and an Express app's public/ a static site
// that outranked the API it belongs to.

// staticIndexSkipSegments hold index.html files that are templates, layouts
// or reports, never a site's entry page.
var staticIndexSkipSegments = map[string]bool{
	"templates": true, "template": true, "jinja2": true, "layouts": true, "_layouts": true, "_includes": true,
	"partials": true, "views": true, "themes": true, "coverage": true, "htmlcov": true, "lcov-report": true,
	"fixtures": true, "test": true, "tests": true, "__tests__": true, "examples": true, "example": true,
	"node_modules": true, "archetypes": true, "_site": true, "site-packages": true, "playwright-report": true,
	"storybook-static": true, "test-results": true, "cypress": true, "e2e": true,
}

// skipStaticIndex decides, from its path and its first 4 KiB, whether an
// index.html is a template or a report rather than a site's entry page.
func (s *repoShapeScan) skipStaticIndex(absolute, rel string) bool {
	rel = filepath.ToSlash(rel)
	directory := path.Dir(rel)
	for _, segment := range strings.Split(directory, "/") {
		if staticIndexSkipSegments[strings.ToLower(segment)] {
			s.addSetAside(DetectionSetAside{Path: rel, Reason: "index.html under " + segment + "/ is a template or report, not a site", Kind: "template"})
			return true
		}
	}
	file, err := os.Open(absolute)
	if err != nil {
		return false
	}
	defer file.Close()
	buffer := make([]byte, 4096)
	n, _ := io.ReadFull(file, buffer)
	head := strings.TrimSpace(string(manifestText(buffer[:n])))
	switch {
	case strings.HasPrefix(head, "---"):
		s.addSetAside(DetectionSetAside{Path: rel, Reason: "index.html starts with front matter; a site generator renders it", Kind: "template"})
		return true
	case strings.Contains(head, "{%") || strings.Contains(head, "<?php") || strings.Contains(head, "<%="):
		s.addSetAside(DetectionSetAside{Path: rel, Reason: "index.html holds template tags; the application renders it", Kind: "template"})
		return true
	}
	if strings.Contains(head, "{{") {
		s.templatedIndex = append(s.templatedIndex, rel)
	}
	return false
}

// staticCodeDirectories are the folders a web framework serves files from,
// where an index.html is the application's own static file.
var staticCodeDirectories = map[string]bool{
	"public": true, "static": true, "assets": true, "www": true, "wwwroot": true, "resources": true,
	"web": true, "webroot": true, "html": true, "templates": true, "views": true, "priv": true,
}

// applyStaticShape folds index.html roots into the sites and applications
// they belong to.
func (s *repoShapeScan) applyStaticShape(result *DetectionResult, context shapeContext) {
	s.applyToolingSites(result, context)
	statics := []int{}
	code := []int{}
	for index, candidate := range result.Candidates {
		if candidate.BuildMethod == BuildStatic {
			statics = append(statics, index)
		} else if candidate.NotDeployable == "" && candidate.Profile != ProfileCompose {
			code = append(code, index)
		}
	}
	drop := map[string]bool{}
	// Pages of a site: an index.html nested in another plain static root.
	pages := map[string]int{}
	for _, index := range statics {
		candidate := result.Candidates[index]
		for _, other := range statics {
			ancestor := result.Candidates[other]
			if other == index || ancestor.Root == candidate.Root || !underRoot(candidate.Root, ancestor.Root) {
				continue
			}
			drop[candidate.ID] = true
			pages[ancestor.ID]++
			break
		}
	}
	// Static files of an application: an index.html at or under a code root.
	for _, index := range statics {
		candidate := &result.Candidates[index]
		if drop[candidate.ID] {
			continue
		}
		owner := -1
		for _, other := range code {
			if underRoot(candidate.Root, result.Candidates[other].Root) {
				if owner < 0 || rootDepth(result.Candidates[other].Root) > rootDepth(result.Candidates[owner].Root) {
					owner = other
				}
			}
		}
		if owner < 0 {
			// A templated index.html with no application around it is a
			// client-side template (Vue or Alpine from a CDN), and stays a site.
			continue
		}
		application := result.Candidates[owner]
		label := applicationLabel(application)
		relative := strings.TrimPrefix(strings.TrimPrefix(candidate.Root, application.Root), "/")
		servedFolder := false
		for _, segment := range strings.Split(relative, "/") {
			servedFolder = servedFolder || staticCodeDirectories[strings.ToLower(segment)]
		}
		if candidate.Root == application.Root || servedFolder {
			drop[candidate.ID] = true
			reason := "static files of the " + label + " application in " + rootLabelOf(application.Root)
			s.addSetAside(DetectionSetAside{Path: rootLabelOf(candidate.Root), Reason: reason, Kind: "static-files"})
			result.Candidates[owner].Evidence = append(result.Candidates[owner].Evidence,
				DetectionEvidence{Path: joinRoot(candidate.Root, "index.html"), Reason: reason})
			continue
		}
		candidate.Confidence = ConfidenceLow
		candidate.Demotion = "static files inside the " + label + " application in " + rootLabelOf(application.Root)
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(candidate.Root, "index.html"), Reason: candidate.Demotion})
	}
	for id, count := range pages {
		for index := range result.Candidates {
			if result.Candidates[index].ID == id {
				result.Candidates[index].Evidence = append(result.Candidates[index].Evidence, DetectionEvidence{
					Path: rootLabelOf(result.Candidates[index].Root), Reason: fmt.Sprintf("%d more pages under %s are part of this site", count, rootLabelOf(result.Candidates[index].Root)),
				})
			}
		}
	}
	for _, index := range statics {
		if drop[result.Candidates[index].ID] {
			continue
		}
		for _, rel := range s.templatedIndex {
			if path.Dir(rel) == result.Candidates[index].Root || (result.Candidates[index].Root == "" && !strings.Contains(rel, "/")) {
				result.Candidates[index].Evidence = append(result.Candidates[index].Evidence, DetectionEvidence{
					Path: rel, Reason: "index.html carries {{ }} bindings a client-side framework fills in",
				})
			}
		}
	}
	removeCandidates(result, func(candidate DetectedCandidate) bool { return drop[candidate.ID] })
}

func applicationLabel(candidate DetectedCandidate) string {
	switch {
	case candidate.Framework != "":
		return frameworkDisplayName(candidate.Framework)
	case candidate.Recipe != "":
		return frameworkDisplayName(candidate.Recipe)
	case candidate.BuildMethod == BuildDockerfile:
		return "Dockerfile"
	}
	return "code"
}

// frameworkDisplayName is how evidence names a framework or language.
func frameworkDisplayName(name string) string {
	if label, ok := frameworkDisplayNames[name]; ok {
		return label
	}
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

var frameworkDisplayNames = map[string]string{
	"nextjs": "Next.js", "sveltekit": "SvelteKit", "nuxt": "Nuxt", "remix": "Remix", "react-router": "React Router",
	"solid-start": "SolidStart", "tanstack-start": "TanStack Start", "angular": "Angular", "nestjs": "NestJS",
	"gatsby": "Gatsby", "docusaurus": "Docusaurus", "vitepress": "VitePress", "eleventy": "Eleventy",
	"create-react-app": "Create React App", "vue-cli": "Vue CLI", "vite": "Vite", "express": "Express",
	"fastify": "Fastify", "hono": "Hono", "koa": "Koa", "node": "Node.js", "go": "Go", "python": "Python",
	"django": "Django", "fastapi": "FastAPI", "flask": "Flask", "streamlit": "Streamlit", "gradio": "Gradio",
	"rust": "Rust", "java": "Java", "spring-boot": "Spring Boot", "dotnet": ".NET", "aspnet": "ASP.NET Core",
	"deno": "Deno", "fresh": "Fresh", "php": "PHP", "laravel": "Laravel", "symfony": "Symfony", "slim": "Slim",
	"rails": "Rails", "sinatra": "Sinatra", "hanami": "Hanami", "phoenix": "Phoenix", "elixir": "Elixir",
	"ruby": "Ruby", "jekyll": "Jekyll", "hugo": "Hugo", "mkdocs": "MkDocs",
}

// Decoys and documentation. A root under examples/, tests/ or a Storybook is
// somebody's illustration of the application; a docs site beside an app is
// its manual. Both stay on offer and are never chosen over the application.
func (s *repoShapeScan) applyDecoys(result *DetectionResult) {
	for index := range result.Candidates {
		candidate := &result.Candidates[index]
		if candidate.Demotion != "" || candidate.NotDeployable != "" {
			continue
		}
		if label := decoySegment(candidate.Root); label != "" {
			candidate.Demotion = rootLabelOf(candidate.Root) + " is " + label + ", not the application"
		}
	}
	for index := range result.Candidates {
		candidate := &result.Candidates[index]
		if candidateTier(*candidate) != 2 || !documentationCandidate(*candidate) {
			continue
		}
		for _, other := range result.Candidates {
			if other.ID != candidate.ID && candidateTier(other) == 2 && !documentationCandidate(other) {
				candidate.Demotion = "documentation site beside the " + applicationLabel(other) + " application in " + rootLabelOf(other.Root)
				break
			}
		}
	}
}

func hasApplication(candidates []DetectedCandidate) bool {
	for _, candidate := range candidates {
		if candidateTier(candidate) == 2 && !documentationCandidate(candidate) {
			return true
		}
	}
	return false
}

var documentationFrameworks = map[string]bool{
	"docusaurus": true, "vitepress": true, "mkdocs": true, "sphinx": true, "vuepress": true, "starlight": true,
	"nextra": true, "hugo": true, "jekyll": true,
}

// documentationCandidate is a docs-site generator's candidate, or a static
// root whose first directory is docs/.
func documentationCandidate(candidate DetectedCandidate) bool {
	if documentationFrameworks[candidate.Framework] {
		return true
	}
	for _, evidence := range candidate.Evidence {
		if strings.HasPrefix(evidence.Reason, "documentation generator ") {
			return true
		}
	}
	first, _, _ := strings.Cut(candidate.Root, "/")
	switch strings.ToLower(first) {
	case "docs", "doc", "documentation":
		return candidate.BuildMethod == BuildStatic || candidate.Profile == ProfileStatic
	}
	return false
}

// validateRepoShapeEvidence bounds and screens what this file adds to a
// detection result, the same way validateDetectionResult treats the rest:
// the result is persisted with the draft and read back later.
func validateRepoShapeEvidence(detection DetectionResult) error {
	if len(detection.SetAside) > shapeMaxSetAside || len(detection.Alternatives) > 8 ||
		len(detection.GitRequirements.SubmoduleList) > 64 || len(detection.GitRequirements.LFSPaths) > 256 ||
		detection.GitRequirements.LFSFiles < 0 {
		return fmt.Errorf("%w: repository shape evidence exceeds its bounds", ErrInvalidPlan)
	}
	text := func(value string, limit int) bool {
		return len(value) <= limit && !strings.ContainsAny(value, "\x00\r\n") &&
			rejectPlanSecretLiteral("repository shape evidence", value) == nil
	}
	for _, item := range detection.SetAside {
		if !setAsideKinds[item.Kind] || !text(item.Path, 4096) || !text(item.Reason, 512) {
			return fmt.Errorf("%w: set-aside evidence is malformed", ErrInvalidPlan)
		}
	}
	for _, alternative := range detection.Alternatives {
		if (alternative.Kind != "template" && alternative.Kind != "image") || alternative.Ref == "" ||
			!text(alternative.Ref, 512) || !text(alternative.Label, 256) || !text(alternative.Evidence, 512) {
			return fmt.Errorf("%w: detection alternative is malformed", ErrInvalidPlan)
		}
	}
	for _, submodule := range detection.GitRequirements.SubmoduleList {
		if submodule.Path == "" || !text(submodule.Path, 4096) {
			return fmt.Errorf("%w: submodule evidence is malformed", ErrInvalidPlan)
		}
	}
	for _, file := range detection.GitRequirements.LFSPaths {
		if file == "" || !text(file, 4096) {
			return fmt.Errorf("%w: LFS evidence is malformed", ErrInvalidPlan)
		}
	}
	for _, candidate := range detection.Candidates {
		if (candidate.NotDeployable != "" && notDeployableKinds[candidate.NotDeployable] == "") ||
			!text(candidate.Demotion, 512) || !text(candidate.DesktopShell, 32) ||
			len(candidate.Companions) > 8 || len(candidate.Processes) > 16 || len(candidate.PlatformManifests) > 12 ||
			len(candidate.ServerlessCode) > 8 || len(candidate.ImportCaseMismatches) > 16 {
			return fmt.Errorf("%w: detected candidate shape is malformed", ErrInvalidPlan)
		}
		for _, companion := range candidate.Companions {
			if companion != "" && !safeRelativePath(companion) {
				return fmt.Errorf("%w: companion root is malformed", ErrInvalidPlan)
			}
		}
		for _, process := range candidate.Processes {
			if !validProcessKind(process.Kind) || !validProcessName(process.Name) || !text(process.Command, 4096) ||
				!text(process.Source, 4096) || !text(process.Reason, 512) {
				return fmt.Errorf("%w: detected process is malformed", ErrInvalidPlan)
			}
		}
		for _, manifest := range candidate.PlatformManifests {
			if err := validatePlatformManifest(manifest, text); err != nil {
				return err
			}
		}
		for _, code := range candidate.ServerlessCode {
			if !text(code.Platform, 64) || !text(code.Entry, 4096) || len(code.Paths) > 16 {
				return fmt.Errorf("%w: serverless evidence is malformed", ErrInvalidPlan)
			}
			for _, file := range code.Paths {
				if !text(file, 4096) {
					return fmt.Errorf("%w: serverless evidence is malformed", ErrInvalidPlan)
				}
			}
		}
		for _, mismatch := range candidate.ImportCaseMismatches {
			if (mismatch.Language != "javascript" && mismatch.Language != "php") || mismatch.Line < 0 ||
				!text(mismatch.File, 4096) || !text(mismatch.Specifier, 1024) || !text(mismatch.Actual, 4096) {
				return fmt.Errorf("%w: import evidence is malformed", ErrInvalidPlan)
			}
		}
	}
	return nil
}

// devStaticServers are the programs a plain HTML site previews itself with
// during development; none of them is how a site is served.
var devStaticServers = map[string]bool{
	"live-server": true, "http-server": true, "lite-server": true, "browser-sync": true, "five-server": true,
	"sirv": true, "sirv-cli": true, "static-server": true, "serve": true,
}

func devStaticServerScript(script string) bool {
	for _, field := range strings.Fields(script) {
		if devStaticServers[field] {
			return true
		}
	}
	return false
}

// applyToolingSites turns a plain HTML site whose package.json only runs
// tooling — the Tailwind CLI, PostCSS, a formatter, a live-reload server —
// into the static site it is. It used to be a Node worker that asked for a
// start command, or a web service on port 3000 running a dev server that
// listens somewhere else.
func (s *repoShapeScan) applyToolingSites(result *DetectionResult, context shapeContext) {
	for index := range result.Candidates {
		candidate := &result.Candidates[index]
		marker := context.markers[candidate.Root]
		if candidate.Recipe != "node" || candidate.NotDeployable != "" || marker == nil || marker.staticFile == "" ||
			marker.staticFile != joinRoot(candidate.Root, "index.html") {
			continue
		}
		var manifest nodeManifest
		if !parseNodeManifest(marker.packageJSON, &manifest) || matchNodeFramework(manifest) != nil || matchNodeServerLibrary(manifest) != "" {
			continue
		}
		if start := manifest.Scripts["start"]; start != "" && !devStaticServerScript(start) {
			continue
		}
		evidence := DetectionEvidence{Path: joinRoot(candidate.Root, "package.json"), Reason: "package.json only runs tooling; index.html is the site"}
		if manifest.Scripts["build"] == "" {
			site := newDetectedCandidate(candidate.Root, BuildStatic, DetectedCandidate{
				Name: "Static site in " + rootLabelOf(candidate.Root), Profile: ProfileStatic, Confidence: ConfidenceMedium, Port: 80,
				Evidence:  []DetectionEvidence{{Path: marker.staticFile, Reason: "static HTML entry point"}, evidence},
				Variables: candidate.Variables, Databases: candidate.Databases,
			})
			*candidate = site
			continue
		}
		candidate.Name = "Static site with a build step"
		candidate.Profile, candidate.Port = ProfileStatic, 80
		candidate.StartCommand = ""
		candidate.OutputDirectory = "."
		candidate.SPAFallback = false
		candidate.RecipeIssue = ""
		candidate.NeedsDecision = removeDecision(removeDecision(candidate.NeedsDecision, "choose a start command or static output"),
			"confirm whether this package serves HTTP (web application) or runs as a worker")
		if len(marker.lockfiles) > 0 && candidate.Confidence == ConfidenceLow && len(candidate.NeedsDecision) == 0 {
			candidate.Confidence = ConfidenceMedium
		}
		candidate.Evidence = append(candidate.Evidence, evidence, DetectionEvidence{Path: marker.staticFile,
			Reason: "the build script runs first; the site is served from the package root without node_modules, manifests or dotfiles"})
	}
}

// validOutputDirectory is a static output inside the source root. "." is the
// package root itself: a plain HTML site whose package.json only runs its
// tooling is served from where its index.html is.
func validOutputDirectory(output string) bool { return output == "." || safeRelativePath(output) }

// packageRootSite is where the Node recipe gathers a site served from its
// package root: everything but node_modules, the package manifests, the
// lockfiles and dotfiles, which are the build's tooling and never the site's.
const packageRootSite = "/site"

var packageRootSiteLine = "RUN mkdir -p " + packageRootSite + " && find . -mindepth 1 -maxdepth 1" +
	" ! -name node_modules ! -name 'package.json' ! -name 'package-lock.json' ! -name 'npm-shrinkwrap.json'" +
	" ! -name 'pnpm-lock.yaml' ! -name 'pnpm-workspace.yaml' ! -name 'yarn.lock' ! -name 'bun.lock' ! -name 'bun.lockb'" +
	" ! -name '.*' -exec cp -a {} " + packageRootSite + "/ \\;"
