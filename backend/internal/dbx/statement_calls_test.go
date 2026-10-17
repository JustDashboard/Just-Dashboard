package dbx

import "testing"

// A statement that leads with SELECT and still does what a read-only scope
// does not stop: it runs a string as SQL, or leaves something on a connection
// that the next request will use. Each of these was classified as a read.
func TestSelectsThatDoMoreThanReadAreNotReads(t *testing.T) {
	cases := []struct {
		driver Driver
		query  string
	}{
		// Text handed to a function as SQL, built so that no reading of the
		// statement can know what it runs.
		{DriverPostgres, `SELECT query_to_xml('select set_'||'config(''role'', ''postgres'', false)', true, false, '')`},
		{DriverPostgres, `SELECT query_to_xml_and_xmlschema('select 1', true, false, '')`},
		{DriverPostgres, `SELECT query_to_xmlschema('select 1', true, false, '')`},
		{DriverPostgres, `SELECT cursor_to_xml('c'::refcursor, 1, false, false, '')`},
		{DriverPostgres, `SELECT cursor_to_xmlschema('c'::refcursor, false, false, '')`},
		{DriverPostgres, `SELECT table_to_xml('t'::regclass, false, false, '')`},
		{DriverPostgres, `SELECT table_to_xmlschema('t'::regclass, false, false, '')`},
		{DriverPostgres, `SELECT table_to_xml_and_xmlschema('t'::regclass, false, false, '')`},
		{DriverPostgres, `SELECT pg_catalog.query_to_xml('select 1', true, false, '')`},
		{DriverPostgres, `SELECT "query_to_xml"('select 1', true, false, '')`},
		{DriverPostgres, `SELECT QUERY_TO_XML('select 1', true, false, '')`},
		{DriverPostgres, `SELECT * FROM ts_stat('select to_tsvector(''a b'')')`},
		{DriverPostgres, `SELECT ts_rewrite('a & b'::tsquery, 'select ''a''::tsquery, ''c''::tsquery')`},
		{DriverPostgres, `SELECT * FROM crosstab('select 1, 2, 3') AS ct(a int, b int)`},
		{DriverPostgres, `SELECT * FROM crosstab2('select 1, 2, 3')`},
		{DriverPostgres, `SELECT * FROM xpath_table('id', 'doc', 't', '/a', 'true') AS x(id int)`},
		{DriverPostgres, `SELECT * FROM connectby('t; select 1 --', 'id', 'parent', '1', 0) AS c(id int, parent int, level int)`},
		{DriverPostgres, `SELECT pg_background_launch('select 1')`},
		{DriverPostgres, `SELECT dblink_connect_u('c', 'dbname=x')`},
		{DriverPostgres, `SELECT dblink_send_query('c', 'select 1')`},
		{DriverPostgres, `SELECT dblink_open('c', 'k', 'select 1')`},
		{DriverPostgres, `SELECT dblink_get_result('c')`},
		{DriverPostgres, `SELECT dblink_disconnect('c')`},
		{DriverPostgres, `SELECT * FROM dblink('dbname=x', 'select 1') AS t(a int)`},
		// A function's name cannot be put together from pieces in the text
		// the engine reads; the one thing that runs a string is a function
		// that takes one, and that is above.
		{DriverPostgres, `SELECT query_to_xml('select pg_terminate_'||'backend(1)', true, false, '')`},
		// Statistics, locks and WAL, which a read-only transaction allows.
		{DriverPostgres, `SELECT pg_stat_statements_reset()`},
		{DriverPostgres, `SELECT pg_stat_reset()`},
		{DriverPostgres, `SELECT pg_stat_reset_shared('bgwriter')`},
		{DriverPostgres, `SELECT pg_stat_reset_single_table_counters('t'::regclass)`},
		{DriverPostgres, `SELECT pg_stat_kcache_reset()`},
		{DriverPostgres, `SELECT pg_advisory_lock(1)`},
		{DriverPostgres, `SELECT pg_advisory_lock_shared(1)`},
		{DriverPostgres, `SELECT pg_try_advisory_lock(1)`},
		{DriverPostgres, `SELECT pg_advisory_xact_lock(1)`},
		{DriverPostgres, `SELECT pg_advisory_unlock_all()`},
		{DriverPostgres, `SELECT pg_logical_emit_message(false, 'p', 'x')`},
		{DriverPostgres, `SELECT pg_replication_origin_advance('o', '0/0')`},
		{DriverPostgres, `SELECT pg_wal_replay_pause()`},
		{DriverPostgres, `SELECT heap_force_kill('t'::regclass, ARRAY['(0,1)']::tid[])`},
		{DriverPostgres, `SELECT nextval('s')`},
		{DriverPostgres, `SELECT setval('s', 10)`},
		// MySQL and MariaDB: a lock and a variable belong to the connection.
		{DriverMySQL, `SELECT GET_LOCK('l', 0)`},
		{DriverMySQL, `SELECT RELEASE_LOCK('l')`},
		{DriverMySQL, `SELECT RELEASE_ALL_LOCKS()`},
		{DriverMySQL, `SELECT service_get_write_locks('ns', 'l', 0)`},
		{DriverMySQL, `SELECT @v := 7`},
		{DriverMySQL, `SELECT @v:=7`},
		{DriverMySQL, `SELECT NEXTVAL(s)`},
		{DriverMySQL, `SELECT NEXT VALUE FOR s`},
		{DriverMySQL, `SELECT SETVAL(s, 100)`},
		// SQL Server: a sequence value taken inside the rolled-back
		// transaction is not given back.
		{DriverMSSQL, `SELECT NEXT VALUE FOR dbo.s`},
		{DriverMSSQL, `SELECT NEXT /* x */ VALUE FOR dbo.s`},
		// Oracle: the same for NEXTVAL, and packages that run text or reach out.
		{DriverOracle, `SELECT s.NEXTVAL FROM dual`},
		{DriverOracle, `SELECT dbms_xmlgen.getxml('select 1 from dual') FROM dual`},
		{DriverOracle, `SELECT dbms_xmlquery.getxml('select 1 from dual') FROM dual`},
		{DriverOracle, `SELECT sys.dbms_sql.open_cursor FROM dual`},
		{DriverOracle, `SELECT utl_http.request('http://example.org') FROM dual`},
		{DriverOracle, `SELECT utl_inaddr.get_host_address('example.org') FROM dual`},
		{DriverOracle, `SELECT httpuritype('http://example.org').getclob() FROM dual`},
		{DriverOracle, `SELECT dbms_pipe.send_message('p') FROM dual`},
		{DriverOracle, `SELECT dbms_lock.request(1) FROM dual`},
		{DriverOracle, `WITH FUNCTION f RETURN NUMBER IS BEGIN EXECUTE IMMEDIATE 'x'`},
		// An engine named by nobody meets every rule.
		{"", `SELECT query_to_xml('select 1', true, false, '')`},
		{"", `SELECT GET_LOCK('l', 0)`},
		{"", `SELECT utl_http.request('x') FROM dual`},
	}
	for _, c := range cases {
		got := ClassifyFor(c.driver, c.query)
		if got.Level != "high" && got.Level != "critical" || !got.Destructive {
			t.Errorf("ClassifyFor(%q, %q) = %q %v, want at least high", c.driver, c.query, got.Level, got.Reasons)
		}
	}
}

