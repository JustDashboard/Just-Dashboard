package deploy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// Railpack (https://railpack.com) is what Railway and Coolify build a
// repository with when it has no Dockerfile. It is a build method beside the
// recipes rather than one of them: Railpack's own detection reads the
// checkout and writes a build plan, and BuildKit builds that plan through
// Railpack's frontend image, so the image lands in the daemon's store under
// the run's tag exactly as a recipe's does, and everything after the build —
// readiness, replay, canary, activation, rollback, previews, promotion — is
// the same.
//
// The planner is the railpack binary in that same frontend image, run as a
// one-shot container (railpackPrepareSpec): the checkout read-only, no
// capabilities, a read-only root, and a writable folder for the plan and its
// info. It needs the network: it downloads its pinned mise release and asks
// it which release each language resolves to. Nothing of Railpack is linked
// into the dashboard; its plan is JSON read here as data.

// RailpackDefaultVersion is the release a plan that names none builds with.
const RailpackDefaultVersion = "0.43.0"

// railpackReleases are the reviewed releases. The frontend image is also the
// CLI, and both are pinned by tag and by the index digest, so the tag cannot
// move under a build; Railpack's docs ask for the frontend of the release
// that wrote the plan.
var railpackReleases = map[string]string{
	"0.43.0": "ghcr.io/railwayapp/railpack-frontend:v0.43.0@sha256:355ef61fae6f2551103cd8b10c710cb07167dd353d561f15c69b8d7ac1c676cc",
}

// railpackProviders are the providers Railpack 0.43 has: the values the
// provider setting, like railpack.json's own provider field, takes.
var railpackProviders = []string{
	"cpp", "deno", "dotnet", "elixir", "gleam", "golang", "java", "node", "php", "python", "ruby", "rust",
	"shell", "staticfile", "zig",
}

// railpackRecipeProviders is the Railpack provider that builds what each
// recipe builds, which detection records on a recipe candidate as the
// Railpack choice beside it. The site generators, sbt, Leiningen and Dart
// have none.
var railpackRecipeProviders = map[string]string{
	"node": "node", "python": "python", "go": "golang", "rust": "rust", "java": "java", "dotnet": "dotnet",
	"deno": "deno", "php": "php", "ruby": "ruby", "elixir": "elixir", "gleam": "gleam",
}

// railpackEcosystemProviders are the languages detection names without a
// recipe that Railpack has a provider for: there Railpack is the fallback.
var railpackEcosystemProviders = map[string]string{"cpp": "cpp", "zig": "zig"}

// railpackExcludes keep the dashboard's own files — the plan itself, the
// generated configuration, buildx's metadata file — and the repository's
// history out of the image, as a recipe's ignore file does.
var railpackExcludes = []string{".git", ".just-dashboard", ".just-dashboard-build-metadata-*"}

const (
	railpackPlanPath        = ".just-dashboard/railpack-plan.json"
	railpackGeneratedConfig = ".just-dashboard/railpack.json"
	railpackDefaultConfig   = "railpack.json"
	railpackPlanLimit       = 1 << 20
	railpackInfoLimit       = 1 << 20
	railpackConfigLimit     = 256 << 10
	railpackPreviewLimit    = 64 << 10
	railpackOutputLines     = 120
	railpackPrepareTimeout  = 5 * time.Minute
	// railpackRoleLabel names a planner container, so one a stopped
	// dashboard left behind is found and removed.
	railpackRoleLabel = "io.just-dashboard.railpack"
)

// railpackRelease is the version and pinned image a plan's setting selects.
func railpackRelease(version string) (string, string, error) {
	if version == "" {
		version = RailpackDefaultVersion
	}
	image, ok := railpackReleases[version]
	if !ok {
		return "", "", fmt.Errorf("Railpack %s is not a release this dashboard has reviewed", version)
	}
	return version, image, nil
}

func railpackVersionList() string {
	versions := make([]string, 0, len(railpackReleases))
	for version := range railpackReleases {
		versions = append(versions, version)
	}
	sort.Strings(versions)
	return strings.Join(versions, ", ")
}

