"use client"

import { useEffect, useRef } from "react"
import { Clock, Inbox, LoaderCircle, Question, Slash, Warning } from "@/components/icons"
import { ApiError } from "@/lib/api"
import { asSentence, plural, sentenceCase } from "@/lib/format"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { cn } from "@/lib/utils"
import type { Tone } from "@/components/tone"

export function Spinner({ className }: { className?: string }) {
  return <LoaderCircle aria-hidden className={cn("size-4 animate-spin", className)} />
}

export function LoadingRows({ rows = 5, className }: { rows?: number; className?: string }) {
  return (
    <div className={cn("space-y-2", className)}>
      {Array.from({ length: rows }).map((_, i) => (
        <Skeleton key={i} className="h-9 w-full rounded-md" />
      ))}
    </div>
  )
}

/**
 * The skeleton for a panel that is about to hold a table.
 *
 * Its first row is heavier than the rest so the placeholder has the same
 * silhouette as the thing arriving — a header strip over rows — rather than
 * an undifferentiated stack of grey bars that jumps when the data lands.
 *
 * `plain` is the same silhouette with no frame, for a page whose blocks arrive
 * as plain panels (§2): a framed box that gives way to unframed rows is the
 * jump this placeholder exists to prevent, one edge later. Its title and rows
 * start on the page's own edge, where the plain block's will.
 */
export function LoadingPanel({
  rows = 6,
  plain,
  className,
}: {
  rows?: number
  plain?: boolean
  className?: string
}) {
  const gutter = plain ? "px-0" : "px-5"
  return (
    <div
      className={cn("min-w-0 overflow-hidden", !plain && "rounded-xl border bg-card", className)}
    >
      <div className={cn("flex min-h-12 items-center border-b border-hairline py-3", gutter)}>
        <Skeleton className="h-4 w-40" />
      </div>
      <div className="divide-y divide-hairline">
        {Array.from({ length: rows }).map((_, i) => (
          <div key={i} className={cn("flex items-center gap-3 py-3.5", gutter)}>
            <Skeleton className="h-3.5 flex-1" style={{ maxWidth: `${34 + ((i * 13) % 26)}%` }} />
            <Skeleton className="h-3.5 w-16" />
            <Skeleton className="h-3.5 w-24" />
          </div>
        ))}
      </div>
    </div>
  )
}

/**
 * "Nothing here" for a region that is already inside something — a rail's list
 * of tables, a panel body under a filter, the saved-queries column.
 *
 * `EmptyState` would draw a dashed frame and an icon plot inside a box that
 * already has a frame, so those places had each written their own sentence
 * instead: eleven of them, at four different sizes and five different paddings.
 * One sentence, centred in the space it was given.
 */
export function EmptyNote({ className, ...props }: React.ComponentProps<"p">) {
  return (
    <p
      data-slot="empty-note"
      className={cn("px-3 py-6 text-center text-body text-muted-foreground", className)}
      {...props}
    />
  )
}

export function EmptyState({
  title,
  description,
  icon: Icon = Inbox,
  mark,
  action,
  className,
}: {
  title: string
  description?: React.ReactNode
  icon?: React.ComponentType<{ className?: string }>
  /**
   * What the list would hold, drawn as itself in place of the glyph on its
   * plate — the products a list of channels or credentials is built for, as
   * `ProductLogos` (§14). An empty list that shows the Discord, Slack and
   * Telegram marks says what goes in it before the sentence under it does.
   */
  mark?: React.ReactNode
  action?: React.ReactNode
  className?: string
}) {
  return (
    <div
      data-slot="empty-state"
      className={cn(
        "flex min-w-0 flex-col items-center justify-center gap-3 rounded-xl border border-dashed px-6 py-12 text-center",
        // A flow screen's focused surface is already the frame around it, and
        // a dashed one inside it is a frame in a frame.
        "in-data-[slot=flow-panel]:border-0",
        className,
      )}
    >
      {mark ?? (
        <span className="flex size-10 items-center justify-center rounded-xl border border-hairline bg-surface-header text-muted-foreground">
          <Icon className="size-4.5" />
        </span>
      )}
      <div className="space-y-1">
        <p className="text-body font-medium">{title}</p>
        {description && (
          <p className="mx-auto max-w-md text-xs leading-relaxed text-muted-foreground">
            {description}
          </p>
        )}
      </div>
      {action}
    </div>
  )
}

