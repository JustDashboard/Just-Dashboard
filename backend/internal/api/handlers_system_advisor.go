package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountAdvisorRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/advisor/storage", s.handle(s.handleStorageAdvisor))
	r.Method(http.MethodGet, "/advisor/workloads", s.handle(s.handleWorkloadAdvisor))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapFileWrite))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodPost, "/advisor/storage/cleanup", s.handle(s.handleStorageCleanup))
		})
	})
	// A workload's remedy is the group's, so these act on many processes in
	// one audited request. Each is the per-process route's own gate:
	// signalling is destructive, reprioritising is administration.
	s.destructive(r, func(r chi.Router) {
		r.Method(http.MethodPost, "/advisor/workloads/signal", s.handle(s.handleWorkloadSignal))
	})
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPost, "/advisor/workloads/priority", s.handle(s.handleWorkloadPriority))
	})
}

// workloadTargetsMax bounds one batch: a group's member list is capped at 256,
// and a launcher and its group together stay well under this.
const workloadTargetsMax = 512

type workloadTarget struct {
	PID       int32  `json:"pid"`
	StartedAt string `json:"startedAt"`
}

type workloadOutcome struct {
	PID     int32  `json:"pid"`
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
}

type workloadResult struct {
	Items     []workloadOutcome `json:"items"`
	Signalled int               `json:"signalled,omitempty"`
	Changed   int               `json:"changed,omitempty"`
}

// validTargets refuses a batch before touching any process. A start time is
// required for every target here, unlike the single-process routes: the
// list was measured seconds ago and a reused PID inside it would be sent a
// signal meant for someone else.
func validTargets(targets []workloadTarget) error {
	if len(targets) == 0 || len(targets) > workloadTargetsMax {
		return httpx.BadRequest("targets must name between 1 and %d processes", workloadTargetsMax)
	}
	for _, target := range targets {
		if target.PID <= 0 || target.StartedAt == "" {
			return httpx.BadRequest("every target needs a pid and the startedAt it was measured with")
		}
	}
	return nil
}

// workloadSubject names a batch in the audit log by its first process.
func workloadSubject(names []string, total int) string {
	if len(names) == 0 {
		return fmt.Sprintf("%d processes", total)
	}
	if total == 1 {
		return names[0]
	}
	return fmt.Sprintf("%s and %d others", names[0], total-1)
}

