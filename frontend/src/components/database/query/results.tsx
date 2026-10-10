"use client"

import { MenuItemText } from "@/components/ui/menu-item-text"

import { useEffect, useId, useRef, useState } from "react"
import { ChevronDown, Download, StopCircle, Warning } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { DbExportFormat } from "@/lib/types"
import { EmptyState, Notice } from "@/components/state"
import { ChipCount, FilterChip, tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { EXPORT_FORMATS, EXPORT_ROWS, EXPORT_ROWS_MAX } from "@/components/database/data/export"
import { EngineMark } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import { ChartView } from "@/components/database/query/chart-view"
import { useKeyNames } from "@/components/database/query/keys"
import { Messages, StatementLine } from "@/components/database/query/messages"
import { PlanView } from "@/components/database/query/plan-view"
import { ResultGrid } from "@/components/database/query/result-grid"
import type { Snippet } from "@/components/database/query/snippets"
import {
  canFetchMore,
  elapsedText,
  runKey,
  spanText,
  stepLimit,
  stepOutcome,
  stepToShow,
  type Run,
  type RunStep,
  type TabRun,
} from "@/components/database/query/run-model"

export type ResultsViewId = "results" | "messages" | "plan" | "chart"

const VIEWS: { id: ResultsViewId; label: string }[] = [
  { id: "results", label: "Results" },
  { id: "messages", label: "Messages" },
  { id: "plan", label: "Plan" },
  { id: "chart", label: "Chart" },
]

/** The row limits a run can ask for; the last is the most the server hands over. */
export const ROW_LIMITS = [100, 500, 1000, 5000] as const
export const MAX_ROWS = ROW_LIMITS[ROW_LIMITS.length - 1]

const grouped = (n: number) => n.toLocaleString("en-US")

/**
 * What a tab's statements came back with: their rows, what each one did, the
 * plan, and the rows drawn.
 *
 * A failure is said here, where the rows would have been, in the engine's own
 * words — a toast is gone before a long message has been read, and says
 * nothing of which statement it was. A result the server cut says so above
 * its rows and offers the way to the rest.
 */
export function Results({
  held,
  checking,
  view,
  onView,
  compact,
  canRun,
  canAnalyze,
  noPlanWhy,
  onRun,
  onCancel,
  onFetchMore,
  onExplain,
  onExport,
  exporting,
  onShowInEditor,
  empty,
  starters,
  onStart,
  refusedWhy,
}: {
  /** The tab's last run and plan. */
  held: TabRun | undefined
  /** The statement is being classified before it is sent. */
  checking: boolean
  view: ResultsViewId
  onView: (view: ResultsViewId) => void
  /** The pane is narrow: what is not the rows is folded so the rows keep the room. */
  compact: boolean
  canRun: boolean
  canAnalyze: boolean
  /** Why a plan cannot be asked for, when it cannot. */
  noPlanWhy?: string
  onRun: () => void
  onCancel: () => void
  /** Runs the same statement again for more rows. */
  onFetchMore: (step: RunStep, limit: number) => void
  onExplain: (analyze: boolean) => void
  onExport: (sql: string, format: DbExportFormat, limit: number) => void
  exporting: boolean
  /** Marks a failed statement in the editor. */
  onShowInEditor: (step: RunStep) => void
  /** The editor holds no statement. */
  empty: boolean
  /** Diagnostics to start from when it holds none. */
  starters: Snippet[]
  onStart: (snippet: Snippet) => void
  /** Why the statement under the cursor would not be run, when it would not. */
  refusedWhy?: string
}) {
  const { engine } = useDatabase()
  const keys = useKeyNames()
  const run = held?.run
  const tabsId = useId()
  const tabs = useRef<HTMLDivElement>(null)

  // Which statement's result is open: the one worth looking at when a run
  // lands, and from then on the one the reader picked.
  const [picked, setPicked] = useState<{ run: string; step: number } | null>(null)
  // A statement being asked again for more rows is the one being looked at.
  const stepIndex = run?.fetching
    ? run.fetching.index
    : run && picked?.run === runKey(run)
      ? Math.min(picked.step, Math.max(0, run.steps.length - 1))
      : run
        ? stepToShow(run)
        : 0
  const step = run?.steps[stepIndex]
  const showStep = (index: number) => {
    if (run) setPicked({ run: runKey(run), step: index })
  }
  // Asking a statement for more rows keeps the reader on that statement afterwards.
  const fetchMore = (of: RunStep, limit: number) => {
    showStep(of.index)
    onFetchMore(of, limit)
  }

  const onTabKey = (event: React.KeyboardEvent) => {
    const at = VIEWS.findIndex((entry) => entry.id === view)
    const to =
      event.key === "ArrowRight"
        ? (at + 1) % VIEWS.length
        : event.key === "ArrowLeft"
          ? (at + VIEWS.length - 1) % VIEWS.length
          : event.key === "Home"
            ? 0
            : event.key === "End"
              ? VIEWS.length - 1
              : -1
    if (to < 0) return
    event.preventDefault()
    onView(VIEWS[to].id)
    tabs.current?.querySelector<HTMLElement>(`[data-view="${VIEWS[to].id}"]`)?.focus()
  }

  const failures = run ? run.steps.filter((entry) => entry.status === "error").length : 0
  const running = run?.phase === "running"

  let body: React.ReactNode
  if (view === "messages") {
    body = (
      <Messages
        run={run}
        onShowStep={(index) => {
          showStep(index)
          onView("results")
        }}
      />
    )
  } else if (view === "plan") {
    body = (
      <PlanView
        explained={held?.explain}
        onExplain={onExplain}
        canAnalyze={canAnalyze}
        why={noPlanWhy}
      />
    )
  } else if (view === "chart") {
    body = running ? <Waiting run={run} onCancel={onCancel} /> : <ChartView result={step?.result} />
  } else if (!run) {
    body = (
      <div className="flex min-h-0 flex-1 items-center justify-center overflow-auto p-6">
        <EmptyState
          className="border-0 py-6"
          mark={<EngineMark engine={engine} />}
          title={checking ? "Checking the statement…" : "Nothing has been run in this tab"}
          description={
            !canRun
              ? "Your role reads this database: it can read a plan, the history and the saved queries, and cannot run statements."
              : refusedWhy
                ? refusedWhy
                : empty
                  ? "Write a statement above, or start from one of this engine's diagnostics."
                  : `Run sends the selection, or the statement the cursor is on. ${keys.run} runs it from the editor; ${keys.runAll}, everything.`
          }
          action={
            canRun &&
            !refusedWhy &&
            (empty ? (
              starters.length > 0 && (
                <div className="flex max-w-xl flex-wrap justify-center gap-2">
                  {starters.map((starter) => (
                    <Button
                      key={starter.id}
                      size="sm"
                      variant="outline"
                      onClick={() => onStart(starter)}
                    >
                      {starter.title}
                    </Button>
                  ))}
                </div>
              )
            ) : (
              <Button size="sm" variant="outline" onClick={onRun} disabled={checking}>
                Run
              </Button>
            ))
          }
        />
      </div>
    )
  } else if (running) {
    body = <Waiting run={run} onCancel={onCancel} />
  } else if (run.refused) {
    body = (
      <Refusal tone="warning" title="Not run" text={run.refused}>
        <StatementLine sql={run.sql} className="block" />
      </Refusal>
    )
  } else if (run.steps.length === 0) {
    body = run.cancelled ? (
      <Refusal tone="warning" title="Stopped" text="The statement was cancelled on the server.">
        <StatementLine sql={run.sql} className="block" />
      </Refusal>
    ) : (
      <Refusal
        tone="danger"
        title="The run was turned down"
        text={run.error ?? "The server gave no reason."}
      >
        <StatementLine sql={run.sql} className="block" />
      </Refusal>
    )
  } else if (step) {
    const chips =
      run.steps.length > 1 ? (
        <StepChips steps={run.steps} current={stepIndex} onPick={showStep} />
      ) : undefined
    if (step.result && step.result.columns.length > 0) {
      body = (
        <ResultGrid
          label={run.steps.length > 1 ? `Result of statement ${step.index + 1}` : "Query result"}
          result={step.result}
          resultKey={`${runKey(run)}.${step.index}.${stepLimit(run, step)}`}
          compact={compact}
          toolbar={chips}
          banner={
            step.result.truncated && (
              <Truncated
                step={step}
                run={run}
                compact={compact}
                onFetchMore={(limit) => fetchMore(step, limit)}
                canExport={engine.can("exportQuery") && canRun && step.risk?.level === "read"}
              />
            )
          }
        />
      )
    } else {
      body = (
        <div className="flex min-h-0 flex-1 flex-col">
          {chips && (
            <div className="flex min-h-9 shrink-0 flex-wrap items-center gap-1.5 border-b border-hairline bg-surface-header px-2.5 py-1.5">
              {chips}
            </div>
          )}
          {step.status === "error" ? (
            <Refusal
              tone="danger"
              title={
                run.steps.length > 1 ? `Statement ${step.index + 1} failed` : "The statement failed"
              }
              text={step.error ?? "The engine gave no reason."}
              action={
                <Button size="xs" variant="outline" onClick={() => onShowInEditor(step)}>
                  Show in the editor
                </Button>
              }
            >
              <p className="flex min-w-0 items-center gap-2">
                <span className="numeric shrink-0 text-hint text-muted-foreground">
                  Line {step.line}
                </span>
                <StatementLine sql={step.sql} />
              </p>
            </Refusal>
          ) : step.status === "skipped" ? (
            <Refusal
              tone="default"
              title="Not run"
              text={`Statement ${run.failed + 1} failed, and the run stopped there.`}
            >
              <StatementLine sql={step.sql} className="block" />
            </Refusal>
          ) : (
            <div className="min-h-0 flex-1 overflow-auto p-4">
              <p className="numeric text-2xl font-semibold">
                {step.result && step.result.rowsAffected > 0
                  ? grouped(step.result.rowsAffected)
                  : "Done"}
              </p>
              <p className="mt-1 text-xs text-muted-foreground">
                {step.result && step.result.rowsAffected > 0
                  ? `${step.result.rowsAffected === 1 ? "row" : "rows"} changed in ${spanText(step.durationMs)}`
                  : `The statement returned no rows. ${spanText(step.durationMs)}`}
              </p>
              <StatementLine sql={step.sql} className="mt-3 block text-muted-foreground" />
            </div>
          )}
        </div>
      )
    }
  }

  const exportable =
    view === "results" &&
    !running &&
    step?.status === "ok" &&
    (step.result?.columns.length ?? 0) > 0 &&
    engine.can("exportQuery") &&
    canRun

  return (
    <div data-slot="query-results" className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex h-9 shrink-0 items-stretch border-b border-hairline bg-surface-header pr-1.5">
        <div
          ref={tabs}
          role="tablist"
          aria-label="What the statement returned"
          className="flex min-w-0 [scrollbar-width:none] items-stretch overflow-x-auto [&::-webkit-scrollbar]:hidden"
          onKeyDown={onTabKey}
        >
          {VIEWS.map((entry) => {
            const selected = view === entry.id
            return (
              <button
                key={entry.id}
                type="button"
                role="tab"
                id={`${tabsId}-${entry.id}`}
                data-view={entry.id}
                aria-selected={selected}
                aria-controls={selected ? `${tabsId}-panel` : undefined}
                tabIndex={selected ? 0 : -1}
                className={tabClasses(selected, "h-9")}
                onClick={() => onView(entry.id)}
              >
                {entry.label}
                {entry.id === "results" && run && run.steps.length > 1 && (
                  <ChipCount>{run.steps.length}</ChipCount>
                )}
                {entry.id === "messages" && failures > 0 && (
                  <span className="text-destructive">
                    <ChipCount className="opacity-100">{failures}</ChipCount>
                    <span className="sr-only"> failed</span>
                  </span>
                )}
              </button>
            )
          })}
        </div>
        <div className="ml-auto flex shrink-0 items-center gap-2 pl-2">
          {run && !running && run.steps.length > 0 && view !== "plan" && (
            <span className="numeric text-hint text-muted-foreground max-sm:hidden">
              {run.steps.length > 1
                ? `${grouped(run.steps.length)} statements`
                : step
                  ? stepOutcome(step)
                  : ""}
              {" · "}
              {/* What the engine took, not the round trip: the figure the history keeps. */}
              {spanText(run.steps.reduce((total, entry) => total + entry.durationMs, 0))}
            </span>
          )}
          {exportable && step.risk?.level === "read" && (
            <ExportMenu
              pending={exporting}
              truncated={Boolean(step.result?.truncated)}
              onExport={(format, limit) => onExport(step.sql, format, limit)}
            />
          )}
        </div>
      </div>
      <div
        role="tabpanel"
        id={`${tabsId}-panel`}
        aria-labelledby={`${tabsId}-${view}`}
        className="flex min-h-0 min-w-0 flex-1 flex-col"
      >
        {body}
      </div>
    </div>
  )
}

/** A run that is out: what was sent, how long it has been, and the way to stop it. */
function Waiting({ run, onCancel }: { run: Run; onCancel: () => void }) {
  const { engine } = useDatabase()
  const elapsed = useElapsed(run.startedAt)
  // One statement of a run, out again for more rows: that statement is what is waited for.
  const again = run.fetching ? run.steps[run.fetching.index] : undefined
  const doing = run.cancelling
    ? "Stopping…"
    : run.fetching
      ? `Fetching ${grouped(run.fetching.limit)} rows…`
      : "Running…"
  return (
    <div role="status" className="flex min-h-0 flex-1 flex-col">
      <div aria-hidden className="h-0.5 shrink-0 overflow-hidden bg-meter-track">
        <div className="h-full w-1/3 animate-sweep bg-primary" />
      </div>
      <div className="min-h-0 flex-1 space-y-3 overflow-auto p-4">
        <p className="flex items-center gap-2 text-body">
          <TextShimmer>{doing}</TextShimmer>
          <span className="numeric text-muted-foreground">{elapsedText(elapsed)}</span>
        </p>
        <StatementLine sql={again?.sql ?? run.sql} className="block text-muted-foreground" />
        {engine.can("queryCancel") && (
          <Button size="sm" variant="outline" onClick={onCancel} disabled={run.cancelling}>
            <StopCircle />
            Cancel
          </Button>
        )}
      </div>
    </div>
  )
}

/** Milliseconds since `since`, ticking while mounted. */
export function useElapsed(since: number): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 250)
    return () => clearInterval(timer)
  }, [])
  return Math.max(0, now - since)
}

