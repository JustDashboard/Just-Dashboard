package deploy

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

var (
	ErrUnsupportedBuilder = errors.New("unsupported deployment builder")
	ErrBuilderUnavailable = errors.New("deployment builder is unavailable")
	ErrArtifactMissing    = errors.New("deployment artifact is missing")
	ErrArtifactRetained   = errors.New("deployment artifact is retained")
)

const AutomaticRecipeVersion = "just-dashboard-recipes-v3"

// The catalogue is deliberately small and reviewed. Tags are never written
// into a release Dockerfile: the backend resolves each to a digest first.
// A Bun project builds and runs on the Node image with Bun's binary copied
// in: the Bun image's node is Bun itself, which silently made Bun the
// production runtime of every bun.lock project. The Node majors a
// repository can declare are node:<major> (Alpine) and node:<major>-glibc
// (Debian slim, for packages that ship only glibc binaries); a declared Bun
// release is copied from oven/bun:<release>-alpine or -slim the same way.
var recipeBaseCatalogue = map[string][]string{
	"node:npm":      {"node:22-alpine"},
	"node:pnpm":     {"node:22-alpine"},
	"node:yarn":     {"node:22-alpine"},
	"node:bun":      {"node:22-alpine", "oven/bun:1-alpine"},
	"node:20":       {"node:20-alpine"},
	"node:22":       {"node:22-alpine"},
	"node:24":       {"node:24-alpine"},
	"node:20-glibc": {"node:20-bookworm-slim"},
	"node:22-glibc": {"node:22-bookworm-slim"},
	"node:24-glibc": {"node:24-bookworm-slim"},
	"bun:glibc":     {"oven/bun:1-slim"},
	"go":            {"golang:1.27-alpine", "alpine:3.22"},
	"go:dynamic":    {"golang:1.27-alpine" + compiledDynamicAlpine, "alpine:" + compiledDynamicAlpine},
	"python":        {"python:3.13-slim"},
	"static":        {"nginx:1.29-alpine"},
	"rust":          {"rust:1-alpine", "alpine:3.22"},
	"rust:leptos":   {"rust:1-" + rustDebianRelease, "debian:" + rustDebianRelease + "-slim"},
	"rust:trunk":    {"rust:1-" + rustDebianRelease, "nginx:1.29-alpine"},
	"java:maven":    {"maven:3-eclipse-temurin-21", "eclipse-temurin:21-jre-alpine"},
	"java:gradle":   {"gradle:8-jdk21", "eclipse-temurin:21-jre-alpine"},
	"dotnet":        {"mcr.microsoft.com/dotnet/sdk:8.0", "mcr.microsoft.com/dotnet/aspnet:8.0"},
	"deno":          {"denoland/deno:alpine"},
	"php":           {"dunglas/frankenphp:1-php8.3-alpine"},
	"php:composer":  {"composer:2"},
}

type ResolvedImage struct {
	Reference    string   `json:"reference"`
	Digest       string   `json:"digest"`
	ConfigDigest string   `json:"configDigest,omitempty"`
	OS           string   `json:"os,omitempty"`
	Architecture string   `json:"architecture,omitempty"`
	Platforms    []string `json:"platforms"`
	SizeBytes    int64    `json:"sizeBytes,omitempty"`
}

type BuildLog struct {
	Stream string
	Text   string
}

type BuildInvocation struct {
	ContextDir string
	Dockerfile string
	Tag        string
	Platform   string
	NoCache    bool
	Pull       bool
	Secrets    []BuildSecretValue
	// Target is the Dockerfile stage to build; BuildArgs reach buildx as
	// `--build-arg NAME` with the value only in its process environment.
	Target    string
	BuildArgs []BuildArgValue
}

type BuildArgValue struct {
	Name  string
	Value string
}

type BuildSecretValue struct {
	ID    string
	Step  string
	Value string
}

type BuildBackend interface {
	ResolveImage(context.Context, string, string) (ResolvedImage, error)
	BuildImage(context.Context, BuildInvocation, func(BuildLog) error) (ResolvedImage, error)
	PullImage(context.Context, string, string, func(BuildLog) error) (ResolvedImage, error)
	InspectImage(context.Context, string) (ResolvedImage, error)
	RemoveImage(context.Context, string) error
}

type PreparedBuild struct {
	Method        BuildMethod `json:"method"`
	Recipe        string      `json:"recipe,omitempty"`
	RecipeVersion string      `json:"recipeVersion,omitempty"`
	GoVersion     string      `json:"goVersion,omitempty"`
	PythonVersion string      `json:"pythonVersion,omitempty"`
	// NodeVersion is the Node major a JavaScript build (or a PHP recipe's
	// asset stage) ran on and what chose it: "22 (.nvmrc)".
	NodeVersion string `json:"nodeVersion,omitempty"`
	// Toolchain names the language release the other recipes built with,
	// for the build evidence: "rust 1.85", "java 21 (maven)", "dotnet 8.0",
	// and for Node the package manager release: "pnpm 10.34.5 (lockfileVersion 9.0)".
	Toolchain            string                   `json:"toolchain,omitempty"`
	Dockerfile           string                   `json:"dockerfile,omitempty"`
	DockerfileDigest     string                   `json:"dockerfileDigest,omitempty"`
	DockerfilePreview    string                   `json:"dockerfilePreview,omitempty"`
	BuildArgv            []string                 `json:"buildArgv"`
	BaseImages           []ResolvedImage          `json:"baseImages"`
	TargetPlatform       string                   `json:"targetPlatform,omitempty"`
	CachePolicy          string                   `json:"cachePolicy"`
	SecretIDs            []string                 `json:"secretIds"`
	SecretBindings       []BuildSecretConfig      `json:"secretBindings,omitempty"`
	SecretLayerGuarantee string                   `json:"secretLayerGuarantee"`
	ComposeServices      []PreparedComposeService `json:"composeServices,omitempty"`

	// Target and BuildArgNames are the stage and the build arguments (by
	// name) a custom Dockerfile build receives (build_dockerfile_args.go).
	Target        string   `json:"target,omitempty"`
	BuildArgNames []string `json:"buildArgNames,omitempty"`
	// Install is the Node dependency install exactly as it runs, and Notes
	// the decisions preparation made that the run log states: a non-frozen
	// install, a command moved to the resolved manager's runner.
	Install string   `json:"install,omitempty"`
	Notes   []string `json:"notes,omitempty"`
	// BuildCommand and StartCommand are set when preparation ran a plan's
	// command through the resolved manager's runner instead of the saved one.
	BuildCommand string `json:"buildCommand,omitempty"`
	StartCommand string `json:"startCommand,omitempty"`
	// ContextDirectory is set when the build context is wider than the
	// build root — a workspace member installs from its workspace root — as
	// that root's path in the source ("." for its top).
	ContextDirectory string `json:"contextDirectory,omitempty"`
}

