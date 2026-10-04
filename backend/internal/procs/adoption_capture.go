package procs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/shirou/gopsutil/v4/process"
)

var ErrHostWorkloadChanged = errors.New("the original host workload changed")

// Capture contains restart authority and private input for an adoption plan.
// Environment and arguments can contain credentials; only their names and the
// compatibility findings may be serialized into discovery or audit responses.
type HostWorkloadCapture struct {
	Manager             string               `json:"manager"`
	ResourceID          string               `json:"resourceId"`
	Name                string               `json:"name"`
	Account             string               `json:"account,omitempty"`
	UID                 uint32               `json:"uid"`
	GID                 uint32               `json:"gid"`
	SourceDirectory     string               `json:"sourceDirectory,omitempty"`
	SourcePath          string               `json:"sourcePath,omitempty"`
	ConfigurationDigest string               `json:"configurationDigest"`
	Processes           []HostProcessCapture `json:"processes"`
	EnvironmentNames    []string             `json:"environmentNames"`
	Blockers            []string             `json:"blockers"`
	Warnings            []string             `json:"warnings"`
	Environment         map[string]string    `json:"-"`
	Command             []string             `json:"-"`
	OriginalConfig      json.RawMessage      `json:"-"`
	SourceFiles         map[string]string    `json:"-"`
}

type HostProcessCapture struct {
	ID          int               `json:"id,omitempty"`
	PID         int32             `json:"pid,omitempty"`
	CreateTime  int64             `json:"createTime,omitempty"`
	State       string            `json:"state"`
	LogSources  []string          `json:"logSources"`
	Environment map[string]string `json:"-"`
	Command     []string          `json:"-"`
}

// CaptureExistingProcess reads the exact initial environment and argv from
// procfs, fenced on both sides by PID creation time. It never signals a process.
func CaptureExistingProcess(ctx context.Context, pid int32, created int64) (*HostWorkloadCapture, error) {
	if pid <= 1 || pid == int32(os.Getpid()) || created <= 0 {
		return nil, ErrHostWorkloadChanged
	}
	p, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return nil, ErrHostWorkloadChanged
	}
	before, err := p.CreateTimeWithContext(ctx)
	if err != nil || before != created {
		return nil, ErrHostWorkloadChanged
	}
	root := os.Getenv("HOST_PROC")
	if root == "" {
		root = "/proc"
	}
	base := filepath.Join(root, strconv.Itoa(int(pid)))
	commandRaw, err := readCaptureFile(filepath.Join(base, "cmdline"))
	if err != nil {
		return nil, fmt.Errorf("the original command could not be read")
	}
	environmentRaw, err := readCaptureFile(filepath.Join(base, "environ"))
	if err != nil {
		return nil, fmt.Errorf("the original environment could not be read")
	}
	command, err := captureNULValues(commandRaw)
	if err != nil || len(command) == 0 {
		return nil, fmt.Errorf("the original command is unavailable")
	}
	environment, err := captureEnvironment(environmentRaw)
	if err != nil {
		return nil, err
	}
	cwd, err := p.CwdWithContext(ctx)
	if err != nil || !filepath.IsAbs(cwd) || strings.HasSuffix(cwd, " (deleted)") {
		return nil, fmt.Errorf("the original working directory is unavailable")
	}
	executable, err := p.ExeWithContext(ctx)
	if err != nil || !filepath.IsAbs(executable) || strings.HasSuffix(executable, " (deleted)") {
		return nil, fmt.Errorf("the original executable is unavailable")
	}
	uids, uidErr := p.UidsWithContext(ctx)
	gids, gidErr := p.GidsWithContext(ctx)
	if uidErr != nil || gidErr != nil || len(uids) < 2 || len(gids) < 2 {
		return nil, fmt.Errorf("the original runtime account could not be verified")
	}
	after, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return nil, ErrHostWorkloadChanged
	}
	identity, err := after.CreateTimeWithContext(ctx)
	if err != nil || identity != created || ctx.Err() != nil {
		return nil, ErrHostWorkloadChanged
	}
	account, _ := p.UsernameWithContext(ctx)
	private, _ := json.Marshal(struct {
		Command     []string
		Environment map[string]string
		CWD         string
		UID         uint32
		GID         uint32
	}{command, environment, cwd, uint32(uids[1]), uint32(gids[1])})
	return &HostWorkloadCapture{
		Manager: "process", ResourceID: strconv.Itoa(int(pid)) + ":" + strconv.FormatInt(created, 10),
		Name: filepath.Base(executable), Account: account, UID: uint32(uids[1]), GID: uint32(gids[1]),
		SourceDirectory: cwd, SourcePath: executable, Command: command, Environment: environment,
		OriginalConfig: private, ConfigurationDigest: captureDigest(private), EnvironmentNames: captureEnvironmentNames(environment),
		Processes: []HostProcessCapture{{PID: pid, CreateTime: created, State: "running", LogSources: []string{}, Environment: environment, Command: command}},
		Blockers:  []string{"This process has no verified restart manager. Automatic cutover cannot restore it if a replacement fails. Configure an authoritative service or PM2 manager before migration."},
		Warnings:  []string{"Open files and live memory do not describe every data dependency. Review persistence and operating-system dependencies before container migration."},
	}, nil
}

