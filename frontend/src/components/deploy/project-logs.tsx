"use client"

import { useState } from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { get } from "@/lib/api"
import { bytes, relativeTime } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentLifecycle, DeploymentRequests, DockerEvent, RequestEntry, TrafficAlertList } from "@/lib/types"
import { ALERT_KINDS, latency, perMinute } from "@/lib/requests"
import { Pane } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { tabClasses } from "@/components/tabs"
import { NumberTicker } from "@/components/ui/number-ticker"
import { useProject } from "@/components/deploy/project-context"
import {
  EMPTY_REQUEST_QUERY,
  RequestsWorkspace,
  type RequestQuery,
  type RequestsView,
} from "@/components/deploy/requests-workspace"
import type { ChartMarker } from "@/components/deploy/request-chart"
import { LifecycleFeed } from "@/components/deploy/lifecycle-feed"

type View = RequestsView | "events"

/**
 * One deployment's traffic, read three ways.
 *
 * This page used to be one thing: the live container's standard output. For a
 * modern framework that is a startup banner and then silence — a Next.js or
 * Rails production server prints nothing per request — so a healthy
 * deployment serving a thousand requests a minute showed thirty-nine lines,
 * ending at "Ready in 236ms", and never changed again. That pane is gone from
 * here. The container's lines are one press away from a failing request
 * ("output around this moment"), which is the only time anybody wanted them.
 *
 * What is here instead:
 *
 *   **Requests** — every request the ingress answered, newest first, with the
 *   chart of them by status family and the moments that matter marked on it:
 *   a release going live, the container exiting or being restarted.
 *
 *   **Insights** — what the same window adds up to: which page is failing,
 *   which client is trying doors, how much is bots, where visitors came from,
 *   which route the slow tenth belongs to.
 *
 *   **Events** — what Docker did to the container: exits with their codes,
 *   OOM kills, restart policies firing, health flips.
 *
 * The readings above the pane come from all of it at once, because the first
 * thing a reader wants is not a view, it is whether anything is wrong — and
 * the alerts line under them says whether anyone will be told when it is.
 */
