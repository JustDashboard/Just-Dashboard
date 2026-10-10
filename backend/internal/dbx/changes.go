package dbx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// A change set is what the table editor produces: the rows an operator added,
// edited and removed in the grid, applied together or not at all.
//
// It is the one path by which the dashboard writes rows without the operator
// having typed SQL, so its rules are stricter than the query runner's. The row
// a change names is resolved here, not trusted from the request: the table's
// primary key is read from the catalogue and every UPDATE and DELETE must name
// all of it. The statements run in one transaction, and every UPDATE and DELETE
// must touch exactly one row — not at most one, exactly one — or the whole set
// is rolled back and the offending change is named. A grid edit that silently
// did nothing because the row had changed underneath it is as much a failure as
// one that touched two.

// Change is one staged edit.
type Change struct {
	// Op is "insert", "update" or "delete".
	Op string `json:"op"`
	// Key identifies the row an update or a delete applies to: the primary key
	// columns, or — for a table with none — the row as it was read.
	Key map[string]any `json:"key,omitempty"`
	// Values are the columns to write. Only the columns that changed belong
	// here; a column that is absent is left alone.
	Values map[string]any `json:"values,omitempty"`
}

const (
	ChangeInsert = "insert"
	ChangeUpdate = "update"
	ChangeDelete = "delete"

	// MaxChanges bounds one set. The grid stages what a person edited by hand.
	MaxChanges = 1000
	// changeAttempts is how many times a set is run when the engine reports it
	// could not be ordered with a concurrent transaction.
	changeAttempts = 3
	// conflictSample bounds how many rows are read back from a statement that
	// matched too many, to report how badly.
	conflictSample = 100
)

// ChangeSet is a request to apply edits to one table.
type ChangeSet struct {
	Schema  string
	Table   string
	Changes []Change
	// DryRun renders the statements and validates the set against the
	// catalogue without opening a transaction.
	DryRun bool
}

// ChangeOutcome is what one change did.
type ChangeOutcome struct {
	Index int    `json:"index"`
	Op    string `json:"op"`
	// Statement is the change as SQL with its values written in, for the
	// operator to read. It is never what was executed — values are bound.
	Statement string `json:"statement"`
	Affected  int64  `json:"rowsAffected"`
	// Row is the row as the database now holds it: what an INSERT's defaults
	// and triggers filled in, what an UPDATE left. Absent for a delete, and
	// where the engine cannot hand the row back and the key cannot find it.
	Row map[string]any `json:"row,omitempty"`
	// Clipped names the columns of Row that hold a preview, not the value.
	Clipped []string `json:"clipped,omitempty"`
}

// ChangeSetResult is the answer to a change set.
type ChangeSetResult struct {
	Applied bool `json:"applied"`
	DryRun  bool `json:"dryRun"`
	// KeyColumns is the primary key the set was applied by. Empty means the
	// table has none and each change was matched on the row it supplied.
	KeyColumns []string        `json:"keyColumns"`
	Statements []string        `json:"statements"`
	Results    []ChangeOutcome `json:"results"`
	// Attempts is how many times the transaction ran; more than one means the
	// engine asked for a retry.
	Attempts int    `json:"attempts"`
	Duration string `json:"duration"`
}

// ChangeError is a change set that was not applied, and which change stopped
// it. Nothing in the set took effect.
type ChangeError struct {
	// Index is the position of the offending change in the request.
	Index int
	Op    string
	// Conflict reports that the change ran and did not touch exactly one row.
	Conflict bool
	// Matched is how many rows it touched; AtLeast marks a count that stopped
	// at the sample bound.
	Matched int64
	AtLeast bool
	Err     error
}

func (e *ChangeError) Error() string {
	if !e.Conflict {
		return fmt.Sprintf("change %d (%s): %v", e.Index+1, e.Op, e.Err)
	}
	matched := fmt.Sprintf("%d rows", e.Matched)
	switch {
	case e.AtLeast:
		matched = fmt.Sprintf("at least %d rows", e.Matched)
	case e.Matched == 0:
		matched = "no row"
	}
	return fmt.Sprintf("change %d (%s) matched %s, and must match exactly one; nothing was applied",
		e.Index+1, e.Op, matched)
}

func (e *ChangeError) Unwrap() error { return e.Err }

