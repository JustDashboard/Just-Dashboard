package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountProcessRoutes(r chi.Router) {
	r.Route("/pm2", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handlePM2List))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapServiceControl))
			r.Method(http.MethodPost, "/save", s.handle(s.handlePM2Save))
			r.Method(http.MethodPost, "/{name}/start", s.handle(s.pm2Action(procs.PM2Start)))
			r.Method(http.MethodPost, "/{name}/reload", s.handle(s.pm2Action(procs.PM2Reload)))
			r.Method(http.MethodPost, "/{name}/reset", s.handle(s.pm2Action(procs.PM2Reset)))
			r.Method(http.MethodPost, "/{name}/scale", s.handle(s.handlePM2Scale))
			// Every process of one account's daemon. The literal `daemons`
			// segment wins over `{name}` in chi, and PM2 itself reserves
			// `all`, so neither can be an application's name.
			r.Method(http.MethodPost, "/daemons/{user}/start", s.handle(s.pm2AllAction(procs.PM2Start)))
			r.Method(http.MethodPost, "/daemons/{user}/reload", s.handle(s.pm2AllAction(procs.PM2Reload)))
		})
		r.Group(func(r chi.Router) {
			// Starting a program that is not yet under PM2 runs whatever file
			// the operator names, as the chosen host account. That is code
			// execution rather than service control, and is gated like the
			// other things that are: the terminal, host accounts, cron.
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Method(http.MethodPost, "/start", s.handle(s.handlePM2Start))
		})
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodPost, "/{name}/stop", s.handle(s.pm2Action(procs.PM2Stop)))
			r.Method(http.MethodPost, "/{name}/restart", s.handle(s.pm2Action(procs.PM2Restart)))
			// Flushing empties the log files; that is the only thing on this
			// page that loses data outright.
			r.Method(http.MethodPost, "/{name}/flush", s.handle(s.pm2Action(procs.PM2Flush)))
			r.Method(http.MethodDelete, "/{name}", s.handle(s.pm2Action(procs.PM2Delete)))
			r.Method(http.MethodPost, "/daemons/{user}/stop", s.handle(s.pm2AllAction(procs.PM2Stop)))
			r.Method(http.MethodPost, "/daemons/{user}/restart", s.handle(s.pm2AllAction(procs.PM2Restart)))
		})
	})

	r.Route("/systemd", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleUnitList))
		r.Method(http.MethodGet, "/timers", s.handle(s.handleTimerList))
		r.Method(http.MethodGet, "/{name}", s.handle(s.handleUnitShow))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapServiceControl))
			r.Method(http.MethodPost, "/{name}/start", s.handle(s.unitAction(procs.UnitStart)))
			r.Method(http.MethodPost, "/{name}/reload", s.handle(s.unitAction(procs.UnitReload)))
			r.Method(http.MethodPost, "/{name}/reset-failed", s.handle(s.unitAction(procs.UnitResetFailed)))
		})
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Method(http.MethodPost, "/daemon-reload", s.handle(s.handleDaemonReload))
			r.Method(http.MethodPost, "/{name}/enable", s.handle(s.unitAction(procs.UnitEnable)))
			r.Method(http.MethodPost, "/{name}/disable", s.handle(s.unitAction(procs.UnitDisable)))
		})
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodPost, "/{name}/stop", s.handle(s.unitAction(procs.UnitStop)))
			r.Method(http.MethodPost, "/{name}/restart", s.handle(s.unitAction(procs.UnitRestart)))
		})
	})

	r.Route("/processes", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleProcessList))
		r.Method(http.MethodGet, "/inventory", s.handle(s.handleProcessInventory))
		r.Method(http.MethodGet, "/{pid}", s.handle(s.handleProcessDetail))
		r.Method(http.MethodGet, "/{pid}/tree", s.handle(s.handleProcessTree))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Method(http.MethodPut, "/{pid}/priority", s.handle(s.handleProcessPriority))
		})
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodPost, "/{pid}/signal", s.handle(s.handleProcessSignal))
		})
	})

	r.Route("/cron", func(r chi.Router) {
		r.Method(http.MethodGet, "/users", s.handle(s.handleCronUsers))
		r.Method(http.MethodGet, "/system", s.handle(s.handleCronSystem))
		r.Method(http.MethodGet, "/user/{user}", s.handle(s.handleCronUserGet))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			s.destructive(r, func(r chi.Router) {
				// A crontab is replaced wholesale, so a write loses whatever
				// was there before.
				r.Method(http.MethodPut, "/user/{user}", s.handle(s.handleCronUserPut))
			})
		})
	})
}