export function ProjectLogs() {
  const project = useProject()
  const search = useSearchParams()
  const { runtime } = project.detail

  const [view, setView] = useState<View>(() => {
    const asked = search.get("view")
    return asked === "insights" || asked === "events" ? asked : "requests"
  })
  const [query, setQuery] = useState<RequestQuery>(EMPTY_REQUEST_QUERY)
  const [moment, setMoment] = useState<string | undefined>()

  // The readings are the page's own, not a view's: they hold still while the
  // reader moves between views, which is what lets the error rate be the
  // thing that sent them to Insights in the first place. They share the
  // record the server holds in memory with the view's own poll — one read,
  // not two.
  const requests = usePoll<DeploymentRequests>(
    (signal) =>
      get<DeploymentRequests>(
        `/deploy/${project.projectId}/requests`,
        { since: new Date(Date.now() - 3_600_000).toISOString(), limit: 1 },
        signal,
      ),
    30000,
    [project.projectId],
  )
  // One read of the container's record serves both the tile and the chart's
  // marks. The hour is worked out in the fetcher, which runs in an effect —
  // where reading a clock belongs — rather than in render.
  const lifecycle = usePoll<{ feed: DeploymentLifecycle; recent: DockerEvent[] }>(
    async (signal) => {
      const feed = await get<DeploymentLifecycle>(
        `/deploy/${project.projectId}/lifecycle`,
        { limit: 200 },
        signal,
      )
      const cutoff = Date.now() - 3_600_000
      return {
        feed,
        recent: feed.events.filter(
          (event) =>
            Date.parse(event.time) > cutoff &&
            (event.action === "restart" || event.action === "die" || event.action === "oom"),
        ),
      }
    },
    30000,
    [project.projectId],
  )
  const alerts = usePoll<TrafficAlertList>(
    (signal) => get<TrafficAlertList>(`/deploy/${project.projectId}/alerts`, undefined, signal),
    30000,
    [project.projectId],
  )

  const markers: ChartMarker[] = [
    ...project.releases
      .filter((release) => release.activatedAt)
      .map((release) => ({
        at: release.activatedAt!,
        kind: "deploy" as const,
        label: `Release #${release.number} went live`,
      })),
    ...(lifecycle.data?.feed.events ?? [])
      .filter((event) => event.action === "die" || event.action === "oom" || event.action === "restart" || event.action === "start")
      .map((event) => ({
        at: event.time,
        kind: event.action === "die" || event.action === "oom" ? ("failure" as const) : ("restart" as const),
        label: event.message,
      })),
  ]

  // The live container, for "output around this moment": the host Logs page
  // opened on its lines for the minute either side of a request — the same
  // handoff a run's own logs use, and the only time the container's output
  // is what somebody wants.
  const liveService =
    runtime?.status === "available"
      ? (runtime.services.find((service) => service.liveRelease) ?? runtime.services[0])
      : undefined
  const outputHref = liveService
    ? (entry: RequestEntry) => {
        const at = Date.parse(entry.time)
        if (!Number.isFinite(at)) return undefined
        const params = new URLSearchParams({
          source: `docker:${liveService.containerId}`,
          mode: "search",
          since: new Date(at - 60_000).toISOString(),
          until: new Date(at + 60_000).toISOString(),
        })
        return `/logs?${params.toString()}`
      }
    : undefined

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-6">
      <TrafficReadings requests={requests.data} recent={lifecycle.data?.recent ?? []} />
      <AlertsLine projectId={project.projectId} alerts={alerts.data} />

      {/* A floor rather than a fill: the project shell's `Page` is a flowing
          column, so a pane that only said `flex-1` sized itself to its rows and
          left the rest of the viewport empty beneath it. */}
      <Pane className="min-h-[40rem] flex-1">
        <div className="flex min-h-10 shrink-0 items-stretch border-b border-hairline pr-1 pl-2">
          <nav aria-label="Log view" className="flex min-w-0 flex-1 items-stretch overflow-x-auto">
            <ViewTab active={view === "requests"} onClick={() => setView("requests")}>
              Requests
            </ViewTab>
            <ViewTab active={view === "insights"} onClick={() => setView("insights")}>
              Insights
            </ViewTab>
            <ViewTab active={view === "events"} onClick={() => setView("events")}>
              Events
            </ViewTab>
          </nav>
        </div>

        {view !== "events" && (
          <RequestsWorkspace
            projectId={project.projectId}
            view={view}
            query={query}
            onQueryChange={setQuery}
            markers={markers}
            outputHref={outputHref}
            onEventsAround={(entry) => {
              setMoment(entry.time)
              setView("events")
            }}
          />
        )}
        {view === "events" && (
          <LifecycleFeed
            projectId={project.projectId}
            moment={moment}
            onClearMoment={() => setMoment(undefined)}
          />
        )}
      </Pane>
    </div>
  )
}

function ViewTab({
  active,
  onClick,
  children,
}: {
  active: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button type="button" aria-pressed={active} className={tabClasses(active, "h-10")} onClick={onClick}>
      {children}
    </button>
  )
}

/**
 * What is happening right now, in five figures.
 *
 * Taken over the last hour rather than the view's own window, so they keep
 * meaning the same thing while the reader narrows the rows beneath them. Each
 * is a rate or a share rather than a count, for the same reason: "1,204" means
 * nothing without the window it was counted over, and a figure whose meaning
 * depends on a control somewhere else is a figure people learn to ignore.
 *
 * The figures count up on arrival (`NumberTicker`), which is the product's
 * way of saying a reading landed — the Overview's live usage does the same.
 */
