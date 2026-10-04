package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
)

func TestLivePM2AdoptionMigratesWithManagedFeaturesAndRestoresBaseline(t *testing.T) {
	if os.Getenv("JD_PM2_ADOPTION_LIVE") != "1" {
		t.Skip("set JD_PM2_ADOPTION_LIVE=1 for an isolated PM2-to-Docker deployment lifecycle")
	}
	testLiveNativeAdoption(t, "pm2")
}

func TestLiveSystemdAdoptionMigratesWithManagedFeaturesAndRestoresBaseline(t *testing.T) {
	if os.Getenv("JD_SYSTEMD_ADOPTION_LIVE") != "1" {
		t.Skip("set JD_SYSTEMD_ADOPTION_LIVE=1 for an isolated systemd-to-Docker deployment lifecycle")
	}
	testLiveNativeAdoption(t, "systemd")
}

func testLiveNativeAdoption(t *testing.T, kind string) {
	client := liveC4Docker(t)
	account, err := user.Current()
	if err != nil {
		t.Fatal("fixture account unavailable")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	data := filepath.Join(source, "data")
	if err := os.MkdirAll(data, 0755); err != nil {
		t.Fatal(err)
	}
	writeBuildFixture(t, source, "package.json", `{"name":"jd-native-adoption-proof","version":"1.0.0","scripts":{"start":"node server.js"}}`)
	writeBuildFixture(t, source, "server.js", `const fs=require("fs"),path=require("path"),http=require("http");http.createServer((req,res)=>{res.setHeader("Content-Type","application/json");res.end(JSON.stringify({marker:"native-adoption-owned",token:process.env.PROOF_TOKEN,empty:process.env.PROOF_EMPTY,uid:process.getuid(),cwd:process.cwd(),data:JSON.parse(fs.readFileSync(path.join(process.env.APP_DATA_DIR,"state.json"),"utf8"))}));console.log("JD_NATIVE_ADOPTION_HTTP");}).listen(Number(process.env.PORT),"127.0.0.1",()=>console.log("JD_NATIVE_ADOPTION_READY"));`)
	if err := os.WriteFile(filepath.Join(data, "state.json"), []byte(`{"value":42}`), 0644); err != nil {
		t.Fatal(err)
	}
	port := liveC5LoopbackPort(t)
	native := setupLiveNativeManager(t, kind, root, source, port, account)
	assertLiveNativeResponse(t, port, source, account.Uid)
	capture, err := native.capture(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	originalPID := capture.Processes[0].PID
	candidate := WorkloadCandidate{Key: kind + ":" + capture.ResourceID, Kind: kind, ResourceID: capture.ResourceID, Name: capture.Name, Running: 1, Total: 1, Digest: "owned-live-fixture", Services: []WorkloadService{{Name: capture.Name, PID: originalPID, CreatedAt: capture.Processes[0].CreateTime, Ports: []dockerx.PortMapping{{HostIP: "127.0.0.1", HostPort: port, ContainerPort: port, Protocol: "tcp"}}}}}
	analyzer := NewHostSourceAnalyzer([]string{root}, nil, filepath.Join(root, "source-cache"), client, nil)
	recovered, err := RecoverHostWorkload(t.Context(), candidate, capture, analyzer, files.New([]string{root}), filepath.Join(root, "recovery-cache"))
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.Adoption.Blockers) > 0 {
		t.Fatalf("standard %s Node recovery blocked: %v", kind, recovered.Adoption.Blockers)
	}
	if recovered.Environment["APP_DATA_DIR"] != "/app/data" || recovered.BaselineEnvironment["APP_DATA_DIR"] != data {
		t.Fatal("absolute data-directory variable did not preserve independent original and container values")
	}
	fixture := newPlanningStoreFixture(t)
	fixture.plans = NewPlanningStore(fixture.store, fixture.sealer, []string{root})
	seed := time.Now().UnixNano()%1_000_000_000 + 1_000_000_000
	foreign, err := client.ListContainersWithLabels(t.Context(), map[string]string{"io.just-dashboard.environment-id": strconv.FormatInt(seed+1, 10)})
	if err != nil || len(foreign) > 0 {
		t.Fatal("the fixture could not reserve an unused runtime namespace")
	}
	for _, table := range []string{"deploy_projects", "deploy_environments", "deploy_runs", "deploy_releases"} {
		if _, err := fixture.store.DB.Exec(`INSERT INTO sqlite_sequence(name,seq) VALUES(?,?)`, table, seed); err != nil {
			t.Fatal(err)
		}
	}
	runs := NewOrchestrationStore(fixture.store)
	dockerOwner := NewDockerRuntimeOwner(client)
	owner := native.owner(dockerOwner)
	owner.WithRecordedRuntimeObserver(NewRecordedRuntimeObserver(runs, dockerOwner, client, owner))
	observer := NewNativePreflightObserver(NewHostPreflightObserver([]string{root}, root, client), owner, runs)
	draft, err := fixture.plans.CreateRecoveredDraft(t.Context(), 41, "operator", DraftIntentConfig{Name: "owned-" + kind + "-managed", Profile: ProfileService}, recovered)
	if err != nil {
		t.Fatal(err)
	}
	preflight, err := PreflightDraft(t.Context(), draft, observer, true)
	if err != nil {
		t.Fatal(err)
	}
	ack := []string{}
	for _, finding := range preflight.Findings {
		if finding.Severity == PreflightBlocked || finding.Severity == PreflightDecision {
			t.Fatalf("live native preflight: %+v", finding)
		}
		if finding.Severity == PreflightWarning {
			ack = append(ack, finding.Code)
		}
	}
	draft, err = fixture.plans.SavePreflight(t.Context(), draft.ID, 41, true, draft.Revision, preflight)
	if err != nil {
		t.Fatal(err)
	}
	adopted, err := fixture.plans.Commit(t.Context(), draft.ID, 41, true, DraftCommitRequest{Revision: draft.Revision, AcknowledgedWarnings: ack})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := runs.LiveRelease(t.Context(), adopted.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	registered, err := native.capture(t.Context())
	if err != nil || registered.Processes[0].PID != originalPID {
		t.Fatal("adoption restarted the original workload")
	}
	assertLiveNativeResponse(t, port, source, account.Uid)
	observation := ObserveRuntimeServices(t.Context(), owner, adopted.EnvironmentID, baseline.Release.ID)
	if observation.Status != "available" || len(observation.Services) != 1 || observation.Services[0].Manager != kind || observation.Services[0].LogSource == "" {
		t.Fatalf("native runtime/logs unavailable: %+v", observation)
	}
	native.assertLogs(t.Context())
	settings, err := fixture.plans.EnvironmentConfiguration(t.Context(), adopted.ProjectID, adopted.EnvironmentID)
	if err != nil || settings.Source == nil || settings.Source.Mode != SourceModeLocalDirectory || settings.Build.Method != native.buildMethod {
		t.Fatal("adoption did not create a regular source/build/settings plan")
	}
	if strings.Contains(string(mustJSON(settings)), "owned-native-private-value") {
		t.Fatal("settings exposed a captured private variable")
	}
	executor := NewNormalizedStepExecutor(runs, fixture.plans, analyzer, NewArtifactBuilder(NewDockerArtifactBackend(client)), owner, NewCheckRunner(client), nil, filepath.Join(root, "workspaces")).WithPreflightObserver(observer)
	engine := NewEngine(runs, executor, nil, EngineConfig{WorkerID: "owned-native-proof", PollEvery: 20 * time.Millisecond, LeaseTTL: time.Minute}, nil)
	engineCtx, cancelEngine := context.WithCancel(context.Background())
	if err := engine.Start(engineCtx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rows, err := fixture.store.DB.Query(`SELECT runtime_id FROM deploy_release_runtimes WHERE kind='container'`)
		if err != nil {
			t.Error("owned runtime cleanup metadata unavailable")
			return
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err == nil {
				_ = client.RemoveContainer(ctx, id, true, false)
			}
		}
		_ = rows.Close()
		artifacts, err := fixture.store.DB.Query(`SELECT DISTINCT reference FROM deploy_release_artifacts WHERE kind='image'`)
		if err != nil {
			t.Error("owned image cleanup metadata unavailable")
			return
		}
		defer artifacts.Close()
		for artifacts.Next() {
			var reference string
			if err := artifacts.Scan(&reference); err == nil && strings.HasPrefix(reference, "just-dashboard/deployment-"+strconv.FormatInt(adopted.EnvironmentID, 10)+":run-") {
				_, _ = client.RemoveImage(ctx, reference, false, false)
			}
		}
	})
	t.Cleanup(func() { cancelEngine(); shutdownEngine(t, engine) })
	operation := func(op Operation, revision int, target *ReleaseWithArtifacts) EngineRun {
		t.Helper()
		request := RunRequest{ProjectID: adopted.ProjectID, EnvironmentID: adopted.EnvironmentID, Operation: op, Trigger: TriggerManual, Actor: "operator", RequestDigest: fmt.Sprintf("owned-proof-%s-%d", op, time.Now().UnixNano()), PlanRevision: revision, SlotClass: SlotHeavy}
		if target != nil {
			request.Metadata = mustJSON(map[string]any{"targetReleaseId": target.Release.ID})
			request.VariableSnapshotRunID = target.Release.RunID
			request.Steps = []StepKey{StepRenderRuntime, StepBackupGate, StepProvisionCertificate, StepStartCandidate, StepVerifyReadiness, StepVerifySmoke, StepActivate, StepRetirePrevious, StepRecordRelease, StepNotify}
		}
		run, _, err := runs.Enqueue(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		engine.Notify()
		deadline := time.Now().Add(10 * time.Minute)
		for time.Now().Before(deadline) {
			current, err := runs.Run(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.State.Terminal() {
				return *current
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("owned native deployment did not finish")
		return EngineRun{}
	}
	// The image builds successfully, then this candidate exits before readiness.
	// This proves production compensation restores a native manager, rather than
	// merely proving a preflight failure left an untouched app running.
	bad := settings.Runtime
	bad.Command = []string{"/bin/sh", "-c", "exit 42"}
	settings, err = fixture.plans.SaveEnvironmentConfiguration(t.Context(), adopted.ProjectID, adopted.EnvironmentID, ConfigurationWriteRequest{Revision: settings.Revision, Build: settings.Build, Runtime: bad, Checks: settings.Checks, Dependencies: settings.Dependencies, Domains: settings.Domains})
	if err != nil {
		t.Fatal(err)
	}
	failed := operation(OperationDeploy, settings.Revision, nil)
	if failed.State == RunSucceeded {
		t.Fatal("non-serving candidate was activated")
	}
	assertLiveNativeResponse(t, port, source, account.Uid)
	stillLive, err := runs.LiveRelease(t.Context(), adopted.EnvironmentID)
	if err != nil || stillLive.Release.ID != baseline.Release.ID {
		t.Fatal("failed candidate replaced the recorded native baseline")
	}
	steps, _ := runs.Steps(t.Context(), failed.ID)
	built, attempted := false, false
	for _, step := range steps {
		if step.Key == StepBuildArtifact && (step.State == StepPassed || step.State == StepWarning) {
			built = true
		}
		if step.Key == StepStartCandidate && (step.State == StepPassed || step.State == StepWarning) {
			attempted = true
		}
	}
	if !built || !attempted {
		t.Fatalf("failed run did not reach real cutover compensation: state=%s code=%s reason=%s", failed.State, failed.TerminalCode, failed.TerminalReason)
	}
	settings, err = fixture.plans.SaveEnvironmentConfiguration(t.Context(), adopted.ProjectID, adopted.EnvironmentID, ConfigurationWriteRequest{Revision: settings.Revision, Build: settings.Build, Runtime: recovered.Configuration.Runtime, Checks: settings.Checks, Dependencies: settings.Dependencies, Domains: settings.Domains})
	if err != nil {
		t.Fatal(err)
	}
	deployed := operation(OperationDeploy, settings.Revision, nil)
	if deployed.State != RunSucceeded {
		steps, _ := runs.Steps(t.Context(), deployed.ID)
		for _, step := range steps {
			if step.State == StepFailed || step.Key == StepStartCandidate {
				t.Logf("owned fixture step %s: %s", step.Key, string(step.Evidence))
			}
		}
		events, _ := runs.EventsAfter(t.Context(), deployed.ID, 0, 1000)
		for _, event := range events {
			if event.Type == EventStepLog && strings.Contains(string(event.Data), `"stream":"stderr"`) {
				t.Logf("owned fixture stderr: %s", string(event.Data))
			}
		}
		t.Fatalf("managed migration failed: state=%s code=%s reason=%s", deployed.State, deployed.TerminalCode, deployed.TerminalReason)
	}
	assertLiveNativeResponse(t, port, "/app", account.Uid)
	live, err := runs.LiveRelease(t.Context(), adopted.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := runs.RuntimeForRelease(t.Context(), live.Release.ID)
	if err != nil || runtime.Kind != "container" {
		t.Fatal("migration did not create a regular Docker release")
	}
	detail, err := client.Inspect(t.Context(), runtime.RuntimeID)
	if err != nil || detail.User != account.Uid+":"+account.Gid || detail.WorkingDir != "/app" {
		t.Fatal("managed runtime did not retain the account and translated working directory")
	}
	logs, closer, err := client.Logs(t.Context(), runtime.RuntimeID, dockerx.LogOptions{Tail: "50"})
	if err != nil {
		t.Fatal(err)
	}
	logged := false
	for line := range logs {
		logged = logged || strings.Contains(line.Text, "JD_NATIVE_ADOPTION_READY")
	}
	_ = closer.Close()
	if !logged {
		t.Fatal("normal deployment Docker logs did not include the running app")
	}
	stopped, err := native.capture(t.Context())
	if err != nil || (stopped.Processes[0].State != "stopped" && stopped.Processes[0].State != "inactive") {
		t.Fatal("original native manager was not stopped at successful cutover")
	}
	rolled := operation(OperationRollback, baseline.Release.PlanRevision, baseline)
	if rolled.State != RunSucceeded {
		t.Fatalf("native baseline rollback failed: state=%s code=%s reason=%s", rolled.State, rolled.TerminalCode, rolled.TerminalReason)
	}
	assertLiveNativeResponse(t, port, source, account.Uid)
	afterRollback, err := runs.LiveRelease(t.Context(), adopted.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	afterRuntime, err := runs.RuntimeForRelease(t.Context(), afterRollback.Release.ID)
	if err != nil || afterRuntime.Kind != kind {
		t.Fatal("rollback did not activate the retained original manager")
	}
	result := map[string]any{"manager": kind, "adoptionRestartedOriginal": false, "managedBuild": native.buildMethod, "capturedEnvironmentPrivate": true, "emptyEnvironmentPreserved": true, "runtimeUIDPreserved": true, "workingDirectoryTranslated": "/app", "absoluteDataDirectoryTranslated": true, "originalEnvironmentSnapshotPreserved": true, "persistentDataRetained": true, "nativeLogsAvailable": true, "dockerLogsAvailable": true, "builtCandidateFailureRestoredNative": true, "normalDockerMigrationSucceeded": true, "originalManagerRollbackSucceeded": true}
	if directory := os.Getenv("JD_" + strings.ToUpper(kind) + "_ADOPTION_EVIDENCE_DIR"); directory != "" {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, kind+"-managed-adoption.json"), mustJSON(result), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Log(string(mustJSON(result)))
}

type ownedLiveNativeManager struct {
	capture     func(context.Context) (*procs.HostWorkloadCapture, error)
	owner       func(RuntimeOwner) *NativeRuntimeOwner
	assertLogs  func(context.Context)
	buildMethod BuildMethod
}

func setupLiveNativeManager(t *testing.T, kind, root, source string, port int, account *user.User) ownedLiveNativeManager {
	t.Helper()
	if kind == "pm2" {
		binary, err := exec.LookPath("pm2")
		if err != nil {
			t.Fatal("the current account must have PM2 on PATH")
		}
		daemon := filepath.Join(root, ".pm2")
		environment := []string{"HOME=" + account.HomeDir, "PM2_HOME=" + daemon, "PATH=" + filepath.Dir(binary) + ":/usr/local/bin:/usr/bin:/bin", "PORT=" + strconv.Itoa(port), "APP_DATA_DIR=" + filepath.Join(source, "data"), "PROOF_TOKEN=owned-native-private-value", "PROOF_EMPTY="}
		command := func(ctx context.Context, args ...string) error {
			process := exec.CommandContext(ctx, binary, args...)
			process.Dir, process.Env = source, environment
			_, err := process.Output()
			return err
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := command(ctx, "kill"); err != nil {
				t.Error("owned PM2 daemon cleanup failed")
			}
		})
		if err := command(t.Context(), "start", filepath.Join(source, "server.js"), "--name", "jd-owned-native-adoption", "--cwd", source); err != nil {
			t.Fatal("owned PM2 fixture failed to start")
		}
		manager, err := procs.NewPM2ForExistingDaemon(account.Username, daemon)
		if err != nil {
			t.Fatal(err)
		}
		return ownedLiveNativeManager{buildMethod: BuildRecipe,
			capture: func(ctx context.Context) (*procs.HostWorkloadCapture, error) {
				return manager.CaptureExisting(ctx, account.Username, "default", "jd-owned-native-adoption")
			},
			owner: func(docker RuntimeOwner) *NativeRuntimeOwner { return NewNativeRuntimeOwner(docker, manager, nil) },
			assertLogs: func(ctx context.Context) {
				path, _, err := manager.LogPathsTarget(ctx, "jd-owned-native-adoption", account.Username, 0)
				if err != nil || !strings.HasPrefix(path, daemon+string(filepath.Separator)) {
					t.Fatal("PM2 did not expose its owned fixture log path")
				}
				contents, err := os.ReadFile(path)
				if err != nil || !strings.Contains(string(contents), "JD_NATIVE_ADOPTION_READY") {
					t.Fatal("native PM2 logs did not include the running app")
				}
			}}
	}
	if kind != "systemd" {
		t.Fatal("unknown owned fixture manager")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("the current account must have Node on PATH")
	}
	if err := exec.CommandContext(t.Context(), "sudo", "-n", "true").Run(); err != nil {
		t.Fatal("the opt-in systemd fixture requires root or passwordless sudo")
	}
	unit := fmt.Sprintf("jd-owned-native-adoption-%d.service", time.Now().UnixNano())
	destination := filepath.Join("/run/systemd/system", unit)
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatal("the fixture unit path is already occupied")
	}
	configuration := "[Unit]\nDescription=Just Dashboard isolated adoption fixture\n[Service]\nType=exec\nUser=" + account.Username + "\nGroup=" + account.Gid + "\nWorkingDirectory=" + source + "\nExecStart=" + node + " " + filepath.Join(source, "server.js") + "\nRestart=on-failure\nRestartSec=100ms\nKillSignal=SIGTERM\nTimeoutStopSec=3s\nEnvironment=\"PORT=" + strconv.Itoa(port) + "\" \"APP_DATA_DIR=" + filepath.Join(source, "data") + "\" \"PROOF_TOKEN=owned-native-private-value\" \"PROOF_EMPTY=\"\n"
	privateUnit := filepath.Join(root, unit)
	if err := os.WriteFile(privateUnit, []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	if err := exec.CommandContext(t.Context(), "sudo", "-n", "install", "-m", "0644", "--", privateUnit, destination).Run(); err != nil {
		t.Fatal("the owned systemd fixture unit could not be installed")
	}
	manager := &ownedLiveSystemd{Systemd: procs.NewSystemd(), unit: unit}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := manager.Control(ctx, unit, procs.UnitStop); err != nil {
			t.Error("owned systemd fixture cleanup could not stop its unit")
		}
		_ = exec.CommandContext(ctx, "sudo", "-n", "systemctl", "reset-failed", "--", unit).Run()
		if err := exec.CommandContext(ctx, "sudo", "-n", "rm", "--", destination).Run(); err != nil {
			t.Error("owned systemd fixture cleanup could not remove its unit file")
		}
	})
	// A newly named unit is loaded on start. Reloading the manager here would
	// also reload unrelated operator unit files, which the fixture must avoid.
	if _, err := manager.Control(t.Context(), unit, procs.UnitStart); err != nil {
		t.Fatal("the host could not load the new owned unit without a global manager reload")
	}
	writeBuildFixture(t, source, "Dockerfile", "FROM node:24-alpine\nWORKDIR /app\nCOPY . .\nENTRYPOINT []\n")
	return ownedLiveNativeManager{buildMethod: BuildDockerfile,
		capture: func(ctx context.Context) (*procs.HostWorkloadCapture, error) {
			return manager.CaptureExisting(ctx, unit)
		},
		owner: func(docker RuntimeOwner) *NativeRuntimeOwner {
			owner := NewNativeRuntimeOwner(docker, nil, nil)
			owner.systemd = manager
			return owner
		},
		assertLogs: func(ctx context.Context) {
			contents, err := exec.CommandContext(ctx, "sudo", "-n", "journalctl", "--unit", unit, "--no-pager", "-n", "50", "--output=cat").Output()
			if err != nil || !strings.Contains(string(contents), "JD_NATIVE_ADOPTION_READY") {
				t.Fatal("native systemd journal did not include the running app")
			}
		}}
}

type ownedLiveSystemd struct {
	*procs.Systemd
	unit string
}

func (s *ownedLiveSystemd) Control(ctx context.Context, unit string, action procs.UnitAction) (*procs.CommandResult, error) {
	if unit != s.unit || (action != procs.UnitStart && action != procs.UnitStop) {
		return nil, fmt.Errorf("fixture refused control of an unowned unit")
	}
	output, err := exec.CommandContext(ctx, "sudo", "-n", "systemctl", string(action), "--", unit).Output()
	return &procs.CommandResult{Stdout: string(output)}, err
}

func assertLiveNativeResponse(t *testing.T, port int, cwd, uid string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		response, err := (&http.Client{Timeout: time.Second}).Get("http://127.0.0.1:" + strconv.Itoa(port))
		if err == nil {
			var body struct {
				Marker string `json:"marker"`
				Token  string `json:"token"`
				Empty  string `json:"empty"`
				UID    int    `json:"uid"`
				CWD    string `json:"cwd"`
				Data   struct {
					Value int `json:"value"`
				} `json:"data"`
			}
			err = json.NewDecoder(response.Body).Decode(&body)
			_ = response.Body.Close()
			if err == nil && body.Marker == "native-adoption-owned" && body.Token == "owned-native-private-value" && body.Empty == "" && strconv.Itoa(body.UID) == uid && body.CWD == cwd && body.Data.Value == 42 {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("owned native app did not preserve its serving behavior, private environment, account, working directory, and data")
}
