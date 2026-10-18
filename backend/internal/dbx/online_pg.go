package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Logical replication, the way a Postgres database is brought over from another
// host while that host keeps taking writes.
//
// The source publishes the tables, the target subscribes to the publication,
// and the subscription copies each table once and then applies every change
// that follows. The dashboard's part is the order and the checks around it: the
// structure goes over first (copy_stream.go with the rows left out), the
// publication is made from the tables both sides have, and a cutover waits for
// the target to reach the position the source was at when the writes stopped,
// puts the sequences where the source's are — replication does not carry them —
// and takes the subscription away, which takes its slot on the source with it.

// ImportNameRe is what a publication, a subscription and the slot between them
// may be called. One name serves the three, so it is what the operator reads in
// pg_stat_subscription on one server and pg_replication_slots on the other.
var ImportNameRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,49}$`)

// ReplicationTable is a table the source would publish.
type ReplicationTable struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	// Rows is the planner's estimate, -1 where it has never counted.
	Rows int64 `json:"estimatedRows"`
	Size int64 `json:"size"`
	// Identity is how an UPDATE or a DELETE on the table says which row:
	// "key" for the primary key, "index" or "full" when the table was set up
	// for it, and "none" when it has nothing — a table that refuses those
	// statements once it is published.
	Identity string `json:"identity"`
}

// Qualified is the table as the catalogue spells it.
func (t ReplicationTable) Qualified() string {
	q, _ := quoteDouble(t.Schema)
	n, _ := quoteDouble(t.Name)
	return q + "." + n
}

// ImportSourceReport is what a server says about being the source of a logical
// replication, and what stands in the way.
type ImportSourceReport struct {
	Version       string             `json:"version"`
	VersionNumber int                `json:"versionNumber"`
	Database      string             `json:"database"`
	WalLevel      string             `json:"walLevel"`
	Tables        []ReplicationTable `json:"tables"`
	Sequences     int                `json:"sequences"`
	// Blockers are what has to change on the source before it can be
	// replicated, each with the statement or the setting that changes it.
	Blockers []string `json:"blockers"`
	// Warnings are what replication does not carry or what may surprise.
	Warnings []string `json:"warnings"`
}

// Ready says whether replication can be started.
func (r *ImportSourceReport) Ready() bool { return len(r.Blockers) == 0 }

// ReplicationTarget is what a server says about being the subscriber.
type ReplicationTarget struct {
	Version       string `json:"version"`
	VersionNumber int    `json:"versionNumber"`
	// CanSubscribe is whether the login may create a subscription: a
	// superuser, or from PostgreSQL 16 a member of pg_create_subscription.
	CanSubscribe bool `json:"canSubscribe"`
}

// importSystemSchemas are the schemas no import carries.
const importSystemSchemas = `('pg_catalog', 'information_schema')`

// InspectReplicationSource reads a Postgres server as the source of a logical
// replication. It changes nothing.
func InspectReplicationSource(ctx context.Context, dsn string) (*ImportSourceReport, error) {
	db, err := openPostgres(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	report := &ImportSourceReport{Tables: []ReplicationTable{}, Blockers: []string{}, Warnings: []string{}}
	var superuser, replication bool
	var freeSlots, freeSenders int
	err = db.QueryRowContext(ctx, `
		SELECT version(), current_setting('server_version_num')::int, current_database(), current_setting('wal_level'),
		       r.rolsuper, r.rolreplication OR pg_has_role(current_user, 'rds_replication', 'member'),
		       current_setting('max_replication_slots')::int - (SELECT count(*) FROM pg_replication_slots),
		       current_setting('max_wal_senders')::int - (SELECT count(*) FROM pg_stat_replication)
		FROM pg_roles r WHERE r.rolname = current_user`).
		Scan(&report.Version, &report.VersionNumber, &report.Database, &report.WalLevel,
			&superuser, &replication, &freeSlots, &freeSenders)
	if err != nil {
		// A server without the rds_replication role answers the has_role call
		// with an error rather than false.
		err = db.QueryRowContext(ctx, `
			SELECT version(), current_setting('server_version_num')::int, current_database(), current_setting('wal_level'),
			       r.rolsuper, r.rolreplication,
			       current_setting('max_replication_slots')::int - (SELECT count(*) FROM pg_replication_slots),
			       current_setting('max_wal_senders')::int - (SELECT count(*) FROM pg_stat_replication)
			FROM pg_roles r WHERE r.rolname = current_user`).
			Scan(&report.Version, &report.VersionNumber, &report.Database, &report.WalLevel,
				&superuser, &replication, &freeSlots, &freeSenders)
	}
	if err != nil {
		return nil, err
	}

	if report.VersionNumber < 100000 {
		report.Blockers = append(report.Blockers, "Logical replication needs PostgreSQL 10 or later on the source.")
	}
	if report.WalLevel != "logical" {
		report.Blockers = append(report.Blockers, fmt.Sprintf(
			"The source's wal_level is %s. Set it to logical (ALTER SYSTEM SET wal_level = 'logical') and restart that server.",
			report.WalLevel))
	}
	if !superuser && !replication {
		report.Blockers = append(report.Blockers,
			"The source login has no REPLICATION attribute, which the subscription connects with. ALTER ROLE it WITH REPLICATION, or use a login that has it.")
	}
	if freeSlots < 1 {
		report.Blockers = append(report.Blockers, "The source has no free replication slot; raise max_replication_slots or drop one it does not use.")
	}
	if freeSenders < 1 {
		report.Blockers = append(report.Blockers, "The source has no free WAL sender; raise max_wal_senders.")
	}

	rows, err := db.QueryContext(ctx, `
		SELECT n.nspname, c.relname, c.reltuples::bigint, pg_total_relation_size(c.oid), c.relpersistence::text,
		       CASE c.relreplident
		         WHEN 'f' THEN 'full'
		         WHEN 'i' THEN 'index'
		         WHEN 'n' THEN 'none'
		         ELSE CASE WHEN EXISTS (SELECT 1 FROM pg_index i WHERE i.indrelid = c.oid AND i.indisprimary)
		                   THEN 'key' ELSE 'none' END
		       END
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'r'
		  AND n.nspname NOT IN `+importSystemSchemas+` AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp\_%'
		  AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e')
		ORDER BY n.nspname, c.relname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var unlogged, keyless []string
	for rows.Next() {
		var t ReplicationTable
		var persistence string
		if err := rows.Scan(&t.Schema, &t.Name, &t.Rows, &t.Size, &persistence, &t.Identity); err != nil {
			return nil, err
		}
		switch persistence {
		case "t":
			continue
		case "u":
			unlogged = append(unlogged, t.Schema+"."+t.Name)
			continue
		}
		if t.Identity == "none" {
			keyless = append(keyless, t.Qualified())
		}
		report.Tables = append(report.Tables, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(report.Tables) == 0 {
		report.Blockers = append(report.Blockers, "The source database has no table to replicate.")
	}
	if len(keyless) > 0 {
		// A published table with no way to name a row refuses UPDATE and DELETE
		// on the source itself, which would break the application the import
		// is meant to leave alone. So the choice is the operator's, made there.
		statements := make([]string, len(keyless))
		for i, rel := range keyless {
			statements[i] = "ALTER TABLE " + rel + " REPLICA IDENTITY FULL;"
		}
		report.Blockers = append(report.Blockers, fmt.Sprintf(
			"%d table(s) have no primary key, so the source would refuse updates and deletes on them once published. Run on the source: %s",
			len(keyless), strings.Join(statements, " ")))
	}
	if len(unlogged) > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("Unlogged tables are not replicated and arrive empty: %s.", strings.Join(unlogged, ", ")))
	}

	var largeObjects bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_largeobject_metadata)`).Scan(&largeObjects); err == nil && largeObjects {
		report.Warnings = append(report.Warnings, "The source holds large objects, which logical replication does not carry.")
	}
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'S' AND n.nspname NOT IN `+importSystemSchemas).Scan(&report.Sequences); err != nil {
		return nil, err
	}
	if report.Sequences > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%d sequence(s) are not replicated; the cutover sets them to the source's positions.", report.Sequences))
	}
	return report, nil
}

