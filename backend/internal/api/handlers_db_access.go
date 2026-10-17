package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// Where a database can be reached from, and changing it.
//
// A database this dashboard starts is published to loopback, which is the
// right default and the wrong permanent answer: the operator's next question
// is "how do I connect to it from my laptop", and the honest reply used to be
// a trip to the Docker page to edit a port binding, then to Security to open
// the port, then back here to work out what the connection string would be
// with the server's address in it. Every one of those facts is in this
// process. This file puts them on the connection page as one reading and one
// switch.
//
// Two things are kept apart deliberately. The *binding* — which interface the
// container's port is published on — is a fact Docker states and this route
// changes. *Reachability* from the internet additionally depends on the
// firewall, which is why the reading carries the firewall's answer beside the
// binding rather than folding the two into one word.
//
// Between this server only and anywhere there are two more answers. One
// address of this machine — a tailnet's, a VPN's, a LAN's — is a binding like
// the others. A list of addresses is not a binding at all: the port is
// published on every interface and the network module's gateway table drops a
// new connection to it from any source the list does not name. The host
// firewall cannot be the one to refuse it — Docker's NAT rules forward a
// published port before ufw's or firewalld's input rules are consulted — so
// the firewall gets a rule per listed source for its own listing and for the
// traffic it does see, and the gateway is what keeps everyone else out.

// Exposure is where a connection's server can be reached from, as far as its
// port binding says.
type dbExposure string

const (
	// exposureLocal is bound to loopback: this server, and nothing else.
	exposureLocal dbExposure = "local"
	// exposurePublic is bound to every interface the host has.
	exposurePublic dbExposure = "public"
	// exposurePrivate is bound to one specific address — a LAN, VPN or
	// tailnet interface — chosen from the host's own.
	exposurePrivate dbExposure = "private"
	// exposureAllowlist is bound to every interface with the gateway keeping
	// the port to the networks a list names.
	exposureAllowlist dbExposure = "allowlist"
	// exposureRemote is a server that is not on this host at all, so its
	// reachability is somebody else's decision.
	exposureRemote dbExposure = "remote"
)

// dbAccess is what the connection page reads before it draws the connection
// string block and the access switch.
type dbAccess struct {
	// Detected reports that this server is one the sync would connect on its
	// own — a container or a host listener at the connection's address — so
	// forgetting the connection is pointless: the next page load re-adds it.
	Detected bool `json:"detected"`
	// Container is the Docker container behind the connection, when there is
	// one on this host. Empty for a native server and for a remote one.
	Container string `json:"container,omitempty"`
	// ComposeProject names the stack that owns the container, when one does.
	// A compose-owned binding is changed in the compose file, not here.
	ComposeProject string `json:"composeProject,omitempty"`
	// Managed reports whether this route can change the binding: a container
	// exists, it is not compose-owned, and it publishes the engine's port.
	Managed  bool       `json:"managed"`
	Exposure dbExposure `json:"exposure"`
	// Port is the host port the server answers on.
	Port int `json:"port,omitempty"`
	// PublicAddresses are this machine's globally routable addresses, for
	// the connection string somebody pastes on another machine.
	PublicAddresses []string   `json:"publicAddresses"`
	Firewall        dbFirewall `json:"firewall"`
	// Address is the one host address a private binding publishes on.
	Address string `json:"address,omitempty"`
	// Allow is what the gateway keeps the port to. It can be set while the
	// binding is not every interface: a list a change could not finish
	// taking away, which the next change does.
	Allow   []string  `json:"allow"`
	Gateway dbGateway `json:"gateway"`
	// Addresses are this machine's addresses the port can be published on
	// alone, for a managed server.
	Addresses []dbHostAddress `json:"addresses"`
	// Client is the address this request came from, which the list's form
	// offers to add.
	Client string `json:"client,omitempty"`
}

// dbGateway is the gateway table's part of the answer.
type dbGateway struct {
	// Available says this host can keep a port to a list, and Reason why
	// not.
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	// AlsoAdmitted are the networks every drop in the gateway table lets
	// through first: the dashboard's own allowlist ranges and the addresses
	// kept on the Protection page. Whoever is on them reaches the dashboard,
	// which can already do anything to the database.
	AlsoAdmitted []string `json:"alsoAdmitted"`
	// Enforced says the kernel holds the port's rules now, and Refused how
	// many connection attempts they dropped since the table was loaded.
	Enforced bool   `json:"enforced"`
	Refused  uint64 `json:"refused"`
}

