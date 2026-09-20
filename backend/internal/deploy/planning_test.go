package deploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	basestore "github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

const testImageDigest = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

func TestDetectorIsDeterministicBoundedAndDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	writePlanningFixture(t, filepath.Join(root, "apps", "web", "package.json"), `{
  "name":"web","scripts":{"build":"vite build"},"devDependencies":{"vite":"6.0.0"}
}`)
	writePlanningFixture(t, filepath.Join(root, "services", "api", "go.mod"), "module example.test/api\n")
	writePlanningFixture(t, filepath.Join(root, ".gitmodules"), "[submodule \"shared\"]\n  path = shared\n")
	writePlanningFixture(t, filepath.Join(root, ".gitattributes"), "assets/** filter=lfs diff=lfs merge=lfs -text\n")
	outside := t.TempDir()
	writePlanningFixture(t, filepath.Join(outside, "Dockerfile"), "FROM scratch\n")
	if err := os.Symlink(outside, filepath.Join(root, "linked-outside")); err != nil {
		t.Fatal(err)
	}

	detector := Detector{}
	identity := SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)}
	first, err := detector.DetectPath(context.Background(), root, identity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := detector.DetectPath(context.Background(), root, identity)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("detection is not deterministic:\n%s\n%s", firstJSON, secondJSON)
	}
	if len(first.Candidates) != 2 || first.SelectedID != "" {
		t.Fatalf("monorepo detection = %#v, want two ambiguous high-confidence candidates", first)
	}
	for _, candidate := range first.Candidates {
		decisions := strings.Join(candidate.NeedsDecision, " ")
		if !strings.Contains(decisions, "submodules") || !strings.Contains(decisions, "Git LFS") {
			t.Fatalf("candidate lacks bounded Git dependency evidence: %#v", candidate)
		}
		for _, evidence := range candidate.Evidence {
			if strings.Contains(evidence.Path, "linked-outside") {
				t.Fatalf("detector followed an out-of-root symlink: %#v", evidence)
			}
		}
	}

	limited, err := (Detector{Limits: DetectionLimits{MaxFiles: 1}}).DetectPath(
		context.Background(), root, identity,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !limited.Truncated || limited.TruncatedReason != "file limit reached" {
		t.Fatalf("file-bounded detection = %#v", limited)
	}

	largeRoot := t.TempDir()
	writePlanningFixture(t, filepath.Join(largeRoot, "package.json"), strings.Repeat("x", 64))
	byteLimited, err := (Detector{Limits: DetectionLimits{MaxReadBytes: 8, MaxFileBytes: 8}}).DetectPath(
		context.Background(), largeRoot, identity,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !byteLimited.Truncated || byteLimited.TruncatedReason != "read-byte limit reached" {
		t.Fatalf("byte-bounded detection = %#v", byteLimited)
	}
}

func TestDetectionEvidenceWithCredentialShapeIsRedacted(t *testing.T) {
	root := t.TempDir()
	writePlanningFixture(t, filepath.Join(root, "package.json"), `{
  "name":"unsafe-script","scripts":{"build":"API_TOKEN=plain-secret vite build"},
  "devDependencies":{"vite":"6.0.0"}
}`)
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{Kind: SourceGit, Revision: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "plain-secret") || !strings.Contains(string(encoded), "details withheld") {
		t.Fatalf("credential-shaped script was not redacted: %s", encoded)
	}
}

func TestDetectionResultValidationRejectsTamperedEvidence(t *testing.T) {
	source := canonicalSourceConfig(DraftSourceConfig{
		Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/owner/repo.git", Ref: "main",
	})
	candidate := newDetectedCandidate("", BuildDockerfile, DetectedCandidate{
		Name: "app", Profile: ProfileWeb, Confidence: ConfidenceHigh,
		Evidence: []DetectionEvidence{{Path: "Dockerfile", Reason: "container build definition"}}, NeedsDecision: []string{},
	})
	base := DetectionResult{
		Source: SourceIdentity{
			Kind: SourceGit, Remote: source.URL, Repository: "owner/repo", Ref: "main",
			Revision: strings.Repeat("a", 40),
		},
		Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID,
	}
	tests := []struct {
		name   string
		mutate func(*DetectionResult)
	}{
		{"remote mismatch", func(result *DetectionResult) { result.Source.Remote = "https://example.test/other/repo.git" }},
		{"short revision", func(result *DetectionResult) { result.Source.Revision = "abc" }},
		{"invalid confidence", func(result *DetectionResult) { result.Candidates[0].Confidence = "certain" }},
		{"candidate traversal", func(result *DetectionResult) { result.Candidates[0].Root = "../../etc" }},
		{"credential command", func(result *DetectionResult) { result.Candidates[0].StartCommand = "API_TOKEN=plain start" }},
		{"foreign Compose evidence", func(result *DetectionResult) { result.Compose = &ComposeAnalysis{} }},
		{"inconsistent truncation", func(result *DetectionResult) { result.TruncatedReason = "file limit reached" }},
		{"secret observed evidence", func(result *DetectionResult) { result.Source.Observed = json.RawMessage(`{"token":"plain-secret"}`) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, _ := json.Marshal(base)
			var result DetectionResult
			if err := json.Unmarshal(encoded, &result); err != nil {
				t.Fatal(err)
			}
			test.mutate(&result)
			if err := validateDetectionResult(&source, result); !errors.Is(err, ErrInvalidPlan) {
				t.Fatalf("tampered detection error = %v", err)
			}
		})
	}
}

func TestSourceAndPlanValidationRejectsTraversalAndPlaintextCredentials(t *testing.T) {
	tests := []struct {
		name   string
		source DraftSourceConfig
	}{
		{"http git", DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "http://example.test/owner/repo.git"}},
		{"git password", DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://user:pass@example.test/owner/repo.git"}},
		{"git username", DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://token@example.test/owner/repo.git"}},
		{"ref traversal", DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/owner/repo.git", Ref: "refs/heads/../secret"}},
		{"ref option", DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/owner/repo.git", Ref: "-upload-pack=evil"}},
		{"ref reflog", DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/owner/repo.git", Ref: "main@{1}"}},
		{"ref double slash", DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/owner/repo.git", Ref: "feature//escape"}},
		{"ref arbitrary namespace", DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/owner/repo.git", Ref: "refs/pull/1/head"}},
		{"git query", DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/owner/repo.git?token=secret"}},
		{"git encoded traversal", DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/owner/%2e%2e/repo.git"}},
		{"subdirectory traversal", DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/owner/repo.git", Subdirectory: "../secret"}},
		{"local relative", DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalCheckout, LocalPath: "relative"}},
		{"local remote ref", DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalCheckout, LocalPath: "/srv/app", Ref: "main"}},
		{"image malformed", DraftSourceConfig{Kind: SourceImage, Mode: SourceModeImageReference, Image: "UPPER/invalid image"}},
		{"image Git field", DraftSourceConfig{Kind: SourceImage, Mode: SourceModeImageReference, Image: "alpine:3", URL: "https://example.test/owner/repo.git"}},
		{"image bad platform", DraftSourceConfig{Kind: SourceImage, Mode: SourceModeImageReference, Image: "alpine:3", Platform: "linux/amd64;evil"}},
		{"compose path", DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "../compose.yml", Content: "services: {}"}}}},
		{"compose noncanonical path", DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "ops/../compose.yml", Content: "services: {}"}}}},
		{"compose inline Git content", DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposeGit, URL: "https://example.test/owner/repo.git", ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services: {}"}}}},
		{"compose upload credential", DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposeUpload, CredentialID: 4, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services: {}"}}}},
		{"compose build traversal", DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services:\n  app:\n    build: ../../etc\n"}}}},
		{"compose dynamic build traversal", DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services:\n  app:\n    build: ../${BUILD_DIR}\n"}}}},
		{"compose Dockerfile traversal", DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services:\n  app:\n    build:\n      context: .\n      dockerfile: ../../Dockerfile\n"}}}},
		{"compose mount traversal", DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services:\n  app:\n    image: alpine\n    volumes: [\"../../etc:/data\"]\n"}}}},
		{"compose dynamic mount", DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services:\n  app:\n    image: alpine\n    volumes: [\"${HOST_DATA}:/data\"]\n"}}}},
		{"compose command credential", DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services:\n  app:\n    image: alpine\n    command: [\"server\", \"--password\", \"plain-secret\"]\n"}}}},
		{"compose env file traversal", DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services:\n  app:\n    image: alpine\n    env_file: ../../secrets.env\n"}}}},
		{"compose secret file traversal", DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services:\n  app:\n    image: alpine\nsecrets:\n  key:\n    file: ../../secret\n"}}}},
		{"compose embedded URL credential fallback", DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services:\n  app:\n    image: alpine\n    environment:\n      DATABASE_URL: ${DATABASE_URL:-postgres://user:password@db/app}\n"}}}},
		{"compose password", DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services:\n  app:\n    image: alpine\n    environment:\n      PASSWORD: literal\n"}}}},
		{"compose fallback password", DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services:\n  app:\n    image: alpine\n    environment:\n      PASSWORD: ${PASSWORD:-literal}\n"}}}},
		{"compose inherited password", DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services:\n  app:\n    image: alpine\n    environment:\n      - PASSWORD\n"}}}},
		{"compose blank password", DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services:\n  app:\n    image: alpine\n    environment:\n      PASSWORD:\n"}}}},
		{"non-Gitea base URL", DraftSourceConfig{Kind: SourceGit, Mode: SourceModeConnectedRepository, Provider: "github", ProviderBaseURL: "https://git.example.test", Repository: "owner/repo"}},
		{"import extra path", DraftSourceConfig{Kind: SourceImport, Mode: SourceModeExistingContainer, ResourceID: "container", LocalPath: "/srv/app"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.source.Validate(); err == nil {
				t.Fatalf("Validate(%#v) succeeded", test.source)
			}
		})
	}

	validCompose := DraftSourceConfig{
		Kind: SourceCompose, Mode: SourceModeComposePaste,
		ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services:\n  app:\n    image: alpine\n    environment:\n      PASSWORD: ${PASSWORD}\n"}},
	}
	if err := validCompose.Validate(); err != nil {
		t.Fatalf("typed Compose variable was rejected: %v", err)
	}

	base := PlanConfiguration{
		Build:   BuildPlanConfig{Method: BuildNone},
		Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst},
		Variables: []PlannedVariable{{
			Name: "API_TOKEN", Sensitivity: "secret", Scopes: []string{"runtime"}, Reference: "${{credential.api-token}}",
		}},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("typed plan reference was rejected: %v", err)
	}
	invalid := base
	invalid.Build.BuildCommand = "API_TOKEN=plain-secret make build"
	if err := invalid.Validate(); err == nil {
		t.Fatal("plaintext credential assignment in build command was accepted")
	}
	invalid = base
	invalid.Checks = []PlannedCheck{{
		Name: "ready", Kind: "http", Phase: "readiness", Required: true,
		Config: json.RawMessage(`{"password":"plain-secret"}`),
	}}
	if err := invalid.Validate(); err == nil {
		t.Fatal("plaintext credential in check config was accepted")
	}
	invalid = base
	invalid.Variables = append([]PlannedVariable(nil), base.Variables...)
	invalid.Variables[0].Reference = "${API_TOKEN}"
	if err := invalid.Validate(); err == nil {
		t.Fatal("untyped variable reference was accepted")
	}
	invalid = base
	invalid.Runtime.Command = []string{"server", "--password", "plain-secret"}
	if err := invalid.Validate(); err == nil {
		t.Fatal("plaintext credential in runtime argv was accepted")
	}
	validArgumentReference := base
	validArgumentReference.Runtime.Command = []string{"server", "--password", "${{credential.server-password}}"}
	if err := validArgumentReference.Validate(); err != nil {
		t.Fatalf("typed credential reference in runtime argv was rejected: %v", err)
	}
	invalid = base
	invalid.Runtime.Mounts = []RuntimeMount{{Source: "../../etc", Target: "/data", Ownership: OwnershipLinked}}
	if err := invalid.Validate(); err == nil {
		t.Fatal("relative runtime bind traversal was accepted")
	}
	invalid = base
	invalid.Runtime.Capabilities = []string{"sys_admin;evil"}
	if err := invalid.Validate(); err == nil {
		t.Fatal("malformed runtime capability was accepted")
	}
}

// A UI control needs to know which field a refusal is about, not just that
// the plan as a whole was rejected. The most common refusals wrap a
// *ValidationError behind ErrInvalidPlan, so errors.Is against the sentinel
// still works exactly as before and errors.As reaches the field pointer.
func TestPlanConfigurationValidateAttachesAFieldPointerToCommonRefusals(t *testing.T) {
	base := PlanConfiguration{
		Build:   BuildPlanConfig{Method: BuildNone},
		Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst},
	}
	fieldOf := func(t *testing.T, err error) string {
		t.Helper()
		if err == nil {
			t.Fatal("Validate() succeeded, want a refusal")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("Validate() error = %v (%T), want a *ValidationError", err, err)
		}
		return validation.Field
	}

	ports := base
	ports.Runtime.InternalPort = 70000
	if field := fieldOf(t, ports.Validate()); field != "runtime.internalPort" {
		t.Fatalf("invalid internal port field = %q", field)
	}
	if !strings.Contains(ports.Validate().Error(), "0 (unset) or between 1 and 65535") {
		t.Fatalf("invalid port message = %q, want it to say 0 is allowed", ports.Validate().Error())
	}
	hostPort := base
	hostPort.Runtime.HostPort = -1
	if field := fieldOf(t, hostPort.Validate()); field != "runtime.hostPort" {
		t.Fatalf("invalid host port field = %q", field)
	}
	for name, mutate := range map[string]func(*RuntimePlanConfig){
		"repeated host port": func(r *RuntimePlanConfig) {
			r.Ports = []PublishedPort{{HostPort: 2222, ContainerPort: 2222}, {HostPort: 2222, ContainerPort: 22}}
		},
		"fixed port repeated": func(r *RuntimePlanConfig) {
			r.HostPort, r.Ports = 2222, []PublishedPort{{HostPort: 2222, ContainerPort: 22}}
		},
		"container port missing": func(r *RuntimePlanConfig) { r.Ports = []PublishedPort{{HostPort: 2222}} },
		"unknown protocol": func(r *RuntimePlanConfig) {
			r.Ports = []PublishedPort{{HostPort: 2222, ContainerPort: 22, Protocol: "sctp"}}
		},
		"foreign bind address": func(r *RuntimePlanConfig) {
			r.Ports = []PublishedPort{{HostPort: 2222, ContainerPort: 22, BindAddress: "10.0.0.1"}}
		},
		"host network": func(r *RuntimePlanConfig) {
			r.HostNetwork, r.Ports = true, []PublishedPort{{HostPort: 2222, ContainerPort: 22}}
		},
	} {
		published := base
		mutate(&published.Runtime)
		if field := fieldOf(t, published.Validate()); !strings.HasPrefix(field, "runtime.ports[") {
			t.Fatalf("%s: field = %q", name, field)
		}
	}
	// The same number on TCP and UDP is two different publications.
	published := base
	published.Runtime.Ports = []PublishedPort{{HostPort: 19132, ContainerPort: 19132, Protocol: "udp"}, {HostPort: 19132, ContainerPort: 19132}}
	if err := published.Validate(); err != nil {
		t.Fatalf("tcp and udp on one number refused: %v", err)
	}

	mounts := base
	mounts.Runtime.Mounts = []RuntimeMount{
		{Source: "config-data", Target: "/data", Ownership: OwnershipManaged},
		{Source: "", Target: "/broken", Ownership: OwnershipManaged},
	}
	if field := fieldOf(t, mounts.Validate()); field != "runtime.mounts[1]" {
		t.Fatalf("invalid mount field = %q, want the second mount's index", field)
	}

	deps := base
	deps.Dependencies = []PlannedDependency{
		{Kind: "backup", ResourceKind: "backup_job", ResourceID: "1", Ownership: OwnershipManaged},
		{Kind: "backup", ResourceKind: "backup_job", ResourceID: "not-a-number", Ownership: OwnershipManaged},
	}
	if field := fieldOf(t, deps.Validate()); field != "dependencies[1]" {
		t.Fatalf("invalid dependency field = %q, want the second dependency's index", field)
	}

	domains := base
	domains.Domains = []PlannedDomain{
		{Hostname: "app.example.test", Ownership: OwnershipManaged},
		{Hostname: "not a hostname", Ownership: OwnershipManaged},
	}
	if field := fieldOf(t, domains.Validate()); field != "domains[1]" {
		t.Fatalf("invalid domain field = %q, want the second domain's index", field)
	}

	variables := base
	variables.Variables = []PlannedVariable{{Name: "1-bad-name", Sensitivity: "plain", Scopes: []string{"runtime"}}}
	if field := fieldOf(t, variables.Validate()); field != "1-bad-name" {
		t.Fatalf("invalid variable field = %q, want the variable's own name", field)
	}

	checks := base
	checks.Checks = []PlannedCheck{
		{Name: "ready", Kind: "http", Phase: "readiness", Required: true},
		{Name: "bad-kind", Kind: "carrier-pigeon", Phase: "readiness", Required: true},
	}
	if field := fieldOf(t, checks.Validate()); field != "checks[1]" {
		t.Fatalf("invalid check field = %q, want the second check's index", field)
	}

	tasks := base
	tasks.Build.ReleaseTasks = []ReleaseTaskConfig{{Name: "bad name!", Command: "true", TimeoutSeconds: 30}}
	if field := fieldOf(t, tasks.Validate()); field != "build.releaseTasks[0]" {
		t.Fatalf("invalid release task field = %q, want its index", field)
	}

	// A refusal outside the named categories still has no field — this is not
	// meant to become exhaustive, only the most common controls.
	strategy := base
	strategy.Runtime.Strategy = "not-a-strategy"
	var validation *ValidationError
	if errors.As(strategy.Validate(), &validation) {
		t.Fatalf("an unnamed-category refusal unexpectedly carried a field: %q", validation.Field)
	}
}

func TestComposeAnalysisAndAdapterPreserveMultiFileOrder(t *testing.T) {
	documents := []ComposeDocument{
		{Path: "compose.override.yml", Order: 2, Content: "services:\n  web:\n    image: nginx:1.27\n    ports: [\"127.0.0.1:8080:80\"]\n"},
		{Path: "compose.yml", Order: 1, Content: "services:\n  db:\n    image: postgres:17\n    environment:\n      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}\n  web:\n    image: nginx:1.26\n"},
	}
	analysis, err := analyzeComposeDocuments(documents)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(analysis.Files, []string{"compose.yml", "compose.override.yml"}) {
		t.Fatalf("Compose order = %#v", analysis.Files)
	}
	if len(analysis.Services) != 2 || analysis.Services[0].Name != "db" || analysis.Services[1].Name != "web" {
		t.Fatalf("Compose services = %#v", analysis.Services)
	}
	if !reflect.DeepEqual(analysis.Variables, []string{"POSTGRES_PASSWORD"}) {
		t.Fatalf("Compose variables = %#v", analysis.Variables)
	}
	reversed := []ComposeDocument{documents[1], documents[0]}
	again, err := analyzeComposeDocuments(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Digest != again.Digest || analysis.Preview != again.Preview {
		t.Fatal("Compose analysis changed when request array order changed")
	}

	fake := &planningDockerFake{composeAvailable: true, composeValid: true}
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), fake, nil)
	result, err := analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceCompose, Mode: SourceModeComposeUpload, ComposeFiles: documents,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Source.Digest == "" || len(result.Source.Services) != 2 || fake.validateFilesCalls != 1 {
		t.Fatalf("multi-file adapter result = %#v, validation calls=%d", result, fake.validateFilesCalls)
	}
	gotPaths := []string{}
	for _, input := range fake.validatedInputs {
		gotPaths = append(gotPaths, input.Path)
	}
	if !reflect.DeepEqual(gotPaths, []string{"compose.yml", "compose.override.yml"}) {
		t.Fatalf("Docker Compose validation order = %#v", gotPaths)
	}
	if fake.validatedProject != "" || !reflect.DeepEqual(fake.validatedVariables, []string{"POSTGRES_PASSWORD"}) {
		t.Fatalf("planning validation scope = project %q variables %#v", fake.validatedProject, fake.validatedVariables)
	}

	fake.composeAvailable = false
	result, err = analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: documents,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Unavailable == "" || len(result.Candidates) != 1 {
		t.Fatalf("unavailable Compose erased structural evidence: %#v", result)
	}
}

func TestLocalGitImageAndImportSourceAdapters(t *testing.T) {
	checkout := t.TempDir()
	writePlanningFixture(t, filepath.Join(checkout, "apps", "worker", "go.mod"), "module example.test/worker\n")
	runPlanningGitFixture(t, checkout, "init", "-q", "-b", "main")
	runPlanningGitFixture(t, checkout, "config", "user.email", "test@example.test")
	runPlanningGitFixture(t, checkout, "config", "user.name", "Planning Test")
	runPlanningGitFixture(t, checkout, "remote", "add", "origin", "https://example.test/owner/repo.git")
	runPlanningGitFixture(t, checkout, "add", ".")
	runPlanningGitFixture(t, checkout, "commit", "-q", "-m", "fixture")

	fake := &planningDockerFake{
		composeAvailable: true, composeValid: true,
		image: &dockerx.DistributionImage{
			Reference: "docker.io/library/alpine:3", Digest: testImageDigest,
			Platforms: []string{"linux/amd64", "linux/arm64"},
		},
		container: &dockerx.ContainerSpec{
			Name: "existing-app", Image: "nginx:1.27", Command: []string{"server", "--password", "never-command-secret"},
			Env:        []dockerx.EnvVar{{Name: "PASSWORD", Value: "never-serialize-this"}, {Name: "MODE", Value: "production"}},
			Ports:      []dockerx.PortMapping{{HostIP: "127.0.0.1", HostPort: 8080, ContainerPort: 80}},
			Mounts:     []dockerx.MountSpec{{Type: "bind", Source: checkout, Target: "/data"}},
			AutoRemove: true, NetworkMode: "container:other",
		},
		stacks: []dockerx.ComposeStack{{Name: "existing-stack", WorkingDir: checkout, ConfigFiles: []string{"compose.yml"}, Services: []dockerx.ComposeService{{Name: "web"}}, Running: 1, Total: 1}},
	}
	analyzer := NewHostSourceAnalyzer([]string{checkout}, []string{checkout}, t.TempDir(), fake, nil)
	local, err := analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceGit, Mode: SourceModeLocalCheckout, LocalPath: checkout, Subdirectory: "apps/worker",
	})
	if err != nil {
		t.Fatal(err)
	}
	if local.Source.Revision == "" || local.Source.LocalPath != checkout || len(local.Candidates) != 1 || local.Candidates[0].Root != "" {
		t.Fatalf("local Git subdirectory result = %#v", local)
	}
	localSource, err := analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceLocal, Mode: SourceModeLocalCheckout, LocalPath: checkout, Subdirectory: "apps/worker",
	})
	if err != nil || localSource.Source.Kind != SourceLocal || localSource.Source.Revision == "" {
		t.Fatalf("local source result = %#v, %v", localSource, err)
	}
	checkoutImport, err := analyzer.PreviewImport(context.Background(), DraftSourceConfig{
		Kind: SourceImport, Mode: SourceModeExistingCheckout, LocalPath: checkout,
	})
	if err != nil || checkoutImport.Kind != "checkout" || len(checkoutImport.WouldChange) != 0 {
		t.Fatalf("checkout import result = %#v, %v", checkoutImport, err)
	}
	// A blueprint source resolves against the reviewed catalogue. Its required
	// acceptance is part of rendering, so an unaccepted EULA never reaches a plan.
	if _, err := analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceBlueprint, Mode: SourceModeBlueprint, BlueprintID: "minecraft-java", BlueprintVersion: "1.0.0",
	}); err == nil || !strings.Contains(err.Error(), "eula") {
		t.Fatalf("unaccepted blueprint EULA rendered a plan: %v", err)
	}
	blueprintSource, err := analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceBlueprint, Mode: SourceModeBlueprint, BlueprintID: "minecraft-java",
		BlueprintVersion: "1.0.0", BlueprintInputs: map[string]string{"eula": "true"},
	})
	// A game blueprint renders as a preview: its identity names the image the
	// release would pull and the reviewed definition, but no digest is
	// resolved because the game integration cannot deploy it yet.
	if err != nil || blueprintSource.Source.Kind != SourceBlueprint ||
		blueprintSource.Source.Repository != "itzg/minecraft-server:2026.9.1-java21" || blueprintSource.Source.Ref != "minecraft-java@1.0.0" ||
		!strings.HasPrefix(blueprintSource.Source.Revision, "sha256:") || blueprintSource.Source.Digest != "" ||
		len(blueprintSource.Candidates) != 1 || blueprintSource.Candidates[0].Profile != ProfileGame ||
		blueprintSource.Candidates[0].Port != 25565 {
		t.Fatalf("blueprint source result = %#v, %v", blueprintSource, err)
	}
	if _, err := analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceBlueprint, Mode: SourceModeBlueprint, BlueprintID: "minecraft-java", BlueprintVersion: "9.9.9",
		BlueprintInputs: map[string]string{"eula": "true"},
	}); err == nil {
		t.Fatal("served a blueprint version this dashboard does not ship")
	}
	writePlanningFixture(t, filepath.Join(checkout, "safe-compose.yml"), "services:\n  app:\n    image: alpine:3\n")
	localCompose, err := analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceCompose, Mode: SourceModeComposeLocal, LocalPath: checkout,
		ComposeFiles: []ComposeDocument{{Path: "safe-compose.yml"}},
	})
	if err != nil || localCompose.Source.Digest == "" || len(localCompose.Candidates) != 1 {
		t.Fatalf("local Compose result = %#v, %v", localCompose, err)
	}
	outside := t.TempDir()
	writePlanningFixture(t, filepath.Join(outside, "go.mod"), "module outside.test/escape\n")
	if err := os.Symlink(outside, filepath.Join(checkout, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceGit, Mode: SourceModeLocalCheckout, LocalPath: checkout, Subdirectory: "escape",
	}); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("symlinked source subdirectory error = %v", err)
	}
	writePlanningFixture(t, filepath.Join(outside, "compose.yml"), "services:\n  outside:\n    image: alpine\n")
	if err := os.Symlink(filepath.Join(outside, "compose.yml"), filepath.Join(checkout, "compose.yml")); err != nil {
		t.Fatal(err)
	}
	if _, err := analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceCompose, Mode: SourceModeComposeLocal, LocalPath: checkout,
		ComposeFiles: []ComposeDocument{{Path: "compose.yml"}},
	}); !errors.Is(err, ErrInvalidCompose) {
		t.Fatalf("symlinked Compose file error = %v", err)
	}

	image, err := analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceImage, Mode: SourceModeImageReference, Image: "alpine:3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if image.Source.Digest != testImageDigest || image.Source.OS != "linux" || image.Source.Architecture != "amd64" {
		t.Fatalf("image result = %#v", image)
	}

	container, err := analyzer.PreviewImport(context.Background(), DraftSourceConfig{
		Kind: SourceImport, Mode: SourceModeExistingContainer, ResourceID: "container-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	containerJSON, _ := json.Marshal(container)
	if strings.Contains(string(containerJSON), "never-serialize-this") ||
		strings.Contains(string(containerJSON), "never-command-secret") || len(container.WouldChange) != 0 {
		t.Fatalf("container preview leaked values or promised changes: %s", containerJSON)
	}
	if len(container.Configuration.Runtime.Command) != 0 || len(container.Unsupported) == 0 {
		t.Fatalf("credential-shaped imported command was not held for review: %#v", container)
	}
	if !stringSliceContains(container.Unsupported, "automatic removal") ||
		!stringSliceContains(container.Unsupported, "network mode container:other") {
		t.Fatalf("unsupported import fields = %#v", container.Unsupported)
	}
	if len(container.Configuration.Variables) != 2 || container.Configuration.Variables[0].Reference != "" {
		t.Fatalf("container variable projection = %#v", container.Configuration.Variables)
	}

	stack, err := analyzer.PreviewImport(context.Background(), DraftSourceConfig{
		Kind: SourceImport, Mode: SourceModeExistingStack, ResourceID: "existing-stack",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stack.WouldChange) != 0 || stack.Configuration.Build.Method != BuildCompose {
		t.Fatalf("stack preview = %#v", stack)
	}
}

func TestRemoteGitAdapterUsesContainedMirrorWorktreeAndEphemeralCredentialFile(t *testing.T) {
	bin := t.TempDir()
	capture := filepath.Join(t.TempDir(), "argv")
	configCapture := filepath.Join(t.TempDir(), "configs")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$PLANNING_GIT_ARGV_CAPTURE"
printf '%s\n' "${GIT_CONFIG_GLOBAL:-}" >> "$PLANNING_GIT_CONFIG_CAPTURE"
test -z "${GIT_CONFIG_VALUE_0:-}"
test -z "${JD_PLANNING_SECRET:-}"
test -f "${GIT_CONFIG_GLOBAL:-missing}"
test "$(stat -c '%a' "$GIT_CONFIG_GLOBAL")" = '600'
grep -q 'Authorization: Basic eC1hY2Nlc3MtdG9rZW46Zml4dHVyZS1iZWFyZXI=' "$GIT_CONFIG_GLOBAL"
	case "$1" in
	  ls-remote)
	    printf '%s\trefs/heads/main\n' "${PLANNING_GIT_REMOTE_REVISION:-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}"
    ;;
	  init)
	    destination=''
	    for argument in "$@"; do destination="$argument"; done
	    mkdir -p "$destination"
	    ;;
	  config)
	    ;;
	  fetch)
	    if test "${PLANNING_GIT_FAIL_FETCH:-}" = 1; then
	      printf '%s\n' 'remote reflected Authorization: Bearer fixture-bearer' >&2
	      exit 23
	    fi
	    ;;
	  worktree)
	    if test "$2" = add; then
	      after_separator=0
	      destination=''
	      for argument in "$@"; do
	        if test "$after_separator" = 1; then destination="$argument"; break; fi
	        if test "$argument" = --; then after_separator=1; fi
	      done
	      mkdir -p "$destination"
	      printf '%s\n' '{"name":"remote-worker","scripts":{"start":"node index.js"}}' > "$destination/package.json"
	      printf '%s\n' 'services:' '  app:' '    image: alpine:3' > "$destination/compose.yml"
	      printf '%s\n' '[submodule "shared"]' '  path = shared' > "$destination/.gitmodules"
	      printf '%s\n' 'assets/** filter=lfs diff=lfs merge=lfs -text' > "$destination/.gitattributes"
	    elif test "$2" = remove; then
	      after_separator=0
	      destination=''
	      for argument in "$@"; do
	        if test "$after_separator" = 1; then destination="$argument"; break; fi
	        if test "$argument" = --; then after_separator=1; fi
	      done
	      rm -r "$destination"
	    fi
    ;;
	  rev-parse)
	    if test "$2" = HEAD; then
	      printf '%s\n' "${PLANNING_GIT_CLONED_REVISION:-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}"
	    else
	      printf '%s\n' "${PLANNING_GIT_FETCHED_REVISION:-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}"
	    fi
    ;;
  *)
    exit 22
    ;;
