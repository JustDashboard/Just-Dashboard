package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

// Live tests for statements that hold a body: a stored program written in the
// console the way each engine's own client takes one — DELIMITER, GO, a /
// line, or no separator at all — then created, called and read back by the
// engine. Only the engine can say that this package cut the script where it
// reads one, and every object here is named jdt_script_splitter_….

// runScript runs a script the way POST …/script does and fails on any step
// that did not run.
func runScript(t *testing.T, db *sql.DB, driver Driver, script string, opts ScriptOptions) *ScriptResult {
	t.Helper()
	statements, err := ParseScript(driver, script)
	if err != nil {
		t.Fatalf("the script was not read: %v\n%s", err, script)
	}
	res, err := RunScript(context.Background(), db, driver, statements, opts)
	if err != nil {
		t.Fatalf("the script was not run: %v", err)
	}
	if res.Failed >= 0 {
		step := res.Steps[res.Failed]
		t.Fatalf("statement %d (line %d) failed: %s\n%s", res.Failed+1, step.Line, step.Error, step.SQL)
	}
	return res
}

// lastRow is the first row of the script's last result, as text.
func lastRow(t *testing.T, res *ScriptResult) []string {
	t.Helper()
	last := res.Steps[len(res.Steps)-1].Result
	if last == nil || len(last.Rows) == 0 {
		t.Fatalf("the last statement returned no row: %+v", last)
	}
	var row []string
	for _, v := range last.Rows[0] {
		row = append(row, fmt.Sprint(v))
	}
	return row
}

// storedProgramRe is a statement that makes a stored program.
var storedProgramRe = regexp.MustCompile(`(?i)^CREATE\b[^(]*?\b(PROCEDURE|FUNCTION|TRIGGER|PACKAGE)\b`)

// definitionsAreNotReads holds every statement of a script that makes a stored
// program to what a protected connection refuses.
func definitionsAreNotReads(t *testing.T, res *ScriptResult) {
	t.Helper()
	made := 0
	for _, step := range res.Steps {
		if storedProgramRe.MatchString(step.SQL) {
			made++
			if !step.Risk.Destructive {
				t.Errorf("line %d makes a stored program and was classified %+v", step.Line, step.Risk)
			}
		}
	}
	if made == 0 {
		t.Error("the script made no stored program")
	}
}

func TestLiveStoredProgramsRunFromTheConsole(t *testing.T) {
	for _, e := range workbenchEngines() {
		t.Run(e.name, func(t *testing.T) {
			db, _ := openWorkbench(t, e)
			dropAfter(t, db, "jdt_script_splitter_t")
			mustExec(t, db, `CREATE TABLE jdt_script_splitter_t (n INT, label VARCHAR(20))`)
			switch e.driver {
			case DriverPostgres:
				livePostgresBodies(t, db)
			case DriverMySQL:
				liveMySQLBodies(t, e, db)
			case DriverMSSQL:
				liveSQLServerBodies(t, db)
			case DriverOracle:
				liveOracleBodies(t, db)
			}
		})
	}
}