/** How long a throttled read waits to ask again when the answer did not say. */
const THROTTLE_WAIT_MS = 10_000
/**
 * Past what the server asked for: a page that polls asks again by itself the
 * moment the wait ends (`hooks/poll-loop.ts`), and a second request then would
 * be the one the limiter counts against it.
 */
const THROTTLE_GRACE_MS = 1_500

/**
 * Renders a fetch failure.
 *
 * A module that is simply not present on this host (no Docker socket, no
 * systemd, no fail2ban) is shown as information rather than an error, because
 * on a given machine that is a normal state and not something broken. It is
 * the same block either way so the page's shape does not change with the
 * severity — only its colour and its title do.
 *
 * Two more answers are things that happen, not things that broke, and are
 * drawn as calmly. An item that is not there (404): an old link to a project,
 * a job or a drawing that was since removed used to open on a red box reading
 * "not found". And a server that asked for a pause (429), which is the most
 * retryable answer there is: the box says so, offers Try again, and asks again
 * by itself when the wait the server named is over.
 *
 * The API words its messages to sit inside other text, lower case and without
 * a full stop. Here they stand alone, so the headline gets its capital and a
 * reason its full stop (`sentenceCase`, `asSentence`): once, in this component,
 * for every page that shows a failure.
 */
export function ErrorState({
  error,
  className,
  onRetry,
  retryable,
}: {
  error: Error
  className?: string
  /** Offered only when the server said the failure is worth trying again. */
  onRetry?: () => void
  /** Safe read operations can explicitly offer another attempt after transport failure. */
  retryable?: boolean
}) {
  const api = error instanceof ApiError ? error : undefined
  const unavailable =
    api?.code === "docker_unavailable" ||
    api?.code === "not_installed" ||
    api?.code === "no_proxy" ||
    api?.code === "terminal_disabled"
  const missing = !unavailable && api?.status === 404
  const throttled = !unavailable && api?.status === 429
  const kind = unavailable
    ? "unavailable"
    : missing
      ? "missing"
      : throttled
        ? "throttled"
        : "failed"
  const calm = kind !== "failed"
  const Glyph = unavailable ? Slash : missing ? Question : throttled ? Clock : Warning

  // The pause ends by itself. The latest `onRetry` is kept beside the timer so
  // that a parent drawing again does not start the wait over.
  const wait = (api?.retryAfterMs ?? THROTTLE_WAIT_MS) + THROTTLE_GRACE_MS
  const retry = useRef(onRetry)
  useEffect(() => {
    retry.current = onRetry
  })
  useEffect(() => {
    if (!throttled) return
    const timer = setTimeout(() => retry.current?.(), wait)
    return () => clearTimeout(timer)
  }, [throttled, wait, error])

  return (
    <div
      role="alert"
      data-slot="error-state"
      data-kind={kind}
      className={cn(
        // A notice's anatomy (§14): the fence step, and the glyph on the
        // banner's own ground — the tinted plate it stood on was a fourth
        // object in the one hue, on a box that already had a wash and a rule.
        "flex min-w-0 items-start gap-2.5 rounded-lg border p-3",
        calm ? "border-hairline bg-card" : "border-rule-danger bg-wash-danger",
        className,
      )}
    >
      <Glyph
        aria-hidden
        className={cn(
          "mt-0.5 size-4 shrink-0",
          calm ? "text-muted-foreground" : "text-destructive",
        )}
      />
      <div className="min-w-0 flex-1 space-y-1">
        {/*
          "Something went wrong" is the fallback, not the headline. Where the
          server said what was being attempted and why it failed, that is what
          is shown — and the subsystem's own words go behind a disclosure so
          they are available without being the first thing read.
        */}
        <p className="text-body font-medium">
          {unavailable
            ? "Not available on this host"
            : api?.operation && api?.resource
              ? `Could not ${api.operation} ${api.resource}`
              : api?.message
                ? sentenceCase(api.message)
                : "Something went wrong"}
        </p>
        {(api?.operation || !api) && error.message && (
          <p className="text-xs leading-relaxed break-words text-muted-foreground">
            {asSentence(error.message)}
          </p>
        )}
        {api?.reason && (
          <p className="text-xs leading-relaxed break-words text-muted-foreground">
            {asSentence(api.reason)}
          </p>
        )}
        {missing && (
          <p className="text-xs leading-relaxed break-words text-muted-foreground">
            It may have been removed or renamed since this link was made.
          </p>
        )}
        {throttled && (
          <p className="text-xs leading-relaxed break-words text-muted-foreground">
            {onRetry
              ? `The server asked for a pause. This is asked again by itself in ${plural(Math.ceil(wait / 1000), "second")}.`
              : "The server asked for a pause. It answers again in a moment."}
          </p>
        )}
        <div className="flex flex-wrap items-center gap-3 pt-0.5">
          {api?.raw && api.raw !== api.message && (
            <details className="min-w-0 basis-full text-xs">
              <summary className="cursor-pointer text-muted-foreground hover:text-foreground">
                What the server said
              </summary>
              <pre className="mt-1 overflow-x-auto rounded-sm border border-hairline bg-surface-header/40 p-2 font-mono text-micro break-words whitespace-pre-wrap">
                {api.raw}
              </pre>
            </details>
          )}
          {onRetry && (throttled || (retryable ?? api?.retryable)) && (
            <Button size="xs" variant="outline" onClick={onRetry}>
              Try again
            </Button>
          )}
        </div>
      </div>
    </div>
  )
}

