import Link from "next/link"
import { cn } from "@/lib/utils"

/**
 * One reading as a sentence on a mark: a tinted glyph, what the reading is,
 * what it says in words, a line of detail, and its last hour at the line's
 * end. It is what a row of `StatTile`s becomes when the figures are better
 * said than shown: "21.4 requests a minute" under "Traffic", where a tile
 * would put "21.4" over "/min" and leave the reader to join them.
 *
 * A project's Overview drew these first (0.8.5), and they lived in the deploy
 * section until every page that still opens on a tile row needed them. Every
 * state of a line is one height, so a reading landing moves nothing under it:
 * with nothing to say yet, say so in `NoReading` rather than leave a part out.
 *
 * With `href` the line is a way to the page that has the rest, and `label` is
 * where it goes. Without one it is a reading and nothing else, so it takes no
 * hover wash and no place in the tab order.
 */
export function ReadingLine({
  href,
  label,
  mark,
  name,
  children,
  hint,
  trend,
  className,
}: {
  href?: string
  /** Where the line goes, as its accessible name. Only read with `href`. */
  label?: string
  /** A `WireMark`, on the measure's own hue or the reading's tone. */
  mark: React.ReactNode
  name: React.ReactNode
  children: React.ReactNode
  hint?: React.ReactNode
  /** The reading's last hour, a `TileTrend`. Hidden below `sm`. */
  trend?: React.ReactNode
  className?: string
}) {
  const body = (
    <>
      {mark}
      <span className="block min-w-0 flex-1">
        <span className="eyebrow flex items-center gap-1.5">{name}</span>
        <span className="block truncate text-body leading-snug font-medium">{children}</span>
        {/* The line's third row holds its place when there is no detail, so
            a line with a hint and one without are the same height. */}
        <span className="block truncate text-hint leading-snug text-muted-foreground">
          {hint ?? " "}
        </span>
      </span>
      <span className="hidden w-32 shrink-0 sm:block 2xl:w-48">{trend}</span>
    </>
  )
  const shape = "flex min-w-0 items-center gap-3.5 rounded-lg px-2 py-2.5"
  if (!href)
    return (
      <div data-slot="reading-line" className={cn(shape, className)}>
        {body}
      </div>
    )
  return (
    <Link
      href={href}
      aria-label={label}
      data-slot="reading-line"
      className={cn(shape, "focus-ring transition-colors hover:bg-row-hover", className)}
    >
      {body}
    </Link>
  )
}

/** A reading that has not arrived, in the reading's own place and height. */
export function NoReading({ children }: { children: React.ReactNode }) {
  return <span className="text-muted-foreground">{children}</span>
}

/**
 * A run of reading lines: two columns from `xl`, out by the lines' own inset
 * so their marks stand on the page's edge and only the hover wash passes it.
 */
export function ReadingLines({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="reading-lines"
      className={cn("-mx-2 grid min-w-0 gap-x-6 xl:grid-cols-2", className)}
      {...props}
    />
  )
}
