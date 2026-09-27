package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/logsx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// A database's own log, and the statements it recorded, for its own page.
//
// The Logs tab used to be two answers: a container's raw output, or a list of
// systemd units whose names looked like the engine's, each a link to the host
// Logs page. The second was wrong twice over — a Debian Postgres writes
// nothing to its unit's journal, so the link opened an empty page, and the
// names were matched against every unit on the host whichever server the
// connection was. This resolves the connection to its own server and follows
// it to where that server actually writes: a container is its container; a
// server on the machine is the process listening on the connection's port,
// the files that process holds open for appending, the files its package
// writes by convention, and its unit's journal. Nothing is run on the host to
// find them — the process table and /proc already say.
//
// Both routes are read surface. The log is the server's own output and the
// query log is statements with their literals, which /activity and
// /statements already hand any role; the files are opened through the same
// log roots every /logs route checks.

// dbLogSource is one log the page offers, in the shape /logs/sources lists.
type dbLogSource struct {
	logsx.Source
	// Primary is the log the server writes its own lines to: the one the
	// page opens on and the query log reads slow statements from.
	Primary bool `json:"primary,omitempty"`
}

// dbLogRefusal is a log the server writes that the log roots do not cover,
// named so the operator knows what to add rather than seeing nothing.
type dbLogRefusal struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type dbLogSources struct {
	Sources []dbLogSource  `json:"sources"`
	Refused []dbLogRefusal `json:"refused,omitempty"`
	// Reason says why there is no source, in a sentence the page prints.
	Reason string `json:"reason,omitempty"`
}

// dbHostProbe is what following a server on the machine to its log reads off
// the machine: who listens on a port, which unit a process belongs to, and
// the files it has open. A test hands in a machine of its own.
type dbHostProbe struct {
	listeners func(context.Context) ([]proxysvc.Listener, error)
	managerOf func(pid int32, cmdline string) (manager, name string)
	proc      string
	logDir    string
}

var hostLogProbe = dbHostProbe{
	listeners: proxysvc.ListListeners,
	managerOf: procs.ManagerOf,
	proc:      "/proc",
	logDir:    "/var/log",
}

// driverLens is the lens an engine's log is read through. The page knows the
// engine for certain, where detection from an image or a path only guesses —
// a Postgres built into a custom image is still Postgres.
func driverLens(driver dbx.Driver) string {
	switch driver {
	case dbx.DriverPostgres:
		return "postgres"
	case dbx.DriverMySQL:
		return "mysql"
	case dbx.DriverRedis:
		return "redis"
	case dbx.DriverMongo:
		return "mongodb"
	case dbx.DriverClickHouse:
		return "clickhouse"
	case dbx.DriverMSSQL:
		return "mssql"
	}
	return ""
}

func (s *Server) handleDBLogSources(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	httpx.JSON(w, http.StatusOK, s.dbLogSourcesFor(ctx, conn, dsn, hostLogProbe))
	return nil
}

func (s *Server) dbLogSourcesFor(ctx context.Context, conn *dbConnection, dsn string, probe dbHostProbe) dbLogSources {
	out := dbLogSources{Sources: []dbLogSource{}}
	if conn.Driver == dbx.DriverSQLite {
		out.Reason = "A SQLite database is a file, not a server: it writes no log of its own."
		return out
	}
	info, err := dbx.ParseDSN(conn.Driver, dsn)
	if err != nil {
		out.Reason = "The saved connection could not be read, so where its server runs is not known."
		return out
	}
	lens := driverLens(conn.Driver)
	if !databaseLoopback(info.Host) && !isContainerAddress(info.Host) {
		out.Reason = fmt.Sprintf("The server is on another machine (%s), and its log is there.", info.Host)
		return out
	}
	if server := s.serverBehind(ctx, conn, info); server != nil {
		out.Sources = append(out.Sources, s.dbContainerLog(ctx, *server.container, lens))
		return out
	}
	if !databaseLoopback(info.Host) {
		out.Reason = fmt.Sprintf("No container on this machine answers at %s: the server is elsewhere on the network, and its log is there.", info.Host)
		return out
	}
	port, _ := strconv.Atoi(info.Port)
	return s.dbHostLogs(ctx, conn.Driver, port, lens, probe)
}

