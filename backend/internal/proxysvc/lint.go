package proxysvc

import (
	"fmt"
	"net"
	"strings"
)

// LintFinding is one thing the configuration nginx loads does that is legal
// but probably not meant, placed at the line that does it.
type LintFinding struct {
	ID   string `json:"id"`
	Rule string `json:"rule"`
	// Level is "critical", "warning" or "notice".
	Level  string `json:"level"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	File   string `json:"file"`
	Line   int    `json:"line"`
}

// lintLevel is one block as the linter walks it: the block's own directive
// (nil for the main file's top) and the directives written directly in it.
type lintLevel struct {
	block *Directive
	list  []Directive
}

type linter struct {
	upstreams map[string]bool
	findings  []LintFinding
}

// Lint reads a tree NginxTree built for the mistakes nginx accepts without a
// word, or with a warning only in its error log. It reads what the tree holds,
// so a file EffectiveConfig leaves out — certbot's options, a password file —
// is not linted.
func Lint(tree []Directive) []LintFinding {
	l := &linter{upstreams: map[string]bool{}}
	var collect func([]Directive)
	collect = func(list []Directive) {
		for _, d := range list {
			if d.Name == "upstream" && len(d.Args) == 1 {
				l.upstreams[strings.ToLower(d.Args[0])] = true
			}
			if d.Block != nil {
				collect(d.Block)
			}
		}
	}
	collect(tree)
	l.walk([]lintLevel{{list: tree}})
	l.duplicateNames(tree)
	if l.findings == nil {
		return []LintFinding{}
	}
	return l.findings
}

func (l *linter) add(rule, level string, d Directive, title, detail string) {
	l.findings = append(l.findings, LintFinding{
		ID:   fmt.Sprintf("%s:%s:%d", rule, d.File, d.Line),
		Rule: rule, Level: level, Title: title, Detail: detail, File: d.File, Line: d.Line,
	})
}

func (l *linter) walk(levels []lintLevel) {
	here := levels[len(levels)-1]
	l.addHeaders(levels)
	l.accessRules(here)
	for i := range here.list {
		d := here.list[i]
		switch d.Name {
		case "location":
			l.alias(d)
			l.stubStatus(levels, d)
		case "proxy_pass":
			l.proxyPass(levels, d)
		case "return":
			l.returnBypass(levels, d)
		case "listen":
			for _, a := range d.Args[min(1, len(d.Args)):] {
				if a == "http2" {
					l.add("listen-http2", "notice", d, "listen … http2 is deprecated",
						"nginx 1.25.1 moved HTTP/2 to its own directive and warns about this parameter on every test. Write http2 on; in the server block and drop http2 from listen.")
					break
				}
			}
		case "ssl_protocols":
			for _, a := range d.Args {
				if a == "SSLv2" || a == "SSLv3" || a == "TLSv1" || a == "TLSv1.1" {
					l.add("old-tls", "warning", d, a+" is enabled",
						"TLS 1.0 and 1.1 are deprecated (RFC 8996) and SSL is broken; no current browser needs them. ssl_protocols TLSv1.2 TLSv1.3; is enough.")
					break
				}
			}
		case "if":
			l.unsafeIf(here, d)
		}
		// A map, geo or types block holds entries rather than directives, and
		// a map read from $http_host is how a host is matched on purpose.
		if tableBlocks[d.Name] {
			continue
		}
		for _, a := range d.Args {
			if strings.Contains(a, "$http_host") {
				l.add("http-host", "notice", d, "$http_host passes the Host header as the client sent it",
					"It is empty from an HTTP/1.0 client and carries whatever name and port the client wrote. $host is the name the request matched, lower-cased and without the port.")
				break
			}
		}
		if d.Block != nil {
			l.walk(append(levels[:len(levels):len(levels)], lintLevel{block: &here.list[i], list: d.Block}))
		}
	}
}

var tableBlocks = map[string]bool{
	"map": true, "geo": true, "split_clients": true, "types": true, "charset_map": true, "match": true,
}

func blockName(lv lintLevel) string {
	if lv.block == nil {
		return ""
	}
	return lv.block.Name
}

// addHeaders: a block that sets any add_header inherits none from outside,
// so the outer ones it does not repeat are not sent from here.
func (l *linter) addHeaders(levels []lintLevel) {
	here := levels[len(levels)-1]
	own := map[string]bool{}
	var first *Directive
	for i, d := range here.list {
		if d.Name == "add_header" && len(d.Args) > 0 {
			own[strings.ToLower(d.Args[0])] = true
			if first == nil {
				first = &here.list[i]
			}
		}
	}
	if first == nil {
		return
	}
	for i := len(levels) - 2; i >= 0; i-- {
		var lost []string
		var at Directive
		for _, d := range levels[i].list {
			if d.Name == "add_header" && len(d.Args) > 0 {
				at = d
				if !own[strings.ToLower(d.Args[0])] {
					lost = append(lost, d.Args[0])
				}
			}
		}
		if at.Name == "" {
			continue
		}
		if len(lost) > 0 {
			l.add("add-header", "warning", *first, "add_header here drops "+strings.Join(lost, ", "),
				fmt.Sprintf("A block that sets any add_header inherits none from outside it, so %s from %s:%d is not sent for requests handled here. Repeat it in this block.",
					strings.Join(lost, ", "), at.File, at.Line))
		}
		return
	}
}

// accessRules: allow lines with no deny all after them let everyone else in,
// since nginx allows what no rule matched.
func (l *linter) accessRules(here lintLevel) {
	var allow *Directive
	for i, d := range here.list {
		if d.Name == "allow" && allow == nil {
			allow = &here.list[i]
		}
		if d.Name == "deny" && len(d.Args) == 1 && d.Args[0] == "all" {
			return
		}
	}
	if allow != nil {
		l.add("allow-without-deny", "warning", *allow, "allow without deny all",
			"nginx lets through every address no rule matched, so these allow lines restrict nothing. End the list with deny all;.")
	}
}

// guarded is the access control in force at the innermost level: its
// allow/deny list (inherited only by a level with none), an auth_basic or an
// auth_request that is not off.
func guarded(levels []lintLevel) (*Directive, bool) {
	accessDecided, authDecided := false, false
	for i := len(levels) - 1; i >= 0; i-- {
		list := levels[i].list
		if !accessDecided {
			for j, d := range list {
				if d.Name == "allow" || d.Name == "deny" {
					accessDecided = true
				}
				if d.Name == "deny" {
					return &list[j], true
				}
			}
		}
		if !authDecided {
			for j, d := range list {
				if (d.Name == "auth_basic" || d.Name == "auth_request") && len(d.Args) > 0 {
					authDecided = true
					if d.Args[0] != "off" {
						return &list[j], true
					}
				}
			}
		}
	}
	return nil, false
}

// returnBypass: return answers in the rewrite phase, before allow/deny and
// auth_basic are checked.
func (l *linter) returnBypass(levels []lintLevel, d Directive) {
	guard, ok := guarded(levels)
	if !ok {
		return
	}
	l.add("return-before-access", "warning", d, "return answers before the access rules",
		fmt.Sprintf("return runs in nginx's rewrite phase, before %s at %s:%d is checked, so everyone gets this response. Put the rule in a block this return is not in, or answer with try_files or a proxied upstream instead.",
			guard.Name, guard.File, guard.Line))
}

// stubStatus: the status page answers anyone who reaches a server that
// neither listens on loopback only nor guards the location.
func (l *linter) stubStatus(levels []lintLevel, loc Directive) {
	var at *Directive
	for i, d := range loc.Block {
		if d.Name == "stub_status" {
			at = &loc.Block[i]
		}
	}
	if at == nil {
		return
	}
	if _, ok := guarded(append(levels[:len(levels):len(levels)], lintLevel{block: &loc, list: loc.Block})); ok {
		return
	}
	for i := len(levels) - 1; i >= 0; i-- {
		if blockName(levels[i]) == "server" && loopbackOnly(levels[i].list) {
			return
		}
	}
	l.add("stub-status", "warning", *at, "stub_status is open to anyone",
		"Connection counts and request rates answer every client that reaches this server. Add allow 127.0.0.1; deny all; to the location, or serve it on a loopback listen.")
}

// loopbackOnly reports whether every listen of a server is on loopback or a
// socket file. A server with no listen listens on *:80.
func loopbackOnly(list []Directive) bool {
	listens := 0
	for _, d := range list {
		if d.Name != "listen" || len(d.Args) == 0 {
			continue
		}
		listens++
		addr := d.Args[0]
		if strings.HasPrefix(addr, "unix:") {
			continue
		}
		host := addr
		if h, _, err := net.SplitHostPort(addr); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if host == "localhost" {
			continue
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return false
		}
	}
	return listens > 0
}

// alias: a prefix location without its trailing slash, aliased to a
// directory with one, serves /prefix../ from the directory's parent.
func (l *linter) alias(loc Directive) {
	var path string
	switch {
	case len(loc.Args) == 1:
		path = loc.Args[0]
	case len(loc.Args) == 2 && loc.Args[0] == "^~":
		path = loc.Args[1]
	default:
		return
	}
	if strings.HasPrefix(path, "@") || strings.HasSuffix(path, "/") {
		return
	}
	for _, d := range loc.Block {
		if d.Name == "alias" && len(d.Args) == 1 && strings.HasSuffix(d.Args[0], "/") {
			l.add("alias-traversal", "critical", d, "alias lets "+path+"../ climb out",
				fmt.Sprintf("location %s matches %s../, which alias turns into %s../ — the parent of the aliased directory. End the location with / as well: location %s/.",
					path, path, d.Args[0], path))
		}
	}
}

// proxyPass: a proxy_pass with variables resolves its host at request time,
// which needs a resolver unless the host is an address or an upstream.
func (l *linter) proxyPass(levels []lintLevel, d Directive) {
	if len(d.Args) == 0 || !strings.Contains(d.Args[0], "$") {
		return
	}
	for _, lv := range levels {
		for _, x := range lv.list {
			if x.Name == "resolver" {
				return
			}
		}
	}
	target := d.Args[0]
	if i := strings.Index(target, "://"); i >= 0 {
		target = target[i+3:]
	}
	if strings.HasPrefix(target, "unix:") {
		return
	}
	host, _, _ := strings.Cut(target, "/")
	if strings.Contains(host, "$") {
		l.add("proxy-pass-resolver", "notice", d, "proxy_pass picks its host at request time with no resolver",
			"Unless the variable always names an upstream block, nginx needs a resolver to look the host up and answers 502 without one. Add resolver 127.0.0.53; (or your DNS server) in this server.")
		return
	}
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	name = strings.Trim(name, "[]")
	if net.ParseIP(name) != nil || l.upstreams[strings.ToLower(host)] || l.upstreams[strings.ToLower(name)] {
		return
	}
	l.add("proxy-pass-resolver", "warning", d, "proxy_pass looks up "+name+" at request time with no resolver",
		"A variable in proxy_pass makes nginx resolve the host on each request rather than at start, and with no resolver every request here fails with 502. Add resolver 127.0.0.53; (or your DNS server), or take the variable out.")
}

// unsafeIf: inside a location, if makes a nested location; only return and
// rewrite … last behave there as written.
func (l *linter) unsafeIf(here lintLevel, d Directive) {
	if blockName(here) != "location" {
		return
	}
	for _, x := range d.Block {
		safe := x.Name == "return" || (x.Name == "rewrite" && len(x.Args) > 0 && x.Args[len(x.Args)-1] == "last")
		if !safe {
			l.add("unsafe-if", "warning", d, "if in a location holds "+x.Name,
				"if inside a location makes a nested location of its own, and directives besides return and rewrite … last can apply differently from how they read. Prefer map, or a location of its own.")
			return
		}
	}
}

// duplicateNames: two server blocks claiming the same name on the same
// address — nginx keeps the first and ignores the second for that name.
func (l *linter) duplicateNames(tree []Directive) {
	seen := map[string]Directive{}
	var walk func([]Directive)
	walk = func(list []Directive) {
		for _, d := range list {
			if d.Name == "server" && d.Block != nil && len(d.Context) > 0 && d.Context[len(d.Context)-1] == "http" {
				l.serverNames(seen, d)
				continue
			}
			if d.Block != nil {
				walk(d.Block)
			}
		}
	}
	walk(tree)
}

func (l *linter) serverNames(seen map[string]Directive, server Directive) {
	var listens []string
	names := []string{}
	nameAt := server
	for _, d := range server.Block {
		switch d.Name {
		case "listen":
			if len(d.Args) > 0 {
				listens = append(listens, listenKey(d.Args[0]))
			}
		case "server_name":
			if nameAt.Name == "server" {
				nameAt = d
			}
			for _, n := range d.Args {
				names = append(names, strings.ToLower(n))
			}
		}
	}
	if len(listens) == 0 {
		listens = []string{"*:80"}
	}
	if len(names) == 0 {
		names = []string{""}
	}
	own := map[string]bool{}
	for _, addr := range listens {
		for _, name := range names {
			key := addr + " " + name
			if own[key] {
				continue
			}
			own[key] = true
			first, dup := seen[key]
			if !dup {
				seen[key] = nameAt
				continue
			}
			shown := name
			if shown == "" {
				shown = `""`
			}
			l.add("duplicate-server-name", "warning", nameAt, "Second server for "+shown+" on "+addr,
				fmt.Sprintf("The server at %s:%d already answers %s on %s, so nginx ignores this one for that name and logs a conflicting server name warning.",
					first.File, first.Line, shown, addr))
		}
	}
}

// listenKey is a listen address as nginx compares them: a port alone is
// *:port, an address alone is on port 80, and 0.0.0.0 is *.
func listenKey(addr string) string {
	if strings.HasPrefix(addr, "unix:") {
		return addr
	}
	if strings.Trim(addr, "0123456789") == "" {
		return "*:" + addr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = strings.Trim(addr, "[]"), "80"
		if strings.HasPrefix(addr, "[") {
			host = "[" + host + "]"
		}
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if host == "0.0.0.0" {
		host = "*"
	}
	return strings.ToLower(host) + ":" + port
}
