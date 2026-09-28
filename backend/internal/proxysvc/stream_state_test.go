package proxysvc

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nginxLayout writes files under a temporary nginx directory, the first
// being nginx.conf, and returns the directory and its stream directory.
func nginxLayout(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.ReplaceAll(content, "$ROOT", root)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "stream.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(root, "stream.d")
}

// The snippet this page prints is exactly what people paste into nginx.conf
// commented out while they think about it. A banner that disappears then is
// worse than none: the files go on being written and silently ignored.
func TestStreamIncludeIgnoresACommentedOutInclude(t *testing.T) {
	root, streams := nginxLayout(t, map[string]string{
		"nginx.conf": "# stream {\n#     include $ROOT/stream.d/*.conf;\n# }\nhttp { }\n",
	})
	if streamIncludeFound(root, streams) {
		t.Error("a commented-out include was read as present")
	}
	root, streams = nginxLayout(t, map[string]string{
		"nginx.conf": "stream {\n    include $ROOT/stream.d/*.conf;\n}\nhttp { }\n",
	})
	if !streamIncludeFound(root, streams) {
		t.Error("a real include was not found")
	}
}

// Include detection was a substring search: "stream" anywhere (every
// `upstream`) plus "stream.d" anywhere. It is now nginx's own include
// resolution, with a probe file standing where a new stream would.
func TestStreamIncludeIsWhereNginxReadsTheDirectory(t *testing.T) {
	cases := []struct {
		name      string
		files     map[string]string
		included  bool
		misplaced string
	}{
		{
			// The mistake the page warns about read as correct: the include
			// inside http, in a file that also has an upstream block.
			name: "inside http beside an upstream",
			files: map[string]string{"nginx.conf": `http {
    upstream app { server 127.0.0.1:3000; }
    include $ROOT/stream.d/*.conf;
}`},
			misplaced: "http",
		},
		{
			name:  "another directory with stream.d in its name",
			files: map[string]string{"nginx.conf": "stream { include $ROOT/other-stream.d/*.conf; }\nhttp {}\n"},
		},
		{
			// A correct stream block kept in its own file read as missing.
			name: "stream block in a separate top-level file",
			files: map[string]string{
				"nginx.conf":                "include $ROOT/modules-enabled/*.conf;\nhttp {}\n",
				"modules-enabled/zz-s.conf": "stream {\n    include $ROOT/stream.d/*.conf;\n}\n",
			},
			included: true,
		},
		{
			name:     "relative include, the way Debian writes them",
			files:    map[string]string{"nginx.conf": "stream { include stream.d/*.conf; }\n"},
			included: true,
		},
		{
			name:      "at the top level with no block",
			files:     map[string]string{"nginx.conf": "include stream.d/*.conf;\n"},
			misplaced: "the top level, outside any block",
		},
		{
			name:      "inside a server of the stream block",
			files:     map[string]string{"nginx.conf": "stream { server { include stream.d/*.conf; } }\n"},
			misplaced: "stream › server",
		},
		{
			// A broken site file does not hide the answer: nginx would refuse
			// it anyway, and the stream block is elsewhere.
			name: "a broken file elsewhere",
			files: map[string]string{
				"nginx.conf":              "stream { include stream.d/*.conf; }\nhttp { include sites-enabled/*; }\n",
				"sites-enabled/broken":    "server { listen 80;\n",
				"stream.d/existing.conf":  "server { listen 6000; proxy_pass 10.0.0.5:6000; }\n",
				"stream.d/.ignored.conf":  "}}}",
				"stream.d/old.conf.bak":   "}}}",
				"sites-enabled/fine-site": "server { listen 81; }\n",
			},
			included: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, streams := nginxLayout(t, tc.files)
			got := readStreamInclude(root, streams)
			if got.err != nil {
				t.Fatal(got.err)
			}
			if got.included != tc.included || got.misplaced != tc.misplaced {
				t.Fatalf("included=%v misplaced=%q, want %v %q", got.included, got.misplaced, tc.included, tc.misplaced)
			}
		})
	}
}

func TestStreamIncludeReportsAnUnreadableMainFile(t *testing.T) {
	root, streams := nginxLayout(t, map[string]string{"nginx.conf": "stream { include stream.d/*.conf;\n"})
	if got := readStreamInclude(root, streams); got.err == nil || got.included {
		t.Fatalf("a main file that does not parse gave %+v", got)
	}
	if got := readStreamInclude(t.TempDir(), streams); got.err == nil {
		t.Fatal("a missing nginx.conf gave no error")
	}
}

