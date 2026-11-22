package deploy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readTimingTranscript feeds a recorded buildx transcript to a reader a line
// at a time, as the build step's emitter does. The transcripts are a Next.js
// app built with the old and the new Node recipe order (testdata/buildkit-timing,
// neutral copies of the deploy-speed experiment's logs).
func readTimingTranscript(t *testing.T, name, install, compile string) *buildTimingReader {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "buildkit-timing", name))
	if err != nil {
		t.Fatal(err)
	}
	reader := newBuildTimingReader(install, compile)
	for _, line := range strings.Split(string(raw), "\n") {
		reader.observe(BuildLog{Stream: "stdout", Text: line})
	}
	return reader
}

func TestBuildTimingReadsWhereTheBuildWent(t *testing.T) {
	for _, tc := range []struct {
		name, file, install, compile string
		want                         BuildTiming
	}{
		{
			// Source before install: a commit reinstalls, and the whole app,
			// node_modules and all, is exported and unpacked again.
			name: "install after source", file: "install-after-source.txt",
			install: "bun install --frozen-lockfile", compile: "bun run build",
			want: BuildTiming{
				InstallSeconds: 5.5, CompileSeconds: 26.6,
				ExportSeconds: 66.4, UnpackSeconds: 35.7, ImageSeconds: 102.4,
				Steps: 8, CachedSteps: 3, ImageBytes: 864_700_000,
			},
		},
		{
			// Install before source: the install is cached and node_modules
			// its own layer, so the export is the app's own files.
			name: "install before source", file: "install-before-source.txt",
			install: "bun install --frozen-lockfile", compile: "bun run build",
			want: BuildTiming{
				InstallCached: true, CompileSeconds: 25.6,
				ExportSeconds: 8.9, UnpackSeconds: 2.0, ImageSeconds: 11.1,
				Steps: 10, CachedSteps: 6, ImageBytes: 864_700_000,
			},
		},
		{
			// The plan named nothing: the package managers' own verbs do.
			name: "named by pattern", file: "install-before-source.txt",
			want: BuildTiming{
				InstallCached: true, CompileSeconds: 25.6,
				ExportSeconds: 8.9, UnpackSeconds: 2.0, ImageSeconds: 11.1,
				Steps: 10, CachedSteps: 6, ImageBytes: 864_700_000,
			},
		},
		{
			name: "everything cached", file: "all-cached.txt",
			install: "bun install --frozen-lockfile", compile: "bun run build",
			want: BuildTiming{
				InstallCached: true, CompileCached: true,
				UnpackSeconds: 0.1, ImageSeconds: 0.5,
				Steps: 10, CachedSteps: 9, ImageBytes: 864_700_000,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := readTimingTranscript(t, tc.file, tc.install, tc.compile).summary(864_700_000)
			if got == nil {
				t.Fatal("no timing read")
			}
			if *got != tc.want {
				t.Fatalf("timing = %+v\nwant     %+v", *got, tc.want)
			}
		})
	}
}

func TestBuildTimingAddsUpEachBuildxRun(t *testing.T) {
	reader := newBuildTimingReader("npm ci", "npm run build")
	for _, text := range []string{
		"#5 [web 2/3] RUN npm ci", "#5 DONE 4.0s",
		"#6 [web 3/3] RUN npm run build", "#6 DONE 10.0s",
		"#7 exporting to image", "#7 exporting layers 3.0s done", "#7 DONE 3.5s",
	} {
		reader.observe(BuildLog{Stream: "stdout", Text: text})
	}
	// A Compose stack's next service is a new buildx run, its vertices
	// numbered from the start again.
	reader.observe(BuildLog{Stream: "status", Text: composeServiceBuildStatus + "worker"})
	for _, text := range []string{
		"#5 [worker 2/2] RUN npm ci", "#5 CACHED",
		"#6 exporting to docker image format", "#6 sending tarball 1.5s done", "#6 DONE 2.0s",
	} {
		reader.observe(BuildLog{Stream: "stdout", Text: text})
	}
	got := reader.summary(0)
	want := BuildTiming{
		InstallSeconds: 4, InstallCached: true, CompileSeconds: 10,
		ExportSeconds: 4.5, ImageSeconds: 5.5, Steps: 3, CachedSteps: 1,
	}
	if got == nil || *got != want {
		t.Fatalf("timing = %+v, want %+v", got, want)
	}
}

