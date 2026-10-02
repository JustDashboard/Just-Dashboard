"use client"

import { CouldNotRead } from "@/components/database/home/blocks"
import { compact } from "@/components/database/home/readings"
import { gauge, sqlSample } from "@/components/database/home/samples"
import { useSamples } from "@/components/database/home/use-samples"
import { BlockedState, SectionFrame } from "@/components/database/kit"
import { tallySessions } from "@/components/database/ops/performance-activity"
import { readActivity } from "@/components/database/ops/performance-api"
import { FileView, MergesView, PartsView } from "@/components/database/ops/performance-engine"
import { type LiveSessions } from "@/components/database/ops/performance-figures"
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
import { Notice } from "@/components/state"
import { ChipCount, tabClasses } from "@/components/tabs"
import { usePoll } from "@/hooks/use-poll"
import { useEffect, useMemo, useRef, useState } from "react"

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

export function SqlPerformance() {
  const { status } = useDatabase()
  return (
    <SectionFrame section="performance">
      {isDown(status.state) ? <ServerDown what="Performance" /> : <Answering key="answering" />}
    </SectionFrame>
  )
}

/** How often the session list is read while the page is open. */
const SESSIONS_EVERY_MS = 5000

/** One live session list supplies the views and their counts. */
function useLiveSessions(id: number, enabled: boolean) {
  const poll = usePoll((signal) => readActivity(id, signal), SESSIONS_EVERY_MS, [id], { enabled })
  const { data, error } = poll
  const live = useMemo<LiveSessions | undefined>(() => {
    // A list whose poll has failed since is the reading before: the server's
    // own counters, which are still arriving, are the truer figure then.
    const tally = error ? undefined : tallySessions(data)
    return tally ? { tally } : undefined
  }, [data, error])
  return { activity: poll, live }
}

function Answering() {
  const { id, engine, param, goto } = useDatabase()
  const analytic = engine.can("clickhouseViews")
  const { activity, live } = useLiveSessions(id, engine.can("sessions") && !analytic)
  const stats = useSamples<DbServerStats>(
    id,
    sqlSample,
    engine.can("stats"),
    engine.can("stats") && !engine.can("fileBased"),
  )

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
