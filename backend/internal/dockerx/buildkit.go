package dockerx

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/docker/docker/api/types/image"
)

type BuildxSecret struct {
	ID    string
	Value string
}

type ImmutableBuildOptions struct {
	Dir        string
	Dockerfile string
	Tag        string
	Platform   string
	NoCache    bool
	Pull       bool
	Secrets    []BuildxSecret
}

type ImmutableImage struct {
	Reference    string
	Digest       string
	ConfigDigest string
	OS           string
	Architecture string
	RepoDigests  []string
	SizeBytes    int64
}

// BuildxAvailable verifies the exact CLI plugin used by BuildImmutable. A
// healthy Docker API alone does not prove that the reviewed builder exists.
func (c *Client) BuildxAvailable(ctx context.Context) bool {
	if c == nil {
		return false
	}
	cmd := hostexec.Command(ctx, "docker", "buildx", "version")
	return cmd.Run() == nil
}

// BuildImmutable is the deployment builder's Docker authority. It uses
// Buildx/BuildKit explicitly, keeps secret values in its process environment,
// streams plain progress, loads one host-platform image, and returns both OCI
// and config identities instead of treating a mutable tag as an artifact.
func (c *Client) BuildImmutable(
	ctx context.Context,
	opts ImmutableBuildOptions,
	out chan<- LogLine,
) (ImmutableImage, error) {
	if !dirExists(opts.Dir) {
		return ImmutableImage{}, os.ErrNotExist
	}
	if opts.Dockerfile == "" {
		opts.Dockerfile = "Dockerfile"
	}
	cleanDockerfile := filepath.Clean(opts.Dockerfile)
	if filepath.IsAbs(cleanDockerfile) || cleanDockerfile == ".." ||
		strings.HasPrefix(cleanDockerfile, ".."+string(filepath.Separator)) ||
		strings.ContainsAny(cleanDockerfile, "\x00\r\n") {
		return ImmutableImage{}, fmt.Errorf("Dockerfile must remain in the build context")
	}
	if info, err := os.Lstat(filepath.Join(opts.Dir, cleanDockerfile)); err != nil ||
		!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ImmutableImage{}, ErrNoDockerfile
	}
	if strings.TrimSpace(opts.Tag) == "" || strings.ContainsAny(opts.Tag, "\x00\r\n") {
		return ImmutableImage{}, fmt.Errorf("a safe image tag is required")
	}
	metadata, err := os.CreateTemp(opts.Dir, ".just-dashboard-build-metadata-")
	if err != nil {
		return ImmutableImage{}, err
	}
	metadataPath := metadata.Name()
	if err := metadata.Close(); err != nil {
		_ = os.Remove(metadataPath)
		return ImmutableImage{}, err
	}
	defer os.Remove(metadataPath)
	args, environment, err := BuildxCommand(opts, metadataPath)
	if err != nil {
		return ImmutableImage{}, err
	}
	buildCtx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()
	cmd := hostexec.CommandInDir(buildCtx, opts.Dir, "docker", args...)
	cmd.Env = environment
	reader := newBuildFailureReader()
	if err := runGroupStream(buildCtx, cmd, out, reader); err != nil {
		return ImmutableImage{}, buildRunError(ctx, buildCtx, err)
	}
	var buildMetadata map[string]any
	if raw, err := os.ReadFile(metadataPath); err == nil {
		_ = json.Unmarshal(raw, &buildMetadata)
	}
	detail, err := c.InspectImage(ctx, opts.Tag)
	if err != nil {
		return ImmutableImage{}, fmt.Errorf("inspect built image: %w", err)
	}
	digest := metadataDigest(buildMetadata)
	if digest == "" {
		digest = firstRepoDigest(detail.RepoDigests)
	}
	if digest == "" {
		digest = detail.ID
	}
	return ImmutableImage{
		Reference: opts.Tag, Digest: digest, ConfigDigest: detail.ID,
		OS: detail.OS, Architecture: detail.Architecture,
		RepoDigests: append([]string(nil), detail.RepoDigests...), SizeBytes: detail.Size,
	}, nil
}

// BuildxCommand is exported as a hermetic transcript boundary. Secret values
// occur only in the returned environment; argv contains generated environment
// variable names, so /proc command lines and persisted previews remain clean.
func BuildxCommand(opts ImmutableBuildOptions, metadataPath string) ([]string, []string, error) {
	args := []string{
		"buildx", "build", "--progress=plain", "--file", filepath.ToSlash(filepath.Clean(opts.Dockerfile)),
		"--tag", ImageRef(opts.Tag), "--load", "--metadata-file", metadataPath,
	}
	if opts.Pull {
		args = append(args, "--pull")
	}
	if opts.Platform != "" {
		if strings.ContainsAny(opts.Platform, "\x00\r\n, ") || !strings.Contains(opts.Platform, "/") {
			return nil, nil, fmt.Errorf("invalid Buildx platform")
		}
		args = append(args, "--platform", strings.ToLower(opts.Platform))
	}
	if opts.NoCache {
		args = append(args, "--no-cache")
	}
	secrets := append([]BuildxSecret(nil), opts.Secrets...)
	sort.Slice(secrets, func(i, j int) bool { return secrets[i].ID < secrets[j].ID })
	environment := scrubBuildEnvironment(os.Environ())
	seen := map[string]bool{}
	for index, secret := range secrets {
		if secret.ID == "" || strings.ContainsAny(secret.ID, "\x00\r\n,=") || seen[secret.ID] {
			return nil, nil, fmt.Errorf("invalid or duplicate BuildKit secret id")
		}
		seen[secret.ID] = true
		envName := fmt.Sprintf("JD_DEPLOY_BUILDKIT_SECRET_%d", index)
		environment = append(environment, envName+"="+secret.Value)
		args = append(args, "--secret", "id="+secret.ID+",env="+envName)
	}
	args = append(args, ".")
	environment = append(environment, "BUILDKIT_PROGRESS=plain", "DOCKER_CLI_HINTS=false")
	return args, environment, nil
}