// Two stream files on one port pass `nginx -t` with a warning, and nginx
// forwards to whichever sorts first while the other card said "forwarding".
func TestApplyStreamRefusesAPortAnotherStreamHolds(t *testing.T) {
	svc, _ := streamHost(t)
	writeStream(t, svc, "bastion", "server { listen 5432; proxy_pass 10.0.0.9:22; }\n")
	writeStream(t, svc, "next", "server { listen 5433; proxy_pass 10.0.0.9:22; }\n")
	var inUse *PortInUseError
	if _, err := svc.ApplyStream(context.Background(), tcpStream(), "", false); !errors.As(err, &inUse) {
		t.Fatalf("got %v, want PortInUseError", err)
	}
	if inUse.Kind != OwnerStream || inUse.Name != "bastion" || inUse.Port != 5432 || inUse.Suggest != 5434 {
		t.Fatalf("got %+v", inUse)
	}
	if _, err := os.Stat(filepath.Join(svc.streamDir(), "postgres-replica.conf")); !os.IsNotExist(err) {
		t.Fatal("the refused stream was written")
	}

	// The same port over UDP, or on another address, is a different socket.
	udp := tcpStream()
	udp.Protocol = "udp"
	if _, err := svc.ApplyStream(context.Background(), udp, "", false); err != nil {
		t.Fatalf("udp on a tcp port: %v", err)
	}
	loopback := tcpStream()
	loopback.Name, loopback.Listen, loopback.Address = "local", 6000, "127.0.0.1"
	writeStream(t, svc, "other-local", "server { listen 127.0.0.2:6000; proxy_pass 10.0.0.9:22; }\n")
	if _, err := svc.ApplyStream(context.Background(), loopback, "", false); err != nil {
		t.Fatalf("another loopback address: %v", err)
	}
	everywhere := tcpStream()
	everywhere.Name, everywhere.Listen = "everywhere", 6000
	if _, err := svc.ApplyStream(context.Background(), everywhere, "", false); !errors.As(err, &inUse) {
		t.Fatalf("a wildcard over a specific address: %v", err)
	}
}

// A port held by another program passes `nginx -t` and `nginx -s reload`
// both, while the master logs "Address already in use" and keeps the old
// configuration — so the save said "forwarding" and nothing was.
func TestApplyStreamRefusesAPortAnotherProgramHolds(t *testing.T) {
	withListeners(t, func(context.Context) ([]Listener, error) {
		return []Listener{
			{Protocol: "tcp", Address: "0.0.0.0", Port: 5432, PID: 900, Process: "postgres"},
			{Protocol: "tcp", Address: "::", Port: 5433, PID: 901, Process: "node"},
		}, nil
	})
	svc, _ := streamHost(t)
	var inUse *PortInUseError
	if _, err := svc.ApplyStream(context.Background(), tcpStream(), "", false); !errors.As(err, &inUse) {
		t.Fatalf("got %v", err)
	}
	if inUse.Name != "postgres" || inUse.PID != 900 || inUse.Suggest != 5434 {
		t.Fatalf("got %+v", inUse)
	}
	if !strings.Contains(inUse.Error(), "postgres (pid 900)") || !strings.Contains(inUse.Error(), "5434 is free") {
		t.Errorf("message: %s", inUse.Error())
	}

	// A program's [::] socket is taken to hold IPv4 as well, since whether it
	// is dual-stack cannot be seen from outside.
	v4 := tcpStream()
	v4.Name, v4.Listen, v4.Address = "v4", 5433, "127.0.0.1"
	if _, err := svc.ApplyStream(context.Background(), v4, "", false); !errors.As(err, &inUse) || inUse.Name != "node" {
		t.Fatalf("got %v", err)
	}
}

// nginx holding the stream's own socket is the stream serving, not a conflict:
// editing its upstream, or renaming it, must not be refused.
func TestApplyStreamIsNotInItsOwnWay(t *testing.T) {
	withListeners(t, func(context.Context) ([]Listener, error) {
		return []Listener{
			{Protocol: "tcp", Address: "0.0.0.0", Port: 5432, PID: 50, Process: "nginx"},
			{Protocol: "tcp", Address: "::", Port: 5432, PID: 50, Process: "nginx"},
			{Protocol: "tcp", Address: "0.0.0.0", Port: 8080, PID: 50, Process: "nginx"},
		}, nil
	})
	svc, _ := streamHost(t)
	rendered, _ := RenderStream(tcpStream())
	writeStream(t, svc, "postgres-replica", rendered)
	edited := tcpStream()
	edited.Upstream = "10.0.0.6:5432"
	if _, err := svc.ApplyStream(context.Background(), edited, "postgres-replica", false); err != nil {
		t.Fatalf("editing: %v", err)
	}
	renamed := tcpStream()
	renamed.Name = "pg"
	if _, err := svc.ApplyStream(context.Background(), renamed, "postgres-replica", false); err != nil {
		t.Fatalf("renaming: %v", err)
	}
	// nginx holding a port for something else — a site — is a conflict.
	site := tcpStream()
	site.Name, site.Listen = "web", 8080
	var inUse *PortInUseError
	if _, err := svc.ApplyStream(context.Background(), site, "", false); !errors.As(err, &inUse) || inUse.Name != "nginx" {
		t.Fatalf("a port nginx holds for a site: %v", err)
	}
}

