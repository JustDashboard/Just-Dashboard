package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

func TestDatabaseNetworkReferencesAndReplacementIdentity(t *testing.T) {
	ids, err := databaseURLConnectionIDs(map[string]string{"POSTGRES": "postgres://app:secret@db-7.jd.internal:5432/app", "REDIS": "redis://db-3.jd.internal:6379/0", "DUPLICATE": "postgres://db-7.jd.internal/other", "EXTERNAL": "postgres://db.example.test/app", "LITERAL": "not a URL"})
	if err != nil || len(ids) != 2 || ids[0] != 3 || ids[1] != 7 {
		t.Fatalf("references=%v, %v", ids, err)
	}
	if _, err := databaseURLConnectionIDs(map[string]string{"URL": "postgres://db-01.jd.internal/app"}); err == nil {
		t.Fatal("noncanonical database identity accepted")
	}
	binding := deploymentDatabaseBinding{Name: "app-db", ContainerID: "original"}
	if !databaseBindingMatches(binding, &dockerx.ContainerDetail{Container: dockerx.Container{ID: "replacement", Name: "/app-db"}}) {
		t.Fatal("same logical container replacement refused")
	}
	if databaseBindingMatches(binding, &dockerx.ContainerDetail{Container: dockerx.Container{ID: "different", Name: "another-db"}}) {
		t.Fatal("different container accepted from matching port alone")
	}
	binding.ComposeProject, binding.ComposeService = "project", "database"
	if !databaseBindingMatches(binding, &dockerx.ContainerDetail{Container: dockerx.Container{ID: "replacement", Name: "project-database-2", ComposeStack: "project", ComposeSvc: "database"}}) {
		t.Fatal("same Compose service replacement refused")
	}
	if databaseBindingMatches(binding, &dockerx.ContainerDetail{Container: dockerx.Container{ID: "replacement", Name: "app-db", ComposeStack: "other", ComposeSvc: "database"}}) {
		t.Fatal("another Compose project accepted")
	}
}

func TestLinkedDatabaseDeletionRefusesBeforeChangingData(t *testing.T) {
	s, router := dbTestRouter(t)
	_, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	path := filepath.Join(s.Cfg.FileRoots[0], "never-opened.db")
	if err := os.WriteFile(path, []byte("fixture-data-must-survive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_database_networks(environment_id,network_name,owner_key,created_at) VALUES(?,'linked-database-fixture','owner',1)`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_database_bindings(environment_id,connection_id,container_name) VALUES(?,1,'fixture-db')`, environmentID); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/databases/1", "/databases/1/database"} {
		request := httptest.NewRequest(http.MethodDelete, route, strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Confirm", "never-opened.db")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "database_linked") {
			t.Fatalf("linked database deletion: %d %s", response.Code, response.Body.String())
		}
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "fixture-data-must-survive" {
		t.Fatalf("database data changed: %q, %v", content, err)
	}
	var count int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_connections WHERE id=1`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("linked connection removed: %d %v", count, err)
	}
	if _, err := s.Store.DB.Exec(`DELETE FROM deploy_database_bindings WHERE connection_id=1`); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodDelete, "/databases/1", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("unlinked connection was not removed: %d %s", response.Code, response.Body.String())
	}
}

func TestDatabaseNetworkStatusIsScopedAndContainsNoCredentials(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	sealed, err := s.Sealer.Seal("postgres://app:private-database-password@127.0.0.1:5432/app")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name,driver,dsn_enc,created_at) VALUES('database-network-fixture','postgres',?,1)`, sealed)
	if err != nil {
		t.Fatal(err)
	}
	connectionID, _ := res.LastInsertId()
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_database_networks(environment_id,network_name,network_id,owner_key,created_at) VALUES(?,'fixture-network','network-id','private-owner-token',1)`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_database_bindings(environment_id,connection_id,container_name,status,checked_at) VALUES(?,?,'app-db','connected',?)`, environmentID, connectionID, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "database-network-reader", auth.RoleReadOnly)}
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/database-links", projectID, environmentID)
	got := reader.do(http.MethodGet, path, "", nil)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "db-") || strings.Contains(got.Body.String(), "private-") {
		t.Fatalf("network status=%d %s", got.Code, got.Body.String())
	}
	wrong := reader.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/database-links", projectID+1, environmentID), "", nil)
	if wrong.Code != http.StatusNotFound {
		t.Fatalf("cross-project status=%d", wrong.Code)
	}
	if _, err := s.Store.DB.Exec(`UPDATE deploy_database_bindings SET checked_at=1`); err != nil {
		t.Fatal(err)
	}
	stale := reader.do(http.MethodGet, path, "", nil)
	if !strings.Contains(stale.Body.String(), `"status":"stale"`) {
		t.Fatal("stale connection was shown as connected")
	}
	plan, err := s.modules.deployPlanning.RemovalPlan(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, target := range plan.Targets {
		if target.Kind == "deployment_database_network" {
			found = true
			if target.ResourceID != "network-id" || target.Data || target.ConfirmationType != "ordinary" {
				t.Fatalf("invalid network removal target=%+v", target)
			}
		}
	}
	if !found {
		t.Fatal("managed network missing from removal plan")
	}
	encoded, _ := json.Marshal(plan)
	if strings.Contains(string(encoded), "private-") {
		t.Fatal("removal plan exposed private data")
	}
	if _, err := s.modules.deployDatabases.NetworksForRuntime(context.Background(), environmentID, deploy.RuntimePlanConfig{HostNetwork: true}, map[string]string{"DATABASE_URL": "postgres://db-1.jd.internal/app"}); err == nil {
		t.Fatal("host network workload accepted a Docker DNS connection")
	}
}
