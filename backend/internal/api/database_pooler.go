package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/portalloc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/docker/docker/errdefs"
)

// The connection pooler: a PgBouncer container the dashboard runs beside a
// PostgreSQL container.
//
// It is the database's, not a separate thing to look after. It shares a
// private network with the database and reaches it by the container's name;
// it is published on the host exactly where the database is (loopback unless
// the access switch has opened the database up, and then by that same switch,
// audited, because the switch recreates the pooler with it); a deployment
// reaches it on its environment network as db-<id>-pool.jd.internal; and it
// starts, stops and goes away with the database.
//
// Nothing about it is edited by hand. The two files PgBouncer reads are
// written from the settings and the connection's own credentials, and a
// digest of them is kept so that a password that has changed since is written
// again (poolerKeeper).

const (
	// poolerLabel marks the container and the network as a connection's
	// pooler, and names the connection, so neither is ever taken for
	// something an operator made.
	poolerLabel = "io.just-dashboard.database-pooler"
	// poolerPreferredPort is where a pooler is published when the host port is
	// free: PgBouncer's own.
	poolerPreferredPort = dbx.PoolerListenPort
)

func poolerDNSName(id int64) string { return fmt.Sprintf("db-%d-pool.jd.internal", id) }

func poolerNetworkName(id int64) string { return fmt.Sprintf("jd-pool-%d", id) }

// poolerSecrets are the two accounts PgBouncer owns: the one it looks
// passwords up as (auth_query mode) and the read-only one the dashboard reads
// its statistics as.
type poolerSecrets struct {
	Auth  string `json:"auth"`
	Stats string `json:"stats"`
}

// poolerRecord is a connection's pooler as stored.
type poolerRecord struct {
	ConnectionID           int64
	Container, ContainerID string
	Network                string
	HostPort               int
	ServerName             string
	ServerPort             int
	Settings               dbx.PoolerSettings
	Secrets                poolerSecrets
	ConfigSHA              string
	CreatedAt, UpdatedAt   time.Time
}

const poolerColumns = `connection_id, container_name, container_id, network_name, host_port, server_name, server_port,
	pool_mode, pool_size, max_client_conn, auth_mode, secrets_enc, config_sha, created_at, updated_at`

// poolerRecord is nil, without an error, for a connection that has no pooler.
func (s *Server) poolerRecord(ctx context.Context, id int64) (*poolerRecord, error) {
	var (
		rec                  poolerRecord
		sealed               string
		created, updated     int64
		mode, auth           string
		poolSize, maxClients int
	)
	err := s.Store.DB.QueryRowContext(ctx, `SELECT `+poolerColumns+` FROM db_poolers WHERE connection_id = ?`, id).Scan(
		&rec.ConnectionID, &rec.Container, &rec.ContainerID, &rec.Network, &rec.HostPort, &rec.ServerName, &rec.ServerPort,
		&mode, &poolSize, &maxClients, &auth, &sealed, &rec.ConfigSHA, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, httpx.Internal(err)
	}
	plain, err := s.Sealer.Open(sealed)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	if err := json.Unmarshal([]byte(plain), &rec.Secrets); err != nil {
		return nil, httpx.Internal(err)
	}
	rec.Settings = dbx.PoolerSettings{PoolMode: mode, PoolSize: poolSize, MaxClientConn: maxClients, AuthMode: auth}
	rec.CreatedAt, rec.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	return &rec, nil
}