/**
 * What stands where rows would be when there are none to show, and why: the
 * product's notice, with the engine's own words under its title and the
 * statement they are about.
 */
function Refusal({
  tone,
  title,
  text,
  action,
  children,
}: {
  tone: "default" | "warning" | "danger"
  title: string
  text: string
  action?: React.ReactNode
  children?: React.ReactNode
}) {
  return (
    <div
      role={tone === "danger" ? "alert" : "status"}
      data-slot="query-refusal"
      className="min-h-0 flex-1 overflow-auto p-3"
    >
      <Notice
        tone={tone}
        icon={tone === "default" ? undefined : Warning}
        title={title}
        className="[&>div]:flex-1"
      >
        <div className="space-y-2">
          <p className="font-mono break-words whitespace-pre-wrap text-foreground">{text}</p>
          {children}
          {action}
        </div>
      </Notice>
    </div>
  )
}

/** One chip per statement of a script: its number, its first word, what it did. */
function StepChips({
  steps,
  current,
  onPick,
}: {
  steps: RunStep[]
  current: number
  onPick: (index: number) => void
}) {
  return (
    <div
      role="group"
      aria-label="Statements of this run"
      className="flex min-w-0 flex-1 [scrollbar-width:none] items-center gap-1 overflow-x-auto [&::-webkit-scrollbar]:hidden"
    >
      {steps.map((step) => (
        <FilterChip
          key={step.index}
          selected={step.index === current}
          title={step.sql}
          onClick={() => onPick(step.index)}
          className="h-6 max-w-56 px-2"
        >
          <span className="numeric text-muted-foreground">{step.index + 1}</span>
          <StatementLine sql={step.sql} className="max-w-28 text-hint" />
          <span
            className={cn(
              "numeric shrink-0",
              step.status === "error" ? "text-destructive" : "text-muted-foreground",
            )}
          >
            {stepOutcome(step)}
          </span>
        </FilterChip>
      ))}
    </div>
  )
}