// validateRailpackSettings holds a plan's Railpack fields to the method that
// reads them. canonicalConfiguration clears them for any other method, so
// switching builder in a form never leaves a plan this refuses.
func validateRailpackSettings(build BuildPlanConfig) error {
	if build.Method != BuildRailpack {
		if build.RailpackVersion != "" || build.RailpackProvider != "" || build.RailpackConfig != "" {
			return invalidField("build.method", "the Railpack version, provider and configuration file apply to a Railpack build")
		}
		return nil
	}
	if _, _, err := railpackRelease(build.RailpackVersion); err != nil {
		return invalidField("build.railpackVersion", "%v; leave it empty for %s (reviewed: %s)", err, RailpackDefaultVersion, railpackVersionList())
	}
	if build.RailpackProvider != "" && !slices.Contains(railpackProviders, build.RailpackProvider) {
		return invalidField("build.railpackProvider", "Railpack's provider is one of %s, or empty to let Railpack detect it", strings.Join(railpackProviders, ", "))
	}
	if path := build.RailpackConfig; path != "" {
		clean := filepath.ToSlash(filepath.Clean(path))
		if len(path) > 256 || !safeRelativePath(path) || !strings.HasSuffix(clean, ".json") ||
			clean == ".just-dashboard" || strings.HasPrefix(clean, ".just-dashboard/") {
			return invalidField("build.railpackConfig", "the Railpack configuration file is a .json file inside the root directory, such as railpack.json")
		}
	}
	if build.OutputDirectory != "" || build.SPAFallback {
		return invalidField("build.outputDirectory", "Railpack serves a static site's output itself; set RAILPACK_SPA_OUTPUT_DIR as a build variable instead")
	}
	return nil
}

// RailpackEvidence is what Railpack decided for a run, read from its info
// and its plan: the run page shows it, and the release keeps it. Every text
// is bounded and screened like the rest of a build's evidence.
type RailpackEvidence struct {
	Version string `json:"version"`
	Image   string `json:"image"`
	// Provider is the plan's own setting; Providers what Railpack detected.
	Provider  string   `json:"provider,omitempty"`
	Providers []string `json:"providers"`
	// ConfigFile is the repository's configuration file Railpack read, or
	// the generated one that adds the provider setting to it.
	ConfigFile   string            `json:"configFile,omitempty"`
	Packages     []RailpackPackage `json:"packages"`
	Steps        []RailpackStep    `json:"steps"`
	StartCommand string            `json:"startCommand,omitempty"`
	// Images are the images the plan builds from, as Railpack names them:
	// its builder and runtime, tagged by the mise release they carry.
	Images []string `json:"images"`
	// Secrets are the build variables, by name, the plan mounts in every
	// command it runs.
	Secrets []string          `json:"secrets,omitempty"`
	Facts   map[string]string `json:"facts,omitempty"`
	Logs    []RailpackLog     `json:"logs,omitempty"`
	Success bool              `json:"success"`
}

type RailpackPackage struct {
	Name      string `json:"name"`
	Requested string `json:"requested,omitempty"`
	Resolved  string `json:"resolved,omitempty"`
	Source    string `json:"source,omitempty"`
}

type RailpackStep struct {
	Name     string   `json:"name"`
	Commands []string `json:"commands"`
}

type RailpackLog struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

// RailpackPrepareError is a planner run that wrote no plan: Railpack's own
// verdict, with what it printed and what its info says.
type RailpackPrepareError struct {
	ExitCode int
	// Transient is Railpack's exit 75: a download that failed, worth a retry.
	Transient bool
	Evidence  *RailpackEvidence
	Output    []string
}

func (e *RailpackPrepareError) Error() string {
	if message := e.firstError(); message != "" {
		return "Railpack could not plan the build: " + firstLine(message)
	}
	return fmt.Sprintf("railpack prepare exited with code %d", e.ExitCode)
}

func (e *RailpackPrepareError) Unwrap() error {
	if e.Transient {
		return ErrBuilderUnavailable
	}
	return ErrUnsupportedBuilder
}

func (e *RailpackPrepareError) firstError() string {
	if e.Evidence != nil {
		for _, log := range e.Evidence.Logs {
			if log.Level == "error" {
				return log.Message
			}
		}
	}
	return ""
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(line)
}

// RailpackRunner runs Railpack's planner over a build root and returns what
// it wrote. DockerArtifactBackend is the one on a real daemon.
type RailpackRunner interface {
	PlanRailpack(ctx context.Context, request RailpackPlanRequest) (RailpackPlanResult, error)
}

