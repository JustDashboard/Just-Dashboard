"use client"

import { useMemo, useState } from "react"
import { Download, Pause, Play } from "@/components/icons"
import { bytes, duration, percent, rate } from "@/lib/format"
import type { MetricEvent, MountStats, Snapshot } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { useMetrics } from "@/hooks/use-metrics"
import {
  useHealth,
  useMetricEvents,
  useMetricsHistory,
  useStorageHistory,
  type HistoryState,
} from "@/hooks/use-metrics-history"
import { useMetricsWindow } from "@/hooks/use-metrics-window"
import {
  historyRows,
  liveRows,
  liveStorageRows,
  rangeSpec,
  storageRows,
  windowLabel,
  type ChartRow,
  type MetricsWindow,
  type StorageSeriesMeta,
} from "@/lib/metrics-range"
import { downloadText, rowsToCsv } from "@/lib/metrics-export"
import {
  formatDelta,
  relativeChange,
  windowStat,
  windowStatSum,
  type WindowStat,
} from "@/lib/metrics-summary"
import { Page, PageContext, PageState, Section } from "@/components/page"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { utilisationTone } from "@/components/meter"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { HealthPanel, HealthVerdict } from "@/components/metrics/health-panel"
import { RangePicker } from "@/components/metrics/range-picker"
import { NotableMoments } from "@/components/metrics/notable-moments"
import { TopProcesses } from "@/components/metrics/top-processes"
import {
  InterfacesPanel,
  MountsPanel,
  PerCorePanel,
  SensorsPanel,
  sensorTone,
} from "@/components/metrics/hardware-panels"
import type { Series } from "@/components/metrics/metric-chart"
import { ErrorState } from "@/components/state"
import { Tag } from "@/components/tag"
import { FactDot, HostFact, HostIdentity, platformName } from "@/components/metrics/host-identity"
import { cpuProduct, platformProduct, virtualizationProduct } from "@/components/product-logo"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

// Peaks share their series' colour: they are the same measurement seen at a
// finer resolution, not a different quantity, and giving them a colour of
// their own would read as four unrelated lines instead of two pairs.
const cpuSeries: Series[] = [
  { key: "cpu", label: "CPU", color: "var(--chart-1)", kind: "area", peakKey: "cpuPeak" },
]

/**
 * The same processor time, split by what it was spent on.
 *
 * `steal` and `iowait` are the reason this view exists. A single "68% busy"
 * figure cannot distinguish a server doing work from one waiting on a disk
 * from one whose hypervisor is running another tenant on the core — and the
 * response to each is completely different. Stacked, because the four parts
 * are shares of one whole rather than four independent measurements.
 */
const cpuModeSeries: Series[] = [
  { key: "cpuUser", label: "User", color: "var(--chart-1)", stack: "cpu" },
  { key: "cpuSystem", label: "System", color: "var(--chart-2)", stack: "cpu" },
  { key: "cpuIowait", label: "I/O wait", color: "var(--chart-4)", stack: "cpu" },
  { key: "cpuSteal", label: "Steal", color: "var(--destructive)", stack: "cpu" },
]

const memSeries: Series[] = [
  { key: "mem", label: "Memory", color: "var(--chart-2)", peakKey: "memPeak" },
  { key: "swap", label: "Swap", color: "var(--chart-4)" },
]

const netSeries: Series[] = [
  { key: "rx", label: "In", color: "var(--chart-2)", kind: "area", peakKey: "rxPeak" },
  { key: "tx", label: "Out", color: "var(--chart-5)", kind: "area", peakKey: "txPeak" },
]

const ioSeries: Series[] = [
  {
    key: "diskRead",
    label: "Read",
    color: "var(--chart-2)",
    kind: "area",
    peakKey: "diskReadPeak",
  },
  {
    key: "diskWrite",
    label: "Write",
    color: "var(--chart-5)",
    kind: "area",
    peakKey: "diskWritePeak",
  },
]

const iopsSeries: Series[] = [
  { key: "diskReads", label: "Reads/s", color: "var(--chart-2)", peakKey: "diskReadsPeak" },
  { key: "diskWrites", label: "Writes/s", color: "var(--chart-5)", peakKey: "diskWritesPeak" },
]

