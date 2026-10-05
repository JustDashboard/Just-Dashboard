"use client"

import { useMemo } from "react"
import { get, ApiError } from "@/lib/api"
import { bytes, percent } from "@/lib/format"
import { byteScale, containerRateLabel, cpuScale, peakOf } from "@/lib/container-usage"
import {
  containerRows,
  HISTORY_RANGES,
  memoryLimit,
  RANGES,
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
import { Section } from "@/components/page"
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
  { key: "netRx", label: "Received", color: "var(--chart-2)", kind: "area", peakKey: "netRxPeak" },
  { key: "netTx", label: "Sent", color: "var(--chart-5)", kind: "area", peakKey: "netTxPeak" },
]

const blockSeries: Series[] = [
  {
    key: "blockRead",
    label: "Read",
    color: "var(--chart-2)",
    kind: "area",
    peakKey: "blockReadPeak",
  },
  {
    key: "blockWrite",
    label: "Write",
    color: "var(--chart-5)",
    kind: "area",
    peakKey: "blockWritePeak",
  },
]

// Module constants rather than inline arrows: `ChartPanel` is memoised on its
// props, and a formatter made fresh on every render redraws every chart on
// every poll of the page around it.
const formatBytes = (v: number) => bytes(v)
// The ticks are a byte scale's quarters, so one decimal is only ever "1.5 MB",
// and a whole figure drops its ".0". No "/s" on a rate's axis: the axis only
// establishes the scale, and the legend and tooltip say what it measures.
const axisBytes = (v: number) => bytes(v, 1).replace(".0 ", " ")
const formatRate = containerRateLabel
// Not capped at 100: a container using two cores is at 200%, and clipping
// that would hide the thing worth seeing.
const formatPercent = (v: number) => percent(v)

/**
 * What this container's recorded history says has changed.
 *
 * Every claim here is a pattern read out of a series rather than a state
 * Docker reported, so every row says it was inferred and none of them fires on
 * a spike — a core pinned for one sample is a busy moment, and a panel that
 * calls that an anomaly is a panel people learn to close.
 */
export function ContainerAnomalies({ containerId }: { containerId: string }) {
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
            Read from {data.samples} recorded points over the last {anomaly.window}, and inferred
            from their shape rather than reported by Docker.
          </p>
        </Notice>
      ))}
    </div>
  )
}

/** What a live feed hands the charts: its rows, and the limit memory is drawn against. */
export type LiveRows = { rows: ContainerRow[]; memoryLimit: number }

/**
 * The window a container's charts cover and the rows they draw over it.
 *
 * With `live`, the Live range draws the stats socket's own rows — a frame a
 * second, the last five minutes — and every other range reads the recorded
 * history. The two are never spliced into one line (§10): their cadences differ
 * fifteenfold, and the recorded points are means where the live ones are not.
 * Without `live` a container has no buffer of its own, so a "live" preference
 * resolves to the narrowest recorded window rather than to nothing at all.
 */
export function useContainerUsage(containerId: string, live?: LiveRows) {
  const controls = useMetricsWindow()
  const win = controls.window
  const named = win.key === "live" && win.from === undefined
  const streaming = named && live !== undefined
  const effective = named && !live ? { ...win, key: "1h" as const } : win
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
    { enabled: !streaming },
  )

  // The same deploys, restarts and reboots the host charts are marked with.
  // A container's memory falling off a cliff is a different event depending on
  // whether the stack was redeployed a second earlier, and that is exactly the
  // fact these markers carry.
  const events = useMetricEvents(effective)

  const recorded = useMemo<ContainerRow[]>(() => (data ? containerRows(data) : []), [data])
  return {
    controls: { ...controls, window: effective },
    ranges: live ? RANGES : HISTORY_RANGES,
    streaming,
    rows: streaming ? live.rows : recorded,
    limit: streaming ? live.memoryLimit : memoryLimit(data),
    events,
    history: data,
    error: streaming ? undefined : error,
    loading: !streaming && loading,
  }
}

export type ContainerUsageState = ReturnType<typeof useContainerUsage>

/** Recorded charts shared by a container's Usage tab and a deployment's runtime. */
export function ContainerUsage({
  containerId,
  name,
  plain = true,
}: {
  containerId: string
  name: string
  plain?: boolean
}) {
  const usage = useContainerUsage(containerId)
  return (
    <Section
      title="Usage history"
      actions={<RangePicker controls={usage.controls} ranges={usage.ranges} />}
    >
      <ContainerCharts usage={usage} containerId={containerId} name={name} plain={plain} />
    </Section>
  )
}

/**
 * Every axis a container's charts draw, ending on a round figure and ticking
 * at its quarters, peaks included, so the top tick is a number someone would
 * say aloud. Memoised, like everything handed to `ChartPanel`.
 */
