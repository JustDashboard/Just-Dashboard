package api

import (
	"errors"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dnsservice"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountNetworkDNSServiceRoutes(r chi.Router) {
	r.Route("/services", func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Cache-Control", "private, no-store")
				next.ServeHTTP(w, r)
			})
		})
		r.Method(http.MethodGet, "/", s.handle(s.handleDNSServiceList))
		r.Method(http.MethodGet, "/handoffs", s.handle(s.handleDNSServiceHandoffs))
		r.Method(http.MethodPost, "/", s.handle(s.handleDNSServiceConnect))
		r.Method(http.MethodGet, "/{id}", s.handle(s.handleDNSServiceInspect))
		r.Method(http.MethodGet, "/{id}/filters", s.handle(s.handleDNSServiceFilters))
		r.Method(http.MethodGet, "/{id}/dhcp", s.handle(s.handleDNSServiceDHCP))
		r.Method(http.MethodGet, "/{id}/zones/{zone}/records", s.handle(s.handleDNSServiceRecords))
		r.Method(http.MethodPut, "/{id}", s.handle(s.handleDNSServiceUpdate))
		r.Method(http.MethodPost, "/{id}/changes", s.handle(s.handleDNSServicePreview))
		r.Method(http.MethodGet, "/{id}/changes", s.handle(s.handleDNSServiceChanges))
		r.Method(http.MethodGet, "/changes/{change}", s.handle(s.handleDNSServiceChange))
		r.Method(http.MethodGet, "/changes/{change}/current", s.handle(s.handleDNSServiceCurrentChange))
		r.Method(http.MethodGet, "/provisions", s.handle(s.handleDNSProvisions))
		r.Method(http.MethodPost, "/provisions", s.handle(s.handleDNSProvisionPreview))
		r.Method(http.MethodGet, "/provisions/{provision}", s.handle(s.handleDNSProvision))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{id}", s.handle(s.handleDNSServiceDelete))
			r.Method(http.MethodPost, "/changes/{change}/apply", s.handle(s.handleDNSServiceApply))
			r.Method(http.MethodPost, "/provisions/{provision}/apply", s.handle(s.handleDNSProvisionApply))
			r.Method(http.MethodDelete, "/provisions/{provision}", s.handle(s.handleDNSProvisionRemove))
		})
	})
}

func (s *Server) handleDNSProvisions(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.modules.dnsServices.Provisions(r.Context())
	if err != nil {
		return mapDNSServiceError(err)
	}
	httpx.JSON(w, http.StatusOK, rows)
	return nil
}
func (s *Server) handleDNSProvisionPreview(w http.ResponseWriter, r *http.Request) error {
	var req dnsservice.ProvisionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	auditRequest := req
	auditRequest.Password = ""
	httpx.SetAudit(r, "network.dns.provision.preview", "", auditRequest)
	plan, err := s.modules.dnsServices.PreviewProvision(r.Context(), req)
	if err != nil {
		return mapDNSServiceError(err)
	}
	httpx.SetAudit(r, "network.dns.provision.preview", plan.ID, auditRequest)
	httpx.JSON(w, http.StatusCreated, plan)
	return nil
}
func (s *Server) handleDNSProvision(w http.ResponseWriter, r *http.Request) error {
	plan, err := s.modules.dnsServices.Provision(r.Context(), chi.URLParam(r, "provision"))
	if err != nil {
		return mapDNSServiceError(err)
	}
	httpx.JSON(w, http.StatusOK, plan)
	return nil
}
func (s *Server) handleDNSProvisionApply(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "provision")
	httpx.SetAudit(r, "network.dns.provision.apply", id, nil)
	plan, err := s.modules.dnsServices.ApplyProvision(r.Context(), id)
	if err != nil {
		return mapDNSServiceError(err)
	}
	httpx.SetAudit(r, "network.dns.provision.apply", id, map[string]any{"engine": plan.Request.Engine, "state": plan.State, "connectionId": plan.ConnectionID})
	httpx.JSON(w, http.StatusOK, plan)
	return nil
}
func (s *Server) handleDNSProvisionRemove(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "provision")
	httpx.SetAudit(r, "network.dns.provision.remove", id, nil)
	plan, err := s.modules.dnsServices.RemoveProvision(r.Context(), id)
	if err != nil {
		return mapDNSServiceError(err)
	}
	httpx.SetAudit(r, "network.dns.provision.remove", id, map[string]any{"engine": plan.Request.Engine, "state": plan.State})
	httpx.JSON(w, http.StatusOK, plan)
	return nil
}

