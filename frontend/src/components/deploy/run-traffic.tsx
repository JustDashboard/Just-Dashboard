"use client"

import { get } from "@/lib/api"
import { clock, percent } from "@/lib/format"
import type { RunTraffic } from "@/lib/types"
import { latency, perMinute } from "@/lib/requests"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { NumberTicker } from "@/components/ui/number-ticker"

/**
 * What the release did to the traffic.
 *
 * The container metrics beside this say whether the release costs more to
 * run; this says whether it serves worse. Three readings, each taken over
 * the half hour before activation and the half hour after, with the change
 * beside the figure — "p95 55ms → 240ms" is the sentence a deploy tool
 * exists to be able to say, and it is said here rather than left to the
 * reader to work out from two charts. The after figure counts up when it
 * lands, because it is the one the reader came for; the moment the release
 * went live is the header's data rather than a caption under it.
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
      <PanelHeader
        title="Traffic around activation"
        actions={
          data?.activationCompletedAt && (
            <span className="numeric text-hint text-muted-foreground">
              went live {clock(data.activationCompletedAt)}
            </span>
          )
        }
      />
      <PanelBody flush className="pt-3">
        {traffic.error && !data ? (
          <ErrorState error={traffic.error} />
        ) : !data ? (
          <LoadingRows rows={2} />
        ) : data.status !== "available" || !data.before || !data.after ? (
          <EmptyNote className="px-0 text-left">
            {data.reason ?? "No traffic comparison for this run."}
          </EmptyNote>
        ) : (
          <StatGrid columns={3} dense className="animate-rise">
            <StatTile
              label="Requests per minute"
              value={
                <Change
                  before={perMinute(data.before.perMinute)}
                  after={<Rate value={data.after.perMinute} />}
                />
              }
              hint={`${data.windowMinutes} min each side`}
            />
            <StatTile
              label="Failing"
              value={
                <Change
                  before={percent(data.before.errorRate * 100, 1)}
                  after={
                    <>
                      <NumberTicker value={data.after.errorRate * 100} decimalPlaces={1} />%
                    </>
                  }
                />
              }
              tone={data.after.errorRate > data.before.errorRate + 0.005 ? "danger" : "default"}
              hint={`${data.after.requests - data.before.requests >= 0 ? "+" : ""}${data.after.requests - data.before.requests} requests after`}
            />
            <StatTile
              label="Slowest tenth"
              value={
                data.latency ? (
                  <Change
                    before={latency(data.before.p95)}
                    after={<Latency ms={data.after.p95} />}
                  />
                ) : (
                  "—"
                )
              }
              tone={
                data.latency &&
                data.before.p95 !== undefined &&
                data.after.p95 !== undefined &&
                data.after.p95 > data.before.p95 * 1.5
                  ? "warning"
                  : "default"
              }
              hint={data.latency ? "p95" : "This ingress records no timing"}
            />
          </StatGrid>
        )}
      </PanelBody>
    </Panel>
  )
}

/** Before → after, at the figure's own size, so the arrow is the reading. */
function Change({ before, after }: { before: React.ReactNode; after: React.ReactNode }) {
  return (
    // One line at every width: on a tile two to a row on a phone the before
    // half steps down to the body size, so the after figure — the one the
    // reader came for — keeps the tile's and never drops under the arrow.
    <span className="numeric inline-flex items-baseline gap-x-1.5 whitespace-nowrap">
      <span className="max-sm:text-body">
        <span className="text-muted-foreground">{before}</span>
        <span className="ml-1.5 text-muted-foreground/60">→</span>
      </span>
      <span className="whitespace-nowrap">{after}</span>
    </span>
  )
}

/** A rate counting up to `perMinute`'s own precision. */
function Rate({ value }: { value: number }) {
  if (value <= 0) return "0"
  return <NumberTicker value={value} decimalPlaces={value >= 100 ? 0 : value >= 10 ? 1 : 2} />
}

/** A latency counting up in milliseconds; past a second it is set as `latency` sets it. */
function Latency({ ms }: { ms: number | undefined }) {
  if (ms === undefined || ms < 1 || ms >= 1000) return latency(ms)
  return (
    <>
      <NumberTicker value={Math.round(ms)} />
      ms
    </>
  )
}
