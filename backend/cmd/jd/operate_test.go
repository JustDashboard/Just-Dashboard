package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// promote takes staging's live release unless --release names one, lands it
// in production, and with --wait follows the run like a deploy.
func TestPromotePicksStagingsLiveRelease(t *testing.T) {
	f := newFake(t)
	code, stdout, stderr := jd(t, invocation{env: f.env(t)}, "promote", "web", "--wait")
	if code != exitOK || !strings.Contains(stdout, "serving") || !strings.Contains(stderr, "Run #5 succeeded") {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "Promoting release 150 (staging's live release) to web (Production): run #7") {
		t.Errorf("the promotion was not named: %s", stderr)
	}
	promotes := f.bodiesFor("POST /api/v1/deploy/7/environments/70/promote")
	if len(promotes) != 1 || promotes[0]["releaseId"].(float64) != 150 {
		t.Errorf("promoted with %v", promotes)
	}
	code, stdout, _ = jd(t, invocation{env: f.env(t)}, "promote", "web", "--release", "149")
	if code != exitOK || !strings.Contains(stdout, "Queued run #7") {
		t.Errorf("--release: exit %d %s", code, stdout)
	}
	if promotes := f.bodiesFor("POST /api/v1/deploy/7/environments/70/promote"); promotes[1]["releaseId"].(float64) != 149 {
		t.Errorf("--release sent %v", promotes[1])
	}
	// A deployment without staging has nothing to promote from.
	code, _, stderr = jd(t, invocation{env: f.env(t)}, "promote", "api")
	if code != exitRefused || !strings.Contains(stderr, "no staging environment") {
		t.Errorf("without staging: exit %d %s", code, stderr)
	}
	if len(f.bodiesFor("POST /api/v1/deploy/8/environments/80/promote")) != 0 {
		t.Error("a promotion was sent for a deployment without staging")
	}
}

// --from-preview lands a pull request's preview in staging, found by its
// kind, with the server's words when the preview may not go.
func TestPromoteFromPreviewLandsInStaging(t *testing.T) {
	f := newFake(t)
	code, stdout, stderr := jd(t, invocation{env: f.env(t)}, "promote", "web", "--from-preview", "42", "--wait")
	if code != exitOK || !strings.Contains(stderr, "Run #5 succeeded") {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "Promoting PR #42's preview to web (Staging): run #9") {
		t.Errorf("the promotion was not named: %s", stderr)
	}
	promotes := f.bodiesFor("POST /api/v1/deploy/7/environments/71/promote")
	if len(promotes) != 1 || promotes[0]["source"] != "preview" || promotes[0]["preview"].(float64) != 42 {
		t.Errorf("promoted with %v", promotes)
	}
	if _, named := promotes[0]["releaseId"]; named {
		t.Errorf("a preview's promotion named a release: %v", promotes[0])
	}
	if len(f.bodiesFor("POST /api/v1/deploy/7/environments/70/promote")) != 0 {
		t.Error("a preview was sent to production")
	}
	code, stdout, _ = jd(t, invocation{env: f.env(t)}, "promote", "web", "--from-preview", "42")
	if code != exitOK || !strings.Contains(stdout, "Queued run #9 for web (Staging)") {
		t.Errorf("without --wait: exit %d %s", code, stdout)
	}
	// The server's refusal reaches the operator in its own words.
	code, _, stderr = jd(t, invocation{env: f.env(t)}, "promote", "web", "--from-preview", "46")
	if code != exitRefused || !strings.Contains(stderr, "comes from a fork (octocat/web)") {
		t.Errorf("an unapproved fork: exit %d %s", code, stderr)
	}
	if code, _, stderr := jd(t, invocation{env: f.env(t)}, "promote", "web", "--from-preview", "42", "--release", "150"); code != exitUsage || !strings.Contains(stderr, "--from-preview") {
		t.Errorf("--from-preview with --release: exit %d %s", code, stderr)
	}
	code, _, stderr = jd(t, invocation{env: f.env(t)}, "promote", "api", "--from-preview", "42")
	if code != exitRefused || !strings.Contains(stderr, "no staging environment to promote PR #42's preview into") {
		t.Errorf("without staging: exit %d %s", code, stderr)
	}
}

