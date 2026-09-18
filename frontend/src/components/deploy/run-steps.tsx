"use client"

import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { StepMark, formatDuration, humanize, stepStateLabel } from "@/components/deploy/vocabulary"
import type { DeploymentStep } from "@/lib/types"

/** Seconds a step took, or has taken so far; undefined until it starts. */
function stepDurationSeconds(step: DeploymentStep, now: number) {
  if (!step.startedAt) return undefined
  const start = new Date(step.startedAt).getTime()
  const end = step.endedAt ? new Date(step.endedAt).getTime() : now
  if (Number.isNaN(start) || Number.isNaN(end)) return undefined
  return Math.max(0, (end - start) / 1000)
}

/**
 * The run's steps as rows, Vercel's per-step durations included. Selecting a
 * row is how the reader jumps to that stage of the build console — the two
 * views are one story about the same run, read two ways.
 */
export function RunSteps({
  steps,
  now,
  onSelectStep,
}: {
  steps: DeploymentStep[]
  now: number
  onSelectStep: (id: number) => void
}) {
  return (
    <Panel plain>
      <PanelHeader title="Details" />
      <PanelBody flush>
        <RowList aria-label="Deployment steps">
          {steps.map((step) => {
            const seconds = stepDurationSeconds(step, now)
            return (
              <Row
                key={step.id}
                onClick={() => onSelectStep(step.id)}
                leading={<StepMark state={step.state} />}
                title={humanize(step.key)}
                subtitle={step.errorMessage}
                trailing={
                  <span className="numeric text-hint text-muted-foreground">
                    {step.attempt > 1 && `attempt ${step.attempt} · `}
                    {seconds !== undefined ? formatDuration(seconds) : stepStateLabel(step.state)}
                  </span>
                }
              />
            )
          })}
        </RowList>
      </PanelBody>
    </Panel>
  )
}
