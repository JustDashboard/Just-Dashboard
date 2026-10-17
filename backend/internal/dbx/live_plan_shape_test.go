package dbx

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The Explain tree on the Query page reads every engine's plan in the browser
// (frontend/src/components/database/query/plan.ts and plan-text.ts): the
// counts, loops and times it compares and the printed layouts it parses. A
// server whose plan changes shape has its plan shown as text instead, and its
// far-off estimates and step times stop being said, with nothing failing.
// These are the shapes those readers rely on, asked of each live engine
// through Explain as the route asks.

var (
	// MySQL's measured tree, which an older MySQL gives only as text: a step
	// a line, and what one loop of it averaged.
	mysqlPlanLine     = regexp.MustCompile(`^( *)-> \S`)
	mysqlPlanMeasured = regexp.MustCompile(`\(actual (time=\S+ )?rows=\S+ loops=\d+\)|\(never executed\)`)
	// DBMS_XPLAN's table of steps, and its conditions by step.
	oraclePlanHead      = regexp.MustCompile(`^\|\s*Id\s*\|\s*Operation\s*\|\s*Name\s*\|\s*Rows\s*\|.*\|\s*Cost \(%CPU\)\|`)
	oraclePlanStep      = regexp.MustCompile(`^\|[ *]\s*\d+ \| ( *)\S`)
	oraclePlanPredicate = regexp.MustCompile(`^\s+\d+ - (access|filter)\(`)
	// ClickHouse's steps, two spaces a level.
	clickhousePlanLine = regexp.MustCompile(`^( *)[A-Z][A-Za-z0-9]*( \(.*\))?$`)
)

