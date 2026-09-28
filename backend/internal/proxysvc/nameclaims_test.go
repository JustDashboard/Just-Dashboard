package proxysvc

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The addresses are the ones nginx 1.26.3 printed in its warnings for these
// listen directives, against a private prefix.
func TestListenAddressIsAsNginxNamesIt(t *testing.T) {
	for arg, want := range map[string]string{
		"80":              "0.0.0.0:80",
		"*:18082":         "0.0.0.0:18082",
		"0.0.0.0:18082":   "0.0.0.0:18082",
		"127.0.0.1:18083": "127.0.0.1:18083",
		"127.0.0.1":       "127.0.0.1:80",
		"[::]:18080":      "[::]:18080",
		"[0::0]:443":      "[::]:443",
		"[::1]":           "[::1]:80",
		"080":             "0.0.0.0:80",
	} {
		if got, ok := listenAddress(arg); !ok || got != want {
			t.Errorf("listen %s: %q, %v; want %q", arg, got, ok, want)
		}
	}
	for _, arg := range []string{"localhost:80", "example.test", "[::1", "[::1]x", "127.0.0.1:http", "70000"} {
		if got, ok := listenAddress(arg); ok {
			t.Errorf("listen %s read as %q; a host name or a malformed address cannot be known", arg, got)
		}
	}
}