export function useContainerScales(rows: ContainerRow[], limit: number, streaming: boolean) {
  return useMemo(() => {
    const memoryCeiling = Math.max(limit, peakOf(rows, ["mem", "memPeak"]))
    return {
      cpu: cpuScale(peakOf(rows, ["cpu", "cpuPeak"])),
      network: byteScale(peakOf(rows, ["netRx", "netTx", "netRxPeak", "netTxPeak"])),
      block: byteScale(
        peakOf(rows, ["blockRead", "blockWrite", "blockReadPeak", "blockWritePeak"]),
      ),
      // A limited container is scaled to its limit rather than to the data. A
      // container sitting at a quarter of its ceiling draws a short line,
      // which is the useful picture: an axis fitted to the series makes every
      // container look equally close to being killed, and pushes the limit
      // line off the top of the chart where recharts silently discards it.
      // The ticks are the limit's quarters, so the top one names the limit.
      memory:
        limit > 0
          ? {
              domain: [0, Math.round(memoryCeiling * 1.04)] as [number, number],
              ticks:
                memoryCeiling === limit
                  ? [0, limit / 4, limit / 2, (limit * 3) / 4, limit]
                  : undefined,
              // The limit is the line that explains an OOM kill, so it is
              // drawn even when the series never gets near it.
              thresholds: [
                {
                  value: limit,
                  label: streaming ? "limit" : "highest recorded limit",
                  tone: "danger" as const,
                },
              ],
            }
          : { ...byteScale(memoryCeiling), thresholds: undefined },
    }
  }, [rows, limit, streaming])
}

/**
 * Processor, memory, network and block I/O over the window `usage` holds,
 * with the anomalies the record shows and what the figures do and do not
 * measure. The section around them — its title and range — is the caller's.
 */
export function ContainerCharts({
  usage,
  containerId,
  name,
  plain = true,
}: {
  usage: ContainerUsageState
  containerId: string
  name: string
  plain?: boolean
}) {
  const { rows, limit, events, history, streaming, controls } = usage
  // Zero is a measurement. Only null is absent; an idle interface keeps its chart.
  const hasNetwork = rows.length === 0 || rows.some((r) => r.netRx !== null || r.netTx !== null)
  const hasBlock =
    rows.length === 0 || rows.some((r) => r.blockRead !== null || r.blockWrite !== null)
  const disabled =
    usage.error instanceof ApiError && usage.error.code === "metrics_history_disabled"
  const scales = useContainerScales(rows, limit, streaming)

  if (disabled) {
    return (
      <ChartPlaceholder
        plain={plain}
        note="History is not being recorded on this server. Set JD_METRICS_RETENTION to keep it."
      />
    )
  }

  if (usage.error) return <ErrorState error={usage.error} />

  const note = streaming
    ? `Waiting for Docker's first readings of ${name}.`
    : usage.loading
      ? "Loading history…"
      : `Nothing recorded for ${name} in this window yet — the server samples every ${history?.sampleIntervalSeconds ?? 15}s.`
  // A dragged span of the live window would be answered from the recorded
  // history, at a fifteenth of the resolution it was dragged on.
  const zoom = streaming ? undefined : controls.zoomTo
  const processor = (
    <ChartPanel
      title="Processor"
      rows={rows}
      series={cpuSeries}
      unit="%"
      format={formatPercent}
      domain={scales.cpu.domain}
      yTicks={scales.cpu.ticks}
      events={events}
      onZoom={zoom}
      note={note}
      height={170}
      plain={plain}
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
      onZoom={zoom}
      note={note}
      height={170}
      plain={plain}
      domain={scales.memory.domain}
      yTicks={scales.memory.ticks}
      thresholds={scales.memory.thresholds}
    />
  )
  const throughput = (
    <div className={cn("grid gap-6 [&>*]:min-w-0", hasNetwork && hasBlock && "lg:grid-cols-2")}>
      {hasNetwork && (
        <ChartPanel
          title="Network throughput"
          rows={rows}
          series={netSeries}
          format={formatRate}
          axisFormat={axisBytes}
          domain={scales.network.domain}
          yTicks={scales.network.ticks}
          events={events}
          onZoom={zoom}
          note={note}
          height={180}
          plain={plain}
        />
      )}
      {hasBlock && (
        <ChartPanel
          title="Block throughput"
          rows={rows}
          series={blockSeries}
          format={formatRate}
          axisFormat={axisBytes}
          domain={scales.block.domain}
          yTicks={scales.block.ticks}
          events={events}
          onZoom={zoom}
          note={note}
          height={180}
          plain={plain}
        />
      )}
    </div>
  )

  return (
    <>
      <ContainerAnomalies containerId={containerId} />
      <div className="grid gap-6 lg:grid-cols-2 [&>*]:min-w-0">
        {processor}
        {memory}
      </div>
      {throughput}
      {(!hasNetwork || !hasBlock) && (
        <p className="text-xs text-muted-foreground">
          No measurable{" "}
          {!hasNetwork && !hasBlock
            ? "network or block I/O"
            : !hasNetwork
              ? "network"
              : "block I/O"}{" "}
          intervals in this window. A rate needs two consecutive readings from available counters.
        </p>
      )}
      {streaming ? (
        <p className="text-hint text-muted-foreground">
          Live from Docker&apos;s stats stream: one reading a second, the last five minutes. Each
          rate is the bytes counted between two consecutive readings, so a gap is a moment no
          reading arrived. Pick a range for the recorded history.
        </p>
      ) : (
        <p className="text-hint text-muted-foreground">
          Recorded every {history?.sampleIntervalSeconds ?? 15}s · chart buckets{" "}
          {history?.stepSeconds ?? "—"}s. Means and measured peaks cover samples in each bucket, not
          activity between samples. History follows the container name across replacements; rates
          break at resets and missing samples.
        </p>
      )}
    </>
  )
}
