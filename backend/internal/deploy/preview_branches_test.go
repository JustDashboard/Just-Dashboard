package deploy

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

func TestNormalizePreviewBranchPolicy(t *testing.T) {
	policy, err := NormalizePreviewBranchPolicy(PreviewBranchPolicy{Mode: PreviewBranchMasked, MaskedColumns: []string{" users.email ", "users.email", "billing.cards.number", ""}})
	if err != nil || !reflect.DeepEqual(policy.MaskedColumns, []string{"billing.cards.number", "users.email"}) {
		t.Fatalf("policy = %+v, %v", policy, err)
	}
	if policy, err := NormalizePreviewBranchPolicy(PreviewBranchPolicy{Mode: PreviewBranchLatest, MaskedColumns: []string{"users.email"}}); err != nil || len(policy.MaskedColumns) != 0 {
		t.Fatalf("an unmasked branch kept columns: %+v, %v", policy, err)
	}
	if policy, err := NormalizePreviewBranchPolicy(PreviewBranchPolicy{}); err != nil || policy.Mode != PreviewBranchNone {
		t.Fatalf("empty policy = %+v, %v", policy, err)
	}
	for name, bad := range map[string]PreviewBranchPolicy{
		"unknown mode":         {Mode: "copy"},
		"masked with nothing":  {Mode: PreviewBranchMasked},
		"a column alone":       {Mode: PreviewBranchMasked, MaskedColumns: []string{"email"}},
		"SQL in a name":        {Mode: PreviewBranchMasked, MaskedColumns: []string{"users.email; DROP TABLE users"}},
		"a quoted name":        {Mode: PreviewBranchMasked, MaskedColumns: []string{`users."email"`}},
		"four parts":           {Mode: PreviewBranchMasked, MaskedColumns: []string{"a.b.c.d"}},
		"a leading digit name": {Mode: PreviewBranchMasked, MaskedColumns: []string{"1users.email"}},
	} {
		if _, err := NormalizePreviewBranchPolicy(bad); !errors.Is(err, ErrInvalidPlan) {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
	if PreviewBranchHostConnection("db-12-branch.jd.internal") != 12 || PreviewBranchHostConnection("db-12.jd.internal") != 0 ||
		PreviewBranchHostConnection("DB-7-BRANCH.JD.INTERNAL.") != 7 || PreviewBranchHostConnection("db-x-branch.jd.internal") != 0 {
		t.Fatal("branch host parsing")
	}
}

type previewBranchFixture struct {
	*automationFixture
	plans        *PlanningStore
	orchestrator *OrchestrationStore
	connectionID int64
	trigger      *Trigger
}

func newPreviewBranchFixture(t *testing.T) *previewBranchFixture {
	t.Helper()
	f := newAutomationFixture(t)
	sealer, err := auth.NewSealer(strings.Repeat("a7", 32))
	if err != nil {
		t.Fatal(err)
	}
	plans := NewPlanningStore(f.store, sealer, []string{"/srv"})
	plans.now = f.automation.now
	result, err := f.store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES('primary', 'postgres', ?, 1)`, mustSeal(t, sealer, "postgres://app:secret@127.0.0.1:5433/app"))
	if err != nil {
		t.Fatal(err)
	}
	connectionID, _ := result.LastInsertId()
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_dependencies(environment_id,release_id,kind,ownership,resource_kind,resource_id,config_json,created_at) VALUES(?,0,'database','linked','database_connection',?,'{}',1)`, f.environmentID, connectionID); err != nil {
		t.Fatal(err)
	}
	for _, variable := range []struct{ name, value string }{
		{"DATABASE_URL", "${{database.primary.url}}"},
		{"POOLED_URL", "${{database.1.pooled}}"},
		{"PLAIN", "value"},
	} {
		if _, err := f.store.DB.Exec(`INSERT INTO deploy_variable_revisions(environment_id,key,revision,sensitivity,scopes,value_enc,value_digest,active,created_by,created_at) VALUES(?,?,1,'secret','runtime',?,'digest',1,'operator',1)`,
			f.environmentID, variable.name, mustSeal(t, sealer, variable.value)); err != nil {
			t.Fatal(err)
		}
	}
	trigger, err := f.automation.EnsurePullRequestTrigger(t.Context(), f.projectID, f.environmentID, "acme/app", "main")
	if err != nil {
		t.Fatal(err)
	}
	orchestrator := NewOrchestrationStore(f.store)
	orchestrator.now = f.automation.now
	return &previewBranchFixture{automationFixture: f, plans: plans, orchestrator: orchestrator, connectionID: connectionID, trigger: trigger}
}

func (f *previewBranchFixture) preview(t *testing.T, event ProviderEvent) *PreviewRef {
	t.Helper()
	if _, err := f.automation.RecordPreviewApproval(t.Context(), f.trigger, event, "admin"); err != nil {
		t.Fatal(err)
	}
	preview, _, err := f.automation.EnsurePreview(t.Context(), f.trigger, event)
	if err != nil {
		t.Fatal(err)
	}
	return preview
}

// A preview is given a branch of each database its environment says so for,
// once, unless its head is a fork's; production's database references are
// copied for a branched database and nothing else.
func TestPreviewBranchesAreRequestedForAPreviewAndNeverForAFork(t *testing.T) {
	ctx := t.Context()
	f := newPreviewBranchFixture(t)
	if _, err := f.orchestrator.PutPreviewBranchPolicy(ctx, f.projectID, f.environmentID, PreviewBranchPolicy{ConnectionID: f.connectionID + 50, Mode: PreviewBranchLatest}, "admin"); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("a database the environment does not link = %v", err)
	}
	saved, err := f.orchestrator.PutPreviewBranchPolicy(ctx, f.projectID, f.environmentID, PreviewBranchPolicy{ConnectionID: f.connectionID, Mode: PreviewBranchMasked, MaskedColumns: []string{"users.email"}}, "admin")
	if err != nil || saved.UpdatedBy != "admin" || saved.Mode != PreviewBranchMasked {
		t.Fatalf("saved = %+v, %v", saved, err)
	}

	preview := f.preview(t, pullRequestEvent(4, strings.Repeat("a", 40)))
	branches, err := f.orchestrator.PreviewBranches(ctx, preview.EnvironmentID)
	if err != nil || len(branches) != 1 {
		t.Fatalf("branches = %+v, %v", branches, err)
	}
	branch := branches[0]
	if branch.State != PreviewBranchWaiting || branch.ConnectionID != f.connectionID || branch.Name != "primary" ||
		branch.SourceEnvironmentID != f.environmentID || !reflect.DeepEqual(branch.MaskedColumns, []string{"users.email"}) {
		t.Fatalf("branch = %+v", branch)
	}
	if _, err := f.orchestrator.PutPreviewBranchPolicy(ctx, f.projectID, preview.EnvironmentID, PreviewBranchPolicy{ConnectionID: f.connectionID, Mode: PreviewBranchLatest}, "admin"); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("a preview's own policy = %v", err)
	}

	// The same head again, and a new one, keep the one branch.
	f.preview(t, pullRequestEvent(4, strings.Repeat("a", 40)))
	f.preview(t, pullRequestEvent(4, strings.Repeat("b", 40)))
	if branches, _ := f.orchestrator.PreviewBranches(ctx, preview.EnvironmentID); len(branches) != 1 || branches[0].ID != branch.ID {
		t.Fatalf("branches after new heads = %+v", branches)
	}

	copied, skipped, err := f.plans.CopyEnvironmentVariables(ctx, f.projectID, f.environmentID, preview.EnvironmentID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(copied, []string{"DATABASE_URL", "PLAIN"}) || !reflect.DeepEqual(skipped, []string{"POOLED_URL"}) {
		t.Fatalf("copied %v, skipped %v: the branched reference travels, the pooled one does not", copied, skipped)
	}

	// A fork's preview gets none, whatever the policy says.
	fork := pullRequestEvent(5, strings.Repeat("c", 40))
	fork.HeadRepository = "someone/app"
	forked := f.preview(t, fork)
	if branches, _ := f.orchestrator.PreviewBranches(ctx, forked.EnvironmentID); len(branches) != 0 {
		t.Fatalf("a fork's preview got branches: %+v", branches)
	}
	copied, skipped, err = f.plans.CopyEnvironmentVariables(ctx, f.projectID, f.environmentID, forked.EnvironmentID, "admin")
	if err != nil || !reflect.DeepEqual(copied, []string{"PLAIN"}) || len(skipped) != 2 {
		t.Fatalf("fork copy = %v %v, %v", copied, skipped, err)
	}
	// A head whose repository was not recorded is not known to be the
	// repository's own.
	unknown := pullRequestEvent(6, strings.Repeat("d", 40))
	unknown.HeadRepository = ""
	if branches, _ := f.orchestrator.PreviewBranches(ctx, f.preview(t, unknown).EnvironmentID); len(branches) != 0 {
		t.Fatalf("an unrecorded head got branches: %+v", branches)
	}
}

