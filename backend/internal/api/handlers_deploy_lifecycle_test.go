package api

import (
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// The Events view is the reading that tells "silent because it logs nothing"
// apart from "silent because it died", so what it leaves out matters as much as
// what it shows: another deployment's restart on the same host is not this
// deployment's news, and "this dashboard did it" is a claim that has to be
// earned rather than guessed at.

func event(at time.Time, kind, action, name string, owner map[string]string) dockerx.Event {
	return dockerx.Event{Time: at, Type: kind, Action: action, Name: name, Owner: owner}
}

func owner(environmentID string) map[string]string {
	return map[string]string{"managed": "true", "environment-id": environmentID}
}

func TestLifecycleFeedShowsOnlyThisEnvironmentsEvents(t *testing.T) {
	now := time.Now().UTC()
	buffer := []dockerx.Event{
		event(now, "container", "die", "jd-e6-r1", owner("6")),
		event(now.Add(-time.Minute), "container", "start", "jd-e21-r1", owner("21")),
		// Docker sends a network's `name` and `type` and nothing else — no
		// labels, ever — so this is how a real one arrives and the name is the
		// only thing left to recognise it by.
		event(now.Add(-2*time.Minute), "network", "destroy", "jd-e6-db-9f2c1a4b7e05", nil),
		// Environment 60's, which a prefix test without the separator would
		// hand to environment 6.
		event(now.Add(-3*time.Minute), "network", "destroy", "jd-e60-db-0011aabbccdd", nil),
		// A container nobody here owns: an operator's own compose stack.
		event(now.Add(-4*time.Minute), "container", "die", "postgres", nil),
	}

	got := ownedByEnvironment(buffer, 6, time.Time{}, 100)
	if len(got) != 2 {
		t.Fatalf("want this environment's two events, got %d: %+v", len(got), got)
	}
	if got[0].Name != "jd-e6-r1" || got[1].Name != "jd-e6-db-9f2c1a4b7e05" {
		t.Errorf("newest-first order not preserved: %s, %s", got[0].Name, got[1].Name)
	}
}

// A preview's network is named for the environment it belongs to as well, and
// both of its spellings have to be recognised without swallowing a longer id.
func TestAPreviewNetworkIsRecognisedByItsName(t *testing.T) {
	for _, c := range []struct {
		name  string
		owned bool
	}{
		{"jd-preview-e7", true},
		{"jd-preview-e7-1f4c9a0b2d3e4f50", true},
		{"jd-e7-db-9f2c1a4b7e05", true},
		{"jd-preview-e70", false},
		{"jd-preview-e70-1f4c9a0b2d3e4f50", false},
		{"jd-e70-db-9f2c1a4b7e05", false},
		{"bridge", false},
	} {
		got := ownsEvent(dockerx.Event{Type: "network", Name: c.name}, 7)
		if got != c.owned {
			t.Errorf("%s: owned by environment 7 = %v, want %v", c.name, got, c.owned)
		}
	}
	// The name is a fallback for networks alone. A container always carries
	// the labels, so reading its name instead would be guessing.
	if ownsEvent(dockerx.Event{Type: "container", Name: "jd-e7-r1"}, 7) {
		t.Error("a container is identified by its labels, not by what it is called")
	}
}

// The window is served here rather than computed in the browser, so it has to
// be the bound the caller asked for and not the one the buffer happens to hold.
func TestLifecycleFeedHonoursTheWindowAndTheLimit(t *testing.T) {
	now := time.Now().UTC()
	buffer := []dockerx.Event{
		event(now, "container", "die", "newest", owner("6")),
		event(now.Add(-30*time.Minute), "container", "start", "middle", owner("6")),
		event(now.Add(-2*time.Hour), "container", "start", "oldest", owner("6")),
	}

	within := ownedByEnvironment(buffer, 6, now.Add(-time.Hour), 100)
	if len(within) != 2 {
		t.Fatalf("an hour holds two of these events, got %d", len(within))
	}
	if capped := ownedByEnvironment(buffer, 6, time.Time{}, 1); len(capped) != 1 || capped[0].Name != "newest" {
		t.Fatalf("limit ignored or applied to the wrong end: %+v", capped)
	}
	// Never nil: an empty feed is rendered as "nothing happened", and a null
	// where the client expects a list is rendered as a crash.
	if empty := ownedByEnvironment(buffer, 99, time.Time{}, 100); empty == nil || len(empty) != 0 {
		t.Fatalf("an environment with no events should read as an empty list, got %+v", empty)
	}
}

// A network disappearing under a running deployment is exactly the kind of
// thing the feed exists for, so the default is not containers alone.
func TestLifecycleQueryDefaultsToContainersAndNetworks(t *testing.T) {
	base := lifecycleQueryFrom(url.Values{})
	if len(base.kinds) != 2 || base.kinds[0] != "container" || base.kinds[1] != "network" {
		t.Fatalf("default kinds are containers and networks, got %v", base.kinds)
	}
	if !base.since.IsZero() || base.search != "" {
		t.Fatalf("an empty question should carry no window and no needle: %+v", base)
	}

	asked := lifecycleQueryFrom(url.Values{
		"kinds":  {"container"},
		"search": {"  oom  "},
		"since":  {"2026-09-19T23:00:00Z"},
	})
	if len(asked.kinds) != 1 || asked.kinds[0] != "container" {
		t.Errorf("kinds not taken from the question: %v", asked.kinds)
	}
	if asked.search != "oom" {
		t.Errorf("the needle should be trimmed, got %q", asked.search)
	}
	if !asked.since.Equal(time.Date(2026, 9, 19, 23, 0, 0, 0, time.UTC)) {
		t.Errorf("since not parsed: %s", asked.since)
	}
	// An unparseable window is no window rather than an error: the feed is a
	// reading, and refusing to draw it over a malformed query parameter is
	// worse than drawing all of it.
	if junk := lifecycleQueryFrom(url.Values{"since": {"yesterday"}}); !junk.since.IsZero() {
		t.Errorf("a junk window should be ignored, got %s", junk.since)
	}
}

func correlated(t *testing.T, s *Server, project string, events []dockerx.Event) []dockerx.Event {
	t.Helper()
	r := httptest.NewRequest("GET", "/api/v1/deploy/3/lifecycle", nil)
	s.correlateDeploymentEvents(r, events, project)
	return events
}

// A release is audited against the project, never the container it goes on to
// create, and the container appears minutes later when the build is done — so
// the match the host feed makes (this exact name, seconds either side) finds
// nothing here, and the feed could never say a deploy did anything.
func TestAReleaseIsNamedAsTheCauseOfItsOwnContainers(t *testing.T) {
	s := testServer(t)
	now := time.Now().UTC()
	s.Audit.Record(t.Context(), audit.Entry{
		TS: now.Add(-4 * time.Minute), Action: "deploy.run", Target: "lampino",
		Username: "wayy", Success: true,
	})

	events := correlated(t, s, "lampino", []dockerx.Event{
		event(now, "container", "start", "jd-e6-r2", owner("6")),
	})
	if events[0].Trigger == nil {
		t.Fatal("a release four minutes before its container started explains it")
	}
	if events[0].Trigger.Action != "deploy.run" || events[0].Trigger.Actor != "wayy" {
		t.Errorf("the entry offered is not the release: %+v", events[0].Trigger)
	}
	if events[0].Trigger.Confidence != "likely" {
		t.Errorf("a window and a name is evidence, not proof: %q", events[0].Trigger.Confidence)
	}
	if events[0].Source != "dashboard" {
		t.Errorf("source should say the dashboard did it, got %q", events[0].Source)
	}
}

// The direction is the whole guard. A symmetric window would let a release
// started at 10:05 explain a container that died at 10:01, which reads to an
// operator as "the deploy broke it" when the truth is the reverse.
func TestAReleaseNeverExplainsWhatHappenedBeforeIt(t *testing.T) {
	s := testServer(t)
	now := time.Now().UTC()
	s.Audit.Record(t.Context(), audit.Entry{
		TS: now, Action: "deploy.run", Target: "lampino", Username: "wayy", Success: true,
	})

	// A start rather than an exit, so this holds the direction alone: an exit
	// is refused for a different reason and would pass this whatever the
	// window did.
	events := correlated(t, s, "lampino", []dockerx.Event{
		event(now.Add(-2*time.Minute), "container", "start", "jd-e6-r1", owner("6")),
	})
	if events[0].Trigger != nil {
		t.Fatalf("an entry written after the event cannot have caused it: %+v", events[0].Trigger)
	}
}

func TestAReleaseStopsExplainingThingsOnceItIsOldEnough(t *testing.T) {
	s := testServer(t)
	now := time.Now().UTC()
	s.Audit.Record(t.Context(), audit.Entry{
		TS: now.Add(-deployCorrelationWindow - time.Minute), Action: "deploy.run",
		Target: "lampino", Username: "wayy", Success: true,
	})

	events := correlated(t, s, "lampino", []dockerx.Event{
		event(now, "container", "start", "jd-e6-r2", owner("6")),
	})
	if events[0].Trigger != nil {
		t.Fatalf("a release older than the window is not this container's cause: %+v", events[0].Trigger)
	}
}

// An exit, the kernel's reaper and a health check are the daemon reporting
// something that happened *to* a container. Filing those under "this
// dashboard" sends an operator to the audit log for an answer that is not
// there — and an exit is the one the reader came to the page to explain, so a
// wrong answer about it is the expensive one.
func TestTheDashboardIsNeverBlamedForAnExitOOMKillOrHealthFlip(t *testing.T) {
	s := testServer(t)
	now := time.Now().UTC()
	s.Audit.Record(t.Context(), audit.Entry{
		TS: now.Add(-time.Minute), Action: "deploy.run", Target: "lampino",
		Username: "wayy", Success: true,
	})

	for _, action := range []string{"die", "oom", "health_status: unhealthy"} {
		events := correlated(t, s, "lampino", []dockerx.Event{
			event(now, "container", action, "jd-e6-r1", owner("6")),
		})
		if events[0].Trigger != nil {
			t.Errorf("%s is the daemon's doing, not a release's: %+v", action, events[0].Trigger)
		}
	}
}

func TestAnotherProjectsReleaseIsNotThisDeploymentsCause(t *testing.T) {
	s := testServer(t)
	now := time.Now().UTC()
	s.Audit.Record(t.Context(), audit.Entry{
		TS: now.Add(-time.Minute), Action: "deploy.run", Target: "some-other-project",
		Username: "wayy", Success: true,
	})

	events := correlated(t, s, "lampino", []dockerx.Event{
		event(now, "container", "start", "jd-e6-r2", owner("6")),
	})
	if events[0].Trigger != nil {
		t.Fatalf("a release of another project explains nothing here: %+v", events[0].Trigger)
	}
}

// An entry naming this exact container is a better answer than one naming the
// project it belongs to, so the container-level match is tried first and kept.
func TestAnActionOnTheContainerItselfWinsOverTheRelease(t *testing.T) {
	s := testServer(t)
	now := time.Now().UTC()
	s.Audit.Record(t.Context(), audit.Entry{
		TS: now.Add(-3 * time.Minute), Action: "deploy.run", Target: "lampino",
		Username: "wayy", Success: true,
	})
	s.Audit.Record(t.Context(), audit.Entry{
		TS: now.Add(-1 * time.Second), Action: "docker.container.restart", Target: "jd-e6-r1",
		Username: "someone-else", Success: true,
	})

	events := correlated(t, s, "lampino", []dockerx.Event{
		event(now, "container", "restart", "jd-e6-r1", owner("6")),
	})
	if events[0].Trigger == nil {
		t.Fatal("an audit entry naming this container explains this event")
	}
	if events[0].Trigger.Action != "docker.container.restart" {
		t.Errorf("the nearer, more specific entry should win: %+v", events[0].Trigger)
	}
}

// A failed request changed nothing, so it cannot be what happened.
func TestARefusedActionIsNotOfferedAsACause(t *testing.T) {
	s := testServer(t)
	now := time.Now().UTC()
	s.Audit.Record(t.Context(), audit.Entry{
		TS: now.Add(-time.Minute), Action: "deploy.run", Target: "lampino",
		Username: "wayy", Success: false,
	})

	events := correlated(t, s, "lampino", []dockerx.Event{
		event(now, "container", "start", "jd-e6-r2", owner("6")),
	})
	if events[0].Trigger != nil {
		t.Fatalf("a deploy that failed did not start this container: %+v", events[0].Trigger)
	}
}
