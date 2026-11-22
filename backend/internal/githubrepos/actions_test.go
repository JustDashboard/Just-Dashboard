package githubrepos

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseDispatchReadsEveryShapeOfOn(t *testing.T) {
	cases := map[string]struct {
		yaml      string
		available bool
		inputs    int
	}{
		"one event":           {"on: workflow_dispatch\njobs: {}\n", true, 0},
		"a list":              {"on: [push, workflow_dispatch]\n", true, 0},
		"a block list":        {"on:\n  - push\n  - workflow_dispatch\n", true, 0},
		"a map with nothing":  {"on:\n  push:\n  workflow_dispatch:\n", true, 0},
		"a map with inputs":   {"on:\n  workflow_dispatch:\n    inputs:\n      a: {}\n      b:\n        type: boolean\n", true, 2},
		"a true key":          {"true:\n  workflow_dispatch: {}\n", true, 0},
		"a quoted on":         {"\"on\":\n  workflow_dispatch: {}\n", true, 0},
		"push only":           {"on: push\n", false, 0},
		"push only as a list": {"on: [push, pull_request]\n", false, 0},
		"reusable only":       {"on:\n  workflow_call:\n    inputs:\n      a: {type: string}\n", false, 0},
		"no on":               {"jobs: {}\n", false, 0},
		"not a mapping":       {"- a\n- b\n", false, 0},
		"not yaml":            {"on: [workflow_dispatch\n", false, 0},
	}
	for name, c := range cases {
		got := ParseDispatch([]byte(c.yaml))
		if got.Available != c.available || len(got.Inputs) != c.inputs || got.Inputs == nil {
			t.Errorf("%s: %+v", name, got)
		}
		if !got.Available && got.Message == "" {
			t.Errorf("%s: no reason given", name)
		}
	}
}

func TestParseDispatchReadsInputs(t *testing.T) {
	got := ParseDispatch([]byte(`
name: Deploy
on:
  workflow_dispatch:
    inputs:
      environment:
        description: Where to
        required: true
        type: choice
        options: [staging, production]
        default: staging
      dry_run:
        type: boolean
        default: false
      count:
        type: number
      note:
        description: Free text
      target:
        type: environment
      odd:
        type: something-new
`))
	if !got.Available || len(got.Inputs) != 6 {
		t.Fatalf("dispatch = %+v", got)
	}
	env := got.Inputs[0]
	if env.Name != "environment" || env.Description != "Where to" || !env.Required || env.Type != "choice" ||
		strings.Join(env.Options, ",") != "staging,production" || env.Default == nil || *env.Default != "staging" {
		t.Fatalf("environment = %+v", env)
	}
	if dry := got.Inputs[1]; dry.Type != "boolean" || dry.Default == nil || *dry.Default != "false" || dry.Required {
		t.Fatalf("dry_run = %+v", dry)
	}
	if note := got.Inputs[3]; note.Type != "string" || note.Default != nil {
		t.Fatalf("note = %+v", note)
	}
	if got.Inputs[4].Type != "environment" || got.Inputs[5].Type != "string" {
		t.Fatalf("types = %+v", got.Inputs)
	}

	var many strings.Builder
	many.WriteString("on:\n  workflow_dispatch:\n    inputs:\n")
	for i := range 26 {
		fmt.Fprintf(&many, "      i%d: {}\n", i)
	}
	if got := ParseDispatch([]byte(many.String())); got.Available {
		t.Fatalf("26 inputs accepted")
	}
}

func contentsAnswer(sha, yaml string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(yaml))
	// GitHub wraps the base64 at 60 columns.
	var wrapped strings.Builder
	for len(encoded) > 60 {
		wrapped.WriteString(encoded[:60] + `\n`)
		encoded = encoded[60:]
	}
	wrapped.WriteString(encoded)
	return fmt.Sprintf(`{"type":"file","encoding":"base64","sha":%q,"size":%d,"content":"%s"}`, sha, len(yaml), wrapped.String())
}

const deployYAML = "on:\n  workflow_dispatch:\n    inputs:\n      environment:\n        required: true\n        type: choice\n        options: [staging, production]\n      dry_run:\n        type: boolean\n"

