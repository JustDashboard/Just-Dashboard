"use client"

import { useRef, useState } from "react"
import { Connection, NetworkDevice, Router, SecureConnection, Servers } from "@/components/icons"
import { get } from "@/lib/api"
import { cn } from "@/lib/utils"
import { bytes, percent, rate } from "@/lib/format"
import type { DirEntry, NetStats, SensorReading, Snapshot } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import type { Tone } from "@/components/tone"
import { Panel, PanelBody, PanelHeader, PanelToolbar, Well } from "@/components/panel"
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
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/**
 * The parts of the machine, read from the newest frame.
 *
 * Everything here is live-only: per-core load, how memory is split, sensor
 * temperatures, mounts and interfaces are all in the socket's snapshot and
 * none of them survive a downsampled chart, so these are readings rather than
 * series. Plain panels, every one but the interfaces table — a title and a
 * hairline on the page's own ground — each set beside the chart of the same
 * resource, so the newest frame and its history are read side by side.
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
 * same lines every utilisation figure does.
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
                    "relative w-full max-w-9 overflow-hidden rounded-sm bg-meter-track/60",
                    labelled ? "h-28" : "h-14",
                  )}
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
 * Where the memory is, as one bar: what programs hold, what the kernel is
 * caching and would give back, and what is untouched — then swap under it.
 *
 * "56% used" says nothing a reader can act on, because on Linux the cache
 * grows into whatever is free and is handed back the moment a program asks.
 * Split, the bar says how much of the "used" is really taken, which is the
 * figure the Memory tile's *available* is made of. The segments take the
 * memory chart's colour, the cache at a lighter step of it, so the bar is the
 * newest point of the line beside it opened up.
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
  const free = Math.max(memory.total - memory.used - cache, 0)
  const cacheColor = `color-mix(in oklab, ${color} 42%, transparent)`
  const parts = [
    { key: "used", label: "Programs", value: memory.used, color },
    { key: "cache", label: "Cache", value: cache, color: cacheColor },
    { key: "free", label: "Free", value: free, color: "var(--meter-track)" },
  ]
  const tone: Tone =
    memory.total > 0 && memory.available / memory.total <= 0.05
      ? "danger"
      : memory.total > 0 && memory.available / memory.total <= 0.1
        ? "warning"
        : "default"

  return (
    <Panel plain>
      <PanelHeader
        title="Allocation"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            <span
              className={cn("font-medium", tone === "default" ? "text-foreground" : toneText(tone))}
            >
              {bytes(memory.available)}
            </span>{" "}
            available of {bytes(memory.total, 0)}
          </span>
        }
      />
      <PanelBody className="flex flex-col gap-6 pt-5">
        <div className="space-y-4">
          <div className="space-y-1.5">
            {/* What the kernel would hand a program that asked now, drawn
                over the bar as the span it is: the free end and most of the
                cache. It is why "56% used" and "6.5 GB available" are both
                true. */}
            <div className="relative h-4" aria-hidden>
              <span
                className="absolute inset-y-0 right-0 flex items-start justify-center border-x border-t border-muted-foreground/40 transition-[left] duration-700 ease-out"
                style={{ left: `${Math.max(0, 100 - (memory.available / total) * 100)}%` }}
              >
                <span className="-mt-2 bg-background px-1.5 text-micro text-muted-foreground">
                  available
                </span>
              </span>
            </div>
            <div
              role="img"
              aria-label={parts.map((p) => `${p.label} ${bytes(p.value)}`).join(", ")}
              className="flex h-4 w-full overflow-hidden rounded-md bg-meter-track"
            >
              {parts.slice(0, 2).map((part) => (
                <span
                  key={part.key}
                  className="h-full transition-[width] duration-700 ease-out"
                  style={{ width: `${(part.value / total) * 100}%`, background: part.color }}
                />
              ))}
            </div>
          </div>
          <dl className="grid grid-cols-3 gap-4">
            {parts.map((part) => (
              <div key={part.key} className="min-w-0 space-y-0.5">
                <dt className="eyebrow truncate">
                  <SeriesKey color={part.color} />
                  {part.label}
                </dt>
                <dd className="numeric text-title font-semibold tracking-tight">
                  {bytes(part.value)}
                </dd>
                <dd className="numeric text-hint text-muted-foreground">
                  {percent((part.value / total) * 100, 0)}
                </dd>
              </div>
            ))}
          </dl>
        </div>

        <div className="space-y-2 border-t border-hairline pt-4">
          <div className="flex min-w-0 items-baseline justify-between gap-3">
            <span className="eyebrow">
              <SeriesKey color={swapColor} />
              Swap
            </span>
            <span className="numeric text-hint text-muted-foreground">
              {swap.total > 0 ? (
                <>
                  <span className="font-medium text-foreground">{bytes(swap.used)}</span> of{" "}
                  {bytes(swap.total, 0)}
                </>
              ) : (
                "none configured"
              )}
            </span>
          </div>
          {swap.total > 0 && (
            <div
              role="meter"
              aria-label="Swap used"
              aria-valuenow={Math.round(swap.usedPercent)}
              aria-valuemin={0}
              aria-valuemax={100}
              className="h-1.5 w-full overflow-hidden rounded-full bg-meter-track"
            >
              <span
                className="block h-full rounded-full transition-[width] duration-700 ease-out"
                style={{ width: `${Math.min(swap.usedPercent, 100)}%`, background: swapColor }}
              />
            </div>
          )}
        </div>
      </PanelBody>
    </Panel>
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
      // A scan that times out or hits an unreadable tree is not worth a toast;
      // the row simply stays un-expanded.
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
                          <span className="numeric shrink-0 text-muted-foreground">
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
    <Panel>
      <PanelHeader
        title="Interfaces"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {interfaces.filter((n) => n.isUp).length} up
          </span>
        }
      />
      <PanelToolbar>
        <ToggleGroup
          type="single"
          value={scope}
          onValueChange={(next) => next && setScope(next as "real" | "all")}
          variant="outline"
          size="sm"
          aria-label="Which interfaces to show"
        >
          <ToggleGroupItem value="real" className="px-2.5 text-hint">
            Real devices
          </ToggleGroupItem>
          <ToggleGroupItem value="all" className="px-2.5 text-hint">
            Everything {snapshot.net.length}
          </ToggleGroupItem>
        </ToggleGroup>
      </PanelToolbar>
      {/* The bleed is plain-only: inside this panel's frame the outer columns
          take the gutter from their own cell padding instead, which lands them
          in the title's column either way (§2). */}
      <PanelBody flush className="group-data-[plain]/panel:-mx-4">
        {/* Capped so "Everything" scrolls inside the frame rather than
            stretching the Network section far past the charts above it. */}
        <Table containerClassName="max-h-[26rem]">
          <TableHeader className={stickyTableHeader}>
            <TableRow>
              <TableHead>Interface</TableHead>
              <TableHead className="w-[28%]">Traffic</TableHead>
              <TableHead className="text-right">In</TableHead>
              <TableHead className="text-right">Out</TableHead>
              {/* Drops next to errors: a link that is up, error-free and
                  dropping packets is a queue that is too short, not a fault. */}
              <TableHead className="text-right">Errors</TableHead>
              <TableHead className="text-right">Drops</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {interfaces.map((iface) => (
              <TableRow key={iface.interface}>
                <TableCell>
                  <div className="flex min-w-0 items-center gap-3">
                    <ProductLogo
                      id={interfaceProduct(iface.interface)}
                      size="sm"
                      fallback={KIND_GLYPH[iface.kind] ?? NetworkDevice}
                    />
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <span
                          aria-label={iface.isUp ? "up" : "down"}
                          className={cn(
                            "size-1.5 shrink-0 rounded-full",
                            iface.isUp ? "bg-success" : "bg-muted-foreground",
                          )}
                        />
                        <span className="font-mono text-xs">{iface.interface}</span>
                        <Tag>{iface.kind}</Tag>
                      </div>
                      <p
                        className="max-w-[16rem] truncate font-mono text-hint text-muted-foreground"
                        title={iface.addrs.join(", ")}
                      >
                        {iface.addrs.join(", ") || "no address"}
                      </p>
                    </div>
                  </div>
                </TableCell>
                <TableCell>
                  <TrafficBar iface={iface} busiest={busiest} colors={colors} />
                </TableCell>
                <TableCell className="numeric text-right font-mono">
                  {rate(iface.recvRate)}
                </TableCell>
                <TableCell className="numeric text-right font-mono">
                  {rate(iface.sendRate)}
                </TableCell>
                <TableCell
                  className={cn(
                    "numeric text-right font-mono text-xs",
                    iface.errIn + iface.errOut > 0 ? "text-warning" : "text-muted-foreground",
                  )}
                >
                  {iface.errIn + iface.errOut}
                </TableCell>
                <TableCell
                  className={cn(
                    "numeric text-right font-mono text-xs",
                    iface.dropIn + iface.dropOut > 0 ? "text-warning" : "text-muted-foreground",
                  )}
                >
                  {iface.dropIn + iface.dropOut}
                </TableCell>
              </TableRow>
            ))}
            {interfaces.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="p-0">
                  <EmptyState title="No interfaces reported" icon={Servers} />
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </PanelBody>
    </Panel>
  )
}

/**
 * In and out as one bar against the busiest interface, in the network chart's
 * two colours: the uplink is the long bar, and which way its traffic runs is
 * the split of it.
 */
function TrafficBar({
  iface,
  busiest,
  colors,
}: {
  iface: NetStats
  busiest: number
  colors: { in: string; out: string }
}) {
  const inbound = (iface.recvRate / busiest) * 100
  const outbound = (iface.sendRate / busiest) * 100
  return (
    <div
      role="img"
      aria-label={`${rate(iface.recvRate)} in, ${rate(iface.sendRate)} out`}
      className="flex h-1.5 w-full min-w-16 overflow-hidden rounded-full bg-meter-track"
    >
      <span
        className="h-full transition-[width] duration-700 ease-out"
        style={{ width: `${inbound}%`, background: colors.in }}
      />
      <span
        className="h-full transition-[width] duration-700 ease-out"
        style={{ width: `${outbound}%`, background: colors.out }}
      />
    </div>
  )
}
