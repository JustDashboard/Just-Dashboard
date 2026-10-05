package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
)

func RecoverHostWorkload(ctx context.Context, candidate WorkloadCandidate, capture *procs.HostWorkloadCapture, analyzer *HostSourceAnalyzer, paths *files.Service, cacheRoot string) (*RecoveredWorkload, error) {
	if capture == nil || analyzer == nil || paths == nil || (capture.Manager != "pm2" && capture.Manager != "systemd" && capture.Manager != "process") {
		return nil, ErrRecoveryBlocked
	}
	localCapture := *capture
	localCapture.Blockers = append([]string(nil), capture.Blockers...)
	capture = &localCapture
	if capture.StartupPlan != nil && analyzer.startup != nil {
		if err := analyzer.startup.Stage(ctx, capture.StartupPlan); err == nil {
			if plan, err := analyzer.startup.VerifyCapture(ctx, capture.StartupPlan.Digest, capture); err == nil {
				removeHandledStartupBlockers(capture, plan)
			}
		}
	}
	origin := &WorkloadAdoption{Key: candidate.Key, Digest: candidate.Digest, Kind: candidate.Kind, ResourceID: candidate.ResourceID, Manager: capture.Manager, Name: candidate.Name,
		ServiceCount: candidate.Total, RunningCount: candidate.Running, Warnings: append([]string{}, capture.Warnings...), Blockers: append([]string{}, capture.Blockers...), Issues: []AdoptionIssue{},
		OriginalSourcePath: capture.SourceDirectory, BaselineDigest: capture.ConfigurationDigest}
	recovered := &RecoveredWorkload{Adoption: origin, Environment: map[string]string{}}
	recovered.BaselineEnvironment = make(map[string]string, len(capture.Environment))
	for name, value := range capture.Environment {
		recovered.BaselineEnvironment[name] = value
	}
	issues := map[string]bool{}
	block := func(code, message, field string) {
		key := code + "\x00" + field
		if issues[key] {
			return
		}
		issues[key] = true
		origin.Blockers = append(origin.Blockers, message)
		origin.Issues = append(origin.Issues, AdoptionIssue{Code: code, Message: message, Field: field, Blocking: true})
	}
	root, err := paths.Resolve(capture.SourceDirectory)
	if err != nil || capture.SourceDirectory == "" {
		block("host_source_unavailable", "The original working directory is outside deployment roots or unavailable. Choose an accessible source before managed migration.", "source.localPath")
		return recovered, nil
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		block("host_source_unavailable", "The original working directory is unavailable. Recover its source before managed migration.", "source.localPath")
		return recovered, nil
	}
	if !hostSourceDirectoryMatches(info, hostexec.HostPath(root)) {
		block("host_source_mount_mismatch", "The working directory visible to the dashboard does not match the original host directory. Mount this deployment root at its matching host path before recovering the application.", "source.localPath")
		return recovered, nil
	}
	recovered.Source = DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalDirectory, LocalPath: root}
	for _, directory := range []string{"node_modules", ".venv", "venv", "__pycache__"} {
		if info, err := os.Stat(filepath.Join(root, directory)); err == nil && info.IsDir() {
			recovered.Source.ExcludePaths = append(recovered.Source.ExcludePaths, directory)
		}
	}
	for _, directory := range []string{"data", "uploads", "storage"} {
		if info, err := os.Stat(filepath.Join(root, directory)); err == nil && info.IsDir() {
			recovered.Source.ExcludePaths = append(recovered.Source.ExcludePaths, directory)
		}
	}
	detection, err := analyzer.Analyze(ctx, recovered.Source)
	if err != nil {
		block("host_source_snapshot_unavailable", "The source could not be safely snapshotted. It may contain unsupported special files, external symlinks, or exceed the source limits.", "source.localPath")
		return recovered, nil
	}
	bindHostRecoveryCandidate(&detection, capture, root)
	recovered.Detection = detection
	drift, driftErr := knownHostSourceDrift(ctx, root, capture, recovered.Source.ExcludePaths)
	if driftErr != nil {
		block("host_running_source_unverified", "The current source cannot be verified against a running process. Review its current startup source under the original manager before migration.", "source.localPath")
	} else if drift {
		block("host_running_source_changed", "Runtime source files changed after this application started. Its loaded code can differ from the captured files. Restore and review the actual running source, or verify the reviewed source by restarting under its original manager before recovery.", "source.localPath")
	}
	origin.Warnings = append(origin.Warnings, "A filesystem snapshot cannot prove every module loaded in memory matches the current files, or recover dynamic in-memory settings. Confirm this is the source and startup configuration the original manager should restore; preserved/backdated timestamps require that same review.")
	origin.Warnings = append(origin.Warnings, "Review inherited host process and file-descriptor limits, umask, scheduling, capabilities and security defaults. Capture does not universally reconstruct these policies in Docker; application-relevant settings require a reviewed Dockerfile and runtime policy before cutover.")
	origin.Warnings = append(origin.Warnings, "Review every external startup authority before cutover, including unloaded systemd timers, paths, sockets and targets, cron and custom scripts. Captured manager state and saved PM2 lists cannot prove no other launcher exists. Keep a reversible handoff plan so the original app cannot restart beside Docker after reboot.")
	recovered.Configuration = configurationFromDetection(detection)
	recovered.Configuration.Build.Secrets = []BuildSecretConfig{}
	recovered.Configuration.Build.ReleaseTasks = []ReleaseTaskConfig{}
	recovered.Configuration.Runtime.Strategy = StrategyStopFirst
	recovered.Configuration.Runtime.User = strconv.FormatUint(uint64(capture.UID), 10) + ":" + strconv.FormatUint(uint64(capture.GID), 10)
	recovered.Configuration.Runtime.WorkingDirectory = "/app"
	// Keeping the host network preserves existing addresses and localhost
	// dependencies. It explicitly trades preview isolation for runtime parity;
	// a later reviewed networking change can move the app behind managed ingress.
	recovered.Configuration.Runtime.HostNetwork = true
	recovered.Configuration.Runtime.RestartPolicy = "unless-stopped"
	if capture.RestartPolicy != "" {
		recovered.Configuration.Runtime.RestartPolicy = capture.RestartPolicy
	}
	recovered.Configuration.Runtime.StopSignal = capture.StopSignal
	recovered.Configuration.Runtime.GracePeriodSeconds = capture.GracePeriodSeconds
	recovered.Configuration.Variables = []PlannedVariable{}
	selected := selectedHostRecoveryCandidate(detection)
	if selected != nil && selected.BuildMethod == BuildRecipe {
		recovered.Configuration.Build.PreserveSourceRoot = true
	}
	for name, value := range capture.Environment {
		if name == "NODE_CHANNEL_FD" || name == "NODE_CHANNEL_SERIALIZATION_MODE" {
			origin.Warnings = append(origin.Warnings, "The original Node IPC channel belongs to its manager and is not transferred into the container. Applications that use process.send or manager messages require an explicit process-manager migration plan.")
			continue
		}
		if name == "VIRTUAL_ENV" && selected != nil && selected.Recipe == "python" {
			origin.Warnings = append(origin.Warnings, "The container installs the captured Python dependencies into its own interpreter; the original virtual environment remains part of the native baseline.")
			continue
		}
		if ValidateEnvKey(name) != nil {
			block("host_variable_unsupported", "The original environment contains a variable name the managed deployment cannot represent.", "variables")
			continue
		}
		used := false
		if selected != nil {
			for _, variable := range selected.Variables {
				used = used || variable.Name == name
			}
		}
		translated, changed, supported := translateHostRuntimeEnvironment(name, value, root)
		if name == "NODE_OPTIONS" && !supported {
			block("host_node_options_unsupported", "NODE_OPTIONS contains a loader, filesystem operand or option outside the reviewed migration set. Review its container-specific interpreter configuration before migration.", "runtime.command")
		} else if !supported && (used || !hostManagerEnvironmentPath(name)) {
			block("host_environment_path_unsupported", "The application variable "+name+" refers to a host filesystem path outside its captured source. Configure a verified retained mount or a supported container path before migration.", "runtime.mounts")
		} else if !supported {
			origin.Warnings = append(origin.Warnings, "The standard manager environment variable "+name+" retains an original host path. Confirm the application does not depend on it as a filesystem resource; host-only manager paths are not mounted into the container.")
		}
		if changed {
			value = translated
			origin.Warnings = append(origin.Warnings, "The application variable "+name+" uses the source's /app container layout. Its original value is retained separately for the native baseline.")
		}
		if name == "PATH" && selected != nil && (selected.Recipe == "node" || selected.Recipe == "python") && !strings.Contains(":"+value+":", ":/usr/local/bin:") {
			value = "/usr/local/bin:" + value
		}
		recovered.Environment[name] = value
		AddRecoveredInput(recovered, name, name, candidate.Name, "environment", "native", "application")
		scopes := []string{"runtime"}
		buildStep := ""
		if selected != nil {
			for _, variable := range selected.Variables {
				if variable.Name == name && (variable.Phase == "build" || variable.BrowserInlined || variable.Step == "install") {
					buildStep = "build"
					if variable.Step == "install" {
						buildStep = "install"
					}
					break
				}
			}
			for _, prefix := range selected.BrowserPrefixes {
				if strings.HasPrefix(name, prefix) {
					if buildStep == "install" {
						buildStep = "install_and_build"
					} else {
						buildStep = "build"
					}
				}
			}
		}
		if buildStep != "" {
			scopes = append(scopes, "build")
			recovered.Configuration.Build.Secrets = append(recovered.Configuration.Build.Secrets, BuildSecretConfig{Variable: name, Step: buildStep})
		}
		recovered.Configuration.Variables = append(recovered.Configuration.Variables, PlannedVariable{Name: name, Sensitivity: "secret", Scopes: scopes, ValueMode: "literal"})
	}
	if selected == nil {
		block("host_build_unknown", "No supported build was found in this application's source. Add a reviewed Dockerfile or select the correct source directory.", "build.method")
	} else {
		recovered.Configuration.Build.Recipe = selected.Recipe
		recovered.Configuration.Build.Framework = selected.Framework
		switch selected.BuildMethod {
		case BuildDockerfile:
			verifyHostDockerfileLayout(root, recovered.Configuration.Build, block)
			origin.Warnings = append(origin.Warnings, "The Dockerfile must provide the original application's operating-system and interpreter dependencies. Review its entrypoint and user permissions before cutover.")
		case BuildRecipe:
			origin.Warnings = append(origin.Warnings, "The managed recipe uses the captured interpreter's supported version with the catalogue image's patch version and container operating system. Confirm the application works without host-installed packages or native host dependencies before deploying changes; provide a Dockerfile when it needs them.")
			origin.Warnings = append(origin.Warnings, "The recipe installs dependencies from the reviewed manifests and lockfiles. It does not reproduce patched dependency directories or undeclared global dependencies from the host. Verify these inputs match the running application, or provide a reviewed Dockerfile before cutover.")
			if selected.Profile == ProfileStatic || strings.TrimSpace(recovered.Configuration.Build.OutputDirectory) != "" {
				block("host_runtime_layout_unsupported", "The detected recipe creates a static serving image whose files and command differ from this running Node process. Provide a reviewed Dockerfile preserving the original source, interpreter and command before migration.", "build.method")
			}
			switch selected.Recipe {
			case "node":
				version := strings.Split(strings.TrimPrefix(capture.InterpreterVersion, "v"), ".")[0]
				if version == "20" || version == "22" || version == "24" {
					recovered.Configuration.Build.NodeVersion = version
				} else {
					block("host_interpreter_version_unknown", "The original Node interpreter version is unavailable or outside the managed recipe catalogue. Select a verified runtime version or provide a Dockerfile.", "build.nodeVersion")
				}
			case "python":
				version := strings.Split(strings.TrimPrefix(capture.InterpreterVersion, "Python "), ".")
				if len(version) >= 2 && pythonRecipeVersionRE.MatchString(strings.Join(version[:2], ".")) {
					recovered.Configuration.Build.PythonVersion = strings.Join(version[:2], ".")
				} else {
					block("host_interpreter_version_unknown", "The captured Python interpreter version is unavailable or outside the supported recipe catalogue. Provide a reviewed Dockerfile.", "build.pythonVersion")
				}
				if !supportedNativePythonCommand(capture.Command) {
					block("host_python_command_unsupported", "Automatic Python recovery supports a verified interpreter running a source file or module; wrappers, inline programs and external launchers need a reviewed Dockerfile.", "runtime.command")
				}
			default:
				block("host_runtime_compatibility_unknown", "Automatic host recovery requires a verified Node or Python interpreter, or an existing Dockerfile. Other native runtimes need a reviewed Dockerfile.", "build.method")
			}
			if selected.Recipe == "node" || selected.Recipe == "python" {
				buildRoot := filepath.Join(root, filepath.FromSlash(recovered.Configuration.Build.RootDirectory))
				if _, err := selectRecipe(root, buildRoot, recovered.Configuration.Build); err != nil {
					block("host_recipe_inputs_unsupported", "The captured manifest, interpreter and full source layout cannot form a supported recipe. Provide a reviewed Dockerfile before migration.", "build.method")
				}
			}
		default:
			block("host_build_compatibility_unknown", "The detected build does not preserve the current host command. Choose a reviewed container build before migration.", "build.method")
		}
	}
	command, commandErr := hostCommandForContainer(capture.Command, root, recovered.Configuration.Build.Recipe)
	if commandErr != nil {
		block("host_command_unsupported", "The original command cannot be transferred with its argument boundaries and interpreter paths intact. Review a container command before migration.", "runtime.command")
	} else {
		args := []string{"exec"}
		prefix := "JD_IMPORTED_ARG_"
		for _, name := range capture.EnvironmentNames {
			if strings.HasPrefix(name, prefix) {
				prefix = "JD_IMPORTED_" + capture.ConfigurationDigest[:min(12, len(capture.ConfigurationDigest))] + "_ARG_"
				break
			}
		}
		for index, arg := range command {
			if commandArgumentContainsSecret(capture.Command, index) {
				block("host_argument_credential", "The original command passes credentials through process arguments. Move those credentials into runtime variables before managed migration.", "runtime.command")
			}
			name := prefix + strconv.Itoa(index)
			recovered.Environment[name] = arg
			AddRecoveredInput(recovered, name, "Argument "+strconv.Itoa(index), candidate.Name, "argument", "native", "runtime_setting")
			recovered.Configuration.Variables = append(recovered.Configuration.Variables, PlannedVariable{Name: name, Sensitivity: "secret", Scopes: []string{"runtime"}, ValueMode: "literal"})
			args = append(args, `"$`+name+`"`)
		}
		recovered.Configuration.Runtime.Command = []string{"/bin/sh", "-c", strings.Join(args, " ")}
	}
	port, host := hostRecoveryAddress(candidate)
	recovered.Configuration.Runtime.InternalPort = port
	recovered.Configuration.Runtime.HostPort = port
	recovered.Configuration.Runtime.BindAddress = host
	if port > 0 {
		recovered.Configuration.Checks = []PlannedCheck{{Name: "Original listening port", Kind: "tcp", Phase: "readiness", Required: true, Config: json.RawMessage(`{"attempts":30,"timeoutSeconds":2,"intervalSeconds":1}`)}}
	}
	origin.Warnings = append(origin.Warnings, "The first replacement uses the existing host network and listening port. It activates only after the build succeeds and uses stop-first cutover; the original manager remains available for compensation and rollback.")
	if err := inspectHostRecoveryFiles(ctx, root, capture, recovered, block); err != nil {
		return nil, err
	}
	mode := map[string]SourceMode{"pm2": SourceModeExistingPM2, "systemd": SourceModeExistingSystemd, "process": SourceModeExistingProcess}[capture.Manager]
	origin.BaselineSource = DraftSourceConfig{Kind: SourceImport, Mode: mode, ResourceID: candidate.ResourceID}
	baselineCandidate := newDetectedCandidate("", BuildNone, DetectedCandidate{Name: candidate.Name, Profile: ProfileImported, Confidence: ConfidenceHigh, Evidence: []DetectionEvidence{{Path: candidate.ResourceID, Reason: "verified original native restart manager"}}})
	origin.BaselineDetection = DetectionResult{Source: SourceIdentity{Kind: SourceImport, Repository: candidate.Name}, Candidates: []DetectedCandidate{baselineCandidate}, SelectedID: baselineCandidate.ID}
	baselineVariables := make([]PlannedVariable, 0, len(capture.Environment))
	for _, name := range sortedStringMapKeys(capture.Environment) {
		baselineVariables = append(baselineVariables, PlannedVariable{Name: name, Sensitivity: "secret", Scopes: []string{"runtime"}, ValueMode: "literal"})
	}
	origin.BaselineConfiguration = PlanConfiguration{Build: BuildPlanConfig{Method: BuildNone, Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}}, Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst, HostPort: port, InternalPort: port, BindAddress: host}, Variables: baselineVariables, Checks: recovered.Configuration.Checks, Dependencies: []PlannedDependency{}, Domains: []PlannedDomain{}}
	if capture.Manager != "process" {
		origin.Runtime, err = NativeBaselineRuntimeInput(capture, 0)
		if err != nil {
			block("host_restart_authority_unavailable", "The original manager identity cannot be retained as a verified rollback baseline.", "source")
		} else {
			var metadata NativeBaselineMetadata
			if json.Unmarshal(origin.Runtime.Metadata, &metadata) != nil {
				return nil, ErrInvalidPlan
			}
			metadata.SourceRoot = root
			metadata.SourceExclusions = append([]string{}, recovered.Source.ExcludePaths...)
			metadata.SourceDigest, err = nativeDirectoryDigest(ctx, root, metadata.SourceExclusions)
			if err != nil {
				block("host_native_source_fence_unavailable", "The original source could not be fenced safely for native restart authority. Review its private files, permissions and source limits before migration.", "source.localPath")
			}
			metadata.SourcePrivateFence = true
			if capture.StartupPlan != nil && analyzer.startup != nil {
				if _, verifyErr := analyzer.startup.VerifyCapture(ctx, capture.StartupPlan.Digest, capture); verifyErr == nil {
					metadata.StartupPlanDigest = capture.StartupPlan.Digest
					metadata.RuntimeConfigurationDigest = capture.RuntimeConfigurationDigest
					summary := capture.StartupPlan.Summary()
					origin.StartupHandoff = &summary
				} else {
					block("host_startup_handoff_changed", "The prepared startup authority changed during recovery. Refresh detection before adoption.", "source")
				}
			}
			origin.Runtime.Metadata = mustJSON(metadata)
			origin.Runtime.Host, origin.Runtime.Port = host, port
			origin.Warnings = append(origin.Warnings, "The original source directory is frozen as native rollback evidence. Keep it unchanged while this manager is a live or retained baseline; use a separate managed checkout for new code. Changed modules block native stop, compensation and rollback until the captured source is restored. Linked data remains writable.")
			origin.Snapshot = mustJSON(runtimeReleaseSnapshot{Version: 1, Plan: origin.BaselineConfiguration.Runtime, NativeBaseline: &origin.Runtime, Checks: origin.BaselineConfiguration.Checks, Dependencies: []PlannedDependency{}, Domains: []PlannedDomain{}, Variables: []ReleaseVariableSnapshot{}})
		}
	}
	if cacheRoot != "" {
		if !filepath.IsAbs(cacheRoot) {
			return nil, ErrInvalidSource
		}
		if err := makePrivateDirectory(cacheRoot); err != nil {
			return nil, err
		}
		digest, private, err := stageRecoveredSource(ctx, cacheRoot, func(tree string) error {
			return copyContainedTree(root, tree, copyTreeLimits{ExcludePrivateFiles: true, ExcludePaths: recovered.Source.ExcludePaths})
		})
		if err != nil || digest != detection.Source.Digest {
			block("host_source_changed", "The source changed during recovery. Refresh discovery and review a new snapshot before migration.", "source")
			return recovered, nil
		}
		origin.RecoveryDirectory = private
		recovered.Source = DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeRecoveredSnapshot, ResourceID: digest, ExcludePaths: append([]string{}, recovered.Source.ExcludePaths...)}
		recovered.Detection.Source.LocalPath = ""
	}
	if selected != nil {
		origin.BuildSources = []RecoveredBuildSource{{Service: candidate.Name, Name: selected.Name, Framework: selected.Framework, Language: selected.Recipe, Role: selected.Profile, Confidence: selected.Confidence, Status: "snapshot", SnapshotDigest: detection.Source.Digest, Reason: "Verified source and captured command were prepared without changing the original runtime."}}
	}
	sort.Slice(recovered.Configuration.Variables, func(i, j int) bool {
		return recovered.Configuration.Variables[i].Name < recovered.Configuration.Variables[j].Name
	})
	sort.Slice(recovered.Configuration.Build.Secrets, func(i, j int) bool {
		return recovered.Configuration.Build.Secrets[i].Variable < recovered.Configuration.Build.Secrets[j].Variable
	})
	origin.Blockers = uniqueHostRecoveryStrings(origin.Blockers)
	origin.Warnings = uniqueHostRecoveryStrings(origin.Warnings)
	baseline := sha256.Sum256(mustJSON(struct {
		ConfigurationDigest string
		SourceDigest        string
		Source              DraftSourceConfig
		Configuration       PlanConfiguration
		Environment         map[string]string
	}{capture.ConfigurationDigest, detection.Source.Digest, canonicalSourceConfig(recovered.Source), canonicalConfiguration(recovered.Configuration), recovered.Environment}))
	origin.BaselineDigest = hex.EncodeToString(baseline[:])
	return recovered, nil
}

