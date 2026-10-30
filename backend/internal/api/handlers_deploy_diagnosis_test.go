package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// A failed run asked about over HTTP: who may ask, what reaches the provider
// and what never does, the answer kept with the run, and the audit trail.
// The provider is a test server answering as Anthropic's API would.

const (
	diagnosisSecret     = "sk-live-variable-never-sent-91"
	diagnosisCredential = "registry-credential-never-sent-27"
)

type diagnosisBench struct {
	s                     *Server
	admin, reader         *client
	provider              *fakeProvider
	projectID, runID      int64
	base                  string
	failingLine, lastLine string
}

func newDiagnosisBench(t *testing.T) *diagnosisBench {
	t.Helper()
	s := testServer(t)
	provider := &fakeProvider{}
	server := httptest.NewServer(provider)
	t.Cleanup(server.Close)
	s.assistant.options.AnthropicURL = server.URL
	b := &diagnosisBench{s: s, provider: provider}
	routes := s.Routes()
	b.admin = &client{t: t, h: routes, cookie: signInAs(t, s, "diag-admin", auth.RoleAdmin)}
	b.reader = &client{t: t, h: routes, cookie: signInAs(t, s, "diag-reader", auth.RoleReadOnly)}

	projectID, environmentID := insertDiagnosisDeployFixture(t, s)
	b.projectID = projectID
	put := fmt.Sprintf(`{"revision":1,"value":%q,"sensitivity":"secret","scopes":["build"]}`, diagnosisSecret)
	if response := b.admin.do(http.MethodPut, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/variables/API_KEY", projectID, environmentID), put, nil); response.Code != http.StatusOK {
		t.Fatalf("variable = %d %s", response.Code, response.Body.String())
	}
	body, _ := json.Marshal(map[string]any{"operation": "deploy", "sourceRevision": strings.Repeat("b", 40)})
	response := b.admin.do(http.MethodPost, runCreatePath(projectID, environmentID), string(body), nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("deploy = %d %s", response.Code, response.Body.String())
	}
	var run struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	b.runID = run.ID
	b.base = fmt.Sprintf("/api/v1/deploy/%d/runs/%d/diagnosis", projectID, run.ID)

	sealed, err := s.Sealer.Seal(diagnosisCredential)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_credentials(name, kind, config_json, secret_enc, created_at, updated_at) VALUES('registry', 'registry', '{}', ?, 0, 0)`, sealed); err != nil {
		t.Fatal(err)
	}
	return b
}

// insertDiagnosisDeployFixture is a Git deployment whose plan the run can
// read back: a Dockerfile build listening on 3000.
func insertDiagnosisDeployFixture(t *testing.T, s *Server) (projectID, environmentID int64) {
	t.Helper()
	now := time.Now().UTC().Unix()
	project, err := s.Store.DB.Exec(`
		INSERT INTO deploy_projects(name, profile, repo_path, branch, compose_file, hook_secret, hook_id, enabled, created_at, updated_at)
		VALUES('diag-app', 'web', '/srv/diag-app', 'main', 'compose.yml', '', 'diag-app-hook', 1, ?, ?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ = project.LastInsertId()
	environment, err := s.Store.DB.Exec(`
		INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		VALUES(?, 'production', 'production', 'production', 1, 'stop_first', 1, 1, ?, ?)`, projectID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ = environment.LastInsertId()
	source, _ := json.Marshal(gitSourceFixture())
	identity, _ := json.Marshal(deploy.SourceIdentity{Kind: deploy.SourceGit})
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at) VALUES(?, 1, 'git', ?, ?, 'src-diag', ?)`,
			[]any{environmentID, string(source), string(identity), now}},
		{`INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at)
			VALUES(?, 1, 'dockerfile', '{"method":"dockerfile","dockerfile":"Dockerfile","secrets":[],"releaseTasks":[]}', '{}', '', 'build-diag', ?)`,
			[]any{environmentID, now}},
		{`INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at)
			VALUES(?, 1, '{"strategy":"stop_first","internalPort":3000}', '', 'runtime-diag', ?)`, []any{environmentID, now}},
	} {
		if _, err := s.Store.DB.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return projectID, environmentID
}

// fail ends the run as a build that npm refused, with a transcript that
// printed both secrets.
func (b *diagnosisBench) fail(t *testing.T) {
	t.Helper()
	snapshot, err := b.s.modules.deployRuns.Snapshot(t.Context(), b.runID)
	if err != nil {
		t.Fatal(err)
	}
	var build int64
	for _, step := range snapshot.Steps {
		if step.Key == deploy.StepBuildArtifact {
			build = step.ID
		}
	}
	if build == 0 {
		t.Fatalf("the run has no build step: %+v", snapshot.Steps)
	}
	evidence := `{"cause":{"code":"npm_lockfile_out_of_sync","phase":"install","command":"npm ci","exitCode":1,"subjects":["package-lock.json"],"lineSeq":1003}}`
	now := time.Now().Unix()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE deploy_runs SET state = 'failed', status = 'failed', terminal_code = 'npm_lockfile_out_of_sync', terminal_reason = 'npm ci refused to install', ended_at = ? WHERE id = ?`, []any{now, b.runID}},
		{`UPDATE deploy_steps SET status = 'failed', error_code = 'npm_lockfile_out_of_sync', error_message = 'package-lock.json is out of sync', evidence_json = ?, last_seq = 1004 WHERE id = ?`, []any{evidence, build}},
	} {
		if _, err := b.s.Store.DB.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	b.failingLine = "npm ERR! `npm ci` can only install packages when your package.json and package-lock.json are in sync."
	b.lastLine = "npm ERR! Missing: left-pad@1.3.0 from lock file"
	for i, text := range []string{
		"#8 [build 3/6] RUN npm ci",
		"npm notice using API_KEY " + diagnosisSecret,
		"npm notice pulled with " + diagnosisCredential,
		b.failingLine,
		b.lastLine,
	} {
		if _, err := b.s.Store.DB.Exec(`INSERT INTO deploy_log_chunks(run_id, step_id, seq, event_type, stream, ts, text, data_json)
			VALUES(?, ?, ?, 'step.log', 'stderr', ?, ?, '{}')`, b.runID, build, 1000+i, now, text); err != nil {
			t.Fatal(err)
		}
	}
}

