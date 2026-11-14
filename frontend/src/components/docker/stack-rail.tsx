"use client"

import { useCallback, useMemo, useState } from "react"
import { Layers } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, percent, plural } from "@/lib/format"
import type { ComposeStack, Container, ContainerStats } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { ProductLogo } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { useInspectionOrder } from "@/components/procs/inspection-order"
import { DockerRail, DockerRailRow } from "@/components/docker/item-rail"
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
 * The column itself is the one every Docker item's page has (`item-rail.tsx`);
 * this file is where the list comes from, and the stack's row.
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
  return (
    <DockerRail
      slot="stack-rail"
      label="Stacks"
      search={{ label: "Search the stacks", placeholder: "Stack, service or image" }}
      rows={rows}
      keyOf={(line) => line.stack.name}
      current={(line) => line.stack.name === current}
      pinned={currentRow}
      row={(line, on) => <RailRow line={line} current={on} onOpen={onOpen} />}
      loading={loading}
      loadingRows={6}
      empty="No stack matches."
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
    <DockerRailRow
      item={`stack:${stack.name}`}
      name={stack.name}
      title={stack.workingDir || undefined}
      current={current}
      onOpen={() => onOpen(stack.name)}
      mark={
        <ProductLogo
          id={stackProducts(stack)[0]}
          size="sm"
          fallback={Layers}
          className="size-7 rounded-md [&_img]:size-4"
        />
      }
      dot={<StatusDot tone={tone} className="size-1.5" />}
      end={
        stack.running > 0 &&
        line.measured && (
          <span className="flex shrink-0 flex-col items-end">
            <span className="numeric font-mono text-hint text-muted-foreground">
              {percent(line.cpu)}
            </span>
            <span className="numeric font-mono text-micro text-muted-foreground">
              {line.memory ? bytes(line.memory) : "—"}
            </span>
          </span>
        )
      }
    >
      <span className={cn("block truncate text-body", current ? "font-semibold" : "font-medium")}>
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
    </DockerRailRow>
  )
}
