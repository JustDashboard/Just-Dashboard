package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/portalloc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// portListTimeout bounds the walk over every process's descriptors, which on
// a host with tens of thousands of them is the slow part. The page polls it,
// so a stuck walk must answer rather than pile up behind itself.
const portListTimeout = 10 * time.Second

// mountPortRoutes is the host's listening sockets. A subrouter rather than a
// single route so the page's detail views can mount beside it; chi serves the
// index at both /ports and /ports/, and the dashboard asks for the first.
func (s *Server) mountPortRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/", s.handle(s.handlePortList))
	r.Method(http.MethodGet, "/meta", s.handle(s.handlePortsMeta))
	r.Method(http.MethodGet, "/firewall", s.handle(s.handlePortsFirewall))
	s.mountPortHistoryRoutes(r)
	// Finding a free port binds each candidate for a moment, on an address
	// the caller names; only someone who can then publish on it may ask.
	r.With(httpx.RequireCapability(auth.CapSystemAdmin)).
		Method(http.MethodGet, "/free", s.handle(s.handlePortsFree))
}

// portsFirewall is the firewall as the ports page needs it: whether rules
// can be handed off to the firewall page, and the rules nothing answers.
// Every signed-in account may read the firewall's rules at /firewall, so
// this says nothing a read-only account could not already see.
type portsFirewall struct {
	Backend   string `json:"backend"`
	Available bool   `json:"available"`
	Enabled   bool   `json:"enabled"`
	Editable  bool   `json:"editable"`
	Incoming  string `json:"incoming,omitempty"`
	// OrphanRules are the inbound rules admitting a port nothing listens
	// on and Docker does not publish.
	OrphanRules []netsec.Rule `json:"orphanRules"`
}

// handlePortsFirewall reads the firewall afresh on every call rather than
// from a cache: the page deletes an orphan rule by its number, and ufw
// renumbers every rule after one it deletes, so a listing even seconds old
// can name a different rule.
func (s *Server) handlePortsFirewall(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, portListTimeout)
	defer cancel()
	status, err := s.modules.netsec.Status(ctx)
	if err != nil {
		status = nil
	}
	out := portsFirewall{OrphanRules: []netsec.Rule{}}
	if status == nil {
		httpx.JSON(w, http.StatusOK, out)
		return nil
	}
	out.Backend, out.Available, out.Enabled = string(status.Backend), status.Available, status.Enabled
	out.Editable, out.Incoming = status.Capabilities.Editable, status.Policy.Incoming
	if !status.Available || status.Error != "" {
		httpx.JSON(w, http.StatusOK, out)
		return nil
	}
	owners := make(chan proxysvc.OwnerInput, 1)
	go func() { owners <- s.ownerInput(ctx, false) }()
	listeners, err := proxysvc.ListListeners(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		return httpx.Err(http.StatusGatewayTimeout, "timeout",
			"Listing the host's sockets took longer than 10 seconds.").Retry()
	}
	if err != nil {
		return httpx.Internal(err)
	}
	// A port Docker publishes through NAT alone has no socket; attributing
	// owners is what adds it, so its rule is not called an orphan.
	listeners = proxysvc.AttributeOwners(listeners, <-owners)
	open := make([]netsec.OpenPort, 0, len(listeners))
	for _, l := range listeners {
		open = append(open, netsec.OpenPort{Port: l.Port, Protocol: l.Protocol})
	}
	out.OrphanRules = netsec.OrphanRules(status, open)
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// portsMeta is what the ports page reads besides the sockets, apart from the
// list so that stays the plain array other pages already read.
type portsMeta struct {
	// EphemeralRange is null where the kernel's range cannot be read, and
	// the page then offers nothing that depends on it.
	EphemeralRange *proxysvc.PortRange `json:"ephemeralRange"`
}

// handlePortsMeta tells the page which ports the kernel hands out on its own,
// so it can set aside the loopback sockets that were given one.
func (s *Server) handlePortsMeta(w http.ResponseWriter, r *http.Request) error {
	meta := portsMeta{}
	if span, err := proxysvc.EphemeralPorts(); err == nil {
		meta.EphemeralRange = &span
	}
	httpx.JSON(w, http.StatusOK, meta)
	return nil
}

