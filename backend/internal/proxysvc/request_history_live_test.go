package proxysvc

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/requesthistory"
)

// The request history's recorder against the real container-side script: a
// Caddy ingress's record read into the history, followed across a roll by
// inode, and continued after a restart from the kept cursor, every request
// once. The fake disk of requesthistory's own tests cannot say whether the
// script's reports and the recorder agree; this can.
func TestLiveRequestHistoryFollowsTheCaddyIngress(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to record from a real Caddy ingress's files")
	}
	ctx := context.Background()
	raw, err := exec.CommandContext(ctx, "docker", "run", "-d", "--rm", "caddy:2-alpine", "sleep", "300").Output()
	if err != nil {
		t.Skipf("docker run: %v", err)
	}
	id := strings.TrimSpace(string(raw))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	dir := "/config/just-dashboard/access"
	// An id far past any this host's deployments have, so nothing real is
	// named by it.
	const route = "just-dashboard-env-900000001.conf"
	path := dir + "/just-dashboard-env-900000001.log"
	at := time.Now().UTC().Add(-time.Hour)
	n := 0
	write := func(redirect string, count int) {
		t.Helper()
		var lines strings.Builder
		for i := 0; i < count; i++ {
			n++
			fmt.Fprintf(&lines, `{"ts":%q,"msg":"handled request","request":{"method":"GET","host":"h","uri":"/%d","client_ip":"203.0.113.1"},"status":200,"size":1,"duration":0.001}`+"\n",
				at.Add(time.Duration(n)*time.Second).Format(time.RFC3339Nano), n)
		}
		script := "mkdir -p " + dir + " && cat " + redirect + " " + path
		command := exec.CommandContext(ctx, "docker", "exec", "-i", id, "sh", "-c", script)
		command.Stdin = strings.NewReader(lines.String())
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", script, err, out)
		}
	}
	in := func(script string) {
		t.Helper()
		if out, err := exec.CommandContext(ctx, "docker", "exec", id, "sh", "-c", script).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", script, err, out)
		}
	}

	data := t.TempDir()
	archive, err := requesthistory.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	recorder := func(a *requesthistory.Archive) *requesthistory.Recorder {
		return requesthistory.NewRecorder(a, func(context.Context, string) (accesslog.Reader, accesslog.Facts, error) {
			return &caddyAccessLog{edge: dockerCaddy{ID: id}, path: path}, accesslog.Facts{}, nil
		}, func(context.Context) ([]string, error) { return []string{route}, nil }, nil)
	}
	tick := func(r *requesthistory.Recorder) {
		t.Helper()
		if err := r.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	kept := func(a *requesthistory.Archive) []string {
		t.Helper()
		var paths []string
		if err := a.Scan(ctx, route, 0, math.MaxInt64, func(e accesslog.Entry) error {
			paths = append(paths, e.Path)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return paths
	}
	want := func(a *requesthistory.Archive, count int) {
		t.Helper()
		got := kept(a)
		if len(got) != count {
			t.Fatalf("the history holds %d requests, want %d: %v", len(got), count, got)
		}
		for i, p := range got {
			if p != fmt.Sprintf("/%d", i+1) {
				t.Fatalf("request %d is %s, want /%d — out of order, missing or twice: %v", i, p, i+1, got)
			}
		}
	}

	write(">", 3)
	first := recorder(archive)
	tick(first)
	want(archive, 3)

	// Written, then rolled aside with the roller's naming and a new live
	// file begun: the old generation's tail is read by inode, then the new
	// file from its start.
	write(">>", 2)
	in("mv " + path + " " + dir + "/just-dashboard-env-900000001-2026-11-27T10-00-00.000.log")
	write(">", 2)
	tick(first)
	tick(first)
	want(archive, 7)

	// A restart: a new recorder on the reopened history continues from the
	// cursor it kept.
	archive.Close()
	archive, err = requesthistory.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	write(">>", 3)
	second := recorder(archive)
	tick(second)
	want(archive, 10)
	if span, err := archive.Span(ctx, route); err != nil || !span.LostAt.IsZero() || !span.Recording {
		t.Fatalf("span = %+v (%v), want nothing lost and the route recording", span, err)
	}
}
