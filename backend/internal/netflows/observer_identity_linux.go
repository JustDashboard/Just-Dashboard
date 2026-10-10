package netflows

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"golang.org/x/sys/unix"
)

func openHostCgroup(relative string) (*os.File, error) {
	if !strings.HasPrefix(relative, "/") || filepath.Clean(relative) != relative || strings.ContainsAny(relative, "\x00\r\n") {
		return nil, fmt.Errorf("invalid native cgroup identity")
	}
	root, err := os.Open(hostexec.HostPath("/"))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	path := filepath.Join("sys/fs/cgroup", strings.TrimPrefix(relative, "/"))
	fd, err := unix.Openat2(int(root.Fd()), path, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "owned-observer-cgroup"), nil
}

// Namespace switching is limited to a dedicated, locked OS thread and raw socket
// syscalls. No Go networking work runs in that thread. If restoring its original
// namespace fails, it exits while locked so the runtime discards the thread.
func pinnedNamespaceCookie(target *os.File) (uint64, error) {
	type result struct {
		cookie uint64
		err    error
	}
	out := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		restored := true
		defer func() {
			if restored {
				runtime.UnlockOSThread()
			}
		}()
		original, err := os.Open(fmt.Sprintf("/proc/self/task/%d/ns/net", unix.Gettid()))
		if err != nil {
			out <- result{err: err}
			return
		}
		defer original.Close()
		if namespaceIdentity(original) != namespaceIdentity(target) {
			if err = unix.Setns(int(target.Fd()), unix.CLONE_NEWNET); err != nil {
				out <- result{err: err}
				return
			}
			restored = false
		}
		fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
		var cookie uint64
		if err == nil {
			cookie, err = unix.GetsockoptUint64(fd, unix.SOL_SOCKET, unix.SO_NETNS_COOKIE)
			_ = unix.Close(fd)
		}
		if !restored {
			if restoreErr := unix.Setns(int(original.Fd()), unix.CLONE_NEWNET); restoreErr != nil {
				out <- result{err: fmt.Errorf("namespace-cookie thread restoration failed: %w", restoreErr)}
				return
			}
			restored = true
		}
		out <- result{cookie, err}
	}()
	r := <-out
	return r.cookie, r.err
}

type observerBinding struct {
	source                   *dockerx.NetworkSource
	group, namespace         *os.File
	groupID, namespaceCookie uint64
	namespaceIdentity        string
	capturedAt               time.Time
}

func (b *observerBinding) close() {
	if b.group != nil {
		b.group.Close()
	}
	if b.namespace != nil {
		b.namespace.Close()
	}
}
func observerCgroupPath(pid int, id string) (string, error) {
	data, err := readProc(pid, "cgroup")
	if err != nil || len(data) > 65536 {
		return "", fmt.Errorf("process cgroup unreadable")
	}
	for _, line := range strings.Split(data, "\n") {
		if strings.HasPrefix(line, "0::/") {
			path := strings.TrimPrefix(line, "0::")
			if cgroupID(line) != id {
				return "", fmt.Errorf("container cgroup ID mismatch")
			}
			return path, nil
		}
	}
	return "", fmt.Errorf("container has no verified cgroup v2 identity")
}
func captureObserverBindings(ctx context.Context, docker DockerSources) ([]observerBinding, int, error) {
	if docker == nil {
		return nil, 0, fmt.Errorf("Docker identity source unavailable")
	}
	containers, err := docker.ListRunning(ctx)
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(containers, func(i, j int) bool { return containers[i].ID < containers[j].ID })
	const cap = 16
	omitted := max(0, len(containers)-cap)
	if len(containers) > cap {
		containers = containers[:cap]
	}
	out := []observerBinding{}
	for index, container := range containers {
		if ctx.Err() != nil {
			omitted += len(containers) - index
			break
		}
		source, err := docker.NetworkSource(ctx, container.ID)
		if err != nil || source == nil {
			omitted++
			continue
		}
		b := observerBinding{source: source, capturedAt: time.Now().UTC()}
		path, err := observerCgroupPath(source.PID, source.ID)
		if err == nil {
			b.group, err = openHostCgroup(path)
		}
		if err == nil {
			b.groupID, err = cgroupHandle(b.group)
		}
		if err == nil {
			b.namespace, err = os.Open(hostexec.HostPath(fmt.Sprintf("/proc/%d/ns/net", source.PID)))
		}
		if err == nil {
			b.namespaceIdentity = namespaceIdentity(b.namespace)
			b.namespaceCookie, err = pinnedNamespaceCookie(b.namespace)
		}
		if err == nil {
			err = verifyObserverBinding(ctx, docker, &b)
		}
		if err != nil || b.namespaceCookie == 0 {
			b.close()
			omitted++
			continue
		}
		out = append(out, b)
	}
	return out, omitted, nil
}
func verifyObserverBinding(ctx context.Context, docker DockerSources, b *observerBinding) error {
	current, err := docker.NetworkSource(ctx, b.source.ID)
	if err != nil || current == nil || current.PID != b.source.PID || current.StartedAt != b.source.StartedAt || current.StartTicks != b.source.StartTicks {
		return fmt.Errorf("container instance changed")
	}
	path, err := observerCgroupPath(current.PID, current.ID)
	if err != nil {
		return err
	}
	fresh, err := openHostCgroup(path)
	if err != nil {
		return err
	}
	defer fresh.Close()
	id, err := cgroupHandle(fresh)
	if err != nil || id != b.groupID {
		return fmt.Errorf("container cgroup changed")
	}
	ns, err := os.Open(hostexec.HostPath(fmt.Sprintf("/proc/%d/ns/net", current.PID)))
	if err != nil {
		return err
	}
	defer ns.Close()
	if namespaceIdentity(ns) != b.namespaceIdentity || namespaceIdentity(b.namespace) != b.namespaceIdentity {
		return fmt.Errorf("container namespace changed")
	}
	return nil
}
func observerOwner(b *Bucket, bindings []observerBinding, valid map[uint64]bool) (Owner, bool) {
	cg, err := strconv.ParseUint(b.SocketCgroup, 10, 64)
	if err != nil {
		return b.Socket.Owner, false
	}
	ns, err := strconv.ParseUint(strings.TrimPrefix(b.Namespace, "cookie:"), 10, 64)
	if err != nil {
		return b.Socket.Owner, false
	}
	for _, binding := range bindings {
		if binding.groupID == cg && binding.namespaceCookie == ns && valid[cg] && !b.FirstSeen.Before(binding.capturedAt) {
			return Owner{Status: "verified_container", ContainerID: binding.source.ID, ContainerName: binding.source.Name, ContainerStartedAt: binding.source.StartedAt, Reason: "Fresh container instance, pinned cgroup handle and namespace cookie match this kernel event."}, true
		}
	}
	return b.Socket.Owner, false
}
