package selfupdate

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stackports"
)

// The program the sibling container runs.
//
// This is the same binary as the dashboard, started with -self-update, and it
// runs with no configuration at all: no master key, no database, no
// environment inherited from the process that launched it. Everything it needs
// is in the record the backend wrote before creating it — which is the point,
// because that record is also what the operator reads afterwards, so the job
// that was described is exactly the job that ran.
//
// Nothing here talks to a browser. The transcript on disk is the whole
// interface, because the process that would have served it is one of the ones
// being restarted.

// probeWindow is how long the dashboard is given to answer after its
// containers have been recreated. A cold start on a small VPS is seconds; five
// minutes covers a machine that is also busy building.
const probeWindow = 5 * time.Minute

// RunUpdater carries out the upgrade described by the record in stateDir.
//
// The error it returns is for the container's exit status. The record on disk
// is the copy that matters, and it is written before returning either way.
func RunUpdater(stateDir string) error {
	store := NewStore(stateDir)
	run, err := store.Load()
	if err != nil {
		return fmt.Errorf("read the update record: %w", err)
	}
	if run == nil {
		return fmt.Errorf("no update was requested (%s is not there)", store.Path())
	}
	if !run.Status.Live() {
		return fmt.Errorf("the update recorded in %s already finished as %s", store.Path(), run.Status)
	}

	logFile, err := store.AppendLog()
	if err != nil {
		return fmt.Errorf("open the transcript: %w", err)
	}
	defer logFile.Close()
	// Everything is written twice: to the transcript the dashboard reads back,
	// and to stdout, so `docker logs just-dashboard-updater` shows the same
	// thing to somebody who has no dashboard to read it in — which, during an
	// upgrade, is the normal case.
	out := io.MultiWriter(logFile, os.Stdout)

	_ = store.Update(func(r *Run) {
		r.Status = StatusRunning
		r.Phase = PhaseFetching
	})

	ctx := context.Background()
	err = upgrade(ctx, store, run, out)
	if ferr := store.Finish(err); ferr != nil {
		fmt.Fprintf(out, "\n! could not record the outcome: %v\n", ferr)
	}
	if err != nil {
		fmt.Fprintf(out, "\nFAILED: %v\n", err)
		return err
	}
	installed := run.InstalledVersion
	if installed == "" {
		installed = run.ToVersion
	}
	fmt.Fprintf(out, "\nJust Dashboard is now on %s.\n", installed)

	// Tidying up after itself, which it can only do by asking Docker to remove
	// the container it is running in. The final state is already on disk, so
	// being killed part-way through this call costs nothing — and the daemon
	// carries out the removal whether or not this process survives to see it.
	// Only on success: a failed run's container is the last place to look when
	// the transcript itself is empty.
	_ = exec.Command("docker", "rm", "-f", UpdaterContainer).Run()
	return nil
}

func upgrade(ctx context.Context, store *Store, run *Run, out io.Writer) error {
	if _, err := os.Stat(run.Dir); err != nil {
		return fmt.Errorf("%s is not there — the checkout this dashboard was installed from has moved or was not mounted", run.Dir)
	}

	// A rollback needs somewhere to go back to, and the record has none when the
	// backend could not read the checkout.
	if run.FromCommit == "" {
		if before, err := gitOutput(ctx, run.Dir, "rev-parse", "HEAD"); err == nil {
			run.FromCommit = strings.TrimSpace(before)
			_ = store.Update(func(r *Run) { r.FromCommit = run.FromCommit })
		}
	}
	commit, installed, err := fastForward(ctx, run.Dir, run.Ref, run.ToVersion, out)
	if err != nil {
		return err
	}
	_ = store.Update(func(r *Run) { r.ToCommit = commit })

	compose := run.Compose
	if compose == "" {
		compose = "docker-compose.yml"
	}
	err = replaceStack(store, run, compose, updateSteps{
		docker: func(args ...string) error {
			fmt.Fprintf(out, "\n$ docker %s\n", strings.Join(args, " "))
			return stream(ctx, run.Dir, out, "docker", args...)
		},
		images: func() []string { return composeImages(ctx, run.Dir, compose) },
		build: func() error {
			return BuildEachService(ctx, run.Dir, compose, func(args ...string) error {
				return stream(ctx, run.Dir, out, "docker", args...)
			})
		},
		start: func() error {
			return stackports.Start(ctx, run.Dir, compose, out, func() error {
				args := []string{"compose", "-f", compose, "up", "-d", "--remove-orphans", "--wait", "--wait-timeout", "90"}
				fmt.Fprintf(out, "\n$ docker %s\n", strings.Join(args, " "))
				return stream(ctx, run.Dir, out, "docker", args...)
			}, func(selected stackports.Selection) error {
				if selected.Health() == "" {
					return nil
				}
				run.Health = selected.Health()
				return store.Update(func(r *Run) { r.Health = run.Health })
			})
		},
		health: func(url string, window time.Duration) error {
			return waitHealthy(ctx, url, run.Dir, window, out)
		},
		restore: func(from string) error {
			return restoreCheckout(ctx, run.Dir, from, commit, out)
		},
	}, out)
	if err != nil {
		return err
	}
	// Only now: a rollback means this version was never what is running.
	run.InstalledVersion = installed
	_ = store.Update(func(r *Run) { r.InstalledVersion = installed })
	return nil
}

