package api

import (
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

// Mount inside the existing /network/dns router. Private policy, queries and
// retained answers require admin even though the classic lookup remains read.
func (s *Server) mountNetworkDNSEvidenceRoutes(r chi.Router) {
	r.Route("/evidence", func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPost, "/", s.handle(s.handleDNSEvidenceCreate))
		r.Method(http.MethodGet, "/", s.handle(s.handleDNSEvidenceList))
		r.Method(http.MethodGet, "/{id}", s.handle(s.handleDNSEvidenceGet))
		r.Method(http.MethodGet, "/{id}/export", s.handle(s.handleDNSEvidenceExport))
		s.destructive(r, func(r chi.Router) { r.Method(http.MethodDelete, "/{id}", s.handle(s.handleDNSEvidenceDelete)) })
	})
}
func dnsEvidencePrivate(w http.ResponseWriter) { w.Header().Set("Cache-Control", "private, no-store") }
func (s *Server) handleDNSEvidenceCreate(w http.ResponseWriter, r *http.Request) error {
	var req netx.DNSInvestigationRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	clean, err := netx.ValidateDNSInvestigation(req)
	if err != nil {
		return httpx.BadRequest("%s", err.Error())
	}
	ctx, cancel := timeoutCtx(r, 25*time.Second)
	defer cancel()
	// Audit only the bounded normalized query and generated ID, never native
	// process details, trust stores, configuration files or raw command output.
	httpx.SetAudit(r, "network.dns.investigate", "", clean)
	record, err := s.modules.network.CreateDNSEvidence(ctx, clean, actor(r), nil)
	if record.ID != "" {
		httpx.SetAudit(r, "network.dns.investigate", record.ID, clean)
	}
	if err != nil {
		return mapDNSError(err)
	}
	dnsEvidencePrivate(w)
	httpx.JSON(w, http.StatusCreated, record)
	return nil
}
func (s *Server) handleDNSEvidenceList(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.modules.network.ListDNSEvidence(r.Context())
	if err != nil {
		return mapDNSError(err)
	}
	dnsEvidencePrivate(w)
	httpx.JSON(w, http.StatusOK, rows)
	return nil
}
func (s *Server) handleDNSEvidenceGet(w http.ResponseWriter, r *http.Request) error {
	row, err := s.modules.network.DNSEvidence(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return mapDNSError(err)
	}
	dnsEvidencePrivate(w)
	httpx.JSON(w, http.StatusOK, row)
	return nil
}
func (s *Server) handleDNSEvidenceExport(w http.ResponseWriter, r *http.Request) error {
	row, err := s.modules.network.DNSEvidence(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return mapDNSError(err)
	}
	dnsEvidencePrivate(w)
	w.Header().Set("Content-Disposition", `attachment; filename="dns-evidence-`+row.ID+`.json"`)
	httpx.JSON(w, http.StatusOK, map[string]any{"version": 1, "investigation": row})
	return nil
}
func (s *Server) handleDNSEvidenceDelete(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	if err := s.modules.network.DeleteDNSEvidence(r.Context(), id); err != nil {
		return mapDNSError(err)
	}
	httpx.SetAudit(r, "network.dns.evidence.delete", id, nil)
	w.WriteHeader(http.StatusNoContent)
	return nil
}
