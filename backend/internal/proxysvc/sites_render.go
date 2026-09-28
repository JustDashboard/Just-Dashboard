package proxysvc

import (
	"fmt"
	"strconv"
	"strings"
)

// The config is written by hand rather than by a template engine, for the
// reason dockerx renders compose by hand: order carries meaning to whoever
// reads the file next. A generated file that groups related directives and
// explains the non-obvious ones is a file somebody can maintain after the
// dashboard has been uninstalled; one that emits directives in map order is a
// file people replace rather than read.

type lines struct{ out []string }

func (l *lines) add(format string, args ...any) {
	if len(args) == 0 {
		l.out = append(l.out, format)
		return
	}
	l.out = append(l.out, fmt.Sprintf(format, args...))
}

func (l *lines) blank() {
	if len(l.out) > 0 && l.out[len(l.out)-1] != "" {
		l.out = append(l.out, "")
	}
}

func (l *lines) String() string {
	return strings.Join(l.out, "\n") + "\n"
}

// RenderNginx turns a spec into the file that would produce it.
func RenderNginx(spec *SiteSpec) (string, error) {
	if err := ValidateSpec(spec); err != nil {
		return "", err
	}
	l := &lines{}
	names := strings.Join(spec.Domains, " ")

	l.add(managedMarker)
	l.add("# Site: %s", spec.Name)
	l.add("# This is ordinary nginx configuration. Edit it here or by hand — the")
	l.add("# form on the Proxy page reads it back either way.")
	l.blank()

	renderHTTPBlock(l, spec)

	if spec.TLS && spec.ForceHTTPS {
		renderRedirectServer(l, names, spec.ManagedACME)
		l.blank()
	}

	l.add("server {")
	renderListen(l, spec)
	l.add("    server_name %s;", names)
	l.blank()

	if spec.TLS {
		renderTLS(l, spec)
		l.blank()
	}
	if spec.SecurityHeaders || spec.HSTS {
		renderHeaders(l, spec)
		l.blank()
	}
	renderServerOptions(l, spec)
	renderUpstreamTLS(l, spec)
	renderErrorRouting(l, spec)
	renderAccess(l, spec)
	renderServerLimits(l, spec)
	renderACMEChallenge(l, spec)

	switch spec.Kind {
	case "redirect":
		code := 302
		if spec.Permanent {
			code = 301
		}
		l.add("    # $request_uri keeps the path and query, so a bookmark deeper than")
		l.add("    # the home page still lands somewhere useful.")
		l.add("    return %d %s$request_uri;", code, strings.TrimSuffix(spec.RedirectTo, "/"))
	case "static":
		l.add("    root %s;", spec.Root)
		l.add("    index index.html index.htm;")
		l.blank()
		l.add("    location / {")
		if spec.SPA {
			l.add("        # A single-page app routes in the browser: a path with no file")
			l.add("        # of its own is answered with index.html, and the app's router")
			l.add("        # takes it from there.")
			l.add("        try_files $uri $uri/ /index.html;")
		} else {
			l.add("        try_files $uri $uri/ =404;")
		}
		l.add("    }")
	default:
		for _, loc := range spec.Locations {
			renderLocation(l, loc, spec)
			l.blank()
		}
		renderLocation(l, SiteLocation{Path: "/", Upstream: spec.mainUpstream(), WebSockets: spec.WebSockets}, spec)
	}

	if spec.usesPages() {
		l.blank()
		renderPageLocations(l, spec)
	}
	if spec.BlockExploits {
		l.blank()
		renderExploitBlocks(l)
	}
	if strings.TrimSpace(spec.Custom) != "" {
		l.blank()
		l.add("    %s", customMarker)
		for _, line := range strings.Split(strings.TrimRight(spec.Custom, "\n"), "\n") {
			l.add("    %s", strings.TrimRight(line, " \t"))
		}
	}
	l.add("}")
	return l.String(), nil
}

// timedLogFormat is combined followed by named fields, so a reader of the
// stock format still reads every line and one that knows the names gets the
// request time, the upstream's share of it and the cache's verdict.
const timedLogFormat = `'$remote_addr - $remote_user [$time_local] "$request" $status $body_bytes_sent "$http_referer" "$http_user_agent" rt=$request_time urt="$upstream_response_time" host=$host cs=$upstream_cache_status'`

