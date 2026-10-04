package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"gopkg.in/yaml.v3"
)

type dockerRecovery struct {
	result     *RecoveredWorkload
	paths      *files.Service
	containers map[string][]*dockerx.AdoptionContainer
	model      map[string]any
}

// RecoverDockerWorkload captures configuration and makes a deployable recipe;
// it never creates/stops/relabels a runtime or reads a registry. The current
// Docker image is the source for an image-only workload: source code that was
// never retained cannot be manufactured from its running process.
func RecoverDockerWorkload(ctx context.Context, candidate WorkloadCandidate, reader DockerWorkloadRecoveryReader, paths *files.Service, recoveryRoot string) (*RecoveredWorkload, error) {
	result := &RecoveredWorkload{Environment: map[string]string{}, Adoption: &WorkloadAdoption{
		Key: candidate.Key, Digest: candidate.Digest, Kind: candidate.Kind,
		ResourceID: candidate.ResourceID, Manager: "docker", Name: candidate.Name,
		Warnings: []string{}, Blockers: []string{}, Issues: []AdoptionIssue{},
		ServiceCount: candidate.Total, RunningCount: candidate.Running,
	}}
	r := &dockerRecovery{result: result, paths: paths, containers: map[string][]*dockerx.AdoptionContainer{}, model: map[string]any{}}
	if reader == nil || paths == nil || (candidate.Kind != "stack" && candidate.Kind != "container") || recoveryRoot == "" {
		return nil, fmt.Errorf("%w: Docker recovery is unavailable", ErrSourceUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	seen := map[string]bool{}
	for _, service := range candidate.Services {
		if service.ResourceID == "" || seen[service.ResourceID] {
			continue
		}
		seen[service.ResourceID] = true
		captured, err := reader.CaptureAdoptionContainer(ctx, service.ResourceID)
		if err != nil {
			r.issue("runtime_capture_unavailable", "The original container image, configuration and writable layer could not all be captured.", service.Name, "", true)
			continue
		}
		name := "app"
		if candidate.Kind == "stack" {
			name = captured.Inspection.Config.Labels["com.docker.compose.service"]
			if captured.Inspection.Config.Labels["com.docker.compose.project"] != candidate.ResourceID || name == "" || strings.EqualFold(captured.Inspection.Config.Labels["com.docker.compose.oneoff"], "true") {
				r.issue("compose_membership_changed", "A container no longer belongs to the reviewed Compose project, or is a one-off task.", service.Name, "", true)
				continue
			}
		}
		r.containers[name] = append(r.containers[name], captured)
		if captured.Inspection.State != nil && captured.Inspection.State.Paused {
			r.issue("paused_runtime", "Resume the paused container in its original manager before adoption. The deployment lifecycle cannot faithfully restore its suspended process state.", name, "state", true)
		}
		if len(captured.Inspection.Mounts) > 0 {
			r.issue("persistent_data_reused", "Existing storage is reused. Image and configuration rollback does not undo database, schema or file changes; configure and verify a backup before Deploy changes.", name, "volumes", false)
		}
		if safe, regenerable := recoverableWritableLayer(captured); !safe {
			r.issue("writable_layer_data", "The container has writable-layer changes. Move or back up that data into persistent storage before adoption; image-based redeployment would lose it.", name, "writableLayer", true)
		} else if regenerable {
			r.issue("regenerable_python_cache", "Added Python bytecode caches are regenerated from the unchanged image. They are excluded from the preserved persistent data; all other writable-layer changes still block adoption.", name, "writableLayer", false)
		}
		if captured.Inspection.Config.Labels["com.docker.swarm.service.id"] != "" {
			r.issue("swarm_owner", "This container is owned by a Swarm service. Adopt its service specification through Swarm rather than replacing this task.", name, "", true)
		}
	}
	if len(r.containers) == 0 {
		r.issue("runtime_missing", "No existing container could be captured for this workload.", "", "", true)
	}
	if candidate.Kind == "stack" && len(r.containers) > 0 {
		r.readOriginalCompose(ctx, candidate, reader)
	}
	if candidate.Kind == "container" {
		r.model = map[string]any{"services": map[string]any{"app": map[string]any{}}}
		r.issue("container_name_changes", "The first Deploy changes action creates a new managed container name. The original container remains available for recovery until the replacement passes its checks; original network aliases are retained.", "app", "containerName", false)
	}
	services := object(r.model["services"])
	if services == nil {
		services = map[string]any{}
		r.model["services"] = services
	}
	names := sortedObjectKeys(services)
	for name := range r.containers {
		if _, ok := services[name]; !ok {
			services[name] = map[string]any{}
			names = append(names, name)
		}
	}
	sort.Strings(names)
	resolved := &ResolvedComposeSnapshot{Files: []string{"compose.yml"}, Services: []ResolvedComposeService{}}
	for _, name := range names {
		service := object(services[name])
		if service == nil {
			r.issue("compose_service_invalid", "The original Compose service is not a mapping.", name, "", true)
			continue
		}
		captures := r.containers[name]
		var image *dockerx.ImageDetail
		if len(captures) > 0 {
			image = captures[0].Image
			for _, other := range captures[1:] {
				if containerSettingsDigest(other) != containerSettingsDigest(captures[0]) {
					r.issue("replica_configuration_differs", "Replicas of this service have different images or runtime settings. Reconcile them before adoption.", name, "", true)
				}
			}
			r.recoverService(name, service, captures[0], candidate.Kind == "stack")
			if len(captures) > 1 {
				if len(captures[0].Inspection.HostConfig.PortBindings) > 0 {
					r.issue("replica_published_ports", "Published ports differ by replica and cannot be reconstructed as one fixed Compose service binding.", name, "ports", true)
				}
				service["scale"] = len(captures)
				delete(service, "container_name")
			}
		} else {
			ref, _ := service["image"].(string)
			var err error
			image, err = reader.InspectImage(ctx, ref)
			if ref == "" || err != nil || image == nil {
				r.issue("inactive_service_image_missing", "A service without a container has no locally available image. Supply its source or image before adoption.", name, "image", true)
				continue
			}
			r.issue("inactive_services", "This service has no current container. Adoption leaves it inactive; an explicit Deploy will create services from the reviewed recipe.", name, "", false)
			r.captureEnvironment(name, service)
		}
		if image == nil || !contentDigestRE.MatchString(image.ID) {
			r.issue("image_identity_missing", "The original service has no immutable local image identity.", name, "image", true)
			continue
		}
		if _, exists := service["build"]; exists {
			r.issue("build_source_retained", "The running image is preserved as the initial redeploy source. Its original Compose build definition is retained in the original project; attach its source checkout for future builds.", name, "build", false)
		}
		delete(service, "build")
		delete(service, "env_file")
		service["image"], service["pull_policy"] = image.ID, "never"
		resolved.Services = append(resolved.Services, ResolvedComposeService{
			Plan: ComposeServicePlan{Name: name, Image: image.ID}, Reference: image.ID,
			Digest: image.ID, ConfigDigest: image.ID, Source: "adopted_local_image",
		})
	}
	r.externalizeResources()
	delete(r.model, "name")
	delete(r.model, "include")
	r.sanitizeStrings(r.model, "", "")
	if len(r.result.Adoption.Issues) > 0 {
		sort.Slice(r.result.Adoption.Issues, func(i, j int) bool {
			return string(mustJSON(r.result.Adoption.Issues[i])) < string(mustJSON(r.result.Adoption.Issues[j]))
		})
	}
	sort.Strings(r.result.Adoption.Blockers)
	sort.Strings(r.result.Adoption.Warnings)
	content, err := yaml.Marshal(r.model)
	if err != nil {
		return nil, fmt.Errorf("%w: recovered Compose could not be rendered", ErrInvalidCompose)
	}
	result.Source = DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: string(content)}}}
	result.Configuration = PlanConfiguration{Build: BuildPlanConfig{Method: BuildCompose}, Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst}, Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{}, Checks: []PlannedCheck{}, Domains: []PlannedDomain{}}
	if candidate.Kind == "stack" {
		result.Configuration.Runtime.ComposeProjectName = candidate.ResourceID
	}
	for _, name := range sortedStringMapKeys(result.Environment) {
		result.Configuration.Variables = append(result.Configuration.Variables, PlannedVariable{Name: name, Sensitivity: "secret", Scopes: []string{"runtime"}, Required: result.Environment[name] != ""})
	}
	analysis, analyzeErr := analyzeComposeDocuments(result.Source.ComposeFiles)
	if analyzeErr != nil {
		r.issue("recovered_source_unsupported", "The recovered configuration contains options or credential arguments that the deployment parser cannot safely manage. Resolve those settings in the original manager before adoption.", "", "", true)
	} else {
		for _, reason := range analysis.Unsupported {
			r.issue("compose_option_unsupported", reason, "", "", true)
		}
		identity := SourceIdentity{Kind: SourceCompose, Digest: analysis.Digest, ComposeFiles: analysis.Files, Services: sortedObjectKeys(services)}
		detected := newDetectedCandidate("", BuildCompose, DetectedCandidate{Name: "Recovered " + candidate.Name, Profile: ProfileCompose, Confidence: ConfidenceHigh, Evidence: []DetectionEvidence{{Path: "compose.yml", Reason: "captured existing Docker runtime configuration"}}})
		result.Detection = DetectionResult{Source: identity, Compose: &analysis, Candidates: []DetectedCandidate{detected}, SelectedID: detected.ID}
		resolved.SourceDigest, resolved.PrimaryService = analysis.Digest, analysis.PrimaryService
		result.Configuration.Build.PrimaryService = analysis.PrimaryService
		for i := range resolved.Services {
			for _, plan := range analysis.Services {
				if plan.Name == resolved.Services[i].Plan.Name {
					resolved.Services[i].Plan = plan
				}
			}
		}
	}
	r.captureBaseline(candidate, resolved)
	result.Configuration = canonicalConfiguration(result.Configuration)
	r.result.Adoption.BaselineSource = result.Source
	r.result.Adoption.BaselineConfiguration = result.Configuration
	r.result.Adoption.BaselineConfiguration.Build.PrimaryService = resolved.PrimaryService
	r.result.Adoption.BaselineDigest = recoveredDockerBaselineDigest(result)
	var baseline dockerReleaseRuntimeMetadata
	_ = json.Unmarshal(result.Adoption.Runtime.Metadata, &baseline)
	r.result.Adoption.Snapshot = mustJSON(runtimeReleaseSnapshot{Version: 1, Plan: result.Configuration.Runtime, Compose: resolved, ComposeBaseline: baseline.BaselineContainers, SourceIdentity: result.Detection.Source, Variables: []ReleaseVariableSnapshot{}, Dependencies: []PlannedDependency{}, Checks: []PlannedCheck{}, Domains: []PlannedDomain{}})
	if len(result.Adoption.Blockers) > 0 {
		return result, ErrRecoveryBlocked
	}
	if err := result.Source.ValidateForDeployment(); err != nil {
		return result, fmt.Errorf("%w: recovered source is not deployable", ErrRecoveryBlocked)
	}
	if err := result.Configuration.Validate(); err != nil {
		return result, fmt.Errorf("%w: recovered runtime plan is not deployable", ErrRecoveryBlocked)
	}
	if err := validateDetectionResult(&result.Source, result.Detection); err != nil {
		return result, fmt.Errorf("%w: recovered source evidence is invalid", ErrRecoveryBlocked)
	}
	if err := r.stageBaseline(recoveryRoot, content); err != nil {
		return nil, err
	}
	return result, nil
}

