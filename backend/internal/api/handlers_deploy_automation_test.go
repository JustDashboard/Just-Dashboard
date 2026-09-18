package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

func TestDeploymentAutomationRoutesFenceProviderDeliveriesAndSecrets(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "automation-admin", auth.RoleAdmin)}
	reader := &client{t: t, h: routes, cookie: signInAs(t, s, "automation-reader", auth.RoleReadOnly)}
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	body := `{"name":"GitHub","kind":"github","provider":"github","enabled":true,"config":{"repository":"acme/app","ref":"main","events":["push"],"watchInclude":["services/api/**"]}}`
	if response := reader.do(http.MethodPost, base+"/triggers", body, nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader trigger create=%d %s", response.Code, response.Body.String())
	}
	created := admin.do(http.MethodPost, base+"/triggers", body, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("trigger create=%d %s", created.Code, created.Body.String())
	}
	var result deploy.TriggerCreated
	if err := json.Unmarshal(created.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Secret == "" || result.Trigger.HookID == "" {
		t.Fatal("one-time trigger credentials missing")
	}
	list := reader.do(http.MethodGet, base+"/triggers", "", nil)
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), result.Secret) {
		t.Fatalf("ordinary trigger read=%d %s", list.Code, list.Body.String())
	}

	payload := []byte(`{"ref":"refs/heads/main","after":"abc123","repository":{"full_name":"acme/app"},"commits":[{"modified":["services/api/main.go"]}]}`)
	headers := map[string]string{"X-GitHub-Event": "push", "X-GitHub-Delivery": "delivery-1", "X-Hub-Signature-256": signProviderPayload(payload, result.Secret)}
	hook := fmt.Sprintf("/api/v1/hooks/providers/github/%s", result.Trigger.HookID)
	bad := map[string]string{"X-GitHub-Event": "push", "X-GitHub-Delivery": "bad-signature", "X-Hub-Signature-256": "sha256=00"}
	if response := (&client{t: t, h: routes}).do(http.MethodPost, hook, string(payload), bad); response.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature=%d %s", response.Code, response.Body.String())
	}
	wrongRepo := []byte(`{"ref":"refs/heads/main","after":"abc123","repository":{"full_name":"other/app"},"commits":[{"modified":["services/api/main.go"]}]}`)
	wrongHeaders := map[string]string{"X-GitHub-Event": "push", "X-GitHub-Delivery": "wrong-repo", "X-Hub-Signature-256": signProviderPayload(wrongRepo, result.Secret)}
	if response := (&client{t: t, h: routes}).do(http.MethodPost, hook, string(wrongRepo), wrongHeaders); response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "wrong_repository") {
		t.Fatalf("wrong repository=%d %s", response.Code, response.Body.String())
	}
	ignored := []byte(`{"ref":"refs/heads/main","after":"abc123","repository":{"full_name":"acme/app"},"commits":[{"modified":["docs/readme.md"]}]}`)
	ignoredHeaders := map[string]string{"X-GitHub-Event": "push", "X-GitHub-Delivery": "ignored-path", "X-Hub-Signature-256": signProviderPayload(ignored, result.Secret)}
	if response := (&client{t: t, h: routes}).do(http.MethodPost, hook, string(ignored), ignoredHeaders); response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "watch_paths_ignored") {
		t.Fatalf("ignored paths=%d %s", response.Code, response.Body.String())
	}
	wrongRef := []byte(`{"ref":"refs/heads/release","after":"abc123","repository":{"full_name":"acme/app"},"commits":[{"modified":["services/api/main.go"]}]}`)
	wrongRefHeaders := map[string]string{"X-GitHub-Event": "push", "X-GitHub-Delivery": "wrong-ref", "X-Hub-Signature-256": signProviderPayload(wrongRef, result.Secret)}
	if response := (&client{t: t, h: routes}).do(http.MethodPost, hook, string(wrongRef), wrongRefHeaders); response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "wrong_ref") {
		t.Fatalf("wrong ref=%d %s", response.Code, response.Body.String())
	}
	wrongEventHeaders := map[string]string{"X-GitHub-Event": "issues", "X-GitHub-Delivery": "wrong-event", "X-Hub-Signature-256": signProviderPayload(payload, result.Secret)}
	if response := (&client{t: t, h: routes}).do(http.MethodPost, hook, string(payload), wrongEventHeaders); response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "wrong_event") {
		t.Fatalf("wrong event=%d %s", response.Code, response.Body.String())
	}
	oversized := strings.Repeat("x", deploy.MaxAutomationBody+1)
	if response := (&client{t: t, h: routes}).do(http.MethodPost, hook, oversized, map[string]string{"X-GitHub-Event": "push", "X-GitHub-Delivery": "oversized", "X-Hub-Signature-256": "sha256=00"}); response.Code != http.StatusRequestEntityTooLarge || !strings.Contains(response.Body.String(), "payload_too_large") {
		t.Fatalf("oversized=%d %s", response.Code, response.Body.String())
	}
	var before int
	_ = s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_runs WHERE project_id=?`, projectID).Scan(&before)
	if before != 0 {
		t.Fatalf("rejected providers enqueued %d runs", before)
	}
	accepted := (&client{t: t, h: routes}).do(http.MethodPost, hook, string(payload), headers)
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("accepted provider=%d %s", accepted.Code, accepted.Body.String())
	}
	replay := (&client{t: t, h: routes}).do(http.MethodPost, hook, string(payload), headers)
	if replay.Code != http.StatusConflict || !strings.Contains(replay.Body.String(), "delivery_replayed") {
		t.Fatalf("replay=%d %s", replay.Code, replay.Body.String())
	}
	var after int
	_ = s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_runs WHERE project_id=?`, projectID).Scan(&after)
	if after != 1 {
		t.Fatalf("accepted delivery created %d runs", after)
	}
	var audits string
	_ = s.Store.DB.QueryRow(`SELECT COALESCE(group_concat(detail),'') FROM audit_log WHERE action LIKE 'deploy.%'`).Scan(&audits)
	if strings.Contains(audits, result.Secret) {
		t.Fatal("automation audit leaked hook secret")
	}
}

