package deploy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/distribution/reference"
)

type CredentialMaterial struct {
	Kind   string
	Config json.RawMessage
	Secret string
}

type CredentialReader interface {
	OpenCredential(context.Context, int64) (CredentialMaterial, error)
}

type PlanningDocker interface {
	Ping(context.Context) dockerx.Availability
	ResolveDistributionImage(context.Context, string, string) (*dockerx.DistributionImage, error)
	InspectImage(context.Context, string) (*dockerx.ImageDetail, error)
	SpecOf(context.Context, string) (*dockerx.ContainerSpec, error)
	ListStacks(context.Context, []string) ([]dockerx.ComposeStack, error)
	ComposeAvailable(context.Context) bool
	ValidateComposePlan(context.Context, string, []dockerx.ComposeInput, []string) (*dockerx.ComposeValidation, error)
}

type HostSourceAnalyzer struct {
	paths        *files.Service
	docker       PlanningDocker
	credentials  CredentialReader
	composeRoots []string
	cacheRoot    string
	detector     Detector
	gitMu        sync.Mutex
}

func NewHostSourceAnalyzer(
	deployRoots []string,
	composeRoots []string,
	cacheRoot string,
	docker PlanningDocker,
	credentials CredentialReader,
) *HostSourceAnalyzer {
	return &HostSourceAnalyzer{
		paths: files.New(deployRoots), docker: docker, credentials: credentials,
		composeRoots: append([]string(nil), composeRoots...), cacheRoot: cacheRoot,
		detector: Detector{},
	}
}

func (a *HostSourceAnalyzer) Analyze(ctx context.Context, source DraftSourceConfig) (DetectionResult, error) {
	source = canonicalSourceConfig(source)
	if err := source.Validate(); err != nil {
		return DetectionResult{}, err
	}
	switch source.Mode {
	case SourceModeGitURL, SourceModeConnectedRepository, SourceModeComposeGit:
		return a.analyzeRemoteGit(ctx, source)
	case SourceModeLocalCheckout, SourceModeExistingCheckout:
		return a.analyzeLocal(ctx, source)
	case SourceModeComposeLocal:
		return a.analyzeLocalCompose(ctx, source)
	case SourceModeComposePaste, SourceModeComposeUpload:
		return a.analyzeCompose(ctx, source, source.ComposeFiles, "")
	case SourceModeImageReference:
		return a.analyzeImage(ctx, source)
	case SourceModeExistingContainer, SourceModeExistingStack:
		preview, err := a.PreviewImport(ctx, source)
		if err != nil {
			return DetectionResult{}, err
		}
		candidate := newDetectedCandidate("", preview.Configuration.Build.Method, DetectedCandidate{
			Name: preview.Name, Profile: ProfileImported, Confidence: ConfidenceHigh,
			Evidence:      []DetectionEvidence{{Path: preview.ResourceID, Reason: "observed existing Docker resource"}},
			NeedsDecision: append([]string(nil), preview.Unsupported...),
		})
		return DetectionResult{
			Source:     SourceIdentity{Kind: SourceImport, Repository: preview.Name, Observed: preview.Observed},
			Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID,
		}, nil
	case SourceModeBlueprint:
		plan, err := RenderBlueprintPlan(source, "")
		if err != nil {
			return DetectionResult{}, err
		}
		if err := source.ValidateForDeployment(); err != nil {
			// A preview-only blueprint still renders; it just has no digest to
			// deploy from, and the picker already explains why.
			return plan.Detection, nil
		}
		return a.resolveBlueprintImage(ctx, plan.Detection)
	default:
		return DetectionResult{}, ErrUnsupportedSource
	}
}

// resolveBlueprintImage gives a rendered blueprint the immutable image digest
// its release will pull, exactly as an operator-typed image reference gets one.
func (a *HostSourceAnalyzer) resolveBlueprintImage(ctx context.Context, detection DetectionResult) (DetectionResult, error) {
	reference, err := normalizeImageReference(detection.Source.Repository)
	if err != nil {
		return DetectionResult{}, fmt.Errorf("%w: blueprint image reference: %v", ErrInvalidSource, err)
	}
	detection.Source.Repository = reference
	if a.docker == nil {
		detection.Unavailable = "Docker is unavailable"
		return detection, nil
	}
	resolveCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resolved, err := a.docker.ResolveDistributionImage(resolveCtx, reference, "")
	if err != nil {
		return DetectionResult{}, fmt.Errorf("%w: %w: registry manifest lookup failed: %v", ErrSourceUnavailable, ErrDockerUnavailable, err)
	}
	if resolved == nil || !contentDigestRE.MatchString(resolved.Digest) {
		return DetectionResult{}, fmt.Errorf("%w: %w: registry returned no immutable image digest", ErrSourceUnavailable, ErrDockerUnavailable)
	}
	platforms := make([]string, 0, len(resolved.Platforms))
	for _, platform := range resolved.Platforms {
		platform = strings.ToLower(strings.TrimSpace(platform))
		if !validPlatform(platform) {
			return DetectionResult{}, fmt.Errorf("%w: %w: registry returned malformed platform metadata", ErrSourceUnavailable, ErrDockerUnavailable)
		}
		platforms = append(platforms, platform)
	}
	detection.Source.Digest = resolved.Digest
	detection.Source.Platforms = uniqueSorted(platforms)
	for index := range detection.Candidates {
		detection.Candidates[index].Evidence = append(detection.Candidates[index].Evidence,
			DetectionEvidence{Path: reference, Reason: "registry digest " + resolved.Digest})
	}
	return detection, nil
}

func (a *HostSourceAnalyzer) analyzeLocal(ctx context.Context, source DraftSourceConfig) (DetectionResult, error) {
	checkoutRoot, err := a.resolveLocalRoot(source.LocalPath, "")
	if err != nil {
		return DetectionResult{}, err
	}
	identity, err := localGitIdentity(
		ctx, source.Kind, checkoutRoot, source.CredentialID,
		source.IncludeSubmodules, source.IncludeLFS,
	)
	if err != nil {
		return DetectionResult{}, err
	}
	root, err := detectionSubdirectory(checkoutRoot, source.Subdirectory)
	if err != nil {
		return DetectionResult{}, err
	}
	return a.detector.DetectPath(ctx, root, identity)
}