// dbContainerLog is a container's output, asked for by name: a recreated
// container keeps its name and gets a new id, and a page remembering the id
// would find its log gone.
func (s *Server) dbContainerLog(ctx context.Context, c dockerx.Container, lens string) dbLogSource {
	src := s.containerSource(ctx, c)
	src.ID = "docker:" + c.Name
	if lens != "" {
		src.Lens = lens
	}
	return dbLogSource{Source: src, Primary: true}
}

// dbHostLogs follows a port on this machine to the logs of the process
// listening on it.
func (s *Server) dbHostLogs(ctx context.Context, driver dbx.Driver, port int, lens string, probe dbHostProbe) dbLogSources {
	out := dbLogSources{Sources: []dbLogSource{}}
	if port <= 0 {
		out.Reason = "The saved connection names no port, so the server listening for it cannot be found."
		return out
	}
	listeners, err := probe.listeners(ctx)
	if err != nil {
		out.Reason = "This machine's listening sockets could not be read: " + err.Error()
		return out
	}
	l := listenerOn(listeners, port)
	if l == nil {
		out.Reason = fmt.Sprintf("Nothing on this machine is listening on port %d, so there is no process to follow to its log. The server may be stopped.", port)
		return out
	}
	// A published container answers on the host through docker-proxy, whose
	// own unit is docker.service: following it would read Docker's journal
	// as the database's. The container publishing the port is the server.
	if l.Process == "docker-proxy" {
		if c := s.containerPublishing(ctx, port); c != nil {
			out.Sources = append(out.Sources, s.dbContainerLog(ctx, *c, lens))
			return out
		}
		out.Reason = fmt.Sprintf("Port %d is published by Docker, but no running container on this machine publishes it.", port)
		return out
	}
	if l.PID <= 0 {
		out.Reason = fmt.Sprintf("The process listening on port %d could not be identified, so its log cannot be found.", port)
		return out
	}
	manager, unit := probe.managerOf(l.PID, l.Cmdline)
	if manager == "container" {
		// A container on the host's own network: it looks like a native
		// server from the socket table, and its cgroup says otherwise.
		if d, err := s.modules.docker.Inspect(ctx, unit); err == nil {
			out.Sources = append(out.Sources, s.dbContainerLog(ctx, d.Container, lens))
			return out
		}
		out.Sources = append(out.Sources, dbLogSource{Source: logsx.Source{
			ID: "docker:" + unit, Label: unit, Kind: logsx.KindDocker, Lens: lens,
		}, Primary: true})
		return out
	}
	if manager != "systemd" {
		unit = ""
	}

	seen := map[string]bool{}
	addFile := func(path, detail string) {
		path = filepath.Clean(path)
		if seen[path] {
			return
		}
		seen[path] = true
		if err := s.modules.logs.Allow(path); err != nil {
			out.Refused = append(out.Refused, dbLogRefusal{
				Path:   path,
				Reason: "outside the log roots (" + strings.Join(s.modules.logs.Roots(), ", ") + ")",
			})
			return
		}
		src, ok, err := s.modules.logs.Describe(path)
		if err != nil || !ok {
			return
		}
		src.ID = "file:" + path
		src.Label = filepath.Base(path)
		if lens != "" {
			src.Lens = lens
		}
		src.Detail = detail
		if src.Size == 0 && src.Archives > 0 {
			// The operator's own case: logrotate emptied it overnight, and
			// an empty live file reads as a server that logs nothing.
			src.Detail = "Empty since it was last rotated — History reads the rotated files too"
		}
		out.Sources = append(out.Sources, dbLogSource{Source: src})
	}
	for _, path := range appendedLogFiles(probe.proc, l.PID) {
		addFile(path, "The file "+l.Process+" writes its log to")
	}
	for _, path := range conventionLogPaths(probe.logDir, driver, unit) {
		if _, err := os.Stat(path); err == nil {
			addFile(path, "Where this engine's package writes its log")
		}
	}
	if unit != "" && s.modules.systemd.Available() {
		detail := "What systemd recorded for this unit, and what it printed"
		if len(out.Sources) > 0 {
			// A server that writes its own file sends the journal nothing
			// but systemd's starts and stops — Debian's Postgres writes no
			// line there at all — and an empty journal should not read as
			// a quiet server.
			detail = "What systemd recorded starting and stopping it; the server's own lines are in " + out.Sources[0].Label
		}
		out.Sources = append(out.Sources, dbLogSource{Source: logsx.Source{
			ID: "journal:" + unit, Label: unit, Kind: logsx.KindJournal, Lens: lens, Detail: detail,
		}})
	}
	if len(out.Sources) > 0 {
		// The file even when it is empty: it is where the server writes, and
		// its rotated generations hold what was written before.
		out.Sources[0].Primary = true
		return out
	}
	if len(out.Refused) > 0 {
		out.Reason = fmt.Sprintf("%s writes its log to %s, which is outside the log roots. An administrator can add its directory to JD_LOG_ROOTS.",
			l.Process, out.Refused[0].Path)
		return out
	}
	out.Reason = fmt.Sprintf("Port %d is served by %s (pid %d), which has no log file open under the log roots and runs under no systemd unit.",
		port, l.Process, l.PID)
	return out
}