func TestBuildTimingIsAbsentWithoutABuild(t *testing.T) {
	reader := newBuildTimingReader("", "")
	for _, text := range []string{
		"docker.io/library/nginx@sha256:abc Pulling from library/nginx",
		"Status: Image is up to date for nginx",
	} {
		reader.observe(BuildLog{Stream: "stdout", Text: text})
	}
	if got := reader.summary(1 << 20); got != nil {
		t.Fatalf("a pulled image has no build timing: %+v", got)
	}
}

func TestBuildTimingNamesStepsByTheirCommand(t *testing.T) {
	reader := newBuildTimingReader("", "")
	for instruction, want := range map[string]timingStepKind{
		"RUN --mount=type=cache,id=x,target=/root/.npm npm ci --omit=dev": timingInstall,
		"RUN pip install --no-cache-dir -r requirements.txt":              timingInstall,
		"RUN --mount=type=secret,id=A,env=A pnpm run build":               timingCompile,
		"RUN go build -o /out/app ./cmd/app":                              timingCompile,
		"RUN bun install && bun run build":                                timingCompile,
		"COPY . .":                                                        timingOther,
		"RUN ln -s bun /usr/local/bin/bunx":                               timingOther,
	} {
		if got := reader.kindOf(instruction); got != want {
			t.Errorf("kindOf(%q) = %d, want %d", instruction, got, want)
		}
	}
}

func TestBuiltImageBytesAddsAComposeStacksImages(t *testing.T) {
	if got := builtImageBytes(BuildArtifactResult{Image: ResolvedImage{SizeBytes: 7}}); got != 7 {
		t.Fatalf("one image = %d", got)
	}
	got := builtImageBytes(BuildArtifactResult{Artifacts: []ReleaseArtifactInput{
		{Kind: ArtifactImage, SizeBytes: 3}, {Kind: ArtifactImage, SizeBytes: 4},
		{Kind: ArtifactCompose, SizeBytes: 100},
	}})
	if got != 7 {
		t.Fatalf("stack = %d, want 7", got)
	}
}

// timedBuildBackend prints a successful BuildKit transcript and answers with
// the image it made.
type timedBuildBackend struct {
	scriptedBuildBackend
	image ResolvedImage
}

func (b *timedBuildBackend) BuildImage(ctx context.Context, invocation BuildInvocation, emit func(BuildLog) error) (ResolvedImage, error) {
	if _, err := b.scriptedBuildBackend.BuildImage(ctx, invocation, emit); err != nil {
		return ResolvedImage{}, err
	}
	return b.image, nil
}

func TestBuildArtifactRecordsWhereTheBuildWent(t *testing.T) {
	t.Parallel()
	backend := &timedBuildBackend{
		scriptedBuildBackend: scriptedBuildBackend{lines: []string{
			"#9 [build 2/4] COPY package.json package-lock.json ./",
			"#9 CACHED",
			"#10 [build 3/4] RUN --mount=type=cache,id=npm,target=/root/.npm npm ci",
			"#10 DONE 6.2s",
			"#11 [build 4/4] RUN --mount=type=secret,id=DATABASE_URL,env=DATABASE_URL npm run build",
			"#11 DONE 25.1s",
			"#12 exporting to image",
			"#12 exporting layers 45.2s done",
			"#12 unpacking to docker.io/library/app:candidate 15.4s done",
			"#12 DONE 61.0s",
		}},
		image: ResolvedImage{
			Reference: "just-dashboard/app:candidate", Digest: fakeContentDigest("app"),
			OS: "linux", Architecture: "amd64", SizeBytes: 865_000_000,
		},
	}
	executor, execution, plan, _ := failedBuildFixture(t, backend, map[string]any{"recipe": "node"})
	result := executor.buildArtifact(context.Background(), execution, plan)
	if result.State != StepPassed {
		t.Fatalf("result = %+v", result)
	}
	var evidence builtStepEvidence
	if err := json.Unmarshal(result.Evidence, &evidence); err != nil {
		t.Fatal(err)
	}
	want := BuildTiming{
		InstallSeconds: 6.2, CompileSeconds: 25.1, ExportSeconds: 45.2, UnpackSeconds: 15.4,
		ImageSeconds: 61, Steps: 3, CachedSteps: 1, ImageBytes: 865_000_000,
	}
	if evidence.Timing == nil || *evidence.Timing != want {
		t.Fatalf("timing = %+v, want %+v", evidence.Timing, want)
	}
}
