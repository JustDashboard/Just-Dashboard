package dbx

import "testing"

// What the engine-aware classifier decides that the old one, which read every
// engine the same way, got wrong in one direction or the other.
func TestClassifyForReadsEachEngine(t *testing.T) {
	cases := []struct {
		driver Driver
		query  string
		level  string
	}{
		// SQL Server needs no separator between two statements, so a leading
		// SELECT says nothing about what follows it.
		{DriverMSSQL, "SELECT 1 EXEC xp_cmdshell 'whoami'", "high"},
		{DriverMSSQL, "SELECT 1 DELETE FROM t", "critical"},
		{DriverMSSQL, "SELECT 1DELETE FROM t", "critical"},
		{DriverMSSQL, "SELECT name FROM #tmp WHERE id = @id", "read"},
		{DriverMSSQL, "SELECT 1 WAITFOR DELAY '00:10:00'", "high"},
		// Service Broker's verbs follow a SELECT the same way, and RECEIVE
		// takes off the queue what it returns.
		{DriverMSSQL, "SELECT 1 RECEIVE TOP (100) * FROM dbo.OrdersQueue", "high"},
		{DriverMSSQL, "SELECT 1 END CONVERSATION @handle", "high"},
		{DriverMSSQL, "SELECT CASE WHEN n > 1 THEN 'many' ELSE 'one' END FROM t", "read"},
		{DriverPostgres, "SELECT send, receive FROM mail", "read"},
		// A CTE can write on PostgreSQL while the statement leads with WITH.
		{DriverPostgres, "WITH gone AS (DELETE FROM t WHERE id = 1 RETURNING *) SELECT * FROM gone", "high"},
		{DriverPostgres, "WITH x AS (SELECT 1) MERGE INTO t USING x ON true WHEN NOT MATCHED THEN INSERT (a) VALUES (1)", "high"},
		{DriverPostgres, "SELECT * INTO backup FROM users", "medium"},
		{DriverMySQL, "SELECT * FROM users INTO OUTFILE '/tmp/x'", "high"},
		{DriverPostgres, "COPY (SELECT 1) TO PROGRAM 'curl example.org'", "critical"},
		{DriverPostgres, "EXPLAIN ANALYZE DELETE FROM t WHERE id = 1", "high"},
		// Oracle's EXPLAIN PLAN answers with nothing and writes the plan into
		// the session's plan table, so there it is not a read.
		{DriverOracle, "EXPLAIN PLAN FOR SELECT * FROM t", "medium"},
		{DriverOracle, "EXPLAIN PLAN FOR DELETE FROM t", "critical"},
		{DriverPostgres, "EXPLAIN SELECT * FROM t", "read"},
		{DriverMySQL, "EXPLAIN SELECT * FROM t", "read"},
		{DriverPostgres, "REFRESH MATERIALIZED VIEW totals", "high"},
		{DriverPostgres, "BEGIN", "high"},
		{DriverSQLite, "PRAGMA journal_mode = WAL", "high"},
		{DriverMySQL, "INSERT t VALUES (1)", "medium"},
		// These add, and take away what was there to make room.
		{DriverMySQL, "REPLACE INTO t VALUES (1)", "high"},
		{DriverSQLite, "INSERT OR REPLACE INTO t VALUES (1)", "high"},
		{DriverMySQL, "CREATE OR REPLACE TABLE t (id INT)", "high"},
		{DriverPostgres, "CREATE OR REPLACE VIEW v AS SELECT 1", "high"},
		{DriverPostgres, "CREATE VIEW v AS SELECT 1", "medium"},
		{DriverMySQL, "CREATE OR /* not a way round */ REPLACE TABLE t (id INT)", "high"},
		{DriverPostgres, "SELECT replace(name, 'or replace', 'x') FROM t", "read"},
		// An account is a permission, however it is spelled.
		{DriverPostgres, "CREATE ROLE app SUPERUSER LOGIN PASSWORD 'x'", "high"},
		{DriverMySQL, "CREATE USER 'app'@'%' IDENTIFIED BY 'x'", "high"},
		{DriverPostgres, "CREATE /* x */ ROLE app", "high"},
		// A column called role is not an account.
		{DriverPostgres, "CREATE TABLE members (id int, role text, login text)", "medium"},
		// A SELECT that ends somebody's session, or writes a file, is not a
		// read; the routes that do those things by name are destructive.
		{DriverPostgres, "SELECT pg_terminate_backend(1234)", "high"},
		{DriverPostgres, "SELECT lo_export(1, '/tmp/x')", "high"},
		{DriverPostgres, "SELECT * FROM dblink('dbname=x', 'SELECT 1') AS t(a int)", "high"},
		{DriverPostgres, "SELECT pg_size_pretty(pg_database_size(current_database()))", "read"},

		// Valid SQL the engine-blind splitter refused outright.
		{DriverPostgres, "SELECT doc #>> '{a,b}' FROM t", "read"},
		{DriverPostgres, "SELECT 1--trailing comment", "read"},
		{DriverMySQL, "SELECT 1 # trailing comment", "read"},
		{DriverOracle, "SELECT sid, serial# FROM v$session", "read"},
		{DriverPostgres, "SELECT /*+ SeqScan(t) */ * FROM t", "read"},
		{DriverPostgres, "(SELECT 1) UNION (SELECT 2)", "read"},

		// A verb is a word. A column that contains one is not.
		{DriverPostgres, "SELECT updated_at, deleted_flag, is_dropped, create_time FROM t", "read"},
		{DriverPostgres, "SELECT replace(name, 'a', 'b') FROM t", "read"},

		// A comment around a statement is not sent, so it is not judged. One
		// inside the statement is sent with it, and is.
		{DriverPostgres, "SELECT 1 -- delete this later", "read"},
		{DriverPostgres, "-- drop the old rows\nSELECT 1", "read"},
		{DriverPostgres, "SELECT /* delete this later */ 1", "critical"},

		// The WHERE that scopes a statement has to be code.
		{DriverPostgres, "DELETE /* where */ FROM t", "critical"},
		{DriverPostgres, "UPDATE t SET note = 'where'", "critical"},
		{DriverPostgres, "DELETE FROM t WHERE id = 1", "high"},
	}
	for _, c := range cases {
		got := ClassifyFor(c.driver, c.query)
		if got.Level != c.level {
			t.Errorf("ClassifyFor(%s, %q) = %q %v, want %q", c.driver, c.query, got.Level, got.Reasons, c.level)
		}
		if want := c.level == "high" || c.level == "critical"; got.Destructive != want {
			t.Errorf("ClassifyFor(%s, %q).Destructive = %v", c.driver, c.query, got.Destructive)
		}
	}
}

func TestWorstRiskKeepsTheStrongestVerdict(t *testing.T) {
	statements, err := ParseScript(DriverPostgres,
		"SELECT 1; INSERT INTO t(a) VALUES (1); DROP TABLE t; SELECT 2; DROP TABLE u")
	if err != nil {
		t.Fatal(err)
	}
	risk := WorstRisk(statements)
	if risk.Level != "critical" || !risk.Destructive {
		t.Fatalf("worst = %+v", risk)
	}
	// Each reason once, however many statements gave it.
	seen := map[string]int{}
	for _, reason := range risk.Reasons {
		seen[reason]++
	}
	for reason, n := range seen {
		if n != 1 {
			t.Errorf("reason %q listed %d times", reason, n)
		}
	}
	if risk := WorstRisk(nil); risk.Level != "read" || risk.Destructive || risk.Reasons == nil {
		t.Errorf("an empty script classified %+v", risk)
	}
}
