"use client"

import { useMemo, useState } from "react"
import { Download, Pause, Play } from "@/components/icons"
import { ApiError } from "@/lib/api"
import { bytes, percent, rate } from "@/lib/format"
import { bucketLive, containerRateLabel, peakOf } from "@/lib/container-usage"
import { downloadText, rowsToCsv } from "@/lib/metrics-export"
import { windowLabel, type ContainerRow } from "@/lib/metrics-range"
import type { ContainerStats, MetricEvent } from "@/lib/types"
import { useContainerFrame, useContainerLive } from "@/hooks/use-container-live"
import { IconAction } from "@/components/icon-action"
import { utilisationTone } from "@/components/meter"
import { Section } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { ChartPanel, ChartPlaceholder } from "@/components/metrics/chart-panel"
import { HeadReading } from "@/components/metrics/chart-reading"
import { SplitBar } from "@/components/metrics/hardware-panels"
import { MomentInspector } from "@/components/metrics/moment-inspector"
import { RangePicker, windowPhrase } from "@/components/metrics/range-picker"
import { TileTrend } from "@/components/metrics/sparkline"
import type { Series } from "@/components/metrics/metric-chart"
import { HUE, LiveBytes, LiveFigure, SeriesKey } from "@/components/overview/readings"
import { useContainerScales, useContainerUsage } from "@/components/docker/container-usage"

// The Metrics page's colours and words for the same measurements (§10), so a
// container's network is the line the host's network is. Its processes stand
// in the slot, and take the hue, of the host's load.
const OUT = "var(--chart-2)"
const WRITE = "var(--chart-3)"

const cpuSeries: Series[] = [
  { key: "cpu", label: "CPU", color: HUE.cpu, kind: "area", peakKey: "cpuPeak" },
]
const memSeries: Series[] = [
  { key: "mem", label: "Memory", color: HUE.mem, kind: "area", peakKey: "memPeak" },
]
const netSeries: Series[] = [
  { key: "netRx", label: "In", color: HUE.net, kind: "area", peakKey: "netRxPeak" },
  { key: "netTx", label: "Out", color: OUT, kind: "area", peakKey: "netTxPeak" },
]
const diskSeries: Series[] = [
  { key: "blockRead", label: "Read", color: HUE.disk, kind: "area", peakKey: "blockReadPeak" },
  { key: "blockWrite", label: "Write", color: WRITE, kind: "area", peakKey: "blockWritePeak" },
]
const pidsSeries: Series[] = [
  { key: "pids", label: "Processes", color: HUE.load, peakKey: "pidsPeak" },
]

// Module constants, because `ChartPanel` is memoised on its props (§10).
const formatPercent = (v: number) => percent(v)
const formatBytes = (v: number) => bytes(v)
const axisBytes = (v: number) => bytes(v, 1).replace(".0 ", " ")
const formatCount = (v: number) => Math.round(v).toLocaleString()

