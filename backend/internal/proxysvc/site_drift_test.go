package proxysvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// driftSpecs are one site of every shape the form writes.
func driftSpecs() map[string]*SiteSpec {
	plain := proxySpec()
	plain.TLS, plain.ForceHTTPS, plain.HSTS, plain.HTTP2 = false, false, false, false
	plain.CertPath, plain.KeyPath = "", ""
	both := proxySpec()
	both.ForceHTTPS = false
	routed := proxySpec()
	routed.Locations = []SiteLocation{
		{Path: "/api/", Upstream: "http://127.0.0.1:4000/", WebSockets: true},
		{Path: "/ws", Upstream: "unix:/run/app.sock"},
		{Path: "/assets", Root: "/srv/app/assets"},
		{Path: "/legacy", Root: "/srv/legacy", RootMode: "root"},
	}
	fenced := proxySpec()
	fenced.AllowFrom, fenced.DenyFrom = []string{"10.0.0.0/8"}, []string{"10.0.0.9"}
	fenced.BasicAuthFile, fenced.BasicAuthRealm = "/etc/nginx/jd-auth/staff", "Staff"
	fenced.BlockExploits, fenced.ManagedACME = true, true
	fenced.Custom = "client_body_buffer_size 1m;\nlocation = /health {\n    return 200 ok;\n}"
	static := proxySpec()
	static.Kind, static.Upstream, static.Root = "static", "", "/srv/www"
	spa := proxySpec()
	spa.Kind, spa.Upstream, spa.Root, spa.SPA = "static", "", "/srv/app", true
	redirect := proxySpec()
	redirect.Kind, redirect.Upstream, redirect.RedirectTo, redirect.Permanent = "redirect", "", "https://new.example.com", true
	plainRedirect := proxySpec()
	plainRedirect.Kind, plainRedirect.Upstream, plainRedirect.RedirectTo = "redirect", "", "https://new.example.com"
	plainRedirect.TLS, plainRedirect.ForceHTTPS, plainRedirect.CertPath, plainRedirect.KeyPath = false, false, "", ""
	quiet := proxySpec()
	quiet.Gzip, quiet.AccessLog, quiet.SecurityHeaders, quiet.HSTS, quiet.WebSockets = false, false, false, false, false
	quiet.ClientMaxBody, quiet.ProxyTimeout, quiet.Upstream = "", 0, "http://127.0.0.1:3000/"
	return map[string]*SiteSpec{
		"proxy over https": proxySpec(), "plain proxy": plain, "https beside http": both,
		"paths": routed, "access controls and extra configuration": fenced, "files": static,
		"single-page app": spa, "redirect": redirect, "plain redirect": plainRedirect, "switches off": quiet,
	}
}

// A file the form wrote and nobody touched loses nothing, whether it is
// compared with the form's reading of it or with the spec that wrote it.
func TestDroppedLinesOfAnUntouchedFileAreNone(t *testing.T) {
	for name, spec := range driftSpecs() {
		t.Run(name, func(t *testing.T) {
			content, err := RenderNginx(spec)
			if err != nil {
				t.Fatal(err)
			}
			read, _ := ParseSiteSpec(spec.Name, content)
			for _, against := range []*SiteSpec{spec, read} {
				dropped, err := DroppedLines("/etc/nginx/sites-available/app", content, against, nil)
				if err != nil || len(dropped) != 0 {
					t.Fatalf("dropped %+v (%v) from\n%s", dropped, err, content)
				}
			}
		})
	}
}

// probeEdits are the hand edits a form save used to drop without a word: a
// second port and a body buffer in the server block, and a header sent to
// the application from location / whose value mixes text and a variable,
// which the headers section cannot hold.
func probeEdits(t *testing.T) (string, *SiteSpec) {
	t.Helper()
	content, err := RenderNginx(proxySpec())
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(content, "    server_name app.example.com;\n\n    ssl_certificate",
		"    server_name app.example.com;\n    listen 8080;\n    client_body_buffer_size 1m;\n\n    ssl_certificate", 1)
	edited = strings.Replace(edited, "        proxy_set_header X-Forwarded-Host  $host;\n",
		"        proxy_set_header X-Forwarded-Host  $host;\n        proxy_set_header X-Tenant acme$is_args;  # for the billing app\n", 1)
	if edited == content {
		t.Fatal("the probe edits did not apply")
	}
	read, managed := ParseSiteSpec("app", edited)
	if !managed {
		t.Fatal("the edited file lost its marker")
	}
	return edited, read
}

func lineOf(content, text string) int {
	for i, line := range strings.Split(content, "\n") {
		if strings.Contains(line, text) {
			return i + 1
		}
	}
	return 0
}

