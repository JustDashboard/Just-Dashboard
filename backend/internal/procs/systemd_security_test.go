package procs

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Items of `systemd-analyze security --json=short --no-pager cron.service`
// on systemd 255 (255.4-1ubuntu8), verbatim and in its order, one per line.
const cronSecurityJSON = `[
{"set":false,"name":"RootDirectory=/RootImage=","json_field":"RootDirectoryOrRootImage","description":"Service runs within the host's root directory","exposure":"0.1"},
{"set":null,"name":"SupplementaryGroups=","json_field":"SupplementaryGroups","description":"Service runs as root, option does not matter","exposure":null},
{"set":false,"name":"User=/DynamicUser=","json_field":"UserOrDynamicUser","description":"Service runs as root user","exposure":"0.4"},
{"set":true,"name":"AmbientCapabilities=","json_field":"AmbientCapabilities","description":"Service process does not receive ambient capabilities","exposure":null},
{"set":false,"name":"RestrictAddressFamilies=~…","json_field":"RestrictAddressFamilies_OTHER","description":"Service may allocate exotic sockets","exposure":"0.3"},
{"set":false,"name":"CapabilityBoundingSet=~CAP_NET_(BIND_SERVICE|BROADCAST|RAW)","json_field":"CapabilityBoundingSet_CAP_NET_BIND_SERVICE_BROADCAST_RAW)","description":"Service has elevated networking privileges","exposure":"0.1"},
{"set":false,"name":"PrivateNetwork=","json_field":"PrivateNetwork","description":"Service has access to the host's network","exposure":"0.5"},
{"set":true,"name":"KeyringMode=","json_field":"KeyringMode","description":"Service doesn't share key material with other services","exposure":null},
{"set":false,"name":"UMask=","json_field":"UMask","description":"Files created by service are world-readable by default","exposure":"0.1"}
]
`

// The text form's last lines for the same unit.
const cronSecurityText = "✗ UMask=                                                      Files created by service are world-readable by default                       0.1\n" +
	"\n" +
	"→ Overall exposure level for cron.service: 9.6 UNSAFE :-{\n"

func TestParseSecurityChecksSortsByExposure(t *testing.T) {
	checks, err := ParseSecurityChecks(cronSecurityJSON)
	if err != nil {
		t.Fatal(err)
	}
	var fields []string
	for _, c := range checks {
		fields = append(fields, c.Field)
	}
	want := []string{
		"PrivateNetwork", "UserOrDynamicUser", "RestrictAddressFamilies_OTHER",
		"RootDirectoryOrRootImage", "CapabilityBoundingSet_CAP_NET_BIND_SERVICE_BROADCAST_RAW)", "UMask",
		"SupplementaryGroups", "AmbientCapabilities", "KeyringMode",
	}
	if !slices.Equal(fields, want) {
		t.Fatalf("order = %q, want %q", fields, want)
	}
	first := checks[0]
	if first.Name != "PrivateNetwork=" || first.Description != "Service has access to the host's network" ||
		first.Set == nil || *first.Set || first.Exposure == nil || *first.Exposure != 0.5 {
		t.Fatalf("first = %+v", first)
	}
	if groups := checks[6]; groups.Set != nil || groups.Exposure != nil {
		t.Fatalf("a check that does not apply = %+v, want set and exposure null", groups)
	}
	if ambient := checks[7]; ambient.Set == nil || !*ambient.Set || ambient.Exposure != nil {
		t.Fatalf("a check that is met = %+v", ambient)
	}
	if checks[2].Name != "RestrictAddressFamilies=~…" {
		t.Fatalf("name = %q", checks[2].Name)
	}
	// A later systemd writing the exposure as a number is read the same.
	numeric, err := ParseSecurityChecks(`[{"set":false,"name":"X=","json_field":"X","description":"d","exposure":0.7}]`)
	if err != nil || len(numeric) != 1 || numeric[0].Exposure == nil || *numeric[0].Exposure != 0.7 {
		t.Fatalf("numeric exposure = %+v (%v)", numeric, err)
	}
	if _, err := ParseSecurityChecks("Unit apt-daily.timer is not a service unit, refusing."); err == nil {
		t.Fatal("text parsed as checks")
	}
}