func TestLivePlansAreTheShapesTheTreeReads(t *testing.T) {
	type planEngine struct {
		name   string
		driver Driver
		env    string
	}
	engines := []planEngine{
		{"postgres", DriverPostgres, "JD_TEST_POSTGRES_DSN"},
		{"mariadb", DriverMySQL, "JD_TEST_MYSQL_DSN"},
		{"mysql8", DriverMySQL, "JD_TEST_MYSQL8_DSN"},
		{"sqlserver", DriverMSSQL, "JD_TEST_MSSQL_DSN"},
		{"oracle", DriverOracle, "JD_TEST_ORACLE_DSN"},
		{"clickhouse", DriverClickHouse, "JD_TEST_CLICKHOUSE_DSN"},
		{"sqlite", DriverSQLite, ""},
	}
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) {
			var db *sql.DB
			if e.driver == DriverSQLite {
				var err error
				if db, err = sql.Open("sqlite", filepath.Join(t.TempDir(), "plan.db")); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { db.Close() })
			} else {
				if os.Getenv(e.env) == "" {
					t.Skipf("set %s to run this", e.env)
				}
				db = liveSQL(t, e.driver, e.env, "")
			}
			seedPlanTables(t, db, e.driver)
			ctx := context.Background()
			explain := func(query string, opts ExplainOptions) *Explanation {
				t.Helper()
				st, err := ExplainTarget(e.driver, query)
				if err != nil {
					t.Fatal(err)
				}
				plan, err := Explain(ctx, db, e.driver, st, opts)
				if err != nil {
					t.Fatalf("Explain(%+v) of %s: %v", opts, query, err)
				}
				return plan
			}
			join := `SELECT o.id, c.name FROM jdt_explain_view_o o JOIN jdt_explain_view_c c ON c.id = o.customer_id WHERE o.a = 1 ORDER BY o.id`
			limited := map[Driver]string{
				DriverOracle: `SELECT id FROM jdt_explain_view_o WHERE a < 50 FETCH FIRST 5 ROWS ONLY`,
				DriverMSSQL:  `SELECT TOP 5 id FROM jdt_explain_view_o WHERE a < 50`,
			}[e.driver]
			if limited == "" {
				limited = `SELECT id FROM jdt_explain_view_o WHERE a < 50 LIMIT 5`
			}

			switch e.name {
			case "postgres":
				measured := explain(join, ExplainOptions{Analyze: true, Format: ExplainJSON})
				root := pgPlanRoot(t, measured.Plan)
				hasKeys(t, "a measured PostgreSQL step", root,
					"Node Type", "Total Cost", "Plan Rows", "Actual Rows", "Actual Loops", "Actual Total Time", "Plans")
				if limit := pgPlanRoot(t, explain(limited, ExplainOptions{Analyze: true, Format: ExplainJSON}).Plan); limit["Node Type"] != "Limit" {
					t.Errorf("a LIMIT's plan opens on %v, not a Limit step", limit["Node Type"])
				}
			case "mysql8":
				measured := explain(join, ExplainOptions{Analyze: true, Format: ExplainJSON})
				root, _ := measured.Plan.(map[string]any)
				hasKeys(t, "a measured MySQL step", root,
					"operation", "estimated_rows", "actual_rows", "actual_loops", "actual_last_row_ms", "inputs")
				limit, _ := explain(limited, ExplainOptions{Analyze: true, Format: ExplainJSON}).Plan.(map[string]any)
				if limit["access_type"] != "limit" {
					t.Errorf("a LIMIT's measured plan opens on access_type %v, not limit", limit["access_type"])
				}
				// The text an older MySQL answers with when the JSON is refused.
				for _, line := range planLines(explain(join, ExplainOptions{Analyze: true}).Result) {
					m := mysqlPlanLine.FindStringSubmatch(line)
					if m == nil || len(m[1])%4 != 0 || !mysqlPlanMeasured.MatchString(line) {
						t.Errorf("a line of MySQL's measured tree the tree does not read: %q", line)
					}
				}
			case "mariadb":
				measured := explain(join, ExplainOptions{Analyze: true, Format: ExplainJSON})
				doc, _ := measured.Plan.(map[string]any)
				block, _ := doc["query_block"].(map[string]any)
				hasKeys(t, "a measured MariaDB block", block, "r_loops", "r_total_time_ms")
				tables := mariadbTables(block)
				if len(tables) == 0 {
					t.Fatalf("no table in MariaDB's measured plan: %v", doc)
				}
				for _, table := range tables {
					hasKeys(t, "a measured MariaDB table", table,
						"table_name", "rows", "filtered", "r_rows", "r_filtered", "r_loops", "r_table_time_ms")
				}
			case "sqlserver":
				hasColumns(t, explain(join, ExplainOptions{}).Result,
					"NodeId", "Parent", "PhysicalOp", "Argument", "EstimateRows", "TotalSubtreeCost", "Type")
			case "sqlite":
				hasColumns(t, explain(join, ExplainOptions{}).Result, "id", "parent", "detail")
			case "oracle":
				lines := planLines(explain(join, ExplainOptions{}).Result)
				head, steps, predicates := -1, 0, 0
				for i, line := range lines {
					switch {
					case oraclePlanHead.MatchString(line):
						head = i
					case head >= 0 && oraclePlanStep.MatchString(line):
						steps++
					case oraclePlanPredicate.MatchString(line):
						predicates++
					}
				}
				if head < 0 || steps < 3 || predicates == 0 {
					t.Errorf("Oracle's plan is not the table the tree reads (head at %d, %d steps, %d conditions):\n%s",
						head, steps, predicates, strings.Join(lines, "\n"))
				}
			case "clickhouse":
				lines := planLines(explain(join, ExplainOptions{}).Result)
				for _, line := range lines {
					m := clickhousePlanLine.FindStringSubmatch(line)
					if m == nil || len(m[1])%2 != 0 {
						t.Errorf("a line of ClickHouse's plan the tree does not read: %q", line)
					}
				}
				if len(lines) == 0 || strings.HasPrefix(lines[0], " ") {
					t.Errorf("ClickHouse's plan does not open on its last step: %q", lines)
				}
			}
		})
	}
}