type PreparedComposeService struct {
	Name             string   `json:"name"`
	BuildContext     string   `json:"buildContext"`
	Dockerfile       string   `json:"dockerfile"`
	DockerfileDigest string   `json:"dockerfileDigest"`
	BuildArgv        []string `json:"buildArgv"`
}

type BuildArtifactResult struct {
	Artifacts []ReleaseArtifactInput   `json:"artifacts"`
	Image     ResolvedImage            `json:"image"`
	Compose   *ResolvedComposeSnapshot `json:"compose,omitempty"`
	Prepared  PreparedBuild            `json:"prepared"`
}

type ResolvedComposeSnapshot struct {
	SourceDigest string                   `json:"sourceDigest"`
	Files        []string                 `json:"files"`
	Services     []ResolvedComposeService `json:"services"`
	// PrimaryService is the analysis's choice; a snapshot recorded before
	// there was one leaves it empty and keeps its first service.
	PrimaryService string `json:"primaryService,omitempty"`
}

type ResolvedComposeService struct {
	Plan         ComposeServicePlan `json:"plan"`
	Reference    string             `json:"reference"`
	Digest       string             `json:"digest"`
	ConfigDigest string             `json:"configDigest,omitempty"`
	Source       string             `json:"source"`
}

type ArtifactBuilder struct {
	backend BuildBackend
	// dryRun prepares without writing the generated Dockerfile, for preflight
	// to learn what the recipe would refuse from the same code that refuses it.
	dryRun bool
}

func NewArtifactBuilder(backend BuildBackend) *ArtifactBuilder {
	return &ArtifactBuilder{backend: backend}
}

func (b *ArtifactBuilder) Prepare(
	ctx context.Context,
	root string,
	config BuildPlanConfig,
	forceNoCache bool,
	tag string,
	buildVariableNames ...string,
) (PreparedBuild, error) {
	return b.PrepareWithin(ctx, root, root, config, forceNoCache, tag, buildVariableNames...)
}

