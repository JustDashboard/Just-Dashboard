package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Where a server listens is usually written down: a port literal handed to
// app.listen, a --port flag in the start script, server.port in
// application.yml, a bind to 127.0.0.1 copied from a README. The runtime
// injects PORT and publishes the container's port to the proxy, so a server
// that ignores PORT listens where nothing looks, and one bound to loopback
// cannot be reached from outside its own container at all. Both used to end
// as a readiness timeout that named neither. Detection reads these facts as
// bounded text; nothing here evaluates the source it reads.

// DetectedListen is what a source says about where its server listens, as
// preflight re-checks it against the plan without the source tree.
type DetectedListen struct {
	// Port is a port the source fixes whatever PORT says: a start-command
	// flag, a literal the code listens on without reading PORT, or a
	// configuration value nothing bridges. Zero when the server follows PORT
	// or its framework's default.
	Port     int    `json:"port,omitempty"`
	PortFrom string `json:"portFrom,omitempty"`
	// ReadsPort says the server listens on the PORT the runtime injects,
	// directly or through a bridge the recipe writes.
	ReadsPort     bool   `json:"readsPort,omitempty"`
	ReadsPortFrom string `json:"readsPortFrom,omitempty"`
	// Loopback is a host the server binds that nothing outside its container
	// reaches — 127.0.0.1, localhost or ::1 — written in the source or its
	// framework's default when the source names none.
	Loopback     string `json:"loopback,omitempty"`
	LoopbackFrom string `json:"loopbackFrom,omitempty"`
	// LoopbackCertain says no other listener was found, so the server cannot
	// answer the proxy at all unless something moves it.
	LoopbackCertain bool `json:"loopbackCertain,omitempty"`
	// LoopbackRecipeFix is the setting the automatic recipe writes that moves
	// the listener to every interface, when there is one.
	LoopbackRecipeFix string `json:"loopbackRecipeFix,omitempty"`
	// LoopbackVariable is the plan variable that moves it (HOST), when the
	// server takes its bind address from the environment.
	LoopbackVariable string `json:"loopbackVariable,omitempty"`
	// Unbridged names listen configuration the recipe cannot move onto the
	// planned port, such as several Kestrel endpoints.
	Unbridged string `json:"unbridged,omitempty"`
}

// validateNetworkFacts bounds what a stored detection may carry in its
// listen facts and network variables, as every other detected field is.
func validateNetworkFacts(candidate DetectedCandidate) error {
	text := func(value string, limit int) bool {
		return len(value) <= limit && !strings.ContainsAny(value, "\x00\r\n") && rejectPlanSecretLiteral("listen fact", value) == nil
	}
	if listen := candidate.Listen; listen != nil {
		if listen.Port < 0 || listen.Port > 65535 || !text(listen.PortFrom, 1024) || !text(listen.ReadsPortFrom, 1024) ||
			!text(listen.Loopback, 255) || !text(listen.LoopbackFrom, 1024) || !text(listen.LoopbackRecipeFix, 1024) ||
			!text(listen.Unbridged, 1024) || (listen.LoopbackVariable != "" && ValidateEnvKey(listen.LoopbackVariable) != nil) {
			return fmt.Errorf("%w: detected listen facts are malformed", ErrInvalidPlan)
		}
	}
	if len(candidate.NetworkVariables) > 8 {
		return fmt.Errorf("%w: detected network variables are malformed", ErrInvalidPlan)
	}
	for _, variable := range candidate.NetworkVariables {
		if ValidateEnvKey(variable.Name) != nil || !text(variable.Value, 256) || !text(variable.Reason, 512) ||
			!text(variable.DomainTemplate, 256) || (variable.DomainTemplate != "" && !strings.Contains(variable.DomainTemplate, "{{hostname}}")) ||
			(variable.Value != "") == (variable.DomainTemplate != "") {
			return fmt.Errorf("%w: detected network variables are malformed", ErrInvalidPlan)
		}
	}
	return nil
}

// listenText is a fact read from source as detection may store it: one
// bounded line, and never anything shaped like a credential.
func listenText(value string) string {
	value = strings.Join(strings.Fields(strings.ReplaceAll(value, "\x00", " ")), " ")
	if rejectPlanSecretLiteral("listen fact", value) != nil || containsURLCredentials(value) {
		return "details withheld because they resemble credential material"
	}
	if len(value) > 200 {
		value = value[:197] + "..."
	}
	return value
}

// sourceMark is one fact read from a file and where it was read.
type sourceMark struct {
	path string
	line int
	port int
	host string
	what string
}

func (m sourceMark) at() string {
	if m.line > 0 {
		return fmt.Sprintf("%s:%d", m.path, m.line)
	}
	return m.path
}

// listenReport gathers what a set of files says about listening.
type listenReport struct {
	// ports are listeners with a port literal the code fixes.
	ports []sourceMark
	// fallbacks are the literals a PORT read falls back to.
	fallbacks []sourceMark
	readsPort *sourceMark
	// loopback are listeners bound to a loopback literal, open those bound to
	// every interface, and defaults those given no host by a framework that
	// then binds loopback.
	loopback []sourceMark
	open     []sourceMark
	defaults []sourceMark
	// hostFallback is a listener whose host is read from HOST with a
	// loopback fallback, which HOST=0.0.0.0 moves.
	hostFallback *sourceMark
	// configPorts are port settings a configuration file fixes.
	configPorts []sourceMark
}

func (r *listenReport) merge(other listenReport) {
	r.ports = append(r.ports, other.ports...)
	r.fallbacks = append(r.fallbacks, other.fallbacks...)
	r.loopback = append(r.loopback, other.loopback...)
	r.open = append(r.open, other.open...)
	r.defaults = append(r.defaults, other.defaults...)
	r.configPorts = append(r.configPorts, other.configPorts...)
	if r.readsPort == nil {
		r.readsPort = other.readsPort
	}
	if r.hostFallback == nil {
		r.hostFallback = other.hostFallback
	}
}

func (r listenReport) empty() bool {
	return len(r.ports) == 0 && len(r.fallbacks) == 0 && r.readsPort == nil && len(r.loopback) == 0 &&
		len(r.open) == 0 && len(r.defaults) == 0 && r.hostFallback == nil && len(r.configPorts) == 0
}

// servedPorts are the literal ports the proxy could be reaching. A port bound
// to loopback is a side listener — a debug or admin endpoint — whenever the
// code also reads PORT or listens somewhere the scan cannot read, so it is
// never the port the server is reached on.
func (r listenReport) servedPorts() []sourceMark {
	if r.readsPort == nil && len(r.open) == 0 {
		return r.ports
	}
	var served []sourceMark
	for _, mark := range r.ports {
		if !loopbackHost(mark.host) {
			served = append(served, mark)
		}
	}
	return served
}

// loopbackCertain reports that the loopback listeners are the only way the
// server listens: nothing else listens, and when the code reads PORT, at
// least one loopback listener takes it — a loopback listener on a port of
// its own beside a PORT read is the side listener, and the PORT read feeds
// a server the scan did not see.
func (r listenReport) loopbackCertain() bool {
	if len(r.loopback) == 0 || len(r.open) > 0 {
		return false
	}
	if r.readsPort == nil {
		return true
	}
	for _, mark := range r.loopback {
		if mark.port == 0 {
			return true
		}
	}
	return false
}

// singlePort is the one port a list agrees on; several different ones are
// no answer.
func singlePort(marks []sourceMark) (sourceMark, bool) {
	if len(marks) == 0 {
		return sourceMark{}, false
	}
	for _, mark := range marks[1:] {
		if mark.port != marks[0].port {
			return sourceMark{}, false
		}
	}
	return marks[0], true
}

func loopbackHost(host string) bool {
	host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	return host == "localhost" || host == "::1" || host == "ip6-localhost" || strings.HasPrefix(host, "127.")
}

func openHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	return host == "" || host == "0.0.0.0" || host == "::" || host == "*" || host == "+"
}

func listenPort(text string) int {
	port, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || port < 1 || port > 65535 {
		return 0
	}
	return port
}

func lineOf(content []byte, offset int) int {
	return bytes.Count(content[:offset], []byte("\n")) + 1
}

// commentedAt reports whether the line holding offset starts as a comment,
// so an example left commented out is not read as the server's listener.
func commentedAt(content []byte, offset int, prefixes ...string) bool {
	start := bytes.LastIndexByte(content[:offset], '\n') + 1
	lead := strings.TrimSpace(string(content[start:offset]))
	for _, prefix := range prefixes {
		if strings.HasPrefix(lead, prefix) {
			return true
		}
	}
	return false
}

