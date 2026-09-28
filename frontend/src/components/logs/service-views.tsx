"use client"

import { useMemo } from "react"
import { get } from "@/lib/api"
import { useSessionState } from "@/lib/view-state"
import type { DbFleet, DbFleetEntry, DbLogSources, VHost } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import type { ServiceLogSource, ServiceLogsView } from "@/components/logs/service-logs"
import { containerEventsView } from "@/components/docker/container-events"
import { unitRunsView } from "@/components/procs/unit-runs"
import { databaseQueriesView } from "@/components/database/queries-view"
import { EMPTY_REQUEST_QUERY, completeQuery } from "@/components/deploy/logs-model"
import type { RequestQuery } from "@/components/deploy/requests-workspace"
import { RecordRequests, recordQueryKey, type OpenLog } from "@/components/logs/request-records"
import {
  containerConnection,
  hostCandidates,
  isDatabaseLens,
  siteOfFile,
  siteRecord,
} from "@/components/logs/service-views-model"

/**
 * What a source was found to be beyond its lines, which decides the page
 * views it is offered.
 */
export type ServiceFinds = {
  /** The saved connection whose server writes this log. */
  database?: DbFleetEntry
  /** The nginx site whose access or error log this file is. */
  site?: VHost
}

/**
 * The page views a source is offered beside Live, History and Insights —
 * the same views, from the same definitions, as the page of the thing that
 * writes it: a container's and a stack's Events, a unit's Runs, a saved
 * database's Queries, a site's Requests.
 *
 * `openLog` is where a view sends the reader to another log — a site's
 * failed request to its error log; `onQuery` is where a statement is run.
 */
export function serviceViews(
  source: ServiceLogSource,
  finds: ServiceFinds,
  { openLog, onQuery }: { openLog: OpenLog; onQuery?: (conn: DbFleetEntry, sql: string) => void },
): ServiceLogsView[] {
  const views: ServiceLogsView[] = []
  const target = source.id.slice(source.id.indexOf(":") + 1)
  if (source.kind === "docker") views.push(containerEventsView({ containerId: target }))
  if (source.kind === "stack") views.push(containerEventsView({ stack: target }))
  // The whole journal is every unit's, and runs are one unit's.
  if (source.kind === "journal" && target) views.push(unitRunsView(target))
  const database = finds.database
  if (database) {
    views.push(databaseQueriesView(database, onQuery && ((sql) => onQuery(database, sql))))
  }
  const site = finds.site
  if (site) {
    views.push({
      id: "requests",
      label: "Requests",
      render: () => <SiteFileRequests site={site} openLog={openLog} />,
    })
  }
  return views
}

/**
 * Finds what a source is the log of, reading only what its lens makes worth
 * asking: the saved databases when it is read as a database server's — a
 * container by the name the fleet gives it, a host file or unit by asking
 * each of the host's servers of that engine which logs are its own — and,
 * for a file, the sites the page already holds.
 */
export function useServiceFinds(
  source: ServiceLogSource | undefined,
  lens: string | undefined,
  sites: VHost[],
): ServiceFinds {
  const kind = source?.kind
  const asksDatabase =
    Boolean(source) &&
    isDatabaseLens(lens) &&
    (kind === "docker" || Boolean(source?.path) || source?.id.startsWith("journal:"))
  const fleet = usePoll(
    (signal) => get<DbFleet>("/databases/fleet", undefined, signal),
    5 * 60_000,
    [],
    { enabled: asksDatabase },
  )
  const connections = useMemo(
    () => (Array.isArray(fleet.data?.connections) ? fleet.data.connections : []),
    [fleet.data],
  )
  const container =
    asksDatabase && kind === "docker" && source
      ? containerConnection(connections, source.label)
      : undefined
  const candidates = useMemo(
    () => (asksDatabase && kind !== "docker" ? hostCandidates(connections, lens) : []),
    [asksDatabase, kind, connections, lens],
  )
  const sourceId = source?.id ?? ""
  const owner = usePoll(
    async (signal) => {
      const answers = await Promise.all(
        candidates.map((conn) =>
          get<DbLogSources>(`/databases/${conn.id}/logs/sources`, undefined, signal).then(
            (found) => (found.sources?.some((s) => s.id === sourceId) ? conn : undefined),
            () => undefined,
          ),
        ),
      )
      return answers.find(Boolean)
    },
    5 * 60_000,
    [sourceId, candidates.map((c) => c.id).join(",")],
    { enabled: candidates.length > 0 },
  )
  const site = source?.path && kind !== "docker" ? siteOfFile(sites, source.path) : undefined
  return { database: container ?? owner.data, site }
}

/**
 * A site's requests as a view of its access or error log: the site's own
 * record, under the question the site's page keeps for it.
 */
function SiteFileRequests({ site, openLog }: { site: VHost; openLog: OpenLog }) {
  const record = siteRecord(site)
  const [stored, setQuery] = useSessionState<RequestQuery>(
    recordQueryKey(record.id),
    EMPTY_REQUEST_QUERY,
  )
  const query = useMemo(() => completeQuery(stored), [stored])
  return (
    <RecordRequests
      record={record}
      view="requests"
      query={query}
      onQueryChange={setQuery}
      openLog={openLog}
    />
  )
}
