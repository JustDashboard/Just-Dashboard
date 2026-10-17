"use client"

import { useMemo, useState } from "react"
import { ChevronDown, ChevronRight, Copy, Route } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { cn } from "@/lib/utils"
import { Segments } from "@/components/deploy/settings/segments"
import { Well } from "@/components/panel"
import { EmptyState } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { StatementLine } from "@/components/database/query/messages"
import {
  estimateText,
  planFromRows,
  planOf,
  planRows,
  planText,
  readingsOf,
  rowsText,
  shareText,
  slowestFirst,
  type Plan,
  type PlanRow,
} from "@/components/database/query/plan"
import { planFromText } from "@/components/database/query/plan-text"
import { ResultGrid } from "@/components/database/query/result-grid"
import { spanText, type Explained } from "@/components/database/query/run-model"

/** The three measures of a step, each in its own series colour wherever it is drawn. */
const MEASURES = {
  cost: { label: "Cost", color: "var(--chart-2)" },
  rows: { label: "Rows", color: "var(--chart-4)" },
  time: { label: "Time", color: "var(--chart-3)" },
} as const

const figure = (value: number, digits = 2) =>
  value.toLocaleString("en-US", {
    maximumFractionDigits: Math.abs(value) >= 100 ? 0 : digits,
  })

/**
 * A statement's plan: what the engine will do, or — measured — what it did.
 *
 * Where the engine hands the plan over as a structure, or prints one this
 * reads, it is drawn as a tree, each step with three bars: the share of the
 * plan's cost that is the step's own, its rows against the busiest step's,
 * and, when the statement was run, its time. The costly step is then the long
 * bar, wherever it sits in the tree. A measured plan can also be read slowest
 * first, and a step whose rows came out ten times more or fewer than planned
 * says so under its name: that is usually where the engine chose its plan on
 * a wrong count. What a plan does not carry is not drawn. Where the engine's
 * print is not one this reads, the print is what is shown.
 */
