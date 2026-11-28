package api

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/gitx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
)

// The GitHub page's clone of a private repository gives its token only to git of
// the dashboard's own: a proxy with checks off in the folder's account's own
// ~/.gitconfig sees no Authorization, and the clone still lands, shallow when
// asked, as a checkout git reads as one.
func TestCloneToServerNeverGivesTheTokenToTheAccountsGit(t *testing.T) {
	account := filepath.Join(t.TempDir(), "account-gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", account)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
	e := newGHEnv(t)
	e.createApp()
	base := "/api/v1/github/connections/" + e.connect(e.admin, 1001)

	served := t.TempDir()
	work := t.TempDir()
	runGit(t, work, "init", "-q", "-b", "main")
	for _, text := range []string{"one\n", "two\n", "three\n"} {
		if err := os.WriteFile(filepath.Join(work, "README.md"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, work, "add", "-A")
		runGit(t, work, "commit", "-qm", strings.TrimSpace(text))
	}
	if err := os.MkdirAll(filepath.Join(served, "octo-dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, served, "clone", "-q", "--bare", work, filepath.Join(served, "octo-dev", "app.git"))
	gitServer, seen := gitHTTPServer(t, served)
	e.s.modules.githubRepos.GitURL = gitServer.URL
	root := t.TempDir()
	e.s.modules.git = gitx.New([]string{root})
	proxy, proxied := interceptingProxy(t)
	if err := os.WriteFile(account, []byte("[http]\n\tproxy = "+proxy+"\n\tsslVerify = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, clone := range []struct{ name, body string }{
		{"whole", `{"parent":"` + root + `","name":"whole"}`},
		{"shallow", `{"parent":"` + root + `","name":"shallow","depth":1}`},
	} {
		w := e.admin.call(http.MethodPost, base+"/repos/octo-dev/app/clone", clone.body, nil)
		if w.Code != 202 {
			t.Fatalf("clone %s = %d %s", clone.name, w.Code, w.Body)
		}
		var start struct {
			JobID  string `json:"jobId"`
			Target string `json:"target"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &start)
		job, lines := waitForGitHubClone(t, e.s, start.JobID)
		text := []string{}
		for _, line := range lines {
			text = append(text, line.Text)
		}
		if job.Status != jobs.StatusSucceeded {
			t.Fatalf("the %s clone = %+v\n%s", clone.name, job, strings.Join(text, "\n"))
		}
		if data, err := os.ReadFile(filepath.Join(start.Target, "README.md")); err != nil || string(data) != "three\n" {
			t.Fatalf("the %s clone's README.md = %q %v", clone.name, data, err)
		}
		count := gitOut(t, start.Target, "rev-list", "--count", "HEAD")
		shallow := gitOut(t, start.Target, "rev-parse", "--is-shallow-repository")
		if clone.name == "whole" && (count != "3" || shallow != "false") || clone.name == "shallow" && (count != "1" || shallow != "true") {
			t.Fatalf("the %s clone has %s commits, shallow %s", clone.name, count, shallow)
		}
		if got := gitOut(t, start.Target, "rev-parse", "--abbrev-ref", "main@{upstream}"); got != "origin/main" {
			t.Fatalf("the %s clone's main tracks %q", clone.name, got)
		}
	}
	// The shallow clone's own relay is gone with it.
	if left, _ := os.ReadDir(filepath.Join(e.s.Cfg.DataDir, gitRelayFolder, "once")); len(left) != 0 {
		t.Fatalf("the shallow clone left its relay: %v", left)
	}
	if got := proxied(); strings.Contains(got, "Basic ") {
		t.Fatalf("the account's proxy saw the token:\n%s", got)
	}
	tokens := 0
	for _, header := range seen() {
		if header != "" {
			tokens++
		}
	}
	if tokens == 0 {
		t.Fatal("the git server never saw the token")
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
