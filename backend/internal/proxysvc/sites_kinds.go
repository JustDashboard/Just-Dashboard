package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// What a site answers with beyond forwarding: how a redirect redirects, a
// canonical www or bare name, the options of a static folder, and PHP-FPM.

// Canonical is which of a name and its www. form visitors end up on.
const (
	canonicalWWW  = "www"
	canonicalApex = "apex"
)

// phpLocation and htLocation are the locations a PHP site's renderer writes
// beside location /; the parser reads them back as the kind, not as paths.
const (
	phpLocation = `\.php$`
	htLocation  = `/\.ht`
)

// phpSocketDir is where Debian's and Ubuntu's php-fpm pools put their
// sockets, and so where the form looks for them.
const phpSocketDir = "/run/php"

var (
	indexFileRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	// canonicalMarkerRe opens each server that sends a name to its canonical
	// form. The parser skips that server whole: its server_name is the other
	// name, which is not one of the site's domains.
	canonicalMarkerRe = regexp.MustCompile(`^# Canonical host \((www|apex)\)`)
)

// redirectCode is the status a redirect site answers with. A spec from
// before the code could be chosen has only Permanent.
func (spec *SiteSpec) redirectCode() int {
	if spec.RedirectCode != 0 {
		return spec.RedirectCode
	}
	if spec.Permanent {
		return 301
	}
	return 302
}

// defaultIndex is the index order a kind gets when the form names none.
func (spec *SiteSpec) defaultIndex() []string {
	if spec.Kind == "php" {
		return []string{"index.php", "index.html"}
	}
	return []string{"index.html", "index.htm"}
}

func (spec *SiteSpec) indexFiles() []string {
	if len(spec.IndexFiles) > 0 {
		return spec.IndexFiles
	}
	return spec.defaultIndex()
}

// canonicalPairs is each name the canonical servers answer, with the name
// it sends visitors to.
func (spec *SiteSpec) canonicalPairs() [][2]string {
	var pairs [][2]string
	for _, d := range spec.Domains {
		lower := strings.ToLower(d)
		if strings.HasPrefix(lower, "*.") {
			continue
		}
		switch spec.Canonical {
		case canonicalWWW:
			if bare, ok := strings.CutPrefix(lower, "www."); ok {
				pairs = append(pairs, [2]string{bare, d})
			}
		case canonicalApex:
			if !strings.HasPrefix(lower, "www.") {
				pairs = append(pairs, [2]string{"www." + lower, d})
			}
		}
	}
	return pairs
}

func validateKinds(spec *SiteSpec) error {
	switch spec.RedirectCode {
	case 0, 301, 302, 307, 308:
	default:
		return fmt.Errorf("a redirect answers 301, 302, 307 or 308, not %d", spec.RedirectCode)
	}
	if len(spec.IndexFiles) > 8 {
		return fmt.Errorf("list at most eight index files")
	}
	seen := map[string]bool{}
	for _, name := range spec.IndexFiles {
		if !indexFileRe.MatchString(name) {
			return fmt.Errorf("%q is not a file name nginx can look for as an index", name)
		}
		if seen[name] {
			return fmt.Errorf("%s is listed twice as an index file", name)
		}
		seen[name] = true
	}
	switch spec.Canonical {
	case "":
	case canonicalWWW, canonicalApex:
		pairs := spec.canonicalPairs()
		if len(pairs) == 0 {
			if spec.Canonical == canonicalWWW {
				return fmt.Errorf("sending visitors to www needs a www. name among the domains")
			}
			return fmt.Errorf("sending visitors to the bare name needs a domain without www.")
		}
		domains := map[string]bool{}
		for _, d := range spec.Domains {
			domains[strings.ToLower(d)] = true
		}
		for _, pair := range pairs {
			if domains[pair[0]] {
				return fmt.Errorf("%s is one of the site's domains and would also be sent to %s — remove it from the domains", pair[0], pair[1])
			}
			if !domainRe.MatchString(pair[0]) {
				return fmt.Errorf("%s is not a valid domain name", pair[0])
			}
		}
	default:
		return fmt.Errorf("the canonical host is www, the bare name or neither")
	}
	if spec.Kind == "php" {
		if !absPathRe.MatchString(spec.Root) {
			return fmt.Errorf("root must be an absolute path")
		}
		if !absPathRe.MatchString(spec.PHPSocket) {
			return fmt.Errorf("the PHP-FPM socket must be an absolute path, for example /run/php/php8.3-fpm.sock")
		}
	}
	return nil
}

