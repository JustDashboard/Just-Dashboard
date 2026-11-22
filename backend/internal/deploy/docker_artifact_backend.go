package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

type DockerArtifactBackend struct {
	docker *dockerx.Client
	// railpackDir is where the Railpack planner writes its plan, a folder
	// Docker binds by its path on this server (build_railpack.go).
	railpackDir   string
	sweepRailpack sync.Once
	// builderDir is where a networked builder's configuration is written,
	// a folder the docker CLI reads by the same path as this process.
	builderDir string
}

// WithBuilderDir names the folder a networked builder's configuration is
// written to.
func (b *DockerArtifactBackend) WithBuilderDir(dir string) *DockerArtifactBackend {
	b.builderDir = dir
	return b
}

// StartNetworkedBuilder starts the builder an environment's builds that reach
// its databases run on (build_database.go).
func (b *DockerArtifactBackend) StartNetworkedBuilder(ctx context.Context, network BuildNetwork) (string, func(context.Context), error) {
	if b == nil || b.docker == nil {
		return "", nil, ErrBuilderUnavailable
	}
	if b.builderDir == "" {
		return "", nil, fmt.Errorf("%w: no folder for the networked builder's configuration", ErrBuilderUnavailable)
	}
	if len(network.Networks) == 0 {
		return "", nil, fmt.Errorf("%w: a networked build needs a network", ErrBuilderUnavailable)
	}
	name := dockerx.NetworkedBuilderName(network.Networks[0])
	builder, err := b.docker.StartNetworkedBuilder(ctx, dockerx.NetworkedBuilderSpec{
		Name: name, Networks: network.Networks, ConfigDir: b.builderDir,
	})
	if err != nil {
		return "", nil, err
	}
	return builder.Name, builder.Close, nil
}

// RunCacheOnly runs a build for what its steps do, keeping only the cache.
func (b *DockerArtifactBackend) RunCacheOnly(ctx context.Context, invocation BuildInvocation, emit func(BuildLog) error) error {
	if b == nil || b.docker == nil {
		return ErrBuilderUnavailable
	}
	secrets := make([]dockerx.BuildxSecret, 0, len(invocation.Secrets))
	for _, secret := range invocation.Secrets {
		secrets = append(secrets, dockerx.BuildxSecret{ID: secret.ID, Value: secret.Value})
	}
	buildArgs := make([]dockerx.BuildxArg, 0, len(invocation.BuildArgs))
	for _, arg := range invocation.BuildArgs {
		buildArgs = append(buildArgs, dockerx.BuildxArg{Name: arg.Name, Value: arg.Value})
	}
	_, err := consumeDockerBuildLogs(ctx, emit, func(child context.Context, output chan<- dockerx.LogLine) (dockerx.ImmutableImage, error) {
		return dockerx.ImmutableImage{}, b.docker.RunCacheOnly(child, dockerx.ImmutableBuildOptions{
			Dir: invocation.ContextDir, Dockerfile: invocation.Dockerfile, Platform: invocation.Platform,
			Secrets: secrets, BuildArgs: buildArgs, Builder: invocation.Builder,
		}, output)
	})
	return err
}

func NewDockerArtifactBackend(client *dockerx.Client) *DockerArtifactBackend {
	return &DockerArtifactBackend{docker: client}
}

// WithRailpackDir names the folder the Railpack planner writes into: one the
// daemon reaches by the same path as this process.
func (b *DockerArtifactBackend) WithRailpackDir(dir string) *DockerArtifactBackend {
	b.railpackDir = dir
	return b
}

func (b *DockerArtifactBackend) ResolveImage(
	ctx context.Context,
	reference, registryAuth string,
) (ResolvedImage, error) {
	if b == nil || b.docker == nil {
		return ResolvedImage{}, ErrBuilderUnavailable
	}
	image, err := b.docker.ResolveDistributionImage(ctx, reference, registryAuth)
	if err != nil {
		return ResolvedImage{}, err
	}
	if image == nil {
		return ResolvedImage{}, ErrArtifactMissing
	}
	return ResolvedImage{
		Reference: image.Reference, Digest: image.Digest,
		Platforms: append([]string(nil), image.Platforms...),
	}, nil
}

func (b *DockerArtifactBackend) BuildImage(
	ctx context.Context,
	invocation BuildInvocation,
	emit func(BuildLog) error,
) (ResolvedImage, error) {
	secrets := make([]dockerx.BuildxSecret, 0, len(invocation.Secrets))
	for _, secret := range invocation.Secrets {
		secrets = append(secrets, dockerx.BuildxSecret{ID: secret.ID, Value: secret.Value})
	}
	buildArgs := make([]dockerx.BuildxArg, 0, len(invocation.BuildArgs))
	for _, arg := range invocation.BuildArgs {
		buildArgs = append(buildArgs, dockerx.BuildxArg{Name: arg.Name, Value: arg.Value})
	}
	var frontend *dockerx.BuildxFrontend
	if invocation.Frontend != nil {
		frontend = &dockerx.BuildxFrontend{
			Image: invocation.Frontend.Image, CacheKey: invocation.Frontend.CacheKey,
			SecretsHash: invocation.Frontend.SecretsHash,
		}
	}
	image, err := consumeDockerBuildLogs(ctx, emit, func(child context.Context, output chan<- dockerx.LogLine) (dockerx.ImmutableImage, error) {
		return b.docker.BuildImmutable(child, dockerx.ImmutableBuildOptions{
			Dir: invocation.ContextDir, Dockerfile: invocation.Dockerfile,
			Tag: invocation.Tag, Platform: invocation.Platform,
			NoCache: invocation.NoCache, Pull: invocation.Pull, Secrets: secrets,
			Target: invocation.Target, BuildArgs: buildArgs, Frontend: frontend,
			Builder: invocation.Builder,
		}, output)
	})
	if err != nil {
		return ResolvedImage{}, err
	}
	return resolvedDockerImage(image), nil
}

