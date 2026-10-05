package procs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/process"
)

// CaptureExisting selects one application in one existing account's daemon.
// JavaScript ecosystem files are never evaluated to discover configuration.
func (p *PM2) CaptureExisting(ctx context.Context, daemon, namespace, name string) (*HostWorkloadCapture, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	if err := ValidateName(daemon); err != nil {
		return nil, err
	}
	home, err := p.homeFor(daemon)
	if err != nil {
		return nil, fmt.Errorf("the original PM2 account is unavailable")
	}
	account, err := pm2Account(home.home)
	if err != nil {
		return nil, fmt.Errorf("the original PM2 account could not be verified")
	}
	data, err := readExistingPM2(ctx, home, account)
	if err != nil {
		return nil, err
	}
	capture, err := parsePM2Capture(data, account, namespace, name)
	if err != nil {
		return nil, err
	}
	startup, startupBlockers := capturePM2Startup(home, namespace, name)
	var runtimeEnvironment map[string]string
	sourceFiles := []string{capture.SourcePath}
	for index := range capture.Processes {
		proc := &capture.Processes[index]
		if proc.PID <= 1 || (proc.State != "online" && proc.State != "launching") {
			continue
		}
		original, err := process.NewProcessWithContext(ctx, proc.PID)
		if err != nil {
			return nil, ErrHostWorkloadChanged
		}
		created, err := original.CreateTimeWithContext(ctx)
		if err != nil {
			return nil, ErrHostWorkloadChanged
		}
		actual, err := CaptureExistingProcess(ctx, proc.PID, created)
		if err != nil {
			return nil, err
		}
		for key, expected := range map[string]string{"pm_id": strconv.Itoa(proc.ID), "pm_exec_path": capture.SourcePath, "pm_cwd": capture.SourceDirectory, "name": capture.Name, "namespace": namespace} {
			if value, present := actual.Environment[key]; present && value != expected {
				return nil, ErrHostWorkloadChanged
			}
		}
		blockPM2ManagerRuntimeDrift(capture, proc.ID, actual.Environment)
		sourceFiles = append(sourceFiles, actual.SourcePath)
		if capture.InterpreterPath == "" {
			capture.InterpreterPath = actual.InterpreterPath
		} else if capture.InterpreterPath != actual.InterpreterPath {
			capture.Blockers = append(capture.Blockers, "PM2 instances use different interpreter executables.")
		}
		if actual.SourceDirectory != capture.SourceDirectory {
			capture.Blockers = append(capture.Blockers, "The PM2 process has changed its working directory. Review its filesystem dependencies before migration.")
		}
		if runtimeEnvironment == nil {
			capture.UID, capture.GID = actual.UID, actual.GID
			runtimeEnvironment = actual.Environment
		} else if !equalCaptureEnvironment(runtimeEnvironment, actual.Environment) || capture.UID != actual.UID || capture.GID != actual.GID {
			capture.Blockers = append(capture.Blockers, "PM2 instances have different runtime accounts or effective environments.")
		}
		proc.Environment, proc.CreateTime = actual.Environment, created
	}
	if runtimeEnvironment != nil {
		capture.Environment, capture.EnvironmentNames = runtimeEnvironment, captureEnvironmentNames(runtimeEnvironment)
		// PM2's nested env can also hold ecosystem metadata objects. The
		// fenced process environment is the exact byte-string environment the
		// running app received and supersedes that ambiguous manager envelope.
		filtered := capture.Blockers[:0]
		for _, blocker := range capture.Blockers {
			if blocker != "PM2 environment values cannot be represented safely." {
				filtered = append(filtered, blocker)
			}
		}
		capture.Blockers = filtered
	}
	capture.SourceFiles, err = CaptureHostSourceFiles(uniqueCaptureStrings(sourceFiles))
	if err != nil {
		capture.Blockers = append(capture.Blockers, "The original PM2 entrypoint cannot be verified for safe restoration.")
	}
	// A daemon row can race a process exit/restart while /proc is read. Read
	// the same existing daemon again before accepting that PID as its app.
	latestData, err := readExistingPM2(ctx, home, account)
	if err != nil {
		return nil, err
	}
	latest, err := parsePM2Capture(latestData, account, namespace, name)
	if err != nil || latest.ConfigurationDigest != capture.ConfigurationDigest || len(latest.Processes) != len(capture.Processes) {
		return nil, ErrHostWorkloadChanged
	}
	for index, original := range capture.Processes {
		if latest.Processes[index].ID != original.ID || latest.Processes[index].PID != original.PID {
			return nil, ErrHostWorkloadChanged
		}
	}
	latestStartup, latestBlockers := capturePM2Startup(home, namespace, name)
	if string(startup) != string(latestStartup) {
		return nil, ErrHostWorkloadChanged
	}
	capture.StartupEvidence = startup
	capture.Blockers = uniqueCaptureStrings(append(capture.Blockers, append(startupBlockers, latestBlockers...)...))
	capture.RuntimeConfigurationDigest = capture.ConfigurationDigest
	capture.ConfigurationDigest = captureDigest(append(append([]byte(capture.ConfigurationDigest), 0), startup...))
	if version, probeErr := ProbeCapturedInterpreter(ctx, capture); probeErr == nil {
		capture.InterpreterVersion = version
	}
	if plan, prepareErr := PreparePM2StartupHandoff(capture, home.daemonDirectory(), namespace); prepareErr == nil {
		capture.StartupPlan = plan
	}
	return capture, nil
}

