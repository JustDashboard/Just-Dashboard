package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
)

func RecoverHostWorkload(ctx context.Context, candidate WorkloadCandidate, capture *procs.HostWorkloadCapture, analyzer *HostSourceAnalyzer, paths *files.Service, cacheRoot string) (*RecoveredWorkload, error) {
	if capture == nil || analyzer == nil || paths == nil || (capture.Manager != "pm2" && capture.Manager != "systemd" && capture.Manager != "process") {
		return nil, ErrRecoveryBlocked
	}
	origin := &WorkloadAdoption{Key: candidate.Key, Digest: candidate.Digest, Kind: candidate.Kind, ResourceID: candidate.ResourceID, Manager: capture.Manager, Name: candidate.Name,
		ServiceCount: candidate.Total, RunningCount: candidate.Running, Warnings: append([]string{}, capture.Warnings...), Blockers: append([]string{}, capture.Blockers...), Issues: []AdoptionIssue{},
		OriginalSourcePath: capture.SourceDirectory, BaselineDigest: capture.ConfigurationDigest}
	recovered := &RecoveredWorkload{Adoption: origin, Environment: map[string]string{}}
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
	recovered.Source = DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalDirectory, LocalPath: root}
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
	recovered.Detection = detection
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
	for name, value := range capture.Environment {
		if name == "NODE_CHANNEL_FD" || name == "NODE_CHANNEL_SERIALIZATION_MODE" {
			origin.Warnings = append(origin.Warnings, "The original Node IPC channel belongs to its manager and is not transferred into the container. Applications that use process.send or manager messages require an explicit process-manager migration plan.")
			continue
		}
		if ValidateEnvKey(name) != nil {
			block("host_variable_unsupported", "The original environment contains a variable name the managed deployment cannot represent.", "variables")
			continue
		}
		recovered.Environment[name] = value
		scopes := []string{"runtime"}
		if selected != nil {
			for _, variable := range selected.Variables {
				if variable.Name == name && (variable.Phase == "build" || variable.BrowserInlined || variable.Step == "install") {
					scopes = append(scopes, "build")
					recovered.Configuration.Build.Secrets = append(recovered.Configuration.Build.Secrets, BuildSecretConfig{Variable: name, Step: "install_and_build"})
					break
				}
			}
		}
		recovered.Configuration.Variables = append(recovered.Configuration.Variables, PlannedVariable{Name: name, Sensitivity: "secret", Scopes: scopes})
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
			origin.Warnings = append(origin.Warnings, "The managed Node recipe uses the captured interpreter major with the catalogue image's patch version and container operating system. Confirm the application works without host-installed packages, native host dependencies or PM2 IPC before deploying changes; provide a Dockerfile when it needs them.")
			if selected.Recipe != "node" {
				block("host_runtime_compatibility_unknown", "Automatic host recovery currently requires a Node recipe or an existing Dockerfile. Other interpreters need a reviewed Dockerfile before migration.", "build.method")
			}
			version := strings.Split(strings.TrimPrefix(capture.InterpreterVersion, "v"), ".")[0]
			if version == "20" || version == "22" || version == "24" {
				recovered.Configuration.Build.NodeVersion = version
			} else {
				block("host_interpreter_version_unknown", "The original Node interpreter version is unavailable or outside the managed recipe catalogue. Select a verified runtime version or provide a Dockerfile.", "build.nodeVersion")
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
			recovered.Configuration.Variables = append(recovered.Configuration.Variables, PlannedVariable{Name: name, Sensitivity: "secret", Scopes: []string{"runtime"}})
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
	origin.BaselineConfiguration = PlanConfiguration{Build: BuildPlanConfig{Method: BuildNone, Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}}, Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst, HostPort: port, InternalPort: port, BindAddress: host}, Variables: recovered.Configuration.Variables, Checks: recovered.Configuration.Checks, Dependencies: []PlannedDependency{}, Domains: []PlannedDomain{}}
	if capture.Manager != "process" {
		origin.Runtime, err = NativeBaselineRuntimeInput(capture, 0)
		if err != nil {
			block("host_restart_authority_unavailable", "The original manager identity cannot be retained as a verified rollback baseline.", "source")
		} else {
			origin.Runtime.Host, origin.Runtime.Port = host, port
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
		private, err := os.MkdirTemp(cacheRoot, "host-adoption-")
		if err != nil {
			return nil, err
		}
		if err := copyContainedTree(root, private, copyTreeLimits{ExcludePrivateFiles: true, ExcludePaths: recovered.Source.ExcludePaths}); err != nil {
			_ = os.RemoveAll(private)
			return nil, err
		}
		digest, err := localDirectoryDigest(ctx, private, recovered.Source.ExcludePaths)
		if err != nil || digest != detection.Source.Digest {
			_ = os.RemoveAll(private)
			block("host_source_changed", "The source changed during recovery. Refresh discovery and review a new snapshot before migration.", "source")
			return recovered, nil
		}
		origin.RecoveryDirectory = private
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

func selectedHostRecoveryCandidate(detection DetectionResult) *DetectedCandidate {
	for index := range detection.Candidates {
		if detection.Candidates[index].ID == detection.SelectedID {
			return &detection.Candidates[index]
		}
	}
	if len(detection.Candidates) == 1 {
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
	for index, arg := range command {
		if strings.ContainsRune(arg, 0) || len(arg) > 4096 {
			return nil, ErrRecoveryBlocked
		}
		if index == 0 && (filepath.Base(arg) == "node" || filepath.Base(arg) == "nodejs") {
			out[index] = "node"
			continue
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
