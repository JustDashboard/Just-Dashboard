package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dnsservice"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/forgex"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gameserver"
	"github.com/Wayy01/Just-Dashboard/backend/internal/ghx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/githubapp"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gitx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/linuxusers"
	"github.com/Wayy01/Just-Dashboard/backend/internal/logdrain"
	"github.com/Wayy01/Just-Dashboard/backend/internal/logsx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netcapture"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netdiag"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netflows"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netipam"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netvantage"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/remotedesk"
	"github.com/Wayy01/Just-Dashboard/backend/internal/secretstore"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfcfg"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate"
	"github.com/Wayy01/Just-Dashboard/backend/internal/sysinfo"
	"github.com/Wayy01/Just-Dashboard/backend/internal/term"
	"github.com/Wayy01/Just-Dashboard/backend/internal/uninstall"
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
	forge         *forgex.Service
	updates       *updates.Service
	selfUpdate    *selfupdate.Service
	selfConfig    *selfcfg.Service
	uninstall     *uninstall.Service
	certKeeper    *selfcfg.CertKeeper
	proxy         *proxysvc.Service
	// accountLink links this dashboard to a just-dashboard.com account. Nil in
	// agent mode, where the only caller is the hub and nothing here applies.
	accountLink *accountlink.Service

	// hostFiles is files with every operation run in the host's mount
	// namespace. The Files page, and only it, uses it: its paths are typed by
	// someone reading the server's tree in the terminal beside it, while the
	// other callers resolve a path and then open it themselves, from here.
	hostFiles *files.Service
	// onHost is what moves hostFiles: hostexec.InHostMounts, warning once
	// when the namespace cannot be entered.
	onHost func(fn func())
	// proxyExtras is the proxy pages' own state; see modules_proxy.go.
	proxyExtras proxyExtras
	// requests holds what every deployment's ingress and every proxy site
	// served, read once and advanced by what was appended since; every poll
	// and live tail on a project's Logs page and a site's page is answered
	// from it.
	requests *accesslog.Store
	// logDrains holds where deployments' output and requests are copied to;
	// logDrainManager ships them, started with the server.
	logDrains       *logdrain.Store
	logDrainManager *logdrain.Manager
	// secretStores are the connections to external secret managers that
	// deployment variables reference.
	secretStores *secretstore.Repository
	// trafficAlerts watches the request record for the rules operators set,
	// and tells the notification channels when one crosses its line;
	// dbAlerts does the same for the databases' readings and their backups,
	// and hostAlerts for the machine's own: CPU, memory, filesystems,
	// containers and every backup job.
	trafficAlerts *deploy.TrafficAlertEvaluator
	// rollbackWatcher reads every open warm-release window and rolls a
	// release back, or removes the warm one, when the window says so.
	rollbackWatcher *deploy.RollbackWatcher
	dbAlerts        *dbAlerts
	hostAlerts      *hostAlerts
	dbs             *dbx.Manager
	linuxUsers      *linuxusers.Service
	netsec          *netsec.Service
	// blocks records the remote addresses blocked from the connection table
	// with why and until when, and lifts the ones that end.
	blocks *netsec.Blocks
	// network changes the host's network: devices, routes, the gateway
	// table, shaping, VPN and resolver. netsec keeps reading it for the
	// posture; this is the half that writes.
	network         *netx.Service
	dnsServices     *dnsservice.Service
	networkVantages *netvantage.Service
	ipam            *netipam.Service
	// remoteDesktop keeps the computers sessions open to and runs guacd,
	// the container that speaks RDP and VNC on the browser's behalf.
	remoteDesktop *remotedesk.Service
	// jobs runs the operations that take longer than a request should:
	// certbot, package upgrades, sshd applies. They outlive the request that
	// started them and are watched by id rather than by the socket.
	jobs           *jobs.Manager
	diagnostics    *netdiag.Service
	captures       *netcapture.Service
	captureNative  captureNativeOwner
	flowAccounting *netflows.Service
	backupStore    *backups.Store
	backupRunner   *backups.Runner
	backupSched    *backups.Scheduler
	deployStore    *deploy.Store
	deployer       *deploy.Deployer
	deployRuns     *deploy.OrchestrationStore
	deployEngine   *deploy.Engine
	deployPlanning *deploy.PlanningStore
	// commitStatuses reports runs to GitHub, GitLab and Gitea/Forgejo, and
	// says in Settings where they go and whether the host took them.
	commitStatuses  *deploy.CommitStatusPublisher
	deploySources   *deploy.HostSourceAnalyzer
	deployPreflight *deploy.HostPreflightObserver
	// deployChecker answers the advisory check and Detect again with the
	// evaluation analyze_plan runs before every build.
	deployChecker    *deploy.DeploymentChecker
	deployArtifacts  *deploy.ArtifactBuilder
	deployAutomation *deploy.AutomationStore
	deploySchedule   *deploy.AutomationScheduler
	deployGit        *deploy.GitWatcher
	deployDatabases  *deploymentDatabaseNetworks
	poolers          *poolerKeeper
	deployPreviews   *deploy.PreviewQuarantineController
	deployRuntime    *deploy.DockerRuntimeOwner
	// deployExecutor is the normalized release path, kept for the tailnet
	// sweep Start runs once the engine is up.
	deployExecutor *deploy.NormalizedStepExecutor
	// promotionImages reads the images a promotion takes: deployArtifacts,
	// unless a test puts a fake in its place.
	promotionImages deploy.LocalImageReader
	// tailnet publishes preview environments on this host's Tailscale node
	// through `tailscale serve`; the executor and the preview route share it.
	tailnet deploy.TailnetPublisher
	// pullRequests reads pull requests through the trusted GitHub identity
	// for the deploy pages and the reconciler.
	pullRequests *pullRequests
	// previewReconciler polls GitHub for every open preview so a merge that
	// no webhook announces still closes it.
	previewReconciler *deploy.PreviewReconciler
	// githubApp is the dashboard's own GitHub identity: one App, installed on
	// the accounts whose repositories deploy here.
	githubApp *githubapp.Service
	// Upstream game-version metadata, behind one bounded client and a short
	// cache so opening the wizard does not hammer somebody else's API.
	gameVersions *gameserver.Adapter
}