// listenerOn is the TCP listener on a port, preferring one whose process is
// known: the socket table lists a port once per address family, and only one
// of the two may have been joined to its owner.
func listenerOn(listeners []proxysvc.Listener, port int) *proxysvc.Listener {
	var found *proxysvc.Listener
	for i := range listeners {
		l := &listeners[i]
		if l.Protocol != "tcp" || int(l.Port) != port {
			continue
		}
		if found == nil || (found.PID <= 0 && l.PID > 0) {
			found = l
		}
	}
	return found
}

// containerPublishing is the running container that publishes a host port,
// whatever its image: a tag that moved leaves the list's image a bare id,
// which is why detection by image missed it before this was asked.
func (s *Server) containerPublishing(ctx context.Context, port int) *dockerx.Container {
	containers, err := s.modules.docker.ListContainers(ctx, false)
	if err != nil {
		return nil
	}
	for i := range containers {
		for _, p := range containers[i].Ports {
			if int(p.PublicPort) == port {
				return &containers[i]
			}
		}
	}
	return nil
}

// The open flags /proc/<pid>/fdinfo prints, in octal: Linux's values, which is
// the only kernel that has the file.
const (
	openWriteOnly = 0o1
	openReadWrite = 0o2
	openAppend    = 0o2000
)

// maxLogChildren bounds how many of a server's children are looked at. A
// Postgres with logging_collector writes through one child; a busy one has
// hundreds of backends holding the same stderr.
const maxLogChildren = 64

// appendedLogFiles are the files a process and its children have open for
// appending that look like logs, in descriptor order — stdout and stderr
// first, which is where a server started by its package writes. A log is
// opened for appending, so a data file opened for writing is not one; the
// name is checked too, because a binary log is appended to as well.
func appendedLogFiles(proc string, pid int32) []string {
	pids := append([]int32{pid}, childPIDs(proc, pid)...)
	seen := map[string]bool{}
	out := []string{}
	for _, p := range pids {
		dir := filepath.Join(proc, strconv.Itoa(int(p)))
		entries, err := os.ReadDir(filepath.Join(dir, "fd"))
		if err != nil {
			continue
		}
		fds := make([]int, 0, len(entries))
		for _, e := range entries {
			if n, err := strconv.Atoi(e.Name()); err == nil {
				fds = append(fds, n)
			}
		}
		sort.Ints(fds)
		for _, fd := range fds {
			name := strconv.Itoa(fd)
			target, err := os.Readlink(filepath.Join(dir, "fd", name))
			if err != nil || !filepath.IsAbs(target) || strings.HasSuffix(target, " (deleted)") || seen[target] {
				continue
			}
			if !appendedForWriting(filepath.Join(dir, "fdinfo", name)) || !logLike(target) {
				continue
			}
			seen[target] = true
			out = append(out, target)
		}
	}
	return out
}

func childPIDs(proc string, pid int32) []int32 {
	id := strconv.Itoa(int(pid))
	b, err := os.ReadFile(filepath.Join(proc, id, "task", id, "children"))
	if err != nil {
		return nil
	}
	out := []int32{}
	for _, field := range strings.Fields(string(b)) {
		if n, err := strconv.ParseInt(field, 10, 32); err == nil && n > 0 {
			out = append(out, int32(n))
		}
		if len(out) == maxLogChildren {
			break
		}
	}
	return out
}

