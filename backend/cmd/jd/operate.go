package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"text/tabwriter"
	"time"
)

// The verbs beside a deploy, on the deployment's production environment
// unless --env says otherwise: promote staging's release, keep the new
// release or switch back to the warm one (jd rollback --instant), the
// maintenance switch, and status, which reads where an environment stands
// in one answer. Each is a route the dashboard's own pages use; what a key
// may do on it is the server's decision, explained in its words.

// environmentSummary is an environment as the environments route lists it.
type environmentSummary struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	Kind          string `json:"kind"`
	LiveReleaseID int64  `json:"liveReleaseId"`
	PromotionOnly bool   `json:"promotionOnly"`
}

func (a *app) environments(t target) ([]environmentSummary, []byte, error) {
	var out []environmentSummary
	raw, err := a.client.getJSON(fmt.Sprintf("/deploy/%d/environments", t.id()), nil, &out)
	if err != nil {
		return nil, nil, err
	}
	return out, raw, nil
}

// promote queues a production run that activates staging's release: its
// live one, or the one --release names. With --from-preview it queues a
// staging run that activates a pull request preview's live release instead.
func (a *app) promote(args []string) error {
	const usage = "promote APP [--release ID | --from-preview N] [--wait]"
	fs := a.newFlags("promote", usage)
	releaseID := fs.Int64("release", 0, "the staging release id to promote (default: staging's live release)")
	fromPreview := fs.Int("from-preview", 0, "promote pull request N's preview into staging instead")
	wait := fs.Bool("wait", false, "follow the run and exit with its result")
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if err := exactly(positional, 1, usage); err != nil {
		return err
	}
	if *fromPreview < 0 || (*fromPreview > 0 && *releaseID != 0) {
		return usagef("usage: jd %s: --from-preview takes the preview's live release, so it goes without --release", usage)
	}
	// A promotion lands in production, which is the environment the fleet
	// names for the deployment, so there is no --env here; a preview's lands
	// in staging, which is found by its kind.
	t, err := a.resolve(positional[0], "")
	if err != nil {
		return err
	}
	if *fromPreview > 0 {
		return a.promotePreview(t, *fromPreview, *wait)
	}
	from := ""
	if *releaseID <= 0 {
		environments, _, err := a.environments(t)
		if err != nil {
			return err
		}
		var staging *environmentSummary
		for i := range environments {
			if environments[i].Kind == "staging" {
				staging = &environments[i]
			}
		}
		if staging == nil {
			return fmt.Errorf("%s has no staging environment to promote from; add one on its Environments page", t.row.Name)
		}
		if staging.LiveReleaseID == 0 {
			return fmt.Errorf("%s's %s has no live release to promote; deploy it first: jd deploy %s --env %d --wait",
				t.row.Name, strings.ToLower(orDash(staging.Name)), t.row.Name, staging.ID)
		}
		*releaseID = staging.LiveReleaseID
		from = " (" + strings.ToLower(orDash(staging.Name)) + "'s live release)"
	}
	var run engineRun
	raw, err := a.client.sendJSON(http.MethodPost, t.envPath("/promote"), nil, map[string]any{"releaseId": *releaseID}, &run)
	if err != nil {
		return err
	}
	return a.afterQueue(t, run, raw, *wait, fmt.Sprintf("Promoting release %d%s to", *releaseID, from))
}

// promotePreview promotes pull request number's preview into the
// deployment's staging environment. Whether the preview may go — approved,
// live, its image still here — is the server's decision, in its words.
func (a *app) promotePreview(t target, number int, wait bool) error {
	environments, _, err := a.environments(t)
	if err != nil {
		return err
	}
	var staging *environmentSummary
	for i := range environments {
		if environments[i].Kind == "staging" {
			staging = &environments[i]
		}
	}
	if staging == nil {
		return fmt.Errorf("%s has no staging environment to promote PR #%d's preview into; add one on its Environments page", t.row.Name, number)
	}
	t.env, t.envName = staging.ID, orDash(staging.Name)
	var run engineRun
	raw, err := a.client.sendJSON(http.MethodPost, t.envPath("/promote"), nil,
		map[string]any{"source": "preview", "preview": number}, &run)
	if err != nil {
		return err
	}
	return a.afterQueue(t, run, raw, wait, fmt.Sprintf("Promoting PR #%d's preview to", number))
}

// rollbackWatch is the window route's answer: the terms, and the open or
// most recent window.
type rollbackWatch struct {
	Policy struct {
		KeepWarm      bool `json:"keepWarm"`
		WindowSeconds int  `json:"windowSeconds"`
		Automatic     bool `json:"automatic"`
	} `json:"policy"`
	Watch   *watchWindow `json:"watch"`
	Preview bool         `json:"preview"`
}

