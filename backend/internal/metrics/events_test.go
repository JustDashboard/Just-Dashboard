package metrics

import (
	"context"
	"testing"
	"time"
)

// The reboot markers are inferred rather than recorded, which is what makes
// them work for the reboot nobody started from this dashboard.
func TestEventsInfersRebootFromUptimeGoingBackwards(t *testing.T) {
	r := testRecorder(t, 15*time.Second, DefaultRetention)
	ctx := context.Background()

	base := time.Now().Add(-30 * time.Minute).Truncate(time.Second)
	// Three samples climbing, then one whose uptime is 60s — the machine came
	// back up a minute before that sample was taken.
	for i, uptime := range []uint64{86_400, 86_415, 86_430} {
		mustRecord(t, r, base.Add(time.Duration(i)*15*time.Second), uptime)
	}
	rebootAt := base.Add(10 * time.Minute)
	mustRecord(t, r, rebootAt, 60)

	events, err := r.Events(ctx, base.Add(-time.Minute), time.Now(), 100)
	if err != nil {
		t.Fatal(err)
	}
	var found *Event
	for i := range events {
		if events[i].Kind == "reboot" {
			found = &events[i]
		}
	}
	if found == nil {
		t.Fatalf("no reboot event in %+v", events)
	}
	// The marker belongs at the restart, not at the sample that noticed it.
	want := rebootAt.Add(-60 * time.Second).Unix()
	if got := found.TS.Unix(); got != want {
		t.Errorf("reboot marked at %v, want %v (sample time minus new uptime)",
			found.TS, time.Unix(want, 0))
	}
}

func TestEventsIgnoresUptimeClimbing(t *testing.T) {
	r := testRecorder(t, 15*time.Second, DefaultRetention)
	base := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	for i := range 6 {
		mustRecord(t, r, base.Add(time.Duration(i)*15*time.Second), uint64(1000+i*15))
	}
	events, err := r.Events(context.Background(), base.Add(-time.Minute), time.Now(), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Kind == "reboot" {
			t.Fatalf("invented a reboot from a monotonic uptime: %+v", e)
		}
	}
}

// A window with nothing in it is a normal answer, not an error, and must come
// back as an empty list rather than a null the client has to guard.
func TestEventsEmptyWindow(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	events, err := r.Events(context.Background(), time.Now().Add(-time.Hour), time.Now(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if events == nil {
		t.Fatal("Events returned nil rather than an empty slice")
	}
	if len(events) != 0 {
		t.Fatalf("expected no events, got %+v", events)
	}
}

// A deploy marker names the project it released, so a row of it can carry
// the project's own icon rather than a deploy glyph.
func TestDeployEventsNameTheirProject(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	started := time.Now().Add(-10 * time.Minute).Unix()
	project, err := r.db.Exec(`INSERT INTO deploy_projects(name, repo_path, hook_secret, hook_id, created_at) VALUES('storefront', '/srv/storefront', 'sealed', 'storefront-hook', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := project.LastInsertId()
	if _, err := r.db.Exec(`INSERT INTO deploy_runs(project_id, started_at, ended_at, status) VALUES(?, ?, ?, 'success')`, projectID, started, started+30); err != nil {
		t.Fatal(err)
	}
	events, err := r.Events(context.Background(), time.Now().Add(-time.Hour), time.Now(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != "deploy" || events[0].Title != "storefront" || events[0].ProjectID != projectID {
		t.Fatalf("events = %+v, want one deploy of storefront naming project %d", events, projectID)
	}
}

func TestSortEventsIsAscendingAndStable(t *testing.T) {
	now := time.Now()
	events := []Event{
		{TS: now.Add(3 * time.Minute), Title: "third"},
		{TS: now, Title: "first"},
		{TS: now.Add(time.Minute), Title: "second-a"},
		{TS: now.Add(time.Minute), Title: "second-b"},
	}
	sortEvents(events)
	want := []string{"first", "second-a", "second-b", "third"}
	for i, title := range want {
		if events[i].Title != title {
			t.Fatalf("position %d = %q, want %q (order: %+v)", i, events[i].Title, title, events)
		}
	}
}

func mustRecord(t *testing.T, r *Recorder, at time.Time, uptime uint64) {
	t.Helper()
	if err := r.record(context.Background(), Sample{TS: at, Uptime: uptime}); err != nil {
		t.Fatal(err)
	}
}
