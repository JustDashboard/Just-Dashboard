"use client"

import { useMemo, useState } from "react"
import { Download, Pause, Play } from "@/components/icons"
import { bytes, duration, percent, rate } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { MetricEvent, Snapshot } from "@/lib/types"
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
import type { Tone } from "@/components/tone"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { HealthPanel, HealthVerdict } from "@/components/metrics/health-panel"
import { RangePicker, windowSpanNote } from "@/components/metrics/range-picker"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { MomentInspector } from "@/components/metrics/moment-inspector"
import { getCrosshair, unpinCrosshair } from "@/lib/metrics-crosshair"
import { NotableMoments } from "@/components/metrics/notable-moments"
import { TopProcesses } from "@/components/metrics/top-processes"
import { TileTrend } from "@/components/metrics/sparkline"
import {
  InterfacesPanel,
  MemoryPanel,
  MountsPanel,
  PerCorePanel,
  SensorsPanel,
} from "@/components/metrics/hardware-panels"
import type { Series } from "@/components/metrics/metric-chart"
import {
  CoreBars,
  HUE,
  LiveBytes,
  LiveFigure,
  SeriesKey,
  StreamState,
} from "@/components/overview/readings"
import { ErrorState } from "@/components/state"
import { Tag } from "@/components/tag"
import { FactDot, HostFact, HostIdentity, platformName } from "@/components/metrics/host-identity"
import { cpuProduct, platformProduct, virtualizationProduct } from "@/components/product-logo"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

/*
 * A series' hue is the measurement's, never its position on a chart (§10).
 * The five readings at the top take the Overview's five (`HUE`), and every
 * line of the same measurement below takes the same one: the processor is the
 * pale line, memory the blue, load the pink, the network green and the disks
 * violet, in the tiles, in the charts and in the Pressure chart that draws
 * three of them at once. A second line on a chart takes the colour that
 * stands furthest from its first — out is blue against in's green, write is
 * pink against read's violet.
 */
const OUT = "var(--chart-2)"
const WRITE = "var(--chart-3)"

// Peaks share their series' colour: they are the same measurement seen at a
// finer resolution, not a different quantity, and giving them a colour of
// their own would read as four unrelated lines instead of two pairs.
const cpuSeries: Series[] = [
  { key: "cpu", label: "CPU", color: HUE.cpu, kind: "area", peakKey: "cpuPeak" },
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
  { key: "cpuUser", label: "User", color: HUE.cpu, stack: "cpu" },
  { key: "cpuSystem", label: "System", color: "var(--chart-2)", stack: "cpu" },
  { key: "cpuIowait", label: "I/O wait", color: HUE.disk, stack: "cpu" },
  { key: "cpuSteal", label: "Steal", color: "var(--destructive)", stack: "cpu" },
]

const memSeries: Series[] = [
  { key: "mem", label: "Memory", color: HUE.mem, peakKey: "memPeak" },
  { key: "swap", label: "Swap", color: HUE.disk },
]

const netSeries: Series[] = [
  { key: "rx", label: "In", color: HUE.net, kind: "area", peakKey: "rxPeak" },
  { key: "tx", label: "Out", color: OUT, kind: "area", peakKey: "txPeak" },
]

const ioSeries: Series[] = [
  { key: "diskRead", label: "Read", color: HUE.disk, kind: "area", peakKey: "diskReadPeak" },
  { key: "diskWrite", label: "Write", color: WRITE, kind: "area", peakKey: "diskWritePeak" },
]

const iopsSeries: Series[] = [
  { key: "diskReads", label: "Reads/s", color: HUE.disk, peakKey: "diskReadsPeak" },
  { key: "diskWrites", label: "Writes/s", color: WRITE, peakKey: "diskWritesPeak" },
]

const latencySeries: Series[] = [
  { key: "diskAwait", label: "Latency", color: HUE.disk, kind: "area", peakKey: "diskAwaitPeak" },
  { key: "diskBusy", label: "Busy", color: WRITE },
]

const pressureSeries: Series[] = [
  { key: "psiCpu", label: "CPU", color: HUE.cpu, peakKey: "psiCpuPeak" },
  { key: "psiMem", label: "Memory", color: HUE.mem, peakKey: "psiMemPeak" },
  { key: "psiIo", label: "I/O", color: HUE.disk, peakKey: "psiIoPeak" },
]