// seedPlanTables makes a join with something to plan: customers, and orders
// whose a column picks out one in a hundred.
func seedPlanTables(t *testing.T, db *sql.DB, driver Driver) {
	t.Helper()
	dropAfter(t, db, "jdt_explain_view_o", "jdt_explain_view_c")
	integer, text, key, engine := "INT", "VARCHAR(40)", " PRIMARY KEY", ""
	switch driver {
	case DriverOracle:
		integer, text = "NUMBER(10)", "VARCHAR2(40)"
	case DriverClickHouse:
		integer, text, key, engine = "Int32", "String", "", " ENGINE = MergeTree ORDER BY id"
	}
	mustExec(t, db,
		"CREATE TABLE jdt_explain_view_c (id "+integer+key+", name "+text+")"+engine,
		"CREATE TABLE jdt_explain_view_o (id "+integer+key+", customer_id "+integer+", a "+integer+")"+engine)
	switch driver {
	case DriverPostgres:
		mustExec(t, db,
			"INSERT INTO jdt_explain_view_c SELECT g, 'customer ' || g FROM generate_series(1, 50) g",
			"INSERT INTO jdt_explain_view_o SELECT g, g % 50 + 1, g % 100 FROM generate_series(1, 500) g",
			"ANALYZE jdt_explain_view_c", "ANALYZE jdt_explain_view_o")
	case DriverMySQL, DriverSQLite:
		// Five hundred is inside both servers' default bound on a recursive query.
		mustExec(t, db,
			"INSERT INTO jdt_explain_view_c WITH RECURSIVE s(g) AS (SELECT 1 UNION ALL SELECT g + 1 FROM s WHERE g < 50) SELECT g, 'customer' FROM s",
			"INSERT INTO jdt_explain_view_o WITH RECURSIVE s(g) AS (SELECT 1 UNION ALL SELECT g + 1 FROM s WHERE g < 500) SELECT g, g % 50 + 1, g % 100 FROM s")
	case DriverMSSQL:
		mustExec(t, db,
			"INSERT INTO jdt_explain_view_c SELECT n, 'customer' FROM (SELECT TOP 50 ROW_NUMBER() OVER (ORDER BY (SELECT NULL)) n FROM sys.all_objects) s",
			"INSERT INTO jdt_explain_view_o SELECT n, n % 50 + 1, n % 100 FROM (SELECT TOP 500 ROW_NUMBER() OVER (ORDER BY (SELECT NULL)) n FROM sys.all_objects a CROSS JOIN sys.all_objects b) s")
	case DriverOracle:
		mustExec(t, db,
			"INSERT INTO jdt_explain_view_c SELECT LEVEL, 'customer ' || LEVEL FROM dual CONNECT BY LEVEL <= 50",
			"INSERT INTO jdt_explain_view_o SELECT LEVEL, MOD(LEVEL, 50) + 1, MOD(LEVEL, 100) FROM dual CONNECT BY LEVEL <= 500")
	case DriverClickHouse:
		mustExec(t, db,
			"INSERT INTO jdt_explain_view_c SELECT number + 1, concat('customer ', toString(number + 1)) FROM numbers(50)",
			"INSERT INTO jdt_explain_view_o SELECT number + 1, (number + 1) % 50 + 1, (number + 1) % 100 FROM numbers(500)")
	}
}

func pgPlanRoot(t *testing.T, plan any) map[string]any {
	t.Helper()
	doc, _ := plan.([]any)
	if len(doc) == 0 {
		t.Fatalf("PostgreSQL's plan is not an array of one: %v", plan)
	}
	first, _ := doc[0].(map[string]any)
	root, _ := first["Plan"].(map[string]any)
	if root == nil {
		t.Fatalf("PostgreSQL's plan has no Plan: %v", plan)
	}
	return root
}

// mariadbTables are the tables of a measured MariaDB block, wherever they sit.
func mariadbTables(node any) []map[string]any {
	var out []map[string]any
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			if table, ok := child.(map[string]any); ok && key == "table" {
				out = append(out, table)
			}
			out = append(out, mariadbTables(child)...)
		}
	case []any:
		for _, child := range v {
			out = append(out, mariadbTables(child)...)
		}
	}
	return out
}

func hasKeys(t *testing.T, what string, doc map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := doc[key]; !ok {
			t.Errorf("%s has no %q: %v", what, key, doc)
		}
	}
}

func hasColumns(t *testing.T, res *QueryResult, columns ...string) {
	t.Helper()
	have := map[string]bool{}
	for _, column := range res.Columns {
		have[strings.ToLower(column)] = true
	}
	for _, column := range columns {
		if !have[strings.ToLower(column)] {
			t.Errorf("the plan has no %s column: %v", column, res.Columns)
		}
	}
}

// planLines is a plan printed as text, a line apiece, as the page joins it.
func planLines(res *QueryResult) []string {
	var lines []string
	for _, row := range res.Rows {
		cell, _ := row[0].(string)
		for _, line := range strings.Split(cell, "\n") {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
		}
	}
	return lines
}
