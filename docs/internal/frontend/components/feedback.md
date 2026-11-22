# Feedback

What says an action is happening or happened: toasts, progress in a dialog, the stages of a run,
and the consoles that show its lines.

**Members:** `notify` (`lib/toast.ts`) and `Toaster` (`ui/sonner.tsx`), `OperationProgress`
(`operation-progress.tsx`), `RunPhases`, `phaseStates` (`run-phases.tsx`), `RunTranscript`
(`run-transcript.tsx`), `TranscriptRow`, `useTranscriptDrip`, `jobLineToTranscript`, `streamKind`
(`transcript-line.tsx`), `ConsoleView` (`console-view.tsx`), `JobConsole`, `useJobConsole`,
`RecentJobs` (`job-console.tsx`), `EmbeddedLog` and `LogSection` (`components/logs/`), and the copy
tick `useCopy` ([Values](values.md)).

## Which one

| What happened | Use | Not |
| --- | --- | --- |
| An action finished or failed, away from where the reader is looking | `notify.success` / `notify.error(title, cause)` | `sonner` called directly (lint refuses it) |
| A copy worked, where a toast would cover what was copied | `useCopy`'s inline tick | a toast |
| A dialog's action is in flight | `OperationProgress` in the `Modal`'s footer | a spinner box in the body |
| A run is passing through stages | `RunPhases` | a spinner and a sentence |
| A run's or a job's lines | `RunTranscript` (a restart, an upgrade), `JobConsole` (a job that outlives the page), `ConsoleView` for a new console | a `<pre>` of the tail, a `Pane` of `<div>`s |
| A service's log at a page's foot | `EmbeddedLog` in a `LogSection` | a console inside a scrolling page on a phone |
| A wait with no count | `Sweep` ([States](states.md)) | a percentage the interface invented |

## notify and the Toaster

**Purpose.** One way to say that something finished, and one place it is drawn. `notify.error`
takes the error itself and unwraps it (`failureReason`), so a toast never reads "ApiError: …" or
"Could not save" with no reason.

**Anatomy.** The `Toaster` draws on the popover ground like every floating panel, the state carried
by its icon alone (no flooded green or red card).

**Props.** `notify.success(title, options)`, `info`, `warning`, `error(title, cause, options)`,
`loading` (with an `id` the later `success` or `error` replaces), `dismiss(id)`; `notify.raw` reaches
the rest of sonner.

**How long.** A failure stays until it is dismissed; raised again with the same title and reason
it replaces its copy. Success, info and warning leave on the toaster's own timer (4 seconds). A
caller that knows better passes `duration`.

**Accessibility.** Toasts are announced politely; a failure the reader must act on is a `Notice` or
an `ErrorState` on the page, not only a toast.

