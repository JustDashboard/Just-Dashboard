import { cn } from "@/lib/utils"
import { VERSION } from "@/lib/version"

/**
 * The wordmark, which is the entire logo.
 *
 * There is no mark, no glyph and no tile. A single-server panel is opened by
 * the person who installed it, on their own network, and a symbol in front of
 * the name is a brand asset for a product with strangers to introduce itself
 * to — here it only spent the width the name could have had. "Just" takes the
 * theme's accent and "Dashboard" the text colour, so the logo is recoloured by
 * the palette like everything else rather than sitting in the corner as the
 * one fixed-colour object on screen.
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
    <span className={cn("flex min-w-0 items-baseline gap-1.5", className)}>
      <span
        className={cn(
          "truncate leading-tight font-semibold tracking-tight",
          size === "sm" && "text-mark-sm",
          size === "md" && "text-mark-md",
          size === "lg" && "text-mark-lg",
        )}
      >
        <span className="text-brand">Just</span> Dashboard
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
 * What is left of the wordmark when there is no room for it — the collapsed
 * sidebar rail is three rem wide. It is the first letter of the name in the
 * accent, not a symbol: a rail that suddenly showed a glyph the expanded
 * sidebar never does would be a second logo to recognise.
 */
export function LogoMark({ className }: { className?: string }) {
  return (
    <span className={cn("text-mark-md leading-none font-semibold text-brand", className)}>J</span>
  )
}
