package deploy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	basestore "github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

type automationFixture struct {
	store                    *basestore.Store
	automation               *AutomationStore
	projectID, environmentID int64
}

func newAutomationFixture(t *testing.T) *automationFixture {
	t.Helper()
	st, err := basestore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("a7", 32))
	if err != nil {
		t.Fatal(err)
	}
	result, err := st.DB.Exec(`INSERT INTO deploy_projects(name,repo_path,branch,compose_file,pre_command,post_command,hook_secret,hook_id,enabled,created_at,profile,updated_at) VALUES('automation','/srv/app','main','compose.yml','','','sealed','legacy',1,1,'web',1)`)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := result.LastInsertId()
	result, err = st.DB.Exec(`INSERT INTO deploy_environments(project_id,name,slug,kind,desired_revision,strategy,expected_downtime,protected,created_at,updated_at) VALUES(?,'Production','production','production',1,'blue_green',0,1,1,1)`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ := result.LastInsertId()
	for _, statement := range []string{
		`INSERT INTO deploy_sources(environment_id,revision,kind,config_json,identity_json,digest,created_at) VALUES(?,1,'git','{}','{}','source',1)`,
		`INSERT INTO deploy_build_plans(environment_id,revision,method,config_json,evidence_json,preview,digest,created_at) VALUES(?,1,'recipe','{}','[]','','build',1)`,
		`INSERT INTO deploy_runtime_plans(environment_id,revision,config_json,preview,digest,created_at) VALUES(?,1,'{}','','runtime',1)`,
	} {
		if _, err := st.DB.Exec(statement, environmentID); err != nil {
			t.Fatal(err)
		}
	}
	automation := NewAutomationStore(st, sealer)
	automation.now = func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) }
	return &automationFixture{st, automation, projectID, environmentID}
}