// callArguments returns the text between the bracket at open and the one
// that closes it, skipping quoted text, within a bound.
func callArguments(content []byte, open int) (string, bool) {
	const limit = 600
	depth := 0
	var quote byte
	for i := open; i < len(content) && i-open < limit; i++ {
		c := content[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				return string(content[open+1 : i]), true
			}
		}
	}
	return "", false
}

// splitArguments splits call arguments at their top-level commas.
func splitArguments(args string) []string {
	var parts []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(args); i++ {
		c := args[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(args[start:i]))
				start = i + 1
			}
		}
	}
	if tail := strings.TrimSpace(args[start:]); tail != "" {
		parts = append(parts, tail)
	}
	return parts
}

// stringLiteral unquotes a single-, double- or backtick-quoted literal with
// no interpolation in it.
func stringLiteral(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if len(text) < 2 {
		return "", false
	}
	quote := text[0]
	if (quote != '\'' && quote != '"' && quote != '`') || text[len(text)-1] != quote {
		return "", false
	}
	inner := text[1 : len(text)-1]
	if strings.ContainsAny(inner, "\\\n") || (quote == '`' && strings.Contains(inner, "${")) {
		return "", false
	}
	return inner, true
}

var identifierRE = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// JavaScript and TypeScript: Node, Bun and Deno servers.
var (
	jsReadsPortRE = regexp.MustCompile(`process\.env\.PORT\b|process\.env\[\s*['"]PORT['"]\s*\]|Bun\.env\.PORT\b|Deno\.env\.get\(\s*['"]PORT['"]\s*\)|\benv\.PORT\b|\{[^{}]*\bPORT\b[^{}]*\}\s*=\s*process\.env`)
	jsReadsHostRE = regexp.MustCompile(`process\.env\.HOST\b|process\.env\[\s*['"]HOST['"]\s*\]|Bun\.env\.HOST\b|Deno\.env\.get\(\s*['"]HOST['"]\s*\)|\benv\.HOST\b`)
	// A PORT read's own fallback: `process.env.PORT || 3000`,
	// `Number(process.env.PORT ?? "8080")`.
	jsFallbackRE     = regexp.MustCompile(`^[\s)'"\]]*(?:\|\||\?\?)\s*\(?\s*['"]?(\d{2,5})\b`)
	jsHostFallbackRE = regexp.MustCompile(`^[\s)'"\]]*(?:\|\||\?\?)\s*['"]([^'"]*)['"]`)
	jsListenCallRE   = regexp.MustCompile(`\.listen\(`)
	jsServeCallRE    = regexp.MustCompile(`\b(?:Bun\.serve|Deno\.serve|serve)\(`)
	jsExportServeRE  = regexp.MustCompile(`export\s+default\s*\{`)
	jsFastifyRE      = regexp.MustCompile(`from\s+['"]fastify['"]|require\(\s*['"]fastify['"]\s*\)|\bFastifyAdapter\b`)
	jsObjectPortRE   = regexp.MustCompile(`(?:^|[\s,{])port\s*:\s*([^,}\n]+)`)
	jsObjectHostRE   = regexp.MustCompile(`(?:^|[\s,{])(?:host|hostname)\s*:\s*([^,}\n]+)`)
	jsObjectHostKey  = regexp.MustCompile(`(?:^|[\s,{])(?:host|hostname)\b`)
	// Shorthand properties name the variable that holds the value.
	jsObjectPortShorthand = regexp.MustCompile(`(?:^|[\s,{])(port)\s*(?:[,}]|$)`)
	jsObjectHostShorthand = regexp.MustCompile(`(?:^|[\s,{])(host|hostname)\s*(?:[,}]|$)`)
)

// jsAssignment finds `const NAME = value` in the file and returns the value
// text, so `app.listen(PORT, HOST)` can be read through its constants.
func jsAssignment(content []byte, name string) (string, int, bool) {
	if !identifierRE.MatchString(name) {
		return "", 0, false
	}
	re := regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:const|let|var)\s+` + regexp.QuoteMeta(name) + `\s*(?::\s*[A-Za-z]+\s*)?=\s*([^;\n]+)`)
	match := re.FindSubmatchIndex(content)
	if match == nil {
		return "", 0, false
	}
	return strings.TrimSpace(string(content[match[2]:match[3]])), match[0], true
}

// scanScriptListen reads a JavaScript or TypeScript file's listeners.
func scanScriptListen(file string, content []byte) listenReport {
	var r listenReport
	comment := []string{"//", "/*", "*"}
	fastify := jsFastifyRE.Match(content)
	mark := func(offset, port int, host, what string) sourceMark {
		return sourceMark{path: file, line: lineOf(content, offset), port: port, host: host, what: what}
	}
	for _, loc := range jsReadsPortRE.FindAllIndex(content, -1) {
		if commentedAt(content, loc[0], comment...) {
			continue
		}
		read := mark(loc[0], 0, "", "reads PORT")
		if r.readsPort == nil {
			r.readsPort = &read
		}
		tail := content[loc[1]:min(len(content), loc[1]+80)]
		if match := jsFallbackRE.FindSubmatch(tail); match != nil {
			if port := listenPort(string(match[1])); port > 0 {
				read.port, read.what = port, fmt.Sprintf("PORT, else %d", port)
				r.fallbacks = append(r.fallbacks, read)
			}
		}
	}
	// portValue reads a listener's port expression: a literal, a PORT read
	// with or without a fallback, or a constant holding one of those.
	var portValue func(expression string, depth int) (int, bool)
	portValue = func(expression string, depth int) (int, bool) {
		expression = strings.TrimSpace(expression)
		if port := listenPort(strings.Trim(expression, `'"`)); port > 0 {
			return port, false
		}
		if jsReadsPortRE.MatchString(expression) {
			return 0, true
		}
		if depth < 2 && identifierRE.MatchString(expression) {
			if value, _, ok := jsAssignment(content, expression); ok {
				return portValue(value, depth+1)
			}
		}
		return 0, false
	}
	// hostValue classifies a listener's host expression: a literal, a HOST
	// read with a fallback, or a constant holding one of those.
	var hostValue func(expression string, depth int) (host string, fromHost bool, known bool)
	hostValue = func(expression string, depth int) (string, bool, bool) {
		expression = strings.TrimSpace(expression)
		if literal, ok := stringLiteral(expression); ok {
			return literal, false, true
		}
		if loc := jsReadsHostRE.FindStringIndex(expression); loc != nil {
			if match := jsHostFallbackRE.FindStringSubmatch(expression[loc[1]:]); match != nil {
				return match[1], true, true
			}
			return "", true, true
		}
		if depth < 2 && identifierRE.MatchString(expression) {
			if value, _, ok := jsAssignment(content, expression); ok {
				return hostValue(value, depth+1)
			}
		}
		return "", false, false
	}
	record := func(offset int, what string, portExpression, hostExpression string, hostGiven bool) {
		port, fromEnv := portValue(portExpression, 0)
		listener := mark(offset, port, "", what)
		fixed := port > 0 && !fromEnv
		if !hostGiven {
			if fixed {
				r.ports = append(r.ports, listener)
			}
			if fastify {
				listener.host = "localhost"
				r.defaults = append(r.defaults, listener)
			} else {
				r.open = append(r.open, listener)
			}
			return
		}
		host, fromHost, known := hostValue(hostExpression, 0)
		if fixed {
			// The port keeps a literal loopback host, so a side listener on
			// localhost is never taken for the port the server is reached on.
			fixedMark := listener
			if known && !fromHost {
				fixedMark.host = host
			}
			r.ports = append(r.ports, fixedMark)
		}
		listener.host = host
		switch {
		case !known:
			// A host read from somewhere this scan cannot follow may well
			// be every interface, so it never makes a loopback certain.
			r.open = append(r.open, listener)
		case fromHost && loopbackHost(host):
			r.hostFallback = &listener
		case fromHost && fastify && host == "":
			listener.host = "localhost"
			r.hostFallback = &listener
		case loopbackHost(host):
			r.loopback = append(r.loopback, listener)
		case openHost(host) || fromHost:
			r.open = append(r.open, listener)
		}
	}
	for _, loc := range jsListenCallRE.FindAllIndex(content, -1) {
		if commentedAt(content, loc[0], comment...) {
			continue
		}
		args, ok := callArguments(content, loc[1]-1)
		if !ok {
			continue
		}
		parts := splitArguments(args)
		if len(parts) == 0 {
			if fastify {
				r.defaults = append(r.defaults, mark(loc[0], 0, "localhost", "listen()"))
			}
			continue
		}
		if strings.HasPrefix(parts[0], "{") {
			object := parts[0]
			portExpression, hostExpression := objectPortAndHost(object)
			record(loc[0], "listen("+boundedCall(object)+")", portExpression, hostExpression, jsObjectHostKey.MatchString(object))
			continue
		}
		host, hostGiven := "", false
		if len(parts) > 1 && !strings.Contains(parts[1], "=>") && !strings.HasPrefix(parts[1], "function") && !strings.HasPrefix(parts[1], "async") {
			host, hostGiven = parts[1], true
		}
		record(loc[0], "listen("+boundedCall(args)+")", parts[0], host, hostGiven)
	}
	serveObject := func(loc []int, what string) {
		args, ok := callArguments(content, loc[1]-1)
		if !ok {
			return
		}
		object := args
		if what != "export default" {
			parts := splitArguments(args)
			if len(parts) == 0 || !strings.HasPrefix(parts[0], "{") {
				// Deno.serve(handler) listens on 8000, whatever PORT says.
				if what == "Deno.serve()" {
					r.ports = append(r.ports, mark(loc[0], 8000, "", "Deno.serve() default port 8000"))
					r.open = append(r.open, mark(loc[0], 8000, "", what))
				}
				return
			}
			object = parts[0]
		}
		portExpression, hostExpression := objectPortAndHost(object)
		if what == "export default" && portExpression == "" {
			return
		}
		if portExpression == "" {
			switch what {
			case "Deno.serve()":
				portExpression = "8000"
			case "Bun.serve()":
				// Bun.serve with no port listens on PORT, else 3000.
				read := mark(loc[0], 3000, "", "Bun.serve() follows PORT, else 3000")
				if r.readsPort == nil {
					r.readsPort = &read
				}
				r.fallbacks = append(r.fallbacks, read)
			}
		}
		// Bun.serve, Deno.serve and @hono/node-server bind every interface
		// unless given a hostname.
		saved := fastify
		fastify = false
		record(loc[0], what, portExpression, hostExpression, jsObjectHostKey.MatchString(object))
		fastify = saved
	}
	for _, loc := range jsServeCallRE.FindAllIndex(content, -1) {
		if !commentedAt(content, loc[0], comment...) {
			serveObject(loc, strings.TrimSuffix(string(content[loc[0]:loc[1]]), "(")+"()")
		}
	}
	for _, loc := range jsExportServeRE.FindAllIndex(content, -1) {
		if !commentedAt(content, loc[0], comment...) {
			serveObject([]int{loc[0], loc[1]}, "export default")
		}
	}
	return r
}

