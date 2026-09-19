"use client"

import { useState } from "react"
import { useSearchParams } from "next/navigation"
import { LogWorkspace } from "@/components/logs/log-workspace"
import type { LogFilterState, LogMode, LogTimeRange } from "@/components/logs/types"
import { EMPTY_FILTER } from "@/lib/log-filter"
import { get } from "@/lib/api"
import { percent, relativeTime } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentLifecycle, DeploymentRequests } from "@/lib/types"
import { latency, perMinute } from "@/lib/requests"
import { Pane } from "@/components/panel"
import { EmptyNote } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { FilterChip, tabClasses } from "@/components/tabs"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useProject } from "@/components/deploy/project-context"
import {
  EMPTY_REQUEST_QUERY,
  RequestsWorkspace,
  type RequestQuery,
} from "@/components/deploy/requests-workspace"
import { LifecycleFeed } from "@/components/deploy/lifecycle-feed"

type LogSource = { containerId: string; name: string; activationUrl?: string }

// `GET .../runs/{run}/logs` — the same handoff `run-logs.tsx` reads for a
// single run, kept local (types.ts has no shape for it) rather than guessed
// from `detail.runtime.services`, which carries no `activationUrl` at all.
type RunLogs = {
  status: "available" | "unavailable"
  reason?: string
  sources: LogSource[]
}

type View = "requests" | "output" | "events"

/**
 * One deployment, read three ways.
 *
 * This page used to be one thing: the live container's standard output, the
 * same workspace as the host-wide Logs page with the source fixed to one
 * container. For a modern framework that is a startup banner and then silence
 * — a Next.js or Rails production server prints nothing per request — so a
 * healthy deployment serving a thousand requests a minute showed thirty-nine
 * lines, ending at "Ready in 236ms", and never changed again. The page was not
 * broken; it was answering a question nobody had.
 *
 * "Logs" turned out to be one word covering three questions, and only the
 * least useful of them was answered:
 *
 *   **Requests** — is it serving traffic, and how well? Read from the ingress,
 *   which is the one place every request passes through whatever the
 *   application chose to say about itself.
 *
 *   **Output** — what did the application print? The original pane, kept whole.
 *
 *   **Events** — what happened to the container? Docker's own record: exits
 *   with their codes, OOM kills, restart policies firing, health flips.
 *
 * The readings above the pane come from all three at once, because the first
 * thing a reader wants is not a view, it is whether anything is wrong.
 */
