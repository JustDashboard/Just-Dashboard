package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// hostAlertHarness is the server with its host alerts' clock, channels and
// delivery replaced, so a test sees every envelope and moves time itself.
type hostAlertHarness struct {
	s     *Server
	a     *hostAlerts
	now   time.Time
	sent  []deploy.NotificationEnvelope
	admin *client
}

func newHostAlertHarness(t *testing.T) *hostAlertHarness {
	t.Helper()
	h := &hostAlertHarness{s: testServer(t), now: hostAlertClock}
	h.a = h.s.modules.hostAlerts
	h.a.now = func() time.Time { return h.now }
	h.a.server = func(context.Context) string { return "web-01" }
	h.a.url = func() string { return "https://dash.example.test/" }
	h.a.channels = func(context.Context) ([]deploy.NotificationChannel, error) {
		return []deploy.NotificationChannel{{ID: 42, Enabled: true}, {ID: 43, Enabled: false}}, nil
	}
	h.a.deliver = func(_ context.Context, channelID int64, envelope deploy.NotificationEnvelope) error {
		if channelID != 42 {
			t.Errorf("host alert sent to channel %d", channelID)
		}
		h.sent = append(h.sent, envelope)
		return nil
	}
	h.admin = &client{t: t, h: h.s.Routes(), cookie: signIn(t, h.s)}
	return h
}

func (h *hostAlertHarness) evaluate(t *testing.T) hostAlertsView {
	t.Helper()
	w := h.admin.do(http.MethodPost, "/api/v1/system/alerts/evaluate", `{}`, nil)
	var view hostAlertsView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || w.Code != http.StatusOK {
		t.Fatalf("evaluate: %d %s, %v", w.Code, w.Body.String(), err)
	}
	return view
}