type watchWindow struct {
	State             string     `json:"state"`
	ReleaseNumber     int64      `json:"releaseNumber"`
	WarmReleaseNumber int64      `json:"warmReleaseNumber"`
	ExpiresAt         time.Time  `json:"expiresAt"`
	EndReason         string     `json:"endReason"`
	EndMessage        string     `json:"endMessage"`
	EndedAt           *time.Time `json:"endedAt"`
	RollbackRunID     int64      `json:"rollbackRunId"`
	// WarmRunning is a warm release skew protection keeps running for the
	// visitors it answered first; WarmServed what it answered since.
	WarmRunning bool `json:"warmRunning"`
	WarmServed  *struct {
		Requests int `json:"requests"`
	} `json:"warmServed"`
}

// keep ends the warm release's window now: the new release stays, and the
// warm one gives its memory back.
func (a *app) keep(args []string) error {
	fs := a.newFlags("keep", "keep APP [--env NAME]")
	env := fs.String("env", "", "environment name or id")
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if err := exactly(positional, 1, "keep APP [--env NAME]"); err != nil {
		return err
	}
	t, err := a.resolve(positional[0], *env)
	if err != nil {
		return err
	}
	var closed watchWindow
	raw, err := a.client.sendJSON(http.MethodPost, t.envPath("/rollback-watch/keep"), nil, nil, &closed)
	if err != nil {
		return err
	}
	if a.opts.json {
		a.printRaw(raw)
		return nil
	}
	fmt.Fprintf(a.stdout, "kept release #%d of %s (%s); the warm release #%d was retired\n",
		closed.ReleaseNumber, t.row.Name, t.envName, closed.WarmReleaseNumber)
	return nil
}

// maintenanceSwitch is where the switch stands, as the route answers it.
type maintenanceSwitch struct {
	Driver string     `json:"driver"`
	Route  bool       `json:"route"`
	Ready  bool       `json:"ready"`
	On     bool       `json:"on"`
	Since  *time.Time `json:"since"`
}

// maintenance turns the switch on or off at once. Who is let past is part
// of the configuration, where --bypass writes it, and reaches the route with
// the next release, as every configuration change does.
func (a *app) maintenance(args []string) error {
	fs := a.newFlags("maintenance", "maintenance APP on|off [--bypass CIDR,...] [--env NAME]")
	env := fs.String("env", "", "environment name or id")
	var bypass stringList
	fs.Var(&bypass, "bypass", "addresses and ranges let past maintenance, comma-separated (an empty value clears them); saved in the configuration, applied by the next deploy")
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if err := exactly(positional, 2, "maintenance APP on|off [--bypass CIDR,...] [--env NAME]"); err != nil {
		return err
	}
	var on bool
	switch positional[1] {
	case "on":
		on = true
	case "off":
	default:
		return usagef("usage: jd maintenance APP on|off [--bypass CIDR,...]")
	}
	t, err := a.resolve(positional[0], *env)
	if err != nil {
		return err
	}
	bypassGiven := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "bypass" {
			bypassGiven = true
		}
	})
	if bypassGiven {
		if err := a.setBypass(t, []string(bypass)); err != nil {
			return err
		}
	}
	var state maintenanceSwitch
	raw, err := a.client.sendJSON(http.MethodPut, t.envPath("/maintenance"), nil, map[string]any{"on": on}, &state)
	if err != nil {
		return err
	}
	if a.opts.json {
		a.printRaw(raw)
		return nil
	}
	if state.On {
		fmt.Fprintf(a.stdout, "maintenance on for %s (%s): visitors get the maintenance page\n", t.row.Name, t.envName)
	} else {
		fmt.Fprintf(a.stdout, "maintenance off for %s (%s)\n", t.row.Name, t.envName)
	}
	if bypassGiven {
		a.hint("The bypass list is saved in the configuration and reaches the route with the next deploy: jd deploy %s", t.row.Name)
	}
	return nil
}

// setBypass writes the addresses let past maintenance into the environment's
// configuration (runtime.maintenance.bypassFrom), through the configuration
// route as jd config set does, at the current revision.
func (a *app) setBypass(t target, entries []string) error {
	document := map[string]json.RawMessage{}
	if _, err := a.client.getJSON(t.envPath("/configuration"), nil, &document); err != nil {
		return err
	}
	runtime := map[string]json.RawMessage{}
	if raw, ok := document["runtime"]; ok {
		if err := json.Unmarshal(raw, &runtime); err != nil {
			return fmt.Errorf("the configuration's runtime was not what jd expected: %v", err)
		}
	}
	maintenance := map[string]json.RawMessage{}
	if raw, ok := runtime["maintenance"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &maintenance); err != nil {
			return fmt.Errorf("the configuration's maintenance was not what jd expected: %v", err)
		}
	}
	if len(entries) == 0 {
		delete(maintenance, "bypassFrom")
	} else {
		maintenance["bypassFrom"], _ = json.Marshal(entries)
	}
	runtime["maintenance"], _ = json.Marshal(maintenance)
	body := map[string]json.RawMessage{}
	for _, field := range configurationFields {
		if value, ok := document[field]; ok && field != "revision" {
			body[field] = value
		}
	}
	body["runtime"], _ = json.Marshal(runtime)
	put := func(revision int) error {
		body["revision"] = json.RawMessage(fmt.Sprint(revision))
		_, err := a.client.sendJSON(http.MethodPut, t.envPath("/configuration"), nil, body, nil)
		return err
	}
	revision := 0
	return a.withRevision(t, &revision, put)
}