// What the rules above must not catch: the same words as parts of other
// names, and the ordinary reads next to them.
func TestCallRulesLeaveOrdinaryReadsAlone(t *testing.T) {
	cases := []struct {
		driver Driver
		query  string
	}{
		{DriverPostgres, `SELECT advisory, reset_count, stat_reset_at FROM t`},
		{DriverPostgres, `SELECT crosstab_total, ts_stats, query_total FROM t`},
		{DriverPostgres, `SELECT * FROM pg_stat_statements ORDER BY total_exec_time DESC LIMIT 10`},
		{DriverPostgres, `SELECT pg_size_pretty(pg_database_size(current_database())), pg_get_viewdef('v'::regclass)`},
		{DriverPostgres, `SELECT current_setting('role'), currval('s'), lastval()`},
		{DriverPostgres, `SELECT pg_stat_get_db_stat_reset_time(oid), stats_reset FROM pg_database`},
		{DriverPostgres, `SELECT xmlelement(name a, 1), table_name FROM information_schema.tables`},
		{DriverMySQL, `SELECT IS_FREE_LOCK('l'), IS_USED_LOCK('l'), lock_name FROM t`},
		{DriverMySQL, `SELECT a, b FROM t WHERE c = 'x:y'`},
		{DriverMSSQL, `SELECT next_id, value_for FROM t`},
		{DriverOracle, `SELECT dbms_metadata.get_ddl('TABLE', 'T') FROM dual`},
		{DriverOracle, `SELECT * FROM TABLE(dbms_xplan.display_cursor)`},
		{DriverOracle, `SELECT dbms_lob.getlength(c), utl_raw.cast_to_raw('a'), sys_context('userenv', 'sid') FROM t`},
		{DriverOracle, `SELECT sequence_name, last_number FROM user_sequences`},
	}
	for _, c := range cases {
		if got := ClassifyFor(c.driver, c.query); got.Level != "read" {
			t.Errorf("ClassifyFor(%q, %q) = %q %v, want read", c.driver, c.query, got.Level, got.Reasons)
		}
	}
}

