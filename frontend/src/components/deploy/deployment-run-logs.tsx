"use client"

import { DeploymentLogSources } from "@/components/deploy/deployment-logs"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"

export type RunLogHandoff = {
  status: "available" | "unavailable"
  reason?: string
  windowReason?: string
  activationCompletedAt?: string
  sources: { containerId: string; name: string; liveUrl: string; activationUrl?: string }[]
}

export function DeploymentRunLogs({ projectID, runID }: { projectID: number; runID: number }) {
  const result = usePoll(
    (signal) => get<RunLogHandoff>(`/deploy/${projectID}/runs/${runID}/logs`, undefined, signal),
    5000,
    [projectID, runID],
  )
  return (
    <Panel>
      <PanelHeader title="Application runtime logs" />
      <PanelBody className="space-y-3">
        {result.error ? (
          <ErrorState error={result.error} />
        ) : !result.data ? (
          <LoadingRows />
        ) : result.data.status === "unavailable" ? (
          <EmptyNote>{result.data.reason}</EmptyNote>
        ) : result.data.sources.length === 0 ? (
          <EmptyNote>
            Docker has no matching container for this run’s release. Review the deployment
            transcript or your external log archive.
          </EmptyNote>
        ) : (
          <>
            {result.data.windowReason && (
              <p className="text-xs text-muted-foreground">{result.data.windowReason}</p>
            )}
            <DeploymentLogSources sources={result.data.sources} />
          </>
        )}
      </PanelBody>
    </Panel>
  )
}
