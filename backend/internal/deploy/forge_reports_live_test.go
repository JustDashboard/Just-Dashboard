package deploy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// Commit statuses and preview comments written to the real servers, each in
// a container on loopback, and read back through the server's own API: a
// Gitea, a Forgejo and (with JD_FORGE_LIVE_GITLAB=1, because it takes a few
// minutes to start) a GitLab. Each gets a pull or merge request, a token with
// the scopes the documentation names and one with too few, and the client
// finds the host's software by asking it, as it does for any self-hosted
// address.
func TestLiveForgesReceiveStatusesAndOneCommentPerPreview(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to run Gitea and Forgejo containers (and JD_FORGE_LIVE_GITLAB=1 for GitLab)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	for _, variant := range []struct{ name, image, binary, envPrefix string }{
		{"gitea", "gitea/gitea:1.24", "gitea", "GITEA"},
		{"forgejo", "codeberg.org/forgejo/forgejo:11", "forgejo", "FORGEJO"},
	} {
		t.Run(variant.name, func(t *testing.T) {
			env := func(key, value string) []string {
				return []string{"--env", variant.envPrefix + "__" + key + "=" + value}
			}
			args := append(append(append(env("security__INSTALL_LOCK", "true"), env("database__DB_TYPE", "sqlite3")...),
				env("server__DISABLE_SSH", "true")...), variant.image)
			name := liveContainer(t, ctx, 3000, args...)
			base := liveWaitFor(t, ctx, name, 3000, "/api/v1/version")
			if _, err := liveDockerOutput(ctx, "exec", "-u", "git", name, variant.binary, "admin", "user", "create", "--username", "ops",
				"--password", "live-password-1", "--email", "ops@example.test", "--admin", "--must-change-password=false"); err != nil {
				t.Fatal(err)
			}
			basic := func(method, path string, body any, result any) {
				t.Helper()
				liveJSON(t, ctx, method, base+path, func(r *http.Request) { r.SetBasicAuth("ops", "live-password-1") }, body, result)
			}
			token := func(name string, scopes ...string) string {
				var created struct {
					SHA1 string `json:"sha1"`
				}
				basic(http.MethodPost, "/api/v1/users/ops/tokens", map[string]any{"name": name, "scopes": scopes}, &created)
				return created.SHA1
			}
			full := token("statuses", "write:repository", "write:issue", "read:user")
			narrow := token("read-only", "read:repository")
			basic(http.MethodPost, "/api/v1/user/repos", map[string]any{"name": "shop", "auto_init": true, "default_branch": "main", "private": true}, nil)
			var file struct {
				Commit struct {
					SHA string `json:"sha"`
				} `json:"commit"`
			}
			basic(http.MethodPost, "/api/v1/repos/ops/shop/contents/feature.txt", map[string]any{
				"content": base64.StdEncoding.EncodeToString([]byte("preview me\n")), "message": "Add a feature", "branch": "main", "new_branch": "feature",
			}, &file)
			var pull struct {
				Number int `json:"number"`
			}
			basic(http.MethodPost, "/api/v1/repos/ops/shop/pulls", map[string]any{"head": "feature", "base": "main", "title": "Try the feature"}, &pull)
			sha := file.Commit.SHA

			host := strings.TrimPrefix(base, "http://")
			client, endpoint := liveForgeEndpoint(t, ctx, base+"/ops/shop.git", host, full)
			if endpoint.Provider != ForgeGitea || endpoint.Product != variant.name || endpoint.Repository != "ops/shop" {
				t.Fatalf("%s was taken for %+v", variant.name, endpoint)
			}
			liveWriteBack(t, ctx, client, endpoint, sha, pull.Number)

			var combined struct {
				State    string `json:"state"`
				Statuses []struct {
					Context   string `json:"context"`
					Status    string `json:"status"`
					TargetURL string `json:"target_url"`
				} `json:"statuses"`
			}
			basic(http.MethodGet, "/api/v1/repos/ops/shop/commits/"+sha+"/status", nil, &combined)
			if combined.State != "success" || len(combined.Statuses) != 1 || combined.Statuses[0].Context != "just-dashboard/production" ||
				combined.Statuses[0].TargetURL != "https://dash.example.test/deploy/4/runs/12" {
				t.Fatalf("%s holds %+v", variant.name, combined)
			}
			var comments []struct {
				Body string `json:"body"`
			}
			basic(http.MethodGet, fmt.Sprintf("/api/v1/repos/ops/shop/issues/%d/comments", pull.Number), nil, &comments)
			liveOneReadyComment(t, variant.name, len(comments), func(i int) string { return comments[i].Body })

			_, narrowEndpoint := liveForgeEndpoint(t, ctx, base+"/ops/shop.git", host, narrow)
			err := client.PostStatus(ctx, narrowEndpoint, sha, CommitStatus{State: "success", Context: "just-dashboard/production"})
			if err == nil || !strings.Contains(err.Error(), "(403)") || !strings.Contains(err.Error(), "write:repository") {
				t.Fatalf("%s read-only token = %v", variant.name, err)
			}
		})
	}

	t.Run("gitlab", func(t *testing.T) {
		if os.Getenv("JD_FORGE_LIVE_GITLAB") != "1" {
			t.Skip("set JD_FORGE_LIVE_GITLAB=1 to start a GitLab container (about five minutes)")
		}
		// GitLab's links and clone URLs carry external_url, so the port is
		// chosen here rather than by Docker.
		port := liveFreePort(t)
		name := "jd-live-gitlab-" + fmt.Sprint(time.Now().UnixNano())
		external := fmt.Sprintf("http://127.0.0.1:%d", port)
		if _, err := liveDockerOutput(ctx, "run", "--detach", "--name", name, "--shm-size", "256m", "--publish", fmt.Sprintf("127.0.0.1:%d:%d", port, port),
			"--env", "GITLAB_OMNIBUS_CONFIG=external_url '"+external+"'; prometheus_monitoring['enable']=false; puma['worker_processes']=2; sidekiq['concurrency']=5; registry['enable']=false; gitlab_kas['enable']=false",
			"gitlab/gitlab-ce:18.4.2-ce.0"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = liveDockerOutput(context.Background(), "rm", "--force", name) })
		for ctx.Err() == nil {
			if res, err := http.Get(external + "/users/sign_in"); err == nil {
				res.Body.Close()
				if res.StatusCode == http.StatusOK {
					break
				}
			}
			time.Sleep(3 * time.Second)
		}
		out, err := liveDockerOutput(ctx, "exec", name, "gitlab-rails", "runner", `
u = User.find_by_username('root')
puts 'FULL=' + u.personal_access_tokens.create!(name: 'statuses', scopes: [:api], expires_at: 2.days.from_now).token
puts 'NARROW=' + u.personal_access_tokens.create!(name: 'read-only', scopes: [:read_api], expires_at: 2.days.from_now).token`)
		if err != nil {
			t.Fatal(err)
		}
		full := regexp.MustCompile(`FULL=(\S+)`).FindStringSubmatch(string(out))
		narrow := regexp.MustCompile(`NARROW=(\S+)`).FindStringSubmatch(string(out))
		if full == nil || narrow == nil {
			t.Fatalf("no tokens in %q", out)
		}
		api := func(method, path string, body any, result any) {
			t.Helper()
			liveJSON(t, ctx, method, external+"/api/v4"+path, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+full[1]) }, body, result)
		}
		var group struct {
			ID int64 `json:"id"`
		}
		api(http.MethodPost, "/groups", map[string]any{"name": "platform", "path": "platform"}, &group)
		var subgroup struct {
			ID int64 `json:"id"`
		}
		api(http.MethodPost, "/groups", map[string]any{"name": "web", "path": "web", "parent_id": group.ID}, &subgroup)
		api(http.MethodPost, "/projects", map[string]any{"name": "shop", "namespace_id": subgroup.ID, "initialize_with_readme": true, "default_branch": "main"}, nil)
		project := url.PathEscape("platform/web/shop")
		api(http.MethodPost, "/projects/"+project+"/repository/files/feature.txt", map[string]any{
			"branch": "feature", "start_branch": "main", "content": "preview me\n", "commit_message": "Add a feature",
		}, nil)
		var branch struct {
			Commit struct {
				ID string `json:"id"`
			} `json:"commit"`
		}
		api(http.MethodGet, "/projects/"+project+"/repository/branches/feature", nil, &branch)
		var request struct {
			IID int `json:"iid"`
		}
		api(http.MethodPost, "/projects/"+project+"/merge_requests", map[string]any{"source_branch": "feature", "target_branch": "main", "title": "Try the feature"}, &request)

		host := strings.TrimPrefix(external, "http://")
		client, endpoint := liveForgeEndpoint(t, ctx, external+"/platform/web/shop.git", host, full[1])
		if endpoint.Provider != ForgeGitLab || endpoint.Product != "gitlab" || endpoint.Repository != "platform/web/shop" || endpoint.Origin != external {
			t.Fatalf("gitlab was taken for %+v", endpoint)
		}
		liveWriteBack(t, ctx, client, endpoint, branch.Commit.ID, request.IID)

		var statuses []struct {
			Name      string `json:"name"`
			Status    string `json:"status"`
			TargetURL string `json:"target_url"`
		}
		api(http.MethodGet, "/projects/"+project+"/repository/commits/"+branch.Commit.ID+"/statuses", nil, &statuses)
		if len(statuses) != 1 || statuses[0].Name != "just-dashboard/production" || statuses[0].Status != "success" ||
			statuses[0].TargetURL != "https://dash.example.test/deploy/4/runs/12" {
			t.Fatalf("gitlab holds %+v", statuses)
		}
		var notes []struct {
			Body   string `json:"body"`
			System bool   `json:"system"`
		}
		api(http.MethodGet, fmt.Sprintf("/projects/%s/merge_requests/%d/notes?per_page=100", project, request.IID), nil, &notes)
		var written []string
		for _, note := range notes {
			if !note.System {
				written = append(written, note.Body)
			}
		}
		liveOneReadyComment(t, "gitlab", len(written), func(i int) string { return written[i] })

		_, narrowEndpoint := liveForgeEndpoint(t, ctx, external+"/platform/web/shop.git", host, narrow[1])
		err = client.PostStatus(ctx, narrowEndpoint, branch.Commit.ID, CommitStatus{State: "success", Context: "just-dashboard/production"})
		if err == nil || !strings.Contains(err.Error(), "(403)") || !strings.Contains(err.Error(), "higher privileges") {
			t.Fatalf("gitlab read-only token = %v", err)
		}
	})
}