// updateTarget chooses what the checkout moves to, once fetched.
//
// The tag v<version> when origin has one: that commit is the release that was
// announced, wherever the branch has gone since. Asked of origin rather than
// looked up locally, so a tag of the operator's own cannot stand in for it, and
// fetched without touching local tags, so one cannot make the fetch fail either.
//
// Without a tag it is the branch, which is the normal case for a fork and for the
// window before the release workflow has tagged a merge. The branch's changelog
// must then name exactly the announced version. A branch that has moved on is
// refused rather than installed: nobody was shown that release's notes, and
// checking again offers it properly.
func updateTarget(ctx context.Context, dir, ref, to string, out io.Writer) (string, error) {
	target, tagged := fetchReleaseTag(ctx, dir, "v"+Normalise(to), out)
	if !tagged {
		target = "origin/" + ref
	}
	raw, err := gitOutput(ctx, dir, "show", target+":"+ManifestPath)
	if err != nil {
		return "", fmt.Errorf("could not read the changelog at %s, so it cannot be confirmed as %s; nothing was changed: %w", target, to, err)
	}
	manifest, err := ParseManifest([]byte(raw))
	if err != nil {
		return "", fmt.Errorf("the changelog at %s could not be read, so it cannot be confirmed as %s; nothing was changed: %w", target, to, err)
	}
	if _, ok := manifest.Find(to); !ok {
		return "", fmt.Errorf("%s does not contain release %s (its newest is %s); nothing was changed", target, to, manifest.Latest)
	}
	if !tagged && Compare(manifest.Latest, to) > 0 {
		return "", fmt.Errorf("%s has moved on to %s since %s was offered, and origin has no tag v%s to install exactly; "+
			"nothing was changed. Check for updates again to install %s", target, manifest.Latest, to, Normalise(to), manifest.Latest)
	}
	fmt.Fprintf(out, "%s carries release %s (newest %s)\n", target, to, manifest.Latest)
	return target, nil
}