// ErrChangesUnsupported is returned for an engine that cannot apply a set
// atomically or cannot identify a row.
var ErrChangesUnsupported = errors.New(
	"ClickHouse has no transactions and no unique row key: its sorting key orders rows without identifying one, " +
		"and an UPDATE or DELETE is an asynchronous mutation of every row that matches. " +
		"Rows cannot be edited from the grid; use ALTER TABLE … UPDATE or DELETE in the query editor")

// ChangesSupported reports whether an engine can apply a change set at all.
func ChangesSupported(driver Driver) bool {
	d, err := DialectFor(driver)
	if err != nil {
		return false
	}
	refuser, refuses := d.(changeRefuser)
	return !refuses || refuser.refuseChanges() == nil
}

// What a change set needs to know about an engine, each found by type
// assertion because each is the exception rather than the rule.

// changeRefuser is implemented by an engine that cannot apply a change set.
type changeRefuser interface {
	refuseChanges() error
}

// changedRowCounter is implemented by the engines that count the rows an
// UPDATE changed rather than the rows it matched.
type changedRowCounter interface {
	countsChangedRows() bool
}

// emptyInserter is implemented by the engines that do not spell a row of
// nothing but defaults `DEFAULT VALUES`.
type emptyInserter interface {
	emptyInsert() (string, error)
}

// generatedKeyReader is implemented by the engines that hand a generated key
// back through a query on the same session rather than from the INSERT.
type generatedKeyReader interface {
	generatedKeyQuery() string
}

// defaultlessUpdater is implemented by an engine with no SET col = DEFAULT.
type defaultlessUpdater interface {
	updateCannotSetDefault() bool
}

// ownRowCounter is implemented by an engine whose driver reports, for an
// UPDATE or a DELETE, the rows of everything the statement set off — a
// trigger's writes added to the statement's own — or nothing at all when the
// session has counting turned off. Either way the figure the one-row rule
// needs is not the one that comes back, so the statement is made to say it.
type ownRowCounter interface {
	// countedSQL makes the statement answer with one row in a column named
	// ownCountColumn: how many rows it touched itself.
	countedSQL(statement string) string
}

// ownCountColumn names the result an ownRowCounter's statement answers with.
// A name rather than a position: a trigger may answer in rows of its own.
const ownCountColumn = "jd_rows_touched"

// plannedChange is one change rendered and ready to run.
type plannedChange struct {
	op       string
	sql      string
	args     []any
	rendered string
	// where and whereArgs are the row's identity on its own, for counting the
	// rows it matches and for reading the row back.
	where     string
	whereArgs []any
	// after is the key the row will have once the change is applied.
	after map[string]any
}

type changePlan struct {
	dialect Dialect
	rel     string
	columns map[string]Column
	// ordered is the same columns in the table's own order.
	ordered []Column
	key     []string
	changes []plannedChange
}

// sqlNull is how a change writes "no value": the keyword, in the statement
// itself, and never a bound argument.
//
// A bound NULL has to be given a type on the way to the server, and the
// driver has only the Go nil to choose one from. SQL Server's sends it as an
// nvarchar, which a varbinary, an image or a sql_variant-typed comparison
// refuses to be converted from ("Implicit conversion from data type nvarchar
// to varbinary is not allowed"), so a binary cell could be filled from the
// grid and never emptied again. The keyword has no type to disagree with any
// column, on any engine, and it is this package's own word rather than
// anything a request carried.
const sqlNull = "NULL"

// isDefault recognises {"$default": true}: write the column's default.
func isDefault(v any) bool {
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return false
	}
	on, ok := m["$default"].(bool)
	return ok && on
}

// loadTable reads what the catalogue says about a table: its columns and the
// key that identifies a row of it.
//
// The key is read here, on the server, every time. A key the request made up —
// a column that is not unique, or no column at all — is how one edit becomes an
// edit of every row.
func loadTable(ctx context.Context, db *sql.DB, driver Driver, schema, table string) (*changePlan, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	if table == "" {
		return nil, fmt.Errorf("table is required")
	}
	rel, err := qualify(d, schema, table)
	if err != nil {
		return nil, err
	}
	catalog := catalogSchema(ctx, db, d, schema)
	cols, err := d.Columns(ctx, db, catalog, table)
	if err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("table %s was not found, or has no columns this account can see", table)
	}
	plan := &changePlan{dialect: d, rel: rel, columns: map[string]Column{}, ordered: cols, key: []string{}}
	for _, c := range cols {
		plan.columns[c.Name] = c
	}
	pk, err := d.PrimaryKey(ctx, db, catalog, table)
	if err != nil {
		return nil, fmt.Errorf("read the primary key: %w", err)
	}
	if pk != nil {
		plan.key = pk
	}
	return plan, nil
}