// --instant uses the window's own route, keeps nothing of --to, and keep
// closes the window with the server's own account of it.
func TestInstantRollbackAndKeepUseTheWindowRoutes(t *testing.T) {
	f := newFake(t)
	code, stdout, stderr := jd(t, invocation{env: f.env(t)}, "rollback", "web", "--instant")
	if code != exitOK || !strings.Contains(stdout, "Queued run #8") {
		t.Fatalf("exit %d %s %s", code, stdout, stderr)
	}
	if len(f.bodiesFor("POST /api/v1/deploy/7/environments/70/rollback-watch/rollback")) != 1 ||
		len(f.bodiesFor("POST /api/v1/deploy/7/environments/70/rollback")) != 0 {
		t.Errorf("requests %v", f.requests)
	}
	if code, _, stderr := jd(t, invocation{env: f.env(t)}, "rollback", "web", "--instant", "--to", "100"); code != exitUsage || !strings.Contains(stderr, "--to") {
		t.Errorf("--instant with --to: exit %d %s", code, stderr)
	}
	code, _, stderr = jd(t, invocation{env: f.env(t)}, "rollback", "web", "--instant", "--wait")
	if code != exitOK || !strings.Contains(stderr, "Switching back to the warm release of web (Production): run #8") {
		t.Errorf("--instant --wait: exit %d %s", code, stderr)
	}
	f.refuse["POST /api/v1/deploy/7/environments/70/rollback-watch/rollback"] = &apiError{Status: 409, Code: "no_warm_release",
		Message: "No release is kept warm for this environment, so there is nothing to switch back to instantly."}
	if code, _, stderr := jd(t, invocation{env: f.env(t)}, "rollback", "web", "--instant"); code != exitRefused || !strings.Contains(stderr, "nothing to switch back to") {
		t.Errorf("nothing warm: exit %d %s", code, stderr)
	}

	code, stdout, _ = jd(t, invocation{env: f.env(t)}, "keep", "web")
	if code != exitOK || !strings.Contains(stdout, "kept release #3 of web (Production); the warm release #2 was retired") {
		t.Errorf("keep: exit %d %s", code, stdout)
	}
	code, stdout, _ = jd(t, invocation{env: f.env(t)}, "keep", "web", "--json")
	if code != exitOK || !strings.Contains(stdout, `"endReason":"kept"`) {
		t.Errorf("keep --json: exit %d %s", code, stdout)
	}
}

