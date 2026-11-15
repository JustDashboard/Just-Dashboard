"use client"

import { useRef, useState } from "react"
import {
  ArrowLeftRight,
  Connection,
  GridSquare,
  Layers,
  NetworkDevice,
  Router,
  SecureConnection,
  Servers,
} from "@/components/icons"
import { get } from "@/lib/api"
import { cn } from "@/lib/utils"
import { bytes, percent, rate } from "@/lib/format"
import type { DirEntry, NetStats, SensorReading, Snapshot } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import type { Tone } from "@/components/tone"
import { BarReading } from "@/components/bar-reading"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { Meter, utilisationTone } from "@/components/meter"
import { EmptyState } from "@/components/state"
import { Tag } from "@/components/tag"
import { Filesystem } from "@/components/overview/storage"
import { SeriesKey } from "@/components/overview/readings"
import {
  ProductGlyph,
  ProductLogo,
  interfaceProduct,
  sensorProduct,
} from "@/components/product-logo"
import { Button } from "@/components/ui/button"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

/**
 * The parts of the machine, read from the newest frame.
 *
 * Everything here is live-only: per-core load, how memory is split, sensor
 * temperatures, mounts and interfaces are all in the socket's snapshot and
 * none of them survive a downsampled chart, so these are readings rather than
 * series. Plain panels, every one — a title and a hairline on the page's own
 * ground — each set beside the chart of the same resource, so the newest frame
 * and its history are read side by side.
 */

/** A figure in its tone: amber or red past the line, the ink otherwise. */
function toneText(tone: Tone) {
  return tone === "danger"
    ? "text-destructive"
    : tone === "warning"
      ? "text-warning"
      : "text-muted-foreground"
}

/** The fill of a column or a segment: the series colour until it crosses a line. */
function toneFill(tone: Tone, color: string) {
  return tone === "danger" ? "var(--destructive)" : tone === "warning" ? "var(--warning)" : color
}

/** Past this many, the cores wrap into rows and each one's figure goes to its title. */
const CORE_ROW = 16

/**
 * Every core as a column, filled to its share and gliding to each frame.
 *
 * A row of thin bars with a figure at each end read as a table of forty
 * numbers; a run of columns reads as one shape — eight even ones, or one tall
 * and seven short — before a figure is read, which is the question this
 * answers. The columns take the processor's series colour, so the shape is
 * the CPU chart beside it seen core by core, and turn amber and red at the
 * same lines every utilisation figure does. The track under a column is a
 * tint of the same colour: on a grey one an idle machine's cores read as four
 * grey slabs, the one uncoloured block on the page.
 */
export function PerCorePanel({ cores, color }: { cores: number[]; color: string }) {
  if (cores.length === 0) return null
  const busiest = cores.reduce((top, share, index) => (share > cores[top] ? index : top), 0)
  const mean = cores.reduce((sum, share) => sum + share, 0) / cores.length
  const labelled = cores.length <= CORE_ROW
  return (
    <Panel plain>
      <PanelHeader
        title="Cores"
        actions={
          // The utilisation chart's control height, so the two hairlines meet.
          <span className="numeric flex h-8 items-center text-hint text-muted-foreground">
            mean {percent(mean, 0)} · busiest cpu{busiest} at {percent(cores[busiest], 0)}
          </span>
        }
      />
      <PanelBody className="flex flex-1 flex-col justify-center">
        <ul
          aria-label="Cores"
          className="grid gap-x-1.5 gap-y-3"
          style={{
            gridTemplateColumns: `repeat(${Math.min(cores.length, CORE_ROW)}, minmax(0, 1fr))`,
          }}
        >
          {cores.map((share, index) => {
            const tone = utilisationTone(share)
            const level = Math.min(Math.max(share, 0), 100)
            return (
              <li
                key={index}
                title={`cpu${index} ${percent(share, 0)}`}
                className="flex min-w-0 flex-col items-center gap-1.5"
              >
                {labelled && (
                  <span className={cn("numeric text-micro", toneText(tone))}>
                    {share.toFixed(0)}%
                  </span>
                )}
                <span
                  role="meter"
                  aria-label={`cpu${index}`}
                  aria-valuenow={Math.round(level)}
                  aria-valuemin={0}
                  aria-valuemax={100}
                  className={cn(
                    "relative w-full max-w-9 overflow-hidden rounded-sm",
                    labelled ? "h-28" : "h-14",
                  )}
                  style={{ background: `color-mix(in oklab, ${color} 16%, transparent)` }}
                >
                  <span
                    className="absolute inset-x-0 bottom-0 transition-[height] duration-700 ease-out"
                    style={{ height: `${level}%`, background: toneFill(tone, color) }}
                  />
                </span>
                {labelled && (
                  <span className="font-mono text-micro text-muted-foreground">{index}</span>
                )}
              </li>
            )
          })}
        </ul>
      </PanelBody>
    </Panel>
  )
}

