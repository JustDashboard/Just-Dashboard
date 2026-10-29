package api

import (
	"context"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// How much of the last day, week and month a deployment was up, for the
// project's Overview and for the exposition. The maths is deploy's
// (availability.go); this is the two reads it is given — the releases' live
// spans and the environment's persisted container events — and the sentence
// for when neither can be had.

// availabilityLookback is the longest window, which is how far back both
// records are read. Events older than it are pruned anyway.
var availabilityLookback = deploy.AvailabilityWindows[len(deploy.AvailabilityWindows)-1]

// handleDeploymentAvailability answers for the project's production
// environment, or for the one `?environment=` names.
func (s *Server) handleDeploymentAvailability(w http.ResponseWriter, r *http.Request) error {
	target, err := s.deploymentTarget(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	httpx.JSON(w, http.StatusOK, s.deploymentAvailability(ctx, target.EnvironmentID, time.Now()))
	return nil
}

// deploymentAvailability is the figure for one environment at `now`.
func (s *Server) deploymentAvailability(ctx context.Context, environmentID int64, now time.Time) deploy.Availability {
	if s.modules.deployEvents == nil || s.modules.dockerEvents == nil {
		return deploy.Unavailability(now, "Docker is unavailable on this host, so container lifecycle cannot be observed.")
	}
	since, err := s.modules.deployEvents.Since(ctx)
	if err != nil {
		return deploy.Unavailability(now, "The container event record could not be read.")
	}
	from := now.Add(-availabilityLookback)
	spans, err := s.modules.deployRuns.ReleaseSpans(ctx, environmentID, from)
	if err != nil {
		return deploy.Unavailability(now, "The release record could not be read.")
	}
	events, err := s.modules.deployEvents.Timeline(ctx, environmentID, from)
	if err != nil {
		return deploy.Unavailability(now, "The container event record could not be read.")
	}
	// Asleep by design is not down: a visitor got the page after a pause.
	asleep, _ := s.modules.deployRuns.SleepSpans(ctx, environmentID, from, 1000)
	return deploy.ComputeAvailability(now, since, spans, events, asleep...)
}
