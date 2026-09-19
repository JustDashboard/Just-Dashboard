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
