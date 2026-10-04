package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

type workloadInventory struct {
	containers []dockerx.Container
	stacks     []dockerx.ComposeStack
	pm2        []procs.PM2Process
	units      []procs.Unit
	listeners  []proxysvc.Listener
	selfStack  string
	selfID     string
}

func (s *Server) discoverWorkloads(ctx context.Context) (*deploy.WorkloadDiscovery, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var inventory workloadInventory
	silences := []string{}
	var lock sync.Mutex
	var readers sync.WaitGroup
	read := func(operation func() string) {
		readers.Add(1)
		go func() {
			defer readers.Done()
			if silence := operation(); silence != "" {
				lock.Lock()
				silences = append(silences, silence)
				lock.Unlock()
			}
		}()
	}
	read(func() string {
		if s.modules.docker == nil {
			return "Docker inventory is unavailable."
		}
		readCtx := s.modules.docker.WithReadSnapshot(ctx)
		var err error
		inventory.containers, err = s.modules.docker.ListContainers(readCtx, true)
		if err != nil {
			return "Docker inventory could not be read."
		}
		if s.modules.selfUpdate != nil {
			if location, err := s.modules.selfUpdate.Location(readCtx); err == nil {
				inventory.selfStack, inventory.selfID = location.Project, location.Container
			}
		}
		inventory.stacks, err = s.modules.docker.ListStacks(readCtx, s.Cfg.ComposeRoots)
		if err != nil {
			return "Compose stack inventory could not be read."
		}
		return ""
	})
	read(func() string {
		if s.modules.pm2 == nil {
			return "PM2 inventory is unavailable."
		}
		var err error
		inventory.pm2, err = s.modules.pm2.ListExisting(ctx)
		if err != nil {
			return "Some existing PM2 daemons could not be read; their applications are omitted."
		}
		return ""
	})
	read(func() string {
		if s.modules.systemd == nil {
			return "Systemd inventory is unavailable."
		}
		var err error
		inventory.units, err = s.modules.systemd.List(ctx)
		if err != nil {
			return "Systemd services could not be read."
		}
		return ""
	})
	read(func() string {
		var err error
		inventory.listeners, err = proxysvc.ListListeners(ctx)
		if err != nil {
			return "Listening host processes could not be read."
		}
		return ""
	})
	readers.Wait()
	if ctx.Err() != nil {
		// Successful managers remain useful when another inventory times out.
		// Every failed manager has its own silence instead of an empty claim.
		silences = append(silences, "Discovery reached its time limit; some workloads may be missing.")
	}
	items := workloadCandidates(inventory)
	paths := files.New(s.Cfg.DeployRoots)
	for index := range items {
		candidate := &items[index]
		if candidate.SourcePath != "" {
			resolved, err := paths.Resolve(candidate.SourcePath)
			if err == nil {
				info, statErr := os.Stat(resolved)
				candidate.ConfigurationAvailable = statErr == nil && info.Mode().IsRegular()
			}
			if !candidate.ConfigurationAvailable {
				candidate.Warnings = append(candidate.Warnings, "The original source or configuration file is missing or outside deployment roots. Recovery will check whether a complete deployment recipe can be reconstructed.")
			}
		}
		candidate.Digest = deploy.WorkloadDigest(*candidate)
	}
	if s.Store != nil {
		rows, err := s.Store.DB.QueryContext(ctx, `SELECT e.project_id,d.resource_kind,d.resource_id,d.config_json FROM deploy_dependencies d JOIN deploy_environments e ON e.id=d.environment_id WHERE d.kind='runtime' AND d.ownership IN ('observed','managed') AND d.release_id=0`)
		if err != nil && ctx.Err() == nil {
			return nil, err
		}
		if err == nil {
			defer rows.Close()
			imported := map[string]int64{}
			for rows.Next() {
				var id int64
				var resourceKind, resourceID, metadata string
				if err := rows.Scan(&id, &resourceKind, &resourceID, &metadata); err != nil {
					return nil, err
				}
				var candidate deploy.WorkloadCandidate
				if json.Unmarshal([]byte(metadata), &candidate) == nil && candidate.Key != "" {
					imported[candidate.Key] = id
				} else {
					for _, kind := range []string{"stack", "container", "pm2", "systemd", "process"} {
						if expected, _ := workloadImportResource(kind); expected == resourceKind {
							imported[kind+":"+resourceID] = id
						}
					}
				}
			}
			if err := rows.Err(); err != nil && ctx.Err() == nil {
				return nil, err
			}
			for index := range items {
				items[index].ImportedProjectID = imported[items[index].Key]
			}
		}
	}
	sort.Strings(silences)
	return &deploy.WorkloadDiscovery{CheckedAt: time.Now().UTC(), Items: items, Silences: silences}, nil
}

