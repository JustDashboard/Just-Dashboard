package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type MaterializedSource struct {
	Workspace string `json:"workspace"`
	Root      string `json:"root"`
	Revision  string `json:"revision,omitempty"`
	Digest    string `json:"digest,omitempty"`
	// Commit summarizes the exact commit a Git source resolved to, so the run
	// metadata can carry a subject and author next to the sha it already
	// records. It is only ever set for a Git source, and only on a best-effort
	// basis: an unreadable history never fails materialization.
	Commit *CommitMetadata `json:"commit,omitempty"`
}

// CommitMetadata is the human-readable summary of the commit a Git run built.
type CommitMetadata struct {
	SHA        string `json:"sha"`
	Subject    string `json:"subject"`
	Author     string `json:"author"`
	AuthoredAt string `json:"authoredAt"`
}

// gitSourceMode reports whether a source mode resolves to a real Git
// checkout on disk, which is what a commit summary can be read from.
func gitSourceMode(mode SourceMode) bool {
	switch mode {
	case SourceModeGitURL, SourceModeConnectedRepository, SourceModeComposeGit,
		SourceModeLocalCheckout, SourceModeExistingCheckout:
		return true
	default:
		return false
	}
}

// commitMetadataForSource reads the recorded revision's subject, author and
// date from whichever repository the materializer already has open. It is
// best-effort: the caller logs and moves on rather than failing the step when
// history cannot be read, exactly like an unavailable digest would be for any
// other UI affordance sourced from Git.
func commitMetadataForSource(ctx context.Context, mode SourceMode, repository, revision string) *CommitMetadata {
	if !gitSourceMode(mode) || revision == "" {
		return nil
	}
	commit, err := readCommitMetadata(ctx, repository, revision)
	if err != nil {
		return nil
	}
	return commit
}

// unitSeparator delimits the git log fields below. Commit subjects and author
// names are free text but never contain the ASCII unit separator.
const unitSeparator = "\x1f"

// readCommitMetadata reads one commit's summary with a single, cheap `git
// log`. The repository is expected to already hold the object locally —
// either a materialized workspace or a release mirror — so this performs no
// network access of its own.
func readCommitMetadata(ctx context.Context, repository, revision string) (*CommitMetadata, error) {
	// revision reaches git log as a bare argv element; refusing anything that
	// is not an immutable object id before it gets there closes the same
	// argument-injection door a leading "-" would otherwise open.
	if !validGitObjectID(revision) {
		return nil, fmt.Errorf("%w: commit metadata revision is not an immutable Git object id", ErrInvalidRef)
	}
	// The trailing bare "--" disambiguates revision from a pathspec without
	// filtering by path; putting it before revision would do the opposite and
	// make git search history for a path named after the sha.
	output, err := runPlanningGit(ctx, repository, nil, "log", "-1",
		"--format=%H"+unitSeparator+"%s"+unitSeparator+"%an"+unitSeparator+"%aI", revision, "--")
	if err != nil {
		return nil, err
	}
	fields := strings.SplitN(strings.TrimRight(output, "\n"), unitSeparator, 4)
	if len(fields) != 4 || fields[0] == "" {
		return nil, fmt.Errorf("unexpected git log output for %s", revision)
	}
	subject := fields[1]
	if runes := []rune(subject); len(runes) > 200 {
		subject = string(runes[:200])
	}
	return &CommitMetadata{SHA: fields[0], Subject: subject, Author: fields[2], AuthoredAt: fields[3]}, nil
}

// releaseWorkspaceRef names the one ref a materialized workspace carries, so
// the fetched objects stay reachable for the life of the run.
const releaseWorkspaceRef = "refs/just-dashboard/release"

type materializedSourceMarker struct {
	Source   DraftSourceConfig `json:"source"`
	Identity SourceIdentity    `json:"identity"`
	Root     string            `json:"root"`
}