func parsedTree(t *testing.T, files ...ConfigFile) []Directive {
	t.Helper()
	tree, err := NginxTree(files)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// The blocks that claim a name on the warned address, in the order nginx
// read them: the first holds it and the others are where it was ignored. A
// block on another address, or claiming another name, is not one of them; a
// block with no listen is on *:80 (or *:8000, for an nginx without root).
func TestNameClaimsAreTheBlocksOnThatAddressInReadingOrder(t *testing.T) {
	tree := parsedTree(t,
		ConfigFile{Path: "/etc/nginx/nginx.conf", Content: "events {}\nhttp {\n    include /etc/nginx/conf.d/*.conf;\n}\n"},
		ConfigFile{Path: "/etc/nginx/conf.d/a.conf", Content: "server {\n    listen 80;\n    server_name A.test www.a.test;\n}\n" +
			"server {\n    listen 8080;\n    server_name a.test;\n}\n"},
		ConfigFile{Path: "/etc/nginx/conf.d/b.conf", Content: "server {\n    server_name  other.test;\n    server_name \"a.test\";\n}\n"},
		ConfigFile{Path: "/etc/nginx/conf.d/c.conf", Content: "server {\n    listen 0.0.0.0:80 default_server;\n    server_name a.test;\n}\n"},
	)
	claims, known := nameClaims(tree, "a.test", "0.0.0.0:80")
	want := []NameClaim{
		{File: "/etc/nginx/conf.d/a.conf", Line: 3},
		{File: "/etc/nginx/conf.d/b.conf", Line: 3, Ignored: true},
		{File: "/etc/nginx/conf.d/c.conf", Line: 3, Ignored: true},
	}
	if !known || !reflect.DeepEqual(claims, want) {
		t.Fatalf("got %+v (known %v), want %+v", claims, known, want)
	}
	if claims, _ := nameClaims(tree, "a.test", "0.0.0.0:8080"); len(claims) != 1 {
		t.Fatalf("on 8080 only the second block claims it: %+v", claims)
	}
}

// A block that claims the name and listens on a host name could sit anywhere
// in nginx's order for that address, so no order is given at all.
func TestNameClaimsAreUnknownWhereAClaimantListensOnAHostName(t *testing.T) {
	tree := parsedTree(t,
		ConfigFile{Path: "/etc/nginx/nginx.conf", Content: "http {\n" +
			"server { listen localhost:80; server_name a.test; }\n" +
			"server { listen 127.0.0.1:80; server_name a.test; }\n" +
			"server { listen 127.0.0.1:80; server_name a.test; }\n" +
			"server { listen other.host:80; server_name b.test; }\n}\n"},
	)
	if _, known := nameClaims(tree, "a.test", "127.0.0.1:80"); known {
		t.Fatal("a claimant on a host name left the order known")
	}
	if claims, known := nameClaims(tree, "c.test", "127.0.0.1:80"); !known || claims != nil {
		t.Fatalf("a host name on a block claiming another name spoiled the order: %+v, %v", claims, known)
	}
}

// Placing reads the configuration nginx loads: each warning gets every block
// claiming its name, the same for each warning about the same name, and a
// name whose count of claims is not the warnings' plus one is left as nginx
// said it. Everything else in the result is as it was, and so is the result
// it was given.
func TestPlaceNameConflictsFromTheLoadedConfiguration(t *testing.T) {
	root := t.TempDir()
	fakeNginx(t, "# configuration file "+root+"/nginx.conf:\n"+
		"http {\n    include "+root+"/conf.d/*.conf;\n}\n\n"+
		"# configuration file "+root+"/conf.d/a.conf:\n"+
		"server {\n    listen 80;\n    server_name a.test;\n}\n\n"+
		"# configuration file "+root+"/conf.d/b.conf:\n"+
		"server {\n    listen 80;\n    server_name a.test b.test;\n}\n\n"+
		"# configuration file "+root+"/conf.d/c.conf:\n"+
		"server {\n    listen 80;\n    server_name a.test;\n}\n\n")
	service := New(root, filepath.Join(root, "Caddyfile"))
	warn := func(name string) Diagnostic {
		return Diagnostic{Level: "warn", Message: `conflicting server name "` + name + `" on 0.0.0.0:80, ignored`}
	}
	res := &ValidationResult{Valid: true, Output: "o", Command: "nginx -t", Warnings: 4, Diagnostics: []Diagnostic{
		warn("a.test"), warn("a.test"),
		// b.test is claimed once in these files, so the one warning about
		// it has a claimant the tree cannot see.
		warn("b.test"),
		{Level: "warn", Message: "the \"listen ... http2\" directive is deprecated", File: root + "/conf.d/a.conf", Line: 2},
	}}
	placed := service.PlaceNameConflicts(context.Background(), res)

	want := []NameClaim{
		{File: root + "/conf.d/a.conf", Line: 3},
		{File: root + "/conf.d/b.conf", Line: 3, Ignored: true},
		{File: root + "/conf.d/c.conf", Line: 3, Ignored: true},
	}
	for i := 0; i < 2; i++ {
		if !reflect.DeepEqual(placed.Diagnostics[i].Claims, want) {
			t.Fatalf("warning %d placed at %+v, want %+v", i, placed.Diagnostics[i].Claims, want)
		}
	}
	if placed.Diagnostics[2].Claims != nil || placed.Diagnostics[3].Claims != nil {
		t.Fatalf("placed what could not be: %+v", placed.Diagnostics[2:])
	}
	if placed.Output != "o" || placed.Warnings != 4 || !placed.Valid || placed.Diagnostics[3].Line != 2 {
		t.Fatalf("placing changed the rest of the result: %+v", placed)
	}
	for _, d := range res.Diagnostics {
		if d.Claims != nil {
			t.Fatal("placing changed the result it was given")
		}
	}
}

// With nothing to place, or no configuration to read, the result is the one
// given and nginx is not asked for its configuration.
func TestPlaceNameConflictsLeavesWhatItCannotPlace(t *testing.T) {
	root := t.TempDir()
	runs := fakeNginx(t, "# configuration file /usr/local/nginx/nginx.conf:\nevents {}\n\n")
	service := New(root, filepath.Join(root, "Caddyfile"))
	clean := &ValidationResult{Valid: true, Diagnostics: []Diagnostic{
		{Level: "warn", Message: "something placed", File: root + "/x.conf", Line: 1},
	}}
	if got := service.PlaceNameConflicts(context.Background(), clean); got != clean {
		t.Fatal("a result with nothing to place was replaced")
	}
	if _, err := os.Stat(runs); !os.IsNotExist(err) {
		t.Fatal("nginx -T ran for a result with nothing to place")
	}
	// The dump names a main file outside the proxy directory, which the
	// service refuses to build a tree from.
	unplaced := &ValidationResult{Valid: true, Diagnostics: []Diagnostic{
		{Level: "warn", Message: `conflicting server name "a.test" on 0.0.0.0:80, ignored`},
	}}
	if got := service.PlaceNameConflicts(context.Background(), unplaced); got != unplaced {
		t.Fatal("a configuration that could not be read still placed the warning")
	}
}

// The real binary: two sites claiming one name pass the test with one
// warning and no file, which is kept as the last test and placed at both
// sites' server_name lines — the Debian site at its sites-available file,
// not the sites-enabled link nginx read it through — with the one nginx read
// second as the one it ignored.
func TestLiveConflictingServerNameIsPlacedAtBothSites(t *testing.T) {
	root := liveNginx(t)
	service := New(root, filepath.Join(root, "Caddyfile"))
	held := filepath.Join(root, "conf.d", "held.conf")
	site := "server {\n    listen 127.0.0.1:18097;\n    server_name dup.example.test;\n}\n"
	if err := os.WriteFile(held, []byte(site), 0o644); err != nil {
		t.Fatal(err)
	}
	ignored := filepath.Join(root, "sites-available", "ignored")
	if err := os.WriteFile(ignored, []byte("# second\n"+site), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ignored, filepath.Join(root, "sites-enabled", "ignored")); err != nil {
		t.Fatal(err)
	}

	res, err := service.Test(context.Background(), KindNginx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid || res.Warnings != 1 || len(res.Diagnostics) != 1 || res.Diagnostics[0].File != "" {
		t.Fatalf("two sites claiming one name should pass with one unplaced warning: %+v", res)
	}
	rec, ok := service.LastTest(KindNginx)
	if !ok || rec.Validation.Warnings != 1 {
		t.Fatalf("the test was not kept: %+v", rec)
	}
	placed := service.PlaceNameConflicts(context.Background(), rec.Validation)
	want := []NameClaim{
		{File: held, Line: 3},
		{File: ignored, Line: 4, Ignored: true},
	}
	if !reflect.DeepEqual(placed.Diagnostics[0].Claims, want) {
		t.Fatalf("placed at %+v, want %+v", placed.Diagnostics[0].Claims, want)
	}
}
