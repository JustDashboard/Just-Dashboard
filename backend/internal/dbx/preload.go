package dbx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Loading a library when the server starts: the change pg_cron,
// pg_stat_statements, auto_explain, TimescaleDB and the rest need before
// CREATE EXTENSION means anything.
//
// shared_preload_libraries is read once, by the postmaster, as it starts. So
// enabling one of these is three steps — add the library to the list, restart
// the server, create the extension — and the first is where it goes wrong by
// hand: SET the list to the new library and every library already on it is
// dropped at the next start (TimescaleDB's own, the one the Performance page
// reads); name a library the server does not have and it does not start at
// all, which on a container with a restart policy is a crash loop. So the
// list is read from the configuration files — what the next start will load,
// which after an earlier change that has not been restarted for is not what
// is loaded now — the library is added to it, every entry already there is
// kept in its order, and a library is added only once its file is found in
// the server's library directory.
//
// Restarting is the power route's, and creating the extension the create
// route's: each is its own audited request, so this file only plans the one
// statement that changes the list and the parameters the library reads at
// start.

// Preloader is the optional dialect half for an engine whose start-time
// libraries can be changed from a session.
type Preloader interface {
	ReadPreload(ctx context.Context, db *sql.DB) (*PreloadState, error)
	PlanPreload(ctx context.Context, db *sql.DB, library string) (*PreloadPlan, error)
}

// PreloaderFor returns the engine's preloader, or false where it has none.
func PreloaderFor(driver Driver) (Preloader, bool) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, false
	}
	p, ok := d.(Preloader)
	return p, ok
}

// PreloadState is what the server loaded at start and what it will load at
// the next one.
type PreloadState struct {
	// Loaded is shared_preload_libraries as the running server read it.
	Loaded []string `json:"loaded"`
	// Configured is what the configuration files name now. It differs from
	// Loaded while a change waits for a restart, which PendingRestart says.
	Configured     []string `json:"configured"`
	PendingRestart bool     `json:"pendingRestart"`
	// Settable is whether the list can be changed from this connection, and
	// Reason, in a sentence, why not.
	Settable bool   `json:"settable"`
	Reason   string `json:"reason,omitempty"`
}

// PreloadPlan is what adding one library runs.
type PreloadPlan struct {
	DDLPlan
	Library string `json:"library"`
	// Libraries is the list the server loads from its next start.
	Libraries []string `json:"libraries"`
	// Notes say what the plan did with each parameter the library reads at
	// start, where it did not simply set it.
	Notes []string `json:"notes"`
}

func (postgresDialect) ReadPreload(ctx context.Context, db *sql.DB) (*PreloadState, error) {
	return pgReadPreload(ctx, db)
}

func pgReadPreload(ctx context.Context, db *sql.DB) (*PreloadState, error) {
	var setting, source, version, user string
	var pending, super bool
	if err := db.QueryRowContext(ctx, `
	  SELECT s.setting, s.source, s.pending_restart, version(), current_user,
	         COALESCE((SELECT rolsuper FROM pg_roles WHERE rolname = current_user), false)
	  FROM pg_settings s WHERE s.name = 'shared_preload_libraries'`).
		Scan(&setting, &source, &pending, &version, &user, &super); err != nil {
		return nil, err
	}
	out := &PreloadState{Loaded: parsePreloadList(setting)}
	out.Configured = out.Loaded
	switch flavor := DetectFlavor(DriverPostgres, version); {
	case flavor == FlavorYugabyteDB, flavor == FlavorCockroachDB:
		out.Reason = FlavorLabel(flavor) + " loads its libraries from its own server flags, not from ALTER SYSTEM."
	case source == "command line":
		out.Reason = "shared_preload_libraries is given on the server's command line, which outranks ALTER SYSTEM: " +
			"a library is added there, where the server is started."
	case source != "default" && source != "configuration file":
		out.Reason = fmt.Sprintf("shared_preload_libraries comes from the %s here, which ALTER SYSTEM does not change.", source)
	case !super:
		out.Reason = fmt.Sprintf("Only a superuser can change shared_preload_libraries, and %s is not one.", user)
	default:
		out.Settable = true
	}
	// pg_file_settings is a superuser's, and only the files say what the
	// next start loads; the command line outranks them. Where they can be
	// read they are the answer: the server's own flag stays up after a
	// change has been taken back, until the next start.
	out.PendingRestart = pending
	if super && source != "command line" {
		configured, err := pgFileSetting(ctx, db, "shared_preload_libraries")
		if err != nil {
			return nil, err
		}
		out.Configured = parsePreloadList(configured.String)
		out.PendingRestart = !sameList(out.Configured, out.Loaded)
	}
	return out, nil
}

