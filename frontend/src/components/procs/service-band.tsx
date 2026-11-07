"use client"

import { BarReading, type ReadingPart } from "@/components/bar-reading"
import { timeAt } from "@/components/bar-reading-model"
import { Servers } from "@/components/icons"
import { useNow } from "@/components/deploy/vocabulary"
import { HUE, LiveBytes, LiveFigure } from "@/components/overview/readings"
import { ProductGlyph, unitProduct } from "@/components/product-logo"
import { DOT_COLOR } from "@/components/status-dot"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { bytes, percent, plural } from "@/lib/format"
import type { Snapshot, SystemdUnit } from "@/lib/types"
import { cn } from "@/lib/utils"
import { cores } from "@/components/procs/shared"
import { ago, failureWords, recentChanges, unitShortName } from "@/components/procs/units"

/** How many units each block names. */
const SHOWN = 5

/** Recent's axis, the day it reads. */
const DAY = 86_400_000

/**
 * What the services are doing: the five using the most processor and the
 * most memory, each a span of one bar as wide as the machine, and the last
 * units to start, stop, finish or fail, placed on the last day.
 *
 * It replaced four tiles — active, failed, inactive, enabled on boot — each a
 * count the table's chips now say while narrowing to it. None of them said
 * which service was busy, or what had just happened, which are what a reader
 * opens Services to find out after "is anything failed". The figures are
 * systemd's own, read from each unit's cgroup, so a service's processor and
 * memory are every process it started, children included. They are polled
 * with the list, so neither bar is marked live. A press opens its unit.
 */