export function ProjectLogs() {
  const project = useProject()
  const search = useSearchParams()
  const { runtime } = project.detail
  const liveRun = project.liveRun

  const [view, setView] = useState<View>(() => {
    const asked = search.get("view")
    return asked === "output" || asked === "events" ? asked : "requests"
  })
  const [query, setQuery] = useState<RequestQuery>(EMPTY_REQUEST_QUERY)

  // The runtime snapshot names the containers but never their activation
  // window; the live run's own log handoff (`run-logs.tsx`'s endpoint) is
  // the one place `activationUrl` is computed, so it is read here too.
  const runLogs = usePoll(
    (signal) =>
      liveRun
        ? get<RunLogs>(`/deploy/${project.projectId}/runs/${liveRun.id}/logs`, undefined, signal)
        : Promise.resolve(undefined),
    5000,
    [project.projectId, liveRun?.id],
    { enabled: Boolean(liveRun) },
  )

  // The readings are the page's own, not a view's: they hold still while the
  // reader moves between Requests, Output and Events, which is what lets the
  // error rate be the thing that sent them to Output in the first place.
  //
  // `limit: 1` because only the summary is wanted here; the rows are the
  // Requests view's own. Both are answered from the record the server holds
  // in memory — it reads the file once and then only what the proxy appended
  // since — so this poll and the view's share one read rather than being two
  // scans of the same bytes.
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
  // The window is asked of the server rather than computed here: "in the last
  // hour" worked out during render reads the clock on every re-render, so the
  // figure would depend on when React happened to paint. The fetcher runs in an
  // effect, which is where reading a clock belongs.
  const lifecycle = usePoll<DeploymentLifecycle>(
    (signal) =>
      get<DeploymentLifecycle>(
        `/deploy/${project.projectId}/lifecycle`,
        { since: new Date(Date.now() - 3_600_000).toISOString(), limit: 200 },
        signal,
      ),
    30000,
    [project.projectId],
  )

  const sources =
    runLogs.data?.sources ??
    (runtime?.status === "available"
      ? runtime.services.map((service) => ({
          containerId: service.containerId,
          name: service.name,
        }))
      : [])

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-6">
      <TrafficReadings requests={requests.data} lifecycle={lifecycle.data} />

      {/* A floor rather than a fill: the project shell's `Page` is a flowing
          column, so a pane that only said `flex-1` sized itself to its rows and
          left the rest of the viewport empty beneath it. */}
      <Pane className="min-h-[40rem] flex-1">
        <div className="flex min-h-10 shrink-0 items-stretch border-b border-hairline pr-1 pl-2">
          <nav aria-label="Log view" className="flex min-w-0 flex-1 items-stretch overflow-x-auto">
            <ViewTab active={view === "requests"} onClick={() => setView("requests")}>
              Requests
            </ViewTab>
            <ViewTab active={view === "output"} onClick={() => setView("output")}>
              Output
            </ViewTab>
            <ViewTab active={view === "events"} onClick={() => setView("events")}>
              Events
            </ViewTab>
          </nav>
        </div>

        {view === "requests" && (
          <RequestsWorkspace
            projectId={project.projectId}
            query={query}
            onQueryChange={setQuery}
          />
        )}
        {view === "output" && <OutputView sources={sources} runtime={runtime} />}
        {view === "events" && <LifecycleFeed projectId={project.projectId} />}
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
 * What is happening right now, in four figures.
 *
 * Taken over the last hour rather than the view's own window, so they keep
 * meaning the same thing while the reader narrows the rows beneath them. Each
 * is a rate or a share rather than a count, for the same reason: "1,204" means
 * nothing without the window it was counted over, and a figure whose meaning
 * depends on a control somewhere else is a figure people learn to ignore.
 */
function TrafficReadings({
  requests,
  lifecycle,
}: {
  requests?: DeploymentRequests
  lifecycle?: DeploymentLifecycle
}) {
  const summary = requests?.summary
  const served = requests?.status === "available"
  // The hour is already the server's answer; what is left is choosing the
  // actions that mean the container's life was interrupted.
  const restarts = (lifecycle?.events ?? []).filter(
    (event) => event.action === "restart" || event.action === "die" || event.action === "oom",
  )
  return (
    <StatGrid columns={4}>
      <StatTile
        label="Requests"
        key={`rate:${summary?.perMinute ?? "none"}`}
        value={served && summary ? perMinute(summary.perMinute) : "—"}
        trailing={served && summary ? "per minute" : undefined}
        hint={served ? "Last hour, at the ingress" : requests?.reason ? "No request record" : undefined}
      />
      <StatTile
        label="Failing"
        key={`err:${summary?.errorRate ?? "none"}`}
        value={served && summary ? percent(summary.errorRate * 100, 1) : "—"}
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
        label="Container"
        key={`restarts:${restarts.length}`}
        value={restarts.length === 0 ? "Steady" : restarts.length.toLocaleString()}
        tone={restarts.length > 0 ? "warning" : "default"}
        hint={
          restarts.length === 0
            ? "No exit or restart in the last hour"
            : `${restarts.length === 1 ? "event" : "events"} in the last hour — newest ${relativeTime(restarts[0].time)}`
        }
      />
    </StatGrid>
  )
}

/**
 * The container's own output, scoped to one service at a time. The original
 * pane, unchanged apart from where it sits: the service picker moved onto this
 * view's own strip, since the page's strip now belongs to the three views.
 */
function OutputView({
  sources,
  runtime,
}: {
  sources: LogSource[]
  runtime?: { status: string; reason?: string }
}) {
  const search = useSearchParams()
  const [activation, setActivation] = useState(false)
  const [picked, setPicked] = useState<string | null>(() => search.get("service"))
  const selected = picked ? sources.find((source) => source.containerId === picked) : sources[0]

  if (runtime?.status !== "available") {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <EmptyNote>{runtime?.reason || "Runtime services are unavailable."}</EmptyNote>
      </div>
    )
  }
  if (!selected) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <EmptyNote>
          {picked
            ? "This service is no longer available. Choose another service to view its output."
            : "No managed runtime logs yet. Output appears here when a container is created."}
        </EmptyNote>
      </div>
    )
  }

  return (
    <>
      <div className="flex min-h-11 shrink-0 items-center gap-2 border-b border-hairline px-2 py-1.5">
        <Select
          value={selected.containerId}
          onValueChange={(value) => {
            setPicked(value)
            setActivation(false)
          }}
        >
          <SelectTrigger size="sm" className="w-56" aria-label="Runtime log source">
            <SelectValue placeholder="Choose a service" />
          </SelectTrigger>
          <SelectContent>
            {sources.map((source) => (
              <SelectItem key={source.containerId} value={source.containerId}>
                {source.name || source.containerId}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {selected.activationUrl && (
          <FilterChip selected={activation} onClick={() => setActivation(!activation)}>
            Around activation
          </FilterChip>
        )}
      </div>
      <ScopedLogWorkspace
        key={`${selected.containerId}:${activation}`}
        source={selected}
        activation={activation}
      />
    </>
  )
}

function ScopedLogWorkspace({ source, activation }: { source: LogSource; activation: boolean }) {
  const activationParams = new URL(source.activationUrl || "/", "http://localhost").searchParams
  const [mode, setMode] = useState<LogMode>(activation ? "search" : "live")
  const [filter, setFilter] = useState<LogFilterState>(EMPTY_FILTER)
  const [range, setRange] = useState<LogTimeRange>(activation ? "custom" : "1h")
  const [since, setSince] = useState(activation ? activationParams.get("since") || "" : "")
  const [until, setUntil] = useState(activation ? activationParams.get("until") || "" : "")
  const [context, setContext] = useState(0)
  const [archives, setArchives] = useState(false)
  const [boot, setBoot] = useState(false)
  const [unit, setUnit] = useState("")
  const sourceId = `docker:${source.containerId}`
  return (
    <LogWorkspace
      // One column of a pane that already draws the frame, so it draws none.
      flush
      className="min-h-0 flex-1"
      source={{ id: sourceId, label: source.name, kind: "docker", rotated: false }}
      sourceId={sourceId}
      units={[]}
      mode={mode}
      onModeChange={setMode}
      filter={filter}
      onFilterChange={setFilter}
      unit={unit}
      onUnitChange={setUnit}
      range={range}
      onRangeChange={setRange}
      since={since}
      until={until}
      onSinceChange={setSince}
      onUntilChange={setUntil}
      onCustomRange={(from, to) => {
        setRange("custom")
        setSince(from.toISOString())
        setUntil(to.toISOString())
      }}
      context={context}
      onContextChange={setContext}
      archives={archives}
      onArchivesChange={setArchives}
      boot={boot}
      onBootChange={setBoot}
    />
  )
}