type RailpackPlanRequest struct {
	Image string
	// Source is the build root on this server, mounted read-only at /app.
	Source string
	// Args follow `railpack prepare /app`.
	Args []string
}

type RailpackPlanResult struct {
	ExitCode   int
	Plan, Info []byte
	Output     []string
}

// railpackPrepareArgs are the planner's flags: where it writes, that a plan
// without a start command is refused rather than built into an image that
// cannot start, the plan's own commands, the configuration file, and the
// plain RAILPACK_* build variables that configure the plan the way they do
// on Railway (RAILPACK_PACKAGES, RAILPACK_BUILD_APT_PACKAGES, …).
func railpackPrepareArgs(build BuildPlanConfig, configFile string, values map[string]string) []string {
	args := []string{
		"--plan-out", "/out/" + filepath.Base(railpackPlanPath),
		"--info-out", "/out/railpack-info.json",
		"--error-missing-start",
	}
	if command := strings.TrimSpace(build.BuildCommand); command != "" {
		args = append(args, "--build-cmd", command)
	}
	if command := strings.TrimSpace(build.StartCommand); command != "" {
		args = append(args, "--start-cmd", command)
	}
	if configFile != "" {
		args = append(args, "--config-file", configFile)
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		args = append(args, "--env", name+"="+values[name])
	}
	return args
}

// railpackPlanValues are the build variables the planner is given values
// of: plain ones named RAILPACK_*, which configure the plan. Every other
// build variable reaches Railpack by name only — the plan lists it, and the
// build mounts its value as a BuildKit secret — and RAILPACK_CONFIG_FILE is
// the configuration-file setting's, so it is not taken from a variable.
func railpackPlanValues(values map[string]string, secret map[string]bool) map[string]string {
	plan := map[string]string{}
	for name, value := range values {
		if !strings.HasPrefix(name, "RAILPACK_") || name == "RAILPACK_CONFIG_FILE" || secret[name] ||
			strings.ContainsAny(value, "\x00\r\n") || rejectPlanSecretLiteral("build variable", value) != nil {
			continue
		}
		plan[name] = value
	}
	return plan
}

// railpackConfiguration is the configuration Railpack reads: the
// repository's own file (railpack.json unless the plan names another), and,
// when the plan sets a provider, a copy of it with the provider added, which
// Railpack is pointed at instead. Railpack has no flag for the provider; its
// configuration file is where it reads one.
func railpackConfiguration(root string, build BuildPlanConfig) (flag, file, generated string, err error) {
	path := railpackDefaultConfig
	if build.RailpackConfig != "" {
		path = filepath.ToSlash(filepath.Clean(build.RailpackConfig))
	}
	content, readErr := readContainedRegular(root, path, railpackConfigLimit)
	present := readErr == nil
	if !present && build.RailpackConfig != "" {
		return "", "", "", fmt.Errorf("%w: the Railpack configuration file %s is not a regular file in the root directory", ErrUnsupportedBuilder, path)
	}
	if build.RailpackProvider == "" {
		if !present {
			return "", "", "", nil
		}
		if build.RailpackConfig != "" {
			flag = path
		}
		return flag, path, "", nil
	}
	document := map[string]json.RawMessage{}
	if present {
		if err := json.Unmarshal(content, &document); err != nil || document == nil {
			return "", "", "", fmt.Errorf("%w: the Railpack configuration file %s is not a plain JSON object, so the provider setting cannot be added to it; set \"provider\" in the file instead", ErrUnsupportedBuilder, path)
		}
	}
	document["provider"] = mustJSON(build.RailpackProvider)
	encoded, _ := json.MarshalIndent(document, "", "  ")
	file = railpackGeneratedConfig
	if present {
		file = path + " with the provider setting"
	}
	return railpackGeneratedConfig, file, string(encoded) + "\n", nil
}

