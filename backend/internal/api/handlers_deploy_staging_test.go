package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// fakePromotionImages stands in for Docker's image store: it holds exactly
// the image ids it was given.
type fakePromotionImages struct{ held map[string]bool }

func (f *fakePromotionImages) LocalImage(_ context.Context, reference string) (deploy.ResolvedImage, error) {
	if !f.held[reference] {
		return deploy.ResolvedImage{}, errors.New("No such image: " + reference)
	}
	return deploy.ResolvedImage{Reference: reference, Digest: reference, ConfigDigest: reference}, nil
}

type stagingRouteFixture struct {
	s          *Server
	admin      *client
	limited    *client
	readonly   *client
	token      string
	project    *deploy.Project
	production int64
	images     *fakePromotionImages
}

func newStagingRouteFixture(t *testing.T) *stagingRouteFixture {
	t.Helper()
	s := testServer(t)
	routes := s.Routes()
	f := &stagingRouteFixture{
		s:        s,
		admin:    &client{t: t, h: routes, cookie: signInAs(t, s, "staging-admin", auth.RoleAdmin)},
		limited:  &client{t: t, h: routes, cookie: signInAs(t, s, "staging-operator", auth.RoleLimited)},
		readonly: &client{t: t, h: routes, cookie: signInAs(t, s, "staging-reader", auth.RoleReadOnly)},
		images:   &fakePromotionImages{held: map[string]bool{}},
	}
	f.project = createLegacyDeploymentFixture(t, s, "staging-routes")
	f.production = convertToNormalizedDeploymentFixture(t, s, f.project)
	s.modules.promotionImages = f.images
	var adminID int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM users WHERE username = 'staging-admin'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	adminUser, err := s.Auth.UserByID(t.Context(), adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, f.token, err = s.Auth.CreateAPIToken(t.Context(), adminUser, "staging-token", auth.RoleAdmin, time.Hour); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *stagingRouteFixture) path(suffix string) string {
	return fmt.Sprintf("/api/v1/deploy/%d%s", f.project.ID, suffix)
}

func (f *stagingRouteFixture) auditDetail(t *testing.T, action string) string {
	t.Helper()
	var detail string
	if err := f.s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action = ? ORDER BY id DESC LIMIT 1`, action).Scan(&detail); err != nil {
		t.Fatalf("no %s audit entry: %v", action, err)
	}
	return detail
}

// addStaging adds staging through the route and leaves one live staging
// release whose image is held, which is what a successful staging deploy
// leaves.
func (f *stagingRouteFixture) addStaging(t *testing.T) (int64, *deploy.ReleaseWithArtifacts) {
	t.Helper()
	response := f.admin.do(http.MethodPost, f.path("/environments"), `{"kind":"staging"}`, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("add staging = %d %s", response.Code, response.Body.String())
	}
	var copied deploy.StagingCopy
	if err := json.Unmarshal(response.Body.Bytes(), &copied); err != nil {
		t.Fatal(err)
	}
	run := f.admin.do(http.MethodPost, f.path(fmt.Sprintf("/environments/%d/runs", copied.Environment.ID)), `{"operation":"deploy"}`, nil)
	if run.Code != http.StatusAccepted {
		t.Fatalf("staging deploy = %d %s", run.Code, run.Body.String())
	}
	var queued deploy.EngineRun
	if err := json.Unmarshal(run.Body.Bytes(), &queued); err != nil {
		t.Fatal(err)
	}
	release := activateSyntheticNormalizedRelease(t, f.s, queued, "staging")
	f.images.held["sha256:"+strings.Repeat("c", 64)] = true
	return copied.Environment.ID, release
}

func TestStagingRoutesNeedAnAdministratorInASessionAndAreAudited(t *testing.T) {
	f := newStagingRouteFixture(t)
	body := `{"kind":"staging","domain":"staging.example.test"}`
	if response := f.readonly.do(http.MethodPost, f.path("/environments"), body, nil); response.Code != http.StatusForbidden {
		t.Fatalf("readonly add staging = %d %s", response.Code, response.Body.String())
	}
	if response := f.limited.do(http.MethodPost, f.path("/environments"), body, nil); response.Code != http.StatusForbidden {
		t.Fatalf("service.control add staging = %d %s", response.Code, response.Body.String())
	}
	if response := (&client{t: t, h: f.s.Routes()}).do(http.MethodPost, f.path("/environments"), body,
		map[string]string{"Authorization": "Bearer " + f.token}); response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), "session_required") {
		t.Fatalf("token add staging = %d %s", response.Code, response.Body.String())
	}
	if response := f.admin.do(http.MethodPost, f.path("/environments"), `{"kind":"preview"}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("adding a preview here = %d %s", response.Code, response.Body.String())
	}
	if response := f.admin.do(http.MethodPost, f.path("/environments"), `{"kind":"staging","ref":"staging"}`, nil); response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "ref_not_applicable") {
		t.Fatalf("a branch for a local checkout = %d %s", response.Code, response.Body.String())
	}
	created := f.admin.do(http.MethodPost, f.path("/environments"), body, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("add staging = %d %s", created.Code, created.Body.String())
	}
	var copied deploy.StagingCopy
	if err := json.Unmarshal(created.Body.Bytes(), &copied); err != nil || copied.Environment.Kind != deploy.EnvironmentStaging {
		t.Fatalf("created = %s, %v", created.Body.String(), err)
	}
	detail := f.auditDetail(t, "deploy.environment.create")
	if !strings.Contains(detail, `"domain":"staging.example.test"`) || !strings.Contains(detail, `"copiedSensitiveValues":false`) {
		t.Fatalf("audit detail = %s", detail)
	}
	if response := f.admin.do(http.MethodPost, f.path("/environments"), body, nil); response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), "staging_exists") {
		t.Fatalf("second staging = %d %s", response.Code, response.Body.String())
	}
	listed := f.readonly.do(http.MethodGet, f.path("/environments"), "", nil)
	var environments []deploy.EnvironmentSummary
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &environments) != nil || len(environments) != 2 {
		t.Fatalf("environments = %d %s", listed.Code, listed.Body.String())
	}
	summary := f.readonly.do(http.MethodGet, f.path(fmt.Sprintf("?environment=%d", copied.Environment.ID)), "", nil)
	if summary.Code != http.StatusOK || !strings.Contains(summary.Body.String(), fmt.Sprintf(`"environmentId":%d`, copied.Environment.ID)) ||
		!strings.Contains(summary.Body.String(), `"environmentKind":"staging"`) {
		t.Fatalf("staging summary = %d %s", summary.Code, summary.Body.String())
	}

	remove := f.path(fmt.Sprintf("/environments/%d", copied.Environment.ID))
	if response := f.limited.do(http.MethodDelete, remove, `{}`, nil); response.Code != http.StatusForbidden {
		t.Fatalf("limited remove staging = %d %s", response.Code, response.Body.String())
	}
	if response := f.admin.do(http.MethodDelete, f.path(fmt.Sprintf("/environments/%d", f.production)), `{}`, nil); response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), "not_staging") {
		t.Fatalf("removing production here = %d %s", response.Code, response.Body.String())
	}
	removed := f.admin.do(http.MethodDelete, remove, `{"volumes":true}`, nil)
	if removed.Code != http.StatusOK {
		t.Fatalf("remove staging = %d %s", removed.Code, removed.Body.String())
	}
	if detail := f.auditDetail(t, "deploy.environment.remove"); !strings.Contains(detail, `"volumes":true`) {
		t.Fatalf("removal audit = %s", detail)
	}
	listed = f.readonly.do(http.MethodGet, f.path("/environments"), "", nil)
	if json.Unmarshal(listed.Body.Bytes(), &environments) != nil || len(environments) != 1 {
		t.Fatalf("environments after removal = %s", listed.Body.String())
	}
}

