import { Slot } from "radix-ui"

import { cn } from "@/lib/utils"
import type { Tone } from "@/components/tone"

/**
 * A tone colours the word. It has nothing to fill and nothing to outline,
 * because the container is what this component stopped drawing.
 */
const TONE: Record<Tone, string> = {
  // A step below the muted text it sits beside. Small caps at 10px have the
  // same cap height as 13px lower-case has x-height, so at equal contrast an
  // untinted tag reads as loud as the title it is annotating. The tinted three
  // stay at full strength — a tag that has taken a tone is saying something.
  default: "text-muted-foreground/80",
  success: "text-success",
  warning: "text-warning",
  danger: "text-destructive",
}

/**
 * The mark for a fixed property of the row it sits in: `ed25519`, `read-only`,
 * `self-signed`, `security`.
 *
 * It is a word, and it is drawn as one. The chip is gone: small caps at 10px
 * inside a hairline box with its own padding is a container three times the
 * height of the word it holds, and beside a 13px title — which is where most
 * of the 117 of these sit — the box read as the louder of the two. Every table
 * with one in each row was a column of empty rectangles with a syllable in the
 * middle.
 *
 * What is left does the same job with type alone: small caps, tracked out, a
 * step down in size and a step back in colour. Beside a title it reads as an
 * aside; in a column it recedes to a texture; and a tinted one still catches
 * the eye, which is the only thing the border was contributing.
 *
 * `mono` is the exception, and the reason it is one is that its contents are
 * not words. A cipher suite, a port map, a config hash and a dependency name
 * are literal strings from the host, they run together when several sit in a
 * row, and small caps would corrupt them. Those keep a quiet recessed ground —
 * no border, no uppercase — so a run of them is still parseable as a list.
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
        "inline-flex w-fit shrink-0 items-center gap-1 text-micro leading-[1.5] font-medium whitespace-nowrap",
        mono ? "rounded-sm bg-surface-sunken px-1 py-px font-mono" : "tracking-[0.06em] uppercase",
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
