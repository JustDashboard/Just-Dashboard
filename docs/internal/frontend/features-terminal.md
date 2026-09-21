# Feature panels and terminal workspace

## Feature panels

**Docker** (`components/docker/`) is where the product's opinion about newcomers lives.

- `explain.tsx` is the teaching layer — `Hint`, `Term` (a dotted underline, definition one hover away),
  `Field`, `GLOSSARY` — written for somebody who has never run a container and phrased around the decision
  rather than the mechanism. The rule it enforces is that **explanation is quiet**: a form that shouts
  every caveat is as unusable as one that explains nothing.
- `create-container.tsx`'s three entry points matter more than the form, and they are the *first* thing
  on the panel rather than three stacked sections: three cards on one line, and only the route you picked
  spends any height. **Paste a command** covers the common case: `lib/docker-run.ts` parses leniently and
  returns three things — the spec, warnings about what it could not read, and `unsupported`, the flags it
  *understood* and the form has nowhere to put. The last is kept separate on purpose. `--gpus all`
  dropped without a word produces a container that starts and then has no GPU, and the operator finds out
  from the application failing; the panel says so and keeps the original command beside the form.
  **Start from a template** is `lib/docker-templates.ts` — images almost every server runs across
  five categories, every one bound to 127.0.0.1; a set of starting points, not an app store, which is
  what goes stale and becomes the maintenance burden in Yacht and CasaOS. A template's setup
  requirement is one word on its card and a full notice at the top of the form the moment it is picked,
  which is the only place the instruction can actually be followed. **Custom** is for people who
  know what they want. The Command tab shows the server-rendered `docker run` and compose, live. Port
  bindings are offered as *who should reach this* rather than as an address, populated from the machine's
  real interfaces so the LAN option names one rather than gesturing at the idea.

  The form's explanations are `?` hover cards on the section headings, the field labels and the
  behaviour switches, not paragraphs printed beside them. Six sections of three lines each is eighteen
  lines of prose around twelve controls: an expert scrolls past all of it every visit and a newcomer
  reads it once. Prose survives in exactly two places — a warning that depends on what you just typed
  (a missing image tag, a port on every interface, an unnamed volume) and the `privileged` switch, which
  can hand the server away and should not need a hover to say so.
- `run-console.tsx` deliberately **does not** let `useSocket` reconnect: reconnecting re-issues the GET,
  and re-issuing the GET runs the command again — a redeploy fired twice because a VPN blinked is not a
  re-render. A dropped socket ends the run and says so.
- `attention.tsx` renders the half of the diagnosis that is not runtime, through the product's one
  `FindingList` (`components/finding-list.tsx`) rather than through a parallel component of its own.
  Docker had grown a second one — a two-line title-and-detail row with a glyph, a tag and a chevron —
  while Metrics and Security shared a one-line accordion for the identical idea, so a page showing both
  read as two products. What is left here is the Docker-specific part: repeats of one kind collapse to a
  single row ("9 containers have no memory limit") whose body states the shared reasoning once and names
  the containers as chips that run that container's own remedy. The severity filter strip, the "N
  distinct" counter and the Hide button are gone — four chips and two counters framing a list capped at
  five rows. `ContainerFindings` filters a diagnosis pass for one container; the containers page passes its own,
  and the container's page asks for one.
  `RuntimeHealthPanel` is the other half and counts what is *fine* as well as what is not, because a
  list of problems can never say "eight healthy, four with no health check at all" — and that last
  number is what stops "all healthy" meaning "nothing is being watched".
- `tabs.tsx` gives selection, hover and focus three different mechanisms. They had two: the accent
  outline meant both "this filter is on" and "the keyboard is here", so a keyboard user could not tell
  which filters were applied and tabbing looked like the selection moving.
- `exposure.tsx` draws a published port so the two cases that matter look different, and `RouteRow`
  shows the full trace — binding, reverse proxy, firewall — with the reasoning behind each verdict and
  an explicit *inferred* where one was worked out rather than read.
- `container-cells.tsx` holds the three cells that were saying something other than what they meant:
  memory showing host RAM as a limit nobody set, CPU with no denominator stated (Docker counts one core
  as 100%), and a Status column carrying the worst security finding underneath the runtime state.
