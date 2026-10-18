package dbx

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
)

// Seed data on real servers. Each engine gets the same small shop — a key the
// server numbers, a unique email, a CHECK's list and its bounds, a table that
// points at itself, a join table keyed by two foreign keys, a boolean in the
// engine's own spelling — and is seeded twice with the same seed. Every row
// asked for has to go in, both times, with every foreign key pointing at a
// row that is there and every unique column still unique: the second seed is
// the one that finds the first's rows in the way.

type seedLiveSchema struct {
	tables   []string
	teardown []string
}

func seedLiveShop(e workbenchEngine) seedLiveSchema {
	boolean, text, stamp, uuid := "BOOLEAN", "TEXT", "TIMESTAMP", "UUID"
	switch e.driver {
	case DriverMySQL:
		stamp, uuid = "DATETIME", "CHAR(36)"
	case DriverMSSQL:
		boolean, text, stamp, uuid = "BIT", "NVARCHAR(MAX)", "DATETIME2", "UNIQUEIDENTIFIER"
	case DriverOracle:
		boolean, text, stamp, uuid = "NUMBER(1)", "CLOB", "TIMESTAMP", "RAW(16)"
	}
	id := e.identity()
	ref := e.bigint()
	out := seedLiveSchema{
		teardown: []string{
			"DROP TABLE jdt_seed_book_tags", "DROP TABLE jdt_seed_books", "DROP TABLE jdt_seed_tags",
			"DROP TABLE jdt_seed_authors", "DROP TABLE jdt_seed_categories",
		},
		tables: []string{
			`CREATE TABLE jdt_seed_authors (` + id + `,
				email VARCHAR(60) NOT NULL UNIQUE,
				full_name VARCHAR(80) NOT NULL,
				born DATE,
				active ` + boolean + ` NOT NULL,
				rating NUMERIC(3,1) CHECK (rating >= 0 AND rating <= 10),
				joined_at ` + stamp + ` NOT NULL,
				bio ` + text + `
			)`,
			// Keys at the end of the table, not beside their columns: MySQL
			// reads a REFERENCES written beside a column and makes nothing of it.
			`CREATE TABLE jdt_seed_categories (` + id + `,
				name VARCHAR(40) NOT NULL,
				parent_id ` + ref + `,
				FOREIGN KEY (parent_id) REFERENCES jdt_seed_categories(id)
			)`,
			`CREATE TABLE jdt_seed_books (` + id + `,
				author_id ` + ref + ` NOT NULL,
				category_id ` + ref + `,
				title VARCHAR(120) NOT NULL,
				isbn VARCHAR(20) NOT NULL UNIQUE,
				price NUMERIC(8,2) NOT NULL CHECK (price > 0),
				status VARCHAR(10) NOT NULL CHECK (status IN ('draft', 'live', 'retired')),
				external_id ` + uuid + `,
				published_at ` + stamp + `,
				FOREIGN KEY (author_id) REFERENCES jdt_seed_authors(id),
				FOREIGN KEY (category_id) REFERENCES jdt_seed_categories(id)
			)`,
			`CREATE TABLE jdt_seed_tags (` + id + `, name VARCHAR(30) NOT NULL UNIQUE)`,
			`CREATE TABLE jdt_seed_book_tags (
				book_id ` + ref + ` NOT NULL,
				tag_id ` + ref + ` NOT NULL,
				PRIMARY KEY (book_id, tag_id),
				FOREIGN KEY (book_id) REFERENCES jdt_seed_books(id),
				FOREIGN KEY (tag_id) REFERENCES jdt_seed_tags(id)
			)`,
		},
	}
	return out
}

