package dbx

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-sql-driver/mysql"
)

// queryer is what a statement runs on: the pool, one pinned connection, or a
// transaction on one.
type queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

const (
	defaultMaxRows = 500
	// MaxResultRows is the most rows one result carries. A browser table is not
	// where a million-row result belongs; the export is.
	MaxResultRows = 5000
)

// clampRows turns a requested row cap into the one that applies. Asking for
// more than the maximum gets the maximum — it used to get the default, so a
// request for 6000 rows came back with 500 and no way to tell why.
func clampRows(n, def, max int) int {
	switch {
	case n <= 0:
		return def
	case n > max:
		return max
	}
	return n
}

// RunQuery executes a statement and materialises the result set. Rows beyond
// maxRows are dropped and flagged rather than streamed.
//
// It is for statements this package generated. An operator's own statement
// goes through RunStatement, which knows the engine and what the statement was
// classified as.
func RunQuery(ctx context.Context, db *sql.DB, query string, maxRows int, args ...any) (*QueryResult, error) {
	return runOn(ctx, db, "", query, returnsRows(query), collectOptions{
		maxRows: clampRows(maxRows, defaultMaxRows, MaxResultRows),
	}, args...)
}

type collectOptions struct {
	maxRows int
	// clipText cuts text cells to this many bytes and records them as clipped.
	// 0 leaves text whole.
	clipText int
}

