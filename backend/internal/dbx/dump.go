package dbx

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"modernc.org/sqlite"
)

// ConnInfo is the parsed form of a DSN, used to build dump and restore command
// lines. Credentials are deliberately kept out of argv — anyone with a shell on
// the box can read /proc/*/cmdline — and are passed via the environment or a
// mode-0600 defaults file instead.
type ConnInfo struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
}

// ParseDSN understands the URL form used by Postgres and Mongo, and both the
// URL and the go-sql-driver form for MySQL.
func ParseDSN(driver Driver, dsn string) (*ConnInfo, error) {
	if driver == DriverSQLite {
		// A SQLite DSN is a file path (optionally with ?_pragma=… query), not a
		// URL. The "database" is the file, which is what the UI shows and what
		// the dump copies.
		path := dsn
		if strings.HasPrefix(path, "file:") {
			path = strings.TrimPrefix(path, "file:")
		}
		if q := strings.IndexByte(path, '?'); q >= 0 {
			path = path[:q]
		}
		return &ConnInfo{Host: "localhost", Database: path}, nil
	}
	if driver == DriverMySQL && !strings.Contains(dsn, "://") {
		return parseMySQLDSN(dsn)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("cannot parse connection string: %w", err)
	}
	info := &ConnInfo{
		Host:     u.Hostname(),
		Port:     u.Port(),
		Database: strings.TrimPrefix(u.Path, "/"),
	}
	if u.User != nil {
		info.User = u.User.Username()
		info.Password, _ = u.User.Password()
	}
	if info.Host == "" {
		info.Host = "127.0.0.1"
	}
	// SQL Server is the one engine whose driver takes the database as a query
	// parameter rather than as the path, so reading only the path reported every
	// SQL Server connection as having no database — and the dump then refused to
	// run for want of one the connection string had all along.
	if driver == DriverMSSQL && info.Database == "" {
		q := u.Query()
		info.Database = firstNonEmpty(q.Get("database"), q.Get("Database"))
	}
	if info.Port == "" {
		info.Port = defaultPort(driver)
	}
	return info, nil
}

// defaultPort is what the engine listens on when the connection string does not
// say. Only three engines had an entry here, which left the connection detail
// the databases list shows blank for the other four.
func defaultPort(driver Driver) string {
	switch driver {
	case DriverPostgres:
		return "5432"
	case DriverMySQL:
		return "3306"
	case DriverMongo:
		return "27017"
	case DriverRedis:
		return "6379"
	case DriverMSSQL:
		return "1433"
	case DriverClickHouse:
		return "9000"
	case DriverOracle:
		return "1521"
	}
	return ""
}

// parseMySQLDSN handles user:pass@tcp(host:port)/dbname.
func parseMySQLDSN(dsn string) (*ConnInfo, error) {
	info := &ConnInfo{Host: "127.0.0.1", Port: "3306"}
	rest := dsn
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		creds := rest[:at]
		rest = rest[at+1:]
		if colon := strings.Index(creds, ":"); colon >= 0 {
			info.User, info.Password = creds[:colon], creds[colon+1:]
		} else {
			info.User = creds
		}
	}
	if open := strings.Index(rest, "("); open >= 0 {
		if close := strings.Index(rest, ")"); close > open {
			hostPort := rest[open+1 : close]
			if colon := strings.LastIndex(hostPort, ":"); colon >= 0 {
				info.Host, info.Port = hostPort[:colon], hostPort[colon+1:]
			} else if hostPort != "" {
				info.Host = hostPort
			}
			rest = rest[close+1:]
		}
	}
	if slash := strings.Index(rest, "/"); slash >= 0 {
		db := rest[slash+1:]
		if q := strings.Index(db, "?"); q >= 0 {
			db = db[:q]
		}
		info.Database = db
	}
	return info, nil
}

// DumpOptions is what a dump can be asked for beyond "this database".
//
// Not every engine can honour every option, and an option that is quietly
// ignored is worse than one that is refused: a "schema only" dump that carries
// the data anyway is a file somebody will send to the wrong person.
// ValidateDumpOptions is what a caller runs first.
type DumpOptions struct {
	// Database is the one to dump; empty means the connection's own. Redis
	// names its databases with integers and takes them in RedisDatabases.
	Database string
	// SchemaOnly writes the structure without a row; DataOnly the rows without
	// the structure they go into.
	SchemaOnly bool
	DataOnly   bool
	// Tables keeps only these (Mongo: collections); ExcludeTables leaves these
	// out. Each is "table" or "schema.table".
	Tables        []string
	ExcludeTables []string
	// Compression is "" for whatever the tool does by default, "gzip" or
	// "none".
	Compression string
	// RedisDatabases is which numbered databases to cover. Empty means every
	// one that holds a key.
	RedisDatabases []int
	// Progress receives the tool's own lines as it works, and one line per
	// table from the built-in dumper.
	Progress func(line string)

	// builtIn skips the engine's own tool. The built-in path is the one that
	// has to work on a machine with nothing installed, so it has to be
	// testable on a machine that has everything.
	builtIn bool
}

func (o DumpOptions) progress(format string, args ...any) {
	if o.Progress != nil {
		o.Progress(fmt.Sprintf(format, args...))
	}
}

func (o DumpOptions) selective() bool {
	return o.SchemaOnly || o.DataOnly || len(o.Tables) > 0 || len(o.ExcludeTables) > 0
}

// The compression settings a dump accepts.
const (
	CompressionDefault = ""
	CompressionGzip    = "gzip"
	CompressionNone    = "none"
)

// DumpCapability says which options a driver's dump honours, so the form
// offers what the request will not refuse.
type DumpCapability struct {
	SchemaOnly  bool `json:"schemaOnly"`
	DataOnly    bool `json:"dataOnly"`
	Tables      bool `json:"tables"`
	Compression bool `json:"compression"`
	// Databases is Redis's choice of numbered databases.
	Databases bool `json:"databases"`
	// NewDatabase is whether a dump can be restored into a database made for
	// it rather than over the one it came from.
	NewDatabase bool `json:"newDatabase"`
}

