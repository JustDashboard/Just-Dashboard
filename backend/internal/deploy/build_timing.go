package deploy

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// BuildTiming is where a build's time went, read from BuildKit's own plain
// progress as it streams: how long the dependency install and the build
// command ran, how long the image took to export and unpack, how many of the
// Dockerfile's steps the cache answered, and how big the image is. The run
// page says it in one sentence on the Build step ("install 6 s, compile 25 s,
// image export 61 s for 865 MB; 9 of 14 steps cached").
//
// A figure is BuildKit's own (`#13 DONE 5.5s`, `exporting layers 45.2s done`),
// not a clock of ours, so it is what the builder spent on that work. A step
// the cache answered has no seconds and is counted as cached; a build with
// several buildx runs (a Compose stack's services) adds them up.
type BuildTiming struct {
	// InstallSeconds is the dependency install's step, CompileSeconds the
	// build command's; zero when the cache answered it or there was none.
	InstallSeconds float64 `json:"installSeconds,omitempty"`
	InstallCached  bool    `json:"installCached,omitempty"`
	CompileSeconds float64 `json:"compileSeconds,omitempty"`
	CompileCached  bool    `json:"compileCached,omitempty"`
	// ExportSeconds is BuildKit compressing the image's layers ("exporting
	// layers"), UnpackSeconds the image store unpacking them again, and
	// ImageSeconds the whole "exporting to image" step around both.
	ExportSeconds float64 `json:"exportSeconds,omitempty"`
	UnpackSeconds float64 `json:"unpackSeconds,omitempty"`
	ImageSeconds  float64 `json:"imageSeconds,omitempty"`
	// Steps are the Dockerfile's own steps (`#9 [build 2/4] COPY . .`), not
	// BuildKit's bookkeeping (`[internal] load …`); CachedSteps those of them
	// the cache answered.
	Steps       int   `json:"steps"`
	CachedSteps int   `json:"cachedSteps"`
	ImageBytes  int64 `json:"imageBytes,omitempty"`
}

var (
	timingHeaderRE = regexp.MustCompile(`^#(\d+) \[([^\]]*)\] (.+)$`)
	timingDoneRE   = regexp.MustCompile(`^#(\d+) DONE (\d+(?:\.\d+)?)s\b`)
	timingCachedRE = regexp.MustCompile(`^#(\d+) CACHED\b`)
	// "#16 exporting to image", "#16 exporting to docker image format": the
	// step that writes the image, matched loosely because the exporter's
	// wording follows the driver and the build lane may change it.
	timingExportStepRE = regexp.MustCompile(`^#(\d+) exporting to\b`)
	// "#16 exporting layers 45.2s done", "#16 unpacking to … 15.4s done",
	// "#16 sending tarball 3.1s done": the parts of it worth naming.
	timingLayersRE = regexp.MustCompile(`^#\d+ (?:exporting layers|sending tarball)\b.*?(\d+(?:\.\d+)?)s done$`)
	timingUnpackRE = regexp.MustCompile(`^#\d+ unpacking\b.*?(\d+(?:\.\d+)?)s done$`)
	// The options of a RUN (`--mount=…`, `--network=…`) before its command.
	timingRunOptionsRE = regexp.MustCompile(`^RUN\s+(?:--\S+\s+)*`)
	// What a dependency install and a build command look like when the plan
	// did not name them: the package managers' and toolchains' own verbs.
	timingInstallRE = regexp.MustCompile(`\b(?:npm (?:ci|install)|pnpm (?:install|i)\b|yarn(?: install)?(?:\s|$)|bun install|pip3? install|poetry install|uv (?:sync|pip install)|pipenv (?:install|sync)|bundle install|composer install|go mod download|cargo fetch|mix deps\.get|dotnet restore)`)
	timingCompileRE = regexp.MustCompile(`\b(?:(?:npm|pnpm|yarn|bun) run build|(?:pnpm|yarn) build|next build|vite build|go build|cargo build|mix (?:compile|release)|dotnet publish|mvn\b.*\bpackage|gradle\w*\b.*\b(?:build|assemble|bootJar|installDist)|rails assets:precompile|rake assets:precompile)`)
)

type timingStepKind int

const (
	timingOther timingStepKind = iota
	timingInstall
	timingCompile
	timingImage
)

// buildTimingReader reads a build's transcript, a line at a time, into a
// BuildTiming. install and compile are the commands the plan runs, which
// name its steps better than any pattern; either may be empty.
type buildTimingReader struct {
	install, compile string
	timing           BuildTiming
	// Vertex numbers start again with every buildx run.
	kinds  map[int]timingStepKind
	steps  map[int]bool
	cached map[int]bool
	seen   bool
}