// objectPortAndHost reads the port and host expressions of a listen or
// serve options object, following shorthand properties to their variables.
func objectPortAndHost(object string) (string, string) {
	port, host := "", ""
	if match := jsObjectPortRE.FindStringSubmatch(object); match != nil {
		port = match[1]
	} else if match := jsObjectPortShorthand.FindStringSubmatch(object); match != nil {
		port = match[1]
	}
	if match := jsObjectHostRE.FindStringSubmatch(object); match != nil {
		host = match[1]
	} else if match := jsObjectHostShorthand.FindStringSubmatch(object); match != nil {
		host = match[1]
	}
	return port, host
}

func boundedCall(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 48 {
		text = text[:45] + "..."
	}
	return text
}

// Go: the main package's listeners. An address is read only when it is one
// whole string literal, or an identifier assigned exactly one: `":" + port`
// names neither a port nor a host. A listen call whose address cannot be read
// may well be on every interface, so it counts as an open listener, and a
// debug server on localhost beside it can never make the loopback certain.
var (
	goListenCallRE    = regexp.MustCompile(`\b(?:ListenAndServe|ListenAndServeTLS|Run|RunTLS|Start|StartTLS|Listen|ListenTLS|Serve|ServeTLS)\(`)
	goNetListenRE     = regexp.MustCompile(`\bnet\.Listen\(\s*"tcp[46]?"\s*,`)
	goServerLiteralRE = regexp.MustCompile(`\b(?:http|fasthttp)\.Server\s*\{`)
	goAddrFieldRE     = regexp.MustCompile(`(?:^|[\s,{])Addr\s*:\s*([^,\n}]+)`)
	goReadsPortRE     = regexp.MustCompile(`os\.(?:Getenv|LookupEnv)\(\s*"PORT"\s*\)`)
	goReadsHostRE     = regexp.MustCompile(`os\.(?:Getenv|LookupEnv)\(\s*"HOST"\s*\)`)
	goPortLiteral     = regexp.MustCompile(`"(?::)?(\d{4,5})"`)
	goEmptyRunRE      = regexp.MustCompile(`\.Run\(\s*\)`)
	goHostPrefixRE    = regexp.MustCompile(`^"([^"]*):"\s*\+`)
	goHTTPServerRE    = regexp.MustCompile(`\bhttp\.(?:ListenAndServe|ListenAndServeTLS|Serve)\(|\bhttp\.Server\s*\{`)
	goPprofImportRE   = regexp.MustCompile(`"net/http/pprof"`)
	goAddressNameRE   = regexp.MustCompile(`(?i)(?:addr|port|listen|host|bind)[A-Za-z0-9_]*$`)
	// goRegisteredRouteRE reads the paths a main package registers, for telling a
	// server from a worker that only exposes its metrics.
	goRegisteredRouteRE = regexp.MustCompile(`\.(?:Handle|HandleFunc|GET|POST|PUT|PATCH|DELETE|Get|Post|Put|Patch|Delete|Any|Group|Route|Mount)\(\s*"([^"]*)"`)
)

// goAddress reads a Go listen address argument: a string literal, or an
// identifier whose assignment's whole right-hand side is one.
func goAddress(content []byte, expression string) (string, bool) {
	expression = strings.TrimSpace(expression)
	if literal, ok := stringLiteral(expression); ok {
		return literal, true
	}
	if !identifierRE.MatchString(expression) {
		return "", false
	}
	re := regexp.MustCompile(`(?m)\b` + regexp.QuoteMeta(expression) + `(?:[ \t]+string)?[ \t]*:?=[ \t]*("[^"\n]*")[ \t]*(?://.*)?$`)
	if match := re.FindSubmatch(content); match != nil {
		return stringLiteral(string(match[1]))
	}
	return "", false
}

// goAddressLike reports whether an argument the scan cannot read is an
// address at all. Run and Start are also what a command, a test or a
// scheduler is started with, so only an argument that is built from a
// quoted piece, assigned from one, or named like an address counts.
func goAddressLike(content []byte, expression string) bool {
	expression = strings.TrimSpace(expression)
	if strings.Contains(expression, `"`) || goAddressNameRE.MatchString(expression) {
		return true
	}
	if !identifierRE.MatchString(expression) {
		return false
	}
	re := regexp.MustCompile(`(?m)\b` + regexp.QuoteMeta(expression) + `[ \t]*:?=[ \t]*([^\n]*)$`)
	match := re.FindSubmatch(content)
	return match != nil && (bytes.Contains(match[1], []byte(`":`)) || bytes.Contains(match[1], []byte("Getenv")))
}

func splitHostPort(address string) (string, int, bool) {
	address = strings.TrimSpace(address)
	colon := strings.LastIndex(address, ":")
	if colon < 0 {
		return "", 0, false
	}
	port := listenPort(address[colon+1:])
	if port == 0 {
		return "", 0, false
	}
	return address[:colon], port, true
}

