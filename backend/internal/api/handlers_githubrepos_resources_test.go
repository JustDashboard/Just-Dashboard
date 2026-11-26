package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/githubrepos"
)

// These drive the GitHub page's repository routes through the whole chain
// (session, capability, destructive budget, handler) with the repository
// resolved by githubAccessOverride to a client aimed at a local fake. Nothing
// reaches github.com, and only the routes each case names are called.

const ghResourceSHA = "0123456789abcdef0123456789abcdef01234567"

// ghResourceFake answers by "METHOD /path?query" and records every call, so
// a test can assert GitHub was never asked.
type ghResourceFake struct {
	mu     sync.Mutex
	routes map[string]func(w http.ResponseWriter, r *http.Request)
	calls  []string
}

func (f *ghResourceFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := r.Method + " " + r.URL.RequestURI()
	f.mu.Lock()
	f.calls = append(f.calls, key)
	handler := f.routes[key]
	f.mu.Unlock()
	_, _ = io.Copy(io.Discard, r.Body)
	if handler == nil {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"message":"unexpected call ` + key + `"}`))
		return
	}
	handler(w, r)
}

func (f *ghResourceFake) answer(key string, status int, body string) {
	f.routes[key] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func (f *ghResourceFake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type ghResourceRig struct {
	fake                     *ghResourceFake
	admin, limited, readonly *client
	s                        *Server
	// role is the person's role on the repository, as GitHub would state it.
	role string
}

const ghResourceBase = "/api/v1/github/connections/1/repos/octo-org/widgets"

func newGHResourceRig(t *testing.T) *ghResourceRig {
	t.Helper()
	rig := &ghResourceRig{fake: &ghResourceFake{routes: map[string]func(http.ResponseWriter, *http.Request){}}, role: "admin"}
	upstream := httptest.NewServer(rig.fake)
	t.Cleanup(upstream.Close)
	s := testServer(t)
	s.githubAccessOverride = func(r *http.Request) (*githubrepos.RepoAccess, error) {
		installation := githubrepos.Installation{ID: 7, Account: githubrepos.Owner{Login: "octo-org", Type: "Organization"}, Permissions: map[string]string{
			"administration": "write", "contents": "write", "issues": "write", "pull_requests": "write", "actions": "write",
			"checks": "read", "statuses": "read", "metadata": "read",
		}}
		repository := githubrepos.Repository{ID: 42, Name: "widgets", FullName: "octo-org/widgets", Owner: installation.Account, DefaultBranch: "main", Role: rig.role}
		repository.Can = githubrepos.ComputeAbilities(repository, installation, true)
		client := githubrepos.NewClient(upstream.URL, upstream.Client(), func(context.Context) (string, error) { return "ghu_test", nil })
		return &githubrepos.RepoAccess{Client: client, ConnectionID: 1, Login: "mona", Owner: "octo-org", Name: "widgets", Repository: repository, Installation: installation}, nil
	}
	h := s.Routes()
	rig.s = s
	rig.admin = &client{t: t, h: h, cookie: signIn(t, s)}
	rig.limited = &client{t: t, h: h, cookie: signInAs(t, s, "operator", auth.RoleLimited)}
	rig.readonly = &client{t: t, h: h, cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	return rig
}

func ghErrorCode(t *testing.T, w *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return body.Error.Code, body.Error.Message
}

// A read is anybody's; a recoverable change needs service.control; deleting
// a branch, removing a collaborator or withdrawing an invitation is in the
// destructive tier. A refusal by tier never reaches GitHub.
func TestGitHubResourceRoutesHoldTheirCapabilityTiers(t *testing.T) {
	rig := newGHResourceRig(t)
	rig.fake.answer("GET /repos/octo-org/widgets/branches?page=1&per_page=100", 200, `[{"name":"main","commit":{"sha":"`+ghResourceSHA+`"}}]`)
	w := rig.readonly.do(http.MethodGet, ghResourceBase+"/branches", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"default":true`) {
		t.Fatalf("reader GET branches: %d %s", w.Code, w.Body.String())
	}
	asked := rig.fake.count()

	writes := []struct{ method, path, body string }{
		{http.MethodPost, "/branches", `{"name":"x","from":"main"}`},
		{http.MethodPost, "/pulls/5/merge", `{"method":"merge","sha":"` + ghResourceSHA + `"}`},
		{http.MethodPost, "/pulls/5/state", `{"state":"closed"}`},
		{http.MethodPost, "/issues", `{"title":"x"}`},
		{http.MethodPatch, "/issues/4", `{"title":"x"}`},
		{http.MethodPost, "/actions/workflows/1/dispatch", `{"ref":"main","inputs":{}}`},
		{http.MethodPost, "/actions/runs/10/rerun", `{"failedOnly":false}`},
		{http.MethodPost, "/actions/runs/10/cancel", ``},
		{http.MethodPut, "/collaborators/newcomer", `{"role":"read"}`},
	}
	destructive := []struct{ method, path string }{
		{http.MethodDelete, "/branches?name=dev"},
		{http.MethodDelete, "/collaborators/newcomer"},
		{http.MethodDelete, "/invitations/5"},
	}
	for _, req := range writes {
		if w := rig.readonly.do(req.method, ghResourceBase+req.path, req.body, nil); w.Code != http.StatusForbidden {
			t.Errorf("reader %s %s: %d %s", req.method, req.path, w.Code, w.Body.String())
		}
	}
	for _, req := range destructive {
		for name, who := range map[string]*client{"reader": rig.readonly, "limited": rig.limited} {
			if w := who.do(req.method, ghResourceBase+req.path, "", nil); w.Code != http.StatusForbidden {
				t.Errorf("%s %s %s: %d %s", name, req.method, req.path, w.Code, w.Body.String())
			}
		}
	}
	if rig.fake.count() != asked {
		t.Fatalf("a refused tier reached GitHub: %v", rig.fake.calls[asked:])
	}

	// The limited account holds service.control: its branch is created.
	rig.fake.answer("GET /repos/octo-org/widgets/git/ref/heads/main", 200, `{"object":{"sha":"`+ghResourceSHA+`"}}`)
	rig.fake.answer("POST /repos/octo-org/widgets/git/refs", 201, `{"ref":"refs/heads/x","object":{"sha":"`+ghResourceSHA+`"}}`)
	if w := rig.limited.do(http.MethodPost, ghResourceBase+"/branches", `{"name":"x","from":"main"}`, nil); w.Code != http.StatusCreated {
		t.Fatalf("limited POST branches: %d %s", w.Code, w.Body.String())
	}
	entries, _, err := rig.s.Audit.List(t.Context(), audit.Filter{Action: "github.branch.create"})
	if err != nil || len(entries) != 1 || entries[0].Target != "octo-org/widgets" || !strings.Contains(entries[0].Detail, `"branch":"x"`) {
		t.Fatalf("audit = %+v %v", entries, err)
	}
}

