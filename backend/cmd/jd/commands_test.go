package main

import (
	"strings"
	"testing"
)

// A value read from stdin reaches the dashboard exactly, and is printed
// nowhere: jd answers with the digest the dashboard computed.
func TestVarsSetFromStdinNeverShowsTheValue(t *testing.T) {
	f := newFake(t)
	const secret = "s3cr3t-value-from-a-pipe"
	code, stdout, stderr := jd(t, invocation{env: f.env(t), stdin: secret + "\n"},
		"vars", "set", "web", "DB_PASSWORD=-", "MODE=production", "--scope", "runtime,build")
	if code != exitOK {
		t.Fatalf("exit %d %s %s", code, stdout, stderr)
	}
	if got := f.vars["DB_PASSWORD"]; got.value != secret || got.sensitivity != "secret" ||
		strings.Join(got.scopes, ",") != "runtime,build" {
		t.Errorf("stored %+v", got)
	}
	if strings.Contains(stdout+stderr, secret) || strings.Contains(stdout+stderr, "production\n") {
		t.Errorf("a value was printed:\n%s%s", stdout, stderr)
	}
	if !strings.Contains(stdout, "set DB_PASSWORD (secret, runtime,build) "+shortDigest(digestOf(secret))) {
		t.Errorf("no digest line:\n%s", stdout)
	}
	// The inline value earns the history hint; --quiet drops it.
	if !strings.Contains(stderr, "shell history") {
		t.Errorf("no hint about the inline value: %s", stderr)
	}
	if _, _, stderr := jd(t, invocation{env: f.env(t)}, "vars", "set", "web", "A=b", "--quiet"); strings.Contains(stderr, "history") {
		t.Errorf("--quiet still hinted: %s", stderr)
	}
	// Two values cannot both come from one stdin.
	if code, _, _ := jd(t, invocation{env: f.env(t), stdin: "x"}, "vars", "set", "web", "A=-", "B=-"); code != exitUsage {
		t.Errorf("two stdin values: exit %d", code)
	}
	// The revision advances with each write rather than being read again.
	puts := f.bodiesFor("PUT /api/v1/deploy/7/environments/70/variables/MODE")
	if len(puts) == 0 || puts[0]["revision"].(float64) != 4 {
		t.Errorf("second write used %v; want the revision the first returned", puts)
	}
}

// The list has digests and no values, because a deploy key gets none.
func TestVarsListShowsDigestsNotValues(t *testing.T) {
	f := newFake(t)
	code, stdout, stderr := jd(t, invocation{env: f.env(t)}, "vars", "ls", "web")
	if code != exitOK {
		t.Fatalf("exit %d %s", code, stderr)
	}
	if !strings.Contains(stdout, "EXISTING") || !strings.Contains(stdout, shortDigest(digestOf("existing-secret-value"))) {
		t.Errorf("no name and digest:\n%s", stdout)
	}
	if strings.Contains(stdout, "existing-secret-value") || strings.Contains(stdout, "••") {
		t.Errorf("a value or a mask was printed:\n%s", stdout)
	}
	code, stdout, _ = jd(t, invocation{env: f.env(t)}, "vars", "rm", "web", "EXISTING")
	if code != exitOK || !strings.Contains(stdout, "removed EXISTING") || len(f.vars) != 0 {
		t.Errorf("rm: exit %d %s %v", code, stdout, f.vars)
	}
	code, stdout, _ = jd(t, invocation{env: f.env(t), stdin: "A=from-file\n"}, "vars", "import", "web", "-", "--dry-run")
	if code != exitOK || !strings.Contains(stdout, "added") || f.vars["A"].value != "" {
		t.Errorf("dry run: exit %d %s", code, stdout)
	}
	code, stdout, _ = jd(t, invocation{env: f.env(t), stdin: "A=from-file\n"}, "vars", "import", "web", "-")
	if code != exitOK || strings.Contains(stdout, "from-file") || f.vars["A"].value != "from-file" {
		t.Errorf("import: exit %d %s", code, stdout)
	}
	if body := f.bodiesFor("POST /api/v1/deploy/7/environments/70/variables/import"); body[len(body)-1]["dotenv"] != "A=from-file\n" {
		t.Errorf("import sent %v", body)
	}
}

