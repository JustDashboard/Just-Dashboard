package githubrepos

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeGitHub answers the calls a test expects, by "METHOD /path" (the query
// is matched when the key carries one), and fails the test on any other.
type fakeGitHub struct {
	t      *testing.T
	mu     sync.Mutex
	routes map[string]http.HandlerFunc
	calls  []string
	bodies map[string]map[string]any
}

func newFake(t *testing.T) *fakeGitHub {
	return &fakeGitHub{t: t, routes: map[string]http.HandlerFunc{}, bodies: map[string]map[string]any{}}
}

func (f *fakeGitHub) on(key string, handler http.HandlerFunc) { f.routes[key] = handler }

// json answers key with status and body.
func (f *fakeGitHub) json(key string, status int, body string) {
	f.on(key, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	withQuery := r.Method + " " + r.URL.RequestURI()
	bare := r.Method + " " + r.URL.EscapedPath()
	f.mu.Lock()
	f.calls = append(f.calls, withQuery)
	if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.bodies[bare] = body
	}
	handler := f.routes[withQuery]
	if handler == nil {
		handler = f.routes[bare]
	}
	f.mu.Unlock()
	if handler == nil {
		f.t.Errorf("unexpected call %s", withQuery)
		http.Error(w, `{"message":"unexpected"}`, http.StatusTeapot)
		return
	}
	handler(w, r)
}

func (f *fakeGitHub) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeGitHub) body(key string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[key]
}

// testAccess is octo-org/widgets, default branch main, reached with every
// ability unless the test takes some away.
func testAccess(t *testing.T, fake *fakeGitHub) *RepoAccess {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := NewClient(server.URL, server.Client(), func(context.Context) (string, error) { return "ghu_test", nil })
	installation := Installation{ID: 7, Account: Owner{Login: "octo-org", Type: "Organization"}, Permissions: map[string]string{
		"administration": "write", "contents": "write", "issues": "write", "pull_requests": "write",
		"actions": "write", "checks": "read", "statuses": "read", "metadata": "read",
	}}
	repository := Repository{ID: 42, Name: "widgets", FullName: "octo-org/widgets", Owner: Owner{Login: "octo-org", Type: "Organization"}, DefaultBranch: "main", Role: "admin"}
	repository.Can = ComputeAbilities(repository, installation, true)
	return &RepoAccess{Client: client, ConnectionID: 1, Login: "mona", Owner: "octo-org", Name: "widgets", Repository: repository, Installation: installation}
}

// withRole recomputes the abilities as if the person's role were role.
func withRole(access *RepoAccess, role string) *RepoAccess {
	access.Repository.Role = role
	access.Repository.Can = ComputeAbilities(access.Repository, access.Installation, true)
	return access
}

func wantInvalid(t *testing.T, err error) {
	t.Helper()
	var bad *InvalidError
	if !errors.As(err, &bad) {
		t.Fatalf("err = %v (%T), want an InvalidError", err, err)
	}
}

func wantNotPermitted(t *testing.T, err error, contains string) {
	t.Helper()
	var refused *NotPermittedError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v (%T), want a NotPermittedError", err, err)
	}
	if !strings.Contains(refused.Reason, contains) {
		t.Fatalf("reason = %q, want it to mention %q", refused.Reason, contains)
	}
}

func TestBranchNamesFollowGitsRules(t *testing.T) {
	for _, good := range []string{"main", "feature/x", "release-1.2", "fix_ä", "user@host", "a/b/c"} {
		if err := ValidBranchName(good); err != nil {
			t.Errorf("%q refused: %v", good, err)
		}
	}
	for _, bad := range []string{"", "-x", "refs/heads/x", "a..b", "a b", "a~1", "a^", "a:b", "a?", "a*", "a[b", `a\b`,
		"/a", "a/", "a//b", "a.", ".a", "a/.b", "x.lock", "a/b.lock/c", "@", "HEAD", "a@{1}", "a\x00b", strings.Repeat("x", 256)} {
		if err := ValidBranchName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestParsePositive(t *testing.T) {
	if n, err := ParsePositive("12", "issue"); err != nil || n != 12 {
		t.Fatalf("12 = %d %v", n, err)
	}
	for _, bad := range []string{"", "0", "-1", "1e3", "12a", "99999999999999999999"} {
		if _, err := ParsePositive(bad, "issue"); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
