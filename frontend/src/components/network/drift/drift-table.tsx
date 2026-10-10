"use client"

import { useArrivals } from "@/hooks/use-arrivals"
import { useMediaQuery } from "@/hooks/use-mobile"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { SearchInput } from "@/components/page"
import { ProductGlyph } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { EmptyState } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Cross, Filter } from "@/components/icons"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  stickyTableHeader,
} from "@/components/ui/table"
import { hueFor, LANES } from "@/lib/hue"
import {
  DRIFT_DOMAINS,
  driftCounts,
  driftKindLabel,
  driftReading,
  driftSorted,
  type DriftDomain,
  type DriftRow,
} from "@/lib/network-drift"
import { cn } from "@/lib/utils"
import { DriftMark, ShortDigest } from "@/components/network/drift/marks"

export type DriftScope = "all" | "differ" | "incomplete" | "matching"

const IN_SCOPE: Record<DriftScope, (row: DriftRow) => boolean> = {
  all: () => true,
  differ: (row) => ["missing", "drift", "conflict"].includes(row.status),
  incomplete: (row) => ["unreadable", "unknown"].includes(row.status),
  matching: (row) => row.status === "matching",
}

const DOMAIN_SHORT: Record<DriftDomain, string> = {
  saved: "Saved",
  files: "Files",
  kernel: "Kernel",
  boot: "Boot",
  blocklists: "Blocklists",
}

// The product a domain's chip is drawn with: the one most of its rows are.
const DOMAIN_GLYPH: Partial<Record<DriftDomain, string>> = {
  files: "netfilter",
  kernel: "linux",
  boot: "systemd",
}

/**
 * Every comparison the inspection made, worst first, framed because it is a
 * table (§2). The four grey tiles that stood over the page are its chips
 * now: what differs (amber, red while another owner holds something), what
 * could not be compared and what matches, each counting and narrowing; and
 * the domains beside them, each drawn as the product most of it is. A row
 * whose state changes is a new row, so it rises (§11 *arrived*).
 *
 * A comparison is named the way a reader knows it — a route by where it
 * goes, an address by its prefix and device — and its evidence is read in
 * place: expected beside observed, a digest as seven characters, amber where
 * the two disagree.
 */