// dbPortKeeper keeps a database's port to a list of networks: the network
// module's gateway table, or a test's stand-in.
type dbPortKeeper interface {
	KeptPorts() ([]netx.PortAllowlistSpec, error)
	PortAllowlists(ctx context.Context) (netx.PortAllowlistReading, error)
	PortAllowlistRules(req netx.PortAllowlistRequest) ([]string, error)
	SetPortAllowlist(ctx context.Context, req netx.PortAllowlistRequest, actor string) (netx.PortAllowlistSpec, error)
	RemovePortAllowlist(ctx context.Context, owner string) (bool, error)
}

func (s *Server) portKeeper() dbPortKeeper {
	if s.dbConns.keeper != nil {
		return s.dbConns.keeper
	}
	if s.modules.network == nil {
		return nil
	}
	return s.modules.network
}

// dbPortOwner is the name a container's list is kept under in the gateway.
func dbPortOwner(container string) string { return "database:" + container }

// dbFirewall is the firewall's part of the answer.
type dbFirewall struct {
	Backend string `json:"backend,omitempty"`
	// Active means a firewall is present and switched on, so a port bound to
	// every interface is still closed until a rule opens it.
	Active bool `json:"active"`
	// Open reports an allow rule for the port from anywhere.
	Open bool `json:"open"`
	// Allowed are the sources the firewall's other allow rules for the port
	// name.
	Allowed []string `json:"allowed"`
	// Editable reports whether a rule can be written from here.
	Editable bool `json:"editable"`
}

// dbAccessRequest is the one change this surface accepts: where the port is
// published, the address for one of the host's own, and the list for
// allowed addresses.
type dbAccessRequest struct {
	Exposure dbExposure `json:"exposure"`
	Address  string     `json:"address,omitempty"`
	Allow    []string   `json:"allow,omitempty"`
}

// dbServer is the container behind a connection, resolved once for the read
// and for the change so they cannot disagree about which container it is.
type dbServer struct {
	container *dockerx.Container
	// port is the container-side port the engine listens on, and binding is
	// the host binding that publishes it, when one does.
	port    int
	binding *dockerx.Port
}

func (s *Server) handleDBAccess(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	access := s.describeDBAccess(ctx, conn, dsn)
	access.Client = s.dbAccessClient(r)
	httpx.JSON(w, http.StatusOK, access)
	return nil
}

// dbAccessClient is the address a request came from as the guards see it:
// behind an SSH tunnel, the session's own rather than loopback.
func (s *Server) dbAccessClient(r *http.Request) string {
	if s.modules.network == nil {
		return httpx.ClientIP(r)
	}
	return s.networkClient(r)
}

func (s *Server) describeDBAccess(ctx context.Context, conn *dbConnection, dsn string) dbAccess {
	out := dbAccess{
		Exposure: exposureRemote, PublicAddresses: []string{}, Allow: []string{}, Addresses: []dbHostAddress{},
		Firewall: dbFirewall{Allowed: []string{}}, Gateway: dbGateway{AlsoAdmitted: []string{}},
	}
	info, err := dbx.ParseDSN(conn.Driver, dsn)
	if err != nil || conn.Driver == dbx.DriverSQLite {
		return out
	}
	out.Port, _ = strconv.Atoi(info.Port)
	if !databaseLoopback(info.Host) && !isContainerAddress(info.Host) {
		// A server somewhere else: nothing here binds it, and the sync would
		// not adopt it either.
		return out
	}
	server := s.serverBehind(ctx, conn, info)
	if server == nil && !databaseLoopback(info.Host) {
		// A private address with no container behind it is a server on the
		// LAN or a VPN, which is somebody else's to publish.
		return out
	}
	if out.Port > 0 {
		if addrs := proxysvc.PublicAddresses(); len(addrs) > 0 {
			out.PublicAddresses = addrs
		}
	}
	if server == nil {
		// Loopback with no container: a native server, or one this dashboard
		// cannot see. The sync adopts a native listener only where it needs no
		// password, so whether it would come back is answered by asking it.
		out.Exposure = exposureLocal
		out.Detected = s.hostListenerAt(ctx, out.Port)
		return out
	}
	out.Detected = true
	out.Container = server.container.Name
	out.ComposeProject = server.container.Labels["com.docker.compose.project"]
	out.Managed = out.ComposeProject == "" && server.binding != nil
	out.Exposure = exposureOf(server)
	if out.Exposure == exposurePrivate {
		out.Address = strings.Trim(server.binding.IP, "[]")
	}
	if out.Managed {
		out.Addresses = bindableAddresses(netsec.ReadHostNetwork(ctx))
		out.Gateway, out.Allow = s.describeDBGateway(ctx, server.container.Name)
		if out.Exposure == exposurePublic && len(out.Allow) > 0 {
			out.Exposure = exposureAllowlist
		}
	}
	if out.Exposure == exposurePublic || out.Managed {
		out.Firewall = s.describeDBFirewall(ctx, out.Port)
	}
	return out
}

