package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestLiveNodeInstallLayerOutlivesASourceChange is DS-1's proof on a real
// BuildKit: a Next.js app on Bun and one on npm, each built twice with a
// one-line change to a page between, keep their install and their
// node_modules layer, so the second build only compiles. The same npm app
// with a postinstall script keeps the source-first order and reinstalls,
// which is the comparison: each build's seconds are logged.
func TestLiveNodeInstallLayerOutlivesASourceChange(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker/Buildx host to prove the install layer survives a source change")
	}
	client := liveC4Docker(t)
	builder := NewArtifactBuilder(NewDockerArtifactBackend(client))
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	prefix := firstNonEmpty(os.Getenv("JD_LIVE_IMAGE_PREFIX"), "just-dashboard-live")
	for _, test := range []struct {
		name, fixture, install string
		build, start           string
		sourceFirst            bool
	}{
		{name: "next-bun", fixture: "next", install: "bun install", build: "bun run build", start: "bun run start"},
		{name: "next-npm", fixture: "next-standalone", install: "npm ci", build: "npm run build", start: "npm start"},
		{name: "next-npm-postinstall", fixture: "next-standalone", install: "npm ci", build: "npm run build", start: "npm start", sourceFirst: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			copyFrameworkFixture(t, test.fixture, root)
			// The fixtures' own postinstall checks a credential's scope;
			// here the install's order is what is measured.
			_ = os.Remove(filepath.Join(root, "install-check.cjs"))
			rewriteLiveManifest(t, root, test.sourceFirst)
			page := filepath.Join(root, "app", "page.jsx")
			config := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: test.build, StartCommand: test.start}
			// The repository name is shared by both builds and unique to the
			// run, so neither reuses another run's cache mounts or layers.
			repository := prefix + "/install-order-" + test.name + "-" + stamp
			var tags []string
			t.Cleanup(func() {
				for _, tag := range tags {
					_, _ = liveDockerOutput(context.Background(), "image", "rm", "--force", tag)
				}
			})
			build := func(run string) ([]string, time.Duration) {
				tag := repository + ":" + run
				tags = append(tags, tag)
				var logs []string
				started := time.Now()
				livePrepareAndBuild(t, builder, root, tag, config, nil, nil, func(line BuildLog) error { logs = append(logs, line.Text); return nil })
				return logs, time.Since(started)
			}
			_, cold := build("first")
			content, err := os.ReadFile(page)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(page, append(content, []byte("\n// a one-line change "+stamp+"\n")...), 0o644); err != nil {
				t.Fatal(err)
			}
			logs, warm := build("second")
			t.Logf("%s: first build %.1fs, after a one-line source change %.1fs", test.name, cold.Seconds(), warm.Seconds())
			installCached := liveInstructionCached(logs, test.install)
			if installCached == test.sourceFirst {
				t.Fatalf("install cached = %v after a source change, source first = %v:\n%s", installCached, test.sourceFirst, strings.Join(logs, "\n"))
			}
			if !test.sourceFirst && !liveInstructionCached(logs, "/app/node_module[s]") {
				t.Fatalf("the runtime stage copied node_modules again:\n%s", strings.Join(logs, "\n"))
			}
			if liveInstructionCached(logs, test.build) {
				t.Fatalf("the build reused its layer after a source change:\n%s", strings.Join(logs, "\n"))
			}
		})
	}
}

// rewriteLiveManifest drops the fixture's postinstall, or replaces it with
// one that reads nothing, which still keeps the install after the source.
func rewriteLiveManifest(t *testing.T, root string, postinstall bool) {
	t.Helper()
	file := filepath.Join(root, "package.json")
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatal(err)
	}
	scripts, _ := manifest["scripts"].(map[string]any)
	delete(scripts, "postinstall")
	if postinstall {
		scripts["postinstall"] = "node -e 0"
	}
	encoded, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(file, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

var liveInstructionHeaderRE = regexp.MustCompile(`^#(\d+) \[[^\]]+\] (.*)$`)

// liveInstructionCached reports whether BuildKit's plain progress marked
// the instruction containing needle as CACHED.
func liveInstructionCached(logs []string, needle string) bool {
	steps := map[string]bool{}
	cached := map[string]bool{}
	for _, line := range logs {
		for _, part := range strings.Split(line, "\n") {
			part = strings.TrimSpace(part)
			if match := liveInstructionHeaderRE.FindStringSubmatch(part); match != nil && strings.Contains(match[2], needle) {
				steps[match[1]] = true
			}
			if step, ok := strings.CutSuffix(part, " CACHED"); ok {
				cached[strings.TrimPrefix(step, "#")] = true
			}
		}
	}
	if len(steps) == 0 {
		return false
	}
	for step := range steps {
		if !cached[step] {
			return false
		}
	}
	return true
}
