"use client"

import { useCallback, useMemo, useState } from "react"
import { Box } from "@/components/icons"
import { dedupeEvents } from "@/lib/docker-events"
import { bytes, percent, plural } from "@/lib/format"
import { hueFor, LANES } from "@/lib/hue"
import type { Container, ContainerStats, DockerEvent } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { ProductLogo, containerProduct } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { useInspectionOrder } from "@/components/procs/inspection-order"
import { DockerRail, DockerRailRow } from "@/components/docker/item-rail"
import {
  CONTAINER_FILTER_KEYS,
  DEFAULT_CONTAINER_SORT,
  SORT_WORDS,
  STANDALONE,
  bucketTone,
  containerBucket,
  containerWord,
  exitCodeOf,
  exitWords,
  matchingContainers,
  oomKilledIds,
  recentEntries,
  sortContainers,
  type ContainerBucket,
  type ContainerSort,
} from "@/components/docker/containers"

const BUCKETS = new Set<string>(["running", "failing", "starting", "paused", "stopped"])

/** How much of Docker's event log the rail holds, to tell a kill for memory from a stop. */
const EVENTS_KEPT = 200

/**
 * The Containers table's list, read for the rail beside one container: the
 * search, state chip, stack chip and order the table was left on (they are
 * its session's), fed by the socket the table reads, so a container that
 * fails while its neighbour is being read moves up this list too.
 */
export function useContainerRail() {
  const [query, setQuery] = useSessionState(CONTAINER_FILTER_KEYS.query, "")
  const [remembered, setState] = useSessionState<string>(CONTAINER_FILTER_KEYS.state, "")
  const state = (BUCKETS.has(remembered) ? remembered : "") as ContainerBucket | ""
  const [stack, setStack] = useSessionState<string>(CONTAINER_FILTER_KEYS.stack, "")
  const [sort] = useSessionState<ContainerSort>(CONTAINER_FILTER_KEYS.sort, DEFAULT_CONTAINER_SORT)

  const [containers, setContainers] = useState<Container[]>()
  const [stats, setStats] = useState<Record<string, ContainerStats>>({})
  const [events, setEvents] = useState<DockerEvent[]>([])
  const onContainers = useCallback((envelope: Envelope) => {
    if (envelope.type === "containers") setContainers(envelope.data as Container[])
    else if (envelope.type === "stats") {
      const rows = envelope.data as ContainerStats[]
      setStats(Object.fromEntries(rows.map((r) => [r.id, r])))
    }
  }, [])
  useSocket("/docker/containers/stream", { onMessage: onContainers })
  const onEvents = useCallback((envelope: Envelope) => {
    if (envelope.type !== "events") return
    const batch = envelope.data as DockerEvent[]
    setEvents((previous) => dedupeEvents([...batch, ...previous]).slice(0, EVENTS_KEPT))
  }, [])
  useSocket("/docker/events/stream", { query: { kinds: "container" }, onMessage: onEvents })

  const oomKilled = useMemo(() => oomKilledIds(recentEntries(events)), [events])
  const bucketOf = useCallback(
    (c: Container) => containerBucket(c, oomKilled.has(c.id)),
    [oomKilled],
  )
  const all = useMemo(() => containers ?? [], [containers])
  // A stack that has gone since the chip was pressed is no filter.
  const stackFilter = all.some((c) => (c.composeStack || STANDALONE) === stack) ? stack : ""
  const matching = useMemo(
    () =>
      sortContainers(
        matchingContainers(all, { query, state, stack: stackFilter }, bucketOf),
        sort,
        stats,
        bucketOf,
      ),
    [all, query, state, stackFilter, bucketOf, sort, stats],
  )
  const inspection = useInspectionOrder(
    matching,
    (c) => c.id,
    JSON.stringify([query, state, stackFilter, sort]),
  )

  const narrowed = [state, stackFilter === STANDALONE ? "standalone" : stackFilter].filter(Boolean)
  const caption = !containers
    ? "Reading the containers…"
    : narrowed.length > 0 || query.trim()
      ? `${matching.length} of ${all.length}${narrowed.length > 0 ? ` · ${narrowed.join(" · ")}` : ""}`
      : `${plural(all.length, "container")} · ${SORT_WORDS[sort.key].replace(/, then by name$/, "")}`

  return {
    all,
    rows: inspection.rows,
    bindings: inspection.bindings,
    loading: containers === undefined,
    stats,
    oomKilled,
    query,
    setQuery,
    filtered: narrowed.length > 0,
    clearFilters: () => {
      setState("")
      setStack("")
    },
    caption,
  }
}