// planChanges validates a set against the table and renders every statement.
// It touches the catalogue and nothing else, so a dry run and a real run
// refuse exactly the same requests.
func planChanges(ctx context.Context, db *sql.DB, driver Driver, set ChangeSet) (*changePlan, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	if refuser, ok := d.(changeRefuser); ok {
		if err := refuser.refuseChanges(); err != nil {
			return nil, err
		}
	}
	if len(set.Changes) == 0 {
		return nil, fmt.Errorf("no changes supplied")
	}
	if len(set.Changes) > MaxChanges {
		return nil, fmt.Errorf("a change set may hold at most %d changes", MaxChanges)
	}
	plan, err := loadTable(ctx, db, driver, set.Schema, set.Table)
	if err != nil {
		return nil, err
	}
	for i, c := range set.Changes {
		p, err := plan.render(c)
		if err != nil {
			return nil, &ChangeError{Index: i, Op: c.Op, Err: err}
		}
		plan.changes = append(plan.changes, *p)
	}
	return plan, nil
}

// column resolves a name against the table or refuses it.
func (p *changePlan) column(name string) (Column, string, error) {
	col, ok := p.columns[name]
	if !ok {
		return Column{}, "", fmt.Errorf("column %q is not in this table", name)
	}
	quoted, err := p.dialect.QuoteIdent(name)
	return col, quoted, err
}

// operand prepares one value for a statement: what the driver binds for it,
// how the marker that stands for it is written, and the same thing with the
// value written in, for the statement the operator reads.
func (p *changePlan) operand(col Column, v any) (arg any, write func(string) string, literal string, err error) {
	if arg, err = cellArgument(col, p.dialect.Driver(), v); err != nil {
		return nil, nil, "", err
	}
	write = func(operand string) string { return operand }
	if w, ok := p.dialect.(valueWriter); ok {
		bound, wrap := w.writeValue(col, arg)
		if arg = bound; wrap != nil {
			write = wrap
		}
	}
	if literal, err = sqlLiteral(p.dialect, arg); err != nil {
		return nil, nil, "", fmt.Errorf("column %s %w", col.Name, err)
	}
	return arg, write, write(literal), nil
}

// selection is the select list a row of the table is read back with, and what
// its cells are typed by.
func (p *changePlan) selection() (string, map[string]string) {
	if project, err := projectionFor(p.dialect, p.ordered, nil); err == nil && project != nil {
		return project.list, project.kinds
	}
	return "*", nil
}

// identity renders the WHERE that names one row.
func (p *changePlan) identity(key map[string]any, argStart int) (string, []any, []string, error) {
	if len(key) == 0 {
		return "", nil, nil, ErrNoPrimaryKey
	}
	for _, name := range p.key {
		v, ok := key[name]
		if !ok {
			return "", nil, nil, fmt.Errorf("the key must name every primary key column (%s)", strings.Join(p.key, ", "))
		}
		if v == nil {
			return "", nil, nil, fmt.Errorf("primary key column %q cannot be null", name)
		}
	}
	names := sortedKeys(key)
	parts := make([]string, 0, len(names))
	rendered := make([]string, 0, len(names))
	args := []any{}
	for _, name := range names {
		col, quoted, err := p.column(name)
		if err != nil {
			return "", nil, nil, err
		}
		if key[name] == nil {
			// `= NULL` is never true. A row whose key column is null is found
			// with IS NULL or not at all.
			parts = append(parts, quoted+" IS NULL")
			rendered = append(rendered, quoted+" IS NULL")
			continue
		}
		arg, write, literal, err := p.operand(col, key[name])
		if err != nil {
			return "", nil, nil, err
		}
		parts = append(parts, keyMatchFor(p.dialect, col, quoted, write(p.dialect.Placeholder(argStart+len(args)))))
		rendered = append(rendered, keyMatchFor(p.dialect, col, quoted, literal))
		args = append(args, arg)
	}
	return strings.Join(parts, " AND "), args, rendered, nil
}

