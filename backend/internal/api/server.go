package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/agent"
	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/config"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfcfg"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/Wayy01/Just-Dashboard/backend/internal/wsx"
)

// Server owns the dependency graph the handlers close over. Feature modules
// are attached as fields so a module that cannot initialise on this host
// (no Docker socket, no systemd) degrades to a clear error on its own routes
// instead of preventing the dashboard from starting.
type Server struct {
	Cfg         *config.Config
	Log         *slog.Logger
	Store       *store.Store
	Auth        *auth.Service
	Sealer      *auth.Sealer
	Audit       *audit.Logger
	ingressStop context.CancelFunc
	ingressDone chan struct{}
	Authn       *httpx.Authenticator
	WS          *wsx.Upgrader
	// Agent is non-nil only in agent mode, where it is both the TLS identity
	// and the record of which hub this server answers to.
	Agent    *agent.Identity
	loginLim *httpx.Limiter
	apiLim   *httpx.Limiter
	destrLim *httpx.Limiter
	// Disk usage recursively visits client-selected trees. Two concurrent
	// scans are enough for the UI without letting requests multiply host I/O.
	diskScans chan struct{}

	modules moduleSet
}

func New(cfg *config.Config, log *slog.Logger, st *store.Store, svc *auth.Service, sealer *auth.Sealer, aud *audit.Logger, id *agent.Identity) *Server {
	s := &Server{
		Cfg:    cfg,
		Log:    log,
		Store:  st,
		Auth:   svc,
		Sealer: sealer,
		Audit:  aud,
		Agent:  id,
		// Secure is dropped in exactly two configurations, and both of them are
		// plain HTTP by design: local development, and the ssh-tunnel install
		// where the proxy serves http://localhost because the tunnel is
		// already the encrypted, authenticated hop. Most browsers do accept a
		// Secure cookie from localhost — they treat it as a trustworthy origin
		// — but not all of them have, and a session cookie the browser
		// silently declines to store is a login page that simply loops.
		Authn: &httpx.Authenticator{Svc: svc, Secure: !cfg.Dev && cfg.TLSMode != selfcfg.TLSOff},
		WS:    wsx.NewUpgrader(cfg.AllowedOrigins, !cfg.Dev),
		// Login is deliberately tight: five attempts a minute per address on
		// top of the per-account lockout.
		loginLim: httpx.NewLimiter(10, 5),
		apiLim:   httpx.NewLimiter(600, 120),
		// Destructive routes get their own budget so a scripted delete loop
		// cannot run away even with a valid admin token.
		destrLim:  httpx.NewLimiter(30, 10),
		diskScans: make(chan struct{}, 2),
	}
	s.initModules()
	return s
}

func (s *Server) handle(fn httpx.Handler) http.Handler { return fn }

// Start brings up the background workers the API owns. It is separate from
// New so that a failure to schedule backups is reported by main rather than
// swallowed during construction.
func (s *Server) Start(ctx context.Context) error {
	// The metrics recorder is started here rather than lazily on the first
	// request precisely because nothing may ever request it: its whole
	// purpose is to have been running while nobody was looking.
	if err := s.modules.metrics.Start(ctx); err != nil {
		return err
	}
	// The Docker event log is started for the same reason and never fails:
	// Docker's stream is the only record of an OOM kill or a health check
	// going red, and the daemon keeps none of it. A host with no Docker
	// simply never connects, which is a steady state rather than an error.
	s.modules.dockerEvents.Start(ctx)
	// Two things, both of which have to happen at boot rather than on request.
	// An upgrade that was in flight when this process started is settled here,
	// because after a successful one *this* process is the evidence it worked
	// and nothing else will ever look. And the periodic version check is
	// started for the reason the metrics recorder is: the operator who most
	// needs to be told about a release is the one who has not opened the
	// dashboard in a month.
	s.modules.selfUpdate.Start(ctx)
	// The same settling, for a restart rather than an upgrade. After a
	// successful one this process is likewise the only evidence it worked,
	// because the sibling that carried it out has already exited.
	s.modules.selfConfig.Start(ctx)
	// Issued and renewed here rather than at install time, because the
	// certificate this obtains lives 90 days and the install that most needs
	// it is the one nobody has touched since.
	s.modules.certKeeper.Start(ctx)
	if err := s.modules.backupSched.Start(ctx); err != nil {
		return err
	}
	ingressCtx, stopIngress := context.WithCancel(ctx)
	s.ingressStop, s.ingressDone = stopIngress, make(chan struct{})
	go func() {
		defer close(s.ingressDone)
		s.modules.proxy.MaintainDockerIngress(ingressCtx, func(name string, success bool) {
			s.Audit.Record(context.Background(), audit.Entry{Actor: "system", Action: "proxy.ingress.reconcile", Target: name, Success: success})
		}, func(err error) { s.Log.Warn("deployment ingress recovery needs attention", "error", err) })
	}()
	s.modules.deploySchedule.Start(ctx)
	return s.modules.deployEngine.Start(ctx)
}

// Shutdown releases the resources that outlive a request: database pools,
// live PTY sessions, the metrics sampler and the backup scheduler.
func (s *Server) Shutdown() {
	// Stop fresh claims first. Active work is given a bounded grace to reach a
	// persisted boundary; Engine.Shutdown never injects a cancellation into an
	// activation or restoration.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := s.modules.deployEngine.Shutdown(ctx); err != nil && s.Log != nil {
		s.Log.Warn("deployment engine did not finish before shutdown", "err", err)
	}
	cancel()
	if s.ingressStop != nil {
		s.ingressStop()
		<-s.ingressDone
	}
	s.modules.metrics.Stop()
	s.modules.backupSched.Stop()
	s.modules.deploySchedule.Stop()
	s.modules.selfUpdate.Stop()
	s.modules.certKeeper.Stop()
	s.modules.term.Shutdown()
	s.modules.dbs.Shutdown()
	s.modules.docker.Close()
	// Jobs are deliberately not cancelled: a certificate issuance or a package
	// upgrade interrupted halfway is worse than one that completes into a
	// dashboard that has restarted. Only the subscribers are released.
	s.modules.jobs.Shutdown()
}
