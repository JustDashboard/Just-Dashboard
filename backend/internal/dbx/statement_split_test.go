package dbx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// splitCase is one script of testdata/statement-splits.json: the statements
// it holds, where each begins, and — for a SQL Server batch that GO repeats —
// the order they run in. The editor's own reading of a script
// (frontend/src/components/database/query/sql-text.ts) is held to the same
// file, which is what keeps "run the statement at the cursor" sending the
// statement the server will read as one.
type splitCase struct {
	Name       string `json:"name"`
	Driver     Driver `json:"driver"`
	SQL        string `json:"sql"`
	Statements []struct {
		SQL  string `json:"sql"`
		Line int    `json:"line"`
	} `json:"statements"`
	Runs []int `json:"runs"`
}

func readSplitCases(t *testing.T) []splitCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "statement-splits.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []splitCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func TestScriptsSplitWhereTheirEngineReadsThem(t *testing.T) {
	cases := readSplitCases(t)
	drivers := map[Driver]bool{}
	for _, c := range cases {
		drivers[c.Driver] = true
		t.Run(string(c.Driver)+"/"+c.Name, func(t *testing.T) {
			statements, err := ParseScript(c.Driver, c.SQL)
			if err != nil {
				t.Fatal(err)
			}
			runs := c.Runs
			if runs == nil {
				for i := range c.Statements {
					runs = append(runs, i)
				}
			}
			if len(statements) != len(runs) {
				var got []string
				for _, st := range statements {
					got = append(got, st.SQL)
				}
				t.Fatalf("%d statements, want %d: %q", len(statements), len(runs), got)
			}
			for i, at := range runs {
				want := c.Statements[at]
				if statements[i].SQL != want.SQL || statements[i].Line != want.Line {
					t.Errorf("statement %d = line %d %q,\nwant line %d %q", i+1, statements[i].Line, statements[i].SQL, want.Line, want.SQL)
				}
			}
		})
	}
	for _, driver := range []Driver{DriverPostgres, DriverMySQL, DriverSQLite, DriverMSSQL, DriverOracle} {
		if !drivers[driver] {
			t.Errorf("no script for %s", driver)
		}
	}
}

func TestScriptSeparatorsThatCannotBeReadAreRefused(t *testing.T) {
	for _, c := range []struct {
		driver Driver
		query  string
		says   string
	}{
		{DriverMySQL, "SELECT 1;\nDELIMITER\nSELECT 2", "line 2: DELIMITER names no delimiter"},
		{DriverMySQL, "DELIMITER   \nSELECT 2", "line 1: DELIMITER names no delimiter"},
		{DriverMySQL, "DELIMITER end\nSELECT 2end", "not punctuation"},
		{DriverMySQL, "DELIMITER '\nSELECT 2'", "not punctuation"},
		{DriverMySQL, `DELIMITER \\` + "\nSELECT 1", "not punctuation"},
		{DriverMySQL, "DELIMITER --\nSELECT 1", "would begin a comment"},
		{DriverMySQL, "DELIMITER #\nSELECT 1", "would begin a comment"},
		{DriverMySQL, "DELIMITER /*\nSELECT 1", "would begin a comment"},
		{DriverMSSQL, "SELECT 1\nGO 0", "line 2: GO 0"},
		{DriverMSSQL, "SELECT 1\nGO 501", "GO 501"},
		{DriverMSSQL, "SELECT 1\nGO 99999999999999999999", "GO 99999999999999999999"},
		// Five hundred statements is a script's whole budget, however it is reached.
		{DriverMSSQL, "SELECT 1;\nSELECT 2\nGO 300", "at most 500 statements"},
	} {
		if _, err := ParseScript(c.driver, c.query); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("ParseScript(%s, %q) = %v, want it refused saying %q", c.driver, c.query, err, c.says)
		}
		if risk := ClassifyFor(c.driver, c.query); !risk.Destructive {
			t.Errorf("ClassifyFor(%s, %q) = %+v: a script that cannot be read was not held to the most", c.driver, c.query, risk)
		}
	}
}

