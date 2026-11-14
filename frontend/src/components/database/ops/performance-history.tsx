"use client"

import { useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import { Clock } from "@/components/icons"
import { ApiError } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { LATENCY_SHARE, latencyHead } from "@/lib/latency-words"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { InfoTip } from "@/components/form"
import { ChartPanel } from "@/components/metrics/chart-panel"
import type { Series } from "@/components/metrics/metric-chart"
import { Sparkline } from "@/components/metrics/sparkline"
import { SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
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
import { DEFAULT_RETENTION_HOURS, HISTORY_RANGES } from "@/components/database/home/use-samples"
import { EngineMark } from "@/components/database/kit"
import { oneLine } from "@/components/database/ops/queries"
import { statementHref } from "@/components/database/ops/performance-address"
import { readStatementHistory } from "@/components/database/ops/performance-api"
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
} from "@/components/database/ops/performance-recorded"
import {
  NoFigure,
  NotAvailable,
  SortHead,
  Stale,
  StatementLine,
  ViewRead,
  RETURNS_FOCUS,
} from "@/components/database/ops/performance-parts"
import { Fig, FigureLine, TitleAside, TitleFact } from "@/components/database/ops/performance-words"
import type {
  DbStatementHistory,
  DbStatementHistoryDetail,
  DbStatementHistoryEntry,
  DbStatementHistoryFacts,
  DbStatementHistorySort,
} from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"

/** How many statements the list asks for. The server's cap is 200. */
const TOP = 50

/** The recorder writes every five minutes; a statement's own history is read again each minute. */
export const HISTORY_EVERY_MS = 60_000

/** The window the history is read over, held for the database: the Overview's charts and a statement's page read the same hours. */
export function useHistoryHours(id: number) {
  const [chosen, setHours] = useViewState(`databases.${id}.history.hours`, 1)
  const hours = HISTORY_RANGES.some((range) => range.hours === chosen) ? chosen : 1
  return [hours, setHours] as const
}

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
 * database, so the two views read the same hours. A row is one line: its
 * trend is a column beside its text, where it hung under it until 0.8.7, and
 * it opens the statement's own page on its history. The text is a shape, as
 * the Statements view shows it.
 */
export function StatementHistoryView() {
  const { id, engine, param, select } = useDatabase()
  const router = useRouter()
  const [frame, width] = useColumnWidth<HTMLDivElement>()
  const [filter, setFilter] = useSessionState(`databases.${id}.performance.history.filter`, "")
  const [hours, setHours] = useHistoryHours(id)
  const asked = param("sort")
  const sort: DbStatementHistorySort = isHistorySort(asked) ? asked : "total"
  const history = usePoll(
    (signal) => readStatementHistory(id, { hours, sort, limit: TOP }, signal),
    HISTORY_EVERY_MS,
    [id, hours, sort],
  )
  const data = history.data

  const rows = useMemo(() => {
    const list = data?.statements ?? []
    const needle = filter.trim().toLowerCase()
    return needle
      ? list.filter((entry) => oneLine(entry.query).toLowerCase().includes(needle))
      : list
  }, [data, filter])
  // The trend has a column of its own now, so each figure gives way a little
  // sooner than it did.
  const roomy = width >= 520
  const counted = width >= 600
  const percentiles = width >= 700 && Boolean(data?.percentiles)
  const returned = width >= 780
  const read = width >= 880 && Boolean(data?.rowsRead)
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
  const listed = data?.supported && data.intervals > 0 ? data : undefined

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
            <HistoryRange
              hours={hours}
              onHours={setHours}
              retentionHours={DEFAULT_RETENTION_HOURS}
              label="Statement history range"
            />
          </>
        }
      >
        {listed && (
          <TitleAside>
            <TitleFact>
              <Fig>{millis(listed.totalMs)}</Fig> recorded over {windowWords(listed.hours)}
              {partlyRecorded(listed) && listed.firstAt && (
                <span title={timestamp(listed.firstAt)}>, from {relativeTime(listed.firstAt)}</span>
              )}
            </TitleFact>
            <InfoTip label="How these figures are made">
              A row&apos;s trend is its time across the window, and its share is of all the
              statement time recorded in it. {historyMethod(engine, listed).join(" ")}
            </InfoTip>
          </TitleAside>
        )}
      </PanelHeader>
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
                        <TableHead className="px-2">Trend</TableHead>
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
                          onActivate={() =>
                            router.push(statementHref(id, entry.id, { tab: "history" }))
                          }
                          aria-label={`Open the history of the statement: ${oneLine(entry.query).slice(0, 80)}`}
                        >
                          <TableCell className="w-full max-w-0 py-2">
                            <StatementLine text={entry.query} />
                          </TableCell>
                          <TableCell className="px-2 py-2">
                            <Sparkline
                              values={trendValues(entry.trend)}
                              color={statementVerb(entry.query)?.color}
                              width={roomy ? 96 : 56}
                              height={20}
                              className={roomy ? "block h-5 w-24" : "block h-5 w-14"}
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
            </div>
          )
        }
      </ViewRead>
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
  { key: "p50", label: latencyHead("p50"), color: "var(--chart-2)" },
  { key: "p95", label: latencyHead("p95"), color: "var(--chart-4)" },
  { key: "p99", label: latencyHead("p99"), color: "var(--chart-3)" },
]
const ROWS: Series[] = [
  { key: "returned", label: "Returned", color: "var(--chart-2)" },
  { key: "read", label: "Examined", color: "var(--chart-5)" },
]
const count = (value: number) => compact(value)
const timeTick = (value: number) => (value === 0 ? "0" : millis(value))
/** One gutter for the four charts, as wide as the widest label any of them prints. */
const AXIS_WIDTH = 56

