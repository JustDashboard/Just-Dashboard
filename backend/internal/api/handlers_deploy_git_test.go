package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

func TestAutomaticGitDeploymentsAreDefaultAuditedAndFenced(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	ctx := context.Background()
	source := deploy.DraftSourceConfig{Kind: deploy.SourceGit, Mode: deploy.SourceModeGitURL, URL: "https://github.com/acme/app.git", Ref: "main"}
	encoded, _ := json.Marshal(source)
	if _, err := s.Store.DB.Exec(`DELETE FROM deploy_sources WHERE environment_id=?`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_sources(environment_id,revision,kind,config_json,identity_json,digest,created_at)
		VALUES(?,1,'git',?,?,'fixture',1)`, environmentID, string(encoded), `{"revision":"`+strings.Repeat("a", 40)+`"}`); err != nil {
		t.Fatal(err)
	}
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "git-status-reader", auth.RoleReadOnly)}
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/git-watch", projectID, environmentID)
	response := reader.do(http.MethodGet, path, "", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"automatic":true`) || !strings.Contains(response.Body.String(), "awaiting_first_deployment") {
		t.Fatalf("status=%d %s", response.Code, response.Body.String())
	}
	target := deploy.GitWatchTarget{ProjectID: projectID, EnvironmentID: environmentID, PlanRevision: 1, Source: source}
	run, err := s.dispatchGitDeployment(ctx, target, strings.Repeat("b", 40), "automatic-first")
	if err != nil || run.SourceRevision != strings.Repeat("b", 40) || run.Trigger != deploy.TriggerGitPush {
		t.Fatalf("run=%+v, %v", run, err)
	}
	var audited int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='deploy.git.change' AND actor='system' AND success=1`).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("audit count=%d, %v", audited, err)
	}
	target.PlanRevision = 2
	if _, err := s.dispatchGitDeployment(ctx, target, strings.Repeat("c", 40), "stale-plan"); !errors.Is(err, deploy.ErrRevisionConflict) {
		t.Fatalf("stale source observation=%v", err)
	}
	target.PlanRevision = 1
	if _, err := s.modules.deployStore.Archive(ctx, projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.dispatchGitDeployment(ctx, target, strings.Repeat("c", 40), "archived-project"); !errors.Is(err, deploy.ErrEnvironmentNotFound) {
		t.Fatalf("archived project enqueue=%v", err)
	}
	var count int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_runs WHERE project_id=?`, projectID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("run count=%d, %v", count, err)
	}
}
