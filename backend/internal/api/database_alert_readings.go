package api

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// What the recorder reads of a server for the alerts, beside the snapshot it
// keeps for the charts: whether the server answered at all, its connections
// against its limit from that same snapshot, and — only for a server a rule
// watches for them — the disk its data is on and its replication.

// observeDatabase turns one recorder read into what the alerts judge. The
// snapshot and its error are the recorder's own; disk and replication are
// read here, once, and only when an enabled rule watches this server for
// them.
func (s *Server) observeDatabase(ctx context.Context, conn *dbConnection, dsn string, snapshot any, readErr error, rules []dbAlertRule) dbAlertObservation {
	obs := dbAlertObservation{Conn: dbAlertConn{ID: conn.ID, Name: conn.Name, Driver: conn.Driver}}
	if identity, _, ok := s.lastIdentity(conn.ID, dsn); ok {
		obs.Conn.Flavor = identity.Flavor
	}
	switch {
	case readErr != nil:
		obs.Error = connectError(dsn, readErr)
	default:
		obs.Answered = true
		if stats, ok := snapshot.(dbStatsResponse); ok && !stats.Supported {
			// The snapshot was refused, which is either a server that answers
			// and will not say, or a pool whose server has gone: ask it.
			if err := s.pingDatabase(ctx, conn, dsn); err != nil {
				obs.Answered, obs.Error = false, connectError(dsn, err)
			}
		}
	}
	if !obs.Answered {
		return obs
	}
	obs.Sessions = dbAlertSessionsOf(snapshot)
	var disk, replication bool
	for _, rule := range rules {
		if rule.covers(obs.Conn) {
			disk = disk || rule.Kind == dbx.AlertDisk
			replication = replication || rule.Kind == dbx.AlertReplication
		}
	}
	if disk {
		obs.Disk, obs.DiskUnknown = s.dbDiskReading(ctx, conn, dsn)
	}
	if replication {
		obs.Replication, obs.ReplicationUnknown = s.dbReplicationReading(ctx, conn, dsn)
	}
	return obs
}

func (s *Server) pingDatabase(ctx context.Context, conn *dbConnection, dsn string) error {
	pool, err := s.modules.dbs.Pool(ctx, conn.ID, conn.Driver, dsn)
	if err != nil {
		return err
	}
	return pool.PingContext(ctx)
}

// dbAlertSessionsOf reads the connections and the limit out of the
// recorder's snapshot: a SQL engine's counts, Redis's connected_clients
// against maxclients, MongoDB's current against current plus available.
func dbAlertSessionsOf(snapshot any) *dbAlertSessions {
	switch v := snapshot.(type) {
	case dbStatsResponse:
		if v.ServerStats == nil || v.Connections == nil || v.Connections.Max <= 0 {
			return nil
		}
		c := v.Connections
		return &dbAlertSessions{Held: c.Total, Limit: c.Max, Reserved: c.Reserved}
	case map[string]any:
		server, _ := v["server"].(map[string]any)
		if counters, ok := server["counters"].(map[string]any); ok {
			held, okHeld := alertNumber(counters["connected_clients"])
			limit, okLimit := alertNumber(counters["maxclients"])
			if okHeld && okLimit && limit > 0 {
				return &dbAlertSessions{Held: int(held), Limit: int(limit)}
			}
			return nil
		}
		if c, ok := server["connections"].(map[string]any); ok {
			current, okCurrent := alertNumber(c["current"])
			available, okAvailable := alertNumber(c["available"])
			if okCurrent && okAvailable && current+available > 0 {
				return &dbAlertSessions{Held: int(current), Limit: int(current + available)}
			}
		}
	}
	return nil
}

func alertNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// --- replication -------------------------------------------------------------