// Two ordinary pushes with disjoint file lists and no watch-path filter must
// still supersede one another: the environment's automatic deployment policy
// has no filter configured here, so decision.ChangedPaths stays nil for both
// (the "unscoped" case) and neither push's run metadata should fall back to
// the provider payload's own per-push commit list — that per-push list is not
// a baseline diff, so two disjoint pushes would never cover one another and
// the first would survive queued forever behind the second. This engine
// never starts in this harness (no call to s.modules.deployEngine.Start), so
// both runs stay RunQueued exactly as they would behind a busy heavy slot in
// production, which is what makes supersession the code path under test.
func TestWebhookPushSupersessionIgnoresThePerPushFileList(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "supersede-admin", auth.RoleAdmin)}
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	body := `{"name":"GitHub","kind":"github","provider":"github","enabled":true,"config":{"repository":"acme/app","ref":"main","events":["push"]}}`
	created := admin.do(http.MethodPost, base+"/triggers", body, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("trigger create=%d %s", created.Code, created.Body.String())
	}
	var result deploy.TriggerCreated
	if err := json.Unmarshal(created.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	hook := fmt.Sprintf("/api/v1/hooks/providers/github/%s", result.Trigger.HookID)
	guest := &client{t: t, h: routes}

	pushA := []byte(`{"ref":"refs/heads/main","after":"` + strings.Repeat("a", 40) + `","repository":{"full_name":"acme/app"},"commits":[{"modified":["src/api.go"]}]}`)
	headersA := map[string]string{"X-GitHub-Event": "push", "X-GitHub-Delivery": "push-a", "X-Hub-Signature-256": signProviderPayload(pushA, result.Secret)}
	first := guest.do(http.MethodPost, hook, string(pushA), headersA)
	if first.Code != http.StatusAccepted {
		t.Fatalf("push A=%d %s", first.Code, first.Body.String())
	}
	var runA struct {
		RunID int64 `json:"runId"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &runA); err != nil {
		t.Fatal(err)
	}
	var stateA string
	if err := s.Store.DB.QueryRow(`SELECT state FROM deploy_runs WHERE id=?`, runA.RunID).Scan(&stateA); err != nil || stateA != string(deploy.RunQueued) {
		t.Fatalf("run A state = %q, error=%v, want queued", stateA, err)
	}

	pushB := []byte(`{"ref":"refs/heads/main","after":"` + strings.Repeat("b", 40) + `","repository":{"full_name":"acme/app"},"commits":[{"modified":["README.md"]}]}`)
	headersB := map[string]string{"X-GitHub-Event": "push", "X-GitHub-Delivery": "push-b", "X-Hub-Signature-256": signProviderPayload(pushB, result.Secret)}
	second := guest.do(http.MethodPost, hook, string(pushB), headersB)
	if second.Code != http.StatusAccepted {
		t.Fatalf("push B=%d %s", second.Code, second.Body.String())
	}
	var runB struct {
		RunID int64 `json:"runId"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &runB); err != nil {
		t.Fatal(err)
	}

	var stateAfter string
	var supersededBy int64
	if err := s.Store.DB.QueryRow(`SELECT state, superseded_by FROM deploy_runs WHERE id=?`, runA.RunID).Scan(&stateAfter, &supersededBy); err != nil {
		t.Fatal(err)
	}
	if stateAfter != string(deploy.RunSuperseded) || supersededBy != runB.RunID {
		t.Fatalf("run A after push B: state=%q supersededBy=%d, want superseded by %d", stateAfter, supersededBy, runB.RunID)
	}
}

