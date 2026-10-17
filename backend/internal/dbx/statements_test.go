package dbx

import (
	"strings"
	"testing"
)

// The server prints shared_preload_libraries with commas and spaces, and
// quotes a name that needs it. Each is one library, and an empty value is
// none.
func TestPgLibrariesReadsTheServersList(t *testing.T) {
	for value, want := range map[string]string{
		"":                               "",
		"pg_stat_statements":             "pg_stat_statements",
		"timescaledb,pg_stat_statements": "timescaledb|pg_stat_statements",
		` timescaledb , "pg_cron" ,, auto_explain `: "timescaledb|pg_cron|auto_explain",
		`"$libdir/plugins/pgaudit"`:                 "$libdir/plugins/pgaudit",
	} {
		if got := strings.Join(pgLibraries(value), "|"); got != want {
			t.Errorf("pgLibraries(%q) = %q, want %q", value, got, want)
		}
	}
	if got := pgLibraries(""); got == nil {
		t.Error("an empty list reads as nil, which marshals as null")
	}
}

// The statement keeps every library the next start would load, in its order,
// with the statistics module after them — each its own literal, which is how
// the server stores a list (one quoted "a, b" is a single library of that
// name, and a server told to load it does not start).
func TestPgPreloadStatementKeepsEveryLibrary(t *testing.T) {
	for _, c := range []struct {
		next []string
		want string
	}{
		{nil, `ALTER SYSTEM SET "shared_preload_libraries" = 'pg_stat_statements'`},
		{[]string{"timescaledb"}, `ALTER SYSTEM SET "shared_preload_libraries" = 'timescaledb', 'pg_stat_statements'`},
		{[]string{"pg_cron", "auto_explain"}, `ALTER SYSTEM SET "shared_preload_libraries" = 'pg_cron', 'auto_explain', 'pg_stat_statements'`},
		{[]string{"it's"}, `ALTER SYSTEM SET "shared_preload_libraries" = 'it''s', 'pg_stat_statements'`},
	} {
		next := append([]string{}, c.next...)
		if got := pgPreloadStatement(withStatementsLibrary(next)); got != c.want {
			t.Errorf("for %q:\n got %s\nwant %s", c.next, got, c.want)
		}
		if strings.Join(next, ",") != strings.Join(c.next, ",") {
			t.Errorf("the list it was given changed: %q", next)
		}
	}
}

// The list cannot be changed from here when the command line sets it, since
// that outranks ALTER SYSTEM and the change would be stored and never
// applied, or when the account is not a superuser. The reason is a sentence
// the page prints as it is.
func TestPgPreloadSaysWhyItCannotBeChanged(t *testing.T) {
	for _, c := range []struct {
		preload pgPreload
		want    string
	}{
		{pgPreload{source: "configuration file", super: true}, ""},
		{pgPreload{source: "default", super: true}, ""},
		{pgPreload{source: "command line", super: true}, "command line"},
		{pgPreload{source: "configuration file", super: false}, "superuser"},
	} {
		got := c.preload.blocked()
		if (got == "") != (c.want == "") || !strings.Contains(got, c.want) || (got != "" && !strings.HasSuffix(got, ".")) {
			t.Errorf("%+v: %q, want one about %q", c.preload, got, c.want)
		}
	}
}

// The setup is PostgreSQL's, where the statistics come from a library loaded
// at start. A fork that has the module loaded already and refuses ALTER
// SYSTEM, or has no module, is not offered it, and no other engine has one.
func TestStatementsSetupIsForThePostgresThatLoadsTheLibrary(t *testing.T) {
	for _, c := range []struct {
		driver Driver
		flavor string
		want   bool
	}{
		{DriverPostgres, FlavorPostgres, true},
		{DriverPostgres, FlavorTimescaleDB, true},
		{DriverPostgres, FlavorCockroachDB, false},
		{DriverPostgres, FlavorYugabyteDB, false},
		{DriverMySQL, FlavorMySQL, false},
		{DriverMySQL, FlavorMariaDB, false},
		{DriverMSSQL, FlavorSQLServer, false},
		{DriverClickHouse, FlavorClickHouse, false},
	} {
		if got := Capable(c.driver, c.flavor, "statementsSetup"); got != c.want {
			t.Errorf("%s/%s: statementsSetup %v, want %v", c.driver, c.flavor, got, c.want)
		}
	}
	if _, err := PreloadStatements(t.Context(), nil, DriverMySQL); err != ErrUnsupported {
		t.Errorf("MySQL: %v", err)
	}
}