func livePostgresBodies(t *testing.T, db *sql.DB) {
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP FUNCTION IF EXISTS jdt_script_splitter_twice(int)`)
		_, _ = db.Exec(`DROP FUNCTION IF EXISTS jdt_script_splitter_label(int)`)
	})
	// A body written in SQL, which psql reads by its BEGIN … END, beside one
	// in PL/pgSQL, which is a dollar-quoted text with semicolons of its own.
	res := runScript(t, db, DriverPostgres, `
CREATE OR REPLACE FUNCTION jdt_script_splitter_twice(n int) RETURNS int
LANGUAGE sql
BEGIN ATOMIC
  SELECT CASE WHEN n IS NULL THEN 0 ELSE n * 2 END;
END;
CREATE OR REPLACE FUNCTION jdt_script_splitter_label(n int) RETURNS text AS $body$
BEGIN
  IF n % 2 = 0 THEN RETURN 'even'; END IF;
  RETURN 'odd';
END;
$body$ LANGUAGE plpgsql;
SELECT jdt_script_splitter_twice(21), jdt_script_splitter_label(4);`, ScriptOptions{})
	if len(res.Steps) != 3 {
		t.Fatalf("%d statements, want 3", len(res.Steps))
	}
	if got := lastRow(t, res); got[0] != "42" || got[1] != "even" {
		t.Errorf("the functions answered %v", got)
	}
	definitionsAreNotReads(t, res)
}

func liveMySQLBodies(t *testing.T, e workbenchEngine, db *sql.DB) {
	t.Cleanup(func() { _, _ = db.Exec(`DROP PROCEDURE IF EXISTS jdt_script_splitter_fill`) })
	// The way a tutorial, Workbench and mysqldump write a procedure.
	res := runScript(t, db, DriverMySQL, `
DROP PROCEDURE IF EXISTS jdt_script_splitter_fill;
DELIMITER $$
CREATE PROCEDURE jdt_script_splitter_fill(IN n INT)
BEGIN
  DECLARE i INT DEFAULT 0;
  WHILE i < n DO
    SET i = i + 1;
    IF i % 2 = 0 THEN
      INSERT INTO jdt_script_splitter_t (n, label) VALUES (i, 'even');
    ELSE
      INSERT INTO jdt_script_splitter_t (n, label) VALUES (i, 'odd');
    END IF;
  END WHILE;
END$$
DELIMITER ;
CALL jdt_script_splitter_fill(4);
SELECT COUNT(*), SUM(n) FROM jdt_script_splitter_t WHERE label = 'even';`, ScriptOptions{})
	if len(res.Steps) != 4 {
		t.Fatalf("%d statements, want 4", len(res.Steps))
	}
	if got := lastRow(t, res); got[0] != "2" || got[1] != "6" {
		t.Errorf("after the procedure: %v, want 2 even rows summing to 6", got)
	}
	definitionsAreNotReads(t, res)

	// The way SHOW CREATE prints one: no DELIMITER, the body read by its
	// blocks — and a trigger whose whole body is one IF.
	t.Run("function and trigger", func(t *testing.T) {
		programs := mysqlProgramsDB(t, e, db)
		t.Cleanup(func() {
			_, _ = programs.Exec(`DROP FUNCTION IF EXISTS jdt_script_splitter_twice`)
			_, _ = programs.Exec(`DROP TRIGGER IF EXISTS jdt_script_splitter_clamp`)
		})
		res := runScript(t, programs, DriverMySQL, `
CREATE FUNCTION jdt_script_splitter_twice(n INT) RETURNS INT DETERMINISTIC NO SQL
BEGIN
  DECLARE doubled INT;
  SET doubled = n * 2;
  RETURN doubled;
END;
CREATE TRIGGER jdt_script_splitter_clamp BEFORE INSERT ON jdt_script_splitter_t FOR EACH ROW
IF NEW.n < 0 THEN SET NEW.n = 0; END IF;
INSERT INTO jdt_script_splitter_t (n, label) VALUES (-5, 'clamped');
SELECT jdt_script_splitter_twice(21), n FROM jdt_script_splitter_t WHERE label = 'clamped';`, ScriptOptions{})
		if got := lastRow(t, res); got[0] != "42" || got[1] != "0" {
			t.Errorf("the function and the trigger answered %v, want 42 and a clamped 0", got)
		}
		definitionsAreNotReads(t, res)
	})
}

// mysqlProgramsDB is where a stored function and a trigger can be made. With
// binary logging on, which MySQL 8 has by default, only an account with SUPER
// may make either (error 1419), and the fixture's application account has
// not got it: the administrator makes them, in the same database.
func mysqlProgramsDB(t *testing.T, e workbenchEngine, db *sql.DB) *sql.DB {
	t.Helper()
	if e.env != "JD_TEST_MYSQL8_DSN" {
		return db
	}
	admin := os.Getenv("JD_TEST_MYSQL8_ADMIN_DSN")
	if admin == "" {
		t.Skip("set JD_TEST_MYSQL8_ADMIN_DSN: with binary logging on, MySQL lets only an account with SUPER make a function or a trigger")
	}
	cfg, err := mysql.ParseDSN(admin)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mysql.ParseDSN(os.Getenv(e.env))
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = app.DBName
	return liveSQL(t, DriverMySQL, "JD_TEST_MYSQL8_ADMIN_DSN", cfg.FormatDSN())
}

func liveSQLServerBodies(t *testing.T, db *sql.DB) {
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP PROCEDURE IF EXISTS dbo.jdt_script_splitter_fill`)
	})
	// The way SSMS saves a script: batches between GO lines, the procedure's
	// body running to the GO after it.
	res := runScript(t, db, DriverMSSQL, `
IF OBJECT_ID('dbo.jdt_script_splitter_fill', 'P') IS NOT NULL DROP PROCEDURE dbo.jdt_script_splitter_fill;
GO
CREATE PROCEDURE dbo.jdt_script_splitter_fill @n int AS
BEGIN
  SET NOCOUNT ON;
  DECLARE @i int = 0;
  WHILE @i < @n
  BEGIN
    SET @i = @i + 1;
    IF @i % 2 = 0
      INSERT INTO dbo.jdt_script_splitter_t (n, label) VALUES (@i, 'even');
    ELSE
      INSERT INTO dbo.jdt_script_splitter_t (n, label) VALUES (@i, 'odd');
  END
END
GO
EXEC dbo.jdt_script_splitter_fill @n = 4;
SELECT COUNT(*), SUM(n) FROM dbo.jdt_script_splitter_t WHERE label = 'even';`, ScriptOptions{})
	if len(res.Steps) != 4 {
		t.Fatalf("%d statements, want 4", len(res.Steps))
	}
	if got := lastRow(t, res); got[0] != "2" || got[1] != "6" {
		t.Errorf("after the procedure: %v, want 2 even rows summing to 6", got)
	}
	definitionsAreNotReads(t, res)

	// GO 3 runs its batch three times, and a block's variable lives as long
	// as the block, which hands back what its query found.
	res = runScript(t, db, DriverMSSQL, `
INSERT INTO dbo.jdt_script_splitter_t (n, label) VALUES (100, 'again')
GO 3
BEGIN
  DECLARE @total int;
  SELECT @total = COUNT(*) FROM dbo.jdt_script_splitter_t WHERE label = 'again';
  SELECT @total AS total;
END`, ScriptOptions{Transaction: true})
	if len(res.Steps) != 4 || res.Transaction != ScriptCommitted {
		t.Fatalf("%d statements, %s; want the batch three times and the block, committed", len(res.Steps), res.Transaction)
	}
	if got := lastRow(t, res); got[0] != "3" {
		t.Errorf("the block counted %v rows of the repeated batch, want 3", got)
	}
}

