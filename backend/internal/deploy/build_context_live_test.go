package deploy

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestLiveUserDockerfileContextIsReusedUnchanged builds an unchanged user
// Dockerfile twice through the real builder: the second build reuses
// `COPY . .` from the first, and nothing of the dashboard's reaches the
// image. buildx's metadata file used to be written into the context, which
// changed it on every build.
func TestLiveUserDockerfileContextIsReusedUnchanged(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker/Buildx host to build a user Dockerfile twice")
	}
	builder := NewArtifactBuilder(NewDockerArtifactBackend(liveC4Docker(t)))
	root := t.TempDir()
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	writeBuildFixture(t, root, "Dockerfile", "FROM alpine:3.22\nWORKDIR /app\nCOPY . .\nCMD [\"ls\", \"-A\", \"/app\"]\n")
	writeBuildFixture(t, root, "app.txt", "context fixture "+stamp+"\n")
	config := BuildPlanConfig{Method: BuildDockerfile}
	copied := regexp.MustCompile(`^#(\d+) \[[^\]]*\] COPY \. \.$`)
	for run := 1; run <= 2; run++ {
		tag := fmt.Sprintf("just-dashboard-context:%s-%d", stamp, run)
		t.Cleanup(func() { _, _ = liveDockerOutput(context.Background(), "image", "rm", "--force", tag) })
		logs := []string{}
		livePrepareAndBuild(t, builder, root, tag, config, nil, nil, func(line BuildLog) error {
			logs = append(logs, line.Text)
			return nil
		})
		vertex := ""
		for _, line := range logs {
			if match := copied.FindStringSubmatch(line); match != nil {
				vertex = match[1]
			}
		}
		if vertex == "" {
			t.Fatalf("build %d printed no COPY . . step:\n%s", run, strings.Join(logs, "\n"))
		}
		cached := false
		for _, line := range logs {
			cached = cached || line == "#"+vertex+" CACHED"
		}
		if run == 2 && !cached {
			t.Fatalf("an unchanged context did not reuse COPY . .:\n%s", strings.Join(logs, "\n"))
		}
		listing, err := liveDockerOutput(context.Background(), "run", "--rm", "--network", "none", tag)
		if err != nil {
			t.Fatal(err)
		}
		if files := strings.Fields(string(listing)); strings.Join(files, " ") != "Dockerfile app.txt" {
			t.Fatalf("build %d image holds %v, want only the repository's files", run, files)
		}
	}
}