func (a *HostSourceAnalyzer) analyzeRemoteGit(ctx context.Context, source DraftSourceConfig) (DetectionResult, error) {
	remote, repository, err := remoteForSource(source)
	if err != nil {
		return DetectionResult{}, err
	}
	cacheRoot, cleanupCache, err := a.planningCacheRoot()
	if err != nil {
		return DetectionResult{}, err
	}
	defer cleanupCache()
	ref := source.Ref
	if ref == "" {
		ref = "main"
	}
	environment, cleanupCredential, err := a.gitEnvironment(ctx, cacheRoot, remote, source.CredentialID)
	if err != nil {
		return DetectionResult{}, fmt.Errorf("%w: %w: credential is unavailable", ErrSourceUnavailable, ErrGitUnavailable)
	}
	defer cleanupCredential()

	// A mirror can be shared by successive planning requests, while its Git
	// metadata and worktree registry cannot be updated concurrently. The
	// analyzer deliberately serializes this bounded planning work; release-time
	// materialization can replace this with keyed locks when C4 adds workers.
	a.gitMu.Lock()
	defer a.gitMu.Unlock()

	remoteRef, _ := planningGitRef(ref)
	resolveCtx, cancelResolve := context.WithTimeout(ctx, 10*time.Second)
	out, err := runPlanningGit(resolveCtx, "", environment,
		"ls-remote", "--exit-code", "--refs", remote, remoteRef)
	cancelResolve()
	if err != nil {
		return DetectionResult{}, fmt.Errorf("%w: %w: Git ref could not be resolved: %v", ErrSourceUnavailable, ErrGitUnavailable, err)
	}
	fields := strings.Fields(out)
	if len(fields) != 2 || fields[1] != remoteRef || !validGitObjectID(fields[0]) {
		return DetectionResult{}, fmt.Errorf("%w: %w: Git returned no immutable revision", ErrSourceUnavailable, ErrGitUnavailable)
	}
	identity := SourceIdentity{
		Kind: source.Kind, Remote: remote, Repository: repository, Ref: ref,
		Revision: fields[0], CredentialID: source.CredentialID,
		IncludeSubmodules: source.IncludeSubmodules, IncludeLFS: source.IncludeLFS,
	}
	mirrorRoot := filepath.Join(cacheRoot, "git-mirrors")
	worktreeRoot := filepath.Join(cacheRoot, "git-worktrees")
	if err := makePrivateDirectory(mirrorRoot); err != nil {
		return DetectionResult{}, err
	}
	if err := makePrivateDirectory(worktreeRoot); err != nil {
		return DetectionResult{}, err
	}
	mirrorName := strings.TrimPrefix(digestBytes([]byte(remote)), "sha256:") + ".git"
	mirror := filepath.Join(mirrorRoot, mirrorName)
	if err := ensurePlanningMirror(ctx, mirror, remote, environment); err != nil {
		return DetectionResult{}, fmt.Errorf("%w: %w: managed Git mirror is unavailable: %v", ErrSourceUnavailable, ErrGitUnavailable, err)
	}
	localRef := "refs/just-dashboard/planning/" + strings.TrimPrefix(digestBytes([]byte(remoteRef)), "sha256:")
	fetchCtx, cancelFetch := context.WithTimeout(ctx, 10*time.Second)
	_, err = runPlanningGit(fetchCtx, mirror, environment,
		"fetch", "--force", "--depth=1", "--filter=blob:limit=1048576", "--no-tags",
		"origin", "+"+remoteRef+":"+localRef)
	cancelFetch()
	if err != nil {
		return DetectionResult{}, fmt.Errorf("%w: %w: bounded Git mirror refresh failed: %v", ErrSourceUnavailable, ErrGitUnavailable, err)
	}
	resolvedRevision, err := runPlanningGit(ctx, mirror, environment, "rev-parse", localRef)
	if err != nil || strings.TrimSpace(resolvedRevision) != identity.Revision {
		return DetectionResult{}, fmt.Errorf("%w: %w: Git ref moved during inspection", ErrSourceUnavailable, ErrGitUnavailable)
	}

	temporary, err := os.MkdirTemp(worktreeRoot, "inspect-")
	if err != nil {
		return DetectionResult{}, err
	}
	if err := os.Remove(temporary); err != nil {
		return DetectionResult{}, err
	}
	worktreeAdded := false
	defer func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelCleanup()
		if worktreeAdded {
			_, _ = runPlanningGit(cleanupCtx, mirror, environment, "worktree", "remove", "--force", "--", temporary)
			_, _ = runPlanningGit(cleanupCtx, mirror, environment, "worktree", "prune")
		}
		_ = os.RemoveAll(temporary)
	}()
	worktreeCtx, cancelWorktree := context.WithTimeout(ctx, 10*time.Second)
	_, err = runPlanningGit(worktreeCtx, mirror, environment,
		"worktree", "add", "--detach", "--force", "--", temporary, identity.Revision)
	cancelWorktree()
	if err != nil {
		return DetectionResult{}, fmt.Errorf("%w: %w: bounded Git worktree failed: %v", ErrSourceUnavailable, ErrGitUnavailable, err)
	}
	worktreeAdded = true
	verifyCtx, cancelVerify := context.WithTimeout(ctx, 5*time.Second)
	inspectedRevision, err := runPlanningGit(verifyCtx, temporary, environment, "rev-parse", "HEAD")
	cancelVerify()
	if err != nil || strings.TrimSpace(inspectedRevision) != identity.Revision {
		return DetectionResult{}, fmt.Errorf("%w: %w: Git ref moved during inspection", ErrSourceUnavailable, ErrGitUnavailable)
	}
	root, err := detectionSubdirectory(temporary, source.Subdirectory)
	if err != nil {
		return DetectionResult{}, err
	}
	if source.Mode == SourceModeComposeGit {
		return a.analyzeComposeAtPath(ctx, source, root, identity)
	}
	return a.detector.DetectPath(ctx, root, identity)
}

func (a *HostSourceAnalyzer) planningCacheRoot() (string, func(), error) {
	cacheRoot := a.cacheRoot
	cleanup := func() {}
	if cacheRoot == "" {
		var err error
		cacheRoot, err = os.MkdirTemp("", "just-dashboard-deployment-detection-")
		if err != nil {
			return "", cleanup, err
		}
		cleanup = func() { _ = os.RemoveAll(cacheRoot) }
	}
	if err := makePrivateDirectory(cacheRoot); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return cacheRoot, cleanup, nil
}

func makePrivateDirectory(path string) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("planning cache path is not a directory")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func ensurePlanningMirror(ctx context.Context, mirror, remote string, environment []string) error {
	if info, err := os.Lstat(mirror); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("managed mirror path is not a directory")
		}
	} else if os.IsNotExist(err) {
		initCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, initErr := runPlanningGit(initCtx, "", environment, "init", "--bare", "--", mirror)
		cancel()
		if initErr != nil {
			return initErr
		}
	} else {
		return err
	}
	if err := os.Chmod(mirror, 0o700); err != nil {
		return err
	}
	configCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := runPlanningGit(configCtx, mirror, environment, "config", "remote.origin.url", remote); err != nil {
		return err
	}
	_, err := runPlanningGit(configCtx, mirror, environment,
		"config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	return err
}