const latencySeries: Series[] = [
  {
    key: "diskAwait",
    label: "Latency",
    color: "var(--chart-4)",
    kind: "area",
    peakKey: "diskAwaitPeak",
  },
  { key: "diskBusy", label: "Busy", color: "var(--chart-3)" },
]

const pressureSeries: Series[] = [
  { key: "psiCpu", label: "CPU", color: "var(--chart-1)", peakKey: "psiCpuPeak" },
  { key: "psiMem", label: "Memory", color: "var(--chart-2)", peakKey: "psiMemPeak" },
  { key: "psiIo", label: "I/O", color: "var(--chart-4)", peakKey: "psiIoPeak" },
]

const loadSeries: Series[] = [
  { key: "load1", label: "1 min", color: "var(--chart-1)", peakKey: "load1Peak" },
  { key: "load5", label: "5 min", color: "var(--chart-2)" },
  { key: "load15", label: "15 min", color: "var(--chart-3)" },
]

const socketSeries: Series[] = [
  { key: "tcp", label: "TCP in use", color: "var(--chart-1)", kind: "area", peakKey: "tcpPeak" },
  { key: "tcpTimeWait", label: "TIME_WAIT", color: "var(--chart-4)" },
]

/*
 * Formatters, domains and thresholds as module constants.
 *
 * These are passed to a memoised ChartPanel, whose bail-out is a shallow prop
 * comparison — so an inline `format={fmtRate}` or `domain={PERCENT_DOMAIN}`
 * is a new identity on every render and quietly turns the memo off for that
 * panel. Hoisting them is the difference between the page's two-second tick
 * touching two panels and touching all ten.
 */
const fmtRate = (v: number) => rate(v)
const fmtPercent0 = (v: number) => percent(v, 0)
const fmtLoad = (v: number) => v.toFixed(2)
const fmtOps = (v: number) => `${Math.round(v)}/s`
const fmtMillis = (v: number) => v.toFixed(1)
const fmtCount = (v: number) => Math.round(v).toLocaleString()

/**
 * A byte figure short enough for an axis gutter.
 *
 * "585.9 KB/s" wraps to two lines and clips; "586 KB" does not, and an axis
 * only has to establish the scale. The units and the exact number belong in
 * the legend and the tooltip, which have room for them.
 */
const axisBytes = (v: number) => bytes(v, 0)

const PERCENT_DOMAIN: [number, number] = [0, 100]
const FROM_ZERO: [number, string] = [0, "auto"]

const DISK_THRESHOLD = [{ value: 85, label: "85%", tone: "warning" as const }]
const INODE_THRESHOLD = [{ value: 90, label: "90%", tone: "warning" as const }]
const PRESSURE_THRESHOLD = [{ value: 10, label: "stalling", tone: "warning" as const }]

// Shared empties. A fresh `[]` each render is a new identity, which is exactly
// the re-render these memos exist to avoid.
const NO_ROWS: ChartRow[] = []
type StorageBundle = { rows: Record<string, number | string | null>[]; series: StorageSeriesMeta[] }
const NO_STORAGE: StorageBundle = { rows: [], series: [] }

// A window that fetches nothing, for the hook that reads the prior window
// when there is no prior window to read.
const NOTHING: MetricsWindow = { key: "live" }

