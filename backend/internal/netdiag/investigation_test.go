package netdiag

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netpath"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

func investigationRequest() netpath.Request {
	return netpath.Request{SourceKind: "container", ContainerID: strings.Repeat("a", 64), SourceAddress: "2001:db8::2", Target: "private.test", Family: "inet6", Protocol: "tcp", Port: 8443, Measure: true}
}

func investigationResult(req netpath.Request, source string) *netpath.Result {
	now := time.Now().UTC()
	return &netpath.Result{Request: req, Scope: netpath.Scope{Vantage: "container_network_namespace", Source: "Application container", SourceAddress: source, Target: req.Target, Address: "2001:db8::8", Family: req.Family, Protocol: req.Protocol, Port: req.Port, Limitations: []string{"Provider policy is unknown."}}, StartedAt: now, EndedAt: now.Add(time.Millisecond),
		Addresses: []string{"2001:db8::8"}, Comparison: "One source-scoped handshake was measured; provider policy remains unknown.",
		Evidence: []netpath.Evidence{{ID: "route", Title: "Kernel route", Basis: netpath.Observed, State: "observed", Facts: []netpath.Fact{{Label: "source", Value: source}}, CheckedAt: now}, {ID: "provider", Title: "Provider firewall", Basis: netpath.Unknown, State: "unknown", Summary: "Provider policy is unknown.", CheckedAt: now}, {ID: "probe", Title: "TCP", Basis: netpath.Measured, State: "connected", CheckedAt: now}}}
}

func TestRetainedInvestigationReopensAndRerunsExactSourceExplicitly(t *testing.T) {
	s, db := diagnosticService(t, func(context.Context, netsec.ProbeRequest) (*netsec.ProbeResult, error) {
		t.Error("path substituted a quick tool")
		return nil, nil
	})
	var calls atomic.Int32
	s.investigator = func(_ context.Context, req netpath.Request) (*netpath.Result, error) {
		if req != investigationRequest() {
			t.Errorf("changed scoped request: %+v", req)
		}
		source := "2001:db8::2"
		if calls.Add(1) > 1 {
			source = "2001:db8::3"
		}
		return investigationResult(req, source), nil
	}
	run, err := s.CreateInvestigation(t.Context(), "Private application path", investigationRequest(), "operator")
	if err != nil {
		t.Fatal(err)
	}
	done := awaitRun(t, s, run.ID)
	if done.Status != "completed" || done.Outcome != "completed_with_unknowns" || done.OutcomeSource != "path_evidence" || done.Result != nil || done.Investigation == nil || !done.HasResult || done.Stages[1].ID != "investigation" || done.Scope.SourceAddress != "2001:db8::2" {
		t.Fatalf("saved report lost its basis or scope: %+v", done)
	}
	reopened := New(NewStore(db.DB), jobs.New(nil), s.runner, WithInvestigator(s.investigator))
	if err := reopened.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.jobs.Shutdown)
	read, err := reopened.Get(t.Context(), done.ID)
	if err != nil || read.Investigation == nil || calls.Load() != 1 {
		t.Fatalf("read replayed or lost the report: %+v %v calls=%d", read, err, calls.Load())
	}
	data, err := reopened.Export(t.Context(), done.ID)
	if err != nil || !json.Valid(data) || !strings.Contains(string(data), "container_network_namespace") || !strings.Contains(string(data), "Provider policy is unknown") {
		t.Fatalf("scope/evidence export: %s %v", data, err)
	}
	second, err := reopened.Rerun(t.Context(), done.ID, "second-operator")
	if err != nil {
		t.Fatal(err)
	}
	after := awaitRun(t, reopened, second.ID)
	if calls.Load() != 2 || after.RerunOf != done.ID || *after.InvestigationRequest != *done.InvestigationRequest || after.Scope.SourceAddress != "2001:db8::3" {
		t.Fatalf("explicit rerun did not use fresh observations: %+v", after)
	}
	comparison, err := reopened.Compare(t.Context(), done.ID, after.ID)
	if err != nil || len(comparison.Records.Added) == 0 || comparison.Kind != "investigation" || comparison.InvestigationRequest == nil {
		t.Fatalf("typed comparison: %+v %v", comparison, err)
	}
	for _, changed := range []func(*netpath.Request){func(r *netpath.Request) { r.SourceAddress = "2001:db8::4" }, func(r *netpath.Request) { r.ContainerID = strings.Repeat("b", 64) }, func(r *netpath.Request) { r.Mark = "0x1" }, func(r *netpath.Request) { r.Measure = false }, func(r *netpath.Request) { r.Protocol = "udp" }, func(r *netpath.Request) { r.Address = "2001:db8::8" }} {
		copy := *done.InvestigationRequest
		changed(&copy)
		other := done
		other.InvestigationRequest = &copy
		if sameRequest(done, other) {
			t.Fatalf("different source or operation became comparable: %+v", copy)
		}
	}
}

func TestInvestigationInvalidRequestDoesNotQueueAndCancellationWaitsForOwner(t *testing.T) {
	s, _ := diagnosticService(t, func(context.Context, netsec.ProbeRequest) (*netsec.ProbeResult, error) { return nil, nil })
	started, drain, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	s.investigator = func(ctx context.Context, req netpath.Request) (*netpath.Result, error) {
		close(started)
		<-ctx.Done()
		close(drain)
		<-release
		return nil, ctx.Err()
	}
	req := investigationRequest()
	req.ContainerID = "reused-pid"
	if _, err := s.CreateInvestigation(t.Context(), "Invalid", req, "admin"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unvalidated source queued: %v", err)
	}
	run, err := s.CreateInvestigation(t.Context(), "Cancel path", investigationRequest(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err = s.Cancel(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	<-drain
	read, err := s.Get(t.Context(), run.ID)
	if err != nil || read.Status != "cancelling" || read.EndedAt != nil {
		t.Fatalf("cleanup prematurely promoted: %+v %v", read, err)
	}
	close(release)
	done := awaitRun(t, s, run.ID)
	if done.Status != "cancelled" || done.OutcomeSource != "context" || done.HasResult {
		t.Fatalf("cancelled report: %+v", done)
	}
}

func TestInvestigationArtifactBoundsCopyEscapedNativeEvidence(t *testing.T) {
	r := investigationResult(investigationRequest(), "2001:db8::2")
	r.Evidence = make([]netpath.Evidence, 40)
	for i := range r.Evidence {
		r.Evidence[i] = netpath.Evidence{ID: "layer", Summary: strings.Repeat("\"", 4000), Facts: make([]netpath.Fact, 40), Limitations: []string{strings.Repeat("🛰", 4000)}}
		for j := range r.Evidence[i].Facts {
			r.Evidence[i].Facts[j] = netpath.Fact{Label: "native", Value: strings.Repeat("\"", 4000)}
		}
	}
	bounded, truncated := boundedInvestigation(r)
	if bounded == nil || !truncated {
		t.Fatal("oversized report was not retained with explicit bounds")
	}
	raw, err := json.Marshal(bounded)
	if err != nil || len(raw) > MaxArtifactBytes || len(r.Evidence) != 40 || len(r.Evidence[0].Summary) != 4000 {
		t.Fatalf("bound or original changed: bytes=%d original=%d %v", len(raw), len(r.Evidence), err)
	}
}
