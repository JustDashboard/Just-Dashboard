package procs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

var capturedNodeVersion = regexp.MustCompile(`^v([0-9]+\.[0-9]+\.[0-9]+)$`)
var capturedPythonVersion = regexp.MustCompile(`^Python ([0-9]+\.[0-9]+\.[0-9]+)$`)

// ProbeCapturedInterpreter executes only a positively classified captured ELF
// interpreter with a fixed version flag. No application argv, wrapper, preload,
// inherited environment, user-site package or source directory is consulted.
func ProbeCapturedInterpreter(ctx context.Context, capture *HostWorkloadCapture) (string, error) {
	if capture == nil || !filepath.IsAbs(capture.InterpreterPath) {
		return "", fmt.Errorf("the captured interpreter is unavailable")
	}
	executable := capture.InterpreterPath
	expected := capture.SourceFiles[executable]
	if expected == "" {
		return "", fmt.Errorf("the captured interpreter identity is unavailable")
	}
	file, err := openCaptureFile(hostexec.HostPath(executable))
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 512<<20 {
		return "", ErrHostWorkloadChanged
	}
	model, err := elf.NewFile(file)
	if err != nil {
		return "", fmt.Errorf("the captured executable is not a supported interpreter")
	}
	symbols, _ := model.DynamicSymbols()
	node, python := false, false
	for _, symbol := range symbols {
		node = node || symbol.Name == "_ZN4node5StartEiPPc" || symbol.Name == "node_module_register"
		python = python || symbol.Name == "Py_BytesMain" || symbol.Name == "Py_Main"
	}
	base := filepath.Base(executable)
	node = node && (base == "node" || base == "nodejs")
	python = python && (base == "python3" || regexp.MustCompile(`^python3\.[0-9]{1,2}$`).MatchString(base))
	if node == python {
		return "", fmt.Errorf("the captured executable is not a supported interpreter")
	}
	before, err := CaptureHostSourceFiles([]string{executable})
	if err != nil || before[executable] != expected {
		return "", ErrHostWorkloadChanged
	}
	ownership, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ErrHostWorkloadChanged
	}
	hash := sha256.New()
	fmt.Fprintf(hash, "%d:%d:%d\x00", info.Mode(), ownership.Uid, ownership.Gid)
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if n, err := io.Copy(hash, io.LimitReader(file, (512<<20)+1)); err != nil || n != info.Size() || hex.EncodeToString(hash.Sum(nil)) != expected {
		return "", ErrHostWorkloadChanged
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// The descriptor pins the already classified file across pathname races.
	args := []string{"--reuid", strconv.FormatUint(uint64(capture.UID), 10), "--regid", strconv.FormatUint(uint64(capture.GID), 10), "--clear-groups", "--", "/proc/self/fd/3"}
	if python {
		args = append(args, "-I", "-S")
	}
	args = append(args, "--version")
	cmd := hostexec.CommandOnHostInDir(probeCtx, "/", "setpriv", args...)
	if os.Geteuid() != 0 && uint32(os.Geteuid()) == capture.UID && uint32(os.Getegid()) == capture.GID && hostexec.HostPath(executable) == executable {
		cmd = exec.CommandContext(probeCtx, "/proc/self/fd/3", args[7:]...)
		cmd.Dir = "/"
	}
	cmd.ExtraFiles = []*os.File{file}
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "HOME=/", "PYTHONNOUSERSITE=1"}
	var output boundedInterpreterOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	if _, err := hostexec.RunGroup(probeCtx, cmd, 100*time.Millisecond); err != nil {
		return "", fmt.Errorf("the captured interpreter version could not be verified")
	}
	after, err := CaptureHostSourceFiles([]string{executable})
	if err != nil || after[executable] != expected {
		return "", ErrHostWorkloadChanged
	}
	pattern := capturedNodeVersion
	if python {
		pattern = capturedPythonVersion
	}
	matched := pattern.FindStringSubmatch(strings.TrimSpace(output.String()))
	if len(matched) != 2 {
		return "", fmt.Errorf("the captured interpreter version is unsupported")
	}
	return matched[1], nil
}

type boundedInterpreterOutput struct{ bytes.Buffer }

func (out *boundedInterpreterOutput) Write(data []byte) (int, error) {
	if out.Len()+len(data) > 256 {
		return 0, io.ErrShortBuffer
	}
	return out.Buffer.Write(data)
}
