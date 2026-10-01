package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// A saved connection as a thing in its own right.
//
// Until now a connection was a row in a list and a set of tabs: there was no
// way to ask about one without asking about all of them, nothing said where
// its server runs or whether it is running at all, and stopping a database
// meant leaving for the Docker page and finding the container by name. A
// server installed on the machine had no controls anywhere.
//
// Two routes close that. `GET /databases/{id}` is one connection's reading —
// what the server says it is, where it runs, whether it answers, what can be
// done to it — without dialling any other connection. `POST
// /databases/{id}/power` starts, stops or restarts whatever is behind it: a
// container through the Docker socket, a unit through the host's systemctl.
//
// Where a server runs is not stored. It is read off the machine each time,
// the way the access route already does: the container that publishes the
// connection's port, the process listening on it and the unit that process
// belongs to. A stored answer would be wrong the first time somebody
// recreated the container, and this one cannot be.

// mountDatabaseConnectionRoutes registers the routes about one saved
// connection as a thing: its summary and its power state. Its protection is
// the middleware in handlers_db_protect.go, mounted once in front of every
// database route. It is called inside the /databases route, so paths are
// relative to it and each group states the capability it needs.
func (s *Server) mountDatabaseConnectionRoutes(r chi.Router) {
	// Read surface, like the fleet it is one row of.
	r.Method(http.MethodGet, "/{id}", s.handle(s.handleDBConnSummary))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapServiceControl))
		// Starting a server is what service.control is for. Stopping and
		// restarting one interrupt whatever is using it, and they share this
		// path, so the handler asks for the destructive capability and
		// spends its budget by hand once it has read which was asked for —
		// the shape the query route has for the same reason.
		r.Method(http.MethodPost, "/{id}/power", s.handle(s.handleDBPower))
	})
}

// dbConnState is what the connection routes remember between requests. Its
// zero value is ready to use.
type dbConnState struct {
	// identities is what each server said it is, by connection id.
	identities sync.Map
	// readings is each connection's last fleet dial, and locks the mutex
	// that keeps several pages polling at once from dialling it several
	// times over.
	readings sync.Map
	locks    sync.Map
	// units is the systemd unit last seen serving each connection, which is
	// what says which unit to start once it has stopped. It is kept in memory
	// only: after the dashboard restarts, a stopped server on a port that is
	// not its engine's own has no unit until it has been seen running again.
	units sync.Map
	// probe reads the machine's sockets and units. Nil is the machine itself;
	// a test hands in one of its own.
	probe *dbHostProbe
	// systemctl starts and stops a unit. Nil is the host's own; a test hands
	// in one that records what it was asked.
	systemctl *dbSystemctl
}

// forget drops everything kept about a connection, for a change after which
// none of it can be trusted: a new DSN, the row's removal.
func (st *dbConnState) forget(id int64) {
	st.identities.Delete(id)
	st.readings.Delete(id)
	st.units.Delete(id)
	st.locks.Delete(id)
}

// stale drops a connection's last reading and keeps what its server said it
// is: stopping a server changes whether it answers, not what it is, and the
// tile of a stopped Valkey should still say Valkey.
func (st *dbConnState) stale(id int64) {
	st.readings.Delete(id)
}