func TestLiveSeedFillsAShopTwice(t *testing.T) {
	for _, e := range workbenchEngines() {
		t.Run(e.name, func(t *testing.T) {
			db, schema := openWorkbench(t, e)
			ctx := context.Background()
			shop := seedLiveShop(e)
			drop := func() {
				for _, stmt := range shop.teardown {
					_, _ = db.Exec(stmt)
				}
			}
			drop()
			t.Cleanup(drop)
			for _, stmt := range shop.tables {
				if _, err := db.Exec(stmt); err != nil {
					t.Fatalf("%v\n%s", err, stmt)
				}
			}
			names := []string{"jdt_seed_book_tags", "jdt_seed_books", "jdt_seed_tags", "jdt_seed_authors", "jdt_seed_categories"}
			req := SeedRequest{Seed: 2026, Schema: schema}
			for _, name := range names {
				req.Tables = append(req.Tables, SeedTableRequest{Name: e.ident(name), Rows: 40})
			}

			plan, err := PlanSeed(ctx, db, e.driver, req)
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			for _, p := range plan.Tables {
				if p.Problem != "" || p.Rows != 40 {
					t.Fatalf("%s: rows %d, problem %q, notes %v", p.Name, p.Rows, p.Problem, p.Notes)
				}
			}
			books := planTable(t, plan, e.ident("jdt_seed_books"))
			for column, want := range map[string]string{
				"id": seedAuto, "author_id": seedReference, "title": "title", "isbn": "code",
				"price": "price", "status": "choice", "external_id": "uuid", "published_at": "pastMoment",
			} {
				if c, _ := planColumn(t, books, e.ident(column)); c.Generator != want {
					t.Errorf("books.%s is filled by %q, want %q", column, c.Generator, want)
				}
			}
			authors := planTable(t, plan, e.ident("jdt_seed_authors"))
			if c, _ := planColumn(t, authors, e.ident("active")); c.Generator != "boolean" {
				t.Errorf("authors.active (%s) is filled by %q", c.Type, c.Generator)
			}

			for round := 1; round <= 2; round++ {
				report, err := Seed(ctx, db, e.driver, req)
				if err != nil {
					t.Fatalf("seed %d: %v", round, err)
				}
				for _, table := range report.Tables {
					if table.Problem != "" || table.Inserted != 40 || table.Skipped != 0 {
						t.Errorf("seed %d, %s: inserted %d, skipped %d, problem %q, errors %v",
							round, table.Name, table.Inserted, table.Skipped, table.Problem, table.Errors)
					}
				}
			}
			count := func(query string) int {
				t.Helper()
				var n int
				if err := db.QueryRow(query).Scan(&n); err != nil {
					t.Fatalf("%v\n%s", err, query)
				}
				return n
			}
			for query, want := range map[string]int{
				`SELECT COUNT(*) FROM jdt_seed_authors`:                                                                                         80,
				`SELECT COUNT(DISTINCT email) FROM jdt_seed_authors`:                                                                            80,
				`SELECT COUNT(DISTINCT isbn) FROM jdt_seed_books`:                                                                               80,
				`SELECT COUNT(*) FROM jdt_seed_books b JOIN jdt_seed_authors a ON a.id = b.author_id`:                                           80,
				`SELECT COUNT(*) FROM jdt_seed_book_tags bt JOIN jdt_seed_books b ON b.id = bt.book_id`:                                         80,
				`SELECT COUNT(*) FROM jdt_seed_book_tags bt JOIN jdt_seed_tags g ON g.id = bt.tag_id`:                                           80,
				`SELECT COUNT(*) FROM jdt_seed_books WHERE status NOT IN ('draft', 'live', 'retired')`:                                          0,
				`SELECT COUNT(*) FROM jdt_seed_authors WHERE rating < 0 OR rating > 10`:                                                         0,
				`SELECT COUNT(*) FROM jdt_seed_books WHERE category_id IS NOT NULL AND category_id NOT IN (SELECT id FROM jdt_seed_categories)`: 0,
			} {
				if got := count(query); got != want {
					t.Errorf("%s = %d, want %d", query, got, want)
				}
			}
			if count(`SELECT COUNT(*) FROM jdt_seed_categories WHERE parent_id IS NOT NULL`) == 0 {
				t.Error("no category points at another")
			}
			// The first round's books point at the first round's authors: the
			// new rows of a parent are the ones a child is given.
			if n := count(`SELECT COUNT(*) FROM jdt_seed_books WHERE id <= (SELECT MIN(id) + 39 FROM jdt_seed_books)
				AND author_id > (SELECT MIN(id) + 39 FROM jdt_seed_authors)`); n != 0 {
				t.Errorf("%d books of the first seed point at authors of the second", n)
			}
		})
	}
}

