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

func TestDeploymentConfigurationRoutesEnforceSessionCapabilityConfirmationAndAudit(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, backupID := insertDeploymentConfigurationAPI(t, s)
	routes := s.Routes()
	adminCookie := signInAs(t, s, "config-admin", auth.RoleAdmin)
	readerCookie := signInAs(t, s, "config-reader", auth.RoleReadOnly)
	admin := &client{t: t, h: routes, cookie: adminCookie}
	reader := &client{t: t, h: routes, cookie: readerCookie}
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)

	if response := reader.do(http.MethodGet, base+"/variables", "", nil); response.Code != http.StatusOK {
		t.Fatalf("readonly variable list = %d %s", response.Code, response.Body.String())
	}
	secret := "top-secret-c6"
	putBody := fmt.Sprintf(`{"revision":1,"value":%q,"sensitivity":"secret","scopes":["runtime"]}`, secret)
	if response := reader.do(http.MethodPut, base+"/variables/API_TOKEN", putBody, nil); response.Code != http.StatusForbidden {
		t.Fatalf("readonly variable mutation = %d %s", response.Code, response.Body.String())
	}
	var adminID int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM users WHERE username = 'config-admin'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	adminUser, err := s.Auth.UserByID(t.Context(), adminID)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Auth.CreateAPIToken(t.Context(), adminUser, "config-token", auth.RoleAdmin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tokenClient := &client{t: t, h: routes}
	if response := tokenClient.do(http.MethodPut, base+"/variables/API_TOKEN", putBody,
		map[string]string{"Authorization": "Bearer " + token}); response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), `"code":"session_required"`) {
		t.Fatalf("token variable mutation = %d %s", response.Code, response.Body.String())
	}
	put := admin.do(http.MethodPut, base+"/variables/API_TOKEN", putBody, nil)
	if put.Code != http.StatusOK || strings.Contains(put.Body.String(), secret) {
		t.Fatalf("admin variable mutation = %d %s", put.Code, put.Body.String())
	}
	reveal := admin.do(http.MethodGet, base+"/variables/API_TOKEN/reveal", "", nil)
	if reveal.Code != http.StatusOK || !strings.Contains(reveal.Body.String(), secret) {
		t.Fatalf("admin variable reveal = %d %s", reveal.Code, reveal.Body.String())
	}
	if response := reader.do(http.MethodGet, base+"/variables/API_TOKEN/reveal", "", nil); response.Code != http.StatusForbidden {
		t.Fatalf("readonly variable reveal = %d %s", response.Code, response.Body.String())
	}
	pending := reader.do(http.MethodGet, base+"/pending", "", nil)
	if pending.Code != http.StatusOK || !strings.Contains(pending.Body.String(), `"desiredRevision":2`) ||
		!strings.Contains(pending.Body.String(), `"pending":true`) || strings.Contains(pending.Body.String(), secret) {
		t.Fatalf("pending read = %d %s", pending.Code, pending.Body.String())
	}

	archivePath := fmt.Sprintf("/api/v1/deploy/%d/archive", projectID)
	if response := reader.do(http.MethodPost, archivePath, `{}`, nil); response.Code != http.StatusForbidden {
		t.Fatalf("readonly archive = %d %s", response.Code, response.Body.String())
	}
	if response := admin.do(http.MethodPost, archivePath, `{}`, nil); response.Code != http.StatusOK {
		t.Fatalf("admin archive = %d %s", response.Code, response.Body.String())
	}
	removalPath := fmt.Sprintf("/api/v1/deploy/%d/removal-plan", projectID)
	if response := tokenClient.do(http.MethodPost, removalPath, `{}`,
		map[string]string{"Authorization": "Bearer " + token}); response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), `"code":"session_required"`) {
		t.Fatalf("token removal preview = %d %s", response.Code, response.Body.String())
	}
	preview := admin.do(http.MethodPost, removalPath, `{}`, nil)
	if preview.Code != http.StatusOK {
		t.Fatalf("removal preview = %d %s", preview.Code, preview.Body.String())
	}
	var plan deploy.RemovalPlan
	if err := json.Unmarshal(preview.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	var volume, backup *deploy.RemovalTarget
	for index := range plan.Targets {
		target := &plan.Targets[index]
		switch target.Kind {
		case "docker_volume":
			volume = target
		case "backup_job":
			backup = target
		}
	}
	if volume == nil || backup == nil || backup.ResourceID != fmt.Sprint(backupID) {
		t.Fatalf("removal targets = %#v", plan.Targets)
	}
	removePath := fmt.Sprintf("/api/v1/deploy/%d/remove-managed", projectID)
	volumeBody, _ := json.Marshal(deploy.RemoveManagedRequest{PlanDigest: plan.Digest, TargetIDs: []string{volume.ID}})
	if response := admin.do(http.MethodPost, removePath, string(volumeBody), nil); response.Code != http.StatusPreconditionRequired {
		t.Fatalf("unconfirmed volume removal = %d %s", response.Code, response.Body.String())
	}
	backupBody, _ := json.Marshal(deploy.RemoveManagedRequest{PlanDigest: plan.Digest, TargetIDs: []string{backup.ID}})
	if response := admin.do(http.MethodPost, removePath, string(backupBody), nil); response.Code != http.StatusOK {
		t.Fatalf("ordinary managed removal = %d %s", response.Code, response.Body.String())
	}
	var backupCount int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM backup_jobs WHERE id = ?`, backupID).Scan(&backupCount); err != nil || backupCount != 0 {
		t.Fatalf("managed backup rows = %d, error=%v", backupCount, err)
	}
	var audits string
	if err := s.Store.DB.QueryRow(`SELECT COALESCE(group_concat(action || ' ' || detail), '') FROM audit_log WHERE action LIKE 'deploy.%'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"deploy.variable.update", "deploy.variable.reveal", "deploy.archive", "deploy.removal.preview", "deploy.resources.remove"} {
		if !strings.Contains(audits, action) {
			t.Errorf("missing audit action %s in %s", action, audits)
		}
	}
	if strings.Contains(audits, secret) {
		t.Fatalf("deployment audit leaked secret: %s", audits)
	}
}

