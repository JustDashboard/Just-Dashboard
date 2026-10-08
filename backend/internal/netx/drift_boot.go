package netx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// BootHealth describes the loaded boot owner and its measured last activation.
// systemd does not say whether that activation was requested at boot or by an
// operator; BootTrigger remains unknown rather than inventing reboot evidence.
type BootHealth struct {
	Status           string        `json:"status"`
	Reason           string        `json:"reason,omitempty"`
	Unit             string        `json:"unit"`
	LoadState        string        `json:"loadState,omitempty"`
	UnitFileState    string        `json:"unitFileState,omitempty"`
	ActiveState      string        `json:"activeState,omitempty"`
	FragmentPath     string        `json:"fragmentPath,omitempty"`
	DropInPaths      string        `json:"dropInPaths,omitempty"`
	NeedDaemonReload string        `json:"needDaemonReload,omitempty"`
	Owned            bool          `json:"owned"`
	Repairable       bool          `json:"repairable"`
	Execution        BootExecution `json:"execution"`
}

type BootExecution struct {
	Status              string        `json:"status"`
	Source              string        `json:"source"`
	Scope               string        `json:"scope"`
	BootTrigger         string        `json:"bootTrigger"`
	BootID              string        `json:"bootId,omitempty"`
	InvocationID        string        `json:"invocationId,omitempty"`
	StartedAt           string        `json:"startedAt,omitempty"`
	FinishedAt          string        `json:"finishedAt,omitempty"`
	StartedMonotonicUS  uint64        `json:"startedMonotonicUs,omitempty"`
	FinishedMonotonicUS uint64        `json:"finishedMonotonicUs,omitempty"`
	Result              string        `json:"result,omitempty"`
	ExitStatus          *int          `json:"exitStatus,omitempty"`
	Reason              string        `json:"reason,omitempty"`
	Commands            []BootCommand `json:"commands"`
}

type BootCommand struct {
	Path         string `json:"path"`
	IgnoreErrors bool   `json:"ignoreErrors"`
	StartedAt    string `json:"startedAt,omitempty"`
	FinishedAt   string `json:"finishedAt,omitempty"`
	Code         string `json:"code,omitempty"`
	ExitStatus   *int   `json:"exitStatus,omitempty"`
}

