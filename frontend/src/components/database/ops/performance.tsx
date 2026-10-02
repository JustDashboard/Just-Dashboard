"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { cn } from "@/lib/utils"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import { StatGrid } from "@/components/stat-tile"
import { Notice } from "@/components/state"
import { ChipCount, tabClasses } from "@/components/tabs"
import { CouldNotRead } from "@/components/database/home/blocks"
import { compact, staled, type Reading } from "@/components/database/home/readings"
import { gauge } from "@/components/database/home/samples"
import { ReadingGrid, ReadingTile } from "@/components/database/home/tiles"
import { useSamples } from "@/components/database/home/use-samples"
import { BlockedState, SectionFrame } from "@/components/database/kit"
import { tallySessions, type SessionTally } from "@/components/database/ops/performance-activity"
import { readActivity } from "@/components/database/ops/performance-api"
import { FileView, MergesView, PartsView } from "@/components/database/ops/performance-engine"
import {
  READING_LABELS,
  performanceReadings,
  performanceSample,
  type FigureFamily,
  type LiveSessions,
} from "@/components/database/ops/performance-figures"
import { IndexesView } from "@/components/database/ops/performance-indexes"
import { LocksView } from "@/components/database/ops/performance-locks"
import { OverviewView } from "@/components/database/ops/performance-overview"
import { ServerDown, isDown } from "@/components/database/ops/performance-parts"
import { ReplicationView } from "@/components/database/ops/performance-replication"
import { SessionsView } from "@/components/database/ops/performance-sessions"
import { StatementsView } from "@/components/database/ops/performance-statements"
import { TablesView } from "@/components/database/ops/performance-tables"
import type { DbServerStats } from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"

type View =
  | "overview"
  | "file"
  | "sessions"
  | "locks"
  | "statements"
  | "tables"
  | "indexes"
  | "parts"
  | "merges"
  | "replication"

/**
 * What a SQL server is doing, and how it is being kept.
 *
 * Six readings head the page — sessions against the limit, who is working,
 * who is waiting on a lock, the engine's own fourth figure, transactions and
 * the cache — each with the shape it has had since the page was opened. They
 * are drawn from the server's counters, sampled every few seconds; a rate is
 * the difference between two samples, so the first line of a trend appears
 * with the second, and nothing here is recorded history. Who is working and
 * who is waiting are counted from the session list where it can be read, so
 * the tiles say what the list under them says.
 *
 * Under them the page reads as many ways as the engine has: the same samples
 * as charts; the sessions, with the two ways of stopping one; who is waiting
 * on whom; the statements that have cost the most; every table and index by
 * what it weighs and how it is kept, with the engine's maintenance on each;
 * and how the data reaches other servers. An analytic engine adds its parts
 * and merges, and a file-based one is read as its file. A view the engine
 * does not have is not in the strip — the registry says which — and the view
 * on screen is in the address, so a link opens on the same reading.
 *
 * The readings are tiles and not counts on the strip because they are the
 * page's first answer: whether anything is wrong is said before a view is
 * chosen.
 */
export function SqlPerformance() {
  const { status } = useDatabase()
  return (
    <SectionFrame section="performance">
      {/* Keyed on whether the server answers: a page that comes back starts
          its samples again rather than joining them across the gap. */}
      {isDown(status.state) ? <ServerDown what="Performance" /> : <Answering key="answering" />}
    </SectionFrame>
  )
}

/** How often the session list is read while the page is open. */
const SESSIONS_EVERY_MS = 5000

/**
 * The session list, read by the page rather than by the view that draws it.
 *
 * Three things on the page are counts of this one list — the tiles for who
 * is working and who is waiting, the count on the Locks tab, the chips over
 * the sessions — and read once, here, they are the same count. The Locks
 * view reads it too: a blocker's state and its application are this list's.
 * The newest tally is also held for the page's samples to take, so the
 * charts and the tiles' trends are drawn from the same counts.
 */
