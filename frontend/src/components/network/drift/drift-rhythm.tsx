"use client"

import { useState } from "react"
import { useNow } from "@/components/deploy/vocabulary"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { ArrowRight } from "@/components/icons"
import { plural, relativeTime } from "@/lib/format"
import {
  driftCounts,
  driftMoves,
  driftReading,
  type DriftMove,
  type DriftRow,
  type DriftStatus,
} from "@/lib/network-drift"
import { cn } from "@/lib/utils"
import { STATUS_FILL } from "@/components/network/drift/marks"

export type Inspection = { at: string; rows: DriftRow[]; moves: DriftMove[] }

/** How much history the strip keeps: an hour of thirty-second inspections. */
const KEEP = 120

/**
 * Every inspection this page has seen, oldest first, each with what moved
 * since the one before it. Kept as state and extended during render when a
 * new inspection lands — `useArrivals`' pattern — so a re-render for any
 * other reason adds nothing, and a poll that returned the same inspection
 * (the same `checkedAt`) is not a second one.
 */
export function useDriftHistory(at: string, rows: DriftRow[]) {
  const [history, setHistory] = useState<Inspection[]>(() => [{ at, rows, moves: [] }])
  const last = history[history.length - 1]
  if (last.at !== at) {
    const next = [...history.slice(1 - KEEP), { at, rows, moves: driftMoves(last.rows, rows, at) }]
    setHistory(next)
    return next
  }
  return history
}

/** The strip's window: fifteen minutes ending at this second. */
const WINDOW = 15 * 60_000

/**
 * The rhythm of the inspections since this page opened. Each one is a bar
 * on an axis that ends at this second, split into its comparisons' states —
 * green what matched, grey what could not be compared, amber what differed,
 * red what another owner holds — and it slides left as the clock runs, so a
 * page left open reads as a heartbeat, and a beat where something changed
 * stands out of the run of others. The countdown under it is the next
 * inspection the page will take.
 *
 * Under the strip is what moved: each comparison whose state at one
 * inspection is not what it was at the one before, newest first. A host that
 * has not drifted says how many inspections it has stood through; nothing is
 * invented to fill the space.
 */
export function DriftRhythm({
  history,
  nextAt,
  inspecting,
}: {
  history: Inspection[]
  nextAt?: number
  inspecting: boolean
}) {
  const now = useNow(1000)
  const shown = history.filter((entry) => now - new Date(entry.at).getTime() <= WINDOW)
  const moves = history.flatMap((entry) => entry.moves).reverse()
  const left = nextAt === undefined ? undefined : Math.max(0, Math.ceil((nextAt - now) / 1000))
  return (
    <Panel plain aria-label="Since this page opened">
      <PanelHeader
        title="Since this page opened"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {plural(history.length, "inspection")}
          </span>
        }
      />
      <PanelBody className="space-y-5">
        <div>
          <div
            role="img"
            aria-label={`${plural(history.length, "inspection")}; the latest found ${beatWords(history[history.length - 1])}`}
            className="relative h-10 overflow-hidden rounded-md bg-meter-track/40"
          >
            {[1, 2].map((third) => (
              <span
                key={third}
                aria-hidden
                className="absolute inset-y-0 w-px bg-hairline"
                style={{ left: `${(third / 3) * 100}%` }}
              />
            ))}
            {shown.map((entry, index) => (
              <Beat
                key={entry.at}
                entry={entry}
                at={1 - (now - new Date(entry.at).getTime()) / WINDOW}
                fresh={index === shown.length - 1}
              />
            ))}
          </div>
          <div className="mt-1.5 flex items-center justify-between gap-3 text-micro text-muted-foreground">
            <span>15 min ago</span>
            <span className="numeric">
              {inspecting ? (
                <TextShimmer>Inspecting now</TextShimmer>
              ) : left !== undefined ? (
                `next inspection in ${left}s`
              ) : (
                "now"
              )}
            </span>
          </div>
        </div>
        {moves.length > 0 ? (
          <ul aria-label="What moved" className="space-y-2.5">
            {moves.slice(0, 6).map((move) => (
              <MoveRow key={`${move.at}:${move.id}`} move={move} />
            ))}
            {moves.length > 6 && (
              <li className="text-hint text-muted-foreground">
                and {plural(moves.length - 6, "earlier move")}
              </li>
            )}
          </ul>
        ) : (
          <p className="text-body text-muted-foreground">
            {history.length === 1
              ? "Each inspection lands here as a beat. Anything that changes between two of them is listed under it."
              : `${plural(history.length, "inspection")}, and nothing has moved between them.`}
          </p>
        )}
      </PanelBody>
    </Panel>
  )
}

