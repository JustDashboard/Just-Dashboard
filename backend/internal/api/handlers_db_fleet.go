package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// Every database at once, and what is talking to each.
//
// The section opened on the first connection's first table, which is the
// wrong first thing to see on a server with nine databases: the operator
// arrives to find out whether they are all up, which one grew, and what
// would break if one went down. `GET /databases/fleet` answers the first two
// in one request — each connection dialled, sized and counted concurrently,
// with the servers found running here but not yet connected beside them —
// and `GET /databases/topology` answers the third by joining four sources
// that each know part of the picture:
//
//   - the deployment bindings, which say which project's environment was
//     handed which database and whether the link still works;
//   - the containers, whose environment names the address, the container or
//     the `db-N.jd.internal` alias they were given;
//   - the compose stacks and user networks a database container shares;
//   - the engine's own session list, which says who is connected right now
//     — the one source that catches a client nobody declared.
//
// None of this crosses a line the routes already draw. The fleet read is on
// the read surface with one exception: the servers *not* connected yet come
// from detection, which reads container environment, so that part of the
// answer is only filled in for an administrator.

// fleetReading is what dialling a connection's server found: the part of a
// fleet tile that costs a connection to learn, and so the part that is kept
// for a few seconds between polls.
type fleetReading struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Version string `json:"version,omitempty"`
	// VersionNumber is the bare number inside Version, and Flavor the product
	// that answered: mariadb behind the mysql driver, valkey behind redis.
	// Until a server has answered once the flavour is the driver's own.
	VersionNumber string `json:"versionNumber,omitempty"`
	Flavor        string `json:"flavor"`
	// LatencyMs is how long the dial and version read took, which on a
	// server across a VPN is the number that explains a slow page.
	LatencyMs int64 `json:"latencyMs"`
	// Bytes is the size of the connection's database, where the engine can
	// say; SizesKnown false otherwise.
	Bytes      int64 `json:"bytes"`
	SizesKnown bool  `json:"sizesKnown"`
	// Objects is tables, collections or keys, in the engine's own vocabulary.
	Objects    int    `json:"objects"`
	ObjectWord string `json:"objectWord"`
	// Sessions is what the server reports connected, less this dashboard's
	// own pool, so the figure is the application's.
	Sessions int `json:"sessions"`
}

type fleetEntry struct {
	*dbConnection
	fleetReading
	// State is running, stopped, paused, unreachable or broken. A stopped or
	// paused server was not dialled: its container or unit says it is down,
	// and a dial would only have spent its timeout finding that out.
	State string `json:"state"`
	// Source is where the server is: a container here, a process on this
	// machine, a file, or somewhere else; unknown for a broken row.
	Source         string `json:"source"`
	Container      string `json:"container,omitempty"`
	ComposeProject string `json:"composeProject,omitempty"`
	// Unit is the systemd unit a native server runs under, where one does.
	Unit     string `json:"unit,omitempty"`
	Exposure string `json:"exposure"`
	// Consumers is how many deployment environments are bound to it.
	Consumers int `json:"consumers"`
	// LastBackup is when the newest dump of it was taken; nil for never.
	LastBackup *time.Time `json:"lastBackup,omitempty"`
}

type fleetResponse struct {
	Connections []fleetEntry `json:"connections"`
	// Detected is what is running here and not yet connected, for an
	// administrator: a container the sync could not reach, a native server
	// waiting for a password.
	Unreachable      []unreachableServer `json:"unreachable"`
	NeedsCredentials []credentialServer  `json:"needsCredentials"`
	CheckedAt        time.Time           `json:"checkedAt"`
}