func newBuildTimingReader(install, compile string) *buildTimingReader {
	r := &buildTimingReader{
		install: strings.TrimSpace(install), compile: strings.TrimSpace(compile),
	}
	r.reset()
	return r
}

func (r *buildTimingReader) reset() {
	r.kinds, r.steps, r.cached = map[int]timingStepKind{}, map[int]bool{}, map[int]bool{}
}

func (r *buildTimingReader) observe(line BuildLog) {
	if line.Stream == "status" {
		// The engine's own sentences separate one buildx run from the next.
		r.reset()
		return
	}
	text := strings.TrimRight(line.Text, "\r\n ")
	if !strings.HasPrefix(text, "#") {
		return
	}
	if match := timingHeaderRE.FindStringSubmatch(text); match != nil {
		vertex, _ := strconv.Atoi(match[1])
		target := strings.TrimSpace(match[2])
		if target == "internal" || strings.HasPrefix(target, "internal ") {
			return
		}
		r.seen = true
		if !r.steps[vertex] {
			r.steps[vertex] = true
			r.timing.Steps++
		}
		r.kinds[vertex] = r.kindOf(match[3])
		return
	}
	if match := timingExportStepRE.FindStringSubmatch(text); match != nil {
		vertex, _ := strconv.Atoi(match[1])
		r.kinds[vertex] = timingImage
		r.seen = true
		return
	}
	if match := timingCachedRE.FindStringSubmatch(text); match != nil {
		vertex, _ := strconv.Atoi(match[1])
		if !r.steps[vertex] || r.cached[vertex] {
			return
		}
		r.cached[vertex] = true
		r.timing.CachedSteps++
		switch r.kinds[vertex] {
		case timingInstall:
			r.timing.InstallCached = true
		case timingCompile:
			r.timing.CompileCached = true
		}
		return
	}
	if match := timingDoneRE.FindStringSubmatch(text); match != nil {
		vertex, _ := strconv.Atoi(match[1])
		seconds, _ := strconv.ParseFloat(match[2], 64)
		switch r.kinds[vertex] {
		case timingInstall:
			r.timing.InstallSeconds += seconds
		case timingCompile:
			r.timing.CompileSeconds += seconds
		case timingImage:
			r.timing.ImageSeconds += seconds
		}
		return
	}
	if match := timingLayersRE.FindStringSubmatch(text); match != nil {
		seconds, _ := strconv.ParseFloat(match[1], 64)
		r.timing.ExportSeconds += seconds
		return
	}
	if match := timingUnpackRE.FindStringSubmatch(text); match != nil {
		seconds, _ := strconv.ParseFloat(match[1], 64)
		r.timing.UnpackSeconds += seconds
	}
}

// kindOf names a step by its instruction: a RUN of the plan's build command
// is the compile, one of its install the install. A RUN that does both is the
// compile, which is what most of its time is.
func (r *buildTimingReader) kindOf(instruction string) timingStepKind {
	if !strings.HasPrefix(instruction, "RUN ") {
		return timingOther
	}
	command := timingRunOptionsRE.ReplaceAllString(instruction, "")
	switch {
	case r.compile != "" && strings.Contains(command, r.compile):
		return timingCompile
	case r.install != "" && strings.Contains(command, r.install):
		return timingInstall
	case timingCompileRE.MatchString(command):
		return timingCompile
	case timingInstallRE.MatchString(command):
		return timingInstall
	}
	return timingOther
}

// summary is what was read, with the image's size; nil when the transcript
// held no BuildKit build at all (an image that was pulled, not built).
func (r *buildTimingReader) summary(imageBytes int64) *BuildTiming {
	if r == nil || !r.seen {
		return nil
	}
	timing := r.timing
	timing.ImageBytes = imageBytes
	for _, value := range []*float64{
		&timing.InstallSeconds, &timing.CompileSeconds, &timing.ExportSeconds,
		&timing.UnpackSeconds, &timing.ImageSeconds,
	} {
		*value = math.Round(*value*10) / 10
	}
	return &timing
}

// builtImageBytes is the size of what a build made: its image, or the sum of
// a Compose stack's service images.
func builtImageBytes(result BuildArtifactResult) int64 {
	if result.Image.SizeBytes > 0 {
		return result.Image.SizeBytes
	}
	var total int64
	for _, artifact := range result.Artifacts {
		if artifact.Kind == ArtifactImage {
			total += artifact.SizeBytes
		}
	}
	return total
}
