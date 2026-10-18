package dbx

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// openSeedDB is a SQLite database with the shapes a seed has to get right:
// a key the engine numbers, a unique email, a CHECK's list and bounds, a
// table that points at itself, a join table whose key is two foreign keys,
// and a one-to-one profile.
func openSeedDB(t *testing.T, extra ...string) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "seed.db")
	db, err := sql.Open("sqlite", sqliteDialect{}.NormaliseDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	stmts := append([]string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			email VARCHAR(40) NOT NULL UNIQUE,
			first_name TEXT NOT NULL,
			last_name TEXT,
			status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'invited', 'banned')),
			age INTEGER CHECK (age >= 18 AND age <= 30),
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE profiles (
			user_id INTEGER PRIMARY KEY REFERENCES users(id),
			bio TEXT,
			website VARCHAR(120)
		)`,
		`CREATE TABLE categories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name VARCHAR(60) NOT NULL,
			parent_id INTEGER REFERENCES categories(id)
		)`,
		`CREATE TABLE products (
			id INTEGER PRIMARY KEY,
			name VARCHAR(80) NOT NULL,
			sku VARCHAR(12) NOT NULL UNIQUE,
			price NUMERIC(8,2) NOT NULL CHECK (price > 0),
			category_id INTEGER NOT NULL REFERENCES categories(id),
			in_stock BOOLEAN NOT NULL DEFAULT 1
		)`,
		`CREATE TABLE orders (
			id INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id),
			placed_at DATETIME NOT NULL,
			total NUMERIC(10,2) NOT NULL
		)`,
		`CREATE TABLE order_items (
			order_id INTEGER NOT NULL REFERENCES orders(id),
			product_id INTEGER NOT NULL REFERENCES products(id),
			quantity INTEGER NOT NULL CHECK (quantity > 0),
			PRIMARY KEY (order_id, product_id)
		)`,
	}, extra...)
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
	return db
}

func seedTables(rows int, names ...string) []SeedTableRequest {
	out := make([]SeedTableRequest, len(names))
	for i, n := range names {
		out[i] = SeedTableRequest{Name: n, Rows: rows}
	}
	return out
}

func planTable(t *testing.T, plan *SeedPlan, name string) SeedTablePlan {
	t.Helper()
	for _, p := range plan.Tables {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no %s in the plan", name)
	return SeedTablePlan{}
}

func planColumn(t *testing.T, p SeedTablePlan, name string) (SeedColumnPlan, int) {
	t.Helper()
	for i, c := range p.Columns {
		if c.Name == name {
			return c, i
		}
	}
	t.Fatalf("no %s.%s in the plan", p.Name, name)
	return SeedColumnPlan{}, -1
}

func TestSeedPlanOrdersParentsFirstAndNamesEachColumnsFill(t *testing.T) {
	db := openSeedDB(t)
	plan, err := PlanSeed(context.Background(), db, DriverSQLite, SeedRequest{
		Seed:   42,
		Tables: seedTables(20, "order_items", "orders", "products", "categories", "users", "profiles"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, p := range plan.Tables {
		order = append(order, p.Name)
		if p.Problem != "" {
			t.Errorf("%s: %s", p.Name, p.Problem)
		}
	}
	at := map[string]int{}
	for i, name := range order {
		at[name] = i
	}
	for child, parents := range map[string][]string{
		"order_items": {"orders", "products"}, "orders": {"users"}, "products": {"categories"}, "profiles": {"users"},
	} {
		for _, parent := range parents {
			if at[parent] > at[child] {
				t.Errorf("%s is written before its parent %s: %v", child, parent, order)
			}
		}
	}
	if got := planTable(t, plan, "order_items").After; strings.Join(got, ",") != "orders,products" {
		t.Errorf("order_items is written after %v", got)
	}

	users := planTable(t, plan, "users")
	for column, want := range map[string]string{
		"id": seedAuto, "email": "email", "first_name": "firstName", "last_name": "lastName",
		"status": "choice", "age": "age", "created_at": "pastMoment",
	} {
		if c, _ := planColumn(t, users, column); c.Generator != want {
			t.Errorf("users.%s is filled by %q, want %q", column, c.Generator, want)
		}
	}
	if id, _ := planColumn(t, users, "id"); len(id.Choices) != 0 || id.Later != "numbered by the server" {
		t.Errorf("a key the engine numbers offers %v and stands as %q", id.Choices, id.Later)
	}
	if len(users.Preview) != seedPreviewRows {
		t.Fatalf("users previews %d rows", len(users.Preview))
	}
	_, statusAt := planColumn(t, users, "status")
	_, ageAt := planColumn(t, users, "age")
	_, emailAt := planColumn(t, users, "email")
	seen := map[string]bool{}
	for _, row := range users.Preview {
		if s := *row[statusAt]; s != "active" && s != "invited" && s != "banned" {
			t.Errorf("status %q is outside its CHECK", s)
		}
		if n, _ := strconv.Atoi(*row[ageAt]); n < 18 || n > 30 {
			t.Errorf("age %q is outside its CHECK", *row[ageAt])
		}
		email := *row[emailAt]
		if len(email) > 40 || !strings.Contains(email, "@example.") || seen[email] {
			t.Errorf("email %q is too long, not an example address, or repeated", email)
		}
		seen[email] = true
	}

	orders := planTable(t, plan, "orders")
	userID, _ := planColumn(t, orders, "user_id")
	if userID.Generator != seedReference || userID.Reference == nil || !userID.Reference.Seeded || userID.Later != "a new users row" {
		t.Errorf("orders.user_id: %+v", userID)
	}
	products := planTable(t, plan, "products")
	price, priceAt := planColumn(t, products, "price")
	if price.Generator != "price" {
		t.Errorf("products.price is %q", price.Generator)
	}
	for _, row := range products.Preview {
		if f, err := strconv.ParseFloat(*row[priceAt], 64); err != nil || f <= 0 || f >= 1000000 {
			t.Errorf("price %q does not fit NUMERIC(8,2) > 0", *row[priceAt])
		}
	}
	if name, _ := planColumn(t, products, "name"); name.Generator != "productName" {
		t.Errorf("products.name is %q", name.Generator)
	}
}

func TestSeedPlanIsTheSameForTheSameSeed(t *testing.T) {
	db := openSeedDB(t)
	req := SeedRequest{Seed: 7, Tables: seedTables(10, "users", "products", "categories")}
	a, err := PlanSeed(context.Background(), db, DriverSQLite, req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := PlanSeed(context.Background(), db, DriverSQLite, req)
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatal("the same seed planned two different sets of rows")
	}
	// A table's rows do not depend on what else is seeded with it.
	c, err := PlanSeed(context.Background(), db, DriverSQLite, SeedRequest{Seed: 7, Tables: seedTables(10, "users")})
	if err != nil {
		t.Fatal(err)
	}
	ju, _ := json.Marshal(planTable(t, a, "users").Preview)
	jc, _ := json.Marshal(planTable(t, c, "users").Preview)
	if string(ju) != string(jc) {
		t.Fatal("users' rows changed with the tables seeded beside it")
	}
	other, err := PlanSeed(context.Background(), db, DriverSQLite, SeedRequest{Seed: 8, Tables: seedTables(10, "users")})
	if err != nil {
		t.Fatal(err)
	}
	jo, _ := json.Marshal(planTable(t, other, "users").Preview)
	if string(jo) == string(jc) {
		t.Fatal("another seed planned the same rows")
	}
}

func TestSeedWritesEveryTableAndKeepsItsKeys(t *testing.T) {
	db := openSeedDB(t)
	ctx := context.Background()
	req := SeedRequest{Seed: 99, Tables: seedTables(30, "users", "profiles", "categories", "products", "orders", "order_items")}
	report, err := Seed(ctx, db, DriverSQLite, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range report.Tables {
		if table.Problem != "" || table.Skipped != 0 || table.Inserted != 30 {
			t.Errorf("%s: inserted %d, skipped %d, problem %q, errors %v", table.Name, table.Inserted, table.Skipped, table.Problem, table.Errors)
		}
	}
	if report.Inserted != 180 || !report.Atomic {
		t.Errorf("the seed wrote %d rows, atomic %v", report.Inserted, report.Atomic)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Error("a seeded row points at a row that is not there")
	}
	rows.Close()
	var linked int
	if err := db.QueryRow(`SELECT COUNT(DISTINCT parent_id) FROM categories WHERE parent_id IS NOT NULL`).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked == 0 {
		t.Error("no category points at another: a table's own earlier rows are what it points at")
	}

	// Again with the same seed: the unique values move on past the rows there.
	again, err := Seed(ctx, db, DriverSQLite, SeedRequest{Seed: 99, Tables: seedTables(30, "users", "products", "categories")})
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range again.Tables {
		if table.Inserted != 30 || table.Skipped != 0 {
			t.Errorf("a second seed of %s: inserted %d, skipped %d, errors %v", table.Name, table.Inserted, table.Skipped, table.Errors)
		}
	}
	var users, emails int
	if err := db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT email) FROM users`).Scan(&users, &emails); err != nil {
		t.Fatal(err)
	}
	if users != 60 || emails != 60 {
		t.Errorf("%d users with %d emails", users, emails)
	}
}