esac
`
	gitPath := filepath.Join(bin, "git")
	writePlanningFixture(t, gitPath, script)
	if err := os.Chmod(gitPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PLANNING_GIT_ARGV_CAPTURE", capture)
	t.Setenv("PLANNING_GIT_CONFIG_CAPTURE", configCapture)
	// These inherited values must not cross into the planning subprocess.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "http.extraHeader")
	t.Setenv("GIT_CONFIG_VALUE_0", "Authorization: Bearer inherited-secret")
	t.Setenv("JD_PLANNING_SECRET", "dashboard-secret")

	credentials := credentialReaderFake{material: CredentialMaterial{Kind: "provider_token", Secret: "fixture-bearer"}}
	cache := t.TempDir()
	analyzer := NewHostSourceAnalyzer(nil, nil, cache, nil, credentials)
	result, err := analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://git.example.test/team/repo.git",
		Ref: "main", CredentialID: 9,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Source.Revision != strings.Repeat("a", 40) || len(result.Candidates) < 1 {
		t.Fatalf("remote Git result = %#v", result)
	}
	connected, err := analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceGit, Mode: SourceModeConnectedRepository, Provider: "github", Repository: "team/repo",
		Ref: "main", CredentialID: 9,
	})
	if err != nil || connected.Source.Remote != "https://github.com/team/repo.git" || len(connected.Candidates) < 1 {
		t.Fatalf("connected provider result = %#v, %v", connected, err)
	}
	compose, err := NewHostSourceAnalyzer(nil, nil, cache, &planningDockerFake{
		composeAvailable: true, composeValid: true,
	}, credentials).Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceCompose, Mode: SourceModeComposeGit, URL: "https://git.example.test/team/repo.git",
		Ref: "main", CredentialID: 9, IncludeSubmodules: true, IncludeLFS: true,
		ComposeFiles: []ComposeDocument{{Path: "compose.yml"}},
	})
	if err != nil || compose.Source.Revision != strings.Repeat("a", 40) ||
		!compose.Source.IncludeSubmodules || !compose.Source.IncludeLFS ||
		!compose.GitRequirements.Submodules || !compose.GitRequirements.LFS {
		t.Fatalf("Compose Git result = %#v, %v", compose, err)
	}
	argv, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"fixture-bearer", "inherited-secret", "http.extraHeader"} {
		if strings.Contains(string(argv), forbidden) {
			t.Fatalf("Git argv leaked %q: %s", forbidden, argv)
		}
	}
	for _, required := range []string{
		"ls-remote --exit-code --refs", "refs/heads/main", "init --bare",
		"fetch --force --depth=1 --filter=blob:limit=1048576 --no-tags",
		"worktree add --detach --force", "rev-parse HEAD", "worktree remove --force", "worktree prune",
	} {
		if !strings.Contains(string(argv), required) {
			t.Errorf("Git argv lacks %q: %s", required, argv)
		}
	}
	configPaths, err := os.ReadFile(configCapture)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range strings.Fields(string(configPaths)) {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("ephemeral Git credential file still exists at %q: %v", path, err)
		}
	}
	entries, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name() != "git-mirrors" || entries[1].Name() != "git-worktrees" {
		t.Fatalf("Git analysis cache layout = %#v", entries)
	}
	worktrees, err := os.ReadDir(filepath.Join(cache, "git-worktrees"))
	if err != nil || len(worktrees) != 0 {
		t.Fatalf("Git analysis left a worktree behind: %#v, %v", worktrees, err)
	}
	mirrors, err := os.ReadDir(filepath.Join(cache, "git-mirrors"))
	if err != nil || len(mirrors) != 2 {
		t.Fatalf("Git analysis did not retain one mirror per remote: %#v, %v", mirrors, err)
	}
	if info, err := mirrors[0].Info(); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("managed mirror mode = %v, %v", info, err)
	}
	t.Setenv("PLANNING_GIT_CLONED_REVISION", strings.Repeat("b", 40))
	if _, err := analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://git.example.test/team/repo.git",
		Ref: "main", CredentialID: 9,
	}); !errors.Is(err, ErrGitUnavailable) || !strings.Contains(err.Error(), "moved during inspection") {
		t.Fatalf("moved Git ref error = %v", err)
	}
	worktrees, err = os.ReadDir(filepath.Join(cache, "git-worktrees"))
	if err != nil || len(worktrees) != 0 {
		t.Fatalf("moved-ref failure left a worktree behind: %#v, %v", worktrees, err)
	}

	t.Setenv("PLANNING_GIT_CLONED_REVISION", strings.Repeat("a", 40))
	t.Setenv("PLANNING_GIT_FAIL_FETCH", "1")
	_, err = analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://git.example.test/team/repo.git",
		Ref: "main", CredentialID: 9,
	})
	if !errors.Is(err, ErrGitUnavailable) || strings.Contains(err.Error(), "fixture-bearer") {
		t.Fatalf("private-auth failure was not typed and redacted: %v", err)
	}
}

func TestPlanningGitMirrorCreatesDetachedWorktreeAtExactRevision(t *testing.T) {
	upstream := t.TempDir()
	writePlanningFixture(t, filepath.Join(upstream, "go.mod"), "module example.test/mirror\n")
	runPlanningGitFixture(t, upstream, "init", "-q", "-b", "main")
	runPlanningGitFixture(t, upstream, "config", "user.email", "test@example.test")
	runPlanningGitFixture(t, upstream, "config", "user.name", "Planning Test")
	runPlanningGitFixture(t, upstream, "add", ".")
	runPlanningGitFixture(t, upstream, "commit", "-q", "-m", "fixture")
	revision := strings.TrimSpace(planningGitOutput(t, upstream, "rev-parse", "HEAD"))

	cache := t.TempDir()
	mirror := filepath.Join(cache, "git-mirrors", "fixture.git")
	if err := makePrivateDirectory(filepath.Dir(mirror)); err != nil {
		t.Fatal(err)
	}
	environment := append(cleanPlanningGitEnvironment(os.Environ()),
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_LFS_SKIP_SMUDGE=1")
	if err := ensurePlanningMirror(context.Background(), mirror, upstream, environment); err != nil {
		t.Fatal(err)
	}
	localRef := "refs/just-dashboard/planning/fixture"
	if _, err := runPlanningGit(context.Background(), mirror, environment,
		"fetch", "--force", "--depth=1", "--filter=blob:limit=1048576", "--no-tags",
		"origin", "+refs/heads/main:"+localRef); err != nil {
		t.Fatal(err)
	}
	resolved, err := runPlanningGit(context.Background(), mirror, environment, "rev-parse", localRef)
	if err != nil || strings.TrimSpace(resolved) != revision {
		t.Fatalf("mirror revision = %q, %v; want %q", strings.TrimSpace(resolved), err, revision)
	}
	worktree := filepath.Join(cache, "git-worktrees", "fixture")
	if err := makePrivateDirectory(filepath.Dir(worktree)); err != nil {
		t.Fatal(err)
	}
	if _, err := runPlanningGit(context.Background(), mirror, environment,
		"worktree", "add", "--detach", "--force", "--", worktree, revision); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = runPlanningGit(context.Background(), mirror, environment, "worktree", "remove", "--force", "--", worktree)
	})
	if _, err := os.Stat(filepath.Join(worktree, "go.mod")); err != nil {
		t.Fatalf("detached worktree has no source: %v", err)
	}
	worktreeRevision, err := runPlanningGit(context.Background(), worktree, environment, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(worktreeRevision) != revision {
		t.Fatalf("worktree revision = %q, %v; want %q", strings.TrimSpace(worktreeRevision), err, revision)
	}
}

func TestPreflightIsPureStableAndSecretFree(t *testing.T) {
	draft := completePlanningDraftModel()
	draft.Data.Detection.Source.Observed = json.RawMessage(`{"runtimeState":"never-preview-this"}`)
	before, _ := json.Marshal(draft)
	observer := &preflightObserverFake{observation: HostObservation{
		Facilities: map[string]FacilityObservation{"docker": {Available: true}},
		Paths:      []PathObservation{}, Ports: []PortObservation{},
		AvailableMemory: 2 << 30, AvailableDisk: 10 << 30,
	}}
	first, err := PreflightDraft(context.Background(), draft, observer, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PreflightDraft(context.Background(), draft, observer, true)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(draft)
	if string(before) != string(after) {
		t.Fatalf("preflight mutated its draft input:\n%s\n%s", before, after)
	}
	if observer.calls != 2 {
		t.Fatalf("observer calls = %d, want one per preflight", observer.calls)
	}
	if first.Preview != second.Preview || first.Digest != second.Digest {
		t.Fatalf("preflight changed with identical evidence:\n%s\n%s", first.Preview, second.Preview)
	}
	if len(first.Plan.Actions) != len(DefaultStepKeys) || len(first.Plan.Actions) != 16 {
		t.Fatalf("exact actions = %d", len(first.Plan.Actions))
	}
	if strings.Contains(first.Preview, "never-preview-this") || strings.Contains(first.Preview, "Observed") {
		t.Fatalf("exact plan leaked observed material: %s", first.Preview)
	}
	for _, finding := range first.Findings {
		if finding.Code == "" || finding.Title == "" || finding.Means == "" || finding.Owner == "" {
			t.Fatalf("finding lacks stable remediation metadata: %#v", finding)
		}
	}
}

func TestHostPreflightObserverDoesNotCreateOrChangeRequestedPaths(t *testing.T) {
	root := t.TempDir()
	sourceFile := filepath.Join(root, "checkout", "source.txt")
	writePlanningFixture(t, sourceFile, "unchanged")
	missing := filepath.Join(root, "runtime", "data")
	docker := &planningDockerFake{composeAvailable: true, buildxAvailable: true}
	proxy := &planningProxyFake{}
	observer := NewHostPreflightObserver([]string{root}, root, docker, proxy)
	before, err := os.ReadFile(sourceFile)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := observer.Observe(context.Background(), ObservationRequest{
		NeedsGit: true, NeedsDocker: true, NeedsBuildx: true, NeedsCompose: true,
		Paths: []string{filepath.Dir(sourceFile), missing},
		Ports: []PortObservation{{Address: "127.0.0.1", Port: 65534, Protocol: "tcp"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(sourceFile)
	if err != nil || string(before) != string(after) {
		t.Fatalf("preflight changed source checkout: %q, %v", after, err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("preflight created requested runtime path: %v", err)
	}
	if proxy.availabilityCalls != 0 || proxy.listCalls != 0 {
		t.Fatalf("preflight queried an unrequested proxy: %#v", proxy)
	}
	if len(observation.Paths) != 2 || observation.Facilities["docker"].Available != true ||
		observation.Facilities["buildx"].Available != true || observation.Facilities["compose"].Available != true {
		t.Fatalf("read-only host observation = %#v", observation)
	}
}

func TestPreflightBlocksBuildAdapterWhenBuildxIsUnavailable(t *testing.T) {
	draft := completePlanningDraftModel()
	draft.Data.Detection.Candidates[0].BuildMethod = BuildDockerfile
	draft.Data.Configuration.Build = BuildPlanConfig{Method: BuildDockerfile, Dockerfile: "Dockerfile"}
	result, err := PreflightDraft(context.Background(), draft, &preflightObserverFake{observation: HostObservation{
		Facilities: map[string]FacilityObservation{
			"docker": {Available: true}, "buildx": {Available: false, Detail: "plugin not installed"},
		},
		Paths: []PathObservation{}, Ports: []PortObservation{}, OS: "linux", Architecture: "amd64",
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if findingSeverity(result.Findings, "buildx_unavailable") != PreflightBlocked {
		t.Fatalf("Buildx finding = %#v", result.Findings)
	}
}

func TestPreflightCoversGitChoicesImageArchitectureAndDomains(t *testing.T) {
	gitDraft := completePlanningDraftModel()
	gitDraft.Data.Source = &DraftSourceConfig{
		Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/owner/repo.git", Ref: "main",
	}
	gitDraft.Data.Detection.Source = SourceIdentity{
		Kind: SourceGit, Remote: "https://example.test/owner/repo.git", Repository: "owner/repo", Ref: "main",
		Revision: strings.Repeat("a", 40),
	}
	gitDraft.Data.Detection.GitRequirements = GitRequirements{Submodules: true, LFS: true}
	gitDraft.Data.Configuration.Build.Method = BuildNone
	gitDraft.Data.Configuration.Runtime.Image = ""
	gitObservation := HostObservation{
		Facilities: map[string]FacilityObservation{"git": {Available: true}},
		Paths:      []PathObservation{}, Ports: []PortObservation{}, Domains: []DomainObservation{},
	}
	gitResult, err := PreflightDraft(context.Background(), gitDraft, &preflightObserverFake{observation: gitObservation}, true)
	if err != nil {
		t.Fatal(err)
	}
	if findingSeverity(gitResult.Findings, "git_submodules") != PreflightDecision ||
		findingSeverity(gitResult.Findings, "git_lfs") != PreflightDecision {
		t.Fatalf("unresolved Git choices = %#v", gitResult.Findings)
	}
	gitDraft.Data.Source.IncludeSubmodules, gitDraft.Data.Source.IncludeLFS = true, true
	gitDraft.Data.Detection.Source.IncludeSubmodules, gitDraft.Data.Detection.Source.IncludeLFS = true, true
	gitResult, err = PreflightDraft(context.Background(), gitDraft, &preflightObserverFake{observation: gitObservation}, true)
	if err != nil {
		t.Fatal(err)
	}
	if findingSeverity(gitResult.Findings, "git_submodules") != PreflightPass ||
		findingSeverity(gitResult.Findings, "git_lfs") != PreflightPass {
		t.Fatalf("resolved Git choices = %#v", gitResult.Findings)
	}

	imageDraft := completePlanningDraftModel()
	imageDraft.Data.Detection.Source.Platforms = []string{"linux/arm64"}
	imageDraft.Data.Detection.Source.OS = "linux"
	imageDraft.Data.Detection.Source.Architecture = "arm64"
	imageResult, err := PreflightDraft(context.Background(), imageDraft, &preflightObserverFake{observation: HostObservation{
		Facilities: map[string]FacilityObservation{"docker": {Available: true}},
		Paths:      []PathObservation{}, Ports: []PortObservation{}, Domains: []DomainObservation{},
		OS: "linux", Architecture: "amd64",
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if findingSeverity(imageResult.Findings, "unsupported_runtime") != PreflightBlocked {
		t.Fatalf("incompatible image findings = %#v", imageResult.Findings)
	}

	domainDraft := completePlanningDraftModel()
	domainDraft.Data.Configuration.Domains = []PlannedDomain{{
		Hostname: "app.example.test", HTTPS: true, Ownership: OwnershipManaged,
	}}
	domainResult, err := PreflightDraft(context.Background(), domainDraft, &preflightObserverFake{observation: HostObservation{
		Facilities: map[string]FacilityObservation{"docker": {Available: true}},
		Paths:      []PathObservation{}, Ports: []PortObservation{}, OS: "linux", Architecture: "amd64",
		Domains: []DomainObservation{{
			Hostname: "app.example.test", Addresses: []string{"192.0.2.20"}, DNSAvailable: true,
			PointsHere: true, ProxyAvailable: true, CertificateAvailable: true, Conflict: true,
		}},
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if findingSeverity(domainResult.Findings, "domain_conflict") != PreflightBlocked ||
		findingSeverity(domainResult.Findings, "dns_verified") != PreflightPass {
		t.Fatalf("domain findings = %#v", domainResult.Findings)
	}
}

func TestComposePreflightCoversVariablesPortsStorageAndAdvancedFields(t *testing.T) {
	documents := []ComposeDocument{{Path: "compose.yml", Content: `services:
  app:
    image: "alpine:${APP_TAG}"
    privileged: true
    extends:
      service: base
    ports:
      - "127.0.0.1:18088:80"
    volumes:
      - "/srv/planned-app:/data"
