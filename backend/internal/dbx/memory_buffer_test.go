package dbx

import (
	"context"
	"os"
	"strconv"
	"testing"
)

// Each engine's buffer is one statement that answers in bytes: a figure in
// the engine's own unit (Postgres counts shared_buffers in 8 kB pages) would
// be compared with a limit in bytes and be wrong by that factor.
func TestMemoryBufferQueriesAnswerInBytes(t *testing.T) {
	for _, c := range []struct {
		d       Driver
		setting string
		query   string
	}{
		{DriverPostgres, "shared_buffers", "SELECT pg_size_bytes(current_setting('shared_buffers'))"},
		{DriverMySQL, "innodb_buffer_pool_size", "SELECT @@innodb_buffer_pool_size"},
		{DriverClickHouse, "max_server_memory_usage",
			"SELECT toInt64(value) FROM system.server_settings WHERE name = 'max_server_memory_usage'"},
	} {
		dialect, err := DialectFor(c.d)
		if err != nil {
			t.Fatal(err)
		}
		q, ok := dialect.(memoryBufferQuerier)
		if !ok {
			t.Fatalf("%s reads no buffer", c.d)
		}
		if setting, query := q.memoryBuffer(); setting != c.setting || query != c.query {
			t.Errorf("%s reads %s with %q", c.d, setting, query)
		}
	}
	for _, d := range []Driver{DriverSQLite, DriverMSSQL, DriverOracle} {
		if setting := MemoryBufferSetting(d, ""); setting != "" {
			t.Errorf("%s is said to keep %s", d, setting)
		}
	}
}

// A fork with no such buffer is not asked: CockroachDB would refuse the
// statement, and TiDB would answer with a variable it keeps only so that MySQL
// clients find it.
func TestMemoryBufferIsNotAskedOfAForkWithout(t *testing.T) {
	for _, flavor := range []string{FlavorCockroachDB, FlavorTiDB} {
		driver := DriverPostgres
		if flavor == FlavorTiDB {
			driver = DriverMySQL
		}
		if _, err := SQLMemoryBuffer(context.Background(), nil, driver, flavor); err != ErrNoMemoryBuffer {
			t.Errorf("%s: %v, want ErrNoMemoryBuffer before anything is dialled", flavor, err)
		}
	}
}

// Against the fixtures, the figure is the one the engine reports in its own
// words elsewhere: the setting in its unit, the variable as text.
func TestLiveMemoryBufferIsTheEnginesOwnFigure(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		d     Driver
		env   string
		check string
	}{
		{DriverPostgres, "JD_TEST_POSTGRES_DSN",
			"SELECT setting::bigint * 8192 FROM pg_settings WHERE name = 'shared_buffers'"},
		{DriverMySQL, "JD_TEST_MYSQL_DSN",
			"SELECT CAST(VARIABLE_VALUE AS UNSIGNED) FROM information_schema.GLOBAL_VARIABLES WHERE VARIABLE_NAME = 'INNODB_BUFFER_POOL_SIZE'"},
		{DriverMySQL, "JD_TEST_MYSQL8_DSN",
			"SELECT CAST(VARIABLE_VALUE AS UNSIGNED) FROM performance_schema.global_variables WHERE VARIABLE_NAME = 'innodb_buffer_pool_size'"},
		{DriverClickHouse, "JD_TEST_CLICKHOUSE_DSN",
			"SELECT toInt64(getServerSetting('max_server_memory_usage'))"},
	} {
		t.Run(c.env, func(t *testing.T) {
			if os.Getenv(c.env) == "" {
				t.Skipf("%s unset", c.env)
			}
			db := liveSQL(t, c.d, c.env, "")
			identity := IdentifySQL(ctx, db, c.d)
			got, err := SQLMemoryBuffer(ctx, db, c.d, identity.Flavor)
			if err != nil {
				t.Fatalf("%s (%s): %v", c.d, identity.Flavor, err)
			}
			if got.Setting != MemoryBufferSetting(c.d, identity.Flavor) || got.Bytes <= 0 {
				t.Fatalf("%s (%s) buffer = %+v", c.d, identity.Flavor, got)
			}
			var want int64
			if err := db.QueryRowContext(ctx, c.check).Scan(&want); err != nil {
				t.Fatal(err)
			}
			// ClickHouse's server setting reads 0 where it was never set,
			// and the figure enforced is then computed from the memory found.
			if want != 0 && got.Bytes != want {
				t.Errorf("%s (%s): %s = %d, the engine reports %d", c.d, identity.Flavor, got.Setting, got.Bytes, want)
			}
		})
	}

	t.Run("redis", func(t *testing.T) {
		client, _ := redisTestClient(t, "JD_TEST_REDIS_DSN")
		got, err := RedisMemoryBuffer(ctx, client)
		if err != nil {
			t.Fatal(err)
		}
		config, err := client.ConfigGet(ctx, "maxmemory").Result()
		if err != nil {
			t.Skipf("CONFIG GET is refused here: %v", err)
		}
		want, _ := strconv.ParseInt(config["maxmemory"], 10, 64)
		if got.Setting != "maxmemory" || got.Bytes != want {
			t.Errorf("maxmemory = %+v, CONFIG GET says %d", got, want)
		}
	})

	t.Run("mongodb", func(t *testing.T) {
		client, _ := liveMongoDB(t)
		got, err := MongoMemoryBuffer(ctx, client)
		if err != nil {
			t.Fatal(err)
		}
		// A WiredTiger cache is never smaller than mongod's floor of 256 MiB.
		if got.Setting != "wiredTigerCacheSizeGB" || got.Bytes < 256<<20 {
			t.Errorf("the WiredTiger cache = %+v", got)
		}
	})
}
