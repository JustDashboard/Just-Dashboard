import { cn } from "@/lib/utils"
import { nameHue } from "@/components/database/home/kinds"

/**
 * A container's own colour, stable for its name, as a small square before it:
 * a schema of a SQL database, a MongoDB database, wherever its name is drawn.
 *
 * Six files drew this square, three through `hueFor` and three through
 * `nameHue`, which are the same hue (`nameHue` is `hueFor` of the lower-cased
 * name in the lanes). One mark means a schema is the same colour in the rail's
 * picker, on the diagram's table cards and in the search's scope chips.
 *
 * With no name (no database chosen yet) it is drawn in the muted ink, so the
 * trigger it heads keeps its shape.
 */
export function NameMark({ name, className }: { name?: string; className?: string }) {
  return (
    <span
      aria-hidden
      data-slot="name-mark"
      className={cn("size-2 shrink-0 rounded-sm", className)}
      style={{ background: name ? nameHue(name) : "var(--muted-foreground)" }}
    />
  )
}
