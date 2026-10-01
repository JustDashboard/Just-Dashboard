package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
)

// What a built-in dump holds, decided before a row is read.
//
// A dump is more than its tables. The tables alone restore into a database
// with no unique constraint, no index and no view: every query the application
// makes still answers, slowly, and the first duplicate it would have been
// refused is accepted. So the plan carries those too, and the order they have
// to be replayed in — a sequence before the column that draws from it, a
// parent before its partitions, rows before the foreign keys that would
// otherwise have to be satisfied row by row, a view after everything it reads.
//
// Each engine reads its own catalogue into the same plan; one writer and one
// reader serve them all.

type dumpPlan struct {
	// session statements go first and sessionEnd last, and are written whether
	// or not the dump carries structure: they are how the restoring session has
	// to be set for the rest to mean what it meant here.
	session    []dumpStatement
	sessionEnd []dumpStatement
	// beforeDrops is what has to go before anything can be dropped: on an
	// engine with no cascading drop, the foreign keys that point at the tables.
	beforeDrops []dumpStatement
	// before is structure the tables need to exist first: schemas, extensions,
	// types.
	before    []dumpStatement
	sequences []dumpObject
	// tables are in dependency order.
	tables []dumpTable
	// after is structure that is only added once the rows are in: constraints,
	// indexes, and the foreign keys last of all.
	after []dumpStatement
	// views are in dependency order.
	views []dumpObject
	// afterAll is data that is not rows: a sequence's position, a
	// materialized view's refresh.
	afterAll []dumpStatement
	// notes say what the dump deliberately leaves out; skipped names what it
	// could not read.
	notes   []string
	skipped []string
}

// dumpObject is something other than a table: a sequence or a view.
type dumpObject struct {
	rel    string
	drop   dumpStatement
	create dumpStatement
	after  []dumpStatement
	// refs are the names this one's definition mentions, for ordering.
	refs []string
	name string
	// schema and label are what the object is called, where name is a key
	// made for ordering — two views in different schemas may share a name —
	// rather than the name somebody would ask for it by.
	schema string
	label  string
}

// dumpTable is one table with everything the dump needs about it read once.
type dumpTable struct {
	table  Table
	detail *TableDetail
	rel    string
	create dumpStatement
	drop   dumpStatement
	// selectSQL reads the rows to dump; empty means every column of rel.
	selectSQL string
	// noData is a table whose rows are not its own: a partitioned parent's
	// live in its partitions and are dumped there.
	noData bool
	// overriding is set when the table generates a column the dump has to
	// write.
	overriding bool
	// documents are the columns, by quoted name, whose value is read as text
	// and has to be written back through the type's own constructor: Oracle's
	// XMLTYPE, which takes a short string as it is and a CLOB not at all.
	documents map[string]bool
	// beforeData goes in front of this table's rows and afterData follows
	// them: what lets a column the table numbers itself be written, and what
	// puts its counter back where it was.
	beforeData []dumpStatement
	afterData  []dumpStatement
	// parents are the tables this one has to be created after, beyond the
	// ones its foreign keys already name.
	parents []string
}

// tableRef names a table, with or without its schema.
type tableRef struct {
	schema string
	table  string
}

func (t tableRef) String() string {
	if t.schema == "" {
		return t.table
	}
	return t.schema + "." + t.table
}

// postgresPattern is the name as pg_dump's --table takes it: each part double
// quoted, so nothing in it is read as a wildcard.
func (t tableRef) postgresPattern() string {
	quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	if t.schema == "" {
		return quote(t.table)
	}
	return quote(t.schema) + "." + quote(t.table)
}

// dumpSelection is which tables a dump covers.
type dumpSelection struct {
	include []tableRef
	exclude []tableRef
}