func liveOracleBodies(t *testing.T, db *sql.DB) {
	t.Cleanup(func() {
		for _, drop := range []string{
			`DROP PROCEDURE jdt_script_splitter_fill`,
			`DROP PROCEDURE jdt_script_splitter_broken`,
			`DROP PACKAGE jdt_script_splitter_pkg`,
		} {
			_, _ = db.Exec(drop)
		}
	})
	// The way SQL*Plus and SQL Developer take a script: each PL/SQL unit
	// ends at a line holding only /, and keeps the semicolon after its END.
	res := runScript(t, db, DriverOracle, `
CREATE OR REPLACE PROCEDURE jdt_script_splitter_fill (n IN NUMBER) AS
BEGIN
  FOR i IN 1 .. n LOOP
    IF MOD(i, 2) = 0 THEN
      INSERT INTO jdt_script_splitter_t (n, label) VALUES (i, 'even');
    ELSE
      INSERT INTO jdt_script_splitter_t (n, label) VALUES (i, 'odd');
    END IF;
  END LOOP;
END jdt_script_splitter_fill;
/
BEGIN
  jdt_script_splitter_fill(4);
END;
/
SELECT COUNT(*), SUM(n) FROM jdt_script_splitter_t WHERE label = 'even'`, ScriptOptions{})
	if len(res.Steps) != 3 {
		t.Fatalf("%d statements, want 3", len(res.Steps))
	}
	if got := lastRow(t, res); got[0] != "2" || got[1] != "6" {
		t.Errorf("after the procedure: %v, want 2 even rows summing to 6", got)
	}
	definitionsAreNotReads(t, res)

	// A package and its body, called from a query; and a query that
	// declares a function of its own. A block that leaves the transaction
	// alone runs inside the script's.
	res = runScript(t, db, DriverOracle, `
CREATE OR REPLACE PACKAGE jdt_script_splitter_pkg AS
  FUNCTION twice(n NUMBER) RETURN NUMBER;
END jdt_script_splitter_pkg;
/
CREATE OR REPLACE PACKAGE BODY jdt_script_splitter_pkg AS
  FUNCTION twice(n NUMBER) RETURN NUMBER IS
  BEGIN
    RETURN n * 2;
  END;
END jdt_script_splitter_pkg;
/
BEGIN
  INSERT INTO jdt_script_splitter_t (n, label) VALUES (jdt_script_splitter_pkg.twice(50), 'package');
END;
/
WITH FUNCTION thrice(n NUMBER) RETURN NUMBER IS BEGIN RETURN n * 3; END;
SELECT thrice(n) FROM jdt_script_splitter_t WHERE label = 'package'
/`, ScriptOptions{Transaction: true})
	if got := lastRow(t, res); got[0] != "300" {
		t.Errorf("the package and the query's own function answered %v, want 300", got)
	}

	// Oracle keeps a procedure that does not compile and calls its CREATE a
	// success; the step says it failed, in the compiler's words.
	statements, err := ParseScript(DriverOracle, "CREATE OR REPLACE PROCEDURE jdt_script_splitter_broken AS\nBEGIN\n  jdt_script_splitter_missing;\nEND;\n/")
	if err != nil {
		t.Fatal(err)
	}
	broken, err := RunScript(context.Background(), db, DriverOracle, statements, ScriptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if broken.Failed != 0 || !strings.Contains(broken.Steps[0].Error, "compilation errors") ||
		!strings.Contains(broken.Steps[0].Error, "PLS-00201") || !strings.Contains(broken.Steps[0].Error, "line 3") {
		t.Errorf("a procedure that does not compile = %+v", broken.Steps[0])
	}
}

func TestLiveSQLiteTriggerRunsFromTheConsole(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "jdt_script_splitter.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	res := runScript(t, db, DriverSQLite, `
CREATE TABLE jdt_script_splitter_t (id INTEGER PRIMARY KEY, n INTEGER, label TEXT);
CREATE TABLE jdt_script_splitter_log (note TEXT);
CREATE TRIGGER jdt_script_splitter_trg AFTER INSERT ON jdt_script_splitter_t
BEGIN
  INSERT INTO jdt_script_splitter_log (note) VALUES (CASE WHEN NEW.n % 2 = 0 THEN 'even' ELSE 'odd' END);
  UPDATE jdt_script_splitter_t SET label = 'seen' WHERE id = NEW.id;
END;
INSERT INTO jdt_script_splitter_t (n) VALUES (1), (2), (4);
SELECT (SELECT COUNT(*) FROM jdt_script_splitter_log WHERE note = 'even'),
       (SELECT COUNT(*) FROM jdt_script_splitter_t WHERE label = 'seen');`, ScriptOptions{})
	if len(res.Steps) != 5 {
		t.Fatalf("%d statements, want 5", len(res.Steps))
	}
	if got := lastRow(t, res); got[0] != "2" || got[1] != "3" {
		t.Errorf("after the trigger: %v, want 2 even notes and 3 rows seen", got)
	}
	definitionsAreNotReads(t, res)
}
