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
  five rows. `ContainerFindings` filters the page's single pass for one container's detail panel.
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
panel, and on a page that is eight stacked panels it reads as a column of orange marks rather than as
eight headings. It started here and now holds product-wide — the prop is gone from `PanelHeader`,
`Modal`, `SidePanel` and `Section`, and the title sits at `text-title` instead
(`docs/internal/frontend/design-system.md` §14). The `?` stays: it is the one mark on those headers
that does something.
- `stacks-tab.tsx` is the stack list, as rows in one panel rather than a grid of bordered cards: each row
  carries the stack's state, its services (dot, name, ports, health) and the one action that belongs
  there — deploy when the application is down. It opens with the same search box and state chips as the
  containers page, because "which of these is down" is the same question asked of the same server. The
  detail panel follows the same rule as a container: the compose verbs that are pressed daily (deploy,
  restart) sit inline, and the rest are behind one overflow menu where each gets its word and its
  sentence. Its services tab is a single fenced list the eye reads down, not a stack of bordered rows.
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

Docker's `/docker/containers?container=` and `/docker/stacks?stack=` select the owning detail panel.
`useQuerySelection` keeps panel selection in browser history so reload and back/forward restore it;
closing clears only that selection parameter. Deployment runtime rows use these handoffs, including
when a container disappears between the observation and the click: the owner panel displays its error.

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
protocol row. `ConnectionsPanel` and `OffendersPanel` both offer a one-click firewall deny — the join that
makes the pages one product, since the address the ban log keeps naming deserves a rule outliving the ban.

The security section is eight pages of the same three shapes, and the rules it follows are the ones the
[design system](shell-design.md#the-design-system) already states:

- **A finding is content, not a floating line.** `AreaFindings` renders inside a `Panel` with the rest of
  the page rather than dropping a bare accordion onto the page ground, where the most urgent sentence on
  screen was also the least contained thing on it.
- **A standing fact is not a banner.** Text that never changes — the firewall's lockout guard, "a probe
  answers outward, not inward" — is stated *once*, in the footer of the thing it qualifies or at the top
  of the page it applies to, never repeated per card. Only a `Notice` that explains why the control under
  it will refuse (sshd with no key on the host) stays above the control.
- **Repeated things are rows, not cards.** `JailsPanel` is one table of jails with the addresses each is
  holding behind a `SidePanel`, and `ToolsPanel` filters twenty one-line probe forms by group and by
  name. Both were grids of same-sized cards whose content was nothing like the same size.
- **The slack in a table belongs to the widest column.** `w-full` goes on the comment, the process list,
  the watched paths — never on the short first column, which is what put eight hundred pixels of nothing
  between an address and the number beside it.

**Packages.** `install-panel.tsx` updates as you type, which is not decoration: the reason people open a
terminal instead of a package page is that they do not know the name (`postgresql-client`, not `psql`;
`build-essential`, not `gcc`), and a form where you type a guess and press a button to find out you were
wrong is a form you use once. Install is one press on the row — there was a tray, and it cost a click and
a concept on every single-package install while protecting against an interruption a job survives anyway.
The command each button runs is its `title`. `package-sheet.tsx`'s second tab is the whole point of the
feature; the installed table caps at 400 rendered rows with the count said plainly underneath.

## The terminal panel

`components/terminal/` is the session rail and window strip; `components/xterm-pane.tsx` is the emulator. The
split matters — the pane is reused by the compose runner and knows nothing about sessions.

- `session-rail.tsx` uses the same framed card, tinted header and hairlines as the Files/Git panel. Folder
  headers are plain disclosure rows: chevron and explanatory name only, with no folder icon, count,
  nested container or empty invitation. Sessions use neutral design-system states rather than assigned
  colours. Pinning still sorts a session to the top of its folder.
- `window-strip.tsx` places compact, horizontally scrolling direct-PTY tabs between exactly two workspace
  toggles: sessions on the left and Files/Git on the right. The strip is embedded in the emulator's own
  title bar; there is no separate workspace bar or working-directory/shell title.
  Window menus retain rename and close only; there are no split, layout or colour actions.
  Every tab has a visible close button. Closing the last window closes its session through the session
  endpoint; both paths explain the consequence in a confirmation.
  The emulator toolbar keeps search, snippets, appearance and fullscreen visible, with copy, export,
  folder navigation, shortcuts and clear in Terminal actions. Text size lives in Appearance.
  Input stays in the shell: there is no separate composer or Workspace/Focus mode. Bundled Bash and
  Zsh startup files install a compact directory/chevron prompt and native Tab completion in new windows.
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
  browser switches windows by connecting the emulator to that window's opaque id. There is no persistent
  option, detach/reattach path or pane model in the terminal API.
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
  `focusRef` and the page calls it as the active socket changes.

**Open-shell links are consumed once.** The page removes `cwd` and `folder` from the current history
entry before creating the session, preserving other query parameters and the hash. A refresh cannot
replay a launch or recreate a closed session; a later explicit Open shell link can still launch anew.

**The page has no separate header or workspace bar.** A terminal is the one screen whose content *is* the
viewport. "New session" sits in the rail beside "New folder"; the emulator title bar contains the two
panel toggles and window tabs, with no shell, user or working-directory title. The one banner that stays
is a missing login account — a broken feature rather than information.
