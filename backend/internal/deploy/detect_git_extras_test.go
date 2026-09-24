package deploy

import (
	"fmt"
	"strings"
	"testing"
)

func TestGitModulesAreJudgedAgainstTheSourceHost(t *testing.T) {
	submodules := parseGitModules([]byte(`[submodule "themes/ananke"]
	path = themes/ananke
	url = https://github.com/theNewDynamic/gohugo-theme-ananke.git
[submodule "shared"]
	path = shared
	url = ../shared.git
[submodule "vendor/private"]
	path = vendor/private
	url = git@gitlab.example.com:team/private.git
[submodule "../escape"]
	path = ../escape
	url = ../escape.git
`), "https://github.com/owner/site.git")
	if len(submodules) != 3 {
		t.Fatalf("submodules = %#v", submodules)
	}
	want := map[string]bool{"themes/ananke": true, "shared": true, "vendor/private": false}
	for _, submodule := range submodules {
		if want[submodule.Path] != submodule.SameSource {
			t.Fatalf("%s same source = %t", submodule.Path, submodule.SameSource)
		}
	}
}

func TestGitAttributesPatternsFollowGitRules(t *testing.T) {
	scan := newRepoShapeScan(t.TempDir(), DetectionLimits{}.normalized())
	scan.readGitAttributes(".gitattributes", []byte("*.png filter=lfs diff=lfs merge=lfs -text\nassets/models/** filter=lfs\n*.txt text\n"))
	scan.readGitAttributes("web/.gitattributes", []byte("/fonts/*.woff2 filter=lfs\n"))
	for path, want := range map[string]bool{
		"logo.png": true, "deep/dir/logo.png": true, "assets/models/a/b.bin": true, "models/b.bin": false,
		"notes.txt": false, "web/fonts/a.woff2": true, "fonts/a.woff2": false, "web/other/fonts/a.woff2": false,
	} {
		if got := scan.lfsTracked(path); got != want {
			t.Fatalf("lfsTracked(%s) = %t", path, got)
		}
	}
}

func TestSubmodulesAndLFSAreDecidedPerBuildRoot(t *testing.T) {
	result := detectShapeFixture(t, map[string]string{
		"index.html":      "<html></html>",
		".gitmodules":     "[submodule \"themes/x\"]\n\tpath = themes/x\n\turl = ../x.git\n",
		".gitattributes":  "*.png filter=lfs\n",
		"images/logo.png": "version https://git-lfs.github.com/spec/v1\noid sha256:abc\nsize 12\n",
	}, SourceIdentity{Remote: "https://github.com/owner/site.git"})
	requirements := result.GitRequirements
	if !requirements.SubmodulesChecked || len(requirements.SubmoduleList) != 1 || !requirements.SubmoduleList[0].SameSource ||
		!requirements.LFSChecked || requirements.LFSFiles != 1 {
		t.Fatalf("requirements = %#v", requirements)
	}
	if len(result.Candidates) != 1 || len(result.Candidates[0].NeedsDecision) != 0 {
		t.Fatalf("a same-host submodule still owes a decision: %#v", result.Candidates)
	}
	source := &DraftSourceConfig{Kind: SourceGit}
	configuration := PlanConfiguration{Build: BuildPlanConfig{Method: BuildStatic}}
	withLFS := HostObservation{Facilities: map[string]FacilityObservation{"git-lfs": {Available: true}}}
	withoutLFS := HostObservation{Facilities: map[string]FacilityObservation{}}

	findings := gitRequirementFindings(source, &result, configuration, withLFS)
	if !hasFinding(findings, "git_submodules", PreflightWarning) || !hasFinding(findings, "git_lfs", PreflightWarning) {
		t.Fatalf("excluded requirements = %#v", findings)
	}
	source.IncludeSubmodules, source.IncludeLFS = true, true
	findings = gitRequirementFindings(source, &result, configuration, withLFS)
	if !hasFinding(findings, "git_submodules", PreflightPass) || !hasFinding(findings, "git_lfs", PreflightPass) {
		t.Fatalf("included requirements = %#v", findings)
	}
	findings = gitRequirementFindings(source, &result, configuration, withoutLFS)
	if !hasFinding(findings, "git_lfs_unavailable", PreflightBlocked) {
		t.Fatalf("missing git-lfs = %#v", findings)
	}
	elsewhere := result
	elsewhere.GitRequirements.SubmoduleList = []GitSubmodule{{Path: "themes/x", SameSource: false}}
	source.IncludeSubmodules = false
	if findings := gitRequirementFindings(source, &elsewhere, configuration, withLFS); !hasFinding(findings, "git_submodules", PreflightDecision) {
		t.Fatalf("another host's submodule = %#v", findings)
	}
	outside := result
	outside.GitRequirements.LFSPaths = []string{"images/logo.png"}
	outside.GitRequirements.SubmoduleList = []GitSubmodule{{Path: "themes/x"}}
	subdirectory := PlanConfiguration{Build: BuildPlanConfig{Method: BuildStatic, RootDirectory: "site"}}
	findings = gitRequirementFindings(source, &outside, subdirectory, withLFS)
	if !hasFinding(findings, "git_submodules", PreflightPass) || !hasFinding(findings, "git_lfs", PreflightPass) {
		t.Fatalf("requirements outside the build root = %#v", findings)
	}
}