// renderHTTPBlock writes the http-level objects the site owns, above its
// servers. A site file is included inside http {}, so its top is http
// context; what is defined there is deleted with the site. The names are
// global to the whole configuration, so each carries the site's own
// NginxIdent: a second log_format of the same name fails every reload, and a
// map variable defined twice is decided by whichever file nginx reads last.
func renderHTTPBlock(l *lines, spec *SiteSpec) {
	wrote := false
	if spec.usesWebSockets() {
		l.add("# The standard upgrade map: a request asking to be upgraded is passed")
		l.add("# on as one, and every other request closes its upstream connection")
		l.add("# rather than forwarding whatever Connection header the client sent.")
		l.add("map $http_upgrade $%s {", spec.connectionVar())
		l.add("    default upgrade;")
		if spec.poolKeepsAlive() {
			l.add("    # Cleared rather than close, so the pool's connections are kept.")
			l.add("    ''      \"\";")
		} else {
			l.add("    ''      close;")
		}
		l.add("}")
		wrote = true
	}
	if spec.Kind == "proxy" && spec.Pool != nil {
		if wrote {
			l.blank()
		}
		renderPool(l, spec)
		wrote = true
	}
	if spec.AccessLog && spec.timedLog() {
		if wrote {
			l.blank()
		}
		l.add("# nginx's combined format with the time taken added, which is what")
		l.add("# the dashboard's traffic pages read latency from.")
		l.add("log_format %s %s;", spec.logFormatName(), timedLogFormat)
		wrote = true
	}
	if spec.Maintenance != nil {
		if wrote {
			l.blank()
		}
		renderMaintenanceMaps(l, spec)
		wrote = true
	}
	if spec.hasLimits() {
		if wrote {
			l.blank()
		}
		renderLimitZones(l, spec)
		wrote = true
	}
	if wrote {
		l.blank()
	}
}

// renderMaintenanceMaps says which addresses maintenance lets through: 0 for
// the listed ones, 1 for everyone else.
func renderMaintenanceMaps(l *lines, spec *SiteSpec) {
	l.add("# Maintenance: these addresses reach the site as usual while it is on;")
	l.add("# everyone else gets the maintenance page.")
	l.add("geo $%s {", spec.maintIPVar())
	l.add("    default 1;")
	for _, entry := range spec.Maintenance.BypassFrom {
		l.add("    %s 0;", entry)
	}
	l.add("}")
}

// pageURI is the internal address a page is served at.
func pageURI(page string) string { return "/__jd/" + page + ".html" }

// maintenanceExempt are the paths maintenance lets through whoever asks: the
// ACME challenge, so a certificate still renews while the site is down, and
// the maintenance page itself.
const maintenanceExempt = `"^/(\.well-known/acme-challenge/|__jd/maintenance\.html$)"`

// renderErrorRouting sends the codes the site has pages for to them, and
// answers a request with the maintenance page while maintenance is on.
//
// error_page to a path rather than a named location, because nginx turns
// the request into a GET on the way there, where a named location keeps a
// POST and the static page answers it 405. The path goes through the
// server's rewrite phase again, which is why the check is a variable set
// on every pass rather than a map, whose value nginx keeps for the rest of
// the request.
func renderErrorRouting(l *lines, spec *SiteSpec) {
	on := spec.Maintenance != nil && spec.Maintenance.On
	if !on && len(spec.ErrorPages) == 0 {
		return
	}
	if on {
		l.add("    # Maintenance is on. It is checked before anything else the")
		l.add("    # server does, a redirect included.")
		l.add("    set $%s $%s;", spec.maintVar(), spec.maintIPVar())
		l.add("    if ($uri ~ %s) {", maintenanceExempt)
		l.add("        set $%s 0;", spec.maintVar())
		l.add("    }")
		l.add("    if ($%s) {", spec.maintVar())
		l.add("        return 503;")
		l.add("    }")
		l.add("    error_page 503 %s;", pageURI("maintenance"))
	}
	// The site's own 503 page waits while maintenance holds the code; its
	// location stays written, which is what brings it back afterwards.
	for _, code := range errorPageCodes {
		if spec.hasErrorPage(code) && !(on && code == 503) {
			l.add("    error_page %d %s;", code, pageURI(strconv.Itoa(code)))
		}
	}
	l.blank()
}

