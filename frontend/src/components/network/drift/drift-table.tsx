"use client"

import { useState } from "react"
import { Cross } from "@/components/icons"
import { useArrivals } from "@/hooks/use-arrivals"
import { driftReading, DRIFT_DOMAINS, type DriftRow } from "@/lib/network-drift"
import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import { Disclosure } from "@/components/form"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { EmptyState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Digest } from "@/components/network/drift/digest"

type Lens = "differ" | "unknown" | "matching" | ""

const DIFFER = ["missing", "drift", "conflict"]
const UNKNOWN = ["unreadable", "unknown"]

const LABEL: Record<string, string> = {
  config: "Saved configuration",
  ...Object.fromEntries(DRIFT_DOMAINS.map(({ key, label }) => [key, label])),
}

/** What a row says about itself, worst first, so the page opens on what needs a look. */
const WORST: Record<string, number> = {
  conflict: 0,
  drift: 1,
  missing: 1,
  unreadable: 2,
  unknown: 3,
  matching: 4,
  not_required: 5,
}

/** A row with nothing compared field by field has no evidence to unfold. */
const hasEvidence = (row: DriftRow) =>
  Boolean(row.observation) &&
  (Object.keys(row.observation!.expected ?? {}).length > 0 ||
    Object.keys(row.observation!.observed ?? {}).length > 0)

/**
 * Every comparison the inspection made, in one table, worst first: the
 * saved configuration and journal, each owned file and kernel object, the boot
 * unit and the blocklist sets.
 *
 * Framed, because it is a table (§2). The three chips are the headline figures
 * the page used to open on, now counting and narrowing the rows they count:
 * *differ* in amber while there is one, *incomplete*, and *matching*. A row
 * whose status changes between two inspections is a new row and rises, so a
 * kernel object that drifted while the page was open is seen arriving. Wide,
 * the status, the resource, its domain and the reason it says so stand in
 * fixed columns; narrower, each row is drawn down.
 */
export function DriftTable({
  rows,
  domain,
  onDomain,
}: {
  rows: DriftRow[]
  domain: string
  onDomain: (domain: string) => void
}) {
  const [lens, setLens] = useState<Lens>("")
  const inDomain = rows.filter((row) => !domain || row.domain === domain)
  const counts = {
    differ: inDomain.filter((row) => DIFFER.includes(row.status)).length,
    unknown: inDomain.filter((row) => UNKNOWN.includes(row.status)).length,
    matching: inDomain.filter((row) => row.status === "matching").length,
  }
  const shown = inDomain
    .filter((row) =>
      lens === "differ"
        ? DIFFER.includes(row.status)
        : lens === "unknown"
          ? UNKNOWN.includes(row.status)
          : lens === "matching"
            ? row.status === "matching"
            : true,
    )
    .sort((a, b) => (WORST[a.status] ?? 3) - (WORST[b.status] ?? 3))
  const arrived = useArrivals(shown.map((row) => `${row.id}:${row.status}`))
  const chip = (value: Exclude<Lens, "">, label: string, tone?: "warning") => (
    <FilterChip
      selected={lens === value}
      onClick={() => setLens(lens === value ? "" : value)}
      title={`Show only the readings that are ${label.toLowerCase()}`}
    >
      {tone && <span aria-hidden className="size-1.5 rounded-full bg-warning" />}
      {label}
      <ChipCount className={cn(tone && "text-warning opacity-100")}>{counts[value]}</ChipCount>
    </FilterChip>
  )
  return (
    <Panel id="comparisons" className="scroll-mt-4">
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
        <ChipStrip aria-label="Which readings" className="mr-auto">
          {(counts.differ > 0 || lens === "differ") && chip("differ", "Differ", "warning")}
          {(counts.unknown > 0 || lens === "unknown") && chip("unknown", "Incomplete")}
          {chip("matching", "Matching")}
        </ChipStrip>
        {domain && (
          <FilterChip selected aria-label="Show every domain" onClick={() => onDomain("")}>
            {LABEL[domain]}
            <Cross aria-hidden className="size-3" />
          </FilterChip>
        )}
      </PanelHeader>
      <PanelBody flush>
        {shown.length === 0 ? (
          <EmptyState
            title="No readings match"
            description="Clear the chip or pick a different domain."
            className="my-4"
            action={
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  setLens("")
                  onDomain("")
                }}
              >
                Clear filters
              </Button>
            }
          />
        ) : (
          <ul aria-label="Comparisons" className="divide-y divide-hairline px-4">
            {shown.map((row) => (
              <li
                key={`${row.id}:${row.status}`}
                className={cn(arrived.has(`${row.id}:${row.status}`) && "animate-rise")}
              >
                <div className="grid min-w-0 gap-x-6 gap-y-1 py-3 xl:grid-cols-[11rem_minmax(0,1.4fr)_9rem_minmax(0,1fr)] xl:items-baseline">
                  <Status {...driftReading(row.status)} />
                  <p className="min-w-0 font-mono text-body break-all">{row.resource}</p>
                  <p className="text-hint text-muted-foreground">{LABEL[row.domain]}</p>
                  <div className="min-w-0 space-y-1">
                    {row.reason && <p className="text-body text-muted-foreground">{row.reason}</p>}
                    {hasEvidence(row) && (
                      <Disclosure quiet summary="Comparison evidence">
                        <div className="space-y-2 text-body">
                          <p>
                            Coverage: {row.observation!.coverage.replaceAll("-", " ")}
                            {" · "}
                            {row.observation!.owned
                              ? "Saved managed resource"
                              : "Ownership cannot be established"}
                          </p>
                          {Object.entries(row.observation!.expected ?? {}).map(([name, value]) => (
                            <Digest key={name} label={`Expected ${name}`} value={value} />
                          ))}
                          {Object.entries(row.observation!.observed ?? {}).map(([name, value]) => (
                            <Digest key={name} label={`Observed ${name}`} value={value} />
                          ))}
                        </div>
                      </Disclosure>
                    )}
                  </div>
                </div>
              </li>
            ))}
          </ul>
        )}
      </PanelBody>
      <PanelFooter className="text-hint text-muted-foreground">
        <span className="numeric">{plural(shown.length, "reading")}</span>
        <span className="text-muted-foreground/40">·</span>
        <span>differences first, then what could not be read, then what matches</span>
      </PanelFooter>
    </Panel>
  )
}