- `cleanup.tsx` and `deploy-preview.tsx` are the two "before you press it" panels: what each category of
  removable object costs, and what a compose deploy is expected to change — including the sentence about
  volumes, stated whether or not any are affected. The preview's services are a flat list, not
  collapsibles: a service that changes gets a row with the server's reason and the images before and
  after, while the unchanged rest collapses to one line naming it — the same rule the attention panel
  follows when one finding repeats across several containers, and the reason the section is not five
  copies of "its configuration is identical". The "compose makes the final call" caveat is stated once
  underneath. A cleanup category is one line — name, size, count, in
  fixed columns — with the cost sentence and the example names behind its `?`. It used to be a block
  whose height depended on how far the cost sentence wrapped and whether that category had examples, so
  the six rows came out at three different heights and the size and count, top-aligned against the
  tallest, never lined up with each other; four of the six were "Nothing in this category" at half
  opacity and were the tallest thing on screen.

Panel headers carry no icon plot. A tinted square in front of every title is chrome repeated once per
panel, and on a page that is eight stacked panels it reads as a column of brand marks rather than as
eight headings. It started here and now holds product-wide — the prop is gone from `PanelHeader`,
`Modal`, `SidePanel` and `Section`, and the title sits at `text-title` instead
(`docs/internal/frontend/design-system.md` §14). The `?` stays: it is the one mark on those headers
that does something.
- `stacks-tab.tsx` is the stack list, as rows in one plain panel rather than a grid of bordered cards:
  each row carries the stack's state, its services (dot, name, ports, health) and the one action that
  belongs there — deploy when the application is down. It opens with the same search box and state
  chips as the containers page, because "which of these is down" is the same question asked of the same
  server. A stack's own page follows the same rule as a container's: the compose verbs that are pressed
  daily (deploy, restart) sit inline, and the rest are behind one overflow menu where each gets its word
  and its sentence — the menu item's word-and-sentence body is `MenuItemBody` in
  `container-actions.tsx`, shared with the container menu. Its services tab, the deploy preview's
  service rows and the deployment history are hairline lists the eye reads down, not stacks of bordered
  rows; the preview's verdict and the network panel's shape diagram are the two `Group tinted` fences
  the section keeps, and a diff or a captured compose file sits in a `Well`.
- `stack-detail.tsx` is a stack as the application it is: clickable ports, the compose file editable in
  place (validated before saving — and saving is *not* deploying, which the UI says), one merged log feed
  tagged by service, links to Files, git and a shell in the stack's directory. `container-detail.tsx` adds
  the reachability join (published port + the proxy site pointing at it turns "running on 3000" into a
  URL), the writable-layer investigator, the failure diagnosis, editable limits, raw inspect, and
  Update/Duplicate/Rename — the last two behind a statement of consequence when compose owns the
  container, because the next deploy silently undoes them days later. Its Storage tab leads with the path
  *inside* the container — the one the application's own configuration names — states the kind of storage
  in words rather than as a Docker noun, and puts where it actually lives on the second line; the header
  answers the question the tab is opened with, which is how much of this survives a rebuild. Its Usage
  tab drops the network chart entirely for a container on the host's network namespace: Docker reports no
  per-container interface there, and a chart-shaped hole explaining itself beside a real chart draws the
  eye first to say "nothing here". `build-dialog.tsx` is where the git panel and Docker stop being two
  products: a repository we already pull is a build context.

Four deep links are worth preserving: `/files?path=`, `/git?repo=`, `/terminal?cwd=`, `/audit?action=`.
All but the terminal one are read once as an initial value rather than kept in sync — the URL is where
the reader arrived, not where they are now — and the terminal one opens a session exactly once per
mount, because a shell is a process. The audit link is how the Docker event feed hands off a correlated
event; landing on an unfiltered list of everything the dashboard has ever done is not the entry it
promised.

A container and a stack are their own destinations — `/docker/containers/<id>` and
`/docker/stacks/<name>` — since 2026-09-21: each holds a live log feed, and the container a shell and
the stack a compose editor, which is the test for a page rather than a sheet (`shell-design.md`). The
container takes `?tab=` so a link can ask its question — "show me the logs" lands on the logs. The
`?container=` and `?stack=` addresses those pages replaced still resolve, by redirect. Deployment
runtime rows use these handoffs, including when a container disappears between the observation and
the click: the page reports it, and a container removed from its own page returns to the list.