// PrepareWithin prepares the build of root, a directory of the checkout at
// boundary. A Node workspace member installs from its workspace root, so the
// build context may widen to an ancestor of root, never beyond boundary;
// PreparedBuild.ContextDirectory then names it.
func (b *ArtifactBuilder) PrepareWithin(
	ctx context.Context,
	boundary, root string,
	config BuildPlanConfig,
	forceNoCache bool,
	tag string,
	buildVariableNames ...string,
) (PreparedBuild, error) {
	bindings, err := recipeBuildBindings(config, buildVariableNames)
	if err != nil {
		return PreparedBuild{}, err
	}
	config.Secrets = bindings
	prepared := PreparedBuild{
		Method: config.Method, Recipe: config.Recipe, BuildArgv: []string{},
		BaseImages: []ResolvedImage{}, TargetPlatform: strings.ToLower(config.TargetPlatform),
		CachePolicy: "reuse", SecretIDs: []string{}, SecretLayerGuarantee: "not_applicable",
		SecretBindings: bindings,
	}
	if config.NoCache || forceNoCache {
		prepared.CachePolicy = "no_cache"
	}
	for _, secret := range config.Secrets {
		prepared.SecretIDs = append(prepared.SecretIDs, secret.Variable)
	}
	sort.Strings(prepared.SecretIDs)

	switch config.Method {
	case BuildRecipe:
		if b.backend == nil {
			return PreparedBuild{}, ErrBuilderUnavailable
		}
		recipe, err := selectRecipe(boundary, root, config)
		if err != nil {
			return PreparedBuild{}, err
		}
		if recipe.kind == "node" {
			if recipe.buildCommand != config.BuildCommand {
				prepared.BuildCommand = recipe.buildCommand
			}
			if recipe.startCommand != config.StartCommand {
				prepared.StartCommand = recipe.startCommand
			}
			config.BuildCommand, config.StartCommand = recipe.buildCommand, recipe.startCommand
		}
		prepared.Recipe = recipe.kind
		prepared.RecipeVersion = AutomaticRecipeVersion
		baseRefs := append([]string(nil), recipeBaseCatalogue[recipe.catalogueKey]...)
		if recipe.kind == "go" {
			prepared.GoVersion = recipe.goVersion
			if stage := recipe.goBuild.frontend; stage != nil {
				b.settleBunImage(ctx, &stage.plan)
				prepared.NodeVersion = stage.plan.node.label()
			}
			baseRefs = goRecipeBases(recipe)
			prepared.Notes = append(prepared.Notes, goRecipeNotes(recipe)...)
		}
		if recipe.kind == "python" {
			prepared.PythonVersion = recipe.pythonVersion
			baseRefs[0] = "python:" + recipe.pythonVersion + "-slim"
		}
		switch recipe.kind {
		case "rust":
			prepared.Toolchain = "rust " + recipe.rust.version
			baseRefs = rustRecipeBases(recipe.rust)
			prepared.Notes = append(prepared.Notes, recipe.rust.notes...)
		case "java":
			prepared.Toolchain = "java " + recipe.java.version + " (" + recipe.java.tool + ")"
			if recipe.java.tool == "maven" {
				baseRefs[0] = "maven:3-eclipse-temurin-" + recipe.java.version
			} else {
				baseRefs[0] = "gradle:8-jdk" + recipe.java.version
			}
			baseRefs[1] = "eclipse-temurin:" + recipe.java.version + "-jre-alpine"
		case "dotnet":
			prepared.Toolchain = "dotnet " + recipe.dotnet.version
			baseRefs[0] = "mcr.microsoft.com/dotnet/sdk:" + recipe.dotnet.version
			baseRefs[1] = "mcr.microsoft.com/dotnet/aspnet:" + recipe.dotnet.version
			if !recipe.dotnet.web {
				baseRefs[1] = "mcr.microsoft.com/dotnet/runtime:" + recipe.dotnet.version
			}
		case "deno":
			prepared.Toolchain = "deno"
		case "php":
			prepared.Toolchain = "php " + recipe.php.version
			if recipe.php.assets != "" {
				b.settleBunImage(ctx, &recipe.php.node)
				prepared.Toolchain += " · assets: " + recipe.php.node.toolchain
				prepared.NodeVersion = recipe.php.node.node.label()
				prepared.Install = recipe.php.node.installLine()
				prepared.Notes = append(prepared.Notes, recipe.php.node.notes...)
			}
			baseRefs = phpRecipeBases(recipe.php)
		case "node":
			b.settleBunImage(ctx, &recipe.nodeInstall)
			prepared.Toolchain = recipe.nodeInstall.toolchain
			prepared.NodeVersion = recipe.nodeInstall.node.label()
			prepared.Install = recipe.nodeInstall.installLine()
			prepared.Notes = append(prepared.Notes, recipe.nodeInstall.notes...)
			baseRefs = recipe.nodeInstall.baseImages(config.OutputDirectory != "")
		}
		bases, err := b.resolveBases(ctx, baseRefs)
		if err != nil {
			return PreparedBuild{}, err
		}
		prepared.BaseImages = bases
		content, err := renderRecipeDockerfile(recipe, config, bases)
		if err != nil {
			return PreparedBuild{}, err
		}
		prepared.SecretLayerGuarantee = "buildkit_ephemeral_mount"
		contextRoot := root
		if recipe.contextDir != "" {
			contextRoot = filepath.Join(boundary, filepath.FromSlash(recipe.contextDir))
			prepared.ContextDirectory = recipe.contextDir
		}
		if err := b.writeGenerated(contextRoot, "Dockerfile", content); err != nil {
			return PreparedBuild{}, err
		}
		ignore, notes := recipeDockerignoreAt(contextRoot, recipe.kind, recipe.nodeInputs)
		if err := b.writeGenerated(contextRoot, "Dockerfile.dockerignore", ignore); err != nil {
			return PreparedBuild{}, err
		}
		prepared.Notes = append(prepared.Notes, notes...)
		prepared.Dockerfile = ".just-dashboard/Dockerfile"
		prepared.DockerfilePreview = content
		prepared.DockerfileDigest = digestText(content)
	case BuildStatic:
		if b.backend == nil {
			return PreparedBuild{}, ErrBuilderUnavailable
		}
		bases, err := b.resolveBases(ctx, recipeBaseCatalogue["static"])
		if err != nil {
			return PreparedBuild{}, err
		}
		prepared.BaseImages = bases
		content, err := renderStaticDockerfile(config, bases[0])
		if err != nil {
			return PreparedBuild{}, err
		}
		if err := b.writeGenerated(root, "Dockerfile", content); err != nil {
			return PreparedBuild{}, err
		}
		ignore, _ := recipeDockerignoreAt(root, "static", nil)
		if err := b.writeGenerated(root, "Dockerfile.dockerignore", ignore); err != nil {
			return PreparedBuild{}, err
		}
		prepared.Dockerfile = ".just-dashboard/Dockerfile"
		prepared.DockerfilePreview = content
		prepared.DockerfileDigest = digestText(content)
	case BuildDockerfile:
		dockerfile := config.Dockerfile
		if dockerfile == "" {
			dockerfile = "Dockerfile"
		}
		content, err := readContainedRegular(root, dockerfile, 2<<20)
		if err != nil {
			return PreparedBuild{}, fmt.Errorf("%w: Dockerfile: %v", ErrUnsupportedBuilder, err)
		}
		if err := validateCustomDockerfile(content); err != nil {
			return PreparedBuild{}, err
		}
		prepared.Target, prepared.BuildArgNames, err = dockerfileBuildInputs(content, config.Target, buildVariableNames)
		if err != nil {
			return PreparedBuild{}, err
		}
		prepared.Dockerfile = filepath.ToSlash(filepath.Clean(dockerfile))
		prepared.DockerfilePreview = string(content)
		prepared.DockerfileDigest = digestBytes(content)
		if len(config.Secrets) != 0 {
			return PreparedBuild{}, fmt.Errorf("%w: custom Dockerfiles cannot prove scoped secrets remain out of layers; use a reviewed recipe", ErrUnsupportedBuilder)
		}
	case BuildImage, BuildCompose, BuildNone, BuildLegacyCompose:
		return prepared, nil
	default:
		return PreparedBuild{}, ErrUnsupportedBuilder
	}

	prepared.BuildArgv = buildPreviewArgv(prepared, tag)
	return prepared, nil
}

