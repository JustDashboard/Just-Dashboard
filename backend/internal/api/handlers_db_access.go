package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
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

// Exposure is where a connection's server can be reached from, as far as its
// port binding says.
type dbExposure string

const (
	// exposureLocal is bound to loopback: this server, and nothing else.
	exposureLocal dbExposure = "local"
	// exposurePublic is bound to every interface the host has.
	exposurePublic dbExposure = "public"
	// exposurePrivate is bound to one specific address — a LAN or VPN
	// interface. Changed on the Docker page, where the address is chosen.
	exposurePrivate dbExposure = "private"
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
}

// dbFirewall is the firewall's part of the answer.
type dbFirewall struct {
	Backend string `json:"backend,omitempty"`
	// Active means a firewall is present and switched on, so a port bound to
	// every interface is still closed until a rule opens it.
	Active bool `json:"active"`
	// Open reports an allow rule for the port from anywhere.
	Open bool `json:"open"`
	// Editable reports whether a rule can be written from here.
	Editable bool `json:"editable"`
}

// dbAccessRequest is the one change this surface accepts.
type dbAccessRequest struct {
	Exposure dbExposure `json:"exposure"`
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
	httpx.JSON(w, http.StatusOK, s.describeDBAccess(ctx, conn, dsn))
	return nil
}

func (s *Server) describeDBAccess(ctx context.Context, conn *dbConnection, dsn string) dbAccess {
	out := dbAccess{Exposure: exposureRemote, PublicAddresses: []string{}}
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
	if out.Exposure == exposurePublic || out.Managed {
		out.Firewall = s.describeDBFirewall(ctx, out.Port)
	}
	return out
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
	out := dbFirewall{}
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
	want := strconv.Itoa(port)
	for _, rule := range st.Rules {
		if strings.EqualFold(rule.Action, "allow") && rule.Port == want && isAnywhere(rule.From) &&
			(rule.Protocol == "" || strings.EqualFold(rule.Protocol, "tcp")) {
			out.Open = true
			break
		}
	}
	return out
}

// isAnywhere mirrors netsec's reading of a source that restricts nothing.
func isAnywhere(from string) bool {
	switch strings.ToLower(strings.TrimSpace(from)) {
	case "", "anywhere", "anywhere (v6)", "0.0.0.0/0", "::/0", "any":
		return true
	}
	return false
}

