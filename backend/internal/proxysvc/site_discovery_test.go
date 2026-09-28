package proxysvc

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The dashboard's own plumbing — a catch-all it writes, a log format — is
// not a site the operator made, and the page that writes it is the one that
// shows it. It is recognised by its jd- name and the marker on its first line
// together, so an operator's own jd- file, and one that only mentions the
// phrase lower down, are still listed.
func TestListingLeavesOutTheDashboardsOwnFiles(t *testing.T) {
	s, root := debianTree(t)
	owned := "# " + OwnedMarker + ": the catch-all default site.\nserver { listen 80 default_server; return 444; }\n"
	writeFile(t, filepath.Join(root, "conf.d", "jd-default.conf"), owned)
	writeFile(t, filepath.Join(root, "sites-available", "jd-catchall"), owned)
	symlink(t, "../sites-available/jd-catchall", filepath.Join(root, "sites-enabled", "jd-catchall"))
	symlink(t, "../sites-available/jd-catchall", filepath.Join(root, "sites-enabled", "00-catchall"))
	writeFile(t, filepath.Join(root, "conf.d", "jd-app.conf"), "server { server_name jd-app.example.com; }\n")
	writeFile(t, filepath.Join(root, "conf.d", "jd-late.conf"), "# a note\n# "+OwnedMarker+"\nserver { server_name late.example.com; }\n")
	writeFile(t, filepath.Join(root, "conf.d", "app.conf"), owned)

	hosts, err := s.ListVHosts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	listed(t, hosts, "conf.d", "jd-app.conf")
	listed(t, hosts, "conf.d", "jd-late.conf")
	listed(t, hosts, "conf.d", "app.conf")
	for _, h := range hosts {
		if h.Name == "jd-default.conf" || h.Name == "jd-catchall" || h.Name == "00-catchall" {
			t.Errorf("the dashboard's own %s/%s is listed as a site", h.Layout, h.Name)
		}
	}
}

// The Access log verb guessed /var/log/nginx/<name>.access.log for every
// site: wrong for a site that logs elsewhere, one with logging off, and one
// that names no log and writes to nginx.conf's. The listing reads each.
func TestListingReadsTheLogsEachSiteWritesTo(t *testing.T) {
	s, root := debianTree(t)
	writeFile(t, filepath.Join(root, "nginx.conf"), `user www-data;
error_log /var/log/nginx/error.log;
events { worker_connections 768; }
http {
	access_log /var/log/nginx/access.log;
	include /etc/nginx/sites-enabled/*;
}
`)
	site := func(name, body string) {
		writeFile(t, filepath.Join(root, "sites-available", name), body)
	}
	site("own", `server {
	server_name own.example.com;
	access_log /var/log/nginx/own.access.log combined buffer=32k;
	error_log /var/log/nginx/own.error.log warn;
}
`)
	site("inherits", "server { server_name inherits.example.com; }\n")
	site("off", "server { server_name off.example.com; access_log off; }\n")
	site("elsewhere", `server {
	server_name elsewhere.example.com;
	access_log syslog:server=10.0.0.1 combined;
	error_log stderr;
}
`)
	site("per-host", "server { server_name per-host.example.com; access_log /var/log/nginx/$host.log; }\n")
	// The site form's own shape for a site that forces HTTPS: the redirect
	// block names no log, the block that serves does.
	site("forced", `server {
	listen 80;
	server_name forced.example.com;
	location / { return 301 https://$host$request_uri; }
}
server {
	listen 443 ssl;
	server_name forced.example.com;
	access_log /var/log/nginx/forced.access.log;
	error_log  /var/log/nginx/forced.error.log;
}
`)
	writeFile(t, filepath.Join(root, "snippets", "logging.conf"), "access_log /var/log/nginx/snippet.access.log;\n")
	site("snippet", "server { server_name snippet.example.com; include snippets/logging.conf; }\n")

	hosts, err := s.ListVHosts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, access, errorLog string }{
		{"own", "/var/log/nginx/own.access.log", "/var/log/nginx/own.error.log"},
		{"inherits", "/var/log/nginx/access.log", "/var/log/nginx/error.log"},
		{"off", "", "/var/log/nginx/error.log"},
		{"elsewhere", "", ""},
		{"per-host", "", "/var/log/nginx/error.log"},
		{"forced", "/var/log/nginx/forced.access.log", "/var/log/nginx/forced.error.log"},
		{"snippet", "/var/log/nginx/snippet.access.log", "/var/log/nginx/error.log"},
	} {
		v := listed(t, hosts, "sites-available", c.name)
		if v.AccessLog != c.access || v.ErrorLog != c.errorLog {
			t.Errorf("%s: logs = %q, %q; want %q, %q", c.name, v.AccessLog, v.ErrorLog, c.access, c.errorLog)
		}
	}
}

