package proxysvc

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestParseProcStat(t *testing.T) {
	for _, tc := range []struct {
		stat, comm string
		ppid       int
		ticks      uint64
		ok         bool
	}{
		{"4242 (nginx) S 4200 4242 4242 0 -1 4194624 12 0 0 0 0 0 0 0 20 0 1 0 678998210 11231232 900 18446744073709551615\n", "nginx", 4200, 678998210, true},
		// A command may hold spaces and parentheses of its own; the fields
		// start after the last ")".
		{"7 (a (b) c) R 1 7 7 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 55 0 0\n", "a (b) c", 1, 55, true},
		{"7 (nginx) S 1 7", "", 0, 0, false},
		{"garbage", "", 0, 0, false},
	} {
		comm, ppid, ticks, ok := parseProcStat(tc.stat)
		if comm != tc.comm || ppid != tc.ppid || ticks != tc.ticks || ok != tc.ok {
			t.Errorf("parseProcStat(%q) = %q %d %d %v", tc.stat, comm, ppid, ticks, ok)
		}
	}
}

// The titles are what ps shows for nginx on this host: Ubuntu's own, an
// official image in a container, and the live harness's.
func TestMasterArgs(t *testing.T) {
	for _, tc := range []struct {
		title, conf, prefix string
		ok                  bool
	}{
		{"nginx: master process /usr/sbin/nginx -g daemon on; master_process on;", "", "", true},
		{"nginx: master process nginx -g daemon off;", "", "", true},
		{"nginx: master process nginx -c /srv/slot/nginx/nginx.conf -g daemon off;", "/srv/slot/nginx/nginx.conf", "", true},
		{"nginx: master process /usr/sbin/nginx -e /x/startup.log -p /x -c conf/nginx.conf", "conf/nginx.conf", "/x", true},
		{"nginx: master process /usr/sbin/nginx -c/etc/alt.conf -p/opt/nginx", "/etc/alt.conf", "/opt/nginx", true},
		{"nginx: worker process", "", "", false},
		{"nginx: worker process is shutting down", "", "", false},
	} {
		conf, prefix, ok := masterArgs(tc.title)
		if conf != tc.conf || prefix != tc.prefix || ok != tc.ok {
			t.Errorf("masterArgs(%q) = %q %q %v", tc.title, conf, prefix, ok)
		}
	}
}

func TestParseNginxBuild(t *testing.T) {
	ubuntu := "nginx version: nginx/1.26.3 (Ubuntu)\nbuilt with OpenSSL 3.4.1 11 Feb 2025\nTLS SNI support enabled\n" +
		"configure arguments: --with-cc-opt='-g -O2 -Werror=implicit-function-declaration' --prefix=/usr/share/nginx " +
		"--conf-path=/etc/nginx/nginx.conf --http-log-path=/var/log/nginx/access.log --pid-path=/run/nginx.pid --with-debug\n"
	if got, want := parseNginxBuild(ubuntu), (nginxBuild{"/usr/share/nginx", "/etc/nginx/nginx.conf", "/run/nginx.pid"}); got != want {
		t.Errorf("ubuntu: %+v, want %+v", got, want)
	}
	// Built with no paths of its own: nginx's defaults under its prefix.
	if got, want := parseNginxBuild("configure arguments: --prefix=/opt/n --with-http_ssl_module"), (nginxBuild{"/opt/n", "/opt/n/conf/nginx.conf", "/opt/n/logs/nginx.pid"}); got != want {
		t.Errorf("bare: %+v, want %+v", got, want)
	}
	if got := parseNginxBuild(""); got.conf != "/usr/local/nginx/conf/nginx.conf" {
		t.Errorf("no output: %+v", got)
	}
}

// runningNginx is a process table holding the host's own nginx and one in a
// container, neither of which reads the test's configuration, and the one
// that does.
type runningNginx struct {
	mu    sync.Mutex
	conf  string
	start time.Time
	ticks uint64
	reads int
	// reload, when set, is the load the workers move to on the read after
	// reads reach it.
	reload *fakeLoad
}

type fakeLoad struct {
	after int
	start time.Time
	ticks uint64
}

func (f *runningNginx) load(start time.Time, ticks uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.start, f.ticks = start, ticks
}

