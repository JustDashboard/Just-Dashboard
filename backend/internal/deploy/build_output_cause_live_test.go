package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// TestLiveBuildFailureIsNamedFromBuildKit builds the incident's shape with the
// real recipe and buildx: an npm lockfile that no longer matches package.json.
// It proves the failed step comes back as a BuildError rather than exec's
// "exit status 1", and that the transcript the executor persists names the
// cause, its phase and the packages the lockfile is missing. Opt-in like the
// rest of the live suite: JD_DEPLOY_LIVE=1.
func TestLiveBuildFailureIsNamedFromBuildKit(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker/Buildx release host to run the live build diagnosis")
	}
	client := liveC4Docker(t)
	builder := NewArtifactBuilder(NewDockerArtifactBackend(client))
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"name":"diagnosis-fixture","version":"1.0.0","scripts":{"build":"echo built","start":"node -e 1"},"dependencies":{"is-odd":"3.0.1"}}`)
	writeBuildFixture(t, root, "package-lock.json", `{"name":"diagnosis-fixture","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"diagnosis-fixture","version":"1.0.0"}}}`)
	tag := fmt.Sprintf("just-dashboard-diagnosis:%d", time.Now().UnixNano())
	t.Cleanup(func() { _, _ = liveDockerOutput(context.Background(), "image", "rm", "--force", tag) })
	config := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", PackageManager: "npm", BuildCommand: "npm run build", StartCommand: "npm start"}
	prepared, err := builder.Prepare(context.Background(), root, config, false, tag)
	if err != nil {
		t.Fatal(err)
	}
	seq := int64(0)
	collector := newBuildOutputCollector(func() int64 { return seq })
	_, err = builder.Build(context.Background(), root, tag, config, prepared, nil, "", SourceIdentity{}, nil, func(line BuildLog) error {
		seq++
		collector.observe(line)
		return nil
	})
	var failure *dockerx.BuildError
	if !errors.As(err, &failure) || failure.Command != "npm ci" || failure.ExitCode != 1 || failure.Step == 0 {
		t.Fatalf("build error = %#v", err)
	}
	cause := buildFailureCause(err, collector, causeContext{build: config, prepared: prepared}, nil)
	if cause == nil || cause.Code != "build_lockfile_out_of_sync" || cause.Phase != phaseInstall ||
		!strings.Contains(strings.Join(cause.Subjects, ","), "is-odd") || cause.LineSeq == 0 {
		t.Fatalf("cause = %+v", cause)
	}
	t.Logf("named: %s", cause.sentence())
}