func (a *HostSourceAnalyzer) analyzeImage(ctx context.Context, source DraftSourceConfig) (DetectionResult, error) {
	reference, err := normalizeImageReference(source.Image)
	if err != nil {
		return DetectionResult{}, err
	}
	identity := SourceIdentity{Kind: SourceImage, Repository: reference, CredentialID: source.CredentialID}
	if a.docker == nil {
		return DetectionResult{Source: identity, Candidates: []DetectedCandidate{}, Unavailable: "Docker is unavailable"}, nil
	}
	auth, err := a.registryAuth(ctx, source.CredentialID, reference)
	if err != nil {
		return DetectionResult{}, fmt.Errorf("%w: %w: credential is unavailable", ErrSourceUnavailable, ErrDockerUnavailable)
	}
	resolveCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resolved, err := a.docker.ResolveDistributionImage(resolveCtx, reference, auth)
	if err != nil {
		return DetectionResult{}, fmt.Errorf("%w: %w: registry manifest lookup failed: %v", ErrSourceUnavailable, ErrDockerUnavailable, err)
	}
	if resolved == nil || !contentDigestRE.MatchString(resolved.Digest) {
		return DetectionResult{}, fmt.Errorf("%w: %w: registry returned no immutable image digest", ErrSourceUnavailable, ErrDockerUnavailable)
	}
	platforms := make([]string, 0, len(resolved.Platforms))
	for _, platform := range resolved.Platforms {
		platform = strings.ToLower(strings.TrimSpace(platform))
		if !validPlatform(platform) {
			return DetectionResult{}, fmt.Errorf("%w: %w: registry returned malformed platform metadata", ErrSourceUnavailable, ErrDockerUnavailable)
		}
		platforms = append(platforms, platform)
	}
	identity.Digest = resolved.Digest
	identity.Platforms = uniqueSorted(platforms)
	selectedPlatform := source.Platform
	for _, platform := range identity.Platforms {
		if selectedPlatform == "" && (platform == runtime.GOOS+"/"+runtime.GOARCH || strings.HasPrefix(platform, runtime.GOOS+"/"+runtime.GOARCH+"/")) {
			selectedPlatform = platform
			break
		}
	}
	if selectedPlatform == "" && len(identity.Platforms) > 0 {
		selectedPlatform = identity.Platforms[0]
	}
	if selectedPlatform != "" {
		parts := strings.Split(selectedPlatform, "/")
		identity.OS = parts[0]
		if len(parts) > 1 {
			identity.Architecture = parts[1]
		}
	}
	// The registry manifest names platforms and nothing else, so the port the
	// image serves on has to come from the image's own configuration — which
	// only exists locally, for an image this host has already pulled. When it
	// is there the plan starts with the right port instead of zero; when it is
	// not, the decision below still says so and the form asks.
	evidence := []DetectionEvidence{{Path: reference, Reason: "registry digest " + resolved.Digest}}
	port, portReason := a.imageExposedPort(ctx, reference)
	decisions := []string{"confirm runtime command, ports, storage, and readiness"}
	if port > 0 {
		evidence = append(evidence, DetectionEvidence{Path: reference, Reason: portReason})
		// The port was the only thing the plan actually asks an image for. The
		// command is the image's own, an empty mount list is a plan preflight
		// passes, and a readiness gate is demanded of web and static workloads
		// rather than of an image — so nothing is left to confirm, and saying
		// otherwise sent every image to the first screen to read a sentence
		// with no field under it.
		decisions = []string{}
	}
	candidate := newDetectedCandidate("", BuildImage, DetectedCandidate{
		Name: reference, Profile: ProfileImage, Confidence: ConfidenceHigh,
		Port: port, Evidence: evidence, NeedsDecision: decisions,
	})
	return DetectionResult{Source: identity, Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID}, nil
}

// imageExposedPort is the lowest TCP port a locally present image declares.
//
// Lowest rather than first so the same image always proposes the same port,
// and TCP only because a UDP listener is not something a readiness check or a
// proxy route can be built on. An image this host has never pulled, or a
// daemon that cannot answer, is no evidence rather than an error: detection
// has already succeeded on the registry manifest by this point.
func (a *HostSourceAnalyzer) imageExposedPort(ctx context.Context, reference string) (int, string) {
	inspectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	detail, err := a.docker.InspectImage(inspectCtx, reference)
	if err != nil || detail == nil {
		return 0, ""
	}
	best := 0
	for _, exposed := range detail.ExposedPorts {
		value, protocol, _ := strings.Cut(exposed, "/")
		if protocol != "" && !strings.EqualFold(protocol, "tcp") {
			continue
		}
		number, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || number < 1 || number > 65535 {
			continue
		}
		if best == 0 || number < best {
			best = number
		}
	}
	if best == 0 {
		return 0, ""
	}
	return best, fmt.Sprintf("image exposes %d/tcp", best)
}

func (a *HostSourceAnalyzer) analyzeLocalCompose(ctx context.Context, source DraftSourceConfig) (DetectionResult, error) {
	root, err := a.resolveLocalRoot(source.LocalPath, source.Subdirectory)
	if err != nil {
		return DetectionResult{}, err
	}
	identity := SourceIdentity{Kind: SourceCompose, LocalPath: root, CredentialID: source.CredentialID}
	return a.analyzeComposeAtPath(ctx, source, root, identity)
}

