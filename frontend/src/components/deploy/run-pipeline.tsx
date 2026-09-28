"use client"

import { motion, useReducedMotion } from "motion/react"
import type { DeploymentStep, DeploymentStepState } from "@/lib/types"
import { cn } from "@/lib/utils"
import { TextShimmer } from "@/components/ui/text-shimmer"
import {
  RELEASE_GROUPS,
  StepMark,
  formatDuration,
  groupedState,
  stepStateLabel,
  type ReleaseNodeState,
} from "@/components/deploy/vocabulary"

// The stages that have ended well enough for the next one to start.
const DONE = new Set<ReleaseNodeState>(["passed", "warning", "skipped"])

/**
 * The release path as a timeline: one bar in seven segments, a segment per
 * stage, each as long as the time the stage took — so the run's shape is read
 * before a word is. Build, which is most of any run, is most of the bar; the
 * stage that is working grows as it works and carries a pulse of light; the
 * stages ahead wait at their narrowest, and a stage the run does not include
 * is a dashed line where a stage would be. Under each segment, its mark, its
 * name and how long it took.
 *
 * Wide, each segment carries its label. On a phone the same seven segments
 * fold into one line and the labels leave the screen for the screen reader —
 * the same elements, not a hidden twin — and one caption under the bar names
 * the stage that matters now: the one at work, the one that failed, or the
 * last one that ran. Two columns of seven labelled bars was four rows and a
 * hundred and seventy pixels, with Route alone on the last.
 */
export function ReleasePipeline({
  steps,
  now,
  className,
}: {
  steps: DeploymentStep[]
  /** The clock a stage still working is measured against. */
  now: number
  className?: string
}) {
  // A stage none of whose steps the run planned is `absent` — a certificate
  // for a project with no new name — and draws as not part of this run
  // rather than as waiting on it for ever.
  const groups = RELEASE_GROUPS.map((group) => ({
    label: group.label,
    state: groupedState(steps, group.keys),
    seconds: groupSeconds(steps, group.keys, now),
  }))
  const present = groups.filter((group) => group.state !== "absent")
  const done = present.filter((group) => DONE.has(group.state)).length
  const focus =
    present.find((group) => group.state === "running") ??
    present.find((group) => group.state === "failed" || group.state === "blocked") ??
    [...present].reverse().find((group) => group.state !== "pending") ??
    present[0]

  return (
    <div className={cn("flex min-w-0 flex-col gap-2", className)}>
      <ol aria-label="Release path" className="flex min-w-0 gap-0.5 sm:flex-wrap sm:gap-3">
        {groups.map((group) => {
          const waiting = group.state === "pending" || group.state === "absent"
          const running = group.state === "running"
          const failed = group.state === "failed" || group.state === "blocked"
          return (
            <li
              key={group.label}
              aria-current={running ? "step" : undefined}
              className={cn(
                // On a phone the bar is the whole picture, so its widths are
                // pure shares of the run and a stage that has not started
                // keeps a share of one; from `sm` each stage keeps a floor
                // wide enough for its label and waits at it.
                "flex min-w-2.5 basis-0 flex-col gap-2 transition-[flex-grow] duration-700 ease-out sm:min-w-0 sm:basis-28",
                waiting && "grow sm:grow-0",
              )}
              // A stage's share of the bar is its share of the run's time.
              style={waiting ? undefined : { flexGrow: Math.max(group.seconds ?? 0, 1) }}
            >
              <Segment state={group.state} />
              <span className="sr-only flex min-w-0 items-center gap-1.5 sm:not-sr-only">
                <StepMark state={group.state} />
                <span
                  className={cn(
                    "truncate text-xs font-medium",
                    group.state === "pending" && "text-muted-foreground",
                    group.state === "absent" && "text-muted-foreground/60",
                    failed && "text-destructive",
                  )}
                >
                  {running ? <TextShimmer>{group.label}</TextShimmer> : group.label}
                </span>
                <span className="numeric ml-auto shrink-0 text-hint text-muted-foreground">
                  {stageReading(group.state, group.seconds)}
                </span>
                <span className="sr-only">{stepStateLabel(group.state)}</span>
              </span>
            </li>
          )
        })}
      </ol>
      {/* The folded bar's one line of words. The list above already says all
          of it to a screen reader, so this is for the eye only. */}
      <p aria-hidden className="flex min-w-0 items-center gap-1.5 text-xs sm:hidden">
        <StepMark state={focus.state} />
        <span
          className={cn(
            "truncate font-medium",
            focus.state === "pending" && "text-muted-foreground",
            (focus.state === "failed" || focus.state === "blocked") && "text-destructive",
          )}
        >
          {focus.state === "running" ? <TextShimmer>{focus.label}</TextShimmer> : focus.label}
        </span>
        <span className="numeric shrink-0 text-hint text-muted-foreground">
          {stageReading(focus.state, focus.seconds)}
        </span>
        <span className="numeric ml-auto shrink-0 text-hint text-muted-foreground">
          {done} of {present.length} stages
        </span>
      </p>
    </div>
  )
}

/** How long a stage took, what became of it when it has no clock, or nothing yet. */
function stageReading(state: ReleaseNodeState, seconds: number | undefined) {
  if (state === "absent") return "—"
  if (seconds !== undefined) return formatDuration(seconds)
  return state === "pending" ? "" : stepStateLabel(state).toLowerCase()
}

