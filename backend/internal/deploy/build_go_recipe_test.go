package deploy

import (
	"reflect"
	"strings"
	"testing"
)

// The Go recipe beyond the main package: C code, where dependencies come
// from, generated code, the front end a server embeds, and the command line
// a binary serves from. Each case is a module detection reads, the plan
// preflight judges, and the Dockerfile the recipe renders.

func prepareGo(t *testing.T, root string, config BuildPlanConfig) PreparedBuild {
	t.Helper()
	config.Method, config.Recipe = BuildRecipe, "go"
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(t.Context(), root, config, false, "fixture:go")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	return prepared
}

func detectGo(t *testing.T, root, candidateRoot string) (DetectionResult, *DetectedCandidate) {
	t.Helper()
	detection, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	for index := range detection.Candidates {
		if candidate := &detection.Candidates[index]; candidate.Recipe == "go" && candidate.Root == candidateRoot {
			return detection, candidate
		}
	}
	t.Fatalf("no Go candidate at %q: %+v", candidateRoot, detection.Candidates)
	return detection, nil
}

func assertGoDockerfile(t *testing.T, dockerfile string, want ...string) {
	t.Helper()
	for _, line := range want {
		if !strings.Contains(dockerfile, line) {
			t.Fatalf("Dockerfile missing %q:\n%s", line, dockerfile)
		}
	}
}

func TestGoCGODependenciesBuildWithCGOAutomatically(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "go.mod", "module example.com/app\n\ngo 1.26.0\n\nrequire (\n\tgorm.io/driver/sqlite v1.6.0\n\tgithub.com/mattn/go-sqlite3 v1.14.28 // indirect\n)\n")
	writeBuildFixture(t, root, "go.sum", "gorm.io/driver/sqlite v1.6.0/go.mod h1:x\ngithub.com/mattn/go-sqlite3 v1.14.28/go.mod h1:y\n")
	writeBuildFixture(t, root, "main.go", "package main\nimport _ \"gorm.io/driver/sqlite\"\nfunc main() {}\n")
	prepared := prepareGo(t, root, BuildPlanConfig{})
	// go-sqlite3 built with CGO_ENABLED=0 compiles a stub that fails its
	// first query; the recipe compiles the bundled SQLite and links a static
	// binary for the ordinary alpine runtime.
	assertGoDockerfile(t, prepared.DockerfilePreview, "FROM golang:1.26-alpine@sha256:", "RUN apk add --no-cache gcc musl-dev\n",
		"ENV CGO_ENABLED=1 GOTOOLCHAIN=local", `go build -trimpath -tags timetzdata -ldflags='-s -w -linkmode external -extldflags "-static"' -o /out/app ./`,
		"FROM alpine:3.22@sha256:", "RUN apk add --no-cache tzdata && adduser -D -u 10001 app")
	_, candidate := detectGo(t, root, "")
	if candidate.Go == nil || !reflect.DeepEqual(candidate.Go.CGOModules, []string{"github.com/mattn/go-sqlite3"}) || candidate.RecipeIssue != "" {
		t.Fatalf("cgo facts = %+v", candidate.Go)
	}
	item := findingByCode(goBuildFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "go"}}, nil), "go_cgo_enabled")
	if item == nil || item.Severity != PreflightWarning || !strings.Contains(item.Action, "modernc.org/sqlite") || !strings.Contains(item.Means, "statically") {
		t.Fatalf("go_cgo_enabled = %+v", item)
	}

	// libvips is a shared library: the build and the runtime share an
	// Alpine release, and the runtime installs it.
	vips := t.TempDir()
	writeBuildFixture(t, vips, "go.mod", "module example.com/thumbs\n\ngo 1.27\n\nrequire github.com/h2non/bimg v1.1.9\n")
	writeBuildFixture(t, vips, "go.sum", "github.com/h2non/bimg v1.1.9/go.mod h1:x\n")
	writeBuildFixture(t, vips, "main.go", "package main\nfunc main() {}\n")
	prepared = prepareGo(t, vips, BuildPlanConfig{})
	assertGoDockerfile(t, prepared.DockerfilePreview, "FROM golang:1.27-alpine3.24@sha256:", "RUN apk add --no-cache gcc musl-dev vips-dev pkgconf",
		"-ldflags='-s -w' -o /out/app", "FROM alpine:3.24@sha256:", "RUN apk add --no-cache tzdata vips && adduser")

	// A library no package is known for is refused before Deploy, by name.
	local := t.TempDir()
	writeBuildFixture(t, local, "go.mod", "module example.com/native\n\ngo 1.26\n")
	writeBuildFixture(t, local, "main.go", "package main\nfunc main() {}\n")
	writeBuildFixture(t, local, "native.go", "package main\n\n// #cgo LDFLAGS: -lfrobnicate -lm\n// #cgo darwin LDFLAGS: -framework Cocoa\n// #include <frob.h>\nimport \"C\"\n")
	if _, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(t.Context(), local, BuildPlanConfig{Method: BuildRecipe, Recipe: "go"}, false, "t:1"); err == nil ||
		!strings.Contains(err.Error(), "-lfrobnicate") {
		t.Fatalf("unknown library: %v", err)
	}
	_, candidate = detectGo(t, local, "")
	if !strings.Contains(candidate.RecipeIssue, "-lfrobnicate") || candidate.Confidence != ConfidenceLow {
		t.Fatalf("unknown library was not marked: %+v", candidate)
	}
	if item := findingByCode(goBuildFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "go"}}, nil), "go_cgo_library_unknown"); item == nil || item.Severity != PreflightBlocked {
		t.Fatalf("go_cgo_library_unknown = %+v", item)
	}
}

