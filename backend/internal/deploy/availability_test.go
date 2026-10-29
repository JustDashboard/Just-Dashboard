package deploy

import (
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// The availability figure is read from two records rather than measured, so
// the maths is where it can go wrong: downtime counted twice for two
// containers, the old release's exit at a cutover counted against the new
// one, a short record reading as a long steady one.

var availabilityNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func lifecycle(at time.Time, action, id, release string) dockerx.Event {
	return dockerx.Event{
		Time: at, Type: "container", Action: action, Name: "api-" + id, ID: id,
		Owner: map[string]string{"environment-id": "7", "release-id": release},
	}
}

func ratioOf(t *testing.T, a Availability, window string) (float64, AvailabilityWindow) {
	t.Helper()
	for _, w := range a.Windows {
		if w.Window == window {
			if w.Ratio == nil {
				return -1, w
			}
			return *w.Ratio, w
		}
	}
	t.Fatalf("no %s window in %+v", window, a.Windows)
	return 0, AvailabilityWindow{}
}

func TestAvailabilityCountsAnExitUntilTheStartThatFollowedIt(t *testing.T) {
	now := availabilityNow
	live := []LiveSpan{{ReleaseID: 21, ReleaseNumber: 3, From: now.Add(-10 * 24 * time.Hour)}}
	// Down for six minutes yesterday: an exit and the restart policy's start.
	events := []dockerx.Event{
		lifecycle(now.Add(-10*24*time.Hour), "start", "c1", "21"),
		lifecycle(now.Add(-20*time.Hour), "die", "c1", "21"),
		lifecycle(now.Add(-20*time.Hour+6*time.Minute), "start", "c1", "21"),
	}
	got := ComputeAvailability(now, now.Add(-10*24*time.Hour), live, events)
	if got.Status != "available" || got.Down || got.LiveRelease == nil || *got.LiveRelease != 3 {
		t.Fatalf("status: %+v", got)
	}
	day, w := ratioOf(t, got, "24h")
	if w.ObservedSeconds != 86400 || w.DownSeconds != 360 || w.Incidents != 1 {
		t.Fatalf("24h window = %+v", w)
	}
	if want := 1 - 360.0/86400; day < want-1e-9 || day > want+1e-9 {
		t.Fatalf("24h ratio = %f, want %f", day, want)
	}
	week, w := ratioOf(t, got, "7d")
	if w.ObservedSeconds != 7*86400 || w.DownSeconds != 360 || week <= day {
		t.Fatalf("7d window = %+v (ratio %f)", w, week)
	}
	// The record is ten days old, so the month is a ten-day figure that says so.
	_, month := ratioOf(t, got, "30d")
	if month.CoverageSeconds != 10*86400 || month.ObservedSeconds != 10*86400 || month.Seconds != 30*86400 {
		t.Fatalf("30d window = %+v", month)
	}
}

func TestAvailabilityIsDownNowWhileNothingHasStartedAgain(t *testing.T) {
	now := availabilityNow
	live := []LiveSpan{{ReleaseID: 21, ReleaseNumber: 3, From: now.Add(-2 * time.Hour)}}
	events := []dockerx.Event{
		lifecycle(now.Add(-2*time.Hour), "start", "c1", "21"),
		lifecycle(now.Add(-30*time.Minute), "oom", "c1", "21"),
		lifecycle(now.Add(-30*time.Minute), "die", "c1", "21"),
	}
	got := ComputeAvailability(now, now.Add(-3*24*time.Hour), live, events)
	if !got.Down {
		t.Fatal("an exit with no start after it is down now")
	}
	_, w := ratioOf(t, got, "24h")
	// Observed over the two hours the release has been live, down for the
	// last half of one: the OOM note and the exit are one incident.
	if w.ObservedSeconds != 7200 || w.DownSeconds != 1800 || w.Incidents != 1 {
		t.Fatalf("24h window = %+v", w)
	}
}

// A blue/green cutover retires the old release, whose containers then exit.
// That exit is the old release's and the environment was serving the new one
// throughout, so none of it is downtime.
func TestAvailabilityDoesNotChargeTheOldReleasesExitToTheNewOne(t *testing.T) {
	now := availabilityNow
	cutover := now.Add(-time.Hour)
	live := []LiveSpan{
		{ReleaseID: 20, ReleaseNumber: 2, From: now.Add(-5 * time.Hour), To: cutover},
		{ReleaseID: 21, ReleaseNumber: 3, From: cutover},
	}
	events := []dockerx.Event{
		lifecycle(now.Add(-5*time.Hour), "start", "old", "20"),
		lifecycle(cutover.Add(-time.Minute), "start", "new", "21"),
		// The old release, stopped after the cutover and removed.
		lifecycle(cutover.Add(time.Minute), "kill", "old", "20"),
		lifecycle(cutover.Add(time.Minute), "die", "old", "20"),
		lifecycle(cutover.Add(2*time.Minute), "destroy", "old", "20"),
	}
	got := ComputeAvailability(now, now.Add(-24*time.Hour), live, events)
	ratio, w := ratioOf(t, got, "24h")
	if ratio != 1 || w.DownSeconds != 0 || w.Incidents != 0 || got.Down {
		t.Fatalf("a cutover is not downtime: %+v (down now %v)", w, got.Down)
	}
	// Five hours observed: the old release's four and the new one's one.
	if w.ObservedSeconds != 5*3600 {
		t.Fatalf("observed %d seconds, want five hours of live releases", w.ObservedSeconds)
	}
}

// Two containers of one release down over the same minutes are one stretch
// of downtime, and a health check going red is downtime until it goes green.
func TestAvailabilityUnionsOverlappingDowntimeAndReadsHealthFlips(t *testing.T) {
	now := availabilityNow
	live := []LiveSpan{{ReleaseID: 21, ReleaseNumber: 3, From: now.Add(-24 * time.Hour)}}
	events := []dockerx.Event{
		lifecycle(now.Add(-24*time.Hour), "start", "web", "21"),
		lifecycle(now.Add(-24*time.Hour), "start", "db", "21"),
		lifecycle(now.Add(-60*time.Minute), "die", "db", "21"),
		lifecycle(now.Add(-58*time.Minute), "health_status: unhealthy", "web", "21"),
		lifecycle(now.Add(-50*time.Minute), "start", "db", "21"),
		lifecycle(now.Add(-45*time.Minute), "health_status: healthy", "web", "21"),
		// A pause later on is a second incident.
		lifecycle(now.Add(-10*time.Minute), "pause", "web", "21"),
		lifecycle(now.Add(-8*time.Minute), "unpause", "web", "21"),
	}
	got := ComputeAvailability(now, now.Add(-24*time.Hour), live, events)
	_, w := ratioOf(t, got, "24h")
	if w.DownSeconds != 15*60+2*60 || w.Incidents != 2 {
		t.Fatalf("24h window = %+v, want 17 minutes down over two incidents", w)
	}
}

// A container removed while down stays down for its release until another
// container of the release starts: a stopped and removed container is not
// serving because it is gone.
func TestAvailabilityKeepsARemovedContainerDownUntilItsReleaseComesBack(t *testing.T) {
	now := availabilityNow
	live := []LiveSpan{{ReleaseID: 21, ReleaseNumber: 3, From: now.Add(-4 * time.Hour)}}
	events := []dockerx.Event{
		lifecycle(now.Add(-4*time.Hour), "start", "c1", "21"),
		lifecycle(now.Add(-3*time.Hour), "kill", "c1", "21"),
		lifecycle(now.Add(-3*time.Hour), "die", "c1", "21"),
		lifecycle(now.Add(-3*time.Hour+time.Minute), "destroy", "c1", "21"),
		lifecycle(now.Add(-2*time.Hour), "start", "c2", "21"),
	}
	got := ComputeAvailability(now, now.Add(-24*time.Hour), live, events)
	_, w := ratioOf(t, got, "24h")
	if w.DownSeconds != 3600 || w.Incidents != 1 || got.Down {
		t.Fatalf("24h window = %+v (down now %v), want the hour between the removal and the new container", w, got.Down)
	}
	// Removed and never replaced: down to this moment.
	got = ComputeAvailability(now, now.Add(-24*time.Hour), live, events[:4])
	_, w = ratioOf(t, got, "24h")
	if w.DownSeconds != 3*3600 || !got.Down {
		t.Fatalf("a release whose only container is gone is down: %+v (down now %v)", w, got.Down)
	}
}

func TestAvailabilityHasNoFigureWithoutARecordOrALiveRelease(t *testing.T) {
	now := availabilityNow
	live := []LiveSpan{{ReleaseID: 21, ReleaseNumber: 3, From: now.Add(-time.Hour)}}
	// No event has ever been recorded: nothing to go on, in any window.
	got := ComputeAvailability(now, time.Time{}, live, nil)
	for _, w := range got.Windows {
		if w.Ratio != nil || w.ObservedSeconds != 0 || w.CoverageSeconds != 0 {
			t.Fatalf("a record that has not begun gives no figure: %+v", w)
		}
	}
	if got.RecordSince != nil {
		t.Fatal("no record, no since")
	}
	// A record, but no release ever live: nothing observed.
	got = ComputeAvailability(now, now.Add(-24*time.Hour), nil, nil)
	if _, w := ratioOf(t, got, "24h"); w.Ratio != nil || w.CoverageSeconds != 86400 {
		t.Fatalf("no live release gives no figure, over a covered day: %+v", w)
	}
	if got.LiveRelease != nil {
		t.Fatal("no release is live")
	}
	// A release live before the record began contributes only the covered part.
	got = ComputeAvailability(now, now.Add(-30*time.Minute), live, nil)
	if ratio, w := ratioOf(t, got, "24h"); ratio != 1 || w.ObservedSeconds != 1800 {
		t.Fatalf("only the recorded half hour is observed: %+v", w)
	}
}

// An event about a release that never went live — a candidate failing its
// readiness checks — or about another environment is not this figure's.
func TestAvailabilityIgnoresEventsOfReleasesThatWereNeverLive(t *testing.T) {
	now := availabilityNow
	live := []LiveSpan{{ReleaseID: 21, ReleaseNumber: 3, From: now.Add(-2 * time.Hour)}}
	events := []dockerx.Event{
		lifecycle(now.Add(-2*time.Hour), "start", "c1", "21"),
		lifecycle(now.Add(-30*time.Minute), "start", "cand", "22"),
		lifecycle(now.Add(-29*time.Minute), "die", "cand", "22"),
		// A container event with no release label at all.
		{Time: now.Add(-20 * time.Minute), Type: "container", Action: "die", Name: "stray", ID: "stray", Owner: map[string]string{"environment-id": "7"}},
		// A network is not a container.
		{Time: now.Add(-10 * time.Minute), Type: "network", Action: "destroy", Name: "jd-e7-db-aaaaaaaaaaaa"},
	}
	got := ComputeAvailability(now, now.Add(-24*time.Hour), live, events)
	if ratio, w := ratioOf(t, got, "24h"); ratio != 1 || w.Incidents != 0 || got.Down {
		t.Fatalf("a failed candidate is not the live release's downtime: %+v", w)
	}
}

func TestReleaseSpansEndWhereTheRecordSaysTheReleaseStoppedServing(t *testing.T) {
	st := newTestOrchestrationStore(t)
	ctx := t.Context()
	now := st.now().UTC()
	projectID, environmentID := seedAvailabilityEnvironment(t, st)
	insert := func(number int64, state string, activated, retired, rolledBack time.Time) {
		t.Helper()
		unix := func(at time.Time) int64 {
			if at.IsZero() {
				return 0
			}
			return at.Unix()
		}
		if _, err := st.db.ExecContext(ctx, `INSERT INTO deploy_releases(project_id, environment_id, release_number, state, created_at, activated_at, retired_at, rolled_back_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, projectID, environmentID, number, state, unix(activated), unix(activated), unix(retired), unix(rolledBack)); err != nil {
			t.Fatal(err)
		}
	}
	insert(1, "retained", now.Add(-40*24*time.Hour), now.Add(-35*24*time.Hour), time.Time{}) // out of the window
	insert(2, "retained", now.Add(-35*24*time.Hour), now.Add(-3*time.Hour), time.Time{})
	insert(3, "retained", now.Add(-3*time.Hour), now.Add(-time.Hour), now.Add(-2*time.Hour)) // rolled back, retired later
	insert(4, "live", now.Add(-2*time.Hour), time.Time{}, time.Time{})
	insert(5, "candidate", time.Time{}, time.Time{}, time.Time{}) // never activated

	spans, err := st.ReleaseSpans(ctx, environmentID, now.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 3 {
		t.Fatalf("want releases 2, 3 and 4, got %+v", spans)
	}
	if spans[0].ReleaseNumber != 2 || !spans[0].To.Equal(now.Add(-3*time.Hour).Truncate(time.Second)) {
		t.Errorf("release 2 ends at its retirement: %+v", spans[0])
	}
	if spans[1].ReleaseNumber != 3 || !spans[1].To.Equal(now.Add(-2*time.Hour).Truncate(time.Second)) {
		t.Errorf("release 3 ends when traffic was taken from it, not when it was retired an hour later: %+v", spans[1])
	}
	if spans[2].ReleaseNumber != 4 || !spans[2].To.IsZero() {
		t.Errorf("the live release is open: %+v", spans[2])
	}
}

func newTestOrchestrationStore(t *testing.T) *OrchestrationStore {
	t.Helper()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := NewOrchestrationStore(st)
	s.now = func() time.Time { return availabilityNow }
	return s
}

func seedAvailabilityEnvironment(t *testing.T, s *OrchestrationStore) (projectID, environmentID int64) {
	t.Helper()
	result, err := s.db.Exec(`INSERT INTO deploy_projects(name,repo_path,branch,compose_file,pre_command,post_command,hook_secret,hook_id,enabled,created_at,profile,updated_at)
		VALUES('availability','/srv/app','main','compose.yml','','','sealed','availability-hook',1,1,'web',1)`)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ = result.LastInsertId()
	result, err = s.db.Exec(`INSERT INTO deploy_environments(project_id,name,slug,kind,desired_revision,strategy,expected_downtime,protected,created_at,updated_at)
		VALUES(?,'Production','production','production',1,'blue_green',0,1,1,1)`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ = result.LastInsertId()
	return projectID, environmentID
}