// The real thing: a socket this test holds is found by reading the host's
// listeners, and named after this process.
func TestApplyStreamFindsARealListener(t *testing.T) {
	withListeners(t, ListListeners)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	svc, _ := streamHost(t)
	spec := tcpStream()
	spec.Listen = port
	var inUse *PortInUseError
	if _, err := svc.ApplyStream(context.Background(), spec, "", false); !errors.As(err, &inUse) {
		t.Fatalf("got %v, want the test's own socket found", err)
	}
	if inUse.PID != int32(os.Getpid()) {
		t.Fatalf("owner %q pid %d, want this process (%d)", inUse.Name, inUse.PID, os.Getpid())
	}
}

// A host whose sockets cannot be read still saves, and says what was not
// checked.
func TestApplyStreamSaysWhenItCouldNotCheckTheHost(t *testing.T) {
	withListeners(t, func(context.Context) ([]Listener, error) { return nil, errors.New("permission denied") })
	svc, _ := streamHost(t)
	res, err := svc.ApplyStream(context.Background(), tcpStream(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(res.Warnings, "was not checked") {
		t.Fatalf("warnings: %v", res.Warnings)
	}
}

// A stream of both protocols asks for a TCP and a UDP socket, and the
// refusal names the protocol that clashed.
func TestApplyStreamChecksBothProtocols(t *testing.T) {
	withListeners(t, func(context.Context) ([]Listener, error) {
		return []Listener{{Protocol: "tcp", Address: "127.0.0.1", Port: 853, PID: 70, Process: "unbound"}}, nil
	})
	svc, _ := streamHost(t)
	writeStream(t, svc, "syslog", "server { listen 514 udp; proxy_pass 10.0.0.9:514; }\n")
	both := tcpStream()
	both.Name, both.Listen, both.Protocol = "logs", 514, "both"
	var inUse *PortInUseError
	if _, err := svc.ApplyStream(context.Background(), both, "", false); !errors.As(err, &inUse) {
		t.Fatalf("got %v, want the UDP side refused", err)
	}
	if inUse.Proto != "udp" || inUse.Kind != OwnerStream || inUse.Name != "syslog" || !strings.Contains(inUse.Error(), "514/udp") {
		t.Fatalf("got %+v", inUse)
	}
	both.Listen = 853
	if _, err := svc.ApplyStream(context.Background(), both, "", false); !errors.As(err, &inUse) || inUse.Proto != "tcp" || inUse.Name != "unbound" {
		t.Fatalf("got %v, want the TCP side refused", err)
	}
	both.Listen = 5353
	if _, err := svc.ApplyStream(context.Background(), both, "", false); err != nil {
		t.Fatalf("a free port: %v", err)
	}
	if got := parseStreamFile("logs.conf", mustRead(t, filepath.Join(svc.streamDir(), "logs.conf"))).binds; len(got) != 4 {
		t.Fatalf("binds = %+v, want TCP and UDP on both families", got)
	}
}

func TestBindsClash(t *testing.T) {
	cases := []struct {
		a, b bind
		want bool
	}{
		{bind{"0.0.0.0", 1, false}, bind{"127.0.0.1", 1, false}, true},
		{bind{"127.0.0.1", 1, false}, bind{"127.0.0.2", 1, false}, false},
		{bind{"::", 1, false}, bind{"0.0.0.0", 1, false}, false},
		{bind{"::", 1, false}, bind{"::1", 1, false}, true},
		{bind{"0.0.0.0", 1, false}, bind{"0.0.0.0", 1, true}, false},
		{bind{"0.0.0.0", 1, false}, bind{"0.0.0.0", 2, false}, false},
	}
	for _, tc := range cases {
		if got := bindsClash(tc.a, tc.b); got != tc.want {
			t.Errorf("%+v vs %+v: %v", tc.a, tc.b, got)
		}
	}
}