func TestGoCgoDirectivesAndEmbedPatternsAreReadForLinux(t *testing.T) {
	links := goCgoLinks([]byte("package x\n\n/*\n#cgo pkg-config: vips\n#cgo linux,amd64 LDFLAGS: -lz\n#cgo windows LDFLAGS: -lws2_32\n#cgo !windows LDFLAGS: -lm -L/opt/lib\n*/\nimport \"C\"\n"))
	if want := []string{"pkg-config:vips", "lib:m"}; !reflect.DeepEqual(links[:1], want[:1]) || !strings.Contains(strings.Join(links, " "), "lib:m") ||
		strings.Contains(strings.Join(links, " "), "ws2_32") {
		t.Fatalf("links = %v", links)
	}
	patterns := goEmbedPatterns([]byte("package web\n\nimport \"embed\"\n\n//go:embed all:dist \"templates/*.html\" static\nvar files embed.FS\n"))
	if !reflect.DeepEqual(patterns, []string{"all:dist", "templates/*.html", "static"}) {
		t.Fatalf("patterns = %v", patterns)
	}
}

func TestGoDependencySourcesDecideTheInstall(t *testing.T) {
	// Vendored modules build offline; a download would ignore vendor/ and
	// fail on the private module it holds.
	vendored := t.TempDir()
	writeBuildFixture(t, vendored, "go.mod", "module example.com/app\n\ngo 1.26\n\nrequire git.corp.example/lib v1.0.0\n")
	writeBuildFixture(t, vendored, "vendor/modules.txt", "# git.corp.example/lib v1.0.0\n## explicit\ngit.corp.example/lib\n")
	writeBuildFixture(t, vendored, "main.go", "package main\nfunc main() {}\n")
	prepared := prepareGo(t, vendored, BuildPlanConfig{})
	assertGoDockerfile(t, prepared.DockerfilePreview, "ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=vendor")
	if strings.Contains(prepared.DockerfilePreview, "go mod download") {
		t.Fatalf("a vendored module downloads:\n%s", prepared.DockerfilePreview)
	}
	_, candidate := detectGo(t, vendored, "")
	if candidate.Go == nil || !candidate.Go.Vendored || findingByCode(goBuildFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}, nil), "go_vendored") == nil {
		t.Fatalf("vendoring not recorded: %+v", candidate.Go)
	}

	// No go.sum: the build records checksums instead of refusing, and says so.
	unsummed := t.TempDir()
	writeBuildFixture(t, unsummed, "go.mod", "module example.com/app\n\ngo 1.26\n\nrequire github.com/go-chi/chi/v5 v5.2.1\n")
	writeBuildFixture(t, unsummed, "main.go", "package main\nfunc main() {}\n")
	prepared = prepareGo(t, unsummed, BuildPlanConfig{})
	assertGoDockerfile(t, prepared.DockerfilePreview, "GOFLAGS=-mod=mod", "RUN go mod download")
	_, candidate = detectGo(t, unsummed, "")
	if item := findingByCode(goBuildFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}, nil), "go_sum_missing"); item == nil || item.Severity != PreflightWarning {
		t.Fatalf("go_sum_missing = %+v", item)
	}
	writeBuildFixture(t, unsummed, "go.sum", "github.com/go-chi/chi/v5 v5.0.0/go.mod h1:x\n")
	_, candidate = detectGo(t, unsummed, "")
	if item := findingByCode(goBuildFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}, nil), "lockfile_out_of_sync"); item == nil ||
		!strings.Contains(item.Measured, "github.com/go-chi/chi/v5") {
		t.Fatalf("stale go.sum = %+v", item)
	}
	module := parseGoMod([]byte("module m\nrequire (\n\ta.example/x v1.0.0\n\tb.example/y v1.0.0\n\tc.example/z v1.0.0\n)\nreplace b.example/y => ../y\nreplace c.example/z v1.0.0 => d.example/z v2.0.0\n"))
	if missing := goSumMissing(module, []byte("a.example/x v1.0.0/go.mod h1:x\nd.example/z v2.0.0/go.mod h1:y\n")); len(missing) != 0 {
		t.Fatalf("replacements were not followed: %v", missing)
	}
}

