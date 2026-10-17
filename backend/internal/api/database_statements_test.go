package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// The statement history as the store keeps it and the routes read it back,
// without an engine: the intervals are made here, as the recorder would make
// them from two readings (dbx/statements_history_test.go holds that half).

func statementHistoryServer(t *testing.T) (*Server, *dbConnRecord, *client) {
	t.Helper()
	s := testServer(t)
	id := saveOpsConnection(t, s, "shop", dbx.DriverPostgres, "postgres://app:private-password@127.0.0.1:1/shop")
	rec, err := scanDBConn(s.Store.DB.QueryRow(`SELECT `+dbConnColumns+` FROM db_connections WHERE id=?`, id).Scan)
	if err != nil {
		t.Fatal(err)
	}
	viewer := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "history-reader", auth.RoleReadOnly)}
	return s, rec, viewer
}

func statementRunOf(id string, calls int64, totalMs float64, rows, read int64, d dbx.Durations) dbx.StatementRun {
	return dbx.StatementRun{ID: id, StatementFigures: dbx.StatementFigures{
		Calls: calls, TotalMs: totalMs, Rows: rows, RowsRead: read, Durations: d,
	}}
}

// save keeps one interval ending at the given age, as the recorder does.
func saveInterval(t *testing.T, s *Server, rec *dbConnRecord, age time.Duration, total float64, reset bool,
	texts map[string]string, runs ...dbx.StatementRun) {
	t.Helper()
	to := time.Now().Add(-age)
	s.saveStatementInterval(context.Background(), rec, databaseMetricIdentity(rec),
		&dbx.StatementSnapshot{Texts: texts},
		&dbx.StatementsInterval{From: to.Add(-statementSnapshotEvery), To: to, Reset: reset, TotalMs: total, Statements: runs})
}

