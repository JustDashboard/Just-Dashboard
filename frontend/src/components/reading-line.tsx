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
 * where it goes. With `onPress` it is a button that does something on the page
 * it is on (a log lens's reading narrows the log to the lines it counts), and
 * `label` is what the press does. The button is laid over the line rather than
 * wrapped around it, since a mark and a sparkline are not what a button may
 * hold; a page used to lay its own. Without either it is a reading and nothing
 * else, so it takes no hover wash and no place in the tab order.
 *
 * `BarReading`'s line under a band is another thing with its own slot
 * (`bar-reading-line`): a spec that counts the one must not count the other.
 */
/**
 * A reading line's height, for a cell that stands in a run of them without
 * being one — the Overview's one-disk storage band. 10 + 14 + 17.875 + 15.125
 * + 10: the padding, the eyebrow's micro line, the reading and its detail. The
 * band had it as `min-h-17`, the 68 the lines were while the eyebrow sat on a
 * 15px line; when the micro step took its own 14px line the lines became 67
 * and the band did not, so a server's sixth cell grew a pixel when it landed.
 */
export const readingLineHeight = "min-h-[4.1875rem]"

export function ReadingLine({
  href,
  onPress,
  label,
  mark,
  name,
  children,
  hint,
  trend,
  className,
}: {
  href?: string
  /** The line is a button that does this. Not read with `href`. */
  onPress?: () => void
  /** Where the line goes or what a press does, as its accessible name. */
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
  const shape = cn("flex min-w-0 items-center gap-3.5 rounded-lg px-2 py-2.5", readingLineHeight)
  if (!href && onPress)
    return (
      <div
        data-slot="reading-line"
        className={cn(shape, "relative transition-colors hover:bg-row-hover", className)}
      >
        {body}
        <button
          type="button"
          onClick={onPress}
          aria-label={label}
          className="absolute inset-0 rounded-lg focus-ring"
        />
      </div>
    )
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