// prepareRailpack runs the planner over root, writes the plan the build reads
// and records what Railpack decided. A dry run checks only the plan's own
// files and runs nothing.
func (b *ArtifactBuilder) prepareRailpack(ctx context.Context, root string, config BuildPlanConfig, prepared *PreparedBuild, names []string) error {
	version, image, err := railpackRelease(config.RailpackVersion)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsupportedBuilder, err)
	}
	evidence := &RailpackEvidence{
		Version: version, Image: image, Provider: config.RailpackProvider,
		Providers: []string{}, Packages: []RailpackPackage{}, Steps: []RailpackStep{}, Images: []string{},
	}
	flag, file, generated, err := railpackConfiguration(root, config)
	if err != nil {
		return err
	}
	evidence.ConfigFile = file
	prepared.Railpack = evidence
	prepared.Dockerfile = railpackPlanPath
	if b.dryRun {
		return nil
	}
	runner, ok := b.backend.(RailpackRunner)
	if !ok {
		return fmt.Errorf("%w: this builder cannot run Railpack's planner", ErrBuilderUnavailable)
	}
	if generated != "" {
		if err := b.writeGenerated(root, filepath.Base(railpackGeneratedConfig), generated); err != nil {
			return err
		}
	}
	planCtx, cancel := context.WithTimeout(ctx, railpackPrepareTimeout)
	defer cancel()
	result, err := runner.PlanRailpack(planCtx, RailpackPlanRequest{
		Image: image, Source: root, Args: railpackPrepareArgs(config, flag, b.railpackValues),
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: Railpack's planner did not run: %v", ErrBuilderUnavailable, err)
	}
	output := railpackOutput(result.Output)
	if info, err := parseRailpackInfo(result.Info); err == nil {
		applyRailpackInfo(evidence, info)
	}
	if result.ExitCode != 0 || !evidence.Success || len(bytes.TrimSpace(result.Plan)) == 0 {
		evidence.Success = false
		return &RailpackPrepareError{ExitCode: result.ExitCode, Transient: result.ExitCode == 75, Evidence: evidence, Output: output}
	}
	plan, view, err := railpackPlanForBuild(result.Plan, names)
	if err != nil {
		return err
	}
	applyRailpackPlan(evidence, view)
	if missing := railpackMissingSecrets(view.Secrets, names); len(missing) > 0 {
		return fmt.Errorf("%w: Railpack's plan reads %s at build time, which this environment does not have as a build variable; add %s with build scope or remove it from %s",
			ErrUnsupportedBuilder, strings.Join(missing, ", "), pluralIt(len(missing)), orDefault(file, "railpack.json"))
	}
	if err := b.writeGenerated(root, filepath.Base(railpackPlanPath), string(plan)); err != nil {
		return err
	}
	prepared.DockerfileDigest = digestBytes(plan)
	if len(plan) <= railpackPreviewLimit {
		prepared.DockerfilePreview = string(plan)
	}
	prepared.Toolchain = railpackToolchain(evidence)
	prepared.Notes = append(prepared.Notes, output...)
	if len(evidence.Secrets) > 0 {
		prepared.SecretLayerGuarantee = "buildkit_ephemeral_mount"
	}
	return nil
}

func pluralIt(count int) string {
	if count == 1 {
		return "it"
	}
	return "them"
}

// railpackToolchain names the build the way the recipes name theirs: the
// language Railpack installs first, then Railpack itself.
func railpackToolchain(evidence *RailpackEvidence) string {
	label := "railpack " + evidence.Version
	for _, provider := range evidence.Providers {
		for _, pkg := range evidence.Packages {
			if railpackPackageProvider(pkg.Name) == provider && pkg.Resolved != "" {
				return pkg.Name + " " + pkg.Resolved + " · " + label
			}
		}
	}
	if len(evidence.Packages) > 0 && evidence.Packages[0].Resolved != "" {
		return evidence.Packages[0].Name + " " + evidence.Packages[0].Resolved + " · " + label
	}
	return label
}

func railpackPackageProvider(name string) string {
	switch name {
	case "go":
		return "golang"
	case "bun":
		return "node"
	}
	return name
}

type railpackInfo struct {
	RailpackVersion  string `json:"railpackVersion"`
	ResolvedPackages map[string]struct {
		Name             string `json:"name"`
		RequestedVersion string `json:"requestedVersion"`
		ResolvedVersion  string `json:"resolvedVersion"`
		Source           string `json:"source"`
	} `json:"resolvedPackages"`
	Metadata          map[string]string `json:"metadata"`
	DetectedProviders []string          `json:"detectedProviders"`
	Logs              []struct {
		Level string `json:"level"`
		Msg   string `json:"msg"`
	} `json:"logs"`
	Success bool `json:"success"`
}

func parseRailpackInfo(raw []byte) (railpackInfo, error) {
	var info railpackInfo
	if len(raw) == 0 || len(raw) > railpackInfoLimit {
		return info, errors.New("no Railpack info")
	}
	err := json.Unmarshal(raw, &info)
	return info, err
}

