package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Server parameters: every one the engine has, and changing one.
//
// The list in admin.go is the thirty an operator asks about. This is the
// whole catalogue, with what the engine itself says about each parameter —
// its type, its bounds, its default, whether a change needs a restart — and
// the one statement per engine that changes a value so that it is still
// changed after the server restarts.
//
// A change is validated twice. Here, against the type and range the engine
// published for that parameter, so a refusal names the bounds instead of
// quoting a syntax error; and by the engine, which has the last word. The
// name is never taken from the request into a statement: it is looked up in
// the engine's own list and the list's spelling is what is used.

// SettingsLister is the optional dialect half that lists every parameter.
type SettingsLister interface {
	AllSettings(ctx context.Context, db *sql.DB) ([]Setting, error)
}

// SettingsWriter is the optional half for the engines that can persist a
// change from a SQL session.
type SettingsWriter interface {
	SetSetting(ctx context.Context, db *sql.DB, current Setting, value string) (*SettingChange, error)
	ResetSetting(ctx context.Context, db *sql.DB, current Setting) (*SettingChange, error)
}

// SettingChange is what a change did.
type SettingChange struct {
	Name string `json:"name"`
	// Value is what the engine reports for the setting after the change.
	Value string `json:"value"`
	// Statements is the SQL that ran, in order.
	Statements []string `json:"statements"`
	// Persisted is true when the change survives a restart.
	Persisted bool `json:"persisted"`
	// RestartRequired is true when the change is stored and the running
	// server still uses the old value.
	RestartRequired bool   `json:"restartRequired"`
	Note            string `json:"note,omitempty"`
}

// ErrSettingRequest marks a refusal that is about the request: a name the
// engine does not have, a value outside its type or range, a setting that
// cannot be changed from a session.
type ErrSettingRequest struct{ msg string }

func (e ErrSettingRequest) Error() string { return e.msg }

func settingRefused(format string, args ...any) error {
	return ErrSettingRequest{msg: fmt.Sprintf(format, args...)}
}

// ListSettings returns every parameter the engine has, or — when all is
// false — the short list an operator asks about, in the same shape.
func ListSettings(ctx context.Context, db *sql.DB, driver Driver, all bool) ([]Setting, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	lister, ok := d.(SettingsLister)
	if !ok {
		return nil, ErrUnsupported
	}
	if !all {
		// The short list is each engine's own choice of names; SQLite's
		// pragmas are few enough that the whole list is the short one.
		if curated, ok := d.(interface {
			Settings(context.Context, *sql.DB) ([]Setting, error)
		}); ok {
			return curated.Settings(ctx, db)
		}
	}
	return lister.AllSettings(ctx, db)
}

// SettingsWritable reports whether the engine can persist a change at all.
func SettingsWritable(driver Driver) bool {
	d, err := DialectFor(driver)
	if err != nil {
		return false
	}
	_, ok := d.(SettingsWriter)
	return ok
}

// ChangeSetting sets one parameter, or resets it to its default, and persists
// the change where the engine can.
func ChangeSetting(ctx context.Context, db *sql.DB, driver Driver, name, value string, reset bool) (*SettingChange, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	lister, ok := d.(SettingsLister)
	if !ok {
		return nil, ErrUnsupported
	}
	writer, ok := d.(SettingsWriter)
	if !ok {
		return nil, settingRefused("%s parameters are changed in the server's configuration, not from here", driver)
	}
	all, err := lister.AllSettings(ctx, db)
	if err != nil {
		return nil, err
	}
	var current *Setting
	for i := range all {
		if strings.EqualFold(all[i].Name, strings.TrimSpace(name)) {
			current = &all[i]
			break
		}
	}
	if current == nil {
		return nil, settingRefused("%q is not a parameter of this server", name)
	}
	if !current.Editable {
		return nil, settingRefused("%s cannot be changed from here", current.Name)
	}
	var out *SettingChange
	if reset {
		out, err = writer.ResetSetting(ctx, db, *current)
	} else {
		out, err = writer.SetSetting(ctx, db, *current, value)
	}
	if err != nil {
		return nil, err
	}
	out.Name = current.Name
	if out.Statements == nil {
		out.Statements = []string{}
	}
	return out, nil
}

// --- value checking ----------------------------------------------------------

// settingBools is every spelling of true and false the engines accept.
var settingBools = map[string]bool{
	"on": true, "true": true, "yes": true, "1": true,
	"off": false, "false": false, "no": false, "0": false,
}

// settingNumber splits "64MB" into 64 and MB.
var settingNumber = regexp.MustCompile(`^([+-]?[0-9]+(?:\.[0-9]+)?)\s*([A-Za-z]*)$`)

