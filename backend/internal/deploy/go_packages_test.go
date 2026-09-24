package deploy

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestGoFileConstraintsFollowTheRecipeTarget(t *testing.T) {
	other := "arm64"
	if runtime.GOARCH == "arm64" {
		other = "amd64"
	}
	for _, fixture := range []struct {
		name, file, content string
		built               bool
	}{
		{"plain", "main.go", "package main\n", true},
		{"ignore tag", "gen.go", "//go:build ignore\n\npackage main\n", false},
		{"tools tag", "tools.go", "//go:build tools\n\npackage main\n", false},
		{"mage file", "magefile.go", "//go:build mage\n\npackage main\n", false},
		{"linux tag", "server.go", "//go:build linux\n\npackage main\n", true},
		{"unix tag", "server.go", "//go:build unix && !windows\n\npackage main\n", true},
		{"cgo tag", "native.go", "//go:build cgo\n\npackage main\n", false},
		{"not cgo", "pure.go", "//go:build !cgo\n\npackage main\n", true},
		{"release tag", "new.go", "//go:build go1.22\n\npackage main\n", true},
		{"legacy plus build", "old.go", "// +build ignore\n\npackage main\n", false},
		{"windows suffix", "main_windows.go", "package main\n", false},
		{"linux suffix", "main_linux.go", "package main\n", true},
		{"other arch suffix", "main_linux_" + other + ".go", "package main\n", false},
		{"host arch suffix", "main_linux_" + runtime.GOARCH + ".go", "package main\n", true},
		{"not a suffix", "http_handler.go", "package main\n", true},
		{"malformed", "broken.go", "//go:build (linux\n\npackage main\n", false},
		{"constraint after package", "late.go", "package main\n\n//go:build ignore\n", true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			facts, ok := readGoSourceFacts("cmd/app/"+fixture.file, []byte(fixture.content))
			if !ok || facts.pkg != "main" || facts.dir != "cmd/app" {
				t.Fatalf("facts = %#v, %v", facts, ok)
			}
			if facts.built != fixture.built {
				t.Fatalf("built = %v, want %v", facts.built, fixture.built)
			}
		})
	}
	facts, _ := readGoSourceFacts("mainframe/frame.go", []byte("package mainframe\n"))
	if facts.pkg != "mainframe" {
		t.Fatalf("a package named mainframe was read as %q", facts.pkg)
	}
	facts, _ = readGoSourceFacts("main.go", []byte("package main\n\nimport (\n\t\"C\"\n\t\"github.com/gin-gonic/gin/binding\"\n)\n"))
	if !facts.cgo || !facts.serves {
		t.Fatalf("imports were not read: %#v", facts)
	}
}

func TestGoMainPackageRankingNamesTheServedCommand(t *testing.T) {
	for _, fixture := range []struct {
		name    string
		mains   []string
		serving []string
		module  string
		want    string
	}{
		{"none", nil, nil, "example.test/app", ""},
		{"only one", []string{"cmd/worker"}, nil, "example.test/app", "cmd/worker"},
		{"module root", []string{".", "cmd/migrate"}, nil, "example.test/app", "."},
		{"named after the module", []string{"cmd/api", "cmd/shop"}, nil, "example.test/shop", "cmd/shop"},
		{"major version suffix", []string{"cmd/api", "cmd/shop"}, nil, "example.test/shop/v2", "cmd/shop"},
		{"conventional server", []string{"cmd/server", "cmd/seed"}, nil, "example.test/app", "cmd/server"},
		{"two conventional, one serves", []string{"cmd/api", "cmd/web"}, []string{"cmd/web"}, "example.test/app", "cmd/web"},
		{"only one serves", []string{"cmd/gateway", "cmd/worker"}, []string{"cmd/gateway"}, "example.test/app", "cmd/gateway"},
		{"tie", []string{"cmd/gateway", "cmd/worker"}, nil, "example.test/app", ""},
		{"both serve", []string{"cmd/a", "cmd/b"}, []string{"cmd/a", "cmd/b"}, "example.test/app", ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			serving := map[string]bool{}
			for _, main := range fixture.serving {
				serving[main] = true
			}
			got, reason := chooseGoMainPackage(fixture.mains, serving, fixture.module)
			if got != fixture.want || (got != "" && reason == "") {
				t.Fatalf("chose %q (%q), want %q", got, reason, fixture.want)
			}
		})
	}
}