// scanGoListen reads a main package file's listeners. gin is the one
// framework whose argument-less Run follows PORT.
func scanGoListen(file string, content []byte, gin bool) listenReport {
	var r listenReport
	mark := func(offset, port int, host, what string) sourceMark {
		return sourceMark{path: file, line: lineOf(content, offset), port: port, host: host, what: what}
	}
	if loc := goReadsPortRE.FindIndex(content); loc != nil && !commentedAt(content, loc[0], "//") {
		read := mark(loc[0], 0, "", "reads PORT")
		r.readsPort = &read
		// The fallback is the port literal written just after the read, as in
		// `if port == "" { port = "8080" }`; one further away is something else.
		tail := content[loc[1]:min(len(content), loc[1]+200)]
		if match := goPortLiteral.FindSubmatchIndex(tail); match != nil {
			read.port = listenPort(string(tail[match[2]:match[3]]))
			if read.port > 0 {
				read.what = fmt.Sprintf("PORT, else %d", read.port)
				r.fallbacks = append(r.fallbacks, read)
			}
		}
	}
	listener := func(offset int, what, address string) {
		host, port, ok := splitHostPort(address)
		if !ok {
			return
		}
		found := mark(offset, port, host, what)
		r.ports = append(r.ports, found)
		switch {
		case loopbackHost(host):
			r.loopback = append(r.loopback, found)
		case openHost(host):
			r.open = append(r.open, found)
		}
	}
	// address records a listen address expression, described by format:
	// read, loopback by its literal host prefix, or unreadable and therefore
	// open.
	address := func(offset int, format, expression string) {
		if value, ok := goAddress(content, expression); ok {
			listener(offset, fmt.Sprintf(format, strconv.Quote(value)), value)
		} else if match := goHostPrefixRE.FindStringSubmatch(strings.TrimSpace(expression)); match != nil && loopbackHost(match[1]) {
			r.loopback = append(r.loopback, mark(offset, 0, match[1], fmt.Sprintf(format, boundedCall(expression))))
		} else {
			r.open = append(r.open, mark(offset, 0, "", fmt.Sprintf(format, boundedCall(expression))))
		}
	}
	// A server literal's Addr is what its argument-less ListenAndServe
	// listens on; only http.Server and fasthttp.Server have one, not every
	// struct with an Addr field (a Redis client's options have one too).
	servers, readServers := 0, 0
	for _, loc := range goServerLiteralRE.FindAllIndex(content, -1) {
		if commentedAt(content, loc[0], "//") {
			continue
		}
		servers++
		body, ok := callArguments(content, loc[1]-1)
		if !ok {
			continue
		}
		match := goAddrFieldRE.FindStringSubmatchIndex(body)
		if match == nil {
			continue
		}
		readServers++
		address(loc[1]+match[2], "Addr: %s", body[match[2]:match[3]])
	}
	pprof := goPprofImportRE.Match(content)
	for _, loc := range goListenCallRE.FindAllIndex(content, -1) {
		if commentedAt(content, loc[0], "//") {
			continue
		}
		name := strings.TrimSuffix(string(content[loc[0]:loc[1]]), "(")
		receiver := content[max(0, loc[0]-5):loc[0]]
		if name == "Listen" && bytes.HasSuffix(receiver, []byte("net.")) {
			// net.Listen is read below, past its network argument.
			continue
		}
		args, ok := callArguments(content, loc[1]-1)
		if !ok {
			continue
		}
		parts := splitArguments(args)
		switch {
		case len(parts) == 0:
			// srv.ListenAndServe() listens on the server's own Addr, which
			// was read above when the file declares the server.
			if strings.HasPrefix(name, "ListenAndServe") && readServers == 0 {
				r.open = append(r.open, mark(loc[0], 0, "", name+"()"))
			}
			continue
		case name == "ListenAndServeTLS" && len(parts) == 2 && servers > 0 && readServers == servers:
			// The method form takes a certificate and a key, not an address.
			continue
		case name == "ListenAndServeTLS" && len(parts) == 2:
			r.open = append(r.open, mark(loc[0], 0, "", name+"("+boundedCall(args)+")"))
			continue
		}
		if value, ok := goAddress(content, parts[0]); ok {
			host, _, valid := splitHostPort(value)
			if !valid {
				// t.Run("case", …), a job's Start("name"): not an address.
				continue
			}
			if pprof && loopbackHost(host) && name == "ListenAndServe" && len(parts) == 2 && parts[1] == "nil" &&
				bytes.HasSuffix(receiver, []byte("http.")) {
				// net/http/pprof registers on the default mux; a loopback
				// listener serving it is the debugging endpoint beside the
				// server, not the server.
				continue
			}
			listener(loc[0], name+"("+strconv.Quote(value)+")", value)
			continue
		}
		if (name == "Run" || name == "Start" || name == "RunTLS" || name == "StartTLS") && !goAddressLike(content, parts[0]) {
			continue
		}
		address(loc[0], name+"(%s)", parts[0])
	}
	for _, loc := range goNetListenRE.FindAllIndex(content, -1) {
		if commentedAt(content, loc[0], "//") {
			continue
		}
		args, ok := callArguments(content, loc[0]+len("net.Listen"))
		if !ok {
			continue
		}
		if parts := splitArguments(args); len(parts) == 2 {
			address(loc[0], `net.Listen("tcp", %s)`, parts[1])
		}
	}
	if gin {
		if loc := goEmptyRunRE.FindIndex(content); loc != nil && !commentedAt(content, loc[0], "//") {
			// gin's Run() with no address listens on :$PORT, else :8080.
			read := mark(loc[0], 8080, "", "gin Run() follows PORT, else 8080")
			if r.readsPort == nil {
				r.readsPort = &read
			}
			r.fallbacks = append(r.fallbacks, read)
			r.open = append(r.open, read)
		}
	}
	if host := hostFallbackMark(content, goReadsHostRE, file); host != nil && len(r.ports)+len(r.open) > 0 {
		r.hostFallback = host
	}
	return r
}

// goOperationalRoutes reports a main package whose every registered path is
// an operational endpoint — metrics, profiling, health — which makes it a
// worker with a side port rather than a web server.
func goOperationalRoutes(content []byte) (routes int, operational bool) {
	operational = true
	for _, match := range goRegisteredRouteRE.FindAllSubmatchIndex(content, -1) {
		if commentedAt(content, match[0], "//") {
			continue
		}
		route := string(content[match[2]:match[3]])
		// Go 1.22 patterns carry a method: "GET /metrics".
		if space := strings.LastIndexByte(route, ' '); space >= 0 {
			route = route[space+1:]
		}
		routes++
		if !operationalRoute(route) {
			operational = false
		}
	}
	return routes, operational
}

func operationalRoute(route string) bool {
	route = strings.TrimSuffix(strings.ToLower(route), "/")
	for _, prefix := range []string{"/metrics", "/debug", "/healthz", "/health", "/livez", "/readyz", "/ready", "/live"} {
		if route == prefix || strings.HasPrefix(route, prefix+"/") {
			return true
		}
	}
	return false
}

var loopbackLiteralRE = regexp.MustCompile(`"(127\.0\.0\.1|localhost|\[::1\]|::1)"`)

// hostFallbackMark finds a HOST read whose fallback, written just after it,
// is a loopback literal: `env::var("HOST").unwrap_or("127.0.0.1".into())`, or
// `if host == "" { host = "localhost" }`. A HOST read with no such fallback
// may well bind every interface when HOST is unset, so it is not a loopback.
func hostFallbackMark(content []byte, read *regexp.Regexp, file string) *sourceMark {
	loc := read.FindIndex(content)
	if loc == nil || commentedAt(content, loc[0], "//") {
		return nil
	}
	tail := content[loc[1]:min(len(content), loc[1]+200)]
	match := loopbackLiteralRE.FindSubmatch(tail)
	if match == nil {
		return nil
	}
	return &sourceMark{path: file, line: lineOf(content, loc[0]), host: string(match[1]), what: "reads HOST, else " + string(match[1])}
}

// Rust: the served binary's bind. Only the calls that take a listen address
// are read — bind, run, serve and SocketAddr's constructors — since
// String::from("localhost:6379") or a client's new("host:port") is some other
// service's address.
var (
	rustBindCallRE  = regexp.MustCompile(`\b(?:bind|run|serve)\(|\bSocketAddr(?:V4|V6)?::(?:from|new)\(`)
	rustParseAddrRE = regexp.MustCompile(`^&?"([^"]*)"\s*\.parse\b`)
	rustReadsPortRE = regexp.MustCompile(`env::var(?:_os)?\(\s*"PORT"\s*\)|dotenvy::var\(\s*"PORT"\s*\)`)
	rustReadsHostRE = regexp.MustCompile(`env::var(?:_os)?\(\s*"HOST"\s*\)`)
	rustFallbackRE  = regexp.MustCompile(`unwrap_or(?:_else)?\(\s*(?:\|_\|\s*)?(?:String::from\()?(?:"(\d{2,5})"|(\d{2,5}))`)
	rustArrayAddrRE = regexp.MustCompile(`^\(\s*\[\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})\s*\]\s*,\s*(\d{2,5})\s*\)$`)
	rustTupleAddrRE = regexp.MustCompile(`^\(\s*"([^"]*)"\s*,\s*(\d{2,5})\s*\)$`)
	rustFormatRE    = regexp.MustCompile(`^&?format!\(\s*"([^"{}]*):\{`)
)