// newDumpSelection parses the two lists. A name is "table" or "schema.table";
// a name with a dot of its own cannot be spelled in this form and is refused
// rather than guessed at.
func newDumpSelection(include, exclude []string) (dumpSelection, error) {
	var sel dumpSelection
	parse := func(names []string) ([]tableRef, error) {
		out := make([]tableRef, 0, len(names))
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name == "" {
				return nil, fmt.Errorf("a table name is empty")
			}
			ref := tableRef{table: name}
			if schema, table, ok := strings.Cut(name, "."); ok {
				if strings.Contains(table, ".") {
					return nil, fmt.Errorf("%q is not schema.table", name)
				}
				ref = tableRef{schema: schema, table: table}
			}
			if ref.schema != "" {
				if err := validateIdent(ref.schema); err != nil {
					return nil, err
				}
			}
			if err := validateIdent(ref.table); err != nil {
				return nil, err
			}
			if strings.HasPrefix(ref.table, "-") || strings.HasPrefix(ref.schema, "-") {
				return nil, fmt.Errorf("%q begins with a dash, which a dump tool would read as an option", name)
			}
			out = append(out, ref)
		}
		return out, nil
	}
	var err error
	if sel.include, err = parse(include); err != nil {
		return sel, err
	}
	if sel.exclude, err = parse(exclude); err != nil {
		return sel, err
	}
	return sel, nil
}

func (s dumpSelection) narrowed() bool { return len(s.include) > 0 }

func matchesAny(refs []tableRef, schema, table string) bool {
	for _, r := range refs {
		if r.table == table && (r.schema == "" || r.schema == schema) {
			return true
		}
	}
	return false
}

// wants reports whether a table is in the dump.
func (s dumpSelection) wants(schema, table string) bool {
	if matchesAny(s.exclude, schema, table) {
		return false
	}
	return len(s.include) == 0 || matchesAny(s.include, schema, table)
}

// named reports whether a table was asked for by name. A view is only dumped
// alongside a selection of tables when it was.
func (s dumpSelection) named(schema, table string) bool {
	return matchesAny(s.include, schema, table) && !matchesAny(s.exclude, schema, table)
}

// missing lists the included names that matched nothing, so a typo is an
// error rather than a dump quietly short of a table.
func (s dumpSelection) missing(found func(tableRef) bool) []string {
	var out []string
	for _, ref := range s.include {
		if !found(ref) {
			out = append(out, ref.String())
		}
	}
	return out
}

// planDump reads the catalogue into a plan for this engine.
func planDump(ctx context.Context, q dumpQueryer, db *sql.DB, d Dialect, database string, sel dumpSelection, opts DumpOptions) (*dumpPlan, error) {
	var (
		plan *dumpPlan
		err  error
	)
	switch d.Driver() {
	case DriverPostgres:
		plan, err = planPostgresDump(ctx, q, sel)
		if err != nil {
			// A server that speaks Postgres's protocol without keeping
			// Postgres's catalogue — CockroachDB is one — cannot be read that
			// way. What the standard views say is less, and is still a dump.
			reason := err
			if plan, err = planGenericDump(ctx, db, d, database, sel); err == nil {
				plan.notes = append(plan.notes,
					"written from the standard catalogue views; this server's own could not be read: "+reason.Error())
				opts.progress("reading the standard catalogue views (%v)", reason)
			}
		}
	case DriverMySQL:
		plan, err = planMySQLDump(ctx, q, sel)
	case DriverSQLite:
		plan, err = planSQLiteDump(ctx, q, sel)
	case DriverClickHouse:
		plan, err = planClickHouseDump(ctx, q, database, sel)
	case DriverMSSQL:
		plan, err = planMSSQLDump(ctx, q, sel)
	case DriverOracle:
		plan, err = planOracleDump(ctx, q, resolveDumpSchema(ctx, db, DriverOracle, database), sel)
	default:
		plan, err = planGenericDump(ctx, db, d, database, sel)
	}
	if err != nil {
		return nil, err
	}
	seen := func(ref tableRef) bool {
		for _, t := range plan.tables {
			if t.table.Name == ref.table && (ref.schema == "" || ref.schema == t.table.Schema) {
				return true
			}
		}
		for _, objects := range [][]dumpObject{plan.views, plan.sequences} {
			for _, o := range objects {
				name := o.name
				if o.label != "" {
					name = o.label
				}
				if name == ref.table && (ref.schema == "" || o.schema == "" || ref.schema == o.schema) {
					return true
				}
			}
		}
		return false
	}
	if missing := sel.missing(seen); len(missing) > 0 {
		return nil, fmt.Errorf("no such table to dump: %s", strings.Join(missing, ", "))
	}
	if len(plan.tables) == 0 && len(plan.views) == 0 && len(plan.skipped) == 0 && !sel.narrowed() {
		opts.progress("the database holds no tables")
	}
	return plan, nil
}

