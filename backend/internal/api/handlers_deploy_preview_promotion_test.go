package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// useRemoteGitProduction gives the fixture's production the GitHub source a
// pull request preview fetches its head from.
func useRemoteGitProduction(t *testing.T, s *Server, environmentID int64) {
	t.Helper()
	config, _ := json.Marshal(deploy.DraftSourceConfig{Kind: deploy.SourceGit, Mode: deploy.SourceModeGitURL, URL: "https://github.com/acme/app.git", Ref: "main"})
	identity, _ := json.Marshal(deploy.SourceIdentity{Kind: deploy.SourceGit, Remote: "https://github.com/acme/app.git", Repository: "acme/app", Ref: "main", Revision: strings.Repeat("a", 40)})
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM deploy_sources WHERE environment_id = ? AND revision = 2`, []any{environmentID}},
		{`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at) VALUES(?, 2, 'git', ?, ?, ?, ?)`,
			[]any{environmentID, string(config), string(identity), "sha256:" + strings.Repeat("b", 64), time.Now().Unix()}},
	} {
		if _, err := s.Store.DB.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

// previewThroughTheWebhook opens pull request number through a signed
// delivery, has the administrator approve and deploy it, and leaves the
// preview's release live, as a successful preview build does.
func (f *stagingRouteFixture) previewThroughTheWebhook(t *testing.T, number int, revision, headRepository string) (int64, *deploy.ReleaseWithArtifacts) {
	t.Helper()
	created := f.admin.do(http.MethodPost, f.path(fmt.Sprintf("/environments/%d/triggers", f.production)),
		`{"name":"Previews","kind":"github","provider":"github","enabled":true,"config":{"repository":"acme/app","ref":"main","events":["pull_request"],"preview":true}}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create trigger = %d %s", created.Code, created.Body.String())
	}
	var trigger deploy.TriggerCreated
	if err := json.Unmarshal(created.Body.Bytes(), &trigger); err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"action":"opened","number":%d,"repository":{"full_name":"acme/app"},"pull_request":{"title":"Faster checkout","user":{"login":"contributor"},"head":{"sha":%q,"ref":"feature","repo":{"full_name":%q}}}}`,
		number, revision, headRepository)
	hook := "/api/v1/hooks/providers/github/" + trigger.Trigger.HookID
	if response := (&client{t: t, h: f.s.Routes()}).do(http.MethodPost, hook, payload, map[string]string{
		"X-GitHub-Event": "pull_request", "X-GitHub-Delivery": fmt.Sprintf("opened-%d", number),
		"X-Hub-Signature-256": signProviderPayload([]byte(payload), trigger.Secret),
	}); response.Code != http.StatusAccepted {
		t.Fatalf("delivery = %d %s", response.Code, response.Body.String())
	}
	listed := f.admin.do(http.MethodGet, f.path("/previews/approvals"), "", nil)
	var approvals []deploy.PreviewApproval
	if err := json.Unmarshal(listed.Body.Bytes(), &approvals); err != nil || len(approvals) == 0 {
		t.Fatalf("approvals = %s, %v", listed.Body.String(), err)
	}
	approved := f.admin.do(http.MethodPost, f.path(fmt.Sprintf("/previews/approvals/%d/approve", approvals[0].ID)),
		fmt.Sprintf(`{"revision":%q,"deploy":true}`, revision), nil)
	if approved.Code != http.StatusAccepted {
		t.Fatalf("approve = %d %s", approved.Code, approved.Body.String())
	}
	var result struct {
		RunID int64 `json:"runId"`
	}
	if err := json.Unmarshal(approved.Body.Bytes(), &result); err != nil || result.RunID == 0 {
		t.Fatalf("approve result = %s, %v", approved.Body.String(), err)
	}
	run, err := f.s.modules.deployRuns.Run(t.Context(), result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return approvals[0].ID, activateSyntheticNormalizedRelease(t, f.s, *run, "preview")
}

// Promoting a pull request's preview into staging: an administrator's call
// (a session, an admin key or a deploy key, never an operator), into staging
// only, refused in words when the preview may not go, and audited with where
// the image came from.
func TestPreviewPromotionIntoStagingIsAnAdministratorsAndRefusedInWords(t *testing.T) {
	f := newStagingRouteFixture(t)
	useRemoteGitProduction(t, f.s, f.production)
	added := f.admin.do(http.MethodPost, f.path("/environments"), `{"kind":"staging"}`, nil)
	if added.Code != http.StatusCreated {
		t.Fatalf("add staging = %d %s", added.Code, added.Body.String())
	}
	var copied deploy.StagingCopy
	if err := json.Unmarshal(added.Body.Bytes(), &copied); err != nil {
		t.Fatal(err)
	}
	staging := copied.Environment.ID
	revision := strings.Repeat("e", 40)
	approvalID, release := f.previewThroughTheWebhook(t, 17, revision, "fork/app")
	image := "sha256:" + strings.Repeat("c", 64)
	f.images.held[image] = true

	promote := f.path(fmt.Sprintf("/environments/%d/promote", staging))
	body := `{"source":"preview","preview":17}`
	if response := f.readonly.do(http.MethodPost, promote, body, nil); response.Code != http.StatusForbidden {
		t.Fatalf("readonly promote = %d %s", response.Code, response.Body.String())
	}
	// An operator may deploy staging, but a pull request's code reaches it
	// only by an administrator's decision.
	if response := f.limited.do(http.MethodPost, promote, body, nil); response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), "takes an administrator") {
		t.Fatalf("operator promote = %d %s", response.Code, response.Body.String())
	}
	for _, bad := range []string{`{"source":"preview"}`, `{"source":"elsewhere","releaseId":1}`, `{"releaseId":1,"preview":17}`} {
		if response := f.admin.do(http.MethodPost, promote, bad, nil); response.Code != http.StatusBadRequest {
			t.Fatalf("promote %s = %d %s", bad, response.Code, response.Body.String())
		}
	}
	if response := f.admin.do(http.MethodPost, f.path(fmt.Sprintf("/environments/%d/promote", f.production)), body, nil); response.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(response.Body.String(), "preview_promotion_target") || !strings.Contains(response.Body.String(), "never straight into production") {
		t.Fatalf("a preview into production = %d %s", response.Code, response.Body.String())
	}
	if response := f.readonly.do(http.MethodGet, f.path(fmt.Sprintf("/environments/%d/promotion?source=preview&preview=17", f.production)), "", nil); response.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(response.Body.String(), "preview_promotion_target") {
		t.Fatalf("the confirmation of a preview into production = %d %s", response.Code, response.Body.String())
	}

	read := f.readonly.do(http.MethodGet, f.path(fmt.Sprintf("/environments/%d/promotion?source=preview&preview=17", staging)), "", nil)
	var reading deploy.PromotionPreview
	if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &reading) != nil {
		t.Fatalf("promotion read = %d %s", read.Code, read.Body.String())
	}
	if reading.Source.Kind != deploy.PromotionFromPreview || reading.Source.Release.ID != release.Release.ID || reading.Source.ImageID != image ||
		!reading.Source.ImagePresent || reading.Source.Preview == nil || reading.Source.Preview.Number != 17 ||
		reading.Source.Preview.ApprovedBy != "staging-admin" || !reading.Source.Preview.Fork ||
		reading.Source.Preview.URL != "https://github.com/acme/app/pull/17" || reading.Target.EnvironmentID != staging || len(reading.Blockers) != 0 {
		t.Fatalf("promotion read = %#v %#v", reading.Source, reading.Blockers)
	}

	delete(f.images.held, image)
	if response := f.admin.do(http.MethodPost, promote, body, nil); response.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(response.Body.String(), "preview_promotion_image_missing") {
		t.Fatalf("a missing image = %d %s", response.Code, response.Body.String())
	}
	f.images.held[image] = true

	// A deploy key carries the administrator's role and may promote, as it
	// may promote staging.
	var adminID int64
	if err := f.s.Store.DB.QueryRow(`SELECT id FROM users WHERE username = 'staging-admin'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	admin, err := f.s.Auth.UserByID(t.Context(), adminID)
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := f.s.Auth.CreateDeployToken(t.Context(), admin, "ci", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	queued := (&client{t: t, h: f.s.Routes()}).do(http.MethodPost, promote, body, map[string]string{"Authorization": "Bearer " + key})
	var run deploy.EngineRun
	if queued.Code != http.StatusAccepted || json.Unmarshal(queued.Body.Bytes(), &run) != nil {
		t.Fatalf("deploy key promote = %d %s", queued.Code, queued.Body.String())
	}
	if run.EnvironmentID != staging || run.Operation != deploy.OperationPromote || run.SourceRevision != revision {
		t.Fatalf("queued run = %#v", run)
	}
	detail := f.auditDetail(t, "deploy.release.promote")
	for _, want := range []string{`"sourceKind":"preview"`, `"previewNumber":17`, `"approvedBy":"staging-admin"`,
		`"pullRequestUrl":"https://github.com/acme/app/pull/17"`, fmt.Sprintf(`"sourceRevision":%q`, revision), fmt.Sprintf(`"imageId":%q`, image)} {
		if !strings.Contains(detail, want) {
			t.Fatalf("audit detail lacks %s: %s", want, detail)
		}
	}
	previews := f.readonly.do(http.MethodGet, f.path("/previews"), "", nil)
	if previews.Code != http.StatusOK || !strings.Contains(previews.Body.String(), fmt.Sprintf(`"promotion":{"runId":%d`, run.ID)) {
		t.Fatalf("previews = %d %s", previews.Code, previews.Body.String())
	}

	// The fork's approval withdrawn: refused, and the words say why.
	if response := f.admin.do(http.MethodPost, f.path(fmt.Sprintf("/previews/approvals/%d/reject", approvalID)), fmt.Sprintf(`{"revision":%q}`, revision), nil); response.Code != http.StatusOK {
		t.Fatalf("reject = %d %s", response.Code, response.Body.String())
	}
	refused := f.admin.do(http.MethodPost, promote, body, nil)
	if refused.Code != http.StatusUnprocessableEntity || !strings.Contains(refused.Body.String(), "preview_promotion_unapproved") ||
		!strings.Contains(refused.Body.String(), "comes from a fork (fork/app)") {
		t.Fatalf("an unapproved fork = %d %s", refused.Code, refused.Body.String())
	}
	var success int
	var target, refusalDetail string
	if err := f.s.Store.DB.QueryRow(`SELECT success, target, detail FROM audit_log WHERE action = 'deploy.release.promote' ORDER BY id DESC LIMIT 1`).
		Scan(&success, &target, &refusalDetail); err != nil {
		t.Fatal(err)
	}
	if success != 0 || target != "PR #17" || !strings.Contains(refusalDetail, `"sourceKind":"preview"`) || !strings.Contains(refusalDetail, `"previewNumber":17`) {
		t.Fatalf("the refused promotion's audit = %d %s %s", success, target, refusalDetail)
	}
}