// lock is the mutex one connection's fleet dial is taken under.
func (st *dbConnState) lock(id int64) *sync.Mutex {
	mu, _ := st.locks.LoadOrStore(id, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// storedConnection reads a connection the way the list does: a row that
// cannot be opened comes back flagged rather than as an error, with an empty
// DSN. It is for the routes that must work on a broken connection — reading
// what is wrong with it, replacing its DSN, forgetting it.
func (s *Server) storedConnection(ctx context.Context, id int64) (*dbConnection, error) {
	conn, _, err := s.storedConnectionDSN(ctx, id)
	return conn, err
}

func (s *Server) storedConnectionDSN(ctx context.Context, id int64) (*dbConnection, string, error) {
	rec, err := scanDBConn(s.Store.DB.QueryRowContext(ctx,
		`SELECT `+dbConnColumns+` FROM db_connections WHERE id = ?`, id).Scan)
	if err == sql.ErrNoRows {
		return nil, "", httpx.ErrNotFound
	}
	if err != nil {
		return nil, "", httpx.Internal(err)
	}
	conn, dsn, err := s.openDBConn(rec)
	if err != nil {
		broken := rec.conn
		broken.Broken, broken.BrokenReason = true, brokenReason(err)
		return &broken, "", nil
	}
	return conn, dsn, nil
}

// siblingConnectionName names a connection to another database on the same
// server after both, within the rule every other connection name is held to.
//
// The two used to be joined with a middle dot, which made a name the
// connection form itself would have refused — and the name becomes a
// directory under the backup root. A parent name long enough to push the
// result past the limit is shortened rather than the database's, which is the
// half that tells the siblings apart; a parent saved under the old rule is
// left out of the name altogether.
func siblingConnectionName(parent, database string) string {
	// Four short of the rule's 64, so the "-2" a second sibling of the same
	// name is given still fits inside it.
	const longest = 60
	name := parent + "." + database
	if over := len(name) - longest; over > 0 && over < len(parent) {
		name = strings.TrimRight(parent[:len(parent)-over], " ._-") + "." + database
	}
	for _, candidate := range []string{name, database, "db." + database} {
		if len(candidate) > longest {
			candidate = candidate[:longest]
		}
		if connNameRe.MatchString(candidate) {
			return candidate
		}
	}
	return "database"
}

// connectionOrigin and afterConnectionForgotten are the two ends of
// forgetting a connection that discovery cares about: where the connection
// came from, read while its row is still there, and the moment the row is
// gone. Discovery fills them in so that a server the sync connected on its own
// is put on its ignore list and does not come back the next time the page
// loads; on their own they do nothing.
func (s *Server) connectionOrigin(ctx context.Context, id int64) string { return "" }

func (s *Server) afterConnectionForgotten(ctx context.Context, id int64, origin, actor string) {}

// dropPoolAfter lets go of a connection's pool when its server refused a
// ping, so the next request dials again instead of reusing dead connections
// for as long as the pool lives.
//
// It does nothing for a ping that failed because the request went away or
// ran out of time. Closing the pool then closes it under every other request
// using it: a ping queued behind a long read on SQLite's single connection
// times out, and the read it was waiting behind is what gets its pool taken
// away mid-catalogue.
func (s *Server) dropPoolAfter(id int64, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	s.modules.dbs.Close(id)
}

// connectError is what a failed dial said, with the connection's password
// taken out of it.
//
// A driver reports a connection string it could not use by quoting it:
// net/url prints the whole URL it failed to parse, and a Redis address that
// is not one comes back inside the dial error, userinfo and all. What it said
// goes to the read surface, and for a refused request into the audit trail,
// and neither is a place a password may be written.
func connectError(dsn string, err error) string {
	text := err.Error()
	for _, secret := range dsnSecrets(dsn) {
		if len(secret) >= 4 {
			text = strings.ReplaceAll(text, secret, "***")
			continue
		}
		// Too short to blank wherever it occurs — a password of "a" would
		// leave no sentence — so only where it stands as a password.
		text = strings.ReplaceAll(text, ":"+secret+"@", ":***@")
		text = strings.ReplaceAll(text, "="+secret, "=***")
	}
	return text
}

// dsnPasswordParam finds a password given as a parameter, the way SQL Server,
// ClickHouse and a key=value Postgres string carry one.
var dsnPasswordParam = regexp.MustCompile(`(?i)\b(?:password|passwd|pwd)\s*=\s*('(?:[^'\\]|\\.)*'|[^&;\s]+)`)

// dsnSecrets are the passwords in a connection string, in every spelling a
// driver might repeat one in, longest first. It reads the string by its shape
// rather than through the engine's parser: the string that matters here is
// the one the parser refused.
func dsnSecrets(dsn string) []string {
	found := map[string]bool{}
	add := func(secret string) {
		secret = strings.Trim(secret, `'"`)
		if secret == "" {
			return
		}
		found[secret] = true
		if plain, err := url.PathUnescape(secret); err == nil {
			found[plain] = true
		}
		if plain, err := url.QueryUnescape(secret); err == nil {
			found[plain] = true
		}
		found[url.QueryEscape(secret)] = true
		found[url.PathEscape(secret)] = true
	}
	rest := dsn
	if _, after, ok := strings.Cut(dsn, "://"); ok {
		rest = after
	}
	// Everything before the last @ is who is connecting, and what follows the
	// first colon in it is the password.
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		if _, password, ok := strings.Cut(rest[:at], ":"); ok {
			add(password)
		}
	}
	for _, m := range dsnPasswordParam.FindAllStringSubmatch(dsn, -1) {
		add(m[1])
	}
	out := make([]string, 0, len(found))
	for secret := range found {
		if secret != "" {
			out = append(out, secret)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// recordRead puts a read on the audit trail by hand. A GET never reaches the
// mutation middleware's record, so the few reads that hand something off the
// server — a password, a table, a dump — write their own, before the read
// runs, so that one which hangs is still on record.
//
// It is written whether or not the client is still there. An entry inserted
// on the request's own context is lost the moment the browser closes the
// connection, and a download abandoned at its last byte is exactly the read
// that must not go unrecorded. A failure is recorded as one: failed is what
// went wrong after the read had begun, nil when it is beginning or ended well.
func (s *Server) recordRead(r *http.Request, action, target string, detail map[string]any, failed error) {
	p := httpx.MustPrincipal(r)
	entry := audit.Entry{
		UserID:   p.UserID(),
		Username: p.Username(),
		Role:     string(p.Role),
		IP:       httpx.ClientIP(r),
		Actor:    p.Kind,
		Action:   action,
		Target:   target,
		Method:   r.Method,
		Path:     r.URL.Path,
		Status:   http.StatusOK,
		Success:  true,
	}
	if failed != nil {
		// What the handler would have answered had the response not already
		// been under way.
		entry.Status, entry.Success = http.StatusBadGateway, false
	}
	entry.Detail = audit.Detail(detail)
	s.Audit.Record(context.WithoutCancel(r.Context()), entry)
}

// --- what the server says it is ---------------------------------------------

// dbIdentityFresh is how long a server's answer about itself is believed
// before it is asked again. A server changes product never and version on an
// upgrade; ten minutes bounds how long a tile can name the release before
// last.
const dbIdentityFresh = 10 * time.Minute

type dbIdentityKept struct {
	dsn      [sha256.Size]byte
	at       time.Time
	identity dbx.Identity
}

// lastIdentity is what a connection's server said it is the last time it
// answered, however long ago, as long as the connection still points where
// it did. It is what names a server that is not answering now.
func (s *Server) lastIdentity(id int64, dsn string) (dbx.Identity, time.Time, bool) {
	v, ok := s.dbConns.identities.Load(id)
	if !ok {
		return dbx.Identity{}, time.Time{}, false
	}
	kept := v.(dbIdentityKept)
	if kept.dsn != sha256.Sum256([]byte(dsn)) {
		return dbx.Identity{}, time.Time{}, false
	}
	return kept.identity, kept.at, true
}

// identityOf is the connection's identity, asked for only when nothing fresh
// is kept. An answer with no version in it is a server that did not say, and
// is not kept: the next asking may do better than the driver's default.
func (s *Server) identityOf(id int64, dsn string, ask func() dbx.Identity) dbx.Identity {
	if kept, at, ok := s.lastIdentity(id, dsn); ok && time.Since(at) < dbIdentityFresh {
		return kept
	}
	identity := ask()
	if identity.Number != "" {
		s.dbConns.identities.Store(id, dbIdentityKept{
			dsn: sha256.Sum256([]byte(dsn)), at: time.Now(), identity: identity,
		})
	}
	return identity
}

// dialConnection reports whether the server behind a connection answers, and
// what it is. It is the whole of what the summary asks of a server: a ping
// and, when nothing fresh is kept, the two or three reads that name it.
func (s *Server) dialConnection(ctx context.Context, conn *dbConnection, dsn string) (dbx.Identity, error) {
	switch conn.Driver {
	case dbx.DriverMongo:
		client, err := dbx.MongoClient(ctx, dsn)
		if err != nil {
			return dbx.Identity{}, err
		}
		defer client.Disconnect(context.Background())
		return s.identityOf(conn.ID, dsn, func() dbx.Identity { return dbx.IdentifyMongo(ctx, client) }), nil
	case dbx.DriverRedis:
		client, err := dbx.RedisClient(ctx, dsn, 0)
		if err != nil {
			return dbx.Identity{}, err
		}
		defer client.Close()
		return s.identityOf(conn.ID, dsn, func() dbx.Identity { return dbx.IdentifyRedis(ctx, client) }), nil
	}
	pool, err := s.modules.dbs.Pool(ctx, conn.ID, conn.Driver, dsn)
	if err == nil {
		err = pool.PingContext(ctx)
	}
	if err != nil {
		s.dropPoolAfter(conn.ID, err)
		return dbx.Identity{}, err
	}
	return s.identityOf(conn.ID, dsn, func() dbx.Identity { return dbx.IdentifySQL(ctx, pool, conn.Driver) }), nil
}

// --- where the server runs --------------------------------------------------

// The words a connection's state is reported in.
const (
	// dbStateRunning: the server answered.
	dbStateRunning = "running"
	// dbStateStopped and dbStatePaused: the container or unit behind the
	// connection is known not to be running, so it was not dialled.
	dbStateStopped = "stopped"
	dbStatePaused  = "paused"
	// dbStateUnreachable: nothing says it is stopped, and it did not answer.
	dbStateUnreachable = "unreachable"
	// dbStateBroken: the saved connection itself cannot be read.
	dbStateBroken = "broken"

	// dbSourceUnknown is where a broken connection's server runs and how far
	// it reaches: its address is inside the DSN that could not be opened.
	dbSourceUnknown = "unknown"
)

// dbContainerRef is the container behind a connection.
type dbContainerRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Image string `json:"image"`
	// State is Docker's word — running, exited, paused, restarting, created,
	// dead — and Status its sentence ("Up 3 hours", "Exited (0) 2 days ago").
	State          string `json:"state"`
	Status         string `json:"status,omitempty"`
	Health         string `json:"health,omitempty"`
	ComposeProject string `json:"composeProject,omitempty"`
	ComposeService string `json:"composeService,omitempty"`
}

// dbUnitRef is the systemd unit behind a connection.
type dbUnitRef struct {
	Name string `json:"name"`
	// ActiveState and SubState are systemd's: active/running,
	// inactive/dead, failed/failed.
	ActiveState string `json:"activeState"`
	SubState    string `json:"subState,omitempty"`
}

// dbFileRef is the file a SQLite connection is.
type dbFileRef struct {
	Path     string     `json:"path"`
	Exists   bool       `json:"exists"`
	Size     int64      `json:"size"`
	Modified *time.Time `json:"modified,omitempty"`
}

// dbPower is which power actions the route would accept right now.
type dbPower struct {
	// Via is what would carry them out: docker, systemd, or empty when
	// nothing here can, in which case Reason says why.
	Via     string `json:"via"`
	Start   bool   `json:"start"`
	Stop    bool   `json:"stop"`
	Restart bool   `json:"restart"`
	Reason  string `json:"reason,omitempty"`
}

// dbPlacement is where a connection's server runs and what state that is in.
type dbPlacement struct {
	// Source is docker, host, file or remote.
	Source    string
	Container *dbContainerRef
	Unit      *dbUnitRef
	File      *dbFileRef
	Exposure  dbExposure
	// Managed reports a port binding the access route can change.
	Managed bool
	// Down is stopped or paused when what is behind the connection is known
	// not to be serving, and empty when it should answer.
	Down string
	// Mark changes whenever the thing behind the connection was restarted or
	// replaced, so a reading taken before is not served after.
	Mark string
	// foreign marks a container found by its port or address alone, whose
	// image is not one of this engine's: the connection goes through it, and
	// whether it is the database or something in front of one is not known.
	foreign bool
	// listening reports a socket on the connection's port, and process the
	// program that holds it where that could be read.
	listening bool
	process   string
	// ambiguous lists the engine's units when more than one could be the
	// stopped server and nothing says which.
	ambiguous []string
}

// dbHostView is the machine as one request read it. A fleet asks where nine
// connections run, and each asking wants the listening sockets, the engine's
// units and what each stopped container publishes: they are read once here
// and shared, as the Docker read snapshot shares its lists. Without that a
// fleet of nine connections to servers that are down lists every unit on the
// machine nine times and inspects every stopped container nine times over.
type dbHostView struct {
	s         *Server
	ctx       context.Context
	probe     dbHostProbe
	systemctl dbSystemctl

	listenersOnce sync.Once
	listeners     []proxysvc.Listener

	unitsOnce sync.Once
	units     []procs.Unit
	unitsErr  error

	// published is what each stopped container's configuration publishes, by
	// container id.
	published sync.Map
}

// dbPublished is one container's published ports, read once.
type dbPublished struct {
	once  sync.Once
	ports []dockerx.PortMapping
}

func (s *Server) newDBHostView(ctx context.Context) *dbHostView {
	view := &dbHostView{s: s, ctx: ctx, probe: hostLogProbe, systemctl: hostSystemctl}
	if s.dbConns.probe != nil {
		view.probe = *s.dbConns.probe
	}
	if s.dbConns.systemctl != nil {
		view.systemctl = *s.dbConns.systemctl
	}
	// The unit list is asked for through the probe by code that takes one, so
	// the sharing is put inside the probe this view hands out.
	if list := view.probe.units; list != nil {
		view.probe.units = func(context.Context) ([]procs.Unit, error) {
			view.unitsOnce.Do(func() { view.units, view.unitsErr = list(view.ctx) })
			return view.units, view.unitsErr
		}
	}
	return view
}

func (v *dbHostView) sockets() []proxysvc.Listener {
	v.listenersOnce.Do(func() {
		v.listeners, _ = v.probe.listeners(v.ctx)
	})
	return v.listeners
}

// publishedBy is the ports a container's configuration publishes, which is
// the only place a stopped container still says them.
func (v *dbHostView) publishedBy(id string) []dockerx.PortMapping {
	kept, _ := v.published.LoadOrStore(id, &dbPublished{})
	read := kept.(*dbPublished)
	read.once.Do(func() {
		if spec, err := v.s.modules.docker.SpecOf(v.ctx, id); err == nil {
			read.ports = spec.Ports
		}
	})
	return read.ports
}

// place finds where a connection's server runs.
//
// In order of how sure each answer is: a running container that publishes or
// answers at the connection's address; the process listening on its port and
// the unit that process belongs to; a container that is not running whose
// configuration publishes the port, or that the connection is named after;
// and last, for a native server that has stopped and left nothing listening
// to follow, the unit it was last seen running under or the engine's only
// unit on the engine's own port.
func (v *dbHostView) place(ctx context.Context, conn *dbConnection, dsn string) dbPlacement {
	out := dbPlacement{Source: "remote", Exposure: exposureRemote}
	if conn.Driver == dbx.DriverSQLite {
		out.Source, out.Exposure, out.File = "file", exposureLocal, fileRef(conn.Database)
		return out
	}
	info, err := dbx.ParseDSN(conn.Driver, dsn)
	if err != nil {
		return out
	}
	loopback := databaseLoopback(info.Host)
	if !loopback && !isContainerAddress(info.Host) {
		return out
	}
	port, _ := strconv.Atoi(info.Port)
	s := v.s

	if server := s.serverBehind(ctx, conn, info); server != nil {
		out.inContainer(server.container)
		out.Exposure = exposureOf(server)
		out.Managed = out.Container.ComposeProject == "" && server.binding != nil
		return out
	}
	if loopback {
		// A server in an image detection does not know still publishes its
		// port, and the connection already says which engine it is.
		if c, binding := s.runningContainerPublishing(ctx, port); c != nil {
			out.inContainer(c)
			out.foreign = !s.engineImage(ctx, conn, c)
			out.Exposure = exposureOf(&dbServer{container: c, binding: binding})
			return out
		}
	} else if c := s.containerAt(ctx, info.Host); c != nil {
		out.inContainer(c)
		out.foreign = !s.engineImage(ctx, conn, c)
		out.Exposure = exposureLocal
		return out
	}

	var listening *proxysvc.Listener
	if loopback && port > 0 {
		listening = listenerOn(v.sockets(), port)
	}
	if listening != nil && listening.Manager == "container" && s.modules.docker != nil {
		// A container on the host's own network: from the socket table it is
		// a native server, and its cgroup says otherwise.
		if d, err := s.modules.docker.Inspect(ctx, listening.ManagerName); err == nil {
			out.inContainer(&d.Container)
			out.foreign = !s.engineImage(ctx, conn, &d.Container)
			out.Exposure = listenerExposure(v.sockets(), port)
			return out
		}
	}
	if listening == nil {
		if c, exposure := v.stoppedContainerFor(ctx, conn, loopback, port); c != nil {
			out.inContainer(c)
			out.Exposure = exposure
			return out
		}
	}
	if !loopback {
		// A private address nothing here answers at: a server on the LAN or a
		// VPN, which is somebody else's to run.
		return out
	}

	out.Source, out.Exposure = "host", exposureLocal
	if listening != nil {
		out.Exposure, out.listening, out.process = listenerExposure(v.sockets(), port), true, listening.Process
		// The unit is the server's only when it is named for the engine. What
		// listens on a database's port is not always the database: an ssh
		// tunnel to one elsewhere is ssh.service, a published container's
		// docker-proxy is docker.service, and a pooler in front of it is its
		// own unit. Offering to stop "the unit behind this connection" there
		// would be offering to stop sshd.
		if listening.Manager == "systemd" && engineUnit(listening.ManagerName, engineUnitPrefixes(conn.Driver)) {
			out.Unit = &dbUnitRef{Name: listening.ManagerName, ActiveState: "active", SubState: "running"}
			out.Mark = listening.ManagerName + "@" + strconv.Itoa(int(listening.PID))
			s.dbConns.units.Store(conn.ID, listening.ManagerName)
		}
		return out
	}
	// Nothing is listening. A unit is taken for this connection's server only
	// on evidence, because what is decided here is what Start starts: the
	// unit the connection was last seen running under, or the engine's only
	// unit when the connection is to the engine's own port. A connection to
	// some other port that nothing answers on is a tunnel that has dropped or
	// a container that was removed, and the PostgreSQL installed on the
	// machine has nothing to do with it.
	remembered, seen := s.dbConns.units.Load(conn.ID)
	ownPort := port > 0 && port == driverLabels[conn.Driver].port
	if !seen && !ownPort {
		return out
	}
	units := engineUnits(ctx, conn.Driver, port, v.sockets(), v.probe)
	var found *procs.Unit
	for i := range units {
		if units[i].Name == remembered {
			found = &units[i]
		}
	}
	if found == nil && ownPort {
		switch {
		case len(units) == 1:
			found = &units[0]
		case len(units) > 1:
			for _, u := range units {
				out.ambiguous = append(out.ambiguous, u.Name)
			}
		}
	}
	if found != nil {
		out.Unit = &dbUnitRef{Name: found.Name, ActiveState: found.ActiveState, SubState: found.SubState}
		if found.ActiveState != "active" && found.ActiveState != "activating" {
			out.Down = dbStateStopped
		}
	}
	return out
}

func (p *dbPlacement) inContainer(c *dockerx.Container) {
	p.Source = "docker"
	p.Container = &dbContainerRef{
		ID: c.ID, Name: c.Name, Image: c.Image, State: c.State, Status: c.Status, Health: c.Health,
		ComposeProject: c.Labels["com.docker.compose.project"],
		ComposeService: c.Labels["com.docker.compose.service"],
	}
	started := ""
	if c.StartedAt != nil {
		started = c.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	p.Mark = c.ID + "@" + started + "@" + c.State
	switch c.State {
	case "running":
	case "paused":
		// Not dialled either: a paused container accepts the connection and
		// never answers it, which is a timeout rather than a refusal.
		p.Down = dbStatePaused
	case "restarting":
		// Between attempts; whether it answers is worth finding out.
	default:
		p.Down = dbStateStopped
	}
}

// power is which actions the power route would accept for a placement.
// systemctl says whether there is one to carry a unit's out.
func (p dbPlacement) power(conn *dbConnection, systemctl bool) dbPower {
	switch {
	case p.Container != nil && p.foreign:
		// The same caution as for a unit: a proxy or a pooler publishing the
		// database's port is not the database, and may be publishing a good
		// deal else besides.
		return dbPower{Reason: fmt.Sprintf("The connection is answered by the container %s, whose image (%s) is not one of this engine's that the dashboard recognises. Start and stop it on the Docker page.",
			p.Container.Name, p.Container.Image)}
	case p.Container != nil:
		running := p.Down == ""
		return dbPower{Via: "docker", Start: !running && p.Down != dbStatePaused, Stop: running || p.Down == dbStatePaused, Restart: running}
	case p.Unit != nil:
		if !systemctl {
			return dbPower{Reason: fmt.Sprintf("%s runs under %s, and systemctl cannot be reached from this dashboard", conn.Name, p.Unit.Name)}
		}
		running := p.Down == ""
		return dbPower{Via: "systemd", Start: !running, Stop: running, Restart: running}
	case p.Source == "file":
		return dbPower{Reason: "A SQLite database is a file; there is no server to start or stop."}
	case p.Source == "remote":
		return dbPower{Reason: fmt.Sprintf("The server is on another machine (%s) and is started and stopped there.", conn.Host)}
	case len(p.ambiguous) > 0:
		return dbPower{Reason: fmt.Sprintf("Nothing is listening on port %s, and more than one unit could be this server (%s). Start the right one on the Services page.",
			conn.Port, strings.Join(p.ambiguous, ", "))}
	case p.process != "":
		return dbPower{Reason: fmt.Sprintf("Port %s is served by %s, which no unit of this engine's runs, so there is nothing here it would be safe to stop or restart.", conn.Port, p.process)}
	case p.listening:
		return dbPower{Reason: fmt.Sprintf("The process listening on port %s could not be identified, so there is nothing here to stop or restart it with.", conn.Port)}
	}
	return dbPower{Reason: fmt.Sprintf("No container or systemd unit on this machine was found serving port %s, so there is nothing here to start or stop it with.", conn.Port)}
}

// notNow is why an action is not one the power route will carry out in the
// state the server is in, where it could in another.
func (p dbPlacement) notNow(action string) string {
	target := ""
	switch {
	case p.Container != nil:
		target = "the container " + p.Container.Name
	case p.Unit != nil:
		target = p.Unit.Name
	}
	switch {
	case p.Down == dbStatePaused:
		return fmt.Sprintf("%s is paused, and a paused container can only be stopped from here. Resume it on the Docker page, or stop it and start it again.", target)
	case p.Down == "" && action == "start":
		return fmt.Sprintf("%s is already running.", target)
	case action == "restart":
		return fmt.Sprintf("%s is not running, so there is nothing to restart. Start it instead.", target)
	}
	return fmt.Sprintf("%s is not running.", target)
}

func fileRef(path string) *dbFileRef {
	ref := &dbFileRef{Path: path}
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		modified := st.ModTime().UTC()
		ref.Exists, ref.Size, ref.Modified = true, st.Size(), &modified
	}
	return ref
}

// engineImage reports a container whose image is one of the connection's
// engine's, by the table detection reads.
func (s *Server) engineImage(ctx context.Context, conn *dbConnection, c *dockerx.Container) bool {
	cand, _ := dbx.Detect(c.Name, s.containerImage(ctx, *c), nil, nil, nil)
	return cand != nil && cand.Driver == conn.Driver
}

// runningContainerPublishing is the running container that publishes a host
// port, whatever its image, with the binding that does it.
func (s *Server) runningContainerPublishing(ctx context.Context, port int) (*dockerx.Container, *dockerx.Port) {
	if s.modules.docker == nil || port <= 0 {
		return nil, nil
	}
	containers, err := s.modules.docker.ListContainers(ctx, false)
	if err != nil {
		return nil, nil
	}
	for i := range containers {
		for j := range containers[i].Ports {
			p := &containers[i].Ports[j]
			// The v4 binding, as serverBehind reads it: Docker lists the v6
			// twin beside it and the saved DSN dials the v4 one.
			if int(p.PublicPort) == port && !strings.Contains(p.IP, ":") {
				return &containers[i], p
			}
		}
	}
	return nil, nil
}

// stoppedContainerFor finds the container behind a connection when it is not
// running, which is when nothing about it shows in a port list: Docker
// reports the bindings of running containers only. The configuration still
// says which host port it publishes, so each stopped container of the
// connection's engine is asked for its own. A connection made to a
// container's own address has no published port to match, and a stopped
// container has no address either; there the container the connection is
// named after is the one, since adopting a container names the connection
// after it.
func (v *dbHostView) stoppedContainerFor(ctx context.Context, conn *dbConnection, loopback bool, port int) (*dockerx.Container, dbExposure) {
	s := v.s
	if s.modules.docker == nil {
		return nil, ""
	}
	all, err := s.modules.docker.ListContainers(ctx, true)
	if err != nil {
		return nil, ""
	}
	var named *dockerx.Container
	for i := range all {
		c := &all[i]
		if c.State == "running" || c.State == "paused" {
			continue
		}
		image := s.containerImage(ctx, *c)
		if cand, _ := dbx.Detect(c.Name, image, nil, nil, nil); cand == nil || cand.Driver != conn.Driver {
			continue
		}
		if loopback && port > 0 {
			// The engine's own port inside the container, from the table
			// detection reads.
			if internal, _ := dbx.Detect(c.Name, image, nil, nil, []string{"container"}); internal != nil {
				for _, p := range v.publishedBy(c.ID) {
					if p.HostPort == port && p.ContainerPort == internal.Port {
						return c, bindingExposure(p.HostIP)
					}
				}
			}
		}
		if !loopback && c.Name == conn.Name && named == nil {
			named = c
		}
	}
	if named != nil {
		return named, exposureLocal
	}
	return nil, ""
}

// bindingExposure reads a host address a port is published on the way
// exposureOf reads a live binding.
func bindingExposure(hostIP string) dbExposure {
	switch hostIP {
	case "", "0.0.0.0", "::", "[::]", "*":
		return exposurePublic
	}
	if ip := net.ParseIP(strings.Trim(hostIP, "[]")); ip != nil && ip.IsLoopback() {
		return exposureLocal
	}
	return exposurePrivate
}

// listenerExposure is how far a native server's sockets on a port reach: the
// widest of them, since a server bound to loopback and to every interface is
// reachable from every interface.
func listenerExposure(listeners []proxysvc.Listener, port int) dbExposure {
	exposure := exposureLocal
	for _, l := range listeners {
		if l.Protocol != "tcp" || int(l.Port) != port {
			continue
		}
		switch l.Scope {
		case proxysvc.ScopeAll:
			return exposurePublic
		case proxysvc.ScopeInterface:
			exposure = exposurePrivate
		}
	}
	return exposure
}

// --- one connection's summary -----------------------------------------------

type dbConnSummary struct {
	*dbConnection
	// Flavor is the product behind the driver, and FlavorLabel its name.
	// Until the server has answered once it is the driver's own.
	Flavor        string `json:"flavor"`
	FlavorLabel   string `json:"flavorLabel"`
	Version       string `json:"version,omitempty"`
	VersionNumber string `json:"versionNumber,omitempty"`
	// State is running, stopped, paused, unreachable or broken.
	State     string `json:"state"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	LatencyMs int64  `json:"latencyMs"`
	// Source is where the server is: docker, host, file or remote, and
	// unknown for a connection that cannot be read.
	Source    string          `json:"source"`
	Container *dbContainerRef `json:"container,omitempty"`
	Unit      *dbUnitRef      `json:"unit,omitempty"`
	File      *dbFileRef      `json:"file,omitempty"`
	Power     dbPower         `json:"power"`
	// Exposure is how far the server's port reaches, as far as its binding
	// says — local, public, private or remote, and unknown beside an unknown
	// source — and Managed whether the access route can change that.
	Exposure dbExposure `json:"exposure"`
	Managed  bool       `json:"managed"`
	// Consumers is how many deployment environments are bound to it.
	Consumers  int        `json:"consumers"`
	LastBackup *time.Time `json:"lastBackup,omitempty"`
	// Capabilities is the feature reading for this driver and flavour, the
	// same object the driver catalogue serves.
	Capabilities map[string]any `json:"capabilities"`
	CheckedAt    time.Time      `json:"checkedAt"`
}

// handleDBConnSummary is one connection's reading.
//
// It answers 200 whatever the server is doing. A connection that is stopped,
// unreachable or cannot even be opened is still a connection the page has to
// draw — that is when its page is opened — so those are states in the answer
// rather than errors, and only a connection that does not exist is a 404.
//
// The cost is bounded: one read of the Docker list, the listening sockets
// only where no container answers for the address, and one ping of this
// connection's server with a few seconds to answer, skipped altogether when
// its container or unit is known to be down.
func (s *Server) handleDBConnSummary(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, dsn, err := s.storedConnectionDSN(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 12*time.Second)
	defer cancel()
	ctx = s.modules.docker.WithReadSnapshot(ctx)

	flavor := dbx.DefaultFlavor(conn.Driver)
	out := dbConnSummary{
		dbConnection: conn, Flavor: flavor, FlavorLabel: dbx.FlavorLabel(flavor),
		State: dbStateBroken, Source: dbSourceUnknown, Exposure: dbSourceUnknown,
		Capabilities: dbx.Capabilities(conn.Driver, flavor), CheckedAt: time.Now().UTC(),
	}
	_ = s.Store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM deploy_database_bindings WHERE connection_id = ?`, id).Scan(&out.Consumers)
	out.LastBackup = s.newestDump(conn.Name)
	if conn.Broken {
		out.Error = conn.BrokenReason
		out.Power = dbPower{Reason: "The saved connection cannot be read, so where its server runs is not known."}
		httpx.JSON(w, http.StatusOK, out)
		return nil
	}

	view := s.newDBHostView(ctx)
	place := view.place(ctx, conn, dsn)
	out.Source, out.Container, out.Unit, out.File = place.Source, place.Container, place.Unit, place.File
	out.Exposure, out.Managed = place.Exposure, place.Managed
	out.Power = place.power(conn, view.systemctl.available())

	identity, _, known := s.lastIdentity(id, dsn)
	if place.Down != "" {
		out.State = place.Down
	} else {
		dialCtx, cancelDial := context.WithTimeout(ctx, 6*time.Second)
		started := time.Now()
		answered, err := s.dialConnection(dialCtx, conn, dsn)
		cancelDial()
		out.LatencyMs = time.Since(started).Milliseconds()
		if err != nil {
			out.State, out.Error = dbStateUnreachable, connectError(dsn, err)
		} else {
			out.State, out.OK = dbStateRunning, true
			identity, known = answered, true
		}
		if place.File != nil {
			// Read again: SQLite creates a file that was not there when it is
			// first opened, and the reading taken before the dial said so.
			out.File = fileRef(place.File.Path)
		}
	}
	if known {
		out.Flavor, out.FlavorLabel = identity.Flavor, dbx.FlavorLabel(identity.Flavor)
		out.Version, out.VersionNumber = identity.Version, identity.Number
		out.Capabilities = dbx.Capabilities(conn.Driver, identity.Flavor)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// --- power ------------------------------------------------------------------

type dbPowerRequest struct {
	Action string `json:"action"`
	// TimeoutSeconds is how long a container is given to shut down before it
	// is killed; zero is dbStopGrace.
	TimeoutSeconds int `json:"timeoutSeconds"`
}

// dbStopGrace is how long a database's container is given to shut down when
// it is stopped or restarted from here, and dbStopGraceLimit the most a
// request may ask for instead.
//
// Docker's own default is ten seconds and then SIGKILL. That is enough for a
// web server and not for a database: a Redis with a few gigabytes answers
// SIGTERM by writing its snapshot, a MySQL by flushing its buffer pool, and
// one killed part-way through loses what it had not written or spends its
// next start in crash recovery. The Docker page stops a container; this
// route stops a database, and waits for one.
const (
	dbStopGrace      = 90 * time.Second
	dbStopGraceLimit = 10 * time.Minute
)

// handleDBPower starts, stops or restarts the server behind a connection.
//
// It does what the Docker page's start, stop and restart do for a container
// and what the Services page's do for a unit, from the place the operator is
// already looking at the database. Like them it takes no typed phrase: each
// is undone by the next. A compose-owned container is not refused, because
// the Docker page does not refuse to stop one either — it is recreating one
// that compose forbids, and nothing here recreates.
//
// What it will not do is guess. A connection to another machine, a file, or
// a port nothing on this machine can be tied to is refused with the reason
// the summary already gave, and so is an action the summary does not offer
// for the state the server is in.
func (s *Server) handleDBPower(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req dbPowerRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	var action dockerx.LifecycleAction
	switch req.Action {
	case "start":
		action = dockerx.ActionStart
	case "stop":
		action = dockerx.ActionStop
	case "restart":
		action = dockerx.ActionRestart
	default:
		return httpx.BadRequest("action must be start, stop or restart")
	}
	grace := dbStopGrace
	if req.TimeoutSeconds != 0 {
		if req.TimeoutSeconds < 0 || req.TimeoutSeconds > int(dbStopGraceLimit.Seconds()) {
			return httpx.BadRequest("timeoutSeconds must be between 1 and %d", int(dbStopGraceLimit.Seconds()))
		}
		grace = time.Duration(req.TimeoutSeconds) * time.Second
	}
	if action != dockerx.ActionStart {
		p := httpx.MustPrincipal(r)
		if !p.Can(auth.CapDestructive) {
			return httpx.Err(http.StatusForbidden, "forbidden",
				"stopping or restarting a database interrupts whatever is using it, and your role does not permit it")
		}
		if !s.destrLim.Allow(p.Username() + "|dbpower") {
			return httpx.Err(http.StatusTooManyRequests, "rate_limited", "too many stops and restarts, slow down")
		}
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	// The grace and then the time the action itself takes.
	ctx, cancel := timeoutCtx(r, grace+time.Minute)
	defer cancel()

	view := s.newDBHostView(ctx)
	place := view.place(ctx, conn, dsn)
	power := place.power(conn, view.systemctl.available())
	unavailable := power.Reason
	if power.Via != "" {
		offered := map[string]bool{"start": power.Start, "stop": power.Stop, "restart": power.Restart}
		if !offered[req.Action] {
			unavailable = place.notNow(req.Action)
		}
	}
	if unavailable != "" {
		httpx.SetAudit(r, "database.power."+req.Action, conn.Name, map[string]any{"refused": true})
		return httpx.Err(http.StatusConflict, "power_unavailable", unavailable)
	}
	detail := map[string]any{"via": power.Via}
	result := map[string]any{"action": req.Action, "via": power.Via}
	switch power.Via {
	case "docker":
		detail["container"], detail["id"] = place.Container.Name, place.Container.ID
		var timeout *int
		if action != dockerx.ActionStart {
			seconds := int(grace.Seconds())
			timeout, detail["timeoutSeconds"] = &seconds, seconds
		}
		if err := s.modules.docker.Lifecycle(ctx, place.Container.ID, action, timeout); err != nil {
			detail["error"] = err.Error()
			httpx.SetAudit(r, "database.power."+req.Action, conn.Name, detail)
			return s.dockerErr(err)
		}
		result["target"] = place.Container.Name
		if after, err := s.modules.docker.Inspect(ctx, place.Container.ID); err == nil {
			result["state"] = after.State
		}
	case "systemd":
		detail["unit"] = place.Unit.Name
		if err := serviceUnit(place.Unit.Name); err != nil {
			detail["error"] = err.Error()
			httpx.SetAudit(r, "database.power."+req.Action, conn.Name, detail)
			return httpx.Err(http.StatusConflict, "power_unavailable", err.Error())
		}
		if output, err := view.systemctl.run(ctx, req.Action, place.Unit.Name); err != nil {
			detail["error"] = firstLine(output, err)
			httpx.SetAudit(r, "database.power."+req.Action, conn.Name, detail)
			return httpx.Err(http.StatusBadGateway, "power_failed",
				fmt.Sprintf("systemctl %s %s failed: %s", req.Action, place.Unit.Name, firstLine(output, err)))
		}
		result["target"] = place.Unit.Name
		// is-active exits non-zero for every state but active and still
		// prints the state, which is the answer wanted.
		state, _ := view.systemctl.run(ctx, "is-active", place.Unit.Name)
		result["state"] = strings.TrimSpace(state)
	}
	// What was kept describes a server that has just been stopped or
	// replaced by a fresh process: the pool's connections are dead, and the
	// last reading says it answers.
	s.modules.dbs.Close(id)
	s.dbConns.stale(id)
	httpx.SetAudit(r, "database.power."+req.Action, conn.Name, detail)
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

// dbSystemctl is the host's systemctl as the power route uses it: whether
// there is one, and one verb carried out on one unit, with what it printed.
// It is a value rather than two functions called directly so that a test can
// stand in for the only code in these routes that runs a command on the host.
type dbSystemctl struct {
	available func() bool
	run       func(ctx context.Context, verb, unit string) (string, error)
}

var hostSystemctl = dbSystemctl{
	available: func() bool { return hostexec.Available("systemctl") },
	run:       runUnit,
}

// serviceUnit refuses anything that is not the name of a service unit.
func serviceUnit(unit string) error {
	if err := procs.ValidateName(unit); err != nil || !strings.HasSuffix(unit, ".service") {
		return fmt.Errorf("%q is not a service unit name", unit)
	}
	return nil
}

// unitCommand is the systemctl invocation for one verb on one unit, on the
// host: an argument vector, never a shell line. The verb is one of four the
// handler chose and the unit is validated here, so nothing a request carried
// reaches systemctl as anything but a name.
func unitCommand(ctx context.Context, verb, unit string) (*exec.Cmd, error) {
	if err := serviceUnit(unit); err != nil {
		return nil, err
	}
	cmd := hostexec.CommandOnHost(ctx, "systemctl", verb, unit)
	// The dashboard shares the host's PID namespace and not its root, which
	// systemd reads as a chroot and answers by ignoring the command.
	cmd.Env = append(cmd.Environ(), "SYSTEMD_IGNORE_CHROOT=1")
	return cmd, nil
}

func runUnit(ctx context.Context, verb, unit string) (string, error) {
	cmd, err := unitCommand(ctx, verb, unit)
	if err != nil {
		return "", err
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// firstLine is what a failed command said, or the error where it said nothing.
func firstLine(output string, err error) string {
	for _, line := range strings.Split(output, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return err.Error()
}