func (s *Server) handlePM2Save(w http.ResponseWriter, r *http.Request) error {
	res, err := s.modules.pm2.Save(r.Context())
	if err != nil {
		return mapProcsError(err)
	}
	httpx.SetAudit(r, "pm2.save", "startup list", map[string]any{"exitCode": res.ExitCode})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func mapProcsError(err error) error {
	switch {
	case errors.Is(err, procs.ErrNotInstalled):
		return httpx.Err(http.StatusServiceUnavailable, "not_installed", err.Error())
	case errors.Is(err, procs.ErrInvalidName):
		return httpx.Err(http.StatusBadRequest, "invalid_name", err.Error())
	default:
		return httpx.Wrap(http.StatusBadGateway, "command_failed", err)
	}
}

func (s *Server) handlePM2List(w http.ResponseWriter, r *http.Request) error {
	if !s.modules.pm2.Available() {
		httpx.JSON(w, http.StatusOK, map[string]any{"available": false, "processes": []any{}})
		return nil
	}
	list, err := s.modules.pm2.List(r.Context())
	if err != nil {
		return mapProcsError(err)
	}
	for index := range list {
		if err := s.checkPM2LogPaths(list[index].OutLogPath, list[index].ErrLogPath); err != nil {
			list[index].LogsUnavailableReason = "Ask an administrator to include this process's log directory in JD_LOG_ROOTS."
		} else {
			list[index].LogsAvailable = true
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"available": true, "processes": list, "daemons": s.modules.pm2.Daemons(),
	})
	return nil
}

func (s *Server) pm2AllAction(action procs.PM2Action) httpx.Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		account := chi.URLParam(r, "user")
		res, err := s.modules.pm2.ControlAll(r.Context(), account, action)
		if err != nil {
			return mapProcsError(err)
		}
		httpx.SetAudit(r, "pm2."+string(action)+".all", account, map[string]any{"exitCode": res.ExitCode})
		httpx.JSON(w, http.StatusOK, res)
		return nil
	}
}

type pm2ScaleRequest struct {
	Instances int `json:"instances"`
}

func (s *Server) handlePM2Scale(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	daemon, id, err := pm2TargetQuery(r)
	if err != nil {
		return err
	}
	var req pm2ScaleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	res, err := s.modules.pm2.Scale(r.Context(), name, daemon, id, req.Instances)
	if err != nil {
		return mapProcsError(err)
	}
	httpx.SetAudit(r, "pm2.scale", name, map[string]any{
		"exitCode": res.ExitCode, "daemonId": daemon, "id": id, "instances": req.Instances,
	})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handlePM2Start(w http.ResponseWriter, r *http.Request) error {
	var req procs.PM2StartRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Account == "" || req.Script == "" {
		return httpx.BadRequest("account and script are required")
	}
	res, err := s.modules.pm2.Start(r.Context(), req)
	if err != nil {
		return mapProcsError(err)
	}
	httpx.SetAudit(r, "pm2.start.new", req.Script, map[string]any{
		"exitCode": res.ExitCode, "account": req.Account, "name": req.Name, "cwd": req.Cwd,
		"instances": req.Instances, "watch": req.Watch,
	})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) pm2Action(action procs.PM2Action) httpx.Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		name := chi.URLParam(r, "name")
		// No typed phrase: starting, stopping and restarting a process is what
		// a process manager is for, and pm2 delete removes it from pm2's list
		// rather than from disk. The dialog names the process.
		daemon, id, err := pm2TargetQuery(r)
		if err != nil {
			return err
		}
		res, err := s.modules.pm2.ControlTarget(r.Context(), name, daemon, id, action)
		if err != nil {
			return mapProcsError(err)
		}
		httpx.SetAudit(r, "pm2."+string(action), name, map[string]any{"exitCode": res.ExitCode, "daemonId": daemon, "id": id})
		httpx.JSON(w, http.StatusOK, res)
		return nil
	}
}

