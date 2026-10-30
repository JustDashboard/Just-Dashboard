package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// Railpack is a build method like any other: saved through the configuration
// PUT (which `jd config set` sends), validated by the same rules, read back
// by the configuration route, audited — and refused for a source that has no
// checkout for it to read.
func TestDeploymentConfigurationSavesARailpackBuild(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "config-railpack-admin", auth.RoleAdmin)}
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	write := func(edit func(*deploy.BuildPlanConfig)) string {
		t.Helper()
		configuration, err := s.modules.deployPlanning.EnvironmentConfiguration(t.Context(), projectID, environmentID)
		if err != nil {
			t.Fatal(err)
		}
		request := deploy.ConfigurationWriteRequest{
			Revision: configuration.Revision, Build: configuration.Build, Runtime: configuration.Runtime,
			Dependencies: configuration.Dependencies, Checks: configuration.Checks, Domains: configuration.Domains,
		}
		request.Runtime.InternalPort = 8080
		edit(&request.Build)
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	refused := func(body, field string) {
		t.Helper()
		response := admin.do(http.MethodPut, base+"/configuration", body, nil)
		var decoded struct {
			Error httpx.APIError `json:"error"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &decoded)
		if response.Code != http.StatusUnprocessableEntity || decoded.Error.Code != "invalid_plan" || decoded.Error.Field != field {
			t.Fatalf("want %s refused, got %d %s", field, response.Code, response.Body.String())
		}
	}
	refused(write(func(b *deploy.BuildPlanConfig) {
		b.Method, b.RailpackProvider = deploy.BuildRailpack, "cobol"
	}), "build.railpackProvider")
	refused(write(func(b *deploy.BuildPlanConfig) {
		b.Method, b.RailpackVersion = deploy.BuildRailpack, "0.0.1"
	}), "build.railpackVersion")
	refused(write(func(b *deploy.BuildPlanConfig) {
		b.Method, b.RailpackConfig = deploy.BuildRailpack, "../railpack.json"
	}), "build.railpackConfig")

	response := admin.do(http.MethodPut, base+"/configuration", write(func(b *deploy.BuildPlanConfig) {
		b.Method = deploy.BuildRailpack
		b.RailpackVersion, b.RailpackProvider, b.RailpackConfig = deploy.RailpackDefaultVersion, "python", "deploy/railpack.json"
		b.StartCommand = "python app.py"
	}), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("save = %d %s", response.Code, response.Body.String())
	}
	saved, err := s.modules.deployPlanning.EnvironmentConfiguration(t.Context(), projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Build.Method != deploy.BuildRailpack || saved.Build.RailpackProvider != "python" ||
		saved.Build.RailpackConfig != "deploy/railpack.json" || saved.Build.RailpackVersion != deploy.RailpackDefaultVersion {
		t.Fatalf("saved build = %+v", saved.Build)
	}
	read := admin.do(http.MethodGet, base+"/configuration", "", nil)
	for _, part := range []string{`"method":"railpack"`, `"railpackProvider":"python"`, `"railpackConfig":"deploy/railpack.json"`} {
		if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), part) {
			t.Fatalf("GET configuration lacks %s: %d %s", part, read.Code, read.Body.String())
		}
	}
	var audits int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'deploy.configuration.update' AND target = ?`, fmt.Sprint(environmentID)).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("configuration audits = %d, %v", audits, err)
	}

	// A Compose source's own files decide its build.
	now := time.Now().UTC().Unix()
	environment, err := s.Store.DB.Exec(`
		INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		VALUES(?, 'staging', 'staging', 'staging', 1, 'stop_first', 1, 0, ?, ?)`, projectID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	composeEnvironment, _ := environment.LastInsertId()
	digest := "sha256:" + strings.Repeat("b", 64)
	for _, statement := range []string{
		`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at) VALUES(?, 1, 'compose', '{}', '{}', ?, ?)`,
		`INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at) VALUES(?, 1, 'compose', '{"method":"compose"}', '{}', '{}', ?, ?)`,
		`INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at) VALUES(?, 1, '{"strategy":"stop_first"}', '{}', ?, ?)`,
	} {
		if _, err := s.Store.DB.Exec(statement, composeEnvironment, digest, now); err != nil {
			t.Fatal(err)
		}
	}
	environmentID, base = composeEnvironment, fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, composeEnvironment)
	refused(write(func(b *deploy.BuildPlanConfig) { b.Method = deploy.BuildRailpack }), "build.method")
}