// DumpCapabilities reports what a dump of this engine can be asked for.
func DumpCapabilities(driver Driver) DumpCapability {
	switch driver {
	case DriverPostgres, DriverMySQL, DriverMSSQL:
		return DumpCapability{SchemaOnly: true, DataOnly: true, Tables: true, Compression: true, NewDatabase: true}
	case DriverClickHouse, DriverOracle:
		// ClickHouse statements name their database and Oracle's "database" is
		// a user, so neither dump can be pointed at another one on the way in.
		return DumpCapability{SchemaOnly: true, DataOnly: true, Tables: true, Compression: true}
	case DriverSQLite:
		// A SQLite database is one file; a second one is a second connection.
		return DumpCapability{SchemaOnly: true, DataOnly: true, Tables: true, Compression: true}
	case DriverMongo:
		// mongodump has no way to leave the documents out, and nothing to put
		// in a dump of documents with no collections.
		return DumpCapability{Tables: true, Compression: true, NewDatabase: true}
	case DriverRedis:
		return DumpCapability{Databases: true, NewDatabase: true}
	}
	return DumpCapability{}
}

// ValidateDumpOptions refuses a combination the engine cannot honour, before
// anything is written.
func ValidateDumpOptions(driver Driver, opts DumpOptions) error {
	has := DumpCapabilities(driver)
	if opts.SchemaOnly && opts.DataOnly {
		return fmt.Errorf("schema only and data only together leave nothing to dump")
	}
	if opts.SchemaOnly && !has.SchemaOnly {
		return fmt.Errorf("a %s dump cannot leave the data out", driver)
	}
	if opts.DataOnly && !has.DataOnly {
		return fmt.Errorf("a %s dump cannot leave the structure out", driver)
	}
	if (len(opts.Tables) > 0 || len(opts.ExcludeTables) > 0) && !has.Tables {
		return fmt.Errorf("a %s dump cannot be narrowed to some tables", driver)
	}
	if len(opts.RedisDatabases) > 0 && !has.Databases {
		return fmt.Errorf("only a Redis dump chooses numbered databases")
	}
	for _, n := range opts.RedisDatabases {
		if n < 0 {
			return fmt.Errorf("redis databases are numbered from 0; %d is not one", n)
		}
	}
	switch opts.Compression {
	case CompressionDefault:
	case CompressionGzip, CompressionNone:
		if !has.Compression {
			return fmt.Errorf("a %s dump has one format; compression is not a choice", driver)
		}
	default:
		return fmt.Errorf("compression is gzip or none, not %q", opts.Compression)
	}
	if _, err := newDumpSelection(opts.Tables, opts.ExcludeTables); err != nil {
		return err
	}
	return nil
}

