package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// composeBackendFake builds each Compose service with build, by service
// name, and counts how many builds run at once.
type composeBackendFake struct {
	*artifactBackendFake
	build func(ctx context.Context, service string, emit func(BuildLog) error) error

	mu      sync.Mutex
	running int
	peak    int
}

func (f *composeBackendFake) BuildImage(ctx context.Context, invocation BuildInvocation, emit func(BuildLog) error) (ResolvedImage, error) {
	service := invocation.Tag[strings.LastIndex(invocation.Tag, "-")+1:]
	f.mu.Lock()
	f.running++
	f.peak = max(f.peak, f.running)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.running--
		f.mu.Unlock()
	}()
	if err := f.build(ctx, service, emit); err != nil {
		return ResolvedImage{}, err
	}
	return f.artifactBackendFake.BuildImage(ctx, invocation, emit)
}

// composeParallelFixture is a project of built services, named a, b, c…,
// each with its own context and Dockerfile, and one pulled image last.
func composeParallelFixture(t *testing.T, names ...string) (string, *ComposeAnalysis) {
	t.Helper()
	root := t.TempDir()
	analysis := &ComposeAnalysis{Digest: fakeContentDigest("compose"), Files: []string{"compose.yml"}}
	for _, name := range names {
		writeBuildFixture(t, root, name+"/Dockerfile", "FROM alpine:3.22\n")
		analysis.Services = append(analysis.Services, ComposeServicePlan{Name: name, BuildContext: name})
	}
	analysis.Services = append(analysis.Services, ComposeServicePlan{Name: "db", Image: "postgres:17"})
	return root, analysis
}

func buildComposeFixture(ctx context.Context, builder *ArtifactBuilder, root string, compose *ComposeAnalysis, emit func(BuildLog) error) (BuildArtifactResult, error) {
	return builder.Build(ctx, root, "just-dashboard/deployment-1:run-1", BuildPlanConfig{Method: BuildCompose},
		PreparedBuild{Method: BuildCompose}, nil, nil, "", SourceIdentity{Kind: SourceCompose}, compose, emit)
}

