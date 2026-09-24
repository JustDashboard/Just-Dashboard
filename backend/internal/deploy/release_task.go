package deploy

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

type ReleaseTaskEvidence struct {
	Name          string   `json:"name"`
	VariableNames []string `json:"variableNames"`
	DurationMS    int64    `json:"durationMs"`
	ExitCode      int      `json:"exitCode"`
	Runner        string   `json:"runner,omitempty"`
}

func runStoredReleaseTask(
	ctx context.Context,
	root string,
	task ReleaseTaskConfig,
	values map[string]string,
	emit func(BuildLog) error,
) (ReleaseTaskEvidence, hostexec.GroupResult, error) {
	evidence := ReleaseTaskEvidence{Name: task.Name, VariableNames: append([]string(nil), task.Env...), ExitCode: -1}
	sort.Strings(evidence.VariableNames)
	workingDirectory := root
	if task.WorkingDirectory != "" && !safeRelativePath(task.WorkingDirectory) {
		return evidence, hostexec.GroupResult{}, fmt.Errorf("release task working directory is invalid")
	}
	workingDirectory, err := containedSubdirectory(root, task.WorkingDirectory)
	if err != nil {
		return evidence, hostexec.GroupResult{}, fmt.Errorf("release task working directory is unavailable")
	}
	if emit == nil {
		emit = func(BuildLog) error { return nil }
	}
	scoped := map[string]string{}
	for _, name := range task.Env {
		value, ok := values[name]
		if !ok {
			return evidence, hostexec.GroupResult{}, fmt.Errorf("release task variable %s is unavailable", name)
		}
		scoped[name] = value
	}
	taskCtx, cancel := context.WithTimeout(ctx, time.Duration(task.TimeoutSeconds)*time.Second)
	defer cancel()
	// This is the sole normalized host shell boundary. The command is immutable
	// admin-authored plan content; request data and secret values never join it.
	command := hostexec.CommandInDir(taskCtx, workingDirectory, "/bin/sh", "-c", task.Command)
	command.Env = mergeEnv(scoped)
	started := time.Now()
	result, err := streamReleaseTask(taskCtx, command, redactBuildEmitter(scoped, emit))
	evidence.DurationMS = time.Since(started).Milliseconds()
	evidence.ExitCode = result.ExitCode
	if err := ctx.Err(); err != nil {
		return evidence, result, fmt.Errorf("release task %s cancelled: %w", task.Name, err)
	}
	if errors.Is(taskCtx.Err(), context.DeadlineExceeded) {
		return evidence, result, fmt.Errorf("release task %s timed out after %d seconds", task.Name, task.TimeoutSeconds)
	}
	return evidence, result, err
}

func streamReleaseTask(
	ctx context.Context,
	command *exec.Cmd,
	emit func(BuildLog) error,
) (hostexec.GroupResult, error) {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	var emitMu sync.Mutex
	stdout := &releaseTaskLineWriter{stream: "stdout", emit: emit, emitMu: &emitMu, cancel: cancel}
	stderr := &releaseTaskLineWriter{stream: "stderr", emit: emit, emitMu: &emitMu, cancel: cancel}
	command.Stdout, command.Stderr = stdout, stderr
	result, runErr := hostexec.RunGroup(child, command, 5*time.Second)
	stdout.flush()
	stderr.flush()
	emitErr := stdout.err
	if emitErr == nil {
		emitErr = stderr.err
	}
	if emitErr != nil {
		return result, fmt.Errorf("persist release task output: %w", emitErr)
	}
	if runErr != nil {
		return result, runErr
	}
	if result.ExitCode != 0 {
		return result, fmt.Errorf("release task exited with code %d", result.ExitCode)
	}
	return result, nil
}

type releaseTaskLineWriter struct {
	mu       sync.Mutex
	emitMu   *sync.Mutex
	stream   string
	pending  string
	overflow bool
	emit     func(BuildLog) error
	cancel   context.CancelFunc
	err      error
}

func (w *releaseTaskLineWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	start := 0
	for index, value := range data {
		if value != '\n' {
			continue
		}
		w.appendSegment(string(data[start:index]))
		w.emitPending()
		start = index + 1
	}
	w.appendSegment(string(data[start:]))
	return len(data), nil
}

func (w *releaseTaskLineWriter) appendSegment(segment string) {
	if w.overflow || segment == "" {
		return
	}
	if len(w.pending)+len(segment) > maxLogTextBytes {
		w.pending = ""
		w.overflow = true
		return
	}
	w.pending += segment
}

func (w *releaseTaskLineWriter) emitPending() {
	if w.err != nil {
		w.pending, w.overflow = "", false
		return
	}
	text := strings.TrimRight(w.pending, "\r")
	if w.overflow {
		text = "[output line omitted: exceeds 64 KiB]"
	}
	w.pending, w.overflow = "", false
	w.emitMu.Lock()
	err := w.emit(BuildLog{Stream: w.stream, Text: text})
	w.emitMu.Unlock()
	if err != nil {
		w.err = err
		w.cancel()
	}
}

func (w *releaseTaskLineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending != "" || w.overflow {
		w.emitPending()
	}
}

// ReleaseTaskRunnerImage runs a release task in a throwaway container of
// the release's own image (release_task_image.go).
const ReleaseTaskRunnerImage = "image"
