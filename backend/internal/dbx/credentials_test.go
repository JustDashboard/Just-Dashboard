package dbx

import (
	"bytes"
	"errors"
	"testing"
)

// The relations that hold what accounts sign in with are refused by name, and
// a name that only looks like one is not. No server is asked: a request that
// names its schema is judged by what it names.
func TestCredentialRelationsAreRefusedByName(t *testing.T) {
	for _, c := range []struct {
		driver        Driver
		schema, table string
		withheld      bool
	}{
		{DriverPostgres, "pg_catalog", "pg_authid", true},
		{DriverPostgres, "pg_catalog", "pg_shadow", true},
		{DriverPostgres, "PG_CATALOG", "PG_SHADOW", true},
		{DriverPostgres, "pg_catalog", "pg_user_mapping", true},
		{DriverPostgres, "pg_catalog", "pg_subscription", true},
		{DriverPostgres, "information_schema", "user_mapping_options", true},
		// The views that show accounts with the verifier blanked stay readable.
		{DriverPostgres, "pg_catalog", "pg_roles", false},
		{DriverPostgres, "pg_catalog", "pg_user", false},
		{DriverPostgres, "public", "pg_authid", false},
		{DriverMySQL, "mysql", "user", true},
		{DriverMySQL, "mysql", "global_priv", true},
		{DriverMySQL, "MySQL", "User", true},
		{DriverMySQL, "mysql", "db", false},
		// An application's own table of that name.
		{DriverMySQL, "shop", "user", false},
		{DriverMSSQL, "sys", "sql_logins", true},
		{DriverMSSQL, "dbo", "syslogins", true},
		{DriverMSSQL, "sys", "server_principals", false},
		{DriverMSSQL, "dbo", "sql_logins", false},
		{DriverOracle, "SYS", "USER$", true},
		{DriverOracle, "SYS", "DBA_USERS", true},
		{DriverOracle, "PUBLIC", "DBA_USERS", true},
		{DriverOracle, "SYS", "ALL_USERS", false},
		{DriverOracle, "APP", "USER$", false},
		// Engines with no such relation refuse nothing.
		{DriverClickHouse, "system", "users", false},
		{DriverSQLite, "", "user", false},
	} {
		d, err := DialectFor(c.driver)
		if err != nil {
			t.Fatal(err)
		}
		err = guardCredentials(t.Context(), nil, d, c.schema, c.table)
		if got := errors.Is(err, ErrCredentialsWithheld); got != c.withheld {
			t.Errorf("%s %s.%s: withheld = %v (%v), want %v", c.driver, c.schema, c.table, got, err, c.withheld)
		}
	}
}

