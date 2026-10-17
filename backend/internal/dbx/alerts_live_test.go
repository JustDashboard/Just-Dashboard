package dbx

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The alerts' own reads against real servers. Each only reads — the
// catalogue, the replication views, the disks — so it runs against the shared
// servers, and like every newer live test it runs only where its variable is
// set.

func alertLiveSQL(t *testing.T, driver Driver, env string) *sql.DB {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("set %s to run this", env)
	}
	d, err := DialectFor(driver)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(d.SQLDriverName(), d.NormaliseDSN(dsn))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("%s unreachable at %s (%v)", driver, env, err)
	}
	return db
}

func TestLiveAlertReadingsOnSQLServers(t *testing.T) {
	for _, c := range []struct {
		driver Driver
		env    string
	}{
		{DriverPostgres, "JD_TEST_POSTGRES_DSN"},
		{DriverMySQL, "JD_TEST_MYSQL8_DSN"},
		{DriverMySQL, "JD_TEST_MARIADB_DSN"},
		{DriverClickHouse, "JD_TEST_CLICKHOUSE_DSN"},
		{DriverMSSQL, "JD_TEST_MSSQL_DSN"},
	} {
		t.Run(c.env, func(t *testing.T) {
			db := alertLiveSQL(t, c.driver, c.env)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			lag, err := ReadReplicationLag(ctx, db, c.driver)
			switch c.driver {
			case DriverPostgres, DriverMySQL:
				// The fixtures replicate with nothing: a standalone server is
				// judged quiet, never stopped.
				if err != nil || lag.Stopped != "" || lag.Seconds != 0 {
					t.Fatalf("replication of a standalone server: %+v, %v", lag, err)
				}
			default:
				if err != ErrNotReadHere {
					t.Fatalf("an engine with no replication view answered %+v, %v", lag, err)
				}
			}

			dir, err := ReadDataDirectory(ctx, db, c.driver)
			switch c.driver {
			case DriverMySQL:
				if err != nil || !filepath.IsAbs(dir) {
					t.Fatalf("@@datadir: %q, %v", dir, err)
				}
			case DriverPostgres:
				// Shown to a superuser or pg_read_all_settings only: either a
				// path or nothing, never an error.
				if err != nil || (dir != "" && !filepath.IsAbs(dir)) {
					t.Fatalf("data_directory: %q, %v", dir, err)
				}
			default:
				if err != ErrNotReadHere {
					t.Fatalf("data directory of %s: %q, %v", c.driver, dir, err)
				}
			}

			use, err := ReadEngineDisk(ctx, db, c.driver)
			switch c.driver {
			case DriverClickHouse, DriverMSSQL:
				if err != nil || use.Total <= 0 || use.Used <= 0 || use.Used > use.Total || use.Path == "" {
					t.Fatalf("the engine's own disk: %+v, %v", use, err)
				}
			default:
				if err != ErrNotReadHere {
					t.Fatalf("%s reported a disk of its own: %+v, %v", c.driver, use, err)
				}
			}
		})
	}
}

func TestLiveAlertReadingsOnMongo(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	t.Run("replica set", func(t *testing.T) {
		client, db := liveMongoReplicaSet(t)
		lag, err := MongoReplicationLag(ctx, client)
		if err != nil || !lag.Replicating || lag.Stopped != "" || lag.Seconds != 0 {
			t.Fatalf("a one-member set with its primary: %+v, %v", lag, err)
		}
		use, err := MongoDiskUse(ctx, client, db)
		if err != nil || use.Total <= 0 || use.Used <= 0 || use.Used > use.Total {
			t.Fatalf("dbStats' filesystem: %+v, %v", use, err)
		}
	})
}

func TestLiveAlertReadingsOnRedis(t *testing.T) {
	client := liveRedis(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lag, err := RedisReplicationLag(ctx, client)
	if err != nil || lag.Replicating || lag.Stopped != "" {
		t.Fatalf("a standalone Redis: %+v, %v", lag, err)
	}
	if dir := RedisDataDirectory(ctx, client); !filepath.IsAbs(dir) {
		t.Fatalf("CONFIG GET dir: %q", dir)
	}
	info, err := RedisInfo(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	counters, _ := info["counters"].(map[string]any)
	if limit, _ := counters["maxclients"].(int64); limit <= 0 {
		t.Fatalf("INFO carries no maxclients the connections alert can read: %+v", counters)
	}
}