/**
 * Sensor temperatures, hottest first, each against its own limit.
 *
 * Absent on most virtual servers, and drawn only when the host reports at
 * least one: a panel saying "no sensors" would be a box on every VPS
 * explaining a feature it cannot have. A sensor whose driver names the
 * silicon it reads — Intel's `coretemp`, AMD's `k10temp` — carries that
 * vendor's mark, as the processor does in the identity line.
 */
export function SensorsPanel({ sensors }: { sensors: SensorReading[] }) {
  if (sensors.length === 0) return null
  return (
    <Panel plain>
      <PanelHeader
        title="Temperatures"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {sensors.length} sensor{sensors.length === 1 ? "" : "s"}
          </span>
        }
      />
      <PanelBody>
        <ul aria-label="Temperatures" className="grid gap-x-8 gap-y-2.5 md:grid-cols-2">
          {sensors.map((s) => {
            // The bar fills towards the sensor's own critical mark where it has
            // one, so two sensors with different ceilings are comparable by
            // how much room each has left rather than by raw degrees.
            const ceiling = s.critical > 0 ? s.critical : s.high > 0 ? s.high : 100
            const tone = sensorTone(s)
            const product = sensorProduct(s.name)
            return (
              <li key={s.name} className="min-w-0 space-y-1.5">
                <div className="flex min-w-0 items-baseline justify-between gap-3">
                  <span className="flex min-w-0 items-center gap-1.5 text-hint">
                    {product && <ProductGlyph id={product} />}
                    <span className="truncate font-mono text-muted-foreground" title={s.name}>
                      {s.name}
                    </span>
                  </span>
                  <span
                    className={cn(
                      "numeric shrink-0 text-body font-medium",
                      tone === "default" ? "text-foreground" : toneText(tone),
                    )}
                  >
                    {s.tempC.toFixed(0)}°C
                  </span>
                </div>
                <Meter
                  value={(s.tempC / ceiling) * 100}
                  tone={tone}
                  size="thin"
                  label={s.name}
                  mark={s.high > 0 && s.critical > 0 ? (s.high / s.critical) * 100 : undefined}
                />
              </li>
            )
          })}
        </ul>
      </PanelBody>
    </Panel>
  )
}

export function sensorTone(s: SensorReading): Tone {
  if (s.critical > 0 && s.tempC >= s.critical) return "danger"
  if (s.high > 0 && s.tempC >= s.high) return "warning"
  return "default"
}

/**
 * Where the memory is, as one bar: what programs hold and what the kernel is
 * caching and would give back, on a track that is the whole of it — then swap
 * as a bar of its own under it.
 *
 * "56% used" says nothing a reader can act on, because on Linux the cache
 * grows into whatever is free and is handed back the moment a program asks.
 * Split, the bar says how much of the "used" is really taken, which is the
 * figure the Memory tile's *available* is made of, and the head says that
 * figure. The spans take the memory chart's colour, the cache at a lighter
 * step of it, so the bar is the newest point of the line beside it opened up.
 *
 * Both are `BarReading`s. They were a bar with three figures in a row under
 * it and a meter under those: the shape the bands on every other page left,
 * where what is under the bar is one line that reads the part pointed at.
 */
