"use client"

import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react"
import { clock, percent, rate, relativeTime } from "@/lib/format"
import type { MetricEvent } from "@/lib/types"
import type { ChartRow } from "@/lib/metrics-range"
import { windowStat, type WindowStat } from "@/lib/metrics-summary"
import { groupIncidents, holds, type Incident, type Signal } from "@/lib/metrics-moments"
import {
  clearCrosshair,
  getCrosshair,
  pinCrosshair,
  setCrosshair,
  subscribeCrosshair,
  unpinCrosshair,
} from "@/lib/metrics-crosshair"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { RowList, ROW_BLEED } from "@/components/row-list"
import { DimActions, IconAction } from "@/components/icon-action"
import { MagnifyingGlassPlus } from "@/components/icons"
import { eventColor, type ChartRowLike } from "@/components/metrics/metric-chart"
import { HUE } from "@/components/overview/readings"
import { cn } from "@/lib/utils"

/**
 * What stood out in the window, as incidents you can put on the charts.
 *
 * Ten charts answer "what did the server do" only for a reader willing to
 * scan ten charts. This is the other order: the handful of incidents the
 * window would be remembered for, each the signals that arrived together — the
 * failed deploy with the CPU peak and the disk queue it caused — and where in
 * the window each one sits.
 *
 * Pressing an incident pins its instant on every chart, which is what the
 * reader wants to see next; the moment's strip then carries the logs and the
 * adjacent samples. Zooming is its own verb on the row: it narrows the window,
 * which is a bigger change than the press had been letting on — the list was
 * recomputed for the narrower window under the pointer and the row just
 * pressed moved.
 *
 * Nothing here is a verdict. The thresholds are only there to keep a quiet
 * night from listing "CPU peaked at 4%": a moment is worth a signal when it is
 * either high in absolute terms or well clear of the window's own mean.
 */
type Moment = Signal & {
  color: string
  /** The sentence that leads an incident. */
  title: string
  detail: string
  /** The few words a signal is when it comes with another one. */
  short: string
}

type Bounds = { from: number; to: number }

const MIB = 1024 * 1024

// A primitive snapshot, so hovering a chart — which moves the crosshair on
// every frame — does not re-render this list; only a pin does.
const pinnedTs = () => {
  const c = getCrosshair()
  return c.pinned ? c.ts : null
}
const noPin = () => null

export function NotableMoments({
  rows,
  events,
  cores,
  onZoom,
  className,
}: {
  rows: ChartRow[]
  events: MetricEvent[]
  cores: number
  onZoom: (from: number, to: number) => void
  className?: string
}) {
  const bounds = useMemo(
    () => (rows.length > 1 ? { from: rows[0].ts, to: rows[rows.length - 1].ts } : null),
    [rows],
  )
  const span = bounds ? bounds.to - bounds.from : 0
  const incidents = useMemo(
    () => (bounds ? groupIncidents(collect(rows, events, cores, bounds), span) : []),
    [rows, events, cores, bounds, span],
  )
  const pinned = useSyncExternalStore(subscribeCrosshair, pinnedTs, noPin)
  const [hovered, setHovered] = useState<string | null>(null)

  const selected = (incident: Incident<Moment>) => pinned !== null && holds(incident, pinned, span)

  const press = (incident: Incident<Moment>) => {
    if (selected(incident)) unpinCrosshair()
    else pinCrosshair(incident.ts)
  }
  // The pin follows a zoom, so the line is where the reader's eye lands. It
  // is set once the narrower window's rows are drawn: the charts are rebuilt
  // for the new window, and a chart going away releases the moment it held.
  const zoomPin = useRef<{ ts: number; span: number } | null>(null)
  useEffect(() => {
    const target = zoomPin.current
    if (!bounds || !target || span > target.span * 1.5) return
    zoomPin.current = null
    if (target.ts >= bounds.from && target.ts <= bounds.to) pinCrosshair(target.ts)
  }, [bounds, span])

  const zoomTo = (incident: Incident<Moment>) => {
    if (!bounds) return
    // The incident and a twentieth of the window either side, never less than
    // a minute: enough to see the shape at the recorded resolution and still
    // find it on the chart.
    const half = Math.max(span / 20, 60_000)
    const from = Math.max(bounds.from, incident.from - half)
    const to = Math.min(bounds.to, incident.to + half)
    zoomPin.current = { ts: incident.ts, span: to - from }
    onZoom(from, to)
  }
  // Hovering a row previews the instant on every chart, the way hovering a
  // chart does; the row is the chart's crosshair from the other side.
  const preview = (incident: Incident<Moment> | null) => {
    setHovered(incident?.id ?? null)
    if (incident) setCrosshair(incident.ts, "notable-moments")
    else clearCrosshair()
  }

  const multiDay = span > 86_400_000
  const signals = incidents.reduce((n, i) => n + 1 + i.rest.length, 0)

  return (
    <Panel plain className={className}>
      <PanelHeader
        title="Notable moments"
        actions={
          // The processes panel's control height, so the two hairlines meet.
          <span className="numeric flex h-8 items-center text-hint text-muted-foreground">
            {incidents.length > 0 && countPhrase(incidents.length, signals)}
          </span>
        }
      />
      <PanelBody flush className="py-1">
        {bounds && (
          <Timeline
            bounds={bounds}
            incidents={incidents}
            pinned={pinned}
            hovered={hovered}
            selected={selected}
            multiDay={multiDay}
            onPress={press}
            onPreview={preview}
          />
        )}
        {incidents.length === 0 ? (
          <p className="py-3 text-body text-muted-foreground">
            {bounds ? "Nothing stood out in this window." : "Waiting for the window to fill."}
          </p>
        ) : (
          <RowList
            key={bounds?.from}
            aria-label="Incidents"
            className="-mx-3 max-h-[17rem] animate-rise overflow-y-auto px-3"
          >
            {incidents.map((incident) => (
              <IncidentRow
                key={incident.id}
                incident={incident}
                selected={selected(incident)}
                hovered={hovered === incident.id}
                multiDay={multiDay}
                onPress={() => press(incident)}
                onZoom={() => zoomTo(incident)}
                onPreview={(on) => preview(on ? incident : null)}
              />
            ))}
          </RowList>
        )}
      </PanelBody>
    </Panel>
  )
}