export default function MetricsPage() {
  // Two sources, deliberately kept apart. The live socket is owned by the
  // dashboard shell and is only ever a view of "since this tab opened"; the
  // recorded series comes from the backend, which has been sampling on its own
  // timer whether or not anyone was watching.
  const { host, snapshot, history, error } = useMetrics()
  const controls = useMetricsWindow()
  const win = controls.window
  const recorded = useMetricsHistory(win)
  const recordedStorage = useStorageHistory(win)
  const events = useMetricEvents(win)
  const { health, loading: healthLoading } = useHealth()

  const live = win.key === "live" && win.from === undefined

  // The same span one window earlier, so each headline figure can say whether
  // it is up or down on the last time this span was looked at.
  const prior = useMemo(
    () => priorWindow(win, live, recorded.history?.to),
    [win, live, recorded.history?.to],
  )
  const priorHistory = useMetricsHistory(prior ?? NOTHING)

  const liveChartRows = useMemo(() => (live ? liveRows(history) : NO_ROWS), [live, history])
  const recordedChartRows = useMemo(
    () => (!live && recorded.history ? historyRows(recorded.history) : NO_ROWS),
    [live, recorded.history],
  )
  const priorRows = useMemo(
    () => (prior && priorHistory.history ? historyRows(priorHistory.history) : NO_ROWS),
    [prior, priorHistory.history],
  )

  const liveStorage = useMemo(
    () => (live ? liveStorageRows(history, snapshot?.mounts ?? []) : NO_STORAGE),
    [live, history, snapshot?.mounts],
  )
  const recordedStorageRows = useMemo(
    () => (!live && recordedStorage.storage ? storageRows(recordedStorage.storage) : NO_STORAGE),
    [live, recordedStorage.storage],
  )

  // Pausing the live feed holds the rows the charts were drawn from; the
  // socket keeps filling the buffer behind it, so resuming catches up rather
  // than restarting. Dropped the moment a recorded range is picked, where
  // there is nothing moving to pause — by remembering which feed it was taken
  // from rather than by an effect that clears it a render late.
  const [frozen, setFrozen] = useState<{
    live: boolean
    rows: ChartRow[]
    storage: StorageBundle
  } | null>(null)
  if (frozen && frozen.live !== live) setFrozen(null)
  const paused = live && frozen !== null

  const rows = paused ? frozen.rows : live ? liveChartRows : recordedChartRows
  const storage = paused ? frozen.storage : live ? liveStorage : recordedStorageRows

  const storageSeries = useMemo<Series[]>(
    () => storage.series.map((m) => ({ key: m.key, label: m.mountpoint, color: m.color })),
    [storage.series],
  )
  const inodeSeries = useMemo<Series[]>(
    () => storage.series.map((m) => ({ key: `${m.key}i`, label: m.mountpoint, color: m.color })),
    [storage.series],
  )

  const stats = useMemo(() => summarise(rows), [rows])
  const priorStats = useMemo(() => (prior ? summarise(priorRows) : null), [prior, priorRows])

  if (!snapshot || !host || (error && !snapshot)) {
    return (
      <PageState
        eyebrow="Server"
        title="Metrics"
        error={error && !snapshot ? new Error(error) : undefined}
        skeleton={
          <>
            <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-5 [&>*]:min-w-0">
              {Array.from({ length: 5 }).map((_, i) => (
                <Skeleton key={i} className="h-24 rounded-xl" />
              ))}
            </div>
            <Skeleton className="h-64 rounded-xl" />
            <Skeleton className="h-64 rounded-xl" />
          </>
        }
      />
    )
  }

  const cores = snapshot.cpu.cores || 1
  const showPeaks = !live
  const note = emptyChartNote(live, recorded)
  const zoom = controls.zoomTo
  const loadThreshold = coreThreshold(cores)
  const sensors = snapshot.sensors ?? []

  const exportCsv = () => {
    const stamp = new Date().toISOString().slice(0, 16).replace(/[:T]/g, "-")
    downloadText(`${host.hostname}-metrics-${windowLabel(win)}-${stamp}.csv`, rowsToCsv(rows))
  }

  return (
    <Page className="animate-rise">
      <PageContext title="Metrics" />

      {/* What this machine is made of. The Overview's line says what it
          runs; this one says what it runs on, drawn as the processor itself,
          because every figure below is a share of something named here. */}
      <HostIdentity
        mark={cpuProduct(host.cpuModel, host.kernelArch)}
        title={host.cpuModel || "Unknown processor"}
        facts={
          <>
            <span className="numeric">
              {cores} cores{host.cpuMhz > 0 ? ` at ${(host.cpuMhz / 1000).toFixed(1)} GHz` : ""}
            </span>
            <FactDot />
            <span className="numeric">{bytes(snapshot.memory.total, 0)} memory</span>
            <FactDot />
            <span className="numeric">
              {snapshot.swap.total > 0 ? `${bytes(snapshot.swap.total, 0)} swap` : "no swap"}
            </span>
            {host.virtualization && (
              <>
                <FactDot />
                <HostFact product={virtualizationProduct(host.virtualization)}>
                  {host.virtualization}
                </HostFact>
              </>
            )}
            <FactDot />
            <HostFact product={platformProduct(host.platform)}>{platformName(host)}</HostFact>
            <FactDot />
            {recorded.disabled ? (
              <Tag tone="warning">history off</Tag>
            ) : (
              recorded.history && (
                <span className="numeric">
                  sampled every {recorded.history.sampleIntervalSeconds}s, kept{" "}
                  {duration(recorded.history.retentionSeconds)}
                </span>
              )
            )}
          </>
        }
        aside={
          <div className="flex max-w-full flex-wrap items-center gap-2">
            {health && <HealthVerdict status={health.status} className="text-body" />}
            {live && (
              <IconAction
                label={paused ? "Resume live feed" : "Pause live feed"}
                onClick={() =>
                  setFrozen(paused ? null : { live, rows: liveChartRows, storage: liveStorage })
                }
              >
                {paused ? <Play /> : <Pause />}
              </IconAction>
            )}
            {/* Export the exact window and resolution on screen, peaks included. */}
            <Button variant="outline" size="sm" disabled={rows.length === 0} onClick={exportCsv}>
              <Download />
              Export CSV
            </Button>
            <RangePicker controls={controls} />
          </div>
        }
      />

      <Readings
        snapshot={snapshot}
        cores={cores}
        stats={stats}
        prior={priorStats}
        priorLabel={prior ? rangeSpec(win.key).label : null}
      />

      {/* The verdict is already in the facts row, and on the page made
          entirely of the numbers it was computed from, "Warning" with no way
          to ask why is a dead end. Rendered only when there is something to
          say: a server with nothing wrong loses no height to a list saying so. */}
      {health && health.findings.length > 0 && (
        <HealthPanel plain health={health} loading={healthLoading} />
      )}

      <div className="grid items-start gap-6 lg:grid-cols-2 [&>*]:min-w-0">
        <NotableMoments rows={rows} events={events} cores={cores} onZoom={zoom} />
        <TopProcesses />
      </div>

      <Section title="Utilisation">
        {recorded.error && <ErrorState error={recorded.error} />}

        <div className="grid gap-6 lg:grid-cols-2 [&>*]:min-w-0">
          <ProcessorPanel
            rows={rows}
            events={events}
            onZoom={zoom}
            showPeaks={showPeaks}
            note={note}
            now={snapshot}
          />

          <ChartPanel
            plain
            title="Memory and swap"
            rows={rows}
            series={memSeries}
            unit="%"
            domain={PERCENT_DOMAIN}
            events={events}
            onZoom={zoom}
            showPeaks={showPeaks}
            note={note}
            height={190}
          />
        </div>

        <ChartPanel
          plain
          title="Network throughput"
          rows={rows}
          series={netSeries}
          format={fmtRate}
          axisFormat={axisBytes}
          events={events}
          onZoom={zoom}
          showPeaks={showPeaks}
          note={note}
          height={180}
        />

        <div className="grid gap-6 lg:grid-cols-2 [&>*]:min-w-0">
          {/* Two charts rather than one with two axes. A capacity percentage
              and a byte rate share no scale, and overlaying them on twin axes
              invites the reader to infer a relationship between two lines that
              have nothing to do with each other. */}
          <ChartPanel
            plain
            title="Capacity"
            rows={storage.rows as { ts: number }[]}
            series={storageSeries}
            unit="%"
            domain={PERCENT_DOMAIN}
            format={fmtPercent0}
            events={events}
            onZoom={zoom}
            showPeaks={false}
            note={note}
            height={165}
            thresholds={DISK_THRESHOLD}
          />
          <ChartPanel
            plain
            title="Disk throughput"
            rows={rows}
            series={ioSeries}
            format={fmtRate}
            axisFormat={axisBytes}
            events={events}
            onZoom={zoom}
            showPeaks={showPeaks}
            note={note}
            height={165}
          />
        </div>
      </Section>

      {/*
        Saturation is a separate question from utilisation, and the reason most
        one-server dashboards leave people stuck. "The CPU is 40% busy" and
        "requests are queueing" are both true at once far more often than they
        look like they should be, and only the charts below can say so.
      */}
      <Section title="Saturation">
        <div className="grid gap-6 lg:grid-cols-2 [&>*]:min-w-0">
          <ChartPanel
            plain
            title="Pressure"
            rows={rows}
            series={pressureSeries}
            unit="%"
            domain={FROM_ZERO}
            events={events}
            onZoom={zoom}
            showPeaks={showPeaks}
            height={165}
            thresholds={PRESSURE_THRESHOLD}
            note={
              snapshot.pressure?.supported === false
                ? "This kernel does not expose /proc/pressure. Pressure needs Linux 4.20 or newer with PSI enabled."
                : note
            }
          />
          <ChartPanel
            plain
            title="Load average"
            rows={rows}
            series={loadSeries}
            format={fmtLoad}
            events={events}
            onZoom={zoom}
            showPeaks={showPeaks}
            height={165}
            thresholds={loadThreshold}
            note={
              live ? "Load averages are read from the recorded series — pick a range above." : note
            }
          />
          <ChartPanel
            plain
            title="Disk operations"
            rows={rows}
            series={iopsSeries}
            format={fmtOps}
            events={events}
            onZoom={zoom}
            showPeaks={showPeaks}
            height={165}
            note={note}
          />
          <ChartPanel
            plain
            title="Disk latency and busy time"
            rows={rows}
            series={latencySeries}
            format={fmtMillis}
            events={events}
            onZoom={zoom}
            showPeaks={showPeaks}
            height={165}
            note={note}
          />
          <ChartPanel
            plain
            title="Sockets"
            rows={rows}
            series={socketSeries}
            format={fmtCount}
            events={events}
            onZoom={zoom}
            showPeaks={showPeaks}
            height={165}
            note={note}
          />
          <InodePanel
            rows={storage.rows as { ts: number }[]}
            series={inodeSeries}
            events={events}
            onZoom={zoom}
            note={live ? "Inode usage is only in the recorded history — pick a range above." : note}
          />
        </div>
      </Section>

      <Section title="Hardware">
        <PerCorePanel cores={snapshot.cpu.perCore} />
        <SensorsPanel sensors={sensors} />
        <div className="grid items-start gap-6 lg:grid-cols-2 [&>*]:min-w-0">
          <MountsPanel snapshot={snapshot} />
          <InterfacesPanel snapshot={snapshot} />
        </div>
      </Section>
    </Page>
  )
}