const (
	railpackNameLength    = 64
	railpackTextLength    = 256
	railpackMessageLength = 600
)

var railpackNameRE = regexp.MustCompile(`^[A-Za-z0-9@._:+/-]{1,64}$`)

func applyRailpackInfo(evidence *RailpackEvidence, info railpackInfo) {
	evidence.Success = info.Success
	for _, provider := range info.DetectedProviders {
		if railpackNameRE.MatchString(provider) && len(evidence.Providers) < 8 && !slices.Contains(evidence.Providers, provider) {
			evidence.Providers = append(evidence.Providers, provider)
		}
	}
	names := make([]string, 0, len(info.ResolvedPackages))
	for name := range info.ResolvedPackages {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, key := range names {
		pkg := info.ResolvedPackages[key]
		name := orDefault(pkg.Name, key)
		if !railpackNameRE.MatchString(name) || len(evidence.Packages) >= 32 {
			continue
		}
		evidence.Packages = append(evidence.Packages, RailpackPackage{
			Name: name, Requested: railpackText(pkg.RequestedVersion, railpackNameLength),
			Resolved: railpackText(pkg.ResolvedVersion, railpackNameLength), Source: railpackText(pkg.Source, railpackNameLength),
		})
	}
	keys := make([]string, 0, len(info.Metadata))
	for key := range info.Metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !railpackNameRE.MatchString(key) || len(evidence.Facts) >= 16 {
			continue
		}
		if value := railpackText(info.Metadata[key], railpackNameLength); value != "" {
			if evidence.Facts == nil {
				evidence.Facts = map[string]string{}
			}
			evidence.Facts[key] = value
		}
	}
	for _, log := range info.Logs {
		level := strings.ToLower(strings.TrimSpace(log.Level))
		switch level {
		case "error", "warn", "info", "suggestion":
		default:
			continue
		}
		message := railpackText(log.Msg, railpackMessageLength)
		if message == "" || len(evidence.Logs) >= 24 {
			continue
		}
		evidence.Logs = append(evidence.Logs, RailpackLog{Level: level, Message: message})
	}
}

// railpackText bounds a text Railpack wrote: terminal escapes and control
// characters out (a newline kept), cut at limit, and withheld when it looks
// like credential material.
func railpackText(value string, limit int) string {
	value = terminalEscapeRE.ReplaceAllString(value, "")
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	if len(value) > limit {
		value = strings.TrimSpace(truncateUTF8Prefix(value, limit-3)) + "..."
	}
	if rejectPlanSecretLiteral("Railpack output", value) != nil {
		return "(withheld: it resembles credential material)"
	}
	return value
}

// railpackPlanView is the part of a plan the evidence reads. Everything else
// is the frontend's, and passes through untouched.
type railpackPlanView struct {
	Steps []struct {
		Name     string `json:"name"`
		Commands []struct {
			Cmd        string `json:"cmd"`
			CustomName string `json:"customName"`
		} `json:"commands"`
		Inputs []struct {
			Image string `json:"image"`
		} `json:"inputs"`
	} `json:"steps"`
	Deploy struct {
		StartCommand string `json:"startCommand"`
		Base         struct {
			Image string `json:"image"`
		} `json:"base"`
		Inputs []struct {
			Image string `json:"image"`
		} `json:"inputs"`
	} `json:"deploy"`
	Secrets []string `json:"secrets"`
}