// Materialize creates a private, deterministic workspace for one run. Remote
// and local Git sources are checked out at the recorded object id; an operator
// checkout is never reset or used as the mutable build context.
func (a *HostSourceAnalyzer) Materialize(
	ctx context.Context,
	source DraftSourceConfig,
	identity SourceIdentity,
	runID int64,
	workspaceRoot string,
) (*MaterializedSource, error) {
	if runID <= 0 || workspaceRoot == "" {
		return nil, fmt.Errorf("%w: materialization workspace is invalid", ErrInvalidSource)
	}
	source = canonicalSourceConfig(source)
	if err := source.ValidateForDeployment(); err != nil {
		return nil, err
	}
	var err error
	source, err = source.withoutBlueprintSecretInputs()
	if err != nil {
		return nil, err
	}
	if err := makePrivateDirectory(workspaceRoot); err != nil {
		return nil, err
	}
	workspace := filepath.Join(workspaceRoot, "run-"+strconv.FormatInt(runID, 10))
	markerPath := filepath.Join(workspace, "source.json")
	sourceRoot := filepath.Join(workspace, "source")
	if existing, err := readMaterializedMarker(markerPath); err == nil {
		if sameMaterializedIdentity(existing, source, identity) {
			root, rootErr := containedSubdirectory(sourceRoot, source.Subdirectory)
			if rootErr == nil {
				return &MaterializedSource{
					Workspace: workspace, Root: root,
					Revision: immutableSourceRevision(identity), Digest: identity.Digest,
					Commit: commitMetadataForSource(ctx, source.Mode, sourceRoot, identity.Revision),
				}, nil
			}
		}
	}
	// workspace is derived only from the trusted root and numeric run id.
	if err := os.RemoveAll(workspace); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil {
		return nil, err
	}

	switch source.Mode {
	case SourceModeGitURL, SourceModeConnectedRepository, SourceModeComposeGit:
		if err := a.materializeRemoteGit(ctx, source, identity, sourceRoot); err != nil {
			_ = os.RemoveAll(workspace)
			return nil, err
		}
	case SourceModeLocalCheckout, SourceModeExistingCheckout:
		if identity.Revision == "" {
			_ = os.RemoveAll(workspace)
			return nil, fmt.Errorf("%w: local Git source has no immutable revision", ErrInvalidSource)
		}
		if err := a.materializeLocalGit(ctx, source, identity, sourceRoot); err != nil {
			_ = os.RemoveAll(workspace)
			return nil, err
		}
	case SourceModeComposeLocal:
		local, err := a.resolveLocalRoot(source.LocalPath, "")
		if err != nil {
			_ = os.RemoveAll(workspace)
			return nil, err
		}
		if err := copyContainedTree(local, sourceRoot, copyTreeLimits{}); err != nil {
			_ = os.RemoveAll(workspace)
			return nil, err
		}
	case SourceModeComposePaste, SourceModeComposeUpload:
		for _, document := range source.ComposeFiles {
			if !safeRelativePath(document.Path) {
				_ = os.RemoveAll(workspace)
				return nil, fmt.Errorf("%w: Compose materialization path is invalid", ErrInvalidCompose)
			}
			path := filepath.Join(sourceRoot, filepath.Clean(document.Path))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				_ = os.RemoveAll(workspace)
				return nil, err
			}
			if err := os.WriteFile(path, []byte(document.Content), 0o600); err != nil {
				_ = os.RemoveAll(workspace)
				return nil, err
			}
		}
	case SourceModeImageReference:
		// An image release has no filesystem source. Keeping the empty,
		// private workspace makes cleanup and evidence uniform.
	case SourceModeExistingContainer, SourceModeExistingStack:
		_ = os.RemoveAll(workspace)
		return nil, fmt.Errorf("%w: observed imports must be adopted before materialization", ErrUnsupportedSource)
	case SourceModeBlueprint:
		// A blueprint release is an image release with reviewed defaults; it
		// has no filesystem source either. Blueprints that would need files
		// written here are refused by ValidateForDeployment above.
	default:
		_ = os.RemoveAll(workspace)
		return nil, ErrUnsupportedSource
	}
	root, err := containedSubdirectory(sourceRoot, source.Subdirectory)
	if err != nil {
		_ = os.RemoveAll(workspace)
		return nil, err
	}
	marker := materializedSourceMarker{Source: source, Identity: identity, Root: root}
	raw, _ := json.Marshal(marker)
	if err := os.WriteFile(markerPath, raw, 0o600); err != nil {
		_ = os.RemoveAll(workspace)
		return nil, err
	}
	return &MaterializedSource{
		Workspace: workspace, Root: root,
		Revision: immutableSourceRevision(identity), Digest: identity.Digest,
		Commit: commitMetadataForSource(ctx, source.Mode, sourceRoot, identity.Revision),
	}, nil
}

