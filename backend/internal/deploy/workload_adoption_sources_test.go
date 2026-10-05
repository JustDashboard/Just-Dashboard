package deploy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
)

func TestRecoveredBuildSourceMaterializesFrozenInputsAndSeparateImageBaseline(t *testing.T) {
	root, recovery := t.TempDir(), t.TempDir()
	for name, content := range map[string]string{"compose.yml": "services:\n  web:\n    build: .\n    image: example/web:latest\n", "Dockerfile": "FROM node:24\nWORKDIR /app\nCOPY . .\nCMD [\"node\", \"server.js\"]\n", "package.json": `{"name":"imported-app","scripts":{"start":"node server.js"},"dependencies":{"express":"5.1.0"}}`, "server.js": "console.log('captured source')", ".env": "TOKEN=private-file-value"} {
		writeBuildFixture(t, root, name, content)
	}
	capture := adoptionCaptureFixture(t, "web", true)
	capture.Inspection.Config.Labels = map[string]string{"com.docker.compose.project": "original", "com.docker.compose.service": "web", "com.docker.compose.container-number": "1", "com.docker.compose.project.config_files": filepath.Join(root, "compose.yml")}
	reader := &adoptionReaderFake{captures: map[string]*dockerx.AdoptionContainer{capture.Inspection.ID: capture}, compose: []byte(`{"services":{"web":{"image":"example/web:latest","build":{"context":"` + root + `","dockerfile":"Dockerfile"}}}}`)}
	candidate := WorkloadCandidate{Key: "stack:original", Kind: "stack", Name: "original", ResourceID: "original", SourcePath: filepath.Join(root, "compose.yml"), Total: 1, Running: 1, Services: []WorkloadService{{Name: "web", ResourceID: capture.Inspection.ID}}}
	result, err := RecoverDockerWorkload(context.Background(), candidate, reader, files.New([]string{root}), recovery)
	if err != nil {
		t.Fatalf("recover=%v blockers=%v", err, result.Adoption.Blockers)
	}
	if result.Source.Mode != SourceModeRecoveredSnapshot || len(result.Adoption.BuildSources) != 1 || result.Adoption.BuildSources[0].Status != "snapshot" {
		t.Fatalf("source not recovered: %+v", result.Adoption.BuildSources)
	}
	if strings.Contains(result.Adoption.BaselineSource.ComposeFiles[0].Content, "build:") || result.Adoption.BaselineDetection.Source.Digest == result.Detection.Source.Digest {
		t.Fatal("desired source changed live image baseline")
	}
	writeBuildFixture(t, root, "server.js", "console.log('new external edit')")
	analyzer := NewHostSourceAnalyzer([]string{root}, nil, t.TempDir(), nil, nil).WithRecoveryRoot(recovery)
	materialized, err := analyzer.Materialize(context.Background(), result.Source, result.Detection.Source, 1, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(materialized.Root, "contexts", "web", "server.js"))
	if err != nil || string(content) != "console.log('captured source')" {
		t.Fatalf("build did not freeze source: %s %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(materialized.Root, "contexts", "web", ".env")); !os.IsNotExist(err) {
		t.Fatal("private file copied into source")
	}
	if capture.Inspection.Config.Image != "example/web:latest" || !capture.Inspection.State.Running {
		t.Fatal("source preparation changed live capture")
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "private-file-value") {
		t.Fatal("private source leaked")
	}
}

func TestRecoveredPrimaryKeepsApplicationAndEveryBaselineContainer(t *testing.T) {
	web, db := adoptionCaptureFixture(t, "web", true), adoptionCaptureFixture(t, "db", true)
	web.Inspection.Config.Image = "example/worker:latest"
	web.Inspection.HostConfig.PortBindings = nil
	db.Inspection.Config.Image = "postgres:17"
	recovery := &dockerRecovery{containers: map[string][]*dockerx.AdoptionContainer{"worker": {web}, "db": {db}}, builds: map[string]any{}, result: &RecoveredWorkload{Environment: map[string]string{}, Adoption: &WorkloadAdoption{}}}
	analysis := ComposeAnalysis{Services: []ComposeServicePlan{{Name: "db", Image: db.Image.ID, Ports: []string{"5432:5432"}}, {Name: "worker", Image: web.Image.ID}}}
	primary := recovery.recoveredPrimaryService(analysis)
	if primary != "worker" {
		t.Fatalf("selected database as app: %s", primary)
	}
	snapshot := &ResolvedComposeSnapshot{PrimaryService: primary}
	recovery.captureBaseline(WorkloadCandidate{Kind: "stack", ResourceID: "original"}, snapshot)
	var metadata dockerReleaseRuntimeMetadata
	if err := json.Unmarshal(recovery.result.Adoption.Runtime.Metadata, &metadata); err != nil {
		t.Fatal(err)
	}
	if len(metadata.BaselineContainers) != 2 || metadata.PrimaryContainerID != web.Inspection.ID {
		t.Fatalf("baseline membership/primary lost: %+v", metadata)
	}
}

func TestNativeRecoveryBindsNestedWorkerAndPreservesSourceLayout(t *testing.T) {
	root, analyzer, candidate, capture := hostRecoveryFixture(t)
	for name, content := range map[string]string{"web/package.json": `{"name":"web","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"15.3.0"}}`, "worker/package.json": `{"name":"worker","scripts":{"start":"node worker.js"}}`, "worker/worker.js": "setInterval(()=>{},1000)"} {
		writeBuildFixture(t, root, name, content)
	}
	capture.SourcePath = filepath.Join(root, "worker", "worker.js")
	capture.Command = []string{"/usr/bin/node", capture.SourcePath}
	result, err := RecoverHostWorkload(context.Background(), candidate, capture, analyzer, files.New([]string{root}), t.TempDir())
	if err != nil || len(result.Adoption.Blockers) != 0 {
		t.Fatalf("recovery=%v blockers=%v", err, result.Adoption.Blockers)
	}
	if result.Configuration.Build.RootDirectory != "worker" || !result.Configuration.Build.PreserveSourceRoot || result.Environment["JD_IMPORTED_ARG_1"] != "/app/worker/worker.js" {
		t.Fatalf("wrong native app/layout: %+v", result.Configuration.Build)
	}
	recipe, err := selectRecipe(root, filepath.Join(root, "worker"), result.Configuration.Build)
	if err != nil || recipe.contextDir != "." || recipe.installDirectory != "worker" {
		t.Fatalf("install/layout=%+v %v", recipe, err)
	}
}

func TestNativePythonRecoveryPreservesInterpreterVersionAndCommand(t *testing.T) {
	root, analyzer, candidate, capture := hostRecoveryFixture(t)
	if err := os.Remove(filepath.Join(root, "package.json")); err != nil {
		t.Fatal(err)
	}
	writeBuildFixture(t, root, "requirements.txt", "fastapi==0.115.0\nuvicorn==0.34.0\n")
	writeBuildFixture(t, root, "app.py", "from fastapi import FastAPI\napp = FastAPI()\n")
	capture.SourcePath = "/usr/bin/python3.11"
	capture.Command = []string{"/usr/bin/python3.11", "-m", "uvicorn", "app:app", "--port", "3000"}
	capture.InterpreterVersion = "3.11.16"
	result, err := RecoverHostWorkload(context.Background(), candidate, capture, analyzer, files.New([]string{root}), t.TempDir())
	if err != nil || len(result.Adoption.Blockers) != 0 {
		t.Fatalf("Python recovery=%v blockers=%v", err, result.Adoption.Blockers)
	}
	if result.Configuration.Build.Recipe != "python" || result.Configuration.Build.PythonVersion != "3.11" || result.Environment["JD_IMPORTED_ARG_0"] != "/usr/local/bin/python" {
		t.Fatalf("Python parity lost: %+v", result.Configuration.Build)
	}
}

func TestNativeNextRecoveryBuildsOnlyVerifiedCapturedInputs(t *testing.T) {
	root, analyzer, candidate, capture := hostRecoveryFixture(t)
	writeBuildFixture(t, root, "package.json", `{"name":"next-app","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"15.3.0"}}`)
	capture.Environment["NEXT_PUBLIC_API_URL"] = "https://api.example.test"
	capture.Environment["UNRELATED_SECRET"] = "runtime-private"
	result, err := RecoverHostWorkload(t.Context(), candidate, capture, analyzer, files.New([]string{root}), t.TempDir())
	if err != nil || len(result.Adoption.Blockers) != 0 {
		t.Fatalf("Next recovery=%v blockers=%v", err, result.Adoption.Blockers)
	}
	buildInputs := map[string]string{}
	for _, input := range result.Configuration.Build.Secrets {
		buildInputs[input.Variable] = input.Step
	}
	if buildInputs["NEXT_PUBLIC_API_URL"] != "build" || buildInputs["UNRELATED_SECRET"] != "" || buildInputs["TOKEN"] != "" {
		t.Fatalf("incorrect captured build inputs: %v", buildInputs)
	}
	for _, input := range result.Adoption.BaselineConfiguration.Variables {
		if len(input.Scopes) != 1 || input.Scopes[0] != "runtime" || strings.HasPrefix(input.Name, "JD_IMPORTED_ARG_") {
			t.Fatalf("native baseline inherited desired build or arguments: %+v", input)
		}
	}
}

func TestCapturedImageOnlyApplicationEvidenceDoesNotInventBuildSource(t *testing.T) {
	for _, fixture := range []struct {
		name, image         string
		command             []string
		framework, language string
		role                WorkloadProfile
	}{
		{"Next standalone", "example/frontend:latest", []string{"node", ".next/standalone/server.js"}, "next", "node", ProfileWeb},
		{"database", "postgres:17", []string{"postgres"}, "postgres", "", ProfileService},
		{"Python queue", "example/queue:latest", []string{"celery", "-A", "app", "worker"}, "", "python", ProfileWorker},
		{"opaque app", "example/unknown:latest", []string{"/app/custom-service"}, "", "", ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			capture := adoptionCaptureFixture(t, "web", true)
			capture.Inspection.Config.Image = fixture.image
			capture.Inspection.Config.Entrypoint = nil
			capture.Inspection.Config.Cmd = fixture.command
			entry := RecoveredBuildSource{Status: "image_only"}
			capturedBuildSourceEvidence(&entry, capture)
			if entry.Framework != fixture.framework || entry.Language != fixture.language || entry.Role != fixture.role || entry.Status != "image_only" || entry.SnapshotDigest != "" {
				t.Fatalf("unsupported runtime/source claim: %+v", entry)
			}
		})
	}
}
