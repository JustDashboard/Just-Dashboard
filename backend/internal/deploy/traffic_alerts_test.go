package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// cannedRecord answers every window with the same summary, which is all an
// evaluator needs to be shown crossing a line and coming back.
type cannedRecord struct {
	exists bool
	held   int
	total  int
	rate   float64
	p95    *float64
	err    error
	asked  []string
}

func (c *cannedRecord) Window(_ context.Context, route string, _ accesslog.Filter) (accesslog.Window, error) {
	c.asked = append(c.asked, route)
	if c.err != nil {
		return accesslog.Window{}, c.err
	}
	summary := accesslog.Summary{Total: c.total, ErrorRate: c.rate, Classes: map[string]int{}}
	if c.p95 != nil {
		summary.Latency = &accesslog.Latency{P95: *c.p95}
	}
	return accesslog.Window{
		Result:   &accesslog.Result{Summary: summary},
		Coverage: accesslog.Coverage{Exists: c.exists, Held: c.held},
		Facts:    accesslog.Facts{Latency: c.p95 != nil},
	}, nil
}

type deliveries struct{ sent []NotificationEnvelope }

func (d *deliveries) deliver(_ context.Context, _ int64, envelope NotificationEnvelope) error {
	d.sent = append(d.sent, envelope)
	return nil
}

func TestTrafficAlertValidation(t *testing.T) {
	cases := []struct {
		in   TrafficAlertWrite
		want string
	}{
		{TrafficAlertWrite{Kind: "error_rate", Threshold: 0, WindowMinutes: 5}, "percentage"},
		{TrafficAlertWrite{Kind: "error_rate", Threshold: 101, WindowMinutes: 5}, "percentage"},
		{TrafficAlertWrite{Kind: "latency", Threshold: 0, WindowMinutes: 5}, "p95 limit"},
		{TrafficAlertWrite{Kind: "silence", WindowMinutes: 2}, "at least 5"},
		{TrafficAlertWrite{Kind: "uptime", Threshold: 1, WindowMinutes: 5}, "kind must be"},
		{TrafficAlertWrite{Kind: "error_rate", Threshold: 1, WindowMinutes: 0}, "window"},
	}
	for _, c := range cases {
		if _, err := validateTrafficAlert(c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%+v: err=%v, want %q", c.in, err, c.want)
		}
	}
	ok, err := validateTrafficAlert(TrafficAlertWrite{Kind: " Error_Rate ", Threshold: 1, WindowMinutes: 5, Channels: []int64{3, 1, 3, 0}})
	if err != nil || ok.Kind != TrafficAlertErrorRate || len(ok.Channels) != 2 || ok.Channels[0] != 1 {
		t.Fatalf("normalised = %+v err=%v", ok, err)
	}
}

func TestTrafficAlertStoreRoundTrip(t *testing.T) {
	f := newAutomationFixture(t)
	ctx := context.Background()
	created, err := f.automation.CreateTrafficAlert(ctx, f.projectID, f.environmentID, TrafficAlertWrite{Kind: "error_rate", Threshold: 1, WindowMinutes: 5})
	if err != nil {
		t.Fatal(err)
	}
	if created.State != trafficAlertStateOK || !created.Enabled || created.Channels == nil {
		t.Fatalf("created = %+v", created)
	}
	// A rule naming a channel that does not exist is a form error, not a
	// silent alert later.
	if _, err := f.automation.CreateTrafficAlert(ctx, f.projectID, f.environmentID, TrafficAlertWrite{Kind: "latency", Threshold: 500, WindowMinutes: 5, Channels: []int64{999}}); err == nil {
		t.Fatal("a missing channel was accepted")
	}
	list, _ := f.automation.ListTrafficAlerts(ctx, f.projectID)
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %+v", list)
	}
	off := false
	updated, err := f.automation.UpdateTrafficAlert(ctx, f.projectID, created.ID, TrafficAlertWrite{Kind: "latency", Threshold: 800, WindowMinutes: 10, Enabled: &off})
	if err != nil || updated.Kind != TrafficAlertLatency || updated.Enabled || updated.WindowMinutes != 10 {
		t.Fatalf("updated = %+v err=%v", updated, err)
	}
	enabled, _ := f.automation.EnabledTrafficAlerts(ctx)
	if len(enabled) != 0 {
		t.Fatal("a disabled rule must not be evaluated")
	}
	if err := f.automation.DeleteTrafficAlert(ctx, f.projectID, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.automation.DeleteTrafficAlert(ctx, f.projectID, created.ID); !errors.Is(err, ErrTrafficAlertNotFound) {
		t.Fatalf("second delete = %v", err)
	}
	// Another project cannot reach it.
	if _, err := f.automation.UpdateTrafficAlert(ctx, f.projectID+1, created.ID, TrafficAlertWrite{Kind: "silence", WindowMinutes: 5}); !errors.Is(err, ErrTrafficAlertNotFound) {
		t.Fatalf("cross-project update = %v", err)
	}
}

