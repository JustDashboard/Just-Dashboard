package api

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/logsx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountLogRoutes(r chi.Router) {
	r.Route("/logs", func(r chi.Router) {
		r.Method(http.MethodGet, "/sources", s.handle(s.handleLogSources))
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
}

// handleLogSources merges file-backed sources with the live sources that are
// not files — docker containers and PM2 processes — so the viewer offers one
// list regardless of where the log actually lives.
func (s *Server) handleLogSources(w http.ResponseWriter, r *http.Request) error {
	sources, err := s.modules.logs.Discover(r.Context())
	if err != nil {
		return httpx.Internal(err)
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
			detail := c.Image
			if c.ComposeStack != "" {
				detail = c.ComposeStack + " · " + c.Image
			}
			index.Sources = append(index.Sources, logsx.Source{
				ID:     "docker:" + c.ID,
				Label:  c.Name,
				Kind:   logsx.KindDocker,
				Detail: detail,
				Status: c.State,
			})
		}
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
		})
		if units, err := s.modules.systemd.List(r.Context()); err == nil {
			for _, u := range units {
				index.Units = append(index.Units, logJournalUnit{
					Name: u.Name, Description: u.Description, Active: u.ActiveState,
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
	kind  logsx.SourceKind
	id    string // container id, PM2 name or systemd unit
	path  string
	label string
}

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
	if name, ok := strings.CutPrefix(raw, "pm2:"); ok {
		if name == "" {
			return logTarget{}, httpx.BadRequest("pm2 source needs a process name")
		}
		return logTarget{kind: logsx.KindPM2, id: name, label: name}, nil
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
	}
	if levels := q.Get("levels"); levels != "" {
		f.Levels = strings.Split(levels, ",")
	}
	return f
}

func logSearchOptions(q url.Values) logsx.SearchOptions {
	opts := logsx.SearchOptions{
		Filter:   logFilterFrom(q),
		Limit:    atoiDefault(q.Get("limit"), 2000),
		Before:   atoiDefault(q.Get("before"), 0),
		After:    atoiDefault(q.Get("after"), 0),
		Archives: q.Get("archives") == "true",
		Head:     q.Get("order") == "asc",
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
	return opts
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

func (s *Server) handleLogSearch(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	// The old route took ?path= and nothing else, which is why the frontend
	// never called it: three of the six source kinds have no path.
	raw := q.Get("source")
	if raw == "" {
		raw = q.Get("path")
	}
	target, err := parseLogTarget(raw)
	if err != nil {
		return err
	}
	opts := logSearchOptions(q)

	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()

	switch target.kind {
	case logsx.KindDocker:
		res, err := s.searchContainer(ctx, target.id, opts)
		if err != nil {
			return s.dockerErr(err)
		}
		httpx.JSON(w, http.StatusOK, res)
		return nil
	case logsx.KindJournal:
		res, err := s.searchJournal(ctx, target.id, opts, q.Get("boot") == "true")
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
	logOpts := dockerx.LogOptions{Tail: "all", Timestamps: true}
	if !opts.Since.IsZero() {
		logOpts.Since = opts.Since.Format(time.RFC3339)
	}
	if !opts.Until.IsZero() {
		logOpts.Until = opts.Until.Format(time.RFC3339)
	}
	ch, closer, err := s.modules.docker.Logs(ctx, id, logOpts)
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	c.NextFile(id, false, nil)
	n := 0
	for line := range ch {
		if ctx.Err() != nil {
			c.Incomplete()
			break
		}
		n++
		parsed := dockerLine(line)
		parsed.No = n
		c.Feed(parsed)
	}
	return c.Result(), nil
}

// searchJournal reads the window rather than a line count. A history search
// bounded by `-n` answers "is it in the last 300 records", which is not the
// question — so the time window is the bound here, and the absence of one is
// reported as an incomplete answer rather than silently capped.
func (s *Server) searchJournal(ctx context.Context, unit string, opts logsx.SearchOptions, boot bool) (*logsx.SearchResult, error) {
	c, err := logsx.NewCollector(opts)
	if err != nil {
		return nil, err
	}
	jopts := procs.JournalOptions{
		Unit:        unit,
		Boot:        boot,
		Since:       journalTimeSpec(opts.Since),
		Until:       journalTimeSpec(opts.Until),
		MaxPriority: maxJournalPriority(opts.Filter.Levels),
	}
	if jopts.Since == "" && !boot {
		// journalctl with no bound at either end walks the entire persistent
		// journal, which on a long-lived host is gigabytes. A default window
		// keeps the unbounded case answerable, and the result says it was
		// bounded so nobody reads an empty answer as "it never happened". A
		// boot is its own bound, so it needs no second one.
		jopts.Since = "2 days ago"
	}
	c.NextFile("journal", false, nil)
	n := 0
	if _, err := s.streamJournalInto(ctx, jopts, func(e procs.JournalEntry) {
		n++
		line := journalLine(e)
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

// journalLine maps a journal record onto the viewer's one line shape. The
// priority becomes a level so the journal's numbers and a text log's words end
// up as one vocabulary — the same filter chips work on both, which is the
// whole promise of a unified viewer.
func journalLine(e procs.JournalEntry) logsx.Line {
	source := e.Syslog
	if source == "" {
		source = strings.TrimSuffix(e.Unit, ".service")
	}
	if e.PID != "" && source != "" {
		source += "[" + e.PID + "]"
	}
	ts := e.Timestamp
	line := logsx.Line{
		Text:   e.Message,
		Level:  logsx.LevelFromPriority(e.Priority),
		Source: source,
	}
	if !ts.IsZero() {
		utc := ts.UTC()
		line.Timestamp = &utc
	}
	return line
}

// dockerLine strips the RFC3339 prefix Docker adds when timestamps are asked
// for, and puts it in the field the viewer renders. Leaving it in the text
// would draw the timestamp twice on every line, and the built-in parser cannot
// read it: Docker emits nanoseconds, which is longer than any layout the file
// parser knows.
func dockerLine(l dockerx.LogLine) logsx.Line {
	text := l.Text
	line := logsx.Line{Stream: l.Stream, Source: l.Service}
	if i := strings.IndexByte(text, ' '); i > 0 {
		if ts, err := time.Parse(time.RFC3339Nano, text[:i]); err == nil {
			utc := ts.UTC()
			line.Timestamp = &utc
			text = text[i+1:]
		}
	}
	line.Text = text
	parsed := logsx.ParseLine(text, l.Service)
	line.Level = parsed.Level
	if line.Timestamp == nil {
		line.Timestamp = parsed.Timestamp
	}
	// Docker's own stderr is a stronger signal than a keyword search over the
	// text, but only where the text said nothing: a container logging "INFO
	// listening" to stderr, which many do, must not be painted as an error.
	if line.Level == "" && l.Stream == "stderr" {
		line.Level = "error"
	}
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
	target, err := parseLogTarget(raw)
	if err != nil {
		return err
	}
	opts := logSearchOptions(q)
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
	if _, err := logsx.NewFilter(opts.Filter); err != nil {
		return httpx.BadRequest("%v", err)
	}

	name := strings.NewReplacer("/", "-", ":", "-", " ", "-").Replace(strings.TrimPrefix(target.label, "/"))
	if name == "" {
		name = "logs"
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q",
		name+"-"+time.Now().UTC().Format("20060102-150405")+".log"))

	ctx, cancel := timeoutCtx(r, 5*time.Minute)
	defer cancel()

	targets := []logsx.SearchTarget{{Path: target.path}}
	switch target.kind {
	case logsx.KindPM2:
		found, err := s.pm2Targets(ctx, target.id)
		if err != nil {
			return mapProcsError(err)
		}
		targets = found
	case logsx.KindDocker, logsx.KindJournal:
		// Neither is a file, so the export is the search result written out
		// rather than a byte range of something on disk.
		opts.Limit = 20000
		var res *logsx.SearchResult
		var err error
		if target.kind == logsx.KindDocker {
			res, err = s.searchContainer(ctx, target.id, opts)
		} else {
			res, err = s.searchJournal(ctx, target.id, opts, q.Get("boot") == "true")
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
	target, err := parseLogTarget(raw)
	if err != nil {
		return err
	}
	spec := logFilterFrom(q)
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

	switch target.kind {
	case logsx.KindDocker:
		if err := s.followContainer(ctx, target.id, lines, filter, out); err != nil {
			conn.SendError(err.Error())
			return nil
		}
	case logsx.KindPM2:
		if err := s.followPM2(ctx, target.id, lines, filter, out); err != nil {
			conn.SendError(err.Error())
			return nil
		}
	case logsx.KindJournal:
		meta.Label = target.label
		if err := s.followJournal(ctx, target.id, lines, spec, filter, q.Get("boot") == "true", out); err != nil {
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
			l.Stream = stream
			if l.Level == "" && stream == "stderr" {
				l.Level = "error"
			}
			return l
		})
	}
	if started == 0 {
		return fmt.Errorf("pm2 process %s has no readable log files", name)
	}
	return nil
}

func (s *Server) followContainer(ctx context.Context, id string, n int, f *logsx.Filter, out chan<- logsx.Line) error {
	ch, closer, err := s.modules.docker.Logs(ctx, id, dockerx.LogOptions{
		Tail:       strconv.Itoa(n),
		Timestamps: true,
		Follow:     true,
	})
	if err != nil {
		return err
	}
	go func() {
		defer closer.Close()
		for line := range ch {
			parsed := dockerLine(line)
			if !f.Match(parsed) {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case out <- parsed:
			}
		}
		close(out)
	}()
	return nil
}

func (s *Server) followJournal(ctx context.Context, unit string, n int, spec logsx.Filter, f *logsx.Filter, boot bool, out chan<- logsx.Line) error {
	opts := procs.JournalOptions{
		Unit:        unit,
		Lines:       n,
		Follow:      true,
		Boot:        boot,
		MaxPriority: maxJournalPriority(spec.Levels),
	}
	// A text filter cannot be pushed into journalctl portably — `-g` needs a
	// build with PCRE2 and a version nobody can assume — so the window is
	// widened instead and the exact test happens here. Without that, "the last
	// 400 records, of which two mention this container" is an empty page in
	// front of a journal that has the answer.
	if spec.Query != "" || spec.Exclude != "" {
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
			if !ok {
				continue
			}
			line := journalLine(e)
			if !f.Match(line) {
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
	target, err := parseLogTarget(defaultStr(r.URL.Query().Get("source"), r.URL.Query().Get("path")))
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