func rowsIn(t *testing.T, s *Server, table string) int {
	t.Helper()
	var n int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type historyAnswer struct {
	Supported       bool         `json:"supported"`
	Reason          string       `json:"reason"`
	Hours           int          `json:"hours"`
	BucketSeconds   int          `json:"bucketSeconds"`
	Intervals       int          `json:"intervals"`
	RecordedSeconds int64        `json:"recordedSeconds"`
	FirstAt         *time.Time   `json:"firstAt"`
	TotalMs         float64      `json:"totalMs"`
	RowsRead        bool         `json:"rowsRead"`
	Percentiles     bool         `json:"percentiles"`
	Resets          []time.Time  `json:"resets"`
	Sort            string       `json:"sort"`
	Buckets         []int64      `json:"buckets"`
	Statements      []historyRow `json:"statements"`
}

type historyRow struct {
	ID       string     `json:"id"`
	Query    string     `json:"query"`
	Calls    int64      `json:"calls"`
	TotalMs  float64    `json:"totalMs"`
	MeanMs   float64    `json:"meanMs"`
	Rows     int64      `json:"rows"`
	RowsRead *int64     `json:"rowsRead"`
	P50Ms    *float64   `json:"p50Ms"`
	P95Ms    *float64   `json:"p95Ms"`
	P99Ms    *float64   `json:"p99Ms"`
	Share    float64    `json:"share"`
	Trend    []*float64 `json:"trend"`
}

func readHistory(t *testing.T, c *client, rec *dbConnRecord, query string) historyAnswer {
	t.Helper()
	reply := c.do(http.MethodGet, fmt.Sprintf("/api/v1/databases/%d/statements/history%s", rec.conn.ID, query), "", nil)
	if reply.Code != http.StatusOK {
		t.Fatalf("history%s: %d %s", query, reply.Code, reply.Body.String())
	}
	var out historyAnswer
	if err := json.Unmarshal(reply.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// One interval keeps its whole and the fifty statements that took the most
// time in it; a statement the list has no text for is in the whole and kept
// nowhere else, and a connection retargeted while the read ran keeps nothing.
func TestStatementHistoryKeepsTheBusiestFiftyOfAnInterval(t *testing.T) {
	s, rec, _ := statementHistoryServer(t)
	texts := map[string]string{}
	runs := []dbx.StatementRun{statementRunOf("untold", 1, 999, 1, -1, nil)}
	for i := range 60 {
		id := fmt.Sprintf("q%02d", i)
		texts[id] = fmt.Sprintf("SELECT * FROM t%02d WHERE id = $1", i)
		runs = append(runs, statementRunOf(id, 1, float64(60-i), 1, -1, nil))
	}
	saveInterval(t, s, rec, time.Minute, 5000, false, texts, runs...)
	if n := rowsIn(t, s, "db_statement_intervals"); n != 1 {
		t.Fatalf("%d intervals", n)
	}
	if n := rowsIn(t, s, "db_statement_samples"); n != statementsKeptPerInterval {
		t.Errorf("%d samples kept, want %d", n, statementsKeptPerInterval)
	}
	var least float64
	s.Store.DB.QueryRow(`SELECT MIN(total_ms) FROM db_statement_samples`).Scan(&least)
	if least != 11 {
		t.Errorf("the quietest statement kept took %v ms, want the fiftieth busiest's 11", least)
	}
	var untold int
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_statement_shapes WHERE statement='untold'`).Scan(&untold)
	if untold != 0 {
		t.Error("a statement with no text in the list was kept")
	}

	// A shape is written again as the list shows it now, ran or not.
	texts["q59"] = "SELECT * FROM t59 WHERE id = ?"
	texts["q00"] = "SELECT * FROM t00 WHERE id = ?"
	saveInterval(t, s, rec, 0, 10, false, texts, statementRunOf("q01", 1, 1, 1, -1, nil))
	var q00 string
	s.Store.DB.QueryRow(`SELECT query FROM db_statement_shapes WHERE statement='q00'`).Scan(&q00)
	if q00 != "SELECT * FROM t00 WHERE id = ?" {
		t.Errorf("a listed statement's shape was left as %q", q00)
	}

	before := rowsIn(t, s, "db_statement_intervals")
	retargeted := *rec
	retargeted.dsnEnc = "another target"
	saveInterval(t, s, &retargeted, 0, 10, false, texts, statementRunOf("q01", 1, 1, 1, -1, nil))
	if rowsIn(t, s, "db_statement_intervals") != before {
		t.Error("an interval of a target the connection no longer has was kept")
	}
}

func TestStatementHistoryIsPrunedToItsRetention(t *testing.T) {
	s, rec, _ := statementHistoryServer(t)
	texts := map[string]string{"old": "SELECT ?", "kept": "SELECT 1"}
	saveInterval(t, s, rec, 8*24*time.Hour, 10, false, texts, statementRunOf("old", 1, 10, 1, -1, nil), statementRunOf("kept", 1, 1, 1, -1, nil))
	saveInterval(t, s, rec, time.Hour, 10, false, texts, statementRunOf("kept", 1, 10, 1, -1, nil))
	s.pruneStatementHistory(context.Background(), time.Now().Add(-statementHistoryRetention))
	if n := rowsIn(t, s, "db_statement_intervals"); n != 1 {
		t.Errorf("%d intervals after pruning", n)
	}
	if n := rowsIn(t, s, "db_statement_samples"); n != 1 {
		t.Errorf("%d samples after pruning", n)
	}
	var shapes []string
	rows, _ := s.Store.DB.Query(`SELECT statement FROM db_statement_shapes ORDER BY statement`)
	for rows.Next() {
		var id string
		rows.Scan(&id)
		shapes = append(shapes, id)
	}
	rows.Close()
	if strings.Join(shapes, ",") != "kept" {
		t.Errorf("shapes after pruning: %v", shapes)
	}
	// Forgetting the connection takes its history with it.
	s.Store.DB.Exec(`DELETE FROM db_connections WHERE id=?`, rec.conn.ID)
	for _, table := range []string{"db_statement_intervals", "db_statement_shapes", "db_statement_samples"} {
		if n := rowsIn(t, s, table); n != 0 {
			t.Errorf("%s kept %d rows of a forgotten connection", table, n)
		}
	}
}

// A window is read back summed: figures, share of all the time counted,
// percentiles from the merged durations, and a trend with a gap where
// nothing was recorded and a nought where the statement did not run.
func TestStatementHistoryReadsAWindowBack(t *testing.T) {
	s, rec, viewer := statementHistoryServer(t)
	texts := map[string]string{
		"orders": "SELECT * FROM orders WHERE customer_id = $1",
		"report": "SELECT count(*) FROM order_items",
	}
	saveInterval(t, s, rec, 50*time.Minute, 400, false, texts,
		statementRunOf("orders", 100, 200, 100, 1000, dbx.Durations{1: 90, 8: 10}),
		statementRunOf("report", 2, 150, 2, 50000, dbx.Durations{70: 2}))
	saveInterval(t, s, rec, 45*time.Minute, 100, false, texts, statementRunOf("orders", 100, 100, 100, 1000, dbx.Durations{1: 100}))
	// Counted from zero again; and a quiet reading nothing ran in.
	saveInterval(t, s, rec, 10*time.Minute, 60, true, texts, statementRunOf("orders", 20, 60, 20, 200, dbx.Durations{1: 17, 120: 3}))
	saveInterval(t, s, rec, 5*time.Minute, 0, false, texts)

	got := readHistory(t, viewer, rec, "?hours=1")
	if !got.Supported || got.Hours != 1 || got.BucketSeconds != 300 || got.Intervals != 4 ||
		got.RecordedSeconds != 1200 || got.TotalMs != 560 || !got.RowsRead || !got.Percentiles || len(got.Resets) != 1 {
		t.Fatalf("window: %+v", got)
	}
	if got.FirstAt == nil || time.Since(*got.FirstAt) < 54*time.Minute {
		t.Errorf("first recorded %v", got.FirstAt)
	}
	if len(got.Statements) != 2 || got.Statements[0].ID != "orders" || got.Sort != "total" {
		t.Fatalf("statements: %+v", got.Statements)
	}
	orders := got.Statements[0]
	if orders.Calls != 220 || orders.TotalMs != 360 || orders.Rows != 220 || *orders.RowsRead != 2200 ||
		orders.Query != texts["orders"] || orders.MeanMs < 1.63 || orders.MeanMs > 1.64 {
		t.Errorf("orders: %+v", orders)
	}
	if orders.Share < 0.64 || orders.Share > 0.65 {
		t.Errorf("orders' share %v of 560 ms", orders.Share)
	}
	// 220 calls: 207 under 1 ms, 10 under 8, 3 under 120.
	if *orders.P50Ms != 1 || *orders.P95Ms != 8 || *orders.P99Ms != 120 {
		t.Errorf("percentiles %v %v %v", *orders.P50Ms, *orders.P95Ms, *orders.P99Ms)
	}
	if len(orders.Trend) != len(got.Buckets) || len(got.Buckets) < 12 {
		t.Fatalf("trend %d points over %d buckets", len(orders.Trend), len(got.Buckets))
	}
	var drawn, gaps, zeros int
	for _, point := range orders.Trend {
		switch {
		case point == nil:
			gaps++
		case *point == 0:
			zeros++
		default:
			drawn++
		}
	}
	if drawn != 3 || zeros != 1 || gaps != len(orders.Trend)-4 {
		t.Errorf("trend: %d drawn, %d zero, %d gaps of %d", drawn, zeros, gaps, len(orders.Trend))
	}
	path := fmt.Sprintf("/api/v1/databases/%d/statements/history", rec.conn.ID)
	if reply := viewer.do(http.MethodGet, path+"?hours=1", "", nil); strings.Contains(reply.Body.String(), "private-password") {
		t.Error("the history carried the connection's credential")
	}

	// The orders, the most first; a statement without the figure after.
	for sort, first := range map[string]string{"mean": "report", "calls": "orders", "rows": "orders", "rowsRead": "report", "p95": "report"} {
		if got := readHistory(t, viewer, rec, "?hours=1&sort="+sort); got.Statements[0].ID != first || got.Sort != sort {
			t.Errorf("by %s: %s first", sort, got.Statements[0].ID)
		}
	}
	if got := readHistory(t, viewer, rec, "?hours=1&limit=1"); len(got.Statements) != 1 {
		t.Errorf("limit 1 listed %d", len(got.Statements))
	}
	// A day is read an hour to a point.
	if got := readHistory(t, viewer, rec, "?hours=24"); got.BucketSeconds != 3600 || got.Intervals != 4 {
		t.Errorf("a day: %+v", got)
	}

	for _, bad := range []string{"?hours=0", "?hours=169", "?hours=x", "?sort=max", "?sort=total%20desc"} {
		if reply := viewer.do(http.MethodGet, path+bad, "", nil); reply.Code != http.StatusBadRequest {
			t.Errorf("%s answered %d", bad, reply.Code)
		}
	}
	// A new target does not inherit the old one's history.
	s.Store.DB.Exec(`UPDATE db_connections SET dsn_enc='another target' WHERE id=?`, rec.conn.ID)
	if got := readHistory(t, viewer, rec, "?hours=1"); len(got.Statements) != 0 || got.Intervals != 0 {
		t.Errorf("a new target inherited the old one's history: %+v", got)
	}
	var audited int
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE path LIKE '%/statements/history%'`).Scan(&audited)
	if audited != 0 {
		t.Errorf("%d history reads were audited", audited)
	}
}

func TestStatementHistoryOfOneStatement(t *testing.T) {
	s, rec, viewer := statementHistoryServer(t)
	texts := map[string]string{"orders": "SELECT * FROM orders WHERE customer_id = $1"}
	saveInterval(t, s, rec, 20*time.Minute, 100, false, texts, statementRunOf("orders", 10, 50, 10, -1, nil))
	saveInterval(t, s, rec, 10*time.Minute, 100, false, texts)
	saveInterval(t, s, rec, 5*time.Minute, 100, false, texts, statementRunOf("orders", 30, 30, 30, -1, nil))
	reply := viewer.do(http.MethodGet, fmt.Sprintf("/api/v1/databases/%d/statements/history/orders?hours=1", rec.conn.ID), "", nil)
	if reply.Code != http.StatusOK {
		t.Fatalf("%d %s", reply.Code, reply.Body.String())
	}
	var got struct {
		TotalMs   float64    `json:"totalMs"`
		RowsRead  bool       `json:"rowsRead"`
		Statement historyRow `json:"statement"`
		Points    []struct {
			At       int64    `json:"at"`
			Recorded bool     `json:"recorded"`
			Calls    int64    `json:"calls"`
			TotalMs  float64  `json:"totalMs"`
			MeanMs   *float64 `json:"meanMs"`
			RowsRead *int64   `json:"rowsRead"`
			P95Ms    *float64 `json:"p95Ms"`
		} `json:"points"`
	}
	if err := json.Unmarshal(reply.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Statement.Calls != 40 || got.Statement.TotalMs != 80 || got.Statement.Share != 80.0/300 ||
		got.Statement.Query != texts["orders"] || got.Statement.RowsRead != nil || got.Statement.P95Ms != nil || got.RowsRead {
		t.Errorf("statement: %+v", got.Statement)
	}
	var recorded, ran int
	for i, point := range got.Points {
		if i > 0 && point.At <= got.Points[i-1].At {
			t.Fatalf("points out of order at %d", i)
		}
		if point.Recorded {
			recorded++
		}
		if point.Calls > 0 {
			ran++
			if point.MeanMs == nil || point.P95Ms != nil {
				t.Errorf("a point that ran: %+v", point)
			}
		}
	}
	if recorded != 3 || ran != 2 || got.Points[len(got.Points)-1].At > time.Now().UnixMilli() {
		t.Errorf("%d recorded, %d ran, of %d points", recorded, ran, len(got.Points))
	}
	if reply := viewer.do(http.MethodGet, fmt.Sprintf("/api/v1/databases/%d/statements/history/never?hours=1", rec.conn.ID), "", nil); reply.Code != http.StatusNotFound {
		t.Errorf("a statement never recorded answered %d", reply.Code)
	}
}

// Engines with no statement list have no history: SQLite says so, and a
// key-value store is not asked.
func TestStatementHistoryOnEnginesWithoutStatements(t *testing.T) {
	s := testServer(t)
	viewer := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "history-reader", auth.RoleReadOnly)}
	redis := saveOpsConnection(t, s, "cache", dbx.DriverRedis, "redis://127.0.0.1:1/0")
	if reply := viewer.do(http.MethodGet, fmt.Sprintf("/api/v1/databases/%d/statements/history", redis), "", nil); reply.Code != http.StatusBadRequest {
		t.Errorf("Redis answered %d %s", reply.Code, reply.Body.String())
	}
	if reply := viewer.do(http.MethodGet, "/api/v1/databases/999/statements/history", "", nil); reply.Code != http.StatusNotFound {
		t.Errorf("no such connection answered %d", reply.Code)
	}
}

// The recorder's memory between readings: a snapshot of another target or
// too old to difference against is not used, and a reset made from here is
// told to the next interval once.
func TestStatementRecorderBaselines(t *testing.T) {
	var h statementHistory
	snap := &dbx.StatementSnapshot{At: time.Now(), Supported: true}
	h.keep(1, "a", snap)
	if got, _ := h.before(1, "a"); got != snap {
		t.Error("the newest snapshot was not the one before")
	}
	if got, _ := h.before(1, "b"); got != nil {
		t.Error("another target's snapshot was differenced against")
	}
	h.keep(1, "a", &dbx.StatementSnapshot{At: time.Now().Add(-statementSnapshotStale - time.Minute)})
	if got, _ := h.before(1, "a"); got != nil {
		t.Error("a snapshot from before an outage was differenced against")
	}
	h.noteReset(1)
	if _, reset := h.before(1, "a"); !reset {
		t.Error("the reset made from here was not told")
	}
	if _, reset := h.before(1, "a"); reset {
		t.Error("the reset was told twice")
	}
}