/** The figures a tile compares between this window and the one before it. */
type Summary = {
  cpu: WindowStat | null
  mem: WindowStat | null
  load: WindowStat | null
  net: WindowStat | null
  disk: WindowStat | null
  tcp: WindowStat | null
}

function summarise(rows: ChartRow[]): Summary {
  return {
    cpu: windowStat(rows, "cpu", "cpuPeak"),
    mem: windowStat(rows, "mem", "memPeak"),
    load: windowStat(rows, "load1"),
    net: windowStatSum(rows, ["rx", "tx"]),
    disk: windowStatSum(rows, ["diskRead", "diskWrite"]),
    tcp: windowStat(rows, "tcp", "tcpPeak"),
  }
}

/**
 * The same span, one window earlier.
 *
 * Anchored to the recorded series' own end rather than to the instant of
 * render, and rounded to a coarse tick: the from/to pair is the request's
 * cache key, and a window that moved on every render would be re-fetched on
 * every poll of the main series. Null for the live buffer and for a dragged
 * span — the first has no "previous five minutes" on record, and the second is
 * a question about one moment rather than a comparison.
 */
function priorWindow(win: MetricsWindow, live: boolean, anchor: string | undefined) {
  if (live || win.from !== undefined || !anchor) return null
  const end = new Date(anchor).getTime()
  if (Number.isNaN(end)) return null
  const spec = rangeSpec(win.key)
  const step = Math.max(spec.refreshMs * 4, 60_000)
  const width = spec.seconds * 1000
  const to = Math.floor(end / step) * step - width
  return { key: win.key, from: to - width, to }
}

