package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// Getting back to the version that was running.
//
// Everything here runs after an update has already failed, on a server whose
// operator is about to read the result in a transcript, so one rule decides
// every branch: a rollback may leave things where they are, but it may not make
// them worse. A step that fails is reported and the others still run, nothing
// is forced, and what could not be done is spelled out as commands to run.

// rollbackWindow is how long the previous version is given to answer. It is
// shorter than probeWindow because these images have already started on this
// server and nothing is being built.
const rollbackWindow = 3 * time.Minute

// updateSteps are the things an update does to the stack, apart so that a test
// can read their order without Docker.
type updateSteps struct {
	docker  func(args ...string) error
	images  func() []string
	build   func() error
	start   func() error
	health  func(url string, window time.Duration) error
	restore func(from string) error
}

// savedImage is an image this update can put back.
type savedImage struct{ current, previous string }

// PreviousImage is the tag an update keeps an image's last version under, or ""
// for a reference that cannot be given another tag.
func PreviousImage(image string) string {
	if image == "" || strings.Contains(image, "@") {
		return ""
	}
	name := image
	if colon := strings.LastIndexByte(image, ':'); colon > strings.LastIndexByte(image, '/') {
		name = image[:colon]
	}
	return name + ":previous"
}

// builtImages reads which images the compose file builds. A pulled image is not
// the update's to replace, and the proxy's Caddy may be shared with other things
// on this server.
func builtImages(raw []byte) []string {
	var config struct {
		Name     string `json:"name"`
		Services map[string]struct {
			Image string          `json:"image"`
			Build json.RawMessage `json:"build"`
		} `json:"services"`
	}
	if json.Unmarshal(raw, &config) != nil {
		return nil
	}
	var images []string
	for service, definition := range config.Services {
		if len(definition.Build) == 0 || string(definition.Build) == "null" {
			continue
		}
		image := definition.Image
		if image == "" {
			// Compose's own name for a built image nobody named.
			image = config.Name + "-" + service
		}
		images = append(images, image)
	}
	sort.Strings(images)
	return images
}

// composeImages asks Compose for builtImages. Not through the transcript: the
// resolved configuration carries the stack's secrets.
func composeImages(ctx context.Context, dir, compose string) []string {
	command := exec.CommandContext(ctx, "docker", "compose", "-f", compose, "config", "--format", "json")
	command.Dir = dir
	raw, err := command.Output()
	if err != nil {
		return nil
	}
	return builtImages(raw)
}

// savePreviousImages tags what is about to be replaced. A failure here does not
// stop the update, which would otherwise be lost to a renamed image on a machine
// where it would have worked. It only means there is nothing to go back to, and
// complete says so.
func savePreviousImages(images []string, docker func(args ...string) error, out io.Writer) (saved []savedImage, complete bool) {
	complete = len(images) > 0
	for _, image := range images {
		previous := PreviousImage(image)
		if previous == "" {
			complete = false
			fmt.Fprintf(out, "! %s cannot be kept for a rollback\n", image)
			continue
		}
		if err := docker("image", "tag", image, previous); err != nil {
			complete = false
			fmt.Fprintf(out, "! %s could not be kept for a rollback: %v\n", image, err)
			continue
		}
		saved = append(saved, savedImage{image, previous})
	}
	if !complete {
		fmt.Fprintf(out, "! this update cannot restart the previous version if it fails\n")
	}
	return saved, complete
}

// replaceStack builds and starts the new version, and goes back if it fails.
func replaceStack(store *Store, run *Run, compose string, steps updateSteps, out io.Writer) error {
	saved, complete := savePreviousImages(steps.images(), steps.docker, out)

	_ = store.Update(func(r *Run) { r.Phase = PhaseBuilding })
	if err := steps.build(); err != nil {
		return rollBack(store, run, compose, steps, saved, complete, false, err, out)
	}
	if err := steps.start(); err != nil {
		return rollBack(store, run, compose, steps, saved, complete, true, err, out)
	}

	_ = store.Update(func(r *Run) { r.Phase = PhaseRestarting })
	if run.Health == "" {
		return nil
	}
	fmt.Fprintf(out, "\nwaiting for the dashboard to answer at %s\n", run.Health)
	if err := steps.health(run.Health, probeWindow); err != nil {
		return rollBack(store, run, compose, steps, saved, complete, true, err, out)
	}
	fmt.Fprintf(out, "the dashboard is answering again\n")
	return nil
}