// planGenericDump is the plan read through the dialect alone, from the
// standard catalogue views. It is what a server that speaks an engine's
// protocol without keeping that engine's catalogue is dumped with: tables with
// the keys their generated DDL carries, and the indexes added once the rows
// are in. A view's definition is not something those views give back whole, so
// views are named in the file as left out.
func planGenericDump(ctx context.Context, db *sql.DB, d Dialect, database string, sel dumpSelection) (*dumpPlan, error) {
	driver := d.Driver()
	schema := resolveDumpSchema(ctx, db, driver, database)
	tables, err := d.Tables(ctx, db, schema)
	if err != nil {
		return nil, fmt.Errorf("cannot list tables: %w", err)
	}
	plan := &dumpPlan{notes: []string{
		"not included: procedures, functions, triggers, grants",
	}}

	// Read every table's structure first, then order the whole dump by what
	// references what.
	//
	// Alphabetical order is wrong twice over on any schema with a foreign key:
	// a CREATE naming a table that has not been created yet fails, and so does
	// an INSERT of a child row before its parent exists. `jd_posts` sorts before
	// `jd_users`, which is exactly the case, and the restore failed on the
	// second statement with a constraint error that described the symptom and
	// not the cause.
	for _, t := range tables {
		if isViewType(t.Type) {
			// A view has no rows of its own and INSERTing into one fails on
			// most engines.
			if sel.wants(t.Schema, t.Name) && (!sel.narrowed() || sel.named(t.Schema, t.Name)) {
				plan.skipped = append(plan.skipped, fmt.Sprintf("view %s: its definition is not in the standard catalogue views", t.Name))
			}
			continue
		}
		if !sel.wants(t.Schema, t.Name) {
			continue
		}
		detail, err := Detail(ctx, db, driver, t.Schema, t.Name)
		if err != nil {
			plan.skipped = append(plan.skipped, fmt.Sprintf("%s.%s: %s", t.Schema, t.Name, err.Error()))
			continue
		}
		rel, err := qualify(d, t.Schema, t.Name)
		if err != nil {
			plan.skipped = append(plan.skipped, fmt.Sprintf("%s.%s: %s", t.Schema, t.Name, err.Error()))
			continue
		}
		ddl := strings.TrimSpace(detail.CreateSQL)
		if ddl == "" {
			ddl = synthCreateTable(d, t.Schema, t.Name, detail)
		}
		plan.tables = append(plan.tables, dumpTable{
			table: t, detail: detail, rel: rel,
			create: rawStmt(ddl), drop: stmt(dropTableStatement(driver, rel)),
		})
		// An index is a separate object the table's definition never mentions.
		for _, ix := range detail.Indexes {
			if ix.Primary || len(ix.Columns) == 0 {
				continue
			}
			if create, err := createIndexStatement(d, rel, ix); err == nil {
				plan.after = append(plan.after, stmt(create))
			}
		}
	}
	plan.tables = orderByDependency(plan.tables)
	return plan, nil
}

func isViewType(tableType string) bool {
	return strings.Contains(strings.ToLower(tableType), "view")
}

