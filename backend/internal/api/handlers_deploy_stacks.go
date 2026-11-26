package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/catalog"
)

// The smart stacks (internal/stacks): a deployment made from a stack's preset
// is configured by the dashboard once its release is live. These routes read
// the catalogue and a stack's state, run a pass again, and reveal the
// accounts the stack created. Everything a stack deploys is an ordinary
// Compose deployment, so every other route treats it as one.

// stackCatalogueEntry is one stack as the Stacks tab offers it.
type stackCatalogueEntry struct {
	ID          string                 `json:"id"`
	Version     string                 `json:"version"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Presets     []stackCataloguePreset `json:"presets"`
	Services    []stackCatalogueItem   `json:"services"`
}

type stackCataloguePreset struct {
	stacks.Preset
	TemplateVersion string `json:"templateVersion"`
	MemoryMB        int    `json:"memoryMb"`
}

type stackCatalogueItem struct {
	stacks.ServiceInfo
	Image string `json:"image"`
}

func (s *Server) handleStackCatalogue(w http.ResponseWriter, _ *http.Request) error {
	out := []stackCatalogueEntry{}
	for _, manifest := range catalog.All() {
		entry := stackCatalogueEntry{ID: manifest.ID, Version: manifest.Version, Title: manifest.Title,
			Description: manifest.Description, Presets: []stackCataloguePreset{}, Services: []stackCatalogueItem{}}
		images := map[string]string{}
		for _, preset := range manifest.Presets {
			definition, err := blueprint.Get(preset.Template)
			if err != nil {
				return err
			}
			memory := 0
			for _, service := range definition.Services {
				memory += service.Resources.MemoryMB
				if images[service.Name] == "" {
					images[service.Name] = service.Image.Reference
				}
			}
			entry.Presets = append(entry.Presets, stackCataloguePreset{Preset: preset,
				TemplateVersion: definition.Version, MemoryMB: memory})
		}
		for _, service := range manifest.Services {
			entry.Services = append(entry.Services, stackCatalogueItem{ServiceInfo: service, Image: images[service.Name]})
		}
		out = append(out, entry)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// stackState is GET /deploy/{id}/stack.
type stackState struct {
	StackID             string                   `json:"stackId"`
	Title               string                   `json:"title"`
	Version             string                   `json:"version"`
	Preset              string                   `json:"preset"`
	PresetTitle         string                   `json:"presetTitle"`
	Status              stacks.Status            `json:"status"`
	Summary             string                   `json:"summary"`
	Readiness           stackReadiness           `json:"readiness"`
	Services            []stackServiceState      `json:"services"`
	Steps               []stackStepState         `json:"steps"`
	Actions             []stacks.Action          `json:"actions"`
	Credentials         []stackCredentialListing `json:"credentials"`
	Limitations         []string                 `json:"limitations"`
	LiveReleaseID       int64                    `json:"liveReleaseId,omitempty"`
	ConfiguredReleaseID int64                    `json:"configuredReleaseId,omitempty"`
	StartedAt           *time.Time               `json:"startedAt,omitempty"`
	FinishedAt          *time.Time               `json:"finishedAt,omitempty"`
	UpdatedAt           time.Time                `json:"updatedAt"`
}

type stackReadiness struct {
	Installed   bool `json:"installed"`
	Healthy     bool `json:"healthy"`
	Connected   bool `json:"connected"`
	Acquisition bool `json:"acquisition"`
	Playback    bool `json:"playback"`
}

type stackServiceState struct {
	Name      string               `json:"name"`
	Title     string               `json:"title"`
	Purpose   string               `json:"purpose"`
	Access    stacks.Access        `json:"access"`
	Image     string               `json:"image"`
	DocsURL   string               `json:"docsUrl,omitempty"`
	Address   string               `json:"address,omitempty"`
	URL       string               `json:"url,omitempty"`
	Container *stackContainerState `json:"container,omitempty"`
	Status    string               `json:"status"`
}

type stackContainerState struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Health string `json:"health,omitempty"`
}

type stackStepState struct {
	ID         string            `json:"id"`
	Service    string            `json:"service"`
	Title      string            `json:"title"`
	Stage      stacks.Stage      `json:"stage"`
	Status     stacks.StepStatus `json:"status"`
	Attempts   int               `json:"attempts"`
	Message    string            `json:"message,omitempty"`
	Error      string            `json:"error,omitempty"`
	StartedAt  *time.Time        `json:"startedAt,omitempty"`
	FinishedAt *time.Time        `json:"finishedAt,omitempty"`
}

type stackCredentialListing struct {
	Name     string `json:"name"`
	Service  string `json:"service"`
	Label    string `json:"label"`
	Username string `json:"username,omitempty"`
	Stored   bool   `json:"stored"`
}

// stackConfigureAnswer is what Retry answers: the pass has been asked for.
type stackConfigureAnswer struct {
	Status stacks.Status `json:"status"`
}

// stackCredentialReveal is one account the stack created, revealed.
type stackCredentialReveal struct {
	Name     string `json:"name"`
	Username string `json:"username,omitempty"`
	Password string `json:"password"`
}

func stackProjectID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, httpx.BadRequest("deployment id must be a positive integer")
	}
	return id, nil
}

func stackNotFound() error {
	return httpx.Err(http.StatusNotFound, "stack_not_found", "this deployment is not a stack")
}

// stackRecord is the project's stack and its manifest, or a 404 that says
// it is not one.
func (s *Server) stackRecord(r *http.Request) (*stacks.Record, *stacks.Manifest, error) {
	projectID, err := stackProjectID(r)
	if err != nil {
		return nil, nil, err
	}
	if s.modules.stackStore == nil {
		return nil, nil, stackNotFound()
	}
	record, err := s.modules.stackStore.Get(r.Context(), projectID)
	if errors.Is(err, stacks.ErrNotFound) {
		return nil, nil, stackNotFound()
	}
	if err != nil {
		return nil, nil, err
	}
	manifest, found := catalog.ByID(record.StackID)
	if !found {
		return nil, nil, stackNotFound()
	}
	return record, manifest, nil
}

func (s *Server) handleStackState(w http.ResponseWriter, r *http.Request) error {
	record, manifest, err := s.stackRecord(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	state, err := s.stackStateOf(ctx, record, manifest)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, state)
	return nil
}

func (s *Server) stackStateOf(ctx context.Context, record *stacks.Record, manifest *stacks.Manifest) (*stackState, error) {
	preset, found := manifest.PresetByID(record.Preset)
	if !found {
		preset = manifest.Presets[0]
	}
	inPreset := map[string]bool{}
	for _, service := range preset.Services {
		inPreset[service] = true
	}
	records, err := s.modules.stackStore.Steps(ctx, record.ProjectID)
	if err != nil {
		return nil, err
	}
	stored, err := s.modules.stackStore.StoredCredentials(ctx, record.ProjectID)
	if err != nil {
		return nil, err
	}
	state := &stackState{
		StackID: manifest.ID, Title: manifest.Title, Version: manifest.Version, Preset: preset.ID,
		PresetTitle: preset.Title, Status: record.Status, Services: []stackServiceState{}, Steps: []stackStepState{},
		Actions: []stacks.Action{}, Credentials: []stackCredentialListing{}, Limitations: preset.Limitations,
		LiveReleaseID: record.LiveReleaseID, ConfiguredReleaseID: record.ConfiguredReleaseID,
		StartedAt: timePointer(record.StartedAt), FinishedAt: timePointer(record.FinishedAt), UpdatedAt: record.UpdatedAt,
	}
	if state.Limitations == nil {
		state.Limitations = []string{}
	}
	if s.modules.stacks != nil && s.modules.stacks.Running(record.ProjectID) {
		state.Status = stacks.StatusConfiguring
	}
	live, liveErr := s.stackLiveRelease(ctx, record.EnvironmentID)
	state.Readiness.Installed = liveErr == nil
	if liveErr == nil && state.Status != stacks.StatusConfiguring && record.Status == stacks.StatusWaiting {
		// Live, and the loop has not picked it up yet.
		state.Status = stacks.StatusConfiguring
	}
	if liveErr != nil {
		state.Status = stacks.StatusWaiting
	}

	// Steps in the manifest's order, without the ones for services this
	// preset does not run.
	status := map[string]stacks.StepStatus{}
	byService := map[string][]stacks.StepStatus{}
	for _, step := range manifest.Steps {
		current, found := records[step.ID]
		if !found {
			current = stacks.StepRecord{StepID: step.ID, Status: stacks.StepPending}
		}
		if current.Status == stacks.StepSkipped && current.Message == stacks.AbsentMessage {
			continue
		}
		applies := inPreset[step.Service]
		for _, service := range step.Needs {
			applies = applies && inPreset[service]
		}
		if !applies {
			continue
		}
		status[step.ID] = current.Status
		byService[step.Service] = append(byService[step.Service], current.Status)
		state.Steps = append(state.Steps, stackStepState{
			ID: step.ID, Service: step.Service, Title: step.Title, Stage: step.Stage, Status: current.Status,
			Attempts: current.Attempts, Message: current.Message, Error: current.Error,
			StartedAt: timePointer(current.StartedAt), FinishedAt: timePointer(current.FinishedAt),
		})
		if current.Status == stacks.StepAction && current.Action != nil {
			state.Actions = append(state.Actions, *current.Action)
		}
	}
	sort.SliceStable(state.Actions, func(i, j int) bool { return state.Actions[i].Blocking && !state.Actions[j].Blocking })

	// Containers of the live release, by service.
	containers := map[string]dockerx.Container{}
	if liveErr == nil && live.RuntimeID != "" && s.modules.docker != nil {
		listed, err := s.modules.docker.ListContainersWithLabels(ctx, map[string]string{"com.docker.compose.project": live.RuntimeID})
		if err == nil {
			for _, container := range listed {
				service := container.Labels["com.docker.compose.service"]
				if service == "" || container.Labels["com.docker.compose.oneoff"] == "True" {
					continue
				}
				if previous, found := containers[service]; !found || (previous.State != "running" && container.State == "running") {
					containers[service] = container
				}
			}
		}
	}
	healthy := liveErr == nil
	images := map[string]string{}
	if definition, err := blueprint.Get(preset.Template); err == nil {
		for _, service := range definition.Services {
			images[service.Name] = service.Image.Reference
		}
	}
	for _, info := range manifest.Services {
		if !inPreset[info.Name] {
			continue
		}
		service := stackServiceState{Name: info.Name, Title: info.Title, Purpose: info.Purpose, Access: info.Access,
			Image: images[info.Name], DocsURL: info.DocsURL, Status: serviceStatus(state.Status, byService[info.Name])}
		if container, found := containers[info.Name]; found {
			service.Container = &stackContainerState{Name: container.Name, State: container.State, Health: container.Health}
			if container.Image != "" {
				service.Image = container.Image
			}
			if container.State != "running" || container.Health == "unhealthy" || container.Health == "starting" {
				healthy = false
			}
			if info.Access == stacks.AccessPrivate && info.Port > 0 {
				for _, port := range container.Ports {
					if int(port.PrivatePort) == info.Port && port.PublicPort > 0 && port.IP == "127.0.0.1" {
						service.Address = "127.0.0.1:" + strconv.Itoa(int(port.PublicPort))
					}
				}
			}
		} else {
			healthy = false
		}
		state.Services = append(state.Services, service)
	}
	state.Readiness.Healthy = healthy
	connected := liveErr == nil
	for _, step := range manifest.Steps {
		if current, applies := status[step.ID]; applies && step.Stage == stacks.StageLink && current != stacks.StepDone {
			connected = false
		}
	}
	state.Readiness.Connected = connected
	state.Readiness.Playback = status["jellyfin.setup"] == stacks.StepDone && status["jellyfin.libraries"] == stacks.StepDone
	acquire, applies := status["prowlarr.sources"]
	state.Readiness.Acquisition = applies && acquire == stacks.StepDone && connected &&
		status["downloads.verify"] == stacks.StepDone

	for _, credential := range manifest.Credentials {
		if credential.Internal || !inPreset[credential.Service] {
			continue
		}
		state.Credentials = append(state.Credentials, stackCredentialListing{Name: credential.Name, Service: credential.Service,
			Label: credential.Label, Username: credential.Username, Stored: stored[credential.Name]})
	}
	state.Summary = stackSummary(state)
	return state, nil
}

func serviceStatus(stack stacks.Status, steps []stacks.StepStatus) string {
	if stack == stacks.StatusWaiting {
		return "waiting"
	}
	outcome := "ok"
	for _, step := range steps {
		switch step {
		case stacks.StepFailed:
			return "failed"
		case stacks.StepAction:
			outcome = "action_required"
		case stacks.StepPending, stacks.StepRunning:
			if outcome == "ok" {
				outcome = "configuring"
			}
		case stacks.StepSkipped:
			if outcome == "ok" {
				outcome = "skipped"
			}
		}
	}
	return outcome
}

// stackSummary is the sentence the page leads with. It never says the stack
// is set up when a link or a source is missing.
func stackSummary(state *stackState) string {
	done, total := 0, 0
	var failed *stackStepState
	for index := range state.Steps {
		total++
		switch state.Steps[index].Status {
		case stacks.StepDone:
			done++
		case stacks.StepFailed:
			if failed == nil {
				failed = &state.Steps[index]
			}
		}
	}
	switch state.Status {
	case stacks.StatusWaiting:
		return "The deployment is still bringing the stack up. Its services are set up and connected once its release is live."
	case stacks.StatusConfiguring:
		return fmt.Sprintf("Setting up the services and connecting them to each other: %d of %d steps done.", done, total)
	case stacks.StatusFailed:
		if failed != nil {
			return fmt.Sprintf("“%s” failed. Fix what its error names, then retry: nothing already set up is done again.", failed.Title)
		}
		return "The last configuration pass did not finish. Retry runs it again."
	case stacks.StatusAction:
		for _, action := range state.Actions {
			if action.Blocking {
				return fmt.Sprintf("Set up and connected, except for one thing only you can do: %s.", lowerFirst(action.Title))
			}
		}
		return "Set up and connected, with something left for you below."
	}
	if state.Readiness.Acquisition {
		return "Every service is set up and connected: requests are searched for through your sources and land in Jellyfin."
	}
	if state.Preset == "essential" {
		return "Jellyfin and Seerr are set up and signed in to each other. Requests are collected; adding the titles is up to you."
	}
	return "Every service is set up and connected."
}

func lowerFirst(text string) string {
	if text == "" {
		return text
	}
	runes := []rune(text)
	if runes[0] >= 'A' && runes[0] <= 'Z' && (len(runes) < 2 || runes[1] < 'A' || runes[1] > 'Z') {
		runes[0] += 'a' - 'A'
	}
	return string(runes)
}

func (s *Server) handleStackConfigure(w http.ResponseWriter, r *http.Request) error {
	record, _, err := s.stackRecord(r)
	if err != nil {
		return err
	}
	if s.modules.stacks == nil {
		return httpx.Err(http.StatusServiceUnavailable, "stacks_unavailable", "stack configuration is not running on this dashboard")
	}
	switch err := s.modules.stacks.Request(r.Context(), record.ProjectID); {
	case errors.Is(err, stacks.ErrBusy):
		return httpx.Err(http.StatusConflict, "stack_busy", "the stack is already being configured; this pass covers every step")
	case errors.Is(err, stacks.ErrWaiting):
		return httpx.Err(http.StatusConflict, "stack_waiting", "the stack has no live release yet; it is configured as soon as its deployment finishes")
	case err != nil:
		return err
	}
	httpx.SetAudit(r, "deploy.stack.configure", strconv.FormatInt(record.ProjectID, 10), map[string]any{
		"stack": record.StackID, "preset": record.Preset,
	})
	httpx.JSON(w, http.StatusAccepted, stackConfigureAnswer{Status: stacks.StatusConfiguring})
	return nil
}

func (s *Server) handleStackCredentialReveal(w http.ResponseWriter, r *http.Request) error {
	record, manifest, err := s.stackRecord(r)
	if err != nil {
		return err
	}
	name := chi.URLParam(r, "name")
	spec, found := manifest.CredentialByName(name)
	notFound := httpx.Err(http.StatusNotFound, "credential_not_found", "the stack holds no such account")
	if !found || spec.Internal {
		return notFound
	}
	value, stored, err := s.modules.stackStore.Credentials(record.ProjectID).Get(r.Context(), name)
	if err != nil {
		return err
	}
	if !stored {
		return notFound
	}
	// A GET, which the mutation audit skips: the disclosure is recorded here,
	// without its value, as a variable's reveal is.
	s.recordAudit(r, "deploy.stack.credential.reveal", name, map[string]any{
		"deploymentId": record.ProjectID, "stack": record.StackID,
	})
	httpx.JSON(w, http.StatusOK, stackCredentialReveal{Name: name, Username: spec.Username, Password: value})
	return nil
}

// registerStack records a deployment made from a stack's preset, in the
// request that committed it, so its first release is configured as soon as
// it is live.
func (s *Server) registerStack(r *http.Request, draft *deploy.Draft, result *deploy.DraftCommitResult) {
	if s.modules.stackStore == nil || result == nil || draft.Data.Source == nil ||
		draft.Data.Source.Mode != deploy.SourceModeBlueprint {
		return
	}
	manifest, preset, found := catalog.ForTemplate(draft.Data.Source.BlueprintID)
	if !found {
		return
	}
	if err := s.modules.stackStore.Register(r.Context(), result.ProjectID, result.EnvironmentID,
		manifest.ID, preset.ID, httpx.MustPrincipal(r).Username()); err != nil {
		if s.Log != nil {
			s.Log.Warn("a stack deployment could not be registered", "project", result.ProjectID, "error", err)
		}
		return
	}
	if s.modules.stacks != nil {
		s.modules.stacks.Kick()
	}
}

// stackLiveRelease is an environment's live release as the stack service
// reads it: its runtime (the Compose project) and the template it was
// rendered from.
func (s *Server) stackLiveRelease(ctx context.Context, environmentID int64) (stacks.Live, error) {
	var live stacks.Live
	var blueprintID, template string
	// A Compose template's release names its template in the source identity
	// ("media-essential@1.0.0"); blueprint_id is a single image's.
	err := s.Store.DB.QueryRowContext(ctx, `
		SELECT e.live_release_id, COALESCE(rt.kind, ''), COALESCE(rt.runtime_id, ''), COALESCE(r.blueprint_id, ''),
		       COALESCE(json_extract(r.source_identity_json, '$.template'), '')
		  FROM deploy_environments e
		  LEFT JOIN deploy_release_runtimes rt ON rt.release_id = e.live_release_id
		  LEFT JOIN deploy_releases r ON r.id = e.live_release_id
		 WHERE e.id = ?`, environmentID).Scan(&live.ReleaseID, &live.Kind, &live.RuntimeID, &blueprintID, &template)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && live.ReleaseID == 0) {
		return stacks.Live{}, stacks.ErrNoLiveRelease
	}
	live.TemplateID = blueprintID
	if id, _, found := strings.Cut(template, "@"); found && id != "" {
		live.TemplateID = id
	}
	return live, err
}

// newStackService is the stack loop over this dashboard's store, Docker and
// deployment variables.
func (s *Server) newStackService() *stacks.Service {
	return stacks.NewService(stacks.ServiceDeps{
		Store:  s.modules.stackStore,
		Docker: s.modules.docker,
		Manifest: func(id string) (*stacks.Manifest, bool) {
			return catalog.ByID(id)
		},
		LiveRelease: s.stackLiveRelease,
		Variables: func(ctx context.Context, projectID, environmentID int64) (map[string]string, error) {
			return s.modules.deployPlanning.LiteralVariableValues(ctx, projectID, environmentID)
		},
		Log: s.Log,
	})
}