func scrubBuildEnvironment(source []string) []string {
	result := make([]string, 0, len(source))
	for _, value := range source {
		if strings.HasPrefix(value, "JD_") || strings.HasPrefix(value, "VPSD_") ||
			strings.HasPrefix(value, "BUILDKIT_PROGRESS=") || strings.HasPrefix(value, "DOCKER_CLI_HINTS=") {
			continue
		}
		result = append(result, value)
	}
	return result
}

// buildTimeout is how long one buildx run may take. A release build of a
// large Rust, Gradle or monorepo project on a small server approaches it.
const buildTimeout = 30 * time.Minute

func runGroupStream(ctx context.Context, cmd *exec.Cmd, out chan<- LogLine, failure *buildFailureReader) error {
	// Plain pipes rather than StdoutPipe: Wait closes a StdoutPipe as soon as
	// the process exits, which can drop the last lines still buffered in it,
	// and BuildKit's last lines are the ones that name the failure.
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	defer stdout.Close()
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		stdoutWriter.Close()
		return err
	}
	defer stderr.Close()
	cmd.Stdout, cmd.Stderr = stdoutWriter, stderrWriter
	var wg sync.WaitGroup
	wg.Add(2)
	scan := func(reader io.Reader, stream string) {
		defer wg.Done()
		// Whatever stops the scanner, the pipe keeps being read so the build
		// never blocks writing to it.
		defer io.Copy(io.Discard, reader)
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 64*1024), maxLogLine)
		for scanner.Scan() {
			line := LogLine{Stream: stream, Text: strings.TrimRight(scanner.Text(), "\r")}
			failure.observe(line.Text)
			if out == nil {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case out <- line:
			}
		}
	}
	go scan(stdout, "stdout")
	go scan(stderr, "stderr")
	result, runErr := hostexec.RunGroup(ctx, cmd, 5*time.Second)
	// The child's copies closed when it exited; closing these ends the reads.
	stdoutWriter.Close()
	stderrWriter.Close()
	wg.Wait()
	var exit *exec.ExitError
	if errors.As(runErr, &exit) {
		return failure.failure(exit.ExitCode())
	}
	if runErr != nil {
		return runErr
	}
	if result.ExitCode != 0 {
		return failure.failure(result.ExitCode)
	}
	return nil
}

func metadataDigest(metadata map[string]any) string {
	for _, key := range []string{"containerimage.digest", "containerimage.config.digest"} {
		if value, ok := metadata[key].(string); ok && strings.HasPrefix(value, "sha256:") {
			return value
		}
	}
	return ""
}

func firstRepoDigest(values []string) string {
	for _, value := range values {
		if _, digest, ok := strings.Cut(value, "@"); ok {
			return digest
		}
	}
	return ""
}

// PullImmutable pulls the already-resolved name@digest and preserves registry
// authentication out of argv and logs.
func (c *Client) PullImmutable(
	ctx context.Context,
	reference, registryAuth string,
	out chan<- LogLine,
) (ImmutableImage, error) {
	cli, err := c.api()
	if err != nil {
		return ImmutableImage{}, err
	}
	stream, err := cli.ImagePull(ctx, reference, image.PullOptions{RegistryAuth: registryAuth})
	if err != nil {
		return ImmutableImage{}, err
	}
	defer stream.Close()
	decoder := json.NewDecoder(stream)
	for {
		var message struct {
			ID       string `json:"id"`
			Status   string `json:"status"`
			Progress string `json:"progress"`
			Error    string `json:"error"`
		}
		if err := decoder.Decode(&message); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return ImmutableImage{}, err
		}
		text := strings.TrimSpace(strings.Join([]string{message.ID, message.Status, message.Progress, message.Error}, " "))
		if text != "" && out != nil {
			select {
			case <-ctx.Done():
				return ImmutableImage{}, ctx.Err()
			case out <- LogLine{Stream: "stdout", Text: text}:
			}
		}
		if message.Error != "" {
			return ImmutableImage{}, fmt.Errorf("pull image: %s", message.Error)
		}
	}
	return c.InspectImmutableImage(ctx, reference)
}

func (c *Client) InspectImmutableImage(ctx context.Context, reference string) (ImmutableImage, error) {
	detail, err := c.InspectImage(ctx, reference)
	if err != nil {
		return ImmutableImage{}, err
	}
	digest := firstRepoDigest(detail.RepoDigests)
	if digest == "" {
		digest = detail.ID
	}
	return ImmutableImage{
		Reference: referenceWithoutDigest(reference), Digest: digest, ConfigDigest: detail.ID,
		OS: detail.OS, Architecture: detail.Architecture,
		RepoDigests: append([]string(nil), detail.RepoDigests...), SizeBytes: detail.Size,
	}, nil
}

func referenceWithoutDigest(reference string) string {
	name, _, ok := strings.Cut(reference, "@")
	if ok {
		return name
	}
	return reference
}