// InspectReplicationTarget reads a Postgres server as the subscriber.
func InspectReplicationTarget(ctx context.Context, dsn string) (*ReplicationTarget, error) {
	db, err := openPostgres(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	target := &ReplicationTarget{}
	var superuser bool
	if err := db.QueryRowContext(ctx, `
		SELECT version(), current_setting('server_version_num')::int, r.rolsuper
		FROM pg_roles r WHERE r.rolname = current_user`).Scan(&target.Version, &target.VersionNumber, &superuser); err != nil {
		return nil, err
	}
	target.CanSubscribe = superuser
	if !superuser && target.VersionNumber >= 160000 {
		if err := db.QueryRowContext(ctx, `SELECT pg_has_role(current_user, 'pg_create_subscription', 'member')`).Scan(&target.CanSubscribe); err != nil {
			return nil, err
		}
	}
	return target, nil
}

// PublicationSQL is the statement an operator runs on the source to publish
// these tables themselves, for a source whose login the dashboard is not given
// the right to publish with.
func PublicationSQL(name string, tables []ReplicationTable) string {
	q, _ := quoteDouble(name)
	if len(tables) == 0 {
		return "CREATE PUBLICATION " + q + " FOR ALL TABLES;"
	}
	rels := make([]string, len(tables))
	for i, t := range tables {
		rels[i] = t.Qualified()
	}
	return "CREATE PUBLICATION " + q + " FOR TABLE " + strings.Join(rels, ", ") + ";"
}

// CreatePublication publishes the tables on the source.
func CreatePublication(ctx context.Context, dsn, name string, tables []ReplicationTable) error {
	if !ImportNameRe.MatchString(name) {
		return fmt.Errorf("%q is not a name a publication can have here", name)
	}
	if len(tables) == 0 {
		return fmt.Errorf("a publication needs at least one table")
	}
	db, err := openPostgres(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, strings.TrimSuffix(PublicationSQL(name, tables), ";"))
	return err
}

// DropPublication removes a publication from the source. One that is not there
// is already what was asked.
func DropPublication(ctx context.Context, dsn, name string) error {
	q, err := quoteDouble(name)
	if err != nil {
		return err
	}
	db, err := openPostgres(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, "DROP PUBLICATION IF EXISTS "+q)
	return err
}

// PublicationExists says whether the source has a publication of this name, and
// how many tables it publishes.
func PublicationExists(ctx context.Context, dsn, name string) (bool, int, error) {
	db, err := openPostgres(ctx, dsn)
	if err != nil {
		return false, 0, err
	}
	defer db.Close()
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = $1)`, name).Scan(&exists); err != nil || !exists {
		return false, 0, err
	}
	var tables int
	err = db.QueryRowContext(ctx, `SELECT count(*) FROM pg_publication_tables WHERE pubname = $1`, name).Scan(&tables)
	return true, tables, err
}

// TablesOf lists the ordinary tables a database holds, for the publication to
// be made of what both servers have.
func TablesOf(ctx context.Context, dsn string) (map[string]bool, error) {
	db, err := openPostgres(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `
		SELECT n.nspname, c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'r' AND n.nspname NOT IN `+importSystemSchemas+` AND n.nspname NOT LIKE 'pg\_toast%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			return nil, err
		}
		have[schema+"."+name] = true
	}
	return have, rows.Err()
}