// renderPageLocations writes the locations the pages are served from. Each
// is internal, so a visitor cannot ask for one, and exact, so no other path
// reaches the folder. The response keeps the status that sent it there.
func renderPageLocations(l *lines, spec *SiteSpec) {
	dir := spec.PagesDir
	if dir == "" {
		dir = "/etc/nginx/jd-pages/" + spec.Name
	}
	page := func(name string, extra func()) {
		l.add("    location = %s {", pageURI(name))
		l.add("        internal;")
		l.add("        alias %s/%s.html;", dir, name)
		if extra != nil {
			extra()
		}
		if spec.BasicAuthFile != "" {
			// A maintenance answer comes before the password check, and
			// asking for a password to show a closed sign helps nobody.
			l.add("        auth_basic off;")
		}
		l.add("    }")
	}
	if m := spec.Maintenance; m != nil {
		page("maintenance", func() {
			if m.RetryAfter == 0 {
				return
			}
			l.add("        add_header Retry-After %d always;", m.RetryAfter)
			// An add_header here stops the server's own from applying, so
			// they are said again rather than lost on this response.
			inner := &lines{}
			renderHeaders(inner, spec)
			for _, line := range inner.out {
				l.add("    %s", line)
			}
		})
	}
	for _, code := range errorPageCodes {
		if spec.hasErrorPage(code) {
			page(strconv.Itoa(code), nil)
		}
	}
}

// renderACMEChallenge serves the dashboard's webroot on a site that answers
// plain HTTP itself, so the site form can have certbot issue its certificate
// over HTTP-01 without borrowing a file of another site's. A forced-HTTPS
// site carries it in its redirect server instead. A redirect site is left
// out: its server-level return is answered before any location is chosen.
//
// ^~ so the exploit regexes and a regex path cannot claim the token, and
// its own access rules because Let's Encrypt's validators are neither on an
// allow list nor holding the site's password.
func renderACMEChallenge(l *lines, spec *SiteSpec) {
	if !spec.ManagedACME || (spec.TLS && spec.ForceHTTPS) || spec.Kind == "redirect" {
		return
	}
	l.add("    location ^~ %s {", acmeChallengePath)
	l.add("        root %s;", deploymentACMEWebroot)
	l.add("        allow all;")
	l.add("        auth_basic off;")
	l.add("    }")
	l.blank()
}

// renderRedirectServer is the plain-HTTP half of a TLS site.
func renderRedirectServer(l *lines, names string, managedACME bool) {
	l.add("server {")
	l.add("    listen 80;")
	l.add("    listen [::]:80;")
	l.add("    server_name %s;", names)
	l.blank()
	l.add("    # Let's Encrypt proves control of the domain over plain HTTP, so the")
	l.add("    # challenge path has to survive the redirect or renewal stops working")
	l.add("    # in sixty days and nobody finds out until the certificate expires.")
	l.add("    location %s {", acmeChallengePath)
	root := "/var/www/html"
	if managedACME {
		root = deploymentACMEWebroot
	}
	l.add("        root %s;", root)
	l.add("    }")
	l.blank()
	l.add("    location / {")
	l.add("        return 301 https://$host$request_uri;")
	l.add("    }")
	l.add("}")
}

func renderListen(l *lines, spec *SiteSpec) {
	if !spec.TLS {
		l.add("    listen 80;")
		l.add("    listen [::]:80;")
		return
	}
	l.add("    listen 443 ssl;")
	l.add("    listen [::]:443 ssl;")
	if spec.HTTP2 {
		// The directive rather than the listen parameter: nginx 1.25
		// deprecated `listen ... http2` and warns on every reload.
		l.add("    http2 on;")
	}
	if !spec.ForceHTTPS {
		l.add("    listen 80;")
		l.add("    listen [::]:80;")
	}
}