// What the person's role or the App's grant does not cover is refused here,
// with the reason, before GitHub is asked.
func TestGitHubResourceAbilitiesAreRefusedWithTheReason(t *testing.T) {
	rig := newGHResourceRig(t)
	rig.role = "triage"
	for _, req := range []struct{ method, path, body string }{
		{http.MethodPost, "/pulls/5/merge", `{"method":"merge","sha":"` + ghResourceSHA + `"}`},
		{http.MethodPost, "/branches", `{"name":"x","from":"main"}`},
		{http.MethodPost, "/actions/runs/10/rerun", `{"failedOnly":true}`},
		{http.MethodPut, "/collaborators/newcomer", `{"role":"write"}`},
		{http.MethodDelete, "/branches?name=dev", ``},
	} {
		w := rig.admin.do(req.method, ghResourceBase+req.path, req.body, nil)
		code, message := ghErrorCode(t, w)
		if w.Code != http.StatusForbidden || code != "github_permission" || !strings.Contains(message, "Your role on octo-org/widgets is triage") {
			t.Errorf("%s %s: %d %s", req.method, req.path, w.Code, w.Body.String())
		}
	}
	if rig.fake.count() != 0 {
		t.Fatalf("GitHub was asked: %v", rig.fake.calls)
	}
	entries, _, err := rig.s.Audit.List(t.Context(), audit.Filter{Action: "github.pull.merge"})
	if err != nil || len(entries) != 1 || entries[0].Success {
		t.Fatalf("a refused merge is audited as refused: %+v %v", entries, err)
	}
}

