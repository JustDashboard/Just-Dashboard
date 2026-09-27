package api

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/logsx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountLogRoutes(r chi.Router) {
	r.Route("/logs", func(r chi.Router) {
		r.Method(http.MethodGet, "/sources", s.handle(s.handleLogSources))
		r.Method(http.MethodGet, "/source", s.handle(s.handleLogSource))
		r.Method(http.MethodGet, "/search", s.handle(s.handleLogSearch))
		r.Method(http.MethodGet, "/download", s.handle(s.handleLogDownload))
		r.Method(http.MethodGet, "/stream", s.handle(s.handleLogStream))
		r.Method(http.MethodGet, "/logrotate", s.handle(s.handleLogrotate))
		r.Method(http.MethodGet, "/retention", s.handle(s.handleLogRetention))
	})
}

// logSourceIndex is one answer to "what can I read on this host". The unit
// list ships with it rather than from a second request because the journal is
// one source with a thousand faces: putting every unit in the source list
// would bury syslog under systemd's inventory, and fetching them separately
// means the unit picker is empty for the first second after the journal is
// chosen — which reads as "this host has no units".
type logSourceIndex struct {
	Sources []logsx.Source    `json:"sources"`
	Units   []logJournalUnit  `json:"units"`
	Roots   []string          `json:"roots"`
	Missing map[string]string `json:"missing"`
}

type logJournalUnit struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Active      string `json:"active"`
	// Lens is what the unit's journal reads through, so the picker can offer
	// the unit's quick views before its stream opens.
	Lens string `json:"lens,omitempty"`
}

// handleLogSources merges file-backed sources with the live sources that are
// not files — docker containers and PM2 processes — so the viewer offers one
// list regardless of where the log actually lives.
func (s *Server) handleLogSources(w http.ResponseWriter, r *http.Request) error {
	discovered, err := s.modules.logs.Discover(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	// Nothing is offered that cannot be opened: auth data needs system.admin
	// to read, so it is not listed for anyone else.
	admin := httpx.MustPrincipal(r).Can(auth.CapSystemAdmin)
	sources := make([]logsx.Source, 0, len(discovered))
	for _, src := range discovered {
		if admin || !authLogFile(src.Path) {
			sources = append(sources, src)
		}
	}
	index := logSourceIndex{
		Sources: sources,
		Units:   []logJournalUnit{},
		Roots:   s.modules.logs.Roots(),
		// A source kind that is absent explains itself here rather than simply
		// not appearing. "No containers" and "no Docker on this host" are
		// different sentences, and a viewer that renders both as an empty
		// group teaches the operator to distrust the list.
		Missing: map[string]string{},
	}

	if containers, err := s.modules.docker.ListContainers(r.Context(), true); err == nil {
		for _, c := range containers {
			index.Sources = append(index.Sources, s.containerSource(r.Context(), c))
		}
		// A compose project is also one source, every container merged by
		// time — "what did the stack do at 03:12" is a question about the
		// stack. Its containers stay listed on their own too.
		index.Sources = append(index.Sources, s.stackSources(r.Context(), containers)...)
	} else {
		index.Missing["docker"] = err.Error()
	}

	if s.modules.pm2.Available() {
		if list, err := s.modules.pm2.List(r.Context()); err == nil {
			if len(list) == 0 {
				index.Missing["pm2"] = "PM2 has no managed processes"
			}

			for _, p := range list {
				detail := "stdout and stderr, merged"
				if err := s.checkPM2LogPaths(p.OutLogPath, p.ErrLogPath); err != nil {
					detail = "Logs unavailable: ask an administrator to include this log directory in JD_LOG_ROOTS."
				}
				index.Sources = append(index.Sources, logsx.Source{
					ID: "pm2:" + pm2LogIdentity(p), Label: p.Name + " (" + p.DaemonID + ")", Kind: logsx.KindPM2,
					Path: p.OutLogPath, Detail: detail, Status: p.Status,
					Lens: logsx.DetectLens(logsx.LensTarget{Kind: logsx.KindPM2}),
				})
			}
		} else {
			index.Missing["pm2"] = err.Error()
		}
	} else {
		index.Missing["pm2"] = "PM2 is not installed on this host"
	}

	if s.modules.systemd.Available() {
		index.Sources = append(index.Sources, logsx.Source{
			ID:     "journal:",
			Label:  "systemd journal",
			Kind:   logsx.KindJournal,
			Detail: "Every unit on the host — pick one below to narrow it",
			Lens:   logsx.DetectLens(logsx.LensTarget{Kind: logsx.KindJournal}),
		})
		if units, err := s.modules.systemd.List(r.Context()); err == nil {
			for _, u := range units {
				if !admin && authJournalUnit(u.Name) {
					continue
				}
				index.Units = append(index.Units, logJournalUnit{
					Name: u.Name, Description: u.Description, Active: u.ActiveState,
					Lens: logsx.DetectLens(logsx.LensTarget{Kind: logsx.KindJournal, Unit: u.Name}),
				})
			}
		}
	} else {
		index.Missing["journal"] = "systemd is not running on this host"
	}

	httpx.JSON(w, http.StatusOK, index)
	return nil
}

// logTarget is a parsed source id. Resolving the id once, here, is what lets
// the stream, the search and the export agree about what "this source" means —
// they used to each re-split the string, and only the stream knew about PM2.
type logTarget struct {
	kind logsx.SourceKind
	// id is the container id, PM2 identity, systemd unit or compose project.
	id string
	// idents are a journal-id source's syslog identifiers.
	idents []string
	path   string
	label  string
}

// stackProject is what docker compose accepts as a project name, which is
// also what keeps the id from being anything but a name.
var stackProject = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// maxJournalIdents bounds a journal-id source. Each is its own -t, and the
// sources the pages build name five at most.
const maxJournalIdents = 16

func parseLogTarget(raw string) (logTarget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return logTarget{}, httpx.BadRequest("source query parameter is required")
	}
	if id, ok := strings.CutPrefix(raw, "docker:"); ok {
		if id == "" {
			return logTarget{}, httpx.BadRequest("docker source needs a container id")
		}
		return logTarget{kind: logsx.KindDocker, id: id, label: id}, nil
	}
	if project, ok := strings.CutPrefix(raw, "stack:"); ok {
		if !stackProject.MatchString(project) {
			return logTarget{}, httpx.BadRequest("stack source needs a compose project name")
		}
		return logTarget{kind: logsx.KindStack, id: project, label: project}, nil
	}
	if name, ok := strings.CutPrefix(raw, "pm2:"); ok {
		if name == "" {
			return logTarget{}, httpx.BadRequest("pm2 source needs a process name")
		}
		return logTarget{kind: logsx.KindPM2, id: name, label: name}, nil
	}
	if list, ok := strings.CutPrefix(raw, "journal-id:"); ok {
		idents := []string{}
		for _, ident := range strings.Split(list, ",") {
			if ident = strings.TrimSpace(ident); ident == "" {
				continue
			}
			if err := procs.ValidateName(ident); err != nil {
				return logTarget{}, httpx.BadRequest("journal-id source: %v", err)
			}
			idents = append(idents, ident)
		}
		if len(idents) == 0 || len(idents) > maxJournalIdents {
			return logTarget{}, httpx.BadRequest("journal-id source needs between 1 and %d syslog identifiers", maxJournalIdents)
		}
		return logTarget{kind: logsx.KindJournalID, idents: idents, label: strings.Join(idents, ", ")}, nil
	}
	if rest, ok := strings.CutPrefix(raw, "kernel:"); ok {
		if rest != "" {
			return logTarget{}, httpx.BadRequest("the kernel source takes nothing after kernel:")
		}
		return logTarget{kind: logsx.KindKernel, label: "kernel"}, nil
	}
	if unit, ok := strings.CutPrefix(raw, "journal:"); ok {
		label := "systemd journal"
		if unit != "" {
			label = unit
		}
		return logTarget{kind: logsx.KindJournal, id: unit, label: label}, nil
	}
	path := strings.TrimPrefix(raw, "file:")
	if !strings.HasPrefix(path, "/") {
		return logTarget{}, httpx.BadRequest("a file source must be an absolute path")
	}
	return logTarget{kind: logsx.KindSystem, path: filepath.Clean(path), label: filepath.Base(path)}, nil
}

