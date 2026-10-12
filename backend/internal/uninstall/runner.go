package uninstall

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// The program the sibling container runs: `just-dashboard -self-uninstall`.
//
// Everything it needs is in the record on disk. It takes the stack down —
// which takes away the browser that was watching — and then removes the
// dashboard's images and, for a wipe, its files. Only taking the stack down
// can fail the run: past that point the dashboard is gone either way, and a
// file that could not be deleted is a line in the transcript, not a reason
// to stop deleting the others.

// Host is what the runner does to the machine, so a test can watch it
// without one.
type Host interface {
	// Docker runs the Docker CLI in dir, streaming its output.
	Docker(ctx context.Context, dir string, out io.Writer, args ...string) error
	// Output runs the Docker CLI in dir and returns what it printed.
	DockerOutput(ctx context.Context, dir string, args ...string) ([]byte, error)
	// Run runs a program on the host, in the host's namespaces.
	Run(ctx context.Context, out io.Writer, name string, args ...string) error
	// Path is a host path as this process can open it.
	Path(path string) string
}

// RunUninstall carries out the uninstall recorded in stateDir.
func RunUninstall(stateDir string) error {
	store := NewStore(stateDir)
	run, err := store.Load()
	if err != nil {
		return fmt.Errorf("read the uninstall record: %w", err)
	}
	if run == nil {
		return fmt.Errorf("no uninstall was requested (%s is not there)", store.Path())
	}
	if !run.Status.Live() {
		return fmt.Errorf("the uninstall recorded in %s already finished as %s", store.Path(), run.Status)
	}
	logFile, err := store.AppendLog()
	if err != nil {
		return fmt.Errorf("open the transcript: %w", err)
	}
	defer logFile.Close()
	writers := []io.Writer{logFile, os.Stdout}
	host := hostSystem{}
	// The copy that outlives a wipe. The backend started it; this appends.
	if durable, err := os.OpenFile(host.Path(DurableLog), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		defer durable.Close()
		writers = append(writers, durable)
	}
	return carryOut(context.Background(), store, run, io.MultiWriter(writers...), host)
}