export function MemoryPanel({
  memory,
  swap,
  color,
  swapColor,
}: {
  memory: Snapshot["memory"]
  swap: Snapshot["swap"]
  color: string
  swapColor: string
}) {
  const total = Math.max(memory.total, 1)
  const cache = memory.cached + memory.buffers
  const share = (value: number) => percent((value / total) * 100, 0)
  const tone: Tone =
    memory.total > 0 && memory.available / memory.total <= 0.05
      ? "danger"
      : memory.total > 0 && memory.available / memory.total <= 0.1
        ? "warning"
        : "default"

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <BarReading
        title="Allocation"
        label="Memory by what holds it"
        list="What holds the memory"
        fixed
        capacity={memory.total}
        free="Free"
        format={(value) => `${bytes(value)}, ${share(value)}`}
        figure={
          <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
            <span
              className={cn("font-medium", tone === "default" ? "text-foreground" : toneText(tone))}
            >
              {bytes(memory.available)}
            </span>
            available of {bytes(memory.total, 0)}
          </span>
        }
        parts={[
          {
            key: "used",
            value: memory.used,
            color,
            mark: (
              <span className="flex" style={{ color }}>
                <GridSquare aria-hidden className="size-3.5" />
              </span>
            ),
            name: "Programs",
            detail: `${share(memory.used)} of memory, held by what is running`,
            figure: bytes(memory.used),
            label: `Programs ${bytes(memory.used)}`,
          },
          {
            key: "cache",
            value: cache,
            color: `color-mix(in oklab, ${color} 42%, transparent)`,
            mark: <Layers aria-hidden className="size-3.5 text-muted-foreground" />,
            name: "Cache",
            detail: `${share(cache)} of memory, given back when a program asks`,
            figure: bytes(cache),
            label: `Cache ${bytes(cache)}`,
          },
        ]}
      />
      <BarReading
        title="Swap"
        label="Swap in use"
        list="Swap"
        fixed
        capacity={swap.total}
        free="Free"
        format={(value) => bytes(value)}
        figure={
          swap.total > 0 && (
            <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
              <span className="font-medium text-foreground">{bytes(swap.used)}</span>
              of {bytes(swap.total, 0)}
            </span>
          )
        }
        message={swap.total > 0 ? undefined : "No swap is configured on this server."}
        parts={
          swap.total > 0
            ? [
                {
                  key: "swap",
                  value: swap.used,
                  color: swapColor,
                  mark: (
                    <span className="flex" style={{ color: swapColor }}>
                      <ArrowLeftRight aria-hidden className="size-3.5" />
                    </span>
                  ),
                  name: "In use",
                  detail: `${percent(swap.usedPercent, 0)} of swap, pages moved out of memory`,
                  figure: bytes(swap.used),
                  label: `Swap in use ${bytes(swap.used)}`,
                },
              ]
            : []
        }
      />
    </div>
  )
}

/**
 * Every filesystem as the Overview draws its disks — name and free space, a
 * wide bar, what it is and what it is doing — with the scan that answers
 * "what is taking the space" beside the name.
 */