// The units PostgreSQL accepts after a number, as a multiple of the smallest
// in each family: bytes for memory, microseconds for time.
var (
	pgMemoryUnits = map[string]float64{"B": 1, "kB": 1 << 10, "MB": 1 << 20, "GB": 1 << 30, "TB": 1 << 40}
	pgTimeUnits   = map[string]float64{"us": 1, "ms": 1e3, "s": 1e6, "min": 6e7, "h": 3.6e9, "d": 8.64e10}
)

// pgBaseUnit reads a setting's unit — "8kB", "ms", "MB" — as a multiple of
// its family's smallest unit, and says which family that is.
func pgBaseUnit(unit string) (multiple float64, family map[string]float64, ok bool) {
	m := regexp.MustCompile(`^([0-9]*)([A-Za-z]+)$`).FindStringSubmatch(unit)
	if m == nil {
		return 0, nil, false
	}
	count := 1.0
	if m[1] != "" {
		count, _ = strconv.ParseFloat(m[1], 64)
	}
	if size, ok := pgMemoryUnits[m[2]]; ok {
		return count * size, pgMemoryUnits, true
	}
	if size, ok := pgTimeUnits[m[2]]; ok {
		return count * size, pgTimeUnits, true
	}
	return 0, nil, false
}

// checkSettingValue validates a value against what the engine published for
// the setting and returns it in the form the engine should be sent.
//
// Numbers may carry a unit where the setting has one: the value is converted
// to the setting's own unit for the range check and sent as it was written,
// because the engine parses "64MB" itself and stores what the operator typed.
func checkSettingValue(s Setting, value string, units bool) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > 4096 {
		return "", settingRefused("the value is too long")
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return "", settingRefused("the value contains a control character")
		}
	}
	switch s.Type {
	case "bool":
		on, ok := settingBools[strings.ToLower(value)]
		if !ok {
			return "", settingRefused("%s is on or off", s.Name)
		}
		if on {
			return "on", nil
		}
		return "off", nil
	case "enum":
		for _, allowed := range s.Enum {
			if strings.EqualFold(allowed, value) {
				return allowed, nil
			}
		}
		return "", settingRefused("%s is one of %s", s.Name, strings.Join(s.Enum, ", "))
	case "integer", "real":
		m := settingNumber.FindStringSubmatch(value)
		if m == nil {
			return "", settingRefused("%s takes a number", s.Name)
		}
		n, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return "", settingRefused("%s takes a number", s.Name)
		}
		if m[2] != "" {
			base, family, ok := pgBaseUnit(s.Unit)
			if !units || !ok {
				return "", settingRefused("%s takes a plain number, without a unit", s.Name)
			}
			size, ok := family[m[2]]
			if !ok {
				return "", settingRefused("%s is measured in %s; %q is not a unit it takes", s.Name, s.Unit, m[2])
			}
			n = n * size / base
		} else if s.Type == "integer" && n != math.Trunc(n) {
			return "", settingRefused("%s takes a whole number", s.Name)
		}
		if min, err := strconv.ParseFloat(s.Min, 64); err == nil && n < min {
			return "", settingRefused("%s is at least %s%s", s.Name, s.Min, unitSuffix(s.Unit))
		}
		if max, err := strconv.ParseFloat(s.Max, 64); err == nil && n > max {
			return "", settingRefused("%s is at most %s%s", s.Name, s.Max, unitSuffix(s.Unit))
		}
		return value, nil
	}
	return value, nil
}

func unitSuffix(unit string) string {
	if unit == "" {
		return ""
	}
	return " " + unit
}

// --- PostgreSQL --------------------------------------------------------------