func carryOut(ctx context.Context, store *Store, run *Run, out io.Writer, host Host) error {
	_ = store.Update(func(r *Run) {
		r.Status = StatusRunning
		r.Phase = PhaseStack
	})
	var warnings []string
	warn := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		fmt.Fprintln(out, line)
		warnings = append(warnings, line)
	}
	compose := run.Compose
	if compose == "" {
		compose = "docker-compose.yml"
	}

	fmt.Fprintf(out, "\nClosing the web terminal's sessions\n")
	// Held sessions run as transient units on the host so they survive a
	// restart of the dashboard. That is exactly why they would survive its
	// removal too, running a holder binary out of a directory about to go.
	if err := host.Run(ctx, out, "systemctl", "stop", "jd-terminal-*"); err != nil {
		fmt.Fprintf(out, "no held sessions were stopped: %v\n", err)
	}

	// Asked before the stack goes, while compose can still read the .env.
	built := builtImages(ctx, host, run.Dir, compose)

	if run.Mode == ModeKeep {
		if err := keepEnv(host, run); err != nil {
			warn("could not keep a copy of %s: %v", run.EnvPath, err)
		} else {
			fmt.Fprintf(out, "kept a copy of %s as %s\n", run.EnvPath, filepath.Join(run.DataDir, KeptEnv))
		}
	}

	fmt.Fprintf(out, "\nStopping Just Dashboard\n")
	args := []string{"compose", "-f", compose}
	if run.Project != "" {
		args = append(args, "-p", run.Project)
	}
	args = append(args, "down", "--remove-orphans", "--timeout", "30")
	if run.Mode == ModeWipe {
		// The proxy's two volumes hold its certificate authority, which is
		// the dashboard's data as much as the database is.
		args = append(args, "--volumes")
	}
	fmt.Fprintf(out, "$ docker %s\n", strings.Join(args, " "))
	if err := host.Docker(ctx, run.Dir, out, args...); err != nil {
		fmt.Fprintf(out, "\nFAILED: %v\n", err)
		// Half a stack is the worst place to leave an operator: no dashboard
		// to read this on, and no way to run it again. Put it back.
		fmt.Fprintf(out, "\nStarting the dashboard again\n")
		up := []string{"compose", "-f", compose, "up", "-d"}
		fmt.Fprintf(out, "$ docker %s\n", strings.Join(up, " "))
		if upErr := host.Docker(ctx, run.Dir, out, up...); upErr != nil {
			err = fmt.Errorf("%w; starting it again failed too: %v — run `docker compose up -d` in %s", err, upErr, run.Dir)
		}
		_ = store.Update(func(r *Run) { r.Warnings = append(r.Warnings, warnings...) })
		_ = store.Finish(err)
		return err
	}

	_ = store.Update(func(r *Run) { r.Phase = PhaseFiles })
	fmt.Fprintf(out, "\nRemoving what is left\n")
	for _, name := range []string{"just-dashboard-updater", "just-dashboard-reconfigure"} {
		_ = host.Docker(ctx, run.Dir, io.Discard, "rm", "-f", name)
	}
	for _, image := range built {
		if image == run.Image {
			continue
		}
		if err := host.Docker(ctx, run.Dir, io.Discard, "image", "rm", image); err != nil {
			warn("could not remove image %s: %v", image, err)
		} else {
			fmt.Fprintf(out, "removed image %s\n", image)
		}
	}
	removeTree(ctx, host, out, warn, "/tmp/just-dashboard")

	if run.Mode == ModeWipe {
		wipeData(ctx, host, out, warn, run)
		if run.DeleteBackups && run.BackupDir != "" {
			removeTree(ctx, host, out, warn, run.BackupDir)
		}
		removeEnv(host, out, warn, run.Dir)
	}

	// The image this runs from cannot be removed while it runs, so the host
	// is asked to do it once this container has gone.
	if run.Image != "" {
		err := host.Run(ctx, io.Discard, "systemd-run", "--unit", "just-dashboard-uninstall-"+strings.ToLower(run.ID),
			"--description", "Remove the Just Dashboard image", "--on-active=30", "--collect", "--quiet",
			"--", "/usr/bin/env", "docker", "image", "rm", run.Image)
		if err != nil {
			warn("the image %s is still on this server; remove it with `docker image rm %s`", run.Image, run.Image)
		} else {
			fmt.Fprintf(out, "the image %s is removed in 30 seconds, once this container has exited\n", run.Image)
		}
	}

	fmt.Fprintf(out, "\nJust Dashboard is uninstalled.\n")
	if run.Mode == ModeKeep {
		fmt.Fprintf(out, "Its data is in %s; reinstall from %s with `sudo ./install.sh` to pick it up again.\n", run.DataDir, run.Dir)
	} else {
		fmt.Fprintf(out, "The source checkout is still at %s; delete it whenever you like.\n", run.Dir)
	}
	if len(warnings) > 0 {
		fmt.Fprintf(out, "%d thing%s could not be removed; each is listed above.\n", len(warnings), plural(len(warnings), "", "s"))
	}
	// After a wipe the record went with the data directory, so these two
	// writes land nowhere; the transcript in DurableLog is the record.
	_ = store.Update(func(r *Run) { r.Warnings = append(r.Warnings, warnings...) })
	_ = store.Finish(nil)
	return nil
}

// builtImages are the images the stack builds rather than pulls: the two the
// dashboard is made of. A pulled image — the proxy's Caddy — is the same one
// the deployments' ingress and anything else on the server may be running.
func builtImages(ctx context.Context, host Host, dir, compose string) []string {
	raw, err := host.DockerOutput(ctx, dir, "compose", "-f", compose, "config", "--format", "json")
	if err != nil {
		return nil
	}
	var config struct {
		Services map[string]struct {
			Image string          `json:"image"`
			Build json.RawMessage `json:"build"`
		} `json:"services"`
	}
	if json.Unmarshal(raw, &config) != nil {
		return nil
	}
	var images []string
	for _, svc := range config.Services {
		if len(svc.Build) > 0 && string(svc.Build) != "null" && svc.Image != "" {
			images = append(images, svc.Image)
		}
	}
	sort.Strings(images)
	return images
}

// keepEnv leaves the key to the kept data beside it, where a reinstall from a
// fresh clone finds it.
func keepEnv(host Host, run *Run) error {
	b, err := os.ReadFile(host.Path(run.EnvPath))
	if err != nil {
		return err
	}
	return os.WriteFile(host.Path(filepath.Join(run.DataDir, KeptEnv)), b, 0o600)
}

// wipeData deletes the data directory, except what a site still needs.
func wipeData(ctx context.Context, host Host, out io.Writer, warn func(string, ...any), run *Run) {
	if len(run.Keep) == 0 {
		removeTree(ctx, host, out, warn, run.DataDir)
		return
	}
	entries, err := os.ReadDir(host.Path(run.DataDir))
	if err != nil {
		warn("could not read %s: %v", run.DataDir, err)
		return
	}
	keep := set(run.Keep)
	for _, entry := range entries {
		if keep[entry.Name()] {
			fmt.Fprintf(out, "kept %s: a site on this server still caches into it\n", filepath.Join(run.DataDir, entry.Name()))
			continue
		}
		removeTree(ctx, host, out, warn, filepath.Join(run.DataDir, entry.Name()))
	}
}