`}}
	source := DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: documents}
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), &planningDockerFake{
		composeAvailable: true, composeValid: true,
	}, nil)
	detection, err := analyzer.Analyze(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	draft := &Draft{
		ID: "compose-preflight", OwnerUserID: 1, OwnerUsername: "admin", Revision: 4,
		Data: DraftData{
			Intent:    &DraftIntentConfig{Name: "compose-app", Profile: ProfileCompose},
			Source:    &source,
			Detection: &detection,
			Configuration: &PlanConfiguration{
				Build: BuildPlanConfig{Method: BuildCompose}, Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst},
				Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{}, Checks: []PlannedCheck{},
			},
		},
	}
	observer := &preflightObserverFake{observation: HostObservation{
		Facilities: map[string]FacilityObservation{"docker": {Available: true}, "compose": {Available: true}},
		Paths:      []PathObservation{{Path: "/srv/planned-app", Contained: true, Exists: true, Writable: true}},
		Ports:      []PortObservation{{Address: "127.0.0.1", Port: 18088, Protocol: "tcp", InUse: true}},
		Domains:    []DomainObservation{}, OS: "linux", Architecture: "amd64",
	}}
	result, err := PreflightDraft(context.Background(), draft, observer, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"compose_variable_app_tag", "compose_unsupported_1", "advanced_authorization_required"} {
		severity := findingSeverity(result.Findings, code)
		if severity != PreflightDecision && severity != PreflightBlocked {
			t.Fatalf("finding %s severity = %q in %#v", code, severity, result.Findings)
		}
	}
	if findingSeverity(result.Findings, "port_conflict") != PreflightWarning ||
		findingSeverity(result.Findings, "backup_policy_missing") != PreflightWarning ||
		findingSeverity(result.Findings, "compose_warning_1") != PreflightWarning {
		t.Fatalf("Compose storage/health warnings = %#v", result.Findings)
	}
	if result.Plan.Compose == nil || len(result.Plan.Compose.Services) != 1 ||
		!result.ExpectedDowntime || !strings.Contains(result.Preview, `"APP_TAG"`) || strings.Contains(result.Preview, "ambient-") {
		t.Fatalf("exact Compose plan = %#v\n%s", result.Plan.Compose, result.Preview)
	}
	if len(observer.requests) != 1 || !reflect.DeepEqual(observer.requests[0].Paths, []string{"/srv/planned-app"}) ||
		len(observer.requests[0].Ports) != 1 || observer.requests[0].Ports[0].Port != 18088 {
		t.Fatalf("Compose observation request = %#v", observer.requests)
	}
}

func findingSeverity(findings []PreflightFinding, code string) PreflightSeverity {
	for _, finding := range findings {
		if finding.Code == code {
			return finding.Severity
		}
	}
	return ""
}

func stringSliceContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// ListDrafts is what the new-project page uses to offer resuming a draft: it
// must show only the caller's own uncommitted, unexpired drafts, newest
// first, with enough of the wizard state to recognise which is which.
func TestListDraftsReturnsOnlyTheOwnersUncommittedUnexpiredDrafts(t *testing.T) {
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	first, err := fixture.plans.Create(ctx, 41, "operator-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.plans.Save(ctx, first.ID, 41, false, DraftSaveRequest{
		Revision: first.Revision, Step: DraftIntent,
		Intent: &DraftIntentConfig{Name: "resume-me", Profile: ProfileWorker},
	}); err != nil {
		t.Fatal(err)
	}
	second, err := fixture.plans.Create(ctx, 41, "operator-a")
	if err != nil {
		t.Fatal(err)
	}
	other, err := fixture.plans.Create(ctx, 99, "operator-b")
	if err != nil {
		t.Fatal(err)
	}

	drafts, err := fixture.plans.ListDrafts(ctx, 41)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 2 {
		t.Fatalf("drafts = %#v, want exactly operator-a's 2", drafts)
	}
	// Newest first.
	if drafts[0].ID != second.ID || drafts[1].ID != first.ID {
		t.Fatalf("drafts order = %#v, want newest (%s) before oldest (%s)", drafts, second.ID, first.ID)
	}
	if drafts[1].Name != "resume-me" || drafts[1].CurrentStep != DraftIntent {
		t.Fatalf("resumable draft summary = %#v", drafts[1])
	}
	for _, draft := range drafts {
		if draft.ID == other.ID {
			t.Fatalf("another owner's draft leaked into the list: %#v", draft)
		}
	}

	// A committed draft has nothing left to resume.
	if _, err := fixture.store.DB.Exec(`UPDATE deploy_drafts SET committed_project_id = 1 WHERE id = ?`, first.ID); err != nil {
		t.Fatal(err)
	}
	// An expired draft is its own recoverable situation, not a resumable one.
	if _, err := fixture.store.DB.Exec(`UPDATE deploy_drafts SET expires_at = 1 WHERE id = ?`, second.ID); err != nil {
		t.Fatal(err)
	}
	drafts, err = fixture.plans.ListDrafts(ctx, 41)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 0 {
		t.Fatalf("drafts after commit/expiry = %#v, want none", drafts)
	}
}

func TestDraftRevisionOwnershipExpiryAndAtomicIdempotentCommit(t *testing.T) {
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	draft, err := fixture.plans.Create(ctx, 41, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if draft.Revision != 1 || draft.ExpiresAt.Sub(draft.CreatedAt) != draftTTL {
		t.Fatalf("new draft = %#v", draft)
	}
	if _, err := fixture.plans.Save(ctx, draft.ID, 42, false, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftIntent,
		Intent: &DraftIntentConfig{Name: "planned-app", Profile: ProfileWorker},
	}); !errors.Is(err, ErrDraftForbidden) {
		t.Fatalf("non-owner save error = %v", err)
	}
	if _, err := fixture.plans.Save(ctx, draft.ID, 41, false, DraftSaveRequest{
		Revision: 0, Step: DraftIntent,
		Intent: &DraftIntentConfig{Name: "planned-app", Profile: ProfileWorker},
	}); !errors.Is(err, ErrDraftRevision) {
		t.Fatalf("stale save error = %v", err)
	}

	draft = saveCompletePlanningDraft(t, fixture.plans, draft)
	preflight, err := PreflightDraft(ctx, draft, &preflightObserverFake{observation: HostObservation{
		Facilities: map[string]FacilityObservation{"git": {Available: true}}, Paths: []PathObservation{}, Ports: []PortObservation{},
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	preflightRevision := preflight.Revision
	tampered := *preflight
	tampered.Preview += "\n{}"
	if _, err := fixture.plans.SavePreflight(ctx, draft.ID, 41, false, draft.Revision, &tampered); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("tampered exact plan error = %v", err)
	}
	tampered = *preflight
	tampered.Findings = append(append([]PreflightFinding(nil), preflight.Findings...), PreflightFinding{
		Code: "bad", Severity: PreflightWarning, Title: "bad", Measured: "API_TOKEN=plain-secret",
	})
	if _, err := fixture.plans.SavePreflight(ctx, draft.ID, 41, false, draft.Revision, &tampered); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("credential-bearing finding error = %v", err)
	}
	draft, err = fixture.plans.SavePreflight(ctx, draft.ID, 41, false, draft.Revision, preflight)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Revision != preflightRevision+1 || preflight.Revision != preflightRevision {
		t.Fatalf("saved preflight revisions draft=%d result=%d input=%d", draft.Revision, preflight.Revision, preflightRevision)
	}

	result, err := fixture.plans.Commit(ctx, draft.ID, 41, true, DraftCommitRequest{Revision: draft.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.ProjectID == 0 || result.EnvironmentID == 0 || result.PlanRevision != 1 {
		t.Fatalf("commit result = %#v", result)
	}
	replay, err := fixture.plans.Commit(ctx, draft.ID, 41, true, DraftCommitRequest{Revision: draft.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if replay.Created || replay.ProjectID != result.ProjectID || replay.EnvironmentID != result.EnvironmentID {
		t.Fatalf("idempotent commit replay = %#v", replay)
	}

	for table, want := range map[string]int{
		"deploy_projects": 1, "deploy_environments": 1, "deploy_sources": 1,
		"deploy_build_plans": 1, "deploy_runtime_plans": 1,
		"deploy_variable_revisions": 1, "deploy_dependencies": 2,
		"deploy_checks": 1, "deploy_triggers": 0, "deploy_runs": 0,
	} {
		if got := planningTableCount(t, fixture.store, table); got != want {
			t.Errorf("%s rows = %d, want %d", table, got, want)
		}
	}
	var sealed string
	if err := fixture.store.DB.QueryRow(`SELECT value_enc FROM deploy_variable_revisions WHERE key = 'API_TOKEN'`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	opened, err := fixture.sealer.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if opened != "${{credential.api-token}}" {
		t.Fatalf("stored variable reference = %q", opened)
	}
	var domainConfig string
	if err := fixture.store.DB.QueryRow(`
		SELECT config_json FROM deploy_dependencies
		 WHERE environment_id = ? AND kind = 'domain' AND resource_id = 'app.example.test'`, result.EnvironmentID).
		Scan(&domainConfig); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(domainConfig, `"https":true`) {
		t.Fatalf("stored domain configuration = %s", domainConfig)
	}
	if strings.Contains(draft.PlanPreview, "plain-secret") {
		t.Fatalf("stored preview contains plaintext credential: %s", draft.PlanPreview)
	}

	expiring, err := fixture.plans.Create(ctx, 41, "operator")
	if err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(draftTTL + time.Second)
	if _, err := fixture.plans.Get(ctx, expiring.ID); !errors.Is(err, ErrDraftExpired) {
		t.Fatalf("expired draft get error = %v", err)
	}
}

func TestCommitRequiresWarningAcknowledgementAndRollsBackNameConflict(t *testing.T) {
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	draft, err := fixture.plans.Create(ctx, 7, "admin")
	if err != nil {
		t.Fatal(err)
	}
	draft = saveCompletePlanningDraft(t, fixture.plans, draft)
	draft.Data.Configuration.Runtime.BindAddress = "0.0.0.0"
	// Save the changed configuration so stale preflight evidence is cleared.
	draft, err = fixture.plans.Save(ctx, draft.ID, 7, true, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: draft.Data.Configuration,
	})
	if err != nil {
		t.Fatal(err)
	}
	preflight, err := PreflightDraft(ctx, draft, &preflightObserverFake{observation: HostObservation{
		Facilities: map[string]FacilityObservation{"git": {Available: true}}, Paths: []PathObservation{}, Ports: []PortObservation{},
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	draft, err = fixture.plans.SavePreflight(ctx, draft.ID, 7, true, draft.Revision, preflight)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.plans.Commit(ctx, draft.ID, 7, true, DraftCommitRequest{Revision: draft.Revision}); !errors.Is(err, ErrPreflightBlocked) {
		t.Fatalf("unacknowledged warning commit error = %v", err)
	}
	if planningTableCount(t, fixture.store, "deploy_projects") != 0 {
		t.Fatal("blocked commit created a project")
	}

	if _, err := fixture.store.DB.Exec(`
		INSERT INTO deploy_projects(
		  name, profile, repo_path, branch, compose_file, pre_command, post_command,
		  hook_secret, hook_id, enabled, created_at, updated_at)
		VALUES('planned-app', 'worker', '/srv/existing', 'main', 'compose.yml', '', '',
		       'sealed', 'existing-hook', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.plans.Commit(ctx, draft.ID, 7, true, DraftCommitRequest{
		Revision: draft.Revision, AcknowledgedWarnings: []string{"public_bind"},
	}); err == nil {
		t.Fatal("duplicate project name commit succeeded")
	}
	if planningTableCount(t, fixture.store, "deploy_environments") != 0 ||
		planningTableCount(t, fixture.store, "deploy_sources") != 0 {
		t.Fatal("name-conflict transaction left partial planning rows")
	}
}

