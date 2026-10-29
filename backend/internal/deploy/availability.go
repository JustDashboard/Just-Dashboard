package deploy

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// How much of the time a deployment was up.
//
// The figure every status page shows and no self-hosted deploy tool keeps:
// over the last day, week and month, what share of the time the live release
// was running and healthy. It is read from two records this dashboard already
// holds rather than from a probe that would have to be running and would
// measure the probe's network as much as the site. The releases say when the
// environment had something live at all — a release's activation is its
// readiness checks passing, so the span from there to its retirement is time
// the environment was meant to be serving. The container events say when,
// inside that span, it was not: an exit until the start that followed it, an
// OOM kill, a pause until the resume, a health check going red until it went
// green. An operator's stop counts as down, because it was: the figure is
// what a visitor would have found, not whether anybody meant it.
//
// What the record does not reach is not counted either way. Events are kept
// from when the store began writing them down, so a thirty-day window read
// on the third day is a figure over three days, and says so; and a release
// that was live before the first recorded event contributes none of that
// earlier time, since nothing can say whether it was up then.

// AvailabilityWindows are the spans the figure is given over, shortest first.
var AvailabilityWindows = []time.Duration{24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour}

// LiveSpan is the time one release was the environment's live release. To is
// zero while it still is.
type LiveSpan struct {
	ReleaseID     int64
	ReleaseNumber int64
	From          time.Time
	To            time.Time
}

// AvailabilityWindow is the figure over one span of time ending now.
type AvailabilityWindow struct {
	// Window names the span: "24h", "7d", "30d".
	Window  string `json:"window"`
	Seconds int64  `json:"seconds"`
	// ObservedSeconds is the time inside the window a release was live and
	// the record was listening: what the ratio is over. Zero means there is
	// no figure, and Ratio is absent.
	ObservedSeconds int64 `json:"observedSeconds"`
	// DownSeconds is how much of that observed time the live release was
	// not running or not healthy; Incidents is how many separate times.
	DownSeconds int64 `json:"downSeconds"`
	Incidents   int   `json:"incidents"`
	// Ratio is 1 - down/observed, 0..1.
	Ratio *float64 `json:"ratio,omitempty"`
	// CoverageSeconds is how much of the window the event record reaches
	// back over; less than Seconds while the record is younger than the
	// window, which the page says rather than lets a short record read as a
	// long steady one.
	CoverageSeconds int64 `json:"coverageSeconds"`
}

// Availability is one environment's figure over every window.
type Availability struct {
	Status     string    `json:"status"`
	Reason     string    `json:"reason,omitempty"`
	ObservedAt time.Time `json:"observedAt"`
	// RecordSince is when the event record began: the oldest event kept for
	// any deployment, since an environment with no events has been steady
	// only for as long as somebody was listening.
	RecordSince *time.Time `json:"recordSince,omitempty"`
	// LiveRelease is the number of the release live now, when there is one.
	LiveRelease *int64 `json:"liveRelease,omitempty"`
	// Down reports whether the record says the live release is down at this
	// moment: an exit, kill, pause or failing health check with nothing
	// since to end it.
	Down    bool                 `json:"down"`
	Windows []AvailabilityWindow `json:"windows"`
}

