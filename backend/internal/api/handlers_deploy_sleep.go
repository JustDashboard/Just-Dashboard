package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// An environment's sleep (deploy/sleep.go): whether the live release
// sleeps, whether it is asleep now and why, and Wake now. Turning it on or
// off is the configuration's (runtime.sleep), saved with the rest of the
// Runtime settings and live with the next release.

func (s *Server) mountDeploymentSleepReads(r chi.Router) {
	r.Method(http.MethodGet, "/{id}/environments/{env}/sleep", s.handle(s.handleDeploymentSleep))
}

// mountDeploymentSleepWake is Wake now: starting the live release's
// container is what a visitor's request does, and what Start does, so it is
// the same control a deploy is.
func (s *Server) mountDeploymentSleepWake(r chi.Router) {
	r.Method(http.MethodPost, "/{id}/environments/{env}/sleep/wake", s.handle(s.handleDeploymentWake))
}

func (s *Server) sleepEnvironment(r *http.Request) (int64, int64, error) {
	projectID, environmentID, _, err := s.deploymentRouteFor(r)
	if err != nil {
		return 0, 0, err
	}
	if s.modules.sleeper == nil {
		return 0, 0, httpx.Err(http.StatusServiceUnavailable, "sleep_unavailable", "Sleeping is not available on this host.")
	}
	return projectID, environmentID, nil
}

func (s *Server) handleDeploymentSleep(w http.ResponseWriter, r *http.Request) error {
	_, environmentID, err := s.sleepEnvironment(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	status, err := s.modules.sleeper.Status(ctx, environmentID)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.JSON(w, http.StatusOK, status)
	return nil
}

// handleDeploymentWake starts the application now and waits for it, up to
// the plan's wake bound: the person who pressed it reads the outcome rather
// than a page that has to be refreshed.
func (s *Server) handleDeploymentWake(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := s.sleepEnvironment(r)
	if err != nil {
		return err
	}
	asleep, err := s.modules.sleeper.Asleep(r.Context(), environmentID)
	if err != nil {
		return httpx.Internal(err)
	}
	detail := map[string]any{"deploymentId": projectID, "environmentId": environmentID}
	target := s.sleepAuditTarget(r.Context(), projectID)
	if asleep == nil {
		detail["result"] = "awake"
		httpx.SetAudit(r, "deploy.environment.wake", target, detail)
		return httpx.Err(http.StatusConflict, "not_asleep", "This environment is not asleep.")
	}
	actor := httpx.MustPrincipal(r).Username()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	wait := 10*time.Minute + 30*time.Second
	outcome := s.modules.sleeper.Wake(ctx, environmentID, deploy.WakeRequest{Trigger: deploy.WakeByHand, Detail: actor, Actor: actor}, wait)
	detail["releaseId"], detail["reason"], detail["seconds"] = asleep.ReleaseID, outcome.Reason, outcome.Seconds
	httpx.SetAudit(r, "deploy.environment.wake", target, detail)
	s.modules.sleeper.Settle(ctx, environmentID)
	status, err := s.modules.sleeper.Status(ctx, environmentID)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"outcome": outcome, "sleep": status})
	return nil
}

// sleepAuditTarget names a deployment the way its other audit entries do,
// by its project's name, which is what the lifecycle feed matches a
// container's events against.
func (s *Server) sleepAuditTarget(ctx context.Context, projectID int64) string {
	if project, err := s.modules.deployStore.Get(ctx, projectID); err == nil && project != nil {
		return project.Name
	}
	return ""
}

// auditSleep records what the sleeper did on nobody's request.
func (s *Server) auditSleep(ctx context.Context, action string, environment deploy.SleepEnvironment, actor string, detail map[string]any, success bool) {
	if detail == nil {
		detail = map[string]any{}
	}
	detail["deploymentId"], detail["environmentId"] = environment.ProjectID, environment.EnvironmentID
	encoded, _ := json.Marshal(detail)
	if actor == "" {
		actor = "system"
	}
	s.Audit.Record(ctx, audit.Entry{
		Actor: actor, Action: action, Target: environment.Project,
		Success: success, Detail: string(encoded),
	})
}

