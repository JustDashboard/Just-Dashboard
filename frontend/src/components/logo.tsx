import { cn } from "@/lib/utils"
import { VERSION } from "@/lib/version"

/**
 * The mark: a J drawn as a stencil — a slanted tittle over a stem that turns
 * into the tick of a checkmark at the foot.
 *
 * It is `currentColor` rather than the orange it was drawn in, so the one place
 * the mark's colour is named is `--brand` in the palette and the glyph tints
 * with whatever it is placed inside. The two subpaths are separate rather than
 * one shape with a hole: the gap between tittle and stem is the background
 * showing through, not a counter, and it has to stay transparent over a card,
 * over the sidebar and over the sign-in wash alike.
 *
 * `label` is what the glyph stands in for in the accessible name, because it
 * stands in for a *word* rather than decorating one — "Just" beside the typeset
 * "Dashboard", the whole name where the mark appears alone. Without it the
 * sidebar's home link announces half the product's name.
 */
export function LogoGlyph({ label, className }: { label?: string; className?: string }) {
  return (
    <svg
      viewBox="0 0 481 600"
      fill="currentColor"
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      focusable="false"
      className={className}
    >
      <path d="M481 0v116.7L288.7 225.6V107.8Z" />
      <path d="M481 142.8v278L233.6 600 0 381.8h164.9l86.6 75.3 37.3-29.3V251.8Z" />
    </svg>
  )
}

/**
 * The logo: the mark, then the word.
 *
 * "Just" used to be set in type beside "Dashboard" and coloured with the accent
 * — a wordmark standing in for a mark the product did not have. It has one now,
 * and the mark *is* the J, so setting the letter again next to it would say the
 * same thing twice. What is left reads as the name it always was: the glyph
 * carries "Just", the type carries "Dashboard".
 *
 * The version rides beside it because this is software an operator upgrades by
 * pulling and rebuilding: "which one am I looking at" is a real question, and
 * the answer belongs where they already look rather than on a page nobody
 * visits.
 */
export function Logo({
  size = "sm",
  version = true,
  className,
}: {
  size?: "sm" | "md" | "lg"
  /** Set false where the version would be noise — a splash, a narrow strip. */
  version?: boolean
  className?: string
}) {
  return (
    <span className={cn("flex min-w-0 items-center gap-2", className)}>
      <LogoGlyph
        label="Just"
        className={cn(
          "w-auto shrink-0 text-brand",
          size === "sm" && "h-[1.15rem]",
          size === "md" && "h-[1.3rem]",
          size === "lg" && "h-[1.7rem]",
        )}
      />
      <span
        className={cn(
          "truncate leading-tight font-semibold tracking-tight",
          size === "sm" && "text-mark-sm",
          size === "md" && "text-mark-md",
          size === "lg" && "text-mark-lg",
        )}
      >
        Dashboard
      </span>
      {version && <LogoVersion />}
    </span>
  )
}

/**
 * The version, as small text beside the name and nothing else. No chip, no
 * border, no fill: a badge would give the number a frame the wordmark itself
 * does not have, which reads as the more important of the two. It is a
 * footnote to the name and should look like one. Tabular digits so it does
 * not shift when 0.5 becomes 0.10.
 */
export function LogoVersion({ className }: { className?: string }) {
  return (
    <span className={cn("numeric shrink-0 text-hint text-muted-foreground", className)}>
      {VERSION}
    </span>
  )
}

/**
 * What is left of the logo when there is no room for it — the collapsed sidebar
 * rail is three rem wide. It is the mark alone, which is the whole point of
 * having one: the rail now shows the same object the expanded sidebar does
 * rather than a letter standing in for it.
 */
export function LogoMark({ className }: { className?: string }) {
  return (
    <LogoGlyph label="Just Dashboard" className={cn("h-[1.3rem] w-auto text-brand", className)} />
  )
}
