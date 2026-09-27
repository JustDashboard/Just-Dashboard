package logsx

import (
	"bufio"
	"context"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type SourceKind string

const (
	KindSystem  SourceKind = "system"
	KindNginx   SourceKind = "nginx"
	KindApp     SourceKind = "app"
	KindPM2     SourceKind = "pm2"
	KindDocker  SourceKind = "docker"
	KindJournal SourceKind = "journal"
	// KindStack is every container of one compose project, merged by time.
	KindStack SourceKind = "stack"
	// KindJournalID is the journal narrowed by syslog identifier rather than
	// by unit. sshd, sudo, su and logind write under no unit of their own that
	// holds all of them, and on a host with no auth.log that is the only way to
	// read "who logged in" as one source.
	KindJournalID SourceKind = "journal-id"
	// KindKernel is the kernel ring as the journal keeps it — the firewall's
	// drops and the OOM killer on a host with no kern.log.
	KindKernel SourceKind = "kernel"
)

// Source is one thing the unified viewer can open. Docker containers and the
// journal are addressed by ID rather than by path, since neither is a file
// this process reads directly.
type Source struct {
	ID       string     `json:"id"`
	Label    string     `json:"label"`
	Kind     SourceKind `json:"kind"`
	Path     string     `json:"path,omitempty"`
	Size     int64      `json:"size,omitempty"`
	Modified *time.Time `json:"modified,omitempty"`
	Rotated  bool       `json:"rotated"`
	// Archives counts the rotated generations sitting next to a live file, and
	// ArchiveBytes their total size. A source that says "+ 7 archives, 2.1 GB"
	// is telling the operator both that history is searchable past last
	// night's rotation and where their disk went.
	Archives     int   `json:"archives,omitempty"`
	ArchiveBytes int64 `json:"archiveBytes,omitempty"`
	// Detail is a one-line description of what this source actually is, for
	// the reader who has never met auth.log. The point of a unified viewer is
	// lost if choosing a source is still a guess.
	Detail string `json:"detail,omitempty"`
	// Status carries a live source's state — a container that is not running
	// has logs worth reading and no new lines coming, and saying so is the
	// difference between "stopped" and "the page is broken".
	Status string `json:"status,omitempty"`
	// Lens is the lens the source reads through when none is forced, so the
	// page can offer the right quick views before the first line arrives.
	Lens string `json:"lens,omitempty"`
	// Images are a stack's container images, which is what a stack is to the
	// reader choosing one: "postgres, redis, the app".
	Images []string `json:"images,omitempty"`
}

// wellKnown are the files an operator expects to find without hunting. Any
// that do not exist on this host are simply skipped. Each carries the sentence
// somebody needs to choose it, because "auth" and "secure" and "messages" are
// only obvious to a reader who already knows which one holds sshd's refusals.
var wellKnown = []struct {
	path   string
	label  string
	kind   SourceKind
	detail string
}{
	{"/var/log/syslog", "syslog", KindSystem, "Everything the system logger was handed — the first place to look"},
	{"/var/log/messages", "messages", KindSystem, "The RPM world's syslog: general system messages"},
	{"/var/log/auth.log", "auth", KindSystem, "Logins, sudo and sshd — who got in and who was refused"},
	{"/var/log/secure", "secure", KindSystem, "The RPM world's auth.log: logins, sudo and sshd"},
	{"/var/log/kern.log", "kernel", KindSystem, "Kernel messages: hardware, filesystems, the OOM killer"},
	{"/var/log/dpkg.log", "dpkg", KindSystem, "Every package installed, upgraded or removed, with timestamps"},
	{"/var/log/apt/history.log", "apt history", KindSystem, "Package transactions as apt recorded them, with the command that ran"},
	{"/var/log/ufw.log", "ufw", KindSystem, "Packets the firewall blocked or allowed, if logging is on"},
	{"/var/log/fail2ban.log", "fail2ban", KindSystem, "Bans, unbans and the jails that issued them"},
	{"/var/log/cloud-init.log", "cloud-init", KindSystem, "What the provider's first-boot provisioning did"},
	{"/var/log/unattended-upgrades/unattended-upgrades.log", "unattended-upgrades", KindSystem, "Automatic security updates: what was applied overnight"},
	{"/var/log/apt/term.log", "apt term", KindSystem, "What apt printed while it ran — the error behind a failed upgrade"},
	{"/var/log/dnf.log", "dnf", KindSystem, "Package transactions as dnf recorded them"},
	{"/var/log/dnf.rpm.log", "dnf rpm", KindSystem, "Every package dnf installed, upgraded or removed"},
	{"/var/log/cron", "cron", KindSystem, "The RPM world's cron log: every scheduled job that ran"},
	{"/var/log/letsencrypt/letsencrypt.log", "certbot", KindSystem, "Certificate renewals: which domains renewed, and why one did not"},
	{"/var/log/nginx/access.log", "nginx access", KindNginx, "Every request nginx served, with status and timing"},
	{"/var/log/nginx/error.log", "nginx error", KindNginx, "Upstream failures, certificate problems, refused requests"},
	{"/var/log/caddy/access.log", "caddy access", KindNginx, "Every request Caddy served"},
	{"/var/log/mysql/error.log", "mysql error", KindApp, "MySQL/MariaDB startup, crashes and replication errors"},
	{"/var/log/redis/redis-server.log", "redis", KindApp, "Redis startup, persistence and eviction"},
}

// Discover enumerates readable log files. It walks the configured roots so
// application logs dropped into /var/log/myapp are found without configuration.
func (s *Service) Discover(ctx context.Context) ([]Source, error) {
	seen := map[string]bool{}
	out := []Source{}

	add := func(p, label string, kind SourceKind, detail string) {
		if p == "" || seen[p] {
			return
		}
		// A well-known path is only worth offering if this service may open
		// it. JD_LOG_ROOTS defaults to /var/log so the list below is normally
		// reachable in full — but an install that narrowed the roots used to
		// get a rail full of files that refused to open, which is the same
		// lie as a control that reports success and does nothing.
		if err := s.Allow(p); err != nil {
			return
		}
		src, ok := describeFile(p, label, kind, detail)
		if !ok {
			return
		}
		seen[p] = true
		out = append(out, src)
	}

	for _, wk := range wellKnown {
		add(wk.path, wk.label, wk.kind, wk.detail)
	}
	for _, root := range s.roots {
		filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || ctx.Err() != nil {
				return nil
			}
			if d.IsDir() {
				if strings.Count(strings.TrimPrefix(p, root), string(os.PathSeparator)) > 3 {
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			// Rotated and compressed archives are noise in a live viewer: they
			// hang off their live file as an archive count instead, and the
			// history search reads them from there.
			if !strings.HasSuffix(name, ".log") && !strings.HasSuffix(name, ".err") && !strings.HasSuffix(name, ".out") {
				return nil
			}
			if Compressed(p) {
				return nil
			}
			add(p, strings.TrimPrefix(p, root+string(os.PathSeparator)), fileKind(p), "")
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Label < out[j].Label
	})
	return out, nil
}

// Describe answers one file source the way Discover would list it, for the
// page that embeds a single log and needs its size, archives and lens without
// walking every root to find them. ok is false when the file is not there.
func (s *Service) Describe(p string) (Source, bool, error) {
	p = filepath.Clean(p)
	if err := s.Allow(p); err != nil {
		return Source{}, false, err
	}
	for _, wk := range wellKnown {
		if wk.path == p {
			src, ok := describeFile(p, wk.label, wk.kind, wk.detail)
			return src, ok, nil
		}
	}
	label := filepath.Base(p)
	for _, root := range s.roots {
		if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
			label = rel
			break
		}
	}
	src, ok := describeFile(p, label, fileKind(p), "")
	return src, ok, nil
}

// fileKind is the group a discovered file is listed under.
func fileKind(p string) SourceKind {
	if strings.Contains(p, "nginx") || strings.Contains(p, "caddy") {
		return KindNginx
	}
	return KindApp
}

func describeFile(p, label string, kind SourceKind, detail string) (Source, bool) {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return Source{}, false
	}
	mod := st.ModTime().UTC()
	src := Source{
		ID: p, Label: label, Kind: kind, Path: p, Detail: detail,
		Size: st.Size(), Modified: &mod,
		Lens: DetectLens(LensTarget{Kind: kind, Path: p}),
	}
	for _, a := range Archives(p) {
		src.Archives++
		if ast, err := os.Stat(a); err == nil {
			src.ArchiveBytes += ast.Size()
		}
	}
	src.Rotated = src.Archives > 0
	return src, true
}

