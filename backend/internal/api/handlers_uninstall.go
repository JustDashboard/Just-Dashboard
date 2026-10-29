package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/uninstall"
	"github.com/go-chi/chi/v5"
)

// Removing the dashboard from this server, from its own Configuration page.
//
// Mounted beside the restart under /dashboard, and admin-only for the same
// reason the settings are. Starting one is inside s.destructive like every
// other way of taking the stack down; a wipe also takes the typed phrase,
// because it is the one way off this page that deletes what nothing brings
// back — the deployments' volumes, the database and its keys.
func (s *Server) mountUninstallRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/uninstall", s.handle(s.handleUninstallStatus))
	r.Method(http.MethodGet, "/uninstall/plan", s.handle(s.handleUninstallPlan))
	r.Method(http.MethodGet, "/uninstall/log", s.handle(s.handleUninstallLog))
	r.Method(http.MethodDelete, "/uninstall/run", s.handle(s.handleUninstallDismiss))
	s.destructive(r, func(r chi.Router) {
		r.Method(http.MethodPost, "/uninstall", s.handle(s.handleUninstallStart))
	})
}

func (s *Server) initUninstall() {
	deployFiles := files.New(s.Cfg.DeployRoots)
	teardown := uninstall.Teardown{
		Targets:          s.uninstallTargets,
		RemoveRoute:      s.modules.proxy.RemoveDeploymentRoute,
		RemovePath:       func(path string) error { return deployFiles.Delete(path, true) },
		WithdrawPreviews: s.modules.deployExecutor.WithdrawPreviewAddresses,
		RemoveIngressDir: func() error { return os.RemoveAll(proxysvc.IngressDir) },
		RemoveTailnetGateDir: func() error {
			return os.RemoveAll(proxysvc.TailnetGateDir)
		},
	}
	// An interface holding a nil client is not nil, and the teardown asks.
	if s.modules.docker != nil {
		teardown.Docker = s.modules.docker
	}
	s.modules.uninstall = uninstall.New(uninstall.Options{
		DataDir:    s.Cfg.DataDir,
		BackupDir:  s.Cfg.BackupLocalDir,
		NginxDir:   s.Cfg.NginxDir,
		DockerHost: s.Cfg.DockerHost,
		MasterKey:  s.Cfg.MasterKeyHex,
		Locate:     s.modules.selfUpdate.Location,
		List:       s.listSiblings,
		Teardown:   teardown,
		Pause:      s.pauseForUninstall,
		Resume: func(ctx context.Context) error {
			_, err := s.modules.selfConfig.Restart(ctx, false, "uninstall")
			return err
		},
		Log: s.Log,
	})
}

// uninstallTargets is every deployment's removal plan, flattened: what the
// database says each one owns, archived or not.
func (s *Server) uninstallTargets(ctx context.Context) (int, []deploy.RemovalTarget, error) {
	projects, err := s.modules.deployStore.List(ctx)
	if err != nil {
		return 0, nil, err
	}
	var targets []deploy.RemovalTarget
	for _, project := range projects {
		plan, err := s.modules.deployPlanning.RemovalPlan(ctx, project.ID)
		if err != nil {
			return 0, nil, fmt.Errorf("%s: %w", project.Name, err)
		}
		targets = append(targets, plan.Targets...)
	}
	return len(projects), targets, nil
}

// pauseForUninstall stops everything that would put back what an uninstall
// takes away, then waits for the deployments already running to stop at a
// point the engine can resume from.
//
// It is the deployment half of Shutdown, run early: the Git watcher, the
// schedules and the preview reconciler would start new runs, the ingress
// keeper would recreate the ingress a wipe removes, and the backup scheduler
// would start restore checks that run containers of their own. None of them
// starts again in this process — a run that fails from here restarts the
// stack, which is what brings them back.
func (s *Server) pauseForUninstall(ctx context.Context, out io.Writer) error {
	s.modules.deployGit.Stop()
	s.modules.previewReconciler.Stop()
	s.modules.deployPreviews.Stop()
	s.modules.deploySchedule.Stop()
	s.modules.deployDatabases.Stop()
	s.modules.poolers.Stop()
	s.modules.dataCopies.Stop()
	s.modules.backupSched.Stop()
	if s.ingressStop != nil {
		s.ingressStop()
		<-s.ingressDone
	}
	fmt.Fprintln(out, "stopped the Git watcher, the schedules, the preview reconciler and the ingress keeper")
	fmt.Fprintln(out, "waiting for running deployments to reach a point they can stop at")
	started := time.Now()
	if err := s.modules.deployEngine.Shutdown(ctx); err != nil {
		return fmt.Errorf("deployments were still running after %s", time.Since(started).Round(time.Second))
	}
	fmt.Fprintln(out, "the deployment engine is stopped")
	return nil
}

// handleUninstallStatus is what the page polls: whether this install can
// uninstall itself, and the run in flight or the last one.
func (s *Server) handleUninstallStatus(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, s.modules.uninstall.Report(r.Context()))
	return nil
}

// handleUninstallPlan is what the dialog shows before anything happens: what
// a wipe would remove and what a keep would leave. Asked for once, when the
// dialog opens, because it lists every container, volume and image.
func (s *Server) handleUninstallPlan(w http.ResponseWriter, r *http.Request) error {
	plan, err := s.modules.uninstall.Plan(r.Context())
	if err != nil {
		return mapUninstallError(err)
	}
	httpx.JSON(w, http.StatusOK, plan)
	return nil
}

// handleUninstallStart admits the uninstall and answers before it finishes —
// it has to, since what it finishes with is the end of this process.
func (s *Server) handleUninstallStart(w http.ResponseWriter, r *http.Request) error {
	var req uninstall.Request
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if !req.Mode.Valid() {
		return mapUninstallError(uninstall.ErrMode)
	}
	if req.Mode == uninstall.ModeWipe {
		if err := httpx.RequireTypedConfirmation(w, r, uninstall.Phrase()); err != nil {
			return err
		}
	}
	actor := httpx.MustPrincipal(r).Username()
	run, err := s.modules.uninstall.Begin(r.Context(), req, actor)
	httpx.SetAudit(r, "dashboard.uninstall", string(req.Mode), map[string]any{
		"deleteBackups": req.Mode == uninstall.ModeWipe && req.DeleteBackups, "ok": err == nil,
	})
	if err != nil {
		return mapUninstallError(err)
	}
	httpx.JSON(w, http.StatusAccepted, run)
	return nil
}

func (s *Server) handleUninstallLog(w http.ResponseWriter, r *http.Request) error {
	writeTranscript(w, s.modules.uninstall.Transcript())
	return nil
}

func (s *Server) handleUninstallDismiss(w http.ResponseWriter, r *http.Request) error {
	if err := s.modules.uninstall.Dismiss(); err != nil {
		return mapUninstallError(err)
	}
	httpx.NoContent(w)
	return nil
}

func mapUninstallError(err error) error {
	switch {
	case errors.Is(err, uninstall.ErrInProgress):
		return httpx.Err(http.StatusConflict, "lifecycle_in_progress", err.Error())
	case errors.Is(err, uninstall.ErrNoLocation):
		return httpx.Err(http.StatusServiceUnavailable, "uninstall_unsupported", err.Error())
	case errors.Is(err, uninstall.ErrMode):
		return httpx.BadRequest("%v", err)
	default:
		return httpx.Internal(err)
	}
}