func appendedForWriting(fdinfo string) bool {
	b, err := os.ReadFile(fdinfo)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "flags:"); ok {
			flags, err := strconv.ParseUint(strings.TrimSpace(v), 8, 64)
			return err == nil && flags&openAppend != 0 && flags&(openWriteOnly|openReadWrite) != 0
		}
	}
	return false
}

// logLike is a path named like a log or kept in a directory of them.
func logLike(path string) bool {
	for _, system := range []string{"/dev/", "/proc/", "/sys/", "/run/"} {
		if strings.HasPrefix(path, system) {
			return false
		}
	}
	base := filepath.Base(path)
	for _, ext := range []string{".log", ".err", ".out"} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	for _, part := range strings.Split(filepath.Dir(path), string(filepath.Separator)) {
		if part == "log" || part == "logs" {
			return true
		}
	}
	return false
}

// conventionLogPaths are where each engine's distribution package writes its
// log, for the servers that open it only to write a line: Redis opens its
// logfile per line and holds nothing between. Postgres's is named after the
// cluster, which only its Debian unit's instance name says.
func conventionLogPaths(dir string, driver dbx.Driver, unit string) []string {
	at := func(parts ...string) string { return filepath.Join(append([]string{dir}, parts...)...) }
	switch driver {
	case dbx.DriverPostgres:
		name := strings.TrimSuffix(unit, ".service")
		if cluster, ok := strings.CutPrefix(name, "postgresql@"); ok && cluster != "" && !strings.Contains(cluster, "..") {
			return []string{at("postgresql", "postgresql-"+cluster+".log")}
		}
	case dbx.DriverMySQL:
		return []string{at("mysql", "error.log"), at("mysql", "mysqld.log"), at("mariadb", "mariadb.log"), at("mysqld.log")}
	case dbx.DriverRedis:
		return []string{at("redis", "redis-server.log"), at("redis", "redis.log"),
			at("valkey", "valkey-server.log"), at("valkey", "valkey.log")}
	case dbx.DriverMongo:
		return []string{at("mongodb", "mongod.log")}
	case dbx.DriverClickHouse:
		return []string{at("clickhouse-server", "clickhouse-server.log"), at("clickhouse-server", "clickhouse-server.err.log")}
	}
	return nil
}

// --- the statements it recorded ----------------------------------------------

// maxQueryLog is the most entries one read returns.
const maxQueryLog = 500

func (s *Server) handleDBQueryLog(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	window, err := queryLogWindow(r.URL.Query(), time.Now())
	if err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	httpx.JSON(w, http.StatusOK, s.dbQueryLog(ctx, conn, dsn, window, hostLogProbe))
	return nil
}

// queryLogWindow reads the question. With no since it is the last day: a
// Postgres log is searched through its rotated files, and an open window
// would read all of them to answer "what was slow".
func queryLogWindow(q url.Values, now time.Time) (dbx.QueryLogWindow, error) {
	w := dbx.QueryLogWindow{Since: now.Add(-24 * time.Hour), Limit: 200}
	for _, bound := range []struct {
		name string
		to   *time.Time
	}{{"since", &w.Since}, {"until", &w.Until}} {
		v := q.Get(bound.name)
		if v == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return w, httpx.BadRequest("%s must be an RFC3339 time", bound.name)
		}
		*bound.to = t
	}
	if !w.Until.IsZero() && !w.Since.Before(w.Until) {
		return w, httpx.BadRequest("since must be before until")
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return w, httpx.BadRequest("limit must be a positive number")
		}
		w.Limit = min(n, maxQueryLog)
	}
	if v := q.Get("minMs"); v != "" {
		ms, err := strconv.ParseFloat(v, 64)
		if err != nil || ms < 0 || math.IsNaN(ms) || math.IsInf(ms, 0) {
			return w, httpx.BadRequest("minMs must be a number of milliseconds")
		}
		w.MinMs = ms
	}
	return w, nil
}

