package api

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gitx"
)

// credentialedWorkspace seeds a deployment of a repository served over HTTPS
// with a saved token, its workspace kept under the fixture's file root. It
// answers the deployment, the bare repository, the workspace's folder, the
// remote and the token.
func credentialedWorkspace(t *testing.T, f *operatorFixture, name string) (int64, string, string, string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
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
	token := "ghp_" + strings.ReplaceAll(name, "-", "_") + "_token_0123456789"
	server := gitOverHTTPS(t, base, token)
	credential, err := f.s.modules.deployPlanning.CreateCredential(context.Background(), deploy.CredentialCreateRequest{
		Name: name + "-token", Kind: deploy.CredentialGitBearer, Target: "127.0.0.1", Secret: token,
	})
	if err != nil {
		t.Fatal(err)
	}
	remote := server.URL + "/acme/shop.git"
	projectID, _ := seedCommittedDeployment(t, f.s, name,
		deploy.DraftSourceConfig{Kind: deploy.SourceGit, Mode: deploy.SourceModeGitURL, URL: remote, Ref: "main", CredentialID: credential.ID},
		deploy.SourceIdentity{Kind: deploy.SourceGit, Revision: strings.Repeat("a", 40), Ref: "main"})
	f.s.modules.git = gitx.New([]string{root})
	f.s.workspaceHomeOverride = root
	return projectID, bare, filepath.Join(root, workspaceFolder, name), remote, token
}

// interceptingProxy is an HTTPS proxy that opens what is sent through it with a
// certificate of its own, as one whose CA a checkout was told to trust (or told
// not to check) would, and keeps every Authorization header it sees.
func interceptingProxy(t *testing.T) (string, func() string) {
	t.Helper()
	cert := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(cert.Close)
	var mu sync.Mutex
	seen := []string{}
	keep := func(r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.String()+" "+r.Header.Get("Authorization"))
		mu.Unlock()
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				req, err := http.ReadRequest(bufio.NewReader(conn))
				if err != nil {
					return
				}
				keep(req)
				if req.Method != http.MethodConnect {
					_, _ = conn.Write([]byte("HTTP/1.1 401 Unauthorized\r\nWWW-Authenticate: Basic realm=\"git\"\r\nContent-Length: 0\r\n\r\n"))
					return
				}
				_, _ = conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
				inner := tls.Server(conn, cert.TLS)
				reader := bufio.NewReader(inner)
				for {
					req, err := http.ReadRequest(reader)
					if err != nil {
						return
					}
					keep(req)
					_, _ = inner.Write([]byte("HTTP/1.1 401 Unauthorized\r\nWWW-Authenticate: Basic realm=\"git\"\r\nContent-Length: 0\r\n\r\n"))
				}
			}(conn)
		}
	}()
	return "http://" + ln.Addr().String(), func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(seen, "\n")
	}
}

