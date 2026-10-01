package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Operations: watching a server and keeping it healthy.
//
// The activity list says what is running this second and the advisor says what
// is wrong with the schema. Between them sits everything an operator would
// otherwise open a shell for: how busy the server is and whether that is
// changing, who is waiting on whom and for which table, how far a replica has
// fallen behind, which table is carrying dead rows and which index nobody
// reads, and the handful of maintenance commands that fix what those show.
//
// Every engine answers some of it and none answers it the same way, so each
// question is an optional interface a dialect implements in its own
// ops_<engine>.go, and the functions below are the one place that asks. An
// engine that has no answer reports Supported false with a sentence the page
// prints, which is a different thing from a query that failed.

// ServerStats is one cheap snapshot of a server's counters and gauges.
//
// Counters are the engine's own running totals, raw. Nothing here is a rate:
// a rate needs two readings and the time between them, and the page that
// polls this already holds both — At is the clock to divide by. Computing
// rates server-side would mean keeping the previous reading per connection
// per viewer, and would still be wrong for the first one.
type ServerStats struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
	// At is when the snapshot was read, on this server's clock.
	At      time.Time `json:"at"`
	Driver  Driver    `json:"driver"`
	Version string    `json:"version,omitempty"`
	// StartedAt is when the engine last started, where it says.
	StartedAt     *time.Time `json:"startedAt,omitempty"`
	UptimeSeconds float64    `json:"uptimeSeconds,omitempty"`
	// Role is primary, replica or standalone.
	Role          string `json:"role,omitempty"`
	Database      string `json:"database,omitempty"`
	DatabaseBytes int64  `json:"databaseBytes"`
	// Connections is the sessions the server holds, by what they are doing,
	// against the limit it was configured with.
	Connections *ConnectionCounts `json:"connections,omitempty"`
	// CountersSince is when the engine last zeroed its counters, where it
	// records that. A counter lower than the previous poll's means a reset or
	// a restart, and this is how the page tells which.
	CountersSince *time.Time `json:"countersSince,omitempty"`
	// Counters only ever grow between resets. The names shared across engines
	// are documented beside StatCounter*; the rest are the engine's own.
	Counters map[string]float64 `json:"counters"`
	// Gauges are readings of now: they go up and down.
	Gauges map[string]float64 `json:"gauges"`
	// Facts are the readings that are words rather than numbers.
	Facts map[string]string `json:"facts,omitempty"`
	// Notes names each part of the snapshot the engine refused, so a missing
	// figure reads as "not permitted" rather than as zero.
	Notes []string `json:"notes,omitempty"`
}

// ConnectionCounts is the server's sessions by state. Max is 0 where the
// engine has no configured limit.
type ConnectionCounts struct {
	Total             int `json:"total"`
	Active            int `json:"active"`
	Idle              int `json:"idle"`
	IdleInTransaction int `json:"idleInTransaction"`
	Waiting           int `json:"waiting"`
	Max               int `json:"max"`
	// Reserved is how many of Max ordinary accounts cannot use.
	Reserved int `json:"reserved,omitempty"`
}

// The counters every engine that has them reports under one name, so a chart
// drawn for one engine draws for the next.
const (
	StatTransactionsCommitted  = "transactionsCommitted"
	StatTransactionsRolledBack = "transactionsRolledBack"
	StatRowsRead               = "rowsRead"
	StatRowsWritten            = "rowsWritten"
	// StatBlocksHit and StatBlocksRead are the two halves of a cache hit
	// ratio: reads served from memory and reads that went to disk.
	StatBlocksHit  = "blocksHit"
	StatBlocksRead = "blocksRead"
	StatDeadlocks  = "deadlocks"
	StatTempFiles  = "tempFiles"
	StatTempBytes  = "tempBytes"
	StatQueries    = "queries"
)

// StatsReader is the optional dialect half of ServerStats.
type StatsReader interface {
	ServerStats(ctx context.Context, db *sql.DB) (*ServerStats, error)
}

// ReadServerStats takes one snapshot of the server behind a pool.
func ReadServerStats(ctx context.Context, db *sql.DB, driver Driver) (*ServerStats, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	reader, ok := d.(StatsReader)
	if !ok {
		return &ServerStats{
			At: time.Now().UTC(), Driver: driver,
			Counters: map[string]float64{}, Gauges: map[string]float64{},
			Reason: "this engine reports no server statistics",
		}, nil
	}
	out, err := reader.ServerStats(ctx, db)
	if err != nil {
		return nil, err
	}
	out.Supported, out.Driver = true, driver
	if out.At.IsZero() {
		out.At = time.Now().UTC()
	}
	if out.Counters == nil {
		out.Counters = map[string]float64{}
	}
	if out.Gauges == nil {
		out.Gauges = map[string]float64{}
	}
	return out, nil
}

