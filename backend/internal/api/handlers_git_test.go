package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gitx"
)

// gitFixture is a server whose git roots hold one real repository with a
// commit, so the routes can be driven end to end rather than against a mock
// that would pass while the argv was wrong.
func gitFixture(t *testing.T) (*client, *Server, string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	c, s := newClient(t)
	root := t.TempDir()
	repo := filepath.Join(root, "project")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.name", "t")
	git("config", "user.email", "t@e")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-qm", "first")
	s.modules.git = gitx.New([]string{root})
	return c, s, root, repo
}

func gitPath(base, repo string, extra ...string) string {
	q := url.Values{"path": {repo}}
	for i := 0; i+1 < len(extra); i += 2 {
		q.Set(extra[i], extra[i+1])
	}
	return base + "?" + q.Encode()
}

// Every read route answers for a repository inside the roots.
func TestGitReadRoutesAnswer(t *testing.T) {
	c, _, _, repo := gitFixture(t)
	if err := os.WriteFile(filepath.Join(repo, "café.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/v1/git/status", "/api/v1/git/log", "/api/v1/git/branches", "/api/v1/git/tags",
		"/api/v1/git/stashes", "/api/v1/git/remotes", "/api/v1/git/graph", "/api/v1/git/diff",
	} {
		t.Run(path, func(t *testing.T) {
			w := c.do(http.MethodGet, gitPath(path, repo), "", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("got %d: %s", w.Code, strings.TrimSpace(w.Body.String()))
			}
		})
	}
	w := c.do(http.MethodGet, gitPath("/api/v1/git/status", repo), "", nil)
	var st gitx.Status
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Files) != 1 || st.Files[0].Path != "café.txt" || st.Identity.Name != "t" {
		t.Fatalf("status = %+v", st)
	}
	w = c.do(http.MethodGet, gitPath("/api/v1/git/commit", repo, "ref", "HEAD"), "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"changes"`) {
		t.Fatalf("commit detail = %d %s", w.Code, w.Body.String())
	}
	w = c.do(http.MethodGet, "/api/v1/git/roots", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"roots"`) {
		t.Fatalf("roots = %d %s", w.Code, w.Body.String())
	}
}

// The roots are the boundary: a real repository outside them is refused on
// every route, reads included.
func TestGitRoutesRefuseARepositoryOutsideTheRoots(t *testing.T) {
	c, _, _, _ := gitFixture(t)
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	w := c.do(http.MethodGet, gitPath("/api/v1/git/status", outside), "", nil)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "outside_roots") {
		t.Fatalf("status outside the roots = %d %s", w.Code, w.Body.String())
	}
	w = c.do(http.MethodPost, "/api/v1/git/init", `{"path":"`+outside+`"}`, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("init outside the roots = %d %s", w.Code, w.Body.String())
	}
	w = c.do(http.MethodPost, "/api/v1/git/clone",
		`{"url":"https://example.invalid/r.git","parent":"`+outside+`"}`, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("clone outside the roots = %d %s", w.Code, w.Body.String())
	}
}

// The three operations that overwrite uncommitted work take a typed phrase;
// the routine deletions do not — see invariant 3.
func TestGitDestructiveRoutesAndTheirPhrases(t *testing.T) {
	c, _, _, repo := gitFixture(t)
	typed := []struct{ path, body, phrase string }{
		{"/api/v1/git/discard", `{"file":"a.txt"}`, "discard changes"},
		{"/api/v1/git/reset", `{"ref":"HEAD","hard":true}`, "reset hard"},
		{"/api/v1/git/stash/drop", `{"index":0}`, "drop stash"},
	}
	for _, tc := range typed {
		w := c.do(http.MethodPost, gitPath(tc.path, repo), tc.body, nil)
		if !strings.Contains(w.Body.String(), "confirmation") {
			t.Errorf("%s ran without a phrase: %d %s", tc.path, w.Code, w.Body.String())
		}
		w = c.do(http.MethodPost, gitPath(tc.path, repo), tc.body, map[string]string{"X-Confirm": "wrong"})
		if !strings.Contains(w.Body.String(), "confirmation") {
			t.Errorf("%s accepted the wrong phrase: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	// A mixed reset moves the pointer and leaves the tree alone: no phrase.
	w := c.do(http.MethodPost, gitPath("/api/v1/git/reset", repo), `{"ref":"HEAD","hard":false}`, nil)
	if strings.Contains(w.Body.String(), "confirmation") {
		t.Errorf("a mixed reset asked for a phrase: %s", w.Body.String())
	}
	for _, tc := range []struct{ path, body string }{
		{"/api/v1/git/branch/delete", `{"ref":"nope"}`},
		{"/api/v1/git/tag/delete", `{"name":"nope"}`},
		{"/api/v1/git/remote/delete", `{"name":"nope"}`},
		{"/api/v1/git/branch/delete-remote", `{"remote":"origin","ref":"nope"}`},
	} {
		w := c.do(http.MethodPost, gitPath(tc.path, repo), tc.body, nil)
		if strings.Contains(w.Body.String(), "confirmation") {
			t.Errorf("%s asked for a phrase, but the commits survive: %s", tc.path, w.Body.String())
		}
	}
}

// A readonly account reads and does nothing else; limited controls but cannot
// destroy. The UI hiding a button is only an affordance.
func TestGitRoutesHonourCapabilities(t *testing.T) {
	c, s, _, repo := gitFixture(t)
	readonly := &client{t: t, h: c.h, cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	if w := readonly.do(http.MethodGet, gitPath("/api/v1/git/log", repo), "", nil); w.Code != http.StatusOK {
		t.Fatalf("readonly log = %d", w.Code)
	}
	for _, path := range []string{
		"/api/v1/git/fetch", "/api/v1/git/stage", "/api/v1/git/commit", "/api/v1/git/tag",
		"/api/v1/git/merge", "/api/v1/git/identity", "/api/v1/git/clone", "/api/v1/git/init",
	} {
		if w := readonly.do(http.MethodPost, gitPath(path, repo), `{}`, nil); w.Code != http.StatusForbidden {
			t.Errorf("readonly %s = %d, want 403", path, w.Code)
		}
	}
	limited := &client{t: t, h: c.h, cookie: signInAs(t, s, "limited", auth.RoleLimited)}
	for _, path := range []string{
		"/api/v1/git/discard", "/api/v1/git/reset", "/api/v1/git/stash/drop",
		"/api/v1/git/branch/delete", "/api/v1/git/tag/delete", "/api/v1/git/remote/delete",
	} {
		if w := limited.do(http.MethodPost, gitPath(path, repo), `{}`, nil); w.Code != http.StatusForbidden {
			t.Errorf("limited %s = %d, want 403", path, w.Code)
		}
	}
}

// The write routes run against the real repository: a tag, an identity, a
// remote and a stash round-trip through the API and show up in the reads.
func TestGitWriteRoutesRoundTrip(t *testing.T) {
	c, _, root, repo := gitFixture(t)
	w := c.do(http.MethodPost, gitPath("/api/v1/git/tag", repo), `{"name":"v1","message":"one"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("tag = %d %s", w.Code, w.Body.String())
	}
	w = c.do(http.MethodGet, gitPath("/api/v1/git/tags", repo), "", nil)
	if !strings.Contains(w.Body.String(), `"v1"`) {
		t.Fatalf("tags after create = %s", w.Body.String())
	}
	w = c.do(http.MethodPost, gitPath("/api/v1/git/identity", repo), `{"name":"Ada","email":"ada@example.com"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("identity = %d %s", w.Code, w.Body.String())
	}
	w = c.do(http.MethodPost, gitPath("/api/v1/git/remote", repo), `{"name":"origin","url":"https://example.invalid/r.git"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("remote add = %d %s", w.Code, w.Body.String())
	}
	w = c.do(http.MethodPost, gitPath("/api/v1/git/remote", repo), `{"name":"evil","url":"/etc"}`, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a local path as a remote = %d, want 400", w.Code)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w = c.do(http.MethodPost, gitPath("/api/v1/git/stash", repo), `{"message":"parked"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("stash = %d %s", w.Code, w.Body.String())
	}
	w = c.do(http.MethodGet, gitPath("/api/v1/git/stashes", repo), "", nil)
	if !strings.Contains(w.Body.String(), `"parked"`) {
		t.Fatalf("stashes = %s", w.Body.String())
	}
	w = c.do(http.MethodPost, gitPath("/api/v1/git/stash/drop", repo), `{"index":0}`,
		map[string]string{"X-Confirm": "drop stash"})
	if w.Code != http.StatusOK {
		t.Fatalf("stash drop with the phrase = %d %s", w.Code, w.Body.String())
	}
	// A new repository can be started in an empty directory under a root.
	fresh := filepath.Join(root, "fresh")
	if err := os.Mkdir(fresh, 0o755); err != nil {
		t.Fatal(err)
	}
	w = c.do(http.MethodPost, "/api/v1/git/init", `{"path":"`+fresh+`"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("init = %d %s", w.Code, w.Body.String())
	}
	w = c.do(http.MethodGet, "/api/v1/git/", "", nil)
	if !strings.Contains(w.Body.String(), `"`+fresh+`"`) {
		t.Fatalf("the new repository is not in the list: %s", w.Body.String())
	}
}
