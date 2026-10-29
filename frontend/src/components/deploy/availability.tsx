"use client"

import { get } from "@/lib/api"
import { timestamp } from "@/lib/format"
import type { DeploymentAvailability } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { WINDOW_LABELS, availabilityReading } from "@/components/deploy/availability-model"

/**
 * How much of the last day, week and month the deployment was up: the figure
 * a status page shows, read here from what the dashboard already keeps rather
 * than from a probe. Each release's activation (its readiness checks passing)
 * and retirement say when the environment had something live; the container
 * events the dashboard keeps say when, inside that, it was not — an exit, an
 * OOM kill, a pause or a failing health check, until the container came back.
 * Three tiles (§15 pass 2), one per window, with the downtime under each
 * figure and the record's reach where it is shorter than the window, so a
 * steady three days never passes for a steady month.
 */
export function Availability({
  projectId,
  environmentId,
}: {
  projectId: number
  /** Staging's figure in place of production's. */
  environmentId?: number
}) {
  const poll = usePoll(
    (signal) =>
      get<DeploymentAvailability>(
        `/deploy/${projectId}/availability`,
        { environment: environmentId },
        signal,
      ),
    60000,
    [projectId, environmentId],
  )
  const data = poll.data
  return (
    <Panel plain>
      <PanelHeader
        title="Availability"
        actions={
          data?.recordSince && (
            <span
              className="text-hint text-muted-foreground"
              title="The oldest container event the dashboard keeps; the record begins here"
            >
              record since <span className="numeric">{timestamp(data.recordSince)}</span>
            </span>
          )
        }
      />
      <PanelBody>
        {poll.error && !data && <ErrorState error={poll.error} onRetry={poll.refresh} />}
        {poll.loading && !data && <LoadingRows rows={1} />}
        {data?.status === "unavailable" && <EmptyNote>{data.reason}</EmptyNote>}
        {data?.status === "available" && (
          <>
            <StatGrid columns={3} dense className="[&_[data-slot=stat-tile]]:py-3">
              {data.windows.map((window) => {
                const reading = availabilityReading(window, data.down)
                return (
                  <StatTile
                    key={window.window}
                    label={WINDOW_LABELS[window.window] ?? window.window}
                    value={reading.value}
                    tone={reading.tone}
                    hint={reading.hint}
                  />
                )
              })}
            </StatGrid>
            <p className="mt-3 text-hint text-muted-foreground">
              The share of the time the live release was running and healthy, from each
              release&apos;s activation and retirement and the container events the dashboard keeps:
              an exit, an OOM kill, a pause or a failing health check counts as down until the
              container comes back, and so does a stop. Time before the record began is not counted
              either way.
            </p>
          </>
        )}
      </PanelBody>
    </Panel>
  )
}