func scanRustListen(file string, content []byte) listenReport {
	var r listenReport
	mark := func(offset, port int, host, what string) sourceMark {
		return sourceMark{path: file, line: lineOf(content, offset), port: port, host: host, what: what}
	}
	if loc := rustReadsPortRE.FindIndex(content); loc != nil && !commentedAt(content, loc[0], "//") {
		read := mark(loc[0], 0, "", "reads PORT")
		r.readsPort = &read
		tail := content[loc[1]:min(len(content), loc[1]+160)]
		if match := rustFallbackRE.FindSubmatch(tail); match != nil {
			value := string(match[1]) + string(match[2])
			if port := listenPort(value); port > 0 {
				read.port, read.what = port, fmt.Sprintf("PORT, else %d", port)
				r.fallbacks = append(r.fallbacks, read)
			}
		}
	}
	for _, loc := range rustBindCallRE.FindAllIndex(content, -1) {
		if commentedAt(content, loc[0], "//") {
			continue
		}
		args, ok := callArguments(content, loc[1]-1)
		if !ok {
			continue
		}
		args = strings.TrimSpace(args)
		name := strings.TrimSuffix(string(content[loc[0]:loc[1]]), "(")
		host, port := "", 0
		switch {
		case rustArrayAddrRE.MatchString(args):
			match := rustArrayAddrRE.FindStringSubmatch(args)
			host, port = strings.Join(match[1:5], "."), listenPort(match[5])
		case strings.HasPrefix(name, "SocketAddr"):
			// SocketAddr::from(([0, 0, 0, 0], 3000)) is the only form of its
			// constructors that names the address in one literal.
			continue
		case rustParseAddrRE.MatchString(args):
			var valid bool
			host, port, valid = splitHostPort(rustParseAddrRE.FindStringSubmatch(args)[1])
			if !valid {
				continue
			}
		case rustTupleAddrRE.MatchString(args):
			match := rustTupleAddrRE.FindStringSubmatch(args)
			host, port = match[1], listenPort(match[2])
		default:
			literal, ok := stringLiteral(args)
			if ok {
				var valid bool
				host, port, valid = splitHostPort(literal)
				if !valid {
					continue
				}
			} else if match := rustFormatRE.FindStringSubmatch(args); match != nil && name == "bind" {
				host = match[1]
			} else {
				continue
			}
		}
		found := mark(loc[0], port, host, name+"("+boundedCall(args)+")")
		if port > 0 {
			r.ports = append(r.ports, found)
		}
		switch {
		case loopbackHost(host):
			r.loopback = append(r.loopback, found)
		case openHost(host):
			r.open = append(r.open, found)
		}
	}
	if host := hostFallbackMark(content, rustReadsHostRE, file); host != nil {
		r.hostFallback = host
	}
	return r
}

// JVM: Spring, Quarkus, Micronaut, Helidon and Ktor configuration, and the
// listen literals Vert.x, Javalin and Ktor's embedded server write in code.
var (
	jvmPortPlaceholderRE = regexp.MustCompile(`^\$\{\??PORT(?::(\d{2,5}))?\}$|^\$PORT(?::(\d{2,5}))?$`)
	jvmListenRE          = regexp.MustCompile(`\.listen\(\s*(\d{2,5})\s*(?:,\s*"([^"]*)"\s*)?[,)]`)
	jvmStartRE           = regexp.MustCompile(`\.start\(\s*(\d{2,5})\s*\)`)
	jvmEmbeddedRE        = regexp.MustCompile(`embeddedServer\(`)
	jvmNamedPortRE       = regexp.MustCompile(`\bport\s*=\s*(\d{2,5})\b`)
	jvmNamedHostRE       = regexp.MustCompile(`\bhost\s*=\s*"([^"]*)"`)
	jvmReadsPortRE       = regexp.MustCompile(`System\.getenv\(\s*"PORT"\s*\)`)
	hoconPortRE          = regexp.MustCompile(`(?m)^\s*port\s*[=:]\s*(\d{2,5})\s*$`)
	hoconPortEnvRE       = regexp.MustCompile(`(?m)^\s*port\s*[=:]\s*\$\{\??PORT\}\s*$`)
	hoconHostRE          = regexp.MustCompile(`(?m)^\s*host\s*[=:]\s*"?([^"\s]+)"?\s*$`)
)

// jvmPortKeys are the settings that fix the port, in each framework's
// spelling, with the environment variable that overrides each.
var jvmPortKeys = []struct{ key, env string }{
	{"server.port", "SERVER_PORT"}, {"quarkus.http.port", "QUARKUS_HTTP_PORT"}, {"%prod.quarkus.http.port", "QUARKUS_HTTP_PORT"},
	{"micronaut.server.port", "MICRONAUT_SERVER_PORT"}, {"ktor.deployment.port", ""},
}

var jvmHostKeys = []string{"server.address", "quarkus.http.host", "%prod.quarkus.http.host", "micronaut.server.host", "ktor.deployment.host"}

// scanJVMConfig reads a properties, YAML or HOCON configuration file's port
// and host settings.
func scanJVMConfig(file string, content []byte) listenReport {
	var r listenReport
	values := map[string]configValue{}
	switch {
	case strings.HasSuffix(file, ".properties"):
		values = flattenProperties(content)
	case strings.HasSuffix(file, ".yml") || strings.HasSuffix(file, ".yaml"):
		values = flattenSimpleYAML(content)
	case strings.HasSuffix(file, ".conf"):
		if loc := hoconPortEnvRE.FindIndex(content); loc != nil {
			read := sourceMark{path: file, line: lineOf(content, loc[0]), what: "port = ${?PORT}"}
			r.readsPort = &read
		}
		if match := hoconPortRE.FindSubmatchIndex(content); match != nil {
			port := listenPort(string(content[match[2]:match[3]]))
			found := sourceMark{path: file, line: lineOf(content, match[0]), port: port, what: fmt.Sprintf("port = %d", port)}
			if r.readsPort != nil {
				r.fallbacks = append(r.fallbacks, found)
			} else {
				r.configPorts = append(r.configPorts, found)
			}
		}
		if match := hoconHostRE.FindSubmatchIndex(content); match != nil {
			host := string(content[match[2]:match[3]])
			found := sourceMark{path: file, line: lineOf(content, match[0]), host: host, what: "host = " + host}
			if loopbackHost(host) {
				r.loopback = append(r.loopback, found)
			}
		}
		return r
	}
	for _, key := range jvmPortKeys {
		value, ok := values[key.key]
		if !ok {
			continue
		}
		found := sourceMark{path: file, line: value.line, what: key.key + "=" + value.text}
		if match := jvmPortPlaceholderRE.FindStringSubmatch(value.text); match != nil {
			read := found
			if r.readsPort == nil {
				r.readsPort = &read
			}
			if port := listenPort(match[1] + match[2]); port > 0 {
				found.port = port
				r.fallbacks = append(r.fallbacks, found)
			}
			continue
		}
		if port := listenPort(value.text); port > 0 {
			found.port = port
			r.configPorts = append(r.configPorts, found)
		}
	}
	for _, key := range jvmHostKeys {
		if value, ok := values[key]; ok && loopbackHost(value.text) {
			r.loopback = append(r.loopback, sourceMark{path: file, line: value.line, host: value.text, what: key + "=" + value.text})
		}
	}
	return r
}

// scanJVMSource reads the listen literals of the frameworks that set their
// port in code.
func scanJVMSource(file string, content []byte) listenReport {
	var r listenReport
	comment := []string{"//", "/*", "*"}
	mark := func(offset, port int, host, what string) sourceMark {
		return sourceMark{path: file, line: lineOf(content, offset), port: port, host: host, what: what}
	}
	if loc := jvmReadsPortRE.FindIndex(content); loc != nil && !commentedAt(content, loc[0], comment...) {
		read := mark(loc[0], 0, "", "reads PORT")
		r.readsPort = &read
	}
	add := func(found sourceMark) {
		if found.port > 0 {
			r.ports = append(r.ports, found)
		}
		switch {
		case loopbackHost(found.host):
			r.loopback = append(r.loopback, found)
		default:
			r.open = append(r.open, found)
		}
	}
	for _, match := range jvmListenRE.FindAllSubmatchIndex(content, -1) {
		if commentedAt(content, match[0], comment...) {
			continue
		}
		port := listenPort(string(content[match[2]:match[3]]))
		host := ""
		if match[4] >= 0 {
			host = string(content[match[4]:match[5]])
		}
		add(mark(match[0], port, host, fmt.Sprintf("listen(%d)", port)))
	}
	for _, match := range jvmStartRE.FindAllSubmatchIndex(content, -1) {
		if commentedAt(content, match[0], comment...) || !bytes.Contains(content, []byte("Javalin")) {
			continue
		}
		port := listenPort(string(content[match[2]:match[3]]))
		add(mark(match[0], port, "", fmt.Sprintf("start(%d)", port)))
	}
	for _, loc := range jvmEmbeddedRE.FindAllIndex(content, -1) {
		if commentedAt(content, loc[0], comment...) {
			continue
		}
		args, ok := callArguments(content, loc[1]-1)
		if !ok {
			continue
		}
		found := mark(loc[0], 0, "", "embeddedServer")
		if match := jvmNamedPortRE.FindStringSubmatch(args); match != nil {
			found.port = listenPort(match[1])
			found.what = fmt.Sprintf("embeddedServer(port = %d)", found.port)
		}
		if match := jvmNamedHostRE.FindStringSubmatch(args); match != nil {
			found.host = match[1]
		}
		add(found)
	}
	return r
}

