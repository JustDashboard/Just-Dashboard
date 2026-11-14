"use client"

import { Fragment, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import {
  ArrowDown,
  ArrowRight,
  Bell,
  Box,
  ChartActivity,
  CheckCircle,
  Globe,
  Lightning,
  Stopwatch,
  Terminal,
  Warning,
} from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, plural, relativeTime } from "@/lib/format"
import { agentProduct } from "@/lib/clients"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useIsMobile } from "@/hooks/use-mobile"
import type {
  DeploymentLifecycle,
  DeploymentRequests,
  DeploymentRuntimeService,
  DockerEvent,
  LogDrain,
  NotificationChannel,
  RequestEntry,
  TrafficAlert,
  TrafficAlertList,
} from "@/lib/types"
import { latencyWords } from "@/lib/latency-words"
import { ALERT_KINDS, latency, latencyTone, perMinute } from "@/lib/requests"
import { cn } from "@/lib/utils"
import { TileTrend } from "@/components/metrics/sparkline"
import { HUE } from "@/components/overview/readings"
import { Pane } from "@/components/panel"
import { PaneBack, PaneBar, type PaneStop } from "@/components/phone-panes"
import { ProductGlyphs } from "@/components/product-logo"
import { NoReading, ReadingLine, ReadingLines } from "@/components/reading-line"
import { LoadingRows } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { NumberTicker } from "@/components/ui/number-ticker"
import { useProject } from "@/components/deploy/project-context"
import { RequestsWorkspace } from "@/components/deploy/requests-workspace"
import type { ChartMarker } from "@/components/deploy/request-chart"
import { LifecycleFeed } from "@/components/deploy/lifecycle-feed"
import { RequestLines } from "@/components/deploy/request-lines"
import { ProjectOutput } from "@/components/deploy/project-output"
import { ProjectBuilds } from "@/components/deploy/project-builds"
import {
  EMPTY_REQUEST_QUERY,
  LOGS_VIEWS,
  QUERY_PARAMS,
  completeQuery,
  containerAt,
  defaultView,
  queryAround,
  queryFromParams,
  queryParams,
  type Lead,
  type LogsView,
  type RequestQuery,
} from "@/components/deploy/logs-model"
import { serviceProduct } from "@/components/deploy/service-product"
import { AddAlertSheet } from "@/components/deploy/add-alert-sheet"
import { traceLinkFor } from "@/components/deploy/log-drains"
import { failingTone } from "@/components/deploy/fleet"
import { failingWords } from "@/components/deploy/live-readings"
import { WireMark } from "@/components/deploy/wire"
import { ChannelNames, toldBy } from "@/components/deploy/settings/traffic-alerts"
import { WrapDot } from "@/components/deploy/request-marks"
import { MinuteStrip, isCleanExit } from "@/components/deploy/traffic-strip"