// describeDBGateway reads what the gateway keeps a container's port to, and
// whether it can keep one at all.
func (s *Server) describeDBGateway(ctx context.Context, container string) (dbGateway, []string) {
	out := dbGateway{AlsoAdmitted: []string{}}
	keeper := s.portKeeper()
	if keeper == nil {
		out.Reason = "The network module is not running here, so nothing can refuse the addresses a list leaves out."
		return out, []string{}
	}
	reading, err := keeper.PortAllowlists(ctx)
	if err != nil {
		out.Reason = "The gateway table's configuration could not be read: " + err.Error()
		return out, []string{}
	}
	out.Available, out.Reason, out.AlsoAdmitted = reading.Available, reading.Reason, reading.AlsoAdmitted
	for _, e := range reading.Entries {
		if e.Owner == dbPortOwner(container) {
			out.Enforced, out.Refused = e.Enforced, e.Refused
			return out, e.Sources
		}
	}
	return out, []string{}
}

// exposureOf reads the binding the way dockerx.Exposure does, without needing
// a listing that has been enriched.
func exposureOf(server *dbServer) dbExposure {
	if server.binding == nil {
		// Reached at its container address: from this host and from other
		// containers on its network, which is "local" as far as anybody
		// outside the machine is concerned.
		return exposureLocal
	}
	switch server.binding.IP {
	case "", "0.0.0.0", "::", "[::]", "*":
		return exposurePublic
	}
	if ip := net.ParseIP(strings.Trim(server.binding.IP, "[]")); ip != nil && ip.IsLoopback() {
		return exposureLocal
	}
	return exposurePrivate
}

// isContainerAddress recognises a connection made straight to a container's
// own address on a bridge network, which is how a database with no published
// port is adopted.
func isContainerAddress(host string) bool {
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsPrivate()
}

// serverBehind finds the container a connection points at: the one that
// publishes the engine's port at the connection's address, or answers on it
// directly. Nil when there is none this process can see.
func (s *Server) serverBehind(ctx context.Context, conn *dbConnection, info *dbx.ConnInfo) *dbServer {
	if s.modules.docker == nil {
		return nil
	}
	port, err := strconv.Atoi(info.Port)
	if err != nil || port <= 0 {
		return nil
	}
	containers, err := s.modules.docker.ListContainers(ctx, false)
	if err != nil {
		return nil
	}
	for i := range containers {
		c := &containers[i]
		cand, _ := dbx.Detect(c.Name, c.Image, nil, publishedPorts(c.Ports), nil)
		if cand == nil || cand.Driver != conn.Driver {
			continue
		}
		// The engine's own port inside the container, from the same table
		// detection reads; a container reached at its own address answers
		// there with nothing published.
		internal, _ := dbx.Detect(c.Name, c.Image, nil, nil, []string{"container"})
		if internal == nil {
			continue
		}
		if databaseLoopback(info.Host) {
			for j := range c.Ports {
				p := &c.Ports[j]
				if int(p.PrivatePort) != internal.Port || int(p.PublicPort) != port {
					continue
				}
				if p.IP == "::" || (p.IP != "" && strings.Contains(p.IP, ":")) {
					// Docker lists the v6 binding beside the v4 one; the v4
					// one is the one the saved DSN dials.
					continue
				}
				return &dbServer{container: c, port: internal.Port, binding: p}
			}
			continue
		}
		if port != internal.Port {
			continue
		}
		detail, err := s.modules.docker.Inspect(ctx, c.ID)
		if err != nil {
			continue
		}
		for _, ip := range containerIPs(detail) {
			if ip == strings.Trim(info.Host, "[]") {
				return &dbServer{container: c, port: internal.Port}
			}
		}
	}
	return nil
}

// hostListenerAt reports a native server listening on the port, which the
// sync would find again.
func (s *Server) hostListenerAt(ctx context.Context, port int) bool {
	if port <= 0 {
		return false
	}
	for _, cand := range s.hostCandidates(ctx, nil) {
		if cand.Port == port && !cand.NeedsCredentials {
			return true
		}
	}
	return false
}

// dbFirewallComment marks the rule this surface writes, so closing access
// removes only what opening it added.
func dbFirewallComment(port int) string { return "just-dashboard database " + strconv.Itoa(port) }

