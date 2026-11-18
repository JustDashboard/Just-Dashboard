"use client"

import { useSearchParams } from "next/navigation"
import { Cpu } from "@/components/icons"
import { bytes, percent } from "@/lib/format"
import type { ProcessRow } from "@/lib/types"
import { ProductLogo, processProduct } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { Rail, RailFigures, RailLine, RailName, RailRow, railInk } from "@/components/rail"
import { processHref } from "@/components/procs/process-facts"
import { cpuTone, ownerName, processKey, processStateTone } from "@/components/procs/shared"

/**
 * The table, folded into a column beside the process it opened: the same
 * ranking, the same filters and the same search, so the next process to look
 * at is one press away rather than a trip back to the table and a scroll to
 * where it was. Each row is the process as the product it is, its PID, who
 * runs it and its two readings; the one on screen is filled.
 *
 * The rows hold their order while the pointer or focus is in the column
 * (`useInspectionOrder`), so the row about to be pressed does not move.
 */
export function ProcessRail({
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
  rows: ProcessRow[]
  current: number | null
  /** The process on screen, drawn first when the list does not hold it. */
  currentRow?: ProcessRow
  loading: boolean
  /** The list could not be read: said in place of the rows. */
  error?: Error
  onRetry?: () => void
  query: string
  onQuery: (value: string) => void
  filtered: boolean
  onClearFilters: () => void
  caption: string
  /** Opens a process carrying the page's view; the row's link does the same without it. */
  onOpen?: (pid: number) => void
  bindings: React.HTMLAttributes<HTMLElement>
  className?: string
}) {
  const tab = useSearchParams().get("tab") ?? undefined
  return (
    <Rail
      slot="process-rail"
      label="Processes"
      search={{ placeholder: "Name, PID, user or owner", query, onQuery }}
      rows={rows}
      keyOf={processKey}
      current={(row) => row.pid === current}
      pinned={currentRow}
      row={(row, on) => (
        <ProcessRow row={row} current={on} href={processHref(row.pid, tab)} onOpen={onOpen} />
      )}
      loading={loading}
      loadingRows={8}
      error={error}
      onRetry={onRetry}
      empty="No process matches."
      filtered={filtered}
      onClearFilters={onClearFilters}
      caption={caption}
      bindings={bindings}
      className={className}
    />
  )
}

function ProcessRow({
  row,
  current,
  href,
  onOpen,
}: {
  row: ProcessRow
  current: boolean
  href: string
  onOpen?: (pid: number) => void
}) {
  const tone = cpuTone(row.cpuPercent)
  return (
    <RailRow
      item={processKey(row)}
      name={row.name}
      label={`${row.name}, PID ${row.pid}`}
      current={current}
      href={href}
      onOpen={onOpen && (() => onOpen(row.pid))}
      mark={
        <ProductLogo
          id={processProduct(row.name)}
          size="sm"
          fallback={Cpu}
          className="size-7 rounded-md [&_img]:size-4"
        />
      }
      dot={<StatusDot state={processStateTone(row.state)} className="size-1.5" />}
      end={
        <RailFigures
          top={row.cpuReady === false ? "—" : percent(row.cpuPercent)}
          bottom={row.rss ? bytes(row.rss) : "—"}
          ink={tone === "danger" ? "font-medium text-destructive" : railInk(tone)}
        />
      }
    >
      <span className="flex min-w-0 items-baseline gap-1.5">
        <RailName current={current}>{row.name}</RailName>
        <span className="numeric shrink-0 font-mono text-micro text-muted-foreground">
          {row.pid}
        </span>
      </span>
      <RailLine>
        {row.manager === "kernel"
          ? "kernel thread"
          : [ownerName(row), row.username].filter(Boolean).join(" · ") || "unmanaged"}
      </RailLine>
    </RailRow>
  )
}