func (p *changePlan) render(c Change) (*plannedChange, error) {
	d := p.dialect
	out := &plannedChange{op: c.Op}
	switch c.Op {
	case ChangeInsert:
		if len(c.Key) > 0 {
			return nil, fmt.Errorf("an insert takes values, not a key")
		}
		var names, marks, literals []string
		for _, name := range sortedKeys(c.Values) {
			col, quoted, err := p.column(name)
			if err != nil {
				return nil, err
			}
			if isDefault(c.Values[name]) {
				// Leaving the column out is what "use the default" means in
				// every engine, including the ones with no DEFAULT keyword.
				continue
			}
			if c.Values[name] == nil {
				names = append(names, quoted)
				marks = append(marks, sqlNull)
				literals = append(literals, sqlNull)
				continue
			}
			arg, write, literal, err := p.operand(col, c.Values[name])
			if err != nil {
				return nil, err
			}
			names = append(names, quoted)
			marks = append(marks, write(d.Placeholder(len(out.args)+1)))
			literals = append(literals, literal)
			out.args = append(out.args, arg)
		}
		if len(names) == 0 {
			clause := "DEFAULT VALUES"
			if e, ok := d.(emptyInserter); ok {
				var err error
				if clause, err = e.emptyInsert(); err != nil {
					return nil, err
				}
			}
			out.sql = "INSERT INTO " + p.rel + " " + clause
			out.rendered = out.sql
		} else {
			head := "INSERT INTO " + p.rel + " (" + strings.Join(names, ", ") + ") VALUES ("
			out.sql = head + strings.Join(marks, ", ") + ")"
			out.rendered = head + strings.Join(literals, ", ") + ")"
		}
		// Where the new row can be found again: only if the request supplied
		// every key column. A generated key is asked of the engine instead.
		out.after = map[string]any{}
		for _, name := range p.key {
			v, ok := c.Values[name]
			if !ok || v == nil || isDefault(v) {
				out.after = nil
				break
			}
			out.after[name] = v
		}
		if len(p.key) == 0 {
			out.after = nil
		}
	case ChangeUpdate:
		if len(c.Values) == 0 {
			return nil, fmt.Errorf("no column values supplied")
		}
		var sets, literals []string
		for _, name := range sortedKeys(c.Values) {
			col, quoted, err := p.column(name)
			if err != nil {
				return nil, err
			}
			if isDefault(c.Values[name]) {
				if u, ok := d.(defaultlessUpdater); ok && u.updateCannotSetDefault() {
					return nil, fmt.Errorf("column %s: this engine cannot set a column back to its default in an UPDATE", name)
				}
				sets = append(sets, quoted+" = DEFAULT")
				literals = append(literals, quoted+" = DEFAULT")
				continue
			}
			if c.Values[name] == nil {
				sets = append(sets, quoted+" = "+sqlNull)
				literals = append(literals, quoted+" = "+sqlNull)
				continue
			}
			arg, write, literal, err := p.operand(col, c.Values[name])
			if err != nil {
				return nil, err
			}
			sets = append(sets, quoted+" = "+write(d.Placeholder(len(out.args)+1)))
			literals = append(literals, quoted+" = "+literal)
			out.args = append(out.args, arg)
		}
		where, whereArgs, renderedWhere, err := p.identity(c.Key, len(out.args)+1)
		if err != nil {
			return nil, err
		}
		out.sql = "UPDATE " + p.rel + " SET " + strings.Join(sets, ", ") + " WHERE " + where
		out.rendered = "UPDATE " + p.rel + " SET " + strings.Join(literals, ", ") +
			" WHERE " + strings.Join(renderedWhere, " AND ")
		out.args = append(out.args, whereArgs...)
		// The row's identity afterwards: the key it was found by, with whatever
		// this change wrote over it.
		out.after = map[string]any{}
		for name, v := range c.Key {
			out.after[name] = v
		}
		for name, v := range c.Values {
			if _, keyed := out.after[name]; keyed {
				if isDefault(v) {
					out.after = nil
					break
				}
				out.after[name] = v
			}
		}
		out.where, out.whereArgs, _, err = p.identity(c.Key, 1)
		if err != nil {
			return nil, err
		}
	case ChangeDelete:
		if len(c.Values) > 0 {
			return nil, fmt.Errorf("a delete takes a key, not values")
		}
		where, whereArgs, renderedWhere, err := p.identity(c.Key, 1)
		if err != nil {
			return nil, err
		}
		out.sql = "DELETE FROM " + p.rel + " WHERE " + where
		out.rendered = "DELETE FROM " + p.rel + " WHERE " + strings.Join(renderedWhere, " AND ")
		out.args = whereArgs
	default:
		return nil, fmt.Errorf("op must be %s, %s or %s", ChangeInsert, ChangeUpdate, ChangeDelete)
	}
	out.rendered += ";"
	return out, nil
}

