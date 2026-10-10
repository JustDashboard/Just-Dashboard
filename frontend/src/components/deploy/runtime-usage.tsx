"use client"

import { useMemo } from "react"
import { ApiError } from "@/lib/api"
import { bytes, percent } from "@/lib/format"
import { bucketLive, containerRateLabel, peakOf } from "@/lib/container-usage"
import type { ContainerRow } from "@/lib/metrics-range"
import type { ContainerStats, MetricEvent } from "@/lib/types"
import { useContainerFrame, useContainerLive } from "@/hooks/use-container-live"
import { Meter, utilisationTone } from "@/components/meter"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChartPanel, ChartPlaceholder } from "@/components/metrics/chart-panel"
import { RangePicker } from "@/components/metrics/range-picker"
import type { Series } from "@/components/metrics/metric-chart"
import {
  ContainerAnomalies,
  useContainerScales,
  useContainerUsage,
} from "@/components/docker/container-usage"

// A hue per measurement rather than the two blues every chart shared, so the
// page reads as five readings at a glance; in and out, read and write keep a
// pair each. The peak shares its mean's colour (§10).
const cpuSeries: Series[] = [
  { key: "cpu", label: "CPU", color: "var(--chart-1)", kind: "area", peakKey: "cpuPeak" },
]
const memSeries: Series[] = [
  { key: "mem", label: "Memory", color: "var(--chart-4)", kind: "area", peakKey: "memPeak" },
]
const netSeries: Series[] = [
  { key: "netRx", label: "Received", color: "var(--chart-2)", kind: "area", peakKey: "netRxPeak" },
  { key: "netTx", label: "Sent", color: "var(--chart-5)", kind: "area", peakKey: "netTxPeak" },
]
const diskSeries: Series[] = [
  {
    key: "blockRead",
    label: "Read",
    color: "var(--chart-2)",
    kind: "area",
    peakKey: "blockReadPeak",
  },
  {
    key: "blockWrite",
    label: "Written",
    color: "var(--chart-4)",
    kind: "area",
    peakKey: "blockWritePeak",
  },
]
const pidsSeries: Series[] = [
  { key: "pids", label: "Processes", color: "var(--chart-5)", peakKey: "pidsPeak" },
]

// Module constants, because `ChartPanel` is memoised on its props (§10).
const formatPercent = (v: number) => percent(v)
const formatBytes = (v: number) => bytes(v)
const axisBytes = (v: number) => bytes(v, 1).replace(".0 ", " ")
const formatCount = (v: number) => Math.round(v).toLocaleString()

/**
 * What the selected service is using now, and how it got there.
 *
 * There is no row of figure tiles over the charts any more: the operator asked
 * for it to go, and each of its five figures now heads the chart it moves on —
 * the processor's share beside Processor, memory against its limit beside
 * Memory, what is received and sent beside Network, read and written beside
 * Disk, and the process count beside Processes — so the number and its shape
 * are read in one place, and what the tile's hint said (the quota, the limit,
 * the counters since the start) sits under the charts as the container's
 * lifetime totals. §15 pass 2 allows the exit when each figure's new place is
 * named, and this is that list.
 *
 * The Live range draws the stats socket's last five minutes in five-second
 * buckets, each a mean inside the envelope of its peaks — the shape the
 * recorded ranges have — where a frame a second drew a saw-tooth. The other
 * ranges read the recorded history. Markers are this project's releases going
 * live and its failed deployments, with the host's reboots: the host feed's
 * deploy markers are every project's.
 *
 * `initial` is a polled reading shown until the first frame lands; `onStats`
 * hands each frame on so the service's card shows the figure the charts do.
 * `picker` chooses the service when there is more than one. `running` is false
 * for a container that is not running — a container's own page draws one —
 * whose socket would only ever wait: it is not opened, the charts open on the
 * recorded hour, and the readings say it is not running rather than show the
 * last frame an earlier visit left behind.
 */
