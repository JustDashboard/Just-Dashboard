"use client"

import { Box } from "@/components/icons"
import { BarReading, type ReadingPart } from "@/components/bar-reading"
import { timeAt } from "@/components/bar-reading-model"
import { HUE, LiveBytes, LiveFigure } from "@/components/overview/readings"
import { ProductGlyph, containerProduct, imageProduct } from "@/components/product-logo"
import { DOT_COLOR, Status, type DotTone } from "@/components/status-dot"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { cores } from "@/components/procs/shared"
import { ago } from "@/components/procs/units"
import { spanWords, type EventEntry } from "@/lib/docker-events"
import { bytes, percent, plural } from "@/lib/format"
import type { Container, ContainerStats, DockerEvent, Snapshot } from "@/lib/types"
import { cn } from "@/lib/utils"
import { exitWords } from "@/components/docker/containers"

/** How many containers each block names; the rest of the host is one muted span. */
const SHOWN = 5

/** Recent's axis: older events sit at its far end, where they can still be reached. */
const DAY = 86_400_000

/**
 * What the containers are doing: the five using the most processor and the
 * most memory, each a span of one bar as wide as the machine, and the last
 * things that happened to them, placed on the last day.
 *
 * It replaced the runtime health bar, which said how many containers were up
 * and checked and nothing about which of them was busy or what had just gone
 * wrong — the two questions a reader opens Containers with after "is anything
 * down". The figures are the container socket's own frames, so a span eases
 * and a figure glides every two seconds; the host's readings size the bars,
 * with everything the host has in use beyond these five drawn muted. Recent
 * is Docker's event log, the one Events reads, with a restart loop folded to
 * one mark and an OOM kill on the exit it caused. A press opens its container,
 * and an event about a container that no longer exists opens nothing.
 */
