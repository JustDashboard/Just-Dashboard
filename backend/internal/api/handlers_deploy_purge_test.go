package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

func TestDeploymentPermanentDeletionRequiresArchiveCapabilityAndPreservesHostResources(t *testing.T) {
	s := testServer(t)
	id, environmentID, backupID := insertDeploymentConfigurationAPI(t, s)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "purge-admin", auth.RoleAdmin)}
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "purge-reader", auth.RoleReadOnly)}
	path := fmt.Sprintf("/api/v1/deploy/%d/permanent", id)
	if res := reader.do(http.MethodDelete, path, "", nil); res.Code != http.StatusForbidden {
		t.Fatalf("reader: %d %s", res.Code, res.Body.String())
	}
	if res := admin.do(http.MethodDelete, path, "", nil); res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "deployment_not_archived") {
		t.Fatalf("unarchived: %d %s", res.Code, res.Body.String())
	}
	if _, err := s.modules.deployStore.Archive(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if res := admin.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d", id), "", nil); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "api-config-app") {
		t.Fatalf("archived detail: %d %s", res.Code, res.Body.String())
	}
	run, err := s.Store.DB.Exec(`INSERT INTO deploy_runs(project_id, environment_id, started_at, status, state) VALUES(?, ?, 1, 'running', 'verifying')`, id, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := run.LastInsertId()
	if res := admin.do(http.MethodDelete, path, "", nil); res.Code != http.StatusConflict {
		t.Fatalf("active: %d %s", res.Code, res.Body.String())
	}
	if _, err := s.Store.DB.Exec(`UPDATE deploy_runs SET state = 'succeeded', status = 'success' WHERE id = ?`, runID); err != nil {
		t.Fatal(err)
	}
	variable, err := s.Store.DB.Exec(`INSERT INTO deploy_variable_revisions(environment_id, key, revision, value_enc, created_at) VALUES(?, 'TOKEN', 1, 'sealed', 1)`, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	variableID, _ := variable.LastInsertId()
	if _, err = s.Store.DB.Exec(`INSERT INTO deploy_run_variable_revisions(run_id, variable_revision_id, ordinal) VALUES(?, ?, 0)`, runID, variableID); err != nil {
		t.Fatal(err)
	}

	if res := admin.do(http.MethodGet, "/api/v1/deploy/?view=archived", "", nil); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "api-config-app") {
		t.Fatalf("archive: %d %s", res.Code, res.Body.String())
	}
	if res := admin.do(http.MethodDelete, path, "", nil); res.Code != http.StatusNoContent {
		t.Fatalf("purge: %d %s", res.Code, res.Body.String())
	}
	for _, table := range []string{"deploy_projects", "deploy_environments", "deploy_runs", "deploy_dependencies", "deploy_variable_revisions", "deploy_run_variable_revisions"} {
		var count int
		if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s: count=%d err=%v", table, count, err)
		}
	}
	var count int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM backup_jobs WHERE id = ?`, backupID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("backup was removed: %d %v", count, err)
	}
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'deploy.project.purge'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit: %d %v", count, err)
	}
	if res := admin.do(http.MethodDelete, path, "", nil); res.Code != http.StatusNotFound {
		t.Fatalf("missing: %d %s", res.Code, res.Body.String())
	}
}
