package api

import (
	"context"
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