const loadSeries: Series[] = [
  { key: "load1", label: "1 min", color: HUE.load, peakKey: "load1Peak" },
  { key: "load5", label: "5 min", color: HUE.disk },
  { key: "load15", label: "15 min", color: OUT },
]

const socketSeries: Series[] = [
  { key: "tcp", label: "TCP in use", color: HUE.net, kind: "area", peakKey: "tcpPeak" },
  { key: "tcpTimeWait", label: "TIME_WAIT", color: OUT },
]

/*
 * How connections fared rather than how many there were: the share of
 * segments TCP had to send again, the kernel's round trip of established
 * connections to peers elsewhere, and the attempts that never became a
 * connection. Three charts, because a percentage, milliseconds and a rate
 * share no scale.
 */
const retransSeries: Series[] = [
  { key: "retransPct", label: "Resent", color: HUE.net, kind: "area", peakKey: "retransPctPeak" },
]

const rttSeries: Series[] = [{ key: "rtt", label: "Median RTT", color: OUT, peakKey: "rttPeak" }]

const failureSeries: Series[] = [
  { key: "attemptFails", label: "Failed attempts", color: WRITE, peakKey: "attemptFailsPeak" },
  {
    key: "listenDrops",
    label: "Refused at accept",
    color: "var(--destructive)",
    peakKey: "listenDropsPeak",
  },
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
const fmtPercent2 = (v: number) => percent(v, 2)
const fmtMs = (v: number) => `${Math.round(v)} ms`
const fmtPerSecond = (v: number) => `${v.toFixed(2)}/s`

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
  const { host, snapshot, history, error, connection } = useMetrics()
  const controls = useMetricsWindow()
  const win = controls.window
  const recorded = useMetricsHistory(win)
  const recordedStorage = useStorageHistory(win)
  const events = useMetricEvents(win)
  const { health, loading: healthLoading, error: healthError } = useHealth()

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
  const trends = useMemo(() => trendsOf(rows), [rows])

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
                <Skeleton key={i} className="h-30 rounded-xl" />
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
  const span = spanPhrase(win, live)

  const exportCsv = () => {
    const stamp = new Date().toISOString().slice(0, 16).replace(/[:T]/g, "-")
    downloadText(`${host.hostname}-metrics-${windowLabel(win)}-${stamp}.csv`, rowsToCsv(rows))
  }

  return (
    <Workspace
      name="Metrics"
      rows={false}
      search={false}
      refresh={() => window.dispatchEvent(new Event("jd:metrics-refresh"))}
      escape={() => {
        if (!getCrosshair().pinned) return false
        unpinCrosshair()
        return true
      }}
    >
      <Page className="animate-rise">
        <PageContext title="Metrics" />

        {/* What this machine is made of. The Overview's line says what it
          runs; this one says what it runs on, drawn as the processor itself,
          because every figure below is a share of something named here. The
          window's controls sit at its end because they change every chart
          under it; the shortcuts are the key beside them, where they had been
          a line of their own above the page. */}
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
              {health && (
                <HealthVerdict
                  partial={!!health.silences?.length}
                  status={health.status}
                  className="mr-2 text-body"
                />
              )}
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
              <WorkspaceHelp compact />
            </div>
          }
        />

        <MomentInspector samples={rows} />

        {/* The readings that move, drawn as the Overview draws them: each
          figure glides to every frame of the socket, keyed by the colour of
          its line, with the window on screen as its trend — so picking 24h
          redraws five shapes here before the charts below have been read.
          The five tiles that stood under these said what a section below
          says better, and went to it: storage to Filesystems, pressure to its
          chart, sockets and open files to Sockets, processes to Load, the
          hottest sensor to Temperatures. */}
        <Section
          title="Resources"
          actions={
            <div className="flex items-center gap-4">
              <span className="numeric text-hint text-muted-foreground">{span.head}</span>
              <StreamState connection={connection} />
            </div>
          }
        >
          <Readings
            snapshot={snapshot}
            cores={cores}
            stats={stats}
            prior={priorStats}
            priorLabel={prior ? rangeSpec(win.key).label : null}
            trends={trends}
            over={span.over}
          />
        </Section>

        {/* The verdict is already in the facts row, and on the page made
          entirely of the numbers it was computed from, "Warning" with no way
          to ask why is a dead end. A clean server keeps the strip of areas
          checked — a line of green that says what was looked at — rather
          than losing the panel and leaving the verdict unexplained. */}
        <HealthPanel plain health={health} error={healthError} loading={healthLoading} />

        <div className="grid items-start gap-8 lg:grid-cols-2 [&>*]:min-w-0">
          <NotableMoments rows={rows} events={events} cores={cores} onZoom={zoom} />
          <TopProcesses />
        </div>

        {recorded.error && <ErrorState error={recorded.error} />}

        {/*
          One section per resource, each reading what it is now beside what it
          did over the window: the processor's chart beside its cores, memory's
          split beside its line, the disks beside their capacity. The page was
          two long runs of charts — utilisation, then saturation — under which
          the cores, temperatures, disks and interfaces sat as a third, so the
          memory chart and how that memory is held were a screen apart.
        */}
        <Section title="Processor">
          <div className="grid gap-8 lg:grid-cols-2 [&>*]:min-w-0">
            <ProcessorPanel
              rows={rows}
              events={events}
              onZoom={zoom}
              showPeaks={showPeaks}
              note={note}
              now={snapshot}
            />
            <div className="flex min-w-0 flex-col gap-8">
              <PerCorePanel cores={snapshot.cpu.perCore} color={HUE.cpu} />
              <SensorsPanel sensors={sensors} />
            </div>
          </div>
        </Section>

        <Section title="Memory">
          <div className="grid gap-8 lg:grid-cols-2 [&>*]:min-w-0">
            <MemoryPanel
              memory={snapshot.memory}
              swap={snapshot.swap}
              color={HUE.mem}
              swapColor={HUE.disk}
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
        </Section>

        <Section title="Network">
          <div className="grid gap-8 lg:grid-cols-2 [&>*]:min-w-0">
            <ChartPanel
              plain
              title="Throughput"
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
            <ChartPanel
              plain
              title="Sockets"
              actions={<SocketReading snapshot={snapshot} />}
              rows={rows}
              series={socketSeries}
              format={fmtCount}
              events={events}
              onZoom={zoom}
              showPeaks={showPeaks}
              height={180}
              note={note}
            />
          </div>
          <div className="grid gap-8 lg:grid-cols-3 [&>*]:min-w-0">
            <ChartPanel
              plain
              title="Resent segments"
              actions={<ConnectionReading snapshot={snapshot} />}
              rows={rows}
              series={retransSeries}
              format={fmtPercent2}
              events={events}
              onZoom={zoom}
              showPeaks={showPeaks}
              height={160}
              note={live ? RECORDED_ONLY : note}
            />
            <ChartPanel
              plain
              title="Connection RTT"
              rows={rows}
              series={rttSeries}
              format={fmtMs}
              events={events}
              onZoom={zoom}
              showPeaks={showPeaks}
              height={160}
              note={live ? RECORDED_ONLY : note}
            />
            <ChartPanel
              plain
              title="Failed connections"
              rows={rows}
              series={failureSeries}
              format={fmtPerSecond}
              events={events}
              onZoom={zoom}
              showPeaks={showPeaks}
              height={160}
              note={live ? RECORDED_ONLY : note}
            />
          </div>
          <InterfacesPanel snapshot={snapshot} colors={{ in: HUE.net, out: OUT }} />
        </Section>

        <Section title="Storage">
          <div className="grid gap-8 lg:grid-cols-2 [&>*]:min-w-0">
            <MountsPanel snapshot={snapshot} />
            {/* Its own chart rather than a second axis on throughput. A
              capacity percentage and a byte rate share no scale, and twin
              axes invite the reader to relate two lines that have nothing to
              do with each other. */}
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
            <InodePanel
              rows={storage.rows as { ts: number }[]}
              series={inodeSeries}
              events={events}
              onZoom={zoom}
              note={
                live ? "Inode usage is only in the recorded history — pick a range above." : note
              }
            />
          </div>
        </Section>

        {/*
          Saturation is a separate question from utilisation, and the reason most
          one-server dashboards leave people stuck. "The CPU is 40% busy" and
          "requests are queueing" are both true at once far more often than they
          look like they should be, and only these two charts can say so. Each
          head reads what it is now: the three stalls, and the processes the
          load average counts.
        */}
        <Section title="Saturation">
          <div className="grid gap-8 lg:grid-cols-2 [&>*]:min-w-0">
            <ChartPanel
              plain
              title="Pressure"
              actions={<PressureReading snapshot={snapshot} />}
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
              actions={<ProcessReading snapshot={snapshot} />}
              rows={rows}
              series={loadSeries}
              format={fmtLoad}
              events={events}
              onZoom={zoom}
              showPeaks={showPeaks}
              height={165}
              thresholds={loadThreshold}
              note={
                live
                  ? "Load averages are read from the recorded series — pick a range above."
                  : note
              }
            />
          </div>
        </Section>
      </Page>
    </Workspace>
  )
}

