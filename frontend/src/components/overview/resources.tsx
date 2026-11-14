"use client"

import { Connection, Cpu, Database, Gauge, Layers, type Icon } from "@/components/icons"
import { bytes, percent, rate } from "@/lib/format"
import type { MountStats, Snapshot } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Meter, utilisationTone } from "@/components/meter"
import { TileTrend } from "@/components/metrics/sparkline"
import { Bone } from "@/components/bone"
import { ReadingLine, ReadingLines } from "@/components/reading-line"
import type { Tone } from "@/components/tone"
import { WireMark } from "@/components/deploy/wire"
import { Skeleton } from "@/components/ui/skeleton"
import { CoreBars, HUE, LiveBytes, LiveFigure } from "./readings"
import { Filesystem } from "./storage"

/**
 * Past this many the fullest are drawn and the rest are on /metrics: a host
 * with a dozen mounts would otherwise push the projects off the first screen.
 */
const SHOWN = 4

export type ResourceTrends = Record<"cpu" | "mem" | "load" | "net" | "disk", number[]>

/**
 * The machine's readings as lines, the shape a project's Overview gives its
 * own: a measurement's glyph on its hue, the reading as words, what it is made
 * of under it, and its last hour at the line's end. They were four tiles and a
 * fifth in the storage band, a figure over a unit over a caption each, on the
 * one page the operator asked to carry no metric cards.
 *
 * The mark takes the reading's tone past the lines every utilisation figure
 * turns at, so a processor at 95% is amber before a word is read. Every line
 * goes to Metrics, which has the rest.
 *
 * The disks close the run: what they are doing is a line like the others, and
 * what they hold is a bar each, because how close a disk is to full is read
 * off a length. One filesystem, the common case, sits beside its line; more
 * take rows of their own, two to a row.
 */
export function ResourceReadings({
  snapshot,
  trends,
  recorded,
}: {
  snapshot: Snapshot
  trends: ResourceTrends
  /** False where the server keeps no history: a meter stands where the hour would be. */
  recorded: boolean
}) {
  const cores = snapshot.cpu.cores || 1
  const modes = snapshot.cpu.modes
  const rx = snapshot.net.reduce((sum, n) => sum + n.recvRate, 0)
  const tx = snapshot.net.reduce((sum, n) => sum + n.sendRate, 0)
  const availPercent =
    snapshot.memory.total > 0 ? (snapshot.memory.available / snapshot.memory.total) * 100 : 0
  const memTone: Tone = availPercent <= 5 ? "danger" : availPercent <= 10 ? "warning" : "default"
  const loadShare = (snapshot.cpu.loadAvg1 / cores) * 100
  const mounts = snapshot.mounts
  const read = mounts.reduce((sum, m) => sum + m.readRate, 0)
  const write = mounts.reduce((sum, m) => sum + m.writeRate, 0)
  const shown = fullest(mounts)

  // `TileTrend` leaves out a line that never moves on a scale of its own, which
  // would otherwise fill the band and read as a full meter.
  const hour = (values: number[], label: string, color: string, max?: number) => (
    <TileTrend values={values} max={max} color={color} label={`${label} over the last hour`} />
  )
  const level = (value: number, tone: Tone, label: string) => (
    <span className="flex h-9 items-center">
      <Meter value={value} tone={tone} label={label} />
    </span>
  )

  return (
    <div data-slot="resources" className="min-w-0">
      <ReadingLines>
        <ReadingLine
          href="/metrics"
          label="CPU on Metrics"
          mark={
            <Mark tone={utilisationTone(snapshot.cpu.totalPercent)} hue={HUE.cpu} glyph={Cpu} />
          }
          name="CPU"
          hint={
            <>
              {modes
                ? `${modes.user.toFixed(0)}% user · ${modes.system.toFixed(0)}% sys · ${modes.iowait.toFixed(0)}% wait`
                : `load ${snapshot.cpu.loadAvg1.toFixed(2)}`}
              {modes && modes.steal >= 1 && (
                <span className="numeric font-medium text-destructive">
                  {" · "}
                  {percent(modes.steal, 0)} steal
                </span>
              )}
            </>
          }
          trend={
            recorded
              ? hour(trends.cpu, "CPU", HUE.cpu, 100)
              : level(snapshot.cpu.totalPercent, utilisationTone(snapshot.cpu.totalPercent), "CPU")
          }
        >
          <span className="numeric">
            <LiveFigure value={snapshot.cpu.totalPercent} decimals={1} unit="%" />
          </span>{" "}
          busy
          {/* Every core from the same frame, on the reading's own line: the
              total says how busy the machine is, this whether that is one
              core pinned. Held to the words' height so the line stays one. */}
          <span className="ml-2 inline-flex align-[-2px] [&>span]:h-3.5">
            <CoreBars cores={snapshot.cpu.perCore} color={HUE.cpu} />
          </span>
        </ReadingLine>
        <ReadingLine
          href="/metrics"
          label="Memory on Metrics"
          mark={<Mark tone={memTone} hue={HUE.mem} glyph={Layers} />}
          name="Memory"
          hint={`${percent(snapshot.memory.usedPercent, 0)} used · ${bytes(snapshot.memory.cached)} cached`}
          trend={
            recorded
              ? hour(trends.mem, "Memory", HUE.mem, 100)
              : level(100 - availPercent, memTone, "Memory")
          }
        >
          <span className="numeric">
            <LiveBytes value={snapshot.memory.available} />
          </span>{" "}
          free
        </ReadingLine>
        <ReadingLine
          href="/metrics"
          label="Load on Metrics"
          mark={
            <Mark
              tone={utilisationTone((snapshot.cpu.loadAvg5 / cores) * 100)}
              hue={HUE.load}
              glyph={Gauge}
            />
          }
          name="Load"
          hint={`${snapshot.cpu.loadAvg5.toFixed(2)} · ${snapshot.cpu.loadAvg15.toFixed(2)} over 5 and 15 min`}
          trend={
            recorded
              ? hour(trends.load, "Load", HUE.load, cores)
              : level(loadShare, utilisationTone(loadShare), "Load")
          }
        >
          <span className="numeric">
            <LiveFigure value={snapshot.cpu.loadAvg1} decimals={2} />
            {", "}
            {(snapshot.cpu.loadAvg1 / cores).toFixed(2)}
          </span>{" "}
          a core
        </ReadingLine>
        <ReadingLine
          href="/metrics"
          label="Network on Metrics"
          mark={<Mark hue={HUE.net} glyph={Connection} />}
          name="Network"
          hint={`${rate(tx)} out · ${snapshot.sockets?.tcpInUse ?? 0} TCP sockets`}
          trend={recorded && hour(trends.net, "Network", HUE.net)}
        >
          <span className="numeric">
            <LiveBytes value={rx} suffix="/s" />
          </span>{" "}
          in
        </ReadingLine>
        <ReadingLine
          href="/metrics"
          label="Disk I/O on Metrics"
          mark={<Mark hue={HUE.disk} glyph={Database} />}
          name="Disk I/O"
          hint={`${rate(read)} read · ${rate(write)} write`}
          trend={recorded && hour(trends.disk, "Disk I/O", HUE.disk)}
        >
          <span className="numeric">
            <LiveBytes value={read + write} suffix="/s" />
          </span>
        </ReadingLine>
        {/* A line's own height, so a server with one disk is six cells of one
            height and the bones that stand for them are too. */}
        {shown.length <= 1 && (
          <Filesystems mounts={shown} className="flex min-h-17 flex-col justify-center px-2" />
        )}
      </ReadingLines>
      {shown.length > 1 && (
        <div className="mt-3 space-y-3">
          <Filesystems mounts={shown} list="gap-x-10 gap-y-4 lg:grid-cols-2" />
          {mounts.length > shown.length && (
            <p className="numeric text-hint text-muted-foreground">
              {mounts.length - shown.length} more on Metrics
            </p>
          )}
        </div>
      )}
    </div>
  )
}