// ApplyChanges applies a change set in one transaction, or renders it.
//
// An error that is a *ChangeError names the change that stopped the set; in
// every error case nothing was applied.
func ApplyChanges(ctx context.Context, db *sql.DB, driver Driver, set ChangeSet) (*ChangeSetResult, error) {
	start := time.Now()
	plan, err := planChanges(ctx, db, driver, set)
	if err != nil {
		return nil, err
	}
	res := &ChangeSetResult{
		DryRun: set.DryRun, KeyColumns: plan.key,
		Statements: make([]string, len(plan.changes)),
		Results:    make([]ChangeOutcome, len(plan.changes)),
	}
	for i, c := range plan.changes {
		res.Statements[i] = c.rendered
		res.Results[i] = ChangeOutcome{Index: i, Op: c.op, Statement: c.rendered}
	}
	if set.DryRun {
		res.Duration = time.Since(start).Round(time.Microsecond).String()
		return res, nil
	}
	for res.Attempts = 1; ; res.Attempts++ {
		outcomes, err := plan.apply(ctx, db)
		if err == nil {
			for i := range outcomes {
				outcomes[i].Index, outcomes[i].Op = i, plan.changes[i].op
				outcomes[i].Statement = plan.changes[i].rendered
			}
			res.Results, res.Applied = outcomes, true
			res.Duration = time.Since(start).Round(time.Microsecond).String()
			return res, nil
		}
		// The engine could not order this transaction with another and aborted
		// it. Running it again is the documented answer; it is the whole
		// transaction that is retried, never one statement of it.
		if !IsSerializationFailure(err) || res.Attempts >= changeAttempts {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(res.Attempts) * 50 * time.Millisecond):
		}
	}
}

