package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

// mountNetworkTrafficRoutes mounts per-process and per-container traffic,
// transfer budgets, chart annotations and eBPF under /network.
//
// Registered as Methods on the /network router and not as a /traffic Route:
// mountNetworkRoutes already owns /traffic/live and /traffic/history that way,
// and a Route on the same prefix would replace them (handlers_security.go
// records the time that happened to /ssh-sessions).
func (s *Server) mountNetworkTrafficRoutes(r chi.Router) {
	// Which program talks to which remote address is a map of what this
	// machine does and who it does it with, so it is system.admin for the
	// reason the connection table's owners and the SSH configuration are.
	// The annotations name who changed what and the incidents an operator
	// saved, which are the audit log's and the saved runs' to show.
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/traffic/processes", s.handle(s.handleNetworkProcesses))
		r.Method(http.MethodGet, "/traffic/annotations", s.handle(s.handleNetworkTrafficAnnotations))
		// A budget only alerts, so setting one is an ordinary change; clearing
		// one removes the alert somebody relied on, as clearing a WireGuard
		// peer's does, and takes the destructive budget.
		r.Method(http.MethodPut, "/traffic/quotas/{iface}", s.handle(s.handleInterfaceQuotaSet))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/traffic/quotas/{iface}", s.handle(s.handleInterfaceQuotaClear))
		})
	})
	// Per container is the recorder's byte counters, which the Docker pages
	// already show to every role that can read them; so are the budgets,
	// which are figures about a device like its rates.
	r.Method(http.MethodGet, "/traffic/containers", s.handle(s.handleNetworkContainers))
	r.Method(http.MethodGet, "/traffic/containers/{name}", s.handle(s.handleNetworkContainer))
	r.Method(http.MethodGet, "/traffic/quotas", s.handle(s.handleInterfaceQuotas))
	// Loaded eBPF programs and where they are attached: the same standing as
	// the interface list that already names an XDP program on a device.
	r.Method(http.MethodGet, "/ebpf", s.handle(s.handleNetworkEBPF))
	r.Method(http.MethodGet, "/ebpf/{id}", s.handle(s.handleNetworkEBPFProgram))
}

// observerPrograms is the dashboard's own kernel observer's programs while
// attached, which the inventory marks as the dashboard's.
func (s *Server) observerPrograms() []uint32 {
	if s.modules.flowAccounting == nil {
		return []uint32{}
	}
	return s.modules.flowAccounting.Standing().ObserverProgramIDs
}

