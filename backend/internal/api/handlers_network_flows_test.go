package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netflows"
	"github.com/go-chi/chi/v5"
)

// Parent integration owns the mount/start hooks. This independent router
// exercises the real capability, session, CSRF, destructive and audit layers.
func flowTestRoutes(s *Server) http.Handler {
	r := chi.NewRouter()
	r.Route("/api/v1/network", func(r chi.Router) {
		r.Use(httpx.AllowlistCIDRs(s.Cfg.AllowedCIDRs, s.Log))
		r.Use(s.Authn.Authenticate)
		r.Use(s.apiLim.ByPrincipal)
		r.Use(httpx.AuditMutations(s.Audit))
		r.Use(httpx.RequireCSRF)
		s.mountNetworkFlowRoutes(r)
	})
	return r
}
func TestFlowHistoryRequiresAdminAndRecordingPolicyAreAudited(t *testing.T) {
	c, s := newClient(t)
	c.h = flowTestRoutes(s)
	w := c.do("GET", "/api/v1/network/flows/", "", nil)
	if w.Code != 503 {
		t.Fatalf("unavailable=%d %s", w.Code, w.Body.String())
	}
	s.modules.flowAccounting = netflows.New(netflows.NewStore(s.Store.DB), nil)
	if err := s.modules.flowAccounting.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.modules.flowAccounting.Shutdown(context.Background()) })
	w = c.do("GET", "/api/v1/network/flows/", "", nil)
	var report netflows.Report
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &report) != nil || report.Status != "off" || report.RecordingSince != nil || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("off=%d %s", w.Code, w.Body.String())
	}
	for _, actor := range []struct {
		name string
		role auth.Role
	}{{"flow-reader", auth.RoleReadOnly}, {"flow-limited", auth.RoleLimited}} {
		reader := &client{t: t, h: c.h, cookie: signInAs(t, s, actor.name, actor.role)}
		for _, req := range []struct{ method, path, body string }{{"GET", "/", ""}, {"GET", "/export", ""}, {"POST", "/recording", `{"enabled":true}`}, {"POST", "/observer", `{"enabled":true}`}, {"PUT", "/policy", `{"intervalSeconds":30,"retentionDays":1}`}, {"DELETE", "/history", ""}} {
			if w := reader.do(req.method, "/api/v1/network/flows"+req.path, req.body, nil); w.Code != 403 {
				t.Fatalf("role %s %s=%d", actor.role, req.path, w.Code)
			}
		}
	}
	for _, path := range []string{"?address=localhost", "?containerId=short", "?limit=1001", "?from=2026-10-08T00:01:00Z&to=2026-10-09T00:00:00Z", "?address=192.0.2.1&address=192.0.2.2", "?command=id"} {
		if w := c.do("GET", "/api/v1/network/flows/"+path, "", nil); w.Code != 400 {
			t.Fatalf("invalid %s=%d %s", path, w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{}`, `{"enabled":null}`, `{"enabled":true,"command":"id"}`} {
		if w := c.do("POST", "/api/v1/network/flows/recording", body, nil); w.Code != 400 {
			t.Fatalf("invalid recording=%d", w.Code)
		}
		if w := c.do("POST", "/api/v1/network/flows/observer", body, nil); w.Code != 400 {
			t.Fatalf("invalid observer=%d", w.Code)
		}
	}
	if w := c.do("POST", "/api/v1/network/flows/recording", `{"enabled":false}`, nil); w.Code != 200 {
		t.Fatalf("recording=%d %s", w.Code, w.Body.String())
	}
	if w := c.do("PUT", "/api/v1/network/flows/policy", `{"intervalSeconds":10,"retentionDays":1}`, nil); w.Code != 200 {
		t.Fatalf("policy=%d %s", w.Code, w.Body.String())
	}
	w = c.do("GET", "/api/v1/network/flows/export", "", nil)
	if w.Code != 200 || w.Body.Len() > netflows.MaxExportBytes || !json.Valid(w.Body.Bytes()) || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("export=%d %s", w.Code, w.Body.String())
	}
	if w := c.do("DELETE", "/api/v1/network/flows/history", "", nil); w.Code != 204 {
		t.Fatalf("clear=%d", w.Code)
	}
	for action, status := range map[string]int{"network.flow.recording": 200, "network.flow.policy": 200, "network.flow.clear": 204} {
		var count int
		if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action=? AND status=?`, action, status).Scan(&count); err != nil || count == 0 {
			t.Fatalf("audit %s count=%d err=%v", action, count, err)
		}
	}
	var id int64
	s.Store.DB.QueryRow(`SELECT id FROM users WHERE username='tester'`).Scan(&id)
	user, err := s.Auth.UserByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Auth.CreateAPIToken(t.Context(), user, "flow narrow", auth.RoleReadOnly, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if w := c.do(http.MethodGet, "/api/v1/network/flows/export", "", map[string]string{"Authorization": "Bearer " + token}); w.Code != 403 {
		t.Fatalf("narrow token=%d", w.Code)
	}
}

type fixtureFlowObserver struct {
	active bool
	starts int
}

func (o *fixtureFlowObserver) Start(context.Context) error { o.active = true; o.starts++; return nil }
func (o *fixtureFlowObserver) Drain(context.Context, time.Time) ([]netflows.Bucket, netflows.ObserverEvidence, error) {
	return nil, o.Status(), nil
}
func (o *fixtureFlowObserver) Stop(context.Context) (netflows.ObserverEvidence, error) {
	o.active = false
	return o.Status(), nil
}
func (o *fixtureFlowObserver) Status() netflows.ObserverEvidence {
	status := "off"
	if o.active {
		status = "recording"
	}
	return netflows.ObserverEvidence{Status: status, AttachmentsRetained: o.active}
}
func (o *fixtureFlowObserver) Acknowledge(string) error { return nil }
func TestFlowObserverRequiresExplicitAdminOptInAndAuditsEachMutation(t *testing.T) {
	c, s := newClient(t)
	c.h = flowTestRoutes(s)
	assertFlowObserverExplicitStop(t, c, s)
}

// The integration fixture also calls this with Server.Routes so the same
// authorization and stop prerequisite are checked on the mounted surface.
func assertFlowObserverExplicitStop(t *testing.T, c *client, s *Server) {
	t.Helper()
	observer := &fixtureFlowObserver{}
	s.modules.flowAccounting = netflows.New(netflows.NewStore(s.Store.DB), nil)
	s.modules.flowAccounting.SetObserver(observer)
	if err := s.modules.flowAccounting.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.modules.flowAccounting.Shutdown(context.Background()) })
	if w := c.do("GET", "/api/v1/network/flows/", "", nil); w.Code != 200 || observer.starts != 0 {
		t.Fatal("reading history attached programs")
	}
	if w := c.do("POST", "/api/v1/network/flows/observer", `{"enabled":true}`, nil); w.Code != 400 || observer.starts != 0 {
		t.Fatalf("kernel attachment without history recording=%d", w.Code)
	}
	if w := c.do("POST", "/api/v1/network/flows/recording", `{"enabled":true}`, nil); w.Code != 200 || observer.starts != 0 {
		t.Fatal("ordinary recording attached programs")
	}
	if w := c.do("POST", "/api/v1/network/flows/observer", `{"enabled":true}`, nil); w.Code != 200 || observer.starts != 1 || !observer.active {
		t.Fatalf("explicit attach=%d %s", w.Code, w.Body.String())
	}
	if w := c.do("POST", "/api/v1/network/flows/recording", `{"enabled":false}`, nil); w.Code != 400 || !observer.active {
		t.Fatalf("ordinary history stop bypassed explicit observer detach: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("POST", "/api/v1/network/flows/observer", `{"enabled":false}`, nil); w.Code != 200 || observer.active {
		t.Fatalf("explicit stop=%d", w.Code)
	}
	if w := c.do("POST", "/api/v1/network/flows/recording", `{"enabled":false}`, nil); w.Code != 200 {
		t.Fatalf("ordinary history stop after explicit detach=%d %s", w.Code, w.Body.String())
	}
	var audits int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='network.flow.observer' AND status=200`).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("observer mutation audits=%d err=%v", audits, err)
	}
}
