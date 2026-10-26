package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

func TestParseForgeRemoteReadsEveryRemoteShape(t *testing.T) {
	cases := []struct {
		remote, scheme, host string
		segments             []string
	}{
		{"https://gitlab.example.com/group/sub/app.git", "https", "gitlab.example.com", []string{"group", "sub", "app"}},
		{"http://127.0.0.1:3300/owner/app.git", "http", "127.0.0.1:3300", []string{"owner", "app"}},
		{"https://git.example.com/gitea/owner/app", "https", "git.example.com", []string{"gitea", "owner", "app"}},
		{"ssh://git@gitlab.example.com:2222/group/app.git", "https", "gitlab.example.com", []string{"group", "app"}},
		{"git@codeberg.org:owner/app.git", "https", "codeberg.org", []string{"owner", "app"}},
	}
	for _, test := range cases {
		remote, ok := parseForgeRemote(test.remote)
		if !ok || remote.scheme != test.scheme || remote.host != test.host || strings.Join(remote.segments, "/") != strings.Join(test.segments, "/") {
			t.Fatalf("parseForgeRemote(%q) = %+v, %v", test.remote, remote, ok)
		}
	}
	for _, remote := range []string{"", "https://gitlab.example.com/only", "file:///srv/app.git", "https://host/a/../b.git", "https://host/a/%2e%2e/b"} {
		if parsed, ok := parseForgeRemote(remote); ok {
			t.Fatalf("parseForgeRemote(%q) accepted %+v", remote, parsed)
		}
	}
}

// fakeForge is a GitLab or Gitea API on loopback that records what it was
// asked and answers the way the real servers did when this was written
// (GitLab 18.4, Gitea 1.24, Forgejo 11).
type fakeForge struct {
	t        *testing.T
	provider string
	prefix   string
	token    string
	userID   int64
	server   *httptest.Server

	mu       sync.Mutex
	requests []fakeForgeRequest
	comments []fakeForgeComment
	nextID   int64
	refuse   int
}

type fakeForgeRequest struct {
	Method, Path, Authorization string
	Body                        map[string]any
}

type fakeForgeComment struct {
	ID     int64
	Author int64
	Body   string
	System bool
}

