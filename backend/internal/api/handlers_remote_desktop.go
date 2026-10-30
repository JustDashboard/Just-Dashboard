package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/remotedesk"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountRemoteDesktopRoutes(r chi.Router) {
	r.Route("/remote-desktop", func(r chi.Router) {
		// A session is a keyboard and mouse on another machine, the same
		// trust as a terminal, and only a person with a browser session may
		// open one; an API token has no screen to draw it on.
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapTerminal), httpx.RequireSession)
			r.Method(http.MethodGet, "/computers/{id}/session", s.handle(s.handleRemoteDesktopSession))
		})
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Cache-Control", "private, no-store")
					next.ServeHTTP(w, r)
				})
			})
			r.Method(http.MethodGet, "/", s.handle(s.handleRemoteDesktopOverview))
			r.Method(http.MethodGet, "/engine", s.handle(s.handleRemoteDesktopEngine))
			r.Method(http.MethodPost, "/engine", s.handle(s.handleRemoteDesktopInstall))
			r.Method(http.MethodGet, "/probe", s.handle(s.handleRemoteDesktopProbe))
			r.Method(http.MethodPost, "/computers", s.handle(s.handleRemoteDesktopAdd))
			r.Method(http.MethodGet, "/computers/{id}", s.handle(s.handleRemoteDesktopComputer))
			r.Method(http.MethodPut, "/computers/{id}", s.handle(s.handleRemoteDesktopUpdate))
			s.mountRemoteDesktopStreamRoutes(r)
			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodDelete, "/engine", s.handle(s.handleRemoteDesktopRemove))
				r.Method(http.MethodDelete, "/computers/{id}", s.handle(s.handleRemoteDesktopDelete))
			})
		})
	})
}

func mapRemoteDesktopError(err error) error {
	var busy *remotedesk.BusyError
	switch {
	case errors.Is(err, remotedesk.ErrNotFound):
		return httpx.ErrNotFound
	case errors.Is(err, remotedesk.ErrInvalid):
		return httpx.Err(http.StatusBadRequest, "invalid", strings.TrimPrefix(err.Error(), remotedesk.ErrInvalid.Error()+": "))
	case errors.As(err, &busy):
		return httpx.Err(http.StatusConflict, "sessions_open", busy.Error())
	}
	return httpx.Internal(err)
}

func remoteDesktopID(r *http.Request) (string, error) {
	id := chi.URLParam(r, "id")
	if !remotedesk.ValidID(id) {
		return "", httpx.ErrNotFound
	}
	return id, nil
}

// remoteDesktopTailnet hands the module the tailnet as the network page
// reads it, trimmed to what choosing and reaching a computer needs.
func (s *Server) remoteDesktopTailnet(ctx context.Context) (remotedesk.Tailnet, error) {
	view, err := s.modules.network.Tailscale(ctx, "")
	t := remotedesk.Tailnet{Peers: []remotedesk.Peer{}}
	if view == nil {
		return t, err
	}
	t.Installed = view.Installed
	if view.Self != nil {
		t.Self = &remotedesk.Peer{Name: view.Self.HostName, DNSName: view.Self.DNSName, OS: view.Self.OS,
			Address: firstAddress(view.Self.TailscaleIPs), Online: view.Self.Online}
	}
	for _, p := range view.Peers {
		t.Peers = append(t.Peers, remotedesk.Peer{ID: p.ID, Name: p.HostName, DNSName: p.DNSName, OS: p.OS,
			Address: firstAddress(p.TailscaleIPs), Online: p.Online, Direct: p.Direct, Relay: p.Relay, LastSeen: p.LastSeen})
	}
	return t, err
}

// remoteDesktopMedia is where the stream bridge publishes its WebRTC ports:
// this server's own tailnet IPv4 address, which only the tailnet reaches.
// Off a tailnet there is none, and streams ride the dashboard's connection.
func (s *Server) remoteDesktopMedia(ctx context.Context) string {
	view, _ := s.modules.network.Tailscale(ctx, "")
	if view == nil || view.Self == nil {
		return ""
	}
	for _, address := range view.Self.TailscaleIPs {
		if ip := net.ParseIP(address); ip != nil && ip.To4() != nil {
			return ip.String()
		}
	}
	return ""
}