/**
 * The delta beside a headline figure: this window's mean against the prior
 * window's, as "+12%". Uncoloured on purpose — more network is not bad and
 * less CPU is not good, and a hue would claim to know which.
 */
function Delta({
  now,
  before,
  label,
}: {
  now: WindowStat | null | undefined
  before: WindowStat | null | undefined
  label: string | null
}) {
  if (!now || !before || !label) return null
  const delta = formatDelta(relativeChange(now.mean, before.mean))
  if (!delta) return null
  return (
    <span className="numeric" title={`this ${label} against the ${label} before it, by mean`}>
      {delta}
    </span>
  )
}

/**
 * The headline readings: ten figures from the newest frame, each with the
 * detail the figure hides and, on a recorded range, how it compares with the
 * previous window. A run of tiles rather than a run of cards, per §15.
 */
function Readings({
  snapshot,
  cores,
  stats,
  prior,
  priorLabel,
}: {
  snapshot: Snapshot
  cores: number
  stats: Summary
  prior: Summary | null
  priorLabel: string | null
}) {
  const modes = snapshot.cpu.modes
  const rx = snapshot.net.reduce((sum, n) => sum + n.recvRate, 0)
  const tx = snapshot.net.reduce((sum, n) => sum + n.sendRate, 0)
  const up = snapshot.net.filter((n) => n.isUp).length
  const disk = snapshot.mounts.reduce(
    (acc, m) => ({
      rate: acc.rate + m.readRate + m.writeRate,
      reads: acc.reads + (m.readOps ?? 0),
      writes: acc.writes + (m.writeOps ?? 0),
      // The busiest device and the slowest request, not the mean: one
      // saturated disk is the problem and three idle ones do not dilute it.
      busy: Math.max(acc.busy, m.busyPercent ?? 0),
      await: Math.max(acc.await, m.readLatencyMs ?? 0, m.writeLatencyMs ?? 0),
    }),
    { rate: 0, reads: 0, writes: 0, busy: 0, await: 0 },
  )
  const availPercent =
    snapshot.memory.total > 0 ? (snapshot.memory.available / snapshot.memory.total) * 100 : 0
  const fullest = snapshot.mounts.reduce<MountStats | undefined>(
    (worst, m) => (!worst || m.usedPercent > worst.usedPercent ? m : worst),
    undefined,
  )
  const psi = snapshot.pressure
  const worstPressure = psi?.supported
    ? [
        { name: "CPU", value: psi.cpuSome },
        { name: "memory", value: psi.memSome },
        { name: "I/O", value: psi.ioSome },
      ].reduce((worst, p) => (p.value > worst.value ? p : worst))
    : null
  const sockets = snapshot.sockets
  const procs = snapshot.procs
  const files = snapshot.files
  const hottest = snapshot.sensors?.[0]
  const filePercent = files && files.max > 0 ? (files.open / files.max) * 100 : null

  return (
    <StatGrid columns={5}>
      <StatTile
        label="CPU"
        value={percent(snapshot.cpu.totalPercent)}
        meter={snapshot.cpu.totalPercent}
        tone={utilisationTone(snapshot.cpu.totalPercent)}
        hint={
          modes
            ? `${modes.user.toFixed(0)}% user · ${modes.system.toFixed(0)}% sys · ${modes.iowait.toFixed(0)}% wait`
            : `${cores} cores`
        }
        trailing={
          modes && modes.steal >= 1 ? (
            <span className="numeric font-medium text-destructive">
              {percent(modes.steal, 0)} steal
            </span>
          ) : (
            (prior && <Delta now={stats.cpu} before={prior.cpu} label={priorLabel} />) ||
            `${cores} cores`
          )
        }
      />
      <StatTile
        label="Memory"
        value={bytes(snapshot.memory.available)}
        meter={100 - availPercent}
        tone={availPercent <= 5 ? "danger" : availPercent <= 10 ? "warning" : "default"}
        hint={`available · ${percent(snapshot.memory.usedPercent, 0)} used · ${bytes(snapshot.memory.cached)} cached`}
        trailing={
          (prior && <Delta now={stats.mem} before={prior.mem} label={priorLabel} />) || "available"
        }
      />
      <StatTile
        label="Load"
        value={snapshot.cpu.loadAvg1.toFixed(2)}
        meter={(snapshot.cpu.loadAvg1 / cores) * 100}
        tone={utilisationTone((snapshot.cpu.loadAvg5 / cores) * 100)}
        hint={`${snapshot.cpu.loadAvg5.toFixed(2)} · ${snapshot.cpu.loadAvg15.toFixed(2)} over 5 and 15 min`}
        trailing={
          (prior && <Delta now={stats.load} before={prior.load} label={priorLabel} />) ||
          `${(snapshot.cpu.loadAvg1 / cores).toFixed(2)}/core`
        }
      />
      {/* Pressure is the figure utilisation cannot give: a host at 40% CPU
          with 30% pressure is one where tasks spend a third of their time
          waiting for a core. The worst of the three is the headline. */}
      <StatTile
        label="Pressure"
        value={worstPressure ? percent(worstPressure.value, 1) : "—"}
        meter={worstPressure ? Math.min(worstPressure.value, 100) : undefined}
        tone={
          !worstPressure
            ? "default"
            : worstPressure.value >= 30
              ? "danger"
              : worstPressure.value >= 10
                ? "warning"
                : "default"
        }
        hint={
          psi?.supported
            ? `cpu ${percent(psi.cpuSome, 1)} · mem ${percent(psi.memSome, 1)} · io ${percent(psi.ioSome, 1)}`
            : "Kernel does not expose /proc/pressure"
        }
        trailing={worstPressure ? `${worstPressure.name} stalled` : "no PSI"}
      />
      <StatTile
        label="Network"
        value={rate(rx)}
        hint={`in · ${rate(tx)} out · ${up} of ${snapshot.net.length} interface${snapshot.net.length === 1 ? "" : "s"} up`}
        trailing={
          (prior && <Delta now={stats.net} before={prior.net} label={priorLabel} />) || "in"
        }
      />
      <StatTile
        label="Disk I/O"
        value={rate(disk.rate)}
        meter={disk.busy}
        tone={utilisationTone(disk.busy)}
        hint={`${Math.round(disk.reads)}/s reads · ${Math.round(disk.writes)}/s writes · ${disk.await.toFixed(1)} ms`}
        trailing={
          (prior && <Delta now={stats.disk} before={prior.disk} label={priorLabel} />) ||
          `${disk.busy.toFixed(0)}% busy`
        }
      />
      {/* The fullest real filesystem, because that is the one that stops the
          machine. */}
      <StatTile
        label="Storage"
        value={fullest ? bytes(fullest.free) : "—"}
        meter={fullest?.usedPercent}
        tone={fullest ? utilisationTone(fullest.usedPercent) : "default"}
        hint={
          fullest
            ? `${percent(fullest.usedPercent, 0)} used of ${bytes(fullest.total)} · ${snapshot.mounts.length} filesystem${snapshot.mounts.length === 1 ? "" : "s"}`
            : "No filesystems reported"
        }
        trailing={fullest && <span className="truncate">free on {fullest.mountpoint}</span>}
      />
      <StatTile
        label="Sockets"
        value={(sockets?.tcpInUse ?? 0).toLocaleString()}
        tone={(sockets?.tcpTimeWait ?? 0) >= 12_000 ? "warning" : "default"}
        hint={`${(sockets?.tcpTimeWait ?? 0).toLocaleString()} TIME_WAIT · ${(sockets?.udpInUse ?? 0).toLocaleString()} UDP · ${(sockets?.tcpOrphan ?? 0).toLocaleString()} orphaned`}
        trailing={
          (prior && <Delta now={stats.tcp} before={prior.tcp} label={priorLabel} />) || "TCP"
        }
      />
      {/* Blocked is the half of the story that turns high iowait from a
          curiosity into a cause: processes queued behind a device. */}
      <StatTile
        label="Processes"
        value={(procs?.total ?? 0).toLocaleString()}
        tone={(procs?.blocked ?? 0) > 0 ? "warning" : "default"}
        hint={`${procs?.running ?? 0} running · ${procs?.blocked ?? 0} blocked on I/O`}
        trailing="total"
      />
      {/* The tenth tile is whichever of the two live-only hardware readings
          this host can give: a temperature where the board reports one, and
          the file handle count everywhere else. Neither exists on a chart. */}
      {hottest ? (
        <StatTile
          label="Temperature"
          value={`${hottest.tempC.toFixed(0)}°C`}
          meter={
            hottest.critical > 0
              ? (hottest.tempC / hottest.critical) * 100
              : hottest.high > 0
                ? (hottest.tempC / hottest.high) * 100
                : undefined
          }
          tone={sensorTone(hottest)}
          hint={
            hottest.critical > 0
              ? `critical at ${hottest.critical.toFixed(0)}°C · ${snapshot.sensors?.length} sensor${snapshot.sensors?.length === 1 ? "" : "s"}`
              : `${snapshot.sensors?.length} sensor${snapshot.sensors?.length === 1 ? "" : "s"} · no limit reported`
          }
          trailing={<span className="truncate">{hottest.name}</span>}
        />
      ) : (
        <StatTile
          label="Open files"
          value={files ? files.open.toLocaleString() : "—"}
          meter={filePercent ?? undefined}
          tone={
            filePercent === null
              ? "default"
              : filePercent >= 90
                ? "danger"
                : filePercent >= 80
                  ? "warning"
                  : "default"
          }
          hint={
            !files
              ? "Not reported by this backend"
              : files.max > 0
                ? `${percent(filePercent ?? 0, 0)} of ${files.max.toLocaleString()} the kernel will allocate`
                : "Kernel reports no ceiling"
          }
          trailing="handles"
        />
      )}
    </StatGrid>
  )
}