func (s *Server) handlePortList(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, portListTimeout)
	defer cancel()
	// The firewall's status is the posture's other input to a port's level,
	// and the owners' sources say whose each socket is: both are read beside
	// the walk rather than after it.
	firewall := make(chan *netsec.FirewallStatus, 1)
	go func() {
		status, err := s.modules.netsec.Status(ctx)
		if err != nil {
			status = nil
		}
		firewall <- status
	}()
	owners := make(chan proxysvc.OwnerInput, 1)
	go func() { owners <- s.ownerInput(ctx, true) }()
	proxy := make(chan portsProxy, 1)
	go func() { proxy <- s.readPortsProxy(ctx) }()
	listeners, err := proxysvc.ListListeners(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		return httpx.Err(http.StatusGatewayTimeout, "timeout",
			"Listing the host's sockets took longer than 10 seconds.").Retry()
	}
	if err != nil {
		return httpx.Internal(err)
	}
	in := <-owners
	in.PM2 = s.pm2Apps(ctx, listeners)
	listeners = proxysvc.AttributeOwners(listeners, in)
	s.nameDeployments(ctx, listeners)
	placeListeners(listeners, netsec.ReadHostNetwork(ctx), <-firewall)
	routes := <-proxy
	proxysvc.AttachProxy(listeners, routes.vhosts, routes.streams)
	httpx.JSON(w, http.StatusOK, s.withFirstSeen(r, listeners))
	return nil
}

// portsProxy is the proxy's sites and streams, as the ports page says what
// routes to each socket.
type portsProxy struct {
	vhosts  []proxysvc.VHost
	streams *proxysvc.StreamStatus
}

// readPortsProxy reads the proxy's configuration beside the socket walk. A
// host without a proxy, or a Docker Caddy that does not answer in time,
// leaves the sockets without routes rather than the listing failing.
func (s *Server) readPortsProxy(ctx context.Context) portsProxy {
	ctx, cancel := context.WithTimeout(ctx, ownerSourceTimeout)
	defer cancel()
	vhosts, err := s.modules.proxy.ListVHosts(ctx)
	if err != nil {
		vhosts = nil
	}
	// A status that could not be read is nil, and its sockets go without routes.
	streams, _ := s.modules.proxy.Streams(ctx)
	return portsProxy{vhosts: vhosts, streams: streams}
}

// freePortsMax bounds how many ports one call finds: each is a bind and a
// close, and a form needs one.
const freePortsMax = 20

// portsFree is GET /ports/free.
type portsFree struct {
	Ports []int `json:"ports"`
	// Skipped is the containers' ports the search passed over although
	// nothing listens on them: a stopped container's, which it binds again
	// when it starts, and a port Docker publishes through NAT alone.
	Skipped []dockerx.HostPortBinding `json:"skipped"`
	// ContainersChecked is false where Docker could not be asked, so the
	// page does not claim to have kept clear of containers' ports.
	ContainersChecked bool `json:"containersChecked"`
}

