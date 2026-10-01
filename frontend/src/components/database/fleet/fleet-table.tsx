"use client"

import Link from "next/link"
import { ChevronDown, ChevronUp } from "@/components/icons"
import { bytes, relativeTime } from "@/lib/format"
import type { DbFleetEntry } from "@/lib/types"
import { useMediaQuery } from "@/hooks/use-mobile"
import { cn } from "@/lib/utils"
import { DimActions } from "@/components/icon-action"
import { Panel } from "@/components/panel"
import { ProductGlyphs } from "@/components/product-logo"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu } from "@/components/verbs"
import { sectionHref, type Engine } from "@/components/database/engine"
import {
  reachWord,
  whereWord,
  type BackupReading,
  type Concern,
  type FleetSort,
  type FleetSortKey,
} from "@/components/database/fleet/fleet"
import type { FleetControl } from "@/components/database/fleet/use-fleet-control"
import { EngineGlyph, EnvironmentTag, ProtectedTag } from "@/components/database/kit"
import { DatabaseStatusMark, fleetStatus } from "@/components/database/shell/status"

/**
 * The fleet as a table: the same databases as the cards, laid across so their
 * figures can be compared down a column and sorted by one.
 *
 * The cards are the page's default and the way in; this is the reading for a
 * server with many databases, where "which is largest" and "which was never
 * dumped" are questions about a column. A row is therefore a row of readings
 * — its name is the link, the rest of it is not a press target — and it keeps
 * its frame for the reason every table does: the grid owns a scroll region.
 */
export function FleetTable({
  entries,
  engineOf,
  concernsOf,
  backups,
  feeds,
  control,
  sort,
  onSort,
}: {
  entries: DbFleetEntry[]
  engineOf: (entry: DbFleetEntry) => Engine
  concernsOf: (entry: DbFleetEntry) => Concern[]
  backups: Map<number, BackupReading>
  feeds: Map<number, { products: string[]; count: number }>
  control: FleetControl
  sort: FleetSort
  onSort: (key: FleetSortKey) => void
}) {
  // Eleven columns need the widest shell. Below it the two that the name's
  // own mark and the cards' shelves already say — the engine, where it runs —
  // step out, so the figures being compared stay on screen (§12).
  const wide = useMediaQuery("(min-width: 1536px)")
  return (
    <Panel className="min-w-0">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Status</TableHead>
            <SortHead label="Name" column="name" sort={sort} onSort={onSort} />
            {wide && <TableHead>Engine</TableHead>}
            {wide && <TableHead>Where</TableHead>}
            <SortHead label="Size" column="size" sort={sort} onSort={onSort} right />
            <SortHead label="Objects" column="objects" sort={sort} onSort={onSort} right />
            <SortHead label="Sessions" column="sessions" sort={sort} onSort={onSort} right />
            <TableHead>Feeds</TableHead>
            <SortHead label="Last backup" column="backup" sort={sort} onSort={onSort} />
            <TableHead>Reachable from</TableHead>
            <TableHead>
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {entries.map((entry) => {
            const engine = engineOf(entry)
            const concerns = concernsOf(entry)
            const has = (kind: Concern["kind"]) => concerns.some((one) => one.kind === kind)
            const where = whereWord(entry)
            const backup = backups.get(entry.id)
            const fed = feeds.get(entry.id)
            const busy = control.busyWord(entry)
            const down = entry.state === "stopped" || entry.state === "paused"
            const quiet = !entry.ok
            return (
              <TableRow key={entry.id} className="group" data-state-of={entry.state}>
                <TableCell>
                  {busy ? (
                    <TextShimmer className="text-xs font-medium">{`${busy}…`}</TextShimmer>
                  ) : (
                    <DatabaseStatusMark status={fleetStatus(entry)} />
                  )}
                </TableCell>
                <TableCell className="max-w-64">
                  <span className="flex min-w-0 items-center gap-2">
                    <span
                      className="flex shrink-0"
                      title={`${engine.label}${entry.versionNumber ? ` ${entry.versionNumber}` : ""} · ${where.text}`}
                    >
                      <EngineGlyph engine={engine} className={cn(down && "opacity-60")} />
                    </span>
                    <Link
                      href={sectionHref(entry.id)}
                      aria-label={`Open ${entry.name}`}
                      className="max-w-full min-w-0 truncate rounded-sm text-body font-medium focus-ring hover:underline"
                    >
                      {entry.name}
                    </Link>
                    <EnvironmentTag environment={entry.environment} />
                    {entry.readOnly && <ProtectedTag />}
                  </span>
                </TableCell>
                {wide && (
                  <TableCell className="text-muted-foreground">
                    {engine.label}
                    {entry.versionNumber ? ` ${entry.versionNumber}` : ""}
                  </TableCell>
                )}
                {wide && (
                  <TableCell className="max-w-56">
                    <span
                      title={where.text}
                      className={cn(
                        "block truncate text-muted-foreground",
                        where.mono && "font-mono",
                      )}
                    >
                      {where.text}
                    </span>
                  </TableCell>
                )}
                <TableCell className="numeric text-right">
                  {entry.sizesKnown ? bytes(entry.bytes) : "—"}
                </TableCell>
                <TableCell className="numeric text-right">
                  {quiet ? (
                    "—"
                  ) : (
                    <>
                      {entry.objects.toLocaleString()}{" "}
                      <span className="text-muted-foreground">{entry.objectWord}</span>
                    </>
                  )}
                </TableCell>
                <TableCell className="numeric text-right">
                  {quiet ? "—" : entry.sessions.toLocaleString()}
                </TableCell>
                <TableCell>
                  {fed && fed.count > 0 ? (
                    <span className="flex items-center gap-1.5">
                      <ProductGlyphs ids={fed.products} max={3} />
                      <span className="numeric text-muted-foreground">{fed.count}</span>
                    </span>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TableCell>
                <TableCell
                  className={cn(
                    "text-muted-foreground",
                    (has("never-backed-up") || has("stale-backup")) && "text-warning",
                  )}
                >
                  {backup?.newest
                    ? relativeTime(backup.newest)
                    : has("never-backed-up")
                      ? "never"
                      : "—"}
                </TableCell>
                <TableCell className={cn("text-muted-foreground", has("public") && "text-warning")}>
                  {reachWord(entry.exposure)}
                </TableCell>
                <TableCell className="w-10 py-0 text-right">
                  <DimActions className="justify-end">
                    <VerbMenu verbs={control.verbsFor(entry)} label={`Actions for ${entry.name}`} />
                  </DimActions>
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </Panel>
  )
}

/** A column heading that sorts by its column, saying so to a screen reader. */
function SortHead({
  label,
  column,
  sort,
  onSort,
  right,
}: {
  label: string
  column: FleetSortKey
  sort: FleetSort
  onSort: (key: FleetSortKey) => void
  right?: boolean
}) {
  const active = sort?.key === column
  const Arrow = sort?.dir === "asc" ? ChevronUp : ChevronDown
  return (
    <TableHead
      className={cn(right && "text-right")}
      aria-sort={active ? (sort.dir === "asc" ? "ascending" : "descending") : "none"}
    >
      <button
        type="button"
        onClick={() => onSort(column)}
        className={cn(
          "-mx-1 inline-flex items-center gap-1 rounded-sm px-1 focus-ring transition-colors hover:text-foreground",
          active && "text-foreground",
        )}
      >
        {label}
        {active && <Arrow aria-hidden className="size-3" />}
      </button>
    </TableHead>
  )
}