func workloadCandidates(inventory workloadInventory) []deploy.WorkloadCandidate {
	items := []deploy.WorkloadCandidate{}
	managedStacks := map[string]bool{}
	for _, container := range inventory.containers {
		self := inventory.selfID != "" && (container.ID == inventory.selfID || container.Name == inventory.selfID)
		// Location can be unavailable while the operator's checkout is
		// temporarily inaccessible. Its documented backend image together
		// with Compose's backend service still identifies this install.
		self = self || container.Labels["com.docker.compose.service"] == "backend" &&
			strings.HasPrefix(container.Image, "just-dashboard-backend:")
		if self || container.Labels["io.just-dashboard.managed"] == "true" || container.Labels["com.just-dashboard.ingress"] == "true" {
			managedStacks[container.ComposeStack] = true
			managedStacks[container.Labels["com.docker.compose.project"]] = true
		}
	}
	for _, stack := range inventory.stacks {
		if !stack.Deployed || stack.Name == inventory.selfStack || managedStacks[stack.Name] {
			continue
		}
		candidate := deploy.WorkloadCandidate{
			Key: "stack:" + stack.Name, Kind: "stack", Name: stack.Name, ResourceID: stack.Name,
			State: string(stack.State), Running: stack.Running, Total: len(stack.Services),
			Services: []deploy.WorkloadService{}, ManagerURL: "/docker/stacks/" + url.PathEscape(stack.Name),
			Warnings: []string{"All existing containers, including stopped services, stay under their original Compose project. Import does not start them."},
		}
		if len(stack.ConfigFiles) > 0 {
			candidate.SourcePath = stack.ConfigFiles[0]
		}
		if len(stack.ConfigFiles) > 1 {
			candidate.Warnings = append(candidate.Warnings, "This stack uses multiple Compose files; their original order and settings remain with Compose.")
		}
		for _, service := range stack.Services {
			state := service.State
			if service.Missing {
				state = "not created"
			} else if state == "" {
				state = "unknown"
			}
			candidate.Services = append(candidate.Services, deploy.WorkloadService{
				Name: service.Name, ResourceID: service.Container, State: state, Health: service.Health,
				Image: service.Image, Ports: importedPorts(service.Ports),
			})
		}
		items = append(items, candidate)
	}
	for _, container := range inventory.containers {
		if container.Labels["com.docker.compose.project"] != "" || container.ComposeStack != "" ||
			container.Labels["io.just-dashboard.managed"] == "true" || container.Labels["com.just-dashboard.ingress"] == "true" ||
			container.ID == inventory.selfID || container.Name == inventory.selfID {
			continue
		}
		candidate := deploy.WorkloadCandidate{
			Key: "container:" + container.ID, Kind: "container", Name: container.Name, ResourceID: container.ID,
			State: container.State, Total: 1, ManagerURL: "/docker/containers/" + url.PathEscape(container.ID),
			Warnings: []string{"The existing container keeps its environment, ports, mounts, networks and restart policy. No replacement is created."},
			Services: []deploy.WorkloadService{{Name: container.Name, ResourceID: container.ID,
				State: container.State, Health: container.Health, Image: container.Image, Ports: importedPorts(container.Ports), CreatedAt: container.CreatedAt.UnixMilli()}},
		}
		if container.State == "running" {
			candidate.Running = 1
		}
		if container.Labels["com.docker.swarm.service.name"] != "" {
			candidate.Warnings = append(candidate.Warnings, "Docker Swarm controls this task. Its task identity can change; import monitors this task only.")
		}
		items = append(items, candidate)
	}
	pm2PIDs := map[int32]bool{}
	pm2Groups := map[string]*deploy.WorkloadCandidate{}
	for _, process := range inventory.pm2 {
		if process.PID > 0 {
			pm2PIDs[int32(process.PID)] = true
		}
		resource := url.PathEscape(process.DaemonID) + "/" + url.PathEscape(process.Namespace) + "/" + url.PathEscape(process.Name)
		candidate := pm2Groups[resource]
		if candidate == nil {
			candidate = &deploy.WorkloadCandidate{
				Key: "pm2:" + resource, Kind: "pm2", Name: process.Name, ResourceID: resource,
				SourcePath: process.ScriptPath, Services: []deploy.WorkloadService{}, ManagerURL: "/processes/pm2",
				Warnings: []string{"PM2 keeps this application's account, runtime, environment, cluster mode and startup settings. No Docker conversion is performed."},
			}
			pm2Groups[resource] = candidate
		}
		candidate.Total++
		if process.Status == "online" {
			candidate.Running++
		}
		candidate.Services = append(candidate.Services, deploy.WorkloadService{
			Name: process.Name, ResourceID: process.DaemonID + "/" + strconv.Itoa(process.ID), State: process.Status,
			PID: int32(process.PID), CreatedAt: process.CreatedAtMS, Ports: listenerPorts(inventory.listeners, int32(process.PID)),
		})
	}
	for _, candidate := range pm2Groups {
		candidate.State = workloadState(candidate.Running, candidate.Total)
		items = append(items, *candidate)
	}
	unitNames := map[string]bool{}
	for _, unit := range inventory.units {
		if unit.LoadState != "loaded" || strings.HasPrefix(unit.Name, "pm2-") || strings.HasPrefix(unit.Name, "jd-terminal-") ||
			strings.HasPrefix(unit.Name, "just-dashboard") || unit.Name == "docker.service" || unit.Name == "containerd.service" {
			continue
		}
		unitNames[unit.Name] = true
		candidate := deploy.WorkloadCandidate{
			Key: "systemd:" + unit.Name, Kind: "systemd", Name: strings.TrimSuffix(unit.Name, ".service"), ResourceID: unit.Name,
			State: unit.ActiveState, Total: 1, Services: []deploy.WorkloadService{}, ManagerURL: "/processes/services",
			Warnings: []string{"Systemd keeps the unit, drop-ins, environment files, dependencies, account and startup policy. Import never rewrites or reloads the unit."},
		}
		ports := []dockerx.PortMapping{}
		for _, listener := range inventory.listeners {
			if listener.Manager == "systemd" && listener.ManagerName == unit.Name {
				ports = appendUniquePort(ports, listenerPort(listener))
			}
		}
		if unit.ActiveState == "active" {
			candidate.Running = 1
		}
		candidate.Services = append(candidate.Services, deploy.WorkloadService{
			Name: unit.Name, ResourceID: unit.Name, State: unit.ActiveState, Ports: ports,
		})
		items = append(items, candidate)
	}
	processes := map[string]*deploy.WorkloadCandidate{}
	for _, listener := range inventory.listeners {
		if listener.PID <= 1 || listener.PID == int32(os.Getpid()) || listener.StartedAt == nil ||
			listener.Self || listener.Manager == "container" || pm2PIDs[listener.PID] ||
			listener.Manager == "systemd" && unitNames[listener.ManagerName] ||
			strings.HasPrefix(listener.ManagerName, "pm2-") || strings.HasPrefix(listener.ManagerName, "jd-terminal-") ||
			listener.Process == "docker-proxy" || listener.Process == "dockerd" || listener.Process == "containerd" {
			continue
		}
		resource := strconv.FormatInt(int64(listener.PID), 10) + "/" + strconv.FormatInt(listener.StartedAt.UnixMilli(), 10)
		candidate := processes[resource]
		if candidate == nil {
			name := listener.DisplayName
			if name == "" {
				name = listener.Process
			}
			candidate = &deploy.WorkloadCandidate{
				Key: "process:" + resource, Kind: "process", Name: name + "-" + strconv.FormatInt(int64(listener.PID), 10),
				ResourceID: resource, State: "running", Running: 1, Total: 1, ManagerURL: "/processes?pid=" + strconv.FormatInt(int64(listener.PID), 10),
				Warnings: []string{"Only this listening process is monitored. Its launch command, secrets and boot behavior cannot be safely reconstructed; a replacement PID is not silently adopted."},
				Services: []deploy.WorkloadService{{Name: name, ResourceID: resource, State: "running", PID: listener.PID,
					CreatedAt: listener.StartedAt.UnixMilli(), Ports: []dockerx.PortMapping{}}},
			}
			processes[resource] = candidate
		}
		candidate.Services[0].Ports = appendUniquePort(candidate.Services[0].Ports, listenerPort(listener))
	}
	for _, candidate := range processes {
		items = append(items, *candidate)
	}
	for index := range items {
		candidate := &items[index]
		sort.Slice(candidate.Services, func(i, j int) bool {
			return candidate.Services[i].Name+candidate.Services[i].ResourceID < candidate.Services[j].Name+candidate.Services[j].ResourceID
		})
		for index := range candidate.Services {
			sort.Slice(candidate.Services[index].Ports, func(i, j int) bool {
				left, right := candidate.Services[index].Ports[i], candidate.Services[index].Ports[j]
				if left.HostPort != right.HostPort {
					return left.HostPort < right.HostPort
				}
				if left.ContainerPort != right.ContainerPort {
					return left.ContainerPort < right.ContainerPort
				}
				return left.HostIP+left.Protocol < right.HostIP+right.Protocol
			})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if (items[i].Running > 0) != (items[j].Running > 0) {
			return items[i].Running > 0
		}
		return items[i].Kind+items[i].Name+items[i].Key < items[j].Kind+items[j].Name+items[j].Key
	})
	return items
}