export function PlanView({
  explained,
  onExplain,
  canAnalyze,
  why,
}: {
  explained: Explained | undefined
  /** Asks for the plan of the statement under the cursor. */
  onExplain: (analyze: boolean) => void
  /** The engine can measure a plan and the role may run statements. */
  canAnalyze: boolean
  /** Why there is no statement to explain, when there is none. */
  why?: string
}) {
  const answer = explained?.answer
  const plan = useMemo<Plan | null>(() => {
    if (!answer) return null
    if (answer.plan !== undefined) return planOf(answer.plan)
    const lines = planText(answer.result)
    return planFromRows(answer.result) ?? (lines === null ? null : planFromText(lines))
  }, [answer])
  const rows = useMemo(() => (plan ? planRows(plan) : []), [plan])
  const timed = rows.some((row) => row.selfMs !== undefined)
  const raw = useMemo(() => {
    if (!answer) return ""
    if (answer.plan !== undefined) return JSON.stringify(answer.plan, null, 2)
    return planText(answer.result) ?? ""
  }, [answer])
  const [form, setForm] = useState<"tree" | "text">("tree")
  const [order, setOrder] = useState<"plan" | "slowest">("plan")
  const ordered = useMemo(
    () => (timed && order === "slowest" ? slowestFirst(rows) : rows),
    [rows, timed, order],
  )

  if (!explained) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center overflow-auto p-6">
        <EmptyState
          className="border-0 py-6"
          icon={Route}
          title="No plan yet"
          description={
            why ??
            "Explain shows how the engine would run the statement under the cursor: which indexes it reads, in what order, and what each step costs. Nothing is run."
          }
          action={
            !why && (
              <div className="flex flex-wrap justify-center gap-2">
                <Button size="sm" variant="outline" onClick={() => onExplain(false)}>
                  Explain
                </Button>
                {canAnalyze && (
                  <Button size="sm" variant="outline" onClick={() => onExplain(true)}>
                    Explain and measure
                  </Button>
                )}
              </div>
            )
          }
        />
      </div>
    )
  }

  const head = (
    <div className="flex min-h-9 shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-hairline px-2.5 py-1">
      <StatementLine sql={explained.sql} className="flex-1 basis-40" />
      {explained.phase === "done" && answer && (
        <>
          <Tag>{answer.analyzed ? "measured" : "estimated"}</Tag>
          {answer.rolledBack && <Tag>changes undone</Tag>}
          {plan?.planningMs !== undefined && (
            <span className="numeric text-hint text-muted-foreground">
              planned in {spanText(plan.planningMs)}
            </span>
          )}
          {plan?.executionMs !== undefined && (
            <span className="numeric text-hint text-muted-foreground">
              ran in {spanText(plan.executionMs)}
            </span>
          )}
          {plan && timed && form === "tree" && (
            <Segments
              label="Order of the steps"
              value={order}
              onChange={setOrder}
              options={[
                { value: "plan", label: "Plan order" },
                { value: "slowest", label: "Slowest first" },
              ]}
            />
          )}
          {plan && raw && (
            <Segments
              label="Plan as"
              value={form}
              onChange={setForm}
              options={[
                { value: "tree", label: "Tree" },
                { value: "text", label: "Text" },
              ]}
            />
          )}
          {raw && (
            <Button size="xs" variant="ghost" onClick={() => void copyText(raw, "Plan copied")}>
              <Copy />
              Copy
            </Button>
          )}
        </>
      )}
    </div>
  )

  let body: React.ReactNode
  if (explained.phase === "running") {
    body = (
      <div role="status" className="flex min-h-0 flex-1 flex-col">
        <div aria-hidden className="h-0.5 shrink-0 overflow-hidden bg-meter-track">
          <div className="h-full w-1/3 animate-sweep bg-primary" />
        </div>
        <p className="p-4 text-body">
          <TextShimmer>
            {explained.analyze ? "Running the statement to measure it…" : "Asking for the plan…"}
          </TextShimmer>
        </p>
      </div>
    )
  } else if (explained.refused || explained.error) {
    body = (
      <div className="min-h-0 flex-1 overflow-auto p-3">
        <div
          role="alert"
          className={cn(
            "space-y-1.5 rounded-lg border p-3",
            explained.refused
              ? "border-rule-warning bg-wash-warning"
              : "border-rule-danger bg-wash-danger",
          )}
        >
          <p className="text-body font-medium">
            {explained.refused ? "No plan was asked for" : "The engine would not explain this"}
          </p>
          <p className="font-mono text-xs leading-relaxed break-words whitespace-pre-wrap">
            {explained.refused ?? explained.error}
          </p>
        </div>
      </div>
    )
  } else if (plan && form === "tree") {
    body = <PlanTree rows={ordered} />
  } else if (raw) {
    body = (
      <div className="min-h-0 flex-1 overflow-auto p-3">
        <Well className="leading-relaxed whitespace-pre">{raw}</Well>
      </div>
    )
  } else if (answer) {
    // A plan that came as a table of several columns and names no parents: the table is the plan.
    body = (
      <ResultGrid
        label="Query plan"
        result={answer.result}
        resultKey={`plan-${explained.startedAt}`}
      />
    )
  }

  return (
    <div className="flex min-h-0 min-w-0 flex-1 animate-rise flex-col" key={explained.startedAt}>
      {head}
      {body}
    </div>
  )
}

