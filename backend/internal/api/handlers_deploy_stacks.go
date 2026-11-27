package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
	// Address is the label the service's own address is made with beside
	// the domain (blueprint.ServiceHostname); empty for the primary service,
	// which is the domain itself, and for one with no web interface.
	Address string `json:"address,omitempty"`
	Primary bool   `json:"primary,omitempty"`
}

func (s *Server) handleStackCatalogue(w http.ResponseWriter, _ *http.Request) error {
	out := []stackCatalogueEntry{}
	for _, manifest := range catalog.All() {
		entry := stackCatalogueEntry{ID: manifest.ID, Version: manifest.Version, Title: manifest.Title,
			Description: manifest.Description, Presets: []stackCataloguePreset{}, Services: []stackCatalogueItem{}}
		images, labels := map[string]string{}, map[string]string{}
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
				if labels[service.Name] == "" {
					labels[service.Name] = service.Address
				}
			}
			entry.Presets = append(entry.Presets, stackCataloguePreset{Preset: preset,
				TemplateVersion: definition.Version, MemoryMB: memory})
		}
		for _, service := range manifest.Services {
			entry.Services = append(entry.Services, stackCatalogueItem{ServiceInfo: service, Image: images[service.Name],
				Address: labels[service.Name], Primary: service.Access == stacks.AccessPublic})
		}
		out = append(out, entry)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// stackState is GET /deploy/{id}/stack.
type stackState struct {
	StackID     string                   `json:"stackId"`
	Title       string                   `json:"title"`
	Version     string                   `json:"version"`
	Preset      string                   `json:"preset"`
	PresetTitle string                   `json:"presetTitle"`
	Status      stacks.Status            `json:"status"`
	Summary     string                   `json:"summary"`
	Readiness   stackReadiness           `json:"readiness"`
	Services    []stackServiceState      `json:"services"`
	Steps       []stackStepState         `json:"steps"`
	Actions     []stacks.Action          `json:"actions"`
	Credentials []stackCredentialListing `json:"credentials"`
	// Login is the one account every application signs in with.
	Login stackLoginState `json:"login"`
	// Addresses are the service domains the deployment's configuration does
	// not have yet, for "Give every app its address".
	Addresses           stackAddresses `json:"addresses"`
	Limitations         []string       `json:"limitations"`
	LiveReleaseID       int64          `json:"liveReleaseId,omitempty"`
	ConfiguredReleaseID int64          `json:"configuredReleaseId,omitempty"`
	StartedAt           *time.Time     `json:"startedAt,omitempty"`
	FinishedAt          *time.Time     `json:"finishedAt,omitempty"`
	UpdatedAt           time.Time      `json:"updatedAt,omitzero"`
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

type stackLoginState struct {
	Username string `json:"username"`
	Stored   bool   `json:"stored"`
}

// stackAddresses is the deployment's primary domain and the address each
// service of the preset with a label would get beside it but has not got.
// Primary is empty when the deployment has no primary domain; the
// hostnames are then empty too, until one is given.
type stackAddresses struct {
	Primary string         `json:"primary"`
	Missing []stackAddress `json:"missing"`
	// Unpublished are the services whose address is saved but that the
	// live release does not serve, while no run of the deployment is under
	// way: the release that was to publish them failed, or none was
	// started. Pressing the same button deploys again.
	Unpublished []stackAddress `json:"unpublished"`
}

type stackAddress struct {
	Service  string `json:"service"`
	Title    string `json:"title"`
	Hostname string `json:"hostname"`
}

// stackLoginRequest changes the stack's one password.
type stackLoginRequest struct {
	Password string `json:"password"`
}

// stackAddressesRequest names the primary domain when the deployment has
// none; it is ignored otherwise.
type stackAddressesRequest struct {
	Domain string `json:"domain,omitempty"`
}

// stackAddressesAnswer is the release started with the added domains.
type stackAddressesAnswer struct {
	RunID   int64    `json:"runId"`
	Domains []string `json:"domains"`
}

type stackContainerState struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Health string `json:"health,omitempty"`
}