// --- locks -------------------------------------------------------------------

// LockWait is one session waiting on another.
//
// It is a pair rather than a lock: the lock table of a busy server has
// thousands of rows and almost all of them are granted and harmless. The
// question asked under pressure is who is stuck, behind whom, on what, and
// for how long — one row per waiter and blocker.
type LockWait struct {
	WaitingPID  string `json:"waitingPid"`
	BlockingPID string `json:"blockingPid"`
	WaitingUser string `json:"waitingUser,omitempty"`
	// BlockingUser is empty when the blocker is not a session: a prepared
	// transaction holds locks with no backend behind it.
	BlockingUser  string `json:"blockingUser,omitempty"`
	WaitingQuery  string `json:"waitingQuery,omitempty"`
	BlockingQuery string `json:"blockingQuery,omitempty"`
	// BlockingState is what the blocker is doing. "idle in transaction" is the
	// usual answer and the reason the blocker's own statement explains nothing.
	BlockingState string `json:"blockingState,omitempty"`
	// Object is what is being waited for, as the engine names it.
	Object   string `json:"object,omitempty"`
	LockType string `json:"lockType,omitempty"`
	Mode     string `json:"mode,omitempty"`
	// WaitSeconds is how long the waiter has been waiting.
	WaitSeconds float64 `json:"waitSeconds"`
	// BlockingSeconds is how long the blocker's transaction has been open.
	BlockingSeconds float64 `json:"blockingSeconds,omitempty"`
}