func TestSeedPreviewIsWhatIsWritten(t *testing.T) {
	db := openSeedDB(t)
	ctx := context.Background()
	req := SeedRequest{Seed: 5, Tables: seedTables(8, "users")}
	plan, err := PlanSeed(ctx, db, DriverSQLite, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Seed(ctx, db, DriverSQLite, req); err != nil {
		t.Fatal(err)
	}
	users := planTable(t, plan, "users")
	rows, err := db.Query(`SELECT email, first_name, status FROM users ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	_, emailAt := planColumn(t, users, "email")
	_, firstAt := planColumn(t, users, "first_name")
	_, statusAt := planColumn(t, users, "status")
	n := 0
	for rows.Next() {
		var email, first, status string
		if err := rows.Scan(&email, &first, &status); err != nil {
			t.Fatal(err)
		}
		want := users.Preview[n]
		if email != *want[emailAt] || first != *want[firstAt] || status != *want[statusAt] {
			t.Errorf("row %d was written as %s %s %s and previewed as %s %s %s",
				n, email, first, status, *want[emailAt], *want[firstAt], *want[statusAt])
		}
		n++
	}
	if n != 8 {
		t.Fatalf("%d rows written", n)
	}
}

func TestSeedLeavesOutWhatCannotBeWritten(t *testing.T) {
	db := openSeedDB(t,
		`CREATE TABLE a (id INTEGER PRIMARY KEY, b_id INTEGER NOT NULL REFERENCES b(id))`,
		`CREATE TABLE b (id INTEGER PRIMARY KEY, a_id INTEGER NOT NULL REFERENCES a(id))`,
		`CREATE TABLE c (id INTEGER PRIMARY KEY, d_id INTEGER REFERENCES d(id))`,
		`CREATE TABLE d (id INTEGER PRIMARY KEY, c_id INTEGER NOT NULL REFERENCES c(id))`,
	)
	plan, err := PlanSeed(context.Background(), db, DriverSQLite, SeedRequest{
		Seed: 1, Tables: seedTables(5, "orders", "a", "b", "c", "d"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if p := planTable(t, plan, "orders").Problem; !strings.Contains(p, "users has none; fill users too") {
		t.Errorf("orders with no users: %q", p)
	}
	if p := planTable(t, plan, "a").Problem; !strings.Contains(p, "point at each other") {
		t.Errorf("a cycle of required keys: %q", p)
	}
	// A cycle with a key that can be left empty is broken there: c is
	// written first and points at the d rows that were there before.
	c, d := planTable(t, plan, "c"), planTable(t, plan, "d")
	if c.Problem != "" || d.Problem != "" || len(d.After) != 1 || d.After[0] != "c" || len(c.Notes) == 0 {
		t.Errorf("c %+v\nd %+v", c, d)
	}
	if plan.Rows != 10 {
		t.Errorf("the plan writes %d rows", plan.Rows)
	}
}

func TestSeedRefusesWhatWasNotAskedProperly(t *testing.T) {
	db := openSeedDB(t, `CREATE VIEW recent AS SELECT * FROM users`)
	ctx := context.Background()
	for _, tc := range []struct {
		req  SeedRequest
		want string
	}{
		{SeedRequest{}, "at least one table"},
		{SeedRequest{Tables: []SeedTableRequest{{Name: "users", Rows: 0}}}, "between 1 and"},
		{SeedRequest{Tables: []SeedTableRequest{{Name: "users", Rows: MaxSeedRows + 1}}}, "between 1 and"},
		{SeedRequest{Tables: []SeedTableRequest{{Name: "nope", Rows: 1}}}, "nope"},
		{SeedRequest{Tables: []SeedTableRequest{{Name: "recent", Rows: 1}}}, "view"},
		{SeedRequest{Tables: []SeedTableRequest{{Name: "users", Rows: 6}, {Schema: "main", Name: "users", Rows: 6}}}, "named twice"},
	} {
		_, err := PlanSeed(ctx, db, DriverSQLite, tc.req)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v, want %q", tc.req, err, tc.want)
		}
	}
}

// A choice the table no longer takes — its column dropped, retyped, given to
// the server — is set aside with a warning, and the rest of the seed stands.
func TestSeedSetsAsideAChoiceTheTableNoLongerTakes(t *testing.T) {
	db := openSeedDB(t)
	plan, err := PlanSeed(context.Background(), db, DriverSQLite, SeedRequest{Seed: 2, Tables: []SeedTableRequest{
		{Name: "users", Rows: 3, Columns: map[string]string{
			"email": "integer", "id": "integer", "last_name": "null", "gone": "email", "first_name": "nope",
		}},
		{Name: "categories", Rows: 3},
	}})
	if err != nil {
		t.Fatal(err)
	}
	users := planTable(t, plan, "users")
	for column, want := range map[string]string{"email": "email", "id": seedAuto, "last_name": seedNull, "first_name": "firstName"} {
		if c, _ := planColumn(t, users, column); c.Generator != want {
			t.Errorf("users.%s is filled by %q, want %q", column, c.Generator, want)
		}
	}
	got := strings.Join(plan.Warnings, "\n")
	for _, want := range []string{
		"users has no column gone now", "users.id is filled by the server",
		`users.email cannot be filled with "integer" now`, `users.first_name cannot be filled with "nope" now`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no warning saying %q in %q", want, got)
		}
	}
	if plan.Rows != 6 {
		t.Errorf("the plan writes %d rows", plan.Rows)
	}
}

func TestSeedChosenGeneratorsAreKept(t *testing.T) {
	db := openSeedDB(t)
	plan, err := PlanSeed(context.Background(), db, DriverSQLite, SeedRequest{Seed: 3, Tables: []SeedTableRequest{{
		Name: "users", Rows: 4, Columns: map[string]string{"last_name": "null", "status": "default", "first_name": "city"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	users := planTable(t, plan, "users")
	last, lastAt := planColumn(t, users, "last_name")
	status, statusAt := planColumn(t, users, "status")
	first, firstAt := planColumn(t, users, "first_name")
	if last.Generator != seedNull || status.Generator != seedDefault || first.Generator != "city" {
		t.Fatalf("%s %s %s", last.Generator, status.Generator, first.Generator)
	}
	cities := map[string]bool{}
	for _, c := range seedCities {
		cities[c.city] = true
	}
	for _, row := range users.Preview {
		if row[lastAt] != nil || row[statusAt] != nil || !cities[*row[firstAt]] {
			t.Errorf("row %v", row)
		}
	}
	if status.Later != "its default" {
		t.Errorf("a default stands as %q", status.Later)
	}
}

func TestSeedNameWords(t *testing.T) {
	for in, want := range map[string]string{
		"created_at": "created at", "createdAt": "created at", "CREATED_AT": "created at",
		"HTTPStatus": "http status", "userID": "user id", "address_line1": "address line1",
	} {
		if got := strings.Join(seedNameWords(in), " "); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}

func TestSeedGeneratorsChosenByNameAndType(t *testing.T) {
	col := func(driver Driver, name, typ string, unique bool) *seedColumn {
		return &seedColumn{name: name, typeName: typ, t: parseORMType(driver, typ), driver: driver, unique: unique}
	}
	for _, tc := range []struct {
		c     *seedColumn
		table string
		want  string
	}{
		{col(DriverPostgres, "email", "character varying(255)", true), "users", "email"},
		{col(DriverPostgres, "email", "integer", false), "users", "integer"},
		{col(DriverPostgres, "name", "text", false), "customers", "fullName"},
		{col(DriverPostgres, "name", "text", false), "products", "productName"},
		{col(DriverPostgres, "name", "text", false), "projects", "title"},
		{col(DriverPostgres, "createdAt", "timestamp with time zone", false), "x", "pastMoment"},
		{col(DriverPostgres, "expires_at", "timestamp", false), "x", "futureMoment"},
		{col(DriverPostgres, "is_admin", "boolean", false), "x", "boolean"},
		{col(DriverOracle, "IS_ADMIN", "NUMBER(1)", false), "X", "boolean"},
		{col(DriverPostgres, "external_id", "uuid", false), "x", "uuid"},
		{col(DriverPostgres, "price", "numeric(10,2)", false), "x", "price"},
		{col(DriverPostgres, "code", "integer", false), "x", "integer"},
		{col(DriverPostgres, "rank", "integer", true), "x", "sequence"},
		{col(DriverPostgres, "country", "character(2)", false), "x", "countryCode"},
		{col(DriverPostgres, "description", "varchar(80)", false), "x", "sentence"},
		{col(DriverPostgres, "description", "text", false), "x", "paragraph"},
		{col(DriverMySQL, "role", "enum('admin','editor')", false), "x", "choice"},
		{col(DriverMySQL, "born", "year", false), "x", "year"},
		{col(DriverPostgres, "client_ip", "inet", false), "x", "ipAddress"},
		{col(DriverPostgres, "settings", "jsonb", false), "x", "json"},
		{col(DriverPostgres, "outline", "geometry", false), "x", ""},
		{col(DriverMSSQL, "id", "uniqueidentifier", true), "x", "uuid"},
	} {
		if tc.c.t.Kind == ormEnumKind {
			tc.c.values = tc.c.t.Values
		}
		g := inferSeedGenerator(tc.c, tc.table)
		got := ""
		if g != nil {
			got = g.id
		}
		if got != tc.want {
			t.Errorf("%s %s in %s: %q, want %q", tc.c.name, tc.c.typeName, tc.table, got, tc.want)
		}
	}
}

func TestSeedChecksAreReadInEveryEnginesSpelling(t *testing.T) {
	checks := readSeedChecks([]string{
		// PostgreSQL's pg_get_constraintdef.
		`CHECK (((status)::text = ANY ((ARRAY['draft'::character varying, 'live'::character varying])::text[])))`,
		`CHECK ((price > (0)::numeric))`,
		`CHECK (((qty >= 1) AND (qty <= 9)))`,
		// MySQL's SHOW CREATE TABLE.
		"CHECK ((`kind` in (_utf8mb4'a',_utf8mb4'b''s')))",
		// SQLite's and SQL Server's.
		`CHECK (level BETWEEN 2 AND 4)`,
		`([tier] IN (1, 2, 3))`,
		`CHECK (length(name) > 2)`,
		// SQL Server keeps an IN list as the equalities it stands for.
		`([grade]='c' OR [grade]='b' OR [grade]='a')`,
		`((((mood)::text = 'up'::text) OR ((mood)::text = 'down'::text)))`,
		`([odd]='x' OR [other]='y')`,
	})
	if got := checks["status"]; got == nil || strings.Join(got.values, ",") != "draft,live" {
		t.Errorf("status: %+v", got)
	}
	if got := checks["price"]; got == nil || got.lo == nil || *got.lo != 0 || !got.exclLo {
		t.Errorf("price: %+v", got)
	}
	if got := checks["qty"]; got == nil || *got.lo != 1 || *got.hi != 9 {
		t.Errorf("qty: %+v", got)
	}
	if got := checks["kind"]; got == nil || strings.Join(got.values, "|") != "a|b's" {
		t.Errorf("kind: %+v", got)
	}
	if got := checks["level"]; got == nil || *got.lo != 2 || *got.hi != 4 {
		t.Errorf("level: %+v", got)
	}
	if got := checks["tier"]; got == nil || !got.numbered || strings.Join(got.values, ",") != "1,2,3" {
		t.Errorf("tier: %+v", got)
	}
	if got := checks["grade"]; got == nil || strings.Join(got.values, ",") != "c,b,a" {
		t.Errorf("grade: %+v", got)
	}
	if got := checks["mood"]; got == nil || strings.Join(got.values, ",") != "up,down" {
		t.Errorf("mood: %+v", got)
	}
	if _, ok := checks["odd"]; ok {
		t.Error("an OR of two columns was read as a list of one")
	}
	if _, ok := checks["name"]; ok {
		t.Error("a check on an expression of the column was read as one on the column")
	}
	if got := checkClauses("CREATE TABLE t (a INT CHECK (a > 0), b TEXT, CONSTRAINT x CHECK (b IN ('(', ')')))"); len(got) != 2 || got[1] != "(b IN ('(', ')'))" {
		t.Errorf("clauses %q", got)
	}
}

func TestSeedValuesFitTheirColumns(t *testing.T) {
	stream := seedStream(11, "t")
	row := newSeedRow(stream)
	for _, tc := range []struct {
		driver Driver
		typ    string
		gen    string
		unique bool
		check  func(string) bool
	}{
		{DriverPostgres, "varchar(10)", "email", false, func(v string) bool { return len(v) <= 10 }},
		{DriverPostgres, "varchar(24)", "email", true, func(v string) bool { return len(v) <= 24 && strings.Contains(v, ".1") && strings.Contains(v, "@") }},
		{DriverPostgres, "varchar(8)", "email", true, func(v string) bool { return len(v) <= 8 && strings.HasPrefix(v, "1") }},
		{DriverPostgres, "varchar(6)", "fullName", true, func(v string) bool { return len(v) <= 6 }},
		{DriverPostgres, "numeric(4,2)", "price", false, func(v string) bool { f, _ := strconv.ParseFloat(v, 64); return f < 100 }},
		{DriverPostgres, "numeric(5,0)", "latitude", false, func(v string) bool { return !strings.Contains(v, ".") }},
		{DriverMySQL, "tinyint", "integer", false, func(v string) bool { n, _ := strconv.Atoi(v); return n <= 127 }},
		{DriverMSSQL, "tinyint", "age", false, func(v string) bool { n, _ := strconv.Atoi(v); return n >= 18 && n <= 90 }},
		{DriverMSSQL, "datetime", "pastMoment", false, regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d$`).MatchString},
		{DriverMySQL, "timestamp", "pastMoment", false, regexp.MustCompile(`^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d$`).MatchString},
		{DriverPostgres, "timestamptz", "pastMoment", false, regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`).MatchString},
		{DriverPostgres, "date", "birthDate", false, regexp.MustCompile(`^\d{4}-\d\d-\d\d$`).MatchString},
		{DriverPostgres, "uuid", "uuid", false, regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString},
		{DriverOracle, "RAW(16)", "uuid", false, regexp.MustCompile(`^\\x[0-9a-f]{32}$`).MatchString},
		{DriverMSSQL, "bit", "boolean", false, func(v string) bool { return v == "true" || v == "false" }},
		{DriverOracle, "NUMBER(1)", "boolean", false, func(v string) bool { return v == "1" || v == "0" }},
		{DriverPostgres, "jsonb", "json", false, func(v string) bool { return json.Valid([]byte(v)) }},
		{DriverPostgres, "inet", "ipAddress", false, regexp.MustCompile(`^(192\.0\.2|198\.51\.100|203\.0\.113)\.\d+$`).MatchString},
	} {
		c := &seedColumn{
			name: "x", typeName: tc.typ, t: parseORMType(tc.driver, tc.typ), driver: tc.driver,
			unique: tc.unique, start: 12345, anchor: time.Now().UTC().Truncate(24 * time.Hour),
		}
		g := seedGeneratorByID[tc.gen]
		if !g.fits(c) {
			t.Errorf("%s does not fit %s", tc.gen, tc.typ)
			continue
		}
		for n := range 50 {
			v, null := g.value(c, stream, row, n)
			if null || !tc.check(v) {
				t.Errorf("%s in %s %s: %q", tc.gen, tc.driver, tc.typ, v)
				break
			}
		}
	}
}

// A table that only points into a cycle is not part of it: its key is not
// the one the cycle is broken at, and it points at the rows the seed writes.
func TestSeedBreaksACycleOnlyWhereItCloses(t *testing.T) {
	db := openSeedDB(t,
		`CREATE TABLE posts (id INTEGER PRIMARY KEY, user_ref INTEGER REFERENCES members(id), title TEXT NOT NULL)`,
		`CREATE TABLE members (id INTEGER PRIMARY KEY, team_id INTEGER REFERENCES teams(id), name TEXT NOT NULL)`,
		`CREATE TABLE teams (id INTEGER PRIMARY KEY, owner_id INTEGER NOT NULL REFERENCES members(id), name TEXT NOT NULL)`,
	)
	ctx := context.Background()
	req := SeedRequest{Seed: 4, Tables: seedTables(10, "posts", "teams", "members")}
	plan, err := PlanSeed(ctx, db, DriverSQLite, req)
	if err != nil {
		t.Fatal(err)
	}
	posts, members := planTable(t, plan, "posts"), planTable(t, plan, "members")
	if len(posts.Notes) != 0 || strings.Join(posts.After, ",") != "members" {
		t.Errorf("posts: after %v, notes %v", posts.After, posts.Notes)
	}
	if len(members.Notes) != 1 || !strings.Contains(members.Notes[0], "members and teams point at each other") {
		t.Errorf("members: notes %v", members.Notes)
	}
	if _, err := Seed(ctx, db, DriverSQLite, req); err != nil {
		t.Fatal(err)
	}
	var empty int
	if err := db.QueryRow(`SELECT COUNT(*) FROM posts WHERE user_ref IS NULL`).Scan(&empty); err != nil || empty != 0 {
		t.Errorf("%d posts point at no member (%v)", empty, err)
	}
}

// A one-to-one key that may be empty is empty once every parent row is
// taken, rather than holding its table to as many rows as the parent has.
func TestSeedFillsAnOptionalOneToOneKeyAndThenLeavesItEmpty(t *testing.T) {
	db := openSeedDB(t,
		`CREATE TABLE files (id INTEGER PRIMARY KEY, path TEXT NOT NULL)`,
		`INSERT INTO files (path) VALUES ('a'), ('b'), ('c')`,
		`CREATE TABLE avatars (id INTEGER PRIMARY KEY, file_id INTEGER UNIQUE REFERENCES files(id), label TEXT)`,
	)
	ctx := context.Background()
	req := SeedRequest{Seed: 5, Tables: seedTables(20, "avatars")}
	plan, err := PlanSeed(ctx, db, DriverSQLite, req)
	if err != nil {
		t.Fatal(err)
	}
	if p := planTable(t, plan, "avatars"); p.Rows != 20 || len(p.Notes) != 0 {
		t.Fatalf("avatars: rows %d, notes %v", p.Rows, p.Notes)
	}
	report, err := Seed(ctx, db, DriverSQLite, req)
	if err != nil || report.Inserted != 20 || report.Skipped != 0 {
		t.Fatalf("report %+v, %v", report, err)
	}
	var pointing, distinct int
	if err := db.QueryRow(`SELECT COUNT(file_id), COUNT(DISTINCT file_id) FROM avatars`).Scan(&pointing, &distinct); err != nil {
		t.Fatal(err)
	}
	if pointing != 3 || distinct != 3 {
		t.Errorf("%d avatars point at %d files", pointing, distinct)
	}
}

// Two keys sharing a column: a row drawn again for one is checked against
// the other too, so nothing the table would refuse is ever sent.
func TestSeedKeepsKeysThatShareAColumnApart(t *testing.T) {
	db := openSeedDB(t, `CREATE TABLE grid (
		a INTEGER NOT NULL CHECK (a IN (1, 2, 3)),
		b INTEGER NOT NULL CHECK (b IN (1, 2, 3)),
		c INTEGER NOT NULL CHECK (c IN (1, 2, 3)),
		PRIMARY KEY (a, b),
		UNIQUE (b, c)
	)`)
	ctx := context.Background()
	for seed := int64(1); seed <= 20; seed++ {
		if _, err := db.Exec(`DELETE FROM grid`); err != nil {
			t.Fatal(err)
		}
		report, err := Seed(ctx, db, DriverSQLite, SeedRequest{Seed: seed, Tables: seedTables(20, "grid")})
		if err != nil {
			t.Fatal(err)
		}
		if g := report.Tables[0]; g.Skipped != 0 || g.Inserted == 0 {
			t.Fatalf("seed %d: inserted %d, skipped %d, errors %v", seed, g.Inserted, g.Skipped, g.Errors)
		}
	}
}

// A join table whose rows are drawn again to keep their pairs apart still
// writes the values its preview showed in every other column.
func TestSeedPreviewOfAJoinTableIsWhatIsWritten(t *testing.T) {
	db := openSeedDB(t,
		`CREATE TABLE tags (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE post_tags (
			order_id INTEGER NOT NULL REFERENCES orders(id),
			tag_id INTEGER NOT NULL REFERENCES tags(id),
			first_name TEXT NOT NULL,
			PRIMARY KEY (order_id, tag_id)
		)`,
	)
	ctx := context.Background()
	req := SeedRequest{Seed: 8, Tables: []SeedTableRequest{
		{Name: "users", Rows: 2}, {Name: "orders", Rows: 3}, {Name: "tags", Rows: 3}, {Name: "post_tags", Rows: 8},
	}}
	plan, err := PlanSeed(ctx, db, DriverSQLite, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Seed(ctx, db, DriverSQLite, req); err != nil {
		t.Fatal(err)
	}
	joins := planTable(t, plan, "post_tags")
	_, at := planColumn(t, joins, "first_name")
	rows, err := db.Query(`SELECT first_name FROM post_tags ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if n < len(joins.Preview) && name != *joins.Preview[n][at] {
			t.Errorf("row %d was written as %s and previewed as %s", n, name, *joins.Preview[n][at])
		}
		n++
	}
	if n != 8 {
		t.Errorf("%d pairs written", n)
	}
}