// logTargetFor parses a source id and refuses auth data to a caller without
// system.admin. Every /logs route that reads a source goes through it — the
// stream, the search, the export, the retention verdict and the description —
// so there is no second door to the same lines.
//
// The reason is the one /logins/failed gives: "Invalid user <what was typed>"
// is sometimes a password typed into the username prompt, and a sudo line
// carries the command that was run. Reading those is closer to reading
// somebody's keystrokes than to operational history. The whole journal stays
// readable, as it was before this gate existed; narrowing it to sshd is what
// is gated.
func (s *Server) logTargetFor(r *http.Request, raw string) (logTarget, error) {
	target, err := parseLogTarget(raw)
	if err != nil {
		return logTarget{}, err
	}
	if authLogTarget(target) && !httpx.MustPrincipal(r).Can(auth.CapSystemAdmin) {
		return logTarget{}, httpx.Err(http.StatusForbidden, "forbidden",
			"Login and sudo records need an administrator: failed logins can hold passwords typed into the username prompt.")
	}
	return target, nil
}

// authIdents are the programs whose lines are auth data.
var authIdents = map[string]bool{
	"sshd": true, "sshd-session": true, "sshd-auth": true, "sudo": true, "su": true,
	"login": true, "systemd-logind": true,
}

func authLogTarget(t logTarget) bool {
	switch t.kind {
	case logsx.KindJournal:
		return authJournalUnit(t.id)
	case logsx.KindJournalID:
		for _, ident := range t.idents {
			if authIdents[ident] {
				return true
			}
		}
		return false
	case logsx.KindSystem:
		return authLogFile(t.path)
	}
	return false
}

// authJournalUnit is sshd's unit under either distribution's name, and the
// per-connection instances a socket-activated sshd runs as.
func authJournalUnit(unit string) bool {
	name := strings.TrimSuffix(unit, ".service")
	return name == "ssh" || name == "sshd" || strings.HasPrefix(name, "ssh@") || strings.HasPrefix(name, "sshd@")
}

// authLogFile judges the file and anything it resolves to, and the rotated
// generations too: auth.log.1 and secure-20240612 are the same data a day
// older, and a symlink named app.log pointing at auth.log is still auth.log.
// A generation is a number after the name, so secure-api.log is not one.
func authLogFile(path string) bool {
	if path == "" {
		return false
	}
	names := []string{filepath.Base(path)}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		names = append(names, filepath.Base(resolved))
	}
	for _, name := range names {
		for _, base := range []string{"auth.log", "secure"} {
			rest, ok := strings.CutPrefix(name, base)
			if !ok {
				continue
			}
			if rest == "" || (len(rest) > 1 && (rest[0] == '.' || rest[0] == '-') && rest[1] >= '0' && rest[1] <= '9') {
				return true
			}
		}
	}
	return false
}

// logFilterFrom reads the filter every log route shares. One parser means the
// live view, the search and the export cannot drift apart — an operator who
// narrows a stream to one request id and then exports it gets that, not the
// whole file, which is what the previous export did.
func logFilterFrom(q url.Values) logsx.Filter {
	f := logsx.Filter{
		Query:      q.Get("q"),
		Exclude:    q.Get("exclude"),
		Regex:      q.Get("regex") == "true",
		IgnoreCase: q.Get("ignoreCase") != "false",
		Fields:     q["f"],
	}
	if levels := q.Get("levels"); levels != "" {
		f.Levels = strings.Split(levels, ",")
	}
	return f
}

