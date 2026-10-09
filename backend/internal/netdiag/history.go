package netdiag

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

// HistoryPoint is one retained run of a request, reduced to its outcome and
// structured numbers.
type HistoryPoint struct {
	ID        string               `json:"id"`
	Name      string               `json:"name"`
	CreatedAt time.Time            `json:"createdAt"`
	Status    string               `json:"status"`
	Outcome   string               `json:"outcome,omitempty"`
	Verdict   string               `json:"verdict,omitempty"`
	Summary   string               `json:"summary,omitempty"`
	Metrics   []netsec.ProbeMetric `json:"metrics"`
}

// History is every retained run with exactly the same normalized request,
// oldest first. It reads saved runs only; it never probes.
type History struct {
	Request     netsec.ProbeRequest `json:"request"`
	Points      []HistoryPoint      `json:"points"`
	Limitations []string            `json:"limitations"`
}

func (s *Service) History(ctx context.Context, id string) (History, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return History{}, err
	}
	if err := s.prune(ctx); err != nil {
		return History{}, err
	}
	base, err := s.store.Get(ctx, id)
	if err != nil {
		return History{}, err
	}
	if base.Kind == "investigation" {
		return History{}, fmt.Errorf("%w: history covers quick-tool runs; compare investigations instead", ErrInvalid)
	}
	runs, err := s.store.List(ctx)
	if err != nil {
		return History{}, err
	}
	h := History{Request: base.Request, Points: []HistoryPoint{}, Limitations: []string{
		"Only runs still inside the retention policy are listed; older runs were pruned.",
		"Each point is one observation from this host at that time; gaps between runs are not observed.",
	}}
	for _, run := range runs {
		if run.Kind == "investigation" || run.Request != base.Request || !terminal(run.Status) || !run.HasResult || run.Result == nil {
			continue
		}
		metrics := run.Result.Metrics
		if metrics == nil {
			metrics = []netsec.ProbeMetric{}
		}
		h.Points = append(h.Points, HistoryPoint{ID: run.ID, Name: run.Name, CreatedAt: run.CreatedAt, Status: run.Status, Outcome: run.Outcome, Verdict: run.Result.Verdict, Summary: run.Result.Summary, Metrics: metrics})
	}
	sort.SliceStable(h.Points, func(i, j int) bool { return h.Points[i].CreatedAt.Before(h.Points[j].CreatedAt) })
	return h, nil
}

// Adopt saves a result the server produced moments ago for an interactive
// run, so the operator can keep what they saw without sending the probe
// again. The result comes from the server's own short-lived cache, never from
// the client.
func (s *Service) Adopt(ctx context.Context, name string, req netsec.ProbeRequest, result *netsec.ProbeResult, actor string, started, ended time.Time) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return Run{}, err
	}
	name, err := validName(name)
	if err != nil {
		return Run{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if result == nil {
		return Run{}, fmt.Errorf("%w: no result to save", ErrInvalid)
	}
	if err := s.prune(ctx); err != nil {
		return Run{}, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Run{}, err
	}
	now := s.now()
	started, ended = started.UTC(), ended.UTC()
	run := Run{ID: hex.EncodeToString(random[:]), Name: name, Request: req, Scope: scopeFor(req), CreatedAt: started, UpdatedAt: now, CreatedBy: actor, StartedAt: &started, EndedAt: &ended}
	run.Scope.Limitations = append(run.Scope.Limitations, "Saved from an interactive run after it finished; the server kept its own result and sent no new traffic when saving.")
	run.Result, run.ResultTruncated = boundedResult(result)
	run.HasResult = run.Result != nil
	run.Outcome, run.OutcomeSource = outcome(nil, result, nil)
	run.Status = "completed"
	if !answered(result) {
		run.Status = "failed"
	}
	run.Error = run.Result.Error
	run.Stages = []Stage{
		{ID: "validation", Status: "completed", StartedAt: &started, EndedAt: &started, Outcome: "validated"},
		{ID: "probe", Status: run.Status, StartedAt: &started, EndedAt: &ended, Outcome: run.Outcome},
		{ID: "recording", Status: "completed", StartedAt: &now, EndedAt: &now, Outcome: "saved"},
	}
	if err := s.store.insert(ctx, run); err != nil {
		return Run{}, err
	}
	return run, s.prune(ctx)
}
