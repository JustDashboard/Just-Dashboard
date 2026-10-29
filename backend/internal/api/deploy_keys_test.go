package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// sessionOnlyBefore is every deployment route that answered an ordinary API
// key with session_required before the deploy key existed, read off the
// router at 4795fbf2. An ordinary key must still get that answer on each.
var sessionOnlyBefore = []string{
	"POST /deploy/blueprints/{id}/render",
	"GET /deploy/credentials", "POST /deploy/credentials", "PUT /deploy/credentials/{id}", "POST /deploy/credentials/{id}/test",
	"GET /deploy/drafts", "POST /deploy/drafts", "DELETE /deploy/drafts/{draft}", "GET /deploy/drafts/{draft}",
	"PUT /deploy/drafts/{draft}", "POST /deploy/drafts/{draft}/commit", "POST /deploy/drafts/{draft}/detect",
	"POST /deploy/drafts/{draft}/preflight",
	"POST /deploy/game/import/preview",
	"DELETE /deploy/github-app/", "GET /deploy/github-app/", "POST /deploy/github-app/callback",
	"POST /deploy/github-app/manifest", "GET /deploy/github-app/repositories",
	"POST /deploy/import/adopt", "POST /deploy/import/preview",
	"POST /deploy/log-drains", "DELETE /deploy/log-drains/{drain}", "PUT /deploy/log-drains/{drain}",
	"PUT /deploy/log-drains/{drain}/enabled", "POST /deploy/log-drains/{drain}/test",
	"POST /deploy/notifications", "DELETE /deploy/notifications/{channel}", "PUT /deploy/notifications/{channel}",
	"PUT /deploy/notifications/{channel}/enabled", "POST /deploy/notifications/{channel}/test",
	"DELETE /deploy/shared-variables/{name}", "PUT /deploy/shared-variables/{name}", "GET /deploy/shared-variables/{name}/reveal",
	"POST /deploy/{id}/alerts", "DELETE /deploy/{id}/alerts/{alert}", "PUT /deploy/{id}/alerts/{alert}", "POST /deploy/{id}/alerts/{alert}/test",
	"POST /deploy/{id}/duplicate",
	"PUT /deploy/{id}/environments/{env}/configuration", "POST /deploy/{id}/environments/{env}/detect",
	"PUT /deploy/{id}/environments/{env}/git-policy", "PUT /deploy/{id}/environments/{env}/releases/{release}/pin",
	"POST /deploy/{id}/environments/{env}/schedules", "POST /deploy/{id}/environments/{env}/schedules/test",
	"DELETE /deploy/{id}/environments/{env}/schedules/{schedule}", "PUT /deploy/{id}/environments/{env}/schedules/{schedule}",
	"PUT /deploy/{id}/environments/{env}/source",
	"POST /deploy/{id}/environments/{env}/triggers", "DELETE /deploy/{id}/environments/{env}/triggers/{trigger}",
	"PUT /deploy/{id}/environments/{env}/triggers/{trigger}", "GET /deploy/{id}/environments/{env}/triggers/{trigger}/deliveries",
	"POST /deploy/{id}/environments/{env}/triggers/{trigger}/rotate-secret", "POST /deploy/{id}/environments/{env}/triggers/{trigger}/test",
	"POST /deploy/{id}/environments/{env}/variables/import", "DELETE /deploy/{id}/environments/{env}/variables/{name}",
	"PUT /deploy/{id}/environments/{env}/variables/{name}", "POST /deploy/{id}/environments/{env}/variables/{name}/generate",
	"GET /deploy/{id}/environments/{env}/variables/{name}/reveal", "POST /deploy/{id}/environments/{env}/variables/{name}/rotate",
	"POST /deploy/{id}/environments/{env}/watch-paths/simulate",
	"PUT /deploy/{id}/game/properties",
	"POST /deploy/{id}/previews/approvals/{approval}/approve", "POST /deploy/{id}/previews/approvals/{approval}/reject",
	"POST /deploy/{id}/pull-requests/{number}/comment", "POST /deploy/{id}/pull-requests/{number}/merge",
	"POST /deploy/{id}/pull-requests/{number}/preview", "POST /deploy/{id}/pull-requests/{number}/preview/close",
	"POST /deploy/{id}/removal-plan",
	"DELETE /deploy/{id}/shared-variables/{name}", "PUT /deploy/{id}/shared-variables/{name}", "GET /deploy/{id}/shared-variables/{name}/reveal",
	"POST /deploy/{id}/unarchive",
	// Session-only from the change that added each, beside the deploy key.
	"POST /deploy/secret-stores", "PUT /deploy/secret-stores/{store}", "POST /deploy/secret-stores/{store}/test",
	"DELETE /deploy/secret-stores/{store}",
	"POST /deploy/{id}/environments", "DELETE /deploy/{id}/environments/{env}",
	"PUT /deploy/{id}/environments/{env}/pages/{page}", "PUT /deploy/{id}/environments/{env}/rollback-policy",
	"PUT /deploy/{id}/environments/{env}/rehearsal",
	"PUT /deploy/{id}/environments/{env}/vulnerabilities",
}

// keyBench is a server with an administrator, a session, and the three kinds
// of key an administrator can hold.
type keyBench struct {
	t        *testing.T
	s        *Server
	h        http.Handler
	admin    *auth.User
	session  string
	ordinary string
	mcp      string
	deploy   string
	deployID int64
}

func newKeyBench(t *testing.T) *keyBench {
	t.Helper()
	s := testServer(t)
	// Every route is walked several times; the limiters would answer the
	// walk rather than the routes.
	s.apiLim, s.destrLim = httpx.NewLimiter(1<<20, 1<<20), httpx.NewLimiter(1<<20, 1<<20)
	wireDeployPlanningAndSources(t, s, []string{t.TempDir()})
	b := &keyBench{t: t, s: s, h: s.Routes(), session: signIn(t, s)}
	var adminID int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM users WHERE username = 'tester'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	admin, err := s.Auth.UserByID(t.Context(), adminID)
	if err != nil {
		t.Fatal(err)
	}
	b.admin = admin
	if _, b.ordinary, err = s.Auth.CreateAPIToken(t.Context(), admin, "scripts", auth.RoleAdmin, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, b.mcp, err = s.Auth.CreateMCPToken(t.Context(), admin, "claude-code", "", time.Hour, nil, auth.DeployRun); err != nil {
		t.Fatal(err)
	}
	tok, secret, err := s.Auth.CreateDeployToken(t.Context(), admin, "github-actions", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	b.deploy, b.deployID = secret, tok.ID
	return b
}

// call sends a request with a key as a bearer token, as jd does.
func (b *keyBench) call(key, method, path string, body any) *httptest.ResponseRecorder {
	b.t.Helper()
	var reader *strings.Reader
	switch v := body.(type) {
	case nil:
		reader = strings.NewReader("")
	case string:
		reader = strings.NewReader(v)
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			b.t.Fatal(err)
		}
		reader = strings.NewReader(string(raw))
	}
	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Authorization", "Bearer "+key)
	if reader.Len() > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	b.h.ServeHTTP(w, req)
	return w
}

// inSession sends a request as the signed-in administrator.
func (b *keyBench) inSession(method, path string, body any) *httptest.ResponseRecorder {
	b.t.Helper()
	raw := ""
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			b.t.Fatal(err)
		}
		raw = string(encoded)
	}
	return (&client{t: b.t, h: b.h, cookie: b.session}).do(method, path, raw, nil)
}

func keyErrorCode(w *httptest.ResponseRecorder) string {
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return body.Error.Code
}

// keyAudit is the newest audit row with an action.
type keyAudit struct {
	Actor, Username, KeyName, Target, Detail string
	KeyID                                    int64
	Success                                  bool
}

func (b *keyBench) lastAudit(action string) keyAudit {
	b.t.Helper()
	var row keyAudit
	err := b.s.Store.DB.QueryRow(`SELECT actor, username, key_id, key_name, target, detail, success
		 FROM audit_log WHERE action = ? ORDER BY id DESC LIMIT 1`, action).
		Scan(&row.Actor, &row.Username, &row.KeyID, &row.KeyName, &row.Target, &row.Detail, &row.Success)
	if err != nil {
		b.t.Fatalf("no audit entry for %s: %v", action, err)
	}
	return row
}