// RotateRule is one logrotate stanza, paired with what is currently on disk so
// the UI can answer "is rotation actually happening for this file".
type RotateRule struct {
	ConfigFile string   `json:"configFile"`
	Patterns   []string `json:"patterns"`
	Frequency  string   `json:"frequency,omitempty"`
	Rotate     string   `json:"rotate,omitempty"`
	Compress   bool     `json:"compress"`
	Size       string   `json:"size,omitempty"`
	MaxSize    string   `json:"maxSize,omitempty"`
	Missing    bool     `json:"missingOk"`
	Options    []string `json:"options"`
}

type RotateStatus struct {
	Available bool         `json:"available"`
	Rules     []RotateRule `json:"rules"`
	StateFile string       `json:"stateFile,omitempty"`
	LastRun   *time.Time   `json:"lastRun,omitempty"`
}

// Retention is the verdict for one file, in the spirit of netsec.Assess: the
// rules are on disk already and nobody reads them, so the page answers the
// question they were consulted for. A log with no rule matching it is the one
// that fills the disk at 3am, and it is invisible in a rule list because it is
// the entry that is not there.
type Retention struct {
	Managed   bool        `json:"managed"`
	Rule      *RotateRule `json:"rule,omitempty"`
	Pattern   string      `json:"pattern,omitempty"`
	Summary   string      `json:"summary"`
	Level     string      `json:"level"` // "ok" | "warn" | "unknown"
	LastRun   *time.Time  `json:"lastRun,omitempty"`
	Available bool        `json:"available"`
}