func TestGoPrivateModulesUseAnInstallScopedToken(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "go.mod", "module github.com/acme/app\n\ngo 1.26\n\nrequire (\n\tgithub.com/acme/shared-lib v0.3.0\n\tgithub.com/go-chi/chi/v5 v5.2.1\n)\n")
	writeBuildFixture(t, root, "go.sum", "github.com/acme/shared-lib v0.3.0/go.mod h1:x\ngithub.com/go-chi/chi/v5 v5.2.1/go.mod h1:y\n")
	writeBuildFixture(t, root, "main.go", "package main\nfunc main() {}\n")
	_, candidate := detectGo(t, root, "")
	if candidate.Go == nil || !reflect.DeepEqual(candidate.Go.OwnerModules, []string{"github.com/acme/shared-lib"}) {
		t.Fatalf("owner modules = %+v", candidate.Go)
	}
	source := &DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, CredentialID: 7}
	item := findingByCode(goBuildFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}, source), "go_private_module")
	if item == nil || item.Severity != PreflightWarning || !strings.Contains(item.Action, "GIT_TOKEN") {
		t.Fatalf("go_private_module = %+v", item)
	}
	if item := findingByCode(goBuildFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}, &DraftSourceConfig{Kind: SourceGit}), "go_private_module"); item != nil {
		t.Fatalf("a public source was warned: %+v", item)
	}
	config := BuildPlanConfig{Secrets: []BuildSecretConfig{{Variable: "GIT_TOKEN", Step: "install"}}}
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(t.Context(), root, BuildPlanConfig{Method: BuildRecipe, Recipe: "go", Secrets: config.Secrets}, false, "t:1", "GIT_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	assertGoDockerfile(t, prepared.DockerfilePreview, "RUN apk add --no-cache git", "GOPRIVATE=github.com/acme",
		`RUN --mount=type=secret,id=GIT_TOKEN,env=GIT_TOKEN,required=true export GIT_CONFIG_COUNT="1" GIT_CONFIG_KEY_0="url.https://x-access-token:${GIT_TOKEN}@github.com/.insteadOf" GIT_CONFIG_VALUE_0="https://github.com/" && go mod download`)
	configured := PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Secrets: config.Secrets}}
	if item := findingByCode(goBuildFindings(candidate, configured, source), "go_private_module"); item == nil || item.Severity != PreflightPass {
		t.Fatalf("a configured token was not recognised: %+v", item)
	}
}

func TestGoTemplComponentsAreGeneratedBeforeTheBuild(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "go.mod", "module example.com/goth\n\ngo 1.26\n\nrequire github.com/a-h/templ v0.3.1020\n")
	writeBuildFixture(t, root, "go.sum", "github.com/a-h/templ v0.3.1020/go.mod h1:x\n")
	writeBuildFixture(t, root, "main.go", "package main\nfunc main() {}\n")
	writeBuildFixture(t, root, "views/index.templ", "package views\n\ntempl Index() { <h1>hi</h1> }\n")
	writeBuildFixture(t, root, "views/layout.templ", "package views\n")
	writeBuildFixture(t, root, "views/layout_templ.go", "package views\n")
	prepared := prepareGo(t, root, BuildPlanConfig{})
	assertGoDockerfile(t, prepared.DockerfilePreview, "RUN GOFLAGS= go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate\n")
	if strings.Index(prepared.DockerfilePreview, "templ@v0.3.1020 generate") > strings.Index(prepared.DockerfilePreview, "go build -trimpath") {
		t.Fatal("templ generated after the build")
	}
	writeBuildFixture(t, root, "go.mod", "module example.com/goth\n\ngo 1.26\n\ntool github.com/a-h/templ/cmd/templ\n\nrequire github.com/a-h/templ v0.3.1020\n")
	prepared = prepareGo(t, root, BuildPlanConfig{})
	assertGoDockerfile(t, prepared.DockerfilePreview, "RUN go tool templ generate\n")
	_, candidate := detectGo(t, root, "")
	if candidate.Go == nil || candidate.Go.Codegen != "go tool templ generate" {
		t.Fatalf("templ = %+v", candidate.Go)
	}
	writeBuildFixture(t, root, "Makefile", "css:\n\tnpx tailwindcss -i ./static/input.css -o ./static/output.css --minify\n")
	_, candidate = detectGo(t, root, "")
	if item := findingByCode(goBuildFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}, nil), "go_codegen_missing"); item == nil ||
		item.Measured != "./static/output.css" {
		t.Fatalf("go_codegen_missing = %+v", item)
	}
}