// A credential of the dashboard's never reaches a git that reads the
// checkout's configuration or its account's: a proxy, a CA the checkout trusts
// or checks turned off, a URL rewrite, set in the checkout's .git/config or in
// the account's own ~/.gitconfig, sees no Authorization, and the clone, the
// fetch and the push still reach the remote.
func TestAWorkspacesConfigurationNeverSeesTheCredential(t *testing.T) {
	account := filepath.Join(t.TempDir(), "account-gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", account)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	// The test server's certificate is its own: the dashboard's own setting.
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
	f := operatorRouterAs(t, auth.RoleAdmin)
	projectID, bare, folder, remote, token := credentialedWorkspace(t, f, "shop-relay")
	proxy, seen := interceptingProxy(t)
	// The account's own configuration sends every https request through the
	// proxy, unchecked.
	if err := os.WriteFile(account, []byte("[http]\n\tproxy = "+proxy+"\n\tsslVerify = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.provider.queue(claudeCalls("Opening it.", [2]any{"open_workspace", map[string]any{"reason": "fix it"}}), claudeAnswer("Open."))
	events := lines(t, f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", projectID), `{"question":"Open it","mode":"full"}`))
	if called := calledLines(events); len(called) != 1 || called[0]["ok"] != true {
		t.Fatalf("open_workspace = %v", events)
	}
	// And the checkout's own: a proxy for its remote, a CA of the proxy's, and
	// its remote rewritten to the proxy.
	gitIn(t, folder, "config", "http."+remote+".proxy", proxy)
	gitIn(t, folder, "config", "http.sslCAInfo", filepath.Join(t.TempDir(), "proxy-ca.pem"))
	gitIn(t, folder, "config", "url."+proxy+"/acme/shop.git.insteadOf", remote)
	f.provider.queue(
		claudeCalls("Again.", [2]any{"open_workspace", map[string]any{}}),
		claudeCalls("Fixing.", [2]any{"edit_file", map[string]any{"path": filepath.Join(folder, "app.js"), "old_text": "'one'", "new_text": "'two'", "reason": "fix"}}),
		claudeCalls("Pushing.", [2]any{"push_changes", map[string]any{"folder": folder, "message": "Print two", "reason": "ship"}}),
		claudeAnswer("Done."),
	)
	events = lines(t, f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", projectID), `{"question":"Make it print two and ship it","mode":"full"}`))
	for i, line := range calledLines(events) {
		if line["ok"] != true {
			t.Fatalf("call %d failed: %v", i, line)
		}
	}
	if subject := gitIn(t, bare, "log", "-1", "--format=%s", "main"); subject != "Print two" {
		t.Fatalf("the remote's last commit is %q", subject)
	}
	if got := seen(); strings.Contains(got, "Basic ") || strings.Contains(got, token) {
		t.Fatalf("the proxy saw the credential:\n%s", got)
	}
	// The checkout's remote-tracking branch says where the push went.
	if got, want := gitIn(t, folder, "rev-parse", "refs/remotes/origin/main"), gitIn(t, bare, "rev-parse", "main"); got != want {
		t.Fatalf("origin/main = %s after the push, the remote's is %s", got, want)
	}
	// The relay is the dashboard's own, in its data folder.
	if entries, err := os.ReadDir(filepath.Join(f.s.Cfg.DataDir, gitRelayFolder, "relays")); err != nil || len(entries) != 1 {
		t.Fatalf("the relays = %v, %v", entries, err)
	}
}

// Two deployments whose names read the same as a folder, of one repository, each
// get a workspace of their own: the second gives way when the folder's marker
// names the first, rather than taking its checkout, branch and credential.
func TestTwoDeploymentsOfOneNameAndRemoteKeepTheirOwnWorkspaces(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
	f := operatorRouterAs(t, auth.RoleAdmin)
	first, _, folder, remote, token := credentialedWorkspace(t, f, "twin")
	credential, err := f.s.modules.deployPlanning.CreateCredential(context.Background(), deploy.CredentialCreateRequest{
		Name: "twin-token-two", Kind: deploy.CredentialGitBearer, Target: "127.0.0.1", Secret: token,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, secondEnvironment := seedCommittedDeployment(t, f.s, "Twin",
		deploy.DraftSourceConfig{Kind: deploy.SourceGit, Mode: deploy.SourceModeGitURL, URL: remote, Ref: "main", CredentialID: credential.ID},
		deploy.SourceIdentity{Kind: deploy.SourceGit, Revision: strings.Repeat("b", 40), Ref: "main"})
	for _, project := range []int64{first, second} {
		f.provider.queue(claudeCalls("Opening it.", [2]any{"open_workspace", map[string]any{"reason": "work"}}), claudeAnswer("Open."))
		events := lines(t, f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", project), `{"question":"Open it","mode":"full"}`))
		if called := calledLines(events); len(called) != 1 || called[0]["ok"] != true {
			t.Fatalf("open_workspace of %d = %v", project, events)
		}
	}
	own := folder + "-" + fmt.Sprint(secondEnvironment)
	if got := f.provider.lastToolResults(t); !strings.Contains(got, "Workspace: "+own+"\n") || strings.Contains(got, "already there") {
		t.Fatalf("the second deployment was told: %s", got)
	}
	if fields := readWorkspaceMarker(folder); len(fields) != 2 || fields[0] != fmt.Sprint(first) {
		t.Fatalf("the first workspace's marker now reads %v", fields)
	}
	if fields := readWorkspaceMarker(own); len(fields) != 2 || fields[0] != fmt.Sprint(second) {
		t.Fatalf("the second workspace's marker reads %v", fields)
	}
}