func (s *Service) driftBoot(ctx context.Context) BootHealth {
	props := []string{"LoadState", "UnitFileState", "ActiveState", "FragmentPath", "DropInPaths", "NeedDaemonReload", "Result", "ExecMainStatus", "ExecMainCode", "ExecMainStartTimestamp", "ExecMainExitTimestamp", "ExecMainStartTimestampMonotonic", "ExecMainExitTimestampMonotonic", "InvocationID", "ExecStart"}
	out, err := run(ctx, "systemctl", "show", UnitName, "--property="+strings.Join(props, ","))
	b := BootHealth{Status: "unknown", Unit: UnitName, Execution: BootExecution{Status: "unknown", Source: "systemd", Scope: "current_boot_last_activation", BootTrigger: "unknown", Commands: []BootCommand{}}}
	if bootID, err := os.ReadFile(filepath.Join(procSysRoot, "kernel/random/boot_id")); err == nil {
		b.Execution.BootID = strings.TrimSpace(string(bootID))
	}
	if err != nil {
		b.Status, b.Reason, b.Execution.Reason = "unreadable", err.Error(), err.Error()
		return b
	}
	values := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = strings.TrimSpace(value)
		}
	}
	b.LoadState, b.UnitFileState, b.ActiveState, b.FragmentPath, b.DropInPaths, b.NeedDaemonReload = values["LoadState"], values["UnitFileState"], values["ActiveState"], values["FragmentPath"], values["DropInPaths"], values["NeedDaemonReload"]
	unitData, unitErr := readDriftFile(s.paths.Unit)
	if unitErr == nil {
		b.Owned = strings.HasPrefix(string(unitData), generatedHeader)
	}
	switch {
	case b.LoadState == "":
		b.Status, b.Reason = "unreadable", "systemd did not expose the unit's load state."
	case b.LoadState == "not-found":
		b.Status, b.Reason = "missing", "The managed boot unit is not loaded."
	case b.LoadState != "loaded":
		b.Status, b.Reason = "drift", "The managed boot unit cannot be loaded."
	case b.FragmentPath != s.paths.Unit || b.DropInPaths != "":
		b.Status, b.Reason = "drift", "systemd uses a different unit fragment or additional overrides."
	case errors.Is(unitErr, os.ErrNotExist):
		b.Status, b.Reason = "missing", "The loaded boot unit's file is absent."
	case unitErr != nil:
		b.Status, b.Reason = "unreadable", unitErr.Error()
	case !b.Owned:
		b.Status, b.Reason = "conflict", "The loaded unit's file does not carry the managed ownership header."
	case b.NeedDaemonReload == "yes":
		b.Status, b.Reason = "drift", "systemd's loaded unit differs from the file on disk."
	case b.NeedDaemonReload != "no":
		b.Status, b.Reason = "unknown", "systemd did not expose whether the unit needs reloading."
	case b.UnitFileState == "":
		b.Status, b.Reason = "unknown", "systemd did not expose boot enablement."
	case b.UnitFileState != "enabled":
		b.Status, b.Reason = "drift", "The managed unit is not enabled persistently for boot."
	default:
		b.Status = "matching"
	}
	b.Repairable = b.Owned && (b.Status == "drift" || b.Status == "missing") && (b.FragmentPath == s.paths.Unit || b.LoadState == "not-found") && b.DropInPaths == ""
	e := &b.Execution
	e.StartedAt, e.FinishedAt, e.InvocationID, e.Result = values["ExecMainStartTimestamp"], values["ExecMainExitTimestamp"], values["InvocationID"], values["Result"]
	e.StartedMonotonicUS, _ = strconv.ParseUint(values["ExecMainStartTimestampMonotonic"], 10, 64)
	e.FinishedMonotonicUS, _ = strconv.ParseUint(values["ExecMainExitTimestampMonotonic"], 10, 64)
	if e.StartedMonotonicUS == 0 || e.StartedAt == "" {
		e.Status, e.Reason = "unrecorded", "No measured activation is available in this boot; a default Result value proves no execution."
		// Default exit fields do not describe an execution that never happened.
		e.StartedAt, e.FinishedAt, e.Result = "", "", ""
		return b
	}
	if code, err := strconv.Atoi(values["ExecMainStatus"]); err == nil {
		e.ExitStatus = &code
	}
	e.Commands = parseBootCommands(values["ExecStart"])
	failed, complete := false, b.Owned && len(e.Commands) > 0
	if unit, err := readDriftFile(s.paths.Unit); err == nil {
		expectedCommands := 0
		for _, line := range strings.Split(string(unit), "\n") {
			if strings.HasPrefix(line, "ExecStart=") {
				expectedCommands++
			}
		}
		complete = complete && expectedCommands > 0 && len(e.Commands) == expectedCommands
	} else {
		complete = false
	}
	for _, command := range e.Commands {
		if command.StartedAt == "" || command.FinishedAt == "" || command.ExitStatus == nil || command.Code != "exited" {
			complete = false
		}
		if command.ExitStatus != nil && *command.ExitStatus != 0 && command.StartedAt != "" {
			failed = true
		}
		if command.Code != "" && command.Code != "exited" && command.Code != "(null)" {
			failed = true
		}
	}
	switch {
	case failed || (e.Result != "" && e.Result != "success"):
		e.Status, e.Reason = "failed", "The measured activation or one of its commands failed, including commands whose errors the unit ignores."
	case b.ActiveState == "activating" || b.ActiveState == "deactivating":
		e.Status = "running"
	case complete && e.FinishedMonotonicUS > 0 && e.Result == "success":
		e.Status = "succeeded"
	default:
		e.Reason = "A measured activation exists, but complete command outcomes are unavailable."
	}
	return b
}

func parseBootCommands(raw string) []BootCommand {
	out := []BootCommand{}
	for _, part := range strings.Split(raw, "{ ") {
		if !strings.HasPrefix(part, "path=") {
			continue
		}
		end := strings.Index(part, " }")
		if end < 0 {
			continue
		}
		values := map[string]string{}
		for _, field := range strings.Split(part[:end], " ; ") {
			if key, value, ok := strings.Cut(field, "="); ok {
				values[key] = strings.TrimSpace(value)
			}
		}
		c := BootCommand{Path: values["path"], IgnoreErrors: values["ignore_errors"] == "yes", Code: values["code"]}
		if v := strings.Trim(values["start_time"], "[]"); v != "n/a" {
			c.StartedAt = v
		}
		if v := strings.Trim(values["stop_time"], "[]"); v != "n/a" {
			c.FinishedAt = v
		}
		status, _, _ := strings.Cut(values["status"], "/")
		if n, err := strconv.Atoi(status); err == nil {
			c.ExitStatus = &n
		}
		out = append(out, c)
	}
	return out
}
