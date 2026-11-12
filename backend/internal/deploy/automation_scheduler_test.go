package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// The scheduler's pass returned nothing: a store that could not hand out the
// due schedules, and a schedule that came due and could not be run, were both
// dropped without a word.
func TestTheScheduleSaysWhenAPassOrADueScheduleFails(t *testing.T) {
	f := newAutomationFixture(t)
	ctx := context.Background()
	_, err := f.automation.CreateSchedule(ctx, f.projectID, f.environmentID, ScheduleWrite{
		Name: "nightly", Expression: "0 12 * * *", Timezone: "UTC", Enabled: true,
		Steps: []ScheduleStep{{Action: "deploy", Config: json.RawMessage(`{}`), Required: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.automation.now = func() time.Time { return time.Date(2026, 9, 8, 12, 0, 1, 0, time.UTC) }

	out := &lockedLog{}
	scheduler := NewAutomationScheduler(f.automation, func(context.Context, ScheduleDispatch) error {
		return errors.New("the project is not supported on this server")
	})
	scheduler.log = slog.New(slog.NewTextHandler(out, nil))
	if err := scheduler.run(ctx); err != nil {
		t.Fatalf("run = %v, want a pass that claimed its schedules to have worked", err)
	}
	logged := out.String()
	if !strings.Contains(logged, "came due and could not be run") || !strings.Contains(logged, "name=nightly") || !strings.Contains(logged, "the project is not supported") {
		t.Fatalf("log = %q, want the schedule that could not be run, by name, with the reason", logged)
	}

	if _, err := f.store.DB.Exec(`ALTER TABLE deploy_schedules RENAME TO deploy_schedules_gone`); err != nil {
		t.Fatal(err)
	}
	err = scheduler.run(ctx)
	if err == nil || !strings.Contains(err.Error(), "could not be claimed") || !strings.Contains(err.Error(), "deploy_schedules") {
		t.Fatalf("run = %v, want the failed claim as the pass's error", err)
	}

	// And the loop says so: once, under the task's name.
	scheduler.Start(ctx)
	defer scheduler.Stop()
	out.waitFor(t, "background task failed", 5*time.Second)
	if logged := out.String(); !strings.Contains(logged, `task="deployment schedule"`) || !strings.Contains(logged, "deploy_schedules") {
		t.Fatalf("log = %s, want the schedule's loop named with the store's reason", logged)
	}
}
