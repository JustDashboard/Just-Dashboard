package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// liveCacheFixtures are the fixtures whose second build, after a one-line
// source change, must start from what the first one installed or compiled:
// the file the change goes to, and what the second build's log must not
// show again.
var liveCacheFixtures = map[string]struct {
	source, comment string
	warm            func(logs []string) error
	// image checks the second build's image, where the build copied
	// something out of a cached directory.
	image func(ctx context.Context, tag string) error
}{
	// Cargo names each crate it compiles; with target/ cached a source
	// change compiles the service's own crate alone.
	"rust": {source: "src/main.rs", comment: "//", warm: notCompiledAgain("tokio ", "axum ")},
	// The Leptos build removes the last build's site from the cached
	// target/ and cargo-leptos writes it again: the second image must
	// still hold the hashed bundle and the hash file naming it.
	"leptos": {source: "src/app.rs", comment: "//", warm: notCompiledAgain("leptos ", "axum "), image: func(ctx context.Context, tag string) error {
		files, err := liveDockerOutput(ctx, "run", "--rm", "--entrypoint", "find", tag, "/home/app/site", "/hash.txt", "-type", "f")
		if err != nil {
			return fmt.Errorf("list the site: %v: %s", err, files)
		}
		listing := string(files)
		if !strings.Contains(listing, ".wasm") || !strings.Contains(listing, "/hash.txt") {
			return fmt.Errorf("the second image's site is incomplete:\n%s", listing)
		}
		return nil
	}},
	"trunk": {source: "src/main.rs", comment: "//", warm: notCompiledAgain("yew ")},
	// The module download comes before the source, so a commit that
	// changes only code reuses its layer; the cgo SQLite is in the build
	// cache.
	"go-embed": {source: "main.go", comment: "//", warm: stepCached("go mod download")},
	// The dependencies install from the lock and the requirement file
	// before the source.
	"python-uv": {source: "main.py", comment: "#", warm: stepCached("--no-install-project")},
	"fastapi":   {source: "main.py", comment: "#", warm: stepCached("pip install --requirement requirements.txt")},
}

// stepCached fails a log in which the RUN step naming part ran again
// rather than being taken from the layer cache.
func stepCached(part string) func(logs []string) error {
	return func(logs []string) error {
		for _, line := range logs {
			at := strings.Index(line, "] RUN ")
			if at < 0 || !strings.Contains(line, part) || !strings.HasPrefix(line, "#") {
				continue
			}
			step, _, _ := strings.Cut(line, " ")
			for _, other := range logs {
				if strings.TrimSpace(other) == step+" CACHED" {
					return nil
				}
			}
			return fmt.Errorf("the step %q ran again", line)
		}
		return fmt.Errorf("no step runs %q", part)
	}
}

// notCompiledAgain fails a log in which Cargo compiled one of the crates.
func notCompiledAgain(crates ...string) func(logs []string) error {
	return func(logs []string) error {
		for _, line := range logs {
			for _, crate := range crates {
				if strings.Contains(line, "Compiling "+crate) {
					return fmt.Errorf("the dependencies compiled again: %q", line)
				}
			}
		}
		return nil
	}
}

// TestLiveRecipeCachesSurviveASourceChange builds a fixture, changes one
// line of its source and builds it again, as two commits of one
// environment do, and logs both times: the measurement the dependency
// order and caches are for.
func TestLiveRecipeCachesSurviveASourceChange(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to build fixtures twice")
	}
	client := liveC4Docker(t)
	builder := NewArtifactBuilder(NewDockerArtifactBackend(client))
	prefix := os.Getenv("JD_LIVE_IMAGE_PREFIX")
	if prefix == "" {
		prefix = "jd-cache-test"
	}
	for name, fixture := range liveCacheFixtures {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Minute)
			defer cancel()
			root := t.TempDir()
			copyFrameworkFixture(t, name, root)
			detection, err := (Detector{}).DetectPath(ctx, root, SourceIdentity{})
			if err != nil {
				t.Fatal(err)
			}
			candidate := selectedDetectionCandidate(&detection)
			if candidate == nil {
				t.Fatalf("no unambiguous candidate: %+v", detection.Candidates)
			}
			config := BuildPlanConfig{Method: candidate.BuildMethod, Recipe: candidate.Recipe, BuildCommand: candidate.BuildCommand,
				StartCommand: candidate.StartCommand, OutputDirectory: candidate.OutputDirectory}
			// A fresh environment each run, so the first build is cold.
			stamp := time.Now().UnixNano()
			repository := fmt.Sprintf("%s/%s-%d", prefix, name, stamp)
			times := []time.Duration{}
			var logs []string
			for build := range 2 {
				// The layer cache leaves a cache mount's id out of a step's
				// key, so the first build's source is new too: otherwise
				// its steps are another run's, and its fresh caches empty.
				source := filepath.Join(root, filepath.FromSlash(candidate.Root), filepath.FromSlash(fixture.source))
				content, err := os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(source, append(content, []byte(fmt.Sprintf("\n%s change %d-%d\n", fixture.comment, stamp, build))...), 0o600); err != nil {
					t.Fatal(err)
				}
				tag := fmt.Sprintf("%s:build-%d", repository, build)
				t.Cleanup(func() { _, _ = liveDockerOutput(context.Background(), "image", "rm", "--force", tag) })
				logs = nil
				started := time.Now()
				livePrepareAndBuildWithin(t, builder, root, candidate.Root, tag, config, nil, func(line BuildLog) error { logs = append(logs, line.Text); return nil })
				times = append(times, time.Since(started))
			}
			t.Logf("%s: first build %.1f s, after a one-line source change %.1f s", name, times[0].Seconds(), times[1].Seconds())
			if err := fixture.warm(logs); err != nil {
				steps := []string{}
				for _, line := range logs {
					if strings.Contains(line, "] RUN ") || strings.Contains(line, " CACHED") {
						steps = append(steps, line)
					}
				}
				t.Errorf("%v\n%s", err, strings.Join(steps, "\n"))
			}
			if fixture.image != nil {
				if err := fixture.image(ctx, repository+":build-1"); err != nil {
					t.Error(err)
				}
			}
		})
	}
}
