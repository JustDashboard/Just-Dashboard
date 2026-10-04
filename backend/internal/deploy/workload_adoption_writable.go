package deploy

import (
	"path"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/docker/docker/api/types/container"
)

func recoverableWritableLayer(capture *dockerx.AdoptionContainer) (bool, bool) {
	additions := []string{}
	bytecode := false
	for _, change := range capture.Changes {
		if engineGeneratedFile(change.Path) || verifiedMountDirectory(capture, change) {
			continue
		}
		mode, known := capture.ChangeModes[change.Path]
		if change.Kind != container.ChangeAdd || !known || !mode.IsRegular() {
			continue
		}
		if path.Base(path.Dir(change.Path)) == "__pycache__" && strings.HasSuffix(path.Base(change.Path), ".pyc") && path.Clean(change.Path) == change.Path && strings.HasPrefix(change.Path, "/") {
			additions, bytecode = append(additions, change.Path), true
		} else if change.Path == "/usr/sbin/docker-init" && capture.Inspection.HostConfig.Init != nil && *capture.Inspection.HostConfig.Init {
			additions = append(additions, change.Path)
		}
	}
	for _, change := range capture.Changes {
		if engineGeneratedFile(change.Path) || verifiedMountDirectory(capture, change) {
			continue
		}
		covered := false
		for _, addition := range additions {
			if change.Path == addition && change.Kind == container.ChangeAdd {
				covered = true
				break
			}
			mode, known := capture.ChangeModes[change.Path]
			if known && mode.IsDir() && (change.Kind == container.ChangeAdd || change.Kind == container.ChangeModify) && strings.HasPrefix(addition, strings.TrimSuffix(change.Path, "/")+"/") {
				covered = true
				break
			}
		}
		if !covered {
			return false, false
		}
	}
	return true, bytecode
}

func verifiedMountDirectory(capture *dockerx.AdoptionContainer, change container.FilesystemChange) bool {
	mode, known := capture.ChangeModes[change.Path]
	if !known || !mode.IsDir() || (change.Kind != container.ChangeAdd && change.Kind != container.ChangeModify) {
		return false
	}
	for _, mount := range capture.Inspection.Mounts {
		if mount.Destination == change.Path {
			return true
		}
	}
	return false
}