// railpackPlanForBuild is the plan as the build reads it: Railpack's own,
// with every build variable named among its secrets — the frontend mounts
// exactly those, and BuildKit is handed their values — and the dashboard's
// files and the repository's history among its excludes.
func railpackPlanForBuild(raw []byte, names []string) ([]byte, railpackPlanView, error) {
	var view railpackPlanView
	if len(raw) > railpackPlanLimit {
		return nil, view, fmt.Errorf("%w: Railpack's plan is larger than %d KiB", ErrUnsupportedBuilder, railpackPlanLimit>>10)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	document := map[string]any{}
	if err := decoder.Decode(&document); err != nil {
		return nil, view, fmt.Errorf("%w: Railpack wrote a plan that is not a JSON object: %v", ErrUnsupportedBuilder, err)
	}
	secrets := []string{}
	if listed, ok := document["secrets"].([]any); ok {
		for _, item := range listed {
			if name, ok := item.(string); ok && ValidateEnvKey(name) == nil && !slices.Contains(secrets, name) {
				secrets = append(secrets, name)
			}
		}
	}
	for _, name := range names {
		if ValidateEnvKey(name) == nil && !slices.Contains(secrets, name) {
			secrets = append(secrets, name)
		}
	}
	sort.Strings(secrets)
	document["secrets"] = secrets
	excludes := []any{}
	if listed, ok := document["exclude"].([]any); ok {
		excludes = listed
	}
	for _, pattern := range railpackExcludes {
		if !slices.Contains(excludes, any(pattern)) {
			excludes = append(excludes, pattern)
		}
	}
	document["exclude"] = excludes
	plan, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, view, err
	}
	if err := json.Unmarshal(plan, &view); err != nil {
		return nil, view, fmt.Errorf("%w: Railpack's plan has an unexpected shape: %v", ErrUnsupportedBuilder, err)
	}
	return append(plan, '\n'), view, nil
}

func applyRailpackPlan(evidence *RailpackEvidence, view railpackPlanView) {
	addImage := func(image string) {
		image = strings.TrimSpace(image)
		if image != "" && len(image) <= railpackTextLength && !strings.ContainsAny(image, " \t\r\n") &&
			len(evidence.Images) < 16 && !slices.Contains(evidence.Images, image) {
			evidence.Images = append(evidence.Images, image)
		}
	}
	for _, step := range view.Steps {
		for _, input := range step.Inputs {
			addImage(input.Image)
		}
		if len(evidence.Steps) >= 32 {
			continue
		}
		recorded := RailpackStep{Name: railpackText(step.Name, railpackNameLength), Commands: []string{}}
		for _, command := range step.Commands {
			label := command.CustomName
			if label == "" {
				label = command.Cmd
			}
			if label = railpackText(label, railpackTextLength); label != "" && len(recorded.Commands) < 16 {
				recorded.Commands = append(recorded.Commands, label)
			}
		}
		if recorded.Name != "" {
			evidence.Steps = append(evidence.Steps, recorded)
		}
	}
	addImage(view.Deploy.Base.Image)
	for _, input := range view.Deploy.Inputs {
		addImage(input.Image)
	}
	evidence.StartCommand = railpackText(view.Deploy.StartCommand, railpackTextLength)
	for _, name := range view.Secrets {
		if ValidateEnvKey(name) == nil && len(evidence.Secrets) < 64 {
			evidence.Secrets = append(evidence.Secrets, name)
		}
	}
}

// railpackMissingSecrets are the secrets the plan mounts that the run has no
// build variable for — a railpack.json's own secrets list — which BuildKit
// would refuse as a secret that is not there, in its words rather than ours.
func railpackMissingSecrets(secrets, names []string) []string {
	missing := []string{}
	for _, name := range secrets {
		if !slices.Contains(names, name) {
			missing = append(missing, name)
		}
	}
	return missing
}

// railpackOutput is what the planner printed, for the transcript: terminal
// escapes out, blank runs collapsed, bounded.
func railpackOutput(lines []string) []string {
	output := []string{}
	blank := false
	for _, line := range lines {
		line = strings.TrimRight(railpackText(line, railpackMessageLength), " ")
		if strings.TrimSpace(line) == "" {
			if blank || len(output) == 0 {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		output = append(output, "Railpack: "+line)
		if len(output) >= railpackOutputLines {
			break
		}
	}
	for len(output) > 0 && strings.TrimSpace(strings.TrimPrefix(output[len(output)-1], "Railpack:")) == "" {
		output = output[:len(output)-1]
	}
	return output
}

// railpackCacheKey isolates a deployment's mount caches (npm's, pip's, apt's)
// from every other project's on this daemon: the release tag's repository,
// "deployment-<environment>".
func railpackCacheKey(tag string) string {
	repository, _, _ := strings.Cut(tag, ":")
	repository = repository[strings.LastIndex(repository, "/")+1:]
	var key strings.Builder
	for _, r := range strings.ToLower(repository) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			key.WriteRune(r)
		} else {
			key.WriteByte('-')
		}
	}
	value := strings.Trim(key.String(), ".-_")
	if value == "" {
		return "deployment"
	}
	if len(value) > 128 {
		value = value[:128]
	}
	return value
}

