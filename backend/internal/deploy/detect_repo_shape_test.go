package deploy

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// detectShapeFixture writes files into a fresh directory and detects it.
func detectShapeFixture(t *testing.T, files map[string]string, identity ...SourceIdentity) DetectionResult {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		writeBuildFixture(t, root, path, content)
	}
	source := SourceIdentity{}
	if len(identity) > 0 {
		source = identity[0]
	}
	result, err := (Detector{}).DetectPath(t.Context(), root, source)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	return result
}

func selectedOf(result DetectionResult) *DetectedCandidate {
	return selectedDetectionCandidate(&result)
}

func candidateAtRoot(result DetectionResult, root string, method BuildMethod) *DetectedCandidate {
	for index := range result.Candidates {
		if result.Candidates[index].Root == root && result.Candidates[index].BuildMethod == method {
			return &result.Candidates[index]
		}
	}
	return nil
}

func setAsideKind(result DetectionResult, kind string) *DetectionSetAside {
	for index := range result.SetAside {
		if result.SetAside[index].Kind == kind {
			return &result.SetAside[index]
		}
	}
	return nil
}

func evidenceMentions(candidate *DetectedCandidate, text string) bool {
	for _, evidence := range candidate.Evidence {
		if strings.Contains(evidence.Reason, text) {
			return true
		}
	}
	return false
}

const (
	expressManifest = `{"name":"api","scripts":{"start":"node server.js"},"dependencies":{"express":"4"}}`
	viteManifest    = `{"name":"client","scripts":{"build":"vite build"},"devDependencies":{"vite":"6"}}`
)

