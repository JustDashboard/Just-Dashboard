"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { useParams } from "next/navigation"
import { CodeBracket, Copy, Cross, StopCircle } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { duration, timestamp } from "@/lib/format"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { EmptyNote, EmptyState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { DbClientMark } from "@/components/database/client-mark"
import type { KnownClient } from "@/components/database/clients"
import { CouldNotRead } from "@/components/database/home/blocks"
import { EngineMark } from "@/components/database/kit"
import { useKnownClients } from "@/components/database/use-known-clients"
import {
  SESSION_STATES,
  blockedBy,
  blockersOf,
  concernOf,
  matchesSession,
  orderSessions,
  stateSeconds,
  statusOf,
  stopsFor,
} from "@/components/database/ops/performance-activity"
import { sessionHref, unsegment } from "@/components/database/ops/performance-address"
import { readActivity } from "@/components/database/ops/performance-api"
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
  activityKey,
  sessionsFilterKey,
  sessionsIdleKey,
} from "@/components/database/ops/performance-lists"
import { SessionState } from "@/components/database/ops/performance-parts"
import { ClientTile, StateFigure, Who } from "@/components/database/ops/performance-session-parts"
import { useStops } from "@/components/database/ops/performance-stops"
import type { DbActivity, DbSessionStatus } from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"

/** How often the session list is read while a session's page is open: the Sessions view's cadence. */
const EVERY_MS = 5000

type Tab = "activity" | "waits"

const isState = (value: string): value is DbSessionStatus =>
  SESSION_STATES.some((state) => state.id === value)

const stateWord = (session: DbActivity) =>
  SESSION_STATES.find((state) => state.id === statusOf(session))?.label ?? statusOf(session)

/**
 * One session, as a place of its own: `/databases/<id>/performance/sessions/<pid>`.
 *
 * It was a sheet over the Sessions view: what the server says about a
 * session, the whole of its statement, and the two ways of stopping it. The
 * page reads the same list the view does, at the view's cadence, so the
 * session is live while it is open — and a session does not outlast its
 * work. One that ends while its page is open stays as it was last seen, said
 * to have ended, with nothing left to stop: that is what its reader came to
 * find out, not a failure to show them.
 */
export function SessionPage() {
  const { engine } = useDatabase()
  const params = useParams<{ pid: string }>()
  const pid = unsegment(params.pid ?? "")
  return (
    <DetailGate
      // An analytic engine has running queries, not sessions that sit: they
      // are read whole in their own view.
      has={engine.kind === "sql" && engine.can("sessions") && !engine.can("clickhouseViews")}
      thing="a session"
    >
      <Session key={pid ?? ""} pid={pid} />
    </DetailGate>
  )
}

