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
 */
export function LoadingPanel({ rows = 6, className }: { rows?: number; className?: string }) {
  return (
    <div className={cn("min-w-0 overflow-hidden rounded-xl border bg-card", className)}>
      <div className="flex min-h-12 items-center border-b border-hairline px-5 py-3">
        <Skeleton className="h-4 w-40" />
      </div>
      <div className="divide-y divide-hairline">
        {Array.from({ length: rows }).map((_, i) => (
          <div key={i} className="flex items-center gap-3 px-5 py-3.5">
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
  action,
  className,
}: {
  title: string
  description?: React.ReactNode
  icon?: React.ComponentType<{ className?: string }>
  action?: React.ReactNode
  className?: string
}) {
  return (
    <div
      data-slot="empty-state"
      className={cn(
        "flex min-w-0 flex-col items-center justify-center gap-3 rounded-xl border border-dashed px-6 py-12 text-center",
        className,
      )}
    >
      <span className="flex size-10 items-center justify-center rounded-xl border border-hairline bg-surface-header text-muted-foreground">
        <Icon className="size-4.5" />
      </span>
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
        "flex min-w-0 items-start gap-3 rounded-xl border p-4",
        unavailable ? "border-hairline bg-card" : "border-rule-danger bg-wash-danger",
        className,
      )}
    >
      <span
        className={cn(
          "flex size-8 shrink-0 items-center justify-center rounded-lg",
          unavailable ? "bg-muted text-muted-foreground" : "bg-plot-danger text-destructive",
        )}
      >
        {unavailable ? <Slash className="size-4" /> : <Warning className="size-4" />}
      </span>
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
        "flex min-w-0 items-start gap-3 rounded-xl border p-3.5",
        tone === "default" && "border-hairline bg-card",
        tone === "warning" && "border-rule-warning bg-wash-warning",
        tone === "danger" && "border-rule-danger bg-wash-danger",
        tone === "success" && "border-rule-success bg-wash-success",
        className,
      )}
    >
      {Icon && (
        <span
          className={cn(
            "flex size-7 shrink-0 items-center justify-center rounded-lg",
            tone === "default" && "bg-muted text-muted-foreground",
            tone === "warning" && "bg-plot-warning text-warning",
            tone === "danger" && "bg-plot-danger text-destructive",
            tone === "success" && "bg-plot-success text-success",
          )}
        >
          <Icon className="size-3.5" />
        </span>
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