func TestGoServerEmbeddingAFrontEndBuildsItFirst(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "go.mod", "module example.com/app\n\ngo 1.26\n")
	writeBuildFixture(t, root, "main.go", "package main\n\nimport (\n\t\"embed\"\n\t\"net/http\"\n)\n\n//go:embed all:web/dist\nvar site embed.FS\n\nfunc main() { http.ListenAndServe(\":8080\", http.FileServerFS(site)) }\n")
	writeBuildFixture(t, root, "web/package.json", `{"name":"web","private":true,"scripts":{"build":"vite build"},"devDependencies":{"vite":"^7.0.0"}}`)
	writeBuildFixture(t, root, "web/index.html", "<div id=app></div>")
	writeBuildFixture(t, root, "web/dist/.gitkeep", "")
	prepared := prepareGo(t, root, BuildPlanConfig{})
	web := prepared.DockerfilePreview[:strings.Index(prepared.DockerfilePreview, "AS build")]
	assertGoDockerfile(t, web, "FROM node:22-alpine@sha256:", "AS web\n", "WORKDIR /app\nCOPY . .\nWORKDIR /app/web\n", "npm run build")
	assertGoDockerfile(t, prepared.DockerfilePreview, "COPY --from=web /app/web/dist/ /src/web/dist/\nRUN go build")
	detection, candidate := detectGo(t, root, "")
	if selected := selectedDetectionCandidate(&detection); selected == nil || selected.ID != candidate.ID {
		t.Fatalf("the embedded front end tied with its server: %+v", detection.Candidates)
	}
	if candidate.Go == nil || len(candidate.Go.Embeds) != 1 || candidate.Go.Embeds[0].Frontend != "web" || candidate.RecipeIssue != "" {
		t.Fatalf("embeds = %+v", candidate.Go)
	}
	if item := findingByCode(goBuildFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}, nil), "go_embed_frontend"); item == nil || item.Severity != PreflightPass {
		t.Fatalf("go_embed_frontend = %+v", item)
	}

	// A Vite outDir that writes into the Go tree is followed.
	outDir := t.TempDir()
	writeBuildFixture(t, outDir, "go.mod", "module example.com/app\n\ngo 1.26\n")
	writeBuildFixture(t, outDir, "main.go", "package main\n\nimport _ \"example.com/app/internal/web\"\n\nfunc main() {}\n")
	writeBuildFixture(t, outDir, "internal/web/embed.go", "package web\n\nimport \"embed\"\n\n//go:embed dist\nvar Dist embed.FS\n")
	writeBuildFixture(t, outDir, "frontend/package.json", `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"^7.0.0"}}`)
	writeBuildFixture(t, outDir, "frontend/vite.config.ts", "export default { build: { outDir: '../internal/web/dist', emptyOutDir: true } }\n")
	prepared = prepareGo(t, outDir, BuildPlanConfig{})
	assertGoDockerfile(t, prepared.DockerfilePreview, "WORKDIR /app/frontend\n", "COPY --from=web /app/internal/web/dist/ /src/internal/web/dist/")

	// Nothing builds it: refused before Deploy, not by go build.
	missing := t.TempDir()
	writeBuildFixture(t, missing, "go.mod", "module example.com/app\n\ngo 1.26\n")
	writeBuildFixture(t, missing, "main.go", "package main\n\nimport \"embed\"\n\n//go:embed static\nvar static embed.FS\n\nfunc main() {}\n")
	_, candidate = detectGo(t, missing, "")
	if !strings.Contains(candidate.RecipeIssue, "//go:embed static") {
		t.Fatalf("missing embed not refused: %+v", candidate)
	}
	if item := findingByCode(goBuildFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}, nil), "go_embed_missing"); item == nil || item.Severity != PreflightBlocked {
		t.Fatalf("go_embed_missing = %+v", item)
	}
	custom := PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, BuildCommand: "make assets && go build -o /out/app ."}}
	if item := findingByCode(goBuildFindings(candidate, custom, nil), "go_embed_missing"); item != nil {
		t.Fatal("a build command that produces the files was refused")
	}
}

