package jobs

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// A second certbot fails on the lock the first holds, so a second start of
// the same family is refused while the first runs and allowed once it ends.
func TestStartExclusiveRefusesWhileTheFamilyRuns(t *testing.T) {
	m := testManager(t)
	release := make(chan struct{})
	first, ok := m.StartExclusive("certbot.", Spec{Kind: "certbot.renew", Title: "Renewing a"},
		func(ctx context.Context, out Emitter) error {
			<-release
			return nil
		})
	if !ok {
		t.Fatal("the first certbot job was refused")
	}

	ran := false
	running, ok := m.StartExclusive("certbot.", Spec{Kind: "certbot.issue", Title: "Issuing b"},
		func(ctx context.Context, out Emitter) error {
			ran = true
			return nil
		})
	if ok || running.ID != first.ID {
		t.Fatalf("a second certbot job started beside the first: ok=%v job=%+v", ok, running)
	}
	// Another family is not held up by it.
	if _, ok := m.StartExclusive("packages.", Spec{Kind: "packages.install"},
		func(ctx context.Context, out Emitter) error { return nil }); !ok {
		t.Fatal("an unrelated job was refused")
	}

	close(release)
	waitFor(t, "the first job to finish", func() bool {
		j, _, _ := m.Get(first.ID)
		return j.Status != StatusRunning
	})
	next, ok := m.StartExclusive("certbot.", Spec{Kind: "certbot.issue"},
		func(ctx context.Context, out Emitter) error { return nil })
	if !ok {
		t.Fatal("refused after the first job finished")
	}
	waitFor(t, "the next job to finish", func() bool {
		j, _, _ := m.Get(next.ID)
		return j.Status != StatusRunning
	})
	if ran {
		t.Fatal("the refused job's runner ran")
	}
}

// Two requests arriving together are the case the lock is for: exactly one
// of them may start.
func TestStartExclusiveLetsOneOfManyThrough(t *testing.T) {
	m := testManager(t)
	release := make(chan struct{})
	defer close(release)
	var wg sync.WaitGroup
	var mu sync.Mutex
	started := 0
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := m.StartExclusive("certbot.", Spec{Kind: "certbot.renew"},
				func(ctx context.Context, out Emitter) error {
					<-release
					return nil
				}); ok {
				mu.Lock()
				started++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if started != 1 {
		t.Fatalf("%d certbot jobs started at once", started)
	}
}

// RunCmd streams a command somebody else built, and the console shows the
// argv it was given rather than the plumbing around it.
func TestEmitterRunCmdStreamsACommandTheCallerBuilt(t *testing.T) {
	m := testManager(t)
	job := m.Start(Spec{Kind: "test"}, func(ctx context.Context, out Emitter) error {
		cmd := exec.CommandContext(ctx, "sh", "-c", "echo out; echo err >&2; exit 3")
		code, err := out.RunCmd(cmd, []string{"certbot", "renew"})
		if err != nil {
			return err
		}
		if code != 3 {
			t.Errorf("exit code = %d", code)
		}
		return nil
	})
	waitFor(t, "the command to finish", func() bool {
		j, _, _ := m.Get(job.ID)
		return j.Status != StatusRunning
	})
	j, lines, _ := m.Get(job.ID)
	if j.ExitCode != 3 {
		t.Fatalf("job exit code = %d", j.ExitCode)
	}
	if lines[0].Stream != "status" || lines[0].Text != "$ certbot renew" {
		t.Fatalf("first line = %+v", lines[0])
	}
	var text []string
	for _, l := range lines[1:] {
		text = append(text, l.Stream+":"+l.Text)
	}
	joined := strings.Join(text, " ")
	if !strings.Contains(joined, "stdout:out") || !strings.Contains(joined, "stderr:err") {
		t.Fatalf("output = %q", joined)
	}
}