func TestParseSecurityOverall(t *testing.T) {
	for text, want := range map[string]struct {
		score   float64
		verdict string
	}{
		cronSecurityText: {9.6, "UNSAFE"},
		"-> Overall exposure level for cron.service: 9.6 UNSAFE :-{\n":                   {9.6, "UNSAFE"},
		"→ Overall exposure level for systemd-resolved.service: 2.2 OK :-)":              {2.2, "OK"},
		"→ Overall exposure level for app@a:b.service: 0.0 PERFECT :-}\n":                {0, "PERFECT"},
		"\x1b[0m→ Overall exposure level for x.service: 10.0 DANGEROUS :-{\x1b[0m\n\n\n": {10, "DANGEROUS"},
	} {
		score, verdict, ok := ParseSecurityOverall(text)
		if !ok || score != want.score || verdict != want.verdict {
			t.Errorf("%q = %v %q %v, want %v %q", text, score, verdict, ok, want.score, want.verdict)
		}
	}
	if _, _, ok := ParseSecurityOverall("Unit x.service not found, cannot analyze.\n"); ok {
		t.Error("a refusal read as a score")
	}
}

// Security asks systemd-analyze twice on the host, and answers a unit it
// will not assess with why, not an error.
func TestSecurityAnswersWhySystemdWillNotAssess(t *testing.T) {
	var asked [][]string
	answer := map[string]*CommandResult{}
	failure := map[string]error{}
	saved := analyze
	t.Cleanup(func() { analyze = saved })
	analyze = func(_ context.Context, args ...string) (*CommandResult, error) {
		asked = append(asked, args)
		unit := args[len(args)-1]
		json := slices.Contains(args, "--json=short")
		key := unit + map[bool]string{true: " json", false: " text"}[json]
		return answer[key], failure[key]
	}
	answer["cron.service json"] = &CommandResult{Stdout: cronSecurityJSON}
	answer["cron.service text"] = &CommandResult{Stdout: cronSecurityText}
	s := NewSystemd()
	report, err := s.Security(context.Background(), "cron.service")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Available || report.Score == nil || *report.Score != 9.6 || report.Verdict != "UNSAFE" ||
		len(report.Checks) != 9 || report.Reason != "" {
		t.Fatalf("report = %+v", report)
	}
	if got := fmt.Sprint(asked); got != "[[security --json=short --no-pager -- cron.service] [security --no-pager -- cron.service]]" {
		t.Fatalf("asked %s", got)
	}

	// The text form failing loses only the score.
	failure["noscore.service text"] = fmt.Errorf("systemd-analyze exited 1: ")
	answer["noscore.service json"] = &CommandResult{Stdout: cronSecurityJSON}
	answer["noscore.service text"] = &CommandResult{ExitCode: 1}
	if report, err := s.Security(context.Background(), "noscore.service"); err != nil || !report.Available ||
		report.Score != nil || report.Verdict != "" || len(report.Checks) != 9 {
		t.Fatalf("report without a score = %+v (%v)", report, err)
	}

	for unit, c := range map[string]struct {
		res    *CommandResult
		err    error
		reason string
	}{
		"apt-daily.timer": {&CommandResult{ExitCode: 1, Stderr: "Unit apt-daily.timer is not a service unit, refusing.\n"},
			fmt.Errorf("systemd-analyze exited 1"), "services only"},
		"ghost.service": {&CommandResult{ExitCode: 1, Stderr: "Unit ghost.service not found, cannot analyze.\n"},
			fmt.Errorf("systemd-analyze exited 1"), "no unit by this name"},
		"bare.service": {&CommandResult{ExitCode: -1}, fmt.Errorf("systemd-analyze %w", ErrNotInstalled), "not installed"},
		"nsenter.service": {&CommandResult{ExitCode: 127, Stderr: "nsenter: failed to execute systemd-analyze: No such file or directory\n"},
			fmt.Errorf("systemd-analyze exited 127"), "not installed"},
		"old.service": {&CommandResult{ExitCode: 1, Stderr: "systemd-analyze: unrecognized option '--json=short'\n"},
			fmt.Errorf("systemd-analyze exited 1"), "older than 250"},
		"slow.service":  {&CommandResult{ExitCode: -1}, fmt.Errorf("systemd-analyze timed out after 15s"), "did not answer"},
		"other.service": {&CommandResult{ExitCode: 1, Stderr: "Failed to connect to bus\n"}, fmt.Errorf("exited 1"), "Failed to connect to bus"},
		"empty.service": {&CommandResult{Stdout: "not json"}, nil, "could not be read"},
	} {
		answer[unit+" json"], failure[unit+" json"] = c.res, c.err
		report, err := s.Security(context.Background(), unit)
		if err != nil {
			t.Fatalf("%s: %v", unit, err)
		}
		if report.Available || report.Checks == nil || len(report.Checks) != 0 || !strings.Contains(report.Reason, c.reason) {
			t.Errorf("%s = %+v, want unavailable because %q", unit, report, c.reason)
		}
	}
	if _, err := s.Security(context.Background(), "-bad"); err == nil {
		t.Fatal("an invalid name was assessed")
	}
}
