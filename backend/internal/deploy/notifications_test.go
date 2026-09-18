package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
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
	URL  string
	Body string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	t.mu.Lock()
	t.requests = append(t.requests, recordedRequest{URL: req.URL.String(), Body: string(body)})
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