func (a *HostSourceAnalyzer) analyzeComposeAtPath(
	ctx context.Context,
	source DraftSourceConfig,
	root string,
	identity SourceIdentity,
) (DetectionResult, error) {
	documents, err := a.readComposeDocuments(root, source.ComposeFiles)
	if err != nil {
		return DetectionResult{}, err
	}
	result, err := a.analyzeCompose(ctx, source, documents, root)
	if err != nil {
		return DetectionResult{}, err
	}
	result.Source.Remote = identity.Remote
	result.Source.Repository = identity.Repository
	result.Source.Ref = identity.Ref
	result.Source.Revision = identity.Revision
	result.Source.LocalPath = identity.LocalPath
	result.Source.Dirty = identity.Dirty
	result.Source.IncludeSubmodules = identity.IncludeSubmodules
	result.Source.IncludeLFS = identity.IncludeLFS
	if source.Mode == SourceModeComposeGit {
		gitEvidence, detectErr := a.detector.DetectPath(ctx, root, identity)
		if detectErr != nil {
			return DetectionResult{}, detectErr
		}
		result.GitRequirements = gitEvidence.GitRequirements
		result.ScannedFiles = gitEvidence.ScannedFiles
		result.ScannedBytes = gitEvidence.ScannedBytes
		result.Truncated = gitEvidence.Truncated
		result.TruncatedReason = gitEvidence.TruncatedReason
		if result.GitRequirements.Submodules {
			result.Candidates[0].Evidence = append(result.Candidates[0].Evidence,
				DetectionEvidence{Path: ".gitmodules", Reason: "Git submodules are declared but not fetched during bounded detection"})
			result.Candidates[0].NeedsDecision = append(result.Candidates[0].NeedsDecision,
				"confirm required submodules and credential access")
		}
		if result.GitRequirements.LFS {
			result.Candidates[0].Evidence = append(result.Candidates[0].Evidence,
				DetectionEvidence{Path: ".gitattributes", Reason: "Git LFS objects are skipped during bounded detection"})
			result.Candidates[0].NeedsDecision = append(result.Candidates[0].NeedsDecision,
				"confirm required Git LFS objects and credential access")
		}
	}
	return result, nil
}

func (a *HostSourceAnalyzer) analyzeCompose(
	ctx context.Context,
	source DraftSourceConfig,
	documents []ComposeDocument,
	projectDirectory string,
) (DetectionResult, error) {
	documents = append([]ComposeDocument(nil), documents...)
	sort.SliceStable(documents, func(i, j int) bool {
		if documents[i].Order != documents[j].Order {
			return documents[i].Order < documents[j].Order
		}
		return documents[i].Path < documents[j].Path
	})
	analysis, err := analyzeComposeDocuments(documents)
	if err != nil {
		return DetectionResult{}, err
	}
	serviceNames := make([]string, 0, len(analysis.Services))
	for _, service := range analysis.Services {
		serviceNames = append(serviceNames, service.Name)
	}
	identity := SourceIdentity{
		Kind: SourceCompose, Digest: analysis.Digest, ComposeFiles: analysis.Files,
		Services: serviceNames, CredentialID: source.CredentialID,
	}
	needs := append([]string{}, analysis.Unsupported...)
	needs = append(needs, analysis.Warnings...)
	candidate := newDetectedCandidate("", BuildCompose, DetectedCandidate{
		Name:    fmt.Sprintf("Compose stack (%d services)", len(serviceNames)),
		Profile: ProfileCompose, Confidence: ConfidenceHigh,
		Evidence:      []DetectionEvidence{{Path: strings.Join(analysis.Files, ", "), Reason: analysis.Digest}},
		NeedsDecision: needs,
	})
	result := DetectionResult{
		Source: identity, Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID,
		Compose: &analysis,
	}
	// Docker Compose is the final parser authority. Its absence is honest but
	// does not erase the structural analysis the UI can still review.
	if a.docker == nil || !a.docker.ComposeAvailable(ctx) {
		result.Unavailable = "Docker Compose validation is unavailable on this host"
		return result, nil
	}
	inputs := make([]dockerx.ComposeInput, 0, len(documents))
	for _, document := range documents {
		inputs = append(inputs, dockerx.ComposeInput{Path: document.Path, Content: document.Content})
	}
	validation, err := a.docker.ValidateComposePlan(ctx, projectDirectory, inputs, analysis.Variables)
	if err != nil {
		return DetectionResult{}, fmt.Errorf("%w: %v", ErrInvalidCompose, err)
	}
	if !validation.Valid {
		return DetectionResult{}, fmt.Errorf("%w: %s", ErrInvalidCompose, validation.Error)
	}
	return result, nil
}

func (a *HostSourceAnalyzer) resolveLocalRoot(path, subdirectory string) (string, error) {
	resolved, err := a.paths.Resolve(path)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w: local source is not a directory", ErrSourceUnavailable)
	}
	return detectionSubdirectory(resolved, subdirectory)
}

func detectionSubdirectory(root, subdirectory string) (string, error) {
	if subdirectory == "" {
		return root, nil
	}
	candidate := filepath.Join(root, filepath.Clean(subdirectory))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("%w: source subdirectory is missing", ErrSourceUnavailable)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: subdirectory escapes source", ErrInvalidSource)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w: source subdirectory is missing", ErrSourceUnavailable)
	}
	return resolved, nil
}

func localGitIdentity(
	ctx context.Context,
	kind SourceKind,
	root string,
	credentialID int64,
	includeSubmodules bool,
	includeLFS bool,
) (SourceIdentity, error) {
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return SourceIdentity{}, fmt.Errorf("%w: %s is not a Git checkout", ErrInvalidSource, root)
	}
	revision, err := runPlanningGit(ctx, root, nil, "rev-parse", "HEAD")
	if err != nil {
		return SourceIdentity{}, fmt.Errorf("%w: %w: %v", ErrSourceUnavailable, ErrGitUnavailable, err)
	}
	remote, _ := runPlanningGit(ctx, root, nil, "remote", "get-url", "origin")
	ref, _ := runPlanningGit(ctx, root, nil, "rev-parse", "--abbrev-ref", "HEAD")
	status, _ := runPlanningGit(ctx, root, nil, "status", "--porcelain", "--untracked-files=normal")
	return SourceIdentity{
		Kind: kind, LocalPath: root, Remote: scrubSourceRemote(strings.TrimSpace(remote)),
		Ref: strings.TrimSpace(ref), Revision: strings.TrimSpace(revision),
		Dirty: strings.TrimSpace(status) != "", CredentialID: credentialID,
		IncludeSubmodules: includeSubmodules, IncludeLFS: includeLFS,
	}, nil
}

func remoteForSource(source DraftSourceConfig) (string, string, error) {
	if source.Mode == SourceModeGitURL || source.Mode == SourceModeComposeGit {
		return normalizeGitRemote(source.URL)
	}
	if !validProvider(source.Provider) || !validRepository(source.Repository) {
		return "", "", fmt.Errorf("%w: invalid connected repository", ErrInvalidSource)
	}
	base := map[string]string{
		"github": "https://github.com", "gitlab": "https://gitlab.com", "bitbucket": "https://bitbucket.org",
	}[source.Provider]
	if source.Provider == "gitea" {
		parsed, err := urlWithoutCredentials(source.ProviderBaseURL)
		if err != nil {
			return "", "", err
		}
		base = strings.TrimSuffix(parsed, "/")
	}
	return base + "/" + source.Repository + ".git", source.Repository, nil
}

func urlWithoutCredentials(raw string) (string, error) {
	remote, _, err := normalizeGitRemote(strings.TrimSuffix(raw, "/") + "/owner/repository.git")
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(remote, "/owner/repository.git"), nil
}

