"use client"

import Link from "next/link"
import { ArrowRight } from "@/components/icons"
import { BarReading, type ReadingPart } from "@/components/bar-reading"
import { timeAt } from "@/components/bar-reading-model"
import { useNow } from "@/components/deploy/vocabulary"
import { HUE, LiveBytes, LiveFigure } from "@/components/overview/readings"
import { ProductGlyph, containerProduct, imageProduct } from "@/components/product-logo"
import { DOT_COLOR } from "@/components/status-dot"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { bytes, percent, plural } from "@/lib/format"
import type { Container, ContainerStats, Snapshot } from "@/lib/types"
import { cn } from "@/lib/utils"
import { cores } from "@/components/procs/shared"
import { ago } from "@/components/procs/units"
import type { ContainerChange } from "@/components/docker/overview"

/** How many containers each block names. */
const SHOWN = 5

/** Recent's axis, the day the feed reads. */
const DAY = 86_400_000

/**
 * What the containers are doing: the five using the most processor and the
 * most memory, each a span of one bar as wide as the machine, and the last
 * thing that happened to each container, placed on the last day.
 *
 * It replaced four tiles — running, runtime health, attention, compose stacks
 * — none of which said *which* container was busy or what had just happened
 * to one, which are what a reader opens Docker to find out once they know
 * nothing is down. The figures are the containers socket's, so they move with
 * every frame: each span eases to its next width and each figure glides.
 * Everything else the host has in use is one muted span, so the bar also says
 * how much of the machine is Docker's. A press opens its container.
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

  const line = (c: Container): Pick<ReadingPart, "key" | "mark" | "name" | "press"> => ({
    key: c.id,
    mark: <ProductGlyph id={containerProduct(c)} className="size-3.5" />,
    name: c.name,
    press: { label: `Open ${c.name}`, opens: true, onPress: () => onOpen(c) },
  })

  return (
    <div
      data-slot="container-band"
      className="grid min-w-0 gap-x-10 gap-y-6 lg:grid-cols-2 xl:grid-cols-3"
    >
      <BarReading
        title="Processor"
        label="Processor by container"
        list="Busiest containers"
        hue={HUE.cpu}
        trail
        live
        capacity={coreCount > 0 ? coreCount * 100 : cpuAll}
        rest={cpuRest}
        format={cores}
        figure={
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
        message={
          up === 0 ? (
            "Nothing is running."
          ) : measured.length === 0 ? (
            <TextShimmer>Measuring the first interval…</TextShimmer>
          ) : byCPU.length === 0 ? (
            "No container is using the processor."
          ) : undefined
        }
        parts={byCPU.map((c) => {
          const value = stats[c.id].cpuPercent
          return {
            ...line(c),
            value,
            figure:
              value / 100 >= 0.005 ? (
                <LiveFigure value={value / 100} decimals={value >= 99.5 ? 1 : 2} unit=" cores" />
              ) : (
                "idle"
              ),
            label: `${c.name} ${cores(value)}`,
            title: `${percent(value)} of one core`,
          }
        })}
      />

      <BarReading
        title="Memory"
        label="Memory by container"
        list="Containers using the most memory"
        hue={HUE.mem}
        trail
        live
        capacity={memTotal > 0 ? memTotal : memAll}
        rest={memRest}
        free="Free"
        format={(v) => bytes(v)}
        figure={
          memTotal > 0 && (
            <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
              <span className="font-medium text-foreground">
                <LiveBytes value={memAll} />
              </span>
              of {bytes(memTotal, 0)} in containers
            </span>
          )
        }
        message={
          up === 0 ? (
            "Nothing is running."
          ) : byMemory.length === 0 ? (
            <TextShimmer>Waiting for the first reading…</TextShimmer>
          ) : undefined
        }
        parts={byMemory.map((c) => {
          const stat = stats[c.id]
          const limited = stat.memLimited || (c.memoryLimit ?? 0) > 0
          return {
            ...line(c),
            value: stat.memUsage,
            figure: <LiveBytes value={stat.memUsage} />,
            detail: limited ? (
              <span className={cn(stat.memPercent >= 85 && "text-warning")}>
                {percent(stat.memPercent)} of its limit
              </span>
            ) : undefined,
            label: `${c.name} ${bytes(stat.memUsage)}`,
            title: limited
              ? `${bytes(stat.memUsage)} of ${bytes(c.memoryLimit || stat.memLimit)}`
              : `${bytes(stat.memUsage)}, no limit` +
                (memTotal > 0 ? ` — ${percent((stat.memUsage / memTotal) * 100)} of the host` : ""),
          }
        })}
      />

      <BarReading
        title="Recent"
        label="Recent changes"
        list="Latest changes"
        className="lg:col-span-2 xl:col-span-1"
        ticks
        figure={
          <Link
            href="/docker/events"
            className="flex h-7 items-center gap-1 rounded-md text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
          >
            {changes.length > 0 ? `${plural(changes.length, "change")} in the last day` : "Events"}
            <ArrowRight className="size-3" />
          </Link>
        }
        message={
          changes.length > 0
            ? undefined
            : listening
              ? "Nothing has started, stopped or failed in the last day."
              : "The dashboard is not listening to Docker's events."
        }
        parts={changes.slice(0, SHOWN + 1).map((change) => ({
          key: change.name,
          value: 1,
          at: timeAt(change.at, now, DAY),
          color: DOT_COLOR[change.tone],
          mark: (
            <ProductGlyph
              id={change.image ? imageProduct(change.image) : "docker"}
              className="size-3.5"
            />
          ),
          name: change.name,
          detail: (
            <span
              className={cn(
                change.tone === "danger" && "text-destructive",
                change.tone === "warning" && "text-warning",
              )}
            >
              {change.verb}
              {change.crashes > 1 && (
                <span className="text-warning">
                  {" "}
                  · {plural(change.crashes, "crash", "crashes")}
                </span>
              )}
            </span>
          ),
          figure: (
            <span className="text-hint font-normal text-muted-foreground">
              {ago(change.at / 1000, now)}
            </span>
          ),
          label: `${change.name} ${change.verb} ${ago(change.at / 1000, now)}`,
          title: new Date(change.at).toLocaleString(),
          press: {
            label: `Open ${change.name}`,
            opens: true,
            onPress: () => onOpen({ id: change.id, name: change.name }),
          },
        }))}
      />
    </div>
  )
}

function sum<T>(list: T[], value: (item: T) => number) {
  return list.reduce((total, item) => total + value(item), 0)
}