function TrafficReadings({
  requests,
  recent,
}: {
  requests?: DeploymentRequests
  recent: DockerEvent[]
}) {
  const summary = requests?.summary
  const served = requests?.status === "available"
  return (
    <StatGrid columns={5}>
      <StatTile
        label="Requests"
        key={`rate:${summary?.perMinute ?? "none"}`}
        value={
          served && summary ? (
            <NumberTicker value={Number(perMinute(summary.perMinute))} decimalPlaces={summary.perMinute < 10 ? 2 : summary.perMinute < 100 ? 1 : 0} />
          ) : (
            "—"
          )
        }
        trailing={served && summary ? "per minute" : undefined}
        hint={
          served && summary
            ? `${summary.pages.toLocaleString()} page views in the last hour`
            : requests?.reason
              ? "No request record"
              : undefined
        }
      />
      <StatTile
        label="Failing"
        key={`err:${summary?.errorRate ?? "none"}`}
        value={
          served && summary ? (
            <span>
              <NumberTicker value={Number((summary.errorRate * 100).toFixed(1))} decimalPlaces={1} />%
            </span>
          ) : (
            "—"
          )
        }
        tone={served && summary && summary.errorRate > 0.01 ? "danger" : "default"}
        hint={
          served && summary
            ? `${(summary.classes["5xx"] ?? 0).toLocaleString()} of ${summary.total.toLocaleString()} answered 5xx`
            : undefined
        }
      />
      <StatTile
        label="Slowest tenth"
        key={`p95:${summary?.latency?.p95 ?? "none"}`}
        value={summary?.latency ? latency(summary.latency.p95) : "—"}
        tone={summary?.latency && summary.latency.p95 > 1000 ? "warning" : "default"}
        hint={
          summary?.latency
            ? `p95 · half answered inside ${latency(summary.latency.p50)}`
            : requests?.latency === false
              ? "This ingress records no timing"
              : undefined
        }
      />
      <StatTile
        label="Served"
        key={`bytes:${summary?.bytes ?? "none"}`}
        value={served && summary ? bytes(summary.bytes) : "—"}
        hint={served && summary ? "sent in the last hour" : undefined}
      />
      <StatTile
        label="Container"
        key={`restarts:${recent.length}`}
        value={recent.length === 0 ? "Steady" : recent.length.toLocaleString()}
        tone={recent.length > 0 ? "warning" : "default"}
        hint={
          recent.length === 0
            ? "No exit or restart in the last hour"
            : `${recent.length === 1 ? "event" : "events"} in the last hour — newest ${relativeTime(recent[0].time)}`
        }
      />
    </StatGrid>
  )
}

/**
 * Whether anybody will be told. One line: the rules that watch this record and
 * the state each is in, or the absence of any, with the settings a press away.
 * A page that can show a 4% error rate at three in the afternoon and say
 * nothing about whether it would have said so at three in the morning is
 * leaving out the part that matters.
 */
function AlertsLine({ projectId, alerts }: { projectId: number; alerts?: TrafficAlertList }) {
  const rules = alerts?.alerts ?? []
  const firing = rules.filter((rule) => rule.enabled && rule.state === "firing")
  const href = `/deploy/${projectId}/settings/automation#alerts`
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-hint text-muted-foreground">
      {rules.length === 0 ? (
        <>
          <Status tone="stopped" label="No alerts" />
          <span>Nobody is told when this deployment fails or slows.</span>
          <Link href={href} className="rounded-sm font-medium text-foreground focus-ring hover:underline">
            Add an alert
          </Link>
        </>
      ) : firing.length > 0 ? (
        <>
          <Status tone="danger" label={`${firing.length} firing`} />
          {firing.map((rule) => (
            <span key={rule.id} className="numeric">
              {ALERT_KINDS[rule.kind].read(rule.observed)}
              {rule.stateSince ? ` · began ${relativeTime(rule.stateSince)}` : ""}
            </span>
          ))}
          <Link href={href} className="rounded-sm font-medium text-foreground focus-ring hover:underline">
            Alerts
          </Link>
        </>
      ) : (
        <>
          <Status tone="running" label="All quiet" />
          <span>
            {rules.filter((rule) => rule.enabled).length} of {rules.length}{" "}
            {rules.length === 1 ? "alert" : "alerts"} watching —{" "}
            {rules
              .filter((rule) => rule.enabled)
              .map((rule) => ALERT_KINDS[rule.kind].describe(rule.threshold, rule.windowMinutes))
              .join("; ")}
            .
          </span>
          <Link href={href} className="rounded-sm font-medium text-foreground focus-ring hover:underline">
            Alerts
          </Link>
        </>
      )}
    </div>
  )
}
