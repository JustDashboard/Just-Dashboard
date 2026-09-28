package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// TestLiveC4ArtifactAdapters is the release-host contract for checkpoint C4.
// It is opt-in because it pulls reviewed base images and intentionally builds
// several images. A bare development machine gets an explicit skip; release
// CI runs it with JD_DEPLOY_LIVE=1.
func TestLiveC4ArtifactAdapters(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker/Buildx release host to run the C4 adapter matrix")
	}
	client := liveC4Docker(t)
	backend := NewDockerArtifactBackend(client)
	builder := NewArtifactBuilder(backend)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	createdTags := []string{}
	t.Cleanup(func() {
		for _, tag := range createdTags {
			_, _ = liveDockerOutput(context.Background(), "image", "rm", "--force", tag)
		}
	})

	t.Run("automatic recipe and secret layers", func(t *testing.T) {
		root := t.TempDir()
		writeBuildFixture(t, root, "package.json", "{\"name\":\"c4-fixture\",\"version\":\"1.0.0\",\"scripts\":{\"start\":\"node server.js\"}}")
		writeBuildFixture(t, root, "package-lock.json", "{\"name\":\"c4-fixture\",\"version\":\"1.0.0\",\"lockfileVersion\":3,\"requires\":true,\"packages\":{\"\":{\"name\":\"c4-fixture\",\"version\":\"1.0.0\"}}}")
		writeBuildFixture(t, root, "server.js", "console.log('c4 fixture')\n")
		tag := "just-dashboard-c4:" + stamp + "-recipe"
		createdTags = append(createdTags, tag)
		secret := "jd-c4-layer-secret-" + stamp
		config := BuildPlanConfig{
			Method: BuildRecipe, Recipe: "node", BuildCommand: `test -n "$BUILD_TOKEN"`, StartCommand: "npm start",
			Secrets: []BuildSecretConfig{{Variable: "BUILD_TOKEN", Step: "build"}},
		}
		logs := []string{}
		result := livePrepareAndBuild(t, builder, root, tag, config, map[string]string{"BUILD_TOKEN": secret}, nil, func(line BuildLog) error {
			logs = append(logs, line.Text)
			return nil
		})
		assertLiveArtifactSecretFree(t, tag, secret, result, logs)
	})

	t.Run("Dockerfile", func(t *testing.T) {
		root := t.TempDir()
		writeBuildFixture(t, root, "containers/Appfile", "FROM alpine:3.22\nCMD [\"true\"]\n")
		tag := "just-dashboard-c4:" + stamp + "-dockerfile"
		createdTags = append(createdTags, tag)
		result := livePrepareAndBuild(t, builder, root, tag, BuildPlanConfig{
			Method: BuildDockerfile, Dockerfile: "containers/Appfile",
		}, nil, nil, nil)
		assertLiveImageResult(t, result)
	})

	t.Run("static", func(t *testing.T) {
		root := t.TempDir()
		writeBuildFixture(t, root, "public/index.html", "<!doctype html><title>C4 static fixture</title>")
		tag := "just-dashboard-c4:" + stamp + "-static"
		createdTags = append(createdTags, tag)
		result := livePrepareAndBuild(t, builder, root, tag, BuildPlanConfig{
			Method: BuildStatic, OutputDirectory: "public",
		}, nil, nil, nil)
		assertLiveImageResult(t, result)
		if len(result.Artifacts) != 2 || result.Artifacts[1].Kind != ArtifactStaticBundle {
			t.Fatalf("static artifacts = %#v", result.Artifacts)
		}
	})

	var pulled ResolvedImage
	t.Run("immutable image", func(t *testing.T) {
		resolved, err := backend.ResolveImage(context.Background(), "alpine:3.22", "")
		if err != nil {
			t.Fatal(err)
		}
		result, err := builder.Build(context.Background(), t.TempDir(), "unused", BuildPlanConfig{Method: BuildImage},
			PreparedBuild{Method: BuildImage}, nil, nil, "", SourceIdentity{
				Kind: SourceImage, Repository: resolved.Reference, Digest: resolved.Digest,
			}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		assertLiveImageResult(t, result)
		if result.Image.Digest != resolved.Digest {
			t.Fatalf("pull changed resolved digest: %s -> %s", resolved.Digest, result.Image.Digest)
		}
		pulled = result.Image
	})

	t.Run("mixed Compose", func(t *testing.T) {
		root := t.TempDir()
		content := "services:\n  built:\n    build: service\n  pulled:\n    image: alpine:3.22\n"
		writeBuildFixture(t, root, "compose.yml", content)
		writeBuildFixture(t, root, "service/Dockerfile", "FROM alpine:3.22\nCMD [\"true\"]\n")
		analysis, err := analyzeComposeDocuments([]ComposeDocument{{Path: "compose.yml", Content: content}})
		if err != nil {
			t.Fatal(err)
		}
		tag := "just-dashboard-c4:" + stamp + "-compose"
		createdTags = append(createdTags, composeServiceImageTag(tag, "built"))
		prepared, err := builder.Prepare(context.Background(), root, BuildPlanConfig{Method: BuildCompose}, false, tag)
		if err != nil {
			t.Fatal(err)
		}
		result, err := builder.Build(context.Background(), root, tag, BuildPlanConfig{Method: BuildCompose},
			prepared, nil, nil, "", SourceIdentity{Kind: SourceCompose}, &analysis, nil)
		if err != nil {
			t.Fatal(err)
		}
		if result.Compose == nil || len(result.Compose.Services) != 2 || len(result.Artifacts) != 3 ||
			result.Artifacts[2].Kind != ArtifactCompose || len(result.Prepared.ComposeServices) != 1 {
			t.Fatalf("Compose result = %#v", result)
		}
		for _, service := range result.Compose.Services {
			if !contentDigestRE.MatchString(service.Digest) {
				t.Fatalf("Compose service is mutable: %#v", service)
			}
		}
	})

	t.Run("failed build preserves live runtime", func(t *testing.T) {
		if pulled.Digest == "" {
			t.Fatal("image fixture did not resolve")
		}
		name := "just-dashboard-c4-live-" + stamp
		_, err := liveDockerOutput(context.Background(), "run", "--detach", "--name", name,
			pulled.Reference+"@"+pulled.Digest, "sleep", "300")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = liveDockerOutput(context.Background(), "rm", "--force", name) })
		before, err := liveDockerOutput(context.Background(), "inspect", "--format", "{{.Id}}|{{.State.Running}}|{{.Image}}", name)
		if err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		writeBuildFixture(t, root, "Dockerfile", "FROM alpine:3.22\nRUN exit 23\n")
		failedTag := "just-dashboard-c4:" + stamp + "-failed"
		createdTags = append(createdTags, failedTag)
		prepared, err := builder.Prepare(context.Background(), root, BuildPlanConfig{Method: BuildDockerfile}, false, failedTag)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := builder.Build(context.Background(), root, failedTag, BuildPlanConfig{Method: BuildDockerfile},
			prepared, nil, nil, "", SourceIdentity{}, nil, nil); err == nil {
			t.Fatal("deliberately failing build succeeded")
		}
		after, err := liveDockerOutput(context.Background(), "inspect", "--format", "{{.Id}}|{{.State.Running}}|{{.Image}}", name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(bytes.TrimSpace(before), bytes.TrimSpace(after)) {
			t.Fatalf("failed build changed live runtime: before %q after %q", before, after)
		}
	})
}

