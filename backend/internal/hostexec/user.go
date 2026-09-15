package hostexec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
)

// CommandOnHostAsUser drops privilege after namespace entry, before the
// account's executable or environment is loaded. Dropping on nsenter itself
// would either prevent entry or encourage a dangerous root fallback.
func CommandOnHostAsUser(ctx context.Context, account *user.User, environment []string, name string, args ...string) (*exec.Cmd, error) {
	if account == nil {
		return nil, fmt.Errorf("host account is required")
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid host UID")
	}
	if _, err := strconv.ParseUint(account.Gid, 10, 32); err != nil {
		return nil, fmt.Errorf("invalid host GID")
	}
	if os.Geteuid() != 0 {
		if uid != uint64(os.Geteuid()) {
			return nil, fmt.Errorf("cannot switch host account")
		}
		command := CommandOnHost(ctx, name, args...)
		command.Env = append([]string{}, environment...)
		return command, nil
	}
	argv := []string{"--reuid=" + account.Uid, "--regid=" + account.Gid, "--init-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", "/usr/bin/env", "-i", "--"}
	argv = append(argv, environment...)
	argv = append(argv, name)
	argv = append(argv, args...)
	command := CommandOnHost(ctx, "/usr/bin/setpriv", argv...)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8"}
	return command, nil
}
