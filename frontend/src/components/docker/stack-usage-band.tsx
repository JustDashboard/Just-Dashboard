"use client"

import { Box } from "@/components/icons"
import { BarReading, type ReadingPart } from "@/components/bar-reading"
import { timeAt } from "@/components/bar-reading-model"
import { useNow } from "@/components/deploy/vocabulary"
import { HUE, LiveBytes, LiveFigure } from "@/components/overview/readings"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { DOT_COLOR } from "@/components/status-dot"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { bytes, percent, plural } from "@/lib/format"
import type { Snapshot } from "@/lib/types"
import { cn } from "@/lib/utils"
import { cores } from "@/components/procs/shared"
import { ago } from "@/components/procs/units"
import {
  serviceLane,
  type ServiceChange,
  type ServiceReading,
} from "@/components/docker/stack-service-readings"

/** How many services each bar names: a stack can run more than a strip of marks holds. */
const SHOWN = 5

/** How many marks Recent keeps; the stack's Events view holds the rest. */
const RECENT = 6

/** Recent's axis: older changes sit at its far end, where they can still be reached. */
const DAY = 86_400_000

/**
 * What the stack takes of the machine and what just happened to it: its
 * heaviest services' processor and memory as spans of one bar the size of the
 * server, beside everything else the server is doing, and the last starts,
 * exits, restart loops and failed checks, placed on the last day.
 *
 * It is the Services and PM2 pages' band, drawn for one stack's services: the
 * same bar, the same steps of the measurement's hue, figures that glide to
 * each frame of the containers socket. Recent names each service in its lane,
 * the hue its lines take in the stack's log. A press opens the service's
 * container.
 */