// HeldLock is one row of the engine's lock table, granted or not.
type HeldLock struct {
	PID      string `json:"pid"`
	User     string `json:"user,omitempty"`
	LockType string `json:"lockType,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Granted  bool   `json:"granted"`
	Object   string `json:"object,omitempty"`
	State    string `json:"state,omitempty"`
	Query    string `json:"query,omitempty"`
}

// maxHeldLocks bounds the lock table. A bulk load holds a lock per row it
// touched on some engines, and a page that draws fifty thousand of them
// answers nothing the first five hundred did not.
const maxHeldLocks = 500

type LocksReport struct {
	Supported bool       `json:"supported"`
	Reason    string     `json:"reason,omitempty"`
	Waits     []LockWait `json:"waits"`
	Locks     []HeldLock `json:"locks"`
	// Truncated is true when the lock table had more rows than Locks holds.
	Truncated bool     `json:"truncated"`
	Notes     []string `json:"notes,omitempty"`
}

// LockLister is the optional dialect half of ListLocks.
type LockLister interface {
	Locks(ctx context.Context, db *sql.DB) (*LocksReport, error)
}

// ListLocks reports who is waiting on whom.
func ListLocks(ctx context.Context, db *sql.DB, driver Driver) (*LocksReport, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	l, ok := d.(LockLister)
	if !ok {
		return &LocksReport{Waits: []LockWait{}, Locks: []HeldLock{}, Reason: "this engine exposes no lock table"}, nil
	}
	out, err := l.Locks(ctx, db)
	if err != nil {
		return nil, err
	}
	if out.Waits == nil {
		out.Waits = []LockWait{}
	}
	if out.Locks == nil {
		out.Locks = []HeldLock{}
	}
	return out, nil
}

// --- replication -------------------------------------------------------------

// Replica is one server streaming from this one.
type Replica struct {
	Name      string `json:"name,omitempty"`
	Client    string `json:"client,omitempty"`
	State     string `json:"state,omitempty"`
	SyncState string `json:"syncState,omitempty"`
	// LagBytes is how much of the log the replica has not replayed; -1 where
	// the engine does not say.
	LagBytes int64 `json:"lagBytes"`
	// LagSeconds is the replay delay; -1 where the engine does not say, which
	// on PostgreSQL includes a replica that is idle and fully caught up.
	LagSeconds float64    `json:"lagSeconds"`
	SentLSN    string     `json:"sentLsn,omitempty"`
	ReplayLSN  string     `json:"replayLsn,omitempty"`
	Since      *time.Time `json:"since,omitempty"`
}

// ReplicationSlot is a position the server keeps log for.
//
// An inactive slot is the classic quiet outage: nothing reads it, the server
// keeps every segment since it was last read, and the disk fills.
type ReplicationSlot struct {
	Name      string `json:"name"`
	Type      string `json:"type,omitempty"`
	Plugin    string `json:"plugin,omitempty"`
	Database  string `json:"database,omitempty"`
	Active    bool   `json:"active"`
	Temporary bool   `json:"temporary,omitempty"`
	// RetainedBytes is the log held back for this slot; -1 when unknown.
	RetainedBytes int64  `json:"retainedBytes"`
	WalStatus     string `json:"walStatus,omitempty"`
}

type Publication struct {
	Name      string `json:"name"`
	Owner     string `json:"owner,omitempty"`
	AllTables bool   `json:"allTables"`
	Insert    bool   `json:"insert"`
	Update    bool   `json:"update"`
	Delete    bool   `json:"delete"`
	Truncate  bool   `json:"truncate"`
	Tables    int    `json:"tables"`
}

type Subscription struct {
	Name         string     `json:"name"`
	Owner        string     `json:"owner,omitempty"`
	Enabled      bool       `json:"enabled"`
	Publications []string   `json:"publications"`
	Running      bool       `json:"running"`
	ReceivedLSN  string     `json:"receivedLsn,omitempty"`
	LastMessage  *time.Time `json:"lastMessage,omitempty"`
}

// ReplicaSource is this server's own side of replication when it is the one
// following: where it reads from and how far behind it is.
type ReplicaSource struct {
	// Channel names the stream on a server that follows several.
	Channel string `json:"channel,omitempty"`
	Host    string `json:"host,omitempty"`
	Port    string `json:"port,omitempty"`
	User    string `json:"user,omitempty"`
	State   string `json:"state,omitempty"`
	// IORunning and SQLRunning are the two threads MySQL replication runs on;
	// PostgreSQL has one receiver and reports it under IORunning.
	IORunning  bool `json:"ioRunning"`
	SQLRunning bool `json:"sqlRunning"`
	// LagSeconds is -1 when the engine does not know, which on MySQL is what
	// a stopped thread reports.
	LagSeconds float64 `json:"lagSeconds"`
	LastError  string  `json:"lastError,omitempty"`
	Position   string  `json:"position,omitempty"`
	GTID       string  `json:"gtid,omitempty"`
}

type ReplicationReport struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
	// Role is primary, replica or standalone.
	Role          string            `json:"role"`
	Replicas      []Replica         `json:"replicas"`
	Slots         []ReplicationSlot `json:"slots"`
	Publications  []Publication     `json:"publications"`
	Subscriptions []Subscription    `json:"subscriptions"`
	Sources       []ReplicaSource   `json:"sources"`
	// Facts are the engine's own settings that decide what replication can
	// do here: wal_level, log_bin, server_id, gtid_mode.
	Facts map[string]string `json:"facts,omitempty"`
	Notes []string          `json:"notes,omitempty"`
}

// ReplicationReader is the optional dialect half of ReadReplication.
type ReplicationReader interface {
	Replication(ctx context.Context, db *sql.DB) (*ReplicationReport, error)
}

// ReadReplication reports the server's place in a replication topology.
func ReadReplication(ctx context.Context, db *sql.DB, driver Driver) (*ReplicationReport, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	out := &ReplicationReport{Reason: "this engine's replication is not read from here"}
	if reader, ok := d.(ReplicationReader); ok {
		out, err = reader.Replication(ctx, db)
		if err != nil {
			return nil, err
		}
		out.Supported = true
	}
	if out.Replicas == nil {
		out.Replicas = []Replica{}
	}
	if out.Slots == nil {
		out.Slots = []ReplicationSlot{}
	}
	if out.Publications == nil {
		out.Publications = []Publication{}
	}
	if out.Subscriptions == nil {
		out.Subscriptions = []Subscription{}
	}
	if out.Sources == nil {
		out.Sources = []ReplicaSource{}
	}
	return out, nil
}

// --- table and index statistics ------------------------------------------------

// TableStat is what a table costs and how it is used.
//
// A count the engine does not keep is -1, never 0: zero sequential scans is a
// finding, "this engine does not count scans" is not.
type TableStat struct {
	Schema string `json:"schema"`
	Table  string `json:"table"`
	// Kind is table, partitioned table or materialized view.
	Kind string `json:"kind,omitempty"`
	// Engine is the storage engine, on the servers that have more than one.
	Engine     string `json:"engine,omitempty"`
	Rows       int64  `json:"rows"`
	DeadRows   int64  `json:"deadRows"`
	TotalBytes int64  `json:"totalBytes"`
	TableBytes int64  `json:"tableBytes"`
	IndexBytes int64  `json:"indexBytes"`
	// ToastBytes is the out-of-line storage for large values (PostgreSQL).
	ToastBytes int64 `json:"toastBytes"`
	// BloatBytes estimates space the table holds and does not need; -1 where
	// it cannot be derived. It is an estimate from the planner's statistics,
	// not a measurement, and the page says so.
	BloatBytes      int64      `json:"bloatBytes"`
	SeqScans        int64      `json:"seqScans"`
	SeqRowsRead     int64      `json:"seqRowsRead"`
	IndexScans      int64      `json:"indexScans"`
	IndexRowsRead   int64      `json:"indexRowsRead"`
	Inserts         int64      `json:"inserts"`
	Updates         int64      `json:"updates"`
	Deletes         int64      `json:"deletes"`
	ModsSinceStats  int64      `json:"modsSinceAnalyze"`
	LastVacuum      *time.Time `json:"lastVacuum,omitempty"`
	LastAutovacuum  *time.Time `json:"lastAutovacuum,omitempty"`
	LastAnalyze     *time.Time `json:"lastAnalyze,omitempty"`
	LastAutoanalyze *time.Time `json:"lastAutoanalyze,omitempty"`
	// Parts is the number of active data parts (ClickHouse).
	Parts int64 `json:"parts,omitempty"`
	// UncompressedBytes is the size before compression (ClickHouse).
	UncompressedBytes int64 `json:"uncompressedBytes,omitempty"`
}

type TableStatsReport struct {
	Supported bool        `json:"supported"`
	Reason    string      `json:"reason,omitempty"`
	Schema    string      `json:"schema"`
	Tables    []TableStat `json:"tables"`
	// Truncated is true when the schema holds more tables than Tables does;
	// the ones kept are the largest.
	Truncated bool     `json:"truncated"`
	Notes     []string `json:"notes,omitempty"`
}

// IndexStat is what an index costs and whether anything reads it.
type IndexStat struct {
	Schema  string   `json:"schema"`
	Table   string   `json:"table"`
	Name    string   `json:"name"`
	Method  string   `json:"method,omitempty"`
	Columns []string `json:"columns"`
	// Definition is the engine's own CREATE INDEX text where it has one.
	Definition string `json:"definition,omitempty"`
	Unique     bool   `json:"unique"`
	Primary    bool   `json:"primary"`
	// Valid is false for an index a failed concurrent build left behind: it
	// costs every write and answers no query.
	Valid bool `json:"valid"`
	// Constraint is true when the index enforces a constraint, so dropping it
	// is dropping the constraint.
	Constraint bool  `json:"constraint,omitempty"`
	Bytes      int64 `json:"bytes"`
	// Scans is how many times the index was used; -1 where the engine does
	// not count.
	Scans    int64 `json:"scans"`
	RowsRead int64 `json:"rowsRead"`
	// Unused is true when the engine counts scans, counted none, and the
	// index enforces nothing.
	Unused bool `json:"unused"`
	// DuplicateOf names another index on the same table with the same
	// definition. CoveredBy names a wider index whose leading columns are
	// exactly this one's, which serves every query this one can.
	DuplicateOf string `json:"duplicateOf,omitempty"`
	CoveredBy   string `json:"coveredBy,omitempty"`
	// signature is what makes two indexes the same index, in the engine's
	// own terms; indexes without one are compared by their column lists.
	signature string
	// plain is true for an index whose columns are all there is to it: no
	// expression, no predicate. Only those can be covered by a wider one.
	plain bool
}

type IndexStatsReport struct {
	Supported bool        `json:"supported"`
	Reason    string      `json:"reason,omitempty"`
	Schema    string      `json:"schema"`
	Indexes   []IndexStat `json:"indexes"`
	Truncated bool        `json:"truncated"`
	Notes     []string    `json:"notes,omitempty"`
}

// StatsOptions bounds a table or index statistics read.
type StatsOptions struct {
	// Schema is the schema to read; empty means every schema that is not the
	// engine's own.
	Schema string
	// Table narrows index statistics to one table.
	Table string
	Limit int
}

// The bounds on a statistics read. A schema of ten thousand tables is real,
// and every row here costs the engine a size function or a statistics lookup.
const (
	defaultStatsLimit = 200
	maxStatsLimit     = 1000
)

func (o StatsOptions) limit() int {
	if o.Limit <= 0 {
		return defaultStatsLimit
	}
	if o.Limit > maxStatsLimit {
		return maxStatsLimit
	}
	return o.Limit
}

// TableStatser and IndexStatser are the optional dialect halves.
type TableStatser interface {
	TableStats(ctx context.Context, db *sql.DB, opts StatsOptions) (*TableStatsReport, error)
}

type IndexStatser interface {
	IndexStats(ctx context.Context, db *sql.DB, opts StatsOptions) (*IndexStatsReport, error)
}

// ReadTableStats reports per-table size and use, largest first.
func ReadTableStats(ctx context.Context, db *sql.DB, driver Driver, opts StatsOptions) (*TableStatsReport, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	t, ok := d.(TableStatser)
	if !ok {
		return &TableStatsReport{Schema: opts.Schema, Tables: []TableStat{}, Reason: "this engine keeps no table statistics"}, nil
	}
	out, err := t.TableStats(ctx, db, opts)
	if err != nil {
		return nil, err
	}
	out.Supported = true
	if out.Tables == nil {
		out.Tables = []TableStat{}
	}
	// One more row than the limit is how a dialect says there were more.
	if limit := opts.limit(); len(out.Tables) > limit {
		out.Tables, out.Truncated = out.Tables[:limit], true
	}
	return out, nil
}

// ReadIndexStats reports per-index size and use, largest first, with the
// duplicates and the unused ones marked.
func ReadIndexStats(ctx context.Context, db *sql.DB, driver Driver, opts StatsOptions) (*IndexStatsReport, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	ix, ok := d.(IndexStatser)
	if !ok {
		return &IndexStatsReport{Schema: opts.Schema, Indexes: []IndexStat{}, Reason: "this engine keeps no index statistics"}, nil
	}
	out, err := ix.IndexStats(ctx, db, opts)
	if err != nil {
		return nil, err
	}
	out.Supported = true
	if out.Indexes == nil {
		out.Indexes = []IndexStat{}
	}
	// Marked before the list is cut, so an index beyond the limit can still
	// be the wider one that covers one of those kept.
	markRedundantIndexes(out.Indexes)
	if limit := opts.limit(); len(out.Indexes) > limit {
		out.Indexes, out.Truncated = out.Indexes[:limit], true
	}
	return out, nil
}

// markRedundantIndexes fills DuplicateOf, CoveredBy and Unused.
//
// Two indexes are duplicates when the engine would build the same thing for
// both: same table, same signature. Of a set, the one that enforces a
// constraint is the one kept, then the unique one, then the first by name —
// so the copy named is always the one that can be dropped. An index is covered
// when a wider one starts with exactly its columns in order; a unique index is
// never covered, because the narrower uniqueness is a rule the wider index
// does not enforce.
func markRedundantIndexes(list []IndexStat) {
	byTable := map[string][]int{}
	for i := range list {
		ix := &list[i]
		ix.Unused = ix.Scans == 0 && !ix.Unique && !ix.Primary && !ix.Constraint
		byTable[ix.Schema+"\x00"+ix.Table] = append(byTable[ix.Schema+"\x00"+ix.Table], i)
	}
	for _, members := range byTable {
		groups := map[string][]int{}
		for _, i := range members {
			sig := list[i].signature
			if sig == "" {
				sig = indexSignature(list[i])
			}
			groups[sig] = append(groups[sig], i)
		}
		for _, group := range groups {
			if len(group) < 2 {
				continue
			}
			sort.SliceStable(group, func(a, b int) bool {
				x, y := list[group[a]], list[group[b]]
				if keepRank(x) != keepRank(y) {
					return keepRank(x) < keepRank(y)
				}
				return x.Name < y.Name
			})
			for _, i := range group[1:] {
				list[i].DuplicateOf = list[group[0]].Name
			}
		}
		for _, i := range members {
			narrow := &list[i]
			if narrow.DuplicateOf != "" || narrow.Unique || narrow.Primary || narrow.Constraint ||
				!narrow.plain || len(narrow.Columns) == 0 {
				continue
			}
			for _, j := range members {
				wide := list[j]
				if i == j || !wide.plain || wide.DuplicateOf != "" || !wide.Valid ||
					!strings.EqualFold(wide.Method, narrow.Method) || len(wide.Columns) <= len(narrow.Columns) {
					continue
				}
				if columnsPrefix(narrow.Columns, wide.Columns) {
					narrow.CoveredBy = wide.Name
					break
				}
			}
		}
	}
}

// sortIndexStatsBySize orders a list largest first, for the dialects whose
// catalogue query cannot: the sizes come from a second, optional read.
func sortIndexStatsBySize(list []IndexStat) {
	sort.SliceStable(list, func(i, j int) bool { return list[i].Bytes > list[j].Bytes })
}

// maxIndexCatalogue bounds how many indexes those dialects read before
// ranking them. They have to read the whole list first: cutting it at the
// limit in catalogue order and sorting what is left by size shows the first
// two hundred indexes by name and calls them the largest. Twenty thousand is
// past any schema this page is useful for and still a bounded read.
const maxIndexCatalogue = 20000

// indexCatalogueNote is what a report says when even that bound was reached.
func indexCatalogueNote() string {
	return fmt.Sprintf("This database has more than %d indexes; only the first %d by name were ranked by size.",
		maxIndexCatalogue, maxIndexCatalogue)
}

// keepRank orders a set of duplicates by which one must stay.
func keepRank(ix IndexStat) int {
	switch {
	case ix.Primary:
		return 0
	case ix.Constraint:
		return 1
	case ix.Unique:
		return 2
	}
	return 3
}

// indexSignature is the comparison used where the engine offers none of its
// own: the method and the ordered column list. Uniqueness is left out on
// purpose — a plain index on the columns a unique one already covers is the
// duplicate, and keepRank is what decides which of the two stays.
func indexSignature(ix IndexStat) string {
	return strings.ToLower(ix.Method) + "|" + strings.ToLower(strings.Join(ix.Columns, "\x1f"))
}

func columnsPrefix(narrow, wide []string) bool {
	for i, c := range narrow {
		if !strings.EqualFold(c, wide[i]) {
			return false
		}
	}
	return true
}

// --- maintenance -------------------------------------------------------------

// MaintenanceAction describes one command the server can be asked to run on
// itself. The set is closed per engine: the request names an action, never a
// statement.
type MaintenanceAction struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	// Scope says what the action can be aimed at: "table" needs one,
	// "database" takes none, "either" runs on the table when given and on
	// the whole database when not.
	Scope string `json:"scope"`
	// Blocking marks an action that locks what it works on for as long as it
	// runs, which on a big table is the application waiting.
	Blocking bool `json:"blocking,omitempty"`
	// Destructive marks an action that can discard data it cannot recover.
	Destructive bool `json:"destructive,omitempty"`
	// Requires is the capability the route asks for before it runs the
	// action: "destructive" for one that blocks or can lose data, and
	// "service.control" for the rest. It is published so the page hides what
	// the viewer cannot run by the same rule the route refuses it by.
	Requires string `json:"requires"`
	// Options names the request options this action reads.
	Options []string `json:"options,omitempty"`
}

// The two capabilities a maintenance action can ask for, in the words the
// session's capability list uses.
const (
	MaintenanceNeedsControl     = "service.control"
	MaintenanceNeedsDestructive = "destructive"
)

// NeedsDestructive reports whether running the action takes the destructive
// capability rather than the ordinary one.
//
// The SQL console treats every statement that is not a recognised read as
// destructive, because it cannot know what a statement does. This list can:
// an action here is one of a closed set and its effect is written beside it.
// So the line is drawn on the effect. An action that can lose rows, or that
// locks a table against the application for as long as it runs — and, with no
// table named, every table in turn — is the same act as typing it into the
// console and takes what the console takes. One that reads, or that works
// alongside the application's reads and writes, is routine upkeep.
func (a MaintenanceAction) NeedsDestructive() bool {
	return a.Destructive || a.Blocking
}

// MaintenanceOptions are the few switches the actions take. Each is a closed
// choice; none carries SQL.
type MaintenanceOptions struct {
	// Concurrently rebuilds an index without blocking writes (PostgreSQL 12+).
	Concurrently bool `json:"concurrently,omitempty"`
	// Mode is the WAL checkpoint mode: passive, full, restart or truncate.
	Mode string `json:"mode,omitempty"`
	// Final forces a merge to a single part per partition (ClickHouse).
	Final bool `json:"final,omitempty"`
}

type MaintenanceRequest struct {
	Action string
	Schema string
	Table  string
	// Index aims a reindex at one index rather than a table's whole set.
	Index   string
	Options MaintenanceOptions
}

// MaintenanceResult is what ran and what the engine said about it.
type MaintenanceResult struct {
	Action string `json:"action"`
	// Statements is the SQL that ran, in order.
	Statements []string `json:"statements"`
	// Output is the engine's own lines: VACUUM VERBOSE's notices, CHECK
	// TABLE's rows, integrity_check's verdict.
	Output []string `json:"output"`
	// OutputTruncated is true when the engine printed more than is kept. The
	// last line of Output then says so.
	OutputTruncated bool   `json:"outputTruncated"`
	Duration        string `json:"duration"`
	// OK is false when the engine ran the action and reported a problem in
	// its output rather than as an error: a CHECK TABLE that found
	// corruption, an integrity_check that did not say "ok".
	OK bool `json:"ok"`
}

// keep adds the engine's lines to the result up to the bound and records that
// there were more. The bound is applied as the lines arrive, not afterwards:
// a foreign key check answers a row per orphan and a verbose vacuum of a whole
// database several lines per table, and collecting all of that in order to
// throw most of it away is the dashboard holding millions of strings for one
// request.
func (r *MaintenanceResult) keep(lines ...string) {
	for _, line := range lines {
		if r.full() {
			r.OutputTruncated = true
			return
		}
		r.Output = append(r.Output, line)
	}
}

// full reports whether the result holds as many lines as it keeps.
func (r *MaintenanceResult) full() bool { return len(r.Output) >= maxMaintenanceLines }

// Maintainer is the optional dialect half. The DSN is passed beside the pool
// because PostgreSQL reports a maintenance command's progress as notices, and
// those are only readable on a connection opened to listen for them.
type Maintainer interface {
	MaintenanceActions() []MaintenanceAction
	Maintain(ctx context.Context, db *sql.DB, dsn string, req MaintenanceRequest) (*MaintenanceResult, error)
}

// MaintenanceActionsFor is the closed set of actions an engine offers; empty
// for an engine that has none.
func MaintenanceActionsFor(driver Driver) []MaintenanceAction {
	d, err := DialectFor(driver)
	if err != nil {
		return []MaintenanceAction{}
	}
	m, ok := d.(Maintainer)
	if !ok {
		return []MaintenanceAction{}
	}
	actions := m.MaintenanceActions()
	for i := range actions {
		actions[i].Requires = MaintenanceNeedsControl
		if actions[i].NeedsDestructive() {
			actions[i].Requires = MaintenanceNeedsDestructive
		}
	}
	return actions
}

// MaintenanceActionFor looks one action up by id.
func MaintenanceActionFor(driver Driver, id string) (MaintenanceAction, bool) {
	for _, a := range MaintenanceActionsFor(driver) {
		if a.ID == id {
			return a, true
		}
	}
	return MaintenanceAction{}, false
}

// ErrMaintenanceRequest marks a refusal that is about the request rather than
// the server: an action this engine does not have, a table where none is
// taken. The handler answers those 400 and everything else 502.
type ErrMaintenanceRequest struct{ msg string }

func (e ErrMaintenanceRequest) Error() string { return e.msg }

func maintenanceRefused(format string, args ...any) error {
	return ErrMaintenanceRequest{msg: fmt.Sprintf(format, args...)}
}

// RunMaintenance runs one closed maintenance action and returns what the
// engine printed.
func RunMaintenance(ctx context.Context, db *sql.DB, driver Driver, dsn string, req MaintenanceRequest) (*MaintenanceResult, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	m, ok := d.(Maintainer)
	if !ok {
		return nil, maintenanceRefused("%s has no maintenance actions from here", driver)
	}
	action, ok := MaintenanceActionFor(driver, req.Action)
	if !ok {
		ids := []string{}
		for _, a := range m.MaintenanceActions() {
			ids = append(ids, a.ID)
		}
		return nil, maintenanceRefused("action must be one of %s", strings.Join(ids, ", "))
	}
	req.Schema, req.Table, req.Index = strings.TrimSpace(req.Schema), strings.TrimSpace(req.Table), strings.TrimSpace(req.Index)
	switch action.Scope {
	case "table":
		if req.Table == "" {
			return nil, maintenanceRefused("%s needs a table", action.ID)
		}
	case "database":
		if req.Table != "" || req.Index != "" {
			return nil, maintenanceRefused("%s runs on the whole database and takes no table", action.ID)
		}
	}
	start := time.Now()
	out, err := m.Maintain(ctx, db, dsn, req)
	if err != nil {
		return nil, err
	}
	out.Action = action.ID
	out.Duration = time.Since(start).Round(time.Millisecond).String()
	if out.Statements == nil {
		out.Statements = []string{}
	}
	if out.Output == nil {
		out.Output = []string{}
	}
	if out.OutputTruncated {
		out.Output = append(out.Output, fmt.Sprintf("… the engine printed more; the first %d lines are kept", maxMaintenanceLines))
	}
	return out, nil
}

// maxMaintenanceLines bounds what an action prints. A verbose ANALYZE of a
// whole database is two lines per table, and a badly damaged file reports a
// line per bad page; the first five hundred say all the rest do.
const maxMaintenanceLines = 500

// --- what an engine has --------------------------------------------------------

// OpsCapabilities reports which of the operations surfaces an engine has,
// under the names the driver catalogue publishes them by.
//
// It is derived from the interfaces each dialect implements rather than
// written out, so a surface added to an engine appears here by being added
// and cannot be forgotten. The flavour only ever takes something away: a
// fork that speaks an engine's protocol without having its statistics views
// answers the catalogue queries with errors, and a page that offers the tab
// and then fails is worse than one that does not offer it. The forks named
// here have not been run against; they are switched off on what their own
// documentation says they lack, and everything else is left as the engine's.
func OpsCapabilities(driver Driver, flavor string) map[string]bool {
	out := map[string]bool{}
	d, err := DialectFor(driver)
	if err != nil {
		return out
	}
	_, out["stats"] = d.(StatsReader)
	_, out["cancel"] = d.(Canceller)
	_, out["locks"] = d.(LockLister)
	_, out["replication"] = d.(ReplicationReader)
	_, out["tableStats"] = d.(TableStatser)
	_, out["indexStats"] = d.(IndexStatser)
	_, out["maintenance"] = d.(Maintainer)
	_, out["settings"] = d.(SettingsLister)
	_, out["settingsWrite"] = d.(SettingsWriter)
	_, out["roles"] = d.(Admin)
	_, out["privileges"] = d.(PrivilegeAdmin)
	_, out["statements"] = d.(StatementStatser)
	_, out["statementsReset"] = d.(StatementResetter)
	_, out["engineAdvisor"] = d.(Adviser)
	// Every SQL engine gets the structure checks; SQLite alone has no
	// sessions to list or end, and says so through the same interface.
	out["advisor"] = true
	out["sessions"] = driver != DriverSQLite
	out["kill"] = driver != DriverSQLite
	out["cancel"] = out["cancel"] && driver != DriverSQLite
	out["clickhouseViews"] = driver == DriverClickHouse
	out["sqliteFile"] = driver == DriverSQLite

	off := func(names ...string) {
		for _, n := range names {
			out[n] = false
		}
	}
	switch flavor {
	case "cockroachdb":
		// No statistics collector, no VACUUM, no ALTER SYSTEM, no
		// pg_stat_statements; its own equivalents are crdb_internal tables.
		off("stats", "locks", "replication", "tableStats", "indexStats", "maintenance", "settingsWrite",
			"statements", "statementsReset", "engineAdvisor", "privileges")
	case "tidb":
		// No InnoDB and no performance_schema digests behind the MySQL wire.
		off("stats", "locks", "replication", "tableStats", "indexStats", "maintenance", "settingsWrite",
			"statements", "statementsReset", "engineAdvisor")
	}
	return out
}

// --- shared scanning ---------------------------------------------------------

// rowMaps reads a result whose columns are not known ahead of time into one
// map per row, every value as text.
//
// The replication commands are the reason: SHOW REPLICA STATUS has fifty-odd
// columns, MariaDB spells half of them differently from MySQL, and each
// release adds some. Scanning by position would break on every one of those;
// asking for a column by either of its names does not.
func rowMaps(rows *sql.Rows) ([]map[string]string, error) {
	out, _, err := rowMapsUpTo(rows, 0)
	return out, err
}

// rowMapsUpTo is rowMaps for a result whose length the caller does not
// control: it stops after limit rows and reports whether there were more,
// leaving them unread. A limit of zero reads everything.
func rowMapsUpTo(rows *sql.Rows, limit int) ([]map[string]string, bool, error) {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, false, err
	}
	out := []map[string]string{}
	for rows.Next() {
		if limit > 0 && len(out) == limit {
			return out, true, nil
		}
		cells := make([]sql.NullString, len(cols))
		dest := make([]any, len(cols))
		for i := range cells {
			dest[i] = &cells[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, false, err
		}
		row := make(map[string]string, len(cols))
		for i, c := range cols {
			row[c] = cells[i].String
		}
		out = append(out, row)
	}
	return out, false, rows.Err()
}

// firstOf returns the first of several spellings a row has a value under.
func firstOf(row map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := row[k]; ok && v != "" {
			return v
		}
	}
	return ""
}

// timePtr turns a nullable timestamp into the pointer JSON omits when empty.
func timePtr(t sql.NullTime) *time.Time {
	if !t.Valid || t.Time.IsZero() {
		return nil
	}
	u := t.Time.UTC()
	return &u
}

// qualifiedName renders schema.table for display, without the dot when there
// is no schema.
func qualifiedName(schema, table string) string {
	if schema == "" {
		return table
	}
	return schema + "." + table
}
