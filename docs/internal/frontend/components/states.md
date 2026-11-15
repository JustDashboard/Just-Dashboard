# States

Empty, unavailable, failed, notice and loading: designed, and at the loaded shape (D11, D13).

**Members:** `EmptyState`, `EmptyNote`, `ErrorState`, `Notice`, `Spinner`, `LoadingRows`,
`LoadingPanel`, `Hold`, `Bone`, `MissingThing`, `MissingLine`, `MissingScreen`, `MissingOrError`,
`MissingPage`, `StandaloneState`.

**Status: moved text.** WP-12 rewrites this page in wave 1 as purpose, anatomy, use when and when
not (and what instead), props, states, the phone, accessibility, an example call site, what enforces
it, and related families. Until then it holds the rules that were in `design-system.md`, below.

## A thing that is not there has one screen

**A thing that is not there has one screen** (`components/missing-thing.tsx`, since 0.8.7).
`MissingThing` is the proxy's `EngineSetup` made general, for any page whose subject is absent
rather than empty or broken: the thing drawn as itself (`thing`, a `ProductLogo` id, at the 48px
tile; `icon` on the same tile for a module with no mark), a headline at the 24px rung that says
what is missing in plain words, one or two sentences on why and where it is fixed, the next step
as one brand-faced button with further steps after it, the subsystem's own words folded behind
*What the server said* (`raw`, never the headline), and optionally a rail of what the page draws
once the thing is there (`gains`). `size="block"` is the same words inside a section of a page
that has other things on it (Timers on Scheduled): the mark beside the words, no frame.
`MissingLine` is the same fact as one line with the thing's glyph and a link, for a rail's group
or an inspector. The words live in `missing-thing-model.ts` so they are the same everywhere:
`noSystemd(reader)` for a server systemd does not run (a container, or the dashboard's own
container started without `/run/systemd`; the steps are **Open Processes** and the mount to
copy), `moduleOff(module, canConfigure)` for a part switched off in Configuration (**Open
Configuration** for an administrator, a sentence naming who can for anybody else), and
`missingFromError(error)` for a failed read whose answer names an absence (`docker_unavailable`,
`systemd_not_running`, `not_installed` for systemd, `terminal_disabled`). `MissingScreen` draws a
model's `Missing` whole, and `MissingOrError` draws it where a read's error is one and
`ErrorState` where it is not, so a page writes one line for both. The Docker section draws its
faults through it (`docker/unreachable-state.tsx`; `DockerMissing` is the same screen for a page
outside the section, asking the daemon itself), and the Services list, a service's page and
Scheduled's Timers share `procs/no-systemd.tsx`. The step is always real: a route that already
exists (`POST /systemd/docker.service/start`, `POST /packages/install`), a link to the page that
helps, or a line to copy. An empty list is still `EmptyState` and a failed read still
`ErrorState`; a tool the package manager installs in place keeps `InstallHandoff`.
