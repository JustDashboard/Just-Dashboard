"use client"

import { useState } from "react"
import {
  ClockRewind,
  CodeBracket,
  Copy,
  Database,
  Filter,
  MoreHorizontal,
  Warning,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { clock, timestamp } from "@/lib/format"
import { useLogView } from "@/lib/log-view"
import { latency, latencyTone } from "@/lib/requests"
import { useSessionState } from "@/lib/view-state"
import type { DbConnection, DbHistoryEntry, DbQueryEntry, DbQueryLog } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import type { ServiceLogsContext, ServiceLogsView } from "@/components/logs/service-logs"
import { laneStyle } from "@/components/logs/log-text"
import { Address } from "@/components/deploy/request-marks"
import { FactDot } from "@/components/metrics/host-identity"
import { PaneFooter, Well } from "@/components/panel"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { VerbMenu, type Verb } from "@/components/verbs"
import { StatementsPanel } from "@/components/database/monitor-tab"
import {
  QUERY_RANGES,
  around,
  countWords,
  entryKeys,
  historyEntries,
  oneLine,
  queryNoun,
  rangeSince,
  refreshEvery,
  sortEntries,
  sourceWords,
  type QueryOrder,
  type QueryRange,
} from "@/components/database/queries"

/** How many rows one read asks for; the server's cap is 500. */
const LIMIT = 200

/** The engines whose statements the query console runs. */
const NOT_SQL = new Set(["redis", "mongodb"])

/** The engines that keep statement statistics for the Top statements list. */
const TOP_STATEMENTS = new Set(["postgres", "mysql"])

type Reading = QueryOrder | "top"

/**
 * The Queries view — Commands on Redis — of a saved connection's server log,
 * wherever that log is read: the database's page and `/logs`.
 */
export function databaseQueriesView(
  conn: DbConnection,
  onQuery?: (sql: string) => void,
): ServiceLogsView {
  return {
    id: "queries",
    label: queryNoun(conn.driver).view,
    render: (ctx) => <DatabaseQueries conn={conn} ctx={ctx} onQuery={onQuery} />,
  }
}

/**
 * A database's queries, the way the deployment page reads its requests.
 *
 * Each statement the server itself recorded as slow — Postgres and MongoDB in
 * their server log, MySQL in its slow-log table, Redis in SLOWLOG, ClickHouse
 * in system.query_log — as one row with when, how long, what, who and from
 * where, newest first or slowest first. A row opens in place on the whole
 * statement and everything recorded with it, and on the one question a slow
 * statement raises that the rows cannot answer: what else the server said
 * around it, which is the log's History a minute either side. The totals
 * over every run (`/statements`) are the third reading, Top statements.
 *
 * An empty list is never left to read as "nothing was slow": when a setting
 * keeps the log from recording anything, it says which, what it is now, and
 * hands over the statement that changes it, to run in the query console —
 * the advisor's rule, since turning logging on is the operator's call.
 *
 * A SQLite file has no server keeping any of this, so its list is the
 * statements this dashboard ran on it. `ctx` is the logs pane this view sits
 * in; a database whose log is not on this machine has none, and its rows
 * simply have nowhere to send the reader. `onQuery` is where the page runs a
 * statement — the Databases section's query console — offered to an account
 * that may run one, on an engine that speaks SQL.
 */
export function DatabaseQueries({
  conn,
  ctx,
  onQuery,
}: {
  conn: DbConnection
  ctx?: ServiceLogsContext
  onQuery?: (sql: string) => void
}) {
  const { can } = useAuth()
  const noun = queryNoun(conn.driver)
  const sqlite = conn.driver === "sqlite"
  const [range, setRange] = useSessionState<QueryRange>(`databases.${conn.id}.queries.range`, "24h")
  const [reading, setReading] = useSessionState<Reading>(
    `databases.${conn.id}.queries.reading`,
    "latest",
  )
  const top = TOP_STATEMENTS.has(conn.driver)
  const shown: Reading = reading === "top" && !top ? "latest" : reading

  const log = usePoll(
    (signal) =>
      get<DbQueryLog>(
        `/databases/${conn.id}/querylog`,
        // The window's start is worked out at each read, not when the range
        // was chosen, or a page left open would keep asking about a morning.
        { since: rangeSince(range), limit: LIMIT },
        signal,
      ),
    refreshEvery(range),
    [conn.id, range],
    { enabled: !sqlite },
  )
  const history = usePoll(
    (signal) => get<DbHistoryEntry[]>(`/databases/${conn.id}/history`, { limit: LIMIT }, signal),
    30_000,
    [conn.id],
    { enabled: sqlite },
  )

  const openInQuery =
    onQuery && !NOT_SQL.has(conn.driver) && can("service.control") ? onQuery : undefined

  const result = sqlite ? history : log
  const entries = sqlite ? historyEntries(history.data ?? []) : (log.data?.entries ?? [])
  const rows = sortEntries(entries, shown === "slowest" ? "slowest" : "latest")
  const data = log.data
  // A global setting Postgres reports as off can be on for one database or
  // one role (ALTER DATABASE … SET), so rows in its log answer the Notice
  // better than the setting does. MySQL's rows beside it are the stand-in the
  // Notice explains, so it stays.
  const enable =
    data?.enable && (data.source !== "log" || rows.length === 0) ? data.enable : undefined

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex min-h-9 shrink-0 items-stretch border-b border-hairline">
        <ChipStrip
          aria-label="Which statements"
          className="scroll-affordance min-w-0 flex-1 px-2 py-1 max-sm:mx-0 max-sm:my-0 max-sm:px-2 max-sm:py-1 sm:flex-nowrap sm:overflow-x-auto"
        >
          <FilterChip selected={shown === "latest"} onClick={() => setReading("latest")}>
            Latest
          </FilterChip>
          <FilterChip selected={shown === "slowest"} onClick={() => setReading("slowest")}>
            Slowest
          </FilterChip>
          {top && (
            <FilterChip selected={shown === "top"} onClick={() => setReading("top")}>
              Top statements
            </FilterChip>
          )}
        </ChipStrip>
        {!sqlite && shown !== "top" && (
          <div
            role="group"
            aria-label="Window"
            className="flex shrink-0 items-center gap-0.5 border-l border-hairline px-1"
          >
            {QUERY_RANGES.map((r) => (
              <FilterChip
                key={r.id}
                selected={range === r.id}
                onClick={() => setRange(r.id)}
                className="px-2"
              >
                {r.label}
              </FilterChip>
            ))}
          </div>
        )}
      </div>

      {shown === "top" ? (
        <div className="min-h-0 flex-1 overflow-auto p-4">
          <StatementsPanel conn={conn} title="Top statements" />
        </div>
      ) : (
        <>
          <div className="min-h-0 flex-1 overflow-auto bg-surface-sunken">
            {enable && (
              <EnableNotice
                enable={enable}
                driver={conn.driver}
                onQuery={openInQuery}
                className="m-3"
              />
            )}
            {result.error && !result.data ? (
              <div className="p-6">
                <ErrorState error={result.error} className="max-w-lg" />
              </div>
            ) : !result.data ? (
              <LoadingRows rows={8} className="p-3" />
            ) : rows.length === 0 ? (
              <div className="flex items-center justify-center p-6">
                <QueriesEmpty log={sqlite ? undefined : data} noun={noun} range={range} />
              </div>
            ) : (
              <QueryRows
                entries={rows}
                noun={noun}
                ctx={ctx}
                range={range}
                fromLog={data?.source === "log"}
                onQuery={openInQuery}
              />
            )}
          </div>
          {result.data && rows.length > 0 && (
            <PaneFooter className="gap-x-2 px-3 text-hint text-muted-foreground">
              <span className="numeric">{countWords(rows.length, noun)}</span>
              {sqlite ? (
                <>
                  <FactDot />
                  <span>run from this dashboard</span>
                </>
              ) : (
                data && (
                  <>
                    <FactDot />
                    <span>from {sourceWords(data.source)}</span>
                    {data.threshold && (
                      <>
                        <FactDot />
                        <span className="numeric">slower than {data.threshold}</span>
                      </>
                    )}
                    {data.reason ? (
                      <>
                        <FactDot />
                        <span className="min-w-0 truncate text-warning" title={data.reason}>
                          {data.reason}
                        </span>
                      </>
                    ) : (
                      data.truncated && (
                        <>
                          <FactDot />
                          <span>the newest {LIMIT} — narrow the window to see the rest</span>
                        </>
                      )
                    )}
                  </>
                )
              )}
            </PaneFooter>
          )}
        </>
      )}
    </div>
  )
}

