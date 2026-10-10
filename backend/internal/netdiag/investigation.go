package netdiag

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netpath"
)

type Investigator func(context.Context, netpath.Request) (*netpath.Result, error)
type Option func(*Service)

// Configure the existing owner adapter before Start; a rerun takes fresh
// inventory and never holds a container PID or namespace across executions.
func WithInvestigator(investigator Investigator) Option {
	return func(s *Service) { s.investigator = investigator }
}

func (s *Service) CreateInvestigation(ctx context.Context, name string, req netpath.Request, actor string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createInvestigation(ctx, name, req, actor, "")
}

func (s *Service) createInvestigation(ctx context.Context, name string, req netpath.Request, actor, rerunOf string) (Run, error) {
	if err := s.ready(); err != nil {
		return Run{}, err
	}
	if s.investigator == nil {
		return Run{}, ErrUnavailable
	}
	name, err := validName(name)
	if err != nil {
		return Run{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	req, err = netpath.Validate(req)
	if err != nil {
		return Run{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return s.enqueue(ctx, Run{Name: name, Kind: "investigation", InvestigationRequest: &req,
		Scope: Scope{Vantage: map[string]string{"host": "dashboard_host", "container": "container_network_namespace"}[req.SourceKind], Source: req.ContainerID, SourceAddress: req.SourceAddress,
			Target: req.Target, Address: req.Address, Family: req.Family, Protocol: req.Protocol, Port: req.Port, Mark: req.Mark,
			Limitations: []string{"This record preserves the selected source and tuple. Each layer states observed, modeled, measured or unknown evidence.", "Rerunning obtains fresh native identity and resolver evidence; it does not replay a saved PID or adopt a foreign resource."}}}, actor, rerunOf)
}

func investigationOutcome(ctxErr error, result *netpath.Result, err error) (string, string) {
	if ctxErr != nil || err != nil || result == nil {
		return outcome(ctxErr, nil, err)
	}
	unknown, finding := false, false
	for _, e := range result.Evidence {
		unknown = unknown || e.Basis == netpath.Unknown || e.State == "unknown" || e.State == "unavailable" || e.State == "skipped" || e.State == "unsupported"
		finding = finding || e.State == "blocked" || e.State == "failed" || e.State == "refused"
	}
	if finding {
		return "completed_with_findings", "path_evidence"
	}
	if unknown {
		return "completed_with_unknowns", "path_evidence"
	}
	return "completed", "path_evidence"
}

// Copy before clipping so the quick report and retained artifact never share
// mutable evidence. Structural limits survive JSON escaping and long native text.
func boundedInvestigation(result *netpath.Result) (*netpath.Result, bool) {
	if result == nil {
		return nil, false
	}
	raw, _ := json.Marshal(result)
	var copy netpath.Result
	if json.Unmarshal(raw, &copy) != nil {
		return nil, true
	}
	trimmed := false
	bound := func(value *string, n int) {
		var clipped bool
		*value, clipped = clip(*value, n)
		trimmed = trimmed || clipped
	}
	bound(&copy.Comparison, 2048)
	bound(&copy.Scope.Source, 256)
	if len(copy.Addresses) > 8 {
		copy.Addresses = copy.Addresses[:8]
		trimmed = true
	}
	if len(copy.Scope.Limitations) > 16 {
		copy.Scope.Limitations = copy.Scope.Limitations[:16]
		trimmed = true
	}
	for i := range copy.Scope.Limitations {
		bound(&copy.Scope.Limitations[i], 1024)
	}
	if len(copy.Evidence) > 32 {
		copy.Evidence = copy.Evidence[:32]
		trimmed = true
	}
	for i := range copy.Evidence {
		e := &copy.Evidence[i]
		bound(&e.ID, 64)
		bound(&e.Title, 128)
		bound(&e.Scope, 1024)
		bound(&e.Owner, 256)
		bound(&e.OwnerPath, 256)
		bound(&e.Summary, 2048)
		if len(e.Facts) > 32 {
			e.Facts = e.Facts[:32]
			trimmed = true
		}
		for j := range e.Facts {
			bound(&e.Facts[j].Label, 128)
			bound(&e.Facts[j].Value, 1024)
		}
		if len(e.Limitations) > 16 {
			e.Limitations = e.Limitations[:16]
			trimmed = true
		}
		for j := range e.Limitations {
			bound(&e.Limitations[j], 1024)
		}
	}
	if copy.Measurement != nil {
		var clipped bool
		copy.Measurement, clipped = boundedResult(copy.Measurement)
		trimmed = trimmed || clipped
	}
	for {
		raw, _ = json.Marshal(copy)
		if len(raw) <= MaxArtifactBytes {
			break
		}
		trimmed = true
		if copy.Measurement != nil {
			copy.Measurement = nil
			continue
		}
		if len(copy.Evidence) > 0 {
			copy.Evidence = copy.Evidence[:len(copy.Evidence)-1]
			continue
		}
		return nil, true
	}
	return &copy, trimmed
}
