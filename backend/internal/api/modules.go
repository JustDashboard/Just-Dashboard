package api

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gameserver"
	"github.com/Wayy01/Just-Dashboard/backend/internal/ghx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gitx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/linuxusers"
	"github.com/Wayy01/Just-Dashboard/backend/internal/logsx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfcfg"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate"
	"github.com/Wayy01/Just-Dashboard/backend/internal/sysinfo"
	"github.com/Wayy01/Just-Dashboard/backend/internal/term"
	"github.com/Wayy01/Just-Dashboard/backend/internal/updates"
	"github.com/Wayy01/Just-Dashboard/backend/internal/version"
)

// moduleSet holds the feature backends. Each is optional: a host without
// Docker, systemd or PM2 still serves everything else, and the corresponding
// routes report a precise "unavailable on this host" error instead.
type moduleSet struct {
	sys          *sysinfo.Collector
	metrics      *metrics.Recorder
	docker       *dockerx.Client
	dockerStats  *dockerx.StatsSampler
	dockerEvents *dockerx.EventLog
	// What a compose stack was before it was changed. Docker keeps no
	// history, so a rollback has no target and "what changed" has no answer
	// unless the dashboard writes it down first.
	dockerDeploys *dockerx.DeploymentStore
	pm2           *procs.PM2
	systemd       *procs.Systemd
	table         *procs.Table
	cron          *procs.Cron
	logs          *logsx.Service
	term          *term.Manager
	files         *files.Service
	git           *gitx.Service
	github        *ghx.Service
	updates       *updates.Service
	selfUpdate    *selfupdate.Service
	selfConfig    *selfcfg.Service
	certKeeper    *selfcfg.CertKeeper
	proxy         *proxysvc.Service
	dbs           *dbx.Manager
	linuxUsers    *linuxusers.Service
	netsec        *netsec.Service
	// jobs runs the operations that take longer than a request should:
	// certbot, package upgrades, sshd applies. They outlive the request that
	// started them and are watched by id rather than by the socket.
	jobs             *jobs.Manager
	backupStore      *backups.Store
	backupRunner     *backups.Runner
	backupSched      *backups.Scheduler
	deployStore      *deploy.Store
	deployer         *deploy.Deployer
	deployRuns       *deploy.OrchestrationStore
	deployEngine     *deploy.Engine
	deployPlanning   *deploy.PlanningStore
	deploySources    *deploy.HostSourceAnalyzer
	deployPreflight  *deploy.HostPreflightObserver
	deployArtifacts  *deploy.ArtifactBuilder
	deployAutomation *deploy.AutomationStore
	deploySchedule   *deploy.AutomationScheduler
	deployGit        *deploy.GitWatcher
	deployDatabases  *deploymentDatabaseNetworks
	deployPreviews   *deploy.PreviewQuarantineController
	// Upstream game-version metadata, behind one bounded client and a short
	// cache so opening the wizard does not hammer somebody else's API.
	gameVersions *gameserver.Adapter
}

