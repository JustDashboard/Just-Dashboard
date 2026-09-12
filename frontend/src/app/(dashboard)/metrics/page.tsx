"use client"

import { useMemo, useRef, useState } from "react"
import {
  ChartActivity,
  Cpu,
  Gauge,
  GridSquare,
  NetworkDevice,
  Router,
  Servers,
  Warning,
} from "@/components/icons"
import { get } from "@/lib/api"
import { cn } from "@/lib/utils"
import { bytes, percent, rate } from "@/lib/format"
import type { DirEntry, MetricEvent, Snapshot } from "@/lib/types"
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
  storageRows,
  type ChartRow,
  type StorageSeriesMeta,
} from "@/lib/metrics-range"
import { Page, PageHeader, PageState, Metric, MetricStrip, Section } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Meter, utilisationTone } from "@/components/meter"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { HealthVerdict } from "@/components/metrics/health-panel"
import { RangePicker } from "@/components/metrics/range-picker"
import type { Series } from "@/components/metrics/metric-chart"
import { EmptyState, ErrorState } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

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
const NO_STORAGE: { rows: Record<string, number | string | null>[]; series: StorageSeriesMeta[] } =
  {
    rows: [],
    series: [],
  }

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
  const { health } = useHealth()

  const live = win.key === "live" && win.from === undefined

  const liveChartRows = useMemo(() => (live ? liveRows(history) : NO_ROWS), [live, history])
  const recordedChartRows = useMemo(
    () => (!live && recorded.history ? historyRows(recorded.history) : NO_ROWS),
    [live, recorded.history],
  )
  const rows = live ? liveChartRows : recordedChartRows

  const liveStorage = useMemo(
    () => (live ? liveStorageRows(history, snapshot?.mounts ?? []) : NO_STORAGE),
    [live, history, snapshot?.mounts],
  )
  const recordedStorageRows = useMemo(
    () => (!live && recordedStorage.storage ? storageRows(recordedStorage.storage) : NO_STORAGE),
    [live, recordedStorage.storage],
  )
  const storage = live ? liveStorage : recordedStorageRows

  const storageSeries = useMemo<Series[]>(
    () => storage.series.map((m) => ({ key: m.key, label: m.mountpoint, color: m.color })),
    [storage.series],
  )
  const inodeSeries = useMemo<Series[]>(
    () => storage.series.map((m) => ({ key: `${m.key}i`, label: m.mountpoint, color: m.color })),
    [storage.series],
  )

  if (!snapshot || !host || (error && !snapshot)) {
    return (
      <PageState
        eyebrow="Server"
        title="Metrics"
        error={error && !snapshot ? new Error(error) : undefined}
        skeleton={
          <>
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

  return (
    <Page>
      <PageHeader
        eyebrow="Server"
        title="Metrics"
        actions={
          <div className="flex items-center gap-3">
            {health && <HealthVerdict status={health.status} />}
            <RangePicker controls={controls} />
          </div>
        }
      />

      <Section
        title="Utilisation"
      >
        {recorded.error && <ErrorState error={recorded.error} />}

        <div className="grid gap-4 lg:grid-cols-2 [&>*]:min-w-0">
          <ProcessorPanel
            rows={rows}
            events={events}
            onZoom={zoom}
            showPeaks={showPeaks}
            note={note}
            model={host.cpuModel}
            now={snapshot}
          />

          <ChartPanel
            icon={GridSquare}
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
            footer={
              <MetricStrip className="[&>*]:flex-1">
                <Metric label="Used" value={bytes(snapshot.memory.used)} />
                <Metric label="Cached" value={bytes(snapshot.memory.cached)} />
                <Metric label="Buffers" value={bytes(snapshot.memory.buffers)} />
                <Metric label="Available" value={bytes(snapshot.memory.available)} />
              </MetricStrip>
            }
          />
        </div>

        <ChartPanel
          icon={ChartActivity}
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

        <div className="grid gap-4 lg:grid-cols-2 [&>*]:min-w-0">
          {/* Two charts rather than one with two axes. A capacity percentage
              and a byte rate share no scale, and overlaying them on twin axes
              invites the reader to infer a relationship between two lines that
              have nothing to do with each other. */}
          <ChartPanel
            icon={Servers}
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
            icon={Servers}
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
      <Section
        title="Saturation"
      >
        <div className="grid gap-4 lg:grid-cols-2 [&>*]:min-w-0">
          <ChartPanel
            icon={Warning}
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
            icon={Gauge}
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
            icon={Servers}
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
            icon={ChartActivity}
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
        </div>

        <div className="grid gap-4 lg:grid-cols-2 [&>*]:min-w-0">
          <ChartPanel
            icon={Router}
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
        {snapshot.cpu.perCore.length > 0 && (
          <Panel>
            <PanelHeader
              icon={Cpu}
              title="Per-core utilisation"
            />
            <PanelBody>
              <PerCoreBars cores={snapshot.cpu.perCore} />
            </PanelBody>
          </Panel>
        )}

        <div className="grid items-start gap-4 lg:grid-cols-2 [&>*]:min-w-0">
          <MountsPanel snapshot={snapshot} />
          <InterfacesPanel snapshot={snapshot} />
        </div>
      </Section>
    </Page>
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
  model: string
  now: Snapshot
}) {
  // Total or the four modes. Somebody chasing steal on a VPS wants the
  // breakdown every time they open the page, not once.
  const [view, setView] = useViewState<"total" | "modes">("metrics.cpu.view", "total")
  const breakdown = view === "modes"

  return (
    <ChartPanel
      icon={Cpu}
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
      icon={Servers}
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

function PerCoreBars({ cores }: { cores: number[] }) {
  if (cores.length === 0) return null
  return (
    // As many columns as fit the panel, not a fixed two: the track count follows
    // the panel's own width rather than the viewport's.
    <div className="grid grid-cols-[repeat(auto-fit,minmax(9rem,1fr))] gap-x-4 gap-y-1.5">
      {cores.map((value, i) => (
        <div key={i} className="flex min-w-0 items-center gap-2">
          <span className="w-9 shrink-0 font-mono text-micro text-muted-foreground">cpu{i}</span>
          <Meter
            value={value}
            tone={utilisationTone(value)}
            size="thin"
            label={`cpu${i}`}
            className="flex-1"
          />
          <span className="numeric w-8 shrink-0 text-right font-mono text-micro text-muted-foreground">
            {value.toFixed(0)}%
          </span>
        </div>
      ))}
    </div>
  )
}

function MountsPanel({ snapshot }: { snapshot: Snapshot }) {
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
    <Panel>
      <PanelHeader
        icon={Servers}
        title="Filesystems"
      />
      <PanelBody className="space-y-4">
        {snapshot.mounts.map((mount) => {
          const tone = utilisationTone(mount.usedPercent)
          const inodes = mount.inodesTotal > 0 ? (mount.inodesUsed / mount.inodesTotal) * 100 : 0
          return (
            <div key={mount.mountpoint} className="min-w-0 space-y-2">
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0 flex-1">
                  <div className="flex min-w-0 items-center gap-2">
                    <span className="truncate text-body font-medium">{mount.mountpoint}</span>
                    <Tag>{mount.fstype}</Tag>
                    {inodes >= 80 && (
                      <span className="numeric shrink-0 text-hint font-medium text-warning">
                        {inodes.toFixed(0)}% inodes
                      </span>
                    )}
                  </div>
                  <p className="truncate font-mono text-hint text-muted-foreground">
                    {mount.device}
                  </p>
                </div>
                <div className="shrink-0 text-right">
                  <div className="numeric text-body">
                    {bytes(mount.used)}{" "}
                    <span className="text-muted-foreground">/ {bytes(mount.total)}</span>
                  </div>
                  <p className="numeric text-hint text-muted-foreground">
                    {rate(mount.readRate)} read · {rate(mount.writeRate)} write
                  </p>
                </div>
              </div>
              <div className="flex items-center gap-2">
                <Meter
                  value={mount.usedPercent}
                  tone={tone}
                  size="thin"
                  label={mount.mountpoint}
                  className="flex-1"
                />
                <span
                  className={cn(
                    "numeric w-9 text-right font-mono text-hint",
                    tone === "danger"
                      ? "text-destructive"
                      : tone === "warning"
                        ? "text-warning"
                        : "text-muted-foreground",
                  )}
                >
                  {mount.usedPercent.toFixed(0)}%
                </span>
                <Button
                  size="xs"
                  variant="ghost"
                  className="text-muted-foreground"
                  disabled={scanning !== null}
                  onClick={() => scan(mount.mountpoint)}
                >
                  {scanning === mount.mountpoint ? "scanning…" : "scan"}
                </Button>
              </div>
              {breakdown[mount.mountpoint] && (
                <div className="space-y-1 rounded-lg border border-hairline bg-surface-sunken p-2">
                  {breakdown[mount.mountpoint].map((entry) => (
                    <div key={entry.path} className="flex justify-between gap-2 text-hint">
                      <span className="truncate font-mono">{entry.name}</span>
                      <span className="numeric shrink-0 text-muted-foreground">
                        {bytes(entry.size)}
                      </span>
                    </div>
                  ))}
                </div>
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

function InterfacesPanel({ snapshot }: { snapshot: Snapshot }) {

  return (
    <Panel>
      <PanelHeader
        icon={NetworkDevice}
        title="Interfaces"
      />
      <PanelBody flush>
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
