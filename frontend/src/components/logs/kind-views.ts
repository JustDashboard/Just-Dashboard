import type { ServiceLogSource, ServiceLogsView } from "@/components/logs/service-logs"
import { containerEventsView } from "@/components/docker/container-events"
import { unitRunsView } from "@/components/procs/unit-runs"

/**
 * The views a source has by being what it is, whatever else is found of it:
 * a container's and a stack's Events, a unit's Runs. A pane that mixes kinds
 * of source — a journal unit beside a container — hands this to `ServiceLogs`
 * as its `views`, so each source is offered the reading that goes with it.
 *
 * Its own module rather than a part of `service-views.tsx`: that file reaches
 * the request records, which reach back to a site's page, so a site's page
 * importing it would be a cycle.
 */
export function kindViews(source: ServiceLogSource): ServiceLogsView[] {
  const target = source.id.slice(source.id.indexOf(":") + 1)
  if (source.kind === "docker") return [containerEventsView({ containerId: target })]
  if (source.kind === "stack") return [containerEventsView({ stack: target })]
  // The whole journal is every unit's, and runs are one unit's.
  if (source.kind === "journal" && target) return [unitRunsView(target)]
  return []
}