func TestWorkflowsReadEachFileOnTheDefaultBranch(t *testing.T) {
	fake := newFake(t)
	fake.json("GET /repos/octo-org/widgets/actions/workflows?per_page=100", 200, `{"workflows":[
		{"id":1,"name":"Deploy","path":".github/workflows/deploy.yml","state":"active","html_url":"https://github.com/w/1"},
		{"id":2,"name":"CI","path":".github/workflows/ci.yml","state":"active"},
		{"id":3,"name":"Old","path":".github/workflows/old.yml","state":"disabled_manually"},
		{"id":4,"name":"Dependabot","path":"dynamic/dependabot/dependabot-updates","state":"active"},
		{"id":5,"name":"Broken","path":".github/workflows/broken.yml","state":"active"}]}`)
	var deployReads atomic.Int32
	fake.on("GET /repos/octo-org/widgets/contents/.github/workflows/deploy.yml?ref=main", func(w http.ResponseWriter, r *http.Request) {
		deployReads.Add(1)
		_, _ = w.Write([]byte(contentsAnswer("blob-deploy-1", deployYAML)))
	})
	fake.json("GET /repos/octo-org/widgets/contents/.github/workflows/ci.yml?ref=main", 200, contentsAnswer("blob-ci", "on: [push]\n"))
	fake.json("GET /repos/octo-org/widgets/contents/.github/workflows/broken.yml?ref=main", 404, `{"message":"Not Found"}`)
	access := testAccess(t, fake)
	list, err := access.Workflows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !list.Readable || len(list.Workflows) != 5 {
		t.Fatalf("list = %+v", list)
	}
	byName := map[string]Workflow{}
	for _, workflow := range list.Workflows {
		byName[workflow.Name] = workflow
	}
	if d := byName["Deploy"].Dispatch; !d.Available || len(d.Inputs) != 2 {
		t.Fatalf("deploy = %+v", d)
	}
	for _, name := range []string{"CI", "Old", "Dependabot", "Broken"} {
		if d := byName[name].Dispatch; d.Available || d.Message == "" || d.Inputs == nil {
			t.Errorf("%s = %+v", name, d)
		}
	}
	// A second read of the same blob is answered from the parse cache; the
	// contents are still asked for, since their sha is what names the entry.
	if _, err := access.Workflows(context.Background()); err != nil {
		t.Fatal(err)
	}
	if deployReads.Load() != 2 {
		t.Fatalf("deploy read %d times", deployReads.Load())
	}
	if _, ok := dispatchCache.get("octo-org/widgets\x00.github/workflows/deploy.yml\x00blob-deploy-1"); !ok {
		t.Fatalf("the parse was not cached")
	}
}

