package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// writeTree puts files under root, creating their folders; a value starting
// with "-> " is a symlink to what follows.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if target, ok := strings.CutPrefix(content, "-> "); ok {
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func entriesByPath(t *testing.T, files *ConfigFiles, root string) map[string]ConfigEntry {
	t.Helper()
	out := map[string]ConfigEntry{}
	for _, e := range files.Files {
		rel, err := filepath.Rel(root, e.Path)
		if err != nil {
			t.Fatal(err)
		}
		out[rel] = e
	}
	return out
}

// A Debian-shaped directory: nginx.conf's includes are followed through
// relative paths, globs, a site's own include of a snippet and the
// sites-enabled links, so each file says whether nginx reads it and through
// which include. Password files are listed but never read, backups and
// dotfiles nginx does not read are left out, and a backup nginx does read —
// sites-enabled/*.bak is a second copy of a site — is listed and marked.
func TestConfigFilesFollowTheIncludes(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeTree(t, outside, map[string]string{"mod-stream.conf": "load_module modules/x.so;\n"})
	writeTree(t, root, map[string]string{
		"nginx.conf": "include modules-enabled/*.conf;\nevents {}\nhttp {\n" +
			"    include mime.types;\n    include conf.d/*.conf;\n    include sites-enabled/*;\n}\n",
		"mime.types":                  "types { text/html html; }\n",
		"modules-enabled/50-mod.conf": "-> " + filepath.Join(outside, "mod-stream.conf"),
		"conf.d/app.conf":             "# Managed by Just Dashboard. Edits outside the custom block are replaced.\nserver {\n    listen 80;\n    include snippets/proxy.conf;\n}\n",
		"conf.d/notes.txt":            "not configuration\n",
		"conf.d/app.conf.bak":         "server {}\n",
		"conf.d/.vpsd-123":            "half-written\n",
		"sites-available/default":     "server {\n    listen 80 default_server;\n}\n",
		"sites-available/old":         "server {\n    listen 8080;\n}\n",
		"sites-available/old.bak":     "server {}\n",
		"sites-enabled/default":       "-> ../sites-available/default",
		"sites-enabled/default.bak":   "server {\n    listen 8081;\n}\n",
		"snippets/proxy.conf":         "proxy_set_header Host $host;\n",
		"snippets/unused.conf":        "gzip on;\n",
		"jd-auth/shop":                "operator:$2y$10$abcdefghijklmnopqrstuv\n",
		".htpasswd":                   "admin:$apr1$xyz\n",
		"stream.d/tcp.conf":           "# Managed by Just Dashboard.\nserver { listen 9000; }\n",
		"deep/one/shallow.conf":       "gzip off;\n",
		"deep/one/two/too-deep.conf":  "gzip off;\n",
		"sites-available/app.example": "-> " + outside,
	})

	service := New(root, filepath.Join(root, "Caddyfile"))
	files, err := service.ConfigFiles()
	if err != nil {
		t.Fatal(err)
	}
	if !files.IncludesKnown || files.Problem != nil || files.Truncated {
		t.Fatalf("a clean tree: known %v, problem %+v, truncated %v", files.IncludesKnown, files.Problem, files.Truncated)
	}
	if files.Main != filepath.Join(root, "nginx.conf") || files.Root != root {
		t.Fatalf("root %q main %q", files.Root, files.Main)
	}
	got := entriesByPath(t, files, root)

	var names []string
	for name := range got {
		names = append(names, name)
	}
	sort.Strings(names)
	want := []string{
		".htpasswd", "conf.d/app.conf", "conf.d/notes.txt", "deep/one/shallow.conf", "jd-auth/shop",
		"mime.types", "modules-enabled/50-mod.conf", "nginx.conf", "sites-available/default",
		"sites-available/old", "sites-enabled/default", "sites-enabled/default.bak", "snippets/proxy.conf",
		"snippets/unused.conf", "stream.d/tcp.conf",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("listed %v\nwant %v", names, want)
	}

	read := map[string]bool{}
	for name, e := range got {
		read[name] = e.Included
	}
	wantRead := map[string]bool{
		".htpasswd": false, "conf.d/app.conf": true, "conf.d/notes.txt": false, "deep/one/shallow.conf": false,
		"jd-auth/shop": false, "mime.types": true, "modules-enabled/50-mod.conf": true, "nginx.conf": true,
		"sites-available/default": true, "sites-available/old": false, "sites-enabled/default": true,
		"sites-enabled/default.bak": true, "snippets/proxy.conf": true, "snippets/unused.conf": false,
		"stream.d/tcp.conf": false,
	}
	if !reflect.DeepEqual(read, wantRead) {
		t.Fatalf("read by nginx %v\nwant %v", read, wantRead)
	}

	main := got["nginx.conf"]
	if main.Kind != "main" || main.IncludedBy != nil || main.Managed {
		t.Fatalf("main file: %+v", main)
	}
	if by := got["snippets/proxy.conf"].IncludedBy; by == nil ||
		*by != (IncludeSite{File: filepath.Join(root, "conf.d/app.conf"), Line: 4}) {
		t.Fatalf("the snippet is read through %+v", by)
	}
	link := got["sites-enabled/default"]
	if link.Kind != "link" || link.Target != filepath.Join(root, "sites-available/default") ||
		link.Outside || link.Missing || link.IncludedBy == nil || link.IncludedBy.Line != 6 {
		t.Fatalf("the enabled link: %+v", link)
	}
	if by := got["sites-available/default"].IncludedBy; by == nil ||
		*by != (IncludeSite{File: filepath.Join(root, "nginx.conf"), Line: 6, Via: filepath.Join(root, "sites-enabled/default")}) {
		t.Fatalf("the enabled site is read through %+v", by)
	}
	module := got["modules-enabled/50-mod.conf"]
	if module.Kind != "link" || !module.Outside || module.Target != filepath.Join(outside, "mod-stream.conf") {
		t.Fatalf("a module outside the directory: %+v", module)
	}
	if bak := got["sites-enabled/default.bak"]; !bak.Backup || bak.Kind != "file" {
		t.Fatalf("a backup nginx reads: %+v", bak)
	}
	for _, name := range []string{"conf.d/app.conf", "stream.d/tcp.conf"} {
		if !got[name].Managed {
			t.Errorf("%s carries the marker and is not managed", name)
		}
	}
	for _, name := range []string{"sites-available/default", "snippets/proxy.conf"} {
		if got[name].Managed {
			t.Errorf("%s was written by hand and is managed", name)
		}
	}
	shop, htpasswd := got["jd-auth/shop"], got[".htpasswd"]
	if shop.Kind != "password" || !shop.Protected || !shop.Managed {
		t.Fatalf("the dashboard's password file: %+v", shop)
	}
	if htpasswd.Kind != "password" || !htpasswd.Protected || htpasswd.Managed || htpasswd.Size == 0 {
		t.Fatalf("a hand-written password file: %+v", htpasswd)
	}
	if got["conf.d/app.conf"].Size == 0 || got["conf.d/app.conf"].Modified.IsZero() {
		t.Fatalf("a file without its size or time: %+v", got["conf.d/app.conf"])
	}
}

