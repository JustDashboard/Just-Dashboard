"use client"

import { errorMessage } from "@/lib/api"
import { asSentence, dateFormatter, duration } from "@/lib/format"
import { cn } from "@/lib/utils"
import { useNow } from "@/hooks/use-now"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"

/**
 * A reading that is the one before: the read after it failed, and what is
 * drawn stays on screen rather than giving way to a red box. One word for it,
 * "Not updating", where the product had four ("Not read again", "Not
 * updating", a `Stale`, a hand-typed strip) — and the reason in the server's
 * words behind it.
 *
 * - `form="mark"` (the default) is the word alone, for a header's end: the
 *   block's title stays, and the reason is the mark's title.
 * - `form="strip"` is a hairline strip across the top of a pane whose content
 *   is the last reading — a rail's tree, a table's facts — with the word, what
 *   the content is the last reading of (`subject`) and Try again.
 *
 * Nothing is drawn without an `error`, so a caller passes the poll's error
 * only while it holds data (`staleOf(poll)`).
 */
export function Stale({
  error,
  form = "mark",
  subject,
  onRetry,
  className,
}: {
  error?: Error
  form?: "mark" | "strip"
  /** What the content is the last reading of: "orders", "the tables". */
  subject?: string
  onRetry?: () => void
  className?: string
}) {
  if (!error) return null
  const why = errorMessage(error)
  if (form === "mark") {
    return (
      // A flex box: a bare span keeps a 16px line box and sets the status a
      // pixel under the centre line of the header it sits in.
      <span data-slot="stale" data-form="mark" className={cn("flex", className)} title={why}>
        <Status verdict="warning" label="Not updating" />
      </span>
    )
  }
  return (
    <div
      role="status"
      data-slot="stale"
      data-form="strip"
      className={cn(
        "flex shrink-0 items-center gap-2 border-b border-hairline py-1 pr-1.5 pl-3",
        className,
      )}
    >
      <Status verdict="warning" label="Not updating" className="shrink-0" />
      <span className="min-w-0 flex-1 truncate text-hint text-muted-foreground" title={why}>
        {subject ? `last reading of ${subject}` : "last reading"}
      </span>
      {onRetry && (
        <Button size="xs" variant="ghost" className="shrink-0" onClick={onRetry}>
          Try again
        </Button>
      )}
    </div>
  )
}

const fullTime = dateFormatter({ dateStyle: "medium", timeStyle: "medium" })

/** "just now", "4 min ago": the distance from `now`, for a time that was a moment ago. */
export function agoFrom(then: number, now: number): string {
  const seconds = (now - then) / 1000
  if (seconds < 45) return "just now"
  return `${duration(seconds)} ago`
}

/**
 * A moment ago, kept true while it is on screen. Its own component so that its
 * clock starts when the banner appears, not when the page did.
 */
function Ago({ then }: { then: number }) {
  const now = useNow(15_000)
  return (
    <time dateTime={new Date(then).toISOString()} title={fullTime(then)}>
      {agoFrom(then, Math.max(now, then))}
    </time>
  )
}

/**
 * The banner for a page or section whose readings are the last known ones: the
 * poll after them failed. It says which readings (`reading`, after "Showing
 * the last known"), when they were last read, why the latest read failed, in
 * the server's words as a sentence, and one verb to ask again.
 *
 * It is the Network section's read warning made general; Docker used it too,
 * and Backups and Proxy wrote the same banner by hand. The time is how long
 * ago, advancing while it stands, with the date in its `title`: a full locale
 * timestamp asked the reader to subtract.
 *
 * Nothing is drawn without an `error`.
 */
export function StaleNotice({
  error,
  refresh,
  lastSuccess,
  reading,
  verb = "Refresh",
  className,
}: {
  error?: Error
  refresh: () => void
  /** When the readings shown were read, in epoch milliseconds. */
  lastSuccess?: number
  /** What is shown: "network state", "packet captures". */
  reading: string
  verb?: string
  className?: string
}) {
  if (!error) return null
  return (
    <Notice
      live="alert"
      tone="warning"
      title={`Showing the last known ${reading}`}
      data-slot="stale-notice"
      className={className}
    >
      {lastSuccess && (
        <p>
          Last successful read: <Ago then={lastSuccess} />
        </p>
      )}
      <p className="wrap-anywhere">{asSentence(errorMessage(error))}</p>
      <Button size="xs" variant="outline" className="mt-2" onClick={refresh}>
        {verb}
      </Button>
    </Notice>
  )
}