func renderTLS(l *lines, spec *SiteSpec) {
	l.add("    ssl_certificate     %s;", spec.CertPath)
	l.add("    ssl_certificate_key %s;", spec.KeyPath)
	l.add("    # TLS 1.0 and 1.1 are retired and no current client needs them.")
	l.add("    ssl_protocols TLSv1.2 TLSv1.3;")
	l.add("    # With 1.3 the client's order is the better one; forcing the server's")
	l.add("    # preference is a habit left over from the RC4 era.")
	l.add("    ssl_prefer_server_ciphers off;")
	l.add("    ssl_session_cache shared:SSL:10m;")
	l.add("    ssl_session_timeout 1d;")
	l.add("    ssl_session_tickets off;")
}

func renderHeaders(l *lines, spec *SiteSpec) {
	if spec.HSTS && spec.TLS {
		l.add("    # Six months, which is what browsers and the preload list expect.")
		l.add("    add_header Strict-Transport-Security \"max-age=15552000; includeSubDomains\" always;")
	}
	if spec.SecurityHeaders {
		l.add("    add_header X-Content-Type-Options nosniff always;")
		l.add("    add_header X-Frame-Options SAMEORIGIN always;")
		l.add("    add_header Referrer-Policy strict-origin-when-cross-origin always;")
	}
}

func renderServerOptions(l *lines, spec *SiteSpec) {
	if spec.ClientMaxBody != "" {
		l.add("    client_max_body_size %s;", spec.ClientMaxBody)
	}
	if spec.Gzip {
		l.add("    gzip on;")
		l.add("    gzip_vary on;")
		l.add("    gzip_types text/plain text/css application/json application/javascript text/xml application/xml image/svg+xml;")
	} else {
		// Said rather than left out: Debian's nginx.conf turns gzip on for
		// the whole http block, and a site saying nothing inherits it.
		l.add("    gzip off;")
	}
	if spec.AccessLog {
		if spec.timedLog() {
			l.add("    access_log /var/log/nginx/%s.access.log %s;", spec.Name, spec.logFormatName())
		} else {
			l.add("    access_log /var/log/nginx/%s.access.log;", spec.Name)
		}
		l.add("    error_log  /var/log/nginx/%s.error.log;", spec.Name)
	} else {
		l.add("    access_log off;")
	}
	l.blank()
}

func renderAccess(l *lines, spec *SiteSpec) {
	wrote := false
	if spec.BasicAuthFile != "" {
		realm := spec.BasicAuthRealm
		if realm == "" {
			realm = "Restricted"
		}
		l.add("    auth_basic \"%s\";", realm)
		l.add("    auth_basic_user_file %s;", spec.BasicAuthFile)
		wrote = true
	}
	if len(spec.AllowFrom) > 0 || len(spec.DenyFrom) > 0 {
		l.add("    # nginx reads these in order and stops at the first match, so the")
		l.add("    # exceptions come first and the fence goes last.")
		renderAddressList(l, "    ", spec.AllowFrom, spec.DenyFrom)
		wrote = true
	}
	if wrote {
		l.blank()
	}
}

// renderAddressList writes allow and deny lines at indent.
func renderAddressList(l *lines, indent string, allow, deny []string) {
	// Denials first, because first match wins: an address that is inside
	// an allowed range and named on the deny list is meant to be refused,
	// and the other order would let the range answer for it.
	for _, entry := range deny {
		entry = strings.TrimSpace(entry)
		if entry == "all" {
			continue
		}
		l.add("%sdeny %s;", indent, entry)
	}
	for _, entry := range allow {
		l.add("%sallow %s;", indent, strings.TrimSpace(entry))
	}
	// An allow list with nothing after it allows everybody: nginx falls
	// through to its default, which is to permit. The operator used to
	// have to know that and write the fence themselves, in a box labelled
	// "deny from" — and the address they would reach for, 0.0.0.0/0, lets
	// in every IPv6 client on the internet. A control labelled "only these
	// addresses" has to mean it, so the fence is written here.
	if len(allow) > 0 || containsDenyAll(deny) {
		l.add("%sdeny all;", indent)
	}
}

// systemCABundle is where Debian, Ubuntu and Alpine keep the system's CAs
// as one file, which is the form proxy_ssl_trusted_certificate reads.
const systemCABundle = "/etc/ssl/certs/ca-certificates.crt"

