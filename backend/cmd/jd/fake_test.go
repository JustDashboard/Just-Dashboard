package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const goodKey = "vpsd_testkey0123456789abcdef"

// fakeVar is a variable the fake dashboard holds, value and all, so a test
// can check jd never shows it.
type fakeVar struct {
	value, sensitivity string
	scopes             []string
}

// fakeDashboard answers the routes jd calls the way the real backend does,
// for one deployment "web" (7) whose production environment is 70.
type fakeDashboard struct {
	t      *testing.T
	server *httptest.Server

	mu            sync.Mutex
	revision      int
	vars          map[string]fakeVar
	maintenanceOn bool
	finalState    string
	transcriptHit int
	conflicts     int // how many variable writes to refuse with revision_conflict
	refuse        map[string]*apiError
	requests      []string
	bodies        map[string][]map[string]any
	fleetBody     []byte
}

func newFake(t *testing.T) *fakeDashboard {
	t.Helper()
	f := &fakeDashboard{t: t, revision: 3, finalState: "succeeded", refuse: map[string]*apiError{},
		bodies: map[string][]map[string]any{}, vars: map[string]fakeVar{
			"EXISTING": {value: "existing-secret-value", sensitivity: "secret", scopes: []string{"runtime"}},
		}}
	f.fleetBody = []byte(`{"deployments":[{"id":7,"name":"web","environmentId":70,"environmentName":"Production",` +
		`"environmentKind":"production","sourceKind":"image","sourceRepository":"nginx:1.27-alpine","health":"healthy",` +
		`"endpoint":"http://127.0.0.1:20007","liveReleaseId":102,"lastRun":{"id":500,"runNumber":4,"state":"succeeded",` +
		`"operation":"deploy","requestedAt":"2026-10-27T10:00:00Z"}},{"id":8,"name":"Api","environmentId":80,` +
		`"environmentName":"Production","environmentKind":"production","health":"healthy"}],"activeWork":[],"slots":{}}`)
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

// The description the fake serves: enough of a document to tell it came
// through unchanged.
var (
	fakeOpenAPIJSON = []byte(`{"openapi":"3.1.0","info":{"title":"Just Dashboard deployment API","version":"0.0.0"},"paths":{}}` + "\n")
	fakeOpenAPIYAML = []byte("\"openapi\": \"3.1.0\"\n\"paths\": {}\n")
)

func digestOf(value string) string {
	sum := sha256.Sum256(append([]byte(value), 0))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (f *fakeDashboard) write(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if raw, ok := body.([]byte); ok {
		w.Write(raw)
		return
	}
	json.NewEncoder(w).Encode(body)
}

func (f *fakeDashboard) fail(w http.ResponseWriter, e *apiError) {
	f.write(w, e.Status, map[string]any{"error": map[string]string{"code": e.Code, "message": e.Message}})
}

func (f *fakeDashboard) variableList() []map[string]any {
	out := []map[string]any{}
	for name, v := range f.vars {
		masked := "configured"
		if v.sensitivity == "secret" {
			masked = "••••••••"
		}
		out = append(out, map[string]any{"name": name, "sensitivity": v.sensitivity, "scopes": v.scopes,
			"masked": masked, "valueDigest": digestOf(v.value), "pending": true, "desiredRevision": f.revision})
	}
	return out
}

func (f *fakeDashboard) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	route := r.Method + " " + r.URL.Path
	f.requests = append(f.requests, route+"?"+r.URL.RawQuery)
	var body map[string]any
	if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
		if r.Header.Get("Content-Type") != "application/json" {
			f.fail(w, &apiError{Status: 415, Code: "json_content_type_required", Message: "request body must use application/json"})
			return
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			f.fail(w, &apiError{Status: 400, Code: "bad_request", Message: err.Error()})
			return
		}
	}
	f.bodies[route] = append(f.bodies[route], body)
	switch r.Header.Get("Authorization") {
	case "Bearer " + goodKey:
	case "Bearer vpsd_mcpkey0123456789":
		f.fail(w, &apiError{Status: 403, Code: "key_scope", Message: "this is an MCP key: it opens the MCP endpoint, /api/v1/mcp, and nothing else"})
		return
	default:
		f.fail(w, &apiError{Status: 401, Code: "unauthorized", Message: "api token invalid, revoked or expired"})
		return
	}
	if e, ok := f.refuse[route]; ok {
		f.fail(w, e)
		return
	}
	env := "/api/v1/deploy/7/environments/70"
	switch {
	case route == "GET /api/v1/deploy/" && r.URL.Query().Get("view") == "fleet":
		f.write(w, 200, f.fleetBody)
	case route == "GET /api/v1/deploy/openapi.json":
		f.write(w, 200, fakeOpenAPIJSON)
	case route == "GET /api/v1/deploy/openapi.yaml":
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.WriteHeader(200)
		w.Write(fakeOpenAPIYAML)
	case route == "GET "+env+"/configuration":
		f.write(w, 200, map[string]any{"revision": f.revision, "build": map[string]any{"method": "image"},
			"runtime":   map[string]any{"image": "nginx:1.27-alpine", "internalPort": 80, "strategy": "stop_first"},
			"variables": f.variableList(), "domains": []any{}, "checks": []any{}, "dependencies": []any{},
			"source": map[string]any{"kind": "image", "image": "nginx:1.27-alpine"}, "pending": map[string]any{"pending": true}})
	case route == "PUT "+env+"/configuration":
		if int(body["revision"].(float64)) != f.revision {
			f.fail(w, &apiError{Status: 409, Code: "revision_conflict", Message: fmt.Sprintf("current revision is %d", f.revision)})
			return
		}
		f.revision++
		f.write(w, 200, map[string]any{"revision": f.revision})
	case route == "GET "+env+"/variables":
		f.write(w, 200, f.variableList())
	case strings.HasPrefix(route, "PUT "+env+"/variables/"):
		name := strings.TrimPrefix(r.URL.Path, env+"/variables/")
		if f.conflicts > 0 {
			f.conflicts--
			f.revision++ // somebody else wrote in between
			f.fail(w, &apiError{Status: 409, Code: "revision_conflict", Message: fmt.Sprintf("current revision is %d", f.revision)})
			return
		}
		if int(body["revision"].(float64)) != f.revision {
			f.fail(w, &apiError{Status: 409, Code: "revision_conflict", Message: fmt.Sprintf("current revision is %d", f.revision)})
			return
		}
		scopes := []string{}
		for _, s := range body["scopes"].([]any) {
			scopes = append(scopes, s.(string))
		}
		v := fakeVar{value: body["value"].(string), sensitivity: body["sensitivity"].(string), scopes: scopes}
		f.vars[name] = v
		f.revision++
		f.write(w, 200, map[string]any{"variable": map[string]any{"name": name, "sensitivity": v.sensitivity,
			"scopes": v.scopes, "valueDigest": digestOf(v.value)}, "desiredRevision": f.revision})
	case strings.HasPrefix(route, "DELETE "+env+"/variables/"):
		name := strings.TrimPrefix(r.URL.Path, env+"/variables/")
		delete(f.vars, name)
		f.revision++
		f.write(w, 200, map[string]any{"desiredRevision": f.revision})
	case route == "POST "+env+"/variables/import":
		if r.URL.Query().Get("dryRun") == "1" {
			f.write(w, 200, map[string]any{"variables": []any{map[string]any{"name": "A", "line": 1, "change": "added"}}})
			return
		}
		f.revision++
		f.vars["A"] = fakeVar{value: "from-file", sensitivity: "secret", scopes: []string{"runtime"}}
		f.write(w, 200, map[string]any{"variables": f.variableList(), "desiredRevision": f.revision})
	case route == "POST "+env+"/runs":
		f.write(w, 202, map[string]any{"id": 600, "runNumber": 5, "state": "queued", "projectId": 7, "environmentId": 70})
	case route == "POST "+env+"/rollback":
		f.write(w, 202, map[string]any{"id": 601, "runNumber": 6, "state": "queued"})
	case route == "GET /api/v1/deploy/7/environments":
		f.write(w, 200, []byte(`[{"id":70,"name":"Production","slug":"production","kind":"production","liveReleaseId":102},`+
			`{"id":71,"name":"Staging","slug":"staging","kind":"staging","liveReleaseId":150}]`))
	case route == "GET /api/v1/deploy/8/environments":
		f.write(w, 200, []byte(`[{"id":80,"name":"Production","slug":"production","kind":"production"}]`))
	case route == "POST "+env+"/promote":
		f.write(w, 202, map[string]any{"id": 602, "runNumber": 7, "state": "queued", "operation": "promote"})
	case route == "POST /api/v1/deploy/7/environments/71/promote":
		// Staging takes a preview's image; the fake holds the server's
		// rule that only a preview's is, and only from an approved head.
		if body["source"] != "preview" {
			f.fail(w, &apiError{Status: 409, Code: "promotion_target", Message: "releases are promoted into production"})
			return
		}
		if body["preview"] == float64(46) {
			f.fail(w, &apiError{Status: 422, Code: "preview_promotion_unapproved",
				Message: "PR #46 comes from a fork (octocat/web), and a fork's code is promoted only at a commit an administrator approved: commit deadbee of PR #46 is waiting for an administrator's approval"})
			return
		}
		f.write(w, 202, map[string]any{"id": 604, "runNumber": 9, "state": "queued", "operation": "promote", "environmentId": 71})
	case route == "POST "+env+"/rollback-watch/rollback":
		f.write(w, 202, map[string]any{"id": 603, "runNumber": 8, "state": "queued", "operation": "rollback"})
	case route == "POST "+env+"/rollback-watch/keep":
		f.write(w, 200, map[string]any{"state": "closed", "endReason": "kept", "releaseNumber": 3, "warmReleaseNumber": 2,
			"endMessage": "ion kept release #3"})
	case route == "GET "+env+"/rollback-watch":
		f.write(w, 200, map[string]any{"policy": map[string]any{"keepWarm": true, "windowSeconds": 600, "automatic": true},
			"watch":   map[string]any{"state": "watching", "releaseNumber": 3, "warmReleaseNumber": 2, "expiresAt": "2026-10-27T10:10:00Z"},
			"preview": false})
	case route == "GET "+env+"/maintenance":
		f.write(w, 200, map[string]any{"driver": "caddy", "route": true, "ready": true, "on": f.maintenanceOn})
	case route == "PUT "+env+"/maintenance":
		f.maintenanceOn, _ = body["on"].(bool)
		f.write(w, 200, map[string]any{"driver": "caddy", "route": true, "ready": true, "on": f.maintenanceOn})
	case route == "GET "+env+"/rehearsal":
		f.write(w, 200, map[string]any{"enabled": false, "connectionId": 0, "whenUnavailable": "block"})
	case route == "GET "+env+"/releases":
		f.write(w, 200, []byte(`[{"id":102,"number":3,"state":"live","createdAt":"2026-10-27T10:00:00Z"},`+
			`{"id":101,"number":2,"state":"retained","sourceRevision":"abcdef1234567","createdAt":"2026-10-26T10:00:00Z"},`+
			`{"id":100,"number":1,"state":"retained","createdAt":"2026-10-25T10:00:00Z"}]`))
	case route == "GET /api/v1/deploy/7/runs":
		f.write(w, 200, []byte(`{"runs":[{"id":600,"runNumber":5,"state":"succeeded","operation":"deploy",`+
			`"trigger":"manual","actor":"ion","requestedAt":"2026-10-27T11:00:00Z"},{"id":500,"runNumber":4,`+
			`"state":"succeeded","operation":"deploy","requestedAt":"2026-10-27T10:00:00Z"}],"running":false}`))
	case strings.HasPrefix(route, "GET /api/v1/deploy/7/runs/") && strings.HasSuffix(route, "/transcript"):
		f.transcript(w, r)
	case strings.HasPrefix(route, "GET /api/v1/deploy/7/runs/"):
		f.write(w, 200, map[string]any{"run": map[string]any{"id": 600, "runNumber": 5, "state": f.finalState}, "steps": []any{}})
	case route == "POST /api/v1/deploy/drafts":
		f.write(w, 201, map[string]any{"id": "d1", "revision": 1, "currentStep": "intent"})
	case route == "PUT /api/v1/deploy/drafts/d1":
		step, _ := body["step"].(string)
		f.write(w, 200, map[string]any{"id": "d1", "revision": int(body["revision"].(float64)) + 1, "currentStep": step})
	case route == "GET /api/v1/deploy/drafts/d1":
		f.write(w, 200, map[string]any{"id": "d1", "revision": 5, "currentStep": "configuration"})
	case route == "POST /api/v1/deploy/drafts/d1/detect":
		f.write(w, 200, map[string]any{"id": "d1", "revision": int(body["revision"].(float64)) + 1, "currentStep": "detection"})
	case route == "POST /api/v1/deploy/drafts/d1/preflight":
		f.write(w, 200, map[string]any{"draft": map[string]any{"id": "d1", "revision": int(body["revision"].(float64)) + 1},
			"preflight": map[string]any{"findings": []any{
				map[string]any{"code": "image_resolved", "severity": "pass", "title": "Image resolves"},
				map[string]any{"code": "no_domain", "severity": "warning", "title": "No domain"}}}})
	case route == "POST /api/v1/deploy/drafts/d1/commit":
		f.write(w, 201, map[string]any{"projectId": 9, "environmentId": 90, "planRevision": 1, "created": true})
	default:
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, "404 page not found\n")
	}
}