// splitList reads a comma-separated parameter, dropping empty entries.
func splitList(v string) []string {
	if v == "" {
		return nil
	}
	out := []string{}
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func logSearchOptions(q url.Values) (logsx.SearchOptions, error) {
	opts := logsx.SearchOptions{
		Filter:          logFilterFrom(q),
		Limit:           atoiDefault(q.Get("limit"), 2000),
		Before:          atoiDefault(q.Get("before"), 0),
		After:           atoiDefault(q.Get("after"), 0),
		Archives:        q.Get("archives") == "true",
		Head:            q.Get("order") == "asc",
		Facets:          splitList(q.Get("facets")),
		Measure:         q.Get("measure"),
		Sample:          splitList(q.Get("sample")),
		HistogramBy:     q.Get("histogramBy"),
		HistogramValues: splitList(q.Get("histogramValues")),
	}
	if v := q.Get("facetLimit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return opts, httpx.BadRequest("facetLimit must be a positive number")
		}
		opts.FacetLimit = n
	}
	if v := q.Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			opts.Since = t
		}
	}
	if v := q.Get("until"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			opts.Until = t
		}
	}
	return opts, nil
}

// logLens resolves the lens a read goes through: the one asked for, or the
// one the source is detected as. An unknown lens asked for by name is the
// caller's mistake and a 400; a detected lens this build does not register is
// no lens, since the operator asked for nothing. A stack resolves per
// container, so only a lens asked for applies to it as a whole.
func (s *Server) logLens(ctx context.Context, target logTarget, asked string) (string, error) {
	if asked != "" {
		if _, err := logsx.LensByID(asked); err != nil {
			return "", httpx.BadRequest("%v; this build reads %s, or none", err, strings.Join(logsx.LensIDs(), ", "))
		}
		return asked, nil
	}
	switch target.kind {
	case logsx.KindStack:
		return "", nil
	case logsx.KindDocker:
		d, err := s.modules.docker.Inspect(ctx, target.id)
		if err != nil {
			// The read reports the container's absence itself, in the words
			// it always has.
			return "", nil
		}
		return logsx.DetectLens(logsx.LensTarget{Kind: logsx.KindDocker, Image: d.Image}), nil
	case logsx.KindJournalID:
		return logsx.DetectLens(logsx.LensTarget{Kind: target.kind, Unit: target.idents[0]}), nil
	}
	return logsx.DetectLens(logsx.LensTarget{Kind: target.kind, Path: target.path, Unit: target.id}), nil
}

// maxJournalPriority turns the operator's level chips into the one number
// journalctl understands. Only the maximum is pushed down — the chips are a
// set and `-p` takes a range — so this narrows the read without changing the
// answer, which the exact test in the collector still decides.
func maxJournalPriority(levels []string) int {
	if len(levels) == 0 {
		return -1
	}
	worst := -1
	for _, l := range levels {
		p := -1
		switch logsx.Normalise(l) {
		case "critical":
			p = 2
		case "error":
			p = 3
		case "warn":
			p = 4
		case "info":
			p = 6
		case "debug":
			p = 7
		}
		if p < 0 {
			// An unrecognised chip (the "unknown" pseudo-level) cannot be
			// expressed as a priority, and narrowing anyway would hide lines
			// the operator asked for.
			return -1
		}
		if p > worst {
			worst = p
		}
	}
	return worst
}

// journalPriority is the -p pushed down for a filter. With a lens reading the
// lines, it never narrows below 6: a lens raises lines the program filed at
// info — Postgres writes its FATAL to stderr, which the journal files at 6 —
// and narrowing to 0..3 would hide exactly the lines "errors" should show.
// The exact level test still runs here. clamped reports that the chips asked
// for less than was read, which widens the tail's window as a text filter
// does.
func journalPriority(spec logsx.Filter) (p int, clamped bool) {
	p = maxJournalPriority(spec.Levels)
	if p >= 0 && p < 6 && spec.Lens != "" && spec.Lens != logsx.LensNone {
		return 6, true
	}
	return p, false
}

// journalOptionsFor is the journalctl selection a journal-kind source names.
func journalOptionsFor(t logTarget, boot bool) procs.JournalOptions {
	o := procs.JournalOptions{Boot: boot, MaxPriority: -1}
	switch t.kind {
	case logsx.KindJournal:
		o.Unit = t.id
	case logsx.KindJournalID:
		o.Identifiers = t.idents
	case logsx.KindKernel:
		o.Kernel = true
	}
	return o
}

// journalRoute hands a journal line to the lens its program has when that is
// not the stream's own. One journalctl run holds two kinds of line: -u reads
// the manager's "Started …" and "Failed with result …" as well as the unit's
// own output, and ssh.service's lines come from sshd-session. The manager's
// lines always go to the systemd lens; the other programs only when the lens
// was detected rather than forced, since forcing one is the operator saying
// how they want the unit's own lines read. The whole journal routes only the
// manager: its own lens is the composite that already dispatches by program.
func journalRoute(t logTarget, forced bool) func(*logsx.Line) string {
	perProgram := !forced && (t.kind == logsx.KindJournalID || (t.kind == logsx.KindJournal && t.id != ""))
	return func(l *logsx.Line) string {
		id := logsx.ProgramLens(l.Attrs["program"])
		if id == "systemd" || perProgram {
			return id
		}
		return ""
	}
}

