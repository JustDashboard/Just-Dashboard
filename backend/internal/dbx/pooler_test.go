package dbx

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestPoolerSettingsAreHeldToTheirBounds(t *testing.T) {
	if err := DefaultPoolerSettings().Validate(); err != nil {
		t.Fatalf("the defaults are refused: %v", err)
	}
	for name, change := range map[string]func(*PoolerSettings){
		"an unknown pool mode":       func(s *PoolerSettings) { s.PoolMode = "statement" },
		"an empty pool mode":         func(s *PoolerSettings) { s.PoolMode = "" },
		"a pool of none":             func(s *PoolerSettings) { s.PoolSize = 0 },
		"a pool past the limit":      func(s *PoolerSettings) { s.PoolSize = PoolerMaxPoolSize + 1 },
		"no clients":                 func(s *PoolerSettings) { s.MaxClientConn = 0 },
		"clients past the limit":     func(s *PoolerSettings) { s.MaxClientConn = PoolerMaxClientConn + 1 },
		"an unknown authentication":  func(s *PoolerSettings) { s.AuthMode = "trust" },
		"a pool mode with a newline": func(s *PoolerSettings) { s.PoolMode = "session\nauth_type = trust" },
	} {
		settings := DefaultPoolerSettings()
		change(&settings)
		if err := settings.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func testPoolerConfig() PoolerConfig {
	return PoolerConfig{
		Settings: DefaultPoolerSettings(), Server: "jd-postgres", ServerPort: 5432,
		Database: "app", User: "app", Password: "s3cret", AuthPassword: "authpw", StatsPassword: "statspw",
	}
}

func TestThePoolerConfigurationStatesTheSettings(t *testing.T) {
	cfg := testPoolerConfig()
	cfg.Settings.PoolMode, cfg.Settings.PoolSize, cfg.Settings.MaxClientConn = PoolModeSession, 12, 345
	ini, err := cfg.INI()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"* = host=jd-postgres port=5432",
		"listen_port = 6432",
		"auth_type = scram-sha-256",
		"auth_file = /etc/pgbouncer/userlist.txt",
		"pool_mode = session",
		"default_pool_size = 12",
		"max_client_conn = 345",
		"stats_users = jd_pooler_stats",
	} {
		if !strings.Contains(ini, want+"\n") {
			t.Errorf("the configuration lacks %q:\n%s", want, ini)
		}
	}
	for _, never := range []string{"s3cret", "authpw", "statspw", "auth_query", "admin_users"} {
		if strings.Contains(ini, never) {
			t.Errorf("the configuration holds %q:\n%s", never, ini)
		}
	}
}

func TestAuthQueryModeNamesTheFunctionAndLeavesTheConnectionOutOfTheUserlist(t *testing.T) {
	cfg := testPoolerConfig()
	cfg.Settings.AuthMode = PoolerAuthQuery
	ini, err := cfg.INI()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"auth_type = md5",
		"auth_user = jd_pgbouncer",
		"auth_dbname = app",
		"auth_query = SELECT usename, passwd FROM jd_pgbouncer.get_auth($1)",
	} {
		if !strings.Contains(ini, want+"\n") {
			t.Errorf("the configuration lacks %q:\n%s", want, ini)
		}
	}
	list, err := cfg.Userlist()
	if err != nil {
		t.Fatal(err)
	}
	if want := "\"jd_pgbouncer\" \"authpw\"\n\"jd_pooler_stats\" \"statspw\"\n"; list != want {
		t.Errorf("userlist = %q, want %q", list, want)
	}
}

func TestTheUserlistHoldsTheConnectionsAccountAndEscapesWhatNeedsIt(t *testing.T) {
	cfg := testPoolerConfig()
	cfg.User, cfg.Password = `we"ird`, `pa"ss`
	list, err := cfg.Userlist()
	if err != nil {
		t.Fatal(err)
	}
	if want := "\"we\"\"ird\" \"pa\"\"ss\"\n\"jd_pooler_stats\" \"statspw\"\n"; list != want {
		t.Errorf("userlist = %q, want %q", list, want)
	}
}