// Procfs birth timestamps and filesystem wall-clock timestamps have different
// precision. Only clear edits beyond that uncertainty are treated as known drift.
const hostSourceClockTolerance = 2 * time.Second

func hostSourceDirectoryMatches(local os.FileInfo, hostPath string) bool {
	host, err := os.Stat(hostPath)
	return err == nil && host.IsDir() && os.SameFile(local, host)
}

func knownHostSourceDrift(ctx context.Context, root string, capture *procs.HostWorkloadCapture, exclusions []string) (bool, error) {
	var started int64
	for _, process := range capture.Processes {
		if process.PID > 1 && process.CreateTime > 0 && (process.State == "online" || process.State == "launching" || process.State == "active" || process.State == "running") {
			if started == 0 || process.CreateTime < started {
				started = process.CreateTime
			}
		}
	}
	if started == 0 {
		return false, ErrSourceUnavailable
	}
	changed := false
	check := func(info os.FileInfo) {
		if info.Mode().IsRegular() && info.ModTime().UnixMilli() > started+hostSourceClockTolerance.Milliseconds() {
			changed = true
		}
	}
	for path := range capture.SourceFiles {
		info, err := os.Stat(hostexec.HostPath(path))
		if err != nil {
			return false, ErrSourceUnavailable
		}
		check(info)
	}
	entries := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			return ErrSourceUnavailable
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if excludedLocalSourcePath(relative, exclusions) || (entry.IsDir() && (entry.Name() == ".git" || entry.Name() == ".just-dashboard")) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		entries++
		if entries > (copyTreeLimits{}).normalized().MaxFiles {
			return ErrSourceUnavailable
		}
		private := false
		for _, part := range strings.Split(relative, string(filepath.Separator)) {
			private = private || privateSourceEntry(part)
		}
		if private && !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return ErrSourceUnavailable
			}
			check(info)
		}
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".vue", ".svelte", ".json", ".py", ".rb", ".php", ".pl", ".sh", ".go", ".rs", ".jar", ".dll", ".class", ".so", ".pyd", ".wasm":
			info, err := entry.Info()
			if err != nil {
				return ErrSourceUnavailable
			}
			check(info)
		}
		return nil
	})
	return changed, err
}