/** Why the list is empty, in the words of whatever keeps it so. */
function QueriesEmpty({
  log,
  noun,
  range,
}: {
  log?: DbQueryLog
  noun: ReturnType<typeof queryNoun>
  range: QueryRange
}) {
  if (!log) {
    return (
      <EmptyState
        icon={Database}
        title="Nothing run from here yet"
        description="A SQLite database is a file, not a server, so nothing records its statements but this dashboard. What is run on the Query page is listed here."
      />
    )
  }
  if (!log.supported) {
    return (
      <EmptyState
        icon={Database}
        title={`No record of ${noun.many} here`}
        description={log.reason}
      />
    )
  }
  if (log.enable) {
    return (
      <EmptyState
        icon={Database}
        title={`No ${noun.title.toLowerCase()} recorded`}
        description={`The server writes none until ${log.enable.setting} is changed — the statement above does that.`}
      />
    )
  }
  const window = QUERY_RANGES.find((r) => r.id === range)?.label ?? range
  return (
    <EmptyState
      icon={Database}
      title={`No ${noun.title.toLowerCase()} in the last ${window}`}
      description={
        log.reason ??
        (log.threshold
          ? `The list holds ${noun.many} slower than ${log.threshold} from ${sourceWords(log.source)}, and none ran that slowly in this window.`
          : `Nothing in ${sourceWords(log.source)} for this window. A longer one may have some.`)
      }
    />
  )
}

