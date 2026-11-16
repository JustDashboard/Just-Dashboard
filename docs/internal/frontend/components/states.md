# States

Empty, absent, failed, stale, notice and loading: each one designed, and each at the loaded shape
(D11, D13).

**Members:** `EmptyState`, `EmptyNote`, `NeedsAdmin`, `ErrorState`, `Notice`
(`components/state.tsx`); `Stale`, `StaleNotice` (`components/stale.tsx`); `MissingThing`,
`MissingScreen`, `MissingOrError`, `MissingLine` (`components/missing-thing.tsx`, words in
`missing-thing-model.ts`); `InstallHandoff` (`components/install-handoff.tsx`); `Spinner`,
`LoadingRows`, `LoadingPanel`, `Hold`, `Sweep` (`components/loading.tsx`); `Bone`;
`MissingPage` and `StandaloneState` for a whole route.

**Deprecated, kept while they have callers:** `ReadFailed` (`database/fleet/read-failed.tsx`,
called only from `database/schema/` and `database/diagram/` now), `ReadError`
(`database/redis/read-error.tsx`), `NetworkReadWarning` (`network/read-warning.tsx`). Each is
one call of the member named in its `@deprecated` line. The database home's own two copies are
gone: its blocks use `ErrorState` and `Stale`.

## Which one