func RecoveredWorkloadDigest(source DraftSourceConfig, configuration PlanConfiguration, environment map[string]string) string {
	values := map[string]string{}
	for name, value := range environment {
		values[name] = digestBytes([]byte(value))
	}
	return digestBytes(mustJSON(source), mustJSON(configuration), mustJSON(values))
}

func recoveredDockerBaselineDigest(result *RecoveredWorkload) string {
	var metadata dockerReleaseRuntimeMetadata
	_ = json.Unmarshal(result.Adoption.Runtime.Metadata, &metadata)
	return digestBytes([]byte(RecoveredWorkloadDigest(result.Source, result.Configuration, result.Environment)), mustJSON(struct {
		Kind       string             `json:"kind"`
		RuntimeID  string             `json:"runtimeId"`
		Containers []AdoptedContainer `json:"containers"`
	}{result.Adoption.Runtime.Kind, result.Adoption.Runtime.RuntimeID, metadata.BaselineContainers}))
}

func (r *dockerRecovery) issue(code, message, service, field string, blocking bool) {
	for _, issue := range r.result.Adoption.Issues {
		if issue.Code == code && issue.Service == service && issue.Field == field {
			return
		}
	}
	r.result.Adoption.Issues = append(r.result.Adoption.Issues, AdoptionIssue{Code: code, Message: message, Service: service, Field: field, Blocking: blocking})
	if service != "" {
		message = service + ": " + message
	}
	if blocking {
		r.result.Adoption.Blockers = append(r.result.Adoption.Blockers, message)
	} else {
		r.result.Adoption.Warnings = append(r.result.Adoption.Warnings, message)
	}
}

