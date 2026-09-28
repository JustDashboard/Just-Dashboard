package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func init() {
	// Unit tests see only the listeners they declare. The host's own sockets
	// — this machine runs Postgres on 5432 — would make them pass or fail by
	// whatever else happens to be running.
	streamListeners = func(context.Context) ([]Listener, error) { return nil, nil }
	// Nor do they find a running nginx: the host's own, or another test's,
	// would answer for their configuration. A test that wants one says so.
	readNginx = func(context.Context, *Service, []ConfigFile, int32) *socketView {
		return listenerView("no nginx runs for unit tests")
	}
}

// withNginx makes the running nginx look like views returns, asked afresh
// each time (master 0) or of a master found before.
func withNginx(t *testing.T, views func(master int32) *socketView) {
	t.Helper()
	previous := readNginx
	readNginx = func(_ context.Context, _ *Service, _ []ConfigFile, master int32) *socketView { return views(master) }
	t.Cleanup(func() { readNginx = previous })
}

// withListeners makes the host look like it holds exactly these sockets.
func withListeners(t *testing.T, listeners func(context.Context) ([]Listener, error)) {
	t.Helper()
	previous := streamListeners
	streamListeners = listeners
	t.Cleanup(func() { streamListeners = previous })
}

func tcpStream() *StreamSpec {
	return &StreamSpec{
		Name: "postgres-replica", Listen: 5432, Protocol: "tcp",
		Upstream: "10.0.0.5:5432", AllowFrom: []string{"10.0.0.0/8"},
	}
}