const ENABLE_WORDS: Record<string, (current: string) => string> = {
  log_min_duration_statement: (current) =>
    `Postgres writes a statement to its log only when it takes longer than log_min_duration_statement, which is ${current} — off. At 250ms it records every statement slower than a quarter of a second. It takes effect on reload, without a restart: run SELECT pg_reload_conf(); after it.`,
  slow_query_log: () =>
    "The slow query log is off. This turns it on for statements slower than 250 ms and keeps a copy in a table this page reads; it holds until the server restarts.",
  log_output: (current) =>
    `The slow query log is on, but log_output is ${current}: it goes only to a file inside the server. Adding TABLE keeps a copy this page reads.`,
}

/**
 * The setting that keeps the list empty, as the advisor draws a fix: what it
 * is, the statement that changes it, and the console to run it in.
 */
function EnableNotice({
  enable,
  driver,
  onQuery,
  className,
}: {
  enable: NonNullable<DbQueryLog["enable"]>
  driver: string
  onQuery?: (sql: string) => void
  className?: string
}) {
  const words = ENABLE_WORDS[enable.setting]
  return (
    <Notice
      tone="warning"
      icon={Warning}
      className={className}
      title={
        driver === "postgres"
          ? "Slow statements are not being logged"
          : "Slow queries are not being recorded"
      }
    >
      <div className="max-w-3xl space-y-2">
        <p>{words ? words(enable.current) : `${enable.setting} is ${enable.current}.`}</p>
        <Well className="text-hint whitespace-pre-wrap text-foreground">{enable.sql}</Well>
        <div className="flex flex-wrap gap-1.5">
          {onQuery && (
            <Button size="xs" variant="outline" onClick={() => onQuery(enable.sql)}>
              <CodeBracket className="size-3" />
              Open in Query
            </Button>
          )}
          <Button
            size="xs"
            variant="ghost"
            onClick={() => void copyText(enable.sql, "Statement copied")}
          >
            <Copy className="size-3" />
            Copy
          </Button>
        </div>
      </div>
    </Notice>
  )
}

/**
 * The statements as the request console draws requests: log lines rather
 * than a table, read down the left for the time and across for the one that
 * matters, on the console's own sunken ground. A slow figure takes amber past
 * a second and a failed statement its red edge, and the statement itself
 * stays ink — it is the message. On a phone a row is two lines.
 */