function PlanTree({ rows }: { rows: PlanRow[] }) {
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set())
  const timed = rows.some((row) => row.timeMs !== undefined)
  const costed = rows.some((row) => row.cost !== undefined)
  const counted = rows.some(
    (row) => row.node.rows !== undefined || row.node.actualRows !== undefined,
  )
  const toggle = (id: string) =>
    setOpen((held) => {
      const next = new Set(held)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  return (
    <div data-slot="plan-tree" className="@container min-h-0 flex-1 overflow-auto">
      <div className="min-w-0">
        <div className="sticky top-0 z-10 flex h-8 items-center gap-3 border-b border-hairline bg-card px-2.5 text-hint font-medium text-muted-foreground">
          <span className="min-w-0 flex-1">Step</span>
          {costed && <MeasureHead measure="cost" />}
          {counted && <MeasureHead measure="rows" wide />}
          {timed && <MeasureHead measure="time" />}
        </div>
        <ul>
          {rows.map((row) => (
            <PlanLine
              key={row.id}
              row={row}
              open={open.has(row.id)}
              onToggle={() => toggle(row.id)}
              costed={costed}
              counted={counted}
              timed={timed}
            />
          ))}
        </ul>
      </div>
    </div>
  )
}

/** A measure's column: a figure on a narrow pane, its bar beside it from a wide one. */
const measureWidth = (wide?: boolean) =>
  cn("shrink-0", wide ? "w-16" : "w-14", "@2xl:w-32 @5xl:w-52")

function MeasureHead({ measure, wide }: { measure: keyof typeof MEASURES; wide?: boolean }) {
  const spec = MEASURES[measure]
  return (
    <span className={cn("flex items-center justify-end gap-1.5", measureWidth(wide))}>
      <span aria-hidden className="size-1.5 rounded-full" style={{ background: spec.color }} />
      {spec.label}
    </span>
  )
}

function PlanLine({
  row,
  open,
  onToggle,
  costed,
  counted,
  timed,
}: {
  row: PlanRow
  open: boolean
  onToggle: () => void
  costed: boolean
  counted: boolean
  timed: boolean
}) {
  const { node, estimate } = row
  const readings = readingsOf(row)
  const detailed = node.notes.length > 0 || node.facts.length > 0 || readings.length > 0
  const seen = node.actualRows ?? node.rows
  return (
    <li data-plan-step={row.id} className="border-b border-hairline last:border-b-0">
      <div className="flex gap-3 px-2.5 hover:bg-row-hover">
        <div className="flex min-w-0 flex-1">
          {/* The tree's rules: a line down through each level that still has
              steps to come, and an elbow into this one at its name's line. */}
          {row.rails.map((runs, index) => (
            <span
              key={index}
              aria-hidden
              className={cn("w-4 shrink-0", runs && "border-l border-hairline")}
            />
          ))}
          {row.depth > 0 && (
            <span
              aria-hidden
              className={cn("relative w-4 shrink-0", !row.last && "border-l border-hairline")}
            >
              <span
                className={cn(
                  "absolute top-0 h-4 w-3 border-b border-hairline",
                  row.last ? "left-0 rounded-bl-sm border-l" : "-left-px",
                )}
              />
            </span>
          )}
          <div className="min-w-0 flex-1">
            <div className="flex h-8 min-w-0 items-center">
              {detailed ? (
                <button
                  type="button"
                  aria-expanded={open}
                  onClick={onToggle}
                  className="flex h-full min-w-0 items-center gap-1 rounded-sm text-left focus-ring-inset"
                >
                  {open ? (
                    <ChevronDown aria-hidden className="size-3 shrink-0 text-muted-foreground" />
                  ) : (
                    <ChevronRight aria-hidden className="size-3 shrink-0 text-muted-foreground" />
                  )}
                  <StepName node={node} />
                </button>
              ) : (
                <span className="flex min-w-0 items-center gap-1 pl-4">
                  <StepName node={node} />
                </span>
              )}
            </div>
            {/* What happened against what was planned, where the two are far
                apart, is a fact of the step and is read with its name. */}
            {estimate?.farOff ? (
              <p
                data-slot="plan-estimate"
                className="numeric -mt-1.5 pb-1.5 pl-4 text-hint leading-snug text-warning"
              >
                <span className="sr-only">Far from the planner&apos;s estimate: </span>
                {estimateText(estimate)}
              </p>
            ) : (
              row.never && (
                <p className="-mt-1.5 pb-1.5 pl-4 text-hint leading-snug text-muted-foreground">
                  Never ran
                </p>
              )
            )}
          </div>
        </div>
        <div className="flex h-8 shrink-0 items-center gap-3">
          {costed && (
            <Measure
              measure="cost"
              share={row.costShare}
              value={row.cost === undefined ? undefined : figure(row.cost)}
              title={
                row.costShare === undefined
                  ? undefined
                  : `${shareText(row.costShare)} of the plan's cost is this step's own`
              }
            />
          )}
          {counted && (
            <Measure
              wide
              measure="rows"
              share={row.rowsShare}
              value={seen === undefined ? undefined : rowsText(seen)}
              title={
                estimate
                  ? `Expected ${rowsText(estimate.expected)}, got ${rowsText(estimate.actual)}${node.loops && node.loops > 1 ? ` over ${rowsText(node.loops)} loops` : ""}`
                  : node.actualRows === undefined
                    ? "Rows the planner expects"
                    : undefined
              }
            />
          )}
          {timed && (
            <Measure
              measure="time"
              share={row.timeShare}
              reach={row.node.children.length > 0 ? row.totalShare : undefined}
              value={row.selfMs === undefined ? undefined : spanText(row.selfMs)}
              title={
                row.selfMs === undefined || row.timeShare === undefined
                  ? undefined
                  : `${spanText(row.selfMs)} in this step itself, ${shareText(row.timeShare)} of the plan's time${row.node.children.length > 0 && row.timeMs !== undefined && row.totalShare !== undefined ? `; ${spanText(row.timeMs)} with the steps under it, ${shareText(row.totalShare)}` : ""}`
              }
            />
          )}
        </div>
      </div>
      {open && detailed && <StepDetail row={row} readings={readings} />}
    </li>
  )
}

/** What opens under a step: its conditions, then what it measured and what the engine said of it. */
function StepDetail({ row, readings }: { row: PlanRow; readings: [string, string][] }) {
  const facts = [...readings, ...row.node.facts]
  return (
    <div
      className="space-y-1.5 pr-2.5 pb-2 text-hint"
      style={{
        paddingLeft: `${0.625 + (row.rails.length + (row.depth > 0 ? 1 : 0)) * 1 + 1.25}rem`,
      }}
    >
      {row.node.notes.map((note, index) => (
        <p key={index} className="font-mono leading-relaxed break-words text-muted-foreground">
          {note}
        </p>
      ))}
      {facts.length > 0 && (
        <dl className="flex flex-wrap gap-x-4 gap-y-1">
          {facts.map(([label, value], index) => (
            <div key={`${label}-${index}`} className="flex min-w-0 items-baseline gap-1.5">
              <dt className="shrink-0 text-muted-foreground">{label}</dt>
              <dd className="min-w-0 font-mono break-all">{value}</dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  )
}

function StepName({ node }: { node: PlanRow["node"] }) {
  return (
    <>
      <span className="min-w-0 truncate text-xs font-medium">{node.title}</span>
      {node.target && (
        <span className="min-w-0 shrink-[4] truncate pl-1 font-mono text-xs text-muted-foreground">
          {node.target}
        </span>
      )}
    </>
  )
}

const percent = (share: number) => `${Math.max(share > 0 ? 4 : 0, Math.round(share * 100))}%`

/**
 * One figure of a step with its bar: the bar is the step's share, the figure
 * its value. A time's bar has a second, fainter ink behind the first: the
 * share of the plan spent in the step and everything under it, so one bar
 * says both what the step took itself and what its part of the tree took.
 */
function Measure({
  measure,
  share,
  reach,
  value,
  title,
  wide,
}: {
  measure: keyof typeof MEASURES
  share: number | undefined
  reach?: number
  value: string | undefined
  title?: string
  wide?: boolean
}) {
  const color = MEASURES[measure].color
  return (
    <span title={title} className={cn("flex items-center justify-end gap-2", measureWidth(wide))}>
      {/* The bar gives way before the figure does: on a narrow pane the numbers
          are the reading. On a wide one it grows with the pane, so the figures
          stay near the step they belong to instead of across an empty row. */}
      {share !== undefined && (
        <span
          aria-hidden
          className="relative hidden h-1 w-12 shrink-0 overflow-hidden rounded-full bg-meter-track @2xl:block @5xl:w-28"
        >
          {reach !== undefined && (
            <span
              className="absolute inset-y-0 left-0 rounded-full opacity-35"
              style={{ width: percent(reach), background: color }}
            />
          )}
          <span
            className="relative block h-full rounded-full"
            style={{ width: percent(share), background: color }}
          />
        </span>
      )}
      <span className="numeric min-w-0 truncate text-right text-xs">
        {value ?? <span className="text-muted-foreground">—</span>}
      </span>
    </span>
  )
}