// AllSettings reads pg_settings whole. A parameter is editable from here when
// it is not compiled in and the connection's role is a superuser: ALTER
// SYSTEM writes the server's configuration, and PostgreSQL lets nobody else.
func (postgresDialect) AllSettings(ctx context.Context, db *sql.DB) ([]Setting, error) {
	var super bool
	if err := db.QueryRowContext(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
	  SELECT name, COALESCE(setting, ''), COALESCE(unit, ''), category, COALESCE(short_desc, ''),
	         vartype, context, COALESCE(source, ''), COALESCE(boot_val, ''),
	         COALESCE(min_val, ''), COALESCE(max_val, ''),
	         COALESCE(array_to_string(enumvals, chr(31)), ''),
	         pending_restart, setting IS DISTINCT FROM boot_val
	  FROM pg_settings
	  ORDER BY category, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Setting{}
	for rows.Next() {
		var s Setting
		var enum string
		if err := rows.Scan(&s.Name, &s.Value, &s.Unit, &s.Category, &s.Description, &s.Type, &s.Context,
			&s.Source, &s.Default, &s.Min, &s.Max, &enum, &s.PendingRestart, &s.Changed); err != nil {
			return nil, err
		}
		if enum != "" {
			s.Enum = splitUnit(enum)
		}
		// postmaster settings are read once at start; internal ones are
		// compiled in or derived, and are never written.
		s.RestartRequired = s.Context == "postmaster" || s.Context == "internal"
		s.Editable = super && s.Context != "internal"
		out = append(out, s)
	}
	return out, rows.Err()
}

// pgListSettings are the parameters whose value is a list the server quotes
// item by item. Given one quoted string they store one item — a single
// library called "a, b" — so their items are sent as separate literals.
var pgListSettings = map[string]bool{
	"search_path": true, "shared_preload_libraries": true, "session_preload_libraries": true,
	"local_preload_libraries": true, "temp_tablespaces": true, "unix_socket_directories": true,
}

// pgSettingLiteral renders a checked value for ALTER SYSTEM SET. The statement
// takes no bind marker, so the value is a quoted literal, by the same rule a
// dumped row's strings are quoted with.
func pgSettingLiteral(s Setting, value string) string {
	if !pgListSettings[s.Name] {
		return dumpString(DriverPostgres, value)
	}
	items := []string{}
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, dumpString(DriverPostgres, strings.Trim(item, `"`)))
		}
	}
	if len(items) == 0 {
		return "''"
	}
	return strings.Join(items, ", ")
}

// pgSettingName quotes the parameter's name, which for an extension's
// parameter has a dot in it and is two identifiers.
func pgSettingName(name string) (string, error) {
	parts := strings.Split(name, ".")
	for i, p := range parts {
		q, err := quoteDouble(p)
		if err != nil {
			return "", err
		}
		parts[i] = q
	}
	return strings.Join(parts, "."), nil
}

// pgApplySetting runs ALTER SYSTEM and asks the server to read its
// configuration again. ALTER SYSTEM writes postgresql.auto.conf, which is
// what makes the change outlast a restart; the reload is what makes it take
// effect now, for every parameter that does not need one.
func pgApplySetting(ctx context.Context, db *sql.DB, s Setting, stmt string) (*SettingChange, error) {
	// One connection for both: ALTER SYSTEM cannot run in a transaction
	// block, and the reload should follow it on the session that issued it.
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, stmt); err != nil {
		return nil, err
	}
	const reload = "SELECT pg_reload_conf()"
	if _, err := conn.ExecContext(ctx, reload); err != nil {
		return nil, err
	}
	out := &SettingChange{Statements: []string{stmt, reload}, Persisted: true, RestartRequired: s.Context == "postmaster"}
	// The file's own value, not the running one: the reload is delivered as
	// a signal and the running value changes a moment after this returns.
	var stored sql.NullString
	if err := conn.QueryRowContext(ctx, `
	  SELECT setting FROM pg_file_settings
	  WHERE name = $1 AND error IS NULL ORDER BY seqno DESC LIMIT 1`, s.Name).Scan(&stored); err == nil && stored.Valid {
		out.Value = stored.String
	}
	if out.RestartRequired {
		out.Note = "Stored in postgresql.auto.conf; it takes effect when the server is next restarted."
	}
	return out, nil
}

func (postgresDialect) SetSetting(ctx context.Context, db *sql.DB, current Setting, value string) (*SettingChange, error) {
	checked, err := checkSettingValue(current, value, true)
	if err != nil {
		return nil, err
	}
	name, err := pgSettingName(current.Name)
	if err != nil {
		return nil, err
	}
	out, err := pgApplySetting(ctx, db, current, "ALTER SYSTEM SET "+name+" = "+pgSettingLiteral(current, checked))
	if err != nil {
		return nil, err
	}
	if out.Value == "" {
		out.Value = checked
	}
	return out, nil
}

func (postgresDialect) ResetSetting(ctx context.Context, db *sql.DB, current Setting) (*SettingChange, error) {
	name, err := pgSettingName(current.Name)
	if err != nil {
		return nil, err
	}
	out, err := pgApplySetting(ctx, db, current, "ALTER SYSTEM RESET "+name)
	if err != nil {
		return nil, err
	}
	// Reset removes the line from postgresql.auto.conf; what applies then is
	// postgresql.conf's value, or the built-in default.
	if out.Value == "" {
		out.Value = current.Default
	}
	return out, nil
}

// --- MySQL / MariaDB ---------------------------------------------------------

