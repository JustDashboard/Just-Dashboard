package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// A deploy whose branch cannot be read says why, from git's own answer, and
// never repeats that answer: the fake remote below echoes a credential into
// stderr, the way a misconfigured helper or proxy can.
func TestDeploymentRunCreateNamesWhyTheSourceCouldNotBeRead(t *testing.T) {
	s := testServer(t)
	projectID, environmentID := insertRefDeployFixture(t, s, "source-cause-app", gitSourceFixture(), deploy.BuildDockerfile)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "source-cause-admin", auth.RoleAdmin)}
	deployBody, _ := json.Marshal(map[string]any{"operation": "deploy"})

	for _, test := range []struct {
		stderr string
		exit   int
		status int
		code   string
	}{
		{"fatal: Authentication failed for 'https://x-token:ghp_leakedtoken@github.com/acme/ref-app.git/'", 128, http.StatusBadGateway, "source_auth_failed"},
		{"remote: Repository not found.", 128, http.StatusBadGateway, "source_repository_missing"},
		{"fatal: unable to access 'https://github.com/acme/ref-app.git/': Could not resolve host: github.com", 128, http.StatusBadGateway, "source_unreachable"},
		// ls-remote --exit-code read the remote and the branch is gone.
		{"", 2, http.StatusBadRequest, "ref_not_found"},
		{"fatal: something else", 128, http.StatusBadGateway, "source_unavailable"},
	} {
		fakeGitOnPath(t, fmt.Sprintf("#!/bin/sh\necho %q >&2\nexit %d\n", test.stderr, test.exit))
		response := admin.do(http.MethodPost, runCreatePath(projectID, environmentID), string(deployBody), nil)
		if response.Code != test.status {
			t.Fatalf("%s: status %d %s", test.code, response.Code, response.Body.String())
		}
		got := decodedAPIError(t, response.Body.Bytes())
		if got.Code != test.code {
			t.Fatalf("code = %q, want %q (%s)", got.Code, test.code, got.Message)
		}
		if strings.Contains(response.Body.String(), "ghp_") || strings.Contains(response.Body.String(), "Authentication failed") {
			t.Fatalf("git's own output reached the response: %s", response.Body.String())
		}
	}
}