func (a *HostSourceAnalyzer) gitEnvironment(
	ctx context.Context,
	cacheRoot string,
	remote string,
	credentialID int64,
) ([]string, func(), error) {
	environment := cleanPlanningGitEnvironment(os.Environ())
	environment = append(environment,
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_LFS_SKIP_SMUDGE=1",
	)
	if credentialID == 0 {
		return append(environment, "GIT_CONFIG_GLOBAL=/dev/null"), func() {}, nil
	}
	if a.credentials == nil {
		return nil, func() {}, fmt.Errorf("%w: %w: credential store is unavailable", ErrSourceUnavailable, ErrGitUnavailable)
	}
	credential, err := a.credentials.OpenCredential(ctx, credentialID)
	if err != nil {
		return nil, func() {}, err
	}
	defer a.markCredentialUsed(credentialID)
	switch credential.Kind {
	case CredentialProviderToken, CredentialGitBearer:
		return a.gitBearerEnvironment(environment, cacheRoot, remote, credential)
	case CredentialGitSSH:
		return a.gitSSHEnvironment(environment, cacheRoot, remote, credential)
	default:
		return nil, func() {}, fmt.Errorf("%w: %w: credential kind is not valid for Git", ErrSourceUnavailable, ErrGitUnavailable)
	}
}

// gitBasicAuthorization is the form a Git host expects a token in: HTTP Basic
// with the token as the password, which is what the credential form promises
// ("sent as the HTTPS password"). "Bearer" is the REST convention, and
// GitHub's git endpoint answers it exactly as it answers no header at all —
// a username prompt — so a token that read the API fine failed every clone.
// The username is a placeholder every major host ignores, except Bitbucket
// Cloud, which insists on its own.
func gitBasicAuthorization(host, token string) string {
	user := "x-access-token"
	if strings.EqualFold(host, "bitbucket.org") {
		user = "x-token-auth"
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+token))
}