func TestAnAccountThatCannotBeWrittenIsRefusedNotTruncated(t *testing.T) {
	for name, change := range map[string]func(*PoolerConfig){
		"a newline in the password":  func(c *PoolerConfig) { c.Password = "a\n\"evil\" \"x\"" },
		"a NUL in the user":          func(c *PoolerConfig) { c.User = "a\x00b" },
		"no password":                func(c *PoolerConfig) { c.Password = "" },
		"no user":                    func(c *PoolerConfig) { c.User = "" },
		"no stats password":          func(c *PoolerConfig) { c.StatsPassword = "" },
		"a carriage return in stats": func(c *PoolerConfig) { c.StatsPassword = "a\rb" },
	} {
		cfg := testPoolerConfig()
		change(&cfg)
		if list, err := cfg.Userlist(); err == nil {
			t.Errorf("%s was written: %q", name, list)
		}
	}
}

func TestThePoolerConfigurationRefusesWhatWouldInjectASetting(t *testing.T) {
	for name, change := range map[string]func(*PoolerConfig){
		"a server name with a space":      func(c *PoolerConfig) { c.Server = "db host=evil" },
		"a server name with a newline":    func(c *PoolerConfig) { c.Server = "db\nauth_type = trust" },
		"an empty server name":            func(c *PoolerConfig) { c.Server = "" },
		"no port":                         func(c *PoolerConfig) { c.ServerPort = 0 },
		"invalid settings":                func(c *PoolerConfig) { c.Settings.PoolSize = -1 },
		"a database name with a newline":  func(c *PoolerConfig) { c.Settings.AuthMode = PoolerAuthQuery; c.Database = "app\nauth_type = trust" },
		"an empty database in auth_query": func(c *PoolerConfig) { c.Settings.AuthMode = PoolerAuthQuery; c.Database = "" },
	} {
		cfg := testPoolerConfig()
		change(&cfg)
		if ini, err := cfg.INI(); err == nil {
			t.Errorf("%s was written:\n%s", name, ini)
		}
	}
}

func TestTheAuthQueryStatementsSendAVerifierAndNeverGrantMoreThanTheFunction(t *testing.T) {
	statements := PoolerAuthStatements("SCRAM-SHA-256$4096:c2FsdA==$c3RvcmVk:c2VydmVy")
	if !strings.HasPrefix(statements[0], "CREATE ROLE jd_pgbouncer WITH LOGIN NOSUPERUSER") ||
		!strings.HasSuffix(statements[0], "PASSWORD 'SCRAM-SHA-256$4096:c2FsdA==$c3RvcmVk:c2VydmVy'") {
		t.Errorf("first statement = %s", statements[0])
	}
	all := strings.Join(statements, "\n")
	for _, want := range []string{
		"CREATE SCHEMA jd_pgbouncer\n",
		"SECURITY DEFINER SET search_path = pg_catalog",
		"FROM pg_catalog.pg_authid a WHERE a.rolname = p_usename AND a.rolcanlogin",
		"CASE WHEN a.rolvaliduntil < now() THEN NULL ELSE a.rolpassword END",
		"REVOKE ALL ON FUNCTION jd_pgbouncer.get_auth(text) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION jd_pgbouncer.get_auth(text) TO jd_pgbouncer",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the statements lack %q:\n%s", want, all)
		}
	}
	for _, never := range []string{"GRANT ALL", "IF NOT EXISTS", "OR REPLACE", "ALTER ROLE"} {
		if strings.Contains(strings.ToUpper(all), never) {
			t.Errorf("the statements contain %q, which could change what is not theirs:\n%s", never, all)
		}
	}
	removal := strings.Join(PoolerAuthRemoval(), "\n")
	for _, want := range []string{"DROP FUNCTION IF EXISTS jd_pgbouncer.get_auth(text)", "DROP SCHEMA IF EXISTS jd_pgbouncer", "DROP ROLE IF EXISTS jd_pgbouncer"} {
		if !strings.Contains(removal, want) {
			t.Errorf("the removal lacks %q:\n%s", want, removal)
		}
	}
}

func TestAScramVerifierIsInPostgresFormAndSaltedEachTime(t *testing.T) {
	shape := regexp.MustCompile(`^SCRAM-SHA-256\$4096:[A-Za-z0-9+/]{22}==\$[A-Za-z0-9+/]{43}=:[A-Za-z0-9+/]{43}=$`)
	first, err := ScramVerifier("a-password")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := ScramVerifier("a-password")
	if !shape.MatchString(first) || first == second {
		t.Errorf("verifiers %q and %q", first, second)
	}
	if strings.Contains(first, "a-password") {
		t.Error("the verifier holds the password")
	}
	// The same salt and password always make the same secret; another password does not.
	salt := []byte("0123456789abcdef")
	one, _ := scramVerifier("pencil", salt, 4096)
	again, _ := scramVerifier("pencil", salt, 4096)
	other, _ := scramVerifier("pen", salt, 4096)
	if one != again || one == other {
		t.Errorf("%q %q %q", one, again, other)
	}
}