func (s *Server) describeDBFirewall(ctx context.Context, port int) dbFirewall {
	out := dbFirewall{Allowed: []string{}}
	if s.modules.netsec == nil || port <= 0 {
		return out
	}
	st, err := s.modules.netsec.Status(ctx)
	if err != nil || st == nil || !st.Available {
		return out
	}
	out.Backend = string(st.Backend)
	out.Active = st.Enabled
	out.Editable = st.Capabilities.Editable
	out.Open, out.Allowed = firewallAdmits(st, port)
	return out
}

// firewallAdmits reads who the firewall's inbound allow rules for the port
// let in: anywhere, or the sources they name, each once.
func firewallAdmits(st *netsec.FirewallStatus, port int) (bool, []string) {
	open, sources := false, []string{}
	want := strconv.Itoa(port)
	for _, rule := range st.Rules {
		if !strings.EqualFold(rule.Action, "allow") || rule.Port != want ||
			(rule.Protocol != "" && !strings.EqualFold(rule.Protocol, "tcp")) ||
			(rule.Direction != "" && !strings.EqualFold(rule.Direction, "in")) {
			continue
		}
		switch {
		case isAnywhere(rule.From):
			open = true
		case !slices.ContainsFunc(sources, func(have string) bool { return sourceIs(rule.From, have) }):
			sources = append(sources, rule.From)
		}
	}
	return open, sources
}

// isAnywhere mirrors netsec's reading of a source that restricts nothing.
func isAnywhere(from string) bool {
	switch strings.ToLower(strings.TrimSpace(from)) {
	case "", "anywhere", "anywhere (v6)", "0.0.0.0/0", "::/0", "any":
		return true
	}
	return false
}

// dbAccessPlan is a change to where a server is reachable from, worked out
// against the container and the gateway as they are now. The preview and the
// change are both read from it, so what the dialog lists is what runs.
type dbAccessPlan struct {
	conn   *dbConnection
	dsn    string
	server *dbServer
	// port is the host port the server is published on, and owner the name
	// its list is kept under.
	port  int
	owner string
	from  dbReach
	to    dbReach
	// kept says the gateway holds a list for the port now.
	kept  bool
	steps dbAccessSteps
	// address is the host address a private binding takes, with the
	// interface holding it.
	address dbHostAddress
}

// planDBAccess reads the request and the server it names, and refuses what
// cannot be done before anything is touched.
func (s *Server) planDBAccess(ctx context.Context, r *http.Request) (*dbAccessPlan, error) {
	id, err := parseID(r)
	if err != nil {
		return nil, err
	}
	var req dbAccessRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return nil, err
	}
	switch req.Exposure {
	case exposureLocal, exposurePrivate, exposureAllowlist, exposurePublic:
	default:
		return nil, httpx.BadRequest("exposure must be local, private, allowlist or public")
	}
	conn, dsn, err := s.dbConnRow(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.modules.docker == nil {
		return nil, httpx.Err(http.StatusServiceUnavailable, "docker_unavailable", "this host has no Docker socket")
	}
	info, err := dbx.ParseDSN(conn.Driver, dsn)
	if err != nil {
		return nil, httpx.BadRequest("the saved connection could not be read")
	}
	server := s.serverBehind(ctx, conn, info)
	if server == nil {
		return nil, httpx.BadRequest("no container on this server publishes %s; change where it is reachable from on the machine that runs it", conn.Name)
	}
	if project := server.container.Labels["com.docker.compose.project"]; project != "" {
		return nil, httpx.Err(http.StatusConflict, "compose_managed",
			fmt.Sprintf("%s belongs to the compose project %s; change the port binding in its compose file and redeploy the stack", server.container.Name, project))
	}
	if server.binding == nil {
		return nil, httpx.BadRequest("%s publishes no port; publish one on the Docker page first", server.container.Name)
	}
	spec, err := s.modules.docker.SpecOf(ctx, server.container.ID)
	if err != nil {
		return nil, s.dockerErr(err)
	}
	hostIP, ok := configuredHostIP(spec.Ports, server)
	if !ok {
		return nil, httpx.BadRequest("the port binding on %s could not be found in its configuration", server.container.Name)
	}
	p := &dbAccessPlan{
		conn: conn, dsn: dsn, server: server, port: int(server.binding.PublicPort),
		owner: dbPortOwner(server.container.Name),
		from:  dbReach{Exposure: exposureOf(server), HostIP: hostIP},
		to:    dbReach{Exposure: req.Exposure},
	}
	keeper := s.portKeeper()
	if keeper != nil {
		kept, err := keeper.KeptPorts()
		if err != nil {
			return nil, mapNetworkError(err)
		}
		for _, e := range kept {
			if e.Owner == p.owner {
				p.kept, p.from.Allow = true, e.Sources
			}
		}
	}
	if p.kept && p.from.Exposure == exposurePublic {
		p.from.Exposure = exposureAllowlist
	}
	switch req.Exposure {
	case exposurePrivate:
		address, ok := bindableAddress(bindableAddresses(netsec.ReadHostNetwork(ctx)), req.Address)
		if !ok {
			return nil, httpx.BadRequest("%q is not one of this machine's tailnet, VPN or private addresses; choose one it has", req.Address)
		}
		p.address = address
	case exposureAllowlist:
		allow, err := netx.NormalizePortSources(req.Allow)
		if err != nil {
			return nil, httpx.BadRequest("%v", err)
		}
		if keeper == nil {
			return nil, httpx.Err(http.StatusConflict, "gateway_unavailable",
				"this host cannot keep a port to a list: the network module is not running")
		}
		p.to.Allow = allow
	}
	p.to.HostIP = reachBinding(req.Exposure, p.address.Address, p.to.Allow)
	p.steps = accessSteps(p.from, p.to, p.kept)
	if !p.steps.Rebind {
		// The binding stays as it is configured, which is what the end of
		// the change then says.
		p.to.HostIP = p.from.HostIP
	}
	return p, nil
}