func TestGoWorkspaceAndLocalReplacementsWidenTheBuildContext(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "go.work", "go 1.26\n\nuse (\n\t./api\n\t./shared\n)\n")
	writeBuildFixture(t, root, "api/go.mod", "module example.com/api\n\ngo 1.26\n\nrequire example.com/shared v0.0.0\n")
	writeBuildFixture(t, root, "api/main.go", "package main\n\nimport \"net/http\"\n\nfunc main() { http.ListenAndServe(\":8080\", nil) }\n")
	writeBuildFixture(t, root, "api/templates/index.html", "<h1>hi</h1>")
	writeBuildFixture(t, root, "shared/go.mod", "module example.com/shared\n\ngo 1.26\n")
	writeBuildFixture(t, root, "shared/shared.go", "package shared\n")
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).PrepareWithin(t.Context(), root, root+"/api",
		BuildPlanConfig{Method: BuildRecipe, Recipe: "go", RootDirectory: "api"}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.ContextDirectory != "." {
		t.Fatalf("context = %q", prepared.ContextDirectory)
	}
	// go mod download fetches every requirement in a workspace, the sibling
	// the go.work provides included, which no proxy has; listing the
	// packages' imports fetches only what the build compiles.
	assertGoDockerfile(t, prepared.DockerfilePreview, "COPY . .\nWORKDIR /src/api\nENV CGO_ENABLED=0 GOTOOLCHAIN=local\nRUN go list -e -deps ./... >/dev/null\n",
		"COPY --from=build --chown=app:app /src/api/templates /home/app/templates")
	if strings.Contains(prepared.DockerfilePreview, "go mod download") {
		t.Fatalf("a workspace build downloads its sibling module:\n%s", prepared.DockerfilePreview)
	}
	detection, candidate := detectGo(t, root, "api")
	if selected := selectedDetectionCandidate(&detection); selected == nil || selected.ID != candidate.ID {
		t.Fatalf("the library module tied with the service: %+v", detection.Candidates)
	}
	if candidate.Go == nil || !candidate.Go.Workspace || candidate.Go.Context != "." {
		t.Fatalf("workspace = %+v", candidate.Go)
	}

	// A go.work in the module's own directory reaching a sibling widens the
	// context to hold both.
	nested := t.TempDir()
	writeBuildFixture(t, nested, "api/go.work", "go 1.26\n\nuse (\n\t.\n\t../shared\n)\n")
	writeBuildFixture(t, nested, "api/go.mod", "module example.com/api\n\ngo 1.26\n")
	writeBuildFixture(t, nested, "api/main.go", "package main\nfunc main() {}\n")
	writeBuildFixture(t, nested, "shared/go.mod", "module example.com/shared\n\ngo 1.26\n")
	prepared, err = NewArtifactBuilder(&artifactBackendFake{}).PrepareWithin(t.Context(), nested, nested+"/api",
		BuildPlanConfig{Method: BuildRecipe, Recipe: "go", RootDirectory: "api"}, false, "t:1")
	if err != nil || prepared.ContextDirectory != "." || !strings.Contains(prepared.DockerfilePreview, "WORKDIR /src/api\nENV CGO_ENABLED=0 GOTOOLCHAIN=local\n") {
		t.Fatalf("go.work beside the module: %v %q\n%s", err, prepared.ContextDirectory, prepared.DockerfilePreview)
	}

	replaced := t.TempDir()
	writeBuildFixture(t, replaced, "services/api/go.mod", "module example.com/api\n\ngo 1.26\n\nrequire example.com/shared v0.0.0\n\nreplace example.com/shared => ../../libs/shared\n")
	writeBuildFixture(t, replaced, "services/api/main.go", "package main\nfunc main() {}\n")
	writeBuildFixture(t, replaced, "libs/shared/go.mod", "module example.com/shared\n\ngo 1.26\n")
	prepared, err = NewArtifactBuilder(&artifactBackendFake{}).PrepareWithin(t.Context(), replaced, replaced+"/services/api",
		BuildPlanConfig{Method: BuildRecipe, Recipe: "go", RootDirectory: "services/api"}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	assertGoDockerfile(t, prepared.DockerfilePreview, "WORKDIR /src/services/api\nENV CGO_ENABLED=0 GOTOOLCHAIN=local GOWORK=off\nRUN go mod download\n")
	if prepared.ContextDirectory != "." {
		t.Fatalf("context = %q", prepared.ContextDirectory)
	}

	outside := t.TempDir()
	writeBuildFixture(t, outside, "go.mod", "module example.com/api\n\ngo 1.26\n\nrequire example.com/shared v0.0.0\n\nreplace example.com/shared => ../shared\n")
	writeBuildFixture(t, outside, "main.go", "package main\nfunc main() {}\n")
	_, candidate = detectGo(t, outside, "")
	if candidate.Go == nil || !reflect.DeepEqual(candidate.Go.ReplacesOutside, []string{"../shared"}) || !strings.Contains(candidate.RecipeIssue, "outside the repository") {
		t.Fatalf("outside replacement = %+v / %q", candidate.Go, candidate.RecipeIssue)
	}
	if item := findingByCode(goBuildFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}, &DraftSourceConfig{Subdirectory: "api"}), "go_local_replace_outside_root"); item == nil ||
		item.Severity != PreflightBlocked || !strings.Contains(item.Action, "subdirectory") {
		t.Fatalf("go_local_replace_outside_root = %+v", item)
	}
}

