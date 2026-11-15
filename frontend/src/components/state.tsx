"use client"

import { useEffect, useRef, useState } from "react"
import { Clock, Inbox, LockClosed, Question, Warning } from "@/components/icons"
import { ApiError } from "@/lib/api"
import { asSentence, plural, sentenceCase } from "@/lib/format"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import type { Tone } from "@/components/tone"
import { absentFromError } from "@/components/missing-thing-model"
// A cycle by design: `MissingOrError` there draws this, and this draws the
// absence through `MissingScreen`. Both are read only while rendering, never
// while the modules load, so either may load first.
import { MissingScreen } from "@/components/missing-thing"

type Glyph = React.ComponentType<{ className?: string }>

/**
 * "Nothing here" for a region that is already inside something — a rail's list
 * of tables, a panel body under a filter, the saved-queries column.
 *
 * `EmptyState` would draw a dashed frame and an icon plot inside a box that
 * already has a frame, so those places had each written their own sentence
 * instead: eleven of them, at four different sizes and five different paddings.
 * One sentence, centred in the space it was given.
 *
 * - `dense` is the size inside a rail or a picker, where the regular note's
 *   48px of air above and below is most of what the rail shows: the hint
 *   step, a third of the padding. The rails had five paddings between them.
 * - `action` is the one way out, under the sentence: "Clear the search" for
 *   a filter that matched nothing. A no-match line with no Clear left the
 *   reader to find the field and empty it by hand.
 * - `as` is the element: `li` for a note that stands in a list's place
 *   inside the `<ul>`, `div` where the note sits in a block of its own.
 */
export function EmptyNote({
  children,
  action,
  dense,
  as: Element = "p",
  className,
  ...props
}: React.HTMLAttributes<HTMLElement> & {
  action?: { label: string; onClick: () => void }
  dense?: boolean
  as?: "p" | "li" | "div"
}) {
  return (
    <Element
      data-slot="empty-note"
      data-dense={dense ? "" : undefined}
      className={cn(
        "text-center text-muted-foreground",
        dense ? "px-2 py-3 text-hint" : "px-3 py-6 text-body",
        className,
      )}
      {...props}
    >
      {children}
      {action && (
        <span className={cn("flex justify-center", dense ? "mt-1.5" : "mt-2")}>
          <Button size="xs" variant="ghost" onClick={action.onClick}>
            {action.label}
          </Button>
        </span>
      )}
    </Element>
  )
}

/**
 * A list, a page or a section with nothing in it yet: the glyph, a title, a
 * sentence and the verb that fills it.
 *
 * The glyph is drawn bare, at the height of the title's line: §14 took the
 * 40px plate it stood on off every other surface, and here it was the last
 * one, a box drawn inside the dashed box (PRM-21).
 */