type configValue struct {
	text string
	line int
}

// flattenProperties reads `key=value`, `key: value` and `key value` lines.
func flattenProperties(content []byte) map[string]configValue {
	values := map[string]configValue{}
	for index, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		cut := strings.IndexAny(line, "=: \t")
		if cut <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:cut])
		value := strings.TrimSpace(strings.TrimLeft(line[cut:], "=: \t"))
		if _, seen := values[key]; !seen {
			values[key] = configValue{text: value, line: index + 1}
		}
	}
	return values
}

// flattenSimpleYAML reads the nested mappings of a YAML file into dotted
// keys. It is not a YAML parser: lists, anchors and flow style yield
// nothing, and the first document to set a key keeps it.
func flattenSimpleYAML(content []byte) map[string]configValue {
	values := map[string]configValue{}
	type level struct {
		indent int
		key    string
	}
	var stack []level
	for index, raw := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "---" {
			stack = nil
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "- ") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		key, value, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}
		key = strings.Trim(strings.TrimSpace(key), `'"`)
		if comment := strings.Index(value, " #"); comment >= 0 {
			value = value[:comment]
		}
		value = strings.Trim(strings.TrimSpace(value), `'"`)
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		parts := make([]string, 0, len(stack)+1)
		for _, parent := range stack {
			parts = append(parts, parent.key)
		}
		parts = append(parts, key)
		if value == "" {
			stack = append(stack, level{indent: indent, key: key})
			continue
		}
		dotted := strings.Join(parts, ".")
		if _, seen := values[dotted]; !seen {
			values[dotted] = configValue{text: value, line: index + 1}
		}
	}
	return values
}

// .NET: appsettings Kestrel endpoints and Urls, and the URLs Program.cs
// passes in code, which win over both.
var (
	dotnetURLCallRE   = regexp.MustCompile(`\.(?:Run|RunAsync|UseUrls)\(\s*"(https?://[^"]+)"`)
	dotnetURLAddRE    = regexp.MustCompile(`\.Urls\.Add\(\s*"(https?://[^"]+)"`)
	dotnetLocalhostRE = regexp.MustCompile(`\.ListenLocalhost\(\s*(\d{2,5})`)
	dotnetAnyIPRE     = regexp.MustCompile(`\.ListenAnyIP\(\s*(\d{2,5})`)
	dotnetListenIPRE  = regexp.MustCompile(`\.Listen\(\s*IPAddress\.(Loopback|IPv6Loopback|Any|IPv6Any)\s*,\s*(\d{2,5})`)
	dotnetReadsPortRE = regexp.MustCompile(`GetEnvironmentVariable\(\s*"PORT"\s*\)`)
)

func dotnetURLListener(file string, line int, url, what string) (sourceMark, bool) {
	url, _, _ = strings.Cut(url, ";")
	scheme := strings.Index(url, "://")
	if scheme < 0 {
		return sourceMark{}, false
	}
	rest := url[scheme+3:]
	rest = strings.TrimSuffix(strings.SplitN(rest, "/", 2)[0], "/")
	host, port, ok := splitHostPort(rest)
	if !ok {
		return sourceMark{}, false
	}
	return sourceMark{path: file, line: line, port: port, host: host, what: what}, true
}

func (r *listenReport) addListener(found sourceMark) {
	if found.port > 0 {
		r.ports = append(r.ports, found)
	}
	switch {
	case loopbackHost(found.host):
		r.loopback = append(r.loopback, found)
	case openHost(found.host):
		r.open = append(r.open, found)
	}
}

func scanDotnetSource(file string, content []byte) listenReport {
	var r listenReport
	comment := []string{"//", "/*", "*"}
	if loc := dotnetReadsPortRE.FindIndex(content); loc != nil && !commentedAt(content, loc[0], comment...) {
		read := sourceMark{path: file, line: lineOf(content, loc[0]), what: "reads PORT"}
		r.readsPort = &read
	}
	for _, re := range []*regexp.Regexp{dotnetURLCallRE, dotnetURLAddRE} {
		for _, match := range re.FindAllSubmatchIndex(content, -1) {
			if commentedAt(content, match[0], comment...) {
				continue
			}
			url := string(content[match[2]:match[3]])
			if found, ok := dotnetURLListener(file, lineOf(content, match[0]), url, strconv.Quote(url)); ok {
				r.addListener(found)
			}
		}
	}
	for _, match := range dotnetLocalhostRE.FindAllSubmatchIndex(content, -1) {
		port := listenPort(string(content[match[2]:match[3]]))
		r.addListener(sourceMark{path: file, line: lineOf(content, match[0]), port: port, host: "localhost", what: fmt.Sprintf("ListenLocalhost(%d)", port)})
	}
	for _, match := range dotnetAnyIPRE.FindAllSubmatchIndex(content, -1) {
		port := listenPort(string(content[match[2]:match[3]]))
		r.addListener(sourceMark{path: file, line: lineOf(content, match[0]), port: port, host: "0.0.0.0", what: fmt.Sprintf("ListenAnyIP(%d)", port)})
	}
	for _, match := range dotnetListenIPRE.FindAllSubmatchIndex(content, -1) {
		port := listenPort(string(content[match[4]:match[5]]))
		host := "0.0.0.0"
		if strings.HasSuffix(string(content[match[2]:match[3]]), "Loopback") {
			host = "127.0.0.1"
		}
		r.addListener(sourceMark{path: file, line: lineOf(content, match[0]), port: port, host: host, what: fmt.Sprintf("Listen(IPAddress.%s, %d)", content[match[2]:match[3]], port)})
	}
	return r
}

// jsonUnmarshalLenient decodes JSON that may carry the comments and trailing
// commas .NET's configuration reader accepts.
func jsonUnmarshalLenient(content []byte, target any) error {
	if err := json.Unmarshal(content, target); err == nil {
		return nil
	}
	return json.Unmarshal(denoJSONWithoutComments(content), target)
}

// kestrelSettings is what an appsettings file says about where Kestrel
// listens: the named endpoints and the Urls setting.
type kestrelSettings struct {
	endpoints map[string]string
	urls      string
}

var kestrelEndpointNameRE = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// readKestrelSettings merges appsettings.json with the Production file that
// overrides it, which is the environment a container runs in.
func readKestrelSettings(read func(name string) ([]byte, bool)) kestrelSettings {
	settings := kestrelSettings{endpoints: map[string]string{}}
	for _, name := range []string{"appsettings.json", "appsettings.Production.json"} {
		content, ok := read(name)
		if !ok {
			continue
		}
		parsed := parseKestrelSettings(content)
		if parsed.urls != "" {
			settings.urls = parsed.urls
		}
		for endpoint, url := range parsed.endpoints {
			settings.endpoints[endpoint] = url
		}
	}
	return settings
}

func parseKestrelSettings(content []byte) kestrelSettings {
	settings := kestrelSettings{endpoints: map[string]string{}}
	var document map[string]any
	if jsonUnmarshalLenient(content, &document) != nil {
		return settings
	}
	lookup := func(object map[string]any, key string) (any, bool) {
		for name, value := range object {
			if strings.EqualFold(name, key) {
				return value, true
			}
		}
		return nil, false
	}
	if urls, ok := lookup(document, "Urls"); ok {
		settings.urls, _ = urls.(string)
	}
	kestrel, _ := lookup(document, "Kestrel")
	kestrelObject, _ := kestrel.(map[string]any)
	endpoints, _ := lookup(kestrelObject, "Endpoints")
	endpointObject, _ := endpoints.(map[string]any)
	for name, value := range endpointObject {
		object, _ := value.(map[string]any)
		url, _ := lookup(object, "Url")
		if text, ok := url.(string); ok && kestrelEndpointNameRE.MatchString(name) {
			settings.endpoints[name] = text
		}
	}
	return settings
}

