package proxysvc

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// RouteResolution is the answer to "which server block and location does
// nginx pick for this URL, and what does it do with the request?", worked out
// from the configuration tree with nginx's own precedence rules.
//
// It is read from the files as `nginx -T` prints them, so a change written
// but not yet reloaded is already part of the answer.
type RouteResolution struct {
	URL    string `json:"url"`
	Scheme string `json:"scheme"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
	// Path is the URI the way nginx matches it: percent-decoded, with "."
	// and ".." resolved and repeated slashes merged.
	Path string `json:"path"`
	// Outcome is one of "refused" (nothing listens), "tls-failed" (the TLS
	// handshake cannot complete), "plain-to-tls" (plain HTTP to a TLS port,
	// answered 400), "return", "redirect" (nginx's own 301 to the path with
	// a slash), "proxy" (handed to an upstream), "handler" (a built-in
	// handler such as stub_status) or "static" (files).
	Outcome  string         `json:"outcome"`
	Server   *RouteServer   `json:"server,omitempty"`
	Location *RouteLocation `json:"location,omitempty"`
	// Serves lists the directives that decide the answer, the deciding one
	// first.
	Serves []RouteDirective `json:"serves"`
	Steps  []RouteStep      `json:"steps"`
	// Certain is false when the configuration holds something this cannot
	// evaluate the way nginx would — a rewrite, an if, a regular expression
	// PCRE accepts and Go does not — on the way to the answer.
	Certain bool `json:"certain"`
}

// RouteServer is the server block that answers.
type RouteServer struct {
	Names []string `json:"names"`
	// Listen is the address and port the request arrives on, as nginx groups
	// server blocks: "*:80", "[::]:443", "127.0.0.1:8080".
	Listen string `json:"listen"`
	// Match says how it won: "exact", "wildcard", "regex", "default_server"
	// (marked default for the address) or "first" (the first block for the
	// address, which nginx falls back to when nothing names the host).
	Match string `json:"match"`
	Name  string `json:"name,omitempty"`
	File  string `json:"file"`
	Line  int    `json:"line"`
}

// RouteLocation is the location block that answers, with the locations it
// is nested in, outermost first.
type RouteLocation struct {
	Modifier string   `json:"modifier"`
	Path     string   `json:"path"`
	Parents  []string `json:"parents"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
}

// RouteDirective is one directive quoted from the configuration.
type RouteDirective struct {
	Text string `json:"text"`
	File string `json:"file"`
	Line int    `json:"line"`
	// Inherited marks a directive written in an enclosing block.
	Inherited bool `json:"inherited,omitempty"`
}

// RouteStep is one rule applied on the way to the answer. Caution marks a
// step whose rule could not be evaluated exactly, or an answer that fails.
type RouteStep struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Caution bool   `json:"caution,omitempty"`
}

// ErrRouteURL is returned for a URL that cannot be asked about.
var ErrRouteURL = errors.New("invalid URL")

// contentHandlers set a location's content handler. They are not inherited
// by nested locations.
var contentHandlers = map[string]bool{
	"proxy_pass": true, "fastcgi_pass": true, "uwsgi_pass": true, "scgi_pass": true,
	"grpc_pass": true, "memcached_pass": true, "stub_status": true,
}

type routeListen struct {
	host  string // "" for the wildcard address of its family
	v6    bool
	port  int
	ssl   bool
	deflt bool
}

func (l routeListen) key() string {
	host := l.host
	switch {
	case host == "" && l.v6:
		host = "[::]"
	case host == "":
		host = "*"
	case l.v6:
		host = "[" + host + "]"
	}
	return host + ":" + strconv.Itoa(l.port)
}

type routeServer struct {
	d       Directive
	http    Directive
	names   []string
	listens []routeListen
}