func TestDroppedLinesReportHandEditsWhereTheyAre(t *testing.T) {
	edited, read := probeEdits(t)
	dropped, err := DroppedLines("/etc/nginx/sites-available/app", edited, read, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []DroppedLine{
		{Line: lineOf(edited, "listen 8080"), Lines: 1, Text: "listen 8080;", Context: "server", Movable: true},
		{Line: lineOf(edited, "client_body_buffer_size"), Lines: 1, Text: "client_body_buffer_size 1m;", Context: "server", Movable: true},
		// Its own line, comment and all, and nowhere in the form to go: a
		// proxy_set_header in the server block is not inherited by a
		// location that sets its own.
		{Line: lineOf(edited, "X-Tenant"), Lines: 1, Text: "proxy_set_header X-Tenant acme$is_args;  # for the billing app", Context: "location /"},
	}
	if !reflect.DeepEqual(dropped, want) {
		t.Fatalf("dropped\n%+v\nwant\n%+v", dropped, want)
	}
}

// Lines moved into the extra configuration are written back, so they are no
// longer dropped; an edit in the form is a change, not a dropped line.
func TestSiteDriftForgetsLinesMovedIntoTheExtraConfiguration(t *testing.T) {
	edited, read := probeEdits(t)
	service := New(t.TempDir(), "")
	path := "/etc/nginx/sites-available/app"
	all, err := service.SiteDrift("app", path, edited, nil)
	if err != nil || len(all) != 3 {
		t.Fatalf("as read: %+v %v", all, err)
	}
	moved := *read
	moved.Custom = "listen 8080;\nclient_body_buffer_size 1m;"
	// And an edit of the operator's own: a new upload limit is not a line
	// the save drops.
	moved.ClientMaxBody = "10m"
	left, err := service.SiteDrift("app", path, edited, &moved)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].Context != "location /" || !strings.HasPrefix(left[0].Text, "proxy_set_header X-Tenant") {
		t.Fatalf("after the move: %+v", left)
	}
}

// certbot's --nginx rewrites a site into two blocks with a comment on every
// line it adds. What the form rewrites of that is said, the lines it writes
// its own way are not offered as a move, and the ones that are, move.
func TestDroppedLinesOfACertbotFile(t *testing.T) {
	content := `server {
    server_name app.example.com;
    location / { proxy_pass http://127.0.0.1:3000/; proxy_set_header Host $host; }

    listen 443 ssl http2; # managed by Certbot
    ssl_certificate /etc/letsencrypt/live/app.example.com/fullchain.pem; # managed by Certbot
    ssl_certificate_key /etc/letsencrypt/live/app.example.com/privkey.pem; # managed by Certbot
    include /etc/letsencrypt/options-ssl-nginx.conf; # managed by Certbot
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem; # managed by Certbot
}
server {
    if ($host = app.example.com) {
        return 301 https://$host$request_uri;
    } # managed by Certbot
    listen 80;
    server_name app.example.com;
    return 404; # managed by Certbot
}
`
	read, managed := ParseSiteSpec("app", content)
	if managed || !read.TLS || !read.ForceHTTPS || !read.HTTP2 ||
		read.CertPath != "/etc/letsencrypt/live/app.example.com/fullchain.pem" {
		t.Fatalf("read as %+v", read)
	}
	options := func(pattern string) []string {
		if pattern == "/etc/letsencrypt/options-ssl-nginx.conf" {
			return []string{"ssl_session_cache", "ssl_session_timeout", "ssl_session_tickets", "ssl_protocols", "ssl_prefer_server_ciphers", "ssl_ciphers"}
		}
		return nil
	}
	dropped, err := DroppedLines("/etc/nginx/sites-available/app", content, read, options)
	if err != nil {
		t.Fatal(err)
	}
	want := []DroppedLine{
		{Line: 8, Lines: 1, Text: "include /etc/letsencrypt/options-ssl-nginx.conf; # managed by Certbot", Context: "server",
			Reason: "the included file sets ssl_session_timeout, which the form writes itself"},
		{Line: 9, Lines: 1, Text: "ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem; # managed by Certbot", Context: "server", Movable: true},
		{Line: 12, Lines: 3, Text: "if ($host = app.example.com) {\n    return 301 https://$host$request_uri;\n} # managed by Certbot", Context: "server on port 80"},
		{Line: 17, Lines: 1, Text: "return 404; # managed by Certbot", Context: "server on port 80"},
	}
	if !reflect.DeepEqual(dropped, want) {
		t.Fatalf("dropped\n%+v\nwant\n%+v", dropped, want)
	}
}