func TestWorkflowsGitHubWillNotListAreASentence(t *testing.T) {
	fake := newFake(t)
	fake.json("GET /repos/octo-org/widgets/actions/workflows?per_page=100", 403, `{"message":"Resource not accessible by integration"}`)
	list, err := testAccess(t, fake).Workflows(context.Background())
	if err != nil || list.Readable || list.Message == "" || list.Workflows == nil {
		t.Fatalf("list = %+v %v", list, err)
	}
	limited := newFake(t)
	limited.on("GET /repos/octo-org/widgets/actions/workflows?per_page=100", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1900000000")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	})
	if _, err := testAccess(t, limited).Workflows(context.Background()); KindOf(err) != KindRateLimited {
		t.Fatalf("rate limit = %v", err)
	}
}

func dispatchFake(t *testing.T) *fakeGitHub {
	fake := newFake(t)
	fake.json("GET /repos/octo-org/widgets/actions/workflows/1", 200, `{"id":1,"name":"Deploy","path":".github/workflows/deploy.yml","state":"active"}`)
	fake.json("GET /repos/octo-org/widgets/contents/.github/workflows/deploy.yml?ref=release%2F1", 200, contentsAnswer("blob-deploy-2", deployYAML))
	fake.json("GET /repos/octo-org/widgets/actions/workflows/2", 200, `{"id":2,"name":"CI","path":".github/workflows/ci.yml","state":"active"}`)
	fake.json("GET /repos/octo-org/widgets/contents/.github/workflows/ci.yml?ref=main", 200, contentsAnswer("blob-ci-2", "on: push\n"))
	return fake
}

func TestDispatchChecksTheInputsAgainstTheFileOnItsBranch(t *testing.T) {
	fake := dispatchFake(t)
	fake.json("POST /repos/octo-org/widgets/actions/workflows/1/dispatches", 200, `{"workflow_run_id":77,"run_url":"https://api.github.com/x","html_url":"https://github.com/octo-org/widgets/actions/runs/77"}`)
	access := testAccess(t, fake)
	result, err := access.Dispatch(context.Background(), 1, DispatchRequest{Ref: "release/1", Inputs: map[string]string{"environment": "production", "dry_run": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != 77 || !strings.HasSuffix(result.HTMLURL, "/runs/77") {
		t.Fatalf("result = %+v", result)
	}
	body := fake.body("POST /repos/octo-org/widgets/actions/workflows/1/dispatches")
	if body["ref"] != "release/1" || fmt.Sprint(body["inputs"]) != "map[dry_run:true environment:production]" {
		t.Fatalf("body = %v", body)
	}

	for name, request := range map[string]DispatchRequest{
		"missing required": {Ref: "release/1", Inputs: map[string]string{"dry_run": "false"}},
		"not an option":    {Ref: "release/1", Inputs: map[string]string{"environment": "moon"}},
		"unknown input":    {Ref: "release/1", Inputs: map[string]string{"environment": "staging", "extra": "x"}},
		"not a boolean":    {Ref: "release/1", Inputs: map[string]string{"environment": "staging", "dry_run": "yes"}},
	} {
		_, err := access.Dispatch(context.Background(), 1, request)
		var bad *InvalidError
		if !errorsAs(err, &bad) {
			t.Errorf("%s: %v", name, err)
		}
	}
	_, err = access.Dispatch(context.Background(), 2, DispatchRequest{})
	wantInvalid(t, err)
	posts := 0
	for _, call := range fake.calls {
		if strings.HasPrefix(call, "POST") {
			posts++
		}
	}
	if posts != 1 {
		t.Fatalf("dispatches sent = %d: %v", posts, fake.calls)
	}
}

func TestDispatchRefusalsBeforeAnyRead(t *testing.T) {
	fake := newFake(t)
	access := testAccess(t, fake)
	_, err := access.Dispatch(context.Background(), 1, DispatchRequest{Ref: "a..b"})
	wantInvalid(t, err)
	inputs := map[string]string{}
	for i := range 26 {
		inputs[fmt.Sprint(i)] = "x"
	}
	_, err = access.Dispatch(context.Background(), 1, DispatchRequest{Inputs: inputs})
	wantInvalid(t, err)
	_, err = withRole(testAccess(t, fake), "triage").Dispatch(context.Background(), 1, DispatchRequest{})
	wantNotPermitted(t, err, "needs write")
	if fake.callCount() != 0 {
		t.Fatalf("GitHub was asked: %v", fake.calls)
	}
}

func TestDispatchAnsweredWithNoContentHasNoRun(t *testing.T) {
	fake := dispatchFake(t)
	fake.on("POST /repos/octo-org/widgets/actions/workflows/1/dispatches", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	result, err := testAccess(t, fake).Dispatch(context.Background(), 1, DispatchRequest{Ref: "release/1", Inputs: map[string]string{"environment": "staging"}})
	if err != nil || result.RunID != 0 || result.HTMLURL != "" {
		t.Fatalf("result = %+v %v", result, err)
	}
}

func TestRunsRerunAndCancel(t *testing.T) {
	fake := newFake(t)
	fake.json("GET /repos/octo-org/widgets/actions/workflows/3/runs?branch=main&page=1&per_page=30", 200, `{"workflow_runs":[
		{"id":10,"name":"CI","display_title":"Fix it","workflow_id":3,"event":"push","status":"completed","conclusion":"failure",
		 "head_branch":"main","head_sha":"`+sha40+`","actor":{"login":"mona"},"run_number":4,"run_attempt":2,
		 "created_at":"2026-11-01T10:00:00Z","updated_at":"2026-11-01T10:05:00Z","html_url":"https://github.com/r/10"},
		{"id":11,"status":"in_progress","conclusion":null}]}`)
	fake.json("POST /repos/octo-org/widgets/actions/runs/10/rerun-failed-jobs", 201, `{}`)
	fake.json("POST /repos/octo-org/widgets/actions/runs/10/rerun", 201, `{}`)
	fake.json("POST /repos/octo-org/widgets/actions/runs/11/cancel", 202, `{}`)
	fake.json("POST /repos/octo-org/widgets/actions/runs/12/cancel", 409, `{"message":"Cannot cancel a workflow run that is completed."}`)
	access := testAccess(t, fake)
	page, err := access.Runs(context.Background(), 3, "main", 1)
	if err != nil {
		t.Fatal(err)
	}
	if run := page.Runs[0]; run.Title != "Fix it" || run.Conclusion != "failure" || run.Actor != "mona" || run.RunAttempt != 2 || run.CreatedAt.IsZero() {
		t.Fatalf("run = %+v", run)
	}
	if page.Runs[1].Conclusion != "" {
		t.Fatalf("an unfinished run has a conclusion: %+v", page.Runs[1])
	}
	if err := access.Rerun(context.Background(), 10, true); err != nil {
		t.Fatal(err)
	}
	if err := access.Rerun(context.Background(), 10, false); err != nil {
		t.Fatal(err)
	}
	if err := access.CancelRun(context.Background(), 11); err != nil {
		t.Fatal(err)
	}
	if err := access.CancelRun(context.Background(), 12); KindOf(err) != KindConflict {
		t.Fatalf("cancel of a finished run = %v", err)
	}
	calls := fake.callCount()
	reader := withRole(testAccess(t, fake), "triage")
	wantNotPermitted(t, reader.Rerun(context.Background(), 10, false), "needs write")
	wantNotPermitted(t, reader.CancelRun(context.Background(), 10), "needs write")
	_, err = access.Runs(context.Background(), 0, "a b", 1)
	wantInvalid(t, err)
	if fake.callCount() != calls {
		t.Fatalf("GitHub was asked: %v", fake.calls)
	}
}
