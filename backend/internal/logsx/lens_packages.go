package logsx

import (
	"strconv"
	"strings"
	"time"
)

// The packages lens reads what the package managers record about changing
// the host: apt's history.log and term.log, dpkg.log, unattended-upgrades'
// log and dnf's. It answers "what changed on this server, when, and who asked
// for it" — the question behind every "it worked yesterday".
//
// history.log is written in blocks, and the block's first lines say when and
// who: Start-Date, Commandline, Requested-By. A reader only moves forward, so
// those values are carried onto the Install and Upgrade lines below them
// rather than those lines being folded under the Start-Date; each line stays
// a head of its own, filterable by package, and filed under the time the
// transaction started. term.log is carried the same way from its "Log
// started" line.
//
// None of these formats has a severity of its own, so a recognised line is
// given info explicitly: left to the word scan, "Install: liberror-perl" is an
// error.

func init() {
	register(&Lens{
		ID: "packages",
		Events: []string{"transaction_start", "command", "install", "upgrade", "remove", "purge", "transaction_end",
			"configure", "unattended_run", "unattended_done", "error"},
		// duration_ms is a transaction's length, End-Date less Start-Date.
		Attrs: []string{"package", "version", "old_version", "packages", "command", "user", "duration_ms"},
		New:   func() Reader { return &packagesReader{} },
	})
}

// packagesListCap bounds the package list a transaction line carries: a
// desktop stack pulled in as dependencies names a hundred packages, and the
// ones asked for come first. It keeps the list, with its "+N more", inside
// the 256 bytes a field predicate may hold, so "only this" works on it.
const packagesListCap = 200

type packagesReader struct {
	// start is the transaction's time — history.log's Start-Date, term.log's
	// "Log started" — which every line of the block is filed under.
	start   *time.Time
	command string
	user    string
}

func (r *packagesReader) Read(l *Line) {
	text := l.Text
	if text == "" {
		return
	}
	if c := text[0]; c >= '0' && c <= '9' {
		r.stamped(l, text)
		return
	}
	if key, value, ok := packagesField(text); ok && r.history(l, key, value) {
		return
	}
	r.term(l, text)
}

// packagesField splits a history.log line, "Key: value", whose key is one
// capitalised word or a hyphenated pair.
func packagesField(text string) (string, string, bool) {
	colon := strings.IndexByte(text, ':')
	if colon < 4 || colon > 13 || text[0] < 'A' || text[0] > 'Z' {
		return "", "", false
	}
	for i := 1; i < colon; i++ {
		if c := text[i]; !(c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return "", "", false
		}
	}
	return text[:colon], strings.TrimPrefix(text[colon+1:], " "), true
}

func (r *packagesReader) history(l *Line, key, value string) bool {
	event := ""
	switch key {
	case "Start-Date":
		t, _, ok := appParseStamp(value)
		if !ok {
			return false
		}
		r.start, r.command, r.user = &t, "", ""
		r.stamp(l)
		l.SetLevel("info")
		l.Event = "transaction_start"
		return true
	case "End-Date":
		t, _, ok := appParseStamp(value)
		if !ok {
			return false
		}
		l.Timestamp = &t
		l.SetLevel("info")
		l.Event = "transaction_end"
		r.carry(l)
		if r.start != nil {
			l.SetAttrNumber("duration_ms", float64(t.Sub(*r.start).Milliseconds()))
		}
		r.start = nil
		return true
	case "Commandline":
		r.command = strings.Clone(appCap(value, 256))
		event = "command"
	case "Requested-By":
		// "ubuntu (1000)": the account, then its uid.
		r.user, _, _ = strings.Cut(value, " (")
		r.user = strings.Clone(r.user)
	case "Install", "Reinstall":
		event = "install"
	case "Upgrade", "Downgrade":
		// A downgrade changes the installed version as an upgrade does; the
		// two versions say which way.
		event = "upgrade"
	case "Remove":
		event = "remove"
	case "Purge":
		event = "purge"
	case "Error":
		l.SetLevel("error")
		l.Event = "error"
		r.carry(l)
		r.stamp(l)
		return true
	default:
		return false
	}
	l.SetLevel("info")
	l.Event = event
	switch event {
	case "install", "upgrade", "remove", "purge":
		packagesListAttrs(l, packagesList(value, event == "upgrade"))
	}
	r.carry(l)
	r.stamp(l)
	return true
}

// carry puts the transaction's command and requester on a line of it.
func (r *packagesReader) carry(l *Line) {
	l.SetAttr("command", r.command)
	l.SetAttr("user", r.user)
}

// stamp files a line of the block under the transaction's time. Each line
// gets its own copy: a stamp is the line's to be replaced by whoever reads it
// next.
func (r *packagesReader) stamp(l *Line) {
	if r.start != nil {
		t := *r.start
		l.Timestamp = &t
	}
}

type packagesEntry struct {
	name, version, old string
	automatic          bool
}

