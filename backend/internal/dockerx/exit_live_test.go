package dockerx

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// What the host alerts' container_down rule reads to tell a crash from a stop,
// asked of a real daemon: a container that exits on its own leaves its code
// and no kill in the event log, and one somebody stops leaves a kill, inside
// a window the log says it covered.
func TestLiveLastExitAndEventsTellACrashFromAStop(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for Docker mutation test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		raw, err := hostexec.Command(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %v: %v: %s", args, err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	c := New("unix:///var/run/docker.sock")
	events := c.NewEventLog(slog.New(slog.NewTextHandler(io.Discard, nil)))
	follow, stop := context.WithCancel(ctx)
	defer stop()
	events.Start(follow)
	for i := 0; i < 50; i++ {
		if running, _, _ := events.Status(); running {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	watching := time.Now()

	stamp := time.Now().UnixNano()
	crashed := docker("run", "-d", "--name", fmt.Sprintf("jd-exit-crash-%d", stamp), "caddy:2-alpine", "sh", "-c", "sleep 1; exit 3")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", crashed).Run()
	stopped := docker("run", "-d", "--name", fmt.Sprintf("jd-exit-stop-%d", stamp), "caddy:2-alpine", "sleep", "300")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", stopped).Run()
	docker("stop", "-t", "1", stopped)
	docker("wait", crashed)
	time.Sleep(500 * time.Millisecond)

	stoppedOn := func(id string, last ContainerExit) bool {
		for _, ev := range events.Find(200, EventFilter{Kinds: []string{"container"}, Container: id}) {
			if (ev.Action == "kill" || ev.Action == "stop") && !ev.Time.Before(last.StartedAt.Add(-time.Second)) &&
				!ev.Time.After(last.FinishedAt.Add(5*time.Second)) {
				return true
			}
		}
		return false
	}
	crash, err := c.LastExit(ctx, crashed)
	if err != nil || crash.ExitCode != 3 || crash.OOMKilled || crash.StartedAt.IsZero() || !crash.FinishedAt.After(crash.StartedAt) {
		t.Fatalf("crash = %+v, %v", crash, err)
	}
	if stoppedOn(crashed, crash) {
		t.Fatal("a container that exited on its own has a kill in the event log")
	}
	stop2, err := c.LastExit(ctx, stopped)
	if err != nil || (stop2.ExitCode != 137 && stop2.ExitCode != 143) {
		t.Fatalf("stop = %+v, %v", stop2, err)
	}
	if !stoppedOn(stopped, stop2) {
		t.Fatalf("docker stop left no kill in the event log: %+v", events.Find(50, EventFilter{Container: stopped}))
	}
	if !events.Covers(watching) {
		t.Fatal("the event log does not say it covered the window it followed")
	}
}
