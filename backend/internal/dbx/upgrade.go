package dbx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Moving a PostgreSQL server to a newer major release.
//
// A major release cannot open the data directory of the one before it, so an
// upgrade is a new server beside the old one with everything copied across:
// the roles, every database, and the parameters an operator set. The copy is
// the server's own tools — pg_dumpall for the roles, pg_dump and pg_restore
// for each database — run inside the two containers by the dashboard, which
// passes the bytes between them; what this file holds is everything about
// that which is a fact of PostgreSQL rather than of Docker: what to ask each
// server before anything changes, what its answers mean for the upgrade, the
// roles' statements as pg_dumpall wrote them, and which parameters the new
// release still has.
//
// Each question is one query that answers one JSON value, so the same reading
// works through the dashboard's own connection and through the container's
// psql, and the old server and the new one are read the same way.

// PostgresServerFacts is what one server says about itself before an upgrade:
// the old server, to know what has to arrive, and the new release's, to know
// what it can take.
type PostgresServerFacts struct {
	Version       string `json:"version"`
	VersionNum    int    `json:"versionNum"`
	DataDirectory string `json:"dataDirectory"`
	// Databases is every database that takes connections, which is every one
	// but template0.
	Databases []PostgresDatabase `json:"databases"`
	// Roles are the server's accounts, without the ones it ships (pg_*).
	Roles []string `json:"roles"`
	// Sessions counts the client sessions other than the one asking and the
	// dashboard's own.
	Sessions      int                  `json:"sessions"`
	Slots         []PostgresSlot       `json:"slots"`
	Subscriptions []string             `json:"subscriptions"`
	Prepared      []string             `json:"prepared"`
	Tablespaces   []PostgresTablespace `json:"tablespaces"`
	// Preload is shared_preload_libraries as the server runs with it, from
	// whichever of the command line, postgresql.conf and ALTER SYSTEM set it.
	Preload string `json:"preload"`
	// AutoConf is postgresql.auto.conf: what ALTER SYSTEM, and so the
	// Parameters page, wrote.
	AutoConf string `json:"autoConf"`
	// FileSettings is what postgresql.conf and its includes set, by name.
	FileSettings map[string]string `json:"fileSettings"`
	// HBA is each pg_hba.conf rule as one line of its fields.
	HBA []string `json:"hba"`
	// Settings names every parameter the release has.
	Settings []string `json:"settings"`
	// Available is every extension the release's image carries, with the
	// version CREATE EXTENSION makes.
	Available map[string]string `json:"available"`
	// Loadable says, of each library the old server preloads, whether the new
	// release can load it. Filled by the caller, which asks the new server.
	Loadable map[string]bool `json:"-"`
}

// PostgresDatabase is one database of a server.
type PostgresDatabase struct {
	Name     string `json:"name"`
	Bytes    int64  `json:"bytes"`
	Template bool   `json:"template"`
}

// PostgresSlot is one replication slot.
type PostgresSlot struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// PostgresTablespace is a tablespace other than the two every server has.
type PostgresTablespace struct {
	Name     string `json:"name"`
	Location string `json:"location"`
}