func (s *Server) handleNetworkProcesses(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	res, err := s.modules.network.Processes(ctx)
	if err != nil {
		return mapNetworkError(err)
	}
	if s.modules.flowAccounting != nil {
		st := s.modules.flowAccounting.Standing()
		res.History = &netx.RecordedHistory{Recording: st.Recording, KernelObserver: st.KernelObserver,
			Since: st.Since, RetentionDays: st.RetentionDays}
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func containerWindow(r *http.Request) (time.Duration, error) {
	window := time.Hour
	if raw := r.URL.Query().Get("window"); raw != "" {
		d, err := metrics.ParseWindow(raw)
		if err != nil || d <= 0 || d > 31*24*time.Hour {
			return 0, httpx.BadRequest("window is a duration up to 31d, such as 1h, 6h, 24h or 7d")
		}
		window = d
	}
	return window, nil
}

func (s *Server) handleNetworkContainers(w http.ResponseWriter, r *http.Request) error {
	window, err := containerWindow(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	res, err := s.modules.network.Containers(ctx, window)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// containerTraffic is one container's recorded traffic with what Docker says
// it is now: its id (which the socket history attributes peers by), image,
// compose service and networks. A container that is gone keeps its history
// and has no Docker half.
type containerTraffic struct {
	netx.ContainerFlow
	WindowSeconds int64    `json:"windowSeconds"`
	ID            string   `json:"id,omitempty"`
	Image         string   `json:"image,omitempty"`
	State         string   `json:"state,omitempty"`
	Project       string   `json:"project,omitempty"`
	Service       string   `json:"service,omitempty"`
	Networks      []string `json:"networks"`
	// DockerError is why the Docker half is missing when it is.
	DockerError string `json:"dockerError,omitempty"`
}

func (s *Server) handleNetworkContainer(w http.ResponseWriter, r *http.Request) error {
	window, err := containerWindow(r)
	if err != nil {
		return err
	}
	name := chi.URLParam(r, "name")
	if name == "" || len(name) > 255 {
		return httpx.BadRequest("a container name is required")
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	flow, err := s.modules.network.ContainerDetail(ctx, name, window)
	if err != nil {
		if errors.Is(err, netx.ErrNotFound) {
			return httpx.Err(http.StatusNotFound, "not_found", "nothing is recorded for "+name+" in this window")
		}
		return httpx.Internal(err)
	}
	out := containerTraffic{ContainerFlow: *flow, WindowSeconds: int64(window.Seconds()), Networks: []string{}}
	if s.modules.docker == nil {
		out.DockerError = "Docker is not available to this dashboard"
	} else if list, err := s.modules.docker.ListContainers(ctx, true); err != nil {
		out.DockerError = "Docker could not be read: " + err.Error()
	} else {
		for _, c := range list {
			if c.Name != name {
				continue
			}
			out.ID, out.Image, out.State = c.ID, c.Image, c.State
			out.Project, out.Service = c.ComposeStack, c.ComposeSvc
			out.Networks = append(out.Networks, c.Networks...)
			break
		}
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleInterfaceQuotas(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	list, err := s.modules.network.InterfaceQuotas(ctx)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, list)
	return nil
}

func (s *Server) handleInterfaceQuotaSet(w http.ResponseWriter, r *http.Request) error {
	iface := chi.URLParam(r, "iface")
	var req netx.InterfaceQuotaRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	q, err := s.modules.network.SetInterfaceQuota(ctx, iface, req, actor(r))
	if err != nil {
		auditChange(r, "network.traffic.quota.set", iface, req, err)
		return mapNetworkError(err)
	}
	httpx.SetAudit(r, "network.traffic.quota.set", iface, req)
	httpx.JSON(w, http.StatusOK, q)
	return nil
}

func (s *Server) handleInterfaceQuotaClear(w http.ResponseWriter, r *http.Request) error {
	iface := chi.URLParam(r, "iface")
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	if err := s.modules.network.ClearInterfaceQuota(ctx, iface); err != nil {
		auditChange(r, "network.traffic.quota.clear", iface, nil, err)
		return mapNetworkError(err)
	}
	httpx.SetAudit(r, "network.traffic.quota.clear", iface, nil)
	httpx.NoContent(w)
	return nil
}

// handleNetworkTrafficAnnotations is the changes and incidents of a window,
// in the shape the charts mark: `from` and `to` as unix seconds or RFC3339.
func (s *Server) handleNetworkTrafficAnnotations(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	to := time.Now()
	from := to.Add(-time.Hour)
	if raw := q.Get("to"); raw != "" {
		at, err := parseInstant(raw)
		if err != nil {
			return httpx.BadRequest("to: %v", err)
		}
		to = at
	}
	if raw := q.Get("from"); raw != "" {
		at, err := parseInstant(raw)
		if err != nil {
			return httpx.BadRequest("from: %v", err)
		}
		from = at
	} else if raw := q.Get("window"); raw != "" {
		d, err := metrics.ParseWindow(raw)
		if err != nil || d <= 0 {
			return httpx.BadRequest("window is a duration up to 31d")
		}
		from = to.Add(-d)
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	list, err := s.modules.network.TrafficAnnotations(ctx, from, to)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, list)
	return nil
}

func (s *Server) handleNetworkEBPF(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	res, err := s.modules.network.EBPF(ctx)
	if err != nil {
		return mapNetworkError(err)
	}
	res.ObserverProgramIDs = s.observerPrograms()
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleNetworkEBPFProgram(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		return httpx.BadRequest("a program id is a positive number")
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	d, err := s.modules.network.EBPFProgram(ctx, id)
	if err != nil {
		return mapNetworkError(err)
	}
	for _, own := range s.observerPrograms() {
		if int(own) == id {
			d.Observer = true
		}
	}
	httpx.JSON(w, http.StatusOK, d)
	return nil
}
