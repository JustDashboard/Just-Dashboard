package dbx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// What a database alert reads from a server beyond the recorder's snapshot:
// how far behind its replication is, and where it keeps its data and how
// full that is. Each read is one bounded question the engine answers from its
// own catalogue, and an engine that has no answer says so with
// ErrNotReadHere rather than with a reading that looks like a healthy one.

// ErrNotReadHere is an engine that does not answer the question asked.
var ErrNotReadHere = errors.New("this engine does not report it")

// ReplicationLag is a server's replication as one reading an alert can
// judge: the worst lag in seconds, whose it is, or that it has stopped.
type ReplicationLag struct {
	// Replicating is false on a server that neither follows another nor is
	// followed by one this server can see, which is nothing to judge.
	Replicating bool `json:"replicating"`
	// Seconds is the worst lag found, of Of.
	Seconds float64 `json:"seconds"`
	Of      string  `json:"of,omitempty"`
	// Stopped says replication that should be running is not, in the
	// engine's words. A stopped replica is behind by however long it stays
	// stopped, which no figure says.
	Stopped string `json:"stopped,omitempty"`
	// Note says why a server that is part of a topology is still not judged.
	Note string `json:"note,omitempty"`
}

// worse keeps the larger lag.
func (l *ReplicationLag) worse(seconds float64, of string) {
	if seconds > l.Seconds || l.Of == "" {
		l.Seconds, l.Of = math.Max(seconds, 0), of
	}
}

// ReadReplicationLag reads a SQL server's replication lag. PostgreSQL is
// asked directly: a standby's "now minus the last replayed commit" grows on
// a primary that is simply idle, so a standby that has replayed everything
// it received is behind by nothing whatever that clock says.
func ReadReplicationLag(ctx context.Context, db *sql.DB, driver Driver) (*ReplicationLag, error) {
	switch driver {
	case DriverPostgres:
		return postgresReplicationLag(ctx, db)
	case DriverMySQL:
		report, err := ReadReplication(ctx, db, driver)
		if err != nil {
			return nil, err
		}
		return mysqlReplicationLag(report), nil
	}
	return nil, ErrNotReadHere
}

func postgresReplicationLag(ctx context.Context, db *sql.DB) (*ReplicationLag, error) {
	var recovery bool
	if err := db.QueryRowContext(ctx, `SELECT pg_is_in_recovery()`).Scan(&recovery); err != nil {
		return nil, err
	}
	out := &ReplicationLag{}
	if recovery {
		var status, sender string
		err := db.QueryRowContext(ctx, `
		  SELECT CASE WHEN pg_last_wal_receive_lsn() IS NULL
		                OR pg_last_wal_receive_lsn() <= pg_last_wal_replay_lsn() THEN 0
		              ELSE COALESCE(EXTRACT(EPOCH FROM (clock_timestamp() - pg_last_xact_replay_timestamp())), 0)
		         END::float8,
		         COALESCE(w.status, ''), COALESCE(w.sender_host, '')
		  FROM (SELECT 1) one
		  LEFT JOIN pg_stat_wal_receiver w ON true`).Scan(&out.Seconds, &status, &sender)
		if err != nil {
			return nil, err
		}
		out.Replicating, out.Of = true, "this standby"
		if sender != "" {
			out.Of = "this standby of " + sender
		}
		// An empty status is a receiver this account may not read, or a
		// standby restoring from an archive, neither of which is a stop.
		if status != "" && status != "streaming" {
			out.Stopped = "The WAL receiver is " + status + ", not streaming from the primary."
		}
		out.Seconds = math.Max(out.Seconds, 0)
		return out, nil
	}
	rows, err := db.QueryContext(ctx, `
	  SELECT COALESCE(NULLIF(application_name, ''), host(client_addr), 'a replica'),
	         COALESCE(state, ''), COALESCE(EXTRACT(EPOCH FROM replay_lag), 0)::float8
	  FROM pg_stat_replication`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, state string
		var lag float64
		if err := rows.Scan(&name, &state, &lag); err != nil {
			return nil, err
		}
		out.Replicating = true
		// replay_lag is NULL once a replica has caught up and nothing new has
		// been written, which is no lag at all.
		out.worse(lag, name)
	}
	return out, rows.Err()
}