func (b *ArtifactBuilder) Build(
	ctx context.Context,
	root, tag string,
	config BuildPlanConfig,
	prepared PreparedBuild,
	variables map[string]string,
	registryAuth string,
	source SourceIdentity,
	compose *ComposeAnalysis,
	emit func(BuildLog) error,
) (BuildArtifactResult, error) {
	result := BuildArtifactResult{Prepared: prepared, Artifacts: []ReleaseArtifactInput{}}
	if emit == nil {
		emit = func(BuildLog) error { return nil }
	}
	if (b == nil || b.backend == nil) && config.Method != BuildNone {
		return result, ErrBuilderUnavailable
	}
	switch config.Method {
	case BuildRecipe, BuildDockerfile, BuildStatic:
		bindings := prepared.SecretBindings
		if bindings == nil {
			bindings = config.Secrets
		}
		if config.Method == BuildRecipe {
			expected, err := recipeBuildBindings(config, buildVariableNames(variables))
			if err != nil {
				return result, err
			}
			if !slices.Equal(expected, bindings) {
				return result, fmt.Errorf("%w: build variable bindings changed after preparation; prepare the build with its frozen variable names", ErrArtifactMissing)
			}
		}
		secrets := make([]BuildSecretValue, 0, len(bindings))
		for _, requested := range bindings {
			value, ok := variables[requested.Variable]
			if !ok {
				return result, fmt.Errorf("%w: build variable %s is unavailable", ErrArtifactMissing, requested.Variable)
			}
			secrets = append(secrets, BuildSecretValue{ID: requested.Variable, Step: requested.Step, Value: value})
		}
		buildArgs, err := dockerfileBuildArgValues(prepared.BuildArgNames, variables)
		if err != nil {
			return result, err
		}
		image, err := b.backend.BuildImage(ctx, BuildInvocation{
			ContextDir: root, Dockerfile: prepared.Dockerfile, Tag: tag,
			Platform: prepared.TargetPlatform, NoCache: prepared.CachePolicy == "no_cache",
			Pull: true, Secrets: secrets, Target: prepared.Target, BuildArgs: buildArgs,
		}, redactBuildEmitter(variables, emit))
		if err != nil {
			return result, err
		}
		if err := validateResolvedImage(image); err != nil {
			return result, err
		}
		result.Image = image
		result.Artifacts = append(result.Artifacts, imageArtifact(image, prepared))
		if config.Method == BuildStatic || (config.Method == BuildRecipe && config.OutputDirectory != "") {
			result.Artifacts = append(result.Artifacts, ReleaseArtifactInput{
				Kind: ArtifactStaticBundle, Reference: config.OutputDirectory,
				Digest:   prepared.DockerfileDigest,
				Metadata: mustJSON(map[string]any{"packagedInImage": image.Digest}),
			})
		}
	case BuildImage:
		if source.Digest == "" || source.Repository == "" {
			return result, fmt.Errorf("%w: image source has no immutable digest", ErrArtifactMissing)
		}
		exact := source.Repository + "@" + source.Digest
		image, err := b.backend.PullImage(ctx, exact, registryAuth, redactBuildEmitter(variables, emit))
		if err != nil {
			return result, err
		}
		if image.Digest == "" {
			image.Digest = source.Digest
		}
		if err := validateResolvedImage(image); err != nil {
			return result, err
		}
		result.Image = image
		result.Artifacts = append(result.Artifacts, imageArtifact(image, prepared))
	case BuildCompose:
		if compose == nil {
			return result, fmt.Errorf("%w: Compose analysis is unavailable", ErrArtifactMissing)
		}
		primary, err := chosenComposePrimaryService(config, *compose)
		if err != nil {
			return result, err
		}
		resolved := &ResolvedComposeSnapshot{
			SourceDigest: compose.Digest, Files: append([]string(nil), compose.Files...),
			Services: []ResolvedComposeService{}, PrimaryService: primary,
		}
		for _, service := range compose.Services {
			if service.BuildContext != "" {
				if strings.Contains(service.BuildContext, "$") || strings.Contains(service.BuildDockerfile, "$") {
					return result, fmt.Errorf("%w: Compose service %s uses a dynamic build path", ErrUnsupportedBuilder, service.Name)
				}
				// `build: .` is the most common Compose build of all, and "." is
				// the project root itself, not a subdirectory to contain.
				contextRelative := filepath.Clean(service.BuildContext)
				if contextRelative == "." {
					contextRelative = ""
				}
				contextRoot, err := containedSubdirectory(root, contextRelative)
				if err != nil {
					return result, fmt.Errorf("%w: Compose service %s build context: %v", ErrUnsupportedBuilder, service.Name, err)
				}
				dockerfile := service.BuildDockerfile
				if dockerfile == "" {
					dockerfile = "Dockerfile"
				}
				content, err := readContainedRegular(contextRoot, dockerfile, 2<<20)
				if err != nil {
					return result, fmt.Errorf("%w: Compose service %s Dockerfile: %v", ErrUnsupportedBuilder, service.Name, err)
				}
				if err := validateCustomDockerfile(content); err != nil {
					return result, fmt.Errorf("Compose service %s: %w", service.Name, err)
				}
				if err := emit(BuildLog{Stream: "status", Text: composeServiceBuildStatus + service.Name}); err != nil {
					return result, err
				}
				serviceTag := composeServiceImageTag(tag, service.Name)
				buildArgs, err := composeServiceBuildArgs(service, variables)
				if err != nil {
					return result, fmt.Errorf("%w: Compose service %s: %v", ErrUnsupportedBuilder, service.Name, err)
				}
				target, err := composeServiceTarget(service, variables)
				if err != nil {
					return result, fmt.Errorf("%w: Compose service %s: %v", ErrUnsupportedBuilder, service.Name, err)
				}
				servicePrepared := PreparedBuild{
					Method: BuildDockerfile, Dockerfile: dockerfile, DockerfileDigest: digestBytes(content),
					TargetPlatform: prepared.TargetPlatform, CachePolicy: prepared.CachePolicy, Target: target,
				}
				for _, arg := range buildArgs {
					servicePrepared.BuildArgNames = append(servicePrepared.BuildArgNames, arg.Name)
				}
				servicePrepared.BuildArgv = buildPreviewArgv(servicePrepared, serviceTag)
				image, err := b.backend.BuildImage(ctx, BuildInvocation{
					ContextDir: contextRoot, Dockerfile: dockerfile, Tag: serviceTag,
					Platform: prepared.TargetPlatform, NoCache: prepared.CachePolicy == "no_cache", Pull: true,
					Target: target, BuildArgs: buildArgs,
				}, redactBuildEmitter(variables, emit))
				if err != nil {
					return result, &ComposeServiceError{Service: service.Name, Err: err}
				}
				if err := validateResolvedImage(image); err != nil {
					return result, err
				}
				result.Prepared.ComposeServices = append(result.Prepared.ComposeServices, PreparedComposeService{
					Name: service.Name, BuildContext: service.BuildContext, Dockerfile: dockerfile,
					DockerfileDigest: servicePrepared.DockerfileDigest, BuildArgv: servicePrepared.BuildArgv,
				})
				resolved.Services = append(resolved.Services, composeResolvedService(service, image, "build"))
				result.Artifacts = append(result.Artifacts, composeImageArtifact(service.Name, image, servicePrepared, "build"))
				continue
			}
			if service.Image == "" || strings.Contains(service.Image, "${") {
				return result, fmt.Errorf("%w: Compose service %s has no immutable image or supported build", ErrUnsupportedBuilder, service.Name)
			}
			image, err := b.backend.ResolveImage(ctx, service.Image, registryAuth)
			if err != nil {
				return result, fmt.Errorf("resolve Compose service %s image: %w", service.Name, err)
			}
			if err := validateResolvedImage(image); err != nil {
				return result, err
			}
			pulled, err := b.backend.PullImage(ctx, image.Reference+"@"+image.Digest, registryAuth, redactBuildEmitter(variables, emit))
			if err != nil {
				return result, fmt.Errorf("pull Compose service %s image: %w", service.Name, err)
			}
			if err := validateResolvedImage(pulled); err != nil || pulled.Digest != image.Digest {
				return result, fmt.Errorf("%w: Compose service %s pull did not preserve its resolved digest", ErrArtifactMissing, service.Name)
			}
			resolved.Services = append(resolved.Services, composeResolvedService(service, pulled, "pull"))
			result.Artifacts = append(result.Artifacts, composeImageArtifact(service.Name, pulled, prepared, "pull"))
		}
		result.Compose = resolved
		metadata := mustJSON(resolved)
		result.Artifacts = append(result.Artifacts, ReleaseArtifactInput{
			Kind: ArtifactCompose, Reference: "compose", Digest: digestBytes(metadata), Metadata: metadata,
		})
	case BuildNone:
		return result, nil
	case BuildLegacyCompose:
		return result, fmt.Errorf("%w: legacy Compose runs use the compatibility executor", ErrUnsupportedBuilder)
	default:
		return result, ErrUnsupportedBuilder
	}
	return result, nil
}