func TestPromotionNeedsServiceControlRefusesAMissingImageAndIsAudited(t *testing.T) {
	f := newStagingRouteFixture(t)
	_, release := f.addStaging(t)
	promote := f.path(fmt.Sprintf("/environments/%d/promote", f.production))
	body := fmt.Sprintf(`{"releaseId":%d}`, release.Release.ID)

	preview := f.readonly.do(http.MethodGet, f.path(fmt.Sprintf("/environments/%d/promotion?releaseId=%d", f.production, release.Release.ID)), "", nil)
	var reading deploy.PromotionPreview
	if preview.Code != http.StatusOK || json.Unmarshal(preview.Body.Bytes(), &reading) != nil {
		t.Fatalf("promotion preview = %d %s", preview.Code, preview.Body.String())
	}
	if !reading.Source.ImagePresent || reading.Source.ImageID != "sha256:"+strings.Repeat("c", 64) || len(reading.Blockers) != 0 ||
		reading.Detail.Status != "available" || reading.Target.EnvironmentID != f.production || reading.Target.Rehearsal {
		t.Fatalf("promotion preview = %#v", reading)
	}
	// Production's rehearsal terms are the confirmation's: a promotion
	// rehearses production's release tasks exactly as a deploy does.
	if response := f.admin.do(http.MethodPut, f.path(fmt.Sprintf("/environments/%d/rehearsal", f.production)),
		`{"enabled":true,"whenUnavailable":"block","restoreTimeoutSeconds":600,"maxBackupMb":512}`, nil); response.Code != http.StatusOK {
		t.Fatalf("rehearsal terms = %d %s", response.Code, response.Body.String())
	}
	rehearsed := f.readonly.do(http.MethodGet, f.path(fmt.Sprintf("/environments/%d/promotion?releaseId=%d", f.production, release.Release.ID)), "", nil)
	if rehearsed.Code != http.StatusOK || json.Unmarshal(rehearsed.Body.Bytes(), &reading) != nil || !reading.Target.Rehearsal {
		t.Fatalf("promotion preview with rehearsal on = %d %s", rehearsed.Code, rehearsed.Body.String())
	}

	if response := f.readonly.do(http.MethodPost, promote, body, nil); response.Code != http.StatusForbidden {
		t.Fatalf("readonly promote = %d %s", response.Code, response.Body.String())
	}
	f.images.held["sha256:"+strings.Repeat("c", 64)] = false
	missing := f.limited.do(http.MethodPost, promote, body, nil)
	if missing.Code != http.StatusConflict || !strings.Contains(missing.Body.String(), "promotion_image_missing") {
		t.Fatalf("promoting a missing image = %d %s", missing.Code, missing.Body.String())
	}
	if preview := f.readonly.do(http.MethodGet, f.path(fmt.Sprintf("/environments/%d/promotion?releaseId=%d", f.production, release.Release.ID)), "", nil); !strings.Contains(preview.Body.String(), "promotion_image_missing") {
		t.Fatalf("the confirmation does not say the image is gone: %s", preview.Body.String())
	}
	f.images.held["sha256:"+strings.Repeat("c", 64)] = true

	stagingPromote := f.path(fmt.Sprintf("/environments/%d/promote", release.Release.EnvironmentID))
	if response := f.limited.do(http.MethodPost, stagingPromote, body, nil); response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), "promotion_target") {
		t.Fatalf("promoting into staging = %d %s", response.Code, response.Body.String())
	}

	// A service.control principal deploys production, so it may promote
	// into it: that is the same change through the same checks.
	accepted := f.limited.do(http.MethodPost, promote, body, map[string]string{"Idempotency-Key": "promote-once"})
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("promote = %d %s", accepted.Code, accepted.Body.String())
	}
	var run deploy.EngineRun
	if err := json.Unmarshal(accepted.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.Operation != deploy.OperationPromote || run.EnvironmentID != f.production || run.Trigger != deploy.TriggerManual {
		t.Fatalf("promotion run = %#v", run)
	}
	steps, _ := f.s.modules.deployRuns.Steps(t.Context(), run.ID)
	for _, step := range steps {
		if step.Key == deploy.StepBuildArtifact || step.Key == deploy.StepPrepareContext || step.Key == deploy.StepAcquireSource {
			t.Fatalf("a promotion has a build step: %#v", steps)
		}
	}
	again := f.limited.do(http.MethodPost, promote, body, map[string]string{"Idempotency-Key": "promote-once"})
	var replay deploy.EngineRun
	if again.Code != http.StatusAccepted || json.Unmarshal(again.Body.Bytes(), &replay) != nil || replay.ID != run.ID {
		t.Fatalf("idempotent promote = %d %s", again.Code, again.Body.String())
	}
	detail := f.auditDetail(t, "deploy.release.promote")
	if !strings.Contains(detail, `"imageId":"sha256:`+strings.Repeat("c", 64)+`"`) || !strings.Contains(detail, fmt.Sprintf(`"runId":%d`, run.ID)) ||
		!strings.Contains(detail, `"fromReleaseNumber":1`) {
		t.Fatalf("promotion audit = %s", detail)
	}
}