func kindWarnings(spec *SiteSpec) []string {
	var warnings []string
	if spec.Kind == "static" && spec.Autoindex {
		warnings = append(warnings,
			"Folder listings show every file in a folder without an index to anyone who can reach the site.")
	}
	if spec.TLS && spec.Canonical != "" {
		names := []string{}
		for _, pair := range spec.canonicalPairs() {
			names = append(names, pair[0])
		}
		warnings = append(warnings, fmt.Sprintf(
			"The certificate must also cover %s: a visitor asking for it over HTTPS gets a certificate error before the redirect.",
			strings.Join(names, ", ")))
	}
	return warnings
}

// renderRedirect is a redirect site's answer.
func renderRedirect(l *lines, spec *SiteSpec) {
	if spec.RedirectDropPath {
		l.add("    # Every path lands on the same address.")
		l.add("    return %d %s;", spec.redirectCode(), spec.RedirectTo)
		return
	}
	l.add("    # $request_uri keeps the path and query, so a bookmark deeper than")
	l.add("    # the home page still lands somewhere useful.")
	l.add("    return %d %s$request_uri;", spec.redirectCode(), strings.TrimSuffix(spec.RedirectTo, "/"))
}

// renderStatic serves the site's folder.
func renderStatic(l *lines, spec *SiteSpec) {
	l.add("    root %s;", spec.Root)
	l.add("    index %s;", strings.Join(spec.indexFiles(), " "))
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
	if spec.Autoindex {
		l.add("        # A folder without an index is answered with its listing.")
		l.add("        autoindex on;")
	}
	if spec.GzipStatic {
		l.add("        # A file.gz beside a file is sent in its place to a client that")
		l.add("        # accepts gzip, compressed once at build time rather than per request.")
		l.add("        gzip_static on;")
	}
	l.add("    }")
}

// renderPHP hands .php scripts to PHP-FPM and serves everything else from
// the folder.
//
// fastcgi_params with SCRIPT_FILENAME rather than Debian's
// snippets/fastcgi-php.conf: the file is in every nginx build, the snippet
// only in Debian's, and the one thing the snippet adds is PATH_INFO, which
// WordPress and Laravel do not use.
func renderPHP(l *lines, spec *SiteSpec) {
	l.add("    root %s;", spec.Root)
	l.add("    index %s;", strings.Join(spec.indexFiles(), " "))
	l.blank()
	l.add("    location / {")
	if spec.PHPFrontController {
		l.add("        # A path with no file of its own goes to index.php, the front")
		l.add("        # controller WordPress and Laravel route every request through.")
		l.add("        try_files $uri $uri/ /index.php?$query_string;")
	} else {
		l.add("        try_files $uri $uri/ =404;")
	}
	l.add("    }")
	l.blank()
	l.add("    location ~ %s {", phpLocation)
	l.add("        # A script that is not on disk is refused here, rather than PHP")
	l.add("        # being asked to find the nearest file that is and run that.")
	l.add("        try_files $uri =404;")
	l.add("        include fastcgi_params;")
	l.add("        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;")
	l.add("        fastcgi_pass unix:%s;", spec.PHPSocket)
	l.add("    }")
	l.blank()
	l.add("    # .htaccess and .htpasswd are Apache's, and hold nothing to serve.")
	l.add("    location ~ %s {", htLocation)
	l.add("        deny all;")
	l.add("    }")
}

