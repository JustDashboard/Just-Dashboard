package api

import (
	"errors"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netdiag"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netpath"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/go-chi/chi/v5"
)

// Saved probe output can expose LAN peers and decoded packet fields. Its reads
// carry the same privilege as the probe, including exports and job streams.
func (s *Server) mountNetworkDiagnosticRoutes(r chi.Router) {
	r.Route("/diagnostics", func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/", s.handle(s.handleDiagnosticList))
		r.Method(http.MethodPost, "/", s.handle(s.handleDiagnosticCreate))
		r.Method(http.MethodPost, "/investigate", s.handle(s.handleDiagnosticInvestigation))
		r.Method(http.MethodGet, "/policy", s.handle(s.handleDiagnosticPolicy))
		r.Method(http.MethodGet, "/compare", s.handle(s.handleDiagnosticCompare))
		r.Method(http.MethodPost, "/results", s.handle(s.handleDiagnosticSaveResult))
		r.Method(http.MethodGet, "/ssh-trust", s.handle(s.handleSSHTrustList))
		r.Method(http.MethodPut, "/ssh-trust", s.handle(s.handleSSHTrustSave))
		r.Method(http.MethodGet, "/wol-devices", s.handle(s.handleWakeDeviceList))
		r.Method(http.MethodPost, "/wol-devices", s.handle(s.handleWakeDeviceSave))
		r.Method(http.MethodPut, "/wol-devices/{device}", s.handle(s.handleWakeDeviceSave))
		r.Method(http.MethodGet, "/{id}/history", s.handle(s.handleDiagnosticHistory))
		r.Method(http.MethodGet, "/{id}", s.handle(s.handleDiagnosticGet))
		r.Method(http.MethodPatch, "/{id}", s.handle(s.handleDiagnosticSave))
		r.Method(http.MethodPost, "/{id}/cancel", s.handle(s.handleDiagnosticCancel))
		r.Method(http.MethodPost, "/{id}/rerun", s.handle(s.handleDiagnosticRerun))
		r.Method(http.MethodGet, "/{id}/export", s.handle(s.handleDiagnosticExport))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{id}", s.handle(s.handleDiagnosticDelete))
			// Reducing retention can erase finished records immediately.
			r.Method(http.MethodPut, "/policy", s.handle(s.handleDiagnosticPolicySet))
			// Forgetting a trusted fingerprint or a saved device erases what
			// later comparisons and wakes rely on.
			r.Method(http.MethodDelete, "/ssh-trust", s.handle(s.handleSSHTrustForget))
			r.Method(http.MethodDelete, "/wol-devices/{device}", s.handle(s.handleWakeDeviceDelete))
		})
	})
}

func mapDiagnosticError(err error) error {
	switch {
	case errors.Is(err, netdiag.ErrNotFound):
		return httpx.ErrNotFound
	case errors.Is(err, netdiag.ErrInvalid):
		return httpx.BadRequest("%v", err)
	case errors.Is(err, netdiag.ErrUnavailable):
		return httpx.Err(http.StatusServiceUnavailable, "diagnostics_unavailable", err.Error()).Retry()
	case errors.Is(err, netdiag.ErrBusy), errors.Is(err, netdiag.ErrNotRunning), errors.Is(err, netdiag.ErrRunning), errors.Is(err, netdiag.ErrIncompatible), errors.Is(err, netdiag.ErrTooMany):
		return httpx.Err(http.StatusConflict, "diagnostic_conflict", err.Error())
	default:
		return httpx.Internal(err)
	}
}

func diagnosticPrivate(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
}

func (s *Server) handleDiagnosticList(w http.ResponseWriter, r *http.Request) error {
	runs, err := s.modules.diagnostics.List(r.Context())
	if err != nil {
		return mapDiagnosticError(err)
	}
	// Lists carry metadata. Retrieve one run to read its bounded artifact.
	for i := range runs {
		runs[i].Result = nil
		runs[i].Investigation = nil
	}
	diagnosticPrivate(w)
	httpx.JSON(w, http.StatusOK, runs)
	return nil
}