// ResolveRoute follows nginx's rules for rawURL through tree, the output of
// NginxTree: the listen address and port first, then server_name (exact,
// longest leading wildcard, longest trailing wildcard, first regex, then the
// default server), then location (exact, longest prefix, ^~, regexes in
// order, nested locations the way ngx_http_core_find_location recurses), and
// last what the location does with the request.
func ResolveRoute(tree []Directive, rawURL string) (RouteResolution, error) {
	raw := strings.TrimSpace(rawURL)
	if raw != "" && !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return RouteResolution{}, fmt.Errorf("%w: enter an address such as https://example.com/path", ErrRouteURL)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return RouteResolution{}, fmt.Errorf("%w: only http and https addresses reach a server block", ErrRouteURL)
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	for _, c := range host {
		if c > 127 {
			return RouteResolution{}, fmt.Errorf("%w: write an international name in its xn-- form, the way a browser sends it", ErrRouteURL)
		}
	}
	port := 80
	if scheme == "https" {
		port = 443
	}
	if p := u.Port(); p != "" {
		port, err = strconv.Atoi(p)
		if err != nil || port < 1 || port > 65535 {
			return RouteResolution{}, fmt.Errorf("%w: port %q", ErrRouteURL, p)
		}
	}
	uri := normalizeURI(u.Path)

	res := RouteResolution{
		Scheme: scheme, Host: host, Port: port, Path: uri,
		Serves: []RouteDirective{}, Steps: []RouteStep{}, Certain: true,
	}
	display := host
	if strings.Contains(host, ":") {
		display = "[" + host + "]"
	}
	res.URL = scheme + "://" + display
	if u.Port() != "" {
		res.URL += ":" + strconv.Itoa(port)
	}
	res.URL += uri
	if u.RawQuery != "" {
		res.URL += "?" + u.RawQuery
	}

	servers := routeServers(tree)
	group, binding, ok := res.pickBinding(servers, host, port)
	if !ok {
		res.Outcome = "refused"
		return res, nil
	}

	ssl := false
	for _, s := range group {
		for _, l := range s.listens {
			if l.key() == binding && l.ssl {
				ssl = true
			}
		}
	}
	chosen := res.pickServer(group, binding, host)
	res.Server.Listen = binding

	switch {
	case scheme == "https" && !ssl:
		res.Outcome = "tls-failed"
		res.caution("listen", fmt.Sprintf("No server listens on %s with ssl, so nginx answers plain HTTP and the TLS handshake fails before any request is sent.", binding), "", 0)
		return res, nil
	case scheme == "http" && ssl:
		res.Outcome = "plain-to-tls"
		res.caution("listen", fmt.Sprintf("%s is a TLS port, so a plain HTTP request is answered 400 “The plain HTTP request was sent to HTTPS port”.", binding), "", 0)
		return res, nil
	case scheme == "https":
		if cert, found := inheritedDirective("ssl_certificate", chosen.d, chosen.http); found {
			res.step("tls", "The certificate offered is "+strings.Join(cert.Args, " ")+".", cert.File, cert.Line)
		} else {
			res.caution("tls", "This server block sets no ssl_certificate, so nginx offers the certificate of the default server for "+binding+", which may not name "+host+".", chosen.d.File, chosen.d.Line)
		}
	}

	// The server's own rewrite-module directives run before any location is
	// looked up.
	for _, d := range chosen.d.Block {
		switch d.Name {
		case "return":
			res.Outcome = "return"
			res.Serves = append(res.Serves, quoteDirective(d, false))
			res.step("server", "The server block returns before any location is looked up.", d.File, d.Line)
			return res, nil
		case "rewrite":
			res.caution("server", "A server-level rewrite runs first and may change the path; this answer assumes it leaves "+uri+" alone.", d.File, d.Line)
		case "if":
			res.caution("server", "A server-level if runs first and may answer or rewrite the request; this answer assumes its condition is false.", d.File, d.Line)
		}
	}

	search := locationSearch{res: &res, uri: uri}
	rc := search.find(chosen.d.Block, nil)
	if len(search.cur) == 0 {
		res.step("location", "No location matches "+uri+", so the server block's own directives answer.", chosen.d.File, chosen.d.Line)
		res.serve(nil, chosen, uri)
		return res, nil
	}
	loc := search.cur[len(search.cur)-1]
	mod, name, _ := splitLocation(loc.Args)
	res.Location = &RouteLocation{Modifier: mod, Path: name, Parents: []string{}, File: loc.File, Line: loc.Line}
	for _, p := range search.cur[:len(search.cur)-1] {
		res.Location.Parents = append(res.Location.Parents, strings.Join(p.Args, " "))
	}
	if rc == locDone {
		res.Outcome = "redirect"
		res.step("answer", "nginx answers 301 to "+uri+"/, since the location is "+name+" and passes requests to a backend.", loc.File, loc.Line)
		return res, nil
	}
	res.serve(search.cur, chosen, uri)
	return res, nil
}

