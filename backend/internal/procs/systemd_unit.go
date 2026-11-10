package procs

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RelatedUnit is a unit another one names in a dependency, with its state.
// Relations are the dependency properties that name it, in the order
// dependencyProps lists them, so the page can say "wants", "after" or
// "wanted by" without a second read.
type RelatedUnit struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	LoadState     string   `json:"loadState"`
	ActiveState   string   `json:"activeState"`
	SubState      string   `json:"subState"`
	UnitFileState string   `json:"unitFileState"`
	Relations     []string `json:"relations"`
}

// dependencyProps are the unit properties that name other units: what it
// needs, what it orders itself against, what it triggers, and the reverse of
// each, which systemd keeps for every unit named in another's.
var dependencyProps = []string{
	"Requires", "Requisite", "Wants", "BindsTo", "PartOf", "Upholds", "Conflicts",
	"Before", "After", "OnFailure", "OnSuccess", "Triggers", "TriggeredBy",
	"RequiredBy", "RequisiteOf", "WantedBy", "BoundBy", "ConsistsOf", "UpheldBy",
	"ConflictedBy", "PropagatesReloadTo", "ReloadPropagatedFrom", "JoinsNamespaceOf",
}

// relatedLimit bounds the one `show` over the related units. A target such as
// multi-user.target names every service it pulls in, and the page lists them;
// past a few hundred it is not a list anybody reads.
const relatedLimit = 300

