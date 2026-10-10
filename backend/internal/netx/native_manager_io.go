package netx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

var nativeHostPath = hostexec.HostPath
var nativeExecute = nativeExecuteHost

type nativeBoundedOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *nativeBoundedOutput) Write(p []byte) (int, error) {
	remaining := (1 << 20) - b.Len()
	if len(p) > remaining {
		b.overflow = true
		_, _ = b.Buffer.Write(p[:remaining])
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

// Error output may contain native profile properties. It stays private; the
// API receives only the named operation and its exit/cancellation outcome.
func nativeExecuteHost(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
	if execute, ok := ctx.Value(recoveryExecutorKey{}).(recoveryExecutor); ok {
		out, err := execute(ctx, input, tool, args...)
		if err != nil {
			return "", fmt.Errorf("native %s recovery operation was refused or failed", tool)
		}
		if len(out) > 1<<20 {
			return "", errors.New("native owner output exceeded its inspection bound")
		}
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := hostexec.CommandOnHost(ctx, tool, args...)
	var out nativeBoundedOutput
	cmd.Stdout, cmd.Stderr = &out, &out
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	_, err := hostexec.RunGroup(ctx, cmd, 200*time.Millisecond)
	if ctx.Err() != nil {
		return "", fmt.Errorf("native %s operation: %w", tool, ctx.Err())
	}
	if err != nil {
		return "", fmt.Errorf("native %s operation was refused or failed", tool)
	}
	if out.overflow {
		return "", errors.New("native owner output exceeded its inspection bound")
	}
	return out.String(), nil
}

type nativeFileIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	UID    uint32 `json:"uid"`
	GID    uint32 `json:"gid"`
	Mode   uint32 `json:"mode"`
}
type nativeProfileFile struct {
	Path     string             `json:"path"`
	Data     []byte             `json:"data"`
	Identity nativeFileIdentity `json:"identity"`
}

func nativeReadProfile(path string) (*nativeProfileFile, error) {
	if err := nativeTrustedParents(path); err != nil {
		return nil, err
	}
	source := path
	path = nativeHostPath(path)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxNativeProfileBytes {
		return nil, errors.New("native profile is not a bounded regular file")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("native profile cannot be read safely")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("native profile changed while being inspected")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxNativeProfileBytes+1))
	if err != nil || len(data) > maxNativeProfileBytes {
		return nil, errors.New("native profile exceeds the inspection bound")
	}
	stat, ok := opened.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || opened.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("native profile identity cannot be read")
	}
	return &nativeProfileFile{Path: source, Data: data, Identity: nativeFileIdentity{Device: uint64(stat.Dev), Inode: stat.Ino, UID: stat.Uid, GID: stat.Gid, Mode: uint32(opened.Mode().Perm())}}, nil
}

func nativeTrustedParents(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("native profile path is not canonical")
	}
	for dir := filepath.Dir(path); dir != "/"; dir = filepath.Dir(dir) {
		info, err := os.Lstat(nativeHostPath(dir))
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("native profile parent ownership is unreadable")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
			return errors.New("native profile parent is not exclusively root-owned")
		}
	}
	return nil
}

func nativeProfilePath(manager, path string) bool {
	if filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\n\r") {
		return false
	}
	dir, base := filepath.Dir(path), filepath.Base(path)
	if base == "" || strings.HasPrefix(base, ".") || len(base) > 200 {
		return false
	}
	switch manager {
	case "NetworkManager":
		return dir == "/etc/NetworkManager/system-connections" && strings.HasSuffix(base, ".nmconnection")
	case "networkd":
		return dir == "/etc/systemd/network" && strings.HasSuffix(base, ".network")
	case "netplan":
		return dir == "/etc/netplan" && (strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml"))
	}
	return false
}

type nativeBusReply struct {
	Type string            `json:"type"`
	Data []json.RawMessage `json:"data"`
}

type nativeBusEpochKey struct{}

func nativePinnedBus(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, nativeBusEpochKey{}, id)
}

func nativeBusEndpoint(ctx context.Context) (string, error) {
	if id, pinned := ctx.Value(nativeBusEpochKey{}).(string); pinned {
		if !nativeTransaction.MatchString(id) || id == strings.Repeat("0", 32) {
			return "", errors.New("native transport epoch is invalid")
		}
		return "--address=unix:path=/run/dbus/system_bus_socket,guid=" + id, nil
	}
	return "--system", nil
}

func nativeBus(ctx context.Context, service, object, iface, verb string, args ...string) (nativeBusReply, error) {
	endpoint, err := nativeBusEndpoint(ctx)
	if err != nil {
		return nativeBusReply{}, err
	}
	// sd-bus checks the expected GUID during authentication, before any
	// message. A bus restart cannot reuse a saved native object identity.
	argv := []string{endpoint, "--json=short", "--timeout=10", "--auto-start=no", "--allow-interactive-authorization=no", verb, service, object, iface}
	argv = append(argv, args...)
	out, err := nativeExecute(ctx, nil, "busctl", argv...)
	if err != nil {
		return nativeBusReply{}, err
	}
	if strings.TrimSpace(out) == "" {
		return nativeBusReply{}, nil
	}
	var wire struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &wire); err != nil {
		return nativeBusReply{}, errors.New("native owner returned unreadable D-Bus evidence")
	}
	reply := nativeBusReply{Type: wire.Type}
	// busctl serializes a property as its value, while a method reply contains
	// the sequence of return arguments. An array-valued property is one value.
	if verb == "get-property" {
		reply.Data = []json.RawMessage{wire.Data}
	} else if json.Unmarshal(wire.Data, &reply.Data) != nil {
		return reply, errors.New("native owner returned unreadable D-Bus arguments")
	}
	return reply, nil
}
func nativeBusValue[T any](reply nativeBusReply, kind string) (T, error) {
	var value T
	if reply.Type != kind || len(reply.Data) != 1 || json.Unmarshal(reply.Data[0], &value) != nil {
		return value, errors.New("native owner returned unexpected D-Bus evidence")
	}
	return value, nil
}
func nativeBusProperty[T any](ctx context.Context, service, object, iface, property, kind string) (T, error) {
	r, err := nativeBus(ctx, service, object, iface, "get-property", property)
	if err != nil {
		var zero T
		return zero, err
	}
	return nativeBusValue[T](r, kind)
}

var nativeVersionPattern = regexp.MustCompile(`(?:^|[ (])([0-9]+)\.([0-9]+)(?:\.([0-9]+))?`)

func nativeVersionSupported(manager, version string) bool {
	if manager == "networkd" {
		fields := strings.Fields(version)
		if len(fields) < 2 || fields[0] != "systemd" {
			return false
		}
		v, err := strconv.Atoi(fields[1])
		return err == nil && v >= 255 && v <= 257
	}
	m := nativeVersionPattern.FindStringSubmatch(strings.TrimSpace(version))
	if len(m) == 0 {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if manager == "NetworkManager" {
		return major == 1 && minor >= 42 && minor <= 54 && minor%2 == 0
	}
	return manager == "netplan" && major == 1 && minor <= 2
}