/**
 * The Containers table, folded into a column beside the container it opened:
 * its search, its chips and its order, so the next container to check is one
 * press away. Each row is the container as the product it runs with its state
 * as a dot in the logo's corner, its name, its project or how it stopped,
 * and, while it runs, its two readings; the one on screen is filled.
 *
 * The column itself is the one every Docker item's page has (`item-rail.tsx`);
 * this file is where the list comes from, and the container's row. The rows
 * hold their order while the pointer or focus is in the column
 * (`useInspectionOrder`), so a container that fails under the pointer does not
 * move the row about to be pressed.
 */
export function ContainerRail({
  rows,
  current,
  currentRow,
  loading,
  stats,
  oomKilled,
  query,
  onQuery,
  filtered,
  onClearFilters,
  caption,
  onOpen,
  bindings,
  className,
}: {
  rows: Container[]
  /** The id or name in the address: a link may name a container either way. */
  current: string
  /** The container on screen, drawn first when the list does not hold it. */
  currentRow?: Container
  loading: boolean
  stats: Record<string, ContainerStats>
  oomKilled: Set<string>
  query: string
  onQuery: (value: string) => void
  filtered: boolean
  onClearFilters: () => void
  caption: string
  onOpen: (id: string) => void
  bindings: React.HTMLAttributes<HTMLElement>
  className?: string
}) {
  return (
    <DockerRail
      slot="container-rail"
      label="Containers"
      search={{ label: "Search the containers", placeholder: "Name, image or stack" }}
      rows={rows}
      keyOf={(row) => row.id}
      current={(row) => row.id === current || row.name === current}
      pinned={currentRow}
      row={(row, on) => (
        <RailRow
          row={row}
          current={on}
          stat={stats[row.id]}
          oom={oomKilled.has(row.id)}
          onOpen={onOpen}
        />
      )}
      loading={loading}
      empty="No container matches."
      query={query}
      onQuery={onQuery}
      filtered={filtered}
      onClearFilters={onClearFilters}
      caption={caption}
      bindings={bindings}
      className={className}
    />
  )
}

/** How a container is doing, in the rail's few words: whose it is while it runs, why it is not when it does not. */
function railLine(row: Container, oom: boolean): { text: string; color?: string; tone?: string } {
  const bucket = containerBucket(row, oom)
  if (bucket === "running") {
    return row.composeStack
      ? { text: row.composeStack, color: hueFor(row.composeStack, LANES) }
      : { text: row.image }
  }
  const word = containerWord(row, oom).toLowerCase()
  const exit = row.state === "exited" ? exitWords(exitCodeOf(row), oom) : undefined
  return {
    text: exit && bucket === "failing" && !oom ? `${word} · ${exit}` : word,
    tone: bucket === "failing" ? "text-destructive" : bucket === "starting" ? "text-warning" : "",
  }
}

function RailRow({
  row,
  current,
  stat,
  oom,
  onOpen,
}: {
  row: Container
  current: boolean
  stat?: ContainerStats
  oom: boolean
  onOpen: (id: string) => void
}) {
  const line = railLine(row, oom)
  const running = row.state === "running" && stat
  return (
    <DockerRailRow
      item={row.id}
      name={row.name}
      title={row.image}
      current={current}
      onOpen={() => onOpen(row.id)}
      mark={
        <ProductLogo
          id={containerProduct(row)}
          size="sm"
          fallback={Box}
          className="size-7 rounded-md [&_img]:size-4"
        />
      }
      dot={<StatusDot tone={bucketTone(containerBucket(row, oom))} className="size-1.5" />}
      end={
        running && (
          <span className="flex shrink-0 flex-col items-end">
            {/* Plain: a share of one core is not an alarm for a container, which
                may be given several. The table tones it against its limit. */}
            <span className="numeric font-mono text-hint text-muted-foreground">
              {stat.cpuReady === false ? "—" : percent(stat.cpuPercent)}
            </span>
            <span className="numeric font-mono text-micro text-muted-foreground">
              {stat.memUsage ? bytes(stat.memUsage) : "—"}
            </span>
          </span>
        )
      }
    >
      <span className={cn("block truncate text-body", current ? "font-semibold" : "font-medium")}>
        {row.name}
      </span>
      <span
        className={cn("block truncate text-hint", line.tone || "text-muted-foreground")}
        style={line.color ? { color: line.color } : undefined}
      >
        {line.text}
      </span>
    </DockerRailRow>
  )
}
