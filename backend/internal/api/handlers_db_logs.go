package api

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
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
	// Note says what the sources are when nothing answers for the
	// connection: the log of a server that has stopped, found without a
	// process to follow, rather than one that has gone quiet.
	Note string `json:"note,omitempty"`
}

// dbHostProbe is what following a server on the machine to its log reads off
// the machine: who listens on a port, which unit a process belongs to, the
// files it has open, and the units systemd has. A test hands in a machine of
// its own.
type dbHostProbe struct {
	listeners func(context.Context) ([]proxysvc.Listener, error)
	managerOf func(pid int32, cmdline string) (manager, name string)
	// systemd reports whether there is a journal to read and units to ask
	// for; units lists them, stopped and failed ones included.
	systemd func() bool
	units   func(context.Context) ([]procs.Unit, error)
	proc    string
	logDir  string
}

// journal reports whether the machine has systemd to ask.
func (p dbHostProbe) journal() bool { return p.systemd != nil && p.systemd() }

var hostSystemd = procs.NewSystemd()

var hostLogProbe = dbHostProbe{
	listeners: proxysvc.ListListeners,
	managerOf: func(pid int32, cmdline string) (string, string) {
		return procs.ManagerOf("/proc", pid, cmdline)
	},
	systemd: hostSystemd.Available,
	units:   hostSystemd.List,
	proc:    "/proc",
	logDir:  "/var/log",
}

// dbLogSourcesFresh is how long one resolution answers for a connection. The
// page asks for the sources each minute and its Queries view for the log
// behind them twice as often, and each asking walks every process's sockets,
// the server's descriptors and Docker's list; a server does not move its log
// between two of them.
const dbLogSourcesFresh = 45 * time.Second