function QueryRows({
  entries,
  noun,
  ctx,
  range,
  fromLog,
  onQuery,
}: {
  entries: DbQueryEntry[]
  noun: ReturnType<typeof queryNoun>
  ctx?: ServiceLogsContext
  range: QueryRange
  fromLog: boolean
  onQuery?: (sql: string) => void
}) {
  const [open, setOpen] = useState<string | null>(null)
  const keys = entryKeys(entries)
  const { highlight } = useLogView()
  const plain = !highlight
  const wide = useMediaQuery("(min-width: 640px)")
  // A column only for what some row has: SLOWLOG knows no database and
  // Postgres's log no row count, and a column of dashes is width the
  // statement needed.
  const has = {
    user: entries.some((e) => e.user),
    db: entries.some((e) => e.db),
    client: entries.some((e) => e.client),
    rows: entries.some((e) => e.rows !== undefined),
  }

  return (
    <div className="animate-rise py-1 font-mono text-xs leading-relaxed">
      {wide && (
        <div className="sticky top-0 z-10 flex items-center gap-3 border-b border-hairline bg-surface-sunken pr-4 pb-1 font-sans text-hint font-medium text-muted-foreground">
          <span aria-hidden className="w-0.5 shrink-0" />
          <span className="w-16 shrink-0">Time</span>
          <span className="w-16 shrink-0 text-right">Took</span>
          <span className="min-w-0 flex-1 capitalize">{noun.one}</span>
          {has.user && <span className="hidden w-24 shrink-0 lg:block">User</span>}
          {has.db && <span className="hidden w-24 shrink-0 lg:block">Database</span>}
          {has.client && <span className="hidden w-32 shrink-0 xl:block">Client</span>}
          {has.rows && <span className="hidden w-14 shrink-0 text-right xl:block">Rows</span>}
        </div>
      )}
      {entries.map((entry, i) => {
        const key = keys[i]
        const slow = latencyTone(entry.durationMs) === "warning"
        const face = cn(
          "flex w-full cursor-default text-left focus-ring-inset transition-colors hover:bg-row-hover",
          entry.error && !plain && "bg-wash-danger",
          open === key && "bg-accent hover:bg-accent",
        )
        const edge = (
          <span
            aria-hidden
            className={cn(
              "w-0.5 shrink-0 self-stretch",
              entry.error ? "bg-destructive" : slow ? "bg-warning/70" : "bg-transparent",
            )}
          />
        )
        const took = (
          <span
            className={cn(
              "numeric shrink-0 text-right",
              slow ? "font-medium text-warning" : "text-muted-foreground",
            )}
          >
            {latency(entry.durationMs)}
          </span>
        )
        return (
          <div key={key} className="[contain-intrinsic-size:auto_22px] [content-visibility:auto]">
            <button
              type="button"
              aria-expanded={open === key}
              onClick={() => setOpen((current) => (current === key ? null : key))}
              className={cn(face, wide ? "items-center gap-3 py-px pr-4" : "gap-3 pr-3")}
            >
              {edge}
              {wide ? (
                <>
                  <span
                    title={timestamp(entry.at)}
                    className="numeric w-16 shrink-0 text-muted-foreground/70 select-none"
                  >
                    {clock(entry.at)}
                  </span>
                  <span className="w-16 shrink-0 text-right">{took}</span>
                  <span className="min-w-0 flex-1 truncate" title={entry.query}>
                    {oneLine(entry.query)}
                  </span>
                  {has.user && (
                    <Lane value={entry.user} plain={plain} className="hidden w-24 lg:block" />
                  )}
                  {has.db && (
                    <Lane value={entry.db} plain={plain} className="hidden w-24 lg:block" />
                  )}
                  {has.client && (
                    <span className="hidden w-32 shrink-0 truncate xl:block">
                      {entry.client ? (
                        <Address ip={entry.client} plain={plain} />
                      ) : (
                        <span className="text-muted-foreground/50">—</span>
                      )}
                    </span>
                  )}
                  {has.rows && (
                    <span className="numeric hidden w-14 shrink-0 text-right text-muted-foreground/70 xl:block">
                      {entry.rows !== undefined ? entry.rows.toLocaleString() : "—"}
                    </span>
                  )}
                </>
              ) : (
                <span className="block min-w-0 flex-1 py-1">
                  <span className="flex min-w-0 items-center gap-3">
                    <span className="min-w-0 flex-1 truncate">{oneLine(entry.query)}</span>
                    {took}
                  </span>
                  <span className="flex min-w-0 items-center gap-1.5 font-sans text-hint text-muted-foreground">
                    <span className="numeric shrink-0">{clock(entry.at)}</span>
                    {(entry.user || entry.db) && (
                      <>
                        <FactDot />
                        <span className="truncate">
                          {[entry.user, entry.db].filter(Boolean).join(" · ")}
                        </span>
                      </>
                    )}
                  </span>
                </span>
              )}
            </button>
            {open === key && (
              <QueryDetail
                entry={entry}
                noun={noun}
                ctx={ctx}
                range={range}
                fromLog={fromLog}
                plain={plain}
                onQuery={onQuery}
              />
            )}
          </div>
        )
      })}
    </div>
  )
}

/** A name in its lane's hue, the way the log console draws who spoke. */
function Lane({ value, plain, className }: { value?: string; plain: boolean; className?: string }) {
  if (!value) return <span className={cn("shrink-0 text-muted-foreground/50", className)}>—</span>
  return (
    <span
      title={value}
      className={cn("shrink-0 truncate", plain && "text-muted-foreground", className)}
      style={plain ? undefined : laneStyle(value)}
    >
      {value}
    </span>
  )
}

