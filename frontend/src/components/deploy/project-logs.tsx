"use client"

import { Fragment, useEffect, useState } from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { ArrowRight, Bell } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, plural, relativeTime } from "@/lib/format"
import { agentProduct } from "@/lib/clients"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import type {
  DeploymentLifecycle,
  DeploymentRequests,
  DockerEvent,
  NotificationChannel,
  RequestEntry,
  TrafficAlert,
  TrafficAlertList,
} from "@/lib/types"
import { ALERT_KINDS, latency, latencyTone, perMinute } from "@/lib/requests"
import { TileTrend } from "@/components/metrics/sparkline"
import { Pane } from "@/components/panel"
import { ProductGlyphs } from "@/components/product-logo"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { NumberTicker } from "@/components/ui/number-ticker"
import { Skeleton } from "@/components/ui/skeleton"
import { useProject } from "@/components/deploy/project-context"
import {
  EMPTY_REQUEST_QUERY,
  RequestsWorkspace,
  type RequestQuery,
  type RequestsView,
} from "@/components/deploy/requests-workspace"
import type { ChartMarker } from "@/components/deploy/request-chart"
import { LifecycleFeed } from "@/components/deploy/lifecycle-feed"
import { AddAlertSheet } from "@/components/deploy/add-alert-sheet"
import { failingTone } from "@/components/deploy/fleet"
import { ChannelNames, toldBy } from "@/components/deploy/settings/traffic-alerts"
import { WrapDot } from "@/components/deploy/request-marks"
import {
  EventStrip,
  MinuteStrip,
  isCleanExit,
  isTickEvent,
} from "@/components/deploy/traffic-strip"

type View = RequestsView | "events"

/**
 * The container's own disruptions — what the Container reading counts and
 * names, and the Events tab counts in amber. An exit is one only when it was
 * not clean: every routine stop and every release swap ends a container with
 * status 0, and the server already calls that a notice, not an error.
 */
function isDisruption(event: DockerEvent) {
  return (
    event.action === "oom" ||
    event.action === "restart" ||
    (event.action === "die" && !isCleanExit(event))
  )
}

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
 *   **Insights** — what the same window adds up to: how the response times
 *   spread, which page is failing, which client is trying doors, how much is
 *   bots, where visitors came from, which route the slow tenth belongs to.
 *
 *   **Events** — what Docker did to the container: exits with their codes,
 *   OOM kills, restart policies firing, health flips.
 *
 * The readings above the pane come from all of it at once, because the first
 * thing a reader wants is not a view, it is whether anything is wrong. Each
 * carries its hour where a meter would be (§15): the request rate and the p95
 * as lines, the failures as a strip of minutes, the container's exits and
 * restarts as ticks on a rail — so "0.9% failing" says whether that was one
 * bad minute or the whole hour. On a phone they sit two-up, so the pane and
 * its first request are on the first screen rather than under five stacked
 * figures.
 *
 * The alerts line under them says whether anyone will be told, and who: the
 * rules' state, and the channels they tell drawn as the services they are.
 * With no rule at all, adding one is a sheet on this page rather than a trip
 * to Automation settings — this is where the reader decides they want one.
 */