// remoteDesktopBridge is the stream bridge the dashboard drives: Moonlight
// Web as a container beside guacd, or, when JD_REMOTE_DESKTOP_BRIDGE names
// one, a bridge on another machine the proxy reaches over the tailnet. The
// address was checked when the configuration was read.
func (s *Server) remoteDesktopBridge() remotedesk.Engine {
	if s.Cfg.RemoteDesktopBridge != "" {
		if bridge, err := remotedesk.NewRemoteBridge(s.Cfg.RemoteDesktopBridge); err == nil {
			return bridge
		}
	}
	return remotedesk.NewDockerStreamBridge(s.Cfg.DockerHost, s.remoteDesktopMedia)
}

// remoteDesktopWarm asks Tailscale for its direct path to a computer before
// a session opens to it. The network module is made after Remote Desktop's,
// so it is read when asked rather than when wired.
func (s *Server) remoteDesktopWarm(ctx context.Context, address string) {
	if s.modules.network != nil {
		s.modules.network.TailscaleWarm(ctx, address)
	}
}

func firstAddress(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (s *Server) handleRemoteDesktopOverview(w http.ResponseWriter, r *http.Request) error {
	o, err := s.modules.remoteDesktop.Overview(r.Context())
	if err != nil {
		return mapRemoteDesktopError(err)
	}
	httpx.JSON(w, http.StatusOK, o)
	return nil
}

func (s *Server) handleRemoteDesktopEngine(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, s.modules.remoteDesktop.Engine(r.Context()))
	return nil
}

func (s *Server) handleRemoteDesktopInstall(w http.ResponseWriter, r *http.Request) error {
	st := s.modules.remoteDesktop.Engine(r.Context())
	switch st.State {
	case remotedesk.EngineUnavailable:
		return httpx.Err(http.StatusServiceUnavailable, "docker_unavailable", "Docker is not reachable from this dashboard.").
			Because("Remote Desktop runs guacd as a container, so it needs the Docker daemon.", st.Error).Retry()
	case remotedesk.EngineForeign:
		return httpx.Err(http.StatusConflict, "engine_foreign", st.Error)
	}
	httpx.SetAudit(r, "remote_desktop.engine.install", remotedesk.GuacdImage, nil)
	spec := jobs.Spec{
		Capability: auth.CapSystemAdmin,
		Kind:       "remote-desktop.install", Title: "Install Remote Desktop", Target: "guacd " + remotedesk.GuacdVersion,
		StartedBy: httpx.MustPrincipal(r).Username(), Timeout: 15 * time.Minute,
	}
	job, _ := s.modules.jobs.StartExclusive("remote-desktop.", spec, func(ctx context.Context, out jobs.Emitter) error {
		out.Status("Installing guacd %s", remotedesk.GuacdVersion)
		return s.modules.remoteDesktop.InstallEngine(ctx, func(line string) { out.Line("stdout", line) })
	})
	httpx.JSON(w, http.StatusAccepted, job)
	return nil
}