func (s *Server) initModules() {
	s.modules.sys = sysinfo.NewCollector()
	s.modules.docker = dockerx.New(s.Cfg.DockerHost)
	s.initFlowAccounting()
	// Callers of the shared sampler arrive when a page asks, not on a cadence,
	// so a baseline older than this is dropped rather than averaged over.
	s.modules.dockerStats = s.modules.docker.NewStatsSampler().WithMaxAge(statsMaxAge)
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
		// After the shell setup, because a held session is started with the
		// login SetupShell assembles.
		if err := s.modules.term.HoldSessions(s.Cfg.DataDir); err != nil {
			s.Log.Warn("new terminal sessions are unavailable until restart protection is restored", "error", err)
		}
	}
	s.modules.files = files.New(s.Cfg.FileRoots)
	var hostMountsWarning sync.Once
	s.modules.onHost = func(fn func()) {
		if err := hostexec.InHostMounts(fn); err != nil {
			hostMountsWarning.Do(func() {
				s.Log.Warn("the Files page is showing this container's filesystem, not the server's", "error", err)
			})
		}
	}
	s.modules.hostFiles = s.modules.files.OnHost(s.modules.onHost)
	s.modules.gameVersions = gameserver.New()
	s.modules.git = gitx.New(s.Cfg.GitRoots)
	s.modules.github = ghx.New()
	s.modules.forge = forgex.New(s.Store, s.Sealer)
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
		Adopted:     s.carryGitHubLogin,
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
	if s.Cfg.IngressContainer != "" {
		s.modules.proxy = proxysvc.NewWithIngressContainer(s.Cfg.NginxDir, s.Cfg.CaddyFile, s.Cfg.IngressContainer)
	}
	s.modules.requests = accesslog.NewStore(s.openRequestRecord)
	s.modules.logDrains = logdrain.NewStore(s.Store, s.Sealer)
	s.modules.dbs = dbx.NewManager()
	s.modules.linuxUsers = linuxusers.New()
	s.modules.netsec = netsec.New()
	s.modules.blocks = netsec.NewBlocks(s.Store.DB, s.modules.netsec)
	s.modules.networkVantages = netvantage.New(s.Store, s.Sealer)
	s.modules.remoteDesktop = remotedesk.New(remotedesk.Options{DB: s.Store.DB, Seal: s.Sealer.Seal, Open: s.Sealer.Open, Engine: remotedesk.NewDockerEngine(s.Cfg.DockerHost), Bridge: s.remoteDesktopBridge(), Tailnet: s.remoteDesktopTailnet, Warm: s.remoteDesktopWarm})
	s.modules.dnsServices = dnsservice.New(dnsservice.Options{DB: s.Store.DB, Seal: s.Sealer.Seal, Open: s.Sealer.Open, Runtime: dnsservice.NewDockerRuntime(s.Cfg.DockerHost)})
	s.modules.network = netx.New(netx.Options{
		Paths:               netx.DefaultPaths(),
		DB:                  s.Store.DB,
		Log:                 s.Log,
		Allowlist:           allowlistStrings(s.Cfg.AllowedCIDRs),
		Seal:                s.Sealer.Seal,
		Open:                s.Sealer.Open,
		SampleEvery:         s.Cfg.MetricsInterval,
		Retention:           s.Cfg.MetricsRetention,
		IndependentRecovery: true,
	})
	// The firewall keeps its rule history beside the audit log, and offers
	// the network module's owned nftables table where no ufw or firewalld
	// runs.
	s.modules.netsec.UseHistory(s.Store.DB)
	s.modules.netsec.UseOwnedFirewall(ownedFirewall{net: s.modules.network})
	s.modules.ipam = netipam.New(s.Store, s.ipamInventory)
	s.modules.jobs = jobs.New(s.Log)
	// Package commands run as units of the host's systemd, so the Docker
	// restart an upgrade can cause does not cut dpkg off halfway; any still
	// running, or finished while nothing watched, are taken back here.
	if err := s.modules.jobs.Detach(filepath.Join(s.Cfg.DataDir, "jobs")); err != nil {
		s.Log.Warn("package commands will run under the dashboard, where a Docker restart can interrupt them", "error", err)
	}
	s.modules.jobs.Adopt(s.modules.updates.BeginMutation)
	s.modules.diagnostics = netdiag.New(netdiag.NewStore(s.Store.DB), s.modules.jobs, s.executeNetworkProbe, netdiag.WithInvestigator(s.executeNetworkInvestigation))
	s.modules.captureNative = netcapture.NewNative()
	s.modules.captures = netcapture.New(s.Store.DB, s.modules.jobs, s.modules.captureNative.Capture)

	databaseDumper := &backupDatabaseDumper{server: s}
	s.modules.backupStore = backups.NewStore(s.Store, s.Sealer, s.modules.files).
		WithDatabaseValidator(databaseDumper.DescribeDatabase)
	s.modules.backupRunner = backups.NewRunner(s.modules.backupStore,
		filepath.Join(s.Cfg.DataDir, "staging"), s.Log).
		WithRecoveryChecker(&backupRecoveryChecker{server: s}).
		WithDatabaseDumper(databaseDumper).
		WithContainerPauser(&backupContainerPauser{docker: s.modules.docker}).
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
	s.modules.poolers = &poolerKeeper{server: s}
	s.modules.secretStores = secretstore.NewRepository(s.Store, s.Sealer)
	s.modules.deployPlanning = deploy.NewPlanningStore(s.Store, s.Sealer, s.Cfg.DeployRoots).
		WithDatabaseURLResolver(s.modules.deployDatabases.ResolveVariable).
		WithSecretStores(s.modules.secretStores)
	s.modules.githubApp = githubapp.New(githubapp.NewStore(s.Store.DB, s.Sealer), s.modules.deployPlanning)
	s.modules.deployPlanning.WithInstallationTokens(s.modules.githubApp)
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
	).withExtensionProbe(s.databaseExtensions))
	s.modules.deployChecker = deploy.NewDeploymentChecker(
		s.modules.deployRuns, s.modules.deployPlanning, s.modules.deploySources, s.modules.deployPreflight,
	)
	artifactBackend := deploy.NewDockerArtifactBackend(s.modules.docker)
	s.modules.deployArtifacts = deploy.NewArtifactBuilder(artifactBackend)
	s.modules.promotionImages = s.modules.deployArtifacts
	runtimeOwner := deploy.NewDockerRuntimeOwner(s.modules.docker).WithNetworks(s.modules.deployDatabases)
	s.modules.deployRuntime = runtimeOwner
	s.modules.deployPreviews = deploy.NewPreviewQuarantineController(s.modules.deployRuns, runtimeOwner, s.modules.proxy,
		func(ctx context.Context, environmentID int64, phase string, success bool) {
			s.Audit.Record(ctx, audit.Entry{Actor: "system", Action: "deploy.preview.quarantine." + phase, Target: strconv.FormatInt(environmentID, 10), Success: success})
		}, func(err error) { s.Log.Warn("preview isolation needs attention", "error", err) })
	s.modules.tailnet = tailnetPublisher{serve: selfcfg.NewTailnetServe()}
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
		WithCertificateIssuer(auditedCertificateIssuer{issuer: s.modules.proxy, record: s.Audit.Record}).
		WithNotifications(s.modules.deployAutomation).
		WithTailnetPublisher(s.modules.tailnet)
	s.modules.deployExecutor = normalizedExecutor
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
	forges := deploy.NewForgeClient(s.modules.deployPlanning)
	s.modules.commitStatuses = deploy.NewCommitStatusPublisher(s.modules.deployRuns, githubStatusPoster{app: s.modules.githubApp, github: s.modules.github}, s.dashboardEndpoint, s.Log).
		WithForges(forges)
	s.modules.deployEngine.WithObservers(
		notifications,
		s.modules.commitStatuses,
		deploy.NewPullRequestCommenter(s.modules.deployRuns, s.modules.githubApp, s.dashboardEndpoint, s.Log).WithForges(forges),
		// What a run read from the secret stores is held for its own steps
		// and dropped the moment it finishes.
		s.modules.deployPlanning.SecretStoreRunObserver(),
	)
	s.modules.deploySchedule = deploy.NewAutomationScheduler(s.modules.deployAutomation, s.dispatchDeploymentSchedule).
		WithSweep(notifications.RetryFailedDeliveries)
	// Alerts reach the same channels a deployment's own outcome does, through
	// the same delivery, with two more events those channels can carry.
	s.modules.trafficAlerts = deploy.NewTrafficAlertEvaluator(
		s.modules.deployAutomation, s.modules.requests,
		func(ctx context.Context, channelID int64, envelope deploy.NotificationEnvelope) error {
			return s.modules.deployAutomation.DeliverNotification(ctx, nil, channelID, envelope)
		},
		s.trafficAlertNames, s.Log)
	// The windows read the same record the traffic alerts do, and ask for
	// a rollback through the same enqueue an operator's press takes.
	s.modules.rollbackWatcher = deploy.NewRollbackWatcher(normalizedExecutor, s.modules.requests,
		s.enqueueAutomaticRollback, s.auditRollbackWatch, s.Log)
	s.modules.dbAlerts = s.newDBAlerts()
	s.modules.hostAlerts = s.newHostAlerts()
	s.modules.deployGit = deploy.NewGitWatcher(s.modules.deployRuns, s.modules.deploySources, s.dispatchGitDeployment)
	s.modules.pullRequests = newPullRequests(s.modules.github, s.modules.githubApp, s.modules.git)
	s.modules.previewReconciler = deploy.NewPreviewReconciler(s.modules.deployAutomation, s.modules.pullRequests, s.closePreviewTarget, s.Log)

	s.initProxyExtras()
	s.initUninstall()
	s.initAccountLink()
}

