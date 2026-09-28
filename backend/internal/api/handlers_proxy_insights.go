package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// mountProxyInsightRoutes holds the readings derived from the engine rather
// than from one site or certificate. Mounted inside /proxy beside the engine,
// and kept in a file of its own so those readings grow without reopening the
// engine's routes.
func (s *Server) mountProxyInsightRoutes(r chi.Router) {
	s.mountProxyMetricRoutes(r)
	s.mountServedCertificateRoutes(r)
	// Whether each upstream answers. Every destination comes from the
	// configuration nginx loads, never from the request, and a read serves
	// the last check for 15 seconds, so polling cannot turn the dashboard
	// into a scanner.
	r.Method(http.MethodGet, "/upstreams", s.handle(s.handleProxyUpstreams(false)))
	r.Group(func(r chi.Router) {
		// A check on demand skips that cache, so it is the operator's.
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPost, "/upstreams/check", s.handle(s.handleProxyUpstreams(true)))
	})
}

// upstreamBudget covers a check that has to dump the configuration first
// and then wait out connect and HEAD timeouts eight addresses at a time.
const upstreamBudget = 45 * time.Second

func (s *Server) handleProxyUpstreams(fresh bool) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		if fresh {
			httpx.SetAudit(r, "proxy.upstreams.check", "", nil)
		}
		ctx, cancel := context.WithTimeout(r.Context(), upstreamBudget)
		defer cancel()
		report, err := s.modules.proxyExtras.upstreams.Report(ctx, fresh)
		var refused *proxysvc.DumpRefusedError
		switch {
		case errors.Is(err, proxysvc.ErrNoProxy):
			return httpx.Err(http.StatusServiceUnavailable, "no_nginx", "Upstream checks read nginx, which is not installed on this host.")
		case errors.As(err, &refused):
			return httpx.Err(http.StatusServiceUnavailable, "invalid_config",
				"nginx refuses its configuration, so there is nothing loaded to check.")
		case errors.Is(err, context.DeadlineExceeded) && r.Context().Err() == nil:
			return httpx.Err(http.StatusServiceUnavailable, "busy", "The upstream check did not finish in time.").Retry()
		case err != nil:
			return httpx.Wrap(http.StatusBadGateway, "upstreams_failed", err)
		}
		httpx.JSON(w, http.StatusOK, report)
		return nil
	}
}
