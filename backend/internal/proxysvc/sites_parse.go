package proxysvc

import (
	"bufio"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ParseSiteSpec reads a site file back into the form.
//
// Not an nginx parser — a line reader that tracks brace depth and which
// location block it is inside, which is enough for the files this form
// produces and for the great majority of hand-written ones. What it cannot
// promise is that saving the form reproduces the file, so it reports whether
// the file carries our marker: a managed file round-trips, and for anything
// else the UI says plainly that saving will replace what is there.
func ParseSiteSpec(name, content string) (*SiteSpec, bool) {
	spec := &SiteSpec{
		Name: name, Kind: "proxy",
		Domains: []string{}, AllowFrom: []string{}, DenyFrom: []string{}, Locations: []SiteLocation{},
	}
	managed := strings.Contains(content, managedMarker)

	seenDomains := map[string]bool{}
	depth := 0
	location := ""
	var current *SiteLocation
	rootLocationUpstream := ""
	rootLocationWS := false
	sawTLSListen := false
	sawPlainRedirect := false
	sawAccessLog := false
	sawGzip := false
	sawBuffering := false
	var custom []string
	inCustom := false
	// Depth of an http-level object at the top of the file (a map, an
	// upstream, a zone). Its contents are its own syntax, not a server's:
	// a map entry or a geo range read as a directive could land in a field.
	objectDepth := 0
	// Inside the site's maintenance geo, whose entries are the addresses
	// let past it.
	inMaintGeo := false
	limits := newLimitParse()
	pools := newPoolParse()
	cache := &cacheParse{}
	realIP := &realIPParse{}
	headers := &headersParse{}
	kinds := &kindParse{}
	// The catch-all's retry settings, which belong to the pool it forwards to.
	var rootRetryOn []string
	rootTries := 0
	maintenance := func() *SiteMaintenance {
		if spec.Maintenance == nil {
			spec.Maintenance = &SiteMaintenance{BypassFrom: []string{}}
		}
		return spec.Maintenance
	}

	// read handles one statement. It is a closure over the reader's state so
	// that a line carrying several statements — `location / { proxy_pass
	// http://x; }` is common in hand-written files — can be split and each
	// piece read in turn, where the line reader used to swallow everything
	// after the opening brace.
	read := func(raw string) {
		if kinds.skipServer {
			if strings.HasSuffix(raw, "{") {
				depth++
			} else if raw == "}" {
				depth--
				kinds.skipServer = depth > 0
			}
			return
		}
		if objectDepth > 0 {
			if strings.HasSuffix(raw, "{") {
				objectDepth++
			} else if raw == "}" {
				objectDepth--
				inMaintGeo = inMaintGeo && objectDepth > 0
				if objectDepth == 0 {
					limits.closed()
					pools.closed()
					cache.closed()
					headers.closed()
				}
			} else if inMaintGeo {
				if fields := strings.Fields(strings.TrimSuffix(raw, ";")); len(fields) == 2 && fields[0] != "default" {
					maintenance().BypassFrom = append(maintenance().BypassFrom, fields[0])
				}
			} else if objectDepth == 1 {
				limits.entry(raw)
				pools.entry(raw)
				cache.entry(raw)
				headers.entry(raw)
			}
			return
		}
		if depth == 0 {
			name, value := cutDirective(raw)
			if httpObjects[name] {
				limits.object(name, value)
				pools.object(name, value)
				cache.object(name, value)
				headers.object(name, value)
				if strings.HasSuffix(raw, "{") {
					objectDepth = 1
					if name == "geo" && maintGeoRe.MatchString(value) {
						inMaintGeo = true
						maintenance()
					}
				}
				return
			}
		}
		if maintIfRe.MatchString(raw) {
			maintenance().On = true
			depth++
			return
		}
		if realIP.ifOpen(raw) {
			depth++
			return
		}
		if m := locationOpenRe.FindStringSubmatch(raw); m != nil {
			depth++
			location = m[2]
			// The exploit blocks are this renderer's, not the operator's, and
			// the switch that produced them is a field of its own. Reading
			// them back as locations would drop them; reading them back as
			// the flag is what makes the switch survive an edit.
			if location == exploitDotLocation {
				spec.BlockExploits = true
			}
			// A page's location is the renderer's, and says the page is on.
			if page, ok := pageFromURI(location); ok {
				if page == "maintenance" {
					maintenance()
				} else if code, err := strconv.Atoi(page); err == nil {
					spec.ErrorPages = append(spec.ErrorPages, code)
				}
				current = nil
				return
			}
			// The ACME challenge location belongs to the redirect block this
			// renderer writes for a forced-HTTPS site. Reading it back as one
			// of the operator's own locations makes a round trip emit it
			// twice — once in the redirect server and once inside the TLS
			// one, where it does nothing.
			// The catch-all is the site's own upstream, and the exploit
			// blocks are read back as their switch above.
			loc := SiteLocation{Path: location, Match: m[1]}
			if (location != "/" || m[1] != "") && location != acmeChallengePath &&
				location != exploitDotLocation && location != exploitExtLocation &&
				location != phpLocation && location != htLocation &&
				validLocationPath(loc) {
				spec.Locations = append(spec.Locations, loc)
				current = &spec.Locations[len(spec.Locations)-1]
			} else {
				current = nil
			}
			return
		}
		if strings.HasSuffix(raw, "{") {
			depth++
			return
		}
		if raw == "}" {
			depth--
			location, current = "", nil
			return
		}

		directive, value := cutDirective(raw)
		if limits.directive(directive, value, location, current, len(spec.Locations)-1) {
			return
		}
		if _, ok := pageFromURI(location); ok {
			switch {
			case directive == "alias":
				spec.PagesDir = filepath.Dir(value)
			case directive == "add_header" && strings.HasPrefix(value, "Retry-After "):
				maintenance().RetryAfter = parseSeconds(strings.Fields(value)[1])
			}
			return
		}
		if cache.directive(directive, value, location) {
			return
		}
		if realIP.directive(spec, directive, value, location == "" && current == nil) {
			return
		}
		if headers.directive(directive, value, location == "" && current == nil, location == "/" && current == nil) {
			return
		}
		if current == nil && kinds.directive(directive, value, location) {
			return
		}
		switch directive {
		case "proxy_intercept_errors":
			spec.InterceptErrors = value == "on"
		case "server_name":
			for _, d := range strings.Fields(value) {
				if d == "_" || seenDomains[d] {
					return
				}
				seenDomains[d] = true
				spec.Domains = append(spec.Domains, d)
			}
		case "listen":
			if listenIsTLS(value) {
				spec.TLS = true
				sawTLSListen = true
			}
			// The pre-1.25 spelling. A file written when `listen 443 ssl http2`
			// was the only form read back as HTTP/2 off, and saving it then
			// turned HTTP/2 off for real.
			if hasField(value, "http2") {
				spec.HTTP2 = true
			}
		case "http2":
			spec.HTTP2 = value == "on"
		case "ssl_protocols":
			if value == "TLSv1.3" {
				spec.TLSProfile = tlsProfileModern
			}
		case "ssl_certificate":
			spec.CertPath = value
		case "ssl_certificate_key":
			spec.KeyPath = value
		case "client_max_body_size":
			if current != nil {
				current.BodyLimit = value
			} else {
				spec.ClientMaxBody = value
			}
		case "gzip":
			sawGzip = true
			spec.Gzip = value == "on"
		case "access_log":
			sawAccessLog = true
			spec.AccessLog = value != "off"
			spec.LogFormat = ""
			if spec.AccessLog {
				spec.LogFormat = accessLogFormat(value)
			}
		case "auth_basic":
			// A path's own prompt says the site's realm; only the server's
			// is the site's.
			// The challenge path's "off" is the renderer's, not a realm.
			if current == nil && location != acmeChallengePath {
				spec.BasicAuthRealm = strings.Trim(value, `"`)
			}
		case "auth_basic_user_file":
			if current != nil {
				current.BasicAuthFile = value
			} else {
				spec.BasicAuthFile = value
			}
		case "allow":
			// A list inside a location restricts that one path, and
			// hoisting it into the form's site-wide list would apply it to
			// the whole site on the next save — a widening or a narrowing
			// nobody asked for. So it stays the path's own.
			if current != nil {
				current.AllowFrom = append(current.AllowFrom, value)
			} else if location == "" {
				spec.AllowFrom = append(spec.AllowFrom, value)
			}
		case "deny":
			if current != nil {
				// The fence the renderer writes after an allow list.
				if value != "all" || len(current.AllowFrom) == 0 {
					current.DenyFrom = append(current.DenyFrom, value)
				}
			} else if location == "" {
				spec.DenyFrom = append(spec.DenyFrom, value)
			}
		case "add_header":
			if strings.HasPrefix(value, "Strict-Transport-Security") {
				readHSTS(spec, value)
			}
			if strings.HasPrefix(value, "X-Content-Type-Options") {
				spec.SecurityHeaders = true
			}
		case "root":
			if location == acmeChallengePath && value == deploymentACMEWebroot {
				spec.ManagedACME = true
			} else if location == "" || location == "/" {
				spec.Root = value
			} else if current != nil {
				// nginx appends the whole path to a root, so this one is
				// kept as a root: saving it as a folder served at the path
				// would move every file the location answers with.
				current.Root, current.RootMode = value, "root"
			}
		case "alias":
			if current != nil {
				current.Root, current.RootMode = strings.TrimSuffix(value, "/"), ""
			}
		case "proxy_pass":
			upstream := strings.TrimPrefix(value, "http://unix:")
			if upstream != value {
				upstream = "unix:" + upstream
			}
			if current != nil {
				current.Upstream = upstream
			} else if location == "/" || location == "" {
				rootLocationUpstream = upstream
			}
		case "proxy_next_upstream":
			if current == nil && location == "/" {
				rootRetryOn = strings.Fields(value)
			}
		case "proxy_next_upstream_tries":
			if current == nil && location == "/" {
				rootTries, _ = strconv.Atoi(value)
			}
		case "proxy_set_header":
			if host, ok := strings.CutPrefix(value, "Host "); ok && current == nil {
				switch host = strings.TrimSpace(host); host {
				case "$host":
					spec.HostHeader, spec.HostHeaderValue = "", ""
				case "$proxy_host":
					spec.HostHeader, spec.HostHeaderValue = "upstream", ""
				default:
					spec.HostHeader, spec.HostHeaderValue = "custom", host
				}
			}
			if strings.HasPrefix(value, "Upgrade") {
				if current != nil {
					current.WebSockets = true
				} else {
					rootLocationWS = true
				}
			}
		case "try_files":
			fields := strings.Fields(value)
			if location == "/" {
				spec.SPA = len(fields) > 0 && fields[len(fields)-1] == "/index.html"
			} else if current != nil {
				current.SPA = len(fields) > 0 && strings.HasSuffix(fields[len(fields)-1], "/index.html")
			}
		case "proxy_read_timeout":
			if current != nil {
				current.Timeout = parseSeconds(value)
			} else {
				spec.ProxyTimeout = parseSeconds(value)
			}
		case "proxy_buffering":
			if current != nil {
				current.Buffering = value
			} else {
				sawBuffering = true
				spec.Buffering = value == "on"
			}
		case "proxy_request_buffering":
			if current != nil {
				current.RequestBuffering = value
			} else {
				spec.StreamUploads = value == "off"
			}
		case "proxy_ssl_server_name":
			spec.UpstreamSNI = spec.UpstreamSNI || (current == nil && value == "on")
		case "proxy_ssl_verify":
			spec.UpstreamVerify = spec.UpstreamVerify || (current == nil && value == "on")
		case "proxy_ssl_name":
			if current == nil {
				spec.UpstreamTLSName = value
			}
		case "proxy_ssl_trusted_certificate":
			if current == nil && value != systemCABundle {
				spec.UpstreamCA = value
			}
		case "return":
			fields := strings.Fields(value)
			if len(fields) >= 2 {
				target := fields[1]
				if strings.Contains(target, "$host$request_uri") {
					sawPlainRedirect = true
				} else if strings.HasPrefix(target, "http") {
					spec.Kind = "redirect"
					spec.RedirectTo = strings.TrimSuffix(target, "$request_uri")
					spec.RedirectDropPath = spec.RedirectTo == target
					// 301 and 302 stay Permanent's, as every file written
					// before the other two could be chosen reads back.
					code, _ := strconv.Atoi(fields[0])
					spec.Permanent = code == 301 || code == 308
					if code == 307 || code == 308 {
						spec.RedirectCode = code
					}
				}
			}
		}
	}

	sc := bufio.NewScanner(strings.NewReader(content))
	sc.Buffer(make([]byte, 0, 8192), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		raw := strings.TrimSpace(line)
		// Everything after the custom marker belongs to the operator, so it is
		// collected verbatim rather than parsed. Without this the "extra
		// configuration" box was written to the file and silently dropped the
		// next time anybody opened the form and saved — the form's own escape
		// hatch was the one field an edit destroyed.
		if inCustom {
			custom = append(custom, strings.TrimPrefix(line, "    "))
			continue
		}
		if raw == customMarker {
			inCustom = true
			continue
		}
		if m := canonicalMarkerRe.FindStringSubmatch(raw); m != nil && depth == 0 {
			spec.Canonical = m[1]
			kinds.skipServer = true
			continue
		}
		if strings.HasPrefix(raw, stripMarker) {
			if current != nil {
				current.StripPrefix = true
			}
			continue
		}
		// A comment after a statement is not part of it: certbot ends every
		// line it adds with "# managed by Certbot", and read as part of the
		// value that made the certificate path "…/fullchain.pem; # managed
		// by Certbot", which the form then refused to save.
		raw = strings.TrimSpace(stripComment(raw))
		if raw == "" {
			continue
		}
		for _, piece := range splitInline(raw) {
			read(piece)
		}
	}

	spec.Upstream = rootLocationUpstream
	pools.settle(spec, rootRetryOn, rootTries)
	spec.WebSockets = rootLocationWS
	// A hand-written file with no access_log line is logging to nginx's
	// default, not to nowhere. Reading it back as "off" meant the first save
	// from the form silently wrote `access_log off;` into a site that had been
	// logging all along. Managed files always carry the directive, so this
	// only ever decides for the ones the form did not write.
	if !managed && !sawAccessLog {
		spec.AccessLog = true
		spec.LogFormat = "combined"
	}
	// The same for compression: a hand-written site without a gzip line
	// inherits the http block's, which is on in Debian's nginx.conf. A
	// managed file always says, so one without the line predates `gzip off;`
	// and was written with the switch off.
	if !managed && !sawGzip {
		spec.Gzip = true
	}
	// And for buffering, which nginx does unless told not to. Every managed
	// file says, one way or the other.
	if !managed && !sawBuffering {
		spec.Buffering = true
	}
	if spec.Kind != "redirect" {
		if spec.Upstream == "" && spec.Pool == nil && spec.Root != "" {
			spec.Kind = "static"
		}
	}
	kinds.settle(spec)
	// The fallback is a static site's; a proxy's location / answers from
	// its upstream whatever try_files a hand-written file put beside it.
	spec.SPA = spec.SPA && spec.Kind == "static"
	spec.ForceHTTPS = sawTLSListen && sawPlainRedirect
	// A file with a plain-HTTP redirect block and nothing else is a redirect
	// site; one that also serves something is a TLS site forcing HTTPS.
	if spec.Kind == "proxy" && spec.Upstream == "" && spec.Pool == nil && spec.Root == "" && sawPlainRedirect {
		spec.Kind = "redirect"
		spec.RedirectTo = "https://" + firstOr(spec.Domains, "")
		spec.Permanent = true
	}
	limits.settle(spec)
	cache.settle(spec)
	realIP.settle(spec)
	headers.settle(spec)
	// Extra locations that ended up with neither an upstream nor a root are
	// something this form cannot express; dropping them is better than
	// offering to save a location that proxies nowhere.
	kept := spec.Locations[:0]
	for _, loc := range spec.Locations {
		// An alias under a regex serves the regex's captures, which the
		// form cannot express either.
		if (loc.Upstream != "" || loc.Root != "") && !(loc.servesFolder() && !loc.isPrefix() && loc.RootMode == "") {
			kept = append(kept, settleLocation(loc, spec))
		}
	}
	spec.Locations = kept
	// The custom block is everything between the marker and the server's
	// closing brace, which the renderer always writes last.
	for len(custom) > 0 && strings.TrimSpace(custom[len(custom)-1]) == "" {
		custom = custom[:len(custom)-1]
	}
	if len(custom) > 0 && strings.TrimSpace(custom[len(custom)-1]) == "}" {
		custom = custom[:len(custom)-1]
	}
	spec.Custom = strings.TrimRight(strings.Join(custom, "\n"), "\n")
	return spec, managed
}

// settleLocation reads a path's settings back as the form holds them: what
// the renderer wrote only because the site says so is the site's, not the
// path's, and a stripped upstream loses the slash stripping gives it.
func settleLocation(loc SiteLocation, spec *SiteSpec) SiteLocation {
	if loc.Timeout == spec.ProxyTimeout {
		loc.Timeout = 0
	}
	if loc.Buffering == onOff(spec.Buffering) {
		loc.Buffering = ""
	}
	if loc.RequestBuffering == onOff(!spec.StreamUploads) {
		loc.RequestBuffering = ""
	}
	if address, uri := splitUpstream(loc.Upstream); loc.StripPrefix && uri == "/" {
		loc.Upstream = address
	}
	// What only a forwarding path uses, read from a hand-written folder,
	// is nothing nginx did with it there.
	if loc.servesFolder() {
		loc.StripPrefix, loc.BodyLimit, loc.Timeout = false, "", 0
		loc.Buffering, loc.RequestBuffering = "", ""
	} else {
		loc.SPA = false
	}
	loc.SPA = loc.SPA && loc.isPrefix()
	return loc
}

// validLocationPath says whether a location read from a file is one the form
// can hold.
func validLocationPath(loc SiteLocation) bool {
	switch loc.Match {
	case "", "=", "^~":
		return locationPathRe.MatchString(loc.Path)
	case "~", "~*":
		return locationRegexRe.MatchString(loc.Path)
	}
	return false
}

const acmeChallengePath = "/.well-known/acme-challenge/"

// customMarker introduces the operator's own directives, and exploitDotLocation
// is the first location renderExploitBlocks writes. Both are read back by the
// parser, so they are constants shared with the renderer rather than strings
// repeated in two files that can drift apart.
const (
	customMarker       = "# Added by hand from the site form."
	exploitDotLocation = `/\.(?!well-known)`
	exploitExtLocation = `\.(sql|bak|old|orig|save|swp|env)$`
)

// httpObjects are the http-level statements a site file may carry above its
// servers, which the renderer writes for the site and the parser steps over.
var httpObjects = map[string]bool{
	"map": true, "geo": true, "upstream": true, "log_format": true,
	"limit_req_zone": true, "limit_conn_zone": true, "proxy_cache_path": true,
}

// accessLogFormat reads which format an access_log line writes in. One of
// this renderer's timed formats is "timed" whichever site's name it carries,
// so a file copied under a new name keeps its format; nginx's default and
// any format the form does not write are "combined", the one the form can
// offer in its place.
func accessLogFormat(value string) string {
	fields := strings.Fields(value)
	if len(fields) >= 2 && strings.HasPrefix(fields[1], "jd_") && strings.HasSuffix(fields[1], "_timed") {
		return "timed"
	}
	return "combined"
}

var (
	maintGeoRe = regexp.MustCompile(`^\$jd_\w+_maint_ip\s*\{$`)
	maintIfRe  = regexp.MustCompile(`^if\s*\(\$jd_\w+_maint\)\s*\{$`)
	pageURIRe  = regexp.MustCompile(`^/__jd/(maintenance|\d{3})\.html$`)
)

// pageFromURI is the page a location serves, when it is one of the site's.
func pageFromURI(location string) (string, bool) {
	m := pageURIRe.FindStringSubmatch(location)
	if m == nil {
		return "", false
	}
	return m[1], true
}

var locationOpenRe = regexp.MustCompile(`^location\s+(?:([~^=*]+)\s+)?(\S+)\s*\{`)

// listenIsTLS reads a listen directive's value the way nginx does: the first
// field is the address, and `ssl` is a parameter after it. `listen 4430` and
// `listen 127.0.0.1:8443` used to count as TLS because the check was a string
// prefix and a substring, which turned a plain-HTTP site on an odd port into
// one the form insisted needed a certificate.
func listenIsTLS(value string) bool {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return false
	}
	if hasField(value, "ssl") {
		return true
	}
	address := fields[0]
	if i := strings.LastIndex(address, ":"); i >= 0 {
		address = address[i+1:]
	}
	return address == "443"
}