func (s *Server) handleLogSearch(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	// The old route took ?path= and nothing else, which is why the frontend
	// never called it: three of the six source kinds have no path.
	raw := q.Get("source")
	if raw == "" {
		raw = q.Get("path")
	}
	target, err := s.logTargetFor(r, raw)
	if err != nil {
		return err
	}
	opts, err := logSearchOptions(q)
	if err != nil {
		return err
	}

	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()

	if opts.Filter.Lens, err = s.logLens(ctx, target, q.Get("lens")); err != nil {
		return err
	}
	// Everything the search can be refused for is refused here, before the
	// source is chosen: a bad expression found inside a container read would
	// come back as a daemon error rather than as the operator's mistake.
	if err := opts.Validate(); err != nil {
		return httpx.BadRequest("%v", err)
	}

	switch target.kind {
	case logsx.KindDocker:
		res, err := s.searchContainer(ctx, target.id, opts)
		if err != nil {
			return s.dockerErr(err)
		}
		httpx.JSON(w, http.StatusOK, res)
		return nil
	case logsx.KindStack:
		res, err := s.searchStack(ctx, target.id, opts)
		if err != nil {
			return err
		}
		httpx.JSON(w, http.StatusOK, res)
		return nil
	case logsx.KindJournal, logsx.KindJournalID, logsx.KindKernel:
		res, err := s.searchJournal(ctx, target, opts, q.Get("boot") == "true", q.Get("lens") != "")
		if err != nil {
			return mapProcsError(err)
		}
		httpx.JSON(w, http.StatusOK, res)
		return nil
	case logsx.KindPM2:
		targets, err := s.pm2Targets(ctx, target.id)
		if err != nil {
			return mapProcsError(err)
		}
		res, err := s.modules.logs.SearchTargets(ctx, targets, opts)
		if err != nil {
			return httpx.BadRequest("%v", err)
		}
		httpx.JSON(w, http.StatusOK, res)
		return nil
	}

	res, err := s.modules.logs.Search(ctx, target.path, opts)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// pm2Targets is the pair of permitted files PM2 writes,
// tagged with the stream each one is. Searching only stdout answered "not
// found" for a crash sitting in the error log, which is the one thing anybody
// searches a PM2 process for.
func (s *Server) pm2Targets(ctx context.Context, name string) ([]logsx.SearchTarget, error) {
	outPath, errPath, err := s.pm2LogPaths(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := s.checkPM2LogPaths(outPath, errPath); err != nil {
		return nil, err
	}
	return []logsx.SearchTarget{{Path: outPath, Stream: "stdout"}, {Path: errPath, Stream: "stderr"}}, nil
}

// searchContainer reads a container's whole log through the Engine and runs
// the same collector a file search uses. Docker keeps the log itself, so there
// is no file to grep and no rotation to follow — the daemon's own since/until
// do the narrowing that logrotate archives do for a file.
func (s *Server) searchContainer(ctx context.Context, id string, opts logsx.SearchOptions) (*logsx.SearchResult, error) {
	c, err := logsx.NewCollector(opts)
	if err != nil {
		return nil, err
	}
	ch, closer, err := s.modules.docker.Logs(ctx, id, containerSearchOptions(opts))
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	c.NextFile(id, false, nil)
	st := c.Stream()
	n := 0
	for raw := range ch {
		if ctx.Err() != nil {
			c.Incomplete()
			break
		}
		n++
		stamp, text := splitDockerStamp(raw.Text)
		if c.Skip(st, text) {
			continue
		}
		line := readDockerLine(stamp, text, raw, st, containerTag{})
		line.No = n
		c.Feed(line)
	}
	return c.Result(), nil
}

func containerSearchOptions(opts logsx.SearchOptions) dockerx.LogOptions {
	logOpts := dockerx.LogOptions{Tail: "all", Timestamps: true}
	if !opts.Since.IsZero() {
		logOpts.Since = opts.Since.Format(time.RFC3339)
	}
	if !opts.Until.IsZero() {
		logOpts.Until = opts.Until.Format(time.RFC3339)
	}
	return logOpts
}

// searchJournal reads the window rather than a line count. A history search
// bounded by `-n` answers "is it in the last 300 records", which is not the
// question — so the time window is the bound here, and the absence of one is
// reported as an incomplete answer rather than silently capped.
func (s *Server) searchJournal(ctx context.Context, target logTarget, opts logsx.SearchOptions, boot, forced bool) (*logsx.SearchResult, error) {
	c, err := logsx.NewCollector(opts)
	if err != nil {
		return nil, err
	}
	jopts := journalOptionsFor(target, boot)
	jopts.Since = journalTimeSpec(opts.Since)
	jopts.Until = journalTimeSpec(opts.Until)
	jopts.MaxPriority, _ = journalPriority(opts.Filter)
	if jopts.Since == "" && !boot {
		// journalctl with no bound at either end walks the entire persistent
		// journal, which on a long-lived host is gigabytes. A default window
		// keeps the unbounded case answerable, and the result says it was
		// bounded so nobody reads an empty answer as "it never happened". A
		// boot is its own bound, so it needs no second one.
		jopts.Since = "2 days ago"
	}
	c.NextFile("journal", false, nil)
	st := c.Stream()
	st.Route(journalRoute(target, forced))
	n := 0
	if _, err := s.streamJournalInto(ctx, jopts, func(e procs.JournalEntry) {
		n++
		if c.Skip(st, e.Message) {
			return
		}
		line := journalLine(e, st)
		line.No = n
		c.Feed(line)
	}); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		c.Incomplete()
	}
	return c.Result(), nil
}

// journalTimeSpec renders a bound in the shape journalctl's parser accepts,
// which is not RFC3339: it wants "2006-01-02 15:04:05" and reads a bare "Z" as
// a timezone it does not know.
func journalTimeSpec(parsed time.Time) string {
	if parsed.IsZero() {
		return ""
	}
	return parsed.UTC().Format("2006-01-02 15:04:05")
}

// streamJournalInto runs journalctl and hands each decoded record to fn. Both
// the search and the live tail use it, so the two cannot disagree about how a
// journal record becomes a log line.
func (s *Server) streamJournalInto(ctx context.Context, opts procs.JournalOptions, fn func(procs.JournalEntry)) (int, error) {
	cmd, err := procs.JournalCommandOpts(ctx, opts)
	if err != nil {
		return 0, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		cmd.Wait()
	}()
	count := 0
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if ctx.Err() != nil {
			return count, nil
		}
		if e, ok := procs.ParseJournalLine(sc.Bytes()); ok {
			count++
			fn(e)
		}
	}
	return count, sc.Err()
}

