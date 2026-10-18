package dbx

import (
	"context"
	"database/sql"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Statement history: what each statement added between two readings of the
// statement list, so the list can be read over an hour, a day or a week and
// not only since the server's counters last started.
//
// Most engines keep running totals per statement — calls, time, rows — from
// when the counters were zeroed or the statement's entry was made. A snapshot
// is one reading of those totals, and an interval is the difference between
// two: what ran in between. ClickHouse keeps every run in its query log
// instead, so its snapshot is the runs that finished since the snapshot
// before, summed, and no difference is taken.
//
// The text a history keeps is the statement list's own. A snapshot's texts
// come from TopStatements and from nowhere else, so each is a shape — every
// value replaced — by whatever rule that list holds the engine to. The reads
// a snapshot adds for an engine (rows read, durations, ClickHouse's window)
// select a statement's id and numbers, never its text.

func init() {
	// The recorder keeps a history of every statement list it can read.
	RegisterCapabilities(Capability{Flag: "statementHistory", Has: opsFlag("statements")})
}

// Durations counts calls by how long each took. A key is the upper edge of a
// bucket in milliseconds, and its count the calls that took at most that long
// and longer than the edge below it. MySQL's buckets are its own histogram's,
// about 5% wide; ClickHouse's are cut here from each run's duration, one a
// millisecond up to 20 ms and about 4% wide above.
type Durations map[float64]int64

// Quantile is the upper edge of the bucket the q-th call falls in: never
// under the true figure, and over it by at most that bucket's width.
func (d Durations) Quantile(q float64) (float64, bool) {
	edges := make([]float64, 0, len(d))
	var n int64
	for edge, count := range d {
		if count > 0 {
			edges = append(edges, edge)
			n += count
		}
	}
	if n == 0 {
		return 0, false
	}
	sort.Float64s(edges)
	want := max(int64(math.Ceil(q*float64(n))), 1)
	var seen int64
	for _, edge := range edges {
		if seen += d[edge]; seen >= want {
			return edge, true
		}
	}
	return edges[len(edges)-1], true
}

// Add counts other's calls into d.
func (d Durations) Add(other Durations) {
	for edge, count := range other {
		d[edge] += count
	}
}

// since is what was counted after before, bucket by bucket. It is false when
// a bucket went down, which is the engine having started counting again.
func (d Durations) since(before Durations) (Durations, bool) {
	out := Durations{}
	for edge, count := range d {
		switch was := before[edge]; {
		case count < was:
			return nil, false
		case count > was:
			out[edge] = count - was
		}
	}
	for edge, was := range before {
		if was > 0 && d[edge] == 0 {
			return nil, false
		}
	}
	return out, true
}

// String is the form a history keeps: edge:count pairs, the smallest first.
func (d Durations) String() string {
	edges := make([]float64, 0, len(d))
	for edge, count := range d {
		if count > 0 {
			edges = append(edges, edge)
		}
	}
	sort.Float64s(edges)
	parts := make([]string, len(edges))
	for i, edge := range edges {
		parts[i] = strconv.FormatFloat(edge, 'g', -1, 64) + ":" + strconv.FormatInt(d[edge], 10)
	}
	return strings.Join(parts, " ")
}

// ParseDurations reads what String wrote, and nil from an empty string. A
// pair it cannot read is left out.
func ParseDurations(text string) Durations {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	out := Durations{}
	for _, pair := range strings.Fields(text) {
		edge, count, ok := strings.Cut(pair, ":")
		e, err1 := strconv.ParseFloat(edge, 64)
		n, err2 := strconv.ParseInt(count, 10, 64)
		if ok && err1 == nil && err2 == nil && n > 0 {
			out[e] += n
		}
	}
	return out
}

// StatementFigures are one statement's numbers: running totals in a
// snapshot, and what ran in an interval.
type StatementFigures struct {
	Calls   int64
	TotalMs float64
	// Rows is what the statement returned or changed.
	Rows int64
	// RowsRead is what the engine read to answer, where it counts that, and
	// -1 where it does not.
	RowsRead int64
	// Durations is how long each call took, where the engine keeps that.
	Durations Durations
}

// since is what ran after before. It is false when a total went down: the
// statement's entry was made again, and every figure it has ran since.
func (f StatementFigures) since(before StatementFigures) (StatementFigures, bool) {
	if f.Calls < before.Calls || f.TotalMs < before.TotalMs || f.Rows < before.Rows {
		return f, false
	}
	out := StatementFigures{
		Calls: f.Calls - before.Calls, TotalMs: f.TotalMs - before.TotalMs, Rows: f.Rows - before.Rows, RowsRead: -1,
	}
	if f.RowsRead >= 0 && before.RowsRead >= 0 {
		if f.RowsRead < before.RowsRead {
			return f, false
		}
		out.RowsRead = f.RowsRead - before.RowsRead
	}
	// Durations that were not read before have nothing to be taken from.
	if f.Durations != nil && before.Durations != nil {
		durations, ok := f.Durations.since(before.Durations)
		if !ok {
			return f, false
		}
		out.Durations = durations
	}
	return out, true
}

// StatementSnapshot is one reading of a server's statement statistics.
type StatementSnapshot struct {
	// At is when it was taken, by this machine's clock.
	At        time.Time
	Supported bool
	Reason    string
	// Window is true where the figures are the runs since the snapshot
	// before, not running totals: ClickHouse's.
	Window bool
	// Through is where a window ends, by the server's own clock; the next
	// window starts there.
	Through time.Time
	// Since is when the engine last zeroed its counters, where it says.
	Since *time.Time
	// TotalMs is the time of every statement the engine counts, not only of
	// the ones read here.
	TotalMs float64
	// Figures and Texts are by the engine's id of a statement.
	Figures map[string]StatementFigures
	Texts   map[string]string
	// What the engine counts besides calls, time and rows.
	RowsRead  bool
	Durations bool
}

// statementSnapshotter is an engine's own part of a snapshot: the figures
// the statement list does not carry. It reads ids and numbers, never text.
type statementSnapshotter interface {
	statementSnapshot(ctx context.Context, db *sql.DB, snap, prev *StatementSnapshot) error
}

// TakeStatementSnapshot reads a server's statement statistics for its
// history: the statement list at its fullest, in the order of the time each
// statement has taken, with what the engine counts besides. prev is the
// snapshot before it, which a window starts from.
func TakeStatementSnapshot(ctx context.Context, db *sql.DB, driver Driver, prev *StatementSnapshot) (*StatementSnapshot, error) {
	report, err := TopStatements(ctx, db, driver, StatementsOptions{Sort: StatementsByTotal, Limit: maxStatementsLimit})
	if err != nil {
		return nil, err
	}
	snap := &StatementSnapshot{
		At: time.Now().UTC(), Supported: report.Supported, Reason: report.Reason, Since: report.Since,
		TotalMs: report.TotalMs, Figures: map[string]StatementFigures{}, Texts: map[string]string{},
	}
	if !report.Supported {
		return snap, nil
	}
	for _, s := range report.Statements {
		// A statement of another role's that this account may not read has
		// neither an id nor a text.
		if s.ID == "" {
			continue
		}
		// One id can be listed more than once — per schema on MySQL, per role
		// on PostgreSQL — and is one statement here.
		f, seen := snap.Figures[s.ID]
		if !seen {
			f.RowsRead = -1
		}
		f.Calls += s.Calls
		f.TotalMs += s.TotalMs
		f.Rows += s.Rows
		snap.Figures[s.ID] = f
		if snap.Texts[s.ID] == "" {
			snap.Texts[s.ID] = s.Query
		}
	}
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	if extra, ok := d.(statementSnapshotter); ok {
		if err := extra.statementSnapshot(ctx, db, snap, prev); err != nil {
			return nil, err
		}
	}
	return snap, nil
}

// StatementsInterval is what ran between two snapshots.
type StatementsInterval struct {
	From, To time.Time
	// Reset is true when the counters started again in between, so what is
	// counted is what ran after that.
	Reset bool
	// TotalMs is the time of every statement the engine counted in the
	// interval, the ones not listed among Statements included.
	TotalMs float64
	// Statements are the ones that ran, the most time first.
	Statements []StatementRun
}

// StatementRun is one statement's part of an interval.
type StatementRun struct {
	ID string
	StatementFigures
}

// StatementsBetween is what ran between prev and cur. resetHere says the
// dashboard zeroed the counters in between, which an engine that keeps no
// time of its last reset has no other way to tell.
//
// A statement that was not in the snapshot before is left out of its first
// interval: it may be new, or it may have climbed into the list with totals
// that ran up long before. One whose totals went down had its entry made
// again, and everything it has ran since the snapshot before.
func StatementsBetween(prev, cur *StatementSnapshot, resetHere bool) *StatementsInterval {
	out := &StatementsInterval{From: prev.At, To: cur.At}
	if cur.Window {
		for id, f := range cur.Figures {
			if f.Calls > 0 {
				out.Statements = append(out.Statements, StatementRun{ID: id, StatementFigures: f})
			}
		}
		out.TotalMs = cur.TotalMs
	} else {
		out.Reset = resetHere || (cur.Since != nil && prev.Since != nil && !cur.Since.Equal(*prev.Since))
		var listed float64
		for id, f := range cur.Figures {
			run := f
			before, had := prev.Figures[id]
			switch {
			case out.Reset:
			case !had:
				continue
			default:
				if d, ok := f.since(before); ok {
					run = d
				}
			}
			if run.Calls <= 0 {
				continue
			}
			listed += run.TotalMs
			out.Statements = append(out.Statements, StatementRun{ID: id, StatementFigures: run})
		}
		// The engine's total is of everything it counts, and went down if a
		// heavy entry was dropped to make room: the listed statements' time is
		// then the truer floor.
		out.TotalMs = cur.TotalMs - prev.TotalMs
		if out.Reset {
			out.TotalMs = cur.TotalMs
		}
		out.TotalMs = max(out.TotalMs, listed)
	}
	sort.Slice(out.Statements, func(i, j int) bool {
		a, b := out.Statements[i], out.Statements[j]
		if a.TotalMs != b.TotalMs {
			return a.TotalMs > b.TotalMs
		}
		return a.ID < b.ID
	})
	return out
}

// statementIDs are a snapshot's ids, as bind arguments, and the markers for
// an IN list of them.
func statementIDs(snap *StatementSnapshot) ([]any, string) {
	args := make([]any, 0, len(snap.Figures))
	for id := range snap.Figures {
		args = append(args, id)
	}
	return args, strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")
}

// --- MySQL / MariaDB ---------------------------------------------------------

// statementSnapshot adds the rows each digest examined, and on MySQL 8 the
// histogram performance_schema keeps of each digest's run times. MariaDB has
// no such histogram, and its digests have no durations here.
func (mysqlDialect) statementSnapshot(ctx context.Context, db *sql.DB, snap, _ *StatementSnapshot) error {
	ids, in := statementIDs(snap)
	if len(ids) == 0 {
		return nil
	}
	if err := eachRow(ctx, db, `
	  SELECT DIGEST, SUM(SUM_ROWS_EXAMINED)
	  FROM performance_schema.events_statements_summary_by_digest
	  WHERE `+mysqlStatementsScope+` AND DIGEST IN (`+in+`)
	  GROUP BY DIGEST`, ids, func(rows *sql.Rows) error {
		var id string
		var examined int64
		if err := rows.Scan(&id, &examined); err != nil {
			return err
		}
		if f, ok := snap.Figures[id]; ok {
			f.RowsRead = examined
			snap.Figures[id] = f
		}
		return nil
	}); err != nil {
		return err
	}
	snap.RowsRead = true

	var histogram bool
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) > 0 FROM information_schema.TABLES
	  WHERE TABLE_SCHEMA = 'performance_schema' AND TABLE_NAME = 'events_statements_histogram_by_digest'`).Scan(&histogram); err != nil || !histogram {
		return nil
	}
	for id, f := range snap.Figures {
		f.Durations = Durations{}
		snap.Figures[id] = f
	}
	if err := eachRow(ctx, db, `
	  SELECT DIGEST, BUCKET_TIMER_HIGH / 1e9, SUM(COUNT_BUCKET)
	  FROM performance_schema.events_statements_histogram_by_digest
	  WHERE `+mysqlStatementsScope+` AND COUNT_BUCKET > 0 AND DIGEST IN (`+in+`)
	  GROUP BY DIGEST, BUCKET_NUMBER, BUCKET_TIMER_HIGH`, ids, func(rows *sql.Rows) error {
		var id string
		var edge float64
		var count int64
		if err := rows.Scan(&id, &edge, &count); err != nil {
			return err
		}
		if f, ok := snap.Figures[id]; ok {
			f.Durations[edge] += count
		}
		return nil
	}); err != nil {
		// An account may be granted the digests and not their histograms:
		// the history then has no percentiles, and the rest of it stands.
		for id, f := range snap.Figures {
			f.Durations = nil
			snap.Figures[id] = f
		}
		return nil
	}
	snap.Durations = true
	return nil
}

// eachRow runs a query and hands each of its rows to read.
func eachRow(ctx context.Context, db *sql.DB, query string, args []any, read func(*sql.Rows) error) error {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := read(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// --- ClickHouse --------------------------------------------------------------

// clickhouseFlushLag is how far behind the server's clock a window ends. The
// query log is written in batches, every seven and a half seconds unless the
// server says otherwise, so the last few seconds' runs may not be in it yet,
// and a window that took them would have the next one miss them.
const clickhouseFlushLag = 15 * time.Second

// statementSnapshot replaces the list's day of runs with the runs that
// finished since the snapshot before, summed by shape, with how many rows
// each read and how long each took. The first snapshot only marks where the
// next one starts. The texts stay the list's.
func (clickhouseDialect) statementSnapshot(ctx context.Context, db *sql.DB, snap, prev *StatementSnapshot) error {
	var now int64
	if err := db.QueryRowContext(ctx, `SELECT toUnixTimestamp64Micro(now64(6))`).Scan(&now); err != nil {
		return err
	}
	snap.Window, snap.Since, snap.RowsRead, snap.Durations = true, nil, true, true
	snap.Through = time.UnixMicro(now).Add(-clickhouseFlushLag).UTC()
	snap.TotalMs, snap.Figures = 0, map[string]StatementFigures{}
	if prev == nil || !prev.Window || !prev.Through.Before(snap.Through) {
		return nil
	}
	from, through := prev.Through.UnixMicro(), snap.Through.UnixMicro()
	// The edge of a run's bucket: a millisecond wide up to 20 ms, then a
	// sixteenth of a doubling. The log has whole milliseconds, so a run of
	// 3 ms took less than 4.
	rows, err := db.QueryContext(ctx, `
	  SELECT id, toInt64(sum(n)), toFloat64(sum(ms)), toInt64(sum(returned)), toInt64(sum(examined)),
	         arrayStringConcat(groupArray(concat(toString(edge), ':', toString(n))), ' ')
	  FROM (
	    SELECT toString(normalized_query_hash) AS id,
	           if(query_duration_ms < 20, toFloat64(query_duration_ms + 1),
	              round(exp2(ceil(log2(toFloat64(query_duration_ms + 1)) * 16) / 16), 1)) AS edge,
	           count() AS n, sum(query_duration_ms) AS ms,
	           sum(result_rows) AS returned, sum(read_rows) AS examined
	    FROM system.query_log
	    WHERE `+clickhouseStatementsScope+`
	      AND event_date >= toDate(fromUnixTimestamp64Micro(toInt64(?)))
	      AND event_time_microseconds > fromUnixTimestamp64Micro(toInt64(?))
	      AND event_time_microseconds <= fromUnixTimestamp64Micro(toInt64(?))
	    GROUP BY id, edge
	  )
	  GROUP BY id`, from, from, through)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, durations string
		var f StatementFigures
		if err := rows.Scan(&id, &f.Calls, &f.TotalMs, &f.Rows, &f.RowsRead, &durations); err != nil {
			return err
		}
		f.Durations = ParseDurations(durations)
		snap.Figures[id] = f
		snap.TotalMs += f.TotalMs
	}
	return rows.Err()
}
