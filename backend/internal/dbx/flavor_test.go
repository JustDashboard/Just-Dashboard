package dbx

import (
	"context"
	"database/sql"
	"os"
	"testing"
)

// The INFO replies are what the four servers actually answered — read off
// running containers of each, not written from their documentation — because
// the differences that tell them apart are exactly the lines nobody would
// think to invent: Valkey still stating a redis_version, KeyDB naming itself
// nowhere but in its program's path.
const (
	infoRedis = "# Server\r\nredis_version:7.4.11\r\nredis_git_sha1:00000000\r\nredis_mode:standalone\r\n" +
		"os:Linux 6.14.0-37-generic x86_64\r\nprocess_id:1\r\ntcp_port:6379\r\n" +
		"executable:/data/redis-server\r\nconfig_file:\r\n"
	infoValkey = "# Server\r\nredis_version:7.2.4\r\nserver_name:valkey\r\nvalkey_version:8.1.10\r\n" +
		"valkey_release_stage:ga\r\nserver_mode:standalone\r\nexecutable:/data/valkey-server\r\n"
	infoKeyDB = "# Server\r\nredis_version:6.3.4\r\nredis_git_sha1:7e7e5e57\r\nredis_mode:standalone\r\n" +
		"executable:/data/keydb-server\r\nconfig_file:\r\navailability_zone:\r\nfeatures:cluster_mget\r\n"
	infoKeyDBSection = "# KeyDB\r\nmvcc_depth:0\r\n"
	infoDragonfly    = "# Server\r\nredis_version:7.4.0\r\ndragonfly_version:df-v2.0.0\r\nredis_mode:standalone\r\n" +
		"arch_bits:64\r\nthread_count:1\r\nexecutable:dragonfly\r\n"
)

func TestDetectFlavorReadsWhatTheServerSaid(t *testing.T) {
	cases := []struct {
		name    string
		driver  Driver
		signals []string
		flavor  string
		number  string
	}{
		{"postgres", DriverPostgres,
			[]string{"PostgreSQL 16.15 on x86_64-pc-linux-musl, compiled by gcc (Alpine 15.2.0) 15.2.0, 64-bit"},
			FlavorPostgres, "16.15"},
		{"timescaledb is an extension, not a version string", DriverPostgres,
			[]string{"PostgreSQL 16.4 on x86_64-pc-linux-musl, compiled by gcc", "timescaledb"},
			FlavorTimescaleDB, "16.4"},
		{"cockroachdb", DriverPostgres,
			[]string{"CockroachDB CCL v23.1.11 (x86_64-pc-linux-gnu, built 2023/09/27 01:53:43, go1.19.10)"},
			FlavorCockroachDB, "23.1.11"},
		{"yugabytedb leads with the postgres it forked", DriverPostgres,
			[]string{"PostgreSQL 11.2-YB-2.20.1.0-b0 on x86_64-pc-linux-gnu, compiled by clang version 16.0.6"},
			FlavorYugabyteDB, "2.20.1.0"},
		{"mysql", DriverMySQL, []string{"8.4.11", "MySQL Community Server - GPL"}, FlavorMySQL, "8.4.11"},
		{"mysql from a distribution package", DriverMySQL,
			[]string{"8.0.36-0ubuntu0.22.04.1", "(Ubuntu)"}, FlavorMySQL, "8.0.36"},
		{"mariadb", DriverMySQL,
			[]string{"11.8.9-MariaDB-ubu2404", "mariadb.org binary distribution"}, FlavorMariaDB, "11.8.9"},
		{"mariadb behind the replication shim", DriverMySQL,
			[]string{"5.5.5-10.6.12-MariaDB-1:10.6.12+maria~ubu2004"}, FlavorMariaDB, "10.6.12"},
		{"percona is only named in the comment", DriverMySQL,
			[]string{"8.0.35-27", "Percona Server (GPL), Release 27, Revision 2f8a1ea0"}, FlavorPercona, "8.0.35-27"},
		{"tidb", DriverMySQL, []string{"8.0.11-TiDB-v7.5.0", "TiDB Server (Apache License 2.0)"}, FlavorTiDB, "7.5.0"},
		{"redis", DriverRedis, []string{infoRedis}, FlavorRedis, "7.4.11"},
		{"valkey reports its own release, not the redis it imitates", DriverRedis,
			[]string{infoValkey}, FlavorValkey, "8.1.10"},
		{"keydb by its program", DriverRedis, []string{infoKeyDB}, FlavorKeyDB, "6.3.4"},
		{"keydb by its own section when the program was renamed", DriverRedis,
			[]string{"# Server\r\nredis_version:6.3.4\r\nexecutable:/usr/bin/cache\r\n", infoKeyDBSection},
			FlavorKeyDB, "6.3.4"},
		{"dragonfly", DriverRedis, []string{infoDragonfly}, FlavorDragonfly, "2.0.0"},
		{"mongodb", DriverMongo, []string{"version", "gitVersion", "versionArray"}, FlavorMongoDB, ""},
		{"ferretdb names itself in a key", DriverMongo,
			[]string{"version", "gitVersion", "ferretdbVersion"}, FlavorFerretDB, ""},
		{"sql server", DriverMSSQL,
			[]string{"Microsoft SQL Server 2022 (RTM-CU12) (KB5033663) - 16.0.4115.5 (X64) \n\tMar  4 2024"},
			FlavorSQLServer, "16.0.4115.5"},
		{"azure sql edge", DriverMSSQL,
			[]string{"Microsoft Azure SQL Edge Developer (RTM) - 15.0.2000.1574 (ARM64) \n\tJan 25 2023"},
			FlavorAzureSQLEdge, "15.0.2000.1574"},
		{"clickhouse", DriverClickHouse, []string{"ClickHouse 24.8.14.39"}, FlavorClickHouse, "24.8.14.39"},
		{"sqlite", DriverSQLite, []string{"SQLite 3.46.0"}, FlavorSQLite, "3.46.0"},
		{"oracle", DriverOracle,
			[]string{"Oracle Database 23ai Free Release 23.0.0.0.0 - Develop, Learn, and Run for Free"},
			FlavorOracle, "23.0.0.0.0"},
		{"a server that was never asked is its driver's own product", DriverMySQL, nil, FlavorMySQL, ""},
	}
	for _, c := range cases {
		flavor := DetectFlavor(c.driver, c.signals...)
		if flavor != c.flavor {
			t.Errorf("%s: flavour = %q, want %q", c.name, flavor, c.flavor)
			continue
		}
		if c.driver == DriverMongo {
			continue
		}
		if number := VersionNumber(c.driver, flavor, c.signals...); number != c.number {
			t.Errorf("%s: version number = %q, want %q", c.name, number, c.number)
		}
	}
}

