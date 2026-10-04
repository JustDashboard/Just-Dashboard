package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
)

func TestHostRecoveryRequiresVerifiableDockerfileLayoutAndEmptyEntrypoint(t *testing.T) {
	for _, fixture := range []struct {
		name, dockerfile string
		blocked          bool
	}{
		{"supported", "FROM node:24\nWORKDIR /app\nCOPY . .\nENTRYPOINT []\n", false},
		{"wrong layout", "FROM node:24\nWORKDIR /srv/site\nCOPY . .\nENTRYPOINT []\n", true},
		{"unknown inherited entrypoint", "FROM node:24\nWORKDIR /app\nCOPY . .\n", true},
		{"command entrypoint", "FROM node:24\nWORKDIR /app\nCOPY . .\nENTRYPOINT [\"node\",\"server.js\"]\n", true},
		{"missing source", "FROM node:24\nWORKDIR /app\nENTRYPOINT []\n", true},
		{"inherited known layout", "FROM node:24 AS base\nWORKDIR /app\nCOPY . .\nENTRYPOINT []\nFROM base AS prod\n", false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte(fixture.dockerfile), 0644); err != nil {
				t.Fatal(err)
			}
			var blockers []string
			verifyHostDockerfileLayout(root, BuildPlanConfig{Method: BuildDockerfile, Dockerfile: "Dockerfile"}, func(code, _, _ string) { blockers = append(blockers, code) })
			if (len(blockers) > 0) != fixture.blocked {
				t.Fatalf("layout blockers=%v", blockers)
			}
		})
	}
}

func TestHostRecoveryBaselineDigestFencesSourceAndPrivateEnvironment(t *testing.T) {
	root, analyzer, candidate, capture := hostRecoveryFixture(t)
	recover := func() string {
		t.Helper()
		result, err := RecoverHostWorkload(context.Background(), candidate, capture, analyzer, files.New([]string{root}), "")
		if err != nil {
			t.Fatal(err)
		}
		return result.Adoption.BaselineDigest
	}
	first := recover()
	capture.Environment["TOKEN"] = "another-private-value"
	second := recover()
	if first == second {
		t.Fatal("private environment change reused baseline digest")
	}
	if err := os.WriteFile(filepath.Join(root, "server.js"), []byte("console.log('source changed')"), 0644); err != nil {
		t.Fatal(err)
	}
	if recover() == second {
		t.Fatal("source change reused baseline digest")
	}
}

func hostRecoveryFixture(t *testing.T) (string, *HostSourceAnalyzer, WorkloadCandidate, *procs.HostWorkloadCapture) {
	t.Helper()
	root := t.TempDir()
	for name, content := range map[string]string{"package.json": `{"name":"existing-node-app","version":"1.0.0","scripts":{"start":"node server.js"}}`, "server.js": `require("http").createServer((req,res)=>res.end("native application")).listen(3000)`} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	analyzer := NewHostSourceAnalyzer([]string{root}, nil, t.TempDir(), nil, nil)
	candidate := WorkloadCandidate{Key: "pm2:alice/default/api", Kind: "pm2", ResourceID: "alice/default/api", Name: "api", Running: 1, Total: 1, Digest: "fresh-discovery", Services: []WorkloadService{{Name: "api", Ports: []dockerx.PortMapping{{HostIP: "127.0.0.1", HostPort: 3000, ContainerPort: 3000, Protocol: "tcp"}}}}}
	capture := &procs.HostWorkloadCapture{Manager: "pm2", ResourceID: candidate.ResourceID, Name: "api", Account: "alice", UID: 1000, GID: 1001, SourceDirectory: root, SourcePath: filepath.Join(root, "server.js"), InterpreterVersion: "24.12.0", ConfigurationDigest: "fresh-config", Environment: map[string]string{"PORT": "3000", "TOKEN": "private-production-value"}, EnvironmentNames: []string{"PORT", "TOKEN"}, Command: []string{"/usr/local/bin/node", filepath.Join(root, "server.js")}, Processes: []procs.HostProcessCapture{{ID: 7, PID: 123, State: "online", LogSources: []string{"pm2:alice/7/api"}}}}
	return root, analyzer, candidate, capture
}

func TestHostRecoveryCreatesNormalManagedNodePlanAndNativeRollbackBaseline(t *testing.T) {
	root, analyzer, candidate, capture := hostRecoveryFixture(t)
	cache := t.TempDir()
	recovered, err := RecoverHostWorkload(context.Background(), candidate, capture, analyzer, files.New([]string{root}), cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.Adoption.Blockers) != 0 {
		t.Fatalf("supported node recovery blocked: %v", recovered.Adoption.Blockers)
	}
	if recovered.Source.Kind != SourceLocal || recovered.Source.Mode != SourceModeLocalDirectory || recovered.Configuration.Build.Method != BuildRecipe || recovered.Configuration.Build.Recipe != "node" || recovered.Configuration.Build.NodeVersion != "24" {
		t.Fatalf("source/build not restored: %+v %+v", recovered.Source, recovered.Configuration.Build)
	}
	runtime := recovered.Configuration.Runtime
	if runtime.User != "1000:1001" || runtime.WorkingDirectory != "/app" || !runtime.HostNetwork || runtime.HostPort != 3000 || runtime.Strategy != StrategyStopFirst {
		t.Fatalf("runtime parity missing: %+v", runtime)
	}
	if recovered.Environment["TOKEN"] != "private-production-value" || recovered.Environment["JD_IMPORTED_ARG_0"] != "node" || recovered.Environment["JD_IMPORTED_ARG_1"] != "/app/server.js" {
		t.Fatal("environment or exact argv lost")
	}
	if recovered.Adoption.Runtime.Kind != "pm2" || recovered.Adoption.Runtime.RuntimeID != candidate.ResourceID || recovered.Adoption.Runtime.Port != 3000 {
		t.Fatal("native baseline identity lost")
	}
	var snapshot runtimeReleaseSnapshot
	if json.Unmarshal(recovered.Adoption.Snapshot, &snapshot) != nil || snapshot.NativeBaseline == nil {
		t.Fatal("baseline release cannot be restored")
	}
	encoded, _ := json.Marshal(recovered)
	if strings.Contains(string(encoded), "private-production-value") || strings.Contains(string(encoded), filepath.Join(root, "server.js")) {
		t.Fatal("private environment or original argv leaked into review")
	}
	if err := recovered.Source.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := recovered.Configuration.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := validateDetectionResult(&recovered.Source, recovered.Detection); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(recovered.Adoption.RecoveryDirectory); err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("recovered snapshot not private")
	}
}