// Decoys: roots that are examples, docs, templates or the static files of an
// application, which used to tie with the application or silently beat it.
func TestRepositoryShapeRanksTheApplicationAboveItsDecoys(t *testing.T) {
	for _, fixture := range []struct {
		name         string
		files        map[string]string
		selected     string // "root|method", or "" for no selection
		candidates   int
		setAside     string
		demotedRoots []string
	}{
		{
			name:     "express without a lockfile and public/index.html",
			files:    map[string]string{"package.json": expressManifest, "server.js": "", "public/index.html": "<html></html>"},
			selected: "|recipe", candidates: 1, setAside: "static-files",
		},
		{
			name: "flask whose app object is elsewhere and static/index.html",
			files: map[string]string{"requirements.txt": "flask==3.1.0\n", "myapp/factory.py": "",
				"myapp/static/index.html": "<html></html>"},
			selected: "|recipe", candidates: 1, setAside: "static-files",
		},
		{
			name: "flask with templates/index.html",
			files: map[string]string{"requirements.txt": "flask==3.1.0\n", "app.py": "from flask import Flask\napp = Flask(__name__)\n",
				"templates/index.html": "<h1>{{ title }}</h1>"},
			selected: "|recipe", candidates: 1, setAside: "template",
		},
		{
			name:     "express with a lockfile and a docs site",
			files:    map[string]string{"package.json": expressManifest, "package-lock.json": "{}", "docs/index.html": "<html></html>"},
			selected: "|recipe", candidates: 2, demotedRoots: []string{"docs"},
		},
		{
			name: "turborepo web app and docs site",
			files: map[string]string{
				"apps/web/package.json": nextManifest, "apps/web/package-lock.json": "{}",
				"apps/docs/package.json":      `{"name":"docs","scripts":{"build":"docusaurus build"},"dependencies":{"@docusaurus/core":"3"}}`,
				"apps/docs/package-lock.json": "{}",
			},
			selected: "apps/web|recipe", candidates: 2, demotedRoots: []string{"apps/docs"},
		},
		{
			name: "application and its examples",
			files: map[string]string{
				"package.json": nextManifest, "package-lock.json": "{}",
				"examples/basic/package.json": viteManifest, "examples/basic/package-lock.json": "{}",
			},
			selected: "|recipe", candidates: 2, demotedRoots: []string{"examples/basic"},
		},
		{
			name: "library with two examples",
			files: map[string]string{
				"package.json":                 `{"name":"lib","main":"dist/index.js","types":"dist/index.d.ts","exports":{".":"./dist/index.js"},"files":["dist"],"scripts":{"build":"tsup"}}`,
				"package-lock.json":            "{}",
				"examples/nextjs/package.json": nextManifest, "examples/nextjs/package-lock.json": "{}",
				"examples/vite/package.json": viteManifest, "examples/vite/package-lock.json": "{}",
			},
			selected: "", candidates: 3, demotedRoots: []string{"examples/nextjs", "examples/vite"},
		},
		{
			name:     "multi-page static site",
			files:    map[string]string{"index.html": "<html></html>", "about/index.html": "<html></html>", "blog/index.html": "<html></html>"},
			selected: "|static", candidates: 1,
		},
		{
			name:     "coverage report beside a static site",
			files:    map[string]string{"index.html": "<html></html>", "coverage/lcov-report/index.html": "<html></html>"},
			selected: "|static", candidates: 1,
		},
		{
			name:     "front matter layout is not a site",
			files:    map[string]string{"index.html": "---\nlayout: default\n---\n<h1>{{ page.title }}</h1>", "package.json": expressManifest},
			selected: "|recipe", candidates: 1, setAside: "template",
		},
		{
			name: "deno with public/index.html",
			files: map[string]string{"deno.json": `{"tasks":{"start":"deno run -A main.ts"}}`, "main.ts": "",
				"public/index.html": "<html></html>"},
			selected: "|recipe", candidates: 1, setAside: "static-files",
		},
		{
			name:     "php beats static at the same root",
			files:    map[string]string{"index.php": "<?php echo 1;", "index.html": "<html></html>"},
			selected: "|recipe", candidates: 1, setAside: "static-files",
		},
		{
			name:     "dev container is tooling",
			files:    map[string]string{".devcontainer/Dockerfile": "FROM mcr.microsoft.com/devcontainers/base\n", "package.json": expressManifest, "package-lock.json": "{}"},
			selected: "|recipe", candidates: 1, setAside: "tooling",
		},
		{
			name:     "vue from a CDN keeps its bindings",
			files:    map[string]string{"index.html": `<div id="app">{{ message }}</div>`},
			selected: "|static", candidates: 1,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			result := detectShapeFixture(t, fixture.files)
			if len(result.Candidates) != fixture.candidates {
				t.Fatalf("candidates = %d, want %d: %#v", len(result.Candidates), fixture.candidates, result.Candidates)
			}
			got := ""
			if selected := selectedOf(result); selected != nil {
				got = selected.Root + "|" + string(selected.BuildMethod)
			}
			if got != fixture.selected {
				t.Fatalf("selected = %q, want %q: %#v", got, fixture.selected, result.Candidates)
			}
			if fixture.setAside != "" && setAsideKind(result, fixture.setAside) == nil {
				t.Fatalf("nothing set aside as %s: %#v", fixture.setAside, result.SetAside)
			}
			for _, root := range fixture.demotedRoots {
				found := false
				for _, candidate := range result.Candidates {
					if candidate.Root == root {
						found = true
						if candidate.Demotion == "" {
							t.Fatalf("%s is not ranked down: %#v", root, candidate)
						}
					}
				}
				if !found {
					t.Fatalf("no candidate at %s", root)
				}
			}
		})
	}
}

func TestMultiPageSiteCountsItsPages(t *testing.T) {
	result := detectShapeFixture(t, map[string]string{
		"index.html": "<html></html>", "about/index.html": "<html></html>", "blog/index.html": "<html></html>",
	})
	if len(result.Candidates) != 1 || !evidenceMentions(&result.Candidates[0], "2 more pages under .") {
		t.Fatalf("pages were not folded into the site: %#v", result.Candidates)
	}
}

