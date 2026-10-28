package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// engineRun is a run as the deployment routes state it.
type engineRun struct {
	ID             int64      `json:"id"`
	RunNumber      int64      `json:"runNumber"`
	ProjectID      int64      `json:"projectId"`
	EnvironmentID  int64      `json:"environmentId"`
	State          string     `json:"state"`
	Operation      string     `json:"operation"`
	Trigger        string     `json:"trigger"`
	Actor          string     `json:"actor"`
	RequestedAt    time.Time  `json:"requestedAt"`
	EndedAt        *time.Time `json:"endedAt"`
	SourceRevision string     `json:"sourceRevision"`
	ReleaseID      int64      `json:"releaseId"`
	TerminalCode   string     `json:"terminalCode"`
	TerminalReason string     `json:"terminalReason"`
}

// fleetRow is one deployment's production environment, as the fleet view of
// the deployment list gives it.
type fleetRow struct {
	ID               int64      `json:"id"`
	Name             string     `json:"name"`
	EnvironmentID    int64      `json:"environmentId"`
	EnvironmentName  string     `json:"environmentName"`
	EnvironmentKind  string     `json:"environmentKind"`
	SourceKind       string     `json:"sourceKind"`
	BuildMethod      string     `json:"buildMethod"`
	SourceRef        string     `json:"sourceRef"`
	SourceRevision   string     `json:"sourceRevision"`
	SourceRepository string     `json:"sourceRepository"`
	Endpoint         string     `json:"endpoint"`
	Health           string     `json:"health"`
	Stopped          bool       `json:"stopped"`
	PendingChanges   bool       `json:"pendingChanges"`
	LiveReleaseID    int64      `json:"liveReleaseId"`
	LastRun          *engineRun `json:"lastRun"`
	ActiveRun        *engineRun `json:"activeRun"`
}

type fleetModel struct {
	Deployments []fleetRow `json:"deployments"`
}

// terminalStates are the states a run does not leave. Only succeeded is a
// success: a run rolled back or superseded did not ship what was asked.
var terminalStates = map[string]bool{
	"succeeded": true, "failed": true, "cancelled": true, "rolled_back": true, "superseded": true,
}

// target is the deployment and environment a command acts on.
type target struct {
	row fleetRow
	env int64
	// envName is how the environment is named in messages.
	envName string
}

func (t target) id() int64 { return t.row.ID }

func (t target) envPath(rest string) string {
	return fmt.Sprintf("/deploy/%d/environments/%d%s", t.row.ID, t.env, rest)
}

func (a *app) fleet() (*fleetModel, []byte, error) {
	c, err := a.api()
	if err != nil {
		return nil, nil, err
	}
	var fleet fleetModel
	raw, err := c.getJSON("/deploy/", query("view", "fleet"), &fleet)
	if err != nil {
		return nil, nil, err
	}
	return &fleet, raw, nil
}

// resolve finds APP by id, by exact name, or by a name that matches one
// deployment alone ignoring case — the rule the MCP tools use, so the same
// word names the same deployment everywhere.
func (a *app) resolve(name, env string) (target, error) {
	fleet, _, err := a.fleet()
	if err != nil {
		return target{}, err
	}
	var row *fleetRow
	if id, err := strconv.ParseInt(name, 10, 64); err == nil {
		for i := range fleet.Deployments {
			if fleet.Deployments[i].ID == id {
				row = &fleet.Deployments[i]
			}
		}
	}
	if row == nil {
		var folded []*fleetRow
		for i := range fleet.Deployments {
			one := &fleet.Deployments[i]
			if one.Name == name {
				row = one
				break
			}
			if strings.EqualFold(one.Name, name) {
				folded = append(folded, one)
			}
		}
		if row == nil && len(folded) == 1 {
			row = folded[0]
		}
	}
	if row == nil {
		names := make([]string, 0, len(fleet.Deployments))
		for _, d := range fleet.Deployments {
			names = append(names, d.Name)
		}
		if len(names) == 0 {
			return target{}, fmt.Errorf("no deployment %q: this key sees no deployments", name)
		}
		return target{}, fmt.Errorf("no deployment %q; this key sees %s", name, strings.Join(names, ", "))
	}
	t := target{row: *row, env: row.EnvironmentID, envName: row.EnvironmentName}
	if t.envName == "" {
		t.envName = row.EnvironmentKind
	}
	switch {
	case env == "", strings.EqualFold(env, row.EnvironmentName), strings.EqualFold(env, row.EnvironmentKind),
		env == strconv.FormatInt(row.EnvironmentID, 10):
	default:
		// The list names each deployment's production environment only. An
		// environment id is passed through and the server, which knows the
		// deployment's environments, decides whether it is one of them.
		id, err := strconv.ParseInt(env, 10, 64)
		if err != nil || id <= 0 {
			return target{}, usagef("--env %q: jd knows %s's %s environment by name; give any other environment by its id",
				env, row.Name, t.envName)
		}
		t.env, t.envName = id, "environment "+env
	}
	return t, nil
}

