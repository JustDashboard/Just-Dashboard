"use client"

import { useMemo, useState } from "react"
import { CodeBracket, Copy, Cross, StopCircle } from "@/components/icons"
import { cn } from "@/lib/utils"
import { copyText } from "@/lib/clipboard"
import { bytes, duration, timestamp } from "@/lib/format"
import { useViewState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { Disclosure, FormFact } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Meter } from "@/components/meter"
import { Detail, DetailList, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { EmptyNote, LoadingPanel, Notice } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { compact } from "@/components/database/home/readings"
import { EngineMark } from "@/components/database/kit"
import {
  SESSION_STATES,
  THRESHOLDS,
  blockedBy,
  blockersOf,
  concernOf,
  countByState,
  heldUp,
  longestRunning,
  matchesSession,
  orderSessions,
  stateSeconds,
  statusOf,
  stopsFor,
} from "@/components/database/ops/performance-activity"
import { endSession, readActivity, readChQueries } from "@/components/database/ops/performance-api"
import {
  Named,
  NoFigure,
  NotAvailable,
  SessionState,
  Stale,
  StatementLine,
  ViewRead,
} from "@/components/database/ops/performance-parts"
import { useStops } from "@/components/database/ops/performance-stops"
import type {
  ChQuery,
  DbActivity,
  DbSessionStatus,
} from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"

/** How often the session list is read while the view is open. */
const EVERY_MS = 5000

const isState = (value: string): value is DbSessionStatus =>
  SESSION_STATES.some((state) => state.id === value)

/**
 * Who is on the server and what each is doing.
 *
 * A session is read by its state: running a statement, waiting on a lock,
 * holding a transaction open while it does nothing, or only connected. The
 * one figure on its row is how long it has been in that state — a statement's
 * running time is a working session's alone, so an idle pooled connection is
 * never the longest-running statement — and it takes a tone once it has gone
 * on too long: thirty seconds of running, ten of waiting, ten of an idle
 * transaction.
 *
 * The idle sessions are most of any pooled server and none of what the reader
 * came for, so they are folded under the list. A session held up names the
 * one it is behind, and that one says how many it holds; pressing either
 * opens the other.
 *
 * Stopping a statement and ending a session change no data, so they are
 * offered on a protected connection too — to a role that may remove things.
 */
export function SessionsView() {
  const { engine } = useDatabase()
  // An analytic engine has no sessions that sit idle: what is on it is the
  // queries running now, and it has more to say about each.
  return engine.can("clickhouseViews") ? <RunningQueries /> : <Sessions />
}

function Sessions() {
  const { id, param, select } = useDatabase()
  const wide = useMediaQuery("(min-width: 1100px)")
  const activity = usePoll((signal) => readActivity(id, signal), EVERY_MS, [id])
  const [filter, setFilter] = useState("")
  const [idleOpen, setIdleOpen] = useViewState(`databases.${id}.performance.idle`, false)
  const [openPid, setOpenPid] = useState<string | null>(null)
  // The session whose panel is open is kept as it was last seen, so a session
  // that ends while it is being read does not take the panel with it.
  const [kept, setKept] = useState<DbActivity | null>(null)

  const asked = param("state")
  const state = isState(asked) ? asked : null

  const sessions = activity.data?.sessions
  const held = useMemo(() => blockedBy(sessions ?? []), [sessions])
  const queued = useMemo(() => heldUp(sessions ?? []), [sessions])
  const counts = useMemo(() => countByState(sessions ?? []), [sessions])
  const ordered = useMemo(() => orderSessions(sessions ?? []), [sessions])
  const longest = useMemo(() => longestRunning(sessions ?? []), [sessions])

  const live = openPid ? sessions?.find((session) => session.pid === openPid) : undefined
  if (live && live !== kept) setKept(live)
  const shown = openPid ? (live ?? (kept?.pid === openPid ? kept : undefined)) : undefined

  const stopping = useStops(activity.refresh)
  const stops = stopping.can

  const rowProps = {
    held,
    queued,
    stops,
    wide,
    onOpen: setOpenPid,
    onCancel: stopping.cancel,
    onTerminate: stopping.terminate,
  }

  return (
    <Panel plain aria-label="Sessions">
      {stopping.dialog}
      <PanelHeader
        title="Sessions"
        actions={
          <>
            <Stale poll={activity} />
            <SearchInput
              dense
              aria-label="Filter the sessions"
              placeholder="Filter by account, application or statement"
              value={filter}
              containerClassName="sm:w-72"
              onChange={(event) => setFilter(event.target.value)}
            />
          </>
        }
      />
      <ViewRead poll={activity} what="the sessions" skeleton={<LoadingPanel plain rows={6} />}>
        {(data) => {
          if (!data.supported) {
            return (
              <div className="pt-4">
                <NotAvailable title="The sessions cannot be listed" reason={data.reason} />
              </div>
            )
          }
          const matching = ordered.filter((session) => matchesSession(session, filter))
          // Idle sessions are folded away unless they are what was asked for.
          const listed = matching.filter((session) =>
            state ? statusOf(session) === state : statusOf(session) !== "idle",
          )
          const idle = state ? [] : matching.filter((session) => statusOf(session) === "idle")
          const longestIdle = idle.reduce(
            (most, session) => Math.max(most, session.idleSeconds ?? 0),
            0,
          )
          return (
            <div className="animate-rise space-y-3 pt-3">
              <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2 max-sm:flex-col max-sm:items-stretch">
                <ChipStrip aria-label="Sessions by state" role="group" className="sm:flex-1">
                  {SESSION_STATES.filter((entry) => counts[entry.id] > 0 || entry.id === state).map(
                    (entry) => (
                      <FilterChip
                        key={entry.id}
                        selected={state === entry.id}
                        onClick={() => select({ state: state === entry.id ? null : entry.id })}
                      >
                        {entry.chip}
                        <ChipCount>{counts[entry.id].toLocaleString()}</ChipCount>
                      </FilterChip>
                    ),
                  )}
                </ChipStrip>
                {longest && longest.seconds >= 1 && (
                  <p className="text-hint text-muted-foreground">
                    Longest statement{" "}
                    <button
                      type="button"
                      onClick={() => setOpenPid(longest.pid)}
                      aria-label={`${duration(longest.seconds)}: open session ${longest.pid}`}
                      className={cn(
                        "numeric rounded-sm font-medium focus-ring hover:underline",
                        longest.seconds >= THRESHOLDS.active ? "text-warning" : "text-foreground",
                      )}
                    >
                      {duration(longest.seconds)}
                    </button>
                  </p>
                )}
              </div>

              <PanelBody flush className="group-data-[plain]/panel:-mx-4">
                {data.sessions.length === 0 ? (
                  <EmptyNote className="px-4">
                    The server listed no session to this account. It lists the sessions an account
                    may see, and showing every account&apos;s takes a grant.
                  </EmptyNote>
                ) : listed.length === 0 ? (
                  <EmptyNote className="px-4">
                    {filter.trim()
                      ? `No session matches ${filter.trim()}.`
                      : state
                        ? "No session is in that state now."
                        : "Nobody is running anything: every session is idle."}
                  </EmptyNote>
                ) : (
                  <SessionRows sessions={listed} {...rowProps} />
                )}
              </PanelBody>

              {idle.length > 0 && (
                <Disclosure
                  quiet
                  open={idleOpen}
                  onOpenChange={setIdleOpen}
                  summary={
                    <>
                      {idle.length.toLocaleString()} idle{" "}
                      {idle.length === 1 ? "session" : "sessions"}
                    </>
                  }
                  facts={
                    longestIdle > 0 ? `connected and waiting, up to ${duration(longestIdle)}` : ""
                  }
                >
                  {idleOpen && (
                    <div className="-mx-4">
                      <SessionRows sessions={idle} {...rowProps} />
                    </div>
                  )}
                </Disclosure>
              )}
            </div>
          )
        }}
      </ViewRead>

      <SessionPanel
        pid={openPid}
        session={shown}
        ended={Boolean(shown) && !live && Boolean(sessions)}
        held={held}
        stops={stops}
        onOpen={setOpenPid}
        onClose={() => setOpenPid(null)}
        onCancel={stopping.cancel}
        onTerminate={stopping.terminate}
      />
    </Panel>
  )
}

type RowTools = {
  held: Map<string, string[]>
  /** How many sessions stand behind each, however long the queue. */
  queued: Map<string, number>
  stops: { cancel: boolean; kill: boolean }
  wide: boolean
  onOpen: (pid: string) => void
  onCancel: (session: DbActivity) => void
  onTerminate: (session: DbActivity) => void
}

/** What a session's figure measures, said for the pointer and for a reader who cannot see the state beside it. */
function figureWords(session: DbActivity): string {
  switch (statusOf(session)) {
    case "active":
      return "Its statement has been running for"
    case "blocked":
      return "It has been waiting for"
    case "idle_in_transaction":
      return "Its transaction has been open for"
    case "idle":
      return "It has been idle for"
    case "background":
      return ""
  }
}

function StateFigure({ session }: { session: DbActivity }) {
  const seconds = stateSeconds(session)
  if (seconds === undefined) return <NoFigure />
  const concern = concernOf(session)
  return (
    <span
      title={`${figureWords(session)} ${duration(seconds)}`}
      className={cn(
        "numeric",
        concern === "long-blocked"
          ? "font-medium text-destructive"
          : concern
            ? "font-medium text-warning"
            : statusOf(session) === "idle" && "text-muted-foreground",
      )}
    >
      {seconds < 1 && seconds > 0 ? "<1s" : duration(seconds)}
    </span>
  )
}

/** Who a session is: the account and the application, each in its own hue. */
function Who({ session }: { session: DbActivity }) {
  return (
    <span className="flex min-w-0 items-center gap-1.5">
      {session.user ? <Named name={session.user} className="font-medium" /> : <NoFigure />}
      {session.application && (
        <>
          <span aria-hidden className="text-muted-foreground/60">
            ·
          </span>
          <Named name={session.application} />
        </>
      )}
    </span>
  )
}

/** What a session waits on, and the sessions on either side of it in the queue. */
function Waiting({
  session,
  queued,
  onOpen,
  quiet,
}: {
  session: DbActivity
  queued: Map<string, number>
  onOpen: (pid: string) => void
  /** Drawn down a phone, a session waiting on nothing has no line for it. */
  quiet?: boolean
}) {
  const behind = blockersOf(session)
  const holding = queued.get(session.pid) ?? 0
  if (behind.length === 0 && holding === 0 && !session.wait) return quiet ? null : <NoFigure />
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
      {holding > 0 && (
        <Tag tone={holding > THRESHOLDS.blocking ? "danger" : "warning"}>
          blocking {holding.toLocaleString()}
        </Tag>
      )}
      {behind.length > 0 ? (
        <span className="flex min-w-0 items-center gap-1 text-hint text-muted-foreground">
          behind
          {behind.slice(0, 3).map((pid) => (
            <button
              key={pid}
              type="button"
              onClick={() => onOpen(pid)}
              aria-label={`Open session ${pid}, which this one waits on`}
              className="rounded-sm font-mono text-foreground focus-ring hover:underline"
            >
              {pid}
            </button>
          ))}
          {behind.length > 3 && <span>+{behind.length - 3}</span>}
        </span>
      ) : (
        session.wait &&
        holding === 0 && (
          <span className="min-w-0 truncate text-hint text-muted-foreground" title={session.wait}>
            {session.wait}
          </span>
        )
      )}
    </span>
  )
}