// A remover failure must reach the operator as the remover's own sentence at
// 503 (a missing owner) rather than a bare 500, with the partial execution —
// what still needs attention — in the body beside the error.
func TestRemoveManagedReportsAnUnavailableOwnerWithThePartialExecution(t *testing.T) {
	s := testServer(t)
	projectID, _, backupID := insertDeploymentConfigurationAPI(t, s)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "remove-fail-admin", auth.RoleAdmin)}
	archivePath := fmt.Sprintf("/api/v1/deploy/%d/archive", projectID)
	if response := admin.do(http.MethodPost, archivePath, `{}`, nil); response.Code != http.StatusOK {
		t.Fatalf("archive = %d %s", response.Code, response.Body.String())
	}
	removalPath := fmt.Sprintf("/api/v1/deploy/%d/removal-plan", projectID)
	preview := admin.do(http.MethodPost, removalPath, `{}`, nil)
	if preview.Code != http.StatusOK {
		t.Fatalf("removal preview = %d %s", preview.Code, preview.Body.String())
	}
	var plan deploy.RemovalPlan
	if err := json.Unmarshal(preview.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	var backup *deploy.RemovalTarget
	for index := range plan.Targets {
		if plan.Targets[index].Kind == "backup_job" && plan.Targets[index].ResourceID == fmt.Sprint(backupID) {
			backup = &plan.Targets[index]
		}
	}
	if backup == nil {
		t.Fatalf("no backup_job target in plan: %#v", plan.Targets)
	}

	s.modules.backupStore = nil // the owner this removal needs is unavailable
	removePath := fmt.Sprintf("/api/v1/deploy/%d/remove-managed", projectID)
	body, _ := json.Marshal(deploy.RemoveManagedRequest{PlanDigest: plan.Digest, TargetIDs: []string{backup.ID}})
	response := admin.do(http.MethodPost, removePath, string(body), nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("remove-managed with unavailable owner = %d %s", response.Code, response.Body.String())
	}
	var decoded struct {
		Error     httpx.APIError          `json:"error"`
		Execution deploy.RemovalExecution `json:"execution"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Error.Code != "removal_failed" || decoded.Error.Message != "Backups is unavailable" {
		t.Fatalf("error body = %#v", decoded.Error)
	}
	found := false
	for _, target := range decoded.Execution.Remaining {
		found = found || target.ID == backup.ID
	}
	if !found {
		t.Fatalf("execution body missing the failed target in remaining: %#v", decoded.Execution)
	}
	var audited string
	if err := s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action='deploy.resources.remove' ORDER BY id DESC LIMIT 1`).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(audited, "Backups is unavailable") {
		t.Fatalf("audit detail missing the remover's message: %s", audited)
	}
}