func mapDNSServiceError(err error) error {
	switch {
	case errors.Is(err, dnsservice.ErrNotFound):
		return httpx.Err(http.StatusNotFound, "dns_service_not_found", "DNS service or reviewed change was not found.")
	case errors.Is(err, dnsservice.ErrReadOnly):
		return httpx.Err(http.StatusForbidden, "dns_service_read_only", "Management is disabled for this connection.")
	case errors.Is(err, dnsservice.ErrConflict):
		return httpx.Err(http.StatusConflict, "dns_service_changed", "DNS service changed; read and review a new plan.")
	case errors.Is(err, dnsservice.ErrUnavailable):
		return httpx.Err(http.StatusServiceUnavailable, "dns_service_unavailable", "Native DNS service management is unavailable.")
	default:
		return httpx.BadRequest("%s", err.Error())
	}
}

func dnsServiceAuditRequest(req dnsservice.ConnectionRequest) map[string]any {
	// Credentials and custom CA material never become audit request metadata.
	return map[string]any{"name": req.Name, "engine": req.Engine, "endpoint": req.Endpoint, "serverName": req.ServerName, "customCA": req.CA != "", "management": req.Management}
}

func (s *Server) handleDNSServiceList(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.modules.dnsServices.List(r.Context())
	if err != nil {
		return mapDNSServiceError(err)
	}
	httpx.JSON(w, http.StatusOK, rows)
	return nil
}
func (s *Server) handleDNSServiceConnect(w http.ResponseWriter, r *http.Request) error {
	var req dnsservice.ConnectionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "network.dns.service.connect", "", dnsServiceAuditRequest(req))
	view, err := s.modules.dnsServices.Connect(r.Context(), req)
	if err != nil {
		return mapDNSServiceError(err)
	}
	httpx.SetAudit(r, "network.dns.service.connect", view.Connection.ID, dnsServiceAuditRequest(req))
	httpx.JSON(w, http.StatusCreated, view)
	return nil
}
func (s *Server) handleDNSServiceInspect(w http.ResponseWriter, r *http.Request) error {
	view, err := s.modules.dnsServices.Inspect(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return mapDNSServiceError(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

func (s *Server) handleDNSServiceRecords(w http.ResponseWriter, r *http.Request) error {
	records, err := s.modules.dnsServices.Records(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "zone"))
	if err != nil {
		return mapDNSServiceError(err)
	}
	httpx.JSON(w, http.StatusOK, records)
	return nil
}

func (s *Server) handleDNSServiceFilters(w http.ResponseWriter, r *http.Request) error {
	view, err := s.modules.dnsServices.Filters(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return mapDNSServiceError(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

func (s *Server) handleDNSServiceDHCP(w http.ResponseWriter, r *http.Request) error {
	view, err := s.modules.dnsServices.DHCP(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return mapDNSServiceError(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

func (s *Server) handleDNSServiceCurrentChange(w http.ResponseWriter, r *http.Request) error {
	view, err := s.modules.dnsServices.CurrentChange(r.Context(), chi.URLParam(r, "change"))
	if err != nil {
		return mapDNSServiceError(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}
func (s *Server) handleDNSServiceUpdate(w http.ResponseWriter, r *http.Request) error {
	var req dnsservice.ConnectionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	id := chi.URLParam(r, "id")
	httpx.SetAudit(r, "network.dns.service.update", id, dnsServiceAuditRequest(req))
	view, err := s.modules.dnsServices.Update(r.Context(), id, req)
	if err != nil {
		return mapDNSServiceError(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}
func (s *Server) handleDNSServiceDelete(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	httpx.SetAudit(r, "network.dns.service.disconnect", id, nil)
	if err := s.modules.dnsServices.Delete(r.Context(), id); err != nil {
		return mapDNSServiceError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
func (s *Server) handleDNSServicePreview(w http.ResponseWriter, r *http.Request) error {
	var req dnsservice.ChangeRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	id := chi.URLParam(r, "id")
	httpx.SetAudit(r, "network.dns.service.preview", id, req)
	plan, err := s.modules.dnsServices.Preview(r.Context(), id, req)
	if err != nil {
		return mapDNSServiceError(err)
	}
	httpx.JSON(w, http.StatusCreated, plan)
	return nil
}
func (s *Server) handleDNSServiceChange(w http.ResponseWriter, r *http.Request) error {
	plan, err := s.modules.dnsServices.Change(r.Context(), chi.URLParam(r, "change"))
	if err != nil {
		return mapDNSServiceError(err)
	}
	httpx.JSON(w, http.StatusOK, plan)
	return nil
}
func (s *Server) handleDNSServiceChanges(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.modules.dnsServices.Changes(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return mapDNSServiceError(err)
	}
	httpx.JSON(w, http.StatusOK, rows)
	return nil
}
func (s *Server) handleDNSServiceApply(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "change")
	httpx.SetAudit(r, "network.dns.service.apply", id, nil)
	plan, err := s.modules.dnsServices.Apply(r.Context(), id)
	if err != nil {
		return mapDNSServiceError(err)
	}
	httpx.SetAudit(r, "network.dns.service.apply", id, map[string]any{"connectionId": plan.ConnectionID, "state": plan.State, "action": plan.Request.Action})
	httpx.JSON(w, http.StatusOK, plan)
	return nil
}