func parsePM2Capture(data []byte, account *user.User, namespace, name string) (*HostWorkloadCapture, error) {
	var rows []struct {
		PID       int32                      `json:"pid"`
		ID        int                        `json:"pm_id"`
		Name      string                     `json:"name"`
		Namespace string                     `json:"namespace"`
		Env       map[string]json.RawMessage `json:"pm2_env"`
	}
	if json.Unmarshal(data, &rows) != nil {
		return nil, fmt.Errorf("the original PM2 configuration could not be parsed")
	}
	uid, e1 := strconv.ParseUint(account.Uid, 10, 32)
	gid, e2 := strconv.ParseUint(account.Gid, 10, 32)
	if e1 != nil || e2 != nil {
		return nil, fmt.Errorf("the original PM2 account could not be verified")
	}
	out := &HostWorkloadCapture{Manager: "pm2", ResourceID: url.PathEscape(account.Username) + "/" + url.PathEscape(namespace) + "/" + url.PathEscape(name), Name: name,
		Account: account.Username, UID: uint32(uid), GID: uint32(gid), Processes: []HostProcessCapture{},
		Blockers: []string{}, Warnings: []string{}, Environment: map[string]string{}}
	private := []map[string]json.RawMessage{}
	var commands [][]string
	for _, row := range rows {
		ns := row.Namespace
		if ns == "" {
			ns = captureString(row.Env["namespace"])
		}
		if row.Name != name || ns != namespace {
			continue
		}
		if row.ID < 0 || len(out.Processes) >= 128 {
			return nil, fmt.Errorf("the original PM2 identity is unsupported")
		}
		script, cwd, interpreter := captureString(row.Env["pm_exec_path"]), captureString(row.Env["pm_cwd"]), captureString(row.Env["exec_interpreter"])
		if !filepath.IsAbs(script) || !filepath.IsAbs(cwd) {
			out.Blockers = append(out.Blockers, "PM2 does not expose an absolute source and working directory.")
		}
		if out.SourceDirectory != "" && (out.SourceDirectory != cwd || out.SourcePath != script) {
			out.Blockers = append(out.Blockers, "Instances of this PM2 application run different source paths or working directories.")
		}
		out.SourceDirectory, out.SourcePath = cwd, script
		version := captureString(row.Env["node_version"])
		if out.InterpreterVersion != "" && version != out.InterpreterVersion {
			out.Blockers = append(out.Blockers, "PM2 instances run different interpreter versions.")
		}
		out.InterpreterVersion = version
		out.RestartPolicy, out.StopSignal, out.GracePeriodSeconds = "unless-stopped", "SIGINT", 2
		if string(row.Env["autorestart"]) == "false" {
			out.RestartPolicy = "no"
		}
		if timeout := captureInt(row.Env["kill_timeout"]); timeout > 0 {
			out.GracePeriodSeconds = (timeout + 999) / 1000
			if out.GracePeriodSeconds > 300 {
				out.Blockers = append(out.Blockers, "The original PM2 shutdown grace period exceeds the managed deployment limit.")
			}
		}
		args, argsErr := captureJSONArgs(row.Env["args"])
		nodeArgs, nodeErr := captureJSONArgs(row.Env["node_args"])
		if argsErr != nil || nodeErr != nil {
			out.Blockers = append(out.Blockers, "PM2 stores command arguments as a command string; migration requires reviewing the exact argument boundaries.")
		}
		command := []string{script}
		if interpreter != "" && interpreter != "none" {
			command = append([]string{interpreter}, append(nodeArgs, script)...)
			command = append(command, args...)
		} else {
			command = append(command, args...)
		}
		commands = append(commands, command)
		env, envErr := captureJSONEnvironment(row.Env["env"])
		if envErr != nil {
			out.Blockers = append(out.Blockers, "PM2 environment values cannot be represented safely.")
		}
		if len(out.Processes) == 0 {
			out.Environment, out.Command = env, command
		} else if !equalCaptureEnvironment(out.Environment, env) {
			out.Blockers = append(out.Blockers, "PM2 instances have different environments; preserve their cluster configuration before migration.")
		}
		proc := HostProcessCapture{ID: row.ID, PID: row.PID, State: captureString(row.Env["status"]), Environment: env, Command: command,
			LogSources: []string{"pm2:" + url.PathEscape(account.Username) + "/" + strconv.Itoa(row.ID) + "/" + url.PathEscape(name)}}
		_ = json.Unmarshal(row.Env["created_at"], &proc.CreateTime)
		out.Processes = append(out.Processes, proc)
		stable := stablePM2Config(row.Env)
		stable["pm_id"], _ = json.Marshal(row.ID)
		private = append(private, stable)
		mode := captureString(row.Env["exec_mode"])
		if mode != "fork_mode" && mode != "fork" {
			out.Blockers = append(out.Blockers, "PM2 cluster mode needs an explicit container process-manager plan; a single process would lose cluster behavior.")
		}
		if instanceVar := captureString(row.Env["instance_var"]); instanceVar != "" && instanceVar != "NODE_APP_INSTANCE" {
			out.Blockers = append(out.Blockers, "A custom PM2 instance variable needs an equivalent managed runtime configuration before migration.")
		}
		for key, defaults := range map[string]int{"min_uptime": 1000, "max_restarts": 16} {
			if value := captureInt(row.Env[key]); value != 0 && value != defaults {
				out.Blockers = append(out.Blockers, "The PM2 setting "+key+" needs an equivalent managed restart policy before migration.")
			}
		}
		for _, key := range []string{"watch", "cron_restart", "wait_ready", "shutdown_with_message", "post_update", "increment_var", "max_memory_restart", "exp_backoff_restart_delay", "stop_exit_codes", "restart_delay"} {
			if rawCaptureEnabled(row.Env[key]) {
				out.Blockers = append(out.Blockers, "The PM2 setting "+key+" needs an equivalent managed runtime configuration before migration.")
			}
		}
	}
	if len(out.Processes) == 0 {
		return nil, ErrHostWorkloadChanged
	}
	if len(out.Processes) > 1 {
		out.Blockers = append(out.Blockers, "Multiple PM2 instances require an explicit replica plan before migration.")
	}
	sort.Slice(private, func(i, j int) bool { return captureInt(private[i]["pm_id"]) < captureInt(private[j]["pm_id"]) })
	sort.Slice(out.Processes, func(i, j int) bool { return out.Processes[i].ID < out.Processes[j].ID })
	for _, command := range commands {
		if !equalCaptureStrings(out.Command, command) {
			out.Blockers = append(out.Blockers, "PM2 instances have different command arguments.")
		}
	}
	out.OriginalConfig, _ = json.Marshal(private)
	out.ConfigurationDigest = captureDigest(out.OriginalConfig)
	out.EnvironmentNames = captureEnvironmentNames(out.Environment)
	out.Blockers = uniqueCaptureStrings(out.Blockers)
	out.Warnings = []string{"PM2 migration uses its current manager restart definition for application and interpreter arguments. PM2 rewrites process titles and omits argument arrays from its process environment, so procfs cannot universally attest the running argument boundaries. Verify the restart command and environment match the running application, or review a restart under the original manager before recovery; retain that frozen manager definition for baseline replay.", "The original PM2 account and configuration are retained as the baseline. Docker migration must preserve file permissions, data paths and interpreter dependencies.", "Native baseline replay requires the original PM2 daemon record and frozen source to remain available. A daemon reset can lose unsaved application records. Review a recoverable authority plan before cutover; missing manager authority causes restoration to refuse rather than implicitly recreating it."}
	return out, nil
}

