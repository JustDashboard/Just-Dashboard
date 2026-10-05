package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	capture := &procs.HostWorkloadCapture{Manager: "pm2", ResourceID: candidate.ResourceID, Name: "api", Account: "alice", UID: 1000, GID: 1001, SourceDirectory: root, SourcePath: filepath.Join(root, "server.js"), InterpreterVersion: "24.12.0", ConfigurationDigest: "fresh-config", Environment: map[string]string{"PORT": "3000", "TOKEN": "private-production-value"}, EnvironmentNames: []string{"PORT", "TOKEN"}, Command: []string{"/usr/local/bin/node", filepath.Join(root, "server.js")}, Processes: []procs.HostProcessCapture{{ID: 7, PID: 123, CreateTime: time.Now().UnixMilli(), State: "online", LogSources: []string{"pm2:alice/7/api"}}}}
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
	if recovered.Source.Kind != SourceLocal || recovered.Source.Mode != SourceModeRecoveredSnapshot || recovered.Configuration.Build.Method != BuildRecipe || recovered.Configuration.Build.Recipe != "node" || recovered.Configuration.Build.NodeVersion != "24" {
		t.Fatalf("source/build not restored: %+v %+v", recovered.Source, recovered.Configuration.Build)
	}
	runtime := recovered.Configuration.Runtime
	if runtime.User != "1000:1001" || runtime.WorkingDirectory != "/app" || !runtime.HostNetwork || runtime.HostPort != 3000 || runtime.Strategy != StrategyStopFirst {
		t.Fatalf("runtime parity missing: %+v", runtime)
	}
	if recovered.Environment["TOKEN"] != "private-production-value" || recovered.Environment["JD_IMPORTED_ARG_0"] != "/usr/local/bin/node" || recovered.Environment["JD_IMPORTED_ARG_1"] != "/app/server.js" {
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
	var baseline NativeBaselineMetadata
	if err := json.Unmarshal(recovered.Adoption.Runtime.Metadata, &baseline); err != nil {
		t.Fatal(err)
	}
	before, err := localDirectoryDigest(context.Background(), root, baseline.SourceExclusions)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data", "production.sqlite"), []byte("database changes while app remains live"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := localDirectoryDigest(context.Background(), root, baseline.SourceExclusions)
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

func TestLocalDirectoryInspectionReadsPrivateReviewedSnapshot(t *testing.T) {
	root, analyzer, _, _ := hostRecoveryFixture(t)
	if err := os.Mkdir(filepath.Join(root, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	writePlanningFixture(t, filepath.Join(root, "data", "state.json"), `{"live":true}`)
	writePlanningFixture(t, filepath.Join(root, ".env"), "TOKEN=private")
	source := DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalDirectory, LocalPath: root, ExcludePaths: []string{"data/"}}
	detection, err := analyzer.Analyze(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	var inspectedRoot string
	err = analyzer.InspectRevision(t.Context(), source, detection.Source, func(snapshot string, identity SourceIdentity) error {
		inspectedRoot = snapshot
		if snapshot == root || identity.Digest != detection.Source.Digest {
			t.Fatal("inspection did not use the reviewed private source snapshot")
		}
		for _, excluded := range []string{".env", "data"} {
			if _, err := os.Stat(filepath.Join(snapshot, excluded)); !os.IsNotExist(err) {
				t.Fatal("inspection copied private environment or linked live data")
			}
		}
		return os.WriteFile(filepath.Join(snapshot, "server.js"), []byte("inspection-owned"), 0644)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(inspectedRoot); !os.IsNotExist(err) {
		t.Fatal("private inspection snapshot was not cleaned up")
	}
	unchanged, err := analyzer.Analyze(t.Context(), source)
	if err != nil || unchanged.Source.Digest != detection.Source.Digest {
		t.Fatal("inspection modified the original source")
	}
	if err := os.WriteFile(filepath.Join(root, "server.js"), []byte("changed-after-review"), 0644); err != nil {
		t.Fatal(err)
	}
	err = analyzer.InspectRevision(t.Context(), source, detection.Source, func(string, SourceIdentity) error {
		t.Fatal("changed source was passed to inspection")
		return nil
	})
	if !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("changed directory source inspection = %v", err)
	}
}

func TestLocalDirectoryExecutionRequiresReviewedContentDigest(t *testing.T) {
	plan := &StoredExecutionPlan{SourceKind: SourceLocal,
		SourceConfig:   DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalDirectory, LocalPath: "/srv/owned-source"},
		SourceIdentity: SourceIdentity{Kind: SourceLocal, LocalPath: "/srv/owned-source", Digest: fakeContentDigest("reviewed directory")}}
	if err := validateImmutableExecutionSource(plan); err != nil {
		t.Fatalf("reviewed directory source rejected: %v", err)
	}
	plan.SourceIdentity.Digest = ""
	if err := validateImmutableExecutionSource(plan); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("directory source without immutable digest accepted: %v", err)
	}
	plan.SourceIdentity.Digest = fakeContentDigest("reviewed directory")
	plan.SourceConfig.Mode = SourceModeLocalCheckout
	if err := validateImmutableExecutionSource(plan); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("Git checkout without immutable revision accepted: %v", err)
	}
}

func TestHostEnvironmentPathTranslationPreservesUrisAndRefusesEscapes(t *testing.T) {
	for _, test := range []struct {
		name, root, value, want string
		changed, supported      bool
	}{
		{"absolute data", "/srv/app", "/srv/app/data", "/app/data", true, true},
		{"source root", "/srv/app", "/srv/app", "/app", true, true},
		{"sqlite uri", "/srv/app", "sqlite:///srv/app/data/state.db?mode=rw", "sqlite:///app/data/state.db?mode=rw", true, true},
		{"file uri", "/srv/app", "file:/srv/app/data/state.db", "file:/app/data/state.db", true, true},
		{"encoded uri", "/srv/app name", "file:///srv/app%20name/data/state.db", "file:///app/data/state.db", true, true},
		{"relative data", "/srv/app", "data/state.db", "data/state.db", false, true},
		{"relative escape", "/srv/app", "../shared/state.db", "../shared/state.db", false, false},
		{"opaque file escape", "/srv/app", "file:../shared/state.db", "file:../shared/state.db", false, false},
		{"outside host", "/srv/app", "/srv/shared/state.db", "/srv/shared/state.db", false, false},
		{"normalized escape", "/srv/app", "/srv/app/../shared/state.db", "/srv/app/../shared/state.db", false, false},
		{"network credentials", "/srv/app", "postgres://user:private@127.0.0.1:5432/app", "postgres://user:private@127.0.0.1:5432/app", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, changed, supported := translateHostEnvironmentPath(test.value, test.root)
			if value != test.want || changed != test.changed || supported != test.supported {
				t.Fatalf("filesystem environment translation mismatch: changed=%t supported=%t", changed, supported)
			}
		})
	}
}