func translateHostEnvironmentPath(value, root string) (string, bool, bool) {
	prefix, path, suffix, escaped := "", value, "", false
	for _, scheme := range []string{"file:", "sqlite:", "sqlite3:"} {
		if strings.HasPrefix(value, scheme) {
			parsed, err := url.Parse(value)
			if err != nil || (parsed.Host != "" && parsed.Host != "localhost") || parsed.User != nil {
				return value, false, false
			}
			path = parsed.EscapedPath()
			if !filepath.IsAbs(parsed.Path) {
				relative := parsed.Path
				if relative == "" {
					relative = parsed.Opaque
				}
				clean := filepath.Clean(relative)
				return value, false, clean != ".." && !strings.HasPrefix(clean, "../")
			}
			start := strings.Index(value[len(scheme):], path)
			if start < 0 {
				return value, false, false
			}
			start += len(scheme)
			prefix, suffix = value[:start], value[start+len(path):]
			path = parsed.Path
			escaped = true
			break
		}
	}
	if !filepath.IsAbs(path) {
		clean := filepath.Clean(path)
		return value, false, clean != ".." && !strings.HasPrefix(clean, "../")
	}
	clean := filepath.Clean(path)
	if clean != root && !strings.HasPrefix(clean, root+string(filepath.Separator)) {
		return value, false, clean == "/tmp" || clean == "/var/tmp"
	}
	relative, err := filepath.Rel(root, clean)
	if err != nil {
		return value, false, false
	}
	containerPath := filepath.Join("/app", relative)
	if escaped {
		containerPath = (&url.URL{Path: containerPath}).EscapedPath()
	}
	return prefix + containerPath + suffix, true, true
}

