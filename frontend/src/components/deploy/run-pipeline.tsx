"use client"

import { motion, useReducedMotion } from "motion/react"
import type { DeploymentStep } from "@/lib/types"
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

/**
 * The release path as a timeline: one bar in seven segments, a segment per
 * stage, each as long as the time the stage took — so the run's shape is read
 * before a word is. Build, which is most of any run, is most of the bar; the
 * stage that is working grows as it works and carries a pulse of light; the
 * stages ahead wait at their narrowest. Under each segment, its mark, its
 * name and how long it took.
 *
 * Wide, the segments sit in one line; narrow, they fall into two columns and
 * each keeps its own bar, so the picture is the same one folded.
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
  const groups = RELEASE_GROUPS.map((group) => ({
    label: group.label,
    state: groupedState(steps, group.keys),
    seconds: groupSeconds(steps, group.keys, now),
  }))

  return (
    <ol
      aria-label="Release path"
      className={cn("grid grid-cols-2 gap-x-4 gap-y-4 sm:flex sm:flex-wrap sm:gap-3", className)}
    >
      {groups.map((group) => {
        const pending = group.state === "pending"
        const running = group.state === "running"
        const failed = group.state === "failed" || group.state === "blocked"
        return (
          <li
            key={group.label}
            aria-current={running ? "step" : undefined}
            className="flex min-w-0 flex-col gap-2 transition-[flex-grow] duration-700 ease-out sm:basis-28"
            // A stage's share of the bar is its share of the run's time. A
            // stage that has not started takes only its floor.
            style={{ flexGrow: pending ? 0 : Math.max(group.seconds ?? 0, 1) }}
          >
            <Segment state={group.state} />
            <span className="flex min-w-0 items-center gap-1.5">
              <StepMark state={group.state} />
              <span
                className={cn(
                  "truncate text-xs font-medium",
                  pending && "text-muted-foreground",
                  failed && "text-destructive",
                )}
              >
                {running ? <TextShimmer>{group.label}</TextShimmer> : group.label}
              </span>
              <span className="numeric ml-auto shrink-0 text-hint text-muted-foreground">
                {group.seconds !== undefined
                  ? formatDuration(group.seconds)
                  : pending
                    ? ""
                    : stepStateLabel(group.state).toLowerCase()}
              </span>
              <span className="sr-only">{stepStateLabel(group.state)}</span>
            </span>
          </li>
        )
      })}
    </ol>
  )
}

/**
 * One stage's length of the bar, coloured by what happened there. Exported for
 * the dashboard's own restarts and upgrades, which are runs with stages too and
 * are drawn with the same bar rather than a second spelling of it.
 */
export function Segment({ state }: { state: ReleaseNodeState }) {
  const reduced = useReducedMotion()
  const track = "relative block h-1.5 w-full overflow-hidden rounded-full"
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
 * The release path at a glance: seven dots, one per stage, for a row that
 * knows only which stage is running. The stages before it are done, the one
 * itself breathes, the rest wait.
 */
export function MiniReleasePath({
  currentStep,
  className,
}: {
  currentStep?: string
  className?: string
}) {
  const current = RELEASE_GROUPS.findIndex((group) =>
    (group.keys as readonly string[]).includes(currentStep ?? ""),
  )
  return (
    <span aria-hidden className={cn("inline-flex items-center gap-1", className)}>
      {RELEASE_GROUPS.map((group, index) => (
        <span
          key={group.label}
          title={group.label}
          className={cn(
            "size-1.5 rounded-full",
            current >= 0 && index < current && "bg-success",
            index === current && "animate-breathe bg-warning",
            (current < 0 || index > current) && "bg-muted-foreground/40",
          )}
        />
      ))}
    </span>
  )
}

/** Seconds from the first of the stage's steps starting to the last ending, or to now while one is still going. */
function groupSeconds(steps: DeploymentStep[], keys: readonly string[], now: number) {
  const included = steps.filter((step) => keys.includes(step.key) && step.startedAt)
  if (included.length === 0) return undefined
  const start = Math.min(...included.map((step) => new Date(step.startedAt!).getTime()))
  const end = included.some((step) => !step.endedAt)
    ? now
    : Math.max(...included.map((step) => new Date(step.endedAt!).getTime()))
  return Math.max(0, (end - start) / 1000)
}