// On the servers: every form that reads a table by name refuses the credential
// catalogues, named in full or left for the engine to resolve, and still reads
// the catalogue beside them.
func TestLiveReadSurfaceWithholdsCredentialCatalogues(t *testing.T) {
	for _, e := range []struct {
		driver        Driver
		env, fallback string
		// withheld are named as a request would name them; an empty schema is
		// one the engine resolves. open is a catalogue relation beside them.
		withheld   [][2]string
		open       [2]string
		searchable string
	}{
		{DriverPostgres, "JD_TEST_POSTGRES_DSN", "postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable",
			[][2]string{{"pg_catalog", "pg_authid"}, {"pg_catalog", "pg_shadow"}, {"", "pg_authid"}, {"", "pg_shadow"}},
			[2]string{"pg_catalog", "pg_roles"}, "pg_catalog"},
		// The administrator's connection names no database, so the mysql
		// schema is named; the application's names one, and resolves to it.
		{DriverMySQL, "JD_TEST_MYSQL_ADMIN_DSN", "root:jdtest@tcp(127.0.0.1:3306)/",
			[][2]string{{"mysql", "user"}, {"mysql", "global_priv"}},
			[2]string{"mysql", "db"}, "mysql"},
		{DriverMSSQL, "JD_TEST_MSSQL_DSN", "sqlserver://sa:JdTest%232024pw@127.0.0.1:1433?database=master&encrypt=disable",
			[][2]string{{"sys", "sql_logins"}, {"sys", "syslogins"}, {"", "syslogins"}},
			[2]string{"sys", "server_principals"}, ""},
		{DriverOracle, "JD_TEST_ORACLE_ADMIN_DSN", "oracle://system:JdTest2024@127.0.0.1:1521/FREEPDB1",
			[][2]string{{"SYS", "USER$"}, {"SYS", "DBA_USERS"}, {"", "DBA_USERS"}},
			[2]string{"SYS", "ALL_USERS"}, ""},
	} {
		t.Run(string(e.driver), func(t *testing.T) {
			db := liveSQL(t, e.driver, e.env, e.fallback)
			ctx := t.Context()
			for _, rel := range e.withheld {
				opts := BrowseOptions{Schema: rel[0], Table: rel[1]}
				_, browseErr := Browse(ctx, db, e.driver, opts)
				_, pageErr := BrowseTablePage(ctx, db, e.driver, opts)
				_, countErr := Count(ctx, db, e.driver, opts)
				var file bytes.Buffer
				_, _, exportErr := ExportSelection(ctx, db, e.driver, opts, ExportOptions{Format: ExportCSV}, &file)
				_, cellErr := ReadCell(ctx, db, e.driver, rel[0], rel[1], "name", map[string]any{"name": "x"})
				for form, err := range map[string]error{"browse": browseErr, "page": pageErr, "count": countErr, "export": exportErr, "cell": cellErr} {
					if !errors.Is(err, ErrCredentialsWithheld) {
						t.Errorf("%s of %q.%q = %v, want it withheld", form, rel[0], rel[1], err)
					}
				}
				if file.Len() != 0 {
					t.Errorf("the export of %q.%q wrote %d bytes before it was refused", rel[0], rel[1], file.Len())
				}
			}
			if res, err := Browse(ctx, db, e.driver, BrowseOptions{Schema: e.open[0], Table: e.open[1], Limit: 5}); err != nil || len(res.Rows) == 0 {
				t.Errorf("%s.%s, which holds no credentials, = %v", e.open[0], e.open[1], err)
			}
			if e.searchable == "" {
				return
			}
			// A needle every account row holds: the search reads the schema's
			// other tables and never the withheld ones.
			found, err := Search(ctx, db, e.driver, e.searchable, "a")
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range found.Matches {
				if guardCredentials(ctx, db, mustDialect(t, e.driver), e.searchable, m.Table) != nil {
					t.Errorf("the search of %s answered with rows of %s", e.searchable, m.Table)
				}
			}
		})
	}
	// A connection on the mysql database reads its tables with no schema
	// named, and one on an application's database does not reach them that way.
	t.Run("mysql with no schema named", func(t *testing.T) {
		admin := liveDSN(t, "JD_TEST_MYSQL_ADMIN_DSN", "root:jdtest@tcp(127.0.0.1:3306)/")
		t.Setenv("JD_TEST_CREDENTIALS_MYSQL_DSN", admin+"mysql")
		onMySQL := liveSQL(t, DriverMySQL, "JD_TEST_CREDENTIALS_MYSQL_DSN", "")
		if _, err := Browse(t.Context(), onMySQL, DriverMySQL, BrowseOptions{Table: "user"}); !errors.Is(err, ErrCredentialsWithheld) {
			t.Errorf("user, read on the mysql database with no schema named = %v, want it withheld", err)
		}
		app := liveSQL(t, DriverMySQL, "JD_TEST_MYSQL_DSN", "jdtest:jdtest@tcp(127.0.0.1:3306)/jdtest")
		d := mustDialect(t, DriverMySQL)
		if err := guardCredentials(t.Context(), app, d, "", "user"); err != nil {
			t.Errorf("a table called user on an application's database was withheld: %v", err)
		}
	})
}
