package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// The seed routes over HTTP. What a seed writes is tested in dbx; here it is
// what only a request reaches: which role may plan and which may write, that
// a protected connection is planned for and never written to, that the plan
// leaves no entry in the audit log and the write leaves one with counts and
// no values, and that a request the seed refuses is a 400 that says why.

func newSeedFixture(t *testing.T) *transferFixture {
	t.Helper()
	f := newTransferFixture(t)
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`CREATE TABLE authors (id INTEGER PRIMARY KEY, email TEXT NOT NULL UNIQUE, full_name TEXT NOT NULL)`,
		`CREATE TABLE books (
			id INTEGER PRIMARY KEY,
			title TEXT NOT NULL,
			author_id INTEGER NOT NULL REFERENCES authors(id),
			published_on DATE
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

const seedBody = `{"seed": 17, "tables": [{"name": "books", "rows": 6}, {"name": "authors", "rows": 3}]}`

func TestSeedPlanIsAReadAnyRoleMayAskFor(t *testing.T) {
	f := newSeedFixture(t)
	f.role = auth.RoleReadOnly
	rec := f.do(http.MethodPost, f.path("/seed/plan"), seedBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body.String())
	}
	var plan dbx.SeedPlan
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Seed != 17 || plan.Rows != 9 || len(plan.Tables) != 2 || plan.Tables[0].Name != "authors" {
		t.Fatalf("plan %+v", plan)
	}
	if books := plan.Tables[1]; len(books.After) != 1 || books.After[0] != "authors" || len(books.Preview) != 6 {
		t.Errorf("books %+v", books)
	}
	if got := f.audited("database.seed"); len(got) != 0 {
		t.Errorf("a plan was recorded: %v", got)
	}
	if n := f.count(`SELECT COUNT(*) FROM authors`); n != 0 {
		t.Errorf("a plan wrote %d rows", n)
	}

	// A seed is a write the read-only role may not make.
	if rec := f.do(http.MethodPost, f.path("/seed"), seedBody); rec.Code != http.StatusForbidden {
		t.Errorf("a read-only seed: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSeedWritesAndIsRecordedWithoutItsValues(t *testing.T) {
	f := newSeedFixture(t)
	rec := f.do(http.MethodPost, f.path("/seed"), seedBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("seed: %d %s", rec.Code, rec.Body.String())
	}
	var report dbx.SeedReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Inserted != 9 || report.Skipped != 0 || !report.Atomic {
		t.Fatalf("report %+v", report)
	}
	if n := f.count(`SELECT COUNT(*) FROM books b JOIN authors a ON a.id = b.author_id`); n != 6 {
		t.Errorf("%d books point at a seeded author", n)
	}
	audited := f.audited("database.seed")
	if len(audited) != 1 {
		t.Fatalf("recorded %d times", len(audited))
	}
	if !strings.Contains(audited[0], `"inserted":9`) || !strings.Contains(audited[0], `"books":6`) {
		t.Errorf("the entry says %s", audited[0])
	}
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var email, title string
	if err := db.QueryRow(`SELECT a.email, b.title FROM books b JOIN authors a ON a.id = b.author_id LIMIT 1`).Scan(&email, &title); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(audited[0], email) || strings.Contains(audited[0], title) {
		t.Errorf("a seeded value is in the audit log: %s", audited[0])
	}
}

func TestSeedOnAProtectedConnectionIsPlannedAndRefused(t *testing.T) {
	f := newSeedFixture(t)
	if _, err := f.s.Store.DB.Exec(`UPDATE db_connections SET read_only = 1 WHERE id = ?`, f.id); err != nil {
		t.Fatal(err)
	}
	if rec := f.do(http.MethodPost, f.path("/seed/plan"), seedBody); rec.Code != http.StatusOK {
		t.Errorf("plan on a protected connection: %d %s", rec.Code, rec.Body.String())
	}
	rec := f.do(http.MethodPost, f.path("/seed"), seedBody)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "protected") {
		t.Errorf("seed on a protected connection: %d %s", rec.Code, rec.Body.String())
	}
	if n := f.count(`SELECT COUNT(*) FROM authors`); n != 0 {
		t.Errorf("%d rows were written to a protected connection", n)
	}
}

func TestSeedRefusalsSayWhy(t *testing.T) {
	f := newSeedFixture(t)
	for body, want := range map[string]string{
		`{"tables": []}`: "at least one table",
		`{"tables": [{"name": "books", "rows": 20000}]}`:                                                 "between 1 and",
		`{"tables": [{"name": "nowhere", "rows": 2}]}`:                                                   "nowhere",
		`{"tables": [{"name": "authors", "rows": 2}, {"schema": "main", "name": "authors", "rows": 2}]}`: "named twice",
		`{"tables": [{"name": "authors", "rows": 2, "columns": {"email": "email"}}], "x": 1}`:            "unknown field",
	} {
		for _, route := range []string{"/seed/plan", "/seed"} {
			rec := f.do(http.MethodPost, f.path(route), body)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), want) {
				t.Errorf("%s %s: %d %s, want 400 saying %q", route, body, rec.Code, rec.Body.String(), want)
			}
		}
	}
}
