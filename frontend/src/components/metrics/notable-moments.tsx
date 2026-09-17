"use client"

import { useMemo } from "react"
import { clock, percent, rate } from "@/lib/format"
import type { MetricEvent } from "@/lib/types"
import type { ChartRow } from "@/lib/metrics-range"
import { windowStat, type WindowStat } from "@/lib/metrics-summary"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { eventColor, type ChartRowLike } from "@/components/metrics/metric-chart"

/**
 * What stood out in the window, as a list you can zoom from.
 *
 * Ten charts answer "what did the server do" only for a reader willing to
 * scan ten charts. This is the other order: the handful of moments the window
 * would be remembered for — the CPU peak, the hour a disk went to 100% busy,
 * the deploy that failed — each one a row, and each row a zoom into the
 * minutes around it. It is the question a person asks first ("did anything
 * happen?") answered before the charts rather than after them.
 *
 * Nothing here is a verdict. The thresholds are only there to keep a quiet
 * night from listing "CPU peaked at 4%": a moment is worth a row when it is
 * either high in absolute terms or well clear of the window's own mean.
 */
type Moment = {
  id: string
  ts: number
  color: string
  title: string
  subtitle: string
}

const MIB = 1024 * 1024

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
  const moments = useMemo(
    () => (bounds ? collect(rows, events, cores, bounds) : []),
    [rows, events, cores, bounds],
  )

  const zoomTo = (ts: number) => {
    if (!bounds) return
    // A twentieth of the window either side, and never less than a minute:
    // enough to see the shape of the moment at the recorded resolution and
    // still find it on the chart.
    const half = Math.max((bounds.to - bounds.from) / 20, 60_000)
    onZoom(Math.max(bounds.from, ts - half), Math.min(bounds.to, ts + half))
  }
  const multiDay = bounds ? bounds.to - bounds.from > 86_400_000 : false

  return (
    <Panel plain className={className}>
      <PanelHeader
        title="Notable moments"
        actions={
          moments.length > 0 && (
            <span className="numeric text-hint text-muted-foreground">{moments.length}</span>
          )
        }
      />
      <PanelBody
        flush
        className={moments.length === 0 ? "py-4" : "-mx-3 max-h-[19rem] overflow-y-auto px-3 py-1"}
      >
        {moments.length === 0 ? (
          <p className="text-body text-muted-foreground">
            {bounds ? "Nothing stood out in this window." : "Waiting for the window to fill."}
          </p>
        ) : (
          <RowList key={bounds?.from} className="animate-rise">
            {moments.map((m) => (
              <Row
                key={m.id}
                onClick={() => zoomTo(m.ts)}
                leading={
                  <span
                    aria-hidden
                    className="size-1.5 rounded-full"
                    style={{ background: m.color }}
                  />
                }
                title={m.title}
                subtitle={m.subtitle}
                trailing={
                  <span className="numeric text-hint text-muted-foreground">
                    {multiDay ? shortDateTime(m.ts) : clock(new Date(m.ts).toISOString())}
                  </span>
                }
                className="py-2.5"
              />
            ))}
          </RowList>
        )}
      </PanelBody>
    </Panel>
  )
}

