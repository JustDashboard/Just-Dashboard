# Processes, terminal, and GitHub

## Processes

`internal/procs/table.go` is a live inventory rather than a thin `ps` rendering. The kernel's cgroup
membership identifies systemd services, containers and login sessions; an empty command line identifies a
kernel worker; PM2's own PID list is overlaid by the handler because a PM2 child otherwise inherits its
daemon's systemd cgroup. **Names are not used to guess ownership** — the same executable started by a
service and by a shell has a different remedy. Unknowns stay `unmanaged`, which is information rather than
a failed detection.

- Process disk counters are cumulative in `/proc`, so `Table` keeps one small, mutex-protected previous
  sample per PID and returns rates. The create timestamp participates in the identity because Linux reuses
  PIDs; a replacement starts at zero rather than inheriting the old process's apparent I/O spike.
- Search, user/state/manager filters and sorting all run **before** the response limit. The response says
  matched, available and truncated separately and carries facets from the complete snapshot — cutting
  first made the old promise that filtering could reach the rest of the table false. That richer response
  is `/processes/inventory`; `/processes/` keeps its original array shape for API clients.
- Automatic focus is a frontend decision over the live host snapshot: blocked work, iowait or I/O pressure
  selects process disk rate; low available memory or memory pressure selects RSS; otherwise CPU. The page
  says which and why, and an operator's explicit focus/refresh/row-count choice is kept in `useViewState`.
- A signal or priority request carries the process's create timestamp. The server re-reads the PID and
  returns `process_replaced` if it now names something else, so a row left on screen cannot act on a reused
  PID. Signals remain destructive and confirmed; changing `nice` is reversible, audited, and
  `system.admin`. PID 1 and the dashboard's own process remain refused in the backend.
- The detail sheet exposes identity, cwd/executable links, resource counters and controls without returning
  environment variables (process environments routinely contain secrets). PM2 can gracefully reload and
  `pm2 save` persists the current list for an existing startup hook; it does not install or rewrite that
  platform-specific hook. The systemd sheet reads effective runtime properties beside the journal and
  links to the unit file; static units do not get an enable/disable control they cannot use.
- PM2 discovery is per host account. It inspects each mounted home for a daemon directory or a PM2 binary,
  prefers the newest nvm installation, and invokes the host command with that account home as `HOME` and
  `PM2_HOME`. Lists combine every discovered daemon, controls return to the daemon that owns the named
  process, and `pm2 save` persists each account separately. Commands still use explicit argv through
  `hostexec`; request data never becomes a shell string.

## The terminal

`internal/term` runs direct PTYs. Three properties are load-bearing:

**Every window is a direct PTY.** There is no persistent-session choice and no pane/split layer. A
dashboard session is an in-memory workspace grouping independent PTYs as windows; each window therefore
keeps native terminal capability negotiation and ends with the dashboard process. Closing a session ends
all of its windows, while closing one window leaves its siblings running.

**`su -l` cannot open a shell in a chosen directory**, because login *is* chdir-to-home; tmux's `-c` is
not enough, since su walks straight back out. `loginArgv(shell, keepCWD)` moves the chdir off su onto the
shell: `su -s <shell> <user> -- -l` switches user without `-l`, and the `-l` after `--` reaches the shell
and still reads the profile. The other half is `hostexec.CommandOnHostInDir`.

**A requested directory is validated on the host, never with `os.Stat`.** The dashboard process sees the
container's filesystem, in which only `/home`, `/opt`, `/srv`, `/root`, `/etc` and a few others are the
host's, while the shell starts in the host's mount namespace. A local stat was wrong in both directions:
a host-only path such as `/var/www` was reported as missing and the new window silently fell back to
home, and a container-only path such as `/usr/lib/postgresql` passed and then made `nsenter` fail, killing
the new PTY on arrival. `hostDir` runs `test -d` through `hostexec.CommandOnHost`, which crosses when
containerised and runs locally otherwise. A new window inherits the directory of the window the operator
was looking at, falling back to the workspace's first window and then home; because every step is
validated, a stale directory can only send the new window home, never kill it.

**Session organisation is intentionally lightweight.** `GET /terminal/` groups live `Session` values by
`WorkspaceID`; naming, folder membership and pinning are copied across the workspace's windows in memory.
Folders remain the dashboard's ordered record (`handlers_terminal_folders.go`, settings key
`terminal.folders`), while membership stays on each workspace. There is no session/window colour model.
Renaming a folder moves every matching workspace in one request. Window routes use opaque PTY ids and
support create, rename, reorder and close. Selecting a window is client state: the page keeps each visited
window's emulator and socket alive while hidden, preserving the complete terminal stream. A new browser
attachment still receives only the bounded best-effort history, not an independent screen snapshot.

The create request carries a provisional size because the emulator does not exist yet. The
attach WebSocket carries xterm's measured `rows`/`cols` in its query. The handler subscribes first, then
applies that size: `TIOCSWINSZ` may produce an application redraw synchronously, and subscribing after
it would lose the first bytes of the only screen a new browser needs. Every later `ResizeObserver` fit
sends only a changed cell size. Reconnect uses `SynchronizeSize` to reapply the size even when the cached
fields agree; ordinary resize frames are de-duplicated. The recorded size changes only after `pty.Setsize`
succeeds. Terminal capability variables replace inherited entries rather than being appended — duplicate
names are legal in `execve`, and appending could leave an inherited `TERM=dumb` as the value libc returns.

## GitHub sign-in

`internal/ghx` exists because the honest answer to "why did my push ask for a password" used to be an ssh
session.

- **Everything is per repository.** gh stores its token under the home of whichever account runs it and
  writes a credential helper into that account's git config; gitx already runs git as the account that
  *owns the checkout* (`hostexec.AsOwner`), so ghx runs gh the same way. Sign in as root, push as
  `deploy`, and the push is anonymous again. Every route takes `?path=`.
- **gh is in the image, not borrowed from the host** — the host's copy runs as the host's root in the
  host's namespaces, and the account that pushes would see neither token nor helper. From this image both
  land in the same account's home, bind-mounted, so ssh finds the same credential.
- **The login is the CLI's own device flow, performed here.** `gh auth login` is a series of prompts and a
  web request has nobody to answer one, so `device.go` runs the OAuth device flow against the GitHub
  CLI's public client id — which is what makes the token indistinguishable from one gh minted, and what
  the operator sees on the authorisation screen — then hands it to `gh auth login --with-token`. The
  device code stays server-side and the token never reaches the browser; the page holds an opaque flow id.
  The polling interval is enforced from the flow's own clock, because GitHub's remedy for polling too fast
  is to slow the whole flow. `LoginWithToken` is three steps that are one operation (store, `gh auth
  setup-git`, write a committer identity if missing) — any two without the third is a state nobody can
  see: a token with no helper pushes anonymously, a helper with no identity fails at the commit.
- **`gh auth status` is parsed, because it has no `--json` and never will.** It is written for a person,
  so the wording is the contract; `parseAuthStatus` matches both wordings gh has shipped and `ghx_test.go`
  pins them. Every field is optional, so a rewording costs a scope list rather than the page.
- **Pull requests are the one thing git has no verb for.** `CreatePull` shells to `gh pr create` and the
  handler pushes the branch first, since gh refuses an unseen branch and its remedy is an interactive
  prompt. That is also why `gitx.Push` sets the upstream itself rather than repeating git's advice.
  `gitConfigured` answers "would a commit and push from this page be this account's" with one dot, and
  knows an **ssh** remote never consults a credential helper.