func (s *Server) handleUnitList(w http.ResponseWriter, r *http.Request) error {
	if !s.modules.systemd.Available() {
		httpx.JSON(w, http.StatusOK, map[string]any{"available": false, "units": []any{}})
		return nil
	}
	units, err := s.modules.systemd.List(r.Context())
	if err != nil {
		return mapProcsError(err)
	}
	if state := r.URL.Query().Get("state"); state != "" {
		filtered := units[:0]
		for _, u := range units {
			if u.ActiveState == state {
				filtered = append(filtered, u)
			}
		}
		units = filtered
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"available": true, "units": units})
	return nil
}

func (s *Server) handleTimerList(w http.ResponseWriter, r *http.Request) error {
	if !s.modules.systemd.Available() {
		httpx.JSON(w, http.StatusOK, map[string]any{"available": false, "timers": []any{}})
		return nil
	}
	timers, err := s.modules.systemd.Timers(r.Context())
	if err != nil {
		return mapProcsError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"available": true, "timers": timers})
	return nil
}

func (s *Server) handleDaemonReload(w http.ResponseWriter, r *http.Request) error {
	res, err := s.modules.systemd.DaemonReload(r.Context())
	if err != nil {
		return mapProcsError(err)
	}
	httpx.SetAudit(r, "systemd.daemon-reload", "systemd", map[string]any{"exitCode": res.ExitCode})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleUnitShow(w http.ResponseWriter, r *http.Request) error {
	unit, props, err := s.modules.systemd.Show(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		return mapProcsError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"unit": unit, "properties": props})
	return nil
}

func (s *Server) unitAction(action procs.UnitAction) httpx.Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		name := chi.URLParam(r, "name")
		// No typed phrase, for the reason pm2Action gives: restarting a unit is
		// the ordinary operation of a service manager and `systemctl start`
		// undoes it.
		res, err := s.modules.systemd.Control(r.Context(), name, action)
		if err != nil {
			return mapProcsError(err)
		}
		httpx.SetAudit(r, "systemd."+string(action), name, map[string]any{"exitCode": res.ExitCode})
		httpx.JSON(w, http.StatusOK, res)
		return nil
	}
}

func (s *Server) handleProcessList(w http.ResponseWriter, r *http.Request) error {
	list, err := s.processInventory(r)
	if err != nil {
		return err
	}
	// Keep the original array response for API clients. The dashboard uses the
	// inventory route because it also needs the full counts and filter facets.
	httpx.JSON(w, http.StatusOK, list.Processes)
	return nil
}

func (s *Server) handleProcessInventory(w http.ResponseWriter, r *http.Request) error {
	list, err := s.processInventory(r)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, list)
	return nil
}

func (s *Server) processInventory(r *http.Request) (procs.ProcessList, error) {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	rows, err := s.modules.table.Snapshot(ctx)
	if err != nil {
		return procs.ProcessList{}, httpx.Internal(err)
	}
	// PM2 is not represented in cgroups, so its own PID inventory is the only
	// reliable way to tell a PM2 child from the same command started by hand.
	// A broken optional manager must not take the raw process table with it.
	if s.modules.pm2.Available() {
		if managed, pmErr := s.modules.pm2.List(ctx); pmErr == nil {
			procs.MarkPM2(rows, managed)
		}
	}
	s.procContainers.label(ctx, s.modules.docker, rows)
	q := r.URL.Query()
	limit := atoiDefault(q.Get("limit"), 200)
	if limit < 50 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	list := procs.Select(rows, procs.ListOptions{
		Limit: limit, Order: procs.ParseOrder(q.Get("sort")), Query: q.Get("q"),
		User: q.Get("user"), State: q.Get("state"), Manager: q.Get("manager"), Group: q.Get("group"),
	})
	return list, nil
}

