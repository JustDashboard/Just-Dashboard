package api

import (
	"context"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// proxyExtras is what the proxy pages keep beyond proxysvc.Service itself:
// recorders, watchers and the stores behind them. It lives apart from
// moduleSet, and its setup and lifecycle apart from initModules, Start and
// Shutdown, because those are shared by every feature in the dashboard and
// the proxy's areas each add to them under their own heading here instead.
type proxyExtras struct {
	// --- lane A: engine & insights ---
	statusMetrics *proxysvc.StatusSampler

	// --- lane B: sites list & lifecycle ---

	// --- lane C: site builder ---

	// --- lane D: streams ---

	// modulePackages remembers which nginx module packages the package
	// manager has, for the module report and the Streams page's install.
	modulePackages modulePackages

	// --- lane E: ports & exposure ---

	// portHistory samples the host's listening sockets once a minute, so the
	// ports page can say what opened and closed while nobody was looking.
	portHistory *proxysvc.PortRecorder

	// --- lane F: certificates ---
	// localCA renews the local CA's certificates daily (handlers_certificates_private.go).
	localCA *localCARenewal
	// ct asks crt.sh for the certificates logged for this host's domains
	// (handlers_cert_transparency.go).
	ct *proxysvc.CTMonitor

	// --- lane G: TLS report & monitoring ---
	tlsMonitor *proxysvc.TLSMonitor
}

// initProxyExtras runs last in initModules, so the proxy service and every
// module it might report through (the audit log, the notification channels)
// already exist.
func (s *Server) initProxyExtras() {
	// --- lane A: engine & insights ---
	s.modules.proxyExtras.statusMetrics = proxysvc.NewStatusSampler(s.modules.proxy)

	// --- lane B: sites list & lifecycle ---

	// --- lane C: site builder ---

	// --- lane D: streams ---

	s.modules.proxyExtras.modulePackages.catalogue = s.modules.updates

	// --- lane E: ports & exposure ---
	s.modules.proxyExtras.portHistory = proxysvc.NewPortRecorder(s.Store.DB, s.Log)

	// --- lane F: certificates ---
	s.modules.proxyExtras.localCA = s.newLocalCARenewal()
	s.modules.proxyExtras.ct = proxysvc.NewCTMonitor()

	// --- lane G: TLS report & monitoring ---
	s.modules.proxyExtras.tlsMonitor = proxysvc.NewTLSMonitor(watchStore{s})
}

// startProxyExtras starts the proxy's background work from Start. Nothing
// here may hold the proxy service's lock while it waits: a loop that touches
// configuration files takes it with TryLock, so an operator's save is never
// queued behind a schedule.
func (s *Server) startProxyExtras(ctx context.Context) error {
	// --- lane A: engine & insights ---
	s.modules.proxyExtras.statusMetrics.Start(ctx)

	// --- lane B: sites list & lifecycle ---

	// --- lane C: site builder ---

	// --- lane D: streams ---

	// --- lane E: ports & exposure ---
	// Started here rather than on request because its whole purpose is to
	// have been running while nobody looked at the ports page.
	s.modules.proxyExtras.portHistory.Start(ctx)

	// --- lane F: certificates ---
	s.modules.proxyExtras.localCA.Start(ctx)

	// --- lane G: TLS report & monitoring ---
	s.modules.proxyExtras.tlsMonitor.Start(ctx)

	return nil
}

// stopProxyExtras runs from Shutdown, which also runs for a server that was
// built and never started — every test server — so each stop must be a no-op
// for work that never began.
func (s *Server) stopProxyExtras() {
	// --- lane A: engine & insights ---
	s.modules.proxyExtras.statusMetrics.Stop()

	// --- lane B: sites list & lifecycle ---

	// --- lane C: site builder ---

	// --- lane D: streams ---

	// --- lane E: ports & exposure ---
	s.modules.proxyExtras.portHistory.Stop()

	// --- lane F: certificates ---
	s.modules.proxyExtras.localCA.Stop()

	// --- lane G: TLS report & monitoring ---
	s.modules.proxyExtras.tlsMonitor.Stop()
}