// initAccountLink makes the connector to a linked just-dashboard.com account.
// It is built whether or not linking is switched on, because the Settings page
// still has to say that it is off; with JD_ACCOUNT_LINK=false it opens no
// connection and starts nothing.
func (s *Server) initAccountLink() {
	if s.Cfg.AgentMode {
		return
	}
	s.modules.accountLink = accountlink.New(accountlink.Options{
		Store: s.Store, Sealer: s.Sealer, Audit: s.Audit, Log: s.Log,
		SiteURL: s.Cfg.AccountURL, Enabled: s.Cfg.AccountLink,
		Host: newAccountHost(s), Version: version.Version,
	})
}

// tailnetPublisher adapts selfcfg's `tailscale serve` driver to the deploy
// package's publisher contract, which knows ports and loopback targets and
// nothing about the CLI.
type tailnetPublisher struct {
	serve *selfcfg.TailnetServe
}

func (p tailnetPublisher) PublishTailnet(ctx context.Context, port, upstreamPort, previousUpstream int) (string, error) {
	return p.serve.Publish(ctx, port, upstreamPort, previousUpstream)
}

func (p tailnetPublisher) WithdrawTailnet(ctx context.Context, port int) error {
	return p.serve.Withdraw(ctx, port)
}

func (p tailnetPublisher) ServedTailnetPorts(ctx context.Context) (map[int]int, error) {
	// A host without the client serves nothing, and says so quietly: the
	// sweep then withdraws nothing and marks any recorded tailnet address
	// unpublished, instead of warning at every boot of every install.
	if !hostexec.Available("tailscale") {
		return map[int]int{}, nil
	}
	entries, err := p.serve.Served(ctx)
	if err != nil {
		return nil, err
	}
	ports := make(map[int]int, len(entries))
	for port, entry := range entries {
		// Every served port is a key, so allocation steps around it; the
		// value names the upstream only for a mapping the dashboard could
		// have made itself, the one kind a sweep or withdrawal may touch.
		ports[port] = entry.LoopbackUpstream()
	}
	return ports, nil
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

// githubStatusPoster adapts the two GitHub identities to the deploy package's
// narrow commit-status contract: the App where it is installed on the
// repository, the CLI's personal credential everywhere else.
type githubStatusPoster struct {
	app    *githubapp.Service
	github *ghx.Service
}

func (p githubStatusPoster) PostCommitStatus(ctx context.Context, nameWithOwner, sha string, status deploy.CommitStatus) error {
	if p.app != nil {
		err := p.app.PostCommitStatus(ctx, nameWithOwner, sha, githubapp.CommitStatus{
			State: status.State, TargetURL: status.TargetURL, Description: status.Description, Context: status.Context,
		})
		if err == nil {
			return nil
		}
		if !errors.Is(err, githubapp.ErrNotConfigured) && !errors.Is(err, githubapp.ErrNotInstalled) {
			return err
		}
	}
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
// trafficAlertNames supplies what an alert's message says about where it is
// from: the project's name, the environment's, and the Logs page to open.
func (s *Server) trafficAlertNames(ctx context.Context, projectID, environmentID int64) (string, string, string) {
	project, environment := "", ""
	if summary, err := s.modules.deployRuns.DeploymentSummary(ctx, projectID, deploy.QueueBudget{
		Heavy: s.Cfg.DeployHeavySlots, Light: s.Cfg.DeployLightSlots,
	}); err == nil {
		project, environment = summary.Name, summary.EnvironmentName
	}
	url := ""
	if base := strings.TrimRight(strings.TrimSpace(s.dashboardEndpoint()), "/"); base != "" {
		url = fmt.Sprintf("%s/deploy/%d/logs", base, projectID)
	}
	return project, environment, url
}

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

// carryGitHubLogin follows the install checkout's handover to the owner of
// its directory: the checkout was using root's GitHub login, and without this
// it would read signed out the moment it changed hands.
func (s *Server) carryGitHubLogin(ctx context.Context, dir string) {
	carried, err := s.modules.github.CarryLogin(ctx, dir)
	if s.Log == nil {
		return
	}
	if err != nil {
		s.Log.Warn("could not carry the GitHub login to the install checkout's new owner", "dir", dir, "err", err)
	} else if carried {
		s.Log.Info("carried the GitHub login to the install checkout's new owner", "dir", dir)
	}
}
