package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// oneConnectionFixture is a deployment with rows in everything a list read
// walks: runs in flight, schedules with steps, one of them due.
type oneConnectionFixture struct {
	*automationFixture
	runs    *OrchestrationStore
	plans   *PlanningStore
	legacy  *Store
	staging int64
	run     *EngineRun
}

func newOneConnectionFixture(t *testing.T) *oneConnectionFixture {
	t.Helper()
	base := newAutomationFixture(t)
	sealer := base.automation.sealer
	f := &oneConnectionFixture{
		automationFixture: base,
		runs:              NewOrchestrationStore(base.store),
		plans:             NewPlanningStore(base.store, sealer, []string{t.TempDir()}),
		legacy:            NewStore(base.store, sealer, []string{t.TempDir()}),
	}
	ctx := context.Background()
	// An environment with no variables: the reads below must also answer
	// "it exists and holds none" without a second connection.
	result, err := base.store.DB.Exec(`INSERT INTO deploy_environments(project_id,name,slug,kind,desired_revision,strategy,expected_downtime,protected,created_at,updated_at) VALUES(?,'Staging','staging','staging',1,'blue_green',0,0,1,1)`, base.projectID)
	if err != nil {
		t.Fatal(err)
	}
	f.staging, _ = result.LastInsertId()
	for index := range 3 {
		run, created, err := f.runs.Enqueue(ctx, RunRequest{
			ProjectID: base.projectID, EnvironmentID: base.environmentID,
			Operation: OperationDeploy, Trigger: TriggerManual, Actor: "admin",
			RequestDigest: fmt.Sprintf("one-connection-%d", index), PlanRevision: 1, SlotClass: SlotLight,
		})
		if err != nil || !created {
			t.Fatalf("enqueue %d: created=%v err=%v", index, created, err)
		}
		f.run = run
	}
	for _, name := range []string{"nightly", "weekly"} {
		if _, err := base.automation.CreateSchedule(ctx, base.projectID, base.environmentID, ScheduleWrite{
			Name: name, Expression: "0 13 * * *", Timezone: "UTC", Enabled: true,
			Steps: []ScheduleStep{
				{Action: "deploy", Config: json.RawMessage(`{}`), Required: true},
				{Action: "backup", Config: json.RawMessage(`{"jobId":4}`), Required: false},
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Both schedules fall due.
	created := base.automation.now()
	base.automation.now = func() time.Time { return created.Add(48 * time.Hour) }
	return f
}

// reads is every read the fixture has rows for. A list read that is added to
// one of these stores belongs here, with rows for it in the fixture: the
// table is how the next query made inside an open cursor is caught.
func (f *oneConnectionFixture) reads() []struct {
	name string
	call func(context.Context) error
} {
	budget := QueueBudget{Heavy: 2, Light: 4}
	return []struct {
		name string
		call func(context.Context) error
	}{
		{"Fleet", func(ctx context.Context) error {
			fleet, err := f.runs.Fleet(ctx, budget)
			if err == nil && len(fleet.ActiveWork) != 3 {
				err = fmt.Errorf("active work = %d, want the 3 runs in flight", len(fleet.ActiveWork))
			}
			return err
		}},
		{"DeploymentSummary", func(ctx context.Context) error {
			_, err := f.runs.DeploymentSummary(ctx, f.projectID, budget)
			return err
		}},
		{"EnvironmentDeploymentSummary", func(ctx context.Context) error {
			_, err := f.runs.EnvironmentDeploymentSummary(ctx, f.projectID, f.staging, budget)
			return err
		}},
		{"ProjectRuns", func(ctx context.Context) error {
			runs, err := f.runs.ProjectRuns(ctx, f.projectID, 10)
			if err == nil && len(runs) != 3 {
				err = fmt.Errorf("runs = %d, want 3", len(runs))
			}
			return err
		}},
		{"ProjectRunsFiltered", func(ctx context.Context) error {
			_, _, err := f.runs.ProjectRunsFiltered(ctx, f.projectID, RunListFilter{State: "active"})
			return err
		}},
		{"ProjectEnvironments", func(ctx context.Context) error {
			_, err := f.runs.ProjectEnvironments(ctx, f.projectID)
			return err
		}},
		{"Steps", func(ctx context.Context) error {
			steps, err := f.runs.Steps(ctx, f.run.ID)
			if err == nil && len(steps) == 0 {
				err = fmt.Errorf("the run has no steps")
			}
			return err
		}},
		{"Snapshot", func(ctx context.Context) error {
			_, err := f.runs.Snapshot(ctx, f.run.ID)
			return err
		}},
		{"EventsAfter", func(ctx context.Context) error {
			events, err := f.runs.EventsAfter(ctx, f.run.ID, 0, 100)
			if err == nil && len(events) == 0 {
				err = fmt.Errorf("the run has no events")
			}
			return err
		}},
		{"RunTranscriptTail", func(ctx context.Context) error {
			_, _, err := f.runs.RunTranscriptTail(ctx, f.run.ID, 50)
			return err
		}},
		{"RunTranscriptAfter", func(ctx context.Context) error {
			_, _, err := f.runs.RunTranscriptAfter(ctx, f.run.ID, 0, 50)
			return err
		}},
		{"ExpiredLeases", func(ctx context.Context) error {
			_, err := f.runs.ExpiredLeases(ctx)
			return err
		}},
		{"PreviewBranchPolicies", func(ctx context.Context) error {
			_, err := f.runs.PreviewBranchPolicies(ctx, f.environmentID)
			return err
		}},
		{"ListSchedules", func(ctx context.Context) error {
			schedules, err := f.automation.ListSchedules(ctx, f.projectID, f.environmentID)
			if err == nil && (len(schedules) != 2 || len(schedules[0].Steps) != 2 || len(schedules[1].Steps) != 2) {
				err = fmt.Errorf("schedules = %+v, want two with two steps each", schedules)
			}
			return err
		}},
		{"ListTriggers", func(ctx context.Context) error {
			_, err := f.automation.ListTriggers(ctx, f.projectID, f.environmentID)
			return err
		}},
		{"ListPreviews", func(ctx context.Context) error {
			_, err := f.automation.ListPreviews(ctx, f.projectID)
			return err
		}},
		{"ListPreviewApprovals", func(ctx context.Context) error {
			_, err := f.automation.ListPreviewApprovals(ctx, f.projectID)
			return err
		}},
		{"ListTrafficAlerts", func(ctx context.Context) error {
			_, err := f.automation.ListTrafficAlerts(ctx, f.projectID)
			return err
		}},
		{"NotificationChannelsWithHistory", func(ctx context.Context) error {
			_, err := f.automation.NotificationChannelsWithHistory(ctx)
			return err
		}},
		{"ListVariables", func(ctx context.Context) error {
			_, err := f.plans.ListVariables(ctx, f.projectID, f.staging)
			return err
		}},
		{"PreviewDotenvImport", func(ctx context.Context) error {
			// Reads inside a transaction, on an environment that holds no
			// variables yet.
			_, err := f.plans.PreviewDotenvImport(ctx, f.projectID, f.staging, DotenvImportRequest{
				Dotenv: "GREETING=hello\n", Sensitivity: "plain", Scopes: []string{"runtime"},
			})
			return err
		}},
		{"ListCredentials", func(ctx context.Context) error {
			_, err := f.plans.ListCredentials(ctx)
			return err
		}},
		{"ListSharedVariables", func(ctx context.Context) error {
			_, err := f.plans.ListSharedVariables(ctx, f.projectID)
			return err
		}},
		{"UserTemplates", func(ctx context.Context) error {
			_, err := f.plans.UserTemplates(ctx)
			return err
		}},
		{"SizingTargets", func(ctx context.Context) error {
			_, err := f.plans.SizingTargets(ctx, f.projectID, f.environmentID)
			return err
		}},
		{"legacy List", func(ctx context.Context) error {
			projects, err := f.legacy.List(ctx)
			if err == nil && len(projects) != 1 {
				err = fmt.Errorf("projects = %d, want 1", len(projects))
			}
			return err
		}},
		{"legacy Runs", func(ctx context.Context) error {
			_, err := f.legacy.Runs(ctx, f.projectID, 10)
			return err
		}},
		// Last: it claims what it lists.
		{"ClaimDueSchedules", func(ctx context.Context) error {
			due, err := f.automation.ClaimDueSchedules(ctx, 20)
			if err == nil && (len(due) != 2 || len(due[0].Schedule.Steps) != 2) {
				err = fmt.Errorf("due = %+v, want both schedules with their steps", due)
			}
			return err
		}},
	}
}

// A read that queries the pool while its own cursor or transaction is open
// works on four connections until four of them run at once. On one connection
// it can never finish, so each read is called with rows present and a deadline.
func TestEveryListReadFinishesOnOneConnection(t *testing.T) {
	f := newOneConnectionFixture(t)
	storetest.OneConnection(f.store)
	for _, read := range f.reads() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := read.call(ctx)
		cancel()
		if err != nil {
			t.Errorf("%s on a single connection: %v (does it query while a cursor or a transaction of its own is open?)", read.name, err)
		}
	}
}

// The pages that poll read through the store's read pool, so they neither
// wait for the pool the engine and every request's session check share, nor
// use it up: with every connection of that pool taken, the fleet, a run list
// and a transcript are still read.
func TestTheReadModelIsReadWhileThePoolIsExhausted(t *testing.T) {
	f := newOneConnectionFixture(t)
	storetest.OwnHandles(t, f.store)
	hold, cancelHold := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelHold()
	for range f.store.DB.Stats().MaxOpenConnections {
		conn, err := f.store.DB.Conn(hold)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	budget := QueueBudget{Heavy: 2, Light: 4}
	fleet, err := f.runs.Fleet(ctx, budget)
	if err != nil {
		t.Fatalf("the fleet behind an exhausted pool: %v", err)
	}
	if len(fleet.Deployments) != 1 || len(fleet.ActiveWork) != 3 {
		t.Fatalf("fleet = %d deployments, %d runs in flight; the read pool must see what was just written",
			len(fleet.Deployments), len(fleet.ActiveWork))
	}
	if _, err := f.runs.DeploymentSummary(ctx, f.projectID, budget); err != nil {
		t.Fatalf("a deployment's summary behind an exhausted pool: %v", err)
	}
	if runs, _, err := f.runs.ProjectRunsFiltered(ctx, f.projectID, RunListFilter{}); err != nil || len(runs) != 3 {
		t.Fatalf("the run list behind an exhausted pool: %d runs, %v", len(runs), err)
	}
	if _, _, err := f.runs.RunTranscriptTail(ctx, f.run.ID, 50); err != nil {
		t.Fatalf("a transcript behind an exhausted pool: %v", err)
	}
	if _, err := f.runs.EnvironmentInsights(ctx, f.projectID, f.environmentID, 30); err != nil {
		t.Fatalf("insights behind an exhausted pool: %v", err)
	}
}

// The freeze as it happened: more concurrent fleet and schedule reads than the
// pool has connections, with runs in flight. Before the reads stopped querying
// inside their cursors, none of these returned until its caller gave up.
func TestConcurrentFleetAndScheduleReadsDoNotExhaustThePool(t *testing.T) {
	f := newOneConnectionFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	const readers = 8
	errs := make(chan error, 2*readers)
	var wg sync.WaitGroup
	for range readers {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 20 {
				if _, err := f.runs.Fleet(ctx, QueueBudget{Heavy: 2, Light: 4}); err != nil {
					errs <- fmt.Errorf("fleet: %w", err)
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			for range 20 {
				if _, err := f.automation.ListSchedules(ctx, f.projectID, f.environmentID); err != nil {
					errs <- fmt.Errorf("schedules: %w", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
