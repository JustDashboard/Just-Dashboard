package dbx

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	mssql "github.com/microsoft/go-mssqldb"
)

func TestStatementShapeKeepsNoValue(t *testing.T) {
	for _, c := range []struct {
		driver     Driver
		text, want string
	}{
		{DriverPostgres, `SELECT * FROM users WHERE email = 'ann@example.com' AND id > 42`,
			`SELECT * FROM users WHERE email = ? AND id > ?`},
		// A quoted name is structure; the comment around it is not kept at all.
		{DriverPostgres, "SELECT \"Name\" -- the owner's\nFROM \"Orders\" /* 'x' */ LIMIT 10",
			`SELECT "Name" FROM "Orders" LIMIT ?`},
		{DriverPostgres, `ALTER ROLE app PASSWORD 'hunter2'`, `ALTER ROLE app PASSWORD ?`},
		{DriverPostgres, `DO $body$ BEGIN PERFORM set_config('app.token', 'abc', false); END $body$`, `DO ?`},
		{DriverPostgres, `SELECT E'it''s', U&'d\0061t\+000061', B'1010', 1.5e3`, `SELECT E?, U&?, B?, ?.?`},
		{DriverPostgres, `SELECT * FROM t WHERE id = $1`, `SELECT * FROM t WHERE id = $?`},
		// MySQL reads "…" as a string unless ANSI_QUOTES is on, so it is masked;
		// a backtick is always a name.
		{DriverMySQL, "SELECT `email` FROM users WHERE email = \"ann@example.com\" OR note = x'0a'",
			"SELECT `email` FROM users WHERE email = ? OR note = x?"},
		{DriverMySQL, "INSERT INTO t VALUES (0xFF, 'a', -3) # a trailing note\n",
			`INSERT INTO t VALUES (?, ?, -?)`},
		{DriverMSSQL, `SELECT [Full Name] FROM dbo.People WHERE Name = N'Ana' AND Age = 30`,
			`SELECT [Full Name] FROM dbo.People WHERE Name = N? AND Age = ?`},
		{DriverClickHouse, `SELECT "user_id", count() FROM events WHERE kind = 'login' GROUP BY 1`,
			`SELECT "user_id", count() FROM events WHERE kind = ? GROUP BY ?`},
		{DriverOracle, `SELECT * FROM v$session WHERE username = 'SCOTT'`, `SELECT * FROM v$session WHERE username = ?`},
		{DriverSQLite, `UPDATE notes SET body = 'call 555-0100' WHERE id = 7`, `UPDATE notes SET body = ? WHERE id = ?`},
		// SQLite takes "…" for a string where no column has the name.
		{DriverSQLite, `SELECT * FROM notes WHERE body = "call 555-0100"`, `SELECT * FROM notes WHERE body = ?`},
	} {
		if got := StatementShape(c.driver, c.text); got != c.want {
			t.Errorf("%s %q\n got %q\nwant %q", c.driver, c.text, got, c.want)
		}
	}
}

// What the lexer will not read has no shape: the runner refused it, and any
// guess at where its strings end could keep one.
func TestStatementShapeOfUnreadableTextIsEmpty(t *testing.T) {
	for _, c := range []struct {
		driver Driver
		text   string
	}{
		{DriverPostgres, `SELECT 'never closed`},
		{DriverMySQL, `SELECT 'it\'s'`},
		{DriverOracle, `SELECT q'[x]' FROM dual`},
		{DriverClickHouse, `SELECT $$x$$`},
		{DriverPostgres, "SELECT 1 /* nested /* */ */"},
	} {
		if got := StatementShape(c.driver, c.text); got != "" {
			t.Errorf("%s %q has the shape %q", c.driver, c.text, got)
		}
	}
}

func TestStatementShapeIsBounded(t *testing.T) {
	long := "SELECT " + strings.Repeat("column_name, ", 200) + "x FROM t"
	if got := StatementShape(DriverPostgres, long); len(got) > 1000 || !strings.HasPrefix(got, "SELECT column_name") {
		t.Fatalf("shape of a long statement is %d bytes: %.40q", len(got), got)
	}
}

// What an engine said comes back as its code alone: every message here quotes
// a value the statement carried.
func TestErrorCodeKeepsNoValue(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{&pgconn.PgError{Code: "22P02", Message: `invalid input syntax for type integer: "hunter2"`}, "SQLSTATE 22P02"},
		{&mysql.MySQLError{Number: 1064, Message: `You have an error in your SQL syntax; check the manual near 'ann@example.com' at line 1`}, "MySQL 1064"},
		{mssql.Error{Number: 2627, Message: "Violation of UNIQUE KEY constraint. The duplicate key value is (ann@example.com)."}, "SQL Server 2627"},
		{fmt.Errorf("run: %w", &mysql.MySQLError{Number: 1062, Message: `Duplicate entry 'ann@example.com' for key 'email'`}), "MySQL 1062"},
		{errors.New(`ORA-00001: unique constraint (APP.EMAIL_UK) violated, value 'ann@example.com'`), "ORA-00001"},
		{errors.New("the table is gone"), ""},
	} {
		if got := ErrorCode(c.err); got != c.want {
			t.Errorf("%v: got %q, want %q", c.err, got, c.want)
		}
	}
}
