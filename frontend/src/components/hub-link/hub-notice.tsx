"use client"

import { useEffect, useState } from "react"
import Link from "next/link"
import { ArrowRight, ArrowUpRight } from "@/components/icons"
import { relativeTime } from "@/lib/format"
import {
  hubHost,
  hubSays,
  keptDismissals,
  noticeHref,
  noticeTone,
  owedNotices,
} from "@/lib/hub-link"
import { useShellHub } from "@/hooks/use-hub-link"
import { LogoGlyph } from "@/components/logo"
import { RailNotice, RailNoticeHead, type RailNoticeTone } from "@/components/rail-notice"
import { Button } from "@/components/ui/button"

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
 * dismissed: who says it ("Just Hub (hub) says", hub-link-protocol §8.10) and when, then its
 * title and body as plain text (it came from another machine, so it is never markup and never
 * reads as this dashboard's own words), and the hub's own page for it in a new tab, named by its
 * host — never while the link is idle, when `shellHub` has dropped every such link. The rest are
 * one line under it that leads to Settings → Hub, where every notice is listed. Dismissing one is
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

  const level = noticeTone(notice.level)
  const tone: RailNoticeTone = level === "default" ? "brand" : level
  const href = noticeHref(notice.href, hub.url)

  return (
    <RailNotice
      tone={tone}
      collapsed={collapsed}
      label={`${hubSays(hub.name)}: ${notice.title}`}
      href="/dashboard/hub"
      glyph={<LogoGlyph small className="h-4 w-auto" />}
      section="From Just Hub"
      slot={collapsed ? undefined : "hub-notice"}
      // Keyed on the notice so the next one arrives rather than repainting in place (§11).
      contentKey={notice.id}
    >
      <RailNoticeHead
        tone={tone}
        mark={<LogoGlyph small className="h-4 w-auto" />}
        onDismiss={() => dismiss(notice.id)}
        dismissLabel={`Dismiss ${notice.title}`}
      >
        <p className="text-hint leading-tight text-muted-foreground">
          <HubSays name={hub.name} />{" "}
          <span className="whitespace-nowrap">· {relativeTime(notice.at)}</span>
        </p>
        <p className="line-clamp-2 text-xs leading-snug font-semibold break-words">
          {notice.title}
        </p>
      </RailNoticeHead>
      {notice.body && (
        <p className="line-clamp-3 text-hint leading-snug break-words text-muted-foreground">
          {notice.body}
        </p>
      )}
      {(href || owed.length > 1) && (
        <div className="flex flex-col gap-1">
          {href && (
            <Button
              asChild
              size="sm"
              variant="ghost"
              className="h-auto min-h-8 w-full justify-between py-1.5"
            >
              <a href={href} target="_blank" rel="noopener noreferrer" title={href}>
                {/* The host is the part that matters (§8.10), so it has a line of its own
                    rather than being what a narrow rail cuts. */}
                <span className="flex min-w-0 flex-col items-start text-left">
                  <span>Open in Just Hub</span>
                  <span className="max-w-full truncate text-hint font-normal text-muted-foreground">
                    {hubHost(href)}
                  </span>
                </span>
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
    </RailNotice>
  )
}

/**
 * "Just Hub (hub) says", kept on one line with only the hub's name clipped when the rail is too
 * narrow for it: the name is the hub's own and up to 64 characters, and "says" is what marks the
 * words below as the hub's (§8.10), so it is the part that must stay. The name may take the line
 * less the room the words around it need (16ch holds "Just Hub () says"), and its whole text is
 * its title. Inline boxes rather than flex, so the line still reads, and copies, as one sentence.
 */
function HubSays({ name }: { name: string }) {
  if (!name) return <span>{hubSays(name)}</span>
  return (
    <span className="whitespace-nowrap">
      Just Hub (
      <span className="inline-block max-w-[calc(100%-16ch)] truncate align-bottom" title={name}>
        {name}
      </span>
      ) says
    </span>
  )
}
