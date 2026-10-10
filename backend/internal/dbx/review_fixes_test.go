package dbx

import (
	"strings"
	"testing"
)

// A statement that merely contains INTO, CREATE or INSERT is not thereby an
// insert or a plain CREATE: only a leading verb that is fully known stays
// below "high", which is what the query runner asks the destructive
// capability for.
func TestOnlyAKnownLeadingVerbStaysBelowHigh(t *testing.T) {
	cases := []struct {
		driver Driver
		sql    string
		want   string
	}{
		{DriverSQLite, `VACUUM INTO '/tmp/copy.db'`, "high"},
		{DriverMySQL, `LOAD DATA INFILE '/etc/passwd' INTO TABLE t`, "high"},
		{DriverPostgres, `CREATE DATABASE other`, "high"},
		{DriverPostgres, `CREATE EXTENSION dblink`, "high"},
		{DriverPostgres, `CREATE LANGUAGE plpython3u`, "high"},
		{DriverPostgres, `CREATE SERVER s FOREIGN DATA WRAPPER postgres_fdw`, "high"},
		{DriverMySQL, `CREATE FUNCTION f RETURNS STRING SONAME 'udf.so'`, "high"},
		{DriverOracle, `CREATE DIRECTORY d AS '/tmp'`, "high"},
		{DriverClickHouse, `INSERT INTO FUNCTION file('x.csv', 'CSV', 'a Int8') SELECT 1`, "high"},

		{DriverPostgres, `CREATE TABLE t (a int)`, "medium"},
		{DriverPostgres, `CREATE TEMPORARY TABLE IF NOT EXISTS t (a int)`, "medium"},
		{DriverPostgres, `CREATE UNIQUE INDEX i ON t (a)`, "medium"},
		{DriverPostgres, `CREATE MATERIALIZED VIEW v AS SELECT 1`, "medium"},
		{DriverPostgres, `CREATE SCHEMA s`, "medium"},
		{DriverPostgres, `INSERT INTO t VALUES (1)`, "medium"},
		{DriverPostgres, `SELECT a INTO copy FROM t`, "medium"},
		{DriverClickHouse, `INSERT INTO t VALUES (1)`, "medium"},
		{DriverPostgres, `SELECT 1`, "read"},
	}
	for _, tc := range cases {
		if got := ClassifyFor(tc.driver, tc.sql).Level; got != tc.want {
			t.Errorf("%s: %s is %q, want %q", tc.driver, tc.sql, got, tc.want)
		}
	}
}

// PostgreSQL's U&"…" identifier names a function without spelling it, so the
// list of server functions cannot see it.
func TestAUnicodeEscapedIdentifierIsNotARead(t *testing.T) {
	for _, sql := range []string{
		`select U&"pg\005fterminate_backend"(1)`,
		`select u&"pg!005fterminate_backend"(1) UESCAPE '!'`,
	} {
		if got := ClassifyFor(DriverPostgres, sql); !got.Destructive {
			t.Errorf("%s is %q and not destructive", sql, got.Level)
		}
	}
	if got := ClassifyFor(DriverPostgres, `select pg_terminate_backend(1)`); !got.Destructive {
		t.Errorf("the plain spelling is %q", got.Level)
	}
}

// What removes data through the console needs what the key browser's own
// delete needs, wherever the option that does it is written.
func TestRedisConsoleSeesATrimOrAnExpiryWhereverItIsWritten(t *testing.T) {
	dangerous := []string{
		"XADD s LIMIT 0 MAXLEN ~ 0 * f v",
		"XADD s IDMPAUTO p MAXLEN 0 * f v",
		"XADD s IDMP a b MINID 5 * f v",
		"XADD s SOMEFUTUREOPTION 1 * f v",
		"HGETEX h FIELDS 1 a PXAT 1",
		"HSETEX h FIELDS 1 a 9 PXAT 1",
		"HSETEX h FIELDS x a 9",
		"HGETEX h PXAT 1 FIELDS 1 a",
		`EXPIRE k " 0"`,
	}
	for _, line := range dangerous {
		args, err := RedisParseCommand(line)
		if err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		if got := RedisClassify(args, nil).Class; got != RedisClassDangerous {
			t.Errorf("%s is %q, want dangerous", line, got)
		}
	}
	// A field that happens to be called EX is a field.
	for _, line := range []string{"XADD s * f v", "XADD s NOMKSTREAM 1700000000000-0 f v", "XADD s 5-* f v", "EXPIRE k 60", "HSETEX h FIELDS 1 EX 0", "HSETEX h EX 60 FIELDS 1 a 9"} {
		args, _ := RedisParseCommand(line)
		if got := RedisClassify(args, nil).Class; got != RedisClassWrite {
			t.Errorf("%s is %q, want write", line, got)
		}
	}
}