/** The figures a tile compares between this window and the one before it. */
type Summary = {
  cpu: WindowStat | null
  mem: WindowStat | null
  load: WindowStat | null
  net: WindowStat | null
  disk: WindowStat | null
}

function summarise(rows: ChartRow[]): Summary {
  return {
    cpu: windowStat(rows, "cpu", "cpuPeak"),
    mem: windowStat(rows, "mem", "memPeak"),
    load: windowStat(rows, "load1"),
    net: windowStatSum(rows, ["rx", "tx"]),
    disk: windowStatSum(rows, ["diskRead", "diskWrite"]),
  }
}

type Trends = Record<keyof Summary, number[]>

/**
 * Each tile's line, read from the rows the charts draw, so a tile's trend and
 * the chart of the same measurement are one window at one resolution. A gap
 * in the record is left out rather than drawn as a drop to zero.
 */
function trendsOf(rows: ChartRow[]): Trends {
  const out: Trends = { cpu: [], mem: [], load: [], net: [], disk: [] }
  for (const row of rows) {
    if (row.cpu !== null) out.cpu.push(row.cpu)
    if (row.mem !== null) out.mem.push(row.mem)
    if (row.load1 !== null) out.load.push(row.load1)
    if (row.rx !== null || row.tx !== null) out.net.push((row.rx ?? 0) + (row.tx ?? 0))
    if (row.diskRead !== null || row.diskWrite !== null) {
      out.disk.push((row.diskRead ?? 0) + (row.diskWrite ?? 0))
    }
  }
  return out
}

