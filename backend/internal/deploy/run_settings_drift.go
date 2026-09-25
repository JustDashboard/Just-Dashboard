package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
)

// RunSettingsDrift is what changed in the environment's settings since a run
// was planned: the plan revision it built against the one saved now, and the
// variables it was given against the ones set now. Retry replays the run's
// own snapshots by design, so a fix saved after a failure only takes effect
// through a new deploy, and the run page needs to know when that is so.
type RunSettingsDrift struct {
	RunID           int64            `json:"runId"`
	PlanRevision    int              `json:"planRevision"`
	DesiredRevision int              `json:"desiredRevision"`
	Changed         bool             `json:"changed"`
	Changes         []SettingsChange `json:"changes"`
}

// SettingsChange names one changed setting. Before and After carry plan
// values that are not secret — a package manager, a port, a command — and,
// for a variable, only its scopes: a variable's value never appears here.
type SettingsChange struct {
	Kind   string `json:"kind"`
	Field  string `json:"field"`
	Change string `json:"change"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

const settingsValueLength = 120

// RunSettingsDrift compares a run's frozen plan revision and variable
// snapshot with the environment's current ones.
func (s *PlanningStore) RunSettingsDrift(ctx context.Context, projectID, runID int64) (*RunSettingsDrift, error) {
	drift := &RunSettingsDrift{RunID: runID, Changes: []SettingsChange{}}
	var environmentID int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT r.environment_id, r.plan_revision, e.desired_revision
		  FROM deploy_runs r JOIN deploy_environments e ON e.id = r.environment_id
		 WHERE r.id = ? AND r.project_id = ?`, runID, projectID).
		Scan(&environmentID, &drift.PlanRevision, &drift.DesiredRevision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRunNotFound
		}
		return nil, err
	}
	if drift.PlanRevision != drift.DesiredRevision {
		changes, err := s.planRevisionChanges(ctx, environmentID, drift.PlanRevision, drift.DesiredRevision)
		if err != nil {
			return nil, err
		}
		drift.Changes = append(drift.Changes, changes...)
	}
	variables, err := s.runVariableChanges(ctx, environmentID, runID)
	if err != nil {
		return nil, err
	}
	drift.Changes = append(drift.Changes, variables...)
	drift.Changed = len(drift.Changes) > 0 || drift.PlanRevision != drift.DesiredRevision
	return drift, nil
}

