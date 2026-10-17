"use client"

import { useMemo, useState } from "react"
import { Clock, CodeBracket } from "@/components/icons"
import { relativeTime, timestamp } from "@/lib/format"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useViewState } from "@/lib/view-state"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { FormNote } from "@/components/form"
import { ChartPanel } from "@/components/metrics/chart-panel"
import type { Series } from "@/components/metrics/metric-chart"
import { Sparkline } from "@/components/metrics/sparkline"
import { Metric, MetricStrip, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { EmptyNote, EmptyState, LoadingPanel, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { HistoryRange } from "@/components/database/home/activity"
import { highest, roundTicks } from "@/components/database/home/charts"
import { statementVerb } from "@/components/database/home/kinds"
import { compact } from "@/components/database/home/readings"
import { HISTORY_RANGES } from "@/components/database/home/use-samples"
import { CodeView } from "@/components/database/kit"
import { oneLine } from "@/components/database/ops/queries"
import {
  readStatementHistory,
  readStatementHistoryOf,
} from "@/components/database/ops/performance-api"
import { millis, shareWords } from "@/components/database/ops/performance-figures"
import {
  firstPointWords,
  historyEvents,
  historyMethod,
  historyRows,
  historySorts,
  isHistorySort,
  partlyRecorded,
  spanWords,
  trendValues,
  windowWords,
  type HistoryRow,
} from "@/components/database/ops/performance-history"
import {
  NoFigure,
  NotAvailable,
  SortHead,
  Stale,
  StatementLine,
  ViewRead,
  useReturnFocus,
  RETURNS_FOCUS,
} from "@/components/database/ops/performance-parts"
import type {
  DbStatementHistory,
  DbStatementHistoryEntry,
  DbStatementHistoryFacts,
  DbStatementHistorySort,
} from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"

/** How many statements the list asks for. The server's cap is 200. */
const TOP = 50

/** The recorder writes every five minutes; the list is read again each minute. */
const HISTORY_EVERY_MS = 60_000

/**
 * The statement list over time: what each statement cost over the last hour,
 * day or week, from what the recorder read every five minutes, with each
 * one's trend across the window.
 *
 * The Statements view answers "what has cost the most since the counters
 * started", which on a server that has run for a month is a month. This
 * answers "what is costing the most now, and since when": a statement whose
 * calls doubled this afternoon is near the bottom of a month's totals and at
 * the top of the last hour's, and its trend shows when it started.
 *
 * The window is the one the Overview's charts are drawn over, held for the
 * database, so the two views read the same hours. A row opens the statement's
 * own charts. The text is a shape, as the Statements view shows it.
 */
export function StatementHistoryView() {
  const { id, engine, param, select } = useDatabase()
  const focus = useReturnFocus()
  const [frame, width] = useColumnWidth<HTMLDivElement>()
  const [filter, setFilter] = useState("")
  const [chosen, setHours] = useViewState(`databases.${id}.history.hours`, 1)
  const hours = HISTORY_RANGES.some((range) => range.hours === chosen) ? chosen : 1
  const asked = param("sort")
  const sort: DbStatementHistorySort = isHistorySort(asked) ? asked : "total"
  const history = usePoll(
    (signal) => readStatementHistory(id, { hours, sort, limit: TOP }, signal),
    HISTORY_EVERY_MS,
    [id, hours, sort],
  )
  const data = history.data
  const openId = param("statement")
  const listed = openId ? data?.statements.find((entry) => entry.id === openId) : undefined

  const rows = useMemo(() => {
    const list = data?.statements ?? []
    const needle = filter.trim().toLowerCase()
    return needle
      ? list.filter((entry) => oneLine(entry.query).toLowerCase().includes(needle))
      : list
  }, [data, filter])
  const counted = width >= 520
  const percentiles = width >= 600 && Boolean(data?.percentiles)
  const returned = width >= 680
  const read = width >= 780 && Boolean(data?.rowsRead)
  const sorts = historySorts(data)
  const head = (of: DbStatementHistorySort, className?: string) => {
    const entry = sorts.find((sort) => sort.id === of)
    return entry ? (
      <SortHead
        label={entry.label}
        active={sort === of}
        descending
        align="right"
        className={className}
        onSort={() => select({ sort: of === "total" ? null : of })}
      />
    ) : null
  }

  return (
    <Panel
      plain
      aria-label="Statement history"
      ref={frame}
      className="focus-ring"
      {...RETURNS_FOCUS}
    >
      <PanelHeader
        title="Statement history"
        actions={
          <>
            <Stale poll={history} />
            {data?.supported && data.statements.length > 0 && (
              <SearchInput
                dense
                aria-label="Filter the statement history"
                placeholder="Filter by text"
                value={filter}
                containerClassName="sm:w-56"
                onChange={(event) => setFilter(event.target.value)}
              />
            )}
            <HistoryRange hours={hours} onHours={setHours} label="Statement history range" />
          </>
        }
      />
      <ViewRead
        poll={history}
        what="the statement history"
        skeleton={<LoadingPanel plain rows={8} />}
      >
        {(answer) =>
          !answer.supported ? (
            <div className="pt-4">
              <NotAvailable
                title={`This server keeps no history of its ${engine.nouns.statements}`}
                reason={answer.reason}
              />
            </div>
          ) : answer.intervals === 0 ? (
            <div className="pt-4">
              <NoHistoryYet answer={answer} />
            </div>
          ) : (
            <div className="animate-rise space-y-3 pt-3">
              {answer.reading && !answer.reading.supported && (
                <Notice tone="warning" title="The newest reading of this server failed">
                  <p className="wrap-anywhere">{answer.reading.reason}</p>
                  <p>The history below ends at the last reading that worked.</p>
                </Notice>
              )}
              <p className="text-hint text-muted-foreground">
                <span className="numeric font-medium text-foreground">
                  {millis(answer.totalMs)}
                </span>{" "}
                of statement time recorded over {windowWords(answer.hours)}
                {partlyRecorded(answer) && answer.firstAt && (
                  <span title={timestamp(answer.firstAt)}>
                    , from its first reading {relativeTime(answer.firstAt)}
                  </span>
                )}
              </p>
              <PanelBody flush className="group-data-[plain]/panel:-mx-4">
                {answer.statements.length === 0 ? (
                  <EmptyNote className="px-4">
                    No {engine.nouns.statement} ran in {windowWords(answer.hours)}.
                  </EmptyNote>
                ) : rows.length === 0 ? (
                  <EmptyNote className="px-4">No statement matches {filter.trim()}.</EmptyNote>
                ) : (
                  <Table>
                    <TableHeader>
                      <TableRow className="hover:bg-transparent">
                        <TableHead>Statement</TableHead>
                        {head("total", "px-2")}
                        {counted && head("calls", "px-2")}
                        {head("mean", "px-2")}
                        {percentiles && head("p95", "px-2")}
                        {returned && head("rows", "px-2")}
                        {read && head("rowsRead")}
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((entry) => (
                        <TableRow
                          key={entry.id}
                          data-state={entry.id === openId ? "selected" : undefined}
                          onActivate={() => {
                            focus.remember()
                            select({ statement: entry.id })
                          }}
                          aria-label={`Open the history of the statement: ${oneLine(entry.query).slice(0, 80)}`}
                        >
                          <TableCell className="w-full max-w-0 py-2">
                            <StatementLine text={entry.query} />
                            <Sparkline
                              values={trendValues(entry.trend)}
                              color={statementVerb(entry.query)?.color}
                              width={240}
                              height={20}
                              className="mt-1 block h-5 w-full"
                              label={`Its time over ${windowWords(answer.hours)}`}
                            />
                          </TableCell>
                          <TableCell className="numeric px-2 py-2 text-right font-medium">
                            {shareWords(entry.share)}
                          </TableCell>
                          {counted && (
                            <TableCell className="numeric px-2 py-2 text-right">
                              {compact(entry.calls)}
                            </TableCell>
                          )}
                          <TableCell className="numeric px-2 py-2 text-right">
                            {millis(entry.meanMs)}
                          </TableCell>
                          {percentiles && (
                            <TableCell className="numeric px-2 py-2 text-right">
                              {entry.p95Ms === undefined ? <NoFigure /> : millis(entry.p95Ms)}
                            </TableCell>
                          )}
                          {returned && (
                            <TableCell className="numeric px-2 py-2 text-right text-muted-foreground">
                              {compact(entry.rows)}
                            </TableCell>
                          )}
                          {read && (
                            <TableCell className="numeric py-2 text-right text-muted-foreground">
                              {entry.rowsRead === undefined ? (
                                <NoFigure />
                              ) : (
                                compact(entry.rowsRead)
                              )}
                            </TableCell>
                          )}
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                )}
              </PanelBody>
              <div className="space-y-1">
                <FormNote>
                  The trend under a statement is its time across the window, and the share is of all
                  the statement time recorded in it.
                </FormNote>
                {historyMethod(engine, answer).map((line) => (
                  <FormNote key={line}>{line}</FormNote>
                ))}
              </div>
            </div>
          )
        }
      </ViewRead>

      {openId && (
        <HistoryPanel
          // One statement's panel: what was read for another is not carried over.
          key={openId}
          statement={openId}
          listed={listed}
          hours={hours}
          onHours={setHours}
          onClose={() => {
            select({ statement: null })
            focus.restore()
          }}
        />
      )}
    </Panel>
  )
}

/**
 * A window with nothing in it yet: the recorder has not read the server
 * twice since the dashboard started, or the server's statement statistics
 * cannot be read — which the Statements view says how to change.
 */
function NoHistoryYet({ answer }: { answer: DbStatementHistory }) {
  const { engine, goto } = useDatabase()
  const [now] = useState(() => Date.now())
  if (answer.reading && !answer.reading.supported) {
    return (
      <NotAvailable
        title={`The ${engine.nouns.statements} this server runs cannot be recorded`}
        reason={answer.reading.reason}
        action={
          engine.can("statements") && (
            <Button
              size="sm"
              variant="outline"
              onClick={() => goto("performance", { view: "statements" })}
            >
              Open Statements
            </Button>
          )
        }
      />
    )
  }
  return (
    <EmptyState
      icon={Clock}
      title="No statement history yet"
      description={`${firstPointWords(answer, now)} What each statement adds is kept for ${spanWords(answer.retentionHours * 3600)}.`}
    />
  )
}

// The charts' series and formats are module constants: `ChartPanel` is
// memoised on what it is handed.
const TIME: Series[] = [{ key: "time", label: "Time", color: "var(--chart-1)", kind: "area" }]
const CALLS: Series[] = [{ key: "calls", label: "Calls", color: "var(--chart-2)", kind: "area" }]
const MEAN: Series[] = [{ key: "mean", label: "Mean", color: "var(--chart-1)" }]
const SPREAD: Series[] = [
  { key: "mean", label: "Mean", color: "var(--chart-1)" },
  { key: "p50", label: "p50", color: "var(--chart-2)" },
  { key: "p95", label: "p95", color: "var(--chart-4)" },
  { key: "p99", label: "p99", color: "var(--chart-3)" },
]
const ROWS: Series[] = [
  { key: "returned", label: "Returned", color: "var(--chart-2)" },
  { key: "read", label: "Read", color: "var(--chart-5)" },
]
const count = (value: number) => compact(value)

/**
 * One statement's history over the window: its figures across it, and a
 * chart each of its time, its calls, its time per call and its rows, a point
 * for every bucket of the window.
 */
function HistoryPanel({
  statement,
  listed,
  hours,
  onHours,
  onClose,
}: {
  statement: string
  /** The list's row for it, drawn while its own read is on its way. */
  listed?: DbStatementHistoryEntry
  hours: number
  onHours: (hours: number) => void
  onClose: () => void
}) {
  const { id, engine, goto } = useDatabase()
  const { can } = useAuth()
  const detail = usePoll(
    (signal) => readStatementHistoryOf(id, statement, { hours }, signal),
    HISTORY_EVERY_MS,
    [id, statement, hours],
  )
  const data = detail.data
  const entry = data?.statement ?? listed
  const rows = useMemo(() => historyRows(data?.points ?? []), [data])
  const events = useMemo(() => historyEvents(data?.resets ?? []), [data])
  const mayQuery = engine.has("query") && can("service.control")
  const lines = entry ? entry.query.split("\n").length : 1

  return (
    <SidePanel
      open
      onOpenChange={(open) => !open && onClose()}
      width="lg"
      initialFocus="body"
      title="Statement history"
      description="One statement's figures over the recorded window, and its charts."
    >
      <div className="space-y-5">
        <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
          <p className="text-body text-muted-foreground">Over {windowWords(hours)}</p>
          <HistoryRange hours={hours} onHours={onHours} label="Statement history range" />
        </div>
        {entry && <Figures entry={entry} facts={data} />}
        <ViewRead
          poll={detail}
          what="the statement's history"
          skeleton={<LoadingPanel plain rows={4} />}
        >
          {(answer) => (
            <div className="space-y-5">
              <p className="text-hint text-muted-foreground">
                A point for every {spanWords(answer.bucketSeconds)}.
                {answer.resets.length > 0 &&
                  " A mark on the time axis is where the statistics were reset."}
              </p>
              <Charts rows={rows} facts={answer} events={events} />
              <div className="space-y-1">
                {historyMethod(engine, answer)
                  .slice(0, 1)
                  .map((line) => (
                    <FormNote key={line}>{line}</FormNote>
                  ))}
              </div>
            </div>
          )}
        </ViewRead>
        {entry && (
          <CodeView
            code={entry.query}
            language={engine.editor}
            label="The statement, as a shape"
            className={lines <= 2 ? "h-28" : lines <= 5 ? "h-44" : lines <= 12 ? "h-72" : "h-96"}
            actions={
              mayQuery && (
                <Button
                  size="xs"
                  variant="ghost"
                  onClick={() => goto("query", { sql: entry.query })}
                >
                  <CodeBracket />
                  Open in Query
                </Button>
              )
            }
          />
        )}
      </div>
    </SidePanel>
  )
}

/** A statement's figures over the window, with the percentiles and rows read where the engine has them. */
function Figures({
  entry,
  facts,
}: {
  entry: DbStatementHistoryEntry
  facts?: DbStatementHistoryFacts
}) {
  const each = entry.calls > 0 ? entry.rows / entry.calls : undefined
  const examined =
    entry.rowsRead !== undefined && entry.rows > 0 ? entry.rowsRead / entry.rows : undefined
  return (
    <MetricStrip>
      <Metric label="Of runtime" value={shareWords(entry.share)} />
      <Metric label="Calls" value={entry.calls.toLocaleString()} />
      <Metric label="Total" value={millis(entry.totalMs)} />
      <Metric label="Mean" value={millis(entry.meanMs)} />
      {entry.p50Ms !== undefined && <Metric label="p50" value={millis(entry.p50Ms)} />}
      {entry.p95Ms !== undefined && <Metric label="p95" value={millis(entry.p95Ms)} />}
      {entry.p99Ms !== undefined && <Metric label="p99" value={millis(entry.p99Ms)} />}
      <Metric
        label="Rows"
        value={entry.rows.toLocaleString()}
        hint={
          each === undefined ? undefined : `${each >= 10 ? compact(each) : each.toFixed(1)} a call`
        }
      />
      {entry.rowsRead !== undefined && (
        <Metric
          label="Rows read"
          value={entry.rowsRead.toLocaleString()}
          hint={
            examined === undefined
              ? facts?.rowsRead
                ? "none returned"
                : undefined
              : `${examined >= 10 ? compact(examined) : examined.toFixed(1)} for each returned`
          }
        />
      )}
    </MetricStrip>
  )
}

/** A chart's y-axis, on round figures, held by what its ticks are. */
function useAxis(rows: readonly HistoryRow[], series: readonly Series[], whole: boolean) {
  const key = roundTicks(highest(rows, series), whole).join(" ")
  return useMemo(() => {
    const ticks = key.split(" ").map(Number)
    return { ticks, domain: [0, ticks[ticks.length - 1]] as [number, number] }
  }, [key])
}

function Charts({
  rows,
  facts,
  events,
}: {
  rows: HistoryRow[]
  facts: DbStatementHistoryFacts
  events: ReturnType<typeof historyEvents>
}) {
  const spread = facts.percentiles ? SPREAD : MEAN
  const time = useAxis(rows, TIME, false)
  const calls = useAxis(rows, CALLS, true)
  const perCall = useAxis(rows, spread, false)
  const read = useAxis(rows, ROWS, true)
  const note = "Nothing was recorded for this statement in the window."
  return (
    <div className="space-y-6">
      <ChartPanel
        plain
        title="Time spent"
        rows={rows}
        series={TIME}
        format={millis}
        domain={time.domain}
        yTicks={time.ticks}
        events={events}
        height={150}
        showPeaks={false}
        note={note}
      />
      <ChartPanel
        plain
        title="Calls"
        rows={rows}
        series={CALLS}
        format={count}
        domain={calls.domain}
        yTicks={calls.ticks}
        events={events}
        height={150}
        showPeaks={false}
        note={note}
      />
      <ChartPanel
        plain
        title="Time per call"
        rows={rows}
        series={spread}
        format={millis}
        domain={perCall.domain}
        yTicks={perCall.ticks}
        events={events}
        height={150}
        showPeaks={false}
        note={note}
      />
      <ChartPanel
        plain
        title="Rows"
        rows={rows}
        series={ROWS}
        format={count}
        domain={read.domain}
        yTicks={read.ticks}
        events={events}
        height={150}
        showPeaks={false}
        note={note}
      />
    </div>
  )
}
