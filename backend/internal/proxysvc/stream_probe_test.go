package proxysvc

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseStreamProcStat(t *testing.T) {
	// A name may hold spaces and parentheses; the fields are counted from the
	// last ")".
	line := "3679472 (nginx) S 3679443 3679472 3679472 0 -1 4194560 5541 18185 2 11 6 6 6 13 20 0 1 0 678494804 13000704 782 18446744073709551615 1 1 0 0 0 0 0 1073745920 402745863 0 0 0 17 0 0 0 0 0 0 0 0 0 0 0 0\n"
	st, ok := parseStreamProcStat(line)
	if !ok || st.pid != 3679472 || st.comm != "nginx" || st.ppid != 3679443 || st.start != 678494804 {
		t.Fatalf("got %+v %v", st, ok)
	}
	odd := strings.Replace(line, "(nginx)", "(a (b) c)", 1)
	if st, ok := parseStreamProcStat(odd); !ok || st.comm != "a (b) c" || st.ppid != 3679443 {
		t.Fatalf("odd name: %+v %v", st, ok)
	}
	for _, bad := range []string{"", "12 nginx S 1", "12 (nginx) S"} {
		if _, ok := parseStreamProcStat(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

// A master names its configuration in the title nginx gives it, and one
// started without -c reads the build's own.
func TestMasterConfig(t *testing.T) {
	build := parseStreamNginxBuild(hostNginxV)
	cases := []struct {
		cmdline, want string
		master        bool
	}{
		// Ubuntu's unit, and the NUL padding the rewritten title leaves.
		{"nginx: master process /usr/sbin/nginx -g daemon on; master_process on;\x00\x00\x00", "/etc/nginx/nginx.conf", true},
		// The harness and the live tests start nginx on a file of their own.
		{"nginx: master process nginx -c /srv/slot/nginx/nginx.conf -g daemon off;", "/srv/slot/nginx/nginx.conf", true},
		{"nginx: master process nginx -e /srv/x/startup.log -c/srv/x/nginx.conf", "/srv/x/nginx.conf", true},
		// A relative -c is under -p, or else beside the build's configuration.
		{"nginx: master process nginx -p /opt/edge/ -c conf/edge.conf", "/opt/edge/conf/edge.conf", true},
		{"nginx: master process nginx -c other.conf", "/etc/nginx/other.conf", true},
		{"nginx: worker process", "", false},
		{"nginx: worker process is shutting down", "", false},
		{"/usr/bin/python3 server.py", "", false},
	}
	for _, tc := range cases {
		got, ok := masterConfig(tc.cmdline, build)
		if ok != tc.master || got != tc.want {
			t.Errorf("%q: got %q %v, want %q %v", tc.cmdline, got, ok, tc.want, tc.master)
		}
	}
}

// Lines as this host's kernel printed them for the harness's nginx pod.
func TestParseSocketTable(t *testing.T) {
	tcp := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 440133612 1 0000000000000000 100 0 0 10 0
   1: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 352957153 1 0000000000000000 100 0 0 10 0
   2: 0100007F:1F90 0100007F:D2C4 01 00000000:00000000 00:00000000 00000000  1000        0 352957999 1 0000000000000000 20 4 30 10 -1
`
	got := parseSocketTable(tcp, false)
	want := []socket{
		{addr: "0.0.0.0", port: 80, inode: 440133612, uid: 0},
		{addr: "127.0.0.1", port: 8080, inode: 352957153, uid: 1000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tcp: got %+v", got)
	}
	tcp6 := `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:01BB 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 22334 1 0000000000000000 100 0 0 10 0
   1: 00000000000000000000000001000000:0016 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 22335 1 0000000000000000 100 0 0 10 0
`
	got = parseSocketTable(tcp6, false)
	if len(got) != 2 || got[0].addr != "::" || got[0].port != 443 || got[1].addr != "::1" || got[1].port != 22 {
		t.Fatalf("tcp6: got %+v", got)
	}
	// UDP has no LISTEN: a bound socket with no peer is the listener, and a
	// connected one — a DNS lookup — is not.
	udp := `   sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops
  512: 0100007F:4E20 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 5511 2 0000000000000000 0
  513: 0100007F:B000 08080808:0035 01 00000000:00000000 00:00000000 00000000  1000        0 5512 2 0000000000000000 0
`
	got = parseSocketTable(udp, true)
	if len(got) != 1 || !got[0].udp || got[0].addr != "127.0.0.1" || got[0].port != 20000 {
		t.Fatalf("udp: got %+v", got)
	}
	if got := parseSocketTable("", false); len(got) != 0 {
		t.Fatalf("empty: %+v", got)
	}
}

// Both C libraries' wording, and nginx -t's own prefix.
func TestParseBindFailure(t *testing.T) {
	cases := []struct {
		line, address, text, when string
	}{
		{"2026/09/28 03:29:05 [emerg] 1#1: bind() to 127.0.0.1:47813 failed (98: Address in use)",
			"127.0.0.1:47813", "bind() to 127.0.0.1:47813 failed (98: Address in use)", "2026/09/28 03:29:05"},
		{"2026/09/28 03:29:05 [emerg] 4712#4712: bind() to [::]:5432 failed (98: Address already in use)",
			"[::]:5432", "bind() to [::]:5432 failed (98: Address already in use)", "2026/09/28 03:29:05"},
		{"nginx: [emerg] bind() to 10.9.9.9:80 failed (99: Cannot assign requested address)",
			"10.9.9.9:80", "bind() to 10.9.9.9:80 failed (99: Cannot assign requested address)", ""},
	}
	for _, tc := range cases {
		got, ok := parseBindFailure(tc.line)
		if !ok || got.address != tc.address || got.text != tc.text || got.when != tc.when {
			t.Errorf("%q: got %+v %v", tc.line, got, ok)
		}
	}
	for _, line := range []string{"2026/09/28 03:29:05 [emerg] 1#1: still could not bind()", "bind() to", ""} {
		if _, ok := parseBindFailure(line); ok {
			t.Errorf("%q read as a failed bind", line)
		}
	}
	if got := (bind{"::", 5432, false}).address(); got != "[::]:5432" {
		t.Errorf("v6 address = %q", got)
	}
	if got := emergencyText("2026/09/28 03:29:05 [emerg] 1#1: still could not bind()"); got != "still could not bind()" {
		t.Errorf("emergency text = %q", got)
	}
}

// The error log is read where the configuration puts it, and only under
// /var/log or the nginx directory: the dashboard runs as root and every
// account reads the states.
func TestErrorLogFile(t *testing.T) {
	root := t.TempDir()
	svc := New(root, filepath.Join(root, "Caddyfile"))
	build := parseStreamNginxBuild(hostNginxV)
	tree := func(conf string) []Directive {
		t.Helper()
		directives, err := NginxTree([]ConfigFile{{Path: filepath.Join(root, "nginx.conf"), Content: conf}})
		if err != nil {
			t.Fatal(err)
		}
		return directives
	}
	if err := os.Symlink("/etc", filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		conf, want string
		ok         bool
	}{
		{"error_log /var/log/nginx/error.log;\nhttp { error_log /var/log/nginx/site.log; }\n", "/var/log/nginx/error.log", true},
		{"error_log stderr;\nerror_log " + root + "/logs/error.log warn;\n", root + "/logs/error.log", true},
		{"error_log /etc/shadow;\n", "", false},
		{"error_log " + root + "/escape/shadow;\n", "", false},
		{"error_log syslog:server=unix:/dev/log;\n", "", false},
		// Ubuntu's build writes to stderr when nginx.conf names no file.
		{"events {}\n", "", false},
	}
	for _, tc := range cases {
		got, err := svc.errorLogFile(tree(tc.conf), build)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("%q: got %q %v", tc.conf, got, err)
		}
	}
	// A relative error_log is under the prefix.
	build.prefix = root
	if got, err := svc.errorLogFile(tree("error_log logs/error.log;\n"), build); err != nil || got != root+"/logs/error.log" {
		t.Errorf("relative: got %q %v", got, err)
	}
}

// Only whole lines written after the mark are read, and a rotated log is
// read from its start.
func TestAppendedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "error.log")
	write := func(content string, appendTo bool) {
		t.Helper()
		flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
		if appendTo {
			flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
		}
		f, err := os.OpenFile(path, flags, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(content)
		f.Close()
	}
	write("old line\n", false)
	info, _ := os.Stat(path)
	offset := info.Size()
	write("first\nsecond\nhalf", true)
	lines, next := appendedLines(path, offset)
	if !reflect.DeepEqual(lines, []string{"first", "second"}) {
		t.Fatalf("lines = %q", lines)
	}
	write(" done\n", true)
	if lines, _ = appendedLines(path, next); !reflect.DeepEqual(lines, []string{"half done"}) {
		t.Fatalf("the rest = %q", lines)
	}
	write("rotated\n", false)
	if lines, _ = appendedLines(path, next); !reflect.DeepEqual(lines, []string{"rotated"}) {
		t.Fatalf("after rotation = %q", lines)
	}
	if lines, _ := appendedLines(filepath.Join(t.TempDir(), "none"), 0); lines != nil {
		t.Fatalf("a missing log gave %q", lines)
	}
}

func TestListenBind(t *testing.T) {
	cases := []struct {
		args []string
		http bool
		want bind
		ok   bool
	}{
		{[]string{"5432"}, false, bind{"0.0.0.0", 5432, false}, true},
		{[]string{"[::]:53", "udp"}, false, bind{"::", 53, true}, true},
		{[]string{"127.0.0.1:6000", "udp", "reuseport"}, false, bind{"127.0.0.1", 6000, true}, true},
		{[]string{"443", "ssl"}, true, bind{"0.0.0.0", 443, false}, true},
		{[]string{"443", "quic", "reuseport"}, true, bind{"0.0.0.0", 443, true}, true},
		{[]string{"[::]:443", "ssl", "default_server"}, true, bind{"::", 443, false}, true},
		{[]string{"127.0.0.1"}, true, bind{"127.0.0.1", 80, false}, true},
		{[]string{"*:8080"}, true, bind{"0.0.0.0", 8080, false}, true},
		// udp means nothing to http, nor quic to a stream.
		{[]string{"8443", "udp"}, true, bind{"0.0.0.0", 8443, false}, true},
		{[]string{"127.0.0.1"}, false, bind{}, false},
		{[]string{"unix:/run/app.sock"}, true, bind{}, false},
		{[]string{"localhost:8080"}, true, bind{}, false},
		{[]string{"27015-27030"}, false, bind{}, false},
		{nil, true, bind{}, false},
	}
	for _, tc := range cases {
		got, ok := listenBind(tc.args, tc.http)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%v http=%v: got %+v %v", tc.args, tc.http, got, ok)
		}
	}
}

// Sites are named as the Sites page names them, and an http server with no
// listen takes nginx's *:80.
func TestConfigClaims(t *testing.T) {
	root, streams := nginxLayout(t, map[string]string{
		"nginx.conf":                    "http { include $ROOT/sites-enabled/*; server { return 204; } }\nstream { include $ROOT/stream.d/*.conf; server { listen 9000; proxy_pass 10.0.0.9:9000; } }\n",
		"sites-enabled/app.example.com": "server { listen 443 ssl; listen 443 quic; server_name app.example.com www.app.example.com; }\n",
		"sites-enabled/default":         "server { listen 80 default_server; listen [::]:80 default_server; server_name _; }\n",
		"stream.d/db.conf":              "server { listen 5432; listen [::]:5432; proxy_pass 10.0.0.5:5432; }\n",
	})
	files, err := readConfigFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := NginxTree(files)
	if err != nil {
		t.Fatal(err)
	}
	gotStreams, gotSites := configClaims(tree, root, streams)
	if len(gotStreams) != 2 || gotStreams[0].owner.Name != "db" || gotStreams[1].owner.Name != "" ||
		gotStreams[1].owner.String() != "a stream server in "+root+"/nginx.conf" {
		t.Fatalf("streams = %+v", gotStreams)
	}
	names := []string{}
	for _, site := range gotSites {
		names = append(names, site.owner.String()+"|"+site.owner.Site+"|"+bindsLabel(site.binds))
	}
	want := []string{
		"the site app.example.com|app.example.com|port 443/tcp and port 443/udp",
		"the site default|default|port 80/tcp",
		"an http server in " + root + "/nginx.conf||port 80/tcp",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("sites = %q", names)
	}
}