// journalLine maps a journal record onto the viewer's one line shape, reading
// it through st when there is one.
//
// The message goes through ParseLine like any other line, so a service that
// logs JSON under systemd gets its message and fields and a colour code is
// stripped. The level is decided in a fixed order: what the lens read from the
// format itself, then a level key in the message's own structure, then the
// journal's PRIORITY — never the free-text word scan, which would let
// "error-reporting enabled" in prose overrule a priority the program chose.
// The priority still becomes the level of a plain line, so the journal's
// numbers and a text log's words end up as one vocabulary and one set of
// chips.
//
// The record's own fields are kept as attrs, because they are what the
// manager's lines mean: the unit a "Failed with result" is about, the
// invocation that groups one run's lines, the exit code.
func journalLine(e procs.JournalEntry, st *logsx.Stream) logsx.Line {
	source := e.Syslog
	if source == "" {
		source = strings.TrimSuffix(e.Unit, ".service")
	}
	if e.PID != "" && source != "" {
		source += "[" + e.PID + "]"
	}
	line := logsx.ParseLine(e.Message, source)
	line.ApplyPriority(e.Priority)
	line.SetAttr("unit", defaultStr(e.About, e.Unit))
	line.SetAttr("program", defaultStr(e.Syslog, e.Comm))
	line.SetAttr("pid", e.PID)
	line.SetAttr("invocation", e.Invocation)
	line.SetAttr("message_id", e.MessageID)
	line.SetAttr("result", e.Result)
	line.SetAttr("exit_code", e.ExitCode)
	line.SetAttr("exit_status", e.ExitStatus)
	if st != nil {
		st.Read(&line)
	}
	// The journal's stamp is authoritative: whatever a lens or the parser
	// found in the message text is when the program thought it was, and the
	// journal is when it was.
	if !e.Timestamp.IsZero() {
		utc := e.Timestamp.UTC()
		line.Timestamp = &utc
	}
	return line
}

// splitDockerStamp strips the RFC3339 prefix Docker adds when timestamps are
// asked for. Leaving it in the text would draw the timestamp twice on every
// line, and the built-in parser cannot read it: Docker emits nanoseconds,
// which is longer than any layout the file parser knows.
func splitDockerStamp(text string) (*time.Time, string) {
	if i := strings.IndexByte(text, ' '); i > 0 {
		if ts, err := time.Parse(time.RFC3339Nano, text[:i]); err == nil {
			utc := ts.UTC()
			return &utc, text[i+1:]
		}
	}
	return nil, text
}

// dockerLine turns one line of a container's output into the viewer's line,
// read through st when there is one.
func dockerLine(l dockerx.LogLine, st *logsx.Stream) logsx.Line {
	stamp, text := splitDockerStamp(l.Text)
	return readDockerLine(stamp, text, l, st, containerTag{})
}

// containerTag is what a stack adds to each of its containers' lines: the
// service as the source and as an attr, the short container id, and the lens
// the container reads through when it is not the stack's.
type containerTag struct {
	service   string
	container string
	stackLens string
}

func readDockerLine(stamp *time.Time, text string, l dockerx.LogLine, st *logsx.Stream, tag containerTag) logsx.Line {
	source := l.Service
	if tag.service != "" {
		source = tag.service
	}
	// ParseLine strips the terminal control a build tool writes, so the text
	// kept here is the text the level scan and the operator's search saw.
	line := logsx.ParseLine(text, source)
	line.Stream = l.Stream
	line.SetAttr("service", tag.service)
	line.SetAttr("container", tag.container)
	if st != nil {
		st.Read(&line)
		if line.Event != "" && line.Lens == "" && st.Lens() != tag.stackLens && tag.service != "" {
			line.Lens = st.Lens()
		}
	}
	// Docker's stamp is authoritative, put back after the lens for the reason
	// journalLine gives.
	if stamp != nil {
		line.Timestamp = stamp
	}
	// stderr is deliberately *not* promoted to a level here.
	//
	// It used to be: a stderr line the word scan could not classify was filed
	// as an error. That is true of a crash and false of almost everything else
	// that reaches stderr — npm's notices, Prisma's "Update available" banner,
	// every CLI that treats stderr as a second stdout. A freshly deployed,
	// perfectly healthy Next.js project opened its Logs tab reading "13
	// errors", all of them a version notice inside an ASCII box. A stream is a
	// stream; the viewer already marks it, and a level the line does not claim
	// is the page inventing a reading.
	return line
}

// handleLogDownload streams the requested window straight to the client rather
// than buffering it, so exporting a day out of a large log costs no memory.
func (s *Server) handleLogDownload(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	raw := q.Get("source")
	if raw == "" {
		raw = q.Get("path")
	}
	target, err := s.logTargetFor(r, raw)
	if err != nil {
		return err
	}
	opts, err := logSearchOptions(q)
	if err != nil {
		return err
	}
	// An export is a file to keep, so it is ordered oldest-first and not
	// capped: the cap exists to keep a browser responsive, which a download
	// does not need.
	opts.Head = true
	opts.Limit = 0

	for _, spec := range []struct{ name, value string }{{"since", q.Get("since")}, {"until", q.Get("until")}} {
		if spec.value != "" {
			if _, err := time.Parse(time.RFC3339, spec.value); err != nil {
				return httpx.BadRequest("%s must be an RFC3339 timestamp", spec.name)
			}
		}
	}

	ctx, cancel := timeoutCtx(r, 5*time.Minute)
	defer cancel()

	if opts.Filter.Lens, err = s.logLens(ctx, target, q.Get("lens")); err != nil {
		return err
	}
	if err := opts.Validate(); err != nil {
		return httpx.BadRequest("%v", err)
	}

	name := strings.NewReplacer("/", "-", ":", "-", " ", "-", ",", "-").Replace(strings.TrimPrefix(target.label, "/"))
	if name == "" {
		name = "logs"
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q",
		name+"-"+time.Now().UTC().Format("20060102-150405")+".log"))

	targets := []logsx.SearchTarget{{Path: target.path}}
	switch target.kind {
	case logsx.KindPM2:
		found, err := s.pm2Targets(ctx, target.id)
		if err != nil {
			return mapProcsError(err)
		}
		targets = found
	case logsx.KindDocker, logsx.KindStack, logsx.KindJournal, logsx.KindJournalID, logsx.KindKernel:
		// None is a file, so the export is the search result written out
		// rather than a byte range of something on disk.
		opts.Limit = 20000
		var res *logsx.SearchResult
		var err error
		switch target.kind {
		case logsx.KindDocker:
			res, err = s.searchContainer(ctx, target.id, opts)
		case logsx.KindStack:
			res, err = s.searchStack(ctx, target.id, opts)
		default:
			res, err = s.searchJournal(ctx, target, opts, q.Get("boot") == "true", q.Get("lens") != "")
		}
		if err != nil {
			s.Log.Error("log export failed", "source", raw, "err", err)
			return nil
		}
		for _, line := range res.Lines {
			stamp := ""
			if line.Timestamp != nil {
				stamp = line.Timestamp.Format(time.RFC3339) + " "
			}
			if _, err := fmt.Fprintf(w, "%s%s\n", stamp, line.Text); err != nil {
				return nil
			}
		}
		return nil
	}

	if _, err := s.modules.logs.RangeTargets(ctx, targets, opts, w); err != nil {
		// Headers are already committed; the truncated body plus the audit
		// record is the honest outcome here.
		s.Log.Error("log export failed", "path", target.path, "err", err)
	}
	return nil
}