export function StackUsageBand({
  readings,
  changes,
  snapshot,
  eventsRead,
  productOf,
  onOpen,
}: {
  readings: ServiceReading[]
  changes: ServiceChange[]
  snapshot?: Snapshot
  /** Whether the stack's events have been read yet. */
  eventsRead: boolean
  productOf: (service: string) => string | undefined
  onOpen: (service: string) => void
}) {
  const now = useNow(15_000)
  const measured = readings.filter((r) => r.stat)
  const ready = measured.filter((r) => r.stat!.cpuReady !== false)
  const usingCPU = ready
    .filter((r) => r.stat!.cpuPercent > 0)
    .sort((a, b) => b.stat!.cpuPercent - a.stat!.cpuPercent)
  const usingMemory = measured
    .filter((r) => r.stat!.memUsage > 0)
    .sort((a, b) => b.stat!.memUsage - a.stat!.memUsage)
  const byCPU = usingCPU.slice(0, SHOWN)
  const byMemory = usingMemory.slice(0, SHOWN)
  // The heads say the whole stack; the bars name the heaviest of it.
  const cpuAll = sum(usingCPU, (r) => r.stat!.cpuPercent)
  const memAll = sum(usingMemory, (r) => r.stat!.memUsage)
  const cpuShown = sum(byCPU, (r) => r.stat!.cpuPercent)
  const memShown = sum(byMemory, (r) => r.stat!.memUsage)

  // The machine's own readings size the bars: a core is a hundred points of
  // the processor bar, and the memory bar is the host's total with what the
  // rest of the server uses drawn muted beside the stack.
  const coreCount =
    snapshot?.cpu?.cores || snapshot?.cpu?.perCore?.length || measured[0]?.stat?.hostCpus || 0
  const busy = snapshot?.cpu ? snapshot.cpu.totalPercent * coreCount : undefined
  const memTotal = snapshot?.memory?.total ?? 0
  const memUsed = snapshot?.memory?.used
  const running = readings.some((r) => r.state === "running")

  const line = (r: ServiceReading): Pick<ReadingPart, "key" | "mark" | "name" | "press"> => ({
    key: r.key,
    mark: <Mark product={productOf(r.key)} />,
    name: r.key,
    press: { label: `Open ${r.key}`, opens: true, onPress: () => onOpen(r.key) },
  })

  return (
    <div
      data-slot="stack-usage"
      className="grid min-w-0 gap-x-10 gap-y-8 lg:grid-cols-2 xl:grid-cols-3"
    >
      <BarReading
        title="Processor"
        label="Processor by service"
        list="Busiest services"
        hue={HUE.cpu}
        trail
        live
        capacity={coreCount > 0 ? coreCount * 100 : cpuAll}
        rest={busy !== undefined ? Math.max(busy - cpuShown, 0) : cpuAll - cpuShown}
        format={cores}
        figure={
          coreCount > 0 && (
            <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
              <span className="font-medium text-foreground">
                <LiveFigure value={cpuAll / 100} decimals={2} />
              </span>
              of {plural(coreCount, "core")}
            </span>
          )
        }
        message={
          !running ? (
            "Nothing in this stack is running."
          ) : ready.length === 0 ? (
            <TextShimmer>Waiting for the first reading…</TextShimmer>
          ) : byCPU.length === 0 ? (
            "No service is using the processor."
          ) : undefined
        }
        note={usingCPU.length > SHOWN ? `and ${usingCPU.length - SHOWN} more` : undefined}
        parts={byCPU.map((r) => {
          const value = r.stat!.cpuPercent
          return {
            ...line(r),
            value,
            figure:
              value / 100 >= 0.005 ? (
                <LiveFigure value={value / 100} decimals={value >= 99.5 ? 1 : 2} unit=" cores" />
              ) : (
                "idle"
              ),
            label: `${r.key} ${cores(value)}`,
            title: `${percent(value)} of one core`,
          }
        })}
      />

      <BarReading
        title="Memory"
        label="Memory by service"
        list="Services using the most memory"
        hue={HUE.mem}
        trail
        live
        capacity={memTotal > 0 ? memTotal : memAll}
        rest={memUsed !== undefined ? Math.max(memUsed - memShown, 0) : memAll - memShown}
        free="Free"
        format={(v) => bytes(v)}
        figure={
          memTotal > 0 && (
            <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
              <span className="font-medium text-foreground">
                <LiveBytes value={memAll} />
              </span>
              of {bytes(memTotal, 0)}
            </span>
          )
        }
        message={
          !running ? (
            "Nothing in this stack is running."
          ) : measured.length === 0 ? (
            <TextShimmer>Waiting for the first reading…</TextShimmer>
          ) : byMemory.length === 0 ? (
            "No memory is reported yet."
          ) : undefined
        }
        note={usingMemory.length > SHOWN ? `and ${usingMemory.length - SHOWN} more` : undefined}
        parts={byMemory.map((r) => {
          const stat = r.stat!
          const limited = stat.memLimited || (r.container?.memoryLimit ?? 0) > 0
          return {
            ...line(r),
            value: stat.memUsage,
            figure: <LiveBytes value={stat.memUsage} />,
            detail: limited ? (
              <span className={cn(stat.memPercent >= 85 && "text-warning")}>
                {percent(stat.memPercent, 0)} of its limit
              </span>
            ) : undefined,
            label: `${r.key} ${bytes(stat.memUsage)}`,
            title:
              memTotal > 0
                ? `${bytes(stat.memUsage)} — ${percent((stat.memUsage / memTotal) * 100)} of the server`
                : bytes(stat.memUsage),
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
          changes.length > 0 && (
            <span className="numeric flex h-7 items-center text-hint text-muted-foreground">
              {plural(changes.length, "change")}
            </span>
          )
        }
        message={
          !eventsRead ? (
            <TextShimmer>Reading what Docker did…</TextShimmer>
          ) : changes.length === 0 ? (
            "Nothing has started, stopped or failed since Docker's events were kept."
          ) : undefined
        }
        parts={changes.slice(0, RECENT).map((change) => ({
          key: change.key,
          value: 1,
          at: timeAt(change.at, now, DAY),
          color: DOT_COLOR[change.tone],
          mark: <Mark product={productOf(change.service)} />,
          name: <span style={{ color: serviceLane(change.service) }}>{change.service}</span>,
          detail: (
            <span
              className={cn(
                change.tone === "danger" && "text-destructive",
                change.tone === "warning" && "text-warning",
              )}
            >
              {change.verb}
            </span>
          ),
          figure: (
            <span className="text-hint font-normal text-muted-foreground">
              {ago(change.at / 1000, now)}
            </span>
          ),
          label: `${change.service} ${change.verb} ${ago(change.at / 1000, now)}`,
          title: new Date(change.at).toLocaleString(),
          press: {
            label: `Open ${change.service}`,
            opens: true,
            onPress: () => onOpen(change.service),
          },
        }))}
      />
    </div>
  )
}

function sum<T>(list: T[], value: (item: T) => number) {
  return list.reduce((total, item) => total + value(item), 0)
}

/** The service as the product it runs. */
function Mark({ product }: { product?: string }) {
  return hasProductLogo(product) ? (
    <ProductGlyph id={product} className="size-3.5" />
  ) : (
    <Box aria-hidden className="size-3.5 text-muted-foreground" />
  )
}