// An http-level error_log is the one a site inherits over the main
// context's, and with no nginx.conf there is nothing to inherit.
func TestInheritedLogsFollowNginxsOrder(t *testing.T) {
	s, root := debianTree(t)
	if got := s.inheritedLogs(); got != (logDefaults{}) {
		t.Errorf("no nginx.conf gave %+v", got)
	}
	writeFile(t, filepath.Join(root, "nginx.conf"), `error_log /var/log/nginx/main.log;
http {
	error_log /var/log/nginx/http.log;
	access_log off;
}
`)
	if got := s.inheritedLogs(); got != (logDefaults{access: "", errorLog: "/var/log/nginx/http.log"}) {
		t.Errorf("inherited = %+v", got)
	}
}

// A proxy_pass to an upstream block read as the block's name, and nothing
// on the card said what the site did beyond its names: a password, an
// address list, a rate limit. The listing reads each from the file, and
// from a snippet the site includes.
func TestListingReadsPoolsAndFeatures(t *testing.T) {
	s, root := debianTree(t)
	writeFile(t, filepath.Join(root, "snippets", "sso.conf"), "auth_request /__auth;\n")
	writeFile(t, filepath.Join(root, "snippets", "office.conf"), "allow 10.0.0.0/8;\ndeny all;\ninclude snippets/nested.conf;\n")
	writeFile(t, filepath.Join(root, "snippets", "nested.conf"), "limit_req zone=nested;\n")
	writeFile(t, filepath.Join(root, "sites-available", "everything"), `upstream app_pool {
	least_conn;
	server 10.0.0.2:8080 weight=2 max_fails=3;
	server unix:/run/app.sock backup;
	keepalive 16;
}
server {
	listen 443 ssl;
	listen 443 quic;
	http2 on;
	server_name everything.example.com;
	auth_basic "Staff";
	auth_basic_user_file /etc/nginx/jd-auth/staff;
	limit_req zone=per_ip burst=20;
	if ($jd_everything_maint) { return 503; }
	location / {
		proxy_pass http://app_pool;
		proxy_cache app_cache;
		proxy_set_header Upgrade $http_upgrade;
		include snippets/sso.conf;
	}
}
`)
	writeFile(t, filepath.Join(root, "sites-available", "office"), `server {
	listen 80 http2;
	server_name office.example.com;
	include snippets/office.conf;
	location /public { auth_basic off; proxy_cache off; auth_request off; }
}
`)
	writeFile(t, filepath.Join(root, "sites-available", "admin-path"), `server {
	server_name admin.example.com;
	location /admin { allow 192.0.2.1; deny all; }
}
`)
	// The site form's own output: its fences around dotfiles and backups
	// deny in a location, which restricts nobody.
	spec := &SiteSpec{
		Name: "form", Kind: "proxy", Domains: []string{"form.example.com"}, Upstream: "http://127.0.0.1:3000",
		BlockExploits: true, AccessLog: true, AllowFrom: []string{}, DenyFrom: []string{}, Locations: []SiteLocation{},
	}
	rendered, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "sites-available", "form"), rendered)
	writeFile(t, filepath.Join(root, "sites-available", "unparsable"), "server {\n\tlisten 80;\n\tauth_basic \"x\";\n")

	hosts, err := s.ListVHosts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	everything := listed(t, hosts, "sites-available", "everything")
	if want := []Pool{{Name: "app_pool", Servers: []string{"10.0.0.2:8080", "unix:/run/app.sock"}}}; !reflect.DeepEqual(everything.Pools, want) {
		t.Errorf("pools = %+v", everything.Pools)
	}
	if want := []string{"auth", "sso", "ratelimit", "cache", "ws", "h2", "h3", "maintenance"}; !reflect.DeepEqual(everything.Features, want) {
		t.Errorf("everything features = %v, want %v", everything.Features, want)
	}
	// A snippet at the server's level counts as the server's own; the
	// snippet it includes in turn is not followed.
	if got, want := listed(t, hosts, "sites-available", "office").Features, []string{"allow", "h2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("office features = %v, want %v", got, want)
	}
	if got, want := listed(t, hosts, "sites-available", "admin-path").Features, []string{"allow"}; !reflect.DeepEqual(got, want) {
		t.Errorf("admin-path features = %v, want %v", got, want)
	}
	form := listed(t, hosts, "sites-available", "form")
	if len(form.Features) != 0 || len(form.Pools) != 0 {
		t.Errorf("the form's plain proxy site reads features %v and pools %v", form.Features, form.Pools)
	}
	if form.AccessLog != "/var/log/nginx/form.access.log" || form.ErrorLog != "/var/log/nginx/form.error.log" {
		t.Errorf("form logs = %q, %q", form.AccessLog, form.ErrorLog)
	}
	unparsable := listed(t, hosts, "sites-available", "unparsable")
	if len(unparsable.Features) != 0 || unparsable.Listen[0] != "80" {
		t.Errorf("a file nginx cannot parse: %+v", unparsable)
	}
}

