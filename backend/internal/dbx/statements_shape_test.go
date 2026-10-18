package dbx

import (
	"strings"
	"testing"
)

// pgShapeCase is one statement as pg_stat_statements can hold it, and the
// shape the Statements list may show of it.
type pgShapeCase struct{ in, want string }

// The statements the extension plans arrive with $n where each constant was.
// Those markers are what the Statements panel asks a value for, so they stay;
// whatever literal is left beside them goes, and nothing else changes.
var pgPlannedShapes = []pgShapeCase{
	{`SELECT a FROM t WHERE a = $1 AND b IN ($2, $3, $4) /* it's a comment */`,
		`SELECT a FROM t WHERE a = $1 AND b IN ($2, $3, $4) /* it's a comment */`},
	// A word after SET in a statement that was planned is a column.
	{`UPDATE t SET b = b + $1 WHERE a = $2`, `UPDATE t SET b = b + $1 WHERE a = $2`},
	{`INSERT INTO users (email, password) VALUES ($1, $2) ON CONFLICT (email) DO UPDATE SET password = excluded.password`,
		`INSERT INTO users (email, password) VALUES ($1, $2) ON CONFLICT (email) DO UPDATE SET password = excluded.password`},
	{`SELECT id, password FROM users WHERE email = $1`, `SELECT id, password FROM users WHERE email = $1`},
	{`/* app */ UPDATE t SET a = b WHERE id = $12`, `/* app */ UPDATE t SET a = b WHERE id = $12`},
	// jsonb's ? operator, a squashed list (18), names with digits and $.
	{`SELECT $1::jsonb ? $2`, `SELECT $1::jsonb ? $2`},
	{`SELECT * FROM t WHERE id IN ($1 /*, ... */)`, `SELECT * FROM t WHERE id IN ($1 /*, ... */)`},
	{`SELECT t1.c$2, "x$1", "it's 2" FROM t1 WHERE a = $1`, `SELECT t1.c$2, "x$1", "it's 2" FROM t1 WHERE a = $1`},
	// The rule is every literal, not every value the extension saw: a type's
	// modifiers and an ORDER BY position are ? as well.
	{`SELECT $1::numeric(10,2) FROM t ORDER BY 1`, `SELECT $1::numeric(?,?) FROM t ORDER BY ?`},
	{`(SELECT 1) UNION (SELECT 'x')`, `(SELECT ?) UNION (SELECT ?)`},
	{``, ``},
}

