"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { Layers } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, percent, plural } from "@/lib/format"
import type { ComposeStack, Container, ContainerStats } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { SearchInput } from "@/components/page"
import { ProductLogo } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { LoadingRows } from "@/components/state"
import { useInspectionOrder } from "@/components/procs/inspection-order"
import {
  STACK_FILTER_KEYS,
  stackLines,
  visibleStacks,
  type StackBucket,
  type StackLine,
} from "@/components/docker/stack-readings"
import { stackProducts, stateWords } from "@/components/docker/stack-row"
import { stackLabel, stackTone } from "@/components/docker/stack-state"

const BUCKETS = new Set<string>(["running", "stopped", "undeployed"])

const BUCKET_WORDS: Record<StackBucket, string> = {
  running: "running",
  stopped: "stopped",
  undeployed: "not deployed",
}

/**
 * The Stacks list, read for the rail beside one stack: the search and the
 * state chip the list was left on (they are its session's), the stacks at the
 * list's cadence and their sums from the containers socket.
 */
export function useStackRail() {
  const [query, setQuery] = useSessionState(STACK_FILTER_KEYS.query, "")
  const [remembered, setState] = useSessionState(STACK_FILTER_KEYS.state, "")
  const state = (BUCKETS.has(remembered) ? remembered : "") as StackBucket | ""
  const list = usePoll(
    (signal) => get<ComposeStack[]>("/docker/stacks/", undefined, signal),
    15_000,
  )
  const [containers, setContainers] = useState<Container[]>([])
  const [stats, setStats] = useState<Record<string, ContainerStats>>({})
  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type === "containers") setContainers(envelope.data as Container[])
    else if (envelope.type === "stats") {
      const rows = envelope.data as ContainerStats[]
      setStats(Object.fromEntries(rows.map((r) => [r.id, r])))
    }
  }, [])
  useSocket("/docker/containers/stream", { onMessage })

  const lines = useMemo(
    () => stackLines(list.data ?? [], containers, stats),
    [list.data, containers, stats],
  )
  const matching = useMemo(
    () => visibleStacks(lines, { query, state }).map((entry) => entry.line),
    [lines, query, state],
  )
  const inspection = useInspectionOrder(
    matching,
    (line) => line.stack.name,
    JSON.stringify([query, state]),
  )
  const caption = !list.data
    ? list.error
      ? "The stacks could not be read"
      : "Reading the stacks…"
    : state || query.trim()
      ? `${matching.length} of ${lines.length}${state ? ` · ${BUCKET_WORDS[state]}` : ""}`
      : `${plural(lines.length, "stack")} · running first`
  return {
    all: lines,
    rows: inspection.rows,
    bindings: inspection.bindings,
    loading: list.loading && !list.data,
    refresh: list.refresh,
    query,
    setQuery,
    filtered: state !== "",
    clearFilters: () => setState(""),
    caption,
  }
}

/**
 * The Stacks list, folded into a column beside the stack it opened: its
 * search, its state chip and its order, so the next stack is one press away.
 * Each row is the stack as the product it mostly is with its state as a dot
 * in the logo's corner, its name, how much of it is up and, while it runs,
 * what its containers take together; the one on screen is filled.
 *
 * The same column the processes, the services and a container have
 * (`procs/service-rail`, which this copies).
 */
export function StackRail({
  rows,
  current,
  currentRow,
  loading,
  query,
  onQuery,
  filtered,
  onClearFilters,
  caption,
  onOpen,
  bindings,
  className,
}: {
  rows: StackLine[]
  current: string
  /** The stack on screen, drawn first when the list does not hold it. */
  currentRow?: StackLine
  loading: boolean
  query: string
  onQuery: (value: string) => void
  filtered: boolean
  onClearFilters: () => void
  caption: string
  onOpen: (name: string) => void
  bindings: React.HTMLAttributes<HTMLElement>
  className?: string
}) {
  const listed = rows.some((line) => line.stack.name === current)
  const pinned = !listed && currentRow ? currentRow : undefined
  const list = useRef<HTMLOListElement>(null)
  // The stack on screen is kept in view: opened from a link or by Alt+↓ past
  // the column's edge, it would otherwise be off its own list.
  useEffect(() => {
    const row = list.current?.querySelector<HTMLElement>("[aria-current='page']")
    row?.scrollIntoView({ block: "nearest" })
  }, [current, rows.length])

  return (
    <nav
      aria-label="Stacks"
      data-slot="stack-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="Stack, service or image"
          aria-label="Search the stacks"
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
        aria-label="Stacks to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
        {...bindings}
      >
        {pinned && <RailRow line={pinned} current onOpen={onOpen} />}
        {pinned && rows.length > 0 && (
          <li aria-hidden className="mx-3 my-1.5 border-t border-hairline" />
        )}
        {rows.map((line) => (
          <RailRow
            key={line.stack.name}
            line={line}
            current={line.stack.name === current}
            onOpen={onOpen}
          />
        ))}
        {loading && rows.length === 0 && !pinned && <LoadingRows rows={6} />}
        {!loading && rows.length === 0 && !pinned && (
          <li className="px-3 py-4 text-hint text-muted-foreground">No stack matches.</li>
        )}
      </ol>
    </nav>
  )
}

function RailRow({
  line,
  current,
  onOpen,
}: {
  line: StackLine
  current: boolean
  onOpen: (name: string) => void
}) {
  const { stack } = line
  const words = stateWords(line)
  const tone = stackTone(stack.state)
  return (
    <li className="min-w-0">
      <button
        type="button"
        data-workspace-item={`stack:${stack.name}`}
        data-workspace-name={stack.name}
        aria-current={current ? "page" : undefined}
        aria-label={stack.name}
        title={stack.workingDir || undefined}
        onClick={() => onOpen(stack.name)}
        className={cn(
          "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
          current ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        <span className="relative flex shrink-0">
          <ProductLogo
            id={stackProducts(stack)[0]}
            size="sm"
            fallback={Layers}
            className="size-7 rounded-md [&_img]:size-4"
          />
          <span
            aria-hidden
            className="absolute -right-0.5 -bottom-0.5 flex size-2.5 items-center justify-center rounded-full bg-background"
          >
            <StatusDot tone={tone} className="size-1.5" />
          </span>
        </span>
        <span className="min-w-0 flex-1">
          <span
            className={cn("block truncate text-body", current ? "font-semibold" : "font-medium")}
          >
            {stack.name}
          </span>
          <span
            className={cn(
              "numeric block truncate text-hint",
              words.unhealthy > 0
                ? "text-destructive"
                : tone === "warning"
                  ? "text-warning"
                  : "text-muted-foreground",
            )}
          >
            {words.unhealthy > 0
              ? `${words.short} · ${words.unhealthy} unhealthy`
              : stack.state === "running" || !stack.deployed
                ? words.short
                : `${stackLabel(stack.state).toLowerCase()} · ${words.short}`}
          </span>
        </span>
        {stack.running > 0 && line.measured && (
          <span className="flex shrink-0 flex-col items-end">
            <span className="numeric font-mono text-hint text-muted-foreground">
              {percent(line.cpu)}
            </span>
            <span className="numeric font-mono text-micro text-muted-foreground">
              {line.memory ? bytes(line.memory) : "—"}
            </span>
          </span>
        )}
      </button>
    </li>
  )
}