func (s *Server) dbQueryLog(ctx context.Context, conn *dbConnection, dsn string, w dbx.QueryLogWindow, probe dbHostProbe) *dbx.QueryLog {
	switch conn.Driver {
	case dbx.DriverPostgres:
		return s.postgresQueryLog(ctx, conn, dsn, w, probe)
	case dbx.DriverMongo:
		return s.mongoQueryLog(ctx, conn, dsn, w, probe)
	case dbx.DriverMySQL, dbx.DriverClickHouse:
		source := dbx.QuerySourceSlowLog
		if conn.Driver == dbx.DriverClickHouse {
			source = dbx.QuerySourceQueryLog
		}
		pool, _, err := s.dbPool(ctx, conn.ID)
		if err != nil {
			return unreadQueryLog(source, err)
		}
		read := dbx.MySQLQueryLog
		if conn.Driver == dbx.DriverClickHouse {
			read = dbx.ClickHouseQueryLog
		}
		out, err := read(ctx, pool, w)
		if err != nil {
			return unreadQueryLog(source, err)
		}
		return out
	case dbx.DriverRedis:
		client, err := dbx.RedisClient(ctx, dsn, 0)
		if err != nil {
			return unreadQueryLog(dbx.QuerySourceSlowlog, err)
		}
		defer client.Close()
		out, err := dbx.RedisQueryLog(ctx, client, w)
		if err != nil {
			return unreadQueryLog(dbx.QuerySourceSlowlog, err)
		}
		return out
	}
	return dbx.QueryLogUnsupported(conn.Driver)
}

// unreadQueryLog is a log the engine keeps and this read could not reach.
func unreadQueryLog(source string, err error) *dbx.QueryLog {
	return &dbx.QueryLog{Supported: true, Source: source, Entries: []dbx.QueryEntry{},
		Reason: "The server could not be read: " + err.Error()}
}

// postgresQueryLog is the server log's slow statements. Postgres writes one
// only past log_min_duration_statement, which is off by default, so the
// answer carries the setting and the statement that turns it on.
func (s *Server) postgresQueryLog(ctx context.Context, conn *dbConnection, dsn string, w dbx.QueryLogWindow, probe dbHostProbe) *dbx.QueryLog {
	out := &dbx.QueryLog{Supported: true, Source: dbx.QuerySourceLog, Entries: []dbx.QueryEntry{}}
	sources := s.dbLogSourcesFor(ctx, conn, dsn, probe)
	primary := primaryLog(sources)
	if primary == nil {
		// The log sources' own reason says why — another machine, a file
		// outside the roots — and the page prints it beside this one.
		out.Supported = false
		out.Reason = "Postgres writes its slow statements to its server log, which this machine cannot read."
		return out
	}
	if pool, _, err := s.dbPool(ctx, conn.ID); err == nil {
		if current, enable, err := dbx.PostgresSlowSetting(ctx, pool); err == nil {
			out.Enable = enable
			if enable == nil {
				out.Threshold = current
			}
		}
	}
	entries, truncated, err := s.slowFromLog(ctx, *primary, "postgres", w)
	if err != nil {
		out.Reason = "The server log could not be searched: " + err.Error()
		return out
	}
	out.Entries, out.Truncated = entries, truncated
	return out
}

// mongoQueryLog is the log's slow operations: the file or the container when
// this machine has one, else the lines the server keeps in memory, which is
// the one log a server on another machine hands over the connection.
func (s *Server) mongoQueryLog(ctx context.Context, conn *dbConnection, dsn string, w dbx.QueryLogWindow, probe dbHostProbe) *dbx.QueryLog {
	out := &dbx.QueryLog{Supported: true, Source: dbx.QuerySourceLog, Entries: []dbx.QueryEntry{}}
	if primary := primaryLog(s.dbLogSourcesFor(ctx, conn, dsn, probe)); primary != nil {
		entries, truncated, err := s.slowFromLog(ctx, *primary, "mongodb", w)
		if err != nil {
			out.Reason = "The server log could not be searched: " + err.Error()
			return out
		}
		out.Entries, out.Truncated = entries, truncated
		return out
	}
	client, err := dbx.MongoClient(ctx, dsn)
	if err != nil {
		return unreadQueryLog(dbx.QuerySourceLog, err)
	}
	defer client.Disconnect(context.Background())
	lines, err := dbx.MongoGlobalLog(ctx, client)
	if err != nil {
		out.Reason = "The server's log could not be read over the connection (getLog needs the clusterMonitor role): " + err.Error()
		return out
	}
	entries := mongoSlowLines(lines)
	kept := entries[:0]
	for _, e := range entries {
		if w.Holds(e) {
			kept = append(kept, e)
		}
	}
	out.Truncated = len(kept) > w.Limit
	if out.Truncated {
		kept = kept[:w.Limit]
	}
	out.Entries = kept
	return out
}