// packagesList reads history.log's package list:
// "cloc:amd64 (2.04-1), libmoo-perl:amd64 (2.005005-1, automatic)", and for
// an upgrade "(old, new)". The architecture is dropped: the package sheet
// and dpkg.log name a package without it.
func packagesList(value string, upgrade bool) []packagesEntry {
	var out []packagesEntry
	for value != "" {
		open := strings.Index(value, " (")
		if open < 0 {
			break
		}
		end := strings.IndexByte(value[open:], ')')
		if end < 0 {
			break
		}
		name, inner := value[:open], value[open+2:open+end]
		value = strings.TrimPrefix(value[open+end+1:], ", ")
		if colon := strings.IndexByte(name, ':'); colon > 0 {
			name = name[:colon]
		}
		entry := packagesEntry{name: name}
		parts := strings.Split(inner, ", ")
		if n := len(parts); n > 1 && parts[n-1] == "automatic" {
			entry.automatic, parts = true, parts[:n-1]
		}
		switch {
		case upgrade && len(parts) >= 2:
			entry.old, entry.version = parts[0], parts[1]
		case len(parts) >= 1:
			entry.version = parts[0]
		}
		out = append(out, entry)
	}
	return out
}

// packagesListAttrs records a transaction line's packages: how many, and
// which — the ones asked for before the ones pulled in, so "apt install cloc"
// reads as cloc rather than as its first Perl dependency. One package also
// carries its versions.
func packagesListAttrs(l *Line, entries []packagesEntry) {
	if len(entries) == 0 {
		return
	}
	l.SetAttrNumber("packages", float64(len(entries)))
	if len(entries) == 1 {
		l.SetAttr("package", entries[0].name)
		l.SetAttr("version", entries[0].version)
		l.SetAttr("old_version", entries[0].old)
		return
	}
	var list strings.Builder
	written := 0
	for _, pass := range []bool{false, true} {
		for _, entry := range entries {
			if entry.automatic != pass {
				continue
			}
			if list.Len()+len(entry.name)+2 > packagesListCap {
				list.WriteString(" +" + strconv.Itoa(len(entries)-written) + " more")
				l.SetAttr("package", list.String())
				return
			}
			if list.Len() > 0 {
				list.WriteString(", ")
			}
			list.WriteString(entry.name)
			written++
		}
	}
	l.SetAttr("package", list.String())
}

// term reads apt's term.log — dpkg's own output during a transaction — and
// unattended-upgrades-dpkg.log, which is the same thing.
func (r *packagesReader) term(l *Line, text string) {
	event, rest := "", ""
	switch {
	case strings.HasPrefix(text, "Log started: "):
		if t, _, ok := appParseStamp(text[len("Log started: "):]); ok {
			r.start, r.command, r.user = &t, "", ""
			r.stamp(l)
			l.SetLevel("info")
			l.Event = "transaction_start"
		}
		return
	case strings.HasPrefix(text, "Log ended: "):
		if t, _, ok := appParseStamp(text[len("Log ended: "):]); ok {
			l.Timestamp = &t
			l.SetLevel("info")
			l.Event = "transaction_end"
			if r.start != nil {
				l.SetAttrNumber("duration_ms", float64(t.Sub(*r.start).Milliseconds()))
			}
			r.start = nil
		}
		return
	case strings.HasPrefix(text, "Unpacking "):
		event, rest = "install", text[len("Unpacking "):]
	case strings.HasPrefix(text, "Setting up "):
		event, rest = "configure", text[len("Setting up "):]
	case strings.HasPrefix(text, "Removing "):
		event, rest = "remove", text[len("Removing "):]
	case strings.HasPrefix(text, "Purging configuration files for "):
		event, rest = "purge", text[len("Purging configuration files for "):]
	case strings.HasPrefix(text, "dpkg: error processing "):
		// "dpkg: error processing package nginx (--configure):"
		l.SetLevel("error")
		l.Event = "error"
		fields := strings.Fields(text[len("dpkg: error processing "):])
		if len(fields) >= 2 {
			l.SetAttr("package", packagesName(fields[1]))
		}
		r.stamp(l)
		return
	case strings.HasPrefix(text, "E: "):
		l.SetLevel("error")
		l.Event = "error"
		r.stamp(l)
		return
	default:
		r.stamp(l)
		return
	}
	// "docker-ce (5:29.8.0-1~ubuntu.24.04~noble) over (5:29.7.2-1~ubuntu.24.04~noble) ..."
	name, after, ok := strings.Cut(rest, " (")
	version, after, ok2 := strings.Cut(after, ")")
	if !ok || !ok2 || !packagesValidName(name) {
		r.stamp(l)
		return
	}
	old := ""
	if strings.HasPrefix(after, " over (") {
		old, _, _ = strings.Cut(after[len(" over ("):], ")")
		if event == "install" && old != version {
			event = "upgrade"
		}
	}
	l.SetLevel("info")
	l.Event = event
	l.SetAttr("package", packagesName(name))
	l.SetAttr("version", version)
	if event == "upgrade" {
		l.SetAttr("old_version", old)
	}
	r.stamp(l)
}

// packagesName drops the architecture a package is qualified with.
func packagesName(name string) string {
	if colon := strings.IndexByte(name, ':'); colon > 0 {
		return name[:colon]
	}
	return name
}