type DumpResult struct {
	Path string `json:"path"`
	// File is the base name of Path. The browser needs it to ask for the dump
	// back, and splitting a path in TypeScript is a separator assumption this
	// side of the wire already knows the answer to.
	File     string `json:"file"`
	Size     int64  `json:"size"`
	Duration string `json:"duration"`
	Database string `json:"database"`
	Driver   Driver `json:"driver"`
	// Summary is one line describing what is in the file — "4 tables, 1054
	// rows", "306 keys". Separate from Output because Output is whatever the
	// tool said, and mongodump says a timestamped paragraph: a dump of nothing
	// and a dump of everything both end in success, and the only thing that
	// tells them apart on screen is this.
	Summary   string    `json:"summary,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	Output    string    `json:"output,omitempty"`
	// Tool is what wrote the file — pg_dump, mysqldump, mongodump — or
	// "built-in" for the dashboard's own dumper. ToolVersion is the tool's own
	// word for its version; the built-in dumper's is the dashboard's, which the
	// caller knows and this package does not.
	Tool        string `json:"tool,omitempty"`
	ToolVersion string `json:"toolVersion,omitempty"`
}

// BuiltInDumpTool is the Tool of a dump the dashboard wrote itself.
const BuiltInDumpTool = "built-in"

// Dump writes a backup of one database into outDir and returns where it landed.
//
// Every engine this dashboard can connect to can be dumped. Where a native tool
// exists and is installed it is used, because its format is the one the engine's
// own restore path is fastest and most faithful with; where it does not, or
// where it fails, dbx writes the dump itself over the connection it already has
// (dump_sql.go, dump_nosql.go).
//
// The fallback is not a nicety. "unsupported database driver: clickhouse" was
// the dashboard admitting, at the moment the operator pressed the button, that
// the backup they were relying on had never been possible — which is the worst
// time to find out and the reason a backup feature exists at all.
func Dump(ctx context.Context, driver Driver, dsn, database, outDir string) (*DumpResult, error) {
	return DumpWith(ctx, driver, dsn, outDir, DumpOptions{Database: database})
}

// DumpWith is Dump with the options a caller may pass.
func DumpWith(ctx context.Context, driver Driver, dsn, outDir string, opts DumpOptions) (*DumpResult, error) {
	if err := ValidateDumpOptions(driver, opts); err != nil {
		return nil, err
	}
	staging, discard, err := NewDumpStaging(outDir, "dump")
	if err != nil {
		return nil, err
	}
	defer discard()
	res, err := runDump(ctx, driver, dsn, staging, opts)
	if err != nil && ctx.Err() != nil {
		// The tool's last words when it is killed are "terminated by signal",
		// which is true and says nothing about why.
		return nil, fmt.Errorf("dump stopped: %w", context.Cause(ctx))
	}
	if err != nil {
		return nil, err
	}
	// Two dumps taken in the same second are after the same name — one taken
	// to keep the current state follows the dump it is about to be replaced
	// from by less than that — and the later one takes the name with a number.
	name := filepath.Base(res.Path)
	for n := 1; ; n++ {
		path, err := PlaceDump(res.Path, outDir, numberedDumpName(name, n))
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		res.Path, res.File = path, filepath.Base(path)
		break
	}
	if res.Tool == "" {
		res.Tool = BuiltInDumpTool
	}
	return res, nil
}

// Where a dump is while it is not yet whole.
//
// A dump used to be written straight under its name, so for as long as it took
// the directory held a file that looked like a backup and was not one, and the
// listing had to guess which that was from its age and from a job happening to
// be running. It is written in a directory of its own instead, inside the one
// it is bound for — the same volume, so the move at the end is a rename — under
// a name the listing does not read, and given its own name once it is whole.
// An upload arrives the same way.

// dumpStagingStale is the age at which a staging directory can only be what a
// process that died left behind: twice the longest a transfer is allowed.
const dumpStagingStale = 24 * time.Hour

// NewDumpStaging makes the directory a dump is written in before it has its
// name, inside dir, and returns it with what removes it. kind says what is
// arriving: "dump" or "upload".
func NewDumpStaging(dir, kind string) (string, func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, err
	}
	// Nobody can delete from the page what the page does not list, so what an
	// earlier process left is cleared here.
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !e.IsDir() || (!strings.HasPrefix(e.Name(), ".dump-") && !strings.HasPrefix(e.Name(), ".upload-")) {
				continue
			}
			if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > dumpStagingStale {
				os.RemoveAll(filepath.Join(dir, e.Name()))
			}
		}
	}
	staging, err := os.MkdirTemp(dir, "."+kind+"-")
	if err != nil {
		return "", nil, err
	}
	return staging, func() { os.RemoveAll(staging) }, nil
}

// PlaceDump gives a dump that is whole its name in dir.
//
// The name is claimed by creating it, which only one writer can do, and the
// dump is then moved onto the claim. Checking that the name is free and
// renaming onto it are two steps, and between them a second upload of the
// same name — or a dump finishing in the same second — used to land on the
// first. The loser now gets fs.ErrExist.
func PlaceDump(staged, dir, name string) (string, error) {
	path := filepath.Join(dir, name)
	claim, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("%w: %s", fs.ErrExist, name)
	}
	if err != nil {
		return "", err
	}
	claim.Close()
	if err := os.Rename(staged, path); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// runDump is Dump without the bookkeeping, so the several places that build a
// result do not each have to remember to fill in every derived field.
func runDump(ctx context.Context, driver Driver, dsn, outDir string, opts DumpOptions) (*DumpResult, error) {
	info, err := ParseDSN(driver, dsn)
	if err != nil {
		return nil, err
	}
	if driver == DriverSQLite {
		if opts.selective() || opts.Compression == CompressionGzip {
			// VACUUM INTO copies the file whole. Anything narrower is the SQL
			// dump, which reads the same file through the same engine.
			return dumpBuiltInSQL(ctx, driver, dsn, outDir, opts)
		}
		return dumpSQLite(ctx, dsn, info.Database, outDir)
	}
	if driver == DriverRedis {
		// Redis names its databases with integers, so it validates its own.
		return dumpRedis(ctx, dsn, outDir, opts)
	}
	if opts.Database == "" {
		opts.Database = info.Database
	}
	if err := validateDumpDatabase(opts.Database); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return nil, err
	}

	fallback := func() (*DumpResult, error) {
		if driver == DriverMongo {
			return dumpMongoDriver(ctx, dsn, outDir, opts)
		}
		return dumpBuiltInSQL(ctx, driver, dsn, outDir, opts)
	}
	if opts.builtIn {
		return fallback()
	}
	native, tool := nativeDumpCommand(ctx, driver, dsn, info, outDir, opts)
	if native == nil {
		return fallback()
	}
	res, err := native()
	if err == nil {
		return res, nil
	}
	if ctx.Err() != nil {
		// Stopped, not refused: a second attempt by another route is exactly
		// what whoever stopped it did not ask for.
		return nil, err
	}
	// The native tool is there and refused. Everything that makes it refuse a
	// dump — a version it will not read, a feature the server has and it does
	// not, a missing helper of its own — is something the driver path does not
	// care about, so it is worth the second attempt before reporting a failure.
	opts.progress("%s refused (%v); writing the dump over the dashboard's own connection instead", tool, err)
	fb, ferr := fallback()
	if ferr != nil {
		return nil, fmt.Errorf("%w (and the built-in dump also failed: %v)", err, ferr)
	}
	fb.Summary += " (built-in dump; " + tool + " refused)"
	fb.Output = strings.TrimSpace(fmt.Sprintf("%s\n%s reported: %v", fb.Output, tool, err))
	return fb, nil
}

// nativeDumpCommand returns a closure running the engine's own dump tool, or
// nil when that tool is not installed. Deciding here rather than inside Dump
// keeps "which tool, and is it present" in one place for the three engines that
// have one.
func nativeDumpCommand(ctx context.Context, driver Driver, dsn string, info *ConnInfo, outDir string, opts DumpOptions) (func() (*DumpResult, error), string) {
	start := time.Now()
	database := opts.Database
	sel, err := newDumpSelection(opts.Tables, opts.ExcludeTables)
	if err != nil {
		return nil, ""
	}
	switch driver {
	case DriverPostgres:
		tool := postgresTool("pg_dump", postgresServerMajor(ctx, dsn))
		if !toolAvailable(tool) {
			return nil, ""
		}
		path := freeDumpPath(outDir, dumpFilename(database, "postgres", "dump", start))
		return func() (*DumpResult, error) {
			run := toolRun{
				name: tool, args: pgDumpArgs(info, database, path, opts, sel),
				env: []string{"PGPASSWORD=" + info.Password}, progress: opts.Progress,
			}
			return runDumpCommand(ctx, run, path, driver, database, start)
		}, filepath.Base(tool)
	case DriverMySQL:
		tool := firstAvailableTool("mysqldump", "mariadb-dump")
		if tool == "" {
			return nil, ""
		}
		ext := "sql"
		if opts.Compression == CompressionGzip {
			ext = "sql.gz"
		}
		path := freeDumpPath(outDir, dumpFilename(database, "mysql", ext, start))
		return func() (*DumpResult, error) {
			defaults, cleanup, err := mysqlDefaultsFile(info)
			if err != nil {
				return nil, err
			}
			defer cleanup()
			args := mysqldumpArgs(defaults, database, opts, sel)
			// The dump goes to this process rather than to a file of the
			// tool's own, which is what lets it be compressed on the way.
			out, err := newDumpFile(path, opts.Compression == CompressionGzip)
			if err != nil {
				return nil, err
			}
			run := toolRun{name: tool, args: args, stdout: out, progress: opts.Progress}
			res, err := runDumpCommand(ctx, run, path, driver, database, start)
			if cerr := out.Close(); cerr != nil && err == nil {
				os.Remove(path)
				return nil, cerr
			}
			if err == nil {
				if st, serr := os.Stat(path); serr == nil {
					res.Size = st.Size()
				}
			}
			return res, err
		}, tool
	case DriverMongo:
		if !toolAvailable("mongodump") {
			return nil, ""
		}
		ext := "archive"
		path := freeDumpPath(outDir, dumpFilename(database, "mongo", ext, start))
		return func() (*DumpResult, error) {
			// The tool refuses a --db that differs from the database the
			// connection string names, so the string is pointed at the one
			// being dumped.
			conf, cleanup, err := mongoConfigFile(mongoURIForDatabase(dsn, database))
			if err != nil {
				return nil, err
			}
			defer cleanup()
			include, exclude, err := mongoToolSelection(ctx, dsn, database, sel)
			if err != nil {
				return nil, err
			}
			args := mongodumpArgs(conf, database, path, opts.Compression != CompressionNone, include, exclude)
			run := toolRun{name: "mongodump", args: args, progress: opts.Progress}
			return runDumpCommand(ctx, run, path, driver, database, start)
		}, "mongodump"
	}
	return nil, ""
}

// runDumpCommand executes one dump tool and turns its exit into a result.
func runDumpCommand(ctx context.Context, run toolRun, path string, driver Driver, database string, start time.Time) (*DumpResult, error) {
	output, err := run.run(ctx)
	if err != nil {
		os.Remove(path)
		// The tool's own output when it produced any, and the exec error when
		// it did not. A missing binary writes nothing to stderr at all, so the
		// message was "dump failed:" followed by nothing — which says something
		// went wrong and withholds the only useful part, the name of the tool
		// that is not installed.
		if detail := strings.TrimSpace(output); detail != "" {
			return nil, fmt.Errorf("dump failed: %s", lastLines(detail, 12))
		}
		return nil, fmt.Errorf("dump failed: %w", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	tool := filepath.Base(run.name)
	return &DumpResult{
		Path: path, Size: st.Size(), Driver: driver, Database: database,
		Duration:  time.Since(start).Round(time.Millisecond).String(),
		StartedAt: start.UTC(), Summary: "written by " + tool,
		Output: lastLines(strings.TrimSpace(output), 40),
		Tool:   tool, ToolVersion: toolVersion(ctx, run.name),
	}, nil
}

// lastLines keeps the end of a tool's output. A verbose dump of a thousand
// tables says a thousand things, and the part worth keeping in a result or an
// error is how it finished.
func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// validateDumpDatabase refuses only what cannot be handled safely rather than a
// conservative character class.
//
// The old rule was identifierRe, which requires an ASCII letter first and
// permits nothing but letters, digits, underscore and dollar — so a database
// called "my-app" or "café" could not be dumped at all, despite being a name
// every engine here accepts. Nothing needs that strictness now: the name goes
// into argv (never a shell), into a quoted SQL identifier, or into a filename
// that dumpFilename sanitises separately.
func validateDumpDatabase(database string) error {
	if strings.TrimSpace(database) == "" {
		return fmt.Errorf("no database named in the connection string; specify one explicitly")
	}
	if strings.HasPrefix(database, "-") {
		// Every tool is given its arguments as option=value, so a name cannot
		// become an option. This is the second lock on the same door.
		return fmt.Errorf("a database name cannot begin with a dash")
	}
	return validateIdent(database)
}

// RestoreOptions is what a restore can be told beyond which file.
type RestoreOptions struct {
	// Database is the one to load into; empty means the connection's own.
	Database string
	// SourceDatabase is the database the dump was taken of, when the caller
	// knows it. Only a mongodump archive needs it, and only to be loaded under
	// another name: its documents carry the name they came from.
	SourceDatabase string
	Progress       func(line string)
}

func (o RestoreOptions) progress(format string, args ...any) {
	if o.Progress != nil {
		o.Progress(fmt.Sprintf(format, args...))
	}
}

// Restore loads a dump back into a database. This overwrites live data, which
// is why the route in front of it sits in the destructive group.
//
// Which reader to use is decided by the file rather than by the engine: a
// Postgres connection may hold either a pg_dump archive or the SQL text this
// package writes, and picking by driver would refuse one of them for no reason.
func Restore(ctx context.Context, driver Driver, dsn, database, dumpPath string) (string, error) {
	return RestoreWith(ctx, driver, dsn, dumpPath, RestoreOptions{Database: database})
}

// RestoreWith is Restore with the options a caller may pass.
func RestoreWith(ctx context.Context, driver Driver, dsn, dumpPath string, opts RestoreOptions) (string, error) {
	out, err := runRestore(ctx, driver, dsn, dumpPath, opts)
	if err != nil && ctx.Err() != nil {
		return out, fmt.Errorf("restore stopped: %w", context.Cause(ctx))
	}
	return out, err
}

func runRestore(ctx context.Context, driver Driver, dsn, dumpPath string, opts RestoreOptions) (string, error) {
	info, err := ParseDSN(driver, dsn)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dumpPath); err != nil {
		return "", fmt.Errorf("dump file not readable: %w", err)
	}
	format := dumpFormatOf(dumpPath)
	if driver == DriverSQLite {
		if format == dumpFormatSQLText {
			return restoreGenericSQL(ctx, driver, dsn, "", dumpPath, opts)
		}
		return restoreSQLite(dumpPath, info.Database)
	}
	database := opts.Database
	if driver == DriverRedis {
		return restoreRedis(ctx, dsn, database, dumpPath, opts)
	}
	if database == "" {
		database = info.Database
	}
	if err := validateDumpDatabase(database); err != nil {
		return "", err
	}
	switch format {
	case dumpFormatArchive:
		if driver != DriverMongo {
			return "", fmt.Errorf("this is a Mongo archive; the connection is %s", driver)
		}
		return restoreMongoDriver(ctx, dsn, database, dumpPath, opts)
	case dumpFormatSQLText:
		return restoreGenericSQL(ctx, driver, dsn, database, dumpPath, opts)
	}

	var run toolRun
	switch driver {
	case DriverPostgres:
		major := postgresServerMajor(ctx, dsn)
		if format == dumpFormatForeignSQL || format == dumpFormatNativeGzip {
			// A plain-format pg_dump is a psql script: its rows travel as COPY
			// blocks, which are not statements and which only psql reads.
			tool := postgresTool("psql", major)
			if !toolAvailable(tool) {
				return "", fmt.Errorf("this is a plain SQL dump and psql is not installed to replay it")
			}
			if other, err := psqlScriptReconnects(dumpPath); err != nil {
				return "", err
			} else if other != "" {
				return "", fmt.Errorf("this script connects to another database (%s), so it cannot be restored into %s; "+
					"take the dump of one database without --create, or replay it with psql yourself", other, database)
			}
			f, closeDump, err := openDumpText(dumpPath)
			if err != nil {
				return "", err
			}
			defer closeDump()
			run = toolRun{
				name: tool, stdin: f, env: []string{"PGPASSWORD=" + info.Password},
				args: psqlArgs(info, database),
			}
			break
		}
		tool := postgresTool("pg_restore", major)
		if !toolAvailable(tool) {
			return "", fmt.Errorf("this is a pg_dump custom-format archive and pg_restore is not installed; " +
				"re-take the backup to get one this dashboard can restore on its own")
		}
		run = toolRun{
			name: tool, env: []string{"PGPASSWORD=" + info.Password},
			args: pgRestoreArgs(info, database, dumpPath),
		}
	case DriverMySQL:
		tool := firstAvailableTool("mysql", "mariadb")
		if tool == "" {
			return restoreGenericSQL(ctx, driver, dsn, database, dumpPath, opts)
		}
		if other, err := mysqlScriptSwitches(dumpPath, database); err != nil {
			return "", err
		} else if other != "" {
			return "", fmt.Errorf("this script switches to another database (%s), so it cannot be restored into %s; "+
				"take the dump of one database without --databases, or replay it with the mysql client yourself", other, database)
		}
		defaults, cleanup, err := mysqlDefaultsFile(info)
		if err != nil {
			return "", err
		}
		defer cleanup()
		f, closeDump, err := openDumpText(dumpPath)
		if err != nil {
			return "", err
		}
		defer closeDump()
		run = toolRun{name: tool, args: mysqlArgs(defaults, database), stdin: f}
	case DriverMongo:
		if !toolAvailable("mongorestore") {
			return "", fmt.Errorf("this is a mongodump archive and mongorestore is not installed; " +
				"re-take the backup to get one this dashboard can restore on its own")
		}
		// An archive names the database each collection came from, and the
		// tool puts it back there unless told otherwise. Where it came from
		// is read out of the archive itself, so that "restore into this
		// database" never writes to another one: a dump of prod uploaded to
		// the staging connection used to be restored over prod.
		source := opts.SourceDatabase
		if names := mongoArchiveDatabases(dumpPath); len(names) == 1 {
			source = names[0]
		} else if len(names) > 1 {
			// A dump of several databases: only the part that is this one's.
			source = database
		}
		// With no database in the connection string the tool takes the
		// archive's own word for the namespaces, which is the only thing the
		// options can then confine and redirect.
		conf, cleanup, err := mongoConfigFile(mongoURIForDatabase(dsn, ""))
		if err != nil {
			return "", err
		}
		defer cleanup()
		run = toolRun{
			name: "mongorestore",
			args: mongorestoreArgs(conf, dumpPath, format == dumpFormatNativeGzip, source, database),
		}
	default:
		return "", fmt.Errorf("%s is not a dump this dashboard wrote, and there is no %s client here to replay it",
			filepath.Base(dumpPath), driver)
	}

	run.progress = opts.Progress
	output, err := run.run(ctx)
	if err != nil {
		if detail := strings.TrimSpace(output); detail != "" {
			return output, fmt.Errorf("restore failed: %s", lastLines(detail, 12))
		}
		return output, fmt.Errorf("restore failed: %w", err)
	}
	return lastLines(strings.TrimSpace(output), 40), nil
}

// dumpFormat is what a dump file turns out to be.
type dumpFormat int

const (
	// dumpFormatNative is a tool's own format — a pg_dump custom archive, a
	// mongodump archive, a mysqldump script — and is replayed by that tool.
	dumpFormatNative dumpFormat = iota
	// dumpFormatSQLText is the SQL this package writes, gzipped or not.
	dumpFormatSQLText
	// dumpFormatArchive is the gzipped JSON Lines this package writes for the
	// engines that are not SQL.
	dumpFormatArchive
	// dumpFormatNativeGzip is a tool's own format, gzipped: a mongodump
	// archive taken with --gzip, a mysqldump script compressed on the way out.
	dumpFormatNativeGzip
	// dumpFormatForeignSQL is SQL text something else wrote: a plain pg_dump,
	// a mysqldump, a script somebody uploaded.
	dumpFormatForeignSQL
)

// dumpHeader is the first line of every SQL dump this package writes, and what
// tells one of them from SQL anybody else wrote.
const dumpHeader = "-- Just Dashboard dump"

// dumpFormatOf reads the first bytes of the file rather than trusting its name.
// A dump gets renamed, and restoring a file with the wrong reader produces an
// error about syntax that says nothing about the actual mistake.
func dumpFormatOf(path string) dumpFormat {
	f, err := os.Open(path)
	if err != nil {
		return dumpFormatNative
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, []byte("PGDMP")):
		// pg_dump's custom format, which only pg_restore reads.
		return dumpFormatNative
	case bytes.HasPrefix(head, []byte(dumpHeader)):
		return dumpFormatSQLText
	case bytes.HasPrefix(head, []byte{0x1f, 0x8b}):
		// gzip: this package's JSON Lines archive, its SQL text compressed, or
		// a tool's own output. The archive is told by the extension its writer
		// chose; the SQL by the header inside, which is one small read away.
		if strings.HasSuffix(path, ".jsonl.gz") {
			return dumpFormatArchive
		}
		if inner := gzipHead(path, len(dumpHeader)); bytes.Equal(inner, []byte(dumpHeader)) {
			return dumpFormatSQLText
		}
		return dumpFormatNativeGzip
	case looksLikeSQLText(head):
		return dumpFormatForeignSQL
	default:
		return dumpFormatNative
	}
}

// gzipHead reads the first n bytes inside a gzip file.
func gzipHead(path string, n int) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil
	}
	defer gz.Close()
	head := make([]byte, n)
	got, _ := io.ReadFull(gz, head)
	return head[:got]
}

// looksLikeSQLText tells a script from an archive by what an archive is not:
// text. A tar or a mongodump archive has a NUL in its first block.
func looksLikeSQLText(head []byte) bool {
	return len(head) > 0 && !bytes.ContainsRune(head, 0) && isPrintableText(head[:validPrefix(head)])
}

// validPrefix trims a head that was cut in the middle of a multi-byte
// character, which is the buffer's doing and not the file's.
func validPrefix(b []byte) int {
	for i := len(b); i > 0 && i > len(b)-4; i-- {
		if isPrintableText(b[:i]) {
			return i
		}
	}
	return len(b)
}

// A script that changes database carries on in whichever one it named, and
// that is not a restore into this one. pg_dump writes a \connect when asked to
// recreate the database (--create) and pg_dumpall one per database; mysqldump
// writes a USE when given --databases. The clients have no option that keeps
// a script where it was started — mysql's --one-database skips everything
// until the first USE — so the script is read for the line first.
//
// A line of row data cannot be mistaken for either: both tools write a line
// break inside a value as an escape, and pg_dump writes a backslash as two.

// psqlScriptReconnects returns the \connect line of a psql script, if any.
func psqlScriptReconnects(path string) (string, error) {
	return scriptLine(path, func(line []byte) bool {
		return bytes.HasPrefix(line, []byte(`\connect`)) || bytes.HasPrefix(line, []byte(`\c `))
	})
}

// mysqlUseLine matches a USE statement on a line of its own.
var mysqlUseLine = regexp.MustCompile("(?i)^USE\\s+`?([^`;\\s]+)`?\\s*;")

// mysqlScriptSwitches returns the USE line of a MySQL script that names a
// database other than the target.
func mysqlScriptSwitches(path, target string) (string, error) {
	return scriptLine(path, func(line []byte) bool {
		m := mysqlUseLine.FindSubmatch(line)
		return m != nil && string(m[1]) != target
	})
}

// scriptLine returns the first line of a script that match accepts. Only the
// start of a line is looked at, so a line longer than the buffer is read in
// pieces and all but the first let go.
func scriptLine(path string, match func(line []byte) bool) (string, error) {
	text, closeDump, err := openDumpText(path)
	if err != nil {
		return "", err
	}
	defer closeDump()
	r := bufio.NewReaderSize(text, 64<<10)
	for {
		line, isPrefix, err := r.ReadLine()
		if err == io.EOF {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		if match(line) {
			return strings.TrimSpace(string(line[:min(len(line), 120)])), nil
		}
		for isPrefix && err == nil {
			_, isPrefix, err = r.ReadLine()
		}
	}
}

// openDumpText opens a SQL script for a client's standard input, decompressing
// it on the way when it was written compressed.
func openDumpText(path string) (io.Reader, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	head := make([]byte, 2)
	n, _ := io.ReadFull(f, head)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, nil, err
	}
	if n == 2 && head[0] == 0x1f && head[1] == 0x8b {
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, nil, err
		}
		return gz, func() { gz.Close(); f.Close() }, nil
	}
	return f, func() { f.Close() }, nil
}

// dumpFile is where a dump is written when this process holds the pen: a file
// created private, optionally gzipped, and synced before it is called written.
type dumpFile struct {
	f  *os.File
	gz *gzip.Writer
}

func newDumpFile(path string, compress bool) (*dumpFile, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	d := &dumpFile{f: f}
	if compress {
		d.gz = gzip.NewWriter(f)
	}
	return d, nil
}

func (d *dumpFile) Write(p []byte) (int, error) {
	if d.gz != nil {
		return d.gz.Write(p)
	}
	return d.f.Write(p)
}

func (d *dumpFile) Close() error {
	var first error
	if d.gz != nil {
		first = d.gz.Close()
	}
	if err := d.f.Sync(); err != nil && first == nil {
		first = err
	}
	if err := d.f.Close(); err != nil && first == nil {
		first = err
	}
	return first
}

// dumpSQLite writes a consistent snapshot of a SQLite file with VACUUM INTO,
// which the engine performs against a live database without blocking it — a
// plain file copy could capture a torn write mid-transaction. The result is an
// ordinary SQLite file, so a "restore" is just copying it back.
func dumpSQLite(ctx context.Context, dsn, path, outDir string) (*DumpResult, error) {
	if path == "" {
		return nil, fmt.Errorf("connection has no database file path")
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return nil, err
	}
	start := time.Now()
	stamp := time.Now().UTC().Format("20060102-150405")
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if base == "" {
		base = "sqlite"
	}
	out := freeDumpPath(outDir, fmt.Sprintf("%s-%s.sqlite", base, stamp))

	db, err := sql.Open("sqlite", sqliteDialect{}.NormaliseDSN(dsn))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	// VACUUM INTO takes a string literal, not a bound parameter, and there is no
	// identifier form to quote — a single-quoted SQLite string literal escapes
	// one character (the quote, by doubling), so that is the whole escaping.
	lit := "'" + strings.ReplaceAll(out, "'", "''") + "'"
	if _, err := db.ExecContext(ctx, "VACUUM INTO "+lit); err != nil {
		os.Remove(out)
		return nil, fmt.Errorf("dump failed: %w", err)
	}
	st, err := os.Stat(out)
	if err != nil {
		return nil, err
	}
	var tables int
	// The underscore is escaped: bare, it is a wildcard, and a table called
	// "sqlitex" was counted out as one of the engine's own.
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\'`).
		Scan(&tables)
	return &DumpResult{
		Path: out, Size: st.Size(), Driver: DriverSQLite, Database: filepath.Base(path),
		Duration: time.Since(start).Round(time.Millisecond).String(), StartedAt: start.UTC(),
		Summary: fmt.Sprintf("%d tables", tables),
	}, nil
}

// sqliteMagic is the sixteen bytes every SQLite database file starts with.
const sqliteMagic = "SQLite format 3\x00"

// restoreSQLite replaces the contents of the live database with a dump's. The
// previous contents are kept alongside as <name>.bak-<stamp> rather than
// discarded, so a restore from the wrong dump is recoverable — the same guard
// WriteComposeFile applies.
//
// The dump is loaded through SQLite's own online backup, into the file that is
// already there, under the engine's locks. Writing over that file by hand is
// what this used to do, and it is wrong whichever way it is done: truncate and
// copy, and a program with the database open reads a file that is half one
// database and half another; rename a new file into place, and that program
// goes on writing to the old one, which no longer has a name. Loaded through
// the engine, every connection — the dashboard's and anybody else's — sees the
// old database, then the new one, and nothing in between.
func restoreSQLite(dumpPath, target string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("connection has no database file path")
	}
	if err := checkSQLiteFile(dumpPath); err != nil {
		return "", err
	}
	st, err := os.Stat(target)
	if errors.Is(err, os.ErrNotExist) {
		// Nothing is there to be open or to keep: the dump becomes the file.
		if err := copyFile(dumpPath, target, 0o600); err != nil {
			return "", fmt.Errorf("restore failed: %w", err)
		}
		return "restored " + target, nil
	}
	if err != nil {
		return "", err
	}

	kept, err := keepSQLiteCopy(target, st.Mode().Perm())
	if err != nil {
		// A restore with no way back is not one to carry on with.
		return "", fmt.Errorf("could not keep a copy of the current database, so nothing was replaced: %w", err)
	}
	if err := restoreSQLiteOnline(dumpPath, target); err != nil {
		// The file that is there cannot be opened as a database — which may be
		// exactly why it is being restored. It is replaced whole, along with
		// the write-ahead log that belonged to it: left behind, SQLite would
		// replay the old database's last transactions into the new one.
		if rerr := replaceSQLiteFile(dumpPath, target, st.Mode().Perm()); rerr != nil {
			return "", fmt.Errorf("restore failed: %v (and replacing the file also failed: %w)", err, rerr)
		}
	}
	return fmt.Sprintf("restored %s (the previous contents are kept as %s)", target, filepath.Base(kept)), nil
}

func checkSQLiteFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, len(sqliteMagic))
	if _, err := io.ReadFull(f, head); err != nil || string(head) != sqliteMagic {
		return fmt.Errorf("%s is not a SQLite database file", filepath.Base(path))
	}
	return nil
}