func TestGitHubResourceRefusalsOfAValueNeverReachGitHub(t *testing.T) {
	rig := newGHResourceRig(t)
	for _, req := range []struct{ method, path, body string }{
		{http.MethodDelete, "/branches?name=main", ``},
		{http.MethodDelete, "/branches?name=a..b", ``},
		{http.MethodPost, "/pulls/5/merge", `{"method":"merge"}`},
		{http.MethodPost, "/pulls/5/merge", `{"method":"merge","sha":"abc123"}`},
		{http.MethodPost, "/pulls/0/merge", `{"method":"merge","sha":"` + ghResourceSHA + `"}`},
		{http.MethodGet, "/pulls/x", ``},
		{http.MethodGet, "/branches/protection?name=", ``},
		{http.MethodPost, "/branches", `{"name":"refs/heads/x"}`},
		{http.MethodPost, "/issues", `{"title":""}`},
		{http.MethodPut, "/collaborators/bad..name", `{"role":"read"}`},
		{http.MethodGet, "/actions/runs?workflow=-1", ``},
	} {
		w := rig.admin.do(req.method, ghResourceBase+req.path, req.body, nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s %s %s: %d %s", req.method, req.path, req.body, w.Code, w.Body.String())
		}
	}
	w := rig.admin.do(http.MethodDelete, ghResourceBase+"/branches?name=main", "", nil)
	if _, message := ghErrorCode(t, w); !strings.Contains(message, "default branch") {
		t.Fatalf("default branch refusal: %s", w.Body.String())
	}
	if rig.fake.count() != 0 {
		t.Fatalf("GitHub was asked: %v", rig.fake.calls)
	}
}