func normalizeURI(p string) string {
	if p == "" {
		return "/"
	}
	clean := path.Clean("/" + p)
	if strings.HasSuffix(p, "/") && clean != "/" {
		clean += "/"
	}
	return clean
}

func (r *RouteResolution) step(stage, message, file string, line int) {
	r.Steps = append(r.Steps, RouteStep{Stage: stage, Message: message, File: file, Line: line})
}

func (r *RouteResolution) caution(stage, message, file string, line int) {
	r.Steps = append(r.Steps, RouteStep{Stage: stage, Message: message, File: file, Line: line, Caution: true})
	if stage != "listen" && stage != "tls" {
		r.Certain = false
	}
}

func routeServers(tree []Directive) []routeServer {
	var out []routeServer
	for _, http := range tree {
		if http.Name != "http" {
			continue
		}
		for _, d := range http.Block {
			if d.Name != "server" {
				continue
			}
			s := routeServer{d: d, http: http}
			for _, c := range d.Block {
				switch c.Name {
				case "server_name":
					s.names = append(s.names, c.Args...)
				case "listen":
					if l, ok := parseRouteListen(c.Args); ok {
						s.listens = append(s.listens, l)
					}
				}
			}
			if !hasDirective(d.Block, "listen") {
				// nginx running as root, as it does here, puts a server
				// without listen on *:80.
				s.listens = append(s.listens, routeListen{port: 80})
			}
			out = append(out, s)
		}
	}
	return out
}

func hasDirective(block []Directive, name string) bool {
	for _, d := range block {
		if d.Name == name {
			return true
		}
	}
	return false
}

// parseRouteListen reads a TCP listen for HTTP; unix sockets and QUIC
// (UDP) listens are not reached by a URL typed into a browser here.
func parseRouteListen(args []string) (routeListen, bool) {
	if len(args) == 0 || strings.HasPrefix(args[0], "unix:") {
		return routeListen{}, false
	}
	l := routeListen{port: 80}
	for _, flag := range args[1:] {
		switch flag {
		case "ssl":
			l.ssl = true
		case "default_server", "default":
			l.deflt = true
		case "quic":
			return routeListen{}, false
		}
	}
	a := args[0]
	portText := ""
	switch {
	case strings.HasPrefix(a, "["):
		end := strings.Index(a, "]")
		if end < 0 {
			return routeListen{}, false
		}
		l.host, l.v6 = a[1:end], true
		portText = strings.TrimPrefix(a[end+1:], ":")
	case strings.Contains(a, ":"):
		i := strings.LastIndex(a, ":")
		l.host, portText = a[:i], a[i+1:]
	default:
		if _, err := strconv.Atoi(a); err == nil {
			portText = a
		} else {
			l.host = a
		}
	}
	if portText != "" {
		p, err := strconv.Atoi(portText)
		if err != nil {
			return routeListen{}, false
		}
		l.port = p
	}
	switch strings.ToLower(l.host) {
	case "*", "0.0.0.0", "::":
		l.host = ""
	case "localhost":
		l.host = "127.0.0.1"
	}
	if ip := net.ParseIP(l.host); ip != nil {
		l.host = ip.String()
	}
	return l, true
}

