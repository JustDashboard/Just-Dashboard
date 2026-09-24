"use client"

import type { RefObject } from "react"
import { Plus, type Icon } from "@/components/icons"
import { cn } from "@/lib/utils"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"

/**
 * The parts of a wiring picture: two or more things and the lines between
 * them, drawn with `ui/animated-beam` from the marks' own positions. The
 * Credentials page draws the GitHub App this way, the run page draws the
 * release path, the overview draws the way a request reaches the project and
 * Notifications draws where an outcome goes — one vocabulary, so a reader who
 * has understood one of them has understood the others.
 *
 * A node is a mark with a name and a line of detail beside it. `align` says
 * which side of the mark the text sits on when the picture is wide, so the
 * line always leaves the mark over nothing: before it on the left edge, after
 * it on the right, under it in the middle. Narrow, marks stand in one column
 * and the text is always to their right.
 *
 * `aside` is a node's readings — a strip of outcomes, the days a certificate
 * has left — set at the far end of a wide column from `xl`, so the width a
 * one-column picture has there holds figures rather than nothing; narrower,
 * they sit under the hint. It is for a node on the start edge.
 */
export function WireNode({
  nodeRef,
  mark,
  eyebrow,
  title,
  hint,
  aside,
  align = "start",
  className,
}: {
  nodeRef?: RefObject<HTMLDivElement | null>
  mark: React.ReactNode
  eyebrow?: React.ReactNode
  title: React.ReactNode
  hint?: React.ReactNode
  aside?: React.ReactNode
  align?: "start" | "center" | "end"
  className?: string
}) {
  const text = (
    <div
      className={cn(
        "min-w-0",
        aside && "xl:flex-1",
        align === "center" &&
          "lg:absolute lg:top-full lg:left-1/2 lg:mt-3 lg:w-44 lg:-translate-x-1/2 lg:text-center",
      )}
    >
      {eyebrow && <p className="eyebrow">{eyebrow}</p>}
      <div className="text-body leading-snug font-medium break-words">{title}</div>
      {hint && <div className="text-hint leading-snug text-muted-foreground">{hint}</div>}
    </div>
  )
  return (
    <div
      className={cn(
        "flex min-w-0 items-center gap-3.5",
        align === "end" && "lg:flex-row-reverse lg:text-right",
        // Wide, the middle mark's caption hangs under it rather than sharing
        // its box, so every mark sits on one line and the lines between
        // them run level.
        align === "center" && "lg:relative lg:justify-center",
        className,
      )}
    >
      {/* Positioned, so the mark paints over the lines drawn under it. */}
      <div ref={nodeRef} className="relative z-10 flex shrink-0">
        {mark}
      </div>
      {aside ? (
        <div className="flex min-w-0 flex-1 flex-col gap-1.5 xl:flex-row xl:items-center xl:gap-6">
          {text}
          <div className="min-w-0 text-hint text-muted-foreground xl:shrink-0 xl:text-right">
            {aside}
          </div>
        </div>
      ) : (
        text
      )}
    </div>
  )
}

/**
 * A filled mark: the thing exists.
 *
 * `logo` is a product drawn as itself — a `ProductGlyph` child, or a kind's
 * glyph for a thing no product names — on ProductLogo's own tile, square as a
 * product's tile is everywhere else, so a channel or a source reads as the
 * service it is rather than as a letter in a circle. A child glyph or image is
 * sized by the mark.
 */
export function WireMark({
  tone = "neutral",
  size = "lg",
  shape = tone === "logo" ? "square" : "round",
  className,
  children,
}: {
  /** neutral is a step of ground; brand is this product; ink is a third party's own mark; logo is a product's. */
  tone?: "neutral" | "brand" | "ink" | "logo" | "success" | "danger" | "warning"
  size?: "lg" | "md" | "sm"
  shape?: "round" | "square"
  className?: string
  children: React.ReactNode
}) {
  // The tints are translucent and the line between marks is drawn under
  // them, so the tint sits on a ring of the page's own ground.
  return (
    <span
      className={cn(
        "flex shrink-0 bg-background",
        shape === "round" ? "rounded-full" : "rounded-xl",
        className,
      )}
    >
      <span
        className={cn(
          "flex shrink-0 items-center justify-center rounded-[inherit] [&>img]:object-contain",
          size === "lg" && "size-14 [&>img]:size-6 [&>svg]:size-6",
          size === "md" && "size-11 [&>img]:size-5 [&>svg]:size-5",
          size === "sm" && "size-9 [&>img]:size-4 [&>svg]:size-4",
          tone === "neutral" && "border border-hairline bg-control text-foreground",
          tone === "brand" && "bg-brand text-brand-foreground",
          tone === "ink" && "bg-foreground text-background",
          tone === "logo" && "border border-hairline bg-background text-muted-foreground",
          tone === "success" && "bg-plot-success text-success",
          tone === "danger" && "bg-plot-danger text-destructive",
          tone === "warning" && "bg-plot-warning text-warning",
        )}
      >
        {children}
      </span>
    </span>
  )
}