func (s *Server) handleProcessDetail(w http.ResponseWriter, r *http.Request) error {
	pid, err := strconv.ParseInt(chi.URLParam(r, "pid"), 10, 32)
	if err != nil {
		return httpx.BadRequest("invalid pid")
	}
	p, err := s.modules.table.Detail(r.Context(), int32(pid))
	if err != nil {
		return httpx.ErrNotFound
	}
	rows := []procs.Process{*p}
	if s.modules.pm2.Available() {
		if managed, pmErr := s.modules.pm2.List(r.Context()); pmErr == nil {
			procs.MarkPM2(rows, managed)
		}
	}
	s.procContainers.label(r.Context(), s.modules.docker, rows)
	httpx.JSON(w, http.StatusOK, rows[0])
	return nil
}

func (s *Server) handleProcessTree(w http.ResponseWriter, r *http.Request) error {
	pid, err := strconv.ParseInt(chi.URLParam(r, "pid"), 10, 32)
	if err != nil {
		return httpx.BadRequest("invalid pid")
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	tree, err := s.modules.table.Tree(ctx, int32(pid))
	if err != nil {
		return httpx.ErrNotFound
	}
	httpx.JSON(w, http.StatusOK, tree)
	return nil
}

type priorityRequest struct {
	Nice      *int   `json:"nice"`
	StartedAt string `json:"startedAt,omitempty"`
}

func (s *Server) handleProcessPriority(w http.ResponseWriter, r *http.Request) error {
	pid64, err := strconv.ParseInt(chi.URLParam(r, "pid"), 10, 32)
	if err != nil {
		return httpx.BadRequest("invalid pid")
	}
	var req priorityRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Nice == nil {
		return httpx.BadRequest("nice is required")
	}
	p, err := s.modules.table.Detail(r.Context(), int32(pid64))
	if err != nil {
		return httpx.ErrNotFound
	}
	if err := requireSameProcess(p, req.StartedAt); err != nil {
		return err
	}
	if p.State == "zombie" {
		return refusedControl(p, procs.ErrZombie)
	}
	if err := s.modules.table.SetNice(r.Context(), int32(pid64), *req.Nice); err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "process.priority", p.Name,
		map[string]any{"pid": pid64, "previous": p.Nice, "nice": *req.Nice})
	httpx.NoContent(w)
	return nil
}

type signalRequest struct {
	Signal    string `json:"signal"`
	StartedAt string `json:"startedAt,omitempty"`
}

func (s *Server) handleProcessSignal(w http.ResponseWriter, r *http.Request) error {
	pid64, err := strconv.ParseInt(chi.URLParam(r, "pid"), 10, 32)
	if err != nil {
		return httpx.BadRequest("invalid pid")
	}
	pid := int32(pid64)
	var req signalRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Signal == "" {
		req.Signal = "SIGTERM"
	}
	detail, err := s.modules.table.Detail(r.Context(), pid)
	if err != nil {
		return httpx.ErrNotFound
	}
	if err := requireSameProcess(detail, req.StartedAt); err != nil {
		return err
	}
	if err := procs.Controllable(detail); err != nil {
		return refusedControl(detail, err)
	}
	// No typed phrase: signalling a process is the process table's whole
	// purpose, and a supervised one comes straight back. The dialog carries the
	// pid and the command line, which is what identifies the right row.
	if err := s.modules.table.Signal(r.Context(), pid, req.Signal); err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "process.signal", detail.Name,
		map[string]any{"pid": pid, "signal": req.Signal, "cmdline": detail.Cmdline})
	httpx.NoContent(w)
	return nil
}

// refusedControl says why a signal or a priority would not reach a process,
// as a conflict with the process's state rather than a malformed request. A
// zombie's answer names the parent, which is the process that can clear it.
func refusedControl(p *procs.Process, err error) error {
	if errors.Is(err, procs.ErrZombie) {
		return httpx.Err(http.StatusConflict, "process_zombie",
			fmt.Sprintf("%s (%d) has already exited; its parent, PID %d, has not collected its exit status. Restarting or ending the parent clears it.", p.Name, p.PID, p.PPID))
	}
	return httpx.Err(http.StatusConflict, "kernel_thread",
		fmt.Sprintf("%s (%d) is a kernel thread, and the kernel does not deliver signals to its own threads.", p.Name, p.PID))
}