// ReleaseSpans reads when each of an environment's releases was live, for
// every release live at any point from `since` on. A release's activation
// is overwritten when it is switched back to after a rollback, so a release
// live twice contributes its later span alone; the time between then reads
// as unobserved rather than as up.
func (s *OrchestrationStore) ReleaseSpans(ctx context.Context, environmentID int64, since time.Time) ([]LiveSpan, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, release_number, state, activated_at, retired_at, rolled_back_at
		  FROM deploy_releases
		 WHERE environment_id = ? AND activated_at > 0
		   AND (state = 'live' OR retired_at >= ? OR rolled_back_at >= ?)
		 ORDER BY activated_at, id`, environmentID, since.Unix(), since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	spans := []LiveSpan{}
	for rows.Next() {
		var span LiveSpan
		var state string
		var activated, retired, rolledBack int64
		if err := rows.Scan(&span.ReleaseID, &span.ReleaseNumber, &state, &activated, &retired, &rolledBack); err != nil {
			return nil, err
		}
		span.From = time.Unix(activated, 0).UTC()
		switch {
		case state == "live":
			// Still live: open.
		case retired > 0 && (rolledBack == 0 || retired <= rolledBack):
			span.To = time.Unix(retired, 0).UTC()
		case rolledBack > 0:
			span.To = time.Unix(rolledBack, 0).UTC()
		default:
			// Activated once and neither live nor retired: nothing says how
			// long it served, so it contributes nothing.
			span.To = span.From
		}
		spans = append(spans, span)
	}
	return spans, rows.Err()
}

// downStart reports whether an action is a container stopping serving, and
// upAgain whether it is one coming back. The health flips are Docker's own
// verdict on a container that is running; an exit is a container that is not.
func downStart(action string) bool {
	return action == "die" || action == "oom" || action == "kill" || action == "pause" || action == "health_status: unhealthy"
}

func upAgain(action string) bool {
	return action == "start" || action == "unpause" || action == "health_status: healthy"
}

type interval struct {
	from, to time.Time
}

// ComputeAvailability is the figure from the releases' live spans and the
// environment's container events, oldest first, with the record's reach.
func ComputeAvailability(now, recordSince time.Time, spans []LiveSpan, events []dockerx.Event) Availability {
	out := Availability{Status: "available", ObservedAt: now.UTC(), Windows: []AvailabilityWindow{}}
	if !recordSince.IsZero() {
		since := recordSince.UTC()
		out.RecordSince = &since
	}
	byRelease := map[int64]LiveSpan{}
	for _, span := range spans {
		byRelease[span.ReleaseID] = span
		if span.To.IsZero() {
			number := span.ReleaseNumber
			out.LiveRelease = &number
		}
	}
	downs, downNow := downIntervals(now, byRelease, events)
	out.Down = downNow
	for _, window := range AvailabilityWindows {
		out.Windows = append(out.Windows, availabilityOver(now, recordSince, window, spans, downs))
	}
	return out
}

// downIntervals walks the events and returns every stretch a live release's
// container was down, clipped to the time its release was live, and whether
// one is open at `now`.
func downIntervals(now time.Time, spans map[int64]LiveSpan, events []dockerx.Event) ([]interval, bool) {
	type open struct {
		release int64
		since   time.Time
	}
	downSince := map[string]open{}
	// A container removed while down stays down for its release until
	// another of the release's containers starts, or the release ends: a
	// stopped and removed container is still not serving.
	orphans := map[int64][]time.Time{}
	var intervals []interval
	clip := func(release int64, from, to time.Time) {
		span, ok := spans[release]
		if !ok {
			return
		}
		end := span.To
		if end.IsZero() {
			end = now
		}
		if from.Before(span.From) {
			from = span.From
		}
		if to.After(end) {
			to = end
		}
		if to.After(from) {
			intervals = append(intervals, interval{from, to})
		}
	}
	releaseOf := func(ev dockerx.Event) (int64, bool) {
		id := labelInt(ev.Owner["release-id"])
		_, ok := spans[id]
		return id, ok
	}
	for _, ev := range events {
		if ev.Type != "container" || ev.Time.IsZero() {
			continue
		}
		release, ok := releaseOf(ev)
		if !ok {
			continue
		}
		key := ev.ID
		if key == "" {
			key = ev.Name
		}
		switch {
		case downStart(ev.Action):
			if _, down := downSince[key]; !down {
				downSince[key] = open{release, ev.Time}
			}
		case upAgain(ev.Action):
			if o, down := downSince[key]; down {
				clip(o.release, o.since, ev.Time)
				delete(downSince, key)
			}
			if ev.Action == "start" {
				for _, since := range orphans[release] {
					clip(release, since, ev.Time)
				}
				delete(orphans, release)
			}
		case ev.Action == "destroy":
			if o, down := downSince[key]; down {
				orphans[o.release] = append(orphans[o.release], o.since)
				delete(downSince, key)
			}
		}
	}
	downNow := false
	for _, o := range downSince {
		clip(o.release, o.since, now)
		if span := spans[o.release]; span.To.IsZero() {
			downNow = true
		}
	}
	for release, sinces := range orphans {
		for _, since := range sinces {
			clip(release, since, now)
		}
		if span := spans[release]; span.To.IsZero() {
			downNow = true
		}
	}
	return mergeIntervals(intervals), downNow
}

// mergeIntervals unions overlapping stretches: two containers of one release
// down at once are one stretch of downtime, not two.
func mergeIntervals(in []interval) []interval {
	if len(in) == 0 {
		return in
	}
	sort.Slice(in, func(i, j int) bool { return in[i].from.Before(in[j].from) })
	out := []interval{in[0]}
	for _, next := range in[1:] {
		last := &out[len(out)-1]
		if !next.from.After(last.to) {
			if next.to.After(last.to) {
				last.to = next.to
			}
			continue
		}
		out = append(out, next)
	}
	return out
}

func availabilityOver(now, recordSince time.Time, window time.Duration, spans []LiveSpan, downs []interval) AvailabilityWindow {
	out := AvailabilityWindow{Window: windowName(window), Seconds: int64(window / time.Second)}
	from := now.Add(-window)
	if recordSince.IsZero() {
		return out
	}
	covered := from
	if recordSince.After(covered) {
		covered = recordSince
	}
	if !covered.Before(now) {
		return out
	}
	out.CoverageSeconds = int64(now.Sub(covered) / time.Second)
	var observed time.Duration
	for _, span := range spans {
		end := span.To
		if end.IsZero() {
			end = now
		}
		observed += overlap(span.From, end, covered, now)
	}
	if observed <= 0 {
		return out
	}
	var down time.Duration
	for _, d := range downs {
		if o := overlap(d.from, d.to, covered, now); o > 0 {
			down += o
			out.Incidents++
		}
	}
	if down > observed {
		down = observed
	}
	out.ObservedSeconds = int64(observed / time.Second)
	out.DownSeconds = int64(down / time.Second)
	ratio := 1 - float64(down)/float64(observed)
	out.Ratio = &ratio
	return out
}

func overlap(aFrom, aTo, bFrom, bTo time.Time) time.Duration {
	from := aFrom
	if bFrom.After(from) {
		from = bFrom
	}
	to := aTo
	if bTo.Before(to) {
		to = bTo
	}
	if !to.After(from) {
		return 0
	}
	return to.Sub(from)
}

// windowName is the label a window goes by — "24h", "7d", "30d" — as the
// page and the exposition both say it.
func windowName(window time.Duration) string {
	if window < 48*time.Hour {
		return strconv.FormatInt(int64(window/time.Hour), 10) + "h"
	}
	return strconv.FormatInt(int64(window/(24*time.Hour)), 10) + "d"
}

func labelInt(raw string) int64 {
	var n int64
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

// Unavailability is the answer when the figure cannot be read at all.
func Unavailability(now time.Time, reason string) Availability {
	return Availability{Status: "unavailable", Reason: reason, ObservedAt: now.UTC(), Windows: []AvailabilityWindow{}}
}