func importedPorts(ports []dockerx.Port) []dockerx.PortMapping {
	result := []dockerx.PortMapping{}
	for _, port := range ports {
		result = appendUniquePort(result, dockerx.PortMapping{HostIP: port.IP, HostPort: int(port.PublicPort), ContainerPort: int(port.PrivatePort), Protocol: port.Type})
	}
	return result
}

func listenerPort(listener proxysvc.Listener) dockerx.PortMapping {
	return dockerx.PortMapping{HostIP: listener.Address, HostPort: int(listener.Port), ContainerPort: int(listener.Port), Protocol: listener.Protocol}
}

func listenerPorts(listeners []proxysvc.Listener, pid int32) []dockerx.PortMapping {
	result := []dockerx.PortMapping{}
	for _, listener := range listeners {
		if listener.PID == pid {
			result = appendUniquePort(result, listenerPort(listener))
		}
	}
	return result
}

func appendUniquePort(ports []dockerx.PortMapping, port dockerx.PortMapping) []dockerx.PortMapping {
	for _, existing := range ports {
		if existing == port {
			return ports
		}
	}
	return append(ports, port)
}

func workloadState(running, total int) string {
	if running == 0 {
		return "stopped"
	}
	if running < total {
		return "partial"
	}
	return "running"
}

