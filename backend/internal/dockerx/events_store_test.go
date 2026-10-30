package dockerx

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// The store is the half of the lifecycle record that survives a restart, so
// what it keeps and what it refuses are the facts worth holding: a
// deployment's events and nobody else's, exactly as they arrived, for thirty
// days at least, with a crash loop sampled rather than kept whole.

func testEventStore(t *testing.T, retention time.Duration) *EventStore {
	t.Helper()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return NewEventStore(st.DB, slog.New(slog.NewTextHandler(io.Discard, nil)), retention)
}

func owned(environment, release string) map[string]string {
	return map[string]string{"managed": "true", "environment-id": environment, "release-id": release, "release-number": "3", "run-id": "42"}
}

func keep(t *testing.T, s *EventStore, ev Event) bool {
	t.Helper()
	if ev.Level == "" {
		ev.Level, ev.Message = describeEvent(ev)
	}
	kept, err := s.Record(context.Background(), ev)
	if err != nil {
		t.Fatal(err)
	}
	return kept
}

func TestEventStoreKeepsADeploymentsEventsAndNobodyElses(t *testing.T) {
	s := testEventStore(t, 0)
	at := time.Date(2026, 9, 27, 4, 0, 0, 123456789, time.UTC)
	if !keep(t, s, Event{Time: at, Type: "container", Action: "die", Name: "api-r3", ID: webID, ExitCode: "137", Source: "daemon", Owner: owned("7", "21")}) {
		t.Fatal("a labelled container's exit is a deployment's event")
	}
	// Networks arrive with no labels at all; the name is what says whose
	// they are, and the separator is what keeps 7 from claiming 70's.
	if !keep(t, s, Event{Time: at.Add(time.Second), Type: "network", Action: "destroy", Name: "jd-e7-db-9f2c1a4b7e05"}) {
		t.Fatal("a deployment's database network is recognised by its name")
	}
	// Environment 70's, which a prefix test without the separator would hand
	// to environment 7: kept, as 70's.
	if !keep(t, s, Event{Time: at.Add(2 * time.Second), Type: "network", Action: "destroy", Name: "jd-e70-db-0011aabbccdd"}) {
		t.Fatal("environment 70's network is a deployment's too")
	}
	if keep(t, s, Event{Time: at.Add(3 * time.Second), Type: "container", Action: "die", Name: "postgres", ID: dbID}) {
		t.Fatal("an operator's own container is the host feed's business, not the table's")
	}
	if keep(t, s, Event{Time: at.Add(4 * time.Second), Type: "image", Action: "pull", Name: "node:22", ID: "node:22"}) {
		t.Fatal("an image pull belongs to no deployment")
	}

	got, err := s.Find(context.Background(), 7, EventFilter{Kinds: []string{"container", "network"}}, time.Time{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"jd-e7-db-9f2c1a4b7e05 destroy", "api-r3 die"}; !slices.Equal(actions(got), want) {
		t.Fatalf("environment 7's record = %v, want %v newest first", actions(got), want)
	}
	// Read back as it arrived: the nanosecond, the code, the labels, the
	// sentence — and no trigger, which is the audit log's to say later.
	died := got[1]
	if !died.Time.Equal(at) || died.ExitCode != "137" || died.Owner["run-id"] != "42" || died.Source != "daemon" {
		t.Fatalf("the exit came back changed: %+v", died)
	}
	if died.Message != "api-r3 exited with status 137" || died.Level != "error" {
		t.Fatalf("the rendered event was not kept: %q %q", died.Level, died.Message)
	}
	if died.Trigger != nil {
		t.Fatal("a correlation is made when the row is read, never frozen into it")
	}
	if other, _ := s.Find(context.Background(), 70, EventFilter{}, time.Time{}, 100); !slices.Equal(actions(other), []string{"jd-e70-db-0011aabbccdd destroy"}) {
		t.Fatalf("environment 70 should hold its own network alone, got %v", actions(other))
	}
}

func TestEventStoreAnswersTheFeedsFilter(t *testing.T) {
	s := testEventStore(t, 0)
	at := time.Date(2026, 9, 27, 4, 0, 0, 0, time.UTC)
	keep(t, s, Event{Time: at, Type: "container", Action: "start", Name: "api-r3", ID: webID, Owner: owned("7", "21")})
	keep(t, s, Event{Time: at.Add(time.Hour), Type: "container", Action: "die", Name: "api-r3", ID: webID, ExitCode: "1", Owner: owned("7", "21")})
	keep(t, s, Event{Time: at.Add(2 * time.Hour), Type: "network", Action: "destroy", Name: "jd-e7-db-9f2c1a4b7e05"})

	ctx := context.Background()
	if got, _ := s.Find(ctx, 7, EventFilter{Kinds: []string{"container"}}, time.Time{}, 100); !slices.Equal(actions(got), []string{"api-r3 die", "api-r3 start"}) {
		t.Fatalf("kinds=container: %v", actions(got))
	}
	if got, _ := s.Find(ctx, 7, EventFilter{}, at.Add(30*time.Minute), 100); !slices.Equal(actions(got), []string{"jd-e7-db-9f2c1a4b7e05 destroy", "api-r3 die"}) {
		t.Fatalf("since half past: %v", actions(got))
	}
	if got, _ := s.Find(ctx, 7, EventFilter{}, time.Time{}, 1); !slices.Equal(actions(got), []string{"jd-e7-db-9f2c1a4b7e05 destroy"}) {
		t.Fatalf("limit 1 keeps the newest: %v", actions(got))
	}
	// The text filter is the ring's, run over the rendered sentence, so the
	// two halves of the feed match the same rows.
	if got, _ := s.Find(ctx, 7, EventFilter{Search: "exited"}, time.Time{}, 100); !slices.Equal(actions(got), []string{"api-r3 die"}) {
		t.Fatalf("search=exited: %v", actions(got))
	}
	// Oldest first for the state machine, containers only.
	timeline, err := s.Timeline(ctx, 7, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(actions(timeline), []string{"api-r3 start", "api-r3 die"}) {
		t.Fatalf("timeline: %v", actions(timeline))
	}
	since, err := s.Since(ctx)
	if err != nil || !since.Equal(at) {
		t.Fatalf("the record reaches back to its oldest row: %s %v", since, err)
	}
}

// An exit that followed a stop or a kill of the same container was asked for;
// one that did not is the process falling over, which is the distinction the
// availability figure and the reader both want on a `die`.
func TestEventStoreSaysWhetherAnExitWasAskedFor(t *testing.T) {
	s := testEventStore(t, 0)
	at := time.Date(2026, 9, 27, 4, 0, 0, 0, time.UTC)
	keep(t, s, Event{Time: at, Type: "container", Action: "die", Name: "api-r3", ID: webID, ExitCode: "1", Owner: owned("7", "21")})
	keep(t, s, Event{Time: at.Add(time.Minute), Type: "container", Action: "kill", Name: "api-r3", ID: webID, Signal: "15", Owner: owned("7", "21")})
	keep(t, s, Event{Time: at.Add(time.Minute + 2*time.Second), Type: "container", Action: "die", Name: "api-r3", ID: webID, ExitCode: "0", Owner: owned("7", "21")})
	// Another container's kill explains nothing about this one, and a kill
	// an hour ago does not explain an exit now.
	keep(t, s, Event{Time: at.Add(2 * time.Hour), Type: "container", Action: "kill", Name: "db-r3", ID: dbID, Signal: "9", Owner: owned("7", "21")})
	keep(t, s, Event{Time: at.Add(2*time.Hour + 5*time.Second), Type: "container", Action: "die", Name: "api-r3", ID: webID, ExitCode: "137", Owner: owned("7", "21")})

	timeline, err := s.Timeline(context.Background(), 7, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	want := []bool{false, true, true, true, false}
	for i, ev := range timeline {
		if ev.Requested != want[i] {
			t.Errorf("%s %s at %s: requested = %v, want %v", ev.Name, ev.Action, ev.Time.Format("15:04:05"), ev.Requested, want[i])
		}
	}
	if timeline[1].Signal != "15" {
		t.Errorf("the signal a kill sent is kept: %q", timeline[1].Signal)
	}
}

func TestEventStorePrunesPastItsRetentionWhichIsThirtyDaysAtLeast(t *testing.T) {
	// A week of metrics history does not shorten the events to a week.
	s := testEventStore(t, 7*24*time.Hour)
	if s.Retention() != DefaultEventRetention {
		t.Fatalf("retention = %s, want the thirty-day floor", s.Retention())
	}
	// A longer metrics history lengthens the events with it.
	if long := testEventStore(t, 90*24*time.Hour); long.Retention() != 90*24*time.Hour {
		t.Fatalf("retention = %s, want the operator's ninety days", long.Retention())
	}
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	keep(t, s, Event{Time: now.Add(-31 * 24 * time.Hour), Type: "container", Action: "oom", Name: "api-r2", ID: oldWeb, Owner: owned("7", "20")})
	keep(t, s, Event{Time: now.Add(-29 * 24 * time.Hour), Type: "container", Action: "die", Name: "api-r2", ID: oldWeb, ExitCode: "137", Owner: owned("7", "20")})
	keep(t, s, Event{Time: now.Add(-time.Hour), Type: "container", Action: "start", Name: "api-r3", ID: webID, Owner: owned("7", "21")})

	pruned, err := s.Prune(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Fatalf("pruned %d rows, want the one past thirty days", pruned)
	}
	got, _ := s.Find(context.Background(), 7, EventFilter{}, time.Time{}, 100)
	if !slices.Equal(actions(got), []string{"api-r3 start", "api-r2 die"}) {
		t.Fatalf("after the prune: %v", actions(got))
	}
}

// A container in a tight loop is written down as its first burst and then a
// sample, so thirty days of one broken container is not ten million rows;
// what is kept still counts every restart Docker's backoff allows at its
// ceiling, and another container's events are not spent on it.
func TestEventStoreSamplesACrashLoop(t *testing.T) {
	s := testEventStore(t, 0)
	at := time.Date(2026, 9, 27, 4, 0, 0, 0, time.UTC)
	kept := 0
	for i := 0; i < 400; i++ {
		// Four events a second: an exit and a start every half second.
		ev := Event{Time: at.Add(time.Duration(i) * 250 * time.Millisecond), Type: "container", Name: "api-r3", ID: webID, Owner: owned("7", "21")}
		if i%2 == 0 {
			ev.Action, ev.ExitCode = "die", "1"
		} else {
			ev.Action = "start"
		}
		if keep(t, s, ev) {
			kept++
		}
	}
	// A hundred seconds of loop: the burst of thirty, then one every ten.
	if kept < eventBurst || kept > eventBurst+12 {
		t.Fatalf("kept %d of 400 looping events, want the burst and then a sample", kept)
	}
	// The sibling is untouched by the loop's spending.
	if !keep(t, s, Event{Time: at.Add(50 * time.Second), Type: "container", Action: "die", Name: "db-r3", ID: dbID, ExitCode: "137", Owner: owned("7", "21")}) {
		t.Fatal("another container's exit was dropped for a loop that was not its own")
	}
	// Once the loop slows to Docker's ceiling of one restart a minute, every
	// event is kept again.
	later := at.Add(time.Hour)
	for i := 0; i < 10; i++ {
		if !keep(t, s, Event{Time: later.Add(time.Duration(i) * time.Minute), Type: "container", Action: "start", Name: "api-r3", ID: webID, Owner: owned("7", "21")}) {
			t.Fatalf("a start a minute apart was dropped (%d)", i)
		}
	}
}

func TestEventStoreCountsOneReleasesRestartsAndKills(t *testing.T) {
	s := testEventStore(t, 0)
	at := time.Date(2026, 9, 27, 4, 0, 0, 0, time.UTC)
	tick := func(i int) time.Time { return at.Add(time.Duration(i) * time.Minute) }
	// Release 21: two containers. The web container came back twice after
	// its first start, once after the kernel stopped it; the db never did.
	keep(t, s, Event{Time: tick(0), Type: "container", Action: "start", Name: "web-r3", ID: webID, Owner: owned("7", "21")})
	keep(t, s, Event{Time: tick(0), Type: "container", Action: "start", Name: "db-r3", ID: dbID, Owner: owned("7", "21")})
	keep(t, s, Event{Time: tick(1), Type: "container", Action: "oom", Name: "web-r3", ID: webID, Owner: owned("7", "21")})
	keep(t, s, Event{Time: tick(1), Type: "container", Action: "die", Name: "web-r3", ID: webID, ExitCode: "137", Owner: owned("7", "21")})
	keep(t, s, Event{Time: tick(2), Type: "container", Action: "start", Name: "web-r3", ID: webID, Owner: owned("7", "21")})
	keep(t, s, Event{Time: tick(3), Type: "container", Action: "die", Name: "web-r3", ID: webID, ExitCode: "1", Owner: owned("7", "21")})
	keep(t, s, Event{Time: tick(4), Type: "container", Action: "start", Name: "web-r3", ID: webID, Owner: owned("7", "21")})
	// The release before it is not this release's count.
	keep(t, s, Event{Time: tick(-10), Type: "container", Action: "oom", Name: "web-r2", ID: oldWeb, Owner: owned("7", "20")})

	counts, err := s.ReleaseCounts(context.Background(), 7, 21)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Restarts != 2 || counts.OOMKills != 1 || counts.Exits["137"] != 1 || counts.Exits["1"] != 1 || len(counts.Exits) != 2 {
		t.Fatalf("release 21 = %+v, want 2 restarts, 1 OOM kill, one exit each of 137 and 1", counts)
	}
	if none, _ := s.ReleaseCounts(context.Background(), 7, 99); none.Restarts != 0 || none.OOMKills != 0 || len(none.Exits) != 0 {
		t.Fatalf("a release never seen counts nothing: %+v", none)
	}
}

// The store hears the ring synchronously, so a burst that would overflow a
// subscriber's channel still reaches the table whole.
func TestEventStoreFollowsTheRingWithoutDroppingABurst(t *testing.T) {
	s := testEventStore(t, 0)
	log := New("unix:///nonexistent").NewEventLog(slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.Follow(context.Background(), log)
	at := time.Date(2026, 9, 27, 4, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		id := string(rune('a'+i)) + webID[1:]
		log.record(Event{Time: at.Add(time.Duration(i) * time.Second), Type: "container", Action: "start", Name: "svc-" + string(rune('a'+i)), ID: id, Owner: owned("7", "21")})
	}
	log.record(Event{Time: at.Add(time.Minute), Type: "container", Action: "start", Name: "elsewhere", ID: dbID})
	got, err := s.Find(context.Background(), 7, EventFilter{}, time.Time{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 20 {
		t.Fatalf("the table holds %d of the burst's 20 events", len(got))
	}
}

func TestAnEventNamesItsEnvironment(t *testing.T) {
	for _, c := range []struct {
		ev   Event
		want int64
		ok   bool
	}{
		{Event{Type: "container", Owner: map[string]string{"environment-id": "7"}}, 7, true},
		{Event{Type: "container", Name: "jd-e7-web"}, 0, false},
		{Event{Type: "network", Name: "jd-e7-db-9f2c1a4b7e05"}, 7, true},
		{Event{Type: "network", Name: "jd-preview-e7"}, 7, true},
		{Event{Type: "network", Name: "jd-preview-e7-1f4c9a0b2d3e4f50"}, 7, true},
		{Event{Type: "network", Name: "jd-preview-e70"}, 70, true},
		{Event{Type: "network", Name: "jd-e70-db-0011aabbccdd"}, 70, true},
		{Event{Type: "network", Name: "jd-e7"}, 0, false},
		{Event{Type: "network", Name: "bridge"}, 0, false},
		{Event{Type: "network", Name: "jd-ex-db"}, 0, false},
	} {
		got, ok := c.ev.EnvironmentID()
		if got != c.want || ok != c.ok {
			t.Errorf("%s %q (%v): environment = %d %v, want %d %v", c.ev.Type, c.ev.Name, c.ev.Owner, got, ok, c.want, c.ok)
		}
	}
}