func TestCandidateRankOrdersTiersConfidenceAreaAndDepth(t *testing.T) {
	candidate := func(root string, method BuildMethod, confidence DetectionConfidence) DetectedCandidate {
		return DetectedCandidate{ID: root + string(method), Root: root, BuildMethod: method, Confidence: confidence}
	}
	for _, fixture := range []struct {
		name string
		a, b DetectedCandidate
		// want is 1 when a is selected over b, 0 when neither is.
		want int
	}{
		{"deployable beats not deployable", candidate("b", BuildRecipe, ConfidenceLow), DetectedCandidate{ID: "x", Confidence: ConfidenceHigh, NotDeployable: "library"}, 1},
		{"application beats demoted", candidate("b", BuildRecipe, ConfidenceLow), DetectedCandidate{ID: "x", Confidence: ConfidenceHigh, Demotion: "an example"}, 1},
		{"confidence", candidate("a", BuildRecipe, ConfidenceHigh), candidate("b", BuildRecipe, ConfidenceMedium), 1},
		{"apps over packages", candidate("apps/web", BuildRecipe, ConfidenceHigh), candidate("packages/ui", BuildRecipe, ConfidenceHigh), 1},
		{"shallower root", candidate("", BuildDockerfile, ConfidenceHigh), candidate("site/web", BuildRecipe, ConfidenceHigh), 1},
		{"shallower static files do not win", candidate("", BuildStatic, ConfidenceMedium), candidate("server", BuildRecipe, ConfidenceMedium), 0},
		// Two ways to build one directory: the repository's own Dockerfile
		// wins (detect_ranking.go).
		{"a same-root Dockerfile beats the recipe", candidate("", BuildDockerfile, ConfidenceHigh), candidate("", BuildRecipe, ConfidenceHigh), 1},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			for _, order := range [][]DetectedCandidate{{fixture.a, fixture.b}, {fixture.b, fixture.a}} {
				selected, reason := rankCandidates(order)
				want := ""
				if fixture.want > 0 {
					want = fixture.a.ID
				}
				if selected != want {
					t.Fatalf("selected %q (%s), want %q", selected, reason, want)
				}
			}
		})
	}
	tied := []DetectedCandidate{candidate("a", BuildRecipe, ConfidenceHigh), candidate("b", BuildRecipe, ConfidenceHigh)}
	if selected, _ := rankCandidates(tied); selected != "" {
		t.Fatal("a tie was resolved arbitrarily")
	}
	notService := []DetectedCandidate{{ID: "only", Confidence: ConfidenceHigh, NotDeployable: "library"}}
	if selected, _ := rankCandidates(notService); selected != "" {
		t.Fatal("a library was selected on its own")
	}
}