function useLiveSessions(id: number, enabled: boolean) {
  const newest = useRef<SessionTally | undefined>(undefined)
  const poll = usePoll(
    async (signal) => {
      try {
        const answer = await readActivity(id, signal)
        newest.current = tallySessions(answer)
        return answer
      } catch (error) {
        // A list that could not be read is no tally: the samples go back to
        // the server's own counters rather than repeat the last one read.
        newest.current = undefined
        throw error
      }
    },
    SESSIONS_EVERY_MS,
    [id],
    { enabled },
  )
  const { data, error } = poll
  const live = useMemo<LiveSessions | undefined>(() => {
    // A list whose poll has failed since is the reading before: the server's
    // own counters, which are still arriving, are the truer figure then.
    const tally = error ? undefined : tallySessions(data)
    return tally ? { tally } : undefined
  }, [data, error])
  return { activity: poll, live, newest }
}

/**
 * The tiles keep their height from the first paint: the band a trend is
 * drawn in is held open before the trend has two points to draw, and a tile
 * is never shorter than it is with every part in place. Nothing under the
 * readings moves when a line arrives, which matters here more than anywhere:
 * the rows below carry the buttons that stop a session, and a list that drops
 * by a row's height under the pointer puts the press on its neighbour.
 *
 * The six stand in one row from the width a laptop has, a step sooner than
 * on the database's home: there the figures are the page, and here they head
 * the view the reader came to work in.
 */
const STEADY_TILES = cn(
  "[&_[data-slot=stat-tile]>div:empty]:block",
  "max-sm:[&_[data-slot=stat-tile]]:min-h-[5.75rem] sm:[&_[data-slot=stat-tile]]:min-h-[9.25rem]",
  "xl:grid-cols-6",
  "xl:[&>*:nth-child(n):nth-child(n)]:border-t-0 xl:[&>*:nth-child(n+2):nth-child(n)]:border-l",
)

