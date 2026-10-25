package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// rewriteTransport sends every request to one test server while recording the
// URL the code asked for, so a Discord or Slack payload can be inspected
// without the provider's host being reachable.
type rewriteTransport struct {
	mu       sync.Mutex
	server   *httptest.Server
	requests []recordedRequest
}

type recordedRequest struct {
	URL    string
	Body   string
	Header http.Header
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	t.mu.Lock()
	t.requests = append(t.requests, recordedRequest{URL: req.URL.String(), Body: string(body), Header: req.Header.Clone()})
	t.mu.Unlock()
	target, _ := url.Parse(t.server.URL)
	clone := req.Clone(req.Context())
	clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
	clone.Body = io.NopCloser(strings.NewReader(string(body)))
	return http.DefaultTransport.RoundTrip(clone)
}

func (t *rewriteTransport) recorded() []recordedRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]recordedRequest(nil), t.requests...)
}

func okServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	t.Cleanup(server.Close)
	return server
}

func TestNotificationChannelValidationMatrix(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		url     string
		config  NotificationConfig
		wantErr bool
		target  string
	}{
		{"webhook http", NotificationKindWebhook, "http://127.0.0.1:9/hook", NotificationConfig{}, false, "http://127.0.0.1:9/hook"},
		{"webhook scheme", NotificationKindWebhook, "ftp://example.test", NotificationConfig{}, true, ""},
		{"discord ok", NotificationKindDiscord, "", NotificationConfig{WebhookURL: "https://discord.com/api/webhooks/123456/abcDEFtoken"}, false, "https://discord.com/api/webhooks/123456/••••"},
		{"discord wrong host", NotificationKindDiscord, "", NotificationConfig{WebhookURL: "https://example.test/api/webhooks/1/2"}, true, ""},
		{"discord plain http", NotificationKindDiscord, "", NotificationConfig{WebhookURL: "http://discord.com/api/webhooks/1/2"}, true, ""},
		{"slack ok", NotificationKindSlack, "", NotificationConfig{WebhookURL: "https://hooks.slack.com/services/T000/B000/XXXX"}, false, "https://hooks.slack.com/services/T000/B000/••••"},
		{"slack wrong path", NotificationKindSlack, "", NotificationConfig{WebhookURL: "https://hooks.slack.com/other/T000"}, true, ""},
		{"telegram ok", NotificationKindTelegram, "", NotificationConfig{BotToken: "123456789:AAExampleTokenValue_0123456789abcdef", ChatID: "-1001234567890"}, false, "Telegram chat -1001234567890"},
		{"telegram channel", NotificationKindTelegram, "", NotificationConfig{BotToken: "123456789:AAExampleTokenValue_0123456789abcdef", ChatID: "@deploys_channel"}, false, "Telegram chat @deploys_channel"},
		{"telegram bad token", NotificationKindTelegram, "", NotificationConfig{BotToken: "not-a-token", ChatID: "1"}, true, ""},
		{"telegram bad chat", NotificationKindTelegram, "", NotificationConfig{BotToken: "123456789:AAExampleTokenValue_0123456789abcdef", ChatID: "room one"}, true, ""},
		{"email ok", NotificationKindEmail, "", NotificationConfig{SMTPHost: "smtp.example.test", SMTPUsername: "ops", SMTPPassword: "pw", From: "Deploys <deploys@example.test>", To: []string{"team@example.test", "Second <two@example.test>"}}, false, "team@example.test, two@example.test"},
		{"email auth without tls", NotificationKindEmail, "", NotificationConfig{SMTPHost: "smtp.example.test", SMTPSecurity: SMTPSecurityNone, SMTPUsername: "ops", SMTPPassword: "pw", From: "a@example.test", To: []string{"b@example.test"}}, true, ""},
		{"email no recipients", NotificationKindEmail, "", NotificationConfig{SMTPHost: "smtp.example.test", From: "a@example.test"}, true, ""},
		{"email bad host", NotificationKindEmail, "", NotificationConfig{SMTPHost: "smtp host", From: "a@example.test", To: []string{"b@example.test"}}, true, ""},
		{"ntfy default server", NotificationKindNtfy, "", NotificationConfig{Topic: "deploys-7Qx"}, false, "https://ntfy.sh/••••"},
		{"ntfy own server and token", NotificationKindNtfy, "", NotificationConfig{ServerURL: "https://push.example.test/ntfy/", Topic: "ops", Token: "tk_AgQdq7mVBoFD37zQVN29RhuMzNIz2"}, false, "https://push.example.test/ntfy/••••"},
		{"ntfy plain http without a token", NotificationKindNtfy, "", NotificationConfig{ServerURL: "http://ntfy.lan", Topic: "ops"}, false, "http://ntfy.lan/••••"},
		{"ntfy token over plain http", NotificationKindNtfy, "", NotificationConfig{ServerURL: "http://ntfy.lan", Topic: "ops", Token: "tk_AgQdq7mVBoFD37zQVN29RhuMzNIz2"}, true, ""},
		{"ntfy token over loopback", NotificationKindNtfy, "", NotificationConfig{ServerURL: "http://127.0.0.1:8090", Topic: "ops", Token: "tk_AgQdq7mVBoFD37zQVN29RhuMzNIz2"}, false, "http://127.0.0.1:8090/••••"},
		{"ntfy prefix is not loopback", NotificationKindNtfy, "", NotificationConfig{ServerURL: "http://127.example.test", Topic: "ops", Token: "tk_AgQdq7mVBoFD37zQVN29RhuMzNIz2"}, true, ""},
		{"ntfy bad topic", NotificationKindNtfy, "", NotificationConfig{Topic: "two words"}, true, ""},
		{"ntfy bad token", NotificationKindNtfy, "", NotificationConfig{Topic: "ops", Token: "password"}, true, ""},
		{"ntfy sign-in in the url", NotificationKindNtfy, "", NotificationConfig{ServerURL: "https://user:pw@ntfy.example.test", Topic: "ops"}, true, ""},
		{"gotify ok", NotificationKindGotify, "", NotificationConfig{ServerURL: "https://gotify.example.test/", Token: "AbCdEf.GhIj-K_l"}, false, "https://gotify.example.test"},
		{"gotify plain http", NotificationKindGotify, "", NotificationConfig{ServerURL: "http://gotify.lan", Token: "AbCdEf.GhIj-K_l"}, true, ""},
		{"gotify loopback", NotificationKindGotify, "", NotificationConfig{ServerURL: "http://localhost:8070", Token: "AbCdEf.GhIj-K_l"}, false, "http://localhost:8070"},
		{"gotify no token", NotificationKindGotify, "", NotificationConfig{ServerURL: "https://gotify.example.test"}, true, ""},
		{"gotify no server", NotificationKindGotify, "", NotificationConfig{Token: "AbCdEf.GhIj-K_l"}, true, ""},
		{"pushover ok", NotificationKindPushover, "", NotificationConfig{Token: "azGDORePK8gMaC0QOYAMyEEuzJnyUi", UserKey: "uQiRzpo4DXghDmr9QzzfQu27cmVRsG"}, false, "Pushover user uQi••••"},
		{"pushover short key", NotificationKindPushover, "", NotificationConfig{Token: "azGDORePK8gMaC0QOYAMyEEuzJnyUi", UserKey: "uQiRzpo4"}, true, ""},
		{"mattermost ok", NotificationKindMattermost, "", NotificationConfig{WebhookURL: "https://chat.example.test/hooks/xi3sdq8j1fdjtpe4xoye5mz1ar"}, false, "https://chat.example.test/hooks/••••"},
		{"mattermost subpath", NotificationKindMattermost, "", NotificationConfig{WebhookURL: "https://example.test/mattermost/hooks/xi3sdq8j1fdjtpe4xoye5mz1ar"}, false, "https://example.test/mattermost/hooks/••••"},
		{"mattermost loopback http", NotificationKindMattermost, "", NotificationConfig{WebhookURL: "http://127.0.0.1:8065/hooks/xi3sdq8j1fdjtpe4xoye5mz1ar"}, false, "http://127.0.0.1:8065/hooks/••••"},
		{"mattermost remote http", NotificationKindMattermost, "", NotificationConfig{WebhookURL: "http://chat.example.test/hooks/xi3sdq8j1fdjtpe4xoye5mz1ar"}, true, ""},
		{"mattermost not a hook", NotificationKindMattermost, "", NotificationConfig{WebhookURL: "https://chat.example.test/api/v4/posts"}, true, ""},
		{"teams workflows", NotificationKindTeams, "", NotificationConfig{WebhookURL: "https://prod-12.westus.logic.azure.com:443/workflows/8a1f/triggers/manual/paths/invoke?api-version=2016-06-01&sp=%2Ftriggers%2Fmanual%2Frun&sv=1.0&sig=SIGNATURE"}, false, "https://prod-12.westus.logic.azure.com/workflows/••••"},
		{"teams power platform", NotificationKindTeams, "", NotificationConfig{WebhookURL: "https://default1234.56.environment.api.powerplatform.com:443/powerautomate/automations/direct/workflows/8a1f/triggers/manual/paths/invoke?api-version=1&sp=%2Ftriggers%2Fmanual%2Frun&sv=1.0&sig=SIGNATURE"}, false, "https://default1234.56.environment.api.powerplatform.com/workflows/••••"},
		{"teams without sig", NotificationKindTeams, "", NotificationConfig{WebhookURL: "https://prod-12.westus.logic.azure.com/workflows/8a1f/triggers/manual/paths/invoke"}, true, ""},
		{"teams retired connector", NotificationKindTeams, "", NotificationConfig{WebhookURL: "https://contoso.webhook.office.com/webhookb2/abc/IncomingWebhook/def/ghi"}, true, ""},
		{"teams elsewhere", NotificationKindTeams, "", NotificationConfig{WebhookURL: "https://example.test/workflows/1?sig=x"}, true, ""},
		{"unknown kind", "pager", "", NotificationConfig{}, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolved, err := validateNotificationChannel(tc.kind, tc.url, tc.config, nil)
			if tc.wantErr {
				if err == nil || !errors.Is(err, ErrInvalidNotification) {
					t.Fatalf("expected ErrInvalidNotification, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Target != tc.target {
				t.Fatalf("target = %q, want %q", resolved.Target, tc.target)
			}
		})
	}
	t.Run("email defaults port by security", func(t *testing.T) {
		resolved, err := validateNotificationChannel(NotificationKindEmail, "", NotificationConfig{SMTPHost: "smtp.example.test", SMTPSecurity: SMTPSecurityTLS, From: "a@example.test", To: []string{"b@example.test"}}, nil)
		if err != nil || resolved.Config.SMTPPort != 465 {
			t.Fatalf("port = %d, err = %v", resolved.Config.SMTPPort, err)
		}
	})
	t.Run("update keeps every stored push setting when omitted", func(t *testing.T) {
		existing := NotificationConfig{ServerURL: "https://push.example.test", Topic: "ops", Token: "tk_AgQdq7mVBoFD37zQVN29RhuMzNIz2"}
		resolved, err := validateNotificationChannel(NotificationKindNtfy, "", NotificationConfig{}, &existing)
		if err != nil || !reflect.DeepEqual(resolved.Config, existing) {
			t.Fatalf("resolved = %+v, err = %v", resolved.Config, err)
		}
		resolved, err = validateNotificationChannel(NotificationKindNtfy, "", NotificationConfig{Topic: "deploys"}, &existing)
		if err != nil || resolved.Config.Topic != "deploys" || resolved.Config.Token != existing.Token || resolved.Config.ServerURL != existing.ServerURL {
			t.Fatalf("resolved = %+v, err = %v", resolved.Config, err)
		}
		pushover := NotificationConfig{Token: "azGDORePK8gMaC0QOYAMyEEuzJnyUi", UserKey: "uQiRzpo4DXghDmr9QzzfQu27cmVRsG"}
		resolved, err = validateNotificationChannel(NotificationKindPushover, "", NotificationConfig{}, &pushover)
		if err != nil || !reflect.DeepEqual(resolved.Config, pushover) {
			t.Fatalf("resolved = %+v, err = %v", resolved.Config, err)
		}
	})
	t.Run("update keeps stored credential when omitted", func(t *testing.T) {
		existing := NotificationConfig{BotToken: "123456789:AAExampleTokenValue_0123456789abcdef", ChatID: "42"}
		resolved, err := validateNotificationChannel(NotificationKindTelegram, "", NotificationConfig{ChatID: "43"}, &existing)
		if err != nil || resolved.Config.BotToken != existing.BotToken || resolved.Config.ChatID != "43" {
			t.Fatalf("resolved = %+v, err = %v", resolved.Config, err)
		}
	})
}