function Session({ pid }: { pid: string | null }) {
  const { id, conn, engine, href, goto, param } = useDatabase()
  const { can } = useAuth()
  const activity = usePoll((signal) => readActivity(id, signal), EVERY_MS, [id], {
    key: activityKey(id),
  })
  const stopping = useStops(activity.refresh)
  const known = useKnownClients()
  const [filter, setFilter] = useSessionState(sessionsFilterKey(id), "")
  const [idleOpen] = useViewState(sessionsIdleKey(id), false)
  const asked = param("state")
  const state = isState(asked) ? asked : null

  const sessions = activity.data?.sessions
  const held = useMemo(() => blockedBy(sessions ?? []), [sessions])
  const live = pid ? sessions?.find((session) => session.pid === pid) : undefined
  // Kept as it was last seen, so a session that ends while it is being read
  // does not take its page with it.
  const [kept, setKept] = useState<DbActivity | null>(null)
  if (live && live !== kept) setKept(live)
  const shown = live ?? kept ?? undefined
  const ended = Boolean(shown) && !live && Boolean(sessions)

  const behind = shown ? blockersOf(shown) : []
  const holding = shown ? (held.get(shown.pid) ?? []) : []
  const views = useMemo<DetailView<Tab>[]>(
    () => [
      { key: "activity", label: "Activity" },
      { key: "waits", label: "Waits", count: behind.length + holding.length },
    ],
    [behind.length, holding.length],
  )
  const [tab, setTab] = useDetailTab(views)
  const carried = { tab: tab === "activity" ? null : tab, state }

  // The rail is the list as the view draws it: the idle sessions are in it
  // only where the list was narrowed to them or its fold was left open.
  const rows = useMemo<RailRow[]>(() => {
    const matching = orderSessions(sessions ?? []).filter((session) =>
      matchesSession(session, filter),
    )
    const listed = matching.filter((session) =>
      state ? statusOf(session) === state : statusOf(session) !== "idle" || idleOpen,
    )
    return listed.map((session) => railRow(id, session, known, carried))
    // `carried` is two strings: held by what they are.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessions, filter, state, idleOpen, id, known, carried.tab, carried.state])
  const pinned =
    shown && !rows.some((row) => row.key === shown.pid)
      ? railRow(id, shown, known, carried, ended)
      : undefined
  const total = sessions?.length ?? 0
  const caption = !activity.data
    ? activity.error
      ? "The sessions could not be read"
      : "Reading the sessions…"
    : `${rows.length} of ${total} ${total === 1 ? "session" : "sessions"}${
        state
          ? ` · ${SESSION_STATES.find((entry) => entry.id === state)?.label.toLowerCase()}`
          : idleOpen
            ? ""
            : " · idle folded"
      }`

  const back = { href: href("performance", { view: "sessions", state }), label: "Sessions" }
  const head = (
    <DetailHead
      title={`${conn.name} · Session ${pid ?? ""}`}
      back={back}
      noun="session"
      step={stepOf(rows, pid)}
    />
  )
  const rail = (
    <DetailRail
      label="Sessions"
      placeholder="Account, application or statement"
      query={filter}
      onQuery={setFilter}
      caption={caption}
      rows={rows}
      current={pid}
      pinned={pinned}
      loading={!activity.data && !activity.error}
      empty={filter.trim() ? `No session matches ${filter.trim()}.` : "No session is listed."}
    />
  )

  if (!shown) {
    return (
      <DetailFrame rail={rail}>
        {head}
        {pid !== null && !activity.data && !activity.error ? (
          <>
            <HostIdentity
              className="border-b-0 pb-0"
              logo={<DbClientMark size="md" />}
              title={<SessionTitle pid={pid} />}
              state={<TextShimmer className="text-xs">Reading the sessions…</TextShimmer>}
              facts={<span>&nbsp;</span>}
            />
            <LoadingRows />
          </>
        ) : activity.error && !activity.data ? (
          <CouldNotRead what="the sessions" error={activity.error} onRetry={activity.refresh} />
        ) : activity.data && !activity.data.supported ? (
          <EmptyState
            mark={<EngineMark engine={engine} size="md" />}
            title="The sessions cannot be listed"
            description={activity.data.reason}
          />
        ) : (
          // A session can wait on something the list does not hold: a
          // prepared transaction, another account's session this one may not
          // see. The address that asked for it is answered with that.
          <EmptyState
            mark={<EngineMark engine={engine} size="md" />}
            title={
              pid === null
                ? "That is not a session's address"
                : `Session ${pid} is not in the session list`
            }
            description="The server lists no session by this id to this account. It may be a prepared transaction, a session of the server's own, or one that has ended."
            action={
              <Button size="sm" variant="outline" asChild>
                <Link href={back.href}>Back to sessions</Link>
              </Button>
            }
          />
        )}
      </DetailFrame>
    )
  }

  const session = shown
  const status = statusOf(session)
  const offered = ended ? { cancel: false, terminate: false } : stopsFor(session, stopping.can)
  const query = session.query
  const mayQuery = Boolean(query) && engine.has("query") && can("service.control")
  const concern = ended ? undefined : concernOf(session)
  const menu: Verb[] = [
    ...(query && mayQuery
      ? [
          {
            key: "query",
            label: "Open in Query",
            icon: CodeBracket,
            run: () => goto("query", { sql: query }),
          },
        ]
      : []),
    ...(query
      ? [
          {
            key: "copy",
            label: "Copy statement",
            icon: Copy,
            run: () => void copyText(query, "Statement copied"),
          },
        ]
      : []),
    // Beside a statement to cancel, ending the session is the heavier of the
    // two and stands in the menu; alone, it is the page's command.
    ...(offered.cancel && offered.terminate
      ? [
          {
            key: "terminate",
            label: "Terminate session",
            icon: Cross,
            danger: true,
            run: () => stopping.terminate(session),
          },
        ]
      : []),
  ]

  return (
    <DetailFrame rail={rail}>
      {stopping.dialog}
      <div className="flex min-w-0 shrink-0 flex-col gap-5">
        {head}
        <HostIdentity
          label={`About session ${session.pid}`}
          className="animate-rise border-b-0 pb-0"
          logo={
            <ClientTile
              session={session}
              known={known}
              size="md"
              tone={concern === "long-blocked" ? "danger" : concern ? "warning" : undefined}
            />
          }
          title={<SessionTitle pid={session.pid} />}
          state={
            ended ? (
              <Status tone="stopped" label="Ended, shown as it was last seen" />
            ) : (
              <>
                <SessionState status={status} late={Boolean(concern)} />
                {stateSeconds(session) !== undefined && (
                  <span className="text-xs text-muted-foreground">
                    for <StateFigure session={session} />
                  </span>
                )}
                {session.self && <Tag>this dashboard</Tag>}
              </>
            )
          }
          facts={
            <>
              <Who session={session} />
              {session.client && (
                <>
                  <FactDot />
                  <span className="truncate font-mono">{session.client}</span>
                </>
              )}
              {session.database && (
                <>
                  <FactDot />
                  <span className="truncate font-mono">{session.database}</span>
                </>
              )}
              {session.connectedAt && (
                <>
                  <FactDot />
                  <span title={timestamp(session.connectedAt)}>
                    connected {timestamp(session.connectedAt)}
                  </span>
                </>
              )}
            </>
          }
          command={
            offered.cancel ? (
              <Button size="sm" onClick={() => stopping.cancel(session)}>
                <StopCircle />
                <span>
                  Cancel<span className="max-sm:sr-only"> statement</span>
                </span>
              </Button>
            ) : offered.terminate ? (
              <Button size="sm" variant="destructive" onClick={() => stopping.terminate(session)}>
                <Cross />
                <span>
                  Terminate<span className="max-sm:sr-only"> session</span>
                </span>
              </Button>
            ) : undefined
          }
          menu={menu.length > 0 && <VerbMenu verbs={menu} label={`More for session ${pid}`} />}
        />
      </div>

      <DetailTabs label={`Views of session ${session.pid}`} views={views} tab={tab} onTab={setTab}>
        {tab === "activity" ? (
          <Activity session={session} ended={ended} />
        ) : (
          <Waits
            behind={behind}
            holding={holding}
            sessions={sessions ?? []}
            known={known}
            carried={carried}
          />
        )}
      </DetailTabs>
    </DetailFrame>
  )
}

/** The page's name on its identity line: the word and the id on one baseline, the id as the server spells it. */
function SessionTitle({ pid }: { pid: string }) {
  return (
    <span className="inline-flex min-w-0 items-baseline gap-1.5">
      Session <span className="truncate font-mono text-[0.9375em]">{pid}</span>
    </span>
  )
}

function railRow(
  id: number,
  session: DbActivity,
  known: ReadonlyMap<string, KnownClient>,
  carried: { tab: string | null; state: string | null },
  ended = false,
): RailRow {
  const seconds = stateSeconds(session)
  return {
    key: session.pid,
    href: sessionHref(id, session.pid, carried),
    label: `Session ${session.pid}`,
    mark: <ClientTile session={session} known={known} />,
    title: session.user || session.application || `Session ${session.pid}`,
    sub: (
      <>
        {ended ? "ended" : stateWord(session).toLowerCase()} ·{" "}
        <span className="font-mono">{session.pid}</span>
        {session.user && session.application ? ` · ${session.application}` : ""}
      </>
    ),
    figure:
      ended || seconds === undefined
        ? undefined
        : seconds < 1 && seconds > 0
          ? "<1s"
          : duration(seconds),
  }
}

/**
 * What the session is doing: its statement, whole, and the times the server
 * keeps for it. The verbs that act on the statement are the page's own, on
 * its identity line.
 */
function Activity({ session, ended }: { session: DbActivity; ended: boolean }) {
  const status = statusOf(session)
  const running = status === "active" || status === "blocked"
  return (
    <div className="space-y-8" data-slot="session-activity">
      <Panel plain aria-label="Statement">
        <PanelHeader title={running && !ended ? "Statement" : "Last statement"} />
        <PanelBody>
          {session.query ? (
            <Well className="max-h-96 overflow-auto text-hint leading-relaxed whitespace-pre-wrap">
              {session.query}
            </Well>
          ) : (
            <EmptyNote>The server reports no statement.</EmptyNote>
          )}
        </PanelBody>
      </Panel>
      <Panel plain aria-label="Timing">
        <PanelHeader title="Timing" />
        <PanelBody>
          <DetailList>
            {running && (
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
              // The engine's own word for what the thread is doing: a lock it
              // waits on, or only a client that has not sent its next statement.
              <Detail
                label={status === "blocked" ? "Waiting on" : "Doing"}
                className="font-mono wrap-anywhere"
              >
                {session.wait}
              </Detail>
            )}
            {/* The engine's own word for the state, where the shared one does not already say it. */}
            {status === "background" && session.state && (
              <Detail label="State" className="font-mono">
                {session.state}
              </Detail>
            )}
          </DetailList>
        </PanelBody>
      </Panel>
    </div>
  )
}

/**
 * Who the session stands behind, and who stands behind it. Each is a way to
 * that session's own page; one the list does not hold — a prepared
 * transaction, another account's session — is named and says so.
 */
function Waits({
  behind,
  holding,
  sessions,
  known,
  carried,
}: {
  behind: string[]
  holding: string[]
  sessions: readonly DbActivity[]
  known: ReadonlyMap<string, KnownClient>
  carried: { tab: string | null; state: string | null }
}) {
  if (behind.length === 0 && holding.length === 0) {
    return <EmptyNote>It waits on nothing, and nothing waits on it.</EmptyNote>
  }
  return (
    <div className="space-y-8" data-slot="session-waits">
      {behind.length > 0 && (
        <Others
          title="Blocked by"
          pids={behind}
          sessions={sessions}
          known={known}
          carried={carried}
        />
      )}
      {holding.length > 0 && (
        <Others
          title="Blocking"
          pids={holding}
          sessions={sessions}
          known={known}
          carried={carried}
        />
      )}
    </div>
  )
}

function Others({
  title,
  pids,
  sessions,
  known,
  carried,
}: {
  title: string
  pids: string[]
  sessions: readonly DbActivity[]
  known: ReadonlyMap<string, KnownClient>
  carried: { tab: string | null; state: string | null }
}) {
  const { id } = useDatabase()
  return (
    <Panel plain aria-label={title}>
      <PanelHeader
        title={title}
        actions={<span className="numeric text-hint">{pids.length}</span>}
      />
      <PanelBody flush>
        <ul className="divide-y divide-hairline border-b border-hairline">
          {pids.map((pid) => {
            const other = sessions.find((session) => session.pid === pid)
            return (
              <li key={pid}>
                <Link
                  // The waits it was opened from are what its reader follows.
                  href={sessionHref(id, pid, { ...carried, tab: "waits" })}
                  aria-label={`Open session ${pid}`}
                  className="flex h-12 min-w-0 items-center gap-3 px-1 focus-ring-inset transition-colors hover:bg-row-hover"
                >
                  {other ? <ClientTile session={other} known={known} /> : <DbClientMark />}
                  <span className="shrink-0 font-mono text-xs">{pid}</span>
                  <span className="min-w-0 flex-1 truncate text-xs">
                    {other ? (
                      <Who session={other} />
                    ) : (
                      <span className="text-muted-foreground">not in the session list</span>
                    )}
                  </span>
                  {other && (
                    <span className="flex shrink-0 items-center gap-3 text-xs">
                      <SessionState status={statusOf(other)} late={Boolean(concernOf(other))} />
                      <StateFigure session={other} />
                    </span>
                  )}
                </Link>
              </li>
            )
          })}
        </ul>
      </PanelBody>
    </Panel>
  )
}
