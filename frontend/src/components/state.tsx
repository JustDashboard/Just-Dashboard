"use client"

import { Inbox, LoaderCircle, Slash, Warning } from "@/components/icons"
import { ApiError } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { cn } from "@/lib/utils"
import type { Tone } from "@/components/tone"

export function Spinner({ className }: { className?: string }) {
  return <LoaderCircle className={cn("size-4 animate-spin", className)} />
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

/**
 * Renders a fetch failure.
 *
 * A module that is simply not present on this host (no Docker socket, no
 * systemd, no fail2ban) is shown as information rather than an error, because
 * on a given machine that is a normal state and not something broken. It is
 * the same block either way so the page's shape does not change with the
 * severity — only its colour and its title do.
 */
export function ErrorState({
  error,
  className,
  onRetry,
}: {
  error: Error
  className?: string
  /** Offered only when the server said the failure is worth trying again. */
  onRetry?: () => void
}) {
  const api = error instanceof ApiError ? error : undefined
  const unavailable =
    api?.code === "docker_unavailable" ||
    api?.code === "not_installed" ||
    api?.code === "no_proxy" ||
    api?.code === "terminal_disabled"

  return (
    <div
      role="alert"
      className={cn(
        // A notice's anatomy (§14): the fence step, and the glyph on the
        // banner's own ground — the tinted plate it stood on was a fourth
        // object in the one hue, on a box that already had a wash and a rule.
        "flex min-w-0 items-start gap-2.5 rounded-lg border p-3",
        unavailable ? "border-hairline bg-card" : "border-rule-danger bg-wash-danger",
        className,
      )}
    >
      {unavailable ? (
        <Slash aria-hidden className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      ) : (
        <Warning aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
      )}
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
              : (api?.message ?? "Something went wrong")}
        </p>
        {(api?.operation || !api) && (
          <p className="text-xs leading-relaxed break-words text-muted-foreground">
            {error.message}
          </p>
        )}
        {api?.reason && (
          <p className="text-xs leading-relaxed break-words text-muted-foreground">{api.reason}</p>
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
          {onRetry && api?.retryable && (
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
            // Nudged onto the title's baseline rather than the box's top edge:
            // 13px of text in an 18px line box puts its optical centre two
            // pixels below a 16px glyph's.
            "mt-0.5 size-4 shrink-0",
            tone === "default" && "text-muted-foreground",
            tone === "warning" && "text-warning",
            tone === "danger" && "text-destructive",
            tone === "success" && "text-success",
          )}
        />
      )}
      <div className="min-w-0 space-y-1">
        <p className="text-body leading-tight font-medium">{title}</p>
        {children && (
          <div className="text-xs leading-relaxed text-muted-foreground">{children}</div>
        )}
      </div>
    </div>
  )
}