// The walk reads manifests before bulk content, counts only files it opens,
// and reads Go source head-first under its own budget.
func TestDetectionLimitsNeverHideTheRootManifest(t *testing.T) {
	t.Run("assets before package.json", func(t *testing.T) {
		root := t.TempDir()
		writeBuildFixture(t, root, "package.json", nextManifest)
		writeBuildFixture(t, root, "package-lock.json", "{}")
		for index := 0; index < 300; index++ {
			writeBuildFixture(t, root, fmt.Sprintf("assets/img/%03d.png", index), "png")
		}
		result, err := (Detector{Limits: DetectionLimits{MaxFiles: 50}}).DetectPath(t.Context(), root, SourceIdentity{})
		if err != nil || result.Truncated || len(result.Candidates) != 1 || result.Candidates[0].Framework != "nextjs" {
			t.Fatalf("detection = %#v, %v", result, err)
		}
	})
	t.Run("generated Go before go.mod", func(t *testing.T) {
		root := t.TempDir()
		writeBuildFixture(t, root, "go.mod", "module example.test/app\n\ngo 1.26\n")
		writeBuildFixture(t, root, "main.go", "package main\n\nfunc main() {}\n")
		generated := "// Code generated by protoc-gen-go. DO NOT EDIT.\n\npackage v1\n\n" + strings.Repeat("// padding\n", 12_000)
		for index := 0; index < 12; index++ {
			writeBuildFixture(t, root, fmt.Sprintf("api/gen/v1/service%d.pb.go", index), generated)
			writeBuildFixture(t, root, fmt.Sprintf("ent/generated%d.go", index), generated)
		}
		result, err := (Detector{Limits: DetectionLimits{MaxReadBytes: 256 << 10}}).DetectPath(t.Context(), root, SourceIdentity{})
		if err != nil || result.Truncated || len(result.Candidates) != 1 || result.Candidates[0].Recipe != "go" ||
			result.Candidates[0].NotDeployable != "" {
			t.Fatalf("detection = %#v, %v", result, err)
		}
	})
	t.Run("manifest past the depth bound is reported", func(t *testing.T) {
		deep := strings.Repeat("d/", 11) + "package.json"
		result := detectShapeFixture(t, map[string]string{deep: nextManifest, "index.html": "<html></html>"})
		if !result.Truncated || !strings.Contains(result.TruncatedReason, "depth limit") {
			t.Fatalf("depth pruning was silent: %#v", result)
		}
		if len(result.Candidates) != 1 || !evidenceMentions(&result.Candidates[0], "found before the bound") {
			t.Fatalf("candidate lacks truncation evidence: %#v", result.Candidates)
		}
	})
	t.Run("root manifest read first under a one-file bound", func(t *testing.T) {
		root := t.TempDir()
		writeBuildFixture(t, root, "a/b/c/package.json", viteManifest)
		writeBuildFixture(t, root, "package.json", nextManifest)
		result, err := (Detector{Limits: DetectionLimits{MaxFiles: 1}}).DetectPath(t.Context(), root, SourceIdentity{})
		if err != nil || len(result.Candidates) != 1 || result.Candidates[0].Root != "" {
			t.Fatalf("detection = %#v, %v", result, err)
		}
	})
	t.Run("committed virtualenv is skipped", func(t *testing.T) {
		files := map[string]string{
			"requirements.txt": "flask==3.1.0\n", "app.py": "from flask import Flask\napp = Flask(__name__)\n",
			"env/pyvenv.cfg": "home = /usr/bin\n",
		}
		for index := 0; index < 40; index++ {
			files[fmt.Sprintf("env/lib/python3.12/site-packages/pkg%d/index.html", index)] = "<html></html>"
		}
		result := detectShapeFixture(t, files)
		if len(result.Candidates) != 1 || setAsideKind(result, "virtualenv") == nil {
			t.Fatalf("virtualenv was walked: %#v", result)
		}
	})
}

func TestGeneratedGoSourceIsRecognised(t *testing.T) {
	for _, fixture := range []struct {
		content string
		want    bool
	}{
		{"// Code generated by ent, DO NOT EDIT.\n\npackage ent\n", true},
		{"// Copyright\n// Code generated by protoc-gen-go. DO NOT EDIT.\npackage v1\n", true},
		{"package main\n// Code generated by hand. DO NOT EDIT.\n", false},
		{"package main\n", false},
	} {
		if got := generatedGoSource([]byte(fixture.content)); got != fixture.want {
			t.Fatalf("generatedGoSource(%q) = %t", fixture.content, got)
		}
	}
}

