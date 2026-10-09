package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netdiag"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

const savedFP = "SHA256:" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func TestSavedQuickResultHistoryTrustAndDevicesAreAdminAndAudited(t *testing.T) {
	c, s := newClient(t)
	installDiagnosticRunner(t, s, func(_ context.Context, req netsec.ProbeRequest) (*netsec.ProbeResult, error) {
		return &netsec.ProbeResult{Tool: req.Tool, Target: req.Target, OK: true, Verdict: netsec.ProbeOK, Duration: "1ms", Metrics: []netsec.ProbeMetric{{Key: "loss", Value: 0}}}, nil
	})
	base := "/api/v1/network/diagnostics/"

	// A quick result is held for its owner and saved without a new probe.
	owner := httpPrincipalName(t, s)
	id := quickResults.keep(owner, netsec.ProbeRequest{Tool: "ping", Target: "192.0.2.9"}, &netsec.ProbeResult{Tool: "ping", Target: "192.0.2.9", Verdict: netsec.ProbeUnknown, Summary: "No ICMP echo replies"}, time.Now().Add(-time.Second), time.Now())
	saved := diagnosticResponse(t, c.do(http.MethodPost, base+"results", `{"resultId":"`+id+`","name":"Unanswered ping"}`, nil), http.StatusCreated)
	if saved.Outcome != "completed_with_unknowns" || saved.Result == nil || saved.Result.Summary != "No ICMP echo replies" || saved.JobID != "" {
		t.Fatalf("saved = %+v", saved)
	}
	if w := c.do(http.MethodPost, base+"results", `{"resultId":"`+id+`","name":"twice"}`, nil); w.Code != http.StatusGone {
		t.Fatalf("a held result was saved twice: %d", w.Code)
	}
	other := quickResults.keep("someone-else", netsec.ProbeRequest{Tool: "ping", Target: "192.0.2.9"}, &netsec.ProbeResult{Tool: "ping"}, time.Now(), time.Now())
	if w := c.do(http.MethodPost, base+"results", `{"resultId":"`+other+`","name":"not mine"}`, nil); w.Code != http.StatusGone {
		t.Fatalf("another account's result was saved: %d", w.Code)
	}

	// History reads retained runs of exactly the same request.
	first := diagnosticResponse(t, c.do(http.MethodPost, base, `{"name":"ping","request":{"tool":"ping","target":"192.0.2.9"}}`, nil), http.StatusAccepted)
	awaitDiagnostic(t, s, first.ID)
	w := c.do(http.MethodGet, base+first.ID+"/history", "", nil)
	var history netdiag.History
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &history) != nil || len(history.Points) != 2 || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("history = %d %s", w.Code, w.Body.String())
	}

	// Trusted fingerprints: validated, audited, and forgotten only destructively.
	if w := c.do(http.MethodPut, base+"ssh-trust", `{"target":"Host.Example.test","port":22,"source":"entered","keys":[{"type":"ssh-ed25519","fingerprint":"MD5:aa"}]}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("bad fingerprint = %d", w.Code)
	}
	w = c.do(http.MethodPut, base+"ssh-trust", `{"target":"Host.Example.test","port":22,"source":"entered","keys":[{"type":"ssh-ed25519","fingerprint":"`+savedFP+`"}]}`, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"target":"host.example.test:22"`) {
		t.Fatalf("trust = %d %s", w.Code, w.Body.String())
	}
	res := &netsec.ProbeResult{Tool: "ssh", Target: "host.example.test:22", OK: true, Records: []string{"ssh-ed25519 " + savedFP}}
	s.compareSSHTrust(t.Context(), netsec.ProbeRequest{Tool: "ssh", Target: "host.example.test", Port: 22}, res)
	if res.Verdict != netsec.ProbeOK || !strings.Contains(res.Summary, "match the saved fingerprints") {
		t.Fatalf("scan did not compare with saved trust: %+v", res)
	}
	if w := c.do(http.MethodDelete, base+"ssh-trust?target=host.example.test:22", "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("forget = %d %s", w.Code, w.Body.String())
	}

	// Saved Wake-on-LAN devices.
	w = c.do(http.MethodPost, base+"wol-devices", `{"name":"NAS","mac":"02:11:22:33:44:55","interface":"eno1","verify":"192.168.1.50","port":22}`, nil)
	var device netdiag.WakeDevice
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &device) != nil || device.ID == "" {
		t.Fatalf("device = %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodPut, base+"wol-devices/"+device.ID, `{"name":"NAS","mac":"ff:ff:ff:ff:ff:ff","interface":"eno1"}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("broadcast MAC = %d", w.Code)
	}
	if w := c.do(http.MethodPut, base+"wol-devices/"+device.ID, `{"name":"Storage","mac":"02:11:22:33:44:55","interface":"eno1"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("update = %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodGet, base+"wol-devices", "", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Storage") {
		t.Fatalf("list = %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodDelete, base+"wol-devices/"+device.ID, "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", w.Code)
	}
	for action, status := range map[string]int{"network.diagnostic.save_result": 201, "network.ssh_trust.save": 200, "network.ssh_trust.forget": 204, "network.wol_device.save": 201, "network.wol_device.delete": 204} {
		if auditCount(t, s, "action=? AND status=?", action, status) == 0 {
			t.Errorf("missing audit %s/%d", action, status)
		}
	}
	if auditCount(t, s, "action=? AND status=?", "network.diagnostic.save_result", 410) == 0 || auditCount(t, s, "action=? AND status=?", "network.ssh_trust.save", 400) == 0 {
		t.Error("rejected attempts were not audited")
	}

	for _, actor := range []struct {
		name string
		role auth.Role
	}{{"reader", auth.RoleReadOnly}, {"limited", auth.RoleLimited}} {
		client := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, actor.name, actor.role)}
		for _, request := range []struct{ method, path, body string }{
			{"GET", "/network/diagnostics/" + first.ID + "/history", ""}, {"POST", "/network/diagnostics/results", `{"resultId":"x","name":"y"}`},
			{"GET", "/network/diagnostics/ssh-trust", ""}, {"PUT", "/network/diagnostics/ssh-trust", `{}`}, {"DELETE", "/network/diagnostics/ssh-trust?target=a:22", ""},
			{"GET", "/network/diagnostics/wol-devices", ""}, {"POST", "/network/diagnostics/wol-devices", `{}`}, {"DELETE", "/network/diagnostics/wol-devices/x", ""},
		} {
			if w := client.do(request.method, "/api/v1"+request.path, request.body, nil); w.Code != http.StatusForbidden {
				t.Errorf("%s %s %s = %d", actor.name, request.method, request.path, w.Code)
			}
		}
	}
}

func httpPrincipalName(t *testing.T, s *Server) string {
	t.Helper()
	var name string
	if err := s.Store.DB.QueryRow(`SELECT username FROM users WHERE username='tester'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestQuickProbeOffersAHeldResultAndSiteOwnersResolve(t *testing.T) {
	c, s := newClient(t)
	installDiagnosticRunner(t, s, func(context.Context, netsec.ProbeRequest) (*netsec.ProbeResult, error) { return nil, nil })
	w := c.do(http.MethodPost, "/api/v1/network/probe", `{"tool":"listeners"}`, nil)
	var res netsec.ProbeResult
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &res) != nil || len(res.ResultID) != 32 {
		t.Fatalf("probe = %d %s", w.Code, w.Body.String())
	}
	saved := diagnosticResponse(t, c.do(http.MethodPost, "/api/v1/network/diagnostics/results", `{"resultId":"`+res.ResultID+`","name":"Listeners now"}`, nil), http.StatusCreated)
	if saved.Request.Tool != "listeners" || saved.Result.ResultID != "" {
		t.Fatalf("saved = %+v", saved)
	}
	cache := &quickResultCache{entries: map[string]quickResult{}, now: time.Now}
	for i := 0; i < quickResultMax+5; i++ {
		cache.keep("a", netsec.ProbeRequest{}, &netsec.ProbeResult{}, time.Now(), time.Now())
	}
	if len(cache.entries) != quickResultMax {
		t.Fatalf("cache not bounded: %d", len(cache.entries))
	}
	expired := cache.keep("a", netsec.ProbeRequest{}, &netsec.ProbeResult{}, time.Now(), time.Now())
	cache.now = func() time.Time { return time.Now().Add(quickResultTTL + time.Minute) }
	if _, err := cache.take("a", expired); err == nil {
		t.Fatal("an expired result was handed out")
	}
}