func (s *Server) savePoolerRecord(ctx context.Context, rec *poolerRecord) error {
	raw, err := json.Marshal(rec.Secrets)
	if err != nil {
		return httpx.Internal(err)
	}
	sealed, err := s.Sealer.Seal(string(raw))
	if err != nil {
		return httpx.Internal(err)
	}
	_, err = s.Store.DB.ExecContext(ctx, `INSERT INTO db_poolers(`+poolerColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(connection_id) DO UPDATE SET container_name=excluded.container_name, container_id=excluded.container_id,
		  network_name=excluded.network_name, host_port=excluded.host_port, server_name=excluded.server_name,
		  server_port=excluded.server_port, pool_mode=excluded.pool_mode, pool_size=excluded.pool_size,
		  max_client_conn=excluded.max_client_conn, auth_mode=excluded.auth_mode, secrets_enc=excluded.secrets_enc,
		  config_sha=excluded.config_sha, updated_at=excluded.updated_at`,
		rec.ConnectionID, rec.Container, rec.ContainerID, rec.Network, rec.HostPort, rec.ServerName, rec.ServerPort,
		rec.Settings.PoolMode, rec.Settings.PoolSize, rec.Settings.MaxClientConn, rec.Settings.AuthMode,
		sealed, rec.ConfigSHA, rec.CreatedAt.Unix(), rec.UpdatedAt.Unix())
	if err != nil {
		return httpx.Internal(err)
	}
	return nil
}

// poolerFiles renders the two files from the record and the connection's
// current credentials, with the digest that says whether the container holds
// them already.
func poolerFiles(rec *poolerRecord, dsn string) (ini, userlist, sha string, err error) {
	info, err := dbx.ParseDSN(dbx.DriverPostgres, dsn)
	if err != nil {
		return "", "", "", err
	}
	cfg := dbx.PoolerConfig{
		Settings: rec.Settings, Server: rec.ServerName, ServerPort: rec.ServerPort, Database: info.Database,
		User: info.User, Password: info.Password,
		AuthPassword: rec.Secrets.Auth, StatsPassword: rec.Secrets.Stats,
	}
	if ini, err = cfg.INI(); err != nil {
		return "", "", "", err
	}
	if userlist, err = cfg.Userlist(); err != nil {
		return "", "", "", err
	}
	digest := sha256.Sum256([]byte(ini + "\x00" + userlist))
	return ini, userlist, hex.EncodeToString(digest[:]), nil
}

// poolerTarget is the database container a pooler would sit beside.
type poolerTarget struct {
	server *dbServer
	detail *dockerx.ContainerDetail
}

// poolerSupported reports whether the engine behind a connection is one a
// pooler is offered to, by the capability table, so a fork that speaks the
// protocol and not the pooler's is excluded the way every other feature
// excludes it.
func (s *Server) poolerSupported(ctx context.Context, conn *dbConnection, dsn string) bool {
	flavor := dbx.DefaultFlavor(conn.Driver)
	if kept, _, ok := s.lastIdentity(conn.ID, dsn); ok {
		flavor = kept.Flavor
	} else if answered, err := s.dialConnection(ctx, conn, dsn); err == nil {
		flavor = answered.Flavor
	}
	on, _ := dbx.Capabilities(conn.Driver, flavor)["pooler"].(bool)
	return on
}

const poolerUnsupported = "a connection pooler is for PostgreSQL servers"

// poolerTarget finds the container behind a connection and refuses the ones a
// pooler cannot sit beside, with the reason.
func (s *Server) poolerTarget(ctx context.Context, conn *dbConnection, dsn string) (*poolerTarget, error) {
	if !s.poolerSupported(ctx, conn, dsn) {
		return nil, httpx.BadRequest("%s", poolerUnsupported)
	}
	if s.modules.docker == nil {
		return nil, httpx.Err(http.StatusServiceUnavailable, "docker_unavailable", "this host has no Docker socket, so a pooler cannot be run")
	}
	info, err := dbx.ParseDSN(conn.Driver, dsn)
	if err != nil {
		return nil, httpx.BadRequest("the saved connection could not be read")
	}
	server := s.serverBehind(ctx, conn, info)
	if server == nil {
		return nil, httpx.BadRequest("no running container on this server publishes %s; a pooler is run beside a database container", conn.Name)
	}
	if project := server.container.Labels["com.docker.compose.project"]; project != "" {
		return nil, httpx.Err(http.StatusConflict, "compose_managed",
			fmt.Sprintf("%s belongs to the compose project %s; add a PgBouncer service to its compose file instead", server.container.Name, project))
	}
	detail, err := s.modules.docker.Inspect(ctx, server.container.ID)
	if err != nil {
		return nil, s.dockerErr(err)
	}
	if detail.NetworkMode == "host" || detail.NetworkMode == "none" || strings.HasPrefix(detail.NetworkMode, "container:") {
		return nil, httpx.BadRequest("%s is not on a bridge network, so a pooler cannot reach it", server.container.Name)
	}
	return &poolerTarget{server: server, detail: detail}, nil
}

// poolerChange says what ensurePooler did, for the audit entry and the reply.
type poolerChange struct {
	Created, Recreated, Reloaded, Restarted bool
	// Bind is the host address the pooler is published on afterwards.
	Bind        string
	Firewall    string
	FirewallErr error
}

// poolerLoopback is where a pooler is published unless the access switch has
// said otherwise.
const poolerLoopback = "127.0.0.1"

// poolerPublishAddress is the host address a pooler is published on. Only the
// access switch names one (requested); anything else keeps the address the
// container already has, and a container being made, or made again, is on
// loopback. Opening the database to anywhere is the operator's audited act,
// and nothing that merely runs the pooler may make it theirs.
func poolerPublishAddress(current, requested string) string {
	switch {
	case requested != "":
		return requested
	case current != "":
		return current
	}
	return poolerLoopback
}

// ensurePooler makes the machine match the record: the network exists and the
// database is on it, the container exists and is published on bind (see
// poolerPublishAddress), and holds the current files. It is the whole of
// creating a pooler and of changing one, and running it again changes nothing.
//
// A container whose binding differs from bind is replaced rather
// than edited, since Docker cannot move a published port. A changed setting or
// password is a reload, which keeps the clients; only a change of how clients
// authenticate (restart) is a restart, because PgBouncer keeps the accounts it
// loaded from the userlist through a reload and then asks them the wrong way.
func (s *Server) ensurePooler(ctx context.Context, r *http.Request, t *poolerTarget, rec *poolerRecord, dsn, bind string, restart bool) (poolerChange, error) {
	var change poolerChange
	docker := s.modules.docker
	rec.ServerName, rec.ServerPort = strings.TrimPrefix(t.detail.Name, "/"), t.server.port
	ini, userlist, sha, err := poolerFiles(rec, dsn)
	if err != nil {
		return change, httpx.BadRequest("%v", err)
	}
	if err := s.ensurePoolerNetwork(ctx, rec); err != nil {
		return change, err
	}
	if !onNetwork(t.detail, rec.Network) {
		if err := docker.ConnectNetwork(ctx, rec.Network, t.detail.ID, nil); err != nil {
			return change, s.dockerErr(err)
		}
	}
	existing, err := docker.Inspect(ctx, rec.Container)
	switch {
	case errdefs.IsNotFound(err):
		existing = nil
	case err != nil:
		return change, s.dockerErr(err)
	case existing.Labels[poolerLabel] != strconv.FormatInt(rec.ConnectionID, 10):
		return change, httpx.Err(http.StatusConflict, "container_exists",
			fmt.Sprintf("a container named %s already exists and is not this database's pooler; remove or rename it first", rec.Container))
	}
	current := ""
	if existing != nil {
		spec, err := docker.SpecOf(ctx, existing.ID)
		if err != nil {
			return change, s.dockerErr(err)
		}
		current, _ = poolerBinding(spec)
	}
	change.Bind = poolerPublishAddress(current, bind)
	if existing != nil && current != change.Bind {
		if err := docker.RemoveContainer(ctx, existing.ID, true, false); err != nil {
			return change, s.dockerErr(err)
		}
		existing, change.Recreated = nil, true
	}
	databaseUp := t.detail.State == "running"
	if existing == nil {
		port := rec.HostPort
		if port == 0 || portalloc.Available(change.Bind, "tcp", port) != nil {
			if port, err = freeHostPort(poolerPreferredPort, change.Bind); err != nil {
				return change, httpx.BadRequest("%v", err)
			}
		}
		created, err := docker.Create(ctx, poolerSpec(rec, port, change.Bind), nil)
		if err != nil {
			return change, httpx.BadRequest("could not start the pooler: %v", err)
		}
		rec.HostPort, rec.ContainerID = port, created.ID
		change.Created = !change.Recreated
	} else {
		rec.ContainerID = existing.ID
	}
	if err := writePoolerFiles(ctx, docker, rec.ContainerID, ini, userlist); err != nil {
		return change, s.dockerErr(err)
	}
	switch {
	case existing != nil && existing.State == "running" && restart:
		if err := docker.Lifecycle(ctx, rec.ContainerID, dockerx.ActionRestart, nil); err != nil {
			return change, s.dockerErr(err)
		}
		change.Restarted = true
	case existing != nil && existing.State == "running":
		if err := docker.Signal(ctx, rec.ContainerID, "SIGHUP"); err != nil {
			return change, s.dockerErr(err)
		}
		change.Reloaded = true
	case databaseUp:
		if err := docker.Lifecycle(ctx, rec.ContainerID, dockerx.ActionStart, nil); err != nil {
			return change, s.dockerErr(err)
		}
	}
	rec.ConfigSHA, rec.UpdatedAt = sha, time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = rec.UpdatedAt
	}
	if err := s.savePoolerRecord(ctx, rec); err != nil {
		return change, err
	}
	if public := change.Bind == "0.0.0.0"; r != nil && (public || change.Recreated) {
		change.Firewall, change.FirewallErr = s.setDBFirewall(ctx, r, rec.HostPort, public)
	}
	return change, nil
}

// poolerSpec is the container: PgBouncer's binary run directly, since the
// image's entrypoint would write its configuration from environment variables
// and leave the password where `docker inspect` shows it. The two files are
// copied in before it starts.
func poolerSpec(rec *poolerRecord, hostPort int, bindIP string) dockerx.ContainerSpec {
	return dockerx.ContainerSpec{
		Name:          rec.Container,
		Image:         dbx.PoolerImage,
		Entrypoint:    []string{"/usr/bin/pgbouncer"},
		Command:       []string{dbx.PoolerConfigPath},
		RestartPolicy: "unless-stopped",
		Ports:         []dockerx.PortMapping{{HostIP: bindIP, HostPort: hostPort, ContainerPort: dbx.PoolerListenPort, Protocol: "tcp"}},
		Networks:      []string{rec.Network},
		Labels:        []dockerx.LabelSpec{{Name: poolerLabel, Value: strconv.FormatInt(rec.ConnectionID, 10)}},
		Logging:       dockerx.CappedLogging(),
		// PgBouncer listens above 1024 as an unprivileged account and opens
		// sockets: it needs no capability, and a pooler with a thousand
		// clients is a few tens of megabytes.
		CapDrop: []string{"ALL"},
		Limits:  dockerx.ResourceLimits{MemoryMB: poolerMemoryMB},
	}
}

const poolerMemoryMB = 256

// poolerUID is the account the image runs PgBouncer as (postgres, 70).
const poolerUID = 70

// writePoolerFiles puts the two files where PgBouncer reads them. The
// configuration holds no secret and is root's; the userlist holds passwords and
// is the PgBouncer account's alone, mode 0600.
func writePoolerFiles(ctx context.Context, docker *dockerx.Client, id, ini, userlist string) error {
	if err := docker.WriteContainerFile(ctx, id, dbx.PoolerConfigPath, []byte(ini), 0o644); err != nil {
		return err
	}
	return docker.WriteContainerFileOwned(ctx, id, dbx.PoolerUserlistPath, []byte(userlist), 0o600, poolerUID, poolerUID)
}

// poolerBinding is where a pooler container's PgBouncer port is published,
// read from its configuration so that it is known for a stopped one too.
func poolerBinding(spec *dockerx.ContainerSpec) (ip string, port int) {
	for _, p := range spec.Ports {
		if p.ContainerPort == dbx.PoolerListenPort {
			ip = p.HostIP
			if ip == "" || ip == "::" {
				ip = "0.0.0.0"
			}
			return ip, p.HostPort
		}
	}
	return "", 0
}

func onNetwork(detail *dockerx.ContainerDetail, network string) bool {
	for _, endpoint := range detail.NetworkList {
		if endpoint.Name == network {
			return true
		}
	}
	return false
}

// ensurePoolerNetwork creates the private network the pooler and its database
// share, or recognises the one made earlier. A network of that name that is
// not this pooler's is refused: it belongs to somebody else.
func (s *Server) ensurePoolerNetwork(ctx context.Context, rec *poolerRecord) error {
	docker := s.modules.docker
	owner := strconv.FormatInt(rec.ConnectionID, 10)
	observed, err := docker.InspectNetwork(ctx, rec.Network)
	if errdefs.IsNotFound(err) {
		_, err = docker.CreateNetwork(ctx, dockerx.NetworkSpec{Name: rec.Network, Driver: "bridge", Labels: map[string]string{poolerLabel: owner}})
		if err != nil {
			return httpx.BadRequest("could not create the pooler's network: %v", err)
		}
		return nil
	}
	if err != nil {
		return s.dockerErr(err)
	}
	if observed.Labels[poolerLabel] != owner {
		return httpx.Err(http.StatusConflict, "network_exists",
			fmt.Sprintf("a Docker network named %s already exists and is not this database's pooler network", rec.Network))
	}
	return nil
}

// removePoolerContainer removes the pooler's container, and only a container
// that carries this connection's mark: the name is the dashboard's choice, and
// a container of somebody else's may have it. Absent is not an error.
func (s *Server) removePoolerContainer(ctx context.Context, rec *poolerRecord) error {
	docker := s.modules.docker
	existing, err := docker.Inspect(ctx, rec.Container)
	switch {
	case errdefs.IsNotFound(err):
		return nil
	case err != nil:
		return err
	case existing.Labels[poolerLabel] != strconv.FormatInt(rec.ConnectionID, 10):
		return nil
	}
	if err := docker.RemoveContainer(ctx, existing.ID, true, false); err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	return nil
}

// removePoolerResources takes the container and the network away, leaving the
// database and its data alone. A thing that is already gone is not an error:
// this is how a pooler is removed and how a half-made one is cleaned up. A
// network that does not carry this connection's mark is never touched.
func (s *Server) removePoolerResources(ctx context.Context, rec *poolerRecord) error {
	docker := s.modules.docker
	if docker == nil {
		return nil
	}
	if err := s.removePoolerContainer(ctx, rec); err != nil {
		return err
	}
	observed, err := docker.InspectNetwork(ctx, rec.Network)
	switch {
	case errdefs.IsNotFound(err):
		return nil
	case err != nil:
		return err
	case observed.Labels[poolerLabel] != strconv.FormatInt(rec.ConnectionID, 10):
		return nil
	}
	if rec.ServerName != "" {
		// The database stays on after the network goes.
		if err := docker.DisconnectNetwork(ctx, rec.Network, rec.ServerName, false); err != nil && !errdefs.IsNotFound(err) && !strings.Contains(strings.ToLower(err.Error()), "is not connected") {
			return err
		}
	}
	if err := docker.RemoveNetwork(ctx, rec.Network); err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	return nil
}

// lockPooler serialises the things that change one connection's pooler: the
// request that turns it on, changes or removes it, the access switch that moves
// it, and the keeper that rewrites its files. Without it the keeper's write
// could land on a pooler that had just been changed or removed.
func (s *Server) lockPooler(id int64) (unlock func()) {
	held, _ := s.dbConns.poolerLocks.LoadOrStore(id, &sync.Mutex{})
	mu := held.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// tryLockPooler is lockPooler for a caller that would rather skip than wait:
// the keeper, which has the next pooler to see to.
func (s *Server) tryLockPooler(id int64) (unlock func(), ok bool) {
	held, _ := s.dbConns.poolerLocks.LoadOrStore(id, &sync.Mutex{})
	mu := held.(*sync.Mutex)
	return mu.Unlock, mu.TryLock()
}

// followPoolerPower starts or stops the pooler with its database and says
// what it did, for the audit entry of the power change. A restart of the
// database is left to PgBouncer, which reconnects by itself.
func (s *Server) followPoolerPower(ctx context.Context, id int64, action dockerx.LifecycleAction) string {
	if action == dockerx.ActionRestart || s.modules.docker == nil {
		return ""
	}
	rec, err := s.poolerRecord(ctx, id)
	if err != nil || rec == nil {
		return ""
	}
	timeout := 10
	if err := s.modules.docker.Lifecycle(ctx, rec.Container, action, &timeout); err != nil {
		if errdefs.IsNotFound(err) {
			return "missing"
		}
		return "failed: " + err.Error()
	}
	if action == dockerx.ActionStart {
		return "started"
	}
	return "stopped"
}

// setupPoolerAuth leaves auth_query mode's role and function in the database,
// in one transaction, and refuses to alter whatever of the name is already
// there (dbx.SetUpPoolerAuth).
func (s *Server) setupPoolerAuth(ctx context.Context, conn *dbConnection, dsn, password string) error {
	pool, err := s.modules.dbs.Pool(ctx, conn.ID, conn.Driver, dsn)
	if err != nil {
		return connectFailed(dsn, err)
	}
	switch err := dbx.SetUpPoolerAuth(ctx, pool, password); {
	case err == nil:
		return nil
	case errors.Is(err, dbx.ErrPoolerNotSuperuser):
		return httpx.BadRequest("%v", err)
	case errors.Is(err, dbx.ErrPoolerAuthTaken):
		return httpx.Err(http.StatusConflict, "pooler_auth_taken", err.Error())
	default:
		return httpx.Err(http.StatusBadGateway, "pooler_auth_failed", connectError(dsn, err))
	}
}

// removePoolerAuth takes auth_query mode's objects out of the database again.
func (s *Server) removePoolerAuth(ctx context.Context, conn *dbConnection, dsn string) error {
	pool, err := s.modules.dbs.Pool(ctx, conn.ID, conn.Driver, dsn)
	if err != nil {
		return err
	}
	return dbx.RemovePoolerAuth(ctx, pool)
}

// poolerSlotFree refuses, before anything is written to the database or to
// Docker, a pooler that would collide with another one: a second connection to
// the same database container (the account auth_query leaves is the server's,
// so it is one pooler's), or a container of the name that is not this
// connection's.
func (s *Server) poolerSlotFree(ctx context.Context, rec *poolerRecord, t *poolerTarget) error {
	var other string
	err := s.Store.DB.QueryRowContext(ctx, `SELECT c.name FROM db_poolers p JOIN db_connections c ON c.id = p.connection_id
		WHERE p.connection_id <> ? AND (p.container_name = ? OR p.server_name = ?) LIMIT 1`,
		rec.ConnectionID, rec.Container, strings.TrimPrefix(t.detail.Name, "/")).Scan(&other)
	switch {
	case err == nil:
		return httpx.Err(http.StatusConflict, "pooler_exists",
			fmt.Sprintf("the connection %s already has a pooler on %s; a database container has one", other, t.server.container.Name))
	case !errors.Is(err, sql.ErrNoRows):
		return httpx.Internal(err)
	}
	existing, err := s.modules.docker.Inspect(ctx, rec.Container)
	switch {
	case errdefs.IsNotFound(err):
		return nil
	case err != nil:
		return s.dockerErr(err)
	case existing.Labels[poolerLabel] != strconv.FormatInt(rec.ConnectionID, 10):
		return httpx.Err(http.StatusConflict, "container_exists",
			fmt.Sprintf("a container named %s already exists and is not this database's pooler; remove or rename it first", rec.Container))
	}
	return nil
}

// --- the connection string ----------------------------------------------------

// poolerConnectionURL is the connection string through the pooler, for the
// place a program will run: this server, a container on the database's
// network, or another machine. It is the saved string with only the address
// changed, so the account, database and options are the ones the direct
// string has.
func (s *Server) poolerConnectionURL(ctx context.Context, conn *dbConnection, dsn, target string) (string, error) {
	rec, err := s.poolerRecord(ctx, conn.ID)
	if err != nil {
		return "", err
	}
	if rec == nil {
		return "", httpx.Err(http.StatusConflict, "no_pooler", "this database has no connection pooler; turn one on under Settings")
	}
	info, err := dbx.ParseDSN(conn.Driver, dsn)
	if err != nil {
		return "", httpx.BadRequest("the saved connection could not be read")
	}
	if target == "container" {
		return applicationConnectionURL(conn.Driver, dsn, info, poolerDNSName(conn.ID), strconv.Itoa(dbx.PoolerListenPort))
	}
	if s.modules.docker == nil {
		return "", httpx.Err(http.StatusServiceUnavailable, "docker_unavailable", "this host has no Docker socket")
	}
	spec, err := s.modules.docker.SpecOf(ctx, rec.Container)
	if err != nil {
		return "", s.dockerErr(err)
	}
	ip, port := poolerBinding(spec)
	host := ip
	if target == "public" {
		if ip != "0.0.0.0" {
			return "", httpx.BadRequest("the pooler is published on %s only; open the database to anywhere under Settings first", ip)
		}
		if host = preferredPublicAddress(proxysvc.PublicAddresses()); host == "" {
			return "", httpx.BadRequest("this server has no public address")
		}
	} else if ip == "0.0.0.0" {
		host = "127.0.0.1"
	}
	return applicationConnectionURL(conn.Driver, dsn, info, host, strconv.Itoa(port))
}

// --- keeping it in sync -------------------------------------------------------

// poolerKeeper writes a pooler's files again when the password they were
// written from has changed. A connection's password changes on several roads
// (the adopt that re-seals it, an edit of the connection, an account reset),
// and the pooler's userlist is a copy of it: one place that notices is
// stronger than a call added to each road.
type poolerKeeper struct {
	server *Server
	cancel context.CancelFunc
	done   chan struct{}
}

// poolerKeeperInterval is how often passwords are compared. A comparison reads
// the store and does no Docker call unless something has changed.
const poolerKeeperInterval = 20 * time.Second

func (k *poolerKeeper) Start(ctx context.Context) {
	if k == nil || k.cancel != nil {
		return
	}
	ctx, k.cancel = context.WithCancel(ctx)
	k.done = make(chan struct{})
	go func() {
		defer close(k.done)
		ticker := time.NewTicker(poolerKeeperInterval)
		defer ticker.Stop()
		for {
			k.Reconcile(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (k *poolerKeeper) Stop() {
	if k == nil || k.cancel == nil {
		return
	}
	k.cancel()
	<-k.done
	k.cancel = nil
}

// Reconcile brings every pooler whose files are out of date up to date.
func (k *poolerKeeper) Reconcile(ctx context.Context) {
	s := k.server
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT connection_id FROM db_poolers ORDER BY connection_id`)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		opCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := s.syncPooler(opCtx, id)
		cancel()
		if err != nil && s.Log != nil {
			s.Log.Warn("a connection pooler could not be brought up to date", "connection", id, "err", err)
		}
	}
}