// sqliteFileURI names a file to the driver without letting anything in the
// name be read as a parameter.
func sqliteFileURI(path, query string) string {
	escaped := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
	return "file:" + escaped + "?" + query
}

// keepSQLiteCopy copies the current database aside and returns where. The
// engine's own snapshot is preferred, since it is whole whatever is being
// written at the time; a database too damaged to be read that way is copied
// byte for byte, which keeps whatever is there to be recovered from.
func keepSQLiteCopy(target string, perm os.FileMode) (string, error) {
	kept := fmt.Sprintf("%s.bak-%s", target, time.Now().UTC().Format("20060102-150405"))
	for n := 2; ; n++ {
		if _, err := os.Lstat(kept); errors.Is(err, os.ErrNotExist) {
			break
		}
		// Two restores inside one second each keep their own copy.
		kept = fmt.Sprintf("%s.bak-%s-%d", target, time.Now().UTC().Format("20060102-150405"), n)
	}
	db, err := sql.Open("sqlite", sqliteFileURI(target, "_pragma=busy_timeout(15000)"))
	if err == nil {
		_, err = db.Exec("VACUUM INTO '" + strings.ReplaceAll(kept, "'", "''") + "'")
		db.Close()
		if err == nil {
			return kept, os.Chmod(kept, perm)
		}
		os.Remove(kept)
	}
	if err := copyFile(target, kept, perm); err != nil {
		os.Remove(kept)
		return "", err
	}
	return kept, nil
}