// A go.work's own lines choose the toolchain as the recipe reads them, and
// the modules it uses are in the checkout, not fetched from the account.
func TestGoWorkspaceLinesAndModulesAreTheWorkspaces(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "go.work", "go 1.27.0\n\nuse (\n\t./api\n\t./shared\n)\n")
	writeBuildFixture(t, root, "api/go.mod", "module github.com/acme/api\n\ngo 1.26.0\n\nrequire (\n\tgithub.com/acme/shared v0.0.0-00010101000000-000000000000\n\tgithub.com/acme/billing v1.2.0\n)\n")
	writeBuildFixture(t, root, "api/main.go", "package main\n\nimport \"net/http\"\n\nfunc main() { http.ListenAndServe(\":8080\", nil) }\n")
	writeBuildFixture(t, root, "shared/go.mod", "module github.com/acme/shared\n\ngo 1.26.0\n")
	_, candidate := detectGo(t, root, "api")
	if candidate.Go == nil || candidate.Go.WorkGo != "1.27.0" || candidate.GoVersion != "1.27" ||
		!reflect.DeepEqual(candidate.Go.OwnerModules, []string{"github.com/acme/billing"}) {
		t.Fatalf("workspace = %q / %+v", candidate.GoVersion, candidate.Go)
	}
	findings := goBuildFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}, &DraftSourceConfig{Kind: SourceGit, CredentialID: 7})
	if item := findingByCode(findings, "go_version_family"); item == nil || !strings.Contains(item.Measured, "maintained Go 1.27 patch") {
		t.Fatalf("go_version_family = %+v", item)
	}
	if item := findingByCode(findings, "go_private_module"); item == nil || item.Measured != "github.com/acme/billing" {
		t.Fatalf("go_private_module = %+v", item)
	}
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).PrepareWithin(t.Context(), root, root+"/api",
		BuildPlanConfig{Method: BuildRecipe, Recipe: "go", RootDirectory: "api"}, false, "t:1")
	if err != nil || prepared.GoVersion != "1.27" {
		t.Fatalf("prepared %q: %v", prepared.GoVersion, err)
	}
	// Go 1.26 cannot build what the go.work asks 1.27 of.
	pinned := PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "go", GoVersion: "1.26"}}
	if item := findingByCode(plannedRecipeFindings(candidate, pinned.Build), "go_version_unsupported"); item == nil || !strings.Contains(item.Measured, "go.work go 1.27.0") {
		t.Fatalf("go_version_unsupported = %+v", item)
	}
}

// A command whose main package is written in cgo is a command, built with
// cgo, not a library.
func TestGoCgoMainPackageIsACommand(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "go.mod", "module example.com/app\n\ngo 1.26\n")
	writeBuildFixture(t, root, "main.go", "package main\n\n// #cgo LDFLAGS: -lm\n// #include <math.h>\nimport \"C\"\n\nimport \"net/http\"\n\nfunc main() { _ = C.sqrt(2); http.ListenAndServe(\":8080\", nil) }\n")
	_, candidate := detectGo(t, root, "")
	if candidate.GoLibrary || candidate.GoPackage != "." || candidate.RecipeIssue != "" || candidate.Go == nil || !reflect.DeepEqual(candidate.Go.CGOLocal, []string{"."}) {
		t.Fatalf("cgo command = %+v / %+v", candidate, candidate.Go)
	}
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "go"}
	if item := findingByCode(plannedRecipeFindings(candidate, build), "go_main_missing"); item != nil {
		t.Fatalf("the cgo command was refused as a library: %+v", item)
	}
	prepared := prepareGo(t, root, BuildPlanConfig{})
	assertGoDockerfile(t, prepared.DockerfilePreview, "RUN apk add --no-cache gcc musl-dev\n", "ENV CGO_ENABLED=1 GOTOOLCHAIN=local", "-o /out/app ./\n")
}