func engineGeneratedFile(path string) bool {
	return path == "/etc/hosts" || path == "/etc/hostname" || path == "/etc/resolv.conf" || path == "/dev" || strings.HasPrefix(path, "/dev/")
}

func (r *dockerRecovery) stageBaseline(root string, content []byte) error {
	if err := makePrivateDirectory(root); err != nil {
		return err
	}
	directory := filepath.Join(root, strings.TrimPrefix(r.result.Adoption.BaselineDigest, "sha256:"))
	if err := makePrivateDirectory(directory); err != nil {
		return err
	}
	if err := writeImmutableRuntimeFile(directory, "compose.yml", string(content)); err != nil {
		return err
	}
	if err := writeImmutableRuntimeFile(directory, "release.yml", "services: {}\n"); err != nil {
		return err
	}
	r.result.Adoption.RecoveryDirectory = directory
	r.result.Adoption.Runtime.WorkingDirectory = directory
	if r.result.Adoption.Runtime.Kind == "compose" {
		var metadata dockerReleaseRuntimeMetadata
		_ = json.Unmarshal(r.result.Adoption.Runtime.Metadata, &metadata)
		metadata.ProjectDirectory, metadata.ComposeFiles, metadata.OverrideFile = directory, []string{"compose.yml"}, filepath.Join(directory, "release.yml")
		r.result.Adoption.Runtime.Metadata = mustJSON(metadata)
	}
	return nil
}