// The same statement spelled another way is the same statement.
func TestDroppedLinesCountOtherSpellingsAsTheSame(t *testing.T) {
	content := `server {
    listen *:80;
    listen [::]:80;
    server_name app.example.com;
    server_name www.app.example.com;
    gzip on; gzip_vary on;
    gzip_types text/plain text/css application/json application/javascript text/xml application/xml image/svg+xml;
    access_log /var/log/nginx/app.access.log;
    error_log  /var/log/nginx/app.error.log;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Frame-Options SAMEORIGIN always;
    add_header Referrer-Policy 'strict-origin-when-cross-origin' always;

    location /static {
        alias /srv/static;
    }
    location /sock/ {
        proxy_pass http://unix:/run/app.sock:/sock/;
        proxy_http_version 1.1;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host  $host;
        proxy_buffering off;
    }
    location / {
        proxy_pass http://127.0.0.1:3000/;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host $host;
        proxy_buffering off;
    }
}
`
	read, _ := ParseSiteSpec("app", content)
	dropped, err := DroppedLines("/etc/nginx/sites-available/app", content, read, nil)
	if err != nil || len(dropped) != 0 {
		t.Fatalf("dropped %+v (%v)", dropped, err)
	}
}

// What the form cannot move says why, and what it can move moves once.
func TestDroppedLinesSayWhichCanMove(t *testing.T) {
	content, err := RenderNginx(proxySpec())
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(content, "    server_name app.example.com;\n\n    ssl_certificate",
		"    server_name app.example.com;\n"+
			"    add_header X-Frame-Options DENY always;\n"+
			"    add_header X-Served-By $hostname always;\n"+
			"    access_log /var/log/nginx/custom.log;\n"+
			"    location ~ \\.php$ { fastcgi_pass unix:/run/php.sock; }\n"+
			"    client_body_buffer_size 1m;\n"+
			"    client_body_buffer_size 1m;\n\n"+
			"    ssl_certificate", 1)
	edited = "upstream app_pool {\n    server 127.0.0.1:3000;\n}\n\n" + edited +
		"\nserver {\n    listen 80 default_server;\n    return 444;\n}\n"
	read, _ := ParseSiteSpec("app", edited)
	dropped, err := DroppedLines("/etc/nginx/sites-available/app", edited, read, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]DroppedLine{}
	for _, d := range dropped {
		got[strings.SplitN(d.Text, "\n", 2)[0]] = d
	}
	expect := func(text string, movable bool, context, reason string) {
		t.Helper()
		d, ok := got[text]
		if !ok {
			t.Fatalf("%q is not dropped: %+v", text, dropped)
		}
		if d.Movable != movable || d.Context != context || d.Reason != reason {
			t.Fatalf("%q: %+v", text, d)
		}
	}
	expect("upstream app_pool {", false, "outside any server", "")
	expect("add_header X-Frame-Options DENY always;", false, "server", "the form writes its own X-Frame-Options header")
	expect("add_header X-Served-By $hostname always;", true, "server", "")
	expect("access_log /var/log/nginx/custom.log;", false, "server", "the form writes its own access_log")
	expect(`location ~ \.php$ { fastcgi_pass unix:/run/php.sock; }`, true, "server", "")
	expect("server {", false, "server on port 80", "")
	if len(dropped) != 8 {
		t.Fatalf("dropped %d: %+v", len(dropped), dropped)
	}
	first, second := dropped[5], dropped[6]
	if first.Text != "client_body_buffer_size 1m;" || !first.Movable ||
		second.Text != first.Text || second.Movable || second.Reason != "the same as line "+strconv.Itoa(first.Line)+", which moves" {
		t.Fatalf("the repeated line: %+v and %+v", first, second)
	}
	// A block the upstream sits in has its lines, whole.
	if pool := got["upstream app_pool {"]; pool.Line != 1 || pool.Lines != 3 || pool.Text != "upstream app_pool {\n    server 127.0.0.1:3000;\n}" {
		t.Fatalf("the upstream: %+v", pool)
	}
}