type dbLogSourcesKept struct {
	dsn     [sha256.Size]byte
	at      time.Time
	sources dbLogSources
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

// dbLogSourcesFor is the connection's logs, as last resolved while that is
// fresh. The kept answer is shared: callers read it and never change it.
func (s *Server) dbLogSourcesFor(ctx context.Context, conn *dbConnection, dsn string, probe dbHostProbe) dbLogSources {
	sum := sha256.Sum256([]byte(dsn))
	if v, ok := s.dbLogSourcesKept.Load(conn.ID); ok {
		if kept := v.(dbLogSourcesKept); kept.dsn == sum && time.Since(kept.at) < dbLogSourcesFresh {
			return kept.sources
		}
	}
	out := s.resolveDBLogSources(ctx, conn, dsn, probe)
	// An answer cut short by the request's deadline is not kept: the next
	// asking may have the time this one did not.
	if ctx.Err() == nil {
		s.dbLogSourcesKept.Store(conn.ID, dbLogSourcesKept{dsn: sum, at: time.Now(), sources: out})
	}
	return out
}

func (s *Server) resolveDBLogSources(ctx context.Context, conn *dbConnection, dsn string, probe dbHostProbe) dbLogSources {
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
	if databaseLoopback(info.Host) {
		port, _ := strconv.Atoi(info.Port)
		return s.dbHostLogs(ctx, conn, port, lens, probe)
	}
	if c := s.containerAt(ctx, info.Host); c != nil {
		out.Sources = append(out.Sources, s.dbContainerLog(ctx, *c, lens))
		return out
	}
	if c := s.containerNamed(ctx, conn); c != nil {
		return s.dbNamedContainerLog(ctx, *c, lens)
	}
	out.Reason = fmt.Sprintf("No container on this machine answers at %s: the server is elsewhere on the network, and its log is there.", info.Host)
	return out
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
func (s *Server) dbHostLogs(ctx context.Context, conn *dbConnection, port int, lens string, probe dbHostProbe) dbLogSources {
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
		return s.dbLogsWithoutListener(ctx, conn, port, lens, listeners, probe)
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

	set := s.newDBLogSet(lens)
	for _, path := range appendedLogFiles(probe.proc, l.PID) {
		set.addFile(path, "The file "+l.Process+" writes its log to")
	}
	set.addConventional(probe.logDir, conn.Driver, unit)
	if unit != "" && probe.journal() {
		set.addJournal(unit, "What systemd recorded for this unit, and what it printed")
	}
	out = set.result()
	switch {
	case len(out.Sources) > 0:
	case len(out.Refused) > 0:
		out.Reason = refusedReason(l.Process, out.Refused[0])
	default:
		out.Reason = fmt.Sprintf("Port %d is served by %s (pid %d), which has no log file open under the log roots and runs under no systemd unit.",
			port, l.Process, l.PID)
	}
	return out
}

func refusedReason(who string, r dbLogRefusal) string {
	return fmt.Sprintf("%s writes its log to %s, which is %s. An administrator can add its directory to JD_LOG_ROOTS.", who, r.Path, r.Reason)
}

// dbLogsWithoutListener finds the log of a server nothing answers for:
// stopped, crashed, or still starting — which is when its log is read most,
// for the lines that say which. With no process to follow, it is found the
// ways a stopped server can be: a container publishing the port without
// docker-proxy, the container the connection is named after (the sync names
// an adopted container's connection after it, and a stopped one publishes
// nothing to be found by), or the engine's units by name with the files
// their packages write.
func (s *Server) dbLogsWithoutListener(ctx context.Context, conn *dbConnection, port int, lens string, listeners []proxysvc.Listener, probe dbHostProbe) dbLogSources {
	// With the userland proxy off, Docker publishes a port in the kernel's
	// tables alone, and nothing is listening on it to be found.
	if c := s.containerPublishing(ctx, port); c != nil {
		return dbLogSources{Sources: []dbLogSource{s.dbContainerLog(ctx, *c, lens)}}
	}
	if c := s.containerNamed(ctx, conn); c != nil {
		return s.dbNamedContainerLog(ctx, *c, lens)
	}
	units := engineUnits(ctx, conn.Driver, port, listeners, probe)
	set := s.newDBLogSet(lens)
	names := make([]string, 0, len(units))
	for _, u := range units {
		set.addConventional(probe.logDir, conn.Driver, u.Name)
		names = append(names, u.Name+" ("+u.ActiveState+")")
	}
	if len(units) == 0 {
		set.addConventional(probe.logDir, conn.Driver, "")
	}
	for _, u := range units {
		set.addJournal(u.Name, fmt.Sprintf("What systemd recorded for this unit, which is %s now", u.ActiveState))
	}
	out := set.result()
	switch {
	case len(out.Sources) == 0 && len(out.Refused) > 0:
		out.Reason = refusedReason("The server", out.Refused[0])
	case len(out.Sources) > 0 && len(names) > 0:
		out.Note = fmt.Sprintf("Nothing on this machine is listening on port %d, so the server may be stopped or starting. These are the logs of %s, found by name.",
			port, strings.Join(names, ", "))
	case len(out.Sources) > 0:
		out.Note = fmt.Sprintf("Nothing on this machine is listening on port %d, so the server may be stopped or starting. This is where its package writes its log.", port)
	default:
		out.Reason = fmt.Sprintf("Nothing on this machine is listening on port %d, and no unit of this engine's was found by name, so there is no process or unit to follow to its log. The server may be stopped.", port)
	}
	return out
}

// dbLogSet gathers a server's logs on the machine: each file once and only
// through the log roots, a file the roots refuse named rather than dropped,
// and the unit's journal after the files.
type dbLogSet struct {
	s    *Server
	lens string
	seen map[string]bool
	out  dbLogSources
	// journals are added last, told whose lines are where once the files
	// are known.
	journals []dbLogSource
}

func (s *Server) newDBLogSet(lens string) *dbLogSet {
	return &dbLogSet{s: s, lens: lens, seen: map[string]bool{}, out: dbLogSources{Sources: []dbLogSource{}}}
}

func (set *dbLogSet) addFile(path, detail string) {
	path = filepath.Clean(path)
	if set.seen[path] {
		return
	}
	set.seen[path] = true
	logs := set.s.modules.logs
	if err := logs.Allow(path); err != nil {
		set.out.Refused = append(set.out.Refused, dbLogRefusal{
			Path:   path,
			Reason: "outside the log roots (" + strings.Join(logs.Roots(), ", ") + ")",
		})
		return
	}
	src, ok, err := logs.Describe(path)
	if err != nil || !ok {
		return
	}
	src.ID = "file:" + path
	src.Label = filepath.Base(path)
	if set.lens != "" {
		src.Lens = set.lens
	}
	src.Detail = detail
	if src.Size == 0 && src.Archives > 0 {
		// The operator's own case: logrotate emptied it overnight, and
		// an empty live file reads as a server that logs nothing.
		src.Detail = "Empty since it was last rotated — History reads the rotated files too"
	}
	set.out.Sources = append(set.out.Sources, dbLogSource{Source: src})
}

// addConventional adds the files the engine's package writes that exist.
func (set *dbLogSet) addConventional(dir string, driver dbx.Driver, unit string) {
	for _, path := range conventionLogPaths(dir, driver, unit) {
		if _, err := os.Stat(path); err == nil {
			set.addFile(path, "Where this engine's package writes its log")
		}
	}
}

func (set *dbLogSet) addJournal(unit, detail string) {
	id := "journal:" + unit
	if set.seen[id] {
		return
	}
	set.seen[id] = true
	set.journals = append(set.journals, dbLogSource{Source: logsx.Source{
		ID: id, Label: unit, Kind: logsx.KindJournal, Lens: set.lens, Detail: detail,
	}})
}

// result is the set in reading order, with the log the server writes its own
// lines to marked primary: the first file, even when it is empty, since it is
// where the server writes and its rotated generations hold what was written
// before. A journal is primary only when no file is the server's — a server
// that writes its own file sends the journal nothing but systemd's starts
// and stops (Debian's Postgres writes no line there at all), and one whose
// file the roots refuse has its statements in that file, not in the journal.
func (set *dbLogSet) result() dbLogSources {
	out := set.out
	for _, j := range set.journals {
		switch {
		case len(out.Sources) > 0:
			j.Detail = "What systemd recorded starting and stopping it; the server's own lines are in " + out.Sources[0].Label
		case len(out.Refused) > 0:
			j.Detail = "What systemd recorded starting and stopping it; the server's own lines are in " + out.Refused[0].Path + ", outside the log roots"
		}
		out.Sources = append(out.Sources, j)
	}
	if len(out.Sources) > 0 && (out.Sources[0].Kind != logsx.KindJournal || len(out.Refused) == 0) {
		out.Sources[0].Primary = true
	}
	return out
}

// engineUnits are the machine's units that run the connection's engine and
// are not serving another port: a second cluster listening elsewhere is not
// the one this connection is to. Debian's postgresql.service starts nothing
// itself — each cluster is a postgresql@ instance, and the umbrella's journal
// is empty — so it is left out beside them.
func engineUnits(ctx context.Context, driver dbx.Driver, port int, listeners []proxysvc.Listener, probe dbHostProbe) []procs.Unit {
	prefixes := engineUnitPrefixes(driver)
	if len(prefixes) == 0 || !probe.journal() || probe.units == nil {
		return nil
	}
	all, err := probe.units(ctx)
	if err != nil {
		return nil
	}
	named := []procs.Unit{}
	instances := false
	for _, u := range all {
		if u.LoadState == "loaded" && engineUnit(u.Name, prefixes) {
			named = append(named, u)
			instances = instances || strings.HasPrefix(u.Name, "postgresql@")
		}
	}
	if len(named) == 0 {
		return nil
	}
	busy := map[string]bool{}
	asked := map[int32]bool{}
	for _, l := range listeners {
		if l.PID <= 0 || int(l.Port) == port || asked[l.PID] {
			continue
		}
		asked[l.PID] = true
		if manager, unit := probe.managerOf(l.PID, l.Cmdline); manager == "systemd" {
			busy[unit] = true
		}
	}
	out := []procs.Unit{}
	for _, u := range named {
		if busy[u.Name] || (instances && u.Name == "postgresql.service") {
			continue
		}
		out = append(out, u)
	}
	return out
}

// engineUnitPrefixes are the names each engine's packages give their units.
func engineUnitPrefixes(driver dbx.Driver) []string {
	switch driver {
	case dbx.DriverPostgres:
		return []string{"postgresql"}
	case dbx.DriverMySQL:
		return []string{"mysql", "mariadb"}
	case dbx.DriverRedis:
		return []string{"redis", "valkey", "keydb"}
	case dbx.DriverMongo:
		return []string{"mongod"}
	case dbx.DriverClickHouse:
		return []string{"clickhouse-server"}
	case dbx.DriverMSSQL:
		return []string{"mssql-server"}
	}
	return nil
}

// engineUnit is a unit named for the engine, and not the sentinel or the
// metrics exporter that are often installed beside it under the same name.
func engineUnit(name string, prefixes []string) bool {
	name = strings.TrimSuffix(name, ".service")
	if strings.Contains(name, "sentinel") || strings.Contains(name, "exporter") {
		return false
	}
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// containerAt is the running container that answers at an address, whatever
// its image. serverBehind matches by image first, and a server in an image of
// its own — or one whose moved tag the list names by id — is still the one the
// connection dials: the connection already says which engine it is.
func (s *Server) containerAt(ctx context.Context, host string) *dockerx.Container {
	if s.modules.docker == nil {
		return nil
	}
	ip := strings.Trim(host, "[]")
	containers, err := s.modules.docker.ListContainers(ctx, false)
	if err != nil {
		return nil
	}
	for i := range containers {
		detail, err := s.modules.docker.Inspect(ctx, containers[i].ID)
		if err == nil && slices.Contains(containerIPs(detail), ip) {
			return &containers[i]
		}
	}
	return nil
}

// containerNamed is the container the connection is named after, running or
// not, when its image is the connection's engine.
func (s *Server) containerNamed(ctx context.Context, conn *dbConnection) *dockerx.Container {
	if s.modules.docker == nil || conn.Name == "" {
		return nil
	}
	containers, err := s.modules.docker.ListContainers(ctx, true)
	if err != nil {
		return nil
	}
	for i := range containers {
		c := &containers[i]
		if c.Name != conn.Name {
			continue
		}
		if cand, _ := dbx.Detect(c.Name, s.containerImage(ctx, *c), nil, nil, nil); cand != nil && cand.Driver == conn.Driver {
			return c
		}
	}
	return nil
}

// dbNamedContainerLog is the log of the container found by the connection's
// name, said to be stopped when it is: its output runs up to the moment it
// stopped, and those last lines are why it is being read.
func (s *Server) dbNamedContainerLog(ctx context.Context, c dockerx.Container, lens string) dbLogSources {
	out := dbLogSources{Sources: []dbLogSource{s.dbContainerLog(ctx, c, lens)}}
	if c.State != "running" {
		out.Note = fmt.Sprintf("%s is %s, so nothing answers for this connection. Its output runs up to the moment it stopped.", c.Name, c.State)
	}
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
		out.Supported = false
		if len(sources.Refused) > 0 {
			// Red Hat's default: the collector writes under the data
			// directory, and the unit's journal beside it holds only systemd's
			// lines — searched, it would say the server ran nothing slowly.
			r := sources.Refused[0]
			out.Reason = fmt.Sprintf("Postgres writes its statements to %s, which is %s. An administrator can add its directory to JD_LOG_ROOTS.", r.Path, r.Reason)
			return out
		}
		// The log sources' own reason says why — another machine, no
		// server found — and the page prints it beside this one.
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
	if err := s.slowFromLog(ctx, *primary, "postgres", w, out); err != nil {
		out.Reason = "The server log could not be searched: " + err.Error()
	}
	return out
}

// mongoQueryLog is the log's slow operations: the file or the container when
// this machine has one, else the lines the server keeps in memory, which is
// the one log a server on another machine hands over the connection.
func (s *Server) mongoQueryLog(ctx context.Context, conn *dbConnection, dsn string, w dbx.QueryLogWindow, probe dbHostProbe) *dbx.QueryLog {
	out := &dbx.QueryLog{Supported: true, Source: dbx.QuerySourceLog, Entries: []dbx.QueryEntry{}}
	if primary := primaryLog(s.dbLogSourcesFor(ctx, conn, dsn, probe)); primary != nil {
		if err := s.slowFromLog(ctx, *primary, "mongodb", w, out); err != nil {
			out.Reason = "The server log could not be searched: " + err.Error()
		}
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

// statementLines is how many lines the search keeps for each row asked for.
// Its budget is lines, and a statement is its head and every line it goes
// on in: asked for the newest 200 lines, twenty ten-line statements came
// back as the tails of the oldest and no head at all. The rows are counted
// once they are joined.
const statementLines = 50

// maxStatementLines is the most lines one search keeps (the collector's own
// cap).
const maxStatementLines = 20_000

// slowFromLog searches a server log for its slow statements through the
// engine's lens, the rotated files included — the answer to "what was slow
// last night" is usually in yesterday's file — and writes them into out.
func (s *Server) slowFromLog(ctx context.Context, src dbLogSource, lens string, w dbx.QueryLogWindow, out *dbx.QueryLog) error {
	fields := []string{"event:slow"}
	if w.MinMs > 0 {
		fields = append(fields, "duration_ms:>="+strconv.FormatFloat(w.MinMs, 'f', -1, 64))
	}
	opts := logsx.SearchOptions{
		Filter: logsx.Filter{Lens: lens, Fields: fields},
		Since:  w.Since, Until: w.Until, Limit: min(w.Limit*statementLines, maxStatementLines), Archives: true,
	}
	if err := opts.Validate(); err != nil {
		return err
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
		return err
	}
	entries := slowEntries(res.Lines)
	out.Truncated = res.Truncated || len(entries) > w.Limit
	out.Entries = entries[:min(len(entries), w.Limit)]
	if !res.Complete {
		// Files and a container are read oldest first, so a search the
		// deadline stopped is missing the newest statements. The journal is
		// read newest first, so there it is the oldest that are missing.
		out.Truncated = true
		missing := "newest"
		if src.Kind == logsx.KindJournal {
			missing = "oldest"
		}
		out.Reason = "The search ran out of time before it read the whole window, so the " + missing + " statements may be missing. A shorter window reads faster."
	}
	return nil
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
