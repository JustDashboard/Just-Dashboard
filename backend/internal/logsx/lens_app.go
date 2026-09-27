package logsx

import (
	"encoding/json"
	"math/bits"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// The app lens reads what an application prints to its own stdout: a
// container's output, a PM2 process, a unit the dashboard has no better lens
// for. There is no one format — a Next.js server, a uvicorn worker and a Rails
// app share nothing — so it recognises the handful of shapes that carry a
// finding regardless of framework: a request line, an exception and the stack
// under it, the moment the server came up or went down, and the failures a
// deployment most often dies of (memory, a taken port, an unreachable or
// empty database, a missing variable). The failure patterns are the ones
// deploy/runtime_output_cause.go already proved against real start-up
// output; they are copied rather than imported so the log reader does not
// depend on the deployment engine.
//
// Everything else passes through untouched. Most of an application's output
// is its own prose, and a lens that guessed at it would put a wrong event on
// a line that had none.

func init() {
	register(&Lens{
		ID:     "app",
		Events: appEvents,
		Attrs:  appAttrs,
		New:    func() Reader { return &appReader{} },
	})
}

var (
	appEvents = []string{"request", "exception", "startup", "shutdown", "oom", "port_in_use", "db_unreachable",
		"schema_missing", "env_missing", "deprecation"}
	// port is the port a server said it listens on or found taken, and table
	// the one a missing-schema error names; both are in the shared vocabulary
	// and are what the startup and schema findings are about.
	appAttrs = []string{"method", "path", "status", "class", "duration_ms", "client", "component", "error", "port", "table"}
)

// appRecord is the kind of multi-line record the last head opened, which
// decides what may continue it: a JavaScript error's inspected properties, a
// Python traceback's body, a goroutine dump.
type appRecord uint8

const (
	appRecNone appRecord = iota
	appRecTrace
	appRecPyBody
	appRecPyTail
	appRecPyChain
	appRecPyChainBody
	appRecGo
	appRecRuby
	appRecPHP
)

// appErrorCap bounds the error attr: enough to tell two exceptions apart,
// short enough that a message embedding a payload stays one row.
const appErrorCap = 200

type appReader struct {
	rec       appRecord
	headLevel string
	seenHead  bool
	// msgLines counts the lines still allowed to continue a JavaScript error
	// whose head carried no message. Prisma prints its class, then the
	// message on the lines below, then the frames.
	msgLines int
	// environ is set when a Python traceback's body read os.environ, which is
	// what turns its closing KeyError into a missing variable.
	environ bool
	// readyLine and prevReady keep one start per occurrence: Puma prints a
	// Listening line per bound address, Werkzeug a Running line per interface.
	readyLine, prevReady bool
	// port is what a server printed before its ready line: Next.js's
	// "- Local:", Spring's "Tomcat started on port". It is carried onto the
	// line that says the server is up.
	port string
	// pending are Rails "Started" and Phoenix "GET /" lines waiting for the
	// "Completed" or "Sent" that states their status. lineID is the request
	// id the current line was tagged with, and phoenix says its prefix was
	// Phoenix's, where a bare "GET /" opens a request.
	pending appPendingRequests
	lineID  string
	phoenix bool
}

func (r *appReader) Read(l *Line) { r.read(l, l.Text) }

// read is Read over a body the caller chose: the pm2 lens passes the text
// after PM2's own timestamp.
func (r *appReader) read(l *Line, text string) {
	if r.seenHead && r.continues(text) {
		l.Cont = true
		if r.headLevel != "" {
			l.SetLevel(r.headLevel)
		}
		return
	}
	prev := r.rec
	r.rec, r.msgLines = appRecNone, 0
	event := r.head(l, text, prev)
	if event != "" {
		l.Event = event
	}
	r.prevReady, r.readyLine = r.readyLine, false
	r.seenHead = true
	r.headLevel = l.Level
}

// continues says whether a line belongs to the record the last head opened.
// Stack frames continue any head — "    at …" after "Failed: Error: boom" is
// that error's stack whatever the line above it looked like. Everything else
// continues only the kind of record that prints it, so an indented banner
// after a crash (Next.js 14 indents its "▲ Next.js") starts a new record
// rather than folding under the old exception.
func (r *appReader) continues(text string) bool {
	if appFrame(text) {
		if r.rec == appRecPyBody && strings.Contains(text, "environ") {
			r.environ = true
		}
		// The message of a class-only head ends where its stack begins.
		r.msgLines = 0
		return true
	}
	switch r.rec {
	case appRecTrace:
		if text == "" || appIndentedProp(text) || appClosing(text) ||
			strings.HasPrefix(text, "Read more: https://nextjs.org/docs/messages/") ||
			(strings.HasPrefix(text, "Node.js v") && len(text) > 9 && text[9] >= '0' && text[9] <= '9') {
			return true
		}
		if r.msgLines > 0 && !appIndented(text) {
			if errText, rec, _ := appExceptionHead(text, appTriggers(text)); errText != "" || rec != appRecNone {
				return false
			}
			r.msgLines--
			return true
		}
	case appRecPyBody:
		if text == "" || appIndented(text) {
			if strings.Contains(text, "environ") {
				r.environ = true
			}
			return true
		}
	case appRecPyTail:
		if text == "" || appIndented(text) {
			return true
		}
		if strings.HasPrefix(text, "During handling of the above exception") || strings.HasPrefix(text, "The above exception was the direct cause") {
			r.rec = appRecPyChain
			return true
		}
	case appRecPyChain:
		if text == "" {
			return true
		}
		if strings.HasPrefix(text, "Traceback (most recent call last)") {
			r.rec = appRecPyChainBody
			return true
		}
	case appRecPyChainBody:
		// A chained exception's own closing line folds under the first: the
		// record is one failure, grouped by the exception it started with.
		if text != "" && !appIndented(text) {
			r.rec = appRecPyTail
		}
		return true
	case appRecGo:
		return text == "" || appIndented(text) || strings.HasPrefix(text, "goroutine ") ||
			strings.HasPrefix(text, "created by ") || strings.HasPrefix(text, "[signal ") ||
			strings.HasPrefix(text, "exit status ") || strings.HasPrefix(text, "runtime stack:") || appGoFrame(text)
	case appRecRuby:
		// Rails logs each backtrace line through its logger, so the prefix
		// and the request tag come first and the frame after them.
		if len(text) > 3 && text[1] == ',' && text[2] == ' ' && text[3] == '[' {
			if m := appRailsPrefixRE.FindStringSubmatch(text); m != nil {
				text = r.railsTags(m[2])
			}
		} else if strings.HasPrefix(text, "[") {
			text = r.railsTags(text)
		}
		return strings.TrimSpace(text) == "" || appIndented(text) ||
			(strings.Contains(text, ".rb:") && (strings.Contains(text, ":in `") || strings.Contains(text, ":in '")))
	case appRecPHP:
		return appIndented(text) || strings.HasPrefix(text, "Stack trace:") || strings.HasPrefix(text, "PHP Stack trace:") ||
			strings.HasPrefix(text, "PHP  ") || (len(text) > 1 && text[0] == '#' && text[1] >= '0' && text[1] <= '9') ||
			strings.HasPrefix(text, "[stacktrace]") || strings.HasPrefix(text, "[previous exception]") || strings.HasPrefix(text, `"}`)
	}
	return false
}

// appFrame is a stack frame in any runtime's spelling: JavaScript, Java and
// .NET "at", Python's File line, Ruby's "from", the elided "... 12 more", and
// a Java cause.
func appFrame(text string) bool {
	if text == "" {
		return false
	}
	c := text[0]
	if c != ' ' && c != '\t' {
		return c == 'C' && strings.HasPrefix(text, "Caused by: ")
	}
	t := strings.TrimLeft(text, " \t")
	switch {
	case len(t) > 3 && strings.HasPrefix(t, "at ") && t[3] != ' ':
		return true
	case strings.HasPrefix(t, `File "`):
		return strings.Contains(t, `", line `)
	case strings.HasPrefix(t, "from "):
		return strings.Contains(t, ":in ")
	case strings.HasPrefix(t, "... "):
		return strings.HasSuffix(t, " more") || strings.Contains(t, "frames omitted")
	case strings.HasPrefix(t, "Caused by: "), strings.HasPrefix(t, "Suppressed: "):
		return true
	}
	return false
}

// appIndentedProp is a property Node prints after an error's stack when it
// inspects one: "  digest: '740006343'", "  code: 'P2002',", "  [cause]: …".
func appIndentedProp(text string) bool {
	if !appIndented(text) {
		return false
	}
	t := strings.TrimLeft(text, " \t")
	if strings.HasPrefix(t, "[cause]") || strings.HasPrefix(t, "[errors]") {
		return true
	}
	colon := strings.IndexByte(t, ':')
	if colon <= 0 || colon+1 >= len(t) || t[colon+1] != ' ' {
		return false
	}
	for i := 0; i < colon; i++ {
		c := t[i]
		if !(c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

func appClosing(text string) bool {
	switch strings.TrimSpace(text) {
	case "}", "},", "]", "],", "})", "}]":
		return true
	}
	return false
}

// appGoFrame is a goroutine dump's function line: "main.main()",
// "net/http.(*conn).serve(0xc0001a2000, {0x7f1c28, 0xc000182000})".
func appGoFrame(text string) bool {
	paren := strings.IndexByte(text, '(')
	return paren > 0 && strings.HasSuffix(text, ")") && strings.IndexByte(text[:paren], ' ') < 0
}

// head reads a line that starts a record and answers its event.
func (r *appReader) head(l *Line, text string, prev appRecord) string {
	if prev == appRecPyBody && text != "" && !appIndented(text) {
		if event, ok := r.pythonFinal(l, text); ok {
			return event
		}
	}
	if l.Fields != nil || l.Message != "" {
		if r.structuredRequest(l) {
			return "request"
		}
		return r.structured(l)
	}
	r.lineID, r.phoenix = "", false
	msg, tok := r.prefix(l, text)
	mask := appTriggers(msg)
	if event, ok := r.request(l, text, msg, tok, mask); ok {
		return event
	}
	return r.classify(l, msg, mask)
}

// pythonFinal reads the line a Python traceback ends with: the exception's
// class and message, which is the line that defines the record, so its event
// goes here rather than on the Traceback header above.
func (r *appReader) pythonFinal(l *Line, text string) (string, bool) {
	class, message, ok := appPythonException(text)
	if !ok {
		return "", false
	}
	errText := class
	if message != "" {
		errText = class + ": " + message
	}
	event := appProblem(text, appTriggers(text))
	switch {
	case event != "":
	case r.environ && class == "KeyError" && appEnvKeyError(message):
		event = "env_missing"
	default:
		event = "exception"
	}
	r.environ = false
	r.rec = appRecPyTail
	l.SetAttr("error", appCap(errText, appErrorCap))
	r.problemAttrs(l, event, text)
	appRaise(l)
	return event, true
}

// appPythonException splits "ValueError: bad" or "KeyboardInterrupt" into a
// dotted class name and its message.
func appPythonException(text string) (string, string, bool) {
	end := strings.IndexByte(text, ':')
	class, message := text, ""
	if end >= 0 {
		class, message = text[:end], strings.TrimSpace(text[end+1:])
	}
	if class == "" || strings.ContainsAny(class, " \t()[]'\"") {
		return "", "", false
	}
	upper := false
	for i := 0; i < len(class); i++ {
		c := class[i]
		if !(c == '_' || c == '.' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return "", "", false
		}
		upper = upper || (c >= 'A' && c <= 'Z')
	}
	return class, message, upper
}

func appEnvKeyError(message string) bool {
	name := strings.Trim(message, "'\"")
	if len(name) < 3 || len(name) == len(message) {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c == '_' || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// appRaise files a failure at error. A critical level the line stated —
// Node's "FATAL ERROR" — stays: it already says more.
func appRaise(l *Line) {
	if l.Level == "critical" {
		l.SetLevel("critical")
		return
	}
	l.SetLevel("error")
}

// classify names a line that is not a request: a specific failure first,
// then an exception head, then a deprecation, a stop or a start.
func (r *appReader) classify(l *Line, msg string, m appWords) string {
	errText, rec, header := appExceptionHead(msg, m)
	event := appProblem(msg, m)
	if event == "" && errText != "" {
		event = "exception"
	}
	if errText != "" {
		l.SetAttr("error", appCap(errText, appErrorCap))
	}
	if rec != appRecNone {
		r.rec = rec
		if rec == appRecTrace && strings.HasSuffix(strings.TrimSpace(msg), ":") {
			r.msgLines = 12
		}
	}
	if header {
		// The Traceback header opens an error record whose defining line is
		// its last; the frames between draw as part of it.
		l.SetLevel("error")
		r.environ = false
		return ""
	}
	if event != "" {
		r.problemAttrs(l, event, msg)
		appRaise(l)
		r.port = ""
		return event
	}
	if m.has(msg, appTrigDeprecation) && appDeprecation(msg) {
		if l.Level == "" {
			l.SetLevel("warn")
		}
		return "deprecation"
	}
	if l.Level == "" && strings.HasPrefix(strings.TrimLeft(msg, " "), "⚠ ") {
		// Next.js's own warning marker is the line's severity token.
		l.SetLevel("warn")
	}
	if stop, graceful := appShutdown(msg, m); stop {
		if graceful {
			l.SetLevel("info")
		}
		return "shutdown"
	}
	if port, ms, ready, carry := appStartup(msg, m); ready || carry != "" {
		if carry != "" {
			r.port = strings.Clone(carry)
			return ""
		}
		r.readyLine = true
		if r.prevReady {
			return ""
		}
		if port == "" {
			port = r.port
		}
		r.port = ""
		l.SetAttr("port", port)
		if ms >= 0 {
			l.SetAttrNumber("duration_ms", ms)
		}
		return "startup"
	}
	return ""
}

// problemAttrs records what a failure names: the port that was taken, the
// table that is missing.
func (r *appReader) problemAttrs(l *Line, event, text string) {
	switch event {
	case "port_in_use":
		l.SetAttr("port", appTakenPort(text))
	case "schema_missing":
		for _, pattern := range appMissingTablePatterns {
			if match := pattern.FindStringSubmatch(text); match != nil {
				l.SetAttr("table", match[1])
				break
			}
		}
	}
}

// appTrigger is a family of checks the lens runs on a line: the failures,
// the lifecycle, the request formats that are found by a substring rather
// than by how the line starts. appTriggers answers which of the families'
// words a line may contain, so a line that contains none — most of an
// application's output — costs one pass over its bytes rather than fifty
// substring scans, and a line that happens to share three bytes with one
// word costs one scan for that word.
type appTrigger uint16

const (
	appTrigOOM appTrigger = 1 << iota
	appTrigPort
	appTrigSchema
	appTrigDB
	appTrigEnv
	appTrigDeprecation
	appTrigShutdown
	appTrigStartup
	appTrigUvicorn
	appTrigHTTP
	appTrigFPM
	appTrigException
)

// appTriggerWords are the words each family's checks cannot match without,
// and for each the three bytes of it the pass looks for — chosen to be rare,
// since a common one only costs the word being looked for in vain. A check
// that could match a line containing none of its family's words would never
// run, so every test in a family is written against these words.
var appTriggerWords = []struct {
	trigger    appTrigger
	word, gram string
}{
	{appTrigOOM, "emory", "emo"}, {appTrigOOM, "heap limit", "eap"},
	{appTrigPort, "n use", "n u"}, {appTrigPort, "EADDRINUSE", "DDR"},
	{appTrigSchema, "exist", "xis"}, {appTrigSchema, "such table", "uch"}, {appTrigSchema, "igration", "igr"},
	{appTrigDB, "onnect", "onn"}, {appTrigDB, "ECONN", "CON"}, {appTrigDB, "database", "aba"},
	{appTrigDB, "P1001", "P10"}, {appTrigDB, "host name", "ost"}, {appTrigDB, "SQLSTATE", "TAT"},
	{appTrigDB, "Communications", "mmu"}, {appTrigDB, "Mongo", "Mon"}, {appTrigDB, "ServerSelection", "rSe"},
	{appTrigDB, "ENOTFOUND", "TFO"}, {appTrigDB, "EAI_AGAIN", "AI_"},
	{appTrigEnv, "nvironment", "nvi"}, {appTrigEnv, "SECRET_KEY", "T_K"}, {appTrigEnv, "secret_key_base", "t_k"},
	{appTrigEnv, "MissingSecret", "gSe"}, {appTrigEnv, "LEPTOS_", "PTO"}, {appTrigEnv, " set", "set"},
	{appTrigEnv, "is required", "uir"}, {appTrigEnv, "issing", "iss"},
	{appTrigDeprecation, "eprecat", "epr"}, {appTrigDeprecation, "EPRECAT", "EPR"},
	{appTrigShutdown, "code 143", "143"}, {appTrigShutdown, "code 130", "130"}, {appTrigShutdown, "SIGTERM", "GTE"},
	{appTrigShutdown, "SIGINT", "GIN"}, {appTrigShutdown, "hutting down", "hut"}, {appTrigShutdown, "raceful", "cef"},
	{appTrigShutdown, "Handling signal", "ndl"},
	{appTrigStartup, "istening", "eni"}, {appTrigStartup, "unning ", "nni"}, {appTrigStartup, "tarted ", "rte"},
	{appTrigStartup, "erver ", "rve"}, {appTrigStartup, "eady ", "ady"}, {appTrigStartup, "Local:", "al:"},
	{appTrigStartup, "with Bandit", "Ban"}, {appTrigStartup, "with Cowboy", "Cow"},
	{appTrigStartup, "successfully started", "ssf"},
	{appTrigUvicorn, ` - "`, `- "`},
	{appTrigHTTP, " HTTP/", "TTP"},
	{appTrigFPM, " -  ", "-  "},
	{appTrigException, "http: panic serving ", "nic"}, {appTrigException, ".rb:", ".rb"},
	{appTrigException, `{"exception":"[object] (`, `{"e`},
}

// appWords is a set of appTriggerWords entries, one bit each.
type appWords uint64

var (
	appTriggerTable [1 << 14]appWords
	appFamilyWords  [16]appWords
)

func init() {
	if len(appTriggerWords) > 64 {
		panic("logsx: more app trigger words than bits")
	}
	for i, w := range appTriggerWords {
		if len(w.gram) != 3 || !strings.Contains(w.word, w.gram) {
			panic("logsx: app trigger " + w.gram + " is not three bytes of " + w.word)
		}
		appTriggerTable[appGram(w.gram[0], w.gram[1], w.gram[2])] |= 1 << i
		appFamilyWords[bits.TrailingZeros16(uint16(w.trigger))] |= 1 << i
	}
}

// appGram hashes three bytes into the table. Two unrelated sequences may
// share a slot; that only has a word looked for that is not there.
func appGram(a, b, c byte) uint16 {
	return uint16((uint32(a)<<16 | uint32(b)<<8 | uint32(c)) * 2654435761 >> 18)
}

func appTriggers(s string) appWords {
	var words appWords
	for i := 2; i < len(s); i++ {
		words |= appTriggerTable[appGram(s[i-2], s[i-1], s[i])]
	}
	return words
}

// has says s contains one of a family's words: the exact test behind the
// table's approximate one, run only for the words the table let through.
func (w appWords) has(s string, t appTrigger) bool {
	candidates := w & appFamilyWords[bits.TrailingZeros16(uint16(t))]
	for candidates != 0 {
		i := bits.TrailingZeros64(uint64(candidates))
		candidates &= candidates - 1
		if strings.Contains(s, appTriggerWords[i].word) {
			return true
		}
	}
	return false
}

// appProblem names the specific failure a line reports, most specific first.
// A family is tried only when the line's triggers include it, and its
// expressions only when one of its words is really there.
func appProblem(s string, m appWords) string {
	switch {
	case m.has(s, appTrigOOM) && appOOM(s):
		return "oom"
	case m.has(s, appTrigPort) && appPortInUse(s):
		return "port_in_use"
	case m.has(s, appTrigSchema) && appSchemaMissing(s):
		return "schema_missing"
	case m.has(s, appTrigDB) && appDBUnreachable(s):
		return "db_unreachable"
	case m.has(s, appTrigEnv) && appEnvMissing(s):
		return "env_missing"
	}
	return ""
}

// appOOM is a process that ran out of memory by its own account: Node's
// "JavaScript heap out of memory", gunicorn's "Perhaps out of memory?", Go's
// "runtime: out of memory", Java's OutOfMemoryError, Python's MemoryError,
// PHP's "Allowed memory size of … exhausted".
func appOOM(s string) bool {
	return strings.Contains(s, "out of memory") || strings.Contains(s, "Out of memory") ||
		strings.Contains(s, "heap limit") || strings.Contains(s, "OutOfMemoryError") ||
		strings.HasPrefix(s, "MemoryError") || strings.Contains(s, "Allowed memory size of")
}

var appPortInUseRE = regexp.MustCompile(`EADDRINUSE|[Aa]ddress already in use|Port \d+ was already in use|Connection in use: |Is port \d+ in use\?|port is already in use`)

func appPortInUse(s string) bool { return appPortInUseRE.MatchString(s) }

var appTakenPortRE = regexp.MustCompile(`(?i)(?:port |:|, )([1-9]\d{1,4})\b`)

// appTakenPort is the port a bind failure names, the last one written:
// Node's ":::3000", Python's "('0.0.0.0', 8000)", Rails's "port 3000".
func appTakenPort(s string) string {
	matches := appTakenPortRE.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1][1]
}

// A freshly linked database is empty, and an application that queries it
// before anything applied its schema fails every request with one of these
// (deploy/runtime_output_cause.go missingTablePatterns).
var appMissingTablePatterns = []*regexp.Regexp{
	regexp.MustCompile("The table `([^`]+)` does not exist in the current database"), // Prisma P2021
	regexp.MustCompile(`relation "([^"]+)" does not exist`),                          // PostgreSQL 42P01
	regexp.MustCompile(`Table '([^']+)' doesn't exist`),                              // MySQL and MariaDB 1146
	regexp.MustCompile(`no such table: ([A-Za-z0-9_.]+)`),                            // SQLite
}

// appSchemaMissing is a missing table, or a framework refusing to run with
// migrations it has not applied: Django's "You have 3 unapplied
// migration(s)", Rails's PendingMigrationError.
func appSchemaMissing(s string) bool {
	if strings.Contains(s, "exist") || strings.Contains(s, "no such table: ") {
		for _, pattern := range appMissingTablePatterns {
			if pattern.MatchString(s) {
				return true
			}
		}
	}
	return strings.Contains(s, "unapplied migration") || strings.Contains(s, "PendingMigrationError") ||
		strings.Contains(s, "Migrations are pending")
}

// appDatabasePorts are the ports a refused connection names a database by.
// A refused connection to any other port is some other dependency.
var appDatabasePorts = []string{":5432", ":3306", ":6379", ":27017", ":1433"}

// appDBUnreachable is a database the application could not reach: the
// driver's own sentence, or a refused connection to a database's port, or a
// linked database's db-N.jd.internal name that did not resolve.
func appDBUnreachable(s string) bool {
	switch {
	case strings.Contains(s, "reach database server"), strings.Contains(s, "P1001"),
		strings.Contains(s, "translate host name"), strings.Contains(s, "connection to server at"),
		strings.Contains(s, "could not connect to server"), strings.Contains(s, "Connection terminated unexpectedly"),
		strings.Contains(s, "Can't connect to MySQL server"), strings.Contains(s, "Can't connect to local MySQL server"),
		strings.Contains(s, "SQLSTATE[HY000] [2002]"), strings.Contains(s, "Communications link failure"),
		strings.Contains(s, "ConnectionNotEstablished"), strings.Contains(s, "PG::ConnectionBad"),
		strings.Contains(s, "MongoServerSelectionError"), strings.Contains(s, "MongoNetworkError"),
		strings.Contains(s, "ServerSelectionTimeoutError"), strings.Contains(s, "failed to connect to `"),
		strings.Contains(s, "Connection is not available, request timed out"):
		return true
	case strings.Contains(s, "ECONNREFUSED") || strings.Contains(s, "onnection refused"):
		for _, port := range appDatabasePorts {
			if strings.Contains(s, port) {
				return true
			}
		}
	case strings.Contains(s, "ENOTFOUND") || strings.Contains(s, "EAI_AGAIN"):
		return strings.Contains(s, ".jd.internal")
	}
	return false
}

// appEnvMissingRE are a variable the program reads and was not given
// (deploy/runtime_output_cause.go envMissingSignatures), and appGenericEnvRE
// the sentences any program may print about one, which are read only when
// the line does not call itself a warning: a program says "SENTRY_DSN is not
// set" and carries on.
var (
	appEnvMissingRE = regexp.MustCompile(`(?:Missing required environment variable|Cannot resolve environment variable): [A-Za-z_]\w*|` +
		`Environment variable not found: [A-Za-z_]\w*|Invalid environment variables|` +
		`Set the [A-Z_][A-Z0-9_]* environment variable|The SECRET_KEY setting must not be empty|` +
		"Missing .?secret_key_base.? for|" + `\[auth\]\[error\] MissingSecret|MissingSecret: Please define a .secret.|` +
		`environment variable [A-Z_][A-Z0-9_]* is missing|\bLEPTOS_[A-Z_]+\b.*(?:NotPresent|not (?:found|present|set)|is missing)`)
	appGenericEnvRE = regexp.MustCompile(`\b[A-Z][A-Z0-9]*_[A-Z0-9_]+ (?:is not set|must be set|is required|environment variable is (?:required|missing))\b|` +
		`\b[A-Z]{3,} (?:is not set|must be set|environment variable is (?:required|missing))\b|` +
		`Missing (?:required )?(?:env(?:ironment)? )?variable:? "?[A-Z][A-Z0-9_]{2,}`)
	appWarningWordRE = regexp.MustCompile(`(?i)\bwarn(?:ing)?\b`)
)

func appEnvMissing(s string) bool {
	if (strings.Contains(s, "nvironment variable") || strings.Contains(s, "SECRET_KEY setting") ||
		strings.Contains(s, "secret_key_base") || strings.Contains(s, "MissingSecret") || strings.Contains(s, "LEPTOS_")) &&
		appEnvMissingRE.MatchString(s) {
		return true
	}
	if !(strings.Contains(s, " is not set") || strings.Contains(s, " must be set") || strings.Contains(s, " is required") ||
		strings.Contains(s, " variable")) || !appCapitals(s) {
		return false
	}
	return appGenericEnvRE.MatchString(s) && !appWarningWordRE.MatchString(s)
}

// appCapitals says s has three capitals in a row, which every variable name
// the generic sentences are about does — and "Password is required" does not.
func appCapitals(s string) bool {
	run := 0
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			if run++; run == 3 {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}

// appDeprecation is a runtime's own deprecation notice: Node's
// "[DEP0040] DeprecationWarning", Python's DeprecationWarning, PHP's
// "Deprecated:", Rails's "DEPRECATION WARNING", npm's "warn deprecated".
func appDeprecation(s string) bool {
	return strings.Contains(s, "DeprecationWarning") || strings.Contains(s, "DEPRECATION WARNING") ||
		strings.Contains(s, "Deprecated: ") || strings.Contains(s, "warn deprecated") || strings.Contains(s, "WARN deprecated")
}

// appShutdown is a server saying it is stopping. graceful marks a package
// manager reporting that the script it ran ended on SIGTERM or SIGINT — bun's
// `error: script "start" exited with code 143`, npm's "npm error signal
// SIGTERM" — which it words as an error and which is a stop. gunicorn's
// closing "Shutting down: Master" is left out: it already said "Handling
// signal: term" about the same stop.
func appShutdown(s string, m appWords) (stop, graceful bool) {
	if !m.has(s, appTrigShutdown) {
		return false, false
	}
	switch {
	case (strings.Contains(s, "exited with code 143") || strings.Contains(s, "exited with code 130")) && strings.Contains(s, "script "),
		(strings.Contains(s, "signal SIGTERM") || strings.Contains(s, "signal SIGINT")) && strings.Contains(s, "npm "):
		return true, true
	case strings.Contains(s, "hutting down"):
		return !strings.Contains(s, "Shutting down: Master"), false
	case strings.Contains(s, "Gracefully stopping"), strings.Contains(s, "Commencing graceful shutdown"),
		strings.Contains(s, "Handling signal: term"), strings.Contains(s, "Handling signal: int"),
		strings.Contains(s, "Handling signal: quit"), strings.Contains(s, "SIGTERM received"),
		strings.Contains(s, "Received SIGTERM"), strings.Contains(s, "received SIGTERM"):
		return true, false
	}
	return false, false
}

// Startup lines are the ones that say the server is up and where. A banner
// ("▲ Next.js 16", "Starting gunicorn") is not one: a crash loop prints the
// banner every time and never gets further, and the line worth a divider is
// the one that proves the start succeeded.
var (
	// "Listening at: http://0.0.0.0:8000", "Uvicorn running on http://0.0.0.0:8000",
	// "Server is running on port 3000", "http server started on [::]:1323". The
	// host must look like one, so "started at 10:30" names no port.
	appListenRE = regexp.MustCompile(`(?i)\b(?:listening(?: and serving HTTP)?|running|started|server) (?:on|at):?\s+(?:port\s+([1-9]\d{1,4})\b|(?:[a-z][a-z0-9+.-]*://)?(?:\[[^\]\s]*\]|[a-z0-9.-]*[a-z.][a-z0-9.-]*)?:([1-9]\d{1,4})\b)`)
	// "Listening on 3000": a bare number is a port only after listening —
	// "running on 16 cores" is not one.
	appListenBareRE = regexp.MustCompile(`(?i)\blistening (?:on|at):?\s+([1-9]\d{1,4})(?:[\s!,;)]|\.?$)`)
	appReadyRE      = regexp.MustCompile(`(?i)\bready in (\d+(?:\.\d+)?) ?(ms|s)\b`)
	appStartedInRE  = regexp.MustCompile(`\bStarted \S+ in (\d+(?:\.\d+)?) seconds`)
	appServletRE    = regexp.MustCompile(`\b(?:Tomcat|Netty|Jetty|Undertow) started on port(?:\(s\))?:? ([1-9]\d{1,4})`)
	appPhoenixRE    = regexp.MustCompile(`with (?:Bandit|Cowboy) [\d.]+ at \S*:([1-9]\d{1,4}) \(http`)
	appLocalURLRE   = regexp.MustCompile(`^\s*(?:-|➜)\s+Local:\s+\S+:([1-9]\d{1,4})\b`)
)

// appStartup answers a ready line's port and time-to-ready (-1 when not
// printed), or carry: a port printed ahead of the ready line.
func appStartup(s string, m appWords) (port string, ms float64, ready bool, carry string) {
	ms = -1
	if !m.has(s, appTrigStartup) {
		return "", ms, false, ""
	}
	// Each expression is tried only on a line with the words it needs; an
	// expression costs more than every substring test before it together.
	if strings.Contains(s, "Local:") {
		if g := appLocalURLRE.FindStringSubmatch(s); g != nil {
			return "", ms, false, g[1]
		}
	}
	if strings.Contains(s, " started on port") {
		if g := appServletRE.FindStringSubmatch(s); g != nil {
			return "", ms, false, g[1]
		}
	}
	if strings.Contains(s, "eady in") {
		if g := appReadyRE.FindStringSubmatch(s); g != nil {
			ms, _ = appDurationMS(g[1], strings.ToLower(g[2]))
			return "", ms, true, ""
		}
	}
	if strings.Contains(s, " seconds") {
		if g := appStartedInRE.FindStringSubmatch(s); g != nil {
			ms, _ = appDurationMS(g[1], "s")
			return "", ms, true, ""
		}
	}
	if strings.Contains(s, "with Bandit") || strings.Contains(s, "with Cowboy") {
		if g := appPhoenixRE.FindStringSubmatch(s); g != nil {
			return g[1], ms, true, ""
		}
	}
	if strings.Contains(s, "Nest application successfully started") || strings.Contains(s, "ready to handle connections") {
		return "", ms, true, ""
	}
	// Both need the verb followed by "on" or "at", which most lines that say
	// "Running" or "started" do not have.
	if !(strings.Contains(s, "ing on") || strings.Contains(s, "ing at") || strings.Contains(s, "ted on") ||
		strings.Contains(s, "ted at") || strings.Contains(s, "ver on") || strings.Contains(s, "ver at") ||
		strings.Contains(s, "HTTP on")) {
		return "", ms, false, ""
	}
	if g := appListenRE.FindStringSubmatch(s); g != nil {
		return g[1] + g[2], ms, true, ""
	}
	if g := appListenBareRE.FindStringSubmatch(s); g != nil {
		return g[1], ms, true, ""
	}
	return "", ms, false, ""
}

// appExceptionHead recognises the first line of an exception in each
// runtime's spelling, and answers the error attr (class and the first line
// of the message, for grouping) and the record it opens. header is Python's
// Traceback line, which opens a record but names nothing yet.
func appExceptionHead(s string, m appWords) (errText string, rec appRecord, header bool) {
	t := strings.TrimLeft(s, " \t")
	if t == "" {
		return "", appRecNone, false
	}
	switch {
	case strings.HasPrefix(t, "Traceback (most recent call last)"):
		return "", appRecPyBody, true
	case strings.HasPrefix(t, "⨯ "):
		// Next.js's error marker. What follows is an error, with or without a
		// class: "⨯ Error: …", "⨯ unhandledRejection: …", "⨯ Failed to …".
		rest := strings.TrimPrefix(t, "⨯ ")
		for _, lead := range []string{"unhandledRejection: ", "uncaughtException: "} {
			rest = strings.TrimPrefix(rest, lead)
		}
		if class, message, ok := appClassToken(strings.TrimPrefix(rest, "[")); ok {
			return appErrorText(class, strings.TrimSuffix(message, "]")), appRecTrace, false
		}
		return appFirstLine(rest), appRecTrace, false
	case strings.HasPrefix(t, "panic: "):
		return appFirstLine(t), appRecGo, false
	case strings.HasPrefix(t, "fatal error: "):
		return appFirstLine(t), appRecGo, false
	case strings.HasPrefix(t, "Exception in thread \""):
		if end := strings.Index(t[len("Exception in thread \""):], "\" "); end >= 0 {
			t = t[len("Exception in thread \"")+end+2:]
		}
	case strings.HasPrefix(t, "Unhandled exception. "):
		t = strings.TrimPrefix(t, "Unhandled exception. ")
	case strings.HasPrefix(t, "Uncaught "):
		t = strings.TrimPrefix(strings.TrimPrefix(t, "Uncaught "), "(in promise) ")
	case strings.HasPrefix(t, "PHP Fatal error:"), strings.HasPrefix(t, "Fatal error:"),
		strings.HasPrefix(t, "PHP Parse error:"), strings.HasPrefix(t, "Parse error:"):
		return appPHPError(t), appRecPHP, false
	case t[0] == '[':
		// Node inspects an error without a stack as "[Error: message]", and
		// its unhandled-rejection report the same way.
		if class, message, ok := appClassToken(t[1:]); ok {
			return appErrorText(class, strings.TrimSuffix(strings.TrimSuffix(message, " {"), "]")), appRecTrace, false
		}
		return "", appRecNone, false
	}
	if m.has(t, appTrigException) {
		if i := strings.Index(t, "http: panic serving "); i >= 0 {
			// net/http's recovered panic, after the client address it served.
			rest := t[i+len("http: panic serving "):]
			if j := strings.Index(rest, ": "); j >= 0 {
				rest = rest[j+2:]
			}
			return "panic: " + appFirstLine(rest), appRecGo, false
		}
	}
	if class, message, ok := appClassToken(t); ok {
		return appErrorText(class, message), appRecTrace, false
	}
	if m.has(t, appTrigException) && strings.Contains(t, ".rb:") {
		if g := appRubyErrorRE.FindStringSubmatch(t); g != nil {
			return appErrorText(g[2], g[1]), appRecRuby, false
		}
	}
	if strings.HasSuffix(t, "):") && strings.Contains(t, "::") {
		if g := appRailsErrorRE.FindStringSubmatch(t); g != nil {
			return appErrorText(g[1], g[2]), appRecRuby, false
		}
	}
	if m.has(t, appTrigException) && strings.Contains(t, `{"exception":"[object] (`) {
		if g := appLaravelErrorRE.FindStringSubmatch(t); g != nil {
			return appErrorText(g[1], g[2]), appRecPHP, false
		}
	}
	return "", appRecNone, false
}

var (
	// "app/models/user.rb:12:in `name': undefined method `x' for nil (NoMethodError)"
	appRubyErrorRE = regexp.MustCompile("^\\S+\\.rb:\\d+:in [`'][^']*': (.*) \\(([A-Z]\\w*(?:::[A-Z]\\w*)*)\\)$")
	// Rails's request exception: "ActionController::RoutingError (No route matches [GET] "/x"):"
	appRailsErrorRE = regexp.MustCompile(`^([A-Z]\w*(?:::[A-Z]\w*)+) \((.*)\):$`)
	// Laravel's context: {"exception":"[object] (ErrorException(code: 0): Undefined variable $x at /path:12)
	appLaravelErrorRE = regexp.MustCompile(`\{"exception":"\[object\] \(([\w\\]+)\(code: [^)]*\): (.*?) at /`)
	appPHPUncaughtRE  = regexp.MustCompile(`Uncaught ([\w\\]+): (.*?)(?: in /\S+(?::\d+| on line \d+)|$)`)
	appPHPLocationRE  = regexp.MustCompile(` in /\S+(?::\d+| on line \d+)$`)
)

// appPHPError is a PHP fatal's class and message without the file it was
// raised in, which would make every call site its own group.
func appPHPError(t string) string {
	if m := appPHPUncaughtRE.FindStringSubmatch(t); m != nil {
		return appErrorText(m[1], m[2])
	}
	t = strings.TrimPrefix(t, "PHP ")
	colon := strings.IndexByte(t, ':')
	kind, message := t[:colon], strings.TrimSpace(t[colon+1:])
	return appErrorText(kind, appPHPLocationRE.ReplaceAllString(message, ""))
}

// appClassToken reads an exception class at the start of a line — "Error:",
// "TypeError [ERR_INVALID_ARG_TYPE]:", "java.lang.IllegalStateException:",
// "PrismaClientKnownRequestError:" — and the message after it. A class with no
// colon counts only as the whole line ("java.lang.NullPointerException"), so
// "Error handling request" is prose, not an exception.
func appClassToken(t string) (class, message string, ok bool) {
	end := 0
	for end < len(t) {
		c := t[end]
		if !(c == '_' || c == '$' || c == '.' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			break
		}
		end++
	}
	class = t[:end]
	if !strings.HasSuffix(class, "Error") && !strings.HasSuffix(class, "Exception") && !strings.HasSuffix(class, "Rejection") {
		return "", "", false
	}
	// The simple name must be a class — "Error", "TypeError", not "error" or
	// a dotted "config.error".
	simple := class[strings.LastIndexByte(class, '.')+1:]
	if simple == "" || simple[0] < 'A' || simple[0] > 'Z' {
		return "", "", false
	}
	rest := t[end:]
	if strings.HasPrefix(rest, " [") {
		if close := strings.IndexByte(rest, ']'); close > 0 {
			rest = rest[close+1:]
		}
	}
	switch {
	case rest == "":
		return class, "", true
	case rest[0] == ':':
		return class, appFirstLine(rest[1:]), true
	}
	return "", "", false
}

func appErrorText(class, message string) string {
	message = appFirstLine(message)
	if message == "" {
		return class
	}
	return class + ": " + message
}

// structured reads a JSON line that is not a request: its message is the
// sentence the classifiers read, and an error object with a stack is an
// exception.
func (r *appReader) structured(l *Line) string {
	msg := appFirstLine(l.Message)
	errText := ""
	for _, key := range []string{"err", "error", "exception"} {
		value := l.Fields[key]
		if value == "" {
			continue
		}
		if value[0] != '{' {
			// An error written as a string names a failure when it is one of
			// the specific ones: a Go service's `"err":"failed to connect to …"`.
			if event := appProblem(value, appTriggers(value)); event != "" {
				r.problemAttrs(l, event, value)
				appRaise(l)
				return event
			}
		} else if strings.Contains(value, `"stack"`) {
			var object struct {
				Type, Name, Message string
			}
			if json.Unmarshal([]byte(value), &object) == nil {
				class := object.Type
				if class == "" {
					class = object.Name
				}
				if class == "" {
					class = "Error"
				}
				errText = appErrorText(class, object.Message)
			}
		}
		break
	}
	if errText == "" && (l.Level == "error" || l.Level == "critical") {
		for _, key := range []string{"stack", "stacktrace", "stack_trace", "exc_info"} {
			if value := l.Fields[key]; value != "" {
				if class, message, ok := appClassToken(strings.TrimSpace(value)); ok {
					errText = appErrorText(class, message)
				} else {
					errText = msg
				}
				break
			}
		}
	}
	if errText != "" {
		l.SetAttr("error", appCap(errText, appErrorCap))
		event := appProblem(msg, appTriggers(msg))
		if event == "" {
			event = appProblem(errText, appTriggers(errText))
		}
		if event == "" {
			event = "exception"
		}
		appRaise(l)
		return event
	}
	return r.classify(l, msg, appTriggers(msg))
}

// appJSON*Keys are the spellings JSON request loggers use for the fields a
// request line carries: pino-http, echo, zap and slog middleware, Python's
// structlog and json formatters.
var (
	appJSONMethodKeys   = []string{"method", "http.method", "request_method", "httpMethod", "http_method"}
	appJSONStatusKeys   = []string{"status", "statusCode", "status_code", "http.status_code", "responseStatus", "http_status"}
	appJSONPathKeys     = []string{"path", "url", "uri", "route", "request_uri", "originalUrl", "http.target", "http.url"}
	appJSONClientKeys   = []string{"remote_ip", "remoteAddress", "remote_addr", "client_ip", "clientIp", "ip"}
	appJSONDurationKeys = []string{"duration_ms", "responseTime", "response_time", "response_time_ms", "latency_ms", "elapsed_ms"}
)

func appField(fields map[string]string, keys []string) string {
	for _, key := range keys {
		if value := fields[key]; value != "" {
			return value
		}
	}
	return ""
}

// structuredRequest reads a JSON request line: a method and a status in
// any of the common spellings, or pino-http's nested req and res.
func (r *appReader) structuredRequest(l *Line) bool {
	if l.Fields == nil {
		return false
	}
	// The method decides: most structured lines are not requests, and one
	// lookup set is all they should cost.
	method, status, path, client := appField(l.Fields, appJSONMethodKeys), "", "", ""
	if method != "" {
		status, path, client = appField(l.Fields, appJSONStatusKeys), appField(l.Fields, appJSONPathKeys), appField(l.Fields, appJSONClientKeys)
	} else if strings.HasPrefix(l.Fields["req"], "{") && strings.HasPrefix(l.Fields["res"], "{") {
		var req struct {
			Method, URL   string
			RemoteAddress string `json:"remoteAddress"`
		}
		var res struct {
			StatusCode int `json:"statusCode"`
		}
		if json.Unmarshal([]byte(l.Fields["req"]), &req) != nil || json.Unmarshal([]byte(l.Fields["res"]), &res) != nil {
			return false
		}
		method, path, client, status = req.Method, req.URL, req.RemoteAddress, strconv.Itoa(res.StatusCode)
	}
	if !appMethod(method) {
		return false
	}
	code, err := strconv.Atoi(status)
	if err != nil || code < 100 || code > 599 {
		return false
	}
	duration := -1.0
	if value := appField(l.Fields, appJSONDurationKeys); value != "" {
		if ms, err := strconv.ParseFloat(value, 64); err == nil {
			duration = ms
		}
	} else if value := appField(l.Fields, []string{"latency", "duration", "elapsed"}); value != "" {
		// A Go duration string carries its unit; a bare number does not, and
		// guessing between seconds and nanoseconds would be wrong by 10⁹.
		if value[len(value)-1] == 's' {
			if ms, ok := appGoDurationMS(value); ok {
				duration = ms
			}
		}
	}
	if i := strings.Index(path, "://"); i >= 0 {
		if slash := strings.IndexByte(path[i+3:], '/'); slash >= 0 {
			path = path[i+3+slash:]
		} else {
			path = "/"
		}
	}
	r.requestAttrs(l, method, path, code, duration, appClientAddress(client), true)
	return true
}

func appMethod(s string) bool {
	switch s {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "CONNECT", "TRACE":
		return true
	}
	return false
}

// appClientAddress is the IP alone: "::ffff:10.0.0.4" as the IPv4 it is,
// "10.0.0.4:51234" without the port, "[::1]:8080" without brackets.
func appClientAddress(s string) string {
	s = strings.TrimPrefix(s, "::ffff:")
	if strings.HasPrefix(s, "[") {
		if end := strings.IndexByte(s, ']'); end > 0 {
			return s[1:end]
		}
	}
	if i := strings.LastIndexByte(s, ':'); i > 0 && strings.IndexByte(s, ':') == i && appDigits(s[i+1:]) {
		return s[:i]
	}
	return s
}

// requestAttrs records a request line. tok says the format carries its own
// level token (uvicorn's "INFO:", a JSON level), which a status only
// overrules when the line did not state one: uvicorn prints every response
// at INFO, and that is its choice to make. A format without a token takes
// its level from the status alone — the word scan would otherwise read
// "error" out of a path like /api/error.
func (r *appReader) requestAttrs(l *Line, method, path string, status int, ms float64, client string, tok bool) {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	l.SetAttr("method", method)
	l.SetAttr("path", path)
	l.SetAttr("status", strconv.Itoa(status))
	l.SetAttr("class", appStatusClass(status))
	if ms >= 0 {
		l.SetAttrNumber("duration_ms", ms)
	}
	l.SetAttr("client", client)
	if tok && l.HasOwnLevel() {
		return
	}
	switch {
	case status >= 500:
		l.SetLevel("error")
	case status >= 400:
		l.SetLevel("warn")
	default:
		l.SetLevel("info")
	}
}

// appPendingRequests holds the few Rails and Phoenix requests whose opening
// line has been read and whose closing line has not. Puma serves several
// at once, so the opening line is found again by the request id the
// production logger tags both with; a fixed ring bounds what a log that
// never prints the closing line can hold.
type appPendingRequests struct {
	entries [16]appPending
	next    int
}

type appPending struct {
	id, method, path, client string
}

func (p *appPendingRequests) put(entry appPending) {
	// Cloned so a waiting request holds its few bytes, not the line they
	// were cut from.
	entry.id, entry.method = strings.Clone(entry.id), strings.Clone(entry.method)
	entry.path, entry.client = strings.Clone(entry.path), strings.Clone(entry.client)
	p.entries[p.next] = entry
	p.next = (p.next + 1) % len(p.entries)
}

func (p *appPendingRequests) take(id string) (appPending, bool) {
	for k := range len(p.entries) {
		i := (p.next - 1 - k + 2*len(p.entries)) % len(p.entries)
		if entry := p.entries[i]; entry.method != "" && entry.id == id {
			p.entries[i] = appPending{}
			return entry, true
		}
	}
	return appPending{}, false
}

// prefix strips a logger's own prefix — its time, level token and logger
// name — reading the time and the logger out of it, and answers the message
// the rest of the lens reads and whether the format has a level token of its
// own. The first byte decides which formats can apply, so the prose that is
// most of an application's output is never offered to a regular expression.
func (r *appReader) prefix(l *Line, text string) (string, bool) {
	if text == "" {
		return text, false
	}
	switch c := text[0]; {
	case c >= '0' && c <= '9':
		return r.stampPrefix(l, text)
	case c == '[':
		return r.bracketPrefix(l, text)
	case c == 'I' || c == 'D' || c == 'W' || c == 'E' || c == 'F' || c == 'A' || c == 'C' || c == 'T':
		return r.levelPrefix(l, text)
	}
	return text, false
}

// appSetStamp records a stamp the lens read, allocating only when it differs
// from the one ParseLine already found — which, for a zoned stamp, it will
// not.
func appSetStamp(l *Line, t time.Time) {
	if l.Timestamp != nil && l.Timestamp.Equal(t) {
		return
	}
	l.Timestamp = &t
}

var (
	// Spring Boot: "2026-08-17T10:51:28.508Z  INFO 11390 --- [myapp] [main] o.s.b.w.e.tomcat.TomcatWebServer : msg"
	appSpringRE = regexp.MustCompile(`^\s+(?:TRACE|DEBUG|INFO|WARN|ERROR|FATAL)\s+\d+\s+---\s+(?:\[[^\]]*\]\s+)?\[\s*[^\]]*\]\s+(\S+)\s+:\s(.*)$`)
	// Phoenix and Elixir's Logger: "10:00:00.123 request_id=F-abc [info] GET /"
	appPhoenixPrefixRE = regexp.MustCompile(`^\d{2}:\d{2}:\d{2}\.\d{3} ((?:[\w.]+=\S+ )*)\[(debug|info|notice|warning|warn|error|critical|alert|emergency)\] (.*)$`)
	// Rails's Logger::Formatter: "I, [2026-09-27T10:00:00.123456 #1]  INFO -- : msg"
	appRailsPrefixRE = regexp.MustCompile(`^[DIWEFAU], \[(\S+) #\d+\]\s+(?:DEBUG|INFO|WARN|ERROR|FATAL|ANY|UNKNOWN) -- [^:]*: (.*)$`)
	// NestJS: "[Nest] 1  - 09/27/2026, 10:00:00 AM     LOG [RouterExplorer] Mapped {/, GET} route"
	appNestRE = regexp.MustCompile(`^\[Nest\] \d+\s+-\s+(.+?)\s+(LOG|ERROR|WARN|DEBUG|VERBOSE|FATAL)\s+\[([^\]]+)\]\s(.*)$`)
)

// appNestLevels maps Nest's level tokens, two of which ("LOG", "VERBOSE")
// the word scan does not know.
var appNestLevels = map[string]string{"LOG": "info", "ERROR": "error", "WARN": "warn", "DEBUG": "debug", "VERBOSE": "debug", "FATAL": "critical"}

// stampPrefix reads a line that starts with a date or a time: Python's
// logging, loguru, Spring, Go's log package, Phoenix.
func (r *appReader) stampPrefix(l *Line, text string) (string, bool) {
	if len(text) > 12 && text[2] == ':' && text[5] == ':' {
		if m := appPhoenixPrefixRE.FindStringSubmatch(text); m != nil {
			r.phoenix = true
			if i := strings.Index(m[1], "request_id="); i >= 0 {
				r.lineID = strings.TrimSpace(strings.Fields(m[1][i+len("request_id="):])[0])
			}
			return m[3], true
		}
		return text, false
	}
	t, n, ok := appParseStamp(text)
	if !ok {
		return text, false
	}
	appSetStamp(l, t)
	rest := text[n:]
	if strings.Contains(rest, " --- ") {
		if m := appSpringRE.FindStringSubmatch(rest); m != nil {
			l.SetAttr("component", m[1])
			return m[2], true
		}
	}
	if len(rest) > 3 && (strings.HasPrefix(rest, " | ") || strings.HasPrefix(rest, " - ")) {
		if msg, ok := appSeparated(l, rest[3:], rest[:3]); ok {
			return msg, true
		}
	}
	return strings.TrimLeft(strings.TrimPrefix(rest, ":"), " "), false
}

// appSeparated reads the logger formats that follow the time with a level
// and a logger name between separators, in either order: loguru's
// "| INFO     | module:function:12 - msg", the common Python
// "- name - LEVEL - msg".
func appSeparated(l *Line, rest, sep string) (string, bool) {
	first, after, ok := strings.Cut(rest, sep)
	if !ok {
		return "", false
	}
	first = strings.TrimSpace(first)
	if Normalise(first) != "" || first == "SUCCESS" {
		name, msg, ok := strings.Cut(after, sep)
		if !ok {
			name, msg, ok = strings.Cut(after, " - ")
		}
		if !ok {
			return "", false
		}
		name, _, _ = strings.Cut(strings.TrimSpace(name), ":")
		if appLoggerName(name) {
			l.SetAttr("component", name)
		}
		return msg, true
	}
	level, msg, ok := strings.Cut(after, sep)
	if !ok || Normalise(strings.TrimSpace(level)) == "" || !appLoggerName(first) {
		return "", false
	}
	l.SetAttr("component", first)
	return msg, true
}

func appLoggerName(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c == '_' || c == '.' || c == '-' || c == '<' || c == '>' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// bracketPrefix reads a line that starts with a bracket: gunicorn's,
// Laravel's, PHP-FPM's and Django's bracketed time, Nest's banner, and the
// request id Rails tags a production line with.
func (r *appReader) bracketPrefix(l *Line, text string) (string, bool) {
	switch {
	case strings.HasPrefix(text, "[GIN]"):
		return text, false
	case strings.HasPrefix(text, "[Nest] "):
		m := appNestRE.FindStringSubmatch(text)
		if m == nil {
			return text, false
		}
		if t, ok := appParseLocal("01/02/2006, 3:04:05 PM", m[1]); ok {
			appSetStamp(l, *t)
		}
		l.SetLevel(appNestLevels[m[2]])
		l.SetAttr("component", m[3])
		return m[4], true
	}
	end := strings.IndexByte(text, ']')
	if end < 0 {
		return text, false
	}
	inner, rest := text[1:end], strings.TrimPrefix(text[end+1:], " ")
	if t, n, ok := appParseStamp(inner); ok && n == len(inner) {
		// gunicorn "[… +0000] [1] [INFO] msg", Laravel "[…] production.ERROR: msg"
		appSetStamp(l, t)
		if strings.HasPrefix(rest, "[") {
			for i := 0; i < 2 && strings.HasPrefix(rest, "["); i++ {
				if close := strings.IndexByte(rest, ']'); close > 0 {
					rest = strings.TrimPrefix(rest[close+1:], " ")
				}
			}
			return rest, true
		}
		if dot := strings.IndexByte(rest, '.'); dot > 0 && dot < 32 {
			if level, msg, ok := strings.Cut(rest[dot+1:], ": "); ok && Normalise(level) != "" {
				return msg, true
			}
		}
		return rest, false
	}
	for _, layout := range []string{"02-Jan-2006 15:04:05", "02/Jan/2006 15:04:05"} {
		if len(inner) >= len(layout) && len(inner) <= len(layout)+7 {
			if t, ok := appParseLocal(layout, strings.SplitN(inner, ".", 2)[0]); ok {
				appSetStamp(l, *t)
				// PHP-FPM follows its time with "NOTICE: " or "WARNING: ".
				if level, msg, ok := strings.Cut(rest, ": "); ok && Normalise(level) != "" {
					return msg, true
				}
				return rest, false
			}
		}
	}
	return r.railsTags(text), false
}

// levelPrefix reads a line that starts with a level: Rails's Logger
// formatter, uvicorn's padded "INFO:     msg", and Python's default
// "WARNING:django.request:msg".
func (r *appReader) levelPrefix(l *Line, text string) (string, bool) {
	if len(text) > 3 && text[1] == ',' && text[2] == ' ' && text[3] == '[' {
		m := appRailsPrefixRE.FindStringSubmatch(text)
		if m == nil {
			return text, false
		}
		if t, n, ok := appParseStamp(m[1]); ok && n == len(m[1]) {
			appSetStamp(l, t)
		}
		return r.railsTags(m[2]), true
	}
	colon := strings.IndexByte(text, ':')
	if colon < 4 || colon > 8 || !appLevelToken(text[:colon]) || colon+1 >= len(text) {
		return text, false
	}
	rest := text[colon+1:]
	if rest[0] == ' ' {
		return strings.TrimLeft(rest, " "), true
	}
	name, msg, ok := strings.Cut(rest, ":")
	if !ok || !appLoggerName(name) {
		return text, false
	}
	l.SetAttr("component", name)
	return msg, true
}

// appLevelToken is a level as Python's logging and uvicorn spell it: upper
// case, so "Error: boom" stays an exception rather than a level and a message.
func appLevelToken(s string) bool {
	switch s {
	case "DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL", "TRACE":
		return true
	}
	return false
}

// railsTags strips the tags Rails's TaggedLogging puts before a message,
// keeping a request id among them for the Completed line to be matched by.
func (r *appReader) railsTags(msg string) string {
	for strings.HasPrefix(msg, "[") {
		end := strings.IndexByte(msg, ']')
		if end < 2 || strings.IndexByte(msg[:end], ' ') >= 0 {
			break
		}
		if tag := msg[1:end]; len(tag) >= 8 && r.lineID == "" && appRequestID(tag) {
			r.lineID = tag
		}
		msg = strings.TrimPrefix(msg[end+1:], " ")
	}
	return msg
}

func appRequestID(tag string) bool {
	for i := 0; i < len(tag); i++ {
		c := tag[i]
		if !(c == '-' || c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}

var (
	appRailsStartedRE   = regexp.MustCompile(`^Started ([A-Z]+) "([^"]*)" for (\S+)(?: at (.+))?$`)
	appRailsCompletedRE = regexp.MustCompile(`^Completed (\d{3}) .*? in (\d+(?:\.\d+)?)ms`)
	appPhoenixSentRE    = regexp.MustCompile(`^Sent (\d{3}) in (\d+(?:\.\d+)?)(µs|μs|us|ms|s)$`)
	appUvicornRE        = regexp.MustCompile(`^(\S+) - "([A-Z]+) (\S+) HTTP/[\d.]+" (\d{3})`)
	appQuotedRequestRE  = regexp.MustCompile(`^"([A-Z]+) (\S+)[^"]*" (\d{3})\b`)
	appWerkzeugRE       = regexp.MustCompile(`^(\S+) - \S+ \[(\d{2}/[A-Za-z]{3}/\d{4} \d{2}:\d{2}:\d{2})\] "([A-Z]+) (\S+)[^"]*" (\d{3})\b`)
	appMorganShortRE    = regexp.MustCompile(`^(\S+) - ([A-Z]+) (\S+) HTTP/[\d.]+ (\d{3}) (?:\d+|-) - (\d+(?:\.\d+)?) ms$`)
	appPHPFPMAccessRE   = regexp.MustCompile(`^(\S+) - \S* (\d{2}/[A-Za-z]{3}/\d{4}:\d{2}:\d{2}:\d{2} [+-]\d{4}) "([A-Z]+) ([^"\s]+)[^"]*" (\d{3})\b`)
)

// request reads the request lines applications print, in the spellings of
// the servers that print them. Each is recognised by its first bytes or by a
// substring it cannot lack before any expression runs.
func (r *appReader) request(l *Line, text, msg string, tok bool, mask appWords) (string, bool) {
	m := strings.TrimLeft(msg, " ")
	if m == "" {
		return "", false
	}
	switch m[0] {
	case 'S':
		if strings.HasPrefix(m, "Started ") {
			if g := appRailsStartedRE.FindStringSubmatch(m); g != nil {
				r.pending.put(appPending{id: r.lineID, method: g[1], path: g[2], client: g[3]})
				if t, _, ok := appParseStamp(g[4]); ok {
					appSetStamp(l, t)
				}
				return "", true
			}
		}
		if r.phoenix && strings.HasPrefix(m, "Sent ") {
			if g := appPhoenixSentRE.FindStringSubmatch(m); g != nil {
				opened, _ := r.pending.take(r.lineID)
				status, _ := strconv.Atoi(g[1])
				ms, _ := appDurationMS(g[2], g[3])
				r.requestAttrs(l, opened.method, opened.path, status, ms, opened.client, tok)
				return "request", true
			}
		}
	case 'C':
		if strings.HasPrefix(m, "Completed ") {
			if g := appRailsCompletedRE.FindStringSubmatch(m); g != nil {
				opened, _ := r.pending.take(r.lineID)
				status, _ := strconv.Atoi(g[1])
				ms, _ := strconv.ParseFloat(g[2], 64)
				r.requestAttrs(l, opened.method, opened.path, status, ms, appClientAddress(opened.client), tok)
				return "request", true
			}
		}
	case '-':
		// Hono's logger: "<-- GET /path" on the way in, "--> GET /path 200 3ms" on the way out.
		if strings.HasPrefix(m, "--> ") {
			if method, rest, ok := appMethodPrefix(m[4:]); ok {
				return r.methodFirst(l, method, rest)
			}
		}
	case '"':
		if g := appQuotedRequestRE.FindStringSubmatch(m); g != nil {
			status, _ := strconv.Atoi(g[3])
			r.requestAttrs(l, g[1], g[2], status, -1, "", false)
			return "request", true
		}
	}
	if method, rest, ok := appMethodPrefix(m); ok {
		if r.phoenix && strings.IndexByte(rest, ' ') < 0 {
			r.pending.put(appPending{id: r.lineID, method: method, path: rest})
			return "", true
		}
		return r.methodFirst(l, method, rest)
	}
	if tok && mask.has(m, appTrigUvicorn) {
		// uvicorn: `INFO:     172.18.0.1:40612 - "GET / HTTP/1.1" 200 OK`
		if g := appUvicornRE.FindStringSubmatch(m); g != nil {
			status, _ := strconv.Atoi(g[4])
			r.requestAttrs(l, g[2], g[3], status, -1, appClientAddress(g[1]), tok)
			return "request", true
		}
	}
	if mask.has(text, appTrigHTTP) {
		if strings.Contains(text, `] "`) {
			if entry, ok := accesslog.Parse(text); ok {
				appSetStamp(l, entry.At())
				r.requestAttrs(l, entry.Method, entry.Path, entry.Status, -1, appClientAddress(entry.RemoteIP), false)
				return "request", true
			}
			if g := appWerkzeugRE.FindStringSubmatch(text); g != nil {
				if t, ok := appParseLocal("02/Jan/2006 15:04:05", g[2]); ok {
					appSetStamp(l, *t)
				}
				status, _ := strconv.Atoi(g[5])
				r.requestAttrs(l, g[3], g[4], status, -1, appClientAddress(g[1]), false)
				return "request", true
			}
		}
		if strings.HasSuffix(text, " ms") {
			if g := appMorganShortRE.FindStringSubmatch(text); g != nil {
				status, _ := strconv.Atoi(g[4])
				ms, _ := strconv.ParseFloat(g[5], 64)
				r.requestAttrs(l, g[2], g[3], status, ms, appClientAddress(g[1]), false)
				return "request", true
			}
		}
	}
	if strings.HasPrefix(text, "[GIN] ") {
		return r.gin(l, text)
	}
	if mask.has(text, appTrigFPM) {
		if g := appPHPFPMAccessRE.FindStringSubmatch(text); g != nil {
			if t, err := time.Parse("02/Jan/2006:15:04:05 -0700", g[2]); err == nil {
				appSetStamp(l, t.UTC())
			}
			status, _ := strconv.Atoi(g[5])
			r.requestAttrs(l, g[3], g[4], status, -1, appClientAddress(g[1]), false)
			return "request", true
		}
	}
	return "", false
}

// appMethodPrefix splits "GET /path …" into the method and the rest.
func appMethodPrefix(s string) (string, string, bool) {
	space := strings.IndexByte(s, ' ')
	if space < 3 || space > 7 || !appMethod(s[:space]) {
		return "", "", false
	}
	rest := strings.TrimLeft(s[space+1:], " ")
	if rest == "" || rest[0] != '/' && rest[0] != '*' && !strings.HasPrefix(rest, "http") {
		return "", "", false
	}
	return s[:space], rest, true
}

// methodFirst reads the request lines that start with the method: Next.js's
// "GET /p 200 in 12ms", morgan's dev "GET /p 200 0.224 ms - 2" and tiny
// "GET /p 200 2 - 0.188 ms", Hono's "GET /p 200 3ms".
func (r *appReader) methodFirst(l *Line, method, rest string) (string, bool) {
	var f [6]string
	n := 0
	for field := range strings.FieldsSeq(rest) {
		if n == len(f) {
			break
		}
		f[n] = field
		n++
	}
	if n < 2 || len(f[1]) != 3 || !appDigits(f[1]) {
		return "", false
	}
	status, _ := strconv.Atoi(f[1])
	if status < 100 || status > 599 {
		return "", false
	}
	ms := -1.0
	switch {
	case n >= 4 && f[2] == "in":
		ms = appSplitDuration(f[3])
	case n >= 4 && f[3] == "ms" && appNumber(f[2]):
		ms, _ = strconv.ParseFloat(f[2], 64)
	case n >= 6 && f[3] == "-" && f[5] == "ms":
		ms, _ = strconv.ParseFloat(f[4], 64)
	case n == 3:
		ms = appSplitDuration(f[2])
		if ms < 0 {
			return "", false
		}
	case n > 3:
		return "", false
	}
	r.requestAttrs(l, method, f[0], status, ms, "", false)
	return "request", true
}

// appSplitDuration reads "9.7s", "12ms", "409µs" as milliseconds, or -1.
func appSplitDuration(s string) float64 {
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	if i == 0 {
		return -1
	}
	ms, ok := appDurationMS(s[:i], s[i:])
	if !ok {
		return -1
	}
	return ms
}

func appNumber(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// gin reads gin's request line:
// "[GIN] 2018/12/07 - 09:11:42 | 200 |            5s |     20.20.20.20 | GET      "/"".
func (r *appReader) gin(l *Line, text string) (string, bool) {
	parts := strings.Split(text, "|")
	if len(parts) < 5 {
		return "", false
	}
	status, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || status < 100 || status > 599 {
		return "", false
	}
	method, path, ok := strings.Cut(strings.TrimSpace(parts[4]), " ")
	if !ok || !appMethod(method) {
		return "", false
	}
	if t, ok := appParseLocal("2006/01/02 - 15:04:05", strings.TrimSpace(strings.TrimPrefix(parts[0], "[GIN]"))); ok {
		appSetStamp(l, *t)
	}
	ms := -1.0
	if value, ok := appGoDurationMS(strings.TrimSpace(parts[2])); ok {
		ms = value
	}
	path = strings.TrimSpace(path)
	if unquoted, err := strconv.Unquote(path); err == nil {
		path = unquoted
	}
	r.requestAttrs(l, method, path, status, ms, strings.TrimSpace(parts[3]), false)
	return "request", true
}
