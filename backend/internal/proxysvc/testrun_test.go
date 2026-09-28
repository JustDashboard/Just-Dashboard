package proxysvc

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// unfinishingNginx is an nginx whose config test passes, or gives no verdict
// the way the file named by the returned mode path says: `sleep` outlives
// its timeout, with the sleep a child of the shell as nsenter's nginx is of
// nsenter; `kill` is killed by a signal; `missing` is nsenter failing to
// find the host's binary. Every run's argv is appended to the log beside it.
func unfinishingNginx(t *testing.T) (mode, log string) {
	t.Helper()
	dir := t.TempDir()
	mode, log = filepath.Join(dir, "mode"), filepath.Join(dir, "log")
	shimOnlyPath(t, map[string]string{"nginx": `echo "$*" >> '` + log + `'
m=
[ -e '` + mode + `' ] && read -r m < '` + mode + `'
if [ "$1" = "-t" ]; then
	case "$m" in
	sleep) /bin/sleep 5 ;;
	kill) kill -9 $$ ;;
	missing) echo 'nsenter: failed to execute nginx: No such file or directory' >&2; exit 127 ;;
	esac
	echo 'nginx: configuration file /etc/nginx/nginx.conf test is successful' >&2
fi
exit 0
`})
	return mode, log
}

func setMode(t *testing.T, path, mode string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(mode+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A test cut off, out of time, killed or never started was kept as the last
// test and read as nginx refusing its configuration: the overview then said
// "nginx's configuration fails its test" about files nginx passes. None of
// them is a verdict: each is ErrTestUnfinished with why, the last test is the
// one that finished before, a reload reloads nothing and a start starts
// nothing.
func TestATestThatGivesNoVerdictIsNotKept(t *testing.T) {
	mode, log := unfinishingNginx(t)
	defer func(was time.Duration) { testTimeout = was }(testTimeout)
	testTimeout = 300 * time.Millisecond
	service := New(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile"))
	if _, err := service.Test(context.Background(), KindNginx); err != nil {
		t.Fatal(err)
	}
	kept, _ := service.LastTest(KindNginx)

	for _, tc := range []struct {
		mode, why string
		cause     error
		output    string
	}{
		{"sleep", "nginx -t took longer than 300ms and was stopped", context.DeadlineExceeded, ""},
		{"kill", "nginx -t was ended by a signal (killed)", nil, ""},
		{"missing", "nginx -t could not be run (exit status 127)", nil, "nsenter: failed to execute nginx"},
	} {
		setMode(t, mode, tc.mode)
		began := time.Now()
		res, err := service.Test(context.Background(), KindNginx)
		var unfinished *UnfinishedTestError
		if res != nil || !errors.Is(err, ErrTestUnfinished) || !errors.As(err, &unfinished) || err.Error() != tc.why ||
			!strings.Contains(unfinished.Output, tc.output) {
			t.Fatalf("%s: %+v, %v; want %q", tc.mode, res, err, tc.why)
		}
		if tc.cause != nil && !errors.Is(err, tc.cause) {
			t.Fatalf("%s: %v does not say it ran out of time", tc.mode, err)
		}
		// The sleep that holds the output open is not waited for.
		if took := time.Since(began); took > testTimeout+testWaitDelay+time.Second {
			t.Fatalf("%s: the test took %s", tc.mode, took)
		}
		if again, _ := service.LastTest(KindNginx); !again.CheckedAt.Equal(kept.CheckedAt) || !again.Validation.Valid {
			t.Fatalf("%s: kept as the last test: %+v", tc.mode, again.Validation)
		}
	}

	setMode(t, mode, "kill")
	if res, err := service.Reload(context.Background(), KindNginx); res != nil || !errors.Is(err, ErrTestUnfinished) {
		t.Fatalf("a reload over an unfinished test: %+v, %v", res, err)
	}
	started := false
	if _, err := service.WithTestedConfig(context.Background(), KindNginx, func() error {
		started = true
		return nil
	}); !errors.Is(err, ErrTestUnfinished) || started {
		t.Fatalf("a start over an unfinished test: %v, started %v", err, started)
	}
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "-s reload") {
		t.Fatalf("nginx was reloaded over a test that did not finish: %q", b)
	}
	if again, _ := service.LastTest(KindNginx); !again.CheckedAt.Equal(kept.CheckedAt) {
		t.Fatalf("a reload's or a start's unfinished test was kept: %+v", again.Validation)
	}
}

// A binary that is not there never ran, which is not Caddy refusing its
// file: the host without Caddy kept "valid: false" with no output.
func TestATestWhoseBinaryIsMissingIsNotKept(t *testing.T) {
	shimOnlyPath(t, map[string]string{})
	service := New(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile"))
	res, err := service.Test(context.Background(), KindCaddy)
	if res != nil || !errors.Is(err, ErrTestUnfinished) || !errors.Is(err, exec.ErrNotFound) ||
		!strings.HasPrefix(err.Error(), "caddy validate --config ") || !strings.Contains(err.Error(), " could not be run: ") {
		t.Fatalf("got %+v, %v", res, err)
	}
	if _, ok := service.LastTest(KindCaddy); ok {
		t.Fatal("a test that never ran was kept")
	}
}

// The overview's Test config and Reload pass the request's context, which a
// closed or refreshed tab cancels. Once begun, the test runs to its verdict
// and is kept.
func TestATestIsNotStoppedByItsCallerGoingAway(t *testing.T) {
	unfinishingNginx(t)
	service := New(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile"))
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := service.Test(gone, KindNginx)
	if err != nil || !res.Valid {
		t.Fatalf("a test whose caller left: %+v, %v", res, err)
	}
	if kept, ok := service.LastTest(KindNginx); !ok || !kept.Validation.Valid {
		t.Fatalf("its verdict was not kept: %+v, %v", kept, ok)
	}
}

// A candidate the editor's caller gave up on is not reported as refused, and
// is put back; a save whose test does not finish saves nothing.
func TestAnUnfinishedValidationIsNotARefusalAndIsPutBack(t *testing.T) {
	unfinishingNginx(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "conf.d", "app.conf")
	if err := os.WriteFile(file, []byte("server { listen 80; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := New(root, filepath.Join(root, "Caddyfile"))
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	for name, run := range map[string]func() (*ValidationResult, error){
		"validate": func() (*ValidationResult, error) {
			return service.Validate(gone, KindNginx, file, "server { listen 81; }\n")
		},
		"save": func() (*ValidationResult, error) {
			return service.WriteConfig(gone, KindNginx, file, "server { listen 81; }\n")
		},
	} {
		res, err := run()
		if res != nil || !errors.Is(err, ErrTestUnfinished) || !errors.Is(err, context.Canceled) || errors.Is(err, ErrInvalidConf) {
			t.Fatalf("%s: %+v, %v", name, res, err)
		}
		if b, _ := os.ReadFile(file); string(b) != "server { listen 80; }\n" {
			t.Fatalf("%s left %q on disk", name, b)
		}
	}
	if _, ok := service.LastTest(KindNginx); ok {
		t.Fatal("an unfinished test was kept")
	}
}

// The ingress is tested through `docker exec`, which exits 1 for its own
// failures as Caddy does for a refusal: a container that stopped under the
// test is docker's words, not Caddy's verdict.
func TestTheIngressTestTellsDockerFromCaddy(t *testing.T) {
	dir := shimOnlyPath(t, map[string]string{"docker": `if [ -e "$0.stopped" ]; then
	echo 'Error response from daemon: container 3f2a is not running' >&2
	exit 1
fi
echo 'Error: adapting config using caddyfile: unknown directive: frobnicate, at /etc/caddy/Caddyfile:3' >&2
exit 1
`})
	edge := &dockerCaddy{ID: "3f2a", Name: "ingress"}
	res, err := edge.validate(context.Background())
	if err != nil || res.Valid || len(res.Diagnostics) != 1 || res.Diagnostics[0].Line != 3 {
		t.Fatalf("Caddy refusing its file: %+v, %v", res, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker.stopped"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = edge.validate(context.Background())
	if !errors.Is(err, ErrTestUnfinished) ||
		err.Error() != "docker exec ingress caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile could not be run: Error response from daemon: container 3f2a is not running" {
		t.Fatalf("docker failing to reach the container: %v", err)
	}
}