func (s *Server) initModules() {
	s.modules.sys = sysinfo.NewCollector()
	s.modules.docker = dockerx.New(s.Cfg.DockerHost)
	s.modules.dockerStats = s.modules.docker.NewStatsSampler()
	s.modules.dockerEvents = s.modules.docker.NewEventLog(s.Log)
	s.modules.dockerDeploys = dockerx.NewDeploymentStore(s.Store.DB)
	// The recorder gets a sampler of its own rather than the shared one: a
	// series kept for a week is worth measuring over even intervals, and an
	// operator refreshing the container table would otherwise keep shortening
	// the window the next stored sample is differenced over.
	s.modules.metrics = metrics.New(s.Store, s.Log, s.Cfg.MetricsInterval, s.Cfg.MetricsRetention).
		WithContainers(s.modules.docker.NewStatsSampler())
	// Docker's disk accounting walks every layer and volume, which takes
	// seconds on a busy host. Priming it here means the first operator to open
	// the Volumes tab reads a warm cache instead of waiting for the walk.
	s.modules.docker.WarmCaches()
	s.modules.pm2 = procs.NewPM2()
	s.modules.systemd = procs.NewSystemd()
	s.modules.table = procs.NewTable()
	s.modules.cron = procs.NewCron()
	s.modules.logs = logsx.New(s.Cfg.LogRoots)
	s.modules.term = term.NewManager(s.Cfg.TerminalEnable, s.Cfg.TerminalShell, s.Cfg.TerminalUser)
	if s.Cfg.TerminalEnable {
		if err := s.modules.term.SetupShell(); err != nil {
			s.Log.Warn("terminal prompt setup unavailable", "error", err)
		}
	}
	s.modules.files = files.New(s.Cfg.FileRoots)
	s.modules.gameVersions = gameserver.New()
	s.modules.git = gitx.New(s.Cfg.GitRoots)
	s.modules.github = ghx.New()
	s.modules.updates = updates.New()
	// The one module that manages the dashboard rather than the server. It is
	// given a *function* for listing containers rather than the Docker client,
	// so that working out which container this dashboard is — the part with
	// all the decisions in it — is testable without a daemon.
	s.modules.selfUpdate = selfupdate.New(selfupdate.Options{
		Current:     version.Version,
		DataDir:     s.Cfg.DataDir,
		UpdateDir:   s.Cfg.UpdateDir,
		Repo:        s.Cfg.UpdateRepo,
		Ref:         s.Cfg.UpdateBranch,
		CheckOnline: s.Cfg.UpdateCheck,
		DockerHost:  s.Cfg.DockerHost,
		Health:      s.healthURL(),
		List:        s.listSiblings,
		Log:         s.Log,
	})
	// The settings half of the same idea, and it shares the update module's
	// answer to "where is this install" rather than working it out again: two
	// packages asking Docker the same question would be two chances to
	// disagree about which container this dashboard is.
	s.modules.selfConfig = selfcfg.New(selfcfg.Options{
		DataDir:    s.Cfg.DataDir,
		DockerHost: s.Cfg.DockerHost,
		Locate:     s.modules.selfUpdate.Location,
		Observed:   s.observedConfig,
		List:       s.listSiblings,
		Log:        s.Log,
	})
	// The dashboard's own certificate, as opposed to the certificates it
	// manages for the server's sites. It renews itself and restarts the proxy
	// that serves it, because Caddy reads a file-based certificate once.
	s.modules.certKeeper = selfcfg.NewCertKeeper(
		s.Cfg.Site, s.Cfg.TLSMode, s.Cfg.DataDir, s.restartProxy, s.Log)
	s.modules.proxy = proxysvc.NewWithDockerIngress(s.Cfg.NginxDir, s.Cfg.CaddyFile)
	s.modules.dbs = dbx.NewManager()
	s.modules.linuxUsers = linuxusers.New()
	s.modules.netsec = netsec.New()
	s.modules.jobs = jobs.New(s.Log)

	databaseDumper := &backupDatabaseDumper{server: s}
	s.modules.backupStore = backups.NewStore(s.Store, s.Sealer, s.modules.files).
		WithDatabaseValidator(databaseDumper.DescribeDatabase)
	s.modules.backupRunner = backups.NewRunner(s.modules.backupStore,
		filepath.Join(s.Cfg.DataDir, "staging"), s.Log).
		WithRecoveryChecker(&backupRecoveryChecker{server: s}).
		WithDatabaseDumper(databaseDumper).
		WithSQLiteSnapshotter(func(ctx context.Context, source, directory string) (string, error) {
			dump, err := dbx.Dump(ctx, dbx.DriverSQLite, source, "", directory)
			if err != nil {
				return "", err
			}
			return dump.Path, nil
		})
	s.modules.backupSched = backups.NewScheduler(s.modules.backupStore, s.modules.backupRunner, s.Log)

	s.modules.deployStore = deploy.NewStore(s.Store, s.Sealer, s.Cfg.DeployRoots)
	s.modules.deployer = deploy.NewDeployer(s.modules.deployStore, s.Log)
	s.modules.deployRuns = deploy.NewOrchestrationStore(s.Store)
	s.modules.deployDatabases = &deploymentDatabaseNetworks{server: s}
	s.modules.deployPlanning = deploy.NewPlanningStore(s.Store, s.Sealer, s.Cfg.DeployRoots).WithDatabaseURLResolver(s.modules.deployDatabases.ResolveVariable)
	s.modules.deployAutomation = deploy.NewAutomationStore(s.Store, s.Sealer)
	s.modules.deploySources = deploy.NewHostSourceAnalyzer(
		s.Cfg.DeployRoots,
		s.Cfg.ComposeRoots,
		filepath.Join(s.Cfg.DataDir, "deployment-detection"),
		s.modules.docker,
		s.modules.deployPlanning,
	)
	s.modules.deployPreflight = deploy.NewHostPreflightObserver(
		s.Cfg.DeployRoots,
		s.Cfg.DataDir,
		s.modules.docker,
		s.modules.proxy,
	).WithFirewall(s.modules.netsec).WithDependencies(newDeploymentDependencyObserver(
		s.Store, s.modules.backupStore, s.modules.docker,
	))
	artifactBackend := deploy.NewDockerArtifactBackend(s.modules.docker)
	s.modules.deployArtifacts = deploy.NewArtifactBuilder(artifactBackend)
	runtimeOwner := deploy.NewDockerRuntimeOwner(s.modules.docker).WithNetworks(s.modules.deployDatabases)
	s.modules.deployPreviews = deploy.NewPreviewQuarantineController(s.modules.deployRuns, runtimeOwner, s.modules.proxy,
		func(ctx context.Context, environmentID int64, phase string, success bool) {
			s.Audit.Record(ctx, audit.Entry{Actor: "system", Action: "deploy.preview.quarantine." + phase, Target: strconv.FormatInt(environmentID, 10), Success: success})
		}, func(err error) { s.Log.Warn("preview isolation needs attention", "error", err) })
	normalizedExecutor := deploy.NewNormalizedStepExecutor(
		s.modules.deployRuns,
		s.modules.deployPlanning,
		s.modules.deploySources,
		s.modules.deployArtifacts,
		runtimeOwner,
		deploy.NewCheckRunner(s.modules.docker),
		s.modules.proxy,
		filepath.Join(s.Cfg.DataDir, "deployment-workspaces"),
	).WithPreflightObserver(s.modules.deployPreflight).
		WithBackupGate(newDeploymentBackupGate(s.modules.backupStore, s.modules.backupRunner, s.modules.docker).WithDatabaseSources(s.databaseBackupSources)).
		// Automatic HTTPS. Issuance is still the Certificates feature's code
		// and activation still resolves an already-existing pair; what this
		// join adds is that the run asks for one before it starts anything,
		// instead of reaching a cutover that has nothing to serve.
		WithCertificateIssuer(s.modules.proxy).
		WithNotifications(s.modules.deployAutomation)
	s.modules.deployEngine = deploy.NewEngine(
		s.modules.deployRuns,
		deploy.NewDeploymentStepExecutor(
			deploy.NewLegacyStepExecutor(s.modules.deployer),
			normalizedExecutor,
		),
		deploy.NewDeploymentReconciler(s.modules.deployRuns),
		deploy.EngineConfig{
			Budget: deploy.QueueBudget{
				Heavy: s.Cfg.DeployHeavySlots,
				Light: s.Cfg.DeployLightSlots,
			},
			LeaseTTL: s.Cfg.DeployLeaseTTL,
		},
		s.Log,
	)
	// Every terminal outcome reaches its audience, not only the release
	// path's success step: a failed deployment is the one somebody most
	// needs to hear about.
	notifications := deploy.NewNotificationDispatcher(s.modules.deployAutomation, s.modules.deployRuns, s.dashboardEndpoint, s.Log)
	s.modules.deployEngine.WithObservers(
		notifications,
		deploy.NewCommitStatusPublisher(s.modules.deployRuns, githubStatusPoster{s.modules.github}, s.dashboardEndpoint, s.Log),
	)
	s.modules.deploySchedule = deploy.NewAutomationScheduler(s.modules.deployAutomation, s.dispatchDeploymentSchedule).
		WithSweep(notifications.RetryFailedDeliveries)
	s.modules.deployGit = deploy.NewGitWatcher(s.modules.deployRuns, s.modules.deploySources, s.dispatchGitDeployment)
}