// handlePortsFree finds ports nothing listens on and no container keeps,
// searching up from `from` and wrapping round, by binding each candidate on
// the address and protocol given. A binding is an observation, not a
// reservation: the port is free when this answers, not when a form is saved.
func (s *Server) handlePortsFree(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	protocol := q.Get("protocol")
	if protocol == "" {
		protocol = "tcp"
	}
	if protocol != "tcp" && protocol != "udp" {
		return httpx.BadRequest("protocol must be tcp or udp")
	}
	address := q.Get("address")
	if address == "" {
		address = "0.0.0.0"
	}
	if net.ParseIP(address) == nil {
		return httpx.BadRequest("address must be an IP address")
	}
	from, count := 1024, 5
	if v := q.Get("from"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return httpx.BadRequest("from must be a port from 1 to 65535")
		}
		from = n
	}
	if v := q.Get("count"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > freePortsMax {
			return httpx.BadRequest("count must be from 1 to %d", freePortsMax)
		}
		count = n
	}

	out := portsFree{Ports: []int{}, Skipped: []dockerx.HostPortBinding{}}
	kept := map[int]dockerx.HostPortBinding{}
	ctx, cancel := context.WithTimeout(r.Context(), ownerSourceTimeout)
	bindings, err := s.modules.docker.HostPortBindings(ctx)
	cancel()
	if err == nil {
		out.ContainersChecked = true
		for _, b := range bindings {
			if b.Protocol == protocol && bindingOverlaps(b.HostIP, address) {
				kept[b.HostPort] = b
			}
		}
	}
	// A port under 1024 asked for is searched from there; otherwise the
	// search stays among the ports an unprivileged program may take.
	minimum := min(from, 1024)
	passed := map[int]bool{}
	for len(out.Ports) < count {
		port, err := portalloc.Select(from, minimum, passed, func(candidate int) error {
			if b, ok := kept[candidate]; ok {
				if !passed[candidate] {
					out.Skipped = append(out.Skipped, b)
					passed[candidate] = true
				}
				return portalloc.ErrReserved
			}
			return portalloc.Available(address, protocol, candidate)
		})
		if err != nil {
			if len(out.Ports) > 0 {
				break
			}
			return httpx.BadRequest("%v", err)
		}
		out.Ports = append(out.Ports, port)
		passed[port] = true
		from = port
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// bindingOverlaps says whether a container's binding on one address stands
// in the way of a bind on another: either on every address, or both on the
// same one.
func bindingOverlaps(held, wanted string) bool {
	if held == "" || held == "0.0.0.0" || held == "::" || wanted == "0.0.0.0" || wanted == "::" {
		return true
	}
	a, b := net.ParseIP(held), net.ParseIP(wanted)
	return a != nil && b != nil && a.Equal(b)
}

// ownerSourceTimeout bounds each source of a socket's owner. Each is a
// nicety beside the sockets themselves: a Docker daemon, systemd or PM2 that
// does not answer in time leaves the process as the owner rather than the
// listing waiting on it.
const ownerSourceTimeout = 4 * time.Second

// ownerInput reads what names a socket's owner beyond its process: the
// running containers, and for the ports page — everything, not only what
// grades a port — systemd's socket units. Each is read at the same time as
// the other, and each that fails is left out.
func (s *Server) ownerInput(ctx context.Context, everything bool) proxysvc.OwnerInput {
	in := proxysvc.OwnerInput{SelfPID: int32(os.Getpid()), DataDir: s.Cfg.DataDir}
	ctx, cancel := context.WithTimeout(ctx, ownerSourceTimeout)
	defer cancel()
	var wg sync.WaitGroup
	run := func(read func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			read()
		}()
	}
	run(func() {
		containers, err := s.modules.docker.ListRunning(ctx)
		if err != nil {
			return
		}
		in.Containers = runningContainers(containers)
	})
	if everything && s.modules.systemd.Available() {
		run(func() {
			units, err := s.modules.systemd.Sockets(ctx)
			if err != nil {
				return
			}
			for _, u := range units {
				if unit, ok := proxysvc.SocketUnitAt(u.Listen, u.Type, u.Unit, u.Activates); ok {
					in.SocketUnits = append(in.SocketUnits, unit)
				}
			}
		})
	}
	wg.Wait()
	return in
}

// pm2Apps is each PM2 app's PID and name, asked of PM2 only when a socket's
// owner is a PM2 daemon's child: `pm2 jlist` starts a daemon where none is
// running, and merely reading this page must not.
func (s *Server) pm2Apps(ctx context.Context, listeners []proxysvc.Listener) map[int32]string {
	if !proxysvc.UnderPM2(listeners) || !s.modules.pm2.Available() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, ownerSourceTimeout)
	defer cancel()
	apps, err := s.modules.pm2.List(ctx)
	if err != nil {
		return nil
	}
	out := map[int32]string{}
	for _, app := range apps {
		if app.PID > 0 {
			out[int32(app.PID)] = app.Name
		}
	}
	return out
}

// deploymentEnvironmentLabel is what a deployment stamps on its containers.
const deploymentEnvironmentLabel = "io.just-dashboard.environment-id"