// A nested root's LFS count comes from the listed paths; when the list was
// cut short, a zero is unknown and warns instead of passing. A submodule the
// root lies inside is one the root needs.
func TestLFSCountBeyondTheListIsUnknown(t *testing.T) {
	paths := make([]string, 256)
	for index := range paths {
		paths[index] = fmt.Sprintf("assets/%03d.png", index)
	}
	detection := DetectionResult{GitRequirements: GitRequirements{
		LFS: true, LFSChecked: true, LFSFiles: 300, LFSPaths: paths,
		Submodules: true, SubmodulesChecked: true, SubmoduleList: []GitSubmodule{{Path: "site", SameSource: true}},
	}}
	nested := PlanConfiguration{Build: BuildPlanConfig{Method: BuildStatic, RootDirectory: "site/public"}}
	observation := HostObservation{Facilities: map[string]FacilityObservation{"git-lfs": {Available: true}}}
	findings := gitRequirementFindings(&DraftSourceConfig{Kind: SourceGit}, &detection, nested, observation)
	if !hasFinding(findings, "git_lfs", PreflightWarning) || hasFinding(findings, "git_lfs", PreflightPass) {
		t.Fatalf("unknown count = %#v", findings)
	}
	if !hasFinding(findings, "git_submodules", PreflightWarning) {
		t.Fatalf("a root inside a submodule did not need it: %#v", findings)
	}
	detection.GitRequirements.LFSFiles = 256
	if findings := gitRequirementFindings(&DraftSourceConfig{Kind: SourceGit}, &detection, nested, observation); !hasFinding(findings, "git_lfs", PreflightPass) {
		t.Fatalf("an exact zero = %#v", findings)
	}
}