// renderUpstreamTLS writes how nginx talks to an HTTPS upstream. At server
// level, so every location that forwards inherits it.
func renderUpstreamTLS(l *lines, spec *SiteSpec) {
	if spec.Kind != "proxy" || (!spec.UpstreamSNI && !spec.UpstreamVerify) {
		return
	}
	if spec.UpstreamSNI {
		l.add("    # nginx sends no name in the handshake with an HTTPS upstream unless")
		l.add("    # told to, and a host serving several names needs one.")
		l.add("    proxy_ssl_server_name on;")
	}
	if spec.UpstreamTLSName != "" {
		l.add("    proxy_ssl_name %s;", spec.UpstreamTLSName)
	}
	if spec.UpstreamVerify {
		ca := spec.UpstreamCA
		if ca == "" {
			ca = systemCABundle
		}
		l.add("    # The upstream's certificate is checked; nginx accepts any by default.")
		l.add("    proxy_ssl_verify on;")
		l.add("    # The default depth of 1 fails a chain with an intermediate.")
		l.add("    proxy_ssl_verify_depth 3;")
		l.add("    proxy_ssl_trusted_certificate %s;", ca)
	}
	l.blank()
}

// stripMarker introduces the comment a stripping location carries, which is
// how the parser tells it from an upstream that merely ends in a slash.
const stripMarker = "# Strips the path before forwarding"

func renderLocation(l *lines, loc SiteLocation, spec *SiteSpec) {
	if loc.Match != "" {
		l.add("    location %s %s {", loc.Match, loc.renderedPath())
	} else {
		l.add("    location %s {", loc.renderedPath())
	}
	renderLocationLimit(l, loc, spec)
	renderLocationAccess(l, loc, spec)
	if loc.servesFolder() {
		if loc.RootMode == "root" {
			l.add("        root %s;", loc.Root)
		} else {
			l.add("        alias %s/;", strings.TrimSuffix(loc.Root, "/"))
		}
		if loc.SPA {
			l.add("        # A path with no file of its own gets the folder's index.html,")
			l.add("        # for a single-page app whose router runs in the browser.")
			l.add("        try_files $uri $uri/ %sindex.html;", loc.renderedPath())
		} else {
			l.add("        try_files $uri $uri/ =404;")
		}
		l.add("    }")
		return
	}
	if loc.StripPrefix {
		_, uri := splitUpstream(loc.renderedUpstream())
		l.add("        %s: %spage reaches the application as %spage.", stripMarker, loc.renderedPath(), uri)
	}
	// As typed: a path on the upstream is how nginx is told to replace the
	// location's own prefix, and trimming its slash turned /app/ into /app,
	// which sent /page to the application as /apppage.
	l.add("        proxy_pass %s;", proxyPassTarget(loc.renderedPath(), loc.renderedUpstream()))
	if spec.Pool != nil && loc.Upstream == spec.poolUpstream() {
		renderPoolLocation(l, loc, spec)
	}
	l.add("        proxy_http_version 1.1;")
	l.add("        # The application sees the visitor's address and scheme rather")
	l.add("        # than the proxy's, which is what makes redirects, cookies and")
	l.add("        # rate limits behind this proxy behave.")
	l.add("        proxy_set_header Host              %s;", hostHeader(spec))
	l.add("        proxy_set_header X-Real-IP         $remote_addr;")
	l.add("        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;")
	l.add("        proxy_set_header X-Forwarded-Proto $scheme;")
	l.add("        proxy_set_header X-Forwarded-Host  $host;")
	if loc.WebSockets {
		l.add("        proxy_set_header Upgrade    $http_upgrade;")
		l.add("        proxy_set_header Connection $%s;", spec.connectionVar())
	}
	if loc.BodyLimit != "" {
		l.add("        client_max_body_size %s;", loc.BodyLimit)
	}
	timeout := spec.ProxyTimeout
	if loc.Timeout > 0 {
		timeout = loc.Timeout
	}
	if timeout > 0 {
		value := strconv.Itoa(timeout) + "s"
		l.add("        proxy_connect_timeout %s;", value)
		l.add("        proxy_send_timeout    %s;", value)
		l.add("        proxy_read_timeout    %s;", value)
	}
	if spec.Kind == "proxy" && spec.InterceptErrors {
		l.add("        # The application's own error responses get the site's pages too.")
		l.add("        proxy_intercept_errors on;")
	}
	if spec.Kind == "proxy" {
		buffering := onOff(spec.Buffering)
		if loc.Buffering != "" {
			buffering = loc.Buffering
		}
		if buffering == "off" {
			l.add("        # Streamed responses arrive as they are produced rather than")
			l.add("        # being held until nginx has the whole body.")
		}
		l.add("        proxy_buffering %s;", buffering)
		requestBuffering := onOff(!spec.StreamUploads)
		if loc.RequestBuffering != "" {
			requestBuffering = loc.RequestBuffering
		}
		// Written when it differs from nginx's default, or when the path
		// turns back on what the site turned off.
		if requestBuffering == "off" || spec.StreamUploads {
			l.add("        proxy_request_buffering %s;", requestBuffering)
		}
	}
	l.add("    }")
}