func (s *PlanningStore) planRevisionChanges(ctx context.Context, environmentID int64, before, after int) ([]SettingsChange, error) {
	read := func(table string, revision int) (string, error) {
		var config string
		err := s.db.QueryRowContext(ctx, `SELECT config_json FROM `+table+` WHERE environment_id = ? AND revision = ?`,
			environmentID, revision).Scan(&config)
		if errors.Is(err, sql.ErrNoRows) {
			return "{}", nil
		}
		return config, err
	}
	changes := []SettingsChange{}
	var oldBuild, newBuild BuildPlanConfig
	for _, pair := range []struct {
		table  string
		target any
		rev    int
	}{
		{"deploy_build_plans", &oldBuild, before}, {"deploy_build_plans", &newBuild, after},
	} {
		config, err := read(pair.table, pair.rev)
		if err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(config), pair.target)
	}
	commands := func(tasks []ReleaseTaskConfig) string {
		names := make([]string, 0, len(tasks))
		for _, task := range tasks {
			entry := task.Name + "=" + task.Command
			if task.Runner != "" {
				// Where a task runs decides what it can reach.
				entry += " (" + task.Runner + ")"
			}
			names = append(names, entry)
		}
		return strings.Join(names, "; ")
	}
	// A build variable's step says which RUN receives it; names and steps are
	// plan text, never a value.
	secrets := func(bindings []BuildSecretConfig) string {
		steps := make([]string, 0, len(bindings))
		for _, binding := range bindings {
			steps = append(steps, binding.Variable+":"+binding.Step)
		}
		return strings.Join(steps, ", ")
	}
	for _, field := range []struct {
		name          string
		before, after string
	}{
		{"method", string(oldBuild.Method), string(newBuild.Method)},
		{"recipe", oldBuild.Recipe, newBuild.Recipe},
		{"packageManager", oldBuild.PackageManager, newBuild.PackageManager},
		{"goVersion", oldBuild.GoVersion, newBuild.GoVersion},
		{"pythonVersion", oldBuild.PythonVersion, newBuild.PythonVersion},
		{"rootDirectory", oldBuild.RootDirectory, newBuild.RootDirectory},
		{"dockerfile", oldBuild.Dockerfile, newBuild.Dockerfile},
		{"target", oldBuild.Target, newBuild.Target},
		{"primaryService", oldBuild.PrimaryService, newBuild.PrimaryService},
		{"goPackage", oldBuild.GoPackage, newBuild.GoPackage},
		{"cargoBin", oldBuild.CargoBin, newBuild.CargoBin},
		{"secrets", secrets(oldBuild.Secrets), secrets(newBuild.Secrets)},
		{"buildCommand", oldBuild.BuildCommand, newBuild.BuildCommand},
		{"startCommand", oldBuild.StartCommand, newBuild.StartCommand},
		{"outputDirectory", oldBuild.OutputDirectory, newBuild.OutputDirectory},
		{"spaFallback", strconv.FormatBool(oldBuild.SPAFallback), strconv.FormatBool(newBuild.SPAFallback)},
		{"targetPlatform", oldBuild.TargetPlatform, newBuild.TargetPlatform},
		{"releaseTasks", commands(oldBuild.ReleaseTasks), commands(newBuild.ReleaseTasks)},
	} {
		if field.before != field.after {
			changes = append(changes, settingsChange("build", field.name, field.before, field.after))
		}
	}
	var oldRuntime, newRuntime RuntimePlanConfig
	for _, pair := range []struct {
		target any
		rev    int
	}{{&oldRuntime, before}, {&newRuntime, after}} {
		config, err := read("deploy_runtime_plans", pair.rev)
		if err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(config), pair.target)
	}
	portText := func(port int) string {
		if port == 0 {
			return ""
		}
		return strconv.Itoa(port)
	}
	for _, field := range []struct {
		name          string
		before, after string
	}{
		{"internalPort", portText(oldRuntime.InternalPort), portText(newRuntime.InternalPort)},
		{"hostPort", portText(oldRuntime.HostPort), portText(newRuntime.HostPort)},
		{"bindAddress", oldRuntime.BindAddress, newRuntime.BindAddress},
		{"strategy", string(oldRuntime.Strategy), string(newRuntime.Strategy)},
		{"memoryMb", portText(int(oldRuntime.MemoryMB)), portText(int(newRuntime.MemoryMB))},
		{"command", strings.Join(oldRuntime.Command, " "), strings.Join(newRuntime.Command, " ")},
		{"mounts", mountTargets(oldRuntime.Mounts), mountTargets(newRuntime.Mounts)},
	} {
		if field.before != field.after {
			changes = append(changes, settingsChange("runtime", field.name, field.before, field.after))
		}
	}
	var oldSource, newSource string
	if err := s.db.QueryRowContext(ctx, `SELECT digest FROM deploy_sources WHERE environment_id = ? AND revision = ?`, environmentID, before).Scan(&oldSource); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT digest FROM deploy_sources WHERE environment_id = ? AND revision = ?`, environmentID, after).Scan(&newSource); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if oldSource != newSource {
		changes = append(changes, SettingsChange{Kind: "source", Field: "source", Change: "changed"})
	}
	return changes, nil
}

// mountTargets names a plan's mounts by where they land, the part a volume
// for detected state changes.
func mountTargets(mounts []RuntimeMount) string {
	targets := make([]string, 0, len(mounts))
	for _, mount := range mounts {
		targets = append(targets, mount.Target)
	}
	sort.Strings(targets)
	return strings.Join(targets, ", ")
}

func settingsChange(kind, field, before, after string) SettingsChange {
	change := "changed"
	switch {
	case before == "":
		change = "added"
	case after == "":
		change = "removed"
	}
	return SettingsChange{
		Kind: kind, Field: field, Change: change,
		Before: truncateUTF8Prefix(before, settingsValueLength), After: truncateUTF8Prefix(after, settingsValueLength),
	}
}

type variableRevision struct {
	digest, scopes string
}

// runVariableChanges compares the variable revisions a run was given with the
// active ones: names, digests and scopes only, never a value.
func (s *PlanningStore) runVariableChanges(ctx context.Context, environmentID, runID int64) ([]SettingsChange, error) {
	read := func(statement string, args ...any) (map[string]variableRevision, error) {
		rows, err := s.db.QueryContext(ctx, statement, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		result := map[string]variableRevision{}
		for rows.Next() {
			var name string
			var revision variableRevision
			if err := rows.Scan(&name, &revision.digest, &revision.scopes); err != nil {
				return nil, err
			}
			result[name] = revision
		}
		return result, rows.Err()
	}
	frozen, err := read(`
		SELECT v.key, v.value_digest, v.scopes FROM deploy_run_variable_revisions rv
		  JOIN deploy_variable_revisions v ON v.id = rv.variable_revision_id
		 WHERE rv.run_id = ?`, runID)
	if err != nil {
		return nil, err
	}
	current, err := read(`
		SELECT key, value_digest, scopes FROM deploy_variable_revisions
		 WHERE environment_id = ? AND active = 1`, environmentID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(frozen)+len(current))
	for name := range frozen {
		names = append(names, name)
	}
	for name := range current {
		if _, known := frozen[name]; !known {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	changes := []SettingsChange{}
	for _, name := range names {
		before, had := frozen[name]
		after, has := current[name]
		switch {
		case !had:
			changes = append(changes, SettingsChange{Kind: "variable", Field: name, Change: "added", After: after.scopes})
		case !has:
			changes = append(changes, SettingsChange{Kind: "variable", Field: name, Change: "removed", Before: before.scopes})
		case before.scopes != after.scopes:
			changes = append(changes, SettingsChange{Kind: "variable", Field: name, Change: "scope", Before: before.scopes, After: after.scopes})
		case before.digest != after.digest:
			changes = append(changes, SettingsChange{Kind: "variable", Field: name, Change: "changed"})
		}
	}
	return changes, nil
}