func TestThePoolerImageIsPinnedByVersionAndDigest(t *testing.T) {
	if !regexp.MustCompile(`^[a-z0-9/_-]+:v\d+\.\d+\.\d+-p\d+@sha256:[0-9a-f]{64}$`).MatchString(PoolerImage) {
		t.Errorf("image = %s", PoolerImage)
	}
}

func TestThePoolerConsoleIsReachedAsItsReadOnlyAccount(t *testing.T) {
	dsn := PoolerAdminDSN("127.0.0.1", 6433, "p@ss/word")
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := u.User.Password(); u.User.Username() != PoolerStatsUser || got != "p@ss/word" {
		t.Errorf("account = %s / %q", u.User.Username(), got)
	}
	if u.Host != "127.0.0.1:6433" || u.Path != "/pgbouncer" {
		t.Errorf("address = %s%s", u.Host, u.Path)
	}
	if q := u.Query(); q.Get("default_query_exec_mode") != "simple_protocol" || q.Get("sslmode") != "disable" {
		t.Errorf("the console needs the simple protocol without TLS: %s", u.RawQuery)
	}
}

func TestPoolRowsAreReadByColumnNameWhateverTheRelease(t *testing.T) {
	pool := poolerPoolOf(map[string]string{
		"database": "app", "user": "app", "pool_mode": "transaction",
		"cl_active": "3", "cl_waiting": "1", "sv_active": "2", "sv_idle": "4", "sv_used": "5",
		"sv_tested": "0", "sv_login": "1", "maxwait": "7", "future_column": "x",
	})
	want := PoolerPool{Database: "app", User: "app", PoolMode: "transaction", ClActive: 3, ClWaiting: 1,
		SvActive: 2, SvIdle: 4, SvUsed: 5, SvLogin: 1, MaxWaitSec: 7}
	if pool != want {
		t.Errorf("pool = %+v, want %+v", pool, want)
	}
	stat := poolerStatOf(map[string]string{
		"database": "app", "total_xact_count": "10", "total_query_count": "20", "total_received": "300",
		"total_sent": "400", "avg_query_time": "1500", "avg_wait_time": "bogus",
	})
	if stat.TotalXactCount != 10 || stat.TotalQueryCnt != 20 || stat.TotalReceived != 300 || stat.TotalSent != 400 ||
		stat.AvgQueryTime != 1500 || stat.AvgWaitTime != 0 || stat.AvgRecv != 0 {
		t.Errorf("stat = %+v", stat)
	}
	if got := poolerVersion("PgBouncer 1.25.2"); got != "1.25.2" {
		t.Errorf("version = %q", got)
	}
}

func TestTheDashboardsPoolerIsNotMistakenForADatabaseServer(t *testing.T) {
	ports := []PublishedPort{{ContainerPort: PoolerListenPort, HostIP: "127.0.0.1", HostPort: 6433}}
	for _, image := range []string{PoolerImage, "edoburu/pgbouncer", "bitnami/pgbouncer:1.23.1"} {
		if candidate, _ := Detect("jd-postgres-pgbouncer", image, nil, ports, nil); candidate != nil {
			t.Errorf("%s was recognised as a %s server", image, candidate.Driver)
		}
	}
}

func TestOnlyPostgresProperIsOfferedAPooler(t *testing.T) {
	for _, tc := range []struct {
		driver Driver
		flavor string
		want   bool
	}{
		{DriverPostgres, FlavorPostgres, true},
		{DriverPostgres, FlavorTimescaleDB, true},
		{DriverPostgres, FlavorCockroachDB, false},
		{DriverPostgres, FlavorYugabyteDB, false},
		{DriverMySQL, FlavorMySQL, false},
		{DriverRedis, FlavorRedis, false},
		{DriverMongo, FlavorMongoDB, false},
		{DriverSQLite, FlavorSQLite, false},
	} {
		if got, _ := Capabilities(tc.driver, tc.flavor)["pooler"].(bool); got != tc.want {
			t.Errorf("pooler for %s/%s = %v, want %v", tc.driver, tc.flavor, got, tc.want)
		}
	}
}
