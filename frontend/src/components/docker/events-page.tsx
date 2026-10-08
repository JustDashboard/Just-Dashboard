"use client"

import { useCallback, useMemo, useState } from "react"
import { ChartActivity, CheckCircle, ClockRewind, Cross, Warning } from "@/components/icons"
import { cn } from "@/lib/utils"
import { get } from "@/lib/api"
import { clock, duration, plural, relativeTime, timestamp } from "@/lib/format"
import {
  comebacks,
  containerActivity,
  dedupeEvents,
  foldBursts,
  foldRestarts,
  outcomeOf,
  perSlice,
  settleAskedExits,
  spanWords,
  type EventEntry,
} from "@/lib/docker-events"
import type { Container, DockerEvent, DockerEventFeed } from "@/lib/types"
import { useSessionState } from "@/lib/view-state"
import { useMetrics } from "@/hooks/use-metrics"
import { usePoll } from "@/hooks/use-poll"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { useNow } from "@/components/deploy/vocabulary"
import { socketReading } from "@/components/deploy/request-marks"
import { InfoTip } from "@/components/form"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { TileTrend } from "@/components/metrics/sparkline"
import { SeriesKey } from "@/components/overview/readings"
import { Page, PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { platformProduct } from "@/components/product-logo"
import { StatButton, StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { NumberTicker } from "@/components/ui/number-ticker"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { ActivityTable, OUTCOME, OutcomeKey, type Span } from "@/components/docker/event-activity"
import { EventFeed } from "@/components/docker/event-feed"

const HOUR = 3_600_000

/** The windows the page reads over; "All" is everything the dashboard has kept. */
const WINDOWS = [
  { value: "1h", label: "1h", ms: HOUR, words: "the last hour" },
  { value: "6h", label: "6h", ms: 6 * HOUR, words: "the last 6 hours" },
  { value: "24h", label: "24h", ms: 24 * HOUR, words: "the last day" },
  { value: "all", label: "All", ms: 0, words: "everything kept" },
] as const

const KINDS = [
  { id: "container", label: "Containers" },
  { id: "image", label: "Images" },
  { id: "volume", label: "Volumes" },
  { id: "network", label: "Networks" },
]

/** The four readings, each a question a press narrows the page to. */
type Reading = "failures" | "restarts" | "changes" | "external"

const READING_LABEL: Record<Reading, string> = {
  failures: "Failures",
  restarts: "Restarts",
  changes: "Created or removed",
  external: "External",
}

/** How many slices each reading's trend has across the window. */
const SLICES = 24

/** How many feed rows are drawn before "Show all". */
const FEED_ROWS = 150

/** Below this, the record is a restart of the dashboard rather than a quiet host. */
const RECENTLY_STARTED_MS = HOUR

function isExternal(event: DockerEvent) {
  return event.source === "docker" && !event.trigger
}

function isFailure(event: DockerEvent) {
  const outcome = outcomeOf(event)
  return outcome === "failed" || outcome === "unhealthy"
}

/**
 * What the Docker daemon did on this server, including while nobody was
 * looking, and who did it.
 *
 * Docker keeps no record of what it does — `docker events` shows what happens
 * from the moment it is run — so the dashboard listens and keeps the recent
 * past. The page used to be that record as one grey column of sentences:
 * nothing on it said whether anything was wrong, a database in a restart loop
 * was thirty rows pushing everything else off the screen, and no row said
 * which container it was beyond its name.
 *
 * It opens now on Docker's identity line, as Containers does — the engine as
 * its mark and version, the host, how long the dashboard has been listening
 * and how much it has kept — with the verdict at its right end (a restart
 * loop, the containers that failed, or that nothing did), the socket's state
 * and the window the page reads over.
 *
 * Then four readings over that window, each counting up as it lands with its
 * shape across the window as its trend, each a press that narrows the page to
 * what it counts (§15 pass 2's `StatButton`): exits that failed, comebacks,
 * objects made or removed, and actions nothing in this dashboard or compose
 * explains.
 *
 * Then the containers the record names as a table, one row each, worst first:
 * the container as its product, its state now from Docker's listing, its
 * window as a lane of marks in each outcome's hue with a restart loop as a
 * red band, its failures and comebacks, and who last acted on it. A row
 * narrows the feed to that container.
 *
 * Last, the feed itself — restart loops folded, every row as the thing it
 * happened to and who did it, a new one rising as the socket delivers it.
 *
 * The buffer is read whole once, and its newest hundred again every fifteen
 * seconds: the polled copy is the one the server laid against the audit log,
 * so an action somebody here pressed reads "wayy" rather than "External" a
 * moment after the socket delivered it.
 */
export function EventsPage() {
  const { host } = useMetrics()
  const [range, setRange] = useSessionState<string>("docker.events.window", "24h")
  const [kinds, setKinds] = useSessionState<string[]>("docker.events.kinds", [])
  const [search, setSearch] = useSessionState("docker.events.query", "")
  const [reading, setReading] = useSessionState<Reading | "">("docker.events.reading", "")
  const [container, setContainer] = useSessionState("docker.events.container", "")
  const [everything, setEverything] = useState(false)
  const [live, setLive] = useState<DockerEvent[]>([])
  const now = useNow(15_000)

  const buffer = usePoll(
    (signal) => get<DockerEventFeed>("/docker/events", { limit: 2000 }, signal),
    0,
  )
  const recent = usePoll(
    (signal) => get<DockerEventFeed>("/docker/events", { limit: 100 }, signal),
    15_000,
  )
  const ping = usePoll(
    (signal) => get<{ serverVersion?: string }>("/docker/ping", undefined, signal),
    0,
  )
  const listing = usePoll(
    (signal) => get<Container[]>("/docker/containers/", undefined, signal),
    15_000,
  )

  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type !== "events") return
    const batch = envelope.data as DockerEvent[]
    setLive((prev) => [...batch].concat(prev).slice(0, 2000))
  }, [])
  const socket = useSocket("/docker/events/stream", { onMessage })

  // The polled copies first: `dedupeEvents` keeps the first of each, and
  // those are the ones with the audit log laid against them. An exit that
  // answered a stop or a restart is read as the stop it was.
  const all = useMemo(
    () =>
      settleAskedExits(
        dedupeEvents([...(recent.data?.events ?? []), ...(buffer.data?.events ?? []), ...live]),
      ),
    [recent.data, buffer.data, live],
  )

  const meta = recent.data ?? buffer.data
  const since = meta?.since && Date.parse(meta.since) > 0 ? Date.parse(meta.since) : undefined
  const win = WINDOWS.find((w) => w.value === range) ?? WINDOWS[2]
  const newest = all.length ? Date.parse(all[0].time) : now
  const to = Math.max(now, newest)
  const oldest = all.length ? Date.parse(all[all.length - 1].time) : to
  const from = win.ms ? to - win.ms : Math.min(since ?? oldest, oldest)
  const span: Span = { from, to, since }

  const inWindow = useMemo(() => all.filter((e) => Date.parse(e.time) >= from), [all, from])
  const activity = useMemo(() => containerActivity(inWindow, to), [inWindow, to])
  const back = useMemo(() => comebacks(inWindow), [inWindow])

  const tally = useMemo(() => {
    const failed = activity.filter((row) => row.failures > 0)
    return {
      failures: activity.reduce((sum, row) => sum + row.failures, 0),
      failed,
      restarts: back.size,
      looping: activity.filter((row) => row.looping),
      changes: inWindow.filter((e) => outcomeOf(e) === "changed"),
      external: inWindow.filter(isExternal),
      trend: {
        failures: perSlice(inWindow, from, to, SLICES, isFailure),
        restarts: perSlice(inWindow, from, to, SLICES, (e) => back.has(e)),
        changes: perSlice(inWindow, from, to, SLICES, (e) => outcomeOf(e) === "changed"),
        external: perSlice(inWindow, from, to, SLICES, isExternal),
      },
    }
  }, [activity, back, inWindow, from, to])

  const rows = useMemo(
    () =>
      activity.filter((row) => {
        if (reading === "failures") return row.failures > 0 || row.events.some(isFailure)
        if (reading === "restarts") return row.restarts > 0
        if (reading === "changes") return row.events.some((e) => outcomeOf(e) === "changed")
        if (reading === "external") return row.outside > 0
        return true
      }),
    [activity, reading],
  )
  const chosen = activity.find((row) => row.key === container)

  const kindCounts = useMemo(() => {
    const out: Record<string, number> = {}
    for (const e of inWindow) out[e.type] = (out[e.type] ?? 0) + 1
    return out
  }, [inWindow])

  const entries = useMemo(() => {
    const wanted = new Set(kinds)
    const needle = search.trim().toLowerCase()
    const kept = inWindow.filter(
      (e) =>
        (wanted.size === 0 || wanted.has(e.type)) &&
        (!container || (e.id || e.name) === container) &&
        (!needle ||
          [e.message, e.name, e.image, e.stack, e.service, e.action, e.trigger?.actor]
            .join(" ")
            .toLowerCase()
            .includes(needle)),
    )
    return foldBursts(foldRestarts(kept).filter((entry) => matches(entry, reading, back)))
  }, [inWindow, kinds, search, container, reading, back])
  const shown = everything ? entries : entries.slice(0, FEED_ROWS)
  const filterKey = [range, kinds.join(","), search, reading, container].join("\u0000")

  const toggleReading = (next: Reading) => setReading(reading === next ? "" : next)
  const selectContainer = (key: string) => setContainer(container === key ? "" : key)
  const refresh = () => {
    buffer.refresh()
    recent.refresh()
    listing.refresh()
  }

  const header = <PageContext eyebrow="Docker" title="Events" />

  if (buffer.loading && !buffer.data) {
    return (
      <Page>
        {header}
        <LoadingPanel />
      </Page>
    )
  }
  if (buffer.error && !buffer.data) {
    return (
      <Page>
        {header}
        <ErrorState error={buffer.error} onRetry={buffer.refresh} />
      </Page>
    )
  }

  const listening = meta?.listening ?? false
  const version = ping.data?.serverVersion

  return (
    <Workspace
      name="Events"
      refresh={refresh}
      escape={() => {
        if (search) {
          setSearch("")
          return true
        }
        if (container) {
          setContainer("")
          return true
        }
        if (reading) {
          setReading("")
          return true
        }
        if (kinds.length) {
          setKinds([])
          return true
        }
        return false
      }}
      commands={[
        {
          id: "failures",
          label: reading === "failures" ? "Show every event" : "Show only failures",
          run: () => toggleReading("failures"),
        },
      ]}
    >
      <Page className="animate-rise">
        {header}

        <HostIdentity
          mark="docker"
          title={version ? `Docker Engine ${version}` : "Docker Engine"}
          facts={
            <>
              {host && (
                <>
                  <HostFact product={platformProduct(host.platform)}>{host.hostname}</HostFact>
                  <FactDot />
                </>
              )}
              {since && listening && (
                <>
                  <span className="numeric" title={`Listening since ${timestamp(meta?.since)}`}>
                    listening for {duration((to - since) / 1000)}
                  </span>
                  <FactDot />
                </>
              )}
              <span className="numeric" title="The dashboard keeps the newest 2,000 in memory">
                {plural(meta?.buffered ?? all.length, "event")} kept
              </span>
              <FactDot />
              <span className="numeric">
                {plural(activity.length, "container")} in {win.words}
              </span>
            </>
          }
          aside={
            <div className="flex flex-wrap items-center gap-3">
              <Verdict
                listening={listening}
                looping={tally.looping.map((row) => row.name)}
                failed={tally.failed.filter((row) => !row.removed).length}
                pressed={reading === "failures"}
                onPress={() => {
                  if (tally.looping.length === 1) selectContainer(tally.looping[0].key)
                  else toggleReading("failures")
                }}
              />
              <Status {...socketReading(socket.state)} className="text-xs" />
              <ToggleGroup
                type="single"
                size="sm"
                variant="outline"
                value={win.value}
                onValueChange={(value) => value && setRange(value)}
                aria-label="Window"
              >
                {WINDOWS.map((w) => (
                  <ToggleGroupItem key={w.value} value={w.value} className="px-2.5 text-hint">
                    {w.label}
                  </ToggleGroupItem>
                ))}
              </ToggleGroup>
              <WorkspaceHelp compact />
            </div>
          }
        />

        {!listening && (
          <Notice tone="warning" icon={Warning} title="The Docker event stream is not connected">
            What is here was recorded before the connection dropped, and nothing new is being kept
            until it is back.
          </Notice>
        )}

        {all.length === 0 ? (
          <EmptyState {...quietReading(since, to, listening)} />
        ) : (
          <>
            <StatGrid columns={4} dense className="animate-rise">
              <StatButton
                label="Show only failures"
                pressed={reading === "failures"}
                onClick={() => toggleReading("failures")}
              >
                <StatTile
                  className="h-full"
                  label={
                    <>
                      <SeriesKey color={OUTCOME.failed.color} />
                      Failures
                    </>
                  }
                  value={<NumberTicker value={tally.failures} />}
                  tone={tally.failures > 0 ? "danger" : "success"}
                  trend={
                    <TileTrend
                      values={tally.trend.failures}
                      color={OUTCOME.failed.color}
                      label={`Failures across ${win.words}`}
                    />
                  }
                  hint={
                    tally.failed.length > 0
                      ? tally.failed
                          .slice(0, 3)
                          .map((row) => (row.oom ? `${row.name} out of memory` : row.name))
                          .join(" · ")
                      : "every exit was clean"
                  }
                />
              </StatButton>
              <StatButton
                label="Show only restarts"
                pressed={reading === "restarts"}
                onClick={() => toggleReading("restarts")}
              >
                <StatTile
                  className="h-full"
                  label={
                    <>
                      <SeriesKey color={OUTCOME.restarted.color} />
                      Restarts
                    </>
                  }
                  value={<NumberTicker value={tally.restarts} />}
                  tone={tally.looping.length > 0 ? "warning" : "default"}
                  trend={
                    <TileTrend
                      values={tally.trend.restarts}
                      color={OUTCOME.restarted.color}
                      label={`Restarts across ${win.words}`}
                    />
                  }
                  hint={restartHint(tally.looping, activity)}
                />
              </StatButton>
              <StatButton
                label="Show only what was created or removed"
                pressed={reading === "changes"}
                onClick={() => toggleReading("changes")}
              >
                <StatTile
                  className="h-full"
                  label={
                    <>
                      <SeriesKey color={OUTCOME.changed.color} />
                      Created or removed
                    </>
                  }
                  value={<NumberTicker value={tally.changes.length} />}
                  trend={
                    <TileTrend
                      values={tally.trend.changes}
                      color={OUTCOME.changed.color}
                      label={`Objects created or removed across ${win.words}`}
                    />
                  }
                  hint={changeHint(tally.changes)}
                />
              </StatButton>
              <StatButton
                label="Show only external actions"
                pressed={reading === "external"}
                onClick={() => toggleReading("external")}
              >
                <StatTile
                  className="h-full"
                  label={
                    <>
                      <SeriesKey color="var(--tag-violet)" />
                      External
                    </>
                  }
                  value={<NumberTicker value={tally.external.length} />}
                  trend={
                    <TileTrend
                      values={tally.trend.external}
                      color="var(--tag-violet)"
                      label={`External actions across ${win.words}`}
                    />
                  }
                  hint={
                    tally.external.length > 0
                      ? `${[...new Set(tally.external.map((e) => e.name))].slice(0, 3).join(", ")}`
                      : "everything is explained"
                  }
                />
              </StatButton>
            </StatGrid>

            {/* Framed, because it is a table: the grid owns a scroll region and
                the edge is what says so (§2). */}
            <Panel aria-label="Containers">
              <PanelHeader
                title={
                  <>
                    Containers
                    <span className="numeric ml-2 text-body font-normal text-muted-foreground">
                      {rows.length}
                    </span>
                  </>
                }
                actions={<OutcomeKey className="max-md:hidden" />}
              />
              <PanelBody flush>
                {rows.length === 0 ? (
                  <EmptyState
                    icon={CheckCircle}
                    title={
                      reading
                        ? `No container has ${READING_LABEL[reading].toLowerCase()} in ${win.words}`
                        : `Nothing happened to a container in ${win.words}`
                    }
                    className="my-4"
                  />
                ) : (
                  <ActivityTable
                    key={reading}
                    rows={rows}
                    containers={listing.data}
                    span={span}
                    now={to}
                    selected={container}
                    onSelect={selectContainer}
                  />
                )}
              </PanelBody>
              <PanelFooter className="text-hint text-muted-foreground">
                <span>a loop still going first, then what failed, then the newest</span>
                <span className="text-muted-foreground/40">·</span>
                <span>a row shows only its events below</span>
              </PanelFooter>
            </Panel>

            <Panel aria-label="Events">
              <PanelHeader
                title={
                  <>
                    Events
                    <span className="numeric ml-2 text-body font-normal text-muted-foreground">
                      {entries.length}
                    </span>
                  </>
                }
              >
                <ChipStrip aria-label="Kind" className="mr-auto">
                  <FilterChip selected={kinds.length === 0} onClick={() => setKinds([])}>
                    All
                    <ChipCount>{inWindow.length}</ChipCount>
                  </FilterChip>
                  {KINDS.map((k) => (
                    <FilterChip
                      key={k.id}
                      selected={kinds.includes(k.id)}
                      onClick={() =>
                        setKinds((prev) =>
                          prev.includes(k.id) ? prev.filter((x) => x !== k.id) : [...prev, k.id],
                        )
                      }
                    >
                      {k.label}
                      <ChipCount>{kindCounts[k.id] ?? 0}</ChipCount>
                    </FilterChip>
                  ))}
                </ChipStrip>
              </PanelHeader>
              <PanelToolbar>
                <SearchInput
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  placeholder="Container, image, stack or person"
                  containerClassName="sm:w-72"
                />
                <div className="flex flex-wrap items-center gap-1 sm:ml-auto">
                  {reading && (
                    <FilterChip
                      selected
                      aria-label={`Stop showing only ${READING_LABEL[reading].toLowerCase()}`}
                      onClick={() => setReading("")}
                    >
                      {READING_LABEL[reading]}
                      <Cross aria-hidden className="size-3" />
                    </FilterChip>
                  )}
                  {container && (
                    <FilterChip
                      selected
                      aria-label="Show every container's events"
                      onClick={() => setContainer("")}
                    >
                      {chosen?.name ?? container.slice(0, 12)}
                      <Cross aria-hidden className="size-3" />
                    </FilterChip>
                  )}
                </div>
              </PanelToolbar>
              <PanelBody>
                {entries.length === 0 ? (
                  <EmptyState
                    icon={ChartActivity}
                    title="Nothing matches"
                    description={`Nothing in ${win.words} matches these filters. Clear one, or widen the window.`}
                    className="my-2"
                  />
                ) : (
                  <div
                    className={cn(
                      "-mx-3 overflow-auto px-3",
                      // Its own scroll once it is longer than a screen, so the
                      // containers above stay a scroll away rather than pages.
                      "max-h-[calc(100svh-14rem)]",
                    )}
                  >
                    <EventFeed key={filterKey} entries={shown} onContainer={selectContainer} />
                    {entries.length > shown.length && (
                      <Button
                        size="xs"
                        variant="ghost"
                        className="mt-2 -ml-2 text-foreground"
                        onClick={() => setEverything(true)}
                      >
                        Show all {entries.length}
                      </Button>
                    )}
                  </div>
                )}
              </PanelBody>
              <PanelFooter className="gap-x-2 gap-y-1 text-hint text-muted-foreground">
                <Status {...socketReading(socket.state)} className="text-hint" />
                <span className="text-muted-foreground/40">·</span>
                <span className="numeric">{plural(all.length, "event")} on this page</span>
                {meta?.since && (
                  <>
                    <span className="text-muted-foreground/40">·</span>
                    <span className="whitespace-nowrap" title={timestamp(meta.since)}>
                      kept since <span className="numeric">{clock(meta.since)}</span>
                    </span>
                  </>
                )}
                <span className="text-muted-foreground/40">·</span>
                <span className="flex items-center gap-1 whitespace-nowrap">
                  in memory
                  <InfoTip label="How long this record is kept">
                    Docker keeps no record of what it did. The dashboard listens and keeps the
                    newest 2,000 events in its own memory, so a restart of the dashboard starts the
                    record again. Everything the dashboard itself did is also in the audit log,
                    which survives a restart.
                  </InfoTip>
                </span>
              </PanelFooter>
            </Panel>
          </>
        )}
      </Page>
    </Workspace>
  )
}

