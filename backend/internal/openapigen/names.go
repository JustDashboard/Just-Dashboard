package openapigen

import (
	"strings"
	"unicode"
)

// initialisms are the words Go spells in capitals (Effective Go's names, and
// the ones this API uses), so a property projectId is ProjectID, not ProjectId.
var initialisms = map[string]string{
	"acl": "ACL", "api": "API", "cpu": "CPU", "csrf": "CSRF", "css": "CSS", "dns": "DNS", "html": "HTML",
	"http": "HTTP", "https": "HTTPS", "id": "ID", "ids": "IDs", "ip": "IP", "json": "JSON", "jwt": "JWT",
	"sql": "SQL", "ssh": "SSH", "tcp": "TCP", "tls": "TLS", "ttl": "TTL", "udp": "UDP", "uid": "UID",
	"uri": "URI", "url": "URL", "urls": "URLs", "utc": "UTC", "uuid": "UUID", "xml": "XML", "yaml": "YAML",
}

// words splits a name into its words: at anything that is not a letter or a
// digit, where a lower-case letter or a digit meets a capital, and before
// the last capital of a run followed by a lower-case letter (HTTPStatus is
// HTTP and Status).
func words(name string) []string {
	var out []string
	var cur []rune
	runes := []rune(name)
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = nil
		}
	}
	for i, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if len(cur) > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return out
}

// exported is a name as an exported Go identifier: each word capitalised,
// initialisms in capitals. A name that starts with a digit is given an X.
func exported(name string) string {
	var sb strings.Builder
	for _, w := range words(name) {
		lower := strings.ToLower(w)
		if up, ok := initialisms[lower]; ok {
			sb.WriteString(up)
			continue
		}
		r := []rune(w)
		sb.WriteString(strings.ToUpper(string(r[0])) + string(r[1:]))
	}
	out := sb.String()
	if out == "" {
		return "X"
	}
	if unicode.IsDigit([]rune(out)[0]) {
		out = "X" + out
	}
	return out
}

// unexported is a name as an unexported Go identifier, for a parameter: the
// first word in lower case, the rest as exported spells them.
func unexported(name string) string {
	ws := words(name)
	if len(ws) == 0 {
		return "x"
	}
	first := strings.ToLower(ws[0])
	out := first + exported(strings.Join(ws[1:], " "))
	if len(ws) == 1 {
		out = first
	}
	if unicode.IsDigit([]rune(out)[0]) {
		out = "x" + out
	}
	return out
}

// camel is a name in lower camel case for TypeScript: the first word in lower
// case and each later one capitalised, with no initialisms.
func camel(name string) string {
	ws := words(name)
	if len(ws) == 0 {
		return "x"
	}
	var sb strings.Builder
	for i, w := range ws {
		lower := strings.ToLower(w)
		if i == 0 {
			sb.WriteString(lower)
			continue
		}
		sb.WriteString(strings.ToUpper(lower[:1]) + lower[1:])
	}
	return sb.String()
}

// Snake is an operationId as a tool name: lower-case words joined by
// underscores (deploymentRunCreate is deployment_run_create).
func Snake(name string) string {
	ws := words(name)
	for i, w := range ws {
		ws[i] = strings.ToLower(w)
	}
	return strings.Join(ws, "_")
}

// goKeywords and the names the generated code itself uses for its locals,
// which a parameter must not take.
var goReserved = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true, "default": true, "defer": true,
	"else": true, "fallthrough": true, "for": true, "func": true, "go": true, "goto": true, "if": true,
	"import": true, "interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
	"ctx": true, "body": true, "params": true, "opts": true, "c": true, "out": true, "query": true, "path": true,
	"err": true, "copy": true, "len": true, "string": true, "error": true, "any": true, "new": true, "make": true,
}

// tsReserved are JavaScript's reserved words and the generated functions'
// own locals.
var tsReserved = map[string]bool{
	"break": true, "case": true, "catch": true, "class": true, "const": true, "continue": true, "debugger": true,
	"default": true, "delete": true, "do": true, "else": true, "enum": true, "export": true, "extends": true,
	"false": true, "finally": true, "for": true, "function": true, "if": true, "import": true, "in": true,
	"instanceof": true, "new": true, "null": true, "return": true, "super": true, "switch": true, "this": true,
	"throw": true, "true": true, "try": true, "typeof": true, "var": true, "void": true, "while": true,
	"with": true, "yield": true, "let": true, "static": true, "implements": true, "interface": true,
	"package": true, "private": true, "protected": true, "public": true, "await": true,
	"client": true, "body": true, "query": true, "options": true,
}

// identifier reports whether a property name can be written bare in a
// TypeScript interface.
func identifier(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_' || r == '$' || unicode.IsLetter(r):
		case i > 0 && unicode.IsDigit(r):
		default:
			return false
		}
	}
	return true
}