// A password file is never opened to find the marker or an include, even
// when nginx.conf includes it by mistake: the list is read by every account.
func TestConfigFilesNeverReadAPasswordFile(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"nginx.conf":   "events {}\nhttp {\n    include jd-auth/*;\n}\n",
		"jd-auth/shop": "include /etc/shadow;\n",
	})
	secret := filepath.Join(root, "jd-auth", "shop")
	if err := os.Chmod(secret, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(secret, 0o644) })
	files, err := New(root, filepath.Join(root, "Caddyfile")).ConfigFiles()
	if err != nil {
		t.Fatal(err)
	}
	// An unreadable file that was opened would have made the includes
	// unknown; one that is never opened leaves them known.
	if !files.IncludesKnown || files.Problem != nil {
		t.Fatalf("a password file was read: known %v, problem %+v", files.IncludesKnown, files.Problem)
	}
	shop := entriesByPath(t, files, root)["jd-auth/shop"]
	if !shop.Protected || !shop.Included {
		t.Fatalf("%+v", shop)
	}
}

// What stops nginx stops the walk too, and is said: a file that does not
// parse leaves what it would include unknown, and an include of a file that
// is not there, a link to nothing or a directory is placed at its line.
func TestConfigFilesReportWhatNginxWouldRefuse(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		known bool
		want  ConfigProblem
	}{
		{
			name: "a file that does not parse",
			files: map[string]string{
				"nginx.conf":      "events {}\nhttp {\n    include conf.d/*.conf;\n}\n",
				"conf.d/app.conf": "server {\n    listen 80;\n    include snippets/x.conf;\n",
				"snippets/x.conf": "gzip on;\n",
			},
			want: ConfigProblem{Message: `unexpected end of file, expecting "}"`, File: "conf.d/app.conf"},
		},
		{
			name: "an include of a file that is not there",
			files: map[string]string{
				"nginx.conf": "events {}\nhttp {\n    include missing.conf;\n}\n",
			},
			known: true,
			want:  ConfigProblem{Message: "includes {root}/missing.conf, which is not there", File: "nginx.conf", Line: 3},
		},
		{
			name: "a link to nothing",
			files: map[string]string{
				"nginx.conf":         "events {}\nhttp {\n    include sites-enabled/*;\n}\n",
				"sites-enabled/gone": "-> ../sites-available/gone",
			},
			known: true,
			want:  ConfigProblem{Message: "includes {root}/sites-enabled/gone, a link to nothing", File: "nginx.conf", Line: 3},
		},
		{
			name: "a glob matching a directory",
			files: map[string]string{
				"nginx.conf":          "events {}\nhttp {\n    include conf.d/*;\n}\n",
				"conf.d/sub/app.conf": "server {}\n",
			},
			known: true,
			want:  ConfigProblem{Message: "includes {root}/conf.d/*, which matches the directory {root}/conf.d/sub", File: "nginx.conf", Line: 3},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			writeTree(t, root, c.files)
			files, err := New(root, filepath.Join(root, "Caddyfile")).ConfigFiles()
			if err != nil {
				t.Fatal(err)
			}
			want := c.want
			want.Message = strings.ReplaceAll(want.Message, "{root}", root)
			if want.File != "" {
				want.File = filepath.Join(root, want.File)
			}
			if files.IncludesKnown != c.known || files.Problem == nil {
				t.Fatalf("known %v, problem %+v", files.IncludesKnown, files.Problem)
			}
			got := *files.Problem
			if c.name == "a file that does not parse" {
				// The tokeniser names the end of the file, not a line.
				got.Line = 0
			}
			if got != want {
				t.Fatalf("problem %+v\nwant %+v", got, want)
			}
		})
	}
}