func (p *changePlan) apply(ctx context.Context, db *sql.DB) ([]ChangeOutcome, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	// Deferred so that every return below — a failed statement, a conflict, a
	// panic in a driver — leaves nothing behind. It is a no-op after Commit.
	defer func() { _ = tx.Rollback() }()

	outcomes := make([]ChangeOutcome, len(p.changes))
	for i := range p.changes {
		c := &p.changes[i]
		if err := p.applyOne(ctx, tx, c, &outcomes[i]); err != nil {
			var conflict *ChangeError
			if errors.As(err, &conflict) {
				conflict.Index, conflict.Op = i, c.op
				return nil, conflict
			}
			if IsSerializationFailure(err) {
				return nil, err
			}
			return nil, &ChangeError{Index: i, Op: c.op, Err: err}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return outcomes, nil
}

func (p *changePlan) applyOne(ctx context.Context, tx *sql.Tx, c *plannedChange, out *ChangeOutcome) error {
	d := p.dialect
	returning := d.SupportsReturning() && c.op != ChangeDelete

	if c.op == ChangeUpdate {
		if counter, ok := d.(changedRowCounter); ok && counter.countsChangedRows() {
			// The engine will report how many rows changed, which is zero for
			// an edit that writes back what was already there. So the rows the
			// key matches are counted, and locked, before the UPDATE runs.
			var matched int64
			args, err := sqlArguments(c.whereArgs)
			if err != nil {
				return err
			}
			if err := tx.QueryRowContext(ctx,
				"SELECT COUNT(*) FROM "+p.rel+" WHERE "+c.where+" FOR UPDATE", args...).Scan(&matched); err != nil {
				return err
			}
			if matched != 1 {
				return &ChangeError{Conflict: true, Matched: matched}
			}
		}
	}

	if returning {
		res, err := runOn(ctx, tx, d.Driver(), c.sql+" RETURNING *", true,
			collectOptions{maxRows: conflictSample}, c.args...)
		if err != nil {
			return err
		}
		out.Affected = int64(res.RowCount)
		if res.RowCount != 1 {
			return &ChangeError{Conflict: true, Matched: out.Affected, AtLeast: res.Truncated}
		}
		out.Row, out.Clipped = rowObject(res, 0)
		return nil
	}

	affected, err := p.execute(ctx, tx, c)
	if err != nil {
		return err
	}
	out.Affected = affected
	switch c.op {
	case ChangeDelete:
		if affected != 1 {
			return &ChangeError{Conflict: true, Matched: affected}
		}
		return nil
	case ChangeUpdate:
		if counter, ok := d.(changedRowCounter); ok && counter.countsChangedRows() {
			// Exactly one row was matched and locked above, whatever the engine
			// says it changed.
			out.Affected = 1
		} else if affected != 1 {
			return &ChangeError{Conflict: true, Matched: affected}
		}
	}
	p.readBack(ctx, tx, c, out)
	return nil
}

// execute runs a change that hands no row back and reports how many rows it
// touched.
func (p *changePlan) execute(ctx context.Context, tx *sql.Tx, c *plannedChange) (int64, error) {
	counter, counted := p.dialect.(ownRowCounter)
	if !counted || c.op == ChangeInsert {
		res, err := runOn(ctx, tx, p.dialect.Driver(), c.sql, false, collectOptions{}, c.args...)
		if err != nil {
			return 0, err
		}
		return res.Affected, nil
	}
	args, err := sqlArguments(c.args)
	if err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, counter.countedSQL(c.sql), args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	affected, found := int64(0), false
	for {
		if cols, err := rows.Columns(); err == nil && len(cols) == 1 && cols[0] == ownCountColumn {
			if rows.Next() {
				if err := rows.Scan(&affected); err != nil {
					return 0, err
				}
				found = true
			}
		}
		if !rows.NextResultSet() {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if !found {
		// No count is not a count of one: the change is not kept.
		return 0, fmt.Errorf("the engine did not say how many rows the change touched")
	}
	return affected, nil
}

// readBack fetches the row a change left behind, on the engines that cannot
// return it from the statement itself. It is a courtesy: the change has
// already been made and verified, so a row that cannot be found again is
// reported as absent rather than failing the set.
func (p *changePlan) readBack(ctx context.Context, tx *sql.Tx, c *plannedChange, out *ChangeOutcome) {
	d := p.dialect
	key := c.after
	if c.op == ChangeInsert && key == nil && len(p.key) == 1 {
		// One generated key column, on an engine that says what it generated
		// when asked on the connection the INSERT ran on.
		reader, ok := d.(generatedKeyReader)
		if !ok {
			return
		}
		var id int64
		if err := tx.QueryRowContext(ctx, reader.generatedKeyQuery()).Scan(&id); err != nil || id == 0 {
			return
		}
		key = map[string]any{p.key[0]: id}
	}
	if key == nil {
		return
	}
	where, args, _, err := p.identity(key, 1)
	if err != nil {
		return
	}
	tail, tailArgs := d.Paginate(2, 0, len(args)+1)
	list, kinds := p.selection()
	res, err := runOn(ctx, tx, d.Driver(), "SELECT "+list+" FROM "+p.rel+" WHERE "+where+" "+tail, true,
		collectOptions{maxRows: 2, kinds: kinds}, append(args, tailArgs...)...)
	if err != nil || res.RowCount != 1 {
		return
	}
	out.Row, out.Clipped = rowObject(res, 0)
}

// rowObject turns one row of a result into a column-keyed object, and names
// the columns whose cell is a preview.
func rowObject(res *QueryResult, row int) (map[string]any, []string) {
	obj := make(map[string]any, len(res.Columns))
	for i, name := range res.Columns {
		obj[name] = res.Rows[row][i]
	}
	var clipped []string
	for _, c := range res.Clipped {
		if c.Row == row {
			clipped = append(clipped, res.Columns[c.Column])
		}
	}
	return obj, clipped
}