// The walk the recipe does and the one detection does agree: both leave out
// files Go would never build as the application — testdata, underscore and
// dot directories, nested modules, ignore-tagged generators — and both count
// a cgo file only when nothing restricts it to cgo builds.
func TestGoModuleScanCountsOnlyWhatGoBuilds(t *testing.T) {
	root := t.TempDir()
	for path, content := range map[string]string{
		"go.mod":                    "module example.test/shop\n\ngo 1.26\n",
		"cmd/shop/main.go":          "package main\n\nimport \"net/http\"\n\nfunc main() { _ = http.ListenAndServe }\n",
		"cmd/worker/main.go":        "package main\n\nfunc main() {}\n",
		"internal/gen/gen.go":       "//go:build ignore\n\npackage main\n\nfunc main() {}\n",
		"testdata/prog/main.go":     "package main\n\nfunc main() {}\n",
		"_scratch/main.go":          "package main\n\nfunc main() {}\n",
		".tools/main.go":            "package main\n\nfunc main() {}\n",
		"tools/go.mod":              "module example.test/tools\n",
		"tools/main.go":             "package main\n\nfunc main() {}\n",
		"mainframe/frame.go":        "package mainframe\n",
		"native/native_cgo.go":      "//go:build cgo\n\npackage native\n\nimport \"C\"\n",
		"native/native_fallback.go": "//go:build !cgo\n\npackage native\n",
		"cmd/shop/main_test.go":     "package main\n",
	} {
		writeBuildFixture(t, root, path, content)
	}
	packages, err := scanGoModule(root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(packages.mains, []string{"cmd/shop", "cmd/worker"}) || len(packages.cgo) != 0 || !packages.serving["cmd/shop"] {
		t.Fatalf("scan = %#v", packages)
	}
	detection, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	var candidate *DetectedCandidate
	for index := range detection.Candidates {
		if detection.Candidates[index].Recipe == "go" && detection.Candidates[index].Root == "" {
			candidate = &detection.Candidates[index]
		}
	}
	if candidate == nil || !slices.Equal(candidate.GoMainPackages, packages.mains) || candidate.GoPackage != "cmd/shop" ||
		candidate.RecipeIssue != "" || candidate.Confidence != ConfidenceHigh {
		t.Fatalf("detected Go candidate = %#v", candidate)
	}

	writeBuildFixture(t, root, "cmd/worker/native.go", "package main\n\nimport \"C\"\n")
	if packages, err := scanGoModule(root); err != nil || !slices.Equal(packages.cgo, []string{"cmd/worker"}) {
		t.Fatalf("an unguarded cgo file was not counted: %#v, %v", packages, err)
	}
}

func TestGoDetectionAsksForTheMainPackageAndRefusesALibrary(t *testing.T) {
	ambiguous := t.TempDir()
	writeBuildFixture(t, ambiguous, "go.mod", "module example.test/app\n\ngo 1.26\n")
	writeBuildFixture(t, ambiguous, "cmd/gateway/main.go", "package main\n\nfunc main() {}\n")
	writeBuildFixture(t, ambiguous, "cmd/worker/main.go", "package main\n\nfunc main() {}\n")
	detection, err := (Detector{}).DetectPath(t.Context(), ambiguous, SourceIdentity{})
	if err != nil || len(detection.Candidates) != 1 {
		t.Fatalf("detect: %#v, %v", detection, err)
	}
	candidate := detection.Candidates[0]
	if candidate.GoPackage != "" || !strings.Contains(strings.Join(candidate.NeedsDecision, " "), "./cmd/gateway, ./cmd/worker") {
		t.Fatalf("ambiguous module = %#v", candidate)
	}
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "go"}
	findings := plannedRecipeFindings(&candidate, build)
	if item := findingByCode(findings, "go_main_ambiguous"); item == nil || item.Severity != PreflightDecision ||
		item.FieldID != "configuration.build.goPackage" {
		t.Fatalf("no main package decision: %#v", findings)
	}
	build.GoPackage = "cmd/worker"
	if findings := plannedRecipeFindings(&candidate, build); len(findings) != 0 {
		t.Fatalf("a chosen main package still asked: %#v", findings)
	}
	build.GoPackage = "cmd/missing"
	if item := findingByCode(plannedRecipeFindings(&candidate, build), "go_main_missing"); item == nil || item.Severity != PreflightBlocked {
		t.Fatalf("an unknown main package was accepted")
	}
	build.GoPackage, build.BuildCommand = "", "go build -o /out/app ./cmd/gateway"
	if findings := plannedRecipeFindings(&candidate, build); len(findings) != 0 {
		t.Fatalf("a custom build command was asked for a main package: %#v", findings)
	}

	library := t.TempDir()
	writeBuildFixture(t, library, "go.mod", "module example.test/sdk\n\ngo 1.26\n")
	writeBuildFixture(t, library, "client.go", "package sdk\n")
	detection, err = (Detector{}).DetectPath(t.Context(), library, SourceIdentity{})
	if err != nil || len(detection.Candidates) != 1 {
		t.Fatalf("detect: %#v, %v", detection, err)
	}
	// The repository-shape pass, which read the whole tree, already calls
	// the module a library; source_not_a_service refuses it before Deploy
	// and the recipe's own check does not say it twice.
	candidate = detection.Candidates[0]
	if candidate.NotDeployable != "library" || candidate.GoPackage != "" {
		t.Fatalf("library module = %#v", candidate)
	}
	draft := nodeDraft(detection)
	configuration := nodeTestConfiguration(BuildPlanConfig{Method: BuildRecipe, Recipe: "go"})
	findings = preflightFindings(draft, configuration, HostObservation{}, true)
	if item := findingByCode(findings, "source_not_a_service"); item == nil || item.Severity != PreflightBlocked {
		t.Fatalf("a library module was not refused before deploy: %#v", findings)
	}
	if item := findingByCode(findings, "go_main_missing"); item != nil {
		t.Fatalf("the library was refused twice: %#v", item)
	}
	// Where the shape pass could not tell — its walk stopped before the
	// module's files — the recipe's own scan still finds no command.
	unshaped := newDetectedCandidate("", BuildRecipe, DetectedCandidate{Name: "Go service in .", Recipe: "go", Confidence: ConfidenceHigh})
	applyGoModulePackages(&unshaped, goModulePackages{serving: map[string]bool{}}, nil)
	if !unshaped.GoLibrary || unshaped.Confidence != ConfidenceLow || !strings.HasPrefix(unshaped.Name, "Go library") {
		t.Fatalf("library module = %#v", unshaped)
	}
	if item := findingByCode(plannedRecipeFindings(&unshaped, BuildPlanConfig{Method: BuildRecipe, Recipe: "go"}), "go_main_missing"); item == nil ||
		item.Severity != PreflightBlocked {
		t.Fatal("a library module was not refused before deploy")
	}
}