func TestProviderVerifierMatrixRejectsWrongSignatureAndParsesIdentity(t *testing.T) {
	secret := "provider-secret"
	cases := []struct {
		name, provider, event, delivery, signature string
		body                                       map[string]any
	}{
		{"github", "github", "push", "gh-1", "X-Hub-Signature-256", map[string]any{"ref": "refs/heads/main", "after": "abc", "repository": map[string]any{"full_name": "acme/app"}}},
		{"gitea", "gitea", "push", "gt-1", "X-Gitea-Signature", map[string]any{"ref": "refs/heads/main", "after": "abc", "repository": map[string]any{"full_name": "acme/app"}}},
		{"bitbucket", "bitbucket", "repo:push", "bb-1", "X-Hub-Signature", map[string]any{"repository": map[string]any{"full_name": "acme/app"}, "push": map[string]any{"changes": []any{map[string]any{"new": map[string]any{"name": "main", "target": map[string]any{"hash": "abc"}}}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.body)
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write(body)
			headers := http.Header{}
			headers.Set(tc.signature, "sha256="+hex.EncodeToString(mac.Sum(nil)))
			switch tc.provider {
			case "github":
				headers.Set("X-GitHub-Event", tc.event)
				headers.Set("X-GitHub-Delivery", tc.delivery)
			case "gitea":
				headers.Set("X-Gitea-Event", tc.event)
				headers.Set("X-Gitea-Delivery", tc.delivery)
			case "bitbucket":
				headers.Set("X-Event-Key", tc.event)
				headers.Set("X-Request-UUID", tc.delivery)
			}
			got, err := VerifyProvider(tc.provider, headers, body, secret)
			if err != nil || got.Repository != "acme/app" || strings.TrimPrefix(got.Ref, "refs/heads/") != "main" {
				t.Fatalf("event=%+v err=%v", got, err)
			}
			headers.Set(tc.signature, "sha256=00")
			if _, err = VerifyProvider(tc.provider, headers, body, secret); !errors.Is(err, ErrBadHookSignature) {
				t.Fatalf("wrong signature error=%v", err)
			}
		})
	}
	body := []byte(`{"object_kind":"push","ref":"refs/heads/main","checkout_sha":"abc","project":{"path_with_namespace":"acme/app"}}`)
	headers := http.Header{}
	headers.Set("X-Gitlab-Token", "provider-secret")
	headers.Set("X-Gitlab-Event-UUID", "gl-1")
	headers.Set("X-Gitlab-Event", "Push Hook")
	got, err := VerifyProvider("gitlab", headers, body, secret)
	if err != nil || got.Repository != "acme/app" {
		t.Fatalf("gitlab=%+v err=%v", got, err)
	}
}

func TestDeliveryReplayAndWatchPathSimulation(t *testing.T) {
	f := newAutomationFixture(t)
	created, err := f.automation.CreateTrigger(context.Background(), f.projectID, f.environmentID, TriggerWrite{Name: "GitHub", Kind: TriggerGitHub, Provider: "github", Enabled: true, Config: TriggerConfig{Repository: "acme/app", Ref: "main", WatchInclude: []string{"services/api/**"}, WatchExclude: []string{"**/*.md"}}})
	if err != nil {
		t.Fatal(err)
	}
	if created.Secret == "" || created.Trigger.HookID == "" {
		t.Fatal("one-time credentials missing")
	}
	if !MatchWatchPaths([]string{"services/api/main.go"}, created.Trigger.Config.WatchInclude, created.Trigger.Config.WatchExclude) {
		t.Fatal("matching source path ignored")
	}
	if MatchWatchPaths([]string{"docs/readme.md"}, created.Trigger.Config.WatchInclude, created.Trigger.Config.WatchExclude) {
		t.Fatal("unrelated path matched")
	}
	event := ProviderEvent{DeliveryID: "same", Event: "push", Repository: "acme/app", Ref: "main"}
	if err = f.automation.RecordDelivery(context.Background(), &created.Trigger, event, []byte("one"), "accepted", "", 4); err != nil {
		t.Fatal(err)
	}
	if err = f.automation.RecordDelivery(context.Background(), &created.Trigger, event, []byte("two"), "accepted", "", 5); !errors.Is(err, ErrDeliveryReplayed) {
		t.Fatalf("replay error=%v", err)
	}
}

func TestPreviewLifecycleIsIsolatedFromProduction(t *testing.T) {
	f := newAutomationFixture(t)
	sealed, _ := f.automation.sealer.Seal("preview-secret")
	_, _ = f.store.DB.Exec(`INSERT INTO deploy_variable_revisions(environment_id,key,revision,sensitivity,scopes,value_enc,value_digest,active,created_by,created_at) VALUES(?,'TOKEN',1,'secret','runtime',?,'digest',1,'admin',1)`, f.environmentID, sealed)
	_, _ = f.store.DB.Exec(`INSERT INTO deploy_dependencies(environment_id,release_id,kind,ownership,resource_kind,resource_id,config_json,created_at) VALUES(?,0,'volume','managed','docker_volume','production-data','{}',1),(?,0,'database','linked','database','shared-db','{}',1)`, f.environmentID, f.environmentID)
	created, err := f.automation.CreateTrigger(context.Background(), f.projectID, f.environmentID, TriggerWrite{Name: "Previews", Kind: TriggerGitHub, Provider: "github", Enabled: true, Config: TriggerConfig{Repository: "acme/app", Ref: "main", Preview: true, PreviewQuota: 1}})
	if err != nil {
		t.Fatal(err)
	}
	event := ProviderEvent{PreviewNumber: 42, PreviewRef: "refs/pull/42/head", Revision: strings.Repeat("a", 40)}
	approvePreviewFixture(t, f, &created.Trigger, event)
	opened, newPreview, err := f.automation.EnsurePreview(context.Background(), &created.Trigger, event)
	if err != nil || !newPreview || opened.EnvironmentID == f.environmentID {
		t.Fatalf("open=%+v new=%v err=%v", opened, newPreview, err)
	}
	var productionKind string
	if err = f.store.DB.QueryRow(`SELECT kind FROM deploy_environments WHERE id=?`, f.environmentID).Scan(&productionKind); err != nil || productionKind != "production" {
		t.Fatalf("production changed: %q %v", productionKind, err)
	}
	var variables, managed, linked int
	_ = f.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_variable_revisions WHERE environment_id=?`, opened.EnvironmentID).Scan(&variables)
	_ = f.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_dependencies WHERE environment_id=? AND ownership='managed'`, opened.EnvironmentID).Scan(&managed)
	_ = f.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_dependencies WHERE environment_id=? AND ownership='linked'`, opened.EnvironmentID).Scan(&linked)
	if variables != 0 || managed != 0 || linked != 0 {
		t.Fatalf("preview inheritance variables=%d managed=%d linked=%d", variables, managed, linked)
	}
	event.Revision = strings.Repeat("b", 40)
	approvePreviewFixture(t, f, &created.Trigger, event)
	updated, createdAgain, err := f.automation.EnsurePreview(context.Background(), &created.Trigger, event)
	if err != nil || createdAgain || updated.EnvironmentID != opened.EnvironmentID {
		t.Fatalf("update=%+v created=%v err=%v", updated, createdAgain, err)
	}
	var desired int
	var identity string
	if err = f.store.DB.QueryRow(`SELECT desired_revision FROM deploy_environments WHERE id=?`, opened.EnvironmentID).Scan(&desired); err != nil {
		t.Fatal(err)
	}
	if err = f.store.DB.QueryRow(`SELECT identity_json FROM deploy_sources WHERE environment_id=? AND revision=?`, opened.EnvironmentID, desired).Scan(&identity); err != nil {
		t.Fatal(err)
	}
	if desired != 2 || !strings.Contains(identity, event.Revision) {
		t.Fatalf("updated preview revision=%d identity=%s", desired, identity)
	}
	event.PreviewNumber, event.PreviewRef = 43, "refs/pull/43/head"
	approvePreviewFixture(t, f, &created.Trigger, event)
	if _, _, err = f.automation.EnsurePreview(context.Background(), &created.Trigger, event); !errors.Is(err, ErrPreviewQuota) {
		t.Fatalf("quota error=%v", err)
	}
	closed, _, err := f.automation.EnsurePreview(context.Background(), &created.Trigger, ProviderEvent{PreviewNumber: 42, PreviewClosed: true})
	if err != nil || closed.State != "closed" {
		t.Fatalf("close=%+v err=%v", closed, err)
	}
	if err = f.automation.ArchiveClosedPreview(context.Background(), closed.ID); err != nil {
		t.Fatal(err)
	}
	var archived int
	if err = f.store.DB.QueryRow(`SELECT archived_at FROM deploy_environments WHERE id=?`, opened.EnvironmentID).Scan(&archived); err != nil || archived == 0 {
		t.Fatalf("preview not archived: %d %v", archived, err)
	}
}

func TestScheduleClaimAdvancesBeforeDispatch(t *testing.T) {
	f := newAutomationFixture(t)
	schedule, err := f.automation.CreateSchedule(context.Background(), f.projectID, f.environmentID, ScheduleWrite{Name: "nightly", Expression: "0 12 * * *", Timezone: "UTC", Enabled: true, Steps: []ScheduleStep{{Action: "deploy", Config: json.RawMessage(`{}`), Required: true}, {Action: "backup", Config: json.RawMessage(`{"jobId":4}`), Required: true}}})
	if err != nil {
		t.Fatal(err)
	}
	f.automation.now = func() time.Time { return time.Date(2026, 9, 8, 12, 0, 1, 0, time.UTC) }
	due, err := f.automation.ClaimDueSchedules(context.Background(), 20)
	if err != nil || len(due) != 1 {
		t.Fatalf("due=%+v err=%v", due, err)
	}
	if due[0].Schedule.ID != schedule.ID || len(due[0].Schedule.Steps) != 2 {
		t.Fatalf("dispatch=%+v", due[0])
	}
	again, err := f.automation.ClaimDueSchedules(context.Background(), 20)
	if err != nil || len(again) != 0 {
		t.Fatalf("duplicate claim=%+v err=%v", again, err)
	}
}

// Archiving a project must stop its schedules from firing: ClaimDueSchedules
// only excludes an archived environment, not an archived project, so without
// Store.Archive disabling them directly every occurrence would keep enqueuing
// and failing at environment_not_found. Unarchive restores the project but
// deliberately leaves the schedule disabled — the operator turns it back on.
func TestArchiveDisablesSchedulesAndUnarchiveLeavesThemDisabled(t *testing.T) {
	ctx := context.Background()
	f := newAutomationFixture(t)
	schedule, err := f.automation.CreateSchedule(ctx, f.projectID, f.environmentID, ScheduleWrite{
		Name: "nightly", Expression: "0 2 * * *", Timezone: "UTC", Enabled: true,
		Steps: []ScheduleStep{{Action: "restart", Config: json.RawMessage(`{}`), Required: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !schedule.Enabled {
		t.Fatalf("schedule not enabled before archive: %#v", schedule)
	}
	sealer, err := auth.NewSealer(strings.Repeat("a7", 32))
	if err != nil {
		t.Fatal(err)
	}
	projects := NewStore(f.store, sealer, []string{t.TempDir()})
	if _, err := projects.Archive(ctx, f.projectID); err != nil {
		t.Fatal(err)
	}
	schedules, err := f.automation.ListSchedules(ctx, f.projectID, f.environmentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(schedules) != 1 || schedules[0].Enabled {
		t.Fatalf("schedule after archive = %#v, want disabled", schedules)
	}
	due, err := f.automation.ClaimDueSchedules(ctx, 20)
	if err != nil || len(due) != 0 {
		t.Fatalf("disabled schedule was claimed: due=%+v err=%v", due, err)
	}
	// Belt and suspenders: even a schedule some other path re-enabled while
	// the project stayed archived must not be claimable — ClaimDueSchedules
	// joins deploy_projects for exactly this case.
	if _, err := f.store.DB.Exec(`UPDATE deploy_schedules SET enabled=1,next_run_at=? WHERE id=?`,
		f.automation.now().Add(-time.Minute).Unix(), schedule.ID); err != nil {
		t.Fatal(err)
	}
	due, err = f.automation.ClaimDueSchedules(ctx, 20)
	if err != nil || len(due) != 0 {
		t.Fatalf("archived project schedule was claimed: due=%+v err=%v", due, err)
	}
	// Restore the pre-unarchive state Archive itself would have left: the
	// schedule disabled, not re-enabled by this test's own defensive check.
	if _, err := f.store.DB.Exec(`UPDATE deploy_schedules SET enabled=0 WHERE id=?`, schedule.ID); err != nil {
		t.Fatal(err)
	}

	project, err := projects.Unarchive(ctx, f.projectID)
	if err != nil {
		t.Fatal(err)
	}
	if project.ArchivedAt != nil || project.Name != "automation" {
		t.Fatalf("project after unarchive = %#v", project)
	}
	schedules, err = f.automation.ListSchedules(ctx, f.projectID, f.environmentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(schedules) != 1 || schedules[0].Enabled {
		t.Fatalf("schedule after unarchive = %#v, want still disabled", schedules)
	}
}

// TriggerDeliveries reads the same rows RecordDelivery/FinishDelivery write,
// newest first, and refuses a trigger id from another project or environment.
// RotateTriggerSecret replaces the sealed secret without disturbing anything
// else about the trigger.
func TestTriggerDeliveriesAndSecretRotation(t *testing.T) {
	ctx := context.Background()
	f := newAutomationFixture(t)
	created, err := f.automation.CreateTrigger(ctx, f.projectID, f.environmentID, TriggerWrite{
		Name: "GitHub", Kind: TriggerGitHub, Provider: "github", Enabled: true,
		Config: TriggerConfig{Repository: "acme/app", Ref: "main"},
	})
	if err != nil {
		t.Fatal(err)
	}
	trigger := &created.Trigger
	first := ProviderEvent{DeliveryID: "one", Event: "push", Repository: "acme/app", Ref: "refs/heads/main"}
	if err := f.automation.RecordDelivery(ctx, trigger, first, []byte("body-1"), "processing", "", 0); err != nil {
		t.Fatal(err)
	}
	if err := f.automation.FinishDelivery(ctx, trigger, "one", "accepted", "", 42); err != nil {
		t.Fatal(err)
	}
	second := ProviderEvent{DeliveryID: "two", Event: "push", Repository: "acme/app", Ref: "refs/heads/main"}
	if err := f.automation.RecordDelivery(ctx, trigger, second, []byte("body-2"), "rejected", "wrong_ref", 0); err != nil {
		t.Fatal(err)
	}

	deliveries, err := f.automation.TriggerDeliveries(ctx, f.projectID, f.environmentID, trigger.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 2 || deliveries[0].DeliveryID != "two" || deliveries[0].Decision != "rejected" ||
		deliveries[0].Reason != "wrong_ref" || deliveries[1].DeliveryID != "one" ||
		deliveries[1].Decision != "accepted" || deliveries[1].RunID != 42 {
		t.Fatalf("deliveries = %#v", deliveries)
	}
	if _, err := f.automation.TriggerDeliveries(ctx, f.projectID+1, f.environmentID, trigger.ID, 50); !errors.Is(err, ErrTriggerNotFound) {
		t.Fatalf("deliveries for another project = %v, want ErrTriggerNotFound", err)
	}

	secret, err := f.automation.RotateTriggerSecret(ctx, f.projectID, f.environmentID, trigger.ID)
	if err != nil {
		t.Fatal(err)
	}
	if secret == "" || secret == created.Secret {
		t.Fatalf("rotated secret = %q, want a new non-empty value distinct from %q", secret, created.Secret)
	}
	_, opened, err := f.automation.TriggerByID(ctx, f.projectID, trigger.ID)
	if err != nil || opened != secret {
		t.Fatalf("stored secret after rotation = %q, %v, want %q", opened, err, secret)
	}
	if _, err := f.automation.RotateTriggerSecret(ctx, f.projectID, f.environmentID, trigger.ID+1); !errors.Is(err, ErrTriggerNotFound) {
		t.Fatalf("rotate for an unknown trigger = %v, want ErrTriggerNotFound", err)
	}
}

// Unarchive refuses to restore a name another active project has since taken.
func TestUnarchiveRefusesATakenName(t *testing.T) {
	ctx := context.Background()
	f := newAutomationFixture(t)
	sealer, err := auth.NewSealer(strings.Repeat("a7", 32))
	if err != nil {
		t.Fatal(err)
	}
	projects := NewStore(f.store, sealer, []string{t.TempDir()})
	if _, err := projects.Archive(ctx, f.projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_projects(name, repo_path, hook_secret, hook_id, created_at) VALUES('automation', '/srv/new', 'sealed', 'new-hook', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := projects.Unarchive(ctx, f.projectID); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("unarchive over a taken name = %v, want ErrNameTaken", err)
	}
}

func TestScheduleTimezoneDSTAndInvalidExpressions(t *testing.T) {
	springAfter := time.Date(2026, 3, 8, 6, 55, 0, 0, time.UTC)
	next, err := NextCron("30 2 * * *", "America/New_York", springAfter)
	if err != nil {
		t.Fatal(err)
	}
	if got := next.In(mustLocation(t, "America/New_York")); got.Day() != 9 || got.Hour() != 2 || got.Minute() != 30 {
		t.Fatalf("spring next=%s", got)
	}
	fallAfter := time.Date(2026, 11, 1, 4, 55, 0, 0, time.UTC)
	first, err := NextCron("30 1 * * *", "America/New_York", fallAfter)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NextCron("30 1 * * *", "America/New_York", first)
	if err != nil {
		t.Fatal(err)
	}
	if second.Sub(first) != time.Hour {
		t.Fatalf("fall occurrences=%s / %s", first, second)
	}
	for _, bad := range []string{"", "61 * * * *", "* * *"} {
		if _, err := NextCron(bad, "UTC", time.Now()); err == nil {
			t.Fatalf("invalid cron accepted: %q", bad)
		}
	}
	if _, err := NextCron("* * * * *", "Mars/Olympus", time.Now()); err == nil {
		t.Fatal("invalid timezone accepted")
	}
}
func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestSignedNotificationPersistsOnlyResponseClass(t *testing.T) {
	f := newAutomationFixture(t)
	secret := "outbound-secret"
	var receivedSignature string
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSignature = r.Header.Get("X-JD-Signature-256")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("reflected " + secret + " private response"))
	}))
	defer remote.Close()
	channel, shown, err := f.automation.CreateNotificationChannel(context.Background(), NotificationWrite{Name: "release events", URL: remote.URL, Secret: secret, Events: []string{"run.succeeded"}, Enabled: true})
	if err != nil || shown != secret {
		t.Fatalf("channel=%+v shown=%q err=%v", channel, shown, err)
	}
	envelope := NotificationEnvelope{Event: "run.succeeded", RunID: 44, ProjectID: f.projectID, EnvironmentID: f.environmentID, State: "succeeded", SentAt: time.Now().UTC()}
	if err = f.automation.DeliverNotification(context.Background(), remote.Client(), channel.ID, envelope); err == nil {
		t.Fatal("500 delivery reported success")
	}
	body, _ := json.Marshal(envelope)
	if receivedSignature != SignNotification(body, secret) {
		t.Fatalf("signature=%q", receivedSignature)
	}
	var status, responseClass string
	if err = f.store.DB.QueryRow(`SELECT status,response_class FROM deploy_notification_deliveries WHERE channel_id=?`, channel.ID).Scan(&status, &responseClass); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || responseClass != "5xx" {
		t.Fatalf("history=%s/%s", status, responseClass)
	}
	var leaks int
	if err = f.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_notification_deliveries WHERE response_class LIKE '%' || ? || '%'`, secret).Scan(&leaks); err != nil || leaks != 0 {
		t.Fatalf("response secret persisted: count=%d err=%v", leaks, err)
	}
}
