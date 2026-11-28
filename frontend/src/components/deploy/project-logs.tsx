"use client"

import { Fragment, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { ArrowRight, Bell, ChartActivity, Globe, Lightning, Terminal } from "@/components/icons"
import { get } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import type {
  DeploymentLifecycle,
  DeploymentRequests,
  DeploymentRuntimeService,
  DockerEvent,
  LogDrain,
  NotificationChannel,
  RequestEntry,
  TrafficAlertList,
} from "@/lib/types"
import { ALERT_KINDS } from "@/lib/requests"
import { cn } from "@/lib/utils"
import { PaneBar, type PaneStop } from "@/components/phone-panes"
import { LoadingRows } from "@/components/loading"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { TabsContent } from "@/components/ui/tabs"
import { useProject } from "@/components/deploy/project-context"
import { RequestsWorkspace } from "@/components/deploy/requests-workspace"
import type { ChartMarker } from "@/components/deploy/request-chart"
import { LifecycleFeed } from "@/components/deploy/lifecycle-feed"
import { RequestLines } from "@/components/deploy/request-lines"
import { ProjectOutput } from "@/components/deploy/project-output"
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
import { ChannelNames, toldBy } from "@/components/deploy/settings/traffic-alerts"
import { WrapDot } from "@/components/deploy/request-marks"
import { isCleanExit } from "@/components/deploy/traffic-strip"
import { LogsStrip, VIEW_LABEL } from "@/components/deploy/logs-strip"

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
 * One deployment's logs, read four ways, as one workbench.
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
 *   **Requests** — every request the ingress answered in the window, newest
 *   first, under one sentence of what the window adds up to and the chart of
 *   it, with the moments that matter marked on it: a release going live, the
 *   container exiting or being restarted. Everyone, people or bots, a press
 *   apart. A request opens in an inspector beside the rows, on the
 *   container's lines while it was in flight and, on a failure, what the
 *   proxy said about it.
 *
 *   **Insights** — what the same window adds up to: who the people were and
 *   what they opened, how the response times spread, which page is failing,
 *   which client is trying doors, where visitors came from, which route the
 *   slow tenth belongs to.
 *
 *   **Output** — what the containers wrote, per service or all of them, live
 *   and back through history, read through each image's lens.
 *
 *   **Events** — what Docker did to the container: exits with their codes and
 *   last lines, OOM kills, crash loops folded into one row, health flips.
 *
 * Builds are not here: a build's transcript is its run's page, on
 * Deployments, and a tab of it here was a second place to read the same
 * thing.
 *
 * It is a workbench (`recipes/workbench.md`): the window's height, the
 * sheet's edges, one strip at the top and no project header over it — the
 * header's facts and verbs are the Overview's, one press back. The readings
 * that stood above the rows as four tiles over the last hour are the
 * window's own sentence now, about the window on screen; the alerts line is
 * that sentence's last words.
 *
 * The page opens on Requests where something routes to it, and on Output
 * where nothing does. The address says which view, which moment and which
 * service, and carries the request query, so a link pasted into a chat — an
 * alert's among them — opens on the same question it was taken from.
 *
 * On a phone the page is one pane, the whole height, and the views are the
 * stops of a bar along its foot (`components/phone-panes.tsx`).
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

  // The last hour, read once a minute for two things the views do not hold:
  // which proxy answers (Output reads its lines beside the containers'), and
  // what an alert added from here would see today. A limit of one caps the
  // rows, not the hour's readings.
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
    60000,
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

  const alertsWords = !staging && (
    <AlertsWords
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
    <TabsPrimitive.Root
      value={view}
      onValueChange={(next) => choose(next as LogsView)}
      data-slot="logs-workbench"
      className={cn(
        "flex min-h-0 flex-1 flex-col bg-card",
        // On a phone the Requests stop is the list: the chart of the same
        // window is Insights' first reading, a stop away, where over the
        // rows it left two of them above the bar.
        view === "requests" && "max-md:[&_[data-slot=request-chart]]:hidden",
      )}
    >
      <LogsStrip view={view} disruptions={disruptions} />

      <TabsContent value={view} className="flex min-h-0 flex-col">
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
            inspect
            headlineAside={alertsWords}
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
      </TabsContent>

      <PaneBar label="Log view" until="md" stops={stops} active={view} onPick={choose} />

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
    </TabsPrimitive.Root>
  )
}

/** Each view's glyph on the phone's bar. */
const VIEW_GLYPH: Record<LogsView, React.ReactNode> = {
  requests: <Globe />,
  insights: <ChartActivity />,
  output: <Terminal />,
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
 * The page's shape before it has anything in it: the strip, the window's
 * controls and its two lines, then rows — so the Suspense fallback gives way
 * to the page rather than to a different layout.
 */
export function LogsSkeleton() {
  return (
    <div data-slot="logs-workbench" className="flex min-h-0 flex-1 flex-col bg-card">
      <div className="h-12 shrink-0 border-b border-hairline bg-surface-header" />
      <div className="h-12 shrink-0 border-b border-hairline" />
      <div className="flex shrink-0 flex-col gap-2 border-b border-hairline px-4 py-3">
        <Skeleton className="h-3.5 w-96 max-w-full" />
        <Skeleton className="h-3 w-48" />
      </div>
      <LoadingRows rows={8} className="min-h-0 flex-1 p-3" />
      <div aria-hidden className="h-14 shrink-0 border-t border-hairline md:hidden" />
    </div>
  )
}

/**
 * Whether anybody will be told, and who, at the end of the window's second
 * line: the rules' state in a word and the channels they tell drawn as the
 * services they are — through Automation's own `ChannelNames`, so the two
 * pages say the same thing about the same rule. A page that can show a 4%
 * error rate at three in the afternoon and say nothing about whether it
 * would have said so at three in the morning is leaving out the part that
 * matters. What each rule watches is a hover away, on the word.
 *
 * A firing rule's reading is over its own window, which is not the one on
 * screen, so the window is said: "4.2% failing over 5 min".
 */
// As tall as the Add alert button a line with no rule carries, in every state:
// the line was 20px waiting and 24px with the button, and the rows moved.
const ALERTS_LINE = "min-h-5"

function AlertsWords({
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
    "flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-hint text-muted-foreground",
  )

  if (rules.length === 0) {
    return (
      <div className={line}>
        <Status
          tone="stopped"
          label="No alerts"
          title="Nobody is told when this deployment fails or slows."
        />
        {onAdd ? (
          <Button size="xs" variant="outline" onClick={onAdd} className="ml-1 h-5">
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
        <Status
          tone="stopped"
          label="Alerts paused"
          title={`${rules.length === 1 ? "The one rule is paused" : `All ${rules.length} rules are paused`} — nobody is told.`}
        />
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
          <Status
            tone="running"
            label="All quiet"
            title={`${watching.length} of ${plural(rules.length, "alert")} watching — ${watching
              .map((rule) => ALERT_KINDS[rule.kind].describe(rule.threshold, rule.windowMinutes))
              .join("; ")}`}
          />
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