// Every kind of literal PostgreSQL's scanner reads, whole.
var pgLiteralShapes = []pgShapeCase{
	{`SELECT 0x1F, 0o17, 0b101, .5, 1e3, 1.5e-3, 1_000_000, 42.`, `SELECT ?, ?, ?, ?, ?, ?, ?, ?`},
	{`SELECT B'1010', X'1F', b'0', x'ff', N'sekrit', n'sekrit'`, `SELECT ?, ?, ?, ?, ?, ?`},
	{`SELECT E'it\'s', e'\\', 'it''s'`, `SELECT ?, ?, ?`},
	// A Unicode name stays a name; a Unicode string and its escape go.
	{`SELECT U&"d\0061t" FROM t WHERE x = U&'d!0061t' UESCAPE '!'`, `SELECT U&"d\0061t" FROM t WHERE x = ? UESCAPE ?`},
	// The letters that open a string open one only where a word starts.
	{`SELECT e, n, b, x, u FROM t WHERE the = 'sekrit'`, `SELECT e, n, b, x, u FROM t WHERE the = ?`},
	{`DO $$ BEGIN PERFORM 'sekrit-eleven'; END $$`, `DO ?`},
	{`DO $body$ BEGIN RAISE NOTICE '$$ sekrit'; END $body$`, `DO ?`},
	{`DO LANGUAGE plpgsql $a$ BEGIN PERFORM $b$ sekrit $b$; END $a$`, `DO LANGUAGE plpgsql ?`},
	{`COMMENT ON TABLE t IS 'пароль'`, `COMMENT ON TABLE t IS ?`},
	{`SELECT "таблица" FROM t WHERE c = 'значение'`, `SELECT "таблица" FROM t WHERE c = ?`},
	// Comments are kept, and a quote inside one opens nothing. Block comments
	// nest: after the inner */ the outer comment is still open, and a reader
	// that closed it there would take it's for a string and show what follows.
	{"SELECT 1 -- don't\n, 'sekrit'", "SELECT ? -- don't\n, ?"},
	{`SELECT 1 /* a /* b */ it's */ , 'sekrit'`, `SELECT ? /* a /* b */ it's */ , ?`},
	{`/* outer /* inner */ it's still one */ SET jdt.k = 'sekrit'`, `/* outer /* inner */ it's still one */ SET jdt.k = ?`},
	// Text the server cut short: what was open at the end is a literal to it.
	{`ALTER ROLE jdt PASSWORD 'sekrit-cut`, `ALTER ROLE jdt PASSWORD ?`},
	{`DO $$ BEGIN PERFORM 'sekrit`, `DO ?`},
	{`COMMENT ON TABLE t IS E'sekrit\`, `COMMENT ON TABLE t IS ?`},
}

// What PostgreSQL 17's pg_stat_statements was seen to keep as it came, with
// track_utility on: the utility statements, each with its value.
var pgUtilityShapes = []pgShapeCase{
	{`CREATE ROLE jdt PASSWORD 'sekrit-one'`, `CREATE ROLE jdt PASSWORD ?`},
	{`ALTER ROLE jdt WITH ENCRYPTED PASSWORD E'sek\'rit-two'`, `ALTER ROLE jdt WITH ENCRYPTED PASSWORD ?`},
	{`ALTER ROLE jdt PASSWORD $pw$sekrit-three$pw$`, `ALTER ROLE jdt PASSWORD ?`},
	{`ALTER ROLE jdt PASSWORD U&'sek\0072it-four'`, `ALTER ROLE jdt PASSWORD ?`},
	{"ALTER ROLE jdt PASSWORD 'sekrit'\n  '-five'", "ALTER ROLE jdt PASSWORD ?\n  ?"},
	{`alter user jdt password /* new */ 'sekrit'`, `alter user jdt password /* new */ ?`},
	{`CREATE USER jdt WITH LOGIN PASSWORD 'SCRAM-SHA-256$4096:c2Vrcml0$sekrit:sekrit' VALID UNTIL 'infinity'`,
		`CREATE USER jdt WITH LOGIN PASSWORD ? VALID UNTIL ?`},
	// Whatever follows the word is the password's, quoted or not.
	{`ALTER ROLE jdt PASSWORD sekrit_bare`, `ALTER ROLE jdt PASSWORD ?`},
	{`ALTER ROLE jdt PASSWORD "sekrit"`, `ALTER ROLE jdt PASSWORD ?`},
	{`ALTER ROLE jdt PASSWORD 4242`, `ALTER ROLE jdt PASSWORD ?`},
	{`ALTER ROLE jdt PASSWORD NULL`, `ALTER ROLE jdt PASSWORD ?`},
	{`CREATE USER MAPPING FOR app SERVER remote OPTIONS (user 'app', password 'sekrit')`,
		`CREATE USER MAPPING FOR app SERVER remote OPTIONS (user ?, password ?)`},
	{`ALTER USER MAPPING FOR app SERVER remote OPTIONS (DROP password)`,
		`ALTER USER MAPPING FOR app SERVER remote OPTIONS (DROP password)`},
	// A role named password is a quoted name, not the keyword.
	{`CREATE ROLE "password" LOGIN`, `CREATE ROLE "password" LOGIN`},
	// A setting's value, however it is written.
	{`SET jdt.other = sekrit_seven`, `SET jdt.other = ?`},
	{`SET jdt.num TO 4242`, `SET jdt.num TO ?`},
	{`SET LOCAL search_path TO "$user", public`, `SET LOCAL search_path TO ?, ?`},
	{`set session "jdt"."token" = 'sekrit'`, `set session "jdt"."token" = ?`},
	{`ALTER ROLE jdt SET work_mem = '64MB'`, `ALTER ROLE jdt SET work_mem = ?`},
	{`ALTER ROLE jdt IN DATABASE app SET jdt.token = sekrit_six`, `ALTER ROLE jdt IN DATABASE app SET jdt.token = ?`},
	{`ALTER SYSTEM SET primary_conninfo = 'host=db password=sekrit'`, `ALTER SYSTEM SET primary_conninfo = ?`},
	{`ALTER DATABASE app SET jdt.flag TO DEFAULT`, `ALTER DATABASE app SET jdt.flag TO ?`},
	{`CREATE FUNCTION f() RETURNS int LANGUAGE sql SET search_path = app, pg_temp AS $$ SELECT 42 $$`,
		`CREATE FUNCTION f() RETURNS int LANGUAGE sql SET search_path = ?, ? AS ?`},
	// The other SETs name a role, a schema, a mode: nothing is a value there
	// but what is written as one.
	{`SET TIME ZONE 'Europe/Chisinau'`, `SET TIME ZONE ?`},
	{`SET ROLE app`, `SET ROLE app`},
	{`SET SESSION AUTHORIZATION app`, `SET SESSION AUTHORIZATION app`},
	{`SET jdt.k FROM CURRENT`, `SET jdt.k FROM CURRENT`},
	{`ALTER TABLE t ALTER COLUMN c SET DEFAULT 'sekrit'`, `ALTER TABLE t ALTER COLUMN c SET DEFAULT ?`},
	{`ALTER TABLE t SET (fillfactor = 70)`, `ALTER TABLE t SET (fillfactor = ?)`},
	{`ALTER TABLE t SET SCHEMA archive`, `ALTER TABLE t SET SCHEMA archive`},
	// A statement wrapped in a utility one is read as one: what follows its
	// SET's = is taken for a value, which shows less and never more.
	{`EXPLAIN UPDATE t SET a = b`, `EXPLAIN UPDATE t SET a = ?`},
	{`COMMENT ON TABLE t IS 'sekrit-ten'`, `COMMENT ON TABLE t IS ?`},
	{`CREATE TABLE t (a text DEFAULT 'sekrit-nine', b int DEFAULT 77, c bit(4) DEFAULT B'1010', d bytea DEFAULT '\xdead', e numeric DEFAULT 1_000.5e3)`,
		`CREATE TABLE t (a text DEFAULT ?, b int DEFAULT ?, c bit(?) DEFAULT ?, d bytea DEFAULT ?, e numeric DEFAULT ?)`},
	{`NOTIFY jdt_chan, 'sekrit-fifteen'`, `NOTIFY jdt_chan, ?`},
}

func TestPostgresStatementShapeDropsEveryLiteralAndKeepsTheMarkers(t *testing.T) {
	for _, set := range [][]pgShapeCase{pgPlannedShapes, pgLiteralShapes, pgUtilityShapes} {
		for _, c := range set {
			got := pgStatementShape(c.in)
			if got != c.want {
				t.Errorf("pgStatementShape(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
			if strings.Contains(got, "sekrit") {
				t.Errorf("a value survived in %q", got)
			}
		}
	}
}

// standard_conforming_strings decides whether a backslash escapes in '…', and
// the text does not say which was on. Read either way alone, one of these
// shows a value; read both ways, neither does.
func TestPostgresStatementShapeReadsAStringBothWaysABackslashCanBeRead(t *testing.T) {
	for _, c := range []pgShapeCase{
		// Off: one string. Read with the setting on, "s sekrit-eight" is code.
		{`COMMENT ON ROLE jdt IS 'it\'s sekrit-eight'`, `COMMENT ON ROLE jdt IS ?`},
		// On: two strings. Read with it off, the first runs on to the second
		// quote and sekrit is code after it.
		{`CREATE TABLE t (p text DEFAULT 'C:\', q text DEFAULT 'sekrit')`, `CREATE TABLE t (p text DEFAULT ?`},
		{`COMMENT ON TABLE t IS 'C:\'`, `COMMENT ON TABLE t IS ?`},
		// An E'' string escapes either way, and a name never does.
		{`SELECT E'a\'b', "c\" FROM t`, `SELECT ?, "c\" FROM t`},
	} {
		if got := pgStatementShape(c.in); got != c.want {
			t.Errorf("pgStatementShape(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
		if !strings.Contains(c.in, "sekrit") {
			continue
		}
		if !strings.Contains(pgShapeOneWay(c.in, false), "sekrit") && !strings.Contains(pgShapeOneWay(c.in, true), "sekrit") {
			t.Errorf("%q is masked by either reading alone, so it says nothing about reading both", c.in)
		}
	}
}

// pgShapeOneWay is pgStatementShape with the text read one way only.
func pgShapeOneWay(text string, backslash bool) string {
	masked := make([]bool, len(text))
	for _, v := range pgValues(text, pgTokens(text, backslash)) {
		for i := v.start; i < v.end; i++ {
			masked[i] = true
		}
	}
	var b strings.Builder
	for i := range len(text) {
		switch {
		case !masked[i]:
			b.WriteByte(text[i])
		case i == 0 || !masked[i-1]:
			b.WriteByte('?')
		}
	}
	return b.String()
}

// A hand-written scanner over text anybody can send: it must not panic, must
// never lengthen the text, and must leave a shape as it found it — a ? it
// wrote is not a literal the next reading takes for a value.
func FuzzPostgresStatementShape(f *testing.F) {
	for _, set := range [][]pgShapeCase{pgPlannedShapes, pgLiteralShapes, pgUtilityShapes} {
		for _, c := range set {
			f.Add(c.in)
		}
	}
	f.Add(`$`)
	f.Add(`$a`)
	f.Add(`'\`)
	f.Add(`/*/`)
	f.Add(`U&`)
	f.Add(`0x`)
	f.Fuzz(func(t *testing.T, text string) {
		shape := pgStatementShape(text)
		if len(shape) > len(text) {
			t.Fatalf("%q became the longer %q", text, shape)
		}
		if again := pgStatementShape(shape); again != shape {
			t.Fatalf("%q shaped to %q, and that to %q", text, shape, again)
		}
	})
}