// A server that has not been asked, or said nothing that names a fork, is its
// driver's own product. The answer must never be a guess from the driver.
func TestDetectFlavorWithoutASignalIsTheDriversOwn(t *testing.T) {
	for _, d := range Drivers() {
		if got := DetectFlavor(d); got != DefaultFlavor(d) {
			t.Errorf("%s with no signal = %q, want its own flavour %q", d, got, DefaultFlavor(d))
		}
		if got := DetectFlavor(d, "an unremarkable 1.2.3 server"); got != DefaultFlavor(d) {
			t.Errorf("%s with an unremarkable signal = %q, want %q", d, got, DefaultFlavor(d))
		}
	}
}

// The flavour table is the shared vocabulary: every driver has its own
// flavour first, every flavour has a name to print, and none is claimed by two
// drivers — a flavour id alone has to say which code path opens it.
func TestFlavorTableIsCoherent(t *testing.T) {
	owner := map[string]Driver{}
	for _, d := range Drivers() {
		list := Flavors(d)
		if len(list) == 0 {
			t.Errorf("%s has no flavours", d)
			continue
		}
		if list[0] != string(d) || DefaultFlavor(d) != string(d) {
			t.Errorf("%s: its own flavour must come first and carry its id, got %q", d, list[0])
		}
		for _, f := range list {
			if prev, taken := owner[f]; taken {
				t.Errorf("flavour %q belongs to both %s and %s", f, prev, d)
			}
			owner[f] = d
			if FlavorLabel(f) == f {
				t.Errorf("flavour %q has no label", f)
			}
			if !FlavorOf(d, f) {
				t.Errorf("FlavorOf(%s, %q) = false", d, f)
			}
		}
		if FlavorOf(d, "nonesuch") {
			t.Errorf("FlavorOf(%s, nonesuch) = true", d)
		}
	}
}

