"use client"

import { useMemo } from "react"
import { cn } from "@/lib/utils"
import { get } from "@/lib/api"
import { dockerSource, stackSource } from "@/lib/log-sources"
import type {
  DeploymentFleet,
  DeploymentRelease,
  DeploymentRuntimeService,
  RequestEntry,
  SiteSpec,
  TrafficPulse,
  VHost,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import type { LogWindow } from "@/components/logs/service-logs"
import type { LogFields } from "@/components/logs/types"
import { Pane } from "@/components/panel"
import { tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import type { ChartMarker } from "@/components/deploy/request-chart"
import { RequestLines } from "@/components/deploy/request-lines"
import { OutputInsights } from "@/components/deploy/output-insights"
import {
  RequestsWorkspace,
  type RequestQuery,
  type RequestsView,
} from "@/components/deploy/requests-workspace"
import { containerAt, liveStack, orderedServices, type Lead } from "@/components/deploy/logs-model"
import type { ProjectDetail } from "@/components/deploy/project-context"
import { serviceProduct } from "@/components/deploy/service-product"
import { siteLogPlan } from "@/components/proxy/site-log-plan"
import { FailedRequestLines } from "@/components/proxy/site-logs"
import { requestRecords, type RequestRecord } from "@/components/logs/service-views-model"

/** How far either side of a moment another log is opened on: the minute around a request. */
const AROUND_MS = 60_000

const iso = (ms: number) => new Date(ms).toISOString()

/** Another log, opened on History over a stretch of time — narrowed, where the asker knows how. */
export type OpenLog = (source: string, at: LogWindow & { fields?: LogFields }) => void

/** The minute either side of a request, or of the exception a container threw. */
function around(time: string): LogWindow {
  const at = Date.parse(time)
  return { since: iso(at - AROUND_MS), until: iso(at + AROUND_MS) }
}

/**
 * The request records `/logs` lists beside the logs: each deployment whose
 * route the ingress keeps a record for, and each nginx site that writes its
 * requests to a file of its own. Read on the minute: the rail only says
 * which there are and how busy each was, and the fleet's own page reads the
 * same answers more often.
 */
export function useRequestRecords() {
  const fleet = usePoll(
    (signal) => get<DeploymentFleet>("/deploy/", { view: "fleet" }, signal),
    60_000,
  )
  const pulses = usePoll(
    (signal) => get<Record<string, TrafficPulse>>("/deploy/traffic", undefined, signal),
    60_000,
  )
  const vhosts = usePoll((signal) => get<VHost[]>("/proxy/vhosts", undefined, signal), 60_000)
  const deployments = fleet.data?.deployments
  const sites = useMemo(() => (Array.isArray(vhosts.data) ? vhosts.data : []), [vhosts.data])
  const records = useMemo(
    () => requestRecords(Array.isArray(deployments) ? deployments : [], pulses.data, sites),
    [deployments, pulses.data, sites],
  )
  // Every list has answered, or failed: a record asked for by name and not
  // among them is then gone rather than still on its way.
  const settled = [fleet, pulses, vhosts].every((poll) => poll.data !== undefined || poll.error)
  return { records, sites, settled }
}

/** The session key a record's question is kept under: its owner's, so it is one question wherever it is read. */
export function recordQueryKey(id: string) {
  if (id.startsWith("deploy:")) return `deploy.${id.slice("deploy:".length)}.requests`
  if (id.startsWith("site:")) return `proxy.site.${id.slice("site:".length)}.requests`
  return ""
}

const VIEW_LABEL: Record<RequestsView, string> = { requests: "Requests", insights: "Insights" }

/**
 * A request record as a column of the logs workbench: the strip the log
 * sources have — its name, what it is, the readings it offers — and under it
 * the record read the way its owner's page reads it, Requests and Insights
 * over one window, so a deployment's traffic is not a second kind of page
 * bolted onto this one.
 */
export function RecordColumn({
  record,
  leading,
  view,
  onViewChange,
  query,
  onQueryChange,
  openLog,
  className,
}: {
  record: RequestRecord
  /** What sits before the name — the rail toggle. */
  leading?: React.ReactNode
  view: RequestsView
  onViewChange: (view: RequestsView) => void
  query: RequestQuery
  onQueryChange: (query: RequestQuery) => void
  openLog: OpenLog
  className?: string
}) {
  return (
    <Pane flush className={cn("min-h-0 flex-1", className)}>
      <div className="flex min-h-10 shrink-0 items-stretch border-b border-hairline pr-1 pl-2">
        <div className="@container flex min-w-0 flex-1 items-center gap-2 py-1.5">
          {leading}
          <span className="truncate text-body font-medium">{record.label}</span>
          <RecordFacts record={record} />
        </div>
        <nav aria-label="Log mode" className="flex shrink-0 items-stretch overflow-x-auto">
          {(["requests", "insights"] as const).map((id) => (
            <button
              key={id}
              type="button"
              aria-pressed={view === id}
              className={cn(tabClasses(view === id, "h-10"), "max-sm:px-2")}
              onClick={() => onViewChange(id)}
            >
              {VIEW_LABEL[id]}
            </button>
          ))}
        </nav>
      </div>
      <RecordRequests
        record={record}
        view={view}
        query={query}
        onQueryChange={onQueryChange}
        openLog={openLog}
      />
    </Pane>
  )
}

/**
 * What the record is, beside its name, giving way by the strip's own width
 * as a source's facts do: what owns it, where it answers, its last hour.
 */
function RecordFacts({ record }: { record: RequestRecord }) {
  return (
    <span className="hidden min-w-0 shrink-[1000] items-center gap-x-3 overflow-hidden text-hint text-muted-foreground @sm:flex">
      <Tag>{record.deployment ? "deployment" : "nginx site"}</Tag>
      {record.detail && (
        <span className="hidden min-w-0 truncate font-mono @md:block" title={record.detail}>
          {record.detail}
        </span>
      )}
      {record.figure && (
        <span
          className={cn(
            "numeric shrink-0 whitespace-nowrap",
            record.tone === "danger" && "text-destructive",
            record.tone === "warning" && "text-warning",
          )}
          title="Requests a minute over the last hour"
        >
          {record.figure}
        </span>
      )}
    </span>
  )
}

/**
 * One record read as its owner's page reads it — the deployment's Logs page
 * or the site's page — with what that page adds to an opened request: the
 * lines its container and its proxy wrote while it was in flight, what nginx
 * said about a failure. Where those pages open another view, this opens the
 * log in the workbench beside it.
 */
export function RecordRequests({
  record,
  view,
  query,
  onQueryChange,
  openLog,
}: {
  record: RequestRecord
  view: RequestsView
  query: RequestQuery
  onQueryChange: (query: RequestQuery) => void
  openLog: OpenLog
}) {
  if (record.deployment) {
    return (
      <DeploymentRecord
        record={record}
        view={view}
        query={query}
        onQueryChange={onQueryChange}
        openLog={openLog}
      />
    )
  }
  return (
    <SiteRecord
      record={record}
      view={view}
      query={query}
      onQueryChange={onQueryChange}
      openLog={openLog}
    />
  )
}

type RecordProps = {
  record: RequestRecord
  view: RequestsView
  query: RequestQuery
  onQueryChange: (query: RequestQuery) => void
  openLog: OpenLog
}

/**
 * A deployment's record, with the containers that answered it. Its runtime
 * and its releases say which container was live at a request's moment — the
 * one whose lines are read inside the request and opened beside it — and
 * where its releases went live on the chart.
 */
function DeploymentRecord({ record, view, query, onQueryChange, openLog }: RecordProps) {
  const deployment = record.deployment!
  const id = deployment.id
  const environment = deployment.environmentId
  const detail = usePoll(
    (signal) => get<ProjectDetail>(`/deploy/${id}`, undefined, signal),
    60_000,
    [id],
  )
  const releases = usePoll(
    (signal) =>
      get<DeploymentRelease[]>(
        `/deploy/${id}/environments/${environment}/releases`,
        { limit: 30 },
        signal,
      ),
    60_000,
    [id, environment],
    { enabled: environment > 0 },
  )
  const runtime = detail.data?.runtime
  const services = useMemo(
    () => (runtime?.status === "available" ? runtime.services : []),
    [runtime],
  )
  const product = record.product
  const kind = deployment.sourceKind
  // The application among a release's containers, as the deployment's own
  // page leads with it: the one drawn as the project, not its database.
  const lead = useMemo<Lead>(
    () => ({ own: (s) => serviceProduct(s.image, kind, product) === (product ?? "docker") }),
    [kind, product],
  )
  const markers = useMemo<ChartMarker[]>(
    () =>
      (Array.isArray(releases.data) ? releases.data : [])
        .filter((release) => release.activatedAt)
        .map((release) => ({
          at: release.activatedAt!,
          kind: "deploy" as const,
          label: `Release #${release.number} went live`,
        })),
    [releases.data],
  )
  // Only once the runtime and the releases have answered: before then an
  // empty list of containers would read as every release's being removed.
  const answering = (entry: RequestEntry) =>
    runtime?.status === "available" && Array.isArray(releases.data)
      ? containerAt(services, releases.data, Date.parse(entry.time), lead)
      : undefined
  const stack = liveStack(services)
  const live = orderedServices(
    services.filter((s) => s.liveRelease),
    lead,
  )[0]
  const containerOf = (short: string | undefined) =>
    short
      ? services.find((s) => s.containerId.startsWith(short) || short.startsWith(s.containerId))
      : undefined
  const openOutput = (container: DeploymentRuntimeService, at: string) =>
    openLog(dockerSource(container.containerId), around(at))

  return (
    <RequestsWorkspace
      base={record.base}
      subject={record.subject}
      emptyTitle={record.emptyTitle}
      view={view}
      query={query}
      onQueryChange={onQueryChange}
      markers={markers}
      outputFor={(entry) => {
        const answer = answering(entry)
        return answer && "container" in answer
          ? () => openOutput(answer.container, entry.time)
          : undefined
      }}
      renderInline={(entry, window) => (
        <RequestLines entry={entry} window={window} answering={answering(entry)} />
      )}
      afterInsights={
        live
          ? (window) => (
              <OutputInsights
                sourceId={stack ? stackSource(stack) : dockerSource(live.containerId)}
                label={stack ? "the live release's services" : live.name}
                window={window}
                onOpen={(at, container) => openOutput(containerOf(container) ?? live, at)}
              />
            )
          : undefined
      }
    />
  )
}

/**
 * A site's record, with what its error log said about a failure: the
 * site's own error log, or nginx's shared one narrowed to the site's names,
 * as the site's page reads it (`site-log-plan.ts`).
 */
function SiteRecord({ record, view, query, onQueryChange, openLog }: RecordProps) {
  const site = record.site!
  const read = usePoll(
    (signal) =>
      get<{ spec: SiteSpec }>(`/proxy/sites/${encodeURIComponent(site.name)}`, undefined, signal),
    60_000,
    [site.name],
  )
  const errors = read.data ? siteLogPlan(site, read.data.spec, undefined).errors : undefined
  return (
    <RequestsWorkspace
      base={record.base}
      subject={record.subject}
      emptyTitle={record.emptyTitle}
      view={view}
      query={query}
      onQueryChange={onQueryChange}
      markers={[]}
      renderInline={
        errors
          ? (entry) =>
              entry.status >= 500 ? (
                <FailedRequestLines
                  entry={entry}
                  errors={errors}
                  engine="nginx"
                  // The error log beside the access log, on the minute around
                  // the failure, narrowed to this site where the log is shared.
                  onOpen={() =>
                    openLog(errors.source, { ...around(entry.time), fields: errors.fields })
                  }
                />
              ) : undefined
          : undefined
      }
    />
  )
}