func TestPreviewBranchLifecycleKeepsItsSignInAcrossARefresh(t *testing.T) {
	ctx := t.Context()
	f := newPreviewBranchFixture(t)
	if _, err := f.orchestrator.PutPreviewBranchPolicy(ctx, f.projectID, f.environmentID, PreviewBranchPolicy{ConnectionID: f.connectionID, Mode: PreviewBranchLatest}, "admin"); err != nil {
		t.Fatal(err)
	}
	preview := f.preview(t, pullRequestEvent(4, strings.Repeat("a", 40)))
	branch, err := f.orchestrator.ActivePreviewBranch(ctx, preview.EnvironmentID, f.connectionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.orchestrator.RefreshPreviewBranch(ctx, preview.EnvironmentID, f.connectionID, "admin"); !errors.Is(err, ErrPreviewBranchEnded) {
		t.Fatalf("a branch still waiting was refreshed: %v", err)
	}
	claimed, err := f.orchestrator.ClaimPreviewBranch(ctx, branch.ID)
	if err != nil || !claimed {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	if again, _ := f.orchestrator.ClaimPreviewBranch(ctx, branch.ID); again {
		t.Fatal("a branch was claimed twice")
	}
	ready, err := f.orchestrator.PreviewBranchReady(ctx, branch.ID, PreviewBranchReadiness{
		Container: "jd-preview-e9-branch1-db", Network: "jd-e9-db-x", Host: PreviewBranchHost(f.connectionID), Port: 5432,
		Database: "app", Engine: "postgres", SecretSealed: "sealed-sign-in", Source: RehearsalSource{Kind: "dump", BackupRunID: 3},
	})
	if err != nil || ready.State != PreviewBranchReady || ready.Source.Kind != "dump" || ready.Source.BackupRunID != 3 || ready.ReadyAt == nil {
		t.Fatalf("ready = %+v, %v", ready, err)
	}
	ended, next, err := f.orchestrator.RefreshPreviewBranch(ctx, preview.EnvironmentID, f.connectionID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if ended.ID != branch.ID || ended.State != PreviewBranchRemoved || next.State != PreviewBranchWaiting || next.RequestedBy != "admin" {
		t.Fatalf("refresh = %+v then %+v", ended, next)
	}
	if sealed, _ := f.orchestrator.PreviewBranchSecret(ctx, next.ID); sealed != "sealed-sign-in" {
		t.Fatalf("the refreshed branch's sign-in = %q, want the old one carried over", sealed)
	}
	if sealed, _ := f.orchestrator.PreviewBranchSecret(ctx, branch.ID); sealed != "" {
		t.Fatal("the ended branch kept its sign-in")
	}
	if _, err := f.orchestrator.EndPreviewBranch(ctx, next.ID, PreviewBranchFailed, "it would not start"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.orchestrator.EndPreviewBranch(ctx, next.ID, PreviewBranchRemoved, "again"); !errors.Is(err, ErrPreviewBranchEnded) {
		t.Fatalf("an ended branch ended again: %v", err)
	}
	// Once the environment gives previews nothing, a branch is not refreshed.
	if _, err := f.orchestrator.PutPreviewBranchPolicy(ctx, f.projectID, f.environmentID, PreviewBranchPolicy{ConnectionID: f.connectionID, Mode: PreviewBranchNone}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.orchestrator.RefreshPreviewBranch(ctx, preview.EnvironmentID, f.connectionID, "admin"); !errors.Is(err, ErrPreviewBranchPolicy) {
		t.Fatalf("refresh without a policy = %v", err)
	}
}