func (s *Server) importedWorkload(ctx context.Context, key string) (*deploy.WorkloadCandidate, error) {
	report, err := s.discoverWorkloads(ctx)
	if err != nil {
		return nil, err
	}
	for index := range report.Items {
		if report.Items[index].Key == key {
			return &report.Items[index], nil
		}
	}
	return nil, deploy.ErrImportNotFound
}

func (s *Server) handleDeploymentWorkloadDiscovery(w http.ResponseWriter, r *http.Request) error {
	report, err := s.discoverWorkloads(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, report)
	return nil
}

func (s *Server) handleDeploymentWorkloadInspect(w http.ResponseWriter, r *http.Request) error {
	var request struct {
		Key string `json:"key"`
	}
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	if request.Key == "" || len(request.Key) > 1024 {
		return httpx.BadRequest("a discovered workload key is required")
	}
	candidate, err := s.importedWorkload(r.Context(), request.Key)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.import.inspect", candidate.ResourceID, map[string]any{"kind": candidate.Kind})
	httpx.JSON(w, http.StatusOK, candidate)
	return nil
}

func (s *Server) handleDeploymentWorkloadRegister(w http.ResponseWriter, r *http.Request) error {
	var request struct {
		Key    string `json:"key"`
		Name   string `json:"name"`
		Digest string `json:"digest"`
	}
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	if request.Key == "" || len(request.Key) > 1024 || len(request.Digest) != 64 {
		return httpx.BadRequest("an inspected workload key and digest are required")
	}
	candidate, err := s.importedWorkload(r.Context(), request.Key)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	if candidate.Digest != request.Digest {
		return httpx.Err(http.StatusConflict, "workload_changed", "The workload changed since inspection. Inspect it again before importing.")
	}
	resourceKind, sourceMode := workloadImportResource(candidate.Kind)
	metadata, err := json.Marshal(candidate)
	if err != nil {
		return httpx.Internal(err)
	}
	result, err := s.modules.deployPlanning.RegisterObservedWorkload(r.Context(), deploy.ObservedWorkloadRegistration{
		Name: request.Name, ResourceKind: resourceKind, ResourceID: candidate.ResourceID, SourceMode: sourceMode,
		Observed: metadata, OwnerUsername: httpx.MustPrincipal(r).Username(),
	})
	if errors.Is(err, deploy.ErrWorkloadAlreadyImported) {
		return httpx.Err(http.StatusConflict, "workload_already_imported", "This workload is already imported. Open its existing deployment.")
	}
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.import.register", candidate.ResourceID, map[string]any{
		"kind": candidate.Kind, "deploymentId": result.ProjectID, "environmentId": result.EnvironmentID,
	})
	status := http.StatusCreated
	if !result.Created {
		status = http.StatusOK
	}
	httpx.JSON(w, status, result)
	return nil
}