// PostgresExtension is one extension installed in a database.
type PostgresExtension struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// PostgresServerFactsQuery answers PostgresServerFacts. It is run in the
// postgres database as a superuser: the file settings, the auto.conf text and
// the prepared transactions are a superuser's to read.
var PostgresServerFactsQuery = `SELECT json_build_object(
  'version', current_setting('server_version'),
  'versionNum', current_setting('server_version_num')::int,
  'dataDirectory', current_setting('data_directory'),
  'databases', (SELECT coalesce(json_agg(json_build_object('name', datname, 'bytes', pg_database_size(oid), 'template', datistemplate) ORDER BY datname), '[]'::json)
    FROM pg_database WHERE datallowconn),
  'roles', (SELECT coalesce(json_agg(rolname ORDER BY rolname), '[]'::json) FROM pg_roles WHERE rolname !~ '^pg_'),
  'sessions', (SELECT count(*) FROM pg_stat_activity
    WHERE backend_type = 'client backend' AND pid <> pg_backend_pid() AND application_name <> ` + quotePostgresLiteral(ApplicationName) + `),
  'slots', (SELECT coalesce(json_agg(json_build_object('name', slot_name, 'type', slot_type) ORDER BY slot_name), '[]'::json) FROM pg_replication_slots),
  'subscriptions', (SELECT coalesce(json_agg(subname ORDER BY subname), '[]'::json) FROM pg_subscription),
  'prepared', (SELECT coalesce(json_agg(gid ORDER BY gid), '[]'::json) FROM pg_prepared_xacts),
  'tablespaces', (SELECT coalesce(json_agg(json_build_object('name', spcname, 'location', pg_tablespace_location(oid)) ORDER BY spcname), '[]'::json)
    FROM pg_tablespace WHERE spcname NOT IN ('pg_default', 'pg_global')),
  'preload', current_setting('shared_preload_libraries'),
  'autoConf', coalesce(pg_read_file('postgresql.auto.conf', 0, 1048576, true), ''),
  'fileSettings', (SELECT coalesce(json_object_agg(name, setting), '{}'::json) FROM (
    SELECT DISTINCT ON (lower(name)) lower(name) AS name, setting FROM pg_file_settings
    WHERE sourcefile NOT LIKE '%postgresql.auto.conf' AND error IS NULL ORDER BY lower(name), seqno DESC) s),
  'hba', (SELECT coalesce(json_agg(concat_ws(' ', type, array_to_string(database, ','), array_to_string(user_name, ','), address, netmask, auth_method) ORDER BY line_number), '[]'::json)
    FROM pg_hba_file_rules WHERE error IS NULL),
  'settings', (SELECT coalesce(json_agg(name ORDER BY name), '[]'::json) FROM pg_settings),
  'available', (SELECT coalesce(json_object_agg(name, default_version), '{}'::json) FROM pg_available_extensions)
)`

// PostgresExtensionsQuery answers the extensions installed in the database it
// is run in.
const PostgresExtensionsQuery = `SELECT coalesce(json_agg(json_build_object('name', extname, 'version', extversion) ORDER BY extname), '[]'::json) FROM pg_extension`

// PostgresRowCountsQuery answers every table of the database it is run in
// with its exact number of rows, by qualified name. query_to_xml counts in a
// session that may only read, which is what the old server is while it is
// copied. A table an extension owns is left out: the extension makes its own
// in the new database, and its contents are the extension's version's.
const PostgresRowCountsQuery = `SELECT coalesce(json_object_agg(t.name, t.rows), '{}'::json) FROM (
  SELECT format('%I.%I', n.nspname, c.relname) AS name,
    (xpath('/row/c/text()', query_to_xml(format('SELECT count(*) AS c FROM %I.%I', n.nspname, c.relname), false, true, '')))[1]::text::bigint AS rows
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE c.relkind = 'r' AND n.nspname NOT IN ('pg_catalog', 'information_schema')
    AND n.nspname !~ '^pg_(toast|temp)'
    AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e')
) t`

// ParsePostgresServerFacts reads what PostgresServerFactsQuery answered.
func ParsePostgresServerFacts(raw []byte) (*PostgresServerFacts, error) {
	var facts PostgresServerFacts
	if err := json.Unmarshal(raw, &facts); err != nil {
		return nil, fmt.Errorf("the server's answer could not be read: %w", err)
	}
	if facts.VersionNum <= 0 {
		return nil, errors.New("the server did not say which release it is")
	}
	return &facts, nil
}

// PostgresMajor is the major release a server_version_num names: 160015 is
// 16. Every release before 10 reads as 9, which orders it before 10 and is
// as far as an upgrade from here needs to tell them apart.
func PostgresMajor(versionNum int) int {
	return versionNum / 10000
}

