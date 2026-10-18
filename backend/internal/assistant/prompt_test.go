package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// What the model is told about a database, and what it is not.

// shop is a SQLite file with rows in it whose values must never be sent: a
// row, a default and a comment that each say so.
func shop(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE customers (id INTEGER PRIMARY KEY, email TEXT NOT NULL UNIQUE, tier TEXT DEFAULT 'row-value-in-a-default')`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, customer_id INTEGER NOT NULL REFERENCES customers(id), total NUMERIC, status TEXT NOT NULL) -- row-value-in-a-comment`,
		`CREATE TABLE audit_events (id INTEGER PRIMARY KEY, at TEXT)`,
		`CREATE VIEW shipped AS SELECT id, total FROM orders WHERE status = 'shipped'`,
		`INSERT INTO customers(email, tier) VALUES ('row-value@example.com', 'gold')`,
		`INSERT INTO orders(customer_id, total, status) VALUES (1, 42.5, 'row-value-status')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%v\n%s", err, stmt)
		}
	}
	return db
}

func TestTheSystemPromptCarriesTheStructureAndNoRows(t *testing.T) {
	db := shop(t)
	snapshot, err := ReadSchema(context.Background(), db, dbx.DriverSQLite, "", "count the orders by customer")
	if err != nil {
		t.Fatal(err)
	}
	text, stats := SchemaText(snapshot, "count the orders by customer")
	prompt := SystemPrompt("SQLite 3.46.0", text, true)
	for _, want := range []string{
		"SQLite 3.46.0 database", "Schema in focus: main",
		"TABLE orders\n", "customer_id INTEGER NOT NULL REFERENCES customers(id)",
		"email TEXT NOT NULL UNIQUE", "id INTEGER PRIMARY KEY", "VIEW shipped",
		"describe_tables", "```sql",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "row-value") {
		t.Fatalf("a value from inside the database reached the prompt:\n%s", prompt)
	}
	if stats.Described != 4 || stats.Named != 0 || stats.Omitted != 0 || stats.Schema != "main" {
		t.Errorf("stats %+v", stats)
	}
	// The named tables come first, then what they reference.
	if strings.Index(text, "TABLE orders") > strings.Index(text, "TABLE customers") ||
		strings.Index(text, "TABLE customers") > strings.Index(text, "TABLE audit_events") {
		t.Errorf("the tables the question names are not first:\n%s", text)
	}
	if strings.Contains(SystemPrompt("SQLite", text, false), "describe_tables") {
		t.Error("a conversation with no tools is told about one")
	}
}

// A schema of any size is a prompt of a known size: described tables up to
// one budget, names up to another, the rest counted.
func TestTheSchemaTextIsBoundedHoweverLargeTheSchema(t *testing.T) {
	s := &Snapshot{Schema: "public"}
	for i := range 3000 {
		name := fmt.Sprintf("table_%04d", i)
		s.Names = append(s.Names, TableName{Schema: "public", Name: name, Kind: "table"})
		if i < graphTables {
			cols := []Column{{Name: "id", Type: "bigint", PrimaryKey: true}}
			for c := range 40 {
				cols = append(cols, Column{Name: fmt.Sprintf("a_rather_long_column_name_%02d", c), Type: "character varying(255)", Nullable: true})
			}
			s.Tables = append(s.Tables, Table{TableName: s.Names[i], Columns: cols})
		}
	}
	text, stats := SchemaText(s, "what is in table_0149")
	if len(text) > describedBudget+namesBudget+200 {
		t.Errorf("the schema text is %d characters", len(text))
	}
	if stats.Described == 0 || stats.Described == graphTables || stats.Named == 0 || stats.Omitted == 0 {
		t.Errorf("stats %+v", stats)
	}
	if stats.Described+stats.Named+stats.Omitted != len(s.Names) {
		t.Errorf("tables lost from the count: %+v of %d", stats, len(s.Names))
	}
	if !strings.HasPrefix(strings.SplitN(text, "\n\n", 2)[1], "TABLE table_0149") {
		t.Error("the table the question named was not described first")
	}
	if !strings.Contains(text, fmt.Sprintf("and %d more not named here", stats.Omitted)) {
		t.Error("the tables left out are not counted")
	}
}

func TestDescribeReadsWhatAToolCallNamed(t *testing.T) {
	db := shop(t)
	snapshot, err := ReadSchema(context.Background(), db, dbx.DriverSQLite, "", "")
	if err != nil {
		t.Fatal(err)
	}
	tables, missing := Describe(context.Background(), db, dbx.DriverSQLite, snapshot.Schema, []string{"ORDERS", "main.customers", "invoices"}, snapshot.Names)
	if len(tables) != 2 || tables[0].Name != "orders" || tables[1].Name != "customers" {
		t.Fatalf("described %+v", tables)
	}
	if len(missing) != 1 || missing[0] != "invoices" {
		t.Errorf("missing %v", missing)
	}
	text := DescribedText(tables, missing, snapshot.Schema)
	if !strings.Contains(text, "customer_id INTEGER NOT NULL REFERENCES customers(id)") ||
		!strings.Contains(text, "No table or view is called invoices.") || strings.Contains(text, "row-value") {
		t.Errorf("the tool's answer:\n%s", text)
	}
}