/**
 * One statement's history over the window, as the History view of its page:
 * its figures across the window as one line, and a chart each of its time,
 * its calls, its time per call and its rows, a point for every bucket of the
 * window. It was a sheet of nine tiles over the list until 0.8.7.
 *
 * The read is the page's, since the page draws its trends from the same
 * points; the window is the database's (`useHistoryHours`).
 */
export function StatementRecorded({
  detail,
  listed,
  hours,
  onHours,
}: {
  detail: PollState<DbStatementHistoryDetail>
  /** A row already in hand for it, drawn while its own read is on its way. */
  listed?: DbStatementHistoryEntry
  hours: number
  onHours: (hours: number) => void
}) {
  const { engine } = useDatabase()
  const data = detail.data
  const entry = data?.statement ?? listed
  const rows = useMemo(() => historyRows(data?.points ?? []), [data])
  const events = useMemo(() => historyEvents(data?.resets ?? []), [data])
  // The recorder keeps the statements that took the most time in each
  // reading: one it never kept has no history, which is an answer and not a
  // failure.
  const unrecorded =
    !data && detail.error instanceof ApiError && detail.error.status === 404 ? detail.error : null

  return (
    <div className="space-y-5" data-slot="statement-recorded">
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <p className="flex min-w-0 items-center gap-1.5 text-body text-muted-foreground">
          <span className="min-w-0">
            Over {windowWords(hours)}
            {data && `, a point for every ${spanWords(data.bucketSeconds)}`}
            {data && data.resets.length > 0 && ". A mark on the time axis is a reset"}
          </span>
          {data && (
            <InfoTip label="How these figures are made">{historyMethod(engine, data)[0]}</InfoTip>
          )}
        </p>
        <HistoryRange
          hours={hours}
          onHours={onHours}
          retentionHours={DEFAULT_RETENTION_HOURS}
          label="Statement history range"
        />
      </div>
      {unrecorded ? (
        <EmptyState
          mark={<EngineMark engine={engine} size="md" />}
          title="No history is recorded for this statement"
          description="The recorder keeps the statements that took the most time in each reading. This one has not been among them."
        />
      ) : (
        <>
          {entry && <RecordedFigures entry={entry} facts={data} />}
          <ViewRead
            poll={detail}
            what="the statement's history"
            skeleton={<LoadingPanel plain rows={4} />}
          >
            {(answer) => <Charts rows={rows} facts={answer} events={events} />}
          </ViewRead>
        </>
      )}
    </div>
  )
}

/** A statement's figures over the window as one line, with the percentiles and rows read where the engine has them. */
function RecordedFigures({
  entry,
  facts,
}: {
  entry: DbStatementHistoryEntry
  facts?: DbStatementHistoryFacts
}) {
  const each = entry.calls > 0 ? entry.rows / entry.calls : undefined
  const examined =
    entry.rowsRead !== undefined && entry.rows > 0 ? entry.rowsRead / entry.rows : undefined
  const ratio = (value: number) => (value >= 10 ? compact(value) : value.toFixed(1))
  return (
    <FigureLine>
      <span>
        <Fig>{shareWords(entry.share)}</Fig> of the window&apos;s statement time
      </span>
      <span>
        <Fig>{entry.calls.toLocaleString()}</Fig> {entry.calls === 1 ? "call" : "calls"}
      </span>
      <span>
        <Fig>{millis(entry.totalMs)}</Fig> in all
      </span>
      <span>
        <Fig>{millis(entry.meanMs)}</Fig> a call
      </span>
      {(["p50", "p95", "p99"] as const).map((key) => {
        const value = entry[`${key}Ms`]
        return value === undefined ? null : (
          <span key={key}>
            {LATENCY_SHARE[key].toLowerCase()} inside <Fig>{millis(value)}</Fig>
          </span>
        )
      })}
      <span>
        <Fig>{entry.rows.toLocaleString()}</Fig> {entry.rows === 1 ? "row" : "rows"}
        {each !== undefined && `, ${ratio(each)} a call`}
      </span>
      {entry.rowsRead !== undefined && (
        <span>
          <Fig>{entry.rowsRead.toLocaleString()}</Fig> examined
          {examined !== undefined
            ? `, ${ratio(examined)} for each returned`
            : facts?.rowsRead
              ? ", none returned"
              : ""}
        </span>
      )}
    </FigureLine>
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
    // Two across where the view has the width for two, by its own measure:
    // the rail beside the page takes a fifth of a laptop.
    <div className="@container">
      <div className="grid gap-x-8 gap-y-6 @3xl:grid-cols-2 [&>*]:min-w-0">
        <ChartPanel
          plain
          title="Time spent"
          rows={rows}
          series={TIME}
          format={millis}
          axisFormat={timeTick}
          axisWidth={AXIS_WIDTH}
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
          axisWidth={AXIS_WIDTH}
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
          axisFormat={timeTick}
          axisWidth={AXIS_WIDTH}
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
          axisWidth={AXIS_WIDTH}
          domain={read.domain}
          yTicks={read.ticks}
          events={events}
          height={150}
          showPeaks={false}
          note={note}
        />
      </div>
    </div>
  )
}