/**
 * What the selected service is using now, and how it got there, drawn the way
 * the Metrics page draws the host: a row of five readings that glide to each
 * frame over their line across the window on screen, then the same chart
 * panels at the same heights in the same hues, under the same section heads.
 * The operator asked that the two read as one page, which is why the five
 * tiles that once left this block are back.
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
  const { limit, streaming, history, controls } = usage

  // Pausing holds the rows the charts were drawn from while the socket keeps
  // filling the window behind them, as on Metrics; a recorded range has nothing
  // moving to pause, so choosing one drops the hold.
  const [frozen, setFrozen] = useState<ContainerRow[] | null>(null)
  if (frozen && !streaming) setFrozen(null)
  const rows = frozen ?? usage.rows

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

  // The reading subscribes to the socket's frames on its own, so the memoised
  // charts are not redrawn every second to change a figure in a head (§10).
  const fallback = initial?.id === containerId ? initial : undefined
  const busy = useMemo(
    () => <BusyNow containerId={containerId} initial={fallback} off={!running} />,
    [containerId, fallback, running],
  )
  const span = windowPhrase(controls.window, streaming)
  const exportCsv = () => {
    const stamp = new Date().toISOString().slice(0, 16).replace(/[:T]/g, "-")
    const file = `${name}-usage-${windowLabel(controls.window)}-${stamp}.csv`
    downloadText(file, rowsToCsv(rows))
  }

  return (
    <div className="flex min-w-0 flex-col gap-8">
      <MomentInspector samples={rows} />

      <Section
        title="Resources"
        actions={
          <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2">
            {picker && <div className="min-w-0 max-sm:w-full">{picker}</div>}
            <span className="numeric text-hint text-muted-foreground">{span.head}</span>
            <Status
              tone={!running ? "stopped" : feed.live ? "running" : "notice"}
              live={running && feed.live}
              label={label}
            />
            <div className="flex max-w-full flex-wrap items-center gap-2">
              {streaming && (
                <IconAction
                  label={frozen ? "Resume live feed" : "Pause live feed"}
                  onClick={() => setFrozen(frozen ? null : usage.rows)}
                >
                  {frozen ? <Play /> : <Pause />}
                </IconAction>
              )}
              <Button variant="outline" size="sm" disabled={rows.length === 0} onClick={exportCsv}>
                <Download />
                Export CSV
              </Button>
              <RangePicker controls={controls} ranges={usage.ranges} />
            </div>
          </div>
        }
      >
        <Readings
          containerId={containerId}
          initial={fallback}
          off={!running}
          rows={rows}
          limit={limit}
          over={span.over}
        />
      </Section>

      {disabled ? (
        <ChartPlaceholder
          plain
          note="History is not being recorded on this server. Set JD_METRICS_RETENTION to keep it."
        />
      ) : usage.error ? (
        <ErrorState error={usage.error} />
      ) : (
        <>
          <Section title="Processor and memory">
            <div className="grid gap-8 lg:grid-cols-2 [&>*]:min-w-0">
              <ChartPanel
                plain
                title="Utilisation"
                actions={busy}
                rows={rows}
                series={cpuSeries}
                unit="%"
                format={formatPercent}
                domain={scales.cpu.domain}
                yTicks={scales.cpu.ticks}
                events={events}
                onZoom={zoom}
                note={note}
                height={190}
              />
              <ChartPanel
                plain
                title="Memory"
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
                height={190}
              />
            </div>
          </Section>

          {(hasNetwork || hasDisk) && (
            <Section
              title={hasNetwork && hasDisk ? "Network and disk" : hasNetwork ? "Network" : "Disk"}
            >
              <div className="grid gap-8 lg:grid-cols-2 [&>*]:min-w-0">
                {hasNetwork && (
                  <ChartPanel
                    plain
                    title="Throughput"
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
                    title="Disk throughput"
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
            </Section>
          )}

          <Section title="Processes and data moved">
            <div className="grid gap-8 lg:grid-cols-2 [&>*]:min-w-0">
              <ChartPanel
                plain
                title="Processes"
                rows={rows}
                series={pidsSeries}
                format={formatCount}
                domain={pidsScale.domain}
                thresholds={pidsScale.thresholds}
                events={events}
                onZoom={zoom}
                note={note}
                height={165}
              />
              <DataMoved stats={stats} running={running} />
            </div>
          </Section>
        </>
      )}
    </div>
  )
}

/** The processor now in its chart's head, from the newest frame of the page's socket. */
function BusyNow({
  containerId,
  initial,
  off,
}: {
  containerId: string
  initial?: ContainerStats
  off: boolean
}) {
  const frame = useContainerFrame(containerId)
  const stats = off ? undefined : (frame.stats ?? initial)
  if (!stats?.cpuReady) return null
  return (
    <HeadReading
      items={[
        {
          label: "busy",
          value: percent(stats.cpuPercent),
          color: HUE.cpu,
          tone: stats.cpuLimit ? utilisationTone(stats.cpuPercent / stats.cpuLimit) : "default",
        },
      ]}
    />
  )
}

type Trends = Record<"cpu" | "mem" | "pids" | "net" | "disk", number[]>