func (s *Server) dbReplicationReading(ctx context.Context, conn *dbConnection, dsn string) (*dbx.ReplicationLag, string) {
	var lag *dbx.ReplicationLag
	var err error
	switch conn.Driver {
	case dbx.DriverMongo:
		client, openErr := dbx.MongoClient(ctx, dsn)
		if openErr != nil {
			return nil, "Replication could not be read: " + connectError(dsn, openErr)
		}
		defer client.Disconnect(context.Background())
		lag, err = dbx.MongoReplicationLag(ctx, client)
	case dbx.DriverRedis:
		client, openErr := dbx.RedisOpen(ctx, dsn, dbx.RedisOpenOptions{DB: 0, RespectContext: true})
		if openErr != nil {
			return nil, "Replication could not be read: " + connectError(dsn, openErr)
		}
		defer client.Close()
		lag, err = dbx.RedisReplicationLag(ctx, client)
	default:
		pool, poolErr := s.modules.dbs.Pool(ctx, conn.ID, conn.Driver, dsn)
		if poolErr != nil {
			return nil, "Replication could not be read: " + connectError(dsn, poolErr)
		}
		lag, err = dbx.ReadReplicationLag(ctx, pool, conn.Driver)
	}
	switch {
	case errors.Is(err, dbx.ErrNotReadHere):
		return nil, conn.Name + " does not report its replication."
	case err != nil:
		return nil, "Replication could not be read: " + err.Error()
	}
	return lag, ""
}

// --- disk ----------------------------------------------------------------------

// dbDiskPlaceFresh is how long where a connection's data lives is believed.
// Finding it reads Docker's list and the container's mounts, or the
// machine's sockets; a database does not move its data between passes.
const dbDiskPlaceFresh = 5 * time.Minute

// dbDiskPlace is where a connection's data is on this machine: the path
// this process measures, and the words a message names it by — or why it
// cannot be measured here.
type dbDiskPlace struct {
	path, where string
	reason      string
}

type dbDiskPlaceKept struct {
	dsn   [sha256.Size]byte
	at    time.Time
	place dbDiskPlace
}

// dbDiskReading is how full the disk under a connection's data is. The
// engine is asked first where it can say, which also covers a server on
// another machine; otherwise the data is found on this machine and its
// filesystem measured.
func (s *Server) dbDiskReading(ctx context.Context, conn *dbConnection, dsn string) (*dbAlertDisk, string) {
	if use := s.engineDiskUse(ctx, conn, dsn); use != nil {
		return &dbAlertDisk{Used: use.Used, Total: use.Total, Where: use.Path}, ""
	}
	place := s.dbDiskPlaceOf(ctx, conn, dsn)
	if place.path == "" {
		return nil, place.reason
	}
	used, total, err := statDisk(place.path)
	if err != nil {
		return nil, "The disk under " + place.where + " could not be read: " + err.Error()
	}
	return &dbAlertDisk{Used: used, Total: total, Where: place.where}, ""
}

func (s *Server) engineDiskUse(ctx context.Context, conn *dbConnection, dsn string) *dbx.DiskUse {
	switch conn.Driver {
	case dbx.DriverMongo:
		client, err := dbx.MongoClient(ctx, dsn)
		if err != nil {
			return nil
		}
		defer client.Disconnect(context.Background())
		use, err := dbx.MongoDiskUse(ctx, client, conn.Database)
		if err != nil {
			return nil
		}
		return use
	case dbx.DriverClickHouse, dbx.DriverMSSQL:
		pool, err := s.modules.dbs.Pool(ctx, conn.ID, conn.Driver, dsn)
		if err != nil {
			return nil
		}
		use, err := dbx.ReadEngineDisk(ctx, pool, conn.Driver)
		if err != nil {
			return nil
		}
		return use
	}
	return nil
}

func (s *Server) dbDiskPlaceOf(ctx context.Context, conn *dbConnection, dsn string) dbDiskPlace {
	sum := sha256.Sum256([]byte(dsn))
	alerts := s.modules.dbAlerts
	if v, ok := alerts.places.Load(conn.ID); ok {
		if kept := v.(dbDiskPlaceKept); kept.dsn == sum && time.Since(kept.at) < dbDiskPlaceFresh {
			return kept.place
		}
	}
	place := s.resolveDBDiskPlace(ctx, conn, dsn)
	if ctx.Err() == nil {
		alerts.places.Store(conn.ID, dbDiskPlaceKept{dsn: sum, at: time.Now(), place: place})
	}
	return place
}

