package netx

import (
	"context"
	"debug/elf"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// The Ubuntu YAML writer changes persistent origins during checkpoint restore.
// Detect the pinned running image rather than guessing from a package name or
// changing that owner's configuration to make an acceptance fixture pass.
func nativeNMOriginGuard(ctx context.Context, owner string) error {
	migrates, err := nativeNMMigratingWriter(ctx, owner)
	if err != nil {
		return err
	}
	if migrates {
		return errors.New("this native daemon migrates saved profiles through Netplan during checkpoint restoration; its exact-origin recovery adapter is not yet verified")
	}
	return nil
}

func nativeNMMigratingWriter(ctx context.Context, owner string) (bool, error) {
	r, err := nativeBus(ctx, "org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus", "call", "GetConnectionUnixProcessID", "s", owner)
	if err != nil {
		return false, errors.New("the native daemon's persistent writer could not be verified")
	}
	pid, err := nativeBusValue[uint32](r, "u")
	if err != nil || pid == 0 || pid > 2147483647 {
		return false, errors.New("the native daemon's process identity could not be verified")
	}
	image, err := os.Open(nativeHostPath("/proc/" + strconv.FormatUint(uint64(pid), 10) + "/exe"))
	if err != nil {
		return false, errors.New("the native daemon's running image could not be inspected")
	}
	defer image.Close()
	info, err := image.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return false, errors.New("the native daemon's running image is outside its inspection bound")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
		return false, errors.New("the native daemon's running image has unverified ownership")
	}
	object, err := elf.NewFile(image)
	if err != nil {
		return false, errors.New("the native daemon's persistent writer features could not be read")
	}
	libraries, err := object.ImportedLibraries()
	if err != nil {
		return false, errors.New("the native daemon's persistent writer features are unreadable")
	}
	return slices.ContainsFunc(libraries, func(name string) bool { return strings.HasPrefix(name, "libnetplan.so") }), nil
}