// pgFileSetting is the value the configuration files give a parameter: the
// last line naming it, which is postgresql.auto.conf's where ALTER SYSTEM
// wrote one. Null where no file names it.
func pgFileSetting(ctx context.Context, db *sql.DB, name string) (sql.NullString, error) {
	var stored sql.NullString
	err := db.QueryRowContext(ctx, `
	  SELECT setting FROM pg_file_settings WHERE name = $1 ORDER BY seqno DESC LIMIT 1`, name).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.NullString{}, nil
	}
	return stored, err
}

// pgLoadedLibraries is shared_preload_libraries as the running server read it.
func pgLoadedLibraries(ctx context.Context, db *sql.DB) ([]string, error) {
	var setting sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT current_setting('shared_preload_libraries', true)`).Scan(&setting); err != nil {
		return nil, err
	}
	return parsePreloadList(setting.String), nil
}

// pgLibrariesPresent reports which of the libraries the server's library
// directories hold — every directory of dynamic_library_path, $libdir read
// from pg_config — or nil where this account cannot look, since pg_config and
// pg_stat_file are a superuser's.
func pgLibrariesPresent(ctx context.Context, db *sql.DB, libraries []string) map[string]bool {
	rows, err := db.QueryContext(ctx, `
	  WITH dirs AS (
	    SELECT replace(trim(d), '$libdir', (SELECT setting FROM pg_config WHERE name = 'PKGLIBDIR')) AS dir
	    FROM unnest(string_to_array(current_setting('dynamic_library_path'), ':')) d
	  )
	  SELECT lib, bool_or((pg_stat_file(dir || '/' || lib || ext, true)).size IS NOT NULL)
	  FROM unnest($1::text[]) lib, dirs, unnest(ARRAY['.so', '.dylib', '.dll']) ext
	  GROUP BY lib`, libraries)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var lib string
		var found sql.NullBool
		if err := rows.Scan(&lib, &found); err != nil {
			return nil
		}
		out[lib] = found.Bool
	}
	if rows.Err() != nil {
		return nil
	}
	return out
}

func (postgresDialect) PlanPreload(ctx context.Context, db *sql.DB, name string) (*PreloadPlan, error) {
	lib, ok := PreloadLibraryFor(strings.TrimSpace(name))
	if !ok {
		return nil, fmt.Errorf("%q is not a library the dashboard loads at start; shared_preload_libraries is changed in Parameters", strings.TrimSpace(name))
	}
	state, err := pgReadPreload(ctx, db)
	if err != nil {
		return nil, planRead("shared_preload_libraries", err)
	}
	switch {
	case containsString(state.Configured, lib.Library) && containsString(state.Loaded, lib.Library):
		return nil, fmt.Errorf("%s is already loaded when the server starts", lib.Library)
	case containsString(state.Configured, lib.Library):
		return nil, fmt.Errorf("%s is already in shared_preload_libraries and is loaded once the server restarts", lib.Library)
	case !state.Settable:
		return nil, errors.New(state.Reason)
	}
	present := pgLibrariesPresent(ctx, db, []string{lib.Library})
	if present == nil {
		return nil, fmt.Errorf("the server's library directory could not be read to check that %s is installed", lib.Library)
	}
	if !present[lib.Library] {
		return nil, fmt.Errorf("this server has no %s library: it cannot load what is not installed, and told to, it would not start", lib.Library)
	}
	libraries := withPreloadLibrary(state.Configured, lib.Library, lib.Info.First)
	statements := []string{"ALTER SYSTEM SET shared_preload_libraries = " +
		pgSettingLiteral(Setting{Name: "shared_preload_libraries"}, strings.Join(libraries, ","))}
	notes := []string{}
	var database string
	if err := db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		return nil, planRead("the database's name", err)
	}
	version := pgServerVersion(ctx, db)
	for _, setting := range lib.Info.Settings {
		want := setting.Value
		if setting.Database {
			want = database
		}
		current, known, err := pgPreloadSettingNow(ctx, db, setting)
		if err != nil {
			return nil, err
		}
		stmt, note := preloadSettingStep(setting, lib.Library, current, want, known || version >= 170000, version/10000)
		if stmt != "" {
			statements = append(statements, stmt)
		}
		if note != "" {
			notes = append(notes, note)
		}
	}
	// The reload is what marks the list as waiting for a restart, and is
	// what applies a parameter set beside it that does not wait.
	statements = append(statements, "SELECT pg_reload_conf()")
	return &PreloadPlan{DDLPlan: DDLPlan{Statements: statements}, Library: lib.Library, Libraries: libraries, Notes: notes}, nil
}

// pgPreloadSettingNow is a start-time parameter's value now: the running one
// where the server knows the parameter, otherwise what the files give it,
// otherwise what it reads unset. known reports the first.
func pgPreloadSettingNow(ctx context.Context, db *sql.DB, setting PreloadSetting) (string, bool, error) {
	var running string
	err := db.QueryRowContext(ctx, `SELECT setting FROM pg_settings WHERE name = $1`, setting.Name).Scan(&running)
	switch {
	case err == nil:
		return running, true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", false, planRead(setting.Name, err)
	}
	stored, err := pgFileSetting(ctx, db, setting.Name)
	if err != nil {
		return "", false, planRead(setting.Name, err)
	}
	if stored.Valid {
		return stored.String, false, nil
	}
	return setting.Unset, false, nil
}

// preloadSettingStep decides what loading a library does with one parameter
// it reads at start: the statement that sets it, and the note that says what
// it now is. A value somebody configured is left as it is.
//
// placeable is whether the server takes the parameter before the library
// that defines it is loaded. PostgreSQL 17 does — ALTER SYSTEM writes an
// unknown dotted parameter as a placeholder the library adopts when it
// loads — and earlier releases refuse a parameter they do not know.
func preloadSettingStep(setting PreloadSetting, library, current, want string, placeable bool, major int) (string, string) {
	switch {
	case current == want:
		return "", ""
	case current != setting.Unset:
		return "", fmt.Sprintf("%s stays %s, as it is configured: %s.", setting.Name, current, setting.Why)
	case !placeable:
		return "", fmt.Sprintf("PostgreSQL %d takes %s only once %s is loaded, so it stays %s until it is set in Parameters: %s.",
			major, setting.Name, library, unsetWords(setting.Unset), setting.Why)
	}
	name, err := pgSettingName(setting.Name)
	if err != nil {
		return "", ""
	}
	return "ALTER SYSTEM SET " + name + " = " + dumpString(DriverPostgres, want),
		fmt.Sprintf("%s is set to %s: %s.", setting.Name, want, setting.Why)
}

func unsetWords(unset string) string {
	if unset == "" {
		return "empty"
	}
	return unset
}

// parsePreloadList reads a library list as the server stores it: names
// separated by commas, each perhaps in double quotes.
func parsePreloadList(value string) []string {
	out := []string{}
	var item strings.Builder
	quoted := false
	flush := func() {
		if name := strings.TrimSpace(item.String()); name != "" {
			out = append(out, strings.Trim(name, `"`))
		}
		item.Reset()
	}
	for _, r := range value {
		switch {
		case r == '"':
			quoted = !quoted
			item.WriteRune(r)
		case r == ',' && !quoted:
			flush()
		default:
			item.WriteRune(r)
		}
	}
	flush()
	return out
}

// withPreloadLibrary is the list with one library added and every entry
// already there kept in its order. A library that has to come first does.
func withPreloadLibrary(list []string, library string, first bool) []string {
	out := make([]string, 0, len(list)+1)
	if first {
		out = append(out, library)
	}
	for _, item := range list {
		if item != library {
			out = append(out, item)
		}
	}
	if !first {
		out = append(out, library)
	}
	return out
}

func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
