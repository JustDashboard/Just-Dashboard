package dbx

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// A database copied from one server to another.
//
// A copy on one server is a dump written to a file and a restore that reads
// it back (api/handlers_db_transfer.go). Between two servers the file is the
// part that costs: it is the size of the database, it has to fit on the
// dashboard's own volume, and the restore cannot begin until the dump has
// finished. So where the dump is SQL the dashboard writes itself, it is not
// written anywhere: the dump goes into one end of a pipe and the restore reads
// the other, and a row is on the target while the source is still reading the
// tables after it.
//
// MongoDB and Redis dump to an archive a tool or a restore reads by seeking, so
// those two stage it in a directory of the copy's own and remove it when the
// copy ends. ClickHouse, Oracle and SQLite are not here: their statements name
// the database or the file they came from, which is the reason none of them can
// be restored under another name either (DumpCapability.NewDatabase).

// CopyOptions says what to copy and where it goes.
type CopyOptions struct {
	// SourceDatabase is the database to read; empty means the one the source
	// connection names. Redis names its databases with numbers.
	SourceDatabase string
	// TargetDatabase is the database to load into. It exists and is empty:
	// making it is the caller's, who knows whether it may.
	TargetDatabase string
	// SchemaOnly copies the structure and no row. Engines whose dump cannot
	// leave the data out refuse it.
	SchemaOnly bool
	// ScratchDir is where an engine whose dump is a file stages it. A copy of
	// a database is the size of that database, so it is the volume sized for it.
	ScratchDir string
	// Progress receives the lines the tools and the dump write as they work.
	Progress func(line string)
	// Report receives how far along the copy is, about once a second and at
	// every table.
	Report func(CopyProgress)
}

// CopyProgress is how far a copy has got. Tables and Rows are what the source's
// catalogue said it would write; they are estimates, and a number the planner
// has never counted is left out of EstimatedRows.
type CopyProgress struct {
	// Phase is "copying" for a stream, and "dumping" then "loading" for an
	// engine that stages.
	Phase         string `json:"phase"`
	Tables        int    `json:"tables"`
	TablesDone    int    `json:"tablesDone"`
	Rows          int64  `json:"rows"`
	EstimatedRows int64  `json:"estimatedRows"`
	// Bytes is what has passed from the dump to the restore.
	Bytes int64 `json:"bytes"`
	// Table is the one being read now.
	Table string `json:"table,omitempty"`
}

// CopyResult is what a finished copy reports.
type CopyResult struct {
	Summary string `json:"summary"`
	// Streamed is whether the dump went from one server to the other without
	// being written down.
	Streamed bool `json:"streamed"`
	// Tool is what wrote the dump: "built-in", or the engine's own tool when it
	// was staged.
	Tool string `json:"tool,omitempty"`
}

// CanCopyBetween says whether a database of this engine can be copied to
// another server. It is the answer the dump capability table gives for a
// restore into a new database: the same two things, one after the other.
func CanCopyBetween(driver Driver) bool {
	return DumpCapabilities(driver).NewDatabase
}

// CopyBetween copies one database from the source connection to an empty
// database on the target. Both are the same engine: a dump is the engine's own
// dialect, and a Postgres dump is not something MySQL restores.
func CopyBetween(ctx context.Context, driver Driver, sourceDSN, targetDSN string, opts CopyOptions) (*CopyResult, error) {
	if !CanCopyBetween(driver) {
		return nil, fmt.Errorf("a %s database cannot be copied to another server", driver)
	}
	if err := ValidateDumpOptions(driver, DumpOptions{SchemaOnly: opts.SchemaOnly}); err != nil {
		return nil, err
	}
	switch driver {
	case DriverMongo, DriverRedis:
		return copyStaged(ctx, driver, sourceDSN, targetDSN, opts)
	}
	return copyStreamed(ctx, driver, sourceDSN, targetDSN, opts)
}

// copyTracker turns what the dump writes into CopyProgress.
type copyTracker struct {
	opts  CopyOptions
	mu    sync.Mutex
	state CopyProgress
	// refused is set when the restore's end of the pipe was closed on a write.
	refused bool
}

func newCopyTracker(opts CopyOptions, phase string) *copyTracker {
	return &copyTracker{opts: opts, state: CopyProgress{Phase: phase}}
}

func (t *copyTracker) trace() *dumpTrace {
	return &dumpTrace{
		planned: func(tables []Table) {
			t.update(func(p *CopyProgress) {
				p.Tables = len(tables)
				for _, table := range tables {
					if table.Rows > 0 {
						p.EstimatedRows += table.Rows
					}
				}
			})
		},
		wrote: func(rel string, rows int64) {
			t.update(func(p *CopyProgress) {
				p.TablesDone++
				p.Rows += rows
				p.Table = rel
			})
		},
	}
}