export function EmptyState({
  title,
  description,
  icon: Icon = Inbox,
  mark,
  action,
  className,
  ...props
}: Omit<React.ComponentProps<"div">, "title"> & {
  title: string
  description?: React.ReactNode
  icon?: Glyph
  /**
   * What the list would hold, drawn as itself in place of the glyph — the
   * products a list of channels or credentials is built for, as
   * `ProductLogos` (§14). An empty list that shows the Discord, Slack and
   * Telegram marks says what goes in it before the sentence under it does.
   */
  mark?: React.ReactNode
  action?: React.ReactNode
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
      {...props}
    >
      {mark ?? <Icon aria-hidden className="size-5 shrink-0 text-muted-foreground" />}
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

/**
 * A section an account without the administrator role may not open: one
 * phrase, "<What> needs an administrator", and one glyph, the lock. The pages
 * said it three ways ("requires the admin capability", "needs the admin
 * capability", "needs an administrator") under six glyphs.
 *
 * It is drawn under the page's own identity header, so the page keeps its
 * shape for the reader who cannot use it. `why` is one sentence where the
 * closed door is not obvious; `action` is the way out.
 */
export function NeedsAdmin({
  what,
  why,
  icon = LockClosed,
  action,
  className,
  ...props
}: Omit<React.ComponentProps<"div">, "title"> & {
  /** What is closed, as the sentence's subject: "Packet captures". */
  what: string
  why?: React.ReactNode
  icon?: Glyph
  action?: React.ReactNode
}) {
  return (
    <EmptyState
      data-slot="needs-admin"
      icon={icon}
      title={`${what} ${needsVerb(what)} an administrator`}
      description={why}
      action={action}
      className={className}
      {...props}
    />
  )
}

/**
 * "need" after a plural subject the caller names ("Server diagnostics"), and
 * "needs" after anything else. A subject is plural when its last word ends on
 * an s that is not a double s ("Access" stays one thing).
 */
export function needsVerb(what: string): "need" | "needs" {
  const last = what.trim().split(/\s+/).pop() ?? ""
  return /[^s]s$/i.test(last) && !/(ss|us|is)$/i.test(last) ? "need" : "needs"
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
 * systemd, a program not installed, no proxy) is not a failure: it is drawn as
 * the designed absence, `MissingThing` at block size, with the step that
 * fixes it (`absentFromError`). It was a calm box reading "Not available on
 * this host", which said less than the screen written for it.
 *
 * A read names itself with `what`: the headline is "Could not read <what>"
 * where the server did not say what it was attempting, the server's words go
 * under it as a sentence, and Try again is offered whether or not the server
 * marked the failure worth repeating — a reader looking at a hole in the page
 * has no other way to fill it than to reload everything around it. `every`
 * says how often the page asks again by itself. A press stays pending until
 * the read it started settles: a new failure is a new error, and a success
 * takes this block away.
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
  what,
  every,
  ...props
}: Omit<React.ComponentProps<"div">, "children"> & {
  error: Error
  /**
   * Offered when the server said the failure is worth trying again, when the
   * read names itself with `what`, or when `retryable` says so.
   */
  onRetry?: () => void
  /** Safe read operations can explicitly offer another attempt after transport failure. */
  retryable?: boolean
  /** What the block would have shown, for the headline: "the busiest statements". */
  what?: string
  /** How often the page asks again by itself: "30 seconds". */
  every?: string
}) {
  const api = error instanceof ApiError ? error : undefined
  const absent = absentFromError(error)
  const missing = !absent && api?.status === 404
  const throttled = !absent && api?.status === 429
  const kind = missing ? "missing" : throttled ? "throttled" : "failed"
  const calm = kind !== "failed"
  const Glyph = missing ? Question : throttled ? Clock : Warning

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

  const [asked, setAsked] = useState<Error>()
  const offered =
    onRetry !== undefined &&
    (throttled || (retryable ?? (what !== undefined || api?.retryable === true)))
  const again = offered && (
    <Button
      size={absent ? "sm" : "xs"}
      variant="outline"
      pending={asked === error}
      onClick={() => {
        setAsked(error)
        onRetry()
      }}
    >
      Try again
    </Button>
  )

  if (absent) {
    return <MissingScreen missing={absent} size="block" retry={again} className={className} />
  }

  const headline =
    api?.operation && api?.resource
      ? `Could not ${api.operation} ${api.resource}`
      : what
        ? `Could not read ${what}`
        : api?.message
          ? sentenceCase(api.message)
          : "Something went wrong"

  return (
    <div
      role="alert"
      data-slot="error-state"
      data-kind={kind}
      {...props}
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
        <p className="text-body font-medium">{headline}</p>
        {(api?.operation || !api || what) && error.message && (
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
          {again}
          {again && every && !throttled && (
            <span className="text-hint text-muted-foreground">
              It is asked again by itself every {every}.
            </span>
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
 *
 * `live` makes the banner its own live region — `status` for news that can
 * wait for a pause, `alert` for a failure the reader has to hear now — so a
 * caller no longer wraps it in a `<div role="alert">` or puts a role inside
 * its title. Other props reach the root, which carries `data-slot="notice"`.
 */
export function Notice({
  title,
  icon: Icon,
  tone = "default",
  live,
  children,
  className,
  ...props
}: Omit<React.ComponentProps<"div">, "title"> & {
  title: React.ReactNode
  icon?: Glyph
  tone?: Tone
  live?: "status" | "alert"
}) {
  return (
    <div
      role={live}
      data-slot="notice"
      data-tone={tone}
      {...props}
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
