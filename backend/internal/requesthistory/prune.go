package requesthistory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
)

// Pruning: what is older than the retention, what is past a cap, and the
// strings no kept row names any more.
//
// Each delete is a range of one route's rows, which the table's clustering
// makes one cut out of its B-tree, and is done in steps so the recorder's
// next append never waits behind a delete of a month at once. A route a cap
// or the retention cut moves its lost_at past what went, so a window that
// reaches back into it says it is a floor.

// pruneStep bounds the rows one delete removes.
const pruneStep = 50_000

// caps are the bounds a prune holds the history to; the tests lower them.
type caps struct {
	route, total int64
}

// Prune deletes what the retention and the caps no longer keep, and the
// dictionary's strings no kept row can name.
func (a *Archive) Prune(ctx context.Context) error {
	return a.prune(ctx, caps{route: RouteCap, total: TotalCap})
}

type routeRows struct {
	id   int64
	kept int64
}

func (a *Archive) prune(ctx context.Context, limit caps) error {
	now := a.now()
	cutoff := now.Add(-Retention).UnixNano()
	routes, err := a.recount(ctx)
	if err != nil {
		return err
	}
	var total int64
	for i := range routes {
		removed, err := a.dropBefore(ctx, routes[i].id, cutoff, 0)
		if err != nil {
			return err
		}
		routes[i].kept -= removed
		if over := routes[i].kept - limit.route; over > 0 {
			// A tenth more than the excess, so a route at its cap is not cut
			// a few rows at a time on every pass.
			removed, err := a.dropOldest(ctx, routes[i].id, over+limit.route/10)
			if err != nil {
				return err
			}
			routes[i].kept -= removed
		}
		total += routes[i].kept
	}
	for total > limit.total {
		busiest := 0
		for i := range routes {
			if routes[i].kept > routes[busiest].kept {
				busiest = i
			}
		}
		n := min(total-limit.total+limit.total/20, routes[busiest].kept)
		if n <= 0 {
			break
		}
		removed, err := a.dropOldest(ctx, routes[busiest].id, n)
		if err != nil {
			return err
		}
		if removed == 0 {
			break
		}
		routes[busiest].kept -= removed
		total -= removed
	}
	if err := a.sweepStrings(ctx, now); err != nil {
		return err
	}
	return a.vacuum(ctx)
}

// recount sets every route's count of rows from the rows themselves, once a
// process: the count is kept beside every append and delete, and this is what
// puts it right if a crash between the two ever let it drift.
func (a *Archive) recount(ctx context.Context) ([]routeRows, error) {
	a.write.Lock()
	defer a.write.Unlock()
	if !a.counted {
		if _, err := a.db.Writer.ExecContext(ctx,
			`UPDATE routes SET kept = (SELECT count(*) FROM requests WHERE route = routes.id)`); err != nil {
			return nil, err
		}
		a.counted = true
	}
	rows, err := a.db.Writer.QueryContext(ctx, `SELECT id, kept FROM routes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []routeRows
	for rows.Next() {
		var r routeRows
		if err := rows.Scan(&r.id, &r.kept); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// dropOldest deletes route's n oldest rows.
func (a *Archive) dropOldest(ctx context.Context, route, n int64) (int64, error) {
	var at, seq int64
	err := a.db.Reader.QueryRowContext(ctx,
		`SELECT at, seq FROM requests WHERE route = ? ORDER BY at, seq LIMIT 1 OFFSET ?`, route, n).Scan(&at, &seq)
	if errors.Is(err, sql.ErrNoRows) {
		// Fewer rows than n: all of them.
		return a.dropBefore(ctx, route, math.MaxInt64, 0)
	}
	if err != nil {
		return 0, err
	}
	return a.dropBefore(ctx, route, at, seq)
}

// dropBefore deletes route's rows before (at, seq) in steps, and moves its
// lost_at to the newest instant deleted: a window that reaches back past it
// is missing what went, whether the retention or a cap took it.
func (a *Archive) dropBefore(ctx context.Context, route, at, seq int64) (int64, error) {
	var removed int64
	for {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		// The step's end: the pruneStep-th row before the bound, or the bound.
		endAt, endSeq := at, seq
		var stepAt, stepSeq int64
		err := a.db.Reader.QueryRowContext(ctx, `SELECT at, seq FROM requests
			WHERE route = ? AND (at, seq) < (?, ?) ORDER BY at, seq LIMIT 1 OFFSET ?`,
			route, at, seq, pruneStep).Scan(&stepAt, &stepSeq)
		switch {
		case err == nil:
			endAt, endSeq = stepAt, stepSeq
		case !errors.Is(err, sql.ErrNoRows):
			return removed, err
		}
		n, err := a.deleteBefore(ctx, route, endAt, endSeq)
		removed += n
		if err != nil || n == 0 || (endAt == at && endSeq == seq) {
			return removed, err
		}
	}
}

// deleteBefore is one step: the rows before (at, seq), the count, and the
// loss, in one transaction.
func (a *Archive) deleteBefore(ctx context.Context, route, at, seq int64) (int64, error) {
	a.write.Lock()
	defer a.write.Unlock()
	tx, err := a.db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var newest sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT at FROM requests WHERE route = ? AND (at, seq) < (?, ?)
		ORDER BY at DESC, seq DESC LIMIT 1`, route, at, seq).Scan(&newest); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if !newest.Valid {
		return 0, nil
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM requests WHERE route = ? AND (at, seq) < (?, ?)`, route, at, seq)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE routes SET kept = max(kept - ?, 0), lost_at = max(lost_at, ?) WHERE id = ?`,
		n, newest.Int64, route); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// sweepStrings deletes the strings no kept row can name: those last written
// into a row an hour and more before the oldest instant the retention keeps.
// A row names a string only if it was written as used in or after the hour
// of that row's instant and of its writing (Append), so a string last used
// before the cutoff is named by no row the retention kept. The writer's map
// starts again, since it may name a string this took.
func (a *Archive) sweepStrings(ctx context.Context, now time.Time) error {
	a.write.Lock()
	defer a.write.Unlock()
	before := now.Add(-Retention).Unix()/3600 - 1
	res, err := a.db.Writer.ExecContext(ctx, `DELETE FROM strings WHERE used < ?`, before)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		a.dict.reset()
		a.names.forget()
	}
	return nil
}

// vacuum gives the file's free pages back to the disk once they are a
// quarter of it, down to an eighth. A few are worth keeping: the next hour's
// appends reuse them rather than growing the file again.
func (a *Archive) vacuum(ctx context.Context) error {
	a.write.Lock()
	defer a.write.Unlock()
	var pages, free int64
	if err := a.db.Writer.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		return err
	}
	if err := a.db.Writer.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err != nil {
		return err
	}
	if free <= pages/4 {
		return nil
	}
	// A pragma takes no bound parameter; the count is this function's own.
	_, err := a.db.Writer.ExecContext(ctx, fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, free-pages/8))
	return err
}
