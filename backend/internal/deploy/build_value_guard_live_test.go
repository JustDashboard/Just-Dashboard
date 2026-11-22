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

// TestLiveBuildValueChangeRerunsTheStepThatReadIt is DG-1's proof on a real
// BuildKit: the same source built again with an unchanged build value reuses
// every step, and with a changed one reruns the step that mounts it — and
// only that one, the install keeping its layer.
func TestLiveBuildValueChangeRerunsTheStepThatReadIt(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker/Buildx host to prove build-value guards")
	}
	client := liveC4Docker(t)
	builder := NewArtifactBuilder(NewDockerArtifactBackend(client)).WithBuildValueKey([]byte("live fixture key, 32 bytes long!"))
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	prefix := firstNonEmpty(os.Getenv("JD_LIVE_IMAGE_PREFIX"), "just-dashboard-live")
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"name":"guard-fixture","version":"1.0.0","scripts":{"start":"node server.js"}}`)
	writeBuildFixture(t, root, "package-lock.json", `{"name":"guard-fixture","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"guard-fixture","version":"1.0.0"}}}`)
	// A unique file keeps this run from reusing another run's layers.
	writeBuildFixture(t, root, "server.js", "console.log('guard fixture "+stamp+"')\n")
	config := BuildPlanConfig{
		Method: BuildRecipe, Recipe: "node", StartCommand: "node server.js",
		BuildCommand: `printf %s "$SITE_URL" > site.txt`,
		Secrets:      []BuildSecretConfig{{Variable: "NPM_CONFIG_FIXTURE", Step: "install"}},
	}
	tags := []string{}
	t.Cleanup(func() {
		for _, tag := range tags {
			_, _ = liveDockerOutput(context.Background(), "image", "rm", "--force", tag)
		}
	})
	build := func(name, site string) (logs []string, shipped string) {
		t.Helper()
		tag := prefix + "/build-values:" + stamp + "-" + name
		tags = append(tags, tag)
		livePrepareAndBuild(t, builder, root, tag, config, map[string]string{
			"SITE_URL": site, "NPM_CONFIG_FIXTURE": "fixture-install-value",
		}, nil, func(line BuildLog) error { logs = append(logs, line.Text); return nil })
		output, err := liveDockerOutput(context.Background(), "run", "--rm", "--network", "none", "--entrypoint", "cat", tag, "/app/site.txt")
		if err != nil {
			t.Fatal(err)
		}
		return logs, string(output)
	}
	_, first := build("first", "https://one.example.test")
	sameLogs, same := build("same", "https://one.example.test")
	changedLogs, changed := build("changed", "https://two.example.test")
	if first != "https://one.example.test" || same != first || changed != "https://two.example.test" {
		t.Fatalf("shipped site.txt: first %q, same %q, changed %q", first, same, changed)
	}
	if !liveStepCached(sameLogs, "site.txt") || !liveStepCached(sameLogs, "npm ci") {
		t.Fatalf("an unchanged value rebuilt:\n%s", strings.Join(sameLogs, "\n"))
	}
	if liveStepCached(changedLogs, "site.txt") {
		t.Fatalf("a changed build value reused the build step:\n%s", strings.Join(changedLogs, "\n"))
	}
	if !liveStepCached(changedLogs, "npm ci") {
		t.Fatalf("a build-step value change reran the install:\n%s", strings.Join(changedLogs, "\n"))
	}
	for _, logs := range [][]string{sameLogs, changedLogs} {
		for _, line := range logs {
			if strings.Contains(line, "fixture-install-value") {
				t.Fatalf("a build value reached the transcript: %q", line)
			}
		}
	}
}

var liveStepHeaderRE = regexp.MustCompile(`^#(\d+) \[[^\]]+\] RUN (.*)$`)

// liveStepCached reports whether BuildKit's plain progress marked the RUN
// whose instruction contains needle as CACHED.
func liveStepCached(logs []string, needle string) bool {
	step := ""
	for _, line := range logs {
		for _, part := range strings.Split(line, "\n") {
			if match := liveStepHeaderRE.FindStringSubmatch(strings.TrimSpace(part)); match != nil && strings.Contains(match[2], needle) {
				step = match[1]
			}
		}
	}
	if step == "" {
		return false
	}
	for _, line := range logs {
		for _, part := range strings.Split(line, "\n") {
			if strings.TrimSpace(part) == "#"+step+" CACHED" {
				return true
			}
		}
	}
	return false
}
