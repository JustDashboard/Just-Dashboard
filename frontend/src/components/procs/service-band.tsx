"use client"

import { ArrowRight, Servers } from "@/components/icons"
import { rowReveal } from "@/components/icon-action"
import { useNow } from "@/components/deploy/vocabulary"
import { HUE, LiveBytes, LiveFigure } from "@/components/overview/readings"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph, unitProduct } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { bytes, percent, plural } from "@/lib/format"
import type { Snapshot, SystemdUnit } from "@/lib/types"
import { cn } from "@/lib/utils"
import { cores } from "@/components/procs/shared"
import { ShareBar, shade } from "@/components/procs/workloads"
import {
  ago,
  failureWords,
  recentChanges,
  unitShortName,
  type UnitChange,
} from "@/components/procs/units"

/** How many units each block names. */
const SHOWN = 5

/**
 * What the services are doing: the five using the most processor and the
 * most memory, each a span of one bar as wide as the machine, and the last
 * units to start, stop, finish or fail.
 *
 * It replaced four tiles — active, failed, inactive, enabled on boot — each a
 * count the table's chips now say while narrowing to it. None of them said
 * which service was busy, or what had just happened, which are what a reader
 * opens Services to find out after "is anything failed". The figures are
 * systemd's own, read from each unit's cgroup, so a service's processor and
 * memory are every process it started, children included. Each span eases to
 * the next poll and each figure glides to it; a row opens its unit.
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

  const coreCount = snapshot?.cpu?.cores || snapshot?.cpu?.perCore?.length || 0
  const busy = snapshot?.cpu ? (snapshot.cpu.totalPercent * coreCount) / 100 : undefined
  const memTotal = snapshot?.memory?.total ?? 0
  const memUsed = snapshot?.memory?.used ?? 0
  const cpuSum = sum(byCPU, (u) => u.cpuPercent ?? 0)
  const memSum = sum(byMemory, (u) => u.memoryBytes ?? 0)

  return (
    <div
      data-slot="service-band"
      className="grid min-w-0 gap-x-10 gap-y-6 lg:grid-cols-2 xl:grid-cols-3"
    >
      <Panel plain aria-label="Processor by service">
        <PanelHeader
          title="Processor"
          actions={
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
        />
        <PanelBody className="space-y-3 pt-4">
          <ShareBar
            label="Processor"
            capacity={coreCount > 0 ? coreCount * 100 : cpuSum}
            rest={busy !== undefined ? Math.max(busy * 100 - cpuSum, 0) : 0}
            parts={byCPU.map((u, rank) => ({
              key: u.name,
              value: u.cpuPercent ?? 0,
              color: shade(HUE.cpu, rank),
              label: `${unitShortName(u.name)} ${cores(u.cpuPercent ?? 0)}`,
            }))}
            format={cores}
          />
          {!ratesReady ? (
            <p className="py-2 text-body">
              <TextShimmer>Measuring the first interval…</TextShimmer>
            </p>
          ) : byCPU.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">
              No service is using the processor.
            </p>
          ) : (
            <ul className="-mx-2">
              {byCPU.map((u, rank) => (
                <UnitLine
                  key={u.name}
                  unit={u}
                  onOpen={onOpen}
                  lead={<RankKey color={shade(HUE.cpu, rank)} />}
                  detail={tasks(u)}
                  title={`${percent(u.cpuPercent ?? 0)} of one core, summed over every process it started`}
                  figure={
                    (u.cpuPercent ?? 0) / 100 >= 0.005 ? (
                      <LiveFigure
                        value={(u.cpuPercent ?? 0) / 100}
                        decimals={(u.cpuPercent ?? 0) >= 99.5 ? 1 : 2}
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

      <Panel plain aria-label="Memory by service">
        <PanelHeader
          title="Memory"
          actions={
            memTotal > 0 && (
              <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
                <span className="font-medium text-foreground">
                  <LiveBytes value={memUsed} />
                </span>
                of {bytes(memTotal, 0)} in use
              </span>
            )
          }
        />
        <PanelBody className="space-y-3 pt-4">
          <ShareBar
            label="Memory"
            capacity={memTotal > 0 ? memTotal : memSum}
            rest={Math.max(memUsed - memSum, 0)}
            parts={byMemory.map((u, rank) => ({
              key: u.name,
              value: u.memoryBytes ?? 0,
              color: shade(HUE.mem, rank),
              label: `${unitShortName(u.name)} ${bytes(u.memoryBytes ?? 0)}`,
            }))}
            format={(v) => bytes(v)}
          />
          {byMemory.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">
              systemd reports no memory for its services.
            </p>
          ) : (
            <ul className="-mx-2">
              {byMemory.map((u, rank) => (
                <UnitLine
                  key={u.name}
                  unit={u}
                  onOpen={onOpen}
                  lead={<RankKey color={shade(HUE.mem, rank)} />}
                  detail={tasks(u)}
                  title={
                    `${bytes(u.memoryBytes ?? 0)} charged to its cgroup, page cache included` +
                    (memTotal > 0
                      ? ` — ${percent(((u.memoryBytes ?? 0) / memTotal) * 100)} of the host`
                      : "")
                  }
                  figure={<LiveBytes value={u.memoryBytes ?? 0} />}
                />
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>

      <Panel plain aria-label="Recent changes" className="lg:col-span-2 xl:col-span-1">
        <PanelHeader
          title="Recent"
          actions={
            <span className="numeric flex h-7 items-center text-hint text-muted-foreground">
              {changes.length > 0
                ? `${plural(changes.length, "change")} in the last day`
                : bootedAt && now / 1000 - bootedAt < 86_400
                  ? `booted ${ago(bootedAt, now)}`
                  : "last day"}
            </span>
          }
        />
        <PanelBody className="pt-3">
          {changes.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">
              Nothing has started, stopped or failed since{" "}
              {bootedAt && now / 1000 - bootedAt < 86_400 ? "boot" : "yesterday"}.
            </p>
          ) : (
            <ul className="-mx-2">
              {changes.slice(0, SHOWN + 1).map((change) => (
                <ChangeLine key={change.unit.name} change={change} now={now} onOpen={onOpen} />
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

function tasks(unit: SystemdUnit) {
  if (!unit.tasks) return undefined
  return unit.tasks > 1 ? `${unit.tasks} tasks` : `PID ${unit.mainPid}`
}

/** The span's key: the rank's step of its measurement's hue, so a row finds itself on the bar. */
function RankKey({ color }: { color: string }) {
  return (
    <span aria-hidden className="h-2.5 w-0.5 shrink-0 rounded-full" style={{ background: color }} />
  )
}