export function ProjectLogs() {
  const project = useProject()
  const search = useSearchParams()
  const { can } = useAuth()
  const { runtime } = project.detail
  // Adding a rule is the Automation page's admin verb — the API refuses
  // anyone else — so the sheet is offered only to a role that can save it.
  const canAddAlert = can("system.admin")
  // Whether anything routes to this deployment, for what an empty request
  // record asks the reader to do next. Unknown until operations are read.
  const routed =
    project.operations?.domains.status === "available"
      ? project.operations.domains.domains.length > 0
      : undefined

  const [view, setView] = useState<View>(() => {
    const asked = search.get("view")
    return asked === "insights" || asked === "events" ? asked : "requests"
  })
  const [query, setQuery] = useState<RequestQuery>(EMPTY_REQUEST_QUERY)
  const [moment, setMoment] = useState<string | undefined>(() => {
    const asked = search.get("moment")
    return asked && Number.isFinite(Date.parse(asked)) ? asked : undefined
  })
  const [adding, setAdding] = useState(false)

  // The view and the instant it is scoped to are written back to the URL. A
  // link to Events at the minute a deployment broke is the thing somebody
  // pastes into a chat, and it was only ever readable on arrival: pressing the
  // tab changed nothing in the address bar, so a reload landed back on
  // Requests and the ±2 minutes around the failing request were gone.
  useEffect(() => {
    const url = new URL(window.location.href)
    if (view === "requests") url.searchParams.delete("view")
    else url.searchParams.set("view", view)
    if (moment) url.searchParams.set("moment", moment)
    else url.searchParams.delete("moment")
    window.history.replaceState(null, "", url)
  }, [view, moment])

  // The readings are the page's own, not a view's: they hold still while the
  // reader moves between views, which is what lets the error rate be the
  // thing that sent them to Insights in the first place. They share the
  // record the server holds in memory with the view's own poll — one read,
  // not two — and a limit of one caps the rows, not the hour's columns, so
  // the trends in the tiles cost nothing more.
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
  // One read of the container's record serves the tile, its strip, the Events
  // tab's count and the chart's marks. The hour is worked out in the fetcher,
  // which runs in an effect — where reading a clock belongs — rather than in
  // render, and it travels with the events so the strip is drawn against the
  // same hour they were cut by.
  const lifecycle = usePoll<{
    feed: DeploymentLifecycle
    hour: DockerEvent[]
    recent: DockerEvent[]
    from: number
    to: number
  }>(
    async (signal) => {
      const feed = await get<DeploymentLifecycle>(
        `/deploy/${project.projectId}/lifecycle`,
        { limit: 200 },
        signal,
      )
      const to = Date.now()
      const from = to - 3_600_000
      // Newest first, whatever order the buffer hands them over in: the
      // reading names the newest.
      const hour = feed.events
        .filter((event) => Date.parse(event.time) > from)
        .sort((a, b) => Date.parse(b.time) - Date.parse(a.time))
      return { feed, hour, recent: hour.filter(isDisruption), from, to }
    },
    30000,
    [project.projectId],
  )
  const alerts = usePoll<TrafficAlertList>(
    (signal) => get<TrafficAlertList>(`/deploy/${project.projectId}/alerts`, undefined, signal),
    30000,
    [project.projectId],
  )
  // Who the rules tell, drawn as the services they are. Read once: channels
  // change on another page, and this line only has to name them.
  const channels = usePoll<NotificationChannel[]>(
    (signal) => get<NotificationChannel[]>("/deploy/notifications", undefined, signal),
    0,
    [],
  )

  const markers: ChartMarker[] = [
    ...project.releases
      .filter((release) => release.activatedAt)
      .map((release) => ({
        at: release.activatedAt!,
        kind: "deploy" as const,
        label: `Release #${release.number} went live`,
      })),
    // A clean exit is not marked: it is the other half of a release going
    // live or of somebody stopping it, and neither is a failure.
    ...(lifecycle.data?.feed.events ?? [])
      .filter(
        (event) => isDisruption(event) || (event.type === "container" && event.action === "start"),
      )
      .map((event) => ({
        at: event.time,
        kind:
          event.action === "die" || event.action === "oom"
            ? ("failure" as const)
            : ("restart" as const),
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

  const disruptions = lifecycle.data?.recent.length ?? 0

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-6">
      <TrafficReadings
        requests={requests.data}
        lifecycle={lifecycle.data}
        alerts={alerts.data?.alerts}
      />
      <AlertsLine
        projectId={project.projectId}
        alerts={alerts.data}
        channels={channels.data}
        onAdd={canAddAlert ? () => setAdding(true) : undefined}
      />

      {/* A floor rather than a fill: the project shell's `Page` is a flowing
          column, so a pane that only said `flex-1` sized itself to its rows and
          left the rest of the viewport empty beneath it. A lower floor on a
          phone, where four events left half the pane empty. */}
      <Pane className="min-h-[28rem] flex-1 sm:min-h-[40rem]">
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
              {/* The same number the Container reading counts, beside the
                  word (§4). None on the other two: their window is the
                  view's, not the page's hour, and a count that disagrees
                  with the rows under it is worse than none. */}
              {disruptions > 0 && (
                <span
                  aria-hidden
                  title={`${plural(disruptions, "exit or restart", "exits or restarts")} in the last hour`}
                  className="numeric text-hint text-warning"
                >
                  {disruptions}
                </span>
              )}
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
            alerts={alerts.data?.alerts}
            routed={routed}
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
            product={project.product}
            moment={moment}
            onClearMoment={() => setMoment(undefined)}
          />
        )}
      </Pane>

      {canAddAlert && (
        <AddAlertSheet
          open={adding}
          onOpenChange={setAdding}
          projectId={project.projectId}
          channels={channels.data ?? []}
          requests={requests.data}
          onAdded={alerts.refresh}
        />
      )}
    </div>
  )
}

/**
 * The page's shape before it has anything in it — five readings, the alerts
 * line and the pane — so the Suspense fallback gives way to the page rather
 * than a framed block giving way to tiles and a pane.
 */
export function LogsSkeleton() {
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-6">
      <StatGrid columns={5} dense>
        {Array.from({ length: 5 }, (_, i) => (
          <div key={i} data-slot="stat-tile" className="flex flex-col gap-2.5 px-5 py-4">
            <Skeleton className="h-2.5 w-16" />
            <Skeleton className="h-7 w-24" />
            <Skeleton className="h-2.5 w-32" />
          </div>
        ))}
      </StatGrid>
      <Skeleton className="h-4 w-64" />
      <Pane className="min-h-[28rem] flex-1 sm:min-h-[40rem]">
        <div className="h-10 shrink-0 border-b border-hairline" />
        <LoadingRows rows={8} className="p-3" />
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
    <button
      type="button"
      aria-pressed={active}
      className={tabClasses(active, "h-10")}
      onClick={onClick}
    >
      {children}
    </button>
  )
}

