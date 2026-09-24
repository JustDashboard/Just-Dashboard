"use client"

import { useMemo } from "react"
import { get, ApiError } from "@/lib/api"
import { bytes, percent, rate } from "@/lib/format"
import {
  containerRows,
  HISTORY_RANGES,
  memoryLimit,
  rangeSpec,
  windowQuery,
  windowRefreshMs,
  type ContainerRow,
} from "@/lib/metrics-range"
import type { AnomalyReport, ContainerHistory } from "@/lib/types"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { useMetricEvents } from "@/hooks/use-metrics-history"
import { useMetricsWindow } from "@/hooks/use-metrics-window"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Metric, MetricStrip, Section } from "@/components/page"
import { ErrorState, Notice } from "@/components/state"
import { ChartPanel, ChartPlaceholder } from "@/components/metrics/chart-panel"
import { RangePicker } from "@/components/metrics/range-picker"
import type { Series } from "@/components/metrics/metric-chart"
import { Warning } from "@/components/icons"

const cpuSeries: Series[] = [
  { key: "cpu", label: "CPU", color: "var(--chart-1)", kind: "area", peakKey: "cpuPeak" },
]

const memSeries: Series[] = [
  { key: "mem", label: "Memory", color: "var(--chart-2)", kind: "area", peakKey: "memPeak" },
]

const netSeries: Series[] = [
  { key: "netRx", label: "In", color: "var(--chart-2)", kind: "area" },
  { key: "netTx", label: "Out", color: "var(--chart-5)", kind: "area" },
]

const blockSeries: Series[] = [
  { key: "blockRead", label: "Read", color: "var(--chart-2)", kind: "area" },
  { key: "blockWrite", label: "Write", color: "var(--chart-5)", kind: "area" },
]

// Module constants rather than inline arrows: `ChartPanel` is memoised on its
// props, and a formatter made fresh on every render redraws every chart on
// every poll of the page around it.
const formatBytes = (v: number) => bytes(v)
const axisBytes = (v: number) => bytes(v, 0)
const formatRate = (v: number) => rate(v)
// Not capped at 100: a container using two cores is at 200%, and clipping
// that would hide the thing worth seeing.
const formatPercent = (v: number) => percent(v)

/**
 * What this container was doing before you opened the panel.
 *
 * The live stats socket, like the host one, only ever describes the time since
 * this panel was mounted — so a container that was killed for exceeding its
 * memory limit at 03:00, or that pinned a core for twenty minutes overnight,
 * left no trace anywhere in the dashboard. These charts read the series the
 * backend has been recording on its own timer.
 *
 * There is no "Live" option here on purpose: nothing accumulates a container's
 * stats across a page load, so it would draw an empty chart that fills in over
 * the next few minutes — the exact behaviour this panel exists to replace.
 */
/**
 * What this container's recorded history says has changed.
 *
 * Every claim here is a pattern read out of a series rather than a state
 * Docker reported, so every row says it was inferred and none of them fires on
 * a spike — a core pinned for one sample is a busy moment, and a panel that
 * calls that an anomaly is a panel people learn to close.
 */
function ContainerAnomalies({ containerId }: { containerId: string }) {
  const { data } = usePoll<AnomalyReport>(
    (signal) =>
      get<AnomalyReport>(
        `/docker/containers/${encodeURIComponent(containerId)}/anomalies`,
        undefined,
        signal,
      ),
    0,
    [containerId],
  )
  if (!data || data.anomalies.length === 0) return null

  return (
    <div className="space-y-2">
      {data.anomalies.map((anomaly) => (
        <Notice
          key={anomaly.id}
          title={anomaly.title}
          icon={Warning}
          tone={anomaly.severity === "critical" ? "danger" : "warning"}
        >
          <p>{anomaly.detail}</p>
          {anomaly.advice && <p className="mt-1">{anomaly.advice}</p>}
          <p className="mt-1 text-hint text-muted-foreground">
            Read from {data.samples} samples over the last {anomaly.window}, and inferred from their
            shape rather than reported by Docker.
          </p>
        </Notice>
      ))}
    </div>
  )
}