// Without nginx.conf nothing is known to be read, which is said rather than
// shown as a directory of unread files.
func TestConfigFilesWithoutAMainFile(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"conf.d/app.conf": "server {}\n"})
	files, err := New(root, filepath.Join(root, "Caddyfile")).ConfigFiles()
	if err != nil {
		t.Fatal(err)
	}
	if files.IncludesKnown || files.Problem == nil || !strings.Contains(files.Problem.Message, "no nginx.conf") {
		t.Fatalf("known %v, problem %+v", files.IncludesKnown, files.Problem)
	}
	if len(files.Files) != 1 || files.Files[0].Included {
		t.Fatalf("%+v", files.Files)
	}

	if _, err := New(filepath.Join(root, "absent"), "").ConfigFiles(); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a directory that is not there: %v", err)
	}
}

// A file including itself, directly or through another, is followed once.
func TestConfigFilesFollowACycleOnce(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"nginx.conf": "events {}\nhttp {\n    include a.conf;\n}\n",
		"a.conf":     "include b.conf;\n",
		"b.conf":     "include a.conf;\n",
	})
	files, err := New(root, filepath.Join(root, "Caddyfile")).ConfigFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range files.Files {
		if !e.Included {
			t.Errorf("%s is not read", e.Path)
		}
	}
}

// The real binary: every file the walk says nginx reads is a file `nginx -T`
// prints, and the other way round — through a relative include, a glob
// sorted and without dotfiles, a negated set, a site's snippet, an enabled
// link, and a backup in sites-enabled that nginx reads as a site.
func TestLiveConfigFilesAgreeWithNginx(t *testing.T) {
	root := liveNginx(t)
	conf := fmt.Sprintf(`pid %[1]s/nginx.pid;
error_log %[1]s/logs/error.log;
events {}
http {
    access_log off;
    client_body_temp_path %[1]s/tmp-body;
    proxy_temp_path %[1]s/tmp-proxy;
    fastcgi_temp_path %[1]s/tmp-fastcgi;
    uwsgi_temp_path %[1]s/tmp-uwsgi;
    scgi_temp_path %[1]s/tmp-scgi;
    include conf.d/[!_]*.conf;
    include %[1]s/sites-enabled/*;
}
`, root)
	writeTree(t, root, map[string]string{
		"conf.d/app.conf":           "server {\n    listen 127.0.0.1:18191;\n    include snippets/headers.conf;\n}\n",
		"conf.d/_draft.conf":        "server { listen 127.0.0.1:18192; }\n",
		"conf.d/.hidden.conf":       "server { listen 127.0.0.1:18193; }\n",
		"conf.d/app.conf.bak":       "server { listen 127.0.0.1:18194; }\n",
		"snippets/headers.conf":     "add_header X-Test 1;\n",
		"snippets/unused.conf":      "add_header X-Unused 1;\n",
		"sites-available/site":      "server {\n    listen 127.0.0.1:18195;\n}\n",
		"sites-available/disabled":  "server {\n    listen 127.0.0.1:18196;\n}\n",
		"sites-enabled/site":        "-> ../sites-available/site",
		"sites-enabled/site.bak":    "server {\n    listen 127.0.0.1:18197;\n}\n",
		"sites-available/site.save": "server {}\n",
	})
	if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	service := New(root, filepath.Join(root, "Caddyfile"))
	dumped, err := service.EffectiveConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fromNginx := map[string]bool{}
	for _, f := range dumped {
		fromNginx[resolvedFile(f.Path)] = true
	}

	files, err := service.ConfigFiles()
	if err != nil {
		t.Fatal(err)
	}
	if !files.IncludesKnown || files.Problem != nil {
		t.Fatalf("known %v, problem %+v", files.IncludesKnown, files.Problem)
	}
	fromWalk := map[string]bool{}
	for _, e := range files.Files {
		if !e.Included {
			continue
		}
		if e.Kind == "link" {
			fromWalk[e.Target] = true
		} else {
			fromWalk[e.Path] = true
		}
	}
	if !reflect.DeepEqual(fromWalk, fromNginx) {
		t.Fatalf("the walk reads %v\nnginx -T printed %v", fromWalk, fromNginx)
	}
	if !fromWalk[filepath.Join(root, "sites-enabled/site.bak")] || fromWalk[filepath.Join(root, "conf.d/_draft.conf")] {
		t.Fatalf("the backup in sites-enabled or the negated set was read wrong: %v", fromWalk)
	}
}
