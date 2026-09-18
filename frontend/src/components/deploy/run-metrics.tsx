"use client"

import Link from "next/link"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Button } from "@/components/ui/button"
import { bytes, percent } from "@/lib/format"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"

type MetricPoint = {
  samples: number
  cpu: number
  cpuPeak: number
  mem: number
  memPeak: number
  memBytes?: number
  memBytesPeak?: number
}
type MetricWindow = {
  releaseId: number
  status: "available" | "partial" | "unavailable"
  reason?: string
  history?: { series: { containerId: string; points: MetricPoint[] }[] }
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

function summarize(points: MetricPoint[]) {
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

/** What the release looked like just before and after it took over. */
export function RunMetrics({ projectId, runId }: { projectId: number; runId: number }) {
  const result = usePoll(
    (signal) =>
      get<MetricComparison>(`/deploy/${projectId}/runs/${runId}/metrics`, undefined, signal),
    30000,
    [projectId, runId],
  )
  return (
    <Panel plain>
      <PanelHeader
        title="Metrics around activation"
        actions={
          <Button variant="outline" size="sm" asChild>
            <Link href="/metrics">Open Metrics</Link>
          </Button>
        }
      />
      <PanelBody flush className="space-y-4 pt-3">
        {result.error ? (
          <ErrorState error={result.error} />
        ) : !result.data ? (
          <LoadingRows />
        ) : result.data.status !== "available" ? (
          <EmptyNote>{result.data.reason || "No metrics comparison for this run."}</EmptyNote>
        ) : (
          <>
            <div className="grid min-w-0 gap-6 lg:grid-cols-2">
              <MetricWindowPanel
                title="Before activation"
                window={result.data.before}
                host={result.data.hostBefore?.points ?? []}
              />
              <MetricWindowPanel
                title="After activation"
                window={result.data.after}
                host={result.data.hostAfter?.points ?? []}
              />
            </div>
            {result.data.hostReason && (
              <p className="text-hint text-muted-foreground">{result.data.hostReason}</p>
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

function MetricWindowPanel({
  title,
  window,
  host,
}: {
  title: string
  window: MetricWindow
  host: MetricPoint[]
}) {
  const hostSummary = summarize(host)
  return (
    <section aria-label={title} className="min-w-0 space-y-3">
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
      {window.history?.series.map((series) => {
        const summary = summarize(series.points)
        return (
          <div key={series.containerId} className="space-y-2">
            <p
              className="truncate font-mono text-hint text-muted-foreground"
              title={series.containerId}
            >
              {series.containerId.slice(0, 12)}
            </p>
            {summary ? (
              <StatGrid columns={3}>
                <StatTile label="CPU mean" value={percent(summary.cpu, 0)} />
                <StatTile label="CPU peak" value={percent(summary.cpuPeak, 0)} />
                <StatTile
                  label="Memory peak"
                  value={bytes(summary.memPeak)}
                  hint={`${summary.samples} samples`}
                />
              </StatGrid>
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