/** How the newest disruption is named in the Container reading, and its tone. */
const NEWEST: Record<string, { word: string; tone: "danger" | "warning" }> = {
  die: { word: "Exited", tone: "danger" },
  oom: { word: "OOM-killed", tone: "danger" },
  restart: { word: "Restarted", tone: "warning" },
}

/** What a clean exit that nothing started after says: a stop, in no tone. */
const STOPPED = { word: "Stopped", tone: "default" } as const

/**
 * What is happening right now, in five figures.
 *
 * Taken over the last hour rather than the view's own window, so they keep
 * meaning the same thing while the reader narrows the rows beneath them. Each
 * is a rate or a share rather than a count, for the same reason: "1,204" means
 * nothing without the window it was counted over, and a figure whose meaning
 * depends on a control somewhere else is a figure people learn to ignore.
 *
 * Each carries the hour it was taken over in the tile's trend slot, as the
 * Overview's do. The request rate, the p95 and the bytes are lines — the p95
 * scaled against the latency alert's line when there is one, so its height is
 * read against where somebody would be told. Failing is a strip of the hour's
 * minutes, red where one had a server error, because "0.9%" is either one bad
 * minute or a whole bad hour and a line of a share cannot say which. The
 * container is its newest disruption in words with its exit code at the
 * figure's side, and the hour's exits, restarts and starts as ticks on a rail
 * under it. A clean exit is none of these: a stop reads "Stopped" in no tone
 * when nothing started after it, and a release swap — the old container
 * stopping, the new one starting — leaves the reading "Steady".
 *
 * The figures count up on arrival (`NumberTicker`), which is the product's
 * way of saying a reading landed — the Overview's live usage does the same.
 */