// A module of several commands embeds for each its own files: the service
// is not held to what another command embeds.
func TestGoEmbedsAreTheChosenCommands(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "go.mod", "module example.com/app\n\ngo 1.26\n")
	writeBuildFixture(t, root, "cmd/server/main.go", "package main\n\nimport (\n\t\"net/http\"\n\n\t\"example.com/app/internal/web\"\n)\n\nfunc main() { http.ListenAndServe(\":8080\", http.FileServerFS(web.Dist)) }\n")
	writeBuildFixture(t, root, "internal/web/embed.go", "package web\n\nimport \"embed\"\n\n//go:embed dist/assets\nvar Dist embed.FS\n")
	writeBuildFixture(t, root, "cmd/admin/main.go", "package main\n\nimport \"embed\"\n\n//go:embed ui\nvar ui embed.FS\n\nfunc main() {}\n")
	writeBuildFixture(t, root, "frontend/package.json", `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"^7.0.0"}}`)
	writeBuildFixture(t, root, "frontend/vite.config.ts", "export default { build: { outDir: '../internal/web/dist' } }\n")
	_, candidate := detectGo(t, root, "")
	if candidate.GoPackage != "cmd/server" || candidate.RecipeIssue != "" || candidate.Go == nil || len(candidate.Go.Embeds) != 1 {
		t.Fatalf("server = %q %q / %+v", candidate.GoPackage, candidate.RecipeIssue, candidate.Go)
	}
	prepared := prepareGo(t, root, BuildPlanConfig{})
	// The embed sits inside the Vite output: that directory is copied, not
	// the whole output into it.
	assertGoDockerfile(t, prepared.DockerfilePreview, "COPY --from=web /app/internal/web/dist/assets/ /src/internal/web/dist/assets/\n", "-o /out/app ./cmd/server\n")
	if _, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(t.Context(), root, BuildPlanConfig{Method: BuildRecipe, Recipe: "go", GoPackage: "cmd/admin"}, false, "t:1"); err == nil ||
		!strings.Contains(err.Error(), "//go:embed ui") {
		t.Fatalf("the admin command's missing embed: %v", err)
	}
}

func TestGoSumReadsAreChargedToTheDetectionBudget(t *testing.T) {
	root := t.TempDir()
	module := []byte("module example.com/app\n\ngo 1.26\n\nrequire github.com/go-chi/chi/v5 v5.2.1\n")
	writeBuildFixture(t, root, "go.mod", string(module))
	writeBuildFixture(t, root, "go.sum", "github.com/other/module v1.0.0/go.mod h1:x\n")
	config := BuildPlanConfig{Method: BuildRecipe, Recipe: "go"}
	plan, _ := planGoBuild(root, root, module, goModulePackages{}, "", config, goSumReadLimit)
	if !reflect.DeepEqual(plan.sumStale, []string{"github.com/go-chi/chi/v5"}) || plan.sumBytes == 0 {
		t.Fatalf("stale = %v (%d bytes)", plan.sumStale, plan.sumBytes)
	}
	// Past the budget go.sum is left to the go command, as too large a one is.
	plan, _ = planGoBuild(root, root, module, goModulePackages{}, "", config, 8)
	if len(plan.sumStale) != 0 || plan.sumAbsent || plan.sumBytes != 0 || plan.modFlag() != "" {
		t.Fatalf("a go.sum past the budget was read: %+v", plan)
	}
}