// sqliteRestorer is the driver connection's online restore.
type sqliteRestorer interface {
	NewRestore(srcURI string) (*sqlite.Backup, error)
}

// restoreSQLiteOnline loads the dump into the database file through the
// engine. The dump is opened read-only and as a file that will not change, so
// reading it leaves nothing beside it.
func restoreSQLiteOnline(dumpPath, target string) error {
	db, err := sql.Open("sqlite", sqliteFileURI(target, "_pragma=busy_timeout(15000)"))
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(driverConn any) error {
		restorer, ok := driverConn.(sqliteRestorer)
		if !ok {
			return fmt.Errorf("this SQLite driver has no online restore")
		}
		backup, err := restorer.NewRestore(sqliteFileURI(dumpPath, "mode=ro&immutable=1"))
		if err != nil {
			return err
		}
		// Somebody else may hold the database for a moment. That is a reason
		// to wait, not to fail.
		deadline := time.Now().Add(30 * time.Second)
		for {
			more, err := backup.Step(-1)
			if err != nil {
				busy := strings.Contains(err.Error(), "locked") || strings.Contains(err.Error(), "busy")
				if busy && time.Now().Before(deadline) {
					time.Sleep(200 * time.Millisecond)
					continue
				}
				backup.Finish()
				return err
			}
			if !more {
				return backup.Finish()
			}
		}
	})
}

