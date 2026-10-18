package dbx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Writing a seed.
//
// The rows go in through the import's writer (importRun): multi-row INSERTs
// sized under each engine's parameter limit, a refused batch undone and
// written again a row at a time to find the row, savepoints on PostgreSQL so
// that one refused row does not spoil the transaction, and SQL Server's values
// converted in the statement. A seed's row is refused for the reasons a file's
// is — a CHECK this did not read, a trigger, a unique index over an
// expression — and it is left out and counted the same way.
//
// Everything is one transaction, tables and all, so a seed that fails part of
// the way leaves nothing behind. ClickHouse has none: there each table is its
// own batch, sent when it is complete, and a table written before a failure
// stays.

// SeedReport is what a seed wrote.
type SeedReport struct {
	Seed   int64             `json:"seed"`
	Tables []SeedTableReport `json:"tables"`
	// Inserted and Skipped are every table's together.
	Inserted int      `json:"inserted"`
	Skipped  int      `json:"skipped"`
	Atomic   bool     `json:"atomic"`
	Warnings []string `json:"warnings"`
	Duration string   `json:"duration"`
}

// SeedTableReport is what one table was given.
type SeedTableReport struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	// Rows is how many it was to be given, Inserted how many it took and
	// Skipped how many it refused.
	Rows     int              `json:"rows"`
	Inserted int              `json:"inserted"`
	Skipped  int              `json:"skipped"`
	Errors   []ImportRowError `json:"errors"`
	// Problem is why nothing was written to it.
	Problem   string   `json:"problem,omitempty"`
	Notes     []string `json:"notes"`
	Statement string   `json:"statement"`
}

// Seed writes the rows a plan describes.
func Seed(ctx context.Context, db *sql.DB, driver Driver, req SeedRequest) (*SeedReport, error) {
	started := time.Now()
	run, err := newSeedRun(ctx, db, driver, &req)
	if err != nil {
		return nil, err
	}
	report := &SeedReport{
		Seed: run.seed, Tables: []SeedTableReport{}, Atomic: driver != DriverClickHouse,
		Warnings: append([]string{}, run.warnings...),
	}
	if driver == DriverClickHouse {
		err = run.writeEach(db, report)
	} else {
		err = run.writeAll(db, report)
	}
	if err != nil {
		return nil, err
	}
	for _, t := range report.Tables {
		report.Inserted += t.Inserted
		report.Skipped += t.Skipped
	}
	report.Duration = time.Since(started).Round(time.Millisecond).String()
	return report, nil
}

