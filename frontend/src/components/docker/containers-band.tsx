"use client"

import { ArrowRight, Box } from "@/components/icons"
import { rowReveal } from "@/components/icon-action"
import { HUE, LiveBytes, LiveFigure } from "@/components/overview/readings"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph, containerProduct, imageProduct } from "@/components/product-logo"
import { Status, StatusDot, type DotTone } from "@/components/status-dot"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { ShareBar, shade } from "@/components/procs/workloads"
import { cores } from "@/components/procs/shared"
import { ago } from "@/components/procs/units"
import { spanWords, type EventEntry } from "@/lib/docker-events"
import { bytes, percent, plural } from "@/lib/format"
import type { Container, ContainerStats, DockerEvent, Snapshot } from "@/lib/types"
import { cn } from "@/lib/utils"
import { exitWords } from "@/components/docker/containers"

/** How many containers each block names; the rest of the host is one muted span. */
const SHOWN = 5

/**
 * What the containers are doing: the five using the most processor and the
 * most memory, each a span of one bar as wide as the machine, and the last
 * things that happened to them.
 *
 * It replaced the runtime health bar, which said how many containers were up
 * and checked and nothing about which of them was busy or what had just gone
 * wrong — the two questions a reader opens Containers with after "is anything
 * down". The figures are the container socket's own frames, so a span eases
 * and a figure glides every two seconds; the host's readings size the bars,
 * with everything the host has in use beyond these five drawn muted. Recent
 * is Docker's event log, the one Events reads, with a restart loop folded to
 * one line and an OOM kill on the exit it caused. A row opens its container.
 */
