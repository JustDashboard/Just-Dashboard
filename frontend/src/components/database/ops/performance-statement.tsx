"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { useParams } from "next/navigation"
import {
  Clock,
  CodeBracket,
  Copy,
  Database,
  Layers,
  MagnifyingGlass,
  Stopwatch,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { copyText } from "@/lib/clipboard"
import { percent, relativeTime, timestamp } from "@/lib/format"
import { useSessionState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { WireMark } from "@/components/deploy/wire"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { TileTrend } from "@/components/metrics/sparkline"
import { ReadingLine } from "@/components/reading-line"
import { EmptyState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { CouldNotRead } from "@/components/database/home/blocks"
import { statementVerb } from "@/components/database/home/kinds"
import { compact } from "@/components/database/home/readings"
import { CodeView, EngineMark } from "@/components/database/kit"
import { oneLine } from "@/components/database/ops/queries"
import { statementHref, unsegment } from "@/components/database/ops/performance-address"
import { readStatementHistoryOf, readStatements } from "@/components/database/ops/performance-api"
import {
  DetailFrame,
  DetailGate,
  DetailHead,
  DetailRail,
  DetailTabs,
  stepOf,
  useDetailTab,
  type DetailView,
  type RailRow,
} from "@/components/database/ops/performance-detail"
import {
  hasPlaceholder,
  isStatementSort,
  millis,
  rowsPerCall,
  shareWords,
  statementKeys,
  statementSorts,
} from "@/components/database/ops/performance-figures"
import {
  HISTORY_EVERY_MS,
  StatementRecorded,
  useHistoryHours,
} from "@/components/database/ops/performance-history"
import { statementsFilterKey } from "@/components/database/ops/performance-lists"
import { StatementLine } from "@/components/database/ops/performance-parts"
import { StatementPlan } from "@/components/database/ops/performance-plan"
import { historyRows, windowWords } from "@/components/database/ops/performance-recorded"
import { Fig } from "@/components/database/ops/performance-words"
import type {
  DbStatement,
  DbStatementSort,
  DbStatements,
} from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"

/** How many statements the page reads to find its own among, and lists beside it. The server's cap. */
const LISTED = 200

type Tab = "figures" | "text" | "plan" | "history"

/**
 * One statement, as a place of its own: `/databases/<id>/performance/statements/<key>`.
 *
 * It was a sheet over the Statements view, and a second sheet over Statement
 * history, and a third over the top statements on Logs — three details of
 * one thing, each with its own strip of tiles. Here it is one page: what it
 * costs said as readings, the whole of its text, its plan, and what it has
 * cost over the recorded window, with the list it came from beside it.
 */
export function StatementPage() {
  const { engine } = useDatabase()
  const params = useParams<{ key: string }>()
  const key = unsegment(params.key ?? "")
  return (
    <DetailGate
      has={engine.kind === "sql" && engine.can("statements")}
      thing="a statement's figures"
    >
      {/* One statement's page: what was typed for one shape's plan is not carried into the next. */}
      <Statement key={key ?? ""} address={key} />
    </DetailGate>
  )
}

function Statement({ address }: { address: string | null }) {
  const { id, conn, engine, href, param, goto } = useDatabase()
  const { can } = useAuth()
  const asked = param("sort")
  const sort: DbStatementSort = isStatementSort(asked) ? asked : "total"
  const [filter, setFilter] = useSessionState(statementsFilterKey(id), "")
  const statements = usePoll(
    (signal) => readStatements(id, { sort, limit: LISTED }, signal),
    30_000,
    [id, sort],
    { key: `/databases/${id}/statements?sort=${sort}&limit=${LISTED}` },
  )
  const data = statements.data
  // A row is addressed by a key no other row has: the engine's digest names
  // a statement twice when it ran in two schemas.
  const keyed = useMemo(() => {
    const list = data?.statements ?? []
    const keys = statementKeys(list)
    return list.map((statement, index) => ({ key: keys[index], statement }))
  }, [data])
  const found = address === null ? undefined : keyed.find((row) => row.key === address)?.statement
  // Held as last seen: a statement that drops out of the top of the list
  // while its page is open is still the one being read.
  const [kept, setKept] = useState<DbStatement | null>(null)
  if (found && found !== kept) setKept(found)
  const listed = found ?? kept ?? undefined

  const recorder = engine.can("statementHistory")
  // The recorder knows a statement by the engine's own id, which a key holds
  // with its place after it where two rows share one.
  const engineId = listed?.id ?? (address ?? "").replace(/~\d+$/, "")
  const views = useMemo<DetailView<Tab>[]>(
    () => [
      { key: "figures", label: "Figures" },
      { key: "text", label: "Text" },
      { key: "plan", label: "Plan" },
      ...(recorder ? [{ key: "history" as const, label: "History" }] : []),
    ],
    [recorder],
  )
  const [tab, setTab] = useDetailTab(views)
  const [hours, setHours] = useHistoryHours(id)
  const detail = usePoll(
    (signal) => readStatementHistoryOf(id, engineId, { hours }, signal),
    HISTORY_EVERY_MS,
    [id, engineId, hours],
    { enabled: recorder && engineId !== "" },
  )
  const recorded = detail.data?.statement
  const text = listed?.query ?? recorded?.query

  const carried = { tab: tab === "figures" ? null : tab, sort: sort === "total" ? null : sort }
  const rows = useMemo<RailRow[]>(() => {
    const needle = filter.trim().toLowerCase()
    return keyed
      .filter((row) => !needle || oneLine(row.statement.query).toLowerCase().includes(needle))
      .map(({ key, statement }) => railRow(id, key, statement, carried))
    // `carried` is two strings: held by what they are.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [keyed, filter, id, carried.tab, carried.sort])
  const pinned =
    address !== null && text !== undefined && !found
      ? railRow(id, address, listed ?? { query: text }, carried)
      : undefined
  const order = statementSorts(data?.statements).find((entry) => entry.id === sort)?.label
  const caption = !data
    ? statements.error
      ? "The statements could not be read"
      : "Reading the statements…"
    : !data.supported
      ? "This server is not counting its statements"
      : `${rows.length === keyed.length ? keyed.length : `${rows.length} of ${keyed.length}`} ${keyed.length === 1 ? "statement" : "statements"} · by ${(order ?? "total time").toLowerCase()}`

  const fromHistory = tab === "history"
  const back = fromHistory
    ? { href: href("performance", { view: "history" }), label: "Statement history" }
    : { href: href("performance", { view: "statements", sort: carried.sort }), label: "Statements" }
  const head = (
    <DetailHead
      title={`${conn.name} · Statement`}
      back={back}
      noun="statement"
      step={stepOf(rows, address)}
    />
  )
  const rail = (
    <DetailRail
      label="Statements"
      placeholder="Filter by text"
      query={filter}
      onQuery={setFilter}
      caption={caption}
      rows={rows}
      current={address}
      pinned={pinned}
      loading={!data && !statements.error}
      empty={filter.trim() ? `No statement matches ${filter.trim()}.` : "No statement is listed."}
    />
  )

  const settled =
    (data !== undefined || statements.error !== undefined) &&
    (!recorder || engineId === "" || detail.data !== undefined || detail.error !== undefined)
  if (text === undefined) {
    return (
      <DetailFrame rail={rail}>
        {head}
        {address !== null && !settled ? (
          <>
            <HostIdentity
              className="border-b-0 pb-0"
              logo={<StatementTile />}
              title={<TextShimmer>Reading the statement…</TextShimmer>}
              menu={<span className="size-8" />}
              facts={<span>&nbsp;</span>}
            />
            <LoadingRows />
          </>
        ) : statements.error && !data ? (
          <CouldNotRead
            what="the statements"
            error={statements.error}
            onRetry={statements.refresh}
          />
        ) : (
          <NotListed data={data} back={back} />
        )}
      </DetailFrame>
    )
  }

  const gone = !found && data !== undefined
  const mayQuery = engine.has("query") && can("service.control")
  const toQuery = (sql: string) => goto("query", { sql })
  const verbs: Verb[] = [
    {
      key: "copy",
      label: "Copy statement",
      icon: Copy,
      run: () => void copyText(text, "Statement copied"),
    },
  ]
  const missing = listed && listed.hitRatio >= 0 && listed.hitRatio < 0.9 ? listed : undefined

  return (
    <DetailFrame rail={rail}>
      <div className="flex min-w-0 shrink-0 flex-col gap-5">
        {head}
        <HostIdentity
          label="About this statement"
          className="animate-rise border-b-0 pb-0"
          logo={<StatementTile text={text} />}
          title={<StatementLine text={text} className="text-body font-medium" />}
          // What is wrong with its reads is one of its facts, so the line is
          // two rows for every statement; only a statement the list has let
          // go of says so beside its text.
          state={
            gone ? <Status tone="stopped" label="No longer among the top statements" /> : undefined
          }
          facts={
            listed ? (
              <>
                <span>
                  <Fig>{shareWords(listed.share)}</Fig> of runtime
                </span>
                <FactDot />
                <span className="numeric">
                  {listed.calls.toLocaleString()} {listed.calls === 1 ? "call" : "calls"}
                </span>
                <FactDot />
                <span className="numeric">{millis(listed.meanMs)} a call</span>
                {missing && (
                  <>
                    <FactDot />
                    <span className="text-warning">
                      {shareWords(1 - missing.hitRatio)} of its reads miss the cache
                    </span>
                  </>
                )}
                {data?.since && (
                  <>
                    <FactDot />
                    <span title={timestamp(data.since)}>
                      counted from {relativeTime(data.since)}
                    </span>
                  </>
                )}
              </>
            ) : recorded ? (
              <>
                <span>
                  <Fig>{shareWords(recorded.share)}</Fig> of {windowWords(hours)}&apos;s statement
                  time
                </span>
                <FactDot />
                <span className="numeric">
                  {recorded.calls.toLocaleString()} {recorded.calls === 1 ? "call" : "calls"}
                </span>
              </>
            ) : (
              <span>&nbsp;</span>
            )
          }
          command={
            mayQuery && (
              <Button size="sm" onClick={() => toQuery(text)}>
                <CodeBracket />
                {/* On a phone the command shares the title's row: the word
                    that says where is enough, and the statement keeps the room. */}
                <span>
                  <span className="max-sm:sr-only">Open in </span>Query
                </span>
              </Button>
            )
          }
          menu={<VerbMenu verbs={verbs} label="More for this statement" />}
        />
      </div>

      <DetailTabs label="Views of this statement" views={views} tab={tab} onTab={setTab}>
        {tab === "figures" ? (
          listed ? (
            <Figures
              statement={listed}
              since={data?.since}
              points={detail.data?.points}
              window={windowWords(hours)}
            />
          ) : (
            <EmptyState
              mark={<EngineMark engine={engine} size="md" />}
              title="Not among the statements the server lists now"
              description="Its counters are no longer in the top of the list, or were reset. What it cost while it ran is in its history."
              action={
                recorder && (
                  <Button size="sm" variant="outline" onClick={() => setTab("history")}>
                    Open its history
                  </Button>
                )
              }
            />
          )
        ) : tab === "text" ? (
          <CodeView
            code={text}
            language={engine.editor}
            label={hasPlaceholder(text) ? "The statement, as a shape" : "The statement"}
            // As tall as the statement, up to what a page shows without a scroll of its own.
            className={heightFor(text)}
            actions={
              mayQuery && (
                <Button size="xs" variant="ghost" onClick={() => toQuery(text)}>
                  <CodeBracket />
                  Open in Query
                </Button>
              )
            }
          />
        ) : tab === "plan" ? (
          <StatementPlan text={text} onQuery={mayQuery ? toQuery : undefined} />
        ) : (
          <StatementRecorded detail={detail} listed={recorded} hours={hours} onHours={setHours} />
        )}
      </DetailTabs>
    </DetailFrame>
  )
}

function heightFor(text: string): string {
  const lines = text.split("\n").length
  return lines <= 2 ? "h-28" : lines <= 5 ? "h-44" : lines <= 12 ? "h-72" : "h-96"
}

function railRow(
  id: number,
  key: string,
  statement: Pick<DbStatement, "query"> & Partial<DbStatement>,
  carried: { tab: string | null; sort: string | null },
): RailRow {
  const line = oneLine(statement.query)
  return {
    key,
    href: statementHref(id, key, carried),
    label: `Statement: ${line.slice(0, 80)}`,
    mark: <StatementTile text={statement.query} small />,
    title: <StatementLine text={statement.query} className="font-[inherit]" />,
    sub:
      statement.calls === undefined || statement.meanMs === undefined
        ? "not among the top statements"
        : `${compact(statement.calls)} ${statement.calls === 1 ? "call" : "calls"} · ${millis(statement.meanMs)} each`,
    figure: statement.share === undefined ? undefined : shareWords(statement.share),
  }
}

/**
 * A statement's mark: what it does, as the hue the lists give its first word
 * (`statementVerb`), on the tile every identity line stands its thing on.
 */
function StatementTile({ text, small }: { text?: string; small?: boolean }) {
  const hue = text === undefined ? undefined : statementVerb(oneLine(text))?.color
  return (
    <span
      aria-hidden
      className={cn(
        "flex shrink-0 items-center justify-center border border-hairline bg-card text-muted-foreground",
        small ? "size-7 rounded-md [&>svg]:size-3.5" : "size-10 rounded-lg [&>svg]:size-5",
      )}
      style={hue ? { color: hue } : undefined}
    >
      <CodeBracket />
    </span>
  )
}

/** The address names a statement nothing lists: said, with the way back. */
function NotListed({
  data,
  back,
}: {
  data: DbStatements | undefined
  back: { href: string; label: string }
}) {
  const { engine, href } = useDatabase()
  const off = data !== undefined && !data.supported
  return (
    <EmptyState
      mark={<EngineMark engine={engine} size="md" />}
      title={
        off
          ? `This server is not counting its ${engine.nouns.statements}`
          : "This statement is not among the ones the server lists"
      }
      description={
        off
          ? "A statement has a page once the server keeps statistics for it. The Statements view says how to turn them on."
          : "The list has moved on since the link was made, or the statistics were reset."
      }
      action={
        <Button size="sm" variant="outline" asChild>
          <Link href={off ? href("performance", { view: "statements" }) : back.href}>
            {off ? "Open Statements" : `Back to ${back.label.toLowerCase()}`}
          </Link>
        </Button>
      }
    />
  )
}

/**
 * What a statement costs, as readings: each a sentence on a mark in its
 * measure's hue, with the measure over the recorded window at the line's end
 * where the recorder keeps one. The sheet said these as seven tiles, a figure
 * over a label, and left the reader to join them.
 */
function Figures({
  statement,
  since,
  points,
  window,
}: {
  statement: DbStatement
  since?: string
  /** The statement's recorded window, where the engine has a history. */
  points?: Parameters<typeof historyRows>[0]
  /** The window in words: "the last hour". */
  window: string
}) {
  const rows = useMemo(() => historyRows(points ?? []), [points])
  const trend = (pick: (row: (typeof rows)[number]) => number | undefined) =>
    rows.filter((row) => row.time !== undefined).map((row) => pick(row) ?? 0)
  const each = rowsPerCall(statement)
  const missed = statement.hitRatio >= 0 && statement.hitRatio < 0.9
  return (
    // Two across where the view itself has the width for two: beside the
    // rail on a laptop it has not, and a reading cut to "60% of all stateme…"
    // is not a reading.
    <div className="@container" data-slot="statement-figures">
      <div className="-mx-2 grid min-w-0 gap-x-6 @5xl:grid-cols-2">
        <ReadingLine
          mark={
            <WireMark size="md" hue="var(--chart-1)">
              <Stopwatch aria-hidden />
            </WireMark>
          }
          name="Time"
          hint={`${millis(statement.totalMs)} in all${since ? `, counted from ${relativeTime(since)}` : ""}`}
          trend={
            <TileTrend
              values={trend((row) => row.time)}
              label={`Its time over ${window}`}
              color="var(--chart-1)"
            />
          }
        >
          {shareWords(statement.share)} of all statement time
        </ReadingLine>
        <ReadingLine
          mark={
            <WireMark size="md" hue="var(--chart-2)">
              <Layers aria-hidden />
            </WireMark>
          }
          name="Calls"
          hint={
            each === undefined
              ? undefined
              : `${each >= 10 ? compact(each) : each.toFixed(1)} rows a call`
          }
          trend={
            <TileTrend
              values={trend((row) => row.calls)}
              label={`Its calls over ${window}`}
              color="var(--chart-2)"
            />
          }
        >
          {statement.calls.toLocaleString()} {statement.calls === 1 ? "call" : "calls"},{" "}
          {statement.rows.toLocaleString()} {statement.rows === 1 ? "row" : "rows"}
        </ReadingLine>
        <ReadingLine
          mark={
            <WireMark size="md" hue="var(--chart-4)">
              <Clock aria-hidden />
            </WireMark>
          }
          name="Each call"
          hint={
            statement.maxMs === undefined ? undefined : `${millis(statement.maxMs)} at its slowest`
          }
          trend={
            <TileTrend
              values={trend((row) => row.mean)}
              label={`Its time per call over ${window}`}
              color="var(--chart-4)"
            />
          }
        >
          {millis(statement.meanMs)} on average
        </ReadingLine>
        {statement.hitRatio >= 0 && (
          <ReadingLine
            mark={
              <WireMark size="md" tone={missed ? "warning" : "success"}>
                {missed ? <MagnifyingGlass aria-hidden /> : <Database aria-hidden />}
              </WireMark>
            }
            name="Cache"
            hint={
              missed
                ? "the rest were read from disk, which is what makes a statement slow"
                : "it reads what the server already holds in memory"
            }
          >
            <span className={cn(missed && "text-warning")}>
              {percent(statement.hitRatio * 100, 1)} of its reads came from cache
            </span>
          </ReadingLine>
        )}
      </div>
    </div>
  )
}
