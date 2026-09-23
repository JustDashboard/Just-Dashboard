"use client"

import { cn } from "@/lib/utils"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Segment } from "@/components/deploy/run-pipeline"
import { StepMark, stepStateLabel, type ReleaseNodeState } from "@/components/deploy/vocabulary"

export type RunPhase = { label: string; state: ReleaseNodeState }

/**
 * Where a restart or an upgrade is, as the release path on a deployment draws
 * it: one bar per stage, coloured by what happened there, the stage at work
 * sweeping and its name lit. It replaced a spinner and a sentence — "Rebuilding
 * the images" says what is happening, and says nothing about how much of the
 * run is left, which is what somebody watching their dashboard replace itself
 * wants to know.
 *
 * The stages are equal: these runs record their phase, not how long each took,
 * and a bar whose segments were guessed at would be the interface inventing a
 * reading.
 */
export function RunPhases({ phases, className }: { phases: RunPhase[]; className?: string }) {
  return (
    <ol
      aria-label="Stages"
      className={cn("grid min-w-0 gap-x-3 gap-y-3", className)}
      style={{ gridTemplateColumns: `repeat(${phases.length}, minmax(0, 1fr))` }}
    >
      {phases.map((phase) => {
        const running = phase.state === "running"
        return (
          <li
            key={phase.label}
            aria-current={running ? "step" : undefined}
            className="flex min-w-0 flex-col gap-2"
          >
            <Segment state={phase.state} />
            {/* Wrapped rather than truncated: three stages across a phone
                are a hundred pixels each, and "Recreate the c…" is not one. */}
            <span className="flex min-w-0 items-start gap-1.5">
              <StepMark state={phase.state} className="mt-0.5" />
              <span
                className={cn(
                  "min-w-0 text-xs leading-snug font-medium",
                  phase.state === "pending" && "text-muted-foreground",
                  (phase.state === "failed" || phase.state === "blocked") && "text-destructive",
                  phase.state === "warning" && "text-warning",
                )}
              >
                {running ? <TextShimmer>{phase.label}</TextShimmer> : phase.label}
              </span>
              <span className="sr-only">{stepStateLabel(phase.state)}</span>
            </span>
          </li>
        )
      })}
    </ol>
  )
}

/**
 * The states of a run of stages, from the index of the one at work. Everything
 * before it is done; the one itself is running, or — once the run has ended in
 * failure — where it failed; everything after waits.
 */
export function phaseStates(
  labels: string[],
  at: number,
  outcome: "running" | "success" | "failed" | "rolled_back",
): RunPhase[] {
  return labels.map((label, index) => {
    if (outcome === "success") return { label, state: "passed" }
    if (index < at) return { label, state: "passed" }
    if (index > at) return { label, state: "pending" }
    if (outcome === "running") return { label, state: "running" }
    return { label, state: outcome === "rolled_back" ? "warning" : "failed" }
  })
}