func newFakeForge(t *testing.T, provider, prefix string) *fakeForge {
	t.Helper()
	f := &fakeForge{t: t, provider: provider, prefix: prefix, token: "forge-token-1234567890", userID: 7, nextID: 100}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeForge) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.EscapedPath(), f.prefix)
	var body map[string]any
	if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	f.mu.Lock()
	f.requests = append(f.requests, fakeForgeRequest{r.Method, path, r.Header.Get("Authorization"), body})
	refuse := f.refuse
	f.mu.Unlock()
	want := "Bearer " + f.token
	if f.provider == ForgeGitea {
		want = "token " + f.token
	}
	switch {
	case f.provider == ForgeGitea && path == "/api/v1/version":
		_, _ = w.Write([]byte(`{"version":"1.24.7"}`))
		return
	case f.provider == ForgeGitLab && path == "/api/v4/version":
		if r.Header.Get("Authorization") != want {
			w.Header().Set("X-Gitlab-Meta", `{"correlation_id":"x","version":"1"}`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"version":"18.4.2"}`))
		return
	}
	if r.Header.Get("Authorization") != want {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"401 Unauthorized"}`))
		return
	}
	if refuse != 0 {
		w.WriteHeader(refuse)
		_, _ = w.Write([]byte(`{"error":"insufficient_scope","error_description":"The request requires higher privileges than provided by the access token."}`))
		return
	}
	switch {
	case path == "/api/v4/user" || path == "/api/v1/user":
		_, _ = w.Write([]byte(`{"id":` + strconv.FormatInt(f.userID, 10) + `}`))
	case strings.Contains(path, "/statuses/"):
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	case strings.HasSuffix(path, "/notes") && r.Method == http.MethodGet:
		f.writeGitLabNotes(w, r)
	case strings.HasSuffix(path, "/comments") && r.Method == http.MethodGet:
		f.mu.Lock()
		out := []map[string]any{}
		for _, comment := range f.comments {
			out = append(out, map[string]any{"id": comment.ID, "body": comment.Body, "user": map[string]any{"id": comment.Author}})
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(out)
	case (strings.HasSuffix(path, "/notes") || strings.HasSuffix(path, "/comments")) && r.Method == http.MethodPost:
		f.mu.Lock()
		f.nextID++
		f.comments = append(f.comments, fakeForgeComment{ID: f.nextID, Author: f.userID, Body: body["body"].(string)})
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	case r.Method == http.MethodPut || r.Method == http.MethodPatch:
		id, _ := strconv.ParseInt(path[strings.LastIndex(path, "/")+1:], 10, 64)
		f.mu.Lock()
		for i := range f.comments {
			if f.comments[i].ID == id {
				f.comments[i].Body = body["body"].(string)
			}
		}
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"404 Not Found"}`))
	}
}

// writeGitLabNotes pages two notes at a time, so a marker on the second page
// is only found by following X-Next-Page.
func (f *fakeForge) writeGitLabNotes(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	start, end := (page-1)*2, page*2
	if end >= len(f.comments) {
		end = len(f.comments)
	} else {
		w.Header().Set("X-Next-Page", strconv.Itoa(page+1))
	}
	out := []map[string]any{}
	for _, comment := range f.comments[min(start, end):end] {
		out = append(out, map[string]any{"id": comment.ID, "body": comment.Body, "system": comment.System, "author": map[string]any{"id": comment.Author}})
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (f *fakeForge) all() []fakeForgeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeForgeRequest(nil), f.requests...)
}

func (f *fakeForge) writes() []fakeForgeRequest {
	var out []fakeForgeRequest
	for _, request := range f.all() {
		if request.Method != http.MethodGet {
			out = append(out, request)
		}
	}
	return out
}

type staticForgeTokens struct {
	token ForgeToken
	host  string
}

func (s staticForgeTokens) ForgeToken(_ context.Context, _ int64, host string) (ForgeToken, bool, error) {
	if s.token.Secret == "" || host != s.host {
		return ForgeToken{}, false, nil
	}
	return s.token, true, nil
}

func (f *fakeForge) host() string { return strings.TrimPrefix(f.server.URL, "http://") }

func (f *fakeForge) client() *ForgeClient {
	return NewForgeClient(staticForgeTokens{token: ForgeToken{ID: 1, Name: "ci-token", Secret: f.token}, host: f.host()})
}

func TestForgeClientFindsGiteaUnderASubpathAndGitLabWithNestedGroups(t *testing.T) {
	gitea := newFakeForge(t, ForgeGitea, "/git")
	endpoint, err := gitea.client().Resolve(t.Context(), SourceIdentity{Remote: gitea.server.URL + "/git/acme/shop.git"}, 0)
	if err != nil || endpoint.Provider != ForgeGitea || endpoint.Product != "gitea" || endpoint.Origin != gitea.server.URL+"/git" || endpoint.Repository != "acme/shop" || endpoint.Credential != "ci-token" {
		t.Fatalf("gitea endpoint = %+v, %v", endpoint, err)
	}
	gitlab := newFakeForge(t, ForgeGitLab, "")
	endpoint, err = gitlab.client().Resolve(t.Context(), SourceIdentity{Remote: gitlab.server.URL + "/platform/web/shop.git"}, 0)
	if err != nil || endpoint.Provider != ForgeGitLab || endpoint.Origin != gitlab.server.URL || endpoint.Repository != "platform/web/shop" {
		t.Fatalf("gitlab endpoint = %+v, %v", endpoint, err)
	}
	// Without a token a GitLab is still recognised by its refusal, so the
	// reading can say which host is waiting for one.
	anonymous := NewForgeClient(staticForgeTokens{})
	endpoint, err = anonymous.Resolve(t.Context(), SourceIdentity{Remote: gitlab.server.URL + "/platform/web/shop.git"}, 0)
	if !errors.Is(err, ErrForgeNoToken) || endpoint.Provider != ForgeGitLab || endpoint.Host != gitlab.host() {
		t.Fatalf("anonymous gitlab = %+v, %v", endpoint, err)
	}
	// A host that is neither is unsupported, and the answer is remembered
	// rather than asked again on every settings poll.
	plain := httptest.NewServer(http.NotFoundHandler())
	defer plain.Close()
	client := NewForgeClient(staticForgeTokens{})
	for range 2 {
		if _, err := client.Resolve(t.Context(), SourceIdentity{Remote: plain.URL + "/acme/shop.git"}, 0); !errors.Is(err, ErrForgeUnsupported) {
			t.Fatalf("plain host = %v", err)
		}
	}
	if len(client.kinds) != 1 {
		t.Fatalf("detections cached = %d", len(client.kinds))
	}
	if _, err := client.Resolve(t.Context(), SourceIdentity{Remote: "https://github.com/acme/shop.git"}, 0); !errors.Is(err, ErrForgeUnsupported) {
		t.Fatalf("github must stay on its own path: %v", err)
	}
}

func TestForgeClientNeverSendsATokenOverPlainHTTPToAnotherMachine(t *testing.T) {
	client := NewForgeClient(staticForgeTokens{token: ForgeToken{Name: "t", Secret: "forge-token-1234567890"}, host: "gitlab.example.test"})
	_, err := client.Resolve(t.Context(), SourceIdentity{Remote: "http://gitlab.example.test/acme/shop.git"}, 0)
	if err == nil || !strings.Contains(err.Error(), "plain http") {
		t.Fatalf("token over http = %v", err)
	}
	if len(client.kinds) != 0 {
		t.Fatal("the host was asked before the token's transport was checked")
	}
}

func TestForgeClientPostsStatusesInEachHostsWords(t *testing.T) {
	sha := strings.Repeat("c", 40)
	for _, test := range []struct {
		provider, remotePath, wantPath, wantAuth string
		states                                   map[string]string
	}{
		{ForgeGitLab, "/platform/web/shop.git", "/api/v4/projects/platform%2Fweb%2Fshop/statuses/" + sha, "Bearer ", map[string]string{"pending": "running", "success": "success", "failure": "failed", "error": "canceled"}},
		{ForgeGitea, "/acme/shop.git", "/api/v1/repos/acme/shop/statuses/" + sha, "token ", map[string]string{"pending": "pending", "success": "success", "failure": "failure", "error": "error"}},
	} {
		forge := newFakeForge(t, test.provider, "")
		client := forge.client()
		endpoint, err := client.Resolve(t.Context(), SourceIdentity{Remote: forge.server.URL + test.remotePath}, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, state := range []string{"pending", "success", "failure", "error"} {
			if err := client.PostStatus(t.Context(), endpoint, sha, CommitStatus{State: state, Context: "just-dashboard/production", Description: strings.Repeat("é", 200), TargetURL: "https://dash.example.test/deploy/1/runs/2"}); err != nil {
				t.Fatalf("%s %s: %v", test.provider, state, err)
			}
		}
		writes := forge.writes()
		if len(writes) != 4 {
			t.Fatalf("%s writes = %+v", test.provider, writes)
		}
		nameField := "name"
		if test.provider == ForgeGitea {
			nameField = "context"
		}
		for i, state := range []string{"pending", "success", "failure", "error"} {
			got := writes[i]
			if got.Path != test.wantPath || got.Authorization != test.wantAuth+forge.token || got.Body["state"] != test.states[state] ||
				got.Body[nameField] != "just-dashboard/production" || got.Body["target_url"] != "https://dash.example.test/deploy/1/runs/2" ||
				len([]rune(got.Body["description"].(string))) != 140 {
				t.Fatalf("%s %s = %+v", test.provider, state, got)
			}
		}
		if err := client.PostStatus(t.Context(), endpoint, "main", CommitStatus{State: "success"}); err == nil {
			t.Fatal("a branch name is not a commit")
		}
	}
}

func TestForgeClientKeepsOneCommentPerPreviewAndNeverEditsAnothersMarker(t *testing.T) {
	marker := "<!-- just-dashboard:preview:9 -->"
	for _, test := range []struct{ provider, remotePath, listSuffix string }{
		{ForgeGitLab, "/platform/shop.git", "/notes"},
		{ForgeGitea, "/acme/shop.git", "/comments"},
	} {
		forge := newFakeForge(t, test.provider, "")
		// Somebody quoted the marker, GitLab wrote a system note with it,
		// and an unrelated note fills the first page.
		forge.comments = []fakeForgeComment{
			{ID: 1, Author: 99, Body: "quoting " + marker},
			{ID: 2, Author: forge.userID, Body: "system " + marker, System: true},
			{ID: 3, Author: forge.userID, Body: "unrelated"},
		}
		forge.nextID = 3
		client := forge.client()
		endpoint, err := client.Resolve(t.Context(), SourceIdentity{Remote: forge.server.URL + test.remotePath}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if test.provider == ForgeGitea {
			// Gitea has no system comments in this list.
			forge.comments = append(forge.comments[:1], forge.comments[2:]...)
		}
		if err := client.UpsertComment(t.Context(), endpoint, 5, marker, "Building"); err != nil {
			t.Fatal(err)
		}
		if err := client.UpsertComment(t.Context(), endpoint, 5, marker, "Ready"); err != nil {
			t.Fatal(err)
		}
		writes := forge.writes()
		if len(writes) != 2 || writes[0].Method != http.MethodPost || !strings.HasSuffix(writes[0].Path, "/5"+test.listSuffix) {
			t.Fatalf("%s writes = %+v", test.provider, writes)
		}
		wantEdit := map[string]string{ForgeGitLab: http.MethodPut, ForgeGitea: http.MethodPatch}[test.provider]
		if writes[1].Method != wantEdit || !strings.HasSuffix(writes[1].Path, "/4") {
			t.Fatalf("%s edit = %+v", test.provider, writes[1])
		}
		if forge.comments[0].Body != "quoting "+marker {
			t.Fatalf("%s overwrote another author's comment: %+v", test.provider, forge.comments)
		}
		if got := forge.comments[len(forge.comments)-1]; got.Body != marker+"\nReady" {
			t.Fatalf("%s comment = %+v", test.provider, got)
		}
	}
}

func TestForgeClientSaysWhatTheHostRefused(t *testing.T) {
	forge := newFakeForge(t, ForgeGitLab, "")
	client := forge.client()
	endpoint, err := client.Resolve(t.Context(), SourceIdentity{Remote: forge.server.URL + "/platform/shop.git"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	forge.refuse = http.StatusForbidden
	err = client.PostStatus(t.Context(), endpoint, strings.Repeat("d", 40), CommitStatus{State: "success", Context: "just-dashboard/production"})
	var refusal *forgeHTTPError
	if !errors.As(err, &refusal) || refusal.Status != http.StatusForbidden ||
		!strings.Contains(err.Error(), "api scope") || !strings.Contains(err.Error(), "higher privileges") {
		t.Fatalf("refusal = %v", err)
	}
}

func forgeTokenFixture(t *testing.T) (*automationFixture, *PlanningStore) {
	t.Helper()
	f := newAutomationFixture(t)
	sealer, err := auth.NewSealer(strings.Repeat("a7", 32))
	if err != nil {
		t.Fatal(err)
	}
	return f, NewPlanningStore(f.store, sealer, nil)
}

func TestPlanningStoreForgeTokenPrefersTheSourcesOwnTokenThenTheHosts(t *testing.T) {
	f, plans := forgeTokenFixture(t)
	ctx := t.Context()
	create := func(name, kind, target, secret string) int64 {
		created, err := plans.CreateCredential(ctx, CredentialCreateRequest{Name: name, Kind: kind, Target: target, Secret: secret})
		if err != nil {
			t.Fatal(err)
		}
		return created.ID
	}
	bearer := create("clone-token", CredentialGitBearer, "", "clone-token-123456")
	sshKey := create("deploy-key", CredentialGitSSH, "", "-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----\n")
	create("other-host", CredentialProviderToken, "gitlab.other.test", "other-token-123456")
	hostToken := create("gitlab-api", CredentialProviderToken, "gitlab.example.test", "api-token-123456")
	portToken := create("gitea-api", CredentialProviderToken, "127.0.0.1:3300", "gitea-token-123456")

	if token, ok, err := plans.ForgeToken(ctx, bearer, "gitlab.example.test"); err != nil || !ok || token.ID != bearer || token.Secret != "clone-token-123456" {
		t.Fatalf("source's own token = %+v, %v, %v", token, ok, err)
	}
	if token, ok, err := plans.ForgeToken(ctx, sshKey, "gitlab.example.test"); err != nil || !ok || token.ID != hostToken {
		t.Fatalf("ssh source falls back to the host's token = %+v, %v, %v", token, ok, err)
	}
	if token, ok, err := plans.ForgeToken(ctx, 0, "127.0.0.1:3300"); err != nil || !ok || token.ID != portToken {
		t.Fatalf("host with port = %+v, %v, %v", token, ok, err)
	}
	if _, ok, err := plans.ForgeToken(ctx, 0, "gitea.unknown.test"); err != nil || ok {
		t.Fatalf("no token for an unknown host: %v, %v", ok, err)
	}
	_ = f
}

func forgeSourceFixture(t *testing.T, remote string) (*automationFixture, *OrchestrationStore) {
	t.Helper()
	f := newAutomationFixture(t)
	// Source rows are immutable, so the fixture's row is replaced whole.
	identity := mustJSON(SourceIdentity{Kind: SourceGit, Remote: remote, Ref: "main", Revision: strings.Repeat("b", 40)})
	for _, statement := range []string{
		`DELETE FROM deploy_sources WHERE environment_id=?`,
		`INSERT INTO deploy_sources(environment_id,revision,kind,config_json,identity_json,digest,created_at) VALUES(?,1,'git','{}',?,'source',1)`,
	} {
		args := []any{f.environmentID}
		if strings.HasPrefix(statement, "INSERT") {
			args = append(args, string(identity))
		}
		if _, err := f.store.DB.Exec(statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	return f, NewOrchestrationStore(f.store)
}

func TestCommitStatusPublisherReportsToGiteaAndSaysSoInSettings(t *testing.T) {
	forge := newFakeForge(t, ForgeGitea, "")
	f, runs := forgeSourceFixture(t, forge.server.URL+"/acme/shop.git")
	sealer, _ := auth.NewSealer(strings.Repeat("a7", 32))
	plans := NewPlanningStore(f.store, sealer, nil)
	publisher := NewCommitStatusPublisher(runs, &recordingStatusPoster{}, func() string { return "https://dash.example.test" }, nil).
		WithForges(NewForgeClient(plans))

	// No token yet: nothing reaches the host, the run records why, and
	// Settings names the host the token is for.
	run := EngineRun{ID: 51, ProjectID: f.projectID, EnvironmentID: f.environmentID, PlanRevision: 1, State: RunPreparing, Operation: OperationDeploy, SourceRevision: strings.Repeat("a", 40)}
	publisher.RunStarted(t.Context(), run)
	if writes := forge.writes(); len(writes) != 0 {
		t.Fatalf("posted without a token: %+v", writes)
	}
	reading, err := publisher.Describe(t.Context(), f.projectID, f.environmentID)
	if err != nil || reading.Provider != ForgeGitea || reading.Ready || !strings.Contains(reading.Reason, forge.host()) ||
		reading.LastStatus == nil || reading.LastStatus.OK || !strings.Contains(reading.LastStatus.Message, "provider token") {
		t.Fatalf("reading without a token = %+v, %v", reading, err)
	}

	created, err := plans.CreateCredential(t.Context(), CredentialCreateRequest{Name: "gitea-ci", Kind: CredentialProviderToken, Target: forge.host(), Secret: forge.token})
	if err != nil {
		t.Fatal(err)
	}
	run.ID = 52
	publisher.RunStarted(t.Context(), run)
	run.State = RunSucceeded
	publisher.RunFinished(t.Context(), run)
	writes := forge.writes()
	if len(writes) != 2 || writes[0].Body["state"] != "pending" || writes[1].Body["state"] != "success" ||
		writes[1].Body["context"] != "just-dashboard/production" || writes[1].Body["target_url"] != "https://dash.example.test/deploy/"+itoa(f.projectID)+"/runs/52" {
		t.Fatalf("writes = %+v", writes)
	}
	reading, err = publisher.Describe(t.Context(), f.projectID, f.environmentID)
	if err != nil || !reading.Ready || reading.Credential != "gitea-ci" || reading.Repository != "acme/shop" || reading.Context != "just-dashboard/production" ||
		reading.LastStatus == nil || !reading.LastStatus.OK || reading.LastStatus.State != "success" || reading.LastStatus.RunID != 52 {
		t.Fatalf("reading = %+v (last %+v), %v", reading, reading.LastStatus, err)
	}
	summary, err := plans.GetCredential(t.Context(), created.ID)
	if err != nil || summary.LastUsedAt == nil {
		t.Fatalf("writing a status did not mark the token used: %+v, %v", summary, err)
	}

	// The policy switch still decides.
	if _, err := runs.SetGitDeploymentPolicy(t.Context(), f.projectID, f.environmentID, GitDeploymentPolicy{Automatic: true, CommitStatuses: false}); err != nil {
		t.Fatal(err)
	}
	run.ID = 53
	publisher.RunFinished(t.Context(), run)
	if len(forge.writes()) != 2 {
		t.Fatal("a status was posted with commit statuses turned off")
	}
}

func TestPullRequestCommenterWritesOnAGitLabMergeRequest(t *testing.T) {
	forge := newFakeForge(t, ForgeGitLab, "")
	f := newAutomationFixture(t)
	setSourceRemote(t, f, f.environmentID, forge.server.URL+"/platform/shop.git", "")
	sealer, _ := auth.NewSealer(strings.Repeat("a7", 32))
	plans := NewPlanningStore(f.store, sealer, nil)
	if _, err := plans.CreateCredential(t.Context(), CredentialCreateRequest{Name: "gitlab-ci", Kind: CredentialProviderToken, Target: forge.host(), Secret: forge.token}); err != nil {
		t.Fatal(err)
	}
	created, err := f.automation.CreateTrigger(t.Context(), f.projectID, f.environmentID, TriggerWrite{Name: "Review", Kind: TriggerGitLab, Provider: "gitlab", Enabled: true, Config: TriggerConfig{Repository: "platform/shop", Ref: "main", Preview: true}})
	if err != nil {
		t.Fatal(err)
	}
	event := ProviderEvent{PreviewNumber: 3, PreviewRef: "refs/merge-requests/3/head", Revision: strings.Repeat("e", 40), Author: "dev", HeadRepository: "platform/shop"}
	if _, _, err := f.automation.EnsurePreview(t.Context(), &created.Trigger, event); !errors.Is(err, ErrPreviewApproval) {
		t.Fatalf("unreviewed preview = %v", err)
	}
	approvals, err := f.automation.ListPreviewApprovals(t.Context(), f.projectID)
	if err != nil || len(approvals) != 1 {
		t.Fatalf("approvals = %+v, %v", approvals, err)
	}
	if _, err := f.automation.ApprovePreview(t.Context(), f.projectID, approvals[0].ID, event.Revision, "admin"); err != nil {
		t.Fatal(err)
	}
	preview, _, err := f.automation.EnsurePreview(t.Context(), &created.Trigger, event)
	if err != nil {
		t.Fatal(err)
	}
	runs := NewOrchestrationStore(f.store)
	commenter := NewPullRequestCommenter(runs, nil, func() string { return "https://dash.example.test" }, nil).WithForges(NewForgeClient(plans))
	commenter.now = func() time.Time { return time.Date(2026, 10, 26, 9, 30, 0, 0, time.UTC) }
	run := EngineRun{ID: 61, RunNumber: 2, ProjectID: f.projectID, EnvironmentID: preview.EnvironmentID, State: RunPreparing, Operation: OperationPreviewCreate, SourceRevision: event.Revision}
	commenter.RunStarted(t.Context(), run)
	run.State = RunSucceeded
	commenter.RunFinished(t.Context(), run)
	writes := forge.writes()
	if len(writes) != 2 || writes[0].Method != http.MethodPost || writes[0].Path != "/api/v4/projects/platform%2Fshop/merge_requests/3/notes" ||
		writes[1].Method != http.MethodPut || !strings.Contains(writes[1].Body["body"].(string), "✅ Ready") {
		t.Fatalf("writes = %+v", writes)
	}
	reports, err := runs.forgeReports(t.Context(), f.environmentID)
	if err != nil || reports[forgeReportComment] == nil || !reports[forgeReportComment].OK || reports[forgeReportComment].State != "ready" {
		t.Fatalf("the comment's outcome belongs to the environment that owns the trigger: %+v, %v", reports, err)
	}

	// A hook for one repository must not comment on another one's request.
	if _, err := f.store.DB.Exec(`UPDATE deploy_triggers SET config_json=json_set(config_json,'$.repository','platform/other') WHERE id=?`, created.Trigger.ID); err != nil {
		t.Fatal(err)
	}
	run.ID = 62
	commenter.RunFinished(t.Context(), run)
	if len(forge.writes()) != 2 {
		t.Fatal("commented on a request of another repository")
	}
	reports, _ = runs.forgeReports(t.Context(), f.environmentID)
	if reports[forgeReportComment].OK || !strings.Contains(reports[forgeReportComment].Message, "platform/other") {
		t.Fatalf("mismatch report = %+v", reports[forgeReportComment])
	}
}

// The Credentials page says which projects a provider token writes for, by
// the rule a run uses to pick it: never over a source's own HTTPS token, and
// only the newest token for the host.
func TestCredentialListSaysWhichProjectsAProviderTokenWritesFor(t *testing.T) {
	f, plans := forgeTokenFixture(t)
	ctx := t.Context()
	setSourceRemote(t, f, f.environmentID, "https://gitlab.example.test/platform/shop.git", "")
	older, err := plans.CreateCredential(ctx, CredentialCreateRequest{Name: "gitlab-old", Kind: CredentialProviderToken, Target: "gitlab.example.test", Secret: "old-token-123456"})
	if err != nil {
		t.Fatal(err)
	}
	// Saved ten seconds before the other, so "newest" is the save time and
	// not merely the higher id.
	if _, err := f.store.DB.Exec(`UPDATE deploy_credentials SET updated_at = updated_at - 10 WHERE id = ?`, older.ID); err != nil {
		t.Fatal(err)
	}
	newer, err := plans.CreateCredential(ctx, CredentialCreateRequest{Name: "gitlab-api", Kind: CredentialProviderToken, Target: "gitlab.example.test", Secret: "new-token-123456"})
	if err != nil {
		t.Fatal(err)
	}
	reportsFor := func(id int64) []int64 {
		t.Helper()
		summary, err := plans.GetCredential(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return summary.ReportsForProjectIDs
	}
	if got := reportsFor(newer.ID); len(got) != 1 || got[0] != f.projectID {
		t.Fatalf("newest token writes for %v", got)
	}
	if got := reportsFor(older.ID); len(got) != 0 {
		t.Fatalf("older token writes for %v", got)
	}
	listed, err := plans.ListCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range listed {
		if summary.UsedBy != 0 {
			t.Fatalf("a status token is not a source's credential: %+v", summary)
		}
	}
	if err := plans.DeleteCredential(ctx, newer.ID); err != nil {
		t.Fatalf("a token that only writes statuses can be removed: %v", err)
	}
	if got := reportsFor(older.ID); len(got) != 1 {
		t.Fatalf("the remaining token takes over: %v", got)
	}
}