func (f *runningNginx) processes() ([]nginxProcess, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.reload != nil && f.reads > f.reload.after {
		f.start, f.ticks, f.reload = f.reload.start, f.reload.ticks, nil
	}
	old := f.start.Add(-time.Hour)
	return []nginxProcess{
		{PID: 1088, PPID: 1, Title: "nginx: master process /usr/sbin/nginx -g daemon on; master_process on;", Ticks: 10, Start: old},
		{PID: 1090, PPID: 1088, Title: "nginx: worker process", Ticks: 20, Start: old},
		{PID: 2000, PPID: 1999, Title: "nginx: master process nginx -g daemon off;", Ticks: 30, Start: old},
		{PID: 2001, PPID: 2000, Title: "nginx: worker process", Ticks: 40, Start: old},
		{PID: 100, PPID: 99, Title: "nginx: master process /usr/sbin/nginx -c " + f.conf, Ticks: 5, Start: old},
		// A worker from the load before, still finishing a connection.
		{PID: 101, PPID: 100, Title: "nginx: worker process is shutting down", Ticks: f.ticks - 50, Start: f.start.Add(-time.Minute)},
		{PID: 102, PPID: 100, Title: "nginx: worker process", Ticks: f.ticks, Start: f.start},
		// Started later on its own, to replace a worker that died.
		{PID: 103, PPID: 100, Title: "nginx: worker process", Ticks: f.ticks + 90, Start: f.start.Add(900 * time.Millisecond)},
	}, nil
}

// pendingTree is a Debian nginx directory behind an nginx that dumps its
// configuration the way `nginx -T` does — nginx.conf, conf.d/*.conf and
// sites-enabled/* — refuses it while a file called "broken" is there, and
// passes every test.
func pendingTree(t *testing.T) (*Service, string, *runningNginx) {
	t.Helper()
	svc, root := debianTree(t)
	nginxShim(t, fmt.Sprintf(`ROOT='%s'
case "$*" in
*-T*)
	if [ -e "$ROOT/broken" ]; then
		echo "nginx: [emerg] unknown directive \"foo\" in $ROOT/sites-enabled/app:3" >&2
		echo "nginx: configuration file $ROOT/nginx.conf test failed" >&2
		exit 1
	fi
	for f in "$ROOT/nginx.conf" "$ROOT"/conf.d/*.conf "$ROOT"/sites-enabled/*; do
		[ -e "$f" ] || continue
		echo "# configuration file $f:"; cat "$f"; echo
	done ;;
*-V*) echo "configure arguments: --prefix=/usr/share/nginx --conf-path=/etc/nginx/nginx.conf --pid-path=/run/nginx.pid" >&2 ;;
esac
exit 0`, root))
	writeFile(t, filepath.Join(root, "nginx.conf"), "events {}\nhttp {\n    include conf.d/*.conf;\n    include sites-enabled/*;\n}\n")
	fake := &runningNginx{conf: filepath.Join(root, "nginx.conf")}
	svc.pending.processes = fake.processes
	return svc, root, fake
}

// pendingOf answers Pending as path → change, with the site each names.
func pendingOf(t *testing.T, svc *Service, after string) (*Pending, map[string]string) {
	t.Helper()
	svc.forgetEffective()
	p, err := svc.Pending(context.Background(), after)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range p.Files {
		got[f.Path] = f.Change + " " + f.Layout + "/" + f.Site
		if (f.Change == pendingRemoved) != (f.Modified == nil) {
			t.Errorf("%s %s with modified %v", f.Path, f.Change, f.Modified)
		}
	}
	return p, got
}

