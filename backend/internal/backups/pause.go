package backups

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ContainerPauser belongs to the Docker owner. Backups asks it to freeze the
// containers a job names for exactly the archive step and to thaw them the
// moment the archive is written, whatever happened in between.
type ContainerPauser interface {
	PauseContainer(ctx context.Context, nameOrID string) error
	UnpauseContainer(ctx context.Context, nameOrID string) error
}

func (r *Runner) WithContainerPauser(pauser ContainerPauser) *Runner {
	r.pauser = pauser
	return r
}

// ErrPauseUnavailable is returned when a job names containers to pause but
// the runner has no Docker owner to pause them with. The run fails rather
// than archiving a volume the job promised would be quiet.
var ErrPauseUnavailable = errors.New("Docker is unavailable, so the containers this job pauses cannot be paused")

// pauseContainers freezes every container the job names and returns the
// function that thaws them. A container that is not running needs no pause
// and is logged as such; one that was already paused is left paused, because
// whoever paused it expects to find it that way. Any other refusal fails the
// run before a single byte is archived.
func (r *Runner) pauseContainers(ctx context.Context, job *Job, logBuf io.Writer) (func(), error) {
	if len(job.PauseContainers) == 0 {
		return func() {}, nil
	}
	if r.pauser == nil {
		return nil, ErrPauseUnavailable
	}
	var paused []string
	resume := func() {
		// The thaw must not inherit a cancelled context: a run that timed out
		// mid-archive still has to hand the containers back.
		thawCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		for _, name := range paused {
			if err := r.pauser.UnpauseContainer(thawCtx, name); err != nil {
				fmt.Fprintf(logBuf, "WARNING: could not resume %s: %v\n", name, err)
				r.log.Error("backup could not resume a paused container", "job", job.Name, "container", name, "err", err)
				continue
			}
			fmt.Fprintf(logBuf, "resumed %s\n", name)
		}
	}
	for _, name := range job.PauseContainers {
		err := r.pauser.PauseContainer(ctx, name)
		switch {
		case err == nil:
			paused = append(paused, name)
			fmt.Fprintf(logBuf, "paused %s for the archive step\n", name)
		case pauseSkippable(err):
			fmt.Fprintf(logBuf, "%s needs no pause: %s\n", name, pauseReason(err))
		default:
			resume()
			return nil, fmt.Errorf("pause %s: %w", name, err)
		}
	}
	return resume, nil
}

// pauseSkippable is Docker's way of saying the container is stopped or was
// already paused — states in which the archive is already quiet.
func pauseSkippable(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "is not running") || strings.Contains(msg, "already paused")
}

func pauseReason(err error) string {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "already paused") {
		return "already paused"
	}
	return "not running"
}