// The configuration save route surfaces PlanConfiguration.Validate's field
// pointer in the error body, so the settings UI can highlight the control
// that needs fixing instead of a plan-wide banner.
func TestDeploymentConfigurationSaveReportsAFieldForAnInvalidPort(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "config-field-admin", auth.RoleAdmin)}
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	configuration, err := s.modules.deployPlanning.EnvironmentConfiguration(t.Context(), projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	request := deploy.ConfigurationWriteRequest{
		Revision: configuration.Revision, Build: configuration.Build,
		Runtime:      configuration.Runtime,
		Dependencies: configuration.Dependencies, Checks: configuration.Checks, Domains: configuration.Domains,
	}
	request.Runtime.InternalPort = 99999
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := admin.do(http.MethodPut, base+"/configuration", string(body), nil)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid port save = %d %s", response.Code, response.Body.String())
	}
	var decoded struct {
		Error httpx.APIError `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Error.Code != "invalid_plan" || decoded.Error.Field != "runtime.internalPort" {
		t.Fatalf("error body = %#v", decoded.Error)
	}
}

// Revealing a variable that does not exist is a missing resource, not a bad
// request: the route answers 404 variable_not_found rather than the 400 an
// operator would otherwise read as "you typed the name wrong".
func TestDeploymentVariableRevealAnswersNotFoundForAnUnknownName(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "reveal-404-admin", auth.RoleAdmin)}
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/variables/NOT_A_VARIABLE/reveal", projectID, environmentID)
	response := admin.do(http.MethodGet, path, "", nil)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"variable_not_found"`) {
		t.Fatalf("reveal unknown variable = %d %s", response.Code, response.Body.String())
	}
}

// POST /deploy/{id}/unarchive restores the display name Archive tombstoned,
// refuses a name another active project has since taken, and leaves triggers
// disabled for the operator to turn back on.
func TestDeploymentUnarchiveRestoresNameAndRefusesATakenOne(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "unarchive-admin", auth.RoleAdmin)}
	if _, err := s.Store.DB.Exec(`
		INSERT INTO deploy_triggers(environment_id, name, kind, enabled, created_at, updated_at)
		VALUES(?, 'push', 'generic_hook', 1, 1, 1)`, environmentID); err != nil {
		t.Fatal(err)
	}
	archivePath := fmt.Sprintf("/api/v1/deploy/%d/archive", projectID)
	if response := admin.do(http.MethodPost, archivePath, `{}`, nil); response.Code != http.StatusOK {
		t.Fatalf("archive = %d %s", response.Code, response.Body.String())
	}

	unarchivePath := fmt.Sprintf("/api/v1/deploy/%d/unarchive", projectID)
	response := admin.do(http.MethodPost, unarchivePath, `{}`, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("unarchive = %d %s", response.Code, response.Body.String())
	}
	var project deploy.Project
	if err := json.Unmarshal(response.Body.Bytes(), &project); err != nil {
		t.Fatal(err)
	}
	if project.Name != "api-config-app" || project.ArchivedAt != nil {
		t.Fatalf("unarchived project = %#v", project)
	}
	var enabled int
	if err := s.Store.DB.QueryRow(`SELECT enabled FROM deploy_triggers WHERE environment_id=?`, environmentID).Scan(&enabled); err != nil || enabled != 0 {
		t.Fatalf("trigger enabled = %d, %v, want still disabled after unarchive", enabled, err)
	}
	var audited int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='deploy.unarchive' AND success=1`).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("unarchive audit count = %d, %v", audited, err)
	}

	// Re-archive, let another project take the freed name, then the next
	// unarchive attempt must be refused rather than colliding.
	if response := admin.do(http.MethodPost, archivePath, `{}`, nil); response.Code != http.StatusOK {
		t.Fatalf("re-archive = %d %s", response.Code, response.Body.String())
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_projects(name, repo_path, hook_secret, hook_id, created_at) VALUES('api-config-app', '/srv/new', 'sealed', 'new-hook', 1)`); err != nil {
		t.Fatal(err)
	}
	taken := admin.do(http.MethodPost, unarchivePath, `{}`, nil)
	if taken.Code != http.StatusConflict {
		t.Fatalf("unarchive over a taken name = %d %s", taken.Code, taken.Body.String())
	}
	if got := decodedAPIError(t, taken.Body.Bytes()); got.Code != "name_taken" {
		t.Fatalf("error code = %q, want name_taken", got.Code)
	}
}

