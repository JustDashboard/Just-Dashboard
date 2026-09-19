package proxysvc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

func TestRenderDockerCaddyRouteWritesAnAccessLog(t *testing.T) {
	content, err := renderDockerCaddyRoute(DeploymentRoute{
		Name: "just-dashboard-env-7.conf", Domains: []string{"app.example.com"},
		TLS: true, AccessLog: true,
	}, "http://172.18.0.4:3000")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, `output file "/config/just-dashboard/access/just-dashboard-env-7.log"`) {
		t.Fatalf("no access log in the rendered route:\n%s", content)
	}
	if !strings.Contains(content, "format json") {
		t.Fatalf("the console format carries no request duration, and latency is the point:\n%s", content)
	}
	// Caddy rolls this itself: the ingress volume is on no logrotate schedule
	// the dashboard controls, and an access log is the fastest-growing file a
	// deployment produces.
	if !strings.Contains(content, "roll_size") || !strings.Contains(content, "roll_keep") {
		t.Fatalf("no rotation on the access log:\n%s", content)
	}
	// Rolled generations stay readable from an offset, which is how the tail
	// of a generation that rolled between two reads is recovered.
	if !strings.Contains(content, "roll_uncompressed") {
		t.Fatalf("rolled generations must stay uncompressed to be followed:\n%s", content)
	}
	if !strings.Contains(content, `reverse_proxy "http://172.18.0.4:3000"`) {
		t.Fatalf("the route stopped proxying:\n%s", content)
	}
}

func TestRenderDockerCaddyRouteWithoutAccessLog(t *testing.T) {
	content, err := renderDockerCaddyRoute(DeploymentRoute{
		Name: "just-dashboard-env-7.conf", Domains: []string{"app.example.com"},
	}, "http://172.18.0.4:3000")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "log {") {
		t.Fatalf("a route that did not ask for a record must not write one:\n%s", content)
	}
}

func TestAccessLogPathRefusesAnUnsafeName(t *testing.T) {
	// The path is handed to Caddy, which creates whatever it is pointed at, so
	// it goes through the same check as the route file rather than a looser one.
	for _, name := range []string{
		"../../etc/passwd",
		"other-tool.conf",
		"just-dashboard-env-1/../../x.conf",
		"just-dashboard\nenv.conf",
	} {
		if _, err := dockerCaddyAccessLogPath(name); err == nil {
			t.Fatalf("%q was accepted as an access log name", name)
		}
	}
}

func TestAccessLogPathMirrorsTheRouteName(t *testing.T) {
	got, err := dockerCaddyAccessLogPath("just-dashboard-env-12.conf")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/config/just-dashboard/access/just-dashboard-env-12.log" {
		t.Fatalf("path = %q", got)
	}
}

func TestNginxAccessLogPathMatchesWhatTheSiteAsksFor(t *testing.T) {
	// deploymentSiteSpec sets AccessLog, and sites_render writes
	// /var/log/nginx/<name>.access.log. The reader must look where the writer
	// writes, and these are the only two places either is spelled.
	spec := deploymentSiteSpec(DeploymentRoute{Name: "just-dashboard-env-3.conf", Domains: []string{"a.example.com"}, Upstream: "http://127.0.0.1:8080"}, "")
	if !spec.AccessLog {
		t.Fatal("nginx deployment routes have always recorded their requests")
	}
	rendered, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, nginxAccessLogPath("just-dashboard-env-3.conf")) {
		t.Fatalf("reader and writer disagree about the path:\n%s", rendered)
	}
}