func TestGoRecipeBuildsTheChosenMainPackage(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "go.mod", "module example.test/app\n\ngo 1.26\n")
	writeBuildFixture(t, root, "cmd/gateway/main.go", "package main\n\nfunc main() {}\n")
	writeBuildFixture(t, root, "cmd/worker/main.go", "package main\n\nfunc main() {}\n")
	builder := NewArtifactBuilder(&artifactBackendFake{})
	config := BuildPlanConfig{Method: BuildRecipe, Recipe: "go"}
	if _, err := builder.Prepare(t.Context(), root, config, false, "fixture:go"); err == nil ||
		!strings.Contains(err.Error(), "./cmd/gateway, ./cmd/worker") {
		t.Fatalf("an undecided module was prepared: %v", err)
	}
	config.GoPackage = "cmd/worker"
	prepared, err := builder.Prepare(t.Context(), root, config, false, "fixture:go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prepared.DockerfilePreview, "go build -trimpath -ldflags='-s -w' -o /out/app ./cmd/worker") {
		t.Fatalf("the chosen package was not built:\n%s", prepared.DockerfilePreview)
	}
	config.GoPackage = "cmd/nothing"
	if _, err := builder.Prepare(t.Context(), root, config, false, "fixture:go"); err == nil ||
		!strings.Contains(err.Error(), "./cmd/nothing is not a buildable package main") {
		t.Fatalf("a directory with no main was prepared: %v", err)
	}
	if err := (PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "go", GoPackage: "../escape"},
		Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst}}).Validate(); err == nil {
		t.Fatal("a Go main package outside the root was accepted")
	}
}

