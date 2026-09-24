package deploy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The channel list is read once for every channel, not a request per row: a
// busy channel draws only its last fourteen attempts, oldest first, and its
// newest one carries the retry still ahead of it.
func TestNotificationChannelsCarryTheirRecentHistory(t *testing.T) {
	f := newAutomationFixture(t)
	ctx := context.Background()
	busy, _, err := f.automation.CreateNotificationChannel(ctx, NotificationWrite{Name: "busy", URL: "https://hooks.example.test/busy", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.automation.CreateNotificationChannel(ctx, NotificationWrite{Name: "quiet", URL: "https://hooks.example.test/quiet", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.automation.CreateNotificationChannel(ctx, NotificationWrite{
		Name: "mail", Kind: NotificationKindEmail, Enabled: true,
		Config: NotificationConfig{SMTPHost: "smtp.example.test", SMTPUsername: "relay", SMTPPassword: "relay-password", From: "jd@example.test", To: []string{"ops@example.test"}},
	}); err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 16; i++ {
		event, runID, status, class, next := NotificationEventSucceeded, i, "delivered", "2xx", int64(0)
		if i == 10 {
			event, runID = NotificationEventTest, 0
		}
		if i == 16 {
			status, class, next = "failed", "5xx", 5000
		}
		if _, err := f.store.DB.Exec(`INSERT INTO deploy_notification_deliveries(channel_id,run_id,event,attempt,status,response_class,next_attempt_at,created_at,completed_at) VALUES(?,?,?,1,?,?,?,?,?)`,
			busy.ID, runID, event, status, class, next, 1000+i, 1000+i); err != nil {
			t.Fatal(err)
		}
	}

	channels, err := f.automation.NotificationChannelsWithHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]NotificationChannel{}
	for _, channel := range channels {
		byName[channel.Name] = channel
	}
	recent := byName["busy"].Recent
	if len(recent) != notificationRecentOutcomes || !recent[0].CreatedAt.Equal(time.Unix(1003, 0)) || !recent[13].CreatedAt.Equal(time.Unix(1016, 0)) {
		t.Fatalf("busy recent = %+v, want attempts 3 to 16 oldest first", recent)
	}
	for index, outcome := range recent {
		if outcome.Test != (index == 7) {
			t.Fatalf("recent[%d].Test = %v, want only the tenth attempt marked as a test", index, outcome.Test)
		}
	}
	last := byName["busy"].LastDelivery
	if last == nil || last.Status != "failed" || last.ResponseClass != "5xx" || last.Event != NotificationEventSucceeded ||
		last.NextAttemptAt == nil || !last.NextAttemptAt.Equal(time.Unix(5000, 0)) {
		t.Fatalf("busy last delivery = %+v, want the failed attempt with its retry", last)
	}
	if quiet := byName["quiet"]; quiet.LastDelivery != nil || quiet.Recent != nil || quiet.Via != "" {
		t.Fatalf("quiet channel = %+v, want no history", quiet)
	}
	if byName["mail"].Via != "smtp.example.test" || byName["busy"].Via != "" {
		t.Fatalf("via = %q / %q, want only the e-mail channel's host", byName["mail"].Via, byName["busy"].Via)
	}

	lean, err := f.automation.ListNotificationChannels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, channel := range lean {
		if channel.LastDelivery != nil || channel.Recent != nil || channel.Via != "" {
			t.Fatalf("dispatcher's channel read carries the page's history: %+v", channel)
		}
	}
}

// A delivery names the run it announced so history can link to it; a test
// message and a run that no longer exists name nothing, and an archived
// project is still called by its own name.
func TestNotificationDeliveriesNameTheRunTheyAnnounced(t *testing.T) {
	f := newAutomationFixture(t)
	ctx := context.Background()
	channel, _, err := f.automation.CreateNotificationChannel(ctx, NotificationWrite{Name: "ops", URL: "https://hooks.example.test/ops", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	var runID int64
	for range 2 {
		result, err := f.store.DB.Exec(`INSERT INTO deploy_runs(project_id,environment_id,started_at,status,state) VALUES(?,?,1,'succeeded','succeeded')`, f.projectID, f.environmentID)
		if err != nil {
			t.Fatal(err)
		}
		runID, _ = result.LastInsertId()
	}
	for index, delivery := range []struct {
		runID int64
		event string
	}{{runID, NotificationEventSucceeded}, {0, NotificationEventTest}, {999999, NotificationEventFailed}} {
		if _, err := f.store.DB.Exec(`INSERT INTO deploy_notification_deliveries(channel_id,run_id,event,attempt,status,response_class,created_at) VALUES(?,?,?,1,'delivered','2xx',?)`,
			channel.ID, delivery.runID, delivery.event, 100+index); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.store.DB.Exec(`UPDATE deploy_projects SET archived_name=name, name='__jd_archived_x', archived_at=1 WHERE id=?`, f.projectID); err != nil {
		t.Fatal(err)
	}

	deliveries, err := f.automation.NotificationDeliveries(ctx, channel.ID, 10)
	if err != nil || len(deliveries) != 3 {
		t.Fatalf("deliveries = %+v, %v", deliveries, err)
	}
	for _, unnamed := range deliveries[:2] {
		if unnamed.ProjectID != 0 || unnamed.ProjectName != "" || unnamed.RunNumber != 0 {
			t.Fatalf("delivery without a run = %+v, want no project or run number", unnamed)
		}
	}
	if named := deliveries[2]; named.RunID != runID || named.ProjectID != f.projectID || named.ProjectName != "automation" || named.RunNumber != 2 {
		t.Fatalf("run delivery = %+v, want automation run #2", named)
	}
}

// The trigger list's delivery summary is one statement for every trigger:
// the newest delivery in full, and the last fourteen decisions oldest first.
func TestAttachTriggerDeliveriesSummarisesEachLog(t *testing.T) {
	f := newAutomationFixture(t)
	ctx := context.Background()
	busy, err := f.automation.CreateTrigger(ctx, f.projectID, f.environmentID, TriggerWrite{Name: "busy", Kind: TriggerGenericHook, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.automation.CreateTrigger(ctx, f.projectID, f.environmentID, TriggerWrite{Name: "quiet", Kind: TriggerGenericHook, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 16; i++ {
		status, reason := "accepted", "hook_requested"
		if i%5 == 0 {
			status, reason = "rejected", "wrong_ref"
		}
		event := ProviderEvent{DeliveryID: fmt.Sprintf("d%d", i), Event: "deploy", Ref: "main"}
		if err := f.automation.RecordDelivery(ctx, &busy.Trigger, event, []byte(event.DeliveryID), status, reason, int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	triggers, err := f.automation.ListTriggers(ctx, f.projectID, f.environmentID)
	if err != nil {
		t.Fatal(err)
	}
	if triggers[0].LastDelivery != nil || triggers[0].Recent != nil {
		t.Fatalf("ListTriggers carries the delivery log on its own: %+v", triggers[0])
	}
	if err := f.automation.AttachTriggerDeliveries(ctx, triggers); err != nil {
		t.Fatal(err)
	}
	recent := triggers[0].Recent
	if len(recent) != triggerRecentOutcomes || recent[2].Decision != "rejected" || recent[2].Reason != "wrong_ref" || recent[3].Decision != "accepted" {
		t.Fatalf("busy recent = %+v, want deliveries 3 to 16 oldest first", recent)
	}
	if last := triggers[0].LastDelivery; last == nil || last.DeliveryID != "d16" || last.RunID != 16 || last.Ref != "main" || last.Decision != "accepted" {
		t.Fatalf("busy last delivery = %+v, want d16", last)
	}
	if triggers[1].LastDelivery != nil || triggers[1].Recent != nil {
		t.Fatalf("quiet trigger = %+v, want no deliveries", triggers[1])
	}
}

// A schedule reports its next five firings, led by the one the dispatcher
// has stored and walked in its own zone. A paused schedule makes none.
func TestSchedulesReportTheirNextRunsInTheirZone(t *testing.T) {
	f := newAutomationFixture(t)
	ctx := context.Background()
	steps := []ScheduleStep{{Action: "restart", Config: []byte(`{}`), Required: true}}
	for _, write := range []ScheduleWrite{
		{Name: "nightly", Expression: "0 3 * * *", Timezone: "Europe/Chisinau", Enabled: true, Steps: steps},
		{Name: "paused", Expression: "0 3 * * *", Timezone: "UTC", Enabled: false, Steps: steps},
	} {
		if _, err := f.automation.CreateSchedule(ctx, f.projectID, f.environmentID, write); err != nil {
			t.Fatal(err)
		}
	}
	schedules, err := f.automation.ListSchedules(ctx, f.projectID, f.environmentID)
	if err != nil {
		t.Fatal(err)
	}
	nightly, paused := schedules[0], schedules[1]
	if len(nightly.NextRuns) != ScheduleNextRuns || !nightly.NextRuns[0].Equal(*nightly.NextRunAt) {
		t.Fatalf("nightly next runs = %v, want %d led by %v", nightly.NextRuns, ScheduleNextRuns, nightly.NextRunAt)
	}
	zone := mustLocation(t, "Europe/Chisinau")
	for index, run := range nightly.NextRuns {
		local := run.In(zone)
		if local.Hour() != 3 || local.Minute() != 0 || (index > 0 && !run.After(nightly.NextRuns[index-1])) {
			t.Fatalf("nightly next runs = %v, want ascending 03:00 in Chisinau", nightly.NextRuns)
		}
	}
	if paused.NextRuns != nil {
		t.Fatalf("paused next runs = %v, want none", paused.NextRuns)
	}

	// 29 February comes once in the next year, so there is one run to report.
	leap, err := NextCronRuns("0 0 29 2 *", "UTC", time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC), ScheduleNextRuns)
	if err != nil || len(leap) != 1 || !leap[0].Equal(time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("leap-day runs = %v, %v", leap, err)
	}
	if _, err := NextCronRuns("0 0 30 2 *", "UTC", time.Now(), ScheduleNextRuns); err == nil {
		t.Fatal("an expression with no firing reported runs")
	}
}

// Every provider's pull request event keeps the branch it proposes, and the
// approval waiting on it reports that branch.
func TestProviderEventsKeepThePullRequestBranch(t *testing.T) {
	secret := "provider-secret"
	revision := strings.Repeat("c", 40)
	cases := []struct {
		provider, eventHeader, event, deliveryHeader, body, headRef, previewRef string
	}{
		{"github", "X-GitHub-Event", "pull_request", "X-GitHub-Delivery",
			fmt.Sprintf(`{"action":"opened","number":7,"repository":{"full_name":"acme/app"},"pull_request":{"user":{"login":"dev"},"head":{"sha":%q,"ref":"feature/login","repo":{"full_name":"fork/app"}}}}`, revision),
			"feature/login", "refs/pull/7/head"},
		{"gitlab", "X-Gitlab-Event", "Merge Request Hook", "X-Gitlab-Event-UUID",
			fmt.Sprintf(`{"object_kind":"merge_request","project":{"path_with_namespace":"acme/app"},"user":{"username":"dev"},"object_attributes":{"iid":4,"state":"opened","source_branch":"fix/typo","last_commit":{"id":%q},"source":{"path_with_namespace":"acme/app"}}}`, revision),
			"fix/typo", "refs/merge-requests/4/head"},
		{"bitbucket", "X-Event-Key", "pullrequest:created", "X-Request-UUID",
			fmt.Sprintf(`{"repository":{"full_name":"acme/app"},"pullrequest":{"id":3,"author":{"nickname":"dev"},"source":{"branch":{"name":"topic"},"commit":{"hash":%q},"repository":{"full_name":"acme/app"}}}}`, revision),
			"topic", "topic"},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			headers := http.Header{}
			headers.Set(tc.eventHeader, tc.event)
			headers.Set(tc.deliveryHeader, tc.provider+"-1")
			// An outbound notification is signed the way GitHub and Bitbucket
			// sign a delivery; GitLab sends the shared token itself.
			switch tc.provider {
			case "github":
				headers.Set("X-Hub-Signature-256", SignNotification([]byte(tc.body), secret))
			case "bitbucket":
				headers.Set("X-Hub-Signature", SignNotification([]byte(tc.body), secret))
			case "gitlab":
				headers.Set("X-Gitlab-Token", secret)
			}
			event, err := VerifyProvider(tc.provider, headers, []byte(tc.body), secret)
			if err != nil || event.HeadRef != tc.headRef || event.PreviewRef != tc.previewRef {
				t.Fatalf("event = %+v, %v; want head %q and preview ref %q", event, err, tc.headRef, tc.previewRef)
			}
		})
	}

	f := newAutomationFixture(t)
	ctx := context.Background()
	created, err := f.automation.CreateTrigger(ctx, f.projectID, f.environmentID, TriggerWrite{Name: "Previews", Kind: TriggerGitHub, Provider: "github", Enabled: true, Config: TriggerConfig{Repository: "acme/app", Ref: "main", Preview: true}})
	if err != nil {
		t.Fatal(err)
	}
	event := ProviderEvent{DeliveryID: "pr-7", Event: "pull_request", Repository: "acme/app", Revision: revision, PreviewNumber: 7, PreviewRef: "refs/pull/7/head", HeadRef: "feature/login", Author: "dev", HeadRepository: "fork/app"}
	if err := f.automation.requirePreviewApproval(ctx, &created.Trigger, event); !errors.Is(err, ErrPreviewApproval) {
		t.Fatalf("first revision error = %v, want it held for approval", err)
	}
	approvals, err := f.automation.ListPreviewApprovals(ctx, f.projectID)
	if err != nil || len(approvals) != 1 || approvals[0].HeadRef != "feature/login" {
		t.Fatalf("approvals = %+v, %v, want the pull request's branch", approvals, err)
	}
}