type preflightObserverFake struct {
	observation HostObservation
	calls       int
	requests    []ObservationRequest
}

type credentialReaderFake struct {
	material CredentialMaterial
	err      error
}

type planningProxyFake struct {
	availabilityCalls int
	listCalls         int
	vhosts            []proxysvc.VHost
}

func (f *planningProxyFake) Availability(context.Context) proxysvc.Availability {
	f.availabilityCalls++
	return proxysvc.Availability{Nginx: true, Certbot: true}
}

func (f *planningProxyFake) ListVHosts(context.Context) ([]proxysvc.VHost, error) {
	f.listCalls++
	return append([]proxysvc.VHost(nil), f.vhosts...), nil
}

func (f credentialReaderFake) OpenCredential(context.Context, int64) (CredentialMaterial, error) {
	return f.material, f.err
}

func (f *preflightObserverFake) Observe(_ context.Context, request ObservationRequest) (HostObservation, error) {
	f.calls++
	f.requests = append(f.requests, request)
	return f.observation, nil
}

type planningDockerFake struct {
	composeAvailable   bool
	composeValid       bool
	buildxAvailable    bool
	validateFilesCalls int
	validatedInputs    []dockerx.ComposeInput
	validatedProject   string
	validatedVariables []string
	image              *dockerx.DistributionImage
	imageDetail        *dockerx.ImageDetail
	container          *dockerx.ContainerSpec
	stacks             []dockerx.ComposeStack
	registryAuth       string
}