func (s *Server) handleRemoteDesktopRemove(w http.ResponseWriter, r *http.Request) error {
	httpx.SetAudit(r, "remote_desktop.engine.remove", remotedesk.GuacdImage, nil)
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	if err := s.modules.remoteDesktop.RemoveEngine(ctx); err != nil {
		var busy *remotedesk.BusyError
		if errors.As(err, &busy) {
			return mapRemoteDesktopError(err)
		}
		return httpx.Err(http.StatusConflict, "engine_remove_failed", err.Error())
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleRemoteDesktopProbe(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	port, err := strconv.Atoi(q.Get("port"))
	if err != nil {
		return httpx.BadRequest("port must be a number")
	}
	reach, err := s.modules.remoteDesktop.ProbeTarget(r.Context(), q.Get("host"), port)
	if err != nil {
		return mapRemoteDesktopError(err)
	}
	httpx.JSON(w, http.StatusOK, reach)
	return nil
}

// The audit names where a session would go, never the password.
func remoteDesktopAuditDetail(in remotedesk.ComputerInput) map[string]any {
	return map[string]any{"host": in.Host, "port": in.Port, "protocol": in.Protocol, "os": in.OS,
		"username": in.Username, "passwordChanged": in.Password != nil}
}

func (s *Server) handleRemoteDesktopAdd(w http.ResponseWriter, r *http.Request) error {
	var in remotedesk.ComputerInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	httpx.SetAudit(r, "remote_desktop.computer.add", in.Name, remoteDesktopAuditDetail(in))
	c, err := s.modules.remoteDesktop.Add(r.Context(), in)
	if err != nil {
		return mapRemoteDesktopError(err)
	}
	httpx.JSON(w, http.StatusCreated, c)
	return nil
}

func (s *Server) handleRemoteDesktopComputer(w http.ResponseWriter, r *http.Request) error {
	id, err := remoteDesktopID(r)
	if err != nil {
		return err
	}
	v, err := s.modules.remoteDesktop.View(r.Context(), id)
	if err != nil {
		return mapRemoteDesktopError(err)
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) handleRemoteDesktopUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := remoteDesktopID(r)
	if err != nil {
		return err
	}
	var in remotedesk.ComputerInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	httpx.SetAudit(r, "remote_desktop.computer.update", id, remoteDesktopAuditDetail(in))
	c, err := s.modules.remoteDesktop.Update(r.Context(), id, in)
	if err != nil {
		return mapRemoteDesktopError(err)
	}
	httpx.JSON(w, http.StatusOK, c)
	return nil
}

func (s *Server) handleRemoteDesktopDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := remoteDesktopID(r)
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "remote_desktop.computer.delete", id, nil)
	if err := s.modules.remoteDesktop.Delete(r.Context(), id); err != nil {
		return mapRemoteDesktopError(err)
	}
	httpx.NoContent(w)
	return nil
}

// handleRemoteDesktopSession is the screen itself. The session is logged
// when it opens, as a terminal's is, so a crash mid-session still leaves the
// record of who was connected to what.
func (s *Server) handleRemoteDesktopSession(w http.ResponseWriter, r *http.Request) error {
	id, err := remoteDesktopID(r)
	if err != nil {
		return err
	}
	c, err := s.modules.remoteDesktop.Computer(r.Context(), id)
	if err != nil {
		return mapRemoteDesktopError(err)
	}
	detail := map[string]any{"id": id, "host": c.Host, "port": c.Port, "protocol": c.Protocol}
	s.recordAudit(r, "remote_desktop.session.open", c.Name, detail)
	conn, err := s.WS.UpgradeProtocol(w, r, "guacamole")
	if err != nil {
		return nil
	}
	defer conn.Close()
	ctx, cancel := contextWithCancel(r)
	defer cancel()
	go conn.Keepalive(ctx)

	q := r.URL.Query()
	display := remotedesk.Display{
		Width: atoiDefault(q.Get("width"), 0), Height: atoiDefault(q.Get("height"), 0),
		DPI: atoiDefault(q.Get("dpi"), 0), Timezone: q.Get("timezone"),
	}
	sess, err := s.modules.remoteDesktop.Connect(ctx, id, display, httpx.MustPrincipal(r).Username())
	if err != nil {
		remotedesk.RefuseSocket(conn, err)
		detail["error"] = err.Error()
		s.recordAudit(r, "remote_desktop.session.refused", c.Name, detail)
		return nil
	}
	relayErr := sess.Relay(ctx, conn)
	s.modules.remoteDesktop.Finish(ctx, sess, relayErr)
	s.recordAudit(r, "remote_desktop.session.close", c.Name, map[string]any{"id": id})
	return nil
}