func translateHostRuntimeEnvironment(name, value, root string) (string, bool, bool) {
	if name == "NODE_OPTIONS" {
		// Only options with no filesystem operands are carried implicitly.
		// A loader or unknown option needs a reviewed container-specific plan;
		// parsing it as a shell command would change Node's own token rules.
		return value, false, supportedHostNodeOptions(value)
	}
	switch name {
	case "NODE_PATH", "SSL_CERT_DIR", "LD_LIBRARY_PATH", "PYTHONPATH", "PERL5LIB", "RUBYLIB":
		parts := strings.Split(value, string(os.PathListSeparator))
		changed := false
		for index, part := range parts {
			translated, partChanged, supported := translateHostEnvironmentPath(part, root)
			if !supported {
				return value, false, false
			}
			parts[index], changed = translated, changed || partChanged
		}
		return strings.Join(parts, string(os.PathListSeparator)), changed, true
	case "LD_PRELOAD":
		if strings.TrimSpace(value) != "" {
			return value, false, false
		}
	}
	return translateHostEnvironmentPath(value, root)
}

func supportedHostNodeOptions(value string) bool {
	if len(value) > 4096 || strings.ContainsAny(value, "\"'\\\x00") {
		return false
	}
	options := strings.Fields(value)
	if len(options) > 128 {
		return false
	}
	for index := 0; index < len(options); index++ {
		option, argument, assigned := strings.Cut(options[index], "=")
		switch option {
		case "--enable-source-maps", "--no-warnings", "--trace-warnings", "--trace-deprecation", "--throw-deprecation", "--no-deprecation", "--abort-on-uncaught-exception":
			if assigned {
				return false
			}
		case "--max-old-space-size", "--max_old_space_size", "--max-semi-space-size", "--max_semi_space_size", "--stack-size", "--stack_size":
			if !assigned {
				index++
				if index >= len(options) {
					return false
				}
				argument = options[index]
			}
			limit, err := strconv.ParseUint(argument, 10, 32)
			if err != nil || limit == 0 {
				return false
			}
		case "--unhandled-rejections":
			if !assigned || (argument != "strict" && argument != "throw" && argument != "warn" && argument != "warn-with-error-code" && argument != "none") {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func hostManagerEnvironmentPath(name string) bool {
	switch name {
	case "HOME", "PATH", "SHELL", "PWD", "OLDPWD", "TMPDIR", "TMP", "TEMP", "XDG_RUNTIME_DIR", "SSH_AUTH_SOCK", "MEMORY_PRESSURE_WATCH", "CPU_PRESSURE_WATCH", "IO_PRESSURE_WATCH",
		"PM2_HOME", "PM2_AGENT_HOME", "pm_out_log_path", "pm_err_log_path", "pm_pid_path", "exec_interpreter",
		"NVM_DIR", "NVM_BIN", "BUN_INSTALL", "PNPM_HOME", "GOPATH", "GOROOT", "GOCACHE", "GOMODCACHE":
		return true
	}
	return false
}

func selectedHostRecoveryCandidate(detection DetectionResult) *DetectedCandidate {
	for index := range detection.Candidates {
		if detection.Candidates[index].ID == detection.SelectedID {
			return &detection.Candidates[index]
		}
	}
	if detection.SelectedID == "" && len(detection.Candidates) == 1 {
		return &detection.Candidates[0]
	}
	return nil
}

func verifyHostDockerfileLayout(root string, build BuildPlanConfig, block func(string, string, string)) {
	tree := openDetectionTree(root)
	defer tree.close()
	content, ok := tree.read(filepath.ToSlash(filepath.Join(build.RootDirectory, build.Dockerfile)), 1<<20)
	if !ok {
		block("host_dockerfile_unavailable", "The original Dockerfile cannot be inspected safely before migration.", "build.dockerfile")
		return
	}
	model := modelDockerfile(content)
	if len(model.stages) == 0 {
		block("host_dockerfile_layout", "The Dockerfile has no verifiable runtime stage. Provide a build that copies this source into /app.", "build.dockerfile")
		return
	}
	lineage := model.lineage(model.finalStage(build.Target))
	workdir, copied, entryKnown, entryEmpty := "/", false, false, false
	for position := len(lineage) - 1; position >= 0; position-- {
		index := lineage[position]
		for _, instruction := range model.stages[index].Instructions {
			switch instruction.Keyword {
			case "WORKDIR":
				value := expandDockerfileWord(strings.TrimSpace(instruction.Args), model.variables(index))
				if filepath.IsAbs(value) {
					workdir = filepath.Clean(value)
				} else {
					workdir = filepath.Join(workdir, value)
				}
			case "ENTRYPOINT":
				entryKnown, entryEmpty = true, instruction.IsJSON && len(instruction.JSON) == 0
			case "COPY":
				if _, fromStage := instruction.flag("from"); fromStage {
					continue
				}
				words := instruction.commandWords(model.escape)
				if len(words) != 2 || (words[0] != "." && words[0] != "./") {
					continue
				}
				destination := words[1]
				if !filepath.IsAbs(destination) {
					destination = filepath.Join(workdir, destination)
				}
				if filepath.Clean(destination) == "/app" {
					copied = true
				}
			}
		}
	}
	if workdir != "/app" || !copied || build.RootDirectory != "" && build.RootDirectory != "." {
		block("host_dockerfile_layout", "Automatic host migration requires this source copied into /app with WORKDIR /app. Other image layouts need an explicit migration plan.", "build.dockerfile")
	}
	if !entryKnown || !entryEmpty {
		block("host_dockerfile_entrypoint", "The Dockerfile must explicitly clear its inherited entrypoint with ENTRYPOINT [] so the captured command runs unchanged.", "build.dockerfile")
	}
}

func hostCommandForContainer(command []string, root, recipe string) ([]string, error) {
	if len(command) == 0 || len(command) > 128 {
		return nil, ErrRecoveryBlocked
	}
	out := append([]string{}, command...)
	nodeCommand := filepath.Base(command[0]) == "node" || filepath.Base(command[0]) == "nodejs"
	for index, arg := range command {
		if strings.ContainsRune(arg, 0) || len(arg) > 4096 {
			return nil, ErrRecoveryBlocked
		}
		if index == 0 && (filepath.Base(arg) == "node" || filepath.Base(arg) == "nodejs") {
			out[index] = "/usr/local/bin/node"
			continue
		}
		if index == 0 && recipe == "python" && (filepath.Base(arg) == "python" || filepath.Base(arg) == "python3" || strings.HasPrefix(filepath.Base(arg), "python3.")) {
			out[index] = "/usr/local/bin/python"
			continue
		}
		if strings.HasPrefix(arg, "-") {
			flag, operand, assigned := strings.Cut(arg, "=")
			if assigned && (filepath.IsAbs(operand) || strings.HasPrefix(operand, "file:") || strings.HasPrefix(operand, "../")) {
				translated, changed, supported := translateHostEnvironmentPath(operand, root)
				if !supported || ((filepath.IsAbs(operand) || strings.HasPrefix(operand, "file:/")) && !changed) {
					return nil, ErrRecoveryBlocked
				}
				out[index] = flag + "=" + translated
				continue
			}
			if nodeCommand && ((strings.HasPrefix(arg, "-r/") || strings.HasPrefix(arg, "-r../")) || strings.HasPrefix(arg, "-e") || strings.HasPrefix(arg, "-p") || flag == "--eval" || flag == "--print") {
				return nil, ErrRecoveryBlocked
			}
		}
		if clean := filepath.Clean(arg); clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, ErrRecoveryBlocked
		}
		if arg == root || strings.HasPrefix(arg, root+string(filepath.Separator)) {
			relative, err := filepath.Rel(root, arg)
			if err != nil || relative == ".." || strings.HasPrefix(relative, "../") {
				return nil, ErrRecoveryBlocked
			}
			out[index] = filepath.Join("/app", relative)
		} else if filepath.IsAbs(arg) || strings.Contains(arg, root+string(filepath.Separator)) {
			return nil, ErrRecoveryBlocked
		}
	}
	return out, nil
}

func hostRecoveryAddress(candidate WorkloadCandidate) (int, string) {
	for _, service := range candidate.Services {
		for _, port := range service.Ports {
			if port.Protocol == "tcp" && port.HostPort > 0 {
				host := port.HostIP
				if host == "" || host == "0.0.0.0" {
					host = "127.0.0.1"
				}
				if host == "::" {
					host = "::1"
				}
				if validBindAddress(host) {
					return port.HostPort, host
				}
			}
		}
	}
	return 0, "127.0.0.1"
}

func inspectHostRecoveryFiles(ctx context.Context, root string, capture *procs.HostWorkloadCapture, recovered *RecoveredWorkload, block func(string, string, string)) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			block("host_source_unreadable", "A source entry could not be inspected for migration.", "source")
			return nil
		}
		if path == root {
			return nil
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == ".just-dashboard" || entry.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if privateSourceEntry(entry.Name()) {
			block("host_private_source_file", "The source contains private configuration files. They are excluded from the managed snapshot; recover their effective variables and file dependencies explicitly before migration.", "variables")
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			block("host_source_changed", "A source file changed during migration review. Refresh the recovered plan.", "source")
			return nil
		}
		if !entry.IsDir() && info.Mode().IsRegular() && capture.UID != 0 && info.Mode().Perm()&0004 == 0 {
			block("host_source_permissions", "Some source files are private to the original account. Use a Dockerfile with explicit COPY ownership so the preserved runtime UID can read them.", "runtime.user")
		}
		if entry.IsDir() && filepath.Dir(path) == root && (entry.Name() == "data" || entry.Name() == "uploads" || entry.Name() == "storage") {
			recovered.Configuration.Runtime.Mounts = append(recovered.Configuration.Runtime.Mounts, RuntimeMount{Source: path, Target: filepath.Join("/app", entry.Name()), Ownership: OwnershipLinked})
			recovered.Adoption.Warnings = append(recovered.Adoption.Warnings, "Existing "+entry.Name()+" is retained as a linked host data directory. Review every persistent path before cutover; importing does not copy or delete its contents.")
			return filepath.SkipDir
		}
		lower := strings.ToLower(entry.Name())
		if strings.HasSuffix(lower, ".db") || strings.HasSuffix(lower, ".sqlite") || strings.HasSuffix(lower, ".sqlite3") {
			block("host_database_storage", "A database file is mixed with the source. Move it into a persistent directory and configure a retained mount, including journal/WAL files, before migration.", "runtime.mounts")
		}
		return nil
	})
}

func uniqueHostRecoveryStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