func (a *app) deployments(args []string) error {
	fs := a.newFlags("deployments", "deployments [ls]")
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) > 1 || (len(positional) == 1 && positional[0] != "ls") {
		return usagef("usage: jd deployments [ls]")
	}
	fleet, raw, err := a.fleet()
	if err != nil {
		return err
	}
	if a.opts.json {
		a.printRaw(raw)
		return nil
	}
	if len(fleet.Deployments) == 0 {
		a.say("No deployments this key can see.")
		return nil
	}
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tENV\tHEALTH\tLAST RUN\tSOURCE\tADDRESS")
	for _, d := range fleet.Deployments {
		health := d.Health
		if d.Stopped {
			health = "stopped"
		}
		if health == "" {
			health = "-"
		}
		last := "-"
		if d.ActiveRun != nil {
			last = fmt.Sprintf("#%d %s", d.ActiveRun.RunNumber, d.ActiveRun.State)
		} else if d.LastRun != nil {
			last = fmt.Sprintf("#%d %s %s", d.LastRun.RunNumber, d.LastRun.State, ago(d.LastRun.RequestedAt))
		}
		if d.PendingChanges {
			last += " (changes pending)"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", d.ID, d.Name, orDash(d.EnvironmentName), health, last,
			sourceWords(d), orDash(d.Endpoint))
	}
	return w.Flush()
}

func sourceWords(d fleetRow) string {
	switch {
	case d.SourceRepository != "" && d.SourceRef != "":
		return d.SourceRepository + "@" + d.SourceRef
	case d.SourceRepository != "":
		return d.SourceRepository
	case d.SourceKind != "":
		return d.SourceKind
	}
	return "-"
}

func (a *app) deploy(args []string) error {
	fs := a.newFlags("deploy", "deploy APP [--env NAME] [--ref REF] [--wait]")
	env := fs.String("env", "", "environment name or id")
	ref := fs.String("ref", "", "a branch, tag or commit to deploy instead of the configured one")
	wait := fs.Bool("wait", false, "follow the run and exit with its result")
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if err := exactly(positional, 1, "deploy APP [--env NAME] [--ref REF] [--wait]"); err != nil {
		return err
	}
	t, err := a.resolve(positional[0], *env)
	if err != nil {
		return err
	}
	body := map[string]any{"operation": "deploy"}
	if *ref != "" {
		body["ref"] = *ref
	}
	var run engineRun
	raw, err := a.client.sendJSON(http.MethodPost, t.envPath("/runs"), nil, body, &run)
	if err != nil {
		return err
	}
	return a.afterQueue(t, run, raw, *wait, "Deploying")
}

// afterQueue reports a queued run, and with --wait follows it to its end.
func (a *app) afterQueue(t target, run engineRun, raw []byte, wait bool, verb string) error {
	if !wait {
		if a.opts.json {
			a.printRaw(raw)
			return nil
		}
		fmt.Fprintf(a.stdout, "Queued run #%d for %s (%s).\n", run.RunNumber, t.row.Name, t.envName)
		a.hint("Follow it with: jd logs %s --run %d --follow", t.row.Name, run.RunNumber)
		return nil
	}
	a.say("%s %s (%s): run #%d", verb, t.row.Name, t.envName, run.RunNumber)
	// With --json the answer is the finished run alone, one object a script
	// can read; the transcript is for a person watching.
	return a.follow(t, run.ID, !a.opts.json, a.opts.json)
}

