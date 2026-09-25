package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

func pullRequestEvent(number int, revision string) ProviderEvent {
	return ProviderEvent{
		Event: "pull_request", Action: "synchronize", Repository: "acme/app",
		PreviewNumber: number, PreviewRef: "refs/pull/" + strconv.Itoa(number) + "/head", Revision: revision,
		HeadRef: "feature", HeadRepository: "acme/app", Author: "dev", BaseRef: "main",
	}
}

func TestEnsurePullRequestTriggerCreatesOnceAndReusesTheRepositoryTrigger(t *testing.T) {
	ctx := t.Context()
	f := newAutomationFixture(t)
	// A webhook trigger that publishes on its own domain belongs to that flow
	// and is never borrowed for dashboard previews.
	if _, err := f.automation.CreateTrigger(ctx, f.projectID, f.environmentID, TriggerWrite{
		Name: "Public previews", Kind: TriggerGitHub, Provider: "github", Enabled: true,
		Config: TriggerConfig{Repository: "acme/app", Ref: "main", Preview: true, PreviewDomain: "pr-{number}.example.test"},
	}); err != nil {
		t.Fatal(err)
	}
	created, err := f.automation.EnsurePullRequestTrigger(ctx, f.projectID, f.environmentID, "acme/app", "main")
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "Pull requests" || created.Provider != "github" || !created.Enabled || !created.Config.Preview ||
		created.Config.PreviewQuota != 5 || created.Config.PreviewDomain != "" || created.Config.Delivery != "" ||
		created.Config.Repository != "acme/app" || created.Config.Ref != "main" || !reflect.DeepEqual(created.Config.Events, []string{"pull_request"}) {
		t.Fatalf("created trigger = %#v", created)
	}
	again, err := f.automation.EnsurePullRequestTrigger(ctx, f.projectID, f.environmentID, "Acme/App.git", "main")
	if err != nil || again.ID != created.ID {
		t.Fatalf("second call = %#v, %v, want trigger %d reused", again, err, created.ID)
	}
	triggers, err := f.automation.ListTriggers(ctx, f.projectID, f.environmentID)
	if err != nil || len(triggers) != 2 {
		t.Fatalf("triggers = %d, %v, want the domain trigger and one pull request trigger", len(triggers), err)
	}
	// Trigger names are unique per environment, so a disabled "Pull requests"
	// trigger is switched back on rather than shadowed by a second one.
	if _, err := f.store.DB.Exec(`UPDATE deploy_triggers SET enabled=0 WHERE id=?`, created.ID); err != nil {
		t.Fatal(err)
	}
	third, err := f.automation.EnsurePullRequestTrigger(ctx, f.projectID, f.environmentID, "acme/app", "main")
	if err != nil || third.ID != created.ID || !third.Enabled {
		t.Fatalf("disabled trigger = %#v, %v, want trigger %d re-enabled", third, err, created.ID)
	}
	if triggers, err = f.automation.ListTriggers(ctx, f.projectID, f.environmentID); err != nil || len(triggers) != 2 {
		t.Fatalf("triggers after re-enabling = %d, %v", len(triggers), err)
	}
}