func (b *diagnosisBench) diagnosis(t *testing.T, response *httptest.ResponseRecorder) *runDiagnosisView {
	t.Helper()
	var out struct {
		Diagnosis *runDiagnosisView `json:"diagnosis"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	return out.Diagnosis
}

func (b *diagnosisBench) audits(t *testing.T) []string {
	t.Helper()
	rows, err := b.s.Store.DB.Query(`SELECT detail FROM audit_log WHERE action = 'deploy.run.diagnose' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	details := []string{}
	for rows.Next() {
		var detail string
		if err := rows.Scan(&detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	return details
}

func TestAFailedRunIsDiagnosedOnlyWhenAskedAndNothingSecretIsSent(t *testing.T) {
	b := newDiagnosisBench(t)
	ask := func(c *client, body string) *httptest.ResponseRecorder {
		return c.do(http.MethodPost, b.base, body, nil)
	}

	if got := b.diagnosis(t, b.reader.do(http.MethodGet, b.base, "", nil)); got != nil {
		t.Fatalf("a run nobody asked about has %+v", got)
	}
	if response := ask(b.admin, `{}`); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "run_not_failed") {
		t.Fatalf("a queued run = %d %s", response.Code, response.Body.String())
	}
	b.fail(t)
	if response := ask(b.reader, `{}`); response.Code != http.StatusForbidden {
		t.Fatalf("a read-only account asked = %d %s", response.Code, response.Body.String())
	}
	if response := ask(b.admin, `{}`); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "assistant_not_configured") {
		t.Fatalf("no assistant = %d %s", response.Code, response.Body.String())
	}
	if b.provider.asked() != 0 {
		t.Fatal("the provider was asked before anybody set it up")
	}

	configureAssistant(t, b.s)
	b.provider.queue(claudeAnswer("CAUSE: package-lock.json does not match package.json, so npm ci refuses to install.\n" +
		"EVIDENCE:\n> 1003 | " + b.failingLine + "\n> npm ERR! a line the run never printed\n" +
		"STEPS:\n1. Run npm install and commit package-lock.json.\n2. Deploy again.\n"))
	response := ask(b.admin, `{}`)
	if response.Code != http.StatusOK {
		t.Fatalf("diagnose = %d %s", response.Code, response.Body.String())
	}
	got := b.diagnosis(t, response)
	if got == nil || got.Answer.Cause != "package-lock.json does not match package.json, so npm ci refuses to install." ||
		len(got.Answer.Evidence) != 1 || got.Answer.Evidence[0].Seq != 1003 || got.Answer.Unmatched != 1 ||
		len(got.Answer.Steps) != 2 || got.ModelLabel != "Claude Opus 5.5" || got.CreatedBy != "diag-admin" || got.Masked < 2 {
		t.Fatalf("diagnosis = %+v", got)
	}

	sent := b.provider.raw[len(b.provider.raw)-1]
	for _, leak := range []string{diagnosisSecret, diagnosisCredential} {
		if strings.Contains(sent, leak) || strings.Contains(got.Sent, leak) || strings.Contains(response.Body.String(), leak) {
			t.Errorf("%s left the server", leak)
		}
	}
	for _, want := range []string{"npm_lockfile_out_of_sync", "1003 | npm ERR!", "API_KEY (build, secret)", "npm notice using API_KEY [REDACTED]", "Dockerfile"} {
		if !strings.Contains(got.Sent, want) {
			t.Errorf("what was sent lacks %q:\n%s", want, got.Sent)
		}
	}
	if !strings.Contains(sent, "never guess them") {
		t.Errorf("the provider was not told the shape of an answer: %.300s", sent)
	}

	// Reopening the run reads the answer kept with it, for any account that
	// reads the run, and pressing again without asking again costs nothing.
	if kept := b.diagnosis(t, b.reader.do(http.MethodGet, b.base, "", nil)); kept == nil || kept.Answer.Cause != got.Answer.Cause || kept.Sent != got.Sent {
		t.Fatalf("kept = %+v", kept)
	}
	if response := ask(b.admin, `{}`); response.Code != http.StatusOK || b.provider.asked() != 1 {
		t.Fatalf("pressed again = %d, provider asked %d times", response.Code, b.provider.asked())
	}
	b.provider.queue(claudeAnswer("CAUSE: The lockfile is stale.\nSTEPS:\n1. Refresh it.\n"))
	if again := b.diagnosis(t, ask(b.admin, `{"again":true}`)); again == nil || again.Answer.Cause != "The lockfile is stale." || b.provider.asked() != 2 {
		t.Fatalf("asked again = %+v", again)
	}

	b.provider.queue(func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	})
	if response := ask(b.admin, `{"again":true}`); response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "assistant_failed") ||
		strings.Contains(response.Body.String(), diagramKey) {
		t.Fatalf("a refusing provider = %d %s", response.Code, response.Body.String())
	}
	if kept := b.diagnosis(t, b.admin.do(http.MethodGet, b.base, "", nil)); kept == nil || kept.Answer.Cause != "The lockfile is stale." {
		t.Fatalf("a failed ask replaced the kept answer: %+v", kept)
	}

	other := fmt.Sprintf("/api/v1/deploy/%d/runs/%d/diagnosis", b.projectID+100, b.runID)
	if response := b.admin.do(http.MethodGet, other, "", nil); response.Code != http.StatusNotFound {
		t.Errorf("another project's run read = %d", response.Code)
	}
	if response := ask(&client{t: t, h: b.admin.h, cookie: b.admin.cookie}, `{"again":"yes"}`); response.Code != http.StatusBadRequest {
		t.Errorf("a malformed body = %d", response.Code)
	}

	details := b.audits(t)
	outcomes := []string{}
	for _, detail := range details {
		for _, leak := range []string{diagnosisSecret, diagnosisCredential, "npm ERR!", "lockfile", diagramKey} {
			if strings.Contains(detail, leak) {
				t.Errorf("the audit entry holds %q: %s", leak, detail)
			}
		}
		// A refused request's entry is its message; a failed one's has the
		// error after the detail.
		if match := regexp.MustCompile(`"outcome":"(\w+)"`).FindStringSubmatch(detail); match != nil {
			outcomes = append(outcomes, match[1])
		}
	}
	if got := strings.Join(outcomes, ","); got != "answered,kept,answered,failed" {
		t.Errorf("audited outcomes = %s (%v)", got, details)
	}

	if _, err := b.s.Store.DB.Exec(`DELETE FROM deploy_runs WHERE id = ?`, b.runID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := b.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_run_diagnoses`).Scan(&left); err != nil || left != 0 {
		t.Errorf("a deleted run left %d diagnoses (%v)", left, err)
	}
}