func TestPendingNamesWhatTheRunningNginxHasNotLoaded(t *testing.T) {
	svc, root, fake := pendingTree(t)
	available := func(name string) string { return filepath.Join(root, "sites-available", name) }
	enabled := func(name string) string { return filepath.Join(root, "sites-enabled", name) }
	app := "server {\n    server_name app.test;\n    return 200;\n}\n"
	writeFile(t, available("app"), app)
	symlink(t, available("app"), enabled("app"))
	writeFile(t, available("api"), "server {\n    server_name api.test;\n}\n")
	symlink(t, "../sites-available/api", enabled("api"))
	writeFile(t, filepath.Join(root, "conf.d", "extra.conf"), "server {\n    server_name extra.test;\n}\n")
	time.Sleep(20 * time.Millisecond)
	fake.load(time.Now(), 1000)

	p, got := pendingOf(t, svc, "")
	if !p.Running || p.Generation != "100-1000" || p.LastReload == nil || !p.LastReload.Equal(fake.start) || len(got) != 0 {
		t.Fatalf("right after the load: %+v %v", p, got)
	}

	// A save nginx has not read. The shutting-down worker and the one that
	// replaced a dead worker do not move the load.
	writeFile(t, available("app"), app+"# edited\n")
	if _, got := pendingOf(t, svc, ""); !reflect.DeepEqual(got, map[string]string{
		enabled("app"): "changed sites-available/app",
	}) {
		t.Errorf("after an edit: %v", got)
	}
	// Written back as it was: what nginx loaded, though the file is newer.
	writeFile(t, available("app"), app)
	if _, got := pendingOf(t, svc, ""); len(got) != 0 {
		t.Errorf("after the edit was undone: %v", got)
	}
	// A dry run puts its candidate at the live path and the original back.
	if res, err := svc.Validate(context.Background(), KindNginx, available("app"), "server {}\n"); err != nil || !res.Valid {
		t.Fatalf("validate: %v %+v", err, res)
	}
	if _, got := pendingOf(t, svc, ""); len(got) != 0 {
		t.Errorf("after a dry run: %v", got)
	}

	// Taken out, the site is still served until nginx reloads; switched
	// back on, it is exactly what nginx loaded.
	if _, err := svc.ToggleVHost(context.Background(), "api", false, false); err != nil {
		t.Fatal(err)
	}
	if _, got := pendingOf(t, svc, ""); !reflect.DeepEqual(got, map[string]string{
		enabled("api"): "removed sites-available/api",
	}) {
		t.Errorf("after a disable: %v", got)
	}
	if _, err := svc.ToggleVHost(context.Background(), "api", true, false); err != nil {
		t.Fatal(err)
	}
	if _, got := pendingOf(t, svc, ""); len(got) != 0 {
		t.Errorf("after the disable was switched back: %v", got)
	}

	// A new site, and a file copied in with its old modification time kept.
	writeFile(t, available("new"), "server {\n    server_name new.test;\n}\n")
	if _, err := svc.ToggleVHost(context.Background(), "new", true, false); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root, "conf.d", "moved.conf")
	writeFile(t, moved, "server {\n    server_name moved.test;\n}\n")
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(moved, old, old); err != nil {
		t.Fatal(err)
	}
	p, got = pendingOf(t, svc, "")
	if !reflect.DeepEqual(got, map[string]string{
		enabled("new"): "added sites-available/new",
		moved:          "changed conf.d/moved.conf",
	}) {
		t.Errorf("after an enable and a copy: %v", got)
	}
	for _, f := range p.Files {
		if f.Modified.Before(fake.start) {
			t.Errorf("%s modified %v, before the load at %v", f.Path, f.Modified, fake.start)
		}
	}

	// nginx loads it all: nothing is pending, whatever the files' times.
	fake.load(time.Now().Add(10*time.Millisecond), 2000)
	if p, got := pendingOf(t, svc, ""); p.Generation != "100-2000" || len(got) != 0 {
		t.Errorf("after a reload: %s %v", p.Generation, got)
	}
}