// configuredHostIP is the host address the container's configuration
// publishes the engine's port on: what a recreate starts from, and what says
// whether one is needed. A port published once per family on every interface
// is every interface of both, which Docker spells as no address.
func configuredHostIP(ports []dockerx.PortMapping, server *dbServer) (string, bool) {
	var found []string
	for _, p := range ports {
		if publishesEngine(p, server) {
			found = append(found, p.HostIP)
		}
	}
	switch {
	case len(found) == 0:
		return "", false
	case len(found) > 1 && !slices.ContainsFunc(found, func(ip string) bool { return !everyInterface(ip) }):
		return "", true
	}
	return found[0], true
}

func publishesEngine(p dockerx.PortMapping, server *dbServer) bool {
	return p.ContainerPort == server.port && p.HostPort == int(server.binding.PublicPort) &&
		(p.Protocol == "" || p.Protocol == "tcp")
}

// rebindPorts is the configuration with the engine's port published on
// hostIP alone. Docker refuses one host port bound twice, so a binding Docker
// was given once per family is folded into the one.
func rebindPorts(ports []dockerx.PortMapping, server *dbServer, hostIP string) []dockerx.PortMapping {
	out := make([]dockerx.PortMapping, 0, len(ports))
	placed := false
	for _, p := range ports {
		if publishesEngine(p, server) {
			if placed {
				continue
			}
			p.HostIP, placed = hostIP, true
		}
		out = append(out, p)
	}
	return out
}

// dbAccessOutcome is what a change did, part by part.
type dbAccessOutcome struct {
	// Gateway is kept, released, restored, unchanged or failed.
	Gateway      string
	GatewayError string
	Recreated    bool
	NewID        string
	Warnings     []string
	// Firewall is reconcileDBFirewall's word.
	Firewall      string
	FirewallError string
	FirewallSteps []netsec.PlanStep
}

// applyDBAccess runs a plan in the order accessSteps chose. A list that
// cannot be kept stops the change before the binding is touched; a recreate
// that fails puts the gateway back to what the port had; a firewall or a
// gateway that cannot follow at the end is reported beside the change, which
// has happened.
func (s *Server) applyDBAccess(ctx context.Context, r *http.Request, p *dbAccessPlan) (dbAccessOutcome, error) {
	out := dbAccessOutcome{Gateway: "unchanged"}
	keeper := s.portKeeper()
	if p.steps.Keep {
		if _, err := keeper.SetPortAllowlist(ctx, s.dbPortRequest(p, p.to.Allow), actor(r)); err != nil {
			return out, mapNetworkError(err)
		}
		out.Gateway = "kept"
	}
	if p.steps.Rebind {
		res, err := s.rebindDBPort(ctx, p)
		if err != nil {
			if p.steps.Keep {
				out.Gateway, out.GatewayError = s.restoreDBGateway(ctx, r, p)
			}
			return out, err
		}
		out.Recreated, out.NewID, out.Warnings = true, res.ID, res.Warnings
		// The pool dialled the old container; the new one has a new socket.
		s.modules.dbs.Close(p.conn.ID)
		s.dbConns.stale(p.conn.ID)
	}
	known := append(append([]string{}, p.from.Allow...), p.to.Allow...)
	word, steps, err := s.reconcileDBFirewall(ctx, r, p.port, p.to, known)
	out.Firewall, out.FirewallSteps = word, steps
	if err != nil {
		out.FirewallError = err.Error()
	}
	if p.steps.Release {
		if _, err := keeper.RemovePortAllowlist(ctx, p.owner); err != nil {
			out.Gateway, out.GatewayError = "failed", err.Error()
		} else {
			out.Gateway = "released"
		}
	}
	return out, nil
}