func stablePM2Config(env map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(env))
	for key, value := range env {
		out[key] = value
	}
	for _, key := range []string{"status", "pm_uptime", "restart_time", "unstable_restarts", "exit_code", "prev_restart_delay", "restart_task", "vizion_running", "axm_actions", "axm_monitor", "axm_options", "axm_dynamic", "versioning", "node_version", "km_link", "_tree_pids"} {
		delete(out, key)
	}
	return out
}

func captureJSONEnvironment(raw json.RawMessage) (map[string]string, error) {
	out := map[string]string{}
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return nil, fmt.Errorf("unsupported environment")
	}
	for key, value := range values {
		if !captureEnvKey.MatchString(key) {
			return nil, fmt.Errorf("unsupported environment")
		}
		var text string
		if json.Unmarshal(value, &text) != nil {
			var scalar any
			if json.Unmarshal(value, &scalar) != nil {
				return nil, fmt.Errorf("unsupported environment")
			}
			switch scalar.(type) {
			case float64, bool:
				text = fmt.Sprint(scalar)
			default:
				return nil, fmt.Errorf("unsupported environment")
			}
		}
		if strings.ContainsRune(text, 0) {
			return nil, fmt.Errorf("unsupported environment")
		}
		out[key] = text
	}
	return out, nil
}

