package api

import (
	"context"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// handleTLSDeepScan is the TLS report's deep scan: every suite, group and
// connection feature, on a request of its own so the quick report is already
// on screen while it runs. It sends forty-odd handshakes to a destination the
// caller chooses, so it sits behind system.admin with the other probes.
func (s *Server) handleTLSDeepScan(w http.ResponseWriter, r *http.Request) error {
	target, err := scanTarget(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	scan := proxysvc.DeepScanTLS(ctx, target.Host, target.Port, s.deepSite(ctx, target.Host, target.Port))
	httpx.JSON(w, http.StatusOK, scan)
	return nil
}

// deepSite is the site on this server serving host on port, with its form's
// HTTP/2 switch, for the deep scan to hold the negotiated protocol against.
// Nothing found is no comparison: the scan says what was negotiated and
// claims nothing about a form.
func (s *Server) deepSite(ctx context.Context, host string, port int) *proxysvc.DeepSite {
	vhosts, err := s.modules.proxy.ListVHosts(ctx)
	if err != nil {
		return nil
	}
	site := proxysvc.SiteForName(vhosts, host, port)
	if site == nil {
		return nil
	}
	content, err := s.modules.proxy.ReadConfig(site.Path)
	if err != nil {
		return nil
	}
	spec, _ := proxysvc.ParseSiteSpec(site.Name, content)
	return &proxysvc.DeepSite{Name: site.Name, HTTP2: spec.HTTP2}
}