// restartProxy restarts the Caddy container in this dashboard's own stack.
//
// By compose service rather than by container name: the name carries a project
// prefix and an index, both of which an operator can change, while
// `com.docker.compose.service=proxy` is what the file says and cannot drift
// from it.
func (s *Server) restartProxy(ctx context.Context) error {
	list, err := s.listSiblings(ctx)
	if err != nil {
		return err
	}
	loc, locErr := s.modules.selfUpdate.Location(ctx)
	for _, c := range list {
		if c.Service != "proxy" {
			continue
		}
		// On a host running more than one of these stacks, only ours.
		if locErr == nil && loc.Project != "" && c.Project != loc.Project {
			continue
		}
		return s.modules.docker.Lifecycle(ctx, c.ID, dockerx.ActionRestart, nil)
	}
	return errors.New("no proxy container in this stack to restart")
}

// observedConfig is what this process is actually running, for the settings
// page to compare against the file on disk.
//
// Only what this backend can see for itself: it binds its own port and
// enforces its own allowlist, but the dashboard port belongs to Caddy and the
// frontend port to Next, and inventing values for those would turn "your file
// and your running dashboard disagree" — a genuinely useful warning — into
// noise nobody reads.
func (s *Server) observedConfig() selfcfg.Observed {
	obs := selfcfg.Observed{
		Site:            s.Cfg.Site,
		TLS:             s.Cfg.TLSMode,
		TerminalEnabled: s.Cfg.TerminalEnable,
		Require2FA:      s.Cfg.Require2FA,
		SessionTTL:      s.Cfg.SessionTTL.String(),
		IdleTTL:         s.Cfg.IdleTTL.String(),
		UpdateCheck:     s.Cfg.UpdateCheck,
		AllowedCIDRs:    joinCIDRs(s.Cfg.AllowedCIDRs),
	}
	if _, port, err := net.SplitHostPort(s.Cfg.Addr); err == nil {
		obs.BackendPort, _ = strconv.Atoi(port)
	}
	return obs
}

