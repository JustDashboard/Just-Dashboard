"use client"

import { get } from "@/lib/api"
import { percent } from "@/lib/format"
import type { RunTraffic } from "@/lib/types"
import { latency, perMinute } from "@/lib/requests"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"

/**
 * What the release did to the traffic.
 *
 * The container metrics beside this say whether the release costs more to
 * run; this says whether it serves worse. Three readings, each taken over
 * the half hour before activation and the half hour after, with the change
 * beside the figure — "p95 55ms → 240ms" is the sentence a deploy tool
 * exists to be able to say, and it is said here rather than left to the
 * reader to work out from two charts.
 */
export function RunTrafficPanel({ projectId, runId }: { projectId: number; runId: number }) {
  const traffic = usePoll<RunTraffic>(
    (signal) => get<RunTraffic>(`/deploy/${projectId}/runs/${runId}/traffic`, undefined, signal),
    30000,
    [projectId, runId],
  )
  const data = traffic.data
  return (
    <Panel plain>
      <PanelHeader title="Traffic around activation" />
      <PanelBody flush className="pt-3">
        {!data ? (
          <EmptyNote>Reading the request record…</EmptyNote>
        ) : data.status !== "available" || !data.before || !data.after ? (
          <EmptyNote>{data.reason ?? "No traffic comparison for this run."}</EmptyNote>
        ) : (
          <StatGrid columns={3}>
            <StatTile
              label="Requests per minute"
              value={<Change before={perMinute(data.before.perMinute)} after={perMinute(data.after.perMinute)} />}
              hint={`${data.windowMinutes} min before → after activation`}
            />
            <StatTile
              label="Failing"
              value={<Change before={percent(data.before.errorRate * 100, 1)} after={percent(data.after.errorRate * 100, 1)} />}
              tone={data.after.errorRate > data.before.errorRate + 0.005 ? "danger" : "default"}
              hint={`${data.after.requests - data.before.requests >= 0 ? "+" : ""}${data.after.requests - data.before.requests} requests after`}
            />
            <StatTile
              label="Slowest tenth"
              value={
                data.latency ? (
                  <Change before={latency(data.before.p95)} after={latency(data.after.p95)} />
                ) : (
                  "—"
                )
              }
              tone={
                data.latency && data.before.p95 !== undefined && data.after.p95 !== undefined && data.after.p95 > data.before.p95 * 1.5
                  ? "warning"
                  : "default"
              }
              hint={data.latency ? "p95, before → after" : "This ingress records no timing"}
            />
          </StatGrid>
        )}
      </PanelBody>
    </Panel>
  )
}

/** Before → after, at the figure's own size, so the arrow is the reading. */
function Change({ before, after }: { before: string; after: string }) {
  return (
    <span className="numeric">
      <span className="text-muted-foreground">{before}</span>
      <span className="mx-1.5 text-muted-foreground/60">→</span>
      {after}
    </span>
  )
}