func (s *Server) handleDBFleet(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	ctx = s.modules.docker.WithReadSnapshot(ctx)
	conns, err := s.allConnections(ctx)
	if err != nil {
		return err
	}
	bindings, _ := s.bindingCounts(ctx)

	out := fleetResponse{Connections: make([]fleetEntry, len(conns)), Unreachable: []unreachableServer{},
		NeedsCredentials: []credentialServer{}, CheckedAt: time.Now().UTC()}
	view := s.newDBHostView(ctx)
	var wg sync.WaitGroup
	// A handful at a time: nine dials in parallel is fine, ninety is a
	// connection storm against every server on the machine at once.
	sem := make(chan struct{}, 6)
	for i, conn := range conns {
		wg.Add(1)
		go func(i int, conn *dbConnection) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			entry := fleetEntry{dbConnection: conn, Exposure: "remote", Consumers: bindings[conn.ID]}
			s.fillFleetEntry(ctx, view, &entry)
			out.Connections[i] = entry
		}(i, conn)
	}
	wg.Wait()

	if p := httpx.MustPrincipal(r); p.Can(auth.CapSystemAdmin) {
		out.Unreachable, out.NeedsCredentials = s.undetectedServers(ctx)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// fleetReadingFresh is how long one dial of a connection answers for. The
// landing page, the overview and a board each poll the fleet, and each poll
// used to dial every server again: three pages open on nine connections was
// twenty-seven connections every half minute to learn what one would have
// said. Long enough to cover pages loading together, short enough that a
// tile is never more stale than its own polling interval.
const fleetReadingFresh = 10 * time.Second

type fleetReadingKept struct {
	dsn     [sha256.Size]byte
	mark    string
	at      time.Time
	reading fleetReading
}

// fillFleetEntry reads where a connection's server runs and, unless that says
// it is down, dials it for the readings the fleet tile draws.
func (s *Server) fillFleetEntry(ctx context.Context, view *dbHostView, e *fleetEntry) {
	e.Flavor, e.ObjectWord = dbx.DefaultFlavor(e.Driver), fleetObjectWord(e.Driver)
	if e.Broken {
		// Listed, flagged, and never dialled: there is no DSN to dial with.
		e.State, e.Error = dbStateBroken, e.BrokenReason
		e.Source, e.Exposure = dbSourceUnknown, dbSourceUnknown
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	conn, dsn, err := s.dbConnRow(ctx, e.ID)
	if err != nil {
		e.State, e.Error = dbStateBroken, "the connection could not be read"
		e.Source, e.Exposure = dbSourceUnknown, dbSourceUnknown
		return
	}
	place := view.place(ctx, conn, dsn)
	e.Source, e.Exposure = place.Source, string(place.Exposure)
	if place.Container != nil {
		e.Container, e.ComposeProject = place.Container.Name, place.Container.ComposeProject
	}
	if place.Unit != nil {
		e.Unit = place.Unit.Name
	}
	if newest := s.newestDump(conn.Name); newest != nil {
		e.LastBackup = newest
	}
	if place.Down != "" {
		e.State = place.Down
		// What it said it was the last time it answered still names it.
		if identity, _, ok := s.lastIdentity(conn.ID, dsn); ok {
			e.Flavor, e.Version, e.VersionNumber = identity.Flavor, identity.Version, identity.Number
		}
		return
	}
	e.fleetReading = s.fleetReadingFor(ctx, conn, dsn, place.Mark)
	e.State = dbStateUnreachable
	if e.OK {
		e.State = dbStateRunning
	}
}

// fleetReadingFor is a connection's dial, shared between the requests that
// ask for it within a few seconds of each other. The lock is per connection
// and held across the dial, so a second poll arriving mid-dial waits for the
// first one's answer instead of opening a second connection to get the same.
func (s *Server) fleetReadingFor(ctx context.Context, conn *dbConnection, dsn, mark string) fleetReading {
	sum := sha256.Sum256([]byte(dsn))
	fresh := func() (fleetReading, bool) {
		v, ok := s.dbConns.readings.Load(conn.ID)
		if !ok {
			return fleetReading{}, false
		}
		kept := v.(fleetReadingKept)
		return kept.reading, kept.dsn == sum && kept.mark == mark && time.Since(kept.at) < fleetReadingFresh
	}
	if reading, ok := fresh(); ok {
		return reading
	}
	mu := s.dbConns.lock(conn.ID)
	mu.Lock()
	defer mu.Unlock()
	if reading, ok := fresh(); ok {
		return reading
	}
	reading := s.dialFleetReading(ctx, conn, dsn)
	// A dial the request's own deadline cut short is not an answer about the
	// server, and keeping it would hand the next poll the same non-answer.
	if ctx.Err() == nil {
		s.dbConns.readings.Store(conn.ID, fleetReadingKept{dsn: sum, mark: mark, at: time.Now(), reading: reading})
	}
	return reading
}

func fleetObjectWord(driver dbx.Driver) string {
	switch driver {
	case dbx.DriverMongo:
		return "collections"
	case dbx.DriverRedis:
		return "keys"
	}
	return "tables"
}

// dialFleetReading dials one connection and reads what the tile draws.
// Everything past the dial is best effort: a version query that fails is an
// empty version, not a red row.
func (s *Server) dialFleetReading(ctx context.Context, conn *dbConnection, dsn string) fleetReading {
	e := fleetReading{Flavor: dbx.DefaultFlavor(conn.Driver), ObjectWord: fleetObjectWord(conn.Driver)}
	identify := func(identity dbx.Identity) {
		e.Flavor, e.Version, e.VersionNumber = identity.Flavor, identity.Version, identity.Number
	}
	started := time.Now()
	switch conn.Driver {
	case dbx.DriverMongo:
		client, err := dbx.MongoClient(ctx, dsn)
		if err != nil {
			e.Error = connectError(dsn, err)
			break
		}
		defer client.Disconnect(context.Background())
		e.OK = true
		e.LatencyMs = time.Since(started).Milliseconds()
		identify(s.identityOf(conn.ID, dsn, func() dbx.Identity { return dbx.IdentifyMongo(ctx, client) }))
		if status, err := dbx.MongoServerStatus(ctx, client); err == nil {
			if c, ok := status["connections"].(map[string]any); ok {
				e.Sessions = intOf(c["current"])
			}
		}
		if dbs, err := dbx.MongoDatabases(ctx, client); err == nil {
			for _, d := range dbs {
				if d.Name == conn.Database {
					e.Bytes, e.SizesKnown = d.Size, true
				}
			}
		}
		if cols, err := dbx.MongoCollections(ctx, client, conn.Database); err == nil {
			e.Objects = len(cols)
		}
	case dbx.DriverRedis:
		client, err := dbx.RedisClient(ctx, dsn, 0)
		if err != nil {
			e.Error = connectError(dsn, err)
			break
		}
		defer client.Close()
		e.OK = true
		e.LatencyMs = time.Since(started).Milliseconds()
		identify(s.identityOf(conn.ID, dsn, func() dbx.Identity { return dbx.IdentifyRedis(ctx, client) }))
		if info, err := dbx.RedisInfo(ctx, client); err == nil {
			e.Sessions = intOf(info["connected_clients"]) - 1
			if e.Sessions < 0 {
				e.Sessions = 0
			}
			if raw, ok := info["used_memory_human"].(string); ok {
				e.Bytes, e.SizesKnown = parseHumanBytes(raw)
			}
		}
		if dbs, err := dbx.RedisDatabases(ctx, client); err == nil {
			for _, d := range dbs {
				e.Objects += int(d.Size)
			}
		}
	default:
		pool, err := s.modules.dbs.Pool(ctx, conn.ID, conn.Driver, dsn)
		if err == nil {
			err = pool.PingContext(ctx)
		}
		if err != nil {
			s.dropPoolAfter(conn.ID, err)
			e.Error = connectError(dsn, err)
			break
		}
		e.OK = true
		e.LatencyMs = time.Since(started).Milliseconds()
		identify(s.identityOf(conn.ID, dsn, func() dbx.Identity { return dbx.IdentifySQL(ctx, pool, conn.Driver) }))
		s.fillSQLReadings(ctx, pool, conn, &e)
	}
	if !e.OK {
		// A server that has stopped answering is still the product it was.
		// Falling back to the driver's own here turned a MariaDB tile into a
		// MySQL one at the moment its server went away, while the connection's
		// own page went on saying MariaDB.
		if identity, _, known := s.lastIdentity(conn.ID, dsn); known {
			identify(identity)
		}
	}
	return e
}

func (s *Server) fillSQLReadings(ctx context.Context, pool *sql.DB, conn *dbConnection, e *fleetReading) {
	d, err := dbx.DialectFor(conn.Driver)
	if err != nil {
		return
	}
	if dbs, err := dbx.ListDatabases(ctx, pool, conn.Driver); err == nil {
		for _, db := range dbs {
			if db.Name == conn.Database || (conn.Database == "" && len(dbs) == 1) {
				e.Bytes = db.Size
				e.SizesKnown = db.Size > 0
			}
		}
	}
	if tables, err := d.Tables(ctx, pool, ""); err == nil {
		e.Objects = len(tables)
		if !e.SizesKnown {
			var total int64
			for _, t := range tables {
				total += t.Size
			}
			e.Bytes, e.SizesKnown = total, total > 0
		}
	}
	if sessions, err := d.Activity(ctx, pool); err == nil {
		e.Sessions = len(sessions) - pool.Stats().OpenConnections
		if e.Sessions < 0 {
			e.Sessions = 0
		}
	}
}

// bindingCounts is how many deployment environments each connection is bound
// to, in one query rather than one per tile.
func (s *Server) bindingCounts(ctx context.Context) (map[int64]int, error) {
	rows, err := s.Store.DB.QueryContext(ctx,
		`SELECT connection_id, COUNT(*) FROM deploy_database_bindings GROUP BY connection_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// undetectedServers is the sync's report of what it could see and not
// connect, read without writing anything: a listing, not a reconcile.
func (s *Server) undetectedServers(ctx context.Context) ([]unreachableServer, []credentialServer) {
	unreachable, needs := []unreachableServer{}, []credentialServer{}
	existing, err := s.existingDSNs(ctx)
	if err != nil {
		return unreachable, needs
	}
	var containers []dockerx.Container
	if s.modules.docker != nil {
		containers, _ = s.modules.docker.ListContainers(ctx, false)
	}
	for _, c := range containers {
		cand, _ := dbx.Detect(c.Name, c.Image, nil, publishedPorts(c.Ports), nil)
		if cand == nil {
			continue
		}
		if _, ok := existing[addressKey(cand.Host, cand.Port)]; ok {
			continue
		}
		detail, err := s.modules.docker.Inspect(ctx, c.ID)
		if err != nil {
			continue
		}
		cand, _ = dbx.Detect(c.Name, c.Image, envMap(detail.Env), publishedPorts(c.Ports), containerIPs(detail))
		if cand == nil {
			continue
		}
		if _, ok := existing[addressKey(cand.Host, cand.Port)]; ok {
			continue
		}
		if row, ok := unreachableFrom(cand); ok {
			unreachable = append(unreachable, row)
		}
	}
	for _, cand := range s.hostCandidates(ctx, detectedFrom(containers)) {
		if _, ok := existing[addressKey(cand.Host, cand.Port)]; ok {
			continue
		}
		needs = append(needs, credentialServer{
			Driver: string(cand.Driver), Host: cand.Host, Port: cand.Port,
			Process: cand.Process, Name: dbx.HostConnectionName(cand),
			User: cand.User, Database: cand.Database,
		})
	}
	return unreachable, needs
}

// newestDump is when the last dump of a connection landed, read off the
// directory the backups route writes to.
func (s *Server) newestDump(connName string) *time.Time {
	entries, err := readDirSorted(s.dbDumpDir(connName))
	if err != nil || len(entries) == 0 {
		return nil
	}
	t := entries[0].TakenAt
	return &t
}

func intOf(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int32:
		return int(x)
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}

// parseHumanBytes reads Redis's "1.23M" form.
func parseHumanBytes(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'K':
		mult = 1024
	case 'M':
		mult = 1024 * 1024
	case 'G':
		mult = 1024 * 1024 * 1024
	}
	if mult > 1 {
		s = s[:len(s)-1]
	}
	if s == "" {
		return 0, false
	}
	if f, err := strconv.ParseFloat(strings.TrimSuffix(s, "B"), 64); err == nil {
		return int64(f * float64(mult)), true
	}
	return 0, false
}

// --- topology ---------------------------------------------------------------

type topoNode struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // database, deployment, container, host, remote
	Name string `json:"name"`
	// Product is the key the frontend looks a logo up by: the engine for a
	// database, the image's product for a container.
	Product string `json:"product,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Status  string `json:"status,omitempty"`
	Href    string `json:"href,omitempty"`
	// ConnID is the connection a database node stands for.
	ConnID int64 `json:"connId,omitempty"`
}

type topoEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Via is how the link is known: binding, env, stack, network, session.
	Via []string `json:"via"`
	// Sessions is how many live connections the engine reports from there.
	Sessions int `json:"sessions"`
	// Status is connected, stale, broken or observed — the binding's word
	// where there is a binding, "observed" for a link only the engine or the
	// container's environment revealed.
	Status string `json:"status"`
}

type topologyResponse struct {
	Nodes     []topoNode `json:"nodes"`
	Edges     []topoEdge `json:"edges"`
	CheckedAt time.Time  `json:"checkedAt"`
}

func (s *Server) handleDBTopology(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	ctx = s.modules.docker.WithReadSnapshot(ctx)
	conns, err := s.existingConnections(ctx)
	if err != nil {
		return err
	}
	out, err := s.topology(ctx, conns)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleDBConsumers(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	ctx = s.modules.docker.WithReadSnapshot(ctx)
	out, err := s.topology(ctx, []*dbConnection{conn})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

type topoBinding struct {
	connID       int64
	envID        int64
	project      string
	projectID    int64
	environment  string
	container    string
	compose      string
	status       string
	detail       string
	checkedAt    int64
	deploymentID int64
}

func (s *Server) topologyBindings(ctx context.Context) ([]topoBinding, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `
	  SELECT b.connection_id, b.environment_id, p.id, p.name, e.name, b.container_name,
	         b.compose_project, b.status, b.detail, b.checked_at
	  FROM deploy_database_bindings b
	  JOIN deploy_environments e ON e.id = b.environment_id
	  JOIN deploy_projects p ON p.id = e.project_id
	  WHERE p.archived_at = 0
	  ORDER BY p.name, e.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []topoBinding{}
	for rows.Next() {
		var b topoBinding
		if err := rows.Scan(&b.connID, &b.envID, &b.projectID, &b.project, &b.environment,
			&b.container, &b.compose, &b.status, &b.detail, &b.checkedAt); err != nil {
			return nil, err
		}
		if b.checkedAt > 0 && time.Since(time.Unix(b.checkedAt, 0)) > 30*time.Second && b.status == "connected" {
			b.status = "stale"
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// inspected is one running container with what the topology needs read off
// it once, so the join below is over memory rather than the socket.
type inspected struct {
	c        dockerx.Container
	env      []string
	ips      []string
	networks []string
	product  string
}

func (s *Server) inspectRunning(ctx context.Context) []inspected {
	if s.modules.docker == nil {
		return nil
	}
	containers, err := s.modules.docker.ListContainers(ctx, false)
	if err != nil {
		return nil
	}
	out := make([]inspected, len(containers))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, c := range containers {
		wg.Add(1)
		go func(i int, c dockerx.Container) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			entry := inspected{c: c, networks: c.Networks, product: imageProduct(c.Image)}
			if detail, err := s.modules.docker.Inspect(ctx, c.ID); err == nil {
				entry.env = detail.Env
				entry.ips = containerIPs(detail)
				for _, n := range detail.NetworkList {
					entry.networks = append(entry.networks, n.Name)
				}
			}
			out[i] = entry
		}(i, c)
	}
	wg.Wait()
	return out
}

// imageProduct is the image's repository name without registry or tag —
// "postgres", "n8nio/n8n" → "n8n" — which is what the logo table is keyed by.
func imageProduct(image string) string {
	name := image
	if i := strings.LastIndex(name, "@"); i > 0 {
		name = name[:i]
	}
	if i := strings.LastIndex(name, ":"); i > 0 && !strings.Contains(name[i:], "/") {
		name = name[:i]
	}
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return strings.ToLower(name)
}

func (s *Server) topology(ctx context.Context, conns []*dbConnection) (*topologyResponse, error) {
	out := &topologyResponse{Nodes: []topoNode{}, Edges: []topoEdge{}, CheckedAt: time.Now().UTC()}
	bindings, err := s.topologyBindings(ctx)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	running := s.inspectRunning(ctx)
	byIP := map[string]*inspected{}
	byName := map[string]*inspected{}
	for i := range running {
		for _, ip := range running[i].ips {
			byIP[ip] = &running[i]
		}
		byName[running[i].c.Name] = &running[i]
	}

	nodes := map[string]*topoNode{}
	edges := map[string]*topoEdge{}
	addNode := func(n topoNode) *topoNode {
		if have, ok := nodes[n.ID]; ok {
			return have
		}
		copy := n
		nodes[n.ID] = &copy
		return &copy
	}
	addEdge := func(from, to, via, status string, sessions int) {
		key := from + "→" + to
		e, ok := edges[key]
		if !ok {
			e = &topoEdge{From: from, To: to, Status: status}
			edges[key] = e
		}
		found := false
		for _, v := range e.Via {
			if v == via {
				found = true
			}
		}
		if !found {
			e.Via = append(e.Via, via)
		}
		e.Sessions += sessions
		// A binding's word outranks an observation's: "broken" is the fact
		// the reader needs even when the engine still sees a session.
		if rank(status) > rank(e.Status) {
			e.Status = status
		}
	}

	for _, conn := range conns {
		dbID := "db:" + strconv.FormatInt(conn.ID, 10)
		node := addNode(topoNode{
			ID: dbID, Kind: "database", Name: conn.Name, Product: string(conn.Driver),
			Detail: conn.Database, ConnID: conn.ID, Href: "/databases/" + strconv.FormatInt(conn.ID, 10),
		})
		_, dsn, err := s.dbConnRow(ctx, conn.ID)
		if err != nil {
			continue
		}
		info, _ := dbx.ParseDSN(conn.Driver, dsn)
		port, _ := strconv.Atoi(info.Port)
		var server *dbServer
		if info != nil && conn.Driver != dbx.DriverSQLite {
			server = s.serverBehind(ctx, conn, info)
		}
		if server != nil {
			node.Detail = server.container.Name + " · " + conn.Database
		}

		// 1. Deployments bound to it.
		for _, b := range bindings {
			if b.connID != conn.ID {
				continue
			}
			depID := "deploy:" + strconv.FormatInt(b.envID, 10)
			name := b.project
			if b.environment != "" && !strings.EqualFold(b.environment, "production") {
				name += " · " + b.environment
			}
			product := ""
			if c, ok := byName[b.container]; ok {
				product = c.product
			}
			addNode(topoNode{
				ID: depID, Kind: "deployment", Name: name, Product: product,
				Detail: b.container, Status: b.status, Href: "/deploy/" + strconv.FormatInt(b.projectID, 10),
			})
			addEdge(dbID, depID, "binding", b.status, 0)
		}

		// 2. Containers whose environment names it, and stack siblings.
		needles := topoNeedles(conn, info, port, server)
		for i := range running {
			c := &running[i]
			if server != nil && c.c.ID == server.container.ID {
				continue
			}
			if isDatabaseImage(c.c) {
				continue
			}
			id := "container:" + c.c.Name
			if bound := boundContainer(bindings, conn.ID, c.c.Name); bound != "" {
				// Already drawn as its deployment; the container is that
				// deployment's release.
				continue
			}
			mentioned := envMentions(c.env, needles)
			sameStack := server != nil && server.container.ComposeStack != "" &&
				c.c.ComposeStack == server.container.ComposeStack
			sameNet := server != nil && sharesUserNetwork(server.container.Networks, c.networks)
			if !mentioned && !sameStack {
				continue
			}
			addNode(topoNode{
				ID: id, Kind: "container", Name: c.c.Name, Product: c.product,
				Detail: c.c.Image, Status: c.c.State, Href: "/docker/containers/" + c.c.ID,
			})
			via := "env"
			if !mentioned && sameStack {
				via = "stack"
			}
			addEdge(dbID, id, via, "observed", 0)
			if sameNet && mentioned {
				addEdge(dbID, id, "network", "observed", 0)
			}
		}

		// 3. Who is connected right now.
		for client, n := range s.liveClients(ctx, conn, dsn) {
			if c, ok := byIP[client]; ok {
				if server != nil && c.c.ID == server.container.ID {
					continue
				}
				id := "container:" + c.c.Name
				if bound := boundContainer(bindings, conn.ID, c.c.Name); bound != "" {
					id = "deploy:" + bound
				} else {
					addNode(topoNode{
						ID: id, Kind: "container", Name: c.c.Name, Product: c.product,
						Detail: c.c.Image, Status: c.c.State, Href: "/docker/containers/" + c.c.ID,
					})
				}
				addEdge(dbID, id, "session", "observed", n)
				continue
			}
			if client == "" || databaseLoopback(client) || isGateway(client, running) {
				addNode(topoNode{ID: "host", Kind: "host", Name: "This server", Detail: "processes on the machine"})
				addEdge(dbID, "host", "session", "observed", n)
				continue
			}
			id := "remote:" + client
			addNode(topoNode{ID: id, Kind: "remote", Name: client, Detail: "another machine"})
			addEdge(dbID, id, "session", "observed", n)
		}
	}

	for _, n := range nodes {
		out.Nodes = append(out.Nodes, *n)
	}
	sort.Slice(out.Nodes, func(i, j int) bool {
		if out.Nodes[i].Kind != out.Nodes[j].Kind {
			return kindRank(out.Nodes[i].Kind) < kindRank(out.Nodes[j].Kind)
		}
		return out.Nodes[i].Name < out.Nodes[j].Name
	})
	for _, e := range edges {
		out.Edges = append(out.Edges, *e)
	}
	sort.Slice(out.Edges, func(i, j int) bool {
		if out.Edges[i].From != out.Edges[j].From {
			return out.Edges[i].From < out.Edges[j].From
		}
		return out.Edges[i].To < out.Edges[j].To
	})
	return out, nil
}

func rank(status string) int {
	switch status {
	case "broken", "failed", "error":
		return 4
	case "stale":
		return 3
	case "pending":
		return 2
	case "connected":
		return 1
	}
	return 0
}

func kindRank(kind string) int {
	switch kind {
	case "database":
		return 0
	case "deployment":
		return 1
	case "container":
		return 2
	case "host":
		return 3
	}
	return 4
}

// topoNeedles is every string a client's environment might carry to name
// this server: its published address, the container's name and aliases, the
// deployment alias.
func topoNeedles(conn *dbConnection, info *dbx.ConnInfo, port int, server *dbServer) []string {
	out := []string{"db-" + strconv.FormatInt(conn.ID, 10) + ".jd.internal"}
	if info != nil && port > 0 {
		p := strconv.Itoa(port)
		if databaseLoopback(info.Host) {
			out = append(out, "127.0.0.1:"+p, "localhost:"+p, "host.docker.internal:"+p, "172.17.0.1:"+p)
		} else {
			out = append(out, info.Host+":"+p)
		}
	}
	if server != nil {
		name := server.container.Name
		out = append(out, "@"+name+":", "@"+name+"/", "//"+name+":", "//"+name+"/", "="+name+"\n")
		if server.container.ComposeSvc != "" {
			svc := server.container.ComposeSvc
			out = append(out, "@"+svc+":", "@"+svc+"/", "//"+svc+":", "//"+svc+"/")
		}
	}
	return out
}

func envMentions(env []string, needles []string) bool {
	for _, kv := range env {
		_, value, ok := strings.Cut(kv, "=")
		if !ok || value == "" {
			continue
		}
		for _, n := range needles {
			needle := strings.TrimSuffix(n, "\n")
			if strings.HasPrefix(n, "=") {
				// "=name" is a bare value: HOST=dbcontainer.
				if value == needle[1:] {
					return true
				}
				continue
			}
			if strings.Contains(value, needle) {
				return true
			}
		}
	}
	return false
}

func isDatabaseImage(c dockerx.Container) bool {
	cand, _ := dbx.Detect(c.Name, c.Image, nil, nil, nil)
	return cand != nil
}

func boundContainer(bindings []topoBinding, connID int64, container string) string {
	for _, b := range bindings {
		if b.connID == connID && b.container == container {
			return strconv.FormatInt(b.envID, 10)
		}
	}
	return ""
}

func sharesUserNetwork(a, b []string) bool {
	for _, x := range a {
		if x == "bridge" || x == "host" || x == "none" {
			continue
		}
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// isGateway reports a client address that is a bridge gateway — what a
// container sees when a host process, or a published-port client, connects.
func isGateway(client string, running []inspected) bool {
	ip := net.ParseIP(client)
	if ip == nil {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil && ip4[3] == 1 && ip.IsPrivate() {
		return true
	}
	return false
}

// liveClients counts the engine's sessions by client address, less the
// dashboard's own.
func (s *Server) liveClients(ctx context.Context, conn *dbConnection, dsn string) map[string]int {
	out := map[string]int{}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	switch conn.Driver {
	case dbx.DriverRedis:
		client, err := dbx.RedisClient(ctx, dsn, 0)
		if err != nil {
			return out
		}
		defer client.Close()
		raw, err := client.ClientList(ctx).Result()
		if err != nil {
			return out
		}
		for _, line := range strings.Split(raw, "\n") {
			for _, f := range strings.Fields(line) {
				if strings.HasPrefix(f, "addr=") {
					host, _, err := net.SplitHostPort(strings.TrimPrefix(f, "addr="))
					if err == nil {
						out[host]++
					}
				}
			}
		}
		// This request's own client is one of them.
		for host := range out {
			if databaseLoopback(host) {
				out[host]--
				if out[host] <= 0 {
					delete(out, host)
				}
				break
			}
		}
		return out
	case dbx.DriverMongo:
		client, err := dbx.MongoClient(ctx, dsn)
		if err != nil {
			return out
		}
		defer client.Disconnect(context.Background())
		for _, addr := range dbx.MongoClientAddresses(ctx, client) {
			out[addr]++
		}
		return out
	case dbx.DriverSQLite:
		return out
	}
	pool, err := s.modules.dbs.Pool(ctx, conn.ID, conn.Driver, dsn)
	if err != nil {
		return out
	}
	sessions, err := dbx.ListActivity(ctx, pool, conn.Driver)
	if err != nil {
		return out
	}
	own := pool.Stats().OpenConnections
	for _, a := range sessions {
		host := a.Client
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if a.Self {
			continue
		}
		if own > 1 && (host == "" || databaseLoopback(host) || strings.HasSuffix(host, ".1")) && a.User == conn.User {
			// The pool's other idle connections, which look exactly like a
			// local client and are not one.
			own--
			continue
		}
		out[host]++
	}
	return out
}