// A statement that shares its line with others is written out on its own,
// and a server_name is dropped name by name.
func TestDroppedLinesOfAOneLiner(t *testing.T) {
	content := "server { listen 80; server_name app.example.com _; location / { proxy_pass http://127.0.0.1:3000; proxy_set_header X-Probe \"a b$is_args\"; } }\n"
	read, _ := ParseSiteSpec("app", content)
	dropped, err := DroppedLines("/etc/nginx/sites-available/app", content, read, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []DroppedLine{
		{Line: 1, Lines: 1, Text: "server_name _;", Context: "server", Movable: true},
		{Line: 1, Lines: 1, Text: `proxy_set_header X-Probe "a b$is_args";`, Context: "location /"},
	}
	if !reflect.DeepEqual(dropped, want) {
		t.Fatalf("dropped\n%+v\nwant\n%+v", dropped, want)
	}
}

func TestQuoteNginxReadsBackTheSame(t *testing.T) {
	for _, text := range []string{"plain", `\.php$`, "a b", `say "hi"`, `back\slash and space`, "", "semi;colon", "{brace}", "#hash", "it's"} {
		written := "x " + quoteNginx(text) + ";"
		ds, err := ParseNginxFile("t", written, nil)
		if err != nil || len(ds) != 1 || len(ds[0].Args) != 1 || ds[0].Args[0] != text {
			t.Fatalf("%q written as %s read back as %+v (%v)", text, written, ds, err)
		}
	}
}

// A comment after a statement is not part of its value.
func TestParseSiteSpecReadsPastATrailingComment(t *testing.T) {
	content := "server {\n    listen 443 ssl; # managed by Certbot\n    server_name app.example.com; #main\n" +
		"    ssl_certificate /etc/ssl/app.pem; # managed by Certbot\n    ssl_certificate_key /etc/ssl/app.key;# key\n" +
		"    add_header X-Note \"a # b\" always;\n    location / { proxy_pass http://127.0.0.1:3000; } # app\n}\n"
	spec, _ := ParseSiteSpec("app", content)
	if spec.CertPath != "/etc/ssl/app.pem" || spec.KeyPath != "/etc/ssl/app.key" ||
		!reflect.DeepEqual(spec.Domains, []string{"app.example.com"}) || spec.Upstream != "http://127.0.0.1:3000" {
		t.Fatalf("read as %+v", spec)
	}
	if err := ValidateSpec(spec); err != nil {
		t.Fatalf("the form cannot save what it read: %v", err)
	}
	if got := stripComment(`add_header X-Note "a # b" always; # c`); got != `add_header X-Note "a # b" always; ` {
		t.Fatalf("stripComment cut inside quotes: %q", got)
	}
	if got := stripComment("return 200 a#b;"); got != "return 200 a#b;" {
		t.Fatalf("stripComment cut inside a word: %q", got)
	}
}

// An include is read for the directives it sets, as nginx resolves it:
// relative to the configuration directory, a glob without its dotfiles.
func TestIncludedNamesReadsWhatAnIncludeSets(t *testing.T) {
	root := t.TempDir()
	service := New(root, "")
	if err := os.MkdirAll(filepath.Join(root, "snippets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"snippets/tls.conf":     "ssl_session_timeout 1d;\nssl_session_cache shared:le:10m;\n",
		"snippets/.hidden.conf": "gzip on;\n",
		"snippets/broken.conf":  "gzip on\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := service.includedNames("snippets/tls.conf"); !reflect.DeepEqual(got, []string{"ssl_session_timeout", "ssl_session_cache"}) {
		t.Fatalf("relative include: %v", got)
	}
	if got := service.includedNames(filepath.Join(root, "snippets", "*.conf")); !reflect.DeepEqual(got, []string{"ssl_session_timeout", "ssl_session_cache"}) {
		t.Fatalf("a glob: %v", got)
	}
	if got := service.includedNames("/nonexistent/*.conf"); got != nil {
		t.Fatalf("nothing there: %v", got)
	}
}

func TestContentDigestNamesTheBytes(t *testing.T) {
	if ContentDigest("a") == ContentDigest("a\n") || ContentDigest("") != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatal("the digest is not sha256 of the content")
	}
}

// A save carrying the digest of the file the form read is refused once the
// file has changed, and leaves it as it is; a save of the same version goes
// through.
func TestSaveRefusesAFileThatChangedSinceTheFormReadIt(t *testing.T) {
	service, root := siteNginx(t, cleanTest, 0, "", 0)
	ctx := context.Background()
	spec := plainSpec("app", "app.example.com")
	if _, err := service.SaveSite(ctx, spec, SiteSave{Enable: true}); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(root, "sites-available", "app")
	read, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	digest := ContentDigest(string(read))
	changed := string(read) + "# edited by hand\n"
	if err := os.WriteFile(full, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	spec.Upstream = "http://127.0.0.1:4000"
	_, err = service.SaveSite(ctx, spec, SiteSave{Overwrite: true, BaseDigest: digest})
	if !errors.Is(err, ErrSiteChanged) {
		t.Fatalf("saved over a changed file: %v", err)
	}
	if now, _ := os.ReadFile(full); string(now) != changed {
		t.Fatalf("the refused save touched the file:\n%s", now)
	}
	if _, err := service.SaveSite(ctx, spec, SiteSave{Overwrite: true, BaseDigest: ContentDigest(changed)}); err != nil {
		t.Fatalf("a save of the version read: %v", err)
	}
	if now, _ := os.ReadFile(full); !strings.Contains(string(now), "127.0.0.1:4000") {
		t.Fatalf("not saved:\n%s", now)
	}
	if err := os.Remove(full); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveSite(ctx, spec, SiteSave{Overwrite: true, BaseDigest: digest}); !errors.Is(err, ErrSiteChanged) {
		t.Fatalf("saved over a file that was deleted: %v", err)
	}
}