// MatchRetention finds the logrotate rule governing a path. logrotate takes
// glob patterns, so this matches the way it does rather than by prefix, and a
// directory-wide pattern (/var/log/nginx/*.log) is expected to cover a file
// inside it.
func MatchRetention(st *RotateStatus, filePath string, size int64) Retention {
	r := Retention{Available: st.Available, LastRun: st.LastRun}
	if !st.Available {
		r.Level = "unknown"
		r.Summary = "logrotate is not installed on this host, so nothing is trimming this file."
		return r
	}
	for i := range st.Rules {
		for _, pattern := range st.Rules[i].Patterns {
			if ok, _ := path.Match(pattern, filePath); ok {
				r.Managed, r.Rule, r.Pattern = true, &st.Rules[i], pattern
				break
			}
		}
		if r.Managed {
			break
		}
	}
	if !r.Managed {
		r.Level = "warn"
		r.Summary = "No logrotate rule matches this file. It grows until the disk is full."
		if size > 0 && size < 16<<20 {
			// Small and unmanaged is a note, not an alarm — plenty of
			// application logs are written once at boot.
			r.Level = "unknown"
			r.Summary = "No logrotate rule matches this file, so nothing trims it. It is small so far."
		}
		return r
	}
	parts := []string{}
	if r.Rule.Frequency != "" {
		parts = append(parts, "rotated "+r.Rule.Frequency)
	} else if r.Rule.Size != "" {
		parts = append(parts, "rotated past "+r.Rule.Size)
	} else {
		parts = append(parts, "rotated")
	}
	if r.Rule.Rotate != "" {
		parts = append(parts, r.Rule.Rotate+" kept")
	}
	if r.Rule.Compress {
		parts = append(parts, "compressed")
	}
	r.Level = "ok"
	r.Summary = strings.ToUpper(parts[0][:1]) + parts[0][1:] + ", " + strings.Join(parts[1:], ", ") + "."
	if len(parts) == 1 {
		r.Summary = strings.ToUpper(parts[0][:1]) + parts[0][1:] + "."
	}
	// A rule that exists and has not run in a fortnight is the failure this
	// panel is really for: the config looks right and the disk fills anyway.
	if st.LastRun != nil && time.Since(*st.LastRun) > 14*24*time.Hour {
		r.Level = "warn"
		r.Summary += " logrotate itself has not run since " + st.LastRun.Format("2 Jan") + " — the timer has stopped."
	}
	return r
}

