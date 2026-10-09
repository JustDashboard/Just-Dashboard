package proxysvc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"
)

func withConnections(t *testing.T, conns []gnet.ConnectionStat) {
	t.Helper()
	previous := streamConnections
	streamConnections = func(context.Context) ([]gnet.ConnectionStat, error) { return conns, nil }
	t.Cleanup(func() { streamConnections = previous })
}

func useStreamLogDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	previous := streamLogDir
	streamLogDir = dir
	t.Cleanup(func() { streamLogDir = previous })
	return dir
}

func established(local string, localPort uint32, remote string, remotePort uint32) gnet.ConnectionStat {
	return gnet.ConnectionStat{Status: "ESTABLISHED", Laddr: gnet.Addr{IP: local, Port: localPort}, Raddr: gnet.Addr{IP: remote, Port: remotePort}}
}

// A stream as the network page joins it: what it forwards and who it admits,
// the client sessions and backend legs nginx holds now, and its last hour by
// the backend each session ended on.
func TestStreamPathJoinsConfigurationSocketsAndLog(t *testing.T) {
	svc, _ := streamHost(t)
	logs := useStreamLogDir(t)
	spec := &StreamSpec{Name: "pg", Listen: 6432, Protocol: "tcp", Balance: "least-conn", LogConnections: true,
		Upstream: "10.0.0.5:5432", Servers: []StreamServer{{Address: "10.0.0.5:5432"}, {Address: "10.0.0.6:5432", Backup: true}},
		Rules: []StreamRule{{Action: "deny", Source: "10.9.0.0/16"}, {Action: "allow", Source: "10.0.0.0/8"}}, DefaultAllow: new(bool)}
	content, err := RenderStream(spec)
	if err != nil {
		t.Fatal(err)
	}
	writeStream(t, svc, "pg", content)
	withConnections(t, []gnet.ConnectionStat{
		established("0.0.0.0", 6432, "10.1.2.3", 51000),
		established("10.0.0.1", 6432, "10.1.2.4", 51001),
		established("10.0.0.1", 40001, "10.0.0.5", 5432),
		established("10.0.0.1", 40002, "10.0.0.5", 5432),
		established("10.0.0.1", 40003, "10.0.0.9", 5432),
		{Status: "TIME_WAIT", Laddr: gnet.Addr{IP: "10.0.0.1", Port: 40004}, Raddr: gnet.Addr{IP: "10.0.0.6", Port: 5432}},
	})
	now := time.Now()
	at := func(ago time.Duration) string { return fmt.Sprintf("%.3f", float64(now.Add(-ago).UnixNano())/1e9) }
	lines := []string{
		at(2*time.Hour) + ` 10.1.2.3 TCP 200 10 10 1.000 "10.0.0.5:5432" 6432`,
		at(30*time.Minute) + ` 10.1.2.3 TCP 200 10 10 1.000 "10.0.0.5:5432" 6432`,
		at(20*time.Minute) + ` 10.1.2.4 TCP 502 0 0 0.001 "10.0.0.5:5432, 10.0.0.6:5432" 6432`,
		at(10*time.Minute) + ` 10.9.1.1 TCP 403 0 0 0.000 "-" 6432`,
	}
	if err := os.WriteFile(filepath.Join(logs, "stream-pg.log"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, err := svc.StreamPath(context.Background(), "pg", now)
	if err != nil {
		t.Fatal(err)
	}
	if path.Protocol != "tcp" || fmt.Sprint(path.Listens) != "[port 6432/tcp]" || path.Balance != "least-conn" ||
		path.Access != "deny 10.9.0.0/16, allow 10.0.0.0/8, then deny everyone else" {
		t.Fatalf("configuration = %+v", path)
	}
	if !path.SocketsRead || path.Clients != 2 || path.Servers[0].Connections != 2 || path.Servers[1].Connections != 0 {
		t.Fatalf("sockets = %+v", path)
	}
	if path.LogSessions != 3 || path.LogFailed != 1 || path.LogDenied != 1 || !path.LogComplete {
		t.Fatalf("log = %+v", path)
	}
	primary, backup := path.Servers[0], path.Servers[1]
	if primary.Sessions != 1 || primary.Failed != 0 || backup.Sessions != 1 || backup.Failed != 1 || !backup.Backup {
		t.Fatalf("servers = %+v", path.Servers)
	}
}

// A UDP stream has no per-session socket to count, and says so; a paused one
// holds none.
func TestStreamPathSaysWhatItCannotCount(t *testing.T) {
	svc, _ := streamHost(t)
	useStreamLogDir(t)
	withConnections(t, nil)
	content, err := RenderStream(&StreamSpec{Name: "dns", Listen: 5353, Protocol: "udp", Upstream: "10.0.0.53:53"})
	if err != nil {
		t.Fatal(err)
	}
	writeStream(t, svc, "dns", content)
	path, err := svc.StreamPath(context.Background(), "dns", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if path.SocketsRead || !strings.Contains(path.Note, "UDP stream has no connection per session") || path.Access != "open to every address that reaches the port" {
		t.Fatalf("path = %+v", path)
	}
}

// The session a measured connection left is found by its client and time,
// and none is an answer of its own rather than an error.
func TestAwaitStreamSessionFindsTheMeasuredConnection(t *testing.T) {
	svc, _ := streamHost(t)
	logs := useStreamLogDir(t)
	content, err := RenderStream(&StreamSpec{Name: "pg", Listen: 6432, Upstream: "10.0.0.5:5432", LogConnections: true})
	if err != nil {
		t.Fatal(err)
	}
	writeStream(t, svc, "pg", content)
	sent := time.Now()
	line := fmt.Sprintf(`%.3f 127.0.0.1 TCP 200 0 0 0.002 "10.0.0.5:5432" 6432`+"\n", float64(sent.Add(50*time.Millisecond).UnixNano())/1e9)
	before := fmt.Sprintf(`%.3f 127.0.0.1 TCP 502 0 0 0.002 "10.0.0.5:5432" 6432`+"\n", float64(sent.Add(-time.Minute).UnixNano())/1e9)
	if err := os.WriteFile(filepath.Join(logs, "stream-pg.log"), []byte(before+line), 0o644); err != nil {
		t.Fatal(err)
	}
	session, err := svc.AwaitStreamSession(context.Background(), "pg", "127.0.0.1", sent, 200*time.Millisecond)
	if err != nil || session == nil || session.Status != 200 || session.Upstream != "10.0.0.5:5432" {
		t.Fatalf("session = %+v, %v", session, err)
	}
	session, err = svc.AwaitStreamSession(context.Background(), "pg", "10.1.1.1", sent, 200*time.Millisecond)
	if err != nil || session != nil {
		t.Fatalf("another client's session = %+v, %v", session, err)
	}
	writeStream(t, svc, "quiet", "server { listen 7000; proxy_pass 10.0.0.7:7000; }\n")
	if _, err := svc.AwaitStreamSession(context.Background(), "quiet", "127.0.0.1", sent, 0); err == nil || !strings.Contains(err.Error(), "does not log") {
		t.Fatalf("a stream without a log: %v", err)
	}
}