// Has says whether the table is among those TablesOf returned.
func (t ReplicationTable) Has(tables map[string]bool) bool { return tables[t.Schema+"."+t.Name] }

// ReplicationConninfo is the connection string a subscription is made with: the
// source as the target server will dial it, rebuilt from the parts of the one
// the operator gave rather than passed through. A connection string can name
// files for the server to read (sslrootcert, sslkey, passfile, service) and a
// command line to run (options), and the server that reads it is the target.
func ReplicationConninfo(dsn string) (string, error) {
	info, err := ParseDSN(DriverPostgres, dsn)
	if err != nil {
		return "", err
	}
	if info.User == "" || info.Database == "" {
		return "", fmt.Errorf("the source connection string names no user or no database")
	}
	pairs := [][2]string{
		{"host", info.Host}, {"port", info.Port}, {"dbname", info.Database}, {"user", info.User},
	}
	if info.Password != "" {
		pairs = append(pairs, [2]string{"password", info.Password})
	}
	if mode := sslModeOf(dsn); mode != "" {
		pairs = append(pairs, [2]string{"sslmode", mode})
	}
	var parts []string
	for _, kv := range pairs {
		if kv[1] != "" {
			parts = append(parts, kv[0]+"="+conninfoValue(kv[1]))
		}
	}
	return strings.Join(parts, " "), nil
}

// sslModeOf reads sslmode from a URL or keyword connection string, limited to
// the values libpq knows.
func sslModeOf(dsn string) string {
	var mode string
	if strings.Contains(dsn, "://") {
		if u, err := url.Parse(dsn); err == nil {
			mode = u.Query().Get("sslmode")
		}
	} else if info := parseKeywordMap(dsn); info != nil {
		mode = info["sslmode"]
	}
	switch mode {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
		return mode
	}
	return ""
}