/** Whether a feed entry is one the pressed reading counts. */
function matches(entry: EventEntry, reading: Reading | "", back: Set<DockerEvent>) {
  if (!reading) return true
  if (entry.kind === "loop") return reading === "failures" || reading === "restarts"
  const event = entry.event
  if (reading === "failures") return isFailure(event) || Boolean(entry.oom)
  if (reading === "restarts") return event.action === "restart" || back.has(event)
  if (reading === "changes") return outcomeOf(event) === "changed"
  return isExternal(event)
}

/**
 * The verdict at the identity line's end: a loop is named, since it is the
 * one thing on the page still happening; then the containers that failed;
 * then that nothing did. A press narrows the page to it.
 */
function Verdict({
  listening,
  looping,
  failed,
  pressed,
  onPress,
}: {
  listening: boolean
  looping: string[]
  failed: number
  pressed: boolean
  onPress: () => void
}) {
  if (looping.length === 0 && failed === 0) {
    return listening ? (
      <Status tone="running" label="Nothing failed" />
    ) : (
      <Status tone="warning" label="Not listening" />
    )
  }
  return (
    <button
      type="button"
      aria-pressed={pressed}
      onClick={onPress}
      className="rounded-md px-1.5 py-1 focus-ring transition-colors hover:bg-row-hover"
    >
      <Status
        tone="danger"
        live={looping.length > 0}
        label={
          looping.length === 1
            ? `${looping[0]} is in a restart loop`
            : looping.length > 1
              ? `${plural(looping.length, "container")} in a restart loop`
              : `${plural(failed, "container")} failed`
        }
      />
    </button>
  )
}

