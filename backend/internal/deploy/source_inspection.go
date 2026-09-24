package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrSourceHasNoTree marks a source whose release is not built from files —
// an image, a blueprint, a pasted or local Compose file, an observed import —
// so there is no checkout to read before deploying it.
var ErrSourceHasNoTree = errors.New("the source has no file tree to inspect")

// SourceInspector reads the tree a deployment would build, before it builds.
// Everything it hands out is a private, temporary checkout that is removed
// when inspect returns; nothing in it is executed.
type SourceInspector interface {
	// InspectRevision checks out identity.Revision of source (the branch head
	// when a remote source's revision is empty) and calls inspect with its
	// root, the source subdirectory applied, and the identity inspected.
	InspectRevision(
		ctx context.Context,
		source DraftSourceConfig,
		identity SourceIdentity,
		inspect func(root string, identity SourceIdentity) error,
	) error
	// ResolveGitRevision is what a remote source's ref points at now.
	ResolveGitRevision(ctx context.Context, source DraftSourceConfig) (string, error)
}

// sourceHasTree says whether a source's release is built from a checkout.
func sourceHasTree(source DraftSourceConfig) bool {
	switch source.Mode {
	case SourceModeGitURL, SourceModeConnectedRepository, SourceModeComposeGit,
		SourceModeLocalCheckout, SourceModeExistingCheckout:
		return true
	}
	return false
}

func (a *HostSourceAnalyzer) InspectRevision(
	ctx context.Context,
	source DraftSourceConfig,
	identity SourceIdentity,
	inspect func(root string, identity SourceIdentity) error,
) error {
	source = canonicalSourceConfig(source)
	if !sourceHasTree(source) {
		return ErrSourceHasNoTree
	}
	if err := source.Validate(); err != nil {
		return err
	}
	if IsRemoteGitSource(source) {
		return a.inspectRemoteGit(ctx, source, identity.Revision, inspect)
	}
	return a.inspectLocalGit(ctx, source, identity, inspect)
}

// inspectLocalGit reads a local checkout at its recorded commit rather than
// its working tree: the release is materialized from that commit, and
// uncommitted edits in the operator's checkout are not what will be built.
func (a *HostSourceAnalyzer) inspectLocalGit(
	ctx context.Context,
	source DraftSourceConfig,
	identity SourceIdentity,
	inspect func(root string, identity SourceIdentity) error,
) error {
	if !validGitObjectID(identity.Revision) {
		return fmt.Errorf("%w: local Git source has no immutable revision", ErrInvalidSource)
	}
	local, err := a.resolveLocalRoot(source.LocalPath, "")
	if err != nil {
		return err
	}
	if _, err := runPlanningGit(ctx, local, nil, "cat-file", "-e", identity.Revision+"^{commit}"); err != nil {
		return fmt.Errorf("%w: recorded local Git object is unavailable", ErrSourceUnavailable)
	}
	cacheRoot, cleanupCache, err := a.planningCacheRoot()
	if err != nil {
		return err
	}
	defer cleanupCache()
	inspections := filepath.Join(cacheRoot, "git-inspections")
	if err := makePrivateDirectory(inspections); err != nil {
		return err
	}
	target, err := os.MkdirTemp(inspections, "inspect-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(target)
	if err := fetchExactGit(ctx, local, local, target, identity.Revision, nil); err != nil {
		return err
	}
	root, err := detectionSubdirectory(target, source.Subdirectory)
	if err != nil {
		return err
	}
	return inspect(root, identity)
}
