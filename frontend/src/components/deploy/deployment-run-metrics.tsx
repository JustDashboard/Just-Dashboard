"use client"

import Link from "next/link"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { bytes } from "@/lib/format"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"

type Point = {
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
  history?: { series: { containerId: string; points: Point[] }[] }
}
type Comparison = {
  status: string
  reason?: string
  before: MetricWindow
  after: MetricWindow
  hostBefore?: { points: Point[] }
  hostAfter?: { points: Point[] }
  hostReason?: string
}

function summarize(points: Point[]) {
  const samples = points.reduce((sum, point) => sum + point.samples, 0)
  if (!samples) return null
  return {
    samples,
    cpu: points.reduce((sum, point) => sum + point.cpu * point.samples, 0) / samples,
    cpuPeak: Math.max(...points.map((point) => point.cpuPeak)),
    mem: points.reduce((sum, point) => sum + point.mem * point.samples, 0) / samples,
    memPeak: Math.max(...points.map((point) => point.memBytesPeak ?? 0)),
  }
}

export function DeploymentRunMetrics({ projectID, runID }: { projectID: number; runID: number }) {
  const result = usePoll(
    (signal) => get<Comparison>(`/deploy/${projectID}/runs/${runID}/metrics`, undefined, signal),
    30000,
    [projectID, runID],
  )
  return (
    <Panel>
      <PanelHeader
        title="Metrics around activation"
        actions={
          <Button variant="outline" size="sm" asChild>
            <Link href="/metrics">Open Metrics</Link>
          </Button>
        }
      />
      <PanelBody className="space-y-4">
        {result.error ? (
          <ErrorState error={result.error} />
        ) : !result.data ? (
          <LoadingRows />
        ) : result.data.status !== "available" ? (
          <EmptyNote>{result.data.reason || "No metrics comparison for this run."}</EmptyNote>
        ) : (
          <>
            <div className="grid min-w-0 gap-4 lg:grid-cols-2">
              <WindowEvidence
                title="Before activation"
                window={result.data.before}
                host={result.data.hostBefore?.points ?? []}
              />
              <WindowEvidence
                title="After activation"
                window={result.data.after}
                host={result.data.hostAfter?.points ?? []}
              />
            </div>
            {result.data.hostReason && (
              <p className="text-xs text-muted-foreground">{result.data.hostReason}</p>
            )}
            <p className="text-xs text-muted-foreground">
              These observations do not establish that the release caused a change. Missing samples
              are not zero utilization.
            </p>
          </>
        )}
      </PanelBody>
    </Panel>
  )
}

function WindowEvidence({
  title,
  window,
  host,
}: {
  title: string
  window: MetricWindow
  host: Point[]
}) {
  const hostSummary = summarize(host)
  return (
    <section className="min-w-0 space-y-3 rounded-lg border border-hairline p-3" aria-label={title}>
      <h3 className="text-sm font-medium">{title}</h3>
      <p className="text-xs text-muted-foreground">
        {window.releaseId ? `Release ID ${window.releaseId}` : "No predecessor"} ·{" "}
        {window.status === "partial"
          ? "Partial history"
          : window.status === "unavailable"
            ? "Unavailable"
            : "Recorded history"}
      </p>
      {window.reason && <p className="text-xs text-muted-foreground">{window.reason}</p>}
      {window.history?.series.map((series) => {
        const summary = summarize(series.points)
        return (
          <div key={series.containerId} className="space-y-1 text-xs">
            <p className="font-mono break-all" title={series.containerId}>
              {series.containerId.slice(0, 12)}
            </p>
            {summary ? (
              <p>
                CPU mean {summary.cpu.toFixed(1)}% · peak {summary.cpuPeak.toFixed(1)}% · memory
                peak {bytes(summary.memPeak)} · {summary.samples} samples
              </p>
            ) : (
              <EmptyNote>No retained samples for this container.</EmptyNote>
            )}
          </div>
        )
      })}
      <p className="border-t border-hairline pt-3 text-xs">
        {hostSummary
          ? `Host CPU mean ${hostSummary.cpu.toFixed(1)}% · memory mean ${hostSummary.mem.toFixed(1)}% · ${hostSummary.samples} samples`
          : "Host history unavailable for this window."}
      </p>
    </section>
  )
}