// What the preview shows is what is written, on an engine with every kind
// of value: an enum, JSON, an address, a moment with a zone, an array left
// empty, and a column no generator writes and nothing can stand in for.
func TestLiveSeedPostgresPreviewIsWhatIsWritten(t *testing.T) {
	e := workbenchEngines()[0]
	db, _ := openWorkbench(t, e)
	ctx := context.Background()
	stmts := []string{
		`DROP TABLE IF EXISTS jdt_seed_pg_events`, `DROP TABLE IF EXISTS jdt_seed_pg_places`,
		`DROP TYPE IF EXISTS jdt_seed_mood`,
	}
	clean := func() {
		for _, s := range stmts {
			_, _ = db.Exec(s)
		}
	}
	clean()
	t.Cleanup(clean)
	for _, s := range []string{
		`CREATE TYPE jdt_seed_mood AS ENUM ('calm', 'busy', 'closed')`,
		`CREATE TABLE jdt_seed_pg_events (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			mood jdt_seed_mood NOT NULL,
			payload jsonb NOT NULL,
			client_ip inet,
			happened_at timestamptz NOT NULL,
			tags text[],
			score integer NOT NULL CHECK (score BETWEEN 3 AND 7),
			kind varchar(10) NOT NULL DEFAULT 'event'
		)`,
		`CREATE TABLE jdt_seed_pg_places (id serial PRIMARY KEY, spot point NOT NULL)`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
	req := SeedRequest{Seed: 9, Schema: "public", Tables: []SeedTableRequest{
		{Name: "jdt_seed_pg_events", Rows: 25}, {Name: "jdt_seed_pg_places", Rows: 5},
	}}
	plan, err := PlanSeed(ctx, db, DriverPostgres, req)
	if err != nil {
		t.Fatal(err)
	}
	if p := planTable(t, plan, "jdt_seed_pg_places").Problem; !strings.Contains(p, "spot is point") {
		t.Errorf("a point no generator writes: %q", p)
	}
	events := planTable(t, plan, "jdt_seed_pg_events")
	for column, want := range map[string]string{
		"id": "uuid", "mood": "choice", "payload": "json", "client_ip": "ipAddress",
		"happened_at": "pastMoment", "tags": seedNull, "score": "integer", "kind": seedDefault,
	} {
		if c, _ := planColumn(t, events, column); c.Generator != want {
			t.Errorf("events.%s is filled by %q, want %q", column, c.Generator, want)
		}
	}
	report, err := Seed(ctx, db, DriverPostgres, req)
	if err != nil {
		t.Fatal(err)
	}
	if report.Inserted != 25 || report.Tables[0].Skipped != 0 {
		t.Fatalf("report %+v", report)
	}
	rows, err := db.Query(`SELECT id::text, mood::text, client_ip::text, score, kind FROM jdt_seed_pg_events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	written := map[string]string{}
	for rows.Next() {
		var id, mood, kind string
		var ip sql.NullString
		var score int
		if err := rows.Scan(&id, &mood, &ip, &score, &kind); err != nil {
			t.Fatal(err)
		}
		if score < 3 || score > 7 || kind != "event" {
			t.Errorf("score %d kind %q", score, kind)
		}
		written[id] = mood + " " + strings.TrimSuffix(ip.String, "/32")
	}
	_, idAt := planColumn(t, events, "id")
	_, moodAt := planColumn(t, events, "mood")
	_, ipAt := planColumn(t, events, "client_ip")
	for _, row := range events.Preview {
		want := *row[moodAt] + " " + *row[ipAt]
		if got, ok := written[*row[idAt]]; !ok || got != want {
			t.Errorf("previewed %s as %q, written as %q", *row[idAt], want, got)
		}
	}
}

func TestLiveSeedClickHouse(t *testing.T) {
	dsn := os.Getenv("JD_TEST_CLICKHOUSE_DSN")
	if dsn == "" {
		t.Skip("set JD_TEST_CLICKHOUSE_DSN to run this")
	}
	db := liveSQL(t, DriverClickHouse, "JD_TEST_CLICKHOUSE_DSN", dsn)
	info, err := ParseDSN(DriverClickHouse, dsn)
	if err != nil {
		t.Fatal(err)
	}
	database := info.Database
	if database == "" {
		database = "default"
	}
	ctx := context.Background()
	_, _ = db.Exec(`DROP TABLE IF EXISTS jdt_seed_ch_visits`)
	t.Cleanup(func() { _, _ = db.Exec(`DROP TABLE IF EXISTS jdt_seed_ch_visits`) })
	if _, err := db.Exec(`CREATE TABLE jdt_seed_ch_visits (
		id UInt64,
		email String,
		country LowCardinality(String),
		amount Decimal(10, 2),
		ok Bool,
		kind Enum8('view' = 1, 'click' = 2),
		visitor UUID,
		visited_at DateTime,
		visited_on Date,
		note Nullable(String),
		day Date MATERIALIZED toDate(visited_at)
	) ENGINE = MergeTree ORDER BY id`); err != nil {
		t.Fatal(err)
	}
	req := SeedRequest{Seed: 4, Schema: database, Tables: []SeedTableRequest{{Name: "jdt_seed_ch_visits", Rows: 300}}}
	plan, err := PlanSeed(ctx, db, DriverClickHouse, req)
	if err != nil {
		t.Fatal(err)
	}
	visits := planTable(t, plan, "jdt_seed_ch_visits")
	if plan.Atomic || visits.Problem != "" {
		t.Fatalf("plan %+v", plan)
	}
	if c, _ := planColumn(t, visits, "day"); c.Generator != seedComputed {
		t.Errorf("a MATERIALIZED column is filled by %q", c.Generator)
	}
	report, err := Seed(ctx, db, DriverClickHouse, req)
	if err != nil {
		t.Fatal(err)
	}
	if report.Inserted != 300 {
		t.Fatalf("report %+v", report.Tables)
	}
	var n, emails int
	if err := db.QueryRow(`SELECT count(), uniqExact(email) FROM jdt_seed_ch_visits`).Scan(&n, &emails); err != nil {
		t.Fatal(err)
	}
	if n != 300 || emails < 100 {
		t.Errorf("%d rows, %d emails", n, emails)
	}
}

// A row the table refuses for a reason the seed could not see — here a CHECK
// on an expression — is left out and counted, the rest of the seed carries
// on, and a table that had to point at those rows says why it was given none.
func TestLiveSeedLeavesOutTheRowsATableRefuses(t *testing.T) {
	for _, e := range workbenchEngines() {
		t.Run(e.name, func(t *testing.T) {
			db, schema := openWorkbench(t, e)
			ctx := context.Background()
			length := "LENGTH"
			if e.driver == DriverMSSQL {
				length = "LEN"
			}
			drop := func() {
				_, _ = db.Exec(`DROP TABLE jdt_seed_ref_child`)
				_, _ = db.Exec(`DROP TABLE jdt_seed_ref_parent`)
				_, _ = db.Exec(`DROP TABLE jdt_seed_ref_other`)
			}
			drop()
			t.Cleanup(drop)
			for _, stmt := range []string{
				`CREATE TABLE jdt_seed_ref_parent (` + e.identity() + `, title VARCHAR(80) NOT NULL CHECK (` + length + `(title) > 70))`,
				`CREATE TABLE jdt_seed_ref_child (` + e.identity() + `, parent_id ` + e.bigint() + ` NOT NULL,
					FOREIGN KEY (parent_id) REFERENCES jdt_seed_ref_parent(id))`,
				`CREATE TABLE jdt_seed_ref_other (` + e.identity() + `, label VARCHAR(30) NOT NULL)`,
			} {
				if _, err := db.Exec(stmt); err != nil {
					t.Fatalf("%v\n%s", err, stmt)
				}
			}
			report, err := Seed(ctx, db, e.driver, SeedRequest{Seed: 3, Schema: schema, Tables: []SeedTableRequest{
				{Name: e.ident("jdt_seed_ref_child"), Rows: 5},
				{Name: e.ident("jdt_seed_ref_parent"), Rows: 40},
				{Name: e.ident("jdt_seed_ref_other"), Rows: 5},
			}})
			if err != nil {
				t.Fatal(err)
			}
			byName := map[string]SeedTableReport{}
			for _, r := range report.Tables {
				byName[strings.ToLower(r.Name)] = r
			}
			if p := byName["jdt_seed_ref_parent"]; p.Inserted != 0 || p.Skipped != 40 || len(p.Errors) == 0 {
				t.Errorf("parent %+v", p)
			}
			if c := byName["jdt_seed_ref_child"]; c.Inserted != 0 || !strings.Contains(c.Problem, "none of the") {
				t.Errorf("child %+v", c)
			}
			if o := byName["jdt_seed_ref_other"]; o.Inserted != 5 {
				t.Errorf("other %+v", o)
			}
			var n int
			if err := db.QueryRow(`SELECT COUNT(*) FROM jdt_seed_ref_other`).Scan(&n); err != nil || n != 5 {
				t.Errorf("%d rows of the table nothing refused were committed (%v)", n, err)
			}
		})
	}
}
