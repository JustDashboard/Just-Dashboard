package dbx

import (
	"context"
	"database/sql"
	"errors"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
)

// The memory an engine sets aside for itself, as its own setting says.
//
// A container's memory limit is enforced by the kernel and an engine's buffer
// by the engine, and neither knows the other's number. A PostgreSQL told to
// keep 2 GiB of shared buffers inside a 1 GiB limit runs until the buffers
// fill and is then killed, which from the outside reads as a crash. So the
// Resources page shows the engine's figure beside the limit, read from the
// server each time: the setting is the operator's, or the image's, or — for
// TimescaleDB, Dragonfly, MongoDB and ClickHouse — sized from the memory the
// engine found when it started.

// MemoryBuffer is that figure. Bytes zero is a setting left unset, which only
// Redis and its forks allow: a key–value server with no maxmemory grows until
// something else stops it.
type MemoryBuffer struct {
	Setting string `json:"setting"`
	Bytes   int64  `json:"bytes"`
}

// memoryBufferQuerier is a dialect whose engine keeps a buffer of its own,
// with the setting's name and the one statement that reads it in bytes.
type memoryBufferQuerier interface {
	memoryBuffer() (setting, query string)
}

func (postgresDialect) memoryBuffer() (string, string) {
	return "shared_buffers", "SELECT pg_size_bytes(current_setting('shared_buffers'))"
}

func (mysqlDialect) memoryBuffer() (string, string) {
	return "innodb_buffer_pool_size", "SELECT @@innodb_buffer_pool_size"
}

// ClickHouse reports the ceiling it is enforcing: unset, it is a share of the
// memory it found, its container's limit included.
func (clickhouseDialect) memoryBuffer() (string, string) {
	return "max_server_memory_usage",
		"SELECT toInt64(value) FROM system.server_settings WHERE name = 'max_server_memory_usage'"
}

// MemoryBufferSetting names the setting for an engine the dashboard reads it
// from, and is empty for the rest. The forks that speak a driver's protocol
// over an engine of their own are left out: CockroachDB and TiDB keep no such
// buffer, YugabyteDB's memory is its tablet server's rather than the
// PostgreSQL layer's, and FerretDB is a proxy whose storage is another
// server's.
func MemoryBufferSetting(d Driver, flavor string) string {
	switch flavor {
	case FlavorCockroachDB, FlavorYugabyteDB, FlavorTiDB, FlavorFerretDB:
		return ""
	}
	switch d {
	case DriverRedis:
		return "maxmemory"
	case DriverMongo:
		return "wiredTigerCacheSizeGB"
	}
	dialect, err := DialectFor(d)
	if err != nil {
		return ""
	}
	if q, ok := dialect.(memoryBufferQuerier); ok {
		setting, _ := q.memoryBuffer()
		return setting
	}
	return ""
}

// ErrNoMemoryBuffer is an engine that keeps no buffer the dashboard reads.
var ErrNoMemoryBuffer = errors.New("this engine keeps no memory buffer of its own")

// SQLMemoryBuffer reads a SQL engine's buffer.
func SQLMemoryBuffer(ctx context.Context, db *sql.DB, d Driver, flavor string) (*MemoryBuffer, error) {
	if MemoryBufferSetting(d, flavor) == "" {
		return nil, ErrNoMemoryBuffer
	}
	dialect, err := DialectFor(d)
	if err != nil {
		return nil, err
	}
	setting, query := dialect.(memoryBufferQuerier).memoryBuffer()
	out := &MemoryBuffer{Setting: setting}
	if err := db.QueryRowContext(ctx, query).Scan(&out.Bytes); err != nil {
		return nil, err
	}
	return out, nil
}

// RedisMemoryBuffer reads maxmemory from INFO rather than CONFIG GET, which a
// managed server renames or refuses and Dragonfly answers differently; INFO's
// memory section carries it on every fork.
func RedisMemoryBuffer(ctx context.Context, client *redis.Client) (*MemoryBuffer, error) {
	raw, err := client.Info(ctx, "memory").Result()
	if err != nil {
		return nil, err
	}
	return &MemoryBuffer{Setting: "maxmemory", Bytes: redisInfoInt(redisInfoMap(parseRedisInfo(raw)), "maxmemory")}, nil
}

// MongoMemoryBuffer is the WiredTiger cache's configured size, which mongod
// sets from the memory it finds at start and keeps until it restarts.
func MongoMemoryBuffer(ctx context.Context, client *mongo.Client) (*MemoryBuffer, error) {
	status, err := MongoServerSnapshot(ctx, client)
	if err != nil {
		return nil, err
	}
	if status.Cache == nil {
		return nil, errors.New("the server reports no WiredTiger cache")
	}
	return &MemoryBuffer{Setting: "wiredTigerCacheSizeGB", Bytes: status.Cache.MaxBytes}, nil
}