func (a *HostSourceAnalyzer) materializeRemoteGit(
	ctx context.Context,
	source DraftSourceConfig,
	identity SourceIdentity,
	target string,
) error {
	if !validGitObjectID(identity.Revision) {
		return fmt.Errorf("%w: remote Git identity has no immutable object id", ErrInvalidRef)
	}
	remote, _, err := remoteForSource(source)
	if err != nil {
		return err
	}
	if identity.Remote != remote {
		return fmt.Errorf("%w: saved remote identity changed", ErrInvalidSource)
	}
	cacheRoot, cleanupCache, err := a.planningCacheRoot()
	if err != nil {
		return err
	}
	defer cleanupCache()
	environment, cleanupCredential, err := a.gitEnvironment(ctx, cacheRoot, remote, source.CredentialID)
	if err != nil {
		return err
	}
	defer cleanupCredential()

	a.gitMu.Lock()
	defer a.gitMu.Unlock()
	// Planning keeps its own shallow, blob-filtered mirror so inspection stays
	// bounded. A release needs every object of one revision, and those two
	// shapes cannot share a mirror: a later planning fetch re-shallows it and
	// the promisor configuration keeps large blobs absent for good.
	mirrorRoot := filepath.Join(cacheRoot, "git-release-mirrors")
	if err := makePrivateDirectory(mirrorRoot); err != nil {
		return err
	}
	mirror := filepath.Join(mirrorRoot, strings.TrimPrefix(digestBytes([]byte(remote)), "sha256:")+".git")
	if err := ensurePlanningMirror(ctx, mirror, remote, environment); err != nil {
		return err
	}
	if err := fetchReleaseRevision(ctx, mirror, environment, source.Ref, identity.Revision); err != nil {
		return err
	}
	if err := fetchExactGit(ctx, mirror, remote, target, identity.Revision, nil); err != nil {
		return err
	}
	return materializeGitExtras(ctx, target, environment, source)
}

func (a *HostSourceAnalyzer) materializeLocalGit(
	ctx context.Context,
	source DraftSourceConfig,
	identity SourceIdentity,
	target string,
) error {
	local, err := a.resolveLocalRoot(source.LocalPath, "")
	if err != nil {
		return err
	}
	if _, err := runPlanningGit(ctx, local, nil, "cat-file", "-e", identity.Revision+"^{commit}"); err != nil {
		return fmt.Errorf("%w: recorded local Git object is unavailable", ErrSourceUnavailable)
	}
	if err := fetchExactGit(ctx, local, local, target, identity.Revision, nil); err != nil {
		return err
	}
	return materializeGitExtras(ctx, target, nil, source)
}

// fetchReleaseRevision makes sure the release mirror holds the recorded
// commit, fetching the source's ref when it does not. The ref may have moved
// on since the run was planned — a push in between, or a mirror cleared since
// — and a full fetch of it still brings the recorded commit as an ancestor,
// which is what the release builds. Only a commit the fetched history no
// longer contains is gone, as after a force-push.
func fetchReleaseRevision(ctx context.Context, mirror string, environment []string, ref, revision string) error {
	present := func() bool {
		_, err := runPlanningGit(ctx, mirror, environment, "cat-file", "-e", revision+"^{commit}")
		return err == nil
	}
	if present() {
		return nil
	}
	remoteRef, _ := planningGitRef(ref)
	releaseRef := "refs/just-dashboard/releases/" + revision
	if _, err := runPlanningGit(ctx, mirror, environment,
		"fetch", "--force", "--no-tags", "origin", "+"+remoteRef+":"+releaseRef); err != nil {
		return sourceFailure(err, "recorded Git object is no longer available")
	}
	if present() {
		return nil
	}
	return &SourceFailure{Code: "source_revision_unavailable", Message: revisionRewrittenMessage}
}

// revisionRewrittenMessage is the recorded commit missing from everything the
// branch holds now, which git itself did not complain about.
const revisionRewrittenMessage = "the recorded commit is no longer in the branch's history on the remote; it was rewritten, as by a force-push"

// fetchExactGit materializes exactly one revision into a fresh workspace
// repository. Cloning is deliberately avoided: `git clone --local` silently
// ignores the local copy when its source is shallow and falls back to the wire
// protocol, which returns nothing from a mirror that publishes no branch refs
// and leaves the failure to surface as an unreadable tree at checkout.
func fetchExactGit(
	ctx context.Context,
	repository, origin, target, revision string,
	environment []string,
) error {
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	if _, err := runPlanningGit(ctx, "", environment, "init", "--quiet", "--", target); err != nil {
		return fmt.Errorf("%w: contained release workspace could not be created", ErrSourceUnavailable)
	}
	if _, err := runPlanningGit(ctx, target, environment, "remote", "add", "origin", origin); err != nil {
		return fmt.Errorf("%w: contained release workspace could not be created", ErrSourceUnavailable)
	}
	fetchCtx, cancelFetch := context.WithTimeout(ctx, 10*time.Minute)
	_, err := runPlanningGit(fetchCtx, target, environment,
		"fetch", "--force", "--no-tags", "--depth=1", "--", repository,
		"+"+revision+":"+releaseWorkspaceRef)
	cancelFetch()
	if err != nil {
		return sourceFailure(err, "exact release fetch failed")
	}
	checkoutCtx, cancelCheckout := context.WithTimeout(ctx, 10*time.Minute)
	_, err = runPlanningGit(checkoutCtx, target, environment, "checkout", "--detach", "--force", revision)
	cancelCheckout()
	if err != nil {
		return fmt.Errorf("%w: exact release checkout failed", ErrSourceUnavailable)
	}
	resolved, err := runPlanningGit(ctx, target, environment, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(resolved) != revision {
		return fmt.Errorf("%w: materialized Git revision does not match the release", ErrSourceUnavailable)
	}
	return nil
}

func materializeGitExtras(
	ctx context.Context,
	target string,
	environment []string,
	source DraftSourceConfig,
) error {
	if source.IncludeSubmodules {
		submoduleCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
		_, err := runPlanningGit(submoduleCtx, target, environment,
			"submodule", "update", "--init", "--recursive", "--depth=1")
		cancel()
		if err != nil {
			return fmt.Errorf("%w: required Git submodules could not be materialized", ErrSourceUnavailable)
		}
	}
	if source.IncludeLFS {
		lfsCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
		_, err := runPlanningGit(lfsCtx, target, environment, "lfs", "pull")
		cancel()
		if err != nil {
			return fmt.Errorf("%w: required Git LFS objects could not be materialized", ErrSourceUnavailable)
		}
	}
	return nil
}

func containedSubdirectory(root, relative string) (string, error) {
	target := root
	if relative != "" {
		if !safeRelativePath(relative) {
			return "", fmt.Errorf("%w: source subdirectory is invalid", ErrInvalidSource)
		}
		target = filepath.Join(root, filepath.Clean(relative))
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("%w: source root is unavailable", ErrSourceUnavailable)
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", fmt.Errorf("%w: source subdirectory is unavailable", ErrSourceUnavailable)
	}
	rel, err := filepath.Rel(realRoot, realTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: source subdirectory escapes its materialized root", ErrInvalidSource)
	}
	info, err := os.Stat(realTarget)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w: source subdirectory is unavailable", ErrSourceUnavailable)
	}
	return realTarget, nil
}