// writeAll writes every table inside one transaction.
func (run *seedRun) writeAll(db *sql.DB, report *SeedReport) error {
	tx, err := db.BeginTx(run.ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := run.read(tx); err != nil {
		return err
	}
	for _, t := range run.written() {
		out, err := run.writeTable(tx, t)
		if err != nil {
			return err
		}
		report.Tables = append(report.Tables, *out)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("the seed could not be committed, and nothing was written: %w", err)
	}
	committed = true
	return nil
}

// writeEach is ClickHouse's: a batch per table, each sent when it is whole.
// A table written stays written whatever happens to the next, so a table
// that fails is reported as left out, with why, and the seed goes on: the
// answer has to say which tables are in, or a second try writes them twice.
func (run *seedRun) writeEach(db *sql.DB, report *SeedReport) error {
	if err := run.read(db); err != nil {
		return err
	}
	for _, t := range run.written() {
		if t.problem != "" {
			report.Tables = append(report.Tables, run.leftOut(t))
			continue
		}
		out, err := run.writeBatch(db, t)
		if err != nil {
			if cerr := run.ctx.Err(); cerr != nil {
				return cerr
			}
			left := run.leftOut(t)
			left.Rows = t.rows
			var row *importRowFailure
			if errors.As(err, &row) {
				// The driver gathers a batch and cannot carry on past a row
				// it refuses. Nothing of this table was sent.
				left.Problem = fmt.Sprintf("row %d was refused, and ClickHouse cannot carry on past it: %v", row.row, row.err)
			} else {
				left.Problem = fmt.Sprintf("%s could not be written: %v", t.label, err)
			}
			out = &left
		}
		report.Tables = append(report.Tables, *out)
	}
	return nil
}

// writeBatch writes one table as one ClickHouse batch.
func (run *seedRun) writeBatch(db *sql.DB, t *seedTable) (*SeedTableReport, error) {
	tx, err := db.BeginTx(run.ctx, nil)
	if err != nil {
		return nil, err
	}
	out, err := run.writeTable(tx, t)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// written is every table in the order it is written, then the ones left out.
func (run *seedRun) written() []*seedTable {
	out := append([]*seedTable{}, run.order...)
	in := map[*seedTable]bool{}
	for _, t := range out {
		in[t] = true
	}
	for _, t := range run.tables {
		if !in[t] {
			out = append(out, t)
		}
	}
	return out
}

func (run *seedRun) leftOut(t *seedTable) SeedTableReport {
	return SeedTableReport{
		Schema: t.schema, Name: t.name, Rows: 0, Errors: []ImportRowError{},
		Problem: t.problem, Notes: append([]string{}, t.notes...),
	}
}

// writeTable writes one table's rows inside tx.
func (run *seedRun) writeTable(tx *sql.Tx, t *seedTable) (*SeedTableReport, error) {
	if t.problem == "" {
		if err := run.pointAtParents(tx, t); err != nil {
			return nil, err
		}
		// A parent left out while the seed was writing is one whose rows
		// from before are what this table points at, and they were not read.
		for _, link := range t.links {
			if !link.seeded() && !link.loaded {
				if err := run.readPool(tx, t, link, nil); err != nil {
					return nil, err
				}
				link.loaded = true
			}
		}
	}
	if t.problem == "" {
		if problem := run.linkProblem(t); problem != "" {
			t.problem = problem
		}
		for _, link := range t.links {
			if link.seeded() && link.required && len(link.pool) == 0 {
				t.problem = fmt.Sprintf("%s must point at a %s row, and none of the %s rows this seed wrote went in",
					strings.Join(link.fk.Columns, ", "), link.parent.label, link.parent.label)
			}
		}
	}
	if t.problem != "" {
		out := run.leftOut(t)
		return &out, nil
	}
	plan := run.importPlan(t)
	out := &SeedTableReport{
		Schema: t.schema, Name: t.name, Rows: t.rows, Errors: []ImportRowError{},
		Notes: append([]string{}, t.notes...), Statement: run.statement(t, plan),
	}
	if len(plan.cols) == 0 {
		return out, run.writeDefaults(tx, t, out)
	}
	importReport := &ImportReport{Errors: []ImportRowError{}}
	writer := &importRun{
		ctx: run.ctx, tx: tx, plan: plan, strategy: plan.strategy(), report: importReport,
		statements: map[int]string{},
	}
	defer writer.close()
	if err := writer.prepare(); err != nil {
		return nil, err
	}
	run.begin(t)
	var self []*seedLink
	for _, link := range t.links {
		if link.self {
			self = append(self, link)
		}
	}
	made := map[int][]seedCell{}
	stopped := false
	chunk := seedChunk
	if len(self) > 0 {
		chunk = max(seedPreviewRows, min(seedChunk, t.rows/5))
	}
	for n := 0; n < t.rows && !stopped; {
		if n > 0 {
			// The rows written so far are rows this table's own key can
			// point at.
			for _, link := range self {
				if err := run.readPool(tx, t, link, nil); err != nil {
					return nil, err
				}
			}
		}
		for k := 0; k < chunk && n < t.rows; k++ {
			cells, ok := run.next(t)
			if !ok {
				stopped = true
				out.Notes = append(out.Notes, run.stoppedNote(t, n))
				break
			}
			n++
			if len(t.referenced) > 0 {
				made[n] = cells
			}
			args, err := run.args(plan, t, cells)
			if err != nil {
				if err := writer.bad(n, n, err); err != nil {
					return nil, err
				}
				continue
			}
			if err := writer.add(pendingRow{row: n, line: n, args: args}); err != nil {
				return nil, err
			}
		}
		if err := writer.flush(); err != nil {
			return nil, err
		}
	}
	out.Inserted, out.Skipped = importReport.Inserted, importReport.Skipped
	for _, e := range importReport.Errors {
		delete(made, e.Row)
		if len(out.Errors) < seedErrorSample {
			out.Errors = append(out.Errors, e)
		}
	}
	t.keptRows = nil
	for n := 1; n <= t.made; n++ {
		if cells, ok := made[n]; ok {
			t.keptRows = append(t.keptRows, cells)
		}
	}
	return out, nil
}

// stoppedNote says why a table was given fewer rows than planned.
func (run *seedRun) stoppedNote(t *seedTable, n int) string {
	for _, link := range t.links {
		if link.unique {
			return fmt.Sprintf("%s points at each %s row once, and there were %d for it: %d rows were written",
				strings.Join(link.fk.Columns, ", "), link.fk.RefTable, len(link.pool), n)
		}
	}
	return fmt.Sprintf("after %d rows no combination of values was left that no earlier row had", n)
}

// writeDefaults writes rows of nothing but defaults, for a table whose every
// column the server fills.
func (run *seedRun) writeDefaults(tx *sql.Tx, t *seedTable, out *SeedTableReport) error {
	statement := run.statement(t, nil)
	for range t.rows {
		if _, err := tx.ExecContext(run.ctx, statement); err != nil {
			return fmt.Errorf("%s: %w", t.label, err)
		}
		out.Inserted++
	}
	return nil
}

// pointAtParents gives each of a table's foreign keys to a seeded parent the
// keys of the rows that parent was just given.
func (run *seedRun) pointAtParents(q seedQuerier, t *seedTable) error {
	for _, link := range t.links {
		if !link.seeded() {
			continue
		}
		parent := link.parent
		ours := true
		at := make([]int, len(link.fk.RefColumns))
		for i, name := range link.fk.RefColumns {
			at[i] = parent.column(name)
			if at[i] < 0 || parent.cols[at[i]].gen == nil || !parent.cols[at[i]].gen.writesValue() {
				ours = false
			}
		}
		switch {
		case ours:
			// The values the seed made for the parent's rows that went in.
			link.pool = nil
			for _, cells := range parent.keptRows {
				tuple := make([]seedCell, len(at))
				for i, j := range at {
					tuple[i] = cells[j]
				}
				link.pool = append(link.pool, tuple)
			}
			link.perm = nil
		case parent.autoKey != nil && len(at) == 1 && parent.cols[at[0]] == parent.autoKey:
			// Numbered by the server: the new ones are past the largest
			// number it had handed out.
			if err := run.readPool(q, t, link, parent.maxBefore); err != nil {
				return err
			}
		default:
			if err := run.readPool(q, t, link, nil); err != nil {
				return err
			}
			t.notes = append(t.notes, fmt.Sprintf(
				"%s points at any %s row, not only the new ones: %s's key is made by the server in a way the seed cannot tell apart",
				strings.Join(link.fk.Columns, ", "), parent.label, parent.label))
		}
		room := len(link.pool)
		if !link.required {
			room++
		}
		if link.unique && run.capsRows(link) && room < t.rows {
			t.rows = room
			t.notes = append(t.notes, fmt.Sprintf("%s points at each %s row once, and %d of the rows this seed wrote went in: %d rows are written",
				strings.Join(link.fk.Columns, ", "), parent.label, len(link.pool), room))
		}
	}
	return nil
}

// importPlan is the import's plan for the columns a seed writes: every one
// it makes a value for, points at a parent with, or leaves empty on purpose.
func (run *seedRun) importPlan(t *seedTable) *importPlan {
	plan := &importPlan{
		d: run.d, rel: t.rel,
		spec: ImportSpec{
			Schema: t.schema, Table: t.name, Mode: ImportModeInsert, SkipBadRows: true,
			BatchSize: seedChunk, MaxErrors: t.rows + 1,
		},
	}
	for i, c := range t.cols {
		if c.gen == nil || !(c.gen.writesValue() || c.gen.id == seedNull || c.gen.id == seedReference) {
			continue
		}
		plan.cols = append(plan.cols, plannedColumn{
			source: i, name: c.name, quoted: c.quoted, family: c.family, typeName: c.typeName,
		})
	}
	return plan
}

// statement is the INSERT one row is written with, as the report shows it.
func (run *seedRun) statement(t *seedTable, plan *importPlan) string {
	if plan != nil && len(plan.cols) > 0 {
		text, err := plan.shownStatement()
		if err == nil {
			return text
		}
	}
	clause := "DEFAULT VALUES"
	if e, ok := run.d.(emptyInserter); ok {
		if form, err := e.emptyInsert(); err == nil {
			clause = form
		}
	}
	return "INSERT INTO " + t.rel + " " + clause
}

// args are a row's values as the driver binds them.
func (run *seedRun) args(plan *importPlan, t *seedTable, cells []seedCell) ([]any, error) {
	out := make([]any, len(plan.cols))
	for i, pc := range plan.cols {
		cell := cells[pc.source]
		c := t.cols[pc.source]
		var v any
		switch {
		case cell.null:
		case cell.read:
			// A key read back from the parent goes back as the driver gave it:
			// the same type, the same bytes.
			v = cell.raw
		default:
			bound, err := bindValue(run.driver, importValue{text: cell.text}, c.family, c.typeName)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", c.name, err)
			}
			v = bound
		}
		if n, ok := v.(int64); ok && run.driver == DriverMSSQL {
			// Every value reaches SQL Server as text or as bytes, so the
			// conversion written around it is always one the server has.
			v = strconv.FormatInt(n, 10)
		}
		out[i] = v
	}
	if run.driver == DriverClickHouse {
		return out, nil
	}
	return sqlArguments(out)
}