func TestManifestsWithAByteOrderMarkAreRead(t *testing.T) {
	bom := "\xEF\xBB\xBF"
	result := detectShapeFixture(t, map[string]string{"package.json": bom + nextManifest, "package-lock.json": "{}"})
	if len(result.Candidates) != 1 || result.Candidates[0].Framework != "nextjs" ||
		!evidenceMentions(&result.Candidates[0], "byte-order mark (accepted)") {
		t.Fatalf("BOM package.json = %#v", result.Candidates)
	}
	if _, err := validateNodeRecipeContent([]byte(bom+nextManifest), nodeRootFiles{}, BuildPlanConfig{Method: BuildRecipe, Recipe: "node"}); err != nil {
		t.Fatalf("recipe refused a BOM manifest: %v", err)
	}
	if facts := readNodeInstallFacts(nodeFiles{}, "", []byte(bom+`{"packageManager":"pnpm@9.0.0"}`), "x64"); facts.declared.name != "pnpm" {
		t.Fatal("packageManager behind a BOM was not read")
	}
	composer := detectShapeFixture(t, map[string]string{"composer.json": bom + `{"require":{"laravel/framework":"^12.0"}}`, "artisan": ""})
	if len(composer.Candidates) != 1 || composer.Candidates[0].Framework != "laravel" {
		t.Fatalf("BOM composer.json = %#v", composer.Candidates)
	}
	utf16 := []byte{0xFF, 0xFE}
	for _, r := range `{"name":"x"}` {
		utf16 = append(utf16, byte(r), 0)
	}
	if text, bom := decodeManifest(utf16); !bom || string(text) != `{"name":"x"}` {
		t.Fatalf("UTF-16 manifest = %q, %t", text, bom)
	}
	if deno := parseDenoConfig([]byte(bom + `{"tasks":{"start":"deno run main.ts"}}`)); deno.task("start") == "" {
		t.Fatal("BOM deno.json lost its tasks")
	}
}

func TestPlainHTMLSiteWithToolingPackageIsAStaticSite(t *testing.T) {
	built := detectShapeFixture(t, map[string]string{
		"index.html":        "<html></html>",
		"package.json":      `{"scripts":{"build":"tailwindcss -i src/in.css -o dist/out.css","start":"live-server"},"devDependencies":{"tailwindcss":"4","live-server":"1"}}`,
		"package-lock.json": "{}",
	})
	if len(built.Candidates) != 1 {
		t.Fatalf("candidates = %#v", built.Candidates)
	}
	candidate := built.Candidates[0]
	if candidate.Profile != ProfileStatic || candidate.OutputDirectory != "." || candidate.StartCommand != "" ||
		candidate.BuildCommand != "npm run build" || candidate.Port != 80 || selectedOf(built) == nil {
		t.Fatalf("tooling site = %#v", candidate)
	}
	plain := detectShapeFixture(t, map[string]string{
		"index.html": "<html></html>", "package.json": `{"devDependencies":{"prettier":"3"}}`,
	})
	if len(plain.Candidates) != 1 || plain.Candidates[0].BuildMethod != BuildStatic {
		t.Fatalf("prettier-only site = %#v", plain.Candidates)
	}
	rendered, err := renderRecipeDockerfile(selectedRecipe{kind: "node", catalogueKey: "node:npm"},
		BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", OutputDirectory: "."},
		[]ResolvedImage{testResolvedImage("node"), testResolvedImage(recipeBaseCatalogue["static"][0])})
	if err != nil || !strings.Contains(rendered, packageRootSiteLine) || !strings.Contains(rendered, "COPY --from=build /site/ /usr/share/nginx/html/") {
		t.Fatalf("package-root site Dockerfile = %s, %v", rendered, err)
	}
	configuration := PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "node", OutputDirectory: "."}}
	if err := configuration.Validate(); err != nil && strings.Contains(err.Error(), "build paths") {
		t.Fatalf("package-root output refused: %v", err)
	}
}

func testResolvedImage(reference string) ResolvedImage {
	return ResolvedImage{Reference: reference, Digest: "sha256:" + strings.Repeat("a", 64)}
}