/**
 * The window on screen as the tiles' trends cover it: "Last hour" in the
 * head, "the last hour" in each trend's name. A dragged span is its two ends,
 * since the range picker beside the identity line already says how wide it is.
 */
function spanPhrase(win: MetricsWindow, live: boolean): { head: string; over: string } {
  const zoomed = windowSpanNote(win)
  if (!live && zoomed) return { head: zoomed, over: "the window on screen" }
  const head = live
    ? "Last 5 minutes"
    : {
        live: "Last 5 minutes",
        "1h": "Last hour",
        "6h": "Last 6 hours",
        "24h": "Last 24 hours",
        "7d": "Last 7 days",
      }[win.key]
  return { head, over: `the ${head.toLowerCase()}` }
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
 * A tile's unit word with, on a recorded range, how its mean compares with
 * the window before — "free · +12%". The change is uncoloured on purpose:
 * more network is not bad and less CPU is not good, and a hue would claim to
 * know which.
 */
function Trailing({
  unit,
  now,
  before,
  label,
}: {
  unit?: React.ReactNode
  now: WindowStat | null | undefined
  before: WindowStat | null | undefined
  label: string | null
}) {
  const delta = now && before && label ? formatDelta(relativeChange(now.mean, before.mean)) : null
  if (!delta) return unit ?? null
  return (
    <span className="numeric">
      {unit}
      {unit && " · "}
      <span title={`this ${label} against the ${label} before it, by mean`}>{delta}</span>
    </span>
  )
}

/**
 * The readings that move: five figures from the newest frame, each gliding to
 * the next, keyed by and drawn over its line across the window on screen —
 * the Overview's four and its Disk I/O, in the same colours. A reading the
 * window has no line for yet — load on the live feed, anything while the
 * record is off or the feed has one frame — keeps a meter where its trend
 * would be, against its ceiling where it has one.
 */
function Readings({
  snapshot,
  cores,
  stats,
  prior,
  priorLabel,
  trends,
  over,
}: {
  snapshot: Snapshot
  cores: number
  stats: Summary
  prior: Summary | null
  priorLabel: string | null
  trends: Trends
  /** The window as the trends' names say it: "the last hour". */
  over: string
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
  const memTone: Tone = availPercent <= 5 ? "danger" : availPercent <= 10 ? "warning" : "default"

  const trend = (values: number[], label: string, color: string, max?: number) =>
    values.length < 2 ? undefined : (
      <TileTrend values={values} max={max} color={color} label={`${label} over ${over}`} />
    )
  const cpuTrend = trend(trends.cpu, "CPU", HUE.cpu, 100)
  const memTrend = trend(trends.mem, "Memory", HUE.mem, 100)
  const loadTrend = trend(trends.load, "Load", HUE.load, cores)
  const diskTrend = trend(trends.disk, "Disk I/O", HUE.disk)

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
        value={<LiveFigure value={snapshot.cpu.totalPercent} decimals={1} unit="%" />}
        tone={utilisationTone(snapshot.cpu.totalPercent)}
        trend={cpuTrend}
        meter={cpuTrend ? undefined : snapshot.cpu.totalPercent}
        hint={
          modes
            ? `${modes.user.toFixed(0)}% user · ${modes.system.toFixed(0)}% sys · ${modes.iowait.toFixed(0)}% wait`
            : `${cores} cores`
        }
        trailing={
          <span className="inline-flex items-center gap-2">
            <CoreBars cores={snapshot.cpu.perCore} color={HUE.cpu} />
            {modes && modes.steal >= 1 ? (
              <span className="numeric font-medium text-destructive">
                {percent(modes.steal, 0)} steal
              </span>
            ) : (
              prior && <Trailing now={stats.cpu} before={prior.cpu} label={priorLabel} />
            )}
          </span>
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
        value={<LiveBytes value={snapshot.memory.available} />}
        tone={memTone}
        trend={memTrend}
        meter={memTrend ? undefined : 100 - availPercent}
        hint={`${percent(snapshot.memory.usedPercent, 0)} used · ${bytes(snapshot.memory.cached)} cached`}
        trailing={<Trailing unit="free" now={stats.mem} before={prior?.mem} label={priorLabel} />}
      />
      <StatTile
        label={
          <>
            <SeriesKey color={HUE.load} />
            Load
          </>
        }
        meterLabel="Load"
        value={<LiveFigure value={snapshot.cpu.loadAvg1} decimals={2} />}
        tone={utilisationTone((snapshot.cpu.loadAvg5 / cores) * 100)}
        trend={loadTrend}
        meter={loadTrend ? undefined : (snapshot.cpu.loadAvg1 / cores) * 100}
        hint={`${snapshot.cpu.loadAvg5.toFixed(2)} · ${snapshot.cpu.loadAvg15.toFixed(2)} over 5 and 15 min`}
        trailing={
          <Trailing
            unit={`${(snapshot.cpu.loadAvg1 / cores).toFixed(2)}/core`}
            now={stats.load}
            before={prior?.load}
            label={priorLabel}
          />
        }
      />
      <StatTile
        label={
          <>
            <SeriesKey color={HUE.net} />
            Network
          </>
        }
        value={<LiveBytes value={rx} suffix="/s" />}
        trend={trend(trends.net, "Network", HUE.net)}
        hint={`${rate(tx)} out · ${up} of ${snapshot.net.length} interface${snapshot.net.length === 1 ? "" : "s"} up`}
        trailing={<Trailing unit="in" now={stats.net} before={prior?.net} label={priorLabel} />}
      />
      <StatTile
        label={
          <>
            <SeriesKey color={HUE.disk} />
            Disk I/O
          </>
        }
        value={<LiveBytes value={disk.rate} suffix="/s" />}
        tone={utilisationTone(disk.busy)}
        trend={diskTrend}
        meter={diskTrend ? undefined : disk.busy}
        hint={`${Math.round(disk.reads)}/s reads · ${Math.round(disk.writes)}/s writes · ${disk.await.toFixed(1)} ms`}
        trailing={
          <Trailing
            unit={`${disk.busy.toFixed(0)}% busy`}
            now={stats.disk}
            before={prior?.disk}
            label={priorLabel}
          />
        }
      />
    </StatGrid>
  )
}

/**
 * What a chart's measurement is now, in its head: each figure keyed by its
 * line's colour, the way a project's Runtime heads its charts, so the head
 * and the legend's Now column are read as one.
 */
function HeadReading({
  items,
}: {
  items: { label: string; value: string; color?: string; tone?: Tone }[]
}) {
  return (
    <span className="numeric flex flex-wrap items-baseline justify-end gap-x-3 gap-y-1 text-hint text-muted-foreground">
      {items.map((item) => (
        <span key={item.label} className="whitespace-nowrap">
          {item.color && <SeriesKey color={item.color} />}
          <span
            className={cn(
              "font-medium text-foreground",
              item.tone === "warning" && "text-warning",
              item.tone === "danger" && "text-destructive",
            )}
          >
            {item.value}
          </span>{" "}
          {item.label}
        </span>
      ))}
    </span>
  )
}

/** The three stalls now. The worst of them is the one past the chart's warning line. */
function PressureReading({ snapshot }: { snapshot: Snapshot }) {
  const psi = snapshot.pressure
  if (!psi?.supported) return null
  const tone = (value: number): Tone =>
    value >= 30 ? "danger" : value >= 10 ? "warning" : "default"
  return (
    <HeadReading
      items={[
        { label: "CPU", value: percent(psi.cpuSome, 1), color: HUE.cpu, tone: tone(psi.cpuSome) },
        {
          label: "memory",
          value: percent(psi.memSome, 1),
          color: HUE.mem,
          tone: tone(psi.memSome),
        },
        { label: "I/O", value: percent(psi.ioSome, 1), color: HUE.disk, tone: tone(psi.ioSome) },
      ]}
    />
  )
}

/**
 * The processes the load average counts: the ones running and the ones
 * blocked on I/O, which is the half that turns high iowait from a curiosity
 * into a cause.
 */
function ProcessReading({ snapshot }: { snapshot: Snapshot }) {
  const procs = snapshot.procs
  if (!procs) return null
  return (
    <HeadReading
      items={[
        { label: "processes", value: procs.total.toLocaleString() },
        { label: "running", value: procs.running.toLocaleString() },
        {
          label: "blocked",
          value: procs.blocked.toLocaleString(),
          tone: procs.blocked > 0 ? "warning" : "default",
        },
      ]}
    />
  )
}

/**
 * Sockets and the handles they are made of, now: TCP in use, TIME_WAIT past
 * the point where ephemeral ports start to run short, and the kernel's open
 * file handles against its ceiling where it has one.
 */
const RECORDED_ONLY = "Recorded every interval, not streamed: pick a range to see it."

/**
 * How connections fare now, from the newest frame: the share of segments
 * resent in its interval and the median RTT of established connections to
 * peers elsewhere — "none" where nothing is connected, never a zero.
 */
function ConnectionReading({ snapshot }: { snapshot: Snapshot }) {
  const tcp = snapshot.tcp
  if (!tcp?.supported) return null
  return (
    <HeadReading
      items={[
        {
          label: "resent",
          value: percent(tcp.retransPercent, 2),
          color: HUE.net,
          tone: tcp.retransPercent >= 5 ? "warning" : "default",
        },
        {
          label: "RTT",
          value: tcp.latency.sockets > 0 ? `${Math.round(tcp.latency.medianMs)} ms` : "none",
          color: OUT,
        },
      ]}
    />
  )
}

function SocketReading({ snapshot }: { snapshot: Snapshot }) {
  const sockets = snapshot.sockets
  const files = snapshot.files
  const filePercent = files && files.max > 0 ? (files.open / files.max) * 100 : null
  return (
    <HeadReading
      items={[
        { label: "TCP", value: (sockets?.tcpInUse ?? 0).toLocaleString(), color: HUE.net },
        {
          label: "TIME_WAIT",
          value: (sockets?.tcpTimeWait ?? 0).toLocaleString(),
          color: OUT,
          tone: (sockets?.tcpTimeWait ?? 0) >= 12_000 ? "warning" : "default",
        },
        ...(files
          ? [
              {
                label: "open files",
                value: files.open.toLocaleString(),
                tone: (filePercent === null
                  ? "default"
                  : filePercent >= 90
                    ? "danger"
                    : filePercent >= 80
                      ? "warning"
                      : "default") as Tone,
              },
            ]
          : []),
      ]}
    />
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
      title="Utilisation"
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
        <div className="flex items-center gap-3">
          <HeadReading
            items={[
              {
                label: "busy",
                value: percent(now.cpu.totalPercent),
                color: HUE.cpu,
                tone: utilisationTone(now.cpu.totalPercent),
              },
            ]}
          />
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