func (b *DockerArtifactBackend) PullImage(
	ctx context.Context,
	reference, registryAuth string,
	emit func(BuildLog) error,
) (ResolvedImage, error) {
	image, err := consumeDockerBuildLogs(ctx, emit, func(child context.Context, output chan<- dockerx.LogLine) (dockerx.ImmutableImage, error) {
		return b.docker.PullImmutable(child, reference, registryAuth, output)
	})
	if err != nil {
		return ResolvedImage{}, err
	}
	return resolvedDockerImage(image), nil
}

func (b *DockerArtifactBackend) TagImage(ctx context.Context, source, target string) (ResolvedImage, error) {
	if b == nil || b.docker == nil {
		return ResolvedImage{}, ErrBuilderUnavailable
	}
	if err := b.docker.TagImage(ctx, source, target); err != nil {
		return ResolvedImage{}, err
	}
	return b.InspectImage(ctx, target)
}

func (b *DockerArtifactBackend) InspectImage(ctx context.Context, reference string) (ResolvedImage, error) {
	if b == nil || b.docker == nil {
		return ResolvedImage{}, ErrBuilderUnavailable
	}
	image, err := b.docker.InspectImmutableImage(ctx, reference)
	if err != nil {
		return ResolvedImage{}, err
	}
	return resolvedDockerImage(image), nil
}

func (b *DockerArtifactBackend) RemoveImage(ctx context.Context, reference string) error {
	if b == nil || b.docker == nil {
		return ErrBuilderUnavailable
	}
	_, err := b.docker.RemoveImage(ctx, reference, false, false)
	return err
}

func resolvedDockerImage(image dockerx.ImmutableImage) ResolvedImage {
	platforms := []string{}
	if image.OS != "" && image.Architecture != "" {
		platforms = append(platforms, image.OS+"/"+image.Architecture)
	}
	return ResolvedImage{
		Reference: image.Reference, Digest: image.Digest, ConfigDigest: image.ConfigDigest,
		OS: image.OS, Architecture: image.Architecture, Platforms: platforms,
		SizeBytes: image.SizeBytes,
	}
}

type dockerBuildResult struct {
	image dockerx.ImmutableImage
	err   error
}

func consumeDockerBuildLogs(
	ctx context.Context,
	emit func(BuildLog) error,
	operation func(context.Context, chan<- dockerx.LogLine) (dockerx.ImmutableImage, error),
) (dockerx.ImmutableImage, error) {
	if emit == nil {
		emit = func(BuildLog) error { return nil }
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	lines := make(chan dockerx.LogLine, 128)
	done := make(chan dockerBuildResult, 1)
	go func() {
		image, err := operation(child, lines)
		close(lines)
		done <- dockerBuildResult{image: image, err: err}
	}()
	var emitErr error
	for line := range lines {
		if emitErr == nil {
			emitErr = emit(BuildLog{Stream: line.Stream, Text: line.Text})
			if emitErr != nil {
				cancel()
			}
		}
	}
	result := <-done
	if emitErr != nil {
		return dockerx.ImmutableImage{}, fmt.Errorf("persist build output: %w", emitErr)
	}
	return result.image, result.err
}

type DockerArtifactRemover struct{ backend BuildBackend }

func NewDockerArtifactRemover(backend BuildBackend) *DockerArtifactRemover {
	return &DockerArtifactRemover{backend: backend}
}

func (r *DockerArtifactRemover) RemoveArtifact(ctx context.Context, artifact ReleaseArtifact) error {
	if artifact.Kind != ArtifactImage {
		// Compose/runtime/static-bundle rows are metadata records. Their bytes
		// are either inside an image or recreated from the immutable snapshot.
		return nil
	}
	if r == nil || r.backend == nil {
		return ErrBuilderUnavailable
	}
	var metadata struct {
		ConfigDigest string `json:"configDigest"`
	}
	_ = json.Unmarshal(artifact.Metadata, &metadata)
	immutableReference := artifact.Digest
	if contentDigestRE.MatchString(metadata.ConfigDigest) {
		// Docker's local image store is keyed by the config digest. A registry
		// manifest digest is immutable but is not always a removable local id.
		immutableReference = metadata.ConfigDigest
	}
	if current, err := r.backend.InspectImage(ctx, artifact.Reference); err == nil {
		sameManifest := current.Digest != "" && current.Digest == artifact.Digest
		sameConfig := metadata.ConfigDigest != "" && current.ConfigDigest == metadata.ConfigDigest
		if sameManifest || sameConfig {
			// The tag still names this release, so remove the tag. Docker will
			// preserve shared content while another retained tag references it.
			return r.backend.RemoveImage(ctx, artifact.Reference)
		}
	}
	// The tag disappeared or moved. Never pass it to removal in that case: use
	// only the config id/recorded digest belonging to the pruned release.
	return r.backend.RemoveImage(ctx, immutableReference)
}