func primaryLog(sources dbLogSources) *dbLogSource {
	for i := range sources.Sources {
		if sources.Sources[i].Primary {
			return &sources.Sources[i]
		}
	}
	return nil
}

// slowFromLog searches a server log for its slow statements through the
// engine's lens, the rotated files included — the answer to "what was slow
// last night" is usually in yesterday's file.
func (s *Server) slowFromLog(ctx context.Context, src dbLogSource, lens string, w dbx.QueryLogWindow) ([]dbx.QueryEntry, bool, error) {
	fields := []string{"event:slow"}
	if w.MinMs > 0 {
		fields = append(fields, "duration_ms:>="+strconv.FormatFloat(w.MinMs, 'f', -1, 64))
	}
	opts := logsx.SearchOptions{
		Filter: logsx.Filter{Lens: lens, Fields: fields},
		Since:  w.Since, Until: w.Until, Limit: w.Limit, Archives: true,
	}
	if err := opts.Validate(); err != nil {
		return nil, false, err
	}
	var (
		res *logsx.SearchResult
		err error
	)
	switch src.Kind {
	case logsx.KindDocker:
		res, err = s.searchContainer(ctx, strings.TrimPrefix(src.ID, "docker:"), opts)
	case logsx.KindJournal:
		var target logTarget
		if target, err = parseLogTarget(src.ID); err == nil {
			res, err = s.searchJournal(ctx, target, opts, false, true)
		}
	default:
		res, err = s.modules.logs.Search(ctx, src.Path, opts)
	}
	if err != nil {
		return nil, false, err
	}
	return slowEntries(res.Lines), res.Truncated, nil
}

// slowEntries turns the lens's slow lines into rows, newest first. A
// statement that spans lines goes on in the tab-indented lines under its
// head, which the lens marks as the head's continuation; they are joined back
// so the row holds the whole statement.
func slowEntries(lines []logsx.Line) []dbx.QueryEntry {
	out := []dbx.QueryEntry{}
	for i, l := range lines {
		if l.Cont || l.Context || l.Event != "slow" {
			continue
		}
		e := dbx.QueryEntry{
			Query: l.Attrs["query"], FP: l.Attrs["fp"], User: l.Attrs["user"], DB: l.Attrs["db"],
			Client: l.Attrs["client"], Code: l.Attrs["code"],
		}
		if l.Timestamp != nil {
			e.At = l.Timestamp.UTC()
		}
		e.DurationMs, _ = strconv.ParseFloat(l.Attrs["duration_ms"], 64)
		if ns := l.Attrs["ns"]; ns != "" && e.DB == "" {
			e.DB, _, _ = strings.Cut(ns, ".")
		}
		e.Rows = attrCount(l.Attrs, "rows", "rows_sent")
		e.Examined = attrCount(l.Attrs, "docs_examined", "rows_examined")
		for _, next := range lines[i+1:] {
			if !next.Cont {
				break
			}
			if rest, ok := strings.CutPrefix(next.Text, "\t"); ok && e.Query != "" {
				e.Query += "\n" + rest
			}
		}
		if e.Query == "" {
			e.Query = l.Text
		}
		out = append(out, e)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func attrCount(attrs map[string]string, keys ...string) *int64 {
	for _, key := range keys {
		if n, err := strconv.ParseFloat(attrs[key], 64); err == nil {
			v := int64(n)
			return &v
		}
	}
	return nil
}

// mongoSlowLines reads getLog's lines through the same lens the log file is
// read through, so a slow operation has one shape wherever it was found.
func mongoSlowLines(lines []string) []dbx.QueryEntry {
	lens, err := logsx.LensByID("mongodb")
	if err != nil || lens == nil {
		return []dbx.QueryEntry{}
	}
	reader := lens.New()
	slow := []logsx.Line{}
	for _, text := range lines {
		l := logsx.ParseLine(text, "")
		reader.Read(&l)
		if l.Event == "slow" {
			slow = append(slow, l)
		}
	}
	return slowEntries(slow)
}