function countPhrase(incidents: number, signals: number): string {
  const head = `${incidents} ${incidents === 1 ? "incident" : "incidents"}`
  return signals > incidents ? `${head} · ${signals} signals` : head
}

/**
 * Where in the window each incident sits, drawn the width of the charts'
 * own axes so a mark here and a spike there line up by eye.
 *
 * The marks are a pointer's shortcut to the rows, not a second list: the rows
 * are the keyboard's and the screen reader's way in, and a duplicate set of
 * buttons would make every incident two tab stops.
 */
function Timeline({
  bounds,
  incidents,
  pinned,
  hovered,
  selected,
  multiDay,
  onPress,
  onPreview,
}: {
  bounds: Bounds
  incidents: Incident<Moment>[]
  pinned: number | null
  hovered: string | null
  selected: (incident: Incident<Moment>) => boolean
  multiDay: boolean
  onPress: (incident: Incident<Moment>) => void
  onPreview: (incident: Incident<Moment> | null) => void
}) {
  const span = bounds.to - bounds.from
  const at = (ts: number) => `${((ts - bounds.from) / span) * 100}%`
  const inWindow = pinned !== null && pinned >= bounds.from && pinned <= bounds.to
  return (
    <div data-slot="moments-timeline" aria-hidden className="pt-3 pb-2">
      <div className="relative h-6">
        <span className="absolute inset-x-0 top-1/2 h-px bg-hairline" />
        {inWindow && (
          <span
            className="absolute top-0 h-full w-px -translate-x-1/2 bg-foreground/60"
            style={{ left: at(pinned) }}
          />
        )}
        {incidents.map((incident) => {
          const lit = hovered === incident.id || selected(incident)
          return (
            <button
              key={incident.id}
              type="button"
              tabIndex={-1}
              onClick={() => onPress(incident)}
              onPointerEnter={() => onPreview(incident)}
              onPointerLeave={() => onPreview(null)}
              className="group/mark absolute top-0 flex h-full w-3 -translate-x-1/2 cursor-pointer items-center justify-center"
              style={{ left: at(incident.ts) }}
            >
              <span
                className={cn(
                  "w-1 rounded-full transition-[height,opacity]",
                  lit ? "h-6 opacity-100" : "h-3.5 opacity-80 group-hover/mark:h-6",
                )}
                style={{ background: incident.lead.color }}
              />
            </button>
          )
        })}
      </div>
      <div className="numeric mt-1 flex justify-between text-hint text-muted-foreground">
        <span>{instant(bounds.from, multiDay)}</span>
        <span>{instant(bounds.to, multiDay)}</span>
      </div>
    </div>
  )
}