**Enforced by** the `no-restricted-imports` rule in `eslint.config.mjs` ("Toasts go through
lib/toast.ts").

## OperationProgress

**Purpose.** A dialog's action in flight, drawn as part of `Modal`'s footer: the words take the
footer's free side beside the buttons, and the footer's top hairline becomes the track. A wait is
not a percentage: only a measured count fills the track; otherwise it sweeps.

**Use when** a dialog's verb runs for more than a moment (a restore, a bulk change). **Not** for a
run that outlives the dialog (a `JobConsole`), nor inside a page body.

## RunPhases

**Purpose.** Where a restart or an upgrade is, as the release path on a deployment draws it: one bar
per stage, coloured by what happened there, the stage at work sweeping and its name lit. The stages
are equal, because these runs record their phase and not how long each took.
`phaseStates(labels, at, outcome)` gives each stage its state: everything before `at` done, `at`
itself running or where the run failed, everything after waiting.

## RunTranscript, TranscriptRow and the drip

**Purpose.** A run's whole transcript in a console sized to be read: every line numbered, each of
`lib/transcript.ts`'s shapes drawn as itself, a step coloured by the service it builds, and a
failure washed where it happened rather than repeated in a banner. `TranscriptRow` is one line, the
same in `RunTranscript` and a deployment's `BuildConsole`.

**States.** Whole, not the tail: when the polled end says it was trimmed, the console reads the
whole file once and extends it from then on. While the run is live and the reader is at the end,
`useTranscriptDrip` lets a burst of lines out evenly over the frames that follow so the page does
not jump, each rising once (§11), and every line is on screen within `DRIP_LAG_MS` (300 ms) of the
batch that brought it, however fast the batches come; with reduced or paused motion they are simply
there. It used to let out a twenty-fourth of what it held a frame and, at its 400-line cap under a
steady build, stopped asking for frames and held the last 400 lines for as long as the reader
followed. `TranscriptRow gutter` takes `gutterCh` of the last number, so the number column is as
wide as the run needs.

**A deployment's build console** (`BuildConsole`) holds what its stream wrote in a
`TranscriptBuffer` (`components/deploy/transcript-buffer.ts`): events that follow the last are read
alone and appended, so a batch costs its own lines rather than the whole transcript, and a line
keeps its number when the oldest events go past the 5,000-event cap. The lines are memoised rows in
blocks of 100 (`ConsoleBlock`), each block `content-visibility: auto`, so a frame lays out the
blocks and the lines of the one growing rather than every line; the steps' running times tick on
their own clocks, so the page's second does not draw the console again. Find, a selection across
blocks, Copy, Download and the scroll position see one transcript.

**Accessibility.** The lines are an `ol` of `li`, so a screen reader can count and step through
them. In the build console the blocks and their lists are `role="none"` and each line says
`role="listitem"`, so a step's list still holds only its lines.

## JobConsole

**Purpose.** Watching an operation that outlives the page (a certificate issuance, a package
upgrade): it attaches to a job by id and resumes from the last line it saw, so a dropped connection
costs nothing and a console opened late shows everything from the beginning. `RecentJobs` lists the
ones that ran recently, so a job started before lunch can be read after it.

**ConsoleView** (`components/console-view.tsx`) is the one console: `ConsoleView({ title, label?,
state: "running" | "ok" | "failed" | "cancelled", detail?, note?, lines: { key, line, prefix? }[],
dropped?, error?, empty?, onCancel?, onDismiss?, rise?, className? })`, each `line` a transcript
line. The header is the title, the state as a `Status` word spoken when it changes, and Stop or
Dismiss. The well is one `max-h-80` `role="log"` region that follows the newest line, and its
lines are `TranscriptRow`s. `JobConsole` maps job lines with `jobLineToTranscript` (status becomes
a `note` heading, stderr a washed `warning`, the number is the job's `seq`). Docker's `RunConsole`
maps compose lines with `streamKind` and puts the service before each line. A running job carries
the Tag "Keeps running if you leave"; the recent list beside it is the way back. A new console
uses `ConsoleView`, not a `Pane` of `<div>`s.

## EmbeddedLog

**Purpose.** A service's log at a page's foot (`components/logs/embedded-log.tsx`), under a
`LogSection` (the title, the hairline and the held skeleton). From `sm` it is the log read in
place through `service-logs.tsx`, at one of three heights (`LOG_PANE_SIZE`: section, sheet,
fill). On a phone it is a card per source that opens the Logs page on it (`logsPageHref`, with
`sourceSummary` as its line), because a console inside a scrolling page is the stacked box D12
rules out.

## The phone

A console is the pane's full height on a phone: one pane, never a short box inside a scrolling page
(see [Workbench](workbench.md)). Its head is one row there: the run's name, its state and Stop or
Dismiss. `ConsoleView`'s `note` (a job's "Keeps running if you leave") is drawn from `sm` up, so the
name has the room; a name still too long is cut at its end, whole on hover and in its accessible
name.

## Related

[States](states.md) (loading, `Sweep`, errors), [Overlays](overlays-confirm.md) (`Modal`'s footer),
[`../foundations/motion.md`](../foundations/motion.md) (a sweep means work; a rise means arrived),
[`../patterns/live-data.md`](../patterns/live-data.md).
