package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// POST .../approvals/{id}/reject moves a pending approval out of the active
// list and refuses a later approval of it; the trigger's next revision opens
// its own, independent pending approval.
func TestPreviewApprovalRejectRoute(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "reject-admin", auth.RoleAdmin)}
	reader := &client{t: t, h: routes, cookie: signInAs(t, s, "reject-reader", auth.RoleReadOnly)}
	guest := &client{t: t, h: routes}
	base := fmt.Sprintf("/api/v1/deploy/%d", projectID)
	created := admin.do(http.MethodPost, fmt.Sprintf("%s/environments/%d/triggers", base, environmentID),
		`{"name":"Previews","kind":"github","provider":"github","enabled":true,"config":{"repository":"acme/app","ref":"main","events":["pull_request"],"preview":true}}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create trigger = %d %s", created.Code, created.Body.String())
	}
	var trigger deploy.TriggerCreated
	if err := json.Unmarshal(created.Body.Bytes(), &trigger); err != nil {
		t.Fatal(err)
	}
	revision := strings.Repeat("a", 40)
	payload := fmt.Sprintf(`{"action":"opened","number":9,"repository":{"full_name":"acme/app"},"pull_request":{"user":{"login":"fork-author"},"head":{"sha":%q,"ref":"untrusted","repo":{"full_name":"fork/app"}}}}`, revision)
	hook := "/api/v1/hooks/providers/github/" + trigger.Trigger.HookID
	if response := guest.do(http.MethodPost, hook, payload, map[string]string{
		"X-GitHub-Event": "pull_request", "X-GitHub-Delivery": "opened",
		"X-Hub-Signature-256": signProviderPayload([]byte(payload), trigger.Secret),
	}); response.Code != http.StatusAccepted {
		t.Fatalf("delivery = %d %s", response.Code, response.Body.String())
	}
	listed := admin.do(http.MethodGet, base+"/previews/approvals", "", nil)
	var approvals []deploy.PreviewApproval
	if err := json.Unmarshal(listed.Body.Bytes(), &approvals); err != nil || len(approvals) != 1 {
		t.Fatalf("approvals = %s, %v", listed.Body.String(), err)
	}
	rejectPath := fmt.Sprintf("%s/previews/approvals/%d/reject", base, approvals[0].ID)
	request := fmt.Sprintf(`{"revision":%q}`, revision)

	if response := reader.do(http.MethodPost, rejectPath, request, nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader reject = %d", response.Code)
	}
	rejected := admin.do(http.MethodPost, rejectPath, request, nil)
	if rejected.Code != http.StatusOK {
		t.Fatalf("reject = %d %s", rejected.Code, rejected.Body.String())
	}
	var approval deploy.PreviewApproval
	if err := json.Unmarshal(rejected.Body.Bytes(), &approval); err != nil || approval.State != "rejected" {
		t.Fatalf("reject result = %#v, %v", approval, err)
	}

	stillPending := admin.do(http.MethodGet, base+"/previews/approvals", "", nil)
	var pending []deploy.PreviewApproval
	if err := json.Unmarshal(stillPending.Body.Bytes(), &pending); err != nil || len(pending) != 0 {
		t.Fatalf("pending approvals after reject = %#v, %v, want none", pending, err)
	}
	approvePath := fmt.Sprintf("%s/previews/approvals/%d/approve", base, approvals[0].ID)
	if response := admin.do(http.MethodPost, approvePath, request, nil); response.Code == http.StatusAccepted {
		t.Fatal("approved a rejected revision")
	}
	var audited int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='deploy.preview.reject' AND success=1`).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("reject audit count = %d, %v", audited, err)
	}
}