// The configuration on disk may be one nginx refuses, which is when a change
// matters most: the reload is refused, nginx keeps serving what it had, and
// only nginx's reason says what to fix. The files it would read are then
// unknown; those it is known to have loaded still say how they changed.
func TestPendingSaysWhenNginxRefusesTheConfigurationOnDisk(t *testing.T) {
	svc, root, fake := pendingTree(t)
	available := filepath.Join(root, "sites-available", "app")
	writeFile(t, available, "server {\n    return 200;\n}\n")
	symlink(t, available, filepath.Join(root, "sites-enabled", "app"))
	writeFile(t, filepath.Join(root, "conf.d", "extra.conf"), "server {\n    server_name extra.test;\n}\n")
	time.Sleep(20 * time.Millisecond)
	fake.load(time.Now(), 1000)
	if _, got := pendingOf(t, svc, ""); len(got) != 0 {
		t.Fatalf("right after the load: %v", got)
	}

	writeFile(t, available, "server {\n    return 200;\n    foo;\n}\n")
	writeFile(t, filepath.Join(root, "broken"), "")
	if err := os.Remove(filepath.Join(root, "conf.d", "extra.conf")); err != nil {
		t.Fatal(err)
	}
	p, got := pendingOf(t, svc, "")
	if want := `unknown directive "foo" in ` + available + ":3"; p.Problem != want {
		t.Errorf("problem = %q, want %q", p.Problem, want)
	}
	if !reflect.DeepEqual(got, map[string]string{
		filepath.Join(root, "sites-enabled", "app"): "changed sites-available/app",
		filepath.Join(root, "conf.d", "extra.conf"): "removed conf.d/extra.conf",
	}) {
		t.Errorf("files: %v", got)
	}
}

// A certificate order can hold the service lock for minutes, and `nginx -T`
// waits behind it. The files known to be loaded are judged meanwhile; with
// none known of the running load, the answer is an error rather than a
// guess that nothing is pending.
func TestPendingJudgesWhatItKnowsWhileTheDumpWaitsForTheLock(t *testing.T) {
	svc, root, fake := pendingTree(t)
	available := filepath.Join(root, "sites-available", "app")
	writeFile(t, available, "server {\n    return 200;\n}\n")
	symlink(t, available, filepath.Join(root, "sites-enabled", "app"))
	time.Sleep(20 * time.Millisecond)
	fake.load(time.Now(), 1000)
	if _, got := pendingOf(t, svc, ""); len(got) != 0 {
		t.Fatalf("right after the load: %v", got)
	}

	svc.pending.dumpWait = 200 * time.Millisecond
	svc.mu.Lock()
	locked := true
	defer func() {
		if locked {
			svc.mu.Unlock()
		}
	}()
	writeFile(t, available, "server {\n    return 204;\n}\n")
	p, got := pendingOf(t, svc, "")
	if p.Problem != "" || !reflect.DeepEqual(got, map[string]string{
		filepath.Join(root, "sites-enabled", "app"): "changed sites-available/app",
	}) {
		t.Errorf("behind the lock: %+v %v", p, got)
	}

	fake.load(time.Now().Add(10*time.Millisecond), 2000)
	svc.forgetEffective()
	if p, err := svc.Pending(context.Background(), ""); err == nil {
		t.Errorf("a load nothing is known of was judged: %+v", p)
	}
	svc.mu.Unlock()
	locked = false
	if p, got := pendingOf(t, svc, ""); p.Generation != "100-2000" || len(got) != 0 {
		t.Errorf("once the lock was free: %+v %v", p, got)
	}
}

// `nginx -s reload` returns once the signal is sent. Asked after a reload,
// the answer waits for nginx to have loaded a newer configuration — and says
// the old one is still running when nginx never does.
func TestPendingWaitsForTheLoadAReloadAskedFor(t *testing.T) {
	svc, _, fake := pendingTree(t)
	fake.load(time.Now(), 1000)
	p, _ := pendingOf(t, svc, "")
	fake.mu.Lock()
	fake.reload = &fakeLoad{after: fake.reads + 3, start: time.Now().Add(time.Second), ticks: 1100}
	fake.mu.Unlock()
	began := time.Now()
	if next, _ := pendingOf(t, svc, p.Generation); next.Generation != "100-1100" {
		t.Errorf("waited for %s", next.Generation)
	}
	if waited := time.Since(began); waited < 200*time.Millisecond || waited > 3*time.Second {
		t.Errorf("waited %v for a load three reads away", waited)
	}

	svc.pending.settle = 400 * time.Millisecond
	began = time.Now()
	if next, _ := pendingOf(t, svc, "100-1100"); next.Generation != "100-1100" || !next.Running {
		t.Errorf("a reload nginx did not take: %+v", next)
	}
	if waited := time.Since(began); waited < 400*time.Millisecond {
		t.Errorf("gave up after %v", waited)
	}
	// Another master read the configuration afresh when it started.
	if next, _ := pendingOf(t, svc, "99-5000"); next.Generation != "100-1100" {
		t.Errorf("another master: %+v", next)
	}
}

