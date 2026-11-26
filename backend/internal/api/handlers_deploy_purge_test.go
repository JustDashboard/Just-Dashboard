package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
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
	confirm := map[string]string{"X-Confirm": "api-config-app"}
	if res := admin.do(http.MethodDelete, path, "", confirm); res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "deployment_not_archived") {
		t.Fatalf("unarchived: %d %s", res.Code, res.Body.String())
	}
	if _, err := s.modules.deployStore.Archive(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if res := admin.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d", id), "", nil); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "api-config-app") {
		t.Fatalf("archived detail: %d %s", res.Code, res.Body.String())
	}
	if res := admin.do(http.MethodDelete, path, "", nil); res.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing phrase: %d %s", res.Code, res.Body.String())
	}
	if res := admin.do(http.MethodDelete, path, "", map[string]string{"X-Confirm": "other-project"}); res.Code != http.StatusPreconditionFailed {
		t.Fatalf("wrong phrase: %d %s", res.Code, res.Body.String())
	}
	run, err := s.Store.DB.Exec(`INSERT INTO deploy_runs(project_id, environment_id, started_at, status, state) VALUES(?, ?, 1, 'running', 'verifying')`, id, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := run.LastInsertId()
	if res := admin.do(http.MethodDelete, path, "", confirm); res.Code != http.StatusConflict {
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
	if res := admin.do(http.MethodDelete, path, "", confirm); res.Code != http.StatusNoContent {
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

// Deleting a project kept its route, which went on answering the domain with
// nothing left to own it, and the next deployment of that domain was refused
// as a conflict. Asked to free its domains, the deletion removes the routes
// its environments wrote, and says which.
func TestDeploymentPermanentDeletionCanFreeItsDomains(t *testing.T) {
	c, s, root := vhostServer(t, func(string) string { return "exit 0" })
	s.modules.proxy = proxysvc.New(root, filepath.Join(root, "Caddyfile"))
	projectID, environmentID := seedDeployment(t, s, "jellyfin")
	route := deploy.RouteNameFor(environmentID)
	other := "shop.example.com"
	available := func(name string) string { return filepath.Join(root, "sites-available", name) }
	for _, name := range []string{route, other} {
		if err := os.WriteFile(available(name), []byte("server { server_name "+name+"; }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(available(name), filepath.Join(root, "sites-enabled", name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.modules.deployStore.Archive(t.Context(), projectID); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/deploy/%d/permanent?routes=remove", projectID)
	res := c.do(http.MethodDelete, path, "", map[string]string{"X-Confirm": "jellyfin"})
	if res.Code != http.StatusOK {
		t.Fatalf("purge: %d %s", res.Code, res.Body.String())
	}
	var out purgedRoutes
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Removed) != 1 || out.Removed[0] != route || len(out.Errors) != 0 {
		t.Fatalf("out = %+v", out)
	}
	for _, p := range []string{available(route), filepath.Join(root, "sites-enabled", route)} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s is still there: %v", p, err)
		}
	}
	if _, err := os.Stat(available(other)); err != nil {
		t.Errorf("another site went with it: %v", err)
	}
	entry := lastAudit(t, s, "deploy.project.purge")
	if !entry.Success || !strings.Contains(string(entry.Detail), route) {
		t.Errorf("audit = %+v", entry)
	}
}

// unreachableIngress is a Docker that does not answer the ingress lookup.
type unreachableIngress struct{}

func (unreachableIngress) InspectRunning(context.Context) ([]byte, error) {
	return nil, errors.New("Cannot connect to the Docker daemon")
}

func (unreachableIngress) InspectOne(context.Context, string) ([]byte, error) {
	return nil, errors.New("Cannot connect to the Docker daemon")
}

// A deletion asked to free its domains that cannot read which routes there
// are says so for each of the project's routes: answering that it removed
// none, with nothing failed, told the operator the domains were free while
// the ingress went on serving them.
func TestDeploymentPermanentDeletionSaysWhenItCouldNotReadItsRoutes(t *testing.T) {
	c, s, _ := vhostServer(t, func(string) string { return "exit 0" })
	s.modules.proxy = proxysvc.NewWithDockerIngress(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile")).
		WithContainerReader(unreachableIngress{})
	projectID, environmentID := seedDeployment(t, s, "jellyfin")
	route := deploy.RouteNameFor(environmentID)
	if _, err := s.modules.deployStore.Archive(t.Context(), projectID); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/deploy/%d/permanent?routes=remove", projectID)
	res := c.do(http.MethodDelete, path, "", map[string]string{"X-Confirm": "jellyfin"})
	if res.Code != http.StatusOK {
		t.Fatalf("purge: %d %s", res.Code, res.Body.String())
	}
	var out purgedRoutes
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Removed) != 0 || out.Errors[route] == "" {
		t.Fatalf("out = %+v, want %s among the routes not removed", out, route)
	}
	entry := lastAudit(t, s, "deploy.project.purge")
	if !strings.Contains(string(entry.Detail), "routeErrors") {
		t.Errorf("audit = %s", entry.Detail)
	}
}
