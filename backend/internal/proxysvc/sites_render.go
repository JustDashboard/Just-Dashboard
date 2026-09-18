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
	renderAccess(l, spec)

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
		l.add("        try_files $uri $uri/ =404;")
		l.add("    }")
	default:
		for _, loc := range spec.Locations {
			renderLocation(l, loc.Path, loc.Upstream, loc.Root, loc.WebSockets, spec)
			l.blank()
		}
		renderLocation(l, "/", spec.Upstream, "", spec.WebSockets, spec)
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
	wrote := false
	if spec.ClientMaxBody != "" {
		l.add("    client_max_body_size %s;", spec.ClientMaxBody)
		wrote = true
	}
	if spec.Gzip {
		l.add("    gzip on;")
		l.add("    gzip_vary on;")
		l.add("    gzip_types text/plain text/css application/json application/javascript text/xml application/xml image/svg+xml;")
		wrote = true
	}
	if spec.AccessLog {
		l.add("    access_log /var/log/nginx/%s.access.log;", spec.Name)
		l.add("    error_log  /var/log/nginx/%s.error.log;", spec.Name)
		wrote = true
	} else {
		l.add("    access_log off;")
		wrote = true
	}
	if wrote {
		l.blank()
	}
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
		// Denials first, because first match wins: an address that is inside
		// an allowed range and named on the deny list is meant to be refused,
		// and the other order would let the range answer for it.
		for _, entry := range spec.DenyFrom {
			entry = strings.TrimSpace(entry)
			if entry == "all" {
				continue
			}
			l.add("    deny %s;", entry)
		}
		for _, entry := range spec.AllowFrom {
			l.add("    allow %s;", strings.TrimSpace(entry))
		}
		// An allow list with nothing after it allows everybody: nginx falls
		// through to its default, which is to permit. The operator used to
		// have to know that and write the fence themselves, in a box labelled
		// "deny from" — and the address they would reach for, 0.0.0.0/0, lets
		// in every IPv6 client on the internet. A control labelled "only these
		// addresses" has to mean it, so the fence is written here.
		if len(spec.AllowFrom) > 0 || containsDenyAll(spec.DenyFrom) {
			l.add("    deny all;")
		}
		wrote = true
	}
	if wrote {
		l.blank()
	}
}

func renderLocation(l *lines, path, upstream, root string, websockets bool, spec *SiteSpec) {
	l.add("    location %s {", path)
	if root != "" {
		l.add("        root %s;", root)
		l.add("        try_files $uri $uri/ =404;")
		l.add("    }")
		return
	}
	l.add("        proxy_pass %s;", strings.TrimSuffix(upstream, "/"))
	l.add("        proxy_http_version 1.1;")
	l.add("        # The application sees the visitor's address and scheme rather")
	l.add("        # than the proxy's, which is what makes redirects, cookies and")
	l.add("        # rate limits behind this proxy behave.")
	l.add("        proxy_set_header Host              $host;")
	l.add("        proxy_set_header X-Real-IP         $remote_addr;")
	l.add("        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;")
	l.add("        proxy_set_header X-Forwarded-Proto $scheme;")
	l.add("        proxy_set_header X-Forwarded-Host  $host;")
	if websockets {
		l.add("        # Passing the client's own Connection header through, rather than")
		l.add("        # the usual $connection_upgrade map: a map is only legal in the")
		l.add("        # http block, and a site file cannot reach there. This form keeps")
		l.add("        # keep-alive working for ordinary requests and upgrades the ones")
		l.add("        # that ask to be upgraded.")
		l.add("        proxy_set_header Upgrade    $http_upgrade;")
		l.add("        proxy_set_header Connection $http_connection;")
	}
	if spec.ProxyTimeout > 0 {
		timeout := strconv.Itoa(spec.ProxyTimeout) + "s"
		l.add("        proxy_connect_timeout %s;", timeout)
		l.add("        proxy_send_timeout    %s;", timeout)
		l.add("        proxy_read_timeout    %s;", timeout)
	}
	if spec.Kind == "proxy" {
		l.add("        # Streamed responses arrive as they are produced rather than")
		l.add("        # being held until nginx has the whole body.")
		l.add("        proxy_buffering off;")
	}
	l.add("    }")
}

func renderExploitBlocks(l *lines) {
	l.add("    # The shapes scanners ask for constantly. Refusing them costs nothing")
	l.add("    # and keeps the log readable; it is not a substitute for the")
	l.add("    # application being sound.")
	l.add("    location ~ %s {", exploitDotLocation)
	l.add("        deny all;")
	l.add("    }")
	l.add("    location ~* \\.(sql|bak|old|orig|save|swp|env)$ {")
	l.add("        deny all;")
	l.add("    }")
}