// Several nginx on one host: the host's own, one in a container, each read
// by its own configuration. Only one that reads this configuration is it, and
// two that read the same path are told apart by the pid file.
func TestPendingFindsTheNginxThatReadsThisConfiguration(t *testing.T) {
	svc, root, fake := pendingTree(t)
	fake.load(time.Now(), 1000)
	if p, _ := pendingOf(t, svc, ""); !p.Running || p.Generation != "100-1000" {
		t.Fatalf("%+v", p)
	}

	fake.conf = filepath.Join(root, "other.conf")
	p, _ := pendingOf(t, svc, "")
	if p.Running || p.Reason != "no running nginx reads "+filepath.Join(root, "nginx.conf") || p.Files == nil {
		t.Errorf("with no nginx reading it: %+v", p)
	}

	// Two masters for this configuration, one of them the pid file's.
	conf := filepath.Join(root, "nginx.conf")
	svc.pending.processes = func() ([]nginxProcess, error) {
		start := time.Now()
		return []nginxProcess{
			{PID: 300, PPID: 1, Title: "nginx: master process nginx -c " + conf, Ticks: 1, Start: start},
			{PID: 301, PPID: 300, Title: "nginx: worker process", Ticks: 7, Start: start},
			{PID: 400, PPID: 1, Title: "nginx: master process nginx -c " + conf, Ticks: 1, Start: start},
			{PID: 401, PPID: 400, Title: "nginx: worker process", Ticks: 9, Start: start},
		}, nil
	}
	p, _ = pendingOf(t, svc, "")
	if p.Running || !strings.Contains(p.Reason, "2 nginx master processes read") {
		t.Errorf("two masters and no pid file: %+v", p)
	}
	writeFile(t, conf, "pid "+filepath.Join(root, "nginx.pid")+";\nevents {}\n")
	writeFile(t, filepath.Join(root, "nginx.pid"), "400\n")
	if p, _ := pendingOf(t, svc, ""); !p.Running || p.Generation != "400-9" {
		t.Errorf("the pid file's master: %+v", p)
	}

	// A master whose workers have all gone is running, with no load to
	// compare with.
	svc.pending.processes = func() ([]nginxProcess, error) {
		return []nginxProcess{{PID: 300, PPID: 1, Title: "nginx: master process nginx -c " + conf, Ticks: 1}}, nil
	}
	if p, _ := pendingOf(t, svc, ""); !p.Running || p.LastReload != nil || p.Reason == "" {
		t.Errorf("no workers: %+v", p)
	}
}

func TestPendingWithoutNginx(t *testing.T) {
	svc, _ := debianTree(t)
	t.Setenv("PATH", t.TempDir())
	p, err := svc.Pending(context.Background(), "")
	if err != nil || p.Running || p.Reason != "nginx is not installed on this host" || p.Files == nil {
		t.Errorf("%+v %v", p, err)
	}
}