// gitBearerEnvironment scopes an HTTPS bearer token to the exact remote being
// fetched through a private, per-call git config file — never the operator's
// own ~/.gitconfig — so the header cannot leak onto an unrelated host even if
// a redirect changed the remote mid-fetch.
func (a *HostSourceAnalyzer) gitBearerEnvironment(
	environment []string, cacheRoot, remote string, credential CredentialMaterial,
) ([]string, func(), error) {
	if !validGitBearerToken(credential.Secret) {
		return nil, func() {}, fmt.Errorf("%w: %w: Git credential is empty or malformed", ErrSourceUnavailable, ErrGitUnavailable)
	}
	parsedRemote, err := url.Parse(remote)
	if err != nil || parsedRemote.Scheme != "https" || parsedRemote.User != nil {
		return nil, func() {}, fmt.Errorf("%w: %w: bearer credentials require an HTTPS remote", ErrSourceUnavailable, ErrGitUnavailable)
	}
	file, err := os.CreateTemp(cacheRoot, "git-credential-*.config")
	if err != nil {
		return nil, func() {}, err
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	content := "[credential]\n\thelper =\n[http \"" + escapeGitConfigSection(remote) + "\"]\n" +
		"\textraHeader = Authorization: " + gitBasicAuthorization(parsedRemote.Hostname(), credential.Secret) + "\n"
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		cleanup()
		return nil, func() {}, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return append(environment, "GIT_CONFIG_GLOBAL="+path), cleanup, nil
}

// gitSSHEnvironment writes the sealed private key to a private 0600 file for
// the lifetime of this one Git invocation and points GIT_SSH_COMMAND at it
// exclusively. -F /dev/null and both known-hosts files pointed at /dev/null
// keep the connection from ever reading or writing the operator's real
// ~/.ssh: no config aliases, no persisted host keys, no identity but the one
// just written. SSH_AUTH_SOCK is already stripped by
// cleanPlanningGitEnvironment, so an agent identity cannot be tried either.
func (a *HostSourceAnalyzer) gitSSHEnvironment(
	environment []string, cacheRoot, remote string, credential CredentialMaterial,
) ([]string, func(), error) {
	if err := validateCredentialSecret(CredentialGitSSH, credential.Secret); err != nil {
		return nil, func() {}, fmt.Errorf("%w: %w: %v", ErrSourceUnavailable, ErrGitUnavailable, err)
	}
	if !strings.HasPrefix(remote, "git@") && !strings.HasPrefix(remote, "ssh://") {
		return nil, func() {}, fmt.Errorf("%w: %w: an SSH key credential requires an SSH remote", ErrSourceUnavailable, ErrGitUnavailable)
	}
	file, err := os.CreateTemp(cacheRoot, "git-ssh-key-*")
	if err != nil {
		return nil, func() {}, err
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if _, err := file.WriteString(credential.Secret); err != nil {
		_ = file.Close()
		cleanup()
		return nil, func() {}, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	command := "ssh -F /dev/null -o IdentitiesOnly=yes -o IdentityFile=" + shellQuote(path) +
		" -o UserKnownHostsFile=/dev/null -o GlobalKnownHostsFile=/dev/null -o StrictHostKeyChecking=accept-new -o BatchMode=yes"
	return append(environment, "GIT_SSH_COMMAND="+command), cleanup, nil
}

// shellQuote single-quotes value for embedding in GIT_SSH_COMMAND, which Git
// hands to "sh -c". value is always a path this process just created under
// its own cache root, never request-supplied text, but the quoting is cheap
// insurance against a data directory whose path itself needs it.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// markCredentialUsed best-effort records that a credential's material was
// just opened for a real Git operation. HostSourceAnalyzer only holds the
// narrow CredentialReader interface, so this reaches past it to the concrete
// PlanningStore when that is what was wired in; any other reader (a test
// double, for instance) simply does not get usage tracking.
func (a *HostSourceAnalyzer) markCredentialUsed(credentialID int64) {
	if store, ok := a.credentials.(*PlanningStore); ok {
		store.recordCredentialUsed(context.Background(), credentialID)
	}
}

func escapeGitConfigSection(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

func cleanPlanningGitEnvironment(source []string) []string {
	result := make([]string, 0, len(source))
	for _, item := range source {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "GIT_") || strings.HasPrefix(key, "JD_") ||
			strings.HasPrefix(key, "VPSD_") || key == "SSH_AUTH_SOCK" {
			continue
		}
		result = append(result, item)
	}
	return result
}

var gitBearerTokenRE = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

func validGitBearerToken(value string) bool {
	return len(value) <= 4096 && gitBearerTokenRE.MatchString(value)
}

var gitObjectIDRE = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func validGitObjectID(value string) bool { return gitObjectIDRE.MatchString(value) }

func planningGitRef(ref string) (remoteRef, cloneRef string) {
	switch {
	case strings.HasPrefix(ref, "refs/heads/"):
		return ref, strings.TrimPrefix(ref, "refs/heads/")
	case strings.HasPrefix(ref, "refs/tags/"):
		return ref, strings.TrimPrefix(ref, "refs/tags/")
	case providerPullRefRE.MatchString(ref):
		// A pull request head has no branch name of its own on the base
		// repository; the fetch names the provider's ref as it is.
		return ref, ref
	default:
		return "refs/heads/" + ref, ref
	}
}

func (a *HostSourceAnalyzer) registryAuth(ctx context.Context, credentialID int64, imageReference string) (string, error) {
	if credentialID == 0 {
		return "", nil
	}
	if a.credentials == nil {
		return "", fmt.Errorf("%w: %w: credential store is unavailable", ErrSourceUnavailable, ErrDockerUnavailable)
	}
	credential, err := a.credentials.OpenCredential(ctx, credentialID)
	if err != nil {
		return "", err
	}
	defer a.markCredentialUsed(credentialID)
	// Decodes into the same credentialConfig the CRUD routes write, not a
	// private anonymous shape: recordCredentialUsed additively sets
	// lastUsedAt on this exact JSON blob after every open, and a decoder
	// that did not know that field would start refusing every registry
	// credential the moment it was used once.
	var config credentialConfig
	decoder := json.NewDecoder(bytes.NewReader(credential.Config))
	decoder.DisallowUnknownFields()
	if credential.Kind != "registry" || len(credential.Config) == 0 || decoder.Decode(&config) != nil ||
		config.Username == "" && !config.IdentityToken || len(config.Username) > 256 ||
		config.ServerAddress == "" || len(config.ServerAddress) > 512 ||
		strings.ContainsAny(config.Username+config.ServerAddress, "\x00\r\n") || credential.Secret == "" || len(credential.Secret) > 16<<10 {
		return "", fmt.Errorf("%w: %w: registry credential configuration is malformed", ErrSourceUnavailable, ErrDockerUnavailable)
	}
	targetRegistry, err := imageRegistryDomain(imageReference)
	if err != nil || registryCredentialDomain(config.ServerAddress) != targetRegistry {
		return "", fmt.Errorf("%w: %w: registry credential is scoped to another registry", ErrSourceUnavailable, ErrDockerUnavailable)
	}
	payload := map[string]string{"username": config.Username, "serveraddress": config.ServerAddress}
	if config.IdentityToken {
		payload["identitytoken"] = credential.Secret
	} else {
		payload["password"] = credential.Secret
	}
	encoded, _ := json.Marshal(payload)
	// Padded: the daemon decodes X-Registry-Auth with base64.URLEncoding, and an
	// unpadded value fails that decode silently whenever the JSON's length is
	// not a multiple of three — so the login was dropped for most usernames
	// and passwords and the registry saw an anonymous request.
	return base64.URLEncoding.EncodeToString(encoded), nil
}

// TestCredential exercises a saved credential the same way a real deployment
// would: a Git-shaped credential runs git ls-remote through the exact
// gitEnvironment isolation detection and materialization already use, and a
// registry credential resolves a manifest through the exact registryAuth
// path analyzeImage uses. It never returns the secret — only whether the
// attempt worked and a safe description of what happened.
func (a *HostSourceAnalyzer) TestCredential(ctx context.Context, credentialID int64, repository string) (bool, string, error) {
	if a.credentials == nil {
		return false, "", fmt.Errorf("%w: credential store is unavailable", ErrSourceUnavailable)
	}
	credential, err := a.credentials.OpenCredential(ctx, credentialID)
	if err != nil {
		return false, "", err
	}
	var config credentialConfig
	// Best effort: a malformed or absent config_json just leaves target
	// blank, which the git branch below already treats as "no saved host".
	_ = json.Unmarshal(credential.Config, &config)
	repository = strings.TrimSpace(repository)
	switch credential.Kind {
	case CredentialGitBearer, CredentialGitSSH, CredentialProviderToken:
		return a.testGitCredential(ctx, credentialID, credential.Kind, config.Target, repository)
	case CredentialRegistry:
		return a.testRegistryCredential(ctx, credentialID, repository)
	default:
		return false, "", fmt.Errorf("%w: unsupported credential kind %q", ErrInvalidCredential, credential.Kind)
	}
}

func (a *HostSourceAnalyzer) testGitCredential(ctx context.Context, credentialID int64, kind, target, repository string) (bool, string, error) {
	remote, err := gitTestRemote(kind, target, repository)
	if err != nil {
		return false, "", err
	}
	cacheRoot, cleanupCache, err := a.planningCacheRoot()
	if err != nil {
		return false, "", err
	}
	defer cleanupCache()
	environment, cleanupCredential, err := a.gitEnvironment(ctx, cacheRoot, remote, credentialID)
	if err != nil {
		return false, err.Error(), nil
	}
	defer cleanupCredential()
	testCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	safeRemote := scrubSourceRemote(remote)
	if _, err := runPlanningGit(testCtx, "", environment, "ls-remote", "--exit-code", remote); err != nil {
		return false, "could not read " + safeRemote + ": " + err.Error(), nil
	}
	return true, "read " + safeRemote + " successfully", nil
}

// gitTestRemote builds a concrete remote to test from the caller's
// repository — a full Git URL, or an owner/name path combined with the
// credential's own saved target host — so testing a saved credential does
// not require retyping a full URL for the common case.
func gitTestRemote(kind, target, repository string) (string, error) {
	if repository == "" {
		return "", fmt.Errorf("%w: repository is required to test a Git credential", ErrInvalidCredential)
	}
	if remote, _, err := normalizeGitRemote(repository); err == nil {
		return remote, nil
	}
	if !validRepositoryPath(repository) {
		return "", fmt.Errorf("%w: repository must be a Git URL or an owner/name path", ErrInvalidCredential)
	}
	if target == "" {
		return "", fmt.Errorf("%w: this credential has no saved target host; send a full repository URL", ErrInvalidCredential)
	}
	if kind == CredentialGitSSH {
		return "git@" + target + ":" + repository + ".git", nil
	}
	return "https://" + target + "/" + repository + ".git", nil
}

func (a *HostSourceAnalyzer) testRegistryCredential(ctx context.Context, credentialID int64, repository string) (bool, string, error) {
	if repository == "" {
		return false, "", fmt.Errorf("%w: repository is required to test a registry credential", ErrInvalidCredential)
	}
	normalized, err := normalizeImageReference(repository)
	if err != nil {
		return false, "", fmt.Errorf("%w: repository must be a valid image reference", ErrInvalidCredential)
	}
	if a.docker == nil {
		return false, "Docker is unavailable on this host", nil
	}
	auth, err := a.registryAuth(ctx, credentialID, normalized)
	if err != nil {
		return false, err.Error(), nil
	}
	testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resolved, err := a.docker.ResolveDistributionImage(testCtx, normalized, auth)
	if err != nil {
		return false, "could not resolve " + normalized + ": " + err.Error(), nil
	}
	if resolved == nil || resolved.Digest == "" {
		return false, "registry returned no image digest for " + normalized, nil
	}
	return true, "resolved " + normalized + " to " + resolved.Digest, nil
}

func imageRegistryDomain(imageReference string) (string, error) {
	named, err := reference.ParseNormalizedNamed(imageReference)
	if err != nil {
		return "", err
	}
	return normalizeRegistryDomain(reference.Domain(named)), nil
}

func registryCredentialDomain(serverAddress string) string {
	value := strings.TrimSpace(serverAddress)
	if parsed, err := url.Parse(value); err == nil && parsed.Hostname() != "" {
		value = parsed.Host
	} else {
		value = strings.TrimPrefix(strings.TrimPrefix(value, "https://"), "http://")
		value = strings.SplitN(value, "/", 2)[0]
	}
	return normalizeRegistryDomain(strings.ToLower(value))
}

func normalizeRegistryDomain(value string) string {
	switch value {
	case "index.docker.io", "registry-1.docker.io":
		return "docker.io"
	default:
		return value
	}
}

func runPlanningGit(ctx context.Context, dir string, environment []string, args ...string) (string, error) {
	if !hostexec.Available("git") {
		return "", fmt.Errorf("git is not installed")
	}
	command := hostexec.CommandInDir(ctx, dir, "git", args...)
	if environment != nil {
		command.Env = environment
	} else {
		command.Env = append(cleanPlanningGitEnvironment(os.Environ()),
			"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false", "GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_LFS_SKIP_SMUDGE=1")
	}
	hostexec.AsOwner(command)
	output := &boundedWriter{limit: 1 << 20}
	command.Stdout, command.Stderr = output, output
	_, err := hostexec.RunGroup(ctx, command, 2*time.Second)
	if err != nil {
		// Remote-controlled stderr may reflect an Authorization header or
		// credential helper response. Preserve bounded output for successful
		// machine-readable commands, but never propagate failure output into an
		// API error, audit detail, or process log.
		return output.String(), fmt.Errorf("git %s failed: %w", strings.Join(redactedGitArgs(args), " "), err)
	}
	return output.String(), nil
}

func redactedGitArgs(args []string) []string {
	result := append([]string(nil), args...)
	for i, arg := range result {
		if strings.Contains(arg, "://") {
			if remote, _, err := normalizeGitRemote(arg); err == nil {
				result[i] = remote
			} else {
				result[i] = "<remote>"
			}
		}
	}
	return result
}

type boundedWriter struct {
	buffer bytes.Buffer
	limit  int
}

func (w *boundedWriter) Write(data []byte) (int, error) {
	original := len(data)
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = w.buffer.Write(data)
	}
	return original, nil
}

func (w *boundedWriter) String() string { return w.buffer.String() }

func scrubSourceRemote(remote string) string {
	if normalized, _, err := normalizeGitRemote(remote); err == nil {
		return normalized
	}
	return ""
}

func (a *HostSourceAnalyzer) readComposeDocuments(root string, configured []ComposeDocument) ([]ComposeDocument, error) {
	paths := []string{}
	if len(configured) > 0 {
		if len(configured) > 16 {
			return nil, fmt.Errorf("%w: at most 16 Compose files are allowed", ErrInvalidCompose)
		}
		seen := map[string]bool{}
		configured = append([]ComposeDocument(nil), configured...)
		sort.SliceStable(configured, func(i, j int) bool {
			if configured[i].Order != configured[j].Order {
				return configured[i].Order < configured[j].Order
			}
			return configured[i].Path < configured[j].Path
		})
		for _, document := range configured {
			if !safeRelativePath(document.Path) ||
				!(strings.HasSuffix(document.Path, ".yml") || strings.HasSuffix(document.Path, ".yaml")) {
				return nil, fmt.Errorf("%w: Compose path escapes source", ErrInvalidCompose)
			}
			if seen[document.Path] {
				return nil, fmt.Errorf("%w: duplicate Compose path", ErrInvalidCompose)
			}
			seen[document.Path] = true
			paths = append(paths, document.Path)
		}
	} else {
		for _, name := range []string{"compose.yml", "compose.yaml", "docker-compose.yml", "docker-compose.yaml"} {
			if _, err := os.Stat(filepath.Join(root, name)); err == nil {
				paths = append(paths, name)
			}
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%w: no Compose file was found", ErrInvalidCompose)
	}
	documents := make([]ComposeDocument, 0, len(paths))
	total := 0
	for order, path := range paths {
		full := filepath.Join(root, filepath.Clean(path))
		resolved, err := filepath.EvalSymlinks(full)
		if err != nil {
			return nil, fmt.Errorf("%w: read %s", ErrInvalidCompose, path)
		}
		relative, err := filepath.Rel(root, resolved)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%w: Compose path escapes source", ErrInvalidCompose)
		}
		content, err := os.ReadFile(resolved)
		if err != nil {
			return nil, fmt.Errorf("%w: read %s: %v", ErrInvalidCompose, path, err)
		}
		total += len(content)
		if total > 4<<20 {
			return nil, fmt.Errorf("%w: Compose input exceeds 4 MiB", ErrInvalidCompose)
		}
		documents = append(documents, ComposeDocument{Path: path, Content: string(content), Order: order})
	}
	return documents, nil
}

func (a *HostSourceAnalyzer) PreviewImport(ctx context.Context, source DraftSourceConfig) (*ImportPreview, error) {
	if err := source.Validate(); err != nil {
		return nil, err
	}
	if source.Mode == SourceModeExistingCheckout {
		result, err := a.analyzeLocal(ctx, source)
		if err != nil {
			return nil, err
		}
		name := filepath.Base(source.LocalPath)
		configuration := configurationFromDetection(result)
		observed, _ := json.Marshal(result.Source)
		return &ImportPreview{
			Kind: "checkout", ResourceID: source.LocalPath, Name: name, Source: source,
			Configuration: configuration, Observed: observed,
			Unsupported: []string{}, Warnings: []string{}, WouldChange: []string{},
		}, nil
	}
	if a.docker == nil {
		return nil, fmt.Errorf("%w: Docker is unavailable", ErrImportNotFound)
	}
	if source.Mode == SourceModeExistingContainer {
		inspectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		spec, err := a.docker.SpecOf(inspectCtx, source.ResourceID)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrImportNotFound, err)
		}
		return containerImportPreview(source, spec), nil
	}
	inspectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	stacks, err := a.docker.ListStacks(inspectCtx, a.composeRoots)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrImportNotFound, err)
	}
	for _, stack := range stacks {
		if stack.Name == source.ResourceID {
			return stackImportPreview(source, stack), nil
		}
	}
	return nil, ErrImportNotFound
}

