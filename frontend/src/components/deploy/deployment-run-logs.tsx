"use client"

import Link from "next/link"
import { Logs } from "@/components/icons"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
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
      <PanelHeader
        icon={Logs}
        title="Application runtime logs"
      />
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
            <ul aria-label="Run runtime logs" className="divide-y divide-hairline">
              {result.data.sources.map((source) => (
                <li
                  key={source.containerId}
                  className="flex min-w-0 flex-wrap items-center justify-between gap-3 py-3"
                >
                  <span className="min-w-0 text-sm font-medium break-all">
                    {source.name || source.containerId}
                  </span>
                  <div className="flex flex-wrap gap-2">
                    <Button variant="outline" size="sm" asChild>
                      <Link href={source.liveUrl} aria-label={`Live logs for ${source.name}`}>
                        Live logs
                      </Link>
                    </Button>
                    {source.activationUrl && (
                      <Button variant="outline" size="sm" asChild>
                        <Link
                          href={source.activationUrl}
                          aria-label={`Activation logs for ${source.name}`}
                        >
                          Around activation
                        </Link>
                      </Button>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          </>
        )}
      </PanelBody>
    </Panel>
  )
}
