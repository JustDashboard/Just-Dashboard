package stacks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/safego"
)

// Live is the release a stack's environment serves now.
type Live struct {
	ReleaseID int64
	// Kind and RuntimeID are the release's runtime: "compose" and the
	// Compose project its containers are labelled with.
	Kind      string
	RuntimeID string
	// TemplateID is the blueprint the release was rendered from.
	TemplateID string
}

// ErrNoLiveRelease is an environment that serves nothing yet.
var ErrNoLiveRelease = errors.New("no live release")

// Errors the routes turn into words.
var (
	ErrBusy    = errors.New("a configuration pass is already running")
	ErrWaiting = errors.New("the stack has no live release to configure yet")
)

// ServiceDeps is what the service reads from the rest of the dashboard.
type ServiceDeps struct {
	Store  *Store
	Docker *dockerx.Client
	// Manifest returns a registered stack.
	Manifest func(id string) (*Manifest, bool)
	// LiveRelease reads an environment's live release, or ErrNoLiveRelease.
	LiveRelease func(ctx context.Context, environmentID int64) (Live, error)
	// Variables are the environment's literal variable values by name,
	// secrets included: the API keys the template generated and the options.
	Variables func(ctx context.Context, projectID, environmentID int64) (map[string]string, error)
	Log       *slog.Logger
	// Interval is how often every stack is looked at; Kick looks at once.
	Interval time.Duration
	Runner   *Runner
}

// Service runs configuration passes: after every new live release, when a
// stack's manifest version changes, on Retry, and again after a restart that
// cut one short. One pass per stack at a time, two stacks at once.
type Service struct {
	deps    ServiceDeps
	kick    chan struct{}
	mu      sync.Mutex
	running map[int64]*pass
	slots   chan struct{}
}

type pass struct {
	releaseID int64
	cancel    context.CancelFunc
}

func NewService(deps ServiceDeps) *Service {
	if deps.Interval <= 0 {
		deps.Interval = 10 * time.Second
	}
	if deps.Runner == nil {
		deps.Runner = DefaultRunner()
	}
	return &Service{deps: deps, kick: make(chan struct{}, 1), running: map[int64]*pass{}, slots: make(chan struct{}, 2)}
}

// Start looks at every stack now and then on every interval until ctx ends.
func (s *Service) Start(ctx context.Context) {
	if err := s.deps.Store.ResetInterrupted(ctx); err != nil && s.deps.Log != nil {
		s.deps.Log.Warn("stacks interrupted by the last stop could not be queued again", "error", err)
	}
	safego.Go(ctx, s.deps.Log, "smart stack configuration", func(ctx context.Context) {
		ticker := time.NewTicker(s.deps.Interval)
		defer ticker.Stop()
		for {
			s.Tick(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-s.kick:
			}
		}
	})
}

// Kick asks the loop to look now rather than at the next interval.
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Running reports whether a pass is under way for a project.
func (s *Service) Running(projectID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[projectID] != nil
}

// Request asks for a pass now: Retry and Repair.
func (s *Service) Request(ctx context.Context, projectID int64) error {
	record, err := s.deps.Store.Get(ctx, projectID)
	if err != nil {
		return err
	}
	if s.Running(projectID) {
		return ErrBusy
	}
	if _, err := s.deps.LiveRelease(ctx, record.EnvironmentID); err != nil {
		if errors.Is(err, ErrNoLiveRelease) {
			return ErrWaiting
		}
		return err
	}
	if err := s.deps.Store.RequestPass(ctx, projectID); err != nil {
		return err
	}
	s.Kick()
	return nil
}

// Tick looks at every stack once and starts the passes that are due.
func (s *Service) Tick(ctx context.Context) {
	records, err := s.deps.Store.List(ctx)
	if err != nil {
		if s.deps.Log != nil && ctx.Err() == nil {
			s.deps.Log.Warn("smart stacks could not be listed", "error", err)
		}
		return
	}
	// A stack whose deployment was removed is gone with it (its rows
	// cascade): a pass still running for it would go on setting up
	// containers that are being taken away, in one of the two places a pass
	// runs in.
	listed := make(map[int64]bool, len(records))
	for _, record := range records {
		listed[record.ProjectID] = true
	}
	s.mu.Lock()
	for projectID, current := range s.running {
		if !listed[projectID] {
			current.cancel()
		}
	}
	s.mu.Unlock()
	for _, record := range records {
		manifest, found := s.deps.Manifest(record.StackID)
		if !found {
			continue
		}
		live, err := s.deps.LiveRelease(ctx, record.EnvironmentID)
		s.mu.Lock()
		current := s.running[record.ProjectID]
		s.mu.Unlock()
		if err != nil {
			if errors.Is(err, ErrNoLiveRelease) && current == nil {
				_ = s.deps.Store.SetWaiting(ctx, record.ProjectID)
			}
			continue
		}
		if current != nil {
			// A pass for a release that is no longer live is configuring
			// containers that are going away: stop it, the next tick starts
			// one for the release that replaced it.
			if current.releaseID != live.ReleaseID {
				current.cancel()
			}
			continue
		}
		if live.Kind != "compose" {
			continue
		}
		preset, found := manifest.PresetForTemplate(live.TemplateID)
		if !found {
			// The source was changed to something that is not this stack;
			// nothing here is the stack's to configure any more.
			continue
		}
		due := live.ReleaseID != record.ConfiguredReleaseID || record.ConfiguredVersion != manifest.Version ||
			!record.RequestedAt.IsZero()
		if !due {
			continue
		}
		if preset.ID != record.Preset {
			// Moving from Essential to Complete is a source change to the
			// other preset's template: the stack follows it.
			if err := s.deps.Store.SetPreset(ctx, record.ProjectID, preset.ID); err != nil {
				continue
			}
			record.Preset = preset.ID
		}
		select {
		case s.slots <- struct{}{}:
		default:
			return
		}
		passCtx, cancel := context.WithCancel(ctx)
		s.mu.Lock()
		s.running[record.ProjectID] = &pass{releaseID: live.ReleaseID, cancel: cancel}
		s.mu.Unlock()
		safego.Go(passCtx, s.deps.Log, "smart stack pass", func(passCtx context.Context) {
			defer func() {
				cancel()
				s.mu.Lock()
				delete(s.running, record.ProjectID)
				s.mu.Unlock()
				<-s.slots
			}()
			s.runPass(passCtx, manifest, preset, record, live)
		})
	}
}