func TestHostRecoveryGuardsImplicitInterpreterAndUnknownFilesystemVariables(t *testing.T) {
	for _, test := range []struct {
		name, value, issue string
	}{
		{"NODE_EXTRA_CA_CERTS", "/host-only/corporate-ca.crt", "host_environment_path_unsupported"},
		{"SSL_CERT_FILE", "/host-only/corporate-ca.crt", "host_environment_path_unsupported"},
		{"NODE_PATH", "/usr/local/lib/node_modules", "host_environment_path_unsupported"},
		{"APP_LIBRARY_CONFIG", "/host-only/private.config", "host_environment_path_unsupported"},
		{"NODE_OPTIONS", "--require /host-only/register.js", "host_node_options_unsupported"},
		{"NODE_OPTIONS", "--experimental-loader=./loader.mjs", "host_node_options_unsupported"},
		{"LD_PRELOAD", "libcustom.so", "host_environment_path_unsupported"},
	} {
		t.Run(test.name+test.issue, func(t *testing.T) {
			root, analyzer, candidate, capture := hostRecoveryFixture(t)
			capture.Environment[test.name] = test.value
			recovered, err := RecoverHostWorkload(t.Context(), candidate, capture, analyzer, files.New([]string{root}), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, issue := range recovered.Adoption.Issues {
				found = found || issue.Code == test.issue
			}
			if !found || recovered.BaselineEnvironment[test.name] != test.value {
				t.Fatal("implicit original filesystem authority was silently lost or modified")
			}
			encoded := string(mustJSON(recovered))
			if strings.Contains(encoded, test.value) {
				t.Fatal("blocked private runtime value entered the public recovery result")
			}
		})
	}
}

func TestHostRecoveryPreservesStandardManagerMetadataWithReviewWarning(t *testing.T) {
	root, analyzer, candidate, capture := hostRecoveryFixture(t)
	capture.Environment["HOME"] = "/home/original-account"
	capture.Environment["NODE_OPTIONS"] = "--max-old-space-size=2048 --enable-source-maps"
	recovered, err := RecoverHostWorkload(t.Context(), candidate, capture, analyzer, files.New([]string{root}), t.TempDir())
	if err != nil || len(recovered.Adoption.Blockers) != 0 {
		t.Fatal("standard manager metadata or memory options were blocked")
	}
	found := false
	for _, warning := range recovered.Adoption.Warnings {
		found = found || strings.Contains(warning, "HOME retains an original host path")
	}
	if !found || recovered.Environment["HOME"] != "/home/original-account" || recovered.Environment["NODE_OPTIONS"] != capture.Environment["NODE_OPTIONS"] {
		t.Fatal("standard metadata was changed or its filesystem limitation hidden")
	}
}

func TestHostCommandKeepsContainedFlagPathsAndRefusesOutsideOperands(t *testing.T) {
	for _, test := range []struct {
		arg, want string
		blocked   bool
	}{
		{"--require=/srv/app/register.js", "--require=/app/register.js", false},
		{"--import=file:///srv/app/loader.mjs", "--import=file:///app/loader.mjs", false},
		{"--require=/outside/register.js", "", true},
		{"--require=../outside/register.js", "", true},
		{"--import=file:///tmp/loader.mjs", "", true},
		{"-r/outside/register.js", "", true},
		{"../outside/server.js", "", true},
		{"--eval=process.env.PRIVATE", "", true},
		{"--redirect=https://example.test/path", "--redirect=https://example.test/path", false},
	} {
		t.Run(test.arg, func(t *testing.T) {
			command, err := hostCommandForContainer([]string{"/usr/bin/node", test.arg, "/srv/app/server.js"}, "/srv/app", "node")
			if test.blocked {
				if !errors.Is(err, ErrRecoveryBlocked) {
					t.Fatal("outside or ambiguous interpreter operand was silently preserved")
				}
				return
			}
			if err != nil || len(command) != 3 || command[1] != test.want || command[2] != "/app/server.js" {
				t.Fatal("contained interpreter operand or network argument changed incorrectly")
			}
		})
	}
}

func TestHostRecoveryNeverCopiesRecognizablePrivateCredentialFiles(t *testing.T) {
	for _, name := range []string{".npmrc", ".netrc", ".pypirc", ".git-credentials", "tls.key", "tls.pem", "keystore.p12", "keystore.jks"} {
		t.Run(name, func(t *testing.T) {
			root, analyzer, candidate, capture := hostRecoveryFixture(t)
			writePlanningFixture(t, filepath.Join(root, name), "owned-private-fixture-value")
			recovered, err := RecoverHostWorkload(t.Context(), candidate, capture, analyzer, files.New([]string{root}), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, issue := range recovered.Adoption.Issues {
				found = found || issue.Code == "host_private_source_file"
			}
			if !found {
				t.Fatal("recognizable credential file was accepted into the migration")
			}
			if _, err := os.Lstat(filepath.Join(recovered.Adoption.RecoveryDirectory, name)); !os.IsNotExist(err) {
				t.Fatal("recognizable credential file entered a build snapshot")
			}
		})
	}
}

func TestHostRecoverySealsOriginalAndTranslatedDirectoryVariablesSeparately(t *testing.T) {
	root, analyzer, candidate, capture := hostRecoveryFixture(t)
	paths := files.New([]string{root})
	if err := os.Mkdir(filepath.Join(root, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	writePlanningFixture(t, filepath.Join(root, "server.js"), `const fs=require("fs");fs.readFileSync(process.env.APP_DATA_DIR+"/state.json");`)
	capture.Environment["APP_DATA_DIR"] = filepath.Join(root, "data")
	before := capture.Environment["APP_DATA_DIR"]
	recovered, err := RecoverHostWorkload(t.Context(), candidate, capture, analyzer, paths, t.TempDir())
	if err != nil || len(recovered.Adoption.Blockers) != 0 {
		t.Fatalf("source-contained path recovery failed: %v", err)
	}
	if recovered.Environment["APP_DATA_DIR"] != "/app/data" || recovered.BaselineEnvironment["APP_DATA_DIR"] != before || capture.Environment["APP_DATA_DIR"] != before {
		t.Fatal("source path translation changed the original environment or baseline")
	}
	capture.Environment["APP_DATA_DIR"] = "/unreviewed-host-storage/app"
	recovered, err = RecoverHostWorkload(t.Context(), candidate, capture, analyzer, paths, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range recovered.Adoption.Issues {
		found = found || issue.Code == "host_environment_path_unsupported"
	}
	if !found {
		t.Fatal("source-read host path outside the captured tree was silently migrated")
	}
}

func TestHostRecoveryBlocksStaticImageForARunningNodeServer(t *testing.T) {
	root, analyzer, candidate, capture := hostRecoveryFixture(t)
	writePlanningFixture(t, filepath.Join(root, "package.json"), `{"name":"existing-vite-server","scripts":{"dev":"vite","build":"vite build","start":"vite --host"},"dependencies":{"vite":"7.0.0","react":"19.0.0"}}`)
	recovered, err := RecoverHostWorkload(t.Context(), candidate, capture, analyzer, files.New([]string{root}), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range recovered.Adoption.Issues {
		found = found || issue.Code == "host_runtime_layout_unsupported"
	}
	if !found {
		t.Fatal("a running Node command was accepted for a static nginx recipe")
	}
}

func TestHostRecoveryRefusesClearlyNewerLoadedModuleWithUnchangedEntrypoint(t *testing.T) {
	root, analyzer, candidate, capture := hostRecoveryFixture(t)
	if err := os.Mkdir(filepath.Join(root, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(root, "data", "mutable.json")
	module := filepath.Join(root, "loaded-module.js")
	writePlanningFixture(t, data, `{"mutable":true}`)
	writePlanningFixture(t, module, "module.exports = 'changed since start'")
	newer := time.UnixMilli(capture.Processes[0].CreateTime).Add(10 * time.Second)
	if err := os.Chtimes(data, newer, newer); err != nil {
		t.Fatal(err)
	}
	if drift, err := knownHostSourceDrift(t.Context(), root, capture, []string{"data"}); err != nil || drift {
		t.Fatal("linked data writes were mistaken for loaded-code drift")
	}
	if err := os.Chtimes(module, newer, newer); err != nil {
		t.Fatal(err)
	}
	recovered, err := RecoverHostWorkload(t.Context(), candidate, capture, analyzer, files.New([]string{root}), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range recovered.Adoption.Issues {
		found = found || issue.Code == "host_running_source_changed"
	}
	if !found {
		t.Fatal("newer loaded module was accepted as the currently running source")
	}
}

func TestHostSourceDriftIncludesKnownPrivateRuntimeFiles(t *testing.T) {
	root, _, _, capture := hostRecoveryFixture(t)
	private := filepath.Join(root, ".env")
	writePlanningFixture(t, private, "private fixture")
	newer := time.UnixMilli(capture.Processes[0].CreateTime).Add(10 * time.Second)
	if err := os.Chtimes(private, newer, newer); err != nil {
		t.Fatal(err)
	}
	if drift, err := knownHostSourceDrift(t.Context(), root, capture, nil); err != nil || !drift {
		t.Fatal("known private startup configuration newer than the process was accepted")
	}
}

func TestNativeSourceIdentityRejectsADifferentDirectoryWithMatchingContents(t *testing.T) {
	original, shadow := t.TempDir(), t.TempDir()
	for _, directory := range []string{original, shadow} {
		writePlanningFixture(t, filepath.Join(directory, "server.js"), "same apparent source")
	}
	info, err := os.Stat(original)
	if err != nil {
		t.Fatal(err)
	}
	if !hostSourceDirectoryMatches(info, original) {
		t.Fatal("the same mapped source directory was refused")
	}
	if hostSourceDirectoryMatches(info, shadow) {
		t.Fatal("a dashboard-image source directory replaced the original host directory")
	}
	if hostSourceDirectoryMatches(info, filepath.Join(shadow, "missing")) {
		t.Fatal("an unavailable host source was accepted")
	}
}