func workloadImportResource(kind string) (string, deploy.SourceMode) {
	switch kind {
	case "stack":
		return "compose_stack", deploy.SourceModeExistingStack
	case "container":
		return "docker_container", deploy.SourceModeExistingContainer
	case "pm2":
		return "pm2_process", deploy.SourceModeExistingPM2
	case "systemd":
		return "systemd_unit", deploy.SourceModeExistingSystemd
	case "process":
		return "host_process", deploy.SourceModeExistingProcess
	default:
		return "", ""
	}
}

// recoverWorkload never changes the original runtime. Capture errors are kept
// behind a safe message because manager responses can contain credentials.
func (s *Server) recoverWorkload(ctx context.Context, candidate *deploy.WorkloadCandidate) (*deploy.RecoveredWorkload, error) {
	root := filepath.Join(s.Cfg.DataDir, "deployment-recovery")
	paths := files.New(s.Cfg.DeployRoots)
	switch candidate.Kind {
	case "stack", "container":
		if s.modules.docker == nil {
			return nil, deploy.ErrSourceUnavailable
		}
		return deploy.RecoverDockerWorkload(ctx, *candidate, s.modules.docker, paths, root)
	default:
		capture, err := s.captureHostWorkload(ctx, candidate)
		if err != nil {
			return nil, err
		}
		return deploy.RecoverHostWorkload(ctx, *candidate, capture, s.modules.deploySources, paths, root)
	}
}