// liveForgeEndpoint saves token as a provider token for host in a fresh
// store and resolves the remote the way a run does.
func liveForgeEndpoint(t *testing.T, ctx context.Context, remote, host, token string) (*ForgeClient, ForgeEndpoint) {
	t.Helper()
	f := newAutomationFixture(t)
	sealer, err := auth.NewSealer(strings.Repeat("a7", 32))
	if err != nil {
		t.Fatal(err)
	}
	plans := NewPlanningStore(f.store, sealer, nil)
	if _, err := plans.CreateCredential(ctx, CredentialCreateRequest{Name: "forge-api", Kind: CredentialProviderToken, Target: host, Secret: token}); err != nil {
		t.Fatal(err)
	}
	client := NewForgeClient(plans)
	endpoint, err := client.Resolve(ctx, SourceIdentity{Kind: SourceGit, Remote: remote}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return client, endpoint
}

// liveWriteBack is a preview's life as the observers write it: a pending
// status and a Building comment, then success and Ready edited in place.
func liveWriteBack(t *testing.T, ctx context.Context, client *ForgeClient, endpoint ForgeEndpoint, sha string, number int) {
	t.Helper()
	status := CommitStatus{Context: "just-dashboard/production", TargetURL: "https://dash.example.test/deploy/4/runs/12"}
	marker := "<!-- just-dashboard:preview:12 -->"
	target := pullRequestTarget{environment: "pr-" + fmt.Sprint(number), url: "https://pr.example.test/"}
	run := EngineRun{RunNumber: 12, SourceRevision: sha}
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	status.State, status.Description = "pending", "Deployment in progress"
	if err := client.PostStatus(ctx, endpoint, sha, status); err != nil {
		t.Fatal(err)
	}
	if err := client.UpsertComment(ctx, endpoint, number, marker, renderPullRequestComment(target, run, "building", status.TargetURL, at)); err != nil {
		t.Fatal(err)
	}
	status.State, status.Description = "success", "Deployed successfully"
	if err := client.PostStatus(ctx, endpoint, sha, status); err != nil {
		t.Fatal(err)
	}
	if err := client.UpsertComment(ctx, endpoint, number, marker, renderPullRequestComment(target, run, "ready", status.TargetURL, at)); err != nil {
		t.Fatal(err)
	}
}

func liveOneReadyComment(t *testing.T, name string, count int, body func(int) string) {
	t.Helper()
	if count != 1 {
		t.Fatalf("%s holds %d comments, want one edited in place", name, count)
	}
	text := body(0)
	if !strings.HasPrefix(text, "<!-- just-dashboard:preview:12 -->") || !strings.Contains(text, "✅ Ready") || strings.Contains(text, "Building") ||
		!strings.Contains(text, "https://pr.example.test/") {
		t.Fatalf("%s comment = %q", name, text)
	}
}

func liveJSON(t *testing.T, ctx context.Context, method, target string, authorize func(*http.Request), body, result any) {
	t.Helper()
	var payload io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, payload)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	authorize(request)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode > 299 {
		t.Fatalf("%s %s = %d %s", method, target, response.StatusCode, raw)
	}
	if result != nil {
		if err := json.Unmarshal(raw, result); err != nil {
			t.Fatalf("%s %s: %v in %s", method, target, err, raw)
		}
	}
}

func liveFreePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