function TrafficReadings({
  requests,
  lifecycle,
  alerts,
}: {
  requests?: DeploymentRequests
  lifecycle?: { hour: DockerEvent[]; recent: DockerEvent[]; from: number; to: number }
  alerts?: TrafficAlert[]
}) {
  const summary = requests?.summary
  const served = requests?.status === "available"
  const buckets = served && summary ? summary.buckets : []
  // What asked, drawn after the reading's name: the browsers, programs and
  // crawlers the hour's agents are, each once (§14 — a reading that counts
  // products carries them after its words). The name's line is the one with
  // room for them; beside the figure they pushed its unit onto a line of its
  // own, and in the hint they cut the page views short.
  const askers = [
    ...new Set(
      (summary?.agents ?? [])
        .map((facet) => agentProduct(facet.value).product)
        .filter((id): id is string => Boolean(id)),
    ),
  ]
  const line = (alerts ?? [])
    .filter((rule) => rule.enabled && rule.kind === "latency")
    .map((rule) => rule.threshold)
  // Only the minutes that had a timed request: a minute without one has no
  // p95, and drawing it at 0ms invents a fast minute the chart beside it
  // draws as the gap it is.
  const p95s = buckets.flatMap((bucket) => (bucket.p95 === undefined ? [] : [bucket.p95]))
  const hour = lifecycle?.hour ?? []
  const recent = lifecycle?.recent ?? []
  const lastTick = hour.find(isTickEvent)
  const newest = recent[0]
    ? NEWEST[recent[0].action]
    : lastTick && isCleanExit(lastTick)
      ? STOPPED
      : undefined
  const exitCode =
    recent[0]?.exitCode && recent[0].exitCode !== "0" ? recent[0].exitCode : undefined
  const exits = recent.filter((event) => event.action !== "restart").length
  const stops = hour.filter(isCleanExit).length
  const restarts = recent.length - exits
  const starts = hour.filter(
    (event) => event.type === "container" && event.action === "start",
  ).length
  const lives = [
    exits > 0 && plural(exits, "exit"),
    stops > 0 && plural(stops, "stop"),
    restarts > 0 && plural(restarts, "restart"),
    starts > 0 && plural(starts, "start"),
  ].filter(Boolean)

  return (
    <StatGrid columns={5} dense>
      <StatTile
        label={
          <span className="flex items-center justify-between gap-2">
            Requests
            <ProductGlyphs ids={askers} max={3} />
          </span>
        }
        key={`rate:${summary?.perMinute ?? "none"}`}
        value={
          served && summary ? (
            <NumberTicker
              value={Number(perMinute(summary.perMinute))}
              decimalPlaces={summary.perMinute < 10 ? 2 : summary.perMinute < 100 ? 1 : 0}
            />
          ) : (
            "—"
          )
        }
        trailing={served && summary ? "per minute" : undefined}
        trend={
          <TileTrend
            values={buckets.map((bucket) => bucket.total)}
            label="Requests over the last hour"
          />
        }
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
              <NumberTicker
                value={Number((summary.errorRate * 100).toFixed(1))}
                decimalPlaces={1}
              />
              %
            </span>
          ) : (
            "—"
          )
        }
        tone={served && summary ? failingTone(summary.errorRate) : "default"}
        trend={buckets.length > 0 ? <MinuteStrip buckets={buckets} /> : undefined}
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
        tone={latencyTone(summary?.latency?.p95)}
        trend={
          summary?.latency ? (
            <TileTrend
              values={p95s}
              max={line.length > 0 ? Math.max(Math.min(...line), ...p95s) : undefined}
              color="var(--chart-3)"
              label="The p95 over the last hour"
            />
          ) : undefined
        }
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
        trend={
          buckets.some((bucket) => bucket.bytes !== undefined) ? (
            <TileTrend
              values={buckets.map((bucket) => bucket.bytes ?? 0)}
              color="var(--chart-2)"
              label="Bytes sent over the last hour"
            />
          ) : undefined
        }
        hint={served && summary ? "sent in the last hour" : undefined}
      />
      <StatTile
        label="Container"
        key={`restarts:${recent.length}:${recent[0]?.time ?? ""}`}
        value={newest?.word ?? "Steady"}
        tone={newest?.tone ?? "default"}
        trailing={
          exitCode ? (
            <Tag tone="danger" mono>
              exit {exitCode}
            </Tag>
          ) : undefined
        }
        trend={
          lifecycle && hour.some(isTickEvent) ? (
            <EventStrip events={hour} from={lifecycle.from} to={lifecycle.to} />
          ) : undefined
        }
        hint={
          recent.length > 0
            ? [...lives, `newest ${relativeTime(recent[0].time)}`].join(" · ")
            : lives.length > 0
              ? [...lives, "none failed"].join(" · ")
              : "No exit or restart in the last hour"
        }
      />
    </StatGrid>
  )
}

