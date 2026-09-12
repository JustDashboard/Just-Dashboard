import { Slot } from "radix-ui"

import { cn } from "@/lib/utils"
import type { Tone } from "@/components/tone"

/**
 * Border and text only. A tone is allowed to colour the word and the rule
 * around it; it is never allowed to fill a shape, because a filled shape is
 * the thing this component exists to replace.
 */
const TONE: Record<Tone, string> = {
  default: "border-hairline text-muted-foreground",
  success: "border-rule-success text-success",
  warning: "border-rule-warning text-warning",
  danger: "border-rule-danger text-destructive",
}

/**
 * The mark for a fixed property of the row it sits in: `ed25519`, `read-only`,
 * `self-signed`, `security`.
 *
 * The product used to spend a `Badge` on this — a filled, fully rounded-sm pill —
 * and a table with one in every row read as a sheet of stickers rather than as
 * data. The shape here is the one the product had already settled on in
 * `PanelHeader`'s `advanced` note: a squared hairline chip, small caps, quiet
 * enough that a column of them recedes and a single tinted one does not.
 *
 * Three things it is deliberately not:
 *
 *   A status. A live running/stopped/failed state, or a health verdict, is
 *   `Status` in `components/status-dot.tsx` — a dot and a word. Using a tag for
 *   state puts two vocabularies on screen for one idea.
 *
 *   A count. `12 lines`, `3 changes`, `5 selected` are numbers, and a number
 *   belongs in `.numeric` text next to its label, not in a container.
 *
 *   A sentence. Small caps stop being legible somewhere around three words. If
 *   the thing to say is "answers 200 without redirecting", it is prose and
 *   belongs in the row's secondary line or in a `Notice`.
 */
export function Tag({
  tone = "default",
  mono,
  icon: Icon,
  asChild,
  className,
  children,
  ...props
}: React.ComponentProps<"span"> & {
  tone?: Tone
  /** For values that are literal strings from the host — a cipher, a port map. */
  mono?: boolean
  icon?: React.ComponentType<{ className?: string }>
  /** For the handful of tags that are also links — a published port. */
  asChild?: boolean
}) {
  // `asChild` hands the classes to the caller's own element — Slot takes a
  // single child, so the icon prop is not available on that path and the caller
  // composes its own contents.
  const Comp = asChild ? Slot.Root : "span"
  return (
    <Comp
      data-slot="tag"
      className={cn(
        "inline-flex w-fit shrink-0 items-center gap-1 rounded-sm border px-1.5 py-px text-micro leading-[1.5] font-medium whitespace-nowrap",
        mono ? "font-mono" : "tracking-[0.08em] uppercase",
        TONE[tone],
        className,
      )}
      {...props}
    >
      {asChild ? (
        children
      ) : (
        <>
          {Icon && <Icon className="size-2.5 shrink-0" />}
          {children}
        </>
      )}
    </Comp>
  )
}