func (s *Server) dbPortRequest(p *dbAccessPlan, allow []string) netx.PortAllowlistRequest {
	return netx.PortAllowlistRequest{
		Owner: p.owner, Name: p.server.container.Name, Protocol: "tcp", Port: p.port, Sources: allow,
	}
}

// rebindDBPort recreates the container with its port on the plan's host
// address: the same spec with one host address changed, through the
// park-and-restore path the Docker page uses, on the same named volume.
func (s *Server) rebindDBPort(ctx context.Context, p *dbAccessPlan) (*dockerx.CreateResult, error) {
	spec, err := s.modules.docker.SpecOf(ctx, p.server.container.ID)
	if err != nil {
		return nil, s.dockerErr(err)
	}
	spec.Ports = rebindPorts(spec.Ports, p.server, p.to.HostIP)
	res, err := s.modules.docker.Recreate(ctx, p.server.container.ID, dockerx.RecreateOptions{Spec: spec}, nil)
	if err != nil {
		if errors.Is(err, dockerx.ErrComposeManaged) {
			return nil, httpx.Err(http.StatusConflict, "compose_managed", err.Error())
		}
		return nil, s.dockerErr(err)
	}
	return res, nil
}

// restoreDBGateway puts back the list the port had before a change whose
// recreate failed: the old list, or none.
func (s *Server) restoreDBGateway(ctx context.Context, r *http.Request, p *dbAccessPlan) (string, string) {
	keeper := s.portKeeper()
	var err error
	if p.kept {
		_, err = keeper.SetPortAllowlist(ctx, s.dbPortRequest(p, p.from.Allow), actor(r))
	} else {
		_, err = keeper.RemovePortAllowlist(ctx, p.owner)
	}
	if err != nil {
		return "failed", err.Error()
	}
	return "restored", ""
}

// handleDBAccessUpdate changes where the server's port is published and who
// it is kept to.
//
// Docker cannot change a binding on a running container, so a new binding is
// a recreate. The engine restarts, which for Postgres is a few seconds of
// refused connections and no lost data. It sits in the destructive group for
// the reason the Docker page's Recreate does, and it is audited with what it
// opened. A list on a port already published on every interface is no
// recreate at all: the gateway starts refusing the addresses it leaves out,
// and sessions already open from them carry on until they close.
//
// The host firewall is then given the rules the new reach wants — one from
// anywhere, one per listed network, or none — and only the rules this route
// wrote are removed, found by their comment, so an operator's own rule for the
// same port is left alone.
func (s *Server) handleDBAccessUpdate(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 5*time.Minute)
	defer cancel()
	p, err := s.planDBAccess(ctx, r)
	if err != nil {
		return err
	}
	out, err := s.applyDBAccess(ctx, r, p)
	detail := map[string]any{
		"container": p.server.container.Name, "port": p.port,
		"from": p.from.Exposure, "to": p.to.Exposure,
		"binding": map[string]string{"from": p.from.HostIP, "to": p.to.HostIP},
		"gateway": out.Gateway,
	}
	if out.Firewall != "" {
		detail["firewall"] = out.Firewall
	}
	if p.kept {
		detail["allowBefore"] = p.from.Allow
	}
	if p.to.Exposure == exposureAllowlist {
		detail["allow"] = p.to.Allow
	}
	if p.to.Exposure == exposurePrivate {
		detail["address"], detail["interface"] = p.address.Address, p.address.Interface
	}
	if out.Recreated {
		detail["newId"], detail["warnings"] = out.NewID, out.Warnings
	}
	if len(out.FirewallSteps) > 0 {
		done := make([]string, 0, len(out.FirewallSteps))
		for _, step := range out.FirewallSteps {
			done = append(done, step.Outcome+": "+step.Description)
		}
		detail["firewallSteps"] = done
	}
	if out.GatewayError != "" {
		detail["gatewayError"] = out.GatewayError
	}
	if out.FirewallError != "" {
		detail["firewallError"] = out.FirewallError
	}
	if err != nil {
		detail["error"] = err.Error()
		httpx.SetAudit(r, "database.access.change", p.conn.Name, detail)
		return err
	}
	httpx.SetAudit(r, "database.access.change", p.conn.Name, detail)

	access := s.describeDBAccess(ctx, p.conn, p.dsn)
	access.Client = s.dbAccessClient(r)
	resp := map[string]any{"access": access, "firewall": out.Firewall, "gateway": out.Gateway}
	if out.FirewallError != "" {
		resp["firewallError"] = out.FirewallError
	}
	if out.GatewayError != "" {
		resp["gatewayError"] = out.GatewayError
	}
	httpx.JSON(w, http.StatusOK, resp)
	return nil
}

