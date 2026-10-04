package dockerx

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
)

var adoptionImageCapture sync.Mutex

type adoptionImageManifest struct {
	Version       int    `json:"version"`
	ID            string `json:"id"`
	SourceDigest  string `json:"sourceDigest"`
	ArchiveDigest string `json:"archiveDigest"`
	Platform      string `json:"platform"`
}

// RecoverAdoptionImage snapshots only the merged rootfs. It sends no signal,
// pauses no process, executes no command and copies no mounted volume data.
// Runtime environment/commands remain in the encrypted deployment plan, not
// the new image configuration. A private manifest makes repeat recovery reuse
// the same exact image; only positively identified Linux platforms are allowed.
func (c *Client) RecoverAdoptionImage(ctx context.Context, capture *AdoptionContainer, cacheRoot string, excludePaths []string) (*ImageDetail, error) {
	if capture == nil || capture.Inspection.ID == "" || capture.Inspection.ImageManifestDescriptor == nil || capture.Inspection.ImageManifestDescriptor.Platform == nil {
		return nil, errors.New("the missing original image has no authoritative platform descriptor; restore its image or attach its original source")
	}
	platform := capture.Inspection.ImageManifestDescriptor.Platform
	if platform.OS != "linux" || (platform.Architecture != "amd64" && platform.Architecture != "arm64") || (platform.Variant != "" && (platform.Architecture != "arm64" || platform.Variant != "v8")) {
		return nil, errors.New("the missing original image platform cannot be safely snapshotted")
	}
	platformName := platform.OS + "/" + platform.Architecture
	if platform.Variant != "" {
		platformName += "/" + platform.Variant
	}
	adoptionImageCapture.Lock()
	defer adoptionImageCapture.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	before, err := cli.ContainerInspect(ctx, capture.Inspection.ID)
	if err != nil {
		return nil, errors.New("original runtime is unavailable for image recovery")
	}
	beforeChanges, err := cli.ContainerDiff(ctx, before.ID)
	if err != nil {
		return nil, errors.New("original writable layer is unavailable for image recovery")
	}
	if adoptionImageFingerprint(before, beforeChanges) != adoptionImageFingerprint(capture.Inspection, capture.Changes) {
		return nil, errors.New("original runtime changed before its image could be recovered")
	}
	excludePaths = append([]string(nil), excludePaths...)
	sort.Strings(excludePaths)
	fingerprint := adoptionHash([]byte(adoptionImageFingerprint(before, beforeChanges)), []byte(platformName), adoptionJSON(excludePaths))
	directory, err := os.OpenRoot(cacheRoot)
	if err != nil {
		return nil, errors.New("private image recovery storage is unavailable")
	}
	defer directory.Close()
	if err := directory.MkdirAll("images/"+fingerprint, 0o700); err != nil {
		return nil, errors.New("private image recovery storage is unavailable")
	}
	base := "images/" + fingerprint + "/"
	manifestFile, err := openAdoptionImageFile(directory, base+"manifest.json", os.O_RDONLY)
	if err == nil {
		var manifest adoptionImageManifest
		decodeErr := json.NewDecoder(io.LimitReader(manifestFile, 8192)).Decode(&manifest)
		manifestFile.Close()
		if decodeErr == nil && manifest.Version == 1 && manifest.SourceDigest == fingerprint && manifest.Platform == platformName {
			cached, err := c.InspectImage(ctx, manifest.ID)
			if err == nil && cached.ID == manifest.ID && cached.Labels["io.just-dashboard.adoption-source"] == fingerprint && len(cached.Env) == 0 && len(cached.Entrypoint) == 0 && len(cached.Command) == 0 {
				return cached, nil
			}
		}
	}
	archiveFile, err := openAdoptionImageFile(directory, base+"rootfs.tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY)
	if err != nil {
		return nil, errors.New("private filesystem snapshot cannot be staged")
	}
	defer directory.Remove(base + "rootfs.tmp")
	boundedWriter, err := newAdoptionArchiveWriter(archiveFile)
	if err != nil {
		archiveFile.Close()
		return nil, err
	}
	stream, err := cli.ContainerExport(ctx, before.ID)
	if err != nil {
		archiveFile.Close()
		return nil, errors.New("original root filesystem cannot be read without changing the workload")
	}
	defer stream.Close()
	hash := sha256.New()
	writer := tar.NewWriter(io.MultiWriter(boundedWriter, hash))
	limited := &io.LimitedReader{R: stream, N: adoptionImageArchiveLimit + 1}
	reader := tar.NewReader(limited)
	exclusions := map[string]bool{}
	for _, name := range excludePaths {
		exclusions[strings.TrimPrefix(path.Clean(name), "/")] = true
	}
	var size int64
	count := 0
	for {
		header, readErr := reader.Next()
		if readErr == io.EOF {
			if limited.N <= 0 {
				archiveFile.Close()
				return nil, errors.New("original filesystem exceeds the safe image recovery bounds")
			}
			break
		}
		if readErr != nil {
			archiveFile.Close()
			return nil, errors.New("original filesystem snapshot could not be fully captured")
		}
		name := strings.TrimPrefix(path.Clean(header.Name), "/")
		count++
		size += header.Size
		if name == ".." || strings.HasPrefix(name, "../") || count > 100000 || header.Size < 0 || size > adoptionImageArchiveLimit {
			archiveFile.Close()
			return nil, errors.New("original filesystem exceeds the safe image recovery bounds")
		}
		if exclusions[name] {
			continue
		}
		if err := writer.WriteHeader(header); err != nil {
			archiveFile.Close()
			if errors.Is(err, errAdoptionImageSpace) {
				return nil, err
			}
			return nil, errors.New("private filesystem snapshot could not be staged")
		}
		if _, err := io.CopyN(writer, reader, header.Size); err != nil {
			archiveFile.Close()
			if errors.Is(err, errAdoptionImageSpace) {
				return nil, err
			}
			return nil, errors.New("private filesystem snapshot could not be staged")
		}
	}
	if err := errors.Join(writer.Close(), archiveFile.Sync(), archiveFile.Close()); err != nil {
		if errors.Is(err, errAdoptionImageSpace) {
			return nil, errAdoptionImageSpace
		}
		return nil, errors.New("private filesystem snapshot could not be completed")
	}
	after, err := cli.ContainerInspect(ctx, before.ID)
	if err != nil {
		return nil, errors.New("original runtime changed during image capture")
	}
	afterChanges, err := cli.ContainerDiff(ctx, before.ID)
	if err != nil || adoptionImageFingerprint(before, beforeChanges) != adoptionImageFingerprint(after, afterChanges) {
		return nil, errors.New("original runtime changed during image capture")
	}
	if err := directory.Rename(base+"rootfs.tmp", base+"rootfs.tar"); err != nil {
		return nil, errors.New("private filesystem snapshot could not be retained")
	}
	archive, err := openAdoptionImageFile(directory, base+"rootfs.tar", os.O_RDONLY)
	if err != nil {
		return nil, errors.New("private filesystem snapshot is unavailable")
	}
	defer archive.Close()
	archiveInfo, err := archive.Stat()
	if err != nil {
		return nil, errors.New("private filesystem snapshot is unavailable")
	}
	if err := checkAdoptionImageImportSpace(archive, archiveInfo.Size()); err != nil {
		return nil, err
	}
	response, err := cli.ImageImport(ctx, image.ImportSource{Source: archive, SourceName: "-"}, "just-dashboard/adoption-recovery", image.ImportOptions{Tag: fingerprint, Platform: platformName, Changes: []string{"LABEL io.just-dashboard.adoption-source=" + fingerprint}})
	if err != nil {
		return nil, errors.New("private filesystem snapshot could not be imported")
	}
	defer response.Close()
	decoder := json.NewDecoder(io.LimitReader(response, 1<<20))
	imageID := ""
	for {
		var message struct {
			Status string `json:"status"`
			Error  string `json:"error"`
			Aux    struct {
				ID string `json:"ID"`
			} `json:"aux"`
		}
		if err := decoder.Decode(&message); err == io.EOF {
			break
		} else if err != nil || message.Error != "" {
			return nil, errors.New("private filesystem snapshot could not be imported")
		}
		if strings.HasPrefix(message.Status, "sha256:") && len(message.Status) == 71 {
			imageID = message.Status
		}
		if message.Aux.ID != "" {
			imageID = message.Aux.ID
		}
	}
	if imageID == "" {
		return nil, errors.New("recovered image identity was not returned")
	}
	recovered, err := c.InspectImage(ctx, imageID)
	if err != nil || recovered.ID != imageID || recovered.OS != platform.OS || recovered.Architecture != platform.Architecture || len(recovered.Env) > 0 || len(recovered.Entrypoint) > 0 || len(recovered.Command) > 0 || recovered.Labels["io.just-dashboard.adoption-source"] != fingerprint {
		return nil, errors.New("recovered image identity or secret-free configuration could not be verified")
	}
	manifest := adoptionImageManifest{Version: 1, ID: imageID, SourceDigest: fingerprint, ArchiveDigest: "sha256:" + hex.EncodeToString(hash.Sum(nil)), Platform: platformName}
	file, err := openAdoptionImageFile(directory, base+"manifest.tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY)
	if err != nil {
		return nil, errors.New("recovered image proof could not be retained")
	}
	_, writeErr := file.Write(adoptionJSON(manifest))
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return nil, errors.New("recovered image proof could not be retained")
	}
	if err := directory.Rename(base+"manifest.tmp", base+"manifest.json"); err != nil {
		return nil, errors.New("recovered image proof could not be retained")
	}
	return recovered, nil
}

func openAdoptionImageFile(root *os.Root, name string, flags int) (*os.File, error) {
	parent, err := root.Open(filepath.Dir(name))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	// os.Root resolves contained symlinks itself. Open the final component with
	// openat so a cache artifact symlink cannot be followed, even inside the root.
	fd, err := syscall.Openat(int(parent.Fd()), filepath.Base(name), (flags&^os.O_TRUNC)|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		file.Close()
		return nil, errors.New("image recovery artifact must be a private regular file")
	}
	if flags&os.O_TRUNC != 0 {
		if err := file.Truncate(0); err != nil {
			file.Close()
			return nil, err
		}
	}
	return file, nil
}

func adoptionImageFingerprint(inspection container.InspectResponse, changes []container.FilesystemChange) string {
	changes = append([]container.FilesystemChange(nil), changes...)
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Path != changes[j].Path {
			return changes[i].Path < changes[j].Path
		}
		return changes[i].Kind < changes[j].Kind
	})
	state := inspection.State
	var process any
	if state != nil {
		process = struct {
			PID                               int
			Running, Paused, Restarting, Dead bool
			StartedAt, FinishedAt             string
		}{state.Pid, state.Running, state.Paused, state.Restarting, state.Dead, state.StartedAt, state.FinishedAt}
	}
	return adoptionHash(adoptionJSON(struct {
		ID, Image  string
		Config     *container.Config
		Host       *container.HostConfig
		Mounts     []container.MountPoint
		Network    *container.NetworkSettings
		Process    any
		Descriptor any
	}{inspection.ID, inspection.Image, inspection.Config, inspection.HostConfig, inspection.Mounts, inspection.NetworkSettings, process, inspection.ImageManifestDescriptor}), adoptionJSON(changes))
}

func adoptionJSON(value any) []byte { content, _ := json.Marshal(value); return content }
func adoptionHash(parts ...[]byte) string {
	sum := sha256.New()
	for _, part := range parts {
		sum.Write(part)
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}