func TestShortVersionFitsATile(t *testing.T) {
	for in, want := range map[string]string{
		"PostgreSQL 16.15 on x86_64-pc-linux-musl, compiled by gcc (Alpine 15.2.0) 15.2.0, 64-bit": "PostgreSQL 16.15",
		"11.8.9-MariaDB-ubu2404":    "11.8.9-MariaDB-ubu2404",
		"  ClickHouse 24.8.14.39\n": "ClickHouse 24.8.14.39",
		"Microsoft SQL Server 2022 (RTM-CU12) (KB5033663) - 16.0.4115.5 (X64) \n\tMar  4 2024":        "Microsoft SQL Server 2022 (RTM-CU12) (KB5033663)",
		"Oracle Database 23ai Free Release 23.0.0.0.0 - Develop, Learn, and Run for Free":             "Oracle Database 23ai Free Release 23.0.0.0.0",
		"A product whose own name keeps going for rather longer than any tile could ever hope to fit": "A product whose own name keeps going for rather",
		"": "",
	} {
		if got := ShortVersion(in); got != want {
			t.Errorf("ShortVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

// The probes against real servers. Each engine is read from the environment
// and skipped when its variable is unset: unlike the older live tests these
// never fall back to an engine's standard port, because on a machine that
// runs databases the standard port is somebody's production server.
func TestLiveIdentifySQL(t *testing.T) {
	for _, c := range []struct {
		driver Driver
		env    string
		// flavor is empty where the variable may point at either fork.
		flavor string
	}{
		{DriverPostgres, "JD_TEST_POSTGRES_DSN", FlavorPostgres},
		{DriverMySQL, "JD_TEST_MYSQL_DSN", ""},
		{DriverMySQL, "JD_TEST_MYSQL8_DSN", FlavorMySQL},
		{DriverMySQL, "JD_TEST_MARIADB_DSN", FlavorMariaDB},
		{DriverClickHouse, "JD_TEST_CLICKHOUSE_DSN", FlavorClickHouse},
	} {
		t.Run(c.env, func(t *testing.T) {
			dsn := os.Getenv(c.env)
			if dsn == "" {
				t.Skipf("%s unset", c.env)
			}
			d, err := DialectFor(c.driver)
			if err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open(d.SQLDriverName(), d.NormaliseDSN(dsn))
			if err != nil {
				t.Skipf("%s unavailable: %v", c.driver, err)
			}
			defer db.Close()
			if err := db.PingContext(context.Background()); err != nil {
				t.Skipf("%s unreachable: %v", c.driver, err)
			}
			got := IdentifySQL(context.Background(), db, c.driver)
			if !FlavorOf(c.driver, got.Flavor) {
				t.Errorf("flavour %q is not one of %s's", got.Flavor, c.driver)
			}
			if c.flavor != "" && got.Flavor != c.flavor {
				t.Errorf("flavour = %q, want %q (%+v)", got.Flavor, c.flavor, got)
			}
			if got.Version == "" || got.Number == "" {
				t.Errorf("no version read: %+v", got)
			}
		})
	}
}

func TestLiveIdentifyRedisFamily(t *testing.T) {
	for _, c := range []struct{ env, flavor string }{
		{"JD_TEST_REDIS_DSN", FlavorRedis},
		{"JD_TEST_VALKEY_DSN", FlavorValkey},
		{"JD_TEST_KEYDB_DSN", FlavorKeyDB},
		{"JD_TEST_DRAGONFLY_DSN", FlavorDragonfly},
	} {
		t.Run(c.env, func(t *testing.T) {
			dsn := os.Getenv(c.env)
			if dsn == "" {
				t.Skipf("%s unset", c.env)
			}
			client, err := RedisClient(context.Background(), dsn, 0)
			if err != nil {
				t.Skipf("unreachable: %v", err)
			}
			defer client.Close()
			got := IdentifyRedis(context.Background(), client)
			if got.Flavor != c.flavor || got.Number == "" {
				t.Errorf("identity = %+v, want flavour %q with a version", got, c.flavor)
			}
		})
	}
}

func TestLiveIdentifyMongo(t *testing.T) {
	dsn := os.Getenv("JD_TEST_MONGO_DSN")
	if dsn == "" {
		t.Skip("JD_TEST_MONGO_DSN unset")
	}
	client, err := MongoClient(context.Background(), dsn)
	if err != nil {
		t.Skipf("mongodb unreachable: %v", err)
	}
	defer client.Disconnect(context.Background())
	got := IdentifyMongo(context.Background(), client)
	if got.Flavor != FlavorMongoDB || got.Number == "" {
		t.Errorf("identity = %+v, want mongodb with a version", got)
	}
}