func TestNotificationEventSelectionKeepsLegacyFinishedAlias(t *testing.T) {
	legacy := []string{NotificationEventFinished}
	for _, event := range []string{NotificationEventSucceeded, NotificationEventFailed, NotificationEventCancelled} {
		if !notificationEventSelected(legacy, event) {
			t.Fatalf("legacy run.finished should select %s", event)
		}
	}
	if notificationEventSelected(legacy, NotificationEventStarted) {
		t.Fatal("legacy run.finished must not announce starts")
	}
	if !notificationEventSelected(nil, NotificationEventStarted) {
		t.Fatal("an empty selection means every event")
	}
	if notificationEventSelected([]string{NotificationEventFailed}, NotificationEventSucceeded) {
		t.Fatal("a failure-only channel must stay quiet on success")
	}
	if _, err := normalizeNotificationEvents([]string{"run.exploded"}); !errors.Is(err, ErrInvalidNotification) {
		t.Fatalf("unknown event accepted: %v", err)
	}
}

func TestRenderNotificationDescribesEveryOutcome(t *testing.T) {
	base := NotificationEnvelope{ProjectName: "shop", EnvironmentName: "Production", RunNumber: 12, Operation: "deploy", Trigger: "git_push", Actor: "git-monitor", SourceRef: "main", SourceRevision: "0123456789abcdef0123456789abcdef01234567", DurationSeconds: 95, URL: "https://dash.example.test/deploy/1/runs/12"}
	failed := base
	failed.Event, failed.State, failed.TerminalReason = NotificationEventFailed, string(RunFailed), "readiness checks failed: could not connect"
	message := renderNotification(failed)
	if message.Emoji != "❌" || !strings.Contains(message.Title, "Run #12 failed") {
		t.Fatalf("failed title = %q emoji = %q", message.Title, message.Emoji)
	}
	joined := ""
	for _, field := range message.Fields {
		joined += field[0] + "=" + field[1] + ";"
	}
	for _, want := range []string{"Reason=readiness checks failed", "Source=main @ 0123456789ab", "Duration=1m 35s", "Trigger=git push · git-monitor"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("fields %q lack %q", joined, want)
		}
	}
	rolledBack := failed
	rolledBack.State = string(RunRolledBack)
	if !strings.Contains(renderNotification(rolledBack).Title, "rolled back") {
		t.Fatal("rollback outcome not described")
	}
	succeeded := base
	succeeded.Event, succeeded.Endpoint = NotificationEventSucceeded, "https://shop.example.test"
	message = renderNotification(succeeded)
	if message.Emoji != "✅" || !strings.Contains(strings.Join(flattenFields(message.Fields), ";"), "Address=https://shop.example.test") {
		t.Fatalf("succeeded message = %+v", message)
	}
	started := base
	started.Event = NotificationEventStarted
	if !strings.Contains(renderNotification(started).Title, "started") {
		t.Fatal("start not described")
	}
	test := renderNotification(NotificationEnvelope{Event: NotificationEventTest})
	if !strings.Contains(test.Title, "Test notification") {
		t.Fatal("test event not described")
	}
	stoppedRun := base
	stoppedRun.Event, stoppedRun.Operation = NotificationEventSucceeded, "stop"
	if joined := strings.Join(flattenFields(renderNotification(stoppedRun).Fields), ";"); !strings.Contains(joined, "Operation=Stop") {
		t.Fatalf("stop operation not described: %q", joined)
	}
	startedRun := base
	startedRun.Operation = "start"
	startedRun.Event = NotificationEventSucceeded
	if joined := strings.Join(flattenFields(renderNotification(startedRun).Fields), ";"); !strings.Contains(joined, "Operation=Start") {
		t.Fatalf("start operation not described: %q", joined)
	}
}