// Python: the calls a served script listens with, and a gunicorn
// configuration file's bind.
var (
	pyReadsPortRE     = regexp.MustCompile(`os\.environ(?:\.get\(\s*|\[\s*)['"]PORT['"]|os\.getenv\(\s*['"]PORT['"]`)
	pyRunCallRE       = regexp.MustCompile(`\b(?:app|application|server|socketio|api)\.run\(`)
	pyUvicornRunRE    = regexp.MustCompile(`\buvicorn\.run\(`)
	pyKwargHostRE     = regexp.MustCompile(`\bhost\s*=\s*['"]([^'"]*)['"]`)
	pyKwargHostKeyRE  = regexp.MustCompile(`\bhost\s*=`)
	pyKwargPortRE     = regexp.MustCompile(`\bport\s*=\s*(\d{2,5})\b`)
	pyKwargPortKeyRE  = regexp.MustCompile(`\bport\s*=`)
	pyKwargPortEnvRE  = regexp.MustCompile(`\bport\s*=\s*int\(\s*os\.(?:environ\.get|getenv)\(\s*['"]PORT['"]\s*(?:,\s*['"]?(\d{2,5}))?`)
	pyFlaskImportRE   = regexp.MustCompile(`(?m)^\s*(?:from\s+flask(?:_socketio)?\s+import|import\s+flask)\b`)
	pyGunicornBindRE  = regexp.MustCompile(`(?m)^\s*bind\s*=\s*\[?\s*[fr]?['"]([^'"]+)['"]`)
	pyGunicornPortEnv = regexp.MustCompile(`(?m)^\s*bind\s*=.*\bPORT\b`)
)

func scanPythonListen(file string, content []byte) listenReport {
	var r listenReport
	mark := func(offset, port int, host, what string) sourceMark {
		return sourceMark{path: file, line: lineOf(content, offset), port: port, host: host, what: what}
	}
	if loc := pyReadsPortRE.FindIndex(content); loc != nil && !commentedAt(content, loc[0], "#") {
		read := mark(loc[0], 0, "", "reads PORT")
		r.readsPort = &read
	}
	// calls reads each run call's host and port. Neither app.run, socketio.run
	// nor uvicorn.run reads PORT: given no port they listen on their own
	// default, which is then a port the code fixes.
	calls := func(re *regexp.Regexp, applies bool, defaultPort int) {
		if !applies {
			return
		}
		for _, loc := range re.FindAllIndex(content, -1) {
			if commentedAt(content, loc[0], "#") {
				continue
			}
			args, ok := callArguments(content, loc[1]-1)
			if !ok {
				continue
			}
			name := strings.TrimSuffix(string(content[loc[0]:loc[1]]), "(")
			found := mark(loc[0], 0, "", name+"("+boundedCall(args)+")")
			// Flask's app.run(host, port) also takes both positionally;
			// socketio.run and uvicorn.run take the application first.
			var positional []string
			if name != "socketio.run" && name != "uvicorn.run" {
				for _, part := range splitArguments(args) {
					if strings.Contains(part, "=") {
						break
					}
					positional = append(positional, part)
				}
			}
			host, hostKnown, hostGiven := "", false, pyKwargHostKeyRE.MatchString(args)
			if match := pyKwargHostRE.FindStringSubmatch(args); match != nil {
				host, hostKnown = match[1], true
			} else if literal, ok := stringLiteral(firstOf(positional)); ok {
				host, hostKnown, hostGiven = literal, true, true
			} else if len(positional) > 0 {
				hostGiven = true
			}
			found.host = host
			switch match := pyKwargPortRE.FindStringSubmatch(args); {
			case match != nil:
				found.port = listenPort(match[1])
				r.ports = append(r.ports, found)
			case len(positional) > 1 && listenPort(positional[1]) > 0:
				found.port = listenPort(positional[1])
				r.ports = append(r.ports, found)
			case pyKwargPortEnvRE.MatchString(args):
				match := pyKwargPortEnvRE.FindStringSubmatch(args)
				if r.readsPort == nil {
					read := found
					r.readsPort = &read
				}
				if port := listenPort(match[1]); port > 0 {
					fallback := found
					fallback.port = port
					r.fallbacks = append(r.fallbacks, fallback)
				}
			case !pyKwargPortKeyRE.MatchString(args) && len(positional) < 2:
				fixed := found
				fixed.port, fixed.what = defaultPort, fmt.Sprintf("%s(%s), default port %d", name, boundedCall(args), defaultPort)
				r.ports = append(r.ports, fixed)
			}
			switch {
			case hostKnown && loopbackHost(host):
				r.loopback = append(r.loopback, found)
			case hostKnown && openHost(host):
				r.open = append(r.open, found)
			case hostGiven && !hostKnown:
				// A host read from a variable may well be every interface.
				r.open = append(r.open, found)
			case !hostGiven:
				// Flask's app.run and uvicorn.run both bind 127.0.0.1 when
				// given no host; neither reads one from the environment.
				found.host = "127.0.0.1"
				r.defaults = append(r.defaults, found)
			}
		}
	}
	calls(pyRunCallRE, pyFlaskImportRE.Match(content), 5000)
	calls(pyUvicornRunRE, true, 8000)
	return r
}

func firstOf(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// scanGunicornConfig reads a gunicorn configuration file's bind, which
// outranks $PORT when no --bind is given on the command line.
func scanGunicornConfig(file string, content []byte) listenReport {
	var r listenReport
	if loc := pyGunicornPortEnv.FindIndex(content); loc != nil {
		read := sourceMark{path: file, line: lineOf(content, loc[0]), what: "bind reads PORT"}
		r.readsPort = &read
		r.open = append(r.open, read)
		return r
	}
	match := pyGunicornBindRE.FindSubmatchIndex(content)
	if match == nil {
		return r
	}
	bind := string(content[match[2]:match[3]])
	host, port, ok := splitHostPort(bind)
	if !ok {
		return r
	}
	found := sourceMark{path: file, line: lineOf(content, match[0]), port: port, host: host, what: "bind = " + strconv.Quote(bind)}
	r.addListener(found)
	return r
}

// Deno: the served file's Deno.serve or Oak listen, read by the JavaScript
// scanner, since both are object literals with a port and a hostname.

// Dockerfile and Elixir: the PORT a Dockerfile or a Phoenix runtime config
// defaults to when the image declares no EXPOSE.
var (
	dockerfilePortEnvRE = regexp.MustCompile(`(?i)^(?:ENV|ARG)\s+PORT(?:\s*=\s*|\s+)["']?(\d{2,5})["']?\s*$`)
	elixirPortRE        = regexp.MustCompile(`System\.get_env\(\s*"PORT"\s*\)\s*\|\|\s*"(\d{2,5})"`)
)

// dockerfileEnvPort reads the final stage's `ENV PORT=N` or `ARG PORT=N`.
func dockerfileEnvPort(content []byte) int {
	port := 0
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(strings.ToUpper(line), "FROM ") {
			port = 0
			continue
		}
		if match := dockerfilePortEnvRE.FindStringSubmatch(line); match != nil {
			port = listenPort(match[1])
		}
	}
	return port
}

// Start commands: the server segment's flags, environment prefix and the
// defaults of the servers that bind loopback unless told otherwise.

// commandListen is what a start command says about where its server
// listens.
type commandListen struct {
	// segment is the server command itself, after scripts were followed.
	segment string
	tool    string
	// port is a literal the command fixes; followsPort that it passes
	// $PORT, with fallback when written ${PORT:-N}.
	port        int
	followsPort bool
	fallback    int
	// host is a bind address the command names.
	host string
	// defaultHost is the loopback a tool binds when given no host, and
	// hostEnv the variable that tool reads its host from instead.
	defaultHost string
	hostEnv     string
	defaultPort int
	// devServer names a development server serving production traffic.
	devServer string
	// configFile is the gunicorn configuration the command loads.
	configFile string
}

var (
	envAssignmentRE = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
	portReferenceRE = regexp.MustCompile(`^\$\{?PORT(?::?-(\d{2,5}))?\}?$`)
	scriptRunRE     = regexp.MustCompile(`^(?:npm|pnpm|yarn|bun)\s+(?:run\s+)?([A-Za-z0-9:_.\-]+)(?:\s+--)?(.*)$`)
)