func TestScopedGenericHookAndAPIIdempotencyCreateOneRun(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "generic-admin", auth.RoleAdmin)}
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	created := admin.do(http.MethodPost, base+"/triggers", `{"name":"CI","kind":"generic_hook","enabled":true,"config":{}}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", created.Code, created.Body.String())
	}
	var result deploy.TriggerCreated
	if err := json.Unmarshal(created.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"revision":"abc"}`)
	headers := map[string]string{"Idempotency-Key": "ci-44", "X-JD-Signature-256": signProviderPayload(payload, result.Secret)}
	path := fmt.Sprintf("/api/v1/deploy/%d/hooks/%d", projectID, result.Trigger.ID)
	guest := &client{t: t, h: routes}
	if response := guest.do(http.MethodPost, path, string(payload), headers); response.Code != http.StatusAccepted {
		t.Fatalf("generic=%d %s", response.Code, response.Body.String())
	}
	if response := guest.do(http.MethodPost, path, string(payload), headers); response.Code != http.StatusConflict {
		t.Fatalf("generic replay=%d %s", response.Code, response.Body.String())
	}
	runPath := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/runs", projectID, environmentID)
	apiHeaders := map[string]string{"Idempotency-Key": "client-44"}
	first := admin.do(http.MethodPost, runPath, `{"operation":"deploy"}`, apiHeaders)
	second := admin.do(http.MethodPost, runPath, `{"operation":"deploy"}`, apiHeaders)
	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("api retries=%d/%d %s %s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	var a, b deploy.EngineRun
	_ = json.Unmarshal(first.Body.Bytes(), &a)
	_ = json.Unmarshal(second.Body.Bytes(), &b)
	if a.ID == 0 || a.ID != b.ID {
		t.Fatalf("api idempotency ids=%d/%d", a.ID, b.ID)
	}
}

