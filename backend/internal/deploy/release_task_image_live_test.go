package deploy

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A release task runs in the image it releases: the application's own files
// and toolchain are there, the variables are the release's, and the container
// is gone afterwards however the task ended.
func TestLiveReleaseTaskRunsInTheReleaseImage(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to run release tasks in real containers")
	}
	client := liveC4Docker(t)
	owner := NewDockerRuntimeOwner(client)
	builder := NewArtifactBuilder(NewDockerArtifactBackend(client))
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	tag := "just-dashboard-release-task:" + stamp
	t.Cleanup(func() { _, _ = liveDockerOutput(context.Background(), "image", "rm", "--force", tag) })

	root := t.TempDir()
	writeBuildFixture(t, root, "Dockerfile", "FROM alpine:3.22\nWORKDIR /app\nCOPY bin/migrate bin/migrate\nRUN chmod +x bin/migrate\nENTRYPOINT [\"/bin/false\"]\n")
	writeBuildFixture(t, root, "bin/migrate", "#!/bin/sh\necho \"migrating $DATABASE_URL with $MIGRATION_TOKEN on $PORT\"\n")
	image := livePrepareAndBuild(t, builder, root, tag, BuildPlanConfig{Method: BuildDockerfile}, nil, nil, nil)
	assertLiveImageResult(t, image)

	environmentID := time.Now().UnixNano()%1_000_000_000 + 7_000
	request := ReleaseTaskRuntimeRequest{
		Run:     EngineRun{ID: environmentID + 1, EnvironmentID: environmentID},
		Release: Release{ID: environmentID + 2, EnvironmentID: environmentID, RunID: environmentID + 1, Number: 1},
		Image:   immutableRuntimeImage(image.Image), Plan: RuntimePlanConfig{InternalPort: 8080},
		Variables: map[string]string{"DATABASE_URL": "postgres://db-1.jd.internal/app"},
		Secret:    map[string]bool{},
	}
	// A container a stopped dashboard left under the task's name does not
	// block the resumed run.
	if _, err := liveDockerOutput(context.Background(), "create", "--name", releaseTaskContainerName(environmentID, request.Run.ID, 0),
		"--label", "io.just-dashboard.managed=true", "--label", "io.just-dashboard.environment-id="+strconv.FormatInt(environmentID, 10),
		"--label", "io.just-dashboard.run-id="+strconv.FormatInt(request.Run.ID, 10), "--label", releaseTaskLabel+"=migrate",
		request.Image, "true"); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		command string
		code    int
		output  string
		timeout int
	}{
		{"bin/migrate", 0, "migrating postgres://db-1.jd.internal/app with [REDACTED] on 8080", 60},
		{"bin/migrate && exit 3", 3, "migrating", 60},
		{"npx prisma migrate deploy", 127, "", 60},
		{"sleep 30", -1, "", 1},
	} {
		request.Task = ReleaseTaskConfig{Name: "migrate", Command: fixture.command, TimeoutSeconds: fixture.timeout, Env: []string{"MIGRATION_TOKEN"}, Runner: ReleaseTaskRunnerImage}
		request.Index = 0
		lines := []string{}
		evidence, cleanup, err := runImageReleaseTask(context.Background(), owner, request, map[string]string{"MIGRATION_TOKEN": "task-secret"},
			func(line BuildLog) error {
				lines = append(lines, line.Text)
				return nil
			})
		output := strings.Join(lines, "\n")
		if evidence.ExitCode != fixture.code || (fixture.code == 0) != (err == nil) || !strings.Contains(output, fixture.output) ||
			strings.Contains(output, "task-secret") || cleanup.(map[string]any)["removed"] != true {
			t.Fatalf("%q: evidence %+v, cleanup %v, err %v, output %q", fixture.command, evidence, cleanup, err, output)
		}
		left, _ := liveDockerOutput(context.Background(), "ps", "--all", "--quiet", "--filter", "label=io.just-dashboard.run-id="+strconv.FormatInt(request.Run.ID, 10))
		if strings.TrimSpace(string(left)) != "" {
			t.Fatalf("%q left its container behind: %s", fixture.command, left)
		}
	}
}
