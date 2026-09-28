package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

func TestGitWorkspaceRoutesEnforceCapabilitiesAndContainment(t *testing.T) {
	c, s, _, repo := gitFixture(t)
	reader := &client{t: t, h: c.h, cookie: signInAs(t, s, "workspace-reader", auth.RoleReadOnly)}
	for _, route := range []struct {
		path  string
		query []string
	}{
		{"reflog", nil}, {"worktrees", nil}, {"blame", []string{"file", "a.txt"}}, {"patch", []string{"file", "a.txt"}},
		{"signature", []string{"ref", "HEAD"}}, {"compare/diff", []string{"base", "HEAD", "head", "HEAD"}},
		{"submodules", nil}, {"lfs", nil}, {"patch/export", []string{"mode", "staged"}}, {"forge/", nil},
	} {
		w := reader.do(http.MethodGet, gitPath("/api/v1/git/"+route.path, repo, route.query...), "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", route.path, w.Code, w.Body.String())
		}
		w = reader.do(http.MethodGet, gitPath("/api/v1/git/"+route.path, "/outside", route.query...), "", nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s outside roots = %d", route.path, w.Code)
		}
	}
	for _, route := range []string{"recover", "remote/update", "upstream", "worktree", "worktree/remove", "operation/start", "operation/continue", "operation/abort", "conflict/resolve", "conflict/choose", "patch/stage", "rebase", "submodule", "submodule/remove", "lfs", "patch/import", "github/pulls/7/review", "forge/account", "forge/disconnect", "forge/requests", "forge/requests/7/action"} {
		w := reader.do(http.MethodPost, gitPath("/api/v1/git/"+route, repo), `{}`, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("reader wrote %s: %d", route, w.Code)
		}
	}
	limited := &client{t: t, h: c.h, cookie: signInAs(t, s, "workspace-limited", auth.RoleLimited)}
	for _, route := range []string{"forge/account", "forge/disconnect", "submodule/remove"} {
		w := limited.do(http.MethodPost, gitPath("/api/v1/git/"+route, repo), `{}`, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("limited wrote %s: %d", route, w.Code)
		}
	}
	w := limited.do(http.MethodPost, gitPath("/api/v1/git/worktree/remove", repo), `{}`, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("limited removed worktree: %d", w.Code)
	}
	w = limited.do(http.MethodPost, gitPath("/api/v1/git/recover", repo), `{"name":"rescued","ref":"HEAD"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("recover: %d %s", w.Code, w.Body.String())
	}
	for _, route := range []string{"operation/abort", "conflict/choose"} {
		w := limited.do(http.MethodPost, gitPath("/api/v1/git/"+route, repo), `{"choice":"theirs"}`, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("limited wrote %s: %d", route, w.Code)
		}
		w = c.do(http.MethodPost, gitPath("/api/v1/git/"+route, repo), `{"choice":"theirs"}`, nil)
		if !strings.Contains(w.Body.String(), "confirmation") {
			t.Fatalf("%s did not require its phrase: %d %s", route, w.Code, w.Body.String())
		}
	}
}

func TestGitWorkspaceRoutesAcceptAmendAndValidateCloneOptions(t *testing.T) {
	c, _, root, repo := gitFixture(t)
	w := c.do(http.MethodPost, gitPath("/api/v1/git/commit", repo), `{"message":"corrected message","amend":true}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("message-only amend: %d %s", w.Code, w.Body.String())
	}
	w = c.do(http.MethodGet, gitPath("/api/v1/git/log", repo, "search", "corrected", "author", "t@e"), "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "corrected message") {
		t.Fatalf("history: %d %s", w.Code, w.Body.String())
	}
	for _, options := range []map[string]any{
		{"depth": -1}, {"depth": 100001}, {"branch": "--upload-pack=oops"}, {"sparse": []string{"../outside"}},
	} {
		options["url"], options["parent"], options["name"] = "https://example.invalid/repo", root, "new"
		body, _ := json.Marshal(options)
		w := c.do(http.MethodPost, "/api/v1/git/clone", string(body), nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("clone accepted %s: %d %s", body, w.Code, w.Body.String())
		}
	}
}
