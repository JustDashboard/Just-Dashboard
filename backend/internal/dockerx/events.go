package dockerx

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/events"
)

// The Docker event stream, kept rather than merely forwarded.
//
// Everything the daemon does emits an event: a container started, an image
// pulled, a network created, a container killed by the OOM reaper. Docker
// keeps none of them — `docker events` shows you what happens from the moment
// you run it, which means the answer to "what happened at 04:00" is nowhere,
// and the answer to "why did this container restart" is a shrug.
//
// This is the same argument the metrics recorder in internal/metrics makes
// about samples, and it has the same answer: the dashboard is a long-running
// process that is already connected to the thing producing the record, so it
// should be listening while nobody is looking. The buffer is in memory and
// bounded — an event log worth keeping across restarts belongs in the audit
// table, which already holds everything this dashboard itself did. What the
// daemon does on its own is worth an hour of context, not a database.

// Event is one thing that happened, in a shape the UI can render directly.
type Event struct {
	Time time.Time `json:"time"`
	// Type is Docker's object class: container, image, volume, network,
	// daemon, plugin.
	Type   string `json:"type"`
	Action string `json:"action"`
	// Name is the human-facing name of whatever it happened to, resolved from
	// the event's own attributes — Docker puts the container name in there,
	// so this does not cost an inspect against an object that may already be
	// gone.
	Name     string `json:"name"`
	ID       string `json:"id,omitempty"`
	Image    string `json:"image,omitempty"`
	Stack    string `json:"stack,omitempty"`
	ExitCode string `json:"exitCode,omitempty"`
	// Message is the event as a sentence. The raw pair — "container", "die" —
	// is precise and means nothing to somebody who has not read the event
	// reference, and this is the layer where that gets fixed once rather than
	// in every component that shows an event.
	Message string `json:"message"`
	// Level lets the feed be scanned rather than read: "error" for the
	// handful of actions that mean something broke, "notice" for the ones
	// worth noticing, "info" for the rest.
	Level string `json:"level"`

	// Source is who did this, as far as the event itself can say: "compose"
	// when the object carries compose's project labels, "daemon" for something
	// Docker did on its own — a restart policy firing, an OOM kill — and
	// "docker" otherwise.
	//
	// "The dashboard did this" cannot be read from the event: Docker records
	// what happened, never who asked. That answer comes from correlating
	// against the audit log, which the API layer does, and which is why
	// Trigger below is separate from this.
	Source string `json:"source"`

	// Trigger is the audit entry this event was caused by, when one can be
	// matched. The distinction it makes is the one an operator actually wants
	// on an unexplained restart: something in this dashboard did it, or
	// something outside it did.
	Trigger *EventTrigger `json:"trigger,omitempty"`
}

// EventTrigger links a Docker event to the dashboard action that caused it.
type EventTrigger struct {
	AuditID int64  `json:"auditId"`
	Action  string `json:"action"`
	Actor   string `json:"actor"`
	// Confidence is always "likely": the match is a time window and a target
	// name, not a causal record, and two things happening within a few seconds
	// of each other is evidence rather than proof.
	Confidence string `json:"confidence"`
}

// eventBufferSize is roughly an hour of a busy host, and a week of a quiet
// one. A container in a tight restart loop can emit four events a second, so
// the bound is what stops one broken container from evicting the record of
// everything else — which is exactly when the record matters most.
const eventBufferSize = 2000

// EventLog follows the daemon's event stream and keeps the recent past.
type EventLog struct {
	client *Client
	log    *slog.Logger

	mu     sync.RWMutex
	buffer []Event
	// next is the write cursor into a ring, so keeping N events costs no
	// allocation and no copying per event.
	next    int
	filled  bool
	subs    map[int]chan Event
	nextSub int
	// running reports whether the stream is currently connected, so the UI
	// can distinguish "nothing happened" from "not listening".
	running bool
	since   time.Time
}

func (c *Client) NewEventLog(log *slog.Logger) *EventLog {
	return &EventLog{
		client: c,
		log:    log,
		buffer: make([]Event, eventBufferSize),
		subs:   map[int]chan Event{},
	}
}

