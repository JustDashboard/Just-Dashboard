package deploy

import (
	"context"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// What a deployment actually served.
//
// The project's Logs tab used to be one thing: the live container's standard
// output. For a modern framework that is a startup banner and then silence —
// a Next.js or Rails production server prints nothing per request — so a page
// that could only show it reported "39 lines" for a deployment serving a
// thousand requests a minute, forever. The record of the requests lives at the
// ingress, which is the one place every request passes through no matter what
// the application chose to write about itself.

// RequestRecord is the held record of what every route served, as this
// package needs it. It is an interface rather than the concrete store so the
// read model stays testable without a Caddy container and a Docker socket.
type RequestRecord interface {
	Window(ctx context.Context, route string, filter accesslog.Filter) (accesslog.Window, error)
}

// RequestWindow is one answer about one deployment's traffic: the rows, the
// readings over the window they came from, what the held record covers, and
// — when there are no rows — the sentence explaining which of the several
// different nothings this is.
type RequestWindow struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	// Driver and Format name which server wrote the record and how, because
	// the two drivers do not record the same things: nginx's stock format
	// carries no request duration, and a latency column drawn from it would
	// be a column of zeros presented as measurements.
	Driver  string `json:"driver,omitempty"`
	Format  string `json:"format,omitempty"`
	Latency bool   `json:"latency"`
	// Complete is false when what is held does not reach the start of the
	// retained record — the seed budget, the cap or a roll cut it — so every
	// figure below is a floor.
	Complete   bool              `json:"complete"`
	ObservedAt time.Time         `json:"observedAt"`
	Entries    []accesslog.Entry `json:"entries"`
	Slowest    []accesslog.Entry `json:"slowest,omitempty"`
	Summary    accesslog.Summary `json:"summary"`
	// Coverage is what the held record is: how far back it reaches, how fresh
	// it is, and the cursor a live tail continues from. It is what lets "no
	// requests in the last 24 hours" be told apart from "the record here only
	// goes back an hour".
	Coverage accesslog.Coverage `json:"coverage"`
}

// ObserveRequests answers the filter over one environment's request record.
func ObserveRequests(ctx context.Context, record RequestRecord, environmentID int64, filter accesslog.Filter) RequestWindow {
	result := RequestWindow{
		Status: "unavailable", ObservedAt: time.Now().UTC(),
		Entries: []accesslog.Entry{}, Summary: accesslog.Summary{Classes: map[string]int{}, Buckets: []accesslog.Bucket{}},
	}
	if record == nil {
		result.Reason = "The proxy is unavailable, so this deployment's requests cannot be read."
		return result
	}
	if environmentID <= 0 {
		result.Reason = "A deployment environment is required to read a request record."
		return result
	}
	window, err := record.Window(ctx, deploymentRouteName(environmentID), filter)
	if err != nil {
		// Transport errors carry socket paths and daemon addresses; the reason
		// shown is the operator's next move, not the daemon's error string.
		result.Reason = "This deployment's request record could not be read. Open Proxy to check the ingress is running."
		return result
	}
	result.Driver, result.Format, result.Latency = window.Facts.Driver, string(window.Facts.Format), window.Facts.Latency
	result.Coverage, result.Complete = window.Coverage, window.Coverage.Complete
	if !window.Coverage.Exists {
		result.Reason = "No request has been recorded for this deployment yet. A record starts with the first request through its public route."
		return result
	}
	result.Status = "available"
	result.Entries, result.Slowest, result.Summary = window.Result.Entries, window.Result.Slowest, window.Result.Summary
	return result
}

// TrafficReading is one window's worth of what the ingress served, small
// enough to sit beside a release or on a card: how much, how much of it
// failed, how slow the slow tenth was, and how many were page views.
type TrafficReading struct {
	Requests  int      `json:"requests"`
	Pages     int      `json:"pages"`
	PerMinute float64  `json:"perMinute"`
	ErrorRate float64  `json:"errorRate"`
	P95       *float64 `json:"p95,omitempty"`
	From      string   `json:"from"`
	Until     string   `json:"until"`
}

func trafficReading(window accesslog.Window, from, until time.Time) TrafficReading {
	reading := TrafficReading{
		Requests: window.Result.Summary.Total, Pages: window.Result.Summary.Pages,
		ErrorRate: window.Result.Summary.ErrorRate,
		From:      from.UTC().Format(time.RFC3339), Until: until.UTC().Format(time.RFC3339),
	}
	if minutes := until.Sub(from).Minutes(); minutes >= 1 {
		reading.PerMinute = float64(reading.Requests) / minutes
	}
	if window.Result.Summary.Latency != nil {
		p95 := window.Result.Summary.Latency.P95
		reading.P95 = &p95
	}
	return reading
}