// A kind is drawn at the size it will have in a WireMark; a bare + is smaller.
const KIND_SIZE = {
  lg: "[&>img]:size-6 [&>svg]:size-6",
  md: "[&>img]:size-5 [&>svg]:size-5",
  sm: "[&>img]:size-4 [&>svg]:size-4",
} as const
const GLYPH_SIZE = { lg: "[&>svg]:size-5", md: "[&>svg]:size-4", sm: "[&>svg]:size-3.5" } as const

/**
 * A mark that is not there yet: a dashed ring where the thing will go.
 *
 * Given the `product` it will be — or the `fallback` glyph of a kind no
 * product names — the ring holds that mark faded and grey, at the size it will
 * have once it exists, with a + in its corner: "add Slack" is read before a
 * word is, where a ring holding only a + said "add something". The dashed
 * line and the + still say it is not added; `WireLink` brings its colour back
 * under the pointer.
 */
export function WirePlaceholder({
  size = "lg",
  product,
  fallback: Fallback,
  className,
  children,
}: {
  size?: "lg" | "md" | "sm"
  product?: string
  fallback?: Icon
  className?: string
  children?: React.ReactNode
}) {
  const kind = hasProductLogo(product) ? (
    <ProductGlyph id={product} className="opacity-60 grayscale transition-[filter,opacity]" />
  ) : Fallback ? (
    <Fallback className="opacity-60" />
  ) : undefined
  return (
    <span
      className={cn(
        "relative flex shrink-0 items-center justify-center rounded-full border border-dashed border-border-strong bg-card text-muted-foreground",
        size === "lg" && "size-14",
        size === "md" && "size-11",
        size === "sm" && "size-9",
        kind ? KIND_SIZE[size] : GLYPH_SIZE[size],
        className,
      )}
    >
      {kind ?? children}
      {kind && (
        <span
          aria-hidden="true"
          className="absolute -right-0.5 -bottom-0.5 flex size-4 items-center justify-center rounded-sm border border-hairline bg-background"
        >
          <Plus className="size-2.5" />
        </span>
      )}
    </span>
  )
}

// Under the pointer a placeholder's faded product takes its colour back: the
// answer to hover is colour, never movement (§11).
const LINK =
  "rounded-full focus-ring transition-colors hover:text-foreground hover:[&_img]:opacity-100 hover:[&_img]:grayscale-0 focus-visible:[&_img]:opacity-100 focus-visible:[&_img]:grayscale-0"

/** A wrapper that is a link when there is somewhere to go. */
export function WireLink({
  href,
  label,
  onClick,
  children,
}: {
  href?: string
  label: string
  onClick?: () => void
  children: React.ReactNode
}) {
  if (onClick)
    return (
      <button type="button" onClick={onClick} aria-label={label} className={LINK}>
        {children}
      </button>
    )
  if (!href) return <>{children}</>
  return (
    <a href={href} target="_blank" rel="noreferrer" aria-label={label} className={LINK}>
      {children}
    </a>
  )
}

/** A caption written along a line, above or below it. */
export function WireLabel({
  lit,
  className,
  children,
}: {
  lit: boolean
  className?: string
  children: React.ReactNode
}) {
  return (
    <p
      className={cn(
        "absolute inset-x-0 text-center text-micro font-medium tracking-[0.06em] whitespace-nowrap uppercase",
        lit ? "text-brand" : "text-muted-foreground",
        className,
      )}
    >
      {children}
    </p>
  )
}