// pickBinding finds the address:port the request arrives on and the server
// blocks listening there. A block listening on a specific address takes
// every request that arrives on that address, and blocks on the wildcard
// address never see those requests.
func (r *RouteResolution) pickBinding(servers []routeServer, host string, port int) ([]routeServer, string, bool) {
	ip := net.ParseIP(host)
	v6 := ip != nil && ip.To4() == nil
	collect := func(match func(routeListen) bool) ([]routeServer, string) {
		var group []routeServer
		binding := ""
		for _, s := range servers {
			for _, l := range s.listens {
				if l.port == port && match(l) {
					if binding == "" {
						binding = l.key()
					}
					if l.key() == binding {
						group = append(group, s)
						break
					}
				}
			}
		}
		return group, binding
	}
	if ip != nil {
		if group, binding := collect(func(l routeListen) bool { return l.host != "" && l.host == ip.String() }); binding != "" {
			r.step("listen", fmt.Sprintf("%d server block(s) listen on %s itself; blocks on the wildcard address never see these requests.", len(group), binding), "", 0)
			return group, binding, true
		}
	}
	if group, binding := collect(func(l routeListen) bool { return l.host == "" && l.v6 == v6 }); binding != "" {
		r.step("listen", fmt.Sprintf("%d server block(s) listen on %s.", len(group), binding), "", 0)
		if ip == nil {
			r.noteSpecific(servers, port)
		}
		return group, binding, true
	}
	if ip == nil {
		// The name could reach the host over either family, and at any of
		// its addresses; this takes the first that is served.
		if group, binding := collect(func(l routeListen) bool { return l.host == "" }); binding != "" {
			r.caution("listen", fmt.Sprintf("Port %d is served only on %s, so this assumes the name reaches the host over IPv6.", port, binding), "", 0)
			return group, binding, true
		}
		if group, binding := collect(func(routeListen) bool { return true }); binding != "" {
			r.caution("listen", fmt.Sprintf("Port %d is served only on specific addresses; this assumes the name resolves to %s.", port, binding), "", 0)
			r.Certain = false
			return group, binding, true
		}
	}
	target := host
	if v6 {
		target = "[" + host + "]"
	}
	r.caution("listen", fmt.Sprintf("No server block listens on %s:%d, so the connection is refused (or reaches something other than nginx).", target, port), "", 0)
	return nil, "", false
}

// noteSpecific says which addresses on the port answer from other blocks,
// since a name that resolves to one of them never reaches the wildcard group.
func (r *RouteResolution) noteSpecific(servers []routeServer, port int) {
	seen := map[string]bool{}
	var keys []string
	for _, s := range servers {
		for _, l := range s.listens {
			if l.port == port && l.host != "" && !seen[l.key()] {
				seen[l.key()] = true
				keys = append(keys, l.key())
			}
		}
	}
	if len(keys) > 0 {
		r.step("listen", "Requests that arrive on "+strings.Join(keys, ", ")+" are answered by the blocks listening there instead.", "", 0)
	}
}

// pickServer applies server_name precedence within one address:port.
func (r *RouteResolution) pickServer(group []routeServer, binding, host string) routeServer {
	win := func(s routeServer, match, name, message string) routeServer {
		r.Server = &RouteServer{Names: s.names, Match: match, Name: name, File: s.d.File, Line: s.d.Line}
		if r.Server.Names == nil {
			r.Server.Names = []string{}
		}
		r.step("server", message, s.d.File, s.d.Line)
		return s
	}
	// A leading-dot name such as ".example.com" is also an exact name for
	// "example.com"; nginx adds both to its hashes.
	for _, s := range group {
		for _, n := range s.names {
			n = strings.ToLower(n)
			if n == host || (strings.HasPrefix(n, ".") && n[1:] == host) {
				return win(s, "exact", n, "server_name "+n+" names "+host+" exactly.")
			}
		}
	}
	best, bestName := -1, ""
	for i, s := range group {
		for _, n := range s.names {
			n = strings.ToLower(n)
			suffix := ""
			switch {
			case strings.HasPrefix(n, "*."):
				suffix = n[1:]
			case strings.HasPrefix(n, "."):
				suffix = n
			default:
				continue
			}
			if strings.HasSuffix(host, suffix) && len(n) > len(bestName) {
				best, bestName = i, n
			}
		}
	}
	if best >= 0 {
		return win(group[best], "wildcard", bestName, "No exact name; "+bestName+" is the longest wildcard that starts with * and matches.")
	}
	for i, s := range group {
		for _, n := range s.names {
			n = strings.ToLower(n)
			if strings.HasSuffix(n, ".*") && strings.HasPrefix(host, n[:len(n)-1]) && len(n) > len(bestName) {
				best, bestName = i, n
			}
		}
	}
	if best >= 0 {
		return win(group[best], "wildcard", bestName, "No exact or leading wildcard; "+bestName+" is the longest wildcard that ends with * and matches.")
	}
	for _, s := range group {
		for _, n := range s.names {
			if !strings.HasPrefix(n, "~") {
				continue
			}
			re, err := regexp.Compile(n[1:])
			if err != nil {
				r.caution("server", "server_name "+n+" uses a pattern only PCRE reads, so whether it matches is unknown; nginx would try it here.", s.d.File, s.d.Line)
				continue
			}
			if re.MatchString(host) {
				return win(s, "regex", n, "No exact or wildcard name; "+n+" is the first regular expression that matches.")
			}
		}
	}
	for _, s := range group {
		for _, l := range s.listens {
			if l.key() == binding && l.deflt {
				return win(s, "default_server", "", "No server_name matches "+host+", so the default_server for "+binding+" answers.")
			}
		}
	}
	return win(group[0], "first", "", "No server_name matches "+host+" and nothing is marked default_server, so the first block for "+binding+" answers.")
}