// replaceSQLiteFile puts the dump where the database was, written beside it
// and renamed into place so an interrupted restore leaves the old file rather
// than half of the new one.
func replaceSQLiteFile(dumpPath, target string, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(target), ".jd-restore-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	tmp.Close()
	os.Remove(tmpName)
	defer os.Remove(tmpName)
	if err := copyFile(dumpPath, tmpName, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := os.Remove(target + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("the database was replaced, but its old %s file could not be removed: %w", suffix, err)
		}
	}
	return nil
}

func copyFile(from, to string, perm os.FileMode) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

// mongoURIForDatabase points a Mongo connection string at another database, or
// at none.
//
// The database in the string is also where the account is looked up when the
// string does not say otherwise, so moving it would move the sign-in with it.
// The original is therefore kept as the authentication source, explicitly.
func mongoURIForDatabase(dsn, database string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	original := strings.TrimPrefix(u.Path, "/")
	if original == database {
		return dsn
	}
	q := u.Query()
	if original != "" && u.User != nil && q.Get("authSource") == "" {
		q.Set("authSource", original)
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + database
	return u.String()
}

// mongoConfigFile writes the connection string to a mode-0600 temporary file
// for --config.
//
// --uri put "mongodb://user:password@host/db" straight into argv, which any
// local user reads out of `ps auxww` or /proc/<pid>/cmdline — and this
// container shares the host's PID namespace, so "local" means anyone on the
// box. Postgres and MySQL already avoided argv; Mongo was the odd one out.
func mongoConfigFile(dsn string) (string, func(), error) {
	f, err := os.CreateTemp("", "vpsd-mongo-*.yaml")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { os.Remove(f.Name()) }
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, err
	}
	// A YAML single-quoted scalar escapes exactly one character — the quote
	// itself, by doubling it — so there is no ambiguity to get wrong here.
	content := "uri: '" + strings.ReplaceAll(dsn, "'", "''") + "'\n"
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return f.Name(), cleanup, nil
}