// fetchReleaseTag reports the commit a tag on origin names. Failing to find out
// is not an error: the branch is then checked just as strictly.
func fetchReleaseTag(ctx context.Context, dir, tag string, out io.Writer) (string, bool) {
	var listed bytes.Buffer
	if err := gitStream(ctx, dir, &listed, "ls-remote", "--tags", "origin", "refs/tags/"+tag); err != nil {
		fmt.Fprintf(out, "could not ask origin for the tag %s, so using the branch: %v\n", tag, err)
		return "", false
	}
	// By whole lines, because ssh's own warnings arrive in the same stream.
	found := false
	for _, line := range strings.Split(listed.String(), "\n") {
		found = found || strings.HasSuffix(line, "\trefs/tags/"+tag)
	}
	if !found {
		fmt.Fprintf(out, "origin has no tag %s yet, so using the branch\n", tag)
		return "", false
	}
	fmt.Fprintf(out, "\n$ git fetch --no-tags origin refs/tags/%s\n", tag)
	if err := gitStream(ctx, dir, out, "fetch", "--no-tags", "origin", "refs/tags/"+tag); err != nil {
		fmt.Fprintf(out, "could not fetch the tag %s, so using the branch: %v\n", tag, err)
		return "", false
	}
	commit, err := gitOutput(ctx, dir, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
	if err != nil {
		fmt.Fprintf(out, "could not read the tag %s, so using the branch: %v\n", tag, err)
		return "", false
	}
	return strings.TrimSpace(commit), true
}

// installedVersion is what the checkout's own changelog says it is on. HEAD can
// be past the target when it already was, so the target's answer would not do.
func installedVersion(ctx context.Context, dir string) string {
	raw, err := gitOutput(ctx, dir, "show", "HEAD:"+ManifestPath)
	if err != nil {
		return ""
	}
	manifest, err := ParseManifest([]byte(raw))
	if err != nil {
		return ""
	}
	return manifest.Latest
}

// fastForward moves the checkout on to the release updateTarget confirms, and
// reports the commit and the version it lands on.
//
// --ff-only, never `reset --hard`.
//
// The deploy pipeline in internal/deploy resets, and is right to: a deploy
// target's working tree is disposable. This one is not. It is the operator's
// own checkout, on their own server, and it is entirely normal for it to carry
// an edited docker-compose.yml or a tweaked Caddyfile. A reset would delete
// that silently as a side effect of pressing "update", which is the kind of
// thing an operator finds out about weeks later. A fast-forward keeps every
// local change that does not collide, and when one does collide git refuses
// and says exactly what is in the way — which is a far better outcome than a
// successful upgrade that ate somebody's configuration.
func fastForward(ctx context.Context, dir, ref, to string, out io.Writer) (commit, version string, err error) {
	if owner, ok := hostexec.OwnerOf(dir); ok {
		if before, err := gitOutput(ctx, dir, "rev-parse", "HEAD"); err == nil {
			defer handBack(ctx, dir, owner, strings.TrimSpace(before), out)
		}
	}
	fmt.Fprintf(out, "$ git fetch --prune origin\n")
	if err := gitStream(ctx, dir, out, "fetch", "--prune", "origin"); err != nil {
		return "", "", err
	}

	target, err := updateTarget(ctx, dir, ref, to, out)
	if err != nil {
		return "", "", err
	}
	fmt.Fprintf(out, "\n$ git merge --ff-only %s\n", target)
	if err := gitStream(ctx, dir, out, "merge", "--ff-only", target); err != nil {
		return "", "", fmt.Errorf("%w\n\nThe checkout at %s could not be fast-forwarded to %s. "+
			"That normally means it carries local commits, or edits to files this release also "+
			"changes; commit, stash or revert them and try again", err, dir, target)
	}
	version = installedVersion(ctx, dir)
	head, err := gitOutput(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		// The merge succeeded, so the upgrade should proceed; only the record
		// of which commit it landed on is missing.
		return "", version, nil
	}
	commit = strings.TrimSpace(head)
	fmt.Fprintf(out, "now at %s (version %s)\n", commit, version)
	return commit, version, nil
}

// handBack gives the checkout's owner back what this process wrote into it.
//
// The updater is root, and git run as root leaves root's name on every file it
// writes: the objects and refs a fetch adds to .git, and each file and new
// folder a merge brings in. Nothing about the update notices — root can write
// anywhere — but the next `git pull` the operator runs as themselves, from a
// shell or from the Git page, can unlink nothing in a folder an update
// created, and stops halfway with the files it did write left behind as local
// changes. Running git itself as the owner would avoid the root-owned files
// and break the update instead: inside this container the owner has no
// passwd entry, which ssh refuses to run without, and the folders earlier
// updates made root-owned would stop the merge partway. So git stays root and
// what it wrote is handed back: anything root-owned in .git, and the files
// this update changed with the folders above them. It runs whether or not the
// merge went through, because a fetch that succeeded has already written.
func handBack(ctx context.Context, dir string, owner hostexec.Owner, before string, out io.Writer) {
	seen := map[string]bool{}
	var rels []string
	rootOwned := func(rel string) {
		if seen[rel] {
			return
		}
		seen[rel] = true
		var st syscall.Stat_t
		if syscall.Lstat(filepath.Join(dir, rel), &st) == nil && st.Uid == 0 {
			rels = append(rels, rel)
		}
	}
	_ = filepath.WalkDir(filepath.Join(dir, ".git"), func(p string, _ fs.DirEntry, err error) error {
		if err == nil {
			rel, _ := filepath.Rel(dir, p)
			rootOwned(rel)
		}
		return nil
	})
	if changed, err := gitOutput(ctx, dir, "diff", "--name-only", "-z", "--no-renames", before, "HEAD", "--"); err == nil {
		for _, rel := range strings.Split(changed, "\x00") {
			for ; rel != "" && rel != "."; rel = filepath.Dir(rel) {
				rootOwned(rel)
			}
		}
	}
	if len(rels) == 0 {
		return
	}
	if err := hostexec.GiveBack(dir, owner, rels); err != nil {
		fmt.Fprintf(out, "could not give what the update wrote back to %s: %v\n", owner.Name, err)
		return
	}
	fmt.Fprintf(out, "gave %d paths the update wrote back to %s\n", len(rels), owner.Name)
}

// waitHealthy is what makes "the update finished" mean the dashboard came
// back, rather than that a command exited zero. `docker compose up -d` returns
// as soon as the containers are started, and a backend that starts and then
// dies on a configuration error looks identical to a healthy one from there.
func waitHealthy(ctx context.Context, url, dir string, window time.Duration, out io.Writer) error {
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(window)
	var last string
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			last = resp.Status
		} else {
			last = err.Error()
		}
		time.Sleep(3 * time.Second)
	}
	fmt.Fprintf(out, "still not answering after %s\n", window)
	return fmt.Errorf("the dashboard did not answer at %s within %s (last attempt: %s) — "+
		"run `docker compose ps` and `docker compose logs backend` in %s to see why", url, window, last, dir)
}