// rehearsalPolicy is the migration rehearsal's terms as the route gives them.
type rehearsalPolicy struct {
	Enabled         bool   `json:"enabled"`
	ConnectionID    int64  `json:"connectionId"`
	WhenUnavailable string `json:"whenUnavailable"`
}

// status reads where a deployment's environment stands: its environments,
// the live release, the warm release's window, the maintenance switch and
// the rehearsal terms. A read the dashboard refuses — no proxy on this host,
// an older dashboard without the route — is reported in its own words and
// the rest is still shown; with --json each read is the server's answer, or
// its error envelope.
func (a *app) status(args []string) error {
	fs := a.newFlags("status", "status APP [--env NAME] [--json]")
	env := fs.String("env", "", "environment name or id")
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if err := exactly(positional, 1, "status APP [--env NAME] [--json]"); err != nil {
		return err
	}
	t, err := a.resolve(positional[0], *env)
	if err != nil {
		return err
	}
	environments, environmentsRaw, err := a.environments(t)
	if err != nil {
		return err
	}
	releases, _, err := a.releaseList(t)
	if err != nil {
		return err
	}
	liveID := t.row.LiveReleaseID
	for _, e := range environments {
		if e.ID == t.env {
			liveID = e.LiveReleaseID
		}
	}
	var live *release
	for i := range releases {
		if releases[i].ID == liveID || (liveID == 0 && releases[i].State == "live") {
			live = &releases[i]
			break
		}
	}
	var watch rollbackWatch
	watchRaw, watchErr := a.optionalRead(t.envPath("/rollback-watch"), &watch)
	var maintenance maintenanceSwitch
	maintenanceRaw, maintenanceErr := a.optionalRead(t.envPath("/maintenance"), &maintenance)
	var rehearsal rehearsalPolicy
	rehearsalRaw, rehearsalErr := a.optionalRead(t.envPath("/rehearsal"), &rehearsal)
	for _, err := range []error{watchErr, maintenanceErr, rehearsalErr} {
		if err != nil && !errors.As(err, new(*apiError)) {
			return err
		}
	}

	if a.opts.json {
		liveRaw := json.RawMessage("null")
		if live != nil {
			liveRaw, _ = json.Marshal(live)
		}
		out, _ := json.Marshal(map[string]json.RawMessage{
			"deployment":    a.fleetRowRaw(t),
			"environments":  environmentsRaw,
			"liveRelease":   liveRaw,
			"rollbackWatch": watchRaw,
			"maintenance":   maintenanceRaw,
			"rehearsal":     rehearsalRaw,
		})
		a.printRaw(out)
		return nil
	}

	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "deployment\t%s (%s, id %d)\n", t.row.Name, t.envName, t.row.ID)
	health := t.row.Health
	if t.row.Stopped {
		health = "stopped"
	}
	fmt.Fprintf(w, "health\t%s\n", orDash(health))
	last := "-"
	switch {
	case t.row.ActiveRun != nil:
		last = fmt.Sprintf("#%d %s", t.row.ActiveRun.RunNumber, t.row.ActiveRun.State)
	case t.row.LastRun != nil:
		last = fmt.Sprintf("#%d %s %s", t.row.LastRun.RunNumber, t.row.LastRun.State, ago(t.row.LastRun.RequestedAt))
	}
	if t.row.PendingChanges {
		last += " (changes pending)"
	}
	fmt.Fprintf(w, "last run\t%s\n", last)
	names := []string{}
	for _, e := range environments {
		name := strings.ToLower(orDash(e.Name))
		if e.LiveReleaseID != 0 {
			name += fmt.Sprintf(" (live release %d)", e.LiveReleaseID)
		}
		if e.PromotionOnly {
			name += " (promotion only)"
		}
		names = append(names, name)
	}
	fmt.Fprintf(w, "environments\t%s\n", orDash(strings.Join(names, ", ")))
	if live == nil {
		fmt.Fprintf(w, "live release\t-\n")
	} else {
		parts := []string{fmt.Sprintf("#%d (id %d)", live.Number, live.ID)}
		if live.SourceRevision != "" {
			parts = append(parts, "commit "+revisionWords(live.SourceRevision))
		}
		if live.ImageDigest != "" {
			parts = append(parts, "image "+shortDigest(live.ImageDigest))
		}
		if live.ActivatedAt != nil {
			parts = append(parts, "since "+live.ActivatedAt.Local().Format("2006-01-02 15:04"))
		}
		if live.Pinned {
			parts = append(parts, "pinned")
		}
		fmt.Fprintf(w, "live release\t%s\n", strings.Join(parts, ", "))
	}
	fmt.Fprintf(w, "rollback watch\t%s\n", watchWords(watch, watchErr))
	fmt.Fprintf(w, "maintenance\t%s\n", maintenanceWords(maintenance, maintenanceErr))
	fmt.Fprintf(w, "rehearsal\t%s\n", rehearsalWords(rehearsal, rehearsalErr))
	return w.Flush()
}