// containerNames is each running container's name by the twelve-character
// id a process's cgroup carries, read from Docker at most every few seconds:
// the table polls every two to thirty, and a process row said
// "Container · 3f9a1c0b7d2e" where the Docker pages say "postgres".
type containerNames struct {
	mu    sync.Mutex
	names map[string]string
	read  time.Time
}

const containerNamesFresh = 10 * time.Second

func (c *containerNames) label(ctx context.Context, docker *dockerx.Client, rows []procs.Process) {
	if docker == nil || !slices.ContainsFunc(rows, func(p procs.Process) bool { return p.Manager == "container" }) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.read) > containerNamesFresh {
		listCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		list, err := docker.ListContainers(listCtx, false)
		cancel()
		// A Docker that does not answer keeps the names it last gave and is
		// not asked again until they would have gone stale, so a hung socket
		// costs one poll its two seconds rather than every poll.
		c.read = time.Now()
		if err == nil {
			c.names = make(map[string]string, len(list))
			for _, ct := range list {
				if len(ct.ID) >= 12 && ct.Name != "" {
					c.names[ct.ID[:12]] = strings.TrimPrefix(ct.Name, "/")
				}
			}
		}
	}
	for i := range rows {
		if rows[i].Manager == "container" {
			rows[i].ManagerLabel = c.names[rows[i].ManagerName]
		}
	}
}

// A PID can be reused between a table poll and a click. The create timestamp
// turns the pair into the process the operator actually saw; old API clients
// that do not send it keep working, while this UI fails closed on a mismatch.
func requireSameProcess(process *procs.Process, startedAt string) error {
	if startedAt == "" {
		return nil
	}
	started, err := time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		return httpx.BadRequest("invalid process start time")
	}
	if process.CreateTime.IsZero() || !process.CreateTime.Equal(started) {
		return httpx.Err(http.StatusConflict, "process_replaced",
			"that PID now belongs to a different process; refresh the list and try again")
	}
	return nil
}

func (s *Server) handleCronUsers(w http.ResponseWriter, r *http.Request) error {
	users, err := s.modules.cron.ListCrontabUsers(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, users)
	return nil
}

func (s *Server) handleCronSystem(w http.ResponseWriter, r *http.Request) error {
	files, err := s.modules.cron.SystemCronFiles(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, files)
	return nil
}

func (s *Server) handleCronUserGet(w http.ResponseWriter, r *http.Request) error {
	ct, err := s.modules.cron.UserCrontab(r.Context(), chi.URLParam(r, "user"))
	if err != nil {
		return mapProcsError(err)
	}
	httpx.JSON(w, http.StatusOK, ct)
	return nil
}

type crontabRequest struct {
	Content string `json:"content"`
}

func (s *Server) handleCronUserPut(w http.ResponseWriter, r *http.Request) error {
	user := chi.URLParam(r, "user")
	var req crontabRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := procs.ValidateCrontab(req.Content); err != nil {
		return httpx.BadRequest("%v", err)
	}
	// No typed phrase: this is the Save button of an editor whose contents are
	// on screen, and a Save that asks you to type the account name is a Save
	// nobody reads either.
	if err := s.modules.cron.SetUserCrontab(r.Context(), user, req.Content); err != nil {
		return mapProcsError(err)
	}
	ct, err := s.modules.cron.UserCrontab(r.Context(), user)
	if err != nil {
		return mapProcsError(err)
	}
	httpx.SetAudit(r, "cron.update", user, map[string]any{"jobs": len(ct.Jobs)})
	httpx.JSON(w, http.StatusOK, ct)
	return nil
}

func pm2TargetQuery(r *http.Request) (string, int, error) {
	daemon, raw := r.URL.Query().Get("user"), r.URL.Query().Get("id")
	if daemon == "" && raw == "" {
		return "", -1, nil
	}
	id, err := strconv.Atoi(raw)
	if daemon == "" || err != nil || id < 0 {
		return "", -1, httpx.BadRequest("PM2 account and non-negative process id are required together")
	}
	return daemon, id, nil
}
