package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"text/tabwriter"
	"time"
)

// A deployment is created the way the new-deployment page creates one: a
// draft is opened, its steps are saved, the server inspects the source and
// checks the plan, and committing the draft makes the deployment. Each step
// is a route of its own, so jd drafts can drive any of them by hand and
// jd create drives them in order for the common case of an image.

type draft struct {
	ID          string `json:"id"`
	Revision    int    `json:"revision"`
	CurrentStep string `json:"currentStep"`
	Data        struct {
		Intent *struct {
			Name    string `json:"name"`
			Profile string `json:"profile"`
		} `json:"intent"`
		Source *struct {
			Kind       string `json:"kind"`
			Image      string `json:"image"`
			URL        string `json:"url"`
			Repository string `json:"repository"`
			Ref        string `json:"ref"`
		} `json:"source"`
	} `json:"data"`
	Findings        []finding `json:"findings"`
	EnvironmentKeys []string  `json:"environmentKeys"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

type finding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Action   string `json:"action"`
}

type commitResult struct {
	ProjectID     int64 `json:"projectId"`
	EnvironmentID int64 `json:"environmentId"`
	PlanRevision  int   `json:"planRevision"`
	Created       bool  `json:"created"`
}

func draftPath(id, rest string) string {
	return "/deploy/drafts/" + url.PathEscape(id) + rest
}

func (a *app) getDraft(id string) (*draft, []byte, error) {
	var d draft
	raw, err := a.client.getJSON(draftPath(id, ""), nil, &d)
	if err != nil {
		return nil, nil, err
	}
	return &d, raw, nil
}

// draftCommand parses a drafts verb's words and connects.
func (a *app) draftCommand(name, usage string, want int, args []string, extra func(*flag.FlagSet)) ([]string, error) {
	fs := a.newFlags(name, usage)
	if extra != nil {
		extra(fs)
	}
	positional, err := a.parse(fs, args)
	if err != nil {
		return nil, err
	}
	if err := exactly(positional, want, usage); err != nil {
		return nil, err
	}
	if _, err := a.api(); err != nil {
		return nil, err
	}
	return positional, nil
}

func (a *app) draftsList(args []string) error {
	if _, err := a.draftCommand("drafts ls", "drafts ls", 0, args, nil); err != nil {
		return err
	}
	var list []struct {
		ID          string    `json:"id"`
		Name        string    `json:"name"`
		Source      string    `json:"source"`
		CurrentStep string    `json:"currentStep"`
		UpdatedAt   time.Time `json:"updatedAt"`
		ExpiresAt   time.Time `json:"expiresAt"`
	}
	raw, err := a.client.getJSON("/deploy/drafts", nil, &list)
	if err != nil {
		return err
	}
	if a.opts.json {
		a.printRaw(raw)
		return nil
	}
	if len(list) == 0 {
		a.say("No unfinished drafts.")
		return nil
	}
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tSOURCE\tSTEP\tUPDATED\tEXPIRES")
	for _, d := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", d.ID, orDash(d.Name), orDash(d.Source), d.CurrentStep,
			d.UpdatedAt.Local().Format("2006-01-02 15:04"), d.ExpiresAt.Local().Format("2006-01-02 15:04"))
	}
	return w.Flush()
}

func (a *app) draftsCreate(args []string) error {
	if _, err := a.draftCommand("drafts create", "drafts create", 0, args, nil); err != nil {
		return err
	}
	var d draft
	raw, err := a.client.sendJSON(http.MethodPost, "/deploy/drafts", nil, map[string]any{}, &d)
	if err != nil {
		return err
	}
	if a.opts.json {
		a.printRaw(raw)
		return nil
	}
	fmt.Fprintf(a.stdout, "draft %s (revision %d)\n", d.ID, d.Revision)
	a.hint("Save its steps with jd drafts save %s FILE.json, then detect, preflight and commit.", d.ID)
	return nil
}

func (a *app) draftsShow(args []string) error {
	positional, err := a.draftCommand("drafts show", "drafts show ID", 1, args, nil)
	if err != nil {
		return err
	}
	d, raw, err := a.getDraft(positional[0])
	if err != nil {
		return err
	}
	if a.opts.json {
		a.printRaw(raw)
		return nil
	}
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "draft\t%s\n", d.ID)
	fmt.Fprintf(w, "step\t%s (revision %d)\n", d.CurrentStep, d.Revision)
	if i := d.Data.Intent; i != nil {
		fmt.Fprintf(w, "name\t%s (%s)\n", i.Name, i.Profile)
	}
	if s := d.Data.Source; s != nil {
		where := s.Image + s.Repository + s.URL
		if s.Ref != "" {
			where += "@" + s.Ref
		}
		fmt.Fprintf(w, "source\t%s %s\n", s.Kind, where)
	}
	if len(d.EnvironmentKeys) > 0 {
		fmt.Fprintf(w, "staged values\t%s\n", strings.Join(d.EnvironmentKeys, ", "))
	}
	if !d.ExpiresAt.IsZero() {
		fmt.Fprintf(w, "expires\t%s\n", d.ExpiresAt.Local().Format("2006-01-02 15:04"))
	}
	w.Flush()
	a.printFindings(d.Findings)
	return nil
}

func (a *app) draftsSave(args []string) error {
	positional, err := a.draftCommand("drafts save", "drafts save ID FILE.json", 2, args, nil)
	if err != nil {
		return err
	}
	document, err := a.readDocument(positional[1])
	if err != nil {
		return err
	}
	d, err := a.saveDraft(positional[0], document)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "saved draft %s; step %s, revision %d\n", d.ID, d.CurrentStep, d.Revision)
	return nil
}

// saveDraft writes one step at the draft's current revision unless the
// document names one.
func (a *app) saveDraft(id string, document map[string]json.RawMessage) (*draft, error) {
	if _, ok := document["revision"]; !ok {
		current, _, err := a.getDraft(id)
		if err != nil {
			return nil, err
		}
		document["revision"] = json.RawMessage(fmt.Sprint(current.Revision))
	}
	var d draft
	if _, err := a.client.sendJSON(http.MethodPut, draftPath(id, ""), nil, document, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (a *app) draftsDetect(args []string) error {
	positional, err := a.draftCommand("drafts detect", "drafts detect ID", 1, args, nil)
	if err != nil {
		return err
	}
	d, raw, err := a.detectDraft(positional[0], 0)
	if err != nil {
		return err
	}
	if a.opts.json {
		a.printRaw(raw)
		return nil
	}
	fmt.Fprintf(a.stdout, "inspected the source of draft %s; revision %d\n", d.ID, d.Revision)
	return nil
}

func (a *app) detectDraft(id string, revision int) (*draft, []byte, error) {
	if revision == 0 {
		current, _, err := a.getDraft(id)
		if err != nil {
			return nil, nil, err
		}
		revision = current.Revision
	}
	var d draft
	raw, err := a.client.sendJSON(http.MethodPost, draftPath(id, "/detect"), nil, map[string]any{"revision": revision}, &d)
	if err != nil {
		return nil, nil, err
	}
	return &d, raw, nil
}

type preflightAnswer struct {
	Draft     draft `json:"draft"`
	Preflight struct {
		Findings []finding `json:"findings"`
	} `json:"preflight"`
}

func (a *app) draftsPreflight(args []string) error {
	positional, err := a.draftCommand("drafts preflight", "drafts preflight ID", 1, args, nil)
	if err != nil {
		return err
	}
	answer, raw, err := a.preflightDraft(positional[0], 0)
	if err != nil {
		return err
	}
	if a.opts.json {
		a.printRaw(raw)
		return nil
	}
	a.printFindings(answer.Preflight.Findings)
	if blockers := blocking(answer.Preflight.Findings); len(blockers) > 0 {
		a.say("%d %s must be resolved before the draft can be committed.", len(blockers), plural(len(blockers), "finding", "findings"))
		return errRunFailed
	}
	return nil
}

func (a *app) preflightDraft(id string, revision int) (*preflightAnswer, []byte, error) {
	if revision == 0 {
		current, _, err := a.getDraft(id)
		if err != nil {
			return nil, nil, err
		}
		revision = current.Revision
	}
	var answer preflightAnswer
	raw, err := a.client.sendJSON(http.MethodPost, draftPath(id, "/preflight"), nil, map[string]any{"revision": revision}, &answer)
	if err != nil {
		return nil, nil, err
	}
	return &answer, raw, nil
}

// blocking are the findings a commit refuses: blocked ones, and decisions
// nobody has taken yet.
func blocking(findings []finding) []finding {
	var out []finding
	for _, f := range findings {
		if f.Severity == "blocked" || f.Severity == "decision" {
			out = append(out, f)
		}
	}
	return out
}

func warnings(findings []finding) []string {
	var out []string
	for _, f := range findings {
		if f.Severity == "warning" {
			out = append(out, f.Code)
		}
	}
	return out
}

func (a *app) printFindings(findings []finding) {
	if len(findings) == 0 || a.opts.json {
		return
	}
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SEVERITY\tCODE\tFINDING")
	for _, f := range findings {
		title := f.Title
		if f.Action != "" && f.Severity != "pass" {
			title += " — " + f.Action
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", f.Severity, f.Code, title)
	}
	w.Flush()
}

func (a *app) draftsCommit(args []string) error {
	var acks stringList
	positional, err := a.draftCommand("drafts commit", "drafts commit ID [--ack CODE ...]", 1, args, func(fs *flag.FlagSet) {
		fs.Var(&acks, "ack", "acknowledge a warning by its code (repeatable)")
	})
	if err != nil {
		return err
	}
	result, raw, err := a.commitDraft(positional[0], 0, acks)
	if err != nil {
		return err
	}
	if a.opts.json {
		a.printRaw(raw)
		return nil
	}
	fmt.Fprintf(a.stdout, "created deployment %d (environment %d)\n", result.ProjectID, result.EnvironmentID)
	return nil
}

func (a *app) commitDraft(id string, revision int, acks []string) (*commitResult, []byte, error) {
	if revision == 0 {
		current, _, err := a.getDraft(id)
		if err != nil {
			return nil, nil, err
		}
		revision = current.Revision
	}
	if acks == nil {
		acks = []string{}
	}
	var result commitResult
	raw, err := a.client.sendJSON(http.MethodPost, draftPath(id, "/commit"), nil,
		map[string]any{"revision": revision, "acknowledgedWarnings": acks}, &result)
	if err != nil {
		return nil, nil, err
	}
	return &result, raw, nil
}

func (a *app) draftsRemove(args []string) error {
	positional, err := a.draftCommand("drafts rm", "drafts rm ID", 1, args, nil)
	if err != nil {
		return err
	}
	if _, err := a.client.do(http.MethodDelete, draftPath(positional[0], ""), nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "discarded draft %s\n", positional[0])
	return nil
}

// create is the new-deployment page's journey for an image, in the order
// and with the bodies the deployment acceptance script uses
// (scripts/e2e-deployments.py), which is known to produce a deployment that
// deploys.
func (a *app) create(args []string) error {
	fs := a.newFlags("create", "create --name NAME --image IMAGE [--port 80] [--profile web]")
	name := fs.String("name", "", "the deployment's name")
	image := fs.String("image", "", "the image to run, e.g. nginx:1.27-alpine")
	port := fs.Int("port", 80, "the port the container listens on; 0 for none")
	profile := fs.String("profile", "web", "web, static, service or worker")
	noCheck := fs.Bool("no-check", false, "leave out the HTTP readiness check")
	ackAll := fs.Bool("ack-warnings", false, "acknowledge every warning the preflight raises")
	var acks stringList
	fs.Var(&acks, "ack", "acknowledge a warning by its code (repeatable)")
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 0 || *name == "" || *image == "" {
		return usagef("usage: jd create --name NAME --image IMAGE [--port 80] [--profile web]")
	}
	if _, err := a.api(); err != nil {
		return err
	}
	var d draft
	if _, err := a.client.sendJSON(http.MethodPost, "/deploy/drafts", nil, map[string]any{}, &d); err != nil {
		return err
	}
	a.say("Opened draft %s.", d.ID)
	keep := func(err error) error {
		a.hint("The draft is kept: jd drafts show %s, or jd drafts rm %s.", d.ID, d.ID)
		return err
	}
	step := func(body map[string]any) error {
		body["revision"] = d.Revision
		var next draft
		if _, err := a.client.sendJSON(http.MethodPut, draftPath(d.ID, ""), nil, body, &next); err != nil {
			return err
		}
		d = next
		return nil
	}
	if err := step(map[string]any{"step": "intent", "intent": map[string]any{"name": *name, "profile": *profile}}); err != nil {
		return keep(err)
	}
	if err := step(map[string]any{"step": "source",
		"source": map[string]any{"kind": "image", "mode": "image_reference", "image": *image}}); err != nil {
		return keep(err)
	}
	detected, _, err := a.detectDraft(d.ID, d.Revision)
	if err != nil {
		return keep(err)
	}
	d = *detected
	a.say("Inspected %s.", *image)
	checks := []any{}
	if *port > 0 && !*noCheck {
		checks = append(checks, map[string]any{"name": "HTTP readiness", "kind": "http", "phase": "readiness", "required": true,
			"config": map[string]any{"path": "/", "attempts": 20, "timeoutSeconds": 5, "intervalSeconds": 2}})
	}
	configuration := map[string]any{
		"build": map[string]any{"method": "image", "noCache": false, "secrets": []any{}, "releaseTasks": []any{}},
		"runtime": map[string]any{
			"image": *image, "command": []string{}, "internalPort": *port, "hostPort": 0,
			"bindAddress": "127.0.0.1", "strategy": "stop_first", "privileged": false,
			"hostNetwork": false, "capabilities": []string{}, "devices": []string{}, "mounts": []any{},
		},
		"variables": []any{}, "dependencies": []any{}, "checks": checks, "domains": []any{},
	}
	if err := step(map[string]any{"step": "configuration", "configuration": configuration}); err != nil {
		return keep(err)
	}
	answer, _, err := a.preflightDraft(d.ID, d.Revision)
	if err != nil {
		return keep(err)
	}
	d = answer.Draft
	findings := answer.Preflight.Findings
	if blockers := blocking(findings); len(blockers) > 0 {
		a.printFindings(blockers)
		return keep(fmt.Errorf("the preflight found %d %s to resolve before %s can be created",
			len(blockers), plural(len(blockers), "problem", "problems"), *name))
	}
	acknowledged := []string(acks)
	if *ackAll {
		acknowledged = append(acknowledged, warnings(findings)...)
	}
	a.say("Preflight passed with %d %s.", len(warnings(findings)), plural(len(warnings(findings)), "warning", "warnings"))
	result, raw, err := a.commitDraft(d.ID, d.Revision, acknowledged)
	if err != nil {
		if pending := warnings(findings); len(pending) > 0 {
			a.hint("Warnings raised: %s (acknowledge with --ack CODE or --ack-warnings).", strings.Join(pending, ", "))
		}
		return keep(err)
	}
	if a.opts.json {
		a.printRaw(raw)
		return nil
	}
	fmt.Fprintf(a.stdout, "created %s: deployment %d, environment %d\n", *name, result.ProjectID, result.EnvironmentID)
	a.hint("Set its variables with jd vars set %s KEY=-, then ship it with jd deploy %s --wait", *name, *name)
	return nil
}