// PostgresPreloadLibraries splits shared_preload_libraries into its names.
func PostgresPreloadLibraries(setting string) []string {
	out := []string{}
	for _, part := range strings.Split(setting, ",") {
		name := strings.Trim(strings.TrimSpace(part), `"`)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// UpgradeVerdict is what one check means for the upgrade: nothing to say, a
// fact to know first, or a reason it cannot start.
type UpgradeVerdict string

const (
	UpgradeOK      UpgradeVerdict = "ok"
	UpgradeWarning UpgradeVerdict = "warning"
	UpgradeBlocker UpgradeVerdict = "blocker"
)

// UpgradeCheck is one line of the report read before an upgrade.
type UpgradeCheck struct {
	ID      string         `json:"id"`
	Verdict UpgradeVerdict `json:"verdict"`
	Title   string         `json:"title"`
	Detail  string         `json:"detail"`
	// Items are the names the detail is about, one each.
	Items []string `json:"items,omitempty"`
}

// PostgresUpgradeChecks is what the two servers' answers mean for an upgrade
// from one to the other. installed is the old server's extensions, by
// database. Disk space and the names the new containers take are the
// caller's: they are facts of the machine, not of either server.
func PostgresUpgradeChecks(from, to *PostgresServerFacts, installed map[string][]PostgresExtension) []UpgradeCheck {
	checks := []UpgradeCheck{versionCheck(from, to), extensionsCheck(installed, to)}
	if libraries := PostgresPreloadLibraries(from.Preload); len(libraries) > 0 {
		checks = append(checks, librariesCheck(libraries, to))
	}
	checks = append(checks, sessionsCheck(from))
	if len(from.Prepared) > 0 {
		checks = append(checks, UpgradeCheck{
			ID: "prepared", Verdict: UpgradeBlocker, Title: "Prepared transactions",
			Detail: "Transactions prepared for a two-phase commit are waiting to be committed or rolled back. A copy cannot carry them; finish them with COMMIT PREPARED or ROLLBACK PREPARED first.",
			Items:  from.Prepared,
		})
	}
	if len(from.Tablespaces) > 0 {
		items := make([]string, 0, len(from.Tablespaces))
		for _, ts := range from.Tablespaces {
			items = append(items, ts.Name+" at "+ts.Location)
		}
		checks = append(checks, UpgradeCheck{
			ID: "tablespaces", Verdict: UpgradeBlocker, Title: "Tablespaces",
			Detail: "These tablespaces keep their data in a directory of their own, which the new server's container does not have. Move their tables to pg_default first.",
			Items:  items,
		})
	}
	if c, ok := replicationCheck(from); ok {
		checks = append(checks, c)
	}
	checks = append(checks, parametersCheck(from, to))
	if c, ok := signInRulesCheck(from, to); ok {
		checks = append(checks, c)
	}
	return checks
}

func versionCheck(from, to *PostgresServerFacts) UpgradeCheck {
	c := UpgradeCheck{ID: "version", Title: "Version"}
	if PostgresMajor(to.VersionNum) <= PostgresMajor(from.VersionNum) {
		c.Verdict = UpgradeBlocker
		c.Detail = fmt.Sprintf("The new image runs PostgreSQL %s, which is not a newer major release than %s.", to.Version, from.Version)
		return c
	}
	c.Verdict = UpgradeOK
	c.Detail = fmt.Sprintf("PostgreSQL %s to %s.", from.Version, to.Version)
	return c
}

// extensionsCheck holds every installed extension to the new image. One it
// does not carry stops the upgrade, since the restore would stop at it; one
// the new release creates at another version is said, because the copy makes
// the image's version and not the one installed now.
func extensionsCheck(installed map[string][]PostgresExtension, to *PostgresServerFacts) UpgradeCheck {
	c := UpgradeCheck{ID: "extensions", Title: "Extensions"}
	missing, changed, carried := []string{}, []string{}, []string{}
	databases := make([]string, 0, len(installed))
	for name := range installed {
		databases = append(databases, name)
	}
	slices.Sort(databases)
	seen := map[string]bool{}
	for _, database := range databases {
		for _, ext := range installed[database] {
			if ext.Name == "plpgsql" {
				continue
			}
			next, ok := to.Available[ext.Name]
			switch {
			case !ok:
				missing = append(missing, ext.Name+" in "+database)
			case seen[ext.Name]:
			case next != "" && next != ext.Version:
				changed = append(changed, fmt.Sprintf("%s %s → %s", ext.Name, ext.Version, next))
			default:
				carried = append(carried, ext.Name+" "+ext.Version)
			}
			seen[ext.Name] = true
		}
	}
	switch {
	case len(missing) > 0:
		c.Verdict, c.Items = UpgradeBlocker, missing
		c.Detail = "The new image does not carry these, so their databases cannot be restored on it. Drop them first if they are unused, or upgrade with an image that has them."
	case len(changed) > 0:
		c.Verdict, c.Items = UpgradeOK, append(changed, carried...)
		c.Detail = "Each is created again in the new server at the version its image carries."
	case len(carried) > 0:
		c.Verdict, c.Items = UpgradeOK, carried
		c.Detail = "The new image carries every extension installed here."
	default:
		c.Verdict = UpgradeOK
		c.Detail = "No extension is installed besides plpgsql."
	}
	return c
}

func librariesCheck(libraries []string, to *PostgresServerFacts) UpgradeCheck {
	c := UpgradeCheck{ID: "libraries", Title: "Preloaded libraries", Items: libraries}
	missing := []string{}
	for _, library := range libraries {
		if !to.Loadable[library] {
			missing = append(missing, library)
		}
	}
	if len(missing) > 0 {
		c.Verdict, c.Items = UpgradeBlocker, missing
		c.Detail = "The server preloads these and the new image has no such library, so the new server would not start. Remove them from shared_preload_libraries first."
		return c
	}
	c.Verdict = UpgradeOK
	c.Detail = "The new image has every library in shared_preload_libraries."
	return c
}

func sessionsCheck(from *PostgresServerFacts) UpgradeCheck {
	c := UpgradeCheck{ID: "sessions", Title: "Connected sessions"}
	if from.Sessions == 0 {
		c.Verdict = UpgradeOK
		c.Detail = "Nothing else is connected now. While the data is copied the server takes reads only, and it is down for a few seconds when the new one takes its place."
		return c
	}
	c.Verdict = UpgradeWarning
	c.Detail = fmt.Sprintf("%d %s connected now. %s disconnected when the copy starts, the server takes reads only until the new one takes its place, and applications have to reconnect after it.",
		from.Sessions, plural(from.Sessions, "session is", "sessions are"), plural(from.Sessions, "It is", "They are"))
	return c
}

// replicationCheck says what a dump does not carry: a slot is the server's
// own position in its write-ahead log, and a subscription arrives switched
// off with no slot on its publisher.
func replicationCheck(from *PostgresServerFacts) (UpgradeCheck, bool) {
	items := []string{}
	for _, slot := range from.Slots {
		items = append(items, "slot "+slot.Name+" ("+slot.Type+")")
	}
	for _, sub := range from.Subscriptions {
		items = append(items, "subscription "+sub)
	}
	if len(items) == 0 {
		return UpgradeCheck{}, false
	}
	return UpgradeCheck{
		ID: "replication", Verdict: UpgradeWarning, Title: "Replication",
		Detail: "Replication slots are not copied: whatever reads from one has to be set up again against the new server. A subscription is copied switched off; enable it and refresh it once the new server runs.",
		Items:  items,
	}, true
}

// parametersCheck says which parameters reach the new server. What ALTER
// SYSTEM wrote is carried, less a parameter the new release no longer has;
// what was edited into postgresql.conf by hand is not, since that file is
// written anew by the new release's initdb.
func parametersCheck(from, to *PostgresServerFacts) UpgradeCheck {
	c := UpgradeCheck{ID: "parameters", Title: "Parameters"}
	_, carried, dropped := CarryPostgresParameters(from.AutoConf, to.Settings)
	edited := []string{}
	for name, value := range from.FileSettings {
		if next, ok := to.FileSettings[name]; !ok || next != value {
			edited = append(edited, name+" = "+value)
		}
	}
	slices.Sort(edited)
	c.Items = append(c.Items, carried...)
	switch {
	case len(dropped) > 0 || len(edited) > 0:
		c.Verdict = UpgradeWarning
		parts := []string{}
		if len(carried) > 0 {
			parts = append(parts, fmt.Sprintf("%d set from the Parameters page %s carried.", len(carried), plural(len(carried), "is", "are")))
		}
		if len(dropped) > 0 {
			parts = append(parts, fmt.Sprintf("%s %s no longer in the new release and %s left out.",
				strings.Join(dropped, ", "), plural(len(dropped), "is", "are"), plural(len(dropped), "is", "are")))
		}
		if len(edited) > 0 {
			parts = append(parts, "Edits made to postgresql.conf by hand are not carried: set them again on the new server.")
			for _, one := range edited {
				c.Items = append(c.Items, "not carried: "+one)
			}
		}
		c.Detail = strings.Join(parts, " ")
	case len(carried) > 0:
		c.Verdict = UpgradeOK
		c.Detail = fmt.Sprintf("%d %s set from the Parameters page %s carried to the new server.",
			len(carried), plural(len(carried), "parameter", "parameters"), plural(len(carried), "is", "are"))
	default:
		c.Verdict = UpgradeOK
		c.Detail = "Every parameter is at the image's default, which the new server starts with too."
	}
	return c
}

// signInRulesCheck says which pg_hba.conf rules the new server will not have:
// the file is initdb's and the image's, and is not part of any dump.
func signInRulesCheck(from, to *PostgresServerFacts) (UpgradeCheck, bool) {
	missing := []string{}
	for _, rule := range from.HBA {
		if !slices.Contains(to.HBA, rule) {
			missing = append(missing, rule)
		}
	}
	if len(missing) == 0 {
		return UpgradeCheck{}, false
	}
	return UpgradeCheck{
		ID: "hba", Verdict: UpgradeWarning, Title: "Sign-in rules",
		Detail: "pg_hba.conf has rules the new server will not have, because the file is not part of the copy. Add them to the new server's pg_hba.conf after the upgrade if they are still needed.",
		Items:  missing,
	}, true
}

// autoConfLineRe is one parameter line of postgresql.auto.conf, which ALTER
// SYSTEM writes as `name = 'value'`.
var autoConfLineRe = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_$]*(?:\.[A-Za-z0-9_$]+)*)\s*=`)

// CarryPostgresParameters is the old server's postgresql.auto.conf as the new
// one takes it, with the parameters carried and those left out by name.
//
// The file is carried as text rather than replayed as ALTER SYSTEM, because a
// list such as shared_preload_libraries reads back as one string, and set
// again as one string it is one library with a comma in its name. A parameter
// the new release does not have is left out, since the server refuses to
// start with one; a name with a dot is an extension's own and is kept, since
// the server accepts it before the extension's library defines it.
func CarryPostgresParameters(text string, known []string) (file string, carried, dropped []string) {
	have := make(map[string]bool, len(known))
	for _, name := range known {
		have[strings.ToLower(name)] = true
	}
	carried, dropped = []string{}, []string{}
	var out strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
		if line == "" {
			continue
		}
		if m := autoConfLineRe.FindStringSubmatch(line); m != nil {
			name := strings.ToLower(m[1])
			if !strings.Contains(name, ".") && !have[name] {
				dropped = append(dropped, name)
				continue
			}
			carried = append(carried, name)
		}
		out.WriteString(line)
	}
	file = out.String()
	if file != "" && !strings.HasSuffix(file, "\n") {
		file += "\n"
	}
	return file, carried, dropped
}

// PostgresGlobals reads what `pg_dumpall --globals-only` wrote into the
// statements a new server is to run: every role, its settings and its
// memberships.
//
// The role the new server was created with is already there, and the
// statement that creates it would fail; it is left out, and the ALTER ROLE
// after it, which gives that role its attributes and password, is kept. The
// script is cut where psql would cut it and the statements are run by the
// server over a connection, so nothing in it is a client command: a
// meta-command other than the \restrict pair is refused here, as a restore
// refuses one.
func PostgresGlobals(r io.Reader, bootstrap string) ([]string, error) {
	skip := map[string]bool{"CREATE ROLE " + quotePostgresIdent(bootstrap): true}
	if simplePostgresIdent(bootstrap) {
		skip["CREATE ROLE "+bootstrap] = true
	}
	script := newPostgresScript(r)
	out := []string{}
	for {
		st, err := script.next()
		if err == io.EOF {
			return out, nil
		}
		var reconnects *scriptReconnectError
		if errors.As(err, &reconnects) {
			return nil, fmt.Errorf("the roles' script names another database (%s)", reconnects.command)
		}
		if err != nil {
			return nil, err
		}
		if st.copyIn {
			return nil, fmt.Errorf("line %d: the roles' script copies rows, which pg_dumpall --globals-only never writes", st.line)
		}
		text := strings.TrimSpace(st.sql)
		if skip[text] {
			continue
		}
		out = append(out, text)
	}
}

// ReplayPostgresStatements runs statements on the server a connection string
// names, as one transaction: either every role arrives or none does.
func ReplayPostgresStatements(ctx context.Context, dsn string, statements []string) error {
	d, err := DialectFor(DriverPostgres)
	if err != nil {
		return err
	}
	db, err := openForDump(ctx, d, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for i, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("statement %d failed: %v\n%s", i+1, err, truncateForMessage(statement))
		}
	}
	return tx.Commit()
}

// PostgresTakeReadsOnly makes every transaction that begins from now on a
// read-only one, for the minutes the server is being copied: a write made
// after its database was dumped would be lost with the old server.
// ALTER SYSTEM is itself allowed in a read-only session, so the same session
// can take it back.
func PostgresTakeReadsOnly() []string {
	return []string{"ALTER SYSTEM SET default_transaction_read_only = on", "SELECT pg_reload_conf()"}
}

// PostgresTakeWrites undoes PostgresTakeReadsOnly, putting back the value
// postgresql.auto.conf held before it (prior, "" when it held none). Without
// reload the file alone is put back, for a server that is about to stop.
func PostgresTakeWrites(prior string, reload bool) []string {
	out := []string{"ALTER SYSTEM RESET default_transaction_read_only"}
	if prior != "" {
		out = []string{"ALTER SYSTEM SET default_transaction_read_only = " + quotePostgresLiteral(prior)}
	}
	if reload {
		out = append(out, "SELECT pg_reload_conf()")
	}
	return out
}

// PostgresEndSessionsQuery disconnects every client session but the asking
// one, so a transaction that began before the server took reads only does
// not go on writing; it answers how many it ended.
const PostgresEndSessionsQuery = `SELECT json_build_object('ended', count(*) FILTER (WHERE pg_terminate_backend(pid))) FROM pg_stat_activity
WHERE backend_type = 'client backend' AND pid <> pg_backend_pid()`

// PostgresReadsOnlyQuery answers whether a new session is read-only, which is
// how the switch is known to have reached the server.
const PostgresReadsOnlyQuery = `SELECT to_json(current_setting('default_transaction_read_only') = 'on')`

// PostgresDropDatabase drops one of a new server's own first databases, to
// make room for the copy of the old server's database of that name.
func PostgresDropDatabase(name string) string {
	return "DROP DATABASE " + quotePostgresIdent(name)
}

// PostgresLoad loads a library into the session, which is how a new release
// is asked whether it has one of the libraries the old server preloads.
func PostgresLoad(library string) string {
	return "LOAD " + quotePostgresLiteral(library)
}

// PostgresAutoConfValue is the value postgresql.auto.conf gives a parameter,
// and false when it gives none.
func PostgresAutoConfValue(text, name string) (string, bool) {
	value, found := "", false
	for _, line := range strings.Split(text, "\n") {
		m := autoConfLineRe.FindStringSubmatch(line)
		if m == nil || !strings.EqualFold(m[1], name) {
			continue
		}
		rest := strings.TrimSpace(line[len(m[0]):])
		if strings.HasPrefix(rest, "'") {
			if end := strings.LastIndex(rest, "'"); end > 0 {
				rest = strings.ReplaceAll(rest[1:end], "''", "'")
			}
		}
		// The last line for a name is the one the server reads.
		value, found = rest, true
	}
	return value, found
}

// PostgresConnInfo is a libpq connection string naming one database, for a
// client's -d. A database is named as a value in the string rather than by
// itself because psql reads a -d that contains an equals sign as a
// connection string, and a database may be called anything.
func PostgresConnInfo(database string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(database)
	return "dbname='" + escaped + "'"
}

// simplePostgresIdent reports whether pg_dump writes a name without quotes:
// lower case letters, digits and underscores, not starting with a digit.
// (A keyword is quoted too; it then matches the quoted form.)
func simplePostgresIdent(name string) bool {
	if name == "" || (name[0] >= '0' && name[0] <= '9') {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

func quotePostgresIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func quotePostgresLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// plural picks one of two words for a count.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// ParsePostgresRowCounts reads what PostgresRowCountsQuery answered.
func ParsePostgresRowCounts(raw []byte) (map[string]int64, error) {
	counts := map[string]int64{}
	if err := json.Unmarshal(raw, &counts); err != nil {
		return nil, fmt.Errorf("the row counts could not be read: %w", err)
	}
	return counts, nil
}

// PostgresCopyDifferences compares a database's tables on the old server and
// the new one, and says what does not match.
func PostgresCopyDifferences(database string, before, after map[string]int64) []string {
	tables := make([]string, 0, len(before))
	for table := range before {
		tables = append(tables, table)
	}
	slices.Sort(tables)
	out := []string{}
	for _, table := range tables {
		got, ok := after[table]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s: %s is missing", database, table))
		case got != before[table]:
			out = append(out, fmt.Sprintf("%s: %s has %s rows, not %s", database, table,
				strconv.FormatInt(got, 10), strconv.FormatInt(before[table], 10)))
		}
	}
	return out
}