// portFlagTools are the servers whose -p is their port, rather than a
// project or a profile.
var portFlagTools = map[string]bool{
	"next": true, "nuxt": true, "nuxi": true, "serve": true, "http-server": true, "astro": true,
	"daphne": true, "vite": true, "sirv": true, "rails": true, "puma": true, "rackup": true, "unicorn": true,
}

// parseCommandListen reads a start command, following package scripts
// through the manifest's own script table.
func parseCommandListen(command string, scripts map[string]string) commandListen {
	return parseCommandListenDepth(command, scripts, 0)
}

func parseCommandListenDepth(command string, scripts map[string]string, depth int) commandListen {
	segments := strings.Split(command, "&&")
	segment := strings.TrimSpace(segments[len(segments)-1])
	if depth < 3 && scripts != nil {
		name, extra := "", ""
		if match := scriptRunRE.FindStringSubmatch(segment); match != nil && !strings.HasPrefix(match[1], "exec") {
			name, extra = match[1], strings.TrimSpace(match[2])
		}
		if name == "start" || (name != "" && scripts[name] != "") {
			if body := scripts[name]; body != "" {
				if extra != "" {
					body += " " + extra
				}
				return parseCommandListenDepth(body, scripts, depth+1)
			}
		}
	}
	facts := commandListen{segment: segment}
	fields := strings.Fields(segment)
	// The environment prefix and the launchers in front of the server.
	for len(fields) > 0 {
		head := fields[0]
		if match := envAssignmentRE.FindStringSubmatch(head); match != nil {
			value := strings.Trim(match[2], `'"`)
			switch match[1] {
			case "PORT":
				facts.setPort(value)
			case "HOST", "HOSTNAME":
				facts.host = value
			}
			fields = fields[1:]
			continue
		}
		switch head {
		case "exec", "env", "cross-env", "npx", "bunx", "uv", "poetry", "pipenv", "pdm", "hatch":
			fields = fields[1:]
			if len(fields) > 0 && fields[0] == "run" && head != "npx" {
				fields = fields[1:]
			}
			continue
		case "pnpm", "yarn":
			if len(fields) > 1 && (fields[1] == "exec" || fields[1] == "dlx") {
				fields = fields[2:]
				continue
			}
		case "bundle":
			if len(fields) > 1 && fields[1] == "exec" {
				fields = fields[2:]
				continue
			}
		}
		break
	}
	if len(fields) == 0 {
		return facts
	}
	tool := path.Base(fields[0])
	args := fields[1:]
	if (tool == "python" || tool == "python3") && len(args) > 1 && args[0] == "-m" {
		tool, args = args[1], args[2:]
	} else if (tool == "python" || tool == "python3") && len(args) > 0 {
		tool, args = path.Base(args[0]), args[1:]
	}
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
	}
	facts.tool = tool
	if sub != "" && (tool == "vite" || tool == "astro" || tool == "next" || tool == "nuxt" || tool == "nuxi" ||
		tool == "flask" || tool == "fastapi" || tool == "manage.py" || tool == "ng") {
		facts.tool = tool + " " + sub
	}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		name, value, inline := strings.Cut(arg, "=")
		next := func() string {
			if inline {
				return value
			}
			if index+1 < len(args) && !strings.HasPrefix(args[index+1], "-") {
				index++
				return args[index]
			}
			return ""
		}
		switch {
		case name == "--port" || name == "--server.port" || name == "--listen-port":
			facts.setPort(next())
		case name == "-p" && portFlagTools[tool]:
			facts.setPort(next())
		case (name == "-l" || name == "--listen") && (tool == "serve" || tool == "sirv"):
			listen := next()
			if host, port, ok := splitHostPort(strings.TrimPrefix(listen, "tcp://")); ok {
				facts.port, facts.host = port, host
			} else {
				facts.setPort(listen)
			}
		case strings.HasPrefix(arg, "-Dserver.port="):
			facts.setPort(strings.TrimPrefix(arg, "-Dserver.port="))
		case strings.HasPrefix(arg, "-Dhttp.port="):
			// Play's start script takes its port as a system property.
			facts.setPort(strings.TrimPrefix(arg, "-Dhttp.port="))
		case name == "--bind" || name == "-b":
			bind := next()
			// `0.0.0.0:${PORT:-8000}` splits at the colon before the
			// expansion, not the one inside it.
			head := bind
			if dollar := strings.Index(bind, "$"); dollar >= 0 {
				head = bind[:dollar]
			}
			if colon := strings.LastIndex(head, ":"); colon >= 0 {
				facts.host = bind[:colon]
				facts.setPort(bind[colon+1:])
			} else if tool != "gunicorn" && tool != "hypercorn" {
				facts.host = bind
			}
		case name == "--host" || name == "--hostname" || name == "--server.address" || name == "--binding" ||
			(name == "-H" && (tool == "next" || tool == "nuxt")) || (name == "-a" && (tool == "http-server" || tool == "fastify")):
			facts.host = next()
			if facts.host == "" {
				// A bare `--host` (vite) means every interface.
				facts.host = "0.0.0.0"
			}
		case name == "--urls":
			if found, ok := dotnetURLListener("", 0, next(), ""); ok {
				facts.host, facts.port = found.host, found.port
			}
		case name == "-c" || name == "--config":
			if tool == "gunicorn" {
				facts.configFile = strings.TrimPrefix(next(), "python:")
			}
		case name == "--reload" && (tool == "uvicorn" || tool == "gunicorn" || tool == "hypercorn"):
			facts.devServer = tool + " --reload"
		case tool == "manage.py" && sub == "runserver" && arg != "runserver" && !strings.HasPrefix(arg, "-"):
			if host, port, ok := splitHostPort(arg); ok {
				facts.host, facts.port = host, port
				if host == "" {
					facts.host = "127.0.0.1"
				}
			} else {
				facts.setPort(arg)
			}
		case tool == "php" && name == "-S":
			if host, port, ok := splitHostPort(next()); ok {
				facts.host, facts.port = host, port
			}
		}
	}
	// What each server does when the command leaves the choice to it.
	switch facts.tool {
	case "vite", "vite dev", "vite serve":
		facts.defaultHost, facts.defaultPort, facts.devServer = "localhost", 5173, "vite"
	case "vite preview":
		facts.defaultHost, facts.defaultPort = "localhost", 4173
	case "astro preview":
		facts.defaultHost, facts.defaultPort = "localhost", 4321
	case "astro dev":
		facts.defaultHost, facts.defaultPort, facts.devServer = "localhost", 4321, "astro dev"
	case "ng serve":
		facts.defaultHost, facts.defaultPort, facts.devServer = "localhost", 4200, "ng serve"
	case "nuxt start", "nuxt":
		facts.defaultHost, facts.hostEnv = "localhost", "HOST"
	case "flask run":
		facts.defaultHost, facts.defaultPort, facts.hostEnv, facts.devServer = "127.0.0.1", 5000, "FLASK_RUN_HOST", "flask run"
	case "fastapi dev":
		facts.defaultHost, facts.defaultPort, facts.devServer = "127.0.0.1", 8000, "fastapi dev"
	case "fastapi run":
		facts.defaultPort = 8000
	case "manage.py runserver":
		facts.defaultHost, facts.defaultPort, facts.devServer = "127.0.0.1", 8000, "manage.py runserver"
	case "uvicorn":
		facts.defaultHost, facts.defaultPort, facts.hostEnv = "127.0.0.1", 8000, "UVICORN_HOST"
	case "hypercorn", "daphne":
		facts.defaultHost, facts.defaultPort = "127.0.0.1", 8000
	case "streamlit":
		facts.defaultPort = 8501
	}
	return facts
}

func (c *commandListen) setPort(value string) {
	value = strings.Trim(strings.TrimSpace(value), `'"`)
	if match := portReferenceRE.FindStringSubmatch(value); match != nil {
		c.followsPort = true
		c.fallback = listenPort(match[1])
		return
	}
	if port := listenPort(value); port > 0 {
		c.port = port
	}
}

// boundHost is the host the command's server ends up on: the one it names,
// else its default; recipeEnv reports the hosts the recipe's environment
// already moves.
func (c commandListen) boundHost(recipeEnv map[string]bool) (string, string) {
	if c.host != "" {
		return c.host, ""
	}
	if c.defaultHost != "" && c.hostEnv != "" && recipeEnv[c.hostEnv] {
		return "", c.hostEnv
	}
	return c.defaultHost, ""
}