func TestSplitRepositoryPicksOrMergesTheAPI(t *testing.T) {
	t.Run("root build compiles the client", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json":             `{"name":"mern","scripts":{"start":"node server.js","build":"npm --prefix client ci && npm --prefix client run build"},"dependencies":{"express":"4"}}`,
			"package-lock.json":        "{}",
			"server.js":                "app.use(express.static(path.join(__dirname, 'client/dist')))\n",
			"client/package.json":      viteManifest,
			"client/package-lock.json": "{}",
		})
		selected := selectedOf(result)
		if len(result.Candidates) != 1 || selected == nil || selected.Root != "" || selected.Confidence != ConfidenceHigh ||
			!evidenceMentions(selected, "builds client/ into this deployment") {
			t.Fatalf("MERN = %#v", result)
		}
	})
	t.Run("frontend reads the API address", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"frontend/package.json": viteManifest, "frontend/package-lock.json": "{}",
			"frontend/.env.example":    "VITE_API_URL=http://localhost:8000\n",
			"backend/requirements.txt": "fastapi==0.115.0\nuvicorn==0.30.0\n",
			"backend/main.py":          "from fastapi import FastAPI\napp = FastAPI()\n",
		})
		selected := selectedOf(result)
		frontend := candidateAtRoot(result, "frontend", BuildRecipe)
		if selected == nil || selected.Root != "backend" || frontend == nil || len(frontend.Companions) != 1 ||
			frontend.Companions[0] != "backend" || len(selected.Companions) != 1 {
			t.Fatalf("split = %#v", result)
		}
	})
	t.Run("vite proxy names the API port", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"web/package.json": viteManifest, "web/package-lock.json": "{}",
			"web/vite.config.ts": "export default { server: { proxy: { '/api': 'http://localhost:3000' } } }",
			"api/package.json":   expressManifest, "api/package-lock.json": "{}",
		})
		selected := selectedOf(result)
		if selected == nil || selected.Root != "api" || !strings.Contains(candidateAtRoot(result, "web", BuildRecipe).Demotion, "localhost:3000") {
			t.Fatalf("proxy split = %#v", result)
		}
	})
}

