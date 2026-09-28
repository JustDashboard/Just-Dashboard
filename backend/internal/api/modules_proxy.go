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

	// --- lane B: sites list & lifecycle ---

	// --- lane C: site builder ---

	// --- lane D: streams ---

	// --- lane E: ports & exposure ---

	// --- lane F: certificates ---
	// localCA renews the local CA's certificates daily (handlers_certificates_private.go).
	localCA *localCARenewal
	// ct asks crt.sh for the certificates logged for this host's domains
	// (handlers_cert_transparency.go).
	ct *proxysvc.CTMonitor

	// --- lane G: TLS report & monitoring ---
}

// initProxyExtras runs last in initModules, so the proxy service and every
// module it might report through (the audit log, the notification channels)
// already exist.
func (s *Server) initProxyExtras() {
	// --- lane A: engine & insights ---

	// --- lane B: sites list & lifecycle ---

	// --- lane C: site builder ---

	// --- lane D: streams ---

	// --- lane E: ports & exposure ---

	// --- lane F: certificates ---
	s.modules.proxyExtras.localCA = s.newLocalCARenewal()
	s.modules.proxyExtras.ct = proxysvc.NewCTMonitor()

	// --- lane G: TLS report & monitoring ---
}

// startProxyExtras starts the proxy's background work from Start. Nothing
// here may hold the proxy service's lock while it waits: a loop that touches
// configuration files takes it with TryLock, so an operator's save is never
// queued behind a schedule.
func (s *Server) startProxyExtras(ctx context.Context) error {
	// --- lane A: engine & insights ---

	// --- lane B: sites list & lifecycle ---

	// --- lane C: site builder ---

	// --- lane D: streams ---

	// --- lane E: ports & exposure ---

	// --- lane F: certificates ---
	s.modules.proxyExtras.localCA.Start(ctx)

	// --- lane G: TLS report & monitoring ---

	return nil
}

// stopProxyExtras runs from Shutdown, which also runs for a server that was
// built and never started — every test server — so each stop must be a no-op
// for work that never began.
func (s *Server) stopProxyExtras() {
	// --- lane A: engine & insights ---

	// --- lane B: sites list & lifecycle ---

	// --- lane C: site builder ---

	// --- lane D: streams ---

	// --- lane E: ports & exposure ---

	// --- lane F: certificates ---
	s.modules.proxyExtras.localCA.Stop()

	// --- lane G: TLS report & monitoring ---
}