// mysqlDefaultsFile writes credentials to a mode-0600 temporary file. Passing
// --password on the command line would expose it in the process table, and
// MYSQL_PWD is readable through /proc/<pid>/environ.
func mysqlDefaultsFile(info *ConnInfo) (string, func(), error) {
	f, err := os.CreateTemp("", "vpsd-my-*.cnf")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { os.Remove(f.Name()) }
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, err
	}
	port, _ := strconv.Atoi(info.Port)
	if port == 0 {
		port = 3306
	}
	if strings.ContainsAny(info.Password, "\n\r") {
		f.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("password contains a line break, which a MySQL option file cannot carry")
	}
	// Order matters: the backslash has to be doubled before the quote is
	// escaped, or the escape this adds is itself escaped away. Leaving the
	// backslash alone entirely — as this did — sent MySQL "pa\tss" as a
	// password containing a tab, and the dump failed on authentication with
	// nothing to suggest why.
	password := strings.ReplaceAll(info.Password, `\`, `\\`)
	password = strings.ReplaceAll(password, `"`, `\"`)
	content := fmt.Sprintf("[client]\nhost=%s\nport=%d\nuser=%s\npassword=\"%s\"\n",
		info.Host, port, info.User, password)
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return f.Name(), cleanup, nil
}

