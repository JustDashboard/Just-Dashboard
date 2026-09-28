import { cn } from "@/lib/utils"

/** How one attempt ended, in the only hues a reading of state may take (§3). */
export type OutcomeTone = "success" | "danger" | "warning" | "running" | "muted"

export type Outcome = {
  key: string
  tone: OutcomeTone
  /** What this one was, said by the square's own title: "failed · 3h ago". */
  title: string
}

/** Each outcome's fill, for a surface that places the same marks on a timeline. */
export const OUTCOME_FILL: Record<OutcomeTone, string> = {
  success: "bg-success/80",
  danger: "bg-destructive",
  warning: "bg-warning",
  // The brand, not a status hue: an attempt still going has no outcome yet,
  // and the blue is what this product draws "happening now" in.
  running: "bg-brand",
  // An attempt that ended without an outcome — cancelled, superseded,
  // stopped. Present, so the count is honest, and quiet, so it never reads as
  // either of the two answers the strip is scanned for.
  muted: "bg-muted-foreground/40",
}

/**
 * The last few attempts at something, oldest first: a square per attempt in
 * the colour of how it ended.
 *
 * Backups drew this for a job's fourteen runs, and the deploy section then
 * wanted it for a project's runs, a channel's deliveries, a webhook's and a
 * schedule's firings — five strips that would have come apart in five ways.
 * One now, so a job that has been failing every other night and a channel
 * that has been dropping every other message read the same way, before either
 * name is read.
 *
 * Squares with a radius of their own, not dots: a row of filled circles reads
 * as a row of pills (§4). No tooltip machinery for fourteen marks — each says
 * what it was in its title — and the strip as a whole is one image whose
 * `label` the caller writes, because only the caller knows which of its
 * outcomes is the one worth counting aloud ("Last 14 runs: 2 failed").
 */
export function OutcomeStrip({ items, label }: { items: Outcome[]; label: string }) {
  if (items.length === 0) return null
  return (
    <span role="img" aria-label={label} className="flex shrink-0 items-center gap-0.5">
      {items.map((item) => (
        <span
          key={item.key}
          title={item.title}
          className={cn("h-3 w-1.5 rounded-sm", OUTCOME_FILL[item.tone])}
        />
      ))}
    </span>
  )
}