export function ServiceBand({
  units,
  snapshot,
  ratesReady,
  bootedAt,
  onOpen,
}: {
  units: SystemdUnit[]
  snapshot?: Snapshot
  ratesReady: boolean
  bootedAt?: number
  onOpen: (unit: SystemdUnit) => void
}) {
  const now = useNow(15_000)
  const byCPU = units
    .filter((u) => u.cpuReady && (u.cpuPercent ?? 0) > 0)
    .sort((a, b) => (b.cpuPercent ?? 0) - (a.cpuPercent ?? 0))
    .slice(0, SHOWN)
  const byMemory = units
    .filter((u) => (u.memoryBytes ?? 0) > 0)
    .sort((a, b) => (b.memoryBytes ?? 0) - (a.memoryBytes ?? 0))
    .slice(0, SHOWN)
  const changes = recentChanges(units, now, bootedAt)
  const sinceBoot = Boolean(bootedAt && now / 1000 - bootedAt < 86_400)

  const coreCount = snapshot?.cpu?.cores || snapshot?.cpu?.perCore?.length || 0
  const busy = snapshot?.cpu ? (snapshot.cpu.totalPercent * coreCount) / 100 : undefined
  const memTotal = snapshot?.memory?.total ?? 0
  const memUsed = snapshot?.memory?.used ?? 0
  const cpuSum = sum(byCPU, (u) => u.cpuPercent ?? 0)
  const memSum = sum(byMemory, (u) => u.memoryBytes ?? 0)

  const line = (u: SystemdUnit): Pick<ReadingPart, "key" | "mark" | "name" | "press"> => {
    const product = unitProduct(u.name)
    return {
      key: u.name,
      mark: product ? (
        <ProductGlyph id={product} className="size-3.5" />
      ) : (
        <Servers aria-hidden className="size-3.5 text-muted-foreground" />
      ),
      name: unitShortName(u.name),
      press: { label: `Open ${u.name}`, opens: true, onPress: () => onOpen(u) },
    }
  }

  return (
    <div
      data-slot="service-band"
      className="grid min-w-0 gap-x-10 gap-y-6 lg:grid-cols-2 xl:grid-cols-3"
    >
      <BarReading
        title="Processor"
        label="Processor by service"
        list="Busiest services"
        hue={HUE.cpu}
        trail
        capacity={coreCount > 0 ? coreCount * 100 : cpuSum}
        rest={busy !== undefined ? Math.max(busy * 100 - cpuSum, 0) : 0}
        format={cores}
        figure={
          coreCount > 0 &&
          busy !== undefined && (
            <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
              <span className="font-medium text-foreground">
                <LiveFigure value={busy} decimals={1} />
              </span>
              of {plural(coreCount, "core")} busy
            </span>
          )
        }
        message={
          !ratesReady ? (
            <TextShimmer>Measuring the first interval…</TextShimmer>
          ) : byCPU.length === 0 ? (
            "No service is using the processor."
          ) : undefined
        }
        parts={byCPU.map((u) => {
          const value = u.cpuPercent ?? 0
          return {
            ...line(u),
            value,
            detail: tasks(u),
            figure:
              value / 100 >= 0.005 ? (
                <LiveFigure value={value / 100} decimals={value >= 99.5 ? 1 : 2} unit=" cores" />
              ) : (
                "idle"
              ),
            label: `${unitShortName(u.name)} ${cores(value)}`,
            title: `${percent(value)} of one core, summed over every process it started`,
          }
        })}
      />

      <BarReading
        title="Memory"
        label="Memory by service"
        list="Services using the most memory"
        hue={HUE.mem}
        trail
        capacity={memTotal > 0 ? memTotal : memSum}
        rest={Math.max(memUsed - memSum, 0)}
        free="Free"
        format={(v) => bytes(v)}
        figure={
          memTotal > 0 && (
            <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
              <span className="font-medium text-foreground">
                <LiveBytes value={memUsed} />
              </span>
              of {bytes(memTotal, 0)} in use
            </span>
          )
        }
        message={byMemory.length === 0 ? "systemd reports no memory for its services." : undefined}
        parts={byMemory.map((u) => {
          const value = u.memoryBytes ?? 0
          return {
            ...line(u),
            value,
            detail: tasks(u),
            figure: <LiveBytes value={value} />,
            label: `${unitShortName(u.name)} ${bytes(value)}`,
            title:
              `${bytes(value)} charged to its cgroup, page cache included` +
              (memTotal > 0 ? ` — ${percent((value / memTotal) * 100)} of the host` : ""),
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
          <span className="numeric flex h-7 items-center text-hint text-muted-foreground">
            {changes.length > 0
              ? `${plural(changes.length, "change")} in the last day`
              : sinceBoot
                ? `booted ${ago(bootedAt!, now)}`
                : "last day"}
          </span>
        }
        message={
          changes.length > 0
            ? undefined
            : `Nothing has started, stopped or failed since ${sinceBoot ? "boot" : "yesterday"}.`
        }
        parts={changes.slice(0, SHOWN + 1).map(({ unit, verb, tone, at }) => ({
          ...line(unit),
          value: 1,
          at: timeAt(at * 1000, now, DAY),
          color: DOT_COLOR[tone],
          // A failure says why on the line, and a unit still on its way
          // somewhere shimmers, because it is happening.
          detail: (
            <span
              className={cn(
                tone === "danger" && "text-destructive",
                tone === "warning" && "text-warning",
              )}
            >
              {tone === "warning" ? (
                <TextShimmer>{verb}</TextShimmer>
              ) : tone === "danger" ? (
                `${verb} · ${failureWords(unit)}`
              ) : (
                verb
              )}
            </span>
          ),
          figure: (
            <span className="text-hint font-normal text-muted-foreground">{ago(at, now)}</span>
          ),
          label: `${unitShortName(unit.name)} ${verb} ${ago(at, now)}`,
          title: new Date(at * 1000).toLocaleString(),
        }))}
      />
    </div>
  )
}

function sum<T>(list: T[], value: (item: T) => number) {
  return list.reduce((total, item) => total + value(item), 0)
}

function tasks(unit: SystemdUnit) {
  if (!unit.tasks) return undefined
  return unit.tasks > 1 ? `${unit.tasks} tasks` : `PID ${unit.mainPid}`
}
