"use client"

import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import Link from "next/link"
import { Clock, LockClosed, Pause, Play, type Icon } from "@/components/icons"
import { duration, plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { PollState } from "@/hooks/use-poll"
import { WireMark, WireNode } from "@/components/deploy/wire"
import { StatusDot } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { LegendLine } from "@/components/database/fleet/wiring"
import { EngineGlyph } from "@/components/database/kit"
import { Block, CardsSkeleton, staleOf } from "@/components/database/home/blocks"
import { Lane, pulseFor } from "@/components/database/home/picture"
import { DbClientMark } from "@/components/database/client-mark"
import { clientOf, type KnownClient } from "@/components/database/clients"
import { useKnownClients } from "@/components/database/use-known-clients"
import {
  clientGroups,
  stateSeconds,
  statusOf,
  type ClientGroup,
} from "@/components/database/ops/performance-activity"
import type {
  DbActivity,
  DbActivityResponse,
  DbServerStats,
  DbSessionStatus,
} from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"

/** How many clients the left lane draws before the rest are counted. */
const SHOWN = 6

type Doing = Exclude<DbSessionStatus, "background">

/** The right lane: what the sessions are doing, in the order the list reads them. */
const DOING: readonly { id: Doing; label: string; glyph: Icon; longest: string }[] = [
  { id: "active", label: "Running a statement", glyph: Play, longest: "longest" },
  { id: "blocked", label: "Waiting on a lock", glyph: LockClosed, longest: "waiting up to" },
  {
    id: "idle_in_transaction",
    label: "Holding a transaction",
    glyph: Clock,
    longest: "open up to",
  },
  { id: "idle", label: "Connected, idle", glyph: Pause, longest: "idle up to" },
]

/**
 * Who is on the server right now and what they are doing, drawn in the
 * wiring vocabulary of the database's home (`HomePicture`): each program
 * connected on the left as itself — Grafana as Grafana, a deployment as the
 * product it runs, `pg_dump` as the elephant, this dashboard as its J — the
 * server in the middle, and on the right the four things a session can be
 * doing, each counted.
 *
 * It is the session list read at a glance, so it moves with it, every five
 * seconds. A client's wire pulses while one of its sessions runs a statement,
 * quicker the more do; it is amber while one holds a transaction open too
 * long or has run too long, red while one waits on a lock, and still while
 * all of them are idle. On the right a state's wire pulses while a session is
 * in it, in the state's own tone, and is dashed where none is — so a server
 * with nothing waiting says so with a line, not a zero to be read. Pressing
 * a state opens the sessions in it.
 *
 * The dashboard's own pool, which stood under the charts as four figures, is
 * the J's line: how many of its connections are open and how many it may hold.
 */
export function NowPicture({
  activity,
  pool,
  ceiling,
}: {
  activity: PollState<DbActivityResponse>
  pool?: DbServerStats["pool"]
  /** The most sessions the server accepts, where it says. */
  ceiling?: number
}) {
  const { conn, engine, status, summary, href } = useDatabase()
  const known = useKnownClients()
  const sessions = activity.data?.supported ? activity.data.sessions : undefined
  const groups = useMemo(() => clientGroups(sessions ?? []), [sessions])
  const doing = useMemo(() => doingOf(sessions ?? []), [sessions])
  const running = status.state === "running"
  const open = sessions?.length ?? 0

  const container = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  // The dashboard's own line always stands, last: it carries the pool's
  // reading, which has nowhere else on the page to go.
  const own = groups.find((group) => group.self)
  const shown = own
    ? [...groups.filter((group) => !group.self).slice(0, SHOWN - 1), own]
    : groups.slice(0, SHOWN)

  // One ref per node, remade only when the set of nodes changes, so a poll
  // that changes nothing does not redraw every wire.
  const ids = [
    "db",
    ...shown.map((group) => `c:${group.key}`),
    ...DOING.map((s) => `s:${s.id}`),
  ].join("\n")
  const refs = useMemo(() => {
    const map = new Map<string, RefObject<HTMLSpanElement | null>>()
    for (const key of ids.split("\n")) map.set(key, createRef<HTMLSpanElement>())
    return map
  }, [ids])
  const refFor = (key: string) => refs.get(key)

  // A client lights the states its sessions are in, and a state the clients
  // with a session in it, so pointing at either end reads the whole line.
  const touches = (a: string, b: string) => {
    const [client, state] = a.startsWith("c:") ? [a, b] : [b, a]
    if (!client.startsWith("c:") || !state.startsWith("s:")) return false
    const group = groups.find((entry) => `c:${entry.key}` === client)
    return Boolean(group && group.counts[state.slice(2) as Doing] > 0)
  }
  const lit = (key: string) =>
    focus === null || focus === key || focus === "db" || key === "db" || touches(focus, key)
  const watch = (key: string) => ({
    onPointerEnter: () => setFocus(key),
    onPointerLeave: () => setFocus((held) => (held === key ? null : held)),
    onFocus: () => setFocus(key),
    onBlur: () => setFocus((held) => (held === key ? null : held)),
  })

  if (activity.error && !activity.data) return null
  return (
    <Block
      title="Right now"
      stale={staleOf(activity)}
      actions={<NowLegend className="max-xl:hidden" />}
    >
      {!activity.data ? (
        <CardsSkeleton count={2} />
      ) : (
        <div className="relative animate-rise py-4">
          <div
            aria-hidden
            className="wire-grid pointer-events-none absolute -inset-x-4 inset-y-0"
          />
          <div ref={container} className="relative">
            {shown.map((group, index) => {
              const from = refFor(`c:${group.key}`)
              const to = refFor("db")
              if (!from || !to) return null
              const working = group.counts.active + group.counts.blocked
              const busy = working + group.counts.idle_in_transaction
              return (
                <AnimatedBeam
                  key={group.key}
                  containerRef={container}
                  fromRef={from}
                  toRef={to}
                  shape="s"
                  startXOffset={24}
                  endXOffset={-34}
                  reverse
                  still={!running || busy === 0}
                  tone={toneOf(group)}
                  duration={pulseFor(working)}
                  delay={(index % 5) * 0.35}
                  className={cn(
                    "transition-opacity max-lg:hidden",
                    !lit(`c:${group.key}`) && "opacity-15",
                  )}
                />
              )
            })}
            {DOING.map((state, index) => {
              const from = refFor("db")
              const to = refFor(`s:${state.id}`)
              if (!from || !to) return null
              const count = doing[state.id].count
              return (
                <AnimatedBeam
                  key={state.id}
                  containerRef={container}
                  fromRef={from}
                  toRef={to}
                  shape="s"
                  startXOffset={34}
                  endXOffset={-24}
                  still={!running || count === 0 || state.id === "idle"}
                  dashed={count === 0}
                  tone={
                    count === 0
                      ? "default"
                      : state.id === "blocked"
                        ? "danger"
                        : state.id === "idle_in_transaction"
                          ? "warning"
                          : "default"
                  }
                  duration={pulseFor(count)}
                  delay={index * 0.4}
                  className={cn(
                    "transition-opacity max-lg:hidden",
                    !lit(`s:${state.id}`) && "opacity-15",
                  )}
                />
              )
            })}

            <div className="relative grid gap-y-8 lg:grid-cols-[minmax(0,1fr)_clamp(3rem,7vw,7rem)_minmax(0,11rem)_clamp(3rem,7vw,7rem)_minmax(0,1fr)] lg:items-center">
              <Lane title="Connected" count={groups.length} className="lg:text-right">
                {groups.length === 0 ? (
                  <li className="text-hint text-muted-foreground lg:text-right">
                    No session but the server&apos;s own is open.
                  </li>
                ) : (
                  shown.map((group) => {
                    const key = `c:${group.key}`
                    const client = group.self
                      ? {}
                      : clientOf(group.named === "application" ? group.name : undefined, known)
                    return (
                      <li
                        key={group.key}
                        {...watch(key)}
                        className={cn("min-w-0 transition-opacity", !lit(key) && "opacity-40")}
                      >
                        <WireNode
                          align="end"
                          mark={
                            <span ref={refFor(key)} className="flex">
                              <DbClientMark
                                product={client.product}
                                projectId={client.projectId}
                                self={group.self}
                                size="md"
                                tone={markTone(group)}
                              />
                            </span>
                          }
                          eyebrow={eyebrowOf(group, client.known)}
                          title={
                            client.known?.href ? (
                              <Link
                                href={client.known.href}
                                aria-label={`Open ${client.known.name}`}
                                className="block truncate rounded-sm focus-ring hover:underline"
                              >
                                {group.name}
                              </Link>
                            ) : (
                              <span className="block truncate">{group.name}</span>
                            )
                          }
                          hint={<ClientWords group={group} pool={group.self ? pool : undefined} />}
                        />
                      </li>
                    )
                  })
                )}
                {groups.length > SHOWN && (
                  <li className="text-hint text-muted-foreground lg:pr-[3.75rem] lg:text-right">
                    and{" "}
                    <Link
                      href={href("performance", { view: "sessions" })}
                      className="rounded-sm underline focus-ring"
                    >
                      {(groups.length - SHOWN).toLocaleString()} more
                    </Link>
                  </li>
                )}
              </Lane>

              <div aria-hidden className="hidden lg:block" />

              <div
                {...watch("db")}
                className="flex min-w-0 lg:flex-col lg:items-center lg:self-center lg:pb-12"
              >
                <WireNode
                  align="center"
                  mark={
                    <span ref={refFor("db")} className="relative flex">
                      <WireMark tone="logo" size="lg">
                        <EngineGlyph engine={engine} />
                      </WireMark>
                      <span className="absolute -right-0.5 -bottom-0.5 flex size-4 items-center justify-center rounded-full bg-background">
                        <StatusDot
                          state={
                            running ? "running" : status.state === "broken" ? "failed" : "stopped"
                          }
                        />
                      </span>
                    </span>
                  }
                  eyebrow={summary?.version ?? engine.label}
                  title={<span className="block truncate">{conn.name}</span>}
                  hint={
                    <span className="numeric">
                      {ceiling
                        ? `${open.toLocaleString()} of ${ceiling.toLocaleString()} sessions`
                        : plural(open, "session")}
                    </span>
                  }
                />
              </div>

              <div aria-hidden className="hidden lg:block" />

              <Lane title="Doing">
                {DOING.map((state) => {
                  const key = `s:${state.id}`
                  const { count, longest } = doing[state.id]
                  const Glyph = state.glyph
                  const tone =
                    count === 0
                      ? undefined
                      : state.id === "active"
                        ? "success"
                        : state.id === "blocked"
                          ? "danger"
                          : state.id === "idle_in_transaction"
                            ? "warning"
                            : undefined
                  return (
                    <li
                      key={state.id}
                      {...watch(key)}
                      className={cn(
                        "min-w-0 transition-opacity",
                        !lit(key) && "opacity-40",
                        count === 0 && lit(key) && "opacity-70",
                      )}
                    >
                      <WireNode
                        mark={
                          <span ref={refFor(key)} className="flex">
                            <WireMark tone={tone ?? "logo"} size="sm">
                              <Glyph aria-hidden />
                            </WireMark>
                          </span>
                        }
                        title={
                          <Link
                            href={href("performance", { view: "sessions", state: state.id })}
                            aria-label={`${plural(count, "session")} ${state.label.toLowerCase()}: open them`}
                            className="flex min-w-0 items-baseline gap-2 rounded-sm focus-ring hover:underline"
                          >
                            <span className="numeric font-semibold">{count.toLocaleString()}</span>
                            <span className="truncate font-normal text-muted-foreground">
                              {state.label}
                            </span>
                          </Link>
                        }
                        hint={
                          longest !== undefined && longest >= 1 ? (
                            <span className="numeric">
                              {state.longest} {duration(longest)}
                            </span>
                          ) : undefined
                        }
                      />
                    </li>
                  )
                })}
              </Lane>
            </div>
          </div>
        </div>
      )}
    </Block>
  )
}

/** How many sessions are in each state, and the longest any has been in it. */
function doingOf(sessions: readonly DbActivity[]) {
  const doing = Object.fromEntries(
    DOING.map((state) => [state.id, { count: 0, longest: undefined as number | undefined }]),
  ) as Record<Doing, { count: number; longest?: number }>
  for (const session of sessions) {
    const status = statusOf(session)
    if (status === "background" || session.self) continue
    const entry = doing[status]
    entry.count += 1
    const seconds = stateSeconds(session)
    if (seconds !== undefined) entry.longest = Math.max(entry.longest ?? 0, seconds)
  }
  return doing
}

function toneOf(group: ClientGroup): "default" | "warning" | "danger" {
  if (group.worst === "blocked" || group.worst === "long-blocked") return "danger"
  if (group.worst) return "warning"
  return "default"
}

function markTone(group: ClientGroup): "warning" | "danger" | undefined {
  const tone = toneOf(group)
  return tone === "default" ? undefined : tone
}

/** What a client is, the way the map knows it, or the kind of name it gave. */
function eyebrowOf(group: ClientGroup, known: KnownClient | undefined): string {
  if (group.self) return "Its own connections"
  if (known) return known.kind === "deployment" ? "Deployment" : "Container"
  return group.named === "account" ? "Account" : "Application"
}

/** A client's sessions in words: how many, and what the ones that are not idle are doing. */
function ClientWords({ group, pool }: { group: ClientGroup; pool?: DbServerStats["pool"] }) {
  const parts = [
    pool ? `${pool.open} of ${pool.maxOpen} pooled open` : plural(group.sessions, "session"),
    pool && pool.waitCount > 0 ? `waited for one ${plural(pool.waitCount, "time")}` : undefined,
    group.counts.active > 0 ? `${group.counts.active} running` : undefined,
    group.counts.blocked > 0 ? `${group.counts.blocked} waiting on a lock` : undefined,
    group.counts.idle_in_transaction > 0
      ? `${group.counts.idle_in_transaction} in a transaction`
      : undefined,
  ].filter(Boolean)
  return (
    <span
      className={cn(
        "numeric",
        group.worst === "blocked" || group.worst === "long-blocked"
          ? "text-destructive"
          : group.worst && "text-warning",
      )}
    >
      {parts.join(" · ")}
    </span>
  )
}

function NowLegend({ className }: { className?: string }) {
  return (
    <ul
      aria-label="How to read the picture"
      className={cn(
        "flex flex-wrap items-center gap-x-5 gap-y-1.5 text-hint text-muted-foreground",
        className,
      )}
    >
      <LegendLine kind="live">running</LegendLine>
      <LegendLine kind="warning">holding a transaction</LegendLine>
      <LegendLine kind="danger">waiting on a lock</LegendLine>
      <LegendLine kind="still">idle</LegendLine>
      <LegendLine kind="dashed">none</LegendLine>
    </ul>
  )
}
