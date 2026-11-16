import { Fragment } from "react"
import { cn } from "@/lib/utils"
import { monoBreaks } from "@/components/wire/mono-breaks"

/**
 * A mono value — a path, an address, a unit's name — that wraps only at its
 * separators: a `<wbr>` after each `/`, `.` and `:` (`monoBreaks`), and the
 * browser's own break after a hyphen. `break-all` cut "links.batch" as
 * "link|s.batch" and an IPv6 address mid-group; this keeps every word whole.
 * A single word longer than the line (a digest) still breaks rather than
 * spilling, as the last resort `break-words` allows.
 *
 * Copying the value copies it whole: a `<wbr>` is not a character.
 */
export function MonoValue({
  value,
  className,
  ...props
}: { value: string; className?: string } & Omit<React.ComponentProps<"span">, "children">) {
  const pieces = monoBreaks(value)
  return (
    <span className={cn("font-mono break-words", className)} {...props}>
      {pieces.map((piece, index) => (
        <Fragment key={index}>
          {index > 0 && <wbr />}
          {piece}
        </Fragment>
      ))}
    </span>
  )
}