// CaptureHostSourceFiles names only authoritative executable/entrypoint paths.
// This fence detects changes before stopping or restoring a baseline; it cannot
// certify arbitrary imported modules or runtime data, which review must cover.
func CaptureHostSourceFiles(paths []string) (map[string]string, error) {
	out := map[string]string{}
	if len(paths) > 32 {
		return nil, fmt.Errorf("too many baseline source files")
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
			return nil, fmt.Errorf("the original entrypoint cannot be verified")
		}
		file, err := os.Open(hostexec.HostPath(path))
		if err != nil {
			return nil, fmt.Errorf("the original entrypoint cannot be read")
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 512<<20 {
			file.Close()
			return nil, fmt.Errorf("the original entrypoint is unavailable or too large")
		}
		hash := sha256.New()
		n, readErr := io.Copy(hash, io.LimitReader(file, 512<<20+1))
		file.Close()
		if readErr != nil || n > 512<<20 {
			return nil, fmt.Errorf("the original entrypoint could not be verified")
		}
		out[path] = hex.EncodeToString(hash.Sum(nil))
	}
	return out, nil
}

func VerifyHostSourceFiles(expected map[string]string) error {
	paths := make([]string, 0, len(expected))
	for path := range expected {
		paths = append(paths, path)
	}
	actual, err := CaptureHostSourceFiles(paths)
	if err != nil {
		return err
	}
	for path, digest := range expected {
		if actual[path] != digest {
			return ErrHostWorkloadChanged
		}
	}
	return nil
}

func readCaptureFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	const limit = 4 << 20
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(data) > limit {
		return nil, fmt.Errorf("capture input is unavailable or too large")
	}
	return data, nil
}

func captureNULValues(data []byte) ([]string, error) {
	if len(data) == 0 {
		return []string{}, nil
	}
	if data[len(data)-1] != 0 {
		return nil, fmt.Errorf("capture input is incomplete")
	}
	parts := bytes.Split(data[:len(data)-1], []byte{0})
	if len(parts) > 4096 {
		return nil, fmt.Errorf("capture input contains too many values")
	}
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if !utf8.Valid(part) {
			return nil, fmt.Errorf("capture input is not valid UTF-8")
		}
		values = append(values, string(part))
	}
	return values, nil
}

var captureEnvKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func captureEnvironment(data []byte) (map[string]string, error) {
	values, err := captureNULValues(data)
	if err != nil {
		return nil, fmt.Errorf("the original environment cannot be represented safely")
	}
	out := make(map[string]string, len(values))
	for _, entry := range values {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !captureEnvKey.MatchString(key) {
			return nil, fmt.Errorf("the original environment contains unsupported variable names")
		}
		if _, exists := out[key]; exists {
			return nil, fmt.Errorf("the original environment contains duplicate variable names")
		}
		out[key] = value
	}
	return out, nil
}

func captureEnvironmentNames(values map[string]string) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func captureDigest(data []byte) string {
	value := sha256.Sum256(data)
	return hex.EncodeToString(value[:])
}