// follow prints a run's transcript as it grows and returns when the run ends,
// with errRunFailed unless it succeeded. It reads the transcript route, whose
// answer carries the run's state as well; against a dashboard without that
// route it falls back to the run itself and prints no lines.
func (a *app) follow(t target, runID int64, printLines, printRun bool) error {
	deadline := time.Now().Add(a.opts.timeout)
	started := time.Now()
	after, step := int64(0), ""
	transcript, drained := true, false
	var run engineRun
	for {
		if transcript {
			page, err := a.transcript(t, runID, after)
			var api *apiError
			if errors.As(err, &api) && api.Status == http.StatusNotFound && api.Raw {
				transcript = false
				a.hint("This dashboard does not serve run transcripts to jd yet; waiting without them.")
				continue
			}
			if err != nil {
				return err
			}
			if printLines {
				step = a.printLines(page.Lines, step)
			}
			if page.Next > after {
				after = page.Next
			}
			run = page.Run
			if page.More {
				continue
			}
			// The run's state and its last lines are read in one answer, but
			// not in one instant: one more read after the end is seen picks
			// up anything written in between.
			if terminalStates[run.State] {
				if drained {
					break
				}
				drained = true
				continue
			}
		} else {
			var snapshot struct {
				Run engineRun `json:"run"`
			}
			if _, err := a.client.getJSON(fmt.Sprintf("/deploy/%d/runs/%d", t.id(), runID), nil, &snapshot); err != nil {
				return err
			}
			run = snapshot.Run
			if terminalStates[run.State] {
				break
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("gave up waiting after %s; run #%d is %s and carries on: jd logs %s --run %d --follow",
				a.opts.timeout, run.RunNumber, run.State, t.row.Name, run.RunNumber)
		}
		time.Sleep(followEvery)
	}
	return a.ended(run, time.Since(started), printRun)
}

// followEvery is how often a followed run is read again. A variable so the
// tests need not wait a second per step.
var followEvery = time.Second

// ended says how a run finished and turns anything but success into exit 1.
func (a *app) ended(run engineRun, took time.Duration, printRun bool) error {
	if printRun {
		raw, _ := json.Marshal(run)
		a.printRaw(raw)
	}
	if run.State == "succeeded" {
		a.say("Run #%d succeeded in %s.", run.RunNumber, took.Round(time.Second))
		return nil
	}
	why := run.TerminalReason
	if run.TerminalCode != "" {
		why = strings.TrimSpace(run.TerminalCode + ": " + why)
	}
	if why == "" {
		a.say("Run #%d %s.", run.RunNumber, strings.ReplaceAll(run.State, "_", " "))
	} else {
		a.say("Run #%d %s — %s", run.RunNumber, strings.ReplaceAll(run.State, "_", " "), why)
	}
	return errRunFailed
}

type transcriptLine struct {
	Seq    int64     `json:"seq"`
	Step   string    `json:"step"`
	Stream string    `json:"stream"`
	At     time.Time `json:"at"`
	Text   string    `json:"text"`
}

type transcriptPage struct {
	Run   engineRun         `json:"run"`
	Lines []json.RawMessage `json:"lines"`
	Next  int64             `json:"next"`
	More  bool              `json:"more"`
}

func (a *app) transcript(t target, runID, after int64) (*transcriptPage, error) {
	var page transcriptPage
	_, err := a.client.getJSON(fmt.Sprintf("/deploy/%d/runs/%d/transcript", t.id(), runID),
		query("after", strconv.FormatInt(after, 10), "limit", "1000"), &page)
	if err != nil {
		return nil, err
	}
	return &page, nil
}

// printLines writes transcript lines, naming the step whenever it changes;
// with --json each line is one JSON object, exactly as the server sent it.
func (a *app) printLines(lines []json.RawMessage, step string) string {
	for _, raw := range lines {
		if a.opts.json {
			a.printRaw(raw)
			continue
		}
		var line transcriptLine
		if json.Unmarshal(raw, &line) != nil {
			continue
		}
		if line.Step != "" && line.Step != step {
			step = line.Step
			fmt.Fprintf(a.stdout, "==> %s\n", step)
		}
		fmt.Fprintln(a.stdout, strings.TrimRight(line.Text, "\r\n"))
	}
	return step
}

// runList reads a deployment's runs from the engine's view of them, which
// is the one that numbers them the way the run page does.
func (a *app) runList(t target, limit int, env bool) ([]engineRun, []byte, error) {
	q := query("view", "engine", "limit", strconv.Itoa(limit))
	if env {
		q.Set("environment", strconv.FormatInt(t.env, 10))
	}
	raw, err := a.client.getJSON(fmt.Sprintf("/deploy/%d/runs", t.id()), q, nil)
	if err != nil {
		return nil, nil, err
	}
	// The route answers {"runs": [...]}; a bare array is read too, so a
	// change of envelope does not break the list.
	var wrapped struct {
		Runs []engineRun `json:"runs"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Runs != nil {
		return wrapped.Runs, raw, nil
	}
	var bare []engineRun
	if err := json.Unmarshal(raw, &bare); err != nil {
		return nil, raw, fmt.Errorf("the dashboard's list of runs was not what jd expected: %v", err)
	}
	return bare, raw, nil
}

func (a *app) runs(args []string) error {
	fs := a.newFlags("runs", "runs APP [--env NAME] [--limit N]")
	env := fs.String("env", "", "only this environment's runs")
	limit := fs.Int("limit", 20, "how many runs")
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if err := exactly(positional, 1, "runs APP [--env NAME] [--limit N]"); err != nil {
		return err
	}
	t, err := a.resolve(positional[0], *env)
	if err != nil {
		return err
	}
	runs, raw, err := a.runList(t, *limit, *env != "")
	if err != nil {
		return err
	}
	if a.opts.json {
		a.printRaw(raw)
		return nil
	}
	if len(runs) == 0 {
		a.say("%s has not run yet.", t.row.Name)
		return nil
	}
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RUN\tSTATE\tOPERATION\tTRIGGER\tBY\tREQUESTED\tCOMMIT\tRESULT")
	for _, r := range runs {
		result := r.TerminalCode
		if r.TerminalReason != "" && r.State != "succeeded" {
			result = strings.TrimSpace(result + " " + clip(r.TerminalReason, 60))
		}
		fmt.Fprintf(w, "#%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.RunNumber, r.State, r.Operation, orDash(r.Trigger),
			orDash(r.Actor), r.RequestedAt.Local().Format("2006-01-02 15:04"), orDash(revisionWords(r.SourceRevision)), orDash(result))
	}
	return w.Flush()
}

func (a *app) logs(args []string) error {
	fs := a.newFlags("logs", "logs APP [--run N] [--follow]")
	env := fs.String("env", "", "environment name or id")
	number := fs.Int64("run", 0, "the run number, as jd runs lists it")
	followFlag := fs.Bool("follow", false, "keep printing until the run ends, and exit with its result")
	fs.BoolVar(followFlag, "f", false, "short for --follow")
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if err := exactly(positional, 1, "logs APP [--run N] [--follow]"); err != nil {
		return err
	}
	t, err := a.resolve(positional[0], *env)
	if err != nil {
		return err
	}
	var run *engineRun
	switch {
	case *number > 0:
		runs, _, err := a.runList(t, 200, false)
		if err != nil {
			return err
		}
		for i := range runs {
			if runs[i].RunNumber == *number {
				run = &runs[i]
			}
		}
		if run == nil {
			return fmt.Errorf("%s has no run #%d among its last 200; jd runs %s lists them", t.row.Name, *number, t.row.Name)
		}
	case t.row.ActiveRun != nil:
		run = t.row.ActiveRun
	case t.row.LastRun != nil:
		run = t.row.LastRun
	default:
		return fmt.Errorf("%s has not run yet", t.row.Name)
	}
	if *followFlag {
		return a.follow(t, run.ID, true, false)
	}
	after, step := int64(0), ""
	for {
		page, err := a.transcript(t, run.ID, after)
		if err != nil {
			var api *apiError
			if errors.As(err, &api) && api.Status == http.StatusNotFound && api.Raw {
				return fmt.Errorf("this dashboard does not serve run transcripts to jd yet; update it, or read run #%d on its page", run.RunNumber)
			}
			return err
		}
		step = a.printLines(page.Lines, step)
		if page.Next > after {
			after = page.Next
		}
		if !page.More {
			if !terminalStates[page.Run.State] {
				a.hint("Run #%d is still %s; add --follow to keep reading.", page.Run.RunNumber, page.Run.State)
			} else {
				a.say("Run #%d %s.", page.Run.RunNumber, strings.ReplaceAll(page.Run.State, "_", " "))
			}
			return nil
		}
	}
}

// release is a retained release as the releases route states it.
type release struct {
	ID             int64      `json:"id"`
	Number         int64      `json:"number"`
	RunID          int64      `json:"runId"`
	State          string     `json:"state"`
	SourceRevision string     `json:"sourceRevision"`
	ImageDigest    string     `json:"imageDigest"`
	CreatedAt      time.Time  `json:"createdAt"`
	ActivatedAt    *time.Time `json:"activatedAt"`
	Pinned         bool       `json:"pinned"`
}

func (a *app) releaseList(t target) ([]release, []byte, error) {
	raw, err := a.client.getJSON(t.envPath("/releases"), query("limit", "200"), nil)
	if err != nil {
		return nil, nil, err
	}
	var bare []release
	if err := json.Unmarshal(raw, &bare); err == nil {
		return bare, raw, nil
	}
	var wrapped struct {
		Releases []release `json:"releases"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, raw, fmt.Errorf("the dashboard's list of releases was not what jd expected: %v", err)
	}
	return wrapped.Releases, raw, nil
}

func (a *app) releases(args []string) error {
	fs := a.newFlags("releases", "releases APP [--env NAME]")
	env := fs.String("env", "", "environment name or id")
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if err := exactly(positional, 1, "releases APP [--env NAME]"); err != nil {
		return err
	}
	t, err := a.resolve(positional[0], *env)
	if err != nil {
		return err
	}
	list, raw, err := a.releaseList(t)
	if err != nil {
		return err
	}
	if a.opts.json {
		a.printRaw(raw)
		return nil
	}
	if len(list) == 0 {
		a.say("%s has no releases yet.", t.row.Name)
		return nil
	}
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNUMBER\tSTATE\tCREATED\tCOMMIT\tIMAGE\tPINNED")
	for _, r := range list {
		pinned := ""
		if r.Pinned {
			pinned = "pinned"
		}
		fmt.Fprintf(w, "%d\t%d\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Number, r.State, r.CreatedAt.Local().Format("2006-01-02 15:04"),
			orDash(revisionWords(r.SourceRevision)), orDash(shortDigest(r.ImageDigest)), pinned)
	}
	return w.Flush()
}

func (a *app) rollback(args []string) error {
	fs := a.newFlags("rollback", "rollback APP [--to RELEASE_ID | --instant] [--wait]")
	env := fs.String("env", "", "environment name or id")
	to := fs.Int64("to", 0, "the release id to go back to (jd releases lists them)")
	instant := fs.Bool("instant", false, "switch traffic back to the release kept warm, without starting anything")
	wait := fs.Bool("wait", false, "follow the run and exit with its result")
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if err := exactly(positional, 1, "rollback APP [--to RELEASE_ID | --instant] [--wait]"); err != nil {
		return err
	}
	if *instant && *to != 0 {
		return usagef("--instant goes back to the release kept warm, which the dashboard chose; it takes no --to")
	}
	t, err := a.resolve(positional[0], *env)
	if err != nil {
		return err
	}
	if *instant {
		// The window's own route: the warm release is already running, so
		// the run only switches traffic and retires the live release.
		var run engineRun
		raw, err := a.client.sendJSON(http.MethodPost, t.envPath("/rollback-watch/rollback"), nil, nil, &run)
		if err != nil {
			return err
		}
		return a.afterQueue(t, run, raw, *wait, "Switching back to the warm release of")
	}
	releaseID := *to
	described := ""
	if releaseID <= 0 {
		list, _, err := a.releaseList(t)
		if err != nil {
			return err
		}
		// Releases come newest first; the first retained one after the live
		// one is what was serving before it — the MCP rollback tool's rule.
		live := t.row.LiveReleaseID
		passedLive := live == 0
		for _, r := range list {
			if r.ID == live {
				passedLive = true
				continue
			}
			if passedLive && r.State == "retained" {
				releaseID = r.ID
				described = fmt.Sprintf(" (release %d, number %d%s)", r.ID, r.Number, commitWords(r.SourceRevision))
				break
			}
		}
		if releaseID <= 0 {
			return fmt.Errorf("%s has no retained release older than the live one to go back to; jd releases %s lists what it has",
				t.row.Name, t.row.Name)
		}
	} else {
		described = fmt.Sprintf(" (release %d)", releaseID)
	}
	var run engineRun
	raw, err := a.client.sendJSON(http.MethodPost, t.envPath("/rollback"), nil, map[string]any{"releaseId": releaseID}, &run)
	if err != nil {
		return err
	}
	return a.afterQueue(t, run, raw, *wait, "Rolling back"+described)
}

func commitWords(revision string) string {
	if revision == "" {
		return ""
	}
	return ", commit " + revisionWords(revision)
}

// revisionWords is what a run or release was built from, as short as it is
// recognisable: a Git commit's first seven hex digits, or for an image the
// digest it was pinned to, which is "sha256:…" and would read as nothing at
// seven characters.
func revisionWords(revision string) string {
	if strings.Contains(revision, ":") {
		return shortDigest(revision)
	}
	return short(revision, 7)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func short(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// shortDigest is a digest as a person compares it: the algorithm and the
// first twelve hex digits, which is plenty to tell two values apart by eye.
func shortDigest(d string) string {
	algo, hex, ok := strings.Cut(d, ":")
	if !ok {
		return short(d, 12)
	}
	return algo + ":" + short(hex, 12)
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