// rollBack returns the stack and the checkout to where the update found them,
// and returns the error the run finishes with.
//
// touched says whether any container was recreated. Until then a failed build has
// changed only tags and files, and restarting containers on the new compose file
// could take down a stack that is still running fine, so the first `up` waits
// for one that has already been replaced. The second comes after the checkout is
// back, because the release may have changed the compose file itself. It is a
// no-op when nothing differs.
//
// Each `up` passes --no-build: a missing image is a thing to report, and
// compiling for fifteen minutes first would be its own outage.
func rollBack(store *Store, run *Run, compose string, steps updateSteps, saved []savedImage, complete, touched bool, cause error, out io.Writer) error {
	fmt.Fprintf(out, "\nThe update failed: %v\n\nGoing back to %s.\n", cause, run.FromVersion)
	_ = store.Update(func(r *Run) { r.Phase = PhaseRestarting })

	var problems []string
	report := func(format string, args ...any) {
		problem := fmt.Sprintf(format, args...)
		problems = append(problems, problem)
		fmt.Fprintf(out, "! %s\n", problem)
	}
	restartable := complete
	for _, image := range saved {
		if err := steps.docker("image", "tag", image.previous, image.current); err != nil {
			report("could not move %s back to %s: %v", image.current, image.previous, err)
			restartable = false
		}
	}
	if touched && !restartable {
		// The containers are running the new images and no old ones can replace
		// them. The checkout matches those containers, so it stays with them.
		report("the previous images are not all available, so the stack and the checkout stay on the failed update")
		return rollbackResult(run, compose, saved, cause, problems)
	}
	if touched {
		if err := steps.docker("compose", "-f", compose, "up", "-d", "--no-build"); err != nil {
			fmt.Fprintf(out, "restarting on the release's compose file failed, and is tried again once the checkout is back: %v\n", err)
		}
	}
	if err := steps.restore(run.FromCommit); err != nil {
		report("could not move the checkout back: %v", err)
	}
	if !restartable {
		// Only reachable with nothing touched, so what was running still is.
		fmt.Fprintf(out, "no container was changed, so the previous version is still running\n")
		return rollbackResult(run, compose, saved, cause, problems)
	}
	if err := steps.docker("compose", "-f", compose, "up", "-d", "--no-build", "--remove-orphans"); err != nil {
		report("could not start the previous version: %v", err)
		return rollbackResult(run, compose, saved, cause, problems)
	}
	if run.Health != "" {
		fmt.Fprintf(out, "\nwaiting for the previous version to answer at %s\n", run.Health)
		if err := steps.health(run.Health, rollbackWindow); err != nil {
			report("the previous version is not answering: %v", err)
		}
	}
	return rollbackResult(run, compose, saved, cause, problems)
}

func rollbackResult(run *Run, compose string, saved []savedImage, cause error, problems []string) error {
	if len(problems) == 0 {
		return fmt.Errorf("%w\n\nThe update was rolled back: %s is running and the checkout at %s is back on %s",
			cause, run.FromVersion, run.Dir, run.FromCommit)
	}
	var commands []string
	for _, image := range saved {
		commands = append(commands, "docker image tag "+image.previous+" "+image.current)
	}
	if run.FromCommit != "" {
		commands = append(commands, "git reset --keep "+run.FromCommit)
	}
	commands = append(commands, "docker compose -f "+compose+" up -d")
	return fmt.Errorf("%w\n\nThe rollback did not finish:\n- %s\n\nTo finish it by hand, run these in %s:\n  %s",
		cause, strings.Join(problems, "\n- "), run.Dir, strings.Join(commands, "\n  "))
}

// restoreCheckout takes the checkout back to the commit the update started from.
//
// --keep, never --hard, for the reason fastForward gives: an edit the operator
// made survives, and if one is in the way git refuses without changing anything.
// It also refuses to act on a checkout that is not where this update left it,
// since then somebody else has moved it and their work is not this run's to undo.
func restoreCheckout(ctx context.Context, dir, from, landed string, out io.Writer) error {
	if from == "" {
		return fmt.Errorf("the commit it was on before the update was not recorded")
	}
	raw, err := gitOutput(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	head := strings.TrimSpace(raw)
	if head == from {
		return nil
	}
	if landed != "" && head != landed {
		return fmt.Errorf("it moved to %s during the update, so it was left there", head)
	}
	if _, err := gitOutput(ctx, dir, "merge-base", "--is-ancestor", from, "HEAD"); err != nil {
		return fmt.Errorf("%s is not an ancestor of %s, so it was left there", from, head)
	}
	if owner, ok := hostexec.OwnerOf(dir); ok {
		defer handBack(ctx, dir, owner, head, out)
	}
	// handBack changed the owner of what the merge wrote, which leaves the index
	// out of date for those files, and reset --keep reads that as a local edit
	// and refuses. Its exit status only says whether real edits exist, and
	// reset --keep is what decides whether they are in the way.
	_, _ = gitOutput(ctx, dir, "update-index", "-q", "--refresh")
	fmt.Fprintf(out, "\n$ git reset --keep %s\n", from)
	if err := gitStream(ctx, dir, out, "reset", "--keep", from); err != nil {
		return fmt.Errorf("git reset --keep %s: %v; a local edit is probably in the way, and git changed nothing", from, err)
	}
	return nil
}
