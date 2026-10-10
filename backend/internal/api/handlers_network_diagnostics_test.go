package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netdiag"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

func installDiagnosticRunner(t *testing.T, s *Server, runner netdiag.Runner) {
	t.Helper()
	s.modules.diagnostics = netdiag.New(netdiag.NewStore(s.Store.DB), s.modules.jobs, runner)
	if err := s.modules.diagnostics.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func diagnosticResponse(t *testing.T, w *httptest.ResponseRecorder, status int) netdiag.Run {
	t.Helper()
	if w.Code != status {
		t.Fatalf("got %d: %s; want %d", w.Code, w.Body.String(), status)
	}
	var run netdiag.Run
	if err := json.Unmarshal(w.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	return run
}

func awaitDiagnostic(t *testing.T, s *Server, id string) netdiag.Run {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		run, err := s.modules.diagnostics.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == "completed" || run.Status == "failed" || run.Status == "cancelled" || run.Status == "interrupted" {
			return run
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("diagnostic never finished")
	return netdiag.Run{}
}

func TestDiagnosticAPIWorkflowAuditExportAndCompare(t *testing.T) {
	c, s := newClient(t)
	var calls atomic.Int32
	installDiagnosticRunner(t, s, func(_ context.Context, req netsec.ProbeRequest) (*netsec.ProbeResult, error) {
		record := "192.0.2.1"
		if calls.Add(1) > 1 {
			record = "2001:db8::1"
		}
		return &netsec.ProbeResult{Tool: req.Tool, Target: req.Target, OK: true, Records: []string{record}, Output: "bounded output", Duration: "4ms"}, nil
	})
	base := "/api/v1/network/diagnostics/"
	a := diagnosticResponse(t, c.do(http.MethodPost, base, `{"name":"DNS baseline","request":{"tool":"dns","target":"example.test","record":"aaaa"}}`, nil), http.StatusAccepted)
	finished := awaitDiagnostic(t, s, a.ID)
	if finished.Request.Record != "AAAA" || finished.CreatedBy != "tester" || finished.JobID == "" {
		t.Fatalf("created=%+v", finished)
	}
	w := c.do(http.MethodGet, base, "", nil)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "bounded output") {
		t.Fatalf("metadata list=%d %s", w.Code, w.Body.String())
	}
	a = diagnosticResponse(t, c.do(http.MethodPatch, base+a.ID, `{"name":"Saved IPv6 DNS"}`, nil), http.StatusOK)
	if a.Name != "Saved IPv6 DNS" {
		t.Fatalf("rename=%+v", a)
	}
	b := diagnosticResponse(t, c.do(http.MethodPost, base+a.ID+"/rerun", `{}`, nil), http.StatusAccepted)
	awaitDiagnostic(t, s, b.ID)
	w = c.do(http.MethodGet, base+"compare?before="+a.ID+"&after="+b.ID, "", nil)
	var comparison netdiag.Comparison
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &comparison) != nil || len(comparison.Records.Added) != 1 || comparison.Records.Added[0] != "2001:db8::1" {
		t.Fatalf("comparison=%d %s", w.Code, w.Body.String())
	}
	w = c.do(http.MethodGet, base+a.ID+"/export", "", nil)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("Content-Type") != "application/json" || !strings.Contains(w.Header().Get("Content-Disposition"), a.ID+".json") || !json.Valid(w.Body.Bytes()) || w.Body.Len() > netdiag.MaxExportBytes {
		t.Fatalf("export=%d %+v %s", w.Code, w.Header(), w.Body.String())
	}
	if w := c.do(http.MethodPost, base+a.ID+"/cancel", `{}`, nil); w.Code != http.StatusConflict {
		t.Fatalf("cancel finished=%d", w.Code)
	}
	if w := c.do(http.MethodPut, base+"policy", `{"maxRuns":2,"maxAgeHours":24}`, nil); w.Code != http.StatusOK {
		t.Fatalf("retention=%d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodDelete, base+a.ID, "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete=%d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodGet, base+a.ID+"/export", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("deleted export=%d", w.Code)
	}
	for action, status := range map[string]int{"network.diagnostic.create": 202, "network.diagnostic.save": 200, "network.diagnostic.rerun": 202, "network.diagnostic.cancel": 409, "network.diagnostic.retention": 200, "network.diagnostic.delete": 204} {
		var count int
		if err := s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action=? AND status=?`, action, status).Scan(&count); err != nil || count == 0 {
			t.Errorf("missing audit %s/%d count=%d err=%v", action, status, count, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("read/save/export/compare replayed work: calls=%d", calls.Load())
	}
}

func TestDiagnosticArtifactsAndJobsRequireAdminIncludingNarrowedToken(t *testing.T) {
	c, s := newClient(t)
	installDiagnosticRunner(t, s, func(ctx context.Context, _ netsec.ProbeRequest) (*netsec.ProbeResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	run := diagnosticResponse(t, c.do(http.MethodPost, "/api/v1/network/diagnostics/", `{"name":"Private packet summary","request":{"tool":"ping","target":"127.0.0.1"}}`, nil), http.StatusAccepted)
	ordinary := s.modules.jobs.Start(jobs.Spec{Kind: "ordinary", Title: "Readable upgrade"}, func(context.Context, jobs.Emitter) error { return nil })
	var adminID int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM users WHERE username='tester'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	user, err := s.Auth.UserByID(t.Context(), adminID)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Auth.CreateAPIToken(t.Context(), user, "narrowed", auth.RoleReadOnly, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []struct{ name, cookie, token string }{
		{"reader", signInAs(t, s, "reader", auth.RoleReadOnly), ""},
		{"limited", signInAs(t, s, "limited", auth.RoleLimited), ""},
		{"narrowed admin token", "", token},
	} {
		t.Run(actor.name, func(t *testing.T) {
			client := &client{t: t, h: s.Routes(), cookie: actor.cookie}
			headers := map[string]string{}
			if actor.token != "" {
				headers["Authorization"] = "Bearer " + actor.token
			}
			for _, request := range []struct{ method, path, body string }{
				{"GET", "/network/diagnostics/", ""}, {"GET", "/network/diagnostics/policy", ""}, {"GET", "/network/diagnostics/" + run.ID, ""}, {"GET", "/network/diagnostics/" + run.ID + "/export", ""}, {"GET", "/network/diagnostics/compare?before=" + run.ID + "&after=" + run.ID, ""},
				{"POST", "/network/diagnostics/", `{"name":"unauthorised","request":{"tool":"ping","target":"127.0.0.1"}}`}, {"POST", "/network/diagnostics/investigate", `{"name":"private path","investigation":{"sourceKind":"host","target":"127.0.0.1","family":"inet","protocol":"tcp","port":443,"measure":true}}`}, {"PATCH", "/network/diagnostics/" + run.ID, `{"name":"changed"}`}, {"POST", "/network/diagnostics/" + run.ID + "/cancel", `{}`}, {"POST", "/network/diagnostics/" + run.ID + "/rerun", `{}`}, {"DELETE", "/network/diagnostics/" + run.ID, ""}, {"PUT", "/network/diagnostics/policy", `{"maxRuns":1,"maxAgeHours":1}`},
				{"GET", "/jobs/" + run.JobID, ""}, {"GET", "/jobs/" + run.JobID + "/stream", ""}, {"POST", "/jobs/" + run.JobID + "/cancel", `{}`},
			} {
				if w := client.do(request.method, "/api/v1"+request.path, request.body, headers); w.Code != http.StatusForbidden {
					t.Errorf("%s %s=%d %s", request.method, request.path, w.Code, w.Body.String())
				}
			}
			w := client.do(http.MethodGet, "/api/v1/jobs/", "", headers)
			if w.Code != http.StatusOK || strings.Contains(w.Body.String(), run.JobID) || !strings.Contains(w.Body.String(), ordinary.ID) {
				t.Fatalf("filtered jobs=%d %s", w.Code, w.Body.String())
			}
			if w := client.do(http.MethodGet, "/api/v1/jobs/"+ordinary.ID, "", headers); w.Code != http.StatusOK {
				t.Errorf("ordinary job read=%d", w.Code)
			}
		})
	}
	if w := c.do(http.MethodPost, "/api/v1/jobs/"+run.JobID+"/cancel", `{}`, nil); w.Code != http.StatusNoContent {
		t.Fatalf("admin job cancel=%d %s", w.Code, w.Body.String())
	}
	if run := awaitDiagnostic(t, s, run.ID); run.Status != "cancelled" {
		t.Fatalf("generic cancel did not settle durable record: %+v", run)
	}
}

func TestDiagnosticInvalidRequestsDoNotCreateJobsAndUnavailableIsExplicit(t *testing.T) {
	c, s := newClient(t)
	if w := c.do(http.MethodGet, "/api/v1/network/diagnostics/", "", nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("not started=%d %s", w.Code, w.Body.String())
	} else if !strings.Contains(w.Body.String(), `"retryable":true`) {
		t.Fatalf("unavailable read must offer retry: %s", w.Body.String())
	}
	var calls atomic.Int32
	installDiagnosticRunner(t, s, func(context.Context, netsec.ProbeRequest) (*netsec.ProbeResult, error) {
		calls.Add(1)
		return &netsec.ProbeResult{OK: true}, nil
	})
	for _, body := range []string{`{"name":"unsafe","request":{"tool":"ping","target":"--help"}}`, `{"name":"unsafe","request":{"tool":"shell","target":"id"}}`, `{"name":"unsafe","request":{"tool":"port","target":"127.0.0.1","port":0}}`, `{"name":"unsafe","request":{"tool":"dns","target":"localhost"},"command":"id"}`} {
		if w := c.do(http.MethodPost, "/api/v1/network/diagnostics/", body, nil); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid=%d %s", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 0 || len(s.modules.jobs.List()) != 0 {
		t.Fatal("invalid request launched a job")
	}
	if w := c.do(http.MethodGet, "/api/v1/network/diagnostics/compare", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("missing comparison=%d", w.Code)
	}
}
