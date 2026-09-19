"use client"

import { Logs } from "@/components/icons"
import { plural, timestamp } from "@/lib/format"
import type { DeploymentStep } from "@/lib/types"
import { Detail, DetailList } from "@/components/page"
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion"
import { Button } from "@/components/ui/button"
import { StepMark, formatDuration, humanize, stepStateLabel } from "@/components/deploy/vocabulary"

/** Seconds a step took, or has taken so far; undefined until it starts. */
function stepDurationSeconds(step: DeploymentStep, now: number) {
  if (!step.startedAt) return undefined
  const start = new Date(step.startedAt).getTime()
  const end = step.endedAt ? new Date(step.endedAt).getTime() : now
  if (Number.isNaN(start) || Number.isNaN(end)) return undefined
  return Math.max(0, (end - start) / 1000)
}

/**
 * The run's steps as rows that open. Closed, a row is the step's name, what
 * it concluded and how long it took; open, it is everything the engine kept
 * about it — when it ran, the evidence it recorded, the error it hit — and,
 * for a step that wrote to the build log, the way to that part of the
 * transcript. The rows used to be one click each into the console, where
 * most steps had written nothing at all; the facts live here now, and the
 * console is offered only where it has something to show.
 */
export function RunSteps({
  steps,
  now,
  withOutput,
  onSelectStep,
}: {
  steps: DeploymentStep[]
  now: number
  /** The steps that wrote at least one line to the build log. */
  withOutput: Set<number>
  onSelectStep: (id: number) => void
}) {
  const passed = steps.filter((step) => step.state === "passed").length
  const skipped = steps.filter((step) => step.state === "skipped").length
  const failed = steps.filter((step) => step.state === "failed" || step.state === "blocked").length

  return (
    <section aria-label="Details" className="space-y-1">
      <p className="numeric text-hint text-muted-foreground">
        {plural(steps.length, "step")} · {passed} passed
        {skipped > 0 && ` · ${skipped} skipped`}
        {failed > 0 && ` · ${failed} failed`}
      </p>
      <Accordion
        type="multiple"
        role="list"
        aria-label="Deployment steps"
        className="border-t border-hairline"
      >
        {steps.map((step) => {
          const seconds = stepDurationSeconds(step, now)
          const facts = evidenceFacts(step.evidence)
          const reading = step.errorMessage ?? readingOf(step.evidence)
          return (
            <AccordionItem
              key={step.id}
              value={String(step.id)}
              role="listitem"
              className="border-hairline"
            >
              <AccordionTrigger className="-mx-2 items-center gap-3 px-2 py-3 hover:bg-row-hover hover:no-underline">
                <span className="flex min-w-0 flex-1 items-center gap-3 text-body">
                  <StepMark state={step.state} />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-medium">{humanize(step.key)}</span>
                    {reading && (
                      <span className="block truncate text-hint font-normal text-muted-foreground">
                        {reading}
                      </span>
                    )}
                  </span>
                  <span className="numeric shrink-0 text-hint font-normal text-muted-foreground">
                    {step.attempt > 1 && `attempt ${step.attempt} · `}
                    {seconds !== undefined ? formatDuration(seconds) : stepStateLabel(step.state)}
                  </span>
                </span>
              </AccordionTrigger>
              <AccordionContent className="pb-4 pl-6.5">
                <div className="space-y-3">
                  <DetailList>
                    <Detail label="Outcome">{stepStateLabel(step.state)}</Detail>
                    {step.startedAt && <Detail label="Started">{timestamp(step.startedAt)}</Detail>}
                    {step.endedAt && <Detail label="Finished">{timestamp(step.endedAt)}</Detail>}
                    {seconds !== undefined && (
                      <Detail label="Took">{formatDuration(seconds)}</Detail>
                    )}
                    {step.errorCode && (
                      <Detail label="Error" className="text-destructive">
                        <span className="font-mono">{step.errorCode}</span>
                        {step.errorMessage && ` — ${step.errorMessage}`}
                      </Detail>
                    )}
                    {facts.map((fact) => (
                      <Detail
                        key={fact.label}
                        label={fact.label}
                        className={fact.mono ? "font-mono break-all" : undefined}
                      >
                        {fact.value}
                      </Detail>
                    ))}
                  </DetailList>
                  {facts.length === 0 && !step.errorCode && (
                    <p className="text-hint text-muted-foreground">
                      The engine kept no evidence for this step.
                    </p>
                  )}
                  {withOutput.has(step.id) && (
                    <Button variant="outline" size="xs" onClick={() => onSelectStep(step.id)}>
                      <Logs className="size-3" /> Build output
                    </Button>
                  )}
                </div>
              </AccordionContent>
            </AccordionItem>
          )
        })}
      </Accordion>
    </section>
  )
}

type Fact = { label: string; value: React.ReactNode; mono?: boolean }

function isPrimitive(value: unknown): value is string | number | boolean {
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean"
}

/** "routeRemoved" → "Route removed", "release_id" → "Release id". */
function factLabel(key: string) {
  const words = key
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/[_-]+/g, " ")
    .toLowerCase()
  return words.charAt(0).toUpperCase() + words.slice(1)
}

/** An identifier, a digest, an address or a path reads better in the mono face. */
function looksLiteral(value: string) {
  return /^(sha256:|[0-9a-f]{12,}$|https?:\/\/|\/)/.test(value)
}

/**
 * The step's evidence as label/value rows: a primitive is its own row, a
 * list of primitives is one row, and anything nested is shown as it was
 * recorded rather than flattened into something it is not.
 */
function evidenceFacts(evidence: Record<string, unknown> | undefined): Fact[] {
  return Object.entries(evidence ?? {}).flatMap(([key, value]): Fact[] => {
    if (value === null || value === undefined || value === "") return []
    const label = factLabel(key)
    if (typeof value === "boolean") return [{ label, value: value ? "yes" : "no" }]
    if (typeof value === "number") return [{ label, value: String(value) }]
    if (typeof value === "string") return [{ label, value, mono: looksLiteral(value) }]
    if (Array.isArray(value) && value.every(isPrimitive))
      return value.length ? [{ label, value: value.map(String).join(", ") }] : []
    return [
      {
        label,
        value: (
          <pre className="font-mono text-hint break-all whitespace-pre-wrap">
            {JSON.stringify(value, null, 2)}
          </pre>
        ),
      },
    ]
  })
}

/** The one line a closed row shows: the reason the engine gave, if it gave one. */
function readingOf(evidence: Record<string, unknown> | undefined) {
  const reason = evidence?.reason
  return typeof reason === "string" && reason ? reason : undefined
}
