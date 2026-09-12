// Command server runs the VPS management dashboard API.
//
// The process is root-equivalent by design: it drives the Docker socket,
// systemd, the firewall and a PTY. It therefore refuses to start in any
// configuration that would leave it reachable from an untrusted network —
// see internal/config for the allowlist rules.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/agent"
	"github.com/Wayy01/Just-Dashboard/backend/internal/api"
	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/config"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfcfg"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stackports"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/Wayy01/Just-Dashboard/backend/internal/version"
)

func main() {
	// The container healthcheck re-executes this binary rather than shipping
	// curl into the image, which keeps the runtime surface smaller.
	healthcheck := flag.Bool("healthcheck", false, "probe the local server and exit non-zero if it is unhealthy")
	agentMode := flag.Bool("agent", false, "run as an agent managed by a hub: no human login, mutual TLS only")
	agentReset := flag.Bool("agent-reset", false, "forget the enrolled hub so this agent can be enrolled again, then exit")
	// The upgrade half of internal/selfupdate. This same binary is started in
	// a separate container to carry out an upgrade, because the containers it
	// replaces include the one that asked for it — see that package for why
	// the work cannot be a child process of the server.
	selfUpdate := flag.Bool("self-update", false,
		"carry out the dashboard upgrade recorded in -state-dir, then exit")
	// The settings half of the same idea: applying a port change or a restart
	// recreates the container that asked for it, so a sibling does the work
	// and this is the flag that makes this binary that sibling.
	selfRestart := flag.Bool("self-restart", false,
		"carry out the dashboard restart recorded in -state-dir, then exit")
	stateDir := flag.String("state-dir", "",
		"directory holding the upgrade record and its transcript (the value of JD_DATA_DIR)")
	preparePorts := flag.String("prepare-ports", "", "select available dashboard ports in the checkout before starting Compose")
	startStack := flag.Bool("start-stack", false, "start and verify Compose after preparing ports")
	flag.Parse()
	if *preparePorts != "" {
		dir, err := filepath.Abs(*preparePorts)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			if *startStack {
				err = stackports.Start(ctx, dir, "docker-compose.yml", os.Stdout, func() error {
					command := hostexec.CommandInDir(ctx, dir, "docker", "compose", "up", "-d", "--wait", "--wait-timeout", "90")
					command.Stdout, command.Stderr = os.Stdout, os.Stderr
					return command.Run()
				}, nil)
			} else {
				_, err = stackports.Reconcile(ctx, dir, "docker-compose.yml", os.Stdout)
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "port selection failed:", err)
			os.Exit(1)
		}
		return
	}
	if *healthcheck {
		if err := probe(); err != nil {
			fmt.Fprintln(os.Stderr, "unhealthy:", err)
			os.Exit(1)
		}
		return
	}
	if *selfUpdate {
		// Deliberately before config.Load and everything under it. The updater
		// is given no master key, no database and no environment from the
		// process that started it: everything it needs is in the record on
		// disk, which is also what the operator reads afterwards, so the job
		// described is exactly the job that ran.
		dir := *stateDir
		if dir == "" {
			dir = config.Env("JD_DATA_DIR")
		}
		if dir == "" {
			fmt.Fprintln(os.Stderr, "fatal: -self-update needs -state-dir (the dashboard's JD_DATA_DIR)")
			os.Exit(2)
		}
		if err := selfupdate.RunUpdater(dir); err != nil {
			fmt.Fprintln(os.Stderr, "update failed:", err)
			os.Exit(1)
		}
		return
	}
	if *selfRestart {
		// Before config.Load, for the reason -self-update is: this process is
		// given no master key, no database and nothing from the environment of
		// the process that started it. The record on disk is the whole job.
		dir := *stateDir
		if dir == "" {
			dir = config.Env("JD_DATA_DIR")
		}
		if dir == "" {
			fmt.Fprintln(os.Stderr, "fatal: -self-restart needs -state-dir (the dashboard's JD_DATA_DIR)")
			os.Exit(2)
		}
		if err := selfcfg.RunRestart(dir); err != nil {
			fmt.Fprintln(os.Stderr, "restart failed:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(*agentMode, *agentReset); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run(agentFlag, agentReset bool) error {
	level := slog.LevelInfo
	if config.Env("JD_LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// The flag is the ergonomic form; the environment variable is what the
	// compose file sets. Either turns agent mode on.
	cfg.AgentMode = cfg.AgentMode || agentFlag
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	sealer, err := auth.NewSealer(cfg.MasterKeyHex)
	if err != nil {
		return err
	}
	svc := auth.NewService(st, sealer, cfg.SessionTTL, cfg.IdleTTL, cfg.Require2FA)
	aud := audit.New(st, log)

	var identity *agent.Identity
	if cfg.AgentMode {
		identity, err = agent.Load(filepath.Join(cfg.DataDir, "agent"))
		if err != nil {
			return err
		}
		if agentReset {
			if err := identity.Reset(); err != nil {
				return err
			}
			log.Warn("agent enrolment cleared; start the agent again to mint a new token",
				"agent_id", identity.ID())
			return nil
		}
		if err := announceAgent(identity, log); err != nil {
			return err
		}
	} else {
		// An agent has no interactive account to bootstrap: the hub is the
		// only caller, and it authenticates with a certificate.
		if err := bootstrapAdmin(context.Background(), svc, st, log); err != nil {
			return err
		}
	}

	// From here on the dashboard spawns children — docker compose, deploy
	// hooks, dumps, git — and every one of them inherits os.Environ(). The
	// master key decrypts TOTP seeds, database DSNs, deploy env and backup
	// credentials, so a project's own compose file declaring
	// `environment: [LEAK=${JD_MASTER_KEY}]` was enough to carry it into a
	// container the pusher controls. It is already in memory; nothing below
	// reads it from the environment again.
	scrubSecretEnv()

	srv := api.New(cfg, log, st, svc, sealer, aud, identity)
	defer srv.Shutdown()

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: log tails, metric pushes and PTY sockets are
		// long-lived by nature and would be severed by one.
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := srv.Start(ctx); err != nil {
		return fmt.Errorf("start background workers: %w", err)
	}
	go janitor(ctx, svc, log)

	if cfg.AgentMode {
		tlsCfg, err := agentTLS(identity)
		if err != nil {
			return err
		}
		httpSrv.TLSConfig = tlsCfg
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("just-dashboard listening",
			"version", version.Version,
			"addr", cfg.Addr, "allowlist", len(cfg.AllowedCIDRs),
			"agent", cfg.AgentMode)
		var err error
		if cfg.AgentMode {
			// The certificate and key are already in the TLS config.
			err = httpSrv.ListenAndServeTLS("", "")
		} else {
			err = httpSrv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutCtx)
	}
}

// scrubSecretEnv removes the boot-time secrets from the process environment
// once they have been consumed, so that nothing spawned later can read them.
// Both prefixes are cleared: config.Env falls back to the legacy VPSD_ names,
// so leaving those set would leave the same value readable under another name.
func scrubSecretEnv() {
	for _, name := range []string{"MASTER_KEY", "BOOTSTRAP_PASSWORD"} {
		os.Unsetenv("JD_" + name)
		os.Unsetenv("VPSD_" + name)
	}
}

// probe asks the local server whether it is serving. It deliberately targets
// the configured bind address so a healthcheck cannot pass against some other
// process that happens to be listening.
func probe() error {
	addr := config.Env("JD_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	if strings.HasPrefix(addr, "0.0.0.0:") {
		addr = "127.0.0.1:" + strings.TrimPrefix(addr, "0.0.0.0:")
	}
	// Agent mode serves TLS on this same address, so probing it with plain
	// HTTP got a handshake error every time: the container went unhealthy
	// after three attempts and stayed there while the agent was serving
	// perfectly. The certificate is self-signed by design and the peer is
	// loopback, so verifying it would be theatre.
	//
	// The environment variable is the only signal available here: the probe
	// is a separate process from the server, so a server started with -agent
	// rather than JD_AGENT_MODE=true is invisible to it. The compose file sets
	// the variable, and the Dockerfile healthcheck inherits it.
	scheme := "http://"
	client := &http.Client{Timeout: 4 * time.Second}
	if agent, err := strconv.ParseBool(config.Env("JD_AGENT_MODE")); err == nil && agent {
		scheme = "https://"
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}
	resp, err := client.Get(scheme + addr + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %s", resp.Status)
	}
	return nil
}

// janitor sweeps expired sessions so a stolen cookie cannot outlive its window
// simply because nobody logged in again.
func janitor(ctx context.Context, svc *auth.Service, log *slog.Logger) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := svc.PurgeExpired(ctx); err != nil {
				log.Warn("session purge failed", "err", err)
			}
		}
	}
}

