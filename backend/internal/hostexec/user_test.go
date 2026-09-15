package hostexec

import (
	"os"
	"os/user"
	"strings"
	"testing"
)

func TestAccountCommandDropsPrivilegeBeforeLoadingUserEnvironment(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("credential switch requires root")
	}
	account, err := user.Lookup("nobody")
	if err != nil {
		t.Skip("nobody account unavailable")
	}
	command, err := CommandOnHostAsUser(t.Context(), account, []string{"HOME=/nonexistent", "JD_TEST_ONLY=public"}, "/usr/bin/id", "-u")
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(command.Args, " ")
	if !strings.Contains(args, "--no-new-privs") || !strings.Contains(args, "-- /usr/bin/env -i -- HOME=/nonexistent") {
		t.Fatalf("unsafe argv: %s", args)
	}
	for _, value := range command.Env {
		if strings.HasPrefix(value, "JD_") {
			t.Fatalf("user environment loaded before credential drop: %s", value)
		}
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("credential drop: %v: %s", err, output)
	}
	if strings.TrimSpace(string(output)) != account.Uid {
		t.Fatalf("executed with uid %q, want %s", output, account.Uid)
	}
}
