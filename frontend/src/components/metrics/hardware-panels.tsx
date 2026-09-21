"use client"

import { useRef, useState } from "react"
import { Servers } from "@/components/icons"
import { get } from "@/lib/api"
import { cn } from "@/lib/utils"
import { bytes, rate } from "@/lib/format"
import type { DirEntry, SensorReading, Snapshot } from "@/lib/types"
import type { Tone } from "@/components/tone"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { Meter, utilisationTone } from "@/components/meter"
import { EmptyState } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
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
 * Everything here is live-only: per-core load, sensor temperatures, mounts
 * and interfaces are all in the socket's snapshot and none of them survive a
 * downsampled chart, so these are readings rather than series. Plain panels,
 * every one — a title and a hairline on the page's own ground.
 */
export function PerCorePanel({ cores }: { cores: number[] }) {
  if (cores.length === 0) return null
  return (
    <Panel plain>
      <PanelHeader
        title="Per-core utilisation"
        actions={
          <span className="numeric text-hint text-muted-foreground">{cores.length} cores</span>
        }
      />
      <PanelBody>
        {/* Four columns at most, not as many as fit. `auto-fit` put all eight
            cores of a typical VPS on one line, which left each bar about forty
            pixels wide — a track too short to tell 40% from 60% on, which is
            the only question this panel answers. */}
        <div className="grid grid-cols-2 gap-x-5 gap-y-1.5 md:grid-cols-3 xl:grid-cols-4">
          {cores.map((value, i) => (
            <Bar
              key={i}
              label={`cpu${i}`}
              value={value}
              tone={utilisationTone(value)}
              figure={`${value.toFixed(0)}%`}
            />
          ))}
        </div>
      </PanelBody>
    </Panel>
  )
}