// packagesValidName is a Debian package name, optionally qualified: it keeps
// maintainer-script prose ("Removing obsolete conffile …") from reading as a
// package.
func packagesValidName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c == '.' || c == '+' || c == '-' || c == ':' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// packagesLevels are the level tokens unattended-upgrades and dnf write after
// the time; dnf's DDEBUG and SUBDEBUG are its own finer debug levels.
var packagesLevels = map[string]string{
	"CRITICAL": "critical", "ERROR": "error", "WARNING": "warn", "INFO": "info",
	"DEBUG": "debug", "DDEBUG": "debug", "SUBDEBUG": "debug", "TRACE": "debug",
}

// stamped reads the formats that start with a time: dpkg.log,
// unattended-upgrades.log and dnf's logs.
func (r *packagesReader) stamped(l *Line, text string) {
	t, n, ok := appParseStamp(text)
	if !ok {
		return
	}
	appSetStamp(l, t)
	if n >= len(text) || text[n] != ' ' {
		return
	}
	word, rest, _ := strings.Cut(text[n+1:], " ")
	switch word {
	case "install", "upgrade", "remove", "purge", "configure", "status", "trigproc", "startup", "conffile":
		l.SetLevel("info")
		r.dpkg(l, word, rest)
		return
	}
	level, ok := packagesLevels[word]
	if !ok {
		return
	}
	l.SetLevel(level)
	switch {
	case strings.HasPrefix(rest, "Starting unattended upgrades script"):
		l.Event = "unattended_run"
	case strings.HasPrefix(rest, "No packages found that can be upgraded unattended"), strings.HasPrefix(rest, "All upgrades installed"):
		l.Event = "unattended_done"
	case strings.HasPrefix(rest, "Packages that will be upgraded: "), strings.HasPrefix(rest, "Packages that are upgraded: "),
		strings.HasPrefix(rest, "Packages that were upgraded: "):
		_, list, _ := strings.Cut(rest, ": ")
		names := strings.Fields(list)
		entries := make([]packagesEntry, len(names))
		for i, name := range names {
			entries[i].name = name
		}
		l.Event = "upgrade"
		packagesListAttrs(l, entries)
	case strings.HasPrefix(rest, "Command: "):
		// dnf.log's first line of a run: what was typed.
		r.command = strings.Clone(appCap(strings.TrimSpace(rest[len("Command: "):]), 256))
		l.Event = "command"
		l.SetAttr("command", r.command)
	case word == "SUBDEBUG":
		r.rpm(l, rest)
	case level == "error" || level == "critical":
		l.Event = "error"
		l.SetAttr("command", r.command)
	}
}

// dpkg reads one dpkg.log action: "install libfoo:all <none> 2.15-1",
// "upgrade docker-ce:amd64 5:29.7.2-1 5:29.8.0-1", "remove libfoo:all 2.15-1
// <none>". status, trigproc, startup and conffile lines are the steps of
// those actions — five of them per package — and are left without an event
// or attrs so a package's history counts each change once.
func (r *packagesReader) dpkg(l *Line, action, rest string) {
	fields := strings.Fields(rest)
	if len(fields) < 3 {
		return
	}
	switch action {
	case "install":
		l.Event = "install"
		l.SetAttr("version", fields[2])
	case "upgrade":
		l.Event = "upgrade"
		l.SetAttr("old_version", fields[1])
		l.SetAttr("version", fields[2])
	case "remove", "purge", "configure":
		l.Event = action
		l.SetAttr("version", fields[1])
	default:
		return
	}
	l.SetAttr("package", packagesName(fields[0]))
}

// rpm reads dnf.rpm.log's per-package line, "Installed: nginx-1:1.22.1-1.fc37.x86_64".
// The old half of an upgrade ("Upgraded:", "Obsoleted:") is not a change of
// its own.
func (r *packagesReader) rpm(l *Line, rest string) {
	verb, nevra, ok := strings.Cut(rest, ": ")
	if !ok {
		return
	}
	switch verb {
	case "Installed", "Install", "Reinstall", "Reinstalled":
		l.Event = "install"
	case "Upgrade", "Downgrade":
		l.Event = "upgrade"
	case "Erase", "Erased", "Removed":
		l.Event = "remove"
	default:
		return
	}
	name, version := packagesNEVRA(strings.TrimSpace(nevra))
	l.SetAttr("package", name)
	l.SetAttr("version", version)
}

// packagesNEVRA splits "openssl-libs-1:3.0.8-1.fc37.x86_64" into the name and
// the epoch:version-release, dropping the architecture.
func packagesNEVRA(nevra string) (string, string) {
	if dot := strings.LastIndexByte(nevra, '.'); dot > 0 {
		nevra = nevra[:dot]
	}
	release := strings.LastIndexByte(nevra, '-')
	if release <= 0 {
		return nevra, ""
	}
	version := strings.LastIndexByte(nevra[:release], '-')
	if version <= 0 {
		return nevra, ""
	}
	return nevra[:version], nevra[version+1:]
}
