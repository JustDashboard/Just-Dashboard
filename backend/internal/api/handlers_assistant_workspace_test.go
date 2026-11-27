package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gitx"
)

// The assistant working on a deployment's code: the workspace, the file tools and
// the push, against a real checkout whose origin is a bare repository on disk.

// gitIn runs git in dir for a test's own setup, with no account configuration.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Seed", "-c", "user.email=seed@example.test", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "gitconfig"), "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// workspaceDeployment seeds a deployment built from a checkout on the server,
// whose origin is a bare repository, under roots the Files page and Local Git
// both reach. It answers the deployment's id, the checkout and the bare remote.
func workspaceDeployment(t *testing.T, f *operatorFixture) (int64, string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := f.s.Cfg.FileRoots[0]
	bare := filepath.Join(root, "remote.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, bare, "init", "--bare", "-q")
	gitIn(t, root, "clone", "-q", bare, "app")
	repo := filepath.Join(root, "app")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# app\n\nThe first version.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "Begin")
	gitIn(t, repo, "push", "-q", "origin", "HEAD:refs/heads/main")
	gitIn(t, repo, "branch", "--set-upstream-to=origin/main")
	head := gitIn(t, repo, "rev-parse", "HEAD")
	projectID, _ := seedCommittedDeployment(t, f.s, "app",
		deploy.DraftSourceConfig{Kind: deploy.SourceGit, Mode: deploy.SourceModeLocalCheckout, LocalPath: repo, Ref: "main"},
		deploy.SourceIdentity{Kind: deploy.SourceGit, Revision: head, LocalPath: repo, Ref: "main"})
	f.s.modules.git = gitx.New([]string{root})
	return projectID, repo, bare
}

func calledLines(events []map[string]any) []map[string]any { return ofType(events, "called") }

func TestTheAssistantChangesADeploymentsCodeAndPushesIt(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	f := operatorRouterAs(t, auth.RoleAdmin)
	projectID, repo, bare := workspaceDeployment(t, f)
	readme := filepath.Join(repo, "README.md")
	hello := filepath.Join(repo, "src", "hello.txt")
	f.provider.queue(
		claudeCalls("Opening it.", [2]any{"open_workspace", map[string]any{"reason": "to change its code"}}),
		claudeCalls("Writing.", [2]any{"write_file", map[string]any{"path": hello, "content": "  hello, indented\n", "reason": "a new file"}}),
		claudeCalls("Editing.", [2]any{"edit_file", map[string]any{"path": readme, "old_text": "The first version.", "new_text": "The second version.", "reason": "say what changed"}}),
		claudeCalls("Reading.", [2]any{"read_file", map[string]any{"path": readme}}),
		claudeCalls("Pushing.", [2]any{"push_changes", map[string]any{"folder": repo, "message": "Say hello", "reason": "ship it"}}),
		claudeAnswer("Pushed."),
	)
	rec := f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", projectID), `{"question":"Add a hello file and push it","mode":"full"}`)
	events := lines(t, rec)
	if done := doneOf(t, events); done["pending"] != nil {
		t.Fatalf("full access waited: %v", done)
	}
	called := calledLines(events)
	if len(called) != 5 {
		t.Fatalf("called %d tools: %v", len(called), events)
	}
	for i, line := range called {
		if line["ok"] != true {
			t.Fatalf("call %d failed: %v", i, line)
		}
	}
	// The new file keeps its leading spaces, and its folder was made for it.
	if raw, err := os.ReadFile(hello); err != nil || string(raw) != "  hello, indented\n" {
		t.Fatalf("hello.txt = %q, %v", raw, err)
	}
	if out, _ := called[2]["output"].(string); !strings.Contains(out, "-The first version.") || !strings.Contains(out, "+The second version.") {
		t.Fatalf("the edit's diff = %q", out)
	}
	// The model reads lines with their numbers.
	model := f.provider.lastToolResults(t)
	if !strings.Contains(model, "3\tThe second version.") {
		t.Fatalf("the read the model got: %s", model)
	}
	if subject := gitIn(t, bare, "log", "-1", "--format=%s", "main"); subject != "Say hello" {
		t.Fatalf("the remote's last commit is %q", subject)
	}
	if files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main"); !strings.Contains(files, "src/hello.txt") {
		t.Fatalf("the remote holds %s", files)
	}
	if detail, _ := f.audit(t, "deploy.assistant.ask"); !strings.Contains(detail, `"git":["open `+repo) || !strings.Contains(detail, "push "+repo+" to main") {
		t.Fatalf("audit = %s", detail)
	}
	if out, _ := called[4]["output"].(string); !strings.Contains(out, "builds this folder's committed state") {
		t.Fatalf("the push did not say how it deploys: %q", out)
	}
}