function Answering() {
  const { id, engine, param, goto } = useDatabase()
  const file = engine.can("fileBased")
  const analytic = engine.can("clickhouseViews")
  const family: FigureFamily = file ? "file" : analytic ? "analytic" : "server"
  const { activity, live, newest } = useLiveSessions(id, engine.can("sessions") && !analytic)
  const stats = useSamples<DbServerStats>(
    id,
    // Called when a snapshot lands, with the tally the session list last gave.
    (answer) => performanceSample(answer, newest.current),
    engine.can("stats"),
  )
  const trends = useMediaQuery("(min-width: 640px)")

  const objects = engine.nouns.objects
  const views = useMemo<{ id: View; label: string }[]>(
    () => [
      // A file has no rates to chart: its Overview would be six flat lines.
      ...(engine.can("stats") && !engine.can("fileBased")
        ? [{ id: "overview" as const, label: "Overview" }]
        : []),
      ...(engine.can("sqliteFile") ? [{ id: "file" as const, label: "File" }] : []),
      ...(engine.can("sessions")
        ? [
            {
              id: "sessions" as const,
              label: engine.can("clickhouseViews") ? "Running queries" : "Sessions",
            },
          ]
        : []),
      ...(engine.can("locks") ? [{ id: "locks" as const, label: "Locks" }] : []),
      ...(engine.can("statements") ? [{ id: "statements" as const, label: "Statements" }] : []),
      ...(engine.can("tableStats")
        ? [{ id: "tables" as const, label: objects[0].toUpperCase() + objects.slice(1) }]
        : []),
      ...(engine.can("indexStats") ? [{ id: "indexes" as const, label: "Indexes" }] : []),
      ...(engine.can("clickhouseViews")
        ? [
            { id: "parts" as const, label: "Parts" },
            { id: "merges" as const, label: "Merges" },
          ]
        : []),
      ...(engine.can("replication") ? [{ id: "replication" as const, label: "Replication" }] : []),
    ],
    [engine, objects],
  )
  const asked = param("view")
  // A press is answered at once and reaches the address after: the view
  // pressed is the one drawn until the address has caught up with it, and
  // the address is the one believed whenever it moves by itself — Back,
  // Forward, a link.
  const [pressed, setPressed] = useState<{ view: View; from: string } | null>(null)
  // Dropped the moment the address moves, wherever to: coming Back to the
  // address a press was made from is not that press again.
  if (pressed && pressed.from !== asked) setPressed(null)
  const wanted = pressed && pressed.from === asked ? pressed.view : asked
  const view: View | undefined = views.some((entry) => entry.id === wanted)
    ? (wanted as View)
    : views[0]?.id
  const scope = param("scope")
  const open = (next: View) => {
    if (next === view) return
    setPressed({ view: next, from: asked })
    // Pushed, not replaced: a view is a place on this page, and Back from
    // Locks is Sessions again. What was open in the view being left is its
    // own — a panel does not follow the reader to another view — and only
    // the schema the two storage views are narrowed to goes with them.
    goto("performance", {
      ...(next === views[0].id ? {} : { view: next }),
      ...(scope && (next === "tables" || next === "indexes") ? { scope } : {}),
    })
  }

  // On a phone the strip is wider than the page and scrolls sideways: the
  // view on screen is kept in sight in it.
  const strip = useRef<HTMLDivElement>(null)
  useEffect(() => {
    strip.current
      ?.querySelector('[aria-pressed="true"]')
      ?.scrollIntoView({ block: "nearest", inline: "nearest" })
  }, [view])

  const samples = stats.samples
  const answer = stats.answer
  const unread = stats.error && samples.length === 0 ? stats.error : undefined
  const refused = answer && !answer.supported ? answer : undefined
  const stale = Boolean(stats.error) && samples.length > 0
  const figures = performanceReadings(family, samples, answer, live)
  const readings: Reading[] = (
    stale ? staled(figures, samples[samples.length - 1]?.at) : figures
  ).map((reading) =>
    stats.loading
      ? { ...reading, value: undefined, hint: undefined, trend: undefined, pending: true }
      : unread
        ? { ...reading, value: undefined, hint: "Could not be read", trend: undefined }
        : reading,
  )
  const bones: Reading[] = READING_LABELS[family].map((label) => ({
    key: label,
    label,
    value: undefined,
    pending: true,
  }))
  const tiles = (stats.loading ? bones : readings).map((reading) => (
    <ReadingTile key={reading.key} reading={reading} trends={trends} />
  ))

  const sessions = gauge(samples, "sessions") ?? activity.data?.sessions.length
  const waiting = live ? live.tally.waiting : gauge(samples, "sessionsWaiting")
  const merging = gauge(samples, "runningMerges")
  const count = (entry: View): number | undefined =>
    entry === "sessions"
      ? analytic
        ? gauge(samples, "runningQueries")
        : sessions || undefined
      : entry === "locks"
        ? waiting || undefined
        : entry === "merges"
          ? merging || undefined
          : undefined

  if (!view) {
    return (
      <BlockedState engine={engine} thing="performance readings">
        This server&apos;s engine reports none of what this page reads.
      </BlockedState>
    )
  }

  return (
    <>
      {refused && (
        <Notice title="This server's statistics are not available">
          <p className="wrap-anywhere">{refused.reason}</p>
        </Notice>
      )}
      {engine.can("stats") &&
        (file ? (
          // Five figures, which the six-across grid would leave a hole beside.
          <StatGrid columns={5} dense>
            {tiles}
          </StatGrid>
        ) : (
          <ReadingGrid className={STEADY_TILES}>{tiles}</ReadingGrid>
        ))}
      {unread && (
        <CouldNotRead what="the server's statistics" error={unread} onRetry={stats.refresh} />
      )}

      <div className="min-w-0 space-y-6">
        {/* A strip of pressed buttons, not a landmark: these are readings of
            one page, and the rail is where the product navigates. */}
        <div
          ref={strip}
          role="group"
          aria-label="Performance views"
          className="flex [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
        >
          {views.map((entry) => {
            const figure = count(entry.id)
            return (
              <button
                key={entry.id}
                type="button"
                aria-pressed={view === entry.id}
                onClick={() => open(entry.id)}
                className={tabClasses(view === entry.id, "h-10")}
              >
                {entry.label}
                {figure !== undefined && <ChipCount>{compact(figure)}</ChipCount>}
              </button>
            )
          })}
        </div>
        {view === "overview" ? (
          <OverviewView
            samples={samples}
            stats={answer}
            loading={stats.loading}
            error={stats.error}
          />
        ) : view === "file" ? (
          <FileView />
        ) : view === "sessions" ? (
          <SessionsView activity={activity} />
        ) : view === "locks" ? (
          <LocksView activity={activity} />
        ) : view === "statements" ? (
          <StatementsView />
        ) : view === "tables" ? (
          <TablesView />
        ) : view === "indexes" ? (
          <IndexesView />
        ) : view === "parts" ? (
          <PartsView />
        ) : view === "merges" ? (
          <MergesView />
        ) : (
          <ReplicationView />
        )}
      </div>
    </>
  )
}
