"use client"

import { useEffect, useRef } from "react"
import { Logs } from "@/components/icons"
import { clock, plural, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import { useRouter } from "next/navigation"
import type {
  DeploymentDetectionCandidate,
  DeploymentPreflightFinding,
  DeploymentStep,
  DeploymentSummary,
} from "@/lib/types"
import { useMediaQuery } from "@/hooks/use-mobile"
import { Section } from "@/components/page"
import { Group } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { ChipCount } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Disclosure } from "@/components/form"
import { TranscriptRow } from "@/components/transcript-line"
import { ProductGlyph, frameworkProduct, packageManagerProduct } from "@/components/product-logo"
import { Button } from "@/components/ui/button"
import {
  RELEASE_GROUPS,
  StepMark,
  formatDuration,
  frameworkLabel,
  groupedState,
  stageName,
  stepName,
  stepSeconds,
  stepStateLabel,
  type ReleaseNodeState,
} from "@/components/deploy/vocabulary"
import { Segment } from "@/components/deploy/run-pipeline"
import { FindingRow } from "@/components/deploy/deployment-findings"
import {
  attentionFindings,
  findingFixAction,
  settingsPathForField,
} from "@/components/deploy/deploy-check-state"
import type { ConsoleRow } from "@/components/deploy/build-console"
import {
  CodeBlock,
  EvidenceView,
  JsonCode,
  StepTile,
  stepSummary,
} from "@/components/deploy/run-evidence"

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

/** The rail's line from one step's tile to the next, in how the upper one went. */
const LINE: Partial<Record<DeploymentStep["state"], string>> = {
  passed: "bg-success/60",
  warning: "bg-warning/60",
  failed: "bg-destructive/60",
  blocked: "bg-destructive/60",
  running: "bg-brand/60",
}

/**
 * The step the inspector opens on: the one that failed, else the one at work,
 * else the build — what a finished run is opened to read — else the last that
 * ran.
 */
export function initialStep(steps: DeploymentStep[]) {
  return (
    steps.find((step) => step.state === "failed" || step.state === "blocked") ??
    steps.find((step) => step.state === "running") ??
    steps.find((step) => step.key === "build_artifact" && step.state === "passed") ??
    [...steps].reverse().find((step) => step.state !== "pending") ??
    steps[0]
  )
}

/**
 * What the inspector leaves out of a step's record because it is drawn
 * another way or read on another step: preflight's findings as findings, the
 * checkout Fetch source already shows, the plan Prepare build context shows,
 * and the image a build repeats beside the artifacts it made.
 */
function drawnElsewhere(step: DeploymentStep): string[] {
  switch (step.key) {
    case "analyze_plan":
      return ["findings", "candidate", "candidateSource"]
    case "prepare_context":
      return ["source"]
    case "build_artifact": {
      const result = step.evidence?.result
      const artifacts =
        typeof result === "object" && result !== null && "artifacts" in result
          ? (result as { artifacts?: unknown[] }).artifacts
          : undefined
      return artifacts?.length ? ["prepared", "image"] : ["prepared"]
    }
    default:
      return []
  }
}

/**
 * The run's steps, and what the one picked recorded.
 *
 * Its steps used to be an accordion: sixteen rows that each opened in place,
 * so reading two of them pushed the rest of the run a screen apart and the
 * evidence of the one open was a well of JSON between them. It is one working
 * surface now, the shape of the Network Tools page: a rail of the steps beside one
 * inspector, and picking a step fills the inspector rather than unfolding a
 * row. The frame is that surface's, because the rail decides what the
 * inspector shows (§7).
 *
 * **The rail** is the release path again, read down rather than across: each
 * stage the run includes under its name and its segment of the top bar, and
 * under it the stage's steps as a timeline — each step drawn as the product it
 * works with on its tile (`StepTile`), the line between tiles in how the step
 * above it went, its name, what it concluded, how long it took and a bar of
 * where in the run it happened, on one scale for every row, so the step that
 * held the run up is the long bar.
 *
 * **The inspector** is the step's whole record: its state and times, the
 * error it stopped on, preflight's findings for Check plan, the last lines it
 * wrote to the build log with the way to the rest, and its evidence drawn as
 * what each thing is (`EvidenceView`), with the record as it was kept one
 * fold away.
 *
 * Below `lg` the inspector follows the rail, and picking a step brings it
 * into view.
 */