/** The fullest few, in the order they are mounted. */
function fullest(mounts: MountStats[]) {
  if (mounts.length <= SHOWN) return mounts
  return [...mounts]
    .sort((a, b) => b.usedPercent - a.usedPercent)
    .slice(0, SHOWN)
    .sort((a, b) => a.mountpoint.localeCompare(b.mountpoint))
}

function Filesystems({
  mounts,
  className,
  list,
}: {
  mounts: MountStats[]
  className?: string
  /** The list's own grid, where there is more than one bar. */
  list?: string
}) {
  return (
    <div data-slot="storage-band" className={cn("min-w-0", className)}>
      {mounts.length === 0 ? (
        <p className="text-body text-muted-foreground">No filesystems reported</p>
      ) : (
        <ul aria-label="Filesystems" className={cn("grid min-w-0", list)}>
          {mounts.map((mount) => (
            <Filesystem key={mount.mountpoint} mount={mount} />
          ))}
        </ul>
      )}
    </div>
  )
}

/** A reading's glyph on its own hue, or on its tone once it is past a line. */
function Mark({ tone = "default", hue, glyph: Glyph }: { tone?: Tone; hue: string; glyph: Icon }) {
  if (tone === "default")
    return (
      <WireMark size="md" hue={hue}>
        <Glyph aria-hidden />
      </WireMark>
    )
  return (
    <WireMark size="md" tone={tone === "danger" ? "danger" : "warning"}>
      <Glyph aria-hidden />
    </WireMark>
  )
}

/**
 * The readings before the first frame of the socket: every line in its place
 * and at its height, so what is under them does not move when it lands.
 */
export function ResourceReadingsSkeleton() {
  return (
    <div data-slot="resources" aria-hidden className="min-w-0">
      <ReadingLines>
        {Array.from({ length: 6 }).map((_, index) => (
          <ReadingLine
            key={index}
            mark={<Skeleton className="size-11 rounded-xl" />}
            name={<Bone className="w-12" />}
            hint={<Bone className="w-40" />}
          >
            <Bone className="w-24" />
          </ReadingLine>
        ))}
      </ReadingLines>
    </div>
  )
}