// The switch acts at once; who is let past is a configuration change,
// written first and applied by the next deploy, which the hint says.
func TestMaintenanceSwitchesAtOnceAndBypassGoesToTheConfiguration(t *testing.T) {
	f := newFake(t)
	code, stdout, stderr := jd(t, invocation{env: f.env(t)}, "maintenance", "web", "on")
	if code != exitOK || !strings.Contains(stdout, "maintenance on for web (Production)") || !f.maintenanceOn {
		t.Fatalf("on: exit %d %s %s", code, stdout, stderr)
	}
	if len(f.bodiesFor("PUT /api/v1/deploy/7/environments/70/configuration")) != 0 {
		t.Error("switching on wrote the configuration")
	}
	if code, _, _ := jd(t, invocation{env: f.env(t)}, "maintenance", "web", "sideways"); code != exitUsage {
		t.Errorf("a position that is not on or off: exit %d", code)
	}

	code, stdout, stderr = jd(t, invocation{env: f.env(t)}, "maintenance", "web", "off", "--bypass", "10.0.0.0/8,192.168.1.5")
	if code != exitOK || !strings.Contains(stdout, "maintenance off for web (Production)") || f.maintenanceOn {
		t.Fatalf("off --bypass: exit %d %s %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "next deploy") {
		t.Errorf("no hint that the bypass list waits for the next deploy: %s", stderr)
	}
	saved := f.bodiesFor("PUT /api/v1/deploy/7/environments/70/configuration")
	if len(saved) != 1 {
		t.Fatalf("configuration writes = %v", saved)
	}
	runtime := saved[0]["runtime"].(map[string]any)
	maintenance := runtime["maintenance"].(map[string]any)
	if bypass, _ := json.Marshal(maintenance["bypassFrom"]); string(bypass) != `["10.0.0.0/8","192.168.1.5"]` || runtime["image"] != "nginx:1.27-alpine" ||
		saved[0]["revision"].(float64) != 3 || saved[0]["variables"] != nil {
		t.Errorf("the configuration was written as %v", saved[0])
	}
	// An empty list clears it.
	if code, _, _ := jd(t, invocation{env: f.env(t)}, "maintenance", "web", "off", "--bypass", ""); code != exitOK {
		t.Fatalf("--bypass '': exit %d", code)
	}
	saved = f.bodiesFor("PUT /api/v1/deploy/7/environments/70/configuration")
	if cleared := saved[1]["runtime"].(map[string]any)["maintenance"].(map[string]any); cleared["bypassFrom"] != nil {
		t.Errorf("the list was not cleared: %v", cleared)
	}
	f.refuse["PUT /api/v1/deploy/7/environments/70/maintenance"] = &apiError{Status: 409, Code: "no_public_route",
		Message: "This environment has no public route yet: add a domain and deploy, and maintenance can be switched from then on."}
	if code, _, stderr := jd(t, invocation{env: f.env(t)}, "maintenance", "web", "on"); code != exitRefused || !strings.Contains(stderr, "no public route yet") {
		t.Errorf("no route: exit %d %s", code, stderr)
	}
}

// status reads every piece and shows the rest when one is refused; --json
// is one object of the server's answers, a refusal as its envelope.
func TestStatusReadsEveryPieceAndJSONIsOneObject(t *testing.T) {
	f := newFake(t)
	code, stdout, stderr := jd(t, invocation{env: f.env(t)}, "status", "web")
	if code != exitOK {
		t.Fatalf("exit %d %s %s", code, stdout, stderr)
	}
	for _, want := range []string{
		"deployment      web (Production, id 7)",
		"health          healthy",
		"last run        #4 succeeded",
		"environments    production (live release 102), staging (live release 150)",
		"live release    #3 (id 102)",
		"rollback watch  watching release #3, warm #2 running for its visitors (41 requests), until",
		"(keep warm 600s, automatic)",
		"maintenance     off (caddy)",
		"rehearsal       off",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status lacks %q:\n%s", want, stdout)
		}
	}
	f.refuse["GET /api/v1/deploy/7/environments/70/maintenance"] = &apiError{Status: 503, Code: "no_proxy", Message: "the proxy is unavailable on this host"}
	code, stdout, _ = jd(t, invocation{env: f.env(t)}, "status", "web")
	if code != exitOK || !strings.Contains(stdout, "maintenance     unavailable: no_proxy (503): the proxy is unavailable on this host") {
		t.Errorf("with the proxy refusing: exit %d\n%s", code, stdout)
	}
	code, stdout, _ = jd(t, invocation{env: f.env(t)}, "status", "web", "--json")
	if code != exitOK {
		t.Fatalf("--json: exit %d %s", code, stdout)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &object); err != nil {
		t.Fatalf("--json is not one object: %v\n%s", err, stdout)
	}
	for _, key := range []string{"deployment", "environments", "liveRelease", "rollbackWatch", "maintenance", "rehearsal"} {
		if _, ok := object[key]; !ok {
			t.Errorf("--json lacks %s: %s", key, stdout)
		}
	}
	if !strings.Contains(string(object["deployment"]), `"name":"web"`) || !strings.Contains(string(object["liveRelease"]), `"id":102`) ||
		!strings.Contains(string(object["maintenance"]), `"code":"no_proxy"`) || !strings.Contains(string(object["rollbackWatch"]), `"watching"`) {
		t.Errorf("--json pieces: %s", stdout)
	}
	if strings.Count(stdout, "\n") != 1 {
		t.Errorf("--json is more than one line:\n%s", stdout)
	}
}
