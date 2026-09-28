package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// watchedHost is a stream directory nginx.conf includes, beside a site on
// 8080, behind an nginx shim: nginx.org's build for -V, a test that passes,
// and a reload that runs $root/on-reload when there is one. nginx logs to
// $root/error.log.
func watchedHost(t *testing.T, conf string) (*Service, string) {
	t.Helper()
	if conf == "" {
		conf = "error_log $ROOT/error.log;\nhttp { include $ROOT/sites-enabled/*; }\nstream { include $ROOT/stream.d/*.conf; }\n"
	}
	root, _ := nginxLayout(t, map[string]string{
		"nginx.conf":        conf,
		"sites-enabled/web": "server { listen 8080; server_name web.example.com; }\n",
	})
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	version := filepath.Join(root, "version")
	if err := os.WriteFile(version, []byte(alpineNginxV), 0o644); err != nil {
		t.Fatal(err)
	}
	shim := fmt.Sprintf(`#!/bin/sh
case "$1" in
-V) cat '%[1]s' >&2 ;;
-s) if [ -x '%[2]s/on-reload' ]; then '%[2]s/on-reload'; fi ;;
esac
exit 0
`, version, root)
	if err := os.WriteFile(filepath.Join(bin, "nginx"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return New(root, filepath.Join(root, "Caddyfile")), root
}

// onReload is what the running nginx does when it is signalled.
func onReload(t *testing.T, root, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "on-reload"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// reloadingNginx is a running nginx whose worker is replaced — the proof a
// reload took — once $root/reloaded exists, and which then holds sockets.
func reloadingNginx(t *testing.T, root string, after []socket, held ...uint64) {
	t.Helper()
	withNginx(t, func(int32) *socketView {
		if _, err := os.Stat(filepath.Join(root, "reloaded")); err != nil {
			return &socketView{nginx: &nginxProcess{master: 7, workers: []int32{10}}, held: map[uint64]bool{}, local: true}
		}
		inodes := map[uint64]bool{}
		for _, inode := range held {
			inodes[inode] = true
		}
		return &socketView{nginx: &nginxProcess{master: 7, workers: []int32{11}}, sockets: after, held: inodes, local: true}
	})
}

func listens(port int) []socket {
	return []socket{{addr: "0.0.0.0", port: port, inode: 5}, {addr: "::", port: port, inode: 6}}
}

func pgStream(port int) *StreamSpec {
	return &StreamSpec{Name: "replica", Listen: port, Upstream: "10.0.0.5:5432", AllowFrom: []string{"10.0.0.0/8"}}
}

// A reload the running nginx took up, holding the stream's sockets, is a
// stream that listens.
func TestApplyStreamWatchesNginxTakeItUp(t *testing.T) {
	svc, root := watchedHost(t, "")
	onReload(t, root, "touch '"+root+"/reloaded'")
	reloadingNginx(t, root, listens(6432), 5, 6)
	res, err := svc.ApplyStream(context.Background(), pgStream(6432), "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Reloaded || res.Listening == nil || !*res.Listening || res.ListenNote != "" {
		t.Fatalf("result = %+v listening=%v", res, res.Listening)
	}
}

// nginx refusing the stream's own port at the reload — a program took it
// after the check — puts the stream back, since a stream nginx cannot bind
// makes every later reload on the host fail, and says so with nginx's words.
func TestApplyStreamPutsBackAStreamNginxCouldNotBind(t *testing.T) {
	for _, tc := range []struct {
		name     string
		previous string
		before   string
	}{
		{name: "new"},
		{name: "edit", previous: "replica"},
		{name: "rename", previous: "old"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withListeners(t, func(context.Context) ([]Listener, error) {
				return []Listener{{Protocol: "tcp", Address: "0.0.0.0", Port: 6432, PID: 900, Process: "postgres"}}, nil
			})
			svc, root := watchedHost(t, "")
			var previous string
			if tc.previous != "" {
				rendered, _ := RenderStream(&StreamSpec{Name: tc.previous, Listen: 6431, Upstream: "10.0.0.5:5432", AllowFrom: []string{"10.0.0.0/8"}})
				previous = writeStream(t, svc, tc.previous, rendered)
			}
			onReload(t, root, fmt.Sprintf(`touch '%[1]s/reloaded'
echo '2026/09/28 03:29:05 [emerg] 1#1: bind() to 0.0.0.0:6432 failed (98: Address in use)' >> '%[1]s/error.log'
echo '2026/09/28 03:29:05 [emerg] 1#1: still could not bind()' >> '%[1]s/error.log'`, root))
			// After the failed reload nginx still serves what it had, and the
			// program holds 6432.
			reloadingNginx(t, root, []socket{{addr: "0.0.0.0", port: 6432, inode: 9, uid: 1000}})

			before := map[string]string{}
			entries, _ := os.ReadDir(svc.streamDir())
			for _, e := range entries {
				before[e.Name()] = mustRead(t, filepath.Join(svc.streamDir(), e.Name()))
			}
			spec := pgStream(6432)
			_, err := svc.ApplyStream(context.Background(), spec, tc.previous, true)
			var inUse *PortInUseError
			if !errors.As(err, &inUse) {
				t.Fatalf("got %v, want the stream refused", err)
			}
			if !strings.Contains(inUse.BindError, "bind() to 0.0.0.0:6432 failed (98: Address in use)") ||
				inUse.Name != "postgres" || inUse.PID != 900 || inUse.Port != 6432 || inUse.Suggest != 6433 {
				t.Fatalf("refusal = %+v", inUse)
			}
			if msg := inUse.Error(); !strings.Contains(msg, "nginx could not bind port 6432/tcp when it reloaded") ||
				!strings.Contains(msg, "held by postgres (pid 900)") || !strings.Contains(msg, "put back as it was") {
				t.Fatalf("message = %s", msg)
			}
			after := map[string]string{}
			entries, _ = os.ReadDir(svc.streamDir())
			for _, e := range entries {
				after[e.Name()] = mustRead(t, filepath.Join(svc.streamDir(), e.Name()))
			}
			if fmt.Sprint(after) != fmt.Sprint(before) {
				t.Fatalf("stream directory %v, want it as it was: %v", after, before)
			}
			if previous != "" && mustRead(t, previous) != before[filepath.Base(previous)] {
				t.Fatal("the previous file was not put back")
			}
		})
	}
}

// A reload nginx refused for something else — another stream's port — is
// the save reported as saved and not reloaded, with nginx's reason; the file
// is valid and stays.
func TestApplyStreamReportsAReloadNginxRefusedForAnotherPort(t *testing.T) {
	svc, root := watchedHost(t, "")
	onReload(t, root, fmt.Sprintf(`touch '%[1]s/reloaded'
echo '2026/09/28 03:29:05 [emerg] 1#1: bind() to 0.0.0.0:9999 failed (98: Address in use)' >> '%[1]s/error.log'`, root))
	reloadingNginx(t, root, nil)
	res, err := svc.ApplyStream(context.Background(), pgStream(6432), "", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reloaded || res.ReloadError != "nginx did not take the reload up: bind() to 0.0.0.0:9999 failed (98: Address in use)" || res.Listening != nil {
		t.Fatalf("result = %+v", res)
	}
	if _, err := os.Stat(res.Path); err != nil {
		t.Fatal("a valid file was removed for another stream's port")
	}
}

// A reload nginx has not taken up when the wait runs out is not called
// listening, and says what nginx had not done.
func TestApplyStreamStopsWaiting(t *testing.T) {
	previous := listenWait
	listenWait = 300 * time.Millisecond
	t.Cleanup(func() { listenWait = previous })
	svc, _ := watchedHost(t, "")
	reloadingNginx(t, t.TempDir(), nil)
	start := time.Now()
	res, err := svc.ApplyStream(context.Background(), pgStream(6432), "", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Listening == nil || *res.Listening || !strings.Contains(res.ListenNote, "had not taken the reload up") {
		t.Fatalf("result = %+v", res)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the wait took %s", time.Since(start))
	}
}

// Where no running nginx can be read, or nginx does not read the stream, the
// save says it could not watch rather than calling the stream listening.
func TestApplyStreamSaysWhenItCouldNotWatch(t *testing.T) {
	svc, _ := watchedHost(t, "")
	res, err := svc.ApplyStream(context.Background(), pgStream(6432), "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Reloaded || res.Listening != nil || res.ListenNote != "Whether nginx took it up could not be checked: no nginx runs for unit tests." {
		t.Fatalf("result = %+v", res)
	}
	svc, _ = watchedHost(t, "error_log $ROOT/error.log;\nhttp { include $ROOT/sites-enabled/*; }\n")
	res, err = svc.ApplyStream(context.Background(), pgStream(6432), "", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Listening != nil || !strings.Contains(res.ListenNote, "does not read this file as a stream") {
		t.Fatalf("result = %+v", res)
	}
}

// A site on the port refuses the save, named as the Sites page names it:
// nginx cannot bind one port for its http and its stream module, its test
// passes, and every reload then fails.
func TestApplyStreamRefusesAPortASiteHolds(t *testing.T) {
	svc, _ := watchedHost(t, "")
	var inUse *PortInUseError
	if _, err := svc.ApplyStream(context.Background(), pgStream(8080), "", false); !errors.As(err, &inUse) {
		t.Fatalf("got %v", err)
	}
	if inUse.Kind != OwnerSite || inUse.Name != "web.example.com" || inUse.Site != "web" || inUse.Suggest != 8081 {
		t.Fatalf("refusal = %+v", inUse)
	}
	if got := inUse.Error(); got != "port 8080/tcp is already in use by the site web.example.com — 8081 is free" {
		t.Fatalf("message = %s", got)
	}
	// UDP 8080 is another socket.
	udp := pgStream(8080)
	udp.Protocol = "udp"
	if _, err := svc.ApplyStream(context.Background(), udp, "", false); err != nil {
		t.Fatalf("udp beside a tcp site: %v", err)
	}
}

// What the running nginx holds is read from nginx itself: its own sockets
// are no conflict — one it holds for a server no longer configured goes at
// the reload — and a socket its descriptors do not hold is another
// program's, named where the host's list can.
func TestApplyStreamReadsNginxSockets(t *testing.T) {
	withListeners(t, func(context.Context) ([]Listener, error) {
		return []Listener{{Protocol: "tcp", Address: "127.0.0.1", Port: 7000, PID: 31, Process: "redis-server"}}, nil
	})
	svc, _ := watchedHost(t, "")
	sockets := []socket{
		{addr: "0.0.0.0", port: 9090, inode: 1},
		{addr: "127.0.0.1", port: 7000, inode: 2},
		{addr: "0.0.0.0", port: 7100, inode: 3, uid: 0},
	}
	view := func(held map[uint64]bool) func(int32) *socketView {
		return func(int32) *socketView {
			return &socketView{nginx: &nginxProcess{master: 7, uid: 0, workers: []int32{10}}, sockets: sockets, held: held, local: true}
		}
	}
	named := func(name string, port int) *StreamSpec {
		spec := pgStream(port)
		spec.Name = name
		return spec
	}
	withNginx(t, view(map[uint64]bool{1: true}))
	if _, err := svc.ApplyStream(context.Background(), named("stale", 9090), "", false); err != nil {
		t.Fatalf("a port nginx holds for nothing configured: %v", err)
	}
	var inUse *PortInUseError
	if _, err := svc.ApplyStream(context.Background(), named("cache", 7000), "", false); !errors.As(err, &inUse) ||
		inUse.Name != "redis-server" || inUse.PID != 31 {
		t.Fatalf("got %v", err)
	}
	// Where nginx's descriptors cannot be read, a socket owned by nginx's
	// user is nginx's only where it is the stream's own; anything else there
	// is refused as a program nothing could name.
	withNginx(t, view(nil))
	if _, err := svc.ApplyStream(context.Background(), named("other", 7100), "", false); !errors.As(err, &inUse) ||
		inUse.Kind != OwnerProgram || inUse.Name != "" || !strings.Contains(inUse.Error(), "in use by another program") {
		t.Fatalf("got %v", err)
	}
}

// The preview asks the same question the save does, and an edit is not in
// its own way.
func TestStreamConflictForThePreview(t *testing.T) {
	svc, _ := watchedHost(t, "")
	rendered, _ := RenderStream(pgStream(6432))
	writeStream(t, svc, "replica", rendered)
	ctx := context.Background()
	if refused := svc.StreamConflict(ctx, pgStream(6432), "replica"); refused != nil {
		t.Fatalf("its own port: %v", refused)
	}
	other := pgStream(6432)
	other.Name = "copy"
	if refused := svc.StreamConflict(ctx, other, ""); refused == nil || refused.Kind != OwnerStream || refused.Name != "replica" || refused.Suggest != 6433 {
		t.Fatalf("another stream's port: %+v", refused)
	}
	if refused := svc.StreamConflict(ctx, pgStream(8080), ""); refused == nil || refused.Kind != OwnerSite {
		t.Fatalf("a site's port: %+v", refused)
	}
	if refused := svc.StreamConflict(ctx, pgStream(6500), ""); refused != nil {
		t.Fatalf("a free port: %+v", refused)
	}
}

// Every stream says what nginx does with it: read and holding its port,
// shadowed by a stream read first, clashing with a site, held by another
// program, refused a bind nginx logged, or not yet taken up.
func TestStreamStatesSayWhatNginxDoes(t *testing.T) {
	withListeners(t, func(context.Context) ([]Listener, error) {
		return []Listener{{Protocol: "tcp", Address: "0.0.0.0", Port: 7000, PID: 900, Process: "postgres"}}, nil
	})
	svc, root := watchedHost(t, "")
	files := map[string]string{
		"a-first":  "server { listen 5432; proxy_pass 10.0.0.5:5432; }\n",
		"b-second": "server { listen 5432; proxy_pass 10.0.0.6:5432; }\n",
		"c-site":   "server { listen 8080; proxy_pass 10.0.0.7:80; }\n",
		"d-held":   "server { listen 7000; proxy_pass 10.0.0.8:7000; }\n",
		"e-logged": "server { listen 7100; proxy_pass 10.0.0.8:7100; }\n",
		"f-stale":  "server { listen 7200; proxy_pass 10.0.0.8:7200; }\n",
		"g-dns":    "server { listen 127.0.0.1:5353 udp; proxy_pass 10.0.0.53:53; }\n",
		"h-broken": "server { listen 7300;\n",
	}
	old := time.Now().Add(-2 * time.Hour)
	for name, content := range files {
		path := writeStream(t, svc, name, content)
		if name != "f-stale" {
			os.Chtimes(path, old, old)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "error.log"),
		[]byte("2026/09/28 03:00:00 [emerg] 1#1: bind() to 0.0.0.0:7100 failed (98: Address in use)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withNginx(t, func(int32) *socketView {
		return &socketView{
			nginx: &nginxProcess{master: 7, workers: []int32{10}, loaded: time.Now().Add(-time.Hour)},
			sockets: []socket{
				{addr: "0.0.0.0", port: 5432, inode: 1},
				{addr: "0.0.0.0", port: 7000, inode: 2},
				{udp: true, addr: "0.0.0.0", port: 5353, inode: 3},
			},
			held:  map[uint64]bool{1: true, 3: true},
			local: true,
		}
	})
	status, err := svc.Streams(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]StreamEntry{}
	for _, entry := range status.Streams {
		got[entry.Name] = entry
	}
	expect := func(name, state, reason string) StreamEntry {
		t.Helper()
		entry := got[name]
		if entry.State != state || !strings.Contains(entry.StateReason, reason) {
			t.Errorf("%s: %s %q, want %s %q", name, entry.State, entry.StateReason, state, reason)
		}
		return entry
	}
	if entry := expect("a-first", StreamLive, ""); entry.StateReason != "" || entry.Blocker != nil {
		t.Errorf("a-first: %+v", entry)
	}
	if entry := expect("b-second", StreamShadowed, "Port 5432/tcp is taken first by the stream a-first"); entry.Blocker == nil || entry.Blocker.Name != "a-first" {
		t.Errorf("b-second blocker: %+v", entry.Blocker)
	}
	if entry := expect("c-site", StreamNotListening, "Port 8080/tcp is also where the site web.example.com listens"); entry.Blocker == nil || entry.Blocker.Site != "web" {
		t.Errorf("c-site blocker: %+v", entry.Blocker)
	}
	if entry := expect("d-held", StreamNotListening, "Port 7000/tcp is held by postgres (pid 900), so nginx cannot bind it, and every reload fails"); entry.Blocker == nil || entry.Blocker.PID != 900 {
		t.Errorf("d-held blocker: %+v", entry.Blocker)
	}
	if entry := expect("e-logged", StreamNotListening, "the last time it tried to bind one, it could not"); entry.BindError != "2026/09/28 03:00:00 bind() to 0.0.0.0:7100 failed (98: Address in use)" {
		t.Errorf("e-logged bind error: %q", entry.BindError)
	}
	expect("f-stale", StreamNotListening, "it last loaded its configuration before this file changed")
	// One address is served by nginx's wildcard for it.
	expect("g-dns", StreamLive, "")
	expect("h-broken", StreamNotRead, "a syntax error")
}

// A stream nginx does not read is said so, with why, and a running nginx
// that cannot be read leaves the rest unknown rather than guessed.
func TestStreamStatesWhenNginxDoesNotReadThem(t *testing.T) {
	cases := []struct {
		name, conf, state, reason string
	}{
		{"not included", "http {}\n", StreamNotRead, "nginx.conf does not include the stream directory"},
		{"inside http", "http { include $ROOT/stream.d/*.conf; }\n", StreamNotRead, "nginx reads the stream directory inside http, where a stream is refused"},
		{"no running nginx", "stream { include $ROOT/stream.d/*.conf; }\n", StreamUnknown, "Whether nginx listens for it could not be checked: no nginx runs for unit tests."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := watchedHost(t, tc.conf)
			writeStream(t, svc, "replica", "server { listen 6432; proxy_pass 10.0.0.5:5432; }\n")
			status, err := svc.Streams(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if entry := status.Streams[0]; entry.State != tc.state || !strings.Contains(entry.StateReason, tc.reason) {
				t.Fatalf("%s %q", entry.State, entry.StateReason)
			}
		})
	}
}