// syncPooler writes a pooler's files again if the credentials they were made
// from have changed, and asks PgBouncer to reread them.
func (s *Server) syncPooler(ctx context.Context, id int64) error {
	unlock, free := s.tryLockPooler(id)
	if !free {
		return nil
	}
	defer unlock()
	rec, err := s.poolerRecord(ctx, id)
	if err != nil || rec == nil || s.modules.docker == nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(ctx, id)
	if err != nil {
		return nil
	}
	ini, userlist, sha, err := poolerFiles(rec, dsn)
	if err != nil || sha == rec.ConfigSHA {
		return err
	}
	existing, err := s.modules.docker.Inspect(ctx, rec.Container)
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := writePoolerFiles(ctx, s.modules.docker, existing.ID, ini, userlist); err != nil {
		return err
	}
	if existing.State == "running" {
		if err := s.modules.docker.Signal(ctx, existing.ID, "SIGHUP"); err != nil {
			return err
		}
	}
	// An update, never an insert: a pooler removed while this ran stays removed.
	if _, err := s.Store.DB.ExecContext(ctx, `UPDATE db_poolers SET container_id=?, config_sha=?, updated_at=? WHERE connection_id=?`,
		existing.ID, sha, time.Now().UTC().Unix(), id); err != nil {
		return err
	}
	raw, _ := json.Marshal(map[string]any{"connectionId": id, "container": rec.Container, "reason": "the connection's credentials changed"})
	s.Audit.Record(ctx, audit.Entry{Actor: "system", Action: "database.pooler.sync", Target: conn.Name, Success: true, Detail: string(raw)})
	return nil
}