func readMaterializedMarker(path string) (materializedSourceMarker, error) {
	var marker materializedSourceMarker
	raw, err := os.ReadFile(path)
	if err != nil {
		return marker, err
	}
	if err := json.Unmarshal(raw, &marker); err != nil {
		return marker, err
	}
	return marker, nil
}

func sameMaterializedIdentity(marker materializedSourceMarker, source DraftSourceConfig, identity SourceIdentity) bool {
	leftSource, _ := json.Marshal(canonicalSourceConfig(marker.Source))
	rightSource, _ := json.Marshal(canonicalSourceConfig(source))
	leftIdentity, _ := json.Marshal(marker.Identity)
	rightIdentity, _ := json.Marshal(identity)
	return string(leftSource) == string(rightSource) && string(leftIdentity) == string(rightIdentity)
}

type copyTreeLimits struct {
	MaxFiles int
	MaxBytes int64
}

func (limits copyTreeLimits) normalized() copyTreeLimits {
	if limits.MaxFiles <= 0 {
		limits.MaxFiles = 100_000
	}
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = 1 << 30
	}
	return limits
}

func copyContainedTree(source, target string, limits copyTreeLimits) error {
	limits = limits.normalized()
	sourceInfo, err := os.Lstat(source)
	if err != nil || !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: local source root is not a regular directory", ErrInvalidSource)
	}
	files := 0
	var bytesCopied int64
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == ".just-dashboard") {
			return filepath.SkipDir
		}
		files++
		if files > limits.MaxFiles {
			return fmt.Errorf("%w: local source exceeds %d entries", ErrInvalidSource, limits.MaxFiles)
		}
		destination := filepath.Join(target, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		switch {
		case mode.IsDir():
			return os.MkdirAll(destination, mode.Perm()&0o777)
		case mode&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(link) {
				return fmt.Errorf("%w: absolute source symlink %s", ErrInvalidSource, relative)
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(relative), link))
			if resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
				return fmt.Errorf("%w: source symlink %s escapes its root", ErrInvalidSource, relative)
			}
			if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
				return err
			}
			return os.Symlink(link, destination)
		case mode.IsRegular():
			bytesCopied += info.Size()
			if bytesCopied > limits.MaxBytes {
				return fmt.Errorf("%w: local source exceeds %d bytes", ErrInvalidSource, limits.MaxBytes)
			}
			if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
				return err
			}
			return copyRegularFile(path, destination, mode.Perm()&0o777)
		default:
			return fmt.Errorf("%w: special source entry %s is not supported", ErrInvalidSource, relative)
		}
	})
}

func copyRegularFile(source, target string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	return errors.Join(copyErr, closeErr)
}

func (source MaterializedSource) Cleanup() (bool, error) {
	if source.Workspace == "" || filepath.Base(source.Workspace) == "." ||
		!strings.HasPrefix(filepath.Base(source.Workspace), "run-") {
		return false, fmt.Errorf("refusing to clean an invalid deployment workspace")
	}
	if _, err := os.Lstat(source.Workspace); os.IsNotExist(err) {
		return true, nil
	}
	if err := os.RemoveAll(source.Workspace); err != nil {
		return false, err
	}
	return true, nil
}