// removeEnv deletes the .env and every copy of it, since each holds the key.
func removeEnv(host Host, out io.Writer, warn func(string, ...any), dir string) {
	entries, err := os.ReadDir(host.Path(dir))
	if err != nil {
		warn("could not read %s: %v", dir, err)
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !(name == ".env" || name == ".env.jd-previous" || name == ".env.jd-tmp" ||
			strings.HasPrefix(name, ".env.backup.")) {
			continue
		}
		path := filepath.Join(dir, name)
		if err := os.Remove(host.Path(path)); err != nil && !errors.Is(err, os.ErrNotExist) {
			warn("could not remove %s: %v", path, err)
			continue
		}
		fmt.Fprintf(out, "removed %s\n", path)
	}
}

// removeTree deletes a host path, through the host's root as this process
// sees it. It stays on the filesystem the path is on: a filesystem mounted
// somewhere inside the tree — a disk under the backup directory — is left
// alone rather than emptied, and said. Done here rather than by the host's
// rm, whose --one-file-system an Alpine host's BusyBox does not have.
func removeTree(ctx context.Context, host Host, out io.Writer, warn func(string, ...any), path string) {
	if err := deletable(path); err != nil {
		warn("did not remove %s: %v", path, err)
		return
	}
	visible := host.Path(path)
	info, err := os.Lstat(visible)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		warn("could not remove %s: %v", path, err)
		return
	}
	var skipped []string
	if _, err := removeContained(ctx, visible, deviceOf(info), func(p string) {
		skipped = append(skipped, filepath.Join(path, strings.TrimPrefix(p, visible)))
	}); err != nil {
		warn("could not remove %s: %v", path, err)
		return
	}
	for _, mount := range skipped {
		warn("left %s: another filesystem is mounted there", mount)
	}
	if len(skipped) == 0 {
		fmt.Fprintf(out, "removed %s\n", path)
	}
}

// removeContained removes path and everything under it on device dev, and
// reports whether anything had to be left: a directory on another device, and
// so every directory above it.
func removeContained(ctx context.Context, path string, dev uint64, skip func(string)) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		if deviceOf(info) != dev {
			skip(path)
			return true, nil
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return false, err
		}
		kept := false
		for _, entry := range entries {
			left, err := removeContained(ctx, filepath.Join(path, entry.Name()), dev, skip)
			if err != nil {
				return kept, err
			}
			kept = kept || left
		}
		if kept {
			return true, nil
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return false, nil
}

func deviceOf(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev)
	}
	return 0
}

// systemDirs are never deleted whatever the record says. Every path a run
// removes comes from the dashboard's own configuration, and this is the check
// that a configuration of JD_DATA_DIR=/var cannot turn into `rm -rf /var`.
var systemDirs = map[string]bool{
	"/": true, "/bin": true, "/boot": true, "/dev": true, "/etc": true, "/home": true, "/lib": true,
	"/lib64": true, "/media": true, "/mnt": true, "/opt": true, "/proc": true, "/root": true, "/run": true,
	"/sbin": true, "/srv": true, "/sys": true, "/tmp": true, "/usr": true, "/var": true, "/var/lib": true,
	"/var/log": true, "/var/backups": true, "/var/cache": true, "/var/tmp": true, "/usr/local": true,
}

func deletable(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("%q is not a clean absolute path", path)
	}
	if systemDirs[path] {
		return fmt.Errorf("%s is a system directory", path)
	}
	return nil
}

type hostSystem struct{}

func (hostSystem) Docker(ctx context.Context, dir string, out io.Writer, args ...string) error {
	return stream(ctx, dir, out, "docker", args...)
}

func (hostSystem) DockerOutput(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	return cmd.Output()
}

func (hostSystem) Run(ctx context.Context, out io.Writer, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := hostexec.CommandOnHost(ctx, name, args...)
	b, err := cmd.CombinedOutput()
	if text := strings.TrimSpace(string(b)); text != "" {
		fmt.Fprintln(out, text)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func (hostSystem) Path(path string) string { return hostexec.HostPath(path) }

func stream(ctx context.Context, dir string, out io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	// Plain progress, for the reason the restart runner gives: a transcript
	// read back later cannot replay cursor movement.
	cmd.Env = append(os.Environ(), "COMPOSE_PROGRESS=plain", "DOCKER_CLI_HINTS=false")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	sc := bufio.NewScanner(pipe)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		fmt.Fprintln(out, sc.Text())
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args[:min(len(args), 3)], " "), err)
	}
	return nil
}
