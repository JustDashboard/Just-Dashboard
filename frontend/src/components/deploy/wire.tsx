"use client"

import type { RefObject } from "react"
import { cn } from "@/lib/utils"

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
 */
export function WireNode({
  nodeRef,
  mark,
  eyebrow,
  title,
  hint,
  align = "start",
  className,
}: {
  nodeRef?: RefObject<HTMLDivElement | null>
  mark: React.ReactNode
  eyebrow?: React.ReactNode
  title: React.ReactNode
  hint?: React.ReactNode
  align?: "start" | "center" | "end"
  className?: string
}) {
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
      <div
        className={cn(
          "min-w-0",
          align === "center" &&
            "lg:absolute lg:top-full lg:left-1/2 lg:mt-3 lg:w-44 lg:-translate-x-1/2 lg:text-center",
        )}
      >
        {eyebrow && <p className="eyebrow">{eyebrow}</p>}
        <div className="text-body leading-snug font-medium break-words">{title}</div>
        {hint && <div className="text-hint leading-snug text-muted-foreground">{hint}</div>}
      </div>
    </div>
  )
}

/** A filled mark: the thing exists. */
export function WireMark({
  tone = "neutral",
  size = "lg",
  shape = "round",
  className,
  children,
}: {
  /** neutral is a step of ground; brand is this product; ink is a third party's own mark. */
  tone?: "neutral" | "brand" | "ink" | "success" | "danger" | "warning"
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
          "flex shrink-0 items-center justify-center rounded-[inherit]",
          size === "lg" && "size-14 [&>svg]:size-6",
          size === "md" && "size-11 [&>svg]:size-5",
          size === "sm" && "size-9 [&>svg]:size-4",
          tone === "neutral" && "border border-hairline bg-control text-foreground",
          tone === "brand" && "bg-brand text-brand-foreground",
          tone === "ink" && "bg-foreground text-background",
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

/** A mark that is not there yet: a dashed ring where the thing will go. */
export function WirePlaceholder({
  size = "lg",
  className,
  children,
}: {
  size?: "lg" | "md" | "sm"
  className?: string
  children: React.ReactNode
}) {
  return (
    <span
      className={cn(
        "flex shrink-0 items-center justify-center rounded-full border border-dashed border-border-strong bg-card text-muted-foreground",
        size === "lg" && "size-14 [&>svg]:size-5",
        size === "md" && "size-11 [&>svg]:size-4",
        size === "sm" && "size-9 [&>svg]:size-3.5",
        className,
      )}
    >
      {children}
    </span>
  )
}

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
      <button
        type="button"
        onClick={onClick}
        aria-label={label}
        className="rounded-full focus-ring transition-colors hover:text-foreground"
      >
        {children}
      </button>
    )
  if (!href) return <>{children}</>
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      aria-label={label}
      className="rounded-full focus-ring transition-colors hover:text-foreground"
    >
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