/**
 * `plain` is for a page where these charts are a block of their own rather
 * than a tab's whole content — a deployment's runtime. The charts lose their
 * frames, the one range switch moves to the section's head so it plainly
 * governs all four, and Processor and Memory sit side by side on a wide
 * screen.
 */
export function ContainerUsage({
  containerId,
  name,
  plain,
}: {
  containerId: string
  name: string
  plain?: boolean
}) {
  const controls = useMetricsWindow()
  const win = controls.window
  // The host's shared range preference starts on "1h", but a container has no
  // live series to fall back on, so a "live" preference has to resolve to the
  // narrowest recorded window rather than to nothing at all.
  const effective =
    win.key === "live" && win.from === undefined ? { ...win, key: "1h" as const } : win
  const params = windowQuery(effective, rangeSpec(effective.key).points)
  const signature = JSON.stringify(params)

  const { data, error, loading } = usePoll<ContainerHistory>(
    (signal) =>
      get<ContainerHistory>(
        `/docker/containers/${encodeURIComponent(containerId)}/stats/history`,
        params,
        signal,
      ),
    windowRefreshMs(effective),
    [containerId, signature],
  )

  // The same deploys, restarts and reboots the host charts are marked with.
  // A container's memory falling off a cliff is a different event depending on
  // whether the stack was redeployed a second earlier, and that is exactly the
  // fact these markers carry.
  const events = useMetricEvents(effective)

  const rows = useMemo<ContainerRow[]>(() => (data ? containerRows(data) : []), [data])
  const limit = memoryLimit(data)
  /**
   * Whether there is a per-container network series at all.
   *
   * Docker omits `networks` entirely for a container sharing the host's
   * network namespace — there is no per-container interface to measure. The
   * panel used to stay and explain itself, which meant a chart-shaped hole
   * with a paragraph in it sitting beside a real chart: the reader's eye goes
   * to it first because it is the odd one out, and what it has to say is
   * "nothing to show here". A container with no network of its own simply has
   * no network chart, and Block I/O takes the width back.
   *
   * `rows.length > 0` is the guard that matters: while the history is loading
   * there is no data either, and a panel that vanishes and then reappears a
   * second later is worse than one that was never there.
   */
  const hasNetwork = useMemo(
    () => rows.length === 0 || rows.some((r) => (r.netRx ?? 0) > 0 || (r.netTx ?? 0) > 0),
    [rows],
  )
  const disabled = error instanceof ApiError && error.code === "metrics_history_disabled"
  const peaks = useMemo(() => summarise(rows), [rows])
  // Scaled to the limit rather than to the data. A container sitting at a
  // quarter of its ceiling draws a short line, which is the useful picture:
  // an axis fitted to the series makes every container look equally close to
  // being killed, and pushes the limit line off the top of the chart where
  // recharts silently discards it. The ticks are the limit's quarters, so the
  // top one names the limit and none is an unround step of the headroom.
  // Memoised because `ChartPanel` is.
  const memoryScale = useMemo(
    () =>
      limit > 0
        ? {
            domain: [0, Math.round(limit * 1.04)] as [number, number],
            ticks: [0, limit / 4, limit / 2, (limit * 3) / 4, limit],
            // The limit is the line that explains an OOM kill, so it is drawn
            // even when the series never gets near it.
            thresholds: [{ value: limit, label: "limit", tone: "danger" as const }],
          }
        : undefined,
    [limit],
  )

  if (disabled) {
    return (
      <Panel plain={plain}>
        <PanelHeader title="Usage history" />
        <PanelBody>
          <ChartPlaceholder
            plain={plain}
            note="History is not being recorded on this server. Set JD_METRICS_RETENTION to keep it."
          />
        </PanelBody>
      </Panel>
    )
  }

  if (error) return <ErrorState error={error} />

  const note = loading
    ? "Loading history…"
    : `Nothing recorded for ${name} in this window yet — the server samples every ${data?.sampleIntervalSeconds ?? 15}s.`

  const range = <RangePicker controls={controls} ranges={HISTORY_RANGES} />
  const processor = (
    <ChartPanel
      title="Processor"
      actions={plain ? undefined : range}
      rows={rows}
      series={cpuSeries}
      unit="%"
      format={formatPercent}
      events={events}
      onZoom={controls.zoomTo}
      note={note}
      height={170}
      plain={plain}
      // Beside Memory the strip only said again what the two charts' Max
      // columns and the limit line say, and made Processor the taller of the
      // pair so their axes no longer lined up.
      footer={
        plain ? undefined : (
          <MetricStrip className="[&>*]:flex-1">
            <Metric label="Peak CPU" value={peaks.cpu === null ? "—" : percent(peaks.cpu)} />
            <Metric label="Peak memory" value={peaks.mem === null ? "—" : bytes(peaks.mem)} />
            <Metric label="Limit" value={limit > 0 ? bytes(limit) : "none"} />
          </MetricStrip>
        )
      }
    />
  )
  const memory = (
    <ChartPanel
      title="Memory"
      rows={rows}
      series={memSeries}
      format={formatBytes}
      axisFormat={axisBytes}
      events={events}
      onZoom={controls.zoomTo}
      note={note}
      height={170}
      plain={plain}
      domain={memoryScale?.domain}
      yTicks={memoryScale?.ticks}
      thresholds={memoryScale?.thresholds}
    />
  )
  /*
    Network and block throughput were being sampled for this container all
    along and thrown away at the end of every request. They are the two
    series that answer "is this the container saturating the host", which
    the CPU and memory charts on their own cannot.
  */
  const throughput = (
    <div
      className={cn(
        "grid [&>*]:min-w-0",
        plain ? "gap-6" : "gap-3",
        hasNetwork && "lg:grid-cols-2",
      )}
    >
      {hasNetwork && (
        <ChartPanel
          title="Network"
          rows={rows}
          series={netSeries}
          format={formatRate}
          axisFormat={axisBytes}
          events={events}
          onZoom={controls.zoomTo}
          showPeaks={false}
          note={note}
          height={150}
          plain={plain}
        />
      )}
      <ChartPanel
        title="Block I/O"
        rows={rows}
        series={blockSeries}
        format={formatRate}
        axisFormat={axisBytes}
        events={events}
        onZoom={controls.zoomTo}
        showPeaks={false}
        note={note}
        height={150}
        plain={plain}
      />
    </div>
  )

  /*
    Rate of change, above the charts that show it.
    "Writable layer: 38.7 GB" is a number nobody can act on; "+6.4 GB
    today" is. The same is true of memory — a container at 400 MB is a
    fact, a container whose floor has doubled in a day is a leak — and the
    history to say either has been recorded all along with nothing reading
    it for this purpose.
  */
  const anomalies = <ContainerAnomalies containerId={containerId} />

  if (plain) {
    return (
      <Section title="Usage history" actions={range}>
        {anomalies}
        <div className="grid gap-6 lg:grid-cols-2 [&>*]:min-w-0">
          {processor}
          {memory}
        </div>
        {throughput}
      </Section>
    )
  }
  return (
    <div className="space-y-3">
      {anomalies}
      {processor}
      {memory}
      {throughput}
    </div>
  )
}

/** The worst moment in the window, which is the number the charts exist to surface. */
function summarise(rows: ContainerRow[]): { cpu: number | null; mem: number | null } {
  let cpu: number | null = null
  let mem: number | null = null
  for (const row of rows) {
    if (row.cpuPeak !== null && (cpu === null || row.cpuPeak > cpu)) cpu = row.cpuPeak
    if (row.memPeak !== null && (mem === null || row.memPeak > mem)) mem = row.memPeak
  }
  return { cpu, mem }
}