// RunTraffic is what a release did to the traffic: the same reading taken
// over the half hour before its activation and the half hour after. The
// container metrics beside it say whether the release costs more to run;
// this says whether it serves worse — which is the sentence a deploy tool
// exists to be able to say.
type RunTraffic struct {
	Status                string          `json:"status"`
	Reason                string          `json:"reason,omitempty"`
	ActivationCompletedAt *time.Time      `json:"activationCompletedAt,omitempty"`
	WindowMinutes         int             `json:"windowMinutes"`
	Before                *TrafficReading `json:"before,omitempty"`
	After                 *TrafficReading `json:"after,omitempty"`
	Latency               bool            `json:"latency"`
}

const runTrafficWindow = 30 * time.Minute

// ObserveRunTraffic compares the traffic either side of a run's activation.
// The window after is cut at now for a release that went live recently, and
// the reading says so through its own bounds rather than pretending to a
// full half hour.
func ObserveRunTraffic(ctx context.Context, record RequestRecord, snapshot RunSnapshot, now time.Time) RunTraffic {
	result := RunTraffic{Status: "unavailable", WindowMinutes: int(runTrafficWindow.Minutes())}
	if record == nil {
		result.Reason = "The proxy is unavailable, so this release's traffic cannot be read."
		return result
	}
	releaseID := snapshot.Run.CandidateReleaseID
	if releaseID == 0 {
		releaseID = snapshot.Run.ReleaseID
	}
	if releaseID <= 0 {
		result.Reason = "This run has no recorded release, so there is no activation to compare around."
		return result
	}
	at := runActivationCompleted(snapshot, releaseID)
	if at == nil {
		result.Reason = "This run has no completed activation. Traffic is compared around the moment a release went live."
		return result
	}
	result.ActivationCompletedAt = at
	route := deploymentRouteName(snapshot.Run.EnvironmentID)
	// A window's bounds are inclusive, so the instant of activation belongs
	// to "after" alone — a request served at that exact moment was served by
	// the new release.
	before, err := record.Window(ctx, route, accesslog.Filter{Since: at.Add(-runTrafficWindow), Until: at.Add(-time.Nanosecond), Limit: 1})
	if err != nil {
		result.Reason = "This deployment's request record could not be read. Open Proxy to check the ingress is running."
		return result
	}
	if !before.Coverage.Exists {
		result.Reason = "No request has been recorded for this deployment yet."
		return result
	}
	afterUntil := at.Add(runTrafficWindow)
	if afterUntil.After(now) {
		afterUntil = now
	}
	after, err := record.Window(ctx, route, accesslog.Filter{Since: *at, Until: afterUntil, Limit: 1})
	if err != nil {
		result.Reason = "This deployment's request record could not be read. Open Proxy to check the ingress is running."
		return result
	}
	result.Status, result.Latency = "available", before.Facts.Latency
	b, a := trafficReading(before, at.Add(-runTrafficWindow), *at), trafficReading(after, *at, afterUntil)
	result.Before, result.After = &b, &a
	return result
}

// TrafficPulse is a project's last hour on a card: enough to say whether the
// site is alive and whether it is failing, and a line of one figure per
// minute to draw it.
type TrafficPulse struct {
	Status    string  `json:"status"`
	PerMinute float64 `json:"perMinute"`
	ErrorRate float64 `json:"errorRate"`
	Pages     int     `json:"pages"`
	Points    []int   `json:"points"`
}

// CachedRecord is a record that can answer from what it already holds.
type CachedRecord interface {
	Cached(ctx context.Context, route string, filter accesslog.Filter, maxAge time.Duration) (accesslog.Window, error)
}

// ObserveTrafficPulse reads one project's last hour, content with a record a
// minute old: a fleet of cards asks about every route at once, and a file
// read per card per poll would be exactly the cost the store exists to avoid.
func ObserveTrafficPulse(ctx context.Context, record CachedRecord, environmentID int64, now time.Time) TrafficPulse {
	pulse := TrafficPulse{Status: "unavailable", Points: []int{}}
	if record == nil || environmentID <= 0 {
		return pulse
	}
	since := now.Add(-time.Hour)
	window, err := record.Cached(ctx, deploymentRouteName(environmentID), accesslog.Filter{Since: since, Until: now, Limit: 1}, time.Minute)
	if err != nil || !window.Coverage.Exists {
		return pulse
	}
	pulse.Status = "available"
	pulse.PerMinute = float64(window.Result.Summary.Total) / 60
	pulse.ErrorRate = window.Result.Summary.ErrorRate
	pulse.Pages = window.Result.Summary.Pages
	// One point per minute of the hour, from the chart's own columns, so the
	// card's line and the page's chart agree.
	points := make([]int, 60)
	for _, bucket := range window.Result.Summary.Buckets {
		start, err := time.Parse(time.RFC3339, bucket.Start)
		if err != nil {
			continue
		}
		if i := int(start.Sub(since).Minutes()); i >= 0 && i < 60 {
			points[i] += bucket.Total
		}
	}
	pulse.Points = points
	return pulse
}
