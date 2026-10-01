package dbx

import (
	"strings"
	"testing"
	"time"
)

// The pure halves of the operations surface: what a statement is rendered
// as, what a request is refused for, and what is derived from rows once they
// are read. None of these need a server.

// An alter emits only the attributes the request carried. This is the whole
// of the password-change bug: the old renderer wrote all four flags every
// time, from booleans that were false when nothing was sent.
func TestPostgresAlterRoleRendersOnlyWhatWasSent(t *testing.T) {
	yes, no := true, false
	until := "infinity"
	for _, c := range []struct {
		name string
		spec RoleSpec
		want string
	}{
		{"password only", RoleSpec{Password: "s3cret", SetPassword: true}, `PASSWORD 's3cret'`},
		{"clear password", RoleSpec{SetPassword: true}, `PASSWORD NULL`},
		{"one flag off", RoleSpec{SetSuperuser: true}, `NOSUPERUSER`},
		{"one flag on", RoleSpec{SetCreateDB: true, CreateDB: true}, `CREATEDB`},
		{"login off", RoleSpec{SetLogin: true}, `NOLOGIN`},
		{"limit", RoleSpec{ConnLimit: -1}, `CONNECTION LIMIT -1`},
		{"optional attributes", RoleSpec{Inherit: &no, Replication: &yes, BypassRLS: &no}, `NOINHERIT REPLICATION NOBYPASSRLS`},
		{"valid until", RoleSpec{ValidUntil: &until}, `VALID UNTIL 'infinity'`},
		{"quote in password", RoleSpec{Password: `a'b`, SetPassword: true}, `PASSWORD 'a''b'`},
	} {
		got, err := pgRoleOptions(c.spec, false)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: ALTER ROLE … WITH %s, want %s", c.name, got, c.want)
		}
	}
	if _, err := pgRoleOptions(RoleSpec{}, false); err == nil {
		t.Error("an alter with nothing to change rendered a statement")
	}
	locked := true
	if _, err := pgRoleOptions(RoleSpec{Locked: &locked}, false); err == nil {
		t.Error("PostgreSQL has no locked attribute and the alter accepted one")
	}
	// A create states the shape of the new role in full.
	got, err := pgRoleOptions(RoleSpec{Login: true, Password: "pw", SetPassword: true}, true)
	if err != nil || got != `LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD 'pw'` {
		t.Errorf("create: %q %v", got, err)
	}
}

func TestPostgresValidUntilIsParsedNotQuoted(t *testing.T) {
	for in, want := range map[string]string{
		"":                          `'infinity'`,
		"Infinity":                  `'infinity'`,
		"2031-05-06":                `'2031-05-06 00:00:00+00'`,
		"2031-05-06T07:08:09Z":      `'2031-05-06 07:08:09+00'`,
		"2031-05-06T09:08:09+02:00": `'2031-05-06 07:08:09+00'`,
	} {
		got, err := pgValidUntil(in)
		if err != nil || got != want {
			t.Errorf("pgValidUntil(%q) = %s, %v; want %s", in, got, err, want)
		}
	}
	for _, bad := range []string{"tomorrow", "2031-05-06'; DROP ROLE x; --", "31/12/2030"} {
		if got, err := pgValidUntil(bad); err == nil {
			t.Errorf("pgValidUntil(%q) = %s, want a refusal", bad, got)
		}
	}
}

// What an account is granted on every table of today is granted on the
// tables of tomorrow too, for each role that may create one.
func TestPostgresDatabaseGrantStatements(t *testing.T) {
	d := postgresDialect{}
	stmts, err := pgGrantStatements(d, DatabaseGrant{Role: "app", Database: "shop", Level: GrantWrite},
		[]string{"public", "billing"}, map[string][]string{"billing": {"migrator"}})
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(stmts, "\n")
	for _, want := range []string{
		`GRANT CONNECT ON DATABASE "shop" TO "app"`,
		`GRANT USAGE ON SCHEMA "billing" TO "app"`,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA "billing" TO "app"`,
		`GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA "public" TO "app"`,
		`ALTER DEFAULT PRIVILEGES IN SCHEMA "public" GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO "app"`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE "migrator" IN SCHEMA "billing" GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO "app"`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %s in:\n%s", want, all)
		}
	}
	if strings.Contains(all, `FOR ROLE "migrator" IN SCHEMA "public"`) {
		t.Errorf("a default privilege was written for an owner with no tables in that schema:\n%s", all)
	}
	if strings.Contains(all, "FUNCTIONS") {
		t.Errorf("the write level grants on functions:\n%s", all)
	}
	if _, err := pgGrantStatements(d, DatabaseGrant{Role: "app", Database: "shop", Level: "owner"}, []string{"public"}, nil); err == nil {
		t.Error("an unknown level was rendered")
	}
}