// mysqlReplicationLag reads the replica side of MySQL and MariaDB. A primary
// does not know how far behind its replicas are — only a replica does — so a
// primary is not judged, and says so.
func mysqlReplicationLag(report *ReplicationReport) *ReplicationLag {
	out := &ReplicationLag{}
	for _, src := range report.Sources {
		out.Replicating = true
		of := strings.TrimSuffix(src.Host+":"+src.Port, ":")
		if src.Channel != "" {
			of = src.Channel + " (" + of + ")"
		}
		switch {
		case !src.IORunning || !src.SQLRunning:
			thread := "SQL thread"
			if !src.IORunning {
				thread = "I/O thread"
			}
			// The engine's error is not repeated: it quotes the row it
			// stopped on, and this sentence goes to a chat or a mailbox. The
			// replication view shows it to whoever opens the page.
			out.Stopped = fmt.Sprintf("Replication from %s is stopped: its %s is not running.", of, thread)
			out.Of = of
		case src.LagSeconds >= 0:
			out.worse(src.LagSeconds, of)
		}
	}
	if !out.Replicating && len(report.Replicas) > 0 {
		out.Note = "A MySQL primary does not know how far behind its replicas are; save a replica as a connection to watch its lag."
	}
	return out
}

// MongoReplicationLag reads a replica set's lag from any member: every
// secondary's last applied operation against the primary's.
func MongoReplicationLag(ctx context.Context, client *mongo.Client) (*ReplicationLag, error) {
	status, err := MongoReplicationStatus(ctx, client)
	if err != nil {
		return nil, err
	}
	return mongoReplicationLag(status), nil
}

func mongoReplicationLag(status *MongoReplication) *ReplicationLag {
	out := &ReplicationLag{}
	if !status.ReplicaSet {
		return out
	}
	out.Replicating = true
	primary := false
	for _, m := range status.Members {
		switch {
		case m.State == "PRIMARY":
			primary = true
		case m.State == "ARBITER":
		case !m.Health:
			out.Stopped = fmt.Sprintf("Member %s of %s is not answering (%s).", m.Name, status.SetName, strings.ToLower(m.State))
			out.Of = m.Name
		case m.LagSeconds != nil:
			out.worse(*m.LagSeconds, m.Name)
		}
	}
	if !primary && out.Stopped == "" {
		out.Stopped = status.SetName + " has no primary, so nothing is replicated."
	}
	return out
}

// RedisReplicationLag reads INFO replication. A replica's lag is how long
// since it last heard from its primary, which pings it every ten seconds by
// default; a primary's is the longest any replica has gone without
// acknowledging.
func RedisReplicationLag(ctx context.Context, client *redis.Client) (*ReplicationLag, error) {
	raw, err := client.Info(ctx, "replication").Result()
	if err != nil {
		return nil, err
	}
	return redisReplicationLag(redisReplicationFacts(redisInfoMap(parseRedisInfo(raw)))), nil
}

func redisReplicationLag(r RedisReplication) *ReplicationLag {
	out := &ReplicationLag{}
	if r.Primary != nil {
		out.Replicating, out.Of = true, r.Primary.Addr
		if !r.Primary.Up {
			out.Stopped = "The link to the primary " + r.Primary.Addr + " is down."
			if r.Primary.Syncing {
				out.Stopped = "This replica is still taking its first copy from " + r.Primary.Addr + "."
			}
			return out
		}
		out.Seconds = float64(max(r.Primary.LastIOSecondsAgo, 0))
		return out
	}
	for _, replica := range r.Replicas {
		out.Replicating = true
		out.worse(float64(replica.LagSeconds), replica.Addr)
	}
	return out
}

// DiskUse is the filesystem a server keeps its data on, as the server itself
// reports it.
type DiskUse struct {
	// Path is where, in the server's own words: its data path, or a volume.
	Path  string `json:"path"`
	Used  int64  `json:"used"`
	Total int64  `json:"total"`
}

// Percent is how full the filesystem is.
func (d DiskUse) Percent() float64 {
	if d.Total <= 0 {
		return 0
	}
	return float64(d.Used) / float64(d.Total) * 100
}

