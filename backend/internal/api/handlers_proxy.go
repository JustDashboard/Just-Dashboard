package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// mountProxyRoutes is the whole proxy surface: the engine and its sites,
// streams and password files, certificates and the live TLS tools, and the
// host's listening ports.
//
// Each area mounts its own routes from its own file, beside its handlers, so
// the capability gate a route sits behind reads next to the code it guards and
// work on one area never has to touch another's mount. The paths, methods and
// gates are pinned by TestProxyRoutesKeepTheirPaths.
func (s *Server) mountProxyRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(withProxyActor)
		r.Route("/proxy", func(r chi.Router) {
			s.mountEngineRoutes(r)
			s.mountVHostRoutes(r)
			s.mountProxyInsightRoutes(r)
			s.mountSiteFileRoutes(r)
		})
		r.Route("/proxy/sites", func(r chi.Router) {
			s.mountSiteBuilderRoutes(r)
			s.mountSiteOpsRoutes(r)
		})
		r.Route("/proxy/realip", s.mountRealIPRoutes)
		r.Route("/proxy/streams", s.mountStreamRoutes)
		r.Route("/proxy/modules", s.mountModuleRoutes)
		r.Route("/proxy/auth-files", s.mountAuthFileRoutes)
		r.Route("/proxy/tools", s.mountProxyToolRoutes)
		r.Route("/certificates", func(r chi.Router) {
			s.mountCertificateRoutes(r)
			s.mountTLSRoutes(r)
			s.mountSiteCertificateRoutes(r)
		})
		r.Route("/ports", s.mountPortRoutes)
	})
}

// withProxyActor tells the proxy service who is asking. The service records
// every configuration file it changes, and a record that cannot say whose
// change it was is no help to the operator reading it after an outage.
func withProxyActor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := proxysvc.WithActor(r.Context(), httpx.MustPrincipal(r).Username())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func mapProxyError(err error) error {
	var unfinished *proxysvc.UnfinishedTestError
	switch {
	case errors.Is(err, proxysvc.ErrUnsafePath):
		return httpx.Err(http.StatusForbidden, "outside_root", err.Error())
	case errors.Is(err, proxysvc.ErrProtectedFile):
		return httpx.Err(http.StatusForbidden, "protected_file", err.Error())
	case errors.Is(err, proxysvc.ErrNoProxy):
		return httpx.Err(http.StatusServiceUnavailable, "no_proxy", err.Error())
	case errors.Is(err, proxysvc.ErrNoIngress):
		return httpx.Err(http.StatusConflict, "no_ingress", err.Error())
	case errors.As(err, &unfinished):
		// Not a refusal: the page shows it as a test that did not answer and
		// offers it again, never as a configuration the engine turned down.
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		return httpx.Err(status, "test_unfinished", err.Error()).
			Because("A test that did not finish says nothing about the configuration either way, and nothing was changed.", unfinished.Output).
			Retry()
	default:
		return httpx.BadRequest("%v", err)
	}
}