// streamHost is a stream directory behind an nginx shim whose -t passes
// unless $root/fail-test exists and whose reload fails while $root/fail-reload
// does. Every run is logged to $root/runs.
func streamHost(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"stream.d", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	shim := fmt.Sprintf(`#!/bin/sh
echo "$*" >> '%[1]s/runs'
case "$1" in
-t) if [ -e '%[1]s/fail-test' ]; then echo "nginx: [emerg] test refused" >&2; exit 1; fi ;;
-s) if [ -e '%[1]s/fail-reload' ]; then echo "nginx: [alert] kill(1234, 1) failed (3: No such process)" >&2; exit 1; fi ;;
esac
exit 0
`, root)
	if err := os.WriteFile(filepath.Join(root, "bin", "nginx"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	return New(root, filepath.Join(root, "Caddyfile")), root
}

func writeStream(t *testing.T, s *Service, name, content string) string {
	t.Helper()
	path := filepath.Join(s.streamDir(), name+".conf")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func nginxRuns(t *testing.T, root string) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(root, "runs"))
	return string(b)
}

func TestValidateStream(t *testing.T) {
	if err := ValidateStream(tcpStream()); err != nil {
		t.Fatalf("rejected a valid stream: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*StreamSpec)
	}{
		{"bad name", func(s *StreamSpec) { s.Name = "../x" }},
		{"port out of range", func(s *StreamSpec) { s.Listen = 0 }},
		{"unknown protocol", func(s *StreamSpec) { s.Protocol = "sctp" }},
		{"upstream with no port", func(s *StreamSpec) { s.Upstream = "10.0.0.5" }},
		{"upstream with an injected directive", func(s *StreamSpec) { s.Upstream = "10.0.0.5:5432; root /" }},
		{"upstream with a variable", func(s *StreamSpec) { s.Upstream = "$host:5432" }},
		{"relative unix socket", func(s *StreamSpec) { s.Upstream = "unix:run/app.sock" }},
		{"acl entry that is not an address", func(s *StreamSpec) { s.AllowFrom = []string{"office"} }},
		{"idle timeout out of range", func(s *StreamSpec) { s.Timeout = 999999 }},
		{"connect timeout out of range", func(s *StreamSpec) { s.ConnectTimeout = -1 }},
		{"address that is not an IP", func(s *StreamSpec) { s.Address = "localhost" }},
		{"unknown UDP mode", func(s *StreamSpec) { s.Protocol, s.UDPMode = "udp", "burst" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := tcpStream()
			tc.mutate(spec)
			if err := ValidateStream(spec); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestValidateStreamDefaultsToTCP(t *testing.T) {
	spec := tcpStream()
	spec.Protocol = ""
	if err := ValidateStream(spec); err != nil {
		t.Fatal(err)
	}
	if spec.Protocol != "tcp" {
		t.Fatalf("protocol = %q", spec.Protocol)
	}
}

// unix:/run/x.sock is a valid stream upstream (nginx 1.26 accepts it), and
// was refused as "the upstream port is not valid".
func TestValidateStreamAcceptsAUnixSocket(t *testing.T) {
	spec := tcpStream()
	spec.Upstream = "unix:/run/postgresql/.s.PGSQL.5432"
	out, err := RenderStream(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "server unix:/run/postgresql/.s.PGSQL.5432;") {
		t.Fatalf("socket upstream missing:\n%s", out)
	}
}

func TestRenderStreamTCP(t *testing.T) {
	out, err := RenderStream(tcpStream())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		managedMarker, "listen 5432;", "listen [::]:5432;", "server 10.0.0.5:5432;",
		"proxy_pass " + streamUpstreamName("postgres-replica") + ";", "allow 10.0.0.0/8;", "deny all;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q from:\n%s", want, out)
		}
	}
	// An allow list with no deny after it allows everybody, which is the same
	// trap the site renderer guards against.
	if strings.Index(out, "allow 10.0.0.0/8;") > strings.Index(out, "deny all;") {
		t.Error("allow must come before deny all")
	}
}

// a-b and a_b are both legal names, and folding "-" to "_" gave both files
// `upstream a_b_backend` — nginx then refused the second with "duplicate
// upstream". A dot survived into the identifier as well.
func TestStreamUpstreamNamesDoNotCollide(t *testing.T) {
	seen := map[string]string{}
	for _, name := range []string{"a-b", "a_b", "a.b", "ab"} {
		spec := tcpStream()
		spec.Name = name
		out, err := RenderStream(spec)
		if err != nil {
			t.Fatal(err)
		}
		id := streamUpstreamName(name)
		if !strings.Contains(out, "upstream "+id+" {") || !strings.Contains(out, "proxy_pass "+id+";") {
			t.Fatalf("%s: upstream %s not used:\n%s", name, id, out)
		}
		if strings.ContainsAny(id, "-.") {
			t.Errorf("%s: %q is not a plain identifier", name, id)
		}
		if other, taken := seen[id]; taken {
			t.Errorf("%s and %s share upstream %s", other, name, id)
		}
		seen[id] = name
	}
}

// proxy_responses 1 ends a UDP session at the first reply, so every datagram
// reached the backend from a new source port — a game server or WireGuard
// sees a new peer each time. It is now the request/reply mode only.
func TestRenderStreamUDPModes(t *testing.T) {
	spec := tcpStream()
	spec.Protocol, spec.Listen = "udp", 5353
	out, err := RenderStream(spec)
	if err != nil {
		t.Fatal(err)
	}
	if spec.UDPMode != "session" {
		t.Fatalf("a UDP stream defaults to %q, want session", spec.UDPMode)
	}
	if !strings.Contains(out, "listen 5353 udp;") || !strings.Contains(out, "listen [::]:5353 udp;") {
		t.Fatalf("udp listener missing:\n%s", out)
	}
	if strings.Contains(out, "proxy_responses") {
		t.Fatalf("a session stream must not end at the first reply:\n%s", out)
	}

	spec.UDPMode = "request"
	out, err = RenderStream(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "proxy_responses 1;") {
		t.Fatalf("request mode needs proxy_responses 1:\n%s", out)
	}

	tcp := tcpStream()
	tcp.UDPMode = "request"
	if out, _ := RenderStream(tcp); strings.Contains(out, "proxy_responses") || tcp.UDPMode != "" {
		t.Fatalf("a TCP stream carried a UDP mode:\n%s", out)
	}
}

// DNS and many game servers take one port over TCP and UDP both. One stream
// carries the two, to one upstream, and the UDP mode leaves TCP alone.
func TestRenderStreamBothProtocols(t *testing.T) {
	spec := tcpStream()
	spec.Name, spec.Protocol, spec.Listen, spec.Upstream = "dns", "BOTH", 53, "10.0.0.53:53"
	out, err := RenderStream(spec)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Protocol != "both" || spec.UDPMode != "session" {
		t.Fatalf("protocol %q mode %q, want both and session", spec.Protocol, spec.UDPMode)
	}
	for _, want := range []string{"listen 53;", "listen [::]:53;", "listen 53 udp;", "listen [::]:53 udp;"} {
		if !strings.Contains(out, "    "+want+"\n") {
			t.Errorf("missing %q from:\n%s", want, out)
		}
	}
	if strings.Contains(out, "proxy_responses") {
		t.Fatalf("a session stream must not end at the first reply:\n%s", out)
	}

	spec.UDPMode, spec.Address = "request", "127.0.0.1"
	out, err = RenderStream(spec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "listen ") != 2 || !strings.Contains(out, "listen 127.0.0.1:53;") ||
		!strings.Contains(out, "listen 127.0.0.1:53 udp;") || !strings.Contains(out, "proxy_responses 1;") {
		t.Fatalf("one address over both protocols, one reply per UDP session:\n%s", out)
	}
}

// Timeouts are written as a person reads them — 10m, not 600s — and read back
// to the same number of seconds.
func TestNginxDuration(t *testing.T) {
	for seconds, want := range map[int]string{
		5: "5s", 60: "1m", 90: "1m30s", 600: "10m", 3600: "1h", 5400: "1h30m", 3661: "1h1m1s", 86400: "24h",
	} {
		got := nginxDuration(seconds)
		if got != want {
			t.Errorf("%d = %q, want %q", seconds, got, want)
		}
		if ms, ok := parseNginxDuration(got); !ok || ms != int64(seconds)*1000 {
			t.Errorf("%q reads back as %d ms", got, ms)
		}
	}
}

// One Timeout used to set proxy_timeout and proxy_connect_timeout alike, so
// an hour of idle for SSH was also an hour's wait on a dead upstream.
func TestRenderStreamKeepsTheTwoTimeoutsApart(t *testing.T) {
	spec := tcpStream()
	spec.Timeout = 3600
	out, _ := RenderStream(spec)
	if !strings.Contains(out, "proxy_timeout 1h;") || strings.Contains(out, "proxy_connect_timeout") {
		t.Fatalf("idle timeout leaked into the connect timeout:\n%s", out)
	}
	spec.ConnectTimeout = 5
	out, _ = RenderStream(spec)
	if !strings.Contains(out, "proxy_connect_timeout 5s;") {
		t.Fatalf("connect timeout missing:\n%s", out)
	}
}

func TestStreamRoundTrip(t *testing.T) {
	cases := map[string]func(*StreamSpec){
		"tcp":            func(s *StreamSpec) { s.ProxyProtocol, s.Timeout, s.ConnectTimeout = true, 300, 10 },
		"udp session":    func(s *StreamSpec) { s.Protocol, s.UDPMode = "udp", "session" },
		"udp request":    func(s *StreamSpec) { s.Protocol, s.UDPMode, s.Listen = "udp", "request", 53 },
		"both session":   func(s *StreamSpec) { s.Protocol, s.UDPMode, s.ProxyProtocol = "both", "session", true },
		"both request":   func(s *StreamSpec) { s.Protocol, s.UDPMode, s.Address = "both", "request", "::1" },
		"odd durations":  func(s *StreamSpec) { s.Timeout, s.ConnectTimeout = 86400, 3661 },
		"loopback only":  func(s *StreamSpec) { s.Address = "127.0.0.1" },
		"ipv6 loopback":  func(s *StreamSpec) { s.Address = "::1" },
		"ipv4 only":      func(s *StreamSpec) { s.Address = "0.0.0.0" },
		"unix upstream":  func(s *StreamSpec) { s.Upstream = "unix:/run/app.sock" },
		"open to anyone": func(s *StreamSpec) { s.AllowFrom = []string{} },
		"folded name":    func(s *StreamSpec) { s.Name = "a_b.c-d" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			original := tcpStream()
			mutate(original)
			out, err := RenderStream(original)
			if err != nil {
				t.Fatal(err)
			}
			parsed, managed, unsupported := ParseStreamSpec(original.Name, out)
			if !managed || len(unsupported) != 0 {
				t.Fatalf("managed=%v unsupported=%v", managed, unsupported)
			}
			if !reflect.DeepEqual(parsed, original) {
				t.Fatalf("parsed\n%+v\nwant\n%+v", parsed, original)
			}
			again, err := RenderStream(parsed)
			if err != nil || again != out {
				t.Fatalf("second render differs (%v):\n%s\nwant\n%s", err, again, out)
			}
		})
	}
}

// A file written by an earlier version of the dashboard — the old upstream
// name, one timeout in both directives, proxy_responses on every UDP stream —
// still reads back as the same forward.
func TestParseStreamSpecReadsTheOldRendering(t *testing.T) {
	old := `# Managed by Just Dashboard.
# Stream: dns
upstream dns_backend {
    server 10.0.0.53:53;
}

server {
    listen 53 udp;
    listen [::]:53 udp;
    proxy_pass dns_backend;
    proxy_timeout 30s;
    proxy_connect_timeout 30s;
    # UDP has no connection to close, so nginx decides a session is
    # over by silence rather than by a shutdown.
    proxy_responses 1;
}
`
	spec, managed, unsupported := ParseStreamSpec("dns", old)
	if !managed || len(unsupported) != 0 {
		t.Fatalf("managed=%v unsupported=%v", managed, unsupported)
	}
	want := &StreamSpec{Name: "dns", Listen: 53, Protocol: "udp", UDPMode: "request", Upstream: "10.0.0.53:53",
		Timeout: 30, ConnectTimeout: 30, AllowFrom: []string{}}
	if !reflect.DeepEqual(spec, want) {
		t.Fatalf("parsed %+v", spec)
	}
}

// The v4 and v6 listen lines carry the same port; reading both would leave the
// form showing whichever came last rather than one value.
func TestParseStreamSpecReadsOnePort(t *testing.T) {
	spec, _, unsupported := ParseStreamSpec("x", "server {\n listen 5432;\n listen [::]:5432;\n proxy_pass 10.0.0.5:5432;\n}\n")
	if spec.Listen != 5432 || spec.Address != "" || len(unsupported) != 0 {
		t.Fatalf("listen = %d address = %q unsupported = %v", spec.Listen, spec.Address, unsupported)
	}
}

// The files people write by hand, as the page map found them.
func TestParseStreamSpecHandWritten(t *testing.T) {
	cases := []struct {
		name        string
		content     string
		want        StreamSpec
		open        bool
		unsupported []string
	}{
		{
			// A loopback bind was read as the bare port, and saving then
			// listened on every interface.
			name:    "loopback bind",
			content: "server {\n    listen 127.0.0.1:6000;\n    proxy_pass 10.0.0.5:6000;\n}\n",
			want:    StreamSpec{Name: "x", Listen: 6000, Address: "127.0.0.1", Protocol: "tcp", Upstream: "10.0.0.5:6000", AllowFrom: []string{}},
			open:    true,
		},
		{
			// One line, a direct proxy_pass and minutes: the old reader
			// found Listen 0, no upstream and a timeout of 0.
			name:    "one line",
			content: "server { listen 6000; proxy_pass 10.0.0.5:6000; proxy_timeout 10m; }",
			want:    StreamSpec{Name: "x", Listen: 6000, Address: "0.0.0.0", Protocol: "tcp", Upstream: "10.0.0.5:6000", Timeout: 600, AllowFrom: []string{}},
			open:    true,
		},
		{
			name:    "hours and minutes",
			content: "server { listen 22 ; proxy_pass bastion:22; proxy_timeout 1h30m; proxy_connect_timeout 5s; allow 10.0.0.0/8; deny all; }",
			want:    StreamSpec{Name: "x", Listen: 22, Address: "0.0.0.0", Protocol: "tcp", Upstream: "bastion:22", Timeout: 5400, ConnectTimeout: 5, AllowFrom: []string{"10.0.0.0/8"}},
		},
		{
			// `allow all` restricts nothing, and the listing counted it as a
			// restriction.
			name:    "allow all",
			content: "server { listen 6000; listen [::]:6000; allow all; deny all; proxy_pass 10.0.0.5:6000; }",
			want:    StreamSpec{Name: "x", Listen: 6000, Protocol: "tcp", Upstream: "10.0.0.5:6000", AllowFrom: []string{}},
			open:    true,
		},
		{
			// Deny lines were dropped, and saving then let the denied in.
			name:    "deny then allow all",
			content: "server { listen 6000; deny 203.0.113.0/24; allow all; proxy_pass 10.0.0.5:6000; }",
			want: StreamSpec{Name: "x", Listen: 6000, Address: "0.0.0.0", Protocol: "tcp", Upstream: "10.0.0.5:6000", AllowFrom: []string{},
				Rules: []StreamRule{{"deny", "203.0.113.0/24"}}, DefaultAllow: new(true)},
			open: true,
		},
		{
			name:    "allow with no deny all",
			content: "server { listen 6000; allow 10.0.0.0/8; proxy_pass 10.0.0.5:6000; }",
			want: StreamSpec{Name: "x", Listen: 6000, Address: "0.0.0.0", Protocol: "tcp", Upstream: "10.0.0.5:6000", AllowFrom: []string{},
				Rules: []StreamRule{{"allow", "10.0.0.0/8"}}, DefaultAllow: new(true)},
			open: true,
		},
		{
			// `server {` was read as the upstream, and the card said
			// "Forward to {". A pool with a backup lost its second server.
			name: "pool with a backup",
			content: `upstream pool { server 10.0.0.5:5432; server 10.0.0.6:5432 backup; }
server { listen 5432; proxy_pass pool; allow 10.0.0.0/8; deny all; }`,
			want: StreamSpec{Name: "x", Listen: 5432, Address: "0.0.0.0", Protocol: "tcp", Upstream: "10.0.0.5:5432", AllowFrom: []string{"10.0.0.0/8"},
				Servers: []StreamServer{{Address: "10.0.0.5:5432"}, {Address: "10.0.0.6:5432", Backup: true}}},
		},
		{
			name:    "tls without a key and a sub-second timeout",
			content: "server { listen 6443 ssl; ssl_certificate /etc/ssl/a.pem; proxy_pass 10.0.0.5:6443; proxy_connect_timeout 500ms; }",
			want: StreamSpec{Name: "x", Listen: 6443, Address: "0.0.0.0", Protocol: "tcp", Upstream: "10.0.0.5:6443", AllowFrom: []string{},
				TLS: true, CertPath: "/etc/ssl/a.pem"},
			open:        true,
			unsupported: []string{"proxy_connect_timeout 500ms", "an ssl listen and its certificate apart"},
		},
		{
			// nginx takes a zero and drops every connection at once. Read
			// as the form's zero it opened as an empty field, and a save
			// wrote nginx's default in its place without a word.
			name:        "zero timeouts",
			content:     "server { listen 6000; proxy_pass 10.0.0.5:6000; proxy_timeout 0; proxy_connect_timeout 0s; }",
			want:        StreamSpec{Name: "x", Listen: 6000, Address: "0.0.0.0", Protocol: "tcp", Upstream: "10.0.0.5:6000", AllowFrom: []string{}},
			open:        true,
			unsupported: []string{"proxy_timeout 0", "proxy_connect_timeout 0s"},
		},
		{
			// DNS written by hand, TCP and UDP on one port: the form's both.
			name:    "tcp and udp",
			content: "server { listen 53; listen 53 udp; proxy_pass 10.0.0.53:53; proxy_responses 1; allow 10.0.0.0/8; deny all; }",
			want: StreamSpec{Name: "x", Listen: 53, Address: "0.0.0.0", Protocol: "both", UDPMode: "request",
				Upstream: "10.0.0.53:53", AllowFrom: []string{"10.0.0.0/8"}},
		},
		{
			// TCP on the loopback and UDP everywhere is not a thing one
			// address field can say; saving would widen the TCP side.
			name:        "tcp and udp on different addresses",
			content:     "server { listen 127.0.0.1:53; listen 53 udp; proxy_pass 10.0.0.53:53; }",
			want:        StreamSpec{Name: "x", Listen: 53, Address: "127.0.0.1", Protocol: "both", UDPMode: "session", Upstream: "10.0.0.53:53", AllowFrom: []string{}},
			open:        true,
			unsupported: []string{"TCP and UDP on different addresses"},
		},
		{
			// A block of game ports, each to the same port on the backend.
			name:    "port range",
			content: "server { listen 27015-27030 udp; proxy_pass 10.0.0.9:$server_port; }",
			want: StreamSpec{Name: "x", Listen: 27015, ListenEnd: 27030, Address: "0.0.0.0", Protocol: "udp", UDPMode: "session",
				Upstream: "10.0.0.9", SamePort: true, AllowFrom: []string{}},
			open: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := parseStreamFile("x.conf", tc.content)
			if !reflect.DeepEqual(p.spec, tc.want) {
				t.Errorf("spec\n%+v\nwant\n%+v", p.spec, tc.want)
			}
			if p.open != tc.open {
				t.Errorf("open = %v, want %v", p.open, tc.open)
			}
			want := tc.unsupported
			if want == nil {
				want = []string{}
			}
			if !reflect.DeepEqual(p.unsupported, want) {
				t.Errorf("unsupported = %q, want %q", p.unsupported, want)
			}
			if p.managed {
				t.Error("a hand-written file read as the dashboard's")
			}
		})
	}
}

func TestParseNginxDuration(t *testing.T) {
	for value, want := range map[string]int64{
		"90": 90_000, "90s": 90_000, "10m": 600_000, "1h30m": 5_400_000, "500ms": 500, "1d": 86_400_000, "2w": 1_209_600_000,
	} {
		if got, ok := parseNginxDuration(value); !ok || got != want {
			t.Errorf("%s = %d (%v), want %d", value, got, ok, want)
		}
	}
	for _, bad := range []string{"", "m", "10x", "1.5s", "-3s"} {
		if _, ok := parseNginxDuration(bad); ok {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestStreamOpen(t *testing.T) {
	for allow, want := range map[string]bool{
		"":                     true,
		"10.0.0.0/8":           false,
		"10.0.0.0/8,all":       true,
		"0.0.0.0/0":            true,
		"::/0":                 true,
		"10.0.0.0/8,192.0.2.1": false,
	} {
		spec := tcpStream()
		spec.AllowFrom = nil
		if allow != "" {
			spec.AllowFrom = strings.Split(allow, ",")
		}
		if got := streamOpen(spec); got != want {
			t.Errorf("allow %q: open = %v, want %v", allow, got, want)
		}
	}
}

func TestStreamWarnings(t *testing.T) {
	open := tcpStream()
	open.AllowFrom = nil
	warnings := StreamWarnings(open)
	if len(warnings) < 2 {
		t.Fatalf("an unrestricted database stream should warn twice: %v", warnings)
	}
	if !containsSubstring(warnings, "no authentication") {
		t.Error("the absence of any auth on a raw stream is the headline")
	}
	if !containsSubstring(warnings, "database") {
		t.Error("the port catalogue's judgement should carry over to streams")
	}

	if got := StreamWarnings(tcpStream()); len(got) != 0 {
		t.Errorf("a source-restricted stream warned anyway: %v", got)
	}

	// `allow all` restricts nothing, so it warns like an empty list.
	everyone := tcpStream()
	everyone.AllowFrom = []string{"all"}
	if !containsSubstring(StreamWarnings(everyone), "database") {
		t.Error("allow all counted as a restriction")
	}

	// nginx does send the header on UDP — in front of the first datagram of
	// the session. The old warning said the backend would not see it.
	udp := tcpStream()
	udp.Protocol, udp.ProxyProtocol = "udp", true
	got := StreamWarnings(udp)
	if !containsSubstring(got, "first datagram") || containsSubstring(got, "will not see the header") {
		t.Errorf("PROXY on UDP warning is wrong: %v", got)
	}
	udp.Protocol = "both"
	if !containsSubstring(StreamWarnings(udp), "first datagram") {
		t.Error("a stream of both protocols carries the header on UDP too")
	}
	tcp := tcpStream()
	tcp.ProxyProtocol = true
	if containsSubstring(StreamWarnings(tcp), "first datagram") {
		t.Error("a TCP stream warned about datagrams")
	}
}

// The stream directory hangs off the configured nginx directory, so a host
// with JD_NGINX_DIR set somewhere else does not write into /etc/nginx.
func TestStreamsLiveUnderTheConfiguredNginxDir(t *testing.T) {
	svc, root := streamHost(t)
	status, err := svc.Streams(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Dir != filepath.Join(root, "stream.d") {
		t.Fatalf("stream dir is %s", status.Dir)
	}
}

// Permission denied on the directory is not "nothing forwarded".
func TestStreamsReportsAnUnreadableDirectory(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads a directory whatever its mode")
	}
	svc, _ := streamHost(t)
	if err := os.Chmod(svc.streamDir(), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(svc.streamDir(), 0o755) })
	if _, err := svc.Streams(context.Background()); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("got %v, want the read error", err)
	}
}

func TestStreamsListsEveryFileNginxWouldRead(t *testing.T) {
	svc, _ := streamHost(t)
	rendered, _ := RenderStream(tcpStream())
	writeStream(t, svc, "postgres-replica", rendered)
	writeStream(t, svc, "Upper", "server { listen 6000; proxy_buffer_size 4k; proxy_pass 10.0.0.5:6000; }")
	writeStream(t, svc, "open", "server { listen 7000; allow all; deny all; proxy_pass 10.0.0.5:7000; }")
	for _, skipped := range []string{".hidden.conf", "old.conf.bak", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(svc.streamDir(), skipped), []byte("server {}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	unreadable := writeStream(t, svc, "secret", "server { listen 8000; proxy_pass 10.0.0.5:8000; }")
	if os.Getuid() != 0 {
		if err := os.Chmod(unreadable, 0o000); err != nil {
			t.Fatal(err)
		}
	}
	status, err := svc.Streams(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]StreamEntry{}
	for _, e := range status.Streams {
		byName[e.Name] = e
	}
	if len(byName) != 4 {
		t.Fatalf("listed %d: %+v", len(byName), status.Streams)
	}
	if e := byName["postgres-replica"]; !e.Managed || e.Open || len(e.Unsupported) != 0 {
		t.Errorf("managed stream: %+v", e)
	}
	if e := byName["Upper"]; e.Managed || !reflect.DeepEqual(e.Unsupported, []string{"proxy_buffer_size"}) {
		t.Errorf("hand-written stream: %+v", e)
	}
	if e := byName["open"]; !e.Open || len(e.AllowFrom) != 0 {
		t.Errorf("allow all should read as open: %+v", e)
	}
	if e := byName["secret"]; os.Getuid() != 0 && e.Error == "" {
		t.Errorf("an unreadable file should say so: %+v", e)
	}
}

// "New stream" and "Edit this stream" post to one route, so without the guard
// a new one named after an existing one replaced it in silence — a forwarding
// rule that quietly stopped pointing where it used to.
func TestApplyStreamRefusesToReplaceANewNameThatIsTaken(t *testing.T) {
	svc, _ := streamHost(t)
	path := writeStream(t, svc, "postgres-replica", "# existing\n")
	if _, err := svc.ApplyStream(context.Background(), tcpStream(), "", false); !errors.Is(err, ErrStreamExists) {
		t.Fatalf("got %v, want ErrStreamExists", err)
	}
	if got := mustRead(t, path); got != "# existing\n" {
		t.Fatalf("the existing file was touched: %q", got)
	}
}

// Renaming used to post overwrite with the new name: editing "bastion" and
// calling it "postgres-replica" replaced postgres-replica's file with no
// backup, and renaming to a free name left both files on one port.
func TestApplyStreamRenames(t *testing.T) {
	svc, root := streamHost(t)
	bastion := tcpStream()
	bastion.Name, bastion.Listen, bastion.Upstream = "bastion", 2222, "10.0.0.9:22"
	rendered, _ := RenderStream(bastion)
	old := writeStream(t, svc, "bastion", rendered)
	other, _ := RenderStream(tcpStream())
	otherPath := writeStream(t, svc, "postgres-replica", other)

	onto := *bastion
	onto.Name = "postgres-replica"
	if _, err := svc.ApplyStream(context.Background(), &onto, "bastion", false); !errors.Is(err, ErrStreamExists) {
		t.Fatalf("rename onto another stream: got %v", err)
	}
	if mustRead(t, otherPath) != other || mustRead(t, old) != rendered {
		t.Fatal("a refused rename changed a file")
	}

	renamed := *bastion
	renamed.Name = "ssh"
	res, err := svc.ApplyStream(context.Background(), &renamed, "bastion", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Renamed != "bastion" {
		t.Errorf("renamed = %q", res.Renamed)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("the old file is still read by nginx")
	}
	if mustRead(t, old+".bak") != rendered {
		t.Error("the old file was not kept as .bak")
	}
	if !strings.Contains(mustRead(t, filepath.Join(svc.streamDir(), "ssh.conf")), "# Stream: ssh") {
		t.Error("the new file was not written")
	}
	if strings.Count(nginxRuns(t, root), "-t") != 1 {
		t.Errorf("a rename is one test, got %q", nginxRuns(t, root))
	}
}

// A rename whose test fails leaves both names exactly as they were, the
// backup of an earlier delete included.
func TestApplyStreamPutsARenameBackWhenTheTestFails(t *testing.T) {
	svc, root := streamHost(t)
	rendered, _ := RenderStream(tcpStream())
	old := writeStream(t, svc, "postgres-replica", rendered)
	if err := os.WriteFile(old+".bak", []byte("# deleted last week\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fail-test"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	renamed := tcpStream()
	renamed.Name = "pg"
	if _, err := svc.ApplyStream(context.Background(), renamed, "postgres-replica", false); !errors.Is(err, ErrInvalidConf) {
		t.Fatalf("got %v", err)
	}
	if mustRead(t, old) != rendered || mustRead(t, old+".bak") != "# deleted last week\n" {
		t.Fatal("the old file or its backup changed")
	}
	if _, err := os.Stat(filepath.Join(svc.streamDir(), "pg.conf")); !os.IsNotExist(err) {
		t.Fatal("the new name was left behind")
	}
}

// A file the form cannot express is not saved over: the form would have
// dropped what it does not know, here a buffer size.
func TestApplyStreamRefusesToOverwriteAHandWrittenFile(t *testing.T) {
	svc, _ := streamHost(t)
	content := "server { listen 5432; proxy_buffer_size 4k; proxy_pass 10.0.0.5:5432; }\n"
	path := writeStream(t, svc, "postgres-replica", content)
	var handwritten *HandwrittenStreamError
	if _, err := svc.ApplyStream(context.Background(), tcpStream(), "postgres-replica", false); !errors.As(err, &handwritten) {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(handwritten.Error(), "proxy_buffer_size") {
		t.Errorf("error does not say what would be lost: %v", handwritten)
	}
	if mustRead(t, path) != content {
		t.Fatal("the hand-written file was changed")
	}
}

// A hand-written file the form can express is rewritten in the dashboard's
// layout, and the original is kept beside it; a loopback bind stays one.
func TestApplyStreamKeepsTheHandWrittenOriginal(t *testing.T) {
	svc, _ := streamHost(t)
	content := "# the replica, only for the app on this box\nserver { listen 127.0.0.1:6000; proxy_pass 10.0.0.5:5432; }\n"
	path := writeStream(t, svc, "replica", content)
	spec, _, _ := ParseStreamSpec("replica", content)
	spec.Timeout = 60
	if _, err := svc.ApplyStream(context.Background(), spec, "replica", false); err != nil {
		t.Fatal(err)
	}
	written := mustRead(t, path)
	if !strings.Contains(written, "listen 127.0.0.1:6000;") || strings.Contains(written, "[::]") ||
		strings.Contains(written, "listen 6000;") {
		t.Fatalf("the loopback bind was widened:\n%s", written)
	}
	if mustRead(t, path+".bak") != content {
		t.Fatal("the hand-written original was not kept")
	}
}

// A file whose name the form would refuse can still be renamed to one it
// accepts, and deleted.
func TestAStreamWithAnUnusualNameCanBeRenamedAndDeleted(t *testing.T) {
	svc, _ := streamHost(t)
	writeStream(t, svc, "Upper", "server { listen 6000; proxy_pass 10.0.0.5:6000; }\n")
	spec, _, _ := ParseStreamSpec("Upper", mustRead(t, filepath.Join(svc.streamDir(), "Upper.conf")))
	spec.Name = "upper"
	if _, err := svc.ApplyStream(context.Background(), spec, "Upper", false); err != nil {
		t.Fatal(err)
	}
	writeStream(t, svc, "Other", "server { listen 7000; proxy_pass 10.0.0.5:7000; }\n")
	if _, err := svc.DeleteStream(context.Background(), "Other"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(svc.streamDir(), "Other.conf")); !os.IsNotExist(err) {
		t.Fatal("Other.conf is still there")
	}
	for _, bad := range []string{"../nginx", ".hidden", "", "a/b"} {
		if _, err := svc.DeleteStream(context.Background(), bad); err == nil {
			t.Errorf("deleted %q", bad)
		}
	}
	if _, err := svc.DeleteStream(context.Background(), "missing"); !errors.Is(err, ErrStreamNotFound) {
		t.Errorf("missing stream: %v", err)
	}
}

// "Save for later" sent reload anyway; the save now reloads only when asked,
// and says when nginx could not have tested the file.
func TestApplyStreamReloadsOnlyWhenAsked(t *testing.T) {
	svc, root := streamHost(t)
	res, err := svc.ApplyStream(context.Background(), tcpStream(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(nginxRuns(t, root), "-s reload") || res.Reloaded {
		t.Fatalf("reloaded without being asked: %q", nginxRuns(t, root))
	}
	if !strings.Contains(res.Validation.Note, "not include") {
		t.Errorf("an untested file claimed a test: %+v", res.Validation)
	}
}

// A reload that fails after the test passed left the file written while the
// page said "Not applied". The file is valid and stays; the result says the
// reload failed.
func TestApplyStreamReportsAFailedReload(t *testing.T) {
	svc, root := streamHost(t)
	if err := os.WriteFile(filepath.Join(root, "fail-reload"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := svc.ApplyStream(context.Background(), tcpStream(), "", true)
	if err != nil {
		t.Fatalf("a failed reload is not a failed save: %v", err)
	}
	if res.Reloaded || !strings.Contains(res.ReloadError, "No such process") {
		t.Fatalf("result = %+v", res)
	}
	if _, err := os.Stat(res.Path); err != nil {
		t.Fatal("the tested file was removed")
	}
}

// A delete wrote the .bak first, so a file it could not read — the listing's
// "unreadable" — failed with permission denied and stayed, and a link to
// nothing, the one file that fails `nginx -t` for the whole host, was refused
// as "not a regular file". Both are removed now, without a backup, and the
// result says why none was kept.
func TestDeleteStreamRemovesWhatItCannotKeep(t *testing.T) {
	svc, root := streamHost(t)
	dir := svc.streamDir()

	kept := writeStream(t, svc, "kept", "server { listen 6000; proxy_pass 10.0.0.5:6000; }\n")
	res, err := svc.DeleteStream(context.Background(), "kept")
	if err != nil {
		t.Fatal(err)
	}
	if res.Backup != kept+".bak" || res.Link != "" || res.Unread != "" || !strings.Contains(mustRead(t, kept+".bak"), "listen 6000") {
		t.Fatalf("readable file: %+v", res)
	}

	dangling := filepath.Join(dir, "dangling.conf")
	if err := os.Symlink("../streams-available/gone.conf", dangling); err != nil {
		t.Fatal(err)
	}
	res, err = svc.DeleteStream(context.Background(), "dangling")
	if err != nil {
		t.Fatalf("a link to nothing: %v", err)
	}
	if _, err := os.Lstat(dangling); !os.IsNotExist(err) {
		t.Fatal("the dangling link is still there")
	}
	if res.Link != "../streams-available/gone.conf" || res.Backup != "" {
		t.Fatalf("dangling link: %+v", res)
	}

	available := filepath.Join(root, "streams-available")
	if err := os.MkdirAll(available, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(available, "linked.conf")
	if err := os.WriteFile(target, []byte("server { listen 7000; proxy_pass 10.0.0.5:7000; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../streams-available/linked.conf", filepath.Join(dir, "linked.conf")); err != nil {
		t.Fatal(err)
	}
	if res, err = svc.DeleteStream(context.Background(), "linked"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "linked.conf")); !os.IsNotExist(err) {
		t.Fatal("the link is still there")
	}
	if !strings.Contains(mustRead(t, target), "listen 7000") || res.Backup != "" {
		t.Fatalf("the link's target was touched, or a backup made: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "linked.conf.bak")); !os.IsNotExist(err) {
		t.Fatal("a link was backed up as a file")
	}

	if os.Getuid() == 0 {
		t.Skip("root reads a file whatever its mode")
	}
	locked := writeStream(t, svc, "locked", "server { listen 8000; proxy_pass 10.0.0.5:8000; }\n")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	if res, err = svc.DeleteStream(context.Background(), "locked"); err != nil {
		t.Fatalf("an unreadable file: %v", err)
	}
	if _, err := os.Lstat(locked); !os.IsNotExist(err) {
		t.Fatal("the unreadable file is still there")
	}
	if !strings.Contains(res.Unread, "permission denied") || res.Backup != "" {
		t.Fatalf("unreadable file: %+v", res)
	}
	if _, err := os.Stat(locked + ".bak"); !os.IsNotExist(err) {
		t.Fatal("an empty backup was left for a file never read")
	}
}

// A linked stream was listed as editable, and both its save and its delete
// then failed with "not a regular file". It is listed as a link now, so the
// form opens read-only, and a save over it is refused as hand-written without
// replacing the link with a file.
func TestALinkedStreamIsListedAsOneAndNotSavedOver(t *testing.T) {
	svc, root := streamHost(t)
	dir := svc.streamDir()
	available := filepath.Join(root, "streams-available")
	if err := os.MkdirAll(available, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "server { listen 7000; proxy_pass 10.0.0.5:7000; }\n"
	if err := os.WriteFile(filepath.Join(available, "linked.conf"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked.conf")
	for from, to := range map[string]string{"linked": "../streams-available/linked.conf", "dangling": "../streams-available/gone.conf"} {
		if err := os.Symlink(to, filepath.Join(dir, from+".conf")); err != nil {
			t.Fatal(err)
		}
	}
	status, err := svc.Streams(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]StreamEntry{}
	for _, e := range status.Streams {
		byName[e.Name] = e
	}
	linked := byName["linked"]
	if linked.Link != "../streams-available/linked.conf" || linked.Listen != 7000 || linked.Error != "" ||
		!reflect.DeepEqual(linked.Unsupported, []string{"a symbolic link"}) {
		t.Errorf("linked stream: %+v", linked)
	}
	dangling := byName["dangling"]
	if dangling.Error != "it links to ../streams-available/gone.conf, which does not exist" ||
		dangling.Link != "../streams-available/gone.conf" {
		t.Errorf("dangling link: %+v", dangling)
	}

	spec, _, _ := ParseStreamSpec("linked", content)
	var handwritten *HandwrittenStreamError
	if _, err := svc.ApplyStream(context.Background(), spec, "linked", false); !errors.As(err, &handwritten) ||
		!strings.Contains(err.Error(), "a symbolic link") {
		t.Fatalf("saving over a link: %v", err)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced")
	}
}

// With the stream directory included inside http, nginx reads the files and
// its test refuses them; the save said the test could not have seen the file.
func TestAStreamIncludedInTheWrongBlockIsTested(t *testing.T) {
	for _, tc := range []struct {
		conf, note string
	}{
		{"events {}\nhttp { include stream.d/*.conf; }\n", ""},
		{"events {}\nstream { include stream.d/*.conf; }\n", ""},
		{"events {}\nhttp {}\n", "not include"},
	} {
		svc, root := streamHost(t)
		if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte(tc.conf), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := svc.ApplyStream(context.Background(), tcpStream(), "", false)
		if err != nil {
			t.Fatal(err)
		}
		if tc.note == "" && res.Validation.Note != "" || tc.note != "" && !strings.Contains(res.Validation.Note, tc.note) {
			t.Errorf("%q: note = %q", tc.conf, res.Validation.Note)
		}
	}
}
