package api

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

func TestPreviewDatabaseReferencesRefuseProductionBeforeBuildSecretsOpen(t *testing.T) {
	s := testServer(t)
	projectID, productionID, _ := insertDeploymentConfigurationAPI(t, s)
	result, err := s.Store.DB.Exec(`INSERT INTO deploy_environments(project_id,name,slug,kind,created_at,updated_at) VALUES(?,'Preview','preview-database','preview',1,1)`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	previewID, _ := result.LastInsertId()
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_runtime_plans(environment_id,revision,config_json,preview,digest,created_at) VALUES(?,1,'{}','','fixture',1)`, previewID); err != nil {
		t.Fatal(err)
	}
	addConnection := func(name, dsn string) int64 {
		t.Helper()
		sealed, err := s.Sealer.Seal(dsn)
		if err != nil {
			t.Fatal(err)
		}
		result, err := s.Store.DB.Exec(`INSERT INTO db_connections(name,driver,dsn_enc,created_at) VALUES(?,'postgres',?,1)`, name, sealed)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := result.LastInsertId()
		return id
	}
	productionConn := addConnection("production-db", "postgres://app:production-secret@db.example.test/app")
	aliasConn := addConnection("alias-db", "postgres://other:alias-secret@DB.EXAMPLE.TEST.:5432/another")
	previewConn := addConnection("preview-db", "postgres://preview:preview-secret@preview.example.test/app")
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_dependencies(environment_id,release_id,kind,ownership,resource_kind,resource_id,config_json,created_at) VALUES(?,0,'database','linked','database_connection',?,'{}',1)`, productionID, strconv.FormatInt(productionConn, 10)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{productionConn, aliasConn} {
		value, err := s.modules.deployDatabases.ResolveVariable(t.Context(), previewID, 1, strconv.FormatInt(id, 10))
		if !errors.Is(err, deploy.ErrPreviewIsolation) || value != "" || strings.Contains(err.Error(), "secret") {
			t.Fatalf("production reference escaped: value=%q error=%v", value, err)
		}
	}
	sealed, err := s.Sealer.Seal(fmt.Sprintf("${{database.%d}}", productionConn))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_variable_revisions(environment_id,key,revision,sensitivity,scopes,value_enc,value_digest,active,created_by,created_at) VALUES(?,'DATABASE_URL',1,'secret','build,runtime',?,'fixture',1,'admin',1)`, previewID, sealed); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"build", "runtime"} {
		values, err := s.modules.deployPlanning.OpenScopedVariables(t.Context(), previewID, scope)
		if err == nil || len(values) != 0 || strings.Contains(err.Error(), "production-secret") {
			t.Fatalf("%s exposed a production database: values=%v error=%v", scope, values, err)
		}
	}
	value, err := s.modules.deployDatabases.ResolveVariable(t.Context(), previewID, 1, strconv.FormatInt(previewConn, 10))
	if err != nil || value != "postgres://preview:preview-secret@preview.example.test/app" {
		t.Fatalf("explicit separate preview connection unavailable: %v", err)
	}
}
