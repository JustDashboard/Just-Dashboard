"use client"

import { useMemo, useState } from "react"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { Panel, PanelBody } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { FilterChip } from "@/components/tabs"
import { serviceProduct } from "@/components/deploy/service-product"
import { get } from "@/lib/api"
import { clockMinute, minuteSpan } from "@/lib/format"
import { dockerSource } from "@/lib/log-sources"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentSourceKind } from "@/lib/types"

type RunLogSource = {
  containerId: string
  name: string
  image?: string
  liveUrl: string
  activationUrl?: string
}

type RunLogHandoff = {
  status: "available" | "unavailable"
  reason?: string
  windowReason?: string
  activationCompletedAt?: string
  sources: RunLogSource[]
}

/**
 * The application's own runtime logs for this run's containers — separate
 * from the build transcript, which is the engine's own narration.
 *
 * No title and no caption: the view strip already says "Runtime logs". The
 * logs are the service logs every page embeds, on this run's containers: the
 * pane's strip names the container, drawn as the product its image is, and
 * becomes the picker when the run has more than one. The window the server
 * computed around the release going live is one more way to read the same
 * container — a chip that opens the history there, with the window's times
 * as the strip's facts while it is open — rather than a second Live beside
 * the pane's own. The state lives as long as the page, as it always did: the
 * next visit to a run should not open on the last one's window.
 */
export function RunLogs({
  projectId,
  runId,
  kind,
  product,
}: {
  projectId: number
  runId: number
  /**
   * Where the project's source comes from and what the project is, so a
   * container of its own build is drawn as the project, as Runtime draws it.
   */
  kind?: DeploymentSourceKind
  product?: string
}) {
  const result = usePoll(
    (signal) => get<RunLogHandoff>(`/deploy/${projectId}/runs/${runId}/logs`, undefined, signal),
    5000,
    [projectId, runId],
  )
  // The container chosen in the picker, as the page's own arrival: one the
  // reader picked and that then went away is said to be gone, not swapped.
  const [picked, setPicked] = useState<string | null>(null)
  const [activation, setActivation] = useState(false)
  const handed = result.data?.sources
  const sources = useMemo(() => handed ?? [], [handed])
  const selected = picked
    ? sources.find((source) => dockerSource(source.containerId) === picked)
    : sources[0]
  const around =
    activation && selected?.activationUrl
      ? new URL(selected.activationUrl, "http://localhost").searchParams
      : undefined
  const since = around?.get("since")
  const until = around?.get("until")
  const wentLive = result.data?.activationCompletedAt

  const logSources = useMemo<ServiceLogSource[]>(
    () =>
      sources.map((source) => ({
        id: dockerSource(source.containerId),
        label: source.name || source.containerId,
        kind: "docker",
        product: serviceProduct(source.image, kind, product),
      })),
    [sources, kind, product],
  )

  return (
    <Panel plain>
      {selected?.activationUrl && (
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <FilterChip selected={activation} onClick={() => setActivation(!activation)}>
            Around activation
          </FilterChip>
        </div>
      )}
      <PanelBody flush className="space-y-3 pt-3">
        {result.error ? (
          <ErrorState error={result.error} />
        ) : !result.data ? (
          <LoadingRows />
        ) : result.data.status === "unavailable" ? (
          <EmptyNote className="px-0 text-left">{result.data.reason}</EmptyNote>
        ) : (
          <>
            {result.data.windowReason && (
              <p className="text-hint text-muted-foreground">{result.data.windowReason}</p>
            )}
            {logSources.length === 0 ? (
              <EmptyNote className="px-0 text-left">
                No managed runtime logs yet. Logs appear here when a container is created.
              </EmptyNote>
            ) : (
              <ServiceLogs
                sources={logSources}
                source={picked}
                onSourceChange={(id) => {
                  setPicked(id)
                  setActivation(false)
                }}
                pickerLabel="Runtime log source"
                window={since && until ? { since, until } : undefined}
                // Live is the pane's own tab; pressed from the window, it
                // leaves the window.
                onLeaveWindow={() => setActivation(false)}
                facts={
                  since &&
                  until && (
                    <span className="numeric shrink-0 text-hint text-muted-foreground">
                      {minuteSpan(since, until)}
                      {wentLive && (
                        <span className="max-sm:hidden"> · went live {clockMinute(wentLive)}</span>
                      )}
                    </span>
                  )
                }
                paneClassName="h-[min(75vh,40rem)] min-h-80"
              />
            )}
          </>
        )}
      </PanelBody>
    </Panel>
  )
}