// ReadEngineDisk asks a SQL server how full the disk under its data is,
// where the server can say: ClickHouse lists its disks, SQL Server the
// volume of each database file. Every other engine answers ErrNotReadHere,
// and its disk is measured on this machine, from the data directory.
func ReadEngineDisk(ctx context.Context, db *sql.DB, driver Driver) (*DiskUse, error) {
	out := &DiskUse{}
	var err error
	switch driver {
	case DriverClickHouse:
		err = db.QueryRowContext(ctx, `
		  SELECT path, toInt64(total_space - free_space), toInt64(total_space)
		  FROM system.disks
		  WHERE total_space > 0
		  ORDER BY (total_space - free_space) / total_space DESC
		  LIMIT 1`).Scan(&out.Path, &out.Used, &out.Total)
	case DriverMSSQL:
		// Needs VIEW SERVER STATE; without it the disk is measured here.
		err = db.QueryRowContext(ctx, `
		  SELECT TOP 1 vs.volume_mount_point,
		         CAST(vs.total_bytes - vs.available_bytes AS BIGINT), CAST(vs.total_bytes AS BIGINT)
		  FROM sys.master_files mf
		  CROSS APPLY sys.dm_os_volume_stats(mf.database_id, mf.file_id) vs
		  WHERE mf.database_id = DB_ID() AND vs.total_bytes > 0
		  ORDER BY CAST(vs.total_bytes - vs.available_bytes AS FLOAT) / vs.total_bytes DESC`).
			Scan(&out.Path, &out.Used, &out.Total)
	default:
		return nil, ErrNotReadHere
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MongoDiskUse reads dbStats, which reports the filesystem the database's
// files are on beside the database's own sizes.
func MongoDiskUse(ctx context.Context, client *mongo.Client, database string) (*DiskUse, error) {
	if database == "" {
		database = "admin"
	}
	raw, err := client.Database(database).RunCommand(ctx, bson.D{{Key: "dbStats", Value: 1}}).Raw()
	if err != nil {
		return nil, err
	}
	used, okUsed := raw.Lookup("fsUsedSize").AsInt64OK()
	total, okTotal := raw.Lookup("fsTotalSize").AsInt64OK()
	if !okUsed || !okTotal || total <= 0 {
		// FerretDB and a database with no files yet say nothing about it.
		return nil, ErrNotReadHere
	}
	return &DiskUse{Path: "the data directory of " + database, Used: used, Total: total}, nil
}

// ReadDataDirectory is where a SQL server says its data lives, as a path on
// the machine it runs on. PostgreSQL shows data_directory only to a
// superuser or a member of pg_read_all_settings; any other account reads
// "", and the place is found from the engine's usual directories instead.
func ReadDataDirectory(ctx context.Context, db *sql.DB, driver Driver) (string, error) {
	var dir string
	var err error
	switch driver {
	case DriverPostgres:
		err = db.QueryRowContext(ctx, `SELECT setting FROM pg_settings WHERE name = 'data_directory'`).Scan(&dir)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
	case DriverMySQL:
		err = db.QueryRowContext(ctx, `SELECT @@datadir`).Scan(&dir)
	default:
		return "", ErrNotReadHere
	}
	return strings.TrimSpace(dir), err
}

// RedisDataDirectory is where Redis writes its RDB and AOF files. CONFIG may
// be renamed away or refused to the account, which reads "".
func RedisDataDirectory(ctx context.Context, client *redis.Client) string {
	values, err := client.ConfigGet(ctx, "dir").Result()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(values["dir"])
}

// DataDirHints are where each engine's official image and packages keep the
// data, most specific first: what is measured when the server does not say.
func DataDirHints(d Driver) []string {
	switch d {
	case DriverPostgres:
		return []string{"/var/lib/postgresql/data", "/var/lib/postgresql"}
	case DriverMySQL:
		return []string{"/var/lib/mysql"}
	case DriverMongo:
		return []string{"/data/db", "/var/lib/mongodb"}
	case DriverRedis:
		return []string{"/data", "/var/lib/redis"}
	case DriverClickHouse:
		return []string{"/var/lib/clickhouse"}
	case DriverMSSQL:
		return []string{"/var/opt/mssql"}
	case DriverOracle:
		return []string{"/opt/oracle/oradata"}
	}
	return nil
}
