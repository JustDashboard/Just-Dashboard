package api

import (
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// ctMonitorKey holds "1" while the Certificate Transparency monitor is on.
// It is off until an administrator turns it on, because every check sends
// this host's domain names to crt.sh.
const ctMonitorKey = "proxy.ct_monitor"

// mountCertTransparencyRoutes sits inside the certificates' admin group: the
// report names every domain this host serves and asks a third party about
// them.
func (s *Server) mountCertTransparencyRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/transparency", s.handle(s.handleCTReport))
	r.Method(http.MethodPut, "/transparency", s.handle(s.handleCTEnable))
	s.destructive(r, func(r chi.Router) {
		// Switching the monitor off loses nothing — the same switch puts
		// it back — so no phrase.
		r.Method(http.MethodDelete, "/transparency", s.handle(s.handleCTDisable))
	})
}

type ctResponse struct {
	Enabled bool `json:"enabled"`
	*proxysvc.CTReport
}

func (s *Server) ctEnabled(r *http.Request) (bool, error) {
	v, ok, err := s.Store.Setting(r.Context(), ctMonitorKey)
	return ok && v == "1", err
}

// handleCTReport answers {enabled:false} while the monitor is off, and asks
// nothing of crt.sh then.
func (s *Server) handleCTReport(w http.ResponseWriter, r *http.Request) error {
	enabled, err := s.ctEnabled(r)
	if err != nil {
		return httpx.Internal(err)
	}
	if !enabled {
		httpx.JSON(w, http.StatusOK, ctResponse{})
		return nil
	}
	// Past this a domain still being asked answers with its error; the
	// query goes on and its answer is cached for the next read.
	ctx, cancel := timeoutCtx(r, 2*time.Minute)
	defer cancel()
	report, err := s.modules.proxyExtras.ct.Transparency(ctx, s.modules.proxy)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, ctResponse{Enabled: true, CTReport: report})
	return nil
}

func (s *Server) handleCTEnable(w http.ResponseWriter, r *http.Request) error {
	if err := s.Store.SetSetting(r.Context(), ctMonitorKey, "1"); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "certificates.transparency.enable", "crt.sh", nil)
	httpx.JSON(w, http.StatusOK, ctResponse{Enabled: true})
	return nil
}

func (s *Server) handleCTDisable(w http.ResponseWriter, r *http.Request) error {
	if err := s.Store.SetSetting(r.Context(), ctMonitorKey, "0"); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "certificates.transparency.disable", "crt.sh", nil)
	httpx.JSON(w, http.StatusOK, ctResponse{})
	return nil
}
