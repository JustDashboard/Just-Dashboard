"use client"

import Link from "next/link"
import { duration, plural, relativeTime } from "@/lib/format"
import {
  isoOf,
  latency,
  osProduct,
  reachability,
  sessionHref,
  sessionLength,
  sessionTotals,
  type RdComputerView,
  type RdOverview,
} from "@/lib/remote-desktop"
import { cn } from "@/lib/utils"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { ShareBar } from "@/components/procs/workloads"
import { StatusDot } from "@/components/status-dot"
import { NumberTicker } from "@/components/ui/number-ticker"
import { HueKey } from "@/components/remote-desktop/marks"

const rowClass =
  "flex h-9 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body focus-ring-inset transition-colors hover:bg-row-hover"

/** How many computers a block names; the rest are counted under it. */
const SHOWN = 5

/**
 * The computers read three ways under the identity line: how far each one is
 * right now, where the fortnight's time went, and the sessions themselves.
 * Every computer keeps its hue from the picture to its card, so "the green
 * one" is the same machine on the bar and on the shelf.
 */
export function RemoteBand({
  overview,
  hueOf,
  now,
}: {
  overview: RdOverview
  hueOf: (computer: RdComputerView) => string
  now: number
}) {
  const byId = new Map(overview.computers.map((computer) => [computer.id, computer]))
  return (
    <div
      data-slot="remote-band"
      className="grid min-w-0 gap-x-10 gap-y-8 lg:grid-cols-2 xl:grid-cols-3 [&>*]:min-w-0"
    >
      <ReachBlock computers={overview.computers} hueOf={hueOf} />
      <TimeBlock overview={overview} byId={byId} hueOf={hueOf} now={now} />
      <RecentBlock overview={overview} byId={byId} hueOf={hueOf} now={now} />
    </div>
  )
}

function HeadFigure({ children, word }: { children: React.ReactNode; word: string }) {
  return (
    <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
      <span className="font-medium text-foreground">{children}</span>
      {word}
    </span>
  )
}

function Glyph({ computer }: { computer: RdComputerView }) {
  return (
    <span className="flex size-4 shrink-0 items-center justify-center">
      <ProductGlyph id={osProduct(computer.os)} className="size-4" />
    </span>
  )
}

/**
 * Each computer's distance: the time its remote desktop port took to answer,
 * as a bar against the slowest, so a machine on the far side of a relay
 * stands out from the ones on a direct path.
 */
function ReachBlock({
  computers,
  hueOf,
}: {
  computers: RdComputerView[]
  hueOf: (computer: RdComputerView) => string
}) {
  const ready = computers.filter((computer) => computer.reach.state === "open")
  const slowest = Math.max(1, ...ready.map((computer) => computer.reach.latencyMs ?? 0))
  const ranked = [...computers].sort(
    (a, b) =>
      Number(b.reach.state === "open") - Number(a.reach.state === "open") ||
      (a.reach.latencyMs ?? Infinity) - (b.reach.latencyMs ?? Infinity),
  )
  return (
    <Panel plain aria-label="Reach">
      <PanelHeader
        title="Reach"
        actions={
          <HeadFigure word={`of ${computers.length} ready`}>
            <NumberTicker value={ready.length} />
          </HeadFigure>
        }
      />
      <PanelBody className="space-y-3 pt-4">
        <ul aria-label="How far each computer is" className="-mx-2">
          {ranked.slice(0, SHOWN).map((computer) => {
            const reach = reachability(computer)
            const ms = computer.reach.latencyMs
            return (
              <li key={computer.id}>
                <Link
                  href={sessionHref(computer.id)}
                  aria-label={`Connect to ${computer.name}`}
                  className={rowClass}
                >
                  <HueKey color={hueOf(computer)} />
                  <Glyph computer={computer} />
                  <span className="min-w-0 truncate font-medium">{computer.name}</span>
                  <span
                    className={cn(
                      "min-w-0 flex-1 truncate text-hint",
                      reach.tone === "warning" ? "text-warning" : "text-muted-foreground",
                    )}
                  >
                    {reach.kind === "open" || reach.kind === "live"
                      ? computer.peer
                        ? computer.peer.direct
                          ? "direct"
                          : computer.peer.relay
                            ? `via ${computer.peer.relay}`
                            : ""
                        : computer.host
                      : reach.kind === "offline"
                        ? "offline"
                        : "not answering"}
                  </span>
                  <span className="numeric shrink-0 font-medium">{latency(ms) ?? "—"}</span>
                  <span
                    aria-hidden
                    className="hidden h-1 w-12 shrink-0 overflow-hidden rounded-full bg-meter-track sm:block"
                  >
                    <span
                      className="block h-full rounded-full transition-[width] duration-700 ease-out"
                      style={{
                        width: `${ms !== undefined ? Math.max((ms / slowest) * 100, 6) : 0}%`,
                        background: hueOf(computer),
                      }}
                    />
                  </span>
                </Link>
              </li>
            )
          })}
        </ul>
        {computers.length > SHOWN && (
          <p className="border-t border-hairline pt-3 text-hint text-muted-foreground">
            {plural(computers.length - SHOWN, "more computer")}
          </p>
        )}
      </PanelBody>
    </Panel>
  )
}

