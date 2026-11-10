package procs

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestRelatedNamesCollectsEachUnitOnce(t *testing.T) {
	props := map[string]string{
		"Id":        "app.service",
		"Requires":  "sysinit.target system.slice",
		"Wants":     "network-online.target",
		"After":     `network-online.target sysinit.target app.service dev-disk-by\x2duuid-1.device -bad.service`,
		"Before":    "shutdown.target",
		"WantedBy":  "multi-user.target",
		"Conflicts": "shutdown.target",
		"Unknown":   "not-a-dependency.service",
	}
	names, relations := relatedNames("app.service", props)
	want := []string{"sysinit.target", "system.slice", "network-online.target", "shutdown.target", "multi-user.target"}
	if !slices.Equal(names, want) {
		t.Fatalf("names = %q, want %q", names, want)
	}
	for unit, rel := range map[string][]string{
		"sysinit.target":        {"Requires", "After"},
		"network-online.target": {"Wants", "After"},
		"shutdown.target":       {"Conflicts", "Before"},
		"multi-user.target":     {"WantedBy"},
	} {
		if !slices.Equal(relations[unit], rel) {
			t.Errorf("%s relations = %q, want %q", unit, relations[unit], rel)
		}
	}
	if names, _ := relatedNames("app.service", map[string]string{"Id": "app.service"}); len(names) != 0 {
		t.Fatalf("a unit with no dependencies named %q", names)
	}

	many := make([]string, relatedLimit+50)
	for i := range many {
		many[i] = fmt.Sprintf("u%d.service", i)
	}
	names, relations = relatedNames("app.service", map[string]string{"Wants": strings.Join(many, " "), "After": "u0.service u320.service"})
	if len(names) != relatedLimit || names[relatedLimit-1] != "u299.service" {
		t.Fatalf("capped to %d names ending %q", len(names), names[len(names)-1])
	}
	if !slices.Equal(relations["u0.service"], []string{"Wants", "After"}) || relations["u320.service"] != nil {
		t.Fatalf("relations past the cap: u0 %q, u320 %q", relations["u0.service"], relations["u320.service"])
	}
}

func TestAllowedSignalIsTheProcessList(t *testing.T) {
	for _, name := range []string{"SIGHUP", "sighup", "SIGTERM", "SIGKILL", "SIGUSR1", "SIGWINCH", "SIGCONT"} {
		if !AllowedSignal(name) {
			t.Errorf("%s refused", name)
		}
	}
	for _, name := range []string{"", "HUP", "SIGSEGV", "9", "SIGHUP; reboot"} {
		if AllowedSignal(name) {
			t.Errorf("%q allowed", name)
		}
	}
}

func TestUnitLimitsAssignments(t *testing.T) {
	str := func(s string) *string { return &s }
	for _, c := range []struct {
		limits UnitLimits
		want   []string
	}{
		{UnitLimits{MemoryMax: str("536870912")}, []string{"MemoryMax=536870912"}},
		{UnitLimits{MemoryMax: str("infinity")}, []string{"MemoryMax=infinity"}},
		{UnitLimits{CPUQuota: str("150%")}, []string{"CPUQuota=150%"}},
		{UnitLimits{CPUQuota: str("")}, []string{"CPUQuota="}},
		{UnitLimits{TasksMax: str("512")}, []string{"TasksMax=512"}},
		{UnitLimits{TasksMax: str("infinity")}, []string{"TasksMax=infinity"}},
		{UnitLimits{TasksMax: str("64"), MemoryMax: str("1024"), CPUQuota: str("50%"), Persistent: true},
			[]string{"MemoryMax=1024", "CPUQuota=50%", "TasksMax=64"}},
		{UnitLimits{}, nil},
		{UnitLimits{Persistent: true}, nil},
		{UnitLimits{MemoryMax: str("")}, nil},
		{UnitLimits{MemoryMax: str("512M")}, nil},
		{UnitLimits{MemoryMax: str("-1")}, nil},
		{UnitLimits{MemoryMax: str("99999999999999999999")}, nil},
		{UnitLimits{MemoryMax: str("1 TasksMax=1")}, nil},
		{UnitLimits{CPUQuota: str("150")}, nil},
		{UnitLimits{CPUQuota: str("1234567%")}, nil},
		{UnitLimits{CPUQuota: str("infinity")}, nil},
		{UnitLimits{TasksMax: str("")}, nil},
		{UnitLimits{TasksMax: str("1234567890")}, nil},
		{UnitLimits{TasksMax: str("50%")}, nil},
		{UnitLimits{TasksMax: str("8\nMemoryMax=1")}, nil},
	} {
		got, err := c.limits.Assignments()
		if c.want == nil {
			if err == nil || !errors.Is(err, ErrInvalidValue) {
				t.Errorf("%+v = %q (%v), want refused", c.limits, got, err)
			}
			continue
		}
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%+v = %q (%v), want %q", c.limits, got, err, c.want)
		}
	}
}
