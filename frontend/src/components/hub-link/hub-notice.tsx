"use client"

import { useEffect, useState } from "react"
import Link from "next/link"
import { ArrowRight, ArrowUpRight, CrossCircle, Warning } from "@/components/icons"
import { duration, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
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
import { RAIL_NOTICE_LINK, RailNotice, type RailNoticeTone } from "@/components/rail-notice"

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
 * The level a warning or critical notice's glyph shows, in words for a screen reader: the glyph is
 * drawn and never read. Said before the hub's title, after who says it (§8.10).
 */
const LEVEL_WORD: Partial<Record<RailNoticeTone, string>> = {
  warning: "Warning",
  danger: "Critical",
}

/** A warning or critical notice says its level with its glyph; the rest carry the hub's J. */
function NoticeMark({ tone }: { tone: RailNoticeTone }) {
  if (tone === "danger") return <CrossCircle />
  if (tone === "warning") return <Warning />
  return <LogoGlyph small />
}

/**
 * When the hub raised it, in its largest unit ("2m", "3h"), the way a mail list puts the time at a
 * line's end: the words around it already say it was raised, so "ago" is room the name needs.
 */
function since(at: string): string {
  const then = Date.parse(at)
  if (Number.isNaN(then)) return ""
  const age = (Date.now() - then) / 1000
  return age < 45 ? "now" : duration(age).split(" ")[0]
}

/**
 * What the hub has to say, where the operator will see it: above the account card, beside the
 * update notice and built the way it is (design report §4.7), as Just Hub draws its own rail
 * notices.
 *
 * It renders **nothing at all** with no hub link, and nothing with a hub that has nothing to say.
 * When it does speak it is a compact card — tinted amber or red only for a notice the hub marked
 * as a warning or critical, whose glyph then says which — holding the newest notice this browser
 * has not dismissed. Its first line is who says it ("Just Hub (hub) says", hub-link-protocol
 * §8.10) with when at its end; then the title, the card's one strong line, and the body as plain
 * text (it came from another machine, so it is never markup and never reads as this dashboard's
 * own words); then the hub's own page for it in a new tab, named by its host — never while the
 * link is idle, when `shellHub` has dropped every such link. The rest are one line under it that
 * leads to Settings → Hub, where every notice is listed. Dismissing one is remembered in this
 * browser until the hub stops carrying it.
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
  const when = since(notice.at)
  const levelWord = LEVEL_WORD[tone]

  return (
    <RailNotice
      tone={tone}
      collapsed={collapsed}
      label={`${hubSays(hub.name)}: ${notice.title}`}
      href="/dashboard/hub"
      glyph={<LogoGlyph small className="h-4 w-auto" />}
      mark={<NoticeMark tone={tone} />}
      section="From Just Hub"
      slot={collapsed ? undefined : "hub-notice"}
      // Keyed on the notice so the next one arrives rather than repainting in place (§11).
      contentKey={notice.id}
      onDismiss={() => dismiss(notice.id)}
      dismissLabel={`Dismiss ${notice.title}`}
      head={
        <p className="flex min-w-0 items-baseline gap-2 text-hint text-muted-foreground">
          <HubSays name={hub.name} />
          {when && (
            <time
              dateTime={notice.at}
              title={timestamp(notice.at)}
              className="shrink-0 text-faint tabular-nums"
            >
              {when}
            </time>
          )}
        </p>
      }
    >
      <div className="space-y-1">
        <p className="line-clamp-2 text-body leading-snug font-semibold break-words text-foreground">
          {levelWord && <span className="sr-only">{levelWord}: </span>}
          {notice.title}
        </p>
        {notice.body && (
          <p className="line-clamp-3 text-hint leading-snug break-words text-muted-foreground">
            {notice.body}
          </p>
        )}
      </div>
      {(href || owed.length > 1) && (
        <div className="flex flex-col gap-1.5 pt-0.5">
          {href && (
            <a
              href={href}
              target="_blank"
              rel="noopener noreferrer"
              title={href}
              className={cn(RAIL_NOTICE_LINK, "items-start")}
            >
              {/* The host is the part that matters (§8.10), so it has a line of its own
                  rather than being what a narrow rail cuts. */}
              <span className="flex min-w-0 flex-col">
                <span className="flex items-center gap-1">
                  Open in Just Hub
                  <ArrowUpRight aria-hidden />
                </span>
                <span className="max-w-full truncate font-normal text-muted-foreground">
                  {hubHost(href)}
                </span>
              </span>
            </a>
          )}
          {owed.length > 1 && (
            <Link href="/dashboard/hub#notices" className={RAIL_NOTICE_LINK}>
              {owed.length - 1} more from Just Hub
              <ArrowRight aria-hidden />
            </Link>
          )}
        </div>
      )}
    </RailNotice>
  )
}

/**
 * "Just Hub (hub) says", kept on one line — what is left of it after the time at its end — with
 * only the hub's name clipped when the rail is too narrow for it: the name is the hub's own and up
 * to 64 characters, and "says" is what marks the words below as the hub's (§8.10), so it is the
 * part that must stay. The name may take the line less the room the words around it need:
 * "Just Hub () says" measures 77px at the hint's 12px, so 6.75em holds it with a few pixels to
 * spare (16ch, 94px, cut a four-letter name for room it did not need). Its whole text is its
 * title. Inline boxes rather than flex, so the line still reads, and copies, as one sentence:
 * flex items are blocks, and the text would come out as three lines.
 */
function HubSays({ name }: { name: string }) {
  if (!name) return <span className="min-w-0 flex-1 truncate">{hubSays(name)}</span>
  return (
    <span className="min-w-0 flex-1 overflow-hidden whitespace-nowrap">
      Just Hub (
      <span className="inline-block max-w-[calc(100%-6.75em)] truncate align-bottom" title={name}>
        {name}
      </span>
      ) says
    </span>
  )
}