// parseKeywordMap reads a libpq keyword string into its pairs.
func parseKeywordMap(dsn string) map[string]string {
	out := map[string]string{}
	rest := strings.TrimSpace(dsn)
	for rest != "" {
		eq := strings.IndexByte(rest, '=')
		if eq < 0 {
			break
		}
		key := strings.TrimSpace(rest[:eq])
		rest = strings.TrimLeft(rest[eq+1:], " ")
		var value strings.Builder
		if strings.HasPrefix(rest, "'") {
			i := 1
			for ; i < len(rest) && rest[i] != '\''; i++ {
				if rest[i] == '\\' && i+1 < len(rest) {
					i++
				}
				value.WriteByte(rest[i])
			}
			rest = strings.TrimLeft(rest[min(i+1, len(rest)):], " ")
		} else {
			end := strings.IndexByte(rest, ' ')
			if end < 0 {
				end = len(rest)
			}
			value.WriteString(rest[:end])
			rest = strings.TrimLeft(rest[end:], " ")
		}
		out[key] = value.String()
	}
	return out
}

// conninfoValue quotes a value the way libpq reads one in a keyword string.
func conninfoValue(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
	return "'" + r.Replace(v) + "'"
}

// CreateSlot makes the logical replication slot a subscription reads from, on
// the source. The dashboard makes it rather than leaving it to the subscription
// so that the subscription can be created from inside a function (see
// CreateSubscription), and so that the slot's whole life is in one place.
func CreateSlot(ctx context.Context, sourceDSN, name string) error {
	if !ImportNameRe.MatchString(name) {
		return fmt.Errorf("%q is not a name a slot can have here", name)
	}
	db, err := openPostgres(ctx, sourceDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, `SELECT pg_create_logical_replication_slot($1, 'pgoutput')`, name)
	return err
}

// CreateSubscription subscribes the target to the publication, over the slot
// CreateSlot made, and starts the copy of every table.
//
// The connection string goes in as a parameter of a function that exists for
// the session, not as text in the statement. A CREATE SUBSCRIPTION carries it
// as a literal, and a utility statement is recorded as written by
// pg_stat_statements — which the dashboard's own Statements page reads back to
// whoever may look at it. The statement cannot be run from a function while it
// makes the slot itself, which is the other reason the slot is made first.
func CreateSubscription(ctx context.Context, targetDSN, name, conninfo, publication string) error {
	if !ImportNameRe.MatchString(name) {
		return fmt.Errorf("%q is not a name a subscription can have here", name)
	}
	db, err := openPostgres(ctx, targetDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `
		CREATE FUNCTION pg_temp.jd_subscribe(name text, conninfo text, publication text) RETURNS void
		LANGUAGE plpgsql AS $f$
		BEGIN
		  EXECUTE format('CREATE SUBSCRIPTION %I CONNECTION %L PUBLICATION %I WITH (copy_data = true, create_slot = false, slot_name = %L)',
		                 name, conninfo, publication, name);
		END
		$f$`); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `SELECT pg_temp.jd_subscribe($1, $2, $3)`, name, conninfo, publication)
	return err
}

// SubscriptionTable is one table's place in the initial copy.
type SubscriptionTable struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	// State is "waiting", "copying", "copied", "synchronizing" or "ready".
	State string `json:"state"`
	// RowsCopied is how many rows the copy of this table has taken so far,
	// and -1 when the server does not say: PostgreSQL 14 added the view that
	// reports it, and only while the copy runs.
	RowsCopied int64 `json:"rowsCopied"`
}

// SubscriptionState is how a subscription is getting on, as the target sees it.
type SubscriptionState struct {
	Exists  bool                `json:"exists"`
	Enabled bool                `json:"enabled"`
	Tables  []SubscriptionTable `json:"tables"`
	Ready   int                 `json:"ready"`
	// Applying is whether the worker that applies changes is running.
	Applying bool `json:"applying"`
	// Errors counts the failures the server has recorded for the subscription,
	// and -1 before PostgreSQL 15, which started counting them.
	Errors int64 `json:"errors"`
}

var subscriptionStates = map[string]string{
	"i": "waiting", "d": "copying", "f": "copied", "s": "synchronizing", "r": "ready",
}