export function ContainerBand({
  containers,
  stats,
  snapshot,
  entries,
  listening,
  loaded,
  now,
  onOpen,
}: {
  containers: Container[]
  stats: Record<string, ContainerStats>
  snapshot?: Snapshot
  entries: EventEntry[]
  /** Whether the event socket is open, so an empty Recent means nothing happened. */
  listening: boolean
  /** Whether the container list has been read, so none running means none rather than not yet. */
  loaded: boolean
  now: number
  onOpen: (id: string) => void
}) {
  const byId = new Map(containers.map((c) => [c.id, c]))
  const up = containers.some((c) => c.state === "running")
  const measured = containers.flatMap((c) => {
    const stat = stats[c.id]
    return stat && c.state === "running" ? [{ container: c, stat }] : []
  })
  const ready = measured.filter(({ stat }) => stat.cpuReady !== false)
  const byCPU = ready
    .filter(({ stat }) => stat.cpuPercent > 0)
    .sort((a, b) => b.stat.cpuPercent - a.stat.cpuPercent)
    .slice(0, SHOWN)
  const byMemory = measured
    .filter(({ stat }) => stat.memUsage > 0)
    .sort((a, b) => b.stat.memUsage - a.stat.memUsage)
    .slice(0, SHOWN)

  const coreCount =
    snapshot?.cpu?.cores || snapshot?.cpu?.perCore?.length || measured[0]?.stat.hostCpus || 0
  const busy = snapshot?.cpu ? (snapshot.cpu.totalPercent * coreCount) / 100 : undefined
  const memTotal = snapshot?.memory?.total ?? 0
  const memUsed = snapshot?.memory?.used ?? 0
  const cpuSum = sum(byCPU, ({ stat }) => stat.cpuPercent)
  const memSum = sum(byMemory, ({ stat }) => stat.memUsage)
  // Everything the containers hold, which the header says against the host:
  // the five named are the bar, the sum is how much of the machine is Docker.
  const allCPU = sum(ready, ({ stat }) => stat.cpuPercent)
  const allMemory = sum(measured, ({ stat }) => stat.memUsage)

  const nothing = loaded ? (
    "Nothing is running."
  ) : (
    <TextShimmer>Reading the containers…</TextShimmer>
  )

  const line = (c: Container): Pick<ReadingPart, "key" | "mark" | "name" | "press"> => ({
    key: c.id,
    mark: <Mark product={containerProduct(c)} />,
    name: c.name,
    press: { label: `Open ${c.name}`, opens: true, onPress: () => onOpen(c.id) },
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
        capacity={coreCount > 0 ? coreCount * 100 : cpuSum}
        rest={busy !== undefined ? Math.max(busy * 100 - cpuSum, 0) : 0}
        format={cores}
        figure={
          measured.length > 0 && (
            <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
              <span className="font-medium text-foreground">
                <LiveFigure value={allCPU / 100} decimals={allCPU >= 995 ? 1 : 2} />
              </span>
              {coreCount > 0 ? `of ${plural(coreCount, "core")}` : "cores"}
            </span>
          )
        }
        message={
          !up ? (
            nothing
          ) : ready.length === 0 ? (
            <TextShimmer>Measuring the first interval…</TextShimmer>
          ) : byCPU.length === 0 ? (
            "No container is using the processor."
          ) : undefined
        }
        parts={byCPU.map(({ container, stat }) => ({
          ...line(container),
          value: stat.cpuPercent,
          figure:
            stat.cpuPercent / 100 >= 0.005 ? (
              <LiveFigure
                value={stat.cpuPercent / 100}
                decimals={stat.cpuPercent >= 99.5 ? 1 : 2}
                unit=" cores"
              />
            ) : (
              "idle"
            ),
          label: `${container.name} ${cores(stat.cpuPercent)}`,
          title: `${percent(stat.cpuPercent)} of one core`,
        }))}
      />

      <BarReading
        title="Memory"
        label="Memory by container"
        list="Containers using the most memory"
        hue={HUE.mem}
        trail
        live
        capacity={memTotal > 0 ? memTotal : memSum}
        rest={Math.max(memUsed - memSum, 0)}
        free="Free"
        format={(v) => bytes(v)}
        figure={
          measured.length > 0 && (
            <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
              <span className="font-medium text-foreground">
                <LiveBytes value={allMemory} />
              </span>
              {memTotal > 0 ? `of ${bytes(memTotal, 0)}` : "in use"}
            </span>
          )
        }
        message={
          !up ? (
            nothing
          ) : measured.length === 0 ? (
            <TextShimmer>Waiting for the first reading…</TextShimmer>
          ) : byMemory.length === 0 ? (
            "No memory is reported."
          ) : undefined
        }
        parts={byMemory.map(({ container, stat }) => {
          const limited = stat.memLimited || (container.memoryLimit ?? 0) > 0
          const limit = container.memoryLimit || stat.memLimit
          return {
            ...line(container),
            value: stat.memUsage,
            figure: <LiveBytes value={stat.memUsage} />,
            // The limit where there is one; "no limit" is the table's to say.
            detail: limited ? (
              <span className={cn(stat.memPercent >= 85 && "text-warning")}>
                of {bytes(limit, 0)}
              </span>
            ) : undefined,
            label: `${container.name} ${bytes(stat.memUsage)}`,
            title:
              `${bytes(stat.memUsage)} in use${limited ? "" : ", no limit"}` +
              (memTotal > 0 ? ` — ${percent((stat.memUsage / memTotal) * 100)} of the host` : ""),
          }
        })}
      />

      <BarReading
        title="Recent"
        label="Recent container events"
        list="Latest events"
        className="lg:col-span-2 xl:col-span-1"
        ticks
        figure={
          <span className="flex h-7 items-center">
            {listening ? (
              <Status live tone="running" label="Live" />
            ) : (
              <Status tone="notice" label="Connecting…" />
            )}
          </span>
        }
        message={
          entries.length > 0 ? undefined : listening ? (
            "Nothing has started, stopped or failed since the dashboard began listening."
          ) : (
            <TextShimmer>Waiting for Docker&apos;s event log.</TextShimmer>
          )
        }
        parts={entries.slice(0, SHOWN + 1).map((entry): ReadingPart => {
          const event: DockerEvent = entry.kind === "loop" ? entry.exit : entry.event
          const container = event.id ? byId.get(event.id) : undefined
          const { words, tone } = describe(entry)
          const time = Date.parse(entry.kind === "loop" ? entry.until : event.time)
          const product = container
            ? containerProduct(container)
            : event.image
              ? imageProduct(event.image)
              : "docker"
          const name = container?.name ?? event.name ?? ""
          return {
            key: entry.key,
            value: 1,
            at: timeAt(time, now, DAY),
            color: DOT_COLOR[tone],
            mark: <Mark product={product} />,
            name,
            detail: (
              <span
                className={cn(
                  tone === "danger" && "text-destructive",
                  tone === "warning" && "text-warning",
                )}
              >
                {words}
              </span>
            ),
            figure: (
              <span className="text-hint font-normal text-muted-foreground">
                {ago(time / 1000, now)}
              </span>
            ),
            label: `${name} ${words} ${ago(time / 1000, now)}`,
            title: new Date(time).toLocaleString(),
            press: container
              ? { label: `Open ${name}`, opens: true, onPress: () => onOpen(container.id) }
              : undefined,
          }
        })}
      />
    </div>
  )
}

function sum<T>(list: T[], value: (item: T) => number) {
  return list.reduce((total, item) => total + value(item), 0)
}

/** The container as its image's product. */
function Mark({ product }: { product: string }) {
  return product !== "docker" ? (
    <ProductGlyph id={product} className="size-3.5" />
  ) : (
    <Box aria-hidden className="size-3.5 text-muted-foreground" />
  )
}

/** What one entry says happened, in the tone it is read in. */
function describe(entry: EventEntry): { words: string; tone: DotTone } {
  if (entry.kind === "loop") {
    const span = spanWords(Date.parse(entry.until) - Date.parse(entry.since))
    const why = exitWords(entry.exitCode ? Number(entry.exitCode) : undefined, entry.oom)
    return {
      words: `restarted ×${entry.times} in ${span}${why ? ` · ${why}` : ""}`,
      tone: "danger",
    }
  }
  const { event, oom } = entry
  switch (event.action) {
    case "start":
      return { words: "started", tone: "running" }
    case "restart":
      return { words: "restarted", tone: "warning" }
    case "die": {
      const code = event.exitCode ? Number(event.exitCode) : undefined
      const failed = Boolean(oom) || (code !== undefined && ![0, 130, 137, 143].includes(code))
      const words = exitWords(code, Boolean(oom)) ?? "exited"
      return failed
        ? { words: oom ? words : `crashed · ${words}`, tone: "danger" }
        : { words, tone: "stopped" }
    }
    case "oom":
      return { words: "a process was killed for memory", tone: "warning" }
    case "pause":
      return { words: "paused", tone: "notice" }
    case "unpause":
      return { words: "resumed", tone: "running" }
    case "destroy":
      return { words: "removed", tone: "stopped" }
    case "create":
      return { words: "created", tone: "notice" }
    case "health_status: unhealthy":
      return { words: "failing its health check", tone: "danger" }
    case "health_status: healthy":
      return { words: "health check passing again", tone: "running" }
    default:
      return { words: event.action, tone: "notice" }
  }
}
