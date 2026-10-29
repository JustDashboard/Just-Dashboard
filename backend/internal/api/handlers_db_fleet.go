package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"net"
	"net/http"
	"os"
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
	// Power is which of start, stop and restart the power route would accept
	// for this connection right now, and Managed whether the access route can
	// change how far its port reaches: the two readings the connection's own
	// summary gives, so that a card can offer what the connection's page
	// offers without working either out again from the fields above.
	Power   dbPower `json:"power"`
	Managed bool    `json:"managed"`
	// InFlight is the start, stop or restart being carried out on the server
	// from this dashboard, as the summary reports it. Absent when there is
	// none.
	InFlight *dbPowerChange `json:"inFlight,omitempty"`
	// Consumers is how many deployment environments are bound to it.
	Consumers int `json:"consumers"`
	// LastBackup is when it was last backed up from here, by hand or by a
	// backup job (lastBackup); nil for never.
	LastBackup *time.Time `json:"lastBackup,omitempty"`
	// KernelProblem is a MongoDB 8 server that cannot start on this kernel
	// without the mitigation (mongoKernelProblem); absent otherwise.
	KernelProblem *dbKernelProblem `json:"kernelProblem,omitempty"`
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
	// One read of the backup runs for every row. Runs that cannot be read
	// leave each row the answer of its dump directory alone.
	jobDumps, _ := s.modules.backupStore.LastDatabaseDumps(ctx)

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
			s.fillFleetEntry(ctx, view, &entry, jobDumps)
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
// it is down, dials it for the readings the fleet tile draws. jobDumps is
// LastDatabaseDumps' reading, taken once for the whole fleet.
func (s *Server) fillFleetEntry(ctx context.Context, view *dbHostView, e *fleetEntry, jobDumps map[int64]time.Time) {
	e.Flavor, e.ObjectWord = dbx.DefaultFlavor(e.Driver), fleetObjectWord(e.Driver)
	if e.Broken {
		// Listed, flagged, and never dialled: there is no DSN to dial with.
		e.State, e.Error = dbStateBroken, e.BrokenReason
		e.Source, e.Exposure = dbSourceUnknown, dbSourceUnknown
		e.Power = dbPower{Reason: dbPowerBrokenReason}
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	conn, dsn, err := s.dbConnRow(ctx, e.ID)
	if err != nil {
		e.State, e.Error = dbStateBroken, "the connection could not be read"
		e.Source, e.Exposure = dbSourceUnknown, dbSourceUnknown
		e.Power = dbPower{Reason: dbPowerBrokenReason}
		return
	}
	place := view.place(ctx, conn, dsn)
	e.Source, e.Exposure = place.Source, string(place.Exposure)
	// Read off the placement the entry already needed: no second look at the
	// machine, and no dial.
	e.Power, e.Managed = place.power(conn, view.systemctlAvailable()), place.Managed
	defer func() { e.InFlight = s.dbConns.inFlight(conn.ID, e.State) }()
	if place.Container != nil {
		e.Container, e.ComposeProject = place.Container.Name, place.Container.ComposeProject
	}
	if place.Unit != nil {
		e.Unit = place.Unit.Name
	}
	e.LastBackup = s.lastBackup(conn, jobDumps)
	e.KernelProblem = s.mongoKernelProblem(ctx, conn.Driver, place)
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
	if place.File != nil && !e.SizesKnown {
		// A SQLite database is its file, and its catalogue gives no size for a
		// table or for itself: the file's own is the answer, to every role,
		// where it used to be known only to whoever may read the inventory.
		// Read after the dial, which is what creates a file that was not there.
		if file := fileRef(place.File.Path); file.Exists {
			e.Bytes, e.SizesKnown = file.Size, true
		}
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
	// server, and keeping it would hand the next poll the same non-answer. Nor
	// is a refusal from a server that is being started or restarted from here:
	// kept, it would go on reading "unreachable" for ten seconds after the
	// engine had begun to answer, with "Starting…" drawn beside it.
	if _, changing := s.dbConns.power.Load(conn.ID); ctx.Err() == nil && (reading.OK || !changing) {
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
		// The connection string's own database, as the ping and the summary
		// dial it, so the three agree on whether the connection works.
		client, err := dbx.RedisClient(ctx, dsn, dbx.RedisDSNDatabase)
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
		e.Sessions = applicationSessions(conn.Driver, sessions, pool.Stats().OpenConnections)
	}
}

// applicationSessions is how many of a server's sessions are an application's
// rather than the dashboard's. Every dashboard session on a PostgreSQL server
// gives the dashboard's name (dbx.ApplicationName), whichever connection's pool
// holds it; on another engine the size of the pool that asked is all there is
// to go on, and two saved connections to one server still count each other.
func applicationSessions(driver dbx.Driver, sessions []dbx.Activity, pooled int) int {
	if driver == dbx.DriverPostgres {
		n := 0
		for _, a := range sessions {
			if !dashboardSession(a) {
				n++
			}
		}
		return n
	}
	return max(len(sessions)-pooled, 0)
}

// dashboardSession reports a session the dashboard holds: the one asking, or
// one that connected under the dashboard's name.
func dashboardSession(a dbx.Activity) bool {
	return a.Self || a.Application == dbx.ApplicationName
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

// undetectedServers is what the sync would report of the servers on this
// machine that nothing is connected to, read from discovery's inventory
// without writing or dialling anything: a listing, not a reconcile.
//
// It is the inventory's reading and the sync's rules, so the fleet, the sync
// and the list of what was found cannot tell three stories about one server:
// a stopped one, one the operator set aside and one recognised only by its
// port are left out here as they are left alone there.
//
// It is the inventory's last reading, never a new one: the fleet does not wait
// on the machine (`keptInventory`). Before the first reading has been taken
// both lists are empty, and the inventory's own route — which the page reads
// beside the fleet — is the fuller account of the same servers.
func (s *Server) undetectedServers(ctx context.Context) ([]unreachableServer, []credentialServer) {
	unreachable, needs := []unreachableServer{}, []credentialServer{}
	kept, ok := s.keptInventory(true)
	if !ok {
		return unreachable, needs
	}
	instances, _, err := s.annotateInventory(ctx, kept.instances)
	if err != nil {
		return unreachable, needs
	}
	for _, inst := range instances {
		if inst.Kind != dbx.KindServer || inst.Driver == "" || inst.State != dbx.StateRunning ||
			len(inst.Connections) > 0 || inst.Ignored || inst.Self {
			continue
		}
		if inst.Source == dbx.SourceHost {
			// A server installed on the machine keeps its password where
			// nothing here reads it: it is offered with everything filled in
			// but that.
			host, port, reachable := dialledAt(inst)
			if !inst.Connectable || !reachable {
				continue
			}
			cand := dbx.Candidate{
				Driver: inst.Driver, Source: dbx.SourceHost, Host: host, Port: port,
				User: inst.User, Database: inst.Database,
			}
			if inst.Host != nil {
				cand.Process = inst.Host.Process
			}
			needs = append(needs, credentialServer{
				Driver: string(cand.Driver), Host: cand.Host, Port: cand.Port,
				Process: cand.Process, Name: dbx.HostConnectionName(cand),
				User: cand.User, Database: cand.Database,
			})
			continue
		}
		if inst.Confidence == dbx.ConfidencePort {
			continue
		}
		switch inst.Credentials {
		case dbx.CredentialsEnv, dbx.CredentialsArgs, dbx.CredentialsOpen, dbx.CredentialsSecretFile:
			// It states what a connection needs, so the sync connects it; it
			// is only worth a line here when it cannot be reached at all.
			if inst.Connectable {
				continue
			}
			unreachable = append(unreachable, unreachableServer{
				Container: inst.Name, Driver: string(inst.Driver), Reason: unreachableReason(inst.Reason),
			})
		default:
			reason := containerStatesNoPassword
			if !inst.Connectable {
				reason = unreachableReason(inst.Reason)
			}
			unreachable = append(unreachable, unreachableServer{
				Container: inst.Name, Driver: string(inst.Driver), Reason: reason,
			})
		}
	}
	return unreachable, needs
}

// dialledAt is the address a connection to a found server is made to.
func dialledAt(inst dbx.Instance) (host string, port int, ok bool) {
	for _, e := range inst.Endpoints {
		if e.Primary && e.Kind == "tcp" {
			return e.Host, e.Port, true
		}
	}
	return "", 0, false
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

// lastBackup is when a connection was last backed up from here: its newest
// dump in the dump directory or the newest backup job run that holds its dump
// (jobDumps), whichever is later. The directory alone called a database a
// nightly job protects never backed up, and the job alone would call one
// dumped by hand since the job last ran older than it is.
func (s *Server) lastBackup(conn *dbConnection, jobDumps map[int64]time.Time) *time.Time {
	newest := s.newestDump(conn.Name)
	if ran, ok := jobDumps[conn.ID]; ok && (newest == nil || ran.After(*newest)) {
		return &ran
	}
	return newest
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
	// ProjectID is the project a deployment node is an environment of, which
	// is what the frontend draws its website's icon by.
	ProjectID int64 `json:"projectId,omitempty"`
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
	environments, err := s.topologyEnvironments(ctx)
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
	hosts := hostAddresses()

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
	// A running container is drawn as the deployment it is a release of, where
	// its labels say so: the reader knows the project, not jd-e291-r1.
	consumer := func(c *inspected) string {
		if env, ok := releaseOf(c.c, environments); ok {
			id := "deploy:" + strconv.FormatInt(env.id, 10)
			addNode(topoNode{
				ID: id, Kind: "deployment", Name: deploymentName(env.project, env.environment),
				Product: c.product, Detail: c.c.Name, Status: c.c.State,
				Href: "/deploy/" + strconv.FormatInt(env.projectID, 10), ProjectID: env.projectID,
			})
			return id
		}
		id := "container:" + c.c.Name
		addNode(topoNode{
			ID: id, Kind: "container", Name: c.c.Name, Product: c.product,
			Detail: c.c.Image, Status: c.c.State, Href: "/docker/containers/" + c.c.ID,
		})
		return id
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
		var serverIPs []string
		if server != nil {
			node.Detail = server.container.Name + " · " + conn.Database
			if c, ok := byName[server.container.Name]; ok {
				serverIPs = c.ips
			}
		}

		// 1. Deployments bound to it.
		for _, b := range bindings {
			if b.connID != conn.ID {
				continue
			}
			depID := "deploy:" + strconv.FormatInt(b.envID, 10)
			product := ""
			if c, ok := byName[b.container]; ok {
				product = c.product
			}
			addNode(topoNode{
				ID: depID, Kind: "deployment", Name: deploymentName(b.project, b.environment), Product: product,
				Detail: b.container, Status: b.status, Href: "/deploy/" + strconv.FormatInt(b.projectID, 10),
				ProjectID: b.projectID,
			})
			addEdge(dbID, depID, "binding", b.status, 0)
		}

		// 2. Containers whose environment names it, and stack siblings.
		addresses := topoAddresses(conn, info, port, server, serverIPs, hosts)
		for i := range running {
			c := &running[i]
			if server != nil && c.c.ID == server.container.ID {
				continue
			}
			if isDatabaseImage(c.c) {
				continue
			}
			if bound := boundContainer(bindings, conn.ID, c.c.Name); bound != "" {
				// Already drawn as its deployment; the container is that
				// deployment's release.
				continue
			}
			mentioned := addresses.namedIn(c.env)
			sameStack := server != nil && server.container.ComposeStack != "" &&
				c.c.ComposeStack == server.container.ComposeStack
			sameNet := server != nil && sharesUserNetwork(server.container.Networks, c.networks)
			if !mentioned && !sameStack {
				continue
			}
			id := consumer(c)
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
				id := ""
				if bound := boundContainer(bindings, conn.ID, c.c.Name); bound != "" {
					id = "deploy:" + bound
				} else {
					id = consumer(c)
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

// deploymentName is a deployment environment as the map names it: the
// project, and the environment where it is not the one every project has.
func deploymentName(project, environment string) string {
	if environment != "" && !strings.EqualFold(environment, "production") {
		return project + " · " + environment
	}
	return project
}

// topoEnvironment is a deployment environment by the id its releases carry.
type topoEnvironment struct {
	id          int64
	projectID   int64
	project     string
	environment string
}

func (s *Server) topologyEnvironments(ctx context.Context) (map[int64]topoEnvironment, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `
	  SELECT e.id, p.id, p.name, e.name
	  FROM deploy_environments e
	  JOIN deploy_projects p ON p.id = e.project_id
	  WHERE p.archived_at = 0 AND e.archived_at = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]topoEnvironment{}
	for rows.Next() {
		var e topoEnvironment
		if err := rows.Scan(&e.id, &e.projectID, &e.project, &e.environment); err != nil {
			return nil, err
		}
		out[e.id] = e
	}
	return out, rows.Err()
}

// releaseOf is the deployment environment a container is a release of, read
// off the labels the deployment runtime starts every release with.
func releaseOf(c dockerx.Container, environments map[int64]topoEnvironment) (topoEnvironment, bool) {
	if c.Labels["io.just-dashboard.managed"] != "true" {
		return topoEnvironment{}, false
	}
	id, err := strconv.ParseInt(c.Labels[deploymentEnvironmentLabel], 10, 64)
	if err != nil {
		return topoEnvironment{}, false
	}
	env, ok := environments[id]
	return env, ok
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

// topoAddressBook is every way a client's environment might name this server:
// whole strings to look for in a value, and host and port pairs for an
// environment that keeps the two apart (DB_HOST and DB_PORT).
type topoAddressBook struct {
	needles []string
	pairs   [][2]string
}

// topoAddresses is the address book of one connection's server: the
// `db-N.jd.internal` alias, the address it was saved with, the container's
// name, compose service and addresses, and every address of this machine with
// the port the server is published on. An application seldom names a server
// the way the dashboard does — a deployment holding
// postgres://…@203.0.113.7:5435/app reaches the container published on 5435
// the dashboard reaches at 127.0.0.1:5435 — so each of those is looked for.
func topoAddresses(conn *dbConnection, info *dbx.ConnInfo, port int, server *dbServer, serverIPs, hosts []string) topoAddressBook {
	book := topoAddressBook{needles: []string{databaseDNSName(conn.ID), poolerDNSName(conn.ID)}}
	seen := map[string]bool{}
	reach := func(hosts []string, port int) {
		p := strconv.Itoa(port)
		for _, h := range hosts {
			if h == "" || seen[h+":"+p] {
				continue
			}
			seen[h+":"+p] = true
			book.needles = append(book.needles, h+":"+p)
			book.pairs = append(book.pairs, [2]string{h, p})
		}
	}
	if info != nil && port > 0 {
		if databaseLoopback(info.Host) {
			// A server this machine answers on: a container reaches it at any
			// of the machine's addresses, never at the loopback it was saved at.
			reach(hosts, port)
		} else {
			reach([]string{info.Host}, port)
		}
	}
	if server != nil {
		for _, b := range server.container.Ports {
			if int(b.PrivatePort) == server.port && b.PublicPort > 0 {
				reach(publishedOn(b.IP, hosts), int(b.PublicPort))
			}
		}
		reach(serverIPs, server.port)
		names := []string{server.container.Name}
		if server.container.ComposeSvc != "" {
			names = append(names, server.container.ComposeSvc)
		}
		for _, name := range names {
			book.needles = append(book.needles, "@"+name+":", "@"+name+"/", "//"+name+":", "//"+name+"/")
			book.pairs = append(book.pairs, [2]string{name, strconv.Itoa(server.port)})
		}
		// A bare value naming the container: HOST=dbcontainer.
		book.needles = append(book.needles, "="+server.container.Name)
	}
	return book
}

// publishedOn is the addresses a port Docker published is reached at: every
// address of the machine for an unspecified binding, the loopback names for a
// loopback one, and otherwise the one address it is bound to.
func publishedOn(bound string, hosts []string) []string {
	ip := net.ParseIP(strings.Trim(bound, "[]"))
	switch {
	case bound == "" || (ip != nil && ip.IsUnspecified()):
		return hosts
	case ip != nil && ip.IsLoopback():
		return []string{"127.0.0.1", "localhost"}
	}
	return []string{bound}
}

// hostAddresses is every name and address this machine is reached at from a
// container or a process on it: loopback, Docker's names for the host, the
// hostname, and every interface's address — the bridge gateways and the public
// address among them.
func hostAddresses() []string {
	out := []string{"127.0.0.1", "localhost", "host.docker.internal", "172.17.0.1"}
	if name, err := os.Hostname(); err == nil && name != "" {
		out = append(out, name)
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLinkLocalUnicast() || ipnet.IP.IsLoopback() {
			continue
		}
		if ip4 := ipnet.IP.To4(); ip4 != nil {
			out = append(out, ip4.String())
		} else {
			out = append(out, "["+ipnet.IP.String()+"]")
		}
	}
	return out
}

// namedIn reports an environment that names the server: a value holding one
// of its addresses whole, or one variable holding a host and another its port.
func (book topoAddressBook) namedIn(env []string) bool {
	return len(book.naming(env)) > 0
}

// naming is the variables of an environment that name the server, in the
// environment's order: each whose value holds one of its addresses whole, and
// both halves of a host and port kept apart.
func (book topoAddressBook) naming(env []string) []string {
	holders := map[string][]string{}
	named := map[string]bool{}
	out := []string{}
	name := func(key string) {
		if !named[key] {
			named[key] = true
			out = append(out, key)
		}
	}
	for _, kv := range env {
		key, value, ok := strings.Cut(kv, "=")
		if !ok || value == "" {
			continue
		}
		holders[value] = append(holders[value], key)
		for _, needle := range book.needles {
			if bare, ok := strings.CutPrefix(needle, "="); ok {
				if value == bare {
					name(key)
				}
				continue
			}
			if containsAddress(value, needle) {
				name(key)
			}
		}
	}
	for _, pair := range book.pairs {
		if len(holders[pair[0]]) == 0 || len(holders[pair[1]]) == 0 {
			continue
		}
		for _, half := range pair {
			for _, key := range holders[half] {
				name(key)
			}
		}
	}
	return out
}

// containsAddress finds an address in a value as a whole: 10.0.0.3:5432 is
// not in 110.0.0.3:54321, though the bytes are.
func containsAddress(value, needle string) bool {
	for from := 0; from < len(value); {
		i := strings.Index(value[from:], needle)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(needle)
		before := i == 0 || !addressByte(needle[0]) || !addressByte(value[i-1])
		after := end == len(value) || !isDigit(needle[len(needle)-1]) || !isDigit(value[end])
		if before && after {
			return true
		}
		from = i + 1
	}
	return false
}

func addressByte(b byte) bool {
	return b == '.' || b == '-' || isDigit(b) || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

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
	named := conn.Driver == dbx.DriverPostgres
	for _, a := range sessions {
		host := a.Client
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if dashboardSession(a) {
			continue
		}
		if !named && own > 1 && (host == "" || databaseLoopback(host) || strings.HasSuffix(host, ".1")) && a.User == conn.User {
			// The pool's other idle connections, which look exactly like a
			// local client and are not one.
			own--
			continue
		}
		out[host]++
	}
	return out
}
