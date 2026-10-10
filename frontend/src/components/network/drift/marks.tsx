"use client"

import { LogoGlyph } from "@/components/logo"
import { ProductLogo } from "@/components/product-logo"
import { FileText } from "@/components/icons"
import { driftReading, type DriftRow, type DriftStatus } from "@/lib/network-drift"
import { cn } from "@/lib/utils"

/** A comparison's status as a fill: the state hues, and the meter's track for nothing to compare. */
export const STATUS_FILL: Record<DriftStatus, string> = {
  matching: "bg-success",
  drift: "bg-warning",
  missing: "bg-warning",
  conflict: "bg-destructive",
  unreadable: "bg-muted-foreground/45",
  unknown: "bg-muted-foreground/45",
  not_required: "bg-meter-track",
}

/**
 * Every comparison in a set as one block in its status's colour, worst
 * first, so a domain of ten matching files and one changed one is ten green
 * blocks and an amber one — the address pool's blocks on the Networks page,
 * drawn for readings instead of subnets.
 */
export function DriftBlocks({ rows, className }: { rows: DriftRow[]; className?: string }) {
  if (rows.length === 0) return null
  return (
    <span
      role="img"
      aria-label={rows.map((row) => `${row.label}: ${driftReading(row.status).label}`).join(", ")}
      className={cn("flex flex-wrap gap-[3px]", className)}
    >
      {rows.map((row) => (
        <span
          key={row.id}
          title={`${row.label} · ${driftReading(row.status).label}`}
          className={cn("size-2.5 rounded-[2px]", STATUS_FILL[row.status] ?? STATUS_FILL.unknown)}
        />
      ))}
    </span>
  )
}

/**
 * A digest as the seven characters git shows of a commit, the whole of it on
 * hover. Beside the value it is compared with, a different one is amber.
 */
export function ShortDigest({
  value,
  against,
  className,
}: {
  value?: string
  against?: string
  className?: string
}) {
  if (!value) return <span className={cn("text-muted-foreground", className)}>unknown</span>
  const differs = against !== undefined && against !== value
  return (
    <span
      title={value}
      className={cn("font-mono text-hint", differs ? "text-warning" : "text-foreground", className)}
    >
      {value.length > 12 ? value.slice(0, 7) : value}
    </span>
  )
}

/**
 * A comparison as the product that owns the thing compared, on the tile every
 * product takes. The saved configuration, the journal and the recovery
 * helper are the dashboard's own, so they carry its mark in its blue.
 */
export function DriftMark({ row, className }: { row: DriftRow; className?: string }) {
  if (["spec", "journal", "recovery-helper"].includes(row.kind))
    return (
      <span
        aria-hidden="true"
        className={cn(
          "flex size-8 shrink-0 items-center justify-center rounded-lg border border-rule-brand bg-wash-brand text-brand",
          className,
        )}
      >
        <LogoGlyph className="h-4" />
      </span>
    )
  return <ProductLogo id={row.product} size="sm" fallback={FileText} className={className} />
}