// Start follows the stream until the context ends, reconnecting on failure.
//
// Docker being unavailable is not an error here: a host with no daemon still
// serves the rest of the dashboard, and a recorder that logged a failure every
// five seconds would be the noisiest thing in the process log.
func (e *EventLog) Start(ctx context.Context) {
	go func() {
		backoff := time.Second
		for {
			if ctx.Err() != nil {
				return
			}
			if err := e.follow(ctx); err != nil && ctx.Err() == nil {
				e.setRunning(false)
				// Logged at debug: on a host without Docker this is the
				// steady state, not an incident.
				e.log.Debug("docker event stream ended", "error", err, "retry_in", backoff)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}()
}

func (e *EventLog) follow(ctx context.Context) error {
	cli, err := e.client.api()
	if err != nil {
		return err
	}
	msgs, errs := cli.Events(ctx, events.ListOptions{})
	e.setRunning(true)
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errs:
			return err
		case msg := <-msgs:
			if worthKeeping(msg) {
				e.record(convertEvent(msg))
			}
		}
	}
}

// worthKeeping drops the events that are the daemon answering a question
// rather than something happening to the server.
//
// This is not tidiness, it is what makes the buffer work at all. A container
// with a health check that shells out emits exec_create, exec_start and
// exec_die every interval — three events per container per thirty seconds, so
// eight containers produce nearly fifty thousand a day. The ring is two
// thousand entries, so within about twenty minutes the record of an overnight
// OOM kill has been evicted by health checks reporting success. The events
// this exists to keep are precisely the rare ones.
func worthKeeping(msg events.Message) bool {
	action := string(msg.Action)
	switch {
	case strings.HasPrefix(action, "exec_"):
		return false
	case action == "top", action == "resize", action == "archive-path", action == "extract-to-dir":
		return false
	case msg.Type == events.NetworkEventType && (action == "connect" || action == "disconnect"):
		// Emitted for every container a compose project starts, and already
		// implied by the container's own create/start.
		return false
	case msg.Type == events.VolumeEventType && (action == "mount" || action == "unmount"):
		return false
	case msg.Type == events.ImageEventType && action == "prune":
		return false
	default:
		return true
	}
}

func (e *EventLog) setRunning(v bool) {
	e.mu.Lock()
	e.running = v
	if v && e.since.IsZero() {
		e.since = time.Now().UTC()
	}
	e.mu.Unlock()
}

func (e *EventLog) record(ev Event) {
	e.mu.Lock()
	e.buffer[e.next] = ev
	e.next = (e.next + 1) % len(e.buffer)
	if e.next == 0 {
		e.filled = true
	}
	subs := make([]chan Event, 0, len(e.subs))
	for _, ch := range e.subs {
		subs = append(subs, ch)
	}
	e.mu.Unlock()

	for _, ch := range subs {
		// Never block on a slow subscriber: a browser tab that has stopped
		// reading must not stall the recorder for every other reader.
		select {
		case ch <- ev:
		default:
		}
	}
}

