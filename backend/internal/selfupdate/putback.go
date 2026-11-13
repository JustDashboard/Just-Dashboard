package selfupdate

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

// Putting an update back when the updater is not there to do it.
//
// The rollback in rollback.go runs inside the updater. An updater that is
// killed (the Docker daemon restarting under it, a reboot, the kernel choosing
// it when the frontend build runs out of memory) rolls nothing back: the
// checkout stays fast-forwarded and whichever images had finished building are
// already :latest, while the containers still run the old ones. The dashboard
// then said, truthfully, that the update failed and the old version was
// running, and that stayed true until the next time anything recreated the
// stack (a restart from Configuration, `manage.sh restart`), which brought up
// half of each version from a compose file that suited neither.
//
// So the backend that finds the updater gone does the two things that cannot
// make matters worse and need no container touched: it points each image tag
// back at the image that was running, and moves the checkout back if it is
// exactly where the update left it. Both are what the updater's own rollback
// does first, with the same care (reset --keep, never over somebody else's
// commit). It does not recreate containers. From inside one of them that is
// the manoeuvre the sibling exists to avoid, and with the tags and the
// checkout back, whatever recreates the stack next gets the old version whole.

// putBackTimeout bounds the work. It runs while the dashboard is starting, and
// a tag and a reset are a second between them.
const putBackTimeout = 2 * time.Minute

// putBack undoes what an updater that has gone had done to the image tags and
// the checkout, and returns the error the run finishes with. asked is whether
// Docker answered: when it did not, the tags cannot be moved and the message
// is the commands to move them.
func (i *Installer) putBack(ctx context.Context, run *Run, cause error, asked bool) error {
	ctx, cancel := context.WithTimeout(ctx, putBackTimeout)
	defer cancel()
	compose := run.Compose
	if compose == "" {
		compose = "docker-compose.yml"
	}
	var out io.Writer = io.Discard
	if file, err := i.store.AppendLog(); err == nil {
		defer file.Close()
		out = file
	}
	fmt.Fprintf(out, "\nThe updater is gone and %s is still what is running. Putting back what it had changed.\n", run.FromVersion)

	var problems []string
	report := func(format string, args ...any) {
		problem := fmt.Sprintf(format, args...)
		problems = append(problems, problem)
		fmt.Fprintf(out, "! %s\n", problem)
	}

	if !asked && len(run.SavedImages) > 0 {
		report("Docker could not be asked, so the image tags were not moved back")
	}
	for _, image := range run.SavedImages {
		if !asked {
			break
		}
		fmt.Fprintf(out, "\n$ docker image tag %s %s\n", image.Previous, image.Current)
		if output, err := DockerCommand(ctx, i.dockerHost, "image", "tag", image.Previous, image.Current).CombinedOutput(); err != nil {
			detail := strings.TrimSpace(string(output))
			if detail == "" {
				detail = err.Error()
			}
			report("could not move %s back to %s: %s", image.Current, image.Previous, detail)
		}
	}

	dir, visible := visiblePath(run.Dir)
	switch {
	case !visible:
		report("the checkout at %s cannot be seen from here, so it was left as it is", run.Dir)
	case run.ToCommit == "":
		// The updater records where it landed the moment the merge is done.
		// Without that, a checkout that is not where the update started
		// cannot be told from one somebody else moved, and theirs is not
		// this run's to undo.
		head, err := gitOutput(ctx, dir, "rev-parse", "HEAD")
		if at := strings.TrimSpace(head); err != nil || run.FromCommit == "" || at != run.FromCommit {
			report("the update did not record moving the checkout, which is on %s, so it was left there", shortCommit(at))
		}
	default:
		if err := restoreCheckout(ctx, dir, run.FromCommit, run.ToCommit, out); err != nil {
			report("could not move the checkout back: %v", err)
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("%w\n\nPutting back what it had changed did not finish:\n- %s\n\nTo finish it by hand, run these in %s:\n  %s%s",
			cause, strings.Join(problems, "\n- "), run.Dir, strings.Join(handCommands(run, compose, run.SavedImages), "\n  "), copyNote(run))
	}
	images := "It had not replaced any image"
	if len(run.SavedImages) > 0 {
		var names []string
		for _, image := range run.SavedImages {
			names = append(names, image.Current)
		}
		images = fmt.Sprintf("%s again name the %s images", strings.Join(names, " and "), run.FromVersion)
	}
	fmt.Fprintf(out, "put back\n")
	return fmt.Errorf("%w\n\nWhat it had changed was put back. %s, and the checkout at %s is on %s. "+
		"No container was recreated to do this. If `docker compose ps` shows one that is not running the %s image, "+
		"run this in %s:\n  docker compose -f %s up -d --no-build%s",
		cause, images, run.Dir, shortCommit(run.FromCommit), run.FromVersion, run.Dir, compose, copyNote(run))
}

// shortCommit is a commit as people read one.
func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	if commit == "" {
		return "a commit that could not be read"
	}
	return commit
}
