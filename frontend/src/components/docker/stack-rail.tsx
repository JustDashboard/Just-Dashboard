"use client"

import { useCallback, useMemo, useState } from "react"
import { Layers } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, percent, plural } from "@/lib/format"
import type { ComposeStack, Container, ContainerStats } from "@/lib/types"
import { useSearchParams } from "next/navigation"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { ProductLogo } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { useInspectionOrder } from "@/components/procs/inspection-order"
import { Rail, RailFigures, RailLine, RailName, RailRow, railInk } from "@/components/rail"
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
 * The stacks, at the list's cadence. Shared by its key, so the rail reads the
 * same answer, and the same failure, as the hook that orders it, with one
 * request between them.
 */
function useStacks() {
  return usePoll(
    (signal) => get<ComposeStack[]>("/docker/stacks/", undefined, signal),
    15_000,
    [],
    { key: "/docker/stacks/" },
  )
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
  const list = useStacks()
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
  // A list that failed says so in the column, not in its caption.
  const caption = !list.data
    ? list.error
      ? undefined
      : "Reading the stacks…"
    : state || query.trim()
      ? `${matching.length} of ${lines.length}${state ? ` · ${BUCKET_WORDS[state]}` : ""}`
      : `${plural(lines.length, "stack")} · running first`
  return {
    all: lines,
    rows: inspection.rows,
    bindings: inspection.bindings,
    loading: list.loading && !list.data,
    error: list.data ? undefined : list.error,
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
 * The column is the shared `Rail`; this file is where the list comes from,
 * and the stack's row. A list that could not be read says so in place of the
 * rows, with a way to ask again, rather than "No stack matches."
 */
export function StackRail({
  rows,
  current,
  currentRow,
  loading,
  error,
  onRetry,
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
  /** The list's failure; read from the shared list when the page does not pass it. */
  error?: Error
  onRetry?: () => void
  query: string
  onQuery: (value: string) => void
  filtered: boolean
  onClearFilters: () => void
  caption?: string
  onOpen?: (name: string) => void
  bindings: React.HTMLAttributes<HTMLElement>
  className?: string
}) {
  const list = useStacks()
  const tab = useSearchParams().get("tab")
  return (
    <Rail
      slot="stack-rail"
      label="Stacks"
      search={{ placeholder: "Stack, service or image", query, onQuery }}
      rows={rows}
      keyOf={(line) => line.stack.name}
      current={(line) => line.stack.name === current}
      pinned={currentRow}
      row={(line, on) => (
        <StackRow line={line} current={on} href={stackHref(line.stack.name, tab)} onOpen={onOpen} />
      )}
      loading={loading}
      error={error ?? (list.data ? undefined : list.error)}
      onRetry={onRetry ?? list.refresh}
      empty="No stack matches."
      filtered={filtered}
      onClearFilters={onClearFilters}
      caption={caption}
      bindings={bindings}
      className={className}
    />
  )
}

/** A stack's page, on the view the one on screen is showing. */
function stackHref(name: string, tab: string | null) {
  const path = `/docker/stacks/${encodeURIComponent(name)}`
  return tab ? `${path}?tab=${encodeURIComponent(tab)}` : path
}

function StackRow({
  line,
  current,
  href,
  onOpen,
}: {
  line: StackLine
  current: boolean
  href: string
  onOpen?: (name: string) => void
}) {
  const { stack } = line
  const words = stateWords(line)
  const tone = stackTone(stack.state)
  return (
    <RailRow
      item={`stack:${stack.name}`}
      name={stack.name}
      title={stack.workingDir || undefined}
      current={current}
      href={href}
      onOpen={onOpen && (() => onOpen(stack.name))}
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
          <RailFigures top={percent(line.cpu)} bottom={line.memory ? bytes(line.memory) : "—"} />
        )
      }
    >
      <RailName current={current}>{stack.name}</RailName>
      <RailLine className="numeric" ink={railInk(words.unhealthy > 0 ? "danger" : tone)}>
        {words.unhealthy > 0
          ? `${words.short} · ${words.unhealthy} unhealthy`
          : stack.state === "running" || !stack.deployed
            ? words.short
            : `${stackLabel(stack.state).toLowerCase()} · ${words.short}`}
      </RailLine>
    </RailRow>
  )
}