/**
 * A short banner for a fact the operator needs before acting on the page —
 * lockout protection, an editor that validates before it saves, a shell that
 * runs as root. Deliberately quieter than ErrorState: it is context, not a
 * failure, and a page that shouts everything says nothing.
 *
 * **The glyph is a mark, not a plate.** This was the last 28px tinted square
 * with an icon inside it left in the product: §14 took the prop off
 * `PanelHeader`, `Modal`, `SidePanel`, `Section`, `ChartPanel` and `StatTile`,
 * and a `Notice` kept one because its glyph is allowed — a severity is a thing
 * a shape can say. What is not allowed is the plate around it, and on a tinted
 * banner it was the fourth tinted object in a box that needed one: a wash, a
 * rule, a plate and the glyph on it, all the same hue, for a sentence saying
 * the certificate was fine. The glyph stands on the banner's own ground now,
 * at the size of the title's own line, and `rounded-lg` puts the banner on
 * §9's fence step beside `Group`, which is what it is, rather than on the
 * block step beside `Panel`, which it is not.
 */
export function Notice({
  title,
  icon: Icon,
  tone = "default",
  children,
  className,
}: {
  title: React.ReactNode
  icon?: React.ComponentType<{ className?: string }>
  tone?: Tone
  children?: React.ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        "flex min-w-0 items-start gap-2.5 rounded-lg border p-3",
        tone === "default" && "border-hairline bg-card",
        tone === "warning" && "border-rule-warning bg-wash-warning",
        tone === "danger" && "border-rule-danger bg-wash-danger",
        tone === "success" && "border-rule-success bg-wash-success",
        className,
      )}
    >
      {Icon && (
        <Icon
          aria-hidden
          className={cn(
            // No nudge: the title is `leading-tight`, a 16px line box, so a
            // 16px glyph at the top edge already shares its centre. The
            // `mt-0.5` this carried dropped the glyph two pixels below it.
            "size-4 shrink-0",
            tone === "default" && "text-muted-foreground",
            tone === "warning" && "text-warning",
            tone === "danger" && "text-destructive",
            tone === "success" && "text-success",
          )}
        />
      )}
      {/* The notice's width, not its words': content laid out across it —
          a title with a reading at its end, two lists side by side — had the
          width of its longest line and hugged the left of a wide box. */}
      <div className="min-w-0 flex-1 space-y-1">
        <p className="text-body leading-tight font-medium">{title}</p>
        {children && (
          <div className="text-xs leading-relaxed text-muted-foreground">{children}</div>
        )}
      </div>
    </div>
  )
}