// --wait follows the run's transcript and exits with its result.
func TestDeployWaitExitsWithTheRunsResult(t *testing.T) {
	f := newFake(t)
	code, stdout, stderr := jd(t, invocation{env: f.env(t)}, "deploy", "web", "--wait", "--ref", "v2")
	if code != exitOK {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	for _, want := range []string{"==> Pull image\npulling nginx\npulled\n", "==> Activate\nserving\n"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("transcript lacks %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "Run #5 succeeded") {
		t.Errorf("no result line: %s", stderr)
	}
	if body := f.bodiesFor("POST /api/v1/deploy/7/environments/70/runs"); body[0]["operation"] != "deploy" || body[0]["ref"] != "v2" {
		t.Errorf("queued with %v", body)
	}

	f = newFake(t)
	f.finalState = "failed"
	code, stdout, stderr = jd(t, invocation{env: f.env(t)}, "deploy", "web", "--wait")
	if code != exitRefused || !strings.Contains(stderr, "readiness_failed") || !strings.Contains(stdout, "serving") {
		t.Errorf("a failed run: exit %d\n%s%s", code, stdout, stderr)
	}
	// Without --wait the run is queued and jd returns at once.
	code, stdout, _ = jd(t, invocation{env: f.env(t)}, "deploy", "web")
	if code != exitOK || !strings.Contains(stdout, "Queued run #5") {
		t.Errorf("without --wait: exit %d %s", code, stdout)
	}
	// logs --follow ends with the run's result too.
	code, _, _ = jd(t, invocation{env: f.env(t)}, "logs", "web", "--run", "5", "--follow")
	if code != exitRefused {
		t.Errorf("logs --follow of a failed run: exit %d", code)
	}
}

// Without --to, a rollback goes to the retained release before the live one.
func TestRollbackGoesToTheReleaseBeforeTheLiveOne(t *testing.T) {
	f := newFake(t)
	code, stdout, stderr := jd(t, invocation{env: f.env(t)}, "rollback", "web", "--wait")
	if code != exitOK {
		t.Fatalf("exit %d %s %s", code, stdout, stderr)
	}
	if body := f.bodiesFor("POST /api/v1/deploy/7/environments/70/rollback"); len(body) != 1 || body[0]["releaseId"].(float64) != 101 {
		t.Errorf("rolled back with %v", body)
	}
	if !strings.Contains(stderr, "release 101, number 2, commit abcdef1") {
		t.Errorf("the target was not named: %s", stderr)
	}
	jd(t, invocation{env: f.env(t)}, "rollback", "web", "--to", "100")
	if body := f.bodiesFor("POST /api/v1/deploy/7/environments/70/rollback"); body[1]["releaseId"].(float64) != 100 {
		t.Errorf("--to sent %v", body[1])
	}
}

// What the dashboard refuses, jd explains in words a person can act on.
func TestRefusalsAreExplained(t *testing.T) {
	f := newFake(t)
	f.refuse["PUT /api/v1/deploy/7/environments/70/configuration"] = &apiError{Status: 403, Code: "session_required",
		Message: "this action requires an interactive session, not an API token"}
	env := f.env(t)
	code, _, stderr := jd(t, invocation{env: env, stdin: `{"build":{},"runtime":{}}`}, "config", "set", "web", "-")
	if code != exitRefused || !strings.Contains(stderr, "signed in to the dashboard") || !strings.Contains(stderr, "interactive session") ||
		!strings.Contains(stderr, "(session_required)") {
		t.Errorf("session_required: exit %d %s", code, stderr)
	}
	f.refuse["GET /api/v1/deploy/"] = &apiError{Status: 403, Code: "key_scope", Message: "this key reaches only the deployments chosen for it"}
	code, _, stderr = jd(t, invocation{env: env}, "deployments")
	if code != exitRefused || !strings.Contains(stderr, "(key_scope)") || !strings.Contains(stderr, "chosen for it") {
		t.Errorf("key_scope: exit %d %s", code, stderr)
	}
	delete(f.refuse, "GET /api/v1/deploy/")
	env["JD_KEY"] = "vpsd_revoked0000000000"
	code, _, stderr = jd(t, invocation{env: env}, "deployments")
	if code != exitNoSession || !strings.Contains(stderr, "jd login") || strings.Contains(stderr, "vpsd_revoked0000000000") {
		t.Errorf("401: exit %d %s", code, stderr)
	}
	env["JD_KEY"] = "vpsd_mcpkey0123456789"
	code, _, stderr = jd(t, invocation{env: map[string]string{"XDG_CONFIG_HOME": t.TempDir()}},
		"login", f.server.URL, "--key", "vpsd_mcpkey0123456789")
	if code != exitRefused || !strings.Contains(stderr, "deploy key") {
		t.Errorf("login with an MCP key: exit %d %s", code, stderr)
	}
	env["JD_KEY"] = goodKey
	code, _, stderr = jd(t, invocation{env: env}, "deploy", "nosuch")
	if code != exitRefused || !strings.Contains(stderr, "web, Api") {
		t.Errorf("an unknown deployment: exit %d %s", code, stderr)
	}
}

// A conflicting write is read again and retried once, never forever.
func TestARevisionConflictIsRetriedOnce(t *testing.T) {
	f := newFake(t)
	f.conflicts = 1
	code, stdout, stderr := jd(t, invocation{env: f.env(t)}, "vars", "set", "web", "A=b", "--quiet")
	if code != exitOK || !strings.Contains(stdout, "set A") {
		t.Fatalf("one conflict: exit %d %s %s", code, stdout, stderr)
	}
	if n := len(f.bodiesFor("PUT /api/v1/deploy/7/environments/70/variables/A")); n != 2 {
		t.Errorf("%d writes; want the refused one and its retry", n)
	}
	f.conflicts = 2
	code, _, stderr = jd(t, invocation{env: f.env(t)}, "vars", "set", "web", "B=c", "--quiet")
	if code != exitRefused || !strings.Contains(stderr, "revision_conflict") {
		t.Errorf("two conflicts: exit %d %s", code, stderr)
	}
	// A configuration document that names its revision is not retried over
	// somebody else's newer change.
	code, _, stderr = jd(t, invocation{env: f.env(t), stdin: `{"revision":1,"build":{},"runtime":{},"variables":[]}`},
		"config", "set", "web", "-")
	if code != exitRefused || !strings.Contains(stderr, "edited against revision 1") {
		t.Errorf("a stale document: exit %d %s", code, stderr)
	}
	code, stdout, stderr = jd(t, invocation{env: f.env(t), stdin: `{"build":{},"runtime":{},"variables":[],"pending":{}}`},
		"config", "set", "web", "-")
	if code != exitOK || !strings.Contains(stdout, "saved the configuration") {
		t.Errorf("a document without a revision: exit %d %s %s", code, stdout, stderr)
	}
	sent := f.bodiesFor("PUT /api/v1/deploy/7/environments/70/configuration")
	if last := sent[len(sent)-1]; last["variables"] != nil || last["pending"] != nil {
		t.Errorf("read-only fields were sent: %v", last)
	}
}

// jd create walks the draft the way the new-deployment page does.
func TestCreateWalksTheDraft(t *testing.T) {
	f := newFake(t)
	code, stdout, stderr := jd(t, invocation{env: f.env(t)}, "create", "--name", "hello", "--image", "nginx:1.27-alpine", "--ack-warnings")
	if code != exitOK || !strings.Contains(stdout, "created hello: deployment 9, environment 90") {
		t.Fatalf("exit %d %s %s", code, stdout, stderr)
	}
	var order []string
	for _, r := range f.requests {
		if strings.Contains(r, "/drafts") {
			order = append(order, strings.SplitN(r, "?", 2)[0])
		}
	}
	want := "POST /api/v1/deploy/drafts,PUT /api/v1/deploy/drafts/d1,PUT /api/v1/deploy/drafts/d1," +
		"POST /api/v1/deploy/drafts/d1/detect,PUT /api/v1/deploy/drafts/d1,POST /api/v1/deploy/drafts/d1/preflight," +
		"POST /api/v1/deploy/drafts/d1/commit"
	if strings.Join(order, ",") != want {
		t.Errorf("draft calls\n%s\nwant\n%s", strings.Join(order, ","), want)
	}
	saves := f.bodiesFor("PUT /api/v1/deploy/drafts/d1")
	source := saves[1]["source"].(map[string]any)
	if saves[0]["step"] != "intent" || source["kind"] != "image" || source["mode"] != "image_reference" ||
		saves[2]["step"] != "configuration" {
		t.Errorf("saves %v", saves)
	}
	commit := f.bodiesFor("POST /api/v1/deploy/drafts/d1/commit")[0]
	if acks := commit["acknowledgedWarnings"].([]any); len(acks) != 1 || acks[0] != "no_domain" {
		t.Errorf("commit acknowledged %v", commit)
	}
}

// The human form of a configuration says how many containers each release
// runs when it is more than one.
func TestConfigGetSaysHowManyReplicas(t *testing.T) {
	f := newFake(t)
	code, stdout, stderr := jd(t, invocation{env: f.env(t)}, "config", "get", "web")
	if code != exitOK {
		t.Fatalf("exit %d %s", code, stderr)
	}
	if !strings.Contains(stdout, "nginx:1.27-alpine · port 80 · stop first · 2 replicas") {
		t.Errorf("runtime line does not name the replicas:\n%s", stdout)
	}
}

// jd canary reads the trial side by side, and an operator's word on it is a
// POST to the trial's own route.
func TestCanaryStatusPromoteAndAbort(t *testing.T) {
	f := newFake(t)
	code, stdout, stderr := jd(t, invocation{env: f.env(t)}, "canary", "status", "web")
	if code != exitOK {
		t.Fatalf("exit %d %s", code, stderr)
	}
	for _, want := range []string{"release #4 on 10% beside release #3", "release #4 (canary)  203", "0.5%", "91 ms", "release #3 (live)    1840", "Within the live release's figures"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status lacks %q:\n%s", want, stdout)
		}
	}
	code, stdout, _ = jd(t, invocation{env: f.env(t)}, "canary", "promote", "web")
	if code != exitOK || !strings.Contains(stdout, "release #4 of web (Production) is being promoted") {
		t.Errorf("promote: exit %d %s", code, stdout)
	}
	code, stdout, _ = jd(t, invocation{env: f.env(t)}, "canary", "abort", "web")
	if code != exitOK || !strings.Contains(stdout, "aborted: it is removed and release #3 keeps every request") {
		t.Errorf("abort: exit %d %s", code, stdout)
	}
	f.mu.Lock()
	posted := 0
	for _, request := range f.requests {
		if strings.HasPrefix(request, "POST /api/v1/deploy/7/environments/70/canary/") {
			posted++
		}
	}
	f.mu.Unlock()
	if posted != 2 {
		t.Errorf("decisions posted = %d", posted)
	}
	if code, _, _ := jd(t, invocation{env: f.env(t)}, "canary", "wait", "web"); code != exitUsage {
		t.Errorf("an unknown canary verb: exit %d", code)
	}
}
