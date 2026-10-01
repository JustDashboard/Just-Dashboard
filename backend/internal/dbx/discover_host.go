package dbx

import (
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The servers installed on the machine itself, as servers rather than as
// sockets.
//
// DetectHost answers one socket at a time, and a server is not one socket. A
// ClickHouse holds five, a MySQL two, and anything bound to both loopback
// families two more — so "one candidate per socket" reported the same server
// several times over, most of them at a port its driver cannot speak. Here the
// sockets are grouped by the process that holds them: one instance, several
// endpoints, and one of those marked as the one to dial.
//
// A TCP socket is also not the only way a server says it is there. One that
// listens on a unix socket only has no entry in the TCP table at all, and one
// that is installed and stopped has no socket of any kind; the first is read
// from the kernel's unix socket table and the second from systemd.

// UnixSocket is one listening unix socket and the process holding it.
type UnixSocket struct {
	Path        string
	PID         int32
	Process     string
	Cmdline     string
	User        string
	Manager     string
	ManagerName string
}

// HostUnit is one systemd service unit, running or not.
type HostUnit struct {
	Name        string
	LoadState   string
	ActiveState string
	SubState    string
	Enabled     bool
	// Port, DataDir and ConfigFile are what the unit's own configuration says
	// about a server that is not running to be asked: a Debian PostgreSQL
	// cluster's postgresql.conf.
	Port       int
	DataDir    string
	ConfigFile string
}

// debianClusterTemplate is the unit file every Debian PostgreSQL cluster is an
// instance of.
const debianClusterTemplate = "postgresql@.service"

// InstalledUnits is the database servers systemd has a unit file for and has
// not loaded, as the stopped units they are.
//
// systemd unloads a unit that is neither running nor enabled, so the server
// somebody stopped and disabled — exactly the installed, idle database an
// inventory exists to show — is in no listing of units. Its unit file still
// is. files maps every installed unit file to its state; loaded is what the
// unit listing already returned; clusters are the Debian PostgreSQL clusters
// configured on the machine, as "<version>-<name>", which are instances of a
// template and have no file of their own.
//
// An alias is another name for a unit that is listed under its own, and a
// masked unit cannot be started: neither is a second server.
func InstalledUnits(files map[string]string, loaded []HostUnit, clusters []string) []HostUnit {
	known := map[string]bool{}
	for _, u := range loaded {
		known[u.Name] = true
	}
	enabled := func(state string) bool {
		return state == "enabled" || state == "enabled-runtime" || state == "static"
	}
	out := []HostUnit{}
	add := func(name string, on bool) {
		if known[name] || productForUnit(name) == nil {
			return
		}
		known[name] = true
		out = append(out, HostUnit{Name: name, LoadState: "loaded", ActiveState: "inactive", SubState: "dead", Enabled: on})
	}
	for name, state := range files {
		switch state {
		case "alias", "masked", "masked-runtime", "transient", "generated", "bad":
			continue
		}
		if !strings.HasSuffix(name, ".service") || strings.Contains(name, "@.") {
			// A template is not a unit until it is given an instance.
			continue
		}
		add(name, enabled(state))
	}
	if _, ok := files[debianClusterTemplate]; ok {
		for _, cluster := range clusters {
			name := "postgresql@" + cluster + ".service"
			if _, _, ok := DebianCluster(name); ok {
				// The umbrella unit is what starts the clusters at boot.
				add(name, enabled(files["postgresql.service"]))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

var (
	pgSocketRe    = regexp.MustCompile(`^\.s\.PGSQL\.([0-9]+)$`)
	mongoSocketRe = regexp.MustCompile(`^mongodb-([0-9]+)\.sock$`)
)

// SocketEngine names the product a unix socket belongs to from its file name,
// and the port number in that name where the engine puts one there. Every
// engine here names its socket by a fixed convention; that convention is what
// distinguishes a database's socket from the hundreds of others a machine has.
func SocketEngine(socketPath string) (id string, port int, ok bool) {
	base := path.Base(socketPath)
	if m := pgSocketRe.FindStringSubmatch(base); m != nil {
		port, _ = strconv.Atoi(m[1])
		return "postgres", port, true
	}
	if m := mongoSocketRe.FindStringSubmatch(base); m != nil {
		port, _ = strconv.Atoi(m[1])
		return "mongodb", port, true
	}
	switch base {
	case "mysqld.sock", "mysql.sock":
		return "mysql", 0, true
	case "mariadb.sock":
		return "mariadb", 0, true
	case "memcached.sock":
		return "memcached", 0, true
	}
	if !strings.HasSuffix(base, ".sock") {
		return "", 0, false
	}
	for _, name := range []string{"redis", "valkey", "keydb"} {
		if strings.HasPrefix(base, name) && !strings.Contains(base, "sentinel") {
			return name, 0, true
		}
	}
	return "", 0, false
}

// group is one server process and every socket it holds.
type hostGroup struct {
	product   *product
	first     HostListener
	listeners []HostListener
}

// discoverHost adds the servers on the machine itself: by the sockets their
// processes hold, by their unix sockets, and by the units systemd has for them.
func discoverHost(listeners []HostListener, sockets []UnixSocket, units []HostUnit, facts []ContainerFacts, inv *Inventory) {
	containers := map[string]*ContainerFacts{}
	for i := range facts {
		if len(facts[i].ID) >= 12 {
			containers[facts[i].ID[:12]] = &facts[i]
		}
	}
	groups := map[int32]*hostGroup{}
	order := []int32{}
	// ownerless are the sockets whose holder could not be read: the kernel
	// says something is listening, and not what. A dashboard that is not
	// root sees every other account's server this way.
	ownerless := map[int][]HostListener{}
	for _, l := range listeners {
		if l.Protocol != "" && !strings.EqualFold(l.Protocol, "tcp") {
			continue
		}
		if l.Port > 0 && l.PID <= 0 && l.Process == "" {
			ownerless[l.Port] = append(ownerless[l.Port], l)
		}
		if l.Port <= 0 || l.PID <= 0 {
			continue
		}
		g := groups[l.PID]
		if g == nil {
			p := productForProcess(l.Process, l.Cmdline)
			if p == nil {
				continue
			}
			g = &hostGroup{product: p, first: l}
			groups[l.PID] = g
			order = append(order, l.PID)
		}
		g.listeners = append(g.listeners, l)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })

	byPID := map[int32]int{}
	for _, pid := range order {
		g := groups[pid]
		endpoints := groupEndpoints(g.product, g.listeners)
		if g.first.Manager == "container" {
			attachToContainer(g, endpoints, containers, inv)
			continue
		}
		inst := hostInstance(g.product, g.first.Process, g.first.Cmdline, g.first.User, g.first.PID,
			g.first.Manager, g.first.ManagerName)
		inst.Endpoints = endpoints
		inst.Confidence = ConfidenceProcess
		inst.Evidence = []string{"a " + g.first.Process + " process is listening"}
		if primary := primaryEndpoint(&inst); primary != nil {
			if inst.Key == "" {
				inst.Key = "host:" + g.product.engine + ":" + strconv.Itoa(primary.Port)
			}
			if inst.Name == "" {
				inst.Name = g.product.label + " on port " + strconv.Itoa(primary.Port)
			}
		} else if len(endpoints) > 0 {
			if inst.Key == "" {
				inst.Key = "host:" + g.product.engine + ":" + strconv.Itoa(endpoints[0].Port)
			}
			if inst.Name == "" {
				inst.Name = g.product.label + " on port " + strconv.Itoa(endpoints[0].Port)
			}
		}
		inv.Instances = append(inv.Instances, inst)
		byPID[pid] = len(inv.Instances) - 1
	}

	for _, s := range sockets {
		id, port, ok := SocketEngine(s.Path)
		if !ok || s.Manager == "container" {
			// A socket inside a container is a path in that container's own
			// filesystem; the container half already describes the server.
			continue
		}
		p := productByID[id]
		if named := productForProcess(s.Process, s.Cmdline); named != nil && named.engine == p.engine {
			// The process knows better than the file name which product it
			// is: a MariaDB keeps its socket at mysqld.sock.
			p = named
		}
		endpoint := Endpoint{Kind: "unix", Path: s.Path}
		if i, found := byPID[s.PID]; found && s.PID > 0 {
			inv.Instances[i].Endpoints = append(inv.Instances[i].Endpoints, endpoint)
			continue
		}
		if i, found := hostInstanceAt(inv, p.engine, port); found && port > 0 {
			inv.Instances[i].Endpoints = append(inv.Instances[i].Endpoints, endpoint)
			continue
		}
		inst := hostInstance(p, s.Process, s.Cmdline, s.User, s.PID, s.Manager, s.ManagerName)
		inst.Endpoints = []Endpoint{endpoint}
		inst.Confidence = ConfidenceSocket
		inst.Evidence = []string{"a unix socket named for " + p.label + " is listening at " + s.Path}
		inst.Reason = "it listens on a unix socket only (" + s.Path + ") — nothing here can dial it over TCP"
		if inst.Key == "" {
			inst.Key = "host:" + p.engine + ":" + s.Path
		}
		if inst.Name == "" {
			inst.Name = p.label + " at " + s.Path
		}
		inv.Instances = append(inv.Instances, inst)
		if s.PID > 0 {
			byPID[s.PID] = len(inv.Instances) - 1
		}
	}

	byUnit := map[string]int{}
	for i := range inv.Instances {
		if h := inv.Instances[i].Host; h != nil && h.Unit != "" {
			byUnit[h.Unit] = i
		}
	}
	// Debian's postgresql.service starts nothing itself — each cluster is a
	// postgresql@ instance — so it is left out beside them.
	clusters := false
	for _, u := range units {
		clusters = clusters || strings.HasPrefix(u.Name, "postgresql@")
	}
	for _, u := range units {
		p := productForUnit(u.Name)
		if p == nil || u.LoadState != "loaded" {
			continue
		}
		if clusters && u.Name == "postgresql.service" {
			continue
		}
		if i, ok := byUnit[u.Name]; ok {
			describeUnit(&inv.Instances[i], u)
			continue
		}
		if u.ActiveState == "active" && u.SubState == "exited" {
			// A unit that ran once and finished is not a server.
			continue
		}
		if i, ok := unitlessInstance(inv, p, u, units); ok {
			// The server was found by a socket whose owner could not be read,
			// so nothing said which unit it belongs to. Its port does.
			inst := &inv.Instances[i]
			inst.Key, inst.Name = "host:"+u.Name, strings.TrimSuffix(u.Name, ".service")
			if version, cluster, ok := DebianCluster(u.Name); ok {
				inst.Version, inst.Host.Cluster = version, version+"/"+cluster
			}
			describeUnit(inst, u)
			claimConfiguredPort(inst, p, u, ownerless)
			byUnit[u.Name] = i
			continue
		}
		inst := hostInstance(p, "", "", "", 0, "systemd", u.Name)
		describeUnit(&inst, u)
		inst.Confidence = ConfidenceUnit
		inst.Evidence = []string{"systemd has the unit " + u.Name}
		switch u.ActiveState {
		case "active":
			inst.State = StateRunning
			inst.Reason = u.Name + " is active, and nothing it listens on was found"
		case "failed":
			inst.State = StateFailed
			inst.Reason = u.Name + " has failed — read its log, then start it to connect"
		case "activating", "reloading":
			inst.State = u.ActiveState
			inst.Reason = u.Name + " is " + u.ActiveState + " — it is not answering yet"
		default:
			inst.State = StateInactive
			inst.Reason = u.Name + " is not running — start it to connect"
		}
		if u.Port > 0 {
			// Where it will listen once started, from its own configuration.
			inst.Endpoints = []Endpoint{{Kind: "tcp", Host: "127.0.0.1", Port: u.Port}}
		}
		claimConfiguredPort(&inst, p, u, ownerless)
		inv.Instances = append(inv.Instances, inst)
		byUnit[u.Name] = len(inv.Instances) - 1
	}

	// Two servers of one engine bound to the same port on different addresses
	// would otherwise share a key, and a key names exactly one instance. Only
	// the host's own servers can collide here — a container's key was settled
	// when it was listed — and how they are signed in to is recorded below,
	// under the key each ends up with.
	taken := map[string]int{}
	for i := range inv.Instances {
		key := inv.Instances[i].Key
		if taken[key]++; taken[key] > 1 && inv.Instances[i].Source == SourceHost {
			inv.Instances[i].Key = key + "#" + strconv.Itoa(taken[key])
		}
	}
	for i := range inv.Instances {
		inst := &inv.Instances[i]
		if inst.Source != SourceHost || inst.Host == nil {
			continue
		}
		settleHostCredentials(inst)
		if primary := primaryEndpoint(inst); primary != nil && inst.Driver != "" {
			inv.access[inst.Key] = Access{Candidate: Candidate{
				Driver: inst.Driver, Source: SourceHost, Process: inst.Host.Process,
				Host: primary.Host, Port: primary.Port, User: inst.User, Database: inst.Database,
				NeedsCredentials: inst.Credentials != CredentialsOpen,
			}}
		}
	}
}

// claimConfiguredPort gives an active unit the socket on the port its own
// configuration names, where the kernel reports one listening and could not
// say whose it is. The unit is running and says it listens there; the socket
// is there. Nothing is dialled that the kernel did not report.
func claimConfiguredPort(inst *Instance, p *product, u HostUnit, ownerless map[int][]HostListener) {
	if u.Port <= 0 || u.ActiveState != "active" || len(ownerless[u.Port]) == 0 {
		return
	}
	kept := inst.Endpoints[:0:0]
	for _, e := range inst.Endpoints {
		if e.Kind == "tcp" && e.Port == u.Port {
			if e.Primary {
				// Already found listening there by its own process.
				return
			}
			continue
		}
		kept = append(kept, e)
	}
	inst.Endpoints = append(groupEndpoints(p, ownerless[u.Port]), kept...)
	inst.State = StateRunning
	inst.Reason = ""
	inst.Evidence = append(inst.Evidence,
		"its configuration names port "+strconv.Itoa(u.Port)+", and something is listening there")
}

// unitlessInstance finds a running server of the unit's engine that no unit
// has claimed: the one listening on the port the unit is configured for, or —
// where the unit states no port — the only one there is, when this is the only
// active unit that could be running it.
func unitlessInstance(inv *Inventory, p *product, u HostUnit, units []HostUnit) (int, bool) {
	if u.ActiveState != "active" {
		return 0, false
	}
	candidates := []int{}
	for i := range inv.Instances {
		inst := &inv.Instances[i]
		if inst.Source != SourceHost || inst.Host == nil || inst.Host.Unit != "" || inst.Engine != p.engine {
			continue
		}
		if u.Port > 0 {
			for _, e := range inst.Endpoints {
				_, socketPort, _ := SocketEngine(e.Path)
				if (e.Kind == "tcp" && e.Port == u.Port) || (e.Kind == "unix" && socketPort == u.Port) {
					return i, true
				}
			}
			continue
		}
		candidates = append(candidates, i)
	}
	if len(candidates) != 1 {
		return 0, false
	}
	active := 0
	for _, other := range units {
		if q := productForUnit(other.Name); q != nil && q.engine == p.engine && other.ActiveState == "active" && other.SubState != "exited" {
			active++
		}
	}
	return candidates[0], active == 1
}

// hostInstance starts the description of a server on the machine itself. The
// key is the unit where systemd manages it — a unit is the same server before
// and after it changes port — and is left for the caller to derive from the
// address otherwise.
func hostInstance(p *product, process, cmdline, user string, pid int32, manager, managerName string) Instance {
	inst := Instance{
		Kind: KindServer, Source: SourceHost, State: StateRunning,
		User: p.user, Database: p.database,
		Host: &HostRef{PID: pid, Process: process, User: user},
	}
	describe(&inst, p)
	if manager == "systemd" && strings.HasSuffix(managerName, ".service") {
		inst.Host.Unit = managerName
		inst.Key = "host:" + managerName
		inst.Name = strings.TrimSuffix(managerName, ".service")
		if version, cluster, ok := DebianCluster(managerName); ok {
			inst.Version, inst.Host.Cluster = version, version+"/"+cluster
		}
	}
	inst.Host.DataDir, inst.Host.ConfigFile = processPaths(p, cmdline)
	if inst.Version == "" {
		inst.Version = processVersion(p, cmdline)
	}
	if p.engine == "mongodb" && mongoAccessControl(strings.Fields(cmdline)) != "" {
		// The one case a command line settles: a mongod told to check who is
		// asking is not the open server the product ships as.
		inst.Credentials = CredentialsNeeded
	}
	return inst
}

// describeUnit records what systemd says about a server's unit.
func describeUnit(inst *Instance, u HostUnit) {
	inst.Host.Unit, inst.Host.UnitState, inst.Host.Enabled = u.Name, u.ActiveState, u.Enabled
	if inst.Host.DataDir == "" {
		inst.Host.DataDir = u.DataDir
	}
	if inst.Host.ConfigFile == "" {
		inst.Host.ConfigFile = u.ConfigFile
	}
}

// settleHostCredentials says how a server on the host is signed in to. Nothing
// here can read its password, but two engines admit their own system account
// over the unix socket, and that is a way in the operator can be offered.
func settleHostCredentials(inst *Instance) {
	p := productByID[inst.Flavor]
	switch {
	case inst.Driver == "":
		inst.Credentials = CredentialsUnknown
	case inst.Credentials != "":
		// Its own command line already said.
	case p != nil && p.open:
		inst.Credentials = CredentialsOpen
	case inst.Engine == "postgres" || inst.Engine == "mysql":
		inst.Credentials = CredentialsNeeded
		for _, e := range inst.Endpoints {
			if e.Kind == "unix" {
				inst.Credentials = CredentialsPeer
			}
		}
	default:
		inst.Credentials = CredentialsNeeded
	}
}

var clusterSegmentRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// DebianCluster reads the version and cluster name out of a Debian PostgreSQL
// unit, "postgresql@17-main.service". Both segments are checked against what
// pg_createcluster allows, because the caller builds a path from them.
func DebianCluster(unit string) (version, cluster string, ok bool) {
	rest, ok := strings.CutPrefix(strings.TrimSuffix(unit, ".service"), "postgresql@")
	if !ok {
		return "", "", false
	}
	version, cluster, ok = strings.Cut(rest, "-")
	if !ok || !clusterSegmentRe.MatchString(version) || !clusterSegmentRe.MatchString(cluster) ||
		strings.Contains(version, "..") || strings.Contains(cluster, "..") {
		return "", "", false
	}
	return version, cluster, true
}

// processPaths reads where a server keeps its data and its configuration out
// of its command line, where the engine is started with them stated.
func processPaths(p *product, cmdline string) (dataDir, configFile string) {
	argv := strings.Fields(cmdline)
	switch p.engine {
	case "postgres":
		dataDir = argValue(argv, "-D")
		configFile = settingValue(argv, "config_file")
	case "mysql":
		dataDir = argValue(argv, "--datadir")
		configFile = argValue(argv, "--defaults-file")
	case "mongodb":
		dataDir = argValue(argv, "--dbpath")
		configFile = firstNonEmpty(argValue(argv, "--config"), argValue(argv, "-f"))
	case "clickhouse":
		configFile = firstNonEmpty(argValue(argv, "--config-file"), argValue(argv, "--config"))
	}
	return dataDir, configFile
}

var pgBinaryRe = regexp.MustCompile(`/postgresql/([0-9]+(?:\.[0-9]+)?)/bin/`)

// processVersion reads a version out of the path a server was started from,
// which Debian's PostgreSQL packages put the major version in.
func processVersion(p *product, cmdline string) string {
	if p.engine != "postgres" {
		return ""
	}
	if m := pgBinaryRe.FindStringSubmatch(cmdline); m != nil {
		return m[1]
	}
	return ""
}

// groupEndpoints turns the sockets one process holds into the addresses it
// answers at, each once.
//
// A server bound to 127.0.0.1 and ::1 is two sockets and one way in, as is one
// bound to 0.0.0.0 and ::. They collapse; what is kept is the widest binding,
// because how far the server can be reached is the thing worth reporting.
func groupEndpoints(p *product, listeners []HostListener) []Endpoint {
	type merged struct {
		endpoint Endpoint
		v4       bool
	}
	byID := map[string]*merged{}
	order := []string{}
	width := map[string]int{ScopeLoopback: 0, ScopePrivate: 1, ScopePublic: 2}
	for _, l := range listeners {
		host := hostAddress(l.Address)
		id := AddressIdentity(host, l.Port)
		scope := bindScope(l.Address)
		v4 := !strings.Contains(l.Address, ":")
		m := byID[id]
		if m == nil {
			m = &merged{endpoint: Endpoint{Kind: "tcp", Host: host, Port: l.Port, Scope: scope}, v4: v4}
			if host != l.Address && l.Address != "" {
				m.endpoint.Bind = l.Address
			}
			byID[id] = m
			order = append(order, id)
			continue
		}
		if width[scope] > width[m.endpoint.Scope] {
			m.endpoint.Scope, m.endpoint.Bind = scope, l.Address
		}
		if v4 && !m.v4 {
			// An IPv4 socket is there to dial; prefer it to the bracketed
			// IPv6 loopback the first socket gave.
			m.endpoint.Host, m.v4 = host, true
		}
	}
	out := make([]Endpoint, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id].endpoint)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	choosePrimary(p, out)
	return out
}

// choosePrimary marks the endpoint a connection is made to: the product's own
// port where it listens there, and otherwise the lowest port that is not one
// of its side doors.
func choosePrimary(p *product, endpoints []Endpoint) {
	pick := -1
	for i, e := range endpoints {
		if e.Kind != "tcp" || p.sidePort(e.Port) {
			continue
		}
		if e.Port == p.port {
			pick = i
			break
		}
		if pick < 0 {
			pick = i
		}
	}
	if pick >= 0 {
		endpoints[pick].Primary = true
	}
}

// hostInstanceAt finds the host server of an engine listening on a port.
func hostInstanceAt(inv *Inventory, engine string, port int) (int, bool) {
	for i := range inv.Instances {
		inst := &inv.Instances[i]
		if inst.Source != SourceHost || inst.Engine != engine {
			continue
		}
		for _, e := range inst.Endpoints {
			if e.Kind == "tcp" && e.Port == port {
				return i, true
			}
		}
	}
	return 0, false
}

// attachToContainer gives a server process found in a container to that
// container's instance.
//
// A container on the host's own network has no published port and no address
// of its own, so Docker has nothing to say about where it listens; the socket
// table does. Without this the same server was reported twice — unreachable
// as a container, and wanting a password as a native server whose host-side
// tools cannot reach it.
func attachToContainer(g *hostGroup, endpoints []Endpoint, containers map[string]*ContainerFacts, inv *Inventory) {
	c := containers[g.first.ManagerName]
	if c == nil {
		// Docker did not list it — the daemon is unreachable, or this is
		// another runtime's container. It is still a server that is listening.
		inst := hostInstance(g.product, g.first.Process, g.first.Cmdline, g.first.User, g.first.PID, "", "")
		inst.Endpoints = endpoints
		inst.Confidence = ConfidenceProcess
		inst.Evidence = []string{"a " + g.first.Process + " process in container " + g.first.ManagerName + " is listening on the host's network"}
		if len(endpoints) > 0 {
			inst.Key = "host:" + g.product.engine + ":" + strconv.Itoa(endpoints[0].Port)
			inst.Name = g.product.label + " on port " + strconv.Itoa(endpoints[0].Port)
			if primary := primaryEndpoint(&inst); primary != nil {
				inst.Key = "host:" + g.product.engine + ":" + strconv.Itoa(primary.Port)
				inst.Name = g.product.label + " on port " + strconv.Itoa(primary.Port)
			}
			inv.Instances = append(inv.Instances, inst)
		}
		return
	}
	inst, found := inv.FindContainer(c.Name)
	if !found {
		// Nothing about the container said what it was; the process does.
		index := addContainerInstance(c, containerMatch{
			product: g.product, style: styleFor(g.product.id, c.Env), confidence: ConfidenceProcess,
			evidence: "a " + g.first.Process + " process in it is listening on the host's network",
		}, map[string]*ContainerFacts{}, inv)
		inst = &inv.Instances[index]
	}
	if len(inst.Endpoints) > 0 || len(endpoints) == 0 {
		return
	}
	inst.Endpoints = endpoints
	inst.Evidence = append(inst.Evidence, "it was found listening on the host's network as "+g.first.Process)
	inst.Reason = ""
	if primary := primaryEndpoint(inst); primary != nil {
		if access, ok := inv.access[inst.Key]; ok {
			access.Candidate.Host, access.Candidate.Port = primary.Host, primary.Port
			access.Candidate.Reason = ""
			inv.access[inst.Key] = access
		}
	}
}