// streamMeta is the frame the socket opens with. A viewer that starts with a
// short list of lines and no explanation cannot tell "this log is quiet" from
// "your filter matched almost nothing" from "we only looked at the last 32 MB",
// and those three call for completely different next moves.
type streamMeta struct {
	Kind     logsx.SourceKind `json:"kind"`
	Label    string           `json:"label"`
	Path     string           `json:"path,omitempty"`
	Filtered bool             `json:"filtered"`
	Prefill  *logsx.Prefill   `json:"prefill,omitempty"`
	Archives int              `json:"archives,omitempty"`
	Note     string           `json:"note,omitempty"`
	// Lens is the lens the lines are read through, which is what their event
	// names mean.
	Lens string `json:"lens,omitempty"`
}

// handleLogStream is the unified live tail. Every source kind — a file, a
// container, a PM2 process, the journal — is turned into the same parsed line
// here and filtered by the same code, which is the fix for the page's oldest
// lie: the grep box and the level chips were applied to file tails only, and
// silently did nothing for the three kinds that were delegated to another
// handler.
func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	raw := q.Get("source")
	if raw == "" {
		raw = q.Get("path")
	}
	target, err := s.logTargetFor(r, raw)
	if err != nil {
		return err
	}
	spec := logFilterFrom(q)
	if spec.Lens, err = s.logLens(r.Context(), target, q.Get("lens")); err != nil {
		return err
	}
	filter, err := logsx.NewFilter(spec)
	if err != nil {
		// Refused before the upgrade, so a bad regular expression is an error
		// the form can show rather than a socket that opens and stays empty.
		return httpx.BadRequest("%v", err)
	}
	lines := atoiDefault(q.Get("lines"), 400)
	if lines <= 0 || lines > 20000 {
		lines = 400
	}
	var members []stackMember
	if target.kind == logsx.KindStack {
		// A stack with no containers is a 404 the page can show, not a
		// socket that opens and ends.
		if members, err = s.stackMembers(r.Context(), target.id); err != nil {
			return err
		}
	}

	conn, err := s.WS.Upgrade(w, r)
	if err != nil {
		return nil
	}
	defer conn.Close()
	ctx, cancel := contextWithCancel(r)
	defer cancel()
	go conn.Keepalive(ctx)
	go conn.DrainControl(cancel)

	out := make(chan logsx.Line, 512)
	meta := streamMeta{Kind: target.kind, Label: target.label, Path: target.path, Filtered: !filter.Empty()}
	if spec.Lens != logsx.LensNone {
		meta.Lens = spec.Lens
	}

	switch target.kind {
	case logsx.KindDocker:
		if err := s.followContainer(ctx, target.id, lines, filter, out); err != nil {
			conn.SendError(err.Error())
			return nil
		}
	case logsx.KindStack:
		meta.Lens = stackLens(members, filter)
		s.followStack(ctx, members, filter, lines, out)
	case logsx.KindPM2:
		if err := s.followPM2(ctx, target.id, lines, filter, out); err != nil {
			conn.SendError(err.Error())
			return nil
		}
	case logsx.KindJournal, logsx.KindJournalID, logsx.KindKernel:
		if err := s.followJournal(ctx, target, lines, spec, filter, q.Get("boot") == "true", q.Get("lens") != "", out); err != nil {
			conn.SendError(err.Error())
			return nil
		}
	default:
		pre, err := s.followFile(ctx, target.path, lines, filter, out)
		if err != nil {
			conn.SendError(err.Error())
			return nil
		}
		// An unfiltered tail always opens on "the last n lines", which is not
		// news. The prefill is only worth reporting when a filter narrowed it,
		// because there a short list can mean either "few matches" or "we only
		// looked so far back" — and those call for different next moves.
		if !filter.Empty() {
			meta.Prefill = pre
		}
		meta.Archives = len(logsx.Archives(target.path))
		if pre != nil && !pre.Complete && !filter.Empty() {
			meta.Note = "The opening window is the matches in the last 32 MB of this file. Search history to go further back."
		}
	}

	conn.Send("meta", meta)
	pumpLogLines(ctx, conn, out)
	return nil
}

func (s *Server) followFile(ctx context.Context, path string, n int, f *logsx.Filter, out chan<- logsx.Line) (*logsx.Prefill, error) {
	ch, pre, err := s.modules.logs.TailLines(ctx, path, n, f)
	if err != nil {
		return nil, err
	}
	go forwardLines(ctx, ch, out, nil)
	return pre, nil
}

// followPM2 merges the two files PM2 writes, tagging each line with the stream
// it came from — which is the one thing `pm2 logs` gets right and a plain tail
// of one file loses.
func (s *Server) followPM2(ctx context.Context, name string, n int, f *logsx.Filter, out chan<- logsx.Line) error {
	outPath, errPath, err := s.pm2LogPaths(ctx, name)
	if err != nil {
		return err
	}
	if err := s.checkPM2LogPaths(outPath, errPath); err != nil {
		return err
	}
	started := 0
	for _, src := range []struct{ path, stream string }{{outPath, "stdout"}, {errPath, "stderr"}} {
		if src.path == "" || src.path == "/dev/null" {
			continue
		}
		ch, _, err := s.modules.logs.TailLines(ctx, src.path, n, f)
		if err != nil {
			continue
		}
		started++
		stream := src.stream
		go forwardLines(ctx, ch, out, func(l logsx.Line) logsx.Line {
			// The stream is recorded, not promoted to a level — see dockerLine
			// for why a process's stderr is a poor proxy for "this went wrong".
			l.Stream = stream
			return l
		})
	}
	if started == 0 {
		return fmt.Errorf("pm2 process %s has no readable log files", name)
	}
	return nil
}