func TestCaseMismatchedImportsAreFound(t *testing.T) {
	result := detectShapeFixture(t, map[string]string{
		"package.json": nextManifest, "package-lock.json": "{}",
		"components/Header.tsx": "export default function Header() {}\n",
		"components/footer.tsx": "export default function Footer() {}\n",
		"lib/util.ts":           "export const x = 1\n",
		"app/page.tsx": "import Header from '../components/header'\nimport Footer from '../components/footer'\n" +
			"import { x } from '../lib/util.js'\nimport styles from '@/styles/home.module.css'\nimport missing from './gone'\n",
	})
	candidate := selectedOf(result)
	if len(candidate.ImportCaseMismatches) != 1 {
		t.Fatalf("mismatches = %#v", candidate.ImportCaseMismatches)
	}
	mismatch := candidate.ImportCaseMismatches[0]
	if mismatch.File != "app/page.tsx" || mismatch.Line != 1 || mismatch.Specifier != "../components/header" || mismatch.Actual != "components/Header.tsx" {
		t.Fatalf("mismatch = %#v", mismatch)
	}
	findings := repoShapeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "node"}})
	if !hasFinding(findings, "import_case_mismatch", PreflightBlocked) {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestPSR4CaseMismatchIsAWarning(t *testing.T) {
	result := detectShapeFixture(t, map[string]string{
		"composer.json": `{"require":{"laravel/framework":"^12.0"},"autoload":{"psr-4":{"App\\":"app/"}}}`, "artisan": "",
		"app/Http/controllers/UserController.php": "<?php\nnamespace App\\Http\\Controllers;\n\nclass UserController {}\n",
		"app/Models/User.php":                     "<?php\nnamespace App\\Models;\n\nclass User {}\n",
	})
	candidate := selectedOf(result)
	if len(candidate.ImportCaseMismatches) != 1 || candidate.ImportCaseMismatches[0].Actual != "app/Http/Controllers/UserController.php" {
		t.Fatalf("mismatches = %#v", candidate.ImportCaseMismatches)
	}
	findings := repoShapeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "php"}})
	if !hasFinding(findings, "import_case_mismatch", PreflightWarning) {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestServerlessAndEdgeCodeIsNamed(t *testing.T) {
	t.Run("cloudflare vite plugin", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json":      `{"name":"app","scripts":{"build":"vite build"},"devDependencies":{"vite":"6","@cloudflare/vite-plugin":"1","wrangler":"4"}}`,
			"package-lock.json": "{}",
			"wrangler.jsonc":    "{\n  // Worker\n  \"main\": \"worker/index.ts\",\n  \"assets\": { \"not_found_handling\": \"single-page-application\" },\n}\n",
		})
		candidate := selectedOf(result)
		if candidate.OutputDirectory != "dist/client" || len(candidate.ServerlessCode) != 1 || !candidate.ServerlessCode[0].Blocking ||
			candidate.ServerlessCode[0].Entry != "worker/index.ts" {
			t.Fatalf("cloudflare = %#v", candidate)
		}
		findings := repoShapeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}})
		if !hasFinding(findings, "edge_runtime_code_not_deployed", PreflightBlocked) {
			t.Fatalf("findings = %#v", findings)
		}
	})
	t.Run("vercel functions beside a vite site", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json": viteManifest, "package-lock.json": "{}", "vercel.json": "{}",
			"api/hello.ts": "export default function handler() {}\n", "api/users/[id].ts": "",
		})
		candidate := selectedOf(result)
		if len(candidate.ServerlessCode) != 1 || candidate.ServerlessCode[0].Platform != "vercel" ||
			strings.Join(candidate.ServerlessCode[0].Paths, ",") != "/api/hello,/api/users/[id]" {
			t.Fatalf("vercel = %#v", candidate.ServerlessCode)
		}
		findings := repoShapeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}})
		if !hasFinding(findings, "serverless_functions_dropped", PreflightWarning) {
			t.Fatalf("findings = %#v", findings)
		}
	})
	t.Run("next.js on the OpenNext adapter", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json":      `{"name":"web","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16","react":"19"},"devDependencies":{"@opennextjs/cloudflare":"1","wrangler":"4"}}`,
			"package-lock.json": "{}",
			"wrangler.jsonc":    "{\n  \"main\": \".open-next/worker.js\",\n  \"assets\": { \"directory\": \".open-next/assets\" }\n}\n",
		})
		candidate := selectedOf(result)
		if candidate == nil || len(candidate.ServerlessCode) != 0 || !evidenceMentions(candidate, "framework's Cloudflare adapter") {
			t.Fatalf("opennext = %#v", candidate)
		}
		findings := repoShapeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}})
		if hasFinding(findings, "edge_runtime_code_not_deployed", PreflightBlocked) || hasFinding(findings, "edge_runtime_code_not_deployed", PreflightWarning) {
			t.Fatalf("findings = %#v", findings)
		}
	})
	t.Run("a Worker beside an application with its own server", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json": nextManifest, "package-lock.json": "{}",
			"wrangler.toml": "name = \"edge\"\nmain = \"src/edge.ts\"\n",
		})
		candidate := selectedOf(result)
		if candidate == nil || len(candidate.ServerlessCode) != 1 || candidate.ServerlessCode[0].Blocking {
			t.Fatalf("worker beside next = %#v", candidate)
		}
		findings := repoShapeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}})
		if !hasFinding(findings, "edge_runtime_code_not_deployed", PreflightWarning) || hasFinding(findings, "serverless_functions_dropped", PreflightWarning) {
			t.Fatalf("findings = %#v", findings)
		}
	})
	t.Run("a Worker-only application", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json":      `{"name":"api","scripts":{"dev":"wrangler dev","deploy":"wrangler deploy"},"dependencies":{"hono":"4"},"devDependencies":{"wrangler":"4"}}`,
			"package-lock.json": "{}",
			"wrangler.toml":     "name = \"api\"\nmain = \"src/index.ts\"\n",
		})
		candidate := candidateAtRoot(result, "", BuildRecipe)
		if candidate == nil || len(candidate.ServerlessCode) != 1 || !candidate.ServerlessCode[0].Blocking {
			t.Fatalf("worker-only = %#v", candidate)
		}
	})
	t.Run("next.js api routes are its own", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json": nextManifest, "package-lock.json": "{}", "app/api/health/route.ts": "",
		})
		if len(selectedOf(result).ServerlessCode) != 0 {
			t.Fatalf("next = %#v", selectedOf(result).ServerlessCode)
		}
	})
}

func TestUpstreamRepositoryOffersItsTemplateAndImage(t *testing.T) {
	result := detectShapeFixture(t, map[string]string{
		"package.json": `{"name":"n8n-monorepo","private":true,"workspaces":["packages/*"]}`, "pnpm-lock.yaml": "",
		".github/workflows/docker.yml": "jobs:\n  build:\n    steps:\n      - uses: docker/build-push-action@v6\n" +
			"        with:\n          tags: ghcr.io/${{ github.repository_owner }}/n8n:latest\n",
	}, SourceIdentity{Kind: SourceGit, Remote: "https://github.com/n8n-io/n8n.git"})
	kinds := map[string]string{}
	for _, alternative := range result.Alternatives {
		kinds[alternative.Kind] = alternative.Ref
	}
	if kinds["template"] != "n8n" || kinds["image"] != "ghcr.io/n8n-io/n8n" {
		t.Fatalf("alternatives = %#v", result.Alternatives)
	}
	findings := repoShapeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}})
	if !hasFinding(findings, "source_has_packaged_release", PreflightWarning) {
		t.Fatalf("findings = %#v", findings)
	}
	if repositorySlug("git@github.com:N8N-IO/n8n.git") != "github.com/n8n-io/n8n" || repositorySlug("https://github.com/n8n-io") != "" {
		t.Fatal("repository slugs are not normalized")
	}
}