// The ways each engine runs a string as SQL from the statement level were
// never reads, because the statement does not lead with a read; they are held
// here so that stays so.
func TestStatementsThatRunAStringAreNotReads(t *testing.T) {
	cases := []struct {
		driver Driver
		query  string
	}{
		{DriverMySQL, `PREPARE s FROM CONCAT('DEL', 'ETE FROM t')`},
		{DriverMySQL, `PREPARE s FROM 'select 1'`},
		{DriverMySQL, `EXECUTE s`},
		{DriverMySQL, `EXECUTE s USING @a`},
		{DriverMySQL, `EXECUTE IMMEDIATE 'select 1'`},
		{DriverMySQL, `DEALLOCATE PREPARE s`},
		{DriverMySQL, `SET @s = CONCAT('DEL', 'ETE FROM t')`},
		{DriverMySQL, `CALL sys.execute_prepared_stmt('select 1')`},
		{DriverMySQL, `CALL sys.ps_truncate_all_tables(FALSE)`},
		{DriverMySQL, `CALL sys.diagnostics(1, 1, 'current')`},
		{DriverMySQL, `DO GET_LOCK('l', 0)`},
		{DriverMySQL, `DO SLEEP(1)`},
		{DriverMSSQL, `EXEC('select 1')`},
		{DriverMSSQL, `EXEC (@sql)`},
		{DriverMSSQL, `EXECUTE(@sql)`},
		{DriverMSSQL, `EXEC sp_executesql @sql`},
		{DriverMSSQL, `EXEC sp_executesql N'select 1'`},
		{DriverMSSQL, `sp_executesql @sql`},
		{DriverMSSQL, `sp_executesql N'select 1'`},
		{DriverMSSQL, `DECLARE @sql nvarchar(max) = N'select 1'`},
		{DriverMSSQL, `SELECT 1 EXEC(@sql)`},
		{DriverMSSQL, `SELECT 1 EXEC sp_executesql @sql`},
		{DriverMSSQL, `SELECT 1 EXECUTE ('select 1')`},
		{DriverMSSQL, `SELECT * FROM OPENQUERY(linked, 'select 1')`},
		{DriverMSSQL, `SELECT * FROM OPENROWSET('SQLNCLI', 'Server=x;', 'select 1')`},
		{DriverOracle, `EXECUTE IMMEDIATE 'select 1 from dual'`},
		{DriverOracle, `BEGIN EXECUTE IMMEDIATE 'select 1 from dual'; END`},
		{DriverOracle, `DECLARE c INTEGER; BEGIN c := DBMS_SQL.OPEN_CURSOR; END`},
		{DriverOracle, `CALL dbms_sql.close_cursor(1)`},
	}
	for _, c := range cases {
		if got := ClassifyFor(c.driver, c.query); got.Level == "read" || got.Level == "medium" {
			t.Errorf("ClassifyFor(%q, %q) = %q %v, want at least high", c.driver, c.query, got.Level, got.Reasons)
		}
	}
}

// A name both lists cover is given one reason, not the same one twice.
func TestAServerFunctionIsGivenOneReason(t *testing.T) {
	for _, query := range []string{
		`SELECT pg_create_restore_point('r')`,
		`SELECT pg_drop_replication_slot('s')`,
		`SELECT pg_replication_origin_drop('o')`,
		`SELECT dblink_exec('c', 'select 1')`,
	} {
		st := mustStatement(t, DriverPostgres, query)
		seen := map[string]bool{}
		for _, reason := range st.Risk.Reasons {
			if seen[reason] {
				t.Errorf("%q gives %q twice: %v", query, reason, st.Risk.Reasons)
			}
			seen[reason] = true
		}
		if st.Risk.Level != "high" {
			t.Errorf("%q = %q %v, want high", query, st.Risk.Level, st.Risk.Reasons)
		}
	}
}

func TestACallReasonNamesTheCallAndDoesNotChangeBetweenRuns(t *testing.T) {
	const query = `SELECT pg_advisory_lock(1), pg_advisory_unlock(1), pg_stat_reset()`
	first := ClassifyFor(DriverPostgres, query)
	for i := 0; i < 20; i++ {
		got := ClassifyFor(DriverPostgres, query)
		if len(got.Reasons) != len(first.Reasons) {
			t.Fatalf("reasons changed: %v then %v", first.Reasons, got.Reasons)
		}
		for j := range got.Reasons {
			if got.Reasons[j] != first.Reasons[j] {
				t.Fatalf("reasons changed: %v then %v", first.Reasons, got.Reasons)
			}
		}
	}
	want := "takes an advisory lock, which stays on the connection after the rollback (pg_advisory_lock)"
	if first.Reasons[0] != want {
		t.Errorf("first reason = %q, want %q", first.Reasons[0], want)
	}
}