type stackStepState struct {
	ID       string            `json:"id"`
	Service  string            `json:"service"`
	Title    string            `json:"title"`
	Stage    stacks.Stage      `json:"stage"`
	Status   stacks.StepStatus `json:"status"`
	Attempts int               `json:"attempts"`
	Message  string            `json:"message,omitempty"`
	Error    string            `json:"error,omitempty"`
	// Log is what the step said in its last pass (a tool's report), masked
	// as the message and error are.
	Log        []string   `json:"log,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
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
		Addresses:     stackAddresses{Missing: []stackAddress{}},
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
			Attempts: current.Attempts, Message: current.Message, Error: current.Error, Log: current.Log,
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
	urls := map[string]string{}
	if liveErr == nil {
		urls = s.stackServiceURLs(ctx, manifest, record.EnvironmentID, live.ReleaseID)
	}
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
		service.URL = urls[info.Name]
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
		if credential.Internal || (credential.Service != "" && !inPreset[credential.Service]) {
			continue
		}
		state.Credentials = append(state.Credentials, stackCredentialListing{Name: credential.Name, Service: credential.Service,
			Label: credential.Label, Username: credential.Username, Stored: stored[credential.Name]})
	}
	if login, found := manifest.CredentialByName(stacks.CredentialLogin); found {
		state.Login = stackLoginState{Username: login.Username, Stored: stored[login.Name]}
	}
	if domains, err := s.stackDomains(ctx, record.EnvironmentID); err == nil {
		state.Addresses = stackMissingAddresses(manifest, preset, domains, "")
		// While a run of the deployment has not ended, it may be the release
		// that publishes them: that one is neither unfinished nor to be
		// started again.
		if state.LiveReleaseID != 0 && !s.stackReleaseUnderWay(ctx, record.ProjectID) {
			state.Addresses.Unpublished = stackUnpublishedAddresses(manifest, domains, state.Services)
		}
	}
	state.Summary = stackSummary(state)
	return state, nil
}

// stackConfiguration is the deployment's configuration as its Domains
// settings read and save it.
func (s *Server) stackConfiguration(ctx context.Context, record *stacks.Record) (*deploy.EnvironmentConfiguration, error) {
	if s.modules.deployPlanning == nil {
		return nil, errors.New("deployment planning is not running on this dashboard")
	}
	return s.modules.deployPlanning.EnvironmentConfiguration(ctx, record.ProjectID, record.EnvironmentID)
}

// stackDomains are the environment's configured domains, as the Domains
// settings show them, read without the rest of the configuration: the page
// asks for the state every two seconds while a pass runs.
func (s *Server) stackDomains(ctx context.Context, environmentID int64) ([]deploy.PlannedDomain, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `
		SELECT config_json FROM deploy_dependencies
		 WHERE environment_id = ? AND release_id = 0 AND kind = 'domain'
		 ORDER BY resource_id`, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	domains := []deploy.PlannedDomain{}
	for rows.Next() {
		var config string
		if err := rows.Scan(&config); err != nil {
			return nil, err
		}
		var domain deploy.PlannedDomain
		if err := json.Unmarshal([]byte(config), &domain); err != nil {
			return nil, err
		}
		domains = append(domains, domain)
	}
	return domains, rows.Err()
}

// stackPrimaryDomain is the domain the primary service is served at: the
// first that names no other service and sends nobody elsewhere.
func stackPrimaryDomain(domains []deploy.PlannedDomain) (deploy.PlannedDomain, bool) {
	for _, domain := range domains {
		if domain.Service == "" && !domain.Redirects() {
			return domain, true
		}
	}
	return deploy.PlannedDomain{}, false
}

// stackMissingAddresses is, for every service of the preset that has an
// address label, the address it would get beside the primary domain when no
// domain of the configuration serves it yet. The labels are the shipped
// template's, whatever version the deployment runs: the 1.0.0 templates
// already published those interfaces on loopback, where the route reaches
// them. primary stands in for a deployment without a primary domain.
func stackMissingAddresses(manifest *stacks.Manifest, preset stacks.Preset, domains []deploy.PlannedDomain, primary string) stackAddresses {
	out := stackAddresses{Missing: []stackAddress{}, Unpublished: []stackAddress{}}
	if domain, found := stackPrimaryDomain(domains); found {
		out.Primary = domain.Hostname
	} else {
		out.Primary = primary
	}
	served := map[string]bool{}
	for _, domain := range domains {
		if domain.Service != "" {
			served[domain.Service] = true
		}
	}
	labels := map[string]string{}
	if definition, err := blueprint.Get(preset.Template); err == nil {
		for _, service := range definition.Services {
			labels[service.Name] = service.Address
		}
	}
	inPreset := map[string]bool{}
	for _, service := range preset.Services {
		inPreset[service] = true
	}
	for _, info := range manifest.Services {
		label := labels[info.Name]
		if !inPreset[info.Name] || label == "" || served[info.Name] {
			continue
		}
		hostname := ""
		if out.Primary != "" {
			hostname = blueprint.ServiceHostname(out.Primary, label)
		}
		out.Missing = append(out.Missing, stackAddress{Service: info.Name, Title: info.Title, Hostname: hostname})
	}
	return out
}

// stackReleaseUnderWay is whether a run of the deployment has not ended yet.
func (s *Server) stackReleaseUnderWay(ctx context.Context, projectID int64) bool {
	if s.modules.deployRuns == nil {
		return false
	}
	active, err := s.modules.deployRuns.ProjectActive(ctx, projectID)
	return err == nil && active
}

// stackUnpublishedAddresses are the services a saved domain names that the
// live release gives no URL: saved by "Give every app its address" or on the
// Domains settings, then not published because that release failed.
func stackUnpublishedAddresses(manifest *stacks.Manifest, domains []deploy.PlannedDomain, services []stackServiceState) []stackAddress {
	served := map[string]bool{}
	for _, service := range services {
		served[service.Name] = service.URL != ""
	}
	titles := map[string]string{}
	for _, info := range manifest.Services {
		titles[info.Name] = info.Title
	}
	out := []stackAddress{}
	seen := map[string]bool{}
	for _, domain := range domains {
		live, inStack := served[domain.Service]
		if domain.Service == "" || domain.Redirects() || !inStack || live || seen[domain.Service] {
			continue
		}
		seen[domain.Service] = true
		out = append(out, stackAddress{Service: domain.Service, Title: titles[domain.Service], Hostname: domain.Hostname})
	}
	return out
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
	case errors.Is(err, stacks.ErrNotAPreset):
		return httpx.Err(http.StatusConflict, "stack_not_preset", "the deployment's live release is not one of the stack's presets, so there is nothing of the stack's to configure; it is configured again once a release of a preset is live")
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

// handleStackLoginUpdate changes the stack's one password: stored at once,
// the one before kept until every application answers to it, and a pass
// asked for that brings each application to it.
func (s *Server) handleStackLoginUpdate(w http.ResponseWriter, r *http.Request) error {
	record, _, err := s.stackRecord(r)
	if err != nil {
		return err
	}
	var request stackLoginRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	if err := stacks.ValidateLogin(request.Password); err != nil {
		return httpx.Err(http.StatusUnprocessableEntity, "password_weak", err.Error())
	}
	if s.modules.stacks == nil {
		return httpx.Err(http.StatusServiceUnavailable, "stacks_unavailable", "stack configuration is not running on this dashboard")
	}
	// A pass under way has read the password it is giving the applications;
	// changing it under that pass could leave qBittorrent on a password the
	// stack no longer holds.
	if s.modules.stacks.Running(record.ProjectID) {
		return httpx.Err(http.StatusConflict, "stack_busy", "the stack is being configured; change the password once this pass finishes")
	}
	if err := stacks.ChangeLogin(r.Context(), s.modules.stackStore.Credentials(record.ProjectID), request.Password); err != nil {
		return err
	}
	status := stacks.StatusConfiguring
	switch err := s.modules.stacks.Request(r.Context(), record.ProjectID); {
	case errors.Is(err, stacks.ErrWaiting), errors.Is(err, stacks.ErrNotAPreset):
		// Stored for the first pass there is, which reads it.
		status = stacks.StatusWaiting
	case errors.Is(err, stacks.ErrBusy):
		// A pass started between the check and the change: ask for another,
		// which runs once it ends.
		if err := s.modules.stackStore.RequestPass(r.Context(), record.ProjectID); err != nil {
			return err
		}
	case err != nil:
		return err
	}
	httpx.SetAudit(r, "deploy.stack.login.update", strconv.FormatInt(record.ProjectID, 10), map[string]any{
		"stack": record.StackID, "preset": record.Preset,
	})
	httpx.JSON(w, http.StatusAccepted, stackConfigureAnswer{Status: status})
	return nil
}

// handleStackAddresses gives every service of the preset that has an address
// label and no domain its address beside the primary domain: the domains are
// saved as a Domains settings save saves them, with its checks and its audit,
// and a release is started as "Deploy changes" starts one.
func (s *Server) handleStackAddresses(w http.ResponseWriter, r *http.Request) error {
	record, manifest, err := s.stackRecord(r)
	if err != nil {
		return err
	}
	var request stackAddressesRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := httpx.DecodeJSON(r, &request); err != nil {
			return err
		}
	}
	preset, found := manifest.PresetByID(record.Preset)
	if !found {
		return httpx.Err(http.StatusConflict, "stack_not_preset", "the stack's preset is not one this dashboard ships")
	}
	configuration, err := s.stackConfiguration(r.Context(), record)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	domains := append([]deploy.PlannedDomain(nil), configuration.Domains...)
	primary, hasPrimary := stackPrimaryDomain(domains)
	added := []string{}
	if !hasPrimary {
		hostname := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(request.Domain)), ".")
		if hostname == "" {
			return httpx.Err(http.StatusUnprocessableEntity, "stack_no_domain", "the deployment has no domain to put the apps' addresses beside; give one")
		}
		primary = deploy.PlannedDomain{Hostname: hostname, HTTPS: true, Ownership: deploy.OwnershipManaged}
		domains = append(domains, primary)
		added = append(added, hostname)
	}
	missing := stackMissingAddresses(manifest, preset, domains, "")
	if len(missing.Missing) == 0 && hasPrimary {
		// Every address is saved. One the live release does not serve yet
		// is published by deploying again, which is what the button is
		// for when the release that was to publish it failed.
		state, stateErr := s.stackStateOf(r.Context(), record, manifest)
		if stateErr != nil || len(state.Addresses.Unpublished) == 0 {
			return httpx.Err(http.StatusConflict, "stack_addresses_present", "every app of the stack already has an address")
		}
		return s.deployStackAddresses(w, r, record, configuration.Revision, nil)
	}
	for _, address := range missing.Missing {
		// Served as the primary is: over HTTPS when it is, and through the
		// tunnel when it is, since a server behind one may take no traffic
		// of its own.
		domains = append(domains, deploy.PlannedDomain{
			Hostname: address.Hostname, HTTPS: primary.HTTPS, Tunnel: primary.Tunnel,
			Ownership: deploy.OwnershipManaged, Service: address.Service,
		})
		added = append(added, address.Hostname)
	}
	saved, detail, err := s.saveDeploymentConfiguration(r, record.ProjectID, record.EnvironmentID, deploy.ConfigurationWriteRequest{
		Revision: configuration.Revision, Build: configuration.Build, Runtime: configuration.Runtime,
		Dependencies: configuration.Dependencies, Checks: configuration.Checks, Domains: domains,
	})
	if err != nil {
		return err
	}
	s.recordAudit(r, "deploy.configuration.update", strconv.FormatInt(record.EnvironmentID, 10), detail)
	return s.deployStackAddresses(w, r, record, saved.Revision, added)
}

// deployStackAddresses starts the release that publishes the stack's
// addresses at the configuration's revision, as "Deploy changes" starts
// one, and answers with it.
func (s *Server) deployStackAddresses(w http.ResponseWriter, r *http.Request, record *stacks.Record, revision int, added []string) error {
	if added == nil {
		added = []string{}
	}
	project, err := s.modules.deployStore.Get(r.Context(), record.ProjectID)
	if err != nil {
		return mapDeployError(err)
	}
	principal := httpx.MustPrincipal(r)
	run, err := s.enqueueNormalizedDeploymentAtSource(r.Context(), project, record.EnvironmentID,
		deploy.OperationDeploy, 0, deploy.TriggerManual, principal.Username(), strings.TrimSpace(r.Header.Get("Idempotency-Key")),
		nil, "", "", revision)
	if err != nil {
		return mapDeployError(err)
	}
	s.recordAudit(r, "deploy.run.request", fmt.Sprint(run.ID), map[string]any{
		"deploymentId": record.ProjectID, "environmentId": record.EnvironmentID, "operation": deploy.OperationDeploy,
	})
	httpx.SetAudit(r, "deploy.stack.addresses", strconv.FormatInt(record.ProjectID, 10), map[string]any{
		"stack": record.StackID, "domains": added, "runId": run.ID,
	})
	httpx.JSON(w, http.StatusAccepted, stackAddressesAnswer{RunID: run.ID, Domains: added})
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

// stackServiceURLs are the addresses the live release serves each service
// at, from the domains its runtime snapshot records: an HTTPS one when a
// service has one, the primary domain for the public service. A release
// whose snapshot cannot be read, and a tailnet-only one, fall back to the
// address the fleet gives the deployment, for the public service.
func (s *Server) stackServiceURLs(ctx context.Context, manifest *stacks.Manifest, environmentID, releaseID int64) map[string]string {
	public := ""
	for _, service := range manifest.Services {
		if service.Access == stacks.AccessPublic && public == "" {
			public = service.Name
		}
	}
	var domains []deploy.PlannedDomain
	if s.modules.deployRuns != nil && releaseID > 0 {
		domains, _ = s.modules.deployRuns.ReleaseDomains(ctx, releaseID)
	}
	urls := stackURLsOf(public, domains)
	if urls[public] == "" {
		if address := s.stackPublicURL(ctx, environmentID); address != "" {
			urls[public] = address
		}
	}
	return urls
}

// stackURLsOf is each service's address among a release's domains: an HTTPS
// one (or one through the tunnel, which Cloudflare serves over HTTPS) when it
// has one, else the first; a domain with no service is the public one's.
func stackURLsOf(public string, domains []deploy.PlannedDomain) map[string]string {
	urls := map[string]string{}
	secure := map[string]bool{}
	for _, domain := range domains {
		if domain.Redirects() || domain.Hostname == "" {
			continue
		}
		service := domain.Service
		if service == "" {
			service = public
		}
		https := domain.HTTPS || domain.Tunnel
		if _, found := urls[service]; found && (secure[service] || !https) {
			continue
		}
		scheme := "http://"
		if https {
			scheme = "https://"
		}
		urls[service], secure[service] = scheme+domain.Hostname, https
	}
	return urls
}

// stackPublicURL is the address the deployment's domain gives its public
// service, read as the fleet reads a deployment's endpoint: the
// environment's first domain that serves the primary service, else a
// tailnet-only environment's address. "" when it has neither.
func (s *Server) stackPublicURL(ctx context.Context, environmentID int64) string {
	var endpoint string
	if err := s.Store.DB.QueryRowContext(ctx, `
		SELECT COALESCE(NULLIF((
		         SELECT d.resource_id FROM deploy_dependencies d
		          WHERE d.environment_id = ? AND d.kind = 'domain'
		            AND COALESCE(json_extract(d.config_json, '$.service'), '') = ''
		          ORDER BY d.id LIMIT 1
		       ), ''), (
		         SELECT x.url FROM deploy_preview_addresses x
		          WHERE x.environment_id = ? AND x.url != ''
		       ), '')`, environmentID, environmentID).Scan(&endpoint); err != nil || endpoint == "" {
		return ""
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	return parsed.String()
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
		PublicURLs: s.stackServiceURLs,
		Log:        s.Log,
	})
}
