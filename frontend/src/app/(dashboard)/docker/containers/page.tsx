"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { Box, Cross, Warning } from "@/components/icons"
import { get } from "@/lib/api"
import { dedupeEvents } from "@/lib/docker-events"
import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import type {
  Container,
  ContainerSparkline,
  ContainerStats,
  DockerDiagnosis,
  DockerEngineInfo,
  DockerEvent,
} from "@/lib/types"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useMetrics } from "@/hooks/use-metrics"
import { useConfirm } from "@/components/confirm-dialog"
import { useNow } from "@/components/deploy/vocabulary"
import { FactDot, HostFact, HostIdentity, platformName } from "@/components/metrics/host-identity"
import { StreamState } from "@/components/overview/readings"
import { Page, PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { platformProduct } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { EmptyState, ErrorState } from "@/components/state"
import { useInspectionOrder } from "@/components/procs/inspection-order"
import { useDockerFindingActions } from "@/components/docker/finding-actions"
import { AttentionPanel } from "@/components/docker/attention"
import { ExplainIcon } from "@/components/docker/explain"
import { ContainerBand } from "@/components/docker/container-band"
import { ContainerRows } from "@/components/docker/container-table"
import { useContainerControl } from "@/components/docker/container-actions"
import {
  containerBucket,
  FIRST_DIR,
  networkRates,
  oomKilledIds,
  recentEntries,
  sortContainers,
  type ContainerBucket,
  type ContainerSort,
  type NetRate,
  type SortKey,
} from "@/components/docker/containers"
import { hueFor, LANES } from "@/lib/hue"
import { Button } from "@/components/ui/button"

/**
 * What is running on this server, what it is using, and what just happened
 * to it.
 *
 * The engine first, as the identity line Services and Live open on — Docker
 * as its mark and version, the host it runs on, the storage driver, how many
 * containers are running and how many of those a health check vouches for —
 * with the verdict at its right end: how many are failing, which narrows the
 * table to them, or that nothing is.
 *
 * Then `ContainerBand`: the containers using the most processor and memory as
 * spans of one bar the size of the machine, and Docker's own record of what
 * started, stopped, crashed or was killed for memory. It replaced the runtime
 * health bar (§15 pass 2 names the exit): running, failing, starting, paused
 * and stopped are the state chips in the table's head, which count *and*
 * narrow, the two that mean trouble in their tones and drawn only while there
 * is one; the health check counts are the identity line's fact and each row's
 * second line, where "no health check" is said out loud as it always was.
 *
 * Then the table, framed because it is one (§2): each container as its
 * image's product (§14), its compose project in that project's lane hue and a
 * press from being the only one listed, its state with how long it has been
 * in it — ticking — and its readings as figures beside short bars, fed by the
 * same socket every two seconds. It sorts by any heading, failing first by
 * default, and holds its order under the pointer. A container new since the
 * last frame rises into place. The row opens the container's own page.
 *
 * Last, the findings that are not about whether anything is up: posture,
 * exposure and configuration, grouped as the Docker overview groups them.
 * They stood over the list until 0.7.1; each row's issue count now says
 * which containers they are about, so the list comes first.
 */

type StateFilter = ContainerBucket | "attention"

/** The state chips, in the order they are asked about; the toned ones only while there is one. */
const STATES: { value: StateFilter; label: string; tone?: "danger" | "warning" }[] = [
  { value: "running", label: "Running" },
  { value: "failing", label: "Failing", tone: "danger" },
  { value: "starting", label: "Starting", tone: "warning" },
  { value: "paused", label: "Paused" },
  { value: "stopped", label: "Stopped" },
  { value: "attention", label: "Needs attention", tone: "warning" },
]

const FILTERS = new Set<string>(STATES.map((s) => s.value))

const DOT: Record<StateFilter, string> = {
  running: "bg-success",
  failing: "bg-destructive",
  starting: "bg-warning",
  paused: "bg-muted-foreground",
  stopped: "bg-muted-foreground/50",
  attention: "bg-warning",
}

/** The stack chip for the containers no compose project owns; brackets are not a project name. */
const STANDALONE = "(standalone)"

const DEFAULT_SORT: ContainerSort = { key: "state", dir: "asc" }

const SORT_WORDS: Record<SortKey, string> = {
  state: "failing first, then by name",
  name: "by name",
  cpu: "by processor",
  memory: "by memory",
}

/** How much of Docker's event log the page holds for Recent. */
const EVENTS_KEPT = 200

export default function ContainersPage() {
  const router = useRouter()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const { host, snapshot } = useMetrics()
  const now = useNow(15_000)
  const [containers, setContainers] = useState<Container[]>([])
  const [stats, setStats] = useState<Record<string, ContainerStats>>({})
  const [rates, setRates] = useState<Record<string, NetRate>>({})
  const [events, setEvents] = useState<DockerEvent[]>([])
  const [socketError, setSocketError] = useState<string>()
  const [query, setQuery] = useSessionState("docker.containers.query", "")
  const [remembered, setFilter] = useSessionState<string>("docker.containers.state", "")
  // A remembered "all" from before the chips were toggles is no filter.
  const filter = (FILTERS.has(remembered) ? remembered : "") as StateFilter | ""
  const [stack, setStack] = useSessionState<string>("docker.containers.stack", "")
  const [sort, setSort] = useSessionState<ContainerSort>("docker.containers.sort", DEFAULT_SORT)
  const [, rememberNavigation] = useSessionState<{ id: string; name: string }[]>(
    "docker.containers.navigation",
    [],
  )

  /**
   * An hour of shape per container, in one request. The live socket shows what
   * every container is doing this second, which is the wrong question once
   * something already went wrong: a container that pinned a core for ten
   * minutes and then settled reads as idle.
   */
  const trends = usePoll<ContainerSparkline[]>(
    (signal) =>
      get<ContainerSparkline[]>(
        "/docker/containers/stats/history",
        { range: "1h", points: 40 },
        signal,
      ),
    120_000,
    [],
  )
  const trendByName = useMemo(() => {
    const map = new Map<string, ContainerSparkline>()
    for (const line of trends.data ?? []) map.set(line.name, line)
    return map
  }, [trends.data])

  const health = usePoll<DockerDiagnosis>(
    (signal) => get<DockerDiagnosis>("/docker/health", undefined, signal),
    60_000,
  )
  // The engine's name and its storage are facts, not readings: read once a
  // few minutes, and a host that refuses the call still lists its containers.
  const info = usePoll<DockerEngineInfo>(
    (signal) => get<DockerEngineInfo>("/docker/info", undefined, signal),
    300_000,
  )

  // The frame before this one, which the network rates are a difference from.
  const lastFrame = useRef<Record<string, ContainerStats>>({})
  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type === "containers") {
      setContainers(envelope.data as Container[])
      setSocketError(undefined)
    } else if (envelope.type === "stats") {
      const rows = envelope.data as ContainerStats[]
      const frame = Object.fromEntries(rows.map((r) => [r.id, r]))
      setRates(networkRates(lastFrame.current, rows))
      lastFrame.current = frame
      setStats(frame)
    } else if (envelope.type === "error") {
      setSocketError(envelope.error)
    }
  }, [])
  const stream = useSocket("/docker/containers/stream", { onMessage })

  // The socket sends the buffered past on connect and then each event as it
  // happens, so Recent is live without a poll.
  const onEvents = useCallback((envelope: Envelope) => {
    if (envelope.type !== "events") return
    const batch = envelope.data as DockerEvent[]
    setEvents((previous) => dedupeEvents([...batch, ...previous]).slice(0, EVENTS_KEPT))
  }, [])
  const eventStream = useSocket("/docker/events/stream", {
    query: { kinds: "container" },
    onMessage: onEvents,
  })
  const entries = useMemo(() => recentEntries(events), [events])
  const oomKilled = useMemo(() => oomKilledIds(entries), [entries])

  // `refresh` is stable, so the verbs a row memoises stay stable with it.
  const { pending, act } = useContainerControl(health.refresh)

  /** Goes to one container, optionally straight at a tab. */
  const open = useCallback(
    (id: string, tab?: string) => {
      rememberNavigation(
        Array.from(
          document.querySelectorAll<HTMLElement>(
            "[data-native-workspace='Docker'] [data-workspace-item]",
          ),
        )
          .slice(0, 500)
          .map((item) => ({
            id: item.dataset.workspaceItem!,
            name: item.dataset.workspaceName ?? "",
          })),
      )
      const suffix = tab ? `?tab=${encodeURIComponent(tab)}` : ""
      router.push(`/docker/containers/${encodeURIComponent(id)}${suffix}`)
    },
    [router, rememberNavigation],
  )

  /*
    `?container=` opened a sheet on this page until 2026-09-21, and
    `useQuerySelection` also put a remembered one back on arrival. Both are
    addresses that exist in the wild — a bookmark, a tab restored by the
    browser, a link pasted into a ticket — so they land on the container
    instead of on a list that quietly ignores them.
  */
  const legacy = useSearchParams().get("container")
  useEffect(() => {
    if (legacy) router.replace(`/docker/containers/${encodeURIComponent(legacy)}`)
  }, [legacy, router])

  const runFix = useDockerFindingActions({ confirm, onChanged: health.refresh, open })

  const flagged = useMemo(() => {
    const ids = new Set<string>()
    for (const finding of health.data?.findings ?? []) {
      if (finding.targetId && (finding.severity === "critical" || finding.severity === "warning")) {
        ids.add(finding.targetId)
      }
    }
    return ids
  }, [health.data])

  const bucketOf = useCallback(
    (c: Container) => containerBucket(c, oomKilled.has(c.id)),
    [oomKilled],
  )

  const counts = useMemo(() => {
    const out: Record<StateFilter, number> = {
      running: 0,
      failing: 0,
      starting: 0,
      paused: 0,
      stopped: 0,
      attention: 0,
    }
    for (const c of containers) {
      out[bucketOf(c)]++
      if (flagged.has(c.id)) out.attention++
    }
    return out
  }, [containers, bucketOf, flagged])

  const stacks = useMemo(() => {
    const out = new Map<string, number>()
    for (const c of containers) {
      const key = c.composeStack || STANDALONE
      out.set(key, (out.get(key) ?? 0) + 1)
    }
    // Projects by name, the containers no project owns last.
    return [...out.entries()].sort(([a], [b]) =>
      a === STANDALONE ? 1 : b === STANDALONE ? -1 : a.localeCompare(b),
    )
  }, [containers])
  const stackFilter = stacks.some(([name]) => name === stack) ? stack : ""

  const matching = useMemo(() => {
    const needle = query.trim().toLowerCase()
    const rows = containers.filter((c) => {
      if (filter === "attention" ? !flagged.has(c.id) : filter && bucketOf(c) !== filter)
        return false
      if (stackFilter && (c.composeStack || STANDALONE) !== stackFilter) return false
      if (!needle) return true
      return (
        c.name.toLowerCase().includes(needle) ||
        c.image.toLowerCase().includes(needle) ||
        c.composeStack?.toLowerCase().includes(needle) === true
      )
    })
    return sortContainers(rows, sort, stats, bucketOf)
  }, [containers, query, filter, stackFilter, flagged, bucketOf, sort, stats])

  const inspection = useInspectionOrder(
    matching,
    (c) => c.id,
    JSON.stringify([query, filter, stackFilter, sort]),
  )
  const visible = inspection.rows
  // A filter change is a new list rather than arrivals into this one.
  const listKey = [query, filter, stackFilter].join("\u0000")

  const toggleFilter = (next: StateFilter) => setFilter(filter === next ? "" : next)
  const toggleStack = (next: string) => setStack(stackFilter === next ? "" : next)
  const chooseSort = (key: SortKey) =>
    setSort(
      sort.key === key
        ? { key, dir: sort.dir === "asc" ? "desc" : "asc" }
        : { key, dir: FIRST_DIR[key] },
    )
  const clearFilters = () => {
    setQuery("")
    setFilter("")
    setStack("")
  }

  const narrowed = query.trim().length > 0 || filter !== "" || stackFilter !== ""
  const running = containers.filter((c) => c.state === "running")
  const checked = running.filter((c) => c.health === "healthy").length
  const projects = stacks.filter(([name]) => name !== STANDALONE).length
  const version = info.data?.ServerVersion
  const loaded = stream.state === "open" || containers.length > 0

  return (
    <Workspace
      name="Docker"
      refresh={() => {
        health.refresh()
        trends.refresh()
        void get<Container[]>("/docker/containers/")
          .then(setContainers)
          .catch(() => setSocketError("Could not refresh containers"))
      }}
      escape={() => {
        if (query) {
          setQuery("")
          return true
        }
        // The stack narrows within a state, so it lets go first.
        if (stackFilter) {
          setStack("")
          return true
        }
        if (filter) {
          setFilter("")
          return true
        }
        return false
      }}
      commands={[
        {
          id: "failing",
          label: filter === "failing" ? "Show every container" : "Show failing containers",
          run: () => toggleFilter("failing"),
        },
      ]}
    >
      <Page className="animate-rise" {...inspection.bindings}>
        {/* Containers are deployed from the Deploy pages — there is no
            standalone create flow here. */}
        <PageContext eyebrow="Docker" title="Containers" />

        <HostIdentity
          mark="docker"
          title={version ? `Docker Engine ${version}` : "Docker Engine"}
          facts={
            <>
              {host && (
                <>
                  <HostFact product={platformProduct(host.platform)}>
                    {host.hostname}
                    {host.platform && ` · ${platformName(host)}`}
                  </HostFact>
                  <FactDot />
                </>
              )}
              {info.data?.Driver && (
                <>
                  <span>{info.data.Driver}</span>
                  <FactDot />
                </>
              )}
              <span className="numeric">
                {running.length} of {plural(containers.length, "container")} running
              </span>
              {projects > 0 && (
                <>
                  <FactDot />
                  <span className="numeric">{plural(projects, "stack")}</span>
                </>
              )}
              {info.data && (
                <>
                  <FactDot />
                  <span className="numeric">{plural(info.data.Images, "image")}</span>
                </>
              )}
              {running.length > 0 && (
                <>
                  <FactDot />
                  <span
                    className="numeric"
                    title={`${plural(running.length - checked, "running container")} not vouched for by a passing health check`}
                  >
                    {checked} of {running.length} pass a health check
                  </span>
                </>
              )}
            </>
          }
          aside={
            <div className="flex flex-wrap items-center gap-3">
              {counts.failing > 0 ? (
                <button
                  type="button"
                  aria-pressed={filter === "failing"}
                  onClick={() => toggleFilter("failing")}
                  className="rounded-md px-1.5 py-1 focus-ring transition-colors hover:bg-row-hover"
                >
                  <Status tone="danger" label={`${plural(counts.failing, "container")} failing`} />
                </button>
              ) : counts.starting > 0 ? (
                <Status tone="warning" label={`${plural(counts.starting, "container")} starting`} />
              ) : containers.length > 0 ? (
                <Status tone="running" label="Nothing failing" />
              ) : null}
              <WorkspaceHelp compact />
            </div>
          }
        />

        <ContainerBand
          containers={containers}
          stats={stats}
          snapshot={snapshot}
          entries={entries}
          listening={eventStream.state === "open"}
          now={now}
          onOpen={open}
        />

        {socketError && <ErrorState error={new Error(socketError)} />}

        {/* Framed, because it is a table: the grid owns a scroll region and
            the edge is what says so (§2). Everything above it stays plain. */}
        <Panel>
          <PanelHeader
            title={
              <span className="inline-flex items-center gap-1.5">
                Containers
                <span className="numeric text-body font-normal text-muted-foreground">
                  {containers.length}
                </span>
                <ExplainIcon name="container" />
              </span>
            }
            // Until the chips fit beside it, Recent's head says Live for both.
            actions={
              <span className="hidden xl:inline-flex">
                <StreamState connection={stream.state} />
              </span>
            }
          >
            <ChipStrip aria-label="State" className="mr-auto">
              {STATES.map(({ value, label, tone }) => {
                const count = counts[value]
                if (value !== "running" && count === 0 && filter !== value) return null
                return (
                  <FilterChip
                    key={value}
                    selected={filter === value}
                    onClick={() => toggleFilter(value)}
                    className={cn(value === "attention" && "text-warning hover:text-warning")}
                  >
                    <span aria-hidden className={cn("size-1.5 rounded-full", DOT[value])} />
                    {label}
                    <ChipCount
                      className={cn(
                        tone === "danger" && "text-destructive opacity-100",
                        tone === "warning" && "text-warning opacity-100",
                      )}
                    >
                      {count}
                    </ChipCount>
                  </FilterChip>
                )
              })}
            </ChipStrip>
          </PanelHeader>
          <PanelToolbar>
            <SearchInput
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Name, image or stack"
              containerClassName="sm:w-64"
            />
            {projects > 0 && (
              <ChipStrip aria-label="Stack">
                {stacks.map(([name, count]) => (
                  <FilterChip
                    key={name}
                    selected={stackFilter === name}
                    title={name === STANDALONE ? "Containers no compose project owns" : undefined}
                    onClick={() => toggleStack(name)}
                  >
                    {name !== STANDALONE && (
                      <span
                        aria-hidden
                        className="size-1.5 rounded-full"
                        style={{ background: hueFor(name, LANES) }}
                      />
                    )}
                    {name === STANDALONE ? "Standalone" : name}
                    <ChipCount>{count}</ChipCount>
                  </FilterChip>
                ))}
              </ChipStrip>
            )}
            {narrowed && (
              <FilterChip
                selected
                className="ml-auto"
                aria-label="Clear every filter"
                onClick={clearFilters}
              >
                Clear
                <Cross aria-hidden className="size-3" />
              </FilterChip>
            )}
          </PanelToolbar>

          <PanelBody flush>
            {visible.length === 0 ? (
              loaded && (
                <EmptyState
                  icon={narrowed ? Warning : Box}
                  title={narrowed ? "Nothing matches those filters" : "Nothing running yet"}
                  description={
                    narrowed
                      ? "Clear the filter, or look under a different state."
                      : "A container is one application, packaged with everything it needs. Everything here is deployed from the Deploy pages."
                  }
                  className="my-4"
                  action={
                    narrowed ? (
                      <Button size="sm" variant="outline" onClick={clearFilters}>
                        Clear filters
                      </Button>
                    ) : (
                      can("service.control") && (
                        <Button size="sm" asChild>
                          <Link href="/deploy">Open Deploy</Link>
                        </Button>
                      )
                    )
                  }
                />
              )
            ) : (
              <ContainerRows
                key={listKey}
                rows={visible}
                sort={sort}
                onSort={chooseSort}
                stats={stats}
                rates={rates}
                trends={trendByName}
                diagnosis={health.data}
                oomKilled={oomKilled}
                pending={pending}
                confirm={confirm}
                act={act}
                onOpen={open}
                onChanged={health.refresh}
                onStack={(name) => toggleStack(name || STANDALONE)}
              />
            )}
          </PanelBody>
          <PanelFooter className="text-hint text-muted-foreground">
            <span className="numeric">{plural(visible.length, "container")} shown</span>
            <span className="text-muted-foreground/40">·</span>
            <span>{SORT_WORDS[sort.key]}</span>
            {sort.key !== "state" && sort.dir !== FIRST_DIR[sort.key] && <span>, reversed</span>}
            <span className="text-muted-foreground/40">·</span>
            <span>readings every 2 seconds</span>
          </PanelFooter>
        </Panel>

        {/* Under the table rather than over it: each row already counts what
            was found about it, and this is the reasoning behind the counts. */}
        {(health.data?.attention.total ?? 0) > 0 && (
          <AttentionPanel diagnosis={health.data} onAction={runFix} onRescan={health.refresh} />
        )}

        {dialog}
      </Page>
    </Workspace>
  )
}