func (r *dockerRecovery) captureBaseline(candidate WorkloadCandidate, resolved *ResolvedComposeSnapshot) {
	ids := []string{}
	baseline := []AdoptedContainer{}
	primary, primaryService := "", ""
	primaryScore := -1
	for _, name := range sortedRecoveryServices(r.containers) {
		for _, capture := range r.containers[name] {
			ids = append(ids, capture.Inspection.ID)
			number, _ := strconv.Atoi(capture.Inspection.Config.Labels["com.docker.compose.container-number"])
			if number < 1 {
				number = 1
			}
			running := capture.Inspection.State != nil && capture.Inspection.State.Running
			baseline = append(baseline, AdoptedContainer{ID: capture.Inspection.ID, Service: name, Number: number, Running: running, StopTimeout: capture.Inspection.Config.StopTimeout})
			score := 0
			if running {
				score += 8
			}
			if name == resolved.PrimaryService {
				score++
			}
			if len(capture.Inspection.HostConfig.PortBindings) > 0 {
				score += 4
			}
			if score > primaryScore {
				primary, primaryService, primaryScore = capture.Inspection.ID, name, score
			}
		}
	}
	// A declared service may not have a container yet. The immutable live
	// baseline needs a real primary for restoration, logs and checks.
	resolved.PrimaryService = primaryService
	sort.Slice(baseline, func(i, j int) bool {
		if baseline[i].Service != baseline[j].Service {
			return baseline[i].Service < baseline[j].Service
		}
		return baseline[i].Number < baseline[j].Number
	})
	sort.Strings(ids)
	metadata := dockerReleaseRuntimeMetadata{Version: 1, Adopted: true, SharedProject: candidate.Kind == "stack", BaselineContainers: baseline, Strategy: string(StrategyStopFirst), ContainerIDs: ids, PrimaryContainerID: primary, VariableNames: sortedStringMapKeys(r.result.Environment)}
	kind, id, name := "compose", candidate.ResourceID, candidate.Name
	if candidate.Kind == "container" {
		kind, id = "container", primary
	} else {
		metadata.ProjectName = candidate.ResourceID
		if err := configureComposeBaseline(&dockerx.ComposeReleaseSpec{}, baseline); err != nil {
			r.issue("replica_identity_unreproducible", "The original Compose replica numbers are duplicated or contain gaps and cannot be recreated safely.", "", "replicas", true)
		}
	}
	r.result.Adoption.Runtime = ReleaseRuntimeInput{Kind: kind, RuntimeID: id, Name: name, Metadata: mustJSON(metadata)}
	for _, service := range candidate.Services {
		if service.ResourceID != primary {
			continue
		}
		for _, port := range service.Ports {
			if port.HostPort > 0 && port.ContainerPort > 0 && (port.Protocol == "tcp" || port.Protocol == "") {
				r.result.Adoption.Runtime.Host, r.result.Adoption.Runtime.Port = port.HostIP, port.HostPort
				r.result.Configuration.Runtime.InternalPort = port.ContainerPort
				return
			}
		}
	}
}

func object(value any) map[string]any { result, _ := value.(map[string]any); return result }

func sortedObjectKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringMapKeys(value map[string]string) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedRecoveryServices(value map[string][]*dockerx.AdoptionContainer) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func recoveryVariableName(service, key string) string {
	// A digest avoids collisions after punctuation/case normalization and
	// keeps service-local variables distinct when two services use PASSWORD.
	hash := strings.TrimPrefix(digestBytes([]byte(service), []byte(key)), "sha256:")[:12]
	var readable strings.Builder
	for _, value := range strings.ToUpper(key) {
		if value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_' {
			readable.WriteRune(value)
		} else {
			readable.WriteByte('_')
		}
		if readable.Len() >= 70 {
			break
		}
	}
	return "JD_IMPORT_" + readable.String() + "_" + strings.ToUpper(hash)
}

func (r *dockerRecovery) privateValue(service, key, value string) string {
	name := recoveryVariableName(service, key)
	r.result.Environment[name] = value
	return "${" + name + "}"
}

func (r *dockerRecovery) captureEnvironment(name string, service map[string]any) {
	environment := object(service["environment"])
	if environment == nil {
		return
	}
	for key, raw := range environment {
		value, ok := raw.(string)
		if !ok {
			value = fmt.Sprint(raw)
			if raw == nil {
				value = ""
			}
		}
		environment[key] = r.privateValue(name, "env_"+key, value)
	}
}

func (r *dockerRecovery) sanitizeStrings(value any, service, key string) {
	switch typed := value.(type) {
	case map[string]any:
		for field, child := range typed {
			if text, ok := child.(string); ok && text != "" && !isVariableExpression(text) {
				if secretShapedKey(field) || containsURLCredentials(text) || strings.Contains(strings.ToLower(text), "-----begin private key-----") {
					typed[field] = r.privateValue(service, key+"_"+field, text)
				}
			} else {
				r.sanitizeStrings(child, service, key+"_"+field)
			}
		}
	case []any:
		command := strings.Contains(key, "command") || strings.Contains(key, "entrypoint") || strings.Contains(key, "healthcheck")
		arguments := make([]string, len(typed))
		for index, child := range typed {
			arguments[index], _ = child.(string)
		}
		for index, child := range typed {
			if text, ok := child.(string); ok {
				unsafe := containsURLCredentials(text) || strings.Contains(strings.ToLower(text), "-----begin private key-----")
				if command && (commandArgumentContainsSecret(arguments, index) || secretCommandFlagRE.MatchString(text)) {
					unsafe = true
					r.issue("credential_arguments", "Credential-bearing command arguments must be moved to scoped environment variables before adoption.", service, "command", true)
				}
				if unsafe {
					typed[index] = r.privateValue(service, key+"_"+strconv.Itoa(index), text)
				}
			} else {
				r.sanitizeStrings(child, service, key)
			}
		}
	case []string:
		command := strings.Contains(key, "command") || strings.Contains(key, "entrypoint") || strings.Contains(key, "healthcheck")
		original := append([]string(nil), typed...)
		for index, text := range typed {
			unsafe := containsURLCredentials(text) || strings.Contains(strings.ToLower(text), "-----begin private key-----")
			if command && (commandArgumentContainsSecret(original, index) || secretCommandFlagRE.MatchString(text)) {
				unsafe = true
				r.issue("credential_arguments", "Credential-bearing command arguments must be moved to scoped environment variables before adoption.", service, "command", true)
			}
			if unsafe {
				typed[index] = r.privateValue(service, key+"_"+strconv.Itoa(index), text)
			}
		}
	case map[string]string:
		for field, text := range typed {
			if text != "" {
				typed[field] = r.privateValue(service, key+"_"+field, text)
			}
		}
	}
}

func containerSettingsDigest(capture *dockerx.AdoptionContainer) string {
	config := *capture.Inspection.Config
	config.Hostname = ""
	config.Labels = map[string]string{}
	for key, value := range capture.Inspection.Config.Labels {
		if !strings.HasPrefix(key, "com.docker.compose.") {
			config.Labels[key] = value
		}
	}
	host := *capture.Inspection.HostConfig
	// Runtime-allocated ports and replica IDs are checked separately by the
	// discovery fence; the settings fingerprint follows the service recipe.
	host.PortBindings = nil
	return digestBytes(mustJSON(config), mustJSON(host), []byte(capture.Inspection.Image))
}

func containsSourceInterpolation(value any) bool {
	encoded, _ := json.Marshal(value)
	return strings.Contains(string(encoded), "${")
}
