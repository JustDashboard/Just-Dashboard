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
  planFromRows,
  planOf,
  planRows,
  planText,
  type Plan,
  type PlanRow,
} from "@/components/database/query/plan"
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
 * Where the engine hands the plan over as a structure it is drawn as a tree,
 * each step with three bars: the share of the plan's cost that is the step's
 * own, its rows against the busiest step's, and, when the statement was run,
 * the share of the time. The costly step is then the long bar, wherever it
 * sits in the tree. Where the engine only prints its plan, the print is what
 * is shown.
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
    return answer.plan !== undefined ? planOf(answer.plan) : planFromRows(answer.result)
  }, [answer])
  const raw = useMemo(() => {
    if (!answer) return ""
    if (answer.plan !== undefined) return JSON.stringify(answer.plan, null, 2)
    return planText(answer.result) ?? ""
  }, [answer])
  const [form, setForm] = useState<"tree" | "text">("tree")

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
    body = <PlanTree plan={plan} />
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

function PlanTree({ plan }: { plan: Plan }) {
  const rows = useMemo(() => planRows(plan), [plan])
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

function MeasureHead({ measure, wide }: { measure: keyof typeof MEASURES; wide?: boolean }) {
  const spec = MEASURES[measure]
  return (
    <span
      className={cn(
        "flex shrink-0 items-center justify-end gap-1.5",
        wide ? "w-24 @2xl:w-44 @5xl:w-64" : "w-14 @2xl:w-32 @5xl:w-52",
      )}
    >
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
  const { node } = row
  const detailed = node.notes.length > 0 || node.facts.length > 0
  const seen = node.actualRows ?? node.rows
  return (
    <li data-plan-step={row.id} className="border-b border-hairline last:border-b-0">
      <div className="flex h-8 items-center gap-3 px-2.5 hover:bg-row-hover">
        <div className="flex h-full min-w-0 flex-1 items-center">
          {/* The tree's rules: a line down through each level that still has
              steps to come, and an elbow into this one. */}
          {row.rails.map((runs, index) => (
            <span
              key={index}
              aria-hidden
              className={cn("w-4 shrink-0 self-stretch", runs && "border-l border-hairline")}
            />
          ))}
          {row.depth > 0 && (
            <span
              aria-hidden
              className={cn(
                "relative w-4 shrink-0 self-stretch",
                !row.last && "border-l border-hairline",
              )}
            >
              <span
                className={cn(
                  "absolute top-0 h-1/2 w-3 border-b border-hairline",
                  row.last ? "left-0 rounded-bl-sm border-l" : "-left-px",
                )}
              />
            </span>
          )}
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
        {costed && (
          <Measure
            measure="cost"
            share={row.costShare}
            value={row.cost === undefined ? undefined : figure(row.cost)}
            title={
              row.costShare === undefined
                ? undefined
                : `${Math.round(row.costShare * 100)}% of the plan's cost is this step's own`
            }
          />
        )}
        {counted && (
          <Measure
            wide
            measure="rows"
            share={row.rowsShare}
            value={
              seen === undefined
                ? undefined
                : node.actualRows !== undefined && node.rows !== undefined
                  ? `${figure(node.rows, 0)} → ${figure(node.actualRows, 0)}`
                  : figure(seen, 0)
            }
            title={
              node.actualRows !== undefined && node.rows !== undefined
                ? `Expected ${figure(node.rows, 0)}, got ${figure(node.actualRows, 0)}${node.loops && node.loops > 1 ? ` over ${figure(node.loops, 0)} loops` : ""}`
                : "Rows the planner expects"
            }
          />
        )}
        {timed && (
          <Measure
            measure="time"
            share={row.timeShare}
            value={row.timeMs === undefined ? undefined : spanText(row.timeMs)}
            title={
              row.timeShare === undefined
                ? undefined
                : `${Math.round(row.timeShare * 100)}% of the time was spent in this step itself`
            }
          />
        )}
      </div>
      {open && detailed && (
        <div
          className="space-y-1.5 pr-2.5 pb-2 text-hint"
          style={{
            paddingLeft: `${0.625 + (row.rails.length + (row.depth > 0 ? 1 : 0)) * 1 + 1.25}rem`,
          }}
        >
          {node.notes.map((note, index) => (
            <p key={index} className="font-mono leading-relaxed break-words text-muted-foreground">
              {note}
            </p>
          ))}
          {node.facts.length > 0 && (
            <dl className="flex flex-wrap gap-x-4 gap-y-1">
              {node.facts.map(([label, value]) => (
                <div key={label} className="flex min-w-0 items-baseline gap-1.5">
                  <dt className="shrink-0 text-muted-foreground">{label}</dt>
                  <dd className="min-w-0 font-mono break-all">{value}</dd>
                </div>
              ))}
            </dl>
          )}
        </div>
      )}
    </li>
  )
}

function StepName({ node }: { node: PlanRow["node"] }) {
  return (
    <>
      <span className="shrink-0 text-xs font-medium whitespace-nowrap">{node.title}</span>
      {node.target && (
        <span className="min-w-0 truncate pl-1 font-mono text-xs text-muted-foreground">
          {node.target}
        </span>
      )}
    </>
  )
}

/** One figure of a step with its bar: the bar is the step's share, the figure its value. */
function Measure({
  measure,
  share,
  value,
  title,
  wide,
}: {
  measure: keyof typeof MEASURES
  share: number | undefined
  value: string | undefined
  title?: string
  wide?: boolean
}) {
  return (
    <span
      title={title}
      className={cn(
        "flex shrink-0 items-center justify-end gap-2",
        wide ? "w-24 @2xl:w-44 @5xl:w-64" : "w-14 @2xl:w-32 @5xl:w-52",
      )}
    >
      {/* The bar gives way before the figure does: on a narrow pane the numbers
          are the reading. On a wide one it grows with the pane, so the figures
          stay near the step they belong to instead of across an empty row. */}
      {share !== undefined && (
        <span
          aria-hidden
          className="hidden h-1 w-12 shrink-0 overflow-hidden rounded-full bg-meter-track @2xl:block @5xl:w-28"
        >
          <span
            className="block h-full rounded-full"
            style={{
              width: `${Math.max(share > 0 ? 4 : 0, Math.round(share * 100))}%`,
              background: MEASURES[measure].color,
            }}
          />
        </span>
      )}
      <span className="numeric min-w-0 truncate text-right text-xs">
        {value ?? <span className="text-muted-foreground">—</span>}
      </span>
    </span>
  )
}
