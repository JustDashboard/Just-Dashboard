package proxysvc

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Renewal against real certbot, a real ACME authority and a real nginx: the
// hook the switch installs is what certbot runs after renewing, and it is
// what makes nginx serve the renewed certificate; a renewal that will fail
// is called before the run, and certbot's own log of the run that did is
// read back as the page reads it on a host that renews from cron.
//
// certbot runs as this user, in directories of the test's own, against an
// isolated Pebble; nginx is a private instance on a loopback port.
func TestLiveRenewalHookReloadsNginxAndAFailedRenewalIsRead(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for a certbot renewal against an isolated Pebble and a private nginx")
	}
	for _, tool := range []string{"certbot", "docker", "nginx"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed here", tool)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	dir := t.TempDir()
	directory, bundle := startPebble(ctx, t, dir)

	config := filepath.Join(dir, "letsencrypt")
	work := filepath.Join(dir, "work")
	logs := filepath.Join(dir, "logs")
	webroot := filepath.Join(dir, "webroot")
	for _, d := range []string{config, work, logs, webroot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	useLetsencryptDir(t, config)
	t.Setenv("JD_ACME_DIRECTORY", directory)
	t.Setenv("JD_ACME_CA_ROOT", "")
	t.Setenv("REQUESTS_CA_BUNDLE", bundle)
	certbot := func(args ...string) (string, error) {
		t.Helper()
		cmd, err := CertbotCommand(ctx, nil, append(args, "--config-dir", config, "--work-dir", work, "--logs-dir", logs)...)
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	const name = "app.jd-live.test"
	args, err := New(t.TempDir(), "").IssueArgs(ctx, IssueRequest{
		Domains: []string{name}, Email: "ops@example.com", Method: "webroot", WebRoot: webroot,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Pebble answers its first requests before it has finished starting.
	for attempt := 0; ; attempt++ {
		out, err := certbot(args...)
		if err == nil {
			break
		}
		if attempt > 10 || !strings.Contains(out, "connect") {
			t.Fatalf("issuance: %v\n%s", err, out)
		}
		time.Sleep(time.Second)
	}

	// How it renews, as certbot wrote it.
	conf, err := readRenewalConf(filepath.Join(config, "renewal", name+".conf"))
	if err != nil {
		t.Fatal(err)
	}
	if conf.Authenticator != "webroot" || conf.Installer != "" || strings.Join(conf.Webroots, " ") != webroot || conf.Server != directory {
		t.Fatalf("renewal configuration = %+v", conf)
	}

	// A private nginx serving the lineage, reached by a shim the hook finds
	// on its PATH; a systemctl that manages no nginx, so the hook signals it.
	port, stopNginx := servePrivateNginx(t, dir,
		filepath.Join(config, "live", name, "fullchain.pem"), filepath.Join(config, "live", name, "privkey.pem"), name)
	defer stopNginx()
	served := func() string {
		t.Helper()
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", "127.0.0.1:"+strconv.Itoa(port),
			&tls.Config{ServerName: name, InsecureSkipVerify: true})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		return conn.ConnectionState().PeerCertificates[0].SerialNumber.Text(16)
	}
	first, _ := CertbotSerials()
	if got := served(); got != first[name] {
		t.Fatalf("nginx serves %s, the lineage holds %s", got, first[name])
	}

	hook, err := InstallRenewalHook()
	if err != nil || hook.State != "installed" {
		t.Fatalf("hook = %+v, %v", hook, err)
	}
	out, err := certbot("renew", "--force-renewal", "--no-random-sleep-on-renew", "--non-interactive")
	if err != nil {
		t.Fatalf("renewal: %v\n%s", err, out)
	}
	renewed, _ := CertbotSerials()
	if renewed[name] == first[name] {
		t.Fatalf("the forced renewal kept the certificate:\n%s", out)
	}
	// The hook ran: nginx serves the new certificate, not the one it read
	// at start. A reload lands a moment after the signal.
	deadline := time.Now().Add(10 * time.Second)
	for served() != renewed[name] {
		if time.Now().After(deadline) {
			t.Fatalf("nginx still serves %s after the renewal to %s:\n%s", served(), renewed[name], out)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if health := logRenewalHealth(logs, nil); health.State != "ok" || len(health.HookFailures) != 0 {
		t.Fatalf("after a renewal that passed: %+v", health)
	}

	// A configuration broken since the last reload: the hook refuses to
	// reload, certbot only warns, and the quiet run the timer makes passes
	// with nothing in its output. certbot's log has the hook's failure.
	nginxConf := filepath.Join(dir, "nginx", "nginx.conf")
	good, _ := os.ReadFile(nginxConf)
	writeFile(t, nginxConf, strings.Replace(string(good), "events {}", "events {}\nbroken_directive on;", 1))
	started := time.Now().UTC()
	out, err = certbot("-q", "renew", "--force-renewal", "--no-random-sleep-on-renew")
	if err != nil || strings.TrimSpace(out) != "" {
		t.Fatalf("a quiet renewal whose hook failed: %v\n%q", err, out)
	}
	unreloaded, _ := CertbotSerials()
	if unreloaded[name] == renewed[name] || served() != renewed[name] {
		t.Fatalf("renewed to %s, nginx serves %s", unreloaded[name], served())
	}
	hookPath := filepath.Join(config, "renewal-hooks", "deploy", renewalHookName)
	want := "nginx -t failed, so nginx was not reloaded for " + filepath.Join(config, "live", name)
	health := logRenewalHealth(logs, nil)
	if health.State != "ok" || len(health.HookFailures) != 1 || health.HookFailures[0].Kind != "deploy-hook" ||
		health.HookFailures[0].Command != hookPath || health.HookFailures[0].Code != 1 ||
		!strings.Contains(health.HookFailures[0].Output, `unknown directive "broken_directive"`) ||
		!strings.HasSuffix(health.HookFailures[0].Output, want) {
		raw, _ := os.ReadFile(filepath.Join(logs, "letsencrypt.log"))
		t.Fatalf("health = %+v\n%s", health, raw)
	}
	// A timer's run is found in the log by when it started.
	run, err := loggedRenewalNear(logs, started)
	if err != nil || run == nil || fmt.Sprint(hookFailures(run.Lines)) != fmt.Sprint(health.HookFailures) {
		t.Fatalf("the run near %v = %+v, %v", started, run, err)
	}
	writeFile(t, nginxConf, string(good))

	// Issued by hand with the manual plugin and no hook: called before the
	// run, and the run fails exactly so.
	raw, _ := os.ReadFile(filepath.Join(config, "renewal", name+".conf"))
	manual := regexp.MustCompile(`(?m)^authenticator = webroot$`).ReplaceAllString(string(raw), "authenticator = manual")
	if err := os.WriteFile(filepath.Join(config, "renewal", name+".conf"), []byte(manual), 0o644); err != nil {
		t.Fatal(err)
	}
	confs, _ := readRenewalConfs(config)
	rt, err := loadCertbotRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	problems := New(t.TempDir(), "").renewalProblems(ctx, rt, confs)
	if len(problems[name]) != 1 || !strings.Contains(problems[name][0], "manual plugin") {
		t.Fatalf("problems = %q", problems)
	}
	if out, err := certbot("-q", "renew", "--force-renewal", "--no-random-sleep-on-renew"); err == nil {
		t.Fatalf("a manual renewal with no hook passed:\n%s", out)
	}
	written := map[string]time.Time{}
	if at, ok := confs[0].written(); ok {
		written[name] = at
	}
	health = logRenewalHealth(logs, written)
	if health.State != "failed" || len(health.Failures) != 1 || health.Failures[0].Lineage != name ||
		!strings.Contains(health.Failures[0].Reason, "--manual-auth-hook") || health.LastRun == nil ||
		time.Since(*health.LastRun) > time.Minute || time.Since(*health.LastRun) < -time.Minute {
		t.Fatalf("health read from certbot's log = %+v", health)
	}
}

// servePrivateNginx starts nginx on a loopback port serving one TLS site,
// with a shim named nginx on PATH that drives this instance, and a systemctl
// that manages no nginx.
func servePrivateNginx(t *testing.T, dir, cert, key, name string) (int, func()) {
	t.Helper()
	binary, _ := exec.LookPath("nginx")
	root := filepath.Join(dir, "nginx")
	bin := filepath.Join(dir, "bin")
	for _, d := range []string{root, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	user := ""
	if os.Getuid() == 0 {
		user = "user root;\n"
	}
	conf := filepath.Join(root, "nginx.conf")
	writeFile(t, conf, fmt.Sprintf(`%[1]spid %[2]s/nginx.pid;
error_log %[2]s/error.log;
events {}
http {
    access_log off;
    client_body_temp_path %[2]s/body;
    proxy_temp_path %[2]s/proxy;
    fastcgi_temp_path %[2]s/fastcgi;
    uwsgi_temp_path %[2]s/uwsgi;
    scgi_temp_path %[2]s/scgi;
    server {
        listen 127.0.0.1:%[3]d ssl;
        server_name %[4]s;
        ssl_certificate %[5]s;
        ssl_certificate_key %[6]s;
        location / { return 200 "ok"; }
    }
}
`, user, root, port, name, cert, key))
	writeExecutable(t, filepath.Join(bin, "nginx"), fmt.Sprintf("#!/bin/sh\nexec %q -p %q -c %q \"$@\"\n", binary, root, conf))
	writeExecutable(t, filepath.Join(bin, "systemctl"), "#!/bin/sh\necho \"Unit nginx.service not loaded.\" >&2\nexit 5\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var output bytes.Buffer
	command := exec.Command(binary, "-p", root, "-c", conf, "-g", "daemon off;")
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; ; attempt++ {
		if conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), time.Second); err == nil {
			conn.Close()
			break
		}
		if attempt > 50 {
			t.Fatalf("nginx did not start: %s", output.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	return port, func() {
		_ = command.Process.Signal(syscall.SIGTERM)
		_ = command.Wait()
		if t.Failed() {
			t.Log(output.String())
		}
	}
}

// This host's own renewal record, read as the page reads it: the real
// systemd and the real journal, read-only. Where the host's certbot.service
// last failed, the reading is "failed" with certbot's reason for each
// certificate — not the "Scheduled" the page used to show while the timer
// was active and every run failed.
func TestLiveRenewalHealthReadsThisHostsTimer(t *testing.T) {
	if os.Getenv("JD_HOST_RENEWAL_LIVE") != "1" {
		t.Skip("set JD_HOST_RENEWAL_LIVE=1 to read this host's certbot.timer and its journal")
	}
	ctx := context.Background()
	scheduled, source := renewalScheduled(ctx)
	if !scheduled || !strings.HasSuffix(source, ".timer") {
		t.Skipf("no active certbot timer here (%q)", source)
	}
	health := renewalHealth(ctx, source, nil)
	t.Logf("%s: %+v", source, *health)
	props, err := systemctlShow(ctx, health.Service, "Result")
	if err != nil {
		t.Fatal(err)
	}
	if health.Error != "" || health.NextRun == nil {
		t.Fatalf("health = %+v", health)
	}
	switch props["Result"] {
	case "success":
		if health.State != "ok" && health.State != "running" && health.State != "never" {
			t.Fatalf("systemd says success, the reading says %s", health.State)
		}
	default:
		if health.State != "failed" || (len(health.Failures) == 0 && health.Reason == "") {
			t.Fatalf("systemd says %s, the reading says %+v", props["Result"], health)
		}
	}
	log, err := New(t.TempDir(), "").RenewalLog(ctx)
	if err != nil || len(log.Runs) == 0 {
		t.Fatalf("log = %+v, %v", log, err)
	}
	t.Logf("%d runs, newest %v %s with %d lines", len(log.Runs), log.Runs[0].Start, log.Runs[0].Result, len(log.Runs[0].Lines))
}