// GET .../triggers/{trigger}/deliveries and POST .../rotate-secret both sit
// behind system.admin+session, matching the trigger CRUD routes beside them.
func TestTriggerDeliveriesAndRotateSecretRoutes(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "trigger-route-admin", auth.RoleAdmin)}
	reader := &client{t: t, h: routes, cookie: signInAs(t, s, "trigger-route-reader", auth.RoleReadOnly)}
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	created := admin.do(http.MethodPost, base+"/triggers",
		`{"name":"CI","kind":"generic_hook","enabled":true,"config":{}}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create trigger = %d %s", created.Code, created.Body.String())
	}
	var trigger deploy.TriggerCreated
	if err := json.Unmarshal(created.Body.Bytes(), &trigger); err != nil {
		t.Fatal(err)
	}
	deliveriesPath := fmt.Sprintf("%s/triggers/%d/deliveries", base, trigger.Trigger.ID)
	rotatePath := fmt.Sprintf("%s/triggers/%d/rotate-secret", base, trigger.Trigger.ID)

	if response := reader.do(http.MethodGet, deliveriesPath, "", nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader deliveries = %d", response.Code)
	}
	empty := admin.do(http.MethodGet, deliveriesPath, "", nil)
	if empty.Code != http.StatusOK {
		t.Fatalf("deliveries = %d %s", empty.Code, empty.Body.String())
	}
	var deliveries []deploy.TriggerDelivery
	if err := json.Unmarshal(empty.Body.Bytes(), &deliveries); err != nil || len(deliveries) != 0 {
		t.Fatalf("deliveries body = %s, %v, want an empty list", empty.Body.String(), err)
	}

	if response := reader.do(http.MethodPost, rotatePath, `{}`, nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader rotate = %d", response.Code)
	}
	rotated := admin.do(http.MethodPost, rotatePath, `{}`, nil)
	if rotated.Code != http.StatusOK {
		t.Fatalf("rotate = %d %s", rotated.Code, rotated.Body.String())
	}
	var result struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(rotated.Body.Bytes(), &result); err != nil || result.Secret == "" || result.Secret == trigger.Secret {
		t.Fatalf("rotated secret = %q, %v, want a new non-empty value distinct from %q", result.Secret, err, trigger.Secret)
	}
	var audited int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='deploy.trigger.rotate_secret' AND success=1`).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("rotate audit count = %d, %v", audited, err)
	}

	unknownPath := fmt.Sprintf("%s/triggers/999999/deliveries", base)
	if response := admin.do(http.MethodGet, unknownPath, "", nil); response.Code != http.StatusNotFound {
		t.Fatalf("deliveries for an unknown trigger = %d %s", response.Code, response.Body.String())
	}
}

