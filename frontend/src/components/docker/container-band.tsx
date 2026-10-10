"use client"

import Link from "next/link"
import { ArrowRight } from "@/components/icons"
import { rowReveal } from "@/components/icon-action"
import { useNow } from "@/components/deploy/vocabulary"
import { HUE, LiveBytes, LiveFigure } from "@/components/overview/readings"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph, containerProduct, imageProduct } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { useArrivals } from "@/hooks/use-arrivals"
import { bytes, percent, plural } from "@/lib/format"
import type { Container, ContainerStats, Snapshot } from "@/lib/types"
import { cn } from "@/lib/utils"
import { cores } from "@/components/procs/shared"
import { ShareBar, shade } from "@/components/procs/workloads"
import { ago } from "@/components/procs/units"
import type { ContainerChange } from "@/components/docker/overview"

/** How many containers each block names. */
const SHOWN = 5

/**
 * What the containers are doing: the five using the most processor and the
 * most memory, each a span of one bar as wide as the machine, and the last
 * thing that happened to each container.
 *
 * It replaced four tiles — running, runtime health, attention, compose stacks
 * — none of which said *which* container was busy or what had just happened
 * to one, which are what a reader opens Docker to find out once they know
 * nothing is down. The figures are the containers socket's, so they move with
 * every frame: each span eases to its next width and each figure glides.
 * Everything else the host has in use is one muted span, so the bar also says
 * how much of the machine is Docker's. A row opens its container.
 */