export function RunDetails({
  projectId,
  deployment,
  steps,
  operation,
  now,
  rows,
  lineCounts,
  selected,
  onSelect,
  onShowOutput,
}: {
  /** The project, so a preflight finding can open the settings page holding its field. */
  projectId?: number
  /** What the project is, for the marks of the steps that work with its source and build. */
  deployment?: DeploymentSummary
  steps: DeploymentStep[]
  /** The run's operation, which names a step a Stop or Restart takes against the live release. */
  operation?: string
  now: number
  /** The transcript's lines, for the last few a step wrote. */
  rows: ConsoleRow[]
  /** How many lines each step wrote to the build log; a step that wrote none is absent. */
  lineCounts: Map<number, number>
  selected?: number
  onSelect: (id: number) => void
  /** Opens the build console on one step's output. */
  onShowOutput: (id: number) => void
}) {
  const wide = useMediaQuery("(min-width: 1024px)")
  const rail = useRef<HTMLOListElement>(null)
  const inspector = useRef<HTMLDivElement>(null)
  const current = steps.find((step) => step.id === selected) ?? initialStep(steps)
  const currentId = current?.id

  // The picked step stays in the rail's view — the failed one a run opens on
  // is often past its fold. Scrolled inside the rail only: `scrollIntoView`
  // would move the page under the reader too.
  useEffect(() => {
    const list = rail.current
    const row = list?.querySelector<HTMLElement>('[aria-pressed="true"]')
    if (!list || !row) return
    const box = list.getBoundingClientRect()
    const at = row.getBoundingClientRect()
    if (at.top < box.top || at.bottom > box.bottom)
      list.scrollTop += at.top - box.top - (box.height - at.height) / 2
  }, [currentId])
  const passed = steps.filter((step) => step.state === "passed").length
  const skipped = steps.filter((step) => step.state === "skipped").length
  const failed = steps.filter((step) => step.state === "failed" || step.state === "blocked").length

  const spans = new Map(steps.map((step) => [step.id, stepSpan(step, now)]))
  const known = [...spans.values()].filter((span) => span !== undefined)
  const from = known.length ? Math.min(...known.map((span) => span.start)) : 0
  const to = known.length ? Math.max(...known.map((span) => span.end)) : 0
  const length = Math.max(to - from, 1)

  // The release path's stages, each holding the steps the run planned for
  // it; a step no stage names — the compatibility pipeline's one — closes
  // the rail under a stage of its own.
  const grouped = new Set<number>()
  const stages: { label: string; state: ReleaseNodeState; steps: DeploymentStep[] }[] =
    RELEASE_GROUPS.map((group) => {
      const members = steps.filter((step) => (group.keys as readonly string[]).includes(step.key))
      for (const step of members) grouped.add(step.id)
      return {
        label: stageName(group.label, operation),
        state: groupedState(steps, group.keys),
        steps: members,
      }
    }).filter((stage) => stage.steps.length > 0)
  const rest = steps.filter((step) => !grouped.has(step.id))
  if (rest.length > 0) stages.push({ label: "Other", state: "pending", steps: rest })

  const pick = (id: number) => {
    onSelect(id)
    if (!wide) inspector.current?.scrollIntoView({ behavior: "smooth", block: "start" })
  }

  return (
    <Section
      title="Details"
      actions={
        <p className="numeric text-xs text-muted-foreground">
          {plural(steps.length, "step")} · <span className="text-success">{passed} passed</span>
          {skipped > 0 && ` · ${skipped} skipped`}
          {failed > 0 && (
            <>
              {" · "}
              <span className="text-destructive">{failed} failed</span>
            </>
          )}
        </p>
      }
    >
      <div
        id="run-details"
        className="grid min-w-0 scroll-mt-16 overflow-hidden rounded-xl border bg-card lg:h-[min(80vh,48rem)] lg:grid-cols-[minmax(0,22rem)_minmax(0,1fr)]"
      >
        <ol
          ref={rail}
          aria-label="Deployment steps"
          className="min-h-0 space-y-1 overflow-y-auto p-2 max-lg:border-b lg:border-r"
        >
          {stages.map((stage) => (
            <li key={stage.label} className="min-w-0">
              <StageHead label={stage.label} state={stage.state} steps={stage.steps} now={now} />
              <ol aria-label={stage.label} className="min-w-0">
                {stage.steps.map((step, index) => {
                  const span = spans.get(step.id)
                  return (
                    <StepRow
                      key={step.id}
                      step={step}
                      deployment={deployment}
                      operation={operation}
                      now={now}
                      lines={lineCounts.get(step.id) ?? 0}
                      selected={step.id === current?.id}
                      above={index > 0 ? stage.steps[index - 1].state : undefined}
                      last={index === stage.steps.length - 1}
                      left={span ? ((span.start - from) / length) * 100 : undefined}
                      width={span ? ((span.end - span.start) / length) * 100 : undefined}
                      onSelect={() => pick(step.id)}
                    />
                  )
                })}
              </ol>
            </li>
          ))}
        </ol>
        <div ref={inspector} className="min-h-0 min-w-0 scroll-mt-16 overflow-y-auto">
          {current && (
            <Inspector
              key={current.id}
              projectId={projectId}
              deployment={deployment}
              step={current}
              operation={operation}
              now={now}
              rows={rows}
              lines={lineCounts.get(current.id) ?? 0}
              onShowOutput={() => onShowOutput(current.id)}
            />
          )}
        </div>
      </div>
    </Section>
  )
}