// Builds run at most the limit at a time; whatever order they end in, the
// release lists the services in the file's order and the transcript holds
// each service's lines whole, in that order.
func TestComposeServicesBuildBoundedAndRecordInTheFilesOrder(t *testing.T) {
	t.Parallel()
	names := []string{"a", "b", "c", "d", "e"}
	root, compose := composeParallelFixture(t, names...)
	// Each build waits until the one after it has ended, so they end in
	// reverse order; e, the last, ends first.
	ended := map[string]chan struct{}{}
	for _, name := range names {
		ended[name] = make(chan struct{})
	}
	backend := &composeBackendFake{artifactBackendFake: &artifactBackendFake{}}
	backend.build = func(ctx context.Context, service string, emit func(BuildLog) error) error {
		defer close(ended[service])
		for step := 1; step <= 3; step++ {
			if err := emit(BuildLog{Stream: "stdout", Text: fmt.Sprintf("#%d %s step", step, service)}); err != nil {
				return err
			}
		}
		if next := slices.Index(names, service) + 1; next < len(names) {
			select {
			case <-ended[names[next]]:
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Second):
				return errors.New("the next build never ended: builds did not overlap")
			}
		}
		return nil
	}
	builder := NewArtifactBuilder(backend)
	// The chain needs every build running at once to end; a limit of two
	// would wait on a slot forever, so the limit is the count here and the
	// bound is checked below with a gate instead.
	builder.composeParallelism = len(names)
	var mu sync.Mutex
	transcript := []BuildLog{}
	result, err := buildComposeFixture(context.Background(), builder, root, compose, func(line BuildLog) error {
		mu.Lock()
		defer mu.Unlock()
		transcript = append(transcript, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	order := []string{}
	for _, service := range result.Compose.Services {
		order = append(order, service.Plan.Name)
	}
	if !slices.Equal(order, append(slices.Clone(names), "db")) || len(result.Artifacts) != len(names)+2 ||
		len(result.Prepared.ComposeServices) != len(names) {
		t.Fatalf("services = %v, artifacts = %d, prepared = %d", order, len(result.Artifacts), len(result.Prepared.ComposeServices))
	}
	for index, name := range names {
		if result.Artifacts[index].Reference != composeServiceImageTag("just-dashboard/deployment-1:run-1", name) ||
			result.Prepared.ComposeServices[index].Name != name {
			t.Fatalf("artifact %d = %#v, want service %s", index, result.Artifacts[index], name)
		}
	}
	want := []string{}
	for _, name := range names {
		want = append(want, composeServiceBuildStatus+name, "#1 "+name+" step", "#2 "+name+" step", "#3 "+name+" step")
	}
	got := []string{}
	for _, line := range transcript {
		got = append(got, line.Text)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("transcript =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// The bound: with a limit of two, no more than two builds run at once,
	// and both slots are used.
	gate := make(chan struct{})
	bounded := &composeBackendFake{artifactBackendFake: &artifactBackendFake{}}
	bounded.build = func(ctx context.Context, _ string, _ func(BuildLog) error) error {
		select {
		case <-gate:
		case <-time.After(50 * time.Millisecond):
		}
		return nil
	}
	limited := NewArtifactBuilder(bounded)
	limited.composeParallelism = 2
	if _, err := buildComposeFixture(context.Background(), limited, root, compose, nil); err != nil {
		t.Fatal(err)
	}
	if bounded.peak != 2 {
		t.Fatalf("at most %d builds ran at once, want 2", bounded.peak)
	}
}

// The first failure stops the builds still running and those not started;
// the error names the service that failed, the services it stopped say so,
// and the failure's cause is read from its own lines though another
// service's output is written after them.
func TestTheFirstComposeFailureStopsTheOthersAndIsNamed(t *testing.T) {
	t.Parallel()
	root, compose := composeParallelFixture(t, "api", "web", "worker")
	backend := &composeBackendFake{artifactBackendFake: &artifactBackendFake{}}
	backend.build = func(ctx context.Context, service string, emit func(BuildLog) error) error {
		if service == "web" {
			for _, line := range []string{
				"#5 [2/2] RUN bun install --frozen-lockfile",
				"#5 0.4 error: lockfile had changes, but lockfile is frozen",
			} {
				if err := emit(BuildLog{Stream: "stderr", Text: line}); err != nil {
					return err
				}
			}
			return &dockerx.BuildError{Step: 5, Command: "bun install --frozen-lockfile", ExitCode: 1}
		}
		_ = emit(BuildLog{Stream: "stderr", Text: "#5 [2/2] RUN npm run build"})
		_ = emit(BuildLog{Stream: "stderr", Text: "#5 0.5 npm error Missing script: \"build\""})
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
			return errors.New("a build was not stopped by another's failure")
		}
	}
	builder := NewArtifactBuilder(backend)
	builder.composeParallelism = 2
	collector := newBuildOutputCollector(nil)
	transcript := []string{}
	started := time.Now()
	_, err := buildComposeFixture(context.Background(), builder, root, compose, func(line BuildLog) error {
		transcript = append(transcript, line.Text)
		collector.observe(line)
		return nil
	})
	if time.Since(started) > 5*time.Second {
		t.Fatal("the failure did not stop the other builds")
	}
	var failure *ComposeServiceError
	if !errors.As(err, &failure) || failure.Service != "web" {
		t.Fatalf("error = %v, want web's build failure", err)
	}
	joined := strings.Join(transcript, "\n")
	for _, want := range []string{
		"Stopped Compose service api: service web failed",
		"Stopped Compose service worker: service web failed",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("transcript lacks %q:\n%s", want, joined)
		}
	}
	// db's pull never started: worker held the last slot until web failed.
	if !strings.Contains(joined, "Stopped Compose service db: service web failed") || len(backend.pulls) != 0 {
		t.Fatalf("db was pulled after the failure (%v):\n%s", backend.pulls, joined)
	}
	if strings.Index(joined, composeServiceBuildStatus+"api") > strings.Index(joined, composeServiceBuildStatus+"web") {
		t.Fatalf("transcript is out of the file's order:\n%s", joined)
	}
	cause := buildFailureCause(err, collector, causeContext{build: BuildPlanConfig{Method: BuildCompose}}, nil)
	if cause == nil || cause.Service != "web" || cause.Code != "build_lockfile_out_of_sync" {
		t.Fatalf("cause = %+v, want web's lockfile", cause)
	}
}

// Cancelling the run stops every build, and nothing is named as the
// failure: the executor reads the run's own cancellation.
func TestCancellingTheRunStopsEveryComposeBuild(t *testing.T) {
	t.Parallel()
	root, compose := composeParallelFixture(t, "api", "web")
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	backend := &composeBackendFake{artifactBackendFake: &artifactBackendFake{}}
	backend.build = func(build context.Context, _ string, _ func(BuildLog) error) error {
		once.Do(func() { time.AfterFunc(20*time.Millisecond, cancel) })
		select {
		case <-build.Done():
			return build.Err()
		case <-time.After(10 * time.Second):
			return errors.New("a build outlived its run")
		}
	}
	builder := NewArtifactBuilder(backend)
	builder.composeParallelism = 2
	_, err := buildComposeFixture(ctx, builder, root, compose, nil)
	var failure *ComposeServiceError
	if !errors.Is(err, context.Canceled) || ctx.Err() == nil || (errors.As(err, &failure) && !errors.Is(failure.Err, context.Canceled)) {
		t.Fatalf("error = %v, want the run's cancellation", err)
	}
}

// A service waiting behind a slow one holds its lines in a private file
// past the memory budget, writes them in order when its turn comes, and
// leaves no file behind.
func TestHeldComposeOutputSpillsToATemporaryFileInOrder(t *testing.T) {
	temporary := t.TempDir()
	t.Setenv("TMPDIR", temporary)
	written := []BuildLog{}
	output := newOrderedBuildOutput(2, func(line BuildLog) error {
		written = append(written, line)
		return nil
	})
	head, held := output.writer(0), output.writer(1)
	line := strings.Repeat("x", 1023)
	count := heldMemoryBytes/len(line) + 200
	for index := 0; index < count; index++ {
		if err := held(BuildLog{Stream: "stdout", Text: fmt.Sprintf("%06d %s", index, line)}); err != nil {
			t.Fatal(err)
		}
	}
	if output.jobs[1].spill == nil {
		t.Fatal("held output past the budget stayed in memory")
	}
	if entries, _ := os.ReadDir(temporary); len(entries) != 1 {
		t.Fatalf("temporary files = %v", entries)
	}
	if err := head(BuildLog{Stream: "stdout", Text: "head"}); err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 {
		t.Fatalf("held lines were written before their turn: %d", len(written))
	}
	output.finish(0)
	_ = held(BuildLog{Stream: "stderr", Text: "after its turn came"})
	output.finish(1)
	if len(written) != count+2 || written[count+1].Text != "after its turn came" || written[count+1].Stream != "stderr" {
		t.Fatalf("wrote %d lines, last %q", len(written), written[len(written)-1].Text)
	}
	for index := 0; index < count; index++ {
		if !strings.HasPrefix(written[index+1].Text, fmt.Sprintf("%06d ", index)) {
			t.Fatalf("line %d = %.12q", index, written[index+1].Text)
		}
	}
	if entries, _ := os.ReadDir(temporary); len(entries) != 0 {
		t.Fatalf("temporary files left = %v", entries)
	}
}

func TestComposeBuildParallelismFollowsTheServer(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		cpus   int
		memory int64
		want   int
	}{
		{2, 4 << 30, 1}, {4, 8 << 30, 1}, {8, 16 << 30, 2}, {12, 48 << 30, 3}, {64, 256 << 30, 3},
		{12, 4 << 30, 2}, {12, 2 << 30, 1}, {12, 0, 3}, {1, 0, 1},
	} {
		if got := composeBuildParallelism(test.cpus, test.memory); got != test.want {
			t.Errorf("%d cores, %d GiB: %d, want %d", test.cpus, test.memory>>30, got, test.want)
		}
	}
}
