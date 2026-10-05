package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Local directory sources preserve dirty and non-Git files, rather than
// silently replacing an operator's running tree with its last Git commit.
func (a *HostSourceAnalyzer) analyzeLocalDirectory(ctx context.Context, source DraftSourceConfig) (DetectionResult, error) {
	root, err := a.resolveLocalRoot(source.LocalPath, "")
	if err != nil {
		return DetectionResult{}, err
	}
	digest, err := localDirectoryDigest(ctx, root, source.ExcludePaths)
	if err != nil {
		return DetectionResult{}, err
	}
	selected, err := detectionSubdirectory(root, source.Subdirectory)
	if err != nil {
		return DetectionResult{}, err
	}
	identity := SourceIdentity{Kind: SourceLocal, LocalPath: root, Digest: digest}
	return a.detector.DetectPath(ctx, selected, identity)
}

func (a *HostSourceAnalyzer) inspectLocalDirectory(ctx context.Context, source DraftSourceConfig, identity SourceIdentity, inspect func(string, SourceIdentity) error) error {
	if identity.Kind != SourceLocal || !contentDigestRE.MatchString(identity.Digest) {
		return fmt.Errorf("%w: directory source has no immutable content digest", ErrInvalidSource)
	}
	root, err := a.resolveLocalRoot(source.LocalPath, "")
	if err != nil {
		return err
	}
	a.inspectMu.Lock()
	defer a.inspectMu.Unlock()
	copyCtx, cancel := context.WithTimeout(ctx, localInspectionTimeout)
	defer cancel()
	digest, err := localDirectoryDigest(copyCtx, root, source.ExcludePaths)
	if err != nil || digest != identity.Digest {
		return fmt.Errorf("%w: directory source changed after review", ErrSourceUnavailable)
	}
	cacheRoot, cleanup, err := a.planningCacheRoot()
	if err != nil {
		return err
	}
	defer cleanup()
	target, err := os.MkdirTemp(cacheRoot, "directory-inspect-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(target)
	if err := copyContainedTree(root, target, copyTreeLimits{MaxFiles: localInspectionMaxFiles, MaxBytes: localInspectionMaxBytes, ExcludePrivateFiles: true, ExcludePaths: source.ExcludePaths}); err != nil {
		return err
	}
	digest, err = localDirectoryDigest(copyCtx, target, source.ExcludePaths)
	if err != nil || digest != identity.Digest {
		return fmt.Errorf("%w: directory source changed during inspection", ErrSourceUnavailable)
	}
	selected, err := detectionSubdirectory(target, source.Subdirectory)
	if err != nil {
		return err
	}
	return inspect(selected, identity)
}

func privateSourceEntry(name string) bool {
	lower := strings.ToLower(name)
	if lower == ".env" || strings.HasPrefix(lower, ".env.") {
		return true
	}
	switch lower {
	case ".pm2", ".ssh", ".gnupg", ".aws", ".kube", ".docker", ".dockercfg", ".npmrc", ".yarnrc.yml", ".netrc", ".pypirc", ".git-credentials", ".gitconfig",
		"id_rsa", "id_ed25519", "credentials.json", "service-account.json":
		return true
	}
	switch filepath.Ext(lower) {
	case ".key", ".pem", ".p12", ".pfx", ".jks", ".keystore":
		return true
	}
	return false
}

func localDirectoryDigest(ctx context.Context, root string, excludeLists ...[]string) (string, error) {
	return localDirectoryDigestWithPolicy(ctx, root, false, excludeLists...)
}

func nativeDirectoryDigest(ctx context.Context, root string, exclusions []string) (string, error) {
	// Native restart authority still reads the original tree. Fence private
	// file contents and ownership there without copying those files into builds.
	return localDirectoryDigestWithPolicy(ctx, root, true, exclusions)
}

func localDirectoryDigestWithPolicy(ctx context.Context, root string, native bool, excludeLists ...[]string) (string, error) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: local source root is unavailable", ErrInvalidSource)
	}
	limits := copyTreeLimits{}.normalized()
	entries := 0
	var total int64
	hash := sha256.New()
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return fmt.Errorf("%w: a local source entry could not be read", ErrSourceUnavailable)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." && !native {
			return nil
		}
		for _, list := range excludeLists {
			if excludedLocalSourcePath(relative, list) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == ".just-dashboard") {
			return filepath.SkipDir
		}
		if !native && privateSourceEntry(entry.Name()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		entries++
		if entries > limits.MaxFiles {
			return fmt.Errorf("%w: local source exceeds its entry limit", ErrInvalidSource)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("%w: a local source entry changed", ErrSourceUnavailable)
		}
		var contents string
		mode := info.Mode()
		switch {
		case mode.IsDir():
		case mode&os.ModeSymlink != 0:
			contents, err = os.Readlink(path)
			if err != nil {
				return fmt.Errorf("%w: a source symlink could not be read", ErrSourceUnavailable)
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(relative), contents))
			if filepath.IsAbs(contents) || resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
				return fmt.Errorf("%w: a source symlink escapes its root", ErrInvalidSource)
			}
		case mode.IsRegular():
			file, err := openRegularSnapshotFile(path, info)
			if err != nil {
				return fmt.Errorf("%w: a source file changed or could not be read", ErrSourceUnavailable)
			}
			fileHash := sha256.New()
			n, readErr := io.Copy(fileHash, io.LimitReader(contextSourceReader{ctx, file}, limits.MaxBytes-total+1))
			if native && readErr == nil {
				after, statErr := file.Stat()
				entryAfter, entryErr := os.Lstat(path)
				if statErr != nil || entryErr != nil || !os.SameFile(info, after) || !os.SameFile(info, entryAfter) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) || info.Mode() != after.Mode() {
					readErr = ErrSourceUnavailable
				} else {
					beforeOwner, beforeOK := info.Sys().(*syscall.Stat_t)
					afterOwner, afterOK := after.Sys().(*syscall.Stat_t)
					if !beforeOK || !afterOK || beforeOwner.Uid != afterOwner.Uid || beforeOwner.Gid != afterOwner.Gid {
						readErr = ErrSourceUnavailable
					}
				}
			}
			file.Close()
			total += n
			if readErr != nil || total > limits.MaxBytes {
				return fmt.Errorf("%w: local source exceeds its byte limit or changed while reading", ErrInvalidSource)
			}
			contents = hex.EncodeToString(fileHash.Sum(nil))
		default:
			return fmt.Errorf("%w: local source contains unsupported sockets, devices or special files", ErrInvalidSource)
		}
		var uid, gid *uint32
		if native {
			ownership, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				return fmt.Errorf("%w: source ownership is unavailable", ErrSourceUnavailable)
			}
			uid, gid = &ownership.Uid, &ownership.Gid
		}
		encoded, _ := json.Marshal(struct {
			Path     string
			Mode     uint32
			Contents string
			UID      *uint32 `json:",omitempty"`
			GID      *uint32 `json:",omitempty"`
		}{relative, uint32(mode.Perm()), contents, uid, gid})
		_, _ = hash.Write(encoded)
		_, _ = hash.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func excludedLocalSourcePath(relative string, excluded []string) bool {
	for _, path := range excluded {
		if relative == path || strings.HasPrefix(relative, path+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

type contextSourceReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextSourceReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func copyRegularSnapshotFile(source, target string, mode os.FileMode, size int64) error {
	expected, err := os.Lstat(source)
	if err != nil || !expected.Mode().IsRegular() || expected.Size() != size || expected.Mode().Perm() != mode.Perm() {
		return fmt.Errorf("%w: source file changed during capture", ErrSourceUnavailable)
	}
	input, err := openRegularSnapshotFile(source, expected)
	if err != nil {
		return fmt.Errorf("%w: source file changed during capture", ErrSourceUnavailable)
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(output, io.LimitReader(input, size+1))
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil || n != size {
		return fmt.Errorf("%w: source file changed during capture", ErrSourceUnavailable)
	}
	return os.Chmod(target, mode)
}

func openRegularSnapshotFile(path string, expected os.FileInfo) (*os.File, error) {
	// A source entry can become a FIFO after enumeration. Open without waiting
	// for a writer, then verify the exact regular file before reading anything.
	input, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	actual, err := input.Stat()
	if err != nil || expected == nil || !actual.Mode().IsRegular() || !os.SameFile(expected, actual) ||
		expected.Size() != actual.Size() || !expected.ModTime().Equal(actual.ModTime()) || expected.Mode() != actual.Mode() {
		input.Close()
		return nil, ErrSourceUnavailable
	}
	beforeOwner, beforeOK := expected.Sys().(*syscall.Stat_t)
	afterOwner, afterOK := actual.Sys().(*syscall.Stat_t)
	if beforeOK != afterOK || (beforeOK && (beforeOwner.Uid != afterOwner.Uid || beforeOwner.Gid != afterOwner.Gid)) {
		input.Close()
		return nil, ErrSourceUnavailable
	}
	return input, nil
}
