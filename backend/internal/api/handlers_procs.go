package api

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountProcessRoutes(r chi.Router) {
	r.Route("/pm2", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handlePM2List))
		r.Method(http.MethodGet, "/{name}/logs/stream", s.handle(s.handlePM2LogStream))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapServiceControl))
			r.Method(http.MethodPost, "/save", s.handle(s.handlePM2Save))
			r.Method(http.MethodPost, "/{name}/start", s.handle(s.pm2Action(procs.PM2Start)))
			r.Method(http.MethodPost, "/{name}/reload", s.handle(s.pm2Action(procs.PM2Reload)))
		})
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodPost, "/{name}/stop", s.handle(s.pm2Action(procs.PM2Stop)))
			r.Method(http.MethodPost, "/{name}/restart", s.handle(s.pm2Action(procs.PM2Restart)))
			r.Method(http.MethodDelete, "/{name}", s.handle(s.pm2Action(procs.PM2Delete)))
		})
	})

	r.Route("/systemd", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleUnitList))
		r.Method(http.MethodGet, "/{name}", s.handle(s.handleUnitShow))
		r.Method(http.MethodGet, "/{name}/journal", s.handle(s.handleUnitJournal))
		r.Method(http.MethodGet, "/{name}/journal/stream", s.handle(s.handleUnitJournalStream))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapServiceControl))
			r.Method(http.MethodPost, "/{name}/start", s.handle(s.unitAction(procs.UnitStart)))
			r.Method(http.MethodPost, "/{name}/reload", s.handle(s.unitAction(procs.UnitReload)))
		})
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
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
	httpx.JSON(w, http.StatusOK, map[string]any{"available": true, "processes": list})
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

// handlePM2LogStream tails a process's stdout and stderr files. Following the
// files rather than `pm2 logs` means the stream survives a restart of the
// process and reports which stream each line came from.
func (s *Server) handlePM2LogStream(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	daemon, id, err := pm2TargetQuery(r)
	if err != nil {
		return err
	}
	outPath, errPath, err := s.modules.pm2.LogPathsTarget(r.Context(), name, daemon, id)
	if err != nil {
		return mapProcsError(err)
	}
	if err := s.checkPM2LogPaths(outPath, errPath); err != nil {
		return err
	}

	conn, err := s.WS.Upgrade(w, r)
	if err != nil {
		return nil
	}
	defer conn.Close()
	ctx, cancel := contextWithCancel(r)
	defer cancel()
	go conn.Keepalive(ctx)
	go conn.DrainControl(cancel)

	lines := atoiDefault(r.URL.Query().Get("lines"), 300)
	events := make(chan taggedLine, 256)
	started := 0
	for _, src := range []struct {
		path, stream string
	}{{outPath, "stdout"}, {errPath, "stderr"}} {
		if src.path == "" || src.path == "/dev/null" {
			continue
		}
		started++
		go s.modules.logs.TailInto(ctx, src.path, lines, func(text string) {
			select {
			case <-ctx.Done():
			case events <- taggedLine{Stream: src.stream, Text: text}:
			}
		})
	}
	if started == 0 {
		conn.SendError("pm2 process " + name + " has no readable log files")
		return nil
	}
	return pumpTagged(ctx, conn, events)
}

type taggedLine struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// pumpTagged batches lines onto the socket. Batching matters for busy logs:
// one frame per line saturates the browser's event loop long before it
// saturates the network.
func pumpTagged(ctx context.Context, conn interface {
	Send(string, any) error
}, events <-chan taggedLine) error {
	batch := make([]taggedLine, 0, 256)
	t := time.NewTicker(150 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case line := <-events:
			batch = append(batch, line)
			if len(batch) >= 256 {
				if err := conn.Send("logs", batch); err != nil {
					return nil
				}
				batch = batch[:0]
			}
		case <-t.C:
			if len(batch) > 0 {
				if err := conn.Send("logs", batch); err != nil {
					return nil
				}
				batch = batch[:0]
			}
		}
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

func (s *Server) handleUnitJournal(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	cmd, err := procs.JournalCommand(r.Context(), chi.URLParam(r, "name"),
		atoiDefault(q.Get("lines"), 300), false, q.Get("since"))
	if err != nil {
		return mapProcsError(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return httpx.Internal(err)
	}
	if err := cmd.Start(); err != nil {
		return mapProcsError(err)
	}
	entries := []procs.JournalEntry{}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if e, ok := procs.ParseJournalLine(sc.Bytes()); ok {
			entries = append(entries, e)
		}
	}
	cmd.Wait()
	httpx.JSON(w, http.StatusOK, entries)
	return nil
}

func (s *Server) handleUnitJournalStream(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	conn, err := s.WS.Upgrade(w, r)
	if err != nil {
		return nil
	}
	defer conn.Close()
	ctx, cancel := contextWithCancel(r)
	defer cancel()
	go conn.Keepalive(ctx)
	go conn.DrainControl(cancel)

	cmd, err := procs.JournalCommand(ctx, chi.URLParam(r, "name"),
		atoiDefault(q.Get("lines"), 300), true, q.Get("since"))
	if err != nil {
		conn.SendError(err.Error())
		return nil
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		conn.SendError(err.Error())
		return nil
	}
	if err := cmd.Start(); err != nil {
		conn.SendError(err.Error())
		return nil
	}
	// Killing the process group on exit stops journalctl -f; otherwise it
	// would linger after the browser tab closes.
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		cmd.Wait()
	}()

	batch := make([]procs.JournalEntry, 0, 128)
	flush := time.NewTicker(200 * time.Millisecond)
	defer flush.Stop()
	lines := make(chan procs.JournalEntry, 256)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			if e, ok := procs.ParseJournalLine(sc.Bytes()); ok {
				select {
				case <-ctx.Done():
					return
				case lines <- e:
				}
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case e, ok := <-lines:
			if !ok {
				if len(batch) > 0 {
					conn.Send("journal", batch)
				}
				conn.Send("eof", nil)
				return nil
			}
			batch = append(batch, e)
			if len(batch) >= 128 {
				if err := conn.Send("journal", batch); err != nil {
					return nil
				}
				batch = batch[:0]
			}
		case <-flush.C:
			if len(batch) > 0 {
				if err := conn.Send("journal", batch); err != nil {
					return nil
				}
				batch = batch[:0]
			}
		}
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
		User: q.Get("user"), State: q.Get("state"), Manager: q.Get("manager"),
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
	if s.modules.pm2.Available() {
		if managed, pmErr := s.modules.pm2.List(r.Context()); pmErr == nil {
			rows := []procs.Process{*p}
			procs.MarkPM2(rows, managed)
			*p = rows[0]
		}
	}
	httpx.JSON(w, http.StatusOK, p)
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