func TestPrivilegeStatementsPerEngine(t *testing.T) {
	for _, c := range []struct {
		driver Driver
		change PrivilegeChange
		want   []string
	}{
		{DriverPostgres, PrivilegeChange{Role: "app", Level: GrantOnDatabase, Database: "shop", Privileges: []string{"connect", "Temporary"}},
			[]string{`GRANT CONNECT, TEMPORARY ON DATABASE "shop" TO "app"`}},
		{DriverPostgres, PrivilegeChange{Role: "app", Level: GrantOnSchema, Schema: "billing", Privileges: []string{"ALL"}, GrantOption: true},
			[]string{`GRANT ALL PRIVILEGES ON SCHEMA "billing" TO "app" WITH GRANT OPTION`}},
		{DriverPostgres, PrivilegeChange{Role: "app", Level: GrantOnTable, Schema: "billing", Privileges: []string{"SELECT"}, Future: true, Revoke: true},
			[]string{`REVOKE SELECT ON ALL TABLES IN SCHEMA "billing" FROM "app"`,
				`ALTER DEFAULT PRIVILEGES IN SCHEMA "billing" REVOKE SELECT ON TABLES FROM "app"`}},
		{DriverPostgres, PrivilegeChange{Role: "app", Level: GrantOnTable, Schema: "billing", Table: "inv\"oices", Privileges: []string{"update"}, Revoke: true, GrantOption: true},
			[]string{`REVOKE GRANT OPTION FOR UPDATE ON TABLE "billing"."inv""oices" FROM "app"`}},
		{DriverPostgres, PrivilegeChange{Role: "app", Level: GrantOnSequence, Schema: "billing", Privileges: []string{"usage"}},
			[]string{`GRANT USAGE ON ALL SEQUENCES IN SCHEMA "billing" TO "app"`}},
		{DriverPostgres, PrivilegeChange{Role: "app", Level: GrantOnRole, MemberOf: "readers"},
			[]string{`GRANT "readers" TO "app"`}},
		{DriverMySQL, PrivilegeChange{Role: "app", Host: "10.%", Level: GrantOnDatabase, Database: "shop_eu", Privileges: []string{"select", "show view"}},
			[]string{"GRANT SELECT, SHOW VIEW ON `shop\\_eu`.* TO 'app'@'10.%'"}},
		{DriverMySQL, PrivilegeChange{Role: "app", Level: GrantOnTable, Database: "shop_eu", Table: "orders", Privileges: []string{"ALL"}, Revoke: true},
			[]string{"REVOKE ALL PRIVILEGES ON `shop_eu`.`orders` FROM 'app'@'%'"}},
		{DriverClickHouse, PrivilegeChange{Role: "app", Level: GrantOnTable, Database: "logs", Table: "events", Privileges: []string{"select", "optimize"}},
			[]string{"GRANT SELECT, OPTIMIZE ON `logs`.`events` TO `app`"}},
		{DriverClickHouse, PrivilegeChange{Role: "app", Level: GrantOnDatabase, Database: "logs", Privileges: []string{"ALL"}, Revoke: true},
			[]string{"REVOKE ALL ON `logs`.* FROM `app`"}},
		{DriverMSSQL, PrivilegeChange{Role: "app", Level: GrantOnSchema, Schema: "sales", Privileges: []string{"select"}, Revoke: true},
			[]string{"REVOKE SELECT ON SCHEMA::[sales] FROM [app] CASCADE"}},
	} {
		got, err := PrivilegeStatements(c.driver, c.change)
		if err != nil {
			t.Errorf("%s %+v: %v", c.driver, c.change, err)
			continue
		}
		if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s:\n got %s\nwant %s", c.driver, strings.Join(got, "\n"), strings.Join(c.want, "\n"))
		}
	}
	// A grant on SQL Server makes the database user first, where there is none.
	got, err := PrivilegeStatements(DriverMSSQL, PrivilegeChange{Role: "o'brien", Level: GrantOnTable, Schema: "dbo", Table: "t", Privileges: []string{"SELECT"}})
	if err != nil || len(got) != 2 || !strings.Contains(got[0], "N'o''brien'") || got[1] != "GRANT SELECT ON OBJECT::[dbo].[t] TO [o'brien]" {
		t.Errorf("SQL Server grant: %v %v", got, err)
	}
}

