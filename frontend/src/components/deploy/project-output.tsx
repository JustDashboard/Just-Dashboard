"use client"

import { useMemo } from "react"
import { Box } from "@/components/icons"
import { clock } from "@/lib/format"
import { dockerSource, stackSource } from "@/lib/log-sources"
import type { DeploymentRuntimeServices, DeploymentSourceKind } from "@/lib/types"
import { ServiceLogs, type LogWindow, type ServiceLogSource } from "@/components/logs/service-logs"
import { EmptyState } from "@/components/state"
import { serviceProduct } from "@/components/deploy/service-product"
import { liveStack, orderedServices, type Lead } from "@/components/deploy/logs-model"

/** What `?service=` says for the live release's stack as a whole. */
export const ALL_SERVICES = "all"

/**
 * What the deployment's containers wrote, on the Logs page.
 *
 * The service logs every page embeds, one source per container the runtime
 * reports — the live release's first, the service readiness follows before
 * its neighbours — and "All services" when the live release is a stack of
 * several, their lines merged by time with each service in its own hue. Each
 * container is read through the lens its image calls for (an application's
 * exceptions and startup failures, a database's own events), detected by the
 * server, so the quick views above the lines are that service's.
 *
 * Live and History only: the page has one Insights, the requests', and the
 * output's own ranking — its exceptions, its startup failures — is a section
 * at the end of it. No frame: the page's pane is the frame, and the source's
 * strip sits under the page's view strip as the pane's second row.
 *
 * The page owns the address: `?service=` is a container (or `all`), and a
 * moment the reader came from — a request, a link — opens History on the
 * minute either side of it.
 */
export function ProjectOutput({
  projectId,
  runtime,
  kind,
  product,
  lead,
  service,
  onServiceChange,
  moment,
  onLeaveMoment,
}: {
  projectId: number
  runtime?: DeploymentRuntimeServices
  kind?: DeploymentSourceKind
  product?: string
  /** Which container is the application, which leads the picker. */
  lead: Lead
  /** A container id, `all`, or nothing: what the address asked for. */
  service?: string
  onServiceChange: (service: string) => void
  moment?: string
  onLeaveMoment: () => void
}) {
  const services = useMemo(
    () => (runtime?.status === "available" ? orderedServices(runtime.services, lead) : []),
    [runtime, lead],
  )
  const stack = liveStack(services)
  const sources = useMemo<(ServiceLogSource & { service: string })[]>(
    () => [
      ...services
        .filter((s) => s.liveRelease)
        .map((s) => ({
          id: dockerSource(s.containerId),
          label: s.name || s.containerId.slice(0, 12),
          kind: "docker" as const,
          status: s.state,
          product: serviceProduct(s.image, kind, product),
          service: s.containerId,
        })),
      ...(stack
        ? [
            {
              id: stackSource(stack),
              label: "All services",
              kind: "stack" as const,
              product: "docker-compose",
              service: ALL_SERVICES,
            },
          ]
        : []),
      // Containers of a release no longer live — kept for a rollback, or not
      // yet removed — after the ones serving, where their last words are.
      ...services
        .filter((s) => !s.liveRelease)
        .map((s) => ({
          id: dockerSource(s.containerId),
          label: s.name || s.containerId.slice(0, 12),
          kind: "docker" as const,
          status: s.state,
          product: serviceProduct(s.image, kind, product),
          service: s.containerId,
        })),
    ],
    [services, stack, kind, product],
  )

  const asked = service
    ? (sources.find((source) => source.service === service)?.id ??
      (service === ALL_SERVICES ? undefined : dockerSource(service)))
    : null
  const window = useMemo<LogWindow | undefined>(() => {
    const at = moment ? Date.parse(moment) : NaN
    if (!Number.isFinite(at)) return undefined
    return {
      since: new Date(at - 60_000).toISOString(),
      until: new Date(at + 60_000).toISOString(),
      label: `Around ${clock(moment!)}`,
    }
  }, [moment])

  if (sources.length === 0) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <EmptyState
          icon={Box}
          title="No container to read"
          description={
            runtime?.status === "unavailable" && runtime.reason
              ? runtime.reason
              : "This deployment runs no container right now. What a release prints appears here once one starts — its build transcript is under Builds."
          }
        />
      </div>
    )
  }

  return (
    <ServiceLogs
      sources={sources}
      source={asked}
      onSourceChange={(id) => {
        const picked = sources.find((source) => source.id === id)
        if (picked) onServiceChange(picked.service)
      }}
      storageKey={`deploy.${projectId}.output`}
      modes={["live", "search"]}
      window={window}
      onLeaveWindow={onLeaveMoment}
      pickerLabel="Service"
      flush
      className="min-h-0 flex-1"
    />
  )
}