const (
	locDeclined = iota
	locAgain
	locOK
	locDone
)

type locationSearch struct {
	res *RouteResolution
	uri string
	// cur is the matched location with the locations it sits in.
	cur []Directive
}

func splitLocation(args []string) (modifier, name string, ok bool) {
	if len(args) == 2 {
		switch args[0] {
		case "=", "^~", "~", "~*":
			return args[0], args[1], true
		}
		return "", "", false
	}
	if len(args) != 1 {
		return "", "", false
	}
	a := args[0]
	switch {
	case strings.HasPrefix(a, "@"):
		return "@", a, true
	case strings.HasPrefix(a, "="):
		return "=", a[1:], true
	case strings.HasPrefix(a, "^~"):
		return "^~", a[2:], true
	case strings.HasPrefix(a, "~*"):
		return "~*", a[2:], true
	case strings.HasPrefix(a, "~"):
		return "~", a[1:], true
	}
	return "", a, true
}

func autoRedirects(d Directive) bool {
	for _, c := range d.Block {
		if contentHandlers[c.Name] && c.Name != "stub_status" {
			return true
		}
	}
	return false
}

// find mirrors ngx_http_core_find_location: the static (exact and prefix)
// locations of one level, then the nested locations of the prefix that won,
// then — unless that prefix is ^~, or an exact match settled it — this
// level's regexes in the order they are written.
func (ls *locationSearch) find(block []Directive, parents []Directive) int {
	rc := ls.static(block, parents)
	noregex := false
	if rc == locAgain {
		matched := ls.cur[len(ls.cur)-1]
		mod, _, _ := splitLocation(matched.Args)
		noregex = mod == "^~"
		if noregex {
			ls.res.step("location", "^~ on the longest prefix stops the regular expressions at this level.", matched.File, matched.Line)
		}
		rc = ls.find(matched.Block, ls.cur)
	}
	if rc == locOK || rc == locDone {
		return rc
	}
	if noregex {
		return rc
	}
	for _, d := range block {
		if d.Name != "location" {
			continue
		}
		mod, pattern, ok := splitLocation(d.Args)
		if !ok || (mod != "~" && mod != "~*") {
			continue
		}
		expr := pattern
		if mod == "~*" {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			ls.res.caution("location", "location "+mod+" "+pattern+" uses a pattern only PCRE reads, so whether it matches is unknown; nginx would try it here.", d.File, d.Line)
			continue
		}
		if !re.MatchString(ls.uri) {
			continue
		}
		message := "location " + mod + " " + pattern + " is the first regular expression that matches"
		if len(ls.cur) > len(parents) {
			message += ", so it wins over the prefix"
		}
		ls.res.step("location", message+".", d.File, d.Line)
		ls.cur = append(append([]Directive(nil), parents...), d)
		ls.find(d.Block, ls.cur)
		return locOK
	}
	return rc
}

func (ls *locationSearch) static(block []Directive, parents []Directive) int {
	var longest *Directive
	longestName := ""
	for i, d := range block {
		if d.Name != "location" {
			continue
		}
		mod, name, ok := splitLocation(d.Args)
		if !ok {
			continue
		}
		switch mod {
		case "=":
			if name == ls.uri {
				ls.res.step("location", "location = "+name+" matches exactly, which ends the search.", d.File, d.Line)
				ls.cur = append(append([]Directive(nil), parents...), d)
				return locOK
			}
		case "", "^~":
			if strings.HasPrefix(ls.uri, name) && len(name) > len(longestName) {
				longest, longestName = &block[i], name
			}
		}
	}
	for _, d := range block {
		if d.Name != "location" {
			continue
		}
		mod, name, ok := splitLocation(d.Args)
		if ok && mod != "@" && !strings.HasPrefix(mod, "~") && name == ls.uri+"/" && autoRedirects(d) && longestName != ls.uri {
			ls.cur = append(append([]Directive(nil), parents...), d)
			return locDone
		}
	}
	if longest == nil {
		return locDeclined
	}
	ls.res.step("location", "location "+strings.Join(longest.Args, " ")+" is the longest prefix that matches.", longest.File, longest.Line)
	ls.cur = append(append([]Directive(nil), parents...), *longest)
	return locAgain
}