/** Each tile's line from the rows the charts draw; a gap is left out, not a drop to zero. */
function trendsOf(rows: ContainerRow[]): Trends {
  const out: Trends = { cpu: [], mem: [], pids: [], net: [], disk: [] }
  for (const row of rows) {
    if (row.cpu !== null) out.cpu.push(row.cpu)
    if (row.mem !== null) out.mem.push(row.mem)
    if (row.pids !== null) out.pids.push(row.pids)
    if (row.netRx !== null || row.netTx !== null) out.net.push((row.netRx ?? 0) + (row.netTx ?? 0))
    if (row.blockRead !== null || row.blockWrite !== null) {
      out.disk.push((row.blockRead ?? 0) + (row.blockWrite ?? 0))
    }
  }
  return out
}

/**
 * The Metrics page's readings for one container: five figures from the newest
 * frame, each gliding to the next, keyed by and drawn over its line across the
 * window on screen, and each saying what it is a share of. What a tile has no
 * line for yet keeps a meter where its trend would be, against its ceiling.
 */
function Readings({
  containerId,
  initial,
  off,
  rows,
  limit,
  over,
}: {
  containerId: string
  initial?: ContainerStats
  /** Not running: whatever frame is held is an earlier visit's, not now. */
  off: boolean
  rows: ContainerRow[]
  limit: number
  over: string
}) {
  const frame = useContainerFrame(containerId)
  const stats = off ? undefined : (frame.stats ?? initial)
  const now = off ? undefined : frame.now
  const trends = useMemo(() => trendsOf(rows), [rows])

  const trend = (values: number[], name: string, color: string, max?: number) =>
    values.length < 2 ? undefined : (
      <TileTrend values={values} max={max} color={color} label={`${name} over ${over}`} />
    )
  const cpu = stats?.cpuReady ? stats.cpuPercent : undefined
  const quotaShare = cpu !== undefined && stats?.cpuLimit ? cpu / stats.cpuLimit : undefined
  const cpuTrend = trend(
    trends.cpu,
    "CPU",
    HUE.cpu,
    stats?.cpuLimit ? stats.cpuLimit * 100 : undefined,
  )
  const memTrend = trend(trends.mem, "Memory", HUE.mem, limit > 0 ? limit : undefined)
  const pidsLimit = stats?.pidsLimit ?? 0
  // A process count sits still for hours, and a line scaled to its own maximum
  // would not be drawn at all: headroom over the top keeps it a line at its level.
  const pidsTrend = trend(trends.pids, "Processes", HUE.load, Math.max(...trends.pids, 1) * 1.25)
  const rx = now?.netRx ?? null
  const tx = now?.netTx ?? null
  const read = now?.blockRead ?? null
  const write = now?.blockWrite ?? null

  return (
    <StatGrid columns={5}>
      <StatTile
        label={
          <>
            <SeriesKey color={HUE.cpu} />
            CPU
          </>
        }
        meterLabel="CPU"
        value={cpu !== undefined ? <LiveFigure value={cpu} decimals={1} unit="%" /> : "—"}
        tone={quotaShare !== undefined ? utilisationTone(quotaShare) : "default"}
        trend={cpuTrend}
        meter={cpuTrend ? undefined : quotaShare}
        hint={
          !stats
            ? undefined
            : stats.cpuLimit
              ? `of a ${stats.cpuLimit}-core quota`
              : stats.hostCpus
                ? `of one core · ${stats.hostCpus} here`
                : "of one core"
        }
      />
      <StatTile
        label={
          <>
            <SeriesKey color={HUE.mem} />
            Memory
          </>
        }
        meterLabel="Memory"
        value={stats ? <LiveBytes value={stats.memUsage} /> : "—"}
        tone={stats?.memLimited ? utilisationTone(stats.memPercent) : "default"}
        trend={memTrend}
        meter={memTrend || !stats?.memLimited ? undefined : stats.memPercent}
        hint={
          !stats
            ? undefined
            : stats.memLimited
              ? `of ${bytes(stats.memLimit)}`
              : stats.memHostPercent !== undefined
                ? `no limit · ${percent(stats.memHostPercent)} of the host`
                : "no limit"
        }
        trailing={stats ? "used" : undefined}
      />
      <StatTile
        label={
          <>
            <SeriesKey color={HUE.load} />
            Processes
          </>
        }
        meterLabel="Processes"
        value={stats ? <LiveFigure value={stats.pids} /> : "—"}
        tone={pidsLimit > 0 && stats ? utilisationTone((stats.pids / pidsLimit) * 100) : "default"}
        trend={pidsTrend}
        meter={pidsTrend || pidsLimit <= 0 || !stats ? undefined : (stats.pids / pidsLimit) * 100}
        hint={!stats ? undefined : pidsLimit > 0 ? `of ${pidsLimit.toLocaleString()}` : "no limit"}
      />
      <StatTile
        label={
          <>
            <SeriesKey color={HUE.net} />
            Network
          </>
        }
        value={rx !== null ? <LiveBytes value={rx} suffix="/s" /> : "—"}
        trend={trend(trends.net, "Network", HUE.net)}
        hint={tx !== null ? `${rate(tx)} out` : undefined}
        trailing={rx !== null ? "in" : undefined}
      />
      <StatTile
        label={
          <>
            <SeriesKey color={HUE.disk} />
            Disk I/O
          </>
        }
        value={
          read !== null || write !== null ? (
            <LiveBytes value={(read ?? 0) + (write ?? 0)} suffix="/s" />
          ) : (
            "—"
          )
        }
        trend={trend(trends.disk, "Disk I/O", HUE.disk)}
        hint={
          read !== null || write !== null
            ? `${rate(read)} read · ${rate(write)} written`
            : undefined
        }
      />
    </StatGrid>
  )
}