// The privilege keyword that reaches a statement is the closed set's own.
// Nothing a request sends becomes SQL: an unknown privilege is refused before
// any statement is rendered.
func TestPrivilegeRequestsAreCheckedAgainstTheClosedSet(t *testing.T) {
	for _, c := range []struct {
		driver Driver
		change PrivilegeChange
	}{
		{DriverPostgres, PrivilegeChange{Role: "app", Level: GrantOnTable, Schema: "s", Table: "t", Privileges: []string{"SELECT ON ALL TABLES"}}},
		{DriverPostgres, PrivilegeChange{Role: "app", Level: GrantOnTable, Schema: "s", Table: "t", Privileges: []string{"ALL", "SELECT"}}},
		{DriverPostgres, PrivilegeChange{Role: "app", Level: GrantOnTable, Schema: "s", Table: "t", Privileges: []string{"SELECT"}, Future: true}},
		{DriverPostgres, PrivilegeChange{Role: "app", Level: GrantOnTable, Table: "t", Privileges: []string{"SELECT"}}},
		{DriverPostgres, PrivilegeChange{Role: "app", Level: GrantOnDatabase, Privileges: []string{"CONNECT"}}},
		{DriverPostgres, PrivilegeChange{Role: "app", Level: GrantOnSchema, Schema: "s", Privileges: []string{"SELECT"}}},
		{DriverPostgres, PrivilegeChange{Role: "app", Level: GrantOnSchema, Schema: "s"}},
		{DriverPostgres, PrivilegeChange{Role: "app", Level: GrantOnRole}},
		{DriverPostgres, PrivilegeChange{Level: GrantOnSchema, Schema: "s", Privileges: []string{"USAGE"}}},
		{DriverMySQL, PrivilegeChange{Role: "app", Level: GrantOnSchema, Schema: "s", Privileges: []string{"SELECT"}}},
		{DriverMySQL, PrivilegeChange{Role: "app", Level: GrantOnTable, Database: "d", Privileges: []string{"SELECT"}}},
		{DriverMySQL, PrivilegeChange{Role: "app", Level: GrantOnDatabase, Database: "d", Privileges: []string{"SELECT"}, Future: true}},
		{DriverClickHouse, PrivilegeChange{Role: "app", Level: GrantOnDatabase, Database: "d", Privileges: []string{"SELECT, INSERT"}}},
	} {
		got, err := PrivilegeStatements(c.driver, c.change)
		if err == nil {
			t.Errorf("%s %+v rendered %v", c.driver, c.change, got)
			continue
		}
		if _, ok := err.(ErrPrivilegeRequest); !ok {
			t.Errorf("%s %+v: %v is not a request refusal", c.driver, c.change, err)
		}
	}
	if _, err := PrivilegeStatements(DriverSQLite, PrivilegeChange{}); err != ErrUnsupported {
		t.Errorf("SQLite has no accounts: %v", err)
	}
	if levels := PrivilegeLevelsFor(DriverOracle); len(levels) != 0 {
		t.Errorf("Oracle offers %v", levels)
	}
}