// Recent returns the buffered events, newest first.
//
// `kinds` filters by object type and `search` by any of the visible text,
// both applied here rather than in the browser: the buffer is two thousand
// entries and sending all of them so the client can show forty is bandwidth
// spent on nothing, in the same spirit as the server-side log filtering the
// rest of the dashboard already does.
func (e *EventLog) Recent(limit int, kinds []string, search string) []Event {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if limit <= 0 || limit > eventBufferSize {
		limit = 200
	}
	want := map[string]bool{}
	for _, k := range kinds {
		if k != "" {
			want[k] = true
		}
	}
	needle := strings.ToLower(strings.TrimSpace(search))

	out := make([]Event, 0, limit)
	count := len(e.buffer)
	if !e.filled {
		count = e.next
	}
	for i := 0; i < count && len(out) < limit; i++ {
		// Walk backwards from the newest.
		idx := (e.next - 1 - i + len(e.buffer)) % len(e.buffer)
		ev := e.buffer[idx]
		if ev.Time.IsZero() {
			continue
		}
		if len(want) > 0 && !want[ev.Type] {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(ev.Message+" "+ev.Name+" "+ev.Image), needle) {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// Status says whether the stream is connected and since when, so an empty feed
// can explain itself.
func (e *EventLog) Status() (running bool, since time.Time, buffered int) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	count := e.next
	if e.filled {
		count = len(e.buffer)
	}
	return e.running, e.since, count
}

// Subscribe returns a channel of events as they arrive, and a function to stop.
func (e *EventLog) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	e.mu.Lock()
	id := e.nextSub
	e.nextSub++
	e.subs[id] = ch
	e.mu.Unlock()
	return ch, func() {
		e.mu.Lock()
		delete(e.subs, id)
		e.mu.Unlock()
		close(ch)
	}
}

func convertEvent(msg events.Message) Event {
	attrs := msg.Actor.Attributes
	if attrs == nil {
		attrs = map[string]string{}
	}
	ev := Event{
		Type:     string(msg.Type),
		Action:   string(msg.Action),
		ID:       msg.Actor.ID,
		Name:     attrs["name"],
		Image:    attrs["image"],
		Stack:    attrs[labelProject],
		ExitCode: attrs["exitCode"],
	}
	if msg.TimeNano > 0 {
		ev.Time = time.Unix(0, msg.TimeNano).UTC()
	} else if msg.Time > 0 {
		ev.Time = time.Unix(msg.Time, 0).UTC()
	} else {
		ev.Time = time.Now().UTC()
	}
	if ev.Name == "" {
		// Images and volumes identify themselves by the id, which for an
		// image is its tag and for a volume is its name.
		ev.Name = ShortID(msg.Actor.ID)
	}
	ev.Source = eventSource(ev)
	ev.Level, ev.Message = describeEvent(ev)
	return ev
}

// eventSource says what kind of thing produced an event, from the event alone.
//
// Deliberately does not try to say "the dashboard did this": Docker records
// what happened and never who asked, so any claim about a human actor has to
// come from the audit log. What can be read here is whether compose owns the
// object, and whether the daemon acted on its own.
func eventSource(ev Event) string {
	switch {
	case ev.Type == "daemon":
		return "daemon"
	case ev.Action == "oom" || ev.Action == "die" || strings.HasPrefix(ev.Action, "health_status"):
		// Nobody asks for these. They are the daemon and the kernel reporting
		// something that happened to a container rather than to it.
		return "daemon"
	case ev.Stack != "":
		return "compose"
	default:
		return "docker"
	}
}

// describeEvent turns the object/action pair into a sentence and a severity.
//
// The list is deliberately not exhaustive. Docker emits about seventy actions
// and most of them — `exec_start`, `top`, `archive-path` — are the daemon
// answering a question, not something that happened to the server. Those fall
// through to a generic rendering rather than being enumerated, so the feed
// stays a record of changes rather than an API access log.
func describeEvent(ev Event) (level, message string) {
	name := ev.Name
	switch ev.Type {
	case "container":
		switch ev.Action {
		case "create":
			return "info", name + " was created"
		case "start":
			return "notice", name + " started"
		case "stop":
			return "notice", name + " was asked to stop"
		case "die":
			if ev.ExitCode != "" && ev.ExitCode != "0" {
				return "error", fmt.Sprintf("%s exited with status %s", name, ev.ExitCode)
			}
			return "notice", name + " exited cleanly"
		case "kill":
			return "notice", name + " was killed"
		case "oom":
			// The single most useful event Docker emits, and the one that is
			// nowhere to be found afterwards without this.
			return "error", name + " ran out of memory"
		case "restart":
			return "notice", name + " restarted"
		case "pause":
			return "notice", name + " was paused"
		case "unpause":
			return "notice", name + " was resumed"
		case "destroy":
			return "notice", name + " was removed"
		case "rename":
			return "info", name + " was renamed"
		case "update":
			return "info", name + "'s limits were changed"
		case "health_status: healthy":
			return "notice", name + " is healthy again"
		case "health_status: unhealthy":
			return "error", name + " is failing its health check"
		}
		if strings.HasPrefix(ev.Action, "health_status") {
			return "notice", name + ": " + ev.Action
		}
	case "image":
		switch ev.Action {
		case "pull":
			return "notice", "pulled " + name
		case "push":
			return "info", "pushed " + name
		case "delete":
			return "notice", "deleted image " + name
		case "tag":
			return "info", "tagged " + name
		case "untag":
			return "info", "untagged " + name
		}
	case "volume":
		switch ev.Action {
		case "create":
			return "info", "created volume " + name
		case "destroy":
			return "notice", "deleted volume " + name
		case "mount":
			return "info", "volume " + name + " was mounted"
		case "unmount":
			return "info", "volume " + name + " was unmounted"
		}
	case "network":
		switch ev.Action {
		case "create":
			return "info", "created network " + name
		case "destroy":
			return "notice", "deleted network " + name
		case "connect":
			return "info", "a container joined network " + name
		case "disconnect":
			return "info", "a container left network " + name
		}
	case "daemon":
		return "notice", "the Docker daemon " + ev.Action
	}
	return "info", strings.TrimSpace(ev.Type + " " + ev.Action + " " + name)
}