/** A stage's name over its segment of the release path, and how long it took. */
function StageHead({
  label,
  state,
  steps,
  now,
}: {
  label: string
  state: ReleaseNodeState
  steps: DeploymentStep[]
  now: number
}) {
  const seconds = steps.reduce<number | undefined>((total, step) => {
    const each = stepSeconds(step, now)
    return each === undefined ? total : (total ?? 0) + each
  }, undefined)
  return (
    <div aria-hidden className="flex min-w-0 items-center gap-3 px-2 pt-3 pb-1.5">
      <span
        className={cn(
          "eyebrow shrink-0",
          (state === "failed" || state === "blocked") && "text-destructive",
        )}
      >
        {label}
      </span>
      <span className="min-w-0 flex-1">
        <Segment state={state} />
      </span>
      <span className="numeric w-10 shrink-0 text-right text-hint text-muted-foreground">
        {seconds !== undefined ? formatDuration(seconds) : ""}
      </span>
    </div>
  )
}

function StepRow({
  step,
  deployment,
  operation,
  now,
  lines,
  selected,
  above,
  last,
  left,
  width,
  onSelect,
}: {
  step: DeploymentStep
  deployment?: DeploymentSummary
  operation?: string
  now: number
  lines: number
  selected: boolean
  /** How the step above it in its stage went, for the line that runs into this one's tile. */
  above?: DeploymentStep["state"]
  last: boolean
  left?: number
  width?: number
  onSelect: () => void
}) {
  const seconds = stepSeconds(step, now)
  const summary = stepSummary(step)
  const failed = step.state === "failed" || step.state === "blocked"
  const fill = FILL[step.state]
  return (
    <li className="relative min-w-0">
      {/* The timeline through the tiles' centres, from the tile above into
          this one and on to the next, each length in how its step went. */}
      {above !== undefined && (
        <span
          aria-hidden
          className={cn(
            "pointer-events-none absolute top-0 left-6 z-10 h-2.5 w-px",
            LINE[above] ?? "bg-hairline",
          )}
        />
      )}
      {!last && (
        <span
          aria-hidden
          className={cn(
            "pointer-events-none absolute top-10.5 bottom-0 left-6 z-10 w-px",
            LINE[step.state] ?? "bg-hairline",
          )}
        />
      )}
      <button
        type="button"
        aria-pressed={selected}
        onClick={onSelect}
        className={cn(
          "relative flex w-full min-w-0 items-start gap-3 rounded-lg px-2 py-2.5 text-left focus-ring-inset transition-colors",
          selected ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        <StepTile step={step} state={step.state} deployment={deployment} />
        <span className="min-w-0 flex-1">
          <span className="flex min-w-0 items-baseline gap-2">
            <span
              className={cn(
                "min-w-0 flex-1 truncate text-body font-medium",
                failed && "text-destructive",
                step.state === "pending" && "text-muted-foreground",
              )}
            >
              {stepName(step.key, operation)}
            </span>
            <span className="numeric shrink-0 text-hint text-muted-foreground">
              {step.attempt > 1 && `attempt ${step.attempt} · `}
              {seconds !== undefined ? formatDuration(seconds) : stepStateLabel(step.state)}
            </span>
          </span>
          <span className="flex min-w-0 items-baseline gap-2 text-hint text-muted-foreground">
            <span className="min-w-0 flex-1 truncate">{summary}</span>
            {lines > 0 && (
              <span className="numeric shrink-0 text-muted-foreground/70">
                {plural(lines, "line")}
              </span>
            )}
          </span>
          {/* Where in the run it happened, on the run's own scale: a picture
              of the duration beside the name, so hidden from a reader that
              has the words. */}
          <span
            aria-hidden
            className="relative mt-1.5 block h-1 overflow-hidden rounded-full bg-meter-track"
          >
            {fill && left !== undefined && width !== undefined && (
              <span
                className={cn("absolute inset-y-0 min-w-1 rounded-full", fill)}
                style={{ left: `${Math.min(left, 99)}%`, width: `${width}%` }}
              />
            )}
          </span>
        </span>
      </button>
    </li>
  )
}

const STEP_TONE: Partial<
  Record<DeploymentStep["state"], "running" | "warning" | "danger" | "stopped">
> = {
  passed: "running",
  running: "warning",
  warning: "warning",
  failed: "danger",
  blocked: "danger",
}

/** How many of a step's last lines the inspector shows before the console takes over. */
const TAIL = 8

/** One step's whole record: its state and times, its error, its output's end and its evidence. */
function Inspector({
  projectId,
  deployment,
  step,
  operation,
  now,
  rows,
  lines,
  onShowOutput,
}: {
  projectId?: number
  deployment?: DeploymentSummary
  step: DeploymentStep
  operation?: string
  now: number
  rows: ConsoleRow[]
  lines: number
  onShowOutput: () => void
}) {
  const seconds = stepSeconds(step, now)
  const evidence = step.evidence ?? {}
  const preflight = step.key === "analyze_plan" ? preflightOf(evidence) : undefined
  const tail = rows.filter((row) => row.stepId === step.id).slice(-TAIL)
  const recorded = Object.keys(evidence).length > 0
  const name = stepName(step.key, operation)
  return (
    <section aria-label={name} className="min-w-0 animate-rise space-y-6 p-4 sm:p-5">
      <header className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-3">
        <div className="flex min-w-0 flex-1 items-center gap-3">
          <StepTile step={step} state={step.state} deployment={deployment} size="md" />
          <div className="min-w-0 space-y-1">
            <h3 className="truncate text-title font-semibold tracking-tight">{name}</h3>
            <p className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
              <Status
                tone={STEP_TONE[step.state] ?? "stopped"}
                label={stepStateLabel(step.state)}
                live={step.state === "running"}
              />
              {seconds !== undefined && (
                <span>
                  took <span className="numeric text-foreground">{formatDuration(seconds)}</span>
                </span>
              )}
              {step.startedAt && (
                <span className="numeric" title={timestamp(step.startedAt)}>
                  {clock(step.startedAt)}
                  {step.endedAt && ` → ${clock(step.endedAt)}`}
                </span>
              )}
              {step.attempt > 1 && <span>attempt {step.attempt}</span>}
            </p>
          </div>
        </div>
        {lines > 0 && (
          <Button variant="outline" size="sm" className="shrink-0" onClick={onShowOutput}>
            <Logs className="size-3.5" /> Build output
            <span aria-hidden>
              <ChipCount>{lines}</ChipCount>
            </span>
          </Button>
        )}
      </header>

      {step.errorCode && (
        <Group tone="danger" className="space-y-1.5">
          <Tag mono className="text-destructive">
            {step.errorCode}
          </Tag>
          {step.errorMessage && (
            <p className="font-mono text-xs leading-relaxed wrap-anywhere text-foreground">
              {step.errorMessage}
            </p>
          )}
        </Group>
      )}

      {preflight && <PreflightChecks projectId={projectId} {...preflight} />}

      {tail.length > 0 && (
        <figure className="min-w-0 overflow-hidden rounded-lg border border-hairline bg-surface-sunken">
          <figcaption className="flex min-h-8 items-center gap-2 border-b border-hairline px-3 py-1 text-hint text-muted-foreground">
            <Logs aria-hidden className="size-3.5" />
            <span className="font-medium text-foreground">Output</span>
            <span className="numeric">
              {lines > tail.length
                ? `last ${tail.length} of ${lines} lines`
                : plural(lines, "line")}
            </span>
          </figcaption>
          <ol aria-label={`${name} output`} className="py-1.5 font-mono text-xs leading-6">
            {tail.map((row) => (
              <TranscriptRow key={row.id} line={row.line} wrap needle="" tokens />
            ))}
          </ol>
        </figure>
      )}

      {recorded ? (
        <EvidenceView evidence={evidence} omit={drawnElsewhere(step)} />
      ) : (
        <p className="text-body text-muted-foreground">
          {step.state === "pending"
            ? "The run has not reached this step."
            : step.state === "running"
              ? "What it records arrives when it finishes."
              : "The engine kept no evidence for this step."}
        </p>
      )}

      {recorded && (
        <Disclosure quiet summary="Recorded evidence">
          <div className="pt-2">
            <CodeBlock title="Evidence" copy={JSON.stringify(evidence, null, 2)}>
              <JsonCode value={evidence} />
            </CodeBlock>
          </div>
        </Disclosure>
      )}
    </section>
  )
}

type Preflight = {
  findings: DeploymentPreflightFinding[]
  candidate?: DeploymentDetectionCandidate
  candidateSource?: string
}

/**
 * analyze_plan's evidence: what preflight found and the candidate it judged
 * the plan against — drawn as findings rather than as a record, beside the
 * digests the step also keeps.
 */
function preflightOf(evidence: Record<string, unknown>): Preflight | undefined {
  if (!Array.isArray(evidence.findings)) return undefined
  return {
    findings: evidence.findings as DeploymentPreflightFinding[],
    candidate: evidence.candidate as DeploymentDetectionCandidate | undefined,
    candidateSource:
      typeof evidence.candidateSource === "string" ? evidence.candidateSource : undefined,
  }
}

/**
 * What the candidate was, as a line: the framework and the package manager,
 * each with its own mark where it has one.
 */
function candidateLine(candidate: DeploymentDetectionCandidate) {
  const framework = frameworkProduct(candidate.framework)
  const packageManager = packageManagerProduct(candidate.packageManager)
  return (
    <>
      {framework && <ProductGlyph id={framework} className="mr-1 inline align-[-2px]" />}
      {candidate.framework ? frameworkLabel(candidate.framework) : candidate.name}
      {candidate.packageManager && (
        <>
          {" · "}
          {packageManager && (
            <ProductGlyph id={packageManager} className="mr-1 inline align-[-2px]" />
          )}
          {candidate.packageManager}
        </>
      )}
      {candidate.root && candidate.root !== "." && ` · in ${candidate.root}`}
    </>
  )
}

/**
 * "Checked before building": the step's findings, each with what to do and
 * the way to its field, and what the plan was judged against — the commit's
 * own detection, or the evidence saved with the plan when the commit could
 * not be read.
 */
function PreflightChecks({
  projectId,
  findings,
  candidate,
  candidateSource,
}: Preflight & {
  projectId?: number
}) {
  const router = useRouter()
  const attention = attentionFindings(findings)
  const counts = (["blocked", "decision", "warning", "unavailable"] as const)
    .map(
      (severity) =>
        [severity, attention.filter((item) => item.severity === severity).length] as const,
    )
    .filter(([, count]) => count > 0)
    .map(([severity, count]) =>
      severity === "blocked"
        ? `${count} blocked`
        : severity === "decision"
          ? plural(count, "decision")
          : severity === "warning"
            ? plural(count, "warning")
            : `${count} unavailable`,
    )
  const judged =
    candidate && candidateSource === "detected" ? (
      <>read from this commit: {candidateLine(candidate)}</>
    ) : candidate && candidateSource === "recorded" ? (
      <>read from the evidence saved with the plan: {candidateLine(candidate)}</>
    ) : undefined
  return (
    <section aria-label="Checked before building" className="space-y-2">
      <p className="text-xs text-muted-foreground">
        <StepMark
          state={counts.length ? "warning" : "passed"}
          className="mr-1.5 inline-flex align-middle"
        />
        Checked before building:{" "}
        {counts.length
          ? counts.join(", ")
          : `every one of ${plural(findings.length, "check")} passed`}
        {judged && <> · {judged}</>}
      </p>
      {attention.map((finding, index) => (
        <FindingRow
          key={`${finding.code}:${finding.fieldId ?? index}`}
          finding={finding}
          index={index}
          canOpenRemedy={projectId !== undefined && Boolean(settingsPathForField(finding.fieldId))}
          onOpenRemedy={(item) => {
            const path = settingsPathForField(item.fieldId)
            if (projectId !== undefined && path) router.push(`/deploy/${projectId}${path}`)
          }}
          fixAction={
            projectId === undefined
              ? undefined
              : findingFixAction(projectId, finding, (href) => router.push(href))
          }
        />
      ))}
    </section>
  )
}