func TestPreviewWebhookRequiresSessionApprovalAndCloseRemainsRetryable(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "preview-admin", auth.RoleAdmin)}
	reader := &client{t: t, h: routes, cookie: signInAs(t, s, "preview-reader", auth.RoleReadOnly)}
	guest := &client{t: t, h: routes}
	base := fmt.Sprintf("/api/v1/deploy/%d", projectID)
	created := admin.do(http.MethodPost, fmt.Sprintf("%s/environments/%d/triggers", base, environmentID), `{"name":"Previews","kind":"github","provider":"github","enabled":true,"config":{"repository":"acme/app","ref":"main","events":["pull_request"],"preview":true}}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var trigger deploy.TriggerCreated
	if err := json.Unmarshal(created.Body.Bytes(), &trigger); err != nil {
		t.Fatal(err)
	}
	revision := strings.Repeat("a", 40)
	payload := fmt.Sprintf(`{"action":"opened","number":17,"repository":{"full_name":"acme/app"},"pull_request":{"user":{"login":"fork-author"},"head":{"sha":%q,"ref":"untrusted","repo":{"full_name":"fork/app"}}}}`, revision)
	hook := "/api/v1/hooks/providers/github/" + trigger.Trigger.HookID
	send := func(body, delivery string) {
		response := guest.do(http.MethodPost, hook, body, map[string]string{"X-GitHub-Event": "pull_request", "X-GitHub-Delivery": delivery, "X-Hub-Signature-256": signProviderPayload([]byte(body), trigger.Secret)})
		if response.Code != http.StatusAccepted {
			t.Fatalf("delivery: %d %s", response.Code, response.Body.String())
		}
	}
	send(payload, "opened")
	var runs, previews int
	_ = s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_runs`).Scan(&runs)
	_ = s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_environments WHERE kind='preview'`).Scan(&previews)
	if runs != 0 || previews != 0 {
		t.Fatalf("untrusted code reached execution: %d runs, %d previews", runs, previews)
	}
	listed := admin.do(http.MethodGet, base+"/previews/approvals", "", nil)
	var approvals []deploy.PreviewApproval
	if err := json.Unmarshal(listed.Body.Bytes(), &approvals); err != nil || len(approvals) != 1 {
		t.Fatalf("approvals: %s, %v", listed.Body.String(), err)
	}
	approvePath := fmt.Sprintf("%s/previews/approvals/%d/approve", base, approvals[0].ID)
	request := fmt.Sprintf(`{"revision":%q,"deploy":true}`, revision)
	if response := reader.do(http.MethodPost, approvePath, request, nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader approved code: %d", response.Code)
	}
	approved := admin.do(http.MethodPost, approvePath, request, nil)
	if approved.Code != http.StatusAccepted {
		t.Fatalf("approve: %d %s", approved.Code, approved.Body.String())
	}
	var result struct {
		Preview deploy.PreviewRef `json:"preview"`
		RunID   int64             `json:"runId"`
	}
	if err := json.Unmarshal(approved.Body.Bytes(), &result); err != nil || result.RunID == 0 {
		t.Fatalf("approve output: %s, %v", approved.Body.String(), err)
	}
	if response := admin.do(http.MethodPost, approvePath, request, nil); response.Code != http.StatusAccepted {
		t.Fatalf("approval retry: %d %s", response.Code, response.Body.String())
	}
	_ = s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_runs`).Scan(&runs)
	if runs != 1 {
		t.Fatalf("approval retry created %d runs", runs)
	}
	send(strings.Replace(payload, `"opened"`, `"closed"`, 1), "closed")
	var operation string
	if err := s.Store.DB.QueryRow(`SELECT operation FROM deploy_runs ORDER BY id DESC LIMIT 1`).Scan(&operation); err != nil || operation != "preview_remove" {
		t.Fatalf("close did not enqueue cleanup: %s, %v", operation, err)
	}
	var archived int
	if err := s.Store.DB.QueryRow(`SELECT archived_at FROM deploy_environments WHERE id=?`, result.Preview.EnvironmentID).Scan(&archived); err != nil || archived != 0 {
		t.Fatalf("preview archived before cleanup completed: %d, %v", archived, err)
	}
	var priorState string
	if err := s.Store.DB.QueryRow(`SELECT state FROM deploy_runs WHERE id=?`, result.RunID).Scan(&priorState); err != nil || priorState != "cancelled" {
		t.Fatalf("close left queued preview work runnable: %s %v", priorState, err)
	}
	send(strings.Replace(payload, `"opened"`, `"reopened"`, 1), "reopened")
	if response := admin.do(http.MethodPost, approvePath, request, nil); response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "preview_cleanup_pending") {
		t.Fatalf("reopened before cleanup: %d %s", response.Code, response.Body.String())
	}
	// This API fixture settles the queued cleanup separately; Docker cleanup has
	// its own live fixture. Reopening must not reuse the old approval run key.
	if _, err := s.Store.DB.Exec(`UPDATE deploy_runs SET state='succeeded',status='success',ended_at=1 WHERE environment_id=? AND operation='preview_remove'`, result.Preview.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	if err := s.modules.deployAutomation.ArchiveClosedPreview(t.Context(), result.Preview.ID); err != nil {
		t.Fatal(err)
	}
	reopened := admin.do(http.MethodPost, approvePath, request, nil)
	if reopened.Code != http.StatusAccepted {
		t.Fatalf("reopen approval: %d %s", reopened.Code, reopened.Body.String())
	}
	var reopenedResult struct {
		RunID int64 `json:"runId"`
	}
	if err := json.Unmarshal(reopened.Body.Bytes(), &reopenedResult); err != nil || reopenedResult.RunID == 0 || reopenedResult.RunID == result.RunID {
		t.Fatalf("reopened head reused an old run: %s %v", reopened.Body.String(), err)
	}
	if response := admin.do(http.MethodPost, approvePath, request, nil); response.Code != http.StatusAccepted {
		t.Fatalf("reopened approval retry: %d %s", response.Code, response.Body.String())
	}
	_ = s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_runs`).Scan(&runs)
	if runs != 3 {
		t.Fatalf("reopening did not create exactly one new deployment: %d runs", runs)
	}
	var generation int
	if err := s.Store.DB.QueryRow(`SELECT generation FROM deploy_preview_approvals WHERE id=?`, approvals[0].ID).Scan(&generation); err != nil || generation != 2 {
		t.Fatalf("approval generation=%d %v", generation, err)
	}
	if response := admin.do(http.MethodPost, fmt.Sprintf("%s/environments/%d/runs", base, environmentID), `{"operation":"preview_remove"}`, nil); response.Code == http.StatusAccepted {
		t.Fatal("ordinary run route admitted production cleanup")
	}
	var audit string
	_ = s.Store.DB.QueryRow(`SELECT COALESCE(group_concat(action),'') FROM audit_log WHERE action='deploy.preview.approve'`).Scan(&audit)
	if audit == "" {
		t.Fatal("preview approval was not audited")
	}
}