function IncidentRow({
  incident,
  selected,
  hovered,
  multiDay,
  onPress,
  onZoom,
  onPreview,
}: {
  incident: Incident<Moment>
  selected: boolean
  hovered: boolean
  multiDay: boolean
  onPress: () => void
  onZoom: () => void
  onPreview: (on: boolean) => void
}) {
  const { lead, rest } = incident
  const iso = new Date(incident.ts).toISOString()
  return (
    <li
      data-slot="row"
      className="min-w-0"
      onPointerEnter={() => onPreview(true)}
      onPointerLeave={() => onPreview(false)}
    >
      {/* The press and the zoom are siblings, laid out in one flow (§6): a
          button inside a button is not a thing HTML allows. */}
      <div
        className={cn(
          "group flex min-w-0 items-center gap-3 transition-colors",
          ROW_BLEED,
          selected ? "bg-accent" : hovered && "bg-row-hover",
        )}
      >
        <button
          type="button"
          aria-pressed={selected}
          onClick={onPress}
          className="flex min-w-0 flex-1 items-start gap-3 rounded-md py-2.5 text-left focus-ring-inset"
        >
          <span className="numeric flex w-16 shrink-0 flex-col pt-px">
            <span className="text-body">{instant(incident.ts, multiDay)}</span>
            <span className="text-hint text-muted-foreground">{relativeTime(iso)}</span>
          </span>
          <span className="min-w-0 flex-1">
            <span className="flex min-w-0 items-center gap-2">
              <Key color={lead.color} />
              <span className="truncate text-body font-medium">{lead.title}</span>
            </span>
            {rest.length === 0 ? (
              <span className="block truncate pl-2.5 text-hint text-muted-foreground">
                {lead.detail}
              </span>
            ) : (
              <span className="flex min-w-0 flex-wrap gap-x-3 gap-y-0.5 pl-2.5">
                <span className="text-hint text-muted-foreground">with</span>
                {rest.map((signal) => (
                  <span
                    key={signal.id}
                    title={`${signal.title} · ${signal.detail}`}
                    className="numeric flex items-center gap-1.5 text-hint text-muted-foreground"
                  >
                    <Key color={signal.color} small />
                    {signal.short}
                  </span>
                ))}
              </span>
            )}
          </span>
        </button>
        <DimActions>
          <IconAction label="Zoom the charts to this incident" onClick={onZoom}>
            <MagnifyingGlassPlus />
          </IconAction>
        </DimActions>
      </div>
    </li>
  )
}

/** The key the legends draw, in the colour of the chart the signal is on. */
function Key({ color, small }: { color: string; small?: boolean }) {
  return (
    <span
      aria-hidden
      className={cn("w-0.5 shrink-0 rounded-full", small ? "h-2" : "h-2.5")}
      style={{ background: color }}
    />
  )
}

function instant(ts: number, multiDay: boolean): string {
  if (!multiDay) return clock(new Date(ts).toISOString())
  return new Date(ts).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  })
}