func (s *Server) captureHostWorkload(ctx context.Context, candidate *deploy.WorkloadCandidate) (*procs.HostWorkloadCapture, error) {
	switch candidate.Kind {
	case "pm2":
		if s.modules.pm2 == nil {
			return nil, deploy.ErrSourceUnavailable
		}
		parts := strings.Split(candidate.ResourceID, "/")
		if len(parts) != 3 {
			return nil, deploy.ErrInvalidPlan
		}
		for i := range parts {
			value, err := url.PathUnescape(parts[i])
			if err != nil {
				return nil, deploy.ErrInvalidPlan
			}
			parts[i] = value
		}
		return s.modules.pm2.CaptureExisting(ctx, parts[0], parts[1], parts[2])
	case "systemd":
		if s.modules.systemd == nil {
			return nil, deploy.ErrSourceUnavailable
		}
		return s.modules.systemd.CaptureExisting(ctx, candidate.ResourceID)
	case "process":
		if len(candidate.Services) != 1 {
			return nil, deploy.ErrInvalidPlan
		}
		service := candidate.Services[0]
		return procs.CaptureExistingProcess(ctx, service.PID, service.CreatedAt)
	default:
		return nil, deploy.ErrInvalidPlan
	}
}

func recoveryError(recovered *deploy.RecoveredWorkload, err error) error {
	if errors.Is(err, deploy.ErrRecoveryBlocked) && recovered != nil && recovered.Adoption != nil {
		return httpx.Err(http.StatusUnprocessableEntity, "recovery_blocked", strings.Join(recovered.Adoption.Blockers, "\n"))
	}
	if errors.Is(err, procs.ErrHostWorkloadChanged) {
		return httpx.Err(http.StatusConflict, "workload_changed", "The workload changed during recovery. Inspect it again.")
	}
	return httpx.Err(http.StatusUnprocessableEntity, "recovery_unavailable", "The current configuration could not be captured safely. Check access to its original manager, files and local images, then inspect it again.")
}

func (s *Server) handleDeploymentWorkloadRecover(w http.ResponseWriter, r *http.Request) error {
	var request struct {
		Key    string `json:"key"`
		Name   string `json:"name"`
		Digest string `json:"digest"`
	}
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	if request.Key == "" || len(request.Key) > 1024 || len(request.Digest) != 64 {
		return httpx.BadRequest("an inspected workload key and digest are required")
	}
	candidate, err := s.importedWorkload(r.Context(), request.Key)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	if candidate.Digest != request.Digest {
		return httpx.Err(http.StatusConflict, "workload_changed", "The workload changed since inspection. Inspect it again before recovering it.")
	}
	if candidate.ImportedProjectID != 0 {
		return httpx.Err(http.StatusConflict, "workload_already_imported", "This workload is already imported. Open its existing deployment.")
	}
	recovered, err := s.recoverWorkload(r.Context(), candidate)
	if err != nil {
		return recoveryError(recovered, err)
	}
	profile := deploy.ProfileService
	if recovered.Detection.SelectedID != "" {
		for _, item := range recovered.Detection.Candidates {
			if item.ID == recovered.Detection.SelectedID {
				profile = item.Profile
				break
			}
		}
	}
	principal := httpx.MustPrincipal(r)
	draft, err := s.modules.deployPlanning.CreateRecoveredDraft(r.Context(), principal.UserID(), principal.Username(), deploy.DraftIntentConfig{Name: request.Name, Profile: profile}, recovered)
	if errors.Is(err, deploy.ErrWorkloadAlreadyImported) {
		return httpx.Err(http.StatusConflict, "workload_already_imported", "This workload is already imported. Open its existing deployment.")
	}
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.import.recover", candidate.ResourceID, map[string]any{"kind": candidate.Kind, "draftId": draft.ID, "services": candidate.Total})
	httpx.JSON(w, http.StatusCreated, draft)
	return nil
}
