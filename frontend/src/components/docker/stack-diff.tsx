"use client"

import { cn } from "@/lib/utils"
import type { DiffLine } from "@/lib/types"
import { ProductGlyph } from "@/components/product-logo"
import { Well } from "@/components/panel"
import { serviceLane } from "@/components/docker/stack-service-readings"
import { diffHunks } from "@/components/docker/stack-views"

/**
 * A service named the way every view of the stack names it: its lane down
 * the left, the hue its lines take in the stack's log and its row's edge in
 * the table, then its product and its name.
 */
export function ServiceLabel({
  name,
  product,
  muted,
  className,
}: {
  name: string
  product?: string
  muted?: boolean
  className?: string
}) {
  return (
    <span className={cn("flex min-w-0 items-center gap-2", className)}>
      <span
        aria-hidden
        className="h-4 w-0.5 shrink-0 rounded-full"
        style={{ background: serviceLane(name) }}
      />
      {product && <ProductGlyph id={product} className={cn(muted && "opacity-50 grayscale")} />}
      <span
        className={cn(
          "min-w-0 truncate text-body font-medium",
          muted && "font-normal text-muted-foreground",
        )}
      >
        {name}
      </span>
    </span>
  )
}

/** A count of added and removed lines, in the colours the lines are drawn in. */
export function DiffCount({ added, removed }: { added: number; removed: number }) {
  return (
    <span className="numeric flex shrink-0 items-center gap-1.5 font-mono text-hint">
      <span className="text-success">+{added}</span>
      <span className="text-destructive">−{removed}</span>
    </span>
  )
}

/**
 * A compose file's changes as hunks, each headed by the service it changes —
 * so a diff of six services reads as which of them moved before a line of it
 * is read. Every diff of the stack is drawn here: what Deploy would apply,
 * what an edit has not saved yet, and how a recorded deployment's file
 * differs from the one on disk.
 */
export function ComposeDiff({
  lines,
  productOf,
  className,
}: {
  lines: DiffLine[]
  productOf?: (service: string) => string | undefined
  className?: string
}) {
  const hunks = diffHunks(lines)
  return (
    <div className={cn("flex min-w-0 flex-col gap-3", className)}>
      {hunks.map((hunk, i) => (
        <section key={i} className="min-w-0" aria-label={hunk.section ?? "Change"}>
          <header className="mb-1.5 flex min-w-0 items-center justify-between gap-3">
            {hunk.section ? (
              <ServiceLabel name={hunk.section} product={productOf?.(hunk.section)} />
            ) : (
              <span className="text-body font-medium text-muted-foreground">Top of the file</span>
            )}
            <DiffCount added={hunk.added} removed={hunk.removed} />
          </header>
          {/* A diff is output you read, so it sits in the well every other
              read-only output in the product sits in. */}
          <Well className="overflow-x-auto p-0 text-hint">
            <pre className="min-w-fit py-1">
              {hunk.lines.map((line, j) => (
                <div
                  key={j}
                  className={cn(
                    "flex px-3 whitespace-pre",
                    line.kind === "added" && "bg-wash-success text-success",
                    line.kind === "removed" && "bg-wash-danger text-destructive",
                    line.kind === "same" && "text-muted-foreground",
                  )}
                >
                  <span aria-hidden className="w-4 shrink-0 select-none">
                    {line.kind === "added" ? "+" : line.kind === "removed" ? "−" : ""}
                  </span>
                  <span className="sr-only">
                    {line.kind === "added" ? "added: " : line.kind === "removed" ? "removed: " : ""}
                  </span>
                  {line.text}
                </div>
              ))}
            </pre>
          </Well>
        </section>
      ))}
    </div>
  )
}