// renderLocationAccess writes a path's own password and address list, which
// replace the site's there.
func renderLocationAccess(l *lines, loc SiteLocation, spec *SiteSpec) {
	if loc.BasicAuthFile != "" {
		realm := spec.BasicAuthRealm
		if realm == "" {
			realm = "Restricted"
		}
		l.add("        auth_basic \"%s\";", realm)
		l.add("        auth_basic_user_file %s;", loc.BasicAuthFile)
	}
	if len(loc.AllowFrom) > 0 || len(loc.DenyFrom) > 0 {
		l.add("        # This path's own list; the site's does not apply here.")
		renderAddressList(l, "        ", loc.AllowFrom, loc.DenyFrom)
	}
}

// hostHeader is the Host the application is sent.
func hostHeader(spec *SiteSpec) string {
	switch spec.HostHeader {
	case "upstream":
		return "$proxy_host"
	case "custom":
		return spec.HostHeaderValue
	}
	return "$host"
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// proxyPassTarget is the upstream as proxy_pass spells it for a location at
// path. nginx refuses a bare unix: address ("invalid URL prefix"); a socket is
// http://unix:<path>.
//
// A path on the upstream that is the location's own prefix is left off. The
// swap it asks for changes nothing, and it is not free: nginx forwards the
// request exactly as the client sent it only to an upstream without a path,
// and to one with a path it sends the path decoded, so http://x/ on / turned
// /pkg/%40scope%2Fname into /pkg/@scope/name — the pasted form of an address
// quietly breaking every registry and repository path that carries a %2F.
func proxyPassTarget(path, upstream string) string {
	if address, uri := splitUpstream(upstream); uri == path {
		upstream = address
	}
	if strings.HasPrefix(upstream, "unix:") {
		return "http://" + upstream
	}
	return upstream
}

// splitUpstream parts an upstream into its address and the path after it,
// where nginx reads them apart: after the host of a URL, and after the colon
// that ends a socket's path in unix:/run/app.sock:/uri.
func splitUpstream(upstream string) (address, uri string) {
	if socket, ok := strings.CutPrefix(upstream, "unix:"); ok {
		if i := strings.Index(socket, ":"); i >= 0 {
			return "unix:" + socket[:i], socket[i+1:]
		}
		return upstream, ""
	}
	scheme := strings.Index(upstream, "://")
	if scheme < 0 {
		return upstream, ""
	}
	host := scheme + len("://")
	if i := strings.Index(upstream[host:], "/"); i >= 0 {
		return upstream[:host+i], upstream[host+i:]
	}
	return upstream, ""
}

func renderExploitBlocks(l *lines) {
	l.add("    # The shapes scanners ask for constantly. Refusing them costs nothing")
	l.add("    # and keeps the log readable; it is not a substitute for the")
	l.add("    # application being sound.")
	l.add("    location ~ %s {", exploitDotLocation)
	l.add("        deny all;")
	l.add("    }")
	l.add("    location ~* %s {", exploitExtLocation)
	l.add("        deny all;")
	l.add("    }")
}