func TestGitHubResourceGitHubsAnswersInThePagesWords(t *testing.T) {
	rig := newGHResourceRig(t)
	rig.fake.answer("PUT /repos/octo-org/widgets/pulls/5/merge", 405, `{"message":"Pull Request is not mergeable"}`)
	w := rig.admin.do(http.MethodPost, ghResourceBase+"/pulls/5/merge", `{"method":"squash","sha":"`+ghResourceSHA+`"}`, nil)
	if code, message := ghErrorCode(t, w); w.Code != http.StatusConflict || code != "github_conflict" || !strings.Contains(message, "not mergeable") {
		t.Fatalf("not mergeable: %d %s", w.Code, w.Body.String())
	}

	rig.fake.answer("GET /repos/octo-org/widgets/collaborators?affiliation=all&per_page=100", 403, `{"message":"Must have push access to view repository collaborators."}`)
	w = rig.admin.do(http.MethodGet, ghResourceBase+"/collaborators", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"readable":false`) || !strings.Contains(w.Body.String(), "push access") {
		t.Fatalf("unreadable collaborators: %d %s", w.Code, w.Body.String())
	}

	rig.fake.routes["GET /repos/octo-org/widgets/pulls?page=1&per_page=30&state=open"] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "4102444800")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}
	w = rig.admin.do(http.MethodGet, ghResourceBase+"/pulls", "", nil)
	if code, _ := ghErrorCode(t, w); w.Code != http.StatusTooManyRequests || code != "github_rate_limited" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limit: %d %s %v", w.Code, w.Body.String(), w.Header())
	}

	rig.fake.answer("GET /repos/octo-org/widgets/issues/9", 401, `{"message":"Bad credentials"}`)
	w = rig.admin.do(http.MethodGet, ghResourceBase+"/issues/9", "", nil)
	if code, _ := ghErrorCode(t, w); w.Code != http.StatusConflict || code != "github_reconnect" {
		t.Fatalf("revoked: %d %s", w.Code, w.Body.String())
	}
}

// An issue's title and body, and a dispatch's input values, are never audit
// details.
func TestGitHubResourceAuditKeepsNoTextOrValues(t *testing.T) {
	rig := newGHResourceRig(t)
	rig.fake.answer("POST /repos/octo-org/widgets/issues", 201, `{"number":12,"title":"Secret title","state":"open"}`)
	if w := rig.admin.do(http.MethodPost, ghResourceBase+"/issues", `{"title":"Secret title","body":"Secret body"}`, nil); w.Code != http.StatusCreated {
		t.Fatalf("create issue: %d %s", w.Code, w.Body.String())
	}
	rig.fake.answer("GET /repos/octo-org/widgets/actions/workflows/1", 200, `{"id":1,"name":"Deploy","path":".github/workflows/deploy.yml","state":"active"}`)
	rig.fake.answer("GET /repos/octo-org/widgets/contents/.github/workflows/deploy.yml?ref=main", 200,
		`{"type":"file","encoding":"base64","sha":"blob-audit","size":53,"content":"b246CiAgd29ya2Zsb3dfZGlzcGF0Y2g6CiAgICBpbnB1dHM6CiAgICAgIHRva2VuOiB7fQo="}`)
	rig.fake.routes["POST /repos/octo-org/widgets/actions/workflows/1/dispatches"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}
	if w := rig.admin.do(http.MethodPost, ghResourceBase+"/actions/workflows/1/dispatch", `{"ref":"main","inputs":{"token":"pasted-value"}}`, nil); w.Code != http.StatusOK {
		t.Fatalf("dispatch: %d %s", w.Code, w.Body.String())
	}
	entries, _, err := rig.s.Audit.List(t.Context(), audit.Filter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Action, "github.") {
			continue
		}
		seen++
		for _, secret := range []string{"Secret title", "Secret body", "pasted-value", "ghu_test"} {
			if strings.Contains(entry.Detail, secret) || strings.Contains(entry.Target, secret) {
				t.Fatalf("%s keeps %q: %s", entry.Action, secret, entry.Detail)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("audited %d GitHub changes: %+v", seen, entries)
	}
}

// The README and the languages are reads like the rest: anybody with a
// session, through the repository as it was resolved.
func TestAReaderGetsARepositorysReadmeAndLanguages(t *testing.T) {
	rig := newGHResourceRig(t)
	source := "# Widgets\n\nWhat they are.\n"
	rig.fake.answer("GET /repos/octo-org/widgets/readme", 200, `{"type":"file","encoding":"base64","size":26,"path":"README.md",
		"content":"`+base64.StdEncoding.EncodeToString([]byte(source))+`","html_url":"https://github.com/octo-org/widgets/blob/main/README.md"}`)
	w := rig.readonly.do(http.MethodGet, ghResourceBase+"/readme", "", nil)
	want := `{"found":true,"path":"README.md","content":"# Widgets\n\nWhat they are.\n","htmlUrl":"https://github.com/octo-org/widgets/blob/main/README.md"}`
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != want {
		t.Fatalf("readme = %d %s", w.Code, w.Body)
	}
	rig.fake.answer("GET /repos/octo-org/widgets/readme", 404, `{"message":"Not Found"}`)
	w = rig.readonly.do(http.MethodGet, ghResourceBase+"/readme", "", nil)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"found":false}` {
		t.Fatalf("no readme = %d %s", w.Code, w.Body)
	}
	rig.fake.answer("GET /repos/octo-org/widgets/readme", 403, `{"message":"Resource not accessible by integration"}`)
	w = rig.readonly.do(http.MethodGet, ghResourceBase+"/readme", "", nil)
	if code, _ := ghErrorCode(t, w); w.Code != http.StatusForbidden || code != "github_forbidden" {
		t.Fatalf("refused readme = %d %s", w.Code, w.Body)
	}

	rig.fake.answer("GET /repos/octo-org/widgets/languages", 200, `{"CSS":300,"Go":9000,"Astro":300}`)
	w = rig.readonly.do(http.MethodGet, ghResourceBase+"/languages", "", nil)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"languages":[{"name":"Go","bytes":9000},{"name":"Astro","bytes":300},{"name":"CSS","bytes":300}]}` {
		t.Fatalf("languages = %d %s", w.Code, w.Body)
	}
	rig.fake.answer("GET /repos/octo-org/widgets/languages", 200, `{}`)
	w = rig.readonly.do(http.MethodGet, ghResourceBase+"/languages", "", nil)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"languages":[]}` {
		t.Fatalf("no languages = %d %s", w.Code, w.Body)
	}
}