function beatWords(entry: Inspection) {
  const counts = driftCounts(entry.rows)
  return counts.differences
    ? plural(counts.differences, "difference")
    : counts.unknown
      ? `${counts.unknown} incomplete`
      : "everything matching"
}

// The order a beat is stacked in, from the ground up: what is fine at the
// foot, what needs a reader at the top where the eye lands.
const STACK: DriftStatus[] = ["matching", "unknown", "drift", "conflict"]

function Beat({ entry, at, fresh }: { entry: Inspection; at: number; fresh: boolean }) {
  const compared = entry.rows.filter((row) => row.status !== "not_required")
  const counts = driftCounts(compared)
  const share: Record<string, number> = {
    matching: counts.matching,
    unknown: counts.unknown,
    drift: counts.differences - counts.conflicts,
    conflict: counts.conflicts,
  }
  const total = Math.max(1, compared.length)
  return (
    <span
      title={`${new Date(entry.at).toLocaleTimeString()} · ${beatWords(entry)}`}
      className={cn(
        "absolute inset-y-1 flex w-1.5 -translate-x-full flex-col-reverse overflow-hidden rounded-[2px] transition-[left] duration-1000 ease-linear",
        fresh && "animate-rise",
        entry.moves.length > 0 && "ring-1 ring-foreground/60",
      )}
      style={{ left: `${Math.max(0, at) * 100}%` }}
    >
      {STACK.map((status) =>
        share[status] ? (
          <span
            key={status}
            className={STATUS_FILL[status]}
            style={{ height: `${(share[status] / total) * 100}%` }}
          />
        ) : null,
      )}
    </span>
  )
}

function MoveRow({ move }: { move: DriftMove }) {
  return (
    <li className="flex min-w-0 animate-rise items-center gap-2.5 text-body">
      {move.product ? (
        <ProductGlyph id={move.product} className="size-4" />
      ) : (
        <span aria-hidden className="size-4 shrink-0" />
      )}
      <span className="min-w-0 flex-1 truncate font-mono text-hint">{move.label}</span>
      <span className="flex shrink-0 items-center gap-1.5 text-hint">
        <StateWord status={move.from} fallback="new" />
        <ArrowRight aria-hidden className="size-3 text-muted-foreground" />
        <StateWord status={move.to} fallback="gone" />
      </span>
      <time
        dateTime={move.at}
        className="numeric w-16 shrink-0 text-right text-micro text-muted-foreground"
      >
        {relativeTime(move.at)}
      </time>
    </li>
  )
}

function StateWord({ status, fallback }: { status?: DriftStatus; fallback: string }) {
  if (!status) return <span className="text-muted-foreground">{fallback}</span>
  const reading = driftReading(status)
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1",
        reading.tone === "warning" && "text-warning",
        reading.tone === "danger" && "text-destructive",
        reading.tone === "running" && "text-success",
        (reading.tone === "unknown" || reading.tone === "stopped") && "text-muted-foreground",
      )}
    >
      <span aria-hidden className={cn("size-1.5 rounded-full", STATUS_FILL[status])} />
      {reading.label}
    </span>
  )
}