// mysqlSettingCategory groups a variable by the subsystem its name starts
// with. Neither engine publishes a category, and five hundred variables in
// one list is not a page.
func mysqlSettingCategory(name string) string {
	name = strings.ToLower(name)
	for _, c := range []struct{ prefix, category string }{
		{"innodb_", "InnoDB"}, {"performance_schema", "Performance schema"}, {"aria_", "Aria"},
		{"myisam_", "MyISAM"}, {"binlog_", "Replication"}, {"log_bin", "Replication"}, {"relay_", "Replication"},
		{"replica_", "Replication"}, {"slave_", "Replication"}, {"gtid_", "Replication"}, {"rpl_", "Replication"},
		{"sync_", "Replication"}, {"wsrep_", "Galera"}, {"ssl_", "Security"}, {"tls_", "Security"},
		{"have_", "Features"}, {"character_set_", "Character sets"}, {"collation_", "Character sets"},
		{"optimizer_", "Optimizer"}, {"slow_", "Logging"}, {"log_", "Logging"}, {"general_log", "Logging"},
		{"long_query", "Logging"}, {"max_", "Limits"}, {"thread_", "Threads"}, {"table_", "Tables"},
		{"query_cache_", "Query cache"}, {"version", "Server"},
	} {
		if strings.HasPrefix(name, c.prefix) {
			return c.category
		}
	}
	return "General"
}