export function MountsPanel({ snapshot }: { snapshot: Snapshot }) {
  const [scanning, setScanning] = useState<string | null>(null)
  const [breakdown, setBreakdown] = useState<Record<string, DirEntry[]>>({})
  const abortRef = useRef<AbortController>(null)

  const scan = async (mountpoint: string) => {
    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller
    setScanning(mountpoint)
    try {
      const entries = await get<DirEntry[]>(
        "/system/disk-usage",
        { path: mountpoint, limit: 12 },
        controller.signal,
      )
      setBreakdown((prev) => ({ ...prev, [mountpoint]: entries }))
    } catch {
      // A scan that runs out of time answers with what it counted, each entry it
      // did not finish said to be "at least"; what still fails here — a directory
      // of more than 10,000 entries, two scans already running — is not worth a
      // toast, and the row simply stays un-expanded.
    } finally {
      setScanning(null)
    }
  }

  return (
    <Panel plain>
      <PanelHeader
        title="Filesystems"
        actions={
          <span className="numeric text-hint text-muted-foreground">{snapshot.mounts.length}</span>
        }
      />
      <PanelBody>
        {snapshot.mounts.length === 0 ? (
          <EmptyState title="No filesystems reported" icon={Servers} />
        ) : (
          <ul aria-label="Filesystems" className="grid min-w-0 gap-y-5">
            {snapshot.mounts.map((mount) => {
              const inodes =
                mount.inodesTotal > 0 ? (mount.inodesUsed / mount.inodesTotal) * 100 : 0
              return (
                <Filesystem
                  key={mount.mountpoint}
                  mount={mount}
                  action={
                    <Button
                      size="xs"
                      variant="ghost"
                      className="-my-1 text-muted-foreground"
                      disabled={scanning !== null}
                      onClick={() => scan(mount.mountpoint)}
                    >
                      {scanning === mount.mountpoint ? "scanning…" : "scan"}
                    </Button>
                  }
                >
                  {/* Inodes fill independently of bytes, so a volume the bar
                      calls two-thirds empty can still refuse to create a
                      file. Said here, under the bar that is about to be
                      wrong, rather than only in the Inodes chart. */}
                  {inodes >= 80 && (
                    <p className="numeric text-hint font-medium text-warning">
                      {inodes.toFixed(0)}% of inodes used
                    </p>
                  )}
                  {breakdown[mount.mountpoint] && (
                    // Output you read — the scan's answer — so it takes the well
                    // the rest of the product uses for one.
                    <Well plain className="space-y-1 p-2">
                      {breakdown[mount.mountpoint].map((entry) => (
                        <div key={entry.path} className="flex justify-between gap-2 text-hint">
                          <span className="truncate font-mono">{entry.name}</span>
                          <span
                            className="numeric shrink-0 text-muted-foreground"
                            title={
                              entry.partial
                                ? "The scan ran out of time before it had counted all of it"
                                : undefined
                            }
                          >
                            {entry.partial && "at least "}
                            {bytes(entry.size)}
                          </span>
                        </div>
                      ))}
                    </Well>
                  )}
                </Filesystem>
              )
            })}
          </ul>
        )}
      </PanelBody>
    </Panel>
  )
}

/** A device Docker made for its containers, rather than one carrying the host's own traffic. */
const VIRTUAL: NetStats["kind"][] = ["virtual", "bridge"]

/** The glyph for an interface no product owns, by what kind of device it is. */
const KIND_GLYPH = {
  physical: NetworkDevice,
  tunnel: SecureConnection,
  bridge: Router,
  virtual: Connection,
  loopback: Connection,
} as const