export function DriftTable({
  rows,
  scope,
  onScope,
  domain,
  onDomain,
  query,
  onQuery,
}: {
  rows: DriftRow[]
  scope: DriftScope
  onScope: (scope: DriftScope) => void
  domain?: DriftDomain
  onDomain: (domain?: DriftDomain) => void
  query: string
  onQuery: (query: string) => void
}) {
  const wide = useMediaQuery("(min-width: 1280px)")
  const counts = driftCounts(rows)
  const sorted = driftSorted(rows)
  const inDomain = domain ? sorted.filter((row) => row.domain === domain) : sorted
  const needle = query.trim().toLowerCase()
  const visible = inDomain
    .filter(IN_SCOPE[scope])
    .filter(
      (row) =>
        !needle ||
        [row.label, row.detail, row.kind, driftKindLabel(row.kind)].some((text) =>
          text?.toLowerCase().includes(needle),
        ),
    )
  const arrived = useArrivals(visible.map((row) => `${row.id}:${row.status}`))
  const chip = (value: DriftScope) => ({
    selected: scope === value,
    onClick: () => onScope(scope === value && value !== "all" ? "all" : value),
  })
  return (
    <Panel aria-label="Comparisons">
      <PanelHeader
        title={
          <>
            Comparisons
            <span className="numeric ml-2 text-body font-normal text-muted-foreground">
              {rows.length}
            </span>
          </>
        }
      >
        <ChipStrip aria-label="State" className="mr-auto">
          <FilterChip {...chip("all")}>
            All <ChipCount>{rows.length}</ChipCount>
          </FilterChip>
          {(counts.differences > 0 || scope === "differ") && (
            <FilterChip {...chip("differ")}>
              <span
                aria-hidden
                className={cn(
                  "size-1.5 rounded-full",
                  counts.conflicts ? "bg-destructive" : "bg-warning",
                )}
              />
              Differ
              <ChipCount
                className={cn(
                  "opacity-100",
                  counts.conflicts ? "text-destructive" : "text-warning",
                )}
              >
                {counts.differences}
              </ChipCount>
            </FilterChip>
          )}
          {(counts.unknown > 0 || scope === "incomplete") && (
            <FilterChip {...chip("incomplete")}>
              <span aria-hidden className="size-1.5 rounded-full bg-muted-foreground" />
              Incomplete <ChipCount>{counts.unknown}</ChipCount>
            </FilterChip>
          )}
          <FilterChip {...chip("matching")}>
            <span aria-hidden className="size-1.5 rounded-full bg-success" />
            Matching <ChipCount>{counts.matching}</ChipCount>
          </FilterChip>
        </ChipStrip>
      </PanelHeader>
      <PanelToolbar>
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="File, device, address or setting"
          containerClassName="sm:w-72"
        />
        <ChipStrip aria-label="Domain">
          {DRIFT_DOMAINS.filter((d) => rows.some((row) => row.domain === d.domain)).map((d) => (
            <FilterChip
              key={d.domain}
              selected={domain === d.domain}
              title={d.label}
              onClick={() => onDomain(domain === d.domain ? undefined : d.domain)}
            >
              {DOMAIN_GLYPH[d.domain] && (
                <ProductGlyph id={DOMAIN_GLYPH[d.domain]!} className="size-3.5" />
              )}
              {DOMAIN_SHORT[d.domain]}
              <ChipCount>{rows.filter((row) => row.domain === d.domain).length}</ChipCount>
            </FilterChip>
          ))}
        </ChipStrip>
        {domain && (
          <FilterChip
            selected
            className="sm:ml-auto"
            aria-label="Show every domain"
            onClick={() => onDomain(undefined)}
          >
            {DRIFT_DOMAINS.find((d) => d.domain === domain)?.label}
            <Cross aria-hidden className="size-3" />
          </FilterChip>
        )}
      </PanelToolbar>
      <PanelBody flush>
        {visible.length === 0 ? (
          <EmptyState
            icon={Filter}
            title="No comparisons match"
            description="Clear the search, or pick a different state or domain."
            className="my-4"
            action={
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  onScope("all")
                  onDomain(undefined)
                  onQuery("")
                }}
              >
                Clear filters
              </Button>
            }
          />
        ) : wide ? (
          <Table className="table-fixed" containerClassName="max-h-[calc(100svh-13rem)]">
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead className="w-44">State</TableHead>
                <TableHead>Compared</TableHead>
                <TableHead className="w-32">Kind</TableHead>
                <TableHead className="w-[38%]">Evidence</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {visible.map((row) => (
                <TableRow
                  key={row.id}
                  className={cn(arrived.has(`${row.id}:${row.status}`) && "animate-rise")}
                >
                  <TableCell className="py-2.5 align-top">
                    <Status {...driftReading(row.status)} />
                  </TableCell>
                  <TableCell className="py-2.5">
                    <Compared row={row} />
                  </TableCell>
                  <TableCell className="py-2.5">
                    <KindWord kind={row.kind} />
                  </TableCell>
                  <TableCell className="py-2.5">
                    <Evidence row={row} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <ul aria-label="Comparisons" className="divide-y divide-hairline px-4">
            {visible.map((row) => (
              <li
                key={row.id}
                className={cn(
                  "min-w-0 space-y-2 py-3",
                  arrived.has(`${row.id}:${row.status}`) && "animate-rise",
                )}
              >
                <div className="flex min-w-0 items-start justify-between gap-3">
                  <Compared row={row} />
                  <Status {...driftReading(row.status)} className="shrink-0" />
                </div>
                <div className="space-y-1 pl-11">
                  <KindWord kind={row.kind} />
                  <Evidence row={row} />
                </div>
              </li>
            ))}
          </ul>
        )}
      </PanelBody>
    </Panel>
  )
}

function Compared({ row }: { row: DriftRow }) {
  return (
    <div className="flex min-w-0 items-center gap-3">
      <DriftMark row={row} />
      <div className="min-w-0">
        <p className="font-mono text-body break-all">{row.label}</p>
        {row.detail && <p className="text-hint break-all text-muted-foreground">{row.detail}</p>}
      </div>
    </div>
  )
}

/** A kind in its lane hue, so the routes, devices and files down a column read apart. */
function KindWord({ kind }: { kind: string }) {
  return (
    <span className="text-hint font-medium" style={{ color: hueFor(kind, LANES) }}>
      {driftKindLabel(kind)}
    </span>
  )
}

/** What was expected beside what was found, for the facts both sides name. */
function Evidence({ row }: { row: DriftRow }) {
  const keys = [
    ...new Set([...Object.keys(row.expected ?? {}), ...Object.keys(row.observed ?? {})]),
  ]
  const pairs = keys.slice(0, 3)
  return (
    <div className="min-w-0 space-y-1">
      {row.reason && (
        <p
          className={cn(
            "text-hint",
            row.status === "conflict" ? "text-destructive" : "text-muted-foreground",
          )}
        >
          {row.reason}
        </p>
      )}
      {pairs.length > 0 && (
        <dl className="flex flex-wrap gap-x-4 gap-y-0.5 text-hint">
          {pairs.map((key) => {
            const expected = row.expected?.[key]
            const observed = row.observed?.[key]
            return (
              <div key={key} className="flex min-w-0 items-center gap-1.5">
                <dt className="text-muted-foreground">{key === "sha256" ? "sha256" : key}</dt>
                <dd className="flex min-w-0 items-center gap-1">
                  {/* A fact only the host reported — the tool that answered — has no expected side. */}
                  <ShortDigest value={expected ?? observed} />
                  {expected !== undefined && observed !== undefined && observed !== expected && (
                    <>
                      <span aria-hidden className="text-muted-foreground">
                        →
                      </span>
                      <span className="sr-only">observed</span>
                      <ShortDigest value={observed} against={expected} />
                    </>
                  )}
                </dd>
              </div>
            )
          })}
          {keys.length > pairs.length && (
            <span className="text-muted-foreground">+{keys.length - pairs.length}</span>
          )}
        </dl>
      )}
    </div>
  )
}