// railpackSecretsHash changes when any build value does, so the frontend
// rebuilds a step that read the old one instead of reusing its layer; it is
// the hash Railpack's own CLI computes, over the sorted NAME=value lines.
func railpackSecretsHash(secrets []BuildSecretValue) string {
	if len(secrets) == 0 {
		return ""
	}
	lines := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		lines = append(lines, secret.ID+"="+secret.Value)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// railpackPrepareSpec is the planner's container: the railpack binary of the
// pinned frontend image, reading the build root read-only and writing only
// its plan and info, with no capabilities, no new privileges and a read-only
// root. It runs as root without capabilities because the checkout is a
// private 0700 tree owned by root, which nobody else may traverse; its
// scratch is a tmpfs that may execute, because Railpack downloads its pinned
// mise release there and runs it to resolve each language's release. That
// download and those lookups are the network it has, on Docker's bridge.
func railpackPrepareSpec(request RailpackPlanRequest, out, suffix string) dockerx.ContainerSpec {
	return dockerx.ContainerSpec{
		Name:       "jd-railpack-prepare-" + suffix,
		Image:      request.Image,
		Entrypoint: []string{"/railpack"},
		Command:    append([]string{"prepare", "/app"}, request.Args...),
		Env: []dockerx.EnvVar{
			{Name: "HOME", Value: "/tmp"}, {Name: "TMPDIR", Value: "/tmp"}, {Name: "NO_COLOR", Value: "1"},
		},
		User:            "0:0",
		WorkingDir:      "/app",
		NetworkMode:     "bridge",
		ReadOnly:        true,
		CapDrop:         []string{"ALL"},
		NoNewPrivileges: true,
		Mounts: []dockerx.MountSpec{
			{Type: "bind", Source: request.Source, Target: "/app", ReadOnly: true},
			{Type: "bind", Source: out, Target: "/out"},
			{Type: "tmpfs", Target: "/tmp", SizeMB: 768, Exec: true},
		},
		Labels:  []dockerx.LabelSpec{{Name: railpackRoleLabel, Value: "prepare"}},
		Limits:  dockerx.ResourceLimits{MemoryMB: 1024, MemorySwapMB: 1024, CPUs: 1, PidsLimit: 256},
		Logging: dockerx.CappedLogging(),
	}
}

// PlanRailpack runs the planner on the daemon: a folder of its own for the
// plan and info, the container to completion, and both files read back
// bounded before the folder is removed.
func (b *DockerArtifactBackend) PlanRailpack(ctx context.Context, request RailpackPlanRequest) (RailpackPlanResult, error) {
	if b == nil || b.docker == nil {
		return RailpackPlanResult{}, ErrBuilderUnavailable
	}
	b.sweepRailpack.Do(func() { b.removeLeftoverPlanners(ctx) })
	workRoot := b.railpackDir
	if workRoot == "" {
		workRoot = filepath.Join(os.TempDir(), "just-dashboard-railpack")
	}
	if err := os.MkdirAll(workRoot, 0o700); err != nil {
		return RailpackPlanResult{}, err
	}
	out, err := os.MkdirTemp(workRoot, "prepare-")
	if err != nil {
		return RailpackPlanResult{}, err
	}
	defer os.RemoveAll(out)
	lines := make(chan dockerx.LogLine, 64)
	var mu sync.Mutex
	output := []string{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for line := range lines {
			mu.Lock()
			output = append(output, strings.TrimRight(line.Text, "\r"))
			if len(output) > 2*railpackOutputLines {
				output = output[len(output)-2*railpackOutputLines:]
			}
			mu.Unlock()
		}
	}()
	code, removed, err := b.docker.RunToCompletion(ctx, railpackPrepareSpec(request, out, rand.Text()[:12]), lines)
	close(lines)
	<-done
	if err != nil {
		return RailpackPlanResult{}, err
	}
	if !removed {
		return RailpackPlanResult{}, errors.New("the Railpack planner's container could not be removed")
	}
	result := RailpackPlanResult{ExitCode: code}
	mu.Lock()
	result.Output = append([]string(nil), output...)
	mu.Unlock()
	result.Plan, _ = readBoundedFile(filepath.Join(out, filepath.Base(railpackPlanPath)), railpackPlanLimit)
	result.Info, _ = readBoundedFile(filepath.Join(out, "railpack-info.json"), railpackInfoLimit)
	return result, nil
}

// removeLeftoverPlanners removes planner containers a stopped dashboard left
// behind, once per process, before its first plan.
func (b *DockerArtifactBackend) removeLeftoverPlanners(ctx context.Context) {
	containers, err := b.docker.ListContainersWithLabels(ctx, map[string]string{railpackRoleLabel: "prepare"})
	if err != nil {
		return
	}
	for _, item := range containers {
		_ = b.docker.RemoveContainer(ctx, item.ID, true, true)
	}
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("larger than %d KiB", limit>>10)
	}
	return raw, nil
}