function restartHint(
  looping: ReturnType<typeof containerActivity>,
  activity: ReturnType<typeof containerActivity>,
) {
  const loop = looping[0]
  if (loop) {
    const folded = foldRestarts(loop.events)[0]
    if (folded?.kind === "loop") {
      return `${loop.name} ×${folded.times} in ${spanWords(
        Date.parse(folded.until) - Date.parse(folded.since),
      )}`
    }
  }
  const back = activity.filter((row) => row.restarts > 0)
  if (back.length === 0) return "nothing came back after exiting"
  return back
    .slice(0, 3)
    .map((row) => `${row.name} ×${row.restarts}`)
    .join(" · ")
}

function changeHint(changes: DockerEvent[]) {
  if (changes.length === 0) return "nothing was created or removed"
  const count = (test: (e: DockerEvent) => boolean) => changes.filter(test).length
  const pulled = count((e) => e.action === "pull")
  const created = count((e) => e.action === "create")
  const removed = count((e) => e.action === "destroy" || e.action === "delete")
  return [
    pulled > 0 && `${pulled} pulled`,
    created > 0 && `${created} created`,
    removed > 0 && `${removed} removed`,
  ]
    .filter(Boolean)
    .join(" · ")
}

/**
 * Why the record is empty. It is this process's, so a dashboard that started
 * a few minutes ago has not seen anything yet — which is not the steadiness
 * "nothing happened" would claim.
 */
function quietReading(since: number | undefined, now: number, listening: boolean) {
  if (!listening) {
    return {
      icon: Warning,
      title: "Nothing is being recorded",
      description:
        "The dashboard is not connected to Docker's event stream, so nothing Docker does is kept until it is.",
    }
  }
  if (since !== undefined && now - since < RECENTLY_STARTED_MS) {
    return {
      icon: ClockRewind,
      title: "Nothing has happened since the dashboard started",
      description: `The record lives in the dashboard's own memory and begins when it starts — ${relativeTime(
        new Date(since).toISOString(),
      )}. Events appear here as Docker emits them.`,
    }
  }
  return {
    icon: CheckCircle,
    title: "Docker has been quiet",
    description: since
      ? `Listening since ${timestamp(new Date(since).toISOString())}. Nothing was started, stopped, created or removed since then.`
      : "Nothing was started, stopped, created or removed.",
  }
}