// Stopping another client's cursor stops work in flight, as killOp does.
func TestMongoKillCursorsIsNotAPlainWrite(t *testing.T) {
	_, verdict, err := MongoClassifyCommand(`{ killCursors: "c", cursors: [ { "$numberLong": "1" } ] }`)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Class != MongoClassDestructive {
		t.Errorf("killCursors is %q", verdict.Class)
	}
}

// pg_dump reads a --dbname with "=" in it, or a URI, as a whole connection
// string whose host overrides the one given beside it.
func TestADumpNameCannotBeAConnectionString(t *testing.T) {
	for _, name := range []string{"host=evil.example dbname=x", "dbname=x", "postgres://evil.example/x", "PostgreSQL://evil.example/x"} {
		if err := validateDumpDatabase(name); err == nil {
			t.Errorf("%q is accepted", name)
		}
	}
	for _, name := range []string{"shop", "my-app", "café", "Mixed Case"} {
		if err := validateDumpDatabase(name); err != nil {
			t.Errorf("%q is refused: %v", name, err)
		}
	}
	if got := mysqlDSNWithDatabase("app:pw@tcp(127.0.0.1:3306)/shop?parseTime=true", "other?allowAllFiles=true"); strings.Contains(got, "allowAllFiles=true&") || strings.HasSuffix(got, "allowAllFiles=true") && !strings.Contains(got, "%3F") {
		t.Errorf("a name carried a parameter into the connection string: %s", got)
	}
}

// A connection string written as pairs is read as pairs. Read as a URL it is
// all path, and the password came back as part of the database's name.
func TestAKeywordConnectionStringIsNotItsOwnDatabaseName(t *testing.T) {
	cases := []struct {
		driver Driver
		dsn    string
		want   ConnInfo
	}{
		{DriverPostgres, `host=db.internal port=5433 user=app password=s3cret dbname=shop sslmode=disable`,
			ConnInfo{Host: "db.internal", Port: "5433", User: "app", Password: "s3cret", Database: "shop"}},
		{DriverPostgres, `host=h user='the app' password='it\'s here' dbname='my shop'`,
			ConnInfo{Host: "h", User: "the app", Password: "it's here", Database: "my shop"}},
		{DriverMSSQL, `server=sql.internal,1444;user id=sa;password=P@ss;w0rd;database=shop`,
			ConnInfo{Host: "sql.internal", Port: "1444", User: "sa", Password: "P@ss", Database: "shop"}},
		{DriverMSSQL, `Server=tcp:host\INSTANCE;Initial Catalog=shop;UID=u;PWD=p`,
			ConnInfo{Host: "host", User: "u", Password: "p", Database: "shop"}},
	}
	for _, tc := range cases {
		got, err := ParseDSN(tc.driver, tc.dsn)
		if err != nil {
			t.Fatalf("%s: %v", tc.dsn, err)
		}
		if got.Host != tc.want.Host || got.Port != tc.want.Port || got.User != tc.want.User || got.Database != tc.want.Database {
			t.Errorf("%s reads as %+v", tc.dsn, *got)
		}
		if strings.Contains(got.Database, "assword") || strings.Contains(got.Database, "=") {
			t.Errorf("%s: the database's name carries the string: %q", tc.dsn, got.Database)
		}
	}
}

// Settings can hold a secret, and the Settings page blanks those for a role
// that is not an administrator; the table reader must not hand them over.
func TestServerSettingsAreNotReadAsATable(t *testing.T) {
	for _, tc := range []struct {
		driver        Driver
		schema, table string
	}{
		{DriverPostgres, "pg_catalog", "pg_settings"},
		{DriverPostgres, "pg_catalog", "pg_db_role_setting"},
		{DriverPostgres, "pg_catalog", "pg_file_settings"},
		{DriverMySQL, "performance_schema", "global_variables"},
	} {
		schemas := credentialRelations[tc.driver][tc.table]
		found := false
		for _, s := range schemas {
			found = found || s == tc.schema
		}
		if !found {
			t.Errorf("%s.%s on %s is readable as a table", tc.schema, tc.table, tc.driver)
		}
	}
}