// LogrotateStatus parses /etc/logrotate.conf and its drop-ins. The parser is
// deliberately shallow — enough to report frequency, retention and compression
// per pattern, which is what an operator checks — rather than a full
// reimplementation of logrotate's grammar.
func LogrotateStatus(ctx context.Context) (*RotateStatus, error) {
	st := &RotateStatus{Rules: []RotateRule{}}
	main := "/etc/logrotate.conf"
	if _, err := os.Stat(main); err != nil {
		return st, nil
	}
	st.Available = true

	files := []string{main}
	if entries, err := os.ReadDir("/etc/logrotate.d"); err == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				names = append(names, filepath.Join("/etc/logrotate.d", e.Name()))
			}
		}
		sort.Strings(names)
		files = append(files, names...)
	}
	for _, f := range files {
		if ctx.Err() != nil {
			break
		}
		st.Rules = append(st.Rules, parseLogrotateFile(f)...)
	}
	for _, state := range []string{"/var/lib/logrotate/status", "/var/lib/logrotate.status"} {
		if fi, err := os.Stat(state); err == nil {
			st.StateFile = state
			mod := fi.ModTime().UTC()
			st.LastRun = &mod
			break
		}
	}
	return st, nil
}

// looksLikePattern separates the paths a stanza governs from the global
// directives that sit at the same indentation in logrotate.conf. A pattern is
// a path or a glob; a directive is a bare word.
func looksLikePattern(field string) bool {
	if field == "" {
		return false
	}
	switch field[0] {
	case '/', '"', '\'', '*', '~':
		return true
	}
	return false
}

func trimQuotes(field string) string {
	if len(field) >= 2 && (field[0] == '"' || field[0] == '\'') && field[len(field)-1] == field[0] {
		return field[1 : len(field)-1]
	}
	return field
}

func parseLogrotateFile(p string) []RotateRule {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()

	rules := []RotateRule{}
	var current *RotateRule
	// A stanza's paths may be listed one per line before the brace, which is
	// exactly how Debian ships rsyslog's: six paths and then `{`. Reading only
	// the brace line meant syslog, auth.log, kern.log and three others were
	// each reported as governed by no rule at all — a false "this file grows
	// until the disk is full" on the six logs that matter most.
	var pending []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if current == nil {
			if strings.HasSuffix(line, "{") {
				for _, field := range strings.Fields(strings.TrimSuffix(line, "{")) {
					pending = append(pending, trimQuotes(field))
				}
				current = &RotateRule{ConfigFile: p, Patterns: pending, Options: []string{}}
				pending = nil
				continue
			}
			fields := strings.Fields(line)
			if looksLikePattern(fields[0]) {
				for _, field := range fields {
					pending = append(pending, trimQuotes(field))
				}
			} else {
				// A global directive (weekly, su root adm, include …). Whatever
				// paths came before it were not this stanza's.
				pending = nil
			}
			continue
		}
		if line == "}" {
			rules = append(rules, *current)
			current = nil
			continue
		}
		fields := strings.Fields(line)
		switch fields[0] {
		case "daily", "weekly", "monthly", "yearly", "hourly":
			current.Frequency = fields[0]
		case "rotate":
			if len(fields) > 1 {
				current.Rotate = fields[1]
			}
		case "compress":
			current.Compress = true
		case "size", "minsize":
			if len(fields) > 1 {
				current.Size = fields[1]
			}
		case "maxsize":
			if len(fields) > 1 {
				current.MaxSize = fields[1]
			}
		case "missingok":
			current.Missing = true
		default:
			current.Options = append(current.Options, line)
		}
	}
	return rules
}