/**
 * Sensor temperatures, hottest first, each against its own limit.
 *
 * Absent on most virtual servers, and drawn only when the host reports at
 * least one: a panel saying "no sensors" would be a box on every VPS
 * explaining a feature it cannot have.
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
        <div className="grid grid-cols-1 gap-x-5 gap-y-1.5 md:grid-cols-2 xl:grid-cols-3">
          {sensors.map((s) => {
            // The bar fills towards the sensor's own critical mark where it has
            // one, so two sensors with different ceilings are comparable by
            // how much room each has left rather than by raw degrees.
            const ceiling = s.critical > 0 ? s.critical : s.high > 0 ? s.high : 100
            return (
              <Bar
                key={s.name}
                label={s.name}
                labelClassName="w-28"
                value={(s.tempC / ceiling) * 100}
                tone={sensorTone(s)}
                figure={`${s.tempC.toFixed(0)}°C`}
              />
            )
          })}
        </div>
      </PanelBody>
    </Panel>
  )
}

export function sensorTone(s: SensorReading): Tone {
  if (s.critical > 0 && s.tempC >= s.critical) return "danger"
  if (s.high > 0 && s.tempC >= s.high) return "warning"
  return "default"
}

function Bar({
  label,
  labelClassName,
  value,
  tone,
  figure,
}: {
  label: string
  labelClassName?: string
  value: number
  tone: Tone
  figure: string
}) {
  return (
    <div className="flex min-w-0 items-center gap-2">
      <span
        className={cn(
          "w-8 shrink-0 truncate font-mono text-micro text-muted-foreground",
          labelClassName,
        )}
        title={label}
      >
        {label}
      </span>
      <Meter value={value} tone={tone} size="thin" label={label} className="flex-1" />
      {/* The figure carries the same tone as its bar. A core pinned at 98%
          beside one idling at 4% should be findable by colour in a grid of
          sixty-four, not by reading every number. */}
      <span
        className={cn(
          "numeric w-10 shrink-0 text-right font-mono text-micro",
          tone === "danger"
            ? "text-destructive"
            : tone === "warning"
              ? "text-warning"
              : "text-muted-foreground",
        )}
      >
        {figure}
      </span>
    </div>
  )
}

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
      <PanelBody className="divide-y divide-hairline [&>*]:pb-3 [&>*+*]:pt-3 [&>*:last-child]:pb-0">
        {snapshot.mounts.map((mount) => {
          const tone = utilisationTone(mount.usedPercent)
          const inodes = mount.inodesTotal > 0 ? (mount.inodesUsed / mount.inodesTotal) * 100 : 0
          const figure = cn(
            tone === "danger"
              ? "text-destructive"
              : tone === "warning"
                ? "text-warning"
                : "text-foreground",
          )
          return (
            // Three lines, in the order the question is asked: which volume and
            // how full, the bar, then the detail.
            <div key={mount.mountpoint} className="min-w-0 space-y-1.5">
              <div className="flex min-w-0 items-baseline justify-between gap-3">
                <span className="flex min-w-0 items-baseline gap-2">
                  <span className="truncate text-body font-medium">{mount.mountpoint}</span>
                  <Tag>{mount.fstype}</Tag>
                </span>
                <span className="numeric shrink-0 text-hint text-muted-foreground">
                  {bytes(mount.used)} / {bytes(mount.total)}
                  <span className={cn("ml-2 text-body font-medium", figure)}>
                    {mount.usedPercent.toFixed(0)}%
                  </span>
                </span>
              </div>
              <Meter value={mount.usedPercent} tone={tone} size="thin" label={mount.mountpoint} />
              <div className="flex min-w-0 items-center justify-between gap-3 text-hint text-muted-foreground">
                <span className="flex min-w-0 items-center gap-2">
                  <span className="truncate font-mono">{mount.device}</span>
                  <span className="numeric shrink-0">
                    {rate(mount.readRate)} read · {rate(mount.writeRate)} write
                  </span>
                  {/* Inodes fill independently of bytes, so a volume the bar
                      above calls two-thirds empty can still refuse to create a
                      file. Said here, next to the bar that is about to be
                      wrong, rather than only in the Inodes chart. */}
                  {inodes >= 80 && (
                    <span className="numeric shrink-0 font-medium text-warning">
                      {inodes.toFixed(0)}% inodes
                    </span>
                  )}
                </span>
                <Button
                  size="xs"
                  variant="ghost"
                  className="-my-1 shrink-0 text-muted-foreground"
                  disabled={scanning !== null}
                  onClick={() => scan(mount.mountpoint)}
                >
                  {scanning === mount.mountpoint ? "scanning…" : "scan"}
                </Button>
              </div>
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
            </div>
          )
        })}
        {snapshot.mounts.length === 0 && (
          <EmptyState title="No filesystems reported" icon={Servers} />
        )}
      </PanelBody>
    </Panel>
  )
}

export function InterfacesPanel({ snapshot }: { snapshot: Snapshot }) {
  return (
    <Panel>
      <PanelHeader
        title="Interfaces"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {snapshot.net.filter((n) => n.isUp).length} up
          </span>
        }
      />
      {/* The bleed is plain-only: inside this panel's frame the outer columns
          take the gutter from their own cell padding instead, which lands them
          in the title's column either way (§2). */}
      <PanelBody flush className="group-data-[plain]/panel:-mx-4">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Interface</TableHead>
              <TableHead className="text-right">In</TableHead>
              <TableHead className="text-right">Out</TableHead>
              {/* Drops next to errors: a link that is up, error-free and
                  dropping packets is a queue that is too short, not a fault. */}
              <TableHead className="text-right">Errors</TableHead>
              <TableHead className="text-right">Drops</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {snapshot.net.map((iface) => (
              <TableRow key={iface.interface}>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <span
                      className={cn(
                        "size-1.5 rounded-full",
                        iface.isUp ? "bg-success" : "bg-muted-foreground",
                      )}
                    />
                    <span className="font-mono text-xs">{iface.interface}</span>
                  </div>
                  <p
                    className="max-w-[14rem] truncate font-mono text-hint text-muted-foreground"
                    title={iface.addrs.join(", ")}
                  >
                    {iface.addrs.join(", ") || "no address"}
                  </p>
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
            {snapshot.net.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="p-0">
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
