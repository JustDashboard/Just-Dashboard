import { cn } from "@/lib/utils"
import {
  DOT_TONE,
  ICON_TONE,
  TEXT_TONE,
  VERDICT_TONE,
  toneFor,
  type DotTone,
  type Verdict,
} from "@/components/tone"

// The vocabulary lives in `components/tone.ts`; these names stay importable
// from here because four hundred files reach them through this one.
export { DOT_COLOR, toneFor, type DotTone, type Verdict } from "@/components/tone"

/**
 * The ring a dot on a mark sits in, and the dot inside it, per size of mark:
 * `sm` for a 28–32px tile (the 41 hand copies of this were all this one),
 * `md` for a row's 36px tile, `lg` for an identity header's 48px one. The ring
 * is the ground the mark sits on, so the dot reads as cut out of the tile's
 * corner rather than stuck on it.
 */
const CORNER = {
  sm: { ring: "size-2.5", dot: "size-1.5" },
  md: { ring: "size-3", dot: "size-2" },
  lg: { ring: "size-4", dot: "size-2" },
} as const

export type CornerSize = keyof typeof CORNER

/**
 * The state as a dot: six pixels of colour and nothing else.
 *
 * `corner` places it on a mark's bottom-right, in a ring of the ground, the
 * way a unit's or a container's tile carries its state: the mark is the
 * positioned parent (`relative`). `label` says the state in words for a
 * screen reader where the row does not already say it — the dirty dot on a
 * view link, a map's system dot — and is left out where the word is beside
 * it, which would say it twice.
 */
export function StatusDot({
  state,
  tone,
  live,
  corner,
  label,
  className,
  ...props
}: Omit<React.ComponentProps<"span">, "children"> & {
  state?: string
  tone?: DotTone
  /**
   * This reading is arriving, not remembered.
   *
   * A halo that breathes out of the dot and fades, once every couple of
   * seconds. It is deliberately not `animate-pulse` — that dims the dot itself,
   * which on a table of twenty running containers reads as twenty things
   * blinking for attention. Here the dot is constant and only the air around it
   * moves, so a glance at the column still reads "all green" and a longer look
   * reads "and it is live".
   *
   * Reserved for a row fed by an open socket. A polled figure is not live, and
   * saying it is would be the interface lying about how fresh its numbers are.
   */
  live?: boolean
  /** On a mark's bottom-right corner; `true` is the small mark's size. */
  corner?: boolean | CornerSize
  /** The state in words, for a screen reader, where the row does not say it. */
  label?: string
}) {
  const fill = DOT_TONE[tone ?? toneFor(state)]
  const size = corner ? CORNER[corner === true ? "sm" : corner] : undefined
  const words = label ? <span className="sr-only">{label}</span> : null
  // The dot alone is one element: most callers place it in a flex line that
  // spaces its children, and a wrapper would be one more of them. It carries
  // the slot itself unless something wraps it.
  const bare = !size && !words && Object.keys(props).length === 0
  const slot = bare ? "status-dot" : undefined
  const sized = cn(size?.dot, !size && className)
  const dot = !live ? (
    <span
      data-slot={slot}
      aria-hidden
      className={cn("size-1.5 shrink-0 rounded-full", fill, sized)}
    />
  ) : (
    <span data-slot={slot} aria-hidden className={cn("relative flex size-1.5 shrink-0", sized)}>
      <span className={cn("absolute inset-0 animate-breathe rounded-full", fill)} />
      <span className={cn("relative size-full rounded-full", fill)} />
    </span>
  )
  if (bare) return dot
  if (size) {
    // The words sit beside the ring, not in it: a filled round shape with
    // text inside is the pill the product does not draw, to a reader and to
    // design-system.spec alike.
    return (
      <>
        <span
          data-slot="status-dot"
          aria-hidden
          className={cn(
            "absolute -right-0.5 -bottom-0.5 flex items-center justify-center rounded-full bg-background",
            size.ring,
            className,
          )}
          {...props}
        >
          {dot}
        </span>
        {words}
      </>
    )
  }
  return (
    <span data-slot="status-dot" className="inline-flex shrink-0" {...props}>
      {dot}
      {words}
    </span>
  )
}

/**
 * The one status indicator in the app: a coloured dot (or a caller-supplied
 * icon) and a label, and nothing else — no border, no filled pill.
 *
 * A pill turns every status into a small stamped object competing for
 * attention; a dot and a word reads as a fact about the row it sits in. It is
 * reached either through a raw state string — `toneFor` reads Docker's,
 * systemd's and pm2's vocabularies — or through a `verdict`, for the health
 * and security posture pages, whose four-value severity scale doesn't fit any
 * of those vocabularies.
 */
export function Status({
  state,
  verdict,
  tone: given,
  label,
  live,
  announce,
  icon: Icon,
  className,
  ...props
}: Omit<React.ComponentProps<"span">, "children"> & {
  state?: string
  verdict?: Verdict
  /**
   * The tone directly, for the callers that already hold one — a table of
   * stacks whose six states map to tones the string vocabularies don't cover.
   */
  tone?: DotTone
  label?: React.ReactNode
  /** Passed to the dot — see `StatusDot`. Ignored when an icon is given. */
  live?: boolean
  /**
   * Say the word aloud when it changes: the state of the one thing a page is
   * about — a container's, a run's, a tunnel's — in a polite live region, so
   * a screen reader hears "stopped" when it stops, once, without being sent
   * to look. A string names what the state is of ("Container state"), and is
   * spoken before the word.
   *
   * Only for a page's own subject. A list of twenty rows each announcing its
   * own change is noise, and a row's state is found by reading the row.
   */
  announce?: boolean | string
  icon?: React.ComponentType<{ className?: string }>
  className?: string
}) {
  const tone = given ?? (verdict ? VERDICT_TONE[verdict] : toneFor(state))
  const word = label ?? state ?? "unknown"
  return (
    <span
      data-slot="status"
      className={cn(
        "inline-flex items-center gap-1.5 text-xs font-medium whitespace-nowrap",
        className,
      )}
      {...props}
    >
      {Icon ? (
        <Icon className={cn("size-3.5 shrink-0", ICON_TONE[tone])} />
      ) : (
        <StatusDot tone={tone} live={live} />
      )}
      {/* With `announce` the word a screen reader reads is the live one, so
          the drawn word is hidden from it rather than read twice. */}
      <span className={TEXT_TONE[tone]} aria-hidden={announce ? true : undefined}>
        {word}
      </span>
      {announce && (
        <StatusWords subject={typeof announce === "string" ? announce : undefined}>
          {word}
        </StatusWords>
      )}
    </span>
  )
}

/**
 * A state in words, in a polite, atomic live region that is not drawn: what
 * `Status announce` speaks, for a caller whose state is not a `Status` — a
 * run's header, a job's console. It is in the page from the first render, so
 * the state it opens with is not announced and each change after is, once.
 */
export function StatusWords({
  subject,
  children,
}: {
  /** What the state is of, spoken first: "Deployment state". */
  subject?: string
  children: React.ReactNode
}) {
  return (
    <span className="sr-only" aria-live="polite" aria-atomic="true">
      {subject ? (
        <>
          {subject}: {children}
        </>
      ) : (
        children
      )}
    </span>
  )
}