// railpackPrepareCause names a plan Railpack refused, from its info's error
// line, as a build failure is named from its output (build_output_remedy.go
// has the sentence and the fix).
func railpackPrepareCause(failure *RailpackPrepareError, context causeContext) *BuildCause {
	cause := &BuildCause{
		Code: "railpack_plan_failed", Phase: phasePlan, Command: "railpack prepare",
		ExitCode: failure.ExitCode, Detail: "railpack",
	}
	message := failure.firstError()
	lower := strings.ToLower(message)
	switch {
	case failure.Transient:
		cause.Code = "railpack_unavailable"
	case strings.Contains(lower, "could not determine how to build"):
		cause.Code = "railpack_no_provider"
	case strings.Contains(lower, "no start command"):
		cause.Code = "railpack_start_command_missing"
	case railpackUnresolvedRE.MatchString(message):
		groups := railpackUnresolvedRE.FindStringSubmatch(message)
		cause.Code = "railpack_package_unresolved"
		cause.Subjects = addCauseSubject(cause.Subjects, groups[2])
		cause.Subjects = addCauseSubject(cause.Subjects, groups[1])
	case strings.Contains(lower, "config file") || strings.Contains(lower, "as json"):
		cause.Code = "railpack_config_invalid"
	}
	cause.Fix = buildCauseFix(cause, context)
	return cause
}

// "failed to resolve version 99.1.0 of node", in mise's lookup through Railpack.
var railpackUnresolvedRE = regexp.MustCompile(`(?i)failed to resolve version (\S+) of ([\w.+@/-]+)`)

// railpackSecretNames lists the run's build variables that reach a Railpack
// build, for the bindings: every one, since its plan mounts every secret in
// every command it runs.
func railpackSecretNames(bindings []BuildSecretConfig) []string {
	names := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		names = append(names, binding.Variable)
	}
	return names
}

// railpackPreviewArgs are the frontend's arguments as the build preview shows
// them; the secrets hash is in buildx's environment and never shown.
func railpackPreviewArgs(prepared PreparedBuild, tag string) []string {
	if prepared.Railpack == nil {
		return nil
	}
	return []string{
		"--build-arg", "BUILDKIT_SYNTAX=" + prepared.Railpack.Image,
		"--build-arg", "cache-key=" + railpackCacheKey(tag),
	}
}

// railpackFrontendFor is the frontend a prepared Railpack build is built
// through.
func railpackFrontendFor(prepared PreparedBuild, tag string, secrets []BuildSecretValue) *BuildFrontend {
	if prepared.Method != BuildRailpack || prepared.Railpack == nil {
		return nil
	}
	return &BuildFrontend{
		Image: prepared.Railpack.Image, CacheKey: railpackCacheKey(tag), SecretsHash: railpackSecretsHash(secrets),
	}
}

// railpackLabel is a provider's name as a reader knows it.
func railpackLabel(provider string) string {
	switch provider {
	case "golang":
		return "Go"
	case "cpp":
		return "C/C++"
	case "dotnet":
		return ".NET"
	case "php":
		return "PHP"
	case "staticfile":
		return "Staticfile"
	}
	if provider == "" {
		return ""
	}
	return strings.ToUpper(provider[:1]) + provider[1:]
}

// refuseRailpackWithoutCheckout keeps Railpack to sources with a checkout to
// read. A Compose source has its own files, which decide its build, and an
// image, a template or an imported workload is not built here at all.
func refuseRailpackWithoutCheckout(kind SourceKind, build BuildPlanConfig) error {
	if build.Method != BuildRailpack {
		return nil
	}
	switch kind {
	case SourceCompose, SourceImage, SourceBlueprint, SourceImport:
		return invalidField("build.method", "Railpack builds a repository checkout; a %s source is not one, and its own files decide how it builds", kind)
	}
	return nil
}
