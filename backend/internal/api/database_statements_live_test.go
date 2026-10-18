package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// The statement recorder against a real PostgreSQL: two readings of a
// database of the test's own, the history route reading back what ran
// between them, and the text it keeps being the statement list's own.
func TestLiveStatementHistoryIsRecordedAndReadBack(t *testing.T) {
	dsn := os.Getenv("JD_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("JD_TEST_POSTGRES_DSN is not set")
	}
	ctx := t.Context()
	server, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	var super bool
	var preload sql.NullString
	if err := server.QueryRowContext(ctx, `SELECT rolsuper, current_setting('shared_preload_libraries', true)
	  FROM pg_roles WHERE rolname = current_user`).Scan(&super, &preload); err != nil {
		t.Skipf("PostgreSQL unreachable: %v", err)
	}
	if !super || !strings.Contains(preload.String, "pg_stat_statements") {
		t.Skipf("needs a superuser on a server that preloads pg_stat_statements")
	}
	const database = "jdt_query_insights_api"
	drop := func() { server.Exec(`DROP DATABASE IF EXISTS ` + database + ` WITH (FORCE)`) }
	drop()
	if _, err := server.ExecContext(ctx, `CREATE DATABASE `+database); err != nil {
		t.Fatal(err)
	}
	own := dbx.DSNForDatabase(dbx.DriverPostgres, dsn, database)
	db, err := sql.Open("pgx", own)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE EXTENSION pg_stat_statements`,
		`CREATE TABLE jdt_query_insights_t (id int PRIMARY KEY, v text)`,
		`INSERT INTO jdt_query_insights_t SELECT n, 'row ' || n FROM generate_series(1, 50) n`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	// Registered after the server's pools, so it runs once they are closed.
	t.Cleanup(drop)
	t.Cleanup(func() { db.Close() })
	s, router, id := liveAPIRouter(t, dbx.DriverPostgres, own)
	rec, err := scanDBConn(s.Store.DB.QueryRow(`SELECT `+dbConnColumns+` FROM db_connections WHERE id=?`, id).Scan)
	if err != nil {
		t.Fatal(err)
	}

	lookup := func(times int) {
		for i := range times {
			var v string
			if err := db.QueryRowContext(ctx, `SELECT v FROM jdt_query_insights_t WHERE id = $1`, i+1).Scan(&v); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Run once before the first reading: a statement new to the list has
	// its first interval left out.
	lookup(1)
	s.recordStatementSnapshot(ctx, rec)
	lookup(6)
	s.recordStatementSnapshot(ctx, rec)

	reply := do(t, router, http.MethodGet, pathf("/databases/%d/statements/history?hours=1", id), "")
	history := readJSON[struct {
		Intervals int `json:"intervals"`
		Reading   *struct {
			Supported bool `json:"supported"`
		} `json:"reading"`
		Statements []historyRow `json:"statements"`
	}](t, reply)
	if history.Intervals != 1 || history.Reading == nil || !history.Reading.Supported {
		t.Fatalf("history: %s", reply.Body.String())
	}
	var found *historyRow
	for i, row := range history.Statements {
		if strings.Contains(row.Query, "FROM jdt_query_insights_t WHERE id = $1") {
			found = &history.Statements[i]
		}
	}
	if found == nil || found.Calls != 6 || found.Rows != 6 || found.TotalMs <= 0 || len(found.Trend) == 0 {
		t.Fatalf("the lookup's history: %+v in %s", found, reply.Body.String())
	}

	// The text kept is the text the statement list shows.
	list := readJSON[struct {
		Statements []struct {
			ID    string `json:"id"`
			Query string `json:"query"`
		} `json:"statements"`
	}](t, do(t, router, http.MethodGet, pathf("/databases/%d/statements?limit=200", id), ""))
	for _, row := range list.Statements {
		if row.ID == found.ID && row.Query != found.Query {
			t.Errorf("the history keeps %q, the list shows %q", found.Query, row.Query)
		}
	}

	one := do(t, router, http.MethodGet, fmt.Sprintf("/databases/%d/statements/history/%s?hours=1", id, url.PathEscape(found.ID)), "")
	if one.Code != http.StatusOK || !strings.Contains(one.Body.String(), `"calls":6`) {
		t.Errorf("one statement's history: %d %s", one.Code, one.Body.String())
	}
}