func TestTrafficAlertFiresOnceAndRecoversOnce(t *testing.T) {
	f := newAutomationFixture(t)
	ctx := context.Background()
	if _, err := f.automation.CreateTrafficAlert(ctx, f.projectID, f.environmentID, TrafficAlertWrite{Kind: "error_rate", Threshold: 1, WindowMinutes: 5}); err != nil {
		t.Fatal(err)
	}
	// A rule naming no channel reaches every enabled one; there has to be one.
	if _, _, err := f.automation.CreateNotificationChannel(ctx, NotificationWrite{Name: "ops", URL: "https://hooks.example.test/ops", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	record := &cannedRecord{exists: true, held: 100, total: 100, rate: 0.05}
	sent := &deliveries{}
	names := func(context.Context, int64, int64) (string, string, string) {
		return "api", "production", "https://dash/deploy/1/logs"
	}
	evaluator := NewTrafficAlertEvaluator(f.automation, record, sent.deliver, names, nil)
	now := time.Date(2026, 9, 19, 3, 12, 0, 0, time.UTC)
	evaluator.now = func() time.Time { return now }

	out := evaluator.Evaluate(ctx)
	if len(out) != 1 || !out[0].Firing || !out[0].Changed || out[0].Observed != 5 {
		t.Fatalf("first pass = %+v", out)
	}
	if !strings.HasSuffix(record.asked[0], "-"+itoa(f.environmentID)+".conf") {
		t.Fatalf("asked %q, want the environment's own route", record.asked[0])
	}
	if len(sent.sent) != 1 || sent.sent[0].Event != NotificationEventTrafficFiring || sent.sent[0].Alert == nil || sent.sent[0].Alert.Observed != 5 {
		t.Fatalf("firing delivery = %+v", sent.sent)
	}
	if sent.sent[0].ProjectName != "api" || sent.sent[0].URL != "https://dash/deploy/1/logs" {
		t.Fatalf("names not carried: %+v", sent.sent[0])
	}

	// Still crossed a minute later: one message, not sixty.
	now = now.Add(time.Minute)
	out = evaluator.Evaluate(ctx)
	if out[0].Changed || len(sent.sent) != 1 {
		t.Fatalf("second pass re-announced: %+v deliveries %d", out, len(sent.sent))
	}
	stored, _ := f.automation.ListTrafficAlerts(ctx, f.projectID)
	if stored[0].State != trafficAlertStateFiring || stored[0].StateSince == nil || !stored[0].StateSince.Equal(now.Add(-time.Minute)) || stored[0].Observed != 5 {
		t.Fatalf("stored state = %+v", stored[0])
	}

	// Back under the line: recovered, once.
	record.rate = 0.002
	now = now.Add(time.Minute)
	out = evaluator.Evaluate(ctx)
	if !out[0].Changed || out[0].Firing || len(sent.sent) != 2 || sent.sent[1].Event != NotificationEventTrafficRecovered {
		t.Fatalf("recovery pass = %+v deliveries %+v", out, sent.sent)
	}
	stored, _ = f.automation.ListTrafficAlerts(ctx, f.projectID)
	if stored[0].State != trafficAlertStateOK {
		t.Fatalf("after recovery = %+v", stored[0])
	}
}

func TestTrafficAlertSilenceAndLatencyAndUnjudgeable(t *testing.T) {
	f := newAutomationFixture(t)
	ctx := context.Background()
	if _, err := f.automation.CreateTrafficAlert(ctx, f.projectID, f.environmentID, TrafficAlertWrite{Kind: "silence", WindowMinutes: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.automation.CreateTrafficAlert(ctx, f.projectID, f.environmentID, TrafficAlertWrite{Kind: "latency", Threshold: 500, WindowMinutes: 5}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.automation.CreateNotificationChannel(ctx, NotificationWrite{Name: "ops", URL: "https://hooks.example.test/ops", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	sent := &deliveries{}

	// A route nobody has ever reached is new, not silent; and nginx's record
	// has no durations, so a latency rule over it has nothing to say.
	fresh := &cannedRecord{exists: true, held: 0, total: 0}
	evaluator := NewTrafficAlertEvaluator(f.automation, fresh, sent.deliver, nil, nil)
	out := evaluator.Evaluate(ctx)
	if len(out) != 2 || out[0].Skipped == "" || out[1].Skipped == "" || len(sent.sent) != 0 {
		t.Fatalf("fresh record judged: %+v", out)
	}
	// No record at all is skipped too.
	none := &cannedRecord{exists: false}
	if out := NewTrafficAlertEvaluator(f.automation, none, sent.deliver, nil, nil).Evaluate(ctx); out[0].Skipped != "no record yet" {
		t.Fatalf("absent record: %+v", out)
	}

	// A busy site that went quiet is silent; a slow site is slow.
	slow := 900.0
	quiet := &cannedRecord{exists: true, held: 5000, total: 0, p95: &slow}
	out = NewTrafficAlertEvaluator(f.automation, quiet, sent.deliver, nil, nil).Evaluate(ctx)
	if !out[0].Firing || !out[1].Firing || len(sent.sent) != 2 {
		t.Fatalf("quiet+slow = %+v deliveries %d", out, len(sent.sent))
	}
	// An unreadable record changes nothing and announces nothing.
	broken := &cannedRecord{err: errors.New("docker exec: gone")}
	out = NewTrafficAlertEvaluator(f.automation, broken, sent.deliver, nil, nil).Evaluate(ctx)
	if out[0].Skipped != "record unreadable" || len(sent.sent) != 2 {
		t.Fatalf("broken record = %+v", out)
	}
}

func TestTrafficAlertMessages(t *testing.T) {
	since := time.Date(2026, 9, 19, 3, 12, 0, 0, time.UTC)
	firing := renderNotification(NotificationEnvelope{
		Event: NotificationEventTrafficFiring, ProjectName: "api", EnvironmentName: "production",
		Alert: &TrafficAlertEnvelope{Kind: TrafficAlertErrorRate, Threshold: 1, Observed: 4.2, WindowMinutes: 5, Since: since},
	})
	if !strings.Contains(firing.Title, "api · production is failing") || !strings.Contains(firing.Title, "5xx rate 4.2% over 5 min") {
		t.Fatalf("firing title = %q", firing.Title)
	}
	if !strings.Contains(firing.Summary, "limit is 1.0%") {
		t.Fatalf("firing summary = %q", firing.Summary)
	}
	recovered := renderNotification(NotificationEnvelope{
		Event: NotificationEventTrafficRecovered, ProjectName: "api", EnvironmentName: "production",
		Alert: &TrafficAlertEnvelope{Kind: TrafficAlertLatency, Threshold: 500, Observed: 240, WindowMinutes: 5},
	})
	if !strings.Contains(recovered.Title, "recovered") || !strings.Contains(recovered.Title, "p95 240ms") || recovered.Color != 0x22c55e {
		t.Fatalf("recovered = %+v", recovered)
	}
	silent := renderNotification(NotificationEnvelope{
		Event: NotificationEventTrafficFiring, ProjectName: "shop", EnvironmentName: "production",
		Alert: &TrafficAlertEnvelope{Kind: TrafficAlertSilence, WindowMinutes: 15},
	})
	if !strings.Contains(silent.Title, "shop · production is silent — no requests for 15 min") {
		t.Fatalf("silent title = %q", silent.Title)
	}
	// A test firing says it is one, in the title, so nobody wakes up for it.
	f := newAutomationFixture(t)
	names := func(context.Context, int64, int64) (string, string, string) { return "api", "production", "" }
	test := NewTrafficAlertEvaluator(f.automation, nil, nil, names, nil).TestEnvelope(context.Background(),
		TrafficAlert{Kind: TrafficAlertErrorRate, Threshold: 1, WindowMinutes: 5})
	if !strings.HasPrefix(test.ProjectName, "[test] ") || test.Alert.Observed != 2 {
		t.Fatalf("test envelope = %+v", test)
	}
}