func TestRenderProxyGradeAlertNamesTheRuleAndReading(t *testing.T) {
	envelope := NotificationEnvelope{
		Event: NotificationEventProxyFiring,
		Proxy: &ProxyAlertEnvelope{
			Kind: "watch_grade_below", Label: "mail.example.test:587", Detail: "TLS grade B is below the expected A.",
		},
	}
	message := renderNotification(envelope)
	if message.Title != "Watched TLS grade below minimum — mail.example.test:587" ||
		message.Summary != envelope.Proxy.Detail {
		t.Fatalf("grade alert message = %+v", message)
	}
	envelope.Event = NotificationEventProxyRecovered
	if recovered := renderNotification(envelope); recovered.Title != "Resolved: "+message.Title {
		t.Fatalf("grade recovery message = %+v", recovered)
	}
}

func TestRenderDatabaseAlertNamesTheDatabaseAndReading(t *testing.T) {
	since := time.Date(2026, 10, 17, 3, 4, 0, 0, time.UTC)
	envelope := NotificationEnvelope{
		Event: NotificationEventDatabaseFiring, URL: "https://dash.example.test/databases/3/alerts",
		Database: &DatabaseAlertEnvelope{
			Kind: "disk", ConnectionID: 3, Subject: "connection:3", Label: "shop",
			Detail: "92% of the volume shop-data is used (184.0 GB of 200.0 GB); the line is 90%.", Since: since,
		},
	}
	message := renderNotification(envelope)
	if message.Title != "Database disk filling up — shop" || message.Summary != envelope.Database.Detail ||
		strings.Join(flattenFields(message.Fields), "|") != "Database=shop|Since=2026-10-17 03:04 UTC" {
		t.Fatalf("disk alert message = %+v", message)
	}
	envelope.Event = NotificationEventDatabaseRecovered
	if recovered := renderNotification(envelope); recovered.Title != "Resolved: "+message.Title || recovered.Emoji != "✅" {
		t.Fatalf("disk recovery message = %+v", recovered)
	}
	envelope.Event = NotificationEventDatabaseFiring
	envelope.Database = &DatabaseAlertEnvelope{Kind: "backup_failed", JobID: 4, Label: "nightly", Detail: "The run failed.", Test: true}
	backup := renderNotification(envelope)
	if backup.Title != "[test] Backup failed — nightly" || flattenFields(backup.Fields)[0] != "Backup job=nightly" {
		t.Fatalf("backup alert message = %+v", backup)
	}
	_, mail := emailMessage(NotificationConfig{From: "ops@example.test", To: []string{"team@example.test"}}, message, since)
	if !strings.Contains(string(mail), "Open it in Just Dashboard: https://dash.example.test/databases/3/alerts") ||
		strings.Contains(string(mail), "Open the run") {
		t.Fatalf("a database alert's mail sends its reader to a run:\n%s", mail)
	}
}

