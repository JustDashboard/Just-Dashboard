"use client"

import { useEffect, useState } from "react"
import Link from "next/link"
import { ArrowRight, ArrowUpRight, Cross } from "@/components/icons"
import { relativeTime } from "@/lib/format"
import { keptDismissals, noticeHref, noticeTone, owedNotices } from "@/lib/hub-link"
import { cn } from "@/lib/utils"
import { useShellHub } from "@/hooks/use-hub-link"
import { LogoGlyph } from "@/components/logo"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

/** Notices read on this screen, so the card clears per browser as the update notice's does. */
const DISMISSED_KEY = "jd.hub.notices.dismissed"

function readDismissed(): string[] {
  if (typeof window === "undefined") return []
  try {
    const stored = JSON.parse(window.localStorage.getItem(DISMISSED_KEY) ?? "[]")
    return Array.isArray(stored) ? stored.filter((id) => typeof id === "string") : []
  } catch {
    return []
  }
}

function writeDismissed(ids: string[]) {
  try {
    window.localStorage.setItem(DISMISSED_KEY, JSON.stringify(ids))
  } catch {
    // Storage can be unavailable; the dismissal then lasts as long as the page.
  }
}

/**
 * What the hub has to say, where the operator will see it: above the account card, beside the
 * update notice and built the way it is (design report §4.7).
 *
 * It renders **nothing at all** with no hub link, and nothing with a hub that has nothing to say.
 * When it does speak it is the account card's container — tinted amber or red only for a notice
 * the hub marked as a warning or critical — holding the newest notice this browser has not
 * dismissed: its title, "From Just Hub" and when, the body as plain text (it came from another
 * machine, so it is never markup), and the hub's own page for it in a new tab. The rest are one
 * line under it that leads to Settings → Hub, where every notice is listed. Dismissing one is
 * remembered in this browser until the hub stops carrying it.
 */
export function HubNotice({ collapsed }: { collapsed: boolean }) {
  const hub = useShellHub()
  const [dismissed, setDismissed] = useState(readDismissed)

  // A notice the hub dropped is over; if it is raised again under the same id it is news again.
  // Only once the hub's list has been read, or the first paint would forget every dismissal.
  const notices = hub?.answered ? hub.notices : undefined
  const stale = notices ? dismissed.length - keptDismissals(dismissed, notices).length : 0
  useEffect(() => {
    if (notices && stale > 0) writeDismissed(keptDismissals(dismissed, notices))
  }, [notices, stale, dismissed])

  if (!hub) return null
  const owed = owedNotices(hub.notices, dismissed)
  const notice = owed[0]
  if (!notice) return null

  const dismiss = (id: string) => {
    const next = keptDismissals([...dismissed, id], hub.notices)
    setDismissed(next)
    writeDismissed(next)
  }

  const tone = noticeTone(notice.level)
  const href = noticeHref(notice.href, hub.url)

  if (collapsed) {
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <Link
            href="/dashboard/hub"
            aria-label={`From Just Hub: ${notice.title}`}
            className={cn(
              "flex size-8 items-center justify-center rounded-lg focus-ring transition-colors",
              tone === "default" && "bg-plot-brand text-brand hover:bg-plot-brand-hover",
              tone === "warning" && "bg-plot-warning text-warning",
              tone === "danger" && "bg-plot-danger text-destructive hover:bg-plot-danger-hover",
            )}
          >
            <LogoGlyph small className="h-4 w-auto" />
          </Link>
        </TooltipTrigger>
        <TooltipContent side="right">{notice.title}</TooltipContent>
      </Tooltip>
    )
  }

  return (
    <section
      aria-label="From Just Hub"
      data-slot="hub-notice"
      className={cn(
        "relative min-w-0 animate-rise overflow-hidden rounded-lg border",
        tone === "default" && "border-sidebar-border bg-card",
        tone === "warning" && "border-rule-warning bg-wash-warning",
        tone === "danger" && "border-rule-danger bg-wash-danger",
      )}
    >
      {/* Keyed on the notice so the next one arrives rather than repainting in place (§11). */}
      <div key={notice.id} className="space-y-2.5 p-3">
        <div className="flex min-w-0 items-start gap-2.5">
          <Mark tone={tone} />
          <div className="min-w-0 flex-1 space-y-1">
            <p className="line-clamp-2 text-xs leading-snug font-semibold break-words">
              {notice.title}
            </p>
            <p className="truncate text-hint leading-tight text-muted-foreground">
              From Just Hub · {relativeTime(notice.at)}
            </p>
          </div>
          <button
            type="button"
            aria-label={`Dismiss ${notice.title}`}
            onClick={() => dismiss(notice.id)}
            className="-mr-1 shrink-0 rounded-sm p-0.5 text-muted-foreground focus-ring transition-colors hover:text-foreground"
          >
            <Cross className="size-3" />
          </button>
        </div>
        {notice.body && (
          <p className="line-clamp-3 text-hint leading-snug break-words text-muted-foreground">
            {notice.body}
          </p>
        )}
        {(href || owed.length > 1) && (
          <div className="flex flex-col gap-1">
            {href && (
              <Button asChild size="sm" variant="ghost" className="w-full justify-between">
                <a href={href} target="_blank" rel="noopener noreferrer">
                  Open in Just Hub
                  <ArrowUpRight />
                </a>
              </Button>
            )}
            {owed.length > 1 && (
              <Button asChild size="sm" variant="ghost" className="w-full justify-between">
                <Link href="/dashboard/hub#notices">
                  {owed.length - 1} more from Just Hub
                  <ArrowRight />
                </Link>
              </Button>
            )}
          </div>
        )}
      </div>
    </section>
  )
}

/** The hub's J on a tile in the notice's tone, as the update notice draws the dashboard's. */
function Mark({ tone }: { tone: ReturnType<typeof noticeTone> }) {
  return (
    <span
      aria-hidden
      className={cn(
        "flex size-8 shrink-0 items-center justify-center rounded-lg border",
        tone === "default" && "border-rule-brand bg-wash-brand text-brand",
        tone === "warning" && "border-rule-warning bg-wash-warning text-warning",
        tone === "danger" && "border-rule-danger bg-wash-danger text-destructive",
      )}
    >
      <LogoGlyph small className="h-4 w-auto" />
    </span>
  )
}