// resolveDBDiskPlace follows a connection to where its server runs — the same
// placement its summary reads — and from there to its data: a container's
// mount under the data directory, which is the volume the data is on; a
// server on this machine's data directory; and Docker's own disk, or the
// root disk, when nothing more precise is known.
func (s *Server) resolveDBDiskPlace(ctx context.Context, conn *dbConnection, dsn string) dbDiskPlace {
	ctx = s.modules.docker.WithReadSnapshot(ctx)
	place := s.newDBHostView(ctx).place(ctx, conn, dsn)
	root := s.inventoryHost().hostRoot
	switch place.Source {
	case "docker":
		if s.modules.docker == nil {
			return dbDiskPlace{reason: "Docker cannot be asked where " + conn.Name + " keeps its data."}
		}
		detail, err := s.modules.docker.Inspect(ctx, place.Container.ID)
		if err != nil {
			return dbDiskPlace{reason: "The container " + place.Container.Name + " could not be inspected: " + err.Error()}
		}
		dirs := append([]string{s.dbDataDirectory(ctx, conn, dsn)}, dbx.DataDirHints(conn.Driver)...)
		if source, where := dataMount(detail.Mounts, dirs); source != "" {
			return dbDiskPlace{path: hostVisible(root, source), where: where}
		}
		// No mount holds it: the data is in the container's own layer, which
		// Docker keeps on its own disk.
		return dbDiskPlace{path: hostVisible(root, "/var/lib/docker"), where: "Docker's disk (/var/lib/docker)"}
	case "host":
		if place.process == "docker-proxy" {
			return dbDiskPlace{reason: "Docker publishes the port of " + conn.Name + ", and the container behind it could not be found."}
		}
		dir := s.dbDataDirectory(ctx, conn, dsn)
		for _, hint := range dbx.DataDirHints(conn.Driver) {
			if dir != "" {
				break
			}
			if _, err := os.Stat(hostVisible(root, hint)); err == nil {
				dir = hint
			}
		}
		if dir == "" {
			return dbDiskPlace{path: hostVisible(root, "/"), where: "this server's root disk"}
		}
		return dbDiskPlace{path: hostVisible(root, dir), where: dir}
	}
	return dbDiskPlace{reason: conn.Name + " runs on another machine, which does not tell the dashboard how full its disk is."}
}

// dbDataDirectory is where the server says it keeps its data, as a path in
// its own filesystem, or "" where it does not say.
func (s *Server) dbDataDirectory(ctx context.Context, conn *dbConnection, dsn string) string {
	var dir string
	switch conn.Driver {
	case dbx.DriverRedis:
		client, err := dbx.RedisOpen(ctx, dsn, dbx.RedisOpenOptions{DB: 0, RespectContext: true})
		if err != nil {
			return ""
		}
		defer client.Close()
		dir = dbx.RedisDataDirectory(ctx, client)
	case dbx.DriverPostgres, dbx.DriverMySQL:
		pool, err := s.modules.dbs.Pool(ctx, conn.ID, conn.Driver, dsn)
		if err != nil {
			return ""
		}
		dir, _ = dbx.ReadDataDirectory(ctx, pool, conn.Driver)
	}
	if dir == "" || !filepath.IsAbs(dir) {
		return ""
	}
	return filepath.Clean(dir)
}

// dataMount is the host path holding the first of dirs that one of a
// container's mounts covers, the deepest mount winning, and what to call it.
func dataMount(mounts []dockerx.MountPoint, dirs []string) (source, where string) {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		var best *dockerx.MountPoint
		for i := range mounts {
			m := &mounts[i]
			dest := filepath.Clean(m.Destination)
			if m.Source == "" || (dir != dest && !strings.HasPrefix(dir, dest+"/")) {
				continue
			}
			if best == nil || len(dest) > len(filepath.Clean(best.Destination)) {
				best = m
			}
		}
		if best == nil {
			continue
		}
		rest := strings.TrimPrefix(dir, filepath.Clean(best.Destination))
		where := best.Source
		if best.Type == "volume" && best.Name != "" {
			where = "the volume " + volumeName(best.Name)
		}
		return filepath.Join(best.Source, rest), where
	}
	return "", ""
}

// volumeName is a volume as Docker's own lists shorten it: an anonymous
// volume's name is 64 hex digits, and its first twelve tell it apart.
func volumeName(name string) string {
	if len(name) == 64 && strings.Trim(name, "0123456789abcdef") == "" {
		return name[:12]
	}
	return name
}

// statDisk is a filesystem's use as df reports it: the blocks root keeps for
// itself count as neither used nor available, so a full disk reads 100%.
func statDisk(path string) (used, total int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	block := int64(st.Bsize)
	used = (int64(st.Blocks) - int64(st.Bfree)) * block
	available := int64(st.Bavail) * block
	return used, used + available, nil
}
