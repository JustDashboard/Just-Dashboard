package proxysvc

import (
	"reflect"
	"strings"
	"testing"
)

// A Debian-shaped configuration as nginx -T would list it: conf.d printed out
// of order, a dotfile nginx's glob would not read, a relative include, and a
// stream block beside http.
var includeFixture = []ConfigFile{
	{Path: "/etc/nginx/nginx.conf", Content: `user www-data;
events { worker_connections 768; }
http {
    include mime.types;
    include /etc/nginx/conf.d/*.conf;
    include /etc/nginx/sites-enabled/*;
}
stream {
    include /etc/nginx/stream.d/*.conf;
}
`},
	{Path: "/etc/nginx/mime.types", Content: "types {\n    text/html html;\n}\n"},
	{Path: "/etc/nginx/conf.d/b.conf", Content: "gzip on;\n"},
	{Path: "/etc/nginx/conf.d/a.conf", Content: "map $http_upgrade $jd_app_upgrade { default upgrade; '' close; }\n"},
	{Path: "/etc/nginx/conf.d/.draft.conf", Content: "gzip off;\n"},
	{Path: "/etc/nginx/sites-enabled/app", Content: `# Managed by Just Dashboard.
server {
    listen 443 ssl; # the comment ; { } is not configuration
    server_name app.example.com;
    return 301 https://${host}$request_uri;
    location ~ \.php$ {
        add_header Content-Security-Policy "default-src 'self'; img-src *" always;
        add_header X-Quoted 'it\'s "fine"';
        proxy_pass http://127.0.0.1:3000;
    }
}
`},
	{Path: "/etc/nginx/stream.d/db.conf", Content: "server {\n    listen 5432;\n    proxy_pass 10.0.0.5:5432;\n}\n"},
}

func TestNginxconfIncludes(t *testing.T) {
	tree, err := NginxTree(includeFixture)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, d := range findDirective(tree, "http").Block {
		names = append(names, d.Name)
	}
	// mime.types in place of its include, then conf.d sorted by name with the
	// dotfile left out, then the site.
	if want := []string{"types", "map", "gzip", "server"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("http holds %v, want %v", names, want)
	}
	gzip := findDirective(tree, "gzip")
	if gzip.File != "/etc/nginx/conf.d/b.conf" || gzip.Args[0] != "on" {
		t.Fatalf("the dotfile was read, or conf.d is out of order: %+v", gzip)
	}

	for _, tc := range []struct {
		name, file string
		line       int
		context    []string
	}{
		{"text/html", "/etc/nginx/mime.types", 2, []string{"http", "types"}},
		{"server_name", "/etc/nginx/sites-enabled/app", 4, []string{"http", "server"}},
		{"proxy_pass", "/etc/nginx/sites-enabled/app", 9, []string{"http", "server", "location"}},
		{"listen", "/etc/nginx/sites-enabled/app", 3, []string{"http", "server"}},
		{"worker_connections", "/etc/nginx/nginx.conf", 2, []string{"events"}},
	} {
		d := findDirective(tree, tc.name)
		if d == nil || d.File != tc.file || d.Line != tc.line || !reflect.DeepEqual(d.Context, tc.context) {
			t.Errorf("%s: got %+v, want %s:%d in %v", tc.name, d, tc.file, tc.line, tc.context)
		}
	}

	stream := findDirective(tree, "stream")
	upstream := findDirective(stream.Block, "proxy_pass")
	if upstream == nil || upstream.Args[0] != "10.0.0.5:5432" ||
		!reflect.DeepEqual(upstream.Context, []string{"stream", "server"}) {
		t.Fatalf("stream's server was not read in stream context: %+v", upstream)
	}
}

func TestNginxconfTokens(t *testing.T) {
	tree, err := NginxTree(includeFixture)
	if err != nil {
		t.Fatal(err)
	}
	location := findDirective(tree, "location")
	if !reflect.DeepEqual(location.Args, []string{"~", `\.php$`}) {
		t.Fatalf("a regex location lost its backslash: %q", location.Args)
	}
	headers := location.Block
	if headers[0].Name != "add_header" || !reflect.DeepEqual(headers[0].Args,
		[]string{"Content-Security-Policy", "default-src 'self'; img-src *", "always"}) {
		t.Fatalf("a quoted semicolon split the directive: %q", headers[0].Args)
	}
	if headers[1].Args[1] != `it's "fine"` {
		t.Fatalf("escapes inside quotes: %q", headers[1].Args[1])
	}
	ret := findDirective(tree, "return")
	if !reflect.DeepEqual(ret.Args, []string{"301", "https://${host}$request_uri"}) {
		t.Fatalf("${host} was read as a block: %q", ret.Args)
	}
	mapBlock := findDirective(tree, "map")
	if len(mapBlock.Block) != 2 || mapBlock.Block[1].Name != "" || mapBlock.Block[1].Args[0] != "close" {
		t.Fatalf("a quoted empty key in a map: %+v", mapBlock.Block)
	}
	for _, d := range findDirective(tree, "server").Block {
		if strings.Contains(d.Name, "comment") {
			t.Fatalf("a comment was read as configuration: %+v", d)
		}
	}
}

func TestNginxconfErrorsSayWhere(t *testing.T) {
	for content, want := range map[string]string{
		"server {\n    listen 80;\n":            "expecting \"}\" in site",
		"server {\n    listen 80;\n}\n}\n":      "unexpected \"}\" in site:4",
		"server {\n    listen 80\n}\n":          "unexpected \"}\" in site:3",
		"add_header X \"unterminated;\n}\n":     "expecting '\"' in site:1",
		"server {\n    listen 80;\n    gzip on": "expecting \";\" or \"}\" in site:3",
	} {
		if _, err := ParseNginxFile("site", content, nil); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error containing %q", content, err, want)
		}
	}
}

func TestNginxTreeReadsASelfIncludeOnce(t *testing.T) {
	tree, err := NginxTree([]ConfigFile{
		{Path: "/etc/nginx/nginx.conf", Content: "http { include /etc/nginx/nginx.conf; gzip on; }\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if http := findDirective(tree, "http"); len(http.Block) != 1 || http.Block[0].Name != "gzip" {
		t.Fatalf("got %+v", tree)
	}
}
