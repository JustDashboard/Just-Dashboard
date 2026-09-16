package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Two observers can announce the same terminal outcome at once (a worker exit
// racing an operator's cancellation). The reservation must let exactly one of
// them speak.
func TestConcurrentObserversDeliverOneNotification(t *testing.T) {
	f := newAutomationFixture(t)
	var received atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer remote.Close()
	if _, _, err := f.automation.CreateNotificationChannel(context.Background(), NotificationWrite{Name: "ops", URL: remote.URL, Events: []string{NotificationEventFailed}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	runs := NewOrchestrationStore(f.store)
	dispatcher := NewNotificationDispatcher(f.automation, runs, func() string { return "https://dash.example.test" }, nil).WithHTTPClient(remote.Client())
	ended := time.Date(2026, 9, 16, 12, 5, 0, 0, time.UTC)
	run := EngineRun{ID: 501, RunNumber: 1, ProjectID: f.projectID, EnvironmentID: f.environmentID, State: RunFailed, Operation: OperationDeploy, Trigger: TriggerManual, Actor: "admin", PlanRevision: 1, EndedAt: &ended}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dispatcher.RunFinished(context.Background(), run)
		}()
	}
	wg.Wait()
	if got := received.Load(); got != 1 {
		t.Fatalf("deliveries = %d, want exactly one", got)
	}
	deliveries, err := f.automation.NotificationDeliveries(context.Background(), 1, 10)
	if err != nil || len(deliveries) != 1 || deliveries[0].Status != "delivered" || deliveries[0].Attempt != 1 {
		t.Fatalf("deliveries = %+v err=%v", deliveries, err)
	}
}

// A provider that answers 5xx once must not lose the message: the failed
// attempt schedules a retry, the sweeper re-sends, and history shows both.
func TestFailedDeliveriesAreRetriedWithBackoffAndThenGiveUp(t *testing.T) {
	f := newAutomationFixture(t)
	var failures atomic.Int32
	failures.Store(1)
	var received atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if failures.Load() > 0 {
			failures.Add(-1)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer remote.Close()
	if _, _, err := f.automation.CreateNotificationChannel(context.Background(), NotificationWrite{Name: "ops", URL: remote.URL, Events: []string{NotificationEventSucceeded}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	runs := NewOrchestrationStore(f.store)
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	f.automation.now = func() time.Time { return now }
	runs.now = func() time.Time { return now }
	run, _, err := runs.Enqueue(context.Background(), RunRequest{
		ProjectID: f.projectID, EnvironmentID: f.environmentID, Operation: OperationDeploy, Trigger: TriggerManual,
		Actor: "admin", RequestDigest: "retry-digest", PlanRevision: 1, SlotClass: SlotLight, Steps: []StepKey{StepNotify},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB.Exec(`UPDATE deploy_runs SET state='succeeded', status='success', ended_at=? WHERE id=?`, now.Unix(), run.ID); err != nil {
		t.Fatal(err)
	}
	dispatcher := NewNotificationDispatcher(f.automation, runs, func() string { return "https://dash.example.test" }, nil).WithHTTPClient(remote.Client())
	persisted, err := runs.Run(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.RunFinished(context.Background(), *persisted)
	deliveries, err := f.automation.NotificationDeliveries(context.Background(), 1, 10)
	if err != nil || len(deliveries) != 1 || deliveries[0].Status != "failed" || deliveries[0].NextAttemptAt == nil {
		t.Fatalf("first attempt = %+v err=%v", deliveries, err)
	}
	if !deliveries[0].NextAttemptAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("next attempt = %v, want one minute later", deliveries[0].NextAttemptAt)
	}

	// Too early: nothing happens.
	dispatcher.RetryFailedDeliveries(context.Background())
	if received.Load() != 0 {
		t.Fatal("retried before its time")
	}
	// The observer firing again for the same outcome must not bypass the
	// backoff either.
	dispatcher.RunFinished(context.Background(), *persisted)
	if received.Load() != 0 {
		t.Fatal("a repeated observer call bypassed the retry schedule")
	}
	now = now.Add(61 * time.Second)
	dispatcher.RetryFailedDeliveries(context.Background())
	if received.Load() != 1 {
		t.Fatalf("retry deliveries = %d, want 1", received.Load())
	}
	deliveries, _ = f.automation.NotificationDeliveries(context.Background(), 1, 10)
	if len(deliveries) != 2 || deliveries[0].Attempt != 2 || deliveries[0].Status != "delivered" || deliveries[0].NextAttemptAt != nil || deliveries[1].NextAttemptAt != nil {
		t.Fatalf("history = %+v", deliveries)
	}
	// Delivered: the sweeper has nothing more to do.
	now = now.Add(time.Hour)
	dispatcher.RetryFailedDeliveries(context.Background())
	if received.Load() != 1 {
		t.Fatal("a delivered event was retried")
	}

	// A dead endpoint stops after the third attempt.
	failures.Store(100)
	second, _, err := runs.Enqueue(context.Background(), RunRequest{
		ProjectID: f.projectID, EnvironmentID: f.environmentID, Operation: OperationDeploy, Trigger: TriggerManual,
		Actor: "admin", RequestDigest: "retry-digest-2", PlanRevision: 1, SlotClass: SlotLight, Steps: []StepKey{StepNotify},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB.Exec(`UPDATE deploy_runs SET state='succeeded', status='success', ended_at=? WHERE id=?`, now.Unix(), second.ID); err != nil {
		t.Fatal(err)
	}
	persisted, _ = runs.Run(context.Background(), second.ID)
	dispatcher.RunFinished(context.Background(), *persisted)
	for i := 0; i < 5; i++ {
		now = now.Add(time.Hour)
		dispatcher.RetryFailedDeliveries(context.Background())
	}
	var attempts int
	_ = f.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_notification_deliveries WHERE run_id=?`, second.ID).Scan(&attempts)
	if attempts != notificationMaxAttempts {
		t.Fatalf("attempts for a dead endpoint = %d, want %d", attempts, notificationMaxAttempts)
	}
	var pending int
	_ = f.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_notification_deliveries WHERE run_id=? AND next_attempt_at<>0`, second.ID).Scan(&pending)
	if pending != 0 {
		t.Fatal("a delivery past its last attempt still schedules a retry")
	}
}

// Slack mrkdwn and Discord embeds each have their own rules; a project name
// that violates them must not break or lose the message.
func TestProviderPayloadsEscapeAndBoundTheirText(t *testing.T) {
	message := renderNotification(NotificationEnvelope{
		Event: NotificationEventSucceeded, State: string(RunSucceeded), RunNumber: 4,
		ProjectName: "shop <live> & co", EnvironmentName: strings.Repeat("x", 400), Operation: "deploy", Trigger: "manual",
		URL: "https://dash.example.test/deploy/1/runs/4", TerminalReason: strings.Repeat("r", 2000),
	})
	var slack struct {
		Text   string `json:"text"`
		Blocks []struct {
			Text *struct {
				Text string `json:"text"`
			} `json:"text"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(slackPayload(message), &slack); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(slack.Text, "shop &lt;live&gt; &amp; co") || strings.Contains(slack.Text, "<live>") {
		t.Fatalf("slack text is not escaped: %s", slack.Text)
	}
	if slack.Blocks[0].Text == nil || !strings.Contains(slack.Blocks[0].Text.Text, "|shop &lt;live&gt; &amp; co") {
		t.Fatalf("slack link title is not escaped: %+v", slack.Blocks[0])
	}
	var discord struct {
		Embeds []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Fields      []struct {
				Value string `json:"value"`
			} `json:"fields"`
		} `json:"embeds"`
	}
	if err := json.Unmarshal(discordPayload(message, time.Now()), &discord); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(discord.Embeds[0].Title)); n > discordTitleLimit {
		t.Fatalf("discord title has %d runes", n)
	}
	for _, field := range discord.Embeds[0].Fields {
		if n := len([]rune(field.Value)); n > discordFieldLimit {
			t.Fatalf("discord field has %d runes", n)
		}
	}
	if got := truncateRunes("héllo", 3); got != "hé…" {
		t.Fatalf("truncateRunes = %q", got)
	}
}

var _ = fmt.Sprint