func captureJSONArgs(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []string{}, nil
	}
	var args []string
	if json.Unmarshal(raw, &args) != nil {
		return nil, fmt.Errorf("unsupported arguments")
	}
	for _, arg := range args {
		if strings.ContainsRune(arg, 0) {
			return nil, fmt.Errorf("unsupported arguments")
		}
	}
	return args, nil
}
func captureString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
func captureInt(raw json.RawMessage) int {
	var value int
	_ = json.Unmarshal(raw, &value)
	return value
}
func rawCaptureEnabled(raw json.RawMessage) bool {
	text := string(raw)
	return len(raw) > 0 && text != "null" && text != "false" && text != "0" && text != `""` && text != "[]" && text != "{}"
}
func equalCaptureEnvironment(a, b map[string]string) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func equalCaptureStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func uniqueCaptureStrings(values []string) []string {
	sort.Strings(values)
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}

func blockPM2ManagerRuntimeDrift(capture *HostWorkloadCapture, id int, actual map[string]string) {
	var configurations []map[string]json.RawMessage
	if json.Unmarshal(capture.OriginalConfig, &configurations) != nil {
		return
	}
	for _, config := range configurations {
		if captureInt(config["pm_id"]) != id {
			continue
		}
		var envelope map[string]json.RawMessage
		_ = json.Unmarshal(config["env"], &envelope)
		for key, value := range envelope {
			// Metadata objects are not byte-string environment values. Their
			// ambiguity is reviewed separately; visible scalar mismatches block.
			expected, scalar := capturePM2ScalarEnvironment(value)
			if !scalar {
				continue
			}
			if current, present := actual[key]; !present || current != expected {
				capture.Blockers = append(capture.Blockers, "The current PM2 restart environment differs from the running process. Review and restore its actual startup definition, or verify a reviewed restart under the original manager before migration.")
				return
			}
			if flat, present := config[key]; present {
				flatExpected, flatScalar := capturePM2ScalarEnvironment(flat)
				if flatScalar && actual[key] != flatExpected {
					capture.Blockers = append(capture.Blockers, "The current PM2 restart environment differs from the running process. Review and restore its actual startup definition, or verify a reviewed restart under the original manager before migration.")
					return
				}
			}
		}
		if value, present := actual["exec_interpreter"]; present && value != captureString(config["exec_interpreter"]) {
			capture.Blockers = append(capture.Blockers, "The current PM2 interpreter definition differs from the running process. Review its actual startup interpreter before migration.")
		}
	}
}

// The existing-daemon transport JSON.stringify uses the same canonical
// primitive text as JavaScript's spawn environment conversion. Preserve its
// number spelling instead of formatting through Go's float notation.
func capturePM2ScalarEnvironment(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var scalar any
	if json.Unmarshal(raw, &scalar) != nil {
		return "", false
	}
	switch value := scalar.(type) {
	case string:
		return value, true
	case float64:
		return string(raw), true
	case bool:
		return strconv.FormatBool(value), true
	default:
		return "", false
	}
}