/**
 * How much the container has moved since it started: Docker's own counters,
 * which begin again at each start, as a bar each for the network and the disk
 * in their charts' colours, drawn as the Metrics page draws an interface. The
 * bars share a scale, so the longer one is where its data mostly went.
 *
 * It was six label-and-value pairs under "Since it started", two of which —
 * the processor quota and the memory limit — had not happened since anything:
 * they are the CPU and Memory readings' ceilings, and the limit is a line on
 * the memory chart.
 */
function DataMoved({ stats, running }: { stats?: ContainerStats; running: boolean }) {
  const rows = stats
    ? [
        {
          name: "Network",
          counted: stats.networkAvailable !== false,
          sides: [
            { word: "in", value: stats.netRx, color: HUE.net },
            { word: "out", value: stats.netTx, color: OUT },
          ],
        },
        {
          name: "Disk",
          counted: stats.blockAvailable !== false,
          sides: [
            { word: "read", value: stats.blockRead, color: HUE.disk },
            { word: "written", value: stats.blockWrite, color: WRITE },
          ],
        },
      ]
    : []
  const busiest = Math.max(
    ...rows.map((row) => (row.counted ? row.sides[0].value + row.sides[1].value : 0)),
    1,
  )
  return (
    <Panel plain>
      <PanelHeader
        title="Data moved"
        actions={<span className="text-hint text-muted-foreground">since it started</span>}
      />
      <PanelBody className="flex flex-1 flex-col group-data-[plain]/panel:px-0">
        {stats ? (
          <ul className="flex flex-1 flex-col justify-around gap-5">
            {rows.map((row) => (
              <li key={row.name} className="min-w-0 space-y-1.5">
                <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-3">
                  <span className="text-body font-medium">{row.name}</span>
                  {row.counted ? (
                    <span className="numeric flex gap-x-3 text-hint text-muted-foreground">
                      {row.sides.map((side) => (
                        <span key={side.word} className="whitespace-nowrap">
                          <SeriesKey color={side.color} />
                          <span className="font-medium text-foreground">
                            {bytes(side.value)}
                          </span>{" "}
                          {side.word}
                        </span>
                      ))}
                    </span>
                  ) : (
                    <span className="text-hint text-muted-foreground">Not counted</span>
                  )}
                </div>
                {row.counted && (
                  <SplitBar
                    label={row.sides.map((s) => `${bytes(s.value)} ${s.word}`).join(", ")}
                    busiest={busiest}
                    parts={row.sides}
                  />
                )}
              </li>
            ))}
          </ul>
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