// forgetPooler removes a forgotten connection's pooler and says what became of
// it, for the audit entry. The row has already gone with the connection, and
// so has the pool its credentials were reached through; the container, its
// network, the firewall rule for its port and (when the server can be reached
// and the connection was not protected) auth_query's role and function are
// removed best-effort, and each outcome is reported. Nothing here is an error
// that would undo forgetting.
func (s *Server) forgetPooler(ctx context.Context, rec *poolerRecord, conn *dbConnection, dsn string) string {
	if rec == nil {
		return ""
	}
	unlock := s.lockPooler(rec.ConnectionID)
	defer unlock()
	var said []string
	if err := s.removePoolerResources(ctx, rec); err != nil {
		said = append(said, "left in place: "+err.Error())
	} else {
		said = append(said, "removed "+rec.Container)
	}
	if rec.HostPort > 0 {
		if firewall, err := s.setDBFirewall(ctx, nil, rec.HostPort, false); err != nil {
			said = append(said, "firewall rule for port "+strconv.Itoa(rec.HostPort)+" left: "+err.Error())
		} else if firewall == "closed" {
			said = append(said, "firewall rule for port "+strconv.Itoa(rec.HostPort)+" closed")
		}
	}
	if rec.Settings.AuthMode == dbx.PoolerAuthQuery {
		said = append(said, s.forgetPoolerAuth(ctx, conn, dsn))
	}
	return strings.Join(said, "; ")
}