export function RuntimeUsage({
  containerId,
  name,
  initial,
  picker,
  events: projectEvents,
  onStats,
  running = true,
}: {
  containerId: string
  name: string
  initial?: ContainerStats
  picker?: React.ReactNode
  /** This project's own moments (`releaseEvents`), or a container's own (`usageMarkers`). */
  events: MetricEvent[]
  onStats?: (stats: ContainerStats) => void
  running?: boolean
}) {
  const feed = useContainerLive(running ? containerId : undefined, onStats)
  const stats = running
    ? (feed.stats ?? (initial?.id === containerId ? initial : undefined))
    : undefined
  const buckets = useMemo(() => bucketLive(feed.rows), [feed.rows])
  const usage = useContainerUsage(
    containerId,
    running ? { rows: buckets, memoryLimit: stats?.memLimited ? stats.memLimit : 0 } : undefined,
  )
  const { rows, limit, streaming, history, controls } = usage
  const scales = useContainerScales(rows, limit, streaming)
  const hostEvents = usage.events
  const events = useMemo(
    () => [...projectEvents, ...hostEvents.filter((event) => event.kind === "reboot")],
    [projectEvents, hostEvents],
  )
  const pidsLimit = stats?.pidsLimit ?? 0
  const pidsScale = useMemo(() => {
    const top = Math.max(peakOf(rows, ["pids", "pidsPeak"]), 1)
    return {
      domain: [0, Math.ceil(Math.max(top * 1.15, pidsLimit > 0 ? 0 : 4))] as [number, number],
      thresholds:
        pidsLimit > 0 && pidsLimit <= top * 2
          ? [{ value: pidsLimit, label: "limit", tone: "danger" as const }]
          : undefined,
    }
  }, [rows, pidsLimit])

  const label = !running
    ? "Not running"
    : feed.live
      ? "Live"
      : feed.stale
        ? "Readings stale"
        : feed.state === "open"
          ? "Waiting for Docker"
          : "Connecting"
  const disabled =
    usage.error instanceof ApiError && usage.error.code === "metrics_history_disabled"
  const note = streaming
    ? `Waiting for Docker's first readings of ${name}.`
    : usage.loading
      ? "Loading history…"
      : `Nothing recorded for ${name} in this window yet — the server samples every ${history?.sampleIntervalSeconds ?? 15}s.`
  // A dragged span of the live window would be answered from the recorded
  // history, at a fifteenth of the resolution it was dragged on.
  const zoom = streaming ? undefined : controls.zoomTo
  const hasNetwork = rows.length === 0 || rows.some((r) => r.netRx !== null || r.netTx !== null)
  const hasDisk =
    rows.length === 0 || rows.some((r) => r.blockRead !== null || r.blockWrite !== null)

  // Each chart's reading now subscribes to the socket's frames on its own,
  // so the memoised charts are not redrawn every second to change a figure
  // in their heads (§10) — on a recorded range their rows have not moved.
  const fallback = initial?.id === containerId ? initial : undefined
  const readings = useMemo(
    () => ({
      cpu: <Now containerId={containerId} initial={fallback} off={!running} render={cpuNow} />,
      memory: (
        <Now containerId={containerId} initial={fallback} off={!running} render={memoryNow} />
      ),
      network: (
        <Now containerId={containerId} initial={fallback} off={!running} render={networkNow} />
      ),
      disk: <Now containerId={containerId} initial={fallback} off={!running} render={diskNow} />,
      processes: (
        <Now containerId={containerId} initial={fallback} off={!running} render={processesNow} />
      ),
    }),
    [containerId, fallback, running],
  )

  return (
    <Panel plain>
      <PanelHeader
        title="Usage"
        actions={
          <>
            <Status
              tone={!running ? "stopped" : feed.live ? "running" : "notice"}
              live={running && feed.live}
              label={label}
              className="mr-2"
            />
            <RangePicker controls={controls} ranges={usage.ranges} />
          </>
        }
      >
        {picker && <div className="min-w-0 max-sm:w-full sm:mr-auto">{picker}</div>}
      </PanelHeader>
      <PanelBody className="space-y-6">
        <ContainerAnomalies containerId={containerId} />
        {disabled ? (
          <ChartPlaceholder
            plain
            note="History is not being recorded on this server. Set JD_METRICS_RETENTION to keep it."
          />
        ) : usage.error ? (
          <ErrorState error={usage.error} />
        ) : (
          <>
            <div className="grid gap-6 lg:grid-cols-2 [&>*]:min-w-0">
              <ChartPanel
                plain
                title="Processor"
                actions={readings.cpu}
                rows={rows}
                series={cpuSeries}
                unit="%"
                format={formatPercent}
                domain={scales.cpu.domain}
                yTicks={scales.cpu.ticks}
                events={events}
                onZoom={zoom}
                note={note}
                height={210}
              />
              <ChartPanel
                plain
                title="Memory"
                actions={readings.memory}
                rows={rows}
                series={memSeries}
                format={formatBytes}
                axisFormat={axisBytes}
                domain={scales.memory.domain}
                yTicks={scales.memory.ticks}
                thresholds={scales.memory.thresholds}
                events={events}
                onZoom={zoom}
                note={note}
                height={210}
              />
            </div>

            {(hasNetwork || hasDisk) && (
              <div className="grid gap-6 lg:grid-cols-2 [&>*]:min-w-0">
                {hasNetwork && (
                  <ChartPanel
                    plain
                    title="Network"
                    actions={readings.network}
                    rows={rows}
                    series={netSeries}
                    format={containerRateLabel}
                    axisFormat={axisBytes}
                    domain={scales.network.domain}
                    yTicks={scales.network.ticks}
                    events={events}
                    onZoom={zoom}
                    note={note}
                    height={180}
                  />
                )}
                {hasDisk && (
                  <ChartPanel
                    plain
                    title="Disk"
                    actions={readings.disk}
                    rows={rows}
                    series={diskSeries}
                    format={containerRateLabel}
                    axisFormat={axisBytes}
                    domain={scales.block.domain}
                    yTicks={scales.block.ticks}
                    events={events}
                    onZoom={zoom}
                    note={note}
                    height={180}
                  />
                )}
              </div>
            )}

            <div className="grid gap-6 lg:grid-cols-2 [&>*]:min-w-0">
              <ChartPanel
                plain
                title="Processes"
                actions={readings.processes}
                rows={rows}
                series={pidsSeries}
                format={formatCount}
                domain={pidsScale.domain}
                thresholds={pidsScale.thresholds}
                events={events}
                onZoom={zoom}
                note={note}
                height={150}
              />
              <Lifetime stats={stats} running={running} />
            </div>

            <p className="text-hint text-muted-foreground">
              {streaming
                ? "Live from Docker's stats stream, a reading a second, drawn as five-second means inside their peaks. A gap is a moment no reading arrived."
                : `Recorded every ${history?.sampleIntervalSeconds ?? 15}s, drawn in ${history?.stepSeconds ?? "—"}s buckets of means and measured peaks. History follows the container's name across replacements.`}
            </p>
          </>
        )}
      </PanelBody>
    </Panel>
  )
}

