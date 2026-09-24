"use client"

import { Logs } from "@/components/icons"
import { plural, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { DeploymentStep } from "@/lib/types"
import { useMediaQuery } from "@/hooks/use-mobile"
import { Well } from "@/components/panel"
import { Detail, DetailList } from "@/components/page"
import { ChipCount } from "@/components/tabs"
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion"
import { Button } from "@/components/ui/button"
import {
  StepMark,
  formatDuration,
  stepName,
  stepSeconds,
  stepStateLabel,
} from "@/components/deploy/vocabulary"

/** Where a step sits in the run's time, as start and end in milliseconds. */
function stepSpan(step: DeploymentStep, now: number) {
  if (!step.startedAt) return undefined
  const start = new Date(step.startedAt).getTime()
  if (Number.isNaN(start)) return undefined
  // A step still going runs to now; one that ended without a clock is a
  // moment, drawn as the narrowest mark rather than stretched to the present.
  const end = step.endedAt
    ? new Date(step.endedAt).getTime()
    : step.state === "running"
      ? now
      : start
  return { start, end: Math.max(start, Number.isNaN(end) ? start : end) }
}

/** The fill of a step's bar, in the release path's own colours (`Segment`). */
const FILL: Partial<Record<DeploymentStep["state"], string>> = {
  passed: "bg-success",
  failed: "bg-destructive",
  blocked: "bg-destructive",
  warning: "bg-warning",
  running: "bg-brand",
  skipped: "bg-muted-foreground/40",
  cancelled: "bg-muted-foreground/40",
  unavailable: "bg-muted-foreground/40",
}

/**
 * The run's steps as rows that open. Closed, a row is the step's mark, its
 * name, what it concluded, where in the run it happened and how long it took;
 * open, it is everything the engine kept about it — when it ran, the evidence
 * it recorded, the error it hit — and, for a step that wrote to the build log,
 * the way to that part of the transcript.
 *
 * **Where in the run** is a waterfall: each row's bar starts where the step
 * started in the run and is as long as the step took, on one scale for every
 * row, so the step that held the run up is the long bar and two steps that
 * ran together overlap. It is drawn in the release path's colours and is a
 * picture of the duration beside it, which stays the reading. On a phone it
 * goes under the name at the row's full width — chosen once, not drawn twice.
 *
 * Every mark sits in one fixed box (`StepMark`), so the names start on one
 * line whether a step passed (a tick) or is waiting (a ring).
 */
export function RunSteps({
  steps,
  now,
  lineCounts,
  onSelectStep,
}: {
  steps: DeploymentStep[]
  now: number
  /** How many lines each step wrote to the build log; a step that wrote none is absent. */
  lineCounts: Map<number, number>
  onSelectStep: (id: number) => void
}) {
  const wide = useMediaQuery("(min-width: 640px)")
  const passed = steps.filter((step) => step.state === "passed").length
  const skipped = steps.filter((step) => step.state === "skipped").length
  const failed = steps.filter((step) => step.state === "failed" || step.state === "blocked").length

  const spans = steps.map((step) => stepSpan(step, now))
  const known = spans.filter((span) => span !== undefined)
  const from = known.length ? Math.min(...known.map((span) => span.start)) : 0
  const to = known.length ? Math.max(...known.map((span) => span.end)) : 0
  const span = Math.max(to - from, 1)

  return (
    <section aria-label="Details" className="space-y-1">
      <p className="numeric text-hint text-muted-foreground">
        {plural(steps.length, "step")} · <span className="text-success">{passed} passed</span>
        {skipped > 0 && ` · ${skipped} skipped`}
        {failed > 0 && (
          <>
            {" · "}
            <span className="text-destructive">{failed} failed</span>
          </>
        )}
      </p>
      <Accordion
        type="multiple"
        role="list"
        aria-label="Deployment steps"
        className="border-t border-hairline"
      >
        {steps.map((step, index) => {
          const seconds = stepSeconds(step, now)
          const facts = evidenceFacts(step.evidence)
          const reading = step.errorMessage ?? readingOf(step.evidence)
          const lines = lineCounts.get(step.id) ?? 0
          const place = spans[index]
          const bar = (
            <Waterfall
              state={step.state}
              left={place ? ((place.start - from) / span) * 100 : undefined}
              width={place ? ((place.end - place.start) / span) * 100 : undefined}
              className={wide ? "w-48 shrink-0" : "mt-2"}
            />
          )
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
                    <span
                      className={cn(
                        "block truncate font-medium",
                        (step.state === "failed" || step.state === "blocked") && "text-destructive",
                        step.state === "pending" && "text-muted-foreground",
                      )}
                    >
                      {stepName(step.key)}
                    </span>
                    {reading && (
                      <span className="block truncate text-hint font-normal text-muted-foreground">
                        {reading}
                      </span>
                    )}
                    {!wide && bar}
                  </span>
                  {wide && bar}
                  {/* One measure on every row, so each row's track ends on one line
                      and the bars share a time axis on a phone too. */}
                  <span className="numeric w-28 shrink-0 text-right text-hint font-normal text-muted-foreground">
                    {step.attempt > 1 && `attempt ${step.attempt} · `}
                    {seconds !== undefined ? formatDuration(seconds) : stepStateLabel(step.state)}
                    {lines > 0 && (
                      <span className="text-muted-foreground/70"> · {plural(lines, "line")}</span>
                    )}
                  </span>
                </span>
              </AccordionTrigger>
              <AccordionContent className="pb-4 pl-6.5">
                <div className="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-start sm:justify-between sm:gap-6">
                  <div className="min-w-0 flex-1 space-y-3">
                    <DetailList>
                      {step.errorCode && (
                        <Detail label="Error" className="text-destructive">
                          <span className="rounded-sm bg-wash-danger px-1 font-mono text-xs">
                            {step.errorCode}
                          </span>
                          {step.errorMessage && ` ${step.errorMessage}`}
                        </Detail>
                      )}
                      <Detail label="Outcome">{stepStateLabel(step.state)}</Detail>
                      {step.startedAt && (
                        <Detail label="Started">{timestamp(step.startedAt)}</Detail>
                      )}
                      {step.endedAt && <Detail label="Finished">{timestamp(step.endedAt)}</Detail>}
                      {seconds !== undefined && (
                        <Detail label="Took">{formatDuration(seconds)}</Detail>
                      )}
                      {facts.map((fact) => (
                        <Detail
                          key={fact.label}
                          label={fact.label}
                          className={fact.mono ? "font-mono wrap-anywhere" : undefined}
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
                  </div>
                  {lines > 0 && (
                    <Button
                      variant="outline"
                      size="xs"
                      className="w-fit shrink-0"
                      onClick={() => onSelectStep(step.id)}
                    >
                      <Logs className="size-3" /> Build output
                      <span aria-hidden>
                        <ChipCount>{lines}</ChipCount>
                      </span>
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

/**
 * One step's place in the run: a track the width of the run, and the step
 * drawn where and for as long as it ran. A picture of the duration beside it,
 * so it is hidden from the reader that has the words.
 */
function Waterfall({
  state,
  left,
  width,
  className,
}: {
  state: DeploymentStep["state"]
  left?: number
  width?: number
  className?: string
}) {
  const fill = FILL[state]
  return (
    <span
      aria-hidden
      className={cn("relative block h-1.5 overflow-hidden rounded-full bg-meter-track", className)}
    >
      {fill && left !== undefined && width !== undefined && (
        <span
          className={cn("absolute inset-y-0 min-w-1.5 rounded-full", fill)}
          style={{ left: `${Math.min(left, 99)}%`, width: `${width}%` }}
        />
      )}
    </span>
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
 * recorded — in a well, because it is output to read rather than a fact to
 * scan — rather than flattened into something it is not.
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
          <Well className="max-h-64 text-hint wrap-anywhere whitespace-pre-wrap">
            {JSON.stringify(value, null, 2)}
          </Well>
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