// Every line of the table is a route the router has, so a renamed route
// cannot leave a stale line that silently opens nothing, or the wrong thing.
func TestTheDeployKeyTableNamesOnlyRealRoutes(t *testing.T) {
	b := newKeyBench(t)
	real := map[string]bool{}
	for _, rt := range apiRoutes(t, b.h) {
		real[rt.method+" "+strings.TrimPrefix(rt.pattern, "/api/v1")] = true
	}
	for route := range deployKeyRoutes {
		if !real[route] {
			t.Errorf("deployKeyRoutes names %s, which the router does not have", route)
		}
	}
}

// The gate folds a route with and without a trailing slash together, as a
// mount answers both; that is sound only while no two routes differ by the
// slash alone.
func TestNoTwoRoutesDifferByATrailingSlash(t *testing.T) {
	b := newKeyBench(t)
	seen := map[string]string{}
	for _, rt := range apiRoutes(t, b.h) {
		key := rt.method + " " + strings.TrimSuffix(rt.pattern, "/")
		if other, ok := seen[key]; ok && other != rt.pattern {
			t.Errorf("%s %s and %s differ only by a trailing slash", rt.method, other, rt.pattern)
		}
		seen[key] = rt.pattern
	}
}

// A mount's root is one route with or without its slash — the dashboard's
// own proxy drops it — and a deploy key gets the one answer for both.
func TestADeployKeyReachesAMountRootWithOrWithoutItsSlash(t *testing.T) {
	b := newKeyBench(t)
	for _, path := range []string{"/api/v1/deploy", "/api/v1/deploy/", "/api/v1/deploy?view=fleet", "/api/v1/deploy/blueprints"} {
		if w := b.call(b.deploy, http.MethodGet, path, nil); w.Code != http.StatusOK {
			t.Errorf("GET %s = %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/deploy/github-app", "/api/v1/deploy/github-app/"} {
		if w := b.call(b.deploy, http.MethodGet, path, nil); keyErrorCode(w) != "session_required" {
			t.Errorf("GET %s = %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := b.call(b.deploy, http.MethodPost, "/api/v1/deploy", map[string]any{}); keyErrorCode(w) != "key_scope" {
		t.Errorf("POST /api/v1/deploy = %d %s", w.Code, w.Body.String())
	}
}

// Every deployment route that is a session's alone has a decision for the
// deploy key, written down, rather than a default. A route added later that
// trips this needs one line in deployKeyRoutes (deploy_keys.go): open it to
// the key, or keep it a session's and say why.
func TestEverySessionOnlyDeploymentRouteHasADeployKeyDecision(t *testing.T) {
	b := newKeyBench(t)
	for _, rt := range apiRoutes(t, b.h) {
		if !strings.HasPrefix(rt.pattern, "/api/v1/deploy/") {
			continue
		}
		w := b.call(b.ordinary, rt.method, rt.path, "{}")
		route := rt.method + " " + strings.TrimPrefix(rt.pattern, "/api/v1")
		if keyErrorCode(w) != "session_required" {
			continue
		}
		if _, decided := ruleForDeployKey(rt.method, strings.TrimPrefix(rt.pattern, "/api/v1")); !decided {
			t.Errorf("%s is session-only and has no line in deployKeyRoutes (deploy_keys.go): decide whether a deploy key may make it", route)
		}
	}
}

// The decision table, walked: on every route of the API a deploy key gets
// the answer its line says — admitted, session_required, or key_scope — and
// key_scope on everything outside the deployments. An ordinary key keeps the
// answers it had: session_required on exactly the routes it got it on before,
// and never key_scope.
func TestADeployKeyGetsTheDecidedAnswerOnEveryRoute(t *testing.T) {
	b := newKeyBench(t)
	before := map[string]bool{}
	for _, route := range sessionOnlyBefore {
		before[route] = true
	}
	walked := map[string]bool{}
	for _, rt := range apiRoutes(t, b.h) {
		route := rt.method + " " + strings.TrimPrefix(rt.pattern, "/api/v1")
		walked[route] = true
		key := b.call(b.deploy, rt.method, rt.path, "{}")
		got := keyErrorCode(key)
		if !strings.HasPrefix(rt.pattern, "/api/v1/deploy/") || rt.pattern == "/api/v1/deploy/{id}/hooks/{triggerID}" {
			// Outside the deployments: refused, or a route that refuses
			// every caller before it reads a credential.
			if key.Code < 400 || (key.Code == http.StatusForbidden && got != "key_scope" && got != "network_denied" && got != "csrf_required") {
				t.Errorf("%s answered a deploy key with %d: %s", route, key.Code, strings.TrimSpace(key.Body.String()))
			}
			continue
		}
		rule, listed := ruleForDeployKey(rt.method, strings.TrimPrefix(rt.pattern, "/api/v1"))
		switch {
		case !listed || rule.deny != "":
			if key.Code != http.StatusForbidden || got != "key_scope" {
				t.Errorf("%s: a deploy key got %d %s, want 403 key_scope", route, key.Code, strings.TrimSpace(key.Body.String()))
			}
		case rule.session != "":
			if key.Code != http.StatusForbidden || got != "session_required" || !strings.Contains(key.Body.String(), rule.session) {
				t.Errorf("%s: a deploy key got %d %s, want 403 session_required saying why", route, key.Code, strings.TrimSpace(key.Body.String()))
			}
		default:
			if got == "key_scope" || got == "session_required" || got == "forbidden" {
				t.Errorf("%s: a deploy key was refused: %d %s", route, key.Code, strings.TrimSpace(key.Body.String()))
			}
		}

		ordinary := b.call(b.ordinary, rt.method, rt.path, "{}")
		switch code := keyErrorCode(ordinary); {
		case before[route] && code != "session_required":
			t.Errorf("%s: an ordinary key got %d %s, want session_required as before", route, ordinary.Code, strings.TrimSpace(ordinary.Body.String()))
		case !before[route] && (code == "session_required" || code == "key_scope"):
			t.Errorf("%s: an ordinary key got %d %s, which it did not before", route, ordinary.Code, strings.TrimSpace(ordinary.Body.String()))
		}
	}
	for route := range before {
		if !walked[route] {
			t.Errorf("%s was session-only and is no longer a route", route)
		}
	}
}

// The writes the key exists for, end to end through the routes: a variable
// set with the key is never handed back to a key — not by the list, not by a
// reveal, not by a generated value — and every write is audited under the
// key's id and name.
func TestADeployKeyWritesVariablesItCannotReadBack(t *testing.T) {
	b := newKeyBench(t)
	projectID, environmentID := seedCommittedDeployment(t, b.s, "shop",
		deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "nginx:1.27-alpine"},
		deploy.SourceIdentity{Kind: deploy.SourceImage, Repository: "nginx:1.27-alpine"})
	env := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	const value = "s3cr3t-value-only-the-container-reads"

	set := b.call(b.deploy, http.MethodPut, env+"/variables/STRIPE_KEY", map[string]any{
		"revision": 1, "value": value, "sensitivity": "secret", "scopes": []string{"runtime"},
	})
	if set.Code != http.StatusOK || strings.Contains(set.Body.String(), value) {
		t.Fatalf("set with a deploy key = %d %s", set.Code, set.Body.String())
	}
	entry := b.lastAudit("deploy.variable.update")
	if entry.Actor != "token" || entry.KeyID != b.deployID || entry.KeyName != "github-actions" || entry.Username != "tester" ||
		entry.Target != "STRIPE_KEY" || !entry.Success || strings.Contains(entry.Detail, value) {
		t.Fatalf("the write was audited as %+v", entry)
	}

	sum := sha256.Sum256(append([]byte(value), 0))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	for name, key := range map[string]string{"the deploy key": b.deploy, "an ordinary key": b.ordinary} {
		list := b.call(key, http.MethodGet, env+"/variables", nil)
		if list.Code != http.StatusOK || strings.Contains(list.Body.String(), value) || !strings.Contains(list.Body.String(), digest) {
			t.Errorf("%s listed %d %s, want the name and digest %s without the value", name, list.Code, list.Body.String(), digest)
		}
	}
	reveal := b.call(b.deploy, http.MethodGet, env+"/variables/STRIPE_KEY/reveal", nil)
	if reveal.Code != http.StatusForbidden || keyErrorCode(reveal) != "session_required" || strings.Contains(reveal.Body.String(), value) {
		t.Fatalf("reveal with a deploy key = %d %s", reveal.Code, reveal.Body.String())
	}
	if refused := b.lastAudit("auth.deploy_key.refused"); refused.KeyID != b.deployID || refused.Success ||
		!strings.Contains(refused.Target, "/variables/STRIPE_KEY/reveal") || !strings.Contains(refused.Detail, "session_required") {
		t.Fatalf("the refused reveal was audited as %+v", refused)
	}
	if reveal := b.call(b.ordinary, http.MethodGet, env+"/variables/STRIPE_KEY/reveal", nil); keyErrorCode(reveal) != "session_required" {
		t.Fatalf("reveal with an ordinary key = %d %s", reveal.Code, reveal.Body.String())
	}
	// The person in the dashboard still reads it: it was written, not lost.
	if reveal := b.inSession(http.MethodGet, env+"/variables/STRIPE_KEY/reveal", nil); reveal.Code != http.StatusOK || !strings.Contains(reveal.Body.String(), value) {
		t.Fatalf("reveal in a session = %d %s", reveal.Code, reveal.Body.String())
	}

	var revision struct {
		DesiredRevision int `json:"desiredRevision"`
	}
	decodeDeployResponse(t, set.Body.Bytes(), &revision)
	generated := b.call(b.deploy, http.MethodPost, env+"/variables/SESSION_SECRET/generate", map[string]any{
		"revision": revision.DesiredRevision, "scopes": []string{"runtime"},
	})
	if generated.Code != http.StatusCreated || strings.Contains(generated.Body.String(), "generatedValue") {
		t.Fatalf("generate with a deploy key = %d %s", generated.Code, generated.Body.String())
	}
	decodeDeployResponse(t, generated.Body.Bytes(), &revision)
	rotated := b.call(b.deploy, http.MethodPost, env+"/variables/SESSION_SECRET/rotate", map[string]any{"revision": revision.DesiredRevision})
	if rotated.Code != http.StatusOK || strings.Contains(rotated.Body.String(), "generatedValue") {
		t.Fatalf("rotate with a deploy key = %d %s", rotated.Code, rotated.Body.String())
	}
	decodeDeployResponse(t, rotated.Body.Bytes(), &revision)
	// A session still gets the generated value it asked for.
	inSession := b.inSession(http.MethodPost, env+"/variables/SESSION_SECRET/rotate", map[string]any{"revision": revision.DesiredRevision})
	if inSession.Code != http.StatusOK || !strings.Contains(inSession.Body.String(), "generatedValue") {
		t.Fatalf("rotate in a session = %d %s", inSession.Code, inSession.Body.String())
	}
	decodeDeployResponse(t, inSession.Body.Bytes(), &revision)

	imported := b.call(b.deploy, http.MethodPost, env+"/variables/import", map[string]any{
		"revision": revision.DesiredRevision, "dotenv": "LOG_LEVEL=debug\nREGION=eu-west\n",
		"sensitivity": "plain", "scopes": []string{"runtime"},
	})
	if imported.Code != http.StatusOK || strings.Contains(imported.Body.String(), "eu-west") {
		t.Fatalf("import with a deploy key = %d %s", imported.Code, imported.Body.String())
	}
	decodeDeployResponse(t, imported.Body.Bytes(), &revision)
	if entry := b.lastAudit("deploy.variable.import"); entry.KeyID != b.deployID {
		t.Fatalf("the import was audited as %+v", entry)
	}
	removed := b.call(b.deploy, http.MethodDelete, env+"/variables/REGION", map[string]any{"revision": revision.DesiredRevision})
	if removed.Code != http.StatusOK {
		t.Fatalf("delete with a deploy key = %d %s", removed.Code, removed.Body.String())
	}
	if entry := b.lastAudit("deploy.variable.delete"); entry.KeyID != b.deployID || entry.Target != "REGION" {
		t.Fatalf("the delete was audited as %+v", entry)
	}

	// A shared variable is written the same way and read back the same way:
	// not at all.
	shared := b.call(b.deploy, http.MethodPut, "/api/v1/deploy/shared-variables/SMTP_PASSWORD", map[string]any{
		"value": value, "sensitivity": "secret",
	})
	if shared.Code != http.StatusCreated || strings.Contains(shared.Body.String(), value) {
		t.Fatalf("shared variable with a deploy key = %d %s", shared.Code, shared.Body.String())
	}
	if list := b.call(b.deploy, http.MethodGet, "/api/v1/deploy/shared-variables", nil); list.Code != http.StatusOK || strings.Contains(list.Body.String(), value) {
		t.Fatalf("shared variables listed %d %s", list.Code, list.Body.String())
	}
	if reveal := b.call(b.deploy, http.MethodGet, "/api/v1/deploy/shared-variables/SMTP_PASSWORD/reveal", nil); keyErrorCode(reveal) != "session_required" {
		t.Fatalf("shared reveal with a deploy key = %d %s", reveal.Code, reveal.Body.String())
	}
	if gone := b.call(b.deploy, http.MethodDelete, "/api/v1/deploy/shared-variables/SMTP_PASSWORD", nil); gone.Code >= 300 {
		t.Fatalf("shared delete with a deploy key = %d %s", gone.Code, gone.Body.String())
	}
	if entry := b.lastAudit("deploy.shared_variable.delete"); entry.KeyID != b.deployID {
		t.Fatalf("the shared delete was audited as %+v", entry)
	}
}

// Making a deploy key is a person's act: an administrator, in a session. No
// key makes one, not even an administrator's own.
func TestOnlyAnAdministratorInASessionMintsADeployKey(t *testing.T) {
	b := newKeyBench(t)
	projectID, _ := seedCommittedDeployment(t, b.s, "blog",
		deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "nginx:1.27-alpine"},
		deploy.SourceIdentity{Kind: deploy.SourceImage})
	body := map[string]any{"name": "ci", "ttlDays": 30, "deploymentIds": []int64{projectID}}
	for name, key := range map[string]string{"an ordinary key": b.ordinary, "an MCP key": b.mcp} {
		if w := b.call(key, http.MethodPost, "/api/v1/tokens/deploy", body); w.Code != http.StatusForbidden {
			t.Errorf("%s minted: %d %s", name, w.Code, w.Body.String())
		}
	}
	if w := b.call(b.deploy, http.MethodPost, "/api/v1/tokens/deploy", body); keyErrorCode(w) != "key_scope" {
		t.Errorf("a deploy key minted: %d %s", w.Code, w.Body.String())
	}
	limited := &client{t: t, h: b.h, cookie: signInAs(t, b.s, "operator", auth.RoleLimited)}
	if w := doDeployJSON(t, limited, http.MethodPost, "/api/v1/tokens/deploy", body); w.Code != http.StatusForbidden || keyErrorCode(w) != "forbidden" {
		t.Errorf("a limited account minted: %d %s", w.Code, w.Body.String())
	}

	w := b.inSession(http.MethodPost, "/api/v1/tokens/deploy", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("an administrator's session = %d %s", w.Code, w.Body.String())
	}
	var minted struct {
		Token  auth.APIToken `json:"token"`
		Secret string        `json:"secret"`
	}
	decodeDeployResponse(t, w.Body.Bytes(), &minted)
	if minted.Token.Scope != auth.ScopeDeploy || minted.Token.Role != auth.RoleAdmin || !strings.HasPrefix(minted.Secret, "vpsd_") ||
		!slices.Equal(minted.Token.DeploymentIDs, []int64{projectID}) || minted.Token.ExpiresAt == nil {
		t.Fatalf("minted %+v", minted.Token)
	}
	if entry := b.lastAudit("dashboard.token.create"); entry.Target != "ci" || entry.Actor != "session" ||
		!strings.Contains(entry.Detail, `"scope":"deploy"`) || !strings.Contains(entry.Detail, `"blog"`) {
		t.Fatalf("minting was audited as %+v", entry)
	}
	// The list shows what it reaches, and never the secret.
	list := b.inSession(http.MethodGet, "/api/v1/tokens/", nil)
	if strings.Contains(list.Body.String(), minted.Secret) || !strings.Contains(list.Body.String(), fmt.Sprintf(`"deploymentIds":[%d]`, projectID)) {
		t.Fatalf("token list = %s", list.Body.String())
	}

	for name, bad := range map[string]map[string]any{
		"no name":                  {"name": " ", "deploymentIds": []int64{}},
		"a deployment not there":   {"name": "ci", "deploymentIds": []int64{projectID + 50}},
		"a deployment named twice": {"name": "ci", "deploymentIds": []int64{projectID, projectID}},
	} {
		if w := b.inSession(http.MethodPost, "/api/v1/tokens/deploy", bad); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
}

// A key that is revoked, expired, or whose owner is disabled is no key; one
// whose owner lost the administrator role keeps only what the owner has.
func TestADeployKeyGoesWithItsRevocationExpiryAndOwner(t *testing.T) {
	b := newKeyBench(t)
	projectID, environmentID := seedCommittedDeployment(t, b.s, "api",
		deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "nginx:1.27-alpine"},
		deploy.SourceIdentity{Kind: deploy.SourceImage})
	variable := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/variables/A", projectID, environmentID)
	write := map[string]any{"revision": 1, "value": "x", "sensitivity": "plain", "scopes": []string{"runtime"}}

	if w := b.call(b.deploy, http.MethodGet, "/api/v1/deploy/", nil); w.Code != http.StatusOK {
		t.Fatalf("a live key = %d %s", w.Code, w.Body.String())
	}
	if w := b.inSession(http.MethodDelete, fmt.Sprintf("/api/v1/tokens/%d", b.deployID), nil); w.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", w.Code, w.Body.String())
	}
	if w := b.call(b.deploy, http.MethodGet, "/api/v1/deploy/", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("a revoked key = %d %s", w.Code, w.Body.String())
	}

	expiring, secret, err := b.s.Auth.CreateDeployToken(t.Context(), b.admin, "laptop", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.s.Store.DB.Exec(`UPDATE api_tokens SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Minute).Unix(), expiring.ID); err != nil {
		t.Fatal(err)
	}
	if w := b.call(secret, http.MethodGet, "/api/v1/deploy/", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("an expired key = %d %s", w.Code, w.Body.String())
	}

	_, secret, err = b.s.Auth.CreateDeployToken(t.Context(), b.admin, "ci", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.s.Store.DB.Exec(`UPDATE users SET role = 'limited' WHERE id = ?`, b.admin.ID); err != nil {
		t.Fatal(err)
	}
	if w := b.call(secret, http.MethodPut, variable, write); w.Code != http.StatusForbidden || keyErrorCode(w) != "forbidden" {
		t.Fatalf("a key whose owner is now limited = %d %s", w.Code, w.Body.String())
	}
	if _, err := b.s.Store.DB.Exec(`UPDATE users SET role = 'admin', disabled = 1 WHERE id = ?`, b.admin.ID); err != nil {
		t.Fatal(err)
	}
	if w := b.call(secret, http.MethodPut, variable, write); w.Code != http.StatusUnauthorized {
		t.Fatalf("a key whose owner is disabled = %d %s", w.Code, w.Body.String())
	}
}

// A key narrowed to some deployments reaches those, lists only those, and
// reaches no route that would create a deployment or touch what all of them
// share — and not another deployment's environment through its own id.
func TestADeployKeyNarrowedToDeploymentsReachesOnlyThem(t *testing.T) {
	b := newKeyBench(t)
	image := deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "nginx:1.27-alpine"}
	mine, myEnv := seedCommittedDeployment(t, b.s, "mine", image, deploy.SourceIdentity{Kind: deploy.SourceImage})
	theirs, theirEnv := seedCommittedDeployment(t, b.s, "theirs", image, deploy.SourceIdentity{Kind: deploy.SourceImage})
	_, key, err := b.s.Auth.CreateDeployToken(t.Context(), b.admin, "mine-ci", time.Hour, []int64{mine})
	if err != nil {
		t.Fatal(err)
	}
	write := map[string]any{"revision": 1, "value": "x", "sensitivity": "plain", "scopes": []string{"runtime"}}

	for _, path := range []string{"/api/v1/deploy/", "/api/v1/deploy/?view=fleet", "/api/v1/deploy/?view=archived"} {
		w := b.call(key, http.MethodGet, path, nil)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"theirs"`) {
			t.Errorf("GET %s = %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := b.call(key, http.MethodGet, "/api/v1/deploy/?view=fleet", nil); !strings.Contains(w.Body.String(), `"mine"`) {
		t.Errorf("the fleet left out the key's own deployment: %s", w.Body.String())
	}
	if w := b.call(key, http.MethodPut, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/variables/A", mine, myEnv), write); w.Code != http.StatusOK {
		t.Errorf("its own deployment = %d %s", w.Code, w.Body.String())
	}
	for name, request := range map[string][2]string{
		"another deployment":                 {http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d", theirs)},
		"another deployment's variables":     {http.MethodPut, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/variables/A", theirs, theirEnv)},
		"another's environment through mine": {http.MethodPut, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/variables/A", mine, theirEnv)},
		"another deployment's runs":          {http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/runs", theirs)},
		"duplicating another's":              {http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/duplicate", theirs)},
		"a server shared variable":           {http.MethodPut, "/api/v1/deploy/shared-variables/SMTP"},
		"the fleet's traffic":                {http.MethodGet, "/api/v1/deploy/traffic"},
		"the secret stores":                  {http.MethodGet, "/api/v1/deploy/secret-stores"},
	} {
		if w := b.call(key, request[0], request[1], write); w.Code != http.StatusForbidden || keyErrorCode(w) != "key_scope" {
			t.Errorf("%s = %d %s, want 403 key_scope", name, w.Code, w.Body.String())
		}
	}
	// Drafts are the key's own to make, and duplicating a deployment it
	// reaches leaves a draft that is its own too (the test of drafts below).
	if w := b.call(key, http.MethodPost, "/api/v1/deploy/drafts", nil); w.Code != http.StatusCreated {
		t.Errorf("a new draft = %d %s", w.Code, w.Body.String())
	}
	if w := b.call(key, http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/duplicate", mine), map[string]any{"name": "mine-copy"}); w.Code != http.StatusCreated {
		t.Errorf("duplicating its own = %d %s", w.Code, w.Body.String())
	}
	shared := b.call(key, http.MethodPut, fmt.Sprintf("/api/v1/deploy/%d/shared-variables/TOKEN", mine), map[string]any{"value": "v", "sensitivity": "secret"})
	if shared.Code != http.StatusCreated {
		t.Errorf("its own deployment's shared variable = %d %s", shared.Code, shared.Body.String())
	}
	if w := b.call(key, http.MethodPut, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/variables/SMTP", mine, myEnv), map[string]any{
		"revision": 2, "reference": "${{server.SMTP}}", "sensitivity": "secret", "scopes": []string{"runtime"},
	}); keyErrorCode(w) != "session_required" || !strings.Contains(w.Body.String(), "server shared variable") {
		t.Errorf("pointing at a server shared variable = %d %s", w.Code, w.Body.String())
	}
}

// What a deploy key writes may not reach past the deployment's own container
// to the server, which would be every secret the session-only routes keep
// from it; and a deployment that already does is changed only by a person.
func TestADeployKeyCannotGiveADeploymentTheHost(t *testing.T) {
	b := newKeyBench(t)
	image := deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "nginx:1.27-alpine"}
	projectID, environmentID := seedCommittedDeployment(t, b.s, "web", image, deploy.SourceIdentity{Kind: deploy.SourceImage})
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/configuration", projectID, environmentID)
	configuration := func(runtime map[string]any, tasks []map[string]any) map[string]any {
		build := map[string]any{"method": "image", "secrets": []any{}, "releaseTasks": tasks}
		runtime["strategy"], runtime["image"], runtime["internalPort"] = "stop_first", "nginx:1.27-alpine", 80
		return map[string]any{"revision": 1, "build": build, "runtime": runtime, "dependencies": []any{}, "checks": []any{}, "domains": []any{}}
	}
	for name, body := range map[string]map[string]any{
		"a privileged container": configuration(map[string]any{"privileged": true}, []map[string]any{}),
		"the host's network":     configuration(map[string]any{"hostNetwork": true}, []map[string]any{}),
		"a capability":           configuration(map[string]any{"capabilities": []string{"SYS_ADMIN"}}, []map[string]any{}),
		"a host path":            configuration(map[string]any{"mounts": []map[string]any{{"source": "/etc", "target": "/host-etc", "ownership": "external"}}}, []map[string]any{}),
		"a host release task": configuration(map[string]any{}, []map[string]any{{
			"name": "migrate", "command": "cat /var/lib/just-dashboard/vpsd.db", "timeoutSeconds": 60, "env": []string{},
		}}),
	} {
		w := b.call(b.deploy, http.MethodPut, path, body)
		if w.Code != http.StatusForbidden || keyErrorCode(w) != "session_required" || !strings.Contains(w.Body.String(), "reaches the server itself") {
			t.Errorf("%s: a deploy key got %d %s", name, w.Code, w.Body.String())
		}
	}
	// An ordinary workload is the key's to configure.
	if w := b.call(b.deploy, http.MethodPut, path, configuration(map[string]any{"memoryMb": 256}, []map[string]any{})); w.Code != http.StatusOK {
		t.Fatalf("an ordinary configuration = %d %s", w.Code, w.Body.String())
	}

	// A deployment a person made privileged is deployed by the key, never
	// reconfigured or pointed at something else by it.
	privileged, privilegedEnv := seedKeyDeployment(t, b.s, "vpn", image, `{"strategy":"stop_first","privileged":true}`, 0)
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", privileged, privilegedEnv)
	if w := b.call(b.deploy, http.MethodPut, base+"/configuration", configuration(map[string]any{"memoryMb": 256}, []map[string]any{})); keyErrorCode(w) != "session_required" {
		t.Errorf("reconfiguring a privileged deployment = %d %s", w.Code, w.Body.String())
	}
	if w := b.call(b.deploy, http.MethodPut, base+"/source", map[string]any{
		"revision": 1, "kind": "image", "mode": "image_reference", "image": "attacker/image:latest",
	}); keyErrorCode(w) != "session_required" {
		t.Errorf("repointing a privileged deployment = %d %s", w.Code, w.Body.String())
	}
	if w := b.call(b.deploy, http.MethodPut, base+"/variables/A", map[string]any{
		"revision": 1, "value": "x", "sensitivity": "plain", "scopes": []string{"runtime"},
	}); w.Code != http.StatusOK {
		t.Errorf("a variable on a privileged deployment = %d %s", w.Code, w.Body.String())
	}
	// The session that made it so can still change it.
	if w := b.inSession(http.MethodPut, base+"/configuration", map[string]any{
		"revision": 2, "build": map[string]any{"method": "image", "secrets": []any{}, "releaseTasks": []any{}},
		"runtime":      map[string]any{"strategy": "stop_first", "image": "nginx:1.27-alpine", "internalPort": 80, "privileged": true},
		"dependencies": []any{}, "checks": []any{}, "domains": []any{},
	}); keyErrorCode(w) == "session_required" {
		t.Errorf("a session was refused: %d %s", w.Code, w.Body.String())
	}

	// Drafts: a source on the server is refused when it is saved, and a plan
	// with host authority when it is committed.
	created := b.call(b.deploy, http.MethodPost, "/api/v1/deploy/drafts", nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("draft = %d %s", created.Code, created.Body.String())
	}
	var draft struct {
		ID       string `json:"id"`
		Revision int    `json:"revision"`
	}
	decodeDeployResponse(t, created.Body.Bytes(), &draft)
	if w := b.call(b.deploy, http.MethodPut, "/api/v1/deploy/drafts/"+draft.ID, map[string]any{
		"revision": draft.Revision, "step": "source",
		"source": map[string]any{"kind": "git", "mode": "local_checkout", "localPath": "/root"},
	}); keyErrorCode(w) != "session_required" || !strings.Contains(w.Body.String(), "folder on this server") {
		t.Errorf("a draft from a folder on the server = %d %s", w.Code, w.Body.String())
	}
	if _, err := b.s.Store.DB.Exec(`UPDATE deploy_drafts SET data_json = ? WHERE id = ?`,
		`{"source":{"kind":"image","mode":"image_reference","image":"nginx:1.27-alpine"},"configuration":{"build":{"method":"image"},"runtime":{"strategy":"stop_first","hostNetwork":true}}}`, draft.ID); err != nil {
		t.Fatal(err)
	}
	if w := b.call(b.deploy, http.MethodPost, "/api/v1/deploy/drafts/"+draft.ID+"/commit", map[string]any{"revision": draft.Revision}); keyErrorCode(w) != "session_required" ||
		!strings.Contains(w.Body.String(), "the host's network") {
		t.Errorf("committing a plan on the host's network = %d %s", w.Code, w.Body.String())
	}
}

// A deploy key cannot choose where a saved credential goes, nor read one or a
// database's connection string through a variable it points at it.
func TestADeployKeyCannotRedirectOrReferenceASavedCredential(t *testing.T) {
	b := newKeyBench(t)
	credential := doDeployJSON(t, &client{t: t, h: b.h, cookie: b.session}, http.MethodPost, "/api/v1/deploy/credentials", map[string]any{
		"name": "github", "kind": "git_bearer", "target": "github.com", "secret": "ghp_real_token",
	})
	if credential.Code != http.StatusCreated {
		t.Fatalf("credential = %d %s", credential.Code, credential.Body.String())
	}
	var saved struct {
		ID int64 `json:"id"`
	}
	decodeDeployResponse(t, credential.Body.Bytes(), &saved)
	source := deploy.DraftSourceConfig{Kind: deploy.SourceGit, Mode: deploy.SourceModeGitURL, URL: "https://github.com/acme/shop.git", Ref: "main", CredentialID: saved.ID}
	projectID, environmentID := seedKeyDeployment(t, b.s, "shop", source, `{"strategy":"stop_first"}`, saved.ID)
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	for name, body := range map[string]map[string]any{
		"the credential to another host": {"revision": 1, "kind": "git", "mode": "git_url", "url": "https://attacker.example/acme/shop.git", "ref": "main", "credentialId": saved.ID},
		"another credential":             {"revision": 1, "kind": "git", "mode": "git_url", "url": source.URL, "ref": "main", "credentialId": saved.ID + 1},
	} {
		if w := b.call(b.deploy, http.MethodPut, base+"/source", body); keyErrorCode(w) != "session_required" || !strings.Contains(w.Body.String(), "credential") {
			t.Errorf("%s = %d %s", name, w.Code, w.Body.String())
		}
	}
	for _, reference := range []string{"${{credential.github}}", fmt.Sprintf("${{credential.%d}}", saved.ID), "${{database.shop}}", "${{store.vault-prod/apps/billing#STRIPE_KEY}}"} {
		w := b.call(b.deploy, http.MethodPut, base+"/variables/LEAK", map[string]any{
			"revision": 1, "reference": reference, "sensitivity": "secret", "scopes": []string{"runtime"},
		})
		if keyErrorCode(w) != "session_required" || strings.Contains(w.Body.String(), "ghp_real_token") {
			t.Errorf("a variable pointing at %s = %d %s", reference, w.Code, w.Body.String())
		}
	}
	// A deployment a person gave a saved credential, through a variable, is
	// deployed by the key and never pointed by it at code of its choosing.
	image := deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "nginx:1.27-alpine"}
	app, appEnv := seedKeyDeployment(t, b.s, "registry-reader", image, `{"strategy":"stop_first"}`, 0)
	appBase := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", app, appEnv)
	if w := b.inSession(http.MethodPut, appBase+"/variables/GH_TOKEN", map[string]any{
		"revision": 1, "reference": "${{credential.github}}", "sensitivity": "secret", "scopes": []string{"runtime"},
	}); w.Code != http.StatusOK {
		t.Fatalf("a person's credential reference = %d %s", w.Code, w.Body.String())
	}
	if w := b.call(b.deploy, http.MethodPut, appBase+"/source", map[string]any{
		"revision": 2, "kind": "image", "mode": "image_reference", "image": "attacker/dump-env:latest",
	}); keyErrorCode(w) != "session_required" || !strings.Contains(w.Body.String(), "GH_TOKEN reads a saved credential") {
		t.Errorf("repointing a deployment that reads a credential = %d %s", w.Code, w.Body.String())
	}

	// A draft's variable becomes a reference by its mode as well as by its
	// reference field; either is refused when the key commits it.
	for _, variable := range []string{
		`{"name":"LEAK","sensitivity":"secret","scopes":["runtime"],"reference":"${{credential.github}}"}`,
		`{"name":"LEAK","sensitivity":"secret","scopes":["runtime"],"valueMode":"reference","value":"${{database.shop}}"}`,
		`{"name":"LEAK","sensitivity":"secret","scopes":["runtime"],"reference":"${{store.vault-prod/apps/billing#STRIPE_KEY}}"}`,
	} {
		created := b.call(b.deploy, http.MethodPost, "/api/v1/deploy/drafts", nil)
		var draft struct {
			ID       string `json:"id"`
			Revision int    `json:"revision"`
		}
		decodeDeployResponse(t, created.Body.Bytes(), &draft)
		if _, err := b.s.Store.DB.Exec(`UPDATE deploy_drafts SET data_json = ? WHERE id = ?`,
			`{"source":{"kind":"image","mode":"image_reference","image":"nginx:1.27-alpine"},"configuration":{"build":{"method":"image"},"runtime":{"strategy":"stop_first"},"variables":[`+variable+`]}}`, draft.ID); err != nil {
			t.Fatal(err)
		}
		w := b.call(b.deploy, http.MethodPost, "/api/v1/deploy/drafts/"+draft.ID+"/commit", map[string]any{"revision": draft.Revision})
		if keyErrorCode(w) != "session_required" || !strings.Contains(w.Body.String(), "LEAK would read") {
			t.Errorf("committing a draft whose variable is %s = %d %s", variable, w.Code, w.Body.String())
		}
	}

	// Credentials themselves are a session's, whatever the key.
	for _, request := range [][2]string{
		{http.MethodGet, "/api/v1/deploy/credentials"},
		{http.MethodPost, fmt.Sprintf("/api/v1/deploy/credentials/%d/test", saved.ID)},
		{http.MethodDelete, fmt.Sprintf("/api/v1/deploy/credentials/%d", saved.ID)},
	} {
		if w := b.call(b.deploy, request[0], request[1], "{}"); keyErrorCode(w) != "session_required" || strings.Contains(w.Body.String(), "ghp_real_token") {
			t.Errorf("%s %s = %d %s", request[0], request[1], w.Code, w.Body.String())
		}
	}
}

// The transcript route reads a run's lines from a sequence on, for jd logs.
func TestTheRunTranscriptRouteRefusesAnotherDeploymentsRun(t *testing.T) {
	b := newKeyBench(t)
	if w := b.call(b.deploy, http.MethodGet, "/api/v1/deploy/1/runs/1/transcript?after=-1", nil); w.Code != http.StatusBadRequest {
		t.Errorf("a negative after = %d %s", w.Code, w.Body.String())
	}
	if w := b.call(b.deploy, http.MethodGet, "/api/v1/deploy/1/runs/1/transcript?limit=0", nil); w.Code != http.StatusBadRequest {
		t.Errorf("a zero limit = %d %s", w.Code, w.Body.String())
	}
	if w := b.call(b.deploy, http.MethodGet, "/api/v1/deploy/1/runs/99/transcript", nil); w.Code != http.StatusNotFound {
		t.Errorf("a run that is not there = %d %s", w.Code, w.Body.String())
	}
}

// seedKeyDeployment is seedCommittedDeployment with the runtime plan and the
// source's credential chosen: a committed revision is immutable, so a
// deployment a person already made privileged, or gave a credential, is
// written that way from the start.
func seedKeyDeployment(t *testing.T, s *Server, name string, source deploy.DraftSourceConfig, runtime string, credentialID int64) (int64, int64) {
	t.Helper()
	sourceJSON, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Store.DB.Exec(
		`INSERT INTO deploy_projects(name, profile, repo_path, branch, compose_file, hook_secret, hook_id, enabled, created_at, updated_at)
		 VALUES(?, 'web', '', 'main', '', '', ?, 1, 0, 0)`, name, name+"-hook")
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := result.LastInsertId()
	result, err = s.Store.DB.Exec(
		`INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		 VALUES(?, 'production', 'production', 'production', 1, 'stop_first', 1, 1, 0, 0)`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ := result.LastInsertId()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, credential_id, identity_json, digest, created_at)
		  VALUES(?, 1, ?, ?, ?, '{}', 'sha256:seed', 0)`, []any{environmentID, source.Kind, string(sourceJSON), credentialID}},
		{`INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at)
		  VALUES(?, 1, 'image', '{"method":"image"}', '{}', '{}', 'sha256:build-seed', 0)`, []any{environmentID}},
		{`INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at)
		  VALUES(?, 1, ?, '{}', 'sha256:runtime-seed', 0)`, []any{environmentID, runtime}},
	} {
		if _, err := s.Store.DB.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return projectID, environmentID
}

// draftRef is what a test keeps of a draft a route answered.
type draftRef struct {
	ID       string `json:"id"`
	Revision int    `json:"revision"`
	KeyID    int64  `json:"keyId"`
}

// seedDraftReadyToCommit writes a draft the preflight step would leave for an
// image, since inspecting an image needs a registry the tests do not have.
func seedDraftReadyToCommit(t *testing.T, s *Server, draftID, name string) {
	t.Helper()
	data := deploy.DraftData{
		Intent: &deploy.DraftIntentConfig{Name: name, Profile: deploy.ProfileWeb},
		Source: &deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "nginx:1.27-alpine"},
		Detection: &deploy.DetectionResult{
			Source: deploy.SourceIdentity{Kind: deploy.SourceImage, Repository: "nginx:1.27-alpine"},
			Candidates: []deploy.DetectedCandidate{{
				ID: "image", Name: name, Profile: deploy.ProfileWeb, BuildMethod: deploy.BuildImage, Confidence: deploy.ConfidenceHigh,
			}},
			SelectedID: "image",
		},
		Configuration: &deploy.PlanConfiguration{
			Build:   deploy.BuildPlanConfig{Method: deploy.BuildImage},
			Runtime: deploy.RuntimePlanConfig{Image: "nginx:1.27-alpine", Strategy: deploy.StrategyStopFirst, InternalPort: 80},
		},
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`UPDATE deploy_drafts SET data_json = ?, current_step = 'preflight', plan_preview = 'seeded', findings_json = '[]' WHERE id = ?`,
		string(raw), draftID); err != nil {
		t.Fatal(err)
	}
}

// A deploy key's drafts are its own: it lists, reads, saves, detects,
// preflights, commits and discards the drafts it made and no session's or
// other key's, whoever owns them. A key narrowed to chosen deployments makes
// drafts too, and the deployment its commit creates is added to its grant in
// the same transaction, so it keeps reaching what it made; a key reaching
// every deployment is given no row, or the first would narrow it.
func TestADeployKeysDraftsAreItsOwnAndANarrowedKeyReachesWhatItMade(t *testing.T) {
	b := newKeyBench(t)
	image := deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "nginx:1.27-alpine"}
	mine, _ := seedCommittedDeployment(t, b.s, "mine", image, deploy.SourceIdentity{Kind: deploy.SourceImage})
	theirs, _ := seedCommittedDeployment(t, b.s, "theirs", image, deploy.SourceIdentity{Kind: deploy.SourceImage})
	narrowed, key, err := b.s.Auth.CreateDeployToken(t.Context(), b.admin, "mine-ci", time.Hour, []int64{mine})
	if err != nil {
		t.Fatal(err)
	}
	open := func(w *httptest.ResponseRecorder) draftRef {
		t.Helper()
		if w.Code != http.StatusCreated {
			t.Fatalf("opening a draft = %d %s", w.Code, w.Body.String())
		}
		var ref draftRef
		decodeDeployResponse(t, w.Body.Bytes(), &ref)
		return ref
	}
	own := open(b.call(key, http.MethodPost, "/api/v1/deploy/drafts", nil))
	if own.KeyID != narrowed.ID {
		t.Fatalf("the key's draft records key %d, want %d", own.KeyID, narrowed.ID)
	}
	session := open(b.inSession(http.MethodPost, "/api/v1/deploy/drafts", nil))
	every := open(b.call(b.deploy, http.MethodPost, "/api/v1/deploy/drafts", nil))
	if session.KeyID != 0 || every.KeyID != b.deployID {
		t.Fatalf("a session's draft records key %d and the other key's %d", session.KeyID, every.KeyID)
	}

	// Each lists its own; a person sees their account's, keys' included.
	list := b.call(key, http.MethodGet, "/api/v1/deploy/drafts", nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), own.ID) ||
		strings.Contains(list.Body.String(), session.ID) || strings.Contains(list.Body.String(), every.ID) {
		t.Errorf("the narrowed key's list = %d %s", list.Code, list.Body.String())
	}
	if list := b.inSession(http.MethodGet, "/api/v1/deploy/drafts", nil); !strings.Contains(list.Body.String(), own.ID) || !strings.Contains(list.Body.String(), session.ID) {
		t.Errorf("the session's list = %d %s", list.Code, list.Body.String())
	}
	for _, foreign := range []draftRef{session, every} {
		path := "/api/v1/deploy/drafts/" + foreign.ID
		for name, request := range map[string][3]any{
			"reading":      {http.MethodGet, path, nil},
			"saving":       {http.MethodPut, path, map[string]any{"revision": foreign.Revision, "step": "intent", "intent": map[string]any{"name": "taken-over", "profile": "web"}}},
			"detecting":    {http.MethodPost, path + "/detect", map[string]any{"revision": foreign.Revision}},
			"preflighting": {http.MethodPost, path + "/preflight", map[string]any{"revision": foreign.Revision}},
			"committing":   {http.MethodPost, path + "/commit", map[string]any{"revision": foreign.Revision}},
			"discarding":   {http.MethodDelete, path, nil},
		} {
			w := b.call(key, request[0].(string), request[1].(string), request[2])
			if w.Code != http.StatusForbidden || keyErrorCode(w) != "draft_forbidden" {
				t.Errorf("%s another's draft (key %d) = %d %s, want 403 draft_forbidden", name, foreign.KeyID, w.Code, w.Body.String())
			}
		}
	}

	// The narrowed key commits its own draft and reaches the deployment it
	// made from the next request on, through the one grant row.
	seedDraftReadyToCommit(t, b.s, own.ID, "made-by-key")
	committed := b.call(key, http.MethodPost, "/api/v1/deploy/drafts/"+own.ID+"/commit", map[string]any{"revision": own.Revision})
	if committed.Code != http.StatusCreated {
		t.Fatalf("the narrowed key's commit = %d %s", committed.Code, committed.Body.String())
	}
	var result deploy.DraftCommitResult
	decodeDeployResponse(t, committed.Body.Bytes(), &result)
	var granted int
	if err := b.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM api_token_deployments WHERE token_id = ? AND project_id = ?`, narrowed.ID, result.ProjectID).Scan(&granted); err != nil || granted != 1 {
		t.Fatalf("grant rows for the made deployment = %d (%v)", granted, err)
	}
	if w := b.call(key, http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d", result.ProjectID), nil); w.Code != http.StatusOK {
		t.Errorf("the key reading what it made = %d %s", w.Code, w.Body.String())
	}
	if w := b.call(key, http.MethodGet, "/api/v1/deploy/?view=fleet", nil); !strings.Contains(w.Body.String(), `"made-by-key"`) || strings.Contains(w.Body.String(), `"theirs"`) {
		t.Errorf("the key's fleet after the commit = %s", w.Body.String())
	}
	if w := b.call(key, http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d", theirs), nil); keyErrorCode(w) != "key_scope" {
		t.Errorf("another deployment after the commit = %d %s", w.Code, w.Body.String())
	}
	if row := b.lastAudit("deploy.create"); row.Actor != "token" || row.KeyID != narrowed.ID || row.KeyName != "mine-ci" ||
		!strings.Contains(row.Detail, `"grantedToKey":true`) || row.Target != fmt.Sprint(result.ProjectID) {
		t.Errorf("the commit's audit row = %+v", row)
	}

	// A key reaching every deployment keeps no rows.
	seedDraftReadyToCommit(t, b.s, every.ID, "made-by-every")
	if w := b.call(b.deploy, http.MethodPost, "/api/v1/deploy/drafts/"+every.ID+"/commit", map[string]any{"revision": every.Revision}); w.Code != http.StatusCreated {
		t.Fatalf("the every-deployment key's commit = %d %s", w.Code, w.Body.String())
	}
	var rows int
	if err := b.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM api_token_deployments WHERE token_id = ?`, b.deployID).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("an every-deployment key was given %d rows (%v)", rows, err)
	}
	if row := b.lastAudit("deploy.create"); row.KeyID != b.deployID || strings.Contains(row.Detail, "grantedToKey") {
		t.Errorf("the every-deployment key's commit audit = %+v", row)
	}
	if w := b.call(b.deploy, http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d", theirs), nil); w.Code != http.StatusOK {
		t.Errorf("the every-deployment key after its commit = %d %s", w.Code, w.Body.String())
	}

	// Duplicating a deployment the narrowed key reaches leaves a draft that
	// is its own; another's is refused at the gate.
	copied := open(b.call(key, http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/duplicate", mine), map[string]any{"name": "mine-copy"}))
	if copied.KeyID != narrowed.ID {
		t.Errorf("the duplicate records key %d, want %d", copied.KeyID, narrowed.ID)
	}
	if w := b.call(key, http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/duplicate", theirs), map[string]any{"name": "theirs-copy"}); keyErrorCode(w) != "key_scope" {
		t.Errorf("duplicating another's = %d %s", w.Code, w.Body.String())
	}
	if w := b.call(key, http.MethodGet, "/api/v1/deploy/drafts/"+copied.ID, nil); w.Code != http.StatusOK {
		t.Errorf("reading its duplicate = %d %s", w.Code, w.Body.String())
	}
	if w := b.call(key, http.MethodDelete, "/api/v1/deploy/drafts/"+copied.ID, nil); w.Code != http.StatusNoContent {
		t.Errorf("discarding its duplicate = %d %s", w.Code, w.Body.String())
	}
	if row := b.lastAudit("deploy.draft.discard"); row.KeyID != narrowed.ID || row.Target != copied.ID {
		t.Errorf("the discard's audit row = %+v", row)
	}
	// An administrator in a session still reads any draft, a key's included.
	if w := b.inSession(http.MethodGet, "/api/v1/deploy/drafts/"+own.ID, nil); w.Code != http.StatusOK {
		t.Errorf("a session reading the key's draft = %d %s", w.Code, w.Body.String())
	}
}

// What a deploy key does beside deploying, on the deployments it reaches:
// promote staging's release, keep the new release or switch back to the warm
// one, turn maintenance on and off, and read the environments, the promotion
// preview, the window, the switch, a page and the rehearsal terms. A key
// narrowed to some deployments is refused each on any other, and every write
// is audited under the key.
func TestADeployKeyOperatesTheDeploymentsItReaches(t *testing.T) {
	b := newKeyBench(t)
	session := &client{t: t, h: b.h, cookie: b.session}
	owner := &warmOwner{}
	b.s.modules.deployExecutor = deploy.NewNormalizedStepExecutor(b.s.modules.deployRuns, b.s.modules.deployPlanning,
		b.s.modules.deploySources, b.s.modules.deployArtifacts, owner, nil, nil, t.TempDir())

	// The maintenance switch and the reads beside it, on a route the proxy
	// can switch.
	switched, switchedEnv, _ := insertDeploymentConfigurationAPI(t, b.s)
	root := t.TempDir()
	for _, dir := range []string{"sites-available", "sites-enabled"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	b.s.modules.proxy = proxysvc.New(root, filepath.Join(root, "Caddyfile"))
	name := deploy.RouteNameFor(switchedEnv)
	flag := filepath.Join(root, "jd-pages", name, "maintenance.on")
	if err := os.WriteFile(filepath.Join(root, "sites-available", name),
		[]byte("server {\n    if (-f "+flag+") {\n        set $jd_x_maint $jd_x_maint_ip;\n    }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", switched, switchedEnv)
	if w := b.call(b.deploy, http.MethodPut, base+"/maintenance", map[string]any{"on": true}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"on":true`) {
		t.Fatalf("switching maintenance on = %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(flag); err != nil {
		t.Fatalf("the switch file = %v", err)
	}
	if row := b.lastAudit("deploy.maintenance"); row.KeyID != b.deployID || row.KeyName != "github-actions" || !strings.Contains(row.Detail, `"on":true`) {
		t.Errorf("the switch's audit row = %+v", row)
	}
	if w := b.call(b.deploy, http.MethodPut, base+"/maintenance", map[string]any{"on": false}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"on":false`) {
		t.Errorf("switching maintenance off = %d %s", w.Code, w.Body.String())
	}
	for _, read := range []string{"/maintenance", "/pages/502", "/rehearsal", "/rollback-watch"} {
		if w := b.call(b.deploy, http.MethodGet, base+read, nil); w.Code != http.StatusOK {
			t.Errorf("GET %s = %d %s", read, w.Code, w.Body.String())
		}
	}
	if w := b.call(b.deploy, http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/environments", switched), nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"production"`) {
		t.Errorf("the environments = %d %s", w.Code, w.Body.String())
	}
	// The terms stay a session's.
	for _, write := range [][2]string{{"/pages/502", `{"content":"<h1>x</h1>"}`}, {"/rollback-policy", `{"keepWarm":true}`}, {"/rehearsal", `{"enabled":true}`}} {
		if w := b.call(b.deploy, http.MethodPut, base+write[0], write[1]); keyErrorCode(w) != "session_required" {
			t.Errorf("PUT %s = %d %s, want session_required", write[0], w.Code, w.Body.String())
		}
	}

	// Keeping the new release ends its window and retires the warm one.
	keepProject, keepEnv, first, _ := warmWindowFixture(t, b.s, session, "keep", true)
	kept := b.call(b.deploy, http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/rollback-watch/keep", keepProject, keepEnv), nil)
	if kept.Code != http.StatusOK || !strings.Contains(kept.Body.String(), `"endReason":"kept"`) {
		t.Fatalf("keep = %d %s", kept.Code, kept.Body.String())
	}
	if row := b.lastAudit("deploy.release.keep"); row.Actor != "token" || row.KeyID != b.deployID || row.KeyName != "github-actions" ||
		!strings.Contains(row.Detail, fmt.Sprintf(`"warmReleaseId":%d`, first.Release.ID)) {
		t.Errorf("keep's audit row = %+v", row)
	}
	if len(owner.stopped) != 1 {
		t.Errorf("stopped = %v", owner.stopped)
	}

	// A second window, opened before the promotion below queues a run the
	// fixture's own claim would otherwise take.
	warmProject, warmEnv, _, _ := warmWindowFixture(t, b.s, session, "warm", true)

	// Promotion: staging's live release into production, by a key narrowed
	// to this deployment alone.
	promo := createLegacyDeploymentFixture(t, b.s, "promo")
	production := convertToNormalizedDeploymentFixture(t, b.s, promo)
	images := &fakePromotionImages{held: map[string]bool{}}
	b.s.modules.promotionImages = images
	added := session.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/environments", promo.ID), `{"kind":"staging"}`, nil)
	if added.Code != http.StatusCreated {
		t.Fatalf("add staging = %d %s", added.Code, added.Body.String())
	}
	var copied deploy.StagingCopy
	decodeDeployResponse(t, added.Body.Bytes(), &copied)
	queued := session.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/runs", promo.ID, copied.Environment.ID), `{"operation":"deploy"}`, nil)
	if queued.Code != http.StatusAccepted {
		t.Fatalf("staging deploy = %d %s", queued.Code, queued.Body.String())
	}
	var stagingRun deploy.EngineRun
	decodeDeployResponse(t, queued.Body.Bytes(), &stagingRun)
	release := activateSyntheticNormalizedRelease(t, b.s, stagingRun, "staging")
	images.held["sha256:"+strings.Repeat("c", 64)] = true
	narrowed, key, err := b.s.Auth.CreateDeployToken(t.Context(), b.admin, "promo-ci", time.Hour, []int64{promo.ID})
	if err != nil {
		t.Fatal(err)
	}
	if w := b.call(key, http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/environments", promo.ID), nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"staging"`) {
		t.Errorf("the narrowed key's environments = %d %s", w.Code, w.Body.String())
	}
	preview := b.call(key, http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/promotion?releaseId=%d", promo.ID, production, release.Release.ID), nil)
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), `"imagePresent":true`) {
		t.Errorf("the narrowed key's promotion preview = %d %s", preview.Code, preview.Body.String())
	}
	promoted := b.call(key, http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/promote", promo.ID, production), map[string]any{"releaseId": release.Release.ID})
	if promoted.Code != http.StatusAccepted {
		t.Fatalf("the narrowed key's promote = %d %s", promoted.Code, promoted.Body.String())
	}
	var promotion deploy.EngineRun
	decodeDeployResponse(t, promoted.Body.Bytes(), &promotion)
	if promotion.Operation != deploy.OperationPromote || promotion.EnvironmentID != production {
		t.Errorf("the promotion run = %+v", promotion)
	}
	if row := b.lastAudit("deploy.release.promote"); row.Actor != "token" || row.KeyID != narrowed.ID || row.KeyName != "promo-ci" ||
		!strings.Contains(row.Detail, fmt.Sprintf(`"runId":%d`, promotion.ID)) {
		t.Errorf("the promotion's audit row = %+v", row)
	}

	// Switching back to the warm release is the key's, and audited under it.
	watch := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/rollback-watch", warmProject, warmEnv)
	if w := b.call(b.deploy, http.MethodGet, watch, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"watching"`) {
		t.Errorf("reading the window = %d %s", w.Code, w.Body.String())
	}
	rolled := b.call(b.deploy, http.MethodPost, watch+"/rollback", nil)
	if rolled.Code != http.StatusAccepted || !strings.Contains(rolled.Body.String(), `"operation":"rollback"`) {
		t.Fatalf("instant rollback = %d %s", rolled.Code, rolled.Body.String())
	}
	if row := b.lastAudit("deploy.release.instant_rollback"); row.KeyID != b.deployID || row.KeyName != "github-actions" {
		t.Errorf("the instant rollback's audit row = %+v", row)
	}

	// The narrowed key reaches none of this on a deployment it was not given.
	other := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", warmProject, warmEnv)
	for name, request := range map[string][3]any{
		"another's environments":      {http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/environments", warmProject), nil},
		"another's promotion preview": {http.MethodGet, other + "/promotion?releaseId=1", nil},
		"promoting another's":         {http.MethodPost, other + "/promote", map[string]any{"releaseId": 1}},
		"another's window":            {http.MethodGet, other + "/rollback-watch", nil},
		"rolling another's back":      {http.MethodPost, other + "/rollback-watch/rollback", nil},
		"keeping another's":           {http.MethodPost, other + "/rollback-watch/keep", nil},
		"another's switch":            {http.MethodGet, base + "/maintenance", nil},
		"switching another's":         {http.MethodPut, base + "/maintenance", map[string]any{"on": true}},
		"another's page":              {http.MethodGet, base + "/pages/502", nil},
		"another's rehearsal terms":   {http.MethodGet, base + "/rehearsal", nil},
	} {
		w := b.call(key, request[0].(string), request[1].(string), request[2])
		if w.Code != http.StatusForbidden || keyErrorCode(w) != "key_scope" {
			t.Errorf("%s = %d %s, want 403 key_scope", name, w.Code, w.Body.String())
		}
	}
	if _, err := os.Stat(flag); !os.IsNotExist(err) {
		t.Error("a refused switch was written")
	}
}
