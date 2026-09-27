"use client"

import { Fragment, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { ArrowRight, Bell } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, plural, relativeTime } from "@/lib/format"
import { agentProduct } from "@/lib/clients"
import { dockerSource, stackSource } from "@/lib/log-sources"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import type {
  DeploymentLifecycle,
  DeploymentRequests,
  DeploymentRuntimeService,
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
import { RequestsWorkspace } from "@/components/deploy/requests-workspace"
import type { ChartMarker } from "@/components/deploy/request-chart"
import { LifecycleFeed } from "@/components/deploy/lifecycle-feed"
import { RequestLines } from "@/components/deploy/request-lines"
import { ProjectOutput } from "@/components/deploy/project-output"
import { OutputInsights } from "@/components/deploy/output-insights"
import { ProjectBuilds } from "@/components/deploy/project-builds"
import {
  EMPTY_REQUEST_QUERY,
  LOGS_VIEWS,
  QUERY_PARAMS,
  completeQuery,
  containerAt,
  defaultView,
  liveStack,
  orderedServices,
  queryAround,
  queryFromParams,
  queryParams,
  type Lead,
  type LogsView,
  type RequestQuery,
} from "@/components/deploy/logs-model"
import { serviceProduct } from "@/components/deploy/service-product"
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
 * One deployment's logs, read five ways.
 *
 * This page used to be one thing: the live container's standard output. For a
 * modern framework that is a startup banner and then silence — a Next.js or
 * Rails production server prints nothing per request — so a healthy
 * deployment serving a thousand requests a minute showed thirty-nine lines,
 * ending at "Ready in 236ms", and never changed again. The request record
 * took its place as the page's first reading; the output came back as a view
 * of its own, beside it rather than instead of it, because a game server, a
 * worker and a crashing release have nothing else to say.
 *
 *   **Requests** — every request the ingress answered, newest first, with the
 *   chart of them by status family and the moments that matter marked on it:
 *   a release going live, the container exiting or being restarted. A
 *   request opens in place on the container's lines while it was in flight,
 *   and a failure on what the proxy said about it.
 *
 *   **Insights** — what the same window adds up to: how the response times
 *   spread, which page is failing, which client is trying doors, how much is
 *   bots, where visitors came from, which route the slow tenth belongs to —
 *   and, at the end, the exceptions and failed starts the output holds over
 *   the same window. One Insights on the page, not one per view.
 *
 *   **Output** — what the containers wrote, per service or all of them, live
 *   and back through history, read through each image's lens.
 *
 *   **Builds** — the recent runs and the chosen one's transcript.
 *
 *   **Events** — what Docker did to the container: exits with their codes and
 *   last lines, OOM kills, crash loops folded into one row, health flips.
 *
 * The page opens on Requests where something routes to it, and on Output
 * where nothing does. The address says which view, which moment and which
 * service, and carries the request query, so a link pasted into a chat — an
 * alert's among them — opens on the same question it was taken from.
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
  // record asks the reader to do next and which view the page opens on.
  // Unknown until operations are read.
  const routed =
    project.operations?.domains.status === "available"
      ? project.operations.domains.domains.length > 0
      : undefined
  // The saved domains answer the same question before the served ones are
  // read, so a deployment nobody routes to does not open on Requests first.
  const planned = project.configuration ? project.configuration.domains.length > 0 : undefined
  const fallback = defaultView(project.detail.deployment.profile, routed ?? planned)

  // What the address asked for, read once on arrival: afterwards the page
  // writes it rather than reads it.
  const [arrival] = useState(() => arrivalOf(search, Date.now()))
  const [asked, setAsked] = useState<LogsView | undefined>(arrival.view)
  const view = asked ?? fallback
  // A moment belongs to the view it was sent to: Events' two minutes around a
  // request, Output's History around one. One moment for both made pressing
  // Output, after reading Events around a request, open History on that
  // request's minute — on whichever service Output last showed.
  const [eventsMoment, setEventsMoment] = useState(
    arrival.view === "output" ? undefined : arrival.moment,
  )
  // Output's is kept for the tab, as the History it opened is: the pane
  // remembers that range, and coming back to it without its moment drew the
  // old minutes with nothing to say what they were or a way back to Live.
  const [keptMoment, setOutputMoment] = useSessionState<string | null>(
    `deploy.${project.projectId}.output.moment`,
    null,
    arrival.view === "events" ? null : arrival.moment,
  )
  const outputMoment = keptMoment ?? undefined
  const [service, setService] = useState(arrival.service)
  // The request query is the tab's, and a link's when it carries one: a
  // narrowing survives a reload and a trip to another view and back.
  const [storedQuery, setQuery] = useSessionState<RequestQuery>(
    `deploy.${project.projectId}.requests`,
    EMPTY_REQUEST_QUERY,
    arrival.query,
  )
  const query = useMemo(() => completeQuery(storedQuery), [storedQuery])
  const [adding, setAdding] = useState(false)

  // The view, the moment it is scoped to, the service and the request query
  // are written back to the address. A link to Events at the minute a
  // deployment broke is the thing somebody pastes into a chat, and it was
  // only ever readable on arrival: pressing the tab changed nothing in the
  // address bar, so a reload landed back on Requests and the ±2 minutes
  // around the failing request were gone. Each word is written only where the
  // view on screen reads it, so the page at rest is its bare address.
  const queryWords = JSON.stringify(queryParams(query))
  const moment = view === "events" ? eventsMoment : view === "output" ? outputMoment : undefined
  useEffect(() => {
    const url = new URL(window.location.href)
    const params = url.searchParams
    if (view === fallback) params.delete("view")
    else params.set("view", view)
    if (moment) params.set("moment", moment)
    else params.delete("moment")
    if (service && view === "output") params.set("service", service)
    else params.delete("service")
    for (const key of QUERY_PARAMS) params.delete(key)
    if (view === "requests" || view === "insights") {
      for (const [key, value] of JSON.parse(queryWords) as [string, string][]) {
        params.set(key, value)
      }
    }
    window.history.replaceState(null, "", url)
  }, [view, fallback, moment, service, queryWords])

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

  const services = runtime?.status === "available" ? runtime.services : []
  const kind = project.detail.deployment.sourceKind
  const primary = project.configuration?.build.primaryService
  // The application among a release's containers: the service readiness
  // follows, else the one drawn as the project itself rather than as a
  // product of its own — the Next.js build, not the Postgres beside it.
  const lead = useMemo<Lead>(
    () => ({
      primary,
      own: (s) => serviceProduct(s.image, kind, project.product) === (project.product ?? "docker"),
    }),
    [primary, kind, project.product],
  )
  const stack = liveStack(services)
  const live = orderedServices(
    services.filter((s) => s.liveRelease),
    lead,
  )[0]
  const containerOf = (id: string | undefined) =>
    id
      ? services.find((s) => s.containerId.startsWith(id) || id.startsWith(s.containerId))
      : undefined

  // Output on one container's lines around a moment: the request it served,
  // the exit it made, the exception it threw.
  const openOutput = (container: DeploymentRuntimeService, at: string) => {
    setService(container.containerId)
    setOutputMoment(at)
    setAsked("output")
  }
  // Only once the runtime has answered: before then an empty list of
  // containers would read as every release's having been removed.
  const answering = (entry: RequestEntry) =>
    runtime?.status === "available"
      ? containerAt(services, project.releases, Date.parse(entry.time), lead)
      : undefined

  const disruptions = lifecycle.data?.recent.length ?? 0
  const choose = (next: LogsView) => setAsked(next)

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

      {/* A height of its own, the window's less the page's chrome: the
          project shell's `Page` is a flowing column, so a pane that only said
          `flex-1` sized itself to its rows — four events left it half empty,
          and a live tail of four thousand lines made the page eighty thousand
          pixels long with nothing to follow. Sized, it is the workspace §7
          says a pane is, and each view scrolls inside it. A floor under it,
          lower on a phone. */}
      <Pane className={PANE_SIZE}>
        <div className="flex min-h-10 shrink-0 items-stretch border-b border-hairline pr-1 pl-2">
          <nav aria-label="Log view" className="flex min-w-0 flex-1 items-stretch overflow-x-auto">
            {LOGS_VIEWS.map((id) => (
              <ViewTab key={id} active={view === id} onClick={() => choose(id)}>
                {VIEW_LABEL[id]}
                {/* The same number the Container reading counts, beside the
                    word (§4). None on the others: their window is the view's,
                    not the page's hour, and a count that disagrees with the
                    rows under it is worse than none. */}
                {id === "events" && disruptions > 0 && (
                  <span
                    aria-hidden
                    title={`${plural(disruptions, "exit or restart", "exits or restarts")} in the last hour`}
                    className="numeric text-hint text-warning"
                  >
                    {disruptions}
                  </span>
                )}
              </ViewTab>
            ))}
          </nav>
        </div>

        {(view === "requests" || view === "insights") && (
          <RequestsWorkspace
            base={`/deploy/${project.projectId}`}
            subject={`deployment ${project.projectId}`}
            view={view}
            query={query}
            onQueryChange={setQuery}
            markers={markers}
            alerts={alerts.data?.alerts}
            routed={routed}
            emptyAction={
              <Button size="sm" variant="outline" asChild>
                <Link href={`/deploy/${project.projectId}/settings/domains`}>Add a domain</Link>
              </Button>
            }
            onEventsAround={(entry) => {
              setEventsMoment(entry.time)
              setAsked("events")
            }}
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
        )}
        {view === "output" && (
          <ProjectOutput
            projectId={project.projectId}
            runtime={runtime}
            kind={kind}
            product={project.product}
            lead={lead}
            service={service}
            onServiceChange={setService}
            moment={outputMoment}
            onLeaveMoment={() => setOutputMoment(null)}
          />
        )}
        {view === "builds" && (
          <ProjectBuilds
            projectId={project.projectId}
            runs={project.runs}
            loading={project.runsLoading}
            remote={project.detail.deployment.sourceRemote}
          />
        )}
        {view === "events" && (
          <LifecycleFeed
            projectId={project.projectId}
            product={project.product}
            moment={eventsMoment}
            onClearMoment={() => setEventsMoment(undefined)}
            outputFor={(event) => {
              const container = containerOf(event.id)
              return container ? () => openOutput(container, event.time) : undefined
            }}
            // Whether the container is still there — its output with it — once
            // the runtime has said which are.
            exists={
              runtime?.status === "available"
                ? (event) => Boolean(containerOf(event.id))
                : undefined
            }
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

const PANE_SIZE = "h-[max(28rem,calc(100dvh-6rem))] sm:h-[max(40rem,calc(100dvh-6rem))]"

const VIEW_LABEL: Record<LogsView, string> = {
  requests: "Requests",
  insights: "Insights",
  output: "Output",
  builds: "Builds",
  events: "Events",
}

/**
 * What the address asked for. A service alone is a request for its output —
 * the Runtime page's and the game console's "Logs" say only which container
 * — and a moment on Requests is the hour around it (an alert's link), turned
 * into the request query rather than kept as a moment.
 */
function arrivalOf(search: URLSearchParams, now: number) {
  const named = search.get("view")
  const service = search.get("service") || undefined
  const view = LOGS_VIEWS.includes(named as LogsView)
    ? (named as LogsView)
    : service
      ? "output"
      : undefined
  const rawMoment = search.get("moment")
  const moment = rawMoment && Number.isFinite(Date.parse(rawMoment)) ? rawMoment : undefined
  const onRequests = view === "requests" || view === "insights"
  const query =
    queryFromParams(search) ?? (onRequests && moment ? queryAround(moment, now) : undefined)
  return { view, service, moment: onRequests ? undefined : moment, query }
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
      <Pane className={PANE_SIZE}>
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