export function InterfacesPanel({
  snapshot,
  colors,
}: {
  snapshot: Snapshot
  /** The network chart's in and out, so a row's bar is read in the chart's colours. */
  colors: { in: string; out: string }
}) {
  const [scope, setScope] = useViewState<"real" | "all">("metrics.interfaces.scope", "real")
  // Docker makes a veth pair per container and a bridge per network, so a host
  // running a dozen containers drew thirty rows here, the page's longest block
  // by far, with the uplink somewhere in the middle. The Network page's answer:
  // the real devices by default, everything one press away.
  const interfaces =
    scope === "all" ? snapshot.net : snapshot.net.filter((n) => !VIRTUAL.includes(n.kind))
  // Each row's share of the busiest interface's traffic, so the uplink is
  // findable as the long bar rather than by comparing figures down a column.
  const busiest = Math.max(1, ...interfaces.map((n) => n.recvRate + n.sendRate))

  return (
    <Panel plain>
      <PanelHeader
        title="Interfaces"
        actions={
          <div className="flex items-center gap-3">
            <span className="numeric text-hint text-muted-foreground">
              {interfaces.filter((n) => n.isUp).length} up
            </span>
            <ToggleGroup
              type="single"
              value={scope}
              onValueChange={(next) => next && setScope(next as "real" | "all")}
              variant="outline"
              size="sm"
              aria-label="Which interfaces to show"
            >
              <ToggleGroupItem value="real" className="px-2 text-hint">
                Real devices
              </ToggleGroupItem>
              <ToggleGroupItem value="all" className="px-2 text-hint">
                Everything {snapshot.net.length}
              </ToggleGroupItem>
            </ToggleGroup>
          </div>
        }
      />
      <PanelBody>
        {interfaces.length === 0 ? (
          <EmptyState title="No interfaces reported" icon={Servers} />
        ) : (
          // Two to a row, as the charts above them are: "Everything" on a host
          // running containers is thirty devices, which as one column was the
          // page's longest block, and the table that held them had to scroll
          // inside a frame of its own — the one box on a page of readings.
          <ul aria-label="Interfaces" className="grid min-w-0 gap-x-10 gap-y-5 lg:grid-cols-2">
            {interfaces.map((iface) => (
              <Interface key={iface.interface} iface={iface} busiest={busiest} colors={colors} />
            ))}
          </ul>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * One interface in the order Filesystems draws a disk: which one and what it
 * is carrying, the bar, then where it answers and what it has lost. The two
 * rates are keyed by the bar's colours, as a chart's head keys its figures.
 */
function Interface({
  iface,
  busiest,
  colors,
}: {
  iface: NetStats
  busiest: number
  colors: { in: string; out: string }
}) {
  const errors = iface.errIn + iface.errOut
  const drops = iface.dropIn + iface.dropOut
  return (
    <li className="flex min-w-0 items-center gap-3">
      <ProductLogo
        id={interfaceProduct(iface.interface)}
        size="sm"
        fallback={KIND_GLYPH[iface.kind] ?? NetworkDevice}
      />
      <div className="min-w-0 flex-1 space-y-1.5">
        <div className="flex min-w-0 items-baseline justify-between gap-3">
          <span className="flex min-w-0 items-center gap-2">
            <span
              role="img"
              aria-label={iface.isUp ? "up" : "down"}
              className={cn(
                "size-1.5 shrink-0 rounded-full",
                iface.isUp ? "bg-success" : "bg-muted-foreground",
              )}
            />
            <span className="truncate font-mono text-body font-medium">{iface.interface}</span>
            <Tag>{iface.kind}</Tag>
          </span>
          <span className="numeric flex shrink-0 gap-x-3 text-hint text-muted-foreground">
            <span className="whitespace-nowrap">
              <SeriesKey color={colors.in} />
              <span className="font-medium text-foreground">{rate(iface.recvRate)}</span> in
            </span>
            <span className="whitespace-nowrap">
              <SeriesKey color={colors.out} />
              <span className="font-medium text-foreground">{rate(iface.sendRate)}</span> out
            </span>
          </span>
        </div>
        <TrafficBar iface={iface} busiest={busiest} colors={colors} />
        <div className="numeric flex min-w-0 justify-between gap-3 text-hint text-muted-foreground">
          <span className="truncate font-mono" title={iface.addrs.join(", ")}>
            {iface.addrs.join(", ") || "no address"}
          </span>
          {/* Drops beside errors: a link that is up, error-free and dropping
              packets is a queue that is too short, not a fault. */}
          <span className="shrink-0">
            <span className={cn(errors > 0 && "font-medium text-warning")}>
              {errors.toLocaleString()} errors
            </span>
            {" · "}
            <span className={cn(drops > 0 && "font-medium text-warning")}>
              {drops.toLocaleString()} drops
            </span>
          </span>
        </div>
      </div>
    </li>
  )
}

/**
 * In and out as one bar against the busiest interface, in the network chart's
 * two colours: the uplink is the long bar, and which way its traffic runs is
 * the split of it. A container's interfaces are drawn the same way on its
 * Usage tab, so it takes only the two rates.
 */
export function TrafficBar({
  iface,
  busiest,
  colors,
}: {
  iface: Pick<NetStats, "recvRate" | "sendRate">
  busiest: number
  colors: { in: string; out: string }
}) {
  return (
    <SplitBar
      label={`${rate(iface.recvRate)} in, ${rate(iface.sendRate)} out`}
      busiest={busiest}
      parts={[
        { value: iface.recvRate, color: colors.in },
        { value: iface.sendRate, color: colors.out },
      ]}
    />
  )
}

/** Two sides of one quantity as one bar, against the largest of the bars beside it. */
export function SplitBar({
  label,
  busiest,
  parts,
}: {
  label: string
  busiest: number
  parts: { value: number; color: string }[]
}) {
  return (
    <div
      role="img"
      aria-label={label}
      className="flex h-1.5 w-full min-w-16 overflow-hidden rounded-full bg-meter-track"
    >
      {parts.map((part, index) => (
        <span
          key={index}
          className="h-full transition-[width] duration-700 ease-out"
          style={{ width: `${(part.value / busiest) * 100}%`, background: part.color }}
        />
      ))}
    </div>
  )
}