func liveC4Docker(t *testing.T) *dockerx.Client {
	t.Helper()
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	client := dockerx.New(host)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	availability := client.Ping(ctx)
	if !availability.Available {
		_ = client.Close()
		t.Skipf("live C4 suite requires Docker: %s", availability.Error)
	}
	if !client.BuildxAvailable(ctx) {
		_ = client.Close()
		t.Skip("live C4 suite requires the Docker Buildx plugin")
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func livePrepareAndBuild(
	t *testing.T,
	builder *ArtifactBuilder,
	root, tag string,
	config BuildPlanConfig,
	variables map[string]string,
	compose *ComposeAnalysis,
	emit func(BuildLog) error,
) BuildArtifactResult {
	t.Helper()
	prepared, err := builder.Prepare(context.Background(), root, config, false, tag, buildVariableNames(variables)...)
	if err != nil {
		t.Fatal(err)
	}
	// Every value a live build is given is treated as secret, as the
	// strictest redaction the executor applies.
	secret := map[string]bool{}
	for name := range variables {
		secret[name] = true
	}
	result, err := builder.Build(context.Background(), root, tag, config, prepared, variables, secret, "", SourceIdentity{}, compose, emit)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertLiveImageResult(t *testing.T, result BuildArtifactResult) {
	t.Helper()
	if !contentDigestRE.MatchString(result.Image.Digest) || !contentDigestRE.MatchString(result.Image.ConfigDigest) ||
		len(result.Artifacts) == 0 || result.Artifacts[0].Kind != ArtifactImage ||
		result.Artifacts[0].Digest != result.Image.Digest {
		t.Fatalf("live image result is not immutable: %#v", result)
	}
}

func assertLiveArtifactSecretFree(t *testing.T, tag, secret string, result BuildArtifactResult, logs []string) {
	t.Helper()
	assertLiveImageResult(t, result)
	serialized, err := json.Marshal(struct {
		Result BuildArtifactResult
		Logs   []string
	}{result, logs})
	if err != nil {
		t.Fatal(err)
	}
	history, historyErr := liveDockerOutput(context.Background(), "history", "--no-trunc", tag)
	inspect, inspectErr := liveDockerOutput(context.Background(), "image", "inspect", tag)
	if historyErr != nil || inspectErr != nil {
		t.Fatalf("inspect built image: history=%v inspect=%v", historyErr, inspectErr)
	}
	for label, content := range map[string][]byte{
		"release metadata/output": serialized, "image history": history, "image config": inspect,
	} {
		if bytes.Contains(content, []byte(secret)) {
			t.Fatalf("secret leaked through %s", label)
		}
	}
	archive := filepath.Join(t.TempDir(), "image.tar")
	if _, err := liveDockerOutput(context.Background(), "save", "--output", archive, tag); err != nil {
		t.Fatal(err)
	}
	contains, err := fileContains(archive, []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	if contains {
		t.Fatal("secret was found in the saved image layers")
	}
}

func liveDockerOutput(ctx context.Context, args ...string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	command := hostexec.Command(commandCtx, "docker", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func fileContains(path string, needle []byte) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	buffer := make([]byte, 64*1024+len(needle))
	carried := 0
	for {
		count, readErr := file.Read(buffer[carried:])
		total := carried + count
		if bytes.Contains(buffer[:total], needle) {
			return true, nil
		}
		if total >= len(needle)-1 {
			carried = copy(buffer, buffer[total-(len(needle)-1):total])
		} else {
			carried = copy(buffer, buffer[:total])
		}
		if readErr != nil {
			if readErr == io.EOF {
				return false, nil
			}
			return false, readErr
		}
	}
}
