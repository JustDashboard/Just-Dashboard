"use client"

import { createRef, useMemo, useRef, type RefObject } from "react"
import { Archive, Servers } from "@/components/icons"
import { bytes, duration, plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import { WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { StatusDot } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { LegendLine } from "@/components/database/fleet/wiring"
import { EngineGlyph } from "@/components/database/kit"
import { Lane } from "@/components/database/home/picture"
import type {
  DbReplica,
  DbReplicaSource,
  DbReplication,
  DbReplicationSlot,
} from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"

/** How far behind a replica may fall before its line turns amber, in seconds. */
const LAG_WARNING = 60

const ROLE: Record<DbReplication["role"], string> = {
  primary: "Primary",
  replica: "Replica",
  standalone: "Standalone",
  "": "Server",
}

/**
 * Where this server's data goes, drawn in the pictures' wiring vocabulary:
 * the server it follows on the left, this one in the middle as its engine,
 * and on the right the replicas that follow it over the slots that hold its
 * log for a reader. It takes the place of three figures — the role, the
 * replicas, the slots — that each said one word of this.
 *
 * A wire pulses while it carries the log: a replica streaming, a source
 * whose threads both run. It is amber where a replica has fallen more than a
 * minute behind or is not streaming, and where a slot has no reader — the
 * slot that keeps the log from being recycled until the disk fills, which is
 * the one thing on this view to act on. A slot being read is still. Below
 * `lg` the lanes stack and each node says in words how it is wired.
 */
export function ReplicationPicture({ data }: { data: DbReplication }) {
  const { conn, engine, status, summary } = useDatabase()
  const container = useRef<HTMLDivElement>(null)
  const running = status.state === "running"

  const ids = [
    "db",
    ...data.sources.map((_, index) => `f:${index}`),
    ...data.replicas.map((_, index) => `r:${index}`),
    ...data.slots.map((slot) => `s:${slot.name}`),
  ].join("\n")
  const refs = useMemo(() => {
    const map = new Map<string, RefObject<HTMLSpanElement | null>>()
    for (const key of ids.split("\n")) map.set(key, createRef<HTMLSpanElement>())
    return map
  }, [ids])
  const refFor = (key: string) => refs.get(key)
  const beam = (from: string, to: string) => {
    const a = refFor(from)
    const b = refFor(to)
    return a && b ? { fromRef: a, toRef: b } : undefined
  }

  const idle = data.slots.filter((slot) => !slot.active).length
  return (
    <section aria-label="Where its data goes" className="relative animate-rise py-4">
      <div aria-hidden className="wire-grid pointer-events-none absolute -inset-x-4 inset-y-0" />
      <ReplicationLegend className="relative mb-6 justify-end max-xl:hidden" />
      <div ref={container} className="relative">
        {data.sources.map((source, index) => {
          const ends = beam(`f:${index}`, "db")
          if (!ends) return null
          const flowing = source.ioRunning && source.sqlRunning
          return (
            <AnimatedBeam
              key={`f:${index}`}
              containerRef={container}
              {...ends}
              shape="s"
              startXOffset={26}
              endXOffset={-34}
              still={!running || !flowing}
              tone={sourceTone(source)}
              className="max-lg:hidden"
            />
          )
        })}
        {data.replicas.map((replica, index) => {
          const ends = beam("db", `r:${index}`)
          if (!ends) return null
          return (
            <AnimatedBeam
              key={`r:${index}`}
              containerRef={container}
              {...ends}
              shape="s"
              startXOffset={34}
              endXOffset={-26}
              still={!running || replica.state !== "streaming"}
              tone={replicaTone(replica)}
              duration={2}
              delay={index * 0.4}
              className="max-lg:hidden"
            />
          )
        })}
        {data.slots.map((slot) => {
          const ends = beam("db", `s:${slot.name}`)
          if (!ends) return null
          return (
            <AnimatedBeam
              key={slot.name}
              containerRef={container}
              {...ends}
              shape="s"
              startXOffset={34}
              endXOffset={-26}
              still
              dashed={!slot.active}
              tone={slot.active ? "default" : "warning"}
              className="max-lg:hidden"
            />
          )
        })}

        <div className="relative grid gap-y-8 lg:grid-cols-[minmax(0,1fr)_clamp(3rem,7vw,7rem)_minmax(0,11rem)_clamp(3rem,7vw,7rem)_minmax(0,1fr)] lg:items-center">
          <Lane title="Follows" count={data.sources.length} className="lg:text-right">
            {data.sources.length === 0 ? (
              <li className="text-hint text-muted-foreground lg:pr-[3.75rem] lg:text-right">
                Nothing: it is the source of its own data.
              </li>
            ) : (
              data.sources.map((source, index) => (
                <li key={`f:${index}`} className="min-w-0">
                  <WireNode
                    align="end"
                    mark={
                      <span ref={refFor(`f:${index}`)} className="flex">
                        <WireMark
                          tone={sourceTone(source) === "default" ? "logo" : "warning"}
                          size="md"
                        >
                          <EngineGlyph engine={engine} />
                        </WireMark>
                      </span>
                    }
                    eyebrow={source.channel ? `Channel · ${source.channel}` : "Source"}
                    title={
                      <span className="block truncate font-mono">
                        {source.host
                          ? `${source.host}${source.port ? `:${source.port}` : ""}`
                          : "another server"}
                      </span>
                    }
                    hint={
                      <span
                        className={cn(
                          "numeric",
                          sourceTone(source) !== "default" && "text-warning",
                        )}
                      >
                        {source.ioRunning && source.sqlRunning
                          ? source.lagSeconds > 0
                            ? `${duration(source.lagSeconds)} behind`
                            : "caught up"
                          : source.lastError || "its threads are not both running"}
                      </span>
                    }
                  />
                </li>
              ))
            )}
          </Lane>

          <div aria-hidden className="hidden lg:block" />

          <div className="flex min-w-0 lg:flex-col lg:items-center lg:self-center lg:pb-12">
            <WireNode
              align="center"
              mark={
                <span ref={refFor("db")} className="relative flex">
                  <WireMark tone="logo" size="lg">
                    <EngineGlyph engine={engine} />
                  </WireMark>
                  <span className="absolute -right-0.5 -bottom-0.5 flex size-4 items-center justify-center rounded-full bg-background">
                    <StatusDot state={running ? "running" : "stopped"} />
                  </span>
                </span>
              }
              eyebrow={`${ROLE[data.role] ?? data.role} · ${summary?.version ?? engine.label}`}
              title={<span className="block truncate">{conn.name}</span>}
              hint={
                <span className="numeric">
                  {[
                    plural(data.replicas.length, "replica"),
                    data.slots.length > 0 ? plural(data.slots.length, "slot") : undefined,
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                </span>
              }
            />
          </div>

          <div aria-hidden className="hidden lg:block" />

          <div className="flex min-w-0 flex-col gap-8">
            <Lane title="Followed by" count={data.replicas.length}>
              {data.replicas.length === 0 ? (
                <li className="min-w-0">
                  <WireNode
                    mark={
                      <WirePlaceholder size="md">
                        <Servers />
                      </WirePlaceholder>
                    }
                    title={<span className="text-muted-foreground">No replica</span>}
                    hint="Nothing is connected to follow it"
                  />
                </li>
              ) : (
                data.replicas.map((replica, index) => (
                  <li key={`r:${index}`} className="min-w-0">
                    <WireNode
                      mark={
                        <span ref={refFor(`r:${index}`)} className="flex">
                          <WireMark
                            tone={replicaTone(replica) === "default" ? "logo" : "warning"}
                            size="md"
                          >
                            <EngineGlyph engine={engine} />
                          </WireMark>
                        </span>
                      }
                      eyebrow={[replica.state || "unknown", replica.syncState]
                        .filter(Boolean)
                        .join(" · ")}
                      title={
                        <span className="block truncate font-mono">
                          {replica.name || replica.client || "a replica"}
                        </span>
                      }
                      hint={<ReplicaLag replica={replica} />}
                    />
                  </li>
                ))
              )}
            </Lane>

            {data.slots.length > 0 && (
              <Lane title="Log kept for" count={data.slots.length}>
                {data.slots.map((slot) => (
                  <li key={slot.name} className="min-w-0">
                    <WireNode
                      mark={
                        <span ref={refFor(`s:${slot.name}`)} className="flex">
                          {slot.active ? (
                            <WireMark tone="logo" size="sm">
                              <Archive aria-hidden />
                            </WireMark>
                          ) : (
                            <WireMark tone="warning" size="sm">
                              <Archive aria-hidden />
                            </WireMark>
                          )}
                        </span>
                      }
                      eyebrow={[slot.type, slot.plugin].filter(Boolean).join(" · ") || "Slot"}
                      title={<span className="block truncate font-mono">{slot.name}</span>}
                      hint={<SlotWords slot={slot} />}
                    />
                  </li>
                ))}
              </Lane>
            )}
          </div>
        </div>
      </div>
      {idle > 0 && (
        <p className="sr-only">
          {plural(idle, "slot")} with no reader {idle === 1 ? "holds" : "hold"} the log back.
        </p>
      )}
    </section>
  )
}

function replicaTone(replica: DbReplica): "default" | "warning" {
  if (replica.state && replica.state !== "streaming") return "warning"
  return replica.lagSeconds >= LAG_WARNING ? "warning" : "default"
}

function sourceTone(source: DbReplicaSource): "default" | "warning" {
  if (!source.ioRunning || !source.sqlRunning || source.lastError) return "warning"
  return source.lagSeconds >= LAG_WARNING ? "warning" : "default"
}

function ReplicaLag({ replica }: { replica: DbReplica }) {
  const parts = [
    replica.lagSeconds >= 0 ? duration(replica.lagSeconds) : undefined,
    replica.lagBytes >= 0 ? bytes(replica.lagBytes) : undefined,
  ].filter(Boolean)
  const behind = replica.lagSeconds > 0 || replica.lagBytes > 0
  return (
    <span className={cn("numeric", replicaTone(replica) === "warning" && "text-warning")}>
      {parts.length === 0
        ? "lag not known here"
        : behind
          ? `${parts.join(" · ")} behind`
          : "caught up"}
    </span>
  )
}

function SlotWords({ slot }: { slot: DbReplicationSlot }) {
  const held = slot.retainedBytes >= 0 ? bytes(slot.retainedBytes) : undefined
  return (
    <span className={cn("numeric", !slot.active && "text-warning")}>
      {slot.active
        ? `read · ${held ?? "?"} held`
        : `no reader · holds ${held ?? "the log"}${slot.walStatus ? ` · ${slot.walStatus}` : ""}`}
    </span>
  )
}

function ReplicationLegend({ className }: { className?: string }) {
  return (
    <ul
      aria-label="How to read the picture"
      className={cn(
        "flex flex-wrap items-center gap-x-5 gap-y-1.5 text-hint text-muted-foreground",
        className,
      )}
    >
      <LegendLine kind="live">streaming</LegendLine>
      <LegendLine kind="still">kept, read</LegendLine>
      <LegendLine kind="warning">behind, or no reader</LegendLine>
    </ul>
  )
}