func TestScheduledChainRecordsRequiredUnavailableAction(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	item := deploy.ScheduleDispatch{Schedule: deploy.Schedule{ID: 71, ProjectID: projectID, EnvironmentID: environmentID, Name: "game maintenance", Steps: []deploy.ScheduleStep{{Action: "game_command", Config: json.RawMessage(`{"command":"save-all"}`), Required: true}, {Action: "deploy", Config: json.RawMessage(`{}`), Required: true}}}, DueAt: time.Date(2026, 9, 8, 3, 0, 0, 0, time.UTC)}
	if err := s.dispatchDeploymentSchedule(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	runs, err := s.modules.deployRuns.ProjectRuns(context.Background(), projectID, 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%+v err=%v", runs, err)
	}
	if runs[0].Operation != deploy.OperationScheduled || runs[0].Trigger != deploy.TriggerSchedule {
		t.Fatalf("marker=%+v", runs[0])
	}
	var metadata struct {
		ChainStatus string                                  `json:"chainStatus"`
		Chain       []struct{ Action, Status, Code string } `json:"chain"`
	}
	if err = json.Unmarshal(runs[0].Metadata, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.ChainStatus != "failed" || len(metadata.Chain) != 1 || metadata.Chain[0].Status != "unavailable" || metadata.Chain[0].Code != "game_console_unavailable" {
		t.Fatalf("chain metadata=%+v", metadata)
	}
}

func TestAutomationConfigurationRoutesRequireAdminSessionAndAuditNamesOnly(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "policy-admin", auth.RoleAdmin)}
	reader := &client{t: t, h: routes, cookie: signInAs(t, s, "policy-reader", auth.RoleReadOnly)}
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	scheduleBody := `{"name":"nightly","expression":"30 2 * * *","timezone":"America/New_York","enabled":true,"steps":[{"action":"backup","config":{"jobId":4,"timeoutSeconds":60},"required":true}]}`
	if response := reader.do(http.MethodPost, base+"/schedules", scheduleBody, nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader schedule=%d %s", response.Code, response.Body.String())
	}
	schedule := admin.do(http.MethodPost, base+"/schedules", scheduleBody, nil)
	if schedule.Code != http.StatusCreated || !strings.Contains(schedule.Body.String(), `"nextRunAt"`) {
		t.Fatalf("admin schedule=%d %s", schedule.Code, schedule.Body.String())
	}
	simulation := admin.do(http.MethodPost, base+"/watch-paths/simulate", `{"changedPaths":["services/api/main.go"],"include":["services/api/**"],"exclude":[]}`, nil)
	if simulation.Code != http.StatusOK || !strings.Contains(simulation.Body.String(), `"matched":true`) {
		t.Fatalf("simulation=%d %s", simulation.Code, simulation.Body.String())
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer remote.Close()
	channelSecret := "never-audit-this-channel-secret"
	notificationBody := fmt.Sprintf(`{"name":"ops","url":%q,"secret":%q,"events":["run.finished"],"enabled":true}`, remote.URL, channelSecret)
	created := admin.do(http.MethodPost, "/api/v1/deploy/notifications", notificationBody, nil)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), channelSecret) {
		t.Fatalf("notification create=%d %s", created.Code, created.Body.String())
	}
	var response struct {
		Channel deploy.NotificationChannel `json:"channel"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	listed := reader.do(http.MethodGet, "/api/v1/deploy/notifications", "", nil)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), channelSecret) {
		t.Fatalf("notification list=%d %s", listed.Code, listed.Body.String())
	}
	tested := admin.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/notifications/%d/test", response.Channel.ID), `{}`, nil)
	if tested.Code != http.StatusOK {
		t.Fatalf("notification test=%d %s", tested.Code, tested.Body.String())
	}
	var audits string
	_ = s.Store.DB.QueryRow(`SELECT COALESCE(group_concat(action || detail),'') FROM audit_log WHERE action LIKE 'deploy.%'`).Scan(&audits)
	for _, action := range []string{"deploy.schedule.create", "deploy.trigger.test", "deploy.notification.create", "deploy.notification.test"} {
		if !strings.Contains(audits, action) {
			t.Errorf("missing audit %s in %s", action, audits)
		}
	}
	if strings.Contains(audits, channelSecret) {
		t.Fatal("notification secret leaked to audit")
	}
}

func signProviderPayload(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