/**
 * The processor panel, which can be read two ways.
 *
 * "Total" is the familiar one line. "Breakdown" is the one that answers the
 * question the total raises: a machine pinned at 90% is either working hard,
 * waiting on a disk, or losing its cores to another tenant.
 */
function ProcessorPanel({
  rows,
  events,
  onZoom,
  showPeaks,
  note,
  now,
}: {
  rows: ChartRow[]
  events: MetricEvent[]
  onZoom: (from: number, to: number) => void
  showPeaks: boolean
  note: string
  now: Snapshot
}) {
  // Total or the four modes. Somebody chasing steal on a VPS wants the
  // breakdown every time they open the page, not once.
  const [view, setView] = useViewState<"total" | "modes">("metrics.cpu.view", "total")
  const breakdown = view === "modes"

  return (
    <ChartPanel
      plain
      title="Processor"
      rows={rows}
      series={breakdown ? cpuModeSeries : cpuSeries}
      unit="%"
      domain={PERCENT_DOMAIN}
      events={events}
      onZoom={onZoom}
      showPeaks={showPeaks && !breakdown}
      stacked={breakdown}
      note={note}
      height={190}
      actions={
        <div className="flex items-center gap-2">
          <span className="numeric text-sm font-medium">{percent(now.cpu.totalPercent)}</span>
          <ToggleGroup
            type="single"
            value={view}
            onValueChange={(next) => setView((next as "total" | "modes") || view)}
            variant="outline"
            size="sm"
            aria-label="Processor view"
          >
            <ToggleGroupItem value="total" className="px-2 text-hint">
              Total
            </ToggleGroupItem>
            <ToggleGroupItem value="modes" className="px-2 text-hint">
              Breakdown
            </ToggleGroupItem>
          </ToggleGroup>
        </div>
      }
    />
  )
}