func TestParseGeneration(t *testing.T) {
	if master, ticks, err := ParseGeneration("100-2000"); err != nil || master != 100 || ticks != 2000 {
		t.Errorf("%d %d %v", master, ticks, err)
	}
	for _, bad := range []string{"", "100", "-5", "100-", "a-b", "0-5", "5-0", "1-2-3"} {
		if _, _, err := ParseGeneration(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

// A process's start, read from /proc, lands on the wall clock within the
// kernel's tick of when it really started.
func TestProcNginxProcessesPlacesAStartOnTheWallClock(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep binary")
	}
	b, err := os.ReadFile(sleep)
	if err != nil {
		t.Fatal(err)
	}
	// Named nginx, which is what the reader looks for.
	binary := filepath.Join(t.TempDir(), "nginx")
	if err := os.WriteFile(binary, b, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "30")
	before := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	after := time.Now()
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	procs, err := procNginxProcesses("/proc")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range procs {
		if p.PID != cmd.Process.Pid {
			continue
		}
		if p.PPID != os.Getpid() || p.Title != binary+" 30" {
			t.Errorf("read as %+v", p)
		}
		if p.Start.Before(before) || p.Start.After(after.Add(20*time.Millisecond)) {
			t.Errorf("started %v, between %v and %v", p.Start, before, after)
		}
		return
	}
	t.Fatalf("pid %d not among %+v", cmd.Process.Pid, procs)
}

// Against the real nginx, run in a private prefix: an edit nginx has not read
// is pending and served as it was, a reload puts it live, a dry run leaves
// nothing behind, and a site nginx cannot start — its port taken, which
// `nginx -t` does not check — stays pending after a reload that "succeeded",
// because nginx kept the workers it had.
func TestLivePendingFollowsTheRunningNginx(t *testing.T) {
	root := liveNginx(t)
	svc := New(root, filepath.Join(root, "Caddyfile"))
	svc.pending.settle = 2 * time.Second
	ctx := context.Background()
	port := freePort(t)
	available := filepath.Join(root, "sites-available", "app")
	site := func(body string) string {
		return fmt.Sprintf("server {\n    listen 127.0.0.1:%d;\n    return 200 %q;\n}\n", port, body)
	}
	writeFile(t, available, site("one"))
	symlink(t, available, filepath.Join(root, "sites-enabled", "app"))
	time.Sleep(20 * time.Millisecond)

	daemon := exec.Command(filepath.Join(root, "bin", "nginx"), "-g", "daemon off;")
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		daemon.Process.Signal(syscall.SIGQUIT)
		daemon.Wait()
	})
	var p *Pending
	for deadline := time.Now().Add(10 * time.Second); ; {
		p, _ = pendingOf(t, svc, "")
		if p.Running && p.Generation != "" && serves(port) == "one" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("nginx did not come up: %+v", p)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(p.Files) != 0 {
		t.Fatalf("pending right after the start: %+v", p.Files)
	}

	writeFile(t, available, site("two"))
	_, got := pendingOf(t, svc, "")
	if !reflect.DeepEqual(got, map[string]string{filepath.Join(root, "sites-enabled", "app"): "changed sites-available/app"}) {
		t.Errorf("after an edit: %v", got)
	}
	if body := serves(port); body != "one" {
		t.Errorf("serving %q before the reload", body)
	}
	if _, err := svc.Reload(ctx, KindNginx); err != nil {
		t.Fatal(err)
	}
	next, got := pendingOf(t, svc, p.Generation)
	if next.Generation == p.Generation || len(got) != 0 || !next.LastReload.After(*p.LastReload) {
		t.Errorf("after the reload: %+v %v", next, got)
	}
	if body := serves(port); body != "two" {
		t.Errorf("serving %q after the reload", body)
	}
	if res, err := svc.Validate(ctx, KindNginx, available, site("three")); err != nil || !res.Valid {
		t.Fatalf("validate: %v %+v", err, res)
	}
	if _, got := pendingOf(t, svc, ""); len(got) != 0 {
		t.Errorf("after a dry run: %v", got)
	}

	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	writeFile(t, filepath.Join(root, "sites-available", "clash"),
		fmt.Sprintf("server {\n    listen %s;\n    return 204;\n}\n", taken.Addr()))
	reload, err := svc.ToggleVHost(ctx, "clash", true, true)
	if err != nil || reload.Err != nil {
		t.Fatalf("enable: %v %+v", err, reload)
	}
	began := time.Now()
	stuck, got := pendingOf(t, svc, next.Generation)
	if stuck.Generation != next.Generation {
		t.Errorf("nginx loaded a configuration it cannot listen for: %+v", stuck)
	}
	if !reflect.DeepEqual(got, map[string]string{filepath.Join(root, "sites-enabled", "clash"): "added sites-available/clash"}) {
		t.Errorf("after a reload nginx refused: %v", got)
	}
	if waited := time.Since(began); waited < 2*time.Second {
		t.Errorf("gave up on the reload after %v", waited)
	}
	taken.Close()
	if _, err := svc.Reload(ctx, KindNginx); err != nil {
		t.Fatal(err)
	}
	if last, got := pendingOf(t, svc, stuck.Generation); last.Generation == stuck.Generation || len(got) != 0 {
		t.Errorf("once the port was free: %+v %v", last, got)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// serves is what the site on port answers, or empty when nothing does.
func serves(port int) string {
	client := http.Client{Timeout: 2 * time.Second}
	res, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return string(b)
}