func (t *copyTracker) update(change func(*CopyProgress)) {
	t.mu.Lock()
	change(&t.state)
	snapshot := t.state
	t.mu.Unlock()
	if t.opts.Report != nil {
		t.opts.Report(snapshot)
	}
}

// every reports the running figures until the context ends, so a table that
// takes ten minutes still moves the byte count.
func (t *copyTracker) every(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.update(func(*CopyProgress) {})
		}
	}
}

// countedWriter counts what passes through to the restore, and notes when the
// restore stopped taking it: a dump that fails after that is the restore's
// failure arriving, not a failure of its own.
type countedWriter struct {
	w io.Writer
	t *copyTracker
}

func (c *countedWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.t.mu.Lock()
	c.t.state.Bytes += int64(n)
	if err != nil {
		c.t.refused = true
	}
	c.t.mu.Unlock()
	return n, err
}

func (t *copyTracker) writeRefused() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.refused
}

// dumpTrace is what a copy watches the built-in dump by.
type dumpTrace struct {
	// planned is told the tables whose rows the dump is about to write.
	planned func(tables []Table)
	// wrote is told when one table's rows are all written.
	wrote func(rel string, rows int64)
}

func copyStreamed(ctx context.Context, driver Driver, sourceDSN, targetDSN string, opts CopyOptions) (*CopyResult, error) {
	database := opts.SourceDatabase
	if database == "" {
		info, err := ParseDSN(driver, sourceDSN)
		if err != nil {
			return nil, err
		}
		database = info.Database
	}
	if err := validateDumpDatabase(database); err != nil {
		return nil, err
	}
	if err := validateDumpDatabase(opts.TargetDatabase); err != nil {
		return nil, err
	}

	stopped := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tracker := newCopyTracker(opts, "copying")
	go tracker.every(ctx, time.Second)

	reader, writer := io.Pipe()
	type dumped struct {
		summary string
		err     error
	}
	read := make(chan dumped, 1)
	go func() {
		summary, err := writeBuiltInDump(ctx, driver, sourceDSN, &countedWriter{w: writer, t: tracker}, DumpOptions{
			Database: database, SchemaOnly: opts.SchemaOnly, Progress: opts.Progress, trace: tracker.trace(),
		}, time.Now())
		// A failed dump reaches the restore as a failed read, so it stops
		// rather than committing what it had as if that were the database.
		writer.CloseWithError(err)
		read <- dumped{summary, err}
	}()

	_, loadErr := replayGenericSQL(ctx, driver, targetDSN, opts.TargetDatabase, reader, RestoreOptions{
		Database: opts.TargetDatabase, Progress: opts.Progress,
	})
	// Closing the read end is what stops a dump whose restore has already
	// failed: its next write is refused instead of waiting for a reader.
	reader.CloseWithError(loadErr)
	source := <-read

	switch {
	case stopped.Err() != nil:
		return nil, fmt.Errorf("copy stopped: %w", context.Cause(stopped))
	case source.err != nil && !tracker.writeRefused():
		return nil, fmt.Errorf("reading the source failed: %w", source.err)
	case loadErr != nil:
		return nil, fmt.Errorf("loading the target failed: %w", loadErr)
	}
	return &CopyResult{Summary: source.summary, Streamed: true, Tool: BuiltInDumpTool}, nil
}

// copyStaged is the copy for the two engines whose dump is a file: it is
// written in a directory of its own, loaded from there, and removed.
func copyStaged(ctx context.Context, driver Driver, sourceDSN, targetDSN string, opts CopyOptions) (*CopyResult, error) {
	if err := os.MkdirAll(opts.ScratchDir, 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(opts.ScratchDir, ".copy-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	tracker := newCopyTracker(opts, "dumping")
	tracker.update(func(*CopyProgress) {})
	res, err := DumpWith(ctx, driver, sourceDSN, dir, DumpOptions{Database: opts.SourceDatabase, Progress: opts.Progress})
	if err != nil {
		return nil, fmt.Errorf("reading the source failed: %w", err)
	}
	tracker.update(func(p *CopyProgress) { p.Phase, p.Bytes = "loading", res.Size })
	if _, err := RestoreWith(ctx, driver, targetDSN, res.Path, RestoreOptions{
		Database: opts.TargetDatabase, SourceDatabase: res.Database, Progress: opts.Progress,
	}); err != nil {
		return nil, fmt.Errorf("loading the target failed: %w", err)
	}
	return &CopyResult{Summary: res.Summary, Tool: res.Tool}, nil
}