func (s *Server) handleWorkloadSignal(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Targets []workloadTarget `json:"targets"`
		Signal  string           `json:"signal"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Signal == "" {
		req.Signal = "SIGTERM"
	}
	if req.Signal != "SIGTERM" && req.Signal != "SIGKILL" {
		return httpx.BadRequest("signal must be SIGTERM or SIGKILL")
	}
	if err := validTargets(req.Targets); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	result := workloadResult{Items: make([]workloadOutcome, 0, len(req.Targets))}
	var names []string
	var pids []int32
	for _, target := range req.Targets {
		outcome := workloadOutcome{PID: target.PID}
		if err := ctx.Err(); err != nil {
			outcome.Error = "interrupted before this process; measure again"
			result.Items = append(result.Items, outcome)
			continue
		}
		detail, err := s.workloadIdentity(ctx, target)
		if err == nil {
			err = s.modules.table.Signal(ctx, target.PID, req.Signal)
		}
		if err != nil {
			outcome.Error = workloadError(err)
		} else {
			outcome.OK = true
			result.Signalled++
			names = append(names, detail.Name)
			pids = append(pids, target.PID)
		}
		result.Items = append(result.Items, outcome)
	}
	httpx.SetAudit(r, "advisor.workloads.signal", workloadSubject(names, len(req.Targets)),
		map[string]any{"signal": req.Signal, "pids": pids, "signalled": result.Signalled, "requested": len(req.Targets)})
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func (s *Server) handleWorkloadPriority(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Targets []workloadTarget `json:"targets"`
		Nice    *int             `json:"nice"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// Only lowering: a batch that could raise priority would hand forty
	// processes the scheduler's favour in one press.
	if req.Nice == nil || *req.Nice < 0 || *req.Nice > 19 {
		return httpx.BadRequest("nice must be between 0 and 19")
	}
	if err := validTargets(req.Targets); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	result := workloadResult{Items: make([]workloadOutcome, 0, len(req.Targets))}
	var names []string
	var pids []int32
	for _, target := range req.Targets {
		outcome := workloadOutcome{PID: target.PID}
		if err := ctx.Err(); err != nil {
			outcome.Error = "interrupted before this process; measure again"
			result.Items = append(result.Items, outcome)
			continue
		}
		detail, err := s.workloadIdentity(ctx, target)
		switch {
		case err != nil:
			outcome.Error = workloadError(err)
		case int(detail.Nice) >= *req.Nice:
			outcome.OK, outcome.Skipped = true, true
		default:
			if err := s.modules.table.SetNice(ctx, target.PID, *req.Nice); err != nil {
				outcome.Error = err.Error()
			} else {
				outcome.OK = true
				result.Changed++
				names = append(names, detail.Name)
				pids = append(pids, target.PID)
			}
		}
		result.Items = append(result.Items, outcome)
	}
	httpx.SetAudit(r, "advisor.workloads.priority", workloadSubject(names, len(req.Targets)),
		map[string]any{"nice": *req.Nice, "pids": pids, "changed": result.Changed, "requested": len(req.Targets)})
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

// workloadIdentity is the per-process routes' checks for one target: it
// still exists, it is the process that was measured, and a signal or a
// priority would reach it at all.
func (s *Server) workloadIdentity(ctx context.Context, target workloadTarget) (*procs.Process, error) {
	detail, err := s.modules.table.Detail(ctx, target.PID)
	if err != nil {
		return nil, errWorkloadGone
	}
	if err := requireSameProcess(detail, target.StartedAt); err != nil {
		return nil, err
	}
	if err := procs.Controllable(detail); err != nil {
		return nil, err
	}
	return detail, nil
}

var errWorkloadGone = errors.New("the process has already exited")

// workloadError words a refusal for the item it belongs to. A batch answers
// 200 with each process's outcome, so the API errors that would have been a
// response's status are folded into the item's sentence.
func workloadError(err error) string {
	var apiErr *httpx.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Message
	}
	return err.Error()
}

func (s *Server) handleWorkloadAdvisor(w http.ResponseWriter, r *http.Request) error {
	order := r.URL.Query().Get("sort")
	if order == "" {
		order = "cpu"
	}
	switch order {
	case "cpu", "memory", "swap", "io", "handles":
	default:
		return httpx.BadRequest("sort must be cpu, memory, swap, io or handles")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	report, err := s.modules.table.Workloads(ctx, order)
	if err != nil {
		return httpx.Internal(err)
	}
	if s.modules.pm2.Available() {
		if managed, err := s.modules.pm2.List(ctx); err == nil {
			procs.MarkPM2(report.Processes, managed)
		} else {
			report.Silences = append(report.Silences, "PM2 ownership is unavailable.")
		}
	}
	httpx.JSON(w, http.StatusOK, report)
	return nil
}

func (s *Server) handleStorageAdvisor(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	report, err := s.modules.files.ScanStorage(ctx, r.URL.Query().Get("path"), r.URL.Query().Get("duplicates") == "true")
	if err != nil {
		return mapFileError(err)
	}
	httpx.JSON(w, http.StatusOK, report)
	return nil
}

func (s *Server) handleStorageCleanup(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Selections []files.StorageSelection `json:"selections"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	httpx.SetAudit(r, "advisor.storage.cleanup", "selected files", map[string]any{"selected": len(req.Selections)})
	result, err := s.modules.files.CleanupStorage(ctx, req.Selections)
	if result != nil {
		// Per-file failures and a deadline can follow successful removals. Keep
		// those outcomes visible and audited instead of hiding them in a 500.
		if err != nil {
			for _, selection := range req.Selections[len(result.Items):] {
				result.Items = append(result.Items, files.StorageCleanupItem{Path: selection.File.Path, Error: "cleanup interrupted; scan again"})
			}
		}
		httpx.SetAudit(r, "advisor.storage.cleanup", "selected files", map[string]any{"items": result.Items, "removedBytes": result.RemovedBytes})
		httpx.JSON(w, http.StatusOK, result)
		return nil
	}
	return mapFileError(err)
}