// ReadSubscription reads a subscription on the target.
func ReadSubscription(ctx context.Context, targetDSN, name string) (*SubscriptionState, error) {
	db, err := openPostgres(ctx, targetDSN)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	state := &SubscriptionState{Tables: []SubscriptionTable{}, Errors: -1}
	var oid string
	err = db.QueryRowContext(ctx, `
		SELECT oid::text, subenabled FROM pg_subscription
		WHERE subname = $1 AND subdbid = (SELECT oid FROM pg_database WHERE datname = current_database())`, name).
		Scan(&oid, &state.Enabled)
	if err == sql.ErrNoRows {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	state.Exists = true

	var version int
	if err := db.QueryRowContext(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&version); err != nil {
		return nil, err
	}
	progress := "-1::bigint"
	join := ""
	if version >= 140000 {
		progress = "COALESCE(pc.tuples_processed, -1)"
		join = "LEFT JOIN pg_stat_progress_copy pc ON pc.relid = sr.srrelid AND pc.datid = (SELECT oid FROM pg_database WHERE datname = current_database())"
	}
	rows, err := db.QueryContext(ctx, `
		SELECT n.nspname, c.relname, sr.srsubstate::text, `+progress+`
		FROM pg_subscription_rel sr
		JOIN pg_class c ON c.oid = sr.srrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		`+join+`
		WHERE sr.srsubid = $1::text::oid
		ORDER BY n.nspname, c.relname`, oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t SubscriptionTable
		var code string
		if err := rows.Scan(&t.Schema, &t.Name, &code, &t.RowsCopied); err != nil {
			return nil, err
		}
		t.State = subscriptionStates[code]
		if code == "r" {
			state.Ready++
		}
		state.Tables = append(state.Tables, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM pg_stat_subscription WHERE subid = $1::text::oid AND relid IS NULL AND pid IS NOT NULL)`, oid).
		Scan(&state.Applying); err != nil {
		return nil, err
	}
	if version >= 150000 {
		if err := db.QueryRowContext(ctx, `
			SELECT COALESCE(apply_error_count + sync_error_count, 0) FROM pg_stat_subscription_stats WHERE subid = $1::text::oid`, oid).
			Scan(&state.Errors); err != nil && err != sql.ErrNoRows {
			return nil, err
		}
	}
	return state, nil
}

// SlotLag is how far behind the source the subscription is.
type SlotLag struct {
	// Exists is whether the source still has the slot.
	Exists bool `json:"exists"`
	Active bool `json:"active"`
	// Bytes is the WAL the source has written that the target has not yet
	// confirmed. Zero is caught up to this moment.
	Bytes int64 `json:"bytes"`
}

// ReadSlotLag reads the slot a subscription made on the source.
func ReadSlotLag(ctx context.Context, sourceDSN, slot string) (SlotLag, error) {
	db, err := openPostgres(ctx, sourceDSN)
	if err != nil {
		return SlotLag{}, err
	}
	defer db.Close()
	return readSlotLag(ctx, db, slot)
}

func readSlotLag(ctx context.Context, db *sql.DB, slot string) (SlotLag, error) {
	var lag SlotLag
	err := db.QueryRowContext(ctx, `
		SELECT true, active, COALESCE(pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn), 0)::bigint
		FROM pg_replication_slots WHERE slot_name = $1 AND slot_type = 'logical'`, slot).Scan(&lag.Exists, &lag.Active, &lag.Bytes)
	if err == sql.ErrNoRows {
		return SlotLag{}, nil
	}
	return lag, err
}

// CatchUpTimeout is how long a cutover waits for the target to reach the
// source before it gives up and leaves the subscription as it was.
const CatchUpTimeout = 15 * time.Minute

// WaitCaughtUp waits until the target has applied everything the source wrote
// up to now. The position is read once, here: what is written after it is not
// waited for, which is why the cutover asks for the writes to have stopped.
func WaitCaughtUp(ctx context.Context, sourceDSN, slot string, report func(remaining int64)) error {
	db, err := openPostgres(ctx, sourceDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	var mark string
	if err := db.QueryRowContext(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&mark); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, CatchUpTimeout)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		var behind sql.NullInt64
		err := db.QueryRowContext(ctx, `
			SELECT GREATEST(pg_wal_lsn_diff($2::text::pg_lsn, confirmed_flush_lsn), 0)::bigint
			FROM pg_replication_slots WHERE slot_name = $1 AND slot_type = 'logical'`, slot, mark).Scan(&behind)
		if err == sql.ErrNoRows {
			return fmt.Errorf("the source has no replication slot called %s; the subscription is gone or never started", slot)
		}
		if err != nil {
			return err
		}
		// A slot that has confirmed nothing yet is as far behind as it can be.
		remaining := int64(1)
		if behind.Valid {
			remaining = behind.Int64
		}
		if report != nil {
			report(remaining)
		}
		if remaining == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("the target was still %d bytes behind after %s; nothing was changed", remaining, CatchUpTimeout)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// SyncSequences sets every sequence on the target to the position the same
// sequence has on the source, and returns how many it set and the names of the
// ones it could not (the target does not have them).
func SyncSequences(ctx context.Context, sourceDSN, targetDSN string) (int, []string, error) {
	src, err := openPostgres(ctx, sourceDSN)
	if err != nil {
		return 0, nil, err
	}
	defer src.Close()
	dst, err := openPostgres(ctx, targetDSN)
	if err != nil {
		return 0, nil, err
	}
	defer dst.Close()

	rows, err := src.QueryContext(ctx, `
		SELECT n.nspname, c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'S' AND n.nspname NOT IN `+importSystemSchemas+` AND n.nspname NOT LIKE 'pg\_toast%'
		ORDER BY 1, 2`)
	if err != nil {
		return 0, nil, err
	}
	type sequence struct{ schema, name string }
	var found []sequence
	for rows.Next() {
		var s sequence
		if err := rows.Scan(&s.schema, &s.name); err != nil {
			rows.Close()
			return 0, nil, err
		}
		found = append(found, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}

	set, missing := 0, []string{}
	for _, s := range found {
		rel, _ := quoteDouble(s.schema)
		name, _ := quoteDouble(s.name)
		var last int64
		var called bool
		if err := src.QueryRowContext(ctx, `SELECT last_value, is_called FROM `+rel+`.`+name).Scan(&last, &called); err != nil {
			return set, missing, fmt.Errorf("could not read sequence %s.%s on the source: %w", s.schema, s.name, err)
		}
		var present bool
		if err := dst.QueryRowContext(ctx, `SELECT to_regclass(format('%I.%I', $1::text, $2::text)) IS NOT NULL`, s.schema, s.name).Scan(&present); err != nil {
			return set, missing, err
		}
		if !present {
			missing = append(missing, s.schema+"."+s.name)
			continue
		}
		if _, err := dst.ExecContext(ctx, `SELECT setval(format('%I.%I', $1::text, $2::text)::regclass, $3, $4)`, s.schema, s.name, last, called); err != nil {
			return set, missing, fmt.Errorf("could not set sequence %s.%s on the target: %w", s.schema, s.name, err)
		}
		set++
	}
	return set, missing, nil
}

// DropSubscription removes a subscription from the target. It is disabled and
// let go of its slot first, so that dropping it does not dial the source, which
// may be what has gone; the slot is DropSlot's.
func DropSubscription(ctx context.Context, targetDSN, name string) error {
	q, err := quoteDouble(name)
	if err != nil {
		return err
	}
	dst, err := openPostgres(ctx, targetDSN)
	if err != nil {
		return err
	}
	defer dst.Close()
	var exists bool
	if err := dst.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM pg_subscription WHERE subname = $1 AND subdbid = (SELECT oid FROM pg_database WHERE datname = current_database()))`, name).
		Scan(&exists); err != nil || !exists {
		return err
	}
	for _, statement := range []string{
		"ALTER SUBSCRIPTION " + q + " DISABLE",
		"ALTER SUBSCRIPTION " + q + " SET (slot_name = NONE)",
		"DROP SUBSCRIPTION " + q,
	} {
		if _, err := dst.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// DropSlot drops the slot on the source once its walsender has let go of it,
// which follows the subscription being disabled by a moment. A slot that is not
// there is already what was asked, and one left behind would hold the source's
// WAL until its disk filled.
func DropSlot(ctx context.Context, sourceDSN, slot string) error {
	src, err := openPostgres(ctx, sourceDSN)
	if err != nil {
		return err
	}
	defer src.Close()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var active bool
		err := src.QueryRowContext(ctx, `SELECT active FROM pg_replication_slots WHERE slot_name = $1`, slot).Scan(&active)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		if !active {
			_, err := src.ExecContext(ctx, `SELECT pg_drop_replication_slot($1)`, slot)
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the replication slot %s on the source is still in use; drop it there with pg_drop_replication_slot once it is idle", slot)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// openPostgres dials a throwaway connection for one of the calls above.
func openPostgres(ctx context.Context, dsn string) (*sql.DB, error) {
	d, err := DialectFor(DriverPostgres)
	if err != nil {
		return nil, err
	}
	return openForDump(ctx, d, dsn)
}