func TestPromotionOnlyIsKeptWhenOmittedAndRefusesDirectBuilds(t *testing.T) {
	f := newStagingRouteFixture(t)
	policyPath := f.path(fmt.Sprintf("/environments/%d/git-policy", f.production))
	if response := f.admin.do(http.MethodPut, policyPath, `{"automatic":true,"watchInclude":[],"watchExclude":[],"commitStatuses":true,"promotionOnly":true,"revision":0}`, nil); response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), "staging_required") {
		t.Fatalf("promotion-only without staging = %d %s", response.Code, response.Body.String())
	}
	staging, release := f.addStaging(t)
	saved := f.admin.do(http.MethodPut, policyPath, `{"automatic":true,"watchInclude":[],"watchExclude":[],"commitStatuses":true,"promotionOnly":true,"revision":0}`, nil)
	if saved.Code != http.StatusOK || !strings.Contains(saved.Body.String(), `"promotionOnly":true`) {
		t.Fatalf("promotion-only = %d %s", saved.Code, saved.Body.String())
	}
	if detail := f.auditDetail(t, "deploy.git.policy"); !strings.Contains(detail, `"promotionOnly":true`) {
		t.Fatalf("policy audit = %s", detail)
	}
	// An editor that predates the field saves without it and must not turn
	// promotion-only off by leaving it out.
	kept := f.admin.do(http.MethodPut, policyPath, `{"automatic":false,"watchInclude":[],"watchExclude":[],"commitStatuses":true,"revision":1}`, nil)
	if kept.Code != http.StatusOK || !strings.Contains(kept.Body.String(), `"promotionOnly":true`) {
		t.Fatalf("omitted promotionOnly = %d %s", kept.Code, kept.Body.String())
	}
	runs := f.path(fmt.Sprintf("/environments/%d/runs", f.production))
	for _, operation := range []string{"deploy", "force_build"} {
		response := f.admin.do(http.MethodPost, runs, fmt.Sprintf(`{"operation":%q}`, operation), nil)
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "promotion_only") {
			t.Fatalf("%s under promotion-only = %d %s", operation, response.Code, response.Body.String())
		}
	}
	watch := f.readonly.do(http.MethodGet, f.path(fmt.Sprintf("/environments/%d/git-watch", f.production)), "", nil)
	if watch.Code != http.StatusOK || !strings.Contains(watch.Body.String(), `"promotionOnly":true`) {
		t.Fatalf("git watch = %d %s", watch.Code, watch.Body.String())
	}
	stagingBuild := f.admin.do(http.MethodPost, f.path(fmt.Sprintf("/environments/%d/runs", staging)), `{"operation":"deploy"}`, nil)
	var stagingRun deploy.EngineRun
	if stagingBuild.Code != http.StatusAccepted || json.Unmarshal(stagingBuild.Body.Bytes(), &stagingRun) != nil {
		t.Fatalf("staging still builds: %d %s", stagingBuild.Code, stagingBuild.Body.String())
	}
	// The unfiltered list keeps answering every environment; production's
	// pages ask for its scope, which leaves staging's builds out.
	listed := func(query string) []int64 {
		t.Helper()
		response := f.readonly.do(http.MethodGet, f.path("/runs?view=engine"+query), "", nil)
		var page struct {
			Runs []deploy.EngineRun `json:"runs"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil {
			t.Fatalf("runs%s = %d %s", query, response.Code, response.Body.String())
		}
		ids := []int64{}
		for _, run := range page.Runs {
			ids = append(ids, run.ID)
		}
		return ids
	}
	if ids := listed(""); !slices.Contains(ids, stagingRun.ID) {
		t.Fatalf("the unfiltered run list lost staging's run: %v", ids)
	}
	if ids := listed("&scope=production"); slices.Contains(ids, stagingRun.ID) {
		t.Fatalf("production's run list shows staging's run: %v", ids)
	}
	if ids := listed(fmt.Sprintf("&environment=%d", staging)); !slices.Contains(ids, stagingRun.ID) {
		t.Fatalf("staging's run list lost its run: %v", ids)
	}
	if response := f.readonly.do(http.MethodGet, f.path("/runs?view=engine&scope=everything"), "", nil); response.Code != http.StatusBadRequest {
		t.Fatalf("unknown scope = %d", response.Code)
	}
	if response := f.admin.do(http.MethodDelete, f.path(fmt.Sprintf("/environments/%d", staging)), `{}`, nil); response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), "promotion_only_requires_staging") {
		t.Fatalf("removing staging under promotion-only = %d %s", response.Code, response.Body.String())
	}
	if response := f.limited.do(http.MethodPost, f.path(fmt.Sprintf("/environments/%d/promote", f.production)),
		fmt.Sprintf(`{"releaseId":%d}`, release.Release.ID), nil); response.Code != http.StatusAccepted {
		t.Fatalf("promotion under promotion-only = %d %s", response.Code, response.Body.String())
	}
}