export function ContainerBand({
  containers,
  stats,
  snapshot,
  hostCpus,
  hostMemory,
  changes,
  listening,
  onOpen,
}: {
  containers: Container[]
  stats: Record<string, ContainerStats>
  snapshot?: Snapshot
  /** The daemon's count, for a host whose metrics have not arrived. */
  hostCpus: number
  hostMemory: number
  changes: ContainerChange[]
  /** Whether the events feed is connected; an empty Recent means nothing only while it is. */
  listening: boolean
  onOpen: (container: { id?: string; name: string }) => void
}) {
  const now = useNow(15_000)
  const up = containers.filter((c) => c.state === "running").length
  const running = containers.filter((c) => c.state === "running" && stats[c.id])
  const measured = running.filter((c) => stats[c.id].cpuReady !== false)
  const byCPU = measured
    .filter((c) => stats[c.id].cpuPercent > 0)
    .sort((a, b) => stats[b.id].cpuPercent - stats[a.id].cpuPercent)
    .slice(0, SHOWN)
  const byMemory = running
    .filter((c) => stats[c.id].memUsage > 0)
    .sort((a, b) => stats[b.id].memUsage - stats[a.id].memUsage)
    .slice(0, SHOWN)

  const coreCount = snapshot?.cpu?.cores || snapshot?.cpu?.perCore?.length || hostCpus
  const memTotal = snapshot?.memory?.total || hostMemory
  const cpuAll = sum(measured, (c) => stats[c.id].cpuPercent)
  const memAll = sum(running, (c) => stats[c.id].memUsage)
  const cpuShown = sum(byCPU, (c) => stats[c.id].cpuPercent)
  const memShown = sum(byMemory, (c) => stats[c.id].memUsage)
  // The rest of the bar is everything else in use on the host, the other
  // containers among it; without the host's own reading, only those.
  const cpuRest = snapshot?.cpu
    ? Math.max(snapshot.cpu.totalPercent * coreCount - cpuShown, 0)
    : cpuAll - cpuShown
  const memRest = snapshot?.memory
    ? Math.max(snapshot.memory.used - memShown, 0)
    : memAll - memShown

  return (
    <div
      data-slot="container-band"
      className="grid min-w-0 gap-x-10 gap-y-6 lg:grid-cols-2 xl:grid-cols-3"
    >
      <Panel plain aria-label="Processor by container">
        <PanelHeader
          title="Processor"
          actions={
            coreCount > 0 && (
              <span
                className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground"
                title="Summed over every running container. One core is 100%."
              >
                <span className="font-medium text-foreground">
                  <LiveFigure value={cpuAll / 100} decimals={cpuAll >= 995 ? 1 : 2} />
                </span>
                of {plural(coreCount, "core")} in containers
              </span>
            )
          }
        />
        <PanelBody className="space-y-3 pt-4">
          <ShareBar
            label="Processor"
            capacity={coreCount > 0 ? coreCount * 100 : cpuAll}
            rest={cpuRest}
            parts={byCPU.map((c, rank) => ({
              key: c.id,
              value: stats[c.id].cpuPercent,
              color: shade(HUE.cpu, rank),
              label: `${c.name} ${cores(stats[c.id].cpuPercent)}`,
            }))}
            format={cores}
          />
          {up === 0 ? (
            <p className="py-2 text-body text-muted-foreground">Nothing is running.</p>
          ) : measured.length === 0 ? (
            <p className="py-2 text-body">
              <TextShimmer>Measuring the first interval…</TextShimmer>
            </p>
          ) : byCPU.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">
              No container is using the processor.
            </p>
          ) : (
            <ul className="-mx-2">
              {byCPU.map((c, rank) => {
                const value = stats[c.id].cpuPercent
                return (
                  <ContainerLine
                    key={c.id}
                    container={c}
                    product={containerProduct(c)}
                    onOpen={onOpen}
                    lead={<RankKey color={shade(HUE.cpu, rank)} />}
                    title={`${percent(value)} of one core`}
                    figure={
                      value / 100 >= 0.005 ? (
                        <LiveFigure
                          value={value / 100}
                          decimals={value >= 99.5 ? 1 : 2}
                          unit=" cores"
                        />
                      ) : (
                        "idle"
                      )
                    }
                  />
                )
              })}
            </ul>
          )}
        </PanelBody>
      </Panel>

      <Panel plain aria-label="Memory by container">
        <PanelHeader
          title="Memory"
          actions={
            memTotal > 0 && (
              <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
                <span className="font-medium text-foreground">
                  <LiveBytes value={memAll} />
                </span>
                of {bytes(memTotal, 0)} in containers
              </span>
            )
          }
        />
        <PanelBody className="space-y-3 pt-4">
          <ShareBar
            label="Memory"
            capacity={memTotal > 0 ? memTotal : memAll}
            rest={memRest}
            parts={byMemory.map((c, rank) => ({
              key: c.id,
              value: stats[c.id].memUsage,
              color: shade(HUE.mem, rank),
              label: `${c.name} ${bytes(stats[c.id].memUsage)}`,
            }))}
            format={(v) => bytes(v)}
          />
          {up === 0 ? (
            <p className="py-2 text-body text-muted-foreground">Nothing is running.</p>
          ) : byMemory.length === 0 ? (
            <p className="py-2 text-body">
              <TextShimmer>Waiting for the first reading…</TextShimmer>
            </p>
          ) : (
            <ul className="-mx-2">
              {byMemory.map((c, rank) => {
                const stat = stats[c.id]
                const limited = stat.memLimited || (c.memoryLimit ?? 0) > 0
                return (
                  <ContainerLine
                    key={c.id}
                    container={c}
                    product={containerProduct(c)}
                    onOpen={onOpen}
                    lead={<RankKey color={shade(HUE.mem, rank)} />}
                    detail={
                      limited ? (
                        <span className={cn(stat.memPercent >= 85 && "text-warning")}>
                          {percent(stat.memPercent)} of its limit
                        </span>
                      ) : undefined
                    }
                    title={
                      limited
                        ? `${bytes(stat.memUsage)} of ${bytes(c.memoryLimit || stat.memLimit)}`
                        : `${bytes(stat.memUsage)}, no limit` +
                          (memTotal > 0
                            ? ` — ${percent((stat.memUsage / memTotal) * 100)} of the host`
                            : "")
                    }
                    figure={<LiveBytes value={stat.memUsage} />}
                  />
                )
              })}
            </ul>
          )}
        </PanelBody>
      </Panel>

      <Panel plain aria-label="Recent changes" className="lg:col-span-2 xl:col-span-1">
        <PanelHeader
          title="Recent"
          actions={
            <Link
              href="/docker/events"
              className="flex h-7 items-center gap-1 rounded-md text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
            >
              {changes.length > 0
                ? `${plural(changes.length, "change")} in the last day`
                : "Events"}
              <ArrowRight className="size-3" />
            </Link>
          }
        />
        <PanelBody className="pt-3">
          <RecentLines changes={changes} now={now} listening={listening} onOpen={onOpen} />
        </PanelBody>
      </Panel>
    </div>
  )
}