func (s *Server) handleDiagnosticCreate(w http.ResponseWriter, r *http.Request) error {
	httpx.SetAudit(r, "network.diagnostic.create", "", nil)
	var req struct {
		Name    string              `json:"name"`
		Request netsec.ProbeRequest `json:"request"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "network.diagnostic.create", req.Request.Tool, map[string]any{"name": req.Name, "target": req.Request.Target})
	run, err := s.modules.diagnostics.Create(r.Context(), req.Name, req.Request, httpx.MustPrincipal(r).Username())
	if err != nil {
		return mapDiagnosticError(err)
	}
	httpx.SetAudit(r, "network.diagnostic.create", run.ID, map[string]any{"name": run.Name, "tool": run.Request.Tool, "target": run.Request.Target, "jobId": run.JobID})
	diagnosticPrivate(w)
	httpx.JSON(w, http.StatusAccepted, run)
	return nil
}

func (s *Server) handleDiagnosticInvestigation(w http.ResponseWriter, r *http.Request) error {
	httpx.SetAudit(r, "network.diagnostic.investigate", "", nil)
	var req struct {
		Name          string          `json:"name"`
		Investigation netpath.Request `json:"investigation"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "network.diagnostic.investigate", req.Investigation.Target, map[string]any{"sourceKind": req.Investigation.SourceKind, "family": req.Investigation.Family, "protocol": req.Investigation.Protocol, "port": req.Investigation.Port, "measure": req.Investigation.Measure})
	run, err := s.modules.diagnostics.CreateInvestigation(r.Context(), req.Name, req.Investigation, httpx.MustPrincipal(r).Username())
	if err != nil {
		return mapDiagnosticError(err)
	}
	httpx.SetAudit(r, "network.diagnostic.investigate", run.ID, map[string]any{"jobId": run.JobID, "sourceKind": req.Investigation.SourceKind, "family": req.Investigation.Family})
	diagnosticPrivate(w)
	httpx.JSON(w, http.StatusAccepted, run)
	return nil
}

func (s *Server) handleDiagnosticGet(w http.ResponseWriter, r *http.Request) error {
	run, err := s.modules.diagnostics.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return mapDiagnosticError(err)
	}
	diagnosticPrivate(w)
	httpx.JSON(w, http.StatusOK, run)
	return nil
}

func (s *Server) handleDiagnosticSave(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	httpx.SetAudit(r, "network.diagnostic.save", id, nil)
	var req struct {
		Name string `json:"name"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	run, err := s.modules.diagnostics.Save(r.Context(), id, req.Name)
	if err != nil {
		return mapDiagnosticError(err)
	}
	httpx.SetAudit(r, "network.diagnostic.save", id, map[string]any{"name": run.Name})
	diagnosticPrivate(w)
	httpx.JSON(w, http.StatusOK, run)
	return nil
}

func (s *Server) handleDiagnosticCancel(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	httpx.SetAudit(r, "network.diagnostic.cancel", id, nil)
	run, err := s.modules.diagnostics.Cancel(r.Context(), id)
	if err != nil {
		return mapDiagnosticError(err)
	}
	diagnosticPrivate(w)
	httpx.JSON(w, http.StatusAccepted, run)
	return nil
}

func (s *Server) handleDiagnosticRerun(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	httpx.SetAudit(r, "network.diagnostic.rerun", id, nil)
	run, err := s.modules.diagnostics.Rerun(r.Context(), id, httpx.MustPrincipal(r).Username())
	if err != nil {
		return mapDiagnosticError(err)
	}
	httpx.SetAudit(r, "network.diagnostic.rerun", id, map[string]any{"runId": run.ID, "jobId": run.JobID})
	diagnosticPrivate(w)
	httpx.JSON(w, http.StatusAccepted, run)
	return nil
}

func (s *Server) handleDiagnosticDelete(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	httpx.SetAudit(r, "network.diagnostic.delete", id, nil)
	if err := s.modules.diagnostics.Delete(r.Context(), id); err != nil {
		return mapDiagnosticError(err)
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleDiagnosticPolicy(w http.ResponseWriter, r *http.Request) error {
	policy, err := s.modules.diagnostics.Retention(r.Context())
	if err != nil {
		return mapDiagnosticError(err)
	}
	diagnosticPrivate(w)
	httpx.JSON(w, http.StatusOK, policy)
	return nil
}

func (s *Server) handleDiagnosticPolicySet(w http.ResponseWriter, r *http.Request) error {
	httpx.SetAudit(r, "network.diagnostic.retention", "", nil)
	var req netdiag.Retention
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "network.diagnostic.retention", "", map[string]any{"maxRuns": req.MaxRuns, "maxAgeHours": req.MaxAgeHours})
	if err := s.modules.diagnostics.SetRetention(r.Context(), req); err != nil {
		return mapDiagnosticError(err)
	}
	httpx.JSON(w, http.StatusOK, req)
	return nil
}

func (s *Server) handleDiagnosticExport(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	data, err := s.modules.diagnostics.Export(r.Context(), id)
	if err != nil {
		return mapDiagnosticError(err)
	}
	diagnosticPrivate(w)
	w.Header().Set("Content-Type", "application/json")
	// The ID is generated by the service; names never enter response headers.
	w.Header().Set("Content-Disposition", `attachment; filename="network-diagnostic-`+id+`.json"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
	return nil
}

func (s *Server) handleDiagnosticCompare(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if q.Get("before") == "" || q.Get("after") == "" {
		return httpx.BadRequest("before and after run IDs are required")
	}
	comparison, err := s.modules.diagnostics.Compare(r.Context(), q.Get("before"), q.Get("after"))
	if err != nil {
		return mapDiagnosticError(err)
	}
	diagnosticPrivate(w)
	httpx.JSON(w, http.StatusOK, comparison)
	return nil
}