// transcript hands out a run in three reads: two lines while running, the
// last line once it has ended, then nothing more.
func (f *fakeDashboard) transcript(w http.ResponseWriter, r *http.Request) {
	f.transcriptHit++
	after := r.URL.Query().Get("after")
	run := map[string]any{"id": 600, "runNumber": 5, "state": "running"}
	lines := []any{}
	next := 0
	switch after {
	case "0":
		lines = append(lines, map[string]any{"seq": 1, "step": "Pull image", "stream": "stdout", "text": "pulling nginx"},
			map[string]any{"seq": 2, "step": "Pull image", "stream": "stdout", "text": "pulled"})
		next = 2
	case "2":
		run["state"] = f.finalState
		lines = append(lines, map[string]any{"seq": 3, "step": "Activate", "stream": "stdout", "text": "serving"})
		next = 3
	default:
		run["state"] = f.finalState
		next = 3
	}
	if run["state"] == "failed" {
		run["terminalCode"], run["terminalReason"] = "readiness_failed", "the container never answered on port 80"
	}
	f.write(w, 200, map[string]any{"run": run, "lines": lines, "next": next, "more": false})
}

func (f *fakeDashboard) bodiesFor(route string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[route]
}

// invocation is one run of jd against the fake, with its own config dir.
type invocation struct {
	env   map[string]string
	stdin string
}

func (f *fakeDashboard) env(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{"JD_URL": f.server.URL, "JD_KEY": goodKey, "XDG_CONFIG_HOME": t.TempDir(), "HOME": t.TempDir()}
}

func jd(t *testing.T, in invocation, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(in.stdin), &stdout, &stderr, func(k string) string { return in.env[k] })
	return code, stdout.String(), stderr.String()
}

func TestMain(m *testing.M) {
	followEvery = time.Millisecond
	os.Exit(m.Run())
}

func configFile(env map[string]string) string {
	return filepath.Join(env["XDG_CONFIG_HOME"], "jd", "config.json")
}