// SHOW GRANTS is parsed, never forwarded. MariaDB prints the account's
// password hash on its first line; nothing after "TO" is read.
func TestMySQLGrantLinesAreParsedAndTheHashIsDropped(t *testing.T) {
	for _, c := range []struct {
		line string
		want Grant
		ok   bool
	}{
		{"GRANT USAGE ON *.* TO `app`@`%` IDENTIFIED BY PASSWORD '*74F59235247D4B7CCF858D3E14295956AEFD9AE2'",
			Grant{Level: "server", Privileges: []string{"USAGE"}}, true},
		{"GRANT ALL PRIVILEGES ON *.* TO `root`@`%` WITH GRANT OPTION",
			Grant{Level: "server", Privileges: []string{"ALL"}, Grantable: true}, true},
		{"GRANT SELECT, SHOW VIEW ON `shop\\_eu`.* TO `app`@`10.%`",
			Grant{Level: GrantOnDatabase, Database: "shop_eu", Privileges: []string{"SELECT", "SHOW VIEW"}}, true},
		{"GRANT SELECT (`id`, `email`), UPDATE ON `shop`.`us``ers` TO `app`@`%`",
			Grant{Level: GrantOnTable, Database: "shop", Table: "us`ers", Privileges: []string{"SELECT (`id`, `email`)", "UPDATE"}}, true},
		{"GRANT SELECT ON `a.b`.`c.d` TO `app`@`%`",
			Grant{Level: GrantOnTable, Database: "a.b", Table: "c.d", Privileges: []string{"SELECT"}}, true},
		{"GRANT `readers`@`%` TO `app`@`%`", Grant{}, false},
		{"GRANT PROXY ON ''@'%' TO `root`@`localhost` WITH GRANT OPTION", Grant{}, false},
		{"GRANT EXECUTE ON PROCEDURE `shop`.`close_day` TO `app`@`%`", Grant{}, false},
		{"SET DEFAULT ROLE `readers` FOR `app`@`%`", Grant{}, false},
	} {
		got, ok := parseMySQLGrant(c.line)
		if ok != c.ok {
			t.Errorf("%q: parsed %v, want %v", c.line, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.Level != c.want.Level || got.Database != c.want.Database || got.Table != c.want.Table ||
			got.Grantable != c.want.Grantable || strings.Join(got.Privileges, "|") != strings.Join(c.want.Privileges, "|") {
			t.Errorf("%q:\n got %+v\nwant %+v", c.line, got, c.want)
		}
		for _, p := range got.Privileges {
			if strings.Contains(p, "74F59235") || strings.Contains(p, "IDENTIFIED") {
				t.Errorf("the hash survived parsing: %q", p)
			}
		}
	}
	if user, host := splitMySQLGrantee(`'o''brien'@'10.0.%'`); user != "o'brien" || host != "10.0.%" {
		t.Errorf("grantee split into %q and %q", user, host)
	}
}

func TestMySQLGrantDatabaseEscapesWildcards(t *testing.T) {
	for in, want := range map[string]string{
		"shop":    "`shop`",
		"shop_eu": "`shop\\_eu`",
		"50%off":  "`50\\%off`",
		"a`b":     "`a``b`",
	} {
		got, err := mysqlGrantDatabase(in)
		if err != nil || got != want {
			t.Errorf("mysqlGrantDatabase(%q) = %s, %v; want %s", in, got, err, want)
		}
	}
}

func TestMaintenanceStatementsAreRenderedFromTheClosedSet(t *testing.T) {
	pg := postgresDialect{}
	for _, c := range []struct {
		req  MaintenanceRequest
		want string
	}{
		{MaintenanceRequest{Action: "vacuum"}, `VACUUM (VERBOSE)`},
		{MaintenanceRequest{Action: "vacuum_full", Schema: "s", Table: "t"}, `VACUUM (FULL, VERBOSE) "s"."t"`},
		{MaintenanceRequest{Action: "analyze", Table: `we"ird`}, `ANALYZE VERBOSE "public"."we""ird"`},
		{MaintenanceRequest{Action: "reindex"}, `REINDEX (VERBOSE) DATABASE "shop"`},
		{MaintenanceRequest{Action: "reindex", Schema: "s"}, `REINDEX (VERBOSE) SCHEMA "s"`},
		{MaintenanceRequest{Action: "reindex", Schema: "s", Index: "i", Options: MaintenanceOptions{Concurrently: true}}, `REINDEX (VERBOSE) INDEX CONCURRENTLY "s"."i"`},
	} {
		got, err := pgMaintenanceSQL(pg, c.req, "shop")
		if err != nil || got != c.want {
			t.Errorf("%+v: %q %v, want %q", c.req, got, err, c.want)
		}
	}
	for _, bad := range []MaintenanceRequest{
		{Action: "vacuum", Schema: "s"},
		{Action: "analyze", Index: "i"},
		{Action: "cluster", Table: "t"},
	} {
		if got, err := pgMaintenanceSQL(pg, bad, "shop"); err == nil {
			t.Errorf("%+v rendered %q", bad, got)
		}
	}

	if got, err := mysqlMaintenanceSQL("check", []string{"`a`", "`b`.`c`"}); err != nil || got != "CHECK TABLE `a`, `b`.`c`" {
		t.Errorf("mysql check: %q %v", got, err)
	}
	if got, err := mysqlMaintenanceSQL("drop", []string{"`a`"}); err == nil {
		t.Errorf("mysql rendered %q for an unknown action", got)
	}

	ch := clickhouseDialect{}
	if got, err := clickhouseMaintenanceSQL(ch, MaintenanceRequest{Action: "optimize", Schema: "logs", Table: "events", Options: MaintenanceOptions{Final: true}}); err != nil || got != "OPTIMIZE TABLE `logs`.`events` FINAL" {
		t.Errorf("clickhouse optimize: %q %v", got, err)
	}

	lite := sqliteDialect{}
	if got, err := sqliteMaintenanceSQL(lite, MaintenanceRequest{Action: "wal_checkpoint", Options: MaintenanceOptions{Mode: "Restart"}}); err != nil || got != "PRAGMA wal_checkpoint(RESTART)" {
		t.Errorf("sqlite checkpoint: %q %v", got, err)
	}
	if got, err := sqliteMaintenanceSQL(lite, MaintenanceRequest{Action: "wal_checkpoint", Options: MaintenanceOptions{Mode: "TRUNCATE); DROP TABLE t; --"}}); err == nil {
		t.Errorf("sqlite rendered %q for a mode outside the set", got)
	}
}

// Every engine's actions are a closed list with the two flags the route and
// the page read; an action nobody declared cannot be run.
func TestMaintenanceCatalogue(t *testing.T) {
	want := map[Driver][]string{
		DriverPostgres:   {"vacuum", "vacuum_analyze", "analyze", "vacuum_full", "reindex"},
		DriverMySQL:      {"analyze", "check", "optimize", "repair"},
		DriverSQLite:     {"analyze", "optimize", "integrity_check", "quick_check", "foreign_key_check", "wal_checkpoint", "reindex", "vacuum"},
		DriverClickHouse: {"optimize"},
		DriverMSSQL:      {},
		DriverOracle:     {},
		DriverMongo:      {},
	}
	for driver, ids := range want {
		got := []string{}
		for _, a := range MaintenanceActionsFor(driver) {
			got = append(got, a.ID)
			if a.Label == "" || a.Description == "" || (a.Scope != "table" && a.Scope != "database" && a.Scope != "either") {
				t.Errorf("%s %s is incompletely described: %+v", driver, a.ID, a)
			}
		}
		if strings.Join(got, ",") != strings.Join(ids, ",") {
			t.Errorf("%s offers %v, want %v", driver, got, ids)
		}
	}
	if a, _ := MaintenanceActionFor(DriverPostgres, "vacuum_full"); !a.Blocking || a.Destructive {
		t.Errorf("vacuum_full: %+v", a)
	}
	if a, _ := MaintenanceActionFor(DriverMySQL, "repair"); !a.Destructive {
		t.Errorf("repair can lose rows and is not marked destructive: %+v", a)
	}
	if _, err := RunMaintenance(t.Context(), nil, DriverMSSQL, "", MaintenanceRequest{Action: "vacuum"}); err == nil {
		t.Error("SQL Server ran a maintenance action")
	} else if _, ok := err.(ErrMaintenanceRequest); !ok {
		t.Errorf("%v is not a request refusal", err)
	}
}

func TestSettingValuesAreCheckedAgainstTheEnginesOwnBounds(t *testing.T) {
	memory := Setting{Name: "work_mem", Type: "integer", Unit: "kB", Min: "64", Max: "2147483647"}
	blocks := Setting{Name: "shared_buffers", Type: "integer", Unit: "8kB", Min: "16", Max: "1073741823"}
	timeout := Setting{Name: "deadlock_timeout", Type: "integer", Unit: "ms", Min: "1", Max: "2147483647"}
	real := Setting{Name: "random_page_cost", Type: "real", Min: "0", Max: "1.79769e+308"}
	flag := Setting{Name: "autovacuum", Type: "bool"}
	enum := Setting{Name: "wal_level", Type: "enum", Enum: []string{"minimal", "replica", "logical"}}
	plain := Setting{Name: "max_connections", Type: "integer", Min: "1", Max: "262143"}
	text := Setting{Name: "log_line_prefix", Type: "string"}

	for _, c := range []struct {
		s     Setting
		value string
		want  string
	}{
		{memory, "4096", "4096"}, {memory, "64MB", "64MB"}, {memory, " 1GB ", "1GB"},
		{blocks, "128MB", "128MB"}, {blocks, "16", "16"},
		{timeout, "1500ms", "1500ms"}, {timeout, "2s", "2s"}, {timeout, "1min", "1min"},
		{real, "1.1", "1.1"}, {flag, "TRUE", "on"}, {flag, "0", "off"},
		{enum, "LOGICAL", "logical"}, {plain, "200", "200"}, {text, "%m [%p] ", "%m [%p]"},
	} {
		got, err := checkSettingValue(c.s, c.value, true)
		if err != nil || got != c.want {
			t.Errorf("%s = %q: %q %v, want %q", c.s.Name, c.value, got, err, c.want)
		}
	}
	for _, c := range []struct {
		s     Setting
		value string
	}{
		{memory, "32"},     // below 64 kB
		{memory, "16kB"},   // the same, with a unit
		{memory, "5000TB"}, // above the maximum once converted
		{memory, "10s"},    // a time unit on a memory setting
		{blocks, "64kB"},   // eight blocks, below the minimum of sixteen
		{timeout, "500us"}, // half a millisecond
		{timeout, "1.5"},   // not a whole number
		{plain, "64MB"},    // a unit on a setting that has none
		{plain, "0"},       // below the minimum
		{plain, "1e3"},     // not a number as the engine writes one
		{flag, "maybe"},    // not a boolean
		{enum, "sideways"}, // not in the enum
		{text, "a\x00b"},   // a control character
		{text, "line\nfeed"},
	} {
		if got, err := checkSettingValue(c.s, c.value, true); err == nil {
			t.Errorf("%s = %q was accepted as %q", c.s.Name, c.value, got)
		}
	}
	// MySQL takes no units: its SET refuses "16M".
	if got, err := checkSettingValue(memory, "64MB", false); err == nil {
		t.Errorf("a unit was accepted where the engine takes none: %q", got)
	}
}

// A list-valued parameter given one quoted string stores one item. The items
// go in as separate literals, each quoted on its own.
func TestPostgresListSettingsAreQuotedItemByItem(t *testing.T) {
	list := Setting{Name: "shared_preload_libraries", Type: "string"}
	if got := pgSettingLiteral(list, `pg_stat_statements, "auto_explain"`); got != `'pg_stat_statements', 'auto_explain'` {
		t.Errorf("list literal: %s", got)
	}
	if got := pgSettingLiteral(list, ""); got != `''` {
		t.Errorf("empty list literal: %s", got)
	}
	if got := pgSettingLiteral(Setting{Name: "log_line_prefix"}, `it's, fine`); got != `'it''s, fine'` {
		t.Errorf("plain literal: %s", got)
	}
	if got, err := pgSettingName("pg_stat_statements.max"); err != nil || got != `"pg_stat_statements"."max"` {
		t.Errorf("extension parameter name: %s %v", got, err)
	}
}

func TestIndexesAreMarkedDuplicateCoveredAndUnused(t *testing.T) {
	list := []IndexStat{
		{Schema: "s", Table: "t", Name: "t_pkey", Method: "btree", Columns: []string{"id"}, Unique: true, Primary: true, Constraint: true, Valid: true, Scans: 9, plain: true},
		{Schema: "s", Table: "t", Name: "t_id_again", Method: "btree", Columns: []string{"id"}, Valid: true, Scans: 0, plain: true},
		{Schema: "s", Table: "t", Name: "t_a", Method: "btree", Columns: []string{"a"}, Valid: true, Scans: 0, plain: true},
		{Schema: "s", Table: "t", Name: "t_a_b", Method: "btree", Columns: []string{"A", "b"}, Valid: true, Scans: 4, plain: true},
		{Schema: "s", Table: "t", Name: "t_a_unique", Method: "btree", Columns: []string{"a", "b", "c"}, Unique: true, Valid: true, Scans: 0, plain: true},
		{Schema: "s", Table: "t", Name: "t_a_hash", Method: "hash", Columns: []string{"a"}, Valid: true, Scans: -1, plain: true},
		{Schema: "s", Table: "t", Name: "t_a_partial", Method: "btree", Columns: []string{"a"}, Valid: true, Scans: 0, signature: "partial"},
		{Schema: "s", Table: "other", Name: "other_a", Method: "btree", Columns: []string{"a"}, Valid: true, Scans: 0, plain: true},
	}
	markRedundantIndexes(list)
	by := map[string]IndexStat{}
	for _, ix := range list {
		by[ix.Name] = ix
	}
	if by["t_id_again"].DuplicateOf != "t_pkey" || by["t_pkey"].DuplicateOf != "" {
		t.Errorf("the copy of the primary key: %+v / %+v", by["t_id_again"], by["t_pkey"])
	}
	if by["t_a"].CoveredBy != "t_a_b" {
		t.Errorf("(a) is covered by %q", by["t_a"].CoveredBy)
	}
	// A unique index is never called covered: its uniqueness is a rule.
	if by["t_a_unique"].CoveredBy != "" || by["t_a_b"].CoveredBy != "t_a_unique" {
		t.Errorf("unique: %q, (a,b): %q", by["t_a_unique"].CoveredBy, by["t_a_b"].CoveredBy)
	}
	// A different method, a predicate and another table are all different indexes.
	if by["t_a_hash"].CoveredBy != "" || by["t_a_hash"].DuplicateOf != "" || by["t_a_partial"].CoveredBy != "" || by["other_a"].CoveredBy != "" {
		t.Errorf("hash %+v partial %+v other %+v", by["t_a_hash"], by["t_a_partial"], by["other_a"])
	}
	// Unused needs a count of zero: an engine that does not count says -1.
	if !by["t_a"].Unused || by["t_a_hash"].Unused || by["t_a_b"].Unused || by["t_a_unique"].Unused {
		t.Errorf("unused: a %v hash %v ab %v unique %v", by["t_a"].Unused, by["t_a_hash"].Unused, by["t_a_b"].Unused, by["t_a_unique"].Unused)
	}
}

func TestHeapBloatEstimate(t *testing.T) {
	// 1000 rows of 40 bytes: 24 + 4 + 40 = 68 bytes each, 121 to a page of
	// 8168 usable bytes, so nine pages hold them.
	if got := estimateHeapBloat(1000, 9, 40, 100, 8192); got != 0 {
		t.Errorf("a table exactly as large as its rows: %d", got)
	}
	if got := estimateHeapBloat(1000, 30, 40, 100, 8192); got != 21*8192 {
		t.Errorf("thirty pages for nine pages of rows: %d, want %d", got, 21*8192)
	}
	// A fill factor of 50 doubles what the rows are entitled to.
	if got := estimateHeapBloat(1000, 30, 40, 50, 8192); got != 13*8192 {
		t.Errorf("at fill factor 50: %d, want %d", got, 13*8192)
	}
	if got := estimateHeapBloat(0, 100, 0, 100, 8192); got != 100*8192 {
		t.Errorf("an emptied table is all bloat: %d", got)
	}
	if got := estimateHeapBloat(1000, 0, 40, 100, 8192); got != 0 {
		t.Errorf("no pages: %d", got)
	}
}

func TestVersionEndOfLife(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		driver  Driver
		version string
		release string
		past    bool
		known   bool
	}{
		{DriverPostgres, "PostgreSQL 13.14 on x86_64-pc-linux-gnu", "13", true, true},
		{DriverPostgres, "PostgreSQL 14.11 (Debian 14.11-1)", "14", false, true},
		{DriverPostgres, "PostgreSQL 16.15 on x86_64-pc-linux-musl", "16", false, true},
		{DriverPostgres, "PostgreSQL 9.6.24 on x86_64", "9.6", true, true},
		{DriverPostgres, "PostgreSQL 99.1", "", false, false},
		{DriverMySQL, "8.0.36-0ubuntu0.22.04.1", "8.0", true, true},
		{DriverMySQL, "8.4.11", "8.4", false, true},
		{DriverMySQL, "5.7.44-log", "5.7", true, true},
		{DriverMySQL, "11.8.9-MariaDB-ubu2404", "11.8", false, true},
		{DriverMySQL, "10.5.23-MariaDB", "10.5", true, true},
		{DriverMySQL, "10.11.6-MariaDB-0+deb12u1", "10.11", false, true},
		{DriverMSSQL, "Microsoft SQL Server 2016 (SP3) - 13.0.6300.2 (X64)", "2016", true, true},
		{DriverMSSQL, "Microsoft SQL Server 2022 (RTM-CU12) - 16.0.4115.5", "2022", false, true},
		{DriverOracle, "Oracle Database 19c Enterprise Edition Release 19.0.0.0.0 - Production", "19", false, true},
		{DriverOracle, "Oracle Database 11g Enterprise Edition Release 11.2.0.4.0", "11", true, true},
		{DriverClickHouse, "ClickHouse 24.8.14.39", "24.8", true, true},
		{DriverClickHouse, "ClickHouse 26.8.1.1", "26.8", false, true},
		{DriverSQLite, "SQLite 3.46.0", "", false, false},
		{DriverMySQL, "something else", "", false, false},
	} {
		got, ok := VersionEndOfLife(c.driver, c.version, now)
		if ok != c.known {
			t.Errorf("%s %q: known %v, want %v", c.driver, c.version, ok, c.known)
			continue
		}
		if !ok {
			continue
		}
		if got.Release != c.release || got.Past != c.past {
			t.Errorf("%s %q: release %q past %v (date %s), want %q %v", c.driver, c.version, got.Release, got.Past, got.Date.Format("2006-01-02"), c.release, c.past)
		}
		if got.Past != (got.DaysLeft < 0) && got.DaysLeft != 0 {
			t.Errorf("%s %q: past %v with %d days left", c.driver, c.version, got.Past, got.DaysLeft)
		}
	}
	// Past its date is a warning; within half a year of it, a notice.
	eol, _ := VersionEndOfLife(DriverPostgres, "PostgreSQL 14.11", now)
	if finding, ok := adviseEndOfLife(eol); !ok || finding.ID != "version-end-of-life-soon" || finding.Level != "notice" {
		t.Errorf("42 days before the date: %+v %v", finding, ok)
	}
	eol, _ = VersionEndOfLife(DriverPostgres, "PostgreSQL 13.14", now)
	if finding, ok := adviseEndOfLife(eol); !ok || finding.ID != "version-end-of-life" || finding.Level != "warning" || finding.Category != AdviceSecurity {
		t.Errorf("a year after the date: %+v %v", finding, ok)
	}
	eol, _ = VersionEndOfLife(DriverPostgres, "PostgreSQL 16.15", now)
	if finding, ok := adviseEndOfLife(eol); ok {
		t.Errorf("two years before the date produced %+v", finding)
	}
}

func TestSessionRowsAreFinishedTheSameWay(t *testing.T) {
	// Oracle's handle has a comma in it, so the joined string cannot be split.
	oracle := Activity{PID: "12,345", BlockedBy: "67,890", BlockedByPIDs: []string{"67,890"}, Status: SessionActive}
	finishActivity(&oracle)
	if len(oracle.BlockedByPIDs) != 1 || oracle.BlockedByPIDs[0] != "67,890" || oracle.Status != SessionBlocked {
		t.Errorf("oracle: %+v", oracle)
	}
	// A dialect that only joins its blockers gets the list derived.
	legacy := Activity{PID: "9", BlockedBy: "4, 7"}
	finishActivity(&legacy)
	if strings.Join(legacy.BlockedByPIDs, "|") != "4|7" || legacy.Status != SessionBlocked {
		t.Errorf("legacy: %+v", legacy)
	}
	// An idle session holding nothing is not turned into an active one.
	idle := Activity{PID: "3", Status: SessionIdle}
	finishActivity(&idle)
	if idle.Status != SessionIdle || idle.BlockedByPIDs != nil {
		t.Errorf("idle: %+v", idle)
	}

	for _, c := range []struct {
		command, user string
		inTx          bool
		want          string
	}{
		{"Sleep", "app", false, SessionIdle},
		{"Sleep", "app", true, SessionIdleInTransaction},
		{"Query", "app", true, SessionActive},
		{"Daemon", "event_scheduler", false, SessionBackground},
		{"Binlog Dump GTID", "repl", false, SessionBackground},
		{"Query", "system user", false, SessionBackground},
	} {
		if got := mysqlSessionStatus(c.command, c.user, c.inTx); got != c.want {
			t.Errorf("%s/%s/%v = %s, want %s", c.command, c.user, c.inTx, got, c.want)
		}
	}
}

func TestStatementOptionsAreAClosedSet(t *testing.T) {
	for _, c := range []struct {
		in    StatementsOptions
		sort  string
		limit int
	}{
		{StatementsOptions{}, StatementsByTotal, 25},
		{StatementsOptions{Sort: "calls", Limit: 10}, StatementsByCalls, 10},
		{StatementsOptions{Sort: "mean", Limit: 100000}, StatementsByMean, 200},
		{StatementsOptions{Sort: "max", Limit: -3}, StatementsByMax, 25},
	} {
		got, err := c.in.normalised()
		if err != nil || got.Sort != c.sort || got.Limit != c.limit {
			t.Errorf("%+v → %+v %v", c.in, got, err)
		}
	}
	for _, bad := range []string{"total DESC", "calls; DROP", "duration"} {
		if _, err := (StatementsOptions{Sort: bad}).normalised(); err == nil {
			t.Errorf("sort %q was accepted", bad)
		}
	}
	// An engine with no statistics still answers, with the reason.
	report, err := TopStatements(t.Context(), nil, DriverSQLite, StatementsOptions{})
	if err != nil || report.Supported || report.Reason == "" || report.Resettable || report.Statements == nil {
		t.Errorf("sqlite statements: %+v %v", report, err)
	}
	if _, err := ResetStatements(t.Context(), nil, DriverSQLite); err != ErrUnsupported {
		t.Errorf("sqlite reset: %v", err)
	}
}

func TestOracleSessionHandles(t *testing.T) {
	// The blocker is the same "sid,serial#" pair a session's handle is.
	if q := oracleSessionsSQL(true); !strings.Contains(q, "TO_CHAR(b.sid) || ',' || TO_CHAR(b.serial#)") ||
		!strings.Contains(q, "q.child_number = s.sql_child_number") || !strings.Contains(q, "v$transaction") {
		t.Errorf("the session query lost a fix:\n%s", q)
	}
	if q := oracleSessionsSQL(false); strings.Contains(q, "v$transaction") {
		t.Errorf("the fallback still needs v$transaction")
	}
	d := oracleDialect{}
	for _, bad := range []string{"12", "12,34,56", "12,34' IMMEDIATE --", "a b,3"} {
		if err := d.Cancel(t.Context(), nil, bad); err == nil {
			t.Errorf("cancel accepted the handle %q", bad)
		}
	}
	if stmt, ok := oracleCompileSQL(d, "APP", "PACKAGE BODY", "BILLING"); !ok || stmt != `ALTER PACKAGE "APP"."BILLING" COMPILE BODY;` {
		t.Errorf("compile: %q %v", stmt, ok)
	}
	if _, ok := oracleCompileSQL(d, "APP", "SYNONYM", "S"); ok {
		t.Error("a synonym was given a COMPILE statement")
	}
}

// What each engine offers is read off the interfaces it implements, so the
// table here is the contract the pages are built against: a surface that
// appears or disappears for an engine has to change this test.
func TestOpsCapabilitiesPerEngine(t *testing.T) {
	on := func(names ...string) map[string]bool {
		out := map[string]bool{}
		for _, n := range names {
			out[n] = true
		}
		return out
	}
	want := map[Driver]map[string]bool{
		DriverPostgres: on("stats", "sessions", "kill", "cancel", "locks", "replication", "tableStats", "indexStats",
			"maintenance", "settings", "settingsWrite", "roles", "privileges", "statements", "statementsReset",
			"advisor", "engineAdvisor"),
		DriverMySQL: on("stats", "sessions", "kill", "cancel", "locks", "replication", "tableStats", "indexStats",
			"maintenance", "settings", "settingsWrite", "roles", "privileges", "statements", "statementsReset",
			"advisor", "engineAdvisor"),
		DriverSQLite: on("stats", "tableStats", "indexStats", "maintenance", "settings", "settingsWrite",
			"advisor", "engineAdvisor", "sqliteFile"),
		DriverClickHouse: on("stats", "sessions", "kill", "tableStats", "indexStats", "maintenance", "settings",
			"roles", "privileges", "statements", "advisor", "engineAdvisor", "clickhouseViews"),
		DriverMSSQL: on("stats", "sessions", "kill", "locks", "tableStats", "indexStats", "settings", "roles",
			"privileges", "advisor", "engineAdvisor"),
		DriverOracle: on("stats", "sessions", "kill", "cancel", "settings", "advisor", "engineAdvisor"),
	}
	for driver, expected := range want {
		got := OpsCapabilities(driver, "")
		for name, has := range got {
			if has != expected[name] {
				t.Errorf("%s %s = %v, want %v", driver, name, has, expected[name])
			}
		}
		for name := range expected {
			if _, ok := got[name]; !ok {
				t.Errorf("%s has no %s flag", driver, name)
			}
		}
	}
	if got := OpsCapabilities(DriverRedis, ""); len(got) != 0 {
		t.Errorf("an engine with no dialect reports %v", got)
	}
	// A fork only ever loses surfaces.
	base, fork := OpsCapabilities(DriverPostgres, "postgres"), OpsCapabilities(DriverPostgres, "cockroachdb")
	for name, has := range fork {
		if has && !base[name] {
			t.Errorf("cockroachdb gained %s", name)
		}
	}
	if fork["maintenance"] || fork["locks"] || !fork["sessions"] || !fork["roles"] {
		t.Errorf("cockroachdb: %v", fork)
	}
}