func containerImportPreview(source DraftSourceConfig, spec *dockerx.ContainerSpec) *ImportPreview {
	unsupported := []string{}
	command, commandSafe := sanitizedImportedCommand(spec.Command)
	if !commandSafe {
		unsupported = append(unsupported, "command contains credential-shaped arguments and must be re-entered with variable references")
	}
	image := spec.Image
	if normalized, err := normalizeImageReference(image); err == nil {
		image = normalized
	} else {
		unsupported = append(unsupported, "image reference is not reusable as a normalized registry reference")
	}
	configuration := PlanConfiguration{
		Build: BuildPlanConfig{Method: BuildImage},
		Runtime: RuntimePlanConfig{
			Image: image, Command: command, Strategy: StrategyStopFirst,
			Privileged: spec.Privileged, HostNetwork: spec.NetworkMode == "host",
			Capabilities: append([]string(nil), spec.CapAdd...),
		},
		Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{}, Checks: []PlannedCheck{},
	}
	seenVariables := map[string]bool{}
	for _, variable := range spec.Env {
		if ValidateEnvKey(variable.Name) != nil || seenVariables[variable.Name] {
			unsupported = append(unsupported, "environment contains an invalid or duplicate variable name")
			continue
		}
		seenVariables[variable.Name] = true
		sensitivity := "plain"
		if secretShapedKey(variable.Name) {
			sensitivity = "secret"
		}
		configuration.Variables = append(configuration.Variables, PlannedVariable{
			Name: variable.Name, Sensitivity: sensitivity, Scopes: []string{"runtime"}, Required: true,
		})
	}
	for _, port := range spec.Ports {
		if configuration.Runtime.InternalPort == 0 {
			configuration.Runtime.InternalPort = port.ContainerPort
			configuration.Runtime.HostPort = port.HostPort
			configuration.Runtime.BindAddress = port.HostIP
		}
	}
	for _, mount := range spec.Mounts {
		ownership := OwnershipObserved
		configuration.Runtime.Mounts = append(configuration.Runtime.Mounts, RuntimeMount{
			Source: mount.Source, Target: mount.Target, ReadOnly: mount.ReadOnly, Ownership: ownership,
		})
	}
	for _, device := range spec.Devices {
		configuration.Runtime.Devices = append(configuration.Runtime.Devices, device.Host)
	}
	observed := map[string]any{
		"name": spec.Name, "image": image, "command": command,
		"environmentNames": variableNames(configuration.Variables), "ports": spec.Ports,
		"mounts": spec.Mounts, "privileged": spec.Privileged, "networkMode": spec.NetworkMode,
	}
	observedJSON, _ := json.Marshal(observed)
	if spec.AutoRemove {
		unsupported = append(unsupported, "automatic removal")
	}
	if spec.NetworkMode != "" && spec.NetworkMode != "bridge" && spec.NetworkMode != "host" {
		unsupported = append(unsupported, "network mode "+spec.NetworkMode)
	}
	warnings := []string{"Import is observation-only until adoption is confirmed."}
	return &ImportPreview{
		Kind: "container", ResourceID: source.ResourceID, Name: spec.Name, Source: source,
		Configuration: canonicalConfiguration(configuration), Observed: observedJSON,
		Unsupported: unsupported, Warnings: warnings, WouldChange: []string{},
	}
}

