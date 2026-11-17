# Feedback

What says an action is happening or happened: toasts, progress in a dialog, the stages of a run,
and the consoles that show its lines.

**Members:** `notify` (`lib/toast.ts`) and `Toaster` (`ui/sonner.tsx`), `OperationProgress`
(`operation-progress.tsx`), `RunPhases`, `phaseStates` (`run-phases.tsx`), `RunTranscript`
(`run-transcript.tsx`), `TranscriptRow`, `useTranscriptDrip` (`transcript-line.tsx`), `JobConsole`,
`useJobConsole`, `RecentJobs` (`job-console.tsx`, moving to `ConsoleView`, WP-32), and the copy tick
`useCopy` ([Values](values.md)).

## Which one

| What happened | Use | Not |
| --- | --- | --- |
| An action finished or failed, away from where the reader is looking | `notify.success` / `notify.error(title, cause)` | `sonner` called directly (lint refuses it) |
| A copy worked, where a toast would cover what was copied | `useCopy`'s inline tick | a toast |
| A dialog's action is in flight | `OperationProgress` in the `Modal`'s footer | a spinner box in the body |
| A run is passing through stages | `RunPhases` | a spinner and a sentence |
| A run's or a job's lines | `RunTranscript` (a restart, an upgrade), `JobConsole` (a job that outlives the page) | a `<pre>` of the tail |
| A wait with no count | `Sweep` ([States](states.md), WP-12) | a percentage the interface invented |

## notify and the Toaster

**Purpose.** One way to say that something finished, and one place it is drawn. `notify.error`
takes the error itself and unwraps it (`failureReason`), so a toast never reads "ApiError: …" or
"Could not save" with no reason.

**Anatomy.** The `Toaster` draws on the popover ground like every floating panel, the state carried
by its icon alone (no flooded green or red card).

**Props.** `notify.success(title, options)`, `info`, `warning`, `error(title, cause, options)`,
`loading` (with an `id` the later `success` or `error` replaces), `dismiss(id)`; `notify.raw` reaches
the rest of sonner.

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
`useTranscriptDrip` lets a burst of lines out a few a frame so the page does not jump, each rising
once (§11); with reduced or paused motion they are simply there.

**Accessibility.** The lines are an `ol` of `li`, so a screen reader can count and step through
them.

## JobConsole

**Purpose.** Watching an operation that outlives the page (a certificate issuance, a package
upgrade): it attaches to a job by id and resumes from the last line it saw, so a dropped connection
costs nothing and a console opened late shows everything from the beginning. `RecentJobs` lists the
ones that ran recently, so a job started before lunch can be read after it.

**Status.** WP-32 merges it and the Docker run console into one `ConsoleView` (title, state, lines,
cancel, dismiss) whose lines are `TranscriptRow`s; until then a new caller uses `JobConsole`.

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