type selectedRecipe struct {
	kind, catalogueKey, lockfile string
	mainPackage                  string
	node                         nodeRecipeFramework
	goVersion                    string
	python                       pythonInstall
	pythonVersion                string
	pythonFramework              string
	// pythonServers are the process managers the start command runs that no
	// manifest declares; the recipe installs each one.
	pythonServers []string
	rust          rustRecipe
	java          javaRecipe
	dotnet        dotnetProject
	deno          denoRecipe
	php           phpRecipe
	// runtimeAssets are the root-level files a compiled service reads at
	// runtime, copied beside its binary (recipe_runtime_files.go).
	runtimeAssets []string
	// emptyDotenv creates an empty .env where a Go or Rust binary runs,
	// because the source treats a missing .env as fatal; see
	// dotenvFileRequired.
	emptyDotenv bool
	// nodeInstall is the Node install plan; member is the package's path
	// under contextDir, the workspace root a member installs from (both
	// empty for a package that installs on its own). buildCommand and
	// startCommand are the plan's commands on the resolved manager's runner.
	nodeInstall                nodeInstallPlan
	member, contextDir         string
	buildCommand, startCommand string
	nodeInputs                 []string

	// goChoice is why the toolchain is goVersion, and goBuild what the
	// module needs beyond it (build_go_recipe.go).
	goChoice goVersionChoice
	goBuild  goBuildPlan
}

