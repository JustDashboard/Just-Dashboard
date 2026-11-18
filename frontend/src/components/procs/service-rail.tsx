"use client"

import { useSearchParams } from "next/navigation"
import { Servers } from "@/components/icons"
import { bytes, duration, percent } from "@/lib/format"
import type { SystemdUnit } from "@/lib/types"
import { ProductLogo, unitProduct } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { Rail, RailFigures, RailLine, RailName, RailRow, railInk } from "@/components/rail"
import { serviceHref } from "@/components/procs/service-facts"
import { cpuTone } from "@/components/procs/shared"
import {
  failureWords,
  unitBucket,
  unitShortName,
  unitStateWord,
  unitTone,
} from "@/components/procs/units"

/**
 * The Services table, folded into a column beside the unit it opened: its
 * filters, its search and its order (failed first, then by name), so the
 * next unit to check is one press away. Each row is the unit as the product
 * it runs with its state in the logo's corner, its name, how it is doing in
 * a few words and, while it runs, its two readings; the one on screen is
 * filled.
 *
 * The rows hold their order while the pointer or focus is in the column
 * (`useInspectionOrder`), so a unit that fails under the pointer does not
 * move the row about to be pressed.
 */
export function ServiceRail({
  units,
  current,
  currentUnit,
  loading,
  error,
  onRetry,
  query,
  onQuery,
  filtered,
  onClearFilters,
  caption,
  now,
  onOpen,
  bindings,
  className,
}: {
  units: SystemdUnit[]
  current: string
  /** The unit on screen, drawn first when the list does not hold it. */
  currentUnit?: SystemdUnit
  loading: boolean
  /** The list could not be read: said in place of the rows. */
  error?: Error
  onRetry?: () => void
  query: string
  onQuery: (value: string) => void
  filtered: boolean
  onClearFilters: () => void
  caption: string
  now: number
  /** Opens a unit carrying the page's view; the row's link does the same without it. */
  onOpen?: (name: string) => void
  bindings: React.HTMLAttributes<HTMLElement>
  className?: string
}) {
  const tab = useSearchParams().get("tab") ?? undefined
  return (
    <Rail
      slot="service-rail"
      label="Services"
      search={{ placeholder: "Name or description", query, onQuery }}
      rows={units}
      keyOf={(unit) => unit.name}
      current={(unit) => unit.name === current}
      pinned={currentUnit}
      row={(unit, on) => (
        <ServiceRow
          unit={unit}
          current={on}
          now={now}
          href={serviceHref(unit.name, tab)}
          onOpen={onOpen}
        />
      )}
      loading={loading}
      loadingRows={8}
      error={error}
      onRetry={onRetry}
      empty="No service matches."
      filtered={filtered}
      onClearFilters={onClearFilters}
      caption={caption}
      bindings={bindings}
      className={className}
    />
  )
}

/** How a unit is doing, in the rail's few words. */
function railLine(unit: SystemdUnit, now: number): { text: string; tone?: "danger" | "warning" } {
  const bucket = unitBucket(unit)
  if (bucket === "failed") return { text: failureWords(unit), tone: "danger" }
  if (unit.subState === "auto-restart") return { text: "restarting", tone: "warning" }
  if (bucket === "changing") return { text: unitStateWord(unit), tone: "warning" }
  if (bucket === "active" && unit.subState === "running" && unit.activeSince) {
    return { text: `up ${duration(Math.max(0, now / 1000 - unit.activeSince))}` }
  }
  if (unit.unitFileState === "masked") return { text: "masked" }
  return { text: unitStateWord(unit) }
}

function ServiceRow({
  unit,
  current,
  now,
  href,
  onOpen,
}: {
  unit: SystemdUnit
  current: boolean
  now: number
  href: string
  onOpen?: (name: string) => void
}) {
  const line = railLine(unit, now)
  const running = unitBucket(unit) === "active" && (unit.cpuReady || (unit.memoryBytes ?? 0) > 0)
  const tone = cpuTone(unit.cpuPercent ?? 0)
  return (
    <RailRow
      item={unit.name}
      name={unit.name}
      title={unit.description}
      current={current}
      href={href}
      onOpen={onOpen && (() => onOpen(unit.name))}
      mark={
        <ProductLogo
          id={unitProduct(unit.name)}
          size="sm"
          fallback={Servers}
          className="size-7 rounded-md [&_img]:size-4"
        />
      }
      dot={<StatusDot tone={unitTone(unit)} className="size-1.5" />}
      end={
        running && (
          <RailFigures
            top={unit.cpuReady ? percent(unit.cpuPercent ?? 0) : "—"}
            bottom={(unit.memoryBytes ?? 0) > 0 ? bytes(unit.memoryBytes) : "—"}
            ink={tone === "danger" ? "font-medium text-destructive" : railInk(tone)}
          />
        )
      }
    >
      <RailName current={current}>{unitShortName(unit.name)}</RailName>
      <RailLine className="numeric" ink={railInk(line.tone)}>
        {line.text}
      </RailLine>
    </RailRow>
  )
}
