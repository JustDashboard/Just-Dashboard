package dockerx

import (
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"
)

// recorded is an event log holding these events, oldest first, as the
// follower would have recorded them.
func recorded(events ...Event) *EventLog {
	log := New("unix:///nonexistent").NewEventLog(slog.New(slog.NewTextHandler(io.Discard, nil)))
	base := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	for i, ev := range events {
		ev.Time = base.Add(time.Duration(i) * time.Second)
		ev.Level, ev.Message = describeEvent(ev)
		log.record(ev)
	}
	return log
}

func actions(events []Event) []string {
	out := []string{}
	for _, ev := range events {
		out = append(out, ev.Name+" "+ev.Action)
	}
	return out
}

const (
	webID  = "1111111111111111111111111111111111111111111111111111111111111111"
	dbID   = "2222222222222222222222222222222222222222222222222222222222222222"
	oldWeb = "3333333333333333333333333333333333333333333333333333333333333333"
)

func shopEvents() *EventLog {
	return recorded(
		Event{Type: "container", Action: "start", Name: "shop-web-1", ID: oldWeb, Stack: "shop"},
		Event{Type: "container", Action: "destroy", Name: "shop-web-1", ID: oldWeb, Stack: "shop"},
		Event{Type: "container", Action: "start", Name: "shop-web-1", ID: webID, Stack: "shop"},
		Event{Type: "container", Action: "die", Name: "shop-db-1", ID: dbID, Stack: "shop", ExitCode: "1"},
		Event{Type: "image", Action: "pull", Name: "postgres:17", ID: "postgres:17"},
		Event{Type: "container", Action: "start", Name: "shop-db-1", ID: dbID, Stack: "shop"},
		Event{Type: "container", Action: "start", Name: "blog-web-1", ID: "4444", Stack: "blog"},
		// The daemon sends a network's name and type, never its labels, so
		// compose's own network belongs to no stack as far as an event says.
		Event{Type: "network", Action: "create", Name: "shop_default", ID: "net1"},
	)
}

// A container's page asks by id, and gets that container's events only —
// not the ones of the container it replaced under the same name, and not
// its neighbours'. Twelve characters of the id are enough, as everywhere
// Docker prints one.
func TestEventsForOneContainer(t *testing.T) {
	log := shopEvents()
	for _, ref := range []string{webID, webID[:12]} {
		if got := actions(log.Find(50, EventFilter{Container: ref})); !slices.Equal(got, []string{"shop-web-1 start"}) {
			t.Fatalf("container=%s: %v", ref, got)
		}
	}
	// A name is every container that carried it, newest first.
	got := actions(log.Find(50, EventFilter{Container: "shop-web-1"}))
	want := []string{"shop-web-1 start", "shop-web-1 destroy", "shop-web-1 start"}
	if !slices.Equal(got, want) {
		t.Fatalf("container=shop-web-1: %v, want %v", got, want)
	}
	// Too short to be an id, and nobody's name: nothing, rather than every
	// container whose id happens to start with it.
	if got := log.Find(50, EventFilter{Container: "1111"}); len(got) != 0 {
		t.Fatalf("a short prefix matched %v", actions(got))
	}
}

// A stack's page asks by compose project, and the limit counts the stack's
// events rather than the host's.
func TestEventsForOneStack(t *testing.T) {
	log := shopEvents()
	got := actions(log.Find(50, EventFilter{Stack: "shop"}))
	want := []string{
		"shop-db-1 start", "shop-db-1 die", "shop-web-1 start", "shop-web-1 destroy", "shop-web-1 start",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("stack=shop: %v, want %v", got, want)
	}
	if got := actions(log.Find(2, EventFilter{Stack: "shop"})); !slices.Equal(got, []string{"shop-db-1 start", "shop-db-1 die"}) {
		t.Fatalf("the newest two of the stack's: %v", got)
	}
}

// The host feed's own filters keep their meaning: kinds by object type, an
// empty kind narrowing nothing, search over the visible text.
func TestEventsByKindAndText(t *testing.T) {
	log := shopEvents()
	if got := log.Recent(50, []string{"image"}, ""); !slices.Equal(actions(got), []string{"postgres:17 pull"}) {
		t.Fatalf("kinds=image: %v", actions(got))
	}
	if got := log.Recent(50, []string{""}, ""); len(got) != 8 {
		t.Fatalf("an empty kind narrowed the feed to %d events", len(got))
	}
	if got := log.Recent(50, nil, "EXITED with status 1"); !slices.Equal(actions(got), []string{"shop-db-1 die"}) {
		t.Fatalf("search: %v", actions(got))
	}
	if got := log.Find(50, EventFilter{Stack: "blog", Search: "shop"}); len(got) != 0 {
		t.Fatalf("every narrowing applies at once: %v", actions(got))
	}
}
