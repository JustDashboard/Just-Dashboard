package selfcfg

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate/selfupdatetest"
)

// restartService is a Service for a checkout at a temporary directory, on the
// daemon at host.
func restartService(t *testing.T, host string) (*Service, string) {
	t.Helper()
	dir, checkout := t.TempDir(), t.TempDir()
	return New(Options{
		DataDir:    dir,
		DockerHost: host,
		Locate: func(context.Context) (*selfupdate.Location, error) {
			return &selfupdate.Location{Dir: checkout, Visible: checkout, Compose: "docker-compose.yml", Image: "img"}, nil
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}), checkout
}

// The restart sibling is replaced and started on the daemon JD_DOCKER_HOST
// names. The CLI used to inherit the process environment and reach
// /var/run/docker.sock whatever the setting said — on the machine where this
// was found, the daemon of another dashboard with a restart container of the
// same fixed name.
func TestRestartUsesTheConfiguredDaemon(t *testing.T) {
	calls := selfupdatetest.Docker(t)
	const host = "unix:///srv/jd/docker.sock"
	s, _ := restartService(t, host)

	if _, err := s.Restart(t.Context(), false, "test"); err != nil {
		t.Fatal(err)
	}
	if diff := selfupdatetest.AimedAt(calls(), host, "version ", "rm -f "+RestartContainer, "run --detach"); diff != "" {
		t.Fatal(diff)
	}
}

// dashboard.restart against a daemon that does not answer is refused with
// that reason, before anything is written: no `docker rm` or `docker run` is
// tried on any daemon, and the last restart's record and transcript stay.
func TestRestartAgainstADaemonThatDoesNotAnswerWritesNothing(t *testing.T) {
	calls := selfupdatetest.Docker(t)
	missing := selfupdatetest.MissingHost(t)
	s, _ := restartService(t, missing)
	done := time.Now().UTC()
	if err := s.store.Save(&Run{ID: "previous", Status: StatusSuccess, Phase: PhaseFinished, FinishedAt: &done}); err != nil {
		t.Fatal(err)
	}
	if f, err := s.store.OpenLog(); err != nil {
		t.Fatal(err)
	} else {
		f.WriteString("the last restart\n")
		f.Close()
	}

	_, err := s.Restart(t.Context(), false, "test")
	if !errors.Is(err, selfupdate.ErrDockerUnreachable) {
		t.Fatalf("err = %v, want ErrDockerUnreachable", err)
	}
	if !strings.Contains(err.Error(), "the restart was not started") || !strings.Contains(err.Error(), missing) {
		t.Errorf("error %q does not say the restart never began, or which daemon did not answer", err)
	}
	if diff := selfupdatetest.AimedAt(calls(), missing, "version "); diff != "" {
		t.Fatal(diff)
	}
	if run, _ := s.store.Load(); run == nil || run.ID != "previous" || run.Status != StatusSuccess {
		t.Fatalf("record = %+v, want the previous restart's", run)
	}
	if log := s.Transcript(); log != "the last restart\n" {
		t.Fatalf("transcript = %q, want the previous restart's", log)
	}
}

// A settings change is checked against the daemon before the .env is touched,
// so neither the file nor the backup the rollback restores from is rewritten.
func TestApplyAgainstADaemonThatDoesNotAnswerWritesNothing(t *testing.T) {
	calls := selfupdatetest.Docker(t)
	missing := selfupdatetest.MissingHost(t)
	s, checkout := restartService(t, missing)
	path := filepath.Join(checkout, ".env")
	const original = "JD_SITE=localhost\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	next := defaults()
	next.TerminalEnabled = !next.TerminalEnabled
	if _, err := s.Apply(t.Context(), next, "test", "127.0.0.1"); !errors.Is(err, selfupdate.ErrDockerUnreachable) {
		t.Fatalf("err = %v, want ErrDockerUnreachable", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != original {
		t.Fatalf(".env = %q, %v; want it untouched", got, err)
	}
	if _, err := os.Stat(path + ".jd-previous"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused apply rewrote the rollback backup")
	}
	if diff := selfupdatetest.AimedAt(calls(), missing, "version "); diff != "" {
		t.Fatal(diff)
	}
}