function collect(
  rows: ChartRowLike[],
  events: MetricEvent[],
  cores: number,
  bounds: { from: number; to: number },
): Moment[] {
  const out: Moment[] = []
  const add = (
    id: string,
    stat: WindowStat | null,
    color: string,
    worth: (s: WindowStat) => boolean,
    title: (s: WindowStat) => string,
    subtitle: (s: WindowStat) => string,
  ) => {
    if (stat && worth(stat)) {
      out.push({ id, ts: stat.peakTs, color, title: title(stat), subtitle: subtitle(stat) })
    }
  }
  const pct0 = (v: number) => percent(v, 0)
  const pct1 = (v: number) => percent(v, 1)

  add(
    "cpu",
    windowStat(rows, "cpu", "cpuPeak"),
    "var(--chart-1)",
    (s) => s.peak >= 70,
    (s) => `CPU peaked at ${pct0(s.peak)}`,
    (s) => `mean ${pct0(s.mean)} over the window`,
  )
  add(
    "steal",
    windowStat(rows, "cpuSteal"),
    "var(--destructive)",
    (s) => s.peak >= 2,
    (s) => `CPU steal reached ${pct1(s.peak)}`,
    (s) => `mean ${pct1(s.mean)} · time the hypervisor gave to another tenant`,
  )
  add(
    "mem",
    windowStat(rows, "mem", "memPeak"),
    "var(--chart-2)",
    (s) => s.peak >= 85,
    (s) => `Memory peaked at ${pct0(s.peak)} used`,
    (s) => `mean ${pct0(s.mean)} over the window`,
  )
  add(
    "load",
    windowStat(rows, "load1"),
    "var(--chart-1)",
    (s) => s.peak >= Math.max(cores, 1),
    (s) => `Load reached ${s.peak.toFixed(2)}`,
    (s) => `above the ${cores} cores this host has · mean ${s.mean.toFixed(2)}`,
  )
  add(
    "rx",
    windowStat(rows, "rx", "rxPeak"),
    "var(--chart-2)",
    (s) => s.peak >= Math.max(s.mean * 3, MIB),
    (s) => `Inbound traffic peaked at ${rate(s.peak)}`,
    (s) => `mean ${rate(s.mean)} over the window`,
  )
  add(
    "tx",
    windowStat(rows, "tx", "txPeak"),
    "var(--chart-5)",
    (s) => s.peak >= Math.max(s.mean * 3, MIB),
    (s) => `Outbound traffic peaked at ${rate(s.peak)}`,
    (s) => `mean ${rate(s.mean)} over the window`,
  )
  add(
    "await",
    windowStat(rows, "diskAwait", "diskAwaitPeak"),
    "var(--chart-4)",
    (s) => s.peak >= 20,
    (s) => `Disk latency reached ${s.peak.toFixed(0)} ms`,
    (s) => `mean ${s.mean.toFixed(1)} ms per request · the slowest device`,
  )
  add(
    "busy",
    windowStat(rows, "diskBusy", "diskBusyPeak"),
    "var(--chart-3)",
    (s) => s.peak >= 90,
    (s) => `A disk was ${pct0(s.peak)} busy`,
    (s) => `mean ${pct0(s.mean)} of the time with a request in flight`,
  )
  add(
    "psi-cpu",
    windowStat(rows, "psiCpu", "psiCpuPeak"),
    "var(--chart-1)",
    (s) => s.peak >= 10,
    (s) => `CPU pressure reached ${pct0(s.peak)}`,
    (s) => `tasks waited for a core ${pct0(s.peak)} of the time`,
  )
  add(
    "psi-mem",
    windowStat(rows, "psiMem", "psiMemPeak"),
    "var(--chart-2)",
    (s) => s.peak >= 10,
    (s) => `Memory pressure reached ${pct0(s.peak)}`,
    (s) => `work stalled on reclaim ${pct0(s.peak)} of the time`,
  )
  add(
    "psi-io",
    windowStat(rows, "psiIo", "psiIoPeak"),
    "var(--chart-4)",
    (s) => s.peak >= 10,
    (s) => `I/O pressure reached ${pct0(s.peak)}`,
    (s) => `work stalled on storage ${pct0(s.peak)} of the time`,
  )
  add(
    "tcp",
    windowStat(rows, "tcp", "tcpPeak"),
    "var(--chart-1)",
    (s) => s.peak >= Math.max(s.mean * 2, 1000),
    (s) => `TCP sockets peaked at ${Math.round(s.peak).toLocaleString()}`,
    (s) => `mean ${Math.round(s.mean).toLocaleString()} in use`,
  )
  add(
    "timewait",
    windowStat(rows, "tcpTimeWait"),
    "var(--chart-4)",
    (s) => s.peak >= 12_000,
    (s) => `TIME_WAIT reached ${Math.round(s.peak).toLocaleString()} sockets`,
    () => "each holds an ephemeral port for about a minute",
  )
  add(
    "swap",
    windowStat(rows, "swap"),
    "var(--chart-4)",
    (s) => s.peak >= 50,
    (s) => `Swap reached ${pct0(s.peak)}`,
    () => "the working set stopped fitting in RAM",
  )

  // The things the dashboard did, or saw, that went wrong: a failed deploy or
  // backup and a reboot are moments in the same sense as a spike, and usually
  // the explanation for one.
  for (const [i, event] of events.entries()) {
    const ts = new Date(event.ts).getTime()
    if (Number.isNaN(ts) || ts < bounds.from || ts > bounds.to) continue
    if (event.severity === "info" && event.kind !== "reboot") continue
    out.push({
      id: `event-${i}`,
      ts,
      color: eventColor(event),
      title: event.title,
      subtitle: event.detail ?? event.kind,
    })
  }

  // Newest first, like the activity list, and capped: past eight rows this
  // stops being the shortlist it exists to be.
  out.sort((a, b) => b.ts - a.ts)
  return out.slice(0, 8)
}

function shortDateTime(ts: number): string {
  return new Date(ts).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  })
}