// PUT .../releases/{release}/pin toggles the pinned column an admin session
// can flip, and refuses a release from another project or environment.
func TestDeploymentReleasePinRoute(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	now := time.Now().UTC().Unix()
	result, err := s.Store.DB.Exec(`
		INSERT INTO deploy_releases(project_id, environment_id, release_number, state, plan_revision, config_digest, variables_digest, strategy, created_at)
		VALUES(?, ?, 1, 'retained', 1, 'digest-config', 'digest-vars', 'blue_green', ?)`, projectID, environmentID, now)
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := result.LastInsertId()
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "pin-admin", auth.RoleAdmin)}
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "pin-reader", auth.RoleReadOnly)}
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/releases/%d/pin", projectID, environmentID, releaseID)

	if response := reader.do(http.MethodPut, path, `{"pinned":true}`, nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader pin = %d %s", response.Code, response.Body.String())
	}
	response := admin.do(http.MethodPut, path, `{"pinned":true}`, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("pin = %d %s", response.Code, response.Body.String())
	}
	var pinned int
	if err := s.Store.DB.QueryRow(`SELECT pinned FROM deploy_releases WHERE id=?`, releaseID).Scan(&pinned); err != nil || pinned != 1 {
		t.Fatalf("pinned column = %d, %v, want 1", pinned, err)
	}
	response = admin.do(http.MethodPut, path, `{"pinned":false}`, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("unpin = %d %s", response.Code, response.Body.String())
	}
	if err := s.Store.DB.QueryRow(`SELECT pinned FROM deploy_releases WHERE id=?`, releaseID).Scan(&pinned); err != nil || pinned != 0 {
		t.Fatalf("pinned column after unpin = %d, %v, want 0", pinned, err)
	}
	var audited int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='deploy.release.pin' AND success=1`).Scan(&audited); err != nil || audited != 2 {
		t.Fatalf("pin audit count = %d, %v, want 2", audited, err)
	}

	otherPath := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/releases/%d/pin", projectID+1, environmentID, releaseID)
	if response := admin.do(http.MethodPut, otherPath, `{"pinned":true}`, nil); response.Code != http.StatusNotFound {
		t.Fatalf("cross-project pin = %d %s", response.Code, response.Body.String())
	}
}

func TestDeploymentLifecycleRoutesUseSharedDestructiveBudget(t *testing.T) {
	s := testServer(t)
	projectID, _, _ := insertDeploymentConfigurationAPI(t, s)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "budget-admin", auth.RoleAdmin)}
	path := fmt.Sprintf("/api/v1/deploy/%d/archive", projectID)
	limited := false
	for attempt := 0; attempt < 60; attempt++ {
		response := admin.do(http.MethodPost, path, `{}`, nil)
		if response.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
		if response.Code != http.StatusOK {
			t.Fatalf("archive attempt %d = %d %s", attempt, response.Code, response.Body.String())
		}
	}
	if !limited {
		t.Fatal("deployment archive did not consume the shared destructive budget")
	}
	if response := admin.do(http.MethodGet, "/api/v1/deploy/", "", nil); response.Code != http.StatusOK {
		t.Fatalf("destructive limiter also blocked deployment read = %d %s", response.Code, response.Body.String())
	}
}

func insertDeploymentConfigurationAPI(t *testing.T, s *Server) (int64, int64, int64) {
	t.Helper()
	now := time.Now().UTC().Unix()
	project, err := s.Store.DB.Exec(`
		INSERT INTO deploy_projects(name, profile, repo_path, branch, compose_file, hook_secret, hook_id, enabled, created_at, updated_at)
		VALUES('api-config-app', 'worker', '/srv/api-config-app', 'main', 'compose.yml', '', 'api-config-hook', 1, ?, ?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := project.LastInsertId()
	environment, err := s.Store.DB.Exec(`
		INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		VALUES(?, 'production', 'production', 'production', 1, 'stop_first', 1, 1, ?, ?)`, projectID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ := environment.LastInsertId()
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, statement := range []string{
		`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at) VALUES(?, 1, 'git', '{}', '{}', ?, ?)`,
		`INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at) VALUES(?, 1, 'none', '{"method":"none"}', '{}', '{}', ?, ?)`,
		`INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at) VALUES(?, 1, '{"strategy":"stop_first","mounts":[{"source":"api-config-data","target":"/data","ownership":"managed"}]}', '{}', ?, ?)`,
	} {
		if _, err := s.Store.DB.Exec(statement, environmentID, digest, now); err != nil {
			t.Fatal(err)
		}
	}
	backup, err := s.Store.DB.Exec(`
		INSERT INTO backup_jobs(name, sources, excludes, target_kind, target_cfg, secrets_enc, schedule, retention, enabled, created_at)
		VALUES('managed-config-backup', '["/srv/api-config-app"]', '[]', 'local', '{"path":"/tmp"}', '', '', 2, 1, ?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	backupID, _ := backup.LastInsertId()
	if _, err := s.Store.DB.Exec(`
		INSERT INTO deploy_dependencies(environment_id, release_id, kind, ownership, resource_kind, resource_id, config_json, created_at)
		VALUES(?, 0, 'backup', 'managed', 'backup_job', ?, '{}', ?)`, environmentID, fmt.Sprint(backupID), now); err != nil {
		t.Fatal(err)
	}
	return projectID, environmentID, backupID
}