func TestRepositoryShapeEvidenceIsValidated(t *testing.T) {
	source := &DraftSourceConfig{Kind: SourceGit, Mode: SourceModeLocalCheckout}
	base := DetectionResult{Source: SourceIdentity{Kind: SourceGit}, Candidates: []DetectedCandidate{{
		ID: "a", Name: "app", Profile: ProfileWeb, BuildMethod: BuildRecipe, Confidence: ConfidenceHigh,
		Evidence: []DetectionEvidence{}, NeedsDecision: []string{},
		Processes: []DetectedProcess{{Name: "worker", Kind: "worker", Command: "celery -A app worker", Source: "Procfile", Reason: "Procfile"}},
	}}}
	if err := validateDetectionResult(source, base); err != nil {
		t.Fatalf("valid shape evidence refused: %v", err)
	}
	for name, mutate := range map[string]func(*DetectionResult){
		"credential in a process command": func(result *DetectionResult) {
			result.Candidates[0].Processes[0].Command = "API_TOKEN=plain-secret worker"
		},
		"unknown process kind":   func(result *DetectionResult) { result.Candidates[0].Processes[0].Kind = "daemon" },
		"unknown not-deployable": func(result *DetectionResult) { result.Candidates[0].NotDeployable = "toaster" },
		"unknown set-aside kind": func(result *DetectionResult) {
			result.SetAside = []DetectionSetAside{{Path: "x", Kind: "?", Reason: "r"}}
		},
		"escaping companion root": func(result *DetectionResult) { result.Candidates[0].Companions = []string{"../x"} },
		"platform with bad output": func(result *DetectionResult) {
			result.Candidates[0].PlatformManifests = []DetectedPlatformManifest{{File: "fly.toml", Platform: "fly", OutputDirectory: "../out"}}
		},
		"alternative with bad kind": func(result *DetectionResult) {
			result.Alternatives = []DetectionAlternative{{Kind: "zip", Ref: "x", Label: "x", Evidence: "x"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			result := base
			result.Candidates = append([]DetectedCandidate(nil), base.Candidates...)
			result.Candidates[0].Processes = append([]DetectedProcess(nil), base.Candidates[0].Processes...)
			mutate(&result)
			if err := validateDetectionResult(source, result); err == nil {
				t.Fatal("malformed shape evidence was accepted")
			}
		})
	}
}

// What validation would refuse is dropped before the result is saved, so an
// odd name in a repository costs that one fact and never the import.
func TestInvalidShapeFactsAreDroppedNotFatal(t *testing.T) {
	source := &DraftSourceConfig{Kind: SourceGit, Mode: SourceModeLocalCheckout}
	result := DetectionResult{Source: SourceIdentity{Kind: SourceGit}, SelectedID: "a",
		SetAside: []DetectionSetAside{{Path: "a\nb", Kind: "template", Reason: "index.html under a template"}, {Path: "docs", Kind: "tooling", Reason: "kept"}},
		Candidates: []DetectedCandidate{{
			ID: "a", Name: "app", Profile: ProfileWeb, BuildMethod: BuildRecipe, Confidence: ConfidenceHigh, NeedsDecision: []string{},
			Demotion: strings.Repeat("d", 600),
			Evidence: []DetectionEvidence{{Path: "fly.toml", Reason: strings.Repeat("r", 700)}, {Path: "x\ny", Reason: "newline path"}},
			Processes: []DetectedProcess{
				{Name: "queue worker", Kind: "worker", Command: "bin/worker", Source: "fly.toml", Reason: "fly.toml"},
				{Name: "jobs", Kind: "worker", Command: "bin/jobs", Source: "fly.toml", Reason: "fly.toml"},
			},
			PlatformManifests: []DetectedPlatformManifest{{File: "fly.toml", Platform: "fly", HealthPath: strings.Repeat("h", 2000)}},
			Companions:        []string{"../api"},
		}},
	}
	if validateDetectionResult(source, result) == nil {
		t.Fatal("the fixture is already valid")
	}
	keepValidShape(&result)
	if err := validateDetectionResult(source, result); err != nil {
		t.Fatalf("still refused: %v", err)
	}
	candidate := result.Candidates[0]
	if len(result.SetAside) != 1 || len(candidate.Processes) != 1 || candidate.Processes[0].Name != "jobs" ||
		len(candidate.PlatformManifests) != 0 || len(candidate.Companions) != 0 || len(candidate.Evidence) != 1 || candidate.Demotion == "" {
		t.Fatalf("kept = %#v", result)
	}
}

func TestHundredsOfRootsKeepTheBestRanked(t *testing.T) {
	files := map[string]string{"package.json": nextManifest, "package-lock.json": "{}"}
	for index := 0; index < 90; index++ {
		files[fmt.Sprintf("fixtures/case%02d/go.mod", index)] = "module example.test/case\n\ngo 1.26\n"
	}
	result := detectShapeFixture(t, files)
	if len(result.Candidates) != shapeMaxCandidates || result.Candidates[0].Root != "" || selectedOf(result) == nil ||
		!strings.Contains(setAsideKind(result, "decoy").Reason, "27 lower-ranked candidates") {
		t.Fatalf("candidates = %d, selected = %v, set aside = %#v", len(result.Candidates), selectedOf(result), result.SetAside)
	}
	result.Source.Kind = SourceGit
	if err := validateDetectionResult(&DraftSourceConfig{Kind: SourceGit, Mode: SourceModeLocalCheckout}, result); err != nil {
		t.Fatalf("bounded result refused: %v", err)
	}
}

// A package.json that only runs tooling owns no static files and ranks below
// the site the repository publishes; a program with a landing page stays the
// program, so its code is never served as files.
func TestToolingPackageNeverHidesTheSite(t *testing.T) {
	tooling := `{"name":"site","private":true,"scripts":{"format":"prettier --write .","prepare":"husky"},"devDependencies":{"prettier":"3","husky":"9"}}`
	for _, folder := range []string{"public", "site", "web", "docs"} {
		t.Run("tooling root and "+folder, func(t *testing.T) {
			result := detectShapeFixture(t, map[string]string{
				"package.json": tooling, "package-lock.json": "{}", folder + "/index.html": "<html></html>",
			})
			selected := selectedOf(result)
			root := candidateAtRoot(result, "", BuildRecipe)
			if selected == nil || selected.Root != folder || selected.BuildMethod != BuildStatic || selected.Demotion != "" {
				t.Fatalf("selected = %#v, candidates = %#v", selected, result.Candidates)
			}
			if root == nil || !strings.Contains(root.Demotion, "only runs tooling") {
				t.Fatalf("tooling root = %#v", root)
			}
		})
	}
	t.Run("npm init's main with no such file is still tooling", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json":      `{"name":"site","main":"index.js","scripts":{"build":"tailwindcss -i in.css -o out.css"},"devDependencies":{"tailwindcss":"4"}}`,
			"package-lock.json": "{}", "index.html": "<html></html>", "about/index.html": "<html></html>",
		})
		if len(result.Candidates) != 1 || result.Candidates[0].Profile != ProfileStatic || result.Candidates[0].OutputDirectory != "." ||
			!evidenceMentions(&result.Candidates[0], "1 more pages") {
			t.Fatalf("tooling site = %#v", result.Candidates)
		}
	})
	for name, files := range map[string]map[string]string{
		"discord bot with a landing page": {
			"package.json": `{"name":"bot","main":"index.js","dependencies":{"discord.js":"14"}}`, "package-lock.json": "{}",
			"index.js": "require('discord.js')\n", "index.html": "<html></html>",
		},
		"script with a main file and a landing page": {
			"package.json": `{"name":"bot","main":"bot.js","dependencies":{"some-sdk":"1"}}`, "package-lock.json": "{}",
			"bot.js": "", "index.html": "<html></html>",
		},
	} {
		t.Run(name, func(t *testing.T) {
			result := detectShapeFixture(t, files)
			for _, candidate := range result.Candidates {
				if candidate.Profile == ProfileStatic || candidate.BuildMethod == BuildStatic || candidate.OutputDirectory != "" {
					t.Fatalf("a program became a static site: %#v", result.Candidates)
				}
			}
			if root := candidateAtRoot(result, "", BuildRecipe); root == nil || root.StartCommand == "" || root.Demotion != "" {
				t.Fatalf("program = %#v", result.Candidates)
			}
		})
	}
	t.Run("an application still owns its public folder", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json": `{"name":"bot","main":"index.js","dependencies":{"discord.js":"14"}}`, "package-lock.json": "{}",
			"index.js": "", "public/index.html": "<html></html>",
		})
		if len(result.Candidates) != 1 || setAsideKind(result, "static-files") == nil {
			t.Fatalf("candidates = %#v", result.Candidates)
		}
	})
}