// followContainer follows one container. Its single producer closes out when
// the container's log ends, which is what sends eof.
func (s *Server) followContainer(ctx context.Context, id string, n int, f *logsx.Filter, out chan<- logsx.Line) error {
	ch, closer, err := s.modules.docker.Logs(ctx, id, dockerx.LogOptions{
		Tail:       strconv.Itoa(n),
		Timestamps: true,
		Follow:     true,
	})
	if err != nil {
		return err
	}
	st := f.Stream("")
	go func() {
		defer close(out)
		defer closer.Close()
		pumpContainer(ctx, ch, st, containerTag{}, time.Time{}, out)
	}()
	return nil
}

// pumpContainer reads one container's log into out through st. It never
// closes out: a stack has one producer per container on one channel, and the
// first container to stop must not end the others' — or panic them, sending
// on a closed channel. The caller closes out once every producer returned.
// Lines stamped at or before after are dropped before anything reads them:
// the stack's opening window already sent them.
func pumpContainer(ctx context.Context, in <-chan dockerx.LogLine, st *logsx.Stream, tag containerTag, after time.Time, out chan<- logsx.Line) {
	for raw := range in {
		stamp, text := splitDockerStamp(raw.Text)
		if !after.IsZero() && stamp != nil && !stamp.After(after) {
			continue
		}
		if st.Skip(text) {
			continue
		}
		line := readDockerLine(stamp, text, raw, st, tag)
		if keep, _ := st.Keep(&line, true); !keep {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case out <- line:
		}
	}
}

func (s *Server) followJournal(ctx context.Context, target logTarget, n int, spec logsx.Filter, f *logsx.Filter, boot, forced bool, out chan<- logsx.Line) error {
	opts := journalOptionsFor(target, boot)
	opts.Lines, opts.Follow = n, true
	var clamped bool
	opts.MaxPriority, clamped = journalPriority(spec)
	// A text filter cannot be pushed into journalctl portably — `-g` needs a
	// build with PCRE2 and a version nobody can assume — so the window is
	// widened instead and the exact test happens here. Without that, "the last
	// 400 records, of which two mention this container" is an empty page in
	// front of a journal that has the answer. Field predicates and a level
	// the lens keeps from being pushed down narrow the same way.
	if spec.Query != "" || spec.Exclude != "" || len(spec.Fields) > 0 || clamped {
		opts.Lines = n * 25
		if opts.Lines > 20000 {
			opts.Lines = 20000
		}
	}
	cmd, err := procs.JournalCommandOpts(ctx, opts)
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	st := f.Stream("")
	st.Route(journalRoute(target, forced))
	go func() {
		// Killing the process group on exit stops journalctl -f; otherwise it
		// would linger after the browser tab closes.
		defer func() {
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
			cmd.Wait()
			close(out)
		}()
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			e, ok := procs.ParseJournalLine(sc.Bytes())
			if !ok || st.Skip(e.Message) {
				continue
			}
			line := journalLine(e, st)
			if keep, _ := st.Keep(&line, true); !keep {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case out <- line:
			}
		}
	}()
	return nil
}

// forwardLines copies one producer into the shared channel. It deliberately
// does not close the destination: PM2 has two producers feeding one channel,
// and the first file to end must not take the other with it. The pump ends on
// the context instead.
func forwardLines(ctx context.Context, in <-chan logsx.Line, out chan<- logsx.Line, tag func(logsx.Line) logsx.Line) {
	for line := range in {
		if tag != nil {
			line = tag(line)
		}
		select {
		case <-ctx.Done():
			return
		case out <- line:
		}
	}
}

// pumpLogLines batches onto the socket. Batching matters for busy logs: one
// frame per line saturates the browser's event loop long before it saturates
// the network.
func pumpLogLines(ctx context.Context, conn interface {
	Send(string, any) error
}, in <-chan logsx.Line) {
	batch := make([]logsx.Line, 0, 256)
	flush := time.NewTicker(150 * time.Millisecond)
	defer flush.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-in:
			if !ok {
				if len(batch) > 0 {
					conn.Send("logs", batch)
				}
				conn.Send("eof", nil)
				return
			}
			batch = append(batch, line)
			if len(batch) >= 256 {
				if err := conn.Send("logs", batch); err != nil {
					return
				}
				batch = batch[:0]
			}
		case <-flush.C:
			if len(batch) > 0 {
				if err := conn.Send("logs", batch); err != nil {
					return
				}
				batch = batch[:0]
			}
		}
	}
}

func (s *Server) handleLogrotate(w http.ResponseWriter, r *http.Request) error {
	st, err := logsx.LogrotateStatus(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, st)
	return nil
}

// handleLogRetention answers the question the rule list was being consulted
// for. A file with no rule governing it is the one that fills the disk, and it
// is exactly the entry a rule list cannot show, because it is the one that is
// not there.
func (s *Server) handleLogRetention(w http.ResponseWriter, r *http.Request) error {
	target, err := s.logTargetFor(r, defaultStr(r.URL.Query().Get("source"), r.URL.Query().Get("path")))
	if err != nil {
		return err
	}
	if target.kind == logsx.KindPM2 {
		outPath, _, err := s.pm2LogPaths(r.Context(), target.id)
		if err != nil {
			return mapProcsError(err)
		}
		target.path = outPath
	}
	if target.path == "" {
		return httpx.BadRequest("retention applies to file-backed sources only")
	}
	if err := s.modules.logs.Allow(target.path); err != nil {
		return httpx.BadRequest("%v", err)
	}
	st, err := logsx.LogrotateStatus(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	var size int64
	if fi, err := os.Stat(target.path); err == nil {
		size = fi.Size()
	}
	httpx.JSON(w, http.StatusOK, logsx.MatchRetention(st, target.path, size))
	return nil
}

// handleLogSource describes one source the way /logs/sources would list it,
// for a page that embeds a single log — a database's, a site's, a unit's — and
// needs its lens, size and archives without walking every root and asking
// Docker, PM2 and systemd about everything else on the host.
func (s *Server) handleLogSource(w http.ResponseWriter, r *http.Request) error {
	target, err := s.logTargetFor(r, r.URL.Query().Get("source"))
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	src, err := s.describeLogSource(ctx, target)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, src)
	return nil
}