func (s *Service) runPass(ctx context.Context, manifest *Manifest, preset Preset, record Record, live Live) {
	store := s.deps.Store
	if err := store.BeginPass(ctx, record.ProjectID, live.ReleaseID); err != nil {
		return
	}
	// Records outlive the pass's context: a step that was running when the
	// pass was cancelled is still written back as pending.
	persist := context.WithoutCancel(ctx)
	env, err := s.Env(ctx, manifest, preset, record, live)
	if err != nil {
		_ = store.FinishPass(persist, record.ProjectID, live.ReleaseID, manifest.Version, StatusFailed, false)
		if s.deps.Log != nil {
			s.deps.Log.Warn("a smart stack could not be prepared for configuration", "project", record.ProjectID, "error", err)
		}
		return
	}
	result := s.deps.Runner.Run(ctx, manifest, env, func(step StepRecord) {
		if err := store.PutStep(persist, record.ProjectID, step); err != nil && s.deps.Log != nil {
			s.deps.Log.Warn("a smart stack step could not be recorded", "project", record.ProjectID, "step", step.StepID, "error", err)
		}
	})
	_ = store.FinishPass(persist, record.ProjectID, live.ReleaseID, manifest.Version, result.Status, result.Complete)
	if !result.Complete {
		// Cut short — the dashboard stopping, or the release replaced
		// under it. Every step converges, so the pass simply runs again.
		_ = store.RequestPass(persist, record.ProjectID)
	}
	if s.deps.Log != nil {
		s.deps.Log.Info("smart stack configuration pass finished", "project", record.ProjectID,
			"stack", manifest.ID, "release", live.ReleaseID, "status", result.Status, "complete", result.Complete)
	}
}

// Env builds the environment a pass runs in, over the live release's
// containers.
func (s *Service) Env(ctx context.Context, manifest *Manifest, preset Preset, record Record, live Live) (*DockerEnv, error) {
	variables, err := s.deps.Variables(ctx, record.ProjectID, record.EnvironmentID)
	if err != nil {
		return nil, fmt.Errorf("could not read the deployment's variables: %w", err)
	}
	options := map[string]string{}
	for _, option := range manifest.Options {
		options[option.Name] = option.Default
		if value, found := variables[option.Variable]; found {
			options[option.Name] = value
		}
	}
	running := map[string]bool{}
	for _, service := range preset.Services {
		running[service] = true
	}
	previous, err := s.deps.Store.Steps(ctx, record.ProjectID)
	if err != nil {
		return nil, err
	}
	outputs := map[string]map[string]string{}
	for id, step := range previous {
		if step.Status == StepDone && len(step.Outputs) > 0 {
			outputs[id] = step.Outputs
		}
	}
	project := live.RuntimeID
	return &DockerEnv{
		Docker:  s.deps.Docker,
		Network: project + "_internal",
		Container: func(ctx context.Context, service string) (string, error) {
			return ServiceContainer(ctx, s.deps.Docker, project, service)
		},
		PresetID: preset.ID,
		Running:  running,
		Options:  options,
		Variables: func(_ context.Context, name string) (string, error) {
			return variables[name], nil
		},
		Credentials: s.deps.Store.Credentials(record.ProjectID),
		Lengths:     CredentialLengths(manifest),
		Outputs:     outputs,
	}, nil
}

// ServiceContainer finds a Compose service's container in a project: the
// running one when there are several (a container being replaced), never a
// one-off `run` container.
func ServiceContainer(ctx context.Context, docker *dockerx.Client, project, service string) (string, error) {
	containers, err := docker.ListContainersWithLabels(ctx, map[string]string{
		"com.docker.compose.project": project, "com.docker.compose.service": service,
	})
	if err != nil {
		return "", err
	}
	chosen := ""
	for _, container := range containers {
		if container.Labels["com.docker.compose.oneoff"] == "True" {
			continue
		}
		if container.State == "running" {
			return container.ID, nil
		}
		if chosen == "" {
			chosen = container.ID
		}
	}
	if chosen == "" {
		return "", fmt.Errorf("%s has no container in this release", service)
	}
	return chosen, nil
}
