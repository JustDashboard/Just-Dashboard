import { cn } from "@/lib/utils"

/**
 * What the DNS page draws where a reading has not landed: the same boxes its
 * words and marks will stand in, in grey. The page used to wait on one panel
 * and then arrive whole, 4,800px at once; each block now waits at the height
 * it will have, so the first read moves nothing.
 *
 * Every piece carries `data-slot="skeleton"`, the mark a spec waits on.
 */

/**
 * A grey bar on a line of text. It sits inside the line box, so the line keeps
 * the height its words will have.
 */
export function Bar({ className }: { className?: string }) {
  return (
    <span
      data-slot="skeleton"
      className={cn(
        "inline-block h-[0.7em] animate-pulse rounded-sm bg-muted align-middle",
        className,
      )}
    />
  )
}

/** A mark's place, at the size a `WireMark` of that size takes. */
export function GhostMark({ size }: { size: "lg" | "md" | "sm" }) {
  return (
    <span
      data-slot="skeleton"
      className={cn(
        "flex shrink-0 animate-pulse rounded-xl bg-muted",
        size === "lg" ? "size-14" : size === "md" ? "size-11" : "size-9",
      )}
    />
  )
}

/** Cards where a `ChoiceList` will be, each the height of a `ChoiceRow` (its 56px and its edge). */
export function GhostRows({ rows, className }: { rows: number; className?: string }) {
  return (
    <ul aria-hidden className={cn("min-w-0 space-y-2", className)}>
      {Array.from({ length: rows }, (_, i) => (
        <li
          key={i}
          className="flex h-[58px] min-w-0 items-center gap-3 rounded-xl border bg-choice-surface px-3"
        >
          <span
            data-slot="skeleton"
            className="size-8 shrink-0 animate-pulse rounded-lg bg-muted"
          />
          <span className="min-w-0 flex-1 text-body">
            <Bar className="w-32" />
          </span>
          <span className="text-body max-sm:hidden">
            <Bar className="w-24" />
          </span>
        </li>
      ))}
    </ul>
  )
}