// Detection's facts about main packages come from the recipe's own scan of
// the module, not from the files its walk read before a byte budget ran out:
// a service whose generated or internal code outweighs the budget is still a
// service, and its root command is still the one the plan builds.
func TestGoDetectionReadsMainPackagesPastTheWalkBudget(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "go.mod", "module example.test/store\n\ngo 1.26\n")
	body := "package store\n\n// " + strings.Repeat("x", 400<<10) + "\n"
	for index := range 12 {
		writeBuildFixture(t, root, fmt.Sprintf("internal/store/generated_%02d.go", index), body)
	}
	writeBuildFixture(t, root, "main.go", "package main\n\nimport \"net/http\"\n\nfunc main() { _ = http.ListenAndServe }\n")
	writeBuildFixture(t, root, "examples/demo/main.go", "package main\n\nfunc main() {}\n")
	// Go ignores files named with a leading underscore or dot, as it ignores
	// such directories.
	writeBuildFixture(t, root, "cmd/tool/_scratch.go", "package main\n\nfunc main() {}\n")
	writeBuildFixture(t, root, "cmd/tool/.hidden.go", "package main\n\nfunc main() {}\n")

	detection, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{Kind: SourceGit})
	if err != nil || len(detection.Candidates) != 1 {
		t.Fatalf("detect: %#v, %v", detection, err)
	}
	candidate := detection.Candidates[0]
	if detection.Truncated || candidate.GoLibrary || candidate.GoPackage != "." ||
		!slices.Equal(candidate.GoMainPackages, []string{".", "examples/demo"}) {
		t.Fatalf("truncated=%v candidate = %#v", detection.Truncated, candidate)
	}
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "go", GoPackage: candidate.GoPackage}
	if findings := plannedRecipeFindings(&candidate, build); findingByCode(findings, "go_main_missing") != nil ||
		findingByCode(findings, "go_main_ambiguous") != nil {
		t.Fatalf("a service was refused as a library: %#v", findings)
	}
	if err := dryRunBuild(t.Context(), root, build, nil); err != nil {
		t.Fatalf("the recipe refuses what detection proposed: %v", err)
	}
}

// A module of many commands is still a detection a draft can save: the list
// is bounded, the text naming the choice is one bounded line, and a package
// chosen from past the list's bound is not called missing.
func TestGoDetectionOfManyMainPackagesStaysWithinItsBounds(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "go.mod", "module example.test/tools\n\ngo 1.26\n")
	for index := range 70 {
		writeBuildFixture(t, root, fmt.Sprintf("cmd/tool-number-%02d/main.go", index), "package main\n\nfunc main() {}\n")
	}
	detection, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)})
	if err != nil || len(detection.Candidates) != 1 {
		t.Fatalf("detect: %#v, %v", detection, err)
	}
	candidate := detection.Candidates[0]
	if len(candidate.GoMainPackages) != goMainPackagesKept || candidate.GoMainPackagesOmitted != 70-goMainPackagesKept ||
		candidate.GoPackage != "" {
		t.Fatalf("main packages = %d listed, %d omitted, chose %q", len(candidate.GoMainPackages), candidate.GoMainPackagesOmitted, candidate.GoPackage)
	}
	decision := strings.Join(candidate.NeedsDecision, " ")
	if !strings.Contains(decision, "./cmd/tool-number-00, ./cmd/tool-number-01") || !strings.Contains(decision, " more") {
		t.Fatalf("decision = %q", decision)
	}
	source := DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/o/r.git", Ref: "main"}
	detection.Source.Remote, detection.Source.Repository, err = remoteForSource(source)
	if err != nil {
		t.Fatal(err)
	}
	detection.Source.Ref = canonicalSourceConfig(source).Ref
	if err := validateDetectionResult(&source, detection); err != nil {
		t.Fatalf("a many-command module cannot be saved: %v", err)
	}
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "go"}
	if item := findingByCode(plannedRecipeFindings(&candidate, build), "go_main_ambiguous"); item == nil ||
		!strings.HasSuffix(item.Measured, "more") || len(item.Measured) > 512 {
		t.Fatalf("ambiguity = %#v", item)
	}
	build.GoPackage = "cmd/tool-number-69"
	if findings := plannedRecipeFindings(&candidate, build); len(findings) != 0 {
		t.Fatalf("a main package past the list's bound was refused: %#v", findings)
	}
	if err := dryRunBuild(t.Context(), root, build, nil); err != nil {
		t.Fatalf("the recipe refuses the chosen package: %v", err)
	}
}

func TestGoMainPackageListIsOneBoundedLine(t *testing.T) {
	if got := goMainPackageList([]string{".", "cmd/api"}, 0); got != "., ./cmd/api" {
		t.Fatalf("short list = %q", got)
	}
	if got := goMainPackageList([]string{"cmd/api"}, 3); got != "./cmd/api and 3 more" {
		t.Fatalf("list with omitted packages = %q", got)
	}
	long := strings.Repeat("deep/", 100) + "cmd"
	if got := goMainPackageList([]string{long, "cmd/api"}, 0); len(got) > 400 || !strings.HasSuffix(got, "... and 1 more") {
		t.Fatalf("an overlong first package = %q", got)
	}
}