func runningContainers(list []dockerx.Container) []proxysvc.RunningContainer {
	out := make([]proxysvc.RunningContainer, 0, len(list))
	for _, c := range list {
		rc := proxysvc.RunningContainer{
			ID: c.ID, Name: c.Name, Image: c.Image,
			Project: c.ComposeStack, Service: c.ComposeSvc,
		}
		if c.Labels["io.just-dashboard.managed"] == "true" {
			rc.EnvironmentID, _ = strconv.ParseInt(c.Labels[deploymentEnvironmentLabel], 10, 64)
		}
		for _, p := range c.Ports {
			rc.Ports = append(rc.Ports, proxysvc.PublishedPort{HostIP: p.IP, HostPort: p.PublicPort, Protocol: p.Type})
		}
		for _, m := range c.Mounts {
			rc.Mounts = append(rc.Mounts, m.Destination)
		}
		out = append(out, rc)
	}
	return out
}

// nameDeployments names the deployment each deployment's container runs
// for. A container whose environment is gone keeps its container name and
// no deployment.
func (s *Server) nameDeployments(ctx context.Context, listeners []proxysvc.Listener) {
	wanted := map[int64][]*proxysvc.ListenerContainer{}
	for i := range listeners {
		if c := listeners[i].Container; c != nil && c.EnvironmentID > 0 {
			wanted[c.EnvironmentID] = append(wanted[c.EnvironmentID], c)
		}
	}
	if len(wanted) == 0 {
		return
	}
	ids := make([]any, 0, len(wanted))
	for id := range wanted {
		ids = append(ids, id)
	}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT e.id, e.name, p.id, p.name
		FROM deploy_environments e JOIN deploy_projects p ON p.id = e.project_id
		WHERE e.id IN (?`+strings.Repeat(",?", len(ids)-1)+`)`, ids...)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var environmentID int64
		var d proxysvc.ListenerDeployment
		if rows.Scan(&environmentID, &d.Environment, &d.ProjectID, &d.Project) != nil {
			continue
		}
		for _, c := range wanted[environmentID] {
			deployment := d
			c.Deployment = &deployment
		}
	}
}

// placeListeners places each socket on its network and grades it by the
// posture's own judgement, so the page cannot call a database critical that
// the posture calls a warning, nor credit a firewall the posture does not:
// the tailnet on tailscale0, Docker's bridge docker0, a port Docker
// publishes past ufw's deny.
func placeListeners(listeners []proxysvc.Listener, network netsec.HostNetwork, firewall *netsec.FirewallStatus) {
	for i := range listeners {
		l := &listeners[i]
		place := network.Place(l.Address)
		l.Reach = string(place.Reach)
		l.Network = string(place.Network)
		l.Interface = place.Interface
		grade := netsec.GradePort(exposedPort(*l), network, firewall)
		l.Level = grade.Level
		l.InboundDefault = grade.InboundDefault
		l.PastFirewall = string(grade.PastFirewall)
		l.FirewallRule = grade.FirewallRule
		if preset, ok := netsec.ServiceOf(exposedPort(*l)); ok {
			l.Service, l.Danger = preset.Name, preset.Danger
		}
		if l.Exposed {
			v := netsec.JudgeFirewall(exposedPort(*l), network, firewall)
			l.Firewall = &proxysvc.ListenerFirewall{
				Verdict: string(v.Verdict), Rule: v.Rule, Action: v.Action, From: v.From,
				Default: v.Default, Backend: string(v.Backend),
			}
		}
	}
}

// exposedPort is a socket as the posture reads it.
func exposedPort(l proxysvc.Listener) netsec.ExposedPort {
	return netsec.ExposedPort{
		Port: l.Port, Protocol: l.Protocol, Address: l.Address, Process: l.Process, Exposed: l.Exposed,
		Published: l.Container != nil && l.Container.Published,
		Dashboard: l.Self && !dashboardCaddy(l),
	}
}

// dashboardCaddy is the dashboard's own Caddy, the one socket of its own
// meant to face the network: the compose file's "proxy" service, which runs
// caddy on the host's network.
func dashboardCaddy(l proxysvc.Listener) bool {
	return l.Process == "caddy" || l.Container != nil && l.Container.Service == "proxy"
}