func TestHostRecoveryRetainsDataWithoutCopyingLiveFilesAndBlocksPrivateConfig(t *testing.T) {
	root, analyzer, candidate, capture := hostRecoveryFixture(t)
	if err := os.Mkdir(filepath.Join(root, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data", "production.sqlite"), []byte("live database content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=private file value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err := RecoverHostWorkload(context.Background(), candidate, capture, analyzer, files.New([]string{root}), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.Adoption.Blockers) == 0 || len(recovered.Configuration.Runtime.Mounts) != 1 || recovered.Configuration.Runtime.Mounts[0].Source != filepath.Join(root, "data") || recovered.Configuration.Runtime.Mounts[0].Ownership != OwnershipLinked {
		t.Fatalf("data/private config not covered: %+v", recovered.Adoption)
	}
	for _, name := range []string{"data", ".env"} {
		if _, err := os.Stat(filepath.Join(recovered.Adoption.RecoveryDirectory, name)); !os.IsNotExist(err) {
			t.Fatalf("private/live %s copied into source archive", name)
		}
	}
	before, err := localDirectoryDigest(context.Background(), root, recovered.Source.ExcludePaths)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data", "production.sqlite"), []byte("database changes while app remains live"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := localDirectoryDigest(context.Background(), root, recovered.Source.ExcludePaths)
	if err != nil || before != after {
		t.Fatal("mutable linked data invalidates source capture")
	}
	encoded, _ := json.Marshal(recovered)
	if strings.Contains(string(encoded), "private file value") || strings.Contains(string(encoded), "live database content") {
		t.Fatal("private source content leaked")
	}
}

func TestHostRecoveryRefusesSourceEscapeUnknownRuntimeAndUnmanagedCutover(t *testing.T) {
	root, analyzer, candidate, capture := hostRecoveryFixture(t)
	capture.SourceDirectory = filepath.Dir(root)
	recovered, err := RecoverHostWorkload(context.Background(), candidate, capture, analyzer, files.New([]string{root}), "")
	if err != nil || len(recovered.Adoption.Blockers) == 0 {
		t.Fatal("outside-root source accepted")
	}
	capture.SourceDirectory = root
	capture.InterpreterVersion = "18.20.0"
	capture.Command = []string{"/bin/custom-runtime", "server.js"}
	recovered, err = RecoverHostWorkload(context.Background(), candidate, capture, analyzer, files.New([]string{root}), "")
	if err != nil || len(recovered.Adoption.Blockers) < 2 {
		t.Fatal("unknown runtime silently translated")
	}
	capture.Manager = "process"
	capture.InterpreterVersion = "24.12.0"
	capture.Command = []string{"node", filepath.Join(root, "server.js")}
	capture.Blockers = []string{"No verified restart authority exists."}
	recovered, err = RecoverHostWorkload(context.Background(), candidate, capture, analyzer, files.New([]string{root}), "")
	if err != nil || len(recovered.Adoption.Blockers) == 0 || recovered.Adoption.Runtime.Kind != "" {
		t.Fatal("bare process claims native rollback authority")
	}
}

func TestLocalDirectoryMaterializationKeepsDirtyFilesModesAndFencesChangedSource(t *testing.T) {
	root, analyzer, _, _ := hostRecoveryFixture(t)
	if err := os.WriteFile(filepath.Join(root, "untracked.js"), []byte("new source"), 0751); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "group-shared"), 0775); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "group-shared"), 0775); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.production"), []byte("TOKEN=private"), 0600); err != nil {
		t.Fatal(err)
	}
	source := DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalDirectory, LocalPath: root}
	detection, err := analyzer.Analyze(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	materialized, err := analyzer.Materialize(context.Background(), source, detection.Source, 1, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]os.FileMode{"untracked.js": 0751, "group-shared": 0775} {
		info, err := os.Stat(filepath.Join(materialized.Root, name))
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("source mode lost for %s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(materialized.Root, ".env.production")); !os.IsNotExist(err) {
		t.Fatal("private dotenv copied into managed source")
	}
	if err := os.WriteFile(filepath.Join(root, "server.js"), []byte("changed after review"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := analyzer.Materialize(context.Background(), source, detection.Source, 2, t.TempDir()); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("changed source accepted: %v", err)
	}
}

func TestLocalDirectoryRejectsEscapingSymlinksAndInvalidExclusions(t *testing.T) {
	root, analyzer, _, _ := hostRecoveryFixture(t)
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	if _, err := analyzer.Analyze(context.Background(), DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalDirectory, LocalPath: root}); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("source escape accepted: %v", err)
	}
	for _, excluded := range [][]string{{"../outside"}, {"."}, {"data", "data"}} {
		if err := (DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalDirectory, LocalPath: root, ExcludePaths: excluded}).Validate(); err == nil {
			t.Fatal("invalid source exclusions accepted")
		}
	}
}