// Related lists the units this one names in its dependencies, each with its
// state, read in one `systemctl show` over all of them.
func (s *Systemd) Related(ctx context.Context, name string) ([]RelatedUnit, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	res, err := run(ctx, 20*time.Second, "systemctl", "show", "--no-pager",
		"-p", "Id,"+strings.Join(dependencyProps, ","), "--", name)
	if err != nil {
		return nil, err
	}
	props := map[string]string{}
	if records := parseShowRecords(res.Stdout); len(records) > 0 {
		props = records[0]
	}
	self := props["Id"]
	if self == "" {
		self = name
	}
	names, relations := relatedNames(self, props)
	out := []RelatedUnit{}
	if len(names) == 0 {
		return out, nil
	}
	args := append([]string{"show", "--no-pager",
		"-p", "Id,Description,LoadState,ActiveState,SubState,UnitFileState", "--"}, names...)
	res, err = run(ctx, 20*time.Second, "systemctl", args...)
	if err != nil {
		return nil, err
	}
	records := parseShowRecords(res.Stdout)
	seen := map[string]bool{}
	for i, record := range records {
		// systemctl answers in the order it was asked, so a record is the
		// name asked for even when its Id is the unit an alias points at.
		asked := ""
		if len(records) == len(names) {
			asked = names[i]
		}
		id := record["Id"]
		if id == "" {
			id = asked
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		rel := relations[asked]
		if rel == nil {
			rel = relations[id]
		}
		out = append(out, RelatedUnit{
			Name: id, Description: record["Description"],
			LoadState: record["LoadState"], ActiveState: record["ActiveState"],
			SubState: record["SubState"], UnitFileState: record["UnitFileState"],
			Relations: append([]string{}, rel...),
		})
	}
	return out, nil
}

// relatedNames is every unit the dependency properties name, once, in the
// order of dependencyProps, without the unit itself and without any name
// that would not pass ValidateName, and at most relatedLimit of them; with
// each name, the properties that named it.
func relatedNames(self string, props map[string]string) ([]string, map[string][]string) {
	var names []string
	relations := map[string][]string{}
	for _, prop := range dependencyProps {
		for _, unit := range strings.Fields(props[prop]) {
			if unit == self || ValidateName(unit) != nil {
				continue
			}
			if _, known := relations[unit]; !known {
				if len(names) >= relatedLimit {
					continue
				}
				names = append(names, unit)
			}
			if r := relations[unit]; len(r) == 0 || r[len(r)-1] != prop {
				relations[unit] = append(r, prop)
			}
		}
	}
	return names, relations
}

// AllowedSignal reports whether a signal, named as SIGHUP is, is one the
// dashboard sends: the same list for a process and for a unit.
func AllowedSignal(name string) bool {
	_, ok := allowedSignals[strings.ToUpper(name)]
	return ok
}

// ErrInvalidValue is a request value outside what a command accepts.
var ErrInvalidValue = errors.New("value is not allowed")

// Kill sends a signal to a unit's processes through systemd: to its main
// process alone, or to every process in its cgroup. Unlike signalling a PID,
// this reaches exactly the unit's processes however many there are, and
// cannot reach a process that took over a reused PID.
func (s *Systemd) Kill(ctx context.Context, name, signal, whom string) (*CommandResult, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	signal = strings.ToUpper(signal)
	if !AllowedSignal(signal) {
		return nil, fmt.Errorf("%w: signal %q", ErrInvalidValue, signal)
	}
	if whom != "main" && whom != "all" {
		return nil, fmt.Errorf("%w: whom %q", ErrInvalidValue, whom)
	}
	// --kill-who= rather than --kill-whom=: systemd 252 added the second
	// spelling and still takes the first, which is all that 249 and older
	// know.
	return run(ctx, 30*time.Second, "systemctl", "kill",
		"--signal="+signal, "--kill-who="+whom, "--", name)
}

// UnitLimits are the resource limits the service page sets on a running
// unit. A nil field is left as it is; CPUQuota "" clears the quota.
// Persistent writes them to a drop-in that survives a reboot; otherwise they
// last until the next one (`--runtime`).
type UnitLimits struct {
	MemoryMax  *string `json:"memoryMax,omitempty"`
	CPUQuota   *string `json:"cpuQuota,omitempty"`
	TasksMax   *string `json:"tasksMax,omitempty"`
	Persistent bool    `json:"persistent"`
}

var (
	memoryMaxValue = regexp.MustCompile(`^[0-9]{1,20}$`)
	cpuQuotaValue  = regexp.MustCompile(`^[0-9]{1,6}%$`)
	tasksMaxValue  = regexp.MustCompile(`^[0-9]{1,9}$`)
)

// Assignments validates the limits and returns them as set-property
// assignments, one argument each, in a fixed order.
func (l UnitLimits) Assignments() ([]string, error) {
	var out []string
	if l.MemoryMax != nil {
		v := *l.MemoryMax
		if v != "infinity" {
			if _, err := strconv.ParseUint(v, 10, 64); err != nil || !memoryMaxValue.MatchString(v) {
				return nil, fmt.Errorf("%w: memoryMax %q must be a number of bytes or infinity", ErrInvalidValue, v)
			}
		}
		out = append(out, "MemoryMax="+v)
	}
	if l.CPUQuota != nil {
		v := *l.CPUQuota
		if v != "" && !cpuQuotaValue.MatchString(v) {
			return nil, fmt.Errorf("%w: cpuQuota %q must be a percentage such as 150%%, or empty to clear it", ErrInvalidValue, v)
		}
		out = append(out, "CPUQuota="+v)
	}
	if l.TasksMax != nil {
		v := *l.TasksMax
		if v != "infinity" && !tasksMaxValue.MatchString(v) {
			return nil, fmt.Errorf("%w: tasksMax %q must be a number or infinity", ErrInvalidValue, v)
		}
		out = append(out, "TasksMax="+v)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: give at least one of memoryMax, cpuQuota and tasksMax", ErrInvalidValue)
	}
	return out, nil
}

// SetLimits applies the limits with `systemctl set-property`, which changes
// the running unit's cgroup at once and, when persistent, records them under
// /etc/systemd/system.control.
func (s *Systemd) SetLimits(ctx context.Context, name string, limits UnitLimits) (*CommandResult, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	assignments, err := limits.Assignments()
	if err != nil {
		return nil, err
	}
	args := []string{"set-property"}
	if !limits.Persistent {
		args = append(args, "--runtime")
	}
	args = append(append(args, "--", name), assignments...)
	return run(ctx, 30*time.Second, "systemctl", args...)
}