// sleepCorrelationWindow is how far a sleep's audit entry may sit from the
// container event it explains: the stop after the idle decision, which a
// grace period of up to five minutes delays, and the start a wake entry is
// written after, once the application passed its readiness checks.
const sleepCorrelationWindow = 11 * time.Minute

// correlateSleepEvents attributes a deployment's stops and starts to its
// sleep: the idle decision is written just before the stop, the wake just
// after the start, so each is matched to the nearest entry on the side it
// falls. It runs before the release pass, whose directional window would
// otherwise file a wake's start under the sleep before it.
func (s *Server) correlateSleepEvents(r *http.Request, events []dockerx.Event, project string) {
	project = strings.TrimSpace(project)
	if len(events) == 0 || s.Audit == nil || project == "" {
		return
	}
	oldest := events[len(events)-1].Time
	entries, _, err := s.Audit.List(r.Context(), audit.Filter{
		ActionPrefixes: []string{"deploy.environment.idle", "deploy.environment.wake"},
		Since:          oldest.Add(-sleepCorrelationWindow),
		Limit:          500,
	})
	if err != nil {
		return
	}
	for i := range events {
		ev := &events[i]
		if ev.Trigger != nil || ev.Type != "container" {
			continue
		}
		var best *audit.Entry
		bestGap := sleepCorrelationWindow + time.Second
		for j := range entries {
			entry := &entries[j]
			if !entry.Success || strings.TrimSpace(entry.Target) != project {
				continue
			}
			var gap time.Duration
			switch {
			case entry.Action == "deploy.environment.idle" && (ev.Action == "kill" || ev.Action == "stop" || ev.Action == "die"):
				gap = ev.Time.Sub(entry.TS)
			case entry.Action == "deploy.environment.wake" && ev.Action == "start":
				gap = entry.TS.Sub(ev.Time)
			default:
				continue
			}
			// A second either way for the two clocks' rounding: the audit
			// keeps seconds, the daemon nanoseconds.
			if gap >= -time.Second && gap <= sleepCorrelationWindow && gap < bestGap {
				best, bestGap = entry, gap
			}
		}
		if best != nil {
			ev.Source = "dashboard"
			ev.Trigger = &dockerx.EventTrigger{AuditID: best.ID, Action: best.Action, Actor: best.Username, Confidence: "likely"}
		}
	}
}

// refuseSleepWithContainerSchedules keeps an environment from being set to
// sleep while one of its schedules needs the container running: a command
// run inside a stopped container fails, and nothing about a schedule firing
// is a request through the route that would wake it.
func (s *Server) refuseSleepWithContainerSchedules(ctx context.Context, projectID, environmentID int64, runtime deploy.RuntimePlanConfig) error {
	if !runtime.SleepOn() || s.modules.deployAutomation == nil {
		return nil
	}
	schedules, err := s.modules.deployAutomation.ListSchedules(ctx, projectID, environmentID)
	if err != nil {
		return nil
	}
	for _, schedule := range schedules {
		if !schedule.Enabled {
			continue
		}
		if conflict := deploy.SleepScheduleConflict(schedule.Steps); conflict != "" {
			apiErr := httpx.Err(http.StatusUnprocessableEntity, "invalid_plan",
				"The schedule \""+schedule.Name+"\" "+conflict+", which a sleeping environment's stopped container would fail. Pause that schedule, or leave sleeping off.")
			apiErr.Field = "runtime.sleep.enabled"
			return apiErr
		}
	}
	return nil
}

// refuseContainerScheduleWhileSleeping is the same rule from the schedule's
// side: one that needs the container is not added to an environment set to
// sleep.
func (s *Server) refuseContainerScheduleWhileSleeping(ctx context.Context, projectID, environmentID int64, schedule deploy.ScheduleWrite) error {
	conflict := deploy.SleepScheduleConflict(schedule.Steps)
	if !schedule.Enabled || conflict == "" || s.modules.deployPlanning == nil {
		return nil
	}
	configuration, err := s.modules.deployPlanning.EnvironmentConfiguration(ctx, projectID, environmentID)
	if err != nil || configuration == nil || !configuration.Runtime.SleepOn() {
		return nil
	}
	return httpx.Err(http.StatusConflict, "sleep_conflict",
		"This schedule "+conflict+", and the environment is set to sleep when idle, when its container is stopped. Turn sleeping off in Runtime settings first, or schedule something that does not need the container.")
}
