package api

import (
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// mountServedCertificateRoutes is the comparison between the certificate
// each TLS site hands out and the file it names: a renewal nginx has not
// reloaded, or a name that falls through to another server block.
func (s *Server) mountServedCertificateRoutes(r chi.Router) {
	// Readable by any account, like the sites list it is drawn from. Every
	// dial goes to an address a site listens on and that belongs to this
	// host, taken from the configuration, never from the request.
	r.Method(http.MethodGet, "/tls/drift", s.handle(s.handleServedCertificates))
}

// handleServedCertificates answers from a five-minute cache. ?refresh=1 runs
// the check now, which handshakes with every TLS site, so only an account
// that may reload nginx can ask for it: the one moment it matters is right
// after a reload.
func (s *Server) handleServedCertificates(w http.ResponseWriter, r *http.Request) error {
	refresh := r.URL.Query().Get("refresh") == "1"
	if refresh && !httpx.MustPrincipal(r).Can(auth.CapSystemAdmin) {
		return httpx.Err(http.StatusForbidden, "forbidden",
			"your role does not permit this action ("+string(auth.CapSystemAdmin)+")")
	}
	httpx.JSON(w, http.StatusOK, s.modules.proxyExtras.servedCerts.Report(r.Context(), refresh))
	return nil
}