// setSourceRemote records where an environment's source was cloned from,
// the way a project's source identity carries it; a preview opened
// afterwards inherits it with the rest of the source. Source rows are
// append-only inputs to releases and refuse an update, so every revision is
// replaced with the identity a clone from that remote would have recorded.
func setSourceRemote(t *testing.T, f *automationFixture, environmentID int64, remote, repository string) {
	t.Helper()
	tx, err := f.store.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`CREATE TEMP TABLE source_rewrite AS SELECT environment_id,revision,kind,config_json,credential_id,identity_json,digest,created_at FROM deploy_sources WHERE environment_id=?`, []any{environmentID}},
		{`DELETE FROM deploy_sources WHERE environment_id=?`, []any{environmentID}},
		{`INSERT INTO deploy_sources(environment_id,revision,kind,config_json,credential_id,identity_json,digest,created_at) SELECT environment_id,revision,kind,config_json,credential_id,json_set(identity_json,'$.remote',?,'$.repository',?),digest,created_at FROM source_rewrite`, []any{remote, repository}},
		{`DROP TABLE source_rewrite`, nil},
	} {
		if _, err := tx.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// Trigger names are unique per environment and kind, so when an unrelated
// GitHub trigger already holds "Pull requests" the dashboard's own carries
// the repository in its name, and is found again under it. With both names
// held, the click is refused with a sentence rather than a constraint error.
func TestEnsurePullRequestTriggerQualifiesItsNameWhenPullRequestsIsTaken(t *testing.T) {
	ctx := t.Context()
	f := newAutomationFixture(t)
	if _, err := f.automation.CreateTrigger(ctx, f.projectID, f.environmentID, TriggerWrite{
		Name: "Pull requests", Kind: TriggerGitHub, Provider: "github", Enabled: true,
		Config: TriggerConfig{Repository: "acme/app", Ref: "main", Preview: true, PreviewDomain: "pr-{number}.example.test"},
	}); err != nil {
		t.Fatal(err)
	}
	created, err := f.automation.EnsurePullRequestTrigger(ctx, f.projectID, f.environmentID, "acme/app", "main")
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "Pull requests (acme/app)" || created.Config.PreviewDomain != "" || created.Config.Repository != "acme/app" || !created.Config.Preview {
		t.Fatalf("created trigger = %#v", created)
	}
	again, err := f.automation.EnsurePullRequestTrigger(ctx, f.projectID, f.environmentID, "acme/app", "main")
	if err != nil || again.ID != created.ID {
		t.Fatalf("second call = %#v, %v, want trigger %d reused", again, err, created.ID)
	}
	if _, err := f.automation.CreateTrigger(ctx, f.projectID, f.environmentID, TriggerWrite{
		Name: "Pull requests (acme/other)", Kind: TriggerGitHub, Provider: "github", Enabled: true,
		Config: TriggerConfig{Repository: "acme/other", Ref: "main", Preview: true, PreviewDomain: "pr-{number}.other.test"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.automation.EnsurePullRequestTrigger(ctx, f.projectID, f.environmentID, "acme/other", "main"); !errors.Is(err, ErrTriggerNameTaken) {
		t.Fatalf("with both names taken = %v, want %v", err, ErrTriggerNameTaken)
	}
}

func TestRecordPreviewApprovalConfiguresAPreviewWithoutAWebhook(t *testing.T) {
	ctx := t.Context()
	f := newAutomationFixture(t)
	setSourceRemote(t, f, f.environmentID, "https://github.com/acme/app.git", "acme/app")
	trigger, err := f.automation.EnsurePullRequestTrigger(ctx, f.projectID, f.environmentID, "acme/app", "main")
	if err != nil {
		t.Fatal(err)
	}
	revision := strings.Repeat("a", 40)
	event := pullRequestEvent(7, revision)
	if _, err := f.automation.RecordPreviewApproval(ctx, trigger, event, " "); !errors.Is(err, ErrPreviewApproval) {
		t.Fatalf("approval without an actor = %v", err)
	}
	closed := event
	closed.PreviewClosed = true
	if _, err := f.automation.RecordPreviewApproval(ctx, trigger, closed, "admin"); !errors.Is(err, ErrWrongEvent) {
		t.Fatalf("approval of a closed pull request = %v", err)
	}
	approval, err := f.automation.RecordPreviewApproval(ctx, trigger, event, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if approval.State != "approved" || approval.ApprovedBy != "admin" || approval.Revision != revision ||
		approval.Generation != 1 || approval.ProviderRef != "7" || approval.HeadRef != "feature" || approval.Event.BaseRef != "main" {
		t.Fatalf("approval = %#v", approval)
	}
	preview, created, err := f.automation.EnsurePreview(ctx, trigger, event)
	if err != nil || !created {
		t.Fatalf("preview = %#v, created = %v, %v", preview, created, err)
	}

	previews, err := f.automation.ListPreviews(ctx, f.projectID)
	if err != nil || len(previews) != 1 {
		t.Fatalf("previews = %#v, %v", previews, err)
	}
	got := previews[0]
	if got.ID != preview.ID || got.ProjectID != f.projectID || got.Number != 7 || got.Revision != revision ||
		got.ApprovalState != "approved" || got.HeadRef != "feature" || got.HeadRepository != "acme/app" || got.Author != "dev" ||
		got.Origin != "" || got.Title != "" || got.HeadRevision != "" || got.VariablesCopiedRevision != "" ||
		got.LiveReleaseID != 0 || got.Address != nil || got.LastRun != nil {
		t.Fatalf("listed preview = %#v", got)
	}

	head := strings.Repeat("b", 40)
	if err := f.automation.MarkPreviewOrigin(ctx, preview.ID, PreviewOriginDashboard, "  Add checkout flow  "); err != nil {
		t.Fatal(err)
	}
	if err := f.automation.MarkPreviewOrigin(ctx, preview.ID, "webhook", "x"); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("unknown origin = %v", err)
	}
	if err := f.automation.MarkPreviewOrigin(ctx, preview.ID+100, PreviewOriginDashboard, "x"); !errors.Is(err, ErrTriggerNotFound) {
		t.Fatalf("unknown preview = %v", err)
	}
	if err := f.automation.RecordPreviewHead(ctx, preview.ID, "short"); !errors.Is(err, ErrInvalidRef) {
		t.Fatalf("malformed head = %v", err)
	}
	if err := f.automation.RecordPreviewHead(ctx, preview.ID, head); err != nil {
		t.Fatal(err)
	}
	if err := f.automation.RecordPreviewVariablesCopied(ctx, preview.ID, revision); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_preview_addresses(environment_id,kind,port,upstream_port,url,published,updated_at) VALUES(?,'tailnet',21000,34567,'https://box.tail.ts.net:21000',1,1)`, preview.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	runs := NewOrchestrationStore(f.store)
	run, _, err := runs.Enqueue(ctx, RunRequest{ProjectID: f.projectID, EnvironmentID: preview.EnvironmentID, PlanRevision: 1,
		Operation: OperationPreviewCreate, Trigger: TriggerPreview, Actor: "admin", RequestDigest: "fixture", SourceRevision: revision})
	if err != nil {
		t.Fatal(err)
	}

	byNumber, err := f.automation.PreviewByNumber(ctx, f.projectID, 7)
	if err != nil || byNumber == nil {
		t.Fatalf("preview by number = %#v, %v", byNumber, err)
	}
	if byNumber.Origin != PreviewOriginDashboard || byNumber.Title != "Add checkout flow" || byNumber.HeadRevision != head ||
		byNumber.VariablesCopiedRevision != revision || byNumber.Revision != revision {
		t.Fatalf("recorded preview = %#v", byNumber)
	}
	if byNumber.Address == nil || *byNumber.Address != (PreviewAddress{Kind: "tailnet", URL: "https://box.tail.ts.net:21000", Port: 21000, UpstreamPort: 34567, Published: true}) {
		t.Fatalf("address = %#v", byNumber.Address)
	}
	if byNumber.LastRun == nil || byNumber.LastRun.ID != run.ID || byNumber.LastRun.Operation != OperationPreviewCreate ||
		byNumber.LastRun.State != run.State || byNumber.LastRun.RunNumber != run.RunNumber || byNumber.LastRun.EndedAt != nil {
		t.Fatalf("last run = %#v, want run %d", byNumber.LastRun, run.ID)
	}
	if missing, err := f.automation.PreviewByNumber(ctx, f.projectID, 8); err != nil || missing != nil {
		t.Fatalf("unknown number = %#v, %v", missing, err)
	}
	byRepository, err := f.automation.PreviewsByRepository(ctx, "ACME/APP.git")
	if err != nil || len(byRepository) != 1 || byRepository[0].ID != preview.ID {
		t.Fatalf("previews by repository = %#v, %v", byRepository, err)
	}
	if other, err := f.automation.PreviewsByRepository(ctx, "acme/other"); err != nil || len(other) != 0 {
		t.Fatalf("previews of another repository = %#v, %v", other, err)
	}
	targets, err := f.automation.OpenPreviewTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []PreviewTarget{{PreviewID: preview.ID, TriggerID: trigger.ID, ProjectID: f.projectID, EnvironmentID: preview.EnvironmentID,
		Repository: "acme/app", Number: 7, Revision: revision, HeadRevision: head, Origin: PreviewOriginDashboard, State: "open"}}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("open targets = %#v, want %#v", targets, want)
	}

	// Closing the pull request takes it off the reconciler's list.
	if _, _, err := f.automation.EnsurePreview(ctx, trigger, ProviderEvent{PreviewNumber: 7, PreviewClosed: true}); err != nil {
		t.Fatal(err)
	}
	if targets, err = f.automation.OpenPreviewTargets(ctx); err != nil || len(targets) != 0 {
		t.Fatalf("targets after close = %#v, %v", targets, err)
	}
	if closedPreview, err := f.automation.PreviewByNumber(ctx, f.projectID, 7); err != nil || closedPreview == nil || closedPreview.State != "closed" {
		t.Fatalf("closed preview = %#v, %v", closedPreview, err)
	}
}

// The reconciler closes a preview on what github.com says about
// owner/name#N, so only a preview whose own source is that github.com
// repository is handed to it: the same name on another host, or another
// repository, is somebody else's pull request.
func TestOpenPreviewTargetsHandOverOnlyPreviewsOfTheTriggersGitHubRepository(t *testing.T) {
	ctx := t.Context()
	f := newAutomationFixture(t)
	setSourceRemote(t, f, f.environmentID, "https://github.com/acme/app.git", "acme/app")
	trigger, err := f.automation.EnsurePullRequestTrigger(ctx, f.projectID, f.environmentID, "acme/app", "main")
	if err != nil {
		t.Fatal(err)
	}
	event := pullRequestEvent(7, strings.Repeat("a", 40))
	if _, err := f.automation.RecordPreviewApproval(ctx, trigger, event, "admin"); err != nil {
		t.Fatal(err)
	}
	preview, _, err := f.automation.EnsurePreview(ctx, trigger, event)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, remote, repository string
		listed                   bool
	}{
		{"github.com", "https://github.com/acme/app.git", "acme/app", true},
		{"github.com in another spelling", "git@github.com:Acme/App.git", "Acme/App", true},
		{"GitHub Enterprise host", "https://ghe.corp/acme/app", "acme/app", false},
		{"another github.com repository", "https://github.com/acme/other.git", "acme/other", false},
		{"no remote at all", "", "", false},
	} {
		setSourceRemote(t, f, preview.EnvironmentID, test.remote, test.repository)
		targets, err := f.automation.OpenPreviewTargets(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if listed := len(targets) == 1 && targets[0].PreviewID == preview.ID; listed != test.listed || len(targets) > 1 {
			t.Fatalf("%s: targets = %#v, want listed %t", test.name, targets, test.listed)
		}
	}
}

func TestListPreviewsDescribesAWebhookPreviewDomainAddress(t *testing.T) {
	ctx := t.Context()
	f := newAutomationFixture(t)
	created, err := f.automation.CreateTrigger(ctx, f.projectID, f.environmentID, TriggerWrite{
		Name: "Public previews", Kind: TriggerGitHub, Provider: "github", Enabled: true,
		Config: TriggerConfig{Repository: "acme/app", Ref: "main", Preview: true, PreviewDomain: "pr-{number}.example.test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	event := pullRequestEvent(9, strings.Repeat("c", 40))
	approvePreviewFixture(t, f, &created.Trigger, event)
	preview, _, err := f.automation.EnsurePreview(ctx, &created.Trigger, event)
	if err != nil {
		t.Fatal(err)
	}
	previews, err := f.automation.ListPreviews(ctx, f.projectID)
	if err != nil || len(previews) != 1 || previews[0].Address == nil {
		t.Fatalf("previews = %#v, %v", previews, err)
	}
	if got := *previews[0].Address; got != (PreviewAddress{Kind: "domain", URL: "https://pr-9.example.test"}) {
		t.Fatalf("domain address before a release = %#v", got)
	}
	if _, err := f.store.DB.Exec(`UPDATE deploy_environments SET live_release_id=5 WHERE id=?`, preview.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	previews, err = f.automation.ListPreviews(ctx, f.projectID)
	if err != nil || len(previews) != 1 || previews[0].Address == nil || !previews[0].Address.Published || previews[0].LiveReleaseID != 5 {
		t.Fatalf("previews with a live release = %#v, %v", previews, err)
	}
}

// A head an administrator rejected stays rejected: the dashboard's own
// approval cannot revive it, only a fresh event for a new head can move on.
func TestRecordPreviewApprovalKeepsARejectedHeadRejected(t *testing.T) {
	ctx := t.Context()
	f := newAutomationFixture(t)
	trigger, err := f.automation.EnsurePullRequestTrigger(ctx, f.projectID, f.environmentID, "acme/app", "main")
	if err != nil {
		t.Fatal(err)
	}
	event := pullRequestEvent(3, strings.Repeat("a", 40))
	if _, _, err := f.automation.EnsurePreview(ctx, trigger, event); !errors.Is(err, ErrPreviewApproval) {
		t.Fatalf("webhook path did not stop for approval: %v", err)
	}
	approvals, err := f.automation.ListPreviewApprovals(ctx, f.projectID)
	if err != nil || len(approvals) != 1 {
		t.Fatalf("approvals = %#v, %v", approvals, err)
	}
	if _, err := f.automation.RejectPreview(ctx, f.projectID, approvals[0].ID, event.Revision, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.automation.RecordPreviewApproval(ctx, trigger, event, "admin"); !errors.Is(err, ErrPreviewApproval) {
		t.Fatalf("rejected head was approved from the dashboard: %v", err)
	}
	var state string
	if err := f.store.DB.QueryRow(`SELECT state FROM deploy_preview_approvals WHERE id=?`, approvals[0].ID).Scan(&state); err != nil || state != "rejected" {
		t.Fatalf("state after the attempt = %q, %v", state, err)
	}
	if _, _, err := f.automation.EnsurePreview(ctx, trigger, event); !errors.Is(err, ErrPreviewApproval) {
		t.Fatalf("rejected head configured a preview: %v", err)
	}
	next := pullRequestEvent(3, strings.Repeat("b", 40))
	approved, err := f.automation.RecordPreviewApproval(ctx, trigger, next, "admin")
	if err != nil || approved.State != "approved" || approved.Revision != next.Revision {
		t.Fatalf("new head = %#v, %v", approved, err)
	}
	if _, err := f.store.DB.Exec(`UPDATE deploy_triggers SET enabled=0 WHERE id=?`, trigger.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.automation.RecordPreviewApproval(ctx, trigger, pullRequestEvent(3, strings.Repeat("d", 40)), "admin"); !errors.Is(err, ErrPreviewApproval) {
		t.Fatalf("disabled trigger approved a head: %v", err)
	}
}

func TestCopyEnvironmentVariablesIntoAPreview(t *testing.T) {
	ctx := t.Context()
	f := newAutomationFixture(t)
	sealer, err := auth.NewSealer(strings.Repeat("a7", 32))
	if err != nil {
		t.Fatal(err)
	}
	plans := NewPlanningStore(f.store, sealer, []string{"/srv"})
	plans.now = f.automation.now
	for _, variable := range []struct{ name, value, sensitivity, scopes string }{
		{"PLAIN", "plain-value", "plain", "runtime"},
		{"SECRET", "secret-value", "secret", "runtime,build"},
		{"SHARED", "production-shared", "plain", "runtime"},
		{"DB_URL", "${{database.primary.url}}", "secret", "runtime"},
		{"ALIAS", "${{variable.PLAIN}}", "plain", "runtime"},
	} {
		sealed, err := sealer.Seal(variable.value)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.DB.Exec(`INSERT INTO deploy_variable_revisions(environment_id,key,revision,sensitivity,scopes,value_enc,value_digest,active,created_by,created_at) VALUES(?,?,1,?,?,?,'digest',1,'operator',1)`,
			f.environmentID, variable.name, variable.sensitivity, variable.scopes, sealed); err != nil {
			t.Fatal(err)
		}
	}
	trigger, err := f.automation.EnsurePullRequestTrigger(ctx, f.projectID, f.environmentID, "acme/app", "main")
	if err != nil {
		t.Fatal(err)
	}
	event := pullRequestEvent(4, strings.Repeat("a", 40))
	if _, err := f.automation.RecordPreviewApproval(ctx, trigger, event, "admin"); err != nil {
		t.Fatal(err)
	}
	preview, _, err := f.automation.EnsurePreview(ctx, trigger, event)
	if err != nil {
		t.Fatal(err)
	}

	for _, refused := range []struct {
		name     string
		from, to int64
		actor    string
		want     error
	}{
		{"into production", preview.EnvironmentID, f.environmentID, "admin", ErrPreviewIsolation},
		{"onto itself", preview.EnvironmentID, preview.EnvironmentID, "admin", ErrPreviewIsolation},
		{"another project's environment", f.environmentID, preview.EnvironmentID + 100, "admin", ErrEnvironmentNotFound},
		{"without an actor", f.environmentID, preview.EnvironmentID, "", ErrInvalidVariable},
	} {
		if _, _, err := plans.CopyEnvironmentVariables(ctx, f.projectID, refused.from, refused.to, refused.actor); !errors.Is(err, refused.want) {
			t.Fatalf("copy %s = %v, want %v", refused.name, err, refused.want)
		}
	}
	var previewVariables int
	if err := f.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_variable_revisions WHERE environment_id=?`, preview.EnvironmentID).Scan(&previewVariables); err != nil || previewVariables != 0 {
		t.Fatalf("refused copies wrote %d rows, %v", previewVariables, err)
	}
	// A value the operator typed into the preview under a key production
	// also has is theirs: the copy skips it, and closing leaves it.
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_variable_revisions(environment_id,key,revision,sensitivity,scopes,value_enc,value_digest,active,created_by,created_at) VALUES(?,'SHARED',1,'plain','runtime',?,'digest',1,'admin',1)`, preview.EnvironmentID, mustSeal(t, sealer, "preview-shared")); err != nil {
		t.Fatal(err)
	}

	copied, skipped, err := plans.CopyEnvironmentVariables(ctx, f.projectID, f.environmentID, preview.EnvironmentID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(copied, []string{"PLAIN", "SECRET"}) || !reflect.DeepEqual(skipped, []string{"ALIAS", "DB_URL", "SHARED"}) {
		t.Fatalf("copied = %v, skipped = %v", copied, skipped)
	}
	type row struct {
		name, sensitivity, scopes, createdBy string
		copiedFrom                           int64
	}
	readRows := func(environmentID int64) []row {
		t.Helper()
		rows, err := f.store.DB.Query(`SELECT key,sensitivity,scopes,created_by,copied_from_environment FROM deploy_variable_revisions WHERE environment_id=? AND active=1 ORDER BY key`, environmentID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		result := []row{}
		for rows.Next() {
			var item row
			if err := rows.Scan(&item.name, &item.sensitivity, &item.scopes, &item.createdBy, &item.copiedFrom); err != nil {
				t.Fatal(err)
			}
			result = append(result, item)
		}
		return result
	}
	if got := readRows(preview.EnvironmentID); !reflect.DeepEqual(got, []row{
		{"PLAIN", "plain", "runtime", "admin", f.environmentID},
		{"SECRET", "secret", "runtime,build", "admin", f.environmentID},
		{"SHARED", "plain", "runtime", "admin", 0},
	}) {
		t.Fatalf("preview variables = %#v", got)
	}
	if got := readRows(f.environmentID); len(got) != 5 || got[0].copiedFrom != 0 || got[4].copiedFrom != 0 {
		t.Fatalf("production variables changed: %#v", got)
	}
	values, err := plans.activeVariableValues(ctx, f.store.DB, f.projectID, preview.EnvironmentID)
	if err != nil || len(values) != 3 || values[0].value != "plain-value" || values[1].value != "secret-value" || values[2].value != "preview-shared" {
		t.Fatalf("preview values = %#v, %v", values, err)
	}

	// A re-test refreshes the copies: production's new value arrives, the
	// variable production dropped goes with it, and the preview's own
	// variable is not a copy and stays.
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_variable_revisions(environment_id,key,revision,sensitivity,scopes,value_enc,value_digest,active,created_by,created_at) VALUES(?,'OWN',1,'plain','runtime',?,'digest',1,'admin',1)`, preview.EnvironmentID, mustSeal(t, sealer, "own")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB.Exec(`UPDATE deploy_variable_revisions SET active=0 WHERE environment_id=? AND key='SECRET'`, f.environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_variable_revisions(environment_id,key,revision,sensitivity,scopes,value_enc,value_digest,active,created_by,created_at) VALUES(?,'PLAIN',2,'plain','runtime',?,'digest',1,'operator',2)`, f.environmentID, mustSeal(t, sealer, "plain-two")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB.Exec(`UPDATE deploy_variable_revisions SET active=0 WHERE environment_id=? AND key='PLAIN' AND revision=1`, f.environmentID); err != nil {
		t.Fatal(err)
	}
	copied, skipped, err = plans.CopyEnvironmentVariables(ctx, f.projectID, f.environmentID, preview.EnvironmentID, "admin")
	if err != nil || !reflect.DeepEqual(copied, []string{"PLAIN"}) || !reflect.DeepEqual(skipped, []string{"ALIAS", "DB_URL", "SHARED"}) {
		t.Fatalf("second copy = %v/%v, %v", copied, skipped, err)
	}
	if got := readRows(preview.EnvironmentID); !reflect.DeepEqual(got, []row{
		{"OWN", "plain", "runtime", "admin", 0},
		{"PLAIN", "plain", "runtime", "admin", f.environmentID},
		{"SHARED", "plain", "runtime", "admin", 0},
	}) {
		t.Fatalf("preview variables after the second copy = %#v", got)
	}
	values, err = plans.activeVariableValues(ctx, f.store.DB, f.projectID, preview.EnvironmentID)
	if err != nil || len(values) != 3 || values[1].name != "PLAIN" || values[1].value != "plain-two" || values[2].value != "preview-shared" {
		t.Fatalf("refreshed values = %#v, %v", values, err)
	}

	dropped, err := plans.DeactivateCopiedVariables(ctx, preview.EnvironmentID)
	if err != nil || dropped != 1 {
		t.Fatalf("deactivated = %d, %v", dropped, err)
	}
	if got := readRows(preview.EnvironmentID); !reflect.DeepEqual(got, []row{
		{"OWN", "plain", "runtime", "admin", 0},
		{"SHARED", "plain", "runtime", "admin", 0},
	}) {
		t.Fatalf("preview variables after deactivation = %#v", got)
	}
	if dropped, err = plans.DeactivateCopiedVariables(ctx, preview.EnvironmentID); err != nil || dropped != 0 {
		t.Fatalf("second deactivation = %d, %v", dropped, err)
	}
}

func mustSeal(t *testing.T, sealer *auth.Sealer, value string) string {
	t.Helper()
	sealed, err := sealer.Seal(value)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func TestCompletePreviewRemovalDropsCopiedVariables(t *testing.T) {
	ctx := t.Context()
	f := newAutomationFixture(t)
	trigger, err := f.automation.EnsurePullRequestTrigger(ctx, f.projectID, f.environmentID, "acme/app", "main")
	if err != nil {
		t.Fatal(err)
	}
	revision := strings.Repeat("a", 40)
	event := pullRequestEvent(5, revision)
	if _, err := f.automation.RecordPreviewApproval(ctx, trigger, event, "admin"); err != nil {
		t.Fatal(err)
	}
	preview, _, err := f.automation.EnsurePreview(ctx, trigger, event)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.automation.RecordPreviewVariablesCopied(ctx, preview.ID, revision); err != nil {
		t.Fatal(err)
	}
	sealed, _ := f.automation.sealer.Seal("value")
	for _, variable := range []struct {
		name       string
		copiedFrom int64
	}{{"COPIED", f.environmentID}, {"OWN", 0}} {
		if _, err := f.store.DB.Exec(`INSERT INTO deploy_variable_revisions(environment_id,key,revision,sensitivity,scopes,value_enc,value_digest,active,created_by,created_at,copied_from_environment) VALUES(?,?,1,'plain','runtime',?,?,1,'admin',1,?)`,
			preview.EnvironmentID, variable.name, sealed, fakeContentDigest("value"), variable.copiedFrom); err != nil {
			t.Fatal(err)
		}
	}
	runs := NewOrchestrationStore(f.store)
	runs.now = f.automation.now
	run, _, err := runs.Enqueue(ctx, RunRequest{ProjectID: f.projectID, EnvironmentID: preview.EnvironmentID, PlanRevision: 1,
		Operation: OperationPreviewRemove, Trigger: TriggerPreview, Actor: "admin", RequestDigest: "remove"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.store.DB.Exec(`INSERT INTO deploy_releases(project_id,environment_id,release_number,run_id,state,created_at) VALUES(?,?,1,?,'live',1)`, f.projectID, preview.EnvironmentID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := result.LastInsertId()
	if _, err := f.store.DB.Exec(`UPDATE deploy_environments SET live_release_id=? WHERE id=?`, releaseID, preview.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	lease, err := runs.ClaimNext(ctx, "remover", QueueBudget{Heavy: 1, Light: 1}, time.Minute)
	if err != nil || lease == nil || lease.RunID != run.ID {
		t.Fatalf("claim = %#v, %v", lease, err)
	}
	if err := runs.CompletePreviewRemoval(ctx, run.ID, lease.Token, releaseID); err != nil {
		t.Fatal(err)
	}
	var active []string
	rows, err := f.store.DB.Query(`SELECT key FROM deploy_variable_revisions WHERE environment_id=? AND active=1 ORDER BY key`, preview.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		active = append(active, name)
	}
	rows.Close()
	if !reflect.DeepEqual(active, []string{"OWN"}) {
		t.Fatalf("active variables after removal = %v, want only the preview's own", active)
	}
	var copiedRevision string
	var archived int64
	if err := f.store.DB.QueryRow(`SELECT p.variables_copied_revision,e.archived_at FROM deploy_preview_refs p JOIN deploy_environments e ON e.id=p.environment_id WHERE p.id=?`, preview.ID).Scan(&copiedRevision, &archived); err != nil {
		t.Fatal(err)
	}
	if copiedRevision != "" || archived == 0 {
		t.Fatalf("preview after removal: copied revision %q, archived %d", copiedRevision, archived)
	}
}

// A preview's source names the pull request ref the forge publishes on the
// base repository. Materialize has to accept that ref and fetch it as it is:
// the commit lives on no branch of the base repository, so mapping it to
// refs/heads/… would find nothing. The remote is answered by a local bare
// repository through Git's own URL rewriting, so the path under test is the
// real one, including the release mirror's fetch of the pull request head.
func TestMaterializeFetchesAPullRequestHeadFromTheBaseRepository(t *testing.T) {
	t.Parallel()
	origin := t.TempDir()
	runPlanningGitFixture(t, origin, "init", "--initial-branch", "main")
	runPlanningGitFixture(t, origin, "config", "user.email", "fixture@example.test")
	runPlanningGitFixture(t, origin, "config", "user.name", "Fixture")
	writeBuildFixture(t, origin, "message.txt", "base\n")
	runPlanningGitFixture(t, origin, "add", "message.txt")
	runPlanningGitFixture(t, origin, "commit", "-m", "base")
	base := strings.TrimSpace(runPlanningGitOutput(t, origin, "rev-parse", "HEAD"))
	writeBuildFixture(t, origin, "message.txt", "pull request\n")
	runPlanningGitFixture(t, origin, "add", "message.txt")
	runPlanningGitFixture(t, origin, "commit", "-m", "pull request")
	head := strings.TrimSpace(runPlanningGitOutput(t, origin, "rev-parse", "HEAD"))
	runPlanningGitFixture(t, origin, "update-ref", "refs/pull/1/head", head)
	runPlanningGitFixture(t, origin, "reset", "--hard", base)

	remote := "https://github.com/acme/app.git"
	cache := t.TempDir()
	mirror := filepath.Join(cache, "git-release-mirrors", strings.TrimPrefix(digestBytes([]byte(remote)), "sha256:")+".git")
	if err := os.MkdirAll(filepath.Dir(mirror), 0o700); err != nil {
		t.Fatal(err)
	}
	runPlanningGitFixture(t, t.TempDir(), "init", "--bare", "--", mirror)
	runPlanningGitFixture(t, mirror, "config", "url."+origin+".insteadOf", remote)

	analyzer := NewHostSourceAnalyzer(nil, nil, cache, nil, nil)
	source := DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: remote, Ref: "refs/pull/1/head"}
	identity := SourceIdentity{Kind: SourceGit, Remote: remote, Repository: "acme/app", Ref: "refs/pull/1/head", Revision: head}
	materialized, err := analyzer.Materialize(context.Background(), source, identity, 7, t.TempDir())
	if err != nil {
		t.Fatalf("Materialize(refs/pull/1/head) = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(materialized.Root, "message.txt"))
	if err != nil || string(content) != "pull request\n" {
		t.Fatalf("materialized content = %q, %v", content, err)
	}
	if materialized.Revision != head {
		t.Fatalf("materialized revision = %q, want %q", materialized.Revision, head)
	}
	if current := strings.TrimSpace(runPlanningGitOutput(t, origin, "rev-parse", "HEAD")); current != base {
		t.Fatalf("base repository moved to %s", current)
	}
	// The mirror started empty, so the commit can only have arrived through
	// the release fetch of the pull request ref itself.
	if fetched := strings.TrimSpace(runPlanningGitOutput(t, mirror, "rev-parse", "refs/just-dashboard/releases/"+head)); fetched != head {
		t.Fatalf("release mirror holds %q under the pull request's release ref, want %q", fetched, head)
	}
}