/**
 * One statement, opened: all of it, as it ran, and what was recorded with
 * it. "Server log around this" is History a minute either side of it —
 * everything the server wrote then, not only the slow lines — because what
 * held a statement up is usually the checkpoint, the lock or the vacuum
 * beside it.
 */
function QueryDetail({
  entry,
  noun,
  ctx,
  range,
  fromLog,
  plain,
  onQuery,
}: {
  entry: DbQueryEntry
  noun: ReturnType<typeof queryNoun>
  ctx?: ServiceLogsContext
  /** The list's window, which "every time this shape was slow" searches. */
  range: QueryRange
  fromLog: boolean
  plain: boolean
  onQuery?: (sql: string) => void
}) {
  const facts: [string, React.ReactNode][] = [
    ["When", timestamp(entry.at)],
    [
      "Took",
      <span
        key="took"
        className={cn(
          "numeric",
          latencyTone(entry.durationMs) === "warning" && "font-medium text-warning",
        )}
      >
        {latency(entry.durationMs)}
      </span>,
    ],
  ]
  if (entry.user) facts.push(["User", <Lane key="u" value={entry.user} plain={plain} />])
  if (entry.db) facts.push(["Database", <Lane key="d" value={entry.db} plain={plain} />])
  if (entry.client) facts.push(["Client", <Address key="c" ip={entry.client} plain={plain} />])
  if (entry.rows !== undefined) facts.push(["Rows", entry.rows.toLocaleString()])
  if (entry.examined !== undefined) facts.push(["Examined", entry.examined.toLocaleString()])
  if (entry.code) facts.push(["Code", entry.code])
  if (entry.error) {
    facts.push([
      "Error",
      <span key="e" className="text-destructive">
        {entry.error}
      </span>,
    ])
  }
  if (entry.fp) facts.push(["Shape", entry.fp])

  const verbs: Verb[] = [
    {
      key: "copy",
      label: `Copy the ${noun.one}`,
      icon: Copy,
      run: () => void copyText(entry.query, "Statement copied"),
    },
    {
      key: "json",
      label: "Copy as JSON",
      icon: Copy,
      run: () => void copyText(JSON.stringify(entry, null, 2), "Copied as JSON"),
    },
    ...(ctx && fromLog && entry.fp
      ? [
          {
            key: "shape",
            label: "Every time this shape was slow",
            icon: Filter,
            // The list's window, not the pane's: the pane's is wherever the
            // last "server log around this" left it, two minutes wide.
            run: () =>
              ctx.openHistory({
                since: rangeSince(range),
                until: new Date().toISOString(),
                fields: { event: ["slow"], fp: [entry.fp!] },
                levels: [],
                q: "",
              }),
          },
        ]
      : []),
  ]

  const action = "w-full font-sans max-sm:h-9 sm:w-auto"

  return (
    <div className="animate-rise border-y border-hairline bg-background px-4 py-3 pl-[1.125rem]">
      <Well className="max-h-72 whitespace-pre-wrap">{entry.query}</Well>
      <dl className="mt-2.5 grid grid-cols-1 gap-x-6 gap-y-1.5 sm:grid-cols-2 xl:grid-cols-3">
        {facts.map(([label, value]) => (
          <div key={label} className="flex min-w-0 gap-2">
            <dt className="w-20 shrink-0 font-sans text-hint leading-5 text-muted-foreground">
              {label}
            </dt>
            <dd className="min-w-0 truncate" title={typeof value === "string" ? value : undefined}>
              {value}
            </dd>
          </div>
        ))}
      </dl>
      <div className="mt-3 flex flex-col gap-1.5 sm:flex-row sm:flex-wrap sm:items-center">
        {ctx && (
          <Button
            size="xs"
            variant="outline"
            className={action}
            onClick={() => ctx.openHistory({ ...around(entry.at), fields: {}, levels: [], q: "" })}
          >
            <ClockRewind className="size-3" />
            Server log around this
          </Button>
        )}
        {onQuery && (
          <Button
            size="xs"
            variant="outline"
            className={action}
            onClick={() => onQuery(entry.query)}
          >
            <CodeBracket className="size-3" />
            Open in Query
          </Button>
        )}
        <VerbMenu
          verbs={verbs}
          align="start"
          label={`More actions for this ${noun.one}`}
          trigger={
            <Button
              size="xs"
              variant="outline"
              className={action}
              aria-label={`More actions for this ${noun.one}`}
            >
              <MoreHorizontal className="size-3" />
              <span className="sm:hidden">More</span>
            </Button>
          }
        />
      </div>
    </div>
  )
}