`useQuerySelection` still keeps the remaining sheets' selections in browser history so reload and
back/forward restore them; closing clears only that selection parameter.

**Security and proxy** (`components/security/`, `components/proxy/`) follow the same rule — teaching next
to the control, not in a banner above it. `posture-panel.tsx` turns a finding's `fix` into a button and
maps it to the request plus the confirmation it deserves. `rule-form.tsx` is why the catalogue lives on
the server: picking "Redis" fills in 6379 *and* raises the warning at the moment of choosing, not in a
report afterwards; the `ufw` line it would run is in the footer. `ssh-panel.tsx` stages every change and
applies them together, and its dialog says to keep the session open and check a second terminal — the one
piece of advice no error message can give afterwards. `site-form.tsx` shows the server-rendered nginx live
beside the form (same renderer that writes the file, so the form is not a black box), with `DNSCheck`
under the domain field because "the name does not point here yet" causes most certificate failures and
certbot reports it as "challenge failed". `tls-report.tsx` says out loud what `unknown` means for a
protocol row.

The security section is eight pages of one shape, the host Overview's: a page header carrying the
area's verdict, a run of `StatTile` readings on the page's own ground, the findings about that area
under them (`AreaFindings`, plain), then the detail as plain tables that bleed to the page edge. The
overview opens on how this browser reaches the panel — the exposure grade, the allowlist, the tunnel
interfaces and **the address you arrived from** (`Exposure.client`) as a row of facts — then five
tiles, one per area with a figure, each a `StatLink` to its page, and the whole posture as one findings
list. There is no second list of the areas: the rail's Security panel already names every page, and the
tiles carry the verdicts that used to sit beside seven links. The rules it follows are the ones the
[design system](design-system.md) states, plus four of its own:

- **An address is the same thing on every page.** A remote peer on Connections, a repeat offender in
  the ban log and an attacker in the failed-login record all take the verbs in `address-verbs.tsx`:
  *block at the firewall* inline (a source-only deny the server puts in front of every allow, omitted
  for a role that cannot write rules and for a private peer), and behind the menu the three lookups —
  who owns it, reverse lookup, trace the route — each of which opens the Tools page narrowed to that
  probe with the address filled in (`?tool=asn&target=…`). Arriving never runs the probe; a probe is
  traffic this server sends to an address, and the run stays a press.
- **The jail sheet can ban, and the tuning dialog knows who you are.** `POST /fail2ban/{jail}/ban`
  existed since the jail controls shipped and nothing on the page reached it; the sheet has the input
  now, with the server refusing the caller's own address. The tuning dialog's allowlist offers "Never
  ban my address" — the exposure's `client` — because banning yourself is the commonest way to lose a
  server you were in the middle of hardening, and its notice says what actually happens: applied to the
  running fail2ban and written to `jail.d/99-just-dashboard.local`, not "lost at the next restart".
- **Logins folds btmp into attackers.** Five hundred lines of the same three addresses answer nothing;
  `GET /logins/attackers` (admin, like the listing it is folded from) gives each address its attempts,
  the account names it tried most — "root, admin, ubuntu" is a scanner, one real name is somebody who
  knows the host — and the block beside it.
- **A standing fact is not a banner.** Text that never changes — the firewall's lockout guard, "a probe
  answers outward, not inward" — is stated *once*, in the footer of the thing it qualifies or at the top
  of the page it applies to, never repeated per block. Only a `Notice` that explains why the control
  under it will refuse (sshd with no key on the host) stays above the control, and it sits under the
  readings rather than above them. The firewall's defaults are readings in tiles and, where the backend
  can take an instruction, controls in one plain "Defaults" block — a read-only host gets the tiles and
  no row of dead controls under them.

`tests/browser/security-ui.spec.ts` drives all eight pages against a mocked API: the readings each
page draws from the shapes the backend sends, the joins above (the block a row sends, the ban the sheet
sends, the allowlist the tuning offers, the prefill Tools arrives with), that no block on any page is
framed, that every icon-only control is named, that row controls are reachable without a pointer, and
that nothing scrolls sideways on a phone. Set `JD_SECURITY_SHOTS` to a directory to have it write
review screenshots of every page at 1280 and 1720 wide.