| The place shows | Use | Not |
|---|---|---|
| A list, page or section with nothing in it yet | `EmptyState`: bare glyph (or `mark`), title, a sentence, the verb that fills it | A hand-typed grey sentence |
| Nothing, inside something already framed: a rail, a picker, a panel under a filter | `EmptyNote`; `dense` in a rail; `action` for the way out ("Clear the search"); `as="li"` inside a `<ul>` | `EmptyState`, which would be a frame in a frame |
| A section this account may not open | `NeedsAdmin({ what })`: "<What> needs an administrator", the lock, under the page's own identity header | A page-specific phrase or glyph |
| The thing the page reads is not there: Docker, systemd, a program, a module | `MissingThing` / `MissingScreen` (`size="block"` in a section), or `InstallHandoff` where the package manager installs it in place | `EmptyState` (that says the list is empty), `ErrorState` (that says it broke) |
| A read that failed, with nothing to show | `ErrorState`, with `what` for a read | An amber `Notice`, or a second Try again under it |
| A read that failed over data already shown | `Stale` (a header's mark or a pane's strip) or `StaleNotice` (a page's banner) | Taking the data away for a red box |
| A fact the reader needs before acting | `Notice` | `ErrorState` for something that did not fail |
| A wait | The skeleton of what is coming (`LoadingRows`, `LoadingPanel`, a block's own); `Sweep` for work of unknown length; `Spinner` only inside a control or beside its own words | A spinner in the middle of an empty panel |

## A thing that is not there has one screen

`MissingThing` is the proxy's `EngineSetup` made general, for any page whose subject is absent
rather than empty or broken: the thing drawn as itself (`thing`, a `ProductLogo` id, at the 48px
tile; `icon` on the same tile for a module with no mark; `mark` for a stack of products), a
headline at the 24px rung that says what is missing in plain words, one or two sentences on why
and where it is fixed, the next step as one brand-faced button with further steps after it, the
subsystem's own words folded behind *What the server said* (`raw`, never the headline), and
optionally a rail of what the page draws once the thing is there (`gains`). `size="block"` is the
same words inside a section of a page that has other things on it (Timers on Scheduled): the mark
beside the words, no frame. `MissingLine` is the same fact as one line with the thing's glyph and
a link, for a rail's group or an inspector.

The words live in `missing-thing-model.ts` so they are the same everywhere: `noSystemd(reader)`
(the steps are **Open Processes** and the mount to copy), `moduleOff(module, canConfigure)`
(**Open Configuration** for an administrator, a sentence naming who can for anybody else),
`missingFromError(error)` for `docker_unavailable`, `systemd_not_running`, `not_installed` for
systemd and `terminal_disabled`, and `absentFromError(error)`, which adds a program that is not
installed (**Open Packages**, searched for it) and a host with no proxy (**Open Proxy**).

**`ErrorState` draws every absence by itself.** A failed read whose answer `absentFromError`
recognises is drawn as `MissingScreen size="block"`, with Try again after its steps where the
retry is offered. There is no "Not available on this host" box any more. `MissingOrError` stays for
a page that knows what it reads from systemd or whether the account may change Configuration, and
for a page-sized screen.

**`InstallHandoff` is the same screen with an install in it.** `pkg`, `products`, `title` and the
sentences as `children`; the step is **Install <pkg>** for an administrator and a sentence naming
the package for anybody else; the job's console and, after a success, the phases the server reads
(`HandoffPhases`) come in `after`. `size="page"` for a page whose whole subject is the tool.
`usePackageKnown` says whether the package manager has the package at all, so a button that would
end in "Unable to locate package" is never offered.

The step is always real: a route that already exists, a link to the page that helps, or a line to
copy.

## ErrorState

```tsx
<ErrorState what="the busiest statements" error={poll.error} onRetry={poll.refresh} every="30 seconds" />
```

| Prop | Says |
|---|---|
| `error` | The failure. An `ApiError`'s `operation` and `resource` make the headline "Could not <operation> <resource>" |
| `what` | What the block would have shown. The headline is "Could not read <what>" where the server named no operation, the server's words go under it as a sentence, and Try again is offered |
| `onRetry` | The read again. Offered when the server marked the failure retryable, when `what` is given, when `retryable` says so, and always for a 429 |
| `retryable` | Forces the offer on (or off) |
| `every` | "It is asked again by itself every <every>." beside Try again |

Try again stays pending until the read it started settles. A 404 is calm ("It may have been removed
or renamed since this link was made."). A 429 is calm, says when it asks again, and does so by
itself past the server's `Retry-After`. Anything else is the danger wash with `role="alert"`. Other
props reach the root (`data-slot="error-state"`, `data-kind` missing, throttled or failed).

## Stale and StaleNotice

When a poll fails over data already drawn, the data stays and says it is old. One word for it:
**Not updating**.

- `<Stale error={staleOf(poll)} />` is the mark at a header's end, with the reason as its title.
  `Block` draws it from its `stale` prop.
- `<Stale form="strip" error={…} subject="orders" onRetry={…} />` is a hairline strip across the
  top of a pane: the word, "last reading of orders", and a ghost Try again. It is `role="status"`.
- `<StaleNotice error={…} refresh={…} lastSuccess={…} reading="packet captures" />` is a page's
  banner: "Showing the last known <reading>", how long ago the last good read was (advancing, with
  the date in its `title`), the server's words as a sentence and one verb (`verb`, default
  Refresh). It is a warning `Notice` with `live="alert"`.

Each draws nothing without an `error`.

## Notice

A banner for context, not failure: a glyph on the banner's own ground, a title and a body.
`live="status"` or `live="alert"` makes it its own live region, so a caller does not wrap it in a
`<div role="alert">`. Other props reach the root (`data-slot="notice"`, `data-tone`). A danger
`Notice` is not an alert by default yet, because several callers still wrap theirs in one; the
default flips when they pass `live` instead.

## EmptyState, EmptyNote and NeedsAdmin

`EmptyState` draws its glyph bare at the height of the title's line, or `mark` (a `ProductLogos`
stack) in its place; the 40px plate it stood on is gone (§14). It takes the root's props.

`EmptyNote` is one centred sentence. `dense` is the hint step at a third of the padding, for rails
and pickers. `action={{ label, onClick }}` puts a ghost button under it. `as` is `p` (the default),
`li` or `div`.

`NeedsAdmin({ what, why?, icon?, action? })` is an `EmptyState` with the lock and the one phrase
("Packet captures need an administrator"; `need` after a plural subject). Draw it under the page's
identity header so the page keeps its shape.

## Loading

- The page holds still: a skeleton is the silhouette of what arrives (`LoadingPanel`, `LoadingRows`,
  a block's own rows), and `hold` keeps what is under it off the page until the first answer
  (`Hold`).
- `label` on `LoadingRows` and `LoadingPanel` says what is being read to a screen reader, once, in
  an sr-only `role="status"`, and marks the skeleton `aria-busy`. `Spinner` takes the same `label`
  when it stands alone; inside a pending `Button` it needs none, because the button is `aria-busy`.
- `Sweep` is the one indeterminate bar: a third of the track crossing it. The caller places it with
  `className`; `track` draws the channel; `tone="quiet"` is the foreground fill over a reading that
  is already coloured; `label` makes it a `progressbar` of that name, otherwise it is `aria-hidden`.
  Under reduced motion, from the system or from `data-motion="paused"`, the bar stops and fills the
  track in `--faint`. `OperationProgress` and the shell's splash draw through it; the other
  hand-built bars move to it in the page passes, and then nothing else writes `animate-sweep`.

## The phone

Nothing here changes shape on a phone. `MissingThing size="page"` puts its rail under the words,
and every button in these states takes the coarse-pointer control height.

## What enforces it

`components/state.test.js` holds ErrorState's headlines, kinds, retry offer and absences, EmptyNote's
props, NeedsAdmin's phrase, Spinner's label, the skeletons' labels and holds, and Sweep's
reduced-motion rule. `missing-thing-model.test.js` holds the words. `app-states`, `missing-states`,
`module-missing` and `operation-progress` hold them in a browser.

## Related

[`surfaces.md`](surfaces.md) (where `MissingThing` sits among the page shells),
[`feedback.md`](feedback.md) (`OperationProgress`, `JobConsole`),
[`../foundations/states.md`](../foundations/states.md) (selected, hover and focus),
[`../foundations/motion.md`](../foundations/motion.md) (sweep among the four animations).