/** A chart's reading now, from the newest frame of the socket the page holds open. */
function Now({
  containerId,
  initial,
  off,
  render,
}: {
  containerId: string
  initial?: ContainerStats
  /** Not running: whatever frame is held is an earlier visit's, not now. */
  off?: boolean
  render: (stats?: ContainerStats, now?: ContainerRow) => React.ReactNode
}) {
  const frame = useContainerFrame(containerId)
  return off ? render() : render(frame.stats ?? initial, frame.now)
}

function cpuNow(stats?: ContainerStats) {
  const cpu = stats?.cpuReady ? stats.cpuPercent : undefined
  return (
    <Reading
      label="CPU now"
      color="var(--chart-1)"
      value={cpu !== undefined ? percent(cpu) : "—"}
      note={
        stats?.cpuLimit
          ? `of a ${stats.cpuLimit}-core quota`
          : stats?.hostCpus
            ? `of one core · ${stats.hostCpus} here`
            : "of one core"
      }
      meter={cpu !== undefined && stats?.cpuLimit ? cpu / stats.cpuLimit : undefined}
    />
  )
}

function memoryNow(stats?: ContainerStats) {
  return (
    <Reading
      label="Memory now"
      color="var(--chart-4)"
      value={stats ? bytes(stats.memUsage) : "—"}
      note={
        !stats
          ? undefined
          : stats.memLimited
            ? `of ${bytes(stats.memLimit)}`
            : stats.memHostPercent !== undefined
              ? `no limit · ${percent(stats.memHostPercent)} of the host`
              : "no limit"
      }
      meter={stats?.memLimited ? stats.memPercent : undefined}
    />
  )
}