**Packages.** `install-panel.tsx` updates as you type, which is not decoration: the reason people open a
terminal instead of a package page is that they do not know the name (`postgresql-client`, not `psql`;
`build-essential`, not `gcc`), and a form where you type a guess and press a button to find out you were
wrong is a form you use once. Install is one press on the row — there was a tray, and it cost a click and
a concept on every single-package install while protecting against an interruption a job survives anyway.
The command each button runs is its `title`. `package-sheet.tsx`'s second tab is the whole point of the
feature; the installed table caps at 400 rendered rows with the count said plainly underneath. The page
is drawn per design-system §15: the search is a plain panel of hand-laid rows (`ROW_BLEED`) whose
install verb is an outline button — sixty brand faces in a result list would be sixty commands — and
a started install is a `Status`, not a disabled button; the sheet's usage sections open with an eyebrow
alone, and its copyable commands sit on the control ground.

## The terminal panel

`components/terminal/` is the session rail and window strip; `components/xterm-pane.tsx` is the emulator. The
split matters — the pane is reused by the compose runner and knows nothing about sessions.

- The page is **one framed workbench**. The rail, the emulator and the Files/Git column are `Pane flush`
  inside a single `rounded-xl border` wrapper, separated by a hairline each (drawn on the rail's right
  edge and the tools column's left edge). Three framed panes with a gutter between them read as three
  boxes floating on the page; the screen is one working surface. Immersive mode drops the wrapper's
  frame along with the page. Below `lg` the rail and the tools column **cover the emulator** inside the
  frame instead of sitting beside it — stacked over and under it they left a phone's terminal one line
  tall — so only one of the two is up at a time (`useMediaQuery` in `page.tsx` decides), picking a
  session puts the rail away, and each carries its own hide button while it is an overlay.
- `session-rail.tsx` is a plain column: a "Sessions" strip with the two new-buttons, then the list. A row
  is one line — the session's label and, at the end, the activity mark (below). Rows carry no terminal
  glyph (every row is a terminal) and no directory line under the title (the label carries the
  directory at a prompt). Rows are plain rounded rows with no fence or divider between them; the
  active one is `bg-accent` and the others take the row hover, and that is the whole difference —
  no brand bar or other colour on the active row. The filter box
  appears only once there are more than five sessions — a filter over one session is a box with
  nothing to do. Folder headers are plain disclosure rows: chevron and explanatory name only, with no
  folder icon, count, nested container or empty invitation. Pinning still sorts a session to the top
  of its folder.
- **Names follow the shell.** An unnamed session or window is "Terminal" (numbered from 2 when that is
  taken), and that default is only a fallback. A tab is labelled the way a desktop terminal's title
  bar is: the title the foreground program set through OSC 0/2, the program's name when it set none,
  and the directory at a prompt (the bundled prompts set that title). The glyph an agent animates in
  front of its title — Claude Code's asterisk, Codex's half-moon — is dropped from the label
  (`plainTitle`): saying "working" is the activity mark's job, and a label that carried both said it
  twice. A session is labelled after its
  *current* window — the one last on screen in any browser (the pane sends a `focus` frame), or
  failing that the newest. A name the operator typed, for a session or a window, is shown as typed
  (`named` in the API), so renaming still works as it did. `lib/terminal-activity.ts` holds the rule
  once for the strip and the rail. The state arrives two ways: polled with the window list and the
  session listing (every five seconds), and pushed over each visited window's attach socket as a
  `state` frame the moment it changes, which is what makes a tab's mark and title move with the
  shell rather than with the poll. The server side is in
  [`processes-terminal-github.md`](../backend/processes-terminal-github.md#the-terminal).
- **The activity mark** (`activity-mark.tsx`) says what is happening in a window, and it is the
  product's own vocabulary rather than a spinner: a breathing `StatusDot` — "this is live" — while the
  window is *working*, and a check once it has finished. It is drawn on every window tab and, for
  the session, on its row in the rail. The mark's box is one fixed size whatever it holds and is
  present even when empty, so a tab sized to its label does not grow and shrink as a dot becomes a
  check or a mark appears. Working is the server's word for "output has
  been arriving for a second and still is, or the job is burning CPU": an agent streaming an answer,
  a build, a test run. A program that is merely open — an editor, Claude Code or Codex waiting for
  the next message — holds the terminal and gets no mark, because nothing is happening; that is
  exactly when an agent's own title glyph goes still, and the tab follows the same signal. Nothing
  is announced for a command over in a blink (`ls`), so the label never flickers. The finished check
  is a notification: `useFinished` keeps it on a tab or row you are not looking at until you go
  there, and shows it for four seconds on the one you are, after which that finish counts as seen
  (`terminal.viewed` in `view-state`, keyed by session or `session/window`, pruned with the sessions).
- **The page remembers where you were.** Which session was open, and within each session which
  window, live in `view-state` (`terminal.session`, `terminal.windows`) rather than in component
  state, so leaving for another page and coming back lands on the same window rather than on the
  first session's first one. It is the one selection that store keeps: a terminal's selection is the
  tab you had open, which is furniture. Entries for sessions that have ended are dropped as the
  listing arrives.
- `window-strip.tsx` places compact, horizontally scrolling direct-PTY tabs between exactly two workspace
  toggles: sessions on the left and Files/Git on the right. The strip is embedded in the emulator's own
  title bar; there is no separate workspace bar or working-directory/shell title.
  A tab is its label with the activity mark's slot in front of it, lit while the window is working or
  has just finished: rename and close sit on the tab but appear under the pointer (`rowReveal`), the
  way a browser's do; the active tab keeps its close visible. Double-click renames. There are no split,
  layout or colour actions. Closing the last window closes its session through the session endpoint.
- The control-key row under the emulator is a run of monospace words on the footer strip, not framed
  keycaps.
- `workspace-tools.tsx` is the Files/Git companion. Its header is two section tabs (`tabClasses`, the
  brand underline, no glyphs) with the changed-file count beside "Git". Under that, the Files half is a
  strip with the root path (middle-truncated), **new file** and **refresh** inline, and hidden files /
  new folder / open in Files behind one menu — `file-tree.tsx` draws that strip; the Files page's sidebar
  drops it (`chrome={false}`) and draws its own place switcher above the same tree, which there also
  reveals the folder being browsed, reloads its open folders on `refreshTick`, and takes drops. The Git half (`git-tools.tsx`) is two strips before content: the repository's
  reading (branch, `detached` tag, ahead/behind, the GitHub account) with **pull** and **push** inline
  and fetch / stash / pop behind one menu where each verb carries a sentence (§13). That strip stays
  one row at every width and the branch is what it keeps: under 400px the account shows its avatar
  alone, and under 320px (the column's minimum, or a phone) pull and push join the menu rather than
  being pushed off the edge. The commit row under the changes wraps its two buttons under "Amend"
  when the column is too narrow for all three. Then a `FilterChip`
  row switching Changes / History / Branches, with a `+` for a new branch on the Branches view that
  opens an inline create row rather than a permanent form. Changed-file rows colour only the status
  letter; the list is all changes, so a tinted band on every row said nothing. A file staged and then
  edited again is listed under both headings with the letter for each side (`lib/git-status.ts` takes
  the side), and discarding an untracked file deletes it and says so. Clicking a changed
  file shows its diff, untracked files included — the server diffs those against nothing, so a new
  file reads as the addition it is, and the file viewer's Diff toggle does the same. Branches are
  grouped: local first, remotes under their own label, no per-row glyph and no "remote" word on every
  line.
  The emulator toolbar keeps search, snippets, appearance and fullscreen visible, with copy, export,
  folder navigation, shortcuts and clear in Terminal actions. Text size lives in Appearance.
  Input stays in the shell: there is no separate composer or Workspace/Focus mode. Bundled Bash and
  Zsh startup files install a compact directory/chevron prompt — which also sets the window title to
  the directory (`\W`, `%1~`), the title the tab shows at a prompt — and native Tab completion in new
  windows.
  The Zsh startup also loads the host's `zsh-syntax-highlighting` and `zsh-autosuggestions` packages
  when present (Debian's `/usr/share/<plugin>` or Arch's `/usr/share/zsh/plugins/<plugin>`), guarded so
  an account rc that already loaded one is not wrapped twice, and sets a history file, `HISTSIZE` and
  `SAVEHIST` only when the account left them unset — a bash-by-default account has no `.zshrc`, and
  suggestions drawn from history need one. `install.sh` installs those packages and writes
  `JD_TERMINAL_SHELL` so a fresh install has the ghost text without touching the account's login shell.
  Account profiles and interactive configuration still load; account dotfiles are never edited.
  `term.SetupShell` atomically installs readable scripts in the process-owned shared terminal root's
  `.shell` directory, rejecting symlink or foreign-owned directories. A constant login bootstrap passes
  shell and startup paths as positional arguments; unsupported shells retain their ordinary startup.
  Existing running shells are not modified.
  The terminal host is absolutely inset into its output region so its own rows cannot grow its parent.
- `ResizeHandle` is an invisible eight-pixel hit target over each panel's own border. The border is the
  visual affordance, so the layout draws no extra divider. Arrow keys and double-click reset remain the
  non-drag alternatives.
- `lib/terminal-settings.ts` keeps scrollback and behaviour in localStorage — on the screen, not the
  account, because it belongs to the machine you are sitting at. Font metrics are deliberately fixed: user-selectable line height,
  spacing and fonts made the emulator grid cease to be a stable terminal grid.
- `lib/terminal-keymap.ts` is every shortcut, all rebindable. A chord must get past the browser, the page
  and the shell, and no default suits everybody. Ctrl+Alt is the default family (neither browser nor shell
  wants it); Ctrl+Shift is the emulator's own. Matching is on `event.code`, the **physical** key, so a binding recorded on QWERTY
  survives a Romanian layout. Actions carry a **scope** — `navigation` is the page's (it alone knows the
  sessions), `terminal` is the pane's (the compose runner needs copy/paste/search with no session at all)
  — and that split is what stops one keydown being handled twice. `shortcuts-dialog.tsx` is both cheatsheet
  and editor, because a read-only list is opened once and a hidden settings page never.

In `xterm-pane.tsx` and the page, load-bearing and easy to undo:

- **New session always opens a direct PTY.** A session is still nameable, pinnable and fileable because
  those properties belong to its in-memory workspace. New window creates a sibling direct PTY and the
  browser connects each visited window's emulator to that window's opaque id. There is no persistent
  option, detach/reattach path or pane model in the terminal API.
- **Visited windows keep their emulator and socket while hidden.** A TUI updates the screen incrementally;
  remounting xterm on each window/session switch loses its parser, alternate buffer and unchanged cells.
  The server's last 128 KiB of raw output is not a screen snapshot and may start inside an escape sequence.
  The page retains visited windows until their window/session is removed from the live lists or the page
  is left. Window lists are cached per session so switching sessions never borrows the previous session's
  windows while its request is pending. Hidden panes keep parsing bytes and answering live terminal
  queries, but remain inert and retain their last visible grid size. Selection fits, refreshes and focuses
  the visible pane; browser visibility/focus changes refresh it even if the dimensions have not changed.
- **`clipboardKey`**: Ctrl+C copies **only when something is selected** and clears the selection as it
  goes, so the interrupt is never more than one keypress away. Ctrl+V returns false *without*
  `preventDefault`, so xterm leaves the key alone instead of sending ^V and the browser's own paste runs —
  arriving through `onData`, where the multi-line confirmation still sees it. Reading the clipboard there
  instead needs a permission Firefox does not grant at all.
- **Clipboard images never enter the PTY.** A capture-phase paste listener on xterm's actual host leaves
  text-only events completely alone, but sends PNG/JPEG/WebP files to
  `POST /terminal/{id}/clipboard`. The handler binds the upload to the authenticated dashboard owner of
  the live session, verifies the declared MIME against the bytes, and chooses the destination under
  `/tmp/just-dashboard/<session-id>` itself. Only the returned absolute path goes through the existing
  terminal socket, with no Enter. The backend container bind-mounts that temporary root at the same path
  on the host; session directories are removed when their PTY ends and old files expire after seven days.
- **Multi-line paste is confirmed, and the guard lives in `onData`.** A pasted block runs every line but
  the last immediately, and Ctrl+V, the context menu and the X11 middle click all arrive as one `onData`
  call — guarding only the Ctrl+Shift+V handler guarded the one route nobody uses. That handler must call
  `preventDefault`: returning false from `attachCustomKeyEventHandler` stops xterm, not the browser, so
  without it the confirmation opened *and* the native paste went through.
- **Direct PTY reconnect uses best-effort shell-history replay.** A direct PTY has no independent screen
  model, so the handler subscribes before resizing and then sends its bounded output suffix. The replay
  protocol below prevents terminal capability replies from being typed into the current prompt.
- **Replies are suppressed while direct-PTY scrollback is replayed.** `CSI c` and friends are the shell asking the
  terminal a question, and xterm answers down the channel a keystroke uses — so replaying a buffer
  containing one typed `1;2c0;276` at whatever prompt exists now and left a column of "command not found".
  The server announces the replay with a `scrollback` frame before the binary snapshot (from the browser's
  side the bytes are identical either way) and the client drops its own output until xterm's write callback
  says the replay is parsed.
- `allowProposedApi` is on because the search addon's match count and highlight-all use xterm's decoration
  API, which is not frozen; without it `findNext` throws and the counter reads "none" over a scrollback
  full of matches.
- **The xterm-6-matched WebGL renderer is the default.** xterm 6's DOM renderer explicitly omits custom
  glyph support, so enabling `customGlyphs` there still leaves box-drawing and block-element characters
  to browser font fallback; that produced disconnected Codex borders and gaps in Claude's block artwork.
  `@xterm/addon-webgl` 0.19 matches xterm 6.0 and paints those structural glyphs to the full cell. Every
  alternate-buffer change schedules a complete refresh to prevent the stale/blank rows seen in the old
  WebGL integration, and context loss disposes WebGL and refreshes the DOM fallback. Setting
  `jd.terminal.renderer=dom` in local storage is the diagnostic A/B override. The Canvas addon remains
  absent because its stable release targets xterm 5 internals. Font metrics remain fixed at unit line
  height and zero letter spacing; `@xterm/addon-unicode11` keeps cursor arithmetic aligned with the
  Unicode-width rules used by modern TUIs.
- **PTY output and input are binary WebSocket frames.** JSON text frames are controls only. Raw PTY chunks
  go straight to `terminal.write(Uint8Array)` (whose streaming decoder preserves a UTF-8 character split
  across chunks); keyboard and paste strings are encoded once with `TextEncoder`. The backend neither
  decodes nor rewrites terminal bytes.
- **The navigation listener runs in the capture phase and must not skip the terminal.** Bubbling lands after
  xterm has forwarded the keystroke, so Ctrl+Alt+→ would switch the window *and* type an escape sequence.
  The usual "ignore keys while a text field has focus" guard needs an exception for `.xterm`, since xterm
  receives keystrokes through a hidden `.xterm-helper-textarea` — the plain form disables every shortcut
  exactly when the terminal has focus.
- **Shortcuts fire only where the shell has the keyboard.** These chords move sessions and close windows;
  anywhere-in-the-workspace was too wide and could close a window while the operator clicked around the
  file tree. The target must be inside `.xterm`, or nothing focused at all (`document.body` on a
  fresh load, which is the difference between "new session" having a shortcut and not). Any open dialog
  vetoes the lot, because focus sits on the body while one closes. The other half: **every switch hands the
  keyboard back** — window tabs are buttons and keep the focus they were given, so `XtermPane` takes a
  `focusRef` owned only by the active pane, which fits and focuses itself when selected. Background
  socket connections and pending image uploads must not steal that focus.

`tests/browser/terminal-ui.spec.ts` exercises both DOM and WebGL renderers with output exceeding the
server's replay limit, ANSI/UTF-8 split across messages, background terminal replies, resize while hidden,
window/session switching, screen preservation, input routing and cleanup when windows/sessions close.

**Open-shell links are consumed once.** The page removes `cwd` and `folder` from the current history
entry before creating the session, preserving other query parameters and the hash. A refresh cannot
replay a launch or recreate a closed session; a later explicit Open shell link can still launch anew.
The link gives the session no title: the prompt names the directory, and a title would have pinned the
row to the folder the shell started in.

**The page has no separate header or workspace bar.** A terminal is the one screen whose content *is* the
viewport. "New session" sits in the rail beside "New folder"; the emulator title bar contains the two
panel toggles and window tabs, with no shell, user or working-directory title. The one banner that stays
is a missing login account — a broken feature rather than information.