func TestHostAlertsRequireAdminAndTellEachTransitionOnce(t *testing.T) {
	h := newHostAlertHarness(t)
	phase := "down"
	h.a.observe = func(_ context.Context, rules []hostAlertRule, _ time.Time) map[int64]hostAlertReading {
		out := map[int64]hostAlertReading{}
		for _, rule := range rules {
			reading := newHostAlertReading()
			switch phase {
			case "down":
				reading.Firing = []hostAlertFinding{{Subject: "shop-api-1", Label: "shop-api-1", Detail: "It exited with code 1."}}
			case "unknown":
				reading.Unknown["shop-api-1"] = true
			case "blind":
				reading = unjudgedHostAlert("Docker did not answer, so its containers could not be listed.")
			case "up":
				reading.Now["shop-api-1"] = "It is running."
			}
			out[rule.ID] = reading
		}
		return out
	}
	reader := &client{t: t, h: h.s.Routes(), cookie: signInAs(t, h.s, "reader", auth.RoleReadOnly)}
	base := "/api/v1/system/alerts"
	for _, req := range []struct{ method, path, body string }{
		{http.MethodGet, base, ""},
		{http.MethodPost, base + "/rules", `{"kind":"container_down"}`},
		{http.MethodPost, base + "/rules/1/test", `{}`},
		{http.MethodPost, base + "/test", `{"channelId":42}`},
		{http.MethodPost, base + "/evaluate", `{}`},
		{http.MethodPut, base + "/rules/1", `{"kind":"container_down"}`},
		{http.MethodDelete, base + "/rules/1", ""},
		{http.MethodPut, base + "/mutes", `{"ruleId":1,"subject":"shop-api-1","muted":true}`},
	} {
		if w := reader.do(req.method, req.path, req.body, nil); w.Code != http.StatusForbidden {
			t.Fatalf("reader %s %s: %d %s", req.method, req.path, w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{"kind":"load_high"}`, `{"kind":"container_down","channels":[99]}`, `{"kind":"cpu_high","params":{"threshold":120}}`} {
		if w := h.admin.do(http.MethodPost, base+"/rules", body, nil); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid rule %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	w := h.admin.do(http.MethodPost, base+"/rules", `{"kind":"container_down","params":{"minutes":2}}`, nil)
	var rule hostAlertRule
	if err := json.Unmarshal(w.Body.Bytes(), &rule); err != nil || w.Code != http.StatusCreated || rule.ID <= 0 || rule.Params.Minutes != 2 {
		t.Fatalf("create: %d %s, %v", w.Code, w.Body.String(), err)
	}
	id := strconv.FormatInt(rule.ID, 10)

	// Two passes and two minutes before a container is told down.
	view := h.evaluate(t)
	if len(h.sent) != 0 || len(view.Subjects) != 1 || view.Subjects[0].State != "pending" {
		t.Fatalf("first pass told or did not hold: sent %+v, view %+v", h.sent, view)
	}
	h.now = h.now.Add(2 * time.Minute)
	view = h.evaluate(t)
	if len(h.sent) != 1 || h.sent[0].Event != deploy.NotificationEventHostFiring || h.sent[0].Host == nil ||
		h.sent[0].Host.Subject != "shop-api-1" || h.sent[0].Host.Server != "web-01" ||
		!h.sent[0].Host.Since.Equal(hostAlertClock) || h.sent[0].URL != "https://dash.example.test/docker/containers/shop-api-1" ||
		len(view.Subjects) != 1 || view.Subjects[0].State != "firing" || len(view.History) != 1 || view.History[0].Delivered != 1 {
		t.Fatalf("firing: sent %+v, view %+v", h.sent, view)
	}
	h.now = h.now.Add(time.Minute)
	h.evaluate(t)
	phase = "unknown"
	h.evaluate(t)
	phase = "blind"
	view = h.evaluate(t)
	if len(h.sent) != 1 || view.Rules[0].Unjudged == "" || len(view.Subjects) != 1 {
		t.Fatalf("a container still down, or unreadable, was told again or dropped: sent %+v, view %+v", h.sent, view)
	}
	// Muted, its recovery is followed but nobody is told.
	if w := h.admin.do(http.MethodPut, base+"/mutes", `{"ruleId":`+id+`,"subject":"shop-api-1","muted":true}`, nil); w.Code != http.StatusNoContent {
		t.Fatalf("mute: %d %s", w.Code, w.Body.String())
	}
	phase = "up"
	view = h.evaluate(t)
	if len(h.sent) != 1 || view.Rules[0].Unjudged != "" || len(view.History) != 2 || !view.History[0].Muted ||
		view.History[0].Event != deploy.NotificationEventHostRecovered || len(view.Subjects) != 1 || view.Subjects[0].State != "quiet" {
		t.Fatalf("muted recovery: sent %+v, view %+v", h.sent, view)
	}
	if w := h.admin.do(http.MethodPut, base+"/mutes", `{"ruleId":`+id+`,"subject":"shop-api-1","muted":false}`, nil); w.Code != http.StatusNoContent {
		t.Fatalf("unmute: %d %s", w.Code, w.Body.String())
	}
	phase = "down"
	h.evaluate(t)
	h.now = h.now.Add(2 * time.Minute)
	h.evaluate(t)
	phase = "up"
	view = h.evaluate(t)
	if len(h.sent) != 3 || h.sent[2].Event != deploy.NotificationEventHostRecovered || h.sent[2].Host.Detail != "It is running." ||
		len(view.Subjects) != 0 {
		t.Fatalf("second episode: sent %+v, view %+v", h.sent, view)
	}

	// A rule's test reaches its channels and is kept in the history as one.
	w = h.admin.do(http.MethodPost, base+"/rules/"+id+"/test", `{}`, nil)
	if w.Code != http.StatusOK || len(h.sent) != 4 || !h.sent[3].Host.Test || h.sent[3].Host.Label != "example-api-1" {
		t.Fatalf("rule test: %d %s, %+v", w.Code, w.Body.String(), h.sent)
	}
	if w := h.admin.do(http.MethodPost, base+"/test", `{"channelId":42}`, nil); w.Code != http.StatusOK || !h.sent[4].Host.Test {
		t.Fatalf("channel test: %d %s", w.Code, w.Body.String())
	}

	// A pause starts the rule over: nothing it followed is kept.
	phase = "down"
	h.evaluate(t)
	w = h.admin.do(http.MethodPut, base+"/rules/"+id, `{"kind":"container_down","params":{"minutes":2},"channels":[],"enabled":false}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("pause: %d %s", w.Code, w.Body.String())
	}
	if view = h.evaluate(t); len(view.Subjects) != 0 || view.Rules[0].Enabled {
		t.Fatalf("a paused rule kept its subjects: %+v", view)
	}
	if w := h.admin.do(http.MethodDelete, base+"/rules/"+id, "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if w := h.admin.do(http.MethodGet, base, "", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"rules":[]`) {
		t.Fatalf("read after delete: %d %s", w.Code, w.Body.String())
	}
	for _, action := range []string{"host.alerts.rule.create", "host.alerts.evaluate", "host.alerts.mute", "host.alerts.unmute",
		"host.alerts.rule.test", "host.alerts.test", "host.alerts.rule.update", "host.alerts.rule.delete"} {
		entries, _, err := h.s.Audit.List(t.Context(), audit.Filter{Action: action})
		if err != nil || len(entries) == 0 || !entries[0].Success {
			t.Fatalf("%s audit: %+v, %v", action, entries, err)
		}
	}
}

// An edit made while a pass is deciding keeps its new terms: the pass's
// verdict on the old ones is neither written nor told.
func TestHostAlertPassDoesNotWriteOverAnEdit(t *testing.T) {
	h := newHostAlertHarness(t)
	ctx := context.Background()
	rule, err := h.a.CreateRule(ctx, hostAlertWrite{Kind: hostAlertBackupFailed})
	if err != nil {
		t.Fatal(err)
	}
	h.a.observe = func(ctx context.Context, rules []hostAlertRule, _ time.Time) map[int64]hostAlertReading {
		if _, err := h.a.UpdateRule(ctx, rule.ID, hostAlertWrite{Kind: hostAlertBackupFailed, Channels: []int64{42}}); err != nil {
			t.Fatal(err)
		}
		reading := newHostAlertReading()
		reading.Firing = []hostAlertFinding{{Subject: "job:1", Label: "files", Detail: "failed"}}
		return map[int64]hostAlertReading{rule.ID: reading}
	}
	h.a.Evaluate(ctx)
	var states int
	h.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM host_alert_state`).Scan(&states)
	if len(h.sent) != 0 || states != 0 {
		t.Fatalf("a verdict on the old terms was kept: %d states, sent %+v", states, h.sent)
	}
}

// The server's own observer, end to end, for the readings a test can stage in
// the store: a file-only backup job's failure is told, a dump job a database
// rule follows is left to it, and a CPU rule with no recorded samples says
// why it cannot be judged rather than looking quiet.
func TestHostAlertObserverReadsBackupsAndExplainsABlindRule(t *testing.T) {
	h := newHostAlertHarness(t)
	ctx := context.Background()
	db := h.s.Store.DB
	for _, job := range []struct{ name, dumps string }{{"files", "[]"}, {"shop", "[7]"}} {
		if _, err := db.Exec(`INSERT INTO backup_jobs(name, sources, target_kind, database_dumps, created_at) VALUES(?, '["/srv"]', 'local', ?, 1)`, job.name, job.dumps); err != nil {
			t.Fatal(err)
		}
	}
	for _, job := range []int{1, 2} {
		if _, err := db.Exec(`INSERT INTO backup_runs(job_id, started_at, status, log) VALUES(?, ?, 'failed', 'FAILED: bucket refused the upload')`, job, hostAlertClock.Add(-time.Hour).Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO db_alert_rules(kind, created_at, updated_at) VALUES('backup_failed', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.a.CreateRule(ctx, hostAlertWrite{Kind: hostAlertBackupFailed}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.a.CreateRule(ctx, hostAlertWrite{Kind: hostAlertCPUHigh}); err != nil {
		t.Fatal(err)
	}
	view := h.evaluate(t)
	if len(h.sent) != 1 || h.sent[0].Host.Kind != hostAlertBackupFailed || h.sent[0].Host.Label != "files" ||
		h.sent[0].URL != "https://dash.example.test/backups/1" ||
		!strings.HasSuffix(h.sent[0].Host.Detail, "failed: bucket refused the upload") {
		t.Fatalf("backup failure: %+v", h.sent)
	}
	if view.Rules[1].Unjudged == "" || !view.Metrics {
		t.Fatalf("a CPU rule with no samples did not say why: %+v", view.Rules)
	}
	// The database rule paused: the dump job is the host rule's now.
	if _, err := db.Exec(`UPDATE db_alert_rules SET enabled=0`); err != nil {
		t.Fatal(err)
	}
	h.evaluate(t)
	if len(h.sent) != 2 || h.sent[1].Host.Label != "shop" {
		t.Fatalf("a dump job no database rule follows: %+v", h.sent)
	}
}