// createIndexStatement rebuilds one index from what the catalogue reports of
// it: its name, its columns in order, and whether it is unique.
func createIndexStatement(d Dialect, rel string, ix Index) (string, error) {
	name, err := d.QuoteIdent(ix.Name)
	if err != nil {
		return "", err
	}
	cols := make([]string, len(ix.Columns))
	for i, c := range ix.Columns {
		q, err := d.QuoteIdent(c)
		if err != nil {
			return "", err
		}
		cols[i] = q
	}
	unique := ""
	if ix.Unique {
		unique = "UNIQUE "
	}
	return fmt.Sprintf("CREATE %sINDEX %s ON %s (%s)", unique, name, rel, strings.Join(cols, ", ")), nil
}

// resolveDumpSchema decides what to pass as the catalogue's "schema" for a dump
// of a whole database.
//
// The two words mean different things per engine and the difference is not
// cosmetic. Where the database is chosen by the connection string an empty
// schema means every schema in it — which is what a database dump should
// contain.
//
// Oracle is the one that cannot be decided statically, and getting it wrong is
// silent. Its schemas are users, but the name in a connection string is the
// *service* — "FREEPDB1" — so treating that as a schema filtered `all_tables`
// down to an owner nobody has and produced a dump of zero tables that reported
// success. An empty file that claims to be a backup is the worst thing this
// code can produce, so the name is checked against the catalogue and falls back
// to the schema the session is in — never to every schema the login can see,
// which is other people's tables and, on the way back in, other people's
// tables dropped.
func resolveDumpSchema(ctx context.Context, db *sql.DB, driver Driver, database string) string {
	switch driver {
	case DriverClickHouse:
		return database
	case DriverOracle:
		if database != "" {
			var n int
			err := db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM all_tables WHERE owner = :1`, database).Scan(&n)
			if err == nil && n > 0 {
				return database
			}
		}
		var current string
		if err := db.QueryRowContext(ctx,
			`SELECT SYS_CONTEXT('USERENV','CURRENT_SCHEMA') FROM dual`).Scan(&current); err == nil {
			return current
		}
		return ""
	default:
		return ""
	}
}

// orderByDependency sorts tables so a referenced table comes before the tables
// referencing it. A cycle — two tables pointing at each other, which several
// engines allow — cannot be ordered, so those tables keep their original
// position rather than being dropped from the dump; the restore of a cyclic
// schema needs the constraints relaxed, and losing the data would be worse than
// needing a hand with the constraints.
func orderByDependency(tables []dumpTable) []dumpTable {
	byName := map[string]int{}
	for i, t := range tables {
		byName[strings.ToLower(t.table.Name)] = i
	}
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make([]int, len(tables))
	out := make([]dumpTable, 0, len(tables))
	var visit func(i int)
	visit = func(i int) {
		if state[i] != unvisited {
			return
		}
		state[i] = visiting
		refs := append([]string(nil), tables[i].parents...)
		if tables[i].detail != nil {
			for _, fk := range tables[i].detail.ForeignKeys {
				refs = append(refs, fk.RefTable)
			}
		}
		for _, ref := range refs {
			j, ok := byName[strings.ToLower(ref)]
			if !ok || j == i {
				// A self-reference orders fine on its own, and a reference out
				// of this schema is not this dump's to satisfy.
				continue
			}
			if state[j] == visiting {
				continue // cycle
			}
			visit(j)
		}
		state[i] = done
		out = append(out, tables[i])
	}
	for i := range tables {
		visit(i)
	}
	return out
}

// nameObjectRefs works out which of a set of views read which others, from
// the quoted names in their definitions.
//
// No engine here reports that portably, and a view created before one it
// reads is a restore that stops. A name that merely appears in a definition
// without being read is counted as a dependency too, which costs nothing: it
// only moves a view later than it needed to be.
func nameObjectRefs(objects []dumpObject) {
	for i := range objects {
		text := strings.ToLower(objects[i].create.sql)
		for j := range objects {
			if i == j {
				continue
			}
			if containsIdentifier(text, strings.ToLower(objects[j].name)) {
				objects[i].refs = append(objects[i].refs, objects[j].name)
			}
		}
	}
}

// containsIdentifier reports whether name appears in text as a whole word, so
// a view called "orders" is not taken to be read by one that reads
// "orders_archive".
func containsIdentifier(text, name string) bool {
	if name == "" {
		return false
	}
	isWord := func(b byte) bool {
		return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 0x80
	}
	for from := 0; ; {
		i := strings.Index(text[from:], name)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(name)
		if (start == 0 || !isWord(text[start-1])) && (end == len(text) || !isWord(text[end])) {
			return true
		}
		from = start + 1
	}
}

// orderObjects sorts views so each comes after the ones it reads. A cycle
// cannot exist between real views; if the name matching invents one, the
// catalogue's own order stands.
func orderObjects(objects []dumpObject) []dumpObject {
	byName := map[string]int{}
	for i, o := range objects {
		byName[o.name] = i
	}
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make([]int, len(objects))
	out := make([]dumpObject, 0, len(objects))
	var visit func(i int)
	visit = func(i int) {
		if state[i] != unvisited {
			return
		}
		state[i] = visiting
		for _, ref := range objects[i].refs {
			if j, ok := byName[ref]; ok && j != i && state[j] != visiting {
				visit(j)
			}
		}
		state[i] = done
		out = append(out, objects[i])
	}
	for i := range objects {
		visit(i)
	}
	return out
}

// CreateDatabase makes an empty database on the connection's server for a
// dump to be restored into. Where the engine has no such statement — a SQLite
// database is a file, Oracle's is a user — it is refused, and the caller says
// so before anything runs.
func CreateDatabase(ctx context.Context, driver Driver, dsn, name string) error {
	if err := validateDumpDatabase(name); err != nil {
		return err
	}
	switch driver {
	case DriverMongo, DriverRedis:
		// A Mongo database exists once something is in it, and Redis's are
		// numbered and all there already.
		return nil
	case DriverSQLite, DriverOracle:
		return fmt.Errorf("%s has no database to create beside this one", driver)
	}
	admin, err := AdminFor(driver)
	if err != nil {
		return err
	}
	d, err := DialectFor(driver)
	if err != nil {
		return err
	}
	db, err := openForDump(ctx, d, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	return admin.CreateDatabase(ctx, db, name, "")
}

// DatabaseExists reports whether a database of this name is already on the
// connection's server. A restore "into a new database" that found one there
// would be a restore over somebody's data under another name.
//
// For Redis the question is whether the numbered database holds anything:
// they all exist, and an empty one is as new as one gets.
func DatabaseExists(ctx context.Context, driver Driver, dsn, name string) (bool, error) {
	switch driver {
	case DriverMongo:
		client, err := MongoClient(ctx, dsn)
		if err != nil {
			return false, err
		}
		defer client.Disconnect(context.Background())
		names, err := client.ListDatabaseNames(ctx, bson.D{{Key: "name", Value: name}})
		if err != nil {
			return false, err
		}
		return len(names) > 0, nil
	case DriverRedis:
		idx, err := redisDatabaseIndex(dsn, name)
		if err != nil {
			return false, err
		}
		client, err := RedisClient(ctx, dsn, 0)
		if err != nil {
			return false, err
		}
		defer client.Close()
		conn := client.Conn()
		defer conn.Close()
		if err := conn.Select(ctx, idx).Err(); err != nil {
			return false, err
		}
		n, err := conn.DBSize(ctx).Result()
		return n > 0, err
	}
	d, err := DialectFor(driver)
	if err != nil {
		return false, err
	}
	db, err := openForDump(ctx, d, dsn)
	if err != nil {
		return false, err
	}
	defer db.Close()
	databases, err := d.Databases(ctx, db)
	if err != nil {
		return false, err
	}
	for _, have := range databases {
		if have.Name == name {
			return true, nil
		}
	}
	return false, nil
}