// mysqlInferType guesses a variable's type from its value on the engine that
// does not publish one.
func mysqlInferType(value string) string {
	switch strings.ToUpper(value) {
	case "ON", "OFF":
		return "bool"
	}
	if _, err := strconv.ParseInt(value, 10, 64); err == nil {
		return "integer"
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil && value != "" {
		return "real"
	}
	return "string"
}

// AllSettings reads the global variables. MariaDB publishes each one's type,
// bounds, default and whether it can be changed at all; MySQL publishes its
// bounds and where its value came from, and is asked for those. What MySQL
// does not say — whether a variable is read-only — only the attempt tells.
func (d mysqlDialect) AllSettings(ctx context.Context, db *sql.DB) ([]Setting, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT VARIABLE_NAME, COALESCE(GLOBAL_VALUE, ''), COALESCE(DEFAULT_VALUE, ''), VARIABLE_TYPE,
	         COALESCE(VARIABLE_COMMENT, ''), COALESCE(NUMERIC_MIN_VALUE, ''), COALESCE(NUMERIC_MAX_VALUE, ''),
	         COALESCE(ENUM_VALUE_LIST, ''), READ_ONLY, COALESCE(GLOBAL_VALUE_ORIGIN, '')
	  FROM information_schema.SYSTEM_VARIABLES
	  WHERE VARIABLE_SCOPE <> 'SESSION ONLY'
	  ORDER BY VARIABLE_NAME`)
	if err == nil {
		defer rows.Close()
		out := []Setting{}
		for rows.Next() {
			var s Setting
			var kind, enum, readOnly string
			if err := rows.Scan(&s.Name, &s.Value, &s.Default, &kind, &s.Description, &s.Min, &s.Max, &enum, &readOnly, &s.Source); err != nil {
				return nil, err
			}
			s.Name = strings.ToLower(s.Name)
			s.Category = mysqlSettingCategory(s.Name)
			switch {
			case kind == "BOOLEAN":
				s.Type = "bool"
			case kind == "ENUM":
				s.Type = "enum"
				s.Enum = strings.Split(enum, ",")
			case kind == "DOUBLE":
				s.Type = "real"
			case strings.Contains(kind, "INT"):
				s.Type = "integer"
			default:
				s.Type = "string"
			}
			s.Changed = s.Default != "" && s.Value != s.Default
			s.RestartRequired = readOnly == "YES"
			s.Context = "dynamic"
			if s.RestartRequired {
				s.Context = "read-only"
			}
			s.Editable = !s.RestartRequired
			out = append(out, s)
		}
		return out, rows.Err()
	}

	rows, err = db.QueryContext(ctx, `
	  SELECT g.VARIABLE_NAME, COALESCE(g.VARIABLE_VALUE, ''), COALESCE(i.VARIABLE_SOURCE, ''),
	         COALESCE(i.MIN_VALUE, ''), COALESCE(i.MAX_VALUE, '')
	  FROM performance_schema.global_variables g
	  LEFT JOIN performance_schema.variables_info i ON i.VARIABLE_NAME = g.VARIABLE_NAME
	  ORDER BY g.VARIABLE_NAME`)
	if err != nil {
		// 5.7 has the variables and not their provenance.
		rows, err = db.QueryContext(ctx, `
		  SELECT VARIABLE_NAME, COALESCE(VARIABLE_VALUE, ''), '', '', ''
		  FROM performance_schema.global_variables ORDER BY VARIABLE_NAME`)
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()
	out := []Setting{}
	for rows.Next() {
		var s Setting
		if err := rows.Scan(&s.Name, &s.Value, &s.Source, &s.Min, &s.Max); err != nil {
			return nil, err
		}
		s.Category = mysqlSettingCategory(s.Name)
		s.Type = mysqlInferType(s.Value)
		// The bounds are published for every variable and mean something
		// only for the integers: a string's are both zero, and a double's
		// are the bit pattern of the limit read as an integer.
		min, minErr := strconv.ParseFloat(s.Min, 64)
		max, maxErr := strconv.ParseFloat(s.Max, 64)
		if s.Type != "integer" || minErr != nil || maxErr != nil || max <= min {
			s.Min, s.Max = "", ""
		}
		s.Changed = s.Source != "" && s.Source != "COMPILED"
		s.Context = "dynamic"
		s.Editable = true
		if mysqlReadOnlyVariable(s.Name) {
			s.Context, s.RestartRequired, s.Editable = "read-only", true, false
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// MySQL before 9.0 publishes nothing that says whether a variable can be set
// while the server runs; the server only says so when a SET fails. These are
// the ones it is known to refuse — the server's identity, where its files
// are, what it was built with — so the page does not offer to edit a version
// number. The list is a courtesy, not the check: a variable missing from it
// is still refused by the server, with the server's own words.
var mysqlReadOnlyVariables = map[string]bool{
	"admin_address": true, "admin_port": true, "auto_generate_certs": true, "back_log": true,
	"basedir": true, "bind_address": true, "character_sets_dir": true, "core_file": true, "datadir": true,
	"disabled_storage_engines": true, "ft_max_word_len": true, "ft_min_word_len": true,
	"ft_stopword_file": true, "hostname": true, "innodb_buffer_pool_chunk_size": true,
	"innodb_buffer_pool_instances": true, "innodb_data_file_path": true, "innodb_data_home_dir": true,
	"innodb_doublewrite_dir": true, "innodb_flush_method": true, "innodb_log_group_home_dir": true,
	"innodb_page_size": true, "innodb_read_only": true, "innodb_undo_directory": true,
	"innodb_version": true, "large_page_size": true, "large_pages": true, "lc_messages_dir": true,
	"license": true, "log_bin": true, "log_bin_basename": true, "log_bin_index": true, "log_error": true,
	"lower_case_file_system": true, "lower_case_table_names": true, "max_digest_length": true,
	"mysqlx_bind_address": true, "mysqlx_port": true, "mysqlx_socket": true, "open_files_limit": true,
	"persisted_globals_load": true, "pid_file": true, "plugin_dir": true, "port": true,
	"protocol_version": true, "relay_log": true, "relay_log_basename": true, "relay_log_index": true,
	"report_host": true, "report_port": true, "secure_file_priv": true, "server_uuid": true,
	"skip_name_resolve": true, "skip_networking": true, "socket": true, "system_time_zone": true,
	"table_open_cache_instances": true, "thread_handling": true, "tmpdir": true,
}

func mysqlReadOnlyVariable(name string) bool {
	return mysqlReadOnlyVariables[name] || strings.HasPrefix(name, "version") ||
		strings.HasPrefix(name, "have_") || strings.HasPrefix(name, "performance_schema")
}

// mysqlSettingName bounds what reaches a SET statement as the variable's
// name: it comes from the engine's own list, and is checked anyway because
// it is written into the statement rather than bound.
var mysqlSettingName = regexp.MustCompile(`^[a-z0-9_.]+$`)

// mysqlSettingArg turns a checked value into the argument a SET takes. The
// type matters: an integer variable refuses a string, even one of digits.
func mysqlSettingArg(s Setting, checked string) any {
	switch s.Type {
	case "integer":
		if n, err := strconv.ParseInt(checked, 10, 64); err == nil {
			return n
		}
		if n, err := strconv.ParseUint(checked, 10, 64); err == nil {
			return n
		}
	case "real":
		if n, err := strconv.ParseFloat(checked, 64); err == nil {
			return n
		}
	case "bool":
		return strings.ToUpper(checked)
	}
	return checked
}

func mysqlSettingValue(ctx context.Context, db *sql.DB, name string) string {
	var v sql.NullString
	_ = db.QueryRowContext(ctx, "SELECT @@GLOBAL."+name).Scan(&v)
	return v.String
}

// SetSetting changes a global variable. MySQL 8 has SET PERSIST, which
// changes the running value and writes it to mysqld-auto.cnf in one
// statement. MariaDB has no equivalent: SET GLOBAL changes the running
// server and nothing on disk, and the answer says so rather than letting a
// change quietly disappear at the next restart.
func (d mysqlDialect) SetSetting(ctx context.Context, db *sql.DB, current Setting, value string) (*SettingChange, error) {
	if !mysqlSettingName.MatchString(current.Name) {
		return nil, settingRefused("%q is not a variable name", current.Name)
	}
	checked, err := checkSettingValue(current, value, false)
	if err != nil {
		return nil, err
	}
	verb, persisted := "SET PERSIST ", true
	if mysqlIsMariaDB(ctx, db) {
		verb, persisted = "SET GLOBAL ", false
	}
	stmt := verb + current.Name + " = ?"
	if _, err := db.ExecContext(ctx, stmt, mysqlSettingArg(current, checked)); err != nil {
		return nil, err
	}
	out := &SettingChange{Statements: []string{stmt}, Persisted: persisted, Value: mysqlSettingValue(ctx, db, current.Name)}
	if !persisted {
		out.Note = "MariaDB keeps this until the server restarts. Add it to the server's configuration file to keep it."
	}
	return out, nil
}

func (d mysqlDialect) ResetSetting(ctx context.Context, db *sql.DB, current Setting) (*SettingChange, error) {
	if !mysqlSettingName.MatchString(current.Name) {
		return nil, settingRefused("%q is not a variable name", current.Name)
	}
	out := &SettingChange{Statements: []string{}}
	if !mysqlIsMariaDB(ctx, db) {
		// Forget the persisted value first, so the default set below is not
		// overridden by the file at the next start.
		stmt := "RESET PERSIST IF EXISTS " + current.Name
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return nil, err
		}
		out.Statements = append(out.Statements, stmt)
		out.Persisted = true
	}
	stmt := "SET GLOBAL " + current.Name + " = DEFAULT"
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return nil, err
	}
	out.Statements = append(out.Statements, stmt)
	out.Value = mysqlSettingValue(ctx, db, current.Name)
	return out, nil
}

// --- SQLite --------------------------------------------------------------------

// sqlitePragma is one pragma the page lists. Only the ones stored in the
// file can be changed from here: the rest belong to a connection, and the
// pool's connections come and go, so a change to one would hold for minutes
// and then silently stop.
type sqlitePragma struct {
	name        string
	category    string
	description string
	kind        string
	enum        []string
	// file is true for a pragma whose value is written into the database.
	file     bool
	editable bool
	fallback string
}

var sqlitePragmas = []sqlitePragma{
	{name: "journal_mode", category: "Durability", kind: "enum", file: true, editable: true, fallback: "delete",
		enum:        []string{"delete", "truncate", "persist", "wal"},
		description: "How a transaction is made atomic. WAL lets readers and a writer work at once; the others block readers while writing."},
	{name: "synchronous", category: "Durability", kind: "enum", enum: sqliteSynchronous,
		description: "How hard SQLite waits for the disk before a commit returns. Set per connection."},
	{name: "auto_vacuum", category: "Storage", kind: "enum", file: true, editable: true, fallback: "none",
		enum:        sqliteAutoVacuum,
		description: "Whether freed pages are returned to the disk as rows are deleted. Changing between none and the others takes effect at the next VACUUM."},
	{name: "page_size", category: "Storage", kind: "integer", file: true,
		description: "The size of a database page in bytes, fixed when the file is created."},
	{name: "page_count", category: "Storage", kind: "integer", file: true,
		description: "The number of pages in the file."},
	{name: "freelist_count", category: "Storage", kind: "integer", file: true,
		description: "Pages the file holds that nothing uses; VACUUM returns them to the disk."},
	{name: "max_page_count", category: "Storage", kind: "integer",
		description: "The most pages the file may grow to on this connection."},
	{name: "encoding", category: "Storage", kind: "string", file: true,
		description: "The text encoding, fixed when the file is created."},
	{name: "user_version", category: "Application", kind: "integer", file: true, editable: true, fallback: "0",
		description: "A number the application stores in the file header, usually its schema version."},
	{name: "application_id", category: "Application", kind: "integer", file: true, editable: true, fallback: "0",
		description: "A number identifying which application the file belongs to."},
	{name: "schema_version", category: "Application", kind: "integer", file: true,
		description: "Incremented by SQLite each time the schema changes."},
	{name: "foreign_keys", category: "Behaviour", kind: "bool",
		description: "Whether foreign keys are enforced. Off by default in SQLite; the dashboard's own connection turns it on."},
	{name: "recursive_triggers", category: "Behaviour", kind: "bool",
		description: "Whether a trigger may fire itself."},
	{name: "secure_delete", category: "Behaviour", kind: "integer",
		description: "Whether deleted content is overwritten with zeroes."},
	{name: "trusted_schema", category: "Behaviour", kind: "bool",
		description: "Whether functions in the schema's views and triggers are trusted."},
	{name: "cache_size", category: "Memory", kind: "integer",
		description: "Pages kept in memory per connection; a negative number is kibibytes."},
	{name: "mmap_size", category: "Memory", kind: "integer",
		description: "Bytes of the file read through memory mapping."},
	{name: "temp_store", category: "Memory", kind: "integer",
		description: "Where temporary tables go: 0 the default, 1 a file, 2 memory."},
	{name: "busy_timeout", category: "Locking", kind: "integer",
		description: "Milliseconds a connection waits for a lock before giving up."},
	{name: "locking_mode", category: "Locking", kind: "string",
		description: "Whether the connection keeps the file locked between transactions."},
	{name: "wal_autocheckpoint", category: "Durability", kind: "integer",
		description: "Pages the write-ahead log grows to before SQLite checkpoints it."},
	{name: "journal_size_limit", category: "Durability", kind: "integer",
		description: "Bytes a journal or log is truncated to after a transaction; -1 is no limit."},
}

func sqlitePragmaValue(ctx context.Context, db *sql.DB, p sqlitePragma) string {
	var v sql.NullString
	if err := db.QueryRowContext(ctx, "PRAGMA "+p.name).Scan(&v); err != nil {
		return ""
	}
	// The enum pragmas answer a number; the page shows the word.
	if n, err := strconv.ParseInt(v.String, 10, 64); err == nil && p.kind == "enum" && p.name != "journal_mode" {
		return sqliteEnum(p.enum, n)
	}
	if p.kind == "bool" {
		if v.String == "1" {
			return "on"
		}
		return "off"
	}
	return v.String
}

func (sqliteDialect) AllSettings(ctx context.Context, db *sql.DB) ([]Setting, error) {
	out := []Setting{}
	for _, p := range sqlitePragmas {
		s := Setting{Name: p.name, Category: p.category, Description: p.description, Type: p.kind,
			Enum: p.enum, Editable: p.editable, Default: p.fallback, Context: "connection"}
		if p.file {
			s.Context = "file"
		}
		s.Value = sqlitePragmaValue(ctx, db, p)
		s.Changed = p.editable && s.Value != p.fallback
		out = append(out, s)
	}
	return out, nil
}

func sqlitePragmaNamed(name string) (sqlitePragma, bool) {
	for _, p := range sqlitePragmas {
		if p.name == name {
			return p, true
		}
	}
	return sqlitePragma{}, false
}

// SetSetting writes one of the pragmas the file stores. A pragma takes no
// bind marker; the value is one of a closed set of words or a whole number,
// and nothing else gets as far as the statement.
func (sqliteDialect) SetSetting(ctx context.Context, db *sql.DB, current Setting, value string) (*SettingChange, error) {
	p, ok := sqlitePragmaNamed(current.Name)
	if !ok || !p.editable {
		return nil, settingRefused("%s cannot be changed from here", current.Name)
	}
	checked, err := checkSettingValue(current, value, false)
	if err != nil {
		return nil, err
	}
	if p.kind == "integer" {
		n, err := strconv.ParseInt(checked, 10, 32)
		if err != nil {
			return nil, settingRefused("%s takes a 32-bit whole number", p.name)
		}
		checked = strconv.FormatInt(n, 10)
	}
	stmt := "PRAGMA " + p.name + " = " + checked
	out := &SettingChange{Statements: []string{stmt}, Persisted: true}
	if p.name == "journal_mode" {
		// This pragma answers with the mode now in force, which is the old
		// one when another connection had the file open.
		var now string
		if err := db.QueryRowContext(ctx, stmt).Scan(&now); err != nil {
			return nil, err
		}
		if !strings.EqualFold(now, checked) {
			return nil, settingRefused("the journal mode stayed %s: something else has the database open", now)
		}
	} else if _, err := db.ExecContext(ctx, stmt); err != nil {
		return nil, err
	}
	out.Value = sqlitePragmaValue(ctx, db, p)
	if p.name == "auto_vacuum" && !strings.EqualFold(out.Value, checked) {
		out.RestartRequired = true
		out.Note = "Stored; the file is reorganised for it at the next VACUUM."
	}
	return out, nil
}

func (d sqliteDialect) ResetSetting(ctx context.Context, db *sql.DB, current Setting) (*SettingChange, error) {
	p, ok := sqlitePragmaNamed(current.Name)
	if !ok || !p.editable {
		return nil, settingRefused("%s cannot be changed from here", current.Name)
	}
	return d.SetSetting(ctx, db, current, p.fallback)
}

// --- ClickHouse --------------------------------------------------------------

// AllSettings reads system.server_settings. They are the configuration
// files' values and are changed there; nothing here writes one. Whether a
// change would apply without a restart is a column only newer servers have.
func (clickhouseDialect) AllSettings(ctx context.Context, db *sql.DB) ([]Setting, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT name, value, default, changed, description, type, toString(changeable_without_restart)
	  FROM system.server_settings WHERE NOT is_obsolete ORDER BY name`)
	if err != nil {
		rows, err = db.QueryContext(ctx, `
		  SELECT name, value, default, changed, description, type, '' FROM system.server_settings ORDER BY name`)
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()
	out := []Setting{}
	for rows.Next() {
		var s Setting
		var kind, live string
		if err := rows.Scan(&s.Name, &s.Value, &s.Default, &s.Changed, &s.Description, &kind, &live); err != nil {
			return nil, err
		}
		switch {
		case kind == "Bool":
			s.Type = "bool"
		case strings.HasPrefix(kind, "UInt") || strings.HasPrefix(kind, "Int"):
			s.Type = "integer"
		case strings.HasPrefix(kind, "Float") || kind == "Double":
			s.Type = "real"
		default:
			s.Type = "string"
		}
		s.Source = "default"
		if s.Changed {
			s.Source = "changed"
		}
		// The column says Yes, No, or which direction a value may move
		// without a restart; an older server does not have it.
		switch live {
		case "":
		case "No":
			s.RestartRequired, s.Context = true, "restart"
		default:
			s.Context = "dynamic"
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// --- SQL Server --------------------------------------------------------------

// AllSettings reads sys.configurations. value is what was configured and
// value_in_use what the server runs with; they differ between an sp_configure
// and its RECONFIGURE, or its restart for an option that is not dynamic.
func (mssqlDialect) AllSettings(ctx context.Context, db *sql.DB) ([]Setting, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT name, CAST(value_in_use AS NVARCHAR(64)), CAST(value AS NVARCHAR(64)),
	         CAST(minimum AS NVARCHAR(64)), CAST(maximum AS NVARCHAR(64)),
	         CAST(description AS NVARCHAR(256)), is_dynamic, is_advanced
	  FROM sys.configurations
	  ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Setting{}
	for rows.Next() {
		var s Setting
		var configured string
		var dynamic, advanced bool
		if err := rows.Scan(&s.Name, &s.Value, &configured, &s.Min, &s.Max, &s.Description, &dynamic, &advanced); err != nil {
			return nil, err
		}
		s.Type = "integer"
		s.Category = "Server"
		if advanced {
			s.Category = "Advanced"
		}
		s.Source, s.Context = "startup", "startup"
		if dynamic {
			s.Source, s.Context = "dynamic", "dynamic"
		}
		s.RestartRequired = !dynamic
		s.PendingRestart = configured != s.Value
		out = append(out, s)
	}
	return out, rows.Err()
}

// --- Oracle ------------------------------------------------------------------

// The parameters an operator asks about first: how many sessions, how much
// memory, where the files are, which version's behaviour is in force.
var oracleSettingNames = []string{
	"db_name", "compatible", "processes", "sessions", "open_cursors", "cpu_count",
	"memory_target", "sga_target", "sga_max_size", "pga_aggregate_target", "db_block_size",
	"control_files", "db_recovery_file_dest", "db_recovery_file_dest_size", "undo_retention",
	"log_archive_dest_1", "optimizer_mode", "parallel_max_servers", "job_queue_processes",
	"audit_trail", "remote_login_passwordfile", "local_listener", "nls_language", "nls_territory",
}

func (d oracleDialect) Settings(ctx context.Context, db *sql.DB) ([]Setting, error) {
	all, err := d.AllSettings(ctx, db)
	if err != nil {
		return nil, err
	}
	return pickSettings(all, oracleSettingNames), nil
}

// AllSettings reads v$parameter, which needs a grant an application schema
// often lacks; the refusal is the engine's own and is passed on.
func (oracleDialect) AllSettings(ctx context.Context, db *sql.DB) ([]Setting, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT name, display_value, type, isdefault, issys_modifiable, description
	  FROM v$parameter
	  ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Setting{}
	for rows.Next() {
		var s Setting
		var kind int
		var isDefault, modifiable string
		if err := rows.Scan(&s.Name, nullText{&s.Value}, &kind, nullText{&isDefault}, nullText{&modifiable}, nullText{&s.Description}); err != nil {
			return nil, err
		}
		switch kind {
		case 1:
			s.Type = "bool"
		case 3, 6:
			s.Type = "integer"
		default:
			s.Type = "string"
		}
		s.Changed = strings.EqualFold(isDefault, "FALSE")
		// FALSE means the parameter is only read from the parameter file at
		// start; IMMEDIATE and DEFERRED can be changed on a running instance.
		s.RestartRequired = strings.EqualFold(modifiable, "FALSE")
		s.Context = strings.ToLower(modifiable)
		out = append(out, s)
	}
	return out, rows.Err()
}
