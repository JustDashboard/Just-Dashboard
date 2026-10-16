"use client"

import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import Link from "next/link"
import { Archive, Plus, type Icon } from "@/components/icons"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import type { DbTopoEdge, DbTopoNode, DbTopology } from "@/lib/types"
import { cn } from "@/lib/utils"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { useNow } from "@/components/deploy/vocabulary"
import { WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { Status, StatusDot } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { describeEdge, edgeRank } from "@/components/database/fleet/fleet"
import {
  ConsumerMark,
  WiringLegend,
  beamTone,
  consumerKind,
  consumerVerdict,
  consumerWord,
} from "@/components/database/fleet/wiring"
import { EngineGlyph } from "@/components/database/kit"
import {
  Block,
  BlockLink,
  CardsSkeleton,
  CouldNotRead,
  staleOf,
} from "@/components/database/home/blocks"
import { copyTone } from "@/components/database/home/holdings"
import { read } from "@/components/database/home/read"
import type { DbBackups } from "@/components/database/home/types"
import { useDatabase } from "@/components/database/shell/database-context"
import { DATABASES_MAP_HREF } from "@/components/database/shell/routes"

/** One thing the database holds, as the right lane draws it: a schema, a keyspace, a collection. */
export type Holding = {
  key: string
  /** The name, as the reader knows it. */
  name: string
  /** What kind of thing it is, in the eyebrow. */
  kind: string
  /** Its size and what is in it. */
  detail?: string
  /** The name's own hue (`nameHue`), so a schema is one colour here and in the lists below. */
  color?: string
  glyph: Icon
  href?: string
}

/** How many readers and holdings the lanes draw before the rest are counted. */
const SHOWN = 5

/**
 * How the database is used, drawn as three lanes in the wiring vocabulary the
 * rest of the product speaks (`deploy/wire`, the fleet's map, a project's
 * Runtime): what reads it on the left, the database in the middle, and on
 * the right what it holds and where its copies go.
 *
 * A reader's wire pulses while it holds sessions — quicker the more it holds
 * — is still where it is linked and idle, dashed where it only could reach
 * the database (the same stack or network), amber when the link has gone
 * stale and red when it broke. What the database holds is wired still: the
 * record says how big each part is, not which one a session is reading. The
 * copy is wired in the colour of how recent the newest dump is — green
 * today, amber past a week, dashed where there is none — and pulses while a
 * dump is being taken.
 *
 * On the page's own ground with no dot grid, as every picture is. Three
 * lanes from `lg`; below it the lanes stack, the wires are not drawn, and
 * each node says in words how it is wired.
 */
export function HomePicture({
  holdings,
  holdingsWord,
  sessions,
  size,
  backups,
}: {
  /** What the database holds, largest first; undefined while it is read. */
  holdings?: Holding[]
  /** The lane's name: "Schemas", "Keyspaces", "Collections". */
  holdingsWord: string
  /** Sessions open now, where the server says. */
  sessions?: number
  /** What the database stores on disk, where the server says. */
  size?: number
  /** The dumps, for an engine the dashboard can dump. */
  backups?: PollState<DbBackups>
}) {
  const { id, conn, engine, status, summary, href } = useDatabase()
  const topology = usePoll(
    (signal) =>
      read<DbTopology>(
        `/databases/${id}/consumers`,
        (answer) => Array.isArray(answer.nodes) && Array.isArray(answer.edges),
        undefined,
        signal,
      ),
    45_000,
    [id],
  )
  const readers = useMemo(() => readersOf(topology.data, id), [topology.data, id])
  const running = status.state === "running"

  const container = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  const shownReaders = readers.slice(0, SHOWN)
  const shownHoldings = (holdings ?? []).slice(0, SHOWN)
  const copies = backups?.data
  const newest = copies?.files[0]
  const dumping = Boolean(copies?.job)

  // One ref per node, remade only when the set of nodes changes, so a poll
  // that changes nothing does not redraw every wire.
  const ids = [
    "db",
    ...shownReaders.map(({ node }) => `r:${node.id}`),
    ...shownHoldings.map((holding) => `h:${holding.key}`),
    backups ? "copies" : "",
  ].join("\n")
  const refs = useMemo(() => {
    const map = new Map<string, RefObject<HTMLSpanElement | null>>()
    for (const key of ids.split("\n")) if (key) map.set(key, createRef<HTMLSpanElement>())
    return map
  }, [ids])
  const refFor = (key: string) => refs.get(key)

  const related = (key: string) => focus === null || focus === key || focus === "db" || key === "db"
  const watch = (key: string) => ({
    onPointerEnter: () => setFocus(key),
    onPointerLeave: () => setFocus((held) => (held === key ? null : held)),
    onFocus: () => setFocus(key),
    onBlur: () => setFocus((held) => (held === key ? null : held)),
  })
  const lit = (key: string) => focus === null || focus === key || focus === "db"

  // The dumps' age is read against a clock that moves, so a page left open
  // overnight does not keep calling yesterday's dump today's.
  const now = useNow(60_000, Boolean(newest))
  const tone = copyTone(newest?.takenAt, now)

  return (
    <Block
      title="How it is used"
      stale={staleOf(topology)}
      actions={
        <>
          <WiringLegend className="mr-2 max-xl:hidden" />
          <BlockLink href={DATABASES_MAP_HREF}>Map</BlockLink>
        </>
      }
    >
      {topology.error && !topology.data ? (
        <CouldNotRead what="what uses it" error={topology.error} onRetry={topology.refresh} />
      ) : !topology.data ? (
        <CardsSkeleton count={2} />
      ) : (
        <div className="relative animate-rise py-4">
          <div ref={container} className="relative">
            {shownReaders.map(({ node, edge }, index) => {
              const from = refFor(`r:${node.id}`)
              const to = refFor("db")
              if (!from || !to) return null
              return (
                <AnimatedBeam
                  key={node.id}
                  containerRef={container}
                  fromRef={from}
                  toRef={to}
                  shape="s"
                  startXOffset={26}
                  endXOffset={-34}
                  // What a session reads leaves the database for the reader.
                  reverse
                  still={!running || (edge.sessions === 0 && edge.status !== "connected")}
                  dashed={edge.via.every((via) => via === "stack" || via === "network")}
                  tone={beamTone(edge)}
                  duration={pulseFor(edge.sessions)}
                  delay={(index % 5) * 0.35}
                  className={cn(
                    "transition-opacity max-lg:hidden",
                    !lit(`r:${node.id}`) && "opacity-15",
                  )}
                />
              )
            })}
            {shownHoldings.map((holding) => {
              const from = refFor("db")
              const to = refFor(`h:${holding.key}`)
              if (!from || !to) return null
              return (
                <AnimatedBeam
                  key={holding.key}
                  containerRef={container}
                  fromRef={from}
                  toRef={to}
                  shape="s"
                  startXOffset={34}
                  endXOffset={-26}
                  still
                  className={cn(
                    "transition-opacity max-lg:hidden",
                    !lit(`h:${holding.key}`) && "opacity-15",
                  )}
                />
              )
            })}
            {backups && refFor("copies") && refFor("db") && (
              <AnimatedBeam
                containerRef={container}
                fromRef={refFor("db") as RefObject<HTMLSpanElement | null>}
                toRef={refFor("copies") as RefObject<HTMLSpanElement | null>}
                shape="s"
                startXOffset={34}
                endXOffset={-26}
                still={!dumping}
                dashed={!newest && !dumping}
                tone={dumping ? "default" : tone}
                duration={1.6}
                className={cn("transition-opacity max-lg:hidden", !lit("copies") && "opacity-15")}
              />
            )}

            <div className="relative grid gap-y-8 lg:grid-cols-[minmax(0,1fr)_clamp(3rem,7vw,7rem)_minmax(0,11rem)_clamp(3rem,7vw,7rem)_minmax(0,1fr)] lg:items-center">
              <Lane title="Read by" count={readers.length} className="lg:text-right">
                {readers.length === 0 ? (
                  <li>
                    <WireNode
                      align="end"
                      mark={
                        <WirePlaceholder size="md">
                          <Plus />
                        </WirePlaceholder>
                      }
                      title={<span className="text-muted-foreground">Nothing reads it yet</span>}
                      hint={`Link it to a deployment, or hand an application its address from Connect`}
                    />
                  </li>
                ) : (
                  shownReaders.map(({ node, edge }) => {
                    const key = `r:${node.id}`
                    const wrong = edgeRank(edge.status) >= 2
                    return (
                      <li
                        key={node.id}
                        {...watch(key)}
                        className={cn("min-w-0 transition-opacity", !related(key) && "opacity-40")}
                      >
                        <WireNode
                          align="end"
                          mark={
                            <span ref={refFor(key)} className="flex">
                              <ConsumerMark node={node} worst={edge.status} size="md" />
                            </span>
                          }
                          eyebrow={eyebrowOf(node)}
                          title={
                            node.href ? (
                              <Link
                                href={node.href}
                                aria-label={`Open ${node.name}`}
                                className="block truncate rounded-sm focus-ring hover:underline"
                              >
                                {node.name}
                              </Link>
                            ) : (
                              <span className="block truncate">{node.name}</span>
                            )
                          }
                          hint={
                            <span className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 lg:justify-end">
                              {wrong && (
                                <Status
                                  verdict={consumerVerdict(edge.status, node.status)}
                                  label={consumerWord(edge.status, node.status)}
                                />
                              )}
                              <span className="min-w-0">{describeEdge(edge)}</span>
                            </span>
                          }
                        />
                      </li>
                    )
                  })
                )}
                {readers.length > SHOWN && (
                  <li className="text-hint text-muted-foreground lg:pr-[3.75rem]">
                    and {plural(readers.length - SHOWN, "more")} on the{" "}
                    <Link href={DATABASES_MAP_HREF} className="rounded-sm underline focus-ring">
                      map
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
                      {[
                        sessions !== undefined ? plural(sessions, "session") : undefined,
                        size !== undefined && size > 0 ? bytes(size) : undefined,
                      ]
                        .filter(Boolean)
                        .join(" · ") || (running ? "answering" : "not answering")}
                    </span>
                  }
                />
              </div>

              <div aria-hidden className="hidden lg:block" />

              <div className="flex min-w-0 flex-col gap-8">
                <Lane title={holdingsWord} count={holdings?.length}>
                  {holdings === undefined ? (
                    <li className="text-hint text-muted-foreground">Reading what it holds…</li>
                  ) : holdings.length === 0 ? (
                    <li className="text-hint text-muted-foreground">
                      Nothing yet: it holds no {holdingsWord.toLowerCase()}.
                    </li>
                  ) : (
                    shownHoldings.map((holding) => {
                      const key = `h:${holding.key}`
                      const Glyph = holding.glyph
                      return (
                        <li
                          key={holding.key}
                          {...watch(key)}
                          className={cn(
                            "min-w-0 transition-opacity",
                            !related(key) && "opacity-40",
                          )}
                        >
                          <WireNode
                            mark={
                              <span ref={refFor(key)} className="flex">
                                <WireMark tone="logo" size="md">
                                  <Glyph aria-hidden style={{ color: holding.color }} />
                                </WireMark>
                              </span>
                            }
                            eyebrow={holding.kind}
                            title={
                              holding.href ? (
                                <Link
                                  href={holding.href}
                                  aria-label={`Open ${holding.name}`}
                                  className="block truncate rounded-sm font-mono focus-ring hover:underline"
                                  style={{ color: holding.color }}
                                >
                                  {holding.name}
                                </Link>
                              ) : (
                                <span
                                  className="block truncate font-mono"
                                  style={{ color: holding.color }}
                                >
                                  {holding.name}
                                </span>
                              )
                            }
                            hint={
                              holding.detail && <span className="numeric">{holding.detail}</span>
                            }
                          />
                        </li>
                      )
                    })
                  )}
                  {holdings && holdings.length > SHOWN && (
                    <li className="text-hint text-muted-foreground lg:pl-[3.75rem]">
                      and {plural(holdings.length - SHOWN, "more")}
                    </li>
                  )}
                </Lane>

                {backups && (
                  <Lane title="Copied to">
                    <li {...watch("copies")} className="min-w-0">
                      <WireNode
                        mark={
                          <span ref={refFor("copies")} className="flex">
                            {newest || dumping ? (
                              <WireMark tone={tone === "warning" ? "warning" : "logo"} size="md">
                                <Archive
                                  aria-hidden
                                  className={cn(tone === "success" && "text-success")}
                                />
                              </WireMark>
                            ) : (
                              <WirePlaceholder size="md">
                                <Archive />
                              </WirePlaceholder>
                            )}
                          </span>
                        }
                        eyebrow="Dumps"
                        title={
                          <Link
                            href={href("backups")}
                            className="block truncate rounded-sm focus-ring hover:underline"
                          >
                            {dumping
                              ? `${copies?.job?.title ?? "A dump"}…`
                              : newest
                                ? plural(copies?.files.length ?? 0, "dump")
                                : "No dump yet"}
                          </Link>
                        }
                        hint={
                          newest ? (
                            <span title={timestamp(newest.takenAt)} className="numeric">
                              newest {relativeTime(newest.takenAt)} · {bytes(newest.size)}
                            </span>
                          ) : backups.error ? (
                            "could not be read"
                          ) : (
                            "Back it up from the Backups block below"
                          )
                        }
                      />
                    </li>
                  </Lane>
                )}
              </div>
            </div>
          </div>
        </div>
      )}
    </Block>
  )
}

/** A reader with how it reaches the database, worst link first, then the busiest. */
type Reader = { node: DbTopoNode; edge: DbTopoEdge }

function readersOf(data: DbTopology | undefined, id: number): Reader[] {
  if (!data) return []
  const nodes = new Map(data.nodes.map((node) => [node.id, node]))
  return data.edges
    .flatMap((edge) => {
      const other = nodes.get(edge.from)?.connId === id ? edge.to : edge.from
      const node = nodes.get(other)
      return node && node.connId !== id ? [{ node, edge }] : []
    })
    .sort(
      (a, b) =>
        edgeRank(b.edge.status) - edgeRank(a.edge.status) ||
        b.edge.sessions - a.edge.sessions ||
        a.node.name.localeCompare(b.node.name),
    )
}

/**
 * What a reader is, and the environment or role the map knows it by — but
 * not a second time: the map names a container's detail "container", and
 * this server's "processes on the machine".
 */
function eyebrowOf(node: DbTopoNode): string {
  const kind = consumerKind(node)
  const detail = node.kind === "host" ? undefined : node.detail
  return detail && detail.toLowerCase() !== kind.toLowerCase() ? `${kind} · ${detail}` : kind
}

/** A busier link pulses quicker: three seconds idle, a second and a half at a dozen sessions. */
function pulseFor(sessions: number): number {
  if (sessions <= 0) return 3
  return Math.max(1.2, 3 - Math.log2(sessions + 1) * 0.5)
}

function Lane({
  title,
  count,
  className,
  children,
}: {
  title: string
  count?: number
  className?: string
  children: React.ReactNode
}) {
  return (
    <div className="min-w-0">
      <p className={cn("eyebrow mb-4", className)}>
        {title}
        {count !== undefined && count > 0 && (
          <span className="numeric ml-1.5 font-medium tracking-normal text-muted-foreground/70">
            {count}
          </span>
        )}
      </p>
      <ol className="flex flex-col gap-5" aria-label={title}>
        {children}
      </ol>
    </div>
  )
}
