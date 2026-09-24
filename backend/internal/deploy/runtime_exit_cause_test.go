package deploy

import (
	"strings"
	"testing"
)

func TestStartCommandExitedIsDiagnosedFromTheExitCode(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		containers []ContainerDiagnostics
		cause      bool
	}{
		"exited 0":           {[]ContainerDiagnostics{{State: "exited", ExitCode: 0}}, true},
		"restarting after 0": {[]ContainerDiagnostics{{State: "restarting", ExitCode: 0, RestartCount: 4}}, true},
		"crashed":            {[]ContainerDiagnostics{{State: "exited", ExitCode: 1}}, false},
		"killed for memory":  {[]ContainerDiagnostics{{State: "exited", ExitCode: 0, OOMKilled: true}}, false},
		"still running":      {[]ContainerDiagnostics{{State: "running", ExitCode: 0, RestartCount: 3}}, false},
		"a compose one-shot": {[]ContainerDiagnostics{{State: "exited"}, {State: "running"}}, false},
	} {
		cause := applicationOutputCause(test.containers)
		if (cause != nil && cause.Code == "start_command_exited") != test.cause {
			t.Fatalf("%s: %+v", name, cause)
		}
		if cause != nil && !strings.Contains(cause.sentence(), "pm2-runtime") {
			t.Fatalf("%s: sentence %q", name, cause.sentence())
		}
	}
	// A missing table in the output is the more specific cause.
	cause := applicationOutputCause([]ContainerDiagnostics{{State: "exited", Lines: []RuntimeLogLine{{Text: `relation "users" does not exist`}}}})
	if cause == nil || cause.Code != "schema_missing" {
		t.Fatalf("schema cause = %+v", cause)
	}
}
