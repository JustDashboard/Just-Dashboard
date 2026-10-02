"use client"

import Link from "next/link"
import { ChevronDown, ChevronUp } from "@/components/icons"
import { bytes, relativeTime, truncateMiddle } from "@/lib/format"
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
  concernLevel,
  reachWord,
  whereWord,
  type BackupReading,
  type Concern,
  type FleetSort,
  type FleetSortKey,
} from "@/components/database/fleet/fleet"
import { ProtectedMark, factTone } from "@/components/database/fleet/fleet-card"
import type { FleetControl } from "@/components/database/fleet/use-fleet-control"
import { EngineGlyph, EnvironmentTag } from "@/components/database/kit"
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
  storedOf,
  control,
  sort,
  onSort,
}: {
  entries: DbFleetEntry[]
  engineOf: (entry: DbFleetEntry) => Engine
  concernsOf: (entry: DbFleetEntry) => Concern[]
  backups: Map<number, BackupReading>
  feeds: Map<number, { products: string[]; count: number }>
  /** What each database holds, where anybody knows: the same answer the cards give. */
  storedOf: (entry: DbFleetEntry) => number | undefined
  control: FleetControl
  sort: FleetSort
  onSort: (key: FleetSortKey) => void
}) {
  // Eleven columns need the widest shell. Below it the two that only name the
  // database — its engine, where it runs — are said on a second line under
  // the name instead of in columns of their own, so nothing the table says is
  // dropped and the figures being compared stay on screen (§12).
  const wide = useMediaQuery("(min-width: 1536px)")
  return (
    <Panel className="min-w-0">
      {/* Below the widest shell the cells stand a step closer together: nine
          columns of figures and a name fit the page without a sideways
          scroll, whatever the figures are. */}
      <Table className={cn(!wide && "[&_td]:px-3 [&_th]:px-3")}>
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
            const stored = storedOf(entry)
            const product = `${engine.label}${entry.versionNumber ? ` ${entry.versionNumber}` : ""}`
            return (
              <TableRow key={entry.id} className="group" data-state-of={entry.state}>
                <TableCell>
                  {busy ? (
                    <TextShimmer className="text-xs font-medium">{`${busy}…`}</TextShimmer>
                  ) : (
                    <DatabaseStatusMark status={fleetStatus(entry)} />
                  )}
                </TableCell>
                <TableCell>
                  {/* The cap is on this box, not the cell: a table sizes a
                      cell to its content, and only a box with a width of its
                      own can make a long name give way. */}
                  <span className={cn("flex min-w-0 flex-col gap-0.5", wide ? "w-64" : "w-60")}>
                    <span className="flex min-w-0 items-center gap-2">
                      <span className="flex shrink-0" title={product}>
                        <EngineGlyph engine={engine} className={cn(down && "opacity-60")} />
                      </span>
                      <Link
                        href={sectionHref(entry.id)}
                        aria-label={`Open ${entry.name}`}
                        title={entry.name}
                        className="min-w-0 truncate rounded-sm pr-0.5 text-body font-medium focus-ring hover:underline"
                      >
                        {entry.name}
                      </Link>
                      <EnvironmentTag environment={entry.environment} className="max-w-24" />
                      {entry.readOnly && <ProtectedMark />}
                    </span>
                    {!wide && (
                      <span
                        className="flex min-w-0 items-baseline gap-1.5 pl-[1.375rem] text-hint text-muted-foreground"
                        title={`${product} · ${where.text}`}
                      >
                        <span className="shrink-0">{product}</span>
                        {/* Cut in the middle: the end of a path is the file's name. */}
                        <span className={cn("min-w-0 truncate", where.mono && "font-mono")}>
                          {truncateMiddle(where.text, 24)}
                        </span>
                      </span>
                    )}
                  </span>
                </TableCell>
                {wide && <TableCell className="text-muted-foreground">{product}</TableCell>}
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
                  {stored !== undefined ? bytes(stored) : "—"}
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
                    "whitespace-nowrap text-muted-foreground",
                    factTone(concernLevel(concerns, "never-backed-up", "stale-backup")),
                  )}
                >
                  {backup?.newest
                    ? relativeTime(backup.newest)
                    : has("never-backed-up")
                      ? "never"
                      : "—"}
                </TableCell>
                <TableCell
                  className={cn(
                    "whitespace-nowrap text-muted-foreground",
                    factTone(concernLevel(concerns, "public")),
                  )}
                >
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