func runOn(ctx context.Context, q queryer, driver Driver, query string, rowsBack bool, opts collectOptions, args ...any) (*QueryResult, error) {
	args, err := sqlArguments(args)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	if !rowsBack {
		exec, err := q.ExecContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		res := &QueryResult{Columns: []string{}, Types: []string{}, Rows: [][]any{}, Statement: query}
		res.Affected, _ = exec.RowsAffected()
		res.Duration = time.Since(start).Round(time.Microsecond).String()
		return res, nil
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	res, err := collect(rows, driver, query, opts)
	if err != nil {
		return nil, err
	}
	res.Duration = time.Since(start).Round(time.Microsecond).String()
	return res, nil
}

// collectRows materialises a result set. It is separate from RunQuery because
// the plan commands on SQL Server and Oracle have to run on a connection they
// hold themselves, and would otherwise duplicate this loop.
func collectRows(rows *sql.Rows, maxRows int, statement string) (*QueryResult, error) {
	return collect(rows, "", statement, collectOptions{maxRows: maxRows})
}

func collect(rows *sql.Rows, driver Driver, statement string, opts collectOptions) (*QueryResult, error) {
	res := &QueryResult{Columns: []string{}, Types: []string{}, Rows: [][]any{}, Statement: statement}
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	res.Columns = cols
	kinds := make([]string, len(cols))
	if types, err := rows.ColumnTypes(); err == nil && len(types) == len(cols) {
		for i, t := range types {
			res.Types = append(res.Types, t.DatabaseTypeName())
			kinds[i] = ValueKind(driver, t.DatabaseTypeName())
		}
		res.Kinds = kinds
	}
	typeName := func(i int) string {
		if i < len(res.Types) {
			return res.Types[i]
		}
		return ""
	}
	for rows.Next() {
		if len(res.Rows) >= opts.maxRows {
			res.Truncated = true
			break
		}
		holders := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range holders {
			ptrs[i] = &holders[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]any, len(cols))
		for i, v := range holders {
			if driver == "" {
				row[i] = normaliseValue(v)
				continue
			}
			var size int64
			row[i], size = encodeCell(driver, kinds[i], typeName(i), v, opts.clipText)
			if size > 0 {
				res.Clipped = append(res.Clipped, ClippedCell{Row: len(res.Rows), Column: i, Size: size})
			}
		}
		res.Rows = append(res.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	res.RowCount = len(res.Rows)
	return res, nil
}

// readScope is how an engine is made to refuse writes for the length of one
// statement.
//
// The classifier decides who may run a statement. This decides what happens
// when the classifier is wrong: a statement it called a read runs where the
// engine itself will not let it write, so a wrong verdict becomes an error
// message instead of a changed database.
type readScope uint8

const (
	// readScopeTransaction: a read-only transaction, rolled back afterwards.
	readScopeTransaction readScope = iota
	// readScopeRollback: the engine has no read-only transaction, so an
	// ordinary one is rolled back. Weaker — a write succeeds and is undone,
	// and anything not transactional is not undone at all.
	readScopeRollback
	// readScopeOracle: SET TRANSACTION READ ONLY as the first statement of a
	// transaction, which the driver cannot ask for itself.
	readScopeOracle
	// readScopeSession: the connection itself is switched to refuse writes for
	// the statement and switched back after (sessionScoper says how).
	readScopeSession
	// readScopeSetting: ClickHouse's readonly setting, sent with the query.
	readScopeSetting
)

// sessionScoper is implemented by the engines whose read-only scope is a state
// of the connection rather than a transaction.
type sessionScoper interface {
	// enterRead makes conn refuse writes and returns what puts it back as it
	// was. A connection that cannot be put back is closed, not pooled.
	enterRead(ctx context.Context, conn *sql.Conn) (leave func(context.Context) error, err error)
}

// session is one connection held for the length of an operator's request.
//
// A pooled connection handed back after an operator's statement carries
// whatever that statement left on it — an open transaction, a search_path, a
// role, a temporary table — into the next request that happens to draw it,
// which may be another person's. So a session that ran anything but a read is
// closed rather than returned.
type session struct {
	db      *sql.DB
	conn    *sql.Conn
	dialect Dialect
	dirty   bool
}

func openSession(ctx context.Context, db *sql.DB, driver Driver) (*session, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	return &session{db: db, conn: conn, dialect: d}, nil
}

// close gives the connection back, or throws it away when it may carry state.
func (s *session) close() {
	if s.dirty {
		discardConn(s.conn)
	}
	_ = s.conn.Close()
}

// discardConn makes database/sql close a connection instead of pooling it.
func discardConn(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
}

// canceller is implemented by the dialects whose driver abandons a cancelled
// statement instead of stopping it: the client stops waiting, and the server
// keeps running the query until it has something to send to a socket nobody
// reads. For those the server has to be told separately. (PostgreSQL's driver
// sends the cancel request itself, SQL Server's sends an attention, ClickHouse's
// a cancel packet and SQLite's interrupts the engine; MySQL's only hangs up.)
type canceller interface {
	// cancelFunc returns what stops the statement currently running on conn.
	// It is called before the statement starts, on the same connection.
	cancelFunc(ctx context.Context, db *sql.DB, conn *sql.Conn) (func(), error)
}

// watch arranges for the statement about to run on the session to be stopped
// on the server when ctx ends first. The returned function is called once the
// statement has returned; after it, no cancellation can still be sent.
func (s *session) watch(ctx context.Context) func() {
	c, ok := s.dialect.(canceller)
	if !ok {
		return func() {}
	}
	stopServer, err := c.cancelFunc(ctx, s.db, s.conn)
	if err != nil || stopServer == nil {
		return func() {}
	}
	var finished atomic.Bool
	var running sync.WaitGroup
	running.Add(1)
	stop := context.AfterFunc(ctx, func() {
		defer running.Done()
		if !finished.Load() {
			stopServer()
		}
	})
	return func() {
		finished.Store(true)
		if stop() {
			return
		}
		// The cancellation already started. Waiting for it means a late one
		// can never land on this connection's next statement.
		running.Wait()
	}
}

// run executes one statement on the session's connection.
func (s *session) run(ctx context.Context, q queryer, st *SQLStatement, maxRows int) (*QueryResult, error) {
	done := s.watch(ctx)
	defer done()
	return runOn(ctx, q, s.dialect.Driver(), st.SQL, st.returnsRows, collectOptions{
		maxRows: clampRows(maxRows, defaultMaxRows, MaxResultRows),
	})
}

// read runs fn inside the engine's read-only scope. fn receives what to run
// its statements on.
func (s *session) read(ctx context.Context, fn func(ctx context.Context, q queryer) error) error {
	switch s.dialect.readScope() {
	case readScopeSession:
		leave, err := s.dialect.(sessionScoper).enterRead(ctx, s.conn)
		if err != nil {
			return err
		}
		defer func() {
			// The caller's context may be the reason the statement stopped, and
			// a connection left refusing writes would refuse everybody's.
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := leave(cleanup); err != nil {
				s.dirty = true
			}
		}()
		return fn(ctx, s.conn)
	case readScopeSetting:
		scoped, err := clickhouseReadScope(ctx, s.conn)
		if err != nil {
			return err
		}
		return fn(scoped, s.conn)
	}
	options := &sql.TxOptions{ReadOnly: s.dialect.readScope() == readScopeTransaction}
	tx, err := s.conn.BeginTx(ctx, options)
	if err != nil {
		return err
	}
	// Always rolled back, never committed: there is nothing to keep, and a
	// rollback also undoes a session setting changed from inside a function.
	defer func() { _ = tx.Rollback() }()
	if s.dialect.readScope() == readScopeOracle {
		// Refused by an account that may not, or by a driver that had already
		// started work: the rollback still stands between a write and the data.
		_, _ = tx.ExecContext(ctx, "SET TRANSACTION READ ONLY")
	}
	return fn(ctx, tx)
}

// RunStatement runs one statement an operator wrote.
//
// A statement classified as a read runs in the engine's read-only scope on a
// pooled connection. Anything else runs on a connection of its own, which is
// closed afterwards rather than handed to the next request.
func RunStatement(ctx context.Context, db *sql.DB, driver Driver, st *SQLStatement, maxRows int) (*QueryResult, error) {
	s, err := openSession(ctx, db, driver)
	if err != nil {
		return nil, err
	}
	defer s.close()
	if st.Risk.Level != "read" {
		s.dirty = true
		return s.run(ctx, s.conn, st, maxRows)
	}
	var res *QueryResult
	err = s.read(ctx, func(ctx context.Context, q queryer) error {
		var err error
		res, err = s.run(ctx, q, st, maxRows)
		return err
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// transactionWords are the statements that open, close or mark a transaction.
// A script run inside one transaction may not carry its own.
var transactionWords = map[string]bool{
	"begin": true, "start": true, "commit": true, "rollback": true, "end": true,
	"abort": true, "savepoint": true, "release": true,
}

// IsSerializationFailure reports SQLSTATE 40001: the engine aborted the
// transaction because it could not be ordered with another one, and running it
// again is the documented answer. CockroachDB reports it under ordinary
// contention, PostgreSQL under SERIALIZABLE, MySQL for a deadlock victim.
func IsSerializationFailure(err error) bool {
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		return state.SQLState() == "40001"
	}
	// MySQL's error type carries the state as a field rather than a method.
	var my *mysql.MySQLError
	if errors.As(err, &my) {
		return string(my.SQLState[:]) == "40001"
	}
	// SQL Server names its deadlock victim by number.
	var ms interface{ SQLErrorNumber() int32 }
	if errors.As(err, &ms) {
		return ms.SQLErrorNumber() == 1205
	}
	return false
}