/**
 * The container's own disruptions — what the Events tab counts in amber. An
 * exit is one only when it was not clean: every routine stop and every
 * release swap ends a container with status 0, and the server already calls
 * that a notice, not an error.
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
 * thing a reader wants is not a view, it is whether anything is wrong. They
 * are the Overview's reading lines, said the same way, each with its hour at
 * the line's end: the request rate, the slow tail and the bytes as lines, the
 * failures as a strip of minutes — so "1.0% of requests failed" says whether
 * that was one bad minute or the whole hour.
 *
 * The alerts line under them says whether anyone will be told, and who: the
 * rules' state, and the channels they tell drawn as the services they are.
 * With no rule at all, adding one is a sheet on this page rather than a trip
 * to Automation settings — this is where the reader decides they want one.
 *
 * On a phone the page is one pane, the whole height under the project's
 * header, and the views are the stops of a bar along its foot
 * (`components/phone-panes.tsx`). Stacked, the readings took the first screen
 * and the pane began under it as a box inside a scrolling page. The hour is
 * one line at the pane's head there, on every view, and pressing it opens the
 * readings and the alerts as a page of their own with a way back.
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
  // Chosen once by width, so each reading is in the document once: above the
  // pane at a desk, behind the pane's first line on a phone.
  const phone = useIsMobile()
  const [hourOpen, setHourOpen] = useState(false)
  const hourShown = phone && hourOpen

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
        {
          since: new Date(Date.now() - 3_600_000).toISOString(),
          limit: 1,
          ...project.environmentQuery,
        },
        signal,
      ),
    30000,
    [project.projectId, project.environmentQuery?.environment],
  )
  // Where the proxy in front of the project says why it failed a request,
  // and the names it answers to: what narrows the ingress, which is every
  // route's, to this one. The served domains, else the saved ones.
  const served = project.operations?.domains
  const saved = project.configuration?.domains
  const hostnames = useMemo(
    () => (served?.status === "available" ? served.domains : (saved ?? [])).map((d) => d.hostname),
    [served, saved],
  )
  const proxy = useMemo(
    () => ({
      ingress: requests.data?.ingress,
      errorLog: requests.data?.errorLog,
      hosts: hostnames,
    }),
    [requests.data?.ingress, requests.data?.errorLog, hostnames],
  )
  // One read of the container's record serves the Events tab's count and the
  // chart's marks. The hour is worked out in the fetcher, which runs in an
  // effect — where reading a clock belongs — rather than in render.
  const lifecycle = usePoll<{ feed: DeploymentLifecycle; recent: DockerEvent[] }>(
    async (signal) => {
      const feed = await get<DeploymentLifecycle>(
        `/deploy/${project.projectId}/lifecycle`,
        { limit: 200, ...project.environmentQuery },
        signal,
      )
      const from = Date.now() - 3_600_000
      const recent = feed.events.filter(
        (event) => Date.parse(event.time) > from && isDisruption(event),
      )
      return { feed, recent }
    },
    30000,
    [project.projectId, project.environmentQuery?.environment],
  )
  // Traffic alerts watch production's route; staging's readings carry none.
  const staging = project.environment === "staging"
  // The drains name where a request's trace opens, when one exports this
  // project's traces to a service with a link. Read once: drains change on
  // their own page.
  const drains = usePoll<LogDrain[]>(
    (signal) => get<LogDrain[]>("/deploy/log-drains", undefined, signal),
    0,
    [],
  )
  const alerts = usePoll<TrafficAlertList>(
    (signal) => get<TrafficAlertList>(`/deploy/${project.projectId}/alerts`, undefined, signal),
    30000,
    [project.projectId],
    { enabled: !staging },
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

  const readings = <TrafficReadings requests={requests.data} alerts={alerts.data?.alerts} />
  const alertsLine = !staging && (
    <AlertsLine
      projectId={project.projectId}
      alerts={alerts.data}
      channels={channels.data}
      onAdd={canAddAlert ? () => setAdding(true) : undefined}
    />
  )
  const stops: PaneStop<LogsView>[] = LOGS_VIEWS.map((id) => ({
    key: id,
    label: VIEW_LABEL[id],
    icon: VIEW_GLYPH[id],
    count: id === "events" ? disruptions : undefined,
    countClassName: "bg-warning",
  }))

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-6">
      {!phone && readings}
      {!phone && alertsLine}

      {/* A height of its own, the window's less the page's chrome: the
          project shell's `Page` is a flowing column, so a pane that only said
          `flex-1` sized itself to its rows — four events left it half empty,
          and a live tail of four thousand lines made the page eighty thousand
          pixels long with nothing to follow. Sized, it is the workspace §7
          says a pane is, and each view scrolls inside it. On a phone the
          shell holds this page to the window and the pane is what is left of
          it, out to the screen's edges. */}
      <Pane className={PANE_SIZE}>
        {phone &&
          (hourShown ? (
            <HourPage onBack={() => setHourOpen(false)}>
              {readings}
              {alertsLine}
            </HourPage>
          ) : (
            <HourLine
              requests={requests.data}
              alerts={staging ? undefined : alerts.data}
              onOpen={() => setHourOpen(true)}
            />
          ))}
        <div className="flex min-h-10 shrink-0 items-stretch border-b border-hairline pr-1 pl-2 max-md:hidden">
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

        {/* Behind the hour's page a view stays as it was: its scroll, its
            live tail and the row it had open are there on the way back. */}
        <div className={hourShown ? "hidden" : "contents"}>
          {(view === "requests" || view === "insights") && (
            <RequestsWorkspace
              base={`/deploy/${project.projectId}`}
              scope={project.environmentQuery}
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
              traceLink={(traceId) => traceLinkFor(drains.data, project.projectId, traceId)}
              renderInline={(entry, window) => (
                <RequestLines entry={entry} window={window} answering={answering(entry)} />
              )}
            />
          )}
          {view === "output" && (
            <ProjectOutput
              projectId={project.projectId}
              runtime={runtime}
              kind={kind}
              product={project.product}
              lead={lead}
              proxy={proxy}
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
              environmentId={project.environmentQuery?.environment}
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
        </div>
        {!hourShown && (
          <PaneBar label="Log view" until="md" stops={stops} active={view} onPick={choose} />
        )}
      </Pane>

      {canAddAlert && !staging && (
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

const PANE_SIZE =
  "max-md:-mx-5 max-md:min-h-0 max-md:flex-1 max-md:rounded-none max-md:border-x-0 max-md:border-b-0 md:h-[max(40rem,calc(100dvh-6rem))]"

const VIEW_LABEL: Record<LogsView, string> = {
  requests: "Requests",
  insights: "Insights",
  output: "Output",
  builds: "Builds",
  events: "Events",
}

/** Each view's glyph on the phone's bar. */
const VIEW_GLYPH: Record<LogsView, React.ReactNode> = {
  requests: <Globe />,
  insights: <ChartActivity />,
  output: <Terminal />,
  builds: <Box />,
  events: <Lightning />,
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
 * The page's shape before it has anything in it — the four reading lines, the
 * alerts line and the pane — so the Suspense fallback gives way to the page
 * rather than a framed block giving way to lines and a pane. On a phone it is
 * the pane alone, with its first line and its bar, as the page is there.
 */
export function LogsSkeleton() {
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-6">
      <div className="flex flex-col gap-6 max-md:hidden">
        <TrafficReadings />
        <div aria-hidden className={ALERTS_LINE} />
      </div>
      <Pane className={PANE_SIZE}>
        <div className="h-10 shrink-0 border-b border-hairline" />
        <LoadingRows rows={8} className="min-h-0 flex-1 p-3" />
        <div aria-hidden className="h-14 shrink-0 border-t border-hairline md:hidden" />
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

/**
 * What is happening right now, in four lines.
 *
 * Taken over the last hour rather than the view's own window, so they keep
 * meaning the same thing while the reader narrows the rows beneath them. Each
 * is a rate or a share rather than a count, for the same reason: "1,204" means
 * nothing without the window it was counted over, and a figure whose meaning
 * depends on a control somewhere else is a figure people learn to ignore.
 *
 * They are the Overview's `ReadingLine`s, in the Overview's words — Traffic
 * and Errors say here exactly what they said on the page that sent the reader
 * — where four `StatTile`s had put "21.4" over "per minute" and left the
 * sentence to the reader. Each carries the hour it was taken over at its end.
 * The request rate, the slow tail and the bytes are lines — the tail scaled
 * against the latency alert's line when there is one, so its height is read
 * against where somebody would be told. Errors is a strip of the hour's
 * minutes, red where one had a server error, because "1.0%" is either one bad
 * minute or a whole bad hour and a line of a share cannot say which. What
 * the container did is the Events tab's, which counts its disruptions.
 *
 * A line is one height with a reading and without one, so the pane under them
 * is where it will be before the record is read.
 */
function TrafficReadings({
  requests,
  alerts,
}: {
  requests?: DeploymentRequests
  alerts?: TrafficAlert[]
}) {
  const summary = requests?.summary
  const served = requests?.status === "available"
  const ready = served && summary ? summary : undefined
  const buckets = ready ? ready.buckets : []
  const waiting = !requests
    ? "Reading the request record…"
    : requests.reason
      ? "No request record"
      : "No requests recorded yet"
  // What asked, drawn after the reading's name: the browsers, programs and
  // crawlers the hour's agents are, each once (§14 — a reading that counts
  // products carries them after its words).
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
  const rate = ready ? Number(perMinute(ready.perMinute)) : 0
  const failing = ready ? failingTone(ready.errorRate) : "default"
  const slow = latencyTone(summary?.latency?.p95) === "warning"

  return (
    <ReadingLines aria-label="The last hour" role="group" className="md:grid-cols-2">
      <ReadingLine
        mark={
          <WireMark size="md" hue={HUE.net}>
            <Globe aria-hidden />
          </WireMark>
        }
        name={
          <>
            Traffic
            <ProductGlyphs ids={askers} max={3} />
          </>
        }
        hint={ready ? `${plural(ready.pages, "page view")} in the last hour` : waiting}
        trend={
          <TileTrend
            values={buckets.map((bucket) => bucket.total)}
            label="Requests over the last hour"
            color={HUE.net}
          />
        }
      >
        {ready ? (
          <>
            <NumberTicker
              key="requests"
              value={rate}
              decimalPlaces={rate < 10 ? 2 : rate < 100 ? 1 : 0}
            />{" "}
            {rate === 1 ? "request" : "requests"} a minute
          </>
        ) : (
          <NoReading>No reading yet</NoReading>
        )}
      </ReadingLine>
      <ReadingLine
        mark={
          ready ? (
            <WireMark
              size="md"
              tone={failing === "default" ? "success" : failing === "danger" ? "danger" : "warning"}
            >
              {failing === "default" ? <CheckCircle aria-hidden /> : <Warning aria-hidden />}
            </WireMark>
          ) : (
            <WireMark size="md">
              <CheckCircle aria-hidden className="text-muted-foreground" />
            </WireMark>
          )
        }
        name="Errors"
        hint={
          ready
            ? `${(ready.classes["5xx"] ?? 0).toLocaleString()} of ${ready.total.toLocaleString()} answered 5xx`
            : undefined
        }
        trend={buckets.length > 0 ? <MinuteStrip buckets={buckets} /> : undefined}
      >
        {ready ? failingWords(ready.errorRate) : <NoReading>No reading yet</NoReading>}
      </ReadingLine>
      <ReadingLine
        mark={
          slow ? (
            <WireMark size="md" tone="warning">
              <Stopwatch aria-hidden />
            </WireMark>
          ) : (
            <WireMark size="md" hue={HUE.load}>
              <Stopwatch aria-hidden />
            </WireMark>
          )
        }
        name="Response time"
        hint={
          summary?.latency
            ? sentence(latencyWords("p50", latency(summary.latency.p50)))
            : requests?.latency === false
              ? "This ingress records no timing"
              : undefined
        }
        trend={
          summary?.latency ? (
            <TileTrend
              values={p95s}
              max={line.length > 0 ? Math.max(Math.min(...line), ...p95s) : undefined}
              color={HUE.load}
              label="How long 19 in 20 responses took, over the last hour"
            />
          ) : undefined
        }
      >
        {summary?.latency ? (
          latencyWords("p95", latency(summary.latency.p95))
        ) : (
          <NoReading>No reading yet</NoReading>
        )}
      </ReadingLine>
      <ReadingLine
        mark={
          <WireMark size="md" hue={HUE.mem}>
            <ArrowDown aria-hidden />
          </WireMark>
        }
        name="Served"
        hint={ready ? "In the last hour" : undefined}
        trend={
          buckets.some((bucket) => bucket.bytes !== undefined) ? (
            <TileTrend
              values={buckets.map((bucket) => bucket.bytes ?? 0)}
              color={HUE.mem}
              label="Bytes sent over the last hour"
            />
          ) : undefined
        }
      >
        {ready ? `${bytes(ready.bytes)} sent` : <NoReading>No reading yet</NoReading>}
      </ReadingLine>
    </ReadingLines>
  )
}

/** A reading's words as the line of a sentence they are: "half inside 24ms" leads with a capital. */
function sentence(words: string) {
  return words.charAt(0).toUpperCase() + words.slice(1)
}

/**
 * The hour in one line, at the head of the phone's pane: how much is asking
 * and how much of it fails, with the firing alerts in front where there are
 * any. It is on every view, as the readings are over every view at a desk,
 * and it is a row that opens: the four readings and the alerts are a page
 * behind it (`HourPage`).
 */
function HourLine({
  requests,
  alerts,
  onOpen,
}: {
  requests?: DeploymentRequests
  alerts?: TrafficAlertList
  onOpen: () => void
}) {
  const summary = requests?.status === "available" ? requests.summary : undefined
  const firing = (alerts?.alerts ?? []).filter((rule) => rule.enabled && rule.state === "firing")
  const failing = summary ? failingTone(summary.errorRate) : "default"
  return (
    <button
      type="button"
      data-slot="hour-line"
      aria-label="The last hour's readings and alerts"
      onClick={onOpen}
      className="flex h-10 shrink-0 items-center gap-2 border-b border-hairline px-3 text-left text-xs focus-ring-inset transition-colors hover:bg-row-hover"
    >
      <StatusDot
        tone={
          firing.length > 0 || failing === "danger"
            ? "danger"
            : failing === "warning"
              ? "warning"
              : summary
                ? "running"
                : "unknown"
        }
      />
      <span className="numeric min-w-0 flex-1 truncate text-muted-foreground">
        {firing.length > 0 && (
          <span className="font-medium text-destructive">{firing.length} firing · </span>
        )}
        {summary ? (
          <>
            <span className="text-foreground">{perMinute(summary.perMinute)}</span> a minute ·{" "}
            <span
              className={cn(
                failing === "danger"
                  ? "text-destructive"
                  : failing === "warning"
                    ? "text-warning"
                    : "text-foreground",
              )}
            >
              {failingWords(summary.errorRate)}
            </span>
          </>
        ) : requests ? (
          "No request record"
        ) : (
          "Reading the request record…"
        )}
      </span>
      <ArrowRight aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
    </button>
  )
}

/**
 * The hour's page on a phone: the readings one under another and the alerts
 * line, in the pane's place, with the way back where a phone's is. The bar is
 * out of its way while it is open, as it is for any page a row opens.
 */
function HourPage({ onBack, children }: { onBack: () => void; children: React.ReactNode }) {
  return (
    <section aria-label="The last hour" className="flex min-h-0 flex-1 flex-col">
      <div className="flex h-10 shrink-0 items-center gap-1 border-b border-hairline px-3">
        <PaneBack label="Back to the logs" until="md" onClick={onBack} />
        <h2 className="text-body font-medium">The last hour</h2>
      </div>
      <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-5 py-4">{children}</div>
    </section>
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
// As tall as the Add alert button a line with no rule carries, in every state:
// the line was 20px waiting and 24px with the button, and the pane moved.
const ALERTS_LINE = "min-h-6"

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
  if (!alerts) return <div aria-hidden className={ALERTS_LINE} />
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
  const line = cn(
    ALERTS_LINE,
    "flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 text-xs text-muted-foreground sm:gap-x-2",
  )

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
