"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { Box } from "@/components/icons"
import { dedupeEvents } from "@/lib/docker-events"
import { bytes, percent, plural } from "@/lib/format"
import { hueFor, LANES } from "@/lib/hue"
import type { Container, ContainerStats, DockerEvent } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { SearchInput } from "@/components/page"
import { ProductLogo, containerProduct } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { LoadingRows } from "@/components/state"
import { useInspectionOrder } from "@/components/procs/inspection-order"
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
 * The processes and the services have the same column (`procs/process-rail`,
 * which this copies); the rows hold their order while the pointer or focus is
 * in the column (`useInspectionOrder`), so a container that fails under the
 * pointer does not move the row about to be pressed.
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
  const here = (row: Container) => row.id === current || row.name === current
  const listed = rows.some(here)
  const pinned = !listed && currentRow ? currentRow : undefined
  const list = useRef<HTMLOListElement>(null)
  // The container on screen is kept in view: opened from a link or by Alt+↓
  // past the column's edge, it would otherwise be off its own list.
  useEffect(() => {
    const row = list.current?.querySelector<HTMLElement>("[aria-current='page']")
    row?.scrollIntoView({ block: "nearest" })
  }, [current, rows.length])

  return (
    <nav
      aria-label="Containers"
      data-slot="container-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="Name, image or stack"
          aria-label="Search the containers"
          containerClassName="sm:w-full"
        />
        <p className="flex min-w-0 items-center justify-between gap-2 text-hint text-muted-foreground">
          <span className="truncate">{caption}</span>
          {filtered && (
            <button
              type="button"
              onClick={onClearFilters}
              className="shrink-0 rounded-sm font-medium focus-ring hover:text-foreground"
            >
              Clear filters
            </button>
          )}
        </p>
      </div>
      <ol
        ref={list}
        aria-label="Containers to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
        {...bindings}
      >
        {pinned && (
          <RailRow
            row={pinned}
            current
            stat={stats[pinned.id]}
            oom={oomKilled.has(pinned.id)}
            onOpen={onOpen}
          />
        )}
        {pinned && rows.length > 0 && (
          <li aria-hidden className="mx-3 my-1.5 border-t border-hairline" />
        )}
        {rows.map((row) => (
          <RailRow
            key={row.id}
            row={row}
            current={here(row)}
            stat={stats[row.id]}
            oom={oomKilled.has(row.id)}
            onOpen={onOpen}
          />
        ))}
        {loading && rows.length === 0 && !pinned && <LoadingRows rows={8} />}
        {!loading && rows.length === 0 && !pinned && (
          <li className="px-3 py-4 text-hint text-muted-foreground">No container matches.</li>
        )}
      </ol>
    </nav>
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
    <li className="min-w-0">
      <button
        type="button"
        data-workspace-item={row.id}
        data-workspace-name={row.name}
        aria-current={current ? "page" : undefined}
        aria-label={row.name}
        title={row.image}
        onClick={() => onOpen(row.id)}
        className={cn(
          "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
          current ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        <span className="relative flex shrink-0">
          <ProductLogo
            id={containerProduct(row)}
            size="sm"
            fallback={Box}
            className="size-7 rounded-md [&_img]:size-4"
          />
          <span
            aria-hidden
            className="absolute -right-0.5 -bottom-0.5 flex size-2.5 items-center justify-center rounded-full bg-background"
          >
            <StatusDot tone={bucketTone(containerBucket(row, oom))} className="size-1.5" />
          </span>
        </span>
        <span className="min-w-0 flex-1">
          <span
            className={cn("block truncate text-body", current ? "font-semibold" : "font-medium")}
          >
            {row.name}
          </span>
          <span
            className={cn("block truncate text-hint", line.tone || "text-muted-foreground")}
            style={line.color ? { color: line.color } : undefined}
          >
            {line.text}
          </span>
        </span>
        {running && (
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
        )}
      </button>
    </li>
  )
}
