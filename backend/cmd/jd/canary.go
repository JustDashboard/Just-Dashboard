package main

import (
	"fmt"
	"net/http"
	"strings"
	"text/tabwriter"
	"time"
)

// canaryReading is one release's figures over a canary window.
type canaryReading struct {
	Requests  int      `json:"requests"`
	Failures  int      `json:"failures"`
	ErrorRate float64  `json:"errorRate"`
	P95       *float64 `json:"p95"`
}

// canaryWindow is a canary as the route answers it.
type canaryWindow struct {
	ID          int64          `json:"id"`
	RunID       int64          `json:"runId"`
	State       string         `json:"state"`
	Percent     int            `json:"percent"`
	AutoPromote bool           `json:"autoPromote"`
	Decision    string         `json:"decision"`
	DecidedBy   string         `json:"decidedBy"`
	StartedAt   time.Time      `json:"startedAt"`
	EndsAt      time.Time      `json:"endsAt"`
	Live        *canaryReading `json:"live"`
	Candidate   *canaryReading `json:"candidate"`
	Verdict     *struct {
		Reason string `json:"reason"`
	} `json:"verdict"`
	EndReason         string `json:"endReason"`
	EndMessage        string `json:"endMessage"`
	ReleaseNumber     int64  `json:"releaseNumber"`
	LiveReleaseNumber int64  `json:"liveReleaseNumber"`
}

// The canary verbs: the trial a canary plan's release is on, and the
// operator's word on it.
func (a *app) canaryPromote(args []string) error { return a.decideCanary("promote", args) }

func (a *app) canaryAbort(args []string) error { return a.decideCanary("abort", args) }

func (a *app) canaryTarget(verb string, args []string) (target, error) {
	usage := "canary " + verb + " APP [--env NAME]"
	fs := a.newFlags("canary "+verb, usage)
	env := fs.String("env", "", "environment name or id")
	positional, err := a.parse(fs, args)
	if err != nil {
		return target{}, err
	}
	if err := exactly(positional, 1, usage); err != nil {
		return target{}, err
	}
	return a.resolve(positional[0], *env)
}

func (a *app) canaryStatus(args []string) error {
	t, err := a.canaryTarget("status", args)
	if err != nil {
		return err
	}
	var response struct {
		Canary *canaryWindow `json:"canary"`
	}
	raw, err := a.client.getJSON(t.envPath("/canary"), nil, &response)
	if err != nil {
		return err
	}
	if a.opts.json {
		a.printRaw(raw)
		return nil
	}
	if response.Canary == nil {
		fmt.Fprintf(a.stdout, "%s (%s) has had no canary\n", t.row.Name, t.envName)
		return nil
	}
	a.printCanary(*response.Canary)
	return nil
}

func (a *app) decideCanary(verb string, args []string) error {
	t, err := a.canaryTarget(verb, args)
	if err != nil {
		return err
	}
	var window canaryWindow
	raw, err := a.client.sendJSON(http.MethodPost, t.envPath("/canary/"+verb), nil, nil, &window)
	if err != nil {
		return err
	}
	if a.opts.json {
		a.printRaw(raw)
		return nil
	}
	done := "promoted: it goes live in the run that holds it"
	if verb == "abort" {
		done = fmt.Sprintf("aborted: it is removed and release #%d keeps every request", window.LiveReleaseNumber)
	}
	fmt.Fprintf(a.stdout, "release #%d of %s (%s) is being %s (run %d)\n", window.ReleaseNumber, t.row.Name, t.envName, done, window.RunID)
	return nil
}

func (a *app) printCanary(window canaryWindow) {
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	state := window.State
	switch window.State {
	case "running":
		state = fmt.Sprintf("running until %s", window.EndsAt.Local().Format("15:04:05"))
	case "holding":
		state = "window passed, waiting for promote or abort"
	}
	if window.Decision != "" && (window.State == "running" || window.State == "holding") {
		state += fmt.Sprintf(" (%s asked to %s)", orDash(window.DecidedBy), window.Decision)
	}
	fmt.Fprintf(w, "canary\trelease #%d on %d%% beside release #%d\n", window.ReleaseNumber, window.Percent, window.LiveReleaseNumber)
	fmt.Fprintf(w, "state\t%s\n", state)
	fmt.Fprintf(w, "\trequests\t5xx\tp95\n")
	for _, side := range []struct {
		name    string
		reading *canaryReading
	}{{fmt.Sprintf("release #%d (canary)", window.ReleaseNumber), window.Candidate}, {fmt.Sprintf("release #%d (live)", window.LiveReleaseNumber), window.Live}} {
		if side.reading == nil {
			fmt.Fprintf(w, "%s\t-\t-\t-\n", side.name)
			continue
		}
		p95 := "-"
		if side.reading.P95 != nil {
			p95 = fmt.Sprintf("%.0f ms", *side.reading.P95)
		}
		fmt.Fprintf(w, "%s\t%d\t%.1f%%\t%s\n", side.name, side.reading.Requests, side.reading.ErrorRate*100, p95)
	}
	if window.Verdict != nil && window.Verdict.Reason != "" {
		fmt.Fprintf(w, "reading\t%s\n", window.Verdict.Reason)
	}
	if window.EndMessage != "" {
		fmt.Fprintf(w, "ended\t%s\n", strings.TrimSpace(window.EndMessage))
	}
	_ = w.Flush()
}