function collect(
  rows: ChartRowLike[],
  events: MetricEvent[],
  cores: number,
  bounds: Bounds,
): Moment[] {
  const out: Moment[] = []
  const add = (
    id: string,
    stat: WindowStat | null,
    color: string,
    threshold: (s: WindowStat) => number,
    title: (s: WindowStat) => string,
    short: (s: WindowStat) => string,
    detail: (s: WindowStat) => string,
  ) => {
    if (!stat) return
    const line = threshold(stat)
    if (stat.peak < line) return
    out.push({
      id,
      ts: stat.peakTs,
      weight: stat.peak / line,
      color,
      title: title(stat),
      short: short(stat),
      detail: detail(stat),
    })
  }
  const pct0 = (v: number) => percent(v, 0)
  const pct1 = (v: number) => percent(v, 1)

  add(
    "cpu",
    windowStat(rows, "cpu", "cpuPeak"),
    HUE.cpu,
    () => 70,
    (s) => `CPU peaked at ${pct0(s.peak)}`,
    (s) => `CPU ${pct0(s.peak)}`,
    (s) => `mean ${pct0(s.mean)} over the window`,
  )
  add(
    "steal",
    windowStat(rows, "cpuSteal"),
    "var(--destructive)",
    () => 2,
    (s) => `CPU steal reached ${pct1(s.peak)}`,
    (s) => `steal ${pct1(s.peak)}`,
    (s) => `mean ${pct1(s.mean)} · time the hypervisor gave to another tenant`,
  )
  add(
    "mem",
    windowStat(rows, "mem", "memPeak"),
    HUE.mem,
    () => 85,
    (s) => `Memory peaked at ${pct0(s.peak)} used`,
    (s) => `memory ${pct0(s.peak)}`,
    (s) => `mean ${pct0(s.mean)} over the window`,
  )
  add(
    "load",
    windowStat(rows, "load1"),
    HUE.load,
    () => Math.max(cores, 1),
    (s) => `Load reached ${s.peak.toFixed(2)}`,
    (s) => `load ${s.peak.toFixed(2)}`,
    (s) => `above the ${cores} cores this host has · mean ${s.mean.toFixed(2)}`,
  )
  add(
    "rx",
    windowStat(rows, "rx", "rxPeak"),
    HUE.net,
    (s) => Math.max(s.mean * 3, MIB),
    (s) => `Inbound traffic peaked at ${rate(s.peak)}`,
    (s) => `in ${rate(s.peak)}`,
    (s) => `mean ${rate(s.mean)} over the window`,
  )
  add(
    "tx",
    windowStat(rows, "tx", "txPeak"),
    "var(--chart-2)",
    (s) => Math.max(s.mean * 3, MIB),
    (s) => `Outbound traffic peaked at ${rate(s.peak)}`,
    (s) => `out ${rate(s.peak)}`,
    (s) => `mean ${rate(s.mean)} over the window`,
  )
  add(
    "await",
    windowStat(rows, "diskAwait", "diskAwaitPeak"),
    HUE.disk,
    () => 20,
    (s) => `Disk latency reached ${s.peak.toFixed(0)} ms`,
    (s) => `disk ${s.peak.toFixed(0)} ms`,
    (s) => `mean ${s.mean.toFixed(1)} ms per request · the slowest device`,
  )
  add(
    "busy",
    windowStat(rows, "diskBusy", "diskBusyPeak"),
    "var(--chart-3)",
    () => 90,
    (s) => `A disk was ${pct0(s.peak)} busy`,
    (s) => `disk ${pct0(s.peak)} busy`,
    (s) => `mean ${pct0(s.mean)} of the time with a request in flight`,
  )
  add(
    "psi-cpu",
    windowStat(rows, "psiCpu", "psiCpuPeak"),
    HUE.cpu,
    () => 10,
    (s) => `CPU pressure reached ${pct0(s.peak)}`,
    (s) => `CPU stall ${pct0(s.peak)}`,
    (s) => `tasks waited for a core ${pct0(s.peak)} of the time`,
  )
  add(
    "psi-mem",
    windowStat(rows, "psiMem", "psiMemPeak"),
    HUE.mem,
    () => 10,
    (s) => `Memory pressure reached ${pct0(s.peak)}`,
    (s) => `memory stall ${pct0(s.peak)}`,
    (s) => `work stalled on reclaim ${pct0(s.peak)} of the time`,
  )
  add(
    "psi-io",
    windowStat(rows, "psiIo", "psiIoPeak"),
    HUE.disk,
    () => 10,
    (s) => `I/O pressure reached ${pct0(s.peak)}`,
    (s) => `I/O stall ${pct0(s.peak)}`,
    (s) => `work stalled on storage ${pct0(s.peak)} of the time`,
  )
  add(
    "tcp",
    windowStat(rows, "tcp", "tcpPeak"),
    HUE.net,
    (s) => Math.max(s.mean * 2, 1000),
    (s) => `TCP sockets peaked at ${Math.round(s.peak).toLocaleString()}`,
    (s) => `${Math.round(s.peak).toLocaleString()} sockets`,
    (s) => `mean ${Math.round(s.mean).toLocaleString()} in use`,
  )
  add(
    "timewait",
    windowStat(rows, "tcpTimeWait"),
    "var(--chart-2)",
    () => 12_000,
    (s) => `TIME_WAIT reached ${Math.round(s.peak).toLocaleString()} sockets`,
    (s) => `${Math.round(s.peak).toLocaleString()} TIME_WAIT`,
    () => "each holds an ephemeral port for about a minute",
  )
  add(
    "swap",
    windowStat(rows, "swap"),
    HUE.disk,
    () => 50,
    (s) => `Swap reached ${pct0(s.peak)}`,
    (s) => `swap ${pct0(s.peak)}`,
    () => "the working set stopped fitting in RAM",
  )

  // The things the dashboard did, or saw, that went wrong: a failed deploy or
  // backup and a reboot are moments in the same sense as a spike, and usually
  // the explanation for one — so they outweigh any spike and lead the
  // incident they fall in.
  for (const [i, event] of events.entries()) {
    const ts = new Date(event.ts).getTime()
    if (Number.isNaN(ts) || ts < bounds.from || ts > bounds.to) continue
    if (event.severity === "info" && event.kind !== "reboot") continue
    out.push({
      id: `event-${i}`,
      ts,
      weight: event.severity === "error" ? 1000 : 500,
      color: eventColor(event),
      title: event.title,
      short: event.title,
      detail: event.detail ?? event.kind,
    })
  }

  return out
}