func quoteDirective(d Directive, inherited bool) RouteDirective {
	words := make([]string, 0, len(d.Args)+1)
	words = append(words, d.Name)
	for _, a := range d.Args {
		if a == "" || strings.ContainsAny(a, " \t;{}\"'") {
			a = strconv.Quote(a)
		}
		words = append(words, a)
	}
	return RouteDirective{Text: strings.Join(words, " ") + ";", File: d.File, Line: d.Line, Inherited: inherited}
}

// inheritedDirective finds name in the innermost of blocks that sets it.
func inheritedDirective(name string, blocks ...Directive) (Directive, bool) {
	for _, b := range blocks {
		for _, d := range b.Block {
			if d.Name == name {
				return d, true
			}
		}
	}
	return Directive{}, false
}

// serve reads what the matched location (or, with chain nil, the server
// block) does with the request: a return runs in the rewrite phase, before
// any content handler; then a *_pass; otherwise files under root or alias.
func (r *RouteResolution) serve(chain []Directive, server routeServer, uri string) {
	var own Directive
	if len(chain) > 0 {
		own = chain[len(chain)-1]
		for _, d := range own.Block {
			switch d.Name {
			case "return":
				r.Outcome = "return"
				r.Serves = append(r.Serves, quoteDirective(d, false))
				r.step("answer", "The location returns without passing the request on.", d.File, d.Line)
				return
			case "rewrite":
				r.caution("answer", "A rewrite in this location may change the path and search the locations again; this answer assumes it does not match.", d.File, d.Line)
			case "if":
				r.caution("answer", "An if in this location may answer or rewrite the request; this answer assumes its condition is false.", d.File, d.Line)
			}
		}
		for _, d := range own.Block {
			if contentHandlers[d.Name] {
				r.Outcome = "proxy"
				if d.Name == "stub_status" {
					r.Outcome = "handler"
				}
				r.Serves = append(r.Serves, quoteDirective(d, false))
				r.step("answer", "The request is handed to "+d.Name+".", d.File, d.Line)
				return
			}
		}
	}

	r.Outcome = "static"
	// root and index inherit from every enclosing block; alias and try_files
	// are read from the location itself.
	scopes := make([]Directive, 0, len(chain)+2)
	for i := len(chain) - 1; i >= 0; i-- {
		scopes = append(scopes, chain[i])
	}
	scopes = append(scopes, server.d, server.http)
	if len(chain) > 0 {
		if d, ok := inheritedDirective("alias", own); ok && len(d.Args) > 0 {
			r.Serves = append(r.Serves, quoteDirective(d, false))
			mod, name, _ := splitLocation(own.Args)
			message := "Files are read from the alias."
			if mod == "" || mod == "^~" {
				message = "Files are read from " + d.Args[0] + strings.TrimPrefix(uri, name) + "."
			}
			r.step("answer", message, d.File, d.Line)
			r.staticExtras(own, scopes)
			return
		}
	}
	for i, s := range scopes {
		if d, ok := inheritedDirective("root", s); ok && len(d.Args) > 0 {
			r.Serves = append(r.Serves, quoteDirective(d, i > 0))
			r.step("answer", "Files are read from "+strings.TrimSuffix(d.Args[0], "/")+uri+".", d.File, d.Line)
			r.staticExtras(own, scopes)
			return
		}
	}
	r.step("answer", "No root is set, so files are read from nginx's built-in html directory under its prefix.", "", 0)
	r.staticExtras(own, scopes)
}

func (r *RouteResolution) staticExtras(own Directive, scopes []Directive) {
	if d, ok := inheritedDirective("try_files", own); ok {
		r.Serves = append(r.Serves, quoteDirective(d, false))
	}
	if strings.HasSuffix(r.Path, "/") {
		for i, s := range scopes {
			if d, ok := inheritedDirective("index", s); ok {
				r.Serves = append(r.Serves, quoteDirective(d, i > 0))
				break
			}
		}
	}
}