export function ContainerBand({
  containers,
  stats,
  snapshot,
  entries,
  listening,
  now,
  onOpen,
}: {
  containers: Container[]
  stats: Record<string, ContainerStats>
  snapshot?: Snapshot
  entries: EventEntry[]
  /** Whether the event socket is open, so an empty Recent means nothing happened. */
  listening: boolean
  now: number
  onOpen: (id: string) => void
}) {
  const byId = new Map(containers.map((c) => [c.id, c]))
  const measured = containers.flatMap((c) => {
    const stat = stats[c.id]
    return stat && c.state === "running" ? [{ container: c, stat }] : []
  })
  const byCPU = measured
    .filter(({ stat }) => stat.cpuReady !== false && stat.cpuPercent > 0)
    .sort((a, b) => b.stat.cpuPercent - a.stat.cpuPercent)
    .slice(0, SHOWN)
  const byMemory = measured
    .filter(({ stat }) => stat.memUsage > 0)
    .sort((a, b) => b.stat.memUsage - a.stat.memUsage)
    .slice(0, SHOWN)
  const waiting = measured.length > 0 && measured.every(({ stat }) => stat.cpuReady === false)

  const coreCount =
    snapshot?.cpu?.cores || snapshot?.cpu?.perCore?.length || measured[0]?.stat.hostCpus || 0
  const busy = snapshot?.cpu ? (snapshot.cpu.totalPercent * coreCount) / 100 : undefined
  const memTotal = snapshot?.memory?.total ?? 0
  const memUsed = snapshot?.memory?.used ?? 0
  const cpuSum = sum(byCPU, ({ stat }) => stat.cpuPercent)
  const memSum = sum(byMemory, ({ stat }) => stat.memUsage)
  // Everything the containers hold, which the header says against the host:
  // the five named are the bar, the sum is how much of the machine is Docker.
  const allCPU = sum(measured, ({ stat }) => (stat.cpuReady === false ? 0 : stat.cpuPercent))
  const allMemory = sum(measured, ({ stat }) => stat.memUsage)

  return (
    <div
      data-slot="container-band"
      className="grid min-w-0 gap-x-10 gap-y-6 lg:grid-cols-2 xl:grid-cols-3"
    >
      <Panel plain aria-label="Processor by container">
        <PanelHeader
          title="Processor"
          actions={
            measured.length > 0 && (
              <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
                <span className="font-medium text-foreground">
                  <LiveFigure value={allCPU / 100} decimals={allCPU >= 995 ? 1 : 2} />
                </span>
                {coreCount > 0 ? `of ${plural(coreCount, "core")}` : "cores"}
              </span>
            )
          }
        />
        <PanelBody className="space-y-3 pt-4">
          <ShareBar
            label="Processor"
            capacity={coreCount > 0 ? coreCount * 100 : cpuSum}
            rest={busy !== undefined ? Math.max(busy * 100 - cpuSum, 0) : 0}
            parts={byCPU.map(({ container, stat }, rank) => ({
              key: container.id,
              value: stat.cpuPercent,
              color: shade(HUE.cpu, rank),
              label: `${container.name} ${cores(stat.cpuPercent)}`,
            }))}
            format={cores}
          />
          {measured.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">Nothing is running.</p>
          ) : waiting ? (
            <p className="py-2 text-body">
              <TextShimmer>Measuring the first interval…</TextShimmer>
            </p>
          ) : byCPU.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">
              No container is using the processor.
            </p>
          ) : (
            <ul className="-mx-2">
              {byCPU.map(({ container, stat }, rank) => (
                <ContainerLine
                  key={container.id}
                  container={container}
                  onOpen={onOpen}
                  lead={<RankKey color={shade(HUE.cpu, rank)} />}
                  detail={
                    container.cpuLimit
                      ? `of ${plural(container.cpuLimit, "core")}`
                      : stat.pids > 0
                        ? plural(stat.pids, "process", "processes")
                        : undefined
                  }
                  title={`${percent(stat.cpuPercent)} of one core`}
                  figure={
                    stat.cpuPercent / 100 >= 0.005 ? (
                      <LiveFigure
                        value={stat.cpuPercent / 100}
                        decimals={stat.cpuPercent >= 99.5 ? 1 : 2}
                        unit=" cores"
                      />
                    ) : (
                      "idle"
                    )
                  }
                />
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>

      <Panel plain aria-label="Memory by container">
        <PanelHeader
          title="Memory"
          actions={
            measured.length > 0 && (
              <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
                <span className="font-medium text-foreground">
                  <LiveBytes value={allMemory} />
                </span>
                {memTotal > 0 ? `of ${bytes(memTotal, 0)}` : "in use"}
              </span>
            )
          }
        />
        <PanelBody className="space-y-3 pt-4">
          <ShareBar
            label="Memory"
            capacity={memTotal > 0 ? memTotal : memSum}
            rest={Math.max(memUsed - memSum, 0)}
            parts={byMemory.map(({ container, stat }, rank) => ({
              key: container.id,
              value: stat.memUsage,
              color: shade(HUE.mem, rank),
              label: `${container.name} ${bytes(stat.memUsage)}`,
            }))}
            format={(v) => bytes(v)}
          />
          {byMemory.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">
              {measured.length === 0 ? "Nothing is running." : "No memory is reported."}
            </p>
          ) : (
            <ul className="-mx-2">
              {byMemory.map(({ container, stat }, rank) => {
                const limited = stat.memLimited || (container.memoryLimit ?? 0) > 0
                const limit = container.memoryLimit || stat.memLimit
                return (
                  <ContainerLine
                    key={container.id}
                    container={container}
                    onOpen={onOpen}
                    lead={<RankKey color={shade(HUE.mem, rank)} />}
                    // The limit where there is one; "no limit" is the table's to say.
                    detail={
                      limited && (
                        <span className={cn(stat.memPercent >= 85 && "text-warning")}>
                          of {bytes(limit, 0)}
                        </span>
                      )
                    }
                    title={
                      `${bytes(stat.memUsage)} in use${limited ? "" : ", no limit"}` +
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

      <Panel plain aria-label="Recent container events" className="lg:col-span-2 xl:col-span-1">
        <PanelHeader
          title="Recent"
          actions={
            <span className="flex h-7 items-center">
              {listening ? (
                <Status live tone="running" label="Live" />
              ) : (
                <Status tone="notice" label="Connecting…" />
              )}
            </span>
          }
        />
        <PanelBody className="pt-3">
          {entries.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">
              {listening
                ? "Nothing has started, stopped or failed since the dashboard began listening."
                : "Waiting for Docker's event log."}
            </p>
          ) : (
            <ul className="-mx-2">
              {entries.slice(0, SHOWN + 1).map((entry) => (
                <EventLine key={entry.key} entry={entry} byId={byId} now={now} onOpen={onOpen} />
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>
    </div>
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

/** The container as its image's product, at the line's height. */
function Mark({ product }: { product: string }) {
  return (
    <span className="flex size-4 shrink-0 items-center justify-center">
      {product !== "docker" ? (
        <ProductGlyph id={product} className="size-3.5" />
      ) : (
        <Box aria-hidden className="size-3.5 text-muted-foreground" />
      )}
    </span>
  )
}

/**
 * One container on a line: what finds it on the bar or in time, its mark and
 * name, a detail, and its figure. A press opens the container; the arrow is
 * what says so, revealed under the pointer and always drawn on a touch screen.
 * A line about a container that no longer exists opens nothing and has no
 * arrow.
 */
function ContainerLine({
  container,
  name,
  product,
  lead,
  detail,
  figure,
  title,
  onOpen,
}: {
  container?: Container
  name?: string
  product?: string
  lead: React.ReactNode
  detail?: React.ReactNode
  figure: React.ReactNode
  title?: string
  onOpen: (id: string) => void
}) {
  const label = container?.name ?? name ?? ""
  const body = (
    <>
      {lead}
      <Mark product={product ?? (container ? containerProduct(container) : "docker")} />
      {/* The name keeps its width before the detail gives up any of its own. */}
      <span className="max-w-[70%] shrink-0 truncate font-medium">{label}</span>
      {detail && (
        <span className="numeric min-w-0 shrink truncate text-hint text-muted-foreground">
          {detail}
        </span>
      )}
      <span className="numeric ml-auto shrink-0 font-medium text-foreground">{figure}</span>
    </>
  )
  const line = "flex h-8 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body"
  if (!container) {
    return (
      <li className={line} title={title}>
        {body}
        <span aria-hidden className="size-3.5 shrink-0" />
      </li>
    )
  }
  return (
    <li>
      <button
        type="button"
        aria-label={`Open ${label}`}
        title={title}
        onClick={() => onOpen(container.id)}
        className={cn("group focus-ring-inset transition-colors hover:bg-row-hover", line)}
      >
        {body}
        <ArrowRight
          aria-hidden
          className={cn("size-3.5 shrink-0 text-muted-foreground", rowReveal())}
        />
      </button>
    </li>
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

/**
 * A recent event: the state's dot, the container, what happened and when. A
 * failure is drawn in its tone, and a loop says how many times and for how
 * long in the same line.
 */
function EventLine({
  entry,
  byId,
  now,
  onOpen,
}: {
  entry: EventEntry
  byId: Map<string, Container>
  now: number
  onOpen: (id: string) => void
}) {
  const event: DockerEvent = entry.kind === "loop" ? entry.exit : entry.event
  const container = event.id ? byId.get(event.id) : undefined
  const { words, tone } = describe(entry)
  const time = entry.kind === "loop" ? entry.until : event.time
  return (
    <ContainerLine
      container={container}
      name={event.name}
      product={container ? undefined : event.image ? imageProduct(event.image) : "docker"}
      onOpen={onOpen}
      lead={<StatusDot tone={tone} className="mx-px" />}
      title={new Date(time).toLocaleString()}
      detail={
        <span
          className={cn(
            tone === "danger" && "text-destructive",
            tone === "warning" && "text-warning",
          )}
        >
          {words}
        </span>
      }
      figure={
        <span className="text-hint font-normal text-muted-foreground">
          {ago(Date.parse(time) / 1000, now)}
        </span>
      }
    />
  )
}
