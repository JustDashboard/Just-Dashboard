package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

func TestProxyAlertsRequireAdminAndTellEachTransitionOnce(t *testing.T) {
	s := testServer(t)
	a := s.modules.proxyExtras.alerts
	now := time.Date(2026, time.September, 29, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	a.channels = func(context.Context) ([]deploy.NotificationChannel, error) {
		return []deploy.NotificationChannel{{ID: 42, Enabled: true}}, nil
	}
	var sent []deploy.NotificationEnvelope
	a.deliver = func(_ context.Context, channelID int64, envelope deploy.NotificationEnvelope) error {
		if channelID != 42 {
			t.Errorf("alert sent to channel %d", channelID)
		}
		sent = append(sent, envelope)
		return nil
	}
	phase := "firing"
	a.observe = func(_ context.Context, rules []proxyAlertRule, _ time.Time) map[int64]proxyAlertReading {
		out := map[int64]proxyAlertReading{}
		for _, rule := range rules {
			reading := newProxyAlertReading()
			switch phase {
			case "firing":
				reading.Firing = []proxyAlertFinding{{Subject: "app", Level: proxyAlertFiringLevel, Label: "app", Detail: "Certificate expired."}}
			case "unknown":
				reading.Unknown["app"] = true
			case "healthy":
				reading.Now["app"] = "Certificate renewed."
			}
			out[rule.ID] = reading
		}
		return out
	}
	h := s.Routes()
	admin := &client{t: t, h: h, cookie: signIn(t, s)}
	reader := &client{t: t, h: h, cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	base := "/api/v1/proxy/alerts"
	for _, req := range []struct{ method, path, body string }{
		{http.MethodGet, base, ""},
		{http.MethodPost, base + "/rules", `{"kind":"cert_expired"}`},
		{http.MethodPost, base + "/evaluate", `{}`},
		{http.MethodPut, base + "/mutes", `{"ruleId":1,"subject":"app","muted":true}`},
	} {
		if w := reader.do(req.method, req.path, req.body, nil); w.Code != http.StatusForbidden {
			t.Fatalf("reader %s %s: %d %s", req.method, req.path, w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{"kind":"no_such_rule"}`, `{"kind":"cert_expired","channels":[99]}`} {
		if w := admin.do(http.MethodPost, base+"/rules", body, nil); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid rule %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	w := admin.do(http.MethodPost, base+"/rules", `{"kind":"cert_expired","channels":[42]}`, nil)
	var rule proxyAlertRule
	if err := json.Unmarshal(w.Body.Bytes(), &rule); err != nil || w.Code != http.StatusCreated || rule.ID <= 0 || rule.Kind != proxyAlertCertExpired {
		t.Fatalf("create: %d %s, %v", w.Code, w.Body.String(), err)
	}
	run := func() proxyAlertsView {
		t.Helper()
		w := admin.do(http.MethodPost, base+"/evaluate", `{}`, nil)
		var view proxyAlertsView
		if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || w.Code != http.StatusOK {
			t.Fatalf("evaluate: %d %s, %v", w.Code, w.Body.String(), err)
		}
		return view
	}
	view := run()
	if len(sent) != 1 || sent[0].Event != deploy.NotificationEventProxyFiring ||
		sent[0].Proxy.Subject != "app" || len(view.Subjects) != 1 || view.Subjects[0].State != "firing" ||
		len(view.History) != 1 || view.History[0].Delivered != 1 {
		t.Fatalf("first firing: sent %+v, view %+v", sent, view)
	}
	run()
	phase = "unknown"
	run()
	if len(sent) != 1 {
		t.Fatalf("an unchanged or unreadable certificate caused another message: %+v", sent)
	}
	phase = "healthy"
	view = run()
	if len(sent) != 2 || sent[1].Event != deploy.NotificationEventProxyRecovered ||
		sent[1].Proxy.Detail != "Certificate renewed." || len(view.Subjects) != 0 || len(view.History) != 2 {
		t.Fatalf("recovery: sent %+v, view %+v", sent, view)
	}
	id := strconv.FormatInt(rule.ID, 10)
	if w := admin.do(http.MethodDelete, base+"/rules/"+id, "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if w := admin.do(http.MethodGet, base, "", nil); w.Code != http.StatusOK {
		t.Fatalf("read after delete: %d %s", w.Code, w.Body.String())
	}
	for _, action := range []string{"proxy.alerts.rule.create", "proxy.alerts.evaluate", "proxy.alerts.rule.delete"} {
		entries, _, err := s.Audit.List(t.Context(), audit.Filter{Action: action})
		if err != nil || len(entries) == 0 || !entries[0].Success {
			t.Fatalf("%s audit: %+v, %v", action, entries, err)
		}
	}
}

func TestProxyAlertReadingsForRenewalAndServedCertificates(t *testing.T) {
	for _, kind := range []string{proxyAlertRenewalFailed, proxyAlertServedDrift} {
		if _, err := validateProxyAlert(proxyAlertWrite{Kind: kind}); err != nil {
			t.Fatalf("%s cannot be saved: %v", kind, err)
		}
	}
	if reading := renewalAlertReading(nil); reading.Judged {
		t.Fatal("an unavailable certbot was judged healthy")
	}
	state := &proxysvc.CertbotState{Available: true, AutoRenew: true, Health: &proxysvc.RenewalHealth{
		State: "failed", Failures: []proxysvc.RenewalFailure{{Lineage: "app", Reason: "challenge failed"}},
		HookFailures: []proxysvc.HookFailure{{Kind: "deploy-hook", Code: 1, Output: "secret from a hook"}},
	}}
	reading := renewalAlertReading(state)
	if !reading.Judged || len(reading.Firing) != 1 ||
		reading.Firing[0].Subject != "certbot renewal" ||
		reading.Firing[0].Detail != "app: challenge failed; deploy-hook failed (exit 1)" {
		t.Fatalf("failed renewal: %+v", reading)
	}
	state.Health.State = "recovered"
	state.Health.Failures = nil
	state.Health.HookFailures = nil
	if reading = renewalAlertReading(state); !reading.Judged || len(reading.Firing) != 0 || reading.Now["certbot renewal"] == "" {
		t.Fatalf("recovered renewal: %+v", reading)
	}
	state.Health.State = "unknown"
	if reading = renewalAlertReading(state); reading.Judged {
		t.Fatalf("unknown renewal was judged: %+v", reading)
	}

	if reading = servedDriftAlertReading(nil); reading.Judged {
		t.Fatal("an unreadable drift report was judged healthy")
	}
	report := &proxysvc.DriftReport{Sites: []proxysvc.DriftSite{
		{Site: "app", ServerName: "app.test", Address: "127.0.0.1:443", State: proxysvc.DriftStale, Reason: "nginx still serves the old certificate"},
		{Site: "other", ServerName: "other.test", Address: "127.0.0.1:443", State: proxysvc.DriftUnreachable},
	}}
	reading = servedDriftAlertReading(report)
	if !reading.Judged || len(reading.Firing) != 1 || reading.Firing[0].Subject != "app app.test 127.0.0.1:443" ||
		!reading.Unknown["other other.test 127.0.0.1:443"] {
		t.Fatalf("served drift and unreachable site: %+v", reading)
	}
	report.Sites[0].State = proxysvc.DriftOK
	if reading = servedDriftAlertReading(report); len(reading.Firing) != 0 ||
		reading.Now["app app.test 127.0.0.1:443"] == "" {
		t.Fatalf("served certificate recovered: %+v", reading)
	}
}

func TestProxyAlertsCheckCurrentPinnedWatchEndpoints(t *testing.T) {
	s := testServer(t)
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer tlsServer.Close()
	address := strings.TrimPrefix(tlsServer.URL, "https://")
	ip, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)
	if _, err := s.Store.DB.Exec(`INSERT INTO watched_endpoints(domain, port, ip, created_at)
		VALUES('origin.example.test', ?, ?, 1)`, port, ip); err != nil {
		t.Fatal(err)
	}
	checks, read := s.checkWatchedDomains(t.Context())
	if !read || len(checks) != 1 || checks[0].IP != ip || checks[0].Cert == nil {
		t.Fatalf("the current pinned watch list was not checked: read %t, %+v", read, checks)
	}
	reading := watchAlertReading(proxyAlertRule{Kind: proxyAlertWatchUntrusted}, checks)
	subject := "origin.example.test:" + portText + " via " + ip
	if len(reading.Firing) != 1 || reading.Firing[0].Subject != subject {
		t.Fatalf("the pinned endpoint was not reported separately: %+v", reading)
	}
}

func TestWatchedGradeRuleScansPinnedTargetsAndJudgesOnlyCompletedGrades(t *testing.T) {
	s := testServer(t)
	if _, err := s.Store.DB.Exec(`INSERT INTO watched_endpoints(domain, port, ip, created_at)
		VALUES('mail.example.test', 587, '127.0.0.1', 1)`); err != nil {
		t.Fatal(err)
	}
	checks, read := s.checkWatchedGrades(t.Context(), func(_ context.Context, domain string, port int, opts proxysvc.ScanOptions) *proxysvc.TLSScan {
		if domain != "mail.example.test" || port != 587 || opts.ConnectTo != "127.0.0.1" || opts.StartTLS != "smtp" {
			t.Errorf("wrong grade scan target: %s:%d, %+v", domain, port, opts)
		}
		return &proxysvc.TLSScan{Grade: "B", Reachable: true}
	})
	if !read || len(checks) != 1 {
		t.Fatalf("grade checks = %+v, read %t", checks, read)
	}
	in, err := validateProxyAlert(proxyAlertWrite{Kind: proxyAlertWatchGradeBelow})
	if err != nil || in.Params.Grade != "A" {
		t.Fatalf("default minimum: %+v, %v", in, err)
	}
	if _, err := validateProxyAlert(proxyAlertWrite{Kind: proxyAlertWatchGradeBelow, Params: proxyAlertParams{Grade: "F"}}); err == nil {
		t.Fatal("F is not a useful minimum grade")
	}
	rule := proxyAlertRule{Kind: proxyAlertWatchGradeBelow, Params: in.Params}
	reading := watchGradeAlertReading(rule, checks)
	if len(reading.Firing) != 1 || reading.Firing[0].Subject != "mail.example.test:587 via 127.0.0.1" ||
		reading.Firing[0].Detail != "TLS grade B is below the expected A." {
		t.Fatalf("grade B under A: %+v", reading)
	}
	checks[0].Grade = "A+"
	reading = watchGradeAlertReading(rule, checks)
	if len(reading.Firing) != 0 || reading.Now["mail.example.test:587 via 127.0.0.1"] == "" {
		t.Fatalf("grade recovery: %+v", reading)
	}
	checks[0].Reachable = false
	reading = watchGradeAlertReading(rule, checks)
	if !reading.Unknown["mail.example.test:587 via 127.0.0.1"] || len(reading.Firing) != 0 {
		t.Fatalf("unreachable is not a grade: %+v", reading)
	}
}

func TestWatchedGradeRuleRotatesLargeWatchListsAfterTwoPasses(t *testing.T) {
	s := testServer(t)
	for i := 0; i < watchGradeBatch+1; i++ {
		if _, err := s.Store.DB.Exec(`INSERT INTO watched_endpoints(domain, port, created_at) VALUES(?, 443, 1)`,
			fmt.Sprintf("host-%02d.example.test", i)); err != nil {
			t.Fatal(err)
		}
	}
	var seen [][]string
	for pass := 0; pass < 4; pass++ {
		checked := []string{}
		checks, read := s.checkWatchedGrades(t.Context(), func(_ context.Context, _ string, _ int, _ proxysvc.ScanOptions) *proxysvc.TLSScan {
			return &proxysvc.TLSScan{Grade: "A", Reachable: true}
		})
		if !read || len(checks) != watchGradeBatch+1 {
			t.Fatalf("pass %d: read %t, checks %+v", pass, read, checks)
		}
		for _, check := range checks {
			if check.Grade != "" {
				checked = append(checked, check.Domain)
			}
		}
		seen = append(seen, checked)
	}
	for _, pair := range [][2]int{{0, 1}, {2, 3}} {
		if !slices.Equal(seen[pair[0]], seen[pair[1]]) {
			t.Fatalf("two-pass batch changed: %+v", seen)
		}
	}
	if slices.Equal(seen[0], seen[2]) || len(seen[0]) != watchGradeBatch || len(seen[2]) != watchGradeBatch {
		t.Fatalf("batch did not rotate: %+v", seen)
	}
}