// optionalRead reads a route whose refusal is part of the answer rather
// than the end of it: the body as sent, or for the dashboard's refusal its
// error envelope, with the error for the words. Any other failure — the
// dashboard unreachable, an answer jd cannot read — is the caller's to end
// the command on.
func (a *app) optionalRead(path string, out any) (json.RawMessage, error) {
	raw, err := a.client.getJSON(path, nil, out)
	var api *apiError
	if err == nil || !errors.As(err, &api) {
		return raw, err
	}
	envelope, _ := json.Marshal(map[string]any{"error": map[string]any{"code": api.Code, "message": api.Message}})
	return envelope, err
}

// fleetRowRaw is the deployment's row as the fleet sent it, for --json.
func (a *app) fleetRowRaw(t target) json.RawMessage {
	var fleet struct {
		Deployments []json.RawMessage `json:"deployments"`
	}
	if _, err := a.client.getJSON("/deploy/", query("view", "fleet"), &fleet); err == nil {
		for _, raw := range fleet.Deployments {
			var row struct {
				ID int64 `json:"id"`
			}
			if json.Unmarshal(raw, &row) == nil && row.ID == t.id() {
				return raw
			}
		}
	}
	raw, _ := json.Marshal(t.row)
	return raw
}

func watchWords(watch rollbackWatch, err error) string {
	if err != nil {
		return "unavailable: " + err.Error()
	}
	terms := "off"
	if watch.Policy.KeepWarm {
		terms = fmt.Sprintf("keep warm %ds", watch.Policy.WindowSeconds)
		if watch.Policy.Automatic {
			terms += ", automatic"
		}
	}
	if watch.Preview {
		return "never for a preview"
	}
	if watch.Watch == nil {
		return "no window (" + terms + ")"
	}
	v := watch.Watch
	switch v.State {
	case "watching":
		warm := fmt.Sprintf("warm #%d", v.WarmReleaseNumber)
		if v.WarmRunning {
			served := 0
			if v.WarmServed != nil {
				served = v.WarmServed.Requests
			}
			warm += fmt.Sprintf(" running for its visitors (%d requests)", served)
		}
		return fmt.Sprintf("watching release #%d, %s, until %s (%s)", v.ReleaseNumber, warm,
			v.ExpiresAt.Local().Format("15:04:05"), terms)
	default:
		words := fmt.Sprintf("%s release #%d", strings.ReplaceAll(v.State, "_", " "), v.ReleaseNumber)
		if v.EndReason != "" {
			words += ": " + strings.ReplaceAll(v.EndReason, "_", " ")
		}
		if v.EndMessage != "" {
			words += " — " + clip(v.EndMessage, 80)
		}
		return words + " (" + terms + ")"
	}
}

func maintenanceWords(state maintenanceSwitch, err error) string {
	if err != nil {
		return "unavailable: " + err.Error()
	}
	switch {
	case !state.Route:
		return "no public route yet"
	case state.On:
		words := "on"
		if state.Since != nil {
			words += " since " + state.Since.Local().Format("2006-01-02 15:04")
		}
		if !state.Ready {
			words += " (the live route cannot read the switch until the next deploy)"
		}
		return words + " (" + orDash(state.Driver) + ")"
	case !state.Ready:
		return "off; the switch acts from the next deploy on (" + orDash(state.Driver) + ")"
	default:
		return "off (" + orDash(state.Driver) + ")"
	}
}

func rehearsalWords(policy rehearsalPolicy, err error) string {
	if err != nil {
		return "unavailable: " + err.Error()
	}
	if !policy.Enabled {
		return "off"
	}
	words := "on"
	if policy.ConnectionID != 0 {
		words += fmt.Sprintf(", database connection %d", policy.ConnectionID)
	}
	if policy.WhenUnavailable != "" {
		words += ", " + strings.ReplaceAll(policy.WhenUnavailable, "_", " ") + " when unavailable"
	}
	return words
}
