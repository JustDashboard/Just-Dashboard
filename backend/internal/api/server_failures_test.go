package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
)

// On a server without nginx, each of these said it its own way: a 400 from
// the settings, a 404 from the file tree, a 409 from the resolver and the
// stream plan, a 502 from the dump and the linter, a 503 no_nginx from the
// upstream check and a 503 not_installed from the module report. A script
// could not tell "this server has no nginx" from "your request was wrong" or
// "the dashboard broke". Now it is one answer.
func TestARouteThatNeedsNginxSaysSoOnAServerWithoutIt(t *testing.T) {
	dir := t.TempDir()
	// A PATH with nothing on it: no nginx to find, and nothing else a handler
	// could run on this machine either.
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	s := testServer(t)
	s.Cfg.NginxDir = filepath.Join(dir, "nginx")
	s.initModules()
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	for _, path := range []string{
		"/proxy/settings",
		"/proxy/files",
		"/proxy/effective",
		"/proxy/lint",
		"/proxy/resolve?url=http://app.example.com/",
		"/proxy/resolve/access?url=http://app.example.com/&source=192.0.2.9",
		"/proxy/upstreams",
		"/proxy/modules/",
		"/proxy/streams/include/plan",
	} {
		w := admin.do(http.MethodGet, "/api/v1"+path, "", nil)
		var body struct {
			Error httpx.APIError `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Errorf("%s: %d %s", path, w.Code, w.Body.String())
			continue
		}
		if w.Code != http.StatusServiceUnavailable || body.Error.Code != "not_installed" || body.Error.Resource != "nginx" ||
			body.Error.Message != "nginx is not installed on this server." {
			t.Errorf("%s = %d %s, want 503 not_installed for nginx", path, w.Code, strings.TrimSpace(w.Body.String()))
		}
	}
}

// A route's own answer stands on a server that has nginx: the mapping is for
// its absence, not for every failure under it.
func TestARouteThatNeedsNginxKeepsItsOwnAnswerWhereNginxIs(t *testing.T) {
	dir := t.TempDir()
	nginxDumping(t, dir, "server {\n    listen 80;\n    server_name app.example.com;\n}\n")
	refusal := httpx.Err(http.StatusConflict, "config_unreadable", "nginx could not print its configuration")
	if got := withoutNginx(refusal); got != error(refusal) {
		t.Errorf("with nginx on the server the answer became %v", got)
	}
}

// systemctl on a machine whose init is not systemd is this server lacking
// systemd, as a missing systemctl is; a unit that failed to start is still a
// command that failed, and a command that ran out of time is a timeout.
func TestSystemdThatIsNotRunningIsAbsentNotACommandThatFailed(t *testing.T) {
	notBooted := errors.New("systemctl exited 1: System has not been booted with systemd as init system (PID 1). Can't operate.")
	for name, tt := range map[string]struct {
		err    error
		status int
		code   string
	}{
		"no systemd":     {notBooted, http.StatusServiceUnavailable, "not_installed"},
		"no systemctl":   {fmt.Errorf("systemctl %w", procs.ErrNotInstalled), http.StatusServiceUnavailable, "not_installed"},
		"a failed start": {errors.New("systemctl exited 1: Job for app.service failed"), http.StatusBadGateway, "command_failed"},
	} {
		var api *httpx.APIError
		if !errors.As(mapProcsError(tt.err), &api) || api.Status != tt.status || api.Code != tt.code {
			t.Errorf("%s: %+v, want %d %s", name, api, tt.status, tt.code)
		}
	}
	var api *httpx.APIError
	if errors.As(mapProcsError(notBooted), &api); api.Resource != "systemd" || api.Message != "systemd is not running on this server." {
		t.Errorf("no systemd said %+v", api)
	}
}

// gh without an account is a state of the server with a code of its own, the
// one a deployment's pull requests already answer with. A sign-in that gh
// turned down names the same command and is the request's refusal.
func TestGitHubWithoutAnAccountIsNotABadRequest(t *testing.T) {
	signedOut := errors.New("could not list your GitHub repositories: To get started with GitHub CLI, please run:  gh auth login")
	var api *httpx.APIError
	if !errors.As(ghError(signedOut), &api) || api.Status != http.StatusConflict || api.Code != "github_login_required" {
		t.Errorf("signed out = %+v, want 409 github_login_required", api)
	}
	if !strings.Contains(api.Raw, "gh auth login") || strings.Contains(api.Message, "gh auth login") {
		t.Errorf("gh's words belong in raw, not in the sentence: %+v", api)
	}
	if !errors.As(ghError(errors.New("gh auth login: error validating token: HTTP 401: Bad credentials")), &api) || api.Status != http.StatusBadRequest {
		t.Errorf("a refused token = %+v, want the request's refusal", api)
	}
}

// httpx.BadRequest("%v", err) was the last branch of eleven mappers and the
// answer at some 250 other places, so a deadline, a missing program or a full
// disk under any of them was "your request was wrong", never logged. Those
// places call httpx.Refused now, which knows the difference. This keeps the
// old spelling from coming back.
func TestNoHandlerCallsAServerFailureABadRequest(t *testing.T) {
	// An error passed whole to BadRequest: the %v or %s of a value named as
	// an error is, or of anything's Error().
	flat := regexp.MustCompile(`httpx\.BadRequest\("%[vs]", (?:(?:\w*[eE]rr|e)\)|\w+\.Error\(\)\))`)
	// The session and assistant handlers belong to the security work and still
	// spell it the old way; a name leaves this list when its file is moved.
	notYet := map[string]bool{"handlers_auth.go": true, "handlers_assistant_turn.go": true}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		found := flat.FindAllString(string(src), -1)
		switch {
		case notYet[name] && len(found) == 0:
			t.Errorf("%s no longer flattens an error into a 400: take it off the list", name)
		case !notYet[name] && len(found) > 0:
			t.Errorf("%s: %s: answer an error nobody named with httpx.Refused(err), which does not blame the request for a deadline, a missing program or a full disk", name, found[0])
		}
	}
}