func TestRenderHostAlertNamesTheSubjectServerAndReading(t *testing.T) {
	since := time.Date(2026, 10, 25, 4, 5, 0, 0, time.UTC)
	envelope := NotificationEnvelope{
		Event: NotificationEventHostFiring, URL: "https://dash.example.test/metrics/alerts",
		Host: &HostAlertEnvelope{
			Kind: "disk_filling", Subject: "/var", Label: "/var", Server: "web-01",
			Detail: "93% of /var is used (186.0 GB of 200.0 GB); the line is 90%.", Since: since,
		},
	}
	message := renderNotification(envelope)
	if message.Title != "Disk filling up — /var" || message.Summary != envelope.Host.Detail ||
		strings.Join(flattenFields(message.Fields), "|") != "Filesystem=/var|Server=web-01|Since=2026-10-25 04:05 UTC" {
		t.Fatalf("disk alert message = %+v", message)
	}
	envelope.Event = NotificationEventHostRecovered
	if recovered := renderNotification(envelope); recovered.Title != "Resolved: "+message.Title || recovered.Emoji != "✅" {
		t.Fatalf("disk recovery message = %+v", recovered)
	}
	// A CPU rule's subject is the server itself, so it is not named twice.
	envelope.Event = NotificationEventHostFiring
	envelope.Host = &HostAlertEnvelope{Kind: "cpu_high", Subject: "cpu", Label: "web-01", Server: "web-01", Detail: "CPU averaged 97%.", Test: true}
	cpu := renderNotification(envelope)
	if cpu.Title != "[test] CPU high — web-01" || strings.Join(flattenFields(cpu.Fields), "|") != "Server=web-01" {
		t.Fatalf("cpu alert message = %+v", cpu)
	}
	envelope.Host = &HostAlertEnvelope{Kind: "container_down", Label: "shop-api-1", Server: "web-01"}
	if container := renderNotification(envelope); container.Title != "Container down — shop-api-1" ||
		flattenFields(container.Fields)[0] != "Container=shop-api-1" {
		t.Fatalf("container alert message = %+v", container)
	}
	_, mail := emailMessage(NotificationConfig{From: "ops@example.test", To: []string{"team@example.test"}}, message, since)
	if !strings.Contains(string(mail), "Open it in Just Dashboard: https://dash.example.test/metrics/alerts") ||
		strings.Contains(string(mail), "Open the run") {
		t.Fatalf("a host alert's mail sends its reader to a run:\n%s", mail)
	}
}

func flattenFields(fields [][2]string) []string {
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		out = append(out, field[0]+"="+field[1])
	}
	return out
}

