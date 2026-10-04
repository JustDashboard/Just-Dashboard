package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
)

// Use the catalogue's real application pin, rather than a server that merely
// has an n8n-shaped port. All state and every Docker resource belong to this test.
func TestLiveManagedN8NAdoptionAndRollback(t *testing.T) {
	if os.Getenv("JD_N8N_ADOPTION_LIVE") != "1" {
		t.Skip("set JD_N8N_ADOPTION_LIVE=1 for real n8n adoption and persistent SQLite data")
	}
	const image = "n8nio/n8n:2.39.10"
	const encryptionKey = "owned-n8n-adoption-fixture-private-encryption-key-2026"
	const credentialValue = "owned-n8n-adoption-private-credential-value"
	const marker = "persistent-n8n-workflow-proof"
	ctx := t.Context()
	client := liveC4Docker(t)
	imageDetail, err := client.InspectImage(ctx, image)
	if err != nil {
		t.Fatalf("pull the pinned fixture image first: docker pull %s", image)
	}
	root := t.TempDir()
	name := fmt.Sprintf("jd-n8n-adoption-proof-%d", time.Now().UnixNano())
	network, volume := name+"-network", name+"-data"
	port := liveC5LoopbackPort(t)
	evidence := map[string]any{"test": t.Name(), "fixtureName": name, "image": image, "imageDigest": imageDetail.ID,
		"readinessPath": "/healthz/readiness", "dataStore": "SQLite in original /home/node/.n8n volume"}
	docker := func(args ...string) []byte {
		t.Helper()
		output, err := liveDockerOutput(ctx, args...)
		if err != nil {
			message := strings.ReplaceAll(strings.ReplaceAll(err.Error(), encryptionKey, "[redacted]"), credentialValue, "[redacted]")
			t.Fatalf("owned n8n Docker fixture operation failed: %s", message)
		}
		return output
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		containers, _ := client.ListContainersWithLabels(cleanupCtx, map[string]string{"fixture.just-dashboard.adoption": name})
		for _, item := range containers {
			if item.Labels["fixture.just-dashboard.adoption"] == name {
				_, _ = liveDockerOutput(cleanupCtx, "rm", "--force", item.ID)
			}
		}
		_, _ = liveDockerOutput(cleanupCtx, "rm", "--force", name)
		_, _ = liveDockerOutput(cleanupCtx, "volume", "rm", volume)
		_, _ = liveDockerOutput(cleanupCtx, "network", "rm", network)
		remainingContainers, containerErr := client.ListContainersWithLabels(cleanupCtx, map[string]string{"fixture.just-dashboard.adoption": name})
		remainingVolumes, volumeErr := liveDockerOutput(cleanupCtx, "volume", "ls", "--filter", "name="+volume, "--format", "{{.Name}}")
		remainingNetworks, networkErr := liveDockerOutput(cleanupCtx, "network", "ls", "--filter", "name="+network, "--format", "{{.Name}}")
		cleaned := containerErr == nil && volumeErr == nil && networkErr == nil && len(remainingContainers) == 0 && strings.TrimSpace(string(remainingVolumes)) == "" && strings.TrimSpace(string(remainingNetworks)) == ""
		if !cleaned {
			t.Error("owned n8n fixture cleanup could not be confirmed")
		}
		evidence["fixtureCleanupConfirmed"] = cleaned
		evidence["checkedAt"] = time.Now().UTC()
		evidence["testPassed"] = !t.Failed()
		if location := os.Getenv("JD_ADOPTION_EVIDENCE_DIR"); location != "" {
			if err := os.MkdirAll(location, 0o700); err != nil {
				t.Error(err)
				return
			}
			if err := os.WriteFile(filepath.Join(location, name+".json"), append(mustJSON(evidence), '\n'), 0o600); err != nil {
				t.Error(err)
			}
		}
		t.Logf("sanitized real n8n lifecycle evidence: %s", mustJSON(evidence))
	})
	docker("network", "create", network)
	docker("volume", "create", volume)
	id := strings.TrimSpace(string(docker("run", "--detach", "--name", name, "--network", network,
		"--network-alias", name+"-alias", "--publish", "127.0.0.1:"+strconv.Itoa(port)+":5678",
		"--volume", volume+":/home/node/.n8n", "--memory", "1073741824", "--cpus", "1",
		"--stop-timeout", "20", "--label", "fixture.just-dashboard.adoption="+name,
		"--env", "N8N_ENCRYPTION_KEY="+encryptionKey, "--env", "N8N_SECURE_COOKIE=false",
		"--env", "N8N_DIAGNOSTICS_ENABLED=false", "--env", "N8N_VERSION_NOTIFICATIONS_ENABLED=false",
		"--env", "N8N_TEMPLATES_ENABLED=false", "--env", "GENERIC_TIMEZONE=UTC", image)))
	t.Log("Started real pinned n8n; waiting for its migrated SQLite database")
	n8nAdoptionReady(t, port)
	identityArgs := []string{"inspect", "--format", "{{.Id}} {{.State.Pid}} {{.State.StartedAt}}", id}
	originalProcess := strings.TrimSpace(string(docker(identityArgs...)))
	jar, _ := cookiejar.New(nil)
	httpClient := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	request := func(method, path string, body any) map[string]any {
		t.Helper()
		var input io.Reader
		if body != nil {
			input = bytes.NewReader(mustJSON(body))
		}
		req, err := http.NewRequestWithContext(ctx, method, "http://127.0.0.1:"+strconv.Itoa(port)+path, input)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := httpClient.Do(req)
		if err != nil {
			t.Fatalf("owned n8n request %s failed: %v", path, err)
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			t.Fatalf("owned n8n request %s returned HTTP %d", path, response.StatusCode)
		}
		var result map[string]any
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatalf("owned n8n response %s was not JSON: %v", path, err)
		}
		if data, ok := result["data"].(map[string]any); ok {
			return data
		}
		return result
	}
	request(http.MethodPost, "/rest/owner/setup", map[string]any{
		"email": "owned-n8n-fixture@example.invalid", "firstName": "Owned", "lastName": "Fixture",
		"password": "OwnedN8nFixturePassword123!",
	})
	workflow := request(http.MethodPost, "/rest/workflows", map[string]any{
		"name": name + "-workflow", "settings": map[string]any{"executionOrder": "v1"},
		"nodes": []any{
			map[string]any{"id": "trigger", "name": "Manual trigger", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1, "position": []int{0, 0}, "parameters": map[string]any{}},
			map[string]any{"id": "data", "name": "Preserved data", "type": "n8n-nodes-base.set", "typeVersion": 3.4, "position": []int{220, 0}, "parameters": map[string]any{"assignments": map[string]any{"assignments": []any{map[string]any{"id": "marker", "name": "marker", "type": "string", "value": marker}}}, "options": map[string]any{}}},
		},
		"connections": map[string]any{"Manual trigger": map[string]any{"main": []any{[]any{map[string]any{"node": "Preserved data", "type": "main", "index": 0}}}}},
	})
	workflowID, _ := workflow["id"].(string)
	if workflowID == "" {
		t.Fatal("n8n did not persist a workflow ID")
	}
	credential := request(http.MethodPost, "/rest/credentials", map[string]any{
		"name": name + "-credential", "type": "httpHeaderAuth",
		"data": map[string]any{"name": "X-Owned-Fixture", "value": credentialValue},
	})
	credentialID, _ := credential["id"].(string)
	if credentialID == "" {
		t.Fatal("n8n did not persist a credential ID")
	}
	evidence["workflowId"], evidence["credentialId"] = workflowID, credentialID
	assertData := func(containerID string) {
		t.Helper()
		n8nAdoptionReady(t, port)
		saved := request(http.MethodGet, "/rest/workflows/"+workflowID, nil)
		if !strings.Contains(string(mustJSON(saved["nodes"])), marker) {
			t.Fatal("n8n lost the workflow's stored data")
		}
		// Ask n8n to decrypt its own credential store. A file and comparison
		// inside the owned data volume avoid printing private values in evidence.
		docker("exec", containerID, "n8n", "export:credentials", "--all", "--decrypted", "--output=/home/node/.n8n/adoption-credentials-proof.json")
		raw := docker("exec", containerID, "node", "-e", "process.stdout.write(require('fs').readFileSync('/home/node/.n8n/adoption-credentials-proof.json','utf8'))")
		var entries []struct {
			ID   string `json:"id"`
			Data struct {
				Value string `json:"value"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &entries); err != nil {
			t.Fatalf("n8n's credential export was invalid: %v", err)
		}
		found := false
		for _, entry := range entries {
			if entry.ID == credentialID && entry.Data.Value == credentialValue {
				found = true
			}
		}
		if !found {
			t.Fatal("n8n could not decrypt the original credential with its preserved encryption key")
		}
		size := strings.TrimSpace(string(docker("exec", containerID, "node", "-e", "process.stdout.write(String(require('fs').statSync('/home/node/.n8n/database.sqlite').size))")))
		if bytes, err := strconv.ParseInt(size, 10, 64); err != nil || bytes <= 0 {
			t.Fatal("n8n's persisted SQLite database is missing")
		}
	}
	assertData(id)
	evidence["originalWorkflowDataAndDecryptedCredentialVerified"] = true
	if strings.TrimSpace(string(docker(identityArgs...))) != originalProcess {
		t.Fatal("the n8n process changed before its read-only recovery capture")
	}
	original, err := client.CaptureAdoptionContainer(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	evidence["originalWritableLayerChanges"] = original.Changes
	evidence["originalWritableLayerStatCount"] = len(original.ChangeModes)
	evidence["originalProcess"] = map[string]any{"containerId": id, "pid": original.Inspection.State.Pid, "startedAt": original.Inspection.State.StartedAt}
	t.Logf("real n8n writable-layer capture: %d changes with %d verified modes", len(original.Changes), len(original.ChangeModes))
	candidate := WorkloadCandidate{Key: "container:" + id, Kind: "container", Name: name, ResourceID: id, Running: 1, Total: 1,
		Services: []WorkloadService{{Name: name, ResourceID: id, State: "running", Image: image, Ports: []dockerx.PortMapping{{HostIP: "127.0.0.1", HostPort: port, ContainerPort: 5678, Protocol: "tcp"}}}}}
	candidate.Digest = WorkloadDigest(candidate)
	recovered, err := RecoverDockerWorkload(ctx, candidate, client, files.New([]string{root}), filepath.Join(root, "baseline-cache"))
	if strings.TrimSpace(string(docker(identityArgs...))) != originalProcess {
		t.Fatal("read-only n8n recovery changed the original process or start time")
	}
	if err != nil {
		var issues []AdoptionIssue
		if recovered != nil && recovered.Adoption != nil {
			issues = recovered.Adoption.Issues
		}
		evidence["recoveryBlocked"], evidence["recoveryIssues"] = true, issues
		t.Fatalf("real n8n recovery blocked; keep fidelity guards intact: %v; sanitized issues=%s", err, mustJSON(issues))
	}
	if strings.Contains(string(mustJSON(recovered)), encryptionKey) || strings.Contains(string(mustJSON(recovered)), credentialValue) {
		t.Fatal("private n8n inputs leaked into recovery JSON")
	}
	fixture := newPlanningStoreFixture(t)
	if _, err := fixture.store.DB.Exec(`INSERT INTO sqlite_sequence(name,seq) VALUES('deploy_environments',?)`, time.Now().UnixMicro()); err != nil {
		t.Fatal(err)
	}
	draft, err := fixture.plans.CreateRecoveredDraft(ctx, 41, "operator", DraftIntentConfig{Name: name, Profile: ProfileCompose}, recovered)
	if err != nil {
		t.Fatal(err)
	}
	var data, originalEnc, desiredEnc string
	if err := fixture.store.DB.QueryRow(`SELECT data_json,adoption_enc,environment_enc FROM deploy_drafts WHERE id=?`, draft.ID).Scan(&data, &originalEnc, &desiredEnc); err != nil {
		t.Fatal(err)
	}
	if originalEnc == "" || desiredEnc == "" || strings.Contains(string(mustJSON(draft))+data+originalEnc+desiredEnc, encryptionKey) {
		t.Fatal("the original and desired n8n encryption key were not independently sealed")
	}
	evidence["originalAndDesiredEnvironmentIndependentlySealed"] = true
	configuration := *draft.Data.Configuration
	configuration.Runtime.InternalPort = 5678
	configuration.Checks = []PlannedCheck{{Name: "n8n SQLite readiness", Kind: "http", Phase: "readiness", Required: true,
		Config: json.RawMessage(`{"path":"/healthz/readiness","attempts":60,"timeoutSeconds":3,"intervalSeconds":1}`)}}
	draft, err = fixture.plans.Save(ctx, draft.ID, 41, false, DraftSaveRequest{Revision: draft.Revision, Step: DraftConfiguration, Configuration: &configuration})
	if err != nil {
		t.Fatal(err)
	}
	draft = checkRecoveredDraft(t, fixture, draft)
	ack := []string{}
	for _, finding := range draft.Findings {
		if finding.Severity == PreflightWarning {
			ack = append(ack, finding.Code)
		}
	}
	result, err := fixture.plans.Commit(ctx, draft.ID, 41, false, DraftCommitRequest{Revision: draft.Revision, AcknowledgedWarnings: ack})
	if err != nil {
		t.Fatal(err)
	}
	after, err := client.CaptureAdoptionContainer(ctx, id)
	if err != nil || after.Inspection.ID != original.Inspection.ID || after.Inspection.State.Pid != original.Inspection.State.Pid || after.Inspection.State.StartedAt != original.Inspection.State.StartedAt || !bytes.Equal(mustJSON(after.Inspection.Config), mustJSON(original.Inspection.Config)) || !bytes.Equal(mustJSON(after.Inspection.HostConfig), mustJSON(original.Inspection.HostConfig)) {
		t.Fatal("adoption changed the live n8n container, process, start time or settings")
	}
	assertData(id)
	runs := NewOrchestrationStore(fixture.store)
	baseline, err := runs.LiveRelease(ctx, result.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	owner := NewDockerRuntimeOwner(client)
	sources := NewHostSourceAnalyzer([]string{root}, []string{root}, filepath.Join(root, "source-cache"), client, fixture.plans)
	executor := NewNormalizedStepExecutor(runs, fixture.plans, sources, NewArtifactBuilder(NewDockerArtifactBackend(client)), owner, NewCheckRunner(client), nil, filepath.Join(root, "runtime-cache"))
	executor.WithPreflightObserver(livePreflightWithReservations(&preflightObserverFake{observation: HostObservation{Facilities: map[string]FacilityObservation{"docker": {Available: true}, "compose": {Available: true}}}}, fixture.store, client, nil))
	engine := NewEngine(runs, executor, nil, EngineConfig{WorkerID: "n8n-adoption-live", PollEvery: 50 * time.Millisecond, LeaseTTL: 2 * time.Minute}, nil)
	if err := engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = engine.Shutdown(shutdownCtx)
	})
	enqueue := func(operation Operation, revision int, target *ReleaseWithArtifacts) *EngineRun {
		t.Helper()
		request := RunRequest{ProjectID: result.ProjectID, EnvironmentID: result.EnvironmentID, Operation: operation, Trigger: TriggerManual,
			Actor: "operator", PlanRevision: revision, SlotClass: SlotHeavy, RequestDigest: fmt.Sprintf("n8n-%s-%d", operation, time.Now().UnixNano())}
		if target != nil {
			request.Metadata = mustJSON(map[string]any{"targetReleaseId": target.Release.ID})
			request.VariableSnapshotRunID = target.Release.RunID
		}
		run, _, err := runs.Enqueue(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		engine.Notify()
		return adoptionLiveWaitRun(t, runs, run.ID)
	}
	assertCurrent := func() {
		t.Helper()
		live, err := runs.LiveRelease(ctx, result.EnvironmentID)
		if err != nil {
			t.Fatal(err)
		}
		runtime, err := runs.RuntimeForRelease(ctx, live.Release.ID)
		if err != nil {
			t.Fatal(err)
		}
		ids := runtimeContainerIDs(*runtime)
		if len(ids) != 1 {
			t.Fatal("n8n managed runtime did not retain exactly one application service")
		}
		capture, err := client.CaptureAdoptionContainer(ctx, ids[0])
		if err != nil {
			t.Fatal(err)
		}
		if capture.Inspection.Image != original.Inspection.Image || capture.Inspection.Config.User != original.Inspection.Config.User || capture.Inspection.HostConfig.Memory != original.Inspection.HostConfig.Memory || capture.Inspection.HostConfig.NanoCPUs != original.Inspection.HostConfig.NanoCPUs {
			t.Fatal("n8n lost its image, runtime user or resource limits")
		}
		if !strings.Contains(strings.Join(capture.Inspection.Config.Env, "\n"), "N8N_ENCRYPTION_KEY="+encryptionKey) {
			t.Fatal("n8n lost its private encryption key")
		}
		preserved := false
		for _, mount := range capture.Inspection.Mounts {
			if mount.Name == volume && mount.Destination == "/home/node/.n8n" && mount.RW {
				preserved = true
			}
		}
		if !preserved {
			t.Fatal("n8n lost its original persistent data volume")
		}
		assertData(ids[0])
	}
	t.Log("Adoption kept the real n8n process and persisted data unchanged; deploying its recovered recipe")
	deployed := enqueue(OperationDeploy, 2, nil)
	if deployed.State != RunSucceeded {
		t.Fatalf("normal n8n Deploy failed: state=%s code=%s", deployed.State, deployed.TerminalCode)
	}
	assertCurrent()
	t.Log("Normal n8n Deploy preserved SQLite workflows and decrypted credentials; restoring the frozen baseline")
	rolled := enqueue(OperationRollback, 1, baseline)
	if rolled.State != RunSucceeded {
		t.Fatalf("n8n baseline rollback failed: state=%s code=%s", rolled.State, rolled.TerminalCode)
	}
	assertCurrent()
	evidence["adoptionPreservedIDsPIDsStartedAtAndSettings"] = true
	evidence["managedDeployRun"], evidence["managedDeployState"] = deployed.ID, deployed.State
	evidence["baselineRollbackRun"], evidence["baselineRollbackState"] = rolled.ID, rolled.State
	evidence["workflowDataPreserved"], evidence["originalEncryptedCredentialDecryptedAfterDeployAndRollback"] = true, true
	evidence["privateInputsAbsentFromRecoveryJSON"], evidence["originalNamedVolumePreserved"] = true, true
	evidence["imageUserAndResourceLimitsPreserved"] = true
}

func n8nAdoptionReady(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(120 * time.Second)
	client := &http.Client{Timeout: 3 * time.Second}
	for time.Now().Before(deadline) {
		response, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/healthz/readiness")
		if err == nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("the real n8n database did not become ready")
}