/**
 * One stage's length of the bar, coloured by what happened there. Exported for
 * the dashboard's own restarts and upgrades, which are runs with stages too and
 * are drawn with the same bar rather than a second spelling of it.
 *
 * `absent` is the wire's own vocabulary for what does not exist (§2): a dashed
 * hairline through the middle of the segment's height, with no track under it,
 * so it is quieter than a stage that is merely waiting.
 */
export function Segment({ state }: { state: ReleaseNodeState }) {
  const reduced = useReducedMotion()
  const track = "relative block h-1.5 w-full overflow-hidden rounded-full"
  if (state === "absent")
    return (
      <span className="relative block h-1.5 w-full">
        <span className="absolute inset-x-0 top-1/2 border-t border-dashed border-hairline" />
      </span>
    )
  if (state === "running")
    return (
      <span className={cn(track, "bg-plot-brand")}>
        {reduced ? (
          <span className="absolute inset-0 bg-brand/60" />
        ) : (
          <motion.span
            className="absolute inset-y-0 left-0 w-1/3 rounded-full bg-brand"
            initial={{ x: "-100%" }}
            animate={{ x: "300%" }}
            transition={{ repeat: Infinity, duration: 1.4, ease: "easeInOut" }}
          />
        )}
      </span>
    )
  return (
    <span
      className={cn(
        track,
        state === "passed" && "bg-success",
        (state === "failed" || state === "blocked") && "bg-destructive",
        state === "warning" && "bg-warning",
        (state === "skipped" || state === "cancelled" || state === "unavailable") &&
          "bg-muted-foreground/40",
        state === "pending" && "bg-meter-track",
      )}
    />
  )
}

/**
 * The release path at a glance, for a row that knows only which stage a run is
 * at: seven short segments of the run page's own bar. The stages before it are
 * done, the one itself sweeps — or is red, when the step it stopped at failed
 * or is blocked — and the rest wait. It was seven dots with an amber one
 * breathing, which said "in flight" in a different shape and colour from the
 * page it links to.
 */
export function MiniReleasePath({
  currentStep,
  currentStatus,
  className,
}: {
  /** The step key the run is at (`DeploymentCurrentStep.key`). */
  currentStep?: string
  /** That step's state, so a run stopped on a failed or blocked step says so. */
  currentStatus?: DeploymentStepState
  className?: string
}) {
  const current = RELEASE_GROUPS.findIndex((group) =>
    (group.keys as readonly string[]).includes(currentStep ?? ""),
  )
  const here: ReleaseNodeState =
    currentStatus === "failed" || currentStatus === "blocked" ? currentStatus : "running"
  const label =
    current < 0
      ? "Release path, not started"
      : `Stage ${current + 1} of ${RELEASE_GROUPS.length}: ${RELEASE_GROUPS[current].label}${here === "running" ? "" : `, ${here}`}`
  return (
    <span
      role="img"
      aria-label={label}
      className={cn("inline-flex shrink-0 items-center gap-0.5", className)}
    >
      {RELEASE_GROUPS.map((group, index) => (
        <span key={group.label} title={group.label} className="w-2.5">
          <Segment
            state={
              current >= 0 && index < current ? "passed" : index === current ? here : "pending"
            }
          />
        </span>
      ))}
    </span>
  )
}

/**
 * Where something sits on a path, as a strip of bars with their names under
 * them and the current one lit: where a release task runs on the release
 * path, where a deployment is in its life on the Danger zone. The steps
 * before it are drawn a step down, the ones after it as the track. On a phone
 * only the current step keeps its name.
 */
export function StageStrip({
  steps,
  current,
  label,
}: {
  steps: readonly string[]
  current: number
  label: string
}) {
  return (
    <span role="img" aria-label={label} className="block">
      <span className="flex gap-1">
        {steps.map((step, index) => (
          <span
            key={step}
            className={cn(
              "h-1 min-w-0 flex-1 rounded-full",
              index === current
                ? "bg-brand"
                : index < current
                  ? "bg-muted-foreground/40"
                  : "bg-meter-track",
            )}
          />
        ))}
      </span>
      <span aria-hidden className="mt-1.5 flex gap-1">
        {steps.map((step, index) => (
          <span
            key={step}
            className={cn(
              "min-w-0 flex-1 truncate text-micro",
              index === current
                ? "font-medium text-foreground"
                : "text-muted-foreground max-sm:invisible",
            )}
          >
            {step}
          </span>
        ))}
      </span>
    </span>
  )
}

/**
 * Seconds from the first of the stage's steps starting to the last ending —
 * to now only while one of them is running. A stage whose steps finished
 * without recording an end has no figure, rather than one ticking for ever.
 */
function groupSeconds(steps: DeploymentStep[], keys: readonly string[], now: number) {
  const included = steps.filter(
    (step) => keys.includes(step.key) && step.startedAt && step.state !== "pending",
  )
  if (included.length === 0) return undefined
  const start = Math.min(...included.map((step) => Date.parse(step.startedAt!)))
  const ends = included.flatMap((step) => (step.endedAt ? [Date.parse(step.endedAt)] : []))
  const end = included.some((step) => step.state === "running")
    ? now
    : ends.length > 0
      ? Math.max(...ends)
      : undefined
  if (end === undefined || Number.isNaN(start) || Number.isNaN(end)) return undefined
  return Math.max(0, (end - start) / 1000)
}
