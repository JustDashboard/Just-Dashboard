package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netdiag"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netpath"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

func TestDiagnosticInvestigationAPIKeepsTypedSourceAndPrivateArtifact(t *testing.T) {
	c, s := newClient(t)
	var calls atomic.Int32
	s.modules.diagnostics = netdiag.New(netdiag.NewStore(s.Store.DB), s.modules.jobs,
		func(context.Context, netsec.ProbeRequest) (*netsec.ProbeResult, error) {
			t.Error("investigation became a host quick tool")
			return nil, nil
		},
		netdiag.WithInvestigator(func(_ context.Context, req netpath.Request) (*netpath.Result, error) {
			calls.Add(1)
			now := time.Now().UTC()
			return &netpath.Result{Request: req, Scope: netpath.Scope{Vantage: "container_network_namespace", Source: "Scoped application", SourceAddress: req.SourceAddress, Target: req.Target, Address: "2001:db8::8", Family: req.Family, Protocol: req.Protocol, Port: req.Port}, StartedAt: now, EndedAt: now,
				Evidence: []netpath.Evidence{{ID: "provider", Title: "Provider", Basis: netpath.Unknown, State: "unknown", CheckedAt: now, Summary: "Private fixture artifact, not inbound proof."}}}, nil
		}))
	if err := s.modules.diagnostics.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/network/diagnostics/"
	request := `{"name":"Container IPv6 path","investigation":{"sourceKind":"container","containerId":"` + strings.Repeat("a", 64) + `","sourceAddress":"2001:db8::2","target":"private.test","family":"inet6","protocol":"udp","port":443,"measure":false}}`
	a := diagnosticResponse(t, c.do(http.MethodPost, base+"investigate", request, nil), http.StatusAccepted)
	done := awaitDiagnostic(t, s, a.ID)
	if calls.Load() != 1 || done.Kind != "investigation" || done.InvestigationRequest == nil || done.InvestigationRequest.Protocol != "udp" || done.Investigation == nil || done.Outcome != "completed_with_unknowns" {
		t.Fatalf("typed source was changed: %+v", done)
	}
	w := c.do(http.MethodGet, base, "", nil)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "Private fixture artifact") || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("metadata list leaked artifact: %d %s", w.Code, w.Body.String())
	}
	w = c.do(http.MethodGet, base+a.ID, "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Private fixture artifact") {
		t.Fatalf("retained result unavailable: %d %s", w.Code, w.Body.String())
	}
	w = c.do(http.MethodGet, base+a.ID+"/export", "", nil)
	if w.Code != http.StatusOK || !json.Valid(w.Body.Bytes()) || !strings.Contains(w.Body.String(), "investigationRequest") || !strings.Contains(w.Body.String(), "container_network_namespace") {
		t.Fatalf("typed export: %d %s", w.Code, w.Body.String())
	}
	b := diagnosticResponse(t, c.do(http.MethodPost, base+a.ID+"/rerun", `{}`, nil), http.StatusAccepted)
	awaitDiagnostic(t, s, b.ID)
	if calls.Load() != 2 || *b.InvestigationRequest != *a.InvestigationRequest || b.RerunOf != a.ID {
		t.Fatalf("rerun changed saved tuple: %+v", b)
	}
	w = c.do(http.MethodGet, base+"compare?before="+a.ID+"&after="+b.ID, "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"kind":"investigation"`) {
		t.Fatalf("typed comparison: %d %s", w.Code, w.Body.String())
	}
	if w = c.do(http.MethodPost, base+"investigate", strings.Replace(request, strings.Repeat("a", 64), "pid-1", 1), nil); w.Code != http.StatusBadRequest || calls.Load() != 2 {
		t.Fatalf("unvalidated source ran: %d %s", w.Code, w.Body.String())
	}
}
