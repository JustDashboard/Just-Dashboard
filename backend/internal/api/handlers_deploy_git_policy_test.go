package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

type policySourceFixture struct {
	revision string
	paths    []string
}

func (f *policySourceFixture) ResolveGitRevision(context.Context, deploy.DraftSourceConfig) (string, error) {
	return f.revision, nil
}
func (f *policySourceFixture) ResolveGitChangedPaths(context.Context, deploy.DraftSourceConfig, string, string) ([]string, error) {
	return f.paths, nil
}

func TestGitPolicyAPIAndSignedHooksUseCompleteDiffAndPinRevision(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	ctx := context.Background()
	source := deploy.DraftSourceConfig{Kind: deploy.SourceGit, Mode: deploy.SourceModeGitURL, URL: "https://github.com/acme/app.git", Ref: "main"}
	encoded, _ := json.Marshal(source)
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	if _, err := s.Store.DB.Exec(`DELETE FROM deploy_sources WHERE environment_id=?`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_sources(environment_id,revision,kind,config_json,identity_json,digest,created_at) VALUES(?,1,'git',?,?,'fixture',1)`, environmentID, string(encoded), `{"revision":"`+a+`"}`); err != nil {
		t.Fatal(err)
	}
	resolver := &policySourceFixture{revision: b, paths: []string{"docs/readme.md"}}
	s.modules.deployGit = deploy.NewGitWatcher(s.modules.deployRuns, resolver, s.dispatchGitDeployment)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "policy-admin", auth.RoleAdmin)}
	reader := &client{t: t, h: routes, cookie: signInAs(t, s, "policy-reader", auth.RoleReadOnly)}
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	write := `{"automatic":true,"revision":0,"watchInclude":["app/**"],"watchExclude":[]}`
	if res := reader.do(http.MethodPut, base+"/git-policy", write, nil); res.Code != http.StatusForbidden {
		t.Fatalf("reader edit=%d %s", res.Code, res.Body.String())
	}
	if res := admin.do(http.MethodPut, base+"/git-policy", write, nil); res.Code != http.StatusOK {
		t.Fatalf("policy save=%d %s", res.Code, res.Body.String())
	}
	if res := admin.do(http.MethodPut, base+"/git-policy", write, nil); res.Code != http.StatusConflict {
		t.Fatalf("stale policy save=%d %s", res.Code, res.Body.String())
	}
	project, err := s.modules.deployStore.Get(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.enqueueNormalizedDeploymentAtSource(ctx, project, environmentID, deploy.OperationDeploy, 0, deploy.TriggerManual, "operator", "first", nil, a, "", 1); err != nil {
		t.Fatal(err)
	}
	created := admin.do(http.MethodPost, base+"/triggers", `{"name":"GitHub","kind":"github","enabled":true,"config":{"repository":"acme/app","ref":"main","events":["push"]}}`, nil)
	var trigger deploy.TriggerCreated
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &trigger) != nil {
		t.Fatalf("trigger=%d %s", created.Code, created.Body.String())
	}
	hook := fmt.Sprintf("/api/v1/hooks/providers/github/%s", trigger.Trigger.HookID)
	deliver := func(id, revision, claimedPath string) (int, string) {
		t.Helper()
		body := fmt.Sprintf(`{"ref":"refs/heads/main","after":%q,"repository":{"full_name":"acme/app"},"commits":[{"modified":[%q]}]}`, revision, claimedPath)
		res := (&client{t: t, h: routes}).do(http.MethodPost, hook, body, map[string]string{"X-GitHub-Event": "push", "X-GitHub-Delivery": id, "X-Hub-Signature-256": signProviderPayload([]byte(body), trigger.Secret)})
		return res.Code, res.Body.String()
	}
	if code, body := deliver("irrelevant", b, "app/claimed.go"); code != http.StatusAccepted || !strings.Contains(body, `"accepted":false`) || !strings.Contains(body, "watch_paths_ignored") {
		t.Fatalf("ignored hook=%d %s", code, body)
	}
	resolver.revision, resolver.paths = c, []string{"app/actual.go"}
	if code, body := deliver("relevant", c, "docs/claimed.md"); code != http.StatusAccepted || !strings.Contains(body, `"accepted":true`) {
		t.Fatalf("relevant hook=%d %s", code, body)
	}
	var revision, metadata string
	if err := s.Store.DB.QueryRow(`SELECT source_revision, metadata_json FROM deploy_runs WHERE project_id=? ORDER BY id DESC LIMIT 1`, projectID).Scan(&revision, &metadata); err != nil || revision != c {
		t.Fatalf("pinned revision=%s, %v", revision, err)
	}
	// The run's metadata carries the watcher's complete diff (from the
	// configured watch filter), not just the payload's own claimed path, so a
	// later automatic run's supersession check has something real to compare.
	if !strings.Contains(metadata, `"changedPaths":["app/actual.go"]`) {
		t.Fatalf("run metadata = %s, want the resolved changed paths", metadata)
	}
	policy, err := s.modules.deployRuns.GitDeploymentPolicy(ctx, projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.dispatchGitDeployment(ctx, deploy.GitWatchTarget{ProjectID: projectID, EnvironmentID: environmentID, PlanRevision: 1, Source: source, PolicyKey: policy.Key()}, c, "same-commit-poll", nil); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_runs WHERE project_id=?`, projectID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("hook/poll duplicate count=%d, %v", count, err)
	}
	if code, body := deliver("late", b, "app/actual.go"); code != http.StatusAccepted || !strings.Contains(body, "superseded_revision") {
		t.Fatalf("late hook=%d %s", code, body)
	}
	if res := admin.do(http.MethodPut, base+"/git-policy", `{"automatic":false,"revision":1,"watchInclude":["app/**"]}`, nil); res.Code != http.StatusOK {
		t.Fatalf("manual policy=%d %s", res.Code, res.Body.String())
	}
	if code, body := deliver("disabled", c, "app/actual.go"); code != http.StatusUnprocessableEntity || !strings.Contains(body, "manual_only") {
		t.Fatalf("manual-only hook=%d %s", code, body)
	}
	status := reader.do(http.MethodGet, base+"/git-watch", "", nil)
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"status":"manual_only"`) {
		t.Fatalf("status=%d %s", status.Code, status.Body.String())
	}
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='deploy.git.policy' AND success=1`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("policy audit=%d, %v", count, err)
	}
	var reason string
	if err := s.Store.DB.QueryRow(`SELECT reason FROM deploy_webhook_deliveries WHERE delivery_id='irrelevant'`).Scan(&reason); err != nil || reason != "watch_paths_ignored" {
		t.Fatalf("delivery evidence=%s, %v", reason, err)
	}
}