func (f *planningDockerFake) Ping(context.Context) dockerx.Availability {
	return dockerx.Availability{Available: true}
}

func (f *planningDockerFake) ResolveDistributionImage(_ context.Context, _ string, auth string) (*dockerx.DistributionImage, error) {
	f.registryAuth = auth
	if f.image == nil {
		return nil, errors.New("image missing")
	}
	copy := *f.image
	return &copy, nil
}

func (f *planningDockerFake) InspectImage(context.Context, string) (*dockerx.ImageDetail, error) {
	if f.imageDetail == nil {
		return nil, errors.New("image is not present on this host")
	}
	copy := *f.imageDetail
	return &copy, nil
}

func (f *planningDockerFake) SpecOf(context.Context, string) (*dockerx.ContainerSpec, error) {
	if f.container == nil {
		return nil, errors.New("container missing")
	}
	copy := *f.container
	return &copy, nil
}

func (f *planningDockerFake) ListStacks(context.Context, []string) ([]dockerx.ComposeStack, error) {
	return append([]dockerx.ComposeStack(nil), f.stacks...), nil
}

func (f *planningDockerFake) ComposeAvailable(context.Context) bool { return f.composeAvailable }
func (f *planningDockerFake) BuildxAvailable(context.Context) bool  { return f.buildxAvailable }