func hasField(value, want string) bool {
	for _, field := range strings.Fields(value) {
		if field == want {
			return true
		}
	}
	return false
}

// splitInline breaks a line holding several statements into one statement
// per element: `location / { proxy_pass http://x; }` becomes the opener,
// the directive and the closing brace, and `gzip on; gzip_vary on;` its two
// directives — read whole, the second made the value "on; gzip_vary on",
// which is not "on", and a save wrote `gzip off;`. Quotes are respected,
// since a Content-Security-Policy value carries semicolons of its own, and
// so is a ${variable}, whose braces open no block.
func splitInline(raw string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	var quote byte
	variable := false
	for j := 0; j < len(raw); j++ {
		c := raw[j]
		switch {
		case quote != 0:
			cur.WriteByte(c)
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
			cur.WriteByte(c)
		case c == '{' && j > 0 && raw[j-1] == '$':
			variable = true
			cur.WriteByte(c)
		case c == '}' && variable:
			variable = false
			cur.WriteByte(c)
		case c == ';' || c == '{':
			cur.WriteByte(c)
			flush()
		case c == '}':
			flush()
			out = append(out, "}")
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	if len(out) == 0 {
		return []string{raw}
	}
	return out
}

// stripComment cuts a line at the "#" that starts a comment: outside quotes,
// and where a word could start, which is where nginx reads one — a "#"
// inside a word is part of it.
func stripComment(raw string) string {
	var quote byte
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#' && (i == 0 || strings.ContainsRune(" \t;{}", rune(raw[i-1]))):
			return raw[:i]
		}
	}
	return raw
}

func cutDirective(line string) (string, string) {
	line = strings.TrimSuffix(strings.TrimSpace(line), ";")
	name, value, ok := strings.Cut(line, " ")
	if !ok {
		return name, ""
	}
	return name, strings.TrimSpace(value)
}

func parseSeconds(value string) int {
	value = strings.TrimSuffix(strings.TrimSpace(value), "s")
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return n
}

func firstOr(list []string, fallback string) string {
	if len(list) == 0 {
		return fallback
	}
	return list[0]
}