/**
 * Whether anybody will be told, and who. One line at the rank of the Overview's
 * identity facts: the rules that watch this record and the state each is in,
 * and the channels they tell drawn as the services they are — through
 * Automation's own `ChannelNames`, so the two pages say the same thing about
 * the same rule. A page that can show a 4% error rate at three in the
 * afternoon and say nothing about whether it would have said so at three in
 * the morning is leaving out the part that matters.
 *
 * A firing rule's reading is over its own window, which is not the tiles'
 * hour, so the window is said: "4.2% failing" under a Failing tile reading
 * 1.0% read as the page contradicting itself.
 */
function AlertsLine({
  projectId,
  alerts,
  channels,
  onAdd,
}: {
  projectId: number
  alerts?: TrafficAlertList
  channels?: NotificationChannel[]
  /** Opens the add-alert sheet. Absent for a role that may not add one. */
  onAdd?: () => void
}) {
  // Nothing until the rules are read: "No alerts" for the second before they
  // land would be the line saying something it does not know.
  if (!alerts) return <div aria-hidden className="h-5" />
  const rules = alerts.alerts
  const watching = rules.filter((rule) => rule.enabled)
  const firing = watching.filter((rule) => rule.state === "firing")
  const href = `/deploy/${projectId}/settings/automation#alerts`
  const settings = (
    <Link
      href={href}
      className="inline-flex items-center gap-1 rounded-sm font-medium text-foreground focus-ring hover:underline"
    >
      Alerts
      <ArrowRight aria-hidden className="size-3" />
    </Link>
  )
  const line =
    "flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 text-xs text-muted-foreground sm:gap-x-2"

  if (rules.length === 0) {
    return (
      <div className={line}>
        <Status tone="stopped" label="No alerts" />
        <WrapDot />
        <span>Nobody is told when this deployment fails or slows.</span>
        {onAdd ? (
          <Button size="xs" variant="outline" onClick={onAdd} className="ml-1">
            <Bell className="size-3" />
            Add alert
          </Button>
        ) : (
          <>
            <WrapDot />
            {settings}
          </>
        )}
      </div>
    )
  }

  if (watching.length === 0) {
    return (
      <div className={line}>
        <Status tone="stopped" label="Alerts paused" />
        <WrapDot />
        <span>
          {rules.length === 1 ? "The one rule is paused" : `All ${rules.length} rules are paused`}
          {" — nobody is told."}
        </span>
        <WrapDot />
        {settings}
      </div>
    )
  }

  const told = firing.length > 0 ? firing : watching
  // A rule naming no channel tells every enabled one, which is what the
  // server does with it; otherwise the channels the rules name, once each.
  const ids = told.some((rule) => rule.channels.length === 0)
    ? []
    : [...new Set(told.flatMap((rule) => rule.channels))]
  return (
    <div className={line}>
      {firing.length > 0 ? (
        <>
          <Status tone="danger" label={`${firing.length} firing`} />
          {firing.map((rule) => (
            <Fragment key={rule.id}>
              <WrapDot />
              <span className="numeric text-foreground">
                {ALERT_KINDS[rule.kind].read(rule.observed)}
                <span className="text-muted-foreground">
                  {" "}
                  over the last {rule.windowMinutes} min
                </span>
                {rule.stateSince ? ` · began ${relativeTime(rule.stateSince)}` : ""}
              </span>
            </Fragment>
          ))}
        </>
      ) : (
        <>
          <Status tone="running" label="All quiet" />
          <WrapDot />
          <span className="min-w-0">
            {watching.length} of {plural(rules.length, "alert")} watching —{" "}
            {watching
              .map((rule) => ALERT_KINDS[rule.kind].describe(rule.threshold, rule.windowMinutes))
              .join("; ")}
          </span>
        </>
      )}
      {channels && (
        <>
          <WrapDot />
          {/* Watching, and nobody hears it: amber, as a reading of state. */}
          <ChannelNames
            ids={ids}
            channels={channels}
            className={toldBy(ids, channels).length === 0 ? "text-warning" : undefined}
          />
        </>
      )}
      <WrapDot />
      {settings}
    </div>
  )
}