function RecentLines({
  changes,
  now,
  listening,
  onOpen,
}: {
  changes: ContainerChange[]
  now: number
  listening: boolean
  onOpen: (container: { id?: string; name: string }) => void
}) {
  const shown = changes.slice(0, SHOWN + 1)
  // A change that lands while the page is open rises into the list.
  const arrived = useArrivals(shown.map((c) => `${c.name}@${c.at}`))
  if (shown.length === 0) {
    return (
      <p className="py-2 text-body text-muted-foreground">
        {listening
          ? "Nothing has started, stopped or failed in the last day."
          : "The dashboard is not listening to Docker's events."}
      </p>
    )
  }
  return (
    <ul className="-mx-2">
      {shown.map((change) => (
        <ChangeLine
          key={change.name}
          change={change}
          now={now}
          onOpen={onOpen}
          arrived={arrived.has(`${change.name}@${change.at}`)}
        />
      ))}
    </ul>
  )
}

function sum<T>(list: T[], value: (item: T) => number) {
  return list.reduce((total, item) => total + value(item), 0)
}

/** The span's key: the rank's step of its measurement's hue, so a row finds itself on the bar. */
function RankKey({ color }: { color: string }) {
  return (
    <span aria-hidden className="h-2.5 w-0.5 shrink-0 rounded-full" style={{ background: color }} />
  )
}

/**
 * One container on a line: what finds it on the bar or in time, its mark
 * and name, a detail, and its figure. A press opens the container; the arrow
 * is what says so, revealed under the pointer and always drawn on a touch
 * screen.
 */
function ContainerLine({
  container,
  product,
  lead,
  detail,
  figure,
  title,
  onOpen,
  className,
}: {
  container: { id?: string; name: string }
  product: string
  lead: React.ReactNode
  detail?: React.ReactNode
  figure: React.ReactNode
  title?: string
  onOpen: (container: { id?: string; name: string }) => void
  className?: string
}) {
  return (
    <li className={className}>
      <button
        type="button"
        aria-label={`Open ${container.name}`}
        title={title}
        onClick={() => onOpen(container)}
        className="group flex h-8 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body focus-ring-inset transition-colors hover:bg-row-hover"
      >
        {lead}
        <span className="flex size-4 shrink-0 items-center justify-center">
          <ProductGlyph id={product} className="size-3.5" />
        </span>
        {/* The name gives way last: a long detail truncates before it does. */}
        <span className="min-w-0 shrink-[0.25] truncate font-medium">{container.name}</span>
        {detail && (
          <span className="numeric min-w-0 truncate text-hint text-muted-foreground">{detail}</span>
        )}
        <span className="numeric ml-auto shrink-0 font-medium text-foreground">{figure}</span>
        <ArrowRight
          aria-hidden
          className={cn("size-3.5 shrink-0 text-muted-foreground", rowReveal())}
        />
      </button>
    </li>
  )
}

/**
 * A recent change: the state's dot, the container, what happened and when. A
 * crash says its status in the same line; a container that came back after
 * crashing says how often it did in the hour, because "started" alone over a
 * restart loop reads as a recovery.
 */
function ChangeLine({
  change,
  now,
  onOpen,
  arrived,
}: {
  change: ContainerChange
  now: number
  onOpen: (container: { id?: string; name: string }) => void
  arrived: boolean
}) {
  const { tone } = change
  return (
    <ContainerLine
      container={{ id: change.id, name: change.name }}
      product={change.image ? imageProduct(change.image) : "docker"}
      onOpen={onOpen}
      className={cn(arrived && "animate-rise")}
      lead={<StatusDot tone={tone} className="mx-px" />}
      title={new Date(change.at).toLocaleString()}
      detail={
        <span
          className={cn(
            tone === "danger" && "text-destructive",
            tone === "warning" && "text-warning",
          )}
        >
          {change.verb}
          {change.crashes > 1 && (
            <span className="text-warning"> · {plural(change.crashes, "crash", "crashes")}</span>
          )}
        </span>
      }
      figure={
        <span className="text-hint font-normal text-muted-foreground">
          {ago(change.at / 1000, now)}
        </span>
      }
    />
  )
}