func (f *planningDockerFake) ValidateCompose(context.Context, string, string) (*dockerx.ComposeValidation, error) {
	return &dockerx.ComposeValidation{Valid: f.composeValid, Services: []string{}}, nil
}

func (f *planningDockerFake) ValidateComposePlan(_ context.Context, project string, inputs []dockerx.ComposeInput, variables []string) (*dockerx.ComposeValidation, error) {
	f.validateFilesCalls++
	f.validatedProject = project
	f.validatedInputs = append([]dockerx.ComposeInput(nil), inputs...)
	f.validatedVariables = append([]string(nil), variables...)
	return &dockerx.ComposeValidation{Valid: f.composeValid, Services: []string{}}, nil
}

type planningStoreFixture struct {
	store  *basestore.Store
	plans  *PlanningStore
	sealer *auth.Sealer
	now    time.Time
}

func newPlanningStoreFixture(t *testing.T) *planningStoreFixture {
	t.Helper()
	st, err := basestore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatal(err)
	}
	fixture := &planningStoreFixture{
		store: st, sealer: sealer, now: time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
	}
	fixture.plans = NewPlanningStore(st, sealer, []string{t.TempDir()})
	fixture.plans.now = func() time.Time { return fixture.now }
	return fixture
}

func saveCompletePlanningDraft(t *testing.T, plans *PlanningStore, draft *Draft) *Draft {
	t.Helper()
	ctx := context.Background()
	var err error
	draft, err = plans.Save(ctx, draft.ID, draft.OwnerUserID, true, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftIntent,
		Intent: &DraftIntentConfig{Name: "planned-app", Profile: ProfileWorker},
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err = plans.Save(ctx, draft.ID, draft.OwnerUserID, true, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftSource,
		Source: &DraftSourceConfig{
			Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/owner/repo.git", Ref: "main",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := newDetectedCandidate("", BuildNone, DetectedCandidate{
		Name: "worker", Profile: ProfileWorker, Confidence: ConfidenceHigh,
		Evidence: []DetectionEvidence{{Path: "go.mod", Reason: "fixture"}}, NeedsDecision: []string{},
	})
	draft, err = plans.SaveDetection(ctx, draft.ID, draft.OwnerUserID, true, draft.Revision, DetectionResult{
		Source: SourceIdentity{
			Kind: SourceGit, Remote: "https://example.test/owner/repo.git", Repository: "owner/repo",
			Ref: "main", Revision: strings.Repeat("a", 40),
		},
		Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	configuration := PlanConfiguration{
		Build:   BuildPlanConfig{Method: BuildNone},
		Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst},
		Variables: []PlannedVariable{{
			Name: "API_TOKEN", Sensitivity: "secret", Scopes: []string{"runtime"}, Required: true,
			Reference: "${{credential.api-token}}",
		}},
		Dependencies: []PlannedDependency{{
			Kind: "cache", Ownership: OwnershipLinked, ResourceKind: "redis", ResourceID: "shared-cache",
			Config: json.RawMessage(`{"password":"${{credential.redis-password}}"}`),
		}},
		Checks: []PlannedCheck{{
			Name: "worker-smoke", Kind: "command", Phase: "smoke", Required: true,
			Config: json.RawMessage(`{"command":["worker","check"]}`),
		}},
		Domains:    []PlannedDomain{{Hostname: "App.Example.Test", HTTPS: true, Ownership: OwnershipManaged}},
		AutoDeploy: true,
	}
	draft, err = plans.Save(ctx, draft.ID, draft.OwnerUserID, true, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: &configuration,
	})
	if err != nil {
		t.Fatal(err)
	}
	return draft
}

func completePlanningDraftModel() *Draft {
	candidate := newDetectedCandidate("", BuildImage, DetectedCandidate{
		Name: "image", Profile: ProfileWorker, Confidence: ConfidenceHigh,
		Evidence: []DetectionEvidence{{Path: "alpine:3", Reason: "registry digest"}}, NeedsDecision: []string{},
	})
	return &Draft{
		ID: "draft-model", OwnerUserID: 1, OwnerUsername: "admin", CurrentStep: DraftConfiguration, Revision: 5,
		Data: DraftData{
			Intent: &DraftIntentConfig{Name: "image-worker", Profile: ProfileWorker},
			Source: &DraftSourceConfig{Kind: SourceImage, Mode: SourceModeImageReference, Image: "alpine:3"},
			Detection: &DetectionResult{
				Source:     SourceIdentity{Kind: SourceImage, Repository: "docker.io/library/alpine:3", Digest: testImageDigest},
				Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID,
			},
			Configuration: &PlanConfiguration{
				Build:   BuildPlanConfig{Method: BuildImage},
				Runtime: RuntimePlanConfig{Image: "alpine:3", Command: []string{"sleep", "3600"}, Strategy: StrategyStopFirst},
				Variables: []PlannedVariable{{
					Name: "API_TOKEN", Sensitivity: "secret", Scopes: []string{"runtime"}, Reference: "${{credential.api-token}}",
				}},
				Dependencies: []PlannedDependency{}, Checks: []PlannedCheck{},
			},
		},
	}
}

func planningTableCount(t *testing.T, st *basestore.Store, table string) int {
	t.Helper()
	allowed := map[string]bool{
		"deploy_projects": true, "deploy_environments": true, "deploy_sources": true,
		"deploy_build_plans": true, "deploy_runtime_plans": true,
		"deploy_variable_revisions": true, "deploy_dependencies": true,
		"deploy_checks": true, "deploy_triggers": true, "deploy_runs": true,
	}
	if !allowed[table] {
		t.Fatalf("test attempted to count unapproved table %q", table)
	}
	var count int
	if err := st.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func writePlanningFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runPlanningGitFixture(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
}

func planningGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func TestRegistryCredentialIsPassedOutOfBandAndNotReturned(t *testing.T) {
	fixture := newPlanningStoreFixture(t)
	sealed, err := fixture.sealer.Seal("registry-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB.Exec(`
		INSERT INTO deploy_credentials(name, kind, config_json, secret_enc, created_at, updated_at)
		VALUES('registry', 'registry', '{"username":"robot","serverAddress":"registry.example.test"}', ?, 1, 1)`, sealed); err != nil {
		t.Fatal(err)
	}
	var credentialID int64
	if err := fixture.store.DB.QueryRow(`SELECT id FROM deploy_credentials WHERE name = 'registry'`).Scan(&credentialID); err != nil {
		t.Fatal(err)
	}
	fake := &planningDockerFake{image: &dockerx.DistributionImage{
		Reference: "registry.example.test/team/app:1", Digest: testImageDigest, Platforms: []string{"linux/amd64"},
	}}
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), fake, fixture.plans)
	result, err := analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceImage, Mode: SourceModeImageReference, Image: "registry.example.test/team/app:1", CredentialID: credentialID,
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.URLEncoding.DecodeString(fake.registryAuth)
	if err != nil || !strings.Contains(string(decoded), "registry-secret") {
		t.Fatalf("registry auth was not passed to the Engine request: %q, %v", decoded, err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "registry-secret") || strings.Contains(string(encoded), fake.registryAuth) {
		t.Fatalf("registry result leaked credential material: %s", encoded)
	}
	wrongRegistryDocker := &planningDockerFake{image: fake.image}
	wrongRegistry := NewHostSourceAnalyzer(nil, nil, t.TempDir(), wrongRegistryDocker, credentialReaderFake{
		material: CredentialMaterial{
			Kind: "registry", Config: json.RawMessage(`{"username":"robot","serverAddress":"docker.io"}`), Secret: "registry-secret",
		},
	})
	_, err = wrongRegistry.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceImage, Mode: SourceModeImageReference, Image: "registry.example.test/team/app:1", CredentialID: 1,
	})
	if !errors.Is(err, ErrDockerUnavailable) || wrongRegistryDocker.registryAuth != "" || strings.Contains(err.Error(), "registry-secret") {
		t.Fatalf("cross-registry credential was not refused before lookup: %v, auth=%q", err, wrongRegistryDocker.registryAuth)
	}
}

func TestProviderRemoteNormalization(t *testing.T) {
	cases := []struct {
		source DraftSourceConfig
		remote string
	}{
		{DraftSourceConfig{Kind: SourceGit, Mode: SourceModeConnectedRepository, Provider: "github", Repository: "Owner/Repo"}, "https://github.com/Owner/Repo.git"},
		{DraftSourceConfig{Kind: SourceGit, Mode: SourceModeConnectedRepository, Provider: "gitlab", Repository: "group/repo"}, "https://gitlab.com/group/repo.git"},
		{DraftSourceConfig{Kind: SourceGit, Mode: SourceModeConnectedRepository, Provider: "bitbucket", Repository: "team/repo"}, "https://bitbucket.org/team/repo.git"},
		{DraftSourceConfig{Kind: SourceGit, Mode: SourceModeConnectedRepository, Provider: "gitea", ProviderBaseURL: "https://git.example.test/gitea", Repository: "team/repo"}, "https://git.example.test/gitea/team/repo.git"},
	}
	for _, test := range cases {
		remote, repository, err := remoteForSource(test.source)
		if err != nil {
			t.Fatalf("%s: %v", test.source.Provider, err)
		}
		if remote != test.remote || repository != test.source.Repository {
			t.Errorf("%s remote = %q/%q, want %q/%q", test.source.Provider, remote, repository, test.remote, test.source.Repository)
		}
	}
	bad := DraftSourceConfig{
		Kind: SourceGit, Mode: SourceModeConnectedRepository, Provider: "gitea",
		ProviderBaseURL: "https://user:password@git.example.test", Repository: "team/repo",
	}
	if _, _, err := remoteForSource(bad); err == nil {
		t.Fatal("Gitea base URL with embedded credentials was accepted")
	}
	for _, test := range []struct {
		input, remote, clone string
	}{
		{"main", "refs/heads/main", "main"},
		{"refs/heads/release", "refs/heads/release", "release"},
		{"refs/tags/v1.2.3", "refs/tags/v1.2.3", "v1.2.3"},
	} {
		remote, clone := planningGitRef(test.input)
		if remote != test.remote || clone != test.clone {
			t.Errorf("planningGitRef(%q) = %q/%q, want %q/%q", test.input, remote, clone, test.remote, test.clone)
		}
	}
}

func TestCanonicalConfigurationOrdering(t *testing.T) {
	configuration := PlanConfiguration{
		Build: BuildPlanConfig{Method: BuildNone}, Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst},
		Variables: []PlannedVariable{
			{Name: "ZED", Sensitivity: "plain", Scopes: []string{"runtime"}},
			{Name: "ALPHA", Sensitivity: "plain", Scopes: []string{"runtime"}},
		},
		Dependencies: []PlannedDependency{
			{Kind: "database", Ownership: OwnershipLinked, ResourceKind: "postgres", ResourceID: "z"},
			{Kind: "backup", Ownership: OwnershipLinked, ResourceKind: "backup_job", ResourceID: "a"},
		},
		Checks: []PlannedCheck{
			{Name: "z", Kind: "command", Phase: "smoke"},
			{Name: "a", Kind: "command", Phase: "smoke"},
		},
	}
	canonical := canonicalConfiguration(configuration)
	if canonical.Variables[0].Name != "ALPHA" || canonical.Dependencies[0].Kind != "backup" || canonical.Checks[0].Name != "a" {
		t.Fatalf("configuration was not canonicalized: %#v", canonical)
	}
	// Guard against callers accidentally depending on map iteration elsewhere.
	names := []string{canonical.Variables[0].Name, canonical.Variables[1].Name}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"ALPHA", "ZED"}) {
		t.Fatal(names)
	}
}

// A Node project's detected commands have to name the package manager its
// lockfile locks to. The recipe picks its base image from that same lockfile,
// and oven/bun carries no npm: "npm run build" was a build that installed
// cleanly and then died on `npm: not found`, with the configuration screen
// showing nothing wrong.
func TestDetectedJavaScriptCommandsFollowTheLockfile(t *testing.T) {
	manifest := `{"name":"site","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.2.10"}}`
	for _, tc := range []struct{ lockfile, build, start string }{
		{"bun.lock", "bun run build", "bun run start"},
		{"bun.lockb", "bun run build", "bun run start"},
		{"pnpm-lock.yaml", "pnpm run build", "pnpm run start"},
		{"yarn.lock", "yarn run build", "yarn run start"},
		{"package-lock.json", "npm run build", "npm run start"},
	} {
		t.Run(tc.lockfile, func(t *testing.T) {
			root := t.TempDir()
			writePlanningFixture(t, filepath.Join(root, "package.json"), manifest)
			writePlanningFixture(t, filepath.Join(root, tc.lockfile), "{}")

			result, err := (Detector{}).DetectPath(context.Background(), root,
				SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Candidates) != 1 {
				t.Fatalf("candidates = %#v, want exactly one", result.Candidates)
			}
			candidate := result.Candidates[0]
			if candidate.BuildCommand != tc.build || candidate.StartCommand != tc.start {
				t.Fatalf("commands = %q / %q, want %q / %q",
					candidate.BuildCommand, candidate.StartCommand, tc.build, tc.start)
			}
			if candidate.Framework != "nextjs" || candidate.Profile != ProfileWeb || candidate.Port != 3000 {
				t.Fatalf("next.js candidate = %#v", candidate)
			}
		})
	}
}

// With no lockfile, or with several, the recipe refuses to build at all — so
// the detected command only has to be the one that fails legibly rather than
// the one that happens to match a package manager nobody pinned.
func TestDetectedJavaScriptCommandsFallBackToNpm(t *testing.T) {
	root := t.TempDir()
	writePlanningFixture(t, filepath.Join(root, "package.json"),
		`{"name":"site","scripts":{"build":"tsc"},"dependencies":{}}`)
	writePlanningFixture(t, filepath.Join(root, "bun.lock"), "{}")
	writePlanningFixture(t, filepath.Join(root, "yarn.lock"), "")

	result, err := (Detector{}).DetectPath(context.Background(), root,
		SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].BuildCommand != "npm run build" {
		t.Fatalf("ambiguous lockfiles = %#v, want an npm command", result.Candidates)
	}
	if !strings.Contains(strings.Join(result.Candidates[0].NeedsDecision, " "), "competing lockfiles") {
		t.Fatalf("ambiguity is not reported: %#v", result.Candidates[0].NeedsDecision)
	}
}