// statusFile writes a dpkg status file and points the listing at it.
func statusFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "status")
	writeFile(t, path, content)
	saved := dpkgStatusFiles
	dpkgStatusFiles = []string{path}
	t.Cleanup(func() { dpkgStatusFiles = saved })
	return path
}

func md5Hex(content string) string {
	sum := md5.Sum([]byte(content))
	return hex.EncodeToString(sum[:])
}

// Ubuntu's own sites-available/default, disabled as most hosts have it, was
// a notice on every overview: "on disk but not serving". While the file is
// still what the package installed — dpkg records its digest — it is the
// distribution's, and the listing says whose.
func TestListingRecognisesTheStockDefaultSite(t *testing.T) {
	s, root := debianTree(t)
	stock := "server {\n\tlisten 80 default_server;\n\troot /var/www/html;\n\tserver_name _;\n}\n"
	defaultSite := filepath.Join(root, "sites-available", "default")
	writeFile(t, defaultSite, stock)
	writeFile(t, filepath.Join(root, "conf.d", "default.conf"), "server { listen 8080; }\n")
	writeFile(t, filepath.Join(root, "sites-available", "app"), stock)
	status := statusFile(t, `Package: adduser
Status: install ok installed
Conffiles:
 /etc/adduser.conf 0123456789abcdef0123456789abcdef

Package: nginx-common
Status: install ok installed
Conffiles:
 `+defaultSite+` `+md5Hex(stock)+`
 `+filepath.Join(root, "conf.d", "default.conf")+` `+md5Hex("server { listen 8080; }\n")+` obsolete
 /etc/nginx/fastcgi.conf newconffile
Description: small, powerful, scalable web/proxy server
 Conffiles: a description line that only looks like the field
`)

	hosts, err := s.ListVHosts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := listed(t, hosts, "sites-available", "default").Package; got != "nginx-common" {
		t.Errorf("the unchanged default reads package %q", got)
	}
	if got := listed(t, hosts, "sites-available", "app").Package; got != "" {
		t.Errorf("a copy of it under another name reads package %q", got)
	}
	if got := listed(t, hosts, "conf.d", "default.conf").Package; got != "" {
		t.Errorf("an obsolete conffile reads package %q", got)
	}

	// Edited, it is the operator's.
	writeFile(t, defaultSite, stock+"# mine\n")
	hosts, _ = s.ListVHosts(context.Background())
	if got := listed(t, hosts, "sites-available", "default").Package; got != "" {
		t.Errorf("the edited default still reads package %q", got)
	}

	// dpkg's record is read again when it changes.
	writeFile(t, defaultSite, stock)
	writeFile(t, status, "Package: nginx-core\nConffiles:\n "+defaultSite+" "+md5Hex(stock)+"\n")
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(status, later, later); err != nil {
		t.Fatal(err)
	}
	hosts, _ = s.ListVHosts(context.Background())
	if got := listed(t, hosts, "sites-available", "default").Package; got != "nginx-core" {
		t.Errorf("after dpkg changed, package = %q", got)
	}
}

func TestReadConffilesKeepsTheFirstPackageForAPath(t *testing.T) {
	into := map[string]conffile{}
	readConffiles(strings.NewReader("Package: a\nConffiles:\n /etc/x "+md5Hex("a")+"\n\nPackage: b\nConffiles:\n /etc/x "+md5Hex("b")+"\n /etc/y "+md5Hex("y")+" remove-on-upgrade\n"), into)
	if into["/etc/x"].pkg != "a" || into["/etc/y"].pkg != "b" {
		t.Errorf("conffiles = %+v", into)
	}
}