// bootstrapAdmin creates the first admin on an empty database. The generated
// password is printed once to the process log and must be changed at first
// login; two-factor enrollment is then forced before the account can do
// anything at all.
func bootstrapAdmin(ctx context.Context, svc *auth.Service, st *store.Store, log *slog.Logger) error {
	users, err := svc.ListUsers(ctx)
	if err != nil {
		return err
	}
	if len(users) > 0 {
		return nil
	}
	username := config.Env("JD_BOOTSTRAP_USER")
	if username == "" {
		username = "admin"
	}
	password := config.Env("JD_BOOTSTRAP_PASSWORD")
	generated := password == ""
	if generated {
		password = auth.RandomToken(18)
	}
	if _, err := svc.CreateUser(ctx, username, password, auth.RoleAdmin, true); err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}
	if generated {
		log.Warn("bootstrap admin created — change this password immediately",
			"username", username, "password", password)
	} else {
		log.Warn("bootstrap admin created from JD_BOOTSTRAP_PASSWORD", "username", username)
	}
	return st.SetSetting(ctx, "bootstrapped_at", time.Now().UTC().Format(time.RFC3339))
}

// announceAgent prints the enrolment token exactly once per boot while the
// agent is unclaimed, in the same spirit as the generated bootstrap password:
// the operator reads it out of the log, carries it to the hub, and it stops
// working the moment it is used or the window closes.
func announceAgent(id *agent.Identity, log *slog.Logger) error {
	if id.Enrolled() {
		log.Info("agent is enrolled",
			"agent_id", id.ID(), "hub_fingerprint", id.HubFingerprint(),
			"enrolled_at", id.EnrolledAt().Format(time.RFC3339))
		return nil
	}
	token, err := id.NewEnrolmentToken()
	if err != nil {
		return err
	}
	log.Warn("agent is not enrolled — add it to a hub with this token",
		"agent_id", id.ID(),
		"fingerprint", id.Fingerprint(),
		"enrolment_token", token,
		"expires_in", agent.TokenTTL.String())
	return nil
}

// agentTLS asks every caller for a certificate without requiring one at the
// handshake. Requiring it here would lock out /agent/enrol, which by
// definition runs before the hub is trusted; the HubOnly middleware does the
// actual enforcement per route, so an unenrolled caller can reach enrolment
// and nothing else.
func agentTLS(id *agent.Identity) (*tls.Config, error) {
	certPEM, keyPEM := id.TLSCertificate()
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("agent tls material: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{pair},
		MinVersion:   tls.VersionTLS12,
		ClientAuth:   tls.RequestClientCert,
	}, nil
}