// joinCIDRs renders the parsed allowlist the way .env spells it, so a
// comparison between the two is comparing like with like.
func joinCIDRs(nets []*net.IPNet) string {
	parts := make([]string, 0, len(nets))
	for _, n := range nets {
		parts = append(parts, n.String())
	}
	return strings.Join(parts, ",")
}

// listSiblings is how internal/selfupdate sees this host's containers.
//
// All of them, not just the running ones: the updater container it is looking
// for may have exited, and "the container is gone" and "the container failed"
// are the two answers that decide whether an upgrade is reported as finished
// or as abandoned.
func (s *Server) listSiblings(ctx context.Context) ([]selfupdate.Sibling, error) {
	list, err := s.modules.docker.ListContainers(ctx, true)
	if err != nil {
		return nil, err
	}
	out := make([]selfupdate.Sibling, 0, len(list))
	for _, c := range list {
		sib := selfupdate.Sibling{
			ID: c.ID, Name: c.Name, Image: c.Image, State: c.State,
			Project: c.ComposeStack, Service: c.ComposeSvc,
			WorkDir: c.Labels["com.docker.compose.project.working_dir"],
		}
		for _, f := range strings.Split(c.Labels["com.docker.compose.project.config_files"], ",") {
			if f = strings.TrimSpace(f); f != "" {
				sib.ConfigFiles = append(sib.ConfigFiles, f)
			}
		}
		for _, m := range c.Mounts {
			sib.Mounts = append(sib.Mounts, selfupdate.Mount{Source: m.Source, Destination: m.Destination})
		}
		out = append(out, sib)
	}
	return out, nil
}

// healthURL is the address the rebuilt dashboard will answer on, which the
// updater probes before calling an upgrade finished.
//
// It is derived from the address this server binds because that is the address
// the next one will bind: the updater is given no configuration of its own, so
// anything it needs to know travels in the job record. A wildcard bind is
// rewritten to loopback — the updater runs on the host network, and asking
// 0.0.0.0 for a page is not a question.
func (s *Server) healthURL() string {
	addr := s.Cfg.Addr
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	scheme := "http"
	if s.Cfg.AgentMode {
		// An agent serves TLS on this same address. The certificate is
		// self-signed by design, so the probe there is about liveness only.
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(host, port) + "/healthz"
}

// githubStatusPoster adapts the GitHub CLI service to the deploy package's
// narrow commit-status contract.
type githubStatusPoster struct{ github *ghx.Service }

func (p githubStatusPoster) PostCommitStatus(ctx context.Context, nameWithOwner, sha string, status deploy.CommitStatus) error {
	if p.github == nil {
		return ghx.ErrNotInstalled
	}
	return p.github.PostCommitStatus(ctx, nameWithOwner, sha, ghx.CommitStatus{
		State: status.State, TargetURL: status.TargetURL, Description: status.Description, Context: status.Context,
	})
}

// dashboardEndpoint is the address links in notifications and commit statuses
// point at. It is the self-configuration report's endpoint — the one the
// settings page shows — cached briefly because the report reads the stack's
// env file and asks Docker where the stack lives.
func (s *Server) dashboardEndpoint() string {
	s.endpointMu.Lock()
	defer s.endpointMu.Unlock()
	if s.endpointCache != "" && time.Since(s.endpointCachedAt) < 5*time.Minute {
		return s.endpointCache
	}
	endpoint := ""
	if s.modules.selfConfig != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		endpoint = s.modules.selfConfig.Report(ctx).Endpoint
	}
	if endpoint == "" {
		endpoint = "https://" + s.Cfg.Site + ":8443"
	}
	s.endpointCache, s.endpointCachedAt = endpoint, time.Now()
	return endpoint
}