// gitStream is gitOutput's streaming twin: same safe.directory handling, but
// the output goes to the transcript as it appears rather than being collected.
func gitStream(ctx context.Context, dir string, out io.Writer, args ...string) error {
	full := append([]string{"-c", "safe.directory=" + dir, "-C", dir}, args...)
	// A credential prompt would hang the upgrade forever with no way to answer
	// it — there is no terminal attached to this container — so git is told to
	// fail instead. A private fork needs a checkout whose remote authenticates
	// without asking, which is what a deploy key is.
	env := append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/true",
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new",
	)
	return streamEnv(ctx, dir, env, out, "git", full...)
}

func stream(ctx context.Context, dir string, out io.Writer, name string, args ...string) error {
	env := append(os.Environ(),
		// Plain progress for the same reason the compose runner asks for it:
		// the default renderer redraws with cursor movement, which in a file
		// read back later is a screenful of half-written words.
		"COMPOSE_PROGRESS=plain",
		"BUILDKIT_PROGRESS=plain",
		"DOCKER_CLI_HINTS=false",
		// Asked for explicitly rather than relied upon. Compose picks BuildKit
		// on its own when buildx is installed, and silently builds with the
		// classic builder when it is not — which is exactly how this feature
		// shipped an updater that could not build the project it updates. The
		// image now carries buildx; saying so here means the day somebody sets
		// DOCKER_BUILDKIT=0 in the environment, the failure is theirs and
		// visible rather than ours and silent.
		"DOCKER_BUILDKIT=1",
		"COMPOSE_DOCKER_CLI_BUILD=1",
	)
	return streamEnv(ctx, dir, env, out, name, args...)
}

func streamEnv(ctx context.Context, dir string, env []string, out io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	sc := bufio.NewScanner(pipe)
	// A build log line can be very long — a bundler printing one path per
	// dependency on one line — and the default 64 KB would end the scan with
	// "token too long" halfway through an otherwise successful upgrade.
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		fmt.Fprintln(out, sc.Text())
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args[:min(len(args), 3)], " "), err)
	}
	return nil
}