func sanitizedImportedCommand(command []string) ([]string, bool) {
	result := append([]string(nil), command...)
	for index, argument := range result {
		if rejectPlanSecretLiteral("imported command", argument) != nil || importSecretFlag(argument) ||
			(index > 0 && importSecretFlag(result[index-1])) {
			return []string{}, false
		}
	}
	return result, true
}

func importSecretFlag(argument string) bool {
	argument = strings.TrimLeft(strings.ToLower(strings.TrimSpace(argument)), "-")
	argument = strings.ReplaceAll(argument, "-", "_")
	return secretShapedKey(argument)
}

func stackImportPreview(source DraftSourceConfig, stack dockerx.ComposeStack) *ImportPreview {
	services := make([]string, 0, len(stack.Services))
	for _, service := range stack.Services {
		services = append(services, service.Name)
	}
	sort.Strings(services)
	observed, _ := json.Marshal(map[string]any{
		"name": stack.Name, "workingDirectory": stack.WorkingDir,
		"configFiles": stack.ConfigFiles, "services": services,
		"running": stack.Running, "total": stack.Total,
	})
	configuration := PlanConfiguration{
		Build:     BuildPlanConfig{Method: BuildCompose},
		Runtime:   RuntimePlanConfig{Strategy: StrategyStopFirst},
		Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{}, Checks: []PlannedCheck{},
	}
	return &ImportPreview{
		Kind: "stack", ResourceID: source.ResourceID, Name: stack.Name, Source: source,
		Configuration: configuration, Observed: observed,
		Unsupported: []string{}, Warnings: []string{"Review every service and persistent path before adoption."},
		WouldChange: []string{},
	}
}

func variableNames(variables []PlannedVariable) []string {
	result := make([]string, 0, len(variables))
	for _, variable := range variables {
		result = append(result, variable.Name)
	}
	sort.Strings(result)
	return result
}

func configurationFromDetection(result DetectionResult) PlanConfiguration {
	configuration := PlanConfiguration{
		Build:     BuildPlanConfig{Method: BuildNone},
		Runtime:   RuntimePlanConfig{Strategy: StrategyStopFirst},
		Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{}, Checks: []PlannedCheck{},
	}
	if len(result.Candidates) == 0 {
		return configuration
	}
	selected := result.Candidates[0]
	for _, candidate := range result.Candidates {
		if candidate.ID == result.SelectedID {
			selected = candidate
			break
		}
	}
	configuration.Build = BuildPlanConfig{
		Method: selected.BuildMethod, RootDirectory: selected.Root,
		BuildCommand: selected.BuildCommand, StartCommand: selected.StartCommand,
		OutputDirectory: selected.OutputDirectory,
	}
	configuration.Runtime.InternalPort = selected.Port
	return configuration
}
