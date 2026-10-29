package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// The canary routes: the trial is read by anyone who reads the deployment,
// an operator with the deploy control asks for its promotion or abort, the
// first decision is the one taken, and each is audited.
func TestCanaryRoutesReadDecideAndAudit(t *testing.T) {
	s := testServer(t)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "canary-admin", auth.RoleAdmin)}
	limited := &client{t: t, h: routes, cookie: signInAs(t, s, "canary-operator", auth.RoleLimited)}
	reader := &client{t: t, h: routes, cookie: signInAs(t, s, "canary-reader", auth.RoleReadOnly)}
	projectID, environmentID, first, second := warmWindowFixture(t, s, admin, "canary", false)
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/canary", projectID, environmentID)

	read := func() *deploy.CanaryWindow {
		t.Helper()
		res := reader.do(http.MethodGet, base, "", nil)
		var body struct {
			Canary *deploy.CanaryWindow `json:"canary"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || res.Code != http.StatusOK {
			t.Fatalf("canary = %d %s", res.Code, res.Body.String())
		}
		return body.Canary
	}
	if read() != nil {
		t.Fatal("an environment that never had a canary read one")
	}
	if res := limited.do(http.MethodPost, base+"/promote", "", nil); res.Code != http.StatusConflict || keyErrorCode(res) != "no_canary" {
		t.Fatalf("promote without a canary = %d %s", res.Code, res.Body.String())
	}

	now := time.Now().Unix()
	policy, _ := json.Marshal(deploy.DefaultRollbackPolicy())
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_canaries(project_id, environment_id, run_id, release_id, live_release_id,
		percent, duration_seconds, auto_promote, policy_json, state, started_at, ends_at, created_at, updated_at)
		VALUES(?, ?, 99, ?, ?, 10, 600, 1, ?, 'running', ?, ?, ?, ?)`,
		projectID, environmentID, second.Release.ID, first.Release.ID, string(policy), now, now+600, now, now); err != nil {
		t.Fatal(err)
	}
	window := read()
	if window == nil || window.Percent != 10 || window.ReleaseID != second.Release.ID || window.LiveReleaseNum != first.Release.Number {
		t.Fatalf("open canary = %+v", window)
	}
	if res := reader.do(http.MethodPost, base+"/abort", "", nil); res.Code != http.StatusForbidden {
		t.Fatalf("abort by a reader = %d %s", res.Code, res.Body.String())
	}
	res := limited.do(http.MethodPost, base+"/abort", "", nil)
	var decided deploy.CanaryWindow
	if err := json.Unmarshal(res.Body.Bytes(), &decided); err != nil || res.Code != http.StatusAccepted ||
		decided.Decision != deploy.CanaryAbort || decided.DecidedBy != "canary-operator" {
		t.Fatalf("abort = %d %s", res.Code, res.Body.String())
	}
	if res := admin.do(http.MethodPost, base+"/promote", "", nil); res.Code != http.StatusConflict || keyErrorCode(res) != "canary_decided" {
		t.Fatalf("a second decision = %d %s", res.Code, res.Body.String())
	}
	if rollbackAuditCount(t, s, "deploy.canary.abort") != 1 || rollbackAuditCount(t, s, "deploy.canary.promote") != 0 {
		t.Fatal("the decision was not audited once")
	}
}