// handleDBAccessUpdate publishes the server's port to every interface, or
// takes it back to loopback.
//
// Docker cannot change a binding on a running container, so this is a
// recreate: the same spec with one host address changed, through the same
// park-and-restore path the Docker page uses, on the same named volume. The
// engine restarts, which for Postgres is a few seconds of refused connections
// and no lost data. It sits in the destructive group for the reason the
// Docker page's Recreate does, and it is audited with what it opened.
//
// Opening also writes the firewall rule where there is a firewall to write to,
// and closing removes the rule this route wrote — only that one, found by its
// comment, so an operator's own rule for the same port is left alone.
func (s *Server) handleDBAccessUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req dbAccessRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Exposure != exposureLocal && req.Exposure != exposurePublic {
		return httpx.BadRequest("exposure must be local or public")
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	if s.modules.docker == nil {
		return httpx.Err(http.StatusServiceUnavailable, "docker_unavailable", "this host has no Docker socket")
	}
	info, err := dbx.ParseDSN(conn.Driver, dsn)
	if err != nil {
		return httpx.BadRequest("the saved connection could not be read")
	}
	ctx, cancel := timeoutCtx(r, 5*time.Minute)
	defer cancel()

	server := s.serverBehind(ctx, conn, info)
	if server == nil {
		return httpx.BadRequest("no container on this server publishes %s; change where it is reachable from on the machine that runs it", conn.Name)
	}
	if project := server.container.Labels["com.docker.compose.project"]; project != "" {
		return httpx.Err(http.StatusConflict, "compose_managed",
			fmt.Sprintf("%s belongs to the compose project %s; change the port binding in its compose file and redeploy the stack", server.container.Name, project))
	}
	if server.binding == nil {
		return httpx.BadRequest("%s publishes no port; publish one on the Docker page first", server.container.Name)
	}
	hostIP := "127.0.0.1"
	if req.Exposure == exposurePublic {
		hostIP = "0.0.0.0"
	}
	current := exposureOf(server)
	detail := map[string]any{
		"container": server.container.Name, "port": server.binding.PublicPort,
		"from": current, "to": req.Exposure,
	}

	if current != req.Exposure {
		spec, err := s.modules.docker.SpecOf(ctx, server.container.ID)
		if err != nil {
			return s.dockerErr(err)
		}
		changed := false
		for i := range spec.Ports {
			p := &spec.Ports[i]
			if p.ContainerPort == server.port && p.HostPort == int(server.binding.PublicPort) {
				p.HostIP = hostIP
				changed = true
			}
		}
		if !changed {
			return httpx.BadRequest("the port binding on %s could not be found in its configuration", server.container.Name)
		}
		res, err := s.modules.docker.Recreate(ctx, server.container.ID, dockerx.RecreateOptions{Spec: spec}, nil)
		if err != nil {
			if errors.Is(err, dockerx.ErrComposeManaged) {
				return httpx.Err(http.StatusConflict, "compose_managed", err.Error())
			}
			return s.dockerErr(err)
		}
		detail["newId"] = res.ID
		detail["warnings"] = res.Warnings
		// The pool dialled the old container; the new one has a new socket.
		s.modules.dbs.Close(id)
	}

	firewall, ferr := s.setDBFirewall(ctx, r, int(server.binding.PublicPort), req.Exposure == exposurePublic)
	detail["firewall"] = firewall
	if ferr != nil {
		detail["firewallError"] = ferr.Error()
	}
	httpx.SetAudit(r, "database.access.change", conn.Name, detail)

	access := s.describeDBAccess(ctx, conn, dsn)
	resp := map[string]any{"access": access, "firewall": firewall}
	if ferr != nil {
		resp["firewallError"] = ferr.Error()
	}
	httpx.JSON(w, http.StatusOK, resp)
	return nil
}

// setDBFirewall opens or closes the port at the firewall and says what it
// did, in one word the response and the audit entry both carry.
//
//   - "opened" / "closed": a rule was written or removed
//   - "already": nothing needed doing
//   - "none": no firewall on this host, so the binding alone decides
//   - "inactive": a firewall is installed but off; same consequence
//   - "read-only": present, on, and not writable from here
func (s *Server) setDBFirewall(ctx context.Context, r *http.Request, port int, open bool) (string, error) {
	if s.modules.netsec == nil {
		return "none", nil
	}
	st, err := s.modules.netsec.Status(ctx)
	if err != nil {
		return "none", err
	}
	if st == nil || !st.Available {
		return "none", nil
	}
	if !st.Capabilities.Editable {
		if !open {
			return "read-only", nil
		}
		return "read-only", fmt.Errorf("the firewall (%s) cannot be edited from here; open port %d on it yourself", st.Backend, port)
	}
	want := strconv.Itoa(port)
	comment := dbFirewallComment(port)
	if open {
		if !st.Enabled {
			return "inactive", nil
		}
		for _, rule := range st.Rules {
			if strings.EqualFold(rule.Action, "allow") && rule.Port == want && isAnywhere(rule.From) {
				return "already", nil
			}
		}
		_, err := s.modules.netsec.AddRule(ctx, netsec.RuleRequest{
			Action: "allow", Direction: "in", Port: want, Protocol: "tcp", Comment: comment,
		}, httpx.ClientIP(r))
		if err != nil {
			return "failed", err
		}
		return "opened", nil
	}
	// Ours only, highest number first so deleting one does not renumber the
	// next. ufw lists the v6 twin of every rule; both carry the comment.
	removed := 0
	for i := len(st.Rules) - 1; i >= 0; i-- {
		rule := st.Rules[i]
		if rule.Port != want || !strings.EqualFold(rule.Action, "allow") || rule.Comment != comment {
			continue
		}
		if _, err := s.modules.netsec.DeleteRule(ctx, rule.Number); err != nil {
			return "failed", err
		}
		removed++
	}
	if removed == 0 {
		return "already", nil
	}
	return "closed", nil
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
