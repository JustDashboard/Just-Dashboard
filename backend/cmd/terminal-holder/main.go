// Command terminal-holder keeps one web-terminal session's PTY open on the
// host, so the session outlives the dashboard that opened it. The dashboard
// starts it as a transient systemd unit; see internal/ptyhold.
//
// It is a binary of its own rather than a mode of the server because it runs
// for as long as a shell does, and a running process pins the file it was
// started from. A few megabytes that rarely change are what each session
// holds on to instead of the whole dashboard, once per upgrade.
package main

import (
	"fmt"
	"os"

	"github.com/Wayy01/Just-Dashboard/backend/internal/ptyhold"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: jd-terminal-holder <socket>")
		os.Exit(2)
	}
	if err := ptyhold.Run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