// renderCanonicalServers answers each other name with a redirect to the
// site's own, over plain HTTP and, when the site has a certificate, HTTPS.
func renderCanonicalServers(l *lines, spec *SiteSpec) {
	scheme := "$scheme"
	if spec.TLS && spec.ForceHTTPS {
		scheme = "https"
	}
	for _, pair := range spec.canonicalPairs() {
		l.add("# Canonical host (%s): %s is sent to %s.", spec.Canonical, pair[0], pair[1])
		l.add("server {")
		l.add("    listen 80;")
		l.add("    listen [::]:80;")
		if spec.TLS {
			l.add("    listen 443 ssl;")
			l.add("    listen [::]:443 ssl;")
			if spec.HTTP2 {
				l.add("    http2 on;")
			}
		}
		l.add("    server_name %s;", pair[0])
		if spec.TLS {
			l.add("    ssl_certificate     %s;", spec.CertPath)
			l.add("    ssl_certificate_key %s;", spec.KeyPath)
		}
		l.blank()
		// So a certificate covering this name can be issued and renewed.
		root := "/var/www/html"
		if spec.ManagedACME {
			root = deploymentACMEWebroot
		}
		l.add("    location ^~ %s {", acmeChallengePath)
		l.add("        root %s;", root)
		l.add("    }")
		l.add("    location / {")
		l.add("        return 301 %s://%s$request_uri;", scheme, pair[1])
		l.add("    }")
		l.add("}")
		l.blank()
	}
}

// kindParse is what the parser collects for the options here until it knows
// the site's kind.
type kindParse struct {
	index      []string
	phpSocket  string
	front      bool
	autoindex  bool
	gzipStatic bool
	// Inside a canonical-host server, which is skipped whole.
	skipServer bool
}

// directive reads one statement, and says whether it was this file's.
func (k *kindParse) directive(directive, value, location string) bool {
	switch {
	case directive == "try_files" && location == "/":
		fields := strings.Fields(value)
		k.front = len(fields) > 0 && fields[len(fields)-1] == "/index.php?$query_string"
		return false
	case directive == "index" && location == "":
		k.index = strings.Fields(value)
	case directive == "autoindex" && (location == "" || location == "/"):
		k.autoindex = value == "on"
	case directive == "gzip_static" && (location == "" || location == "/"):
		k.gzipStatic = value == "on"
	case directive == "fastcgi_pass" && location == phpLocation:
		k.phpSocket = strings.TrimPrefix(value, "unix:")
	case location == phpLocation || location == htLocation:
	default:
		return false
	}
	return true
}

// settle decides the kind and keeps what applies to it. An index order
// that is the kind's own default is left unset, so the form shows none.
func (k *kindParse) settle(spec *SiteSpec) {
	if spec.Kind == "static" && k.phpSocket != "" {
		spec.Kind = "php"
		spec.PHPSocket = k.phpSocket
		spec.PHPFrontController = k.front
	}
	if spec.Kind == "static" {
		spec.Autoindex, spec.GzipStatic = k.autoindex, k.gzipStatic
	}
	if (spec.Kind == "static" || spec.Kind == "php") && !slices.Equal(k.index, spec.defaultIndex()) {
		spec.IndexFiles = k.index
	}
}

// ListPHPSockets is every socket in /run/php on the host, which is where
// nginx reaches them. A host without PHP has no such folder, and no sockets.
func ListPHPSockets(ctx context.Context) ([]string, error) {
	raw, err := hostexec.CommandOnHost(ctx, "find", phpSocketDir, "-maxdepth", "1", "-type", "s").Output()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return nil, err
	}
	sockets := []string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if absPathRe.MatchString(line) {
			sockets = append(sockets, line)
		}
	}
	slices.Sort(sockets)
	return sockets, nil
}