func selectRecipe(boundary, root string, config BuildPlanConfig) (selectedRecipe, error) {
	requested := config.Recipe
	if requested == "" {
		switch {
		case regularExists(root, "package.json"):
			requested = "node"
		case regularExists(root, "go.mod"):
			requested = "go"
		case regularExists(root, "uv.lock") || regularExists(root, "poetry.lock") || regularExists(root, "requirements.txt") || regularExists(root, "pyproject.toml"):
			requested = "python"
		case regularExists(root, "Cargo.toml"):
			requested = "rust"
		case regularExists(root, "pom.xml") || regularExists(root, "build.gradle") || regularExists(root, "build.gradle.kts"):
			requested = "java"
		case len(listContainedFiles(root, ".csproj")) > 0:
			requested = "dotnet"
		case regularExists(root, "deno.json") || regularExists(root, "deno.jsonc"):
			requested = "deno"
		case regularExists(root, "composer.json") || regularExists(root, "index.php") || regularExists(root, "public/index.php"):
			requested = "php"
		}
	}
	switch requested {
	case "node":
		manifest, err := readContainedRegular(root, "package.json", 512<<10)
		if err != nil {
			return selectedRecipe{}, fmt.Errorf("%w: Node recipe requires package.json", ErrUnsupportedBuilder)
		}
		source, err := readNodeInstallSource(boundary, checkoutPath(boundary, root), nodeTargetArch(config.TargetPlatform), newNodeReadBudget())
		if err != nil {
			return selectedRecipe{}, err
		}
		plan := planNodeInstall(source.facts, nodeInstallChoice{
			selected: config.PackageManager, build: config.BuildCommand, start: config.StartCommand,
		})
		if plan.blocked != nil {
			return selectedRecipe{}, plan.blockedError()
		}
		manager := plan.manager
		files := nodeRootFiles{}
		if regularExists(root, "angular.json") {
			files.angularJSON, _ = readContainedRegular(root, "angular.json", 512<<10)
		}
		if regularExists(root, "Procfile") {
			files.procfile, _ = readContainedRegular(root, "Procfile", 64<<10)
		}
		framework, err := validateNodeRecipeContent(manifest, files, BuildPlanConfig{
			Method: config.Method, Recipe: config.Recipe, PackageManager: manager,
			BuildCommand: plan.build, StartCommand: plan.start, OutputDirectory: config.OutputDirectory,
		})
		if err != nil {
			return selectedRecipe{}, err
		}
		recipe := selectedRecipe{
			kind: "node", catalogueKey: "node:" + manager, lockfile: plan.lockfile, node: framework,
			nodeInstall: plan, member: source.member(), buildCommand: plan.build, startCommand: plan.start,
			nodeInputs: source.installInputs(plan),
		}
		if source.context != source.dir {
			recipe.contextDir = source.context
			if recipe.contextDir == "" {
				recipe.contextDir = "."
			}
		}
		return recipe, nil
	case "go":
		if !regularExists(root, "go.mod") {
			return selectedRecipe{}, fmt.Errorf("%w: Go recipe requires go.mod", ErrUnsupportedBuilder)
		}
		module, err := readContainedRegular(root, "go.mod", 512<<10)
		if err != nil {
			return selectedRecipe{}, err
		}
		var versionFile []byte
		if regularExists(root, ".go-version") {
			versionFile, err = readContainedRegular(root, ".go-version", 1024)
			if err != nil {
				return selectedRecipe{}, err
			}
		}
		packages, err := scanGoModule(root)
		if err != nil {
			return selectedRecipe{}, err
		}
		build, err := planGoBuild(boundary, root, module, packages, config)
		if err != nil {
			return selectedRecipe{}, err
		}
		choice, err := resolveGoRecipeVersion(config.GoVersion, string(versionFile), goVersionInputs(module, build.context))
		if err != nil {
			return selectedRecipe{}, err
		}
		main, err := selectGoMainPackage(packages, config, goModulePath(module))
		if err != nil {
			return selectedRecipe{}, err
		}
		recipe := selectedRecipe{kind: "go", catalogueKey: "go", mainPackage: main, goVersion: choice.version, goChoice: choice, goBuild: build,
			runtimeAssets: compiledRuntimeAssets(root, ".go"), emptyDotenv: dotenvFileRequired(root, "go"), contextDir: build.context.dir}
		if stage := build.frontend; stage != nil {
			recipe.nodeInputs = stage.inputs
		}
		return recipe, nil
	case "python":
		files := map[string][]byte{}
		for _, name := range []string{"requirements.txt", "pyproject.toml", "uv.lock", "poetry.lock"} {
			if content, err := readContainedRegular(root, name, 2<<20); err == nil {
				files[name] = content
			} else if regularExists(root, name) {
				files[name] = []byte("locked")
			}
		}
		versionFile, _ := readContainedRegular(root, ".python-version", 4096)
		runtimeFile, _ := readContainedRegular(root, "runtime.txt", 4096)
		version, err := choosePythonRecipeVersion(config.PythonVersion, string(versionFile), string(runtimeFile), string(files["pyproject.toml"]))
		if err != nil {
			return selectedRecipe{}, err
		}
		install, err := selectPythonInstall(root, version)
		if err != nil {
			return selectedRecipe{}, err
		}
		if strings.TrimSpace(config.StartCommand) == "" {
			return selectedRecipe{}, fmt.Errorf("%w: Python recipe requires a start command; detection proposes one for Django, FastAPI, Flask, Streamlit and Gradio", ErrUnsupportedBuilder)
		}
		deps := readPythonDependencies(files)
		recipe := selectedRecipe{
			kind: "python", catalogueKey: "python", lockfile: install.kind, python: install, pythonVersion: version,
			pythonServers: undeclaredPythonServers(config.StartCommand, deps),
		}
		if framework := matchPythonFramework(deps); framework != nil {
			recipe.pythonFramework = framework.Name
		}
		return recipe, nil
	case "rust":
		rust, err := selectRustRecipe(boundary, root, config)
		if err != nil {
			return selectedRecipe{}, err
		}
		rust.assets = compiledRuntimeAssets(root, ".rs")
		return selectedRecipe{kind: "rust", catalogueKey: "rust", rust: rust, emptyDotenv: rust.trunk == "" && dotenvFileRequired(root, "rust"),
			contextDir: rust.context}, nil
	case "java":
		java, err := selectJavaRecipe(root)
		if err != nil {
			return selectedRecipe{}, err
		}
		return selectedRecipe{kind: "java", catalogueKey: "java:" + java.tool, java: java}, nil
	case "dotnet":
		project, err := selectDotnetRecipe(root)
		if err != nil {
			return selectedRecipe{}, err
		}
		project.seeds = dotnetSQLiteSeeds(root, project)
		return selectedRecipe{kind: "dotnet", catalogueKey: "dotnet", dotnet: project}, nil
	case "deno":
		deno, err := selectDenoRecipe(root, config)
		if err != nil {
			return selectedRecipe{}, err
		}
		return selectedRecipe{kind: "deno", catalogueKey: "deno", deno: deno}, nil
	case "php":
		php, err := selectPHPRecipe(boundary, root, config)
		if err != nil {
			return selectedRecipe{}, err
		}
		recipe := selectedRecipe{kind: "php", catalogueKey: "php", php: php}
		if php.assets != "" {
			recipe.nodeInputs = php.inputs
		}
		return recipe, nil
	default:
		return selectedRecipe{}, fmt.Errorf("%w: no supported automatic recipe was selected", ErrUnsupportedBuilder)
	}
}