func (s *Server) describeLogSource(ctx context.Context, t logTarget) (logsx.Source, error) {
	switch t.kind {
	case logsx.KindDocker:
		d, err := s.modules.docker.Inspect(ctx, t.id)
		if err != nil {
			return logsx.Source{}, s.dockerErr(err)
		}
		src := s.containerSource(ctx, d.Container)
		// The page asked by the id it holds, which may be a name; answering
		// under another id would make the two look like different sources.
		src.ID = "docker:" + t.id
		return src, nil
	case logsx.KindStack:
		members, err := s.stackMembers(ctx, t.id)
		if err != nil {
			return logsx.Source{}, err
		}
		containers := make([]dockerx.Container, len(members))
		for i, m := range members {
			containers[i] = m.container
		}
		return s.stackSources(ctx, containers)[0], nil
	case logsx.KindPM2:
		name, daemon, id, err := parsePM2LogIdentity(t.id)
		if err != nil {
			return logsx.Source{}, err
		}
		list, err := s.modules.pm2.List(ctx)
		if err != nil {
			return logsx.Source{}, mapProcsError(err)
		}
		for _, p := range list {
			if p.Name != name || (daemon != "" && (p.DaemonID != daemon || p.ID != id)) {
				continue
			}
			src := logsx.Source{
				ID: "pm2:" + t.id, Label: p.Name + " (" + p.DaemonID + ")", Kind: logsx.KindPM2,
				Path: p.OutLogPath, Detail: "stdout and stderr, merged", Status: p.Status,
				Lens: logsx.DetectLens(logsx.LensTarget{Kind: logsx.KindPM2}),
			}
			if err := s.checkPM2LogPaths(p.OutLogPath, p.ErrLogPath); err != nil {
				src.Detail = "Logs unavailable: ask an administrator to include this log directory in JD_LOG_ROOTS."
			} else if file, ok, _ := s.modules.logs.Describe(p.OutLogPath); ok {
				src.Size, src.Modified = file.Size, file.Modified
				src.Archives, src.ArchiveBytes, src.Rotated = file.Archives, file.ArchiveBytes, file.Rotated
			}
			return src, nil
		}
		return logsx.Source{}, httpx.Err(http.StatusNotFound, "not_found", "PM2 has no process "+name+".")
	case logsx.KindJournal, logsx.KindJournalID, logsx.KindKernel:
		lens, _ := s.logLens(ctx, t, "")
		src := logsx.Source{Label: t.label, Kind: t.kind, Lens: lens}
		switch t.kind {
		case logsx.KindJournal:
			src.ID = "journal:" + t.id
			src.Detail = "Every unit on the host — pick one below to narrow it"
			if t.id != "" {
				src.Detail = "What systemd recorded for this unit, and what it printed"
				if u, _, err := s.modules.systemd.Show(ctx, t.id); err == nil {
					src.Status = u.ActiveState
				}
			}
		case logsx.KindJournalID:
			src.ID = "journal-id:" + strings.Join(t.idents, ",")
			src.Detail = "The journal's lines from " + t.label
		case logsx.KindKernel:
			src.ID = "kernel:"
			src.Detail = "The kernel ring as the journal keeps it: the firewall, the OOM killer, the disks"
		}
		return src, nil
	}
	src, ok, err := s.modules.logs.Describe(t.path)
	if err != nil {
		return logsx.Source{}, httpx.BadRequest("%v", err)
	}
	if !ok {
		return logsx.Source{}, httpx.Err(http.StatusNotFound, "not_found", "There is no log file at "+t.path+".")
	}
	return src, nil
}

// PM2 metadata belongs to a host user. It cannot expand the reader's log roots.
func (s *Server) checkPM2LogPaths(paths ...string) error {
	for _, path := range paths {
		if path == "" || path == "/dev/null" {
			continue
		}
		if err := s.modules.logs.Allow(path); err != nil {
			return httpx.Err(http.StatusForbidden, "pm2_log_roots_required", "PM2 log path is outside JD_LOG_ROOTS; ask an administrator to configure its log directory.")
		}
	}
	return nil
}
func pm2LogIdentity(process procs.PM2Process) string {
	return url.PathEscape(process.DaemonID) + "/" + strconv.Itoa(process.ID) + "/" + url.PathEscape(process.Name)
}
func parsePM2LogIdentity(identity string) (name, daemon string, id int, err error) {
	parts := strings.SplitN(identity, "/", 3)
	if len(parts) == 1 {
		return identity, "", -1, nil
	}
	if len(parts) != 3 {
		return "", "", -1, httpx.BadRequest("PM2 source identity is malformed")
	}
	daemon, err = url.PathUnescape(parts[0])
	if err != nil || daemon == "" {
		return "", "", -1, httpx.BadRequest("PM2 source account is malformed")
	}
	id, err = strconv.Atoi(parts[1])
	if err != nil || id < 0 {
		return "", "", -1, httpx.BadRequest("PM2 source process id is malformed")
	}
	name, err = url.PathUnescape(parts[2])
	if err != nil || name == "" {
		return "", "", -1, httpx.BadRequest("PM2 source process name is malformed")
	}
	return name, daemon, id, nil
}
func (s *Server) pm2LogPaths(ctx context.Context, identity string) (string, string, error) {
	name, daemon, id, err := parsePM2LogIdentity(identity)
	if err != nil {
		return "", "", err
	}
	return s.modules.pm2.LogPathsTarget(ctx, name, daemon, id)
}