func TestATaskBecomesAMessageWithinItsBounds(t *testing.T) {
	msg, err := Task{Kind: TaskError, Statement: "SELEC 1", Error: "syntax error at or near \"SELEC\""}.Message()
	if err != nil || !strings.Contains(msg, "<statement>\nSELEC 1\n</statement>") || !strings.Contains(msg, "<error>") {
		t.Errorf("error task: %v\n%s", err, msg)
	}
	msg, err = Task{Kind: TaskPlan, Statement: "SELECT 1", Plan: strings.Repeat("Seq Scan ", 10_000)}.Message()
	if err != nil || !strings.Contains(msg, "[the rest was cut]") || len(msg) > maxPlan+2000 {
		t.Errorf("plan task: %v, %d characters", err, len(msg))
	}
	msg, err = Task{Kind: TaskPolicy, Table: "orders", Prompt: "each customer sees their own"}.Message()
	if err != nil || !strings.Contains(msg, "row-level security for the table orders") || !strings.Contains(msg, "FORCE ROW LEVEL SECURITY") {
		t.Errorf("policy task: %v\n%s", err, msg)
	}
	for _, refused := range []struct {
		task  Task
		field string
	}{
		{Task{Kind: TaskSQL}, "prompt"},
		{Task{Kind: TaskSQL, Prompt: strings.Repeat("x", maxPrompt+1)}, "prompt"},
		{Task{Kind: TaskSQL, Prompt: "x", Statement: strings.Repeat("x", maxStatement+1)}, "statement"},
		{Task{Kind: TaskError, Statement: "SELECT 1"}, "error"},
		{Task{Kind: TaskPlan, Plan: "x"}, "plan"},
		{Task{Kind: TaskPolicy, Prompt: "x"}, "table"},
		{Task{Kind: "drop everything", Prompt: "x"}, "kind"},
	} {
		_, err := refused.task.Message()
		if got := fieldOf(t, err); got != refused.field {
			t.Errorf("%+v refused on %q, want %q", refused.task.Kind, got, refused.field)
		}
	}
}

// A result reaches the model only when attached, and then only as much of it
// as the bounds allow, whatever the page sent.
func TestAnAttachedResultIsHeldToItsBounds(t *testing.T) {
	var body struct {
		Rows [][]any `json:"rows"`
	}
	rows := make([][]any, 80)
	for i := range rows {
		rows[i] = []any{i, strings.Repeat("v", 500), nil, map[string]int{"n": i}, "a|b\nc"}
	}
	encoded, _ := json.Marshal(map[string]any{"rows": rows})
	dec := json.NewDecoder(strings.NewReader(string(encoded)))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		t.Fatal(err)
	}
	columns := make([]string, 40)
	for i := range columns {
		columns[i] = fmt.Sprintf("c%d", i)
	}
	task := Task{Kind: TaskSQL, Prompt: "why are these totals wrong", Result: &Result{Columns: columns, Rows: body.Rows, Total: 900}}
	msg, err := task.Message()
	if err != nil {
		t.Fatal(err)
	}
	// The header and fifty rows.
	if !strings.Contains(msg, `<result rows="50" of="900">`) || strings.Count(msg, "\n| ") != 51 {
		t.Errorf("rows sent:\n%s", msg[:400])
	}
	if strings.Contains(msg, strings.Repeat("v", maxCell+1)) || !strings.Contains(msg, "(10 more columns not attached)") {
		t.Error("a cell or a column past the bound was sent")
	}
	if !strings.Contains(msg, `| 7 | `) || !strings.Contains(msg, "NULL") || !strings.Contains(msg, `{"n":7}`) || !strings.Contains(msg, `a\|b c`) {
		t.Errorf("values are not written as the grid shows them:\n%s", msg[:600])
	}
	attached, _ := json.Marshal(task.Attachments())
	if string(attached) != `{"resultColumns":30,"resultRows":50}` {
		t.Errorf("the audit's account of the attachment: %s", attached)
	}
	if plain, _ := json.Marshal(Task{Kind: TaskSQL, Prompt: "x"}.Attachments()); string(plain) != "{}" {
		t.Errorf("a question with nothing attached: %s", plain)
	}
}

func TestMentionedFindsWholeWordsOnly(t *testing.T) {
	names := []TableName{{Name: "orders"}, {Name: "order_items"}, {Name: "Users"}, {Name: "user"}}
	var got []string
	for _, n := range Mentioned("join ORDERS to users, not order", names) {
		got = append(got, n.Name)
	}
	if strings.Join(got, ",") != "orders,Users" {
		t.Errorf("mentioned %v", got)
	}
}