// The breadth-first pass and the lexical pass share one judgement of each
// directory, so a pruned directory is recorded once and never entered.
func TestWalkJudgesEachDirectoryOnce(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{"package.json", "a/b/package.json", "a/b/c.txt", "skip/package.json", "skip/deep/x.txt"} {
		writeBuildFixture(t, root, file, "{}")
	}
	directories := map[string]int{}
	files := map[string]int{}
	err := walkDetectionTree(root, func(name string) bool { return name == "package.json" }, func(path string, entry fs.DirEntry, err error) error {
		rel, _ := filepath.Rel(root, path)
		if entry.IsDir() {
			directories[filepath.ToSlash(rel)]++
			if entry.Name() == "skip" {
				return filepath.SkipDir
			}
			return nil
		}
		files[filepath.ToSlash(rel)]++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for directory, visits := range directories {
		if visits != 1 {
			t.Fatalf("%s judged %d times: %v", directory, visits, directories)
		}
	}
	if directories["skip/deep"] != 0 || files["skip/package.json"] != 0 || files["skip/deep/x.txt"] != 0 {
		t.Fatalf("a pruned directory was entered: %v, %v", directories, files)
	}
	for _, file := range []string{"package.json", "a/b/package.json", "a/b/c.txt"} {
		if files[file] != 1 {
			t.Fatalf("%s visited %d times: %v", file, files[file], files)
		}
	}
}