/** The fortnight's session time as one bar, each computer its share in its hue. */
function TimeBlock({
  overview,
  byId,
  hueOf,
  now,
}: {
  overview: RdOverview
  byId: Map<string, RdComputerView>
  hueOf: (computer: RdComputerView) => string
  now: number
}) {
  const totals = sessionTotals(overview.sessions, now).filter((total) => byId.has(total.computerId))
  const seconds = totals.reduce((sum, total) => sum + total.seconds, 0)
  const top = totals.slice(0, SHOWN)
  const rest = totals.slice(SHOWN).reduce((sum, total) => sum + total.seconds, 0)
  return (
    <Panel plain aria-label="Time on each">
      <PanelHeader
        title="Time on each"
        actions={
          <HeadFigure word="in 14 days">{seconds > 0 ? duration(seconds) : "0s"}</HeadFigure>
        }
      />
      <PanelBody className="space-y-3 pt-4">
        {top.length === 0 ? (
          <p className="py-2 text-body text-muted-foreground">
            No sessions in the last 14 days. Connect to a computer and its time shows here.
          </p>
        ) : (
          <>
            <ShareBar
              label="Session time by computer"
              capacity={seconds}
              rest={rest}
              parts={top.map((total) => {
                const computer = byId.get(total.computerId)!
                return {
                  key: total.computerId,
                  value: total.seconds,
                  color: hueOf(computer),
                  label: `${computer.name} ${duration(total.seconds)}`,
                }
              })}
              format={(value) => duration(value)}
            />
            <ul aria-label="Time on each computer" className="-mx-2">
              {top.map((total) => {
                const computer = byId.get(total.computerId)!
                return (
                  <li key={total.computerId}>
                    <Link
                      href={sessionHref(computer.id)}
                      aria-label={`Connect to ${computer.name}`}
                      className={rowClass}
                    >
                      <HueKey color={hueOf(computer)} />
                      <Glyph computer={computer} />
                      <span className="min-w-0 truncate font-medium">{computer.name}</span>
                      <span className="min-w-0 flex-1 truncate text-hint text-muted-foreground">
                        {plural(total.count, "session")}
                      </span>
                      <span className="numeric shrink-0 font-medium">
                        {duration(total.seconds)}
                      </span>
                    </Link>
                  </li>
                )
              })}
            </ul>
          </>
        )}
      </PanelBody>
    </Panel>
  )
}

/** The latest sessions, an open one first and breathing. */
function RecentBlock({
  overview,
  byId,
  hueOf,
  now,
}: {
  overview: RdOverview
  byId: Map<string, RdComputerView>
  hueOf: (computer: RdComputerView) => string
  now: number
}) {
  const sessions = overview.sessions.filter((session) => byId.has(session.computerId))
  const open = sessions.filter((session) => session.endedAt === 0).length
  return (
    <Panel plain aria-label="Recent sessions">
      <PanelHeader
        title="Recent sessions"
        actions={
          <HeadFigure word={open > 0 ? "open now" : "in 14 days"}>
            {open > 0 ? open : sessions.length}
          </HeadFigure>
        }
      />
      <PanelBody className="space-y-3 pt-4">
        {sessions.length === 0 ? (
          <p className="py-2 text-body text-muted-foreground">
            Every session is kept here: who opened it, on which computer, and for how long.
          </p>
        ) : (
          <ul aria-label="Recent sessions" className="-mx-2">
            {sessions.slice(0, SHOWN).map((session) => {
              const computer = byId.get(session.computerId)!
              const live = session.endedAt === 0
              return (
                <li key={session.id}>
                  <Link
                    href={sessionHref(computer.id)}
                    aria-label={`Connect to ${computer.name}`}
                    className={rowClass}
                  >
                    <HueKey color={hueOf(computer)} />
                    <Glyph computer={computer} />
                    <span className="min-w-0 truncate font-medium">{computer.name}</span>
                    <span className="min-w-0 flex-1 truncate text-hint text-muted-foreground">
                      {session.actor}
                    </span>
                    {live ? (
                      <span className="flex shrink-0 items-center gap-1.5 text-hint font-medium text-success">
                        <StatusDot tone="running" live className="size-1.5" />
                        now
                      </span>
                    ) : (
                      <span className="numeric shrink-0 text-hint text-muted-foreground">
                        {relativeTime(isoOf(session.startedAt))}
                      </span>
                    )}
                    <span className="numeric w-16 shrink-0 text-right font-medium">
                      {sessionLength(session, now)}
                    </span>
                  </Link>
                </li>
              )
            })}
          </ul>
        )}
      </PanelBody>
    </Panel>
  )
}
