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
  environment variables (process environments routinely contain secrets). `Detail` also reads what the
  process listens on and how many connections it holds (`sockets` in `detail.go`, from gopsutil's
  per-process connection list) and its soft `NOFILE` limit, so an open-file count is a warning rather
  than a number; a snapshot leaves those empty because reading every process's sockets on each poll
  costs more than the table. `GET /processes/{pid}/tree` returns the parent chain (outermost first)
  and direct children from one pass over the table, because the remedy for a runaway worker is
  usually its supervisor. Signals are the existing `POST /processes/{pid}/signal`; the page offers
  SIGTERM, SIGKILL, SIGSTOP/SIGCONT and SIGHUP as words with a sentence each. PM2 can gracefully reload and
  `pm2 save` persists the current list for an existing startup hook; it does not install or rewrite that
  platform-specific hook. The systemd sheet reads effective runtime properties beside the journal and
  links to the unit file; static units do not get an enable/disable control they cannot use.
- `GET /pm2/` also carries `daemons`: per account, when `~/.pm2/dump.pm2` was last written and whether a
  `pm2-<user>.service` boot hook exists (a stat under the host's `/etc` and `/lib`); the page states
  both, because a daemon with three online applications and no saved list restores nothing. The
  per-process verbs grew `reset` (restart counters, `service.control`) and `flush` (truncates the log
  files, destructive); `POST /pm2/{name}/scale` (`{instances}`) runs `pm2 scale <name> <n>` and
  refuses a fork-mode application; `POST /pm2/daemons/{user}/{start|reload}` and, destructive,
  `/{restart|stop}` run the verb with `all` against one account's daemon. Flush is addressed by name
  because every PM2 release matches a flush target by name and cluster instances share their files;
  everything else is addressed by numeric id as before. `POST /pm2/start` (`PM2StartRequest`) runs
  `pm2 start` with one argv element per field — script, `--name`, `--cwd`, `--interpreter`, `-i`,
  `--watch`, `--max-memory-restart`, then `--` and the arguments — and hands an ecosystem file
  (`*.config.js`, `*.json`, `*.yml`) to PM2 whole with at most `--only <name>`. It requires
  `system.admin`: it runs an operator-named file as a host account, which is code execution rather
  than service control. Paths must be absolute, the interpreter a known runtime or an absolute path,
  and every value is validated before it becomes an argument (`pm2StartArgs`, tested without a daemon).
- PM2 discovery uses host `/etc/passwd` accounts and their mounted homes. Numeric version ordering
  selects the newest nvm installation. `hostexec.CommandOnHostAsUser` enters the host namespaces,
  then uses `setpriv` to switch UID/GID and supplementary groups before loading the account's PM2
  executable. No dashboard environment is inherited, privilege escalation is disabled, and command
  output is bounded. The host must provide `/usr/bin/setpriv`; failure never falls back to root.
- Each PM2 row carries trusted `daemonId` (the Linux username) and numeric `id`. Controls and logs pass
  `?user=<daemonId>&id=<id>`; ambiguous name-only calls are refused. Controls refresh the process list,
  verify the name/account/id tuple, and invoke PM2 with the numeric id. `pm2 save` visits each account.
- PM2 log filenames cannot grant access outside `JD_LOG_ROOTS`. An administrator must explicitly
  configure custom log directories; the source list and stream errors explain this requirement. Unified
  log source ids carry account, numeric id and name, while unique legacy name-only ids remain accepted.
- systemd grew `reset-failed` (`service.control`) and `POST /systemd/daemon-reload` (`system.admin`),
  and `GET /systemd/timers` joins `list-timers --all` (schedule) with `list-units --type=timer` (state)
  and `list-unit-files --type=timer` (startup) on the unit name. `next` and `last` are read as
  microsecond timestamps; `left` and `passed` are ignored because across systemd versions they have
  been a string, a duration and — on some 257 builds — a copy of `next`. Timer control reuses the
  unit routes, and "run now" is `start` on the service the timer activates. The unit sheet offers
  reload only when `systemctl show` reports `CanReload=yes`.
- User cron inventory and edits run the host's `crontab` through `hostexec.CommandOnHost`.
  Per-job enable, disable, edit, add and remove are edits to the crontab's text made in the browser
  (`lib/crontab.ts`: only the job's own line and the comment the parser attached to it change) and
  written back through the same wholesale `PUT`, so the row controls and the text editor are two
  views of one file. Schedules are described and their next runs computed in `lib/cron.ts`, in the
  browser's clock; the page says so. There is no "run now" for a cron line: a crontab command is a
  shell string, and running it would mean a request-built `sh -c`, which invariant 4 forbids. Writes use
  stdin (`crontab -u <user> -`), so a container-only temporary file or spool cannot receive a host job.

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
support create, rename, reorder and close. Selecting a window is client state, remembered per browser:
the page keeps each visited window's emulator and socket alive while hidden, preserving the complete
terminal stream, and tells the server which window it is showing with a `focus` control frame — the
socket itself stays open while a window is hidden, so attaching says nothing about what is on screen.
A new browser attachment still receives only the bounded best-effort history, not an independent screen
snapshot. Unnamed sessions and windows are "Terminal", numbered from 2 when that is taken (`freeName`);
`SessionMeta.Named` and the window's `named` record whether the operator chose the name, because a chosen
name is shown as given while a default gives way to what the window is doing.

**What a window is doing is read off the PTY, not asked of the shell** (`activity.go`). Two facts, both
available without touching the account's shell configuration. The title is parsed out of the byte stream
by the read loop as OSC 0/2 — BEL- or ST-terminated, across chunk boundaries, capped at 512 bytes, other
OSC kinds skipped. Whether anything is running is `TIOCGPGRP` on the PTY master: the process group holding
the terminal is the shell's own at a prompt and the job's while one runs, which is the fact job control is
built on, and Linux answers it for a master precisely so a terminal program can ask. The group's leader is
read from `/proc` and judged — a shell with nothing to run (no script, no `-c`) is a prompt, anything
else is a program, and wrappers such as `sudo -i` are looked through to their innermost descendant so a
root shell at a prompt is idle. When the leader has already exited (`cat big | less`) the job is whichever
of the shell's children still carries the group. The program's name is what the operator typed:
interpreters (`node`, `python3`, `bash -c`) and wrappers (`sudo`, `timeout`) are looked through, a script
loses its path and extension, and a tool run from `node_modules` answers with its package, so
`node /usr/local/bin/claude` is "claude". A title counts only while the group that set it still holds the
terminal, which lets a program that names itself win, one that does not fall back to its process name, and
the prompt's directory title return the moment the job ends — the bundled prompts set that title (`\W`,
`%1~`). A group that holds the terminal with no live process left in it is the instant between a job
ending and its shell taking the terminal back; that reading is a transition, not an idle prompt with no
title, and what was last published stands until the shell is back.

Holding the terminal is not working, and the difference is what the marks are for. A job is announced
(`busy`) only once it has lasted a second (`holdOff`): `ls` holds the terminal for milliseconds, and a
tab that switched its name for every one of those would flicker all day. `working` is true only while
something is actually happening — output has been arriving for a second (`sustain`) and the last of it
is less than 2.5 s old (`quietAfter`), or the job is using at least 5% of a core over a tick (its leader,
its reaped children and its live descendants, from `/proc/<pid>/stat`). Output within 150 ms of a
keystroke (`echoWindow`) is its echo, or an editor redrawing its input line, and does not count. An
editor or an agent waiting at its prompt is therefore busy and not working; an agent streaming an
answer, a build or a test run is working, and a compiler that prints nothing is caught by its CPU.
`finishedAt` stamps the end of a stretch of work and is kept until work starts again; the browser
decides how long to show it. The read loop observes at most every 100 ms during output and once more
350 ms after it stops, because the echo of the Enter that starts a command arrives before the shell has
handed over the terminal; while a job is in the foreground, or output has just gone quiet, a 500 ms tick
keeps looking, so a silent job's CPU and the end of a run are noticed without output; the listing and
window endpoints observe on request. Every change reaches attached browsers as a `state` control frame
(`{title, busy, process, working, finishedAt}`), sent once after the replay and then on change, over a
per-subscriber event channel beside the output channel so the byte stream is never interleaved with it.
`GET /terminal/` carries `named`, `busy` and `working` (any window), `finishedAt` (the latest) and
`current` (the window last focused, else the newest) per workspace; `GET /terminal/{id}/windows` carries
`named`, `title`, `busy`, `process`, `working` and `finishedAt` per window.

The create request carries a provisional size because the emulator does not exist yet. The
attach WebSocket carries xterm's measured `rows`/`cols` in its query. The handler subscribes first, then
applies that size: `TIOCSWINSZ` may produce an application redraw synchronously, and subscribing after
it would lose the first bytes of the only screen a new browser needs. Every later `ResizeObserver` fit
sends only a changed cell size. Reconnect uses `SynchronizeSize` to reapply the size even when the cached
fields agree; ordinary resize frames are de-duplicated. The recorded size changes only after `pty.Setsize`
succeeds. Terminal capability variables replace inherited entries rather than being appended — duplicate
names are legal in `execve`, and appending could leave an inherited `TERM=dumb` as the value libc returns.

Legacy tmux compatibility tests use a private `TMUX_TMPDIR` and clear inherited `TMUX`. Socket-directory
setup failures stop the suite before any tmux command or cleanup can reach the operator's server.
The private server uses an empty config and stays alive until package cleanup, avoiding tmux's
last-session exit racing the next test's connection.
Startup-timeout failures include bounded PTY output to distinguish launch failures from slow discovery.

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
  `ListPulls` takes a state (open, closed, merged, all) and folds each request's review decision and
  status-check rollup into one word each — one failing check outranks any number of passes, one pending
  outranks passes — so the row can say "checks failed" without a second call; `ViewPull` adds what
  GitHub computes lazily (mergeable, additions, deletions, changed files, body). `MergePull` runs
  `gh pr merge` with merge, squash or rebase and an optional `--delete-branch`; `CheckoutPull` runs
  `gh pr checkout`; `ListRuns` reads `gh run list` for a branch. Merging and checking out sit under
  `service.control` like every other recoverable git write, and each is audited with the request number.
  `gitConfigured` answers "would a commit and push from this page be this account's" with one dot, and
  knows an **ssh** remote never consults a credential helper.