// forgetPoolerAuth takes auth_query's role and function out of the database of
// a connection being forgotten, on a connection of its own since the saved one
// has gone. A protected connection's database is not written to; the account
// and function are left there, and the entry says so.
func (s *Server) forgetPoolerAuth(ctx context.Context, conn *dbConnection, dsn string) string {
	const left = "auth_query's role and function left in the database"
	switch {
	case conn == nil:
		return left + " (the connection could not be read)"
	case conn.ReadOnly:
		return left + " (the connection is protected)"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	if err == nil {
		defer db.Close()
		err = dbx.RemovePoolerAuth(ctx, db)
	}
	if err != nil {
		return left + ": " + connectError(dsn, err)
	}
	return "auth_query's role and function removed from the database"
}

// followPoolerExposure moves a connection's pooler to the address the access
// switch has just been asked for (bind: loopback or every interface), and
// reports what that did for the audit entry and the reply of the switch. Nil
// for a connection with no pooler, or one already there.
//
// This is the only road by which a pooler leaves loopback, so it fails closed:
// when the request is for loopback and the pooler cannot be moved, its container
// is removed and its firewall rule closed rather than left published, and the
// outcome says so. A failure to widen leaves the pooler where it was.
func (s *Server) followPoolerExposure(ctx context.Context, r *http.Request, conn *dbConnection, dsn, bind string) map[string]any {
	unlock := s.lockPooler(conn.ID)
	defer unlock()
	rec, err := s.poolerRecord(ctx, conn.ID)
	if err != nil || rec == nil || s.modules.docker == nil {
		return nil
	}
	if spec, err := s.modules.docker.SpecOf(ctx, rec.Container); err == nil {
		if ip, _ := poolerBinding(spec); ip == bind {
			return nil
		}
	} else if errdefs.IsNotFound(err) {
		// Nothing is published.
		return nil
	}
	out := map[string]any{"container": rec.Container}
	target, err := s.poolerTarget(ctx, conn, dsn)
	if err == nil {
		var change poolerChange
		if change, err = s.ensurePooler(ctx, r, target, rec, dsn, bind, false); err == nil {
			out["port"], out["recreated"], out["exposure"] = rec.HostPort, change.Recreated, exposureOfBinding(change.Bind)
			if change.Firewall != "" {
				out["firewall"] = change.Firewall
			}
			if change.FirewallErr != nil {
				out["firewallError"] = change.FirewallErr.Error()
			}
			return out
		}
	}
	out["error"] = err.Error()
	if bind == poolerLoopback {
		if cerr := s.removePoolerContainer(ctx, rec); cerr != nil {
			out["closeError"] = cerr.Error()
		} else {
			out["removed"] = true
		}
		if rec.HostPort > 0 {
			firewall, ferr := s.setDBFirewall(ctx, r, rec.HostPort, false)
			out["firewall"] = firewall
			if ferr != nil {
				out["firewallError"] = ferr.Error()
			}
		}
	}
	return out
}