/**
 * Inode consumption per filesystem.
 *
 * Its own panel rather than a second line on the capacity chart: they are both
 * percentages but of completely different things, and a mount at 30% of its
 * bytes and 96% of its inodes needs those read as two facts.
 */
function InodePanel({
  rows,
  series,
  events,
  onZoom,
  note,
}: {
  rows: { ts: number }[]
  series: Series[]
  events: MetricEvent[]
  onZoom: (from: number, to: number) => void
  note: string
}) {
  return (
    <ChartPanel
      plain
      title="Inodes"
      rows={rows}
      series={series}
      unit="%"
      domain={PERCENT_DOMAIN}
      format={fmtPercent0}
      events={events}
      onZoom={onZoom}
      showPeaks={false}
      height={165}
      thresholds={INODE_THRESHOLD}
      note={note}
    />
  )
}

/**
 * The "one runnable task per core" line, cached per core count.
 */
let coreThresholdCache: {
  cores: number
  value: { value: number; label: string; tone: "warning" }[]
} | null = null
function coreThreshold(cores: number) {
  if (!coreThresholdCache || coreThresholdCache.cores !== cores) {
    coreThresholdCache = {
      cores,
      value: [{ value: cores, label: `${cores} cores`, tone: "warning" }],
    }
  }
  return coreThresholdCache.value
}

/**
 * What a chart shows when it has nothing to draw.
 */
function emptyChartNote(live: boolean, recorded: HistoryState): string {
  if (live) return "Waiting for the first frame…"
  if (recorded.disabled) {
    return "History is not being recorded on this server. Set JD_METRICS_RETENTION to keep it."
  }
  if (recorded.loading) return "Loading history…"
  if (recorded.error) return "History could not be read."
  const every = recorded.history?.sampleIntervalSeconds
  return every
    ? `Nothing recorded in this window yet — the server samples every ${every}s.`
    : "Nothing recorded in this window yet."
}