// dbAccessPreview is what a change would do, in the order it would do it.
type dbAccessPreview struct {
	From dbReach `json:"from"`
	To   dbReach `json:"to"`
	// Recreate says the container is replaced, which drops every session.
	Recreate bool           `json:"recreate"`
	Steps    []dbAccessStep `json:"steps"`
	// Firewall is the word the host firewall's part would end in, and
	// FirewallError what it would refuse.
	Firewall      string `json:"firewall"`
	FirewallError string `json:"firewallError,omitempty"`
}

// dbAccessStep is one thing a change does: the gateway, the recreate, or the
// host firewall, in a sentence, with the exact rules where there are some.
type dbAccessStep struct {
	Kind        string   `json:"kind"`
	Description string   `json:"description"`
	Lines       []string `json:"lines,omitempty"`
}

// handleDBAccessPreview says what a change would do without doing any of
// it: the rules the gateway would load, the recreate, and the host firewall's
// plan as netsec reviews it, so the confirmation names exactly what runs.
func (s *Server) handleDBAccessPreview(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	p, err := s.planDBAccess(ctx, r)
	if err != nil {
		return err
	}
	out := dbAccessPreview{From: p.from, To: p.to, Recreate: p.steps.Rebind, Steps: []dbAccessStep{}}
	if p.steps.Keep {
		rules, err := s.portKeeper().PortAllowlistRules(s.dbPortRequest(p, p.to.Allow))
		if err != nil {
			return mapNetworkError(err)
		}
		out.Steps = append(out.Steps, dbAccessStep{
			Kind:        "gateway",
			Description: fmt.Sprintf("The gateway table refuses new connections to port %d from every address not on the list.", p.port),
			Lines:       rules,
		})
	}
	if p.steps.Rebind {
		out.Steps = append(out.Steps, dbAccessStep{
			Kind:        "recreate",
			Description: fmt.Sprintf("%s is recreated with port %d published on %s.", p.server.container.Name, p.port, bindingWords(p.to.HostIP, p.address)),
		})
	}
	known := append(append([]string{}, p.from.Allow...), p.to.Allow...)
	change, err := s.dbFirewallChange(ctx, p.port, p.to, known)
	out.Firewall = change.word
	if err != nil {
		out.FirewallError = err.Error()
	}
	if len(change.ops) == 0 {
		out.Steps = append(out.Steps, dbAccessStep{Kind: "firewall", Description: change.note})
	}
	client := s.dbAccessClient(r)
	fctx := netsec.WithAccess(ctx, s.firewallAccess(r))
	for _, chunk := range planChunks(change.ops) {
		review, err := s.modules.netsec.PreviewPlan(fctx, netsec.FirewallPlan{Operations: chunk}, client)
		if err != nil {
			out.Firewall, out.FirewallError = "failed", err.Error()
			break
		}
		for _, step := range review.Steps {
			out.Steps = append(out.Steps, dbAccessStep{Kind: "firewall", Description: change.backend + ": " + step.Description})
		}
	}
	if p.steps.Release {
		out.Steps = append(out.Steps, dbAccessStep{
			Kind:        "gateway",
			Description: fmt.Sprintf("The gateway table stops refusing other addresses on port %d.", p.port),
		})
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// bindingWords is a host address the way a sentence says it.
func bindingWords(hostIP string, address dbHostAddress) string {
	switch {
	case hostIP == "":
		return "every interface, IPv4 and IPv6"
	case hostIP == "0.0.0.0":
		return "every IPv4 interface (0.0.0.0)"
	case loopbackAddress(hostIP):
		return hostIP + ", this server only"
	case address.Interface != "":
		return hostIP + " (" + address.Interface + ")"
	}
	return hostIP
}

// dbFirewallOutlook is what the host firewall would be told for a reach: the
// operations, or the reason there are none.
type dbFirewallOutlook struct {
	backend string
	ops     []netsec.PlanOperation
	// word is reconcileDBFirewall's, and note the sentence for a firewall
	// that is told nothing.
	word string
	note string
}

// dbFirewallChange reads the host firewall and works out what it would be
// told. The error is what an operator has to do themselves.
func (s *Server) dbFirewallChange(ctx context.Context, port int, to dbReach, known []string) (dbFirewallOutlook, error) {
	none := dbFirewallOutlook{word: "none", note: "No firewall is active on this machine: the binding and the gateway decide."}
	if s.modules.netsec == nil {
		return none, nil
	}
	st, err := s.modules.netsec.Status(ctx)
	if err != nil {
		return none, err
	}
	if st == nil || !st.Available {
		return none, nil
	}
	out := dbFirewallOutlook{backend: string(st.Backend)}
	want := dbFirewallWant(to, port)
	switch {
	case !st.Capabilities.Editable:
		out.word, out.note = "read-only", fmt.Sprintf("%s cannot be edited from here.", st.Backend)
		if len(want) == 0 {
			return out, nil
		}
		from := "from anywhere"
		if to.Exposure == exposureAllowlist {
			from = "from the listed addresses"
		}
		return out, fmt.Errorf("the firewall (%s) cannot be edited from here; let port %d in %s on it yourself", st.Backend, port, from)
	case !st.Enabled:
		out.word, out.note = "inactive", fmt.Sprintf("%s is installed and switched off, so no rule is written to it.", st.Backend)
		return out, nil
	}
	out.ops = dbFirewallPlan(st, port, want, known)
	out.word = dbFirewallWord(out.ops)
	if len(out.ops) == 0 {
		out.note = fmt.Sprintf("%s already holds the rules this needs for port %d.", st.Backend, port)
	}
	return out, nil
}

// reconcileDBFirewall gives the host firewall the rules a reach wants for the
// port, as plans netsec reviews, guards and takes back as a whole, inside the
// network journal ufw and firewalld changes run in. It says what it did in one
// word the response and the audit entry both carry:
//
//   - "opened", "closed", "updated": rules were added, removed, or both
//   - "already": nothing needed doing
//   - "none": no firewall on this host, so the binding alone decides
//   - "inactive": a firewall is installed but off; same consequence
//   - "read-only": present, on, and not writable from here
//   - "failed": a plan was refused or failed, and what it had done was taken back
func (s *Server) reconcileDBFirewall(ctx context.Context, r *http.Request, port int, to dbReach, known []string) (string, []netsec.PlanStep, error) {
	change, err := s.dbFirewallChange(ctx, port, to, known)
	if err != nil || len(change.ops) == 0 {
		return change.word, nil, err
	}
	client := s.dbAccessClient(r)
	fctx := netsec.WithAccess(ctx, s.firewallAccess(r))
	var steps []netsec.PlanStep
	for _, chunk := range planChunks(change.ops) {
		plan := netsec.FirewallPlan{Operations: chunk}
		var review *netsec.PlanReview
		_, err := s.protectFirewall(fctx, func(ctx context.Context) (string, error) {
			var err error
			review, err = s.modules.netsec.ApplyPlan(ctx, plan, client)
			return "", err
		})
		if review != nil {
			for _, step := range review.Steps {
				s.recordFirewall(r, netsec.RuleEvent{Operation: "plan." + step.Op, RuleID: step.RuleID,
					Outcome: firstNonEmptyString(step.Outcome, outcomeOf(err)), Detail: step.Description})
			}
			steps = append(steps, review.Steps...)
		}
		if err != nil {
			return "failed", steps, err
		}
	}
	return change.word, steps, nil
}

// setDBFirewall opens a newly provisioned server's port at the firewall, or
// closes it, the way the access route reconciles it.
func (s *Server) setDBFirewall(ctx context.Context, r *http.Request, port int, open bool) (string, error) {
	to := dbReach{Exposure: exposureLocal}
	if open {
		to.Exposure = exposurePublic
	}
	word, _, err := s.reconcileDBFirewall(ctx, r, port, to, nil)
	return word, err
}

// publicConnectionURL is the saved DSN with the server's public address in
// place of loopback: what somebody pastes on another machine.
func publicConnectionURL(driver dbx.Driver, dsn string, info *dbx.ConnInfo, address string) (string, error) {
	if driver == dbx.DriverSQLite {
		return "", fmt.Errorf("a SQLite database is a file on this server")
	}
	if address == "" {
		return "", fmt.Errorf("this server has no public address")
	}
	// applicationConnectionURL joins host and port itself, bracketing an
	// IPv6 literal on the way.
	return applicationConnectionURL(driver, dsn, info, address, info.Port)
}