func TestGoCommandLineApplicationsStartTheirServeSubcommand(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "go.mod", "module example.com/tool\n\ngo 1.26\n\nrequire github.com/spf13/cobra v1.9.1\n")
	writeBuildFixture(t, root, "go.sum", "github.com/spf13/cobra v1.9.1/go.mod h1:x\n")
	writeBuildFixture(t, root, "main.go", "package main\n\nimport \"example.com/tool/cmd\"\n\nfunc main() { cmd.Execute() }\n")
	writeBuildFixture(t, root, "cmd/root.go", "package cmd\n\nimport \"github.com/spf13/cobra\"\n\nvar rootCmd = &cobra.Command{Use: \"tool\"}\n\nfunc Execute() { rootCmd.Execute() }\n")
	writeBuildFixture(t, root, "cmd/serve.go", "package cmd\n\nimport \"github.com/spf13/cobra\"\n\nvar serveCmd = &cobra.Command{\n\tUse:   \"serve [flags]\",\n\tShort: \"Serve HTTP\",\n}\n")
	_, candidate := detectGo(t, root, "")
	if candidate.StartCommand != "/app serve" || candidate.Go == nil || candidate.Go.Subcommand != "serve" {
		t.Fatalf("serve subcommand = %q / %+v", candidate.StartCommand, candidate.Go)
	}
	plan := PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, StartCommand: "/app serve"}}
	if item := findingByCode(goBuildFindings(candidate, plan, nil), "go_start_subcommand"); item == nil || item.Severity != PreflightWarning {
		t.Fatalf("go_start_subcommand = %+v", item)
	}
	// server-status is another command, and a tree main never executes
	// serves nothing.
	writeBuildFixture(t, root, "cmd/serve.go", "package cmd\n\nimport \"github.com/spf13/cobra\"\n\nvar statusCmd = &cobra.Command{Use: \"server-status\"}\n")
	if _, candidate = detectGo(t, root, ""); candidate.StartCommand != "" {
		t.Fatalf("server-status started as %q", candidate.StartCommand)
	}
	writeBuildFixture(t, root, "cmd/serve.go", "package cmd\n\nimport \"github.com/spf13/cobra\"\n\nvar serveCmd = &cobra.Command{Use: \"serve\"}\n")
	writeBuildFixture(t, root, "main.go", "package main\n\nimport _ \"example.com/tool/cmd\"\n\nfunc main() {}\n")
	writeBuildFixture(t, root, "cmd/root.go", "package cmd\n\nimport \"github.com/spf13/cobra\"\n\nvar rootCmd = &cobra.Command{Use: \"tool\"}\n")
	if _, candidate = detectGo(t, root, ""); candidate.StartCommand != "" {
		t.Fatalf("a command tree nothing executes started as %q", candidate.StartCommand)
	}

	// PocketBase is proved serving by its own health endpoint.
	pocketbase := t.TempDir()
	writeBuildFixture(t, pocketbase, "go.mod", "module example.com/pb\n\ngo 1.26\n\nrequire github.com/pocketbase/pocketbase v0.29.0\n")
	writeBuildFixture(t, pocketbase, "main.go", "package main\n\nimport \"github.com/pocketbase/pocketbase\"\n\nfunc main() { pocketbase.New().Start() }\n")
	_, candidate = detectGo(t, pocketbase, "")
	if candidate.Readiness == nil || candidate.Readiness.Path != "/api/health" || !strings.Contains(candidate.StartCommand, "serve --http=0.0.0.0:${PORT:-8090}") {
		t.Fatalf("PocketBase = %q / %+v", candidate.StartCommand, candidate.Readiness)
	}
}

func TestGoToolchainFindingsSayWhatBuilds(t *testing.T) {
	candidate := &DetectedCandidate{Recipe: "go", GoMinimumVersion: "1.26.0", GoToolchain: "go1.28.1"}
	if item := findingByCode(goBuildFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}, nil), "go_toolchain_downgraded"); item == nil ||
		item.Severity != PreflightWarning || !strings.Contains(item.Measured, "building with Go 1.27") {
		t.Fatalf("go_toolchain_downgraded = %+v", item)
	}
	candidate = &DetectedCandidate{Recipe: "go", GoMinimumVersion: "1.25"}
	if item := findingByCode(goBuildFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, GoVersion: "1.25.4"}}, nil), "go_version_eol"); item == nil ||
		item.Severity != PreflightWarning {
		t.Fatalf("go_version_eol = %+v", item)
	}
	candidate = &DetectedCandidate{Recipe: "go", GoMinimumVersion: "1.26.0"}
	if item := findingByCode(goBuildFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}, nil), "go_version_family"); item == nil ||
		!strings.Contains(item.Measured, "maintained Go 1.26 patch") {
		t.Fatalf("go_version_family = %+v", item)
	}
	root := t.TempDir()
	writeBuildFixture(t, root, "go.mod", "module example.com/app\n\ngo 1.26.0\n")
	writeBuildFixture(t, root, "main.go", "package main\nfunc main() {}\n")
	prepared := prepareGo(t, root, BuildPlanConfig{})
	if prepared.GoVersion != "1.26" || !strings.Contains(strings.Join(prepared.Notes, "\n"), "go 1.26.0 in go.mod is a minimum") {
		t.Fatalf("version = %q notes = %v", prepared.GoVersion, prepared.Notes)
	}
}

// The findings reach the check before Deploy, and a refusal a finding names
// is not relayed a second time from the candidate's recipeIssue.
func TestCompiledFindingsReachPreflightOnce(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "go.mod", "module example.com/app\n\ngo 1.26\n")
	writeBuildFixture(t, root, "main.go", "package main\n\nimport \"embed\"\n\n//go:embed static\nvar static embed.FS\n\nfunc main() {}\n")
	detection, candidate := detectGo(t, root, "")
	detection.SelectedID = candidate.ID
	draft := &Draft{Data: DraftData{Detection: &detection, Source: &DraftSourceConfig{Kind: SourceGit}, Intent: &DraftIntentConfig{Profile: ProfileService}}}
	findings := preflightFindings(draft, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "go"}}, HostObservation{}, true)
	if findingByCode(findings, "go_embed_missing") == nil || findingByCode(findings, "recipe_unsupported") != nil {
		t.Fatalf("findings = %+v", findings)
	}
}