func TestAccessLogReaderWithoutDockerIngressIsTheHostFile(t *testing.T) {
	s := New(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile"))
	reader, facts, err := s.AccessLogReader(context.Background(), "just-dashboard-env-3.conf")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reader.(*fileAccessLog); !ok {
		t.Fatalf("reader = %T, want the host file", reader)
	}
	if facts.Driver != accessDriverNginx || facts.Format != accesslog.FormatCombined || facts.Latency {
		t.Fatalf("facts = %+v", facts)
	}
	if _, _, err := s.AccessLogReader(context.Background(), "../etc/passwd"); err == nil {
		t.Fatal("an unsafe route name must not become a path under /var/log")
	}
}

func TestParseCaddyReadFramesTheTwoReports(t *testing.T) {
	stdout := []byte("file 4517209 25 1789850644\nline1\nline2\npartial")
	stderr := []byte("live 4517300 40 1789850650\n")
	read, live, data := parseCaddyRead(stdout, stderr)
	if !read.Exists || read.Identity != "4517209" || read.Size != 25 || read.Modified.Unix() != 1789850644 {
		t.Fatalf("read = %+v", read)
	}
	if !live.Exists || live.Identity != "4517300" || live.Size != 40 {
		t.Fatalf("live = %+v", live)
	}
	if string(data) != "line1\nline2\npartial" {
		t.Fatalf("data = %q", data)
	}

	// Absent generation, live file present.
	read, live, data = parseCaddyRead([]byte("absent\n"), []byte("live 1 2 3\n"))
	if read.Exists || !live.Exists || data != nil {
		t.Fatalf("absent: read %+v live %+v data %q", read, live, data)
	}
	// Nothing at all — the shell died before reporting — reads as absent.
	read, live, _ = parseCaddyRead(nil, []byte("live absent\n"))
	if read.Exists || live.Exists {
		t.Fatalf("empty: read %+v live %+v", read, live)
	}
	// A limit of zero prints the report and no data, and no trailing newline.
	read, _, data = parseCaddyRead([]byte("file 9 9 9\n"), nil)
	if !read.Exists || len(data) != 0 {
		t.Fatalf("report only: read %+v data %q", read, data)
	}
}

func TestFileAccessLogFollowsAppendsAndARename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site.access.log")
	reader := &fileAccessLog{path: path}
	ctx := context.Background()

	// Absent: nothing to read, and nothing wrong.
	read, live, next, err := reader.Read(ctx, "", 0, 1<<20, nil)
	if err != nil || read.Exists || live.Exists || next != 0 {
		t.Fatalf("absent: %+v %+v %d %v", read, live, next, err)
	}

	write := func(text string) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(text); err != nil {
			t.Fatal(err)
		}
	}
	write("one\ntwo\nthr")
	var lines []string
	read, live, next, err = reader.Read(ctx, "", 0, 1<<20, func(l string) { lines = append(lines, l) })
	if err != nil || !read.Exists || read.Identity == "" || read.Identity != live.Identity {
		t.Fatalf("first read: %+v %+v %v", read, live, err)
	}
	if strings.Join(lines, ",") != "one,two" || next != int64(len("one\ntwo\n")) {
		t.Fatalf("lines %v next %d: the half-written line stays for the next read", lines, next)
	}

	// Finish the line and add one; the read from `next` yields exactly those.
	write("ee\nfour\n")
	lines = nil
	_, _, next2, _ := reader.Read(ctx, read.Identity, next, 1<<20, func(l string) { lines = append(lines, l) })
	if strings.Join(lines, ",") != "three,four" {
		t.Fatalf("delta read: %v", lines)
	}

	// logrotate renames the file aside and nginx opens a new one. The old
	// generation is still readable by its identity, from where we left off.
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	write("tail-of-old\n") // written to the NEW live file, not the old one
	lines = nil
	oldRead, live2, _, err := reader.Read(ctx, read.Identity, next2, 1<<20, func(l string) { lines = append(lines, l) })
	if err != nil || !oldRead.Exists || oldRead.Identity != read.Identity {
		t.Fatalf("the rolled generation must be found by identity: %+v %v", oldRead, err)
	}
	if live2.Identity == read.Identity {
		t.Fatal("after the rename the live file is a new generation")
	}
	if len(lines) != 0 {
		t.Fatalf("nothing more was written to the old generation, got %v", lines)
	}
	rolled, _ := reader.Rolled(ctx)
	if len(rolled) != 1 || rolled[0].Identity != read.Identity {
		t.Fatalf("rolled = %+v", rolled)
	}
	lines = nil
	_, _, _, _ = reader.Read(ctx, "", 0, 1<<20, func(l string) { lines = append(lines, l) })
	if strings.Join(lines, ",") != "tail-of-old" {
		t.Fatalf("the new live file: %v", lines)
	}

	// A compressed generation is not something to read from an offset.
	if err := os.WriteFile(path+".2.gz", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rolled, _ = reader.Rolled(ctx)
	if len(rolled) != 1 {
		t.Fatalf("compressed generations must be skipped: %+v", rolled)
	}
}

func TestFileAccessLogReportsTruncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site.access.log")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader := &fileAccessLog{path: path}
	read, _, next, _ := reader.Read(context.Background(), "", 0, 1<<20, nil)
	if next != 14 {
		t.Fatalf("next = %d", next)
	}
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	after, _, next2, _ := reader.Read(context.Background(), read.Identity, next, 1<<20, nil)
	if !after.Exists || after.Identity != read.Identity || after.Size != 0 || next2 != next {
		t.Fatalf("after copytruncate: %+v next %d — same identity, shorter than the cursor, nothing read", after, next2)
	}
}

// The container-side script, against the real image. Gated like the other
// live checks: it needs Docker and pulls nothing, but a unit run should not
// depend on either.
func TestLiveCaddyAccessLogReader(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to exercise the container-side reader")
	}
	ctx := context.Background()
	raw, err := exec.CommandContext(ctx, "docker", "run", "-d", "--rm", "caddy:2-alpine", "sleep", "300").Output()
	if err != nil {
		t.Skipf("docker run: %v", err)
	}
	id := strings.TrimSpace(string(raw))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	in := func(script string) {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", "exec", id, "sh", "-c", script).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", script, err, out)
		}
	}
	dir := "/config/just-dashboard/access"
	in("mkdir -p " + dir + " && printf 'one\\ntwo\\nthr' > " + dir + "/just-dashboard-env-1.log")

	reader := &caddyAccessLog{edge: dockerCaddy{ID: id}, path: dir + "/just-dashboard-env-1.log"}
	var lines []string
	read, live, next, err := reader.Read(ctx, "", 0, 1<<20, func(l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatal(err)
	}
	if !read.Exists || read.Identity == "" || read.Identity != live.Identity || read.Size != 11 {
		t.Fatalf("read %+v live %+v", read, live)
	}
	if strings.Join(lines, ",") != "one,two" || next != 8 {
		t.Fatalf("lines %v next %d", lines, next)
	}

	// Roll: the old generation is renamed aside with the roller's naming, and
	// a new live file appears. The old one is found by inode, and reading it
	// from the cursor recovers what was written to it before the roll.
	in("printf 'ee\\n' >> " + dir + "/just-dashboard-env-1.log && mv " + dir + "/just-dashboard-env-1.log " + dir + "/just-dashboard-env-1-2026-09-19T10-00-00.000.log && printf 'new1\\n' > " + dir + "/just-dashboard-env-1.log")
	lines = nil
	old, live2, next2, err := reader.Read(ctx, read.Identity, next, 1<<20, func(l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatal(err)
	}
	if !old.Exists || old.Identity != read.Identity || strings.Join(lines, ",") != "three" || next2 != 14 {
		t.Fatalf("old generation: %+v lines %v next %d", old, lines, next2)
	}
	if !live2.Exists || live2.Identity == read.Identity {
		t.Fatalf("live after roll: %+v", live2)
	}
	rolled, err := reader.Rolled(ctx)
	if err != nil || len(rolled) != 1 || rolled[0].Identity != read.Identity {
		t.Fatalf("rolled = %+v %v", rolled, err)
	}
	lines = nil
	fresh, _, _, _ := reader.Read(ctx, "", 0, 1<<20, func(l string) { lines = append(lines, l) })
	if fresh.Identity != live2.Identity || strings.Join(lines, ",") != "new1" {
		t.Fatalf("new live: %+v %v", fresh, lines)
	}
	// A generation that no longer exists reads as absent, not as an error.
	gone, _, _, err := reader.Read(ctx, "999999999", 0, 1<<20, nil)
	if err != nil || gone.Exists {
		t.Fatalf("gone: %+v %v", gone, err)
	}
	// A limit of zero is a stat.
	stat, _, n, _ := reader.Read(ctx, "", 0, 0, func(string) { t.Fatal("limit zero must read nothing") })
	if !stat.Exists || n != 0 {
		t.Fatalf("stat-only read: %+v %d", stat, n)
	}
}