func TestAMaskedSecretIsNeverWrittenBackOverTheRealOne(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	f := operatorRouterAs(t, auth.RoleAdmin)
	projectID, repo, _ := workspaceDeployment(t, f)
	env := filepath.Join(repo, ".env")
	if err := os.WriteFile(env, []byte("TOKEN=real-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.provider.queue(
		claudeCalls("Rewriting.", [2]any{"write_file", map[string]any{"path": env, "content": "TOKEN=[REDACTED]\nDEBUG=1\n", "reason": "turn on debug"}}),
		claudeCalls("Editing.", [2]any{"edit_file", map[string]any{"path": env, "old_text": "TOKEN=[REDACTED]", "new_text": "TOKEN=[REDACTED]\nDEBUG=1", "reason": "turn on debug"}}),
		claudeAnswer("I could not."),
	)
	events := lines(t, f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", projectID), `{"question":"Turn on debug","mode":"full"}`))
	for _, line := range calledLines(events) {
		if line["ok"] == true {
			t.Fatalf("a masked value was written: %v", line)
		}
	}
	if raw, _ := os.ReadFile(env); string(raw) != "TOKEN=real-value\n" {
		t.Fatalf(".env = %q", raw)
	}
}

func TestAnEditThatMatchesTwiceSaysWhere(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	projectID, repo, _ := workspaceDeployment(t, f)
	file := filepath.Join(repo, "twice.txt")
	if err := os.WriteFile(file, []byte("a\nsame\nb\nsame\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.provider.queue(
		claudeCalls("Editing.", [2]any{"edit_file", map[string]any{"path": file, "old_text": "same", "new_text": "other", "reason": "x"}}),
		claudeAnswer("It matched twice."),
	)
	lines(t, f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", projectID), `{"question":"Change it","mode":"full"}`))
	if got := f.provider.lastToolResults(t); !strings.Contains(got, "occurs 2 times") || !strings.Contains(got, "lines 2, 4") {
		t.Fatalf("the model was told: %s", got)
	}
}

// In the default mode the code tools are changes, and wait for the person.
func TestCodeChangesWaitForThePersonInTheDefaultMode(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	projectID, repo, _ := workspaceDeployment(t, f)
	f.provider.queue(claudeCalls("Writing.", [2]any{"write_file", map[string]any{"path": filepath.Join(repo, "x.txt"), "content": "x", "reason": "x"}}))
	events := lines(t, f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", projectID), `{"question":"Write x","mode":"changes"}`))
	pending, _ := doneOf(t, events)["pending"].([]any)
	if len(pending) != 1 {
		t.Fatalf("pending = %v", events)
	}
	view, _ := json.Marshal(pending[0])
	if !strings.Contains(string(view), `"kind":"change"`) || !strings.Contains(string(view), `"content":"x"`) {
		t.Fatalf("the waiting call = %s", view)
	}
	if _, err := os.Stat(filepath.Join(repo, "x.txt")); err == nil {
		t.Fatal("the file was written before the person allowed it")
	}
}

func TestLineDiffShowsTheOneRegionThatChanged(t *testing.T) {
	before := "1\n2\n3\n4\n5\n6\n7\n8\n"
	after := "1\n2\n3\n4\nfive\n6\n7\n8\n"
	got := lineDiff(before, after, 2, 50)
	want := "@@ -3,5 +3,5 @@\n 3\n 4\n-5\n+five\n 6\n 7"
	if got != want {
		t.Fatalf("diff =\n%s\nwant\n%s", got, want)
	}
	if got := lineDiff("", "a\nb\n", 3, 50); got != "@@ -1,0 +1,2 @@\n+a\n+b" {
		t.Fatalf("a new file's diff = %q", got)
	}
	if got := lineDiff("", strings.Repeat("x\n", 10), 3, 4); !strings.HasSuffix(got, "… 7 more lines of the diff") {
		t.Fatalf("a long diff = %q", got)
	}
}

func TestWorkspaceNamesAndRemotes(t *testing.T) {
	if got := workspaceName(&deploy.DeploymentSummary{ID: 3, Name: "My Shop", EnvironmentKind: deploy.EnvironmentProduction, EnvironmentName: "production"}); got != "my-shop" {
		t.Fatalf("production's workspace = %q", got)
	}
	if got := workspaceName(&deploy.DeploymentSummary{ID: 3, Name: "shop", EnvironmentKind: "staging", EnvironmentName: "staging"}); got != "shop-staging" {
		t.Fatalf("staging's workspace = %q", got)
	}
	if got := workspaceName(&deploy.DeploymentSummary{ID: 3, Name: "..", EnvironmentKind: deploy.EnvironmentProduction}); got != "deployment-3" {
		t.Fatalf("an unusable name's workspace = %q", got)
	}
	for remote, want := range map[string]string{
		"https://github.com/acme/shop.git": "acme/shop",
		"https://github.com/acme/shop":     "acme/shop",
		"git@github.com:acme/shop.git":     "acme/shop",
		"https://gitlab.com/acme/shop.git": "",
	} {
		owner, name, ok := githubRepository(remote)
		if got := owner + "/" + name; ok && got != want || !ok && want != "" {
			t.Fatalf("%s read as %q (%v)", remote, got, ok)
		}
	}
	if !sameRemote("https://github.com/Acme/Shop.git", "https://github.com/acme/shop") || sameRemote("", "") {
		t.Fatal("sameRemote")
	}
}

// lastToolResults is every tool result the model was sent in the last request,
// as text.
func (p *fakeProvider) lastToolResults(t *testing.T) string {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.raw) == 0 {
		t.Fatal("the model was never asked")
	}
	var body struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(p.raw[len(p.raw)-1]), &body); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, message := range body.Messages {
		var blocks []struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(message.Content, &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if block.Type != "tool_result" {
				continue
			}
			var text string
			if json.Unmarshal(block.Content, &text) == nil {
				out = append(out, text)
				continue
			}
			var parts []struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(block.Content, &parts)
			for _, part := range parts {
				out = append(out, part.Text)
			}
		}
	}
	return strings.Join(out, "\n")
}

// gitOverHTTPS serves the bare repositories under base as git's smart HTTP does,
// over TLS, and answers 401 to a request without the token given.
func gitOverHTTPS(t *testing.T, base, token string) *httptest.Server {
	t.Helper()
	execPath := gitIn(t, base, "--exec-path")
	backend := &cgi.Handler{
		Path: filepath.Join(execPath, "git-http-backend"),
		Env:  []string{"GIT_PROJECT_ROOT=" + base, "GIT_HTTP_EXPORT_ALL=1", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"},
	}
	want := deploy.GitBasicAuthorization("127.0.0.1", token)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// A push asks the backend to receive, which it refuses unless asked.
		backend.Env = append(backend.Env[:4:4], "REMOTE_USER=deploy")
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestAWorkspaceIsClonedAndPushedWithTheDeploymentsCredential(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	// The test server's certificate is its own.
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
	f := operatorRouterAs(t, auth.RoleAdmin)
	root := f.s.Cfg.FileRoots[0]
	base := filepath.Join(t.TempDir(), "served")
	bare := filepath.Join(base, "acme", "shop.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, bare, "init", "--bare", "-q")
	gitIn(t, bare, "config", "http.receivepack", "true")
	seed := filepath.Join(t.TempDir(), "seed")
	gitIn(t, filepath.Dir(seed), "clone", "-q", bare, "seed")
	if err := os.WriteFile(filepath.Join(seed, "app.js"), []byte("console.log('one')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, seed, "add", "-A")
	gitIn(t, seed, "commit", "-q", "-m", "Begin")
	gitIn(t, seed, "push", "-q", "origin", "HEAD:refs/heads/main")

	const token = "ghp_workspace_test_token_0123456789"
	server := gitOverHTTPS(t, base, token)
	credential, err := f.s.modules.deployPlanning.CreateCredential(context.Background(), deploy.CredentialCreateRequest{
		Name: "shop-token", Kind: deploy.CredentialGitBearer, Target: "127.0.0.1", Secret: token,
	})
	if err != nil {
		t.Fatal(err)
	}
	remote := server.URL + "/acme/shop.git"
	projectID, environmentID := seedCommittedDeployment(t, f.s, "shop-two",
		deploy.DraftSourceConfig{Kind: deploy.SourceGit, Mode: deploy.SourceModeGitURL, URL: remote, Ref: "main", CredentialID: credential.ID},
		deploy.SourceIdentity{Kind: deploy.SourceGit, Revision: strings.Repeat("a", 40), Ref: "main"})
	_ = environmentID
	f.s.modules.git = gitx.New([]string{root})
	f.s.workspaceHomeOverride = root
	folder := filepath.Join(root, workspaceFolder, "shop-two")
	f.provider.queue(
		claudeCalls("Opening it.", [2]any{"open_workspace", map[string]any{"reason": "fix it"}}),
		claudeCalls("Fixing.", [2]any{"edit_file", map[string]any{"path": filepath.Join(folder, "app.js"), "old_text": "'one'", "new_text": "'two'", "reason": "fix"}}),
		claudeCalls("Pushing.", [2]any{"push_changes", map[string]any{"folder": folder, "message": "Print two", "reason": "ship"}}),
		claudeAnswer("Done."),
	)
	events := lines(t, f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", projectID), `{"question":"Make it print two and ship it","mode":"full"}`))
	called := calledLines(events)
	if len(called) != 3 {
		t.Fatalf("called %d tools: %v", len(called), events)
	}
	for i, line := range called {
		if line["ok"] != true {
			t.Fatalf("call %d failed: %v", i, line)
		}
	}
	if subject := gitIn(t, bare, "log", "-1", "--format=%s", "main"); subject != "Print two" {
		t.Fatalf("the remote's last commit is %q", subject)
	}
	// Nothing of the token was kept in the checkout, or told to the model.
	config, _ := os.ReadFile(filepath.Join(folder, ".git", "config"))
	if strings.Contains(string(config), token) || strings.Contains(string(config), "Authorization") {
		t.Fatalf("the checkout kept the credential: %s", config)
	}
	told := f.provider.lastToolResults(t)
	if strings.Contains(told, token) || !strings.Contains(told, "with the deployment's saved credential shop-token") {
		t.Fatalf("the model was told: %s", told)
	}
	if !strings.Contains(told, "does not deploy a push on its own") && !strings.Contains(told, "deploys a push to main on its own") {
		t.Fatalf("the push did not say whether it deploys: %s", told)
	}
	// Opened again, the workspace is fetched rather than cloned, and keeps what changed.
	if err := os.WriteFile(filepath.Join(folder, "notes.txt"), []byte("kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.provider.queue(claudeCalls("Again.", [2]any{"open_workspace", map[string]any{}}), claudeAnswer("Done."))
	lines(t, f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", projectID), `{"question":"Open it again","mode":"full"}`))
	if again := f.provider.lastToolResults(t); !strings.Contains(again, "already there") || !strings.Contains(again, "notes.txt") {
		t.Fatalf("opened again: %s", again)
	}
}
