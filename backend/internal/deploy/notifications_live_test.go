package deploy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The three kinds a server can be run for here, asked of the real thing: an
// ntfy server that denies anonymous publishing, a Gotify server and a
// Mattermost server, each in a container on loopback, sent a failed run
// through DeliverNotification with its default client, and read back through
// the server's own API. Pushover and Teams are hosted services only; their
// payloads are checked against their documented shapes in
// notifications_test.go.
func TestLivePushServersReceiveARunFailure(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to run ntfy, Gotify and Mattermost containers")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	f := newAutomationFixture(t)
	envelope := NotificationEnvelope{
		Event: NotificationEventFailed, State: string(RunFailed), RunID: 31, RunNumber: 31,
		ProjectName: "shop", EnvironmentName: "Production", Operation: "deploy", Trigger: "manual",
		TerminalReason: "the image did not build", URL: "https://dash.example.test/deploy/1/runs/31",
	}

	t.Run("ntfy", func(t *testing.T) {
		name := liveContainer(t, ctx, 80, "--env", "NTFY_AUTH_FILE=/tmp/auth.db", "--env", "NTFY_AUTH_DEFAULT_ACCESS=deny-all",
			"binwiederhier/ntfy:latest", "serve")
		base := liveWaitFor(t, ctx, name, 80, "/v1/health")
		if _, err := liveDockerOutput(ctx, "exec", "--env", "NTFY_PASSWORD=live-password", name, "ntfy", "user", "add", "--role=admin", "ops"); err != nil {
			t.Fatal(err)
		}
		out, err := liveDockerOutput(ctx, "exec", name, "ntfy", "token", "add", "ops")
		if err != nil {
			t.Fatal(err)
		}
		token := regexp.MustCompile(`tk_[A-Za-z0-9]{29}`).FindString(string(out))
		if token == "" {
			t.Fatalf("no token in %q", out)
		}

		anonymous, _, err := f.automation.CreateNotificationChannel(ctx, NotificationWrite{Name: "anonymous", Kind: NotificationKindNtfy, Config: NotificationConfig{ServerURL: base, Topic: "deploys"}, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.automation.DeliverNotification(ctx, nil, anonymous.ID, envelope); err == nil || !strings.Contains(err.Error(), "4xx") {
			t.Fatalf("a server that denies anonymous publishing took the message: %v", err)
		}
		channel, _, err := f.automation.CreateNotificationChannel(ctx, NotificationWrite{Name: "ntfy", Kind: NotificationKindNtfy, Config: NotificationConfig{ServerURL: base, Topic: "deploys", Token: token}, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.automation.DeliverNotification(ctx, nil, channel.ID, envelope); err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/deploys/json?poll=1", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var got []map[string]any
		for scanner := bufio.NewScanner(res.Body); scanner.Scan(); {
			var message map[string]any
			if json.Unmarshal(scanner.Bytes(), &message) == nil && message["event"] == "message" {
				got = append(got, message)
			}
		}
		if len(got) != 1 {
			t.Fatalf("ntfy holds %d messages: %+v", len(got), got)
		}
		message := got[0]
		if !strings.Contains(message["title"].(string), "Run #31 failed") || !strings.Contains(message["message"].(string), "Reason: the image did not build") ||
			message["priority"] != float64(4) || message["click"] != envelope.URL {
			t.Fatalf("ntfy stored %+v", message)
		}
		actions, _ := message["actions"].([]any)
		if len(actions) != 1 || actions[0].(map[string]any)["url"] != envelope.URL {
			t.Fatalf("ntfy actions = %+v", message["actions"])
		}
	})

	t.Run("gotify", func(t *testing.T) {
		name := liveContainer(t, ctx, 80, "--env", "GOTIFY_DEFAULTUSER_PASS=live-password", "gotify/server:latest")
		base := liveWaitFor(t, ctx, name, 80, "/health")
		admin := func(method, path string, body any) *http.Response {
			t.Helper()
			encoded, _ := json.Marshal(body)
			req, _ := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(encoded))
			req.SetBasicAuth("admin", "live-password")
			req.Header.Set("Content-Type", "application/json")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			return res
		}
		res := admin(http.MethodPost, "/application", map[string]any{"name": "Just Dashboard"})
		var app struct{ Token string }
		if err := json.NewDecoder(res.Body).Decode(&app); err != nil || app.Token == "" {
			t.Fatalf("application = %+v (%v)", app, err)
		}
		res.Body.Close()

		channel, _, err := f.automation.CreateNotificationChannel(ctx, NotificationWrite{Name: "gotify", Kind: NotificationKindGotify, Config: NotificationConfig{ServerURL: base, Token: app.Token}, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.automation.DeliverNotification(ctx, nil, channel.ID, envelope); err != nil {
			t.Fatal(err)
		}
		res = admin(http.MethodGet, "/message", nil)
		defer res.Body.Close()
		var page struct {
			Messages []struct {
				Title, Message string
				Priority       int
				Extras         map[string]any
			}
		}
		if err := json.NewDecoder(res.Body).Decode(&page); err != nil || len(page.Messages) != 1 {
			t.Fatalf("gotify holds %+v (%v)", page, err)
		}
		message := page.Messages[0]
		if !strings.Contains(message.Title, "Run #31 failed") || !strings.Contains(message.Message, "Open the run: "+envelope.URL) || message.Priority != 8 ||
			message.Extras["client::notification"] == nil {
			t.Fatalf("gotify stored %+v", message)
		}
	})

	t.Run("mattermost", func(t *testing.T) {
		name := liveContainer(t, ctx, 8065, "mattermost/mattermost-preview:latest")
		base := liveWaitFor(t, ctx, name, 8065, "/api/v4/system/ping")
		var session string
		call := func(method, path string, body any, into any) http.Header {
			t.Helper()
			encoded, _ := json.Marshal(body)
			req, _ := http.NewRequestWithContext(ctx, method, base+"/api/v4"+path, bytes.NewReader(encoded))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
			if session != "" {
				req.Header.Set("Authorization", "Bearer "+session)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode >= 300 {
				raw, _ := io.ReadAll(res.Body)
				t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, raw)
			}
			if into != nil {
				if err := json.NewDecoder(res.Body).Decode(into); err != nil {
					t.Fatalf("%s %s: %v", method, path, err)
				}
			}
			return res.Header
		}
		// The first account on a fresh server is its administrator.
		call(http.MethodPost, "/users", map[string]any{"email": "ops@example.test", "username": "ops", "password": "Live-password-1!"}, nil)
		session = call(http.MethodPost, "/users/login", map[string]any{"login_id": "ops", "password": "Live-password-1!"}, nil).Get("Token")
		var team struct{ ID string }
		call(http.MethodPost, "/teams", map[string]any{"name": "ops", "display_name": "Ops", "type": "O"}, &team)
		var town struct{ ID string }
		call(http.MethodGet, "/teams/"+team.ID+"/channels/name/town-square", nil, &town)
		var hook struct{ ID string }
		call(http.MethodPost, "/hooks/incoming", map[string]any{"channel_id": town.ID, "display_name": "Just Dashboard"}, &hook)

		channel, _, err := f.automation.CreateNotificationChannel(ctx, NotificationWrite{Name: "mattermost", Kind: NotificationKindMattermost, Config: NotificationConfig{WebhookURL: base + "/hooks/" + hook.ID}, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.automation.DeliverNotification(ctx, nil, channel.ID, envelope); err != nil {
			t.Fatal(err)
		}
		var posts struct {
			Order []string
			Posts map[string]struct {
				Props struct {
					Attachments []struct {
						Title, Text, Color string
						TitleLink          string `json:"title_link"`
						Fields             []struct{ Title, Value string }
					}
				}
			}
		}
		call(http.MethodGet, "/channels/"+town.ID+"/posts", nil, &posts)
		var found bool
		for _, post := range posts.Posts {
			for _, attachment := range post.Props.Attachments {
				if strings.Contains(attachment.Title, "Run #31 failed") && attachment.TitleLink == envelope.URL && attachment.Color == "#ef4444" && len(attachment.Fields) > 0 {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("town-square holds no attachment from the dashboard: %+v", posts)
		}
	})
}

// liveContainer runs an image on a loopback port Docker picks, removed when
// the test ends.
func liveContainer(t *testing.T, ctx context.Context, port int, args ...string) string {
	t.Helper()
	name := "jd-live-notify-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	run := append([]string{"run", "--detach", "--name", name, "--publish", "127.0.0.1::" + strconv.Itoa(port)}, args...)
	if _, err := liveDockerOutput(ctx, run...); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = liveDockerOutput(context.Background(), "rm", "--force", name) })
	return name
}

// liveWaitFor answers the container's base URL once path answers 200.
func liveWaitFor(t *testing.T, ctx context.Context, name string, port int, path string) string {
	t.Helper()
	out, err := liveDockerOutput(ctx, "port", name, strconv.Itoa(port)+"/tcp")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + strings.TrimSpace(strings.Split(string(out), "\n")[0])
	for ctx.Err() == nil {
		if res, err := http.Get(base + path); err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return base
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("%s never answered %s", name, path)
	return ""
}
