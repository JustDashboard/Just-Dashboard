package dbx

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplainRefusesExecutionAndMultipleStatements(t *testing.T) {
	for _, driver := range []Driver{DriverSQLite, DriverPostgres, DriverMySQL, DriverMSSQL, DriverOracle, DriverClickHouse} {
		d, _ := DialectFor(driver)
		for _, query := range []string{"SELECT 1; DELETE FROM t", "ANALYZE DELETE FROM t", "(ANALYZE) DELETE FROM t", "SELECT 1--1; DELETE FROM t", "SELECT $tag$; DELETE FROM t; $tag$", "SELECT 1 /* outer /* inner */; DELETE FROM t */", "SELECT 1 /*!; DELETE FROM t */", "SELECT 1 /*M!; DELETE FROM t */", "SELECT 1 /*m!; DELETE FROM t */"} {
			t.Run(string(driver)+"/"+query, func(t *testing.T) {
				if _, err := d.ExplainPlan(context.Background(), nil, query); err == nil {
					t.Fatal("unsafe statement was not refused before accessing the database")
				}
			})
		}
	}
}

func TestSQLiteExplainLeavesRowsUnchanged(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE t (id INTEGER); INSERT INTO t VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	d, _ := DialectFor(DriverSQLite)
	if _, err := d.ExplainPlan(context.Background(), db, "DELETE FROM t"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExplainPlan(context.Background(), db, "SELECT * FROM t; DELETE FROM t"); err == nil {
		t.Fatal("batch accepted")
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM t").Scan(&count); err != nil || count != 1 {
		t.Fatalf("explain mutated data: %d %v", count, err)
	}
}

func TestStatementClassificationKeepsStrongestRisk(t *testing.T) {
	if risk := Classify("SELECT 'semi;colon'"); risk.Level != "read" {
		t.Fatalf("quoted semicolon changed read-only classification: %+v", risk)
	}
	for _, query := range []string{"CREATE TABLE t(v); VACUUM", "INSERT INTO t VALUES (1); ATTACH DATABASE 'another.db' AS another", "SELECT 1--1; DELETE FROM t"} {
		if risk := Classify(query); !risk.Destructive {
			t.Fatalf("batch failed open: %q %+v", query, risk)
		}
	}
	if risk := Classify("DELETE FROM t; SELECT 'where'"); risk.Level != "critical" {
		t.Fatalf("later WHERE text weakened delete risk: %+v", risk)
	}
	for _, query := range []string{"SELECT 'semi;colon'", "SELECT 'it''s; data'; -- trailing comment", "/* leading */ SELECT 1;", "SELECT 1 -- standard comment\n"} {
		if _, err := SingleStatement(query); err != nil {
			t.Errorf("safe statement refused: %q: %v", query, err)
		}
	}
	for _, query := range []string{"", "-- empty", "SELECT 'unterminated", "SELECT 1; SELECT 2", "SELECT 1 /* unterminated"} {
		if _, err := SingleStatement(query); err == nil {
			t.Errorf("ambiguous/batch statement accepted: %q", query)
		}
	}
}

func TestRedisCursorMarshalsWithoutPrecisionLoss(t *testing.T) {
	raw, err := json.Marshal(RedisPage{Cursor: math.MaxUint64})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"cursor":"18446744073709551615"`) {
		t.Fatalf("cursor lost string contract: %s", raw)
	}
}