function Stops({
  session,
  stops,
  onCancel,
  onTerminate,
}: {
  session: DbActivity
} & Pick<RowTools, "stops" | "onCancel" | "onTerminate">) {
  const offered = stopsFor(session, stops)
  if (!offered.cancel && !offered.terminate) return null
  return (
    <span className="flex items-center justify-end gap-0.5">
      {offered.cancel && (
        <IconAction
          label={`Cancel the statement of session ${session.pid}`}
          onClick={() => onCancel(session)}
        >
          <StopCircle />
        </IconAction>
      )}
      {offered.terminate && (
        <IconAction label={`Terminate session ${session.pid}`} onClick={() => onTerminate(session)}>
          <Cross />
        </IconAction>
      )}
    </span>
  )
}

/**
 * The sessions as a table where there is room for one, and drawn down — a
 * state, who, the statement, what it waits on — where there is not: chosen
 * once by the window's width rather than drawn twice and half hidden.
 */
function SessionRows({ sessions, ...tools }: { sessions: DbActivity[] } & RowTools) {
  const { queued, wide, onOpen } = tools
  if (!wide) {
    return (
      <ul className="divide-y divide-hairline border-y border-hairline">
        {sessions.map((session) => (
          <li key={session.pid} className="space-y-1.5 px-4 py-2.5">
            <div className="flex min-w-0 items-center justify-between gap-3">
              <span className="flex min-w-0 items-center gap-2">
                <SessionState status={statusOf(session)} late={Boolean(concernOf(session))} />
                {session.self && <Tag>this dashboard</Tag>}
              </span>
              <span className="flex shrink-0 items-center gap-2 text-xs">
                <StateFigure session={session} />
                <Stops session={session} {...tools} />
              </span>
            </div>
            <button
              type="button"
              onClick={() => onOpen(session.pid)}
              aria-label={`Open session ${session.pid}`}
              className="flex w-full min-w-0 flex-col gap-1 rounded-sm text-left text-xs focus-ring"
            >
              <span className="flex min-w-0 items-center gap-2">
                <span className="font-mono text-muted-foreground">{session.pid}</span>
                <Who session={session} />
              </span>
              {session.query && (
                <StatementLine text={session.query} className="text-hint text-foreground/80" />
              )}
            </button>
            <Waiting session={session} queued={queued} onOpen={onOpen} quiet />
          </li>
        ))}
      </ul>
    )
  }
  return (
    <Table>
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          <TableHead>State</TableHead>
          <TableHead className="px-2">Session</TableHead>
          <TableHead className="px-2 text-right">For</TableHead>
          <TableHead className="px-2">Waiting on</TableHead>
          <TableHead className="px-2">Statement</TableHead>
          <TableHead>
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {sessions.map((session) => (
          <TableRow
            key={session.pid}
            onActivate={() => onOpen(session.pid)}
            aria-label={`Session ${session.pid}`}
          >
            <TableCell className="py-2">
              <span className="flex items-center gap-2">
                <SessionState status={statusOf(session)} late={Boolean(concernOf(session))} />
                {session.self && <Tag>this dashboard</Tag>}
              </span>
            </TableCell>
            <TableCell className="max-w-64 px-2 py-2">
              <Who session={session} />
              <span className="flex min-w-0 items-center gap-1.5 font-mono text-hint text-muted-foreground">
                <span>{session.pid}</span>
                {session.client && <span className="truncate">· {session.client}</span>}
              </span>
            </TableCell>
            <TableCell className="px-2 py-2 text-right">
              <StateFigure session={session} />
            </TableCell>
            <TableCell className="max-w-56 px-2 py-2">
              <Waiting session={session} queued={queued} onOpen={onOpen} />
            </TableCell>
            <TableCell className="w-full max-w-0 px-2 py-2">
              {session.query ? <StatementLine text={session.query} /> : <NoFigure />}
            </TableCell>
            <TableCell className="py-1">
              <Stops session={session} {...tools} />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

/**
 * One session, opened: everything the server says about it, the whole of its
 * statement, and the two ways of stopping it. A glance beside the list, which
 * goes on being read behind it.
 */
function SessionPanel({
  pid,
  session,
  ended,
  held,
  stops,
  onOpen,
  onClose,
  onCancel,
  onTerminate,
}: {
  /** The session asked for, or none. */
  pid: string | null
  /** It, as the list has it or last had it; nothing where the list never did. */
  session: DbActivity | undefined
  /** It is no longer in the list: what is shown is how it was last seen. */
  ended: boolean
} & Pick<RowTools, "held" | "stops" | "onOpen" | "onCancel" | "onTerminate"> & {
    onClose: () => void
  }) {
  const { engine, goto } = useDatabase()
  const { can } = useAuth()
  if (!pid) return null
  if (!session) {
    // A session can wait on something the list does not hold: a prepared
    // transaction, another account's session this one may not see. The press
    // that asked for it is answered with that, not with nothing.
    return (
      <SidePanel
        open
        onOpenChange={(open) => !open && onClose()}
        width="md"
        initialFocus="body"
        title={
          <>
            Session <span className="font-mono">{pid}</span>
          </>
        }
        description="A session the list does not hold."
      >
        <Notice title="Not in the session list">
          <p>
            The server lists no session by this id to this account. It may be a prepared
            transaction, a session of the server&apos;s own, or one that has just ended.
          </p>
        </Notice>
      </SidePanel>
    )
  }
  const status = statusOf(session)
  const offered = ended ? { cancel: false, terminate: false } : stopsFor(session, stops)
  const behind = blockersOf(session)
  const holding = held.get(session.pid) ?? []
  const query = session.query
  const pids = (list: string[]) => (
    <span className="flex flex-wrap gap-x-2">
      {list.map((pid) => (
        <button
          key={pid}
          type="button"
          onClick={() => onOpen(pid)}
          className="rounded-sm font-mono focus-ring hover:underline"
        >
          {pid}
        </button>
      ))}
    </span>
  )
  return (
    <SidePanel
      open
      onOpenChange={(open) => !open && onClose()}
      width="md"
      initialFocus="body"
      title={
        <>
          Session <span className="font-mono">{session.pid}</span>
        </>
      }
      description="What this session is doing, and the ways to stop it."
      footer={
        (offered.cancel || offered.terminate) && (
          <>
            {offered.cancel && (
              <Button variant="outline" onClick={() => onCancel(session)}>
                <StopCircle />
                Cancel statement
              </Button>
            )}
            {offered.terminate && (
              <Button variant="destructive" onClick={() => onTerminate(session)}>
                <Cross />
                Terminate session
              </Button>
            )}
          </>
        )
      }
    >
      <div className="space-y-4">
        {ended && (
          <Notice title="This session has ended">
            <p>It is no longer on the server. This is how it was last seen.</p>
          </Notice>
        )}
        <DetailList>
          <Detail label="State">
            <span className="flex flex-wrap items-center gap-2">
              <SessionState status={status} late={Boolean(concernOf(session))} />
              {/* The engine's own word, where the shared one does not already say it. */}
              {status === "background" && session.state && (
                <span className="font-mono text-muted-foreground">{session.state}</span>
              )}
              {session.self && <Tag>this dashboard</Tag>}
            </span>
          </Detail>
          {session.user && (
            <Detail label="Account">
              <Named name={session.user} />
            </Detail>
          )}
          {session.application && (
            <Detail label="Application">
              <Named name={session.application} />
            </Detail>
          )}
          {session.client && (
            <Detail label="Client" className="font-mono">
              {session.client}
            </Detail>
          )}
          {session.database && (
            <Detail label="Database" className="font-mono">
              {session.database}
            </Detail>
          )}
          {(status === "active" || status === "blocked") && (
            <Detail label={status === "blocked" ? "Waiting for" : "Statement running for"}>
              <StateFigure session={session} />
            </Detail>
          )}
          {session.transactionSeconds !== undefined && (
            <Detail label="Transaction open for">
              <span className="numeric" title={timestamp(session.transactionStart)}>
                {duration(session.transactionSeconds)}
              </span>
            </Detail>
          )}
          {session.idleSeconds !== undefined && (
            <Detail label="Idle for">
              <span className="numeric">{duration(session.idleSeconds)}</span>
            </Detail>
          )}
          {session.wait && (
            <Detail label="Waiting on" className="font-mono wrap-anywhere">
              {session.wait}
            </Detail>
          )}
          {behind.length > 0 && <Detail label="Blocked by">{pids(behind)}</Detail>}
          {holding.length > 0 && <Detail label="Blocking">{pids(holding)}</Detail>}
          {session.connectedAt && (
            <Detail label="Connected">{timestamp(session.connectedAt)}</Detail>
          )}
        </DetailList>

        <div className="space-y-1.5">
          <div className="flex min-h-6 items-center justify-between gap-3">
            <p className="eyebrow">
              {status === "active" || status === "blocked" ? "Statement" : "Last statement"}
            </p>
            {query && (
              <span className="flex items-center gap-1">
                {engine.has("query") && can("service.control") && (
                  <Button size="xs" variant="ghost" onClick={() => goto("query", { sql: query })}>
                    <CodeBracket />
                    Open in Query
                  </Button>
                )}
                <Button
                  size="xs"
                  variant="ghost"
                  onClick={() => void copyText(query, "Statement copied")}
                >
                  <Copy />
                  Copy
                </Button>
              </span>
            )}
          </div>
          {query ? (
            <Well className="max-h-80 overflow-auto text-hint leading-relaxed whitespace-pre-wrap">
              {query}
            </Well>
          ) : (
            <p className="text-hint text-muted-foreground">The server reports no statement.</p>
          )}
        </div>
      </div>
    </SidePanel>
  )
}

/**
 * What is running on an analytic engine now, with what each query has read
 * and how far along it says it is. Stopping one is the engine's own kill,
 * which there ends the query and not a session.
 */
function RunningQueries() {
  const { id, conn, engine } = useDatabase()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const queries = usePoll((signal) => readChQueries(id, signal), 3000, [id])
  const mayStop = can("destructive") && engine.can("kill")

  const stop = (query: ChQuery) =>
    confirm({
      title: "Stop query",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{query.id}</span>,
        facts: (
          <>
            <FormFact label="As">{query.user}</FormFact>
            <FormFact label="On">{conn.name}</FormFact>
          </>
        ),
      },
      description: (
        <>
          <p>The server stops this query. What it had read is discarded.</p>
          <Well className="max-h-32 overflow-auto text-hint whitespace-pre-wrap">
            {query.query}
          </Well>
        </>
      ),
      confirmLabel: "Stop query",
      action: async () => {
        await endSession(id, query.id)
      },
      onDone: queries.refresh,
    })

  return (
    <Panel plain aria-label="Running queries">
      {dialog}
      <PanelHeader title="Running queries" actions={<Stale poll={queries} />} />
      <ViewRead
        poll={queries}
        what="the running queries"
        skeleton={<LoadingPanel plain rows={4} />}
      >
        {(data) => {
          const others = data.queries.filter((query) => !query.self)
          return (
            <PanelBody flush className="animate-rise group-data-[plain]/panel:-mx-4">
              {others.length === 0 ? (
                <EmptyNote className="px-4">
                  Nothing is running but this page&apos;s own read of the list.
                </EmptyNote>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow className="hover:bg-transparent">
                      <TableHead>Query</TableHead>
                      <TableHead className="px-2">Account</TableHead>
                      <TableHead className="px-2 text-right">For</TableHead>
                      <TableHead className="px-2">Progress</TableHead>
                      <TableHead className="px-2 text-right">Read</TableHead>
                      <TableHead className="px-2 text-right">Memory</TableHead>
                      <TableHead>
                        <span className="sr-only">Actions</span>
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {others.map((query) => (
                      <TableRow key={query.id}>
                        <TableCell className="w-full max-w-0 py-2">
                          <StatementLine text={query.query} />
                          <span className="block truncate font-mono text-hint text-muted-foreground">
                            {[query.kind, query.clientName, query.client]
                              .filter(Boolean)
                              .join(" · ")}
                          </span>
                        </TableCell>
                        <TableCell className="px-2 py-2">
                          <Named name={query.user} />
                        </TableCell>
                        <TableCell
                          className={cn(
                            "numeric px-2 py-2 text-right",
                            query.elapsed >= THRESHOLDS.active && "font-medium text-warning",
                          )}
                        >
                          {query.elapsed < 1 ? "<1s" : duration(query.elapsed)}
                        </TableCell>
                        <TableCell className="px-2 py-2">
                          {query.cancelled ? (
                            <Tag tone="warning">stopping</Tag>
                          ) : query.progress >= 0 ? (
                            <span className="flex w-28 items-center gap-2">
                              <Meter
                                value={query.progress * 100}
                                size="thin"
                                label="Rows read of the estimate"
                                className="flex-1"
                              />
                              <span className="numeric text-hint text-muted-foreground">
                                {Math.round(query.progress * 100)}%
                              </span>
                            </span>
                          ) : (
                            <span className="text-hint text-muted-foreground">no estimate</span>
                          )}
                        </TableCell>
                        <TableCell className="numeric px-2 py-2 text-right">
                          {compact(query.rowsRead)} rows
                          <span className="block text-hint text-muted-foreground">
                            {bytes(query.bytesRead)}
                          </span>
                        </TableCell>
                        <TableCell className="numeric px-2 py-2 text-right">
                          {bytes(query.memory)}
                          <span className="block text-hint text-muted-foreground">
                            peak {bytes(query.peakMemory)}
                          </span>
                        </TableCell>
                        <TableCell className="py-1 text-right">
                          {mayStop && !query.cancelled && (
                            <IconAction
                              label={`Stop query ${query.id}`}
                              onClick={() => stop(query)}
                            >
                              <StopCircle />
                            </IconAction>
                          )}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </PanelBody>
          )
        }}
      </ViewRead>
    </Panel>
  )
}
