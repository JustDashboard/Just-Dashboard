package files

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Compose mounts the real host at /host, while / is the backend image. Verify
// that mount against host PID 1 before presenting its bytes as host evidence.
func storageHostRoot() (string, error) {
	host, err := os.Stat("/host")
	if err != nil || !host.IsDir() {
		return "", nil
	}
	processRoot, err := os.Stat("/proc/1/root")
	if err != nil {
		return "", fmt.Errorf("%w: host filesystem mount could not be verified against PID 1; check the backend host permissions", ErrOutsideRoot)
	}
	if !os.SameFile(host, processRoot) {
		return "", nil
	}
	local, err := os.Stat("/")
	if err != nil || os.SameFile(host, local) {
		return "", nil
	}
	return "/host", nil
}

func (s *Service) storagePath(path string) (string, error) {
	if s.storageHostRoot == "" || path == s.storageHostRoot || strings.HasPrefix(path, s.storageHostRoot+"/") {
		return path, nil
	}
	hostPath := filepath.Join(s.storageHostRoot, path)
	if s.within(hostPath) {
		return s.Resolve(hostPath)
	}
	// A same-path bind (such as /srv) already names host files and respects the
	// configured roots. Never broaden those roots to admit a /host alias.
	current, currentErr := os.Stat(path)
	host, hostErr := os.Stat(hostPath)
	if currentErr == nil && hostErr == nil && os.SameFile(current, host) {
		return path, nil
	}
	return "", fmt.Errorf("%w: the host directory at %s is not permitted by JD_FILE_ROOTS; this container path is not a verified host mirror", ErrOutsideRoot, hostPath)
}

func storageTemporaryPath(path, hostRoot string) bool {
	if temporaryPath(path) {
		return true
	}
	if hostRoot == "" || !strings.HasPrefix(path, hostRoot+"/") {
		return false
	}
	return temporaryPath(strings.TrimPrefix(path, hostRoot))
}
