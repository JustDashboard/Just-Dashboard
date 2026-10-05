package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
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

// Bounds on the copy of a local checkout's commit that an inspection reads.
// The copy is a checkout of the whole commit, made on a request's behalf —
// a project page's arrival, a Review — so a commit larger than this is not
// copied: the check reads detection's evidence instead, and the deployment
// reads the commit itself before it builds.
const (
	localInspectionMaxFiles = 20_000
	localInspectionMaxBytes = 256 << 20
	localInspectionTimeout  = time.Minute
)

// errInspectionTooLarge marks a commit an inspection declines to copy.
var errInspectionTooLarge = errors.New("the commit is too large to copy for inspection")

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
	a.inspectMu.Lock()
	defer a.inspectMu.Unlock()
	copyCtx, cancel := context.WithTimeout(ctx, localInspectionTimeout)
	defer cancel()
	if _, err := runPlanningGit(copyCtx, local, nil, "cat-file", "-e", identity.Revision+"^{commit}"); err != nil {
		return fmt.Errorf("%w: recorded local Git object is unavailable", ErrSourceUnavailable)
	}
	if err := boundLocalGitTree(copyCtx, local, identity.Revision); err != nil {
		return err
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
	if err := fetchExactGit(copyCtx, local, local, target, identity.Revision, nil); err != nil {
		if copyCtx.Err() != nil {
			return copyCtx.Err()
		}
		return err
	}
	root, err := detectionSubdirectory(target, source.Subdirectory)
	if err != nil {
		return err
	}
	return inspect(root, identity)
}

// boundLocalGitTree lists the commit's tree as it streams and refuses one
// with more files or bytes than an inspection copies, before anything is
// copied.
func boundLocalGitTree(ctx context.Context, repository, revision string) error {
	if !hostexec.Available("git") {
		return fmt.Errorf("%w: git is not installed", ErrGitUnavailable)
	}
	budget := &gitTreeBudget{maxFiles: localInspectionMaxFiles, maxBytes: localInspectionMaxBytes}
	command := hostexec.CommandInDir(ctx, repository, "git", "ls-tree", "-r", "-l", "-z", "--full-tree", revision)
	command.Env = append(cleanPlanningGitEnvironment(os.Environ()),
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_LFS_SKIP_SMUDGE=1")
	hostexec.AsOwner(command)
	command.Stdout, command.Stderr = budget, &boundedWriter{limit: 4096}
	_, err := hostexec.RunGroup(ctx, command, 2*time.Second)
	switch {
	case budget.exceeded:
		return errInspectionTooLarge
	case ctx.Err() != nil:
		return ctx.Err()
	case err != nil:
		return fmt.Errorf("%w: recorded local Git tree could not be listed", ErrSourceUnavailable)
	}
	return nil
}

// gitTreeBudget counts a `git ls-tree -r -l -z` listing — one NUL-ended
// record per file, its size the fourth field — and stops it once the tree
// is past either bound, which ends the listing early.
type gitTreeBudget struct {
	pending  []byte
	files    int
	bytes    int64
	maxFiles int
	maxBytes int64
	exceeded bool
}

func (b *gitTreeBudget) Write(data []byte) (int, error) {
	b.pending = append(b.pending, data...)
	for {
		end := bytes.IndexByte(b.pending, 0)
		if end < 0 {
			break
		}
		header, _, _ := bytes.Cut(b.pending[:end], []byte{'\t'})
		b.pending = b.pending[end+1:]
		b.files++
		if fields := strings.Fields(string(header)); len(fields) == 4 {
			if size, err := strconv.ParseInt(fields[3], 10, 64); err == nil {
				b.bytes += size
			}
		}
		if b.files > b.maxFiles || b.bytes > b.maxBytes {
			b.exceeded = true
			return 0, errInspectionTooLarge
		}
	}
	// No path Git stores is this long; a record that is must not grow
	// the buffer without end.
	if len(b.pending) > 64<<10 {
		b.exceeded = true
		return 0, errInspectionTooLarge
	}
	return len(data), nil
}
