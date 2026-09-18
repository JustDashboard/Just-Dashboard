package deploy

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type GitChangedPathResolver interface {
	ResolveGitChangedPaths(context.Context, DraftSourceConfig, string, string) ([]string, error)
}

// Compare immutable trees in a private mirror. Provider payloads can omit or
// truncate changed paths, and checking out untrusted files is unnecessary here.
func (a *HostSourceAnalyzer) ResolveGitChangedPaths(ctx context.Context, source DraftSourceConfig, before, after string) ([]string, error) {
	source = canonicalSourceConfig(source)
	if err := source.ValidateForDeployment(); err != nil {
		return nil, err
	}
	if !IsRemoteGitSource(source) || !validGitObjectID(before) || !validGitObjectID(after) {
		return nil, ErrInvalidRef
	}
	if before == after {
		return []string{}, nil
	}
	remote, _, err := remoteForSource(source)
	if err != nil {
		return nil, err
	}
	root, cleanup, err := a.planningCacheRoot()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	environment, cleanupCredential, err := a.gitEnvironment(ctx, root, remote, source.CredentialID)
	if err != nil {
		return nil, err
	}
	defer cleanupCredential()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	a.gitMu.Lock()
	defer a.gitMu.Unlock()
	mirrorRoot := filepath.Join(root, "git-watch-mirrors")
	if err := makePrivateDirectory(mirrorRoot); err != nil {
		return nil, err
	}
	mirror := filepath.Join(mirrorRoot, strings.TrimPrefix(digestBytes([]byte(remote)), "sha256:")+".git")
	if err := ensurePlanningMirror(ctx, mirror, remote, environment); err != nil {
		return nil, err
	}
	for _, revision := range []string{before, after} {
		if _, err := runPlanningGit(ctx, mirror, environment, "cat-file", "-e", revision+"^{commit}"); err == nil {
			continue
		}
		if _, err := runPlanningGit(ctx, mirror, environment, "fetch", "--force", "--no-tags", "--filter=blob:none", "origin", "+"+revision+":refs/just-dashboard/watch/"+revision); err != nil {
			return nil, fmt.Errorf("%w: complete changed paths could not be read", ErrSourceUnavailable)
		}
	}
	output, err := runPlanningGit(ctx, mirror, environment, "diff", "--no-ext-diff", "--no-textconv", "--name-only", "--no-renames", "-z", before, after, "--")
	if err != nil || len(output) >= 1<<20 || (output != "" && !strings.HasSuffix(output, "\x00")) {
		return nil, fmt.Errorf("%w: changed paths are unavailable or exceed the safe comparison limit", ErrSourceUnavailable)
	}
	if output == "" {
		return []string{}, nil
	}
	paths := strings.Split(strings.TrimSuffix(output, "\x00"), "\x00")
	if len(paths) > 10000 {
		return nil, fmt.Errorf("%w: too many changed paths to evaluate", ErrSourceUnavailable)
	}
	for _, name := range paths {
		if name == "" || strings.HasPrefix(name, "/") || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") {
			return nil, ErrInvalidSource
		}
	}
	return paths, nil
}