/** The line above a result the server cut: how much is shown, and the ways to the rest. */
function Truncated({
  step,
  run,
  compact,
  onFetchMore,
  canExport,
}: {
  step: RunStep
  run: Run
  compact: boolean
  onFetchMore: (limit: number) => void
  canExport: boolean
}) {
  const shown = step.result?.rowCount ?? 0
  const asked = stepLimit(run, step)
  const larger = ROW_LIMITS.filter((limit) => limit > Math.max(asked, shown))
  const more = canFetchMore(step, asked, MAX_ROWS)
  return (
    <div
      role="status"
      data-slot="result-truncated"
      className="flex shrink-0 items-center gap-x-2 gap-y-1 border-b border-rule-warning bg-wash-warning px-2.5 py-1.5 text-hint"
    >
      <Tag tone="warning">cut</Tag>
      <span className={cn("min-w-0 flex-1", compact && "truncate")}>
        {compact ? `First ${grouped(shown)} rows.` : `These are the first ${grouped(shown)} rows.`}{" "}
        The statement returns more.
        {!more &&
          step.risk?.level !== "read" &&
          " It changes data, so it is not run again for them."}
        {!more &&
          step.risk?.level === "read" &&
          ` ${grouped(MAX_ROWS)} is the most a run shows${canExport ? "; Export writes every row to a file" : ""}.`}
      </span>
      {more &&
        // On a narrow pane the ways to more are one control on the banner's own line.
        (compact ? (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button size="xs" variant="outline" className="shrink-0">
                Fetch more
                <ChevronDown />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              {larger.map((limit) => (
                <DropdownMenuItem key={limit} onSelect={() => onFetchMore(limit)}>
                  Fetch {grouped(limit)}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        ) : (
          larger.map((limit) => (
            <Button
              key={limit}
              size="xs"
              variant="outline"
              className="shrink-0"
              onClick={() => onFetchMore(limit)}
            >
              Fetch {grouped(limit)}
            </Button>
          ))
        ))}
    </div>
  )
}

function ExportMenu({
  pending,
  truncated,
  onExport,
}: {
  pending: boolean
  truncated: boolean
  onExport: (format: DbExportFormat, limit: number) => void
}) {
  const { engine } = useDatabase()
  const [rows, setRows] = useState(EXPORT_ROWS)
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="xs" variant="outline" aria-label="Export" pending={pending}>
          <Download />
          <span className="max-sm:hidden">Export</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-64">
        <p className="px-2 pt-1.5 pb-1 text-hint text-muted-foreground">
          {truncated
            ? "Every row the statement returns, not only the ones shown"
            : "Every row the statement returns"}
          , up to {grouped(rows)}. The server runs it again as a read.
        </p>
        {engine.capabilities.exportFormats.map((format) => (
          <DropdownMenuItem key={format} onSelect={() => onExport(format, rows)}>
            <MenuItemText
              hint={
                <span className="text-hint text-muted-foreground">
                  {EXPORT_FORMATS[format].detail}
                </span>
              }
            >
              <span className="w-20 shrink-0">{EXPORT_FORMATS[format].label}</span>
            </MenuItemText>
          </DropdownMenuItem>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuCheckboxItem
          checked={rows === EXPORT_ROWS_MAX}
          onSelect={(event) => event.preventDefault()}
          onCheckedChange={(checked) => setRows(checked === true ? EXPORT_ROWS_MAX : EXPORT_ROWS)}
        >
          Up to {grouped(EXPORT_ROWS_MAX)} rows
        </DropdownMenuCheckboxItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
