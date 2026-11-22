package deploy

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The daemon is what release retention asks before it removes an image, so
// the question is asked of a real one: every container of an image counts,
// created, running, paused (a warm release) and exited (a sleeping
// environment's), and an image that is gone says so.
func TestLiveImageUseNamesEveryContainerOfAnImage(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to ask a real Docker daemon what uses an image")
	}
	client := liveC4Docker(t)
	backend := NewDockerArtifactBackend(client)
	ctx := context.Background()
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	tag := "jd-live-image-use:" + stamp
	if _, err := liveDockerOutput(ctx, "pull", "alpine:3.22"); err != nil {
		t.Fatal(err)
	}
	if _, err := liveDockerOutput(ctx, "tag", "alpine:3.22", tag); err != nil {
		t.Fatal(err)
	}
	names := []string{}
	t.Cleanup(func() {
		for _, name := range names {
			_, _ = liveDockerOutput(context.Background(), "rm", "--force", name)
		}
		_, _ = liveDockerOutput(context.Background(), "image", "rm", tag)
	})

	use, err := backend.ImageUse(ctx, tag)
	if err != nil {
		t.Fatal(err)
	}
	if len(use.Containers) != 0 || !slices.Contains(use.Tags, tag) || !strings.HasPrefix(use.ID, "sha256:") {
		t.Fatalf("an image nothing uses = %+v", use)
	}
	for _, step := range []struct {
		state string
		args  []string
	}{
		{"created", []string{"create", "--name", "jd-live-image-use-created-" + stamp, tag, "true"}},
		{"exited", []string{"run", "--name", "jd-live-image-use-exited-" + stamp, tag, "true"}},
		{"running", []string{"run", "-d", "--name", "jd-live-image-use-running-" + stamp, tag, "sleep", "300"}},
		{"paused", []string{"run", "-d", "--name", "jd-live-image-use-paused-" + stamp, tag, "sleep", "300"}},
	} {
		names = append(names, step.args[slices.Index(step.args, "--name")+1])
		if _, err := liveDockerOutput(ctx, step.args...); err != nil {
			t.Fatal(err)
		}
		if step.state == "paused" {
			if _, err := liveDockerOutput(ctx, "pause", names[len(names)-1]); err != nil {
				t.Fatal(err)
			}
		}
		use, err := backend.ImageUse(ctx, tag)
		if err != nil {
			t.Fatal(err)
		}
		if len(use.Containers) != len(names) {
			t.Fatalf("after a %s container, the daemon named %d containers, want %d", step.state, len(use.Containers), len(names))
		}
	}
	for _, name := range names {
		if _, err := liveDockerOutput(ctx, "rm", "--force", name); err != nil {
			t.Fatal(err)
		}
	}
	if use, err := backend.ImageUse(ctx, tag); err != nil || len(use.Containers) != 0 {
		t.Fatalf("after removing them = %+v, %v", use, err)
	}
	if _, err := backend.ImageUse(ctx, "jd-live-image-use:missing-"+stamp); !errors.Is(err, ErrImageGone) {
		t.Fatalf("a missing image = %v, want ErrImageGone", err)
	}
}