/**
 * One unit on a line: what finds it on the bar or in time, its mark and
 * name, a detail, and its figure. A press opens the unit; the arrow is what
 * says so, revealed under the pointer and always drawn on a touch screen.
 */
function UnitLine({
  unit,
  lead,
  detail,
  figure,
  title,
  onOpen,
}: {
  unit: SystemdUnit
  lead: React.ReactNode
  detail?: React.ReactNode
  figure: React.ReactNode
  title?: string
  onOpen: (unit: SystemdUnit) => void
}) {
  const product = unitProduct(unit.name)
  return (
    <li>
      <button
        type="button"
        aria-label={`Open ${unit.name}`}
        title={title}
        onClick={() => onOpen(unit)}
        className="group flex h-8 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body focus-ring-inset transition-colors hover:bg-row-hover"
      >
        {lead}
        <span className="flex size-4 shrink-0 items-center justify-center">
          {product ? (
            <ProductGlyph id={product} className="size-3.5" />
          ) : (
            <Servers aria-hidden className="size-3.5 text-muted-foreground" />
          )}
        </span>
        <span className="min-w-0 truncate font-medium">{unitShortName(unit.name)}</span>
        {detail && (
          <span className="numeric shrink-0 truncate text-hint text-muted-foreground">
            {detail}
          </span>
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
 * A recent change: the state's dot, the unit, what happened and when. A
 * failure says why in the same line, and a unit still on its way somewhere
 * shimmers, because it is happening.
 */
function ChangeLine({
  change,
  now,
  onOpen,
}: {
  change: UnitChange
  now: number
  onOpen: (unit: SystemdUnit) => void
}) {
  const { unit, verb, tone } = change
  return (
    <UnitLine
      unit={unit}
      onOpen={onOpen}
      lead={<StatusDot tone={tone} className="mx-px" />}
      title={new Date(change.at * 1000).toLocaleString()}
      detail={
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
      }
      figure={
        <span className="text-hint font-normal text-muted-foreground">{ago(change.at, now)}</span>
      }
    />
  )
}
