"use client"

import { createContext, useContext } from "react"
import { cn } from "@/lib/utils"
import { FormFact, FormFacts } from "@/components/form"

/**
 * One figure with its name, said inline as "label value".
 *
 * It was an eyebrow over a figure, which is a tile in all but its border: a
 * fifth way to say one fact beside `FormFact`, `Detail`, `StatTile` and
 * `HostFact`. It now draws as `FormFact`, the inline label and value, so a
 * strip of them reads as a line of words (D4).
 *
 * @deprecated use `FormFacts` and `FormFact`; WP-39 bans this.
 */
export function Metric({
  label,
  value,
  hint,
  className,
}: {
  label: React.ReactNode
  value: React.ReactNode
  hint?: React.ReactNode
  className?: string
}) {
  return (
    <span className={cn("inline-flex min-w-0 items-baseline gap-1.5", className)}>
      <FormFact label={label}>
        <span className="numeric">{value}</span>
      </FormFact>
      {hint && <span className="truncate">{hint}</span>}
    </span>
  )
}

/**
 * A run of `Metric`s, as a line of facts that wraps between them.
 *
 * @deprecated use `FormFacts`; WP-39 bans this.
 */
export function MetricStrip({ className, ...props }: React.ComponentProps<"div">) {
  return <FormFacts className={cn("text-xs", className)} {...props} />
}

type Size = "xs" | "body"

const SizeContext = createContext<Size>("xs")

/**
 * Labels and their values, stacked: the replacement for the `<dl>` grids each
 * card used to hand-roll with its own column widths, and for the five local
 * "label, then value, ruled" lists that differed only in the label's width
 * and the text's size.
 *
 * - `ruled` draws a hairline between rows, for a list read as a record (a
 *   file's details, a runtime's facts) rather than a few lines in a card.
 * - `labelWidth="fixed"` gives every label one column (8rem), so two lists
 *   one over the other line up; `auto` (the default) is as wide as the
 *   longest label.
 * - `size="body"` is for a list that is the page's subject; `xs` (the
 *   default) for one inside a card or a pane.
 */
export function DetailList({
  ruled,
  labelWidth = "auto",
  size = "xs",
  className,
  ...props
}: React.ComponentProps<"dl"> & {
  ruled?: boolean
  labelWidth?: "auto" | "fixed"
  size?: Size
}) {
  return (
    <SizeContext.Provider value={size}>
      <dl
        data-slot="detail-list"
        data-ruled={ruled || undefined}
        className={cn(
          "grid gap-x-4",
          labelWidth === "fixed"
            ? "grid-cols-[8rem_minmax(0,1fr)]"
            : "grid-cols-[minmax(0,auto)_minmax(0,1fr)]",
          ruled
            ? "gap-y-0 *:border-t *:border-hairline *:py-2 [&>*:nth-child(-n+2)]:border-t-0"
            : "gap-y-1.5",
          className,
        )}
        {...props}
      />
    </SizeContext.Provider>
  )
}

/**
 * One row of a `DetailList`: the label, then the value. `mono` sets a literal
 * from the host (a path, an id) in mono, `icon` draws a glyph before the
 * label, and `after` sits at the value's end (a copy button, a link).
 */
export function Detail({
  label,
  mono,
  icon: Glyph,
  after,
  children,
  className,
}: {
  label: React.ReactNode
  mono?: boolean
  icon?: React.ComponentType<{ className?: string }>
  after?: React.ReactNode
  children: React.ReactNode
  className?: string
}) {
  const size = useContext(SizeContext) === "body" ? "text-body" : "text-xs"
  return (
    <>
      <dt
        className={cn(
          "text-muted-foreground",
          size,
          Glyph && "inline-flex min-w-0 items-center gap-1.5",
        )}
      >
        {Glyph && <Glyph aria-hidden className="size-3.5 shrink-0" />}
        {label}
      </dt>
      <dd
        className={cn(
          "min-w-0",
          size,
          mono && "font-mono",
          after !== undefined && "flex items-center gap-2",
          className,
        )}
      >
        {after !== undefined ? <span className="min-w-0 truncate">{children}</span> : children}
        {after}
      </dd>
    </>
  )
}