// listContainedFiles names the regular files directly under a root with the
// given suffix, sorted, for the recipes whose manifest has no fixed name.
func listContainedFiles(root, suffix string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), suffix) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

func renderRecipeDockerfile(recipe selectedRecipe, config BuildPlanConfig, bases []ResolvedImage) (string, error) {
	if len(bases) == 0 {
		return "", ErrBuilderUnavailable
	}
	installSecrets := buildSecretMounts(config.Secrets, "install")
	buildSecrets := buildSecretMounts(config.Secrets, "build")
	var lines []string
	lines = append(lines, "# syntax=docker/dockerfile:1.10")
	switch recipe.kind {
	case "node":
		rendered, err := renderNodeDockerfile(recipe, config, bases, installSecrets, buildSecrets)
		if err != nil {
			return "", err
		}
		lines = append(lines, rendered...)
	case "go":
		rendered, err := renderGoDockerfile(recipe, config, bases, installSecrets, buildSecrets)
		if err != nil {
			return "", err
		}
		lines = append(lines, rendered...)
	case "python":
		base := immutableImageReference(bases[0])
		lines = append(lines, "FROM "+base, "WORKDIR /app",
			"ENV PYTHONUNBUFFERED=1 PYTHONDONTWRITEBYTECODE=1 PIP_DISABLE_PIP_VERSION_CHECK=1 PIP_ROOT_USER_ACTION=ignore",
			pythonRecipeNetworkEnv())
		for _, env := range recipe.python.env {
			lines = append(lines, "ENV "+env)
		}
		lines = append(lines, "COPY . .", "RUN "+installSecrets+recipe.python.command)
		for _, server := range recipe.pythonServers {
			lines = append(lines, "RUN "+installSecrets+recipe.python.serverInstall+" "+pythonServerPackages[server])
		}
		if command := strings.TrimSpace(config.BuildCommand); command != "" {
			lines = append(lines, "RUN "+buildSecrets+command)
		}
		for _, env := range pythonFrameworkEnv(recipe.pythonFramework) {
			lines = append(lines, "ENV "+env)
		}
		lines = append(lines, shellCMD(config.StartCommand))
	case "rust", "java", "dotnet", "deno", "php":
		var rendered []string
		var err error
		switch recipe.kind {
		case "rust":
			rendered, err = renderRustDockerfile(recipe.rust, config, bases, installSecrets, buildSecrets)
		case "java":
			rendered, err = renderJavaDockerfile(recipe.java, config, bases, installSecrets, buildSecrets)
		case "dotnet":
			rendered, err = renderDotnetDockerfile(recipe.dotnet, config, bases, installSecrets, buildSecrets)
		case "php":
			rendered, err = renderPHPDockerfile(recipe.php, config, bases, installSecrets, buildSecrets)
		default:
			rendered, err = renderDenoDockerfile(recipe.deno, config, bases, installSecrets, buildSecrets)
		}
		if err != nil {
			return "", err
		}
		lines = append(lines, rendered...)
	default:
		return "", ErrUnsupportedBuilder
	}
	if recipe.emptyDotenv {
		lines = withEmptyDotenv(lines)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func renderStaticDockerfile(config BuildPlanConfig, base ResolvedImage) (string, error) {
	output := strings.TrimSpace(config.OutputDirectory)
	if output == "" {
		output = "."
	} else if !validOutputDirectory(output) {
		return "", fmt.Errorf("%w: static output directory is invalid", ErrUnsupportedBuilder)
	}
	lines := append([]string{"# syntax=docker/dockerfile:1.10", "FROM " + immutableImageReference(base)}, staticServerLines(config.SPAFallback)...)
	lines = append(lines, "COPY "+filepath.ToSlash(output)+"/ /usr/share/nginx/html/")
	return strings.Join(lines, "\n") + "\n", nil
}

// staticServerLines configures nginx the way its own default does — port 80,
// index.html — and refuses every dot-path except .well-known, so a stray
// .git/, .env or .htaccess in the published directory is never served. A
// site whose client owns its routes also answers a path with no file behind
// it with index.html, so a deep link into the application opens instead of
// 404ing.
func staticServerLines(spaFallback bool) []string {
	conf := []string{
		"server {", "    listen 80;", "    server_name _;", "    root /usr/share/nginx/html;",
		"    index index.html index.htm;", `    location ~ /\.(?!well-known/) {`, "        deny all;", "    }",
	}
	if spaFallback {
		conf = append(conf, "    location / {", "        try_files $uri $uri/ /index.html;", "    }")
	}
	conf = append(conf, "}")
	quoted := make([]string, 0, len(conf))
	for _, line := range conf {
		quoted = append(quoted, "'"+line+"'")
	}
	return []string{"RUN printf '%s\\n' " + strings.Join(quoted, " ") + " > /etc/nginx/conf.d/default.conf"}
}

func (b *ArtifactBuilder) resolveBases(ctx context.Context, references []string) ([]ResolvedImage, error) {
	resolved := make([]ResolvedImage, 0, len(references)+1)
	for _, reference := range references {
		image, err := b.backend.ResolveImage(ctx, reference, "")
		if err != nil {
			return nil, fmt.Errorf("%w: resolve reviewed base image %s: %v", ErrBuilderUnavailable, reference, err)
		}
		if err := validateResolvedImage(image); err != nil {
			return nil, err
		}
		resolved = append(resolved, image)
	}
	return resolved, nil
}

func buildPreviewArgv(prepared PreparedBuild, tag string) []string {
	argv := []string{"docker", "buildx", "build", "--progress=plain", "--file", prepared.Dockerfile, "--tag", tag, "--load", "--pull"}
	if prepared.TargetPlatform != "" {
		argv = append(argv, "--platform", prepared.TargetPlatform)
	}
	if prepared.CachePolicy == "no_cache" {
		argv = append(argv, "--no-cache")
	}
	if prepared.Target != "" {
		argv = append(argv, "--target", prepared.Target)
	}
	for _, name := range prepared.BuildArgNames {
		argv = append(argv, "--build-arg", name)
	}
	for _, id := range prepared.SecretIDs {
		argv = append(argv, "--secret", "id="+id+",env=<ephemeral>")
	}
	return append(argv, ".")
}

func buildSecretMounts(secrets []BuildSecretConfig, step string) string {
	ids := []string{}
	for _, secret := range secrets {
		if buildSecretReaches(secret.Step, step) {
			ids = append(ids, secret.Variable)
		}
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, "--mount=type=secret,id="+id+",env="+id+",required=true")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ") + " "
}

func shellCMD(command string) string {
	encoded, _ := json.Marshal([]string{"/bin/sh", "-c", strings.TrimSpace(command)})
	return "CMD " + string(encoded)
}

func immutableImageReference(image ResolvedImage) string {
	return image.Reference + "@" + image.Digest
}

func resolveCatalogueImage(images []ResolvedImage, reference string) (ResolvedImage, error) {
	for _, image := range images {
		if image.Reference == reference {
			return image, nil
		}
	}
	return ResolvedImage{}, fmt.Errorf("%w: reviewed base %s was not resolved", ErrBuilderUnavailable, reference)
}

func validateResolvedImage(image ResolvedImage) error {
	if image.Reference == "" || !contentDigestRE.MatchString(image.Digest) ||
		strings.ContainsAny(image.Reference, "\x00\r\n") {
		return fmt.Errorf("%w: backend returned no immutable image identity", ErrArtifactMissing)
	}
	return nil
}

func imageArtifact(image ResolvedImage, prepared PreparedBuild) ReleaseArtifactInput {
	metadata := mustJSON(map[string]any{
		"configDigest": image.ConfigDigest, "os": image.OS, "architecture": image.Architecture,
		"platforms": image.Platforms, "dockerfileDigest": prepared.DockerfileDigest,
		"goVersion": prepared.GoVersion,
	})
	return ReleaseArtifactInput{
		Kind: ArtifactImage, Reference: image.Reference, Digest: image.Digest,
		Metadata: metadata, SizeBytes: image.SizeBytes,
	}
}

func composeResolvedService(plan ComposeServicePlan, image ResolvedImage, source string) ResolvedComposeService {
	return ResolvedComposeService{
		Plan: plan, Reference: image.Reference, Digest: image.Digest,
		ConfigDigest: image.ConfigDigest, Source: source,
	}
}

func composeImageArtifact(service string, image ResolvedImage, prepared PreparedBuild, source string) ReleaseArtifactInput {
	artifact := imageArtifact(image, prepared)
	artifact.Metadata = mustJSON(map[string]any{
		"service": service, "source": source, "configDigest": image.ConfigDigest,
		"os": image.OS, "architecture": image.Architecture, "platforms": image.Platforms,
		"dockerfileDigest": prepared.DockerfileDigest,
	})
	return artifact
}

func composeServiceImageTag(tag, service string) string {
	service = strings.ToLower(service)
	var normalized strings.Builder
	for _, r := range service {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-' {
			normalized.WriteRune(r)
		} else {
			normalized.WriteByte('-')
		}
	}
	suffix := strings.Trim(normalized.String(), ".-")
	if suffix == "" {
		suffix = "service"
	}
	return tag + "-" + suffix
}

func redactBuildEmitter(values map[string]string, emit func(BuildLog) error) func(BuildLog) error {
	redact := buildRedactor(values)
	return func(line BuildLog) error {
		line.Text = redact(line.Text)
		return emit(line)
	}
}

// buildRedactor replaces every variable value in a text, longest first so a
// value that contains another is not left half redacted.
func buildRedactor(values map[string]string) func(string) string {
	secrets := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			secrets = append(secrets, value)
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return func(text string) string {
		for _, secret := range secrets {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
		return text
	}
}

// writeGenerated places a generated file beside the build unless this is a
// preflight dry run, which renders everything and writes nothing.
func (b *ArtifactBuilder) writeGenerated(root, name, content string) error {
	if b.dryRun {
		return nil
	}
	return writeGeneratedFile(root, name, content)
}

func writeGeneratedFile(root, name, content string) error {
	directory, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.MkdirAll(".just-dashboard", 0o700); err != nil {
		return err
	}
	// The checkout can contain symlinks. Root-relative operations and an
	// exclusive temporary file keep generated output inside this build context.
	temporary := ".just-dashboard/." + name + "-" + rand.Text()
	file, err := directory.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer directory.Remove(temporary)
	_, writeErr := file.WriteString(content)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return directory.Rename(temporary, ".just-dashboard/"+name)
}

func readContainedRegular(root, relative string, limit int64) ([]byte, error) {
	realPath, err := containedRegularPath(root, relative, limit)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(realPath)
}

// containedRegularPath is where a relative path lands when it is a bounded
// regular file inside root, reached without leaving it through a symlink.
func containedRegularPath(root, relative string, limit int64) (string, error) {
	if !safeRelativePath(relative) {
		return "", fmt.Errorf("path escapes the build context")
	}
	path := filepath.Join(root, filepath.Clean(relative))
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(realRoot, realPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path escapes the build context")
	}
	info, err := os.Lstat(realPath)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > limit {
		return "", fmt.Errorf("path is not a bounded regular file")
	}
	return realPath, nil
}

func regularExists(root, relative string) bool {
	info, err := os.Lstat(filepath.Join(root, filepath.Clean(relative)))
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}

func digestText(value string) string {
	hash := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(hash[:])
}

func releaseImageTag(environmentID, runID int64) string {
	return "just-dashboard/deployment-" + strconv.FormatInt(environmentID, 10) + ":run-" + strconv.FormatInt(runID, 10)
}