// Picking the right pg_dump for the server being dumped.
//
// PostgreSQL refuses point blank: "aborting because of server version
// mismatch". A pg_dump older than the server it is pointed at will not run, so
// the client that happened to be in the image decided which servers could be
// backed up at all — a Debian bookworm image ships 15, and a dashboard next to
// a Postgres 16 could not dump it.
//
// Distributions install each major version under its own directory, so the fix
// is to look for the one that matches. A newer client can read an older server,
// so the fallback is the highest installed rather than whatever is on PATH.
// Nothing is guessed about what exists: the directory is read.

var pgBinDirs = []string{"/usr/lib/postgresql", "/usr/pgsql", "/opt/homebrew/opt"}

// postgresTool returns the path to a Postgres client binary suitable for a
// server of the given major version, and the plain name if nothing better is
// found — which keeps a machine that installs the tools somewhere unusual
// working exactly as it did before.
func postgresTool(name string, serverMajor int) string {
	installed := installedPGVersions(name)
	if len(installed) == 0 {
		return name
	}
	// Exactly the server's version is always safe.
	if serverMajor > 0 {
		if path, ok := installed[serverMajor]; ok {
			return path
		}
	}
	// Otherwise the newest, which can read anything older than itself.
	best := 0
	for major := range installed {
		if major > best {
			best = major
		}
	}
	if serverMajor > 0 && best < serverMajor {
		// Everything installed is older than the server. Returning the newest
		// still produces the clearest possible failure — Postgres names both
		// versions — and there is nothing better to try.
		return installed[best]
	}
	return installed[best]
}

// installedPGVersions maps a major version to the path of `name` for it.
func installedPGVersions(name string) map[int]string {
	out := map[int]string{}
	for _, root := range pgBinDirs {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			major, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "postgresql@"))
			if err != nil {
				continue
			}
			path := filepath.Join(root, e.Name(), "bin", name)
			if _, err := os.Stat(path); err == nil {
				out[major] = path
			}
		}
	}
	return out
}

// postgresServerMajor asks the server what it is. A failure here is not fatal:
// the caller falls back to the newest client installed, which is the right
// answer far more often than not.
func postgresServerMajor(ctx context.Context, dsn string) int {
	d, err := DialectFor(DriverPostgres)
	if err != nil {
		return 0
	}
	db, err := sql.Open(d.SQLDriverName(), d.NormaliseDSN(dsn))
	if err != nil {
		return 0
	}
	defer db.Close()
	var num int
	// server_version_num is 160015 for 16.15, which is the major without any
	// of the parsing that the display string would need.
	if err := db.QueryRowContext(ctx, "SHOW server_version_num").Scan(&num); err != nil {
		return 0
	}
	return num / 10000
}
