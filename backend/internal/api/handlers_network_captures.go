package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netcapture"
	"github.com/go-chi/chi/v5"
)

type captureNativeOwner interface {
	Ready() error
	Interfaces(context.Context) ([]netcapture.Interface, error)
	Capture(context.Context, netcapture.Request) (*netcapture.Result, error)
}

func (s *Server) mountNetworkCaptureRoutes(r chi.Router) {
	r.Route("/captures", func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method("GET", "/", s.handle(s.handleCaptureList))
		r.Method("GET", "/interfaces", s.handle(s.handleCaptureInterfaces))
		r.Method("POST", "/", s.handle(s.handleCaptureCreate))
		r.Method("GET", "/{id}", s.handle(s.handleCaptureGet))
		r.Method("GET", "/{id}/pcap", s.handle(s.handleCaptureArtifact))
		r.Method("GET", "/{id}/support", s.handle(s.handleCaptureSupport))
		r.Method("POST", "/{id}/cancel", s.handle(s.handleCaptureCancel))
		s.destructive(r, func(r chi.Router) { r.Method("DELETE", "/{id}", s.handle(s.handleCaptureDelete)) })
	})
}

func mapCaptureError(err error) error {
	switch {
	case errors.Is(err, netcapture.ErrInvalid):
		return httpx.BadRequest("%s", err)
	case errors.Is(err, netcapture.ErrNotFound):
		return httpx.ErrNotFound
	case errors.Is(err, netcapture.ErrRunning), errors.Is(err, netcapture.ErrBusy):
		return httpx.Err(409, "capture_refused", err.Error())
	case errors.Is(err, netcapture.ErrUnavailable):
		return httpx.Err(503, "capture_unavailable", err.Error())
	default:
		return httpx.Internal(err)
	}
}
func (s *Server) handleCaptureList(w http.ResponseWriter, r *http.Request) error {
	runs, err := s.modules.captures.List(r.Context())
	if err != nil {
		return mapCaptureError(err)
	}
	diagnosticPrivate(w)
	httpx.JSON(w, 200, map[string]any{"runs": runs, "maxRetained": netcapture.MaxRetained, "retentionHours": 24, "maxArtifactBytes": netcapture.MaxArtifactBytes, "maxRunning": netcapture.MaxRunning})
	return nil
}
func (s *Server) handleCaptureInterfaces(w http.ResponseWriter, r *http.Request) error {
	native := s.modules.captureNative
	if err := native.Ready(); err != nil {
		return mapCaptureError(err)
	}
	rows, err := native.Interfaces(r.Context())
	if err != nil {
		return httpx.Err(503, "capture_inventory_unreadable", err.Error())
	}
	diagnosticPrivate(w)
	httpx.JSON(w, 200, rows)
	return nil
}
func (s *Server) handleCaptureCreate(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name    string             `json:"name"`
		Request netcapture.Request `json:"request"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	validated, err := netcapture.Validate(req.Request)
	if err != nil {
		return mapCaptureError(err)
	}
	if _, err := netcapture.ValidateName(req.Name); err != nil {
		return mapCaptureError(err)
	}
	if validated.IncidentRunID != "" {
		if _, err := s.modules.diagnostics.Get(r.Context(), validated.IncidentRunID); err != nil {
			return httpx.Err(409, "capture_incident_absent", "The selected saved diagnostic is absent or unreadable; select it again")
		}
	}
	if err := s.modules.captureNative.Ready(); err != nil {
		return mapCaptureError(err)
	}
	httpx.SetAudit(r, "network.capture.create", validated.Interface, map[string]any{"family": validated.Family, "protocol": validated.Protocol, "packets": validated.Packets, "seconds": validated.Seconds, "maxBytes": validated.MaxBytes, "snapshotLength": validated.SnapshotLength, "incidentRunId": validated.IncidentRunID})
	run, err := s.modules.captures.Create(r.Context(), req.Name, validated, httpx.MustPrincipal(r).Username())
	if err != nil {
		return mapCaptureError(err)
	}
	httpx.SetAudit(r, "network.capture.create", run.ID, map[string]any{"jobId": run.JobID, "family": validated.Family, "packets": validated.Packets, "seconds": validated.Seconds})
	diagnosticPrivate(w)
	httpx.JSON(w, 202, run)
	return nil
}
func (s *Server) handleCaptureGet(w http.ResponseWriter, r *http.Request) error {
	run, err := s.modules.captures.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return mapCaptureError(err)
	}
	diagnosticPrivate(w)
	httpx.JSON(w, 200, run)
	return nil
}
func (s *Server) handleCaptureArtifact(w http.ResponseWriter, r *http.Request) error {
	run, data, err := s.modules.captures.Artifact(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return mapCaptureError(err)
	}
	httpx.AuditRead(s.Audit, r, "network.capture.download", run.ID)
	diagnosticPrivate(w)
	w.Header().Set("Content-Type", "application/vnd.tcpdump.pcap")
	w.Header().Set("Content-Disposition", `attachment; filename="network-capture-`+run.ID+`.pcap"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(200)
	_, err = w.Write(data)
	return err
}
func (s *Server) handleCaptureSupport(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	data, err := s.modules.captures.Support(r.Context(), id)
	if err != nil {
		return mapCaptureError(err)
	}
	httpx.AuditRead(s.Audit, r, "network.capture.support", id)
	diagnosticPrivate(w)
	w.Header().Set("Content-Disposition", `attachment; filename="network-capture-support-`+id+`.json"`)
	httpx.JSON(w, 200, json.RawMessage(data))
	return nil
}
func (s *Server) handleCaptureCancel(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	httpx.SetAudit(r, "network.capture.cancel", id, nil)
	run, err := s.modules.captures.Cancel(r.Context(), id)
	if err != nil {
		return mapCaptureError(err)
	}
	diagnosticPrivate(w)
	httpx.JSON(w, 200, run)
	return nil
}
func (s *Server) handleCaptureDelete(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	httpx.SetAudit(r, "network.capture.delete", id, nil)
	ctx, cancel := timeoutCtx(r, 5*time.Second)
	defer cancel()
	if err := s.modules.captures.Delete(ctx, id); err != nil {
		return mapCaptureError(err)
	}
	httpx.NoContent(w)
	return nil
}