func TestDispatcherDeliversFailedRunsAndDeduplicates(t *testing.T) {
	f := newAutomationFixture(t)
	var mu sync.Mutex
	var received []NotificationEnvelope
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var envelope NotificationEnvelope
		_ = json.NewDecoder(r.Body).Decode(&envelope)
		mu.Lock()
		received = append(received, envelope)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer remote.Close()
	if _, _, err := f.automation.CreateNotificationChannel(context.Background(), NotificationWrite{Name: "legacy", URL: remote.URL, Events: []string{NotificationEventFinished}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	runs := NewOrchestrationStore(f.store)
	dispatcher := NewNotificationDispatcher(f.automation, runs, func() string { return "https://dash.example.test/" }, nil).WithHTTPClient(remote.Client())
	ended := time.Date(2026, 9, 16, 12, 5, 0, 0, time.UTC)
	claimed := ended.Add(-90 * time.Second)
	run := EngineRun{ID: 77, RunNumber: 3, ProjectID: f.projectID, EnvironmentID: f.environmentID, State: RunFailed, Operation: OperationDeploy, Trigger: TriggerGitPush, Actor: "git-monitor", PlanRevision: 1, ClaimedAt: &claimed, EndedAt: &ended, TerminalCode: "health_gate_failed", TerminalReason: "readiness checks failed", SourceRevision: strings.Repeat("a", 40)}

	dispatcher.RunStarted(context.Background(), run)
	dispatcher.RunFinished(context.Background(), run)
	dispatcher.RunFinished(context.Background(), run)

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("deliveries = %d, want exactly one failure notification (%+v)", len(received), received)
	}
	got := received[0]
	if got.Event != NotificationEventFailed || got.State != string(RunFailed) || got.ProjectName != "automation" || got.EnvironmentName != "Production" {
		t.Fatalf("envelope = %+v", got)
	}
	if got.URL != "https://dash.example.test/deploy/"+itoa(f.projectID)+"/runs/77" || got.DurationSeconds != 90 || got.RunNumber != 3 || got.TerminalReason != "readiness checks failed" {
		t.Fatalf("envelope details = %+v", got)
	}
	deliveries, err := f.automation.NotificationDeliveries(context.Background(), 1, 10)
	if err != nil || len(deliveries) != 1 || deliveries[0].Status != "delivered" || deliveries[0].Event != NotificationEventFailed {
		t.Fatalf("deliveries = %+v err=%v", deliveries, err)
	}
}

func itoa(v int64) string {
	return strings.TrimSpace(strings.Replace(string(mustJSON(v)), "\"", "", -1))
}

func TestDispatcherHonoursDisabledChannelsAndStartedSelection(t *testing.T) {
	f := newAutomationFixture(t)
	var count int
	var mu sync.Mutex
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer remote.Close()
	started, _, err := f.automation.CreateNotificationChannel(context.Background(), NotificationWrite{Name: "starts", URL: remote.URL, Events: []string{NotificationEventStarted}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	paused, _, err := f.automation.CreateNotificationChannel(context.Background(), NotificationWrite{Name: "paused", URL: remote.URL, Events: nil, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := NewNotificationDispatcher(f.automation, NewOrchestrationStore(f.store), nil, nil).WithHTTPClient(remote.Client())
	run := EngineRun{ID: 5, ProjectID: f.projectID, EnvironmentID: f.environmentID, State: RunPreparing, Operation: OperationDeploy}
	dispatcher.RunStarted(context.Background(), run)
	run.State = RunSucceeded
	dispatcher.RunFinished(context.Background(), run)
	mu.Lock()
	got := count
	mu.Unlock()
	if got != 1 {
		t.Fatalf("deliveries = %d, want one start notification and nothing from the paused channel", got)
	}
	resumed, err := f.automation.SetNotificationChannelEnabled(context.Background(), paused.ID, true)
	if err != nil || !resumed.Enabled {
		t.Fatalf("resume = %+v err=%v", resumed, err)
	}
	if _, err := f.automation.SetNotificationChannelEnabled(context.Background(), started.ID+paused.ID+50, true); !errors.Is(err, ErrTriggerNotFound) {
		t.Fatalf("missing channel error = %v", err)
	}
}

func TestProviderKindsPostTheirDocumentedPayloads(t *testing.T) {
	f := newAutomationFixture(t)
	server := okServer(t)
	transport := &rewriteTransport{server: server}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	envelope := NotificationEnvelope{Event: NotificationEventSucceeded, State: string(RunSucceeded), RunID: 9, RunNumber: 9, ProjectName: "shop", EnvironmentName: "Production", Operation: "deploy", Trigger: "manual", Actor: "admin", URL: "https://dash.example.test/deploy/1/runs/9", SentAt: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)}

	discord, _, err := f.automation.CreateNotificationChannel(context.Background(), NotificationWrite{Name: "discord", Kind: NotificationKindDiscord, Config: NotificationConfig{WebhookURL: "https://discord.com/api/webhooks/123456/secretToken"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if discord.Kind != NotificationKindDiscord || strings.Contains(discord.URL, "secretToken") || strings.Contains(discord.Target, "secretToken") {
		t.Fatalf("discord channel leaks its token: %+v", discord)
	}
	slack, _, err := f.automation.CreateNotificationChannel(context.Background(), NotificationWrite{Name: "slack", Kind: NotificationKindSlack, Config: NotificationConfig{WebhookURL: "https://hooks.slack.com/services/T000/B000/SECRET"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	telegram, _, err := f.automation.CreateNotificationChannel(context.Background(), NotificationWrite{Name: "telegram", Kind: NotificationKindTelegram, Config: NotificationConfig{BotToken: "123456789:AAExampleTokenValue_0123456789abcdef", ChatID: "-100123"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, channel := range []*NotificationChannel{discord, slack, telegram} {
		if err := f.automation.DeliverNotification(context.Background(), client, channel.ID, envelope); err != nil {
			t.Fatalf("%s delivery: %v", channel.Kind, err)
		}
	}
	requests := transport.recorded()
	if len(requests) != 3 {
		t.Fatalf("requests = %d", len(requests))
	}
	var discordBody struct {
		Embeds []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
			Color int    `json:"color"`
		} `json:"embeds"`
	}
	if err := json.Unmarshal([]byte(requests[0].Body), &discordBody); err != nil || len(discordBody.Embeds) != 1 || !strings.Contains(discordBody.Embeds[0].Title, "Run #9 succeeded") || discordBody.Embeds[0].URL != envelope.URL {
		t.Fatalf("discord payload = %s (%v)", requests[0].Body, err)
	}
	if !strings.Contains(requests[0].URL, "discord.com/api/webhooks/123456/secretToken") {
		t.Fatalf("discord posted to %s", requests[0].URL)
	}
	var slackBody struct {
		Text   string `json:"text"`
		Blocks []any  `json:"blocks"`
	}
	if err := json.Unmarshal([]byte(requests[1].Body), &slackBody); err != nil || !strings.Contains(slackBody.Text, "shop · Production") || len(slackBody.Blocks) < 1 {
		t.Fatalf("slack payload = %s", requests[1].Body)
	}
	var telegramBody struct {
		ChatID    int64  `json:"chat_id"`
		Text      string `json:"text"`
		ParseMode string `json:"parse_mode"`
	}
	if err := json.Unmarshal([]byte(requests[2].Body), &telegramBody); err != nil || telegramBody.ChatID != -100123 || telegramBody.ParseMode != "HTML" || !strings.Contains(telegramBody.Text, "Run #9 succeeded") {
		t.Fatalf("telegram payload = %s", requests[2].Body)
	}
	if !strings.HasPrefix(requests[2].URL, "https://api.telegram.org/bot123456789:AAExampleTokenValue_0123456789abcdef/sendMessage") {
		t.Fatalf("telegram posted to %s", requests[2].URL)
	}
	channels, err := f.automation.ListNotificationChannels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, channel := range channels {
		encoded := string(mustJSON(channel))
		if strings.Contains(encoded, "secretToken") || strings.Contains(encoded, "SECRET") || strings.Contains(encoded, "AAExampleTokenValue") {
			t.Fatalf("listing leaks a credential: %s", encoded)
		}
	}
}

func TestPushAndChatKindsPostTheirDocumentedPayloads(t *testing.T) {
	f := newAutomationFixture(t)
	server := okServer(t)
	transport := &rewriteTransport{server: server}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	envelope := NotificationEnvelope{Event: NotificationEventFailed, State: string(RunFailed), RunID: 12, RunNumber: 12, ProjectName: "shop", EnvironmentName: "Production", Operation: "deploy", Trigger: "manual", Actor: "admin", TerminalReason: "build_failed: *exit* [1]", URL: "https://dash.example.test/deploy/1/runs/12", SentAt: time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)}
	writes := []NotificationWrite{
		{Name: "ntfy", Kind: NotificationKindNtfy, Config: NotificationConfig{ServerURL: "https://push.example.test", Topic: "secretTopic", Token: "tk_AgQdq7mVBoFD37zQVN29RhuMzNIz2"}},
		{Name: "gotify", Kind: NotificationKindGotify, Config: NotificationConfig{ServerURL: "https://gotify.example.test/sub/", Token: "AsecretGotify_1"}},
		{Name: "pushover", Kind: NotificationKindPushover, Config: NotificationConfig{Token: "azGDORePK8gMaC0QOYAMyEEuzJnyUi", UserKey: "uQiRzpo4DXghDmr9QzzfQu27cmVRsG"}},
		{Name: "mattermost", Kind: NotificationKindMattermost, Config: NotificationConfig{WebhookURL: "https://chat.example.test/hooks/xi3sdq8j1fdjtpe4xoye5mz1ar"}},
		{Name: "teams", Kind: NotificationKindTeams, Config: NotificationConfig{WebhookURL: "https://prod-12.westus.logic.azure.com:443/workflows/8a1f/triggers/manual/paths/invoke?api-version=2016-06-01&sv=1.0&sig=TEAMSSIG"}},
	}
	for _, write := range writes {
		write.Enabled = true
		channel, secret, err := f.automation.CreateNotificationChannel(context.Background(), write)
		if err != nil {
			t.Fatalf("%s: %v", write.Kind, err)
		}
		if secret != "" {
			t.Fatalf("%s returned a signing secret", write.Kind)
		}
		if err := f.automation.DeliverNotification(context.Background(), client, channel.ID, envelope); err != nil {
			t.Fatalf("%s delivery: %v", write.Kind, err)
		}
	}
	requests := transport.recorded()
	if len(requests) != len(writes) {
		t.Fatalf("requests = %d", len(requests))
	}

	ntfy := requests[0]
	var ntfyBody struct {
		Topic    string `json:"topic"`
		Title    string `json:"title"`
		Message  string `json:"message"`
		Priority int    `json:"priority"`
		Click    string `json:"click"`
		Actions  []struct {
			Action, Label, URL string
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(ntfy.Body), &ntfyBody); err != nil || ntfy.URL != "https://push.example.test/" || ntfyBody.Topic != "secretTopic" ||
		!strings.Contains(ntfyBody.Title, "Run #12 failed") || !strings.Contains(ntfyBody.Message, "Reason: build_failed") || ntfyBody.Priority != 4 ||
		ntfyBody.Click != envelope.URL || len(ntfyBody.Actions) != 1 || ntfyBody.Actions[0].Action != "view" {
		t.Fatalf("ntfy posted %s to %s (%v)", ntfy.Body, ntfy.URL, err)
	}
	if ntfy.Header.Get("Authorization") != "Bearer tk_AgQdq7mVBoFD37zQVN29RhuMzNIz2" {
		t.Fatalf("ntfy authorization = %q", ntfy.Header.Get("Authorization"))
	}

	gotify := requests[1]
	var gotifyBody struct {
		Title    string         `json:"title"`
		Message  string         `json:"message"`
		Priority int            `json:"priority"`
		Extras   map[string]any `json:"extras"`
	}
	if err := json.Unmarshal([]byte(gotify.Body), &gotifyBody); err != nil || gotify.URL != "https://gotify.example.test/sub/message" || gotifyBody.Priority != 8 ||
		!strings.Contains(gotifyBody.Message, "Open the run: "+envelope.URL) || gotifyBody.Extras["client::notification"] == nil {
		t.Fatalf("gotify posted %s to %s (%v)", gotify.Body, gotify.URL, err)
	}
	if gotify.Header.Get("X-Gotify-Key") != "AsecretGotify_1" || strings.Contains(gotify.URL, "AsecretGotify_1") {
		t.Fatalf("gotify key travelled as %q in %s", gotify.Header.Get("X-Gotify-Key"), gotify.URL)
	}

	pushover := requests[2]
	var pushoverBody struct {
		Token, User, Title, Message, URL string
		URLTitle                         string `json:"url_title"`
		Priority                         int    `json:"priority"`
		Timestamp                        int64  `json:"timestamp"`
	}
	if err := json.Unmarshal([]byte(pushover.Body), &pushoverBody); err != nil || pushover.URL != "https://api.pushover.net/1/messages.json" ||
		pushoverBody.Token != "azGDORePK8gMaC0QOYAMyEEuzJnyUi" || pushoverBody.User != "uQiRzpo4DXghDmr9QzzfQu27cmVRsG" || pushoverBody.Priority != 1 ||
		pushoverBody.URL != envelope.URL || pushoverBody.URLTitle != "Open the run" || pushoverBody.Timestamp != envelope.SentAt.Unix() {
		t.Fatalf("pushover posted %s to %s (%v)", pushover.Body, pushover.URL, err)
	}

	mattermost := requests[3]
	var mattermostBody struct {
		Attachments []struct {
			Color, Title, Text string
			TitleLink          string `json:"title_link"`
			Fields             []struct{ Title, Value string }
		} `json:"attachments"`
	}
	if err := json.Unmarshal([]byte(mattermost.Body), &mattermostBody); err != nil || mattermost.URL != "https://chat.example.test/hooks/xi3sdq8j1fdjtpe4xoye5mz1ar" || len(mattermostBody.Attachments) != 1 {
		t.Fatalf("mattermost posted %s to %s (%v)", mattermost.Body, mattermost.URL, err)
	}
	attachment := mattermostBody.Attachments[0]
	var reason string
	for _, field := range attachment.Fields {
		if field.Title == "Reason" {
			reason = field.Value
		}
	}
	if attachment.Color != "#ef4444" || attachment.TitleLink != envelope.URL || reason != `build\_failed: \*exit\* \[1\]` {
		t.Fatalf("mattermost attachment = %+v", attachment)
	}

	teams := requests[4]
	var teamsBody struct {
		Type        string `json:"type"`
		Attachments []struct {
			ContentType string `json:"contentType"`
			Content     struct {
				Type    string `json:"type"`
				Body    []map[string]any
				Actions []struct{ Type, Title, URL string }
			} `json:"content"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal([]byte(teams.Body), &teamsBody); err != nil || teamsBody.Type != "message" || len(teamsBody.Attachments) != 1 ||
		teamsBody.Attachments[0].ContentType != "application/vnd.microsoft.card.adaptive" || teamsBody.Attachments[0].Content.Type != "AdaptiveCard" {
		t.Fatalf("teams posted %s (%v)", teams.Body, err)
	}
	card := teamsBody.Attachments[0].Content
	if card.Body[0]["color"] != "attention" || len(card.Actions) != 1 || card.Actions[0].URL != envelope.URL || !strings.Contains(teams.URL, "sig=TEAMSSIG") {
		t.Fatalf("teams card = %+v to %s", card, teams.URL)
	}

	channels, err := f.automation.ListNotificationChannels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, channel := range channels {
		encoded := string(mustJSON(channel))
		for _, secret := range []string{"secretTopic", "tk_AgQ", "AsecretGotify_1", "azGDORe", "uQiRzpo4DX", "xi3sdq8j", "TEAMSSIG", "8a1f"} {
			if strings.Contains(encoded, secret) {
				t.Fatalf("listing leaks %q: %s", secret, encoded)
			}
		}
	}
}

func TestPushKindsRaisePriorityOnlyForBadNews(t *testing.T) {
	good := renderNotification(NotificationEnvelope{Event: NotificationEventSucceeded, State: string(RunSucceeded), ProjectName: "shop"})
	bad := renderNotification(NotificationEnvelope{Event: NotificationEventHostFiring, Host: &HostAlertEnvelope{Kind: "disk_filling", Label: "/var", Detail: "92% used"}})
	priority := func(body []byte) int {
		var payload struct {
			Priority int `json:"priority"`
		}
		_ = json.Unmarshal(body, &payload)
		return payload.Priority
	}
	if priority(ntfyPayload("ops", good)) != 3 || priority(ntfyPayload("ops", bad)) != 4 {
		t.Fatal("ntfy priorities")
	}
	if priority(gotifyPayload(good)) != 5 || priority(gotifyPayload(bad)) != 8 {
		t.Fatal("gotify priorities")
	}
	if priority(pushoverPayload(NotificationConfig{}, good, time.Now())) != 0 || priority(pushoverPayload(NotificationConfig{}, bad, time.Now())) != 1 {
		t.Fatal("pushover priorities")
	}
}

func TestPushoverAndNtfyStayWithinTheirLimits(t *testing.T) {
	long := renderNotification(NotificationEnvelope{Event: NotificationEventFailed, State: string(RunFailed), ProjectName: strings.Repeat("ш", 400), TerminalReason: strings.Repeat("é", 5000)})
	var pushover struct{ Title, Message string }
	if err := json.Unmarshal(pushoverPayload(NotificationConfig{}, long, time.Now()), &pushover); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(pushover.Title)); n > pushoverTitleLimit {
		t.Fatalf("pushover title is %d characters", n)
	}
	if n := len([]rune(pushover.Message)); n > pushoverMessageLimit {
		t.Fatalf("pushover message is %d characters", n)
	}
	var ntfy struct{ Message string }
	if err := json.Unmarshal(ntfyPayload("ops", long), &ntfy); err != nil {
		t.Fatal(err)
	}
	if len(ntfy.Message) > ntfyMessageLimit || !utf8.ValidString(ntfy.Message) {
		t.Fatalf("ntfy message is %d bytes, valid %v", len(ntfy.Message), utf8.ValidString(ntfy.Message))
	}
}

func TestEmailChannelRendersMessageThroughMailer(t *testing.T) {
	f := newAutomationFixture(t)
	var captured []byte
	var capturedConfig NotificationConfig
	f.automation.WithMailer(func(_ context.Context, cfg NotificationConfig, body []byte) error {
		capturedConfig, captured = cfg, body
		return nil
	})
	channel, secret, err := f.automation.CreateNotificationChannel(context.Background(), NotificationWrite{Name: "mail", Kind: NotificationKindEmail, Config: NotificationConfig{SMTPHost: "smtp.example.test", SMTPUsername: "ops", SMTPPassword: "hunter2", From: "Deploys <deploys@example.test>", To: []string{"team@example.test"}}, Events: []string{NotificationEventFailed}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if secret != "" || channel.Target != "team@example.test" {
		t.Fatalf("email channel = %+v secret=%q", channel, secret)
	}
	envelope := NotificationEnvelope{Event: NotificationEventFailed, State: string(RunFailed), RunID: 4, RunNumber: 4, ProjectName: "shop", EnvironmentName: "Production", Operation: "deploy", TerminalReason: "build failed", SentAt: time.Now().UTC()}
	if err := f.automation.DeliverNotification(context.Background(), nil, channel.ID, envelope); err != nil {
		t.Fatal(err)
	}
	body := string(captured)
	if capturedConfig.SMTPPassword != "hunter2" || capturedConfig.SMTPPort != 587 || !strings.Contains(body, "To: team@example.test") || !strings.Contains(body, "Reason: build failed") || !strings.HasPrefix(body, "From: deploys@example.test") {
		t.Fatalf("mail = %+v\n%s", capturedConfig, body)
	}
	updated, err := f.automation.UpdateNotificationChannel(context.Background(), channel.ID, NotificationWrite{Name: "mail", Config: NotificationConfig{SMTPHost: "smtp.example.test", SMTPUsername: "ops", From: "deploys@example.test", To: []string{"other@example.test"}}, Events: []string{NotificationEventFailed}, Enabled: true})
	if err != nil || updated.Target != "other@example.test" {
		t.Fatalf("update = %+v err=%v", updated, err)
	}
	if err := f.automation.DeliverNotification(context.Background(), nil, channel.ID, envelope); err != nil {
		t.Fatal(err)
	}
	if capturedConfig.SMTPPassword != "hunter2" {
		t.Fatal("omitting the password on update must keep the stored one")
	}
	if _, err := f.automation.UpdateNotificationChannel(context.Background(), channel.ID, NotificationWrite{Name: "mail", Kind: NotificationKindSlack, Enabled: true}); !errors.Is(err, ErrInvalidNotification) {
		t.Fatalf("kind change accepted: %v", err)
	}
}

func TestNotifyStepRecordsSelectionInsteadOfDelivering(t *testing.T) {
	f := newAutomationFixture(t)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer remote.Close()
	if _, _, err := f.automation.CreateNotificationChannel(context.Background(), NotificationWrite{Name: "failing", URL: remote.URL, Events: []string{NotificationEventFinished}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	executor := &NormalizedStepExecutor{notifications: f.automation}
	result := executor.notify(context.Background(), StepExecution{Run: EngineRun{ID: 8, ProjectID: f.projectID, EnvironmentID: f.environmentID, Metadata: json.RawMessage(`{}`)}})
	if result.State != StepPassed {
		t.Fatalf("notify step = %+v", result)
	}
	deliveries, err := f.automation.NotificationDeliveries(context.Background(), 1, 10)
	if err != nil || len(deliveries) != 0 {
		t.Fatalf("step must not deliver on its own: %+v err=%v", deliveries, err)
	}
	failedChain := executor.notify(context.Background(), StepExecution{Run: EngineRun{ID: 9, ProjectID: f.projectID, EnvironmentID: f.environmentID, Metadata: json.RawMessage(`{"scheduleMarker":true,"chainStatus":"failed"}`)}})
	if failedChain.State != StepFailed || failedChain.ErrorCode != "schedule_chain_failed" {
		t.Fatalf("failed schedule marker=%+v", failedChain)
	}
}