// What only a client takes for a separator is that client's alone: the same
// line means nothing, or something else, to every other engine.
func TestSeparatorsBelongToTheirEngine(t *testing.T) {
	for _, c := range []struct {
		driver Driver
		query  string
		want   int
	}{
		{DriverPostgres, "SELECT 1\nGO\nSELECT 2", 1},
		{DriverMySQL, "SELECT 1\nGO\nSELECT 2", 1},
		{DriverOracle, "SELECT 1 FROM dual\nGO\nSELECT 2 FROM dual", 1},
		{DriverMSSQL, "SELECT 10\n/\n2", 1},
		{DriverPostgres, "SELECT 10\n/\n2", 1},
		// DELIMITER is the mysql client's, and only where a statement begins.
		{DriverPostgres, "DELIMITER //\nSELECT 1//", 1},
		{DriverMySQL, "SELECT delimiter FROM t;\nSELECT 2", 2},
		{DriverMySQL, "SELECT 1,\nDELIMITER $$\nSELECT 2", 1},
		// GO is a separator only on a line of its own.
		{DriverMSSQL, "SELECT 1 GO\nSELECT 2", 1},
		{DriverMSSQL, "SELECT 1\nGO SELECT 2", 1},
		{DriverMSSQL, "SELECT 1\nEXEC go", 1},
	} {
		statements, err := ParseScript(c.driver, c.query)
		if err != nil {
			t.Errorf("ParseScript(%s, %q): %v", c.driver, c.query, err)
			continue
		}
		if len(statements) != c.want {
			t.Errorf("ParseScript(%s, %q) = %d statements, want %d", c.driver, c.query, len(statements), c.want)
		}
	}
}

// A statement that holds a body is never a read, whatever word leads it, so a
// protected connection refuses it and a role that may not destroy cannot send
// it. The DELETE without a WHERE in a body is found though another statement
// of the body has one.
func TestStatementsWithABodyAreNotReads(t *testing.T) {
	for _, c := range []struct {
		driver Driver
		query  string
		level  string
	}{
		{DriverMySQL, "CREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END", "high"},
		{DriverMySQL, "CREATE PROCEDURE p() SELECT 1", "high"},
		{DriverMySQL, "DELIMITER $$\nCREATE PROCEDURE p() BEGIN SELECT 1; END$$", "high"},
		{DriverMySQL, "BEGIN NOT ATOMIC SELECT 1; END", "high"},
		// A delimiter of the script's own makes one statement of two reads.
		{DriverMySQL, "DELIMITER $$\nSELECT 1; SELECT 2$$", "high"},
		{DriverMySQL, "CREATE TRIGGER t BEFORE INSERT ON x FOR EACH ROW BEGIN SET NEW.a = 1; END", "high"},
		{DriverSQLite, "CREATE TRIGGER t AFTER INSERT ON x BEGIN SELECT 1; END", "high"},
		{DriverPostgres, "CREATE FUNCTION f() RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT 1; END", "high"},
		{DriverMSSQL, "CREATE PROCEDURE p AS BEGIN SELECT 1; END", "high"},
		{DriverMSSQL, "BEGIN SELECT 1; SELECT 2; END", "high"},
		{DriverOracle, "BEGIN NULL; END;", "high"},
		{DriverOracle, "CREATE OR REPLACE PROCEDURE p AS BEGIN NULL; END;", "high"},
		// A query that declares a function of its own can write through it
		// (PRAGMA AUTONOMOUS_TRANSACTION), and leads with WITH all the same.
		{DriverOracle, "WITH FUNCTION f RETURN NUMBER IS BEGIN RETURN 1; END;\nSELECT f FROM dual", "high"},
		{DriverMySQL, "CREATE PROCEDURE p() BEGIN DELETE FROM log; SELECT * FROM t WHERE id = 1; END", "critical"},
		{DriverOracle, "BEGIN UPDATE t SET a = 1; DELETE FROM u WHERE id = 1; END;", "critical"},
		{DriverMSSQL, "CREATE PROCEDURE p AS BEGIN UPDATE t SET a = 1 WHERE id = 1; END", "high"},
	} {
		statements, err := ParseScript(c.driver, c.query)
		if err != nil {
			t.Errorf("ParseScript(%s, %q): %v", c.driver, c.query, err)
			continue
		}
		if len(statements) != 1 {
			t.Errorf("ParseScript(%s, %q) = %d statements, want one", c.driver, c.query, len(statements))
			continue
		}
		if risk := statements[0].Risk; risk.Level != c.level || !risk.Destructive {
			t.Errorf("%s %q classified %+v, want %s", c.driver, c.query, risk, c.level)
		}
		if _, err := explainStatement(c.driver, c.query); err == nil {
			t.Errorf("%s %q: a body was accepted for a plan", c.driver, c.query)
		}
	}
	// The body is not mistaken for a statement of its own when it has none.
	for _, query := range []string{"SELECT 1", "SELECT 1;", "SELECT 'a;b'"} {
		if st, err := SingleStatementFor(DriverOracle, query); err != nil || st.Risk.Level != "read" {
			t.Errorf("%q = %+v %v, want a read", query, st, err)
		}
	}
}

