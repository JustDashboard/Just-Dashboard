"use client"

import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Sparkline } from "@/components/metrics/sparkline"
import { bytes, percent } from "@/lib/format"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"

type MetricPoint = {
  ts?: string
  samples: number
  cpu: number
  cpuPeak: number
  mem: number
  memPeak: number
  memBytes?: number
  memBytesPeak?: number
}
type MetricSeries = { containerId: string; points: MetricPoint[] }
type MetricWindow = {
  releaseId: number
  status: "available" | "partial" | "unavailable"
  reason?: string
  history?: { series: MetricSeries[] }
}
type MetricComparison = {
  status: string
  reason?: string
  before: MetricWindow
  after: MetricWindow
  hostBefore?: { points: MetricPoint[] }
  hostAfter?: { points: MetricPoint[] }
  hostReason?: string
}

type Summary = { samples: number; cpu: number; cpuPeak: number; mem: number; memPeak: number }

function summarize(points: MetricPoint[]): Summary | undefined {
  const samples = points.reduce((sum, point) => sum + point.samples, 0)
  if (!samples) return undefined
  return {
    samples,
    cpu: points.reduce((sum, point) => sum + point.cpu * point.samples, 0) / samples,
    cpuPeak: Math.max(...points.map((point) => point.cpuPeak)),
    mem: points.reduce((sum, point) => sum + point.mem * point.samples, 0) / samples,
    memPeak: Math.max(...points.map((point) => point.memBytesPeak ?? 0)),
  }
}

function seriesOf(window: MetricWindow) {
  return window.history?.series ?? []
}

/**
 * What the release looked like in the ten minutes before it took over and
 * the ten after: the figures, and the shape of them. The two windows share
 * one scale, so a line that sits higher after activation is higher in fact
 * and not just drawn taller — the comparison is the point, and it is made
 * here rather than on the Metrics page, which cannot know which minute the
 * release went live.
 */
export function RunMetrics({ projectId, runId }: { projectId: number; runId: number }) {
  const result = usePoll(
    (signal) =>
      get<MetricComparison>(`/deploy/${projectId}/runs/${runId}/metrics`, undefined, signal),
    30000,
    [projectId, runId],
  )
  const data = result.data
  const everyPoint = data
    ? [...seriesOf(data.before), ...seriesOf(data.after)].flatMap((series) => series.points)
    : []
  const scale = {
    cpu: Math.max(1, ...everyPoint.map((point) => point.cpuPeak)),
    mem: Math.max(1, ...everyPoint.map((point) => point.memBytesPeak ?? point.memBytes ?? 0)),
  }
  const before = data && summarize(seriesOf(data.before)[0]?.points ?? [])
  const after = data && summarize(seriesOf(data.after)[0]?.points ?? [])

  return (
    <Panel plain>
      <PanelHeader title="Metrics around activation" />
      <PanelBody flush className="space-y-4 pt-3">
        {result.error ? (
          <ErrorState error={result.error} />
        ) : !data ? (
          <LoadingRows />
        ) : data.status !== "available" ? (
          <EmptyNote>{data.reason || "No metrics comparison for this run."}</EmptyNote>
        ) : (
          <>
            <div className="grid min-w-0 gap-8 lg:grid-cols-2 lg:gap-0 lg:divide-x lg:divide-hairline lg:[&>*+*]:pl-8 lg:[&>*:first-child]:pr-8">
              <MetricWindowPanel
                title="Before activation"
                window={data.before}
                host={data.hostBefore?.points ?? []}
                scale={scale}
              />
              <MetricWindowPanel
                title="After activation"
                window={data.after}
                host={data.hostAfter?.points ?? []}
                scale={scale}
              />
            </div>
            {before && after && (
              <p className="numeric text-hint text-muted-foreground">
                After activation, CPU mean{" "}
                {delta(after.cpu - before.cpu, (v) => `${v.toFixed(0)} pts`)} and memory peak{" "}
                {delta(after.memPeak - before.memPeak, bytes)}.
              </p>
            )}
            {data.hostReason && (
              <p className="text-hint text-muted-foreground">{data.hostReason}</p>
            )}
            <p className="text-hint text-muted-foreground">
              These observations do not establish that the release caused a change. Missing samples
              are not zero utilization.
            </p>
          </>
        )}
      </PanelBody>
    </Panel>
  )
}

function delta(change: number, format: (value: number) => string) {
  if (Math.abs(change) < 0.5) return "unchanged"
  return `${change > 0 ? "up" : "down"} ${format(Math.abs(change))}`
}

function MetricWindowPanel({
  title,
  window,
  host,
  scale,
}: {
  title: string
  window: MetricWindow
  host: MetricPoint[]
  scale: { cpu: number; mem: number }
}) {
  const hostSummary = summarize(host)
  return (
    <section aria-label={title} className="min-w-0 space-y-4">
      <div className="flex items-center justify-between gap-3">
        <p className="eyebrow">{title}</p>
        {window.status !== "available" && (
          <Status
            tone={window.status === "unavailable" ? "stopped" : "warning"}
            label={window.status === "unavailable" ? "Unavailable" : "Partial history"}
          />
        )}
      </div>
      {window.reason && <p className="text-hint text-muted-foreground">{window.reason}</p>}
      {seriesOf(window).map((series) => {
        const summary = summarize(series.points)
        const cpu = series.points.map((point) => point.cpu)
        const mem = series.points.map((point) => point.memBytes ?? 0)
        return (
          <div key={series.containerId} className="space-y-3">
            <p
              className="truncate font-mono text-hint text-muted-foreground"
              title={series.containerId}
            >
              {series.containerId.slice(0, 12)}
            </p>
            {summary ? (
              <>
                <StatGrid columns={3}>
                  <StatTile label="CPU mean" value={percent(summary.cpu, 0)} />
                  <StatTile label="CPU peak" value={percent(summary.cpuPeak, 0)} />
                  <StatTile
                    label="Memory peak"
                    value={bytes(summary.memPeak)}
                    hint={`${summary.samples} samples`}
                  />
                </StatGrid>
                {series.points.length > 1 && (
                  <div className="space-y-2">
                    <Trend label="CPU" values={cpu} max={scale.cpu} color="var(--chart-1)" />
                    {mem.some((value) => value > 0) && (
                      <Trend label="Memory" values={mem} max={scale.mem} color="var(--chart-2)" />
                    )}
                  </div>
                )}
              </>
            ) : (
              <EmptyNote>No retained samples for this container.</EmptyNote>
            )}
          </div>
        )
      })}
      <div className="border-t border-hairline pt-3 text-hint text-muted-foreground">
        {hostSummary
          ? `Host CPU mean ${percent(hostSummary.cpu, 0)} · memory mean ${percent(hostSummary.mem, 0)} · ${hostSummary.samples} samples`
          : "Host history unavailable for this window."}
      </div>
    </section>
  )
}

/** One measure over the window, as a line the width of the column. */
function Trend({
  label,
  values,
  max,
  color,
}: {
  label: string
  values: number[]
  max: number
  color: string
}) {
  return (
    <div className="flex items-center gap-3">
      <span className="w-14 shrink-0 text-hint text-muted-foreground">{label}</span>
      <Sparkline
        values={values}
        max={max}
        color={color}
        width={240}
        height={36}
        className="h-9 w-full"
        label={`${label} over the window`}
      />
    </div>
  )
}