function networkNow(_stats?: ContainerStats, now?: ContainerRow) {
  return (
    <span className="flex flex-wrap items-baseline justify-end gap-x-4 gap-y-1">
      <Reading
        label="Received now"
        color="var(--chart-2)"
        value={containerRateLabel(now?.netRx)}
        note="in"
      />
      <Reading
        label="Sent now"
        color="var(--chart-5)"
        value={containerRateLabel(now?.netTx)}
        note="out"
      />
    </span>
  )
}

function diskNow(_stats?: ContainerStats, now?: ContainerRow) {
  return (
    <span className="flex flex-wrap items-baseline justify-end gap-x-4 gap-y-1">
      <Reading
        label="Read now"
        color="var(--chart-2)"
        value={containerRateLabel(now?.blockRead)}
        note="read"
      />
      <Reading
        label="Written now"
        color="var(--chart-4)"
        value={containerRateLabel(now?.blockWrite)}
        note="written"
      />
    </span>
  )
}

function processesNow(stats?: ContainerStats) {
  const limit = stats?.pidsLimit ?? 0
  return (
    <Reading
      label="Processes now"
      color="var(--chart-5)"
      value={stats ? stats.pids.toLocaleString() : "—"}
      note={limit > 0 ? `of ${limit.toLocaleString()}` : "no limit"}
      meter={limit > 0 && stats ? (stats.pids / limit) * 100 : undefined}
    />
  )
}

/**
 * A chart's reading now, at the end of its head: the figure beside the key of
 * the series it is, what it is a share of, and — where there is a ceiling — a
 * short meter of how close it is. The words are read once, as a sentence; the
 * figure, the key and the meter are what the eye reads.
 */
function Reading({
  label,
  color,
  value,
  note,
  meter,
}: {
  label: string
  color: string
  value: string
  note?: string
  meter?: number
}) {
  return (
    <span className="flex min-w-0 items-baseline gap-1.5">
      <span className="sr-only">
        {label}: {value}
        {note && ` ${note}`}
      </span>
      <span
        aria-hidden
        className="h-2.5 w-0.5 shrink-0 self-center rounded-full"
        style={{ background: color }}
      />
      <span aria-hidden className="numeric text-sm font-semibold tracking-tight">
        {value}
      </span>
      {note && (
        <span aria-hidden className="truncate text-hint text-muted-foreground">
          {note}
        </span>
      )}
      {meter !== undefined && (
        <Meter
          value={meter}
          tone={utilisationTone(meter)}
          size="thin"
          label={label}
          className="w-14 self-center"
        />
      )}
    </span>
  )
}

/**
 * What the container has done since it started, and what it may use: the
 * counters Docker keeps from the start and the limits it runs under. These
 * were the tiles' hints, and they are facts about the container rather than
 * about the window on screen, so they stand beside the charts rather than
 * under one of them.
 */
function Lifetime({ stats, running = true }: { stats?: ContainerStats; running?: boolean }) {
  const counted = stats?.networkAvailable !== false
  const facts: [string, string][] = stats
    ? [
        ["Received", counted ? bytes(stats.netRx) : "not counted"],
        ["Sent", counted ? bytes(stats.netTx) : "not counted"],
        ["Read from disk", stats.blockAvailable === false ? "not counted" : bytes(stats.blockRead)],
        [
          "Written to disk",
          stats.blockAvailable === false ? "not counted" : bytes(stats.blockWrite),
        ],
        [
          "Processor",
          stats.cpuLimit
            ? `${stats.cpuLimit}-core quota`
            : `no quota${stats.hostCpus ? ` · ${stats.hostCpus} cores here` : ""}`,
        ],
        ["Memory", stats.memLimited ? `${bytes(stats.memLimit)} limit` : "no limit"],
      ]
    : []
  return (
    <Panel plain>
      <PanelHeader title="Since it started" />
      <PanelBody>
        {stats ? (
          <dl className="grid grid-cols-1 gap-x-8 sm:grid-cols-2">
            {facts.map(([name, value]) => (
              <div
                key={name}
                className="flex min-w-0 items-baseline justify-between gap-3 border-b border-hairline py-2 text-body"
              >
                <dt className="text-muted-foreground">{name}</dt>
                <dd className="numeric truncate font-medium">{value}</dd>
              </div>
            ))}
          </dl>
        ) : (
          <p className="py-2 text-hint text-muted-foreground">
            {running
              ? "Waiting for Docker's first reading."
              : "Not running. Docker counts again from its next start."}
          </p>
        )}
      </PanelBody>
    </Panel>
  )
}