func TestBlocksRunNowAndDefinitionsLater(t *testing.T) {
	for _, c := range []struct {
		driver                     Driver
		query                      string
		block, rows, ownTransation bool
	}{
		{DriverOracle, "BEGIN NULL; END;", true, false, false},
		{DriverOracle, "BEGIN INSERT INTO t VALUES (1); COMMIT; END;", true, false, true},
		{DriverOracle, "DECLARE n NUMBER; BEGIN n := 1; END;", true, false, false},
		{DriverOracle, "CREATE OR REPLACE PROCEDURE p AS BEGIN COMMIT; END;", false, false, false},
		{DriverMSSQL, "BEGIN SELECT 1 AS one; END", true, true, false},
		{DriverMSSQL, "BEGIN TRAN", false, false, true},
		{DriverMSSQL, "BEGIN TRANSACTION", false, false, true},
		{DriverMSSQL, "IF @@TRANCOUNT > 0 COMMIT", true, true, true},
		{DriverMSSQL, "CREATE PROCEDURE p AS BEGIN SELECT 1; END", false, false, false},
		{DriverMySQL, "BEGIN NOT ATOMIC SELECT 1; END", true, true, false},
		{DriverMySQL, "BEGIN", false, false, true},
		{DriverMySQL, "CALL p()", false, true, false},
		{DriverPostgres, "BEGIN", false, false, true},
	} {
		st, err := SingleStatementFor(c.driver, c.query)
		if err != nil {
			t.Errorf("%s %q: %v", c.driver, c.query, err)
			continue
		}
		if st.block != c.block || st.returnsRows != c.rows || st.ownTransaction != c.ownTransation {
			t.Errorf("%s %q: block %v, rows %v, own transaction %v; want %v %v %v",
				c.driver, c.query, st.block, st.returnsRows, st.ownTransaction, c.block, c.rows, c.ownTransation)
		}
	}
}

// A PL/SQL block in a script run as one transaction is refused when it
// commits or rolls back itself, before anything is sent.
func TestAScriptInOneTransactionRefusesABlockThatCommits(t *testing.T) {
	commits, err := ParseScript(DriverOracle, "BEGIN INSERT INTO t VALUES (1); COMMIT; END;")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunScript(t.Context(), nil, DriverOracle, commits, ScriptOptions{Transaction: true}); err == nil || !strings.Contains(err.Error(), "line 1 manages a transaction itself") {
		t.Errorf("a block that commits = %v", err)
	}
}

func TestOracleProgramIsReadFromItsCreate(t *testing.T) {
	for _, c := range []struct {
		query string
		want  *storedProgram
	}{
		{"CREATE OR REPLACE PROCEDURE jdt_p AS BEGIN NULL; END;", &storedProgram{"PROCEDURE", "", "JDT_P"}},
		{"create function f(n number) return number is begin return n; end;", &storedProgram{"FUNCTION", "", "F"}},
		{`create or replace package body "Mixed"."Pkg" as end;`, &storedProgram{"PACKAGE BODY", "Mixed", "Pkg"}},
		{"CREATE OR REPLACE EDITIONABLE TRIGGER app.trg BEFORE INSERT ON t BEGIN NULL; END;", &storedProgram{"TRIGGER", "APP", "TRG"}},
		{"CREATE TYPE BODY t AS MEMBER FUNCTION f RETURN NUMBER IS BEGIN RETURN 1; END; END;", &storedProgram{"TYPE BODY", "", "T"}},
		{"CREATE PACKAGE p AS END;", &storedProgram{"PACKAGE", "", "P"}},
		{"CREATE PROCEDURE IF NOT EXISTS p AS BEGIN NULL; END;", &storedProgram{"PROCEDURE", "", "P"}},
		{`CREATE PROCEDURE "a""b" AS BEGIN NULL; END;`, &storedProgram{"PROCEDURE", "", `a"b`}},
		{"CREATE OR REPLACE AND COMPILE JAVA SOURCE NAMED x AS class X {}", nil},
	} {
		st, err := SingleStatementFor(DriverOracle, c.query)
		if err != nil {
			t.Errorf("%q: %v", c.query, err)
			continue
		}
		got := st.program
		if (got == nil) != (c.want == nil) || got != nil && *got != *c.want {
			t.Errorf("%q: program %+v, want %+v", c.query, got, c.want)
		}
	}
	// Only a CREATE names a program to check.
	if st, err := SingleStatementFor(DriverOracle, "BEGIN NULL; END;"); err != nil || st.program != nil {
		t.Errorf("a block = %+v %v", st, err)
	}
}
